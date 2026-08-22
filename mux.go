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
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// MuxSide identifies the client or server role in a multiplexed session.
type MuxSide uint8

const (
	MuxSideClient MuxSide = 0
	MuxSideServer MuxSide = 1
)

// Stream priority levels for outbound data scheduling.
const (
	MuxPriorityHigh   uint8 = 0
	MuxPriorityNormal uint8 = 1
	MuxPriorityLow    uint8 = 2
)

const (
	muxHeaderSize = 8

	muxCmdOpen   uint8 = 1
	muxCmdClose  uint8 = 2
	muxCmdWindow uint8 = 3
	muxCmdData   uint8 = 4
)

// MuxConfig holds multiplexing session parameters.
type MuxConfig struct {
	Side         MuxSide
	MaxFrameSize uint32
	SendWindow   uint32
	RecvWindow   uint32
}

// DefaultMuxConfig returns sensible defaults for a multiplexing session.
func DefaultMuxConfig() MuxConfig {
	return MuxConfig{
		Side:         MuxSideClient,
		MaxFrameSize: 4096,
		SendWindow:   256 * 1024,
		RecvWindow:   256 * 1024,
	}
}

// MuxSession multiplexes many ordered sub-streams over one connection.
type MuxSession struct {
	conn   net.Conn
	cfg    MuxConfig
	snmp   *Snmp
	closed atomic.Bool

	mu          sync.Mutex
	streams     map[uint32]*MuxStream
	nextID      uint32
	numStreams  int
	acceptCh    chan *MuxStream
	acceptClose sync.Once

	writeMu   sync.Mutex
	controlQ  [][]byte
	dataQ     [3][]*muxPendingData
	writeWake chan struct{}
	shutdown  chan struct{}
}

type muxPendingData struct {
	streamID uint32
	frame    []byte
}

// MuxStream is an ordered sub-stream within a MuxSession.
type MuxStream struct {
	session  *MuxSession
	id       uint32
	priority uint8

	readMu       sync.Mutex
	readBuf      []byte
	readNotify   chan struct{}
	readDeadline time.Time
	remoteClosed bool

	writeMu     sync.Mutex
	writeNotify chan struct{}
	localClosed bool
	sendCredit  uint32

	pendingDataFrames int
	closePending      bool
}

type muxTimeoutError struct{}

func (muxTimeoutError) Error() string   { return "mux read timeout" }
func (muxTimeoutError) Timeout() bool   { return true }
func (muxTimeoutError) Temporary() bool { return true }

// NewMuxSession wraps conn with a multiplexing session.
func NewMuxSession(conn net.Conn, cfg *MuxConfig) (*MuxSession, error) {
	if conn == nil {
		return nil, io.ErrClosedPipe
	}
	c := DefaultMuxConfig()
	if cfg != nil {
		c = *cfg
	}
	if c.MaxFrameSize == 0 {
		c.MaxFrameSize = DefaultMuxConfig().MaxFrameSize
	}
	if c.SendWindow == 0 {
		c.SendWindow = DefaultMuxConfig().SendWindow
	}
	if c.RecvWindow == 0 {
		c.RecvWindow = DefaultMuxConfig().RecvWindow
	}

	var nextID uint32
	switch c.Side {
	case MuxSideClient:
		nextID = 1
	case MuxSideServer:
		nextID = 2
	default:
		nextID = 1
	}

	s := &MuxSession{
		conn:      conn,
		cfg:       c,
		snmp:      DefaultSnmp,
		streams:   make(map[uint32]*MuxStream),
		nextID:    nextID,
		acceptCh:  make(chan *MuxStream, 64),
		writeWake: make(chan struct{}, 1),
		shutdown:  make(chan struct{}),
	}
	go s.readLoop()
	go s.writeLoop()
	return s, nil
}

// Close shuts down the session without waiting for background goroutines.
func (s *MuxSession) Close() error {
	if s.closed.Swap(true) {
		return nil
	}

	s.mu.Lock()
	streams := make([]*MuxStream, 0, len(s.streams))
	for _, st := range s.streams {
		streams = append(streams, st)
	}
	s.mu.Unlock()

	close(s.shutdown)
	_ = s.conn.SetDeadline(time.Now())

	for _, st := range streams {
		st.signalClosed()
	}
	s.closeAcceptCh()

	select {
	case s.writeWake <- struct{}{}:
	default:
	}
	return nil
}

func (s *MuxSession) closeAcceptCh() {
	s.acceptClose.Do(func() {
		close(s.acceptCh)
	})
}

// NumStreams returns the number of live streams in the session.
func (s *MuxSession) NumStreams() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.numStreams
}

// OpenStream opens a locally-initiated sub-stream.
func (s *MuxSession) OpenStream(priority uint8) (*MuxStream, error) {
	if s.closed.Load() {
		return nil, io.ErrClosedPipe
	}
	if priority > MuxPriorityLow {
		priority = MuxPriorityNormal
	}

	s.mu.Lock()
	if s.closed.Load() {
		s.mu.Unlock()
		return nil, io.ErrClosedPipe
	}
	id := s.nextID
	s.nextID += 2
	st := s.newStream(id, priority)
	s.streams[id] = st
	s.numStreams++
	s.mu.Unlock()

	frame := s.encodeOpen(id, priority)
	s.enqueueControl(frame)
	atomic.AddUint64(&s.snmp.MuxStreamsOpened, 1)
	return st, nil
}

// AcceptStream waits for a remotely-opened sub-stream.
func (s *MuxSession) AcceptStream() (*MuxStream, error) {
	for {
		select {
		case st, ok := <-s.acceptCh:
			if !ok {
				return nil, io.ErrClosedPipe
			}
			return st, nil
		case <-s.shutdown:
			return nil, io.ErrClosedPipe
		}
	}
}

func (s *MuxSession) newStream(id uint32, priority uint8) *MuxStream {
	return &MuxStream{
		session:     s,
		id:          id,
		priority:    priority,
		sendCredit:  s.cfg.SendWindow,
		readNotify:  make(chan struct{}, 1),
		writeNotify: make(chan struct{}, 1),
	}
}

func (s *MuxSession) removeStreamIfDone(st *MuxStream) {
	st.readMu.Lock()
	readEmpty := len(st.readBuf) == 0
	st.readMu.Unlock()
	st.writeMu.Lock()
	bothClosed := st.localClosed && st.remoteClosed
	st.writeMu.Unlock()
	if !bothClosed || !readEmpty {
		return
	}

	s.mu.Lock()
	if cur, ok := s.streams[st.id]; ok && cur == st {
		delete(s.streams, st.id)
		s.numStreams--
	}
	s.mu.Unlock()
}

func (s *MuxSession) readLoop() {
	header := make([]byte, muxHeaderSize)
	for {
		if s.closed.Load() {
			return
		}
		if _, err := io.ReadFull(s.conn, header); err != nil {
			if !s.closed.Load() {
				s.Close()
			}
			return
		}
		cmd := header[0]
		id := binary.BigEndian.Uint32(header[2:6])
		length := binary.BigEndian.Uint16(header[6:8])

		var payload []byte
		if length > 0 {
			payload = make([]byte, length)
			if _, err := io.ReadFull(s.conn, payload); err != nil {
				if !s.closed.Load() {
					s.Close()
				}
				return
			}
		}

		atomic.AddUint64(&s.snmp.MuxFramesReceived, 1)
		s.handleFrame(cmd, id, payload)
	}
}

func (s *MuxSession) handleFrame(cmd uint8, id uint32, payload []byte) {
	switch cmd {
	case muxCmdOpen:
		if len(payload) < 1 {
			return
		}
		priority := payload[0]
		s.mu.Lock()
		if s.closed.Load() {
			s.mu.Unlock()
			return
		}
		if _, exists := s.streams[id]; exists {
			s.mu.Unlock()
			return
		}
		st := s.newStream(id, priority)
		s.streams[id] = st
		s.numStreams++
		s.mu.Unlock()
		atomic.AddUint64(&s.snmp.MuxStreamsOpened, 1)
		select {
		case s.acceptCh <- st:
		case <-s.shutdown:
		}

	case muxCmdClose:
		st := s.getStream(id)
		if st == nil {
			return
		}
		st.writeMu.Lock()
		st.remoteClosed = true
		st.writeMu.Unlock()
		st.readMu.Lock()
		st.remoteClosed = true
		st.signalRead()
		st.readMu.Unlock()
		st.signalWrite()
		atomic.AddUint64(&s.snmp.MuxStreamsClosed, 1)
		s.removeStreamIfDone(st)

	case muxCmdWindow:
		if len(payload) < 4 {
			return
		}
		credit := binary.BigEndian.Uint32(payload)
		st := s.getStream(id)
		if st == nil {
			return
		}
		st.writeMu.Lock()
		st.sendCredit += credit
		st.writeMu.Unlock()
		st.signalWrite()

	case muxCmdData:
		if len(payload) == 0 {
			return
		}
		st := s.getStream(id)
		if st == nil {
			return
		}
		atomic.AddUint64(&s.snmp.MuxBytesReceived, uint64(len(payload)))
		st.readMu.Lock()
		st.readBuf = append(st.readBuf, payload...)
		st.signalRead()
		st.readMu.Unlock()

	default:
	}
}

func (s *MuxSession) getStream(id uint32) *MuxStream {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streams[id]
}

func (s *MuxSession) encodeFrame(cmd uint8, id uint32, payload []byte) []byte {
	frame := make([]byte, muxHeaderSize+len(payload))
	frame[0] = cmd
	frame[1] = 0
	binary.BigEndian.PutUint32(frame[2:6], id)
	binary.BigEndian.PutUint16(frame[6:8], uint16(len(payload)))
	copy(frame[muxHeaderSize:], payload)
	return frame
}

func (s *MuxSession) encodeOpen(id uint32, priority uint8) []byte {
	return s.encodeFrame(muxCmdOpen, id, []byte{priority})
}

func (s *MuxSession) encodeClose(id uint32) []byte {
	return s.encodeFrame(muxCmdClose, id, nil)
}

func (s *MuxSession) encodeWindow(id uint32, credit uint32) []byte {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, credit)
	return s.encodeFrame(muxCmdWindow, id, payload)
}

func (s *MuxSession) encodeData(id uint32, data []byte) []byte {
	return s.encodeFrame(muxCmdData, id, data)
}

func (s *MuxSession) enqueueControl(frame []byte) {
	s.writeMu.Lock()
	s.controlQ = append(s.controlQ, frame)
	s.writeMu.Unlock()
	s.signalWrite()
}

func (s *MuxSession) enqueueData(st *MuxStream, priority uint8, frame []byte) {
	if priority > MuxPriorityLow {
		priority = MuxPriorityNormal
	}
	st.writeMu.Lock()
	st.pendingDataFrames++
	st.writeMu.Unlock()

	s.writeMu.Lock()
	s.dataQ[priority] = append(s.dataQ[priority], &muxPendingData{streamID: st.id, frame: frame})
	s.writeMu.Unlock()
	s.signalWrite()
}

func (s *MuxSession) signalWrite() {
	select {
	case s.writeWake <- struct{}{}:
	default:
	}
}

func (s *MuxSession) dequeueFrame() ([]byte, uint32, bool) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if len(s.controlQ) > 0 {
		frame := s.controlQ[0]
		s.controlQ = s.controlQ[1:]
		return frame, 0, true
	}
	for pri := 0; pri < 3; pri++ {
		if len(s.dataQ[pri]) > 0 {
			item := s.dataQ[pri][0]
			s.dataQ[pri] = s.dataQ[pri][1:]
			return item.frame, item.streamID, true
		}
	}
	return nil, 0, false
}

func (s *MuxSession) onDataFrameSent(streamID uint32) {
	if streamID == 0 {
		return
	}
	st := s.getStream(streamID)
	if st == nil {
		return
	}
	st.writeMu.Lock()
	st.pendingDataFrames--
	if st.pendingDataFrames == 0 && st.closePending {
		st.closePending = false
		st.writeMu.Unlock()
		s.enqueueControl(s.encodeClose(streamID))
		return
	}
	st.writeMu.Unlock()
}

func (s *MuxSession) writeLoop() {
	for {
		select {
		case <-s.shutdown:
			return
		default:
		}

		frame, streamID, ok := s.dequeueFrame()
		if !ok {
			select {
			case <-s.shutdown:
				return
			case <-s.writeWake:
				continue
			}
		}

		if s.closed.Load() {
			return
		}
		if _, err := s.conn.Write(frame); err != nil {
			if !s.closed.Load() {
				s.Close()
			}
			return
		}
		atomic.AddUint64(&s.snmp.MuxFramesSent, 1)
		if frame[0] == muxCmdData {
			s.onDataFrameSent(streamID)
		}
	}
}

// ID returns the stream identifier.
func (st *MuxStream) ID() uint32 {
	return st.id
}

// Read reads data from the stream.
func (st *MuxStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if st.session.closed.Load() {
			return 0, io.ErrClosedPipe
		}

		st.readMu.Lock()
		if len(st.readBuf) > 0 {
			n := copy(p, st.readBuf)
			freed := uint32(n)
			st.readBuf = st.readBuf[n:]
			st.readMu.Unlock()
			if freed > 0 {
				st.session.enqueueControl(st.session.encodeWindow(st.id, freed))
			}
			return n, nil
		}
		if st.remoteClosed {
			st.readMu.Unlock()
			return 0, io.ErrClosedPipe
		}
		if st.localClosed {
			st.readMu.Unlock()
			return 0, io.ErrClosedPipe
		}
		if !st.readDeadline.IsZero() && !time.Now().Before(st.readDeadline) {
			st.readMu.Unlock()
			return 0, muxTimeoutError{}
		}
		deadline := st.readDeadline
		notify := st.readNotify
		st.readMu.Unlock()

		var timeout <-chan time.Time
		if !deadline.IsZero() {
			d := time.Until(deadline)
			if d <= 0 {
				return 0, muxTimeoutError{}
			}
			timeout = time.After(d)
		}

		select {
		case <-st.session.shutdown:
			return 0, io.ErrClosedPipe
		case <-timeout:
		case <-notify:
		}
	}
}

// Write writes data to the stream; it blocks until all bytes are accepted.
func (st *MuxStream) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	written := 0
	for written < len(p) {
		if st.session.closed.Load() {
			return written, io.ErrClosedPipe
		}

		st.writeMu.Lock()
		for {
			if st.localClosed || st.remoteClosed {
				st.writeMu.Unlock()
				if written > 0 {
					return written, io.ErrClosedPipe
				}
				return 0, io.ErrClosedPipe
			}
			if st.sendCredit > 0 {
				break
			}
			if st.session.closed.Load() {
				st.writeMu.Unlock()
				if written > 0 {
					return written, io.ErrClosedPipe
				}
				return 0, io.ErrClosedPipe
			}
			st.writeMu.Unlock()
			select {
			case <-st.session.shutdown:
				if written > 0 {
					return written, io.ErrClosedPipe
				}
				return 0, io.ErrClosedPipe
			case <-st.writeNotify:
			}
			st.writeMu.Lock()
		}

		chunk := len(p) - written
		maxFrame := int(st.session.cfg.MaxFrameSize)
		if chunk > maxFrame {
			chunk = maxFrame
		}
		if chunk > int(st.sendCredit) {
			chunk = int(st.sendCredit)
		}

		data := make([]byte, chunk)
		copy(data, p[written:written+chunk])
		st.sendCredit -= uint32(chunk)
		priority := st.priority
		st.writeMu.Unlock()

		frame := st.session.encodeData(st.id, data)
		st.session.enqueueData(st, priority, frame)
		atomic.AddUint64(&st.session.snmp.MuxBytesSent, uint64(chunk))

		written += chunk
	}
	return written, nil
}

// Close half-closes the stream for writing.
func (st *MuxStream) Close() error {
	if st.session.closed.Load() {
		return io.ErrClosedPipe
	}

	st.writeMu.Lock()
	if st.localClosed {
		st.writeMu.Unlock()
		return nil
	}
	st.localClosed = true
	if st.pendingDataFrames == 0 {
		st.writeMu.Unlock()
		st.session.enqueueControl(st.session.encodeClose(st.id))
	} else {
		st.closePending = true
		st.writeMu.Unlock()
	}
	st.signalWrite()
	atomic.AddUint64(&st.session.snmp.MuxStreamsClosed, 1)
	st.session.removeStreamIfDone(st)
	return nil
}

// SetReadDeadline sets the read deadline.
func (st *MuxStream) SetReadDeadline(t time.Time) error {
	st.readMu.Lock()
	st.readDeadline = t
	st.readMu.Unlock()
	st.signalRead()
	return nil
}

func (st *MuxStream) signalRead() {
	select {
	case st.readNotify <- struct{}{}:
	default:
	}
}

func (st *MuxStream) signalWrite() {
	select {
	case st.writeNotify <- struct{}{}:
	default:
	}
}

func (st *MuxStream) signalClosed() {
	st.writeMu.Lock()
	st.localClosed = true
	st.remoteClosed = true
	st.writeMu.Unlock()
	st.signalWrite()

	st.readMu.Lock()
	st.remoteClosed = true
	st.readMu.Unlock()
	st.signalRead()
}
