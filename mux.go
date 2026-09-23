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
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Mux framing (big endian), layered on a reliable stream connection:
//
//	0        version (uint8)
//	1        command (uint8)
//	2        priority (uint8, meaningful on SYN and PSH)
//	3        reserved
//	4..7     stream id (uint32)
//	8..11    payload length (uint32)
//	12..     payload
//
// SYN payload is the sender's receive window (uint32). The peer may send that
// many data bytes before a window update. WIN is the matching initial grant
// (uint32) from the acceptor. UPD adds consumed bytes back to the send window.
// FIN half-closes the sender's write side. PSH carries ordered stream data.

const (
	muxVersion    = 1
	muxHeaderSize = 12

	muxCmdSYN byte = 1
	muxCmdFIN byte = 2
	muxCmdPSH byte = 3
	muxCmdUPD byte = 4
	muxCmdWIN byte = 5

	defaultMuxMaxFrame = 32 * 1024
	defaultMuxWindow   = 256 * 1024

	// ctrl payloads are a window integer; keep them well above that and
	// independent of MaxFrameSize so a tiny data frame size still handshakes.
	muxMaxCtrlPayload = 16
)

// MuxSide selects which stream IDs a session allocates.
// Client sessions use odd IDs (1, 3, 5, ...). Server sessions use even IDs
// (2, 4, 6, ...). The peer observes the same ID for a stream.
type MuxSide uint8

const (
	// MuxSideClient allocates odd stream IDs.
	MuxSideClient MuxSide = iota
	// MuxSideServer allocates even stream IDs.
	MuxSideServer
)

// Stream scheduling classes. Larger values preempt smaller ones.
const (
	// MuxPriorityLow is the lowest data scheduling class.
	MuxPriorityLow uint8 = iota
	// MuxPriorityNormal is the default data scheduling class.
	MuxPriorityNormal
	// MuxPriorityHigh preempts normal and low data frames.
	MuxPriorityHigh
)

// MuxConfig configures a multiplexed session. Window sizes are in bytes.
type MuxConfig struct {
	Side         MuxSide
	MaxFrameSize int
	SendWindow   int
	RecvWindow   int
}

// DefaultMuxConfig returns a client-side configuration with 32KiB frames and
// 256KiB send and receive windows.
func DefaultMuxConfig() MuxConfig {
	return MuxConfig{
		Side:         MuxSideClient,
		MaxFrameSize: defaultMuxMaxFrame,
		SendWindow:   defaultMuxWindow,
		RecvWindow:   defaultMuxWindow,
	}
}

// MuxSession carries many independent streams over one reliable connection.
type MuxSession struct {
	conn net.Conn
	cfg  MuxConfig

	mu         sync.Mutex
	acceptCond *sync.Cond
	closed     bool
	streams    map[uint32]*MuxStream
	acceptQ    []*MuxStream
	nextID     uint32

	wq writeQueue
}

// MuxStream is one ordered byte stream inside a MuxSession.
type MuxStream struct {
	sess     *MuxSession
	id       uint32
	priority uint8

	mu   sync.Mutex
	cond *sync.Cond
	wmu  sync.Mutex // serializes Write calls on this stream

	buf            []byte
	tokens         int
	localCap       int
	initialGranted bool
	deferredTokens int

	localClosed  bool
	remoteClosed bool
	sessionDead  bool
	removed      bool

	readDeadline time.Time
}

type muxFrame struct {
	bytes  []byte
	payLen int
	data   bool
	ctrl   bool
	pri    uint8
	stream *MuxStream
	done   bool
	err    error
}

// NewMuxSession starts a multiplexed session on conn.
// cfg may be nil, in which case DefaultMuxConfig is used.
// The session closes conn when Close is called.
func NewMuxSession(conn net.Conn, cfg *MuxConfig) (*MuxSession, error) {
	if conn == nil {
		return nil, errors.New("mux: nil conn")
	}
	c, err := normalizeMuxConfig(cfg)
	if err != nil {
		return nil, err
	}
	s := &MuxSession{
		conn:    conn,
		cfg:     c,
		streams: make(map[uint32]*MuxStream),
	}
	if c.Side == MuxSideServer {
		s.nextID = 2
	} else {
		s.nextID = 1
	}
	s.acceptCond = sync.NewCond(&s.mu)
	s.wq.cond = sync.NewCond(&s.wq.mu)
	go s.readLoop()
	go s.writeLoop()
	return s, nil
}

func normalizeMuxConfig(cfg *MuxConfig) (MuxConfig, error) {
	c := DefaultMuxConfig()
	if cfg != nil {
		c = *cfg
		if c.MaxFrameSize <= 0 {
			c.MaxFrameSize = defaultMuxMaxFrame
		}
		if c.SendWindow <= 0 {
			c.SendWindow = defaultMuxWindow
		}
		if c.RecvWindow <= 0 {
			c.RecvWindow = defaultMuxWindow
		}
	}
	if c.Side != MuxSideClient && c.Side != MuxSideServer {
		return MuxConfig{}, errors.New("mux: invalid side")
	}
	if c.MaxFrameSize > int(^uint32(0)) || c.RecvWindow > int(^uint32(0)) || c.SendWindow > int(^uint32(0)) {
		return MuxConfig{}, errors.New("mux: config value overflows uint32")
	}
	return c, nil
}

// Close shuts the session down and unblocks every stream operation.
// It returns as soon as shutdown is signaled. It does not wait for the
// background reader or writer, including when the underlying Write is blocked.
// The first call returns nil. Later calls return io.ErrClosedPipe.
func (s *MuxSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	s.closed = true
	streams := make([]*MuxStream, 0, len(s.streams))
	for _, st := range s.streams {
		streams = append(streams, st)
	}
	s.acceptQ = nil
	s.acceptCond.Broadcast()
	s.mu.Unlock()

	var remove []uint32
	for _, st := range streams {
		st.mu.Lock()
		st.sessionDead = true
		if st.markRemoveLocked() {
			remove = append(remove, st.id)
		}
		st.cond.Broadcast()
		st.mu.Unlock()
	}
	for _, id := range remove {
		s.deleteStream(id)
	}
	s.wq.close()
	// Unblock a Read stuck in the socket without waiting on a Write that
	// may itself be stuck. Close of the underlying conn can block if it
	// shares a lock with that Write, so it runs separately.
	go s.conn.Close()
	return nil
}

// NumStreams reports streams still tracked by the session. A stream stays
// until both sides have closed it and its buffered inbound data is drained.
func (s *MuxSession) NumStreams() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.streams)
}

// OpenStream allocates a stream and notifies the peer.
// priority should be one of the MuxPriority constants; higher values preempt
// lower ones. Either side may open streams.
func (s *MuxSession) OpenStream(priority uint8) (*MuxStream, error) {
	st := newMuxStream(s, 0, priority)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, io.ErrClosedPipe
	}
	st.id = s.nextID
	s.nextID += 2
	s.streams[st.id] = st
	atomic.AddUint64(&DefaultSnmp.MuxStreamsOpened, 1)
	s.mu.Unlock()

	if err := s.sendSYN(st); err != nil {
		s.deleteStream(st.id)
		st.mu.Lock()
		st.sessionDead = true
		st.cond.Broadcast()
		st.mu.Unlock()
		return nil, err
	}
	return st, nil
}

// AcceptStream waits for a stream opened by the peer.
func (s *MuxSession) AcceptStream() (*MuxStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for !s.closed && len(s.acceptQ) == 0 {
		s.acceptCond.Wait()
	}
	if s.closed {
		return nil, io.ErrClosedPipe
	}
	st := s.acceptQ[0]
	s.acceptQ[0] = nil
	s.acceptQ = s.acceptQ[1:]
	return st, nil
}

func (s *MuxSession) deleteStream(id uint32) {
	s.mu.Lock()
	_, ok := s.streams[id]
	if ok {
		delete(s.streams, id)
	}
	s.mu.Unlock()
	if ok {
		atomic.AddUint64(&DefaultSnmp.MuxStreamsClosed, 1)
	}
}

func (s *MuxSession) getStream(id uint32) *MuxStream {
	s.mu.Lock()
	st := s.streams[id]
	s.mu.Unlock()
	return st
}

func newMuxStream(s *MuxSession, id uint32, priority uint8) *MuxStream {
	st := &MuxStream{
		sess:     s,
		id:       id,
		priority: priority,
	}
	st.cond = sync.NewCond(&st.mu)
	return st
}

// ID returns the stream identifier shared by both peers.
func (st *MuxStream) ID() uint32 { return st.id }

// Read reads ordered bytes. A remote half-close yields io.EOF after buffered
// data is consumed. A dead session yields io.ErrClosedPipe.
func (st *MuxStream) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	n, upd, remove, err := st.read(b)
	if upd > 0 {
		_ = st.sess.sendUPD(st.id, upd)
	}
	if remove {
		st.sess.deleteStream(st.id)
	}
	return n, err
}

func (st *MuxStream) read(b []byte) (n int, windowUpdate int, remove bool, err error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		if len(st.buf) > 0 {
			n = copy(b, st.buf)
			st.buf = st.buf[n:]
			if len(st.buf) == 0 {
				st.buf = nil
			} else if cap(st.buf) > 2*len(st.buf)+4096 {
				compact := make([]byte, len(st.buf))
				copy(compact, st.buf)
				st.buf = compact
			}
			windowUpdate = n
			if len(st.buf) == 0 {
				remove = st.markRemoveLocked()
			}
			return n, windowUpdate, remove, nil
		}
		if st.sessionDead {
			remove = st.markRemoveLocked()
			return 0, 0, remove, io.ErrClosedPipe
		}
		if st.remoteClosed {
			remove = st.markRemoveLocked()
			return 0, 0, remove, io.EOF
		}
		if dl := st.readDeadline; !dl.IsZero() && !time.Now().Before(dl) {
			return 0, 0, false, os.ErrDeadlineExceeded
		}
		if dl := st.readDeadline; !dl.IsZero() {
			wait := time.Until(dl)
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(wait, func() {
				st.mu.Lock()
				st.cond.Broadcast()
				st.mu.Unlock()
			})
		}
		st.cond.Wait()
	}
}

// Write writes p in full. It blocks while the per-stream send window is
// exhausted and returns only after the bytes have been written to the
// underlying connection, or on error. A blocked write does not hold the
// session writer, so other streams continue to make progress.
func (st *MuxStream) Write(p []byte) (int, error) {
	if len(p) == 0 {
		st.mu.Lock()
		dead := st.writeClosedLocked()
		st.mu.Unlock()
		if dead {
			return 0, io.ErrClosedPipe
		}
		return 0, nil
	}

	st.wmu.Lock()
	defer st.wmu.Unlock()

	var queued []*muxFrame
	queuedBytes := 0
	for queuedBytes < len(p) {
		st.mu.Lock()
		for !st.writeClosedLocked() && st.tokens == 0 {
			st.cond.Wait()
		}
		if st.writeClosedLocked() {
			st.mu.Unlock()
			n := st.waitFrames(queued)
			if n == 0 {
				return 0, io.ErrClosedPipe
			}
			return n, io.ErrClosedPipe
		}
		n := min(st.tokens, st.sess.cfg.MaxFrameSize, len(p)-queuedBytes)
		st.tokens -= n
		payload := p[queuedBytes : queuedBytes+n]
		st.mu.Unlock()

		fr, err := st.sess.enqueue(muxCmdPSH, st.priority, st.id, payload, st, false)
		if err != nil {
			st.mu.Lock()
			st.tokens += n
			st.cond.Broadcast()
			st.mu.Unlock()
			accepted := st.waitFrames(queued)
			if accepted == 0 {
				return 0, err
			}
			return accepted, err
		}
		queued = append(queued, fr)
		queuedBytes += n
	}
	accepted := st.waitFrames(queued)
	if accepted < len(p) {
		return accepted, io.ErrClosedPipe
	}
	return accepted, nil
}

// waitFrames blocks until each frame has been written or the stream can no
// longer write. Bytes of frames that finished successfully are returned.
func (st *MuxStream) waitFrames(frames []*muxFrame) int {
	accepted := 0
	for _, fr := range frames {
		st.mu.Lock()
		for !fr.done && !st.writeClosedLocked() {
			st.cond.Wait()
		}
		ok := fr.done && fr.err == nil
		pay := fr.payLen
		st.mu.Unlock()
		if !ok {
			return accepted
		}
		accepted += pay
	}
	return accepted
}

// Close half-closes the stream: local writes stop and a FIN is queued.
// Bytes already buffered for reading remain readable until drained.
// Close unblocks writers waiting on this stream and returns without waiting
// for the FIN to hit the wire.
func (st *MuxStream) Close() error {
	st.mu.Lock()
	if st.sessionDead || st.localClosed {
		st.mu.Unlock()
		return io.ErrClosedPipe
	}
	st.localClosed = true
	remove := st.markRemoveLocked()
	st.cond.Broadcast()
	st.mu.Unlock()

	_ = st.sess.sendFIN(st.id)
	if remove {
		st.sess.deleteStream(st.id)
	}
	return nil
}

// SetReadDeadline sets the deadline for future and pending Read calls.
// A zero time clears the deadline. The error from a missed deadline
// satisfies net.Error and reports Timeout() == true.
func (st *MuxStream) SetReadDeadline(t time.Time) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.sessionDead {
		return io.ErrClosedPipe
	}
	st.readDeadline = t
	st.cond.Broadcast()
	return nil
}

func (st *MuxStream) writeClosedLocked() bool {
	return st.sessionDead || st.localClosed || st.remoteClosed
}

// markRemoveLocked records that the stream should leave the session map.
// The caller must hold st.mu. The caller deletes the map entry after releasing
// st.mu so session and stream locks are never nested.
func (st *MuxStream) markRemoveLocked() bool {
	if st.removed || len(st.buf) != 0 {
		return false
	}
	if st.sessionDead || (st.localClosed && st.remoteClosed) {
		st.removed = true
		return true
	}
	return false
}

// grantInitialLocked sets the send window from the peer's advertised receive
// window, capped by the local send window. Caller holds st.mu.
func (st *MuxStream) grantInitialLocked(peerRecv int) {
	if st.initialGranted {
		return
	}
	st.initialGranted = true
	w := peerRecv
	if w < 0 {
		w = 0
	}
	if capw := st.sess.cfg.SendWindow; w > capw {
		w = capw
	}
	st.localCap = w
	st.tokens = w + st.deferredTokens
	st.deferredTokens = 0
	if st.localCap > 0 && st.tokens > st.localCap {
		st.tokens = st.localCap
	}
	st.cond.Broadcast()
}

func (st *MuxStream) grantInitial(peerRecv int) {
	st.mu.Lock()
	st.grantInitialLocked(peerRecv)
	st.mu.Unlock()
}

func (st *MuxStream) addTokens(n int) {
	if n <= 0 {
		return
	}
	st.mu.Lock()
	if !st.initialGranted {
		st.deferredTokens += n
		st.cond.Broadcast()
		st.mu.Unlock()
		return
	}
	st.tokens += n
	if st.localCap > 0 && st.tokens > st.localCap {
		st.tokens = st.localCap
	}
	st.cond.Broadcast()
	st.mu.Unlock()
}

func (st *MuxStream) finishFrame(fr *muxFrame, err error) {
	st.mu.Lock()
	if !fr.done {
		fr.done = true
		fr.err = err
	}
	st.cond.Broadcast()
	st.mu.Unlock()
}

func (st *MuxStream) appendPayload(p []byte) {
	if len(p) == 0 {
		return
	}
	st.buf = append(st.buf, p...)
}

func (s *MuxSession) sendSYN(st *MuxStream) error {
	var payload [4]byte
	binary.BigEndian.PutUint32(payload[:], uint32(s.cfg.RecvWindow))
	_, err := s.enqueue(muxCmdSYN, st.priority, st.id, payload[:], nil, true)
	return err
}

func (s *MuxSession) sendWIN(id uint32) error {
	var payload [4]byte
	binary.BigEndian.PutUint32(payload[:], uint32(s.cfg.RecvWindow))
	_, err := s.enqueue(muxCmdWIN, 0, id, payload[:], nil, true)
	return err
}

func (s *MuxSession) sendFIN(id uint32) error {
	_, err := s.enqueue(muxCmdFIN, 0, id, nil, nil, true)
	return err
}

func (s *MuxSession) sendUPD(id uint32, n int) error {
	if n <= 0 {
		return nil
	}
	var payload [4]byte
	binary.BigEndian.PutUint32(payload[:], uint32(n))
	_, err := s.enqueue(muxCmdUPD, 0, id, payload[:], nil, true)
	return err
}

func (s *MuxSession) enqueue(cmd byte, pri uint8, id uint32, payload []byte, st *MuxStream, ctrl bool) (*muxFrame, error) {
	fr := &muxFrame{
		bytes:  encodeMuxFrame(cmd, pri, id, payload),
		payLen: len(payload),
		data:   cmd == muxCmdPSH,
		ctrl:   ctrl,
		pri:    pri,
		stream: st,
	}
	if err := s.wq.push(fr); err != nil {
		return nil, err
	}
	return fr, nil
}

func encodeMuxFrame(cmd byte, pri uint8, id uint32, payload []byte) []byte {
	buf := make([]byte, muxHeaderSize+len(payload))
	buf[0] = muxVersion
	buf[1] = cmd
	buf[2] = pri
	binary.BigEndian.PutUint32(buf[4:8], id)
	binary.BigEndian.PutUint32(buf[8:12], uint32(len(payload)))
	copy(buf[muxHeaderSize:], payload)
	return buf
}

func (s *MuxSession) readLoop() {
	var hdr [muxHeaderSize]byte
	for {
		if _, err := io.ReadFull(s.conn, hdr[:]); err != nil {
			_ = s.Close()
			return
		}
		if hdr[0] != muxVersion {
			_ = s.Close()
			return
		}
		cmd := hdr[1]
		pri := hdr[2]
		id := binary.BigEndian.Uint32(hdr[4:8])
		n := binary.BigEndian.Uint32(hdr[8:12])
		if cmd == muxCmdPSH {
			if n > uint32(s.cfg.MaxFrameSize) {
				_ = s.Close()
				return
			}
		} else if n > muxMaxCtrlPayload {
			_ = s.Close()
			return
		}
		var payload []byte
		if n > 0 {
			payload = make([]byte, n)
			if _, err := io.ReadFull(s.conn, payload); err != nil {
				_ = s.Close()
				return
			}
		}
		atomic.AddUint64(&DefaultSnmp.MuxFramesReceived, 1)
		if cmd == muxCmdPSH {
			atomic.AddUint64(&DefaultSnmp.MuxBytesReceived, uint64(n))
		}
		if !s.handleFrame(cmd, pri, id, payload) {
			_ = s.Close()
			return
		}
	}
}

func (s *MuxSession) handleFrame(cmd byte, pri uint8, id uint32, payload []byte) bool {
	switch cmd {
	case muxCmdSYN:
		return s.handleSYN(pri, id, payload)
	case muxCmdFIN:
		s.handleFIN(id)
		return true
	case muxCmdPSH:
		s.handlePSH(id, payload)
		return true
	case muxCmdUPD:
		return s.handleUPD(id, payload)
	case muxCmdWIN:
		return s.handleWIN(id, payload)
	default:
		return true
	}
}

func (s *MuxSession) handleSYN(pri uint8, id uint32, payload []byte) bool {
	if len(payload) != 4 {
		return false
	}
	peerRecv := int(binary.BigEndian.Uint32(payload))
	st := newMuxStream(s, id, pri)
	st.grantInitial(peerRecv)
	// Queue the initial window before the stream is visible so this grant
	// cannot line up behind data the acceptor writes immediately.
	if err := s.sendWIN(id); err != nil {
		return s.isClosed()
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return true
	}
	if _, exists := s.streams[id]; exists {
		s.mu.Unlock()
		return true
	}
	s.streams[id] = st
	s.acceptQ = append(s.acceptQ, st)
	atomic.AddUint64(&DefaultSnmp.MuxStreamsOpened, 1)
	s.acceptCond.Broadcast()
	s.mu.Unlock()
	return true
}

func (s *MuxSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *MuxSession) handleFIN(id uint32) {
	st := s.getStream(id)
	if st == nil {
		return
	}
	st.mu.Lock()
	st.remoteClosed = true
	remove := st.markRemoveLocked()
	st.cond.Broadcast()
	st.mu.Unlock()
	if remove {
		s.deleteStream(id)
	}
}

func (s *MuxSession) handlePSH(id uint32, payload []byte) {
	st := s.getStream(id)
	if st == nil || len(payload) == 0 {
		return
	}
	st.mu.Lock()
	if st.remoteClosed || st.sessionDead {
		st.mu.Unlock()
		return
	}
	st.appendPayload(payload)
	st.cond.Broadcast()
	st.mu.Unlock()
}

func (s *MuxSession) handleUPD(id uint32, payload []byte) bool {
	if len(payload) != 4 {
		return false
	}
	st := s.getStream(id)
	if st == nil {
		return true
	}
	st.addTokens(int(binary.BigEndian.Uint32(payload)))
	return true
}

func (s *MuxSession) handleWIN(id uint32, payload []byte) bool {
	if len(payload) != 4 {
		return false
	}
	st := s.getStream(id)
	if st == nil {
		return true
	}
	st.grantInitial(int(binary.BigEndian.Uint32(payload)))
	return true
}

func (s *MuxSession) writeLoop() {
	for {
		fr, ok := s.wq.pop()
		if !ok {
			return
		}
		if fr.stream != nil && !fr.ctrl && fr.stream.dropDataLocked() {
			fr.stream.finishFrame(fr, io.ErrClosedPipe)
			continue
		}
		err := writeFull(s.conn, fr.bytes)
		if err != nil {
			if fr.stream != nil {
				fr.stream.finishFrame(fr, io.ErrClosedPipe)
			}
			_ = s.Close()
			return
		}
		atomic.AddUint64(&DefaultSnmp.MuxFramesSent, 1)
		if fr.data {
			atomic.AddUint64(&DefaultSnmp.MuxBytesSent, uint64(fr.payLen))
		}
		if fr.stream != nil {
			fr.stream.finishFrame(fr, nil)
		}
	}
}

func (st *MuxStream) dropDataLocked() bool {
	st.mu.Lock()
	drop := st.writeClosedLocked()
	st.mu.Unlock()
	return drop
}

func writeFull(conn net.Conn, b []byte) error {
	for len(b) > 0 {
		n, err := conn.Write(b)
		if n > 0 {
			b = b[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// writeQueue schedules control frames ahead of data, and higher-priority
// data ahead of lower-priority data. Pop is FIFO inside one class.
type writeQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	closed bool
	ctrl   frameQueue
	data   [3]frameQueue // 0 high, 1 normal, 2 low
}

func (q *writeQueue) push(fr *muxFrame) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return io.ErrClosedPipe
	}
	if fr.ctrl {
		q.ctrl.push(fr)
	} else {
		q.data[muxPriorityBucket(fr.pri)].push(fr)
	}
	q.cond.Signal()
	return nil
}

func (q *writeQueue) pop() (*muxFrame, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for {
		if fr := q.ctrl.pop(); fr != nil {
			return fr, true
		}
		for i := range q.data {
			if fr := q.data[i].pop(); fr != nil {
				return fr, true
			}
		}
		if q.closed {
			return nil, false
		}
		q.cond.Wait()
	}
}

func (q *writeQueue) close() {
	q.mu.Lock()
	var frames []*muxFrame
	frames = append(frames, q.ctrl.drain()...)
	for i := range q.data {
		frames = append(frames, q.data[i].drain()...)
	}
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
	for _, fr := range frames {
		if fr.stream != nil {
			fr.stream.finishFrame(fr, io.ErrClosedPipe)
		}
	}
}

func muxPriorityBucket(p uint8) int {
	switch {
	case p >= MuxPriorityHigh:
		return 0
	case p >= MuxPriorityNormal:
		return 1
	default:
		return 2
	}
}

type frameQueue struct {
	items []*muxFrame
	head  int
}

func (q *frameQueue) len() int {
	return len(q.items) - q.head
}

func (q *frameQueue) push(fr *muxFrame) {
	q.items = append(q.items, fr)
}

func (q *frameQueue) pop() *muxFrame {
	if q.head >= len(q.items) {
		return nil
	}
	fr := q.items[q.head]
	q.items[q.head] = nil
	q.head++
	if q.head == len(q.items) {
		q.items = q.items[:0]
		q.head = 0
	} else if q.head > 32 && q.head*2 >= len(q.items) {
		n := copy(q.items, q.items[q.head:])
		for i := n; i < len(q.items); i++ {
			q.items[i] = nil
		}
		q.items = q.items[:n]
		q.head = 0
	}
	return fr
}

func (q *frameQueue) drain() []*muxFrame {
	var out []*muxFrame
	for {
		fr := q.pop()
		if fr == nil {
			return out
		}
		out = append(out, fr)
	}
}

// pendingDataCount reports queued data frames. It is used by tests to wait
// until lower-priority traffic is actually sitting in the scheduler.
func (s *MuxSession) pendingDataCount() int {
	s.wq.mu.Lock()
	defer s.wq.mu.Unlock()
	n := 0
	for i := range s.wq.data {
		n += s.wq.data[i].len()
	}
	return n
}

// pendingCtrlCount reports queued control frames.
func (s *MuxSession) pendingCtrlCount() int {
	s.wq.mu.Lock()
	defer s.wq.mu.Unlock()
	return s.wq.ctrl.len()
}
