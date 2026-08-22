// Copyright 2026 The LevelDB-Go and Pebble Authors. All rights reserved. Use
// of this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package pebble

import (
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
	// DurabilityJobRetention is the number of recent Sync-commit job IDs
	// retained for WaitForJobDurability.
	DurabilityJobRetention = 1024
	// MaxDurabilityNotify is the maximum number of outstanding DurabilityNotify
	// subscriptions.
	MaxDurabilityNotify = 1024
)

var (
	// ErrUnknownDurabilityJob is returned by WaitForJobDurability for a job ID
	// that has never been assigned (including zero).
	ErrUnknownDurabilityJob = errors.New("pebble: unknown durability job")
	// ErrExpiredDurabilityJob is returned by WaitForJobDurability for a job ID
	// that has fallen out of the bounded retention window.
	ErrExpiredDurabilityJob = errors.New("pebble: expired durability job")
	// ErrDurabilityNotifyLimit is returned via DurabilityNotify when too many
	// subscriptions are already outstanding.
	ErrDurabilityNotifyLimit = errors.New("pebble: durability notify subscription limit exceeded")
)

// DurabilityStats is a snapshot of Sync-commit durability tracking.
type DurabilityStats struct {
	HighestDurableSeqNum   base.SeqNum
	FirstErr               error
	PendingWaiters         int64
	TotalDurableCommits    uint64
	TotalFailedCommits     uint64
	CumulativeSyncDuration time.Duration
	MaxSyncDuration        time.Duration
}

type syncHook struct {
	wg  sync.WaitGroup
	err error
}

// inflightDurable joins apply and WAL-sync completion for a single Sync commit.
type inflightDurable struct {
	jobID         int
	seqNum        base.SeqNum
	keyCount      uint32
	batchSize     int
	correlationID uint64
	syncStart     crtime.Mono
	hook          *syncHook
	origWG        *sync.WaitGroup
	origErr       *error

	applyCh       chan struct{}
	applyOnce     sync.Once
	applyDuration time.Duration
	applyErr      error
}

func (r *inflightDurable) finishApply(d time.Duration, err error) {
	r.applyOnce.Do(func() {
		r.applyDuration = d
		r.applyErr = err
		close(r.applyCh)
	})
}

func (r *inflightDurable) waitApply() (time.Duration, error) {
	<-r.applyCh
	return r.applyDuration, r.applyErr
}

type durableJob struct {
	id   int
	err  error
	done bool
}

type seqWaiter struct {
	seqNums []base.SeqNum
	ch      chan error
}

type jobWaiter struct {
	jobID int
	ch    chan error
}

type notifySub struct {
	seqNum base.SeqNum
	ch     chan error
}

type durabilityTracker struct {
	db                     *DB
	batchDurableConfigured bool
	disableWAL             bool

	pendingWaiters atomic.Int64
	metricCount    atomic.Uint64
	metricDuration atomic.Int64

	mu         sync.Mutex
	closed     bool
	highest    base.SeqNum
	firstErr   error
	nextJobID  int
	jobs       map[int]*durableJob
	seqWaiters []seqWaiter
	jobWaiters []jobWaiter
	notifySubs []notifySub

	totalDurable uint64
	totalFailed  uint64
	cumSync      time.Duration
	maxSync      time.Duration
}

func newDurabilityTracker(d *DB, batchDurableConfigured, disableWAL bool) *durabilityTracker {
	return &durabilityTracker{
		db:                     d,
		batchDurableConfigured: batchDurableConfigured,
		disableWAL:             disableWAL,
		nextJobID:              1,
		jobs:                   make(map[int]*durableJob),
	}
}

func (t *durabilityTracker) intercept(
	b *Batch, origWG *sync.WaitGroup, origErr *error,
) (*sync.WaitGroup, *error) {
	if t == nil || origWG == nil || t.disableWAL || b.durableInflight != nil {
		return origWG, origErr
	}
	// directWrite (flushable ingest) passes a stack-local WaitGroup and never
	// goes through commitApply. Only intercept regular Commit sync waits.
	if origWG != &b.commit && origWG != &b.fsyncWait {
		return origWG, origErr
	}
	rec := t.beginJob(b)
	hook := &syncHook{}
	hook.wg.Add(1)
	rec.hook = hook
	rec.origWG = origWG
	rec.origErr = origErr
	rec.syncStart = crtime.NowMono()
	b.durableInflight = rec
	go t.watchSync(rec)
	return &hook.wg, &hook.err
}

func (t *durabilityTracker) beginJob(b *Batch) *inflightDurable {
	t.mu.Lock()
	id := t.nextJobID
	t.nextJobID++
	t.jobs[id] = &durableJob{id: id}
	t.expireJobsLocked()
	t.mu.Unlock()

	return &inflightDurable{
		jobID:         id,
		seqNum:        b.SeqNum(),
		keyCount:      b.Count(),
		batchSize:     b.Len(),
		correlationID: b.commitCorrelationID,
		applyCh:       make(chan struct{}),
	}
}

func (t *durabilityTracker) expireJobsLocked() {
	minKeep := t.nextJobID - DurabilityJobRetention
	if minKeep < 1 {
		return
	}
	for id, j := range t.jobs {
		if id < minKeep && j.done {
			delete(t.jobs, id)
		}
	}
}

func (t *durabilityTracker) watchSync(rec *inflightDurable) {
	rec.hook.wg.Wait()
	err := rec.hook.err
	syncDur := rec.syncStart.Elapsed()
	applyDur, applyErr := rec.waitApply()
	if err == nil {
		err = applyErr
	}
	if err == nil {
		if applyDur <= 0 {
			applyDur = time.Nanosecond
		}
		if syncDur <= 0 {
			syncDur = time.Nanosecond
		}
	}
	info := BatchDurableInfo{
		JobID:         rec.jobID,
		SeqNum:        rec.seqNum,
		Err:           err,
		ApplyDuration: applyDur,
		SyncDuration:  syncDur,
		CorrelationID: rec.correlationID,
		BatchSize:     rec.batchSize,
		KeyCount:      rec.keyCount,
	}
	t.recordAndNotify(info)
	if t.db != nil && t.db.opts != nil && t.db.opts.EventListener != nil {
		t.db.opts.EventListener.BatchDurable(info)
	}
	if rec.origErr != nil {
		*rec.origErr = err
	}
	if rec.origWG != nil {
		rec.origWG.Done()
	}
}

func (t *durabilityTracker) recordAndNotify(info BatchDurableInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if j := t.jobs[info.JobID]; j != nil {
		j.done = true
		j.err = info.Err
	}
	t.expireJobsLocked()

	if info.Err != nil {
		t.totalFailed++
		if t.firstErr == nil {
			t.firstErr = info.Err
		}
	} else {
		t.totalDurable++
		last := info.SeqNum
		if info.KeyCount > 0 {
			last = info.SeqNum + base.SeqNum(info.KeyCount) - 1
		}
		if last > t.highest {
			t.highest = last
		}
		t.cumSync += info.SyncDuration
		if info.SyncDuration > t.maxSync {
			t.maxSync = info.SyncDuration
		}
		if t.batchDurableConfigured {
			t.metricCount.Add(1)
			t.metricDuration.Add(int64(info.SyncDuration))
		}
	}

	if t.closed {
		return
	}
	t.wakeLocked()
}

func (t *durabilityTracker) seqDurableLocked(seqNum base.SeqNum) bool {
	if seqNum == 0 {
		return t.totalDurable > 0
	}
	return t.highest >= seqNum
}

func (t *durabilityTracker) seqsDurableLocked(seqNums []base.SeqNum) bool {
	for _, s := range seqNums {
		if !t.seqDurableLocked(s) {
			return false
		}
	}
	return true
}

func (t *durabilityTracker) wakeLocked() {
	{
		remain := t.seqWaiters[:0]
		for _, w := range t.seqWaiters {
			if t.closed {
				w.ch <- ErrClosed
				continue
			}
			if t.seqsDurableLocked(w.seqNums) {
				w.ch <- nil
				continue
			}
			if t.firstErr != nil {
				w.ch <- t.firstErr
				continue
			}
			remain = append(remain, w)
		}
		t.seqWaiters = remain
	}
	{
		remain := t.jobWaiters[:0]
		for _, w := range t.jobWaiters {
			if t.closed {
				w.ch <- ErrClosed
				continue
			}
			if j := t.jobs[w.jobID]; j != nil && j.done {
				w.ch <- j.err
				continue
			}
			if t.firstErr != nil && (t.jobs[w.jobID] == nil || !t.jobs[w.jobID].done) {
				// Still in-flight; wait for this job unless the DB is closed.
				remain = append(remain, w)
				continue
			}
			remain = append(remain, w)
		}
		t.jobWaiters = remain
	}
	{
		remain := t.notifySubs[:0]
		for _, s := range t.notifySubs {
			if t.closed {
				s.ch <- ErrClosed
				continue
			}
			if t.seqDurableLocked(s.seqNum) {
				s.ch <- nil
				continue
			}
			if t.firstErr != nil {
				s.ch <- t.firstErr
				continue
			}
			remain = append(remain, s)
		}
		t.notifySubs = remain
	}
}

func (t *durabilityTracker) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	if t.firstErr == nil {
		t.firstErr = ErrClosed
	}
	t.wakeLocked()
}

func (t *durabilityTracker) metrics() (uint64, time.Duration) {
	return t.metricCount.Load(), time.Duration(t.metricDuration.Load())
}

func (t *durabilityTracker) stats() DurabilityStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return DurabilityStats{
		HighestDurableSeqNum:   t.highest,
		FirstErr:               t.firstErr,
		PendingWaiters:         t.pendingWaiters.Load(),
		TotalDurableCommits:    t.totalDurable,
		TotalFailedCommits:     t.totalFailed,
		CumulativeSyncDuration: t.cumSync,
		MaxSyncDuration:        t.maxSync,
	}
}

func (t *durabilityTracker) durableState() (base.SeqNum, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.highest, t.firstErr
}

func (t *durabilityTracker) waitOn(ctx context.Context, ch <-chan error, remove func()) error {
	defer t.pendingWaiters.Add(-1)
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		if remove != nil {
			remove()
		}
		select {
		case err := <-ch:
			return err
		default:
			return ctx.Err()
		}
	}
}

func (t *durabilityTracker) removeSeqWaiterLocked(ch chan error) {
	remain := t.seqWaiters[:0]
	for _, w := range t.seqWaiters {
		if w.ch != ch {
			remain = append(remain, w)
		}
	}
	t.seqWaiters = remain
}

func (t *durabilityTracker) removeJobWaiterLocked(ch chan error) {
	remain := t.jobWaiters[:0]
	for _, w := range t.jobWaiters {
		if w.ch != ch {
			remain = append(remain, w)
		}
	}
	t.jobWaiters = remain
}

func (t *durabilityTracker) waitForSeqs(ctx context.Context, seqNums []base.SeqNum) error {
	if t.disableWAL {
		return nil
	}
	t.mu.Lock()
	if t.seqsDurableLocked(seqNums) {
		t.mu.Unlock()
		return nil
	}
	if t.closed {
		t.mu.Unlock()
		return ErrClosed
	}
	if t.firstErr != nil {
		t.mu.Unlock()
		return t.firstErr
	}
	ch := make(chan error, 1)
	t.seqWaiters = append(t.seqWaiters, seqWaiter{seqNums: slices.Clone(seqNums), ch: ch})
	t.pendingWaiters.Add(1)
	t.mu.Unlock()
	return t.waitOn(ctx, ch, func() {
		t.mu.Lock()
		t.removeSeqWaiterLocked(ch)
		t.mu.Unlock()
	})
}

func (t *durabilityTracker) waitForJob(ctx context.Context, jobID int) error {
	if t.disableWAL {
		return nil
	}
	if jobID <= 0 {
		return ErrUnknownDurabilityJob
	}
	t.mu.Lock()
	if jobID >= t.nextJobID {
		t.mu.Unlock()
		return ErrUnknownDurabilityJob
	}
	if jobID < t.nextJobID-DurabilityJobRetention {
		t.mu.Unlock()
		return ErrExpiredDurabilityJob
	}
	j := t.jobs[jobID]
	if j == nil {
		t.mu.Unlock()
		return ErrExpiredDurabilityJob
	}
	if j.done {
		err := j.err
		t.mu.Unlock()
		return err
	}
	if t.closed {
		t.mu.Unlock()
		return ErrClosed
	}
	ch := make(chan error, 1)
	t.jobWaiters = append(t.jobWaiters, jobWaiter{jobID: jobID, ch: ch})
	t.pendingWaiters.Add(1)
	t.mu.Unlock()
	return t.waitOn(ctx, ch, func() {
		t.mu.Lock()
		t.removeJobWaiterLocked(ch)
		t.mu.Unlock()
	})
}

func (t *durabilityTracker) notify(seqNum base.SeqNum) <-chan error {
	ch := make(chan error, 1)
	if t.disableWAL {
		ch <- nil
		return ch
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.seqDurableLocked(seqNum) {
		ch <- nil
		return ch
	}
	if t.closed {
		ch <- ErrClosed
		return ch
	}
	if t.firstErr != nil {
		ch <- t.firstErr
		return ch
	}
	if len(t.notifySubs) >= MaxDurabilityNotify {
		ch <- ErrDurabilityNotifyLimit
		return ch
	}
	t.notifySubs = append(t.notifySubs, notifySub{seqNum: seqNum, ch: ch})
	return ch
}

// WaitForDurability blocks until seqNum is durable. A zero seqNum succeeds
// after any successful Sync commit.
func (d *DB) WaitForDurability(seqNum base.SeqNum) error {
	return d.WaitForDurabilityContext(context.Background(), seqNum)
}

// WaitForDurabilityContext is the context-aware variant of WaitForDurability.
// A durability or close error takes precedence over context cancellation.
func (d *DB) WaitForDurabilityContext(ctx context.Context, seqNum base.SeqNum) error {
	return d.durability.waitForSeqs(ctx, []base.SeqNum{seqNum})
}

// WaitForDurabilityBatch blocks until every sequence number in seqNums is
// durable. A nil or empty slice returns nil.
func (d *DB) WaitForDurabilityBatch(seqNums []base.SeqNum) error {
	return d.WaitForDurabilityBatchContext(context.Background(), seqNums)
}

// WaitForDurabilityBatchContext is the context-aware variant of
// WaitForDurabilityBatch. A durability or close error takes precedence over
// context cancellation.
func (d *DB) WaitForDurabilityBatchContext(ctx context.Context, seqNums []base.SeqNum) error {
	if len(seqNums) == 0 {
		return nil
	}
	return d.durability.waitForSeqs(ctx, seqNums)
}

// WaitForJobDurability blocks until the Sync commit identified by jobID has
// become durable (or failed). Never-seen and zero IDs return an error whose
// message contains "unknown". IDs outside the retention window return an
// error whose message contains "expired".
func (d *DB) WaitForJobDurability(jobID int) error {
	return d.WaitForJobDurabilityContext(context.Background(), jobID)
}

// WaitForJobDurabilityContext is the context-aware variant of
// WaitForJobDurability. A durability or close error takes precedence over
// context cancellation.
func (d *DB) WaitForJobDurabilityContext(ctx context.Context, jobID int) error {
	return d.durability.waitForJob(ctx, jobID)
}

// DurableState returns the highest durable sequence number and the first
// latched durability (or close) error.
func (d *DB) DurableState() (base.SeqNum, error) {
	return d.durability.durableState()
}

// DurabilityNotify returns a pre-filled receive-only channel that delivers
// nil once seqNum is durable, or a non-nil error on WAL sync failure or DB
// close. Outstanding subscriptions are bounded; excess callers receive a
// pre-filled channel with an immediate non-nil error.
func (d *DB) DurabilityNotify(seqNum base.SeqNum) <-chan error {
	return d.durability.notify(seqNum)
}

// DurabilityStats returns a snapshot of durability tracking state.
func (d *DB) DurabilityStats() DurabilityStats {
	return d.durability.stats()
}
