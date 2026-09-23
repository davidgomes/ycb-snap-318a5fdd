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
	KeySize    = 32
	ChunkSize  = 64 * 1024
	nonceSize  = 12
	tagSize    = 16
	macSize    = sha256.Size
	version    = 0x01
	maxEncSize = ChunkSize + nonceSize + tagSize
)

var (
	ErrInvalidKey = errors.New("invalid encryption key")
	magic         = []byte{0x4F, 0x44}
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
		return nil, err
	}
	return cipher.NewGCM(block)
}

type encryptWriter struct {
	w         io.Writer
	aead      cipher.AEAD
	mac       hash.Hash
	buf       []byte
	headerOut bool
	closed    bool
	err       error
}

func (e *Encryptor) EncryptWriter(w io.Writer) io.WriteCloser {
	ew := &encryptWriter{w: w, mac: hmac.New(sha256.New, e.key), buf: make([]byte, 0, ChunkSize)}
	ew.aead, ew.err = newGCM(e.key)
	return ew
}

func (ew *encryptWriter) writeHeader() error {
	if ew.headerOut {
		return nil
	}
	ew.headerOut = true
	_, err := ew.w.Write(append(append([]byte{}, magic...), version))
	return err
}

func (ew *encryptWriter) flushChunk() error {
	if len(ew.buf) == 0 {
		return nil
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	out := make([]byte, 4, 4+nonceSize+len(ew.buf)+tagSize)
	binary.BigEndian.PutUint32(out, uint32(nonceSize+len(ew.buf)+tagSize))
	out = append(out, nonce...)
	out = ew.aead.Seal(out, nonce, ew.buf, nil)
	ew.buf = ew.buf[:0]
	ew.mac.Write(out)
	_, err := ew.w.Write(out)
	return err
}

func (ew *encryptWriter) Write(p []byte) (int, error) {
	if ew.closed {
		return 0, errors.New("encryption: write to closed writer")
	}
	if ew.err != nil {
		return 0, ew.err
	}
	if ew.err = ew.writeHeader(); ew.err != nil {
		return 0, ew.err
	}
	n := 0
	for len(p) > 0 {
		space := ChunkSize - len(ew.buf)
		take := min(space, len(p))
		ew.buf = append(ew.buf, p[:take]...)
		p = p[take:]
		n += take
		if len(ew.buf) == ChunkSize {
			if ew.err = ew.flushChunk(); ew.err != nil {
				return n, ew.err
			}
		}
	}
	return n, nil
}

func (ew *encryptWriter) Close() error {
	if ew.closed {
		return nil
	}
	ew.closed = true
	if ew.err != nil {
		return ew.err
	}
	if err := ew.writeHeader(); err != nil {
		return err
	}
	if err := ew.flushChunk(); err != nil {
		return err
	}
	tail := append(make([]byte, 4), ew.mac.Sum(nil)...)
	_, err := ew.w.Write(tail)
	return err
}

type decryptReader struct {
	r      io.Reader
	key    []byte
	aead   cipher.AEAD
	mac    hash.Hash
	inited bool
	buf    []byte
	done   bool
	err    error
}

func DecryptReader(r io.Reader, key []byte) (io.Reader, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("%w: key must be %d bytes, got %d", ErrInvalidKey, KeySize, len(key))
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	return &decryptReader{r: r, key: append([]byte{}, key...), aead: aead, mac: hmac.New(sha256.New, key)}, nil
}

func truncErr(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("encryption: truncated data: %w", io.ErrUnexpectedEOF)
	}
	return err
}

func (d *decryptReader) init() error {
	header := make([]byte, 3)
	if _, err := io.ReadFull(d.r, header); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("encryption: invalid header: %w", io.ErrUnexpectedEOF)
		}
		return err
	}
	if !bytes.Equal(header[:2], magic) {
		return errors.New("encryption: invalid header")
	}
	if header[2] != version {
		return fmt.Errorf("encryption: unsupported version %d", header[2])
	}
	return nil
}

func (d *decryptReader) nextChunk() error {
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(d.r, lenBuf); err != nil {
		return truncErr(err)
	}
	n := binary.BigEndian.Uint32(lenBuf)
	if n == 0 {
		sum := make([]byte, macSize)
		if _, err := io.ReadFull(d.r, sum); err != nil {
			return truncErr(err)
		}
		if !hmac.Equal(sum, d.mac.Sum(nil)) {
			return errors.New("encryption: integrity check failed")
		}
		d.done = true
		return nil
	}
	if n < nonceSize+tagSize || n > maxEncSize {
		return fmt.Errorf("encryption: invalid chunk length %d", n)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(d.r, data); err != nil {
		return truncErr(err)
	}
	d.mac.Write(lenBuf)
	d.mac.Write(data)
	plain, err := d.aead.Open(nil, data[:nonceSize], data[nonceSize:], nil)
	if err != nil {
		return fmt.Errorf("encryption: failed to decrypt chunk (wrong key or corrupted data): %w", err)
	}
	d.buf = plain
	return nil
}

func (d *decryptReader) Read(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	if !d.inited {
		d.inited = true
		if d.err = d.init(); d.err != nil {
			return 0, d.err
		}
	}
	for len(d.buf) == 0 {
		if d.done {
			return 0, io.EOF
		}
		if d.err = d.nextChunk(); d.err != nil {
			return 0, d.err
		}
	}
	n := copy(p, d.buf)
	d.buf = d.buf[n:]
	return n, nil
}
