package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"io"
)

const (
	magic0         = 0x4F
	magic1         = 0x44
	version        = 0x01
	headerSize     = 3
	nonceSize      = 12
	tagSize        = 16
	sentinelSize   = 4
	hmacSize       = 32
	maxChunkSize   = 64 * 1024
	lengthPrefixSz = 4
)

var (
	ErrInvalidKey = errors.New("invalid encryption key")
)

type Encryptor struct {
	key []byte
}

func NewEncryptor(key []byte) (*Encryptor, error) {
	if len(key) != 32 {
		return nil, errors.Join(ErrInvalidKey, errors.New("key must be 32 bytes"))
	}
	return &Encryptor{key: append([]byte(nil), key...)}, nil
}

type encryptWriter struct {
	w         io.Writer
	aead      cipher.AEAD
	mac       hash.Hash
	buf       []byte
	closed    bool
	headerWrt bool
}

func (e *Encryptor) EncryptWriter(w io.Writer) io.WriteCloser {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return &errorWriter{err: err}
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return &errorWriter{err: err}
	}
	return &encryptWriter{
		w:    w,
		aead: aead,
		mac:  hmac.New(sha256.New, e.key),
		buf:  make([]byte, 0, maxChunkSize),
	}
}

type errorWriter struct {
	err error
}

func (ew *errorWriter) Write(p []byte) (int, error) {
	return 0, ew.err
}

func (ew *errorWriter) Close() error {
	return ew.err
}

func (ew *encryptWriter) writeHeader() error {
	if ew.headerWrt {
		return nil
	}
	header := []byte{magic0, magic1, version}
	if _, err := ew.w.Write(header); err != nil {
		return err
	}
	ew.headerWrt = true
	return nil
}

func (ew *encryptWriter) writeChunk(plaintext []byte) error {
	if len(plaintext) == 0 {
		return nil
	}

	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}

	ciphertext := ew.aead.Seal(nil, nonce, plaintext, nil)

	chunkLen := nonceSize + len(ciphertext)
	lengthPrefix := make([]byte, lengthPrefixSz)
	binary.BigEndian.PutUint32(lengthPrefix, uint32(chunkLen))

	if _, err := ew.w.Write(lengthPrefix); err != nil {
		return err
	}
	ew.mac.Write(lengthPrefix)

	if _, err := ew.w.Write(nonce); err != nil {
		return err
	}
	ew.mac.Write(nonce)

	if _, err := ew.w.Write(ciphertext); err != nil {
		return err
	}
	ew.mac.Write(ciphertext)

	return nil
}

func (ew *encryptWriter) Write(p []byte) (int, error) {
	if ew.closed {
		return 0, io.ErrClosedPipe
	}
	if err := ew.writeHeader(); err != nil {
		return 0, err
	}

	total := len(p)
	for len(p) > 0 {
		space := maxChunkSize - len(ew.buf)
		if space > len(p) {
			space = len(p)
		}
		ew.buf = append(ew.buf, p[:space]...)
		p = p[space:]

		if len(ew.buf) == maxChunkSize {
			if err := ew.writeChunk(ew.buf); err != nil {
				return 0, err
			}
			ew.buf = ew.buf[:0]
		}
	}
	return total, nil
}

func (ew *encryptWriter) Close() error {
	if ew.closed {
		return nil
	}
	ew.closed = true

	if err := ew.writeHeader(); err != nil {
		return err
	}

	if len(ew.buf) > 0 {
		if err := ew.writeChunk(ew.buf); err != nil {
			return err
		}
	}

	sentinel := make([]byte, sentinelSize)
	if _, err := ew.w.Write(sentinel); err != nil {
		return err
	}

	sum := ew.mac.Sum(nil)
	if _, err := ew.w.Write(sum[:hmacSize]); err != nil {
		return err
	}

	return nil
}

type decryptReader struct {
	r      io.Reader
	aead   cipher.AEAD
	mac    hash.Hash
	key    []byte
	inited bool
	done   bool
	plain  []byte
	err    error
}

func DecryptReader(r io.Reader, key []byte) (io.Reader, error) {
	if len(key) != 32 {
		return nil, errors.Join(ErrInvalidKey, errors.New("key must be 32 bytes"))
	}
	return &decryptReader{
		r:   r,
		key: append([]byte(nil), key...),
		mac: hmac.New(sha256.New, key),
	}, nil
}

func (dr *decryptReader) init() error {
	if dr.inited {
		return dr.err
	}
	dr.inited = true

	header := make([]byte, headerSize)
	if _, err := io.ReadFull(dr.r, header); err != nil {
		dr.err = err
		return dr.err
	}

	if header[0] != magic0 || header[1] != magic1 {
		dr.err = errors.New("invalid header")
		return dr.err
	}
	if header[2] != version {
		dr.err = errors.New("unsupported version")
		return dr.err
	}

	block, err := aes.NewCipher(dr.key)
	if err != nil {
		dr.err = err
		return dr.err
	}
	dr.aead, err = cipher.NewGCM(block)
	if err != nil {
		dr.err = err
		return dr.err
	}

	return nil
}

func (dr *decryptReader) readNextChunk() error {
	lengthPrefix := make([]byte, lengthPrefixSz)
	if _, err := io.ReadFull(dr.r, lengthPrefix); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return io.ErrUnexpectedEOF
		}
		return err
	}

	chunkLen := binary.BigEndian.Uint32(lengthPrefix)

	if chunkLen == 0 {
		if err := dr.verifyHMAC(); err != nil {
			return err
		}
		dr.done = true
		return io.EOF
	}

	dr.mac.Write(lengthPrefix)

	chunk := make([]byte, chunkLen)
	if _, err := io.ReadFull(dr.r, chunk); err != nil {
		return err
	}
	dr.mac.Write(chunk)

	nonce := chunk[:nonceSize]
	ciphertext := chunk[nonceSize:]

	plaintext, err := dr.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return err
	}

	dr.plain = plaintext
	return nil
}

func (dr *decryptReader) verifyHMAC() error {
	expectedMAC := make([]byte, hmacSize)
	if _, err := io.ReadFull(dr.r, expectedMAC); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return io.ErrUnexpectedEOF
		}
		return err
	}

	if !hmac.Equal(dr.mac.Sum(nil), expectedMAC) {
		return errors.New("integrity verification failed")
	}
	return nil
}

func (dr *decryptReader) Read(p []byte) (int, error) {
	if err := dr.init(); err != nil {
		return 0, err
	}
	if dr.done {
		return 0, io.EOF
	}

	for len(dr.plain) == 0 {
		err := dr.readNextChunk()
		if err == io.EOF {
			return 0, io.EOF
		}
		if err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return 0, io.ErrUnexpectedEOF
			}
			return 0, err
		}
	}

	n := copy(p, dr.plain)
	dr.plain = dr.plain[n:]
	return n, nil
}
