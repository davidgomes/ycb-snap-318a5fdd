"""Multiplex independent bidirectional channels over a single tube.

Two :class:`TubeMultiplexer` instances speaking this module's framing
protocol can share one underlying connection while keeping each
logical channel's data, flow control, and lifetime isolated.
"""
import collections
import socket
import struct
import threading
import time

from pwnlib import atexit
from pwnlib.context import context
from pwnlib.timeout import Timeout
from pwnlib.tubes.buffer import Buffer
from pwnlib.tubes.tube import tube


# Wire format: big-endian [type:u8][channel_id:u16][length:u32][payload]
_HEADER = struct.Struct('!BHI')

_FRAME_OPEN = 1
_FRAME_ACK = 2
_FRAME_DATA = 3
_FRAME_CLOSE = 4
_FRAME_SHUTDOWN = 5
_FRAME_PAUSE = 6
_FRAME_RESUME = 7

_MIN_CHANNEL_ID = 1
_MAX_CHANNEL_ID = 65535


def _is_int(value):
    return isinstance(value, int) and not isinstance(value, bool)


class TubeMultiplexer(object):
    """Multiplexer that carries many :class:`MuxChannel` objects on one tube.

    Arguments:
        underlying(tube): Tube that carries framed mux traffic.
        max_channels(int): Maximum number of concurrently open channels
            (default 256). Must be in ``[1, 65535]``.
        high_water_mark(int): Receive-buffer size at which the remote
            sender for a channel is paused (default 1048576).
        low_water_mark(int): Receive-buffer size at or below which a
            paused remote sender is resumed (default 262144).

    Raises:
        TypeError: If *underlying* is not a :class:`~pwnlib.tubes.tube.tube`.
        ValueError: If *max_channels* is outside ``[1, 65535]`` or
            *low_water_mark* is greater than *high_water_mark*.

    Examples:

        >>> t1, t2 = _connected_tubes()
        >>> m1, m2 = t1.mux(), t2.mux()
        >>> m1.high_water_mark, m1.low_water_mark
        (1048576, 262144)
        >>> isinstance(m1.channels, dict)
        True
        >>> a = m1.open_channel(1, timeout=2)
        >>> b = m2.accept_channel(timeout=2)
        >>> isinstance(a, MuxChannel) and isinstance(a, tube)
        True
        >>> a.channel_id, b.channel_id
        (1, 1)
        >>> a.send(b'hello')
        >>> b.recv()
        b'hello'
        >>> b.send(b'world')
        >>> a.recv()
        b'world'
        >>> a.stats == {'bytes_sent': 5, 'bytes_received': 5,
        ...             'frames_sent': 1, 'frames_received': 1}
        True
        >>> m1.close()
        >>> m2.close()
    """

    def __init__(self, underlying, max_channels=256,
                 high_water_mark=1048576, low_water_mark=262144):
        if not isinstance(underlying, tube):
            raise TypeError('underlying must be a pwnlib tube')
        if not _is_int(max_channels) or not (_MIN_CHANNEL_ID <= max_channels <= _MAX_CHANNEL_ID):
            raise ValueError('max_channels must be in [1, 65535]')
        if low_water_mark > high_water_mark:
            raise ValueError('low_water_mark cannot exceed high_water_mark')

        self._underlying = underlying
        self._max_channels = max_channels
        self._high_water_mark = high_water_mark
        self._low_water_mark = low_water_mark
        self._channels = {}
        self._closed = False
        self._lock = threading.RLock()
        self._send_lock = threading.Lock()
        self._accept_cond = threading.Condition(self._lock)
        self._accept_queue = collections.deque()
        self._reader = context.Thread(target=self._reader_loop,
                                      name='pwnlib-mux-reader')
        self._reader.daemon = True
        self._reader.start()
        atexit.register(self.close)

    @property
    def channels(self):
        """dict: Mapping of ``channel_id`` to :class:`MuxChannel`."""
        return self._channels

    @property
    def high_water_mark(self):
        """int: Receive-side high water mark in bytes."""
        return self._high_water_mark

    @property
    def low_water_mark(self):
        """int: Receive-side low water mark in bytes."""
        return self._low_water_mark

    def _capacity_used(self):
        return sum(1 for ch in self._channels.values() if not ch._fully_closed)

    def _allocate_id(self):
        for cid in range(_MIN_CHANNEL_ID, _MAX_CHANNEL_ID + 1):
            ch = self._channels.get(cid)
            if ch is None or ch._fully_closed:
                return cid
        raise ValueError('no free channel IDs')

    def _validate_channel_id(self, channel_id):
        if not _is_int(channel_id):
            raise TypeError('channel_id must be an integer')
        if not (_MIN_CHANNEL_ID <= channel_id <= _MAX_CHANNEL_ID):
            raise ValueError('channel_id must be in [1, 65535]')
        existing = self._channels.get(channel_id)
        if existing is not None and not existing._fully_closed:
            raise ValueError('channel_id %d is already in use' % channel_id)

    def open_channel(self, channel_id=None, timeout=None):
        """open_channel(channel_id=None, timeout=None) -> MuxChannel

        Open a channel and wait for the remote peer to acknowledge it.

        If *channel_id* is :const:`None`, a free identifier in
        ``[1, 65535]`` is allocated.

        Arguments:
            channel_id(int): Requested channel identifier, or :const:`None`.
            timeout(float): Seconds to wait for the remote acknowledgement.
                :const:`None` waits indefinitely.

        Raises:
            TypeError: If *channel_id* is not an integer.
            ValueError: If *channel_id* is out of range, already in use,
                or the multiplexer is at *max_channels*.
            TimeoutError: If no acknowledgement arrives before *timeout*.
            EOFError: If the multiplexer is already closed.

        Examples:

            >>> t1, t2 = _connected_tubes()
            >>> m1, m2 = t1.mux(), t2.mux()
            >>> TubeMultiplexer('not a tube')
            Traceback (most recent call last):
                ...
            TypeError: underlying must be a pwnlib tube
            >>> TubeMultiplexer(t1, max_channels=0)
            Traceback (most recent call last):
                ...
            ValueError: max_channels must be in [1, 65535]
            >>> TubeMultiplexer(t1, high_water_mark=1, low_water_mark=2)
            Traceback (most recent call last):
                ...
            ValueError: low_water_mark cannot exceed high_water_mark
            >>> m1.open_channel('1')
            Traceback (most recent call last):
                ...
            TypeError: channel_id must be an integer
            >>> m1.open_channel(0)
            Traceback (most recent call last):
                ...
            ValueError: channel_id must be in [1, 65535]
            >>> ch = m1.open_channel(7, timeout=2)
            >>> peer = m2.accept_channel(timeout=2)
            >>> ch.channel_id, peer.channel_id
            (7, 7)
            >>> m1.open_channel(7)
            Traceback (most recent call last):
                ...
            ValueError: channel_id 7 is already in use
            >>> m1.close()
            >>> m1.open_channel(8)
            Traceback (most recent call last):
                ...
            EOFError
            >>> m2.close()
        """
        if self._closed:
            raise EOFError

        with self._lock:
            if self._closed:
                raise EOFError
            if channel_id is None:
                if self._capacity_used() >= self._max_channels:
                    raise ValueError('maximum number of channels (%d) reached' % self._max_channels)
                channel_id = self._allocate_id()
            else:
                self._validate_channel_id(channel_id)
                if self._capacity_used() >= self._max_channels:
                    raise ValueError('maximum number of channels (%d) reached' % self._max_channels)
            ch = MuxChannel(self, channel_id)
            self._channels[channel_id] = ch

        try:
            self._send_frame(_FRAME_OPEN, channel_id)
        except EOFError:
            ch._force_eof()
            raise

        if timeout is None:
            ch._ready.wait()
        elif not ch._ready.wait(timeout):
            ch._force_eof()
            try:
                self._send_frame(_FRAME_CLOSE, channel_id)
            except EOFError:
                pass
            raise TimeoutError(
                'Timed out waiting for channel %d acknowledgement' % channel_id)

        if self._closed or not ch._acked:
            raise EOFError
        return ch

    def accept_channel(self, timeout=None):
        """accept_channel(timeout=None) -> MuxChannel or None

        Wait for the remote peer to open a channel.

        Arguments:
            timeout(float): Seconds to wait.  :const:`None` waits
                indefinitely.  Returns :const:`None` if *timeout*
                elapses with no incoming channel.

        Raises:
            EOFError: If the multiplexer is closed.

        Examples:

            >>> t1, t2 = _connected_tubes()
            >>> m1, m2 = t1.mux(), t2.mux()
            >>> m1.accept_channel(timeout=0.05) is None
            True
            >>> ch = m2.open_channel(timeout=2)
            >>> got = m1.accept_channel(timeout=2)
            >>> got.channel_id == ch.channel_id
            True
            >>> m1.close()
            >>> m1.accept_channel()
            Traceback (most recent call last):
                ...
            EOFError
            >>> m2.close()
        """
        with self._accept_cond:
            if self._closed:
                raise EOFError
            if timeout is None:
                while not self._closed and not self._accept_queue:
                    self._accept_cond.wait()
            else:
                deadline = Timeout(timeout).timeout
                if deadline < Timeout.maximum:
                    end = time.time() + deadline
                    while not self._closed and not self._accept_queue:
                        remaining = end - time.time()
                        if remaining <= 0:
                            break
                        self._accept_cond.wait(remaining)
                else:
                    while not self._closed and not self._accept_queue:
                        self._accept_cond.wait()
            if self._closed:
                raise EOFError
            if not self._accept_queue:
                return None
            return self._accept_queue.popleft()

    def close(self):
        """close()

        Signal EOF on every channel, close the underlying tube, and
        unblock waiters.  Safe to call more than once.

        The underlying tube is closed so an idle remote reader observes
        EOF promptly.

        Examples:

            >>> t1, t2 = _connected_tubes()
            >>> m1, m2 = t1.mux(), t2.mux()
            >>> err = []
            >>> def waiter():
            ...     try:
            ...         m2.accept_channel()
            ...     except EOFError:
            ...         err.append(True)
            >>> th = context.Thread(target=waiter)
            >>> th.daemon = True
            >>> th.start()
            >>> m2.close()
            >>> th.join(2)
            >>> err
            [True]
            >>> m2.close()
            >>> m1.close()
        """
        with self._lock:
            if self._closed:
                return
            self._closed = True
            channels = list(self._channels.values())
            self._accept_cond.notify_all()

        for ch in channels:
            ch._force_eof()

        try:
            self._underlying.close()
        except Exception:
            pass

        reader = getattr(self, '_reader', None)
        if reader is not None and reader is not threading.current_thread():
            reader.join(timeout=1.0)

    def _send_frame(self, typ, channel_id, payload=b''):
        if not payload:
            payload = b''
        elif not isinstance(payload, (bytes, bytearray)):
            payload = bytes(payload)
        frame = _HEADER.pack(typ, channel_id, len(payload)) + payload
        with self._send_lock:
            if self._closed:
                raise EOFError
            self._underlying.send(frame)

    def _read_exact(self, n):
        if n == 0:
            return b''
        data = self._underlying.recvn(n, timeout=Timeout.forever)
        if not data or len(data) != n:
            raise EOFError
        return data

    def _read_frame(self):
        typ, channel_id, length = _HEADER.unpack(self._read_exact(_HEADER.size))
        payload = self._read_exact(length)
        return typ, channel_id, payload

    def _reader_loop(self):
        try:
            while not self._closed:
                typ, channel_id, payload = self._read_frame()
                self._handle_frame(typ, channel_id, payload)
        except (EOFError, OSError, socket.error):
            pass
        finally:
            self.close()

    def _handle_frame(self, typ, channel_id, payload):
        if typ == _FRAME_OPEN:
            self._handle_open(channel_id)
        elif typ == _FRAME_ACK:
            self._handle_ack(channel_id)
        elif typ == _FRAME_DATA:
            ch = self._channels.get(channel_id)
            if ch is not None:
                ch._deliver(payload)
        elif typ == _FRAME_CLOSE:
            self._handle_peer_close(channel_id)
        elif typ == _FRAME_SHUTDOWN:
            ch = self._channels.get(channel_id)
            if ch is not None:
                ch._on_remote_shutdown()
        elif typ == _FRAME_PAUSE:
            ch = self._channels.get(channel_id)
            if ch is not None:
                ch._on_pause()
        elif typ == _FRAME_RESUME:
            ch = self._channels.get(channel_id)
            if ch is not None:
                ch._on_resume()

    def _handle_open(self, channel_id):
        with self._lock:
            if self._closed:
                return
            existing = self._channels.get(channel_id)
            if existing is not None and not existing._fully_closed:
                if not existing._acked:
                    existing._acked = True
                    existing._ready.set()
                try:
                    self._send_frame(_FRAME_ACK, channel_id)
                except EOFError:
                    pass
                return
            if self._capacity_used() >= self._max_channels:
                try:
                    self._send_frame(_FRAME_CLOSE, channel_id)
                except EOFError:
                    pass
                return
            ch = MuxChannel(self, channel_id)
            ch._acked = True
            self._channels[channel_id] = ch
        try:
            self._send_frame(_FRAME_ACK, channel_id)
        except EOFError:
            ch._force_eof()
            return
        with self._accept_cond:
            self._accept_queue.append(ch)
            self._accept_cond.notify()

    def _handle_ack(self, channel_id):
        ch = self._channels.get(channel_id)
        if ch is None:
            return
        ch._acked = True
        ch._ready.set()

    def _handle_peer_close(self, channel_id):
        ch = self._channels.get(channel_id)
        if ch is None:
            return
        ch._force_eof()
        ch._ready.set()


class MuxChannel(tube):
    """A single multiplexed channel.

    Each instance is a :class:`~pwnlib.tubes.tube.tube`, so ``send``,
    ``recv``, ``shutdown``, and friends work as usual.  Closing this
    channel does not affect siblings on the same multiplexer.

    Examples:

        >>> t1, t2 = _connected_tubes()
        >>> m1, m2 = t1.mux(), t2.mux()
        >>> a = m1.open_channel(1, timeout=2)
        >>> b = m2.accept_channel(timeout=2)
        >>> a.stats == {'bytes_sent': 0, 'bytes_received': 0,
        ...             'frames_sent': 0, 'frames_received': 0}
        True
        >>> a.send(b'xy')
        >>> a.send(b'z')
        >>> b.recvn(3)
        b'xyz'
        >>> a.stats['frames_sent'], b.stats['frames_received']
        (2, 2)
        >>> a.close()
        >>> b.recv()
        Traceback (most recent call last):
            ...
        EOFError
        >>> b.send(b'nope')
        Traceback (most recent call last):
            ...
        EOFError
        >>> a.send(b'nope')
        Traceback (most recent call last):
            ...
        EOFError
        >>> a.connected()
        False
        >>> c = m1.open_channel(2, timeout=2)
        >>> d = m2.accept_channel(timeout=2)
        >>> c.send(b'still-open')
        >>> d.recv()
        b'still-open'
        >>> c.shutdown('send')
        >>> c.send(b'x')
        Traceback (most recent call last):
            ...
        EOFError
        >>> d.send(b'reply')
        >>> c.recv()
        b'reply'
        >>> c.connected('send')
        False
        >>> c.connected('recv')
        True
        >>> m1.close(); m2.close()
    """

    def __init__(self, mux, channel_id, timeout=Timeout.default, level=None):
        super(MuxChannel, self).__init__(timeout=timeout, level=level)
        self._mux = mux
        self._channel_id = channel_id
        self._incoming = Buffer()
        self._stats = {
            'bytes_sent': 0,
            'bytes_received': 0,
            'frames_sent': 0,
            'frames_received': 0,
        }
        self._stats_lock = threading.Lock()
        self._recv_cond = threading.Condition()
        self._flow_cond = threading.Condition()
        self._ready = threading.Event()
        self._acked = False
        self._send_closed = False
        self._recv_closed = False
        self._fully_closed = False
        self._paused = False
        self._peer_paused = False
        self._wm_lock = threading.Lock()
        self.buffer.set_watermarks(high=mux.high_water_mark,
                                   low=mux.low_water_mark)
        self._incoming.set_watermarks(high=mux.high_water_mark,
                                      low=mux.low_water_mark)
        orig_get = self.buffer.get
        def _get(want=float('inf')):
            data = orig_get(want)
            self._maybe_resume()
            return data
        self.buffer.get = _get

    @property
    def channel_id(self):
        """int: Identifier of this channel in ``[1, 65535]``."""
        return self._channel_id

    @property
    def stats(self):
        """dict: ``bytes_sent``, ``bytes_received``, ``frames_sent``,
        ``frames_received``."""
        with self._stats_lock:
            return dict(self._stats)

    def _unread(self):
        return len(self.buffer) + len(self._incoming)

    def _deliver(self, data):
        with self._recv_cond:
            if self._fully_closed:
                return
            self._incoming.add(data)
            with self._stats_lock:
                self._stats['bytes_received'] += len(data)
                self._stats['frames_received'] += 1
            self._recv_cond.notify_all()
        self._maybe_pause()

    def _maybe_pause(self):
        with self._wm_lock:
            if self._peer_paused or self._unread() < self._mux.high_water_mark:
                return
            self._peer_paused = True
        try:
            self._mux._send_frame(_FRAME_PAUSE, self.channel_id)
        except EOFError:
            pass

    def _maybe_resume(self):
        with self._wm_lock:
            if not self._peer_paused or self._unread() > self._mux.low_water_mark:
                return
            self._peer_paused = False
        try:
            self._mux._send_frame(_FRAME_RESUME, self.channel_id)
        except EOFError:
            pass
        self._maybe_pause()

    def _on_pause(self):
        with self._flow_cond:
            self._paused = True

    def _on_resume(self):
        with self._flow_cond:
            self._paused = False
            self._flow_cond.notify_all()

    def _on_remote_shutdown(self):
        with self._recv_cond:
            self._recv_closed = True
            self._recv_cond.notify_all()

    def _force_eof(self):
        self._fully_closed = True
        self._send_closed = True
        self._recv_closed = True
        with self._recv_cond:
            self._recv_cond.notify_all()
        with self._flow_cond:
            self._paused = False
            self._flow_cond.notify_all()
        self._ready.set()

    def _wait_timeout(self):
        t = self.timeout
        if t is None or t >= Timeout.maximum:
            return None
        return t

    def recv_raw(self, numb):
        with self.countdown():
            with self._recv_cond:
                while not self._incoming and not self._recv_closed and not self._mux._closed:
                    if not self.countdown_active():
                        return None
                    self._recv_cond.wait(self._wait_timeout())
                if self._incoming:
                    return self._incoming.get(numb)
                if self._recv_closed or self._mux._closed:
                    raise EOFError
                return None

    def send_raw(self, data):
        if self._send_closed or self._mux._closed:
            raise EOFError
        with self.countdown():
            with self._flow_cond:
                while self._paused and not self._send_closed and not self._mux._closed:
                    if not self.countdown_active():
                        raise TimeoutError(
                            'Timed out waiting for flow-control resume on channel %d'
                            % self.channel_id)
                    self._flow_cond.wait(self._wait_timeout())
        if self._send_closed or self._mux._closed:
            raise EOFError
        self._mux._send_frame(_FRAME_DATA, self.channel_id, data)
        with self._stats_lock:
            self._stats['bytes_sent'] += len(data)
            self._stats['frames_sent'] += 1

    def settimeout_raw(self, timeout):
        pass

    def can_recv_raw(self, timeout):
        if self._incoming:
            return True
        if self._recv_closed or self._mux._closed:
            return False
        with self._recv_cond:
            if self._incoming:
                return True
            if self._recv_closed or self._mux._closed:
                return False
            self._recv_cond.wait(timeout)
            return bool(self._incoming)

    def connected_raw(self, direction):
        if self._mux._closed and self._send_closed and self._recv_closed:
            return False
        if direction == 'any':
            return not (self._send_closed and self._recv_closed)
        if direction == 'send':
            return not self._send_closed
        if direction == 'recv':
            return not self._recv_closed
        return False

    def close(self):
        if self._fully_closed:
            return
        self._force_eof()
        try:
            self._mux._send_frame(_FRAME_CLOSE, self.channel_id)
        except (EOFError, OSError):
            pass

    def shutdown_raw(self, direction):
        if direction == 'send':
            if self._send_closed:
                return
            self._send_closed = True
            with self._flow_cond:
                self._flow_cond.notify_all()
            try:
                self._mux._send_frame(_FRAME_SHUTDOWN, self.channel_id)
            except (EOFError, OSError):
                pass
            if self._recv_closed:
                self._fully_closed = True
        elif direction == 'recv':
            if self._recv_closed:
                return
            self._recv_closed = True
            with self._recv_cond:
                self._recv_cond.notify_all()
            if self._send_closed:
                self._fully_closed = True

    def fileno(self):
        raise NotImplementedError('MuxChannel has no dedicated file descriptor')


def _connected_tubes():
    """Return two sock tubes joined by a socketpair (test helper)."""
    from pwnlib.tubes.sock import sock

    class _PairSock(sock):
        def __init__(self, s):
            super(_PairSock, self).__init__()
            self.sock = s
            self.rhost = 'mux'
            self.rport = 0

        def _close_msg(self):
            pass

    a, b = socket.socketpair()
    return _PairSock(a), _PairSock(b)
