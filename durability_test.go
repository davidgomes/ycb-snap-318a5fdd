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

func openDurabilityDB(t *testing.T, opts *Options) *DB {
	t.Helper()
	if opts == nil {
		opts = &Options{}
	}
	if opts.FS == nil {
		opts.FS = vfs.NewMem()
	}
	opts.DisableTableStats = true
	d, err := Open("", opts)
	require.NoError(t, err)
	return d
}

func TestBatchDurableConfigured(t *testing.T) {
	defer leaktest.AfterTest(t)()

	require.False(t, batchDurableConfigured(nil))
	require.False(t, batchDurableConfigured(noopBatchDurable))
	require.True(t, batchDurableConfigured(func(BatchDurableInfo) {}))

	noopTee := TeeEventListener(EventListener{}, EventListener{})
	require.False(t, batchDurableConfigured(noopTee.BatchDurable))

	userTee := TeeEventListener(EventListener{BatchDurable: func(BatchDurableInfo) {}}, EventListener{})
	require.True(t, batchDurableConfigured(userTee.BatchDurable))
}

func TestDurabilitySyncCommit(t *testing.T) {
	defer leaktest.AfterTest(t)()

	var mu sync.Mutex
	var infos []BatchDurableInfo
	record := func(info BatchDurableInfo) {
		mu.Lock()
		infos = append(infos, info)
		mu.Unlock()
	}
	d := openDurabilityDB(t, &Options{
		EventListener: &EventListener{BatchDurable: record},
	})
	defer d.Close()

	require.Equal(t, DurabilityStats{}, d.DurabilityStats())
	state, err := d.DurableState()
	require.NoError(t, err)
	require.Equal(t, base.SeqNum(0), state)

	// Zero blocks until a Sync commit succeeds. A positive seqnum blocks until
	// that sequence number is covered.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	require.ErrorIs(t, d.WaitForDurabilityContext(ctx, 0), context.DeadlineExceeded)
	require.ErrorIs(t, d.WaitForDurabilityContext(ctx, base.SeqNumStart), context.DeadlineExceeded)
	require.ErrorIs(t, d.WaitForDurabilityBatchContext(ctx, []base.SeqNum{base.SeqNumStart}), context.DeadlineExceeded)
	cancel()
	require.Equal(t, int64(0), d.DurabilityStats().PendingWaiters)

	// Nil and empty batches return immediately, including when the context is
	// already cancelled.
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, d.WaitForDurabilityBatchContext(cancelCtx, nil))
	require.NoError(t, d.WaitForDurabilityBatchContext(cancelCtx, []base.SeqNum{}))

	require.NoError(t, d.Set([]byte("a"), []byte("1"), NoSync))
	mu.Lock()
	require.Empty(t, infos)
	mu.Unlock()
	state, err = d.DurableState()
	require.NoError(t, err)
	require.Equal(t, base.SeqNum(0), state)
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	require.ErrorIs(t, d.WaitForDurabilityContext(ctx, 0), context.DeadlineExceeded)
	require.ErrorIs(t, d.WaitForDurabilityContext(ctx, base.SeqNumStart), context.DeadlineExceeded)
	cancel()

	require.NoError(t, d.Set([]byte("b"), []byte("2"), &WriteOptions{Sync: true, CommitCorrelationID: 7}))
	mu.Lock()
	require.Len(t, infos, 1)
	info := infos[0]
	mu.Unlock()
	require.NoError(t, info.Err)
	require.Equal(t, base.SeqNumStart+1, info.SeqNum)
	require.EqualValues(t, 1, info.KeyCount)
	require.EqualValues(t, 7, info.CorrelationID)
	require.Positive(t, info.JobID)
	require.Positive(t, info.ApplyDuration)
	require.Positive(t, info.SyncDuration)
	require.Positive(t, info.BatchSize)

	// The earlier non-sync key is durable once a later Sync commit finishes,
	// because WAL order matches sequence-number order.
	require.NoError(t, d.WaitForDurability(0))
	require.NoError(t, d.WaitForDurability(base.SeqNumStart))
	require.NoError(t, d.WaitForDurability(info.SeqNum))
	require.NoError(t, d.WaitForDurabilityBatch([]base.SeqNum{0, base.SeqNumStart, info.SeqNum}))
	state, err = d.DurableState()
	require.NoError(t, err)
	require.Equal(t, info.SeqNum, state)

	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	require.ErrorIs(t, d.WaitForDurabilityContext(ctx, info.SeqNum+1), context.DeadlineExceeded)
	cancel()

	st := d.DurabilityStats()
	require.Equal(t, info.SeqNum, st.HighestDurableSeqNum)
	require.NoError(t, st.FirstErr)
	require.EqualValues(t, 0, st.PendingWaiters)
	require.EqualValues(t, 1, st.TotalDurableCommits)
	require.EqualValues(t, 0, st.TotalFailedCommits)
	require.Equal(t, info.SyncDuration, st.CumulativeSyncDuration)
	require.Equal(t, info.SyncDuration, st.MaxSyncDuration)

	m := d.Metrics()
	require.EqualValues(t, 1, m.DurableCommitCount)
	require.Equal(t, info.SyncDuration, m.DurableCommitDuration)

	ch := d.DurabilityNotify(info.SeqNum)
	select {
	case err := <-ch:
		require.NoError(t, err)
	default:
		t.Fatal("expected a pre-filled notify channel")
	}
	ch = d.DurabilityNotify(0)
	select {
	case err := <-ch:
		require.NoError(t, err)
	default:
		t.Fatal("expected a pre-filled notify channel for seqnum 0")
	}

	require.NoError(t, d.WaitForJobDurability(info.JobID))
	err = d.WaitForJobDurability(0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown")
	err = d.WaitForJobDurability(info.JobID + 10)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown")

	// Already-durable results beat a cancelled context. A nil context is treated
	// as background.
	require.NoError(t, d.WaitForDurabilityContext(cancelCtx, info.SeqNum))
	require.NoError(t, d.WaitForJobDurabilityContext(cancelCtx, info.JobID))
	require.NoError(t, d.WaitForDurabilityContext(nil, info.SeqNum))
}

func TestDurabilityMultiKeyAndOrdering(t *testing.T) {
	defer leaktest.AfterTest(t)()

	release := make(chan struct{})
	started := make(chan BatchDurableInfo, 1)
	var calls atomic.Int32
	d := openDurabilityDB(t, &Options{
		EventListener: &EventListener{BatchDurable: func(info BatchDurableInfo) {
			calls.Add(1)
			select {
			case started <- info:
			default:
			}
			<-release
		}},
	})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		d.Close()
	}()

	b := d.NewBatch()
	require.NoError(t, b.Set([]byte("a"), []byte("va"), nil))
	require.NoError(t, b.Set([]byte("b"), []byte("vb"), nil))
	commitDone := make(chan error, 1)
	go func() {
		commitDone <- b.Commit(&WriteOptions{Sync: true, CommitCorrelationID: 42})
	}()
	var info BatchDurableInfo
	select {
	case info = <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("BatchDurable did not run")
	}
	select {
	case err := <-commitDone:
		t.Fatalf("Commit returned before BatchDurable finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-commitDone)
	require.EqualValues(t, 1, calls.Load())

	require.NoError(t, info.Err)
	require.EqualValues(t, 2, info.KeyCount)
	require.EqualValues(t, 42, info.CorrelationID)
	require.Equal(t, len(b.Repr()), info.BatchSize)
	require.Positive(t, info.ApplyDuration)
	require.Positive(t, info.SyncDuration)

	state, err := d.DurableState()
	require.NoError(t, err)
	require.Equal(t, info.SeqNum+base.SeqNum(info.KeyCount)-1, state)
	require.NoError(t, d.WaitForDurability(state))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, d.WaitForDurabilityContext(ctx, state+1), context.DeadlineExceeded)
	require.NoError(t, b.Close())

	// ApplyNoSyncWait can return before the callback finishes. SyncWait cannot.
	release2 := make(chan struct{})
	started2 := make(chan struct{}, 1)
	d.opts.EventListener.BatchDurable = func(BatchDurableInfo) {
		calls.Add(1)
		select {
		case started2 <- struct{}{}:
		default:
		}
		<-release2
	}
	b2 := d.NewBatch()
	require.NoError(t, b2.Set([]byte("c"), []byte("vc"), nil))
	require.NoError(t, d.ApplyNoSyncWait(b2, &WriteOptions{Sync: true, CommitCorrelationID: 9}))
	select {
	case <-started2:
	case <-time.After(5 * time.Second):
		t.Fatal("BatchDurable did not run for ApplyNoSyncWait")
	}
	syncDone := make(chan error, 1)
	go func() { syncDone <- b2.SyncWait() }()
	select {
	case err := <-syncDone:
		t.Fatalf("SyncWait returned before BatchDurable finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release2)
	require.NoError(t, <-syncDone)
	require.NoError(t, b2.Close())
	require.EqualValues(t, 2, calls.Load())

	// LogData syncs the WAL without advancing the highest key sequence number.
	before, err := d.DurableState()
	require.NoError(t, err)
	lb := d.NewBatch()
	require.NoError(t, lb.LogData([]byte("marker"), nil))
	require.NoError(t, lb.Commit(Sync))
	require.NoError(t, lb.Close())
	after, err := d.DurableState()
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.NoError(t, d.WaitForDurability(0))
	require.EqualValues(t, 3, calls.Load())
	require.EqualValues(t, 3, d.DurabilityStats().TotalDurableCommits)
}

func TestDurabilityWaitersAndNotify(t *testing.T) {
	defer leaktest.AfterTest(t)()

	d := openDurabilityDB(t, &Options{
		EventListener: &EventListener{BatchDurable: func(BatchDurableInfo) {}},
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.WaitForDurability(base.SeqNumStart)
	}()
	require.Eventually(t, func() bool {
		return d.DurabilityStats().PendingWaiters == 1
	}, 2*time.Second, time.Millisecond)

	notifyCh := d.DurabilityNotify(base.SeqNumStart)
	select {
	case <-notifyCh:
		t.Fatal("notify delivered before the commit")
	default:
	}

	batchCh := make(chan error, 1)
	go func() {
		batchCh <- d.WaitForDurabilityBatch([]base.SeqNum{base.SeqNumStart, base.SeqNumStart})
	}()
	require.Eventually(t, func() bool {
		return d.DurabilityStats().PendingWaiters == 2
	}, 2*time.Second, time.Millisecond)

	require.NoError(t, d.Set([]byte("a"), []byte("1"), Sync))
	require.NoError(t, <-errCh)
	require.NoError(t, <-batchCh)
	select {
	case err := <-notifyCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("notify was not delivered")
	}
	require.Equal(t, int64(0), d.DurabilityStats().PendingWaiters)

	// Subscriptions that are already satisfied do not consume the outstanding
	// notify budget.
	for i := 0; i < durabilityNotifyLimit+5; i++ {
		ch := d.DurabilityNotify(base.SeqNumStart)
		select {
		case err := <-ch:
			require.NoError(t, err)
		default:
			t.Fatal("expected a pre-filled notify channel")
		}
	}

	var blocked []<-chan error
	pendingSeq := base.SeqNumStart + 1000
	for i := 0; i < durabilityNotifyLimit; i++ {
		blocked = append(blocked, d.DurabilityNotify(pendingSeq))
	}
	extra := d.DurabilityNotify(pendingSeq)
	select {
	case err := <-extra:
		require.Error(t, err)
	default:
		t.Fatal("expected a pre-filled error once the notify limit is exceeded")
	}
	// The rejected subscription did not occupy a slot.
	d.durability.mu.Lock()
	outstanding := d.durability.outstandingNotifies
	d.durability.mu.Unlock()
	require.Equal(t, durabilityNotifyLimit, outstanding)

	require.NoError(t, d.Close())
	for i, ch := range blocked {
		select {
		case err := <-ch:
			require.ErrorIs(t, err, ErrClosed, "notify %d", i)
		case <-time.After(5 * time.Second):
			t.Fatalf("notify %d was not closed", i)
		}
	}
}

func TestDurabilityClose(t *testing.T) {
	defer leaktest.AfterTest(t)()

	d := openDurabilityDB(t, nil)
	require.NoError(t, d.Set([]byte("a"), []byte("1"), Sync))
	state, err := d.DurableState()
	require.NoError(t, err)
	require.Equal(t, base.SeqNumStart, state)

	waitErr := make(chan error, 2)
	go func() { waitErr <- d.WaitForDurability(state + 10) }()
	go func() { waitErr <- d.WaitForDurabilityBatch([]base.SeqNum{state, state + 10}) }()
	require.Eventually(t, func() bool {
		return d.DurabilityStats().PendingWaiters == 2
	}, 2*time.Second, time.Millisecond)
	jobErr := make(chan error, 1)
	go func() { jobErr <- d.WaitForJobDurability(1_000_000) }()

	notifyCh := d.DurabilityNotify(state + 10)
	require.NoError(t, d.Close())

	require.ErrorIs(t, <-waitErr, ErrClosed)
	require.ErrorIs(t, <-waitErr, ErrClosed)
	// Never-issued job IDs are unknown even after close.
	err = <-jobErr
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown")
	require.NotContains(t, err.Error(), "expired")
	select {
	case err := <-notifyCh:
		require.ErrorIs(t, err, ErrClosed)
	case <-time.After(5 * time.Second):
		t.Fatal("notify was not delivered on close")
	}

	// A new wait for an already-durable seqnum succeeds. A cancelled context
	// does not override a close error for a seqnum that is not durable.
	require.NoError(t, d.WaitForDurability(state))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = d.WaitForDurabilityContext(ctx, state)
	require.NoError(t, err)
	err = d.WaitForDurabilityContext(ctx, state+10)
	require.ErrorIs(t, err, ErrClosed)
	require.False(t, errors.Is(err, context.Canceled))
	err = d.WaitForDurabilityBatchContext(ctx, []base.SeqNum{state, state + 10})
	require.ErrorIs(t, err, ErrClosed)

	seq, serr := d.DurableState()
	require.NoError(t, serr)
	require.Equal(t, state, seq)
	require.Equal(t, int64(0), d.DurabilityStats().PendingWaiters)
}

func TestDurabilityJobExpiry(t *testing.T) {
	defer leaktest.AfterTest(t)()

	var mu sync.Mutex
	var jobs []int
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblockSync := func() { releaseOnce.Do(func() { close(release) }) }
	var blockSync atomic.Bool
	fs := errorfs.Wrap(vfs.NewMem(), errorfs.InjectorFunc(func(op errorfs.Op) error {
		if blockSync.Load() &&
			(op.Kind == errorfs.OpFileSync || op.Kind == errorfs.OpFileSyncData) &&
			strings.HasSuffix(op.Path, ".log") {
			<-release
		}
		return nil
	}))
	d := openDurabilityDB(t, &Options{
		FS: fs,
		EventListener: &EventListener{BatchDurable: func(info BatchDurableInfo) {
			mu.Lock()
			jobs = append(jobs, info.JobID)
			mu.Unlock()
		}},
		MemTableSize: 64 << 20,
	})
	defer func() {
		unblockSync()
		d.Close()
	}()

	const n = durabilityJobRetain + 1
	for i := 0; i < n; i++ {
		require.NoError(t, d.Set([]byte{byte(i >> 8), byte(i)}, []byte("v"), Sync))
	}
	mu.Lock()
	require.Len(t, jobs, n)
	first, last := jobs[0], jobs[n-1]
	mu.Unlock()

	err := d.WaitForJobDurability(first)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expired")
	require.NotContains(t, err.Error(), "unknown")
	require.NoError(t, d.WaitForJobDurability(last))
	mu.Lock()
	second := jobs[1]
	mu.Unlock()
	require.NoError(t, d.WaitForJobDurability(second))

	err = d.WaitForJobDurability(last + 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown")

	// Hold the WAL sync so the job stays in flight until the sync is released.
	blockSync.Store(true)
	commitDone := make(chan error, 1)
	go func() {
		commitDone <- d.Set([]byte("z"), []byte("z"), Sync)
	}()
	var inflight int
	require.Eventually(t, func() bool {
		d.durability.mu.Lock()
		defer d.durability.mu.Unlock()
		job := d.durability.jobs[d.durability.nextJobID]
		if job == nil || job.done {
			return false
		}
		inflight = d.durability.nextJobID
		return true
	}, 5*time.Second, time.Millisecond)

	jobDone := make(chan error, 1)
	go func() { jobDone <- d.WaitForJobDurability(inflight) }()
	require.Eventually(t, func() bool {
		return d.DurabilityStats().PendingWaiters == 1
	}, 2*time.Second, time.Millisecond)
	select {
	case err := <-jobDone:
		t.Fatalf("job wait returned before the sync finished: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	unblockSync()
	require.NoError(t, <-commitDone)
	require.NoError(t, <-jobDone)
}

func TestDurabilityDisableWAL(t *testing.T) {
	defer leaktest.AfterTest(t)()

	var calls atomic.Int32
	d := openDurabilityDB(t, &Options{
		DisableWAL: true,
		EventListener: &EventListener{BatchDurable: func(BatchDurableInfo) {
			calls.Add(1)
		}},
	})
	defer d.Close()

	err := d.Set([]byte("a"), []byte("1"), Sync)
	require.Error(t, err)
	require.Contains(t, err.Error(), "WAL disabled")
	require.NoError(t, d.Set([]byte("a"), []byte("1"), NoSync))
	require.EqualValues(t, 0, calls.Load())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, d.WaitForDurabilityContext(ctx, 0))
	require.NoError(t, d.WaitForDurabilityContext(ctx, 1<<40))
	require.NoError(t, d.WaitForDurabilityBatchContext(ctx, []base.SeqNum{1, 2, 3}))
	require.NoError(t, d.WaitForJobDurabilityContext(ctx, 0))
	require.NoError(t, d.WaitForJobDurability(999))

	state, err := d.DurableState()
	require.NoError(t, err)
	require.Equal(t, base.SeqNum(0), state)
	require.Equal(t, DurabilityStats{}, d.DurabilityStats())

	ch := d.DurabilityNotify(1 << 40)
	select {
	case err := <-ch:
		require.NoError(t, err)
	default:
		t.Fatal("expected a pre-filled nil notify channel when the WAL is disabled")
	}
	m := d.Metrics()
	require.Zero(t, m.DurableCommitCount)
	require.Zero(t, m.DurableCommitDuration)
}

func TestDurabilitySyncError(t *testing.T) {
	defer leaktest.AfterTest(t)()

	injected := errors.New("injected wal sync failure")
	var fail atomic.Bool
	fs := errorfs.Wrap(vfs.NewMem(), errorfs.InjectorFunc(func(op errorfs.Op) error {
		if !fail.Load() {
			return nil
		}
		if (op.Kind == errorfs.OpFileSync || op.Kind == errorfs.OpFileSyncData) &&
			strings.HasSuffix(op.Path, ".log") {
			return injected
		}
		return nil
	}))

	var mu sync.Mutex
	var infos []BatchDurableInfo
	d := openDurabilityDB(t, &Options{
		FS:     fs,
		Logger: &base.InMemLogger{},
		EventListener: &EventListener{BatchDurable: func(info BatchDurableInfo) {
			mu.Lock()
			infos = append(infos, info)
			mu.Unlock()
		}},
	})

	require.NoError(t, d.Set([]byte("a"), []byte("1"), Sync))
	mu.Lock()
	require.Len(t, infos, 1)
	require.NoError(t, infos[0].Err)
	first := infos[0]
	mu.Unlock()

	fail.Store(true)
	// applyInternal reports a sync failure through Logger.Fatalf. InMemLogger
	// records it and returns, so the process keeps running and the callback is
	// observable.
	require.NoError(t, d.Set([]byte("b"), []byte("2"), Sync))
	mu.Lock()
	require.Len(t, infos, 2)
	failed := infos[1]
	mu.Unlock()
	require.ErrorIs(t, failed.Err, injected)
	require.Positive(t, failed.JobID)
	require.NotEqual(t, first.JobID, failed.JobID)

	state, err := d.DurableState()
	require.ErrorIs(t, err, injected)
	require.Equal(t, first.SeqNum, state)
	require.NoError(t, d.WaitForDurability(first.SeqNum))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = d.WaitForDurabilityContext(ctx, first.SeqNum+1)
	require.ErrorIs(t, err, injected)
	require.False(t, errors.Is(err, context.Canceled))
	err = d.WaitForJobDurabilityContext(ctx, failed.JobID)
	require.ErrorIs(t, err, injected)
	require.False(t, errors.Is(err, context.Canceled))

	st := d.DurabilityStats()
	require.Equal(t, first.SeqNum, st.HighestDurableSeqNum)
	require.ErrorIs(t, st.FirstErr, injected)
	require.EqualValues(t, 1, st.TotalDurableCommits)
	require.EqualValues(t, 1, st.TotalFailedCommits)
	require.Equal(t, first.SyncDuration, st.CumulativeSyncDuration)
	require.Equal(t, first.SyncDuration, st.MaxSyncDuration)

	m := d.Metrics()
	require.EqualValues(t, 1, m.DurableCommitCount)
	require.Equal(t, first.SyncDuration, m.DurableCommitDuration)

	ch := d.DurabilityNotify(first.SeqNum + 1)
	select {
	case err := <-ch:
		require.ErrorIs(t, err, injected)
	default:
		t.Fatal("expected a pre-filled notify channel after a sync failure")
	}

	// The latched sync error makes Close return an error as well.
	require.Error(t, d.Close())
}

func TestDurabilityMetricsRequireCallback(t *testing.T) {
	defer leaktest.AfterTest(t)()

	d := openDurabilityDB(t, nil)
	require.NoError(t, d.Set([]byte("a"), []byte("1"), Sync))
	require.NoError(t, d.Set([]byte("b"), []byte("2"), NoSync))
	require.NoError(t, d.Set([]byte("c"), []byte("3"), Sync))
	m := d.Metrics()
	require.Zero(t, m.DurableCommitCount)
	require.Zero(t, m.DurableCommitDuration)
	st := d.DurabilityStats()
	require.EqualValues(t, 2, st.TotalDurableCommits)
	require.Positive(t, st.CumulativeSyncDuration)
	require.GreaterOrEqual(t, st.MaxSyncDuration, time.Duration(0))
	require.LessOrEqual(t, st.MaxSyncDuration, st.CumulativeSyncDuration)
	require.NoError(t, d.Close())

	var mu sync.Mutex
	var syncSum time.Duration
	var n atomic.Int32
	l1 := EventListener{BatchDurable: func(info BatchDurableInfo) {
		n.Add(1)
		mu.Lock()
		syncSum += info.SyncDuration
		mu.Unlock()
	}}
	l2 := EventListener{BatchDurable: func(BatchDurableInfo) { n.Add(1) }}
	tee := TeeEventListener(l1, l2)
	d2 := openDurabilityDB(t, &Options{EventListener: &tee})
	require.NoError(t, d2.Set([]byte("a"), []byte("1"), Sync))
	require.NoError(t, d2.Set([]byte("b"), []byte("2"), NoSync))
	require.NoError(t, d2.Set([]byte("c"), []byte("3"), Sync))
	require.EqualValues(t, 4, n.Load())
	m = d2.Metrics()
	mu.Lock()
	sum := syncSum
	mu.Unlock()
	require.EqualValues(t, 2, m.DurableCommitCount)
	require.Equal(t, sum, m.DurableCommitDuration)
	require.Equal(t, sum, d2.DurabilityStats().CumulativeSyncDuration)
	require.Positive(t, m.DurableCommitDuration)
	require.NoError(t, d2.Close())
}

func TestDurabilityPendingWaiterCancel(t *testing.T) {
	defer leaktest.AfterTest(t)()

	d := openDurabilityDB(t, nil)
	defer d.Close()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.WaitForDurabilityContext(ctx, base.SeqNumStart)
	}()
	require.Eventually(t, func() bool {
		return d.DurabilityStats().PendingWaiters == 1
	}, 2*time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-errCh, context.Canceled)
	require.Equal(t, int64(0), d.DurabilityStats().PendingWaiters)
}
