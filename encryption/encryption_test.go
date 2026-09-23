package encryption

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, KeySize)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return key
}

func encrypt(t *testing.T, key, plain []byte, writeSize int) []byte {
	t.Helper()
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	var buf bytes.Buffer
	w := enc.EncryptWriter(&buf)
	for len(plain) > 0 {
		n := min(writeSize, len(plain))
		_, err := w.Write(plain[:n])
		require.NoError(t, err)
		plain = plain[n:]
	}
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func decrypt(key, data []byte) ([]byte, error) {
	r, err := DecryptReader(bytes.NewReader(data), key)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func TestNewEncryptorRejectsInvalidKey(t *testing.T) {
	for _, size := range []int{0, 16, 24, 31, 33} {
		_, err := NewEncryptor(make([]byte, size))
		assert.ErrorIs(t, err, ErrInvalidKey)
	}

	_, err := DecryptReader(bytes.NewReader(nil), make([]byte, 16))
	assert.ErrorIs(t, err, ErrInvalidKey)
}

func TestRoundTrip(t *testing.T) {
	key := testKey(t)

	sizes := []int{0, 1, maxChunkSize - 1, maxChunkSize, maxChunkSize + 1, 3*maxChunkSize + 123}
	for _, size := range sizes {
		plain := make([]byte, size)
		_, err := rand.Read(plain)
		require.NoError(t, err)

		for _, writeSize := range []int{7, 4096, maxChunkSize * 4} {
			data := encrypt(t, key, plain, writeSize)
			assert.Equal(t, []byte{0x4F, 0x44, 0x01}, data[:3])

			got, err := decrypt(key, data)
			require.NoError(t, err, "size %d", size)
			assert.Equal(t, plain, got)
		}
	}
}

func TestStreamFormat(t *testing.T) {
	key := testKey(t)
	plain := bytes.Repeat([]byte("a"), maxChunkSize+10)
	data := encrypt(t, key, plain, len(plain))

	offset := headerSize
	var lengths []uint32
	var nonces [][]byte
	for {
		l := binary.BigEndian.Uint32(data[offset : offset+4])
		offset += 4
		if l == 0 {
			break
		}
		lengths = append(lengths, l)
		nonces = append(nonces, data[offset:offset+nonceSize])
		offset += int(l)
	}

	assert.Equal(t, []uint32{nonceSize + maxChunkSize + tagSize, nonceSize + 10 + tagSize}, lengths)
	assert.NotEqual(t, nonces[0], nonces[1])
	assert.Equal(t, len(data), offset+macSize)
}

func TestEncryptionIsNonDeterministic(t *testing.T) {
	key := testKey(t)
	plain := []byte("same plaintext")
	assert.NotEqual(t, encrypt(t, key, plain, 100), encrypt(t, key, plain, 100))
}

func TestCloseIsIdempotent(t *testing.T) {
	enc, err := NewEncryptor(testKey(t))
	require.NoError(t, err)

	var buf bytes.Buffer
	w := enc.EncryptWriter(&buf)
	_, err = w.Write([]byte("hello"))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	size := buf.Len()

	require.NoError(t, w.Close())
	assert.Equal(t, size, buf.Len())

	_, err = w.Write([]byte("more"))
	assert.ErrorIs(t, err, ErrClosed)
}

func TestDecryptErrors(t *testing.T) {
	key := testKey(t)
	plain := bytes.Repeat([]byte("onedump"), 20000)
	data := encrypt(t, key, plain, 1000)

	t.Run("invalid header", func(t *testing.T) {
		bad := bytes.Clone(data)
		bad[0] = 0x00
		_, err := decrypt(key, bad)
		assert.ErrorContains(t, err, "invalid header")

		_, err = decrypt(key, nil)
		assert.Error(t, err)
	})

	t.Run("unsupported version", func(t *testing.T) {
		bad := bytes.Clone(data)
		bad[2] = 0x02
		_, err := decrypt(key, bad)
		assert.ErrorContains(t, err, "unsupported version")
	})

	t.Run("integrity", func(t *testing.T) {
		bad := bytes.Clone(data)
		bad[len(bad)-1] ^= 0xFF
		_, err := decrypt(key, bad)
		assert.ErrorContains(t, err, "integrity")
	})

	t.Run("wrong key", func(t *testing.T) {
		_, err := decrypt(testKey(t), data)
		assert.Error(t, err)

		empty := encrypt(t, key, nil, 1)
		_, err = decrypt(testKey(t), empty)
		assert.ErrorContains(t, err, "integrity")
	})

	t.Run("truncated", func(t *testing.T) {
		for _, cut := range []int{1, 2, 3, 5, headerSize + 4 + 100, len(data) - macSize - 4, len(data) - 10, len(data) - 1} {
			_, err := decrypt(key, data[:cut])
			assert.Error(t, err, "cut at %d", cut)
		}
	})

	t.Run("tampered ciphertext", func(t *testing.T) {
		bad := bytes.Clone(data)
		bad[headerSize+4+nonceSize+5] ^= 0x01
		_, err := decrypt(key, bad)
		assert.Error(t, err)
	})
}
