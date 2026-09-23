// Copyright 2026 The LevelDB-Go and Pebble Authors. All rights reserved. Use
// of this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package pebble

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/cockroachdb/pebble/internal/base"
	"github.com/cockroachdb/pebble/vfs"
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

func openDurabilityTestDB(t *testing.T, disableWAL bool, el *EventListener) *DB {
	opts := &Options{FS: vfs.NewMem(), DisableWAL: disableWAL}
	if el != nil {
		opts.EventListener = el
	}
	d, err := Open("", opts)
	require.NoError(t, err)
	return d
}

func applyBatch(t *testing.T, d *DB, key string, opts *WriteOptions) base.SeqNum {
	b := d.NewBatch()
	require.NoError(t, b.Set([]byte(key), []byte("v"), nil))
	require.NoError(t, b.Set([]byte(key+"2"), []byte("v"), nil))
	require.NoError(t, d.Apply(b, opts))
	seq := b.SeqNum()
	require.NoError(t, b.Close())
	return seq
}

func TestBatchDurableEvent(t *testing.T) {
	var r batchDurableRecorder
	d := openDurabilityTestDB(t, false, &EventListener{BatchDurable: r.record})
	defer func() { require.NoError(t, d.Close()) }()

	require.Equal(t, DurabilityStats{}, d.DurabilityStats())

	applyBatch(t, d, "a", NoSync)
	require.Empty(t, r.get())

	seq := applyBatch(t, d, "b", &WriteOptions{Sync: true, CommitCorrelationID: 42})
	infos := r.get()
	require.Len(t, infos, 1)
	info := infos[0]
	require.NoError(t, info.Err)
	require.Equal(t, seq, info.SeqNum)
	require.Equal(t, 1, info.JobID)
	require.Equal(t, uint64(42), info.CorrelationID)
	require.Equal(t, uint32(2), info.KeyCount)
	require.Greater(t, info.BatchSize, 0)
	require.Greater(t, info.ApplyDuration, time.Duration(0))
	require.Greater(t, info.SyncDuration, time.Duration(0))
	require.NoError(t, d.WaitForJobDurability(info.JobID))

	// ApplyNoSyncWait reports from SyncWait.
	b := d.NewBatch()
	require.NoError(t, b.Set([]byte("c"), []byte("v"), nil))
	require.NoError(t, d.ApplyNoSyncWait(b, Sync))
	require.NoError(t, b.SyncWait())
	require.NoError(t, b.Close())
	infos = r.get()
	require.Len(t, infos, 2)
	require.Equal(t, 2, infos[1].JobID)

	highest, err := d.DurableState()
	require.NoError(t, err)
	require.Equal(t, infos[1].SeqNum, highest)

	stats := d.DurabilityStats()
	require.Equal(t, uint64(2), stats.TotalDurableCommits)
	require.Zero(t, stats.TotalFailedCommits)
	require.Greater(t, stats.CumulativeSyncDuration, time.Duration(0))
	require.GreaterOrEqual(t, stats.CumulativeSyncDuration, stats.MaxSyncDuration)

	m := d.Metrics()
	require.Equal(t, uint64(2), m.DurableCommitCount)
	require.Equal(t, stats.CumulativeSyncDuration, m.DurableCommitDuration)
}

func TestBatchDurableNotConfigured(t *testing.T) {
	d := openDurabilityTestDB(t, false, nil)
	defer func() { require.NoError(t, d.Close()) }()
	seq := applyBatch(t, d, "a", Sync)
	require.NoError(t, d.WaitForDurability(seq))
	require.Equal(t, uint64(1), d.DurabilityStats().TotalDurableCommits)
	m := d.Metrics()
	require.Zero(t, m.DurableCommitCount)
	require.Zero(t, m.DurableCommitDuration)
}

func TestTeeEventListenerBatchDurable(t *testing.T) {
	require.True(t, isNoopBatchDurable(TeeEventListener(EventListener{}, EventListener{}).BatchDurable))

	var r1, r2 batchDurableRecorder
	el := TeeEventListener(EventListener{BatchDurable: r1.record}, EventListener{})
	el = TeeEventListener(el, EventListener{BatchDurable: r2.record})
	d := openDurabilityTestDB(t, false, &el)
	defer func() { require.NoError(t, d.Close()) }()
	applyBatch(t, d, "a", Sync)
	require.Len(t, r1.get(), 1)
	require.Len(t, r2.get(), 1)
	require.Equal(t, uint64(1), d.Metrics().DurableCommitCount)
}

func TestWaitForDurability(t *testing.T) {
	d := openDurabilityTestDB(t, false, nil)
	defer func() { require.NoError(t, d.Close()) }()

	require.NoError(t, d.WaitForDurability(0))
	require.NoError(t, d.WaitForDurabilityBatch(nil))

	seq := applyBatch(t, d, "a", NoSync)
	notifyCh := d.DurabilityNotify(seq)

	errCh := make(chan error, 1)
	go func() { errCh <- d.WaitForDurabilityBatch([]base.SeqNum{0, seq}) }()
	require.Eventually(t, func() bool {
		return d.DurabilityStats().PendingWaiters == 1
	}, 10*time.Second, time.Millisecond)

	select {
	case <-errCh:
		t.Fatal("waiter unblocked before sync")
	case <-notifyCh:
		t.Fatal("notify fired before sync")
	default:
	}

	// A later Sync commit makes the earlier NoSync write durable.
	seq2 := applyBatch(t, d, "b", Sync)
	require.NoError(t, <-errCh)
	require.NoError(t, <-notifyCh)
	require.Zero(t, d.DurabilityStats().PendingWaiters)
	require.NoError(t, d.WaitForDurability(seq2))
	require.NoError(t, <-d.DurabilityNotify(seq2))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := d.WaitForDurabilityContext(ctx, seq2+100)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Zero(t, d.DurabilityStats().PendingWaiters)

	// Known outcomes take precedence over a cancelled context.
	cancelled, cancel2 := context.WithCancel(context.Background())
	cancel2()
	require.NoError(t, d.WaitForDurabilityContext(cancelled, seq2))
}

func TestWaitForDurabilityClose(t *testing.T) {
	d := openDurabilityTestDB(t, false, nil)
	seq := applyBatch(t, d, "a", NoSync)
	notifyCh := d.DurabilityNotify(seq)
	errCh := make(chan error, 1)
	go func() { errCh <- d.WaitForDurabilityContext(context.Background(), seq) }()
	require.Eventually(t, func() bool {
		return d.DurabilityStats().PendingWaiters == 1
	}, 10*time.Second, time.Millisecond)
	require.NoError(t, d.Close())
	require.ErrorIs(t, <-errCh, ErrClosed)
	require.ErrorIs(t, <-notifyCh, ErrClosed)
}

func TestWaitForDurabilityDisableWAL(t *testing.T) {
	var r batchDurableRecorder
	d := openDurabilityTestDB(t, true, &EventListener{BatchDurable: r.record})
	defer func() { require.NoError(t, d.Close()) }()
	seq := applyBatch(t, d, "a", NoSync)
	require.NoError(t, d.WaitForDurability(seq+1000))
	require.NoError(t, d.WaitForDurabilityBatch([]base.SeqNum{seq, seq + 1000}))
	require.NoError(t, <-d.DurabilityNotify(seq+1000))
	require.Empty(t, r.get())
	require.Equal(t, DurabilityStats{}, d.DurabilityStats())
}

func TestWaitForJobDurability(t *testing.T) {
	d := openDurabilityTestDB(t, false, nil)
	defer func() { require.NoError(t, d.Close()) }()

	for _, id := range []int{0, 1, -1} {
		err := d.WaitForJobDurability(id)
		require.ErrorIs(t, err, ErrDurabilityJobUnknown)
		require.Contains(t, err.Error(), "unknown")
	}
	applyBatch(t, d, "a", Sync)
	require.NoError(t, d.WaitForJobDurability(1))
	require.ErrorIs(t, d.WaitForJobDurability(2), ErrDurabilityJobUnknown)

	for i := 0; i < durabilityJobRetention; i++ {
		applyBatch(t, d, "b", Sync)
	}
	err := d.WaitForJobDurabilityContext(context.Background(), 1)
	require.ErrorIs(t, err, ErrDurabilityJobExpired)
	require.Contains(t, err.Error(), "expired")
	require.NoError(t, d.WaitForJobDurability(2))
	require.NoError(t, d.WaitForJobDurability(durabilityJobRetention+1))
}

func TestDurabilityTrackerSyncFailure(t *testing.T) {
	var tr durabilityTracker
	w := make(chan error, 1)
	go func() { w <- tr.wait(context.Background(), 20) }()
	require.Eventually(t, func() bool { return tr.pendingWaiters.Load() == 1 }, 10*time.Second, time.Millisecond)
	n := tr.notify(30)

	require.Equal(t, uint64(1), tr.record(10, nil, time.Millisecond))
	syncErr := errors.New("injected sync error")
	require.Equal(t, uint64(2), tr.record(20, syncErr, 3*time.Millisecond))
	require.ErrorIs(t, <-w, syncErr)
	require.ErrorIs(t, <-n, syncErr)

	// Already-durable sequence numbers still succeed.
	require.NoError(t, tr.wait(context.Background(), 10))
	require.ErrorIs(t, tr.wait(context.Background(), 11), syncErr)
	require.NoError(t, tr.jobResult(1))
	require.ErrorIs(t, tr.jobResult(2), syncErr)

	highest, err := tr.state()
	require.Equal(t, base.SeqNum(10), highest)
	require.ErrorIs(t, err, syncErr)
	s := tr.stats()
	require.Equal(t, uint64(1), s.TotalDurableCommits)
	require.Equal(t, uint64(1), s.TotalFailedCommits)
	require.Equal(t, 4*time.Millisecond, s.CumulativeSyncDuration)
	require.Equal(t, 3*time.Millisecond, s.MaxSyncDuration)
}

func TestDurabilityNotifyLimit(t *testing.T) {
	var tr durabilityTracker
	chans := make([]<-chan error, maxDurabilityNotifySubscriptions)
	for i := range chans {
		chans[i] = tr.notify(100)
	}
	select {
	case err := <-tr.notify(100):
		require.Error(t, err)
	default:
		t.Fatal("expected pre-filled channel")
	}
	tr.record(100, nil, time.Millisecond)
	for _, ch := range chans {
		require.NoError(t, <-ch)
	}
	// Resolved subscriptions free up capacity.
	ch := tr.notify(200)
	select {
	case <-ch:
		t.Fatal("unexpected resolution")
	default:
	}
}
