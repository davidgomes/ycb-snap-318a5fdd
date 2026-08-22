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
	"encoding/binary"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// MuxStream is one ordered sub-stream within a MuxSession.
type MuxStream struct {
	st *muxStream
}

type muxStream struct {
	sess     *MuxSession
	id       uint32
	priority uint8

	mu sync.Mutex

	recvBuf      []byte
	numRead      uint32
	readWaiting  bool
	readCh       chan struct{}
	readDeadline atomic.Value

	localClosed  bool
	remoteClosed bool
	sessionDead  bool

	numWritten   uint32
	peerConsumed uint32
	peerWindow   uint32
	sendCh       chan struct{}
}

func newMuxStream(sess *MuxSession, id uint32, priority uint8) *muxStream {
	st := &muxStream{
		sess:       sess,
		id:         id,
		priority:   priority,
		readCh:     make(chan struct{}, 1),
		sendCh:     make(chan struct{}, 1),
		peerWindow: uint32(sess.cfg.SendWindow),
	}
	return st
}

// ID returns the stream identifier shared by both peers.
func (s *MuxStream) ID() uint32 {
	return s.st.id
}

// Read reads buffered data from the stream.
func (s *MuxStream) Read(p []byte) (int, error) {
	return s.st.read(p)
}

// Write sends data on the stream, blocking until all bytes are accepted.
func (s *MuxStream) Write(p []byte) (int, error) {
	return s.st.write(p)
}

// Close half-closes the local write side; buffered inbound data remains readable.
func (s *MuxStream) Close() error {
	return s.st.closeLocal()
}

// SetReadDeadline sets the deadline for Read.
func (s *MuxStream) SetReadDeadline(t time.Time) error {
	s.st.readDeadline.Store(t)
	return nil
}

func (st *muxStream) read(p []byte) (int, error) {
	for {
		st.mu.Lock()
		if st.sessionDead {
			st.mu.Unlock()
			return 0, io.ErrClosedPipe
		}
		if len(st.recvBuf) > 0 {
			n := copy(p, st.recvBuf)
			st.recvBuf = st.recvBuf[n:]
			st.numRead += uint32(n)
			st.mu.Unlock()
			st.maybeSendWindowUpdate()
			st.sess.maybeRemoveStream(st)
			return n, nil
		}
		if st.remoteClosed {
			st.mu.Unlock()
			st.sess.maybeRemoveStream(st)
			return 0, io.EOF
		}
		deadline, _ := st.readDeadline.Load().(time.Time)
		st.readWaiting = true
		st.mu.Unlock()

		if !deadline.IsZero() {
			timer := time.NewTimer(time.Until(deadline))
			select {
			case <-st.readCh:
				timer.Stop()
			case <-st.sess.die:
				timer.Stop()
				return 0, io.ErrClosedPipe
			case <-timer.C:
				return 0, muxErrTimeout
			}
		} else {
			select {
			case <-st.readCh:
			case <-st.sess.die:
				return 0, io.ErrClosedPipe
			}
		}
	}
}

func (st *muxStream) write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	total := 0
	for total < len(p) {
		st.mu.Lock()
		if st.sessionDead || st.localClosed {
			st.mu.Unlock()
			if total > 0 {
				return total, io.ErrClosedPipe
			}
			return 0, io.ErrClosedPipe
		}
		avail := st.sendAvailableLocked()
		if avail <= 0 {
			st.mu.Unlock()
			select {
			case <-st.sendCh:
			case <-st.sess.die:
				if total > 0 {
					return total, io.ErrClosedPipe
				}
				return 0, io.ErrClosedPipe
			}
			continue
		}
		chunk := len(p) - total
		maxFrame := st.sess.cfg.MaxFrameSize
		if chunk > maxFrame {
			chunk = maxFrame
		}
		if chunk > avail {
			chunk = avail
		}
		data := p[total : total+chunk]
		st.numWritten += uint32(chunk)
		st.mu.Unlock()

		done := make(chan error, 1)
		req := muxWriteReq{
			priority: st.priority,
			frame: muxFrame{
				cmd:      muxCmdData,
				sid:      st.id,
				priority: st.priority,
				data:     append([]byte(nil), data...),
			},
			done: done,
		}
		if err := st.sess.enqueue(req); err != nil {
			st.mu.Lock()
			st.numWritten -= uint32(chunk)
			st.mu.Unlock()
			if total > 0 {
				return total, err
			}
			return 0, err
		}
		if err := <-done; err != nil {
			st.mu.Lock()
			st.numWritten -= uint32(chunk)
			st.mu.Unlock()
			if total > 0 {
				return total, err
			}
			return 0, err
		}
		total += chunk
	}
	return total, nil
}

func (st *muxStream) closeLocal() error {
	st.mu.Lock()
	if st.sessionDead {
		st.mu.Unlock()
		return io.ErrClosedPipe
	}
	if st.localClosed {
		st.mu.Unlock()
		return nil
	}
	st.localClosed = true
	st.mu.Unlock()

	err := st.sess.writeControl(muxFrame{cmd: muxCmdClose, sid: st.id})
	st.wakeupWriters()
	st.sess.maybeRemoveStream(st)
	return err
}

func (st *muxStream) remoteClose() {
	st.mu.Lock()
	st.remoteClosed = true
	st.mu.Unlock()
	st.wakeupReader()
	st.wakeupWriters()
	st.sess.maybeRemoveStream(st)
}

func (st *muxStream) sessionClosed() {
	st.mu.Lock()
	st.sessionDead = true
	st.mu.Unlock()
	st.wakeupReader()
	st.wakeupWriters()
}

func (st *muxStream) push(data []byte) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.sessionDead {
		return false
	}
	buffered := len(st.recvBuf) + len(data)
	if buffered > st.sess.cfg.RecvWindow {
		return false
	}
	st.recvBuf = append(st.recvBuf, data...)
	st.wakeupReaderLocked()
	return true
}

func (st *muxStream) updateSendWindow(consumed, window uint32) {
	st.mu.Lock()
	st.peerConsumed = consumed
	st.peerWindow = window
	st.mu.Unlock()
	st.wakeupWriters()
}

func (st *muxStream) sendAvailableLocked() int {
	inflight := int(st.numWritten - st.peerConsumed)
	avail := int(st.peerWindow) - inflight
	if avail < 0 {
		return 0
	}
	return avail
}

func (st *muxStream) sendInitialWindowUpdate() {
	payload := make([]byte, muxWindowSize)
	binary.LittleEndian.PutUint32(payload[0:4], 0)
	binary.LittleEndian.PutUint32(payload[4:8], uint32(st.sess.cfg.RecvWindow))
	_ = st.sess.writeControl(muxFrame{cmd: muxCmdWindow, sid: st.id, data: payload})
}

func (st *muxStream) maybeSendWindowUpdate() {
	st.mu.Lock()
	threshold := st.sess.cfg.RecvWindow / 2
	if threshold <= 0 {
		threshold = 1
	}
	if int(st.numRead) < threshold {
		st.mu.Unlock()
		return
	}
	consumed := st.numRead
	st.mu.Unlock()

	payload := make([]byte, muxWindowSize)
	binary.LittleEndian.PutUint32(payload[0:4], consumed)
	binary.LittleEndian.PutUint32(payload[4:8], uint32(st.sess.cfg.RecvWindow))
	_ = st.sess.writeControl(muxFrame{cmd: muxCmdWindow, sid: st.id, data: payload})
}

func (st *muxStream) wakeupReader() {
	st.mu.Lock()
	st.wakeupReaderLocked()
	st.mu.Unlock()
}

func (st *muxStream) wakeupReaderLocked() {
	if st.readWaiting {
		select {
		case st.readCh <- struct{}{}:
		default:
		}
	}
}

func (st *muxStream) wakeupWriters() {
	select {
	case st.sendCh <- struct{}{}:
	default:
	}
}
