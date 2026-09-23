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
)

const (
	// durabilityJobRetention bounds the number of completed BatchDurable jobs
	// retained for WaitForJobDurability.
	durabilityJobRetention = 4096
	// maxDurabilityNotifySubs bounds outstanding DurabilityNotify channels.
	maxDurabilityNotifySubs = 16384
)

// ErrDurabilityJobExpired is returned by WaitForJobDurability for a job ID
// that has aged out of the retention window.
var ErrDurabilityJobExpired = errors.New("pebble: durability job expired")

// ErrDurabilityJobUnknown is returned by WaitForJobDurability for a job ID
// that was never issued.
var ErrDurabilityJobUnknown = errors.New("pebble: unknown durability job")

// errDurabilityNotifyLimit is delivered by DurabilityNotify when too many
// subscriptions are outstanding.
var errDurabilityNotifyLimit = errors.New("pebble: too many outstanding durability subscriptions")

// DurabilityStats is a snapshot of durability tracking state.
type DurabilityStats struct {
	HighestDurableSeqNum   base.SeqNum
	FirstErr               error
	PendingWaiters         int64
	TotalDurableCommits    uint64
	TotalFailedCommits     uint64
	CumulativeSyncDuration time.Duration
	MaxSyncDuration        time.Duration
}

type durabilityWaiter struct {
	seq    base.SeqNum
	ch     chan error
	notify bool
}

type durabilityJob struct {
	id  int
	err error
}

// durabilityTracker records the durable prefix of the WAL and wakes waiters.
// The zero value is ready for use.
type durabilityTracker struct {
	nextJobID      atomic.Int64
	pendingWaiters atomic.Int64

	mu struct {
		sync.Mutex
		highest    base.SeqNum
		firstErr   error
		closedErr  error
		waiters    map[*durabilityWaiter]struct{}
		notifySubs int
		jobs       map[int]error
		jobRing    []int
		jobRingPos int
		stats      DurabilityStats
		// Metrics accumulated only when BatchDurable is configured.
		metricCount    uint64
		metricDuration time.Duration
	}
}

func (t *durabilityTracker) newJobID() int {
	return int(t.nextJobID.Add(1))
}

// record marks the batch ending at lastSeq as durable (or failed with err).
func (t *durabilityTracker) record(
	jobID int, lastSeq base.SeqNum, err error, syncDur time.Duration, trackMetrics bool,
) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err != nil {
		t.mu.stats.TotalFailedCommits++
		if t.mu.firstErr == nil {
			t.mu.firstErr = err
		}
	} else {
		t.mu.stats.TotalDurableCommits++
		if lastSeq > t.mu.highest {
			t.mu.highest = lastSeq
		}
	}
	t.mu.stats.CumulativeSyncDuration += syncDur
	if syncDur > t.mu.stats.MaxSyncDuration {
		t.mu.stats.MaxSyncDuration = syncDur
	}
	if trackMetrics {
		t.mu.metricCount++
		t.mu.metricDuration += syncDur
	}

	if t.mu.jobs == nil {
		t.mu.jobs = make(map[int]error)
		t.mu.jobRing = make([]int, durabilityJobRetention)
	}
	if old := t.mu.jobRing[t.mu.jobRingPos]; old != 0 {
		delete(t.mu.jobs, old)
	}
	t.mu.jobRing[t.mu.jobRingPos] = jobID
	t.mu.jobRingPos = (t.mu.jobRingPos + 1) % durabilityJobRetention
	t.mu.jobs[jobID] = err

	for w := range t.mu.waiters {
		if res, done := t.resolveLocked(w.seq); done {
			t.removeLocked(w)
			w.ch <- res
		}
	}
}

func (t *durabilityTracker) close(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.mu.closedErr != nil {
		return
	}
	t.mu.closedErr = err
	for w := range t.mu.waiters {
		t.removeLocked(w)
		w.ch <- err
	}
}

// resolveLocked reports whether seq is resolved and with which result.
func (t *durabilityTracker) resolveLocked(seq base.SeqNum) (error, bool) {
	if seq <= t.mu.highest {
		return nil, true
	}
	if t.mu.firstErr != nil {
		return t.mu.firstErr, true
	}
	if t.mu.closedErr != nil {
		return t.mu.closedErr, true
	}
	return nil, false
}

func (t *durabilityTracker) removeLocked(w *durabilityWaiter) {
	delete(t.mu.waiters, w)
	if w.notify {
		t.mu.notifySubs--
	}
}

// subscribe returns a buffered channel that receives the result for seq.
func (t *durabilityTracker) subscribe(seq base.SeqNum, notify bool) (*durabilityWaiter, error) {
	w := &durabilityWaiter{seq: seq, ch: make(chan error, 1), notify: notify}
	t.mu.Lock()
	defer t.mu.Unlock()
	if res, done := t.resolveLocked(seq); done {
		w.ch <- res
		return w, nil
	}
	if notify {
		if t.mu.notifySubs >= maxDurabilityNotifySubs {
			return nil, errDurabilityNotifyLimit
		}
		t.mu.notifySubs++
	}
	if t.mu.waiters == nil {
		t.mu.waiters = make(map[*durabilityWaiter]struct{})
	}
	t.mu.waiters[w] = struct{}{}
	return w, nil
}

func (t *durabilityTracker) wait(ctx context.Context, seq base.SeqNum) error {
	w, _ := t.subscribe(seq, false)
	select {
	case err := <-w.ch:
		return err
	default:
	}
	t.pendingWaiters.Add(1)
	defer t.pendingWaiters.Add(-1)
	select {
	case err := <-w.ch:
		return err
	case <-ctx.Done():
		t.mu.Lock()
		t.removeLocked(w)
		t.mu.Unlock()
		// Durability and close errors take precedence over cancellation.
		select {
		case err := <-w.ch:
			return err
		default:
		}
		return ctx.Err()
	}
}

func (d *DB) durabilityDisabled() bool {
	return d.opts.DisableWAL
}

// WaitForDurability blocks until seq is durable, the WAL sync fails, or the DB
// is closed.
func (d *DB) WaitForDurability(seq base.SeqNum) error {
	return d.WaitForDurabilityContext(context.Background(), seq)
}

// WaitForDurabilityContext is like WaitForDurability but honors ctx.
func (d *DB) WaitForDurabilityContext(ctx context.Context, seq base.SeqNum) error {
	if d.durabilityDisabled() {
		return nil
	}
	return d.durability.wait(ctx, seq)
}

// WaitForDurabilityBatch blocks until every sequence number in seqs is durable.
func (d *DB) WaitForDurabilityBatch(seqs []base.SeqNum) error {
	return d.WaitForDurabilityBatchContext(context.Background(), seqs)
}

// WaitForDurabilityBatchContext is like WaitForDurabilityBatch but honors ctx.
func (d *DB) WaitForDurabilityBatchContext(ctx context.Context, seqs []base.SeqNum) error {
	if len(seqs) == 0 || d.durabilityDisabled() {
		return nil
	}
	maxSeq := seqs[0]
	for _, s := range seqs[1:] {
		if s > maxSeq {
			maxSeq = s
		}
	}
	return d.durability.wait(ctx, maxSeq)
}

// WaitForJobDurability returns the result of the BatchDurable job with the
// given ID, blocking until it is known.
func (d *DB) WaitForJobDurability(jobID int) error {
	return d.WaitForJobDurabilityContext(context.Background(), jobID)
}

// WaitForJobDurabilityContext is like WaitForJobDurability but honors ctx.
func (d *DB) WaitForJobDurabilityContext(ctx context.Context, jobID int) error {
	if d.durabilityDisabled() {
		return nil
	}
	t := &d.durability
	if jobID <= 0 || int64(jobID) > t.nextJobID.Load() {
		return errors.Wrapf(ErrDurabilityJobUnknown, "job %d", jobID)
	}
	t.mu.Lock()
	err, ok := t.mu.jobs[jobID]
	closedErr := t.mu.closedErr
	t.mu.Unlock()
	if ok {
		return err
	}
	// Job IDs are issued when the sync completes and recorded immediately
	// after, so an issued ID missing from the map was evicted, unless the
	// record is still in flight.
	if int64(jobID) > t.nextJobID.Load()-durabilityJobRetention {
		t.pendingWaiters.Add(1)
		defer t.pendingWaiters.Add(-1)
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			t.mu.Lock()
			err, ok = t.mu.jobs[jobID]
			closedErr = t.mu.closedErr
			t.mu.Unlock()
			if ok {
				return err
			}
			if closedErr != nil {
				return closedErr
			}
			time.Sleep(time.Microsecond)
			if int64(jobID) <= t.nextJobID.Load()-durabilityJobRetention {
				break
			}
		}
	}
	return errors.Wrapf(ErrDurabilityJobExpired, "job %d", jobID)
}

// DurableState returns the highest durable sequence number and the first
// latched durability error.
func (d *DB) DurableState() (base.SeqNum, error) {
	d.durability.mu.Lock()
	defer d.durability.mu.Unlock()
	return d.durability.mu.highest, d.durability.mu.firstErr
}

// DurabilityNotify returns a channel that receives nil once seq is durable, or
// a non-nil error on WAL sync failure or DB close.
func (d *DB) DurabilityNotify(seq base.SeqNum) <-chan error {
	if d.durabilityDisabled() {
		ch := make(chan error, 1)
		ch <- nil
		return ch
	}
	w, err := d.durability.subscribe(seq, true)
	if err != nil {
		ch := make(chan error, 1)
		ch <- err
		return ch
	}
	return w.ch
}

// DurabilityStats returns a snapshot of durability tracking statistics.
func (d *DB) DurabilityStats() DurabilityStats {
	t := &d.durability
	t.mu.Lock()
	s := t.mu.stats
	s.HighestDurableSeqNum = t.mu.highest
	s.FirstErr = t.mu.firstErr
	t.mu.Unlock()
	s.PendingWaiters = t.pendingWaiters.Load()
	return s
}

type pendingDurable struct {
	db       *DB
	start    crtime.Mono
	applyDur time.Duration
	corrID   uint64
	size     int
	count    uint32
}

func (p *pendingDurable) info(err error, syncDur time.Duration) BatchDurableInfo {
	return BatchDurableInfo{
		Err:           err,
		ApplyDuration: p.applyDur,
		SyncDuration:  syncDur,
		CorrelationID: p.corrID,
		BatchSize:     p.size,
		KeyCount:      p.count,
	}
}

// batchDurable records durability of a synced batch and fires the
// BatchDurable event. info.JobID and info.SeqNum are filled in here.
func (d *DB) batchDurable(b *Batch, info BatchDurableInfo) {
	info.SeqNum = b.SeqNum()
	lastSeq := info.SeqNum
	if info.KeyCount > 0 {
		lastSeq += base.SeqNum(info.KeyCount) - 1
	}
	if info.ApplyDuration <= 0 {
		info.ApplyDuration = 1
	}
	if info.SyncDuration <= 0 {
		info.SyncDuration = 1
	}
	info.JobID = d.durability.newJobID()
	cb := d.opts.EventListener.BatchDurable
	configured := batchDurableConfigured(cb)
	d.durability.record(info.JobID, lastSeq, info.Err, info.SyncDuration, configured)
	if configured {
		cb(info)
	}
}
