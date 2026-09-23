package encryption

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, KeySize)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return key
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return b
}

func encrypt(t *testing.T, key, plaintext []byte) []byte {
	t.Helper()
	e, err := NewEncryptor(key)
	require.NoError(t, err)

	var out bytes.Buffer
	w := e.EncryptWriter(&out)

	// Write in odd-sized pieces so chunking does not line up with the writes.
	for p := plaintext; len(p) > 0; {
		n := min(len(p), 1000)
		written, err := w.Write(p[:n])
		require.NoError(t, err)
		require.Equal(t, n, written)
		p = p[n:]
	}

	require.NoError(t, w.Close())
	return out.Bytes()
}

func decrypt(key, data []byte) ([]byte, error) {
	r, err := DecryptReader(bytes.NewReader(data), key)
	if err != nil {
		return nil, err
	}

	return io.ReadAll(r)
}

type chunkSpan struct {
	offset int
	length int
}

// Split an encrypted stream into its chunks, returning the offset of the sentinel.
func parseChunks(t *testing.T, data []byte) ([]chunkSpan, int) {
	t.Helper()
	var chunks []chunkSpan

	offset := headerSize
	for {
		require.GreaterOrEqual(t, len(data), offset+lengthSize)
		n := int(binary.BigEndian.Uint32(data[offset:]))
		if n == 0 {
			return chunks, offset
		}

		chunks = append(chunks, chunkSpan{offset: offset, length: lengthSize + n})
		offset += lengthSize + n
	}
}

func TestNewEncryptor(t *testing.T) {
	for _, size := range []int{0, 1, 16, 24, 31, 33, 64} {
		_, err := NewEncryptor(make([]byte, size))
		assert.ErrorIs(t, err, ErrInvalidKey, "key size %d", size)
	}

	e, err := NewEncryptor(newKey(t))
	assert.NoError(t, err)
	assert.NotNil(t, e)
}

func TestNewEncryptorCopiesKey(t *testing.T) {
	key := newKey(t)
	original := bytes.Clone(key)
	plaintext := []byte("copy the key")

	e, err := NewEncryptor(key)
	require.NoError(t, err)

	key[0] ^= 0xFF

	var out bytes.Buffer
	w := e.EncryptWriter(&out)
	_, err = w.Write(plaintext)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	decrypted, err := decrypt(original, out.Bytes())
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestRoundTrip(t *testing.T) {
	key := newKey(t)

	sizes := map[string]int{
		"empty":                  0,
		"one byte":               1,
		"just under one chunk":   ChunkSize - 1,
		"exactly one chunk":      ChunkSize,
		"just over one chunk":    ChunkSize + 1,
		"several chunks":         3*ChunkSize + 17,
		"exactly several chunks": 4 * ChunkSize,
	}

	for name, size := range sizes {
		t.Run(name, func(t *testing.T) {
			plaintext := randomBytes(t, size)
			decrypted, err := decrypt(key, encrypt(t, key, plaintext))
			require.NoError(t, err)
			assert.True(t, bytes.Equal(plaintext, decrypted))
		})
	}
}

func TestSmallReads(t *testing.T) {
	key := newKey(t)
	plaintext := randomBytes(t, ChunkSize+123)

	r, err := DecryptReader(bytes.NewReader(encrypt(t, key, plaintext)), key)
	require.NoError(t, err)

	var decrypted []byte
	buf := make([]byte, 7)
	for {
		n, err := r.Read(buf)
		decrypted = append(decrypted, buf[:n]...)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
	}

	assert.True(t, bytes.Equal(plaintext, decrypted))
}

func TestStreamFormat(t *testing.T) {
	key := newKey(t)
	plaintext := randomBytes(t, 2*ChunkSize+500)
	data := encrypt(t, key, plaintext)

	assert.Equal(t, []byte{0x4F, 0x44, 0x01}, data[:headerSize])

	chunks, sentinel := parseChunks(t, data)
	require.Len(t, chunks, 3)

	nonces := make(map[string]bool)
	expectedPlaintextSizes := []int{ChunkSize, ChunkSize, 500}
	for i, c := range chunks {
		assert.Equal(t, lengthSize+nonceSize+expectedPlaintextSizes[i]+tagSize, c.length)

		nonce := string(data[c.offset+lengthSize : c.offset+lengthSize+nonceSize])
		assert.False(t, nonces[nonce], "nonce reused in chunk %d", i)
		nonces[nonce] = true
	}

	assert.Equal(t, []byte{0, 0, 0, 0}, data[sentinel:sentinel+lengthSize])
	assert.Len(t, data, sentinel+lengthSize+sha256.Size)

	mac := hmac.New(sha256.New, key)
	mac.Write(data[headerSize:sentinel])
	assert.Equal(t, mac.Sum(nil), data[sentinel+lengthSize:])
}

func TestEmptyStream(t *testing.T) {
	key := newKey(t)
	data := encrypt(t, key, nil)

	assert.Len(t, data, headerSize+lengthSize+sha256.Size)

	decrypted, err := decrypt(key, data)
	require.NoError(t, err)
	assert.Empty(t, decrypted)
}

func TestEncryptionIsNotDeterministic(t *testing.T) {
	key := newKey(t)
	plaintext := []byte("the same plaintext")

	first := encrypt(t, key, plaintext)
	second := encrypt(t, key, plaintext)

	assert.NotEqual(t, first, second)
}

func TestCloseIsIdempotent(t *testing.T) {
	e, err := NewEncryptor(newKey(t))
	require.NoError(t, err)

	var out bytes.Buffer
	w := e.EncryptWriter(&out)
	_, err = w.Write([]byte("hello"))
	require.NoError(t, err)

	require.NoError(t, w.Close())
	written := bytes.Clone(out.Bytes())

	require.NoError(t, w.Close())
	assert.Equal(t, written, out.Bytes())

	_, err = w.Write([]byte("more"))
	assert.Error(t, err)
}

type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func TestEncryptWriterPropagatesWriteErrors(t *testing.T) {
	e, err := NewEncryptor(newKey(t))
	require.NoError(t, err)

	w := e.EncryptWriter(failingWriter{})
	_, err = w.Write(randomBytes(t, ChunkSize+1))
	assert.ErrorIs(t, err, io.ErrClosedPipe)

	assert.ErrorIs(t, w.Close(), io.ErrClosedPipe)
}

func TestDecryptReaderInvalidKey(t *testing.T) {
	_, err := DecryptReader(bytes.NewReader(nil), make([]byte, 16))
	assert.ErrorIs(t, err, ErrInvalidKey)
}

func TestDecryptReaderIsLazy(t *testing.T) {
	r, err := DecryptReader(bytes.NewReader([]byte("not encrypted")), newKey(t))
	require.NoError(t, err)

	_, err = io.ReadAll(r)
	assert.Error(t, err)
}

func TestDecryptInvalidHeader(t *testing.T) {
	key := newKey(t)
	data := encrypt(t, key, []byte("hello"))

	for _, i := range []int{0, 1} {
		tampered := bytes.Clone(data)
		tampered[i] ^= 0xFF

		_, err := decrypt(key, tampered)
		assert.ErrorIs(t, err, ErrInvalidHeader)
		assert.ErrorContains(t, err, "invalid header")
	}
}

func TestDecryptUnsupportedVersion(t *testing.T) {
	key := newKey(t)
	data := encrypt(t, key, []byte("hello"))

	for _, v := range []byte{0x00, 0x02, 0xFF} {
		tampered := bytes.Clone(data)
		tampered[2] = v

		_, err := decrypt(key, tampered)
		assert.ErrorIs(t, err, ErrUnsupportedVersion)
		assert.ErrorContains(t, err, "unsupported version")
	}
}

func TestDecryptTamperedHMAC(t *testing.T) {
	key := newKey(t)
	data := encrypt(t, key, []byte("hello"))
	data[len(data)-1] ^= 0x01

	_, err := decrypt(key, data)
	assert.ErrorIs(t, err, ErrIntegrity)
	assert.ErrorContains(t, err, "integrity")
}

func TestDecryptTamperedCiphertext(t *testing.T) {
	key := newKey(t)
	data := encrypt(t, key, []byte("hello, this is a secret"))
	chunks, _ := parseChunks(t, data)
	data[chunks[0].offset+lengthSize+nonceSize] ^= 0x01

	_, err := decrypt(key, data)
	assert.ErrorIs(t, err, ErrIntegrity)
}

func TestDecryptInvalidChunkLength(t *testing.T) {
	key := newKey(t)
	data := encrypt(t, key, []byte("hello"))

	for _, n := range []uint32{1, minChunkLength - 1, maxChunkLength + 1} {
		tampered := bytes.Clone(data)
		binary.BigEndian.PutUint32(tampered[headerSize:], n)

		_, err := decrypt(key, tampered)
		assert.ErrorIs(t, err, ErrIntegrity, "chunk length %d", n)
	}
}

func TestDecryptReorderedChunks(t *testing.T) {
	key := newKey(t)
	data := encrypt(t, key, randomBytes(t, 2*ChunkSize+1))
	chunks, _ := parseChunks(t, data)
	first, second := chunks[0], chunks[1]
	require.Equal(t, first.length, second.length)

	var reordered []byte
	reordered = append(reordered, data[:first.offset]...)
	reordered = append(reordered, data[second.offset:second.offset+second.length]...)
	reordered = append(reordered, data[first.offset:first.offset+first.length]...)
	reordered = append(reordered, data[second.offset+second.length:]...)
	require.Len(t, reordered, len(data))

	_, err := decrypt(key, reordered)
	assert.ErrorIs(t, err, ErrIntegrity)
}

func TestDecryptDroppedChunk(t *testing.T) {
	key := newKey(t)
	data := encrypt(t, key, randomBytes(t, 2*ChunkSize+1))
	chunks, _ := parseChunks(t, data)

	var dropped []byte
	dropped = append(dropped, data[:chunks[1].offset]...)
	dropped = append(dropped, data[chunks[1].offset+chunks[1].length:]...)

	_, err := decrypt(key, dropped)
	assert.ErrorIs(t, err, ErrIntegrity)
}

func TestDecryptWrongKey(t *testing.T) {
	key := newKey(t)

	for _, plaintext := range [][]byte{nil, []byte("hello")} {
		_, err := decrypt(newKey(t), encrypt(t, key, plaintext))
		assert.Error(t, err)
	}
}

func TestDecryptTruncated(t *testing.T) {
	key := newKey(t)

	data := encrypt(t, key, []byte("a short secret message"))
	for i := 0; i < len(data); i++ {
		_, err := decrypt(key, data[:i])
		assert.Error(t, err, "truncated to %d bytes", i)
	}

	data = encrypt(t, key, randomBytes(t, 2*ChunkSize+1))
	chunks, sentinel := parseChunks(t, data)
	cuts := []int{sentinel, sentinel + lengthSize, len(data) - 1}
	for _, c := range chunks {
		cuts = append(cuts, c.offset, c.offset+2, c.offset+lengthSize+nonceSize, c.offset+c.length-1)
	}

	for _, cut := range cuts {
		_, err := decrypt(key, data[:cut])
		assert.Error(t, err, "truncated to %d bytes", cut)
	}
}

func TestDecryptTruncatedAtChunkBoundary(t *testing.T) {
	key := newKey(t)
	data := encrypt(t, key, randomBytes(t, ChunkSize+1))
	chunks, _ := parseChunks(t, data)

	_, err := decrypt(key, data[:chunks[1].offset])
	assert.ErrorIs(t, err, ErrTruncated)
}
