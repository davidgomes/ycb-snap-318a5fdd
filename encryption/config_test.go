package encryption

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testKeyB64  = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, KeySize))
	testSaltB64 = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, MinSaltSize))
)

func TestConfigValidate(t *testing.T) {
	valid := []Config{
		{},
		{Enabled: false, KeySource: "bogus", Key: "x", KeyFile: "y"},
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
		{Config{Enabled: true}, "required"},
		{Config{Enabled: true, KeySource: "vault"}, "unsupported"},
		{Config{Enabled: true, KeySource: "env"}, "keyenvvar"},
		{Config{Enabled: true, KeySource: "file"}, "keyfile"},
		{Config{Enabled: true, KeySource: "literal"}, "key"},
		{Config{Enabled: true, KeySource: "derive", Salt: testSaltB64}, "passphrase"},
		{Config{Enabled: true, KeySource: "derive", Passphrase: "secret"}, "salt"},
		{Config{Enabled: true, KeySource: "env", KeyEnvVar: "K", Key: testKeyB64}, "mutually exclusive"},
		{Config{Enabled: true, KeySource: "file", KeyFile: "/tmp/key", KeyEnvVar: "K"}, "mutually exclusive"},
		{Config{Enabled: true, KeySource: "literal", Key: testKeyB64, Passphrase: "p"}, "mutually exclusive"},
		{Config{Enabled: true, KeySource: "derive", Passphrase: "p", Salt: testSaltB64, KeyFile: "f"}, "mutually exclusive"},
	}
	for _, tc := range invalid {
		assert.ErrorContains(t, tc.cfg.Validate(), tc.contains, "%+v", tc.cfg)
	}
}

func TestLoadKeyEnv(t *testing.T) {
	cfg := Config{Enabled: true, KeySource: "env", KeyEnvVar: "ONEDUMP_TEST_ENCRYPTION_KEY"}

	_, err := LoadKey(cfg)
	assert.Error(t, err)

	t.Setenv("ONEDUMP_TEST_ENCRYPTION_KEY", testKeyB64)
	key, err := LoadKey(cfg)
	require.NoError(t, err)
	assert.Len(t, key, KeySize)

	t.Setenv("ONEDUMP_TEST_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("short")))
	_, err = LoadKey(cfg)
	assert.ErrorIs(t, err, ErrInvalidKey)
}

func TestLoadKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	require.NoError(t, os.WriteFile(path, []byte("  "+testKeyB64+"\n"), 0o600))

	key, err := LoadKey(Config{Enabled: true, KeySource: "file", KeyFile: path})
	require.NoError(t, err)
	assert.Equal(t, bytes.Repeat([]byte{0x42}, KeySize), key)

	_, err = LoadKey(Config{Enabled: true, KeySource: "file", KeyFile: path + ".missing"})
	assert.Error(t, err)
}

func TestLoadKeyLiteral(t *testing.T) {
	key, err := LoadKey(Config{Enabled: true, KeySource: "LITERAL", Key: testKeyB64})
	require.NoError(t, err)
	assert.Len(t, key, KeySize)

	_, err = LoadKey(Config{Enabled: true, KeySource: "literal", Key: "not base64!"})
	assert.ErrorIs(t, err, ErrInvalidKey)
}

func TestLoadKeyDerive(t *testing.T) {
	cfg := Config{Enabled: true, KeySource: "derive", Passphrase: "secret", Salt: testSaltB64}

	k1, err := LoadKey(cfg)
	require.NoError(t, err)
	assert.Len(t, k1, KeySize)

	k2, err := LoadKey(cfg)
	require.NoError(t, err)
	assert.Equal(t, k1, k2)

	cfg.Passphrase = "other"
	k3, err := LoadKey(cfg)
	require.NoError(t, err)
	assert.NotEqual(t, k1, k3)

	_, err = LoadKey(Config{Enabled: true, KeySource: "derive", Passphrase: "", Salt: testSaltB64})
	assert.Error(t, err)

	shortSalt := base64.StdEncoding.EncodeToString(make([]byte, MinSaltSize-1))
	_, err = LoadKey(Config{Enabled: true, KeySource: "derive", Passphrase: "secret", Salt: shortSalt})
	assert.Error(t, err)
}

func TestLoadKeyUnsupportedSource(t *testing.T) {
	_, err := LoadKey(Config{Enabled: true, KeySource: "vault"})
	assert.Error(t, err)

	_, err = LoadKey(Config{Enabled: true})
	assert.Error(t, err)
}
