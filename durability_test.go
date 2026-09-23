// Copyright 2026 The LevelDB-Go and Pebble Authors. All rights reserved. Use
// of this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package pebble

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/pebble/internal/base"
	"github.com/cockroachdb/pebble/vfs"
	"github.com/cockroachdb/pebble/vfs/errorfs"
	"github.com/stretchr/testify/require"
)

func TestBatchDurableSyncCommit(t *testing.T) {
	var mu sync.Mutex
	var infos []BatchDurableInfo
	listener := EventListener{
		BatchDurable: func(info BatchDurableInfo) {
			mu.Lock()
			infos = append(infos, info)
			mu.Unlock()
		},
	}
	d, err := Open("", &Options{
		FS:            vfs.NewMem(),
		Logger:        &base.InMemLogger{},
		EventListener: &listener,
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, d.Close()) }()

	st := d.DurabilityStats()
	require.Equal(t, DurabilityStats{}, st)

	require.NoError(t, d.Set([]byte("a"), []byte("b"), &WriteOptions{Sync: false}))
	mu.Lock()
	require.Empty(t, infos)
	mu.Unlock()
	m := d.Metrics()
	require.Zero(t, m.DurableCommitCount)

	const correlation = uint64(42)
	require.NoError(t, d.Set([]byte("a"), []byte("b"), &WriteOptions{
		Sync:                true,
		CommitCorrelationID: correlation,
	}))

	mu.Lock()
	require.Len(t, infos, 1)
	info := infos[0]
	mu.Unlock()
	require.NotZero(t, info.JobID)
	require.NotZero(t, info.SeqNum)
	require.NoError(t, info.Err)
	require.Positive(t, info.ApplyDuration)
	require.Positive(t, info.SyncDuration)
	require.Equal(t, correlation, info.CorrelationID)
	require.Positive(t, info.BatchSize)
	require.Equal(t, uint32(1), info.KeyCount)

	require.NoError(t, d.WaitForDurability(info.SeqNum))
	require.NoError(t, d.WaitForDurability(0))
	require.NoError(t, d.WaitForDurabilityBatch(nil))
	require.NoError(t, d.WaitForDurabilityBatch([]base.SeqNum{}))
	require.NoError(t, d.WaitForDurabilityBatch([]base.SeqNum{0, info.SeqNum}))
	require.NoError(t, d.WaitForJobDurability(info.JobID))

	seq, err := d.DurableState()
	require.NoError(t, err)
	require.GreaterOrEqual(t, seq, info.SeqNum)

	stats := d.DurabilityStats()
	require.Equal(t, seq, stats.HighestDurableSeqNum)
	require.NoError(t, stats.FirstErr)
	require.Equal(t, int64(0), stats.PendingWaiters)
	require.Equal(t, uint64(1), stats.TotalDurableCommits)
	require.Zero(t, stats.TotalFailedCommits)
	require.Positive(t, stats.CumulativeSyncDuration)
	require.Positive(t, stats.MaxSyncDuration)

	m = d.Metrics()
	require.Equal(t, uint64(1), m.DurableCommitCount)
	require.Positive(t, m.DurableCommitDuration)

	ch := d.DurabilityNotify(info.SeqNum)
	select {
	case err := <-ch:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("notify was not pre-filled")
	}

	unknown := d.WaitForJobDurability(0)
	require.Error(t, unknown)
	require.Contains(t, unknown.Error(), "unknown")
	never := d.WaitForJobDurability(info.JobID + 1000)
	require.Error(t, never)
	require.Contains(t, never.Error(), "unknown")
}

func TestBatchDurableNoSyncAndDisabledWAL(t *testing.T) {
	var called atomicInt
	listener := EventListener{BatchDurable: func(BatchDurableInfo) { called.add(1) }}
	d, err := Open("", &Options{
		FS:            vfs.NewMem(),
		DisableWAL:    true,
		Logger:        &base.InMemLogger{},
		EventListener: &listener,
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, d.Close()) }()

	require.NoError(t, d.Set([]byte("a"), []byte("b"), NoSync))
	require.Equal(t, 0, called.load())
	require.NoError(t, d.WaitForDurability(100))
	require.NoError(t, d.WaitForDurabilityBatch([]base.SeqNum{1, 2}))
	require.NoError(t, d.WaitForJobDurability(0))
	ch := d.DurabilityNotify(50)
	select {
	case err := <-ch:
		require.NoError(t, err)
	default:
		t.Fatal("DisableWAL notify should be pre-filled")
	}
	m := d.Metrics()
	require.Zero(t, m.DurableCommitCount)
	require.Zero(t, m.DurableCommitDuration)
}

func TestBatchDurableMetricsRequireCallback(t *testing.T) {
	d, err := Open("", &Options{
		FS:     vfs.NewMem(),
		Logger: &base.InMemLogger{},
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, d.Close()) }()
	require.NoError(t, d.Set([]byte("a"), []byte("b"), Sync))
	m := d.Metrics()
	require.Zero(t, m.DurableCommitCount)
	require.Zero(t, m.DurableCommitDuration)
	stats := d.DurabilityStats()
	require.Equal(t, uint64(1), stats.TotalDurableCommits)
	require.Positive(t, stats.CumulativeSyncDuration)
}

func TestBatchDurableTeeAndNoSyncWait(t *testing.T) {
	var a, b atomicInt
	l1 := EventListener{BatchDurable: func(BatchDurableInfo) { a.add(1) }}
	l2 := EventListener{BatchDurable: func(BatchDurableInfo) { b.add(1) }}
	opts := &Options{FS: vfs.NewMem(), Logger: &base.InMemLogger{}}
	opts.AddEventListener(l1)
	opts.AddEventListener(l2)
	d, err := Open("", opts)
	require.NoError(t, err)
	defer func() { require.NoError(t, d.Close()) }()

	batch := d.NewBatch()
	require.NoError(t, batch.Set([]byte("k"), []byte("v"), nil))
	require.NoError(t, d.ApplyNoSyncWait(batch, Sync))
	deadline := time.After(2 * time.Second)
	for a.load() == 0 || b.load() == 0 {
		select {
		case <-deadline:
			t.Fatalf("callback counts a=%d b=%d", a.load(), b.load())
		default:
			time.Sleep(time.Millisecond)
		}
	}
	require.NoError(t, batch.SyncWait())
	require.Equal(t, 1, a.load())
	require.Equal(t, 1, b.load())
	require.Equal(t, uint64(1), d.Metrics().DurableCommitCount)
}

func TestDurabilityWaitersCloseAndContext(t *testing.T) {
	d, err := Open("", &Options{FS: vfs.NewMem(), Logger: &base.InMemLogger{}})
	require.NoError(t, err)

	started := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		close(started)
		errCh <- d.WaitForDurability(1 << 30)
	}()
	<-started
	require.Eventually(t, func() bool {
		return d.DurabilityStats().PendingWaiters >= 1
	}, time.Second, time.Millisecond)
	require.NoError(t, d.Close())
	select {
	case err := <-errCh:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("waiter was not unblocked by Close")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = d.WaitForDurabilityContext(ctx, 1<<30)
	require.Error(t, err)
	require.NotErrorIs(t, err, context.Canceled)
}

func TestDurabilityNotifyLimitAndContextCancel(t *testing.T) {
	d, err := Open("", &Options{FS: vfs.NewMem(), Logger: &base.InMemLogger{}})
	require.NoError(t, err)
	defer func() { require.NoError(t, d.Close()) }()

	var chans []<-chan error
	for range durabilityNotifyLimit {
		chans = append(chans, d.DurabilityNotify(1))
	}
	extra := d.DurabilityNotify(1)
	select {
	case err := <-extra:
		require.Error(t, err)
	default:
		t.Fatal("subscription past the limit should be pre-filled")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = d.WaitForDurabilityContext(ctx, 1<<40)
	require.ErrorIs(t, err, context.Canceled)

	require.NoError(t, d.Set([]byte("a"), []byte("b"), Sync))
	for _, ch := range chans {
		select {
		case err := <-ch:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("notify did not fire")
		}
	}
}

func TestDurabilityJobExpired(t *testing.T) {
	prev := durabilityJobRetention
	durabilityJobRetention = 2
	defer func() { durabilityJobRetention = prev }()

	var jobs []int
	var mu sync.Mutex
	d, err := Open("", &Options{
		FS:     vfs.NewMem(),
		Logger: &base.InMemLogger{},
		EventListener: &EventListener{BatchDurable: func(info BatchDurableInfo) {
			mu.Lock()
			jobs = append(jobs, info.JobID)
			mu.Unlock()
		}},
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, d.Close()) }()

	for i := range 4 {
		require.NoError(t, d.Set([]byte{byte('a' + i)}, []byte("v"), Sync))
	}
	mu.Lock()
	require.Len(t, jobs, 4)
	first := jobs[0]
	mu.Unlock()
	err = d.WaitForJobDurability(first)
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "expired")
	require.NoError(t, d.WaitForJobDurability(jobs[len(jobs)-1]))
}

func TestBatchDurableSyncError(t *testing.T) {
	var mu sync.Mutex
	var infos []BatchDurableInfo
	var inject atomicBool
	fs := errorfs.Wrap(vfs.NewMem(), errorfs.InjectorFunc(func(op errorfs.Op) error {
		if inject.load() && (op.Kind == errorfs.OpFileSync || op.Kind == errorfs.OpFileSyncData) {
			return errorfs.ErrInjected
		}
		return nil
	}))
	d, err := Open("", &Options{
		FS:     fs,
		Logger: &base.InMemLogger{},
		EventListener: &EventListener{BatchDurable: func(info BatchDurableInfo) {
			mu.Lock()
			infos = append(infos, info)
			mu.Unlock()
		}},
	})
	require.NoError(t, err)
	defer func() {
		inject.store(false)
		_ = d.Close()
	}()

	require.NoError(t, d.Set([]byte("a"), []byte("b"), Sync))
	mu.Lock()
	require.NotEmpty(t, infos)
	infos = nil
	mu.Unlock()

	inject.store(true)
	_ = d.Set([]byte("c"), []byte("d"), Sync)
	mu.Lock()
	require.NotEmpty(t, infos)
	require.Error(t, infos[len(infos)-1].Err)
	mu.Unlock()
	_, err = d.DurableState()
	require.Error(t, err)
	require.NotZero(t, d.DurabilityStats().TotalFailedCommits)
}

type atomicBool struct {
	mu sync.Mutex
	v  bool
}

func (a *atomicBool) store(v bool) {
	a.mu.Lock()
	a.v = v
	a.mu.Unlock()
}

func (a *atomicBool) load() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.v
}

type atomicInt struct {
	mu sync.Mutex
	v  int
}

func (a *atomicInt) add(n int) {
	a.mu.Lock()
	a.v += n
	a.mu.Unlock()
}

func (a *atomicInt) load() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.v
}
