"""Multiplex many bidirectional channels over one tube.

Frames on the underlying tube are length-prefixed and tagged with a
channel id so several independent streams can share a single connection.
Each :class:`MuxChannel` is a :class:`~pwnlib.tubes.tube.tube`.
"""
import select
import struct
import threading
from collections import deque

from pwnlib import atexit
from pwnlib.timeout import maximum as _TIMEOUT_MAXIMUM
from pwnlib.tubes.buffer import Buffer
from pwnlib.tubes.tube import tube

__all__ = ['TubeMultiplexer', 'MuxChannel']

_HEADER = struct.Struct('!BHI')

_T_OPEN = 1
_T_ACK = 2
_T_DATA = 3
_T_EOF = 4
_T_CLOSE = 5
_T_PAUSE = 6
_T_RESUME = 7
_T_SHUTDOWN = 8
_T_CLOSE_ACK = 9
_T_EOF_ACK = 10

_MAX_PAYLOAD = 16 * 1024 * 1024

_ST_OPENING = 'opening'
_ST_OPEN = 'open'
_ST_FAILED = 'failed'


def _pack(ftype, channel_id, payload=b''):
    if not isinstance(payload, (bytes, bytearray)):
        payload = bytes(payload)
    return _HEADER.pack(ftype, channel_id, len(payload)) + payload


class TubeMultiplexer(object):
    """Share one tube across independent bidirectional channels.

    Arguments:
        underlying(tube): Tube that carries framed bytes.
        max_channels(int): Maximum number of channels alive at once.
            Must be in ``[1, 65535]``.
        high_water_mark(int): Pause the remote sender when a channel's
            receive buffer reaches this size.
        low_water_mark(int): Resume the remote sender once the receive
            buffer drains to this size. Must be ``<= high_water_mark``.
    """

    def __init__(self, underlying, max_channels=256, high_water_mark=1048576,
                 low_water_mark=262144):
        if not isinstance(underlying, tube):
            raise TypeError('underlying must be a tube, got %s' % type(underlying).__name__)
        if isinstance(max_channels, bool) or not isinstance(max_channels, int) or not 1 <= max_channels <= 65535:
            raise ValueError('max_channels must be an integer in [1, 65535]')
        if low_water_mark > high_water_mark:
            raise ValueError('low_water_mark cannot exceed high_water_mark')

        self._underlying = underlying
        self._max_channels = max_channels
        self._high_water_mark = high_water_mark
        self._low_water_mark = low_water_mark

        self._lock = threading.Lock()
        self._cv = threading.Condition(self._lock)
        self._channels = {}
        self._reserved = set()
        self._pending = deque()
        self._next_id = 1
        self._closed = False

        self._q_lock = threading.Lock()
        self._q_cv = threading.Condition(self._q_lock)
        self._queue = deque()
        self._writer_stop = False

        self._reader = threading.Thread(target=self._reader_loop, name='mux-reader')
        self._reader.daemon = True
        self._writer = threading.Thread(target=self._writer_loop, name='mux-writer')
        self._writer.daemon = True
        self._reader.start()
        self._writer.start()

    @property
    def channels(self):
        """Map of ``channel_id`` to :class:`MuxChannel`."""
        return self._channels

    @property
    def high_water_mark(self):
        return self._high_water_mark

    @property
    def low_water_mark(self):
        return self._low_water_mark

    def open_channel(self, channel_id=None, timeout=None):
        """Open a channel and wait until the remote end acknowledges it.

        Arguments:
            channel_id(int): Channel id in ``[1, 65535]``. Allocated
                automatically when ``None``.
            timeout(float): Seconds to wait for acknowledgement. ``None``
                waits indefinitely.

        Raises:
            TypeError: ``channel_id`` is not an integer.
            ValueError: The id is out of range, already in use, or the
                multiplexer is at ``max_channels``.
            TimeoutError: The remote end did not acknowledge in time.
            EOFError: The multiplexer is closed.
        """
        with self._lock:
            if self._closed:
                raise EOFError('multiplexer is closed')
            cid = self._allocate_id(channel_id)
            chan = MuxChannel(self, cid)
            chan._state = _ST_OPENING
            self._channels[cid] = chan

        self._enqueue(_pack(_T_OPEN, cid))
        try:
            chan._wait_until_open(timeout)
        except BaseException:
            self._drop_channel(chan)
            if not self._closed:
                self._enqueue(_pack(_T_CLOSE, cid))
            raise
        return chan

    def accept_channel(self, timeout=None):
        """Wait for the remote end to open a channel.

        Arguments:
            timeout(float): Seconds to wait. ``None`` waits indefinitely.

        Returns:
            A :class:`MuxChannel`, or ``None`` if ``timeout`` elapses.

        Raises:
            EOFError: The multiplexer is closed.
        """
        with self._lock:
            if timeout is not None:
                deadline = _now() + float(timeout)
            else:
                deadline = None
            while not self._pending and not self._closed:
                remaining = None if deadline is None else deadline - _now()
                if remaining is not None and remaining <= 0:
                    return None
                self._cv.wait(remaining)
            if self._closed:
                raise EOFError('multiplexer is closed')
            cid = self._pending.popleft()
            self._reserved.discard(cid)
            chan = MuxChannel(self, cid)
            chan._state = _ST_OPEN
            self._channels[cid] = chan
        self._enqueue(_pack(_T_ACK, cid))
        return chan

    def close(self):
        """Close every channel and the underlying tube.

        Idempotent. A thread blocked in :meth:`accept_channel` is woken
        with :class:`EOFError`. The remote multiplexer observes the
        shutdown even when it is idle.
        """
        with self._lock:
            if self._closed:
                return
            self._closed = True
            chans = list(self._channels.values())
            self._channels.clear()
            self._reserved.clear()
            self._pending.clear()
            self._cv.notify_all()
        for chan in chans:
            chan._force_eof()
        self._enqueue(_pack(_T_SHUTDOWN, 0), shutdown=True)

    def _allocate_id(self, channel_id):
        """Pick or validate a channel id. Caller holds ``self._lock``."""
        if channel_id is not None:
            if isinstance(channel_id, bool) or not isinstance(channel_id, int):
                raise TypeError('channel_id must be an integer')
            if not 1 <= channel_id <= 65535:
                raise ValueError('channel_id out of range')
            if channel_id in self._channels or channel_id in self._reserved:
                raise ValueError('channel_id already in use')
        at_capacity = len(self._channels) + len(self._reserved) >= self._max_channels
        if channel_id is None:
            if at_capacity:
                raise ValueError('channel capacity exceeded')
            for _ in range(65535):
                cid = self._next_id
                self._next_id = 1 if cid >= 65535 else cid + 1
                if cid not in self._channels and cid not in self._reserved:
                    return cid
            raise ValueError('channel capacity exceeded')
        if at_capacity:
            raise ValueError('channel capacity exceeded')
        return channel_id

    def _drop_channel(self, chan):
        with self._lock:
            current = self._channels.get(chan.channel_id)
            if current is chan:
                del self._channels[chan.channel_id]

    def _unregister(self, chan):
        self._drop_channel(chan)

    def _enqueue(self, frame, front=False, shutdown=False):
        with self._q_cv:
            if self._writer_stop and not shutdown:
                return
            if front:
                self._queue.appendleft((frame, shutdown))
            else:
                self._queue.append((frame, shutdown))
            if shutdown:
                self._writer_stop = True
            self._q_cv.notify()

    def _writer_loop(self):
        while True:
            with self._q_cv:
                while not self._queue and not self._writer_stop:
                    self._q_cv.wait()
                if not self._queue:
                    break
                frame, shutdown = self._queue.popleft()
            try:
                self._underlying.send(frame)
            except Exception:
                self._shutdown_local()
                break
            if shutdown:
                self._close_underlying()
                break

    def _read_exact(self, size):
        """Read exactly ``size`` bytes from the underlying tube.

        Uses ``recv_raw`` so a forever timeout does not go through
        :meth:`tube.recvn`, which cannot take ``None``.
        """
        buf = b''
        while len(buf) < size:
            if self._closed:
                return None
            # Poll instead of blocking inside recv. A blocking recv holds the
            # socket open, so the peer would not observe underlying.close().
            try:
                fd = self._underlying.fileno()
            except Exception:
                fd = None
            if fd is not None:
                try:
                    readable, _, _ = select.select([fd], [], [], 0.2)
                except (OSError, ValueError):
                    return None
                if not readable:
                    continue
            try:
                chunk = self._underlying.recv_raw(size - len(buf))
            except (EOFError, OSError, ValueError):
                return None
            if chunk is None:
                continue
            if chunk == b'':
                return None
            buf += chunk
        return buf

    def _reader_loop(self):
        try:
            while not self._closed:
                header = self._read_exact(_HEADER.size)
                if not header:
                    break
                ftype, cid, length = _HEADER.unpack(header)
                if length > _MAX_PAYLOAD:
                    break
                payload = b''
                if length:
                    payload = self._read_exact(length)
                    if not payload:
                        break
                if not self._dispatch(ftype, cid, payload):
                    break
        except Exception:
            pass
        self._shutdown_local()

    def _dispatch(self, ftype, cid, payload):
        if ftype == _T_SHUTDOWN:
            return False
        if ftype == _T_OPEN:
            self._on_open(cid)
            return True
        if ftype == _T_ACK:
            self._on_ack(cid)
            return True
        chan = self._channels.get(cid)
        if chan is None:
            return True
        if ftype == _T_DATA:
            chan._deliver(payload)
        elif ftype == _T_EOF:
            chan._peer_eof()
        elif ftype == _T_EOF_ACK:
            chan._peer_ack()
        elif ftype == _T_CLOSE:
            chan._peer_close()
        elif ftype == _T_CLOSE_ACK:
            chan._peer_ack()
        elif ftype == _T_PAUSE:
            chan._peer_pause()
        elif ftype == _T_RESUME:
            chan._peer_resume()
        return True

    def _on_open(self, cid):
        reject = False
        with self._lock:
            if self._closed:
                return
            if (not isinstance(cid, int) or not 1 <= cid <= 65535 or
                    cid in self._channels or cid in self._reserved or
                    len(self._channels) + len(self._reserved) >= self._max_channels):
                reject = True
            else:
                self._reserved.add(cid)
                self._pending.append(cid)
                self._cv.notify()
        if reject:
            self._enqueue(_pack(_T_CLOSE, cid))

    def _on_ack(self, cid):
        chan = self._channels.get(cid)
        if chan is not None:
            chan._ack()

    def _shutdown_local(self):
        with self._lock:
            if self._closed:
                chans = ()
            else:
                self._closed = True
                chans = list(self._channels.values())
                self._channels.clear()
                self._reserved.clear()
                self._pending.clear()
                self._cv.notify_all()
        for chan in chans:
            chan._force_eof()
        with self._q_cv:
            self._writer_stop = True
            self._queue.clear()
            self._q_cv.notify_all()
        self._close_underlying()

    def _close_underlying(self):
        try:
            self._underlying.close()
        except Exception:
            pass


class MuxChannel(tube):
    """One logical stream inside a :class:`TubeMultiplexer`."""

    def __init__(self, mux, channel_id):
        super(MuxChannel, self).__init__()
        # tube() registers an atexit close. Waiting for a peer ack while the
        # interpreter is tearing every tube down deadlocks, so the channel is
        # closed by the multiplexer (or by an explicit close) instead.
        for ident, (func, args, _kwargs, _ctx) in list(atexit._handlers.items()):
            if func == self.close and not args:
                atexit.unregister(ident)
        self._mux = mux
        self._channel_id = channel_id
        self._state = _ST_OPEN
        self._send_closed = False
        self._recv_closed = False
        self._peer_paused = False
        self._we_paused = False
        self._inflight = 0
        self._ack_event = threading.Event()
        self._bytes_sent = 0
        self._bytes_received = 0
        self._frames_sent = 0
        self._frames_received = 0
        self._rx = Buffer()
        self._rx.set_watermarks(mux.high_water_mark, mux.low_water_mark)
        self._lock = threading.Lock()
        self._cv = threading.Condition(self._lock)

    @property
    def channel_id(self):
        return self._channel_id

    @property
    def stats(self):
        """Byte and frame counters for this channel.

        ``frames_sent`` increments once per :meth:`send` call.
        ``frames_received`` increments once per remote data delivery.
        """
        with self._lock:
            return {
                'bytes_sent': self._bytes_sent,
                'bytes_received': self._bytes_received,
                'frames_sent': self._frames_sent,
                'frames_received': self._frames_received,
            }

    def _wait_until_open(self, timeout):
        deadline = None if timeout is None else _now() + float(timeout)
        with self._cv:
            while self._state == _ST_OPENING and not self._mux._closed:
                remaining = None if deadline is None else deadline - _now()
                if remaining is not None and remaining <= 0:
                    break
                self._cv.wait(remaining)
            if self._state == _ST_OPEN:
                return
            if self._mux._closed or self._state == _ST_FAILED:
                self._state = _ST_FAILED
                raise EOFError('multiplexer is closed')
            self._state = _ST_FAILED
        raise TimeoutError('timed out waiting for channel acknowledgement')

    def _ack(self):
        with self._cv:
            if self._state == _ST_OPENING:
                self._state = _ST_OPEN
                self._cv.notify_all()

    def _deliver(self, payload):
        pause = False
        with self._cv:
            if self._recv_closed:
                return
            self._rx.add(payload)
            self._bytes_received += len(payload)
            self._frames_received += 1
            if not self._we_paused and self._rx.over_high_water:
                self._we_paused = True
                pause = True
            self._cv.notify_all()
        if pause:
            self._mux._enqueue(_pack(_T_PAUSE, self._channel_id), front=True)

    def _peer_eof(self):
        with self._cv:
            self._recv_closed = True
            self._cv.notify_all()
        self._ack_event.set()
        if not self._mux._closed:
            self._mux._enqueue(_pack(_T_EOF_ACK, self._channel_id))

    def _peer_ack(self):
        self._ack_event.set()

    def _peer_close(self):
        with self._cv:
            initiated = self._send_closed and self._recv_closed
            self._send_closed = True
            self._recv_closed = True
            if self._state == _ST_OPENING:
                self._state = _ST_FAILED
            self._cv.notify_all()
        self._ack_event.set()
        self._mux._unregister(self)
        if not initiated and not self._mux._closed:
            self._mux._enqueue(_pack(_T_CLOSE_ACK, self._channel_id))

    def _peer_pause(self):
        with self._cv:
            self._peer_paused = True
            self._cv.notify_all()

    def _peer_resume(self):
        with self._cv:
            self._peer_paused = False
            # Bytes still sitting in the peer buffer are at most the low
            # water mark, so further sends are limited to the remaining window.
            self._inflight = min(self._inflight, self._mux.low_water_mark)
            self._cv.notify_all()

    def _force_eof(self):
        with self._cv:
            self._send_closed = True
            self._recv_closed = True
            if self._state == _ST_OPENING:
                self._state = _ST_FAILED
            self._cv.notify_all()
        self._ack_event.set()

    def recv_raw(self, numb):
        if numb is None:
            numb = self.buffer.get_fill_size()
        with self._cv:
            timeout = self.timeout
            if timeout is not None and timeout >= _TIMEOUT_MAXIMUM:
                timeout = None
            deadline = None if timeout is None else _now() + timeout
            while len(self._rx) == 0 and not self._recv_closed:
                remaining = None if deadline is None else deadline - _now()
                if remaining is not None and remaining <= 0:
                    return None
                self._cv.wait(None if remaining is None else max(remaining, 0))
            if len(self._rx) == 0 and self._recv_closed:
                raise EOFError('channel closed')
            data = self._rx.get(numb)
            resume = self._we_paused and self._rx.under_low_water
            if resume:
                self._we_paused = False
        if resume:
            self._mux._enqueue(_pack(_T_RESUME, self._channel_id), front=True)
        return data

    def send_raw(self, data):
        if not isinstance(data, (bytes, bytearray)):
            data = bytes(data)
        with self._cv:
            timeout = self.timeout
            if timeout is not None and timeout >= _TIMEOUT_MAXIMUM:
                timeout = None
            deadline = None if timeout is None else _now() + timeout
            limit = self._mux.high_water_mark
            while not self._send_closed and not self._mux._closed:
                blocked = self._peer_paused or (limit and self._inflight >= limit)
                if not blocked:
                    break
                remaining = None if deadline is None else deadline - _now()
                if remaining is not None and remaining <= 0:
                    raise TimeoutError('timed out waiting for flow control')
                self._cv.wait(None if remaining is None else max(remaining, 0))
            if self._send_closed or self._mux._closed or self._state != _ST_OPEN:
                raise EOFError('channel closed')
            self._bytes_sent += len(data)
            self._frames_sent += 1
            self._inflight += len(data)
            if limit and self._inflight >= limit:
                self._peer_paused = True
        if data:
            self._mux._enqueue(_pack(_T_DATA, self._channel_id, data))

    def settimeout_raw(self, timeout):
        return None

    def connected_raw(self, direction):
        if direction == 'send':
            return not self._send_closed
        if direction == 'recv':
            return not self._recv_closed
        return not (self._send_closed and self._recv_closed)

    def close(self):
        send_close = False
        with self._cv:
            if self._send_closed and self._recv_closed:
                already = True
            else:
                already = False
                self._send_closed = True
                self._recv_closed = True
                if self._state == _ST_OPENING:
                    self._state = _ST_FAILED
                self._cv.notify_all()
                send_close = True
        if already:
            return
        if send_close and not self._mux._closed:
            self._ack_event.clear()
            self._mux._enqueue(_pack(_T_CLOSE, self._channel_id))
            self._ack_event.wait()
        self._mux._unregister(self)

    def shutdown_raw(self, direction):
        send_eof = False
        send_close = False
        with self._cv:
            if self._closed_direction(direction):
                return
            if direction == 'send':
                self._send_closed = True
            else:
                self._recv_closed = True
            if self._send_closed and self._recv_closed:
                send_close = True
            elif direction == 'send':
                send_eof = True
            self._cv.notify_all()
        if send_close:
            if not self._mux._closed:
                self._ack_event.clear()
                self._mux._enqueue(_pack(_T_CLOSE, self._channel_id))
                self._ack_event.wait()
            self._mux._unregister(self)
            return
        if send_eof and not self._mux._closed:
            self._ack_event.clear()
            self._mux._enqueue(_pack(_T_EOF, self._channel_id))
            self._ack_event.wait()

    def _closed_direction(self, direction):
        if direction == 'send':
            return self._send_closed
        return self._recv_closed

    def can_recv_raw(self, timeout):
        with self._cv:
            if len(self._rx) > 0:
                return True
            if self._recv_closed:
                return False
        if not timeout:
            return False
        with self._cv:
            if len(self._rx) == 0 and not self._recv_closed:
                self._cv.wait(timeout)
            return len(self._rx) > 0


def _now():
    import time
    return time.time()
