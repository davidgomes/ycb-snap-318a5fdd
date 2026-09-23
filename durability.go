// Copyright 2026 The LevelDB-Go and Pebble Authors. All rights reserved. Use
// of this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package pebble

import (
	"container/heap"
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/crlib/crtime"
	"github.com/cockroachdb/errors"
	"github.com/cockroachdb/pebble/internal/base"
)

const (
	// durableJobRetention is the number of most recent BatchDurable jobs whose
	// outcome is retained for DB.WaitForJobDurability.
	durableJobRetention = 1024
	// maxDurabilitySubscriptions bounds the number of outstanding channels
	// returned by DB.DurabilityNotify.
	maxDurabilitySubscriptions = 1024
)

var (
	// ErrDurabilityJobExpired is returned by DB.WaitForJobDurability for a job
	// that is older than the retained window of BatchDurable jobs.
	ErrDurabilityJobExpired = errors.New("pebble: durability job expired")
	// ErrDurabilityJobUnknown is returned by DB.WaitForJobDurability for a job
	// ID that has not been assigned to a BatchDurable event.
	ErrDurabilityJobUnknown = errors.New("pebble: unknown durability job")

	errTooManyDurabilitySubscriptions = errors.New("pebble: too many outstanding durability subscriptions")
)

// DurabilityStats is a snapshot of the durability of commits with
// WriteOptions.Sync. See DB.DurabilityStats.
type DurabilityStats struct {
	// HighestDurableSeqNum is the highest sequence number known to be durable.
	// All lower sequence numbers are durable as well.
	HighestDurableSeqNum base.SeqNum
	// FirstErr is the first error reported by a commit with WriteOptions.Sync.
	FirstErr error
	// PendingWaiters is the number of goroutines currently blocked in the
	// DB.WaitForDurability family of methods.
	PendingWaiters int64
	// TotalDurableCommits is the number of Sync commits that became durable.
	TotalDurableCommits uint64
	// TotalFailedCommits is the number of Sync commits that failed.
	TotalFailedCommits uint64
	// CumulativeSyncDuration is the sum of BatchDurableInfo.SyncDuration over
	// all Sync commits.
	CumulativeSyncDuration time.Duration
	// MaxSyncDuration is the largest BatchDurableInfo.SyncDuration.
	MaxSyncDuration time.Duration
}

// batchDurableState is the per-batch state used to report the durability of a
// commit with WriteOptions.Sync.
type batchDurableState struct {
	// db is set while the report of a DB.ApplyNoSyncWait commit is pending on
	// Batch.SyncWait observing the WAL sync.
	db   *DB
	info BatchDurableInfo
	// walWritten is when the batch was written to the WAL, from which
	// SyncDuration is measured.
	walWritten crtime.Mono
}

// batchDurable reports the outcome of a commit with WriteOptions.Sync, once its
// WAL sync has completed or the commit has failed.
func (d *DB) batchDurable(s *batchDurableState, err error) {
	info := s.info
	info.Err = err
	if s.walWritten != 0 {
		info.SyncDuration = s.walWritten.Elapsed()
	}
	d.durability.record(&info)
	d.opts.EventListener.BatchDurable(info)
}

// WaitForDurability is WaitForDurabilityContext without a context.
func (d *DB) WaitForDurability(seqNum base.SeqNum) error {
	return d.WaitForDurabilityContext(context.Background(), seqNum)
}

// WaitForDurabilityContext blocks until seqNum is durable in the WAL. Only
// commits with WriteOptions.Sync make sequence numbers durable: once the WAL
// sync of such a commit completes, its sequence numbers and all lower ones are
// durable. The zero sequence number is always durable.
//
// It returns nil once seqNum is durable. If seqNum is not durable, it returns
// the first error reported by a Sync commit, or an error wrapping ErrClosed
// once the DB is closed; these take precedence over ctx being done, which
// returns ctx.Err(). If Options.DisableWAL is set, it returns nil immediately.
func (d *DB) WaitForDurabilityContext(ctx context.Context, seqNum base.SeqNum) error {
	return d.durability.wait(ctx, seqNum)
}

// WaitForDurabilityBatch is WaitForDurabilityBatchContext without a context.
func (d *DB) WaitForDurabilityBatch(seqNums []base.SeqNum) error {
	return d.WaitForDurabilityBatchContext(context.Background(), seqNums)
}

// WaitForDurabilityBatchContext is like WaitForDurabilityContext, but blocks
// until every sequence number in seqNums is durable. It returns nil if seqNums
// is empty.
func (d *DB) WaitForDurabilityBatchContext(ctx context.Context, seqNums []base.SeqNum) error {
	if len(seqNums) == 0 {
		return nil
	}
	// Durability covers a prefix of the sequence numbers, so the highest one
	// becoming durable implies all others are.
	return d.durability.wait(ctx, slices.Max(seqNums))
}

// WaitForJobDurability is WaitForJobDurabilityContext without a context.
func (d *DB) WaitForJobDurability(jobID int) error {
	return d.WaitForJobDurabilityContext(context.Background(), jobID)
}

// WaitForJobDurabilityContext returns the outcome of the commit reported by
// the BatchDurable event with the given BatchDurableInfo.JobID: nil if it
// became durable, or its error. Only the most recent BatchDurable jobs are
// retained; older jobs return an error wrapping ErrDurabilityJobExpired, and
// job IDs that have not been assigned (including zero) return an error
// wrapping ErrDurabilityJobUnknown. If Options.DisableWAL is set, it returns
// nil immediately.
//
// Job IDs are only assigned once the outcome of a commit is known, so this
// never blocks and ctx does not affect the result; it is accepted for symmetry
// with the other wait methods.
func (d *DB) WaitForJobDurabilityContext(ctx context.Context, jobID int) error {
	return d.durability.jobResult(jobID)
}

// DurableState returns the highest durable sequence number and the first error
// reported by a commit with WriteOptions.Sync, if any.
func (d *DB) DurableState() (base.SeqNum, error) {
	d.durability.mu.Lock()
	defer d.durability.mu.Unlock()
	return d.durability.mu.highest, d.durability.mu.firstErr
}

// DurabilityNotify returns a channel that receives a single value once the
// outcome for seqNum is known: nil when seqNum is durable (see
// WaitForDurabilityContext), or an error if a WAL sync fails or the DB is
// closed first. The channel is buffered, and is returned already filled when
// the outcome is known up front. The number of outstanding subscriptions is
// bounded; past the bound, the returned channel is filled with an error. If
// Options.DisableWAL is set, the channel is filled with nil.
func (d *DB) DurabilityNotify(seqNum base.SeqNum) <-chan error {
	return d.durability.notify(seqNum)
}

// DurabilityStats returns a snapshot of the durability of commits with
// WriteOptions.Sync.
func (d *DB) DurabilityStats() DurabilityStats {
	t := &d.durability
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.mu.stats
	s.HighestDurableSeqNum = t.mu.highest
	s.FirstErr = t.mu.firstErr
	s.PendingWaiters = t.pendingWaiters.Load()
	return s
}

// durabilityTracker tracks the outcome of commits with WriteOptions.Sync, as
// reported by DB.batchDurable, and serves the DB's durability wait methods.
type durabilityTracker struct {
	// walDisabled is set when Options.DisableWAL is set, in which case nothing
	// can be waited upon.
	walDisabled bool
	// batchDurableConfigured is set when EventListener.BatchDurable is
	// configured, which gates the Metrics.DurableCommit* metrics.
	batchDurableConfigured bool

	pendingWaiters atomic.Int64

	mu struct {
		sync.Mutex
		highest   base.SeqNum
		firstErr  error
		closedErr error
		// waiters holds the unresolved waiters, ordered by sequence number.
		waiters durabilityWaiters
		// subscriptions is the number of waiters created by DurabilityNotify.
		subscriptions int
		// lastJobID is the job ID of the most recent BatchDurable event.
		lastJobID int
		// jobErrs holds the outcome of the most recent jobs, indexed by job ID
		// modulo durableJobRetention.
		jobErrs [durableJobRetention]error
		// stats does not maintain the fields populated by DB.DurabilityStats.
		stats                 DurabilityStats
		durableCommitCount    uint64
		durableCommitDuration time.Duration
	}
}

// record records the outcome of a Sync commit and assigns info.JobID.
func (t *durabilityTracker) record(info *BatchDurableInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.mu.lastJobID++
	info.JobID = t.mu.lastJobID
	t.mu.jobErrs[info.JobID%durableJobRetention] = info.Err
	t.mu.stats.CumulativeSyncDuration += info.SyncDuration
	t.mu.stats.MaxSyncDuration = max(t.mu.stats.MaxSyncDuration, info.SyncDuration)

	if info.Err != nil {
		t.mu.stats.TotalFailedCommits++
		if t.mu.firstErr == nil {
			t.mu.firstErr = info.Err
			t.resolveAllLocked(info.Err)
		}
		return
	}
	t.mu.stats.TotalDurableCommits++
	if t.batchDurableConfigured {
		t.mu.durableCommitCount++
		t.mu.durableCommitDuration += info.SyncDuration
	}
	// The batch occupies [SeqNum, SeqNum+KeyCount). The WAL is synced in
	// order, so the preceding sequence numbers are durable too, including
	// those of commits that did not request a sync.
	if end := info.SeqNum + base.SeqNum(info.KeyCount); end > t.mu.highest+1 {
		t.mu.highest = end - 1
		for len(t.mu.waiters) > 0 && t.mu.waiters[0].seqNum <= t.mu.highest {
			t.resolveLocked(heap.Pop(&t.mu.waiters).(*durabilityWaiter), nil)
		}
	}
}

func (t *durabilityTracker) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.mu.closedErr == nil {
		t.mu.closedErr = errors.Wrap(ErrClosed, "pebble: DB closed before sequence number became durable")
		t.resolveAllLocked(t.mu.closedErr)
	}
}

// resolvedLocked returns done=true with the outcome for seqNum if it is
// already known.
func (t *durabilityTracker) resolvedLocked(seqNum base.SeqNum) (done bool, err error) {
	switch {
	case seqNum <= t.mu.highest:
		return true, nil
	case t.mu.firstErr != nil:
		return true, t.mu.firstErr
	case t.mu.closedErr != nil:
		return true, t.mu.closedErr
	}
	return false, nil
}

func (t *durabilityTracker) resolveLocked(w *durabilityWaiter, err error) {
	if w.subscription {
		t.mu.subscriptions--
	}
	w.ch <- err
}

func (t *durabilityTracker) resolveAllLocked(err error) {
	for _, w := range t.mu.waiters {
		w.index = -1
		t.resolveLocked(w, err)
	}
	t.mu.waiters = nil
}

func (t *durabilityTracker) wait(ctx context.Context, seqNum base.SeqNum) error {
	if t.walDisabled {
		return nil
	}
	t.mu.Lock()
	if done, err := t.resolvedLocked(seqNum); done {
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
		defer t.mu.Unlock()
		if w.index < 0 {
			// The waiter was resolved concurrently, and its outcome takes
			// precedence over the context.
			return <-w.ch
		}
		heap.Remove(&t.mu.waiters, w.index)
		return ctx.Err()
	}
}

func (t *durabilityTracker) notify(seqNum base.SeqNum) <-chan error {
	ch := make(chan error, 1)
	if t.walDisabled {
		ch <- nil
		return ch
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if done, err := t.resolvedLocked(seqNum); done {
		ch <- err
		return ch
	}
	if t.mu.subscriptions >= maxDurabilitySubscriptions {
		ch <- errTooManyDurabilitySubscriptions
		return ch
	}
	t.mu.subscriptions++
	heap.Push(&t.mu.waiters, &durabilityWaiter{seqNum: seqNum, ch: ch, subscription: true})
	return ch
}

func (t *durabilityTracker) jobResult(jobID int) error {
	if t.walDisabled {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case jobID <= 0 || jobID > t.mu.lastJobID:
		return errors.Wrapf(ErrDurabilityJobUnknown, "job %d", errors.Safe(jobID))
	case jobID <= t.mu.lastJobID-durableJobRetention:
		return errors.Wrapf(ErrDurabilityJobExpired, "job %d (only the last %d jobs are retained)",
			errors.Safe(jobID), errors.Safe(durableJobRetention))
	}
	return t.mu.jobErrs[jobID%durableJobRetention]
}

func (t *durabilityTracker) durableCommitMetrics() (count uint64, duration time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.mu.durableCommitCount, t.mu.durableCommitDuration
}

// durabilityWaiter is a pending DB.WaitForDurability call or DB.DurabilityNotify
// subscription.
type durabilityWaiter struct {
	seqNum base.SeqNum
	// ch is buffered so that resolving the waiter never blocks.
	ch           chan error
	subscription bool
	// index is the waiter's position in durabilityWaiters, or -1 once the
	// waiter has been removed.
	index int
}

// durabilityWaiters is a min-heap of waiters ordered by sequence number.
type durabilityWaiters []*durabilityWaiter

var _ heap.Interface = (*durabilityWaiters)(nil)

func (h durabilityWaiters) Len() int           { return len(h) }
func (h durabilityWaiters) Less(i, j int) bool { return h[i].seqNum < h[j].seqNum }

func (h durabilityWaiters) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *durabilityWaiters) Push(x any) {
	w := x.(*durabilityWaiter)
	w.index = len(*h)
	*h = append(*h, w)
}

func (h *durabilityWaiters) Pop() any {
	old := *h
	n := len(old)
	w := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	w.index = -1
	return w
}
