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

func TestConfigValidate(t *testing.T) {
	disabled := Config{Enabled: false, KeySource: "nope", Key: "x", KeyFile: "y"}
	assert.NoError(t, disabled.Validate())

	empty := Config{Enabled: true}
	err := empty.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")

	unsupported := Config{Enabled: true, KeySource: "kms", Key: "abc"}
	err = unsupported.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported")

	valid := []Config{
		{Enabled: true, KeySource: "ENV", KeyEnvVar: "ONEDUMP_KEY"},
		{Enabled: true, KeySource: "File", KeyFile: "/tmp/key"},
		{Enabled: true, KeySource: "literal", Key: "a2V5"},
		{Enabled: true, KeySource: "Derive", Passphrase: "secret", Salt: "c2FsdA=="},
	}
	for _, cfg := range valid {
		assert.NoError(t, cfg.Validate(), cfg.KeySource)
	}

	exclusive := []Config{
		{Enabled: true, KeySource: "env", KeyEnvVar: "ONEDUMP_KEY", KeyFile: "/tmp/key"},
		{Enabled: true, KeySource: "file", KeyFile: "/tmp/key", Key: "abc"},
		{Enabled: true, KeySource: "literal", Key: "abc", Passphrase: "secret"},
		{Enabled: true, KeySource: "derive", Passphrase: "secret", Salt: "c2FsdA==", KeyEnvVar: "ONEDUMP_KEY"},
		{Enabled: true, KeySource: "env", KeyEnvVar: "ONEDUMP_KEY", Salt: "c2FsdA=="},
	}
	for _, cfg := range exclusive {
		err := cfg.Validate()
		require.Error(t, err, cfg.KeySource)
		assert.Contains(t, err.Error(), "mutually exclusive")
	}

	missing := Config{Enabled: true, KeySource: "derive", Passphrase: "secret"}
	err = missing.Validate()
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "mutually exclusive")
}

func TestLoadKey(t *testing.T) {
	raw := bytes.Repeat([]byte{7}, 32)
	encoded := base64.StdEncoding.EncodeToString(raw)

	t.Run("env", func(t *testing.T) {
		t.Setenv("ONEDUMP_TEST_KEY", encoded+"\n")
		key, err := LoadKey(Config{KeySource: "env", KeyEnvVar: "ONEDUMP_TEST_KEY"})
		require.NoError(t, err)
		assert.Equal(t, raw, key)

		os.Unsetenv("ONEDUMP_TEST_KEY")
		_, err = LoadKey(Config{KeySource: "ENV", KeyEnvVar: "ONEDUMP_TEST_KEY"})
		require.Error(t, err)
		msg := err.Error()
		assert.Contains(t, msg, "encryption")
		assert.Contains(t, msg, "key")
	})

	t.Run("file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "key.txt")
		require.NoError(t, os.WriteFile(path, []byte("  "+encoded+"\n"), 0o600))
		key, err := LoadKey(Config{KeySource: "file", KeyFile: path})
		require.NoError(t, err)
		assert.Equal(t, raw, key)
	})

	t.Run("literal", func(t *testing.T) {
		key, err := LoadKey(Config{KeySource: "literal", Key: " " + encoded + " "})
		require.NoError(t, err)
		assert.Equal(t, raw, key)

		short := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 16))
		_, err = LoadKey(Config{KeySource: "literal", Key: short})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidKey)
	})

	t.Run("derive", func(t *testing.T) {
		salt := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 16))
		cfg := Config{KeySource: "derive", Passphrase: "correct horse", Salt: salt}
		first, err := LoadKey(cfg)
		require.NoError(t, err)
		second, err := LoadKey(cfg)
		require.NoError(t, err)
		assert.Equal(t, first, second)
		assert.Len(t, first, 32)

		cfg.Passphrase = "other"
		other, err := LoadKey(cfg)
		require.NoError(t, err)
		assert.NotEqual(t, first, other)

		_, err = LoadKey(Config{KeySource: "derive", Passphrase: "   ", Salt: salt})
		require.Error(t, err)

		shortSalt := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 15))
		_, err = LoadKey(Config{KeySource: "derive", Passphrase: "secret", Salt: shortSalt})
		require.Error(t, err)
	})
}
