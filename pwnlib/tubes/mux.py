r"""
Multiplexes several independent, bidirectional channels over a single tube.

Both ends of the underlying tube must be wrapped in a :class:`TubeMultiplexer`.
Either end may open channels with :meth:`TubeMultiplexer.open_channel`, which
the other end receives with :meth:`TubeMultiplexer.accept_channel`.  Every
channel is a :class:`MuxChannel`, a regular :class:`pwnlib.tubes.tube.tube`.

Each channel has its own flow control.  Once a channel's receive buffer holds
``high_water_mark`` bytes, the remote sender on that channel is paused until
the buffer drains to ``low_water_mark`` bytes.  Other channels are unaffected.

Examples:

    >>> l = listen()
    >>> r = remote('localhost', l.lport)
    >>> _ = l.wait_for_connection()
    >>> server, client = l.mux(), r.mux()

    Open a channel on one end and accept it on the other:

    >>> a = client.open_channel(timeout=5)
    >>> b = server.accept_channel(timeout=5)
    >>> a.channel_id == b.channel_id
    True
    >>> a.sendline(b'hello')
    >>> b.recvline(timeout=5)
    b'hello\n'
    >>> b.send(b'world')
    >>> a.recvn(5, timeout=5)
    b'world'
    >>> a.stats
    {'bytes_sent': 6, 'bytes_received': 5, 'frames_sent': 1, 'frames_received': 1}

    Closing a channel is seen by its peer:

    >>> a.close()
    >>> b.recv(timeout=5)
    Traceback (most recent call last):
    ...
    EOFError
    >>> b.connected()
    False

    Closing the multiplexer closes the underlying tube, which the other end
    detects:

    >>> client.close()
    >>> server.accept_channel(timeout=5)
    Traceback (most recent call last):
    ...
    EOFError: multiplexer is closed
"""
import collections
import os
import struct
import threading
import time

from pwnlib.context import context
from pwnlib.log import getLogger
from pwnlib.tubes.buffer import Buffer
from pwnlib.tubes.tube import tube

__all__ = ['TubeMultiplexer', 'MuxChannel']

log = getLogger(__name__)

MAX_CHANNEL_ID = 0xffff

# Frame header: frame type, channel id, payload length
_HEADER = struct.Struct('>BHI')
_U64 = struct.Struct('>Q')
# OPEN / ACCEPT payload: nonce, receive window of the sending side
_HANDSHAKE = struct.Struct('>QQ')

_OPEN    = 1  # Request to open a channel
_ACCEPT  = 2  # Channel opened
_REJECT  = 3  # Channel could not be opened
_DATA    = 4  # Channel payload
_WINDOW  = 5  # Receiver consumed some bytes; sender may send that many more
_FIN     = 6  # Sender will not send any more data (half-close)
_CLOSE   = 7  # Channel closed in both directions

# Outcomes of a pending open_channel()
_ACCEPTED = 'accepted'
_REJECTED = 'rejected'
_LOST     = 'lost'
_CLOSED   = 'closed'


def _frame(ftype, channel_id, payload=b''):
    return _HEADER.pack(ftype, channel_id, len(payload)) + payload


def _window(size):
    return min(int(size), 0xffffffffffffffff)


class _PendingOpen(object):
    def __init__(self, channel):
        self.channel = channel
        self.nonce = int.from_bytes(os.urandom(8), 'big')
        self.result = None


class _Waiter(object):
    def __init__(self):
        self.event = threading.Event()
        self.ok = False


class TubeMultiplexer(object):
    r"""TubeMultiplexer(underlying, max_channels=256, high_water_mark=1048576, low_water_mark=262144)

    Carries multiple :class:`MuxChannel` over a single tube.

    The peer on the other end of ``underlying`` must also use a
    :class:`TubeMultiplexer`.  After wrapping it, ``underlying`` must not be
    used directly anymore.

    Arguments:
        underlying(tube): Tube to carry the channels over.
        max_channels(int): Maximum number of simultaneously open channels,
            in the range ``[1, 65535]``.
        high_water_mark(int): Size of a channel's receive buffer at which the
            remote sender on that channel is paused.
        low_water_mark(int): Size of a channel's receive buffer at which a
            paused remote sender resumes.

    Raises:
        TypeError: ``underlying`` is not a tube.
        ValueError: ``max_channels`` is out of range, or ``low_water_mark``
            exceeds ``high_water_mark``.

    Examples:

        >>> TubeMultiplexer(object())
        Traceback (most recent call last):
        ...
        TypeError: underlying must be a tube, not 'object'
        >>> TubeMultiplexer(tube(), max_channels=0)
        Traceback (most recent call last):
        ...
        ValueError: max_channels must be in the range [1, 65535], got 0
        >>> TubeMultiplexer(tube(), high_water_mark=10, low_water_mark=20)
        Traceback (most recent call last):
        ...
        ValueError: low_water_mark (20) must not exceed high_water_mark (10)
    """

    #: Interval (in seconds) at which the reader thread checks for shutdown
    poll_interval = 0.05

    def __init__(self, underlying, max_channels=256, high_water_mark=1048576, low_water_mark=262144):
        if not isinstance(underlying, tube):
            raise TypeError('underlying must be a tube, not %r' % type(underlying).__name__)
        if isinstance(max_channels, bool) or not isinstance(max_channels, int) \
           or not 1 <= max_channels <= MAX_CHANNEL_ID:
            raise ValueError('max_channels must be in the range [1, %d], got %r' % (MAX_CHANNEL_ID, max_channels))
        if low_water_mark < 0:
            raise ValueError('low_water_mark must not be negative, got %r' % low_water_mark)
        if low_water_mark > high_water_mark:
            raise ValueError('low_water_mark (%r) must not exceed high_water_mark (%r)' % (low_water_mark, high_water_mark))

        self._underlying = underlying
        self._max_channels = max_channels
        self._high_water_mark = high_water_mark
        self._low_water_mark = low_water_mark

        # Lock ordering: self._lock -> MuxChannel._lock -> self._out_lock
        self._lock = threading.RLock()
        self._cond = threading.Condition(self._lock)
        self._channels = {}
        self._pending = {}
        self._accept_queue = collections.deque()
        self._next_channel_id = 1
        self._closed = False
        self._underlying_closed = False

        self._out_lock = threading.Lock()
        self._out_cond = threading.Condition(self._out_lock)
        self._outq = collections.deque()
        self._out_closed = False

        self._reader = context.Thread(target=self._reader_loop, name='TubeMultiplexer reader')
        self._reader.daemon = True
        self._writer = context.Thread(target=self._writer_loop, name='TubeMultiplexer writer')
        self._writer.daemon = True
        self._reader.start()
        self._writer.start()

    @property
    def channels(self):
        """Dictionary of open channels, keyed by channel id."""
        return self._channels

    @property
    def high_water_mark(self):
        """Receive buffer size at which the remote sender of a channel is paused."""
        return self._high_water_mark

    @property
    def low_water_mark(self):
        """Receive buffer size at which a paused remote sender resumes."""
        return self._low_water_mark

    @property
    def max_channels(self):
        """Maximum number of simultaneously open channels."""
        return self._max_channels

    @property
    def underlying(self):
        """The tube carrying the channels."""
        return self._underlying

    @property
    def closed(self):
        """:const:`True` once the multiplexer or its underlying tube is closed."""
        return self._closed

    def open_channel(self, channel_id=None, timeout=None):
        """open_channel(channel_id=None, timeout=None) -> MuxChannel

        Opens a channel and waits until the remote end acknowledges it.

        Arguments:
            channel_id(int): Channel id in the range ``[1, 65535]``.
                If :const:`None`, an unused id is allocated.
            timeout(float): Seconds to wait for the acknowledgement,
                or :const:`None` to wait forever.

        Raises:
            TypeError: ``channel_id`` is not an integer.
            ValueError: ``channel_id`` is out of range or already in use, or
                too many channels are open.
            TimeoutError: The remote end did not acknowledge in time.
            EOFError: The multiplexer is closed.
        """
        if channel_id is not None:
            if isinstance(channel_id, bool) or not isinstance(channel_id, int):
                raise TypeError('channel_id must be an integer, not %r' % type(channel_id).__name__)
            if not 1 <= channel_id <= MAX_CHANNEL_ID:
                raise ValueError('channel_id must be in the range [1, %d], got %d' % (MAX_CHANNEL_ID, channel_id))

        deadline = None if timeout is None else time.time() + timeout

        with self._lock:
            while True:
                if self._closed:
                    raise EOFError('multiplexer is closed')

                if channel_id is None:
                    cid = self._allocate_channel_id()
                else:
                    cid = channel_id
                    if cid in self._channels or cid in self._pending:
                        raise ValueError('channel %d is already open' % cid)
                    self._check_capacity()

                pending = _PendingOpen(MuxChannel(self, cid))
                self._pending[cid] = pending
                try:
                    self._enqueue(_frame(_OPEN, cid, _HANDSHAKE.pack(pending.nonce, _window(self._high_water_mark))))
                except EOFError:
                    del self._pending[cid]
                    raise

                while pending.result is None:
                    if deadline is None:
                        self._cond.wait()
                        continue
                    remaining = deadline - time.time()
                    if remaining <= 0:
                        break
                    self._cond.wait(remaining)

                if pending.result is None:
                    del self._pending[cid]
                    pending.channel._release()
                    # The remote may have opened its end already; tear it down.
                    self._enqueue_quiet(_frame(_CLOSE, cid))
                    raise TimeoutError('remote did not acknowledge channel %d within %r seconds' % (cid, timeout))
                if pending.result is _ACCEPTED:
                    return pending.channel
                if pending.result is _REJECTED:
                    raise ValueError('remote refused to open channel %d' % cid)
                if pending.result is _CLOSED:
                    raise EOFError('multiplexer is closed')

                # _LOST: the remote opened the same id concurrently and won.
                if channel_id is not None:
                    raise ValueError('channel %d is already open' % cid)

    def accept_channel(self, timeout=None):
        """accept_channel(timeout=None) -> MuxChannel

        Waits for the remote end to open a channel.

        Arguments:
            timeout(float): Seconds to wait, or :const:`None` to wait forever.

        Returns:
            The opened :class:`MuxChannel`, or :const:`None` on timeout.

        Raises:
            EOFError: The multiplexer is closed.
        """
        deadline = None if timeout is None else time.time() + timeout

        with self._lock:
            while True:
                if self._closed:
                    raise EOFError('multiplexer is closed')
                if self._accept_queue:
                    return self._accept_queue.popleft()
                if deadline is None:
                    self._cond.wait()
                    continue
                remaining = deadline - time.time()
                if remaining <= 0:
                    return None
                self._cond.wait(remaining)

    def close(self):
        """close()

        Signals EOF to all channels and closes the underlying tube.

        Calling :meth:`close` more than once has no effect.
        """
        with self._lock:
            channels = []
            if not self._closed:
                self._closed = True
                channels = list(self._channels.values())
                channels += [p.channel for p in self._pending.values()]
                for pending in self._pending.values():
                    pending.result = _CLOSED
                self._channels.clear()
                self._pending.clear()
                self._accept_queue.clear()
                self._cond.notify_all()

        with self._out_lock:
            self._out_closed = True
            dropped = list(self._outq)
            self._outq.clear()
            self._out_cond.notify_all()

        for _, waiter in dropped:
            if waiter is not None:
                waiter.event.set()

        for channel in channels:
            channel._on_eof()

        # Let both threads finish before closing the tube under them, but
        # never hold up the close for long.
        current = threading.current_thread()
        if current is not self._reader and current is not self._writer:
            for thread in (self._writer, self._reader):
                if thread.is_alive():
                    thread.join(timeout=1)

        with self._lock:
            if self._underlying_closed:
                return
            self._underlying_closed = True

        try:
            self._underlying.close()
        except Exception:
            pass

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()

    # Called with self._lock held
    def _open_count(self):
        return len(self._channels) + len(self._pending)

    def _check_capacity(self):
        if self._open_count() >= self._max_channels:
            raise ValueError('cannot open more than %d channels' % self._max_channels)

    def _allocate_channel_id(self):
        self._check_capacity()
        for _ in range(MAX_CHANNEL_ID):
            cid = self._next_channel_id
            self._next_channel_id = cid % MAX_CHANNEL_ID + 1
            if cid not in self._channels and cid not in self._pending:
                return cid
        raise ValueError('no free channel id')

    def _forget(self, channel):
        with self._lock:
            if self._channels.get(channel.channel_id) is channel:
                del self._channels[channel.channel_id]

    def _enqueue(self, frame, wait=False):
        waiter = _Waiter() if wait else None
        with self._out_lock:
            if self._out_closed:
                raise EOFError('multiplexer is closed')
            self._outq.append((frame, waiter))
            self._out_cond.notify()
        return waiter

    def _enqueue_quiet(self, frame):
        try:
            self._enqueue(frame)
        except EOFError:
            pass

    def _writer_loop(self):
        while True:
            with self._out_lock:
                while not self._outq and not self._out_closed:
                    self._out_cond.wait()
                if self._out_closed:
                    return
                frame, waiter = self._outq.popleft()

            try:
                self._underlying.send(frame)
            except Exception as e:
                log.debug('TubeMultiplexer: sending to the underlying tube failed: %r', e)
                if waiter is not None:
                    waiter.event.set()
                self.close()
                return

            if waiter is not None:
                waiter.ok = True
                waiter.event.set()

    def _read_some(self):
        """Returns data from the underlying tube, or b'' if none is ready yet.

        The socket timeout is shared with the writer thread, so it must not
        be changed here: waiting is done with can_recv(), and recv() is only
        called once data is available.
        """
        underlying = self._underlying
        try:
            ready = underlying.can_recv(self.poll_interval)
        except NotImplementedError:
            return underlying.recv(timeout=self.poll_interval)

        if ready:
            return underlying.recv()

        try:
            connected = underlying.connected('recv')
        except NotImplementedError:
            connected = True
        if not connected:
            raise EOFError
        return b''

    def _reader_loop(self):
        buf = bytearray()
        try:
            while not self._closed:
                data = self._read_some()
                if not data:
                    continue
                buf += data
                while len(buf) >= _HEADER.size:
                    ftype, cid, length = _HEADER.unpack_from(buf)
                    end = _HEADER.size + length
                    if len(buf) < end:
                        break
                    payload = bytes(buf[_HEADER.size:end])
                    del buf[:end]
                    self._dispatch(ftype, cid, payload)
        except EOFError:
            pass
        except Exception as e:
            if not self._closed:
                log.debug('TubeMultiplexer: receiving from the underlying tube failed: %r', e)
        finally:
            # If already closed, whoever closed it also closes the underlying tube.
            if not self._closed:
                self.close()

    def _dispatch(self, ftype, cid, payload):
        if ftype == _OPEN:
            nonce, window = _HANDSHAKE.unpack(payload)
            self._on_open(cid, nonce, window)
            return
        if ftype == _ACCEPT:
            nonce, window = _HANDSHAKE.unpack(payload)
            self._on_accept(cid, nonce, window)
            return
        if ftype == _REJECT:
            nonce, = _U64.unpack(payload)
            self._on_reject(cid, nonce)
            return

        # Frames for ids that are closed or still pending are left over from
        # a previous channel with the same id, and are dropped.
        with self._lock:
            if ftype == _CLOSE:
                channel = self._channels.pop(cid, None)
            else:
                channel = self._channels.get(cid)

        if ftype == _DATA:
            if channel is not None:
                channel._on_data(payload)
        elif ftype == _WINDOW:
            size, = _U64.unpack(payload)
            if channel is not None:
                channel._on_window(size)
        elif ftype == _FIN:
            if channel is not None:
                channel._on_fin()
        elif ftype == _CLOSE:
            if channel is not None:
                channel._on_close()
        else:
            raise ValueError('unknown frame type %d' % ftype)

    def _on_open(self, cid, nonce, window):
        with self._lock:
            if self._closed:
                return

            pending = self._pending.get(cid)
            if pending is not None:
                # Both ends opened this id at once; the larger nonce wins.
                if nonce < pending.nonce:
                    return
                del self._pending[cid]
                pending.result = _LOST
                pending.channel._release()
                self._cond.notify_all()
            elif cid == 0 or cid in self._channels or self._open_count() >= self._max_channels:
                self._enqueue_quiet(_frame(_REJECT, cid, _U64.pack(nonce)))
                return

            channel = MuxChannel(self, cid)
            channel._remote_window = window
            self._channels[cid] = channel
            # Queued before the channel is handed out, so that ACCEPT always
            # precedes any frame sent on the channel.
            self._enqueue_quiet(_frame(_ACCEPT, cid, _HANDSHAKE.pack(nonce, _window(self._high_water_mark))))
            self._accept_queue.append(channel)
            self._cond.notify_all()

    def _on_accept(self, cid, nonce, window):
        with self._lock:
            pending = self._pending.get(cid)
            if pending is None or pending.nonce != nonce:
                return
            del self._pending[cid]
            pending.channel._remote_window = window
            self._channels[cid] = pending.channel
            pending.result = _ACCEPTED
            self._cond.notify_all()

    def _on_reject(self, cid, nonce):
        with self._lock:
            pending = self._pending.get(cid)
            if pending is None or pending.nonce != nonce:
                return
            del self._pending[cid]
            pending.channel._release()
            pending.result = _REJECTED
            self._cond.notify_all()


class MuxChannel(tube):
    r"""A logical channel of a :class:`TubeMultiplexer`.

    Channels are created with :meth:`TubeMultiplexer.open_channel` and
    :meth:`TubeMultiplexer.accept_channel`, and support all
    :class:`pwnlib.tubes.tube.tube` methods.

    Closing a channel with :meth:`close` ends it in both directions, and the
    peer's :meth:`recv` and :meth:`send` raise :class:`EOFError`.
    ``shutdown('send')`` only ends the sending direction: the peer receives
    EOF, but may continue to send.

    Sending blocks while the peer's receive buffer for this channel is full.
    If the channel's :attr:`timeout` expires first, :class:`TimeoutError`
    is raised.

    Examples:

        >>> l = listen()
        >>> r = remote('localhost', l.lport)
        >>> _ = l.wait_for_connection()
        >>> server = l.mux(high_water_mark=16, low_water_mark=4)
        >>> client = r.mux()
        >>> a = client.open_channel(timeout=5)
        >>> b = server.accept_channel(timeout=5)

        Fill up the peer's receive buffer, and the next send blocks:

        >>> a.timeout = 0.5
        >>> a.send(b'A' * 16)
        >>> a.send(b'B')
        Traceback (most recent call last):
        ...
        TimeoutError: ...

        Draining the buffer lets the sender resume:

        >>> b.recvn(16, timeout=5)
        b'AAAAAAAAAAAAAAAA'
        >>> a.send(b'B')
        >>> b.recv(timeout=5)
        b'B'

        Half-closing a channel:

        >>> a.shutdown('send')
        >>> a.send(b'C')
        Traceback (most recent call last):
        ...
        EOFError
        >>> b.recv(timeout=5)
        Traceback (most recent call last):
        ...
        EOFError
        >>> b.send(b'still open')
        >>> a.recv(timeout=5)
        b'still open'
        >>> client.close()
        >>> server.close()
    """

    def __init__(self, multiplexer, channel_id):
        super(MuxChannel, self).__init__()
        self._mux = multiplexer
        self._channel_id = channel_id

        # Lock ordering: TubeMultiplexer._lock -> self._lock -> TubeMultiplexer._out_lock
        self._lock = threading.RLock()
        self._cond = threading.Condition(self._lock)

        self._rbuf = Buffer()
        self._rbuf.set_watermarks(high=multiplexer.high_water_mark, low=multiplexer.low_water_mark)

        self._send_open = True   # We may send
        self._recv_open = True   # The peer may send
        self._recv_shut = False  # shutdown('recv') was called
        self._closed = False     # close() was called
        self._released = False   # The channel id is no longer ours; no frames may be sent

        self._remote_window = 0  # Peer's high water mark
        self._unacked = 0        # Bytes sent that the peer has not consumed yet
        self._unreported = 0     # Bytes consumed that we have not told the peer about yet

        self._stats = {
            'bytes_sent': 0,
            'bytes_received': 0,
            'frames_sent': 0,
            'frames_received': 0,
        }

    @property
    def channel_id(self):
        """Id of this channel, shared by both ends."""
        return self._channel_id

    @property
    def multiplexer(self):
        """The :class:`TubeMultiplexer` that owns this channel."""
        return self._mux

    @property
    def stats(self):
        """Dictionary with the ``bytes_sent``, ``bytes_received``,
        ``frames_sent`` and ``frames_received`` counters of this channel.

        One frame is sent per :meth:`send` call.
        """
        with self._lock:
            return dict(self._stats)

    def recv_raw(self, numb):
        deadline = time.time() + self.timeout
        with self._cond:
            while True:
                if self._closed or self._recv_shut:
                    raise EOFError
                if self._rbuf.size:
                    data = self._rbuf.get(numb)
                    self._unreported += len(data)
                    self._report_consumed()
                    return data
                if not self._recv_open:
                    raise EOFError
                remaining = deadline - time.time()
                if remaining <= 0:
                    return None
                self._cond.wait(remaining)

    def send_raw(self, data):
        deadline = time.time() + self.timeout
        with self._cond:
            while True:
                if not self._send_open:
                    raise EOFError
                # Always allow a send when nothing is outstanding, so that
                # payloads larger than the peer's buffer can still get through.
                if not data or self._unacked < max(self._remote_window, 1):
                    break
                remaining = deadline - time.time()
                if remaining <= 0:
                    raise TimeoutError('channel %d: peer receive buffer is full' % self._channel_id)
                self._cond.wait(remaining)

            waiter = self._mux._enqueue(_frame(_DATA, self._channel_id, data), wait=True)
            self._unacked += len(data)
            self._stats['frames_sent'] += 1
            self._stats['bytes_sent'] += len(data)

        waiter.event.wait()
        if not waiter.ok:
            raise EOFError

    def can_recv_raw(self, timeout):
        deadline = None if timeout is None else time.time() + timeout
        with self._cond:
            while not self._rbuf.size:
                if self._closed or self._recv_shut or not self._recv_open:
                    return False
                if deadline is None:
                    self._cond.wait()
                    continue
                remaining = deadline - time.time()
                if remaining <= 0:
                    return False
                self._cond.wait(remaining)
            return not (self._closed or self._recv_shut)

    def settimeout_raw(self, timeout):
        pass

    def connected_raw(self, direction):
        with self._lock:
            send = self._send_open
            recv = not (self._closed or self._recv_shut) \
                   and bool(self._recv_open or self._rbuf.size or self.buffer.size)
        if direction == 'send':
            return send
        if direction == 'recv':
            return recv
        return send or recv

    def shutdown_raw(self, direction):
        with self._cond:
            if direction == 'send':
                if self._send_open and not self._released:
                    self._mux._enqueue_quiet(_frame(_FIN, self._channel_id))
                self._send_open = False
            elif direction == 'recv':
                self._recv_shut = True
                self._unreported += self._rbuf.size
                self._rbuf.get()
                self._report_consumed()
            self._cond.notify_all()

    def close(self):
        """close()

        Closes the channel in both directions, and signals EOF to the peer.
        """
        with self._cond:
            if self._closed:
                return
            self._closed = True
            self._send_open = False
            self._rbuf.get()
            if not self._released:
                self._released = True
                self._mux._enqueue_quiet(_frame(_CLOSE, self._channel_id))
            self._cond.notify_all()
        self._mux._forget(self)

    # Called with self._lock held
    def _report_consumed(self):
        if self._unreported and not self._released and (self._recv_shut or self._rbuf.under_low_water):
            self._mux._enqueue_quiet(_frame(_WINDOW, self._channel_id, _U64.pack(self._unreported)))
            self._unreported = 0

    def _release(self):
        with self._cond:
            self._released = True
            self._send_open = False
            self._recv_open = False
            self._cond.notify_all()

    # Called by the multiplexer
    def _on_data(self, data):
        with self._cond:
            self._stats['frames_received'] += 1
            self._stats['bytes_received'] += len(data)
            if self._closed or self._recv_shut:
                self._unreported += len(data)
                self._report_consumed()
                return
            self._rbuf.add(data)
            self._cond.notify_all()

    def _on_window(self, size):
        with self._cond:
            self._unacked = max(self._unacked - size, 0)
            self._cond.notify_all()

    def _on_fin(self):
        with self._cond:
            self._recv_open = False
            self._cond.notify_all()

    def _on_close(self):
        self._release()

    def _on_eof(self):
        self._release()
