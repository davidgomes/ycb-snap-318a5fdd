"""
Carry multiple independent, bidirectional channels over a single tube.

Both ends of the underlying tube must be wrapped in a
:class:`TubeMultiplexer`.  Either end may open channels with
:meth:`TubeMultiplexer.open_channel`, and the other end receives them via
:meth:`TubeMultiplexer.accept_channel`.  Every channel is a regular
:class:`pwnlib.tubes.tube.tube`, so all the usual ``recv*``/``send*``
helpers work on it.

Each channel has its own receive buffer and its own flow control: once the
remote sender of a channel has sent as much data as the high water mark
without it being consumed, it is paused until the receive buffer has drained
to the low water mark.  Other channels are unaffected.

Example:

    >>> l = listen()
    >>> r = remote('localhost', l.lport)
    >>> a = r.mux()
    >>> b = l.wait_for_connection().mux()
    >>> ch1 = a.open_channel()
    >>> ch2 = a.open_channel()
    >>> peer1 = b.accept_channel()
    >>> peer2 = b.accept_channel()
    >>> ch2.sendline(b'second')
    >>> ch1.sendline(b'first')
    >>> peer1.recvline()
    b'first\\n'
    >>> peer2.recvline()
    b'second\\n'
    >>> peer1.send(b'reply')
    >>> ch1.recvn(5)
    b'reply'
    >>> ch1.close()
    >>> peer1.recv()
    Traceback (most recent call last):
    ...
    EOFError
    >>> a.close()
    >>> b.accept_channel()
    Traceback (most recent call last):
    ...
    EOFError: multiplexer is closed

Wire format:

    Every frame starts with a 7-byte big-endian header ``type:u8``,
    ``channel_id:u16``, ``length:u32``, followed by ``length`` bytes of
    payload.  Channel ``0`` is reserved for frames addressing the
    multiplexer as a whole.

    ``OPEN`` and ``OPEN_ACK`` carry the high water mark of their sender as
    ``u32``.  The other end may send up to that many bytes on the channel
    before it has to wait for a ``CREDIT`` frame (``u64``) that reports how
    many bytes were consumed.
"""
import collections
import random
import socket
import struct
import threading
import time

from pwnlib.context import context
from pwnlib.log import getLogger
from pwnlib.timeout import Timeout
from pwnlib.tubes.buffer import Buffer
from pwnlib.tubes.tube import tube

log = getLogger(__name__)

__all__ = ['TubeMultiplexer', 'MuxChannel']

MIN_CHANNEL_ID = 1
MAX_CHANNEL_ID = 0xffff
MAX_PAYLOAD = 0xffffffff

_HEADER = struct.Struct('>BHI')
_WINDOW = struct.Struct('>I')
_CREDIT_AMOUNT = struct.Struct('>Q')

_OPEN     = 1
_OPEN_ACK = 2
_REJECT   = 3
_DATA     = 4
_FIN      = 5   # sender will not send any more data on the channel
_CLOSE    = 6   # channel is closed; answered with a CLOSE of its own
_CREDIT   = 7   # receiver consumed data, the sender may send more
_RDSHUT   = 8   # sender will not read any more data on the channel
_GOAWAY   = 9   # multiplexer is shutting down

_POLL_INTERVAL = 0.05

_REJECT_IN_USE  = 1
_REJECT_FULL    = 2
_REJECT_CLOSING = 3

# Reserved channel ids that are waiting for the CLOSE handshake to finish.
_AWAIT_ACK   = 'await-ack'    # we sent CLOSE, waiting for the peer's CLOSE
_SENDING_ACK = 'sending-ack'  # peer sent CLOSE, our reply is not written yet

_PENDING, _ACKED, _REJECTED, _FAILED = range(4)


def _deadline(timeout):
    if timeout is None or timeout >= Timeout.maximum:
        return None
    return time.time() + timeout


def _remaining(deadline):
    if deadline is None:
        return None
    return max(deadline - time.time(), 0)


class MuxChannel(tube):
    """A logical channel of a :class:`TubeMultiplexer`.

    Instances are created by :meth:`TubeMultiplexer.open_channel` and
    :meth:`TubeMultiplexer.accept_channel`, and should not be instantiated
    directly.
    """

    def __init__(self, mux, channel_id, *a, **kw):
        super(MuxChannel, self).__init__(*a, **kw)
        self._mux = mux
        self._channel_id = channel_id
        self._cond = mux._cond

        # Data delivered by the reader thread, not yet handed to the tube.
        self._rx = Buffer()
        self._rx.set_watermarks(mux.high_water_mark, mux.low_water_mark)

        self._stats = {
            'bytes_sent': 0,
            'bytes_received': 0,
            'frames_sent': 0,
            'frames_received': 0,
        }

        self._open_state = _PENDING
        self._reject_reason = None

        self._closed = False
        self._send_closed = False   # local shutdown('send') or close()
        self._recv_closed = False   # local shutdown('recv') or close()
        self._remote_eof = False    # peer will not send any more data
        self._peer_rdshut = False   # peer will not read any more data
        self._close_sent = False
        self._dead = False          # multiplexer is gone

        # Flow control.  _unacked counts bytes sent that the peer has not
        # reported as consumed yet; sending pauses once it reaches the peer's
        # high water mark.  _unreported counts bytes consumed locally that
        # have not been reported to the peer yet.
        self._peer_window = None
        self._unacked = 0
        self._unreported = 0

    def __repr__(self):
        return '<%s channel_id=%d>' % (type(self).__name__, self._channel_id)

    @property
    def channel_id(self):
        """Identifier of this channel, shared by both ends."""
        return self._channel_id

    @property
    def stats(self):
        """Dictionary with the keys ``bytes_sent``, ``bytes_received``,
        ``frames_sent`` and ``frames_received``."""
        with self._cond:
            return dict(self._stats)

    def _send_blocked(self):
        return self._send_closed or self._peer_rdshut or self._dead

    def _flow_blocked(self):
        return self._peer_window is not None and 0 < self._unacked and self._peer_window <= self._unacked

    def _maybe_credit(self):
        """Returns consumed bytes to the peer once the receive buffer has
        drained to the low water mark.  The caller must hold ``_cond``.

        The peer can only be paused if everything it sent but was not
        credited yet (``_unreported`` plus the buffer) reached the high water
        mark, so smaller amounts are not worth a frame.
        """
        rx = self._rx
        if self._unreported and rx.under_low_water and self._unreported + rx.size >= rx.high_water:
            self._mux._queue_control(_CREDIT, self._channel_id, _CREDIT_AMOUNT.pack(self._unreported))
            self._unreported = 0

    def recv_raw(self, numb):
        deadline = _deadline(self.timeout)
        with self._cond:
            while True:
                if self._recv_closed:
                    raise EOFError
                if self._rx:
                    data = self._rx.get(numb)
                    break
                if self._remote_eof or self._dead:
                    raise EOFError
                remaining = _remaining(deadline)
                if remaining == 0:
                    return None
                self._cond.wait(remaining)

            self._unreported += len(data)
            self._maybe_credit()
        return data

    def send_raw(self, data):
        if len(data) > MAX_PAYLOAD:
            raise ValueError('cannot send more than %#x bytes at once' % MAX_PAYLOAD)

        mux = self._mux
        deadline = _deadline(self.timeout)
        with self._cond:
            while True:
                if self._send_blocked():
                    raise EOFError
                if not self._flow_blocked():
                    break
                remaining = _remaining(deadline)
                if remaining == 0:
                    raise TimeoutError('channel %d: send blocked by flow control' % self._channel_id)
                self._cond.wait(remaining)

        with mux._send_lock:
            with self._cond:
                if self._send_blocked():
                    raise EOFError
                # Account before writing, the peer may credit the data
                # before we get to run again.
                self._unacked += len(data)
                self._stats['bytes_sent'] += len(data)
                self._stats['frames_sent'] += 1
            mux._write_frame(_DATA, self._channel_id, data)

    def settimeout_raw(self, timeout):
        pass

    def can_recv_raw(self, timeout):
        deadline = _deadline(timeout)
        with self._cond:
            while True:
                if self._rx:
                    return True
                if self._recv_closed or self._remote_eof or self._dead:
                    return False
                remaining = _remaining(deadline)
                if remaining == 0:
                    return False
                self._cond.wait(remaining)

    def connected_raw(self, direction):
        with self._cond:
            send = not self._send_blocked()
            recv = not (self._recv_closed or self._remote_eof or self._dead)
        if direction == 'send':
            return send
        if direction == 'recv':
            return recv
        return send or recv

    def shutdown_raw(self, direction):
        with self._cond:
            if self._closed:
                return
            if direction == 'send':
                if self._send_closed:
                    return
                self._send_closed = True
                frame = _FIN
            else:
                if self._recv_closed:
                    return
                self._recv_closed = True
                self._rx.get()
                frame = _RDSHUT
            finished = self._send_closed and self._recv_closed
            notify = not (finished or self._dead or self._close_sent)
            self._cond.notify_all()

        if finished:
            self.close()
        elif notify:
            self._mux._send_frame_quietly(frame, self._channel_id)

    def close(self):
        """close()

        Closes the channel in both directions.  The remote end of the channel
        receives EOF for both sending and receiving.
        """
        mux = getattr(self, '_mux', None)
        if mux is None:
            return
        with self._cond:
            if self._closed:
                return
            self._closed = True
            self._send_closed = True
            self._recv_closed = True
            self._rx.get()
            send_close = not (self._close_sent or self._dead) and \
                mux._channels.get(self._channel_id) is self
            if send_close:
                self._close_sent = True
                del mux._channels[self._channel_id]
                mux._closing[self._channel_id] = _AWAIT_ACK
            self._cond.notify_all()

        if send_close:
            mux._send_frame_quietly(_CLOSE, self._channel_id)


class TubeMultiplexer(object):
    """TubeMultiplexer(underlying, max_channels=256, high_water_mark=1048576, low_water_mark=262144)

    Multiplexes independent :class:`MuxChannel` tubes over ``underlying``.
    The remote end of ``underlying`` must also be wrapped in a
    :class:`TubeMultiplexer`.

    Once wrapped, ``underlying`` must not be used directly anymore.

    Arguments:
        underlying(tube): Tube to carry the channels over.
        max_channels(int): Maximum number of simultaneously open channels,
            between 1 and 65535.
        high_water_mark(int): Receive buffer size of a channel at which the
            remote sender of that channel is paused.
        low_water_mark(int): Receive buffer size of a channel at which the
            remote sender of that channel is resumed.

    Raises:
        TypeError: ``underlying`` is not a tube.
        ValueError: ``max_channels`` is out of range, or ``low_water_mark``
            exceeds ``high_water_mark``.

    Example:

        A sender that is paused by flow control times out according to the
        timeout of its channel, and resumes once the receiver drained its
        buffer:

        >>> l = listen()
        >>> a = remote('localhost', l.lport).mux(high_water_mark=4, low_water_mark=0)
        >>> b = l.wait_for_connection().mux(high_water_mark=4, low_water_mark=0)
        >>> ch, other = a.open_channel(), a.open_channel()
        >>> peer, other_peer = b.accept_channel(), b.accept_channel()
        >>> ch.settimeout(0.5)
        >>> ch.send(b'full')
        >>> ch.send(b'more')
        Traceback (most recent call last):
        ...
        TimeoutError: channel 1: send blocked by flow control
        >>> other.send(b'fine')
        >>> other_peer.recv()
        b'fine'
        >>> peer.recv()
        b'full'
        >>> ch.send(b'more')
        >>> peer.recv()
        b'more'
        >>> a.close(); b.close()
    """

    def __init__(self, underlying, max_channels=256, high_water_mark=1048576, low_water_mark=262144):
        if not isinstance(underlying, tube):
            raise TypeError('underlying must be a tube, not %r' % type(underlying).__name__)
        for name, value in (('max_channels', max_channels),
                            ('high_water_mark', high_water_mark),
                            ('low_water_mark', low_water_mark)):
            if isinstance(value, bool) or not isinstance(value, int):
                raise TypeError('%s must be an integer' % name)
        if not MIN_CHANNEL_ID <= max_channels <= MAX_CHANNEL_ID:
            raise ValueError('max_channels must be in [%d, %d]' % (MIN_CHANNEL_ID, MAX_CHANNEL_ID))
        if high_water_mark < 0 or low_water_mark < 0:
            raise ValueError('water marks must not be negative')
        if low_water_mark > high_water_mark:
            raise ValueError('low_water_mark must not exceed high_water_mark')

        self._underlying = underlying
        self._max_channels = max_channels
        self._high_water_mark = high_water_mark
        self._low_water_mark = low_water_mark

        # _cond guards all channel and multiplexer state.  _send_lock
        # serializes frames on the underlying tube.  When both are needed,
        # _send_lock is taken first.  The reader thread never takes
        # _send_lock, so it keeps draining the underlying tube no matter
        # how congested the sending side is.
        self._cond = threading.Condition(threading.RLock())
        self._send_lock = threading.Lock()

        self._channels = {}     # id -> MuxChannel, open channels
        self._pending = {}      # id -> MuxChannel, local opens awaiting ack
        self._closing = {}      # id -> _AWAIT_ACK / _SENDING_ACK
        self._abandoned = set() # ids of local opens that timed out
        self._accept_queue = collections.deque()
        self._control = collections.deque()
        self._next_id = MIN_CHANNEL_ID

        self._closed = False
        self._closed_locally = False

        # Credit frames are tiny and a paused sender waits for them, so do
        # not let Nagle's algorithm hold them back.
        sock = getattr(underlying, 'sock', None)
        if isinstance(sock, socket.socket) and sock.type == socket.SOCK_STREAM \
                and sock.family in (socket.AF_INET, socket.AF_INET6):
            try:
                sock.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
            except OSError:
                pass

        self._reader = context.Thread(target=self._reader_loop, name='mux-reader')
        self._reader.daemon = True
        self._writer = context.Thread(target=self._control_loop, name='mux-control')
        self._writer.daemon = True
        self._reader.start()
        self._writer.start()

    def __repr__(self):
        return '<%s underlying=%r channels=%d%s>' % (
            type(self).__name__, self._underlying, len(self._channels),
            ' closed' if self._closed else '')

    def __enter__(self):
        return self

    def __exit__(self, *a):
        self.close()

    @property
    def channels(self):
        """Dictionary mapping channel ids to open :class:`MuxChannel` objects."""
        with self._cond:
            return dict(self._channels)

    @property
    def high_water_mark(self):
        """Per-channel receive buffer size at which the remote sender is paused."""
        return self._high_water_mark

    @property
    def low_water_mark(self):
        """Per-channel receive buffer size at which the remote sender is resumed."""
        return self._low_water_mark

    @property
    def closed(self):
        """True once the multiplexer is closed, locally or by the remote end."""
        return self._closed

    def open_channel(self, channel_id=None, timeout=None):
        """open_channel(channel_id=None, timeout=None) -> MuxChannel

        Opens a channel and waits until the remote end acknowledges it.

        Arguments:
            channel_id(int): Identifier in ``[1, 65535]``, or :const:`None` to
                pick an unused one.
            timeout(float): Seconds to wait for the acknowledgement, or
                :const:`None` to wait forever.

        Raises:
            TypeError: ``channel_id`` is not an integer.
            ValueError: ``channel_id`` is out of range or already in use, the
                maximum number of channels is reached, or the remote end
                refused the channel.
            TimeoutError: The remote end did not acknowledge in time.
            EOFError: The multiplexer is closed.
        """
        if channel_id is not None:
            if isinstance(channel_id, bool) or not isinstance(channel_id, int):
                raise TypeError('channel_id must be an integer, not %r' % type(channel_id).__name__)
            if not MIN_CHANNEL_ID <= channel_id <= MAX_CHANNEL_ID:
                raise ValueError('channel_id must be in [%d, %d]' % (MIN_CHANNEL_ID, MAX_CHANNEL_ID))

        deadline = _deadline(timeout)
        randomize = False

        while True:
            with self._cond:
                if self._closed:
                    raise EOFError('multiplexer is closed')
                self._check_capacity()
                if channel_id is None:
                    cid = self._allocate_id(randomize)
                else:
                    cid = channel_id
                    if cid in self._channels or cid in self._pending or cid in self._abandoned:
                        raise ValueError('channel %d is already in use' % cid)
                    while cid in self._closing:
                        remaining = _remaining(deadline)
                        if remaining == 0:
                            raise TimeoutError('channel %d is still being closed' % cid)
                        self._cond.wait(remaining)
                        if self._closed:
                            raise EOFError('multiplexer is closed')
                    if cid in self._channels or cid in self._pending or cid in self._abandoned:
                        raise ValueError('channel %d is already in use' % cid)
                    self._check_capacity()
                ch = MuxChannel(self, cid)
                self._pending[cid] = ch

            try:
                self._send_frame(_OPEN, cid, self._window_payload())
            except EOFError:
                raise EOFError('multiplexer is closed')

            with self._cond:
                while ch._open_state == _PENDING:
                    remaining = _remaining(deadline)
                    if remaining == 0:
                        del self._pending[cid]
                        self._abandoned.add(cid)
                        ch._open_state = _FAILED
                        ch._dead = True
                        raise TimeoutError('remote did not acknowledge channel %d' % cid)
                    self._cond.wait(remaining)

                if ch._open_state == _ACKED:
                    return ch
                if ch._open_state == _FAILED:
                    raise EOFError('multiplexer is closed')

                reason = ch._reject_reason
                if channel_id is None and reason == _REJECT_IN_USE:
                    # Both ends picked the same id at the same time.
                    randomize = True
                    continue
                if reason == _REJECT_FULL:
                    raise ValueError('remote refused channel %d: too many channels' % cid)
                if reason == _REJECT_CLOSING:
                    raise EOFError('multiplexer is closed')
                raise ValueError('remote refused channel %d: already in use' % cid)

    def accept_channel(self, timeout=None):
        """accept_channel(timeout=None) -> MuxChannel

        Waits for the remote end to open a channel.

        Arguments:
            timeout(float): Seconds to wait, or :const:`None` to wait forever.

        Returns:
            The new :class:`MuxChannel`, or :const:`None` on timeout.

        Raises:
            EOFError: The multiplexer is closed.
        """
        deadline = _deadline(timeout)
        with self._cond:
            while True:
                if self._closed_locally:
                    raise EOFError('multiplexer is closed')
                if self._accept_queue:
                    return self._accept_queue.popleft()
                if self._closed:
                    raise EOFError('multiplexer is closed')
                remaining = _remaining(deadline)
                if remaining == 0:
                    return None
                self._cond.wait(remaining)

    def close(self):
        """close()

        Signals EOF to all channels, notifies the remote end and closes the
        underlying tube.  Calling it more than once has no effect.
        """
        with self._cond:
            self._closed_locally = True
        self._teardown(notify_remote=True)

    # Internals

    def _window_payload(self):
        return _WINDOW.pack(min(self._high_water_mark, 0xffffffff))

    @staticmethod
    def _parse_window(payload):
        if len(payload) < _WINDOW.size:
            return None
        return _WINDOW.unpack_from(payload)[0]

    def _check_capacity(self):
        if len(self._channels) + len(self._pending) >= self._max_channels:
            raise ValueError('maximum number of channels (%d) reached' % self._max_channels)

    def _id_in_use(self, cid):
        return cid in self._channels or cid in self._pending \
            or cid in self._closing or cid in self._abandoned

    def _allocate_id(self, randomize):
        if randomize:
            free = [cid for cid in range(MIN_CHANNEL_ID, MAX_CHANNEL_ID + 1) if not self._id_in_use(cid)]
            if free:
                return random.choice(free)
        else:
            cid = self._next_id
            for _ in range(MAX_CHANNEL_ID):
                if not self._id_in_use(cid):
                    self._next_id = cid % MAX_CHANNEL_ID + 1
                    return cid
                cid = cid % MAX_CHANNEL_ID + 1
        raise ValueError('no free channel id available')

    def _write_frame(self, ftype, cid, payload=b''):
        """Writes a frame.  The caller must hold ``_send_lock``."""
        if self._closed and ftype != _GOAWAY:
            raise EOFError
        try:
            self._underlying.send(_HEADER.pack(ftype, cid, len(payload)) + payload)
        except Exception:
            self._teardown(notify_remote=False)
            raise EOFError

    def _send_frame(self, ftype, cid, payload=b''):
        with self._send_lock:
            self._write_frame(ftype, cid, payload)

    def _send_frame_quietly(self, ftype, cid):
        try:
            self._send_frame(ftype, cid)
        except EOFError:
            pass

    def _queue_control(self, ftype, cid, payload=b'', done=None):
        """Queues a frame for the control thread.  The caller must hold ``_cond``."""
        self._control.append((ftype, cid, payload, done))
        self._cond.notify_all()

    def _control_loop(self):
        while True:
            with self._cond:
                while not self._control and not self._closed:
                    self._cond.wait()
                if self._closed:
                    return
                ftype, cid, payload, done = self._control.popleft()
            try:
                with self._send_lock:
                    self._write_frame(ftype, cid, payload)
                    if done:
                        with self._cond:
                            done()
                            self._cond.notify_all()
            except EOFError:
                return

    def _read_underlying(self, poll):
        """Returns data from the underlying tube, or ``b''`` if there is none
        yet.  Raises :exc:`EOFError` once the underlying tube is gone.

        Polling instead of blocking in ``recv`` lets us notice when the
        underlying tube is closed behind our back: closing a socket does not
        wake up a thread blocked reading from it.  ``can_recv`` does not touch
        the timeout of the underlying tube, which would affect concurrent
        senders.
        """
        underlying = self._underlying
        if poll:
            if not underlying.can_recv(timeout=_POLL_INTERVAL):
                try:
                    alive = underlying.connected('recv')
                except NotImplementedError:
                    alive = True
                if not alive:
                    raise EOFError
                return b''
        return underlying.recv(65536)

    def _reader_loop(self):
        buf = bytearray()
        poll = True
        try:
            while not self._closed:
                try:
                    data = self._read_underlying(poll)
                except NotImplementedError:
                    poll = False
                    continue
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
                    if not self._handle_frame(ftype, cid, payload):
                        return
        except Exception:
            pass
        finally:
            self._teardown(notify_remote=False)

    def _handle_frame(self, ftype, cid, payload):
        if ftype == _GOAWAY:
            return False
        if ftype not in (_OPEN, _OPEN_ACK, _REJECT, _DATA, _FIN, _CLOSE, _CREDIT, _RDSHUT) \
                or not MIN_CHANNEL_ID <= cid <= MAX_CHANNEL_ID:
            log.debug('mux: protocol error, frame type %d on channel %d', ftype, cid)
            return False

        with self._cond:
            if self._closed:
                return False

            if ftype == _OPEN:
                self._handle_open(cid, self._parse_window(payload))
            elif ftype == _OPEN_ACK:
                self._handle_open_ack(cid, self._parse_window(payload))
            elif ftype == _REJECT:
                ch = self._pending.pop(cid, None)
                if ch is not None:
                    ch._open_state = _REJECTED
                    ch._reject_reason = payload[0] if payload else None
                    ch._dead = True
                self._abandoned.discard(cid)
            elif ftype == _CLOSE:
                self._handle_close(cid)
            else:
                ch = self._channels.get(cid)
                if ch is None:
                    return True
                if ftype == _DATA:
                    self._deliver(ch, payload)
                elif ftype == _FIN:
                    ch._remote_eof = True
                elif ftype == _RDSHUT:
                    ch._peer_rdshut = True
                elif ftype == _CREDIT and len(payload) >= _CREDIT_AMOUNT.size:
                    ch._unacked -= _CREDIT_AMOUNT.unpack_from(payload)[0]
            self._cond.notify_all()
        return True

    def _handle_open(self, cid, window):
        if self._id_in_use(cid):
            self._queue_control(_REJECT, cid, bytes([_REJECT_IN_USE]))
            return
        if len(self._channels) + len(self._pending) >= self._max_channels:
            self._queue_control(_REJECT, cid, bytes([_REJECT_FULL]))
            return

        ch = MuxChannel(self, cid)
        ch._open_state = _ACKED
        ch._peer_window = window
        self._channels[cid] = ch

        # Only hand the channel out once the ack is on the wire, so that
        # nothing sent on it can overtake the ack.
        self._queue_control(_OPEN_ACK, cid, self._window_payload(),
                            done=lambda: self._accept_queue.append(ch))

    def _handle_open_ack(self, cid, window):
        ch = self._pending.pop(cid, None)
        if ch is not None:
            ch._open_state = _ACKED
            ch._peer_window = window
            self._channels[cid] = ch
        elif cid in self._abandoned:
            self._abandoned.discard(cid)
            self._closing[cid] = _AWAIT_ACK
            self._queue_control(_CLOSE, cid)

    def _handle_close(self, cid):
        if self._closing.get(cid) == _AWAIT_ACK:
            del self._closing[cid]
            return
        ch = self._channels.pop(cid, None)
        if ch is None:
            return
        ch._remote_eof = True
        ch._peer_rdshut = True
        ch._close_sent = True
        self._closing[cid] = _SENDING_ACK

        def done():
            if self._closing.get(cid) == _SENDING_ACK:
                del self._closing[cid]
        self._queue_control(_CLOSE, cid, done=done)

    def _deliver(self, ch, payload):
        if ch._recv_closed:
            return
        ch._rx.add(payload)
        ch._stats['bytes_received'] += len(payload)
        ch._stats['frames_received'] += 1
        ch._maybe_credit()

    def _teardown(self, notify_remote):
        with self._cond:
            if self._closed:
                return
            self._closed = True
            for ch in list(self._channels.values()) + list(self._pending.values()) + list(self._accept_queue):
                ch._dead = True
                if ch._open_state == _PENDING:
                    ch._open_state = _FAILED
            self._channels.clear()
            self._pending.clear()
            self._control.clear()
            self._cond.notify_all()

        underlying = self._underlying
        if notify_remote:
            # Tell the remote end right away, even if it is idle.
            if self._send_lock.acquire(timeout=1):
                try:
                    underlying.send(_HEADER.pack(_GOAWAY, 0, 0))
                except Exception:
                    pass
                finally:
                    self._send_lock.release()

        # Shutting down the receive direction wakes up the reader thread if
        # it is blocked on the underlying tube.
        for action in (lambda: underlying.shutdown('recv'), underlying.close):
            try:
                action()
            except Exception:
                pass
