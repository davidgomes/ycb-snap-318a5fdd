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

var testSalt = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{name: "disabled empty", config: Config{}},
		{name: "disabled ignores fields", config: Config{KeySource: "unknown", Key: "k", KeyFile: "f"}},
		{name: "enabled without source", config: Config{Enabled: true}, wantErr: "key source is required"},
		{name: "enabled with blank source", config: Config{Enabled: true, KeySource: "  "}, wantErr: "key source is required"},
		{name: "unsupported source", config: Config{Enabled: true, KeySource: "vault"}, wantErr: "unsupported key source"},

		{name: "env", config: Config{Enabled: true, KeySource: "env", KeyEnvVar: "ONEDUMP_KEY"}},
		{name: "env case insensitive", config: Config{Enabled: true, KeySource: " ENV ", KeyEnvVar: "ONEDUMP_KEY"}},
		{name: "env missing var", config: Config{Enabled: true, KeySource: "env"}, wantErr: "requires keyenvvar"},
		{name: "env with keyfile", config: Config{Enabled: true, KeySource: "env", KeyEnvVar: "ONEDUMP_KEY", KeyFile: "/key"}, wantErr: "mutually exclusive"},
		{name: "env with other source only", config: Config{Enabled: true, KeySource: "env", Key: "a2V5"}, wantErr: "mutually exclusive"},

		{name: "file", config: Config{Enabled: true, KeySource: "file", KeyFile: "/key"}},
		{name: "file case insensitive", config: Config{Enabled: true, KeySource: "File", KeyFile: "/key"}},
		{name: "file missing path", config: Config{Enabled: true, KeySource: "file"}, wantErr: "requires keyfile"},
		{name: "file with key", config: Config{Enabled: true, KeySource: "file", KeyFile: "/key", Key: "a2V5"}, wantErr: "mutually exclusive"},

		{name: "literal", config: Config{Enabled: true, KeySource: "literal", Key: "a2V5"}},
		{name: "literal case insensitive", config: Config{Enabled: true, KeySource: "LITERAL", Key: "a2V5"}},
		{name: "literal missing key", config: Config{Enabled: true, KeySource: "literal"}, wantErr: "requires key"},
		{name: "literal with passphrase", config: Config{Enabled: true, KeySource: "literal", Key: "a2V5", Passphrase: "secret"}, wantErr: "mutually exclusive"},
		{name: "literal with env var", config: Config{Enabled: true, KeySource: "literal", Key: "a2V5", KeyEnvVar: "ONEDUMP_KEY"}, wantErr: "mutually exclusive"},

		{name: "derive", config: Config{Enabled: true, KeySource: "derive", Passphrase: "secret", Salt: testSalt}},
		{name: "derive case insensitive", config: Config{Enabled: true, KeySource: "Derive", Passphrase: "secret", Salt: testSalt}},
		{name: "derive missing passphrase", config: Config{Enabled: true, KeySource: "derive", Salt: testSalt}, wantErr: "requires passphrase"},
		{name: "derive missing salt", config: Config{Enabled: true, KeySource: "derive", Passphrase: "secret"}, wantErr: "requires salt"},
		{name: "derive with key", config: Config{Enabled: true, KeySource: "derive", Passphrase: "secret", Salt: testSalt, Key: "a2V5"}, wantErr: "mutually exclusive"},
		{name: "derive with keyfile", config: Config{Enabled: true, KeySource: "derive", Passphrase: "secret", Salt: testSalt, KeyFile: "/key"}, wantErr: "mutually exclusive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}

func encodedKey(t *testing.T) ([]byte, string) {
	t.Helper()
	key := newKey(t)
	return key, base64.StdEncoding.EncodeToString(key)
}

func TestLoadKeyFromEnv(t *testing.T) {
	key, encoded := encodedKey(t)

	t.Setenv("ONEDUMP_TEST_ENCRYPTION_KEY", encoded)
	loaded, err := LoadKey(Config{Enabled: true, KeySource: "env", KeyEnvVar: "ONEDUMP_TEST_ENCRYPTION_KEY"})
	require.NoError(t, err)
	assert.Equal(t, key, loaded)

	loaded, err = LoadKey(Config{Enabled: true, KeySource: "ENV", KeyEnvVar: "ONEDUMP_TEST_ENCRYPTION_KEY"})
	require.NoError(t, err)
	assert.Equal(t, key, loaded)

	_, err = LoadKey(Config{Enabled: true, KeySource: "env", KeyEnvVar: "ONEDUMP_TEST_ENCRYPTION_KEY_UNSET"})
	assert.ErrorContains(t, err, "not set")

	t.Setenv("ONEDUMP_TEST_ENCRYPTION_KEY", "")
	_, err = LoadKey(Config{Enabled: true, KeySource: "env", KeyEnvVar: "ONEDUMP_TEST_ENCRYPTION_KEY"})
	assert.ErrorContains(t, err, "not set")

	t.Setenv("ONEDUMP_TEST_ENCRYPTION_KEY", "not base64!")
	_, err = LoadKey(Config{Enabled: true, KeySource: "env", KeyEnvVar: "ONEDUMP_TEST_ENCRYPTION_KEY"})
	assert.ErrorIs(t, err, ErrInvalidKey)

	t.Setenv("ONEDUMP_TEST_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 16)))
	_, err = LoadKey(Config{Enabled: true, KeySource: "env", KeyEnvVar: "ONEDUMP_TEST_ENCRYPTION_KEY"})
	assert.ErrorIs(t, err, ErrInvalidKey)
}

func TestLoadKeyFromFile(t *testing.T) {
	key, encoded := encodedKey(t)
	dir := t.TempDir()

	keyFile := filepath.Join(dir, "key")
	require.NoError(t, os.WriteFile(keyFile, []byte("  "+encoded+"\n"), 0600))

	loaded, err := LoadKey(Config{Enabled: true, KeySource: "file", KeyFile: keyFile})
	require.NoError(t, err)
	assert.Equal(t, key, loaded)

	_, err = LoadKey(Config{Enabled: true, KeySource: "file", KeyFile: filepath.Join(dir, "missing")})
	assert.Error(t, err)

	shortKeyFile := filepath.Join(dir, "short")
	require.NoError(t, os.WriteFile(shortKeyFile, []byte(base64.StdEncoding.EncodeToString(make([]byte, 31))), 0600))
	_, err = LoadKey(Config{Enabled: true, KeySource: "file", KeyFile: shortKeyFile})
	assert.ErrorIs(t, err, ErrInvalidKey)
}

func TestLoadKeyFromLiteral(t *testing.T) {
	key, encoded := encodedKey(t)

	loaded, err := LoadKey(Config{Enabled: true, KeySource: "literal", Key: encoded})
	require.NoError(t, err)
	assert.Equal(t, key, loaded)

	_, err = LoadKey(Config{Enabled: true, KeySource: "literal", Key: "%%%"})
	assert.ErrorIs(t, err, ErrInvalidKey)

	_, err = LoadKey(Config{Enabled: true, KeySource: "literal", Key: base64.StdEncoding.EncodeToString(make([]byte, 33))})
	assert.ErrorIs(t, err, ErrInvalidKey)
}

func TestLoadKeyDerive(t *testing.T) {
	cfg := Config{Enabled: true, KeySource: "derive", Passphrase: "correct horse battery staple", Salt: testSalt}

	first, err := LoadKey(cfg)
	require.NoError(t, err)
	assert.Len(t, first, KeySize)

	second, err := LoadKey(cfg)
	require.NoError(t, err)
	assert.Equal(t, first, second)

	otherPassphrase := cfg
	otherPassphrase.Passphrase = "another passphrase"
	derived, err := LoadKey(otherPassphrase)
	require.NoError(t, err)
	assert.False(t, bytes.Equal(first, derived))

	otherSalt := cfg
	otherSalt.Salt = base64.StdEncoding.EncodeToString([]byte("fedcba9876543210"))
	derived, err = LoadKey(otherSalt)
	require.NoError(t, err)
	assert.False(t, bytes.Equal(first, derived))

	emptyPassphrase := cfg
	emptyPassphrase.Passphrase = ""
	_, err = LoadKey(emptyPassphrase)
	assert.ErrorContains(t, err, "passphrase")

	shortSalt := cfg
	shortSalt.Salt = base64.StdEncoding.EncodeToString(make([]byte, MinSaltSize-1))
	_, err = LoadKey(shortSalt)
	assert.ErrorContains(t, err, "salt")

	invalidSalt := cfg
	invalidSalt.Salt = "not base64!"
	_, err = LoadKey(invalidSalt)
	assert.ErrorContains(t, err, "salt")
}

func TestLoadKeyInvalidConfig(t *testing.T) {
	_, err := LoadKey(Config{Enabled: true})
	assert.Error(t, err)

	_, err = LoadKey(Config{Enabled: true, KeySource: "vault"})
	assert.ErrorContains(t, err, "unsupported key source")

	_, err = LoadKey(Config{Enabled: true, KeySource: "literal", Key: "a2V5", KeyFile: "/key"})
	assert.ErrorContains(t, err, "mutually exclusive")
}

func TestLoadedKeyWorksWithEncryptor(t *testing.T) {
	key, err := LoadKey(Config{Enabled: true, KeySource: "derive", Passphrase: "secret", Salt: testSalt})
	require.NoError(t, err)

	plaintext := []byte("database dump")
	decrypted, err := decrypt(key, encrypt(t, key, plaintext))
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}
