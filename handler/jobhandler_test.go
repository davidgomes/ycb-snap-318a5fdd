package handler

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
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

	enc := fileutil.EnsureFileSuffix("test.sql", true, true)
	assert.Equal(t, "test.sql.gz.enc", enc)
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

func TestStorageReadWriteCloserWithEncryption(t *testing.T) {
	assert := assert.New(t)

	key := make([]byte, encryption.KeySize)
	_, err := rand.Read(key)
	assert.NoError(err)

	encryptor, err := encryption.NewEncryptor(key)
	assert.NoError(err)

	data := bytes.Repeat([]byte("onedump encrypted dump\n"), 10000)

	readers, writer, closer := storageReadWriteCloser(2, true, encryptor)

	go func() {
		_, err := writer.Write(data)
		assert.NoError(err)
		assert.NoError(closer.Close())
	}()

	var wg sync.WaitGroup
	outputs := make([][]byte, len(readers))
	for i, r := range readers {
		wg.Add(1)
		go func(i int, r io.Reader) {
			defer wg.Done()
			out, err := io.ReadAll(r)
			assert.NoError(err)
			outputs[i] = out
		}(i, r)
	}
	wg.Wait()

	assert.NotEqual(outputs[0], outputs[1])

	for _, out := range outputs {
		dr, err := encryption.DecryptReader(bytes.NewReader(out), key)
		assert.NoError(err)

		gr, err := gzip.NewReader(dr)
		assert.NoError(err)

		plain, err := io.ReadAll(gr)
		assert.NoError(err)
		assert.Equal(data, plain)
	}
}

func TestSaveFailsFastOnMissingEncryptionKey(t *testing.T) {
	encryptionConfig := encryption.Config{
		Enabled:   true,
		KeySource: "env",
		KeyEnvVar: "ONEDUMP_TEST_MISSING_ENCRYPTION_KEY",
	}

	withoutStorage := config.NewJob("no-storage", "mysql", testDBDsn)
	withoutStorage.Encryption = encryptionConfig

	withStorage := config.NewJob("with-storage", "mysql", testDBDsn)
	withStorage.Encryption = encryptionConfig
	dumpFile := filepath.Join(t.TempDir(), "dump.sql")
	withStorage.Storage.Local = []*local.Local{{Path: dumpFile}}

	for _, job := range []*config.Job{withoutStorage, withStorage} {
		result := NewJobHandler(job).Do()
		assert.Error(t, result.Error)
		assert.ErrorContains(t, result.Error, "encryption key")
	}

	_, err := os.Stat(dumpFile)
	assert.True(t, errors.Is(err, os.ErrNotExist))
}
