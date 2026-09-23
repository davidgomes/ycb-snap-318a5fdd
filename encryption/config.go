package encryption

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

const (
	sourceEnv     = "env"
	sourceFile    = "file"
	sourceLiteral = "literal"
	sourceDerive  = "derive"
)

// Config describes how a job obtains its AES-256 key.
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

	src := strings.ToLower(strings.TrimSpace(c.KeySource))
	switch src {
	case sourceEnv:
		if strings.TrimSpace(c.KeyEnvVar) == "" {
			return fmt.Errorf("encryption key env var is required")
		}
		if exclusiveSet(c.KeyFile, c.Key, c.Passphrase, c.Salt) {
			return fmt.Errorf("encryption key fields are mutually exclusive")
		}
	case sourceFile:
		if strings.TrimSpace(c.KeyFile) == "" {
			return fmt.Errorf("encryption key file is required")
		}
		if exclusiveSet(c.KeyEnvVar, c.Key, c.Passphrase, c.Salt) {
			return fmt.Errorf("encryption key fields are mutually exclusive")
		}
	case sourceLiteral:
		if strings.TrimSpace(c.Key) == "" {
			return fmt.Errorf("encryption key is required")
		}
		if exclusiveSet(c.KeyEnvVar, c.KeyFile, c.Passphrase, c.Salt) {
			return fmt.Errorf("encryption key fields are mutually exclusive")
		}
	case sourceDerive:
		if strings.TrimSpace(c.Passphrase) == "" {
			return fmt.Errorf("encryption passphrase is required")
		}
		if strings.TrimSpace(c.Salt) == "" {
			return fmt.Errorf("encryption salt is required")
		}
		if exclusiveSet(c.KeyEnvVar, c.KeyFile, c.Key) {
			return fmt.Errorf("encryption key fields are mutually exclusive")
		}
	case "":
		return fmt.Errorf("encryption key source is required")
	default:
		return fmt.Errorf("unsupported encryption key source %q", c.KeySource)
	}
	return nil
}

func exclusiveSet(fields ...string) bool {
	for _, f := range fields {
		if strings.TrimSpace(f) != "" {
			return true
		}
	}
	return false
}

// LoadKey resolves cfg to a 32-byte key.
func LoadKey(cfg Config) ([]byte, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	src := strings.ToLower(strings.TrimSpace(cfg.KeySource))
	switch src {
	case sourceEnv:
		raw, ok := os.LookupEnv(cfg.KeyEnvVar)
		if !ok || strings.TrimSpace(raw) == "" {
			return nil, fmt.Errorf("encryption key env var %s is not set", cfg.KeyEnvVar)
		}
		return decodeKey(raw)
	case sourceFile:
		b, err := os.ReadFile(cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("encryption key file: %w", err)
		}
		return decodeKey(string(b))
	case sourceLiteral:
		return decodeKey(cfg.Key)
	case sourceDerive:
		return deriveKey(cfg.Passphrase, cfg.Salt)
	default:
		return nil, fmt.Errorf("unsupported encryption key source %q", cfg.KeySource)
	}
}

func decodeKey(raw string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: expected 32 bytes, got %d", ErrInvalidKey, len(key))
	}
	return key, nil
}

func deriveKey(passphrase, saltB64 string) ([]byte, error) {
	if strings.TrimSpace(passphrase) == "" {
		return nil, fmt.Errorf("encryption passphrase is empty")
	}
	salt, err := base64.StdEncoding.DecodeString(strings.TrimSpace(saltB64))
	if err != nil {
		return nil, fmt.Errorf("encryption salt: %w", err)
	}
	if len(salt) < 16 {
		return nil, fmt.Errorf("encryption salt must be at least 16 bytes, got %d", len(salt))
	}
	key, err := hkdf.Key(sha256.New, []byte(passphrase), salt, "onedump-encryption", 32)
	if err != nil {
		return nil, fmt.Errorf("encryption key derivation: %w", err)
	}
	return key, nil
}
