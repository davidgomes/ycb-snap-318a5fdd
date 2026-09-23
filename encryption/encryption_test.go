package encryption

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
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

func encrypt(t *testing.T, key, plaintext []byte) []byte {
	t.Helper()
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	var buf bytes.Buffer
	w := enc.EncryptWriter(&buf)
	_, err = w.Write(plaintext)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	return buf.Bytes()
}

func decrypt(key, ciphertext []byte) ([]byte, error) {
	r, err := DecryptReader(bytes.NewReader(ciphertext), key)
	if err != nil {
		return nil, err
	}

	return io.ReadAll(r)
}

type chunk struct {
	nonce  []byte
	length int
}

func parseChunks(t *testing.T, stream []byte) []chunk {
	t.Helper()
	var chunks []chunk
	body := stream[headerSize:]
	for {
		length := int(binary.BigEndian.Uint32(body[:lengthSize]))
		if length == 0 {
			break
		}
		chunks = append(chunks, chunk{nonce: body[lengthSize : lengthSize+nonceSize], length: length})
		body = body[lengthSize+length:]
	}
	require.Len(t, body, lengthSize+macSize)
	return chunks
}

func TestNewEncryptorRejectsInvalidKeys(t *testing.T) {
	for _, size := range []int{0, 16, 24, 31, 33, 64} {
		_, err := NewEncryptor(make([]byte, size))
		assert.ErrorIs(t, err, ErrInvalidKey, "size %d", size)
	}

	_, err := NewEncryptor(make([]byte, KeySize))
	assert.NoError(t, err)
}

func TestRoundTrip(t *testing.T) {
	key := testKey(t)

	sizes := []int{0, 1, 100, ChunkSize - 1, ChunkSize, ChunkSize + 1, 3*ChunkSize + 17}
	for _, size := range sizes {
		plaintext := make([]byte, size)
		_, err := rand.Read(plaintext)
		require.NoError(t, err)

		stream := encrypt(t, key, plaintext)
		got, err := decrypt(key, stream)
		require.NoError(t, err, "size %d", size)
		assert.Equal(t, plaintext, got, "size %d", size)
	}
}

func TestRoundTripSmallWritesAndReads(t *testing.T) {
	key := testKey(t)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	plaintext := bytes.Repeat([]byte("onedump"), 40000)

	var buf bytes.Buffer
	w := enc.EncryptWriter(&buf)
	for i := 0; i < len(plaintext); i += 1000 {
		end := min(i+1000, len(plaintext))
		_, err := w.Write(plaintext[i:end])
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())

	r, err := DecryptReader(&buf, key)
	require.NoError(t, err)

	var got []byte
	p := make([]byte, 333)
	for {
		n, err := r.Read(p)
		got = append(got, p[:n]...)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
	}
	assert.Equal(t, plaintext, got)
}

func TestStreamFormat(t *testing.T) {
	key := testKey(t)
	plaintext := make([]byte, 2*ChunkSize+10)

	stream := encrypt(t, key, plaintext)
	assert.Equal(t, []byte{0x4F, 0x44, 0x01}, stream[:3])

	chunks := parseChunks(t, stream)
	require.Len(t, chunks, 3)
	assert.Equal(t, nonceSize+ChunkSize+tagSize, chunks[0].length)
	assert.Equal(t, nonceSize+ChunkSize+tagSize, chunks[1].length)
	assert.Equal(t, nonceSize+10+tagSize, chunks[2].length)

	sentinelAt := len(stream) - macSize - lengthSize
	assert.Equal(t, []byte{0, 0, 0, 0}, stream[sentinelAt:sentinelAt+lengthSize])

	mac := hmac.New(sha256.New, key)
	mac.Write(stream[headerSize:sentinelAt])
	assert.Equal(t, mac.Sum(nil), stream[len(stream)-macSize:])
}

func TestUniqueNoncesAndRandomisedOutput(t *testing.T) {
	key := testKey(t)
	plaintext := make([]byte, 4*ChunkSize)

	first := encrypt(t, key, plaintext)
	second := encrypt(t, key, plaintext)
	assert.NotEqual(t, first, second)

	seen := map[string]bool{}
	for _, stream := range [][]byte{first, second} {
		for _, c := range parseChunks(t, stream) {
			assert.False(t, seen[string(c.nonce)], "nonce reused")
			seen[string(c.nonce)] = true
		}
	}

	assert.NotEqual(t, encrypt(t, key, nil), encrypt(t, key, nil))
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
	assert.ErrorIs(t, err, ErrWriterClosed)
}

func TestDecryptErrors(t *testing.T) {
	key := testKey(t)
	stream := encrypt(t, key, bytes.Repeat([]byte("a"), ChunkSize+100))

	mutate := func(f func(b []byte) []byte) []byte {
		b := append([]byte(nil), stream...)
		return f(b)
	}

	t.Run("invalid key length", func(t *testing.T) {
		_, err := DecryptReader(bytes.NewReader(stream), key[:16])
		assert.ErrorIs(t, err, ErrInvalidKey)
	})

	t.Run("invalid header", func(t *testing.T) {
		_, err := decrypt(key, mutate(func(b []byte) []byte { b[0] = 0x00; return b }))
		assert.ErrorContains(t, err, "invalid header")

		_, err = decrypt(key, []byte{0x4F})
		assert.ErrorContains(t, err, "invalid header")

		_, err = decrypt(key, nil)
		assert.Error(t, err)
	})

	t.Run("unsupported version", func(t *testing.T) {
		_, err := decrypt(key, mutate(func(b []byte) []byte { b[2] = 0x02; return b }))
		assert.ErrorContains(t, err, "unsupported version")
	})

	t.Run("hmac mismatch", func(t *testing.T) {
		_, err := decrypt(key, mutate(func(b []byte) []byte { b[len(b)-1] ^= 0xFF; return b }))
		assert.ErrorContains(t, err, "integrity")
	})

	t.Run("tampered ciphertext", func(t *testing.T) {
		_, err := decrypt(key, mutate(func(b []byte) []byte { b[headerSize+lengthSize+nonceSize+5] ^= 0xFF; return b }))
		assert.ErrorContains(t, err, "integrity")
	})

	t.Run("wrong key", func(t *testing.T) {
		_, err := decrypt(testKey(t), stream)
		assert.Error(t, err)

		_, err = decrypt(testKey(t), encrypt(t, key, nil))
		assert.Error(t, err)
	})

	t.Run("truncated", func(t *testing.T) {
		for _, cut := range []int{1, macSize, macSize + lengthSize, macSize + lengthSize + 1, len(stream) - headerSize - 2, len(stream) - headerSize} {
			_, err := decrypt(key, stream[:len(stream)-cut])
			assert.Error(t, err, "cut %d", cut)
		}
	})

	t.Run("invalid chunk length", func(t *testing.T) {
		_, err := decrypt(key, mutate(func(b []byte) []byte {
			binary.BigEndian.PutUint32(b[headerSize:], maxChunkLength+1)
			return b
		}))
		assert.ErrorContains(t, err, "invalid chunk length")
	})

	t.Run("errors are sticky", func(t *testing.T) {
		r, err := DecryptReader(bytes.NewReader(mutate(func(b []byte) []byte { b[2] = 0x09; return b })), key)
		require.NoError(t, err)

		p := make([]byte, 10)
		_, err1 := r.Read(p)
		_, err2 := r.Read(p)
		assert.ErrorContains(t, err1, "unsupported version")
		assert.Equal(t, err1, err2)
	})
}

type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) { return 0, errors.New("boom") }

func TestEncryptWriterPropagatesWriteErrors(t *testing.T) {
	enc, err := NewEncryptor(testKey(t))
	require.NoError(t, err)

	w := enc.EncryptWriter(failingWriter{})
	_, err = w.Write([]byte("data"))
	assert.ErrorContains(t, err, "boom")
	assert.ErrorContains(t, w.Close(), "boom")
}
