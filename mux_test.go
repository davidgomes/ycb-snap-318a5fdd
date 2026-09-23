package kcp

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newMuxPair(t *testing.T, clientCfg, serverCfg *MuxConfig) (*MuxSession, *MuxSession) {
	t.Helper()
	c1, c2 := net.Pipe()
	if clientCfg == nil {
		cfg := DefaultMuxConfig()
		clientCfg = &cfg
	}
	if serverCfg == nil {
		cfg := DefaultMuxConfig()
		serverCfg = &cfg
	}
	clientCfg.Side = MuxSideClient
	serverCfg.Side = MuxSideServer
	client, err := NewMuxSession(c1, clientCfg)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewMuxSession(c2, serverCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	return client, server
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestMuxConfigValidation(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	bad := []MuxConfig{
		{Side: 7, MaxFrameSize: 1024, SendWindow: 1024, RecvWindow: 1024},
		{Side: MuxSideClient, MaxFrameSize: 0, SendWindow: 1024, RecvWindow: 1024},
		{Side: MuxSideClient, MaxFrameSize: 70000, SendWindow: 1024, RecvWindow: 1024},
		{Side: MuxSideClient, MaxFrameSize: 1024, SendWindow: 0, RecvWindow: 1024},
		{Side: MuxSideClient, MaxFrameSize: 1024, SendWindow: 1024, RecvWindow: -1},
	}
	for i := range bad {
		if _, err := NewMuxSession(c1, &bad[i]); err == nil {
			t.Fatalf("config %d: expected error", i)
		}
	}
}

func TestMuxStreamIDs(t *testing.T) {
	client, server := newMuxPair(t, nil, nil)

	for i, want := range []uint32{1, 3, 5} {
		s, err := client.OpenStream(MuxPriorityNormal)
		if err != nil {
			t.Fatal(err)
		}
		if s.ID() != want {
			t.Fatalf("client stream %d: id %d, want %d", i, s.ID(), want)
		}
		rs, err := server.AcceptStream()
		if err != nil {
			t.Fatal(err)
		}
		if rs.ID() != want {
			t.Fatalf("accepted id %d, want %d", rs.ID(), want)
		}
	}
	for _, want := range []uint32{2, 4} {
		s, err := server.OpenStream(MuxPriorityHigh)
		if err != nil {
			t.Fatal(err)
		}
		if s.ID() != want {
			t.Fatalf("server stream id %d, want %d", s.ID(), want)
		}
		rs, err := client.AcceptStream()
		if err != nil {
			t.Fatal(err)
		}
		if rs.ID() != want {
			t.Fatalf("client accepted id %d, want %d", rs.ID(), want)
		}
	}
	if client.NumStreams() != 5 || server.NumStreams() != 5 {
		t.Fatalf("NumStreams = %d/%d, want 5", client.NumStreams(), server.NumStreams())
	}
}

func TestMuxEchoManyStreams(t *testing.T) {
	cfg := DefaultMuxConfig()
	cfg.SendWindow = 8192
	cfg.RecvWindow = 8192
	cfg.MaxFrameSize = 1000
	scfg := cfg
	client, server := newMuxPair(t, &cfg, &scfg)

	go func() {
		for {
			s, err := server.AcceptStream()
			if err != nil {
				return
			}
			go func() {
				io.Copy(s, s)
				s.Close()
			}()
		}
	}()

	const numStreams = 16
	const size = 200 * 1024
	var wg sync.WaitGroup
	errs := make(chan error, numStreams)
	for i := 0; i < numStreams; i++ {
		wg.Add(1)
		go func(prio uint8) {
			defer wg.Done()
			s, err := client.OpenStream(prio)
			if err != nil {
				errs <- err
				return
			}
			data := make([]byte, size)
			rand.Read(data)
			go func() {
				if n, err := s.Write(data); err != nil || n != len(data) {
					errs <- errors.New("short write")
				}
			}()
			got := make([]byte, len(data))
			if _, err := io.ReadFull(s, got); err != nil {
				errs <- err
				return
			}
			s.Close()
			if !bytes.Equal(got, data) {
				errs <- errors.New("echo mismatch")
			}
		}(uint8(i % 3))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	waitFor(t, "streams removed", func() bool {
		return client.NumStreams() == 0 && server.NumStreams() == 0
	})
}

func TestMuxFlowControlBlocksOnlyOneStream(t *testing.T) {
	cfg := DefaultMuxConfig()
	cfg.SendWindow = 4096
	cfg.RecvWindow = 4096
	scfg := cfg
	client, server := newMuxPair(t, &cfg, &scfg)

	blocked, err := client.OpenStream(MuxPriorityNormal)
	if err != nil {
		t.Fatal(err)
	}
	blockedRemote, _ := server.AcceptStream()
	other, _ := client.OpenStream(MuxPriorityNormal)
	otherRemote, _ := server.AcceptStream()

	payload := make([]byte, 64*1024)
	rand.Read(payload)
	var writeDone atomic.Bool
	writeErr := make(chan error, 1)
	go func() {
		_, err := blocked.Write(payload)
		writeDone.Store(true)
		writeErr <- err
	}()

	time.Sleep(100 * time.Millisecond)
	if writeDone.Load() {
		t.Fatal("write should block when the send window is exhausted")
	}

	go other.Write([]byte("hello"))
	buf := make([]byte, 5)
	otherRemote.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(otherRemote, buf); err != nil || string(buf) != "hello" {
		t.Fatalf("other stream stalled: %v %q", err, buf)
	}

	got := make([]byte, len(payload))
	if _, err := io.ReadFull(blockedRemote, got); err != nil {
		t.Fatal(err)
	}
	if err := <-writeErr; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("payload mismatch")
	}
}

// gatedConn blocks all Writes until the gate is opened.
type gatedConn struct {
	net.Conn
	gate chan struct{}
}

func (g *gatedConn) Write(p []byte) (int, error) {
	<-g.gate
	return g.Conn.Write(p)
}

func TestMuxPriorityScheduling(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c2.Close()
	gc := &gatedConn{Conn: c1, gate: make(chan struct{})}
	cfg := DefaultMuxConfig()
	cfg.MaxFrameSize = 1024
	m, err := NewMuxSession(gc, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	low, _ := m.OpenStream(MuxPriorityLow)
	normal, _ := m.OpenStream(MuxPriorityNormal)
	high, _ := m.OpenStream(MuxPriorityHigh)
	if _, err := low.Write(make([]byte, 8*1024)); err != nil {
		t.Fatal(err)
	}
	if _, err := normal.Write(make([]byte, 8*1024)); err != nil {
		t.Fatal(err)
	}
	if _, err := high.Write(make([]byte, 8*1024)); err != nil {
		t.Fatal(err)
	}
	low.Close()
	close(gc.gate)

	// The settings frame may already be in flight; after it, all control
	// frames precede data, and data drains strictly by priority.
	var order []uint32
	var sawData bool
	finAfterLowData := false
	lowRemaining := 8
	hdr := make([]byte, muxHeaderSize)
	for len(order) < 24 || !finAfterLowData {
		c2.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := io.ReadFull(c2, hdr); err != nil {
			t.Fatal(err)
		}
		cmd := hdr[1]
		sid := binary.LittleEndian.Uint32(hdr[4:])
		if n := binary.LittleEndian.Uint16(hdr[2:]); n > 0 {
			io.ReadFull(c2, make([]byte, n))
		}
		switch cmd {
		case muxCmdPSH:
			sawData = true
			order = append(order, sid)
			if sid == low.ID() {
				lowRemaining--
			}
		case muxCmdSYN:
			if sawData {
				t.Fatal("SYN sent after data")
			}
		case muxCmdFIN:
			if sid != low.ID() || lowRemaining != 0 {
				t.Fatalf("FIN for stream %d sent before its data drained", sid)
			}
			finAfterLowData = true
		}
	}
	want := []uint32{high.ID(), normal.ID(), low.ID()}
	for i, sid := range order {
		if sid != want[i/8] {
			t.Fatalf("frame %d from stream %d, want %d (order %v)", i, sid, want[i/8], order)
		}
	}
}

func TestMuxReadDeadline(t *testing.T) {
	client, server := newMuxPair(t, nil, nil)
	s, _ := client.OpenStream(MuxPriorityNormal)
	server.AcceptStream()

	s.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	start := time.Now()
	_, err := s.Read(make([]byte, 10))
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("expected timeout net.Error, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("deadline fired too late")
	}

	// extending the deadline from another goroutine wakes and re-arms the reader
	s.SetReadDeadline(time.Time{})
	done := make(chan error, 1)
	go func() {
		_, err := s.Read(make([]byte, 10))
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	s.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	select {
	case err := <-done:
		if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
			t.Fatalf("expected timeout, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader not woken by new deadline")
	}
}

func TestMuxHalfCloseAndRemoval(t *testing.T) {
	client, server := newMuxPair(t, nil, nil)
	s, _ := client.OpenStream(MuxPriorityNormal)
	rs, _ := server.AcceptStream()

	if _, err := s.Write([]byte("bye")); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != io.ErrClosedPipe {
		t.Fatalf("double close: %v", err)
	}
	if _, err := s.Write([]byte("x")); err != io.ErrClosedPipe {
		t.Fatalf("write after close: %v", err)
	}

	// remote closed but data not drained; peer can still write back
	waitFor(t, "remote FIN", func() bool {
		rs.mu.Lock()
		defer rs.mu.Unlock()
		return rs.remoteClosed
	})
	if _, err := rs.Write([]byte("x")); err != io.ErrClosedPipe {
		t.Fatalf("write after remote close: %v", err)
	}
	if client.NumStreams() != 1 || server.NumStreams() != 1 {
		t.Fatal("stream removed before both sides closed")
	}
	rs.Close()
	time.Sleep(50 * time.Millisecond)
	if server.NumStreams() != 1 {
		t.Fatal("stream removed before buffered data drained")
	}
	buf := make([]byte, 16)
	n, err := rs.Read(buf)
	if err != nil || string(buf[:n]) != "bye" {
		t.Fatalf("read buffered after close: %q %v", buf[:n], err)
	}
	if server.NumStreams() != 0 {
		t.Fatal("server stream not removed after drain")
	}
	waitFor(t, "client removal", func() bool { return client.NumStreams() == 0 })
	if _, err := s.Read(buf); err != io.ErrClosedPipe {
		t.Fatalf("read on closed stream: %v", err)
	}
}

func TestMuxRemoteCloseUnblocksWriter(t *testing.T) {
	cfg := DefaultMuxConfig()
	cfg.SendWindow = 1024
	scfg := cfg
	client, server := newMuxPair(t, &cfg, &scfg)
	s, _ := client.OpenStream(MuxPriorityNormal)
	rs, _ := server.AcceptStream()

	errc := make(chan error, 1)
	go func() {
		_, err := s.Write(make([]byte, 10*1024))
		errc <- err
	}()
	time.Sleep(50 * time.Millisecond)
	rs.Close()
	select {
	case err := <-errc:
		if err != io.ErrClosedPipe {
			t.Fatalf("got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("writer not unblocked by remote close")
	}
}

func TestMuxSessionCloseUnblocks(t *testing.T) {
	cfg := DefaultMuxConfig()
	cfg.SendWindow = 1024
	scfg := cfg
	client, server := newMuxPair(t, &cfg, &scfg)
	s, _ := client.OpenStream(MuxPriorityNormal)
	server.AcceptStream()

	var wg sync.WaitGroup
	check := func(err error) {
		defer wg.Done()
		if err != io.ErrClosedPipe {
			t.Errorf("got %v, want io.ErrClosedPipe", err)
		}
	}
	wg.Add(3)
	go func() { _, err := s.Read(make([]byte, 1)); check(err) }()
	go func() { _, err := s.Write(make([]byte, 10*1024)); check(err) }()
	go func() { _, err := client.AcceptStream(); check(err) }()
	time.Sleep(50 * time.Millisecond)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	if client.Close() != io.ErrClosedPipe {
		t.Fatal("second Close should return io.ErrClosedPipe")
	}
	if _, err := client.OpenStream(MuxPriorityNormal); err != io.ErrClosedPipe {
		t.Fatalf("OpenStream after close: %v", err)
	}
	if client.NumStreams() != 0 {
		t.Fatal("streams remain after session close")
	}
	waitFor(t, "peer session close", func() bool { return server.isClosed() })
}

// stuckConn blocks forever on Write and Close.
type stuckConn struct {
	net.Conn
	block chan struct{}
}

func (c *stuckConn) Write(p []byte) (int, error) { <-c.block; return 0, io.ErrClosedPipe }
func (c *stuckConn) Close() error                { <-c.block; return nil }

func TestMuxSessionCloseDoesNotBlock(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c2.Close()
	sc := &stuckConn{Conn: c1, block: make(chan struct{})}
	defer close(sc.block)
	m, err := NewMuxSession(sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := m.OpenStream(MuxPriorityNormal)
	s.Write([]byte("data"))

	done := make(chan struct{})
	go func() {
		m.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close blocked on the underlying conn")
	}
}

func TestMuxSnmp(t *testing.T) {
	before := DefaultSnmp.Copy()
	client, server := newMuxPair(t, nil, nil)
	s, _ := client.OpenStream(MuxPriorityNormal)
	rs, _ := server.AcceptStream()

	data := make([]byte, 5000)
	s.Write(data)
	io.ReadFull(rs, make([]byte, len(data)))
	s.Close()
	rs.Close()
	waitFor(t, "stream removal", func() bool {
		return client.NumStreams() == 0 && server.NumStreams() == 0
	})

	after := DefaultSnmp.Copy()
	if d := after.MuxStreamsOpened - before.MuxStreamsOpened; d < 2 {
		t.Fatalf("MuxStreamsOpened delta %d", d)
	}
	if d := after.MuxStreamsClosed - before.MuxStreamsClosed; d < 2 {
		t.Fatalf("MuxStreamsClosed delta %d", d)
	}
	if d := after.MuxBytesSent - before.MuxBytesSent; d < uint64(len(data)) {
		t.Fatalf("MuxBytesSent delta %d", d)
	}
	if d := after.MuxBytesReceived - before.MuxBytesReceived; d < uint64(len(data)) {
		t.Fatalf("MuxBytesReceived delta %d", d)
	}
	if after.MuxFramesSent <= before.MuxFramesSent || after.MuxFramesReceived <= before.MuxFramesReceived {
		t.Fatal("frame counters not incremented")
	}

	if h, v := DefaultSnmp.Header(), DefaultSnmp.ToSlice(); len(h) != len(v) || h[len(h)-1] != "MuxBytesReceived" {
		t.Fatalf("header/slice mismatch: %d vs %d", len(h), len(v))
	}
	snap := &Snmp{MuxStreamsOpened: 1, MuxBytesReceived: 2}
	snap.Reset()
	if snap.MuxStreamsOpened != 0 || snap.MuxBytesReceived != 0 {
		t.Fatal("Reset did not clear mux counters")
	}
}

func TestMuxOverKCP(t *testing.T) {
	l, err := ListenWithOptions("127.0.0.1:0", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	serverErr := make(chan error, 1)
	go func() {
		conn, err := l.AcceptKCP()
		if err != nil {
			serverErr <- err
			return
		}
		conn.SetNoDelay(1, 10, 2, 1)
		cfg := DefaultMuxConfig()
		cfg.Side = MuxSideServer
		m, err := NewMuxSession(conn, &cfg)
		if err != nil {
			serverErr <- err
			return
		}
		defer m.Close()
		for i := 0; i < 4; i++ {
			s, err := m.AcceptStream()
			if err != nil {
				serverErr <- err
				return
			}
			go func() {
				io.Copy(s, s)
				s.Close()
			}()
		}
		serverErr <- nil
		<-time.After(10 * time.Second)
	}()

	conn, err := DialWithOptions(l.Addr().String(), nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetNoDelay(1, 10, 2, 1)
	cfg := DefaultMuxConfig()
	m, err := NewMuxSession(conn, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := m.OpenStream(MuxPriorityNormal)
			if err != nil {
				t.Error(err)
				return
			}
			data := make([]byte, 128*1024)
			rand.Read(data)
			go s.Write(data)
			defer s.Close()
			got := make([]byte, len(data))
			s.SetReadDeadline(time.Now().Add(10 * time.Second))
			if _, err := io.ReadFull(s, got); err != nil {
				t.Error(err)
				return
			}
			if !bytes.Equal(got, data) {
				t.Error("echo mismatch over KCP")
			}
		}()
	}
	wg.Wait()
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}
