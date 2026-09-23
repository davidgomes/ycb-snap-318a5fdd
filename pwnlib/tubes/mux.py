"""Multiplex many bidirectional channels over one tube.

A :class:`TubeMultiplexer` frames an underlying
:class:`~pwnlib.tubes.tube.tube` so each :class:`MuxChannel` can send and
receive as its own tube. Channels are independent: flow control, EOF, and
closure on one channel do not stall the others.

Wire format (big-endian)::

    uint8  type
    uint8  flags
    uint16 channel_id
    uint32 length
    bytes  payload[length]
"""
import struct
import threading
import time

from pwnlib.context import context
from pwnlib.log import getLogger
from pwnlib.timeout import Timeout
from pwnlib.tubes.buffer import Buffer
from pwnlib.tubes.tube import tube

log = getLogger(__name__)

_HEADER = struct.Struct('>BBHI')

_OPEN = 1
_OPEN_ACK = 2
_DATA = 3
_EOF = 4
_CLOSE = 5
_PAUSE = 6
_RESUME = 7
_WINDOW = 8
_MUX_CLOSE = 9
_OPEN_FAIL = 10

# Split only very large writes. Smaller sends stay a single data frame so
# frames_sent and frames_received stay in step unless flow control intervenes.
_MAX_CHUNK = 8 * 1024 * 1024
_MAX_FRAME = 32 * 1024 * 1024

_MIN_CHANNEL_ID = 1
_MAX_CHANNEL_ID = 65535


class _WriteSlot(object):
    __slots__ = ('frame', 'done', 'error')

    def __init__(self, frame):
        self.frame = frame
        self.done = threading.Event()
        self.error = None


class _ChannelBuffer(Buffer):
    """Buffer that reports consumption back to its :class:`MuxChannel`."""

    def __init__(self, channel):
        super(_ChannelBuffer, self).__init__()
        self._channel = channel

    def add(self, data):
        if isinstance(data, Buffer):
            n = data.size
        else:
            n = len(data) if data else 0
        super(_ChannelBuffer, self).add(data)
        if n:
            self._channel._on_buffered(n)

    def get(self, want=float('inf')):
        data = super(_ChannelBuffer, self).get(want)
        if data:
            self._channel._on_consumed(len(data))
        return data

    def unget(self, data):
        if isinstance(data, Buffer):
            n = data.size
        else:
            n = len(data) if data else 0
        super(_ChannelBuffer, self).unget(data)
        if n:
            self._channel._on_unget(n)


class MuxChannel(tube):
    """One logical channel on a :class:`TubeMultiplexer`.

    The usual tube operations (:meth:`send`, :meth:`recv`, :meth:`shutdown`,
    :meth:`close`) apply to this channel only.
    """

    def __init__(self, mux, channel_id, timeout=Timeout.default):
        super(MuxChannel, self).__init__(timeout)
        self._mux = mux
        self._channel_id = channel_id
        self.buffer = _ChannelBuffer(self)
        self.buffer.set_watermarks(mux.high_water_mark, mux.low_water_mark)

        self._cv = threading.Condition()
        self._rx = bytearray()
        self._queued = 0
        self._to_buffer = 0
        self._buffered = 0

        self._credit = 0
        self._paused = False
        self._peer_blocked = False

        self._send_closed = False
        self._recv_eof = False
        self._acked = False
        self._failed = False
        self._close_sent = False

        self._stats = {
            'bytes_sent': 0,
            'bytes_received': 0,
            'frames_sent': 0,
            'frames_received': 0,
        }

    @property
    def channel_id(self):
        """Integer id of this channel, in ``[1, 65535]``."""
        return self._channel_id

    @property
    def stats(self):
        """Copy of traffic counters for this channel.

        ``frames_sent`` increases once per :meth:`send` call.
        ``frames_received`` increases once per data frame delivered
        from the remote peer.
        """
        with self._cv:
            return dict(self._stats)

    def _unread(self):
        return self._queued + self._to_buffer + self._buffered

    def _wait_timeout(self):
        """Return a finite wait or ``None`` when the tube timeout is infinite."""
        timeout = self.timeout
        if timeout is None or timeout >= Timeout.maximum:
            return None
        return timeout

    def _signal_pause(self):
        """Caller holds ``_cv``. Return True if a PAUSE frame should be sent."""
        if self._peer_blocked:
            return False
        if self._unread() >= self._mux.high_water_mark:
            self._peer_blocked = True
            return True
        return False

    def _enqueue(self, data):
        pause = False
        with self._cv:
            if self._recv_eof:
                return False
            if data:
                self._rx.extend(data)
                self._queued += len(data)
                self._stats['bytes_received'] += len(data)
            self._stats['frames_received'] += 1
            self._cv.notify_all()
            pause = self._signal_pause()
            if pause:
                # Queue PAUSE before releasing so a RESUME from a concurrent
                # recv cannot overtake it on the wire.
                try:
                    self._mux._send_frame(
                        _PAUSE, self._channel_id, b'', control=True, wait=False)
                except EOFError:
                    return False
        return True

    def _on_buffered(self, n):
        with self._cv:
            self._to_buffer -= n
            self._buffered += n

    def _on_unget(self, n):
        with self._cv:
            self._buffered += n
            if self._signal_pause():
                try:
                    self._mux._send_frame(
                        _PAUSE, self._channel_id, b'', control=True, wait=False)
                except EOFError:
                    pass

    def _on_consumed(self, n):
        resume = None
        window = None
        with self._cv:
            self._buffered -= n
            if self._buffered < 0:
                self._buffered = 0
            high = self._mux.high_water_mark
            low = self._mux.low_water_mark
            if self._peer_blocked:
                if self._unread() <= low:
                    self._peer_blocked = False
                    resume = high - self._unread()
                    if resume < 0:
                        resume = 0
            else:
                window = n
            try:
                if resume is not None:
                    self._mux._send_frame(
                        _RESUME, self._channel_id, struct.pack('>Q', int(resume)),
                        control=True, wait=False)
                elif window:
                    self._mux._send_frame(
                        _WINDOW, self._channel_id, struct.pack('>Q', int(window)),
                        control=True, wait=False)
            except EOFError:
                pass

    def _grant_credit(self, amount, absolute=False):
        with self._cv:
            if absolute:
                self._paused = False
                self._credit = amount
            elif self._paused:
                return
            else:
                self._credit += amount
            self._cv.notify_all()

    def _on_pause(self):
        with self._cv:
            self._paused = True
            self._credit = 0
            self._cv.notify_all()

    def _on_peer_eof(self):
        with self._cv:
            self._recv_eof = True
            self._cv.notify_all()
        self._mux._drop_if_idle(self)

    def _on_peer_close(self):
        with self._cv:
            self._send_closed = True
            self._recv_eof = True
            self._cv.notify_all()
        self._mux._unregister(self)

    def _force_eof(self):
        with self._cv:
            self._send_closed = True
            self._recv_eof = True
            self._failed = True
            self._cv.notify_all()

    def _wait_ack(self, timeout):
        """Block until the peer acknowledges the open.

        Returns ``'ok'``, ``'timeout'``, ``'fail'``, or ``'eof'``.
        """
        if timeout is not None:
            timeout = float(timeout)
            if timeout < 0:
                timeout = 0
        deadline = None if timeout is None else time.monotonic() + timeout
        with self._cv:
            while not self._acked and not self._failed and not self._recv_eof and not self._mux.closed:
                if deadline is None:
                    self._cv.wait()
                else:
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        return 'timeout'
                    self._cv.wait(remaining)
            if self._acked:
                return 'ok'
            if self._failed and not self._mux.closed and not self._recv_eof:
                return 'fail'
            if self._mux.closed or self._recv_eof:
                return 'eof'
            return 'timeout'

    def mark_acked(self, credit):
        with self._cv:
            self._credit = credit
            self._acked = True
            self._paused = False
            self._cv.notify_all()

    def mark_rejected(self):
        with self._cv:
            self._failed = True
            self._cv.notify_all()

    def _reserve(self, want):
        timeout = self._wait_timeout()
        deadline = None if timeout is None else time.monotonic() + timeout
        with self._cv:
            while not self._send_closed and (self._paused or self._credit <= 0):
                if deadline is None:
                    self._cv.wait()
                else:
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        raise TimeoutError('timed out waiting for flow-control credit')
                    self._cv.wait(remaining)
            if self._send_closed:
                raise EOFError('channel is closed')
            n = want if want < self._credit else self._credit
            # One send() stays one frame unless flow control splits it.
            if n > _MAX_CHUNK:
                n = _MAX_CHUNK
            self._credit -= n
            return n

    def recv_raw(self, numb):
        if numb is None or numb <= 0:
            numb = 4096
        timeout = self._wait_timeout()
        deadline = None if timeout is None else time.monotonic() + timeout
        with self._cv:
            while not self._rx and not self._recv_eof:
                if deadline is None:
                    self._cv.wait()
                else:
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        return None
                    self._cv.wait(remaining)
            if self._rx:
                n = numb if numb < len(self._rx) else len(self._rx)
                data = bytes(self._rx[:n])
                del self._rx[:n]
                self._queued -= n
                self._to_buffer += n
                return data
            raise EOFError('channel is closed')

    def send_raw(self, data):
        if self._send_closed or self._mux.closed:
            raise EOFError('channel is closed')
        if not data:
            with self._cv:
                if self._send_closed:
                    raise EOFError('channel is closed')
                self._stats['frames_sent'] += 1
            return

        view = memoryview(data)
        offset = 0
        counted = False
        while offset < len(view):
            n = self._reserve(len(view) - offset)
            chunk = view[offset:offset + n]
            self._mux._send_frame(_DATA, self._channel_id, chunk.tobytes(), control=False)
            with self._cv:
                if self._send_closed:
                    # Peer closed after we reserved credit; the bytes were still written.
                    pass
                if not counted:
                    self._stats['frames_sent'] += 1
                    counted = True
                self._stats['bytes_sent'] += n
            offset += n

    def settimeout_raw(self, timeout):
        return None

    def can_recv_raw(self, timeout):
        if timeout is None or timeout >= Timeout.maximum:
            deadline = None
        else:
            deadline = time.monotonic() + float(timeout)
        with self._cv:
            while not self._rx and not self._recv_eof:
                if deadline is None:
                    self._cv.wait()
                else:
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        return False
                    self._cv.wait(remaining)
            return bool(self._rx)

    def connected_raw(self, direction):
        with self._cv:
            send_open = not self._send_closed
            recv_open = not self._recv_eof
        if direction == 'send':
            return send_open
        if direction == 'recv':
            return recv_open
        return send_open or recv_open

    def shutdown_raw(self, direction):
        if direction == 'send':
            self._shutdown_send()
        elif direction == 'recv':
            self._shutdown_recv()

    def _shutdown_send(self):
        with self._cv:
            if self._send_closed:
                return
            self._send_closed = True
            self._cv.notify_all()
        if not self._mux.closed:
            try:
                self._mux._send_frame(_EOF, self._channel_id, b'', control=True)
            except EOFError:
                pass
        self._mux._drop_if_idle(self)

    def _shutdown_recv(self):
        with self._cv:
            if self._recv_eof:
                return
            self._recv_eof = True
            # Drop unread bytes so a later recv does not surface them.
            self._rx.clear()
            self._queued = 0
            self._cv.notify_all()
        self._mux._drop_if_idle(self)

    def close(self):
        send_close = False
        with self._cv:
            if self._send_closed and self._recv_eof and self._close_sent:
                self._cv.notify_all()
                return
            self._send_closed = True
            self._recv_eof = True
            self._cv.notify_all()
            if not self._close_sent:
                self._close_sent = True
                send_close = True
        if send_close and not self._mux.closed:
            try:
                self._mux._send_frame(_CLOSE, self._channel_id, b'', control=True)
            except EOFError:
                pass
        self._mux._unregister(self)


class TubeMultiplexer(object):
    """Share one tube across many independent :class:`MuxChannel` objects.

    Arguments:
        underlying(tube): Tube that carries framed bytes. It is owned by
            the multiplexer until :meth:`close`.
        max_channels(int): Maximum number of channels at once. Must be in
            ``[1, 65535]``.
        high_water_mark(int): Pause a remote sender when unread bytes on a
            channel reach this size. Default is 1 MiB.
        low_water_mark(int): Resume that sender once unread bytes fall to
            this size. Default is 256 KiB.

    Example:

        >>> from pwnlib.tubes.mux import TubeMultiplexer
        >>> TubeMultiplexer(object())
        Traceback (most recent call last):
            ...
        TypeError: underlying must be a tube
        >>> from pwnlib.tubes.tube import tube
        >>> TubeMultiplexer(tube(), max_channels=0)
        Traceback (most recent call last):
            ...
        ValueError: max_channels must be between 1 and 65535
        >>> TubeMultiplexer(tube(), high_water_mark=1, low_water_mark=2)
        Traceback (most recent call last):
            ...
        ValueError: low_water_mark cannot exceed high_water_mark
    """

    def __init__(self, underlying, max_channels=256,
                 high_water_mark=1048576, low_water_mark=262144):
        if not isinstance(underlying, tube):
            raise TypeError('underlying must be a tube')
        if isinstance(max_channels, bool) or not isinstance(max_channels, int):
            raise ValueError('max_channels must be between 1 and 65535')
        if max_channels < 1 or max_channels > _MAX_CHANNEL_ID:
            raise ValueError('max_channels must be between 1 and 65535')
        if low_water_mark > high_water_mark:
            raise ValueError('low_water_mark cannot exceed high_water_mark')

        self._underlying = underlying
        self._max_channels = max_channels
        self._high_water_mark = high_water_mark
        self._low_water_mark = low_water_mark

        self._lock = threading.RLock()
        self._channels = {}
        self._next_id = _MIN_CHANNEL_ID
        self._pending = []
        self._accept_cv = threading.Condition(self._lock)
        self._closed = False

        self._out_cv = threading.Condition()
        self._ctrlq = []
        self._dataq = []
        self._writer_stopped = False

        self._reader = context.Thread(target=self._reader_loop, name='mux-reader')
        self._writer = context.Thread(target=self._writer_loop, name='mux-writer')
        self._reader.daemon = True
        self._writer.daemon = True
        self._writer.start()
        self._reader.start()

    @property
    def channels(self):
        """dict of ``channel_id`` to open :class:`MuxChannel`."""
        with self._lock:
            return dict(self._channels)

    @property
    def high_water_mark(self):
        return self._high_water_mark

    @property
    def low_water_mark(self):
        return self._low_water_mark

    @property
    def closed(self):
        return self._closed

    def open_channel(self, channel_id=None, timeout=None):
        """Open a channel and wait until the remote side acknowledges it.

        Arguments:
            channel_id(int): Channel id in ``[1, 65535]``. ``None`` allocates one.
            timeout(float): Seconds to wait for the acknowledgement. ``None``
                waits forever.

        Raises:
            TypeError: ``channel_id`` is not an integer.
            ValueError: The id is out of range, already in use, or the
                multiplexer is at ``max_channels``.
            TimeoutError: The peer did not acknowledge in time.
            EOFError: The multiplexer is closed, or the peer rejected the open.
        """
        with self._lock:
            if self._closed:
                raise EOFError('multiplexer is closed')
            cid = self._allocate_id(channel_id)
            channel = MuxChannel(self, cid)
            self._channels[cid] = channel

        try:
            self._send_frame(
                _OPEN, cid, struct.pack('>Q', int(self._high_water_mark)), control=True)
        except EOFError:
            self._unregister(channel)
            raise EOFError('multiplexer is closed')

        result = channel._wait_ack(timeout)
        if result == 'ok':
            return channel

        self._unregister(channel)
        if not self._closed:
            try:
                self._send_frame(_CLOSE, cid, b'', control=True, wait=False)
            except EOFError:
                pass
        if result == 'timeout':
            raise TimeoutError('timed out waiting for channel acknowledgement')
        if result == 'fail':
            raise EOFError('remote rejected channel open')
        raise EOFError('multiplexer is closed')

    def accept_channel(self, timeout=None):
        """Wait for the remote side to open a channel.

        Arguments:
            timeout(float): Seconds to wait. ``None`` waits forever.

        Returns:
            The new :class:`MuxChannel`, or ``None`` if ``timeout`` elapsed
            before a channel was opened.

        Raises:
            EOFError: The multiplexer is closed.
        """
        if timeout is not None:
            timeout = float(timeout)
            if timeout < 0:
                timeout = 0
        deadline = None if timeout is None else time.monotonic() + timeout
        with self._accept_cv:
            while not self._pending and not self._closed:
                if deadline is None:
                    self._accept_cv.wait()
                else:
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        return None
                    self._accept_cv.wait(remaining)
            if self._pending:
                return self._pending.pop(0)
            raise EOFError('multiplexer is closed')

    def close(self):
        """Close every channel and the underlying tube.

        The call is idempotent. A thread blocked in :meth:`accept_channel`
        wakes with :class:`EOFError`. The remote multiplexer observes the
        closure even when it is idle.
        """
        channels = self._begin_close()
        if channels is None:
            self._join_io()
            return
        for channel in channels:
            channel._force_eof()
        try:
            self._send_frame(_MUX_CLOSE, 0, b'', control=True, wait=True, timeout=1)
        except (EOFError, TimeoutError):
            pass
        self._fail_pending_writes()
        try:
            self._underlying.close()
        except Exception:
            pass
        self._stop_writer()
        self._join_io()

    def _begin_close(self):
        with self._lock:
            if self._closed:
                return None
            self._closed = True
            channels = list(self._channels.values())
            self._channels.clear()
            self._pending[:] = []
            self._accept_cv.notify_all()
            return channels

    def _allocate_id(self, channel_id):
        """Caller holds ``_lock``."""
        if channel_id is None:
            if len(self._channels) >= self._max_channels:
                raise ValueError('channel limit of %d reached' % self._max_channels)
            cid = self._next_id
            for _ in range(_MAX_CHANNEL_ID):
                if cid > _MAX_CHANNEL_ID:
                    cid = _MIN_CHANNEL_ID
                if cid not in self._channels:
                    self._next_id = cid + 1 if cid < _MAX_CHANNEL_ID else _MIN_CHANNEL_ID
                    return cid
                cid += 1
            raise ValueError('channel limit of %d reached' % self._max_channels)

        if isinstance(channel_id, bool) or not isinstance(channel_id, int):
            raise TypeError('channel_id must be an integer')
        if channel_id < _MIN_CHANNEL_ID or channel_id > _MAX_CHANNEL_ID:
            raise ValueError('channel_id %s is out of range [1, 65535]' % (channel_id,))
        if channel_id in self._channels:
            raise ValueError('channel %s is already open' % (channel_id,))
        if len(self._channels) >= self._max_channels:
            raise ValueError('channel limit of %d reached' % self._max_channels)
        return channel_id

    def _unregister(self, channel):
        with self._lock:
            current = self._channels.get(channel.channel_id)
            if current is channel:
                del self._channels[channel.channel_id]
            if self._pending:
                self._pending = [item for item in self._pending if item is not channel]

    def _drop_if_idle(self, channel):
        with channel._cv:
            idle = channel._send_closed and channel._recv_eof
        if idle:
            self._unregister(channel)

    def _send_frame(self, typ, channel_id, payload, control=True, wait=True, timeout=None):
        if isinstance(payload, memoryview):
            payload = payload.tobytes()
        if len(payload) > _MAX_FRAME:
            raise ValueError('frame exceeds maximum size')
        frame = _HEADER.pack(typ, 0, channel_id & 0xffff, len(payload)) + payload
        slot = _WriteSlot(frame)
        with self._out_cv:
            if typ != _MUX_CLOSE and (self._closed or self._writer_stopped):
                raise EOFError('multiplexer is closed')
            if self._writer_stopped:
                raise EOFError('multiplexer is closed')
            if control:
                self._ctrlq.append(slot)
            else:
                self._dataq.append(slot)
            self._out_cv.notify()
        if not wait:
            return
        if timeout is None:
            slot.done.wait()
        elif not slot.done.wait(timeout):
            raise TimeoutError('timed out writing multiplexer frame')
        if slot.error is not None:
            if isinstance(slot.error, EOFError):
                raise EOFError('multiplexer is closed')
            raise slot.error

    def _fail_pending_writes(self):
        with self._out_cv:
            pending = self._ctrlq + self._dataq
            self._ctrlq[:] = []
            self._dataq[:] = []
        for slot in pending:
            if not slot.done.is_set():
                slot.error = EOFError('multiplexer is closed')
                slot.done.set()

    def _stop_writer(self):
        with self._out_cv:
            self._writer_stopped = True
            self._out_cv.notify_all()

    def _join_io(self):
        current = threading.current_thread()
        if current is not self._reader:
            self._reader.join(timeout=2)
        if current is not self._writer:
            self._writer.join(timeout=2)

    def _pop_slot(self):
        with self._out_cv:
            while not self._ctrlq and not self._dataq and not self._writer_stopped:
                self._out_cv.wait()
            if self._ctrlq:
                return self._ctrlq.pop(0)
            if self._dataq:
                return self._dataq.pop(0)
            return None

    def _writer_loop(self):
        while True:
            slot = self._pop_slot()
            if slot is None:
                return
            try:
                self._underlying.send(slot.frame)
            except Exception as exc:
                slot.error = exc if isinstance(exc, EOFError) else EOFError('multiplexer is closed')
                slot.done.set()
                self._on_transport_lost()
                return
            else:
                slot.done.set()

    def _reader_loop(self):
        try:
            while not self._closed:
                header = self._read_exact(_HEADER.size)
                typ, _flags, channel_id, length = _HEADER.unpack(header)
                if length > _MAX_FRAME:
                    break
                payload = self._read_exact(length) if length else b''
                self._dispatch(typ, channel_id, payload)
        except EOFError:
            pass
        except Exception as exc:
            log.exception('multiplexer reader failed: %s', exc)
        finally:
            self._on_transport_lost()

    def _read_exact(self, n):
        got = []
        have = 0
        while have < n:
            if self._closed:
                raise EOFError('multiplexer is closed')
            with self._underlying.local(Timeout.maximum):
                part = self._underlying.recvn(n - have, timeout=Timeout.maximum)
            if not part:
                if self._closed:
                    raise EOFError('multiplexer is closed')
                continue
            got.append(part)
            have += len(part)
        return b''.join(got)

    def _dispatch(self, typ, channel_id, payload):
        if typ == _MUX_CLOSE:
            self._on_transport_lost()
            return
        if typ == _OPEN:
            self._handle_open(channel_id, payload)
            return
        if typ == _OPEN_ACK:
            self._handle_open_ack(channel_id, payload)
            return
        if typ == _OPEN_FAIL:
            channel = self._channel(channel_id)
            if channel is not None:
                channel.mark_rejected()
            return

        channel = self._channel(channel_id)
        if channel is None:
            return
        if typ == _DATA:
            channel._enqueue(payload)
        elif typ == _PAUSE:
            channel._on_pause()
        elif typ == _RESUME:
            credit = struct.unpack('>Q', payload[:8])[0] if len(payload) >= 8 else 0
            channel._grant_credit(credit, absolute=True)
        elif typ == _WINDOW:
            credit = struct.unpack('>Q', payload[:8])[0] if len(payload) >= 8 else 0
            channel._grant_credit(credit, absolute=False)
        elif typ == _EOF:
            channel._on_peer_eof()
        elif typ == _CLOSE:
            channel._on_peer_close()

    def _channel(self, channel_id):
        with self._lock:
            return self._channels.get(channel_id)

    def _parse_window(self, payload):
        if len(payload) >= 8:
            return struct.unpack_from('>Q', payload)[0]
        return self._high_water_mark

    def _handle_open(self, channel_id, payload):
        reject = False
        channel = None
        with self._lock:
            if self._closed:
                return
            if (channel_id < _MIN_CHANNEL_ID or channel_id > _MAX_CHANNEL_ID
                    or channel_id in self._channels
                    or len(self._channels) >= self._max_channels):
                reject = True
            else:
                channel = MuxChannel(self, channel_id)
                channel._credit = self._parse_window(payload)
                channel._acked = True
                self._channels[channel_id] = channel
                self._pending.append(channel)
                self._accept_cv.notify_all()
        if reject:
            try:
                self._send_frame(_OPEN_FAIL, channel_id, b'', control=True, wait=False)
            except EOFError:
                pass
            return
        try:
            self._send_frame(
                _OPEN_ACK, channel_id,
                struct.pack('>Q', int(self._high_water_mark)),
                control=True)
        except EOFError:
            pass

    def _handle_open_ack(self, channel_id, payload):
        channel = self._channel(channel_id)
        if channel is None:
            return
        channel.mark_acked(self._parse_window(payload))

    def _on_transport_lost(self):
        channels = self._begin_close()
        if channels is None:
            self._stop_writer()
            return
        for channel in channels:
            channel._force_eof()
        self._fail_pending_writes()
        self._stop_writer()
        try:
            self._underlying.close()
        except Exception:
            pass


__all__ = ['TubeMultiplexer', 'MuxChannel']
