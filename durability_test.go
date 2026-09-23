// Copyright 2026 The LevelDB-Go and Pebble Authors. All rights reserved. Use
// of this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package pebble

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/cockroachdb/pebble/internal/base"
	"github.com/cockroachdb/pebble/vfs"
	"github.com/cockroachdb/pebble/vfs/errorfs"
	"github.com/stretchr/testify/require"
)

type batchDurableRecorder struct {
	mu    sync.Mutex
	infos []BatchDurableInfo
}

func (r *batchDurableRecorder) record(info BatchDurableInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.infos = append(r.infos, info)
}

func (r *batchDurableRecorder) get() []BatchDurableInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]BatchDurableInfo(nil), r.infos...)
}

func openDurabilityTestDB(t *testing.T, opts *Options) *DB {
	t.Helper()
	if opts.FS == nil {
		opts.FS = vfs.NewMem()
	}
	d, err := Open("", opts)
	require.NoError(t, err)
	return d
}

func TestBatchDurableEvent(t *testing.T) {
	var rec batchDurableRecorder
	d := openDurabilityTestDB(t, &Options{
		EventListener: &EventListener{BatchDurable: rec.record},
	})
	defer func() { require.NoError(t, d.Close()) }()

	// Non-sync commits never fire.
	require.NoError(t, d.Set([]byte("a"), []byte("1"), NoSync))
	require.Empty(t, rec.get())

	b := d.NewBatch()
	require.NoError(t, b.Set([]byte("b"), []byte("2"), nil))
	require.NoError(t, b.Set([]byte("c"), []byte("3"), nil))
	require.NoError(t, b.Delete([]byte("a"), nil))
	size := len(b.Repr())
	require.NoError(t, d.Apply(b, &WriteOptions{Sync: true, CommitCorrelationID: 42}))
	seqNum := b.SeqNum()
	require.NoError(t, b.Close())

	infos := rec.get()
	require.Len(t, infos, 1)
	info := infos[0]
	require.NoError(t, info.Err)
	require.Equal(t, seqNum, info.SeqNum)
	require.Equal(t, uint64(42), info.CorrelationID)
	require.Equal(t, size, info.BatchSize)
	require.Equal(t, uint32(3), info.KeyCount)
	require.Positive(t, info.JobID)
	require.Positive(t, info.ApplyDuration)
	require.Positive(t, info.SyncDuration)
	require.Contains(t, info.String(), fmt.Sprintf("[JOB %d]", info.JobID))

	durable, err := d.DurableState()
	require.NoError(t, err)
	require.Equal(t, seqNum+2, durable)
	require.NoError(t, d.WaitForJobDurability(info.JobID))

	// Nil WriteOptions default to Sync.
	require.NoError(t, d.Set([]byte("d"), []byte("4"), nil))
	infos = rec.get()
	require.Len(t, infos, 2)
	require.Equal(t, info.JobID+1, infos[1].JobID)
	require.Zero(t, infos[1].CorrelationID)

	m := d.Metrics()
	require.Equal(t, uint64(2), m.DurableCommitCount)
	require.Equal(t, infos[0].SyncDuration+infos[1].SyncDuration, m.DurableCommitDuration)

	stats := d.DurabilityStats()
	require.Equal(t, uint64(2), stats.TotalDurableCommits)
	require.Zero(t, stats.TotalFailedCommits)
	require.Equal(t, m.DurableCommitDuration, stats.CumulativeSyncDuration)
	require.Equal(t, max(infos[0].SyncDuration, infos[1].SyncDuration), stats.MaxSyncDuration)
	require.Zero(t, stats.PendingWaiters)
}

func TestBatchDurableApplyNoSyncWait(t *testing.T) {
	var rec batchDurableRecorder
	d := openDurabilityTestDB(t, &Options{
		EventListener: &EventListener{BatchDurable: rec.record},
	})
	defer func() { require.NoError(t, d.Close()) }()

	b := d.NewBatch()
	require.NoError(t, b.Set([]byte("a"), []byte("1"), nil))
	require.NoError(t, d.ApplyNoSyncWait(b, &WriteOptions{Sync: true, CommitCorrelationID: 7}))
	seqNum := b.SeqNum()
	require.NoError(t, b.SyncWait())
	require.NoError(t, b.Close())

	infos := rec.get()
	require.Len(t, infos, 1)
	require.Equal(t, seqNum, infos[0].SeqNum)
	require.Equal(t, uint64(7), infos[0].CorrelationID)
	require.Positive(t, infos[0].ApplyDuration)
	require.Positive(t, infos[0].SyncDuration)
	require.NoError(t, d.WaitForDurability(seqNum))
}

func TestBatchDurableTeeAndMetricsGating(t *testing.T) {
	// Without a configured BatchDurable, metrics do not accumulate but the
	// durability state is still tracked.
	d := openDurabilityTestDB(t, &Options{})
	require.NoError(t, d.Set([]byte("a"), []byte("1"), Sync))
	m := d.Metrics()
	require.Zero(t, m.DurableCommitCount)
	require.Zero(t, m.DurableCommitDuration)
	require.Equal(t, uint64(1), d.DurabilityStats().TotalDurableCommits)
	require.NoError(t, d.Close())

	var a, b batchDurableRecorder
	opts := &Options{EventListener: &EventListener{BatchDurable: a.record}}
	opts.AddEventListener(EventListener{BatchDurable: b.record})
	d = openDurabilityTestDB(t, opts)
	require.NoError(t, d.Set([]byte("a"), []byte("1"), Sync))
	require.Len(t, a.get(), 1)
	require.Equal(t, a.get(), b.get())
	require.Equal(t, uint64(1), d.Metrics().DurableCommitCount)
	require.NoError(t, d.Close())

	require.False(t, isBatchDurableConfigured(TeeEventListener(EventListener{}, EventListener{}).BatchDurable))
	require.False(t, isBatchDurableConfigured(MakeLoggingEventListener(nil).BatchDurable))
}

func TestDurabilityWaitAPIs(t *testing.T) {
	d := openDurabilityTestDB(t, &Options{})
	defer func() { require.NoError(t, d.Close()) }()

	require.Equal(t, DurabilityStats{}, d.DurabilityStats())
	seq, err := d.DurableState()
	require.NoError(t, err)
	require.Zero(t, seq)
	require.NoError(t, d.WaitForDurability(0))
	require.NoError(t, d.WaitForDurabilityBatch(nil))
	require.NoError(t, d.WaitForDurabilityBatch([]base.SeqNum{}))

	require.True(t, errors.Is(d.WaitForJobDurability(0), ErrDurabilityJobUnknown))
	require.True(t, errors.Is(d.WaitForJobDurability(1), ErrDurabilityJobUnknown))
	require.Contains(t, d.WaitForJobDurability(-1).Error(), "unknown")

	// A non-sync commit is not durable until a later sync commit.
	require.NoError(t, d.Set([]byte("a"), []byte("1"), NoSync))
	nonSyncSeq := d.mu.versions.visibleSeqNum.Load() - 1
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	require.ErrorIs(t, d.WaitForDurabilityContext(ctx, nonSyncSeq), context.DeadlineExceeded)
	cancel()

	// A blocked waiter is counted and released by a later sync commit.
	ch := d.DurabilityNotify(nonSyncSeq)
	errCh := make(chan error, 1)
	go func() { errCh <- d.WaitForDurabilityBatch([]base.SeqNum{1, nonSyncSeq}) }()
	require.Eventually(t, func() bool { return d.DurabilityStats().PendingWaiters == 1 },
		10*time.Second, time.Millisecond)
	select {
	case err := <-ch:
		t.Fatalf("notification delivered early: %v", err)
	default:
	}

	require.NoError(t, d.Set([]byte("b"), []byte("2"), Sync))
	require.NoError(t, <-errCh)
	require.NoError(t, <-ch)
	require.Zero(t, d.DurabilityStats().PendingWaiters)

	syncSeq := d.mu.versions.visibleSeqNum.Load() - 1
	seq, err = d.DurableState()
	require.NoError(t, err)
	require.Equal(t, syncSeq, seq)
	require.NoError(t, d.WaitForDurability(nonSyncSeq))
	require.NoError(t, d.WaitForDurabilityBatch([]base.SeqNum{nonSyncSeq, syncSeq}))
	require.NoError(t, <-d.DurabilityNotify(syncSeq))
	require.NoError(t, d.WaitForJobDurability(1))

	// Cancelled contexts are reported for waits that cannot be satisfied, but
	// not for ones that already are.
	cctx, ccancel := context.WithCancel(context.Background())
	ccancel()
	require.NoError(t, d.WaitForDurabilityContext(cctx, syncSeq))
	require.ErrorIs(t, d.WaitForDurabilityContext(cctx, syncSeq+1), context.Canceled)
	require.NoError(t, d.WaitForJobDurabilityContext(cctx, 1))
	require.Zero(t, d.DurabilityStats().PendingWaiters)
}

func TestDurabilityJobRetention(t *testing.T) {
	d := openDurabilityTestDB(t, &Options{})
	defer func() { require.NoError(t, d.Close()) }()

	for i := 0; i < durabilityJobRetention+5; i++ {
		require.NoError(t, d.Set([]byte("k"), []byte("v"), Sync))
	}
	last := durabilityJobRetention + 5
	for _, id := range []int{1, 5} {
		err := d.WaitForJobDurability(id)
		require.True(t, errors.Is(err, ErrDurabilityJobExpired), "%v", err)
		require.Contains(t, err.Error(), "expired")
	}
	for _, id := range []int{6, last} {
		require.NoError(t, d.WaitForJobDurability(id))
	}
	err := d.WaitForJobDurability(last + 1)
	require.True(t, errors.Is(err, ErrDurabilityJobUnknown), "%v", err)
	require.Contains(t, err.Error(), "unknown")
}

func TestDurabilityClose(t *testing.T) {
	d := openDurabilityTestDB(t, &Options{})
	require.NoError(t, d.Set([]byte("a"), []byte("1"), Sync))
	durable, _ := d.DurableState()

	errCh := make(chan error, 2)
	go func() { errCh <- d.WaitForDurability(durable + 100) }()
	go func() { errCh <- d.WaitForDurabilityContext(context.Background(), durable+100) }()
	notifyCh := d.DurabilityNotify(durable + 100)
	require.Eventually(t, func() bool { return d.DurabilityStats().PendingWaiters == 2 },
		10*time.Second, time.Millisecond)

	require.NoError(t, d.Close())
	require.ErrorIs(t, <-errCh, ErrClosed)
	require.ErrorIs(t, <-errCh, ErrClosed)
	require.ErrorIs(t, <-notifyCh, ErrClosed)

	// Close takes precedence over context cancellation; already durable
	// sequence numbers still succeed.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, d.WaitForDurabilityContext(ctx, durable+1), ErrClosed)
	require.ErrorIs(t, <-d.DurabilityNotify(durable+1), ErrClosed)
	require.NoError(t, d.WaitForDurability(durable))
}

func TestDurabilityDisableWAL(t *testing.T) {
	var rec batchDurableRecorder
	d := openDurabilityTestDB(t, &Options{
		DisableWAL:    true,
		EventListener: &EventListener{BatchDurable: rec.record},
	})
	defer func() { require.NoError(t, d.Close()) }()

	require.NoError(t, d.Set([]byte("a"), []byte("1"), NoSync))
	require.Empty(t, rec.get())
	require.NoError(t, d.WaitForDurability(1000))
	require.NoError(t, d.WaitForDurabilityBatch([]base.SeqNum{1000, 2000}))
	require.NoError(t, d.WaitForJobDurability(1000))
	require.NoError(t, <-d.DurabilityNotify(1000))
	require.Zero(t, d.Metrics().DurableCommitCount)
}

func TestDurabilityNotifyLimit(t *testing.T) {
	d := openDurabilityTestDB(t, &Options{})
	defer func() { require.NoError(t, d.Close()) }()

	const far = base.SeqNum(1 << 40)
	next := d.mu.versions.logSeqNum.Load()
	first := d.DurabilityNotify(next)
	for i := 1; i < maxDurabilityNotifySubscriptions; i++ {
		_ = d.DurabilityNotify(far)
	}
	require.ErrorIs(t, <-d.DurabilityNotify(far), ErrDurabilityNotifyLimit)
	// Already durable sequence numbers do not need a subscription.
	require.NoError(t, <-d.DurabilityNotify(0))

	// Resolving a subscription frees up capacity.
	require.NoError(t, d.Set([]byte("a"), []byte("1"), Sync))
	require.NoError(t, <-first)
	ch := d.DurabilityNotify(far)
	select {
	case err := <-ch:
		t.Fatalf("unexpected notification: %v", err)
	default:
	}
	require.ErrorIs(t, <-d.DurabilityNotify(far), ErrDurabilityNotifyLimit)
}

func TestDurabilitySyncFailure(t *testing.T) {
	var failSyncs atomic.Bool
	fs := errorfs.Wrap(vfs.NewMem(), errorfs.InjectorFunc(func(op errorfs.Op) error {
		if failSyncs.Load() && strings.HasSuffix(op.Path, ".log") &&
			(op.Kind == errorfs.OpFileSync || op.Kind == errorfs.OpFileSyncData || op.Kind == errorfs.OpFileSyncTo) {
			return errorfs.ErrInjected
		}
		return nil
	}))
	var fatalCount atomic.Int32
	var rec batchDurableRecorder
	d := openDurabilityTestDB(t, &Options{
		FS:            fs,
		Logger:        &mockLogger{fatalFunc: func(string, ...interface{}) { fatalCount.Add(1) }},
		EventListener: &EventListener{BatchDurable: rec.record},
	})

	require.NoError(t, d.Set([]byte("a"), []byte("1"), Sync))
	durable, err := d.DurableState()
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() { errCh <- d.WaitForDurability(durable + 100) }()
	require.Eventually(t, func() bool { return d.DurabilityStats().PendingWaiters == 1 },
		10*time.Second, time.Millisecond)

	failSyncs.Store(true)
	// The commit error is reported through Logger.Fatalf, which the mock
	// logger swallows.
	_ = d.Set([]byte("b"), []byte("2"), Sync)
	require.Equal(t, int32(1), fatalCount.Load())

	infos := rec.get()
	require.Len(t, infos, 2)
	require.NoError(t, infos[0].Err)
	require.ErrorIs(t, infos[1].Err, errorfs.ErrInjected)

	require.ErrorIs(t, <-errCh, errorfs.ErrInjected)
	seq, err := d.DurableState()
	require.Equal(t, durable, seq)
	require.ErrorIs(t, err, errorfs.ErrInjected)
	stats := d.DurabilityStats()
	require.Equal(t, uint64(1), stats.TotalDurableCommits)
	require.Equal(t, uint64(1), stats.TotalFailedCommits)
	require.ErrorIs(t, stats.FirstErr, errorfs.ErrInjected)
	require.Equal(t, uint64(1), d.Metrics().DurableCommitCount)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, d.WaitForDurabilityContext(ctx, durable+1), errorfs.ErrInjected)
	require.ErrorIs(t, d.WaitForJobDurability(infos[1].JobID), errorfs.ErrInjected)
	require.ErrorIs(t, <-d.DurabilityNotify(durable+1), errorfs.ErrInjected)
	require.NoError(t, d.WaitForJobDurability(infos[0].JobID))
	require.NoError(t, d.WaitForDurability(durable))

	_ = d.Close()
}

func TestDurabilityConcurrentCommits(t *testing.T) {
	var rec batchDurableRecorder
	d := openDurabilityTestDB(t, &Options{
		EventListener: &EventListener{BatchDurable: rec.record},
	})
	defer func() { require.NoError(t, d.Close()) }()

	const goroutines, perGoroutine = 8, 50
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				key := []byte(fmt.Sprintf("%d-%d", g, i))
				if i%3 == 0 {
					require.NoError(t, d.Set(key, nil, NoSync))
					continue
				}
				b := d.NewBatch()
				require.NoError(t, b.Set(key, nil, nil))
				if i%2 == 0 {
					require.NoError(t, d.ApplyNoSyncWait(b, Sync))
					require.NoError(t, b.SyncWait())
				} else {
					require.NoError(t, d.Apply(b, Sync))
				}
				require.NoError(t, d.WaitForDurability(b.SeqNum()))
				require.NoError(t, b.Close())
			}
		}(g)
	}
	wg.Wait()

	infos := rec.get()
	jobIDs := make(map[int]struct{})
	for _, info := range infos {
		require.NoError(t, info.Err)
		jobIDs[info.JobID] = struct{}{}
		require.NoError(t, d.WaitForJobDurability(info.JobID))
	}
	var syncCommits int
	for i := 0; i < perGoroutine; i++ {
		if i%3 != 0 {
			syncCommits++
		}
	}
	require.Len(t, infos, goroutines*syncCommits)
	require.Len(t, jobIDs, len(infos))
	require.Equal(t, uint64(len(infos)), d.DurabilityStats().TotalDurableCommits)
	require.Equal(t, uint64(len(infos)), d.Metrics().DurableCommitCount)
}
