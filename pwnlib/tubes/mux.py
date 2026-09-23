"""Multiplex many bidirectional channels over a single tube.

A :class:`TubeMultiplexer` frames a byte stream so each :class:`MuxChannel`
behaves like its own :class:`~pwnlib.tubes.tube.tube`. Channels are independent:
closing or flow-controlling one does not stall the others.

Wire format (big-endian)::

    uint8  type
    uint16 channel_id
    uint32 length
    bytes  payload
"""
import collections
import errno
import select
import socket
import struct
import threading
import time

from pwnlib import atexit
from pwnlib.context import context
from pwnlib.log import getLogger
from pwnlib.timeout import Timeout
from pwnlib.tubes.buffer import Buffer
from pwnlib.tubes.tube import tube

log = getLogger(__name__)

_HEADER = struct.Struct('!BHI')

_OPEN = 1
_OPEN_ACK = 2
_OPEN_NAK = 3
_DATA = 4
_EOF = 5
_CLOSE = 6
_PAUSE = 7
_RESUME = 8
_DATA_ACK = 9
_EOF_ACK = 10
_CLOSE_ACK = 11
_MUX_CLOSE = 12


class _Outbound(object):
    """One frame waiting to be written to the underlying tube."""

    __slots__ = ('packet', 'event', 'ok')

    def __init__(self, packet):
        self.packet = packet
        self.event = threading.Event()
        self.ok = False

_NAK_DUPLICATE = 1
_NAK_CAPACITY = 2
_NAK_INVALID = 3

_MAX_FRAME = 128 * 1024 * 1024

_NAK_MESSAGES = {
    _NAK_DUPLICATE: 'channel id already open on the remote multiplexer',
    _NAK_CAPACITY: 'remote multiplexer channel capacity exceeded',
    _NAK_INVALID: 'remote multiplexer rejected channel id',
}


def _as_timeout_seconds(timeout):
    """Return ``None`` for "wait forever", otherwise a non-negative float."""
    if timeout is None:
        return None
    if timeout is Timeout.default:
        return None
    seconds = float(timeout)
    if seconds < 0:
        raise ValueError('timeout cannot be negative')
    if seconds >= Timeout.maximum:
        return None
    return seconds


class _MuxBuffer(Buffer):
    """Receive buffer that notifies its channel when bytes are consumed."""

    def __init__(self, channel):
        super(_MuxBuffer, self).__init__()
        self._channel = channel

    def get(self, want=float('inf')):
        channel = self._channel
        with channel._cv:
            data = Buffer.get(self, want)
            if data:
                channel._dirty = True
        if data:
            channel._on_buffer_change()
        return data

    def unget(self, data):
        channel = self._channel
        with channel._cv:
            Buffer.unget(self, data)
            channel._dirty = True
        channel._on_buffer_change()


class MuxChannel(tube):
    """One logical stream inside a :class:`TubeMultiplexer`.

    ``send`` writes a single data frame. ``recv`` returns bytes the remote
    side has sent on this channel. ``shutdown('send')`` half-closes the
    channel; ``close`` closes both directions.
    """

    def __init__(self, mux, channel_id, timeout=Timeout.default):
        super(MuxChannel, self).__init__(timeout)
        self._mux = mux
        self._channel_id = channel_id
        self._cv = threading.Condition()
        self._ctrl = threading.Lock()
        self._send_lock = threading.Lock()
        self._send_closed = False
        self._recv_closed = False
        self._local_closed = False
        self._cancelled = False
        self._acked = False
        self._nak = None
        self._open_failed = False
        self._remote_paused = False
        self._we_paused_peer = False
        self._need_ack = False
        self._dirty = False
        self._data_acked = False
        self._eof_acked = False
        self._close_acked = False
        self._eof_ack_sent = False
        self._close_ack_sent = False
        self._bytes_sent = 0
        self._bytes_received = 0
        self._frames_sent = 0
        self._frames_received = 0
        self.buffer = _MuxBuffer(self)
        self.buffer.set_watermarks(high=mux.high_water_mark, low=mux.low_water_mark)

    @property
    def channel_id(self):
        """Numeric id of this channel (``1`` .. ``65535``)."""
        return self._channel_id

    @property
    def stats(self):
        """Byte and frame counters for this channel.

        ``frames_sent`` increments once per :meth:`send`. ``frames_received``
        increments once per data frame delivered by the remote peer.
        """
        with self._cv:
            return {
                'bytes_sent': self._bytes_sent,
                'bytes_received': self._bytes_received,
                'frames_sent': self._frames_sent,
                'frames_received': self._frames_received,
            }

    def _op_timeout(self):
        value = self.timeout
        if value is None or value >= Timeout.maximum:
            return None
        return value

    def _wait_cv_pred(self, predicate, timeout):
        """Wait until ``predicate()`` is true.

        ``predicate`` runs while ``_cv`` is held. *timeout* is seconds, or
        ``None`` to wait forever. Returns True, or None if the timeout elapses.
        """
        if timeout is not None and timeout >= Timeout.maximum:
            timeout = None
        deadline = None if timeout is None else time.monotonic() + timeout
        with self._cv:
            while not predicate():
                if deadline is not None:
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        return None
                    self._cv.wait(remaining)
                else:
                    self._cv.wait()
            return True

    def recv_raw(self, numb):
        """Receive up to *numb* bytes already queued for this channel."""
        if numb is None:
            numb = self.buffer.get_fill_size()
        ready = self._wait_cv_pred(lambda: bool(self.buffer) or self._recv_closed,
                                   self._op_timeout())
        if ready is None:
            return None
        if not self.buffer:
            raise EOFError('channel is closed')
        return self.buffer.get(numb)

    def _recv(self, numb=None, timeout=Timeout.default):
        numb = self.buffer.get_fill_size(numb)
        with self.local(timeout):
            ready = self._wait_cv_pred(lambda: bool(self.buffer) or self._recv_closed,
                                       self._op_timeout())
        if ready is None:
            return b''
        if not self.buffer:
            raise EOFError('channel is closed')
        return self.buffer.get(numb)

    def recvn(self, numb, timeout=Timeout.default):
        numb = int(numb)
        # Pull bytes out of the watermarked buffer as they arrive. Leaving the
        # whole request queued would pin the peer above the high water mark
        # for any read larger than the window.
        got = []
        total = 0
        eof = False
        with self.countdown(timeout):
            while total < numb and self.countdown_active():
                ready = self._wait_cv_pred(
                    lambda: bool(self.buffer) or self._recv_closed,
                    self._op_timeout(),
                )
                if len(self.buffer):
                    part = self.buffer.get(numb - total)
                    if part:
                        got.append(part)
                        total += len(part)
                        continue
                if ready is None or not self.countdown_active():
                    break
                if self._recv_closed:
                    eof = True
                    break
        if total >= numb:
            return b''.join(got)
        if got:
            self.buffer.unget(b''.join(got))
        if eof:
            raise EOFError('channel is closed')
        return b''

    def _fillbuffer(self, timeout=Timeout.default):
        # Used by recvrepeat/recvall to pull more bytes already queued by the
        # reader thread. A generation captured under the wait lock avoids
        # missing bytes that landed before the wait started.
        with self.local(timeout):
            state = {}

            def grew_or_closed():
                if 'start' not in state:
                    state['start'] = len(self.buffer)
                return len(self.buffer) > state['start'] or self._recv_closed

            ready = self._wait_cv_pred(grew_or_closed, self._op_timeout())
            start = state.get('start', 0)
        if ready is None:
            return b''
        if len(self.buffer) > start:
            return b'\x01'
        raise EOFError('channel is closed')

    def send_raw(self, data):
        if not isinstance(data, (bytes, bytearray)):
            data = bytes(data)
        else:
            data = bytes(data)
        with self._send_lock:
            self._wait_writable()
            with self._cv:
                if self._send_closed:
                    raise EOFError('channel is closed')
                self._data_acked = False
            # One frame per send(), so a peer blocked in recvn() still sees
            # the whole payload once this frame arrives. PAUSE/DATA_ACK is
            # observed before we return, which is what makes the next send
            # honor flow control.
            self._mux._write_frame(_DATA, self.channel_id, data)
            with self._cv:
                while not self._data_acked and not self._send_closed:
                    self._cv.wait()
                if not self._data_acked:
                    raise EOFError('channel is closed')
                self._frames_sent += 1
                self._bytes_sent += len(data)

    def _wait_writable(self):
        timeout = self._op_timeout()
        deadline = None if timeout is None else time.monotonic() + timeout
        with self._cv:
            while self._remote_paused and not self._send_closed:
                if deadline is not None:
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        raise TimeoutError('timed out waiting for flow control')
                    self._cv.wait(remaining)
                else:
                    self._cv.wait()
            if self._send_closed:
                raise EOFError('channel is closed')

    def settimeout_raw(self, timeout):
        return None

    def can_recv_raw(self, timeout):
        seconds = _as_timeout_seconds(timeout)
        ready = self._wait_cv_pred(lambda: bool(self.buffer) or self._recv_closed, seconds)
        if ready is None:
            return False
        return bool(self.buffer)

    def connected_raw(self, direction):
        with self._cv:
            send_ok = not self._send_closed
            recv_ok = not self._recv_closed
        if direction == 'send':
            return send_ok
        if direction == 'recv':
            return recv_ok
        if direction == 'any':
            return send_ok or recv_ok
        return False

    def shutdown_raw(self, direction):
        if direction == 'recv':
            with self._cv:
                self._recv_closed = True
                self._cv.notify_all()
            return
        if direction != 'send':
            return
        # Mark the direction closed before taking ``_send_lock`` so a sender
        # blocked on flow control wakes, raises, and releases that lock.
        with self._cv:
            if self._send_closed:
                return
            self._send_closed = True
            self._eof_acked = False
            self._cv.notify_all()
        with self._send_lock:
            if self._mux._closed or self._mux._closing:
                return
            try:
                self._mux._write_frame(_EOF, self.channel_id, b'')
                self._wait_flag('_eof_acked')
            except EOFError:
                pass

    def close(self):
        """Close both directions and signal EOF to the remote peer."""
        try:
            self._close_impl()
        except Exception:
            with self._cv:
                self._send_closed = True
                self._recv_closed = True
                self._local_closed = True
                self._cv.notify_all()

    def _close_impl(self):
        with self._cv:
            if self._local_closed:
                return
            self._local_closed = True
            self._send_closed = True
            self._recv_closed = True
            self._close_acked = False
            self._cv.notify_all()
        with self._send_lock:
            if not self._mux._closed and not self._mux._closing:
                try:
                    self._mux._write_frame(_CLOSE, self.channel_id, b'')
                    self._wait_flag('_close_acked')
                except EOFError:
                    pass
        self._mux._discard_channel(self)

    def _wait_flag(self, name):
        with self._cv:
            while not getattr(self, name) and not self._mux._closed and not self._mux._closing:
                self._cv.wait()

    def force_local_eof(self):
        """Unblock local waiters because the multiplexer is gone."""
        with self._cv:
            self._send_closed = True
            self._recv_closed = True
            self._local_closed = True
            self._open_failed = True
            self._eof_acked = True
            self._close_acked = True
            self._cv.notify_all()

    def _deliver(self, payload, final=True):
        if not isinstance(payload, (bytes, bytearray)):
            payload = bytes(payload)
        else:
            payload = bytes(payload)
        with self._cv:
            if not self._recv_closed:
                if payload:
                    Buffer.add(self.buffer, payload)
                self._bytes_received += len(payload)
                if final:
                    self._frames_received += 1
                self._cv.notify_all()
            self._need_ack = True
            self._dirty = True
        self._kick()

    def _on_buffer_change(self):
        try:
            self._kick()
        except EOFError:
            pass

    def _on_data_ack(self):
        with self._cv:
            self._remote_paused = False
            self._data_acked = True
            self._cv.notify_all()

    def _on_pause(self):
        with self._cv:
            self._remote_paused = True
            self._data_acked = True
            self._cv.notify_all()

    def _on_resume(self):
        with self._cv:
            self._remote_paused = False
            self._cv.notify_all()

    def _on_remote_eof(self):
        send_ack = False
        with self._cv:
            self._recv_closed = True
            if not self._eof_ack_sent:
                self._eof_ack_sent = True
                send_ack = True
            self._cv.notify_all()
        if send_ack:
            try:
                self._mux._write_frame(_EOF_ACK, self.channel_id, b'', wait=False)
            except EOFError:
                pass

    def _on_eof_ack(self):
        with self._cv:
            self._eof_acked = True
            self._cv.notify_all()

    def _on_remote_close(self):
        send_ack = False
        with self._cv:
            self._send_closed = True
            self._recv_closed = True
            self._local_closed = True
            self._close_acked = True
            if not self._close_ack_sent:
                self._close_ack_sent = True
                send_ack = True
            self._cv.notify_all()
        self._mux._discard_channel(self)
        if send_ack:
            try:
                self._mux._write_frame(_CLOSE_ACK, self.channel_id, b'', wait=False)
            except EOFError:
                pass

    def _on_close_ack(self):
        with self._cv:
            self._close_acked = True
            self._cv.notify_all()

    def _kick(self):
        """Send the flow-control / data-ack frames implied by the buffer."""
        with self._ctrl:
            while True:
                frames = self._plan_frames()
                for ftype in frames:
                    # Never block the reader on the underlying send. Control
                    # frames are queued ahead of user data and written by the
                    # dedicated writer thread.
                    self._mux._write_frame(ftype, self.channel_id, b'', wait=False)
                with self._cv:
                    if not self._dirty and not self._need_ack:
                        return

    def _plan_frames(self):
        with self._cv:
            self._dirty = False
            over = self.buffer.over_high_water
            under = self.buffer.under_low_water
            should_pause = over and not under
            became_resume = False
            became_pause = False
            if self._we_paused_peer:
                if under:
                    self._we_paused_peer = False
                    became_resume = True
            elif should_pause:
                self._we_paused_peer = True
                became_pause = True
            need_ack = self._need_ack
            if need_ack:
                self._need_ack = False
            frames = []
            if need_ack and self._we_paused_peer:
                frames.append(_PAUSE)
            elif need_ack:
                frames.append(_DATA_ACK)
            elif became_pause:
                frames.append(_PAUSE)
            if became_resume:
                frames.append(_RESUME)
            return frames


class TubeMultiplexer(object):
    """Multiplex logical tubes over a single underlying tube.

    Arguments:
        underlying(tube): Connected tube owned by the multiplexer.
        max_channels(int): Maximum number of simultaneously open channels.
        high_water_mark(int): Pause a remote sender at this receive-buffer size.
        low_water_mark(int): Resume a remote sender at or below this size.

    Examples:

        >>> from pwnlib.context import context
        >>> context.log_level = 'error'
        >>> from pwnlib.tubes.listen import listen
        >>> from pwnlib.tubes.remote import remote
        >>> listener = listen(bindaddr='127.0.0.1', fam='ipv4')
        >>> remote_tube = remote('127.0.0.1', listener.lport)
        >>> _ = listener.wait_for_connection()
        >>> left = listener.mux(max_channels=4, high_water_mark=64, low_water_mark=16)
        >>> right = remote_tube.mux(max_channels=4, high_water_mark=64, low_water_mark=16)
        >>> opened = right.open_channel(7)
        >>> accepted = left.accept_channel(timeout=2)
        >>> accepted.channel_id == opened.channel_id == 7
        True
        >>> opened.send(b'hello mux')
        >>> accepted.recv(timeout=2)
        b'hello mux'
        >>> accepted.stats['bytes_received']
        9
        >>> opened.stats['frames_sent']
        1
        >>> opened.close()
        >>> accepted.connected()
        False
        >>> left.close()
        >>> right.close()
        >>> TubeMultiplexer(object())
        Traceback (most recent call last):
            ...
        TypeError: underlying must be a tube
        >>> TubeMultiplexer(listener, max_channels=0)
        Traceback (most recent call last):
            ...
        ValueError: max_channels must be an integer in the range [1, 65535]
        >>> TubeMultiplexer(listener, high_water_mark=1, low_water_mark=2)
        Traceback (most recent call last):
            ...
        ValueError: low_water_mark cannot exceed high_water_mark
    """

    def __init__(self, underlying, max_channels=256, high_water_mark=1048576,
                 low_water_mark=262144):
        if not isinstance(underlying, tube):
            raise TypeError('underlying must be a tube')
        if isinstance(max_channels, bool) or not isinstance(max_channels, int):
            raise ValueError('max_channels must be an integer in the range [1, 65535]')
        if max_channels < 1 or max_channels > 65535:
            raise ValueError('max_channels must be an integer in the range [1, 65535]')
        if low_water_mark > high_water_mark:
            raise ValueError('low_water_mark cannot exceed high_water_mark')

        self._underlying = underlying
        self._max_channels = max_channels
        self._high_water_mark = high_water_mark
        self._low_water_mark = low_water_mark
        self._lock = threading.RLock()
        self._accept_cv = threading.Condition(self._lock)
        self._state_cv = threading.Condition(self._lock)
        self._out_cv = threading.Condition()
        self._ctrlq = collections.deque()
        self._dataq = collections.deque()
        self._channels = {}
        self._accept_queue = []
        self._closed = False
        self._closing = False
        self._reader_error = None
        self._enable_nodelay(underlying)

        # The writer is the only thread that touches the underlying send
        # path. The reader only reads, so a large send cannot stall acks
        # or hide a peer FIN.
        self._writer = context.Thread(target=self._writer_loop, name='tube-mux-writer')
        self._writer.daemon = True
        self._writer.start()
        self._reader = context.Thread(target=self._reader_loop, name='tube-mux')
        self._reader.daemon = True
        self._reader.start()
        atexit.register(self.close)

    @staticmethod
    def _enable_nodelay(underlying):
        # ``listen`` and ``ssh`` tubes implement ``sock`` as a property that
        # blocks until a peer connects. Only a socket already installed on
        # the instance is safe to touch here.
        sock_obj = getattr(underlying, '__dict__', {}).get('sock')
        if sock_obj is None:
            return
        try:
            sock_obj.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
        except Exception:
            pass

    @property
    def channels(self):
        """Map of channel id to open :class:`MuxChannel` objects."""
        with self._lock:
            return dict(self._channels)

    @property
    def high_water_mark(self):
        """Receive-buffer size at which the peer sender is paused."""
        return self._high_water_mark

    @property
    def low_water_mark(self):
        """Receive-buffer size at which a paused peer sender is resumed."""
        return self._low_water_mark

    def open_channel(self, channel_id=None, timeout=None):
        """Open a channel and wait until the remote multiplexer acknowledges it.

        Arguments:
            channel_id(int): Id to open, or ``None`` to allocate one.
            timeout(float): Seconds to wait for acknowledgement. ``None`` waits
                forever.

        Returns:
            The new :class:`MuxChannel`.

        Raises:
            TypeError: *channel_id* is not an integer.
            ValueError: *channel_id* is out of range, already open, or the
                multiplexer is at ``max_channels``.
            TimeoutError: The peer did not acknowledge in time.
            EOFError: This multiplexer is closed.
        """
        if channel_id is not None:
            self._validate_channel_id(channel_id)
        seconds = _as_timeout_seconds(timeout)
        deadline = None if seconds is None else time.monotonic() + seconds

        with self._lock:
            if self._closed or self._closing:
                raise EOFError('multiplexer is closed')
            if len(self._channels) >= self._max_channels:
                raise ValueError('maximum number of channels exceeded')
            if channel_id is None:
                channel_id = self._allocate_id()
            elif channel_id in self._channels:
                raise ValueError('channel id %d is already in use' % channel_id)
            channel = MuxChannel(self, channel_id)
            self._channels[channel_id] = channel

        try:
            self._write_frame(_OPEN, channel_id, b'')
        except EOFError:
            self._discard_channel(channel)
            raise EOFError('multiplexer is closed')

        with self._state_cv:
            while not channel._acked and channel._nak is None and not channel._open_failed \
                    and not self._closed and not self._closing:
                if deadline is not None:
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        break
                    self._state_cv.wait(remaining)
                else:
                    self._state_cv.wait()
            if channel._acked and not self._closed and not self._closing:
                return channel
            if self._closed or self._closing or channel._open_failed:
                self._channels.pop(channel_id, None)
                raise EOFError('multiplexer is closed')
            if channel._nak is not None:
                reason = channel._nak
                self._channels.pop(channel_id, None)
                channel._cancelled = True
                raise ValueError(_NAK_MESSAGES.get(reason, 'remote multiplexer rejected channel'))
            # Timed out. Drop the local reservation and tell the peer, which
            # may already have accepted the OPEN.
            self._channels.pop(channel_id, None)
            channel._cancelled = True
            channel.force_local_eof()

        try:
            self._write_frame(_CLOSE, channel_id, b'')
        except EOFError:
            pass
        raise TimeoutError('timed out waiting for channel open acknowledgement')

    def accept_channel(self, timeout=None):
        """Wait for the remote side to open a channel.

        Arguments:
            timeout(float): Seconds to wait. ``None`` waits forever.

        Returns:
            A :class:`MuxChannel`, or ``None`` if *timeout* elapses.

        Raises:
            EOFError: This multiplexer is closed.
        """
        seconds = _as_timeout_seconds(timeout)
        deadline = None if seconds is None else time.monotonic() + seconds
        with self._accept_cv:
            while not self._accept_queue and not self._closed and not self._closing:
                if deadline is not None:
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        return None
                    self._accept_cv.wait(remaining)
                else:
                    self._accept_cv.wait()
            if self._closed or self._closing:
                raise EOFError('multiplexer is closed')
            if not self._accept_queue:
                return None
            return self._accept_queue.pop(0)

    def close(self):
        """Close every channel and the underlying tube.

        Safe to call more than once. A thread blocked in :meth:`accept_channel`
        is woken with :class:`EOFError`.
        """
        with self._lock:
            if self._closed or self._closing:
                return
            self._closing = True
        on_writer = threading.current_thread() is getattr(self, '_writer', None)
        if not on_writer:
            try:
                self._write_frame(_MUX_CLOSE, 0, b'', wait=True, wait_timeout=0.5)
            except Exception:
                pass
        with self._lock:
            self._closed = True
            channels = list(self._channels.values())
            self._channels.clear()
            self._accept_queue[:] = []
            self._accept_cv.notify_all()
            self._state_cv.notify_all()
        self._fail_outgoing()
        for channel in channels:
            channel.force_local_eof()
        try:
            self._underlying.close()
        except Exception:
            pass
        writer = getattr(self, '_writer', None)
        if writer is not None and not on_writer:
            writer.join(timeout=1.0)
        reader = getattr(self, '_reader', None)
        if reader is not None and reader is not threading.current_thread():
            reader.join(timeout=1.0)

    def _discard_channel(self, channel):
        with self._lock:
            current = self._channels.get(channel.channel_id)
            if current is channel:
                del self._channels[channel.channel_id]
            if self._accept_queue:
                self._accept_queue[:] = [item for item in self._accept_queue if item is not channel]

    def _allocate_id(self):
        for channel_id in range(1, 65536):
            if channel_id not in self._channels:
                return channel_id
        raise ValueError('no channel ids available')

    @staticmethod
    def _validate_channel_id(channel_id):
        if isinstance(channel_id, bool) or not isinstance(channel_id, int):
            raise TypeError('channel id must be an integer')
        if channel_id < 1 or channel_id > 65535:
            raise ValueError('channel id must be in the range [1, 65535]')

    def _write_frame(self, ftype, channel_id, payload, wait=True, wait_timeout=None):
        if payload is None:
            payload = b''
        if not isinstance(payload, (bytes, bytearray)):
            payload = bytes(payload)
        else:
            payload = bytes(payload)
        if len(payload) > _MAX_FRAME:
            raise ValueError('mux frame exceeds maximum size')
        packet = _HEADER.pack(ftype, channel_id, len(payload)) + payload
        item = _Outbound(packet)
        with self._out_cv:
            if self._closed and ftype != _MUX_CLOSE:
                raise EOFError('multiplexer is closed')
            if ftype == _DATA:
                self._dataq.append(item)
            else:
                self._ctrlq.append(item)
            self._out_cv.notify()
        if not wait:
            return
        if not item.event.wait(wait_timeout):
            raise EOFError('multiplexer is closed')
        if not item.ok:
            raise EOFError('multiplexer is closed')

    def _fail_outgoing(self):
        with self._out_cv:
            pending = list(self._ctrlq)
            pending.extend(self._dataq)
            self._ctrlq.clear()
            self._dataq.clear()
            self._out_cv.notify_all()
        for item in pending:
            item.ok = False
            item.event.set()

    def _writer_loop(self):
        while True:
            with self._out_cv:
                while not self._ctrlq and not self._dataq and not self._closed:
                    self._out_cv.wait()
                if self._ctrlq:
                    item = self._ctrlq.popleft()
                elif self._dataq:
                    item = self._dataq.popleft()
                else:
                    return
            try:
                self._underlying.send(item.packet)
                item.ok = True
            except Exception:
                item.ok = False
                item.event.set()
                self._fail_outgoing()
                if not self._closing and not self._closed:
                    self.close()
                return
            item.event.set()

    def _read_exact(self, size):
        chunks = []
        remaining = size
        while remaining:
            part = self._read_some(remaining)
            chunks.append(part)
            remaining -= len(part)
        return b''.join(chunks)

    def _read_some(self, size):
        """Read at least one byte, noticing peer and local closure promptly.

        A blocking ``recv`` on Linux does not wake when another thread closes
        the socket, and that stuck call also holds the TCP state so the peer
        never observes FIN. Waiting in ``select`` lets both sides see the
        close as soon as it happens.
        """
        tube = self._underlying
        while not self._closed:
            if len(tube.buffer):
                try:
                    part = tube.recv(size, timeout=0)
                except EOFError:
                    raise EOFError('multiplexer is closed')
                if part:
                    return part
            fd = self._poll_fd()
            if fd is not None:
                try:
                    readable, _, _ = select.select([fd], [], [], 0.2)
                except (OSError, ValueError, select.error) as exc:
                    err = getattr(exc, 'errno', None)
                    if err == errno.EINTR:
                        continue
                    raise EOFError('multiplexer is closed')
                if not readable:
                    if self._underlying_is_dead():
                        raise EOFError('multiplexer is closed')
                    continue
            try:
                part = tube.recv(size, timeout=0.2)
            except EOFError:
                raise EOFError('multiplexer is closed')
            if part:
                return part
            if self._underlying_is_dead():
                raise EOFError('multiplexer is closed')
        raise EOFError('multiplexer is closed')

    def _poll_fd(self):
        try:
            return self._underlying.fileno()
        except Exception:
            return None

    def _underlying_is_dead(self):
        tube = self._underlying
        closed = getattr(tube, 'closed', None)
        if isinstance(closed, dict) and closed.get('recv'):
            return True
        attrs = getattr(tube, '__dict__', {})
        if 'sock' in attrs and attrs.get('sock') is None:
            return True
        proc = attrs.get('proc')
        if proc is not None:
            stdout = getattr(proc, 'stdout', None)
            if stdout is None or getattr(stdout, 'closed', False):
                return True
        return False

    def _reader_loop(self):
        try:
            while not self._closed:
                header = self._read_exact(_HEADER.size)
                ftype, channel_id, length = _HEADER.unpack(header)
                if length > _MAX_FRAME:
                    raise EOFError('mux frame exceeds maximum size')
                payload = self._read_exact(length) if length else b''
                if self._closed:
                    break
                self._dispatch(ftype, channel_id, payload)
        except EOFError:
            pass
        except Exception as exc:
            self._reader_error = exc
            if not self._closed and not self._closing:
                log.warning('tube multiplexer reader failed: %s', exc)
        finally:
            self.close()

    def _dispatch(self, ftype, channel_id, payload):
        if ftype == _MUX_CLOSE:
            raise EOFError('multiplexer is closed')
        if ftype == _OPEN:
            self._handle_open(channel_id)
            return
        if ftype == _OPEN_ACK:
            self._handle_open_ack(channel_id)
            return
        if ftype == _OPEN_NAK:
            self._handle_open_nak(channel_id, payload)
            return

        with self._lock:
            channel = self._channels.get(channel_id)
        if channel is None or getattr(channel, '_cancelled', False):
            if ftype == _CLOSE:
                try:
                    self._write_frame(_CLOSE_ACK, channel_id, b'', wait=False)
                except EOFError:
                    pass
            return
        if ftype == _DATA:
            channel._deliver(payload, final=True)
        elif ftype == _DATA_ACK:
            channel._on_data_ack()
        elif ftype == _PAUSE:
            channel._on_pause()
        elif ftype == _RESUME:
            channel._on_resume()
        elif ftype == _EOF:
            channel._on_remote_eof()
        elif ftype == _EOF_ACK:
            channel._on_eof_ack()
        elif ftype == _CLOSE:
            channel._on_remote_close()
        elif ftype == _CLOSE_ACK:
            channel._on_close_ack()

    def _handle_open(self, channel_id):
        nak = None
        channel = None
        with self._lock:
            if self._closed or self._closing:
                return
            if not isinstance(channel_id, int) or isinstance(channel_id, bool) \
                    or channel_id < 1 or channel_id > 65535:
                nak = _NAK_INVALID
            elif channel_id in self._channels:
                nak = _NAK_DUPLICATE
            elif len(self._channels) >= self._max_channels:
                nak = _NAK_CAPACITY
            else:
                channel = MuxChannel(self, channel_id)
                self._channels[channel_id] = channel
                self._accept_queue.append(channel)
                self._accept_cv.notify_all()
        if nak is not None:
            try:
                self._write_frame(_OPEN_NAK, channel_id & 0xffff, bytes([nak]), wait=False)
            except EOFError:
                pass
            return
        try:
            self._write_frame(_OPEN_ACK, channel_id, b'', wait=False)
        except EOFError:
            if channel is not None:
                channel.force_local_eof()
                self._discard_channel(channel)

    def _handle_open_ack(self, channel_id):
        with self._state_cv:
            channel = self._channels.get(channel_id)
            if channel is None or channel._cancelled:
                return
            channel._acked = True
            self._state_cv.notify_all()

    def _handle_open_nak(self, channel_id, payload):
        reason = payload[0] if payload else _NAK_INVALID
        with self._state_cv:
            channel = self._channels.get(channel_id)
            if channel is None or channel._cancelled:
                return
            channel._nak = reason
            self._state_cv.notify_all()

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, tb):
        self.close()

    def __repr__(self):
        state = 'closed' if self._closed else 'open'
        return '<TubeMultiplexer %s channels=%d>' % (state, len(self._channels))
