package config

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/liweiyi88/onedump/encryption"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJobEncrypted(t *testing.T) {
	job := &Job{}
	assert.False(t, job.Encrypted())

	job.Encryption.Enabled = true
	assert.True(t, job.Encrypted())
}

func TestJobValidateEncryptionConfig(t *testing.T) {
	job := NewJob("job", "mysql", testDBDsn)
	job.Encryption = encryption.Config{
		Enabled:   true,
		KeySource: "env",
	}
	err := job.Validate()
	require.Error(t, err)

	key := make([]byte, 32)
	_, err = rand.Read(key)
	require.NoError(t, err)

	job.Encryption.KeyEnvVar = "TEST_JOB_KEY"
	t.Setenv("TEST_JOB_KEY", base64.StdEncoding.EncodeToString(key))
	err = job.Validate()
	assert.NoError(t, err)
}

func TestJobValidateDisabledEncryption(t *testing.T) {
	job := NewJob("job", "mysql", testDBDsn)
	job.Encryption = encryption.Config{Enabled: false}
	assert.NoError(t, job.Validate())
}
