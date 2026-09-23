package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
)

const (
	magic0        = 0x4F
	magic1        = 0x44
	formatVersion = 0x01

	chunkSize = 64 * 1024
	nonceSize = 12
	tagSize   = 16
	hmacSize  = sha256.Size

	headerSize = 3
)

// ErrInvalidKey is returned when a key is not a 32-byte AES-256 key.
var ErrInvalidKey = errors.New("invalid encryption key")

// Encryptor encrypts streams with AES-256-GCM.
type Encryptor struct {
	key  []byte
	aead cipher.AEAD
}

// NewEncryptor returns an encryptor for a 32-byte key.
func NewEncryptor(key []byte) (*Encryptor, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: AES-256-GCM requires a 32-byte key, got %d", ErrInvalidKey, len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidKey, err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}

	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)

	return &Encryptor{key: keyCopy, aead: aead}, nil
}

// EncryptWriter wraps w with a streaming AES-256-GCM writer.
// The stream is: 3-byte header, length-prefixed chunks, a zero sentinel, then HMAC-SHA256.
func (e *Encryptor) EncryptWriter(w io.Writer) io.WriteCloser {
	return &encryptWriter{
		w:      w,
		aead:   e.aead,
		mac:    hmac.New(sha256.New, e.key),
		buf:    make([]byte, 0, chunkSize),
		nonces: make(map[[nonceSize]byte]struct{}),
	}
}

type encryptWriter struct {
	w          io.Writer
	aead       cipher.AEAD
	mac        hash.Hash
	buf        []byte
	nonces     map[[nonceSize]byte]struct{}
	chunks     int
	headerDone bool
	closed     bool
}

func (e *encryptWriter) Write(p []byte) (int, error) {
	if e.closed {
		return 0, io.ErrClosedPipe
	}

	if err := e.writeHeader(); err != nil {
		return 0, err
	}

	written := 0
	for len(p) > 0 {
		n := chunkSize - len(e.buf)
		if n > len(p) {
			n = len(p)
		}
		e.buf = append(e.buf, p[:n]...)
		p = p[n:]
		written += n

		if len(e.buf) == chunkSize {
			if err := e.flush(false); err != nil {
				return written, err
			}
		}
	}

	return written, nil
}

func (e *encryptWriter) writeHeader() error {
	if e.headerDone {
		return nil
	}

	if _, err := e.w.Write([]byte{magic0, magic1, formatVersion}); err != nil {
		return err
	}

	e.headerDone = true
	return nil
}

func (e *encryptWriter) nextNonce() ([]byte, error) {
	var nonce [nonceSize]byte
	for {
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, fmt.Errorf("read nonce: %w", err)
		}
		if _, exists := e.nonces[nonce]; exists {
			continue
		}
		e.nonces[nonce] = struct{}{}
		out := make([]byte, nonceSize)
		copy(out, nonce[:])
		return out, nil
	}
}

func (e *encryptWriter) flush(allowEmpty bool) error {
	if len(e.buf) == 0 && !allowEmpty {
		return nil
	}

	nonce, err := e.nextNonce()
	if err != nil {
		return err
	}

	sealed := e.aead.Seal(nil, nonce, e.buf, nil)
	payloadLen := nonceSize + len(sealed)
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(payloadLen))

	chunk := make([]byte, 0, 4+payloadLen)
	chunk = append(chunk, prefix[:]...)
	chunk = append(chunk, nonce...)
	chunk = append(chunk, sealed...)

	e.mac.Write(chunk)
	if _, err := e.w.Write(chunk); err != nil {
		return err
	}

	e.buf = e.buf[:0]
	e.chunks++
	return nil
}

// Close finishes the stream. It is idempotent.
func (e *encryptWriter) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true

	if err := e.writeHeader(); err != nil {
		return err
	}

	// A stream with no plaintext still gets one empty chunk so the nonce
	// makes two encryptions of the same (empty) plaintext differ.
	if len(e.buf) > 0 || e.chunks == 0 {
		if err := e.flush(true); err != nil {
			return err
		}
	}

	var sentinel [4]byte
	if _, err := e.w.Write(sentinel[:]); err != nil {
		return err
	}

	sum := e.mac.Sum(nil)
	if _, err := e.w.Write(sum); err != nil {
		return err
	}

	return nil
}

// DecryptReader returns a reader that reverses EncryptWriter.
// The returned reader is lazy: header, version, integrity, and truncation
// errors are reported from Read.
func DecryptReader(r io.Reader, key []byte) (io.Reader, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: AES-256-GCM requires a 32-byte key, got %d", ErrInvalidKey, len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidKey, err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}

	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)

	return &decryptReader{
		r:    r,
		aead: aead,
		mac:  hmac.New(sha256.New, keyCopy),
	}, nil
}

type decryptReader struct {
	r        io.Reader
	aead     cipher.AEAD
	mac      hash.Hash
	plain    []byte
	off      int
	initDone bool
	eof      bool
	err      error
}

func (d *decryptReader) Read(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}

	if !d.initDone {
		if err := d.init(); err != nil {
			d.err = err
			return 0, err
		}
		d.initDone = true
	}

	for d.off >= len(d.plain) {
		if d.eof {
			return 0, io.EOF
		}
		if err := d.nextChunk(); err != nil {
			if errors.Is(err, io.EOF) {
				d.eof = true
				return 0, io.EOF
			}
			d.err = err
			return 0, err
		}
	}

	n := copy(p, d.plain[d.off:])
	d.off += n
	return n, nil
}

func (d *decryptReader) init() error {
	header := make([]byte, headerSize)
	if err := readFull(d.r, header); err != nil {
		return err
	}

	if header[0] != magic0 || header[1] != magic1 {
		return fmt.Errorf("invalid header")
	}

	if header[2] != formatVersion {
		return fmt.Errorf("unsupported version %d", header[2])
	}

	return nil
}

func (d *decryptReader) nextChunk() error {
	var prefix [4]byte
	if err := readFull(d.r, prefix[:]); err != nil {
		return err
	}

	n := binary.BigEndian.Uint32(prefix[:])
	if n == 0 {
		mac := make([]byte, hmacSize)
		if err := readFull(d.r, mac); err != nil {
			return err
		}
		if !hmac.Equal(d.mac.Sum(nil), mac) {
			return fmt.Errorf("integrity check failed")
		}
		return io.EOF
	}

	const maxPayload = chunkSize + nonceSize + tagSize
	if n < nonceSize+tagSize || n > maxPayload {
		return fmt.Errorf("truncated data: invalid chunk length %d", n)
	}

	payload := make([]byte, n)
	if err := readFull(d.r, payload); err != nil {
		return err
	}

	// Length prefix and payload sit between the header and the sentinel.
	d.mac.Write(prefix[:])
	d.mac.Write(payload)

	nonce := payload[:nonceSize]
	sealed := payload[nonceSize:]
	plain, err := d.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return fmt.Errorf("decryption failed: %w", err)
	}

	d.plain = plain
	d.off = 0
	return nil
}

func readFull(r io.Reader, buf []byte) error {
	_, err := io.ReadFull(r, buf)
	if err == nil {
		return nil
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("truncated data: %w", err)
	}
	return err
}
