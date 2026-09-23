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

	minSaltSize      = 16
	deriveIterations = 210000
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

func (c Config) source() string {
	return strings.ToLower(strings.TrimSpace(c.KeySource))
}

func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}

	fields := map[string]string{
		"keyenvvar":  c.KeyEnvVar,
		"keyfile":    c.KeyFile,
		"key":        c.Key,
		"passphrase": c.Passphrase,
		"salt":       c.Salt,
	}

	var required []string
	switch c.source() {
	case "":
		return errors.New("encryption: key source is required when encryption is enabled")
	case KeySourceEnv:
		required = []string{"keyenvvar"}
	case KeySourceFile:
		required = []string{"keyfile"}
	case KeySourceLiteral:
		required = []string{"key"}
	case KeySourceDerive:
		required = []string{"passphrase", "salt"}
	default:
		return fmt.Errorf("encryption: unsupported key source %q", c.KeySource)
	}

	allowed := map[string]bool{}
	for _, f := range required {
		allowed[f] = true
		if strings.TrimSpace(fields[f]) == "" {
			return fmt.Errorf("encryption: %s is required for key source %q", f, c.source())
		}
	}

	for _, f := range []string{"keyenvvar", "keyfile", "key", "passphrase", "salt"} {
		if !allowed[f] && strings.TrimSpace(fields[f]) != "" {
			return fmt.Errorf("encryption: %s cannot be used with key source %q, key source fields are mutually exclusive", f, c.source())
		}
	}

	return nil
}

func decodeKey(s string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("encryption: failed to decode base64 key: %w", err)
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("%w: key must be %d bytes, got %d", ErrInvalidKey, KeySize, len(key))
	}
	return key, nil
}

func LoadKey(cfg Config) ([]byte, error) {
	switch cfg.source() {
	case KeySourceEnv:
		if strings.TrimSpace(cfg.KeyEnvVar) == "" {
			return nil, errors.New("encryption: key env var name is required")
		}
		v, ok := os.LookupEnv(cfg.KeyEnvVar)
		if !ok || strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("encryption: key env var %s is not set", cfg.KeyEnvVar)
		}
		return decodeKey(v)
	case KeySourceFile:
		if strings.TrimSpace(cfg.KeyFile) == "" {
			return nil, errors.New("encryption: key file is required")
		}
		data, err := os.ReadFile(cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("encryption: failed to read key file: %w", err)
		}
		return decodeKey(string(data))
	case KeySourceLiteral:
		if strings.TrimSpace(cfg.Key) == "" {
			return nil, errors.New("encryption: key is required")
		}
		return decodeKey(cfg.Key)
	case KeySourceDerive:
		if cfg.Passphrase == "" {
			return nil, errors.New("encryption: passphrase is required to derive key")
		}
		salt, err := base64.StdEncoding.DecodeString(strings.TrimSpace(cfg.Salt))
		if err != nil {
			return nil, fmt.Errorf("encryption: failed to decode base64 salt: %w", err)
		}
		if len(salt) < minSaltSize {
			return nil, fmt.Errorf("encryption: salt must be at least %d bytes, got %d", minSaltSize, len(salt))
		}
		return pbkdf2.Key(sha256.New, cfg.Passphrase, salt, deriveIterations, KeySize)
	case "":
		return nil, errors.New("encryption: key source is required")
	default:
		return nil, fmt.Errorf("encryption: unsupported key source %q", cfg.KeySource)
	}
}
