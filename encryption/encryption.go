package encryption

import (
	"bytes"
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
	KeySize = 32

	magic0  byte = 0x4F
	magic1  byte = 0x44
	version byte = 0x01

	headerSize   = 3
	lengthSize   = 4
	nonceSize    = 12
	tagSize      = 16
	macSize      = sha256.Size
	maxChunkSize = 64 * 1024
	maxFrameSize = nonceSize + maxChunkSize + tagSize
	minFrameSize = nonceSize + tagSize
)

var (
	ErrInvalidKey = errors.New("invalid encryption key")
	ErrClosed     = errors.New("encryption writer is closed")
)

type Encryptor struct {
	key  []byte
	aead cipher.AEAD
}

func NewEncryptor(key []byte) (*Encryptor, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}

	return &Encryptor{key: bytes.Clone(key), aead: aead}, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("%w: expected %d bytes, got %d", ErrInvalidKey, KeySize, len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}

	return cipher.NewGCM(block)
}

// Chunk index is bound as additional authenticated data so chunks cannot be reordered.
func chunkAAD(index uint64) []byte {
	aad := make([]byte, 8)
	binary.BigEndian.PutUint64(aad, index)
	return aad
}

type encryptWriter struct {
	w           io.Writer
	aead        cipher.AEAD
	mac         hash.Hash
	buf         []byte
	index       uint64
	wroteHeader bool
	closed      bool
	err         error
}

// EncryptWriter returns a writer that encrypts everything written to it into w.
// Close must be called to flush the final chunk and write the trailer; it does not close w.
func (e *Encryptor) EncryptWriter(w io.Writer) io.WriteCloser {
	return &encryptWriter{
		w:    w,
		aead: e.aead,
		mac:  hmac.New(sha256.New, e.key),
		buf:  make([]byte, 0, maxChunkSize),
	}
}

func (ew *encryptWriter) writeHeader() error {
	if ew.wroteHeader {
		return nil
	}

	if _, err := ew.w.Write([]byte{magic0, magic1, version}); err != nil {
		return err
	}

	ew.wroteHeader = true
	return nil
}

func (ew *encryptWriter) Write(p []byte) (int, error) {
	if ew.closed {
		return 0, ErrClosed
	}

	if ew.err != nil {
		return 0, ew.err
	}

	if err := ew.writeHeader(); err != nil {
		ew.err = err
		return 0, err
	}

	written := 0
	for len(p) > 0 {
		n := min(maxChunkSize-len(ew.buf), len(p))
		ew.buf = append(ew.buf, p[:n]...)
		p = p[n:]
		written += n

		if len(ew.buf) == maxChunkSize {
			if err := ew.flushChunk(); err != nil {
				ew.err = err
				return written, err
			}
		}
	}

	return written, nil
}

func (ew *encryptWriter) flushChunk() error {
	if len(ew.buf) == 0 {
		return nil
	}

	frame := make([]byte, lengthSize+nonceSize, lengthSize+nonceSize+len(ew.buf)+tagSize)
	nonce := frame[lengthSize : lengthSize+nonceSize]
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("failed to generate nonce: %w", err)
	}

	frame = ew.aead.Seal(frame, nonce, ew.buf, chunkAAD(ew.index))
	binary.BigEndian.PutUint32(frame[:lengthSize], uint32(len(frame)-lengthSize))

	if _, err := ew.w.Write(frame); err != nil {
		return err
	}

	ew.mac.Write(frame)
	ew.index++
	ew.buf = ew.buf[:0]

	return nil
}

func (ew *encryptWriter) Close() error {
	if ew.closed {
		return ew.err
	}
	ew.closed = true

	if ew.err != nil {
		return ew.err
	}

	if err := ew.writeHeader(); err != nil {
		ew.err = err
		return err
	}

	if err := ew.flushChunk(); err != nil {
		ew.err = err
		return err
	}

	trailer := make([]byte, lengthSize, lengthSize+macSize)
	trailer = ew.mac.Sum(trailer)

	if _, err := ew.w.Write(trailer); err != nil {
		ew.err = err
		return err
	}

	return nil
}

type decryptReader struct {
	r           io.Reader
	aead        cipher.AEAD
	mac         hash.Hash
	initialized bool
	index       uint64
	plain       []byte
	done        bool
	err         error
}

// DecryptReader returns a reader that decrypts a stream produced by EncryptWriter.
// The stream is read lazily and any format, authentication or integrity error is returned from Read.
func DecryptReader(r io.Reader, key []byte) (io.Reader, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}

	return &decryptReader{
		r:    r,
		aead: aead,
		mac:  hmac.New(sha256.New, key),
	}, nil
}

func (dr *decryptReader) Read(p []byte) (int, error) {
	for len(dr.plain) == 0 {
		if dr.err != nil {
			return 0, dr.err
		}

		if dr.done {
			return 0, io.EOF
		}

		if err := dr.next(); err != nil {
			dr.err = err
		}
	}

	n := copy(p, dr.plain)
	dr.plain = dr.plain[n:]
	return n, nil
}

func (dr *decryptReader) readFull(buf []byte, what string) error {
	if _, err := io.ReadFull(dr.r, buf); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return fmt.Errorf("failed to read %s: truncated or corrupted data: %w", what, err)
	}
	return nil
}

func (dr *decryptReader) next() error {
	if !dr.initialized {
		header := make([]byte, headerSize)
		if err := dr.readFull(header, "header"); err != nil {
			return fmt.Errorf("invalid header: %w", err)
		}

		if header[0] != magic0 || header[1] != magic1 {
			return fmt.Errorf("invalid header: unexpected magic bytes %#x %#x", header[0], header[1])
		}

		if header[2] != version {
			return fmt.Errorf("unsupported version: %d", header[2])
		}

		dr.initialized = true
	}

	lenBuf := make([]byte, lengthSize)
	if err := dr.readFull(lenBuf, "chunk length"); err != nil {
		return err
	}

	frameLen := binary.BigEndian.Uint32(lenBuf)
	if frameLen == 0 {
		expected := make([]byte, macSize)
		if err := dr.readFull(expected, "hmac"); err != nil {
			return err
		}

		if !hmac.Equal(expected, dr.mac.Sum(nil)) {
			return errors.New("integrity check failed: hmac mismatch")
		}

		dr.done = true
		return nil
	}

	if frameLen < minFrameSize || frameLen > maxFrameSize {
		return fmt.Errorf("invalid chunk length %d", frameLen)
	}

	frame := make([]byte, frameLen)
	if err := dr.readFull(frame, "chunk"); err != nil {
		return err
	}

	dr.mac.Write(lenBuf)
	dr.mac.Write(frame)

	plain, err := dr.aead.Open(nil, frame[:nonceSize], frame[nonceSize:], chunkAAD(dr.index))
	if err != nil {
		return fmt.Errorf("failed to decrypt chunk %d (wrong key or corrupted data): %w", dr.index, err)
	}

	dr.index++
	dr.plain = plain
	return nil
}
