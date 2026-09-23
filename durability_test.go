// Copyright 2026 The LevelDB-Go and Pebble Authors. All rights reserved. Use
// of this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package pebble

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/crlib/testutils/leaktest"
	"github.com/cockroachdb/errors"
	"github.com/cockroachdb/pebble/internal/base"
	"github.com/cockroachdb/pebble/internal/testutils"
	"github.com/cockroachdb/pebble/vfs"
	"github.com/stretchr/testify/require"
)

func openDurabilityTestDB(t *testing.T, opts *Options) *DB {
	t.Helper()
	if opts == nil {
		opts = &Options{}
	}
	if opts.FS == nil {
		opts.FS = vfs.NewMem()
	}
	opts.Logger = testutils.Logger{T: t}
	d, err := Open("", opts)
	require.NoError(t, err)
	return d
}

func TestDurabilitySyncCommit(t *testing.T) {
	defer leaktest.AfterTest(t)()

	var got atomic.Int32
	ch := make(chan BatchDurableInfo, 4)
	opts := &Options{
		FS: vfs.NewMem(),
		EventListener: &EventListener{
			BatchDurable: func(info BatchDurableInfo) {
				got.Add(1)
				ch <- info
			},
		},
	}
	d := openDurabilityTestDB(t, opts)
	defer d.Close()

	st := d.DurabilityStats()
	require.Equal(t, DurabilityStats{}, st)
	seq, err := d.DurableState()
	require.NoError(t, err)
	require.Equal(t, base.SeqNum(0), seq)

	require.NoError(t, d.Set([]byte("nosync"), []byte("v"), NoSync))
	require.Equal(t, int32(0), got.Load())
	require.Equal(t, uint64(0), d.DurabilityStats().TotalDurableCommits)
	require.Equal(t, uint64(0), d.Metrics().DurableCommitCount)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, d.WaitForDurabilityContext(ctx, 0), context.DeadlineExceeded)

	b := d.NewBatch()
	require.NoError(t, b.Set([]byte("k"), []byte("v"), nil))
	require.NoError(t, b.Set([]byte("k2"), []byte("v2"), nil))
	reprLen := len(b.Repr())
	wopts := &WriteOptions{Sync: true, CommitCorrelationID: 42}
	require.NoError(t, d.Apply(b, wopts))
	require.Equal(t, int32(1), got.Load())

	info := <-ch
	require.Equal(t, b.SeqNum(), info.SeqNum)
	require.NoError(t, info.Err)
	require.Greater(t, info.ApplyDuration, time.Duration(0))
	require.Greater(t, info.SyncDuration, time.Duration(0))
	require.Equal(t, uint64(42), info.CorrelationID)
	require.Equal(t, reprLen, info.BatchSize)
	require.Equal(t, uint32(2), info.KeyCount)
	require.Positive(t, info.JobID)

	require.NoError(t, d.WaitForDurability(0))
	require.NoError(t, d.WaitForDurability(info.SeqNum))
	require.NoError(t, d.WaitForDurability(info.SeqNum+1))
	require.NoError(t, d.WaitForJobDurability(info.JobID))
	require.NoError(t, d.WaitForDurabilityBatch([]base.SeqNum{0, info.SeqNum, info.SeqNum + 1}))
	require.NoError(t, d.WaitForDurabilityBatch(nil))
	require.NoError(t, d.WaitForDurabilityBatch([]base.SeqNum{}))

	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	require.NoError(t, d.WaitForDurabilityContext(cancelled, info.SeqNum))

	stateSeq, stateErr := d.DurableState()
	require.NoError(t, stateErr)
	require.Equal(t, info.SeqNum+1, stateSeq)

	stats := d.DurabilityStats()
	require.Equal(t, info.SeqNum+1, stats.HighestDurableSeqNum)
	require.NoError(t, stats.FirstErr)
	require.Equal(t, int64(0), stats.PendingWaiters)
	require.Equal(t, uint64(1), stats.TotalDurableCommits)
	require.Equal(t, uint64(0), stats.TotalFailedCommits)
	require.Greater(t, stats.CumulativeSyncDuration, time.Duration(0))
	require.Equal(t, stats.MaxSyncDuration, stats.CumulativeSyncDuration)

	m := d.Metrics()
	require.Equal(t, uint64(1), m.DurableCommitCount)
	require.Equal(t, info.SyncDuration, m.DurableCommitDuration)

	nch := d.DurabilityNotify(info.SeqNum)
	select {
	case err := <-nch:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("expected pre-filled notify channel")
	}

	unknown := d.WaitForJobDurability(0)
	require.Error(t, unknown)
	require.Contains(t, unknown.Error(), "unknown")
	never := d.WaitForJobDurability(info.JobID + 1000)
	require.Error(t, never)
	require.Contains(t, never.Error(), "unknown")
}

func TestDurabilityMetricsRequireCallback(t *testing.T) {
	defer leaktest.AfterTest(t)()
	d := openDurabilityTestDB(t, &Options{FS: vfs.NewMem()})
	defer d.Close()
	require.NoError(t, d.Set([]byte("k"), []byte("v"), Sync))
	require.Equal(t, uint64(1), d.DurabilityStats().TotalDurableCommits)
	require.Equal(t, uint64(0), d.Metrics().DurableCommitCount)
	require.Equal(t, time.Duration(0), d.Metrics().DurableCommitDuration)
}

func TestDurabilityTeeAndNoSync(t *testing.T) {
	defer leaktest.AfterTest(t)()
	var a, b atomic.Int32
	listener := TeeEventListener(
		EventListener{BatchDurable: func(BatchDurableInfo) { a.Add(1) }},
		EventListener{BatchDurable: func(BatchDurableInfo) { b.Add(1) }},
	)
	d := openDurabilityTestDB(t, &Options{FS: vfs.NewMem(), EventListener: &listener})
	defer d.Close()
	require.NoError(t, d.Set([]byte("k"), []byte("v"), NoSync))
	require.Equal(t, int32(0), a.Load())
	require.Equal(t, int32(0), b.Load())
	require.NoError(t, d.Set([]byte("k"), []byte("v"), Sync))
	require.Equal(t, int32(1), a.Load())
	require.Equal(t, int32(1), b.Load())
}

func TestDurabilityDisableWAL(t *testing.T) {
	defer leaktest.AfterTest(t)()
	var got atomic.Int32
	d := openDurabilityTestDB(t, &Options{
		FS:         vfs.NewMem(),
		DisableWAL: true,
		EventListener: &EventListener{
			BatchDurable: func(BatchDurableInfo) { got.Add(1) },
		},
	})
	defer d.Close()

	require.NoError(t, d.Set([]byte("k"), []byte("v"), NoSync))
	require.Error(t, d.Set([]byte("k2"), []byte("v"), Sync))
	require.Equal(t, int32(0), got.Load())
	require.NoError(t, d.WaitForDurability(0))
	require.NoError(t, d.WaitForDurability(100))
	require.NoError(t, d.WaitForDurabilityBatch([]base.SeqNum{1, 2, 3}))
	require.NoError(t, d.WaitForJobDurability(0))
	require.NoError(t, d.WaitForJobDurability(99))
	seq, err := d.DurableState()
	require.NoError(t, err)
	require.Equal(t, base.SeqNum(0), seq)
	ch := d.DurabilityNotify(50)
	select {
	case err := <-ch:
		require.NoError(t, err)
	default:
		t.Fatal("expected immediate nil notification")
	}
}

func TestDurabilityJobExpiration(t *testing.T) {
	defer leaktest.AfterTest(t)()
	ch := make(chan BatchDurableInfo, durabilityJobWindow+2)
	d := openDurabilityTestDB(t, &Options{
		FS: vfs.NewMem(),
		EventListener: &EventListener{
			BatchDurable: func(info BatchDurableInfo) { ch <- info },
		},
	})
	defer d.Close()

	require.NoError(t, d.Set([]byte("k"), []byte("v"), Sync))
	first := <-ch
	for i := 0; i < durabilityJobWindow; i++ {
		require.NoError(t, d.Set([]byte("k"), []byte{byte(i)}, Sync))
		<-ch
	}
	err := d.WaitForJobDurability(first.JobID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expired")
	require.NotContains(t, strings.ToLower(err.Error()), "unknown")
}

func TestDurabilityCloseAndCancel(t *testing.T) {
	defer leaktest.AfterTest(t)()
	d := openDurabilityTestDB(t, &Options{FS: vfs.NewMem()})

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.WaitForDurability(base.SeqNum(1 << 60))
	}()
	require.Eventually(t, func() bool {
		return d.DurabilityStats().PendingWaiters == 1
	}, 5*time.Second, time.Millisecond)
	require.NoError(t, d.Close())
	require.ErrorIs(t, <-errCh, ErrClosed)

	// Notifications created before close are failed; a fresh DB covers the
	// already-closed notify path below.
	d = openDurabilityTestDB(t, &Options{FS: vfs.NewMem()})
	nch := d.DurabilityNotify(base.SeqNum(1 << 60))
	require.NoError(t, d.Close())
	select {
	case err := <-nch:
		require.ErrorIs(t, err, ErrClosed)
	case <-time.After(5 * time.Second):
		t.Fatal("notify was not failed on close")
	}

	d = openDurabilityTestDB(t, &Options{FS: vfs.NewMem()})
	defer d.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	require.ErrorIs(t, d.WaitForDurabilityContext(ctx, base.SeqNum(1<<60)), context.Canceled)
	require.Equal(t, int64(0), d.DurabilityStats().PendingWaiters)
}

func TestDurabilityNotifyLimit(t *testing.T) {
	defer leaktest.AfterTest(t)()
	d := openDurabilityTestDB(t, &Options{FS: vfs.NewMem()})
	defer d.Close()

	hit := false
	for i := 0; i < durabilityMaxSubscriptions+10; i++ {
		ch := d.DurabilityNotify(base.SeqNum(1 << 60))
		select {
		case err := <-ch:
			require.Error(t, err)
			hit = true
		default:
		}
		if hit {
			break
		}
	}
	require.True(t, hit)
}

func TestDurabilitySyncError(t *testing.T) {
	defer leaktest.AfterTest(t)()
	fs := &failWALSyncFS{FS: vfs.NewMem()}
	ch := make(chan BatchDurableInfo, 1)
	d := openDurabilityTestDB(t, &Options{
		FS: fs,
		EventListener: &EventListener{
			BatchDurable: func(info BatchDurableInfo) { ch <- info },
		},
	})

	b := d.NewBatch()
	require.NoError(t, b.Set([]byte("k"), []byte("v"), nil))
	fs.fail.Store(true)
	require.NoError(t, d.ApplyNoSyncWait(b, Sync))
	require.Error(t, b.SyncWait())

	info := <-ch
	require.Error(t, info.Err)
	require.Equal(t, b.SeqNum(), info.SeqNum)
	waitErr := d.WaitForDurability(info.SeqNum)
	require.Error(t, waitErr)
	_, stateErr := d.DurableState()
	require.Error(t, stateErr)
	require.Equal(t, uint64(1), d.DurabilityStats().TotalFailedCommits)
	require.Equal(t, uint64(0), d.Metrics().DurableCommitCount)

	nch := d.DurabilityNotify(info.SeqNum)
	select {
	case err := <-nch:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("expected failed notify")
	}

	fs.fail.Store(false)
	d.Close()
}

func TestDurabilityPendingWaiters(t *testing.T) {
	defer leaktest.AfterTest(t)()
	fs := &gateWALSyncFS{
		FS:      vfs.NewMem(),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	d := openDurabilityTestDB(t, &Options{FS: fs})
	fs.block.Store(true)

	setDone := make(chan error, 1)
	go func() {
		setDone <- d.Set([]byte("k"), []byte("v"), Sync)
	}()
	select {
	case <-fs.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for wal sync")
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- d.WaitForDurability(0)
	}()
	require.Eventually(t, func() bool {
		return d.DurabilityStats().PendingWaiters == 1
	}, 5*time.Second, time.Millisecond)

	close(fs.release)
	require.NoError(t, <-setDone)
	require.NoError(t, <-waitDone)
	require.Equal(t, int64(0), d.DurabilityStats().PendingWaiters)
	require.NoError(t, d.Close())
}

type failWALSyncFS struct {
	vfs.FS
	fail atomic.Bool
}

func (fs *failWALSyncFS) Create(name string, category vfs.DiskWriteCategory) (vfs.File, error) {
	f, err := fs.FS.Create(name, category)
	if err != nil {
		return nil, err
	}
	return &failWALSyncFile{File: f, fs: fs, name: name}, nil
}

func (fs *failWALSyncFS) Open(name string, opts ...vfs.OpenOption) (vfs.File, error) {
	f, err := fs.FS.Open(name, opts...)
	if err != nil {
		return nil, err
	}
	return &failWALSyncFile{File: f, fs: fs, name: name}, nil
}

func (fs *failWALSyncFS) ReuseForWrite(oldname, newname string, category vfs.DiskWriteCategory) (vfs.File, error) {
	f, err := fs.FS.ReuseForWrite(oldname, newname, category)
	if err != nil {
		return nil, err
	}
	return &failWALSyncFile{File: f, fs: fs, name: newname}, nil
}

type failWALSyncFile struct {
	vfs.File
	fs   *failWALSyncFS
	name string
}

func (f *failWALSyncFile) Sync() error {
	if err := f.maybeFail(); err != nil {
		return err
	}
	return f.File.Sync()
}

func (f *failWALSyncFile) SyncData() error {
	if err := f.maybeFail(); err != nil {
		return err
	}
	return f.File.SyncData()
}

func (f *failWALSyncFile) maybeFail() error {
	// WAL files are wrapped by vfs.SyncingFile, whose Sync calls SyncData on
	// the inner file.
	if f.fs.fail.Load() && strings.HasSuffix(f.name, ".log") {
		return errors.New("injected wal sync failure")
	}
	return nil
}

type gateWALSyncFS struct {
	vfs.FS
	block   atomic.Bool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (fs *gateWALSyncFS) Create(name string, category vfs.DiskWriteCategory) (vfs.File, error) {
	f, err := fs.FS.Create(name, category)
	if err != nil {
		return nil, err
	}
	return &gateWALSyncFile{File: f, fs: fs, name: name}, nil
}

type gateWALSyncFile struct {
	vfs.File
	fs   *gateWALSyncFS
	name string
}

func (f *gateWALSyncFile) Sync() error {
	f.maybeBlock()
	return f.File.Sync()
}

func (f *gateWALSyncFile) SyncData() error {
	f.maybeBlock()
	return f.File.SyncData()
}

func (f *gateWALSyncFile) maybeBlock() {
	if f.fs.block.Load() && strings.HasSuffix(f.name, ".log") {
		f.fs.once.Do(func() { close(f.fs.entered) })
		<-f.fs.release
	}
}
