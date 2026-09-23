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
	"github.com/cockroachdb/pebble/vfs"
	"github.com/cockroachdb/pebble/vfs/errorfs"
	"github.com/stretchr/testify/require"
)

type swallowFatalLogger struct{}

func (swallowFatalLogger) Infof(string, ...interface{})  {}
func (swallowFatalLogger) Errorf(string, ...interface{}) {}
func (swallowFatalLogger) Fatalf(string, ...interface{}) {}

func TestBatchDurableAndWait(t *testing.T) {
	defer leaktest.AfterTest(t)()

	var mu sync.Mutex
	var infos []BatchDurableInfo
	el := &EventListener{
		BatchDurable: func(info BatchDurableInfo) {
			mu.Lock()
			infos = append(infos, info)
			mu.Unlock()
		},
	}
	d, err := Open("", &Options{
		FS:            vfs.NewMem(),
		EventListener: el,
		Logger:        swallowFatalLogger{},
	})
	require.NoError(t, err)

	require.Equal(t, DurabilityStats{}, d.DurabilityStats())
	seq, stateErr := d.DurableState()
	require.NoError(t, stateErr)
	require.Equal(t, base.SeqNum(0), seq)
	m := d.Metrics()
	require.Zero(t, m.DurableCommitCount)
	require.Zero(t, m.DurableCommitDuration)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = d.WaitForDurabilityContext(ctx, 0)
	cancel()
	require.ErrorIs(t, err, context.DeadlineExceeded)

	require.NoError(t, d.Set([]byte("nosync"), []byte("v"), NoSync))
	mu.Lock()
	require.Empty(t, infos)
	mu.Unlock()
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = d.WaitForDurabilityContext(ctx, 0)
	cancel()
	require.ErrorIs(t, err, context.DeadlineExceeded)

	b := d.NewBatch()
	require.NoError(t, b.Set([]byte("a"), []byte("va"), nil))
	require.NoError(t, b.Set([]byte("b"), []byte("vb"), nil))
	wantSize := len(b.Repr())
	wantCount := b.Count()
	require.NoError(t, d.Apply(b, &WriteOptions{Sync: true, CommitCorrelationID: 42}))
	batchSeq := b.SeqNum()
	require.NoError(t, b.Close())

	mu.Lock()
	require.Len(t, infos, 1)
	info := infos[0]
	mu.Unlock()
	require.NoError(t, info.Err)
	require.Equal(t, batchSeq, info.SeqNum)
	require.Greater(t, info.SeqNum, base.SeqNum(0))
	require.Equal(t, uint64(42), info.CorrelationID)
	require.Equal(t, wantSize, info.BatchSize)
	require.Equal(t, wantCount, info.KeyCount)
	require.Positive(t, info.ApplyDuration)
	require.Positive(t, info.SyncDuration)
	require.Positive(t, info.JobID)

	require.NoError(t, d.WaitForDurability(0))
	require.NoError(t, d.WaitForDurability(info.SeqNum))
	require.NoError(t, d.WaitForDurability(info.SeqNum+base.SeqNum(info.KeyCount)-1))
	require.NoError(t, d.WaitForDurabilityBatch(nil))
	require.NoError(t, d.WaitForDurabilityBatch([]base.SeqNum{}))
	require.NoError(t, d.WaitForDurabilityBatch([]base.SeqNum{info.SeqNum, info.SeqNum + 1}))
	require.NoError(t, d.WaitForJobDurability(info.JobID))

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, d.WaitForDurabilityContext(cancelled, info.SeqNum))

	unknown := d.WaitForJobDurability(0)
	require.Error(t, unknown)
	require.Contains(t, unknown.Error(), "unknown")
	never := d.WaitForJobDurability(info.JobID + 1000)
	require.Error(t, never)
	require.Contains(t, never.Error(), "unknown")

	st := d.DurabilityStats()
	require.Equal(t, info.SeqNum+base.SeqNum(info.KeyCount)-1, st.HighestDurableSeqNum)
	require.NoError(t, st.FirstErr)
	require.Equal(t, int64(0), st.PendingWaiters)
	require.Equal(t, uint64(1), st.TotalDurableCommits)
	require.Equal(t, uint64(0), st.TotalFailedCommits)
	require.Positive(t, st.CumulativeSyncDuration)
	require.Equal(t, st.CumulativeSyncDuration, st.MaxSyncDuration)
	high, first := d.DurableState()
	require.Equal(t, st.HighestDurableSeqNum, high)
	require.NoError(t, first)

	m = d.Metrics()
	require.Equal(t, uint64(1), m.DurableCommitCount)
	require.Positive(t, m.DurableCommitDuration)
	require.Equal(t, st.CumulativeSyncDuration, m.DurableCommitDuration)

	ch := d.DurabilityNotify(info.SeqNum)
	select {
	case err := <-ch:
		require.NoError(t, err)
	default:
		t.Fatal("DurabilityNotify was not prefilled")
	}

	pendingCh := d.DurabilityNotify(info.SeqNum + 100)
	select {
	case err := <-pendingCh:
		t.Fatalf("future notify delivered early: %v", err)
	default:
	}

	blocked := make(chan struct{})
	waitErr := make(chan error, 1)
	go func() {
		close(blocked)
		waitErr <- d.WaitForDurability(info.SeqNum + 50)
	}()
	<-blocked
	require.Eventually(t, func() bool {
		return d.DurabilityStats().PendingWaiters == 1
	}, 2*time.Second, 5*time.Millisecond)

	require.NoError(t, d.Close())
	require.ErrorIs(t, <-waitErr, ErrClosed)
	select {
	case err := <-pendingCh:
		require.ErrorIs(t, err, ErrClosed)
	case <-time.After(2 * time.Second):
		t.Fatal("notify was not released on close")
	}

	closedCtx, cancel := context.WithCancel(context.Background())
	cancel()
	err = d.WaitForDurabilityContext(closedCtx, info.SeqNum+100)
	require.ErrorIs(t, err, ErrClosed)
	require.NotErrorIs(t, err, context.Canceled)
}

func TestBatchDurableNoListenerMetrics(t *testing.T) {
	defer leaktest.AfterTest(t)()
	d, err := Open("", &Options{FS: vfs.NewMem(), Logger: swallowFatalLogger{}})
	require.NoError(t, err)
	defer d.Close()

	require.NoError(t, d.Set([]byte("a"), []byte("b"), Sync))
	require.Zero(t, d.Metrics().DurableCommitCount)
	require.Zero(t, d.Metrics().DurableCommitDuration)
	st := d.DurabilityStats()
	require.Equal(t, uint64(1), st.TotalDurableCommits)
	require.Positive(t, st.CumulativeSyncDuration)
	require.NoError(t, d.WaitForDurability(0))
}

func TestBatchDurableTeeAndNoSyncWait(t *testing.T) {
	defer leaktest.AfterTest(t)()
	var n1, n2 atomic.Int32
	el := TeeEventListener(
		EventListener{BatchDurable: func(BatchDurableInfo) { n1.Add(1) }},
		EventListener{BatchDurable: func(BatchDurableInfo) { n2.Add(1) }},
	)
	d, err := Open("", &Options{
		FS:            vfs.NewMem(),
		EventListener: &el,
		Logger:        swallowFatalLogger{},
	})
	require.NoError(t, err)
	defer d.Close()

	b := d.NewBatch()
	require.NoError(t, b.Set([]byte("k"), []byte("v"), nil))
	require.NoError(t, d.ApplyNoSyncWait(b, Sync))
	require.NoError(t, b.SyncWait())
	require.NoError(t, b.Close())
	require.Eventually(t, func() bool {
		return n1.Load() == 1 && n2.Load() == 1
	}, 2*time.Second, 5*time.Millisecond)
	require.Equal(t, uint64(1), d.Metrics().DurableCommitCount)

	// A second SyncWait must not emit another event. The batch is already
	// applied; commit another sync write and ensure the tee count is exactly 2.
	require.NoError(t, d.Set([]byte("k2"), []byte("v2"), Sync))
	require.Eventually(t, func() bool {
		return n1.Load() == 2 && n2.Load() == 2
	}, 2*time.Second, 5*time.Millisecond)
}

func TestBatchDurableDisableWAL(t *testing.T) {
	defer leaktest.AfterTest(t)()
	var n atomic.Int32
	d, err := Open("", &Options{
		FS:         vfs.NewMem(),
		DisableWAL: true,
		EventListener: &EventListener{
			BatchDurable: func(BatchDurableInfo) { n.Add(1) },
		},
		Logger: swallowFatalLogger{},
	})
	require.NoError(t, err)
	defer d.Close()

	require.NoError(t, d.Set([]byte("a"), []byte("b"), NoSync))
	require.Zero(t, n.Load())
	require.NoError(t, d.WaitForDurability(0))
	require.NoError(t, d.WaitForDurability(1<<40))
	require.NoError(t, d.WaitForDurabilityBatch([]base.SeqNum{1, 2, 3}))
	require.NoError(t, d.WaitForJobDurability(0))
	ch := d.DurabilityNotify(99)
	select {
	case err := <-ch:
		require.NoError(t, err)
	default:
		t.Fatal("expected prefilled nil notify")
	}
	require.Error(t, d.Set([]byte("c"), []byte("d"), Sync))
	require.Zero(t, n.Load())
}

func TestDurabilityJobExpiredAndNotifyLimit(t *testing.T) {
	defer leaktest.AfterTest(t)()
	var mu sync.Mutex
	var firstJob int
	d, err := Open("", &Options{
		FS: vfs.NewMem(),
		EventListener: &EventListener{
			BatchDurable: func(info BatchDurableInfo) {
				mu.Lock()
				if firstJob == 0 {
					firstJob = info.JobID
				}
				mu.Unlock()
			},
		},
		Logger: swallowFatalLogger{},
	})
	require.NoError(t, err)
	defer d.Close()

	for i := 0; i < durabilityJobHistory+1; i++ {
		require.NoError(t, d.Set([]byte{byte(i)}, []byte("v"), Sync))
	}
	mu.Lock()
	job := firstJob
	mu.Unlock()
	require.Positive(t, job)
	err = d.WaitForJobDurability(job)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expired")
	require.NotContains(t, err.Error(), "unknown")

	var chans []<-chan error
	target := base.SeqNum(1 << 40)
	for i := 0; i < durabilityNotifyLimit+1; i++ {
		chans = append(chans, d.DurabilityNotify(target))
	}
	select {
	case err := <-chans[len(chans)-1]:
		require.Error(t, err)
	default:
		t.Fatal("excess notify was not prefilled")
	}
	select {
	case err := <-chans[0]:
		t.Fatalf("in-limit notify delivered early: %v", err)
	default:
	}
}

func TestBatchDurableSyncError(t *testing.T) {
	defer leaktest.AfterTest(t)()
	var inject atomic.Bool
	fs := errorfs.Wrap(vfs.NewMem(), errorfs.InjectorFunc(func(op errorfs.Op) error {
		if inject.Load() && strings.Contains(op.Path, ".log") &&
			(op.Kind == errorfs.OpFileSync || op.Kind == errorfs.OpFileSyncData || op.Kind == errorfs.OpFileSyncTo) {
			return errors.New("wal sync failed")
		}
		return nil
	}))
	var mu sync.Mutex
	var info BatchDurableInfo
	var saw atomic.Bool
	d, err := Open("", &Options{
		FS: fs,
		EventListener: &EventListener{
			BatchDurable: func(in BatchDurableInfo) {
				mu.Lock()
				info = in
				mu.Unlock()
				saw.Store(true)
			},
		},
		Logger: swallowFatalLogger{},
	})
	require.NoError(t, err)
	defer func() { _ = d.Close() }()

	inject.Store(true)
	_ = d.Set([]byte("a"), []byte("b"), Sync)
	require.True(t, saw.Load())
	mu.Lock()
	got := info
	mu.Unlock()
	require.Error(t, got.Err)
	require.Contains(t, got.Err.Error(), "wal sync failed")

	err = d.WaitForDurability(got.SeqNum)
	require.Error(t, err)
	require.Contains(t, err.Error(), "wal sync failed")
	st := d.DurabilityStats()
	require.Equal(t, uint64(1), st.TotalFailedCommits)
	require.Equal(t, uint64(0), st.TotalDurableCommits)
	require.Error(t, st.FirstErr)
	_, first := d.DurableState()
	require.Equal(t, st.FirstErr, first)
	require.Zero(t, d.Metrics().DurableCommitCount)

	jobErr := d.WaitForJobDurability(got.JobID)
	require.Error(t, jobErr)
	require.Contains(t, jobErr.Error(), "wal sync failed")
}
