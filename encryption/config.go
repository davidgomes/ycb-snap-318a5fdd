package encryption

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/hkdf"
)

const (
	KeySourceEnv     = "env"
	KeySourceFile    = "file"
	KeySourceLiteral = "literal"
	KeySourceDerive  = "derive"
)

// Config selects how a job obtains its 32-byte encryption key.
type Config struct {
	Enabled    bool   `yaml:"enabled"`
	KeySource  string `yaml:"keysource"`
	KeyEnvVar  string `yaml:"keyenvvar"`
	KeyFile    string `yaml:"keyfile"`
	Key        string `yaml:"key"`
	Passphrase string `yaml:"passphrase"`
	Salt       string `yaml:"salt"`
}

// Validate checks key-source fields when encryption is enabled.
// Disabled configs are always valid.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}

	source := strings.ToLower(strings.TrimSpace(c.KeySource))
	switch source {
	case "":
		return fmt.Errorf("encryption key source is empty")
	case KeySourceEnv, KeySourceFile, KeySourceLiteral, KeySourceDerive:
	default:
		return fmt.Errorf("unsupported encryption key source %q", c.KeySource)
	}

	owned := sourceFields(source)
	set := configuredFields(c)

	var foreign []string
	for name, present := range set {
		if !present || owned[name] {
			continue
		}
		foreign = append(foreign, name)
	}
	if len(foreign) > 0 {
		return fmt.Errorf("encryption key fields are mutually exclusive")
	}

	for name := range owned {
		if !set[name] {
			return fmt.Errorf("encryption %s is required for key source %s", name, source)
		}
	}

	return nil
}

func sourceFields(source string) map[string]bool {
	switch source {
	case KeySourceEnv:
		return map[string]bool{"keyenvvar": true}
	case KeySourceFile:
		return map[string]bool{"keyfile": true}
	case KeySourceLiteral:
		return map[string]bool{"key": true}
	case KeySourceDerive:
		return map[string]bool{"passphrase": true, "salt": true}
	default:
		return nil
	}
}

func configuredFields(c Config) map[string]bool {
	return map[string]bool{
		"keyenvvar":  strings.TrimSpace(c.KeyEnvVar) != "",
		"keyfile":    strings.TrimSpace(c.KeyFile) != "",
		"key":        strings.TrimSpace(c.Key) != "",
		"passphrase": strings.TrimSpace(c.Passphrase) != "",
		"salt":       strings.TrimSpace(c.Salt) != "",
	}
}

// LoadKey resolves cfg to a 32-byte key.
// env, file, and literal sources are base64. derive is a deterministic
// HKDF-SHA256 of the passphrase and the decoded salt.
func LoadKey(cfg Config) ([]byte, error) {
	source := strings.ToLower(strings.TrimSpace(cfg.KeySource))
	switch source {
	case KeySourceEnv:
		name := strings.TrimSpace(cfg.KeyEnvVar)
		if name == "" {
			return nil, fmt.Errorf("encryption key env var is unset")
		}
		val, ok := os.LookupEnv(name)
		if !ok {
			return nil, fmt.Errorf("encryption key env var %q is unset", name)
		}
		return decodeKey(val)
	case KeySourceFile:
		path := strings.TrimSpace(cfg.KeyFile)
		if path == "" {
			return nil, fmt.Errorf("encryption key file is empty")
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("encryption key file: %w", err)
		}
		return decodeKey(string(content))
	case KeySourceLiteral:
		return decodeKey(cfg.Key)
	case KeySourceDerive:
		return deriveKey(cfg.Passphrase, cfg.Salt)
	case "":
		return nil, fmt.Errorf("encryption key source is empty")
	default:
		return nil, fmt.Errorf("unsupported encryption key source %q", cfg.KeySource)
	}
}

func decodeKey(encoded string) ([]byte, error) {
	key, err := decodeBase64(encoded)
	if err != nil {
		return nil, fmt.Errorf("encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: expected 32 bytes, got %d", ErrInvalidKey, len(key))
	}
	return key, nil
}

func deriveKey(passphrase, encodedSalt string) ([]byte, error) {
	if strings.TrimSpace(passphrase) == "" {
		return nil, fmt.Errorf("encryption passphrase is empty")
	}

	salt, err := decodeBase64(encodedSalt)
	if err != nil {
		return nil, fmt.Errorf("encryption salt: %w", err)
	}
	if len(salt) < 16 {
		return nil, fmt.Errorf("encryption salt must be at least 16 bytes, got %d", len(salt))
	}

	r := hkdf.New(sha256.New, []byte(passphrase), salt, []byte("onedump/encryption/v1"))
	key := make([]byte, 32)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("encryption key derivation: %w", err)
	}
	return key, nil
}

func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}

	var last error
	for _, enc := range encodings {
		decoded, err := enc.DecodeString(s)
		if err == nil {
			return decoded, nil
		}
		last = err
	}
	return nil, last
}
