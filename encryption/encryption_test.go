package encryption

import (
	"bytes"
	"crypto/hmac"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewEncryptorRejectsKeyLength(t *testing.T) {
	_, err := NewEncryptor([]byte("short"))
	if !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("expected ErrInvalidKey, got %v", err)
	}
}

func TestRoundTripAndUniqueNonce(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}

	plain := bytes.Repeat([]byte("abc123"), 20000)

	var a, b bytes.Buffer
	write := func(dst *bytes.Buffer) {
		t.Helper()
		w := enc.EncryptWriter(dst)
		if _, err := w.Write(plain); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	write(&a)
	write(&b)

	if bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("two encryptions of the same plaintext must differ")
	}
	if a.Bytes()[0] != 0x4F || a.Bytes()[1] != 0x44 || a.Bytes()[2] != 0x01 {
		t.Fatalf("bad header %x", a.Bytes()[:3])
	}

	r, err := DecryptReader(&a, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("plaintext mismatch")
	}
}

func TestDecryptErrors(t *testing.T) {
	key := bytes.Repeat([]byte{3}, 32)
	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := enc.EncryptWriter(&buf)
	if _, err := io.WriteString(w, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	blob := buf.Bytes()

	r, err := DecryptReader(bytes.NewReader(append([]byte{0x00, 0x01, 0x01}, blob[3:]...)), key)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(r)
	if err == nil || !strings.Contains(err.Error(), "invalid header") {
		t.Fatalf("magic: %v", err)
	}

	r, err = DecryptReader(bytes.NewReader(append([]byte{0x4F, 0x44, 0x02}, blob[3:]...)), key)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(r)
	if err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Fatalf("version: %v", err)
	}

	tampered := append([]byte(nil), blob...)
	tampered[len(tampered)-1] ^= 0xff
	r, err = DecryptReader(bytes.NewReader(tampered), key)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(r)
	if err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("hmac: %v", err)
	}

	other := bytes.Repeat([]byte{9}, 32)
	r, err = DecryptReader(bytes.NewReader(blob), other)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(r)
	if err == nil {
		t.Fatal("wrong key should fail")
	}

	r, err = DecryptReader(bytes.NewReader(blob[:len(blob)/2]), key)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(r)
	if err == nil {
		t.Fatal("truncated data should fail")
	}
}

func TestConfigValidate(t *testing.T) {
	if err := (Config{Enabled: false, KeySource: "nope"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Config{Enabled: true}).Validate(); err == nil {
		t.Fatal("empty source")
	}
	if err := (Config{Enabled: true, KeySource: "KMS"}).Validate(); err == nil {
		t.Fatal("unsupported")
	}
	if err := (Config{Enabled: true, KeySource: "ENV", KeyEnvVar: "K", KeyFile: "x"}).Validate(); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("exclusive: %v", err)
	}
	if err := (Config{Enabled: true, KeySource: "File", KeyFile: "k"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Config{Enabled: true, KeySource: "literal"}).Validate(); err == nil {
		t.Fatal("missing key")
	}
	if err := (Config{Enabled: true, KeySource: "derive", Passphrase: "p"}).Validate(); err == nil {
		t.Fatal("missing salt")
	}
}

func TestLoadKey(t *testing.T) {
	raw := bytes.Repeat([]byte{1}, 32)
	b64 := base64.StdEncoding.EncodeToString(raw)

	t.Setenv("ONEDUMP_TEST_KEY", b64)
	key, err := LoadKey(Config{Enabled: true, KeySource: "env", KeyEnvVar: "ONEDUMP_TEST_KEY"})
	if err != nil || !bytes.Equal(key, raw) {
		t.Fatalf("env: %v %x", err, key)
	}
	_, err = LoadKey(Config{Enabled: true, KeySource: "env", KeyEnvVar: "ONEDUMP_TEST_KEY_MISSING"})
	if err == nil || (!strings.Contains(err.Error(), "encryption") && !strings.Contains(err.Error(), "key")) {
		t.Fatalf("missing env: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "key")
	if err := os.WriteFile(path, []byte("  "+b64+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err = LoadKey(Config{Enabled: true, KeySource: "file", KeyFile: path})
	if err != nil || !bytes.Equal(key, raw) {
		t.Fatalf("file: %v", err)
	}

	key, err = LoadKey(Config{Enabled: true, KeySource: "literal", Key: b64})
	if err != nil || !hmac.Equal(key, raw) {
		t.Fatalf("literal: %v", err)
	}

	salt := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 16))
	k1, err := LoadKey(Config{Enabled: true, KeySource: "derive", Passphrase: "secret", Salt: salt})
	if err != nil || len(k1) != 32 {
		t.Fatal(err)
	}
	k2, err := LoadKey(Config{Enabled: true, KeySource: "derive", Passphrase: "secret", Salt: salt})
	if err != nil || !bytes.Equal(k1, k2) {
		t.Fatal("derive not deterministic")
	}
	_, err = LoadKey(Config{Enabled: true, KeySource: "derive", Passphrase: "", Salt: salt})
	if err == nil {
		t.Fatal("empty passphrase")
	}
	short := base64.StdEncoding.EncodeToString([]byte("0123456789abcde"))
	_, err = LoadKey(Config{Enabled: true, KeySource: "derive", Passphrase: "secret", Salt: short})
	if err == nil {
		t.Fatal("short salt")
	}
}
