package encryption

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEncryptorRejectsNon32ByteKeys(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33, 64} {
		_, err := NewEncryptor(bytes.Repeat([]byte{1}, n))
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidKey)
	}

	enc, err := NewEncryptor(bytes.Repeat([]byte{2}, 32))
	require.NoError(t, err)
	assert.NotNil(t, enc)
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{9}, 32)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	payloads := [][]byte{
		nil,
		[]byte("hello"),
		bytes.Repeat([]byte("A"), chunkSize),
		bytes.Repeat([]byte("B"), chunkSize+1),
		bytes.Repeat([]byte("C"), chunkSize*2+100),
	}

	for _, payload := range payloads {
		var buf bytes.Buffer
		w := enc.EncryptWriter(&buf)
		_, err := w.Write(payload)
		require.NoError(t, err)
		require.NoError(t, w.Close())
		require.NoError(t, w.Close())

		assertStreamFormat(t, key, buf.Bytes(), payload)

		r, err := DecryptReader(bytes.NewReader(buf.Bytes()), key)
		require.NoError(t, err)
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		if payload == nil {
			assert.Empty(t, got)
		} else {
			assert.Equal(t, payload, got)
		}
	}
}

func TestEncryptionsDifferAndNoncesAreUnique(t *testing.T) {
	key := bytes.Repeat([]byte{3}, 32)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	payload := bytes.Repeat([]byte("same plaintext"), 5000)

	seal := func() []byte {
		t.Helper()
		var buf bytes.Buffer
		w := enc.EncryptWriter(&buf)
		_, err := w.Write(payload)
		require.NoError(t, err)
		require.NoError(t, w.Close())
		return buf.Bytes()
	}

	first := seal()
	second := seal()
	assert.NotEqual(t, first, second)

	nonces := chunkNonces(t, first)
	require.GreaterOrEqual(t, len(nonces), 2)
	seen := map[string]struct{}{}
	for _, nonce := range nonces {
		_, ok := seen[string(nonce)]
		assert.False(t, ok)
		seen[string(nonce)] = struct{}{}
	}
}

func TestDecryptReaderLazyErrors(t *testing.T) {
	key := bytes.Repeat([]byte{4}, 32)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	var buf bytes.Buffer
	w := enc.EncryptWriter(&buf)
	_, err = w.Write([]byte("payload"))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	stream := buf.Bytes()

	r, err := DecryptReader(bytes.NewReader(stream), key)
	require.NoError(t, err)
	// Header is not consumed until Read.
	require.Equal(t, stream[:3], []byte{magic0, magic1, version})

	badMagic := append([]byte{}, stream...)
	badMagic[0] = 0x00
	r, err = DecryptReader(bytes.NewReader(badMagic), key)
	require.NoError(t, err)
	_, err = io.ReadAll(r)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid header")

	badVersion := append([]byte{}, stream...)
	badVersion[2] = 0x02
	r, err = DecryptReader(bytes.NewReader(badVersion), key)
	require.NoError(t, err)
	_, err = io.ReadAll(r)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported version")

	tampered := append([]byte{}, stream...)
	tampered[len(tampered)-1] ^= 0xFF
	r, err = DecryptReader(bytes.NewReader(tampered), key)
	require.NoError(t, err)
	_, err = io.ReadAll(r)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "integrity")

	wrongKey := bytes.Repeat([]byte{5}, 32)
	r, err = DecryptReader(bytes.NewReader(stream), wrongKey)
	require.NoError(t, err)
	_, err = io.ReadAll(r)
	require.Error(t, err)

	_, err = DecryptReader(bytes.NewReader(stream), []byte("short"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidKey)

	for _, truncated := range [][]byte{
		nil,
		stream[:2],
		stream[:len(stream)/2],
		stream[:len(stream)-1],
	} {
		r, err = DecryptReader(bytes.NewReader(truncated), key)
		require.NoError(t, err)
		_, err = io.ReadAll(r)
		require.Error(t, err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	key := bytes.Repeat([]byte{6}, 32)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	var buf bytes.Buffer
	w := enc.EncryptWriter(&buf)
	_, err = w.Write([]byte("once"))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	n := buf.Len()
	require.NoError(t, w.Close())
	assert.Equal(t, n, buf.Len())

	_, err = w.Write([]byte("nope"))
	require.Error(t, err)
}

func assertStreamFormat(t *testing.T, key, stream, plaintext []byte) {
	t.Helper()
	require.GreaterOrEqual(t, len(stream), 3+4+hmacSize)
	assert.Equal(t, []byte{magic0, magic1, version}, stream[:3])

	rest := stream[3:]
	var recovered []byte
	for {
		require.GreaterOrEqual(t, len(rest), 4)
		n := binary.BigEndian.Uint32(rest[:4])
		if n == 0 {
			rest = rest[4:]
			break
		}
		require.LessOrEqual(t, int(n), chunkSize+nonceSize+tagSize)
		require.GreaterOrEqual(t, len(rest), 4+int(n))
		body := rest[4 : 4+n]
		plainLen := int(n) - nonceSize - tagSize
		assert.LessOrEqual(t, plainLen, chunkSize)
		recovered = append(recovered, body...)
		rest = rest[4+n:]
		_ = recovered
	}
	require.Len(t, rest, hmacSize)

	framed := stream[3 : len(stream)-hmacSize-4]
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(framed)
	assert.True(t, hmac.Equal(mac.Sum(nil), stream[len(stream)-hmacSize:]))

	if len(plaintext) == 0 {
		assert.Empty(t, framed)
		return
	}
	assert.NotEmpty(t, framed)
}

func chunkNonces(t *testing.T, stream []byte) [][]byte {
	t.Helper()
	rest := stream[3:]
	var nonces [][]byte
	for {
		n := binary.BigEndian.Uint32(rest[:4])
		rest = rest[4:]
		if n == 0 {
			return nonces
		}
		nonces = append(nonces, append([]byte(nil), rest[:nonceSize]...))
		rest = rest[n:]
	}
}

func TestDecryptReaderDoesNotReadBeforeRead(t *testing.T) {
	key := bytes.Repeat([]byte{8}, 32)
	cr := &countReader{r: bytes.NewReader([]byte{magic0, magic1, version})}
	r, err := DecryptReader(cr, key)
	require.NoError(t, err)
	assert.Zero(t, cr.n)

	_, err = r.Read(make([]byte, 8))
	require.Error(t, err)
	assert.Greater(t, cr.n, 0)
}

type countReader struct {
	r io.Reader
	n int
}

func (c *countReader) Read(p []byte) (int, error) {
	c.n++
	return c.r.Read(p)
}
