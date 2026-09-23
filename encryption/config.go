package encryption

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	KeySourceEnv     = "env"
	KeySourceFile    = "file"
	KeySourceLiteral = "literal"
	KeySourceDerive  = "derive"

	MinSaltSize = 16

	pbkdf2Iterations = 600_000
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

type configField struct {
	name  string
	value string
}

func (cfg Config) fields() []configField {
	return []configField{
		{"keyenvvar", cfg.KeyEnvVar},
		{"keyfile", cfg.KeyFile},
		{"key", cfg.Key},
		{"passphrase", cfg.Passphrase},
		{"salt", cfg.Salt},
	}
}

var sourceFields = map[string][]string{
	KeySourceEnv:     {"keyenvvar"},
	KeySourceFile:    {"keyfile"},
	KeySourceLiteral: {"key"},
	KeySourceDerive:  {"passphrase", "salt"},
}

// Validate checks that an enabled config names a supported key source, sets the fields that source
// requires and leaves the fields of every other source empty. Disabled configs are always valid.
func (cfg Config) Validate() error {
	if !cfg.Enabled {
		return nil
	}

	source := cfg.source()
	if source == "" {
		return errors.New("encryption: key source is required when encryption is enabled")
	}

	required, ok := sourceFields[source]
	if !ok {
		return fmt.Errorf("encryption: unsupported key source %q, expected one of env, file, literal, derive", cfg.KeySource)
	}

	isRequired := make(map[string]bool, len(required))
	for _, name := range required {
		isRequired[name] = true
	}

	var errs error
	for _, f := range cfg.fields() {
		set := strings.TrimSpace(f.value) != ""

		if isRequired[f.name] && !set {
			errs = errors.Join(errs, fmt.Errorf("encryption: %s is required for key source %q", f.name, source))
		}

		if !isRequired[f.name] && set {
			errs = errors.Join(errs, fmt.Errorf("encryption: %s is mutually exclusive with key source %q", f.name, source))
		}
	}

	return errs
}

// LoadKey resolves the 32-byte encryption key described by cfg.
func LoadKey(cfg Config) ([]byte, error) {
	switch source := cfg.source(); source {
	case KeySourceEnv:
		name := strings.TrimSpace(cfg.KeyEnvVar)
		if name == "" {
			return nil, errors.New("encryption: key env var name is required")
		}

		value, ok := os.LookupEnv(name)
		if !ok {
			return nil, fmt.Errorf("encryption: key env var %s is not set", name)
		}

		return decodeKey(value, "env var "+name)
	case KeySourceFile:
		path := strings.TrimSpace(cfg.KeyFile)
		if path == "" {
			return nil, errors.New("encryption: key file path is required")
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("encryption: failed to read key file: %w", err)
		}

		return decodeKey(string(content), "key file "+path)
	case KeySourceLiteral:
		return decodeKey(cfg.Key, "literal key")
	case KeySourceDerive:
		return deriveKey(cfg.Passphrase, cfg.Salt)
	case "":
		return nil, errors.New("encryption: key source is required")
	default:
		return nil, fmt.Errorf("encryption: unsupported key source %q", cfg.KeySource)
	}
}

func decodeKey(encoded, origin string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, fmt.Errorf("%w: %s is empty", ErrInvalidKey, origin)
	}

	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: %s is not valid base64: %v", ErrInvalidKey, origin, err)
	}

	if len(key) != KeySize {
		return nil, fmt.Errorf("%w: %s must decode to %d bytes, got %d", ErrInvalidKey, origin, KeySize, len(key))
	}

	return key, nil
}

func deriveKey(passphrase, encodedSalt string) ([]byte, error) {
	if passphrase == "" {
		return nil, errors.New("encryption: passphrase is required to derive a key")
	}

	salt, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedSalt))
	if err != nil {
		return nil, fmt.Errorf("encryption: salt is not valid base64: %w", err)
	}

	if len(salt) < MinSaltSize {
		return nil, fmt.Errorf("encryption: salt must be at least %d bytes, got %d", MinSaltSize, len(salt))
	}

	key, err := pbkdf2.Key(sha256.New, passphrase, salt, pbkdf2Iterations, KeySize)
	if err != nil {
		return nil, fmt.Errorf("encryption: failed to derive key: %w", err)
	}

	return key, nil
}
