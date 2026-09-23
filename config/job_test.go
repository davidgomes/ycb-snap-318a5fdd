package config

import (
	"errors"
	"testing"
	"time"

	"github.com/liweiyi88/onedump/encryption"
	"github.com/liweiyi88/onedump/jobresult"
	"github.com/stretchr/testify/assert"
)

var testDBDsn = "root@tcp(127.0.0.1:3306)/dump_test"

func TestWithSshHost(t *testing.T) {
	job := NewJob("job", "mysql", testDBDsn, WithSshHost("localhost"))
	assert.Equal(t, "localhost", job.SshHost)
}

func TestWithSshUser(t *testing.T) {
	job := NewJob("job", "mysql", testDBDsn, WithSshUser("root"))
	assert.Equal(t, "root", job.SshUser)
}

func TestWithGzip(t *testing.T) {
	job := NewJob("job", "mysql", testDBDsn, WithGzip(true))
	assert.True(t, job.Gzip)
}

func TestWithSshKey(t *testing.T) {
	job := NewJob("job", "mysql", testDBDsn, WithSshKey("ssh key"))
	assert.Equal(t, "ssh key", job.SshKey)
}

func TestValidateDump(t *testing.T) {
	assert := assert.New(t)

	jobs := make([]*Job, 0)
	job1 := NewJob(
		"job1",
		"mysql",
		testDBDsn,
		WithGzip(true),
		WithDumpOptions("--skip-comments"),
		WithSshKey("====privatekey===="),
		WithSshUser("root"),
		WithSshHost("localhost"),
	)
	jobs = append(jobs, job1)

	dump := Dump{MaxJobs: DefaultMaxConcurrentJobs, Jobs: jobs}

	err := dump.Validate()
	assert.Nil(err)

	job2 := NewJob("", "mysql", "")
	jobs = append(jobs, job2)
	dump.Jobs = jobs
	err = dump.Validate()
	assert.ErrorIs(err, ErrMissingJobName)

	job3 := NewJob("job3", "mysql", "")
	jobs = append(jobs, job3)
	dump.Jobs = jobs
	err = dump.Validate()
	assert.ErrorIs(err, ErrMissingDBDsn)

	job4 := NewJob("job3", "", testDBDsn)
	jobs = append(jobs, job4)
	dump.Jobs = jobs
	err = dump.Validate()

	assert.ErrorIs(err, ErrMissingDBDriver)
}

func TestResultString(t *testing.T) {
	assert := assert.New(t)
	r1 := &jobresult.JobResult{
		JobName: "job1",
		Elapsed: time.Second,
	}

	s := r1.String()
	assert.Equal("job1 succeeded, it took 1s", s)

	r2 := &jobresult.JobResult{
		Error:   errors.New("test err"),
		JobName: "job1",
		Elapsed: time.Second,
	}

	s = r2.String()
	assert.Equal("job1 failed, it took 1s with error: test err", s)
}

func TestJobEncryption(t *testing.T) {
	assert := assert.New(t)
	job := NewJob("job", "mysql", testDBDsn)
	assert.False(job.Encrypted())
	assert.NoError(job.Validate())

	job.Encryption = encryption.Config{Enabled: true, KeySource: "not-a-source"}
	assert.True(job.Encrypted())
	err := job.Validate()
	assert.Error(err)
	assert.Contains(err.Error(), "unsupported")

	job.Encryption = encryption.Config{Enabled: false, KeySource: "not-a-source"}
	assert.False(job.Encrypted())
	assert.NoError(job.Validate())
}

func TestViaSsh(t *testing.T) {
	assert := assert.New(t)
	job := &Job{}
	assert.False(job.ViaSsh())

	job.SshHost = "mydump.com"
	job.SshUser = "admin"
	job.SshKey = "my-ssh-key"

	assert.True(job.ViaSsh())
}
