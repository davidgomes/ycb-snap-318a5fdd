package encryption

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEncryptorRejectsKeyLength(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33, 64} {
		_, err := NewEncryptor(bytes.Repeat([]byte{1}, n))
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidKey)
	}

	enc, err := NewEncryptor(bytes.Repeat([]byte{1}, 32))
	require.NoError(t, err)
	require.NotNil(t, enc)
}

func TestRoundTripAndUniqueCiphertext(t *testing.T) {
	key := bytes.Repeat([]byte{0x2a}, 32)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	plain := bytes.Repeat([]byte("database-dump-row\n"), 5000) // > 64KB

	a := encryptAll(t, enc, plain)
	b := encryptAll(t, enc, plain)
	assert.NotEqual(t, a, b)
	assert.Equal(t, []byte{0x4F, 0x44, 0x01}, a[:3])
	assert.Equal(t, []byte{0x4F, 0x44, 0x01}, b[:3])

	assert.Equal(t, plain, decryptAll(t, a, key))
	assert.Equal(t, plain, decryptAll(t, b, key))

	nonces := chunkNonces(t, a)
	require.GreaterOrEqual(t, len(nonces), 2)
	seen := map[string]struct{}{}
	for _, n := range nonces {
		_, ok := seen[string(n)]
		assert.False(t, ok)
		seen[string(n)] = struct{}{}
		assert.Len(t, n, 12)
	}
}

func TestEmptyPlaintextDiffers(t *testing.T) {
	key := bytes.Repeat([]byte{0x7}, 32)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	a := encryptAll(t, enc, nil)
	b := encryptAll(t, enc, nil)
	assert.NotEqual(t, a, b)
	assert.Empty(t, decryptAll(t, a, key))
	assert.Empty(t, decryptAll(t, b, key))
}

func TestCloseIdempotent(t *testing.T) {
	key := bytes.Repeat([]byte{0x3}, 32)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	var buf bytes.Buffer
	w := enc.EncryptWriter(&buf)
	_, err = w.Write([]byte("hello"))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	n := buf.Len()
	require.NoError(t, w.Close())
	assert.Equal(t, n, buf.Len())
	assert.Equal(t, []byte("hello"), decryptAll(t, buf.Bytes(), key))
}

func TestHMACCoversBytesBetweenHeaderAndSentinel(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, 32)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	blob := encryptAll(t, enc, []byte("abc"))
	require.GreaterOrEqual(t, len(blob), 3+4+32)

	mac := hmac.New(sha256.New, key)
	body := blob[3 : len(blob)-hmacSize]
	require.Equal(t, []byte{0, 0, 0, 0}, body[len(body)-4:])
	_, _ = mac.Write(body[:len(body)-4])
	assert.True(t, hmac.Equal(mac.Sum(nil), blob[len(blob)-hmacSize:]))

	// First chunk length is big-endian and covers nonce + ciphertext + tag.
	length := binary.BigEndian.Uint32(blob[3:7])
	assert.Equal(t, uint32(12+len("abc")+16), length)
}

func TestDecryptErrors(t *testing.T) {
	key := bytes.Repeat([]byte{0x5}, 32)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)
	blob := encryptAll(t, enc, []byte("secret-dump"))

	t.Run("invalid header", func(t *testing.T) {
		bad := append([]byte(nil), blob...)
		bad[0] = 0x00
		err := decryptErr(t, bad, key)
		assert.ErrorContains(t, err, "invalid header")
	})

	t.Run("unsupported version", func(t *testing.T) {
		bad := append([]byte(nil), blob...)
		bad[2] = 0x02
		err := decryptErr(t, bad, key)
		assert.ErrorContains(t, err, "unsupported version")
	})

	t.Run("integrity", func(t *testing.T) {
		bad := append([]byte(nil), blob...)
		bad[len(bad)-1] ^= 0xff
		err := decryptErr(t, bad, key)
		assert.ErrorContains(t, err, "integrity")
	})

	t.Run("wrong key", func(t *testing.T) {
		other := bytes.Repeat([]byte{0x6}, 32)
		err := decryptErr(t, blob, other)
		assert.Error(t, err)
	})

	t.Run("truncated", func(t *testing.T) {
		err := decryptErr(t, blob[:len(blob)/2], key)
		assert.Error(t, err)
		err = decryptErr(t, blob[:2], key)
		assert.Error(t, err)
		err = decryptErr(t, nil, key)
		assert.Error(t, err)
	})

	t.Run("lazy init", func(t *testing.T) {
		r, err := DecryptReader(bytes.NewReader([]byte{0x00, 0x00, 0x00}), key)
		require.NoError(t, err)
		_, err = r.Read(make([]byte, 8))
		assert.ErrorContains(t, err, "invalid header")
	})
}

func TestChunkBoundary(t *testing.T) {
	key := bytes.Repeat([]byte{0x9}, 32)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	one := encryptAll(t, enc, bytes.Repeat([]byte{1}, maxPlain))
	assert.Len(t, chunkNonces(t, one), 1)

	two := encryptAll(t, enc, bytes.Repeat([]byte{1}, maxPlain+1))
	assert.Len(t, chunkNonces(t, two), 2)
	assert.Equal(t, bytes.Repeat([]byte{1}, maxPlain+1), decryptAll(t, two, key))
}

func encryptAll(t *testing.T, enc *Encryptor, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := enc.EncryptWriter(&buf)
	_, err := w.Write(plain)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func decryptAll(t *testing.T, blob, key []byte) []byte {
	t.Helper()
	r, err := DecryptReader(bytes.NewReader(blob), key)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	return got
}

func decryptErr(t *testing.T, blob, key []byte) error {
	t.Helper()
	r, err := DecryptReader(bytes.NewReader(blob), key)
	require.NoError(t, err)
	_, err = io.ReadAll(r)
	require.Error(t, err)
	return err
}

func chunkNonces(t *testing.T, blob []byte) [][]byte {
	t.Helper()
	require.GreaterOrEqual(t, len(blob), 3+4+hmacSize)
	body := blob[3 : len(blob)-hmacSize]
	require.Equal(t, []byte{0, 0, 0, 0}, body[len(body)-4:])
	framed := body[:len(body)-4]

	var nonces [][]byte
	for len(framed) > 0 {
		require.GreaterOrEqual(t, len(framed), 4)
		n := binary.BigEndian.Uint32(framed[:4])
		framed = framed[4:]
		require.GreaterOrEqual(t, len(framed), int(n))
		payload := framed[:n]
		framed = framed[n:]
		require.GreaterOrEqual(t, len(payload), nonceSize+tagSize)
		nonces = append(nonces, append([]byte(nil), payload[:nonceSize]...))
	}
	return nonces
}

func TestDecryptReaderInvalidKey(t *testing.T) {
	r, err := DecryptReader(bytes.NewReader([]byte{0x4F, 0x44, 0x01}), []byte("short"))
	require.NoError(t, err)
	_, err = io.ReadAll(r)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidKey)
	assert.False(t, strings.Contains(err.Error(), "invalid header"))
}
