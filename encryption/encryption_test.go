package encryption

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return key
}

func TestNewEncryptorInvalidKey(t *testing.T) {
	_, err := NewEncryptor([]byte("short"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidKey)
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := testKey(t)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	plaintext := []byte("hello world " + strings.Repeat("x", 70000))

	var buf bytes.Buffer
	w := enc.EncryptWriter(&buf)
	_, err = w.Write(plaintext)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	require.NoError(t, w.Close())

	reader, err := DecryptReader(&buf, key)
	require.NoError(t, err)

	decrypted, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestEncryptDecryptWithGzipRoundTrip(t *testing.T) {
	key := testKey(t)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	plaintext := []byte("gzip me " + strings.Repeat("y", 1000))

	var encrypted bytes.Buffer
	ew := enc.EncryptWriter(&encrypted)
	gw := gzip.NewWriter(ew)
	_, err = gw.Write(plaintext)
	require.NoError(t, err)
	require.NoError(t, gw.Close())
	require.NoError(t, ew.Close())

	reader, err := DecryptReader(&encrypted, key)
	require.NoError(t, err)

	gr, err := gzip.NewReader(reader)
	require.NoError(t, err)
	decrypted, err := io.ReadAll(gr)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestTwoEncryptionsDiffer(t *testing.T) {
	key := testKey(t)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	plaintext := []byte("same plaintext")

	var buf1, buf2 bytes.Buffer
	w1 := enc.EncryptWriter(&buf1)
	_, err = w1.Write(plaintext)
	require.NoError(t, err)
	require.NoError(t, w1.Close())

	w2 := enc.EncryptWriter(&buf2)
	_, err = w2.Write(plaintext)
	require.NoError(t, err)
	require.NoError(t, w2.Close())

	assert.NotEqual(t, buf1.Bytes(), buf2.Bytes())
}

func TestDecryptInvalidHeader(t *testing.T) {
	key := testKey(t)
	data := []byte{0x00, 0x00, 0x01}
	reader, err := DecryptReader(bytes.NewReader(data), key)
	require.NoError(t, err)

	_, err = io.ReadAll(reader)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid header")
}

func TestDecryptUnsupportedVersion(t *testing.T) {
	key := testKey(t)
	data := []byte{magic0, magic1, 0x02}
	reader, err := DecryptReader(bytes.NewReader(data), key)
	require.NoError(t, err)

	_, err = io.ReadAll(reader)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported version")
}

func TestDecryptIntegrityFailure(t *testing.T) {
	key := testKey(t)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	var buf bytes.Buffer
	w := enc.EncryptWriter(&buf)
	_, err = w.Write([]byte("tamper me"))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	ciphertext := buf.Bytes()
	ciphertext[len(ciphertext)-1] ^= 0xff

	reader, err := DecryptReader(bytes.NewReader(ciphertext), key)
	require.NoError(t, err)

	_, err = io.ReadAll(reader)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "integrity")
}

func TestDecryptWrongKey(t *testing.T) {
	key := testKey(t)
	wrongKey := testKey(t)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	var buf bytes.Buffer
	w := enc.EncryptWriter(&buf)
	_, err = w.Write([]byte("secret"))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	reader, err := DecryptReader(&buf, wrongKey)
	require.NoError(t, err)

	_, err = io.ReadAll(reader)
	require.Error(t, err)
}

func TestDecryptTruncatedData(t *testing.T) {
	key := testKey(t)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	var buf bytes.Buffer
	w := enc.EncryptWriter(&buf)
	_, err = w.Write([]byte("data"))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	truncated := buf.Bytes()[:len(buf.Bytes())-10]
	reader, err := DecryptReader(bytes.NewReader(truncated), key)
	require.NoError(t, err)

	_, err = io.ReadAll(reader)
	require.Error(t, err)
}

func TestConfigValidateDisabled(t *testing.T) {
	cfg := Config{Enabled: false, KeySource: ""}
	assert.NoError(t, cfg.Validate())
}

func TestConfigValidateEnvSource(t *testing.T) {
	key := testKey(t)
	encoded := base64.StdEncoding.EncodeToString(key)

	cfg := Config{
		Enabled:   true,
		KeySource: "ENV",
		KeyEnvVar: "TEST_KEY",
		KeyFile:   "file",
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")

	cfg = Config{
		Enabled:   true,
		KeySource: "env",
		KeyEnvVar: "TEST_KEY",
	}
	assert.NoError(t, cfg.Validate())

	t.Setenv("TEST_KEY", encoded)
	loaded, err := LoadKey(cfg)
	require.NoError(t, err)
	assert.Equal(t, key, loaded)
}

func TestConfigValidateDeriveSource(t *testing.T) {
	salt := make([]byte, 16)
	_, err := rand.Read(salt)
	require.NoError(t, err)

	cfg := Config{
		Enabled:    true,
		KeySource:  "derive",
		Passphrase: "secret",
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Key:        "other",
	}
	err = cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")

	cfg.Key = ""
	assert.NoError(t, cfg.Validate())

	key1, err := LoadKey(cfg)
	require.NoError(t, err)
	key2, err := LoadKey(cfg)
	require.NoError(t, err)
	assert.Equal(t, key1, key2)
}

func TestLoadKeyMissingEnv(t *testing.T) {
	cfg := Config{
		KeySource: "env",
		KeyEnvVar: "MISSING_ONEDUMP_KEY",
	}
	_, err := LoadKey(cfg)
	require.Error(t, err)
	assert.True(t, strings.Contains(strings.ToLower(err.Error()), "encryption") ||
		strings.Contains(strings.ToLower(err.Error()), "key"))
}

func TestLoadKeyFromFile(t *testing.T) {
	key := testKey(t)
	encoded := base64.StdEncoding.EncodeToString(key)

	f, err := os.CreateTemp("", "key-*")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	_, err = f.WriteString("  " + encoded + "  \n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	cfg := Config{KeySource: "file", KeyFile: f.Name()}
	loaded, err := LoadKey(cfg)
	require.NoError(t, err)
	assert.Equal(t, key, loaded)
}

func TestLoadKeyDeriveRejectsEmptyPassphrase(t *testing.T) {
	salt := base64.StdEncoding.EncodeToString(make([]byte, 16))
	cfg := Config{KeySource: "derive", Passphrase: "", Salt: salt}
	_, err := LoadKey(cfg)
	require.Error(t, err)
}

func TestLoadKeyDeriveRejectsShortSalt(t *testing.T) {
	cfg := Config{
		KeySource:  "derive",
		Passphrase: "secret",
		Salt:       base64.StdEncoding.EncodeToString([]byte("short")),
	}
	_, err := LoadKey(cfg)
	require.Error(t, err)
}

func TestDecryptReaderInvalidKeyLength(t *testing.T) {
	_, err := DecryptReader(strings.NewReader(""), []byte("short"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidKey)
}

func TestConfigValidateUnsupportedSource(t *testing.T) {
	cfg := Config{Enabled: true, KeySource: "unknown"}
	err := cfg.Validate()
	require.Error(t, err)
}

func TestConfigValidateEmptySourceWhenEnabled(t *testing.T) {
	cfg := Config{Enabled: true}
	err := cfg.Validate()
	require.Error(t, err)
}

func TestLoadKeyLiteral(t *testing.T) {
	key := testKey(t)
	cfg := Config{
		KeySource: "literal",
		Key:       base64.StdEncoding.EncodeToString(key),
	}
	loaded, err := LoadKey(cfg)
	require.NoError(t, err)
	assert.Equal(t, key, loaded)
}

func TestLoadKeyInvalidBase64Key(t *testing.T) {
	cfg := Config{KeySource: "literal", Key: "not-valid-base64!!!"}
	_, err := LoadKey(cfg)
	require.Error(t, err)
}

func TestErrInvalidKeyWrapping(t *testing.T) {
	cfg := Config{KeySource: "literal", Key: base64.StdEncoding.EncodeToString([]byte("tooshort"))}
	_, err := LoadKey(cfg)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidKey))
}
