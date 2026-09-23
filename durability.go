// Copyright 2026 The LevelDB-Go and Pebble Authors. All rights reserved. Use
// of this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package pebble

import (
	"context"
	"reflect"
	"sync"
	"time"

	"github.com/cockroachdb/crlib/crtime"
	"github.com/cockroachdb/errors"
	"github.com/cockroachdb/pebble/internal/base"
	"github.com/cockroachdb/pebble/wal"
)

const (
	// durabilityJobRetain is the number of completed Sync-commit job results
	// kept for WaitForJobDurability. Older jobs return an expired error.
	durabilityJobRetain = 1024
	// durabilityNotifyLimit bounds outstanding DurabilityNotify subscriptions
	// that have not yet been delivered.
	durabilityNotifyLimit = 1024
)

var (
	errDurabilityJobUnknown  = errors.New("pebble: unknown durability job")
	errDurabilityJobExpired  = errors.New("pebble: expired durability job")
	errDurabilityNotifyLimit = errors.New("pebble: durability notify subscription limit exceeded")
)

// DurabilityStats is a snapshot of WAL durability tracking.
type DurabilityStats struct {
	// HighestDurableSeqNum is the highest sequence number known to be durable.
	// It is zero before any successful Sync commit. A Sync commit that contains
	// keys advances this to the last sequence number in that batch.
	HighestDurableSeqNum base.SeqNum
	// FirstErr is the first WAL sync error observed, if any.
	FirstErr error
	// PendingWaiters is the number of goroutines blocked in the durability
	// wait APIs.
	PendingWaiters int64
	// TotalDurableCommits is the number of Sync commits whose WAL sync succeeded.
	TotalDurableCommits uint64
	// TotalFailedCommits is the number of Sync commits whose WAL sync failed.
	TotalFailedCommits uint64
	// CumulativeSyncDuration is the sum of WAL sync phase durations for
	// successful Sync commits.
	CumulativeSyncDuration time.Duration
	// MaxSyncDuration is the longest WAL sync phase among successful Sync commits.
	MaxSyncDuration time.Duration
}

// batchDurableConfigured reports whether fn is a user-configured BatchDurable
// callback. The default no-op installed by EnsureDefaults does not count.
func batchDurableConfigured(fn func(BatchDurableInfo)) bool {
	if fn == nil {
		return false
	}
	return reflect.ValueOf(fn).Pointer() != reflect.ValueOf(noopBatchDurable).Pointer()
}

type durabilityWaitKind uint8

const (
	durabilityWaitSeq durabilityWaitKind = iota
	durabilityWaitBatch
	durabilityWaitJob
)

type durabilityWaiter struct {
	kind     durabilityWaitKind
	seq      base.SeqNum
	seqs     []base.SeqNum
	jobID    int
	ch       chan error
	signaled bool
}

type durabilitySub struct {
	seq base.SeqNum
	ch  chan error
}

type durabilityJob struct {
	done bool
	err  error
}

type durableCommit struct {
	jobID         int
	seqNum        base.SeqNum
	endSeq        base.SeqNum
	countPositive bool
	err           error
	applyDur      time.Duration
	syncDur       time.Duration
	correlation   uint64
	batchSize     int
	keyCount      uint32
}

// durabilityTracker records which sequence numbers have been synced to the WAL
// and wakes waiters. It is independent of EventListener.BatchDurable; the
// callback is optional.
type durabilityTracker struct {
	mu sync.Mutex

	highest    base.SeqNum
	anySuccess bool
	firstErr   error
	closed     bool
	closeErr   error

	nextJobID  int
	jobs       map[int]*durabilityJob
	retainIDs  [durabilityJobRetain]int
	retainLen  int
	retainNext int

	waiters        []*durabilityWaiter
	pendingWaiters int64

	notifies            []durabilitySub
	outstandingNotifies int

	totalDurable uint64
	totalFailed  uint64
	cumSync      time.Duration
	maxSync      time.Duration

	metricsOn   bool
	metricCount uint64
	metricDur   time.Duration
}

func (t *durabilityTracker) init(metricsOn bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.jobs == nil {
		t.jobs = make(map[int]*durabilityJob)
	}
	t.metricsOn = metricsOn
	if t.closeErr == nil {
		t.closeErr = ErrClosed
	}
}

func (t *durabilityTracker) beginJob() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.jobs == nil {
		t.jobs = make(map[int]*durabilityJob)
	}
	t.nextJobID++
	id := t.nextJobID
	t.jobs[id] = &durabilityJob{}
	return id
}

func (t *durabilityTracker) finish(c durableCommit) BatchDurableInfo {
	t.mu.Lock()
	info := BatchDurableInfo{
		JobID:         c.jobID,
		SeqNum:        c.seqNum,
		Err:           c.err,
		ApplyDuration: c.applyDur,
		SyncDuration:  c.syncDur,
		CorrelationID: c.correlation,
		BatchSize:     c.batchSize,
		KeyCount:      c.keyCount,
	}
	job := t.jobs[c.jobID]
	if job == nil {
		job = &durabilityJob{}
		t.jobs[c.jobID] = job
	}
	job.done = true
	job.err = c.err
	t.retainJobLocked(c.jobID)

	if c.err != nil {
		if t.firstErr == nil {
			t.firstErr = c.err
		}
		t.totalFailed++
	} else {
		if c.countPositive && c.endSeq > t.highest {
			t.highest = c.endSeq
		}
		t.anySuccess = true
		t.totalDurable++
		t.cumSync += c.syncDur
		if c.syncDur > t.maxSync {
			t.maxSync = c.syncDur
		}
		if t.metricsOn {
			t.metricCount++
			t.metricDur += c.syncDur
		}
	}
	t.signalLocked()
	t.mu.Unlock()
	return info
}

func (t *durabilityTracker) retainJobLocked(id int) {
	if t.retainLen == durabilityJobRetain {
		old := t.retainIDs[t.retainNext]
		delete(t.jobs, old)
	} else {
		t.retainLen++
	}
	t.retainIDs[t.retainNext] = id
	t.retainNext++
	if t.retainNext == durabilityJobRetain {
		t.retainNext = 0
	}
}

func (t *durabilityTracker) onClose(err error) {
	if err == nil {
		err = ErrClosed
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	t.closeErr = err
	for _, w := range t.waiters {
		t.sendWaiterLocked(w, err)
	}
	t.waiters = nil
	for _, n := range t.notifies {
		n.ch <- err
		t.outstandingNotifies--
	}
	t.notifies = nil
}

func (t *durabilityTracker) sendWaiterLocked(w *durabilityWaiter, err error) {
	if w.signaled {
		return
	}
	w.signaled = true
	w.ch <- err
	if t.pendingWaiters > 0 {
		t.pendingWaiters--
	}
}

// seqResultLocked reports whether a wait for seq can complete immediately.
// A positive seq is durable when it is less than or equal to the highest
// synced sequence number. Zero waits until any Sync commit has succeeded, and
// returns the latched sync error when every Sync commit so far has failed.
func (t *durabilityTracker) seqResultLocked(seq base.SeqNum) (error, bool) {
	if seq == 0 {
		if t.anySuccess {
			return nil, true
		}
		if t.firstErr != nil {
			return t.firstErr, true
		}
		if t.closed {
			return t.closeErr, true
		}
		return nil, false
	}
	if t.anySuccess && seq <= t.highest {
		return nil, true
	}
	// A latched sync error fails waits for sequence numbers that are not yet
	// durable so callers do not block after the WAL sync has already failed.
	if t.firstErr != nil {
		return t.firstErr, true
	}
	if t.closed {
		return t.closeErr, true
	}
	return nil, false
}

func (t *durabilityTracker) jobResultLocked(id int) (error, bool) {
	if id <= 0 || id > t.nextJobID {
		return errDurabilityJobUnknown, true
	}
	job, ok := t.jobs[id]
	if !ok {
		return errDurabilityJobExpired, true
	}
	if !job.done {
		if t.closed {
			return t.closeErr, true
		}
		return nil, false
	}
	return job.err, true
}

func (t *durabilityTracker) waiterResultLocked(w *durabilityWaiter) (error, bool) {
	switch w.kind {
	case durabilityWaitSeq:
		return t.seqResultLocked(w.seq)
	case durabilityWaitBatch:
		var first error
		for _, seq := range w.seqs {
			err, ok := t.seqResultLocked(seq)
			if !ok {
				return nil, false
			}
			if err != nil && first == nil {
				first = err
			}
		}
		return first, true
	case durabilityWaitJob:
		return t.jobResultLocked(w.jobID)
	default:
		return errors.AssertionFailedf("pebble: unknown durability waiter"), true
	}
}

func (t *durabilityTracker) signalLocked() {
	remaining := t.waiters[:0]
	for _, w := range t.waiters {
		if w.signaled {
			continue
		}
		err, ok := t.waiterResultLocked(w)
		if !ok {
			remaining = append(remaining, w)
			continue
		}
		t.sendWaiterLocked(w, err)
	}
	t.waiters = remaining

	nrem := t.notifies[:0]
	for _, n := range t.notifies {
		err, ok := t.seqResultLocked(n.seq)
		if !ok {
			nrem = append(nrem, n)
			continue
		}
		n.ch <- err
		t.outstandingNotifies--
	}
	t.notifies = nrem
}

func (t *durabilityTracker) removeWaiterLocked(w *durabilityWaiter) {
	if w.signaled {
		return
	}
	for i, cur := range t.waiters {
		if cur == w {
			t.waiters = append(t.waiters[:i], t.waiters[i+1:]...)
			if t.pendingWaiters > 0 {
				t.pendingWaiters--
			}
			return
		}
	}
}

func (t *durabilityTracker) wait(ctx context.Context, w *durabilityWaiter) error {
	if ctx == nil {
		ctx = context.Background()
	}
	w.ch = make(chan error, 1)

	t.mu.Lock()
	if err, ok := t.waiterResultLocked(w); ok {
		t.mu.Unlock()
		return err
	}
	if err := ctx.Err(); err != nil {
		// A durability or close result takes precedence over cancellation.
		if err, ok := t.waiterResultLocked(w); ok {
			t.mu.Unlock()
			return err
		}
		t.mu.Unlock()
		return err
	}
	t.waiters = append(t.waiters, w)
	t.pendingWaiters++
	t.mu.Unlock()

	select {
	case err := <-w.ch:
		return err
	case <-ctx.Done():
	}

	t.mu.Lock()
	if w.signaled {
		t.mu.Unlock()
		return <-w.ch
	}
	t.removeWaiterLocked(w)
	if err, ok := t.waiterResultLocked(w); ok {
		t.mu.Unlock()
		return err
	}
	// Cancellation lost to a sync failure that has not yet been delivered
	// through waiterResultLocked (sequence waits block again after a failure
	// unless the error was explicitly delivered). Prefer a latched sync error
	// or close over the context error.
	if w.kind != durabilityWaitJob && t.firstErr != nil {
		err := t.firstErr
		t.mu.Unlock()
		return err
	}
	if t.closed {
		err := t.closeErr
		t.mu.Unlock()
		return err
	}
	t.mu.Unlock()
	return ctx.Err()
}

func (t *durabilityTracker) notify(seq base.SeqNum) <-chan error {
	ch := make(chan error, 1)
	t.mu.Lock()
	defer t.mu.Unlock()
	if err, ok := t.seqResultLocked(seq); ok {
		ch <- err
		return ch
	}
	if t.outstandingNotifies >= durabilityNotifyLimit {
		ch <- errDurabilityNotifyLimit
		return ch
	}
	t.outstandingNotifies++
	t.notifies = append(t.notifies, durabilitySub{seq: seq, ch: ch})
	return ch
}

func (t *durabilityTracker) state() (base.SeqNum, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.highest, t.firstErr
}

func (t *durabilityTracker) stats() DurabilityStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return DurabilityStats{
		HighestDurableSeqNum:   t.highest,
		FirstErr:               t.firstErr,
		PendingWaiters:         t.pendingWaiters,
		TotalDurableCommits:    t.totalDurable,
		TotalFailedCommits:     t.totalFailed,
		CumulativeSyncDuration: t.cumSync,
		MaxSyncDuration:        t.maxSync,
	}
}

func (t *durabilityTracker) metricSnapshot() (count uint64, dur time.Duration, enabled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.metricCount, t.metricDur, t.metricsOn
}

func prefilledErr(err error) <-chan error {
	ch := make(chan error, 1)
	ch <- err
	return ch
}

// WaitForDurability blocks until seqNum is durable. A zero seqNum succeeds
// after any successful Sync commit. The returned error is a WAL sync error or
// ErrClosed when the DB closes before the sequence number becomes durable.
func (d *DB) WaitForDurability(seqNum base.SeqNum) error {
	return d.WaitForDurabilityContext(context.Background(), seqNum)
}

// WaitForDurabilityContext is like WaitForDurability. A durability or close
// error takes precedence over context cancellation.
func (d *DB) WaitForDurabilityContext(ctx context.Context, seqNum base.SeqNum) error {
	if d.opts.DisableWAL {
		return nil
	}
	return d.durability.wait(ctx, &durabilityWaiter{kind: durabilityWaitSeq, seq: seqNum})
}

// WaitForDurabilityBatch blocks until every sequence number in seqNums is
// durable. A nil or empty slice returns nil.
func (d *DB) WaitForDurabilityBatch(seqNums []base.SeqNum) error {
	return d.WaitForDurabilityBatchContext(context.Background(), seqNums)
}

// WaitForDurabilityBatchContext is like WaitForDurabilityBatch. A durability
// or close error takes precedence over context cancellation.
func (d *DB) WaitForDurabilityBatchContext(ctx context.Context, seqNums []base.SeqNum) error {
	if len(seqNums) == 0 {
		return nil
	}
	if d.opts.DisableWAL {
		return nil
	}
	seqs := append([]base.SeqNum(nil), seqNums...)
	return d.durability.wait(ctx, &durabilityWaiter{kind: durabilityWaitBatch, seqs: seqs})
}

// WaitForJobDurability blocks until the Sync commit identified by jobID (from
// BatchDurableInfo.JobID) finishes its WAL sync. A zero or never-issued id
// returns an error whose message contains "unknown". An id that has fallen
// outside the retention window returns an error whose message contains
// "expired".
func (d *DB) WaitForJobDurability(jobID int) error {
	return d.WaitForJobDurabilityContext(context.Background(), jobID)
}

// WaitForJobDurabilityContext is like WaitForJobDurability. A durability or
// close error takes precedence over context cancellation.
func (d *DB) WaitForJobDurabilityContext(ctx context.Context, jobID int) error {
	if d.opts.DisableWAL {
		return nil
	}
	return d.durability.wait(ctx, &durabilityWaiter{kind: durabilityWaitJob, jobID: jobID})
}

// DurableState returns the highest durable sequence number and the first
// latched WAL sync error. Both are zero before any Sync commit.
func (d *DB) DurableState() (base.SeqNum, error) {
	if d.opts.DisableWAL {
		return 0, nil
	}
	return d.durability.state()
}

// DurabilityNotify returns a buffered channel that receives nil when seqNum is
// durable, or a non-nil error if the WAL sync fails or the DB closes first.
// The channel is pre-filled when the result is already known. Outstanding
// subscriptions are bounded; callers beyond the bound receive a pre-filled
// channel with a non-nil error. A zero seqNum completes after any successful
// Sync commit.
func (d *DB) DurabilityNotify(seqNum base.SeqNum) <-chan error {
	if d.opts.DisableWAL {
		return prefilledErr(nil)
	}
	return d.durability.notify(seqNum)
}

// DurabilityStats returns a snapshot of durability tracking. Every field is
// zero before any commits. PendingWaiters counts goroutines blocked in the
// wait APIs.
func (d *DB) DurabilityStats() DurabilityStats {
	if d.opts.DisableWAL {
		return DurabilityStats{}
	}
	return d.durability.stats()
}

// writeRecordSyncAware writes repr to the WAL. Sync commits install a relay
// that fires BatchDurable after the WAL sync completes, including on failure.
func (d *DB) writeRecordSyncAware(
	b *Batch, repr []byte, syncWG *sync.WaitGroup, syncErr *error,
) (int64, error) {
	if syncWG == nil || !b.trackSyncCommit || d.opts.DisableWAL {
		return d.mu.log.writer.WriteRecord(repr, wal.SyncOptions{Done: syncWG, Err: syncErr}, b)
	}
	syncStart := crtime.NowMono()
	proxy := &sync.WaitGroup{}
	proxy.Add(1)
	logicalOffset, err := d.mu.log.writer.WriteRecord(repr, wal.SyncOptions{Done: proxy, Err: syncErr}, b)
	if err != nil {
		return logicalOffset, err
	}
	d.launchDurableRelay(b, proxy, syncWG, syncErr, len(repr), syncStart)
	return logicalOffset, nil
}

func (d *DB) launchDurableRelay(
	b *Batch, proxy, syncWG *sync.WaitGroup, syncErr *error, batchSize int, syncStart crtime.Mono,
) {
	seq := b.SeqNum()
	count := b.Count()
	correlation := b.commitCorrelationID
	jobID := d.durability.beginJob()
	var listener func(BatchDurableInfo)
	if d.opts.EventListener != nil {
		listener = d.opts.EventListener.BatchDurable
	}
	go func() {
		defer syncWG.Done()
		proxy.Wait()
		var syncErrVal error
		if syncErr != nil {
			syncErrVal = *syncErr
		}
		applyDur := b.waitApplyDuration()
		syncDur := syncStart.Elapsed()
		if syncErrVal == nil {
			if applyDur <= 0 {
				applyDur = time.Nanosecond
			}
			if syncDur <= 0 {
				syncDur = time.Nanosecond
			}
		}
		endSeq := seq
		if count > 0 {
			endSeq = seq + base.SeqNum(count) - 1
		}
		info := d.durability.finish(durableCommit{
			jobID:         jobID,
			seqNum:        seq,
			endSeq:        endSeq,
			countPositive: count > 0,
			err:           syncErrVal,
			applyDur:      applyDur,
			syncDur:       syncDur,
			correlation:   correlation,
			batchSize:     batchSize,
			keyCount:      count,
		})
		if listener != nil {
			listener(info)
		}
	}()
}
