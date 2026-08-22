package handler

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"io"
	"strings"
	"testing"

	"github.com/liweiyi88/onedump/config"
	"github.com/liweiyi88/onedump/encryption"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorageReadWriteCloserEncryptionRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)

	enc, err := encryption.NewEncryptor(key)
	require.NoError(t, err)

	readers, writer, closer := storageReadWriteCloser(1, true, enc)

	plaintext := []byte("pipeline test " + strings.Repeat("z", 5000))

	var encrypted bytes.Buffer
	readDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(&encrypted, readers[0])
		readDone <- copyErr
	}()

	_, err = writer.Write(plaintext)
	require.NoError(t, err)
	require.NoError(t, closer.Close())
	require.NoError(t, <-readDone)

	decryptedReader, err := encryption.DecryptReader(&encrypted, key)
	require.NoError(t, err)

	gr, err := gzip.NewReader(decryptedReader)
	require.NoError(t, err)

	decrypted, err := io.ReadAll(gr)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestSaveFailsFastOnMissingEncryptionKey(t *testing.T) {
	job := config.NewJob("job", "mysqldump", testDBDsn)
	job.Encryption = encryption.Config{
		Enabled:   true,
		KeySource: "env",
		KeyEnvVar: "ONEDUMP_MISSING_ENCRYPTION_KEY",
	}

	handler := NewJobHandler(job)
	err := handler.save()
	require.Error(t, err)
	errMsg := strings.ToLower(err.Error())
	assert.True(t, strings.Contains(errMsg, "encryption") || strings.Contains(errMsg, "key"))
}

func TestSaveFailsFastOnMissingKeyWithoutStorage(t *testing.T) {
	job := config.NewJob("job", "mysqldump", testDBDsn)
	job.Encryption = encryption.Config{
		Enabled:   true,
		KeySource: "env",
		KeyEnvVar: "ONEDUMP_MISSING_ENCRYPTION_KEY_NO_STORAGE",
	}

	handler := NewJobHandler(job)
	err := handler.save()
	require.Error(t, err)
	errMsg := strings.ToLower(err.Error())
	assert.True(t, strings.Contains(errMsg, "encryption") || strings.Contains(errMsg, "key"))
}
