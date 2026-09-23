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
//	header:   magic (0x4F 0x44) | version (0x01)
//	chunk:    length (uint32 BE, covers nonce+ciphertext+tag) | nonce (12) | ciphertext+tag
//	sentinel: 4 zero bytes
//	trailer:  HMAC-SHA256 (32) over every byte between the header and the sentinel
const (
	KeySize        = 32
	ChunkSize      = 64 * 1024
	Version   byte = 0x01

	nonceSize       = 12
	tagSize         = 16
	lengthSize      = 4
	macSize         = sha256.Size
	minChunkLength  = nonceSize + tagSize
	maxChunkLength  = nonceSize + ChunkSize + tagSize
	headerSize      = 3
	magicByteFirst  = 0x4F
	magicByteSecond = 0x44
)

var (
	ErrInvalidKey   = errors.New("invalid encryption key")
	ErrWriterClosed = errors.New("encryption: write to closed writer")
)

type Encryptor struct {
	key []byte
}

func NewEncryptor(key []byte) (*Encryptor, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("%w: key must be %d bytes, got %d", ErrInvalidKey, KeySize, len(key))
	}

	k := make([]byte, KeySize)
	copy(k, key)

	return &Encryptor{key: k}, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}

	return cipher.NewGCM(block)
}

// EncryptWriter returns a writer that encrypts everything written to it and writes the
// encrypted stream to w. Close must be called to flush the last chunk and write the trailer;
// it does not close w.
func (e *Encryptor) EncryptWriter(w io.Writer) io.WriteCloser {
	ew := &encryptWriter{
		w:   w,
		mac: hmac.New(sha256.New, e.key),
		buf: make([]byte, 0, ChunkSize),
	}

	gcm, err := newGCM(e.key)
	if err != nil {
		ew.err = err
	}
	ew.gcm = gcm

	return ew
}

type encryptWriter struct {
	w             io.Writer
	gcm           cipher.AEAD
	mac           hash.Hash
	buf           []byte
	headerWritten bool
	chunksWritten int
	closed        bool
	err           error
}

func (ew *encryptWriter) Write(p []byte) (int, error) {
	if ew.closed {
		return 0, ErrWriterClosed
	}

	if ew.err != nil {
		return 0, ew.err
	}

	if err := ew.writeHeader(); err != nil {
		return 0, err
	}

	written := 0
	for len(p) > 0 {
		n := copy(ew.buf[len(ew.buf):cap(ew.buf)], p)
		ew.buf = ew.buf[:len(ew.buf)+n]
		p = p[n:]
		written += n

		if len(ew.buf) == cap(ew.buf) {
			if err := ew.flushChunk(); err != nil {
				return written, err
			}
		}
	}

	return written, nil
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
		return err
	}

	// Always emit at least one chunk so that encrypting an empty plaintext still yields a randomised stream.
	if len(ew.buf) > 0 || ew.chunksWritten == 0 {
		if err := ew.flushChunk(); err != nil {
			return err
		}
	}

	trailer := make([]byte, lengthSize, lengthSize+macSize)
	trailer = ew.mac.Sum(trailer)

	if _, err := ew.w.Write(trailer); err != nil {
		ew.err = fmt.Errorf("encryption: failed to write trailer: %w", err)
		return ew.err
	}

	return nil
}

func (ew *encryptWriter) writeHeader() error {
	if ew.headerWritten {
		return nil
	}

	ew.headerWritten = true
	if _, err := ew.w.Write([]byte{magicByteFirst, magicByteSecond, Version}); err != nil {
		ew.err = fmt.Errorf("encryption: failed to write header: %w", err)
		return ew.err
	}

	return nil
}

func (ew *encryptWriter) flushChunk() error {
	var nonce [nonceSize]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		ew.err = fmt.Errorf("encryption: failed to generate nonce: %w", err)
		return ew.err
	}

	chunkLength := nonceSize + len(ew.buf) + tagSize
	out := make([]byte, lengthSize+nonceSize, lengthSize+chunkLength)
	binary.BigEndian.PutUint32(out[:lengthSize], uint32(chunkLength))
	copy(out[lengthSize:], nonce[:])
	out = ew.gcm.Seal(out, nonce[:], ew.buf, nil)

	ew.mac.Write(out)
	ew.buf = ew.buf[:0]
	ew.chunksWritten++

	if _, err := ew.w.Write(out); err != nil {
		ew.err = fmt.Errorf("encryption: failed to write chunk: %w", err)
		return ew.err
	}

	return nil
}

// DecryptReader returns a reader that decrypts a stream produced by EncryptWriter.
// The stream is read lazily, so format, authentication and truncation errors are reported by Read.
// Plaintext of a chunk is only returned after that chunk has been authenticated, and io.EOF is only
// returned once the trailing HMAC has been verified.
func DecryptReader(r io.Reader, key []byte) (io.Reader, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("%w: key must be %d bytes, got %d", ErrInvalidKey, KeySize, len(key))
	}

	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}

	return &decryptReader{
		r:   r,
		gcm: gcm,
		mac: hmac.New(sha256.New, key),
	}, nil
}

type decryptReader struct {
	r          io.Reader
	gcm        cipher.AEAD
	mac        hash.Hash
	headerRead bool
	plaintext  []byte
	chunk      []byte
	done       bool
	err        error
}

func (dr *decryptReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, dr.err
	}

	for len(dr.plaintext) == 0 {
		if dr.err != nil {
			return 0, dr.err
		}

		if dr.done {
			return 0, io.EOF
		}

		if err := dr.next(); err != nil {
			dr.err = err
			return 0, err
		}
	}

	n := copy(p, dr.plaintext)
	dr.plaintext = dr.plaintext[n:]

	return n, nil
}

func (dr *decryptReader) next() error {
	if !dr.headerRead {
		if err := dr.readHeader(); err != nil {
			return err
		}
		dr.headerRead = true
	}

	var lengthBuf [lengthSize]byte
	if _, err := io.ReadFull(dr.r, lengthBuf[:]); err != nil {
		return truncatedErr("chunk length", err)
	}

	length := binary.BigEndian.Uint32(lengthBuf[:])
	if length == 0 {
		return dr.verifyTrailer()
	}

	if length < minChunkLength || length > maxChunkLength {
		return fmt.Errorf("encryption: invalid chunk length %d", length)
	}

	if cap(dr.chunk) < int(length) {
		dr.chunk = make([]byte, maxChunkLength)
	}
	chunk := dr.chunk[:length]

	if _, err := io.ReadFull(dr.r, chunk); err != nil {
		return truncatedErr("chunk", err)
	}

	dr.mac.Write(lengthBuf[:])
	dr.mac.Write(chunk)

	nonce, ciphertext := chunk[:nonceSize], chunk[nonceSize:]
	plaintext, err := dr.gcm.Open(ciphertext[:0], nonce, ciphertext, nil)
	if err != nil {
		return fmt.Errorf("encryption: chunk integrity check failed (wrong key or corrupted data): %w", err)
	}

	dr.plaintext = plaintext

	return nil
}

func (dr *decryptReader) readHeader() error {
	var header [headerSize]byte
	if _, err := io.ReadFull(dr.r, header[:]); err != nil {
		return fmt.Errorf("encryption: invalid header: %w", unexpectedEOF(err))
	}

	if header[0] != magicByteFirst || header[1] != magicByteSecond {
		return fmt.Errorf("encryption: invalid header: unexpected magic bytes 0x%02X 0x%02X", header[0], header[1])
	}

	if header[2] != Version {
		return fmt.Errorf("encryption: unsupported version 0x%02X", header[2])
	}

	return nil
}

func (dr *decryptReader) verifyTrailer() error {
	var expected [macSize]byte
	if _, err := io.ReadFull(dr.r, expected[:]); err != nil {
		return truncatedErr("HMAC", err)
	}

	if !hmac.Equal(expected[:], dr.mac.Sum(nil)) {
		return errors.New("encryption: integrity check failed: HMAC mismatch")
	}

	dr.done = true

	return nil
}

func unexpectedEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}

	return err
}

func truncatedErr(part string, err error) error {
	return fmt.Errorf("encryption: truncated stream while reading %s: %w", part, unexpectedEOF(err))
}
