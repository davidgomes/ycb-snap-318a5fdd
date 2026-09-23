// Copyright 2026 The LevelDB-Go and Pebble Authors. All rights reserved. Use
// of this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package pebble

import (
	"context"
	"sync"
	"time"

	"github.com/cockroachdb/crlib/crtime"
	"github.com/cockroachdb/errors"
	"github.com/cockroachdb/pebble/internal/base"
)

const (
	// durabilityJobWindow is the number of recent sync-commit job IDs retained
	// for WaitForJobDurability. Older IDs return an error containing "expired".
	durabilityJobWindow = 64
	// durabilityMaxSubscriptions bounds outstanding DurabilityNotify
	// subscriptions. Additional callers receive an immediate error.
	durabilityMaxSubscriptions = 1024
)

var (
	errDurabilityJobUnknown        = errors.New("pebble: unknown durability job")
	errDurabilityJobExpired        = errors.New("pebble: expired durability job")
	errDurabilitySubscriptionLimit = errors.New("pebble: durability subscription limit exceeded")
)

// DurabilityStats is a point-in-time snapshot of WAL durability tracking.
type DurabilityStats struct {
	// HighestDurableSeqNum is the highest sequence number known to be durable.
	// It is zero before any successful sync commit.
	HighestDurableSeqNum base.SeqNum
	// FirstErr is the first WAL sync error observed since the DB was opened.
	FirstErr error
	// PendingWaiters is the number of goroutines currently blocked in the
	// durability wait APIs.
	PendingWaiters int64
	// TotalDurableCommits is the number of sync commits whose WAL sync succeeded.
	TotalDurableCommits uint64
	// TotalFailedCommits is the number of sync commits whose WAL sync failed.
	TotalFailedCommits uint64
	// CumulativeSyncDuration is the total wall-clock time spent in the WAL sync
	// phase across completed sync commits.
	CumulativeSyncDuration time.Duration
	// MaxSyncDuration is the longest single WAL sync phase observed.
	MaxSyncDuration time.Duration
}

// durabilityJob is one sync commit tracked for WaitForJobDurability.
type durabilityJob struct {
	id      int
	done    bool
	expired bool
	err     error
}

// durabilitySub is one outstanding DurabilityNotify subscription.
type durabilitySub struct {
	seq base.SeqNum
	ch  chan error
}

// durabilityCoordinator tracks the durable sequence-number watermark, job
// retention, and notify subscriptions.
type durabilityCoordinator struct {
	mu   sync.Mutex
	cond *sync.Cond

	disableWAL   bool
	trackMetrics bool
	closed       bool

	highest       base.SeqNum
	firstErr      error
	failedThrough base.SeqNum

	pendingWaiters int64

	totalDurable   uint64
	totalFailed    uint64
	cumulativeSync time.Duration
	maxSync        time.Duration

	metricCount uint64
	metricDur   time.Duration

	nextJob int
	jobs    []*durabilityJob

	subs            []durabilitySub
	outstandingSubs int
}

func (c *durabilityCoordinator) init(disableWAL, trackMetrics bool) {
	c.cond = sync.NewCond(&c.mu)
	c.disableWAL = disableWAL
	c.trackMetrics = trackMetrics
	c.jobs = make([]*durabilityJob, durabilityJobWindow)
}

func (c *durabilityCoordinator) shutdown() {
	if c == nil || c.cond == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	for _, s := range c.subs {
		s.ch <- ErrClosed
	}
	c.subs = nil
	c.outstandingSubs = 0
	c.cond.Broadcast()
}

func (c *durabilityCoordinator) allocJob() *durabilityJob {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextJob++
	id := c.nextJob
	j := &durabilityJob{id: id}
	idx := (id - 1) % durabilityJobWindow
	if old := c.jobs[idx]; old != nil {
		old.expired = true
		c.cond.Broadcast()
	}
	c.jobs[idx] = j
	return j
}

func (c *durabilityCoordinator) classifyJobLocked(id int) (*durabilityJob, error) {
	if id <= 0 || id > c.nextJob {
		return nil, errDurabilityJobUnknown
	}
	j := c.jobs[(id-1)%durabilityJobWindow]
	if j == nil || j.id != id || j.expired {
		return nil, errDurabilityJobExpired
	}
	return j, nil
}

// seqStatusLocked reports whether a wait or notification for seq is complete.
func (c *durabilityCoordinator) seqStatusLocked(seq base.SeqNum) (error, bool) {
	if seq == 0 {
		if c.totalDurable > 0 {
			return nil, true
		}
		if c.totalFailed > 0 {
			return c.firstErr, true
		}
		if c.closed {
			return ErrClosed, true
		}
		return nil, false
	}
	if c.totalDurable > 0 && c.highest >= seq {
		return nil, true
	}
	if c.failedThrough >= seq && c.firstErr != nil {
		return c.firstErr, true
	}
	if c.closed {
		return ErrClosed, true
	}
	return nil, false
}

func (c *durabilityCoordinator) deliverSubsLocked() {
	if len(c.subs) == 0 {
		return
	}
	remaining := c.subs[:0]
	for _, s := range c.subs {
		if err, done := c.seqStatusLocked(s.seq); done {
			s.ch <- err
			c.outstandingSubs--
			continue
		}
		remaining = append(remaining, s)
	}
	c.subs = remaining
}

func (c *durabilityCoordinator) complete(info BatchDurableInfo, job *durabilityJob, endSeq base.SeqNum) {
	c.mu.Lock()
	syncDur := info.SyncDuration
	c.cumulativeSync += syncDur
	if syncDur > c.maxSync {
		c.maxSync = syncDur
	}
	if info.Err != nil {
		if c.firstErr == nil {
			c.firstErr = info.Err
		}
		c.totalFailed++
		if endSeq > c.failedThrough {
			c.failedThrough = endSeq
		}
	} else {
		if endSeq > c.highest {
			c.highest = endSeq
		}
		c.totalDurable++
		if c.trackMetrics {
			c.metricCount++
			c.metricDur += syncDur
		}
	}
	if job != nil {
		job.done = true
		job.err = info.Err
	}
	c.deliverSubsLocked()
	c.cond.Broadcast()
	c.mu.Unlock()
}

func (c *durabilityCoordinator) commitMetrics() (uint64, time.Duration) {
	if c.cond == nil {
		return 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.metricCount, c.metricDur
}

func (c *durabilityCoordinator) stats() DurabilityStats {
	if c.cond == nil {
		return DurabilityStats{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return DurabilityStats{
		HighestDurableSeqNum:   c.highest,
		FirstErr:               c.firstErr,
		PendingWaiters:         c.pendingWaiters,
		TotalDurableCommits:    c.totalDurable,
		TotalFailedCommits:     c.totalFailed,
		CumulativeSyncDuration: c.cumulativeSync,
		MaxSyncDuration:        c.maxSync,
	}
}

func (c *durabilityCoordinator) state() (base.SeqNum, error) {
	if c.disableWAL || c.cond == nil {
		return 0, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.highest, c.firstErr
}

// wait blocks until status reports completion. The caller must not hold c.mu.
// Durability and close errors take precedence over context cancellation.
func (c *durabilityCoordinator) wait(ctx context.Context, status func() (error, bool)) error {
	if c.disableWAL {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err, done := status(); done {
		return err
	}
	c.pendingWaiters++
	defer func() { c.pendingWaiters-- }()

	stop := make(chan struct{})
	defer close(stop)
	if ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				c.mu.Lock()
				c.cond.Broadcast()
				c.mu.Unlock()
			case <-stop:
			}
		}()
	}
	for {
		if err, done := status(); done {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		c.cond.Wait()
	}
}

// durableCommit coordinates apply and WAL-sync completion for one sync commit
// so BatchDurable fires exactly once, after the sync, with both durations.
type durableCommit struct {
	db          *DB
	job         *durabilityJob
	seq         base.SeqNum
	count       uint32
	size        int
	correlation uint64
	syncStart   crtime.Mono

	mu        sync.Mutex
	applyDone bool
	syncDone  bool
	fired     bool
	applyDur  time.Duration
	syncDur   time.Duration
	syncErr   error
}

func (d *DB) maybeArmDurable(b *Batch, syncWG *sync.WaitGroup, repr []byte) func(error) {
	if b == nil || !b.trackDurability || syncWG == nil {
		return nil
	}
	if b.durable != nil {
		return b.durable.onSync
	}
	dc := &durableCommit{
		db:          d,
		seq:         b.SeqNum(),
		count:       b.Count(),
		size:        len(repr),
		correlation: b.correlationID,
		syncStart:   crtime.NowMono(),
	}
	dc.job = d.durability.allocJob()
	b.durable = dc
	return dc.onSync
}

func (dc *durableCommit) noteApply(dur time.Duration) {
	dc.mu.Lock()
	dc.applyDone = true
	dc.applyDur = dur
	fire := dc.syncDone && !dc.fired
	if fire {
		dc.fired = true
	}
	dc.mu.Unlock()
	if fire {
		dc.finish()
	}
}

func (dc *durableCommit) onSync(err error) {
	dur := dc.syncStart.Elapsed()
	dc.mu.Lock()
	if dc.syncDone {
		dc.mu.Unlock()
		return
	}
	dc.syncDone = true
	dc.syncErr = err
	dc.syncDur = dur
	fire := dc.applyDone && !dc.fired
	if fire {
		dc.fired = true
	}
	dc.mu.Unlock()
	if fire {
		dc.finish()
	}
}

func (dc *durableCommit) finish() {
	dc.mu.Lock()
	info := BatchDurableInfo{
		JobID:         dc.job.id,
		SeqNum:        dc.seq,
		Err:           dc.syncErr,
		ApplyDuration: dc.applyDur,
		SyncDuration:  dc.syncDur,
		CorrelationID: dc.correlation,
		BatchSize:     dc.size,
		KeyCount:      dc.count,
	}
	var endSeq base.SeqNum
	if dc.count == 0 {
		endSeq = dc.seq
	} else {
		endSeq = dc.seq + base.SeqNum(dc.count) - 1
	}
	dc.mu.Unlock()

	if info.Err == nil {
		// Successful sync commits report positive measured durations. A
		// sub-nanosecond phase still counts as measured time.
		if info.ApplyDuration <= 0 {
			info.ApplyDuration = time.Nanosecond
		}
		if info.SyncDuration <= 0 {
			info.SyncDuration = time.Nanosecond
		}
	}
	dc.db.durability.complete(info, dc.job, endSeq)
	if el := dc.db.opts.EventListener; el != nil && el.BatchDurable != nil {
		el.BatchDurable(info)
	}
}

// WaitForDurability blocks until seq is durable. A zero sequence number
// succeeds once any sync commit has become durable. The returned error is
// non-nil when the WAL sync covering seq fails or the DB is closed.
func (d *DB) WaitForDurability(seq base.SeqNum) error {
	return d.WaitForDurabilityContext(context.Background(), seq)
}

// WaitForDurabilityContext is like WaitForDurability. A durability or close
// error takes precedence over context cancellation.
func (d *DB) WaitForDurabilityContext(ctx context.Context, seq base.SeqNum) error {
	return d.durability.wait(ctx, func() (error, bool) {
		return d.durability.seqStatusLocked(seq)
	})
}

// WaitForDurabilityBatch blocks until every sequence number in seqs is
// durable. A nil or empty slice returns nil.
func (d *DB) WaitForDurabilityBatch(seqs []base.SeqNum) error {
	return d.WaitForDurabilityBatchContext(context.Background(), seqs)
}

// WaitForDurabilityBatchContext is like WaitForDurabilityBatch. A durability
// or close error takes precedence over context cancellation.
func (d *DB) WaitForDurabilityBatchContext(ctx context.Context, seqs []base.SeqNum) error {
	if len(seqs) == 0 {
		return nil
	}
	if d.durability.disableWAL {
		return nil
	}
	var maxSeq base.SeqNum
	for _, seq := range seqs {
		if seq > maxSeq {
			maxSeq = seq
		}
	}
	// Sequence numbers become durable in order, so the maximum covers the
	// rest. An all-zero slice waits for the first durable commit.
	return d.WaitForDurabilityContext(ctx, maxSeq)
}

// WaitForJobDurability blocks until the sync commit identified by jobID
// finishes its WAL sync. jobID is BatchDurableInfo.JobID. A zero or never-seen
// ID returns an error containing "unknown". An ID outside the retention window
// returns an error containing "expired".
func (d *DB) WaitForJobDurability(jobID int) error {
	return d.WaitForJobDurabilityContext(context.Background(), jobID)
}

// WaitForJobDurabilityContext is like WaitForJobDurability. A durability or
// close error takes precedence over context cancellation.
func (d *DB) WaitForJobDurabilityContext(ctx context.Context, jobID int) error {
	if d.durability.disableWAL {
		return nil
	}
	d.durability.mu.Lock()
	job, err := d.durability.classifyJobLocked(jobID)
	if err != nil {
		d.durability.mu.Unlock()
		return err
	}
	if job.done {
		err = job.err
		d.durability.mu.Unlock()
		return err
	}
	d.durability.mu.Unlock()
	return d.durability.wait(ctx, func() (error, bool) {
		// A job that finished while the caller was waiting still reports its
		// result. Eviction after that is only visible to later lookups.
		if job.done {
			return job.err, true
		}
		if job.expired {
			return errDurabilityJobExpired, true
		}
		if d.durability.closed {
			return ErrClosed, true
		}
		return nil, false
	})
}

// DurableState returns the highest durable sequence number and the first
// latched WAL sync error.
func (d *DB) DurableState() (base.SeqNum, error) {
	return d.durability.state()
}

// DurabilityNotify returns a buffered channel that receives nil when seq is
// durable, or a non-nil error when the WAL sync covering seq fails or the DB
// is closed. The channel is already populated when the result is known.
// Outstanding subscriptions are bounded; callers past the limit receive a
// channel holding an immediate error.
func (d *DB) DurabilityNotify(seq base.SeqNum) <-chan error {
	ch := make(chan error, 1)
	c := &d.durability
	if c.disableWAL || c.cond == nil {
		ch <- nil
		return ch
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err, done := c.seqStatusLocked(seq); done {
		ch <- err
		return ch
	}
	if c.outstandingSubs >= durabilityMaxSubscriptions {
		ch <- errDurabilitySubscriptionLimit
		return ch
	}
	c.subs = append(c.subs, durabilitySub{seq: seq, ch: ch})
	c.outstandingSubs++
	return ch
}

// DurabilityStats returns a snapshot of durability tracking. Every field is
// zero before any commit completes.
func (d *DB) DurabilityStats() DurabilityStats {
	return d.durability.stats()
}
