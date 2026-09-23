package encryption

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	KeySourceEnv     = "env"
	KeySourceFile    = "file"
	KeySourceLiteral = "literal"
	KeySourceDerive  = "derive"

	MinSaltSize = 16
)

type Config struct {
	Enabled    bool   `yaml:"enabled"`
	KeySource  string `yaml:"keysource"`
	KeyEnvVar  string `yaml:"keyenvvar"`
	KeyFile    string `yaml:"keyfile"`
	Key        string `yaml:"key"`
	Passphrase string `yaml:"passphrase"`
	Salt       string `yaml:"salt"`
}

func (cfg Config) source() string {
	return strings.ToLower(strings.TrimSpace(cfg.KeySource))
}

type sourceField struct {
	name   string
	source string
	value  string
}

func (cfg Config) fields() []sourceField {
	return []sourceField{
		{"keyenvvar", KeySourceEnv, cfg.KeyEnvVar},
		{"keyfile", KeySourceFile, cfg.KeyFile},
		{"key", KeySourceLiteral, cfg.Key},
		{"passphrase", KeySourceDerive, cfg.Passphrase},
		{"salt", KeySourceDerive, cfg.Salt},
	}
}

func (cfg Config) Validate() error {
	if !cfg.Enabled {
		return nil
	}

	source := cfg.source()

	switch source {
	case "":
		return errors.New("encryption key source is required when encryption is enabled")
	case KeySourceEnv, KeySourceFile, KeySourceLiteral, KeySourceDerive:
	default:
		return fmt.Errorf("unsupported encryption key source %q", cfg.KeySource)
	}

	var errs error
	for _, f := range cfg.fields() {
		hasValue := strings.TrimSpace(f.value) != ""

		if f.source == source && !hasValue {
			errs = errors.Join(errs, fmt.Errorf("encryption %s is required for key source %q", f.name, source))
		}

		if f.source != source && hasValue {
			errs = errors.Join(errs, fmt.Errorf("encryption %s is mutually exclusive with key source %q", f.name, source))
		}
	}

	return errs
}

// LoadKey loads a 32-byte encryption key from the source configured in cfg.
func LoadKey(cfg Config) ([]byte, error) {
	switch source := cfg.source(); source {
	case KeySourceEnv:
		name := strings.TrimSpace(cfg.KeyEnvVar)
		if name == "" {
			return nil, errors.New("encryption key env var name is required")
		}

		value, ok := os.LookupEnv(name)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("encryption key env var %s is not set", name)
		}

		return decodeKey(value)
	case KeySourceFile:
		path := strings.TrimSpace(cfg.KeyFile)
		if path == "" {
			return nil, errors.New("encryption key file is required")
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read encryption key file: %w", err)
		}

		return decodeKey(string(content))
	case KeySourceLiteral:
		if strings.TrimSpace(cfg.Key) == "" {
			return nil, errors.New("encryption key is required")
		}

		return decodeKey(cfg.Key)
	case KeySourceDerive:
		return deriveKey(cfg.Passphrase, cfg.Salt)
	case "":
		return nil, errors.New("encryption key source is required")
	default:
		return nil, fmt.Errorf("unsupported encryption key source %q", cfg.KeySource)
	}
}

func decodeBase64(value string) ([]byte, error) {
	value = strings.TrimSpace(value)

	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(value)
	}

	return decoded, err
}

func decodeKey(value string) ([]byte, error) {
	key, err := decodeBase64(value)
	if err != nil {
		return nil, fmt.Errorf("%w: key is not valid base64: %v", ErrInvalidKey, err)
	}

	if len(key) != KeySize {
		return nil, fmt.Errorf("%w: expected %d bytes, got %d", ErrInvalidKey, KeySize, len(key))
	}

	return key, nil
}

func deriveKey(passphrase, encodedSalt string) ([]byte, error) {
	if passphrase == "" {
		return nil, errors.New("encryption passphrase is required to derive a key")
	}

	salt, err := decodeBase64(encodedSalt)
	if err != nil {
		return nil, fmt.Errorf("encryption salt is not valid base64: %w", err)
	}

	if len(salt) < MinSaltSize {
		return nil, fmt.Errorf("encryption salt must be at least %d bytes, got %d", MinSaltSize, len(salt))
	}

	return argon2.IDKey([]byte(passphrase), salt, 1, 64*1024, 4, KeySize), nil
}
