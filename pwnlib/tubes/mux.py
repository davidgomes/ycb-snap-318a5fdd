"""
Multiplexes several independent, bidirectional logical channels over a
single underlying tube.

Both ends of the underlying tube must be wrapped in a
:class:`TubeMultiplexer`.

Example:

    >>> from pwnlib.tubes.mux import TubeMultiplexer
    >>> from pwnlib.tubes.listen import listen
    >>> from pwnlib.tubes.remote import remote
    >>> l = listen()
    >>> r = remote('localhost', l.lport)
    >>> ma = r.mux()
    >>> mb = l.wait_for_connection().mux()
    >>> ca = ma.open_channel(5, timeout=5)
    >>> cb = mb.accept_channel(timeout=5)
    >>> cb.channel_id
    5
    >>> ca.send(b'hello')
    >>> cb.recvn(5)
    b'hello'
    >>> ma.close(); mb.close()
"""
from __future__ import absolute_import
from __future__ import division

import collections
import struct
import threading
import time

from pwnlib.tubes.buffer import Buffer
from pwnlib.tubes.tube import tube

__all__ = ['TubeMultiplexer', 'MuxChannel']

_HEADER = struct.Struct('>BHI')

_OPEN, _ACK, _NACK, _DATA, _FIN, _CLOSE, _PAUSE, _RESUME, _GOODBYE = range(1, 10)

_MAX_ID = 0xffff


def _check_id(channel_id):
    if isinstance(channel_id, bool) or not isinstance(channel_id, int):
        raise TypeError("channel_id must be an integer")
    if not 1 <= channel_id <= _MAX_ID:
        raise ValueError("channel_id must be in range [1, %d]" % _MAX_ID)


class MuxChannel(tube):
    """A single logical channel of a :class:`TubeMultiplexer`."""

    def __init__(self, mux, channel_id, *a, **kw):
        super(MuxChannel, self).__init__(*a, **kw)
        self._mux = mux
        self._id = channel_id
        self._cond = threading.Condition(mux._lock)
        self._rx = Buffer()
        self._rx.set_watermarks(mux.high_water_mark, mux.low_water_mark)
        self._recv_eof = False
        self._send_eof = False
        self._remote_paused = False   # we asked the remote to stop sending
        self._send_paused = False     # remote asked us to stop sending
        self._stats = {'bytes_sent': 0, 'bytes_received': 0,
                       'frames_sent': 0, 'frames_received': 0}

    @property
    def channel_id(self):
        return self._id

    @property
    def stats(self):
        with self._cond:
            return dict(self._stats)

    # Called by the multiplexer with its lock held.
    def _on_data(self, data):
        self._rx.add(data)
        self._stats['bytes_received'] += len(data)
        self._stats['frames_received'] += 1
        pause = not self._remote_paused and self._rx.over_high_water
        if pause:
            self._remote_paused = True
        self._cond.notify_all()
        return pause

    def _on_eof(self, recv=True, send=True):
        if recv:
            self._recv_eof = True
        if send:
            self._send_eof = True
        self._cond.notify_all()

    def recv_raw(self, numb):
        deadline = time.time() + self.timeout
        with self._cond:
            while not self._rx:
                if self._recv_eof:
                    raise EOFError
                remaining = deadline - time.time()
                if remaining <= 0:
                    return b''
                self._cond.wait(min(remaining, 0.5))
            data = self._rx.get(numb)
            resume = self._remote_paused and self._rx.under_low_water
            if resume:
                self._remote_paused = False
        if resume:
            self._mux._send_frame(_RESUME, self._id)
        return data

    def send_raw(self, data):
        deadline = time.time() + self.timeout
        with self._cond:
            while True:
                if self._send_eof:
                    raise EOFError
                if not self._send_paused:
                    break
                remaining = deadline - time.time()
                if remaining <= 0:
                    raise TimeoutError("channel %d send blocked by flow control" % self._id)
                self._cond.wait(min(remaining, 0.5))
            self._stats['bytes_sent'] += len(data)
            self._stats['frames_sent'] += 1
        self._mux._send_frame(_DATA, self._id, data)

    def can_recv_raw(self, timeout):
        deadline = time.time() + (timeout or 0)
        with self._cond:
            while not self._rx and not self._recv_eof:
                remaining = deadline - time.time()
                if remaining <= 0:
                    return False
                self._cond.wait(remaining)
            return True

    def connected_raw(self, direction):
        with self._cond:
            if direction == 'recv':
                return not self._recv_eof or bool(self._rx)
            if direction == 'send':
                return not self._send_eof
            return (not self._recv_eof or bool(self._rx)) or not self._send_eof

    def shutdown_raw(self, direction):
        with self._cond:
            if direction == 'send':
                if self._send_eof:
                    return
                self._send_eof = True
            else:
                self._recv_eof = True
                self._rx = Buffer()
                self._cond.notify_all()
                return
        self._mux._send_frame(_FIN, self._id)

    def close(self):
        with self._cond:
            already = self._recv_eof and self._send_eof and self._id not in self._mux._channels
            self._on_eof()
            self._rx = Buffer()
            self._mux._channels.pop(self._id, None)
        if not already:
            self._mux._send_frame(_CLOSE, self._id)

    def fileno(self):
        raise NotImplementedError("MuxChannel has no file descriptor")

    def timeout_change(self):
        pass


class TubeMultiplexer(object):
    """Multiplexes logical :class:`MuxChannel` objects over a single tube."""

    def __init__(self, underlying, max_channels=256,
                 high_water_mark=1048576, low_water_mark=262144):
        if not isinstance(underlying, tube):
            raise TypeError("underlying must be a tube")
        if isinstance(max_channels, bool) or not isinstance(max_channels, int) \
                or not 1 <= max_channels <= _MAX_ID:
            raise ValueError("max_channels must be in range [1, %d]" % _MAX_ID)
        if low_water_mark > high_water_mark:
            raise ValueError("low_water_mark must not exceed high_water_mark")

        self._tube = underlying
        self._max_channels = max_channels
        self._high = high_water_mark
        self._low = low_water_mark
        self._lock = threading.RLock()
        self._cond = threading.Condition(self._lock)
        self._send_lock = threading.Lock()
        self._channels = {}
        self._pending = {}             # channel_id -> None | True | False
        self._accept_queue = collections.deque()
        self._closed = False

        self._reader = threading.Thread(target=self._read_loop)
        self._reader.daemon = True
        self._reader.start()

    @property
    def channels(self):
        with self._lock:
            return dict(self._channels)

    @property
    def high_water_mark(self):
        return self._high

    @property
    def low_water_mark(self):
        return self._low

    def _send_frame(self, kind, channel_id, payload=b''):
        frame = _HEADER.pack(kind, channel_id, len(payload)) + payload
        with self._send_lock:
            try:
                self._tube.send(frame)
            except EOFError:
                if kind == _DATA:
                    raise
            except Exception:
                if kind == _DATA:
                    raise EOFError("underlying tube is closed")

    def _read_loop(self):
        t = self._tube
        try:
            while not self._closed:
                header = t.recvn(_HEADER.size, timeout=0.1)
                if not header:
                    continue
                kind, cid, length = _HEADER.unpack(header)
                payload = b''
                if length:
                    payload = t.recvn(length, timeout=tube.maximum)
                    if len(payload) < length:
                        break
                if kind == _GOODBYE:
                    break
                self._dispatch(kind, cid, payload)
        except Exception:
            pass
        self._shutdown(notify_remote=False)

    def _dispatch(self, kind, cid, payload):
        reply = None
        with self._lock:
            chan = self._channels.get(cid)
            if kind == _OPEN:
                if chan is not None or cid in self._pending \
                        or len(self._channels) >= self._max_channels:
                    reply = _NACK
                else:
                    self._channels[cid] = MuxChannel(self, cid)
                    self._accept_queue.append(cid)
                    self._cond.notify_all()
                    reply = _ACK
            elif kind in (_ACK, _NACK):
                if cid in self._pending:
                    self._pending[cid] = (kind == _ACK)
                    self._cond.notify_all()
            elif chan is None:
                return
            elif kind == _DATA:
                if chan._on_data(payload):
                    reply = _PAUSE
            elif kind == _FIN:
                chan._on_eof(recv=True, send=False)
            elif kind == _CLOSE:
                chan._on_eof()
                self._channels.pop(cid, None)
            elif kind == _PAUSE:
                chan._send_paused = True
            elif kind == _RESUME:
                chan._send_paused = False
                chan._cond.notify_all()
        if reply is not None:
            self._send_frame(reply, cid)

    def open_channel(self, channel_id=None, timeout=None):
        """Opens a channel and waits for the remote end to acknowledge it."""
        with self._lock:
            if self._closed:
                raise EOFError("multiplexer is closed")
            if channel_id is None:
                if len(self._channels) + len(self._pending) >= self._max_channels:
                    raise ValueError("maximum number of channels reached")
                channel_id = next(i for i in range(1, _MAX_ID + 1)
                                  if i not in self._channels and i not in self._pending)
            else:
                _check_id(channel_id)
                if channel_id in self._channels or channel_id in self._pending:
                    raise ValueError("channel %d already exists" % channel_id)
                if len(self._channels) + len(self._pending) >= self._max_channels:
                    raise ValueError("maximum number of channels reached")
            self._pending[channel_id] = None

        try:
            self._send_frame(_OPEN, channel_id)
            deadline = None if timeout is None else time.time() + timeout
            with self._lock:
                while self._pending[channel_id] is None:
                    if self._closed:
                        raise EOFError("multiplexer is closed")
                    remaining = 0.5
                    if deadline is not None:
                        remaining = deadline - time.time()
                        if remaining <= 0:
                            raise TimeoutError("channel %d open timed out" % channel_id)
                    self._cond.wait(min(remaining, 0.5))
                if not self._pending[channel_id]:
                    raise ValueError("remote rejected channel %d" % channel_id)
                chan = MuxChannel(self, channel_id)
                self._channels[channel_id] = chan
                return chan
        finally:
            with self._lock:
                self._pending.pop(channel_id, None)

    def accept_channel(self, timeout=None):
        """Waits for the remote end to open a channel.  Returns ``None`` on timeout."""
        deadline = None if timeout is None else time.time() + timeout
        with self._lock:
            while True:
                if self._closed:
                    raise EOFError("multiplexer is closed")
                while self._accept_queue:
                    chan = self._channels.get(self._accept_queue.popleft())
                    if chan is not None:
                        return chan
                remaining = 0.5
                if deadline is not None:
                    remaining = deadline - time.time()
                    if remaining <= 0:
                        return None
                self._cond.wait(min(remaining, 0.5))

    def _shutdown(self, notify_remote):
        with self._lock:
            if self._closed:
                return
            self._closed = True
            for chan in self._channels.values():
                chan._on_eof()
            self._cond.notify_all()
        if notify_remote:
            self._send_frame(_GOODBYE, 0)
        try:
            self._tube.close()
        except Exception:
            pass

    def close(self):
        """Closes all channels and the underlying tube.  Idempotent."""
        self._shutdown(notify_remote=True)

    def __enter__(self):
        return self

    def __exit__(self, *a):
        self.close()
