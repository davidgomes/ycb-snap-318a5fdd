// Copyright 2026 The LevelDB-Go and Pebble Authors. All rights reserved. Use
// of this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package pebble

import (
	"context"
	"fmt"
	"slices"
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

type batchDurableRecorder struct {
	mu    sync.Mutex
	infos []BatchDurableInfo
}

func (r *batchDurableRecorder) record(info BatchDurableInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.infos = append(r.infos, info)
}

func (r *batchDurableRecorder) take() []BatchDurableInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	infos := r.infos
	r.infos = nil
	return infos
}

// farSeqNum is a sequence number that no test commits reach.
const farSeqNum = base.SeqNumMax

func requireBlocked(t *testing.T, ch <-chan error) {
	t.Helper()
	select {
	case err := <-ch:
		t.Fatalf("unexpectedly resolved with %v", err)
	default:
	}
}

func requireResolved(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	default:
		t.Fatal("unexpectedly unresolved")
		return nil
	}
}

func waitForPendingWaiters(t *testing.T, d *DB, n int64) {
	t.Helper()
	require.Eventually(t, func() bool {
		return d.DurabilityStats().PendingWaiters == n
	}, 10*time.Second, time.Millisecond)
}

func TestBatchDurable(t *testing.T) {
	defer leaktest.AfterTest(t)()
	var rec batchDurableRecorder
	d, err := Open("", &Options{
		FS:            vfs.NewMem(),
		EventListener: &EventListener{BatchDurable: rec.record},
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, d.Close()) }()

	require.Equal(t, DurabilityStats{}, d.DurabilityStats())

	// Commits that don't sync the WAL, and empty batches, are not reported.
	noSync := d.NewBatch()
	require.NoError(t, noSync.Set([]byte("a"), []byte("1"), nil))
	require.NoError(t, noSync.Commit(NoSync))
	empty := d.NewBatch()
	require.NoError(t, empty.Commit(Sync))
	require.NoError(t, empty.Close())
	require.Empty(t, rec.take())
	seqNum, err := d.DurableState()
	require.NoError(t, err)
	require.Zero(t, seqNum)

	b := d.NewBatch()
	for _, k := range []string{"b", "c", "d"} {
		require.NoError(t, b.Set([]byte(k), []byte(k), nil))
	}
	require.NoError(t, b.Commit(&WriteOptions{Sync: true, CommitCorrelationID: 42}))
	infos := rec.take()
	require.Len(t, infos, 1)
	info := infos[0]
	require.NoError(t, info.Err)
	require.Equal(t, 1, info.JobID)
	require.Equal(t, b.SeqNum(), info.SeqNum)
	require.Equal(t, uint32(3), info.KeyCount)
	require.Equal(t, len(b.Repr()), info.BatchSize)
	require.Equal(t, uint64(42), info.CorrelationID)
	require.Positive(t, info.ApplyDuration)
	require.Positive(t, info.SyncDuration)
	require.Less(t, info.SyncDuration, b.CommitStats().TotalDuration)

	// All of the batch's sequence numbers are durable, and so is the earlier
	// commit that didn't request a sync.
	last := b.SeqNum() + 2
	seqNum, err = d.DurableState()
	require.NoError(t, err)
	require.Equal(t, last, seqNum)
	require.NoError(t, d.WaitForDurability(0))
	require.NoError(t, d.WaitForDurability(noSync.SeqNum()))
	require.NoError(t, d.WaitForDurability(last))
	require.NoError(t, d.WaitForDurabilityBatch(nil))
	require.NoError(t, d.WaitForDurabilityBatch([]base.SeqNum{last, noSync.SeqNum(), b.SeqNum()}))
	require.NoError(t, requireResolved(t, d.DurabilityNotify(last)))
	require.NoError(t, d.WaitForJobDurability(info.JobID))
	require.NoError(t, noSync.Close())

	m := d.Metrics()
	require.Equal(t, uint64(1), m.DurableCommitCount)
	require.Equal(t, info.SyncDuration, m.DurableCommitDuration)
	require.Equal(t, DurabilityStats{
		HighestDurableSeqNum:   last,
		TotalDurableCommits:    1,
		CumulativeSyncDuration: info.SyncDuration,
		MaxSyncDuration:        info.SyncDuration,
	}, d.DurabilityStats())
	require.NoError(t, b.Close())

	// ApplyNoSyncWait commits are reported by Batch.SyncWait.
	b = d.NewBatch()
	require.NoError(t, b.Set([]byte("e"), nil, nil))
	require.NoError(t, d.ApplyNoSyncWait(b, &WriteOptions{Sync: true, CommitCorrelationID: 7}))
	require.NoError(t, b.SyncWait())
	infos = rec.take()
	require.Len(t, infos, 1)
	info = infos[0]
	require.NoError(t, info.Err)
	require.Equal(t, 2, info.JobID)
	require.Equal(t, b.SeqNum(), info.SeqNum)
	require.Equal(t, uint32(1), info.KeyCount)
	require.Equal(t, uint64(7), info.CorrelationID)
	require.Positive(t, info.ApplyDuration)
	require.Positive(t, info.SyncDuration)
	require.NoError(t, d.WaitForDurability(b.SeqNum()))
	require.NoError(t, b.Close())

	// A batch with only LogData consumes no sequence numbers.
	next := d.mu.versions.logSeqNum.Load()
	require.NoError(t, d.LogData([]byte("data"), Sync))
	infos = rec.take()
	require.Len(t, infos, 1)
	require.NoError(t, infos[0].Err)
	require.Equal(t, next, infos[0].SeqNum)
	require.Zero(t, infos[0].KeyCount)
	require.Positive(t, infos[0].BatchSize)
	seqNum, err = d.DurableState()
	require.NoError(t, err)
	require.Equal(t, next-1, seqNum)

	m = d.Metrics()
	require.Equal(t, uint64(3), m.DurableCommitCount)
	stats := d.DurabilityStats()
	require.Equal(t, uint64(3), stats.TotalDurableCommits)
	require.Equal(t, stats.CumulativeSyncDuration, m.DurableCommitDuration)
	require.LessOrEqual(t, stats.MaxSyncDuration, stats.CumulativeSyncDuration)
}

func TestBatchDurableCallbackQueriesDB(t *testing.T) {
	defer leaktest.AfterTest(t)()
	var d *DB
	var callbackErrs []error
	d, err := Open("", &Options{
		FS: vfs.NewMem(),
		EventListener: &EventListener{BatchDurable: func(info BatchDurableInfo) {
			// The outcome is recorded before the callback is invoked.
			callbackErrs = append(callbackErrs,
				d.WaitForJobDurability(info.JobID),
				d.WaitForDurability(info.SeqNum),
				requireResolved(t, d.DurabilityNotify(info.SeqNum)))
		}},
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, d.Close()) }()

	require.NoError(t, d.Set([]byte("a"), nil, Sync))
	require.Equal(t, []error{nil, nil, nil}, callbackErrs)
}

func TestBatchDurableLargeBatch(t *testing.T) {
	defer leaktest.AfterTest(t)()
	var rec batchDurableRecorder
	d, err := Open("", &Options{
		FS:            vfs.NewMem(),
		MemTableSize:  1 << 20,
		EventListener: &EventListener{BatchDurable: rec.record},
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, d.Close()) }()

	// A batch larger than half the memtable is committed as a flushable batch,
	// whose contents are cleared once committed.
	b := d.NewBatch()
	value := make([]byte, 1<<10)
	for i := 0; i < 1<<10; i++ {
		require.NoError(t, b.Set([]byte(fmt.Sprintf("k%04d", i)), value, nil))
	}
	size := b.Len()
	require.NoError(t, b.Commit(Sync))
	infos := rec.take()
	require.Len(t, infos, 1)
	require.NoError(t, infos[0].Err)
	require.Equal(t, size, infos[0].BatchSize)
	require.Equal(t, uint32(1<<10), infos[0].KeyCount)
	require.Positive(t, infos[0].ApplyDuration)
	require.Positive(t, infos[0].SyncDuration)
	require.NoError(t, d.WaitForDurability(infos[0].SeqNum+1<<10-1))
	require.NoError(t, b.Close())
}

func TestBatchDurableNotConfigured(t *testing.T) {
	defer leaktest.AfterTest(t)()
	tee := TeeEventListener(EventListener{}, EventListener{})
	for _, tc := range []struct {
		name     string
		listener *EventListener
	}{
		{name: "nil", listener: nil},
		{name: "unset", listener: &EventListener{}},
		{name: "tee", listener: &tee},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := vfs.NewMem()
			// Reopening exercises a listener populated by EnsureDefaults during the
			// first Open.
			for i := 0; i < 2; i++ {
				d, err := Open("", &Options{FS: fs, EventListener: tc.listener})
				require.NoError(t, err)
				b := d.NewBatch()
				require.NoError(t, b.Set([]byte("a"), nil, nil))
				require.NoError(t, b.Commit(Sync))
				require.NoError(t, d.WaitForDurability(b.SeqNum()))
				require.NoError(t, d.WaitForJobDurability(1))
				require.Equal(t, uint64(1), d.DurabilityStats().TotalDurableCommits)
				m := d.Metrics()
				require.Zero(t, m.DurableCommitCount)
				require.Zero(t, m.DurableCommitDuration)
				require.NoError(t, b.Close())
				require.NoError(t, d.Close())
			}
		})
	}
}

func TestTeeEventListenerBatchDurable(t *testing.T) {
	defer leaktest.AfterTest(t)()
	require.False(t, isBatchDurableConfigured(TeeEventListener(EventListener{}, EventListener{}).BatchDurable))

	var a, b batchDurableRecorder
	for _, tc := range []struct {
		name     string
		listener EventListener
		want     int
	}{
		{name: "both", listener: TeeEventListener(
			EventListener{BatchDurable: a.record}, EventListener{BatchDurable: b.record}), want: 2},
		{name: "first", listener: TeeEventListener(EventListener{BatchDurable: a.record}, EventListener{}), want: 1},
		{name: "second", listener: TeeEventListener(EventListener{}, EventListener{BatchDurable: b.record}), want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, isBatchDurableConfigured(tc.listener.BatchDurable))
			d, err := Open("", &Options{FS: vfs.NewMem(), EventListener: &tc.listener})
			require.NoError(t, err)
			require.NoError(t, d.Set([]byte("a"), nil, &WriteOptions{Sync: true, CommitCorrelationID: 9}))
			infos := append(a.take(), b.take()...)
			require.Len(t, infos, tc.want)
			for _, info := range infos {
				require.Equal(t, uint64(9), info.CorrelationID)
			}
			require.Equal(t, uint64(1), d.Metrics().DurableCommitCount)
			require.NoError(t, d.Close())
		})
	}
}

func TestDurabilityWaiters(t *testing.T) {
	defer leaktest.AfterTest(t)()
	d, err := Open("", &Options{FS: vfs.NewMem()})
	require.NoError(t, err)

	// A commit that doesn't sync the WAL becomes durable with the next commit
	// that does.
	b := d.NewBatch()
	require.NoError(t, b.Set([]byte("a"), nil, nil))
	require.NoError(t, b.Commit(NoSync))
	seqNum := b.SeqNum()
	require.NoError(t, b.Close())

	waitErr := make(chan error, 2)
	go func() { waitErr <- d.WaitForDurability(seqNum) }()
	go func() { waitErr <- d.WaitForDurabilityBatch([]base.SeqNum{seqNum - 1, seqNum}) }()
	notify := d.DurabilityNotify(seqNum)
	waitForPendingWaiters(t, d, 2)
	requireBlocked(t, waitErr)
	requireBlocked(t, notify)

	require.NoError(t, d.Set([]byte("b"), nil, Sync))
	require.NoError(t, <-waitErr)
	require.NoError(t, <-waitErr)
	require.NoError(t, <-notify)
	require.Zero(t, d.DurabilityStats().PendingWaiters)

	// Outcomes that are already known take precedence over the context.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, d.WaitForDurabilityContext(canceled, seqNum))
	require.NoError(t, d.WaitForDurabilityBatchContext(canceled, nil))
	require.ErrorIs(t, d.WaitForDurabilityContext(canceled, farSeqNum), context.Canceled)
	require.ErrorIs(t, d.WaitForDurabilityBatchContext(canceled, []base.SeqNum{seqNum, farSeqNum}), context.Canceled)

	// A blocked waiter returns once its context is done, and is unregistered.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, d.WaitForDurabilityContext(ctx, farSeqNum), context.DeadlineExceeded)
	require.Zero(t, d.DurabilityStats().PendingWaiters)
	d.durability.mu.Lock()
	require.Empty(t, d.durability.mu.waiters)
	d.durability.mu.Unlock()

	// Closing the DB unblocks waiters and subscriptions with an error.
	go func() { waitErr <- d.WaitForDurability(farSeqNum) }()
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	go func() { waitErr <- d.WaitForDurabilityContext(ctx, farSeqNum) }()
	notify = d.DurabilityNotify(farSeqNum)
	waitForPendingWaiters(t, d, 2)
	require.NoError(t, d.Close())
	require.ErrorIs(t, <-waitErr, ErrClosed)
	require.ErrorIs(t, <-waitErr, ErrClosed)
	require.ErrorIs(t, <-notify, ErrClosed)
	require.Zero(t, d.DurabilityStats().PendingWaiters)

	// After Close, durable sequence numbers remain durable, and the close error
	// takes precedence over the context.
	require.NoError(t, d.WaitForDurability(seqNum))
	require.ErrorIs(t, d.WaitForDurability(farSeqNum), ErrClosed)
	require.ErrorIs(t, d.WaitForDurabilityContext(canceled, farSeqNum), ErrClosed)
	require.ErrorIs(t, requireResolved(t, d.DurabilityNotify(farSeqNum)), ErrClosed)
	durable, err := d.DurableState()
	require.NoError(t, err)
	require.Less(t, seqNum, durable)
}

func TestDurabilityNotifyBound(t *testing.T) {
	defer leaktest.AfterTest(t)()
	d, err := Open("", &Options{FS: vfs.NewMem()})
	require.NoError(t, err)

	next := d.mu.versions.logSeqNum.Load()
	subs := []<-chan error{d.DurabilityNotify(next)}
	for len(subs) < maxDurabilitySubscriptions {
		subs = append(subs, d.DurabilityNotify(farSeqNum))
	}
	for _, ch := range subs {
		requireBlocked(t, ch)
	}
	require.ErrorIs(t, requireResolved(t, d.DurabilityNotify(farSeqNum)), errTooManyDurabilitySubscriptions)
	// Waiters are not subject to the bound.
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	require.ErrorIs(t, d.WaitForDurabilityContext(ctx, farSeqNum), context.DeadlineExceeded)

	// Resolving a subscription releases its slot.
	require.NoError(t, d.Set([]byte("a"), nil, Sync))
	require.NoError(t, requireResolved(t, subs[0]))
	subs[0] = d.DurabilityNotify(farSeqNum)
	requireBlocked(t, subs[0])
	require.ErrorIs(t, requireResolved(t, d.DurabilityNotify(farSeqNum)), errTooManyDurabilitySubscriptions)

	require.NoError(t, d.Close())
	for _, ch := range subs {
		require.ErrorIs(t, requireResolved(t, ch), ErrClosed)
	}
}

func TestWaitForJobDurability(t *testing.T) {
	defer leaktest.AfterTest(t)()
	var rec batchDurableRecorder
	d, err := Open("", &Options{
		FS:            vfs.NewMem(),
		EventListener: &EventListener{BatchDurable: rec.record},
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, d.Close()) }()

	requireUnknown := func(jobID int) {
		t.Helper()
		err := d.WaitForJobDurability(jobID)
		require.ErrorIs(t, err, ErrDurabilityJobUnknown)
		require.ErrorContains(t, err, "unknown")
	}
	requireUnknown(0)
	requireUnknown(1)

	const n = durableJobRetention + 1
	for i := 0; i < n; i++ {
		require.NoError(t, d.Set([]byte("a"), nil, Sync))
	}
	infos := rec.take()
	require.Len(t, infos, n)
	for i, info := range infos {
		require.Equal(t, i+1, info.JobID)
	}

	err = d.WaitForJobDurability(1)
	require.ErrorIs(t, err, ErrDurabilityJobExpired)
	require.ErrorContains(t, err, "expired")
	require.NoError(t, d.WaitForJobDurability(2))
	require.NoError(t, d.WaitForJobDurability(n))
	requireUnknown(0)
	requireUnknown(-1)
	requireUnknown(n + 1)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, d.WaitForJobDurabilityContext(canceled, n))
	require.ErrorIs(t, d.WaitForJobDurabilityContext(canceled, 1), ErrDurabilityJobExpired)
}

func TestDurabilityDisableWAL(t *testing.T) {
	defer leaktest.AfterTest(t)()
	var rec batchDurableRecorder
	d, err := Open("", &Options{
		FS:            vfs.NewMem(),
		DisableWAL:    true,
		EventListener: &EventListener{BatchDurable: rec.record},
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, d.Close()) }()

	require.NoError(t, d.Set([]byte("a"), nil, NoSync))
	require.Error(t, d.Set([]byte("a"), nil, Sync))
	require.Empty(t, rec.take())

	require.NoError(t, d.WaitForDurability(farSeqNum))
	require.NoError(t, d.WaitForDurabilityBatch([]base.SeqNum{1, farSeqNum}))
	require.NoError(t, d.WaitForJobDurability(1))
	require.NoError(t, requireResolved(t, d.DurabilityNotify(farSeqNum)))
	require.Equal(t, DurabilityStats{}, d.DurabilityStats())
	require.Zero(t, d.Metrics().DurableCommitCount)
}

func TestBatchDurableSyncFailure(t *testing.T) {
	defer leaktest.AfterTest(t)()
	for _, noSyncWait := range []bool{false, true} {
		t.Run(fmt.Sprintf("noSyncWait=%t", noSyncWait), func(t *testing.T) {
			var injectSyncErr atomic.Bool
			fs := errorfs.Wrap(vfs.NewMem(), errorfs.InjectorFunc(func(op errorfs.Op) error {
				switch op.Kind {
				case errorfs.OpFileSync, errorfs.OpFileSyncData, errorfs.OpFileSyncTo:
					if injectSyncErr.Load() && strings.HasSuffix(op.Path, ".log") {
						return errorfs.ErrInjected
					}
				}
				return nil
			}))
			var rec batchDurableRecorder
			logger := &fatalCapturingLogger{t: t}
			d, err := Open("", &Options{
				FS:            fs,
				Logger:        logger,
				EventListener: &EventListener{BatchDurable: rec.record},
			})
			require.NoError(t, err)

			require.NoError(t, d.Set([]byte("a"), nil, Sync))
			durable, err := d.DurableState()
			require.NoError(t, err)

			waitErr := make(chan error, 1)
			go func() { waitErr <- d.WaitForDurability(farSeqNum) }()
			notify := d.DurabilityNotify(farSeqNum)
			waitForPendingWaiters(t, d, 1)

			injectSyncErr.Store(true)
			b := d.NewBatch()
			require.NoError(t, b.Set([]byte("b"), nil, nil))
			if noSyncWait {
				require.NoError(t, d.ApplyNoSyncWait(b, &WriteOptions{Sync: true, CommitCorrelationID: 3}))
				require.ErrorIs(t, b.SyncWait(), errorfs.ErrInjected)
			} else {
				// The commit error is fatal, which fatalCapturingLogger captures.
				require.NoError(t, d.Apply(b, &WriteOptions{Sync: true, CommitCorrelationID: 3}))
				require.ErrorIs(t, logger.err, errorfs.ErrInjected)
			}

			infos := rec.take()
			require.Len(t, infos, 2)
			info := infos[1]
			require.ErrorIs(t, info.Err, errorfs.ErrInjected)
			require.Equal(t, 2, info.JobID)
			require.Equal(t, b.SeqNum(), info.SeqNum)
			require.Equal(t, uint64(3), info.CorrelationID)

			require.ErrorIs(t, <-waitErr, errorfs.ErrInjected)
			require.ErrorIs(t, <-notify, errorfs.ErrInjected)
			seqNum, err := d.DurableState()
			require.ErrorIs(t, err, errorfs.ErrInjected)
			require.Equal(t, durable, seqNum)
			require.NoError(t, d.WaitForDurability(durable))
			require.ErrorIs(t, d.WaitForDurability(b.SeqNum()), errorfs.ErrInjected)
			require.ErrorIs(t, requireResolved(t, d.DurabilityNotify(b.SeqNum())), errorfs.ErrInjected)
			require.NoError(t, d.WaitForJobDurability(1))
			require.ErrorIs(t, d.WaitForJobDurability(info.JobID), errorfs.ErrInjected)

			stats := d.DurabilityStats()
			require.Equal(t, uint64(1), stats.TotalDurableCommits)
			require.Equal(t, uint64(1), stats.TotalFailedCommits)
			require.ErrorIs(t, stats.FirstErr, errorfs.ErrInjected)
			require.Equal(t, durable, stats.HighestDurableSeqNum)
			require.Equal(t, uint64(1), d.Metrics().DurableCommitCount)

			// Closing the WAL fails as well.
			_ = d.Close()
		})
	}
}

func TestBatchDurableConcurrent(t *testing.T) {
	defer leaktest.AfterTest(t)()
	var rec batchDurableRecorder
	d, err := Open("", &Options{
		FS:            vfs.NewMem(),
		EventListener: &EventListener{BatchDurable: rec.record},
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, d.Close()) }()

	const goroutines = 8
	const commitsPerGoroutine = 100
	var wg sync.WaitGroup
	seqNums := make([][]base.SeqNum, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < commitsPerGoroutine; i++ {
				b := d.NewBatch()
				require.NoError(t, b.Set([]byte(fmt.Sprintf("%d-%d", g, i)), nil, nil))
				opts := &WriteOptions{Sync: i%3 != 0, CommitCorrelationID: uint64(g*commitsPerGoroutine + i)}
				if opts.Sync && i%2 == 0 {
					require.NoError(t, d.ApplyNoSyncWait(b, opts))
					require.NoError(t, b.SyncWait())
				} else {
					require.NoError(t, b.Commit(opts))
				}
				if opts.Sync {
					seqNums[g] = append(seqNums[g], b.SeqNum())
				}
				ch := d.DurabilityNotify(b.SeqNum())
				if opts.Sync {
					require.NoError(t, d.WaitForDurability(b.SeqNum()))
					require.NoError(t, requireResolved(t, ch))
				}
				require.NoError(t, b.Close())
			}
		}()
	}
	wg.Wait()

	var want []base.SeqNum
	for _, s := range seqNums {
		want = append(want, s...)
	}
	infos := rec.take()
	require.Len(t, infos, len(want))
	var got []base.SeqNum
	var jobIDs []int
	for _, info := range infos {
		require.NoError(t, info.Err)
		require.Equal(t, uint32(1), info.KeyCount)
		got = append(got, info.SeqNum)
		jobIDs = append(jobIDs, info.JobID)
	}
	slices.Sort(want)
	slices.Sort(got)
	require.Equal(t, want, got)
	slices.Sort(jobIDs)
	for i, jobID := range jobIDs {
		require.Equal(t, i+1, jobID)
	}
	require.NoError(t, d.WaitForDurabilityBatch(want))
	stats := d.DurabilityStats()
	require.Equal(t, uint64(len(want)), stats.TotalDurableCommits)
	require.Zero(t, stats.PendingWaiters)
	require.Equal(t, uint64(len(want)), d.Metrics().DurableCommitCount)
}

func TestBatchDurableInfoFormat(t *testing.T) {
	defer leaktest.AfterTest(t)()
	info := BatchDurableInfo{
		JobID:         3,
		SeqNum:        12,
		ApplyDuration: 1500 * time.Microsecond,
		SyncDuration:  2 * time.Millisecond,
		CorrelationID: 7,
		BatchSize:     64,
		KeyCount:      2,
	}
	require.Equal(t,
		"[JOB 3] batch durable (seqnum 12, correlation 7): 2 keys (64B), apply 1.5ms, sync 2ms",
		info.String())
	info.Err = errors.New("sync failed")
	require.Equal(t, "[JOB 3] batch durability error (seqnum 12, correlation 7): sync failed", info.String())

	// The logging listener only logs failures.
	var log base.InMemLogger
	l := MakeLoggingEventListener(&log)
	l.BatchDurable(BatchDurableInfo{JobID: 1, SeqNum: 10, KeyCount: 1})
	require.Empty(t, log.String())
	l.BatchDurable(info)
	require.Equal(t, "[JOB 3] batch durability error (seqnum 12, correlation 7): sync failed\n", log.String())
}
