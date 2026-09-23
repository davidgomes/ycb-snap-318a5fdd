// Copyright 2026 The LevelDB-Go and Pebble Authors. All rights reserved. Use
// of this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package pebble

import (
	"container/heap"
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/crlib/crtime"
	"github.com/cockroachdb/errors"
	"github.com/cockroachdb/pebble/internal/base"
)

var (
	// ErrDurabilityJobExpired is returned by DB.WaitForJobDurability when the
	// job ID is older than the retention window of recorded durability jobs.
	ErrDurabilityJobExpired = errors.New("pebble: durability job expired")
	// ErrDurabilityJobUnknown is returned by DB.WaitForJobDurability when the
	// job ID is zero or has never been issued.
	ErrDurabilityJobUnknown = errors.New("pebble: durability job unknown")

	errTooManyDurabilitySubscriptions = errors.New(
		"pebble: too many outstanding durability subscriptions")
)

const (
	// durabilityJobRetention is the number of most recent BatchDurable job
	// results retained for DB.WaitForJobDurability.
	durabilityJobRetention = 1024
	// maxDurabilityNotifySubscriptions bounds the number of unresolved
	// channels handed out by DB.DurabilityNotify.
	maxDurabilityNotifySubscriptions = 1 << 14
)

// DurabilityStats is a snapshot of the durability state of a DB. See
// DB.DurabilityStats.
type DurabilityStats struct {
	// HighestDurableSeqNum is the highest sequence number known to be durable
	// in the WAL.
	HighestDurableSeqNum base.SeqNum
	// FirstErr is the first WAL sync error observed by a Sync commit.
	FirstErr error
	// PendingWaiters is the number of goroutines currently blocked in one of
	// the DB.WaitFor*Durability* methods.
	PendingWaiters int64
	// TotalDurableCommits is the number of Sync commits whose WAL sync
	// succeeded.
	TotalDurableCommits uint64
	// TotalFailedCommits is the number of Sync commits whose WAL sync failed.
	TotalFailedCommits uint64
	// CumulativeSyncDuration is the sum of the WAL sync phase durations of all
	// Sync commits.
	CumulativeSyncDuration time.Duration
	// MaxSyncDuration is the largest WAL sync phase duration of any Sync
	// commit.
	MaxSyncDuration time.Duration
}

type durabilityWaiter struct {
	seqNum base.SeqNum
	// ch has capacity 1 and receives exactly one value.
	ch chan error
	// index is the position in durabilityWaiterHeap, or -1 once removed.
	index  int
	notify bool
}

type durabilityWaiterHeap []*durabilityWaiter

func (h durabilityWaiterHeap) Len() int           { return len(h) }
func (h durabilityWaiterHeap) Less(i, j int) bool { return h[i].seqNum < h[j].seqNum }
func (h durabilityWaiterHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *durabilityWaiterHeap) Push(x any) {
	w := x.(*durabilityWaiter)
	w.index = len(*h)
	*h = append(*h, w)
}
func (h *durabilityWaiterHeap) Pop() any {
	old := *h
	n := len(old)
	w := old[n-1]
	old[n-1] = nil
	w.index = -1
	*h = old[:n-1]
	return w
}

// durabilityTracker tracks which sequence numbers have been durably synced to
// the WAL by Sync commits, and wakes up goroutines waiting for durability. The
// zero value is ready to use.
//
// A successful WAL sync for a batch implies that every record written to the
// WAL before it is also durable, so durability is tracked as a single
// ratcheting high-water mark.
type durabilityTracker struct {
	pendingWaiters atomic.Int64

	mu struct {
		sync.Mutex
		highest  base.SeqNum
		firstErr error
		closeErr error
		waiters  durabilityWaiterHeap
		// notifySubs is the number of unresolved DurabilityNotify channels.
		notifySubs int
		lastJobID  uint64
		// jobs holds the result of job ID i at jobs[i%durabilityJobRetention].
		jobs         []error
		totalDurable uint64
		totalFailed  uint64
		cumSync      time.Duration
		maxSync      time.Duration
	}
}

// resolvedLocked returns the result for a wait on seqNum if it is already
// determined. Durability takes precedence over sync errors, which take
// precedence over DB close.
func (t *durabilityTracker) resolvedLocked(seqNum base.SeqNum) (error, bool) {
	switch {
	case seqNum <= t.mu.highest:
		return nil, true
	case t.mu.firstErr != nil:
		return t.mu.firstErr, true
	case t.mu.closeErr != nil:
		return t.mu.closeErr, true
	}
	return nil, false
}

func (t *durabilityTracker) deliverLocked(w *durabilityWaiter, err error) {
	if w.notify {
		t.mu.notifySubs--
	}
	w.ch <- err
}

func (t *durabilityTracker) wakeLocked() {
	for len(t.mu.waiters) > 0 {
		err, ok := t.resolvedLocked(t.mu.waiters[0].seqNum)
		if !ok {
			return
		}
		t.deliverLocked(heap.Pop(&t.mu.waiters).(*durabilityWaiter), err)
	}
}

// record records the outcome of the WAL sync for a Sync commit whose last
// sequence number is lastSeqNum, and returns the job ID assigned to it.
func (t *durabilityTracker) record(
	lastSeqNum base.SeqNum, err error, syncDuration time.Duration,
) uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.mu.jobs == nil {
		t.mu.jobs = make([]error, durabilityJobRetention)
	}
	t.mu.lastJobID++
	jobID := t.mu.lastJobID
	t.mu.jobs[jobID%durabilityJobRetention] = err

	t.mu.cumSync += syncDuration
	t.mu.maxSync = max(t.mu.maxSync, syncDuration)
	if err == nil {
		t.mu.totalDurable++
		t.mu.highest = max(t.mu.highest, lastSeqNum)
	} else {
		t.mu.totalFailed++
		if t.mu.firstErr == nil {
			t.mu.firstErr = err
		}
	}
	t.wakeLocked()
	return jobID
}

// close unblocks all waiters and causes future waits for sequence numbers that
// are not yet durable to fail with err.
func (t *durabilityTracker) close(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.mu.closeErr = err
	t.wakeLocked()
}

func (t *durabilityTracker) wait(ctx context.Context, seqNum base.SeqNum) error {
	t.mu.Lock()
	if err, ok := t.resolvedLocked(seqNum); ok {
		t.mu.Unlock()
		return err
	}
	if err := ctx.Err(); err != nil {
		t.mu.Unlock()
		return err
	}
	w := &durabilityWaiter{seqNum: seqNum, ch: make(chan error, 1)}
	heap.Push(&t.mu.waiters, w)
	t.pendingWaiters.Add(1)
	t.mu.Unlock()
	defer t.pendingWaiters.Add(-1)

	select {
	case err := <-w.ch:
		return err
	case <-ctx.Done():
		t.mu.Lock()
		if w.index >= 0 {
			heap.Remove(&t.mu.waiters, w.index)
			t.mu.Unlock()
			return ctx.Err()
		}
		t.mu.Unlock()
		// The waiter was resolved concurrently with the cancellation; its
		// result takes precedence.
		return <-w.ch
	}
}

func (t *durabilityTracker) notify(seqNum base.SeqNum) <-chan error {
	ch := make(chan error, 1)
	t.mu.Lock()
	defer t.mu.Unlock()
	if err, ok := t.resolvedLocked(seqNum); ok {
		ch <- err
		return ch
	}
	if t.mu.notifySubs >= maxDurabilityNotifySubscriptions {
		ch <- errTooManyDurabilitySubscriptions
		return ch
	}
	t.mu.notifySubs++
	heap.Push(&t.mu.waiters, &durabilityWaiter{seqNum: seqNum, ch: ch, notify: true})
	return ch
}

func (t *durabilityTracker) jobResult(jobID uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case jobID == 0 || jobID > t.mu.lastJobID:
		return errors.Wrapf(ErrDurabilityJobUnknown, "job %d", errors.Safe(jobID))
	case t.mu.lastJobID-jobID >= durabilityJobRetention:
		return errors.Wrapf(ErrDurabilityJobExpired, "job %d", errors.Safe(jobID))
	}
	return t.mu.jobs[jobID%durabilityJobRetention]
}

func (t *durabilityTracker) state() (base.SeqNum, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.mu.highest, t.mu.firstErr
}

func (t *durabilityTracker) stats() DurabilityStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return DurabilityStats{
		HighestDurableSeqNum:   t.mu.highest,
		FirstErr:               t.mu.firstErr,
		PendingWaiters:         t.pendingWaiters.Load(),
		TotalDurableCommits:    t.mu.totalDurable,
		TotalFailedCommits:     t.mu.totalFailed,
		CumulativeSyncDuration: t.mu.cumSync,
		MaxSyncDuration:        t.mu.maxSync,
	}
}

// batchDurableState is the per-batch state needed to report BatchDurable for a
// Sync commit.
type batchDurableState struct {
	walWritten    crtime.Mono
	applyDuration time.Duration
	batchSize     int
	keyCount      uint32
	correlationID uint64
	// db is set for ApplyNoSyncWait commits whose BatchDurable report is
	// deferred to Batch.SyncWait.
	db *DB
}

// reportBatchDurable records the outcome of the WAL sync of a Sync commit and
// invokes EventListener.BatchDurable. It must be called exactly once per Sync
// commit, after the WAL sync has completed.
func (d *DB) reportBatchDurable(b *Batch, err error) {
	// Durations are clamped to be positive: the commit did perform both
	// phases, and coarse monotonic clocks may otherwise report zero.
	syncDuration := max(b.durable.walWritten.Elapsed(), 1)
	applyDuration := max(b.durable.applyDuration, 1)

	lastSeqNum := b.SeqNum() - 1
	if b.durable.keyCount > 0 {
		lastSeqNum = b.SeqNum() + base.SeqNum(b.durable.keyCount) - 1
	}
	jobID := d.durability.record(lastSeqNum, err, syncDuration)
	if !d.batchDurableEnabled {
		return
	}
	if err == nil {
		d.durableCommitCount.Add(1)
		d.durableCommitDuration.Add(int64(syncDuration))
	}
	d.opts.EventListener.BatchDurable(BatchDurableInfo{
		JobID:         int(jobID),
		SeqNum:        b.SeqNum(),
		Err:           err,
		ApplyDuration: applyDuration,
		SyncDuration:  syncDuration,
		CorrelationID: b.durable.correlationID,
		BatchSize:     b.durable.batchSize,
		KeyCount:      b.durable.keyCount,
	})
}

// WaitForDurability blocks until seqNum has been durably synced to the WAL. It
// returns an error if a WAL sync fails or the DB is closed before seqNum is
// durable. A seqNum of zero is always durable. If the WAL is disabled,
// WaitForDurability returns nil immediately.
func (d *DB) WaitForDurability(seqNum base.SeqNum) error {
	return d.WaitForDurabilityContext(context.Background(), seqNum)
}

// WaitForDurabilityContext is like WaitForDurability, but also returns
// ctx.Err() if ctx is done before the outcome is known. A known outcome
// (durability, WAL sync failure or DB close) takes precedence over context
// cancellation.
func (d *DB) WaitForDurabilityContext(ctx context.Context, seqNum base.SeqNum) error {
	if d.opts.DisableWAL {
		return nil
	}
	return d.durability.wait(ctx, seqNum)
}

// WaitForDurabilityBatch blocks until every sequence number in seqNums is
// durable. See WaitForDurability.
func (d *DB) WaitForDurabilityBatch(seqNums []base.SeqNum) error {
	return d.WaitForDurabilityBatchContext(context.Background(), seqNums)
}

// WaitForDurabilityBatchContext is like WaitForDurabilityBatch, but also
// respects ctx. See WaitForDurabilityContext.
func (d *DB) WaitForDurabilityBatchContext(ctx context.Context, seqNums []base.SeqNum) error {
	if len(seqNums) == 0 {
		return nil
	}
	// Durability is a ratcheting high-water mark, so waiting for the largest
	// sequence number suffices.
	var maxSeqNum base.SeqNum
	for _, s := range seqNums {
		maxSeqNum = max(maxSeqNum, s)
	}
	return d.WaitForDurabilityContext(ctx, maxSeqNum)
}

// WaitForJobDurability returns the outcome of the WAL sync of the Sync commit
// identified by jobID, as reported by BatchDurableInfo.JobID. Only the most
// recent jobs are retained; older jobs return an error marked with
// ErrDurabilityJobExpired. Zero and never-issued job IDs return an error
// marked with ErrDurabilityJobUnknown.
func (d *DB) WaitForJobDurability(jobID int) error {
	return d.WaitForJobDurabilityContext(context.Background(), jobID)
}

// WaitForJobDurabilityContext is like WaitForJobDurability, but also respects
// ctx. See WaitForDurabilityContext.
func (d *DB) WaitForJobDurabilityContext(ctx context.Context, jobID int) error {
	if d.opts.DisableWAL {
		return nil
	}
	if jobID < 0 {
		return errors.Wrapf(ErrDurabilityJobUnknown, "job %d", errors.Safe(jobID))
	}
	// Job IDs are only issued once the job's outcome is known, so the result
	// is always immediately available and takes precedence over ctx.
	return d.durability.jobResult(uint64(jobID))
}

// DurableState returns the highest sequence number known to be durable and the
// first WAL sync error observed by a Sync commit, if any.
func (d *DB) DurableState() (base.SeqNum, error) {
	return d.durability.state()
}

// DurabilityNotify returns a channel that receives a single value once the
// outcome for seqNum is known: nil once seqNum is durable, or a non-nil error
// if a WAL sync fails or the DB is closed first. The channel is buffered, so
// the caller need not receive from it. If the outcome is already known, or too
// many subscriptions are outstanding, the returned channel already contains
// the result (an error in the latter case). If the WAL is disabled, the
// channel contains nil.
func (d *DB) DurabilityNotify(seqNum base.SeqNum) <-chan error {
	if d.opts.DisableWAL {
		ch := make(chan error, 1)
		ch <- nil
		return ch
	}
	return d.durability.notify(seqNum)
}

// DurabilityStats returns a snapshot of the durability state of the DB.
func (d *DB) DurabilityStats() DurabilityStats {
	return d.durability.stats()
}
