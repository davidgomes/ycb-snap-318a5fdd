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

// Stream layout:
//
//	header    magic (0x4F 0x44) + version (0x01)
//	chunk*    4-byte big-endian length of (nonce + ciphertext + tag), 12-byte nonce, ciphertext + 16-byte GCM tag
//	sentinel  4 zero bytes
//	hmac      HMAC-SHA256 of every byte between the header and the sentinel, keyed with the encryption key
const (
	KeySize   = 32
	ChunkSize = 64 * 1024

	version        byte = 0x01
	headerSize          = 3
	lengthSize          = 4
	nonceSize           = 12
	tagSize             = 16
	macSize             = sha256.Size
	minChunkLength      = nonceSize + tagSize
	maxChunkLength      = nonceSize + ChunkSize + tagSize
)

var magic = [2]byte{0x4F, 0x44}

var (
	ErrInvalidKey         = errors.New("invalid encryption key")
	ErrInvalidHeader      = errors.New("invalid header")
	ErrUnsupportedVersion = errors.New("unsupported version")
	ErrIntegrity          = errors.New("integrity check failed")
	ErrTruncated          = errors.New("truncated stream")

	errWriterClosed = errors.New("encryption: write to closed writer")
)

type Encryptor struct {
	key  []byte
	aead cipher.AEAD
}

// Create an AES-256-GCM encryptor, the key must be exactly 32 bytes.
func NewEncryptor(key []byte) (*Encryptor, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("encryption: %w: expected %d bytes, got %d", ErrInvalidKey, KeySize, len(key))
	}

	k := make([]byte, KeySize)
	copy(k, key)

	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, fmt.Errorf("encryption: %w: %v", ErrInvalidKey, err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("encryption: could not create GCM cipher: %w", err)
	}

	return &Encryptor{key: k, aead: aead}, nil
}

// Wrap w so that everything written is encrypted in chunks of up to 64KB.
// Close flushes the last chunk and writes the trailer, it does not close w.
func (e *Encryptor) EncryptWriter(w io.Writer) io.WriteCloser {
	return &encryptWriter{
		w:    w,
		aead: e.aead,
		mac:  hmac.New(sha256.New, e.key),
		buf:  make([]byte, 0, ChunkSize),
		out:  make([]byte, 0, lengthSize+maxChunkLength),
	}
}

type encryptWriter struct {
	w             io.Writer
	aead          cipher.AEAD
	mac           hash.Hash
	buf           []byte
	out           []byte
	headerWritten bool
	closed        bool
	err           error
}

func (ew *encryptWriter) Write(p []byte) (int, error) {
	if ew.closed {
		return 0, errWriterClosed
	}

	if ew.err != nil {
		return 0, ew.err
	}

	n := 0
	for len(p) > 0 {
		if len(ew.buf) == ChunkSize {
			if err := ew.flush(); err != nil {
				ew.err = err
				return n, err
			}
		}

		c := copy(ew.buf[len(ew.buf):ChunkSize], p)
		ew.buf = ew.buf[:len(ew.buf)+c]
		p = p[c:]
		n += c
	}

	return n, nil
}

func (ew *encryptWriter) Close() error {
	if ew.closed {
		return ew.err
	}

	ew.closed = true

	if ew.err != nil {
		return ew.err
	}

	if len(ew.buf) > 0 {
		if err := ew.flush(); err != nil {
			ew.err = err
			return err
		}
	}

	if err := ew.writeHeader(); err != nil {
		ew.err = err
		return err
	}

	trailer := make([]byte, lengthSize, lengthSize+macSize)
	trailer = ew.mac.Sum(trailer)

	if _, err := ew.w.Write(trailer); err != nil {
		ew.err = fmt.Errorf("encryption: could not write trailer: %w", err)
		return ew.err
	}

	return nil
}

func (ew *encryptWriter) writeHeader() error {
	if ew.headerWritten {
		return nil
	}

	if _, err := ew.w.Write([]byte{magic[0], magic[1], version}); err != nil {
		return fmt.Errorf("encryption: could not write header: %w", err)
	}

	ew.headerWritten = true
	return nil
}

// Encrypt the buffered plaintext as one chunk and write it out.
func (ew *encryptWriter) flush() error {
	if err := ew.writeHeader(); err != nil {
		return err
	}

	var nonce [nonceSize]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("encryption: could not generate nonce: %w", err)
	}

	chunk := append(ew.out[:lengthSize], nonce[:]...)
	chunk = ew.aead.Seal(chunk, nonce[:], ew.buf, nil)
	binary.BigEndian.PutUint32(chunk[:lengthSize], uint32(len(chunk)-lengthSize))

	ew.mac.Write(chunk)

	if _, err := ew.w.Write(chunk); err != nil {
		return fmt.Errorf("encryption: could not write chunk: %w", err)
	}

	ew.buf = ew.buf[:0]
	return nil
}

// Return a reader that decrypts a stream produced by EncryptWriter.
// The stream is only consumed on Read, so header, integrity and truncation errors are reported by Read.
// Plaintext of authenticated chunks is returned before the trailing HMAC is verified,
// callers must treat any error other than io.EOF as a failed decryption.
func DecryptReader(r io.Reader, key []byte) (io.Reader, error) {
	e, err := NewEncryptor(key)
	if err != nil {
		return nil, err
	}

	return &decryptReader{
		r:    r,
		aead: e.aead,
		mac:  hmac.New(sha256.New, e.key),
	}, nil
}

type decryptReader struct {
	r           io.Reader
	aead        cipher.AEAD
	mac         hash.Hash
	chunk       []byte
	plain       []byte
	initialized bool
	chunks      int
	err         error
}

func (dr *decryptReader) Read(p []byte) (int, error) {
	for len(dr.plain) == 0 {
		if dr.err != nil {
			return 0, dr.err
		}

		dr.err = dr.next()
	}

	n := copy(p, dr.plain)
	dr.plain = dr.plain[n:]
	return n, nil
}

// Decrypt the next chunk into dr.plain, returns io.EOF once the trailer has been verified.
func (dr *decryptReader) next() error {
	if !dr.initialized {
		if err := dr.readHeader(); err != nil {
			return err
		}

		dr.initialized = true
	}

	var length [lengthSize]byte
	if err := dr.readFull(length[:], "chunk length"); err != nil {
		return err
	}

	n := binary.BigEndian.Uint32(length[:])
	if n == 0 {
		return dr.verifyMac()
	}

	if n < minChunkLength || n > maxChunkLength {
		return fmt.Errorf("encryption: %w: invalid chunk length %d", ErrIntegrity, n)
	}

	if dr.chunk == nil {
		dr.chunk = make([]byte, maxChunkLength)
	}

	chunk := dr.chunk[:n]
	if err := dr.readFull(chunk, "chunk"); err != nil {
		return err
	}

	dr.mac.Write(length[:])
	dr.mac.Write(chunk)

	nonce, sealed := chunk[:nonceSize], chunk[nonceSize:]
	plain, err := dr.aead.Open(sealed[:0], nonce, sealed, nil)
	if err != nil {
		return fmt.Errorf("encryption: %w: chunk %d could not be authenticated (wrong key or corrupted data)", ErrIntegrity, dr.chunks)
	}

	dr.chunks++
	dr.plain = plain
	return nil
}

func (dr *decryptReader) readHeader() error {
	var header [headerSize]byte
	if _, err := io.ReadFull(dr.r, header[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("encryption: %w: %w", ErrInvalidHeader, ErrTruncated)
		}

		return fmt.Errorf("encryption: could not read header: %w", err)
	}

	if header[0] != magic[0] || header[1] != magic[1] {
		return fmt.Errorf("encryption: %w: unexpected magic bytes 0x%02X 0x%02X", ErrInvalidHeader, header[0], header[1])
	}

	if header[2] != version {
		return fmt.Errorf("encryption: %w 0x%02X", ErrUnsupportedVersion, header[2])
	}

	return nil
}

func (dr *decryptReader) verifyMac() error {
	var expected [macSize]byte
	if err := dr.readFull(expected[:], "HMAC"); err != nil {
		return err
	}

	if !hmac.Equal(expected[:], dr.mac.Sum(nil)) {
		return fmt.Errorf("encryption: %w: HMAC mismatch", ErrIntegrity)
	}

	return io.EOF
}

func (dr *decryptReader) readFull(buf []byte, part string) error {
	if _, err := io.ReadFull(dr.r, buf); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("encryption: %w: missing %s", ErrTruncated, part)
		}

		return fmt.Errorf("encryption: could not read %s: %w", part, err)
	}

	return nil
}
