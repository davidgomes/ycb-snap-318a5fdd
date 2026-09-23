package kcp

import (
	"bytes"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testMuxPair(t *testing.T, cfg MuxConfig) (*MuxSession, *MuxSession) {
	t.Helper()
	c1, c2 := net.Pipe()
	clientCfg := cfg
	clientCfg.Side = MuxSideClient
	serverCfg := cfg
	serverCfg.Side = MuxSideServer
	cs, err := NewMuxSession(c1, &clientCfg)
	if err != nil {
		t.Fatal(err)
	}
	ss, err := NewMuxSession(c2, &serverCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Close()
	})
	return cs, ss
}

func TestMuxStreamEchoAndIDs(t *testing.T) {
	cs, ss := testMuxPair(t, DefaultMuxConfig())
	st, err := cs.OpenStream(MuxPriorityNormal)
	if err != nil {
		t.Fatal(err)
	}
	if st.ID()%2 != 1 {
		t.Fatalf("client id %d", st.ID())
	}
	acc := make(chan *MuxStream, 1)
	go func() {
		s, err := ss.AcceptStream()
		if err != nil {
			t.Error(err)
			acc <- nil
			return
		}
		acc <- s
	}()
	remote := <-acc
	if remote == nil || remote.ID() != st.ID() {
		t.Fatalf("id mismatch local %d remote %v", st.ID(), remote)
	}

	srv, err := ss.OpenStream(MuxPriorityHigh)
	if err != nil {
		t.Fatal(err)
	}
	if srv.ID()%2 != 0 {
		t.Fatalf("server id %d", srv.ID())
	}
	back, err := cs.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	if back.ID() != srv.ID() {
		t.Fatalf("server stream id %d vs %d", back.ID(), srv.ID())
	}

	payload := []byte("hello mux")
	if _, err := st.Write(payload); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(remote, buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, payload) {
		t.Fatalf("got %q", buf)
	}
}

func TestMuxFlowControlDoesNotBlockOtherStream(t *testing.T) {
	cfg := DefaultMuxConfig()
	cfg.MaxFrameSize = 16
	cfg.SendWindow = 32
	cfg.RecvWindow = 32
	cs, ss := testMuxPair(t, cfg)

	a, err := cs.OpenStream(MuxPriorityNormal)
	if err != nil {
		t.Fatal(err)
	}
	b, err := cs.OpenStream(MuxPriorityNormal)
	if err != nil {
		t.Fatal(err)
	}
	ra, err := ss.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	rb, err := ss.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	if ra.ID() != a.ID() || rb.ID() != b.ID() {
		t.Fatalf("accept order %d %d vs %d %d", ra.ID(), rb.ID(), a.ID(), b.ID())
	}

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := a.Write(bytes.Repeat([]byte("A"), 256))
		done <- err
	}()
	<-started
	time.Sleep(30 * time.Millisecond)

	if _, err := b.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2)
	if _, err := io.ReadFull(rb, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ok" {
		t.Fatalf("stream b got %q", buf)
	}

	go func() {
		_, _ = io.Copy(io.Discard, ra)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked stream did not finish after peer read")
	}
}

func TestMuxPriorityPreemptsQueuedData(t *testing.T) {
	left, right := net.Pipe()
	release := make(chan struct{})
	var writes atomic.Int32
	var mu sync.Mutex
	var frames []byte
	wrapped := &gateConn{
		Conn:    left,
		release: release,
		allow:   2, // two SYN frames
		onWrite: func(p []byte) {
			writes.Add(1)
			if len(p) >= muxHeaderLen && p[0] == muxCmdData {
				mu.Lock()
				frames = append(frames, p[1]) // priority byte
				mu.Unlock()
			}
		},
	}
	cfg := DefaultMuxConfig()
	cfg.MaxFrameSize = 4
	cfg.SendWindow = 64
	cfg.Side = MuxSideClient
	cs, err := NewMuxSession(wrapped, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	scfg := cfg
	scfg.Side = MuxSideServer
	ss, err := NewMuxSession(right, &scfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	low, err := cs.OpenStream(MuxPriorityLow)
	if err != nil {
		t.Fatal(err)
	}
	high, err := cs.OpenStream(MuxPriorityHigh)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ss.AcceptStream(); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.AcceptStream(); err != nil {
		t.Fatal(err)
	}

	lowDone := make(chan error, 1)
	go func() {
		_, err := low.Write(bytes.Repeat([]byte("L"), 8))
		lowDone <- err
	}()
	// Wait until the first data frame is stuck inside Write.
	deadline := time.Now().Add(2 * time.Second)
	for writes.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if writes.Load() < 3 {
		t.Fatal("writer did not block on first data frame")
	}
	highDone := make(chan error, 1)
	go func() {
		_, err := high.Write([]byte("HIGH"))
		highDone <- err
	}()
	time.Sleep(30 * time.Millisecond)
	close(release)

	if err := <-lowDone; err != nil {
		t.Fatal(err)
	}
	if err := <-highDone; err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for writes.Load() < 5 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	got := append([]byte(nil), frames...)
	mu.Unlock()
	// First low frame may already be inside Write; the next frame must be high.
	if len(got) < 3 {
		t.Fatalf("frames %v", got)
	}
	if got[1] != MuxPriorityHigh {
		t.Fatalf("priority order %v", got)
	}
}

func TestMuxCloseAndDeadline(t *testing.T) {
	cs, ss := testMuxPair(t, DefaultMuxConfig())
	st, err := cs.OpenStream(MuxPriorityNormal)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := ss.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Write([]byte("xyz")); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Write([]byte("more")); err != io.ErrClosedPipe {
		t.Fatalf("write after close: %v", err)
	}
	buf := make([]byte, 3)
	if _, err := io.ReadFull(remote, buf); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Read(buf); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
	// Remote close unblocks local writers.
	writeDone := make(chan error, 1)
	go func() {
		_, err := remote.Write(bytes.Repeat([]byte("Z"), 1<<20))
		writeDone <- err
	}()
	time.Sleep(20 * time.Millisecond)
	if err := remote.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-writeDone:
		if err != io.ErrClosedPipe {
			t.Fatalf("writer err %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remote close did not unblock writer")
	}

	// Both sides closed and buffer drained => stream leaves the map.
	deadline := time.Now().Add(2 * time.Second)
	for (cs.NumStreams() != 0 || ss.NumStreams() != 0) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if cs.NumStreams() != 0 || ss.NumStreams() != 0 {
		t.Fatalf("streams left client %d server %d", cs.NumStreams(), ss.NumStreams())
	}

	st2, err := cs.OpenStream(MuxPriorityLow)
	if err != nil {
		t.Fatal(err)
	}
	remote2, err := ss.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	if err := remote2.SetReadDeadline(time.Now().Add(40 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, err = remote2.Read(make([]byte, 1))
	ne, ok := err.(net.Error)
	if !ok || !ne.Timeout() {
		t.Fatalf("timeout error: %v", err)
	}
	_ = st2.Close()
}

func TestMuxSessionCloseUnblocksAndReturns(t *testing.T) {
	left, right := net.Pipe()
	hold := make(chan struct{})
	wrapped := &gateConn{Conn: left, release: hold, allow: 0}
	cfg := DefaultMuxConfig()
	cs, err := NewMuxSession(wrapped, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	ss, err := NewMuxSession(right, &MuxConfig{
		Side:         MuxSideServer,
		MaxFrameSize: cfg.MaxFrameSize,
		SendWindow:   cfg.SendWindow,
		RecvWindow:   cfg.RecvWindow,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Queue a frame so writeLoop blocks inside the gated Write.
	opened := make(chan error, 1)
	go func() {
		_, err := cs.OpenStream(MuxPriorityNormal)
		opened <- err
	}()
	time.Sleep(30 * time.Millisecond)

	returned := make(chan struct{})
	go func() {
		_ = cs.Close()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Close blocked behind conn.Write")
	}
	close(hold)

	acceptDone := make(chan error, 1)
	go func() {
		_, err := ss.AcceptStream()
		acceptDone <- err
	}()
	_ = ss.Close()
	select {
	case err := <-acceptDone:
		if err != io.ErrClosedPipe {
			t.Fatalf("accept after close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("session close did not unblock Accept")
	}
}

func TestMuxSnmpCounters(t *testing.T) {
	DefaultSnmp.Reset()
	cs, ss := testMuxPair(t, DefaultMuxConfig())
	st, err := cs.OpenStream(MuxPriorityNormal)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := ss.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("snmp-payload")
	if _, err := st.Write(msg); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(remote, buf); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	_ = remote.Close()

	time.Sleep(50 * time.Millisecond)
	snap := DefaultSnmp.Copy()
	if snap.MuxStreamsOpened < 2 {
		t.Fatalf("opened %d", snap.MuxStreamsOpened)
	}
	if snap.MuxStreamsClosed < 2 {
		t.Fatalf("closed %d", snap.MuxStreamsClosed)
	}
	if snap.MuxFramesSent == 0 || snap.MuxFramesReceived == 0 {
		t.Fatalf("frames %d %d", snap.MuxFramesSent, snap.MuxFramesReceived)
	}
	if snap.MuxBytesSent != uint64(len(msg)) || snap.MuxBytesReceived != uint64(len(msg)) {
		t.Fatalf("bytes sent %d recv %d", snap.MuxBytesSent, snap.MuxBytesReceived)
	}
	hdr := snap.Header()
	row := snap.ToSlice()
	if len(hdr) != len(row) {
		t.Fatalf("header %d slice %d", len(hdr), len(row))
	}
	DefaultSnmp.Reset()
	if DefaultSnmp.Copy().MuxBytesSent != 0 {
		t.Fatal("reset failed")
	}
}

type gateConn struct {
	net.Conn
	release chan struct{}
	allow   int32
	writes  atomic.Int32
	onWrite func([]byte)
}

func (g *gateConn) Write(p []byte) (int, error) {
	n := g.writes.Add(1)
	if g.onWrite != nil {
		g.onWrite(p)
	}
	if n > g.allow {
		<-g.release
	}
	return g.Conn.Write(p)
}
