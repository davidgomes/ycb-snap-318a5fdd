// Copyright 2026 The LevelDB-Go and Pebble Authors. All rights reserved. Use
// of this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package pebble

import (
	"container/heap"
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/crlib/crtime"
	"github.com/cockroachdb/errors"
	"github.com/cockroachdb/pebble/internal/base"
	"github.com/cockroachdb/redact"
)

// ErrDurabilityJobExpired is returned by DB.WaitForJobDurability when the job
// ID is older than the bounded window of retained durability jobs.
var ErrDurabilityJobExpired = errors.New("pebble: durability job expired")

// ErrDurabilityJobUnknown is returned by DB.WaitForJobDurability when the job
// ID was never assigned to a sync commit.
var ErrDurabilityJobUnknown = errors.New("pebble: unknown durability job")

// ErrDurabilityNotifyLimit is delivered on the channel returned by
// DB.DurabilityNotify when too many subscriptions are outstanding.
var ErrDurabilityNotifyLimit = errors.New("pebble: too many outstanding durability notifications")

const (
	// durabilityJobRetention is the number of most recent sync commit jobs
	// that can be looked up by DB.WaitForJobDurability.
	durabilityJobRetention = 1024
	// maxDurabilityNotifySubscriptions bounds the number of unresolved
	// channels handed out by DB.DurabilityNotify.
	maxDurabilityNotifySubscriptions = 1 << 14
)

// BatchDurableInfo contains the info for a BatchDurable event.
type BatchDurableInfo struct {
	// JobID identifies the sync commit. It can be passed to
	// DB.WaitForJobDurability.
	JobID int
	// SeqNum is the sequence number assigned to the first entry in the batch.
	SeqNum base.SeqNum
	// Err is non-nil if the WAL sync failed.
	Err error
	// ApplyDuration is the time from the start of the commit until the batch
	// was applied to the memtable.
	ApplyDuration time.Duration
	// SyncDuration is the time from when the batch was written to the WAL
	// until the WAL sync covering it completed.
	SyncDuration time.Duration
	// CorrelationID is WriteOptions.CommitCorrelationID of the commit.
	CorrelationID uint64
	// BatchSize is the size of the encoded batch in bytes.
	BatchSize int
	// KeyCount is the number of entries in the batch.
	KeyCount uint32
}

func (i BatchDurableInfo) String() string {
	return redact.StringWithoutMarkers(i)
}

// SafeFormat implements redact.SafeFormatter.
func (i BatchDurableInfo) SafeFormat(w redact.SafePrinter, _ rune) {
	if i.Err != nil {
		w.Printf("[JOB %d] batch at seqnum %s failed to become durable: %s",
			redact.Safe(i.JobID), i.SeqNum, i.Err)
		return
	}
	w.Printf("[JOB %d] batch at seqnum %s durable (%d bytes, %d keys), apply %.3fms, sync %.3fms",
		redact.Safe(i.JobID), i.SeqNum, redact.Safe(i.BatchSize), redact.Safe(i.KeyCount),
		redact.Safe(float64(i.ApplyDuration)/float64(time.Millisecond)),
		redact.Safe(float64(i.SyncDuration)/float64(time.Millisecond)))
}

// DurabilityStats is a snapshot of the durability state of a DB.
type DurabilityStats struct {
	// HighestDurableSeqNum is the highest sequence number known to be durable
	// in the WAL.
	HighestDurableSeqNum base.SeqNum
	// FirstErr is the first WAL sync error observed by a sync commit.
	FirstErr error
	// PendingWaiters is the number of goroutines currently blocked in the
	// durability wait APIs.
	PendingWaiters int64
	// TotalDurableCommits is the number of sync commits that became durable.
	TotalDurableCommits uint64
	// TotalFailedCommits is the number of sync commits whose WAL sync failed.
	TotalFailedCommits uint64
	// CumulativeSyncDuration is the sum of SyncDuration across durable sync
	// commits.
	CumulativeSyncDuration time.Duration
	// MaxSyncDuration is the largest SyncDuration across durable sync
	// commits.
	MaxSyncDuration time.Duration
}

// noopBatchDurable is the default EventListener.BatchDurable. It is used as a
// sentinel to detect whether BatchDurable was configured.
func noopBatchDurable(BatchDurableInfo) {}

func isBatchDurableConfigured(fn func(BatchDurableInfo)) bool {
	return fn != nil &&
		reflect.ValueOf(fn).Pointer() != reflect.ValueOf(noopBatchDurable).Pointer()
}

// batchDurability is the per-batch state used to report a sync commit to the
// durabilityTracker. It is reset along with the rest of batchInternal.
type batchDurability struct {
	// tracker is set when the commit requested a WAL sync.
	tracker *durabilityTracker
	// jobID is non-zero once the batch has been written to the WAL and
	// registered with the tracker, and until the commit has been reported.
	jobID         int
	seqNum        base.SeqNum
	correlationID uint64
	batchSize     int
	keyCount      uint32
	startTime     crtime.Mono
	writtenTime   crtime.Mono
	appliedTime   crtime.Mono
}

func (bd *batchDurability) markApplied() {
	if bd.jobID != 0 {
		bd.appliedTime = crtime.NowMono()
	}
}

type durabilityWaiter struct {
	seqNum base.SeqNum
	ch     chan error
	// index is the position in durabilityWaiterHeap, or -1 once resolved or
	// removed.
	index int
	// notify is true for DurabilityNotify subscriptions, which count against
	// maxDurabilityNotifySubscriptions.
	notify bool
}

type durabilityWaiterHeap []*durabilityWaiter

var _ heap.Interface = (*durabilityWaiterHeap)(nil)

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

type durabilityJob struct {
	id int
	// highestSeqNum is the highest sequence number that is durable once this
	// job's WAL sync completes.
	highestSeqNum base.SeqNum
	done          bool
	err           error
}

// durabilityTracker tracks the durability of sync commits and serves the
// DB's durability wait APIs.
//
// Lock ordering: durabilityTracker.mu may be acquired while holding
// commitPipeline.mu or DB.mu, but not the other way around.
type durabilityTracker struct {
	walDisabled bool
	// listener is the configured EventListener.BatchDurable, or nil.
	listener func(BatchDurableInfo)

	pendingWaiters atomic.Int64

	mu struct {
		sync.Mutex
		durableSeqNum base.SeqNum
		firstErr      error
		closedErr     error
		waiters       durabilityWaiterHeap
		numNotify     int
		lastJobID     int
		jobs          [durabilityJobRetention]durabilityJob

		totalDurable    uint64
		totalFailed     uint64
		cumSyncDuration time.Duration
		maxSyncDuration time.Duration

		// Only accumulated when listener is non-nil; surfaced in Metrics.
		metricsCount        uint64
		metricsSyncDuration time.Duration
	}
}

func (t *durabilityTracker) init(opts *Options) {
	t.walDisabled = opts.DisableWAL
	if opts.EventListener != nil && isBatchDurableConfigured(opts.EventListener.BatchDurable) {
		t.listener = opts.EventListener.BatchDurable
	}
}

// register assigns a job ID to a sync commit that has just been written to
// the WAL. Called with commitPipeline.mu held, so job IDs are assigned in
// sequence number order.
func (t *durabilityTracker) register(b *Batch, batchSize int) {
	bd := &b.durable
	bd.seqNum = b.SeqNum()
	bd.batchSize = batchSize
	bd.keyCount = b.Count()
	bd.writtenTime = crtime.NowMono()

	t.mu.Lock()
	t.mu.lastJobID++
	bd.jobID = t.mu.lastJobID
	t.mu.jobs[bd.jobID%durabilityJobRetention] = durabilityJob{
		id:            bd.jobID,
		highestSeqNum: bd.seqNum + base.SeqNum(bd.keyCount) - 1,
	}
	t.mu.Unlock()
}

// complete reports the outcome of the WAL sync for a registered sync commit.
// It is a no-op if the batch has already been reported.
func (t *durabilityTracker) complete(b *Batch, err error) {
	bd := &b.durable
	if bd.jobID == 0 {
		return
	}
	now := crtime.NowMono()
	if bd.appliedTime == 0 {
		bd.appliedTime = now
	}
	// Clamp to 1ns so that coarse clocks still yield positive durations.
	info := BatchDurableInfo{
		JobID:         bd.jobID,
		SeqNum:        bd.seqNum,
		Err:           err,
		ApplyDuration: max(bd.appliedTime.Sub(bd.startTime), time.Nanosecond),
		SyncDuration:  max(now.Sub(bd.writtenTime), time.Nanosecond),
		CorrelationID: bd.correlationID,
		BatchSize:     bd.batchSize,
		KeyCount:      bd.keyCount,
	}
	highest := bd.seqNum + base.SeqNum(bd.keyCount) - 1
	bd.jobID = 0

	t.mu.Lock()
	if job := &t.mu.jobs[info.JobID%durabilityJobRetention]; job.id == info.JobID {
		job.done = true
		job.err = err
	}
	if err == nil {
		t.mu.totalDurable++
		t.mu.cumSyncDuration += info.SyncDuration
		t.mu.maxSyncDuration = max(t.mu.maxSyncDuration, info.SyncDuration)
		if t.listener != nil {
			t.mu.metricsCount++
			t.mu.metricsSyncDuration += info.SyncDuration
		}
		if highest > t.mu.durableSeqNum {
			t.mu.durableSeqNum = highest
		}
		for len(t.mu.waiters) > 0 && t.mu.waiters[0].seqNum <= t.mu.durableSeqNum {
			t.resolveLocked(heap.Pop(&t.mu.waiters).(*durabilityWaiter), nil)
		}
	} else {
		t.mu.totalFailed++
		if t.mu.firstErr == nil {
			t.mu.firstErr = err
		}
		t.resolveAllLocked(t.mu.firstErr)
	}
	t.mu.Unlock()

	if t.listener != nil {
		t.listener(info)
	}
}

// close fails all outstanding waiters and causes future waits on
// sequence numbers that are not yet durable to fail with ErrClosed.
func (t *durabilityTracker) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.mu.closedErr = ErrClosed
	t.resolveAllLocked(t.mu.closedErr)
}

func (t *durabilityTracker) resolveLocked(w *durabilityWaiter, err error) {
	w.ch <- err
	if w.notify {
		t.mu.numNotify--
	}
}

func (t *durabilityTracker) resolveAllLocked(err error) {
	for _, w := range t.mu.waiters {
		w.index = -1
		t.resolveLocked(w, err)
	}
	clear(t.mu.waiters)
	t.mu.waiters = t.mu.waiters[:0]
}

// checkLocked returns done=true if a wait for seqNum can be resolved without
// blocking, along with the result.
func (t *durabilityTracker) checkLocked(seqNum base.SeqNum) (done bool, err error) {
	switch {
	case seqNum <= t.mu.durableSeqNum:
		return true, nil
	case t.mu.firstErr != nil:
		return true, t.mu.firstErr
	case t.mu.closedErr != nil:
		return true, t.mu.closedErr
	}
	return false, nil
}

func (t *durabilityTracker) wait(ctx context.Context, seqNum base.SeqNum) error {
	if t.walDisabled {
		return nil
	}
	t.mu.Lock()
	if done, err := t.checkLocked(seqNum); done {
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

func (t *durabilityTracker) waitForJob(ctx context.Context, jobID int) error {
	if t.walDisabled {
		return nil
	}
	t.mu.Lock()
	if jobID <= 0 || jobID > t.mu.lastJobID {
		t.mu.Unlock()
		return errors.Wrapf(ErrDurabilityJobUnknown, "job %d", errors.Safe(jobID))
	}
	if t.mu.lastJobID-jobID >= durabilityJobRetention {
		t.mu.Unlock()
		return errors.Wrapf(ErrDurabilityJobExpired, "job %d", errors.Safe(jobID))
	}
	job := t.mu.jobs[jobID%durabilityJobRetention]
	t.mu.Unlock()
	if job.done {
		return job.err
	}
	return t.wait(ctx, job.highestSeqNum)
}

func (t *durabilityTracker) notify(seqNum base.SeqNum) <-chan error {
	ch := make(chan error, 1)
	if t.walDisabled {
		ch <- nil
		return ch
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if done, err := t.checkLocked(seqNum); done {
		ch <- err
		return ch
	}
	if t.mu.numNotify >= maxDurabilityNotifySubscriptions {
		ch <- ErrDurabilityNotifyLimit
		return ch
	}
	t.mu.numNotify++
	heap.Push(&t.mu.waiters, &durabilityWaiter{seqNum: seqNum, ch: ch, notify: true})
	return ch
}

func (t *durabilityTracker) state() (base.SeqNum, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.mu.durableSeqNum, t.mu.firstErr
}

func (t *durabilityTracker) stats() DurabilityStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return DurabilityStats{
		HighestDurableSeqNum:   t.mu.durableSeqNum,
		FirstErr:               t.mu.firstErr,
		PendingWaiters:         t.pendingWaiters.Load(),
		TotalDurableCommits:    t.mu.totalDurable,
		TotalFailedCommits:     t.mu.totalFailed,
		CumulativeSyncDuration: t.mu.cumSyncDuration,
		MaxSyncDuration:        t.mu.maxSyncDuration,
	}
}

func (t *durabilityTracker) metrics() (count uint64, syncDuration time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.mu.metricsCount, t.mu.metricsSyncDuration
}

// WaitForDurability blocks until seqNum is durable in the WAL, a WAL sync
// fails, or the DB is closed. Only sync commits advance durability, so a
// sequence number written by a non-sync commit becomes durable once a later
// sync commit completes. A zero seqNum is always satisfied. Returns nil
// immediately if the WAL is disabled.
func (d *DB) WaitForDurability(seqNum base.SeqNum) error {
	return d.durability.wait(context.Background(), seqNum)
}

// WaitForDurabilityContext is like WaitForDurability, but also returns
// ctx.Err() if ctx is done first. A WAL sync error or DB close takes
// precedence over context cancellation.
func (d *DB) WaitForDurabilityContext(ctx context.Context, seqNum base.SeqNum) error {
	return d.durability.wait(ctx, seqNum)
}

// WaitForDurabilityBatch blocks until every sequence number in seqNums is
// durable. See WaitForDurability.
func (d *DB) WaitForDurabilityBatch(seqNums []base.SeqNum) error {
	return d.WaitForDurabilityBatchContext(context.Background(), seqNums)
}

// WaitForDurabilityBatchContext is like WaitForDurabilityBatch, but also
// returns ctx.Err() if ctx is done first. See WaitForDurabilityContext.
func (d *DB) WaitForDurabilityBatchContext(ctx context.Context, seqNums []base.SeqNum) error {
	if len(seqNums) == 0 {
		return nil
	}
	// Durability advances monotonically through the WAL, so waiting for the
	// largest sequence number suffices.
	var highest base.SeqNum
	for _, s := range seqNums {
		highest = max(highest, s)
	}
	return d.durability.wait(ctx, highest)
}

// WaitForJobDurability blocks until the sync commit identified by jobID
// (BatchDurableInfo.JobID) is durable. Only the most recent sync commits are
// retained: older job IDs return an error wrapping ErrDurabilityJobExpired,
// and zero or never-assigned job IDs return an error wrapping
// ErrDurabilityJobUnknown. Returns nil immediately if the WAL is disabled.
func (d *DB) WaitForJobDurability(jobID int) error {
	return d.durability.waitForJob(context.Background(), jobID)
}

// WaitForJobDurabilityContext is like WaitForJobDurability, but also returns
// ctx.Err() if ctx is done first. See WaitForDurabilityContext.
func (d *DB) WaitForJobDurabilityContext(ctx context.Context, jobID int) error {
	return d.durability.waitForJob(ctx, jobID)
}

// DurableState returns the highest sequence number known to be durable and
// the first WAL sync error observed by a sync commit.
func (d *DB) DurableState() (base.SeqNum, error) {
	return d.durability.state()
}

// DurabilityNotify returns a buffered channel that receives exactly one
// value: nil once seqNum is durable, or a non-nil error if a WAL sync fails or
// the DB is closed first. The number of outstanding subscriptions is bounded;
// beyond the bound the channel immediately holds ErrDurabilityNotifyLimit.
// If the WAL is disabled, the channel immediately holds nil.
func (d *DB) DurabilityNotify(seqNum base.SeqNum) <-chan error {
	return d.durability.notify(seqNum)
}

// DurabilityStats returns a snapshot of the DB's durability state.
func (d *DB) DurabilityStats() DurabilityStats {
	return d.durability.stats()
}
