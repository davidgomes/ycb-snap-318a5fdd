package encryption

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigValidate(t *testing.T) {
	disabled := Config{Enabled: false, KeySource: "nope", Key: "x", KeyFile: "y"}
	assert.NoError(t, disabled.Validate())

	assert.Error(t, Config{Enabled: true}.Validate())
	assert.Error(t, Config{Enabled: true, KeySource: "kms", Key: "x"}.Validate())

	valid := []Config{
		{Enabled: true, KeySource: "ENV", KeyEnvVar: "ONEDUMP_KEY"},
		{Enabled: true, KeySource: "file", KeyFile: "/tmp/key"},
		{Enabled: true, KeySource: "Literal", Key: "aaaa"},
		{Enabled: true, KeySource: " derive ", Passphrase: "pw", Salt: "c2FsdA=="},
	}
	for _, cfg := range valid {
		assert.NoError(t, cfg.Validate(), cfg.KeySource)
	}

	exclusive := []Config{
		{Enabled: true, KeySource: "env", KeyEnvVar: "K", KeyFile: "f"},
		{Enabled: true, KeySource: "file", KeyFile: "f", Key: "abc"},
		{Enabled: true, KeySource: "literal", Key: "abc", Passphrase: "pw"},
		{Enabled: true, KeySource: "derive", Passphrase: "pw", Salt: "c2FsdA==", KeyEnvVar: "K"},
		{Enabled: true, KeySource: "env", KeyFile: "only-foreign"},
	}
	for _, cfg := range exclusive {
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mutually exclusive")
	}

	assert.Error(t, Config{Enabled: true, KeySource: "env"}.Validate())
	assert.Error(t, Config{Enabled: true, KeySource: "file"}.Validate())
	assert.Error(t, Config{Enabled: true, KeySource: "literal"}.Validate())
	assert.Error(t, Config{Enabled: true, KeySource: "derive", Passphrase: "pw"}.Validate())
	assert.Error(t, Config{Enabled: true, KeySource: "derive", Salt: "c2FsdA=="}.Validate())
}

func TestLoadKeySources(t *testing.T) {
	raw := bytes.Repeat([]byte{0xab}, 32)
	encoded := base64.StdEncoding.EncodeToString(raw)

	t.Setenv("ONEDUMP_TEST_KEY", encoded)
	got, err := LoadKey(Config{KeySource: "env", KeyEnvVar: "ONEDUMP_TEST_KEY"})
	require.NoError(t, err)
	assert.Equal(t, raw, got)

	_, err = LoadKey(Config{KeySource: "env", KeyEnvVar: "ONEDUMP_TEST_KEY_UNSET"})
	require.Error(t, err)
	msg := strings.ToLower(err.Error())
	assert.True(t, strings.Contains(msg, "encryption") || strings.Contains(msg, "key"), msg)

	dir := t.TempDir()
	path := filepath.Join(dir, "key.txt")
	require.NoError(t, os.WriteFile(path, []byte("  "+encoded+"\n"), 0o600))
	got, err = LoadKey(Config{KeySource: "File", KeyFile: path})
	require.NoError(t, err)
	assert.Equal(t, raw, got)

	got, err = LoadKey(Config{KeySource: "literal", Key: encoded})
	require.NoError(t, err)
	assert.Equal(t, raw, got)

	_, err = LoadKey(Config{KeySource: "literal", Key: base64.StdEncoding.EncodeToString([]byte("short"))})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidKey)
}

func TestDeriveKey(t *testing.T) {
	salt := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x2}, 16))
	a, err := LoadKey(Config{KeySource: "derive", Passphrase: "correct horse", Salt: salt})
	require.NoError(t, err)
	b, err := LoadKey(Config{KeySource: "DERIVE", Passphrase: "correct horse", Salt: salt})
	require.NoError(t, err)
	assert.Equal(t, a, b)
	assert.Len(t, a, 32)

	c, err := LoadKey(Config{KeySource: "derive", Passphrase: "other", Salt: salt})
	require.NoError(t, err)
	assert.NotEqual(t, a, c)

	_, err = LoadKey(Config{KeySource: "derive", Passphrase: "", Salt: salt})
	assert.Error(t, err)
	_, err = LoadKey(Config{KeySource: "derive", Passphrase: "   ", Salt: salt})
	assert.Error(t, err)

	short := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 15))
	_, err = LoadKey(Config{KeySource: "derive", Passphrase: "pw", Salt: short})
	assert.Error(t, err)

	exact, err := LoadKey(Config{KeySource: "derive", Passphrase: "pw", Salt: salt})
	require.NoError(t, err)
	assert.Len(t, exact, 32)
}
