package kcp

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func newMuxPair(t *testing.T, window int) (*MuxSession, *MuxSession) {
	a, b := net.Pipe()
	cc := DefaultMuxConfig()
	cc.SendWindow, cc.RecvWindow, cc.MaxFrameSize = window, window, 1024
	sc := cc
	sc.Side = MuxSideServer
	c, err := NewMuxSession(a, &cc)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewMuxSession(b, &sc)
	if err != nil {
		t.Fatal(err)
	}
	return c, s
}

func TestMuxEcho(t *testing.T) {
	c, s := newMuxPair(t, 4096)
	defer c.Close()
	defer s.Close()
	payload := bytes.Repeat([]byte("kcp-mux"), 10000)
	for i := 0; i < 3; i++ {
		cs, err := c.OpenStream(MuxPriorityNormal)
		if err != nil {
			t.Fatal(err)
		}
		if cs.ID() != uint32(2*i+1) {
			t.Fatalf("id %d", cs.ID())
		}
		done := make(chan error, 1)
		go func() {
			_, err := cs.Write(payload)
			if err == nil {
				err = cs.Close()
			}
			done <- err
		}()
		ss, err := s.AcceptStream()
		if err != nil || ss.ID() != cs.ID() {
			t.Fatal(err)
		}
		got, err := io.ReadAll(ss)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("mismatch %v %d", err, len(got))
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		ss.Close()
	}
	so, _ := s.OpenStream(MuxPriorityHigh)
	if so.ID() != 2 {
		t.Fatalf("server id %d", so.ID())
	}
}

func TestMuxBlockedStreamAndDeadline(t *testing.T) {
	c, s := newMuxPair(t, 1024)
	defer s.Close()
	blocked, _ := c.OpenStream(MuxPriorityLow)
	wErr := make(chan error, 1)
	go func() { _, err := blocked.Write(make([]byte, 10000)); wErr <- err }()
	bs, _ := s.AcceptStream()
	other, _ := c.OpenStream(MuxPriorityHigh)
	other.Write([]byte("hi"))
	os, _ := s.AcceptStream()
	buf := make([]byte, 2)
	if _, err := io.ReadFull(os, buf); err != nil || string(buf) != "hi" {
		t.Fatal(err)
	}
	os.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	if _, err := os.Read(buf); err == nil || !err.(net.Error).Timeout() {
		t.Fatal("expected timeout", err)
	}
	_ = bs
	start := time.Now()
	c.Close()
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("close blocked")
	}
	if err := <-wErr; err != io.ErrClosedPipe {
		t.Fatal(err)
	}
}
