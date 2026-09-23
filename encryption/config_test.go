package encryption

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testKeyB64  = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	testSaltB64 = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
)

func TestValidate(t *testing.T) {
	valid := []Config{
		{},
		{Enabled: false, KeySource: "bogus", Key: "x", Passphrase: "y"},
		{Enabled: true, KeySource: "env", KeyEnvVar: "ONEDUMP_KEY"},
		{Enabled: true, KeySource: "ENV", KeyEnvVar: "ONEDUMP_KEY"},
		{Enabled: true, KeySource: "File", KeyFile: "/tmp/key"},
		{Enabled: true, KeySource: "literal", Key: testKeyB64},
		{Enabled: true, KeySource: "Derive", Passphrase: "secret", Salt: testSaltB64},
	}

	for _, cfg := range valid {
		assert.NoError(t, cfg.Validate(), "%+v", cfg)
	}

	invalid := []struct {
		cfg      Config
		contains string
	}{
		{Config{Enabled: true}, "key source is required"},
		{Config{Enabled: true, KeySource: "vault"}, "unsupported key source"},
		{Config{Enabled: true, KeySource: "env"}, "keyenvvar is required"},
		{Config{Enabled: true, KeySource: "file"}, "keyfile is required"},
		{Config{Enabled: true, KeySource: "literal"}, "key is required"},
		{Config{Enabled: true, KeySource: "derive", Salt: testSaltB64}, "passphrase is required"},
		{Config{Enabled: true, KeySource: "derive", Passphrase: "secret"}, "salt is required"},
		{Config{Enabled: true, KeySource: "env", KeyEnvVar: "K", Key: testKeyB64}, "mutually exclusive"},
		{Config{Enabled: true, KeySource: "env", KeyEnvVar: "K", KeyFile: "/tmp/key"}, "mutually exclusive"},
		{Config{Enabled: true, KeySource: "file", KeyFile: "/tmp/key", Passphrase: "secret"}, "mutually exclusive"},
		{Config{Enabled: true, KeySource: "literal", Key: testKeyB64, Salt: testSaltB64}, "mutually exclusive"},
		{Config{Enabled: true, KeySource: "derive", Passphrase: "p", Salt: testSaltB64, KeyEnvVar: "K"}, "mutually exclusive"},
	}

	for _, tc := range invalid {
		assert.ErrorContains(t, tc.cfg.Validate(), tc.contains, "%+v", tc.cfg)
	}
}

func TestLoadKeyEnv(t *testing.T) {
	t.Setenv("ONEDUMP_TEST_KEY", testKeyB64)
	key, err := LoadKey(Config{KeySource: "env", KeyEnvVar: "ONEDUMP_TEST_KEY"})
	require.NoError(t, err)
	assert.Len(t, key, KeySize)

	_, err = LoadKey(Config{KeySource: "env", KeyEnvVar: "ONEDUMP_TEST_KEY_UNSET"})
	assert.ErrorContains(t, err, "not set")

	t.Setenv("ONEDUMP_TEST_KEY", base64.StdEncoding.EncodeToString([]byte("short")))
	_, err = LoadKey(Config{KeySource: "env", KeyEnvVar: "ONEDUMP_TEST_KEY"})
	assert.ErrorIs(t, err, ErrInvalidKey)
}

func TestLoadKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	require.NoError(t, os.WriteFile(path, []byte("  "+testKeyB64+"\n"), 0600))

	key, err := LoadKey(Config{KeySource: "FILE", KeyFile: path})
	require.NoError(t, err)
	assert.Equal(t, []byte("0123456789abcdef0123456789abcdef"), key)

	_, err = LoadKey(Config{KeySource: "file", KeyFile: filepath.Join(t.TempDir(), "missing")})
	assert.Error(t, err)
}

func TestLoadKeyLiteral(t *testing.T) {
	key, err := LoadKey(Config{KeySource: "literal", Key: testKeyB64})
	require.NoError(t, err)
	assert.Equal(t, []byte("0123456789abcdef0123456789abcdef"), key)

	_, err = LoadKey(Config{KeySource: "literal", Key: "not base64!"})
	assert.ErrorIs(t, err, ErrInvalidKey)
}

func TestLoadKeyDerive(t *testing.T) {
	cfg := Config{KeySource: "derive", Passphrase: "secret", Salt: testSaltB64}

	first, err := LoadKey(cfg)
	require.NoError(t, err)
	assert.Len(t, first, KeySize)

	second, err := LoadKey(cfg)
	require.NoError(t, err)
	assert.Equal(t, first, second)

	other, err := LoadKey(Config{KeySource: "derive", Passphrase: "other", Salt: testSaltB64})
	require.NoError(t, err)
	assert.NotEqual(t, first, other)

	_, err = LoadKey(Config{KeySource: "derive", Passphrase: "", Salt: testSaltB64})
	assert.ErrorContains(t, err, "passphrase")

	_, err = LoadKey(Config{KeySource: "derive", Passphrase: "secret", Salt: base64.StdEncoding.EncodeToString([]byte("short"))})
	assert.ErrorContains(t, err, "salt")
}

func TestLoadKeyUnsupportedSource(t *testing.T) {
	_, err := LoadKey(Config{KeySource: "vault"})
	assert.ErrorContains(t, err, "unsupported key source")

	_, err = LoadKey(Config{})
	assert.Error(t, err)
}
