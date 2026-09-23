package kcp

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// MuxSide determines which half of the stream ID space a MuxSession allocates from.
type MuxSide uint8

const (
	MuxSideClient MuxSide = iota // opens odd stream IDs
	MuxSideServer                // opens even stream IDs
)

// Stream priorities; lower value is scheduled first.
const (
	MuxPriorityHigh uint8 = iota
	MuxPriorityNormal
	MuxPriorityLow
	muxNumPriorities
)

const (
	muxCmdSYN byte = iota // payload: priority byte
	muxCmdPSH             // payload: data
	muxCmdFIN
	muxCmdUPD // payload: uint32 credit increment

	muxHeaderSize = 9 // cmd(1) sid(4) len(4)
)

var (
	errMuxInvalidPriority = errors.New("mux: invalid priority")
	errMuxInvalidConfig   = errors.New("mux: invalid config")
	errMuxProtocol        = errors.New("mux: protocol error")
)

// MuxConfig configures a MuxSession. Window sizes are in bytes; SendWindow
// should not exceed the peer's RecvWindow.
type MuxConfig struct {
	Side         MuxSide
	MaxFrameSize int
	SendWindow   int
	RecvWindow   int
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

type muxTimeoutError struct{}

func (muxTimeoutError) Error() string   { return "mux: i/o timeout" }
func (muxTimeoutError) Timeout() bool   { return true }
func (muxTimeoutError) Temporary() bool { return true }

type muxFrame struct {
	cmd    byte
	sid    uint32
	data   []byte
	stream *MuxStream // set for frames counted in stream.pending
}

// MuxSession multiplexes many ordered streams over a single net.Conn.
type MuxSession struct {
	conn net.Conn
	cfg  MuxConfig

	mu       sync.Mutex
	streams  map[uint32]*MuxStream
	nextID   uint32
	acceptCh chan *MuxStream

	sendMu     sync.Mutex
	ctrlQueue  []muxFrame
	dataQueues [muxNumPriorities][]muxFrame
	sendNotify chan struct{}

	die     chan struct{}
	dieOnce sync.Once
}

// NewMuxSession creates a multiplexed session on top of conn.
func NewMuxSession(conn net.Conn, cfg *MuxConfig) (*MuxSession, error) {
	c := DefaultMuxConfig()
	if cfg != nil {
		c = *cfg
	}
	if conn == nil || c.MaxFrameSize <= 0 || c.SendWindow <= 0 || c.RecvWindow <= 0 ||
		(c.Side != MuxSideClient && c.Side != MuxSideServer) {
		return nil, errMuxInvalidConfig
	}
	s := &MuxSession{
		conn:       conn,
		cfg:        c,
		streams:    make(map[uint32]*MuxStream),
		acceptCh:   make(chan *MuxStream, 1024),
		sendNotify: make(chan struct{}, 1),
		die:        make(chan struct{}),
	}
	if c.Side == MuxSideClient {
		s.nextID = 1
	} else {
		s.nextID = 2
	}
	go s.recvLoop()
	go s.sendLoop()
	return s, nil
}

// Close shuts down the session and all its streams without waiting for I/O.
func (s *MuxSession) Close() error {
	err := io.ErrClosedPipe
	s.dieOnce.Do(func() {
		close(s.die)
		err = nil
		go s.conn.Close()
	})
	return err
}

func (s *MuxSession) isClosed() bool {
	select {
	case <-s.die:
		return true
	default:
		return false
	}
}

// NumStreams returns the number of streams currently tracked by the session.
func (s *MuxSession) NumStreams() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.streams)
}

// OpenStream opens a new stream with the given priority.
func (s *MuxSession) OpenStream(priority uint8) (*MuxStream, error) {
	if priority >= muxNumPriorities {
		return nil, errMuxInvalidPriority
	}
	s.mu.Lock()
	if s.isClosed() {
		s.mu.Unlock()
		return nil, io.ErrClosedPipe
	}
	id := s.nextID
	s.nextID += 2
	st := newMuxStream(s, id, priority)
	s.streams[id] = st
	s.mu.Unlock()

	atomic.AddUint64(&DefaultSnmp.MuxStreamsOpened, 1)
	s.enqueue(muxFrame{cmd: muxCmdSYN, sid: id, data: []byte{priority}}, true)
	return st, nil
}

// AcceptStream waits for the next stream opened by the peer.
func (s *MuxSession) AcceptStream() (*MuxStream, error) {
	select {
	case st := <-s.acceptCh:
		return st, nil
	case <-s.die:
		return nil, io.ErrClosedPipe
	}
}

func (s *MuxSession) enqueue(f muxFrame, ctrl bool) {
	s.sendMu.Lock()
	if ctrl {
		s.ctrlQueue = append(s.ctrlQueue, f)
	} else {
		st := f.stream
		st.pending++
		s.dataQueues[st.priority] = append(s.dataQueues[st.priority], f)
	}
	s.sendMu.Unlock()
	select {
	case s.sendNotify <- struct{}{}:
	default:
	}
}

// enqueueFIN sends FIN as a control frame unless data for the stream is still
// queued, in which case it must follow that data to preserve ordering.
func (s *MuxSession) enqueueFIN(st *MuxStream) {
	s.sendMu.Lock()
	if st.pending == 0 {
		s.ctrlQueue = append(s.ctrlQueue, muxFrame{cmd: muxCmdFIN, sid: st.id})
	} else {
		s.dataQueues[st.priority] = append(s.dataQueues[st.priority], muxFrame{cmd: muxCmdFIN, sid: st.id})
	}
	s.sendMu.Unlock()
	select {
	case s.sendNotify <- struct{}{}:
	default:
	}
}

func (s *MuxSession) dequeue() (muxFrame, bool) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if len(s.ctrlQueue) > 0 {
		f := s.ctrlQueue[0]
		s.ctrlQueue[0] = muxFrame{}
		s.ctrlQueue = s.ctrlQueue[1:]
		return f, true
	}
	for p := range s.dataQueues {
		q := s.dataQueues[p]
		if len(q) > 0 {
			f := q[0]
			q[0] = muxFrame{}
			s.dataQueues[p] = q[1:]
			if f.stream != nil {
				f.stream.pending--
			}
			return f, true
		}
	}
	return muxFrame{}, false
}

func (s *MuxSession) sendLoop() {
	buf := make([]byte, muxHeaderSize+s.cfg.MaxFrameSize)
	for {
		f, ok := s.dequeue()
		if !ok {
			select {
			case <-s.sendNotify:
				continue
			case <-s.die:
				return
			}
		}
		if s.isClosed() {
			return
		}
		n := muxHeaderSize + len(f.data)
		if n > len(buf) {
			buf = make([]byte, n)
		}
		buf[0] = f.cmd
		binary.LittleEndian.PutUint32(buf[1:], f.sid)
		binary.LittleEndian.PutUint32(buf[5:], uint32(len(f.data)))
		copy(buf[muxHeaderSize:], f.data)
		if _, err := s.conn.Write(buf[:n]); err != nil {
			s.Close()
			return
		}
		atomic.AddUint64(&DefaultSnmp.MuxFramesSent, 1)
		if f.cmd == muxCmdPSH {
			atomic.AddUint64(&DefaultSnmp.MuxBytesSent, uint64(len(f.data)))
		}
	}
}

func (s *MuxSession) recvLoop() {
	defer s.Close()
	var hdr [muxHeaderSize]byte
	maxLen := s.cfg.MaxFrameSize
	if s.cfg.RecvWindow > maxLen {
		maxLen = s.cfg.RecvWindow
	}
	for {
		if _, err := io.ReadFull(s.conn, hdr[:]); err != nil {
			return
		}
		cmd := hdr[0]
		sid := binary.LittleEndian.Uint32(hdr[1:])
		length := binary.LittleEndian.Uint32(hdr[5:])
		if int64(length) > int64(maxLen) {
			return
		}
		var payload []byte
		if length > 0 {
			payload = make([]byte, length)
			if _, err := io.ReadFull(s.conn, payload); err != nil {
				return
			}
		}
		atomic.AddUint64(&DefaultSnmp.MuxFramesReceived, 1)
		if err := s.handleFrame(cmd, sid, payload); err != nil {
			return
		}
	}
}

func (s *MuxSession) handleFrame(cmd byte, sid uint32, payload []byte) error {
	s.mu.Lock()
	st := s.streams[sid]
	s.mu.Unlock()

	switch cmd {
	case muxCmdSYN:
		if st != nil || len(payload) != 1 || payload[0] >= muxNumPriorities {
			return errMuxProtocol
		}
		if (sid%2 == 1) == (s.cfg.Side == MuxSideClient) {
			return errMuxProtocol
		}
		st = newMuxStream(s, sid, payload[0])
		s.mu.Lock()
		s.streams[sid] = st
		s.mu.Unlock()
		atomic.AddUint64(&DefaultSnmp.MuxStreamsOpened, 1)
		select {
		case s.acceptCh <- st:
		default:
			st.Close()
		}
	case muxCmdPSH:
		atomic.AddUint64(&DefaultSnmp.MuxBytesReceived, uint64(len(payload)))
		if st != nil {
			st.pushData(payload)
		}
	case muxCmdFIN:
		if st != nil {
			st.remoteClose()
		}
	case muxCmdUPD:
		if len(payload) != 4 {
			return errMuxProtocol
		}
		if st != nil {
			st.addCredit(int(binary.LittleEndian.Uint32(payload)))
		}
	default:
		return errMuxProtocol
	}
	return nil
}

func (s *MuxSession) maybeRemove(st *MuxStream) {
	st.mu.Lock()
	done := st.localClosed && st.remoteClosed && len(st.buf) == 0
	st.mu.Unlock()
	if done {
		s.mu.Lock()
		if s.streams[st.id] == st {
			delete(s.streams, st.id)
		}
		s.mu.Unlock()
	}
}

// MuxStream is one ordered, flow-controlled byte stream within a MuxSession.
type MuxStream struct {
	sess     *MuxSession
	id       uint32
	priority uint8
	pending  int // queued data frames; guarded by sess.sendMu

	mu           sync.Mutex
	buf          []byte
	consumed     int
	credit       int
	localClosed  bool
	remoteClosed bool
	readDeadline time.Time

	readEv  chan struct{}
	writeEv chan struct{}
}

func newMuxStream(s *MuxSession, id uint32, priority uint8) *MuxStream {
	return &MuxStream{
		sess:     s,
		id:       id,
		priority: priority,
		credit:   s.cfg.SendWindow,
		readEv:   make(chan struct{}, 1),
		writeEv:  make(chan struct{}, 1),
	}
}

func notify(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// ID returns the stream identifier, identical on both peers.
func (st *MuxStream) ID() uint32 { return st.id }

// Read reads buffered inbound data, blocking until data, EOF, close or deadline.
func (st *MuxStream) Read(b []byte) (int, error) {
	for {
		if st.sess.isClosed() {
			return 0, io.ErrClosedPipe
		}
		st.mu.Lock()
		if len(st.buf) > 0 {
			n := copy(b, st.buf)
			st.buf = st.buf[n:]
			if len(st.buf) == 0 {
				st.buf = nil
			}
			st.consumed += n
			var upd int
			if st.consumed >= st.sess.cfg.RecvWindow/2 || len(st.buf) == 0 {
				upd = st.consumed
				st.consumed = 0
			}
			sendUpd := upd > 0 && !st.remoteClosed
			st.mu.Unlock()
			if sendUpd {
				var p [4]byte
				binary.LittleEndian.PutUint32(p[:], uint32(upd))
				st.sess.enqueue(muxFrame{cmd: muxCmdUPD, sid: st.id, data: p[:]}, true)
			}
			st.sess.maybeRemove(st)
			return n, nil
		}
		if st.remoteClosed {
			st.mu.Unlock()
			notify(st.readEv)
			return 0, io.EOF
		}
		if st.localClosed {
			st.mu.Unlock()
			return 0, io.ErrClosedPipe
		}
		deadline := st.readDeadline
		st.mu.Unlock()

		var timeout <-chan time.Time
		if !deadline.IsZero() {
			d := time.Until(deadline)
			if d <= 0 {
				return 0, muxTimeoutError{}
			}
			t := time.NewTimer(d)
			timeout = t.C
			defer t.Stop()
		}
		select {
		case <-st.readEv:
		case <-timeout:
			return 0, muxTimeoutError{}
		case <-st.sess.die:
			return 0, io.ErrClosedPipe
		}
	}
}

// Write queues all of b for sending, blocking while the send window is exhausted.
func (st *MuxStream) Write(b []byte) (int, error) {
	written := 0
	for written < len(b) {
		if st.sess.isClosed() {
			return written, io.ErrClosedPipe
		}
		st.mu.Lock()
		if st.localClosed || st.remoteClosed {
			st.mu.Unlock()
			notify(st.writeEv)
			return written, io.ErrClosedPipe
		}
		if st.credit <= 0 {
			st.mu.Unlock()
			select {
			case <-st.writeEv:
			case <-st.sess.die:
				return written, io.ErrClosedPipe
			}
			continue
		}
		n := len(b) - written
		if n > st.credit {
			n = st.credit
		}
		if n > st.sess.cfg.MaxFrameSize {
			n = st.sess.cfg.MaxFrameSize
		}
		st.credit -= n
		if st.credit > 0 {
			notify(st.writeEv)
		}
		st.mu.Unlock()

		data := make([]byte, n)
		copy(data, b[written:written+n])
		st.sess.enqueue(muxFrame{cmd: muxCmdPSH, sid: st.id, data: data, stream: st}, false)
		written += n
	}
	return written, nil
}

// Close half-closes the stream: no further writes, buffered inbound data stays readable.
func (st *MuxStream) Close() error {
	st.mu.Lock()
	if st.localClosed {
		st.mu.Unlock()
		return io.ErrClosedPipe
	}
	st.localClosed = true
	st.mu.Unlock()
	notify(st.writeEv)
	notify(st.readEv)
	atomic.AddUint64(&DefaultSnmp.MuxStreamsClosed, 1)
	if !st.sess.isClosed() {
		st.sess.enqueueFIN(st)
	}
	st.sess.maybeRemove(st)
	return nil
}

// SetReadDeadline sets the deadline for pending and future Read calls.
func (st *MuxStream) SetReadDeadline(t time.Time) error {
	st.mu.Lock()
	st.readDeadline = t
	st.mu.Unlock()
	notify(st.readEv)
	return nil
}

func (st *MuxStream) pushData(p []byte) {
	st.mu.Lock()
	st.buf = append(st.buf, p...)
	st.mu.Unlock()
	notify(st.readEv)
}

func (st *MuxStream) remoteClose() {
	st.mu.Lock()
	st.remoteClosed = true
	st.mu.Unlock()
	notify(st.readEv)
	notify(st.writeEv)
	st.sess.maybeRemove(st)
}

func (st *MuxStream) addCredit(n int) {
	st.mu.Lock()
	st.credit += n
	st.mu.Unlock()
	notify(st.writeEv)
}
