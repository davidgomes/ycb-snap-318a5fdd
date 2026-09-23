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

func serverMuxConfig() *MuxConfig {
	cfg := DefaultMuxConfig()
	cfg.Side = MuxSideServer
	return &cfg
}

func newMuxPair(t *testing.T, ccfg, scfg *MuxConfig) (client, server *MuxSession) {
	t.Helper()
	if scfg == nil {
		scfg = serverMuxConfig()
	}
	c1, c2 := net.Pipe()
	client, err := NewMuxSession(c1, ccfg)
	if err != nil {
		t.Fatal(err)
	}
	server, err = NewMuxSession(c2, scfg)
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
		time.Sleep(time.Millisecond)
	}
}

func mustNotFinish(t *testing.T, what string, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("%s should block, returned %v", what, err)
	case <-time.After(200 * time.Millisecond):
	}
}

func mustFinish(t *testing.T, what string, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not return", what)
		return nil
	}
}

func (st *MuxStream) bufferedBytes() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.buffered
}

func (st *MuxStream) isRemoteClosed() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.remoteClosed
}

func TestMuxStreamIDs(t *testing.T) {
	client, server := newMuxPair(t, nil, nil)

	for _, want := range []uint32{1, 3, 5} {
		st, err := client.OpenStream(MuxPriorityNormal)
		if err != nil {
			t.Fatal(err)
		}
		if st.ID() != want {
			t.Fatalf("client stream id = %d, want %d", st.ID(), want)
		}
		acc, err := server.AcceptStream()
		if err != nil {
			t.Fatal(err)
		}
		if acc.ID() != want {
			t.Fatalf("accepted stream id = %d, want %d", acc.ID(), want)
		}
	}

	for _, want := range []uint32{2, 4} {
		st, err := server.OpenStream(MuxPriorityHigh)
		if err != nil {
			t.Fatal(err)
		}
		if st.ID() != want {
			t.Fatalf("server stream id = %d, want %d", st.ID(), want)
		}
		acc, err := client.AcceptStream()
		if err != nil {
			t.Fatal(err)
		}
		if acc.ID() != want {
			t.Fatalf("accepted stream id = %d, want %d", acc.ID(), want)
		}
	}

	if client.NumStreams() != 5 || server.NumStreams() != 5 {
		t.Fatalf("NumStreams = %d/%d, want 5/5", client.NumStreams(), server.NumStreams())
	}
}

func TestMuxConfigValidation(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	if _, err := NewMuxSession(nil, nil); err == nil {
		t.Fatal("nil conn should be rejected")
	}

	bad := []func(*MuxConfig){
		func(c *MuxConfig) { c.Side = 7 },
		func(c *MuxConfig) { c.MaxFrameSize = 0 },
		func(c *MuxConfig) { c.MaxFrameSize = muxMaxFrameSize + 1 },
		func(c *MuxConfig) { c.SendWindow = 0 },
		func(c *MuxConfig) { c.RecvWindow = -1 },
	}
	for i, mutate := range bad {
		cfg := DefaultMuxConfig()
		mutate(&cfg)
		if _, err := NewMuxSession(c1, &cfg); err == nil {
			t.Fatalf("config %d should be rejected", i)
		}
	}

	sess, err := NewMuxSession(c1, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess.Close()
}

func muxEcho(t *testing.T, client, server *MuxSession, streams, size int) {
	t.Helper()

	go func() {
		for {
			st, err := server.AcceptStream()
			if err != nil {
				return
			}
			go func() {
				io.Copy(st, st)
				st.Close()
			}()
		}
	}()

	var wg sync.WaitGroup
	errs := make(chan error, streams)
	priorities := []uint8{MuxPriorityHigh, MuxPriorityNormal, MuxPriorityLow}
	for i := 0; i < streams; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st, err := client.OpenStream(priorities[i%len(priorities)])
			if err != nil {
				errs <- err
				return
			}
			payload := make([]byte, size)
			rand.Read(payload)

			writeErr := make(chan error, 1)
			go func() {
				_, err := st.Write(payload)
				writeErr <- err
			}()

			got := make([]byte, size)
			if _, err := io.ReadFull(st, got); err != nil {
				errs <- fmt.Errorf("stream %d: read: %w", st.ID(), err)
				return
			}
			if err := <-writeErr; err != nil {
				errs <- fmt.Errorf("stream %d: write: %w", st.ID(), err)
				return
			}
			if !bytes.Equal(got, payload) {
				errs <- fmt.Errorf("stream %d: echoed data mismatch", st.ID())
				return
			}
			st.Close()
			if _, err := st.Read(make([]byte, 1)); !errors.Is(err, io.ErrClosedPipe) {
				errs <- fmt.Errorf("stream %d: read after close: %v", st.ID(), err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	waitFor(t, "streams to be removed", func() bool {
		return client.NumStreams() == 0 && server.NumStreams() == 0
	})
}

func TestMuxEchoPipe(t *testing.T) {
	cfg := DefaultMuxConfig()
	cfg.MaxFrameSize = 1000
	cfg.SendWindow = 8192
	cfg.RecvWindow = 8192
	scfg := cfg
	scfg.Side = MuxSideServer
	client, server := newMuxPair(t, &cfg, &scfg)
	muxEcho(t, client, server, 16, 256*1024)
}

func TestMuxEchoKCP(t *testing.T) {
	port := nextPort()
	l, err := ListenWithOptions(fmt.Sprintf("127.0.0.1:%v", port), nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	cconn, err := DialWithOptions(fmt.Sprintf("127.0.0.1:%v", port), nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	cconn.SetStreamMode(true)
	cconn.SetWindowSize(1024, 1024)
	cconn.SetNoDelay(1, 10, 2, 1)
	client, err := NewMuxSession(cconn, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// Streams can be opened and written before the server session exists.
	first, err := client.OpenStream(MuxPriorityNormal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}

	sconn, err := l.AcceptKCP()
	if err != nil {
		t.Fatal(err)
	}
	sconn.SetStreamMode(true)
	sconn.SetWindowSize(1024, 1024)
	sconn.SetNoDelay(1, 10, 2, 1)
	server, err := NewMuxSession(sconn, serverMuxConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	acc, err := server.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(acc, buf); err != nil || string(buf) != "hello" {
		t.Fatalf("read %q, %v", buf, err)
	}
	first.Close()
	acc.Close()

	muxEcho(t, client, server, 8, 128*1024)
}

func TestMuxFlowControl(t *testing.T) {
	cfg := DefaultMuxConfig()
	cfg.MaxFrameSize = 1024
	cfg.SendWindow = 4096
	client, server := newMuxPair(t, &cfg, nil)

	blocked, _ := client.OpenStream(MuxPriorityHigh)
	other, _ := client.OpenStream(MuxPriorityLow)
	sBlocked, _ := server.AcceptStream()
	sOther, _ := server.AcceptStream()

	payload := make([]byte, 16*1024)
	rand.Read(payload)
	done := make(chan error, 1)
	go func() {
		_, err := blocked.Write(payload)
		done <- err
	}()
	mustNotFinish(t, "write beyond the send window", done)
	if n := sBlocked.bufferedBytes(); n != cfg.SendWindow {
		t.Fatalf("receiver buffered %d bytes, want exactly the %d byte window", n, cfg.SendWindow)
	}

	// A stream without credit must not stall the others, even lower-priority ones.
	if _, err := other.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	sOther.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4)
	if _, err := io.ReadFull(sOther, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("other stream read %q, %v", buf, err)
	}

	got := make([]byte, len(payload))
	if _, err := io.ReadFull(sBlocked, got); err != nil {
		t.Fatal(err)
	}
	if err := mustFinish(t, "write after the receiver drained", done); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("data mismatch")
	}
}

func TestMuxRecvWindowLimitsPeer(t *testing.T) {
	scfg := serverMuxConfig()
	scfg.RecvWindow = 2048
	client, server := newMuxPair(t, nil, scfg)

	// A round trip guarantees the client has applied the server's SETTINGS.
	sync1, _ := client.OpenStream(MuxPriorityNormal)
	ssync1, _ := server.AcceptStream()
	ssync1.Write([]byte("x"))
	if _, err := io.ReadFull(sync1, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}

	st, _ := client.OpenStream(MuxPriorityNormal)
	sst, _ := server.AcceptStream()
	done := make(chan error, 1)
	go func() {
		_, err := st.Write(make([]byte, 8192))
		done <- err
	}()
	mustNotFinish(t, "write beyond the peer's receive window", done)
	if n := sst.bufferedBytes(); n != scfg.RecvWindow {
		t.Fatalf("receiver buffered %d bytes, want %d", n, scfg.RecvWindow)
	}
	if _, err := io.ReadFull(sst, make([]byte, 8192)); err != nil {
		t.Fatal(err)
	}
	if err := mustFinish(t, "write", done); err != nil {
		t.Fatal(err)
	}
}

type muxRawFrame struct {
	cmd     byte
	sid     uint32
	payload []byte
}

func (f muxRawFrame) String() string {
	return fmt.Sprintf("{cmd:%d sid:%d payload:%q}", f.cmd, f.sid, f.payload)
}

func readRawMuxFrame(t *testing.T, r io.Reader) muxRawFrame {
	t.Helper()
	var hdr [muxHeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		t.Fatal(err)
	}
	if hdr[0] != muxVersion {
		t.Fatalf("bad version %d", hdr[0])
	}
	f := muxRawFrame{cmd: hdr[1], sid: binary.LittleEndian.Uint32(hdr[2:])}
	f.payload = make([]byte, binary.LittleEndian.Uint32(hdr[6:]))
	if _, err := io.ReadFull(r, f.payload); err != nil {
		t.Fatal(err)
	}
	return f
}

func writeRawMuxFrame(w io.Writer, cmd byte, sid uint32, payload []byte) error {
	buf := make([]byte, muxHeaderSize+len(payload))
	muxPutHeader(buf, cmd, sid, len(payload))
	copy(buf[muxHeaderSize:], payload)
	_, err := w.Write(buf)
	return err
}

func rawMuxSettings(recvWindow, sendWindow uint32) []byte {
	var p [8]byte
	binary.LittleEndian.PutUint32(p[0:], recvWindow)
	binary.LittleEndian.PutUint32(p[4:], sendWindow)
	return p[:]
}

// The raw end of the pipe is not read until everything is queued, so the wire
// order shows exactly how the scheduler drains its queues.
func TestMuxSchedulingOrder(t *testing.T) {
	c1, raw := net.Pipe()
	defer raw.Close()
	cfg := DefaultMuxConfig()
	cfg.MaxFrameSize = 4
	client, err := NewMuxSession(c1, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	low, _ := client.OpenStream(MuxPriorityLow)
	normal, _ := client.OpenStream(MuxPriorityNormal)
	idle, _ := client.OpenStream(MuxPriorityNormal)
	low.Write([]byte("llllLLLL"))
	normal.Write([]byte("nnnn"))
	high, _ := client.OpenStream(MuxPriorityHigh)
	high.Write([]byte("hhhh"))
	idle.Close()
	low.Close()

	want := []muxRawFrame{
		{muxCmdSettings, 0, rawMuxSettings(uint32(cfg.RecvWindow), uint32(cfg.SendWindow))},
		{muxCmdSYN, 1, []byte{MuxPriorityLow}},
		{muxCmdSYN, 3, []byte{MuxPriorityNormal}},
		{muxCmdSYN, 5, []byte{MuxPriorityNormal}},
		{muxCmdSYN, 7, []byte{MuxPriorityHigh}},
		{muxCmdFIN, 5, []byte{}},
		{muxCmdPSH, 7, []byte("hhhh")},
		{muxCmdPSH, 3, []byte("nnnn")},
		{muxCmdPSH, 1, []byte("llll")},
		{muxCmdPSH, 1, []byte("LLLL")},
		{muxCmdFIN, 1, []byte{}},
	}
	for i, w := range want {
		got := readRawMuxFrame(t, raw)
		if got.cmd != w.cmd || got.sid != w.sid || !bytes.Equal(got.payload, w.payload) {
			t.Fatalf("frame %d = %v, want %v", i, got, w)
		}
	}
}

func TestMuxSettingsAdjustEarlyStreams(t *testing.T) {
	c1, raw := net.Pipe()
	defer raw.Close()
	client, err := NewMuxSession(c1, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	go io.Copy(io.Discard, raw)

	st, _ := client.OpenStream(MuxPriorityNormal)
	if _, err := st.Write(make([]byte, 1000)); err != nil {
		t.Fatal(err)
	}
	if err := writeRawMuxFrame(raw, muxCmdSettings, 0, rawMuxSettings(4096, 4096)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "credit adjustment", func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return st.sendCredit == 4096-1000
	})

	late, _ := client.OpenStream(MuxPriorityNormal)
	late.mu.Lock()
	credit := late.sendCredit
	late.mu.Unlock()
	if credit != 4096 {
		t.Fatalf("stream opened after SETTINGS has credit %d, want 4096", credit)
	}
}

func TestMuxProtocolViolationClosesSession(t *testing.T) {
	cases := map[string]func(w io.Writer){
		"frame before settings": func(w io.Writer) {
			writeRawMuxFrame(w, muxCmdSYN, 2, []byte{MuxPriorityNormal})
		},
		"window overrun": func(w io.Writer) {
			writeRawMuxFrame(w, muxCmdSettings, 0, rawMuxSettings(1024, 16))
			writeRawMuxFrame(w, muxCmdSYN, 2, []byte{MuxPriorityNormal})
			writeRawMuxFrame(w, muxCmdPSH, 2, make([]byte, 17))
		},
		"wrong stream parity": func(w io.Writer) {
			writeRawMuxFrame(w, muxCmdSettings, 0, rawMuxSettings(1024, 1024))
			writeRawMuxFrame(w, muxCmdSYN, 3, []byte{MuxPriorityNormal})
		},
	}
	for name, send := range cases {
		t.Run(name, func(t *testing.T) {
			c1, raw := net.Pipe()
			defer raw.Close()
			client, err := NewMuxSession(c1, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			go io.Copy(io.Discard, raw)
			go send(raw)

			done := make(chan error, 1)
			go func() {
				for {
					if _, err := client.AcceptStream(); err != nil {
						done <- err
						return
					}
				}
			}()
			if err := mustFinish(t, "AcceptStream", done); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("AcceptStream = %v, want io.ErrClosedPipe", err)
			}
		})
	}
}

func TestMuxHalfClose(t *testing.T) {
	client, server := newMuxPair(t, nil, nil)

	cs, _ := client.OpenStream(MuxPriorityNormal)
	cs.Write([]byte("hello"))
	ss, _ := server.AcceptStream()
	buf := make([]byte, 2)
	if _, err := io.ReadFull(ss, buf); err != nil || string(buf) != "he" {
		t.Fatalf("read %q, %v", buf, err)
	}
	ss.Write([]byte("world"))

	if err := ss.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ss.Close(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("second Close = %v, want io.ErrClosedPipe", err)
	}
	if _, err := ss.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write after close = %v, want io.ErrClosedPipe", err)
	}

	// buffered inbound data survives the local close
	rest, err := io.ReadAll(io.LimitReader(ss, 3))
	if err != nil || string(rest) != "llo" {
		t.Fatalf("read after close %q, %v", rest, err)
	}
	if _, err := ss.Read(buf); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("read after drain = %v, want io.ErrClosedPipe", err)
	}

	// the peer reads everything written before the close, then EOF
	got, err := io.ReadAll(cs)
	if err != nil || string(got) != "world" {
		t.Fatalf("peer read %q, %v", got, err)
	}
	if _, err := cs.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write after remote close = %v, want io.ErrClosedPipe", err)
	}

	if client.NumStreams() != 1 || server.NumStreams() != 1 {
		t.Fatalf("NumStreams = %d/%d before both sides closed", client.NumStreams(), server.NumStreams())
	}
	cs.Close()
	waitFor(t, "stream removal", func() bool {
		return client.NumStreams() == 0 && server.NumStreams() == 0
	})
}

func TestMuxRemovalWaitsForDrain(t *testing.T) {
	client, server := newMuxPair(t, nil, nil)

	cs, _ := client.OpenStream(MuxPriorityNormal)
	cs.Write([]byte("data"))
	cs.Close()
	ss, _ := server.AcceptStream()
	waitFor(t, "remote close", ss.isRemoteClosed)

	ss.Close()
	time.Sleep(50 * time.Millisecond)
	if server.NumStreams() != 1 {
		t.Fatal("stream removed before its buffered data was drained")
	}
	buf := make([]byte, 16)
	n, err := ss.Read(buf)
	if err != nil || string(buf[:n]) != "data" {
		t.Fatalf("read %q, %v", buf[:n], err)
	}
	if server.NumStreams() != 0 {
		t.Fatal("drained stream still registered on the server")
	}
	waitFor(t, "client removal", func() bool { return client.NumStreams() == 0 })
}

func TestMuxCloseUnblocksWriters(t *testing.T) {
	cfg := DefaultMuxConfig()
	cfg.SendWindow = 1024
	client, server := newMuxPair(t, &cfg, nil)

	for _, remote := range []bool{false, true} {
		st, _ := client.OpenStream(MuxPriorityNormal)
		sst, _ := server.AcceptStream()

		done := make(chan error, 1)
		go func() {
			_, err := st.Write(make([]byte, 64*1024))
			done <- err
		}()
		mustNotFinish(t, "write", done)

		if remote {
			sst.Close()
		} else {
			st.Close()
		}
		if err := mustFinish(t, "blocked write", done); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("remote=%v: write = %v, want io.ErrClosedPipe", remote, err)
		}
	}
}

func TestMuxSessionCloseUnblocks(t *testing.T) {
	cfg := DefaultMuxConfig()
	cfg.SendWindow = 1024
	client, server := newMuxPair(t, &cfg, nil)

	st, _ := client.OpenStream(MuxPriorityNormal)
	server.AcceptStream()

	reads := make(chan error, 1)
	writes := make(chan error, 1)
	accepts := make(chan error, 1)
	go func() {
		_, err := st.Read(make([]byte, 1))
		reads <- err
	}()
	go func() {
		_, err := st.Write(make([]byte, 64*1024))
		writes <- err
	}()
	go func() {
		_, err := client.AcceptStream()
		accepts <- err
	}()
	mustNotFinish(t, "blocked operations", reads)

	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	for name, ch := range map[string]chan error{"read": reads, "write": writes, "accept": accepts} {
		if err := mustFinish(t, name, ch); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("%s = %v, want io.ErrClosedPipe", name, err)
		}
	}

	if _, err := client.OpenStream(MuxPriorityNormal); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("OpenStream after close = %v", err)
	}
	if err := client.Close(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("second Close = %v", err)
	}
	if client.NumStreams() != 0 {
		t.Fatalf("NumStreams after close = %d", client.NumStreams())
	}

	// the peer notices the closed connection
	if _, err := server.AcceptStream(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("peer AcceptStream = %v", err)
	}
}

type stuckWriteConn struct {
	net.Conn
	release chan struct{}
}

func (c *stuckWriteConn) Write(b []byte) (int, error) {
	<-c.release
	return 0, io.ErrClosedPipe
}

func TestMuxCloseDoesNotWaitForBlockedWrite(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c2.Close()
	conn := &stuckWriteConn{Conn: c1, release: make(chan struct{})}
	defer close(conn.release)

	sess, err := NewMuxSession(conn, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := sess.OpenStream(MuxPriorityNormal)
	st.Write([]byte("queued behind a stuck conn"))

	done := make(chan error, 1)
	go func() { done <- sess.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close blocked on the underlying connection")
	}
	if _, err := st.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write after session close = %v", err)
	}
}

func TestMuxReadDeadline(t *testing.T) {
	client, server := newMuxPair(t, nil, nil)
	st, _ := client.OpenStream(MuxPriorityNormal)
	sst, _ := server.AcceptStream()

	st.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	start := time.Now()
	_, err := st.Read(make([]byte, 1))
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("read = %v, want a timeout net.Error", err)
	}
	if _, ok := err.(net.Error); !ok {
		t.Fatalf("read error %T does not implement net.Error directly", err)
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Fatal("read returned before the deadline")
	}

	st.SetReadDeadline(time.Time{})
	sst.Write([]byte("ok"))
	buf := make([]byte, 2)
	if _, err := io.ReadFull(st, buf); err != nil || string(buf) != "ok" {
		t.Fatalf("read after clearing deadline %q, %v", buf, err)
	}
}

func TestMuxSnmp(t *testing.T) {
	client, server := newMuxPair(t, nil, nil)
	DefaultSnmp.Reset()

	cs, _ := client.OpenStream(MuxPriorityNormal)
	ss, _ := server.AcceptStream()
	cs.Write(make([]byte, 3000))
	ss.Write(make([]byte, 1000))
	io.ReadFull(ss, make([]byte, 3000))
	io.ReadFull(cs, make([]byte, 1000))
	cs.Close()
	ss.Close()
	waitFor(t, "stream removal", func() bool {
		return client.NumStreams() == 0 && server.NumStreams() == 0
	})

	snmp := DefaultSnmp.Copy()
	if snmp.MuxBytesSent != 4000 || snmp.MuxBytesReceived != 4000 {
		t.Fatalf("mux bytes sent/received = %d/%d, want 4000/4000", snmp.MuxBytesSent, snmp.MuxBytesReceived)
	}
	if snmp.MuxStreamsOpened != 2 || snmp.MuxStreamsClosed != 2 {
		t.Fatalf("mux streams opened/closed = %d/%d, want 2/2", snmp.MuxStreamsOpened, snmp.MuxStreamsClosed)
	}
	if snmp.MuxFramesSent == 0 || snmp.MuxFramesReceived == 0 {
		t.Fatalf("mux frames sent/received = %d/%d", snmp.MuxFramesSent, snmp.MuxFramesReceived)
	}

	header := DefaultSnmp.Header()
	values := DefaultSnmp.ToSlice()
	if len(header) != len(values) {
		t.Fatalf("Header has %d fields, ToSlice has %d", len(header), len(values))
	}
	for i, name := range []string{"MuxStreamsOpened", "MuxStreamsClosed", "MuxFramesSent", "MuxFramesReceived", "MuxBytesSent", "MuxBytesReceived"} {
		if header[len(header)-6+i] != name {
			t.Fatalf("header %q missing", name)
		}
	}

	DefaultSnmp.Reset()
	if snmp := DefaultSnmp.Copy(); snmp.MuxBytesSent != 0 || snmp.MuxStreamsOpened != 0 || snmp.MuxFramesReceived != 0 {
		t.Fatal("Reset did not clear mux counters")
	}
}

func BenchmarkMuxThroughput(b *testing.B) {
	c1, c2 := net.Pipe()
	client, _ := NewMuxSession(c1, nil)
	server, _ := NewMuxSession(c2, serverMuxConfig())
	defer client.Close()
	defer server.Close()

	st, _ := client.OpenStream(MuxPriorityNormal)
	sst, _ := server.AcceptStream()
	go io.Copy(io.Discard, sst)

	buf := make([]byte, 64*1024)
	b.SetBytes(int64(len(buf)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.Write(buf); err != nil {
			b.Fatal(err)
		}
	}
}
