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
	"io"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
)

// MuxSide determines which half of the stream ID space a MuxSession allocates from.
type MuxSide uint8

const (
	// MuxSideClient opens streams with odd IDs (1, 3, 5, ...).
	MuxSideClient MuxSide = iota
	// MuxSideServer opens streams with even IDs (2, 4, 6, ...).
	MuxSideServer
)

// Stream priorities; smaller values are scheduled first.
const (
	MuxPriorityHigh   uint8 = 0
	MuxPriorityNormal uint8 = 1
	MuxPriorityLow    uint8 = 2
)

const (
	muxVersion    = 1
	muxHeaderSize = 8 // ver(1) cmd(1) length(2) streamID(4)
	muxMaxPayload = math.MaxUint16

	muxCmdSettings = 0 // payload: sendWindow(4) recvWindow(4), streamID 0
	muxCmdSYN      = 1 // payload: priority(1)
	muxCmdFIN      = 2
	muxCmdPSH      = 3 // payload: data
	muxCmdUPD      = 4 // payload: credit increment(4)

	muxAcceptBacklog = 1024
	muxNumPriorities = int(MuxPriorityLow) + 1
)

var (
	errMuxInvalidConfig   = errors.New("mux: invalid config")
	errMuxInvalidProtocol = errors.New("mux: invalid protocol")
	errMuxStreamIDExhaust = errors.New("mux: stream id exhausted")
)

// MuxConfig configures a MuxSession.
type MuxConfig struct {
	// Side selects the stream ID space; the two peers must use opposite sides.
	Side MuxSide
	// MaxFrameSize is the maximum data payload per frame, at most 65535.
	MaxFrameSize int
	// SendWindow is the maximum number of unacknowledged bytes in flight per stream.
	SendWindow int
	// RecvWindow is the maximum number of bytes buffered per stream, advertised to the peer.
	RecvWindow int
}

// DefaultMuxConfig returns a client-side configuration with sensible defaults.
func DefaultMuxConfig() MuxConfig {
	return MuxConfig{
		Side:         MuxSideClient,
		MaxFrameSize: 32 * 1024,
		SendWindow:   256 * 1024,
		RecvWindow:   256 * 1024,
	}
}

func (c *MuxConfig) validate() error {
	if c.Side != MuxSideClient && c.Side != MuxSideServer {
		return errors.Wrap(errMuxInvalidConfig, "unknown side")
	}
	if c.MaxFrameSize <= 0 || c.MaxFrameSize > muxMaxPayload {
		return errors.Wrap(errMuxInvalidConfig, "MaxFrameSize must be in (0, 65535]")
	}
	if c.SendWindow <= 0 || c.SendWindow > math.MaxInt32 {
		return errors.Wrap(errMuxInvalidConfig, "SendWindow must be in (0, MaxInt32]")
	}
	if c.RecvWindow <= 0 || c.RecvWindow > math.MaxInt32 {
		return errors.Wrap(errMuxInvalidConfig, "RecvWindow must be in (0, MaxInt32]")
	}
	return nil
}

type muxTimeoutError struct{}

func (muxTimeoutError) Error() string   { return "mux: i/o timeout" }
func (muxTimeoutError) Timeout() bool   { return true }
func (muxTimeoutError) Temporary() bool { return true }

var _ net.Error = muxTimeoutError{}

type muxFrame struct {
	buf    []byte
	stream *MuxStream // set for data frames, used to order a pending FIN after them
}

func newMuxFrame(cmd byte, sid uint32, payloadLen int) *muxFrame {
	buf := make([]byte, muxHeaderSize+payloadLen)
	buf[0] = muxVersion
	buf[1] = cmd
	binary.LittleEndian.PutUint16(buf[2:], uint16(payloadLen))
	binary.LittleEndian.PutUint32(buf[4:], sid)
	return &muxFrame{buf: buf}
}

// MuxSession multiplexes many independent, ordered streams over a single net.Conn.
type MuxSession struct {
	conn net.Conn
	cfg  MuxConfig

	mu             sync.Mutex
	streams        map[uint32]*MuxStream
	nextID         uint32
	peerSendWindow int
	peerRecvWindow int

	sendMu    sync.Mutex
	ctrlQueue []*muxFrame
	dataQueue [muxNumPriorities][]*muxFrame
	sendReady chan struct{}

	acceptCh chan *MuxStream

	die     chan struct{}
	dieOnce sync.Once
}

// NewMuxSession wraps conn with a stream multiplexer. A nil cfg uses DefaultMuxConfig.
func NewMuxSession(conn net.Conn, cfg *MuxConfig) (*MuxSession, error) {
	if conn == nil {
		return nil, errors.New("mux: nil conn")
	}
	c := DefaultMuxConfig()
	if cfg != nil {
		c = *cfg
	}
	if err := c.validate(); err != nil {
		return nil, err
	}

	m := &MuxSession{
		conn:      conn,
		cfg:       c,
		streams:   make(map[uint32]*MuxStream),
		sendReady: make(chan struct{}, 1),
		acceptCh:  make(chan *MuxStream, muxAcceptBacklog),
		die:       make(chan struct{}),
		// assume a symmetric peer until its settings frame arrives
		peerSendWindow: c.SendWindow,
		peerRecvWindow: c.RecvWindow,
	}
	if c.Side == MuxSideClient {
		m.nextID = 1
	} else {
		m.nextID = 2
	}

	f := newMuxFrame(muxCmdSettings, 0, 8)
	binary.LittleEndian.PutUint32(f.buf[muxHeaderSize:], uint32(c.SendWindow))
	binary.LittleEndian.PutUint32(f.buf[muxHeaderSize+4:], uint32(c.RecvWindow))
	m.queueCtrl(f)

	go m.sendLoop()
	go m.recvLoop()
	return m, nil
}

// OpenStream opens a new stream with the given priority.
func (m *MuxSession) OpenStream(priority uint8) (*MuxStream, error) {
	if priority > MuxPriorityLow {
		priority = MuxPriorityLow
	}

	m.mu.Lock()
	if m.isClosed() {
		m.mu.Unlock()
		return nil, io.ErrClosedPipe
	}
	id := m.nextID
	if id == 0 || id > math.MaxUint32-2 {
		m.mu.Unlock()
		return nil, errMuxStreamIDExhaust
	}
	m.nextID += 2
	s := newMuxStream(m, id, priority, m.initialSendCreditLocked())
	m.streams[id] = s
	// queue SYN while holding m.mu so it precedes any frame of this stream
	f := newMuxFrame(muxCmdSYN, id, 1)
	f.buf[muxHeaderSize] = priority
	m.queueCtrl(f)
	m.mu.Unlock()

	atomic.AddUint64(&DefaultSnmp.MuxStreamsOpened, 1)
	return s, nil
}

// AcceptStream waits for and returns the next stream opened by the peer.
func (m *MuxSession) AcceptStream() (*MuxStream, error) {
	select {
	case s := <-m.acceptCh:
		return s, nil
	case <-m.die:
		return nil, io.ErrClosedPipe
	}
}

// NumStreams returns the number of streams currently tracked by the session.
func (m *MuxSession) NumStreams() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.streams)
}

// Close shuts down the session and all its streams. It does not wait for
// queued frames to be flushed or for the underlying connection to close.
func (m *MuxSession) Close() error {
	closed := false
	m.dieOnce.Do(func() {
		close(m.die)
		closed = true
	})
	if !closed {
		return io.ErrClosedPipe
	}

	m.mu.Lock()
	n := len(m.streams)
	m.streams = make(map[uint32]*MuxStream)
	m.mu.Unlock()
	atomic.AddUint64(&DefaultSnmp.MuxStreamsClosed, uint64(n))

	m.sendMu.Lock()
	m.ctrlQueue = nil
	for i := range m.dataQueue {
		m.dataQueue[i] = nil
	}
	m.sendMu.Unlock()

	// conn.Close may block behind an in-flight Write on some transports.
	go m.conn.Close()
	return nil
}

func (m *MuxSession) isClosed() bool {
	select {
	case <-m.die:
		return true
	default:
		return false
	}
}

func (m *MuxSession) initialSendCreditLocked() int {
	return min(m.cfg.SendWindow, m.peerRecvWindow)
}

// updateThreshold is the number of drained bytes after which a receiver returns credit.
func (m *MuxSession) updateThreshold() int {
	m.mu.Lock()
	w := min(m.peerSendWindow, m.cfg.RecvWindow)
	m.mu.Unlock()
	return max(w/2, 1)
}

func (m *MuxSession) notifySend() {
	select {
	case m.sendReady <- struct{}{}:
	default:
	}
}

func (m *MuxSession) queueCtrl(f *muxFrame) {
	m.sendMu.Lock()
	m.ctrlQueue = append(m.ctrlQueue, f)
	m.sendMu.Unlock()
	m.notifySend()
}

func (m *MuxSession) queueFIN(sid uint32) {
	m.queueCtrl(newMuxFrame(muxCmdFIN, sid, 0))
}

func (m *MuxSession) queueUPD(sid uint32, n int) {
	f := newMuxFrame(muxCmdUPD, sid, 4)
	binary.LittleEndian.PutUint32(f.buf[muxHeaderSize:], uint32(n))
	m.queueCtrl(f)
}

// queueData must be called with s.mu held so it is ordered against s.Close.
func (m *MuxSession) queueData(s *MuxStream, p []byte) {
	f := newMuxFrame(muxCmdPSH, s.id, len(p))
	copy(f.buf[muxHeaderSize:], p)
	f.stream = s
	m.sendMu.Lock()
	m.dataQueue[s.priority] = append(m.dataQueue[s.priority], f)
	s.pendingFrames++
	m.sendMu.Unlock()
	m.notifySend()
}

// closeStreamSend sends FIN for s once all of its already queued data has been sent.
// Must be called with s.mu held.
func (m *MuxSession) closeStreamSend(s *MuxStream) {
	m.sendMu.Lock()
	if s.pendingFrames > 0 {
		s.finPending = true
		m.sendMu.Unlock()
		return
	}
	m.sendMu.Unlock()
	m.queueFIN(s.id)
}

// popFrame returns control frames first, then data by descending priority.
func (m *MuxSession) popFrame() *muxFrame {
	m.sendMu.Lock()
	defer m.sendMu.Unlock()
	if len(m.ctrlQueue) > 0 {
		f := m.ctrlQueue[0]
		m.ctrlQueue[0] = nil
		m.ctrlQueue = m.ctrlQueue[1:]
		return f
	}
	for i := range m.dataQueue {
		q := m.dataQueue[i]
		if len(q) == 0 {
			continue
		}
		f := q[0]
		q[0] = nil
		m.dataQueue[i] = q[1:]
		s := f.stream
		s.pendingFrames--
		if s.pendingFrames == 0 && s.finPending {
			s.finPending = false
			m.ctrlQueue = append(m.ctrlQueue, newMuxFrame(muxCmdFIN, s.id, 0))
		}
		return f
	}
	return nil
}

func (m *MuxSession) sendLoop() {
	for {
		if m.isClosed() {
			return
		}
		f := m.popFrame()
		if f == nil {
			select {
			case <-m.sendReady:
				continue
			case <-m.die:
				return
			}
		}

		atomic.AddUint64(&DefaultSnmp.MuxFramesSent, 1)
		if f.buf[1] == muxCmdPSH {
			atomic.AddUint64(&DefaultSnmp.MuxBytesSent, uint64(len(f.buf)-muxHeaderSize))
		}
		if _, err := m.conn.Write(f.buf); err != nil {
			m.Close()
			return
		}
	}
}

func (m *MuxSession) recvLoop() {
	defer m.Close()
	var hdr [muxHeaderSize]byte
	for {
		if _, err := io.ReadFull(m.conn, hdr[:]); err != nil {
			return
		}
		if hdr[0] != muxVersion {
			return
		}
		cmd := hdr[1]
		length := int(binary.LittleEndian.Uint16(hdr[2:]))
		sid := binary.LittleEndian.Uint32(hdr[4:])
		var payload []byte
		if length > 0 {
			payload = make([]byte, length)
			if _, err := io.ReadFull(m.conn, payload); err != nil {
				return
			}
		}
		atomic.AddUint64(&DefaultSnmp.MuxFramesReceived, 1)
		if err := m.handleFrame(cmd, sid, payload); err != nil {
			return
		}
	}
}

func (m *MuxSession) handleFrame(cmd byte, sid uint32, payload []byte) error {
	switch cmd {
	case muxCmdSettings:
		if len(payload) != 8 {
			return errMuxInvalidProtocol
		}
		m.applySettings(int(binary.LittleEndian.Uint32(payload)), int(binary.LittleEndian.Uint32(payload[4:])))
	case muxCmdSYN:
		if len(payload) != 1 {
			return errMuxInvalidProtocol
		}
		m.handleSYN(sid, payload[0])
	case muxCmdFIN:
		if s := m.getStream(sid); s != nil {
			s.handleFIN()
		}
	case muxCmdPSH:
		atomic.AddUint64(&DefaultSnmp.MuxBytesReceived, uint64(len(payload)))
		if s := m.getStream(sid); s != nil && len(payload) > 0 {
			s.pushData(payload)
		}
	case muxCmdUPD:
		if len(payload) != 4 {
			return errMuxInvalidProtocol
		}
		if s := m.getStream(sid); s != nil {
			s.addCredit(int(binary.LittleEndian.Uint32(payload)))
		}
	default:
		return errMuxInvalidProtocol
	}
	return nil
}

func (m *MuxSession) getStream(sid uint32) *MuxStream {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.streams[sid]
}

func (m *MuxSession) applySettings(peerSend, peerRecv int) {
	if peerSend <= 0 || peerRecv <= 0 {
		return
	}
	m.mu.Lock()
	oldInit := m.initialSendCreditLocked()
	m.peerSendWindow = peerSend
	m.peerRecvWindow = peerRecv
	delta := m.initialSendCreditLocked() - oldInit
	streams := make([]*MuxStream, 0, len(m.streams))
	for _, s := range m.streams {
		streams = append(streams, s)
	}
	m.mu.Unlock()

	if delta != 0 {
		for _, s := range streams {
			s.addCredit(delta)
		}
	}
}

func (m *MuxSession) handleSYN(sid uint32, priority uint8) {
	if sid == 0 {
		return
	}
	remoteOdd := m.cfg.Side == MuxSideServer
	if (sid%2 == 1) != remoteOdd {
		return
	}
	if priority > MuxPriorityLow {
		priority = MuxPriorityLow
	}

	m.mu.Lock()
	if m.isClosed() {
		m.mu.Unlock()
		return
	}
	if _, ok := m.streams[sid]; ok {
		m.mu.Unlock()
		return
	}
	s := newMuxStream(m, sid, priority, m.initialSendCreditLocked())
	select {
	case m.acceptCh <- s:
		m.streams[sid] = s
		m.mu.Unlock()
		atomic.AddUint64(&DefaultSnmp.MuxStreamsOpened, 1)
	default:
		m.mu.Unlock()
		m.queueFIN(sid)
	}
}

func (m *MuxSession) removeStream(s *MuxStream) {
	m.mu.Lock()
	cur, ok := m.streams[s.id]
	if ok && cur == s {
		delete(m.streams, s.id)
	}
	m.mu.Unlock()
	if ok && cur == s {
		atomic.AddUint64(&DefaultSnmp.MuxStreamsClosed, 1)
	}
}

// MuxStream is a single ordered, flow-controlled byte stream within a MuxSession.
type MuxStream struct {
	sess     *MuxSession
	id       uint32
	priority uint8

	mu           sync.Mutex
	recvBuf      [][]byte
	unacked      int // bytes drained by Read but not yet returned to the peer as credit
	credit       int
	localClosed  bool
	remoteClosed bool
	removed      bool

	// guarded by sess.sendMu
	pendingFrames int
	finPending    bool

	readDeadline atomic.Value // time.Time
	readEvent    chan struct{}
	writeEvent   chan struct{}
}

func newMuxStream(sess *MuxSession, id uint32, priority uint8, credit int) *MuxStream {
	s := &MuxStream{
		sess:       sess,
		id:         id,
		priority:   priority,
		credit:     credit,
		readEvent:  make(chan struct{}, 1),
		writeEvent: make(chan struct{}, 1),
	}
	s.readDeadline.Store(time.Time{})
	return s
}

// ID returns the stream identifier, identical on both peers.
func (s *MuxStream) ID() uint32 { return s.id }

func notify(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// Read reads buffered stream data. It returns io.EOF once the peer has closed
// its side and all data has been drained.
func (s *MuxStream) Read(p []byte) (int, error) {
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		s.mu.Lock()
		if len(s.recvBuf) > 0 {
			n := 0
			for n < len(p) && len(s.recvBuf) > 0 {
				c := copy(p[n:], s.recvBuf[0])
				n += c
				if c == len(s.recvBuf[0]) {
					s.recvBuf[0] = nil
					s.recvBuf = s.recvBuf[1:]
				} else {
					s.recvBuf[0] = s.recvBuf[0][c:]
				}
			}
			var upd int
			if !s.remoteClosed {
				s.unacked += n
				if s.unacked >= s.sess.updateThreshold() {
					upd = s.unacked
					s.unacked = 0
				}
			}
			remove := s.checkRemoveLocked()
			more := len(s.recvBuf) > 0
			s.mu.Unlock()
			if more {
				notify(s.readEvent)
			}
			if upd > 0 {
				s.sess.queueUPD(s.id, upd)
			}
			if remove {
				s.sess.removeStream(s)
			}
			return n, nil
		}
		localClosed, remoteClosed := s.localClosed, s.remoteClosed
		s.mu.Unlock()

		if localClosed || s.sess.isClosed() {
			notify(s.readEvent)
			return 0, io.ErrClosedPipe
		}
		if remoteClosed {
			notify(s.readEvent)
			return 0, io.EOF
		}
		if len(p) == 0 {
			return 0, nil
		}

		var timeout <-chan time.Time
		if d := s.readDeadline.Load().(time.Time); !d.IsZero() {
			wait := time.Until(d)
			if wait <= 0 {
				return 0, muxTimeoutError{}
			}
			if timer == nil {
				timer = time.NewTimer(wait)
			} else {
				timer.Reset(wait)
			}
			timeout = timer.C
		}

		select {
		case <-s.readEvent:
		case <-s.sess.die:
		case <-timeout:
			return 0, muxTimeoutError{}
		}
	}
}

// Write queues all of p for transmission, blocking while the stream's send
// window is exhausted. It only returns a short count together with an error.
func (s *MuxStream) Write(p []byte) (int, error) {
	written := 0
	for {
		s.mu.Lock()
		if s.localClosed || s.remoteClosed || s.sess.isClosed() {
			s.mu.Unlock()
			notify(s.writeEvent)
			return written, io.ErrClosedPipe
		}
		if written == len(p) {
			more := s.credit > 0
			s.mu.Unlock()
			if more {
				notify(s.writeEvent)
			}
			return written, nil
		}
		if s.credit > 0 {
			n := min(len(p)-written, s.credit, s.sess.cfg.MaxFrameSize)
			s.credit -= n
			s.sess.queueData(s, p[written:written+n])
			s.mu.Unlock()
			written += n
			continue
		}
		s.mu.Unlock()

		select {
		case <-s.writeEvent:
		case <-s.sess.die:
		}
	}
}

// Close half-closes the stream: no more data can be written, while inbound
// data already buffered can still be read.
func (s *MuxStream) Close() error {
	s.mu.Lock()
	if s.localClosed {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	s.localClosed = true
	if !s.sess.isClosed() {
		s.sess.closeStreamSend(s)
	}
	remove := s.checkRemoveLocked()
	s.mu.Unlock()

	notify(s.writeEvent)
	notify(s.readEvent)
	if remove {
		s.sess.removeStream(s)
	}
	return nil
}

// SetReadDeadline sets the deadline for pending and future Read calls.
// A zero value disables the deadline.
func (s *MuxStream) SetReadDeadline(t time.Time) error {
	s.readDeadline.Store(t)
	notify(s.readEvent)
	return nil
}

func (s *MuxStream) checkRemoveLocked() bool {
	if !s.removed && s.localClosed && s.remoteClosed && len(s.recvBuf) == 0 {
		s.removed = true
		return true
	}
	return false
}

func (s *MuxStream) pushData(p []byte) {
	s.mu.Lock()
	if s.remoteClosed {
		s.mu.Unlock()
		return
	}
	s.recvBuf = append(s.recvBuf, p)
	s.mu.Unlock()
	notify(s.readEvent)
}

func (s *MuxStream) handleFIN() {
	s.mu.Lock()
	if s.remoteClosed {
		s.mu.Unlock()
		return
	}
	s.remoteClosed = true
	remove := s.checkRemoveLocked()
	s.mu.Unlock()

	notify(s.readEvent)
	notify(s.writeEvent)
	if remove {
		s.sess.removeStream(s)
	}
}

func (s *MuxStream) addCredit(n int) {
	s.mu.Lock()
	s.credit += n
	s.mu.Unlock()
	notify(s.writeEvent)
}
