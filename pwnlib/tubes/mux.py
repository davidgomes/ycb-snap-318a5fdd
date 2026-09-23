"""Multiplex many independent, bidirectional logical channels over one tube.

Both ends of the underlying tube must be wrapped in a :class:`TubeMultiplexer`.
Either end may open channels with :meth:`TubeMultiplexer.open_channel`, and the
other end receives them with :meth:`TubeMultiplexer.accept_channel`.  Every
channel is a :class:`MuxChannel`, which is a full :class:`.tube`.

Flow control is per channel: once a channel's peer holds ``high_water_mark``
unconsumed bytes, sends on that channel block until the peer has drained its
buffer down to ``low_water_mark`` bytes.

Example:

    >>> l = listen()
    >>> r = remote('localhost', l.lport)
    >>> a = r.mux()
    >>> b = l.wait_for_connection().mux()
    >>> ca = a.open_channel()
    >>> cb = b.accept_channel(timeout=5)
    >>> ca.channel_id == cb.channel_id
    True
    >>> ca.sendline(b'hello')
    >>> cb.recvline()
    b'hello\\n'
    >>> cb.send(b'world')
    >>> ca.recvn(5)
    b'world'
    >>> ca.stats['frames_sent'], cb.stats['frames_received']
    (1, 1)
    >>> ca.close()
    >>> cb.recv()
    Traceback (most recent call last):
    ...
    EOFError
    >>> a.close()
    >>> b.accept_channel()
    Traceback (most recent call last):
    ...
    EOFError: multiplexer is closed
"""
import collections
import queue
import random
import struct
import threading
import time

from pwnlib.timeout import Timeout
from pwnlib.tubes.buffer import Buffer
from pwnlib.tubes.tube import tube

__all__ = ['TubeMultiplexer', 'MuxChannel']

MIN_CHANNEL_ID = 1
MAX_CHANNEL_ID = 0xffff

# Frame header: type, channel id, payload length
_HEADER = struct.Struct('>BHI')
_WATERMARKS = struct.Struct('>QQ')
_WINDOW = struct.Struct('>q')

_OPEN        = 1
_OPEN_ACK    = 2
_OPEN_REJECT = 3
_DATA        = 4
_SHUTDOWN    = 5
_CLOSE       = 6
_WINDOW_UPD  = 7
_GOODBYE     = 8

_POLL_INTERVAL = 0.1
_CLOSE_FLUSH_TIMEOUT = 2.0
_AUTO_ID_ATTEMPTS = 32


def _frame(kind, channel_id, payload=b''):
    return _HEADER.pack(kind, channel_id, len(payload)) + payload


def _deadline(timeout):
    if timeout is None or timeout >= Timeout.maximum:
        return None
    return time.time() + timeout


def _remaining(deadline):
    if deadline is None:
        return None
    return max(deadline - time.time(), 0)


class _ChannelBuffer(Buffer):
    """Receive buffer of a :class:`MuxChannel` which reports consumption to
    the channel so the remote sender's window can be updated."""

    def __init__(self, channel, *a, **kw):
        super(_ChannelBuffer, self).__init__(*a, **kw)
        self._channel = channel

    def get(self, want=float('inf')):
        data = super(_ChannelBuffer, self).get(want)
        if data:
            self._channel._credit(len(data))
        return data

    def unget(self, data):
        before = self.size
        super(_ChannelBuffer, self).unget(data)
        if self.size > before:
            self._channel._credit(before - self.size)


class MuxChannel(tube):
    """A logical channel of a :class:`TubeMultiplexer`.

    Instances are created by :meth:`TubeMultiplexer.open_channel` and
    :meth:`TubeMultiplexer.accept_channel`; they should not be created directly.
    """

    def __init__(self, mux, channel_id, remote_high, remote_low, timeout=tube.default, level=None):
        super(MuxChannel, self).__init__(timeout=timeout, level=level)
        self._mux = mux
        self._channel_id = channel_id
        self._cond = threading.Condition(threading.Lock())

        self.buffer = _ChannelBuffer(self)
        self.buffer.set_watermarks(mux.high_water_mark, mux.low_water_mark)
        self._rx = Buffer()

        self._remote_high = remote_high
        self._remote_low = remote_low
        self._outstanding = 0
        self._paused = False

        self._closed = False         # closed locally
        self._send_shut = False      # shutdown('send') locally
        self._recv_shut = False      # shutdown('recv') locally
        self._recv_eof = False       # remote will send nothing more
        self._remote_closed = False  # remote no longer accepts data

        self._stats = {
            'bytes_sent': 0,
            'bytes_received': 0,
            'frames_sent': 0,
            'frames_received': 0,
        }

    def __repr__(self):
        return '<%s channel_id=%d>' % (type(self).__name__, self._channel_id)

    @property
    def channel_id(self):
        """Numeric identifier of this channel, shared by both ends."""
        return self._channel_id

    @property
    def stats(self):
        """Dictionary of traffic counters: ``bytes_sent``, ``bytes_received``,
        ``frames_sent`` and ``frames_received``."""
        with self._cond:
            return dict(self._stats)

    def _peer_reachable(self):
        return not self._remote_closed and not self._mux._closed

    # Called by the multiplexer's reader thread

    def _deliver(self, data):
        with self._cond:
            if self._closed:
                return
            self._stats['bytes_received'] += len(data)
            self._stats['frames_received'] += 1
            if self._recv_shut:
                self._send_window(len(data))
                return
            self._rx.add(data)
            self._cond.notify_all()

    def _on_window(self, delta):
        with self._cond:
            self._outstanding = max(self._outstanding - delta, 0)
            if self._outstanding >= self._remote_high:
                self._paused = True
            elif self._paused and self._outstanding <= self._remote_low:
                self._paused = False
            self._cond.notify_all()

    def _on_remote_shutdown(self):
        with self._cond:
            self._recv_eof = True
            self._cond.notify_all()

    def _on_remote_close(self):
        with self._cond:
            self._recv_eof = True
            self._remote_closed = True
            self._cond.notify_all()

    # Flow control credits, sent while holding self._cond so that window
    # updates keep their order relative to other frames on this channel.

    def _send_window(self, delta):
        if delta and not self._closed and self._peer_reachable():
            self._mux._enqueue(_frame(_WINDOW_UPD, self._channel_id, _WINDOW.pack(delta)))

    def _credit(self, delta):
        with self._cond:
            self._send_window(delta)

    # tube interface

    def recv_raw(self, numb):
        with self._cond:
            deadline = _deadline(self.timeout)
            while True:
                if self._closed or self._recv_shut:
                    raise EOFError
                if self._rx:
                    break
                if self._recv_eof:
                    raise EOFError
                remaining = _remaining(deadline)
                if remaining == 0:
                    return None
                self._cond.wait(remaining)

            # Bytes moved into self.buffer are credited when the user consumes
            # them, so the move itself must be uncounted.
            data = self._rx.get(numb)
            return data

    def send_raw(self, data):
        with self._cond:
            deadline = _deadline(self.timeout)
            while True:
                if self._closed or self._send_shut or not self._peer_reachable():
                    raise EOFError
                if not self._paused:
                    break
                remaining = _remaining(deadline)
                if remaining == 0:
                    raise TimeoutError('channel %d: send blocked by flow control' % self._channel_id)
                self._cond.wait(remaining)

            self._outstanding += len(data)
            if self._outstanding >= self._remote_high:
                self._paused = True
            self._stats['bytes_sent'] += len(data)
            self._stats['frames_sent'] += 1
            self._mux._enqueue(_frame(_DATA, self._channel_id, data))

    def settimeout_raw(self, timeout):
        pass

    def can_recv_raw(self, timeout):
        with self._cond:
            deadline = _deadline(timeout)
            while not self._rx and not (self._closed or self._recv_shut or self._recv_eof):
                remaining = _remaining(deadline)
                if remaining == 0:
                    break
                self._cond.wait(remaining)
            return bool(self._rx) and not (self._closed or self._recv_shut)

    def connected_raw(self, direction):
        with self._cond:
            recv = not (self._closed or self._recv_shut or self._recv_eof)
            send = not (self._closed or self._send_shut) and self._peer_reachable()
        if direction == 'recv':
            return recv
        if direction == 'send':
            return send
        return recv or send

    def shutdown_raw(self, direction):
        with self._cond:
            if self._closed:
                return
            if direction == 'send' and not self._send_shut:
                self._send_shut = True
                if self._peer_reachable():
                    self._mux._enqueue(_frame(_SHUTDOWN, self._channel_id))
            elif direction == 'recv' and not self._recv_shut:
                self._recv_shut = True
                discarded = len(self._rx)
                self._rx.get()
                self._send_window(discarded)
            self._cond.notify_all()
            fully_shut = self._send_shut and self._recv_shut

        if fully_shut:
            self.close()

    def close(self):
        """close()

        Closes the channel in both directions and signals EOF to the peer.
        Other channels of the multiplexer are unaffected.
        """
        with self._cond:
            if self._closed:
                return
            self._closed = True
            if self._peer_reachable():
                self._mux._enqueue(_frame(_CLOSE, self._channel_id))
            self._cond.notify_all()
        self._mux._forget(self)

    def fileno(self):
        raise NotImplementedError('MuxChannel has no file descriptor')


class _PendingOpen(object):
    def __init__(self):
        self.result = None  # 'ack', 'reject' or 'closed'
        self.channel = None


class TubeMultiplexer(object):
    """Multiplexes independent channels over a single underlying tube.

    Arguments:
        underlying(tube): Tube to carry the channels.  The remote end must
            also wrap its end in a :class:`TubeMultiplexer`.  The multiplexer
            takes ownership of the tube; it should not be used directly anymore.
        max_channels(int): Maximum number of simultaneously open channels,
            between 1 and 65535.
        high_water_mark(int): Number of unconsumed bytes in a channel's
            receive buffer at which the remote sender is paused.
        low_water_mark(int): Number of bytes to which a paused channel's
            receive buffer must drain before the remote sender resumes.
    """

    def __init__(self, underlying, max_channels=256, high_water_mark=1048576, low_water_mark=262144):
        if not isinstance(underlying, tube):
            raise TypeError('underlying must be a tube, not %s' % type(underlying).__name__)
        for name, value in (('max_channels', max_channels),
                            ('high_water_mark', high_water_mark),
                            ('low_water_mark', low_water_mark)):
            if not isinstance(value, int) or isinstance(value, bool):
                raise TypeError('%s must be an integer, not %s' % (name, type(value).__name__))
        if not MIN_CHANNEL_ID <= max_channels <= MAX_CHANNEL_ID:
            raise ValueError('max_channels must be in [%d, %d], got %d'
                             % (MIN_CHANNEL_ID, MAX_CHANNEL_ID, max_channels))
        if high_water_mark < 1:
            raise ValueError('high_water_mark must be positive, got %d' % high_water_mark)
        if low_water_mark < 0:
            raise ValueError('low_water_mark must not be negative, got %d' % low_water_mark)
        if low_water_mark > high_water_mark:
            raise ValueError('low_water_mark (%d) must not exceed high_water_mark (%d)'
                             % (low_water_mark, high_water_mark))

        self._tube = underlying
        self._max_channels = max_channels
        self._high = high_water_mark
        self._low = low_water_mark

        self._lock = threading.RLock()
        self._cond = threading.Condition(self._lock)
        self._channels = {}
        self._pending = {}
        self._accept_queue = collections.deque()
        self._next_id = MIN_CHANNEL_ID
        self._closed = False
        self._dead = False

        self._outgoing = queue.Queue()
        self._writer = threading.Thread(target=self._writer_loop, name='mux-writer')
        self._reader = threading.Thread(target=self._reader_loop, name='mux-reader')
        self._writer.daemon = True
        self._reader.daemon = True
        self._writer.start()
        self._reader.start()

    def __repr__(self):
        return '<%s channels=%d%s>' % (type(self).__name__, len(self._channels),
                                       ' closed' if self._closed else '')

    def __enter__(self):
        return self

    def __exit__(self, *a):
        self.close()

    @property
    def channels(self):
        """Dictionary mapping channel ids to open :class:`MuxChannel` objects."""
        with self._lock:
            return dict(self._channels)

    @property
    def high_water_mark(self):
        return self._high

    @property
    def low_water_mark(self):
        return self._low

    @property
    def closed(self):
        """True once the multiplexer has been closed, locally or remotely."""
        return self._closed

    # Public API

    def open_channel(self, channel_id=None, timeout=None):
        """open_channel(channel_id=None, timeout=None) -> MuxChannel

        Opens a channel and waits for the remote end to acknowledge it.

        Arguments:
            channel_id(int): Identifier in ``[1, 65535]``, or :const:`None`
                to allocate an unused one.
            timeout(float): Seconds to wait for acknowledgement, or
                :const:`None` to wait forever.

        Raises:
            TypeError: ``channel_id`` is not an integer.
            ValueError: ``channel_id`` is out of range, already in use, or
                the maximum number of channels is open.
            TimeoutError: The remote end did not acknowledge in time.
            EOFError: The multiplexer is closed.
        """
        auto = channel_id is None
        if not auto and (not isinstance(channel_id, int) or isinstance(channel_id, bool)):
            raise TypeError('channel_id must be an integer, not %s' % type(channel_id).__name__)

        deadline = _deadline(timeout)
        for attempt in range(_AUTO_ID_ATTEMPTS if auto else 1):
            with self._lock:
                if self._closed:
                    raise EOFError('multiplexer is closed')
                if auto:
                    cid = self._allocate_id(randomize=attempt > 0)
                else:
                    self._check_new_id(channel_id)
                    cid = channel_id
                pending = self._pending[cid] = _PendingOpen()
                self._enqueue(_frame(_OPEN, cid, _WATERMARKS.pack(self._high, self._low)))

                while pending.result is None and not self._closed:
                    remaining = _remaining(deadline)
                    if remaining == 0:
                        break
                    self._cond.wait(remaining)
                del self._pending[cid]

                if pending.result == 'ack':
                    return pending.channel
                if pending.result == 'closed' or self._closed:
                    raise EOFError('multiplexer is closed')
                if pending.result is None:
                    raise TimeoutError('remote did not acknowledge channel %d' % cid)
            if not auto:
                raise ValueError('channel %d was rejected by the remote end' % cid)
        raise ValueError('could not allocate a channel id accepted by the remote end')

    def accept_channel(self, timeout=None):
        """accept_channel(timeout=None) -> MuxChannel

        Waits for the remote end to open a channel.

        Arguments:
            timeout(float): Seconds to wait, or :const:`None` to wait forever.

        Returns:
            The new :class:`MuxChannel`, or :const:`None` on timeout.

        Raises:
            EOFError: The multiplexer is closed, including while waiting.
        """
        deadline = _deadline(timeout)
        with self._lock:
            while True:
                if self._closed:
                    raise EOFError('multiplexer is closed')
                if self._accept_queue:
                    return self._accept_queue.popleft()
                remaining = _remaining(deadline)
                if remaining == 0:
                    return None
                self._cond.wait(remaining)

    def close(self):
        """close()

        Signals EOF on every channel, tells the remote end the multiplexer is
        going away, and closes the underlying tube.  Idempotent.
        """
        with self._lock:
            if self._closed:
                return
            self._closed = True
            self._cond.notify_all()
            self._outgoing.put(_frame(_GOODBYE, 0))
            self._outgoing.put(None)

        if threading.current_thread() is not self._writer:
            self._writer.join(_CLOSE_FLUSH_TIMEOUT)
        self._teardown()

    # Internals

    def _check_new_id(self, cid):
        if not MIN_CHANNEL_ID <= cid <= MAX_CHANNEL_ID:
            raise ValueError('channel_id must be in [%d, %d], got %d'
                             % (MIN_CHANNEL_ID, MAX_CHANNEL_ID, cid))
        if cid in self._channels or cid in self._pending:
            raise ValueError('channel %d is already in use' % cid)
        if len(self._channels) + len(self._pending) >= self._max_channels:
            raise ValueError('maximum number of channels (%d) reached' % self._max_channels)

    def _allocate_id(self, randomize):
        if len(self._channels) + len(self._pending) >= self._max_channels:
            raise ValueError('maximum number of channels (%d) reached' % self._max_channels)
        # A random start avoids repeatedly colliding with the remote end when
        # both sides allocate ids at the same time.
        start = random.randint(MIN_CHANNEL_ID, MAX_CHANNEL_ID) if randomize else self._next_id
        span = MAX_CHANNEL_ID - MIN_CHANNEL_ID + 1
        for i in range(span):
            cid = MIN_CHANNEL_ID + (start - MIN_CHANNEL_ID + i) % span
            if cid not in self._channels and cid not in self._pending:
                self._next_id = MIN_CHANNEL_ID + (cid - MIN_CHANNEL_ID + 1) % span
                return cid
        raise ValueError('no free channel ids')

    def _enqueue(self, frame):
        if not self._dead:
            self._outgoing.put(frame)

    def _forget(self, channel):
        with self._lock:
            if self._channels.get(channel.channel_id) is channel:
                del self._channels[channel.channel_id]
            try:
                self._accept_queue.remove(channel)
            except ValueError:
                pass

    def _teardown(self):
        with self._lock:
            if self._dead:
                return
            self._dead = True
            self._closed = True
            channels = list(self._channels.values())
            self._channels.clear()
            self._accept_queue.clear()
            for pending in self._pending.values():
                pending.result = 'closed'
            self._cond.notify_all()
            self._outgoing.put(None)

        for channel in channels:
            channel._on_remote_close()

        try:
            self._tube.close()
        except Exception:
            pass

    def _writer_loop(self):
        try:
            while True:
                frame = self._outgoing.get()
                if frame is None:
                    return
                self._tube.send(frame)
        except Exception:
            self._teardown()

    def _read_some(self):
        """Returns bytes from the underlying tube, or b'' if nothing arrived
        within the poll interval.  Raises EOFError once the tube is dead."""
        t = self._tube
        try:
            ready = t.can_recv(_POLL_INTERVAL)
        except NotImplementedError:
            return t.recv(timeout=Timeout.forever)
        if ready:
            return t.recv()
        try:
            alive = t.connected('recv')
        except NotImplementedError:
            alive = True
        if not alive:
            raise EOFError
        return b''

    def _reader_loop(self):
        data = bytearray()
        try:
            while not self._closed:
                chunk = self._read_some()
                if not chunk:
                    continue
                data += chunk
                while len(data) >= _HEADER.size:
                    kind, cid, length = _HEADER.unpack_from(data)
                    end = _HEADER.size + length
                    if len(data) < end:
                        break
                    payload = bytes(data[_HEADER.size:end])
                    del data[:end]
                    if not self._dispatch(kind, cid, payload):
                        return
        except Exception:
            pass
        finally:
            self._teardown()

    def _dispatch(self, kind, cid, payload):
        """Handles one incoming frame.  Returns False once the remote end has
        said goodbye."""
        if kind == _GOODBYE:
            return False

        if kind == _OPEN:
            high, low = _WATERMARKS.unpack(payload)
            with self._lock:
                if self._closed:
                    return True
                busy = cid in self._channels or cid in self._pending
                full = len(self._channels) + len(self._pending) >= self._max_channels
                if busy or full or not MIN_CHANNEL_ID <= cid <= MAX_CHANNEL_ID:
                    self._enqueue(_frame(_OPEN_REJECT, cid))
                    return True
                channel = MuxChannel(self, cid, high, low)
                self._channels[cid] = channel
                # Acknowledge before the channel becomes visible, so no data
                # frame on it can overtake the acknowledgement.
                self._enqueue(_frame(_OPEN_ACK, cid, _WATERMARKS.pack(self._high, self._low)))
                self._accept_queue.append(channel)
                self._cond.notify_all()
            return True

        if kind in (_OPEN_ACK, _OPEN_REJECT):
            with self._lock:
                pending = self._pending.get(cid)
                if pending is None or pending.result is not None:
                    # The opener gave up waiting; tear down what the remote made.
                    if kind == _OPEN_ACK:
                        self._enqueue(_frame(_CLOSE, cid))
                    return True
                if kind == _OPEN_ACK:
                    high, low = _WATERMARKS.unpack(payload)
                    pending.channel = MuxChannel(self, cid, high, low)
                    self._channels[cid] = pending.channel
                    pending.result = 'ack'
                else:
                    pending.result = 'reject'
                self._cond.notify_all()
            return True

        with self._lock:
            channel = self._channels.get(cid)
            if kind == _CLOSE and channel is not None:
                del self._channels[cid]
        if channel is None:
            return True

        if kind == _DATA:
            channel._deliver(payload)
        elif kind == _WINDOW_UPD:
            channel._on_window(_WINDOW.unpack(payload)[0])
        elif kind == _SHUTDOWN:
            channel._on_remote_shutdown()
        elif kind == _CLOSE:
            channel._on_remote_close()
        return True
