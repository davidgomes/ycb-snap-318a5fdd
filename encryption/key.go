package encryption

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const (
	derivedKeyLen    = 32
	minSaltLen       = 16
	pbkdf2Iterations = 10000
)

// Config selects how a 32-byte encryption key is supplied.
type Config struct {
	Enabled    bool   `yaml:"enabled"`
	KeySource  string `yaml:"keysource"`
	KeyEnvVar  string `yaml:"keyenvvar"`
	KeyFile    string `yaml:"keyfile"`
	Key        string `yaml:"key"`
	Passphrase string `yaml:"passphrase"`
	Salt       string `yaml:"salt"`
}

// Validate checks key-source settings.
// Disabled configs are always valid. When enabled, KeySource is required and
// matched case-insensitively against env, file, literal, and derive.
// Each source requires only its own fields; any other key field is rejected.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}

	source := strings.ToLower(strings.TrimSpace(c.KeySource))
	if source == "" {
		return errors.New("encryption key source is empty")
	}

	required, ok := requiredFields(source)
	if !ok {
		return fmt.Errorf("unsupported encryption key source %q", c.KeySource)
	}

	present := sourceFields{
		envVar:     strings.TrimSpace(c.KeyEnvVar) != "",
		file:       strings.TrimSpace(c.KeyFile) != "",
		key:        strings.TrimSpace(c.Key) != "",
		passphrase: strings.TrimSpace(c.Passphrase) != "",
		salt:       strings.TrimSpace(c.Salt) != "",
	}

	if present.envVar && !required.envVar ||
		present.file && !required.file ||
		present.key && !required.key ||
		present.passphrase && !required.passphrase ||
		present.salt && !required.salt {
		return fmt.Errorf("encryption key source %q fields are mutually exclusive", source)
	}

	switch {
	case required.envVar && !present.envVar:
		return errors.New("encryption key source env requires KeyEnvVar")
	case required.file && !present.file:
		return errors.New("encryption key source file requires KeyFile")
	case required.key && !present.key:
		return errors.New("encryption key source literal requires Key")
	case required.passphrase && !present.passphrase:
		return errors.New("encryption key source derive requires Passphrase")
	case required.salt && !present.salt:
		return errors.New("encryption key source derive requires Salt")
	}

	return nil
}

type sourceFields struct {
	envVar     bool
	file       bool
	key        bool
	passphrase bool
	salt       bool
}

func requiredFields(source string) (sourceFields, bool) {
	switch source {
	case "env":
		return sourceFields{envVar: true}, true
	case "file":
		return sourceFields{file: true}, true
	case "literal":
		return sourceFields{key: true}, true
	case "derive":
		return sourceFields{passphrase: true, salt: true}, true
	default:
		return sourceFields{}, false
	}
}

// LoadKey resolves cfg to a 32-byte key.
// env reads standard base64 from the named environment variable.
// file reads trimmed standard base64 from KeyFile.
// literal decodes Key as standard base64.
// derive returns a deterministic 32-byte PBKDF2-HMAC-SHA256 key from
// Passphrase and a base64 salt of at least 16 decoded bytes.
func LoadKey(cfg Config) ([]byte, error) {
	source := strings.ToLower(strings.TrimSpace(cfg.KeySource))
	switch source {
	case "env":
		val, ok := os.LookupEnv(cfg.KeyEnvVar)
		if !ok || strings.TrimSpace(val) == "" {
			return nil, fmt.Errorf("encryption key environment variable %q is not set", cfg.KeyEnvVar)
		}
		return decodeKey(val)
	case "file":
		b, err := os.ReadFile(cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("read encryption key file: %w", err)
		}
		return decodeKey(string(b))
	case "literal":
		return decodeKey(cfg.Key)
	case "derive":
		return deriveKey(cfg.Passphrase, cfg.Salt)
	default:
		if source == "" {
			return nil, errors.New("encryption key source is empty")
		}
		return nil, fmt.Errorf("unsupported encryption key source %q", cfg.KeySource)
	}
}

func decodeKey(encoded string) ([]byte, error) {
	raw, err := decodeBase64(encoded)
	if err != nil {
		return nil, fmt.Errorf("invalid encryption key: %w", err)
	}
	if len(raw) != derivedKeyLen {
		return nil, fmt.Errorf("%w: decoded length %d", ErrInvalidKey, len(raw))
	}
	return raw, nil
}

func deriveKey(passphrase, saltB64 string) ([]byte, error) {
	if strings.TrimSpace(passphrase) == "" {
		return nil, errors.New("encryption passphrase is empty")
	}

	salt, err := decodeBase64(saltB64)
	if err != nil {
		return nil, fmt.Errorf("invalid encryption salt: %w", err)
	}
	if len(salt) < minSaltLen {
		return nil, fmt.Errorf("encryption salt must be at least %d bytes", minSaltLen)
	}

	key := pbkdf2.Key([]byte(passphrase), salt, pbkdf2Iterations, derivedKeyLen, sha256.New)
	return key, nil
}

func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty base64 value")
	}
	if raw, err := base64.StdEncoding.DecodeString(s); err == nil {
		return raw, nil
	}
	return base64.RawStdEncoding.DecodeString(s)
}
