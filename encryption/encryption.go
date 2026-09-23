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

	nonceSize = 12
	tagSize   = 16
	chunkSize = 64 * 1024
	macSize   = 32

	// length prefix covers nonce + ciphertext + tag.
	maxChunkFrame = nonceSize + chunkSize + tagSize
)

var ErrInvalidKey = errors.New("invalid encryption key")

// Encryptor seals plaintext with AES-256-GCM.
type Encryptor struct {
	key []byte
}

// NewEncryptor rejects keys that are not 32 bytes. The returned error wraps ErrInvalidKey.
func NewEncryptor(key []byte) (*Encryptor, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: expected 32 bytes, got %d", ErrInvalidKey, len(key))
	}
	k := make([]byte, 32)
	copy(k, key)
	return &Encryptor{key: k}, nil
}

type encryptWriter struct {
	w       io.Writer
	aead    cipher.AEAD
	key     []byte
	buf     []byte
	mac     hash.Hash
	prefix  [4]byte
	counter uint64
	started bool
	closed  bool
}

// EncryptWriter returns a streaming AES-256-GCM writer. Close is idempotent.
func (e *Encryptor) EncryptWriter(w io.Writer) io.WriteCloser {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		// Key length is checked in NewEncryptor.
		panic(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}

	ew := &encryptWriter{
		w:    w,
		aead: aead,
		key:  e.key,
		buf:  make([]byte, 0, chunkSize),
		mac:  hmac.New(sha256.New, e.key),
	}
	if _, err := rand.Read(ew.prefix[:]); err != nil {
		// crypto/rand failure is unrecoverable for nonce generation.
		panic(err)
	}
	return ew
}

func (ew *encryptWriter) Write(p []byte) (int, error) {
	if ew.closed {
		return 0, errors.New("write to closed encrypt writer")
	}
	if err := ew.writeHeader(); err != nil {
		return 0, err
	}

	written := 0
	for len(p) > 0 {
		space := chunkSize - len(ew.buf)
		n := len(p)
		if n > space {
			n = space
		}
		ew.buf = append(ew.buf, p[:n]...)
		p = p[n:]
		written += n
		if len(ew.buf) == chunkSize {
			if err := ew.flushChunk(); err != nil {
				return written, err
			}
		}
	}
	return written, nil
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
		if err := ew.flushChunk(); err != nil {
			return err
		}
	}

	sentinel := make([]byte, 4)
	if _, err := ew.w.Write(sentinel); err != nil {
		return err
	}
	sum := ew.mac.Sum(nil)
	_, err := ew.w.Write(sum)
	return err
}

func (ew *encryptWriter) writeHeader() error {
	if ew.started {
		return nil
	}
	ew.started = true
	_, err := ew.w.Write([]byte{magic0, magic1, formatVersion})
	return err
}

func (ew *encryptWriter) flushChunk() error {
	nonce := ew.nextNonce()
	sealed := ew.aead.Seal(nil, nonce, ew.buf, nil)
	ew.buf = ew.buf[:0]

	frameLen := nonceSize + len(sealed)
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(frameLen))

	frame := make([]byte, 0, 4+frameLen)
	frame = append(frame, prefix[:]...)
	frame = append(frame, nonce...)
	frame = append(frame, sealed...)

	if _, err := ew.mac.Write(frame); err != nil {
		return err
	}
	_, err := ew.w.Write(frame)
	return err
}

func (ew *encryptWriter) nextNonce() []byte {
	nonce := make([]byte, nonceSize)
	copy(nonce[:4], ew.prefix[:])
	binary.BigEndian.PutUint64(nonce[4:], ew.counter)
	ew.counter++
	return nonce
}

// DecryptReader reverses EncryptWriter. Header and integrity checks happen on Read.
func DecryptReader(r io.Reader, key []byte) (io.Reader, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: expected 32 bytes, got %d", ErrInvalidKey, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	k := make([]byte, 32)
	copy(k, key)
	return &decryptReader{
		r:    r,
		key:  k,
		aead: aead,
		mac:  hmac.New(sha256.New, k),
	}, nil
}

type decryptReader struct {
	r      io.Reader
	key    []byte
	aead   cipher.AEAD
	mac    hash.Hash
	plain  []byte
	inited bool
	done   bool
	err    error
}

func (d *decryptReader) Read(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	if !d.inited {
		if err := d.init(); err != nil {
			d.err = err
			return 0, err
		}
	}

	for len(d.plain) == 0 && !d.done {
		if err := d.readChunk(); err != nil {
			d.err = err
			return 0, err
		}
	}

	if len(d.plain) == 0 && d.done {
		return 0, io.EOF
	}

	n := copy(p, d.plain)
	d.plain = d.plain[n:]
	return n, nil
}

func (d *decryptReader) init() error {
	d.inited = true
	hdr := make([]byte, 3)
	if _, err := io.ReadFull(d.r, hdr); err != nil {
		return fmt.Errorf("truncated encryption header: %w", err)
	}
	if hdr[0] != magic0 || hdr[1] != magic1 {
		return errors.New("invalid header")
	}
	if hdr[2] != formatVersion {
		return errors.New("unsupported version")
	}
	return nil
}

func (d *decryptReader) readChunk() error {
	var prefix [4]byte
	if _, err := io.ReadFull(d.r, prefix[:]); err != nil {
		return fmt.Errorf("truncated encryption stream: %w", err)
	}

	n := binary.BigEndian.Uint32(prefix[:])
	if n == 0 {
		return d.finish()
	}
	if n < nonceSize+tagSize || n > maxChunkFrame {
		return fmt.Errorf("invalid chunk length %d", n)
	}

	frame := make([]byte, 4+n)
	copy(frame[:4], prefix[:])
	if _, err := io.ReadFull(d.r, frame[4:]); err != nil {
		return fmt.Errorf("truncated encryption chunk: %w", err)
	}
	if _, err := d.mac.Write(frame); err != nil {
		return err
	}

	nonce := frame[4 : 4+nonceSize]
	sealed := frame[4+nonceSize:]
	plain, err := d.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return fmt.Errorf("decryption failed: %w", err)
	}
	d.plain = append(d.plain, plain...)
	return nil
}

func (d *decryptReader) finish() error {
	got := make([]byte, macSize)
	if _, err := io.ReadFull(d.r, got); err != nil {
		return fmt.Errorf("truncated encryption integrity tag: %w", err)
	}
	sum := d.mac.Sum(nil)
	if !hmac.Equal(sum, got) {
		return errors.New("encryption integrity check failed")
	}
	d.done = true
	return nil
}
