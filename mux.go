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

// [STREAM MULTIPLEXING OVER A SINGLE CONNECTION]
//
// Every frame starts with a 10-byte little-endian header:
//
//	| version(1) | cmd(1) | stream id(4) | payload length(4) | payload ... |
//
// Commands:
//
//	SETTINGS  stream 0, payload: recv window(4) | send window(4). Must be the first frame.
//	SYN       opens a stream, payload: priority(1)
//	FIN       the sender will write no more data on the stream
//	PSH       stream data
//	UPD       window update, payload: credit increment(4)
//
// A stream may have at most min(sender's SendWindow, receiver's RecvWindow) bytes
// written but not yet consumed by the remote reader. Streams opened before the
// peer's SETTINGS arrive start with the local SendWindow and are adjusted once
// the peer's RecvWindow is known.

package kcp

import (
	"bufio"
	"container/heap"
	"encoding/binary"
	"io"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
)

// MuxSide selects the half of the stream ID space a MuxSession allocates from.
// The two ends of a connection must use opposite sides.
type MuxSide uint8

const (
	// MuxSideClient opens streams with odd IDs: 1, 3, 5, ...
	MuxSideClient MuxSide = iota
	// MuxSideServer opens streams with even IDs: 2, 4, 6, ...
	MuxSideServer
)

// Stream priorities for OpenStream. Queued data frames with a lower priority
// value are transmitted first; any uint8 value is accepted.
const (
	MuxPriorityHigh   uint8 = 0
	MuxPriorityNormal uint8 = 1
	MuxPriorityLow    uint8 = 2
)

const (
	muxVersion    = 1
	muxHeaderSize = 10

	muxMaxFrameSize = 1 << 24
	muxMaxWindow    = math.MaxInt32

	muxAcceptBacklog  = 1024
	muxReadBufferSize = 64 * 1024
)

const (
	muxCmdSettings byte = iota
	muxCmdSYN
	muxCmdFIN
	muxCmdPSH
	muxCmdUPD
)

var errMuxStreamIDsExhausted = errors.New("mux stream IDs exhausted")

// MuxConfig configures a MuxSession.
type MuxConfig struct {
	// Side decides whether locally opened streams use odd (client) or even (server) IDs.
	Side MuxSide

	// MaxFrameSize is the largest data payload carried by a single frame, in bytes.
	// Smaller frames let higher-priority streams preempt bulk transfers sooner.
	MaxFrameSize int

	// SendWindow is the maximum number of bytes a stream may have written
	// but not yet consumed by the remote reader.
	SendWindow int

	// RecvWindow is the maximum number of unread bytes buffered per stream,
	// advertised to the peer to cap its send window.
	RecvWindow int
}

// DefaultMuxConfig returns a client-side configuration with sensible defaults.
func DefaultMuxConfig() MuxConfig {
	return MuxConfig{
		Side:         MuxSideClient,
		MaxFrameSize: 16 * 1024,
		SendWindow:   256 * 1024,
		RecvWindow:   256 * 1024,
	}
}

func (c *MuxConfig) validate() error {
	if c.Side != MuxSideClient && c.Side != MuxSideServer {
		return errors.Errorf("invalid mux side: %d", c.Side)
	}
	if c.MaxFrameSize <= 0 || c.MaxFrameSize > muxMaxFrameSize {
		return errors.Errorf("mux MaxFrameSize must be in [1, %d], got %d", muxMaxFrameSize, c.MaxFrameSize)
	}
	if c.SendWindow <= 0 || c.SendWindow > muxMaxWindow {
		return errors.Errorf("mux SendWindow must be in [1, %d], got %d", muxMaxWindow, c.SendWindow)
	}
	if c.RecvWindow <= 0 || c.RecvWindow > muxMaxWindow {
		return errors.Errorf("mux RecvWindow must be in [1, %d], got %d", muxMaxWindow, c.RecvWindow)
	}
	return nil
}

// muxFrame is an encoded frame waiting in the send queue.
type muxFrame struct {
	buf      []byte     // header and payload
	stream   *MuxStream // owner of a data frame; nil for control frames
	priority uint8
	seq      uint64
	pooled   bool // buf came from MuxSession.framePool
}

// muxFrameHeap orders data frames by priority, then FIFO.
type muxFrameHeap []*muxFrame

func (h muxFrameHeap) Len() int { return len(h) }
func (h muxFrameHeap) Less(i, j int) bool {
	if h[i].priority != h[j].priority {
		return h[i].priority < h[j].priority
	}
	return h[i].seq < h[j].seq
}
func (h muxFrameHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *muxFrameHeap) Push(x any)   { *h = append(*h, x.(*muxFrame)) }
func (h *muxFrameHeap) Pop() any {
	old := *h
	n := len(old)
	f := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return f
}

func muxPutHeader(b []byte, cmd byte, sid uint32, length int) {
	b[0] = muxVersion
	b[1] = cmd
	binary.LittleEndian.PutUint32(b[2:], sid)
	binary.LittleEndian.PutUint32(b[6:], uint32(length))
}

// MuxSession carries many independent, ordered streams over a single net.Conn,
// such as a *UDPSession.
//
// Each stream has its own byte-level flow control window, so a stream whose
// reader stalls never blocks the others. Outgoing control frames (open, close,
// window update) are sent ahead of data frames, and queued data frames are sent
// in stream priority order.
type MuxSession struct {
	conn   net.Conn
	config MuxConfig

	peerSendWindow atomic.Int64 // from the peer's SETTINGS; bounds inbound buffering per stream

	mu             sync.Mutex // lock order: mu, then MuxStream.mu, then sendMu
	streams        map[uint32]*MuxStream
	nextID         uint64 // wider than a stream ID so exhaustion is detectable
	peerReady      bool   // the peer's SETTINGS have been applied
	peerRecvWindow int
	chAccepts      chan *MuxStream

	sendMu     sync.Mutex
	sendClosed bool
	ctrlQueue  []*muxFrame
	dataQueue  muxFrameHeap
	dataSeq    uint64
	chSend     chan struct{} // wakes sendLoop
	framePool  sync.Pool     // buffers for full-sized data frames

	die     chan struct{}
	dieOnce sync.Once
	closed  atomic.Bool // Close has been called
}

// NewMuxSession starts multiplexing streams over conn and takes ownership of it:
// the connection is closed when the session closes. A nil cfg selects DefaultMuxConfig().
func NewMuxSession(conn net.Conn, cfg *MuxConfig) (*MuxSession, error) {
	if conn == nil {
		return nil, errors.New("mux requires a non-nil connection")
	}

	config := DefaultMuxConfig()
	if cfg != nil {
		config = *cfg
	}
	if err := config.validate(); err != nil {
		return nil, err
	}

	s := &MuxSession{
		conn:      conn,
		config:    config,
		streams:   make(map[uint32]*MuxStream),
		chAccepts: make(chan *MuxStream, muxAcceptBacklog),
		chSend:    make(chan struct{}, 1),
		die:       make(chan struct{}),
	}
	if config.Side == MuxSideClient {
		s.nextID = 1
	} else {
		s.nextID = 2
	}
	frameSize := muxHeaderSize + config.MaxFrameSize
	s.framePool.New = func() any { return make([]byte, frameSize) }

	var settings [8]byte
	binary.LittleEndian.PutUint32(settings[0:], uint32(config.RecvWindow))
	binary.LittleEndian.PutUint32(settings[4:], uint32(config.SendWindow))
	s.sendControl(muxCmdSettings, 0, settings[:])

	go s.recvLoop()
	go s.sendLoop()
	return s, nil
}

// OpenStream opens a new stream with the given priority. Either side may open streams.
func (s *MuxSession) OpenStream(priority uint8) (*MuxStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if s.nextID > math.MaxUint32 {
		return nil, errMuxStreamIDsExhausted
	}
	id := uint32(s.nextID)
	s.nextID += 2

	credit := s.config.SendWindow
	if s.peerReady {
		credit = min(credit, s.peerRecvWindow)
	}
	st := newMuxStream(s, id, priority, credit)
	s.streams[id] = st

	// SYNs are queued under mu so the peer sees stream IDs in increasing order.
	s.sendControl(muxCmdSYN, id, []byte{priority})
	atomic.AddUint64(&DefaultSnmp.MuxStreamsOpened, 1)
	return st, nil
}

// AcceptStream waits for and returns the next stream opened by the peer.
func (s *MuxSession) AcceptStream() (*MuxStream, error) {
	if s.isClosed() {
		return nil, io.ErrClosedPipe
	}
	select {
	case st := <-s.chAccepts:
		return st, nil
	case <-s.die:
		return nil, io.ErrClosedPipe
	}
}

// NumStreams returns the number of streams that are not yet fully closed.
func (s *MuxSession) NumStreams() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.streams)
}

// Close shuts the session down and closes the underlying connection. Blocked
// readers, writers and acceptors return io.ErrClosedPipe. Close does not wait
// for queued frames to be transmitted.
func (s *MuxSession) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return io.ErrClosedPipe
	}
	s.shutdown()
	return nil
}

func (s *MuxSession) isClosed() bool {
	select {
	case <-s.die:
		return true
	default:
		return false
	}
}

// shutdown must not wait on sendLoop or recvLoop: sendLoop may be stuck in conn.Write.
func (s *MuxSession) shutdown() {
	s.dieOnce.Do(func() {
		close(s.die)
		s.conn.Close()

		s.sendMu.Lock()
		s.sendClosed = true
		s.ctrlQueue = nil
		s.dataQueue = nil
		s.sendMu.Unlock()

		s.mu.Lock()
		streams := s.streams
		s.streams = make(map[uint32]*MuxStream)
		s.mu.Unlock()

		var closed uint64
		for _, st := range streams {
			st.mu.Lock()
			if !st.removed {
				st.removed = true
				closed++
			}
			st.mu.Unlock()
		}
		atomic.AddUint64(&DefaultSnmp.MuxStreamsClosed, closed)
	})
}

func (s *MuxSession) notifySend() {
	select {
	case s.chSend <- struct{}{}:
	default:
	}
}

func (s *MuxSession) sendControl(cmd byte, sid uint32, payload []byte) {
	s.sendMu.Lock()
	s.pushControlLocked(cmd, sid, payload)
	s.sendMu.Unlock()
}

// pushControlLocked requires sendMu.
func (s *MuxSession) pushControlLocked(cmd byte, sid uint32, payload []byte) {
	if s.sendClosed {
		return
	}
	buf := make([]byte, muxHeaderSize+len(payload))
	muxPutHeader(buf, cmd, sid, len(payload))
	copy(buf[muxHeaderSize:], payload)
	s.ctrlQueue = append(s.ctrlQueue, &muxFrame{buf: buf})
	atomic.AddUint64(&DefaultSnmp.MuxFramesSent, 1)
	s.notifySend()
}

// pushData queues one data frame for st; it reports false once the session is closed.
func (s *MuxSession) pushData(st *MuxStream, p []byte) bool {
	f := &muxFrame{stream: st, priority: st.priority}
	if len(p) == s.config.MaxFrameSize {
		f.buf = s.framePool.Get().([]byte)
		f.pooled = true
	} else {
		f.buf = make([]byte, muxHeaderSize+len(p))
	}
	muxPutHeader(f.buf, muxCmdPSH, st.id, len(p))
	copy(f.buf[muxHeaderSize:], p)

	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.sendClosed {
		s.releaseFrame(f)
		return false
	}
	s.dataSeq++
	f.seq = s.dataSeq
	heap.Push(&s.dataQueue, f)
	st.queuedFrames++
	atomic.AddUint64(&DefaultSnmp.MuxFramesSent, 1)
	atomic.AddUint64(&DefaultSnmp.MuxBytesSent, uint64(len(p)))
	s.notifySend()
	return true
}

func (s *MuxSession) releaseFrame(f *muxFrame) {
	if f.pooled {
		s.framePool.Put(f.buf)
	}
}

// popFrame returns the next frame to transmit, or nil if nothing is queued.
func (s *MuxSession) popFrame() *muxFrame {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	if len(s.ctrlQueue) > 0 {
		f := s.ctrlQueue[0]
		s.ctrlQueue[0] = nil
		s.ctrlQueue = s.ctrlQueue[1:]
		return f
	}
	if len(s.dataQueue) == 0 {
		return nil
	}
	f := heap.Pop(&s.dataQueue).(*muxFrame)
	st := f.stream
	st.queuedFrames--
	if st.queuedFrames == 0 && st.finPending {
		st.finPending = false
		s.pushControlLocked(muxCmdFIN, st.id, nil)
	}
	return f
}

// purgeLocked drops st's queued data frames and releases a deferred FIN; requires sendMu.
func (s *MuxSession) purgeLocked(st *MuxStream) {
	if st.queuedFrames > 0 {
		kept := s.dataQueue[:0]
		for _, f := range s.dataQueue {
			if f.stream == st {
				s.releaseFrame(f)
			} else {
				kept = append(kept, f)
			}
		}
		clear(s.dataQueue[len(kept):])
		s.dataQueue = kept
		heap.Init(&s.dataQueue)
		st.queuedFrames = 0
	}
	if st.finPending {
		st.finPending = false
		s.pushControlLocked(muxCmdFIN, st.id, nil)
	}
}

func (s *MuxSession) sendLoop() {
	for {
		f := s.popFrame()
		if f == nil {
			select {
			case <-s.chSend:
				continue
			case <-s.die:
				return
			}
		}

		_, err := s.conn.Write(f.buf)
		s.releaseFrame(f)
		if err != nil {
			s.shutdown()
			return
		}
	}
}

func (s *MuxSession) recvLoop() {
	defer s.shutdown()

	r := bufio.NewReaderSize(s.conn, muxReadBufferSize)
	var hdr [muxHeaderSize]byte
	var ctrl [8]byte
	var lastRemoteID uint32
	settled := false

	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return
		}
		if hdr[0] != muxVersion {
			return
		}
		cmd := hdr[1]
		sid := binary.LittleEndian.Uint32(hdr[2:])
		length := binary.LittleEndian.Uint32(hdr[6:])
		if length > muxMaxFrameSize {
			return
		}
		if settled == (cmd == muxCmdSettings) {
			return // SETTINGS must be the first frame, and only the first
		}

		if cmd == muxCmdPSH {
			data := make([]byte, length)
			if _, err := io.ReadFull(r, data); err != nil {
				return
			}
			atomic.AddUint64(&DefaultSnmp.MuxFramesReceived, 1)
			atomic.AddUint64(&DefaultSnmp.MuxBytesReceived, uint64(length))
			if !s.handlePSH(sid, data) {
				return
			}
			continue
		}

		var want uint32
		switch cmd {
		case muxCmdSettings:
			want = 8
		case muxCmdSYN:
			want = 1
		case muxCmdFIN:
			want = 0
		case muxCmdUPD:
			want = 4
		default:
			return
		}
		if length != want {
			return
		}
		if _, err := io.ReadFull(r, ctrl[:length]); err != nil {
			return
		}
		atomic.AddUint64(&DefaultSnmp.MuxFramesReceived, 1)

		switch cmd {
		case muxCmdSettings:
			recvWindow := binary.LittleEndian.Uint32(ctrl[0:])
			sendWindow := binary.LittleEndian.Uint32(ctrl[4:])
			if sid != 0 || recvWindow == 0 || recvWindow > muxMaxWindow || sendWindow == 0 || sendWindow > muxMaxWindow {
				return
			}
			s.handleSettings(int(recvWindow), int(sendWindow))
			settled = true
		case muxCmdSYN:
			if !s.isRemoteID(sid) || sid <= lastRemoteID {
				return
			}
			lastRemoteID = sid
			s.handleSYN(sid, ctrl[0])
		case muxCmdFIN:
			s.handleFIN(sid)
		case muxCmdUPD:
			s.handleUPD(sid, binary.LittleEndian.Uint32(ctrl[:]))
		}
	}
}

// isRemoteID reports whether sid belongs to the peer's half of the ID space.
func (s *MuxSession) isRemoteID(sid uint32) bool {
	return sid != 0 && (sid%2 == 1) == (s.config.Side == MuxSideServer)
}

func (s *MuxSession) lookup(sid uint32) *MuxStream {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streams[sid]
}

func (s *MuxSession) removeStream(st *MuxStream) {
	s.mu.Lock()
	if s.streams[st.id] == st {
		delete(s.streams, st.id)
	}
	s.mu.Unlock()
	atomic.AddUint64(&DefaultSnmp.MuxStreamsClosed, 1)
}

func (s *MuxSession) handleSettings(recvWindow, sendWindow int) {
	s.peerSendWindow.Store(int64(sendWindow))

	s.mu.Lock()
	defer s.mu.Unlock()
	s.peerRecvWindow = recvWindow
	s.peerReady = true
	if recvWindow >= s.config.SendWindow {
		return
	}
	// Only locally opened streams can predate SETTINGS; they started with the full SendWindow.
	delta := int64(recvWindow - s.config.SendWindow)
	for _, st := range s.streams {
		st.mu.Lock()
		st.sendCredit += delta
		st.mu.Unlock()
	}
}

func (s *MuxSession) handleSYN(sid uint32, priority uint8) {
	s.mu.Lock()
	if s.isClosed() {
		s.mu.Unlock()
		return
	}
	st := newMuxStream(s, sid, priority, min(s.config.SendWindow, s.peerRecvWindow))
	select {
	case s.chAccepts <- st:
		s.streams[sid] = st
		s.mu.Unlock()
		atomic.AddUint64(&DefaultSnmp.MuxStreamsOpened, 1)
	default:
		s.mu.Unlock()
		s.sendControl(muxCmdFIN, sid, nil) // accept backlog full: refuse the stream
	}
}

// handlePSH buffers inbound data; it reports false if the peer overran the stream window.
func (s *MuxSession) handlePSH(sid uint32, data []byte) bool {
	st := s.lookup(sid)
	if st == nil {
		return true
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.localClosed || st.remoteClosed || len(data) == 0 {
		return true
	}
	if int64(st.buffered)+int64(len(data)) > s.peerSendWindow.Load() {
		return false
	}
	st.buffers = append(st.buffers, data)
	st.buffered += len(data)
	st.notifyReadEvent()
	return true
}

func (s *MuxSession) handleFIN(sid uint32) {
	st := s.lookup(sid)
	if st == nil {
		return
	}

	st.mu.Lock()
	if st.remoteClosed {
		st.mu.Unlock()
		return
	}
	st.remoteClosed = true
	close(st.chRemoteClose)

	// the peer discards anything that arrives after its FIN
	s.sendMu.Lock()
	s.purgeLocked(st)
	s.sendMu.Unlock()

	remove := st.markRemovedLocked()
	st.mu.Unlock()
	if remove {
		s.removeStream(st)
	}
}

func (s *MuxSession) handleUPD(sid uint32, increment uint32) {
	st := s.lookup(sid)
	if st == nil {
		return
	}

	st.mu.Lock()
	st.sendCredit += int64(increment)
	st.notifyWriteEvent()
	st.mu.Unlock()
}

// returnCreditLocked grants consumed bytes back to the peer once half the window
// has been read; requires st.mu.
func (s *MuxSession) returnCreditLocked(st *MuxStream) {
	if st.localClosed || st.remoteClosed {
		return
	}
	window := min(int(s.peerSendWindow.Load()), s.config.RecvWindow)
	if st.consumed < max(window/2, 1) {
		return
	}
	var p [4]byte
	binary.LittleEndian.PutUint32(p[:], uint32(st.consumed))
	st.consumed = 0
	s.sendControl(muxCmdUPD, st.id, p[:])
}

// MuxStream is an ordered, flow-controlled byte stream within a MuxSession.
type MuxStream struct {
	sess     *MuxSession
	id       uint32
	priority uint8

	writeMu sync.Mutex // keeps the frames of concurrent Writes from interleaving

	mu           sync.Mutex
	buffers      [][]byte // inbound data not yet read
	buffered     int      // total bytes in buffers
	consumed     int      // bytes read but not yet granted back to the peer
	sendCredit   int64    // bytes that may be written before the peer grants more
	readDeadline time.Time
	localClosed  bool
	remoteClosed bool
	removed      bool

	chReadEvent   chan struct{}
	chWriteEvent  chan struct{}
	chLocalClose  chan struct{}
	chRemoteClose chan struct{}

	// guarded by sess.sendMu
	queuedFrames int  // data frames waiting in the session send queue
	finPending   bool // FIN deferred until the queued data frames are sent
}

func newMuxStream(sess *MuxSession, id uint32, priority uint8, credit int) *MuxStream {
	return &MuxStream{
		sess:          sess,
		id:            id,
		priority:      priority,
		sendCredit:    int64(credit),
		chReadEvent:   make(chan struct{}, 1),
		chWriteEvent:  make(chan struct{}, 1),
		chLocalClose:  make(chan struct{}),
		chRemoteClose: make(chan struct{}),
	}
}

// ID returns the stream ID, which is identical on both peers.
func (st *MuxStream) ID() uint32 { return st.id }

// Read reads buffered stream data. After the local side has closed, remaining
// buffered data can still be read, then io.ErrClosedPipe is returned. Once the
// peer has closed and all data is drained, Read returns io.EOF.
func (st *MuxStream) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	s := st.sess

	for {
		st.mu.Lock()
		if st.buffered > 0 {
			n := 0
			for n < len(b) && len(st.buffers) > 0 {
				c := copy(b[n:], st.buffers[0])
				n += c
				if c == len(st.buffers[0]) {
					st.buffers[0] = nil
					st.buffers = st.buffers[1:]
				} else {
					st.buffers[0] = st.buffers[0][c:]
				}
			}
			st.buffered -= n
			st.consumed += n
			s.returnCreditLocked(st)
			if st.buffered > 0 {
				st.notifyReadEvent()
			}
			remove := st.markRemovedLocked()
			st.mu.Unlock()
			if remove {
				s.removeStream(st)
			}
			return n, nil
		}

		var err error
		switch {
		case st.localClosed:
			err = io.ErrClosedPipe
		case st.remoteClosed:
			err = io.EOF
		case s.isClosed():
			err = io.ErrClosedPipe
		}
		deadline := st.readDeadline
		st.mu.Unlock()
		if err != nil {
			return 0, err
		}

		var timer *time.Timer
		var timeout <-chan time.Time
		if !deadline.IsZero() {
			d := time.Until(deadline)
			if d <= 0 {
				return 0, errTimeout
			}
			timer = time.NewTimer(d)
			timeout = timer.C
		}

		select {
		case <-st.chReadEvent:
		case <-st.chLocalClose:
		case <-st.chRemoteClose:
		case <-s.die:
		case <-timeout:
			return 0, errTimeout
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

// Write blocks until all of b has been accepted for transmission, waiting for
// window updates from the peer as needed. It returns a short count only
// together with an error.
func (st *MuxStream) Write(b []byte) (n int, err error) {
	st.writeMu.Lock()
	defer st.writeMu.Unlock()
	s := st.sess

	for {
		st.mu.Lock()
		if st.localClosed || st.remoteClosed || s.isClosed() {
			st.mu.Unlock()
			return n, io.ErrClosedPipe
		}
		if len(b) == 0 {
			st.mu.Unlock()
			return n, nil
		}
		if st.sendCredit > 0 {
			sz := int(min(int64(len(b)), st.sendCredit, int64(s.config.MaxFrameSize)))
			if !s.pushData(st, b[:sz]) {
				st.mu.Unlock()
				return n, io.ErrClosedPipe
			}
			st.sendCredit -= int64(sz)
			n += sz
			b = b[sz:]
			st.mu.Unlock()
			continue
		}
		st.mu.Unlock()

		select {
		case <-st.chWriteEvent:
		case <-st.chLocalClose:
		case <-st.chRemoteClose:
		case <-s.die:
		}
	}
}

// Close half-closes the stream: no more data can be written, while data
// already buffered from the peer stays readable. Data accepted by earlier
// Writes is sent before the peer is told about the close.
func (st *MuxStream) Close() error {
	s := st.sess

	st.mu.Lock()
	if st.localClosed {
		st.mu.Unlock()
		return io.ErrClosedPipe
	}
	st.localClosed = true
	close(st.chLocalClose)

	s.sendMu.Lock()
	if st.queuedFrames > 0 {
		st.finPending = true
	} else {
		s.pushControlLocked(muxCmdFIN, st.id, nil)
	}
	s.sendMu.Unlock()

	remove := st.markRemovedLocked()
	st.mu.Unlock()
	if remove {
		s.removeStream(st)
	}

	if s.isClosed() {
		return io.ErrClosedPipe
	}
	return nil
}

// SetReadDeadline sets the deadline for pending and future Read calls.
// A zero value disables the deadline.
func (st *MuxStream) SetReadDeadline(t time.Time) error {
	st.mu.Lock()
	st.readDeadline = t
	st.mu.Unlock()
	st.notifyReadEvent()
	return nil
}

// markRemovedLocked reports, once, that both sides have closed and all inbound
// data is drained; requires st.mu.
func (st *MuxStream) markRemovedLocked() bool {
	if st.removed || !st.localClosed || !st.remoteClosed || st.buffered > 0 {
		return false
	}
	st.removed = true
	return true
}

func (st *MuxStream) notifyReadEvent() {
	select {
	case st.chReadEvent <- struct{}{}:
	default:
	}
}

func (st *MuxStream) notifyWriteEvent() {
	select {
	case st.chWriteEvent <- struct{}{}:
	default:
	}
}
