// The MIT License (MIT)
//
// Copyright (c) 2015 xtaci
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package kcp

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// MuxSide identifies which peer allocates which stream IDs.
type MuxSide uint8

const (
	// MuxSideClient allocates odd stream IDs (1, 3, 5, ...).
	MuxSideClient MuxSide = 1
	// MuxSideServer allocates even stream IDs (2, 4, 6, ...).
	MuxSideServer MuxSide = 2
)

const (
	// MuxPriorityLow is the lowest scheduling priority.
	// Larger priority values are scheduled ahead of smaller ones.
	MuxPriorityLow uint8 = iota
	// MuxPriorityNormal is the default scheduling priority.
	MuxPriorityNormal
	// MuxPriorityHigh is scheduled ahead of normal and low streams.
	MuxPriorityHigh
)

const (
	defaultMuxMaxFrameSize = 32 * 1024
	defaultMuxWindow       = 256 * 1024

	muxVersion    = 1
	muxHeaderSize = 12

	cmdSYN byte = 1
	cmdACK byte = 2
	cmdPSH byte = 3
	cmdUPD byte = 4
	cmdFIN byte = 5
)

// MuxConfig configures a multiplexed session.
type MuxConfig struct {
	Side         MuxSide
	MaxFrameSize int
	SendWindow   int
	RecvWindow   int
}

// DefaultMuxConfig returns a client-side config with 32KiB frames and 256KiB windows.
// Servers must set Side to MuxSideServer.
func DefaultMuxConfig() MuxConfig {
	return MuxConfig{
		Side:         MuxSideClient,
		MaxFrameSize: defaultMuxMaxFrameSize,
		SendWindow:   defaultMuxWindow,
		RecvWindow:   defaultMuxWindow,
	}
}

// MuxSession carries many independent streams over one reliable connection.
type MuxSession struct {
	conn net.Conn
	cfg  MuxConfig

	mu      sync.Mutex
	cond    *sync.Cond
	streams map[uint32]*MuxStream
	acceptQ []*MuxStream
	ctrl    []ctrlItem
	nextID  uint32
	rr      int
	closed  bool
	cause   error
	die     chan struct{}
}

// MuxStream is one ordered byte stream inside a MuxSession.
type MuxStream struct {
	sess     *MuxSession
	id       uint32
	priority uint8

	// send window: bytes accepted into chunks but not yet read by the peer
	peerWindow   uint64
	peerConsumed uint64
	written      uint64
	sendReady    bool

	chunks   []*writeChunk
	chunkOff int

	localClosed  bool
	remoteFin    bool
	finQueued    bool
	writeAborted bool
	writeErr     error
	writeAbort   chan struct{}

	recvBuf      []byte
	consumed     uint64
	updSent      uint64
	updQueued    bool
	readDeadline time.Time

	removed      bool
	metricClosed bool
}

type writeChunk struct {
	st   *MuxStream
	data []byte
	done chan error
}

type ctrlItem struct {
	frame []byte
	upd   *MuxStream
}

// NewMuxSession starts a multiplexer over conn.
// conn must be a reliable ordered byte stream (for example a KCP session).
func NewMuxSession(conn net.Conn, cfg *MuxConfig) (*MuxSession, error) {
	if conn == nil {
		return nil, errors.New("mux: nil conn")
	}
	if cfg == nil {
		return nil, errors.New("mux: nil config")
	}
	c := *cfg
	if c.Side != MuxSideClient && c.Side != MuxSideServer {
		return nil, errors.New("mux: invalid side")
	}
	if c.MaxFrameSize <= 0 || uint64(c.MaxFrameSize) > math.MaxUint32 {
		return nil, errors.New("mux: invalid max frame size")
	}
	if c.SendWindow <= 0 || c.RecvWindow <= 0 ||
		uint64(c.SendWindow) > math.MaxUint32 || uint64(c.RecvWindow) > math.MaxUint32 {
		return nil, errors.New("mux: invalid window")
	}

	s := &MuxSession{
		conn:    conn,
		cfg:     c,
		streams: make(map[uint32]*MuxStream),
		die:     make(chan struct{}),
	}
	if c.Side == MuxSideClient {
		s.nextID = 1
	} else {
		s.nextID = 2
	}
	s.cond = sync.NewCond(&s.mu)
	go s.readLoop()
	go s.writeLoop()
	return s, nil
}

// Close shuts the session down and unblocks every stream operation.
// It returns as soon as shutdown is signaled and does not wait for background I/O.
func (s *MuxSession) Close() error {
	if !s.shutdown(io.ErrClosedPipe) {
		return io.ErrClosedPipe
	}
	return nil
}

// NumStreams reports streams still tracked by the session.
func (s *MuxSession) NumStreams() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.streams)
}

// OpenStream opens a new stream. Either peer may call it.
// The returned stream shares its ID with the remote AcceptStream result.
func (s *MuxSession) OpenStream(priority uint8) (*MuxStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.closedErrLocked(); err != nil {
		return nil, err
	}
	id, err := s.allocIDLocked()
	if err != nil {
		return nil, err
	}
	st := newMuxStream(s, id, priority)
	s.streams[id] = st
	payload := make([]byte, 5)
	payload[0] = priority
	binary.BigEndian.PutUint32(payload[1:5], uint32(s.cfg.RecvWindow))
	s.enqueueCtrlLocked(encodeFrame(cmdSYN, id, payload))
	atomic.AddUint64(&DefaultSnmp.MuxStreamsOpened, 1)
	s.cond.Broadcast()
	return st, nil
}

// AcceptStream waits for a stream opened by the remote peer.
func (s *MuxSession) AcceptStream() (*MuxStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if len(s.acceptQ) > 0 {
			st := s.acceptQ[0]
			s.acceptQ[0] = nil
			s.acceptQ = s.acceptQ[1:]
			return st, nil
		}
		if err := s.closedErrLocked(); err != nil {
			return nil, err
		}
		s.cond.Wait()
	}
}

// ID returns the stream identifier shared by both peers.
func (st *MuxStream) ID() uint32 { return st.id }

// Read reads the next bytes from the stream.
// A remote close yields io.EOF once buffered data has been consumed.
func (st *MuxStream) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	s := st.sess
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if s.closed && len(st.recvBuf) == 0 {
			return 0, s.opErrLocked()
		}
		if dl, ok := st.deadlineExceededLocked(); ok {
			return 0, dl
		}
		if len(st.recvBuf) > 0 {
			n := copy(b, st.recvBuf)
			st.recvBuf = st.recvBuf[n:]
			if len(st.recvBuf) == 0 {
				st.recvBuf = nil
			}
			st.consumed += uint64(n)
			s.noteConsumedLocked(st)
			st.maybeRemoveLocked()
			s.cond.Broadcast()
			return n, nil
		}
		if s.closed {
			return 0, s.opErrLocked()
		}
		if st.remoteFin {
			st.maybeRemoveLocked()
			return 0, io.EOF
		}
		timer := st.armReadTimerLocked()
		s.cond.Wait()
		if timer != nil {
			timer.Stop()
		}
	}
}

// Write writes p in full. It blocks until every byte is written to the
// underlying connection or an error occurs. A successful Write is never short.
func (st *MuxStream) Write(p []byte) (int, error) {
	s := st.sess
	if len(p) == 0 {
		s.mu.Lock()
		err := st.writeBlockErrLocked()
		s.mu.Unlock()
		if err != nil {
			return 0, err
		}
		return 0, nil
	}

	accepted := 0
	for accepted < len(p) {
		s.mu.Lock()
		if err := st.writeBlockErrLocked(); err != nil {
			s.mu.Unlock()
			if accepted > 0 {
				return accepted, err
			}
			return 0, err
		}
		var batch []*writeChunk
		pos := accepted
		for pos < len(p) {
			cred := st.creditLocked()
			if cred <= 0 {
				break
			}
			n := cred
			if n > s.cfg.MaxFrameSize {
				n = s.cfg.MaxFrameSize
			}
			if n > len(p)-pos {
				n = len(p) - pos
			}
			ch := &writeChunk{
				st:   st,
				data: append([]byte(nil), p[pos:pos+n]...),
				done: make(chan error, 1),
			}
			st.chunks = append(st.chunks, ch)
			st.written += uint64(n)
			batch = append(batch, ch)
			pos += n
		}
		if len(batch) == 0 {
			s.cond.Wait()
			s.mu.Unlock()
			continue
		}
		s.cond.Broadcast()
		s.mu.Unlock()

		for _, ch := range batch {
			if err := st.waitChunk(ch); err != nil {
				if accepted > 0 {
					return accepted, err
				}
				return 0, err
			}
			accepted += len(ch.data)
		}
	}
	return accepted, nil
}

// Close half-closes the stream: local writes stop, and data already buffered
// for reading stays readable until it is drained.
func (st *MuxStream) Close() error {
	s := st.sess
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return s.opErrLocked()
	}
	if st.localClosed {
		return io.ErrClosedPipe
	}
	st.localClosed = true
	st.abortWritesLocked(io.ErrClosedPipe)
	st.ensureFINLocked()
	st.maybeRemoveLocked()
	s.cond.Broadcast()
	return nil
}

// SetReadDeadline sets the deadline for future Read calls.
// A zero time clears the deadline. An expired deadline makes Read return
// an error that satisfies net.Error and whose Timeout method returns true.
func (st *MuxStream) SetReadDeadline(t time.Time) error {
	s := st.sess
	s.mu.Lock()
	st.readDeadline = t
	s.cond.Broadcast()
	s.mu.Unlock()
	return nil
}

func newMuxStream(s *MuxSession, id uint32, priority uint8) *MuxStream {
	return &MuxStream{
		sess:       s,
		id:         id,
		priority:   priority,
		writeAbort: make(chan struct{}),
	}
}

func (s *MuxSession) shutdown(err error) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	s.closed = true
	s.cause = muxCause(err)
	for _, st := range s.streams {
		st.abortWritesLocked(s.cause)
		if !st.metricClosed {
			st.metricClosed = true
			atomic.AddUint64(&DefaultSnmp.MuxStreamsClosed, 1)
		}
	}
	s.streams = make(map[uint32]*MuxStream)
	s.acceptQ = nil
	close(s.die)
	s.cond.Broadcast()
	conn := s.conn
	s.mu.Unlock()
	_ = conn.Close()
	return true
}

func (s *MuxSession) closedErrLocked() error {
	if !s.closed {
		return nil
	}
	return s.opErrLocked()
}

func (s *MuxSession) opErrLocked() error {
	if s.cause != nil {
		return s.cause
	}
	return io.ErrClosedPipe
}

func (s *MuxSession) allocIDLocked() (uint32, error) {
	start := s.nextID
	for {
		id := s.nextID
		next := id + 2
		if next < id || next == 0 {
			if s.cfg.Side == MuxSideClient {
				next = 1
			} else {
				next = 2
			}
		}
		s.nextID = next
		if id != 0 {
			if _, exists := s.streams[id]; !exists {
				return id, nil
			}
		}
		if s.nextID == start {
			return 0, errors.New("mux: stream id exhausted")
		}
	}
}

func (s *MuxSession) clampWindow(remote uint32) uint64 {
	w := uint64(remote)
	local := uint64(s.cfg.SendWindow)
	if local < w {
		return local
	}
	return w
}

func (st *MuxStream) creditLocked() int {
	if !st.sendReady || st.peerWindow == 0 {
		return 0
	}
	var inflight uint64
	if st.written > st.peerConsumed {
		inflight = st.written - st.peerConsumed
	}
	if inflight >= st.peerWindow {
		return 0
	}
	c := st.peerWindow - inflight
	if c > uint64(math.MaxInt) {
		return math.MaxInt
	}
	return int(c)
}

func (st *MuxStream) hasChunks() bool {
	return st.chunkOff < len(st.chunks)
}

func (st *MuxStream) popChunkLocked() *writeChunk {
	if !st.hasChunks() {
		return nil
	}
	ch := st.chunks[st.chunkOff]
	st.chunks[st.chunkOff] = nil
	st.chunkOff++
	if st.chunkOff == len(st.chunks) {
		st.chunks = st.chunks[:0]
		st.chunkOff = 0
	} else if st.chunkOff > 64 {
		st.chunks = append([]*writeChunk(nil), st.chunks[st.chunkOff:]...)
		st.chunkOff = 0
	}
	return ch
}

func (st *MuxStream) writeBlockErrLocked() error {
	if st.writeAborted {
		if st.writeErr != nil {
			return st.writeErr
		}
		return io.ErrClosedPipe
	}
	if st.localClosed || st.sess.closed {
		if st.sess.closed {
			return st.sess.opErrLocked()
		}
		return io.ErrClosedPipe
	}
	return nil
}

func (st *MuxStream) abortWritesLocked(err error) {
	if err == nil {
		err = io.ErrClosedPipe
	}
	if !st.writeAborted {
		st.writeAborted = true
		st.writeErr = err
		close(st.writeAbort)
	}
	for i := st.chunkOff; i < len(st.chunks); i++ {
		signalChunk(st.chunks[i], st.writeErr)
		st.chunks[i] = nil
	}
	st.chunks = st.chunks[:0]
	st.chunkOff = 0
}

func (st *MuxStream) waitChunk(ch *writeChunk) error {
	select {
	case err := <-ch.done:
		return err
	case <-st.writeAbort:
		return st.abortedChunk(ch)
	case <-st.sess.die:
		return st.abortedChunk(ch)
	}
}

func (st *MuxStream) abortedChunk(ch *writeChunk) error {
	select {
	case err := <-ch.done:
		return err
	default:
	}
	st.sess.mu.Lock()
	err := st.writeErr
	if err == nil && st.sess.cause != nil {
		err = st.sess.cause
	}
	st.sess.mu.Unlock()
	if err == nil {
		err = io.ErrClosedPipe
	}
	return err
}

func (st *MuxStream) deadlineExceededLocked() (error, bool) {
	if st.readDeadline.IsZero() {
		return nil, false
	}
	if !time.Now().Before(st.readDeadline) {
		return errTimeout, true
	}
	return nil, false
}

func (st *MuxStream) armReadTimerLocked() *time.Timer {
	if st.readDeadline.IsZero() {
		return nil
	}
	d := time.Until(st.readDeadline)
	if d < 0 {
		d = 0
	}
	s := st.sess
	return time.AfterFunc(d, func() {
		s.mu.Lock()
		s.cond.Broadcast()
		s.mu.Unlock()
	})
}

func (st *MuxStream) ensureFINLocked() {
	if st.finQueued || !st.localClosed || st.hasChunks() {
		return
	}
	st.finQueued = true
	st.sess.enqueueCtrlLocked(encodeFrame(cmdFIN, st.id, nil))
}

func (st *MuxStream) maybeRemoveLocked() {
	if st.removed || st.metricClosed {
		return
	}
	if !(st.localClosed && st.finQueued && st.remoteFin && len(st.recvBuf) == 0 && !st.hasChunks()) {
		return
	}
	st.removed = true
	st.metricClosed = true
	delete(st.sess.streams, st.id)
	aq := st.sess.acceptQ[:0]
	for _, q := range st.sess.acceptQ {
		if q != nil && q != st {
			aq = append(aq, q)
		}
	}
	// drop leftover refs when the queue shrank in place
	for i := len(aq); i < len(st.sess.acceptQ); i++ {
		st.sess.acceptQ[i] = nil
	}
	st.sess.acceptQ = aq
	atomic.AddUint64(&DefaultSnmp.MuxStreamsClosed, 1)
}

func (s *MuxSession) noteConsumedLocked(st *MuxStream) {
	if s.closed || st.consumed <= st.updSent || st.updQueued {
		return
	}
	st.updQueued = true
	s.ctrl = append(s.ctrl, ctrlItem{upd: st})
}

func (s *MuxSession) enqueueCtrlLocked(frame []byte) {
	s.ctrl = append(s.ctrl, ctrlItem{frame: frame})
}

func (s *MuxSession) readLoop() {
	for {
		cmd, sid, payload, err := s.readFrame()
		if err != nil {
			s.shutdown(err)
			return
		}
		atomic.AddUint64(&DefaultSnmp.MuxFramesReceived, 1)
		if cmd == cmdPSH {
			atomic.AddUint64(&DefaultSnmp.MuxBytesReceived, uint64(len(payload)))
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		herr := s.dispatchLocked(cmd, sid, payload)
		s.mu.Unlock()
		if herr != nil {
			s.shutdown(herr)
			return
		}
	}
}

func (s *MuxSession) dispatchLocked(cmd byte, sid uint32, payload []byte) error {
	switch cmd {
	case cmdSYN:
		return s.handleSYNLocked(sid, payload)
	case cmdACK:
		return s.handleACKLocked(sid, payload)
	case cmdPSH:
		return s.handlePSHLocked(sid, payload)
	case cmdUPD:
		return s.handleUPDLocked(sid, payload)
	case cmdFIN:
		s.handleFINLocked(sid)
		return nil
	default:
		return errors.New("mux: bad command")
	}
}

func (s *MuxSession) handleSYNLocked(sid uint32, payload []byte) error {
	if len(payload) != 5 || sid == 0 {
		return errors.New("mux: bad syn")
	}
	if _, exists := s.streams[sid]; exists {
		return errors.New("mux: duplicate stream")
	}
	priority := payload[0]
	recvWindow := binary.BigEndian.Uint32(payload[1:5])
	st := newMuxStream(s, sid, priority)
	st.peerWindow = s.clampWindow(recvWindow)
	st.sendReady = true
	s.streams[sid] = st
	s.acceptQ = append(s.acceptQ, st)
	ack := make([]byte, 4)
	binary.BigEndian.PutUint32(ack, uint32(s.cfg.RecvWindow))
	s.enqueueCtrlLocked(encodeFrame(cmdACK, sid, ack))
	atomic.AddUint64(&DefaultSnmp.MuxStreamsOpened, 1)
	s.cond.Broadcast()
	return nil
}

func (s *MuxSession) handleACKLocked(sid uint32, payload []byte) error {
	if len(payload) != 4 {
		return errors.New("mux: bad ack")
	}
	st := s.streams[sid]
	if st == nil {
		return nil
	}
	st.peerWindow = s.clampWindow(binary.BigEndian.Uint32(payload))
	st.sendReady = true
	s.cond.Broadcast()
	return nil
}

func (s *MuxSession) handlePSHLocked(sid uint32, payload []byte) error {
	st := s.streams[sid]
	if st == nil || len(payload) == 0 {
		return nil
	}
	st.recvBuf = append(st.recvBuf, payload...)
	s.cond.Broadcast()
	return nil
}

func (s *MuxSession) handleUPDLocked(sid uint32, payload []byte) error {
	if len(payload) != 8 {
		return errors.New("mux: bad window update")
	}
	st := s.streams[sid]
	if st == nil {
		return nil
	}
	v := binary.BigEndian.Uint64(payload)
	if v > st.peerConsumed {
		st.peerConsumed = v
		s.cond.Broadcast()
	}
	return nil
}

func (s *MuxSession) handleFINLocked(sid uint32) {
	st := s.streams[sid]
	if st == nil || st.remoteFin {
		return
	}
	st.remoteFin = true
	st.abortWritesLocked(io.ErrClosedPipe)
	st.maybeRemoveLocked()
	s.cond.Broadcast()
}

func (s *MuxSession) writeLoop() {
	for {
		frame, chunk, ok := s.awaitOutgoing()
		if !ok {
			return
		}
		err := writeFull(s.conn, frame)
		if err != nil {
			if chunk != nil {
				signalChunk(chunk, muxCause(err))
			}
			s.shutdown(err)
			return
		}
		atomic.AddUint64(&DefaultSnmp.MuxFramesSent, 1)
		if chunk != nil {
			atomic.AddUint64(&DefaultSnmp.MuxBytesSent, uint64(len(chunk.data)))
		}
		s.mu.Lock()
		if chunk != nil {
			signalChunk(chunk, nil)
			st := chunk.st
			if !s.closed && st.localClosed && !st.hasChunks() {
				st.ensureFINLocked()
			}
			st.maybeRemoveLocked()
		}
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return
		}
	}
}

func (s *MuxSession) awaitOutgoing() (frame []byte, chunk *writeChunk, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if s.closed {
			return nil, nil, false
		}
		frame, chunk, ok = s.popOutgoingLocked()
		if ok {
			return frame, chunk, true
		}
		s.cond.Wait()
	}
}

func (s *MuxSession) popOutgoingLocked() ([]byte, *writeChunk, bool) {
	for len(s.ctrl) > 0 {
		item := s.ctrl[0]
		s.ctrl[0] = ctrlItem{}
		s.ctrl = s.ctrl[1:]
		if item.upd != nil {
			st := item.upd
			st.updQueued = false
			if st.consumed <= st.updSent {
				continue
			}
			var buf [8]byte
			binary.BigEndian.PutUint64(buf[:], st.consumed)
			st.updSent = st.consumed
			return encodeFrame(cmdUPD, st.id, buf[:]), nil, true
		}
		if len(item.frame) > 0 {
			return item.frame, nil, true
		}
	}
	st, ch := s.pickChunkLocked()
	if st == nil {
		return nil, nil, false
	}
	return encodeFrame(cmdPSH, st.id, ch.data), ch, true
}

func (s *MuxSession) pickChunkLocked() (*MuxStream, *writeChunk) {
	var bestPri int = -1
	var cands []*MuxStream
	for _, st := range s.streams {
		if !st.hasChunks() {
			continue
		}
		p := int(st.priority)
		if p > bestPri {
			bestPri = p
			cands = cands[:0]
			cands = append(cands, st)
		} else if p == bestPri {
			cands = append(cands, st)
		}
	}
	if len(cands) == 0 {
		return nil, nil
	}
	// Stable order so round-robin does not depend on map iteration.
	for i := 1; i < len(cands); i++ {
		j := i
		for j > 0 && cands[j].id < cands[j-1].id {
			cands[j], cands[j-1] = cands[j-1], cands[j]
			j--
		}
	}
	idx := 0
	if len(cands) > 1 {
		idx = s.rr % len(cands)
		s.rr++
	}
	st := cands[idx]
	return st, st.popChunkLocked()
}

func (s *MuxSession) readFrame() (cmd byte, sid uint32, payload []byte, err error) {
	var hdr [muxHeaderSize]byte
	if _, err = io.ReadFull(s.conn, hdr[:]); err != nil {
		return 0, 0, nil, err
	}
	if hdr[0] != muxVersion {
		return 0, 0, nil, errors.New("mux: bad version")
	}
	cmd = hdr[1]
	sid = binary.BigEndian.Uint32(hdr[4:8])
	ln := binary.BigEndian.Uint32(hdr[8:12])
	if cmd == cmdPSH {
		if ln > uint32(s.cfg.MaxFrameSize) {
			return 0, 0, nil, errors.New("mux: frame too large")
		}
	} else if ln > 64 {
		return 0, 0, nil, errors.New("mux: control frame too large")
	}
	if ln == 0 {
		return cmd, sid, nil, nil
	}
	payload = make([]byte, ln)
	if _, err = io.ReadFull(s.conn, payload); err != nil {
		return 0, 0, nil, err
	}
	return cmd, sid, payload, nil
}

func encodeFrame(cmd byte, sid uint32, payload []byte) []byte {
	buf := make([]byte, muxHeaderSize+len(payload))
	buf[0] = muxVersion
	buf[1] = cmd
	binary.BigEndian.PutUint32(buf[4:8], sid)
	binary.BigEndian.PutUint32(buf[8:12], uint32(len(payload)))
	copy(buf[muxHeaderSize:], payload)
	return buf
}

func writeFull(conn net.Conn, b []byte) error {
	for len(b) > 0 {
		n, err := conn.Write(b)
		if err != nil {
			return err
		}
		if n <= 0 {
			return errors.New("mux: short write")
		}
		b = b[n:]
	}
	return nil
}

func signalChunk(ch *writeChunk, err error) {
	if ch == nil {
		return
	}
	select {
	case ch.done <- err:
	default:
	}
}

func muxCause(err error) error {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
		return io.ErrClosedPipe
	}
	return err
}
