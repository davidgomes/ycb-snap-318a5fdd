package kcp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

func testMuxCfg(window, frame int) MuxConfig {
	return MuxConfig{
		MaxFrameSize: frame,
		SendWindow:   window,
		RecvWindow:   window,
	}
}

func newMuxPair(t *testing.T, cfg MuxConfig) (*MuxSession, *MuxSession) {
	t.Helper()
	return newMuxPairConn(t, cfg, func(c net.Conn) net.Conn { return c })
}

func newMuxPairConn(t *testing.T, cfg MuxConfig, wrap func(net.Conn) net.Conn) (*MuxSession, *MuxSession) {
	t.Helper()
	c, s := net.Pipe()
	cc := cfg
	cc.Side = MuxSideClient
	sc := cfg
	sc.Side = MuxSideServer
	mc, err := NewMuxSession(wrap(c), &cc)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := NewMuxSession(s, &sc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = mc.Close()
		_ = ms.Close()
	})
	return mc, ms
}

func waitUntil(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met")
}

func TestMuxConfigAndIDs(t *testing.T) {
	def := DefaultMuxConfig()
	if def.Side != MuxSideClient || def.MaxFrameSize <= 0 || def.SendWindow <= 0 || def.RecvWindow <= 0 {
		t.Fatalf("default config: %+v", def)
	}
	if MuxPriorityHigh <= MuxPriorityNormal || MuxPriorityNormal <= MuxPriorityLow {
		t.Fatal("priority order")
	}
	if _, err := NewMuxSession(nil, &def); err == nil {
		t.Fatal("expected error for nil conn")
	}
	bad := def
	bad.Side = 9
	c, s := net.Pipe()
	defer c.Close()
	defer s.Close()
	if _, err := NewMuxSession(c, &bad); err == nil {
		t.Fatal("expected error for invalid side")
	}

	mc, ms := newMuxPair(t, testMuxCfg(64*1024, 1024))
	if mc.NumStreams() != 0 || ms.NumStreams() != 0 {
		t.Fatal("expected empty sessions")
	}

	c1, err := mc.OpenStream(MuxPriorityNormal)
	if err != nil {
		t.Fatal(err)
	}
	if c1.ID() != 1 {
		t.Fatalf("client id %d", c1.ID())
	}
	if mc.NumStreams() != 1 {
		t.Fatalf("client streams %d", mc.NumStreams())
	}
	waitUntil(t, time.Second, func() bool { return ms.NumStreams() == 1 })

	s1, err := ms.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	if s1.ID() != c1.ID() {
		t.Fatalf("id mismatch %d %d", c1.ID(), s1.ID())
	}

	c2, err := mc.OpenStream(MuxPriorityHigh)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := ms.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	if c2.ID() != 3 || s2.ID() != 3 {
		t.Fatalf("second client ids %d %d", c2.ID(), s2.ID())
	}

	ss, err := ms.OpenStream(MuxPriorityLow)
	if err != nil {
		t.Fatal(err)
	}
	cc, err := mc.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	if ss.ID() != 2 || cc.ID() != ss.ID() {
		t.Fatalf("server ids %d %d", ss.ID(), cc.ID())
	}
	ss2, err := ms.OpenStream(MuxPriorityNormal)
	if err != nil {
		t.Fatal(err)
	}
	cc2, err := mc.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	if ss2.ID() != 4 || cc2.ID() != 4 {
		t.Fatalf("second server ids %d %d", ss2.ID(), cc2.ID())
	}
}

func TestMuxEchoAndIsolation(t *testing.T) {
	mc, ms := newMuxPair(t, testMuxCfg(32, 8))
	c1, s1 := openPair(t, mc, ms, MuxPriorityLow)
	c2, s2 := openPair(t, mc, ms, MuxPriorityHigh)

	msg := bytes.Repeat([]byte("ab"), 16) // 32 bytes, the whole window
	n, err := c1.Write(msg)
	if err != nil || n != len(msg) {
		t.Fatalf("write %d %v", n, err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(s1, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("echo %q", got)
	}

	// Fill the window again and block the next write. The other stream must move.
	if _, err := c1.Write(msg); err != nil {
		t.Fatal(err)
	}
	blocked := make(chan struct{})
	writeDone := make(chan error, 1)
	extra := bytes.Repeat([]byte("Z"), 16)
	go func() {
		close(blocked)
		_, err := c1.Write(extra)
		writeDone <- err
	}()
	<-blocked
	select {
	case err := <-writeDone:
		t.Fatalf("low stream write did not block: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	hi := []byte("high-priority")
	done := make(chan error, 1)
	go func() {
		_, err := c2.Write(hi)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second stream stalled behind a window-blocked stream")
	}
	got = make([]byte, len(hi))
	if _, err := io.ReadFull(s2, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, hi) {
		t.Fatalf("high stream %q", got)
	}

	// Draining the blocked stream returns its credit and releases the writer.
	rest := make([]byte, len(msg)+len(extra))
	go func() {
		_, _ = io.ReadFull(s1, rest)
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("writer stayed blocked after the peer read")
	}
}

func openPair(t *testing.T, mc, ms *MuxSession, pri uint8) (*MuxStream, *MuxStream) {
	t.Helper()
	c, err := mc.OpenStream(pri)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ms.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	return c, s
}

func TestMuxHalfCloseAndRemoval(t *testing.T) {
	mc, ms := newMuxPair(t, testMuxCfg(64*1024, 1024))
	c, s := openPair(t, mc, ms, MuxPriorityNormal)
	payload := []byte("hello-mux")
	if _, err := c.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("second close %v", err)
	}
	if _, err := c.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write after close %v", err)
	}
	if mc.NumStreams() != 1 || ms.NumStreams() != 1 {
		t.Fatalf("removed before both sides closed and drained: client=%d server=%d", mc.NumStreams(), ms.NumStreams())
	}

	buf := make([]byte, len(payload))
	n, err := s.Read(buf)
	if err != nil || n != len(payload) || !bytes.Equal(buf, payload) {
		t.Fatalf("read after remote close n=%d err=%v %q", n, err, buf[:n])
	}
	// FIN may still be in flight; a further read blocks until it arrives,
	// then reports EOF. Local close has not happened, so the stream remains.
	n, err = s.Read(buf)
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, n=%d err=%v", n, err)
	}
	if ms.NumStreams() != 1 {
		t.Fatalf("removed before local close: %d", ms.NumStreams())
	}
	if _, err := s.Write([]byte{1}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write after remote close %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if ms.NumStreams() != 0 {
		t.Fatalf("server streams %d", ms.NumStreams())
	}
	waitUntil(t, time.Second, func() bool { return mc.NumStreams() == 0 })
}

func TestMuxRemoteCloseUnblocksWriter(t *testing.T) {
	mc, ms := newMuxPair(t, testMuxCfg(16, 16))
	c, s := openPair(t, mc, ms, MuxPriorityNormal)
	if _, err := c.Write(bytes.Repeat([]byte("a"), 16)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := c.Write(bytes.Repeat([]byte("b"), 16))
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("expected block, got %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("remote close err %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remote close did not unblock writer")
	}
}

func TestMuxLocalCloseUnblocksWriter(t *testing.T) {
	mc, ms := newMuxPair(t, testMuxCfg(16, 16))
	c, _ := openPair(t, mc, ms, MuxPriorityNormal)
	if _, err := c.Write(bytes.Repeat([]byte("a"), 16)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := c.Write(bytes.Repeat([]byte("b"), 16))
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("expected block, got %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("local close err %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("local close did not unblock writer")
	}
}

func TestMuxSessionCloseUnblocks(t *testing.T) {
	mc, ms := newMuxPair(t, testMuxCfg(32, 16))
	c, _ := openPair(t, mc, ms, MuxPriorityNormal)

	readDone := make(chan error, 1)
	go func() {
		_, err := c.Read(make([]byte, 4))
		readDone <- err
	}()
	acceptDone := make(chan error, 1)
	go func() {
		_, err := mc.AcceptStream()
		acceptDone <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if err := mc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := mc.Close(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("second session close %v", err)
	}
	select {
	case err := <-readDone:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("read err %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("session close did not unblock read")
	}
	select {
	case err := <-acceptDone:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("accept err %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("session close did not unblock accept")
	}
	if _, err := mc.OpenStream(MuxPriorityNormal); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("open after close %v", err)
	}
	if err := c.SetReadDeadline(time.Now()); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("deadline after close %v", err)
	}
}

func TestMuxCloseDoesNotWaitForBlockedWrite(t *testing.T) {
	rawC, rawS := net.Pipe()
	defer rawS.Close()
	bw := &blockWriteConn{Conn: rawC, block: make(chan struct{}), started: make(chan struct{})}
	cfg := DefaultMuxConfig()
	cfg.Side = MuxSideClient
	sess, err := NewMuxSession(bw, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.OpenStream(MuxPriorityNormal); err != nil {
		t.Fatal(err)
	}
	select {
	case <-bw.started:
	case <-time.After(time.Second):
		t.Fatal("write was not attempted")
	}
	start := time.Now()
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("Close blocked for %s", time.Since(start))
	}
	close(bw.block)
}

type blockWriteConn struct {
	net.Conn
	block   chan struct{}
	started chan struct{}
	once    sync.Once
}

func (b *blockWriteConn) Write(p []byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	<-b.block
	return b.Conn.Write(p)
}

func TestMuxReadDeadline(t *testing.T) {
	mc, ms := newMuxPair(t, testMuxCfg(4096, 1024))
	c, s := openPair(t, mc, ms, MuxPriorityNormal)

	if err := c.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err := c.Read(make([]byte, 4))
	ne, ok := err.(net.Error)
	if !ok || !ne.Timeout() || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("deadline err %#v", err)
	}

	if err := c.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(30 * time.Millisecond)
		_, _ = s.Write([]byte("late"))
	}()
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "late" {
		t.Fatalf("got %q", buf)
	}

	started := time.Now()
	if err := c.SetReadDeadline(time.Now().Add(40 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, err = c.Read(buf)
	if ne, ok = err.(net.Error); !ok || !ne.Timeout() {
		t.Fatalf("pending deadline %#v", err)
	}
	if time.Since(started) < 20*time.Millisecond {
		t.Fatal("deadline returned too quickly")
	}
}

func TestMuxPriorityAndControl(t *testing.T) {
	c, s := net.Pipe()
	gc := newGateConn(c)
	cfg := testMuxCfg(64*1024, 4)
	cc := cfg
	cc.Side = MuxSideClient
	sc := cfg
	sc.Side = MuxSideServer
	mc, err := NewMuxSession(gc, &cc)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := NewMuxSession(s, &sc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		gc.release()
		_ = mc.Close()
		_ = ms.Close()
	})

	lowC, lowS := openPair(t, mc, ms, MuxPriorityLow)
	highC, highS := openPair(t, mc, ms, MuxPriorityHigh)
	// Complete the handshake and refill windows before arming the gate.
	if _, err := lowC.Write([]byte("wxyz")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(lowS, make([]byte, 4)); err != nil {
		t.Fatal(err)
	}
	if _, err := highC.Write([]byte("wxyz")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(highS, make([]byte, 4)); err != nil {
		t.Fatal(err)
	}

	gc.arm()
	lowPayload := bytes.Repeat([]byte("L"), 40) // 10 frames
	highPayload := bytes.Repeat([]byte("H"), 8) // 2 frames
	go func() { _, _ = lowC.Write(lowPayload) }()
	// 10 low frames, one of which may already be inside the blocked Write.
	var last, stable int
	waitUntil(t, time.Second, func() bool {
		n := mc.pendingDataCount()
		if n == last && n >= 9 {
			stable++
		} else {
			stable = 0
			last = n
		}
		return stable >= 5
	})
	before := mc.pendingDataCount()
	go func() { _, _ = highC.Write(highPayload) }()
	waitUntil(t, time.Second, func() bool { return mc.pendingDataCount() >= before+2 })

	gc.release()
	waitUntil(t, 2*time.Second, func() bool {
		_, ids := gc.snapshot()
		n := 0
		for _, id := range ids {
			if id == highC.ID() {
				n++
			}
		}
		return n >= 2
	})
	_, ids := gc.snapshot()
	lowBefore := 0
	highSeen := 0
	for _, id := range ids {
		switch id {
		case highC.ID():
			if lowBefore > 1 {
				t.Fatalf("low frames before high: %d in %v", lowBefore, ids)
			}
			highSeen++
		case lowC.ID():
			if highSeen == 0 {
				lowBefore++
			}
		}
	}
	if highSeen < 2 {
		t.Fatalf("high frames %d ids %v", highSeen, ids)
	}
	if lowBefore > 1 {
		t.Fatalf("priority inversion, lowBefore=%d ids=%v", lowBefore, ids)
	}

	// Drain so the sessions can close cleanly.
	_, _ = io.ReadFull(lowS, make([]byte, 4))
	_, _ = io.ReadFull(highS, make([]byte, 4))
}

func TestMuxControlFramePreemptsData(t *testing.T) {
	c, s := net.Pipe()
	gc := newGateConn(c)
	cfg := testMuxCfg(64*1024, 4)
	cc := cfg
	cc.Side = MuxSideClient
	sc := cfg
	sc.Side = MuxSideServer
	mc, err := NewMuxSession(gc, &cc)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := NewMuxSession(s, &sc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		gc.release()
		_ = mc.Close()
		_ = ms.Close()
	})
	st, rs := openPair(t, mc, ms, MuxPriorityNormal)
	if _, err := st.Write([]byte("abcd")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(rs, make([]byte, 4)); err != nil {
		t.Fatal(err)
	}

	gc.arm()
	go func() { _, _ = st.Write(bytes.Repeat([]byte("D"), 32)) }()
	waitUntil(t, time.Second, func() bool { return mc.pendingDataCount() >= 4 })
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, time.Second, func() bool { return mc.pendingCtrlCount() >= 1 })
	gc.release()

	waitUntil(t, 2*time.Second, func() bool {
		cmds, _ := gc.snapshot()
		return bytes.Contains(cmds, []byte{muxCmdFIN})
	})
	cmds, _ := gc.snapshot()
	finAt := bytes.IndexByte(cmds, muxCmdFIN)
	if finAt < 0 {
		t.Fatalf("missing FIN in %v", cmds)
	}
	// The frame already inside Write may be data. The next frame must be FIN,
	// ahead of any data still queued.
	if finAt > 1 {
		t.Fatalf("FIN at %d in %v", finAt, cmds)
	}
	if finAt == 1 && cmds[0] != muxCmdPSH {
		t.Fatalf("expected in-flight data then FIN, got %v", cmds)
	}
}

func TestMuxSNMP(t *testing.T) {
	DefaultSnmp.Reset()
	mc, ms := newMuxPair(t, testMuxCfg(64*1024, 1024))
	c, s := openPair(t, mc, ms, MuxPriorityNormal)
	payload := []byte("payload!")
	if _, err := c.Write(payload); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(s, buf); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // let the window update hit the wire
	snap := DefaultSnmp.Copy()
	if snap.MuxBytesSent != uint64(len(payload)) || snap.MuxBytesReceived != uint64(len(payload)) {
		t.Fatalf("bytes sent=%d recv=%d", snap.MuxBytesSent, snap.MuxBytesReceived)
	}
	if snap.MuxStreamsOpened < 2 {
		t.Fatalf("opened %d", snap.MuxStreamsOpened)
	}
	if snap.MuxFramesSent < 3 || snap.MuxFramesReceived < 3 {
		t.Fatalf("frames sent=%d recv=%d", snap.MuxFramesSent, snap.MuxFramesReceived)
	}

	hdr := DefaultSnmp.Header()
	slice := DefaultSnmp.ToSlice()
	if len(hdr) != len(slice) {
		t.Fatalf("header %d slice %d", len(hdr), len(slice))
	}
	want := []string{"MuxStreamsOpened", "MuxStreamsClosed", "MuxFramesSent", "MuxFramesReceived", "MuxBytesSent", "MuxBytesReceived"}
	for _, name := range want {
		found := false
		for _, h := range hdr {
			if h == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing %s in %v", name, hdr)
		}
	}
	copied := snap.Copy()
	if copied.MuxBytesSent != snap.MuxBytesSent || copied.MuxFramesReceived != snap.MuxFramesReceived {
		t.Fatal("copy mismatch")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, time.Second, func() bool {
		return mc.NumStreams() == 0 && ms.NumStreams() == 0
	})
	closedSnap := DefaultSnmp.Copy()
	if closedSnap.MuxStreamsClosed != closedSnap.MuxStreamsOpened {
		t.Fatalf("opened %d closed %d", closedSnap.MuxStreamsOpened, closedSnap.MuxStreamsClosed)
	}
	DefaultSnmp.Reset()
	cleared := DefaultSnmp.Copy()
	if cleared.MuxBytesSent != 0 || cleared.MuxStreamsOpened != 0 || cleared.MuxFramesSent != 0 || cleared.MuxBytesReceived != 0 || cleared.MuxStreamsClosed != 0 || cleared.MuxFramesReceived != 0 {
		t.Fatalf("reset %+v", cleared)
	}

	// Keep the header index aligned with ToSlice for the new fields.
	idx := map[string]int{}
	for i, h := range hdr {
		idx[h] = i
	}
	if idx["MuxBytesSent"] != idx["MuxBytesReceived"]-1 {
		t.Fatal("unexpected header order")
	}
}

type gateConn struct {
	net.Conn
	mu    sync.Mutex
	armed bool
	wait  chan struct{}
	cmds  []byte
	ids   []uint32
}

func newGateConn(c net.Conn) *gateConn {
	return &gateConn{Conn: c}
}

func (g *gateConn) arm() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.armed = true
	g.wait = make(chan struct{})
	g.cmds = nil
	g.ids = nil
}

func (g *gateConn) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.armed = false
	if g.wait != nil {
		select {
		case <-g.wait:
		default:
			close(g.wait)
		}
		g.wait = nil
	}
}

func (g *gateConn) Write(p []byte) (int, error) {
	g.mu.Lock()
	var ch chan struct{}
	if g.armed {
		ch = g.wait
	}
	g.mu.Unlock()
	if ch != nil {
		<-ch
	}
	if len(p) >= muxHeaderSize {
		g.mu.Lock()
		g.cmds = append(g.cmds, p[1])
		g.ids = append(g.ids, binary.BigEndian.Uint32(p[4:8]))
		g.mu.Unlock()
	}
	return g.Conn.Write(p)
}

func (g *gateConn) snapshot() ([]byte, []uint32) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]byte(nil), g.cmds...), append([]uint32(nil), g.ids...)
}
