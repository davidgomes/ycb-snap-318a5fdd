"""Multiplex many bidirectional channels over a single tube.

A :class:`TubeMultiplexer` frames an underlying
:class:`~pwnlib.tubes.tube.tube` so each :class:`MuxChannel` behaves like
its own tube. Channels are flow-controlled independently: a channel whose
receive buffer reaches its high water mark stops the remote sender until
the buffer drains to the low water mark.

Wire format, all integers big-endian::

    uint8  type
    uint16 channel_id
    uint32 length
    bytes  payload

``OPEN`` / ``OPEN_ACK`` carry the sender's high water mark as a ``uint64``.
``WINDOW`` carries the absolute number of payload bytes the receiver's
application has consumed, also as a ``uint64``. Senders may have at most
``peer_high + peer_consumed - sent`` bytes outstanding, and a ``WINDOW``
update is withheld while the receive buffer sits between the low and high
water marks after a pause. ``EOF`` half-closes the sender; ``CLOSE`` closes
both directions; ``MUX_EOF`` closes the multiplexer.
"""
import collections
import logging
import struct
import threading
import time

from pwnlib import atexit
from pwnlib.log import getLogger
from pwnlib.timeout import Timeout
from pwnlib.tubes.buffer import Buffer
from pwnlib.tubes.tube import tube

log = getLogger(__name__)

_HEADER = struct.Struct('>BHI')
_U64 = struct.Struct('>Q')

_TYPE_OPEN = 1
_TYPE_OPEN_ACK = 2
_TYPE_DATA = 3
_TYPE_EOF = 4
_TYPE_CLOSE = 5
_TYPE_WINDOW = 6
_TYPE_MUX_EOF = 7

_MAX_MARK = (1 << 64) - 1
_MAX_CHANNEL = 65535


def _pack(frame_type, channel_id, payload):
    payload = bytes(payload)
    if len(payload) > 0xFFFFFFFF:
        raise ValueError('frame exceeds maximum length')
    return _HEADER.pack(frame_type, channel_id, len(payload)) + payload


def _parse_high(payload, default):
    if len(payload) < _U64.size:
        return default
    return _U64.unpack_from(payload)[0]


class _ChannelBuffer(Buffer):
    """Tube buffer that reports consumes and unrecvs to its channel."""

    def __init__(self, channel):
        super(_ChannelBuffer, self).__init__()
        self.channel = channel

    def add(self, data):
        advertise = None
        with self.channel._lock:
            before = self.size
            Buffer.add(self, data)
            delta = self.size - before
            if delta:
                self.channel._after_buffer_growth_locked(delta)
            advertise = self.channel._take_window_locked()
        if advertise is not None:
            self.channel._emit_window(advertise)

    def unget(self, data):
        advertise = None
        with self.channel._lock:
            before = self.size
            Buffer.unget(self, data)
            delta = self.size - before
            if delta:
                self.channel._after_buffer_growth_locked(delta)
            advertise = self.channel._take_window_locked()
        if advertise is not None:
            self.channel._emit_window(advertise)

    def get(self, want=float('inf')):
        advertise = None
        with self.channel._lock:
            before = self.size
            data = Buffer.get(self, want)
            delta = before - self.size
            if delta:
                self.channel._after_consume_locked(delta)
            advertise = self.channel._take_window_locked()
        if advertise is not None:
            self.channel._emit_window(advertise)
        return data


class MuxChannel(tube):
    """One logical stream inside a :class:`TubeMultiplexer`.

    The usual tube ``recv`` / ``send`` interface moves bytes for this
    channel only. ``shutdown('send')`` half-closes the outbound direction.
    ``close()`` closes both directions and signals the peer.
    """

    def __init__(self, mux, channel_id, peer_high=None):
        super(MuxChannel, self).__init__()
        self._mux = mux
        self._channel_id = channel_id
        self._high = mux.high_water_mark
        self._low = mux.low_water_mark
        self.buffer = _ChannelBuffer(self)
        self.buffer.set_watermarks(self._high, self._low)

        self._lock = threading.RLock()
        self._cond = threading.Condition(self._lock)
        self._pending = Buffer()
        self._staged = 0
        self._app_read = 0
        self._paused = False
        self._need_window = False
        self._send_closed = False
        self._recv_closed = False
        self._sent = 0
        self._bytes_sent = 0
        self._bytes_received = 0
        self._frames_sent = 0
        self._frames_received = 0
        self._peer_read = 0
        self._open_event = None
        if peer_high is None:
            self._peer_high = 0
            self._ready = False
        else:
            self._peer_high = peer_high
            self._ready = True

    def __repr__(self):
        return '<MuxChannel id=%d>' % self._channel_id

    @property
    def channel_id(self):
        """Integer id of this channel, in ``[1, 65535]``."""
        return self._channel_id

    @property
    def fully_closed(self):
        return self._send_closed and self._recv_closed

    @property
    def stats(self):
        """Byte and frame counters for this channel.

        ``frames_sent`` increments once per :meth:`send` call.
        ``frames_received`` increments once per data frame delivered
        from the peer.
        """
        with self._lock:
            return {
                'bytes_sent': self._bytes_sent,
                'bytes_received': self._bytes_received,
                'frames_sent': self._frames_sent,
                'frames_received': self._frames_received,
            }

    def _unread_locked(self):
        return self._pending.size + self.buffer.size + self._staged

    def _recalc_pause_locked(self, consumed=False, injected=False):
        unread = self._unread_locked()
        was_paused = self._paused
        if not self._paused and unread >= self._high:
            self._paused = True
        elif self._paused and unread <= self._low:
            self._paused = False

        if self._paused:
            self._need_window = False
        elif consumed or injected or (was_paused and not self._paused):
            self._need_window = True

    def _after_buffer_growth_locked(self, delta):
        staged = min(self._staged, delta)
        self._staged -= staged
        injected = delta - staged
        if injected:
            self._app_read = max(0, self._app_read - injected)
        self._recalc_pause_locked(injected=bool(injected))

    def _after_consume_locked(self, delta):
        self._app_read += delta
        self._recalc_pause_locked(consumed=True)

    def _take_window_locked(self):
        if not self._need_window or self._paused or self._recv_closed:
            if self._paused:
                self._need_window = False
            return None
        self._need_window = False
        return self._app_read

    def _emit_window(self, app_read):
        if self._mux._closed:
            return
        try:
            self._mux._write_frame(_TYPE_WINDOW, self._channel_id, _U64.pack(app_read))
        except EOFError:
            pass

    def _deliver(self, payload):
        with self._lock:
            if self._recv_closed:
                return
            if payload:
                self._pending.add(payload)
            self._bytes_received += len(payload)
            self._frames_received += 1
            self._recalc_pause_locked()
            self._cond.notify_all()

    def _complete_handshake(self, peer_high):
        with self._lock:
            self._peer_high = peer_high
            self._ready = True
            self._cond.notify_all()
        if self._open_event is not None:
            self._open_event.set()

    def _remote_eof(self):
        with self._lock:
            self._recv_closed = True
            self._cond.notify_all()

    def _remote_close(self):
        with self._lock:
            self._send_closed = True
            self._recv_closed = True
            self._cond.notify_all()
        self._mux._drop_unaccepted(self)

    def _on_window(self, app_read):
        with self._lock:
            self._peer_read = app_read
            self._cond.notify_all()

    def _on_mux_closed(self):
        with self._lock:
            self._send_closed = True
            self._recv_closed = True
            self._ready = True
            self._cond.notify_all()
        if self._open_event is not None:
            self._open_event.set()

    def _deadline(self):
        timeout = self.timeout
        if timeout is None or timeout >= Timeout.maximum:
            return None
        return time.time() + timeout

    def _wait_locked(self, deadline):
        if deadline is None:
            self._cond.wait()
            return
        remaining = deadline - time.time()
        if remaining > 0:
            self._cond.wait(remaining)

    def recv_raw(self, numb):
        if numb is None or numb < 0:
            numb = self.buffer.get_fill_size()
        deadline = self._deadline()
        with self._lock:
            while self._pending.size == 0 and not self._recv_closed:
                if deadline is not None and time.time() >= deadline:
                    return b''
                self._wait_locked(deadline)
            if self._pending.size == 0:
                raise EOFError('channel closed')
            data = self._pending.get(numb)
            self._staged += len(data)
            return data

    def send_raw(self, data):
        data = bytes(data)
        deadline = self._deadline()
        offset = 0
        total = len(data)
        # One user send() is one frame when it fits in the peer window.
        # Larger sends are split so a full buffer pauses this channel only.
        while True:
            with self._lock:
                if self._send_closed or self._mux._closed:
                    raise EOFError('channel closed')
                if not self._ready:
                    if deadline is not None and time.time() >= deadline:
                        raise TimeoutError('timed out waiting for channel handshake')
                    self._wait_locked(deadline)
                    continue
                if total == 0:
                    take = 0
                else:
                    credit = self._peer_read + self._peer_high - self._sent
                    if credit <= 0:
                        if deadline is not None and time.time() >= deadline:
                            raise TimeoutError('timed out waiting for receiver window')
                        self._wait_locked(deadline)
                        continue
                    take = min(total - offset, credit, 0xFFFFFFFF)
                self._sent += take
                chunk = data[offset:offset + take]
            try:
                self._mux._write_frame(_TYPE_DATA, self._channel_id, chunk)
            except EOFError:
                with self._lock:
                    self._sent -= take
                raise EOFError('channel closed')
            offset += take
            if offset >= total:
                break
        with self._lock:
            self._bytes_sent += total
            self._frames_sent += 1

    def settimeout_raw(self, timeout):
        return None

    def can_recv_raw(self, timeout):
        if timeout is Timeout.default or timeout is tube.default:
            seconds = 0
        elif timeout is None or timeout >= Timeout.maximum:
            seconds = None
        else:
            seconds = float(timeout)
        deadline = None if seconds is None else time.time() + seconds
        with self._lock:
            while self._pending.size == 0 and not self._recv_closed:
                if deadline is not None and time.time() >= deadline:
                    return False
                self._wait_locked(deadline)
            return self._pending.size > 0

    def connected_raw(self, direction):
        if direction == 'send':
            return not self._send_closed
        if direction == 'recv':
            return not self._recv_closed
        return not self.fully_closed

    def close(self):
        self._shutdown_local(both=True)

    def shutdown_raw(self, direction):
        if direction == 'send':
            self._shutdown_local(both=False)
            return
        if direction == 'recv':
            with self._lock:
                if self._recv_closed:
                    return
                self._recv_closed = True
                self._cond.notify_all()
            return
        raise ValueError('unknown shutdown direction %r' % (direction,))

    def _shutdown_local(self, both):
        with self._lock:
            if both:
                if self._send_closed and self._recv_closed and getattr(self, '_close_sent', False):
                    return
                self._send_closed = True
                self._recv_closed = True
                already = getattr(self, '_close_sent', False)
                self._close_sent = True
                frame_type = _TYPE_CLOSE
            else:
                if self._send_closed:
                    return
                self._send_closed = True
                already = getattr(self, '_eof_sent', False)
                self._eof_sent = True
                frame_type = _TYPE_EOF
            self._cond.notify_all()
        if not already and not self._mux._closed:
            try:
                self._mux._write_frame(frame_type, self._channel_id, b'')
            except EOFError:
                pass


class TubeMultiplexer(object):
    """Carry many :class:`MuxChannel` streams over one tube.

    Arguments:
        underlying(tube): Tube that already connects two peers.
        max_channels(int): Maximum number of simultaneously open channels.
            Must be in ``[1, 65535]``.
        high_water_mark(int): Pause a remote sender when a channel's unread
            buffer reaches this size. Default is 1 MiB.
        low_water_mark(int): Resume that sender once the buffer drains to
            this size. Default is 256 KiB.

    Examples:

        >>> from pwnlib.context import context
        >>> from pwnlib.tubes.listen import listen
        >>> from pwnlib.tubes.remote import remote
        >>> with context.local(log_level='error'):
        ...     lst = listen(0)
        ...     client = remote('127.0.0.1', lst.lport)
        ...     server = lst.wait_for_connection()
        ...     ma, mb = client.mux(high_water_mark=64, low_water_mark=16), server.mux()
        ...     left = ma.open_channel()
        ...     right = mb.accept_channel(timeout=2)
        ...     left.send(b'ping')
        ...     got = right.recv()
        ...     ma.close(); mb.close()
        >>> got
        b'ping'
    """

    def __init__(self, underlying, max_channels=256, high_water_mark=1048576,
                 low_water_mark=262144):
        if not isinstance(underlying, tube):
            raise TypeError('underlying must be a tube, got %s' % type(underlying).__name__)
        if (isinstance(max_channels, bool) or not isinstance(max_channels, int)
                or not 1 <= max_channels <= _MAX_CHANNEL):
            raise ValueError('max_channels must be an integer in [1, 65535]')
        try:
            marks_ordered = low_water_mark > high_water_mark
        except TypeError:
            raise ValueError('high_water_mark and low_water_mark must be ordered numbers')
        if marks_ordered:
            raise ValueError('low_water_mark cannot be greater than high_water_mark')
        if high_water_mark < 0 or low_water_mark < 0:
            raise ValueError('water marks must be non-negative')
        if high_water_mark > _MAX_MARK or low_water_mark > _MAX_MARK:
            raise ValueError('water marks must fit in a 64-bit unsigned integer')

        self.underlying = underlying
        self._max_channels = max_channels
        self._high_water_mark = high_water_mark
        self._low_water_mark = low_water_mark
        self._channels = {}
        self._accept_queue = collections.deque()
        self._open_events = {}
        self._state_lock = threading.RLock()
        self._state_cv = threading.Condition(self._state_lock)
        self._write_lock = threading.Lock()
        self._closed = False
        self._shutdown_started = False
        self._reader = threading.Thread(
            target=self._reader_main,
            name='mux-reader-%d' % id(self),
        )
        self._reader.daemon = True
        self._reader.start()
        atexit.register(self.close)

    def __repr__(self):
        return '<TubeMultiplexer channels=%d closed=%s>' % (len(self._channels), self._closed)

    @property
    def channels(self):
        """Mapping of ``channel_id`` to :class:`MuxChannel`."""
        return self._channels

    @property
    def high_water_mark(self):
        """Receive-buffer size at which the peer sender is paused."""
        return self._high_water_mark

    @property
    def low_water_mark(self):
        """Receive-buffer size at which a paused peer sender is resumed."""
        return self._low_water_mark

    @property
    def max_channels(self):
        """Maximum number of channels open at once."""
        return self._max_channels

    def _active_count_locked(self):
        return sum(1 for ch in self._channels.values() if not ch.fully_closed)

    def _check_open_locked(self):
        if self._closed:
            raise EOFError('multiplexer is closed')

    def open_channel(self, channel_id=None, timeout=None):
        """Open a channel and wait until the remote multiplexer acknowledges it.

        Arguments:
            channel_id(int): Channel id in ``[1, 65535]``. ``None`` allocates
                the lowest free id.
            timeout(float): Seconds to wait for the acknowledgement. ``None``
                waits indefinitely.

        Returns:
            The new :class:`MuxChannel`.

        Raises:
            TypeError: ``channel_id`` is not an integer.
            ValueError: ``channel_id`` is out of range, already open, or the
                multiplexer is at ``max_channels``.
            TimeoutError: The peer did not acknowledge in time.
            EOFError: This multiplexer is closed.
        """
        if timeout is not None and timeout < 0:
            raise ValueError('timeout cannot be negative')

        event = threading.Event()
        with self._state_lock:
            self._check_open_locked()
            if channel_id is None:
                channel_id = self._allocate_id_locked()
            else:
                self._validate_id(channel_id)
                existing = self._channels.get(channel_id)
                if existing is not None and not existing.fully_closed:
                    raise ValueError('channel %d is already open' % channel_id)
                if self._active_count_locked() >= self._max_channels:
                    raise ValueError('channel capacity exceeded')
            channel = MuxChannel(self, channel_id)
            channel._open_event = event
            self._channels[channel_id] = channel
            self._open_events[channel_id] = event

        try:
            self._write_frame(
                _TYPE_OPEN, channel_id, _U64.pack(int(self._high_water_mark) & _MAX_MARK))
        except EOFError:
            with self._state_lock:
                self._channels.pop(channel_id, None)
                self._open_events.pop(channel_id, None)
            raise EOFError('multiplexer is closed')

        signaled = event.wait(timeout)
        with self._state_lock:
            if channel._ready and not self._closed and not channel.fully_closed:
                self._open_events.pop(channel_id, None)
                return channel
            closed = self._closed
            current = self._channels.get(channel_id)
            if current is channel and not channel._ready:
                self._channels.pop(channel_id, None)
            self._open_events.pop(channel_id, None)

        if closed or channel.fully_closed:
            raise EOFError('multiplexer is closed')
        if not signaled or not channel._ready:
            try:
                self._write_frame(_TYPE_CLOSE, channel_id, b'')
            except EOFError:
                raise EOFError('multiplexer is closed')
            raise TimeoutError('timed out waiting for channel acknowledgement')
        return channel

    def accept_channel(self, timeout=None):
        """Wait for the remote multiplexer to open a channel.

        Arguments:
            timeout(float): Seconds to wait. ``None`` waits indefinitely.
                ``0`` returns immediately when no channel is pending.

        Returns:
            A :class:`MuxChannel`, or ``None`` if ``timeout`` elapsed.

        Raises:
            EOFError: This multiplexer is closed.
        """
        if timeout is not None and timeout < 0:
            raise ValueError('timeout cannot be negative')
        deadline = None if timeout is None else time.time() + timeout
        with self._state_lock:
            while not self._accept_queue:
                if self._closed:
                    raise EOFError('multiplexer is closed')
                if deadline is not None:
                    remaining = deadline - time.time()
                    if remaining <= 0:
                        return None
                else:
                    remaining = None
                if not self._state_cv.wait(remaining):
                    if self._closed:
                        raise EOFError('multiplexer is closed')
                    if not self._accept_queue:
                        return None
            if self._closed:
                raise EOFError('multiplexer is closed')
            return self._accept_queue.popleft()

    def close(self):
        """Close every channel and the underlying tube.

        The peer observes the shutdown even if it is idle. A thread blocked
        in :meth:`accept_channel` is woken with :class:`EOFError`. Calling
        :meth:`close` again does nothing.
        """
        self._shutdown(notify_peer=True)

    def _shutdown(self, notify_peer):
        with self._state_lock:
            if self._shutdown_started:
                self._state_cv.notify_all()
                for event in self._open_events.values():
                    event.set()
                return
            self._shutdown_started = True
            self._closed = True
            events = list(self._open_events.values())
            channels = list(self._channels.values())
            self._accept_queue.clear()
            self._state_cv.notify_all()
            for event in events:
                event.set()
        for channel in channels:
            channel._on_mux_closed()
        if notify_peer:
            packet = _pack(_TYPE_MUX_EOF, 0, b'')
            with self._write_lock:
                try:
                    self.underlying.send(packet)
                except Exception:
                    pass
        try:
            self.underlying.close()
        except Exception:
            pass

    def _drop_unaccepted(self, channel):
        with self._state_lock:
            try:
                self._accept_queue.remove(channel)
            except ValueError:
                return
            self._state_cv.notify_all()

    def _write_frame(self, frame_type, channel_id, payload):
        packet = _pack(frame_type, channel_id, payload)
        with self._write_lock:
            if self._closed:
                raise EOFError('multiplexer is closed')
            try:
                self.underlying.send(packet)
            except EOFError:
                raise EOFError('multiplexer is closed')
            except Exception as exc:
                raise EOFError('multiplexer write failed') from exc

    def _allocate_id_locked(self):
        if self._active_count_locked() >= self._max_channels:
            raise ValueError('channel capacity exceeded')
        used = {cid for cid, ch in self._channels.items() if not ch.fully_closed}
        for channel_id in range(1, _MAX_CHANNEL + 1):
            if channel_id not in used:
                return channel_id
        raise ValueError('no channel ids available')

    @staticmethod
    def _validate_id(channel_id):
        if isinstance(channel_id, bool) or not isinstance(channel_id, int):
            raise TypeError('channel_id must be an integer')
        if channel_id < 1 or channel_id > _MAX_CHANNEL:
            raise ValueError('channel_id %r is out of range' % (channel_id,))

    def _reader_main(self):
        pending = bytearray()
        try:
            while not self._closed:
                if len(pending) < _HEADER.size:
                    chunk = self._read_some()
                    if not chunk:
                        continue
                    pending += chunk
                    continue
                frame_type, channel_id, length = _HEADER.unpack_from(pending)
                need = _HEADER.size + length
                while len(pending) < need:
                    chunk = self._read_some()
                    if not chunk:
                        continue
                    pending += chunk
                payload = bytes(pending[_HEADER.size:need])
                del pending[:need]
                if self._closed:
                    break
                self._dispatch(frame_type, channel_id, payload)
        except EOFError:
            pass
        except Exception:
            log.exception('tube multiplexer reader failed')
        finally:
            # Tell the peer explicitly. A local close() of the underlying
            # socket often does not unblock a concurrent recv, so FIN alone
            # can be delayed until that recv returns.
            self._shutdown(notify_peer=True)

    def _read_some(self):
        """Return the next bytes from the underlying tube.

        A short timeout keeps the read interruptible. ``close()`` on a
        socket from another thread does not reliably wake a blocking
        ``recv``, but it does clear the tube's connected state, which we
        observe between polls. A remote hangup still arrives as
        ``EOFError`` or an empty read on a disconnected tube.
        """
        if self._closed:
            raise EOFError('multiplexer is closed')
        try:
            chunk = self.underlying.recv(4096, timeout=0.05)
        except EOFError:
            raise EOFError('underlying tube closed')
        except OSError:
            raise EOFError('underlying tube closed')
        if chunk:
            return chunk
        if self._closed or not self._underlying_recv_open():
            raise EOFError('underlying tube closed')
        return b''

    def _underlying_recv_open(self):
        try:
            return self.underlying.connected('recv')
        except Exception:
            return False

    def _dispatch(self, frame_type, channel_id, payload):
        if frame_type == _TYPE_MUX_EOF:
            self._shutdown(notify_peer=False)
            return
        if frame_type == _TYPE_OPEN:
            self._handle_open(channel_id, payload)
            return
        channel = self._channels.get(channel_id)
        if channel is None:
            return
        if frame_type == _TYPE_OPEN_ACK:
            channel._complete_handshake(_parse_high(payload, self._high_water_mark))
        elif frame_type == _TYPE_DATA:
            channel._deliver(payload)
        elif frame_type == _TYPE_EOF:
            channel._remote_eof()
        elif frame_type == _TYPE_CLOSE:
            channel._remote_close()
        elif frame_type == _TYPE_WINDOW:
            if len(payload) >= _U64.size:
                channel._on_window(_U64.unpack_from(payload)[0])

    def _handle_open(self, channel_id, payload):
        ack = False
        with self._state_lock:
            if self._closed:
                return
            if isinstance(channel_id, bool) or not isinstance(channel_id, int):
                return
            if channel_id < 1 or channel_id > _MAX_CHANNEL:
                return
            existing = self._channels.get(channel_id)
            if existing is not None and not existing.fully_closed:
                return
            if self._active_count_locked() >= self._max_channels:
                return
            peer_high = _parse_high(payload, self._high_water_mark)
            channel = MuxChannel(self, channel_id, peer_high=peer_high)
            self._channels[channel_id] = channel
            self._accept_queue.append(channel)
            self._state_cv.notify_all()
            ack = True
        if ack:
            try:
                self._write_frame(
                    _TYPE_OPEN_ACK, channel_id,
                    _U64.pack(int(self._high_water_mark) & _MAX_MARK))
            except EOFError:
                pass
