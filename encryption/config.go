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
	KeySourceEnv     = "env"
	KeySourceFile    = "file"
	KeySourceLiteral = "literal"
	KeySourceDerive  = "derive"
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

func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}

	source := strings.ToLower(strings.TrimSpace(c.KeySource))
	if source == "" {
		return errors.New("encryption key source is required when encryption is enabled")
	}

	switch source {
	case KeySourceEnv:
		return c.validateEnvSource()
	case KeySourceFile:
		return c.validateFileSource()
	case KeySourceLiteral:
		return c.validateLiteralSource()
	case KeySourceDerive:
		return c.validateDeriveSource()
	default:
		return fmt.Errorf("unsupported encryption key source: %s", c.KeySource)
	}
}

func (c Config) validateEnvSource() error {
	if strings.TrimSpace(c.KeyEnvVar) == "" {
		return errors.New("keyenvvar is required for env key source")
	}
	return c.rejectMutuallyExclusive(KeySourceEnv)
}

func (c Config) validateFileSource() error {
	if strings.TrimSpace(c.KeyFile) == "" {
		return errors.New("keyfile is required for file key source")
	}
	return c.rejectMutuallyExclusive(KeySourceFile)
}

func (c Config) validateLiteralSource() error {
	if strings.TrimSpace(c.Key) == "" {
		return errors.New("key is required for literal key source")
	}
	return c.rejectMutuallyExclusive(KeySourceLiteral)
}

func (c Config) validateDeriveSource() error {
	if strings.TrimSpace(c.Passphrase) == "" {
		return errors.New("passphrase is required for derive key source")
	}
	if strings.TrimSpace(c.Salt) == "" {
		return errors.New("salt is required for derive key source")
	}
	return c.rejectMutuallyExclusive(KeySourceDerive)
}

func (c Config) rejectMutuallyExclusive(active string) error {
	var conflicts []string

	if active != KeySourceEnv && strings.TrimSpace(c.KeyEnvVar) != "" {
		conflicts = append(conflicts, "keyenvvar")
	}
	if active != KeySourceFile && strings.TrimSpace(c.KeyFile) != "" {
		conflicts = append(conflicts, "keyfile")
	}
	if active != KeySourceLiteral && strings.TrimSpace(c.Key) != "" {
		conflicts = append(conflicts, "key")
	}
	if active != KeySourceDerive {
		if strings.TrimSpace(c.Passphrase) != "" {
			conflicts = append(conflicts, "passphrase")
		}
		if strings.TrimSpace(c.Salt) != "" {
			conflicts = append(conflicts, "salt")
		}
	}

	if len(conflicts) > 0 {
		return fmt.Errorf("encryption fields %s are mutually exclusive with key source %s", strings.Join(conflicts, ", "), active)
	}

	return nil
}

func LoadKey(cfg Config) ([]byte, error) {
	source := strings.ToLower(strings.TrimSpace(cfg.KeySource))

	switch source {
	case KeySourceEnv:
		return loadKeyFromEnv(cfg.KeyEnvVar)
	case KeySourceFile:
		return loadKeyFromFile(cfg.KeyFile)
	case KeySourceLiteral:
		return decodeKey(cfg.Key)
	case KeySourceDerive:
		return deriveKey(cfg.Passphrase, cfg.Salt)
	default:
		return nil, fmt.Errorf("unsupported encryption key source: %s", cfg.KeySource)
	}
}

func loadKeyFromEnv(envVar string) ([]byte, error) {
	value := os.Getenv(envVar)
	if value == "" {
		return nil, fmt.Errorf("encryption key environment variable %s is not set", envVar)
	}
	return decodeKey(value)
}

func loadKeyFromFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read encryption key file: %w", err)
	}
	return decodeKey(strings.TrimSpace(string(data)))
}

func decodeKey(encoded string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("decode encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.Join(ErrInvalidKey, fmt.Errorf("key must be 32 bytes, got %d", len(key)))
	}
	return key, nil
}

func deriveKey(passphrase, saltEncoded string) ([]byte, error) {
	if strings.TrimSpace(passphrase) == "" {
		return nil, errors.New("passphrase must not be empty")
	}

	salt, err := base64.StdEncoding.DecodeString(strings.TrimSpace(saltEncoded))
	if err != nil {
		return nil, fmt.Errorf("decode salt: %w", err)
	}
	if len(salt) < 16 {
		return nil, fmt.Errorf("salt must be at least 16 bytes, got %d", len(salt))
	}

	key := pbkdf2.Key([]byte(passphrase), salt, 100000, 32, sha256.New)
	return key, nil
}
