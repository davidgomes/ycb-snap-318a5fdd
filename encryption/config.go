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
	keySourceEnv     = "env"
	keySourceFile    = "file"
	keySourceLiteral = "literal"
	keySourceDerive  = "derive"

	minSaltLen  = 16
	derivedInfo = "onedump-encryption"
)

// Config describes how a job obtains its encryption key.
type Config struct {
	Enabled    bool   `yaml:"enabled"`
	KeySource  string `yaml:"keysource"`
	KeyEnvVar  string `yaml:"keyenvvar"`
	KeyFile    string `yaml:"keyfile"`
	Key        string `yaml:"key"`
	Passphrase string `yaml:"passphrase"`
	Salt       string `yaml:"salt"`
}

// Validate checks key-source settings. Disabled configs are always valid.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}

	source := strings.ToLower(strings.TrimSpace(c.KeySource))
	switch source {
	case "":
		return fmt.Errorf("encryption key source is empty")
	case keySourceEnv, keySourceFile, keySourceLiteral, keySourceDerive:
	default:
		return fmt.Errorf("unsupported encryption key source %q", c.KeySource)
	}

	if err := c.requireOwnFields(source); err != nil {
		return err
	}

	if c.hasForeignFields(source) {
		return fmt.Errorf("encryption key fields are mutually exclusive")
	}

	return nil
}

func (c Config) requireOwnFields(source string) error {
	switch source {
	case keySourceEnv:
		if strings.TrimSpace(c.KeyEnvVar) == "" {
			return fmt.Errorf("encryption key env var is required")
		}
	case keySourceFile:
		if strings.TrimSpace(c.KeyFile) == "" {
			return fmt.Errorf("encryption key file is required")
		}
	case keySourceLiteral:
		if strings.TrimSpace(c.Key) == "" {
			return fmt.Errorf("encryption key is required")
		}
	case keySourceDerive:
		if strings.TrimSpace(c.Passphrase) == "" {
			return fmt.Errorf("encryption passphrase is required")
		}
		if strings.TrimSpace(c.Salt) == "" {
			return fmt.Errorf("encryption salt is required")
		}
	}
	return nil
}

func (c Config) hasForeignFields(source string) bool {
	set := map[string]bool{
		keySourceEnv:     strings.TrimSpace(c.KeyEnvVar) != "",
		keySourceFile:    strings.TrimSpace(c.KeyFile) != "",
		keySourceLiteral: strings.TrimSpace(c.Key) != "",
		keySourceDerive:  strings.TrimSpace(c.Passphrase) != "" || strings.TrimSpace(c.Salt) != "",
	}

	for name, present := range set {
		if name != source && present {
			return true
		}
	}
	return false
}

// LoadKey resolves cfg to a 32-byte key.
func LoadKey(cfg Config) ([]byte, error) {
	// Key material is validated even when Enabled is false so callers can
	// resolve a key without toggling the job flag.
	check := cfg
	check.Enabled = true
	if err := check.Validate(); err != nil {
		return nil, err
	}

	source := strings.ToLower(strings.TrimSpace(cfg.KeySource))
	switch source {
	case keySourceEnv:
		return loadEncodedKey(readEnvKey(cfg.KeyEnvVar))
	case keySourceFile:
		return loadEncodedKey(readFileKey(cfg.KeyFile))
	case keySourceLiteral:
		return loadEncodedKey(strings.TrimSpace(cfg.Key), nil)
	case keySourceDerive:
		return deriveKey(cfg.Passphrase, cfg.Salt)
	default:
		return nil, fmt.Errorf("unsupported encryption key source %q", cfg.KeySource)
	}
}

func readEnvKey(name string) (string, error) {
	val, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(val) == "" {
		return "", fmt.Errorf("encryption key environment variable %q is not set", name)
	}
	return strings.TrimSpace(val), nil
}

func readFileKey(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read encryption key file: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

func loadEncodedKey(encoded string, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}

	key, decErr := base64.StdEncoding.DecodeString(encoded)
	if decErr != nil {
		return nil, fmt.Errorf("decode encryption key: %w", decErr)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: decoded key is %d bytes", ErrInvalidKey, len(key))
	}
	return key, nil
}

func deriveKey(passphrase, saltB64 string) ([]byte, error) {
	if strings.TrimSpace(passphrase) == "" {
		return nil, fmt.Errorf("empty passphrase")
	}

	salt, err := base64.StdEncoding.DecodeString(strings.TrimSpace(saltB64))
	if err != nil {
		return nil, fmt.Errorf("decode encryption salt: %w", err)
	}
	if len(salt) < minSaltLen {
		return nil, fmt.Errorf("encryption salt must be at least %d bytes", minSaltLen)
	}

	key, err := hkdf.Key(sha256.New, []byte(passphrase), salt, derivedInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("derive encryption key: %w", err)
	}
	return key, nil
}
