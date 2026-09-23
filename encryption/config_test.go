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
	assert.NoError(t, Config{}.Validate())
	assert.NoError(t, Config{Enabled: false, KeySource: "nope", Key: "x", Passphrase: "y"}.Validate())

	err := Config{Enabled: true}.Validate()
	require.Error(t, err)

	err = Config{Enabled: true, KeySource: "vault"}.Validate()
	require.Error(t, err)

	assert.NoError(t, Config{Enabled: true, KeySource: "ENV", KeyEnvVar: "DUMP_KEY"}.Validate())
	assert.NoError(t, Config{Enabled: true, KeySource: "File", KeyFile: "/tmp/key"}.Validate())
	assert.NoError(t, Config{Enabled: true, KeySource: "LiTeRaL", Key: "a2V5"}.Validate())
	assert.NoError(t, Config{Enabled: true, KeySource: "derive", Passphrase: "pw", Salt: "c2FsdA=="}.Validate())

	err = Config{Enabled: true, KeySource: "env"}.Validate()
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "mutually exclusive")

	err = Config{Enabled: true, KeySource: "env", KeyEnvVar: "DUMP_KEY", KeyFile: "/tmp/k"}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")

	err = Config{Enabled: true, KeySource: "file", KeyFile: "/tmp/k", Key: "abc"}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")

	err = Config{Enabled: true, KeySource: "literal", Key: "abc", Passphrase: "nope"}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")

	err = Config{Enabled: true, KeySource: "derive", Passphrase: "pw", Salt: "c2FsdA==", KeyEnvVar: "X"}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")

	err = Config{Enabled: true, KeySource: "derive", Passphrase: "   ", Salt: "c2FsdA=="}.Validate()
	require.Error(t, err)
}

func TestLoadKey(t *testing.T) {
	raw := bytes.Repeat([]byte{7}, 32)
	encoded := base64.StdEncoding.EncodeToString(raw)

	t.Setenv("ONEDUMP_TEST_KEY", encoded)
	got, err := LoadKey(Config{KeySource: "env", KeyEnvVar: "ONEDUMP_TEST_KEY"})
	require.NoError(t, err)
	assert.Equal(t, raw, got)

	_, err = LoadKey(Config{KeySource: "env", KeyEnvVar: "ONEDUMP_TEST_KEY_MISSING"})
	require.Error(t, err)

	dir := t.TempDir()
	path := filepath.Join(dir, "key")
	require.NoError(t, os.WriteFile(path, []byte("  "+encoded+"\n"), 0o600))
	got, err = LoadKey(Config{KeySource: "file", KeyFile: path})
	require.NoError(t, err)
	assert.Equal(t, raw, got)

	got, err = LoadKey(Config{KeySource: "literal", Key: encoded})
	require.NoError(t, err)
	assert.Equal(t, raw, got)

	_, err = LoadKey(Config{KeySource: "literal", Key: base64.StdEncoding.EncodeToString([]byte("short"))})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidKey)

	salt := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 16))
	d1, err := LoadKey(Config{KeySource: "derive", Passphrase: "correct horse", Salt: salt})
	require.NoError(t, err)
	d2, err := LoadKey(Config{KeySource: "Derive", Passphrase: "correct horse", Salt: salt})
	require.NoError(t, err)
	assert.Equal(t, d1, d2)
	assert.Len(t, d1, 32)

	d3, err := LoadKey(Config{KeySource: "derive", Passphrase: "other", Salt: salt})
	require.NoError(t, err)
	assert.NotEqual(t, d1, d3)

	_, err = LoadKey(Config{KeySource: "derive", Passphrase: "", Salt: salt})
	require.Error(t, err)

	_, err = LoadKey(Config{KeySource: "derive", Passphrase: "pw", Salt: base64.StdEncoding.EncodeToString([]byte("too-short"))})
	require.Error(t, err)
}
