package encryption

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKey() []byte {
	return bytes.Repeat([]byte{0x11}, 32)
}

func encryptAll(t *testing.T, key, plain []byte) []byte {
	t.Helper()
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	var buf bytes.Buffer
	w := enc.EncryptWriter(&buf)
	_, err = w.Write(plain)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func decryptAll(t *testing.T, key, ciphertext []byte) ([]byte, error) {
	t.Helper()
	r, err := DecryptReader(bytes.NewReader(ciphertext), key)
	require.NoError(t, err)
	return io.ReadAll(r)
}

func TestNewEncryptorRejectsKeyLength(t *testing.T) {
	_, err := NewEncryptor([]byte("short"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidKey)

	_, err = NewEncryptor(bytes.Repeat([]byte{1}, 31))
	assert.ErrorIs(t, err, ErrInvalidKey)

	_, err = NewEncryptor(bytes.Repeat([]byte{1}, 33))
	assert.ErrorIs(t, err, ErrInvalidKey)
}

func TestRoundTrip(t *testing.T) {
	key := testKey()
	sizes := []int{0, 1, 17, chunkSize - 1, chunkSize, chunkSize + 1, chunkSize*3 + 100}
	for _, n := range sizes {
		plain := bytes.Repeat([]byte{byte(n % 251)}, n)
		out, err := decryptAll(t, key, encryptAll(t, key, plain))
		require.NoError(t, err, "size %d", n)
		assert.Equal(t, plain, out, "size %d", n)
	}
}

func TestEncryptionsDifferAndNoncesUnique(t *testing.T) {
	key := testKey()
	plain := bytes.Repeat([]byte("abc"), chunkSize)
	a := encryptAll(t, key, plain)
	b := encryptAll(t, key, plain)
	assert.NotEqual(t, a, b)

	nonces := parseNonces(t, a)
	seen := map[string]struct{}{}
	require.GreaterOrEqual(t, len(nonces), 2)
	for _, n := range nonces {
		_, ok := seen[string(n)]
		assert.False(t, ok)
		seen[string(n)] = struct{}{}
		assert.Len(t, n, nonceSize)
	}
}

func TestDecryptErrors(t *testing.T) {
	key := testKey()
	plain := []byte("database dump bytes")
	ct := encryptAll(t, key, plain)

	t.Run("invalid header", func(t *testing.T) {
		bad := bytes.Clone(ct)
		bad[0] = 0x00
		_, err := decryptAll(t, key, bad)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid header")
	})

	t.Run("unsupported version", func(t *testing.T) {
		bad := bytes.Clone(ct)
		bad[2] = 0x02
		_, err := decryptAll(t, key, bad)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported version")
	})

	t.Run("integrity", func(t *testing.T) {
		bad := bytes.Clone(ct)
		bad[len(bad)-1] ^= 0xff
		_, err := decryptAll(t, key, bad)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "integrity")
	})

	t.Run("wrong key", func(t *testing.T) {
		other := bytes.Repeat([]byte{0x22}, 32)
		_, err := decryptAll(t, other, ct)
		require.Error(t, err)
	})

	t.Run("truncated", func(t *testing.T) {
		_, err := decryptAll(t, key, ct[:len(ct)/2])
		require.Error(t, err)

		_, err = decryptAll(t, key, ct[:2])
		require.Error(t, err)

		_, err = decryptAll(t, key, ct[:len(ct)-5])
		require.Error(t, err)
	})

	t.Run("lazy", func(t *testing.T) {
		r, err := DecryptReader(bytes.NewReader([]byte{0x00, 0x00, 0x00}), key)
		require.NoError(t, err)
		_, err = r.Read(make([]byte, 8))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid header")
	})
}

func TestDecryptReaderRejectsShortKey(t *testing.T) {
	_, err := DecryptReader(bytes.NewReader(nil), []byte("nope"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidKey)
}

func TestChunkLayout(t *testing.T) {
	key := testKey()
	plain := bytes.Repeat([]byte{9}, chunkSize+10)
	ct := encryptAll(t, key, plain)

	assert.Equal(t, []byte{magic0, magic1, formatVersion}, ct[:3])

	mac := hmac.New(sha256.New, key)
	rest := ct[3:]
	var total int
	for {
		require.GreaterOrEqual(t, len(rest), 4)
		n := binary.BigEndian.Uint32(rest[:4])
		if n == 0 {
			mac.Write(nil) // sentinel is not part of the mac
			sum := rest[4:]
			assert.Len(t, sum, hmacSize)
			assert.True(t, hmac.Equal(mac.Sum(nil), sum))
			break
		}
		require.GreaterOrEqual(t, len(rest), 4+int(n))
		mac.Write(rest[:4+int(n)])
		body := int(n) - nonceSize - tagSize
		assert.LessOrEqual(t, body, chunkSize)
		total += body
		rest = rest[4+int(n):]
	}
	assert.Equal(t, len(plain), total)
}

func parseNonces(t *testing.T, ct []byte) [][]byte {
	t.Helper()
	require.GreaterOrEqual(t, len(ct), 3)
	rest := ct[3:]
	var nonces [][]byte
	for {
		require.GreaterOrEqual(t, len(rest), 4)
		n := binary.BigEndian.Uint32(rest[:4])
		if n == 0 {
			return nonces
		}
		require.GreaterOrEqual(t, len(rest), 4+int(n))
		nonces = append(nonces, bytes.Clone(rest[4:4+nonceSize]))
		rest = rest[4+int(n):]
	}
}

func TestReadErrorIsSticky(t *testing.T) {
	key := testKey()
	r, err := DecryptReader(bytes.NewReader([]byte{0x01, 0x02}), key)
	require.NoError(t, err)
	_, err = io.ReadAll(r)
	require.Error(t, err)
	_, err2 := r.Read(make([]byte, 1))
	assert.Equal(t, err, err2)
	assert.True(t, errors.Is(err2, err) || err2.Error() == err.Error())
}
