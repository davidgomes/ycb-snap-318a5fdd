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
	"io"
	"net"
	"testing"
	"time"
)

type muxPipe struct {
	r, w net.Conn
}

func newMuxPipe() *muxPipe {
	c1, c2 := net.Pipe()
	return &muxPipe{r: c1, w: c2}
}

func (p *muxPipe) Close() {
	p.r.Close()
	p.w.Close()
}

func muxTestConfig(side MuxSide) *MuxConfig {
	cfg := DefaultMuxConfig()
	cfg.Side = side
	cfg.MaxFrameSize = 1024
	cfg.SendWindow = 32 * 1024
	cfg.RecvWindow = 32 * 1024
	return &cfg
}

func TestMuxBasic(t *testing.T) {
	p := newMuxPipe()
	defer p.Close()

	client, err := NewMuxSession(p.r, muxTestConfig(MuxSideClient))
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewMuxSession(p.w, muxTestConfig(MuxSideServer))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	defer server.Close()

	cStream, err := client.OpenStream(MuxPriorityNormal)
	if err != nil {
		t.Fatal(err)
	}
	if cStream.ID() != 1 {
		t.Fatalf("client stream id = %d, want 1", cStream.ID())
	}

	done := make(chan *MuxStream, 1)
	go func() {
		st, err := server.AcceptStream()
		if err != nil {
			t.Error(err)
			return
		}
		done <- st
	}()

	sStream := <-done
	if sStream.ID() != 1 {
		t.Fatalf("server accepted id = %d, want 1", sStream.ID())
	}

	msg := []byte("hello mux")
	if _, err := cStream.Write(msg); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(sStream, buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, msg) {
		t.Fatalf("got %q want %q", buf, msg)
	}
}

func TestMuxStreamIDs(t *testing.T) {
	p := newMuxPipe()
	defer p.Close()

	client, _ := NewMuxSession(p.r, muxTestConfig(MuxSideClient))
	server, _ := NewMuxSession(p.w, muxTestConfig(MuxSideServer))
	defer client.Close()
	defer server.Close()

	c1, _ := client.OpenStream(MuxPriorityNormal)
	c2, _ := client.OpenStream(MuxPriorityNormal)
	if c1.ID() != 1 || c2.ID() != 3 {
		t.Fatalf("client ids = %d,%d want 1,3", c1.ID(), c2.ID())
	}

	go func() {
		server.AcceptStream()
		server.AcceptStream()
	}()

	s1, _ := server.OpenStream(MuxPriorityNormal)
	s2, _ := server.OpenStream(MuxPriorityNormal)
	if s1.ID() != 2 || s2.ID() != 4 {
		t.Fatalf("server ids = %d,%d want 2,4", s1.ID(), s2.ID())
	}
}

func TestMuxHalfClose(t *testing.T) {
	p := newMuxPipe()
	defer p.Close()

	client, _ := NewMuxSession(p.r, muxTestConfig(MuxSideClient))
	server, _ := NewMuxSession(p.w, muxTestConfig(MuxSideServer))
	defer client.Close()
	defer server.Close()

	cStream, _ := client.OpenStream(MuxPriorityNormal)
	sStream, err := server.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	if err := cStream.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := cStream.Write([]byte("x")); err != io.ErrClosedPipe {
		t.Fatalf("write after close = %v", err)
	}
	if _, err := sStream.Write([]byte("back")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(cStream, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "back" {
		t.Fatalf("got %q", buf)
	}
	if err := sStream.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMuxReadDeadline(t *testing.T) {
	p := newMuxPipe()
	defer p.Close()

	client, _ := NewMuxSession(p.r, muxTestConfig(MuxSideClient))
	server, _ := NewMuxSession(p.w, muxTestConfig(MuxSideServer))
	defer client.Close()
	defer server.Close()

	cStream, _ := client.OpenStream(MuxPriorityNormal)
	go server.AcceptStream()

	_ = cStream.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	buf := make([]byte, 8)
	_, err := cStream.Read(buf)
	if err == nil {
		t.Fatal("expected timeout")
	}
	if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatalf("expected timeout net.Error, got %T %v", err, err)
	}
}

func TestMuxSessionCloseUnblocks(t *testing.T) {
	p := newMuxPipe()
	defer p.Close()

	client, _ := NewMuxSession(p.r, muxTestConfig(MuxSideClient))
	server, _ := NewMuxSession(p.w, muxTestConfig(MuxSideServer))

	cStream, _ := client.OpenStream(MuxPriorityNormal)
	go server.AcceptStream()

	writeDone := make(chan error, 1)
	go func() {
		_, err := cStream.Write(make([]byte, 1024*1024))
		writeDone <- err
	}()

	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 16)
		_, err := cStream.Read(buf)
		readDone <- err
	}()

	time.Sleep(20 * time.Millisecond)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-writeDone:
		if err != io.ErrClosedPipe {
			t.Fatalf("write err = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("write not unblocked")
	}
	select {
	case err := <-readDone:
		if err != io.ErrClosedPipe {
			t.Fatalf("read err = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("read not unblocked")
	}
	server.Close()
}

func TestMuxFlowControlDoesNotStallOtherStreams(t *testing.T) {
	p := newMuxPipe()
	defer p.Close()

	cfgClient := muxTestConfig(MuxSideClient)
	cfgClient.SendWindow = 4096
	cfgClient.RecvWindow = 4096
	cfgServer := muxTestConfig(MuxSideServer)
	cfgServer.SendWindow = 4096
	cfgServer.RecvWindow = 4096

	client, _ := NewMuxSession(p.r, cfgClient)
	server, _ := NewMuxSession(p.w, cfgServer)
	defer client.Close()
	defer server.Close()

	low, _ := client.OpenStream(MuxPriorityLow)
	high, _ := client.OpenStream(MuxPriorityHigh)

	sLow, _ := server.AcceptStream()
	sHigh, _ := server.AcceptStream()
	if sLow.ID() != low.ID() || sHigh.ID() != high.ID() {
		t.Fatalf("stream id mismatch low=%d/%d high=%d/%d", sLow.ID(), low.ID(), sHigh.ID(), high.ID())
	}

	// Fill low-priority stream window without reading on server side.
	payload := make([]byte, 4096)
	writeStarted := make(chan struct{})
	go func() {
		close(writeStarted)
		_, _ = low.Write(payload)
	}()
	<-writeStarted
	time.Sleep(20 * time.Millisecond)

	// High priority stream should still deliver promptly.
	if _, err := high.Write([]byte("prio")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(sHigh, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "prio" {
		t.Fatalf("got %q", buf)
	}

	// Drain low stream to unblock writer.
	drain := make([]byte, 4096)
	if _, err := io.ReadFull(sLow, drain); err != nil {
		t.Fatal(err)
	}
}

func TestMuxSnmpCounters(t *testing.T) {
	DefaultSnmp.Reset()
	p := newMuxPipe()
	defer p.Close()

	client, _ := NewMuxSession(p.r, muxTestConfig(MuxSideClient))
	server, _ := NewMuxSession(p.w, muxTestConfig(MuxSideServer))
	defer client.Close()
	defer server.Close()

	cStream, _ := client.OpenStream(MuxPriorityNormal)
	go func() {
		st, _ := server.AcceptStream()
		io.Copy(io.Discard, st)
	}()

	data := []byte("snmp-test-payload")
	if _, err := cStream.Write(data); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	s := DefaultSnmp.Copy()
	if s.MuxStreamsOpened < 2 {
		t.Fatalf("MuxStreamsOpened = %d", s.MuxStreamsOpened)
	}
	if s.MuxFramesSent == 0 || s.MuxFramesReceived == 0 {
		t.Fatalf("frames sent/recv = %d/%d", s.MuxFramesSent, s.MuxFramesReceived)
	}
	if s.MuxBytesSent < uint64(len(data)) || s.MuxBytesReceived < uint64(len(data)) {
		t.Fatalf("bytes sent/recv = %d/%d", s.MuxBytesSent, s.MuxBytesReceived)
	}

	hdr := DefaultSnmp.Header()
	if len(hdr) != len(DefaultSnmp.ToSlice()) {
		t.Fatalf("header/slice mismatch")
	}
}

func TestMuxNumStreams(t *testing.T) {
	p := newMuxPipe()
	defer p.Close()

	client, _ := NewMuxSession(p.r, muxTestConfig(MuxSideClient))
	server, _ := NewMuxSession(p.w, muxTestConfig(MuxSideServer))
	defer client.Close()
	defer server.Close()

	if client.NumStreams() != 0 {
		t.Fatalf("initial streams = %d", client.NumStreams())
	}
	st, _ := client.OpenStream(MuxPriorityNormal)
	go server.AcceptStream()
	time.Sleep(20 * time.Millisecond)
	if client.NumStreams() != 1 {
		t.Fatalf("streams after open = %d", client.NumStreams())
	}
	st.Close()
	time.Sleep(20 * time.Millisecond)
}
