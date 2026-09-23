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
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func muxConfig(side MuxSide, frame, window int) MuxConfig {
	cfg := DefaultMuxConfig()
	cfg.Side = side
	if frame > 0 {
		cfg.MaxFrameSize = frame
	}
	if window > 0 {
		cfg.SendWindow = window
		cfg.RecvWindow = window
	}
	return cfg
}

func newTestMux(t *testing.T, frame, window int) (client, server *MuxSession, cleanup func()) {
	t.Helper()
	cconn, sconn := net.Pipe()
	ccfg := muxConfig(MuxSideClient, frame, window)
	scfg := muxConfig(MuxSideServer, frame, window)
	client, err := NewMuxSession(cconn, &ccfg)
	if err != nil {
		t.Fatalf("client session: %v", err)
	}
	server, err = NewMuxSession(sconn, &scfg)
	if err != nil {
		t.Fatalf("server session: %v", err)
	}
	cleanup = func() {
		client.Close()
		server.Close()
	}
	return client, server, cleanup
}

func openPair(t *testing.T, client, server *MuxSession, pri uint8) (cs, ss *MuxStream) {
	t.Helper()
	type result struct {
		st  *MuxStream
		err error
	}
	ch := make(chan result, 1)
	go func() {
		st, err := server.AcceptStream()
		ch <- result{st, err}
	}()
	cs, err := client.OpenStream(pri)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("accept: %v", r.err)
		}
		ss = r.st
	case <-time.After(3 * time.Second):
		t.Fatal("accept timed out")
	}
	if cs.ID() != ss.ID() {
		t.Fatalf("id mismatch client %d server %d", cs.ID(), ss.ID())
	}
	return cs, ss
}

func readFull(t *testing.T, st *MuxStream, n int) []byte {
	t.Helper()
	_ = st.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, n)
	if _, err := io.ReadFull(st, buf); err != nil {
		t.Fatalf("read %d: %v", n, err)
	}
	_ = st.SetReadDeadline(time.Time{})
	return buf
}

func waitUntil(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		t.Fatal("condition not met")
	}
}

func TestMuxConfigValidation(t *testing.T) {
	c, _ := net.Pipe()
	if _, err := NewMuxSession(nil, &MuxConfig{}); err == nil {
		t.Fatal("expected nil conn error")
	}
	if _, err := NewMuxSession(c, nil); err == nil {
		t.Fatal("expected nil config error")
	}
	cfg := DefaultMuxConfig()
	cfg.Side = 0
	if _, err := NewMuxSession(c, &cfg); err == nil {
		t.Fatal("expected invalid side")
	}
	cfg = DefaultMuxConfig()
	cfg.MaxFrameSize = 0
	if _, err := NewMuxSession(c, &cfg); err == nil {
		t.Fatal("expected invalid frame")
	}
	cfg = DefaultMuxConfig()
	cfg.SendWindow = 0
	if _, err := NewMuxSession(c, &cfg); err == nil {
		t.Fatal("expected invalid window")
	}
	_ = c.Close()

	def := DefaultMuxConfig()
	if def.Side != MuxSideClient || def.MaxFrameSize <= 0 || def.SendWindow <= 0 || def.RecvWindow <= 0 {
		t.Fatalf("bad default config: %+v", def)
	}
	if !(MuxPriorityHigh > MuxPriorityNormal && MuxPriorityNormal > MuxPriorityLow) {
		t.Fatal("priority constants are not ordered")
	}
}

func TestMuxStreamIDs(t *testing.T) {
	client, server, cleanup := newTestMux(t, 0, 0)
	defer cleanup()

	c1, s1 := openPair(t, client, server, MuxPriorityNormal)
	c2, s2 := openPair(t, client, server, MuxPriorityHigh)
	if c1.ID()%2 != 1 || c2.ID()%2 != 1 {
		t.Fatalf("client ids = %d %d", c1.ID(), c2.ID())
	}
	if c2.ID() != c1.ID()+2 {
		t.Fatalf("client ids %d %d", c1.ID(), c2.ID())
	}
	if s1.ID() != c1.ID() || s2.ID() != c2.ID() {
		t.Fatal("peer ids diverged")
	}
	if client.NumStreams() != 2 || server.NumStreams() != 2 {
		t.Fatalf("num streams client %d server %d", client.NumStreams(), server.NumStreams())
	}

	type result struct {
		st  *MuxStream
		err error
	}
	ch := make(chan result, 1)
	go func() {
		st, err := client.AcceptStream()
		ch <- result{st, err}
	}()
	ss, err := server.OpenStream(MuxPriorityLow)
	if err != nil {
		t.Fatal(err)
	}
	var cs *MuxStream
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatal(r.err)
		}
		cs = r.st
	case <-time.After(3 * time.Second):
		t.Fatal("client accept timed out")
	}
	if ss.ID()%2 != 0 || ss.ID() != cs.ID() {
		t.Fatalf("server id %d client id %d", ss.ID(), cs.ID())
	}
}

func TestMuxEchoAndOrder(t *testing.T) {
	client, server, cleanup := newTestMux(t, 64, 1024)
	defer cleanup()
	cs, ss := openPair(t, client, server, MuxPriorityNormal)

	payload := bytes.Repeat([]byte("abc"), 200)
	errCh := make(chan error, 1)
	go func() {
		buf := readFull(t, ss, len(payload))
		if !bytes.Equal(buf, payload) {
			errCh <- errors.New("payload mismatch")
			return
		}
		_, err := ss.Write(buf)
		errCh <- err
	}()
	n, err := cs.Write(payload)
	if err != nil || n != len(payload) {
		t.Fatalf("write n=%d err=%v", n, err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("echo stalled")
	}
	got := readFull(t, cs, len(payload))
	if !bytes.Equal(got, payload) {
		t.Fatal("echo mismatch")
	}
}

func TestMuxBidirectionalFlowControl(t *testing.T) {
	const (
		frame   = 1024
		window  = 8 * 1024
		payload = 100 * 1024
	)
	client, server, cleanup := newTestMux(t, frame, window)
	defer cleanup()
	cs, ss := openPair(t, client, server, MuxPriorityNormal)

	a := bytes.Repeat([]byte("A"), payload)
	b := bytes.Repeat([]byte("B"), payload)
	errCh := make(chan error, 2)
	go func() {
		_, err := cs.Write(a)
		errCh <- err
	}()
	go func() {
		_, err := ss.Write(b)
		errCh <- err
	}()
	gotB := readFull(t, cs, len(b))
	gotA := readFull(t, ss, len(a))
	for i := 0; i < 2; i++ {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("bidirectional write stalled")
		}
	}
	if !bytes.Equal(gotA, a) || !bytes.Equal(gotB, b) {
		t.Fatal("bidirectional payload mismatch")
	}
}

func TestMuxBlockedStreamDoesNotStallOthers(t *testing.T) {
	const window = 2000
	client, server, cleanup := newTestMux(t, 500, window)
	defer cleanup()
	csA, _ := openPair(t, client, server, MuxPriorityNormal)
	csB, ssB := openPair(t, client, server, MuxPriorityNormal)

	huge := bytes.Repeat([]byte("H"), 100000)
	done := make(chan struct{})
	go func() {
		_, _ = csA.Write(huge)
		close(done)
	}()

	start := atomic.LoadUint64(&DefaultSnmp.MuxBytesSent)
	waitUntil(t, 3*time.Second, func() bool {
		sent := atomic.LoadUint64(&DefaultSnmp.MuxBytesSent) - start
		select {
		case <-done:
			t.Fatal("blocked stream write returned early")
		default:
		}
		return sent >= uint64(window)
	})
	time.Sleep(30 * time.Millisecond)
	sent := atomic.LoadUint64(&DefaultSnmp.MuxBytesSent) - start
	if sent > uint64(window) {
		t.Fatalf("send window exceeded: sent %d window %d", sent, window)
	}
	select {
	case <-done:
		t.Fatal("stream A was not blocked by its send window")
	default:
	}

	msg := []byte("other-stream-still-runs")
	writeDone := make(chan error, 1)
	go func() {
		_, err := csB.Write(msg)
		writeDone <- err
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a window-blocked stream stalled another stream")
	}
	got := readFull(t, ssB, len(msg))
	if !bytes.Equal(got, msg) {
		t.Fatalf("got %q", got)
	}
}

func TestMuxHalfCloseAndRemoteWriteAbort(t *testing.T) {
	client, server, cleanup := newTestMux(t, 128, 64*1024)
	defer cleanup()
	cs, ss := openPair(t, client, server, MuxPriorityNormal)

	payload := bytes.Repeat([]byte("xyz"), 300)
	if _, err := cs.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := cs.Close(); err != nil {
		t.Fatal(err)
	}
	got := readFull(t, ss, len(payload))
	if !bytes.Equal(got, payload) {
		t.Fatal("buffered data lost across half-close")
	}
	_ = ss.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := ss.Read(make([]byte, 8))
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("after drain n=%d err=%v", n, err)
	}

	// Remote close unblocks and fails local writers.
	if _, err := ss.Write([]byte("nope")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write after remote close: %v", err)
	}
	// Local close already happened; further writes fail too.
	if _, err := cs.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write after local close: %v", err)
	}
	if err := cs.Close(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("double close: %v", err)
	}

	// Inbound data remains readable after the local half-close.
	reply := []byte("still-readable")
	if _, err := ss.Write(reply); err == nil {
		t.Fatal("server write should fail after client close")
	}
	// Queue the reply before the client FIN is observed.
	cs2, ss2 := openPair(t, client, server, MuxPriorityLow)
	if _, err := ss2.Write(reply); err != nil {
		t.Fatal(err)
	}
	if err := cs2.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readFull(t, cs2, len(reply)), reply) {
		t.Fatal("local half-close discarded inbound data")
	}
}

func TestMuxCloseUnblocksWriters(t *testing.T) {
	const window = 1024
	client, server, cleanup := newTestMux(t, 256, window)
	defer cleanup()
	cs, _ := openPair(t, client, server, MuxPriorityNormal)

	errCh := make(chan error, 1)
	go func() {
		_, err := cs.Write(bytes.Repeat([]byte("W"), 200000))
		errCh <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if err := cs.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("blocked writer after Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not unblock the writer")
	}
}

func TestMuxRemoteCloseUnblocksWriter(t *testing.T) {
	const window = 1024
	client, server, cleanup := newTestMux(t, 256, window)
	defer cleanup()
	cs, ss := openPair(t, client, server, MuxPriorityNormal)

	errCh := make(chan error, 1)
	go func() {
		_, err := cs.Write(bytes.Repeat([]byte("R"), 200000))
		errCh <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if err := ss.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("writer after remote close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remote close did not unblock the writer")
	}
}

func TestMuxSessionCloseUnblocks(t *testing.T) {
	client, server, cleanup := newTestMux(t, 0, 0)
	defer cleanup()
	cs, _ := openPair(t, client, server, MuxPriorityNormal)

	readErr := make(chan error, 1)
	go func() {
		_, err := cs.Read(make([]byte, 4))
		readErr <- err
	}()
	acceptErr := make(chan error, 1)
	go func() {
		_, err := client.AcceptStream()
		acceptErr <- err
	}()
	time.Sleep(30 * time.Millisecond)
	if client.NumStreams() != 1 {
		t.Fatalf("streams before close: %d", client.NumStreams())
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if client.NumStreams() != 0 {
		t.Fatalf("streams after close: %d", client.NumStreams())
	}
	select {
	case err := <-readErr:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("read: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session close did not unblock read")
	}
	select {
	case err := <-acceptErr:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("accept: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session close did not unblock accept")
	}
	if _, err := client.OpenStream(MuxPriorityNormal); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("open after close: %v", err)
	}
	if err := client.Close(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("double session close: %v", err)
	}
}

func TestMuxCloseDoesNotWaitForBlockedWrite(t *testing.T) {
	left, right := net.Pipe()
	gate := &gateConn{Conn: left}
	ccfg := muxConfig(MuxSideClient, 256, 64*1024)
	scfg := muxConfig(MuxSideServer, 256, 64*1024)
	client, err := NewMuxSession(gate, &ccfg)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewMuxSession(right, &scfg)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	cs, _ := openPair(t, client, server, MuxPriorityNormal)

	gate.arm()
	go func() {
		_, _ = cs.Write(bytes.Repeat([]byte("B"), 4096))
	}()
	select {
	case <-gate.hit:
	case <-time.After(2 * time.Second):
		t.Fatal("write never reached the gated connection")
	}

	closed := make(chan struct{})
	go func() {
		_ = client.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Close blocked while the underlying Write was blocked")
	}
	close(gate.resume)
}

type gateConn struct {
	net.Conn
	mu       sync.Mutex
	armed    bool
	signaled bool
	hit      chan struct{}
	resume   chan struct{}
}

func (g *gateConn) arm() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.armed = true
	g.signaled = false
	g.hit = make(chan struct{})
	g.resume = make(chan struct{})
}

func (g *gateConn) Write(p []byte) (int, error) {
	g.mu.Lock()
	armed := g.armed
	hit := g.hit
	resume := g.resume
	signal := false
	if armed && !g.signaled {
		g.signaled = true
		signal = true
	}
	g.mu.Unlock()
	if armed {
		if signal {
			close(hit)
		}
		<-resume
		g.mu.Lock()
		g.armed = false
		g.mu.Unlock()
	}
	return g.Conn.Write(p)
}

func TestMuxReadDeadline(t *testing.T) {
	client, server, cleanup := newTestMux(t, 0, 0)
	defer cleanup()
	cs, ss := openPair(t, client, server, MuxPriorityNormal)

	if err := cs.SetReadDeadline(time.Now().Add(40 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := cs.Read(make([]byte, 4))
	if time.Since(start) > time.Second {
		t.Fatal("deadline did not fire promptly")
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("timeout error = %v", err)
	}

	if err := cs.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = cs.Read(make([]byte, 4))
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("past deadline = %v", err)
	}

	if err := cs.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	got := readFull(t, cs, 2)
	if string(got) != "ok" {
		t.Fatalf("got %q", got)
	}

	// Data already buffered still loses to an expired deadline, matching net.Conn.
	if _, err := ss.Write([]byte("later")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := cs.SetReadDeadline(time.Now().Add(-time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, err = cs.Read(make([]byte, 5))
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("buffered read past deadline = %v", err)
	}
	if err := cs.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if string(readFull(t, cs, 5)) != "later" {
		t.Fatal("data discarded after timeout")
	}
}

func TestMuxStreamRemoval(t *testing.T) {
	client, server, cleanup := newTestMux(t, 64, 32*1024)
	defer cleanup()
	cs, ss := openPair(t, client, server, MuxPriorityNormal)
	payload := []byte("keep-until-drained-and-both-closed")
	if _, err := cs.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := cs.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ss.Close(); err != nil {
		t.Fatal(err)
	}
	// A partial read leaves buffered inbound bytes, so the stream stays.
	first := readFull(t, ss, 1)
	if server.NumStreams() != 1 {
		t.Fatalf("removed before drain: %d", server.NumStreams())
	}
	rest := readFull(t, ss, len(payload)-1)
	got := append(first, rest...)
	if !bytes.Equal(got, payload) {
		t.Fatal(string(got))
	}
	_ = ss.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := ss.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		return server.NumStreams() == 0 && client.NumStreams() == 0
	})
}

func TestMuxSchedulePriorityAndControl(t *testing.T) {
	client, server, cleanup := newTestMux(t, 100, 100000)
	defer cleanup()
	lowC, _ := openPair(t, client, server, MuxPriorityLow)
	highC, _ := openPair(t, client, server, MuxPriorityHigh)

	// Handshake is done and the writer is idle. Inject queued frames and
	// observe the scheduler without the write loop racing us.
	client.mu.Lock()
	if len(client.ctrl) != 0 {
		client.mu.Unlock()
		t.Fatal("unexpected control frames")
	}
	push := func(st *MuxStream, payload []byte) {
		st.chunks = append(st.chunks, &writeChunk{
			st:   st,
			data: append([]byte(nil), payload...),
			done: make(chan error, 1),
		})
	}
	push(lowC, bytes.Repeat([]byte("L"), 100))
	push(lowC, bytes.Repeat([]byte("L"), 100))
	push(highC, bytes.Repeat([]byte("H"), 100))
	client.enqueueCtrlLocked(encodeFrame(cmdSYN, 99, []byte{MuxPriorityHigh, 0, 0, 0, 1}))

	var got []byte
	for {
		frame, _, ok := client.popOutgoingLocked()
		if !ok {
			break
		}
		got = append(got, frame[1]) // cmd
		got = append(got, frame[4:8]...)
	}
	client.mu.Unlock()

	// control, then high stream data, then low stream data.
	wantCmds := []byte{cmdSYN, cmdPSH, cmdPSH, cmdPSH}
	if len(got) != len(wantCmds)*5 {
		t.Fatalf("frame trace len %d", len(got))
	}
	for i, cmd := range wantCmds {
		if got[i*5] != cmd {
			t.Fatalf("cmd[%d]=%d want %d", i, got[i*5], cmd)
		}
	}
	highID := highC.ID()
	lowID := lowC.ID()
	sid := func(i int) uint32 { return binary.BigEndian.Uint32(got[i*5+1 : i*5+5]) }
	if sid(0) != 99 || sid(1) != highID || sid(2) != lowID || sid(3) != lowID {
		t.Fatalf("order high=%d low=%d got %d %d %d %d", highID, lowID, sid(0), sid(1), sid(2), sid(3))
	}
}

func TestMuxSnmp(t *testing.T) {
	s := newSnmp()
	atomic.StoreUint64(&s.MuxStreamsOpened, 3)
	atomic.StoreUint64(&s.MuxBytesSent, 9)
	cp := s.Copy()
	if cp.MuxStreamsOpened != 3 || cp.MuxBytesSent != 9 || cp.MuxFramesReceived != 0 {
		t.Fatalf("copy = %+v", cp)
	}
	header := s.Header()
	slice := s.ToSlice()
	if len(header) != len(slice) {
		t.Fatalf("header %d slice %d", len(header), len(slice))
	}
	want := []string{
		"MuxStreamsOpened",
		"MuxStreamsClosed",
		"MuxFramesSent",
		"MuxFramesReceived",
		"MuxBytesSent",
		"MuxBytesReceived",
	}
	for i, name := range want {
		if header[len(header)-len(want)+i] != name {
			t.Fatalf("header[%d]=%s", len(header)-len(want)+i, header[len(header)-len(want)+i])
		}
	}
	s.Reset()
	if atomic.LoadUint64(&s.MuxStreamsOpened) != 0 || atomic.LoadUint64(&s.MuxBytesSent) != 0 ||
		atomic.LoadUint64(&s.MuxFramesSent) != 0 || atomic.LoadUint64(&s.MuxBytesReceived) != 0 {
		t.Fatal("reset left mux counters")
	}

	opened := atomic.LoadUint64(&DefaultSnmp.MuxStreamsOpened)
	closed := atomic.LoadUint64(&DefaultSnmp.MuxStreamsClosed)
	sent := atomic.LoadUint64(&DefaultSnmp.MuxBytesSent)
	recv := atomic.LoadUint64(&DefaultSnmp.MuxBytesReceived)
	frames := atomic.LoadUint64(&DefaultSnmp.MuxFramesSent)

	client, server, cleanup := newTestMux(t, 1000, 64*1024)
	defer cleanup()
	cs, ss := openPair(t, client, server, MuxPriorityNormal)
	payload := bytes.Repeat([]byte("m"), 1000)
	if _, err := cs.Write(payload); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readFull(t, ss, len(payload)), payload) {
		t.Fatal("payload")
	}
	if atomic.LoadUint64(&DefaultSnmp.MuxBytesSent)-sent != uint64(len(payload)) {
		t.Fatalf("MuxBytesSent delta %d", atomic.LoadUint64(&DefaultSnmp.MuxBytesSent)-sent)
	}
	if atomic.LoadUint64(&DefaultSnmp.MuxBytesReceived)-recv != uint64(len(payload)) {
		t.Fatalf("MuxBytesReceived delta %d", atomic.LoadUint64(&DefaultSnmp.MuxBytesReceived)-recv)
	}
	if atomic.LoadUint64(&DefaultSnmp.MuxStreamsOpened)-opened != 2 {
		t.Fatalf("opened delta %d", atomic.LoadUint64(&DefaultSnmp.MuxStreamsOpened)-opened)
	}
	if atomic.LoadUint64(&DefaultSnmp.MuxFramesSent)-frames == 0 {
		t.Fatal("no frames counted")
	}
	if err := cs.Close(); err != nil {
		t.Fatal(err)
	}
	_ = ss.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := ss.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if err := ss.Close(); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		return atomic.LoadUint64(&DefaultSnmp.MuxStreamsClosed)-closed == 2
	})
}

func TestMuxManyStreams(t *testing.T) {
	client, server, cleanup := newTestMux(t, 80, 400)
	defer cleanup()
	const n = 8
	type pair struct {
		c, s *MuxStream
		pay  []byte
	}
	pairs := make([]pair, n)
	pris := []uint8{MuxPriorityLow, MuxPriorityNormal, MuxPriorityHigh}
	for i := 0; i < n; i++ {
		pay := bytes.Repeat([]byte{byte(i + 1)}, 1000+i*50)
		cs, ss := openPair(t, client, server, pris[i%len(pris)])
		pairs[i] = pair{cs, ss, pay}
	}
	var wg sync.WaitGroup
	errCh := make(chan error, n*2)
	for i := range pairs {
		p := pairs[i]
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := p.c.Write(p.pay)
			errCh <- err
		}()
		go func() {
			defer wg.Done()
			buf := make([]byte, len(p.pay))
			_ = p.s.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, err := io.ReadFull(p.s, buf)
			if err != nil {
				errCh <- err
				return
			}
			if !bytes.Equal(buf, p.pay) {
				errCh <- errors.New("stream payload mismatch")
				return
			}
			errCh <- nil
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestMuxOverKCP(t *testing.T) {
	port := nextPort()
	ln, err := listenSink(port)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	payload := bytes.Repeat([]byte("kcp-mux"), 3000)
	errCh := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		// Match the dialed session: mux needs one ordered byte stream.
		sess := conn.(*UDPSession)
		sess.SetStreamMode(true)
		sess.SetWindowSize(1024, 1024)
		sess.SetNoDelay(1, 10, 2, 1)
		sess.SetMtu(1400)
		sess.SetACKNoDelay(false)
		sess.SetReadBuffer(4 * 1024 * 1024)
		sess.SetWriteBuffer(4 * 1024 * 1024)
		defer conn.Close()
		scfg := muxConfig(MuxSideServer, 1024, 32*1024)
		server, err := NewMuxSession(conn, &scfg)
		if err != nil {
			errCh <- err
			return
		}
		defer server.Close()
		ss, err := server.AcceptStream()
		if err != nil {
			errCh <- err
			return
		}
		buf := make([]byte, len(payload))
		_ = ss.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.ReadFull(ss, buf); err != nil {
			errCh <- err
			return
		}
		if !bytes.Equal(buf, payload) {
			errCh <- errors.New("server payload mismatch")
			return
		}
		_, err = ss.Write(buf)
		errCh <- err
	}()

	cliConn, err := dialSink(port)
	if err != nil {
		t.Fatal(err)
	}
	defer cliConn.Close()
	ccfg := muxConfig(MuxSideClient, 1024, 32*1024)
	client, err := NewMuxSession(cliConn, &ccfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	cs, err := client.OpenStream(MuxPriorityHigh)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Write(payload); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readFull(t, cs, len(payload)), payload) {
		t.Fatal("client read mismatch")
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("kcp mux exchange timed out")
	}
}
