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
	"errors"
	"io"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// MuxSide identifies which peer allocates which stream IDs.
// Clients allocate odd IDs (1, 3, 5, ...) and servers allocate even IDs (2, 4, 6, ...).
type MuxSide uint8

const (
	// MuxSideClient allocates odd stream IDs.
	MuxSideClient MuxSide = iota
	// MuxSideServer allocates even stream IDs.
	MuxSideServer
)

const (
	// MuxPriorityHigh is the highest data-frame priority.
	// Lower numeric values are scheduled ahead of higher ones.
	MuxPriorityHigh uint8 = iota
	// MuxPriorityNormal is the default data-frame priority.
	MuxPriorityNormal
	// MuxPriorityLow is scheduled after higher-priority streams.
	MuxPriorityLow
)

const (
	defaultMuxMaxFrameSize = 32 * 1024
	defaultMuxWindow       = 256 * 1024

	muxVersion    = 1
	muxHeaderLen  = 11
	muxMaxPayload = 16 << 20

	muxCmdHello  = 1
	muxCmdOpen   = 2
	muxCmdData   = 3
	muxCmdClose  = 4
	muxCmdWindow = 5
)

var errMuxBadFrame = errors.New("mux: bad frame")

// MuxConfig configures a multiplexed session.
// SendWindow and RecvWindow are byte counts.
type MuxConfig struct {
	Side         MuxSide
	MaxFrameSize int
	SendWindow   int
	RecvWindow   int
}

// DefaultMuxConfig returns a client-side configuration with 32KiB frames
// and 256KiB per-stream send and receive windows.
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

	mu         sync.Mutex
	streams    map[uint32]*MuxStream
	acceptQ    []*MuxStream
	acceptCond *sync.Cond
	nextID     uint32

	peerWindowKnown bool
	peerRecvWindow  int

	wmu        sync.Mutex
	wcond      *sync.Cond
	ctrlQ      muxQueue // hello, open, window update
	closes     []muxFrame
	dataQ      [256]muxQueue
	outPending map[uint32]int // data frames queued but not yet handed to conn.Write

	closed  uint32
	dieOnce sync.Once
}

// MuxStream is one ordered, flow-controlled byte stream inside a MuxSession.
type MuxStream struct {
	session  *MuxSession
	id       uint32
	priority uint8

	wlock sync.Mutex

	mu           sync.Mutex
	cond         *sync.Cond
	recvBuf      bytes.Buffer
	readDeadline time.Time
	removed      bool

	localClosed  uint32
	remoteClosed uint32

	sendMu     sync.Mutex
	sendCond   *sync.Cond
	sendCredit int
	sendMax    int
	creditInit bool
}

// NewMuxSession starts a multiplexed session over conn.
// A nil config selects DefaultMuxConfig.
// Close returns as soon as shutdown is signaled; it does not wait for
// background reads or writes to finish.
func NewMuxSession(conn net.Conn, cfg *MuxConfig) (*MuxSession, error) {
	if conn == nil {
		return nil, errors.New("mux: nil conn")
	}
	c := normalizeMuxConfig(cfg)
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
	s.wcond = sync.NewCond(&s.wmu)

	hello := make([]byte, 4)
	rw := c.RecvWindow
	if rw > math.MaxUint32 {
		rw = math.MaxUint32
	}
	binary.BigEndian.PutUint32(hello, uint32(rw))
	if err := s.queueFrame(muxCmdHello, 0, 0, hello); err != nil {
		return nil, err
	}

	go s.readLoop()
	go s.writeLoop()
	return s, nil
}

func normalizeMuxConfig(cfg *MuxConfig) MuxConfig {
	def := DefaultMuxConfig()
	if cfg == nil {
		return def
	}
	c := *cfg
	if c.MaxFrameSize <= 0 {
		c.MaxFrameSize = def.MaxFrameSize
	}
	if c.SendWindow <= 0 {
		c.SendWindow = def.SendWindow
	}
	if c.RecvWindow <= 0 {
		c.RecvWindow = def.RecvWindow
	}
	return c
}

// Close shuts the session down and unblocks every stream operation.
// It returns promptly and does not wait for the read or write loops,
// even when the underlying connection's Write is blocked.
// A second call returns io.ErrClosedPipe.
func (s *MuxSession) Close() error {
	if s.shutdown() {
		return nil
	}
	return io.ErrClosedPipe
}

// shutdown signals session death at most once.
// The first caller closes the underlying connection to unblock any
// in-progress Read or Write, then returns without joining goroutines.
func (s *MuxSession) shutdown() bool {
	first := false
	s.dieOnce.Do(func() {
		first = true
		atomic.StoreUint32(&s.closed, 1)
		s.wakeAll()
		_ = s.conn.Close()
	})
	return first
}

func (s *MuxSession) isClosed() bool {
	return atomic.LoadUint32(&s.closed) == 1
}

func (s *MuxSession) wakeAll() {
	s.mu.Lock()
	streams := make([]*MuxStream, 0, len(s.streams))
	for _, st := range s.streams {
		streams = append(streams, st)
	}
	s.acceptCond.Broadcast()
	s.mu.Unlock()

	for _, st := range streams {
		st.mu.Lock()
		st.cond.Broadcast()
		st.mu.Unlock()
		st.sendMu.Lock()
		st.sendCond.Broadcast()
		st.sendMu.Unlock()
	}

	s.wmu.Lock()
	s.wcond.Broadcast()
	s.wmu.Unlock()
}

// NumStreams reports streams that are still tracked by the session.
// A stream leaves the map only after both sides have closed it and its
// buffered inbound bytes have been drained.
func (s *MuxSession) NumStreams() int {
	s.mu.Lock()
	n := len(s.streams)
	s.mu.Unlock()
	return n
}

// OpenStream allocates a stream and notifies the peer.
// Either side may call it. The returned stream shares its ID with the peer.
func (s *MuxSession) OpenStream(priority uint8) (*MuxStream, error) {
	if s.isClosed() {
		return nil, io.ErrClosedPipe
	}
	st := newMuxStream(s, priority)

	s.mu.Lock()
	if s.isClosed() {
		s.mu.Unlock()
		return nil, io.ErrClosedPipe
	}
	id := s.nextID
	s.nextID += 2
	st.id = id
	known := s.peerWindowKnown
	eff := s.effectiveLocked()
	s.streams[id] = st
	s.mu.Unlock()

	if known {
		st.grantInitialCredit(eff)
	}
	if err := s.queueFrame(muxCmdOpen, priority, id, nil); err != nil {
		s.mu.Lock()
		delete(s.streams, id)
		s.mu.Unlock()
		return nil, err
	}
	atomic.AddUint64(&DefaultSnmp.MuxStreamsOpened, 1)
	return st, nil
}

// AcceptStream blocks until the peer opens a stream or the session ends.
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
		if s.isClosed() {
			return nil, io.ErrClosedPipe
		}
		s.acceptCond.Wait()
	}
}

func (s *MuxSession) effectiveLocked() int {
	if !s.peerWindowKnown {
		return 0
	}
	eff := s.cfg.SendWindow
	if s.peerRecvWindow < eff {
		eff = s.peerRecvWindow
	}
	if eff < 0 {
		eff = 0
	}
	return eff
}

func (s *MuxSession) stream(id uint32) *MuxStream {
	s.mu.Lock()
	st := s.streams[id]
	s.mu.Unlock()
	return st
}

func (s *MuxSession) readLoop() {
	defer s.shutdown()
	for {
		cmd, pri, id, payload, err := readMuxFrame(s.conn)
		if err != nil {
			return
		}
		atomic.AddUint64(&DefaultSnmp.MuxFramesReceived, 1)
		if cmd == muxCmdData {
			atomic.AddUint64(&DefaultSnmp.MuxBytesReceived, uint64(len(payload)))
		}
		s.dispatch(cmd, pri, id, payload)
	}
}

func (s *MuxSession) writeLoop() {
	defer s.shutdown()
	for {
		frame := s.nextFrame()
		if frame == nil {
			return
		}
		if _, err := s.conn.Write(frame); err != nil {
			return
		}
	}
}

func (s *MuxSession) dispatch(cmd byte, priority uint8, id uint32, payload []byte) {
	switch cmd {
	case muxCmdHello:
		s.handleHello(payload)
	case muxCmdOpen:
		s.handleOpen(id, priority)
	case muxCmdData:
		s.handleData(id, payload)
	case muxCmdClose:
		s.handleClose(id)
	case muxCmdWindow:
		s.handleWindow(id, payload)
	}
}

func (s *MuxSession) handleHello(payload []byte) {
	if len(payload) < 4 {
		return
	}
	w := int(binary.BigEndian.Uint32(payload[:4]))
	s.mu.Lock()
	if s.peerWindowKnown {
		s.mu.Unlock()
		return
	}
	s.peerWindowKnown = true
	s.peerRecvWindow = w
	eff := s.effectiveLocked()
	streams := make([]*MuxStream, 0, len(s.streams))
	for _, st := range s.streams {
		streams = append(streams, st)
	}
	s.mu.Unlock()
	for _, st := range streams {
		st.grantInitialCredit(eff)
	}
}

func (s *MuxSession) handleOpen(id uint32, priority uint8) {
	if id == 0 {
		return
	}
	st := newMuxStream(s, priority)
	st.id = id

	s.mu.Lock()
	if s.isClosed() {
		s.mu.Unlock()
		return
	}
	if _, exists := s.streams[id]; exists {
		s.mu.Unlock()
		return
	}
	known := s.peerWindowKnown
	eff := s.effectiveLocked()
	s.streams[id] = st
	s.acceptQ = append(s.acceptQ, st)
	s.acceptCond.Broadcast()
	s.mu.Unlock()

	if known {
		st.grantInitialCredit(eff)
	}
	atomic.AddUint64(&DefaultSnmp.MuxStreamsOpened, 1)
}

func (s *MuxSession) handleData(id uint32, payload []byte) {
	if len(payload) == 0 {
		return
	}
	st := s.stream(id)
	if st == nil {
		return
	}
	st.mu.Lock()
	if atomic.LoadUint32(&st.remoteClosed) == 1 || st.session.isClosed() {
		st.mu.Unlock()
		return
	}
	_, _ = st.recvBuf.Write(payload)
	st.cond.Broadcast()
	st.mu.Unlock()
}

func (s *MuxSession) handleClose(id uint32) {
	st := s.stream(id)
	if st == nil {
		return
	}
	if !atomic.CompareAndSwapUint32(&st.remoteClosed, 0, 1) {
		return
	}
	st.mu.Lock()
	st.cond.Broadcast()
	st.mu.Unlock()
	st.sendMu.Lock()
	st.sendCond.Broadcast()
	st.sendMu.Unlock()
	st.tryRemove()
}

func (s *MuxSession) handleWindow(id uint32, payload []byte) {
	if len(payload) < 4 {
		return
	}
	delta := int(binary.BigEndian.Uint32(payload[:4]))
	if delta <= 0 {
		return
	}
	st := s.stream(id)
	if st == nil {
		return
	}
	st.sendMu.Lock()
	st.sendCredit += delta
	if st.sendMax > 0 && st.sendCredit > st.sendMax {
		st.sendCredit = st.sendMax
	}
	st.sendCond.Broadcast()
	st.sendMu.Unlock()
}

func (s *MuxSession) sendWindowUpdate(id uint32, n int) {
	if n <= 0 || s.isClosed() {
		return
	}
	var payload [4]byte
	binary.BigEndian.PutUint32(payload[:], uint32(n))
	_ = s.queueFrame(muxCmdWindow, 0, id, payload[:])
}

func (s *MuxSession) queueFrame(cmd byte, priority uint8, id uint32, payload []byte) error {
	if s.isClosed() {
		return io.ErrClosedPipe
	}
	frame := muxFrame{id: id, bytes: encodeMuxFrame(cmd, priority, id, payload)}
	s.wmu.Lock()
	if s.isClosed() {
		s.wmu.Unlock()
		return io.ErrClosedPipe
	}
	switch cmd {
	case muxCmdData:
		if s.outPending == nil {
			s.outPending = make(map[uint32]int)
		}
		s.dataQ[priority].push(frame)
		s.outPending[id]++
	case muxCmdClose:
		// FIN follows this stream's data, but is eligible ahead of other
		// streams' data once those bytes have been popped for writing.
		s.closes = append(s.closes, frame)
	default:
		s.ctrlQ.push(frame)
	}
	s.wcond.Broadcast()
	s.wmu.Unlock()

	atomic.AddUint64(&DefaultSnmp.MuxFramesSent, 1)
	if cmd == muxCmdData {
		atomic.AddUint64(&DefaultSnmp.MuxBytesSent, uint64(len(payload)))
	}
	return nil
}

func (s *MuxSession) nextFrame() []byte {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	for {
		if s.isClosed() {
			return nil
		}
		if f, ok := s.ctrlQ.pop(); ok {
			return f.bytes
		}
		if b := s.popEligibleCloseLocked(); b != nil {
			return b
		}
		if f, ok := s.popDataLocked(); ok {
			return f.bytes
		}
		s.wcond.Wait()
	}
}

func (s *MuxSession) popEligibleCloseLocked() []byte {
	for i := range s.closes {
		if s.outPending[s.closes[i].id] == 0 {
			b := s.closes[i].bytes
			s.closes = append(s.closes[:i], s.closes[i+1:]...)
			return b
		}
	}
	return nil
}

func (s *MuxSession) popDataLocked() (muxFrame, bool) {
	for pri := range s.dataQ {
		f, ok := s.dataQ[pri].pop()
		if !ok {
			continue
		}
		if n := s.outPending[f.id]; n > 1 {
			s.outPending[f.id] = n - 1
		} else {
			delete(s.outPending, f.id)
		}
		return f, true
	}
	return muxFrame{}, false
}

func newMuxStream(s *MuxSession, priority uint8) *MuxStream {
	st := &MuxStream{
		session:  s,
		priority: priority,
	}
	st.cond = sync.NewCond(&st.mu)
	st.sendCond = sync.NewCond(&st.sendMu)
	return st
}

func (st *MuxStream) grantInitialCredit(eff int) {
	st.sendMu.Lock()
	if st.creditInit {
		st.sendMu.Unlock()
		return
	}
	st.creditInit = true
	st.sendMax = eff
	st.sendCredit = eff
	st.sendCond.Broadcast()
	st.sendMu.Unlock()
}

// ID returns the stream identifier shared by both peers.
func (st *MuxStream) ID() uint32 { return st.id }

// Read reads the next bytes from the stream.
// A remote half-close yields io.EOF once buffered data has been consumed.
// Session shutdown yields io.ErrClosedPipe.
// A deadline set by SetReadDeadline returns an error whose Timeout method is true.
func (st *MuxStream) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	for {
		st.mu.Lock()
		if st.session.isClosed() {
			st.mu.Unlock()
			return 0, io.ErrClosedPipe
		}
		if st.recvBuf.Len() > 0 {
			n, _ := st.recvBuf.Read(b)
			st.mu.Unlock()
			if n > 0 {
				st.session.sendWindowUpdate(st.id, n)
			}
			st.tryRemove()
			return n, nil
		}
		if atomic.LoadUint32(&st.remoteClosed) == 1 {
			st.mu.Unlock()
			st.tryRemove()
			return 0, io.EOF
		}
		if dl := st.readDeadline; !dl.IsZero() && !time.Now().Before(dl) {
			st.mu.Unlock()
			return 0, errTimeout
		}
		st.waitRecvLocked()
		st.mu.Unlock()
	}
}

// waitRecvLocked blocks until the stream is signalled or the read deadline fires.
// st.mu is held by the caller and reacquired before return.
func (st *MuxStream) waitRecvLocked() {
	if st.readDeadline.IsZero() {
		st.cond.Wait()
		return
	}
	d := time.Until(st.readDeadline)
	if d <= 0 {
		return
	}
	timer := time.AfterFunc(d, func() {
		st.mu.Lock()
		st.cond.Broadcast()
		st.mu.Unlock()
	})
	st.cond.Wait()
	timer.Stop()
}

// Write writes p in full. It blocks while the per-stream send window is exhausted
// and returns a short count only together with an error.
func (st *MuxStream) Write(p []byte) (int, error) {
	if st.writeAborted() {
		return 0, io.ErrClosedPipe
	}
	if len(p) == 0 {
		return 0, nil
	}

	st.wlock.Lock()
	defer st.wlock.Unlock()

	maxF := st.session.cfg.MaxFrameSize
	written := 0
	for written < len(p) {
		st.sendMu.Lock()
		for st.sendCredit == 0 && !st.writeAborted() {
			st.sendCond.Wait()
		}
		if st.writeAborted() {
			st.sendMu.Unlock()
			if written > 0 {
				return written, io.ErrClosedPipe
			}
			return 0, io.ErrClosedPipe
		}
		n := len(p) - written
		if n > st.sendCredit {
			n = st.sendCredit
		}
		if n > maxF {
			n = maxF
		}
		st.sendCredit -= n
		st.sendMu.Unlock()

		if err := st.session.queueFrame(muxCmdData, st.priority, st.id, p[written:written+n]); err != nil {
			if written > 0 {
				return written, err
			}
			return 0, err
		}
		written += n
	}
	return written, nil
}

func (st *MuxStream) writeAborted() bool {
	return st.session.isClosed() ||
		atomic.LoadUint32(&st.localClosed) == 1 ||
		atomic.LoadUint32(&st.remoteClosed) == 1
}

// Close half-closes the stream: local writes stop, inbound bytes already
// buffered remain readable, and blocked writers are unblocked with io.ErrClosedPipe.
func (st *MuxStream) Close() error {
	if !atomic.CompareAndSwapUint32(&st.localClosed, 0, 1) {
		return io.ErrClosedPipe
	}
	st.mu.Lock()
	st.cond.Broadcast()
	st.mu.Unlock()
	st.sendMu.Lock()
	st.sendCond.Broadcast()
	st.sendMu.Unlock()
	atomic.AddUint64(&DefaultSnmp.MuxStreamsClosed, 1)
	st.tryRemove()
	if st.session.isClosed() {
		return io.ErrClosedPipe
	}
	if err := st.session.queueFrame(muxCmdClose, 0, st.id, nil); err != nil {
		return err
	}
	return nil
}

// SetReadDeadline sets the deadline for future Read calls.
// A zero time clears the deadline. An expired deadline makes Read return
// an error that implements net.Error and reports Timeout.
func (st *MuxStream) SetReadDeadline(t time.Time) error {
	if st.session.isClosed() {
		return io.ErrClosedPipe
	}
	st.mu.Lock()
	if st.session.isClosed() {
		st.mu.Unlock()
		return io.ErrClosedPipe
	}
	st.readDeadline = t
	st.cond.Broadcast()
	st.mu.Unlock()
	return nil
}

func (st *MuxStream) tryRemove() {
	st.mu.Lock()
	if st.removed ||
		atomic.LoadUint32(&st.localClosed) == 0 ||
		atomic.LoadUint32(&st.remoteClosed) == 0 ||
		st.recvBuf.Len() != 0 {
		st.mu.Unlock()
		return
	}
	st.removed = true
	id := st.id
	st.mu.Unlock()

	st.session.mu.Lock()
	delete(st.session.streams, id)
	st.session.mu.Unlock()
}

// Frame layout:
//
//	version(1) | cmd(1) | priority(1) | streamID(4) | length(4) | payload
//
// Control commands (hello, open, close, window update) are queued ahead of data.
// Data frames are scheduled by ascending priority value so MuxPriorityHigh
// preempts lower-priority queued frames.
func encodeMuxFrame(cmd byte, priority uint8, id uint32, payload []byte) []byte {
	buf := make([]byte, muxHeaderLen+len(payload))
	buf[0] = muxVersion
	buf[1] = cmd
	buf[2] = priority
	binary.BigEndian.PutUint32(buf[3:7], id)
	binary.BigEndian.PutUint32(buf[7:11], uint32(len(payload)))
	copy(buf[muxHeaderLen:], payload)
	return buf
}

func readMuxFrame(r io.Reader) (cmd byte, priority uint8, id uint32, payload []byte, err error) {
	var hdr [muxHeaderLen]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return
	}
	if hdr[0] != muxVersion {
		err = errMuxBadFrame
		return
	}
	cmd = hdr[1]
	priority = hdr[2]
	id = binary.BigEndian.Uint32(hdr[3:7])
	n := binary.BigEndian.Uint32(hdr[7:11])
	if n > muxMaxPayload {
		err = errMuxBadFrame
		return
	}
	if n == 0 {
		return
	}
	payload = make([]byte, n)
	_, err = io.ReadFull(r, payload)
	return
}

// muxFrame is one encoded frame waiting in a send queue.
type muxFrame struct {
	id    uint32
	bytes []byte
}

// muxQueue is a FIFO of encoded frames with a moving head so popped
// references can be released.
type muxQueue struct {
	items []muxFrame
	head  int
}

func (q *muxQueue) push(f muxFrame) {
	q.items = append(q.items, f)
}

func (q *muxQueue) pop() (muxFrame, bool) {
	if q.head >= len(q.items) {
		return muxFrame{}, false
	}
	f := q.items[q.head]
	q.items[q.head] = muxFrame{}
	q.head++
	if q.head == len(q.items) {
		q.items = q.items[:0]
		q.head = 0
		return f, true
	}
	if q.head > 32 && q.head*2 >= len(q.items) {
		remain := len(q.items) - q.head
		copy(q.items, q.items[q.head:])
		for i := remain; i < len(q.items); i++ {
			q.items[i] = muxFrame{}
		}
		q.items = q.items[:remain]
		q.head = 0
	}
	return f, true
}
