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
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func muxTestConfig(side MuxSide, frame, window int) *MuxConfig {
	cfg := DefaultMuxConfig()
	cfg.Side = side
	if frame > 0 {
		cfg.MaxFrameSize = frame
	}
	if window > 0 {
		cfg.SendWindow = window
		cfg.RecvWindow = window
	}
	return &cfg
}

func newMuxPipe(t *testing.T, frame, window int) (*MuxSession, *MuxSession) {
	t.Helper()
	c1, c2 := net.Pipe()
	cli, err := NewMuxSession(c1, muxTestConfig(MuxSideClient, frame, window))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewMuxSession(c2, muxTestConfig(MuxSideServer, frame, window))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cli.Close()
		srv.Close()
	})
	return cli, srv
}

func muxOpenAccept(t *testing.T, opener, acceptor *MuxSession, prio uint8) (*MuxStream, *MuxStream) {
	t.Helper()
	a, err := opener.OpenStream(prio)
	if err != nil {
		t.Fatal(err)
	}
	b, err := acceptor.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	if a.ID() != b.ID() {
		t.Fatalf("stream id mismatch: %d vs %d", a.ID(), b.ID())
	}
	return a, b
}

func muxRandBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
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

type muxResult struct {
	n   int
	err error
}

func TestMuxStreamIDs(t *testing.T) {
	cli, srv := newMuxPipe(t, 0, 0)

	for _, want := range []uint32{1, 3, 5} {
		a, b := muxOpenAccept(t, cli, srv, MuxPriorityNormal)
		if a.ID() != want {
			t.Fatalf("client stream id = %d, want %d", a.ID(), want)
		}
		_ = b
	}
	for _, want := range []uint32{2, 4} {
		a, _ := muxOpenAccept(t, srv, cli, MuxPriorityNormal)
		if a.ID() != want {
			t.Fatalf("server stream id = %d, want %d", a.ID(), want)
		}
	}
	if cli.NumStreams() != 5 || srv.NumStreams() != 5 {
		t.Fatalf("NumStreams = %d/%d, want 5/5", cli.NumStreams(), srv.NumStreams())
	}
}

func TestMuxConcurrentStreams(t *testing.T) {
	cli, srv := newMuxPipe(t, 1024, 8192)

	const streams = 16
	const size = 256 * 1024
	payloads := make(map[uint32][]byte)
	var mu sync.Mutex

	var wg sync.WaitGroup
	errs := make(chan error, 2*streams)
	for i := 0; i < streams; i++ {
		st, err := cli.OpenStream(uint8(i % 3))
		if err != nil {
			t.Fatal(err)
		}
		data := muxRandBytes(size)
		mu.Lock()
		payloads[st.ID()] = data
		mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if n, err := st.Write(data); err != nil || n != len(data) {
				errs <- fmt.Errorf("write %d: n=%d err=%v", st.ID(), n, err)
				return
			}
			errs <- st.Close()
		}()
	}

	for i := 0; i < streams; i++ {
		st, err := srv.AcceptStream()
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := io.ReadAll(st)
			if err != nil {
				errs <- err
				return
			}
			mu.Lock()
			want := payloads[st.ID()]
			mu.Unlock()
			if !bytes.Equal(got, want) {
				errs <- fmt.Errorf("stream %d: data mismatch (%d bytes, want %d)", st.ID(), len(got), len(want))
				return
			}
			errs <- st.Close()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	waitFor(t, "streams removed", func() bool { return cli.NumStreams() == 0 && srv.NumStreams() == 0 })
}

func TestMuxFlowControl(t *testing.T) {
	const window = 4096
	cli, srv := newMuxPipe(t, 1024, window)

	slow, slowPeer := muxOpenAccept(t, cli, srv, MuxPriorityNormal)
	fast, fastPeer := muxOpenAccept(t, cli, srv, MuxPriorityNormal)

	data := muxRandBytes(8 * window)
	done := make(chan muxResult, 1)
	go func() {
		n, err := slow.Write(data)
		done <- muxResult{n, err}
	}()

	time.Sleep(100 * time.Millisecond)
	select {
	case r := <-done:
		t.Fatalf("write finished without the reader draining: %+v", r)
	default:
	}
	srv.mu.Lock()
	buffered := slowPeer.recvBufBytes
	srv.mu.Unlock()
	if buffered > window {
		t.Fatalf("receiver buffered %d bytes, window is %d", buffered, window)
	}

	// the blocked stream must not stall others
	msg := muxRandBytes(3 * window)
	go fast.Write(msg)
	got := make([]byte, len(msg))
	fastPeer.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(fastPeer, got); err != nil {
		t.Fatalf("unblocked stream stalled: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatal("fast stream data mismatch")
	}

	got = make([]byte, len(data))
	if _, err := io.ReadFull(slowPeer, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("slow stream data mismatch")
	}
	select {
	case r := <-done:
		if r.err != nil || r.n != len(data) {
			t.Fatalf("write returned %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("writer did not resume after window update")
	}
}

// Asymmetric windows: the sender must honour the smaller of its SendWindow
// and the receiver's RecvWindow, and the receiver must return credit even if
// the sender's window is far below its own.
func TestMuxAsymmetricWindows(t *testing.T) {
	c1, c2 := net.Pipe()
	cli, err := NewMuxSession(c1, muxTestConfig(MuxSideClient, 512, 1000))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	srv, err := NewMuxSession(c2, muxTestConfig(MuxSideServer, 4096, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	for _, dir := range []struct{ opener, acceptor *MuxSession }{{cli, srv}, {srv, cli}} {
		a, b := muxOpenAccept(t, dir.opener, dir.acceptor, MuxPriorityNormal)
		data := muxRandBytes(64 * 1024)
		go func() {
			a.Write(data)
			a.Close()
		}()
		got, err := io.ReadAll(b)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, data) {
			t.Fatal("data mismatch")
		}
	}
}

func writeRawMuxFrame(t *testing.T, w io.Writer, cmd uint8, sid uint32, payload []byte) {
	t.Helper()
	buf := make([]byte, muxHeaderSize+len(payload))
	encodeMuxHeader(buf, cmd, 0, len(payload), sid)
	copy(buf[muxHeaderSize:], payload)
	if _, err := w.Write(buf); err != nil {
		t.Fatal(err)
	}
}

type rawMuxFrame struct {
	cmd     uint8
	sid     uint32
	payload []byte
}

func readRawMuxFrame(t *testing.T, r io.Reader) rawMuxFrame {
	t.Helper()
	var hdr [muxHeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		t.Fatal(err)
	}
	f := rawMuxFrame{cmd: hdr[0], sid: binary.LittleEndian.Uint32(hdr[4:])}
	f.payload = make([]byte, binary.LittleEndian.Uint16(hdr[2:]))
	if _, err := io.ReadFull(r, f.payload); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestMuxPriorityScheduling(t *testing.T) {
	c1, raw := net.Pipe()
	defer raw.Close()
	cli, err := NewMuxSession(c1, muxTestConfig(MuxSideClient, 1024, 64*1024))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	low, _ := cli.OpenStream(MuxPriorityLow)
	high, _ := cli.OpenStream(MuxPriorityHigh)
	for _, sid := range []uint32{low.ID(), high.ID()} {
		if f := readRawMuxFrame(t, raw); f.cmd != muxCmdSYN || f.sid != sid {
			t.Fatalf("expected SYN %d, got cmd=%d sid=%d", sid, f.cmd, f.sid)
		}
	}
	var win [muxWindowPayload]byte
	binary.LittleEndian.PutUint32(win[0:], 64*1024)
	binary.LittleEndian.PutUint32(win[4:], 64*1024)
	writeRawMuxFrame(t, raw, muxCmdACK, low.ID(), win[:])
	writeRawMuxFrame(t, raw, muxCmdACK, high.ID(), win[:])
	waitFor(t, "ACKs applied", func() bool {
		cli.mu.Lock()
		defer cli.mu.Unlock()
		return low.acked && high.acked
	})

	// the send loop blocks writing the first low-priority frame into the pipe
	go low.Write(make([]byte, 8*1024))
	time.Sleep(50 * time.Millisecond)
	go high.Write([]byte("urgent"))
	time.Sleep(50 * time.Millisecond)
	next, _ := cli.OpenStream(MuxPriorityLow)
	time.Sleep(50 * time.Millisecond)

	want := []struct {
		cmd uint8
		sid uint32
	}{
		{muxCmdPSH, low.ID()},  // already on the wire
		{muxCmdSYN, next.ID()}, // control frames first
		{muxCmdPSH, high.ID()}, // then higher priority data
	}
	for i := 0; i < 7; i++ {
		want = append(want, struct {
			cmd uint8
			sid uint32
		}{muxCmdPSH, low.ID()})
	}
	for i, w := range want {
		f := readRawMuxFrame(t, raw)
		if f.cmd != w.cmd || f.sid != w.sid {
			t.Fatalf("frame %d: got cmd=%d sid=%d, want cmd=%d sid=%d", i, f.cmd, f.sid, w.cmd, w.sid)
		}
	}
}

func TestMuxReadDeadline(t *testing.T) {
	cli, srv := newMuxPipe(t, 0, 0)
	a, b := muxOpenAccept(t, cli, srv, MuxPriorityNormal)

	b.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	start := time.Now()
	_, err := b.Read(make([]byte, 16))
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("expected timeout net.Error, got %v", err)
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Fatal("read returned before deadline")
	}

	// extending the deadline wakes a blocked reader
	b.SetReadDeadline(time.Now().Add(time.Hour))
	done := make(chan error, 1)
	go func() {
		_, err := b.Read(make([]byte, 16))
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	b.SetReadDeadline(time.Now())
	select {
	case err := <-done:
		if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
			t.Fatalf("expected timeout, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked reader did not observe new deadline")
	}

	b.SetReadDeadline(time.Time{})
	a.Write([]byte("ping"))
	buf := make([]byte, 4)
	if _, err := io.ReadFull(b, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("read after clearing deadline: %q %v", buf, err)
	}
}

func TestMuxHalfClose(t *testing.T) {
	cli, srv := newMuxPipe(t, 0, 0)
	a, b := muxOpenAccept(t, cli, srv, MuxPriorityNormal)

	if _, err := b.Write([]byte("world")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "data buffered", func() bool {
		cli.mu.Lock()
		defer cli.mu.Unlock()
		return a.recvBufBytes == 5
	})

	a.Write([]byte("hello"))
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != io.ErrClosedPipe {
		t.Fatalf("second close: %v", err)
	}
	if _, err := a.Write([]byte("x")); err != io.ErrClosedPipe {
		t.Fatalf("write after close: %v", err)
	}

	// locally closed: buffered data stays readable, then ErrClosedPipe
	buf := make([]byte, 16)
	n, err := a.Read(buf)
	if err != nil || string(buf[:n]) != "world" {
		t.Fatalf("read buffered after close: %q %v", buf[:n], err)
	}
	if _, err := a.Read(buf); err != io.ErrClosedPipe {
		t.Fatalf("read after drain: %v", err)
	}

	// remotely closed: data then EOF, writes fail
	got, err := io.ReadAll(b)
	if err != nil || string(got) != "hello" {
		t.Fatalf("peer read: %q %v", got, err)
	}
	if _, err := b.Write([]byte("x")); err != io.ErrClosedPipe {
		t.Fatalf("write after remote close: %v", err)
	}
}

func TestMuxStreamRemoval(t *testing.T) {
	cli, srv := newMuxPipe(t, 0, 0)
	a, b := muxOpenAccept(t, cli, srv, MuxPriorityNormal)

	a.Write([]byte("leftover"))
	a.Close()
	b.Close()

	waitFor(t, "client stream removed", func() bool { return cli.NumStreams() == 0 })
	time.Sleep(20 * time.Millisecond)
	if srv.NumStreams() != 1 {
		t.Fatalf("stream with undrained data removed early, NumStreams=%d", srv.NumStreams())
	}

	buf := make([]byte, 64)
	n, err := b.Read(buf)
	if err != nil || string(buf[:n]) != "leftover" {
		t.Fatalf("drain: %q %v", buf[:n], err)
	}
	if srv.NumStreams() != 0 {
		t.Fatalf("NumStreams after drain = %d", srv.NumStreams())
	}
}

func TestMuxCloseUnblocksWriters(t *testing.T) {
	cli, srv := newMuxPipe(t, 512, 1024)

	startBlockedWrite := func(st *MuxStream) chan muxResult {
		ch := make(chan muxResult, 1)
		go func() {
			n, err := st.Write(make([]byte, 64*1024))
			ch <- muxResult{n, err}
		}()
		time.Sleep(50 * time.Millisecond)
		select {
		case r := <-ch:
			t.Fatalf("write was not blocked: %+v", r)
		default:
		}
		return ch
	}
	expectClosed := func(what string, ch chan muxResult) {
		t.Helper()
		select {
		case r := <-ch:
			if r.err != io.ErrClosedPipe {
				t.Fatalf("%s: got %v, want io.ErrClosedPipe", what, r.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: writer still blocked", what)
		}
	}

	a, _ := muxOpenAccept(t, cli, srv, MuxPriorityNormal)
	ch := startBlockedWrite(a)
	a.Close()
	expectClosed("local close", ch)

	c, d := muxOpenAccept(t, cli, srv, MuxPriorityNormal)
	ch = startBlockedWrite(c)
	d.Close()
	expectClosed("remote close", ch)
}

func TestMuxSessionCloseUnblocksAll(t *testing.T) {
	cli, srv := newMuxPipe(t, 512, 1024)
	a, _ := muxOpenAccept(t, cli, srv, MuxPriorityNormal)
	r, _ := muxOpenAccept(t, cli, srv, MuxPriorityNormal)

	results := make(chan error, 3)
	go func() {
		_, err := a.Write(make([]byte, 64*1024))
		results <- err
	}()
	go func() {
		_, err := r.Read(make([]byte, 1))
		results <- err
	}()
	go func() {
		_, err := cli.AcceptStream()
		results <- err
	}()
	time.Sleep(50 * time.Millisecond)

	if err := cli.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		select {
		case err := <-results:
			if err != io.ErrClosedPipe {
				t.Fatalf("got %v, want io.ErrClosedPipe", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("operation still blocked after session close")
		}
	}

	if _, err := cli.OpenStream(MuxPriorityNormal); err != io.ErrClosedPipe {
		t.Fatalf("OpenStream after close: %v", err)
	}
	if err := cli.Close(); err != io.ErrClosedPipe {
		t.Fatalf("second Close: %v", err)
	}
	if err := a.Close(); err != io.ErrClosedPipe {
		t.Fatalf("stream Close after session close: %v", err)
	}
	if cli.NumStreams() != 0 {
		t.Fatalf("NumStreams after close = %d", cli.NumStreams())
	}

	// the peer notices the connection going away
	if _, err := srv.AcceptStream(); err != io.ErrClosedPipe {
		t.Fatalf("peer AcceptStream: %v", err)
	}
}

// stuckConn models a transport whose Write never returns and whose Close
// waits for the in-progress Write.
type stuckConn struct {
	net.Conn
	mu      sync.Mutex
	release chan struct{}
}

func (c *stuckConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	<-c.release
	return 0, io.ErrClosedPipe
}

func (c *stuckConn) Read(b []byte) (int, error) {
	<-c.release
	return 0, io.EOF
}

func (c *stuckConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return nil
}

func TestMuxCloseWithBlockedConnWrite(t *testing.T) {
	conn := &stuckConn{release: make(chan struct{})}
	defer close(conn.release)

	sess, err := NewMuxSession(conn, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err := sess.OpenStream(MuxPriorityNormal)
	if err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan error, 1)
	go func() {
		_, err := st.Write([]byte("never acknowledged"))
		writeDone <- err
	}()
	time.Sleep(50 * time.Millisecond)

	closed := make(chan error, 1)
	go func() { closed <- sess.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close blocked on the underlying connection")
	}
	select {
	case err := <-writeDone:
		if err != io.ErrClosedPipe {
			t.Fatalf("write: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer still blocked")
	}
}

func TestMuxSnmp(t *testing.T) {
	cli, srv := newMuxPipe(t, 1000, 0)
	before := DefaultSnmp.Copy()

	a, b := muxOpenAccept(t, cli, srv, MuxPriorityNormal)
	const size = 10000
	go a.Write(make([]byte, size))
	if _, err := io.ReadFull(b, make([]byte, size)); err != nil {
		t.Fatal(err)
	}
	a.Close()
	b.Close()
	waitFor(t, "streams removed", func() bool { return cli.NumStreams() == 0 && srv.NumStreams() == 0 })

	after := DefaultSnmp.Copy()
	if d := after.MuxStreamsOpened - before.MuxStreamsOpened; d != 2 {
		t.Fatalf("MuxStreamsOpened delta = %d, want 2", d)
	}
	if d := after.MuxStreamsClosed - before.MuxStreamsClosed; d != 2 {
		t.Fatalf("MuxStreamsClosed delta = %d, want 2", d)
	}
	if d := after.MuxBytesSent - before.MuxBytesSent; d != size {
		t.Fatalf("MuxBytesSent delta = %d, want %d", d, size)
	}
	if d := after.MuxBytesReceived - before.MuxBytesReceived; d != size {
		t.Fatalf("MuxBytesReceived delta = %d, want %d", d, size)
	}
	// SYN, ACK, 10 PSH, two FINs at least
	sent := after.MuxFramesSent - before.MuxFramesSent
	recv := after.MuxFramesReceived - before.MuxFramesReceived
	if sent < 14 || recv < 14 {
		t.Fatalf("frame deltas sent=%d received=%d, want >= 14", sent, recv)
	}

	s := newSnmp()
	s.MuxStreamsOpened, s.MuxStreamsClosed = 1, 2
	s.MuxFramesSent, s.MuxFramesReceived = 3, 4
	s.MuxBytesSent, s.MuxBytesReceived = 5, 6
	header, values := s.Header(), s.ToSlice()
	if len(header) != len(values) {
		t.Fatalf("Header has %d fields, ToSlice has %d", len(header), len(values))
	}
	tail := len(header) - 6
	for i, name := range []string{"MuxStreamsOpened", "MuxStreamsClosed", "MuxFramesSent", "MuxFramesReceived", "MuxBytesSent", "MuxBytesReceived"} {
		if header[tail+i] != name || values[tail+i] != fmt.Sprint(i+1) {
			t.Fatalf("field %s: header=%s value=%s", name, header[tail+i], values[tail+i])
		}
	}
	if c := s.Copy(); c.MuxBytesReceived != 6 || c.MuxStreamsOpened != 1 {
		t.Fatalf("Copy lost mux counters: %+v", c)
	}
	s.Reset()
	if *s != (Snmp{}) {
		t.Fatalf("Reset left values: %+v", s)
	}
}

func TestMuxConfigValidation(t *testing.T) {
	if _, err := NewMuxSession(nil, nil); err == nil {
		t.Fatal("expected error for nil conn")
	}
	bad := []func(*MuxConfig){
		func(c *MuxConfig) { c.Side = 7 },
		func(c *MuxConfig) { c.MaxFrameSize = 0 },
		func(c *MuxConfig) { c.MaxFrameSize = 1 << 16 },
		func(c *MuxConfig) { c.SendWindow = 0 },
		func(c *MuxConfig) { c.RecvWindow = -1 },
	}
	for i, mod := range bad {
		cfg := DefaultMuxConfig()
		mod(&cfg)
		c1, c2 := net.Pipe()
		if _, err := NewMuxSession(c1, &cfg); err == nil {
			t.Fatalf("case %d: expected error for %+v", i, cfg)
		}
		c1.Close()
		c2.Close()
	}

	c1, c2 := net.Pipe()
	defer c2.Close()
	sess, err := NewMuxSession(c1, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if st, err := sess.OpenStream(MuxPriorityNormal); err != nil || st.ID() != 1 {
		t.Fatalf("default config should be client side: %v", err)
	}
}

func TestMuxOverKCP(t *testing.T) {
	port := nextPort()
	l, err := ListenWithOptions(fmt.Sprintf("127.0.0.1:%d", port), nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	srvErr := make(chan error, 1)
	go func() {
		conn, err := l.AcceptKCP()
		if err != nil {
			srvErr <- err
			return
		}
		conn.SetNoDelay(1, 10, 2, 1)
		conn.SetWindowSize(1024, 1024)
		srv, err := NewMuxSession(conn, muxTestConfig(MuxSideServer, 0, 0))
		if err != nil {
			srvErr <- err
			return
		}
		defer srv.Close()
		for {
			st, err := srv.AcceptStream()
			if err != nil {
				return
			}
			go func() {
				io.Copy(st, st)
				st.Close()
			}()
		}
	}()

	conn, err := DialWithOptions(fmt.Sprintf("127.0.0.1:%d", port), nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetNoDelay(1, 10, 2, 1)
	conn.SetWindowSize(1024, 1024)
	cli, err := NewMuxSession(conn, muxTestConfig(MuxSideClient, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	const streams = 6
	const size = 512 * 1024
	var wg sync.WaitGroup
	errs := make(chan error, streams)
	for i := 0; i < streams; i++ {
		st, err := cli.OpenStream(uint8(i % 3))
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer st.Close()
			data := muxRandBytes(size)
			go st.Write(data)
			st.SetReadDeadline(time.Now().Add(20 * time.Second))
			got := make([]byte, size)
			if _, err := io.ReadFull(st, got); err != nil {
				errs <- fmt.Errorf("stream %d: %v", st.ID(), err)
				return
			}
			if !bytes.Equal(got, data) {
				errs <- fmt.Errorf("stream %d: echo mismatch", st.ID())
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	select {
	case err := <-srvErr:
		t.Fatal(err)
	default:
	}
	waitFor(t, "client streams removed", func() bool { return cli.NumStreams() == 0 })
}
