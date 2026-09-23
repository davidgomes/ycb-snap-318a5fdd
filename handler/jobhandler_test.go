package handler

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/liweiyi88/onedump/config"
	"github.com/liweiyi88/onedump/dumper"
	"github.com/liweiyi88/onedump/encryption"
	"github.com/liweiyi88/onedump/fileutil"
	"github.com/liweiyi88/onedump/storage/dropbox"
	"github.com/liweiyi88/onedump/storage/gdrive"
	"github.com/liweiyi88/onedump/storage/local"
	"github.com/liweiyi88/onedump/storage/s3"
	"github.com/liweiyi88/onedump/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testDBDsn = "root@tcp(127.0.0.1:3306)/dump_test"

func TestGenerateCacheFileName(t *testing.T) {
	expectedLen := 5
	name := fileutil.GenerateRandomName(expectedLen)

	actualLen := len([]rune(name))
	assert.Equal(t, expectedLen, actualLen)
}

func TestDo(t *testing.T) {
	assert := assert.New(t)
	privateKey, err := testutils.GenerateRSAPrivateKey()
	assert.Nil(err)

	jobs := make([]*config.Job, 0, 1)
	sshJob := config.NewJob("ssh", "mysqldump", testDBDsn, config.WithSshHost("127.0.0.1:20002"), config.WithSshUser("root"), config.WithSshKey(privateKey))
	localStorages := make([]*local.Local, 0)

	dir, _ := os.Getwd()
	dumpFile := dir + "/hello.sql"

	t.Logf("dump file: %s", dumpFile)

	localStorages = append(localStorages, &local.Local{Path: dumpFile})

	sshJob.Storage.Local = localStorages

	jobs = append(jobs, sshJob)
	onedump := config.Dump{Jobs: jobs}

	// An SSH server is represented by a ServerConfig, which holds
	// certificate details and handles authentication of ServerConns.
	sshConfig := &ssh.ServerConfig{
		PublicKeyCallback: func(c ssh.ConnMetadata, pubKey ssh.PublicKey) (*ssh.Permissions, error) {
			return &ssh.Permissions{
				// Record the public key used for authentication.
				Extensions: map[string]string{
					"pubkey-fp": ssh.FingerprintSHA256(pubKey),
				},
			}, nil
		},
	}

	private, err := ssh.ParsePrivateKey([]byte(privateKey))
	assert.Nil(err)

	sshConfig.AddHostKey(private)

	// Once a ServerConfig has been configured, connections can be
	// accepted.
	listener, err := net.Listen("tcp", "0.0.0.0:20002")
	assert.Nil(err)

	finishCh := make(chan struct{}, len(onedump.Jobs))
	go func(onedump config.Dump) {
		for _, job := range onedump.Jobs {
			NewJobHandler(job).Do()
		}

		finishCh <- struct{}{}
	}(onedump)

	nConn, err := listener.Accept()
	assert.Nil(err)

	// Before use, a handshake must be performed on the incoming
	// net.Conn.
	conn, chans, reqs, err := ssh.NewServerConn(nConn, sshConfig)
	assert.Nil(err)
	t.Logf("logged in with key %s", conn.Permissions.Extensions["pubkey-fp"])

	// The incoming Request channel must be serviced.
	go ssh.DiscardRequests(reqs)

	// Service the incoming Channel channel.
	newChannel := <-chans
	// Channels have a type, depending on the application level
	// protocol intended. In the case of a shell, the type is
	// "session" and ServerShell may be used to present a simple
	// terminal interface.
	if newChannel.ChannelType() != "session" {
		newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
		t.Fatal("unknown channel type")
	}

	channel, requests, err := newChannel.Accept()
	assert.Nil(err)

	req := <-requests
	req.Reply(true, nil)

	_, err = channel.Write([]byte("ssh dump"))
	assert.Nil(err)

	_, err = channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
	assert.Nil(err)

	err = channel.Close()
	assert.Nil(err)

	<-finishCh
	if _, err := os.Stat(dumpFile); errors.Is(err, os.ErrNotExist) {
		t.Error("dump file does not existed")
	} else {
		err := os.Remove(dumpFile)
		assert.Nil(err)
	}
}

func TestGetStorages(t *testing.T) {
	localStore := local.Local{Path: "db_backup/onedump.sql"}
	s3 := s3.NewS3("mybucket", "key", "", "", "", "")
	gdrive := &gdrive.GDrive{
		FileName: "mydump",
		FolderId: "",
	}

	dropbox := &dropbox.Dropbox{
		RefreshToken: "token",
	}

	job := &config.Job{}
	job.Storage.Local = append(job.Storage.Local, &localStore)
	job.Storage.S3 = append(job.Storage.S3, s3)
	job.Storage.GDrive = append(job.Storage.GDrive, gdrive)
	job.Storage.Dropbox = append(job.Storage.Dropbox, dropbox)

	jobHandler := NewJobHandler(job)

	assert.Len(t, jobHandler.getStorages(), 4)
}

func TestEnsureFileSuffix(t *testing.T) {
	gzip := fileutil.EnsureFileSuffix("test.sql", true, false)
	assert.Equal(t, "test.sql.gz", gzip)

	sql := fileutil.EnsureFileSuffix("test.sql.gz", true, false)
	assert.Equal(t, "test.sql.gz", sql)

	encrypted := fileutil.EnsureFileSuffix("test.sql", true, true)
	assert.Equal(t, "test.sql.gz.enc", encrypted)
}

// Write data through the storage pipeline and return what each storage reader received.
func runPipeline(t *testing.T, count int, compress bool, encryptor *encryption.Encryptor, data []byte) [][]byte {
	t.Helper()
	readers, writer, closer := storageReadWriteCloser(count, compress, encryptor)
	require.Len(t, readers, count)

	outputs := make([][]byte, count)
	errs := make([]error, count)

	var wg sync.WaitGroup
	wg.Add(count)
	for i, r := range readers {
		go func(i int, r io.Reader) {
			defer wg.Done()
			outputs[i], errs[i] = io.ReadAll(r)
		}(i, r)
	}

	for p := data; len(p) > 0; {
		n := min(len(p), 4096)
		_, err := writer.Write(p[:n])
		require.NoError(t, err)
		p = p[n:]
	}

	require.NoError(t, closer.Close())
	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}

	return outputs
}

func newTestKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, encryption.KeySize)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return key
}

func TestStorageReadWriteCloserEncrypted(t *testing.T) {
	key := newTestKey(t)
	encryptor, err := encryption.NewEncryptor(key)
	require.NoError(t, err)

	data := bytes.Repeat([]byte("INSERT INTO users VALUES (1, 'onedump');\n"), 10000)

	outputs := runPipeline(t, 3, true, encryptor, data)
	for _, output := range outputs {
		assert.Equal(t, []byte{0x4F, 0x44, 0x01}, output[:3])

		dr, err := encryption.DecryptReader(bytes.NewReader(output), key)
		require.NoError(t, err)

		gr, err := gzip.NewReader(dr)
		require.NoError(t, err)

		decrypted, err := io.ReadAll(gr)
		require.NoError(t, err)
		assert.True(t, bytes.Equal(data, decrypted))
	}

	assert.NotEqual(t, outputs[0], outputs[1])
}

func TestStorageReadWriteCloserEncryptedWithoutGzip(t *testing.T) {
	key := newTestKey(t)
	encryptor, err := encryption.NewEncryptor(key)
	require.NoError(t, err)

	data := make([]byte, 3*encryption.ChunkSize+10)
	_, err = rand.Read(data)
	require.NoError(t, err)

	for _, output := range runPipeline(t, 2, false, encryptor, data) {
		dr, err := encryption.DecryptReader(bytes.NewReader(output), key)
		require.NoError(t, err)

		decrypted, err := io.ReadAll(dr)
		require.NoError(t, err)
		assert.True(t, bytes.Equal(data, decrypted))
	}
}

func TestStorageReadWriteCloserWithoutEncryption(t *testing.T) {
	data := []byte("plain dump")

	for _, output := range runPipeline(t, 2, false, nil, data) {
		assert.Equal(t, data, output)
	}

	for _, output := range runPipeline(t, 1, true, nil, data) {
		gr, err := gzip.NewReader(bytes.NewReader(output))
		require.NoError(t, err)

		decompressed, err := io.ReadAll(gr)
		require.NoError(t, err)
		assert.Equal(t, data, decompressed)
	}
}

func TestDoFailsFastOnEncryptionKeyError(t *testing.T) {
	missingKey := encryption.Config{Enabled: true, KeySource: "env", KeyEnvVar: "ONEDUMP_TEST_MISSING_ENCRYPTION_KEY"}
	t.Setenv(missingKey.KeyEnvVar, "")
	require.NoError(t, os.Unsetenv(missingKey.KeyEnvVar))

	t.Run("without storages", func(t *testing.T) {
		job := config.NewJob("encrypted", "mysqldump", testDBDsn)
		job.Encryption = missingKey

		result := NewJobHandler(job).Do()
		require.Error(t, result.Error)
		assert.ErrorContains(t, result.Error, "encryption")
	})

	t.Run("with storages", func(t *testing.T) {
		dumpFile := filepath.Join(t.TempDir(), "dump.sql")

		job := config.NewJob("encrypted", "mysqldump", testDBDsn, config.WithGzip(true))
		job.Encryption = missingKey
		job.Storage.Local = []*local.Local{{Path: dumpFile}}

		result := NewJobHandler(job).Do()
		require.Error(t, result.Error)
		assert.ErrorContains(t, result.Error, "encryption")

		matches, err := filepath.Glob(dumpFile + "*")
		require.NoError(t, err)
		assert.Empty(t, matches)
	})

	t.Run("invalid key length", func(t *testing.T) {
		job := config.NewJob("encrypted", "mysqldump", testDBDsn)
		job.Encryption = encryption.Config{Enabled: true, KeySource: "literal", Key: base64.StdEncoding.EncodeToString([]byte("too short"))}

		result := NewJobHandler(job).Do()
		assert.ErrorContains(t, result.Error, encryption.ErrInvalidKey.Error())
	})
}

func TestGetEncryptor(t *testing.T) {
	job := config.NewJob("plain", "mysqldump", testDBDsn)

	encryptor, err := NewJobHandler(job).getEncryptor()
	assert.NoError(t, err)
	assert.Nil(t, encryptor)

	job.Encryption = encryption.Config{Enabled: true, KeySource: "literal", Key: base64.StdEncoding.EncodeToString(newTestKey(t))}
	encryptor, err = NewJobHandler(job).getEncryptor()
	assert.NoError(t, err)
	assert.NotNil(t, encryptor)
}

func TestGetDumper(t *testing.T) {
	assert := assert.New(t)
	job := &config.Job{}
	jobHandler := NewJobHandler(job)

	_, err := jobHandler.getDumper()
	assert.NotNil(err)

	job.DBDriver = "mysqldump"
	r, err := jobHandler.getDumper()
	assert.Nil(err)

	if _, ok := r.(*dumper.MysqlDump); !ok {
		t.Errorf("expect exec dumper, but got type: %T", r)
	}

	job.DBDriver = "postgresql"
	job.SshHost = "localhost"
	job.SshUser = "admin"
	job.SshKey = "ssh key"
	r, err = jobHandler.getDumper()
	assert.Nil(err)

	if _, ok := r.(*dumper.PgDump); !ok {
		t.Errorf("expect ssh dumper, but got type: %T", r)
	}
}
