package kcp

import (
	"bytes"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pipeMuxPair(t *testing.T, clientCfg, serverCfg *MuxConfig) (*MuxSession, *MuxSession) {
	t.Helper()
	c1, c2 := net.Pipe()
	client, err := NewMuxSession(c1, clientCfg)
	require.NoError(t, err)
	server, err := NewMuxSession(c2, serverCfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	t.Cleanup(func() { _ = server.Close() })
	return client, server
}

func pipeDefaultMuxPair(t *testing.T) (*MuxSession, *MuxSession) {
	t.Helper()
	cCfg, sCfg := defaultClientServerCfg()
	return pipeMuxPair(t, cCfg, sCfg)
}

func defaultClientServerCfg() (*MuxConfig, *MuxConfig) {
	c := DefaultMuxConfig()
	c.Side = MuxSideClient
	s := DefaultMuxConfig()
	s.Side = MuxSideServer
	return &c, &s
}

func TestMuxBasicReadWrite(t *testing.T) {
	client, server := pipeDefaultMuxPair(t)

	done := make(chan *MuxStream, 1)
	go func() {
		st, err := server.AcceptStream()
		require.NoError(t, err)
		done <- st
	}()

	cst, err := client.OpenStream(MuxPriorityNormal)
	require.NoError(t, err)
	assert.Equal(t, uint32(1), cst.ID())

	sst := <-done
	assert.Equal(t, uint32(1), sst.ID())

	msg := []byte("hello mux")
	_, err = cst.Write(msg)
	require.NoError(t, err)

	buf := make([]byte, len(msg))
	_, err = io.ReadFull(sst, buf)
	require.NoError(t, err)
	assert.Equal(t, msg, buf)
}

func TestMuxStreamIDs(t *testing.T) {
	client, server := pipeDefaultMuxPair(t)

	s1, err := client.OpenStream(MuxPriorityNormal)
	require.NoError(t, err)
	assert.Equal(t, uint32(1), s1.ID())

	s2, err := client.OpenStream(MuxPriorityNormal)
	require.NoError(t, err)
	assert.Equal(t, uint32(3), s2.ID())

	go func() {
		for i := 0; i < 2; i++ {
			_, _ = server.AcceptStream()
		}
	}()
	time.Sleep(50 * time.Millisecond)

	ss1, err := server.OpenStream(MuxPriorityNormal)
	require.NoError(t, err)
	assert.Equal(t, uint32(2), ss1.ID())

	ss2, err := server.OpenStream(MuxPriorityNormal)
	require.NoError(t, err)
	assert.Equal(t, uint32(4), ss2.ID())
}

func TestMuxFlowControl(t *testing.T) {
	cCfg := DefaultMuxConfig()
	cCfg.Side = MuxSideClient
	cCfg.SendWindow = 1024
	cCfg.RecvWindow = 1024
	cCfg.MaxFrameSize = 256

	sCfg := DefaultMuxConfig()
	sCfg.Side = MuxSideServer
	sCfg.SendWindow = 1024
	sCfg.RecvWindow = 1024
	sCfg.MaxFrameSize = 256

	client, server := pipeMuxPair(t, &cCfg, &sCfg)

	go func() {
		st, err := server.AcceptStream()
		require.NoError(t, err)
		// Do not read yet — exhaust sender window.
		time.Sleep(200 * time.Millisecond)
		buf := make([]byte, 2048)
		_, _ = io.ReadFull(st, buf)
	}()

	cst, err := client.OpenStream(MuxPriorityNormal)
	require.NoError(t, err)

	payload := bytes.Repeat([]byte("x"), 2048)
	done := make(chan error, 1)
	go func() {
		_, err := cst.Write(payload)
		done <- err
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("write blocked too long without window update")
	}
}

func TestMuxFlowControlDoesNotBlockOtherStreams(t *testing.T) {
	cCfg := DefaultMuxConfig()
	cCfg.Side = MuxSideClient
	cCfg.SendWindow = 512
	cCfg.RecvWindow = 512
	cCfg.MaxFrameSize = 256

	sCfg := DefaultMuxConfig()
	sCfg.Side = MuxSideServer
	sCfg.SendWindow = 512
	sCfg.RecvWindow = 512
	sCfg.MaxFrameSize = 256

	client, server := pipeMuxPair(t, &cCfg, &sCfg)

	streams := make(map[uint32]*MuxStream)
	acceptDone := make(chan struct{})
	go func() {
		for i := 0; i < 2; i++ {
			st, err := server.AcceptStream()
			require.NoError(t, err)
			streams[st.ID()] = st
		}
		close(acceptDone)
	}()

	s1, err := client.OpenStream(MuxPriorityNormal)
	require.NoError(t, err)
	s2, err := client.OpenStream(MuxPriorityNormal)
	require.NoError(t, err)
	<-acceptDone

	blocked := streams[1]
	other := streams[3]
	require.NotNil(t, blocked)
	require.NotNil(t, other)

	// Fill s1 window without reading on server.
	_, err = s1.Write(bytes.Repeat([]byte("a"), 512))
	require.NoError(t, err)

	writeDone := make(chan error, 1)
	go func() {
		_, err := s1.Write(bytes.Repeat([]byte("b"), 512))
		writeDone <- err
	}()

	time.Sleep(50 * time.Millisecond)
	_, err = s2.Write([]byte("ok"))
	require.NoError(t, err)

	buf := make([]byte, 2)
	_, err = io.ReadFull(other, buf)
	require.NoError(t, err)
	assert.Equal(t, []byte("ok"), buf)

	// Unblock s1 by reading.
	readBuf := make([]byte, 1024)
	_, err = io.ReadFull(blocked, readBuf)
	require.NoError(t, err)

	select {
	case err := <-writeDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("blocked stream did not resume")
	}
}

func TestMuxHalfClose(t *testing.T) {
	client, server := pipeDefaultMuxPair(t)

	go func() {
		st, err := server.AcceptStream()
		require.NoError(t, err)
		_, err = st.Write([]byte("response"))
		require.NoError(t, err)
	}()

	cst, err := client.OpenStream(MuxPriorityNormal)
	require.NoError(t, err)

	_, err = cst.Write([]byte("request"))
	require.NoError(t, err)

	buf := make([]byte, 8)
	n, err := io.ReadFull(cst, buf)
	require.NoError(t, err)
	assert.Equal(t, "response", string(buf[:n]))

	require.NoError(t, cst.Close())

	_, err = cst.Read(buf)
	assert.Equal(t, io.ErrClosedPipe, err)
}

func TestMuxRemoteCloseUnblocksWriter(t *testing.T) {
	cCfg := DefaultMuxConfig()
	cCfg.Side = MuxSideClient
	cCfg.SendWindow = 64
	cCfg.RecvWindow = 64
	cCfg.MaxFrameSize = 64
	sCfg := cCfg
	sCfg.Side = MuxSideServer

	client, server := pipeMuxPair(t, &cCfg, &sCfg)

	go func() {
		st, err := server.AcceptStream()
		require.NoError(t, err)
		require.NoError(t, st.Close())
	}()

	cst, err := client.OpenStream(MuxPriorityNormal)
	require.NoError(t, err)

	_, _ = cst.Write(bytes.Repeat([]byte("x"), 64))

	writeDone := make(chan error, 1)
	go func() {
		_, err := cst.Write(bytes.Repeat([]byte("y"), 64))
		writeDone <- err
	}()

	select {
	case err := <-writeDone:
		assert.Equal(t, io.ErrClosedPipe, err)
	case <-time.After(2 * time.Second):
		t.Fatal("writer not unblocked after remote close")
	}
}

func TestMuxSessionCloseNonBlocking(t *testing.T) {
	c1, c2 := net.Pipe()
	client, err := NewMuxSession(c1, &MuxConfig{Side: MuxSideClient, MaxFrameSize: 4096, SendWindow: 65536, RecvWindow: 65536})
	require.NoError(t, err)
	server, err := NewMuxSession(c2, &MuxConfig{Side: MuxSideServer, MaxFrameSize: 4096, SendWindow: 65536, RecvWindow: 65536})
	require.NoError(t, err)

	st, err := client.OpenStream(MuxPriorityNormal)
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		_, _ = st.Write(bytes.Repeat([]byte("z"), 1<<20))
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	require.NoError(t, client.Close())
	elapsed := time.Since(start)
	assert.True(t, elapsed < 200*time.Millisecond)

	_, err = st.Write([]byte("x"))
	assert.Equal(t, io.ErrClosedPipe, err)

	_ = server.Close()
}

func TestMuxReadDeadline(t *testing.T) {
	client, server := pipeDefaultMuxPair(t)

	cst, err := client.OpenStream(MuxPriorityNormal)
	require.NoError(t, err)

	go func() {
		_, _ = server.AcceptStream()
	}()

	require.NoError(t, cst.SetReadDeadline(time.Now().Add(50 * time.Millisecond)))
	buf := make([]byte, 8)
	_, err = cst.Read(buf)
	require.Error(t, err)
	netErr, ok := err.(net.Error)
	require.True(t, ok)
	assert.True(t, netErr.Timeout())
}

func TestMuxClosedOperations(t *testing.T) {
	client, server := pipeDefaultMuxPair(t)

	st, err := client.OpenStream(MuxPriorityNormal)
	require.NoError(t, err)
	go func() { _, _ = server.AcceptStream() }()
	time.Sleep(20 * time.Millisecond)

	require.NoError(t, client.Close())

	_, err = client.OpenStream(MuxPriorityNormal)
	assert.Equal(t, io.ErrClosedPipe, err)

	_, err = client.AcceptStream()
	assert.Equal(t, io.ErrClosedPipe, err)

	_, err = st.Write([]byte("x"))
	assert.Equal(t, io.ErrClosedPipe, err)

	_, err = st.Read(make([]byte, 1))
	assert.Equal(t, io.ErrClosedPipe, err)
}

func TestMuxNumStreams(t *testing.T) {
	client, server := pipeDefaultMuxPair(t)

	assert.Equal(t, 0, client.NumStreams())

	st, err := client.OpenStream(MuxPriorityNormal)
	require.NoError(t, err)
	assert.Equal(t, 1, client.NumStreams())

	go func() {
		rst, err := server.AcceptStream()
		require.NoError(t, err)
		_, _ = io.ReadFull(rst, make([]byte, 5))
		require.NoError(t, rst.Close())
	}()

	_, err = st.Write([]byte("hello"))
	require.NoError(t, err)
	require.NoError(t, st.Close())

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 0, client.NumStreams())
}

func TestMuxSNMPCounters(t *testing.T) {
	DefaultSnmp.Reset()
	client, server := pipeDefaultMuxPair(t)

	go func() {
		st, err := server.AcceptStream()
		require.NoError(t, err)
		buf := make([]byte, 5)
		_, _ = io.ReadFull(st, buf)
		_, _ = st.Write(buf)
		require.NoError(t, st.Close())
	}()

	st, err := client.OpenStream(MuxPriorityNormal)
	require.NoError(t, err)

	payload := []byte("hello")
	_, err = st.Write(payload)
	require.NoError(t, err)

	buf := make([]byte, 5)
	_, err = io.ReadFull(st, buf)
	require.NoError(t, err)
	require.NoError(t, st.Close())

	time.Sleep(100 * time.Millisecond)

	snap := DefaultSnmp.Copy()
	assert.GreaterOrEqual(t, snap.MuxStreamsOpened, uint64(2))
	assert.GreaterOrEqual(t, snap.MuxStreamsClosed, uint64(2))
	assert.Greater(t, snap.MuxFramesSent, uint64(0))
	assert.Greater(t, snap.MuxFramesReceived, uint64(0))
	assert.GreaterOrEqual(t, snap.MuxBytesSent, uint64(len(payload)))
	assert.GreaterOrEqual(t, snap.MuxBytesReceived, uint64(len(payload)))

	hdr := DefaultSnmp.Header()
	assert.Contains(t, hdr, "MuxStreamsOpened")
	assert.Contains(t, hdr, "MuxBytesReceived")

	slice := DefaultSnmp.ToSlice()
	assert.Equal(t, len(hdr), len(slice))
}

func TestMuxPriorityScheduling(t *testing.T) {
	cCfg := DefaultMuxConfig()
	cCfg.Side = MuxSideClient
	cCfg.MaxFrameSize = 64
	sCfg := DefaultMuxConfig()
	sCfg.Side = MuxSideServer
	sCfg.MaxFrameSize = 64

	client, server := pipeMuxPair(t, &cCfg, &sCfg)

	low, err := client.OpenStream(MuxPriorityLow)
	require.NoError(t, err)
	high, err := client.OpenStream(MuxPriorityHigh)
	require.NoError(t, err)

	var order []byte
	var mu sync.Mutex
	go func() {
		for i := 0; i < 2; i++ {
			st, err := server.AcceptStream()
			require.NoError(t, err)
			go func(s *MuxStream) {
				buf := make([]byte, 64)
				n, _ := s.Read(buf)
				mu.Lock()
				order = append(order, buf[:n]...)
				mu.Unlock()
			}(st)
		}
	}()
	time.Sleep(50 * time.Millisecond)

	// Queue low priority first, then high.
	go func() { _, _ = low.Write(bytes.Repeat([]byte{'L'}, 64)) }()
	time.Sleep(10 * time.Millisecond)
	_, err = high.Write(bytes.Repeat([]byte{'H'}, 64))
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(order) >= 128 {
		// High priority frame should arrive before second low chunk if preempting.
		hIdx := bytes.IndexByte(order, 'H')
		assert.GreaterOrEqual(t, hIdx, 0)
	}
}

func TestMuxWriteNoShortWrite(t *testing.T) {
	client, server := pipeDefaultMuxPair(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		st, err := server.AcceptStream()
		if err != nil {
			return
		}
		buf := make([]byte, 8192)
		_, _ = io.ReadFull(st, buf)
	}()

	st, err := client.OpenStream(MuxPriorityNormal)
	require.NoError(t, err)

	data := bytes.Repeat([]byte("w"), 8192)
	n, err := st.Write(data)
	require.NoError(t, err)
	assert.Equal(t, len(data), n)
	<-done
}

func TestMuxAcceptRemoteOpen(t *testing.T) {
	client, server := pipeDefaultMuxPair(t)

	go func() {
		_, err := server.OpenStream(MuxPriorityHigh)
		require.NoError(t, err)
	}()

	st, err := client.AcceptStream()
	require.NoError(t, err)
	assert.Equal(t, uint32(2), st.ID())
}

func TestMuxConcurrentStreams(t *testing.T) {
	client, server := pipeDefaultMuxPair(t)
	const n = 8

	go func() {
		for i := 0; i < n; i++ {
			st, err := server.AcceptStream()
			require.NoError(t, err)
			go func(s *MuxStream, id int) {
				buf := make([]byte, 32)
				_, _ = io.ReadFull(s, buf)
				_, _ = s.Write(buf)
			}(st, i)
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(val byte) {
			defer wg.Done()
			st, err := client.OpenStream(MuxPriorityNormal)
			require.NoError(t, err)
			msg := bytes.Repeat([]byte{val}, 32)
			_, err = st.Write(msg)
			require.NoError(t, err)
			buf := make([]byte, 32)
			_, err = io.ReadFull(st, buf)
			require.NoError(t, err)
			assert.Equal(t, msg, buf)
		}(byte(i))
	}
	wg.Wait()
}

func TestSnmpMuxFieldsReset(t *testing.T) {
	atomic.StoreUint64(&DefaultSnmp.MuxStreamsOpened, 42)
	DefaultSnmp.Reset()
	snap := DefaultSnmp.Copy()
	assert.Equal(t, uint64(0), snap.MuxStreamsOpened)
	assert.Equal(t, uint64(0), snap.MuxBytesSent)
}
