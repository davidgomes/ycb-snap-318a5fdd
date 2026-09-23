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
	"io"
)

const (
	magic0     = 0x4F
	magic1     = 0x44
	version1   = 0x01
	nonceSize  = 12
	tagSize    = 16
	hmacSize   = sha256.Size
	maxPlain   = 64 * 1024
	headerSize = 3
)

// ErrInvalidKey is returned when a key is not a 32-byte AES-256 key.
var ErrInvalidKey = errors.New("invalid key")

// Encryptor streams plaintext as AES-256-GCM chunks.
type Encryptor struct {
	key  []byte
	aead cipher.AEAD
}

// NewEncryptor rejects keys that are not exactly 32 bytes.
// The returned error wraps ErrInvalidKey.
func NewEncryptor(key []byte) (*Encryptor, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: got %d bytes", ErrInvalidKey, len(key))
	}

	copied := make([]byte, 32)
	copy(copied, key)

	block, err := aes.NewCipher(copied)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}

	return &Encryptor{key: copied, aead: aead}, nil
}

// EncryptWriter returns a streaming writer.
// The stream starts with a 3-byte header (magic 0x4F 0x44, version 0x01).
// Each chunk is a 4-byte big-endian length, a 12-byte nonce, and ciphertext
// with a 16-byte GCM tag. The length covers nonce, ciphertext, and tag.
// Plaintext chunks are at most 64KB. A 4-byte zero sentinel ends the chunks,
// followed by HMAC-SHA256 over every byte between the header and the sentinel.
// Close is idempotent. Each chunk uses a unique random nonce, so two
// encryptions of the same plaintext differ.
func (e *Encryptor) EncryptWriter(w io.Writer) io.WriteCloser {
	return &encryptWriter{
		dst:    w,
		aead:   e.aead,
		buf:    make([]byte, 0, maxPlain),
		mac:    hmac.New(sha256.New, e.key),
		nonces: make(map[[nonceSize]byte]struct{}),
	}
}

type encryptWriter struct {
	dst    io.Writer
	aead   cipher.AEAD
	buf    []byte
	mac    hashMAC
	nonces map[[nonceSize]byte]struct{}
	chunks int
	header bool
	closed bool
}

// hashMAC is the subset of hash.Hash used to accumulate the stream MAC.
type hashMAC interface {
	Write([]byte) (int, error)
	Sum([]byte) []byte
}

func (e *encryptWriter) Write(p []byte) (int, error) {
	if e.closed {
		return 0, errors.New("write to closed encryption writer")
	}

	written := 0
	for len(p) > 0 {
		space := maxPlain - len(e.buf)
		n := len(p)
		if n > space {
			n = space
		}
		e.buf = append(e.buf, p[:n]...)
		p = p[n:]
		written += n

		if len(e.buf) == maxPlain {
			if err := e.writeChunk(e.buf); err != nil {
				return written, err
			}
			e.buf = e.buf[:0]
		}
	}

	return written, nil
}

// Close finishes the stream. A second call is a no-op.
func (e *encryptWriter) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true

	// An empty plaintext still gets one chunk so the random nonce makes
	// repeated encryptions differ.
	if len(e.buf) > 0 || e.chunks == 0 {
		if err := e.writeChunk(e.buf); err != nil {
			return err
		}
		e.buf = e.buf[:0]
	}

	var sentinel [4]byte
	if err := writeFull(e.dst, sentinel[:]); err != nil {
		return err
	}

	return writeFull(e.dst, e.mac.Sum(nil))
}

func (e *encryptWriter) writeChunk(plain []byte) error {
	if err := e.writeHeader(); err != nil {
		return err
	}

	nonce, err := e.uniqueNonce()
	if err != nil {
		return err
	}

	sealed := e.aead.Seal(nil, nonce, plain, nil)
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(nonce)+len(sealed)))

	if err := e.writeMAC(prefix[:]); err != nil {
		return err
	}
	if err := e.writeMAC(nonce); err != nil {
		return err
	}
	if err := e.writeMAC(sealed); err != nil {
		return err
	}

	e.chunks++
	return nil
}

func (e *encryptWriter) writeHeader() error {
	if e.header {
		return nil
	}
	e.header = true
	return writeFull(e.dst, []byte{magic0, magic1, version1})
}

func (e *encryptWriter) writeMAC(p []byte) error {
	if _, err := e.mac.Write(p); err != nil {
		return err
	}
	return writeFull(e.dst, p)
}

func (e *encryptWriter) uniqueNonce() ([]byte, error) {
	var nonce [nonceSize]byte
	for range 8 {
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, fmt.Errorf("generate nonce: %w", err)
		}
		if _, exists := e.nonces[nonce]; exists {
			continue
		}
		e.nonces[nonce] = struct{}{}
		out := make([]byte, nonceSize)
		copy(out, nonce[:])
		return out, nil
	}
	return nil, errors.New("failed to generate a unique nonce")
}

func writeFull(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}

// DecryptReader reverses EncryptWriter.
// The reader is initialized lazily; header, version, integrity, and truncation
// errors are returned from Read.
func DecryptReader(r io.Reader, key []byte) (io.Reader, error) {
	d := &decryptReader{r: r, keyLen: len(key)}
	if len(key) == 32 {
		d.key = append([]byte(nil), key...)
	}
	return d, nil
}

type decryptReader struct {
	r           io.Reader
	key         []byte
	keyLen      int
	plain       []byte
	off         int
	initialized bool
	err         error
}

func (d *decryptReader) Read(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	if !d.initialized {
		d.initialized = true
		if err := d.consume(); err != nil {
			d.err = err
			return 0, err
		}
	}
	if len(p) == 0 {
		return 0, nil
	}
	if d.off >= len(d.plain) {
		return 0, io.EOF
	}
	n := copy(p, d.plain[d.off:])
	d.off += n
	return n, nil
}

func (d *decryptReader) consume() error {
	if d.keyLen != 32 {
		return fmt.Errorf("%w: got %d bytes", ErrInvalidKey, d.keyLen)
	}
	if d.r == nil {
		return errors.New("truncated encryption stream")
	}

	header := make([]byte, headerSize)
	if err := readFull(d.r, header); err != nil {
		return err
	}
	if header[0] != magic0 || header[1] != magic1 {
		return errors.New("invalid header")
	}
	if header[2] != version1 {
		return fmt.Errorf("unsupported version 0x%02x", header[2])
	}

	block, err := aes.NewCipher(d.key)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("create gcm: %w", err)
	}

	mac := hmac.New(sha256.New, d.key)
	var plain []byte
	for {
		var prefix [4]byte
		if err := readFull(d.r, prefix[:]); err != nil {
			return err
		}
		n := binary.BigEndian.Uint32(prefix[:])
		if n == 0 {
			sum := make([]byte, hmacSize)
			if err := readFull(d.r, sum); err != nil {
				return err
			}
			if !hmac.Equal(mac.Sum(nil), sum) {
				return errors.New("encryption integrity check failed")
			}
			d.plain = plain
			return nil
		}
		if n < nonceSize+tagSize || n > maxPlain+nonceSize+tagSize {
			return fmt.Errorf("invalid encryption chunk length %d", n)
		}

		if _, err := mac.Write(prefix[:]); err != nil {
			return err
		}
		payload := make([]byte, n)
		if err := readFull(d.r, payload); err != nil {
			return err
		}
		if _, err := mac.Write(payload); err != nil {
			return err
		}

		pt, err := aead.Open(nil, payload[:nonceSize], payload[nonceSize:], nil)
		if err != nil {
			return fmt.Errorf("decryption failed: %w", err)
		}
		plain = append(plain, pt...)
	}
}

func readFull(r io.Reader, buf []byte) error {
	_, err := io.ReadFull(r, buf)
	if err == nil {
		return nil
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("truncated encryption stream: %w", err)
	}
	return fmt.Errorf("read encryption stream: %w", err)
}
