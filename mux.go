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
)

const (
	muxVersion     = 1
	muxHeaderSize  = 8
	muxWindowSize  = 8
	muxCmdOpen     = 0
	muxCmdClose    = 1
	muxCmdData     = 2
	muxCmdWindow   = 3
	muxAcceptQueue = 128
	muxShaperCap   = 1024
)

// MuxSide identifies the client or server role for stream ID allocation.
type MuxSide int

const (
	MuxSideClient MuxSide = iota
	MuxSideServer
)

// Stream priority levels. Lower numeric values are scheduled first.
const (
	MuxPriorityHigh   uint8 = 0
	MuxPriorityNormal uint8 = 1
	MuxPriorityLow    uint8 = 2
)

// MuxConfig holds multiplexing session parameters.
type MuxConfig struct {
	Side         MuxSide
	MaxFrameSize int
	SendWindow   int
	RecvWindow   int
}

// DefaultMuxConfig returns sensible defaults; callers should set Side.
func DefaultMuxConfig() MuxConfig {
	return MuxConfig{
		Side:         MuxSideClient,
		MaxFrameSize: 4096,
		SendWindow:   256 * 1024,
		RecvWindow:   256 * 1024,
	}
}

type muxTimeoutError struct{}

func (muxTimeoutError) Error() string   { return "i/o timeout" }
func (muxTimeoutError) Timeout() bool   { return true }
func (muxTimeoutError) Temporary() bool { return true }

var muxErrTimeout net.Error = muxTimeoutError{}

type muxFrame struct {
	cmd      byte
	sid      uint32
	priority uint8
	data     []byte
}

type muxWriteReq struct {
	ctrl     bool
	priority uint8
	frame    muxFrame
	done     chan error
}

type muxShaper struct {
	ctrl []muxWriteReq
	data [3][]muxWriteReq
}

func (q *muxShaper) push(req muxWriteReq) {
	if req.ctrl {
		q.ctrl = append(q.ctrl, req)
		return
	}
	p := int(req.priority)
	if p < 0 {
		p = 0
	}
	if p > 2 {
		p = 2
	}
	q.data[p] = append(q.data[p], req)
}

func (q *muxShaper) pop() (muxWriteReq, bool) {
	if len(q.ctrl) > 0 {
		req := q.ctrl[0]
		q.ctrl = q.ctrl[1:]
		return req, true
	}
	for p := 0; p < 3; p++ {
		if len(q.data[p]) > 0 {
			req := q.data[p][0]
			q.data[p] = q.data[p][1:]
			return req, true
		}
	}
	return muxWriteReq{}, false
}

func (q *muxShaper) len() int {
	n := len(q.ctrl)
	for p := 0; p < 3; p++ {
		n += len(q.data[p])
	}
	return n
}

// MuxSession multiplexes many ordered streams over one connection.
type MuxSession struct {
	conn net.Conn
	cfg  MuxConfig

	mu           sync.Mutex
	streams      map[uint32]*muxStream
	nextStreamID uint32
	closed       int32
	die          chan struct{}
	dieOnce      sync.Once

	acceptCh chan *muxStream

	shaperCh     chan muxWriteReq
	shaper       muxShaper
	shaperNotify chan struct{}
	shaperResume chan struct{}
}

// NewMuxSession creates a multiplexing session over conn.
func NewMuxSession(conn net.Conn, cfg *MuxConfig) (*MuxSession, error) {
	if conn == nil {
		return nil, io.ErrClosedPipe
	}
	c := DefaultMuxConfig()
	if cfg != nil {
		c = *cfg
	}
	if c.MaxFrameSize <= 0 {
		c.MaxFrameSize = DefaultMuxConfig().MaxFrameSize
	}
	if c.SendWindow <= 0 {
		c.SendWindow = DefaultMuxConfig().SendWindow
	}
	if c.RecvWindow <= 0 {
		c.RecvWindow = DefaultMuxConfig().RecvWindow
	}

	s := &MuxSession{
		conn:         conn,
		cfg:          c,
		streams:      make(map[uint32]*muxStream),
		die:          make(chan struct{}),
		acceptCh:     make(chan *muxStream, muxAcceptQueue),
		shaperCh:     make(chan muxWriteReq, muxShaperCap),
		shaperNotify: make(chan struct{}, 1),
		shaperResume: make(chan struct{}, 1),
	}
	if c.Side == MuxSideClient {
		s.nextStreamID = 1
	} else {
		s.nextStreamID = 2
	}

	go s.recvLoop()
	go s.sendLoop()
	go s.shaperLoop()
	return s, nil
}

func (s *MuxSession) isClosed() bool {
	return atomic.LoadInt32(&s.closed) != 0
}

// NumStreams returns the number of live streams in the session map.
func (s *MuxSession) NumStreams() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.streams)
}

// Close shuts down the session without waiting for background goroutines.
func (s *MuxSession) Close() error {
	var once bool
	s.dieOnce.Do(func() {
		atomic.StoreInt32(&s.closed, 1)
		close(s.die)
		once = true
	})
	if !once {
		return io.ErrClosedPipe
	}

	s.mu.Lock()
	for _, st := range s.streams {
		st.sessionClosed()
	}
	s.mu.Unlock()

	go s.conn.Close()
	return nil
}

// OpenStream opens a locally-initiated stream with the given priority.
func (s *MuxSession) OpenStream(priority uint8) (*MuxStream, error) {
	if s.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if priority > MuxPriorityLow {
		priority = MuxPriorityLow
	}

	s.mu.Lock()
	sid := s.nextStreamID
	s.nextStreamID += 2
	if sid == 0 || sid > 0xfffffffd {
		s.mu.Unlock()
		return nil, io.ErrClosedPipe
	}
	st := newMuxStream(s, sid, priority)
	s.streams[sid] = st
	s.mu.Unlock()

	if err := s.writeControl(muxFrame{cmd: muxCmdOpen, sid: sid, priority: priority, data: []byte{priority}}); err != nil {
		s.removeStream(st)
		return nil, err
	}
	st.sendInitialWindowUpdate()

	atomic.AddUint64(&DefaultSnmp.MuxStreamsOpened, 1)
	return &MuxStream{st: st}, nil
}

// AcceptStream waits for a stream opened by the remote peer.
func (s *MuxSession) AcceptStream() (*MuxStream, error) {
	select {
	case st := <-s.acceptCh:
		return &MuxStream{st: st}, nil
	case <-s.die:
		return nil, io.ErrClosedPipe
	}
}

func (s *MuxSession) getStream(sid uint32) *muxStream {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streams[sid]
}

func (s *MuxSession) addRemoteStream(sid uint32, priority uint8) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.streams[sid]; ok {
		return
	}
	st := newMuxStream(s, sid, priority)
	s.streams[sid] = st
	atomic.AddUint64(&DefaultSnmp.MuxStreamsOpened, 1)
	st.sendInitialWindowUpdate()

	select {
	case s.acceptCh <- st:
	case <-s.die:
	}
}

func (s *MuxSession) removeStream(st *muxStream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.streams[st.id]; ok && cur == st {
		delete(s.streams, st.id)
		atomic.AddUint64(&DefaultSnmp.MuxStreamsClosed, 1)
	}
}

func (s *MuxSession) maybeRemoveStream(st *muxStream) {
	st.mu.Lock()
	removable := st.localClosed && st.remoteClosed && len(st.recvBuf) == 0
	st.mu.Unlock()
	if removable {
		s.removeStream(st)
	}
}

func (s *MuxSession) writeControl(f muxFrame) error {
	done := make(chan error, 1)
	req := muxWriteReq{ctrl: true, frame: f, done: done}
	select {
	case s.shaperCh <- req:
	case <-s.die:
		return io.ErrClosedPipe
	}
	select {
	case err := <-done:
		return err
	case <-s.die:
		return io.ErrClosedPipe
	}
}

func (s *MuxSession) enqueue(req muxWriteReq) error {
	select {
	case s.shaperCh <- req:
		return nil
	case <-s.die:
		return io.ErrClosedPipe
	}
}

func (s *MuxSession) shaperLoop() {
	ch := s.shaperCh
	for {
		select {
		case <-s.die:
			return
		case req := <-ch:
			s.shaper.push(req)
			for len(ch) > 0 && s.shaper.len() < muxShaperCap {
				select {
				case req := <-ch:
					s.shaper.push(req)
				default:
					goto notify
				}
			}
		notify:
			select {
			case s.shaperNotify <- struct{}{}:
			default:
			}
			if s.shaper.len() >= muxShaperCap {
				ch = nil
			}
		case <-s.shaperResume:
			ch = s.shaperCh
		}
	}
}

func (s *MuxSession) sendLoop() {
	header := make([]byte, muxHeaderSize)
	for {
		select {
		case <-s.die:
			return
		case <-s.shaperNotify:
		frames:
			for {
				req, ok := s.shaper.pop()
				if !ok {
					select {
					case s.shaperResume <- struct{}{}:
					default:
					}
					break frames
				}
				n, err := s.writeFrame(header, req.frame)
				if req.done != nil {
					if err != nil {
						req.done <- err
					} else if n < len(req.frame.data) && req.frame.cmd == muxCmdData {
						req.done <- io.ErrClosedPipe
					} else {
						req.done <- nil
					}
				}
				if err != nil {
					return
				}
			}
		}
	}
}

func (s *MuxSession) writeFrame(header []byte, f muxFrame) (int, error) {
	header[0] = muxVersion
	header[1] = f.cmd
	binary.LittleEndian.PutUint16(header[2:], uint16(len(f.data)))
	binary.LittleEndian.PutUint32(header[4:], f.sid)

	atomic.AddUint64(&DefaultSnmp.MuxFramesSent, 1)
	if f.cmd == muxCmdData {
		atomic.AddUint64(&DefaultSnmp.MuxBytesSent, uint64(len(f.data)))
	}

	var err error
	if len(f.data) == 0 {
		_, err = s.conn.Write(header)
	} else {
		buf := make([]byte, len(header)+len(f.data))
		copy(buf, header)
		copy(buf[len(header):], f.data)
		_, err = s.conn.Write(buf)
	}
	if err != nil {
		return 0, err
	}
	return len(f.data), nil
}

func (s *MuxSession) recvLoop() {
	header := make([]byte, muxHeaderSize)
	for {
		if _, err := io.ReadFull(s.conn, header); err != nil {
			s.dieOnce.Do(func() {
				atomic.StoreInt32(&s.closed, 1)
				close(s.die)
			})
			return
		}

		atomic.AddUint64(&DefaultSnmp.MuxFramesReceived, 1)
		if header[0] != muxVersion {
			return
		}
		cmd := header[1]
		length := binary.LittleEndian.Uint16(header[2:])
		sid := binary.LittleEndian.Uint32(header[4:])

		var payload []byte
		if length > 0 {
			payload = make([]byte, length)
			if _, err := io.ReadFull(s.conn, payload); err != nil {
				return
			}
		}

		switch cmd {
		case muxCmdOpen:
			priority := MuxPriorityNormal
			if len(payload) > 0 {
				priority = payload[0]
			}
			s.addRemoteStream(sid, priority)
		case muxCmdClose:
			if st := s.getStream(sid); st != nil {
				st.remoteClose()
			}
		case muxCmdData:
			atomic.AddUint64(&DefaultSnmp.MuxBytesReceived, uint64(len(payload)))
			if st := s.getStream(sid); st != nil {
				if !st.push(payload) {
					// stream gone; drop
				}
			}
		case muxCmdWindow:
			if len(payload) >= muxWindowSize {
				consumed := binary.LittleEndian.Uint32(payload[0:4])
				window := binary.LittleEndian.Uint32(payload[4:8])
				if st := s.getStream(sid); st != nil {
					st.updateSendWindow(consumed, window)
				}
			}
		}
	}
}
