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
	magic0    = 0x4F
	magic1    = 0x44
	version   = 0x01
	chunkSize = 64 * 1024
	nonceSize = 12
	tagSize   = 16
	hmacSize  = 32
)

// ErrInvalidKey is returned when an encryption key is not 32 bytes.
var ErrInvalidKey = errors.New("invalid key")

// Encryptor encrypts streams with AES-256-GCM.
type Encryptor struct {
	key []byte
	gcm cipher.AEAD
}

// NewEncryptor returns an encryptor for a 32-byte AES-256 key.
func NewEncryptor(key []byte) (*Encryptor, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: expected 32 bytes, got %d", ErrInvalidKey, len(key))
	}

	copied := make([]byte, 32)
	copy(copied, key)

	block, err := aes.NewCipher(copied)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	return &Encryptor{key: copied, gcm: gcm}, nil
}

// EncryptWriter returns a streaming writer that encrypts plaintext into w.
// The stream is header, one or more chunks of at most 64KB, a zero sentinel,
// and an HMAC-SHA256 over the bytes between the header and the sentinel.
func (e *Encryptor) EncryptWriter(w io.Writer) io.WriteCloser {
	ew := &encryptWriter{
		w:   w,
		gcm: e.gcm,
		mac: hmac.New(sha256.New, e.key),
		buf: make([]byte, 0, chunkSize),
	}
	if _, err := rand.Read(ew.noncePrefix[:]); err != nil {
		ew.writeErr = err
	}
	return ew
}

type encryptWriter struct {
	w           io.Writer
	gcm         cipher.AEAD
	mac         hash.Hash
	buf         []byte
	noncePrefix [8]byte
	counter     uint32
	headerDone  bool
	closed      bool
	writeErr    error
}

func (ew *encryptWriter) Write(p []byte) (int, error) {
	if ew.closed {
		return 0, errors.New("write to closed encryptor")
	}
	if ew.writeErr != nil {
		return 0, ew.writeErr
	}
	if err := ew.writeHeader(); err != nil {
		ew.writeErr = err
		return 0, err
	}

	total := 0
	for len(p) > 0 {
		if len(ew.buf) == chunkSize {
			if err := ew.flush(); err != nil {
				ew.writeErr = err
				return total, err
			}
		}

		space := chunkSize - len(ew.buf)
		n := len(p)
		if n > space {
			n = space
		}
		ew.buf = append(ew.buf, p[:n]...)
		p = p[n:]
		total += n
	}

	return total, nil
}

// Close flushes the final chunk, the sentinel, and the HMAC.
// It is safe to call more than once.
func (ew *encryptWriter) Close() error {
	if ew.closed {
		return nil
	}
	ew.closed = true

	if ew.writeErr != nil {
		return ew.writeErr
	}
	if err := ew.writeHeader(); err != nil {
		ew.writeErr = err
		return err
	}
	if err := ew.flush(); err != nil {
		ew.writeErr = err
		return err
	}
	if err := writeAll(ew.w, []byte{0, 0, 0, 0}); err != nil {
		ew.writeErr = err
		return err
	}
	if err := writeAll(ew.w, ew.mac.Sum(nil)); err != nil {
		ew.writeErr = err
		return err
	}
	return nil
}

func (ew *encryptWriter) writeHeader() error {
	if ew.headerDone {
		return nil
	}
	if err := writeAll(ew.w, []byte{magic0, magic1, version}); err != nil {
		return err
	}
	ew.headerDone = true
	return nil
}

func (ew *encryptWriter) flush() error {
	if len(ew.buf) == 0 {
		return nil
	}

	nonce, err := ew.nextNonce()
	if err != nil {
		return err
	}

	sealed := ew.gcm.Seal(nil, nonce, ew.buf, nil)
	ew.buf = ew.buf[:0]

	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(nonce)+len(sealed)))

	if err := ew.emit(hdr[:]); err != nil {
		return err
	}
	if err := ew.emit(nonce); err != nil {
		return err
	}
	return ew.emit(sealed)
}

func (ew *encryptWriter) emit(p []byte) error {
	if err := writeAll(ew.w, p); err != nil {
		return err
	}
	_, _ = ew.mac.Write(p)
	return nil
}

func (ew *encryptWriter) nextNonce() ([]byte, error) {
	if ew.counter == ^uint32(0) {
		return nil, errors.New("exhausted unique nonces")
	}

	nonce := make([]byte, nonceSize)
	copy(nonce, ew.noncePrefix[:])
	binary.BigEndian.PutUint32(nonce[8:], ew.counter)
	ew.counter++
	return nonce, nil
}

func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if n > 0 {
			p = p[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

type decryptReader struct {
	r           io.Reader
	key         []byte
	gcm         cipher.AEAD
	mac         hash.Hash
	pending     []byte
	initialized bool
	eof         bool
	err         error
}

// DecryptReader reverses EncryptWriter. The returned reader does not touch r
// until Read is called.
func DecryptReader(r io.Reader, key []byte) (io.Reader, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: expected 32 bytes, got %d", ErrInvalidKey, len(key))
	}

	copied := make([]byte, 32)
	copy(copied, key)

	return &decryptReader{r: r, key: copied}, nil
}

func (d *decryptReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if d.err != nil {
		return 0, d.err
	}
	if !d.initialized {
		if err := d.init(); err != nil {
			d.err = err
			return 0, err
		}
		d.initialized = true
	}

	for len(d.pending) == 0 {
		if d.eof {
			return 0, io.EOF
		}
		if err := d.readChunk(); err != nil {
			d.err = err
			return 0, err
		}
	}

	n := copy(p, d.pending)
	d.pending = d.pending[n:]
	if len(d.pending) == 0 {
		d.pending = nil
	}
	return n, nil
}

func (d *decryptReader) init() error {
	header := make([]byte, 3)
	if _, err := io.ReadFull(d.r, header); err != nil {
		return fmt.Errorf("truncated encryption stream: %w", err)
	}
	if header[0] != magic0 || header[1] != magic1 {
		return errors.New("invalid header")
	}
	if header[2] != version {
		return fmt.Errorf("unsupported version %d", header[2])
	}

	block, err := aes.NewCipher(d.key)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	d.gcm = gcm
	d.mac = hmac.New(sha256.New, d.key)
	return nil
}

func (d *decryptReader) readChunk() error {
	var lenBuf [4]byte
	if _, err := io.ReadFull(d.r, lenBuf[:]); err != nil {
		return fmt.Errorf("truncated encryption stream: %w", err)
	}

	n := binary.BigEndian.Uint32(lenBuf[:])
	if n == 0 {
		mac := make([]byte, hmacSize)
		if _, err := io.ReadFull(d.r, mac); err != nil {
			return fmt.Errorf("truncated encryption stream: %w", err)
		}
		if !hmac.Equal(d.mac.Sum(nil), mac) {
			return errors.New("integrity check failed")
		}
		d.eof = true
		return nil
	}

	if n < nonceSize+tagSize || n > chunkSize+nonceSize+tagSize {
		return fmt.Errorf("invalid chunk length %d", n)
	}

	_, _ = d.mac.Write(lenBuf[:])

	body := make([]byte, n)
	if _, err := io.ReadFull(d.r, body); err != nil {
		return fmt.Errorf("truncated encryption stream: %w", err)
	}
	_, _ = d.mac.Write(body)

	plain, err := d.gcm.Open(nil, body[:nonceSize], body[nonceSize:], nil)
	if err != nil {
		return fmt.Errorf("decrypt chunk: %w", err)
	}
	d.pending = plain
	return nil
}
