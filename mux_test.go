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
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testMuxConfig(side MuxSide, frame, sendWin, recvWin int) *MuxConfig {
	cfg := DefaultMuxConfig()
	cfg.Side = side
	if frame > 0 {
		cfg.MaxFrameSize = frame
	}
	if sendWin > 0 {
		cfg.SendWindow = sendWin
	}
	if recvWin > 0 {
		cfg.RecvWindow = recvWin
	}
	return &cfg
}

func newMuxPair(t *testing.T, clientCfg, serverCfg *MuxConfig) (*MuxSession, *MuxSession) {
	t.Helper()
	if clientCfg == nil {
		clientCfg = testMuxConfig(MuxSideClient, 0, 0, 0)
	}
	if serverCfg == nil {
		serverCfg = testMuxConfig(MuxSideServer, 0, 0, 0)
	}
	clientCfg.Side = MuxSideClient
	serverCfg.Side = MuxSideServer

	a, b := net.Pipe()
	client, err := NewMuxSession(a, clientCfg)
	if err != nil {
		t.Fatalf("client session: %v", err)
	}
	server, err := NewMuxSession(b, serverCfg)
	if err != nil {
		t.Fatalf("server session: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return client, server
}

func openPair(t *testing.T, client, server *MuxSession, pri uint8) (*MuxStream, *MuxStream) {
	t.Helper()
	errCh := make(chan error, 1)
	var local *MuxStream
	go func() {
		var err error
		local, err = client.OpenStream(pri)
		errCh <- err
	}()
	remote, err := server.AcceptStream()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("open: %v", err)
	}
	return local, remote
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

func TestDefaultMuxConfig(t *testing.T) {
	cfg := DefaultMuxConfig()
	if cfg.Side != MuxSideClient {
		t.Fatalf("side = %v", cfg.Side)
	}
	if cfg.MaxFrameSize <= 0 || cfg.SendWindow <= 0 || cfg.RecvWindow <= 0 {
		t.Fatalf("non-positive defaults: %+v", cfg)
	}
}

func TestMuxStreamIDsAndEcho(t *testing.T) {
	client, server := newMuxPair(t,
		testMuxConfig(MuxSideClient, 8, 64, 64),
		testMuxConfig(MuxSideServer, 8, 64, 64),
	)

	c1, s1 := openPair(t, client, server, MuxPriorityNormal)
	c2, s2 := openPair(t, client, server, MuxPriorityHigh)
	if c1.ID() != 1 || c2.ID() != 3 {
		t.Fatalf("client ids = %d %d", c1.ID(), c2.ID())
	}
	if s1.ID() != c1.ID() || s2.ID() != c2.ID() {
		t.Fatalf("ids diverged: %d/%d %d/%d", c1.ID(), s1.ID(), c2.ID(), s2.ID())
	}
	if client.NumStreams() != 2 || server.NumStreams() != 2 {
		t.Fatalf("num streams client=%d server=%d", client.NumStreams(), server.NumStreams())
	}

	sOpen, err := server.OpenStream(MuxPriorityLow)
	if err != nil {
		t.Fatal(err)
	}
	cAccept, err := client.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	if sOpen.ID() != 2 || cAccept.ID() != 2 {
		t.Fatalf("server id = %d/%d", sOpen.ID(), cAccept.ID())
	}
	if sOpen.ID()%2 != 0 || c1.ID()%2 != 1 {
		t.Fatalf("parity server=%d client=%d", sOpen.ID(), c1.ID())
	}

	payload := bytes.Repeat([]byte("xyz"), 50) // larger than max frame and send window
	readDone := make(chan error, 1)
	go func() {
		got := make([]byte, len(payload))
		_, err := io.ReadFull(s1, got)
		if err == nil && !bytes.Equal(got, payload) {
			err = errors.New("payload mismatch")
		}
		readDone <- err
	}()
	if n, err := c1.Write(payload); err != nil || n != len(payload) {
		t.Fatalf("write n=%d err=%v", n, err)
	}
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}

	// Reverse direction on the server-opened stream.
	msg := []byte("from-server")
	if n, err := sOpen.Write(msg); err != nil || n != len(msg) {
		t.Fatalf("server write n=%d err=%v", n, err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(cAccept, buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, msg) {
		t.Fatalf("got %q", buf)
	}
}

func TestMuxFlowControlDoesNotStallOtherStreams(t *testing.T) {
	const win = 16
	client, server := newMuxPair(t,
		testMuxConfig(MuxSideClient, 8, win, win),
		testMuxConfig(MuxSideServer, 8, win, win),
	)
	blocked, blockedRemote := openPair(t, client, server, MuxPriorityLow)
	other, otherRemote := openPair(t, client, server, MuxPriorityHigh)

	before := atomic.LoadUint64(&DefaultSnmp.MuxBytesSent)
	writeDone := make(chan error, 1)
	go func() {
		_, err := blocked.Write(bytes.Repeat([]byte("B"), 1024))
		writeDone <- err
	}()

	waitUntil(t, 2*time.Second, func() bool {
		return atomic.LoadUint64(&DefaultSnmp.MuxBytesSent)-before >= win
	})
	time.Sleep(30 * time.Millisecond)
	sent := atomic.LoadUint64(&DefaultSnmp.MuxBytesSent) - before
	if sent != win {
		t.Fatalf("send window not enforced: sent %d want %d", sent, win)
	}

	if n, err := other.Write([]byte("hello")); err != nil || n != 5 {
		t.Fatalf("other stream stalled: n=%d err=%v", n, err)
	}
	got := make([]byte, 5)
	if _, err := io.ReadFull(otherRemote, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q", got)
	}

	// Draining the blocked stream restores credit and lets the writer finish.
	readDone := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(blockedRemote, make([]byte, 1024))
		readDone <- err
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("writer stayed blocked after the peer drained")
	}
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reader did not receive the full payload")
	}
}

func TestMuxRecvWindowLimitsPeer(t *testing.T) {
	client, server := newMuxPair(t,
		testMuxConfig(MuxSideClient, 8, 1000, 1000),
		testMuxConfig(MuxSideServer, 8, 1000, 20),
	)
	local, _ := openPair(t, client, server, MuxPriorityNormal)
	before := atomic.LoadUint64(&DefaultSnmp.MuxBytesSent)
	done := make(chan struct{})
	go func() {
		_, _ = local.Write(bytes.Repeat([]byte("Z"), 500))
		close(done)
	}()
	waitUntil(t, 2*time.Second, func() bool {
		return atomic.LoadUint64(&DefaultSnmp.MuxBytesSent)-before >= 20
	})
	time.Sleep(40 * time.Millisecond)
	sent := atomic.LoadUint64(&DefaultSnmp.MuxBytesSent) - before
	if sent != 20 {
		t.Fatalf("peer recv window not honored: sent %d", sent)
	}
	select {
	case <-done:
		t.Fatal("write finished without the receiver draining")
	default:
	}
}

func TestMuxScheduleControlAndPriority(t *testing.T) {
	s := &MuxSession{}
	s.wcond = sync.NewCond(&s.wmu)

	if err := s.queueFrame(muxCmdData, MuxPriorityLow, 1, []byte("L1")); err != nil {
		t.Fatal(err)
	}
	if err := s.queueFrame(muxCmdData, MuxPriorityLow, 1, []byte("L2")); err != nil {
		t.Fatal(err)
	}
	if err := s.queueFrame(muxCmdData, MuxPriorityNormal, 3, []byte("N1")); err != nil {
		t.Fatal(err)
	}
	if err := s.queueFrame(muxCmdData, MuxPriorityHigh, 5, []byte("H1")); err != nil {
		t.Fatal(err)
	}
	if err := s.queueFrame(muxCmdWindow, 0, 1, []byte{0, 0, 0, 1}); err != nil {
		t.Fatal(err)
	}

	var payloads []string
	var cmds []byte
	for i := 0; i < 5; i++ {
		frame := s.nextFrame()
		if frame == nil {
			t.Fatal("missing frame")
		}
		cmd, _, _, payload, err := readMuxFrame(bytes.NewReader(frame))
		if err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, cmd)
		payloads = append(payloads, string(payload))
	}
	if cmds[0] != muxCmdWindow {
		t.Fatalf("control frame not first: %v", cmds)
	}
	if payloads[1] != "H1" || payloads[2] != "N1" || payloads[3] != "L1" || payloads[4] != "L2" {
		t.Fatalf("priority order = %q", payloads[1:])
	}

	// A high-priority close with no queued data for that stream jumps ahead of
	// lower-priority data. A close does not pass data already queued on its stream.
	if err := s.queueFrame(muxCmdData, MuxPriorityLow, 1, []byte("L3")); err != nil {
		t.Fatal(err)
	}
	if err := s.queueFrame(muxCmdClose, 0, 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.queueFrame(muxCmdData, MuxPriorityHigh, 9, []byte("H2")); err != nil {
		t.Fatal(err)
	}
	if err := s.queueFrame(muxCmdClose, 0, 9, nil); err != nil {
		t.Fatal(err)
	}
	want := []struct {
		cmd     byte
		payload string
	}{
		{muxCmdData, "H2"},
		{muxCmdClose, ""},
		{muxCmdData, "L3"},
		{muxCmdClose, ""},
	}
	for i, exp := range want {
		frame := s.nextFrame()
		cmd, _, _, payload, err := readMuxFrame(bytes.NewReader(frame))
		if err != nil {
			t.Fatal(err)
		}
		if cmd != exp.cmd || string(payload) != exp.payload {
			t.Fatalf("frame %d cmd=%d payload=%q", i, cmd, payload)
		}
	}
}

func TestMuxHalfCloseAndRemoval(t *testing.T) {
	client, server := newMuxPair(t, nil, nil)
	local, remote := openPair(t, client, server, MuxPriorityNormal)

	payload := []byte("buffered-bytes")
	before := atomic.LoadUint64(&DefaultSnmp.MuxBytesReceived)
	if _, err := remote.Write(payload); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		return atomic.LoadUint64(&DefaultSnmp.MuxBytesReceived)-before >= uint64(len(payload))
	})

	// Local close stops writes but keeps already-buffered inbound data readable.
	if err := local.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write after close: %v", err)
	}
	if client.NumStreams() != 1 {
		t.Fatalf("stream removed before peer close, n=%d", client.NumStreams())
	}

	got := make([]byte, len(payload))
	if _, err := io.ReadFull(local, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q", got)
	}
	// Peer has not closed, so the stream stays tracked after the buffer drains.
	time.Sleep(20 * time.Millisecond)
	if client.NumStreams() != 1 {
		t.Fatalf("removed with peer still open, n=%d", client.NumStreams())
	}

	if err := remote.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := local.Read(make([]byte, 1))
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("read after remote close n=%d err=%v", n, err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		return client.NumStreams() == 0
	})

	if err := local.Close(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("second close: %v", err)
	}
}

func TestMuxCloseUnblocksWriters(t *testing.T) {
	client, server := newMuxPair(t,
		testMuxConfig(MuxSideClient, 4, 4, 4),
		testMuxConfig(MuxSideServer, 4, 4, 4),
	)
	local, remote := openPair(t, client, server, MuxPriorityNormal)

	localDone := make(chan error, 1)
	go func() {
		_, err := local.Write(bytes.Repeat([]byte("A"), 256))
		localDone <- err
	}()
	waitUntil(t, 2*time.Second, func() bool {
		return client.NumStreams() == 1
	})
	time.Sleep(20 * time.Millisecond)
	if err := local.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-localDone:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("local close unblocked with %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("local close did not unblock writer")
	}

	remoteDone := make(chan error, 1)
	go func() {
		_, err := remote.Write(bytes.Repeat([]byte("B"), 256))
		remoteDone <- err
	}()
	time.Sleep(30 * time.Millisecond)
	if err := local.Close(); !errors.Is(err, io.ErrClosedPipe) {
		// already closed; closing the remote side is what must unblock remote.Write
	}
	if err := remote.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	// remote.Close closes the remote write side. The writer above is the remote
	// writer, so it unblocks because the local side of that Write was closed.
	select {
	case err := <-remoteDone:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("stream close unblocked remote writer with %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream close did not unblock its writer")
	}
}

func TestMuxRemoteCloseUnblocksWriter(t *testing.T) {
	client, server := newMuxPair(t,
		testMuxConfig(MuxSideClient, 4, 4, 4),
		testMuxConfig(MuxSideServer, 4, 4, 4),
	)
	local, remote := openPair(t, client, server, MuxPriorityNormal)
	done := make(chan error, 1)
	go func() {
		_, err := local.Write(bytes.Repeat([]byte("C"), 256))
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	if err := remote.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("remote close unblocked with %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remote close did not unblock writer")
	}
}

func TestMuxSessionCloseUnblocks(t *testing.T) {
	client, server := newMuxPair(t,
		testMuxConfig(MuxSideClient, 4, 4, 4),
		testMuxConfig(MuxSideServer, 4, 4, 4),
	)
	local, _ := openPair(t, client, server, MuxPriorityNormal)

	readDone := make(chan error, 1)
	writeDone := make(chan error, 1)
	acceptDone := make(chan error, 1)
	go func() {
		_, err := local.Read(make([]byte, 4))
		readDone <- err
	}()
	go func() {
		_, err := local.Write(bytes.Repeat([]byte("D"), 128))
		writeDone <- err
	}()
	go func() {
		_, err := client.AcceptStream()
		acceptDone <- err
	}()
	time.Sleep(30 * time.Millisecond)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []chan error{readDone, writeDone, acceptDone} {
		select {
		case err := <-ch:
			if !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("got %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("session close did not unblock")
		}
	}
	if err := client.Close(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("second session close: %v", err)
	}
	if _, err := client.OpenStream(MuxPriorityNormal); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("open after close: %v", err)
	}
}

func TestMuxReadDeadline(t *testing.T) {
	client, server := newMuxPair(t, nil, nil)
	local, remote := openPair(t, client, server, MuxPriorityNormal)

	if err := local.SetReadDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, err := local.Read(make([]byte, 1))
	ne, ok := err.(net.Error)
	if !ok || !ne.Timeout() {
		t.Fatalf("deadline err = %v", err)
	}

	if err := local.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = local.Read(make([]byte, 1))
	ne, ok = err.(net.Error)
	if !ok || !ne.Timeout() {
		t.Fatalf("past deadline err = %v", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("past deadline blocked")
	}

	if err := local.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		_, _ = remote.Write([]byte("Q"))
	}()
	buf := make([]byte, 1)
	if _, err := io.ReadFull(local, buf); err != nil {
		t.Fatal(err)
	}
	if buf[0] != 'Q' {
		t.Fatalf("got %q", buf)
	}
}

func TestMuxCloseReturnsWhileWriteBlocked(t *testing.T) {
	gc := newGateConn(true)
	cfg := testMuxConfig(MuxSideClient, 32, 64, 64)
	sess, err := NewMuxSession(gc, cfg)
	if err != nil {
		t.Fatal(err)
	}
	gc.waitBlocked(1)

	start := time.Now()
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Close blocked for %s", elapsed)
	}
	if err := sess.Close(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("second close: %v", err)
	}
}

func TestMuxClosePreservesQueuedData(t *testing.T) {
	rawClient, rawServer := net.Pipe()
	paused := newPauseConn(rawClient)
	clientCfg := testMuxConfig(MuxSideClient, 8, 4096, 4096)
	serverCfg := testMuxConfig(MuxSideServer, 8, 4096, 4096)
	client, err := NewMuxSession(paused, clientCfg)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewMuxSession(rawServer, serverCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	local, remote := openPair(t, client, server, MuxPriorityLow)
	paused.Pause()

	payload := bytes.Repeat([]byte("abc"), 40)
	before := atomic.LoadUint64(&DefaultSnmp.MuxBytesSent)
	writeDone := make(chan error, 1)
	go func() {
		_, err := local.Write(payload)
		writeDone <- err
	}()
	waitUntil(t, 2*time.Second, func() bool {
		return atomic.LoadUint64(&DefaultSnmp.MuxBytesSent)-before >= uint64(len(payload))
	})
	if err := local.Close(); err != nil {
		t.Fatal(err)
	}
	paused.Resume()

	got, err := io.ReadAll(remote)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("delivered %d bytes, want %d", len(got), len(payload))
	}
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("write did not finish")
	}
}

func TestMuxSnmpCounters(t *testing.T) {
	header := (&Snmp{}).Header()
	want := []string{
		"MuxStreamsOpened",
		"MuxStreamsClosed",
		"MuxFramesSent",
		"MuxFramesReceived",
		"MuxBytesSent",
		"MuxBytesReceived",
	}
	if len(header) < len(want) {
		t.Fatalf("header len %d", len(header))
	}
	gotTail := header[len(header)-len(want):]
	for i, name := range want {
		if gotTail[i] != name {
			t.Fatalf("header[%d]=%s want %s", i, gotTail[i], name)
		}
	}
	if len((&Snmp{}).ToSlice()) != len(header) {
		t.Fatalf("ToSlice len %d header %d", len((&Snmp{}).ToSlice()), len(header))
	}

	DefaultSnmp.Reset()
	snap := DefaultSnmp.Copy()
	for _, v := range []uint64{
		snap.MuxStreamsOpened, snap.MuxStreamsClosed, snap.MuxFramesSent,
		snap.MuxFramesReceived, snap.MuxBytesSent, snap.MuxBytesReceived,
	} {
		if v != 0 {
			t.Fatalf("reset left %d", v)
		}
	}

	client, server := newMuxPair(t, nil, nil)
	// Handshake frames must not count as payload bytes.
	waitUntil(t, 2*time.Second, func() bool {
		s := DefaultSnmp.Copy()
		return s.MuxFramesReceived >= 2 && s.MuxFramesSent >= 2
	})
	if b := atomic.LoadUint64(&DefaultSnmp.MuxBytesSent); b != 0 {
		t.Fatalf("handshake counted as payload sent: %d", b)
	}
	if b := atomic.LoadUint64(&DefaultSnmp.MuxBytesReceived); b != 0 {
		t.Fatalf("handshake counted as payload received: %d", b)
	}

	local, remote := openPair(t, client, server, MuxPriorityNormal)
	opened := atomic.LoadUint64(&DefaultSnmp.MuxStreamsOpened)
	if opened < 2 {
		t.Fatalf("opened = %d", opened)
	}

	payload := []byte("snmp-payload-data")
	sentBefore := atomic.LoadUint64(&DefaultSnmp.MuxBytesSent)
	framesBefore := atomic.LoadUint64(&DefaultSnmp.MuxFramesSent)
	if _, err := local.Write(payload); err != nil {
		t.Fatal(err)
	}
	if delta := atomic.LoadUint64(&DefaultSnmp.MuxBytesSent) - sentBefore; delta != uint64(len(payload)) {
		t.Fatalf("bytes sent delta=%d", delta)
	}
	if atomic.LoadUint64(&DefaultSnmp.MuxFramesSent) <= framesBefore {
		t.Fatal("data frame was not counted")
	}

	waitUntil(t, 2*time.Second, func() bool {
		return atomic.LoadUint64(&DefaultSnmp.MuxBytesReceived) >= uint64(len(payload))
	})
	recvBeforeRead := atomic.LoadUint64(&DefaultSnmp.MuxBytesReceived)
	sentBeforeRead := atomic.LoadUint64(&DefaultSnmp.MuxBytesSent)
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(remote, buf); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if atomic.LoadUint64(&DefaultSnmp.MuxBytesReceived) != recvBeforeRead {
		t.Fatal("window update counted as received payload")
	}
	if atomic.LoadUint64(&DefaultSnmp.MuxBytesSent) != sentBeforeRead {
		t.Fatal("window update counted as sent payload")
	}

	closedBefore := atomic.LoadUint64(&DefaultSnmp.MuxStreamsClosed)
	if err := local.Close(); err != nil {
		t.Fatal(err)
	}
	if err := remote.Close(); err != nil {
		t.Fatal(err)
	}
	if delta := atomic.LoadUint64(&DefaultSnmp.MuxStreamsClosed) - closedBefore; delta != 2 {
		t.Fatalf("closed delta=%d", delta)
	}

	copied := DefaultSnmp.Copy()
	if copied.MuxBytesSent != atomic.LoadUint64(&DefaultSnmp.MuxBytesSent) {
		t.Fatal("copy mismatch")
	}
	DefaultSnmp.Reset()
	if atomic.LoadUint64(&DefaultSnmp.MuxBytesSent) != 0 || atomic.LoadUint64(&DefaultSnmp.MuxStreamsOpened) != 0 {
		t.Fatal("reset did not clear mux counters")
	}
}

// pauseConn delays Write while paused. Close stays non-blocking.
type pauseConn struct {
	net.Conn
	mu     sync.Mutex
	cond   *sync.Cond
	paused bool
	closed bool
}

func newPauseConn(conn net.Conn) *pauseConn {
	p := &pauseConn{Conn: conn}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *pauseConn) Pause() {
	p.mu.Lock()
	p.paused = true
	p.mu.Unlock()
}

func (p *pauseConn) Resume() {
	p.mu.Lock()
	p.paused = false
	p.cond.Broadcast()
	p.mu.Unlock()
}

func (p *pauseConn) Write(b []byte) (int, error) {
	p.mu.Lock()
	for p.paused && !p.closed {
		p.cond.Wait()
	}
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return 0, io.ErrClosedPipe
	}
	return p.Conn.Write(b)
}

func (p *pauseConn) Close() error {
	p.mu.Lock()
	p.closed = true
	p.paused = false
	p.cond.Broadcast()
	p.mu.Unlock()
	return p.Conn.Close()
}

type muxAddr string

func (a muxAddr) Network() string { return "mux" }
func (a muxAddr) String() string  { return string(a) }

// gateConn blocks Write while hold is set. Close is non-blocking.
type gateConn struct {
	mu      sync.Mutex
	cond    *sync.Cond
	hold    bool
	closed  bool
	blocked int
	frames  [][]byte
}

func newGateConn(hold bool) *gateConn {
	g := &gateConn{hold: hold}
	g.cond = sync.NewCond(&g.mu)
	return g
}

func (g *gateConn) Read(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for !g.closed {
		g.cond.Wait()
	}
	return 0, io.EOF
}

func (g *gateConn) Write(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.blocked++
	g.cond.Broadcast()
	for g.hold && !g.closed {
		g.cond.Wait()
	}
	g.blocked--
	if g.closed {
		return 0, io.ErrClosedPipe
	}
	g.frames = append(g.frames, append([]byte(nil), p...))
	g.cond.Broadcast()
	return len(p), nil
}

func (g *gateConn) Close() error {
	g.mu.Lock()
	g.closed = true
	g.hold = false
	g.cond.Broadcast()
	g.mu.Unlock()
	return nil
}

func (g *gateConn) LocalAddr() net.Addr  { return muxAddr("local") }
func (g *gateConn) RemoteAddr() net.Addr { return muxAddr("remote") }
func (g *gateConn) SetDeadline(time.Time) error {
	return nil
}
func (g *gateConn) SetReadDeadline(time.Time) error { return nil }
func (g *gateConn) SetWriteDeadline(time.Time) error {
	return nil
}

func (g *gateConn) waitBlocked(n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for g.blocked < n && !g.closed && time.Now().Before(deadline) {
		g.cond.Wait()
	}
}
