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
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// MuxSide determines which half of the stream ID space a MuxSession allocates from.
type MuxSide uint8

const (
	// MuxSideClient opens streams with odd IDs (1, 3, 5, ...).
	MuxSideClient MuxSide = iota
	// MuxSideServer opens streams with even IDs (2, 4, 6, ...).
	MuxSideServer
)

// Stream priorities, from most to least urgent. Frames of a higher priority
// stream are always written before queued frames of a lower priority stream.
const (
	MuxPriorityHigh uint8 = iota
	MuxPriorityNormal
	MuxPriorityLow
)

const muxNumPriorities = int(MuxPriorityLow) + 1

// Wire format of a frame, all integers little endian:
//
//	| cmd (1) | priority (1) | payload length (2) | stream id (4) | payload |
//
// SYN and ACK carry the sender's RecvWindow and SendWindow (4 bytes each),
// UPD carries the number of bytes consumed by the reader since the last UPD.
const (
	muxCmdSYN uint8 = iota + 1 // open a stream
	muxCmdACK                  // acknowledge a SYN
	muxCmdPSH                  // stream data
	muxCmdUPD                  // window update
	muxCmdFIN                  // half-close a stream
)

const (
	muxHeaderSize     = 8
	muxMaxPayloadSize = math.MaxUint16
	muxWindowPayload  = 8
	muxUpdatePayload  = 4
	muxAcceptBacklog  = 1024
	muxRecvBufferSize = 64 * 1024
)

var (
	errMuxProtocol          = errors.New("kcp: mux protocol violation")
	errMuxStreamIDExhausted = errors.New("kcp: mux stream IDs exhausted")
)

// MuxConfig configures a MuxSession.
type MuxConfig struct {
	// Side selects the stream ID space; the two peers must use opposite sides.
	Side MuxSide
	// MaxFrameSize is the largest data payload carried by a single frame.
	MaxFrameSize int
	// SendWindow caps the bytes a stream may have outstanding (sent but not
	// yet consumed by the remote reader).
	SendWindow int
	// RecvWindow caps the bytes a stream buffers for the local reader.
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
		return fmt.Errorf("kcp: invalid mux side %d", c.Side)
	}
	if c.MaxFrameSize <= 0 || c.MaxFrameSize > muxMaxPayloadSize {
		return fmt.Errorf("kcp: mux MaxFrameSize must be in [1, %d]", muxMaxPayloadSize)
	}
	if c.SendWindow <= 0 || uint64(c.SendWindow) > math.MaxUint32 {
		return fmt.Errorf("kcp: mux SendWindow must be in [1, %d]", uint32(math.MaxUint32))
	}
	if c.RecvWindow <= 0 || uint64(c.RecvWindow) > math.MaxUint32 {
		return fmt.Errorf("kcp: mux RecvWindow must be in [1, %d]", uint32(math.MaxUint32))
	}
	return nil
}

type muxFrame struct {
	buf    []byte
	stream *MuxStream // nil for control frames
}

type muxChunk struct {
	raw  []byte // buffer to return to the pool
	data []byte // unread part of raw
}

// MuxSession multiplexes many independent, ordered streams over one net.Conn.
type MuxSession struct {
	conn net.Conn
	cfg  MuxConfig

	mu      sync.Mutex
	streams map[uint32]*MuxStream
	nextID  uint32
	noIDs   bool
	ctrlQ   []*muxFrame
	dataQ   [muxNumPriorities][]*muxFrame
	closed  bool // Close was called
	broken  bool // the underlying connection failed

	chAccept chan *MuxStream
	chSend   chan struct{}
	die      chan struct{}
	chBroken chan struct{}

	connCloseOnce sync.Once
	bufSize       int
	bufPool       sync.Pool
}

// NewMuxSession starts multiplexing over conn. A nil cfg uses DefaultMuxConfig.
// The session takes ownership of conn and closes it when the session ends.
func NewMuxSession(conn net.Conn, cfg *MuxConfig) (*MuxSession, error) {
	if conn == nil {
		return nil, errors.New("kcp: nil conn for mux session")
	}
	c := DefaultMuxConfig()
	if cfg != nil {
		c = *cfg
	}
	if err := c.validate(); err != nil {
		return nil, err
	}

	s := &MuxSession{
		conn:     conn,
		cfg:      c,
		streams:  make(map[uint32]*MuxStream),
		chAccept: make(chan *MuxStream, muxAcceptBacklog),
		chSend:   make(chan struct{}, 1),
		die:      make(chan struct{}),
		chBroken: make(chan struct{}),
		bufSize:  muxHeaderSize + c.MaxFrameSize,
	}
	s.bufPool.New = func() any { return make([]byte, s.bufSize) }
	if c.Side == MuxSideClient {
		s.nextID = 1
	} else {
		s.nextID = 2
	}

	go s.recvLoop()
	go s.sendLoop()
	return s, nil
}

// OpenStream opens a new stream with the given priority. Priorities beyond
// MuxPriorityLow are treated as MuxPriorityLow.
func (s *MuxSession) OpenStream(priority uint8) (*MuxStream, error) {
	priority = min(priority, MuxPriorityLow)

	s.mu.Lock()
	if s.closed || s.broken {
		s.mu.Unlock()
		return nil, io.ErrClosedPipe
	}
	if s.noIDs {
		s.mu.Unlock()
		return nil, errMuxStreamIDExhausted
	}
	id := s.nextID
	if id > math.MaxUint32-2 {
		s.noIDs = true
	} else {
		s.nextID += 2
	}

	st := newMuxStream(s, id, priority)
	st.recvWindow = s.cfg.RecvWindow
	s.streams[id] = st
	s.enqueueCtrlLocked(muxCmdSYN, priority, id, s.windowPayload())
	s.mu.Unlock()

	atomic.AddUint64(&DefaultSnmp.MuxStreamsOpened, 1)
	s.notifySend()
	return st, nil
}

// AcceptStream waits for and returns the next stream opened by the peer.
func (s *MuxSession) AcceptStream() (*MuxStream, error) {
	select {
	case <-s.die:
		return nil, io.ErrClosedPipe
	default:
	}

	select {
	case st := <-s.chAccept:
		return st, nil
	case <-s.die:
		return nil, io.ErrClosedPipe
	case <-s.chBroken:
		// streams that arrived before the connection failed may still hold data
		select {
		case st := <-s.chAccept:
			return st, nil
		default:
			return nil, io.ErrClosedPipe
		}
	}
}

// NumStreams returns the number of streams tracked by the session.
func (s *MuxSession) NumStreams() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.streams)
}

// Close terminates the session and all of its streams, then closes the
// underlying connection in the background. It never waits for in-flight I/O.
func (s *MuxSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	s.closed = true
	for id, st := range s.streams {
		st.releaseRecvBufLocked()
		delete(s.streams, id)
		atomic.AddUint64(&DefaultSnmp.MuxStreamsClosed, 1)
	}
	s.dropAllFramesLocked()
	s.mu.Unlock()

	close(s.die)
	s.closeConn()
	return nil
}

// closeConn closes the underlying connection asynchronously, since Close on
// some connections may wait behind a blocked Write.
func (s *MuxSession) closeConn() {
	s.connCloseOnce.Do(func() { go s.conn.Close() })
}

// markBroken handles failure of the underlying connection: every stream is
// treated as closed by the peer, so readers drain what is buffered and see
// io.EOF, while writers fail with io.ErrClosedPipe.
func (s *MuxSession) markBroken() {
	s.mu.Lock()
	if !s.closed && !s.broken {
		s.broken = true
		close(s.chBroken)
		for _, st := range s.streams {
			if !st.remoteClosed {
				st.remoteCloseLocked()
			}
			s.tryRemoveLocked(st)
		}
		s.dropAllFramesLocked()
	}
	s.mu.Unlock()
	s.closeConn()
}

func (s *MuxSession) dropAllFramesLocked() {
	for _, f := range s.ctrlQ {
		s.finishFrameLocked(f, io.ErrClosedPipe)
	}
	s.ctrlQ = nil
	for p := range s.dataQ {
		for _, f := range s.dataQ[p] {
			s.finishFrameLocked(f, io.ErrClosedPipe)
		}
		s.dataQ[p] = nil
	}
}

// dropStreamFramesLocked removes the queued data frames of st, failing its
// pending Write.
func (s *MuxSession) dropStreamFramesLocked(st *MuxStream) {
	q := s.dataQ[st.prio]
	kept := q[:0]
	for _, f := range q {
		if f.stream == st {
			s.finishFrameLocked(f, io.ErrClosedPipe)
		} else {
			kept = append(kept, f)
		}
	}
	clear(q[len(kept):])
	s.dataQ[st.prio] = kept
}

// finishFrameLocked completes a frame that has been taken off the queues.
func (s *MuxSession) finishFrameLocked(f *muxFrame, err error) {
	if f.stream != nil {
		f.stream.chWriteDone <- err
		s.putBuf(f.buf)
	}
}

// tryRemoveLocked forgets st once both directions are closed and the local
// reader has drained everything.
func (s *MuxSession) tryRemoveLocked(st *MuxStream) {
	if st.localClosed && st.remoteClosed && st.recvBufBytes == 0 && s.streams[st.id] == st {
		delete(s.streams, st.id)
		atomic.AddUint64(&DefaultSnmp.MuxStreamsClosed, 1)
	}
}

func (s *MuxSession) isRemoteID(id uint32) bool {
	if id == 0 {
		return false
	}
	even := id%2 == 0
	return even == (s.cfg.Side == MuxSideClient)
}

func (s *MuxSession) windowPayload() []byte {
	var b [muxWindowPayload]byte
	binary.LittleEndian.PutUint32(b[0:], uint32(s.cfg.RecvWindow))
	binary.LittleEndian.PutUint32(b[4:], uint32(s.cfg.SendWindow))
	return b[:]
}

func encodeMuxHeader(b []byte, cmd, prio uint8, length int, sid uint32) {
	b[0] = cmd
	b[1] = prio
	binary.LittleEndian.PutUint16(b[2:], uint16(length))
	binary.LittleEndian.PutUint32(b[4:], sid)
}

func (s *MuxSession) enqueueCtrlLocked(cmd, prio uint8, sid uint32, payload []byte) {
	buf := make([]byte, muxHeaderSize+len(payload))
	encodeMuxHeader(buf, cmd, prio, len(payload), sid)
	copy(buf[muxHeaderSize:], payload)
	s.ctrlQ = append(s.ctrlQ, &muxFrame{buf: buf})
}

func (s *MuxSession) notifySend() {
	select {
	case s.chSend <- struct{}{}:
	default:
	}
}

func (s *MuxSession) getBuf(n int) []byte {
	if n <= s.bufSize {
		return s.bufPool.Get().([]byte)[:n]
	}
	return make([]byte, n)
}

func (s *MuxSession) putBuf(b []byte) {
	if cap(b) == s.bufSize {
		s.bufPool.Put(b[:s.bufSize])
	}
}

// nextFrameLocked pops the most urgent frame: control frames first, then data
// frames by stream priority.
func (s *MuxSession) nextFrameLocked() *muxFrame {
	if len(s.ctrlQ) > 0 {
		f := s.ctrlQ[0]
		s.ctrlQ[0] = nil
		s.ctrlQ = s.ctrlQ[1:]
		return f
	}
	for p := range s.dataQ {
		if q := s.dataQ[p]; len(q) > 0 {
			f := q[0]
			q[0] = nil
			s.dataQ[p] = q[1:]
			return f
		}
	}
	return nil
}

func (s *MuxSession) sendLoop() {
	for {
		s.mu.Lock()
		if s.closed || s.broken {
			s.mu.Unlock()
			return
		}
		f := s.nextFrameLocked()
		s.mu.Unlock()

		if f == nil {
			select {
			case <-s.chSend:
				continue
			case <-s.die:
				return
			case <-s.chBroken:
				return
			}
		}

		_, err := s.conn.Write(f.buf)
		s.mu.Lock()
		if err != nil {
			s.finishFrameLocked(f, io.ErrClosedPipe)
			s.mu.Unlock()
			s.markBroken()
			return
		}
		atomic.AddUint64(&DefaultSnmp.MuxFramesSent, 1)
		if f.stream != nil {
			atomic.AddUint64(&DefaultSnmp.MuxBytesSent, uint64(len(f.buf)-muxHeaderSize))
		}
		s.finishFrameLocked(f, nil)
		s.mu.Unlock()
	}
}

func (s *MuxSession) recvLoop() {
	r := bufio.NewReaderSize(s.conn, muxRecvBufferSize)
	var hdr [muxHeaderSize]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			s.markBroken()
			return
		}
		cmd := hdr[0]
		prio := hdr[1]
		length := int(binary.LittleEndian.Uint16(hdr[2:]))
		sid := binary.LittleEndian.Uint32(hdr[4:])

		var payload []byte
		if length > 0 {
			payload = s.getBuf(length)
			if _, err := io.ReadFull(r, payload); err != nil {
				s.putBuf(payload)
				s.markBroken()
				return
			}
		}
		atomic.AddUint64(&DefaultSnmp.MuxFramesReceived, 1)

		if err := s.handleFrame(cmd, prio, sid, payload); err != nil {
			s.markBroken()
			return
		}
	}
}

// handleFrame processes one inbound frame and takes ownership of payload.
func (s *MuxSession) handleFrame(cmd, prio uint8, sid uint32, payload []byte) error {
	if cmd == muxCmdPSH {
		return s.handleData(sid, payload)
	}

	var err error
	switch cmd {
	case muxCmdSYN:
		err = s.handleSYN(sid, prio, payload)
	case muxCmdACK:
		err = s.handleACK(sid, payload)
	case muxCmdUPD:
		err = s.handleUPD(sid, payload)
	case muxCmdFIN:
		s.handleFIN(sid)
	default:
		err = errMuxProtocol
	}
	s.putBuf(payload)
	return err
}

func (s *MuxSession) handleData(sid uint32, payload []byte) error {
	n := len(payload)
	atomic.AddUint64(&DefaultSnmp.MuxBytesReceived, uint64(n))

	s.mu.Lock()
	st := s.streams[sid]
	if n == 0 || st == nil || st.remoteClosed || s.closed {
		s.mu.Unlock()
		s.putBuf(payload)
		return nil
	}
	st.recvBuf = append(st.recvBuf, muxChunk{raw: payload, data: payload})
	st.recvBufBytes += n
	s.mu.Unlock()
	notifyChan(st.chRead)
	return nil
}

func (s *MuxSession) handleSYN(sid uint32, prio uint8, payload []byte) error {
	if len(payload) != muxWindowPayload || !s.isRemoteID(sid) {
		return errMuxProtocol
	}
	peerRecv := int(binary.LittleEndian.Uint32(payload[0:]))
	peerSend := int(binary.LittleEndian.Uint32(payload[4:]))

	s.mu.Lock()
	if s.closed || s.broken {
		s.mu.Unlock()
		return nil
	}
	if _, ok := s.streams[sid]; ok {
		s.mu.Unlock()
		return errMuxProtocol
	}
	if len(s.chAccept) >= cap(s.chAccept) {
		// backlog full: refuse the stream
		s.enqueueCtrlLocked(muxCmdFIN, 0, sid, nil)
		s.mu.Unlock()
		s.notifySend()
		return nil
	}

	st := newMuxStream(s, sid, min(prio, MuxPriorityLow))
	st.sendCredit = int64(min(s.cfg.SendWindow, peerRecv))
	st.recvWindow = min(s.cfg.RecvWindow, peerSend)
	st.acked = true
	s.streams[sid] = st
	s.enqueueCtrlLocked(muxCmdACK, 0, sid, s.windowPayload())
	s.chAccept <- st
	s.mu.Unlock()

	atomic.AddUint64(&DefaultSnmp.MuxStreamsOpened, 1)
	s.notifySend()
	return nil
}

func (s *MuxSession) handleACK(sid uint32, payload []byte) error {
	if len(payload) != muxWindowPayload {
		return errMuxProtocol
	}
	peerRecv := int(binary.LittleEndian.Uint32(payload[0:]))
	peerSend := int(binary.LittleEndian.Uint32(payload[4:]))

	s.mu.Lock()
	st := s.streams[sid]
	if st == nil || st.acked {
		s.mu.Unlock()
		return nil
	}
	st.acked = true
	st.sendCredit += int64(min(s.cfg.SendWindow, peerRecv))
	st.recvWindow = min(s.cfg.RecvWindow, peerSend)
	s.mu.Unlock()
	notifyChan(st.chWrite)
	return nil
}

func (s *MuxSession) handleUPD(sid uint32, payload []byte) error {
	if len(payload) != muxUpdatePayload {
		return errMuxProtocol
	}
	delta := int64(binary.LittleEndian.Uint32(payload))

	s.mu.Lock()
	st := s.streams[sid]
	if st == nil {
		s.mu.Unlock()
		return nil
	}
	st.sendCredit += delta
	s.mu.Unlock()
	notifyChan(st.chWrite)
	return nil
}

func (s *MuxSession) handleFIN(sid uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.streams[sid]
	if st == nil || st.remoteClosed {
		return
	}
	st.remoteCloseLocked()
	s.tryRemoveLocked(st)
}

func notifyChan(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// MuxStream is one ordered, flow-controlled byte stream within a MuxSession.
type MuxStream struct {
	id   uint32
	prio uint8
	sess *MuxSession

	// guarded by sess.mu
	acked        bool
	sendCredit   int64
	recvWindow   int
	recvBuf      []muxChunk
	recvBufBytes int
	pendingUpd   int
	localClosed  bool
	remoteClosed bool
	readDeadline time.Time

	writeMu       sync.Mutex
	chWriteDone   chan error // result of the single in-flight data frame
	chRead        chan struct{}
	chWrite       chan struct{}
	chLocalClose  chan struct{}
	chRemoteClose chan struct{}
}

func newMuxStream(s *MuxSession, id uint32, prio uint8) *MuxStream {
	return &MuxStream{
		id:            id,
		prio:          prio,
		sess:          s,
		chWriteDone:   make(chan error, 1),
		chRead:        make(chan struct{}, 1),
		chWrite:       make(chan struct{}, 1),
		chLocalClose:  make(chan struct{}),
		chRemoteClose: make(chan struct{}),
	}
}

// ID returns the stream ID, which is identical on both peers.
func (st *MuxStream) ID() uint32 { return st.id }

// Read reads buffered stream data. After the peer closes the stream, Read
// returns io.EOF once the buffer is drained; after a local Close it returns
// io.ErrClosedPipe once the buffer is drained.
func (st *MuxStream) Read(b []byte) (int, error) {
	s := st.sess
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return 0, io.ErrClosedPipe
		}
		if st.recvBufBytes > 0 {
			if len(b) == 0 {
				s.mu.Unlock()
				return 0, nil
			}
			n := st.readLocked(b)
			updated := st.consumedLocked(n)
			s.tryRemoveLocked(st)
			s.mu.Unlock()
			if updated {
				s.notifySend()
			}
			return n, nil
		}
		if st.localClosed {
			s.mu.Unlock()
			return 0, io.ErrClosedPipe
		}
		if st.remoteClosed {
			s.mu.Unlock()
			return 0, io.EOF
		}
		var timeout <-chan time.Time
		var timer *time.Timer
		if !st.readDeadline.IsZero() {
			d := time.Until(st.readDeadline)
			if d <= 0 {
				s.mu.Unlock()
				return 0, errTimeout
			}
			timer = time.NewTimer(d)
			timeout = timer.C
		}
		s.mu.Unlock()

		select {
		case <-st.chRead:
		case <-st.chLocalClose:
		case <-st.chRemoteClose:
		case <-s.die:
		case <-timeout:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

func (st *MuxStream) readLocked(b []byte) int {
	n := 0
	for n < len(b) && len(st.recvBuf) > 0 {
		c := &st.recvBuf[0]
		m := copy(b[n:], c.data)
		n += m
		c.data = c.data[m:]
		if len(c.data) == 0 {
			st.sess.putBuf(c.raw)
			st.recvBuf[0] = muxChunk{}
			st.recvBuf = st.recvBuf[1:]
		}
	}
	st.recvBufBytes -= n
	return n
}

// consumedLocked accounts n bytes handed to the reader and queues a window
// update once half of the receive window has been consumed.
func (st *MuxStream) consumedLocked(n int) bool {
	s := st.sess
	st.pendingUpd += n
	if st.remoteClosed || s.broken || st.pendingUpd < max(1, st.recvWindow/2) {
		return false
	}
	var b [muxUpdatePayload]byte
	binary.LittleEndian.PutUint32(b[:], uint32(st.pendingUpd))
	s.enqueueCtrlLocked(muxCmdUPD, 0, st.id, b[:])
	st.pendingUpd = 0
	return true
}

func (st *MuxStream) releaseRecvBufLocked() {
	for _, c := range st.recvBuf {
		st.sess.putBuf(c.raw)
	}
	st.recvBuf = nil
	st.recvBufBytes = 0
}

func (st *MuxStream) remoteCloseLocked() {
	st.remoteClosed = true
	close(st.chRemoteClose)
	st.sess.dropStreamFramesLocked(st)
}

func (st *MuxStream) writeErrLocked() error {
	s := st.sess
	if s.closed || s.broken || st.localClosed || st.remoteClosed {
		return io.ErrClosedPipe
	}
	return nil
}

// Write sends b on the stream, blocking until all of it has been handed to the
// underlying connection. It blocks while the stream's send window is exhausted.
func (st *MuxStream) Write(b []byte) (int, error) {
	st.writeMu.Lock()
	defer st.writeMu.Unlock()

	s := st.sess
	n := 0
	for {
		s.mu.Lock()
		if err := st.writeErrLocked(); err != nil {
			s.mu.Unlock()
			return n, err
		}
		if len(b) == 0 {
			s.mu.Unlock()
			return n, nil
		}
		if st.sendCredit <= 0 {
			s.mu.Unlock()
			select {
			case <-st.chWrite:
			case <-st.chLocalClose:
			case <-st.chRemoteClose:
			case <-s.die:
			case <-s.chBroken:
			}
			continue
		}

		sz := min(len(b), s.cfg.MaxFrameSize, int(min(st.sendCredit, math.MaxInt32)))
		st.sendCredit -= int64(sz)
		buf := s.getBuf(muxHeaderSize + sz)
		encodeMuxHeader(buf, muxCmdPSH, 0, sz, st.id)
		copy(buf[muxHeaderSize:], b[:sz])
		s.dataQ[st.prio] = append(s.dataQ[st.prio], &muxFrame{buf: buf, stream: st})
		s.mu.Unlock()
		s.notifySend()

		select {
		case err := <-st.chWriteDone:
			if err != nil {
				return n, err
			}
		case <-s.die:
			return n, io.ErrClosedPipe
		case <-s.chBroken:
			return n, io.ErrClosedPipe
		}
		n += sz
		b = b[sz:]
	}
}

// Close half-closes the stream: no more data may be written, while data
// already received stays readable until drained. Blocked writers are released
// with io.ErrClosedPipe.
func (st *MuxStream) Close() error {
	s := st.sess
	s.mu.Lock()
	if s.closed || st.localClosed {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	st.localClosed = true
	close(st.chLocalClose)
	s.dropStreamFramesLocked(st)
	if !s.broken {
		s.enqueueCtrlLocked(muxCmdFIN, 0, st.id, nil)
	}
	s.tryRemoveLocked(st)
	s.mu.Unlock()
	s.notifySend()
	return nil
}

// SetReadDeadline sets the deadline for pending and future Read calls. A zero
// value disables the deadline. An expired deadline makes Read fail with a
// net.Error whose Timeout() is true.
func (st *MuxStream) SetReadDeadline(t time.Time) error {
	s := st.sess
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	st.readDeadline = t
	s.mu.Unlock()
	notifyChan(st.chRead)
	return nil
}
