package handler

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
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

func TestStorageReadWriteCloserEncryptGzipRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, 32)
	enc, err := encryption.NewEncryptor(key)
	require.NoError(t, err)

	payload := bytes.Repeat([]byte("row,"), 30000)
	readers, writer, closer := storageReadWriteCloser(1, true, enc)

	errCh := make(chan error, 1)
	go func() {
		_, werr := writer.Write(payload)
		if werr != nil {
			errCh <- werr
			_ = closer.Close()
			return
		}
		errCh <- closer.Close()
	}()

	encrypted, err := io.ReadAll(readers[0])
	require.NoError(t, err)
	require.NoError(t, <-errCh)
	require.GreaterOrEqual(t, len(encrypted), 3)
	assert.Equal(t, []byte{0x4F, 0x44, 0x01}, encrypted[:3])

	dec, err := encryption.DecryptReader(bytes.NewReader(encrypted), key)
	require.NoError(t, err)
	gz, err := gzip.NewReader(dec)
	require.NoError(t, err)
	got, err := io.ReadAll(gz)
	require.NoError(t, err)
	assert.Equal(t, payload, got)
	require.NoError(t, gz.Close())
}

func TestSaveMissingEncryptionKey(t *testing.T) {
	envName := "ONEDUMP_MISSING_ENCRYPTION_KEY"
	require.NoError(t, os.Unsetenv(envName))

	cfg := encryption.Config{
		Enabled:   true,
		KeySource: "env",
		KeyEnvVar: envName,
	}

	without := config.NewJob("job", "mysql", testDBDsn)
	without.Encryption = cfg
	err := NewJobHandler(without).save()
	require.Error(t, err)
	assertEncryptionKeyError(t, err)

	with := config.NewJob("job", "mysql", testDBDsn)
	with.Encryption = cfg
	with.Storage.Local = []*local.Local{{Path: filepath.Join(t.TempDir(), "dump.sql")}}
	err = NewJobHandler(with).save()
	require.Error(t, err)
	assertEncryptionKeyError(t, err)
}

func assertEncryptionKeyError(t *testing.T, err error) {
	t.Helper()
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "encryption") && !strings.Contains(msg, "key") {
		t.Fatalf("error %q does not mention encryption or key", err.Error())
	}
}
