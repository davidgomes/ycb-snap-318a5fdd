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
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// MuxSide identifies which peer allocates which stream IDs.
type MuxSide uint8

const (
	// MuxSideClient allocates odd stream IDs (1, 3, 5, ...).
	MuxSideClient MuxSide = iota + 1
	// MuxSideServer allocates even stream IDs (2, 4, 6, ...).
	MuxSideServer
)

const (
	// MuxPriorityHigh is served before normal and low traffic.
	MuxPriorityHigh uint8 = iota
	// MuxPriorityNormal is the default scheduling class.
	MuxPriorityNormal
	// MuxPriorityLow is served when no higher-priority frame is queued.
	MuxPriorityLow
)

const (
	muxCmdOpen byte = iota + 1
	muxCmdData
	muxCmdWindow
	muxCmdClose
)

const muxHeaderLen = 10

// MuxConfig configures a multiplexed session.
type MuxConfig struct {
	Side         MuxSide
	MaxFrameSize int
	SendWindow   int
	RecvWindow   int
}

// DefaultMuxConfig returns conservative defaults for a client-side session.
func DefaultMuxConfig() MuxConfig {
	return MuxConfig{
		Side:         MuxSideClient,
		MaxFrameSize: 16 * 1024,
		SendWindow:   256 * 1024,
		RecvWindow:   256 * 1024,
	}
}

// MuxSession carries many independent streams over one net.Conn.
type MuxSession struct {
	conn net.Conn
	cfg  MuxConfig

	mu       sync.Mutex
	streams  map[uint32]*MuxStream
	incoming []*MuxStream
	acceptC  *sync.Cond
	closed   bool
	nextID   uint32

	schedMu   sync.Mutex
	schedC    *sync.Cond
	ctrl      [][]byte
	buckets   [3][][]byte
	schedDone bool

	die  chan struct{}
	once sync.Once
}

// MuxStream is one ordered, flow-controlled byte stream inside a MuxSession.
type MuxStream struct {
	id       uint32
	priority uint8
	sess     *MuxSession

	mu   sync.Mutex
	cond *sync.Cond

	rbuf           bytes.Buffer
	sendCredit     int
	bytesEnqueued  uint64
	bytesReceived  uint64
	closeAt        uint64
	remoteClosed   bool
	localClosed    bool
	sessDead       bool
	removed        bool
	readDeadline   time.Time
	closeAccounted bool
}

type muxTimeoutError struct{}

func (muxTimeoutError) Error() string   { return "mux: i/o timeout" }
func (muxTimeoutError) Timeout() bool   { return true }
func (muxTimeoutError) Temporary() bool { return true }

// NewMuxSession starts a multiplexed session over conn.
// Close returns without waiting for the read or write loops.
func NewMuxSession(conn net.Conn, cfg *MuxConfig) (*MuxSession, error) {
	if conn == nil {
		return nil, io.ErrClosedPipe
	}
	c := DefaultMuxConfig()
	if cfg != nil {
		c = *cfg
		if c.MaxFrameSize <= 0 {
			c.MaxFrameSize = DefaultMuxConfig().MaxFrameSize
		}
		if c.SendWindow <= 0 {
			c.SendWindow = DefaultMuxConfig().SendWindow
		}
		if c.RecvWindow <= 0 {
			c.RecvWindow = DefaultMuxConfig().RecvWindow
		}
		if c.Side != MuxSideClient && c.Side != MuxSideServer {
			c.Side = MuxSideClient
		}
	}
	s := &MuxSession{
		conn:    conn,
		cfg:     c,
		streams: make(map[uint32]*MuxStream),
		die:     make(chan struct{}),
	}
	if c.Side == MuxSideServer {
		s.nextID = 2
	} else {
		s.nextID = 1
	}
	s.acceptC = sync.NewCond(&s.mu)
	s.schedC = sync.NewCond(&s.schedMu)
	go s.readLoop()
	go s.writeLoop()
	return s, nil
}

// Close shuts the session down and returns immediately.
// It does not wait for background loops, even if conn.Write is blocked.
func (s *MuxSession) Close() error {
	s.once.Do(func() {
		close(s.die)
		s.mu.Lock()
		s.closed = true
		for _, st := range s.streams {
			st.mu.Lock()
			st.sessDead = true
			st.cond.Broadcast()
			st.mu.Unlock()
		}
		s.acceptC.Broadcast()
		s.mu.Unlock()

		s.schedMu.Lock()
		s.schedDone = true
		s.schedC.Broadcast()
		s.schedMu.Unlock()

		// Unblock a stuck conn.Write/Read. Do not join the loops.
		_ = s.conn.Close()
	})
	return nil
}

// NumStreams reports streams still tracked by the session.
func (s *MuxSession) NumStreams() int {
	s.mu.Lock()
	n := len(s.streams)
	s.mu.Unlock()
	return n
}

// OpenStream allocates a stream. Either side may call it.
// Client IDs are odd and server IDs are even; both peers observe the same ID.
func (s *MuxSession) OpenStream(priority uint8) (*MuxStream, error) {
	if priority > MuxPriorityLow {
		priority = MuxPriorityLow
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, io.ErrClosedPipe
	}
	id := atomic.AddUint32(&s.nextID, 2) - 2
	st := newMuxStream(s, id, priority)
	s.streams[id] = st
	s.mu.Unlock()

	atomic.AddUint64(&DefaultSnmp.MuxStreamsOpened, 1)
	if err := s.enqueue(muxCmdOpen, priority, id, nil, true); err != nil {
		s.dropStream(st)
		return nil, err
	}
	return st, nil
}

// AcceptStream waits for a stream opened by the remote peer.
func (s *MuxSession) AcceptStream() (*MuxStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.incoming) == 0 && !s.closed {
		s.acceptC.Wait()
	}
	if len(s.incoming) == 0 {
		return nil, io.ErrClosedPipe
	}
	st := s.incoming[0]
	s.incoming = s.incoming[1:]
	return st, nil
}

func newMuxStream(s *MuxSession, id uint32, priority uint8) *MuxStream {
	st := &MuxStream{
		id:         id,
		priority:   priority,
		sess:       s,
		sendCredit: s.cfg.SendWindow,
	}
	st.cond = sync.NewCond(&st.mu)
	return st
}

// ID returns the stream identifier shared by both peers.
func (st *MuxStream) ID() uint32 { return st.id }

// Read reads the next bytes from the stream.
// A read deadline expiry returns an error satisfying net.Error with Timeout() == true.
func (st *MuxStream) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	for {
		if st.rbuf.Len() > 0 {
			n, _ := st.rbuf.Read(b)
			st.mu.Unlock()
			st.advertise(n)
			st.maybeRemove()
			st.mu.Lock()
			return n, nil
		}
		if st.sessDead {
			return 0, io.ErrClosedPipe
		}
		if st.remoteEOFLocked() {
			return 0, io.EOF
		}
		if !st.readDeadline.IsZero() && !time.Now().Before(st.readDeadline) {
			return 0, muxTimeoutError{}
		}
		if err := st.waitLocked(); err != nil {
			if st.rbuf.Len() > 0 || st.sessDead || st.remoteEOFLocked() {
				continue
			}
			return 0, err
		}
	}
}

// Write writes p in full. It blocks while the per-stream send window is exhausted.
// A blocked write does not prevent other streams from sending.
func (st *MuxStream) Write(p []byte) (int, error) {
	if len(p) == 0 {
		st.mu.Lock()
		dead := st.sessDead || st.localClosed || st.remoteClosed
		st.mu.Unlock()
		if dead {
			return 0, io.ErrClosedPipe
		}
		return 0, nil
	}
	written := 0
	for written < len(p) {
		st.mu.Lock()
		for st.sendCredit == 0 && !st.sessDead && !st.localClosed && !st.remoteClosed {
			st.cond.Wait()
		}
		if st.sessDead || st.localClosed || st.remoteClosed {
			st.mu.Unlock()
			if written > 0 {
				return written, io.ErrClosedPipe
			}
			return 0, io.ErrClosedPipe
		}
		n := len(p) - written
		if n > st.sendCredit {
			n = st.sendCredit
		}
		if n > st.sess.cfg.MaxFrameSize {
			n = st.sess.cfg.MaxFrameSize
		}
		chunk := make([]byte, n)
		copy(chunk, p[written:written+n])
		st.sendCredit -= n
		st.bytesEnqueued += uint64(n)
		st.mu.Unlock()

		if err := st.sess.enqueue(muxCmdData, st.priority, st.id, chunk, false); err != nil {
			if written == 0 {
				return 0, err
			}
			return written, err
		}
		written += n
	}
	return written, nil
}

// Close is a half-close: local writes stop, buffered inbound data stays readable.
func (st *MuxStream) Close() error {
	st.mu.Lock()
	if st.sessDead {
		st.mu.Unlock()
		return io.ErrClosedPipe
	}
	if st.localClosed {
		st.mu.Unlock()
		return io.ErrClosedPipe
	}
	st.localClosed = true
	total := st.bytesEnqueued
	st.cond.Broadcast()
	st.mu.Unlock()

	var payload [8]byte
	binary.BigEndian.PutUint64(payload[:], total)
	_ = st.sess.enqueue(muxCmdClose, st.priority, st.id, payload[:], true)
	st.accountClosed()
	st.maybeRemove()
	return nil
}

// SetReadDeadline sets the deadline for future Read calls.
// A zero time clears the deadline.
func (st *MuxStream) SetReadDeadline(t time.Time) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.sessDead || st.removed {
		return io.ErrClosedPipe
	}
	st.readDeadline = t
	st.cond.Broadcast()
	return nil
}

func (st *MuxStream) remoteEOFLocked() bool {
	return st.remoteClosed && st.bytesReceived >= st.closeAt && st.rbuf.Len() == 0
}

func (st *MuxStream) waitLocked() error {
	if st.readDeadline.IsZero() {
		st.cond.Wait()
		return nil
	}
	remain := time.Until(st.readDeadline)
	if remain <= 0 {
		return muxTimeoutError{}
	}
	timer := time.AfterFunc(remain, func() {
		st.mu.Lock()
		st.cond.Broadcast()
		st.mu.Unlock()
	})
	st.cond.Wait()
	timer.Stop()
	if st.rbuf.Len() == 0 && !st.sessDead && !st.remoteEOFLocked() && !time.Now().Before(st.readDeadline) {
		return muxTimeoutError{}
	}
	return nil
}

func (st *MuxStream) advertise(n int) {
	if n <= 0 {
		return
	}
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(n))
	_ = st.sess.enqueue(muxCmdWindow, st.priority, st.id, buf[:], true)
}

func (st *MuxStream) accountClosed() {
	st.mu.Lock()
	if st.closeAccounted {
		st.mu.Unlock()
		return
	}
	st.closeAccounted = true
	st.mu.Unlock()
	atomic.AddUint64(&DefaultSnmp.MuxStreamsClosed, 1)
}

func (st *MuxStream) maybeRemove() {
	st.mu.Lock()
	if st.removed || !st.localClosed || !st.remoteClosed || st.rbuf.Len() != 0 || st.bytesReceived < st.closeAt {
		st.mu.Unlock()
		return
	}
	st.removed = true
	id := st.id
	st.mu.Unlock()

	st.sess.mu.Lock()
	delete(st.sess.streams, id)
	st.sess.mu.Unlock()
}

func (s *MuxSession) dropStream(st *MuxStream) {
	s.mu.Lock()
	delete(s.streams, st.id)
	s.mu.Unlock()
}

func (s *MuxSession) enqueue(cmd, prio byte, id uint32, payload []byte, control bool) error {
	frame := encodeMuxFrame(cmd, prio, id, payload)
	s.schedMu.Lock()
	defer s.schedMu.Unlock()
	if s.schedDone {
		return io.ErrClosedPipe
	}
	if control {
		s.ctrl = append(s.ctrl, frame)
	} else {
		if prio > MuxPriorityLow {
			prio = MuxPriorityLow
		}
		s.buckets[prio] = append(s.buckets[prio], frame)
	}
	s.schedC.Signal()
	return nil
}

func (s *MuxSession) dequeue() ([]byte, bool) {
	s.schedMu.Lock()
	defer s.schedMu.Unlock()
	for {
		if len(s.ctrl) > 0 {
			f := s.ctrl[0]
			s.ctrl[0] = nil
			s.ctrl = s.ctrl[1:]
			return f, true
		}
		for i := 0; i < len(s.buckets); i++ {
			if len(s.buckets[i]) > 0 {
				f := s.buckets[i][0]
				s.buckets[i][0] = nil
				s.buckets[i] = s.buckets[i][1:]
				return f, true
			}
		}
		if s.schedDone {
			return nil, false
		}
		s.schedC.Wait()
	}
}

func (s *MuxSession) writeLoop() {
	for {
		frame, ok := s.dequeue()
		if !ok {
			return
		}
		_, err := s.conn.Write(frame)
		if err != nil {
			s.Close()
			return
		}
		atomic.AddUint64(&DefaultSnmp.MuxFramesSent, 1)
		if len(frame) >= muxHeaderLen && frame[0] == muxCmdData {
			n := binary.BigEndian.Uint32(frame[6:10])
			atomic.AddUint64(&DefaultSnmp.MuxBytesSent, uint64(n))
		}
	}
}

func (s *MuxSession) readLoop() {
	defer s.Close()
	hdr := make([]byte, muxHeaderLen)
	for {
		if _, err := io.ReadFull(s.conn, hdr); err != nil {
			return
		}
		cmd := hdr[0]
		prio := hdr[1]
		id := binary.BigEndian.Uint32(hdr[2:6])
		n := binary.BigEndian.Uint32(hdr[6:10])
		if n > uint32(s.cfg.MaxFrameSize) && cmd == muxCmdData {
			return
		}
		if n > 8<<20 {
			return
		}
		var payload []byte
		if n > 0 {
			payload = make([]byte, n)
			if _, err := io.ReadFull(s.conn, payload); err != nil {
				return
			}
		}
		atomic.AddUint64(&DefaultSnmp.MuxFramesReceived, 1)
		if cmd == muxCmdData {
			atomic.AddUint64(&DefaultSnmp.MuxBytesReceived, uint64(n))
		}
		s.dispatch(cmd, prio, id, payload)
	}
}

func (s *MuxSession) dispatch(cmd, prio byte, id uint32, payload []byte) {
	switch cmd {
	case muxCmdOpen:
		s.onOpen(id, prio)
	case muxCmdData:
		s.onData(id, payload)
	case muxCmdWindow:
		s.onWindow(id, payload)
	case muxCmdClose:
		s.onClose(id, payload)
	}
}

func (s *MuxSession) onOpen(id uint32, prio byte) {
	if prio > MuxPriorityLow {
		prio = MuxPriorityLow
	}
	if s.cfg.Side == MuxSideClient && id%2 == 1 {
		return
	}
	if s.cfg.Side == MuxSideServer && id%2 == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if _, exists := s.streams[id]; exists {
		return
	}
	st := newMuxStream(s, id, prio)
	s.streams[id] = st
	s.incoming = append(s.incoming, st)
	atomic.AddUint64(&DefaultSnmp.MuxStreamsOpened, 1)
	s.acceptC.Signal()
}

func (s *MuxSession) onData(id uint32, payload []byte) {
	st := s.stream(id)
	if st == nil || len(payload) == 0 {
		return
	}
	st.mu.Lock()
	_, _ = st.rbuf.Write(payload)
	st.bytesReceived += uint64(len(payload))
	st.cond.Broadcast()
	st.mu.Unlock()
}

func (s *MuxSession) onWindow(id uint32, payload []byte) {
	if len(payload) < 4 {
		return
	}
	st := s.stream(id)
	if st == nil {
		return
	}
	delta := int(binary.BigEndian.Uint32(payload[:4]))
	if delta <= 0 {
		return
	}
	st.mu.Lock()
	st.sendCredit += delta
	if st.sendCredit > st.sess.cfg.SendWindow {
		st.sendCredit = st.sess.cfg.SendWindow
	}
	st.cond.Broadcast()
	st.mu.Unlock()
}

func (s *MuxSession) onClose(id uint32, payload []byte) {
	st := s.stream(id)
	if st == nil {
		return
	}
	var total uint64
	if len(payload) >= 8 {
		total = binary.BigEndian.Uint64(payload[:8])
	}
	st.mu.Lock()
	st.remoteClosed = true
	st.closeAt = total
	st.cond.Broadcast()
	st.mu.Unlock()
	st.accountClosed()
	st.maybeRemove()
}

func (s *MuxSession) stream(id uint32) *MuxStream {
	s.mu.Lock()
	st := s.streams[id]
	s.mu.Unlock()
	return st
}

func encodeMuxFrame(cmd, prio byte, id uint32, payload []byte) []byte {
	buf := make([]byte, muxHeaderLen+len(payload))
	buf[0] = cmd
	buf[1] = prio
	binary.BigEndian.PutUint32(buf[2:6], id)
	binary.BigEndian.PutUint32(buf[6:10], uint32(len(payload)))
	copy(buf[10:], payload)
	return buf
}
