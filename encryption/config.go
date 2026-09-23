package encryption

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"slices"
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

// Keys derived from a passphrase depend on these parameters,
// changing them makes previously encrypted dumps undecryptable.
const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024
	argonThreads uint8  = 4
)

var keySources = []string{KeySourceEnv, KeySourceFile, KeySourceLiteral, KeySourceDerive}

type Config struct {
	Enabled    bool   `yaml:"enabled"`
	KeySource  string `yaml:"keysource"`
	KeyEnvVar  string `yaml:"keyenvvar"`
	KeyFile    string `yaml:"keyfile"`
	Key        string `yaml:"key"`
	Passphrase string `yaml:"passphrase"`
	Salt       string `yaml:"salt"`
}

type sourceField struct {
	name   string
	value  string
	source string
}

func (c Config) sourceFields() []sourceField {
	return []sourceField{
		{name: "keyenvvar", value: c.KeyEnvVar, source: KeySourceEnv},
		{name: "keyfile", value: c.KeyFile, source: KeySourceFile},
		{name: "key", value: c.Key, source: KeySourceLiteral},
		{name: "passphrase", value: c.Passphrase, source: KeySourceDerive},
		{name: "salt", value: c.Salt, source: KeySourceDerive},
	}
}

// Validate the encryption config, a disabled config is always valid.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}

	_, err := c.keySource()
	return err
}

// Return the normalised key source after checking that exactly the fields of that source are set.
func (c Config) keySource() (string, error) {
	source := strings.ToLower(strings.TrimSpace(c.KeySource))

	if source == "" {
		return "", errors.New("encryption: key source is required when encryption is enabled")
	}

	if !slices.Contains(keySources, source) {
		return "", fmt.Errorf("encryption: unsupported key source %q, supported key sources: %s", c.KeySource, strings.Join(keySources, ", "))
	}

	var missing, conflicting []string
	for _, field := range c.sourceFields() {
		set := strings.TrimSpace(field.value) != ""

		if field.source == source && !set {
			missing = append(missing, field.name)
		}

		if field.source != source && set {
			conflicting = append(conflicting, field.name)
		}
	}

	if len(conflicting) > 0 {
		return "", fmt.Errorf("encryption: %s cannot be used with key source %q, key source fields are mutually exclusive", strings.Join(conflicting, ", "), source)
	}

	if len(missing) > 0 {
		return "", fmt.Errorf("encryption: key source %q requires %s", source, strings.Join(missing, ", "))
	}

	return source, nil
}

// Load the 32-byte encryption key from the configured key source.
func LoadKey(cfg Config) ([]byte, error) {
	source, err := cfg.keySource()
	if err != nil {
		return nil, err
	}

	switch source {
	case KeySourceEnv:
		value, ok := os.LookupEnv(cfg.KeyEnvVar)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("encryption: environment variable %s for the encryption key is not set", cfg.KeyEnvVar)
		}

		return decodeKey(value, "environment variable "+cfg.KeyEnvVar)
	case KeySourceFile:
		content, err := os.ReadFile(cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("encryption: could not read key file: %w", err)
		}

		return decodeKey(string(content), "key file "+cfg.KeyFile)
	case KeySourceLiteral:
		return decodeKey(cfg.Key, "literal key")
	default:
		return deriveKey(cfg.Passphrase, cfg.Salt)
	}
}

func decodeKey(encoded, origin string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("encryption: %w: %s is not valid base64: %w", ErrInvalidKey, origin, err)
	}

	if len(key) != KeySize {
		return nil, fmt.Errorf("encryption: %w: %s must decode to %d bytes, got %d", ErrInvalidKey, origin, KeySize, len(key))
	}

	return key, nil
}

func deriveKey(passphrase, encodedSalt string) ([]byte, error) {
	salt, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedSalt))
	if err != nil {
		return nil, fmt.Errorf("encryption: salt is not valid base64: %w", err)
	}

	if len(salt) < MinSaltSize {
		return nil, fmt.Errorf("encryption: salt must be at least %d bytes, got %d", MinSaltSize, len(salt))
	}

	return argon2.IDKey([]byte(passphrase), salt, argonTime, argonMemory, argonThreads, KeySize), nil
}
