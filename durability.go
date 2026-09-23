// Copyright 2026 The LevelDB-Go and Pebble Authors. All rights reserved. Use
// of this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package pebble

import (
	"context"
	"sync"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/cockroachdb/pebble/internal/base"
)

const durabilityNotifyLimit = 1024

// durabilityJobRetention bounds how many sync-commit job results are retained
// for WaitForJobDurability. Older completed jobs return an expired error.
var durabilityJobRetention = 4096

var (
	errDurabilityJobUnknown  = errors.New("pebble: durability job unknown")
	errDurabilityJobExpired  = errors.New("pebble: durability job expired")
	errDurabilityNotifyLimit = errors.New("pebble: durability notify subscription limit exceeded")
)

// DurabilityStats is a point-in-time snapshot of sync-commit durability.
type DurabilityStats struct {
	// HighestDurableSeqNum is the highest sequence number known to be durable.
	HighestDurableSeqNum base.SeqNum
	// FirstErr is the first WAL sync error latched by the DB. Later sync
	// errors do not replace it.
	FirstErr error
	// PendingWaiters is the number of goroutines blocked in the wait APIs.
	PendingWaiters int64
	// TotalDurableCommits is the number of sync commits whose WAL sync succeeded.
	TotalDurableCommits uint64
	// TotalFailedCommits is the number of sync commits whose WAL sync failed.
	TotalFailedCommits uint64
	// CumulativeSyncDuration is the sum of WAL sync phase durations.
	CumulativeSyncDuration time.Duration
	// MaxSyncDuration is the longest WAL sync phase duration observed.
	MaxSyncDuration time.Duration
}

// durableCommitState is one sync commit, from WAL queue through sync and apply.
type durableCommitState struct {
	coord       *durabilityCoord
	jobID       int
	seq         base.SeqNum
	endSeq      base.SeqNum
	batchSize   int
	keyCount    uint32
	correlation uint64
	listener    func(BatchDurableInfo)

	mu        sync.Mutex
	applyDone bool
	syncDone  bool
	fired     bool
	ready     bool
	failed    bool
	applyDur  time.Duration
	syncDur   time.Duration
	applyErr  error
	syncErr   error
}

type durabilityJob struct {
	done    bool
	err     error
	waiters []*durabilityWaiter
}

type durabilityWaiter struct {
	seq  base.SeqNum
	ch   chan error
	done bool
}

type durabilityNotify struct {
	seq  base.SeqNum
	ch   chan error
	done bool
}

// durabilityCoord tracks WAL sync completion for sync commits and the waiters
// blocked on that durability.
type durabilityCoord struct {
	mu                sync.Mutex
	disableWAL        bool
	metricsEnabled    bool
	closed            bool
	closeErr          error
	highest           base.SeqNum
	firstErr          error
	failedAt          base.SeqNum
	sawSuccess        bool
	pendingWaiters    int64
	totalDurable      uint64
	totalFailed       uint64
	cumulativeSync    time.Duration
	maxSync           time.Duration
	metricCount       uint64
	metricDuration    time.Duration
	nextJob           int
	queue             []*durableCommitState
	jobs              map[int]*durabilityJob
	jobRing           []int
	seqWaiters        []*durabilityWaiter
	notifies          []*durabilityNotify
	notifyOutstanding int
}

func (c *durabilityCoord) init(disableWAL, metrics bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disableWAL = disableWAL
	c.metricsEnabled = metrics
	if c.jobs == nil {
		c.jobs = make(map[int]*durabilityJob)
	}
}

func (c *durabilityCoord) begin(b *Batch, listener func(BatchDurableInfo)) *durableCommitState {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.jobs == nil {
		c.jobs = make(map[int]*durabilityJob)
	}
	c.nextJob++
	jobID := c.nextJob
	count := b.Count()
	end := b.SeqNum()
	if count > 0 {
		end = b.SeqNum() + base.SeqNum(count) - 1
	}
	st := &durableCommitState{
		coord:       c,
		jobID:       jobID,
		seq:         b.SeqNum(),
		endSeq:      end,
		batchSize:   len(b.Repr()),
		keyCount:    count,
		correlation: b.commitCorrelationID,
		listener:    listener,
	}
	c.queue = append(c.queue, st)
	c.jobs[jobID] = &durabilityJob{}
	c.jobRing = append(c.jobRing, jobID)
	c.evictLocked()
	return st
}

func (c *durabilityCoord) evictLocked() {
	limit := durabilityJobRetention
	if limit < 1 {
		limit = 1
	}
	for len(c.jobRing) > limit {
		id := c.jobRing[0]
		job := c.jobs[id]
		if job != nil && !job.done {
			return
		}
		c.jobRing = c.jobRing[1:]
		delete(c.jobs, id)
	}
}

func (s *durableCommitState) noteApply(d time.Duration, err error) {
	s.finish(true /* apply */, d, err)
}

func (s *durableCommitState) noteSync(err error, syncLatency time.Duration) {
	s.finish(false /* apply */, syncLatency, err)
}

func (s *durableCommitState) finish(apply bool, dur time.Duration, err error) {
	s.mu.Lock()
	if apply {
		if s.applyDone {
			s.mu.Unlock()
			return
		}
		s.applyDone = true
		s.applyDur = dur
		s.applyErr = err
	} else {
		if s.syncDone {
			s.mu.Unlock()
			return
		}
		s.syncDone = true
		s.syncDur = dur
		s.syncErr = err
	}
	fire := s.applyDone && s.syncDone && !s.fired
	if fire {
		s.fired = true
	}
	s.mu.Unlock()
	if fire {
		s.coord.publish(s)
	}
}

func positiveDuration(d time.Duration) time.Duration {
	if d <= 0 {
		return time.Nanosecond
	}
	return d
}

func (c *durabilityCoord) publish(s *durableCommitState) {
	s.mu.Lock()
	applyDur := s.applyDur
	syncDur := s.syncDur
	applyErr := s.applyErr
	syncErr := s.syncErr
	s.mu.Unlock()

	if syncErr == nil {
		applyDur = positiveDuration(applyDur)
		syncDur = positiveDuration(syncDur)
		s.mu.Lock()
		s.applyDur = applyDur
		s.syncDur = syncDur
		s.mu.Unlock()
	}
	infoErr := syncErr
	if infoErr == nil {
		infoErr = applyErr
	}
	info := BatchDurableInfo{
		JobID:         s.jobID,
		SeqNum:        s.seq,
		Err:           infoErr,
		ApplyDuration: applyDur,
		SyncDuration:  syncDur,
		CorrelationID: s.correlation,
		BatchSize:     s.batchSize,
		KeyCount:      s.keyCount,
	}

	c.mu.Lock()
	s.ready = true
	s.failed = syncErr != nil
	if syncErr != nil {
		c.totalFailed++
		if c.firstErr == nil {
			c.firstErr = syncErr
		}
		if c.failedAt == 0 || s.seq < c.failedAt {
			c.failedAt = s.seq
		}
	} else {
		c.sawSuccess = true
		c.totalDurable++
		c.cumulativeSync += syncDur
		if syncDur > c.maxSync {
			c.maxSync = syncDur
		}
		if c.metricsEnabled {
			c.metricCount++
			c.metricDuration += syncDur
		}
	}
	for len(c.queue) > 0 && c.queue[0].ready {
		head := c.queue[0]
		if head.failed {
			c.queue = c.queue[1:]
			break
		}
		c.queue = c.queue[1:]
		if head.endSeq > c.highest {
			c.highest = head.endSeq
		}
	}
	c.completeJobLocked(s.jobID, info.Err)
	c.wakeLocked()
	c.mu.Unlock()

	if s.listener != nil {
		s.listener(info)
	}
}

func (c *durabilityCoord) completeJobLocked(jobID int, err error) {
	job := c.jobs[jobID]
	if job == nil {
		return
	}
	job.done = true
	job.err = err
	for _, w := range job.waiters {
		if w.done {
			continue
		}
		w.done = true
		w.ch <- err
	}
	job.waiters = nil
	c.evictLocked()
}

func (c *durabilityCoord) canStillSucceedLocked() bool {
	for _, st := range c.queue {
		if !st.ready {
			return true
		}
	}
	return false
}

func (c *durabilityCoord) waitResultLocked(seq base.SeqNum) (error, bool) {
	if seq == 0 {
		if c.sawSuccess {
			return nil, true
		}
		if c.firstErr != nil && !c.canStillSucceedLocked() {
			return c.firstErr, true
		}
		if c.closed {
			return c.closeErr, true
		}
		return nil, false
	}
	if seq <= c.highest {
		return nil, true
	}
	if c.failedAt != 0 && seq >= c.failedAt && c.firstErr != nil {
		return c.firstErr, true
	}
	if c.closed {
		return c.closeErr, true
	}
	return nil, false
}

func (c *durabilityCoord) wakeLocked() {
	kept := c.seqWaiters[:0]
	for _, w := range c.seqWaiters {
		if w.done {
			continue
		}
		err, ok := c.waitResultLocked(w.seq)
		if !ok {
			kept = append(kept, w)
			continue
		}
		w.done = true
		w.ch <- err
	}
	c.seqWaiters = kept

	keptN := c.notifies[:0]
	for _, n := range c.notifies {
		if n.done {
			continue
		}
		err, ok := c.waitResultLocked(n.seq)
		if !ok {
			keptN = append(keptN, n)
			continue
		}
		n.done = true
		n.ch <- err
		if c.notifyOutstanding > 0 {
			c.notifyOutstanding--
		}
	}
	c.notifies = keptN
}

func (c *durabilityCoord) closeDB(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	if err == nil {
		err = ErrClosed
	}
	c.closeErr = err
	for _, job := range c.jobs {
		if job.done {
			continue
		}
		for _, w := range job.waiters {
			if w.done {
				continue
			}
			w.done = true
			w.ch <- c.closeErr
		}
		job.waiters = nil
	}
	c.wakeLocked()
}

func (c *durabilityCoord) waitSeq(ctx context.Context, seq base.SeqNum) error {
	if c.disableWAL {
		return nil
	}
	c.mu.Lock()
	if err, ok := c.waitResultLocked(seq); ok {
		c.mu.Unlock()
		return err
	}
	w := &durabilityWaiter{seq: seq, ch: make(chan error, 1)}
	c.seqWaiters = append(c.seqWaiters, w)
	c.pendingWaiters++
	c.mu.Unlock()

	select {
	case err := <-w.ch:
		c.finishWait()
		return err
	case <-ctx.Done():
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if err, ok := c.waitResultLocked(seq); ok {
		if !w.done {
			w.done = true
		}
		c.pendingWaiters--
		return err
	}
	if !w.done {
		w.done = true
		c.removeSeqWaiterLocked(w)
	}
	c.pendingWaiters--
	return ctx.Err()
}

func (c *durabilityCoord) finishWait() {
	c.mu.Lock()
	c.pendingWaiters--
	c.mu.Unlock()
}

func (c *durabilityCoord) removeSeqWaiterLocked(w *durabilityWaiter) {
	for i := range c.seqWaiters {
		if c.seqWaiters[i] == w {
			c.seqWaiters = append(c.seqWaiters[:i], c.seqWaiters[i+1:]...)
			return
		}
	}
}

func (c *durabilityCoord) jobResultLocked(jobID int) (error, bool) {
	if jobID == 0 || jobID > c.nextJob {
		return errDurabilityJobUnknown, true
	}
	job := c.jobs[jobID]
	if job == nil {
		return errDurabilityJobExpired, true
	}
	if !job.done {
		if c.closed {
			return c.closeErr, true
		}
		return nil, false
	}
	return job.err, true
}

func (c *durabilityCoord) waitJob(ctx context.Context, jobID int) error {
	if c.disableWAL {
		return nil
	}
	c.mu.Lock()
	if err, ok := c.jobResultLocked(jobID); ok {
		c.mu.Unlock()
		return err
	}
	job := c.jobs[jobID]
	w := &durabilityWaiter{ch: make(chan error, 1)}
	job.waiters = append(job.waiters, w)
	c.pendingWaiters++
	c.mu.Unlock()

	select {
	case err := <-w.ch:
		c.finishWait()
		return err
	case <-ctx.Done():
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if err, ok := c.jobResultLocked(jobID); ok {
		if !w.done {
			w.done = true
		}
		c.pendingWaiters--
		return err
	}
	if !w.done {
		w.done = true
	}
	c.pendingWaiters--
	return ctx.Err()
}

func (c *durabilityCoord) notify(seq base.SeqNum) <-chan error {
	ch := make(chan error, 1)
	if c.disableWAL {
		ch <- nil
		return ch
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err, ok := c.waitResultLocked(seq); ok {
		ch <- err
		return ch
	}
	if c.notifyOutstanding >= durabilityNotifyLimit {
		ch <- errDurabilityNotifyLimit
		return ch
	}
	c.notifies = append(c.notifies, &durabilityNotify{seq: seq, ch: ch})
	c.notifyOutstanding++
	return ch
}

func (c *durabilityCoord) state() (base.SeqNum, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.highest, c.firstErr
}

func (c *durabilityCoord) stats() DurabilityStats {
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

func (c *durabilityCoord) metricsSnapshot() (uint64, time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.metricsEnabled {
		return 0, 0
	}
	return c.metricCount, c.metricDuration
}

// WaitForDurability blocks until seqNum is durable. A zero seqNum returns
// after any successful sync commit. DisableWAL makes the call return nil.
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
	var max base.SeqNum
	onlyZero := true
	for _, seq := range seqNums {
		if seq == 0 {
			continue
		}
		onlyZero = false
		if seq > max {
			max = seq
		}
	}
	if onlyZero {
		return d.WaitForDurabilityContext(ctx, 0)
	}
	return d.WaitForDurabilityContext(ctx, max)
}

// WaitForJobDurability blocks until the sync commit identified by jobID (the
// BatchDurableInfo.JobID) finishes its WAL sync. An unknown or zero job ID
// returns an error whose message contains "unknown". A job that has fallen
// out of the retention window returns an error whose message contains
// "expired".
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

// DurableState returns the highest durable sequence number and the first
// latched WAL sync error.
func (d *DB) DurableState() (base.SeqNum, error) {
	return d.durability.state()
}

// DurabilityNotify returns a pre-filled, receive-only channel that yields nil
// once seqNum is durable, or a non-nil error if the WAL sync fails or the DB
// is closed. Outstanding subscriptions are bounded; callers beyond the limit
// receive a channel that already holds a non-nil error. DisableWAL yields a
// channel holding nil.
func (d *DB) DurabilityNotify(seqNum base.SeqNum) <-chan error {
	if d.opts.DisableWAL {
		ch := make(chan error, 1)
		ch <- nil
		return ch
	}
	return d.durability.notify(seqNum)
}

// DurabilityStats returns a snapshot of durability counters. Every field is
// zero before any sync commit.
func (d *DB) DurabilityStats() DurabilityStats {
	return d.durability.stats()
}
