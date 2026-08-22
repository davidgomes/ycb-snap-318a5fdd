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

	"github.com/cockroachdb/crlib/testutils/leaktest"
	"github.com/cockroachdb/errors"
	"github.com/cockroachdb/pebble/internal/base"
	"github.com/cockroachdb/pebble/internal/testutils"
	"github.com/cockroachdb/pebble/vfs"
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
	if opts.Logger == nil {
		opts.Logger = testutils.Logger{T: t}
	}
	d, err := Open("", opts)
	require.NoError(t, err)
	return d
}

func TestBatchDurableBasic(t *testing.T) {
	defer leaktest.AfterTest(t)()

	var infos []BatchDurableInfo
	var mu sync.Mutex
	d := openDurabilityDB(t, &Options{
		EventListener: &EventListener{
			BatchDurable: func(info BatchDurableInfo) {
				mu.Lock()
				infos = append(infos, info)
				mu.Unlock()
			},
		},
	})
	defer func() { require.NoError(t, d.Close()) }()

	stats := d.DurabilityStats()
	require.Equal(t, base.SeqNum(0), stats.HighestDurableSeqNum)
	require.NoError(t, stats.FirstErr)
	require.EqualValues(t, 0, stats.PendingWaiters)
	require.EqualValues(t, 0, stats.TotalDurableCommits)
	require.EqualValues(t, 0, stats.TotalFailedCommits)
	require.Equal(t, time.Duration(0), stats.CumulativeSyncDuration)
	require.Equal(t, time.Duration(0), stats.MaxSyncDuration)
	seq, err := d.DurableState()
	require.NoError(t, err)
	require.Equal(t, base.SeqNum(0), seq)
	require.EqualValues(t, 0, d.Metrics().DurableCommitCount)
	require.Equal(t, time.Duration(0), d.Metrics().DurableCommitDuration)

	require.NoError(t, d.Set([]byte("a"), []byte("1"), &WriteOptions{
		Sync:                true,
		CommitCorrelationID: 42,
	}))

	mu.Lock()
	require.Len(t, infos, 1)
	info := infos[0]
	mu.Unlock()

	require.Greater(t, info.JobID, 0)
	require.Greater(t, info.SeqNum, base.SeqNum(0))
	require.NoError(t, info.Err)
	require.Greater(t, info.ApplyDuration, time.Duration(0))
	require.Greater(t, info.SyncDuration, time.Duration(0))
	require.EqualValues(t, 42, info.CorrelationID)
	require.Greater(t, info.BatchSize, 0)
	require.EqualValues(t, 1, info.KeyCount)

	require.NoError(t, d.WaitForDurability(info.SeqNum))
	require.NoError(t, d.WaitForDurability(0))
	require.NoError(t, d.WaitForJobDurability(info.JobID))
	require.NoError(t, d.WaitForDurabilityBatch([]base.SeqNum{0, info.SeqNum}))
	require.NoError(t, d.WaitForDurabilityBatch(nil))
	require.NoError(t, d.WaitForDurabilityBatch([]base.SeqNum{}))

	seq, err = d.DurableState()
	require.NoError(t, err)
	require.GreaterOrEqual(t, seq, info.SeqNum)

	stats = d.DurabilityStats()
	require.GreaterOrEqual(t, stats.HighestDurableSeqNum, info.SeqNum)
	require.NoError(t, stats.FirstErr)
	require.EqualValues(t, 0, stats.PendingWaiters)
	require.EqualValues(t, 1, stats.TotalDurableCommits)
	require.EqualValues(t, 0, stats.TotalFailedCommits)
	require.Greater(t, stats.CumulativeSyncDuration, time.Duration(0))
	require.Greater(t, stats.MaxSyncDuration, time.Duration(0))

	m := d.Metrics()
	require.EqualValues(t, 1, m.DurableCommitCount)
	require.Greater(t, m.DurableCommitDuration, time.Duration(0))
	require.Equal(t, stats.CumulativeSyncDuration, m.DurableCommitDuration)
}

func TestBatchDurableNotFiredForNoSyncOrDisableWAL(t *testing.T) {
	defer leaktest.AfterTest(t)()

	t.Run("nosync", func(t *testing.T) {
		var n atomic.Int32
		d := openDurabilityDB(t, &Options{
			EventListener: &EventListener{
				BatchDurable: func(BatchDurableInfo) { n.Add(1) },
			},
		})
		defer func() { require.NoError(t, d.Close()) }()
		require.NoError(t, d.Set([]byte("a"), []byte("1"), NoSync))
		require.EqualValues(t, 0, n.Load())
		require.EqualValues(t, 0, d.DurabilityStats().TotalDurableCommits)
	})

	t.Run("disable-wal", func(t *testing.T) {
		var n atomic.Int32
		d := openDurabilityDB(t, &Options{
			DisableWAL: true,
			EventListener: &EventListener{
				BatchDurable: func(BatchDurableInfo) { n.Add(1) },
			},
		})
		defer func() { require.NoError(t, d.Close()) }()
		require.NoError(t, d.Set([]byte("a"), []byte("1"), NoSync))
		require.EqualValues(t, 0, n.Load())
		require.NoError(t, d.WaitForDurability(0))
		require.NoError(t, d.WaitForDurability(99))
		require.NoError(t, d.WaitForDurabilityBatch([]base.SeqNum{1, 2}))
		require.NoError(t, d.WaitForJobDurability(0))
		require.NoError(t, d.WaitForJobDurability(1))
		require.NoError(t, <-d.DurabilityNotify(0))
		require.NoError(t, <-d.DurabilityNotify(99))
		require.EqualValues(t, 0, d.Metrics().DurableCommitCount)
	})
}

func TestBatchDurableMetricsOnlyWhenConfigured(t *testing.T) {
	defer leaktest.AfterTest(t)()

	d := openDurabilityDB(t, &Options{})
	defer func() { require.NoError(t, d.Close()) }()
	require.NoError(t, d.Set([]byte("a"), []byte("1"), Sync))
	require.NoError(t, d.WaitForDurability(0))
	require.EqualValues(t, 1, d.DurabilityStats().TotalDurableCommits)
	require.EqualValues(t, 0, d.Metrics().DurableCommitCount)
	require.Equal(t, time.Duration(0), d.Metrics().DurableCommitDuration)
}

func TestWaitForDurabilityBlocksThenUnblocks(t *testing.T) {
	defer leaktest.AfterTest(t)()

	got := make(chan BatchDurableInfo, 1)
	d := openDurabilityDB(t, &Options{
		EventListener: &EventListener{
			BatchDurable: func(info BatchDurableInfo) { got <- info },
		},
	})
	defer func() { require.NoError(t, d.Close()) }()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.WaitForDurability(0)
	}()
	select {
	case err := <-errCh:
		t.Fatalf("WaitForDurability returned before commit: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	require.NoError(t, d.Set([]byte("k"), []byte("v"), Sync))
	info := <-got
	require.NoError(t, <-errCh)
	require.NoError(t, d.WaitForDurabilityContext(context.Background(), info.SeqNum))
}

func TestWaitForJobDurabilityUnknownExpired(t *testing.T) {
	defer leaktest.AfterTest(t)()

	var last atomic.Int32
	d := openDurabilityDB(t, &Options{
		EventListener: &EventListener{
			BatchDurable: func(info BatchDurableInfo) {
				last.Store(int32(info.JobID))
			},
		},
	})
	defer func() { require.NoError(t, d.Close()) }()

	err := d.WaitForJobDurability(0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown")

	err = d.WaitForJobDurability(1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown")

	require.NoError(t, d.Set([]byte("a"), []byte("1"), Sync))
	require.NoError(t, d.WaitForJobDurability(int(last.Load())))

	for i := 0; i < DurabilityJobRetention+1; i++ {
		require.NoError(t, d.Set([]byte("k"), []byte(fmt.Sprint(i)), Sync))
	}
	err = d.WaitForJobDurability(1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expired")
}

func TestDurabilityNotify(t *testing.T) {
	defer leaktest.AfterTest(t)()

	d := openDurabilityDB(t, &Options{
		EventListener: &EventListener{BatchDurable: func(BatchDurableInfo) {}},
	})
	defer func() { require.NoError(t, d.Close()) }()

	ch := d.DurabilityNotify(0)
	select {
	case <-ch:
		t.Fatal("DurabilityNotify(0) delivered before any commit")
	default:
	}

	require.NoError(t, d.Set([]byte("a"), []byte("1"), Sync))
	require.NoError(t, <-ch)
	require.NoError(t, <-d.DurabilityNotify(0))

	// Bound outstanding subscriptions against a seqnum that is not yet durable.
	var filled int
	for i := 0; i < MaxDurabilityNotify+5; i++ {
		c := d.DurabilityNotify(base.SeqNum(1 << 20))
		select {
		case err := <-c:
			require.Error(t, err)
			filled++
		default:
		}
	}
	require.Greater(t, filled, 0)
}

func TestDurabilityWaitersUnblockOnClose(t *testing.T) {
	defer leaktest.AfterTest(t)()

	d := openDurabilityDB(t, &Options{})
	errCh := make(chan error, 2)
	go func() { errCh <- d.WaitForDurability(0) }()
	go func() { errCh <- d.WaitForDurability(12345) }()
	notify := d.DurabilityNotify(99)

	// Wait until waiters are registered.
	require.Eventually(t, func() bool {
		return d.DurabilityStats().PendingWaiters >= 2
	}, 2*time.Second, time.Millisecond)

	require.NoError(t, d.Close())
	for i := 0; i < 2; i++ {
		err := <-errCh
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrClosed) || strings.Contains(err.Error(), "closed"))
	}
	err := <-notify
	require.Error(t, err)
}

func TestDurabilityContextCanceled(t *testing.T) {
	defer leaktest.AfterTest(t)()

	d := openDurabilityDB(t, &Options{})
	defer func() { require.NoError(t, d.Close()) }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := d.WaitForDurabilityContext(ctx, 0)
	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled))

	// Already-durable seqnum wins over a cancelled context.
	require.NoError(t, d.Set([]byte("a"), []byte("1"), Sync))
	err = d.WaitForDurabilityContext(ctx, 0)
	require.NoError(t, err)
}

func TestDurabilityContextVsClosePrecedence(t *testing.T) {
	defer leaktest.AfterTest(t)()

	d := openDurabilityDB(t, &Options{})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.WaitForDurabilityContext(ctx, 0)
	}()
	require.Eventually(t, func() bool {
		return d.DurabilityStats().PendingWaiters >= 1
	}, 2*time.Second, time.Millisecond)

	require.NoError(t, d.Close())
	cancel()
	err := <-errCh
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrClosed) || strings.Contains(err.Error(), "closed"))
}

func TestTeeEventListenerBatchDurable(t *testing.T) {
	defer leaktest.AfterTest(t)()

	var a, b int
	el := TeeEventListener(
		EventListener{BatchDurable: func(BatchDurableInfo) { a++ }},
		EventListener{BatchDurable: func(BatchDurableInfo) { b++ }},
	)
	d := openDurabilityDB(t, &Options{EventListener: &el})
	defer func() { require.NoError(t, d.Close()) }()
	require.NoError(t, d.Set([]byte("a"), []byte("1"), Sync))
	require.Equal(t, 1, a)
	require.Equal(t, 1, b)
}

func TestBatchDurableApplyNoSyncWait(t *testing.T) {
	defer leaktest.AfterTest(t)()

	done := make(chan BatchDurableInfo, 1)
	d := openDurabilityDB(t, &Options{
		EventListener: &EventListener{
			BatchDurable: func(info BatchDurableInfo) { done <- info },
		},
	})
	defer func() { require.NoError(t, d.Close()) }()
	b := d.NewBatch()
	require.NoError(t, b.Set([]byte("a"), []byte("1"), nil))
	require.NoError(t, d.ApplyNoSyncWait(b, Sync))
	info := <-done
	require.NoError(t, info.Err)
	require.Greater(t, info.SyncDuration, time.Duration(0))
	require.NoError(t, b.SyncWait())
	require.NoError(t, b.Close())
}

func TestWaitForDurabilityBatchEmpty(t *testing.T) {
	defer leaktest.AfterTest(t)()
	d := openDurabilityDB(t, &Options{})
	defer func() { require.NoError(t, d.Close()) }()
	require.NoError(t, d.WaitForDurabilityBatch(nil))
	require.NoError(t, d.WaitForDurabilityBatch([]base.SeqNum{}))
}

func TestBatchDurableOncePerSyncCommit(t *testing.T) {
	defer leaktest.AfterTest(t)()

	var n atomic.Int32
	d := openDurabilityDB(t, &Options{
		EventListener: &EventListener{
			BatchDurable: func(BatchDurableInfo) { n.Add(1) },
		},
	})
	defer func() { require.NoError(t, d.Close()) }()
	const commits = 20
	for i := 0; i < commits; i++ {
		require.NoError(t, d.Set([]byte{byte(i)}, []byte("v"), Sync))
	}
	require.EqualValues(t, commits, n.Load())
	require.EqualValues(t, commits, d.DurabilityStats().TotalDurableCommits)
}
