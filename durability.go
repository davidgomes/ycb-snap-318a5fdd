// Copyright 2026 The LevelDB-Go and Pebble Authors. All rights reserved. Use
// of this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package pebble

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/crlib/crtime"
	"github.com/cockroachdb/errors"
	"github.com/cockroachdb/pebble/internal/base"
	"github.com/cockroachdb/pebble/wal"
)

// How many completed durability jobs are remembered. Older job IDs return an
// error whose message contains "expired". IDs that were never allocated, and
// zero, return an error whose message contains "unknown".
const durabilityJobHistory = 1024

// Maximum number of DurabilityNotify subscriptions that have not yet fired.
const durabilityNotifyLimit = 1024

// DurabilityStats is a point-in-time snapshot of WAL durability tracking.
// Every field is zero before any Sync commit completes.
type DurabilityStats struct {
	// HighestDurableSeqNum is the highest sequence number known to be durable.
	// Sequence numbers are inclusive: a batch covering [seq, seq+count) advances
	// this to seq+count-1.
	HighestDurableSeqNum base.SeqNum
	// FirstErr is the first WAL sync error observed since Open. It is sticky.
	FirstErr error
	// PendingWaiters is the number of goroutines currently blocked in the
	// WaitFor* durability APIs.
	PendingWaiters int64
	// TotalDurableCommits is the number of Sync commits whose WAL sync succeeded.
	TotalDurableCommits uint64
	// TotalFailedCommits is the number of Sync commits whose WAL sync failed.
	TotalFailedCommits uint64
	// CumulativeSyncDuration is the sum of per-commit WAL sync phase durations.
	CumulativeSyncDuration time.Duration
	// MaxSyncDuration is the longest single WAL sync phase duration observed.
	MaxSyncDuration time.Duration
}

type durWaitKind int8

const (
	durWaitSeq durWaitKind = iota
	durWaitJob
)

type durWaiter struct {
	kind    durWaitKind
	seq     base.SeqNum
	job     int
	ch      chan error
	once    sync.Once
	counted bool
}

func (w *durWaiter) deliver(err error, t *durabilityTracker) {
	w.once.Do(func() {
		if w.counted {
			t.pending--
			w.counted = false
		}
		w.ch <- err
	})
}

type jobState struct {
	id   int
	done bool
	err  error
}

type durSub struct {
	seq base.SeqNum
	ch  chan error
}

// durabilityTracker records which sequence numbers have reached stable storage
// and wakes waiters. It is independent of whether BatchDurable is configured.
type durabilityTracker struct {
	mu sync.Mutex

	nextJob   int
	jobs      map[int]*jobState
	completed []int

	highest       base.SeqNum
	anySuccess    bool
	firstErr      error
	failedThrough base.SeqNum
	failErr       error

	closed   bool
	closeErr error

	seqWaiters []*durWaiter
	jobWaiters map[int][]*durWaiter
	subs       []*durSub
	subCount   int
	pending    int64

	totalDurable uint64
	totalFailed  uint64
	cumSync      time.Duration
	maxSync      time.Duration
}

func newDurabilityTracker() *durabilityTracker {
	return &durabilityTracker{
		nextJob:    1,
		jobs:       make(map[int]*jobState),
		jobWaiters: make(map[int][]*durWaiter),
	}
}

func (t *durabilityTracker) allocJob() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	id := t.nextJob
	t.nextJob++
	t.jobs[id] = &jobState{id: id}
	return id
}

func (t *durabilityTracker) beginCommit(d *DB, b *Batch) *durableCommit {
	seq := b.SeqNum()
	count := b.Count()
	var high base.SeqNum
	if count > 0 {
		high = seq + base.SeqNum(count) - 1
	}
	dc := &durableCommit{
		db:            d,
		jobID:         t.allocJob(),
		seqNum:        seq,
		highSeq:       high,
		keyCount:      count,
		batchSize:     len(b.data),
		correlationID: b.commitCorrelationID,
		syncCh:        make(chan error, 1),
		syncStart:     crtime.NowMono(),
	}
	return dc
}

// durableCommit is the state for one in-flight Sync commit.
type durableCommit struct {
	db            *DB
	jobID         int
	seqNum        base.SeqNum
	highSeq       base.SeqNum
	keyCount      uint32
	batchSize     int
	correlationID uint64
	applyDur      time.Duration
	syncStart     crtime.Mono
	syncNanos     atomic.Int64
	syncCh        chan error
	syncOnce      sync.Once
	finishOnce    sync.Once
}

func (dc *durableCommit) onSync(err error) {
	dc.syncOnce.Do(func() {
		dur := dc.syncStart.Elapsed()
		if dur < 0 {
			dur = 0
		}
		dc.syncNanos.Store(int64(dur))
		dc.syncCh <- err
	})
}

func (d *DB) syncRecordOpts(b *Batch, syncWG *sync.WaitGroup, syncErr *error) wal.SyncOptions {
	opts := wal.SyncOptions{Done: syncWG, Err: syncErr}
	if syncWG == nil || !b.reportDurability || d.durability == nil {
		return opts
	}
	if b.durable == nil {
		b.durable = d.durability.beginCommit(d, b)
	}
	opts.OnSync = b.durable.onSync
	return opts
}

func (d *DB) finishDurable(dc *durableCommit) {
	dc.finishOnce.Do(func() {
		syncErr := <-dc.syncCh
		applyDur := dc.applyDur
		syncDur := time.Duration(dc.syncNanos.Load())
		if syncErr == nil {
			// Successful Sync commits report positive measured durations. A
			// sub-nanosecond apply or sync still counts as positive.
			if applyDur <= 0 {
				applyDur = time.Nanosecond
			}
			if syncDur <= 0 {
				syncDur = time.Nanosecond
			}
		}
		info := BatchDurableInfo{
			JobID:         dc.jobID,
			SeqNum:        dc.seqNum,
			Err:           syncErr,
			ApplyDuration: applyDur,
			SyncDuration:  syncDur,
			CorrelationID: dc.correlationID,
			BatchSize:     dc.batchSize,
			KeyCount:      dc.keyCount,
		}
		if d.durability != nil {
			d.durability.record(dc, syncErr, syncDur)
		}
		if syncErr == nil && d.trackDurableMetrics {
			d.durableCommitCount.Add(1)
			d.durableCommitNanos.Add(int64(syncDur))
		}
		if d.opts.EventListener != nil && d.opts.EventListener.BatchDurable != nil {
			d.opts.EventListener.BatchDurable(info)
		}
	})
}

func (t *durabilityTracker) record(dc *durableCommit, syncErr error, syncDur time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	js := t.jobs[dc.jobID]
	if js == nil {
		js = &jobState{id: dc.jobID}
		t.jobs[dc.jobID] = js
	}
	js.done = true
	js.err = syncErr
	t.completed = append(t.completed, dc.jobID)
	for len(t.completed) > durabilityJobHistory {
		old := t.completed[0]
		t.completed = t.completed[1:]
		delete(t.jobs, old)
	}

	if syncErr != nil {
		t.totalFailed++
		if t.firstErr == nil {
			t.firstErr = syncErr
		}
		if dc.highSeq > t.failedThrough {
			t.failedThrough = dc.highSeq
			t.failErr = t.firstErr
		}
	} else {
		t.totalDurable++
		t.anySuccess = true
		if dc.highSeq > t.highest {
			t.highest = dc.highSeq
		}
		if t.failedThrough > 0 && t.highest >= t.failedThrough {
			t.failedThrough = 0
			t.failErr = nil
		}
	}
	t.cumSync += syncDur
	if syncDur > t.maxSync {
		t.maxSync = syncDur
	}
	t.wakeLocked()
}

func (t *durabilityTracker) closeDB(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	if err == nil {
		err = ErrClosed
	}
	t.closeErr = err
	t.wakeLocked()
}

func (t *durabilityTracker) outcomeSeqLocked(seq base.SeqNum) (error, bool) {
	// A sequence number of zero means "any Sync commit has become durable".
	if seq == 0 && t.anySuccess && !t.closed {
		return nil, true
	}
	if t.closed {
		return t.closeErr, true
	}
	if seq == 0 {
		if t.firstErr != nil {
			return t.firstErr, true
		}
		return nil, false
	}
	if seq <= t.highest {
		return nil, true
	}
	if t.failedThrough >= seq && t.failErr != nil {
		return t.failErr, true
	}
	return nil, false
}

func (t *durabilityTracker) outcomeJobLocked(id int) (error, bool) {
	if id == 0 || id >= t.nextJob {
		return errors.Newf("pebble: unknown durability job %d", id), true
	}
	js := t.jobs[id]
	if js == nil {
		return errors.Newf("pebble: durability job %d expired", id), true
	}
	if !js.done {
		if t.closed {
			return t.closeErr, true
		}
		return nil, false
	}
	return js.err, true
}

func (t *durabilityTracker) outcomeForWaiterLocked(w *durWaiter) (error, bool) {
	if w.kind == durWaitJob {
		return t.outcomeJobLocked(w.job)
	}
	return t.outcomeSeqLocked(w.seq)
}

func (t *durabilityTracker) wakeLocked() {
	waiters := t.seqWaiters
	t.seqWaiters = nil
	for _, w := range waiters {
		if err, ok := t.outcomeSeqLocked(w.seq); ok {
			w.deliver(err, t)
			continue
		}
		t.seqWaiters = append(t.seqWaiters, w)
	}
	for id, ws := range t.jobWaiters {
		err, ok := t.outcomeJobLocked(id)
		if !ok {
			continue
		}
		for _, w := range ws {
			w.deliver(err, t)
		}
		delete(t.jobWaiters, id)
	}
	subs := t.subs
	t.subs = nil
	t.subCount = 0
	for _, s := range subs {
		if err, ok := t.outcomeSeqLocked(s.seq); ok {
			s.ch <- err
			continue
		}
		t.subs = append(t.subs, s)
		t.subCount++
	}
}

func (t *durabilityTracker) removeWaiterLocked(w *durWaiter) {
	if w.kind == durWaitJob {
		ws := t.jobWaiters[w.job]
		for i, x := range ws {
			if x != w {
				continue
			}
			t.jobWaiters[w.job] = append(ws[:i], ws[i+1:]...)
			if len(t.jobWaiters[w.job]) == 0 {
				delete(t.jobWaiters, w.job)
			}
			return
		}
		return
	}
	for i, x := range t.seqWaiters {
		if x != w {
			continue
		}
		t.seqWaiters = append(t.seqWaiters[:i], t.seqWaiters[i+1:]...)
		return
	}
}

func (t *durabilityTracker) cancelWaiter(w *durWaiter, ctxErr error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if out, ok := t.outcomeForWaiterLocked(w); ok {
		// Durability and close errors take precedence over cancellation.
		w.deliver(out, t)
		t.removeWaiterLocked(w)
		return out
	}
	t.removeWaiterLocked(w)
	if w.counted {
		w.counted = false
		t.pending--
	}
	w.once.Do(func() {})
	return ctxErr
}

func (t *durabilityTracker) waitSeq(ctx context.Context, seq base.SeqNum) error {
	if err := ctx.Err(); err != nil {
		t.mu.Lock()
		out, ok := t.outcomeSeqLocked(seq)
		t.mu.Unlock()
		if ok {
			return out
		}
		return err
	}
	w := &durWaiter{kind: durWaitSeq, seq: seq, ch: make(chan error, 1)}
	t.mu.Lock()
	if out, ok := t.outcomeSeqLocked(seq); ok {
		t.mu.Unlock()
		return out
	}
	w.counted = true
	t.pending++
	t.seqWaiters = append(t.seqWaiters, w)
	t.mu.Unlock()

	select {
	case err := <-w.ch:
		return err
	case <-ctx.Done():
		return t.cancelWaiter(w, ctx.Err())
	}
}

func (t *durabilityTracker) waitJob(ctx context.Context, id int) error {
	if err := ctx.Err(); err != nil {
		t.mu.Lock()
		out, ok := t.outcomeJobLocked(id)
		t.mu.Unlock()
		if ok {
			return out
		}
		return err
	}
	w := &durWaiter{kind: durWaitJob, job: id, ch: make(chan error, 1)}
	t.mu.Lock()
	if out, ok := t.outcomeJobLocked(id); ok {
		t.mu.Unlock()
		return out
	}
	w.counted = true
	t.pending++
	t.jobWaiters[id] = append(t.jobWaiters[id], w)
	t.mu.Unlock()

	select {
	case err := <-w.ch:
		return err
	case <-ctx.Done():
		return t.cancelWaiter(w, ctx.Err())
	}
}

func (t *durabilityTracker) notify(seq base.SeqNum, ch chan error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err, ok := t.outcomeSeqLocked(seq); ok {
		ch <- err
		return
	}
	if t.subCount >= durabilityNotifyLimit {
		ch <- errors.New("pebble: durability notify subscription limit exceeded")
		return
	}
	t.subs = append(t.subs, &durSub{seq: seq, ch: ch})
	t.subCount++
}

func (t *durabilityTracker) stats() DurabilityStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return DurabilityStats{
		HighestDurableSeqNum:   t.highest,
		FirstErr:               t.firstErr,
		PendingWaiters:         t.pending,
		TotalDurableCommits:    t.totalDurable,
		TotalFailedCommits:     t.totalFailed,
		CumulativeSyncDuration: t.cumSync,
		MaxSyncDuration:        t.maxSync,
	}
}

func (t *durabilityTracker) state() (base.SeqNum, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.highest, t.firstErr
}

// WaitForDurability blocks until seqNum is durable. A zero seqNum succeeds
// after any successful Sync commit. The call returns an error if the WAL sync
// covering seqNum failed or the DB is closed.
func (d *DB) WaitForDurability(seqNum base.SeqNum) error {
	return d.WaitForDurabilityContext(context.Background(), seqNum)
}

// WaitForDurabilityContext is WaitForDurability with a context. A durability
// or close error takes precedence over context cancellation.
func (d *DB) WaitForDurabilityContext(ctx context.Context, seqNum base.SeqNum) error {
	if d.opts.DisableWAL {
		return nil
	}
	return d.durability.waitSeq(ctx, seqNum)
}

// WaitForDurabilityBatch blocks until every sequence number in seqNums is
// durable. A nil or empty slice returns nil.
func (d *DB) WaitForDurabilityBatch(seqNums []base.SeqNum) error {
	return d.WaitForDurabilityBatchContext(context.Background(), seqNums)
}

// WaitForDurabilityBatchContext is WaitForDurabilityBatch with a context. A
// durability or close error takes precedence over context cancellation.
func (d *DB) WaitForDurabilityBatchContext(ctx context.Context, seqNums []base.SeqNum) error {
	if len(seqNums) == 0 || d.opts.DisableWAL {
		return nil
	}
	var maxSeq base.SeqNum
	for _, seq := range seqNums {
		d.durability.mu.Lock()
		err, done := d.durability.outcomeSeqLocked(seq)
		d.durability.mu.Unlock()
		if done && err != nil {
			return err
		}
		if seq > maxSeq {
			maxSeq = seq
		}
	}
	return d.WaitForDurabilityContext(ctx, maxSeq)
}

// WaitForJobDurability blocks until the Sync commit identified by jobID (the
// BatchDurableInfo.JobID) finishes its WAL sync. A zero or never-seen job ID
// returns an error containing "unknown". A job that has fallen out of the
// retention window returns an error containing "expired".
func (d *DB) WaitForJobDurability(jobID int) error {
	return d.WaitForJobDurabilityContext(context.Background(), jobID)
}

// WaitForJobDurabilityContext is WaitForJobDurability with a context. A
// durability or close error takes precedence over context cancellation.
func (d *DB) WaitForJobDurabilityContext(ctx context.Context, jobID int) error {
	if d.opts.DisableWAL {
		return nil
	}
	return d.durability.waitJob(ctx, jobID)
}

// DurableState returns the highest durable sequence number and the first WAL
// sync error latched since Open.
func (d *DB) DurableState() (base.SeqNum, error) {
	if d.durability == nil {
		return 0, nil
	}
	return d.durability.state()
}

// DurabilityNotify returns a buffered channel that receives nil once seqNum is
// durable, or a non-nil error if the WAL sync fails or the DB closes. The
// channel is pre-filled when the result is already known. Outstanding
// subscriptions are bounded; callers beyond the limit receive a pre-filled
// channel containing a non-nil error. A zero seqNum completes after any
// successful Sync commit.
func (d *DB) DurabilityNotify(seqNum base.SeqNum) <-chan error {
	ch := make(chan error, 1)
	if d.opts.DisableWAL {
		ch <- nil
		return ch
	}
	d.durability.notify(seqNum, ch)
	return ch
}

// DurabilityStats returns a snapshot of durability tracking. PendingWaiters
// counts goroutines blocked inside the WaitFor* APIs.
func (d *DB) DurabilityStats() DurabilityStats {
	if d.durability == nil {
		return DurabilityStats{}
	}
	return d.durability.stats()
}
