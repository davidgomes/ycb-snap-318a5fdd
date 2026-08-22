import struct
import threading
import time

from pwnlib.tubes.buffer import Buffer
from pwnlib.tubes.tube import tube


class MuxChannel(tube):
    def __init__(self, mux, channel_id):
        tube.__init__(self)
        self._mux = mux
        self._channel_id = channel_id
        self._rx = Buffer()
        self._rx.set_watermarks(mux.high_water_mark, mux.low_water_mark)
        self._condition = threading.Condition()
        self._send_open = True
        self._recv_open = True
        self._stats = dict(bytes_sent=0, bytes_received=0,
                           frames_sent=0, frames_received=0)

    @property
    def channel_id(self):
        return self._channel_id

    @property
    def stats(self):
        with self._condition:
            return dict(self._stats)

    def _deliver(self, data):
        with self._condition:
            if self._recv_open:
                self._rx.add(data)
                self._stats['bytes_received'] += len(data)
                self._stats['frames_received'] += 1
                self._condition.notify_all()

    def _eof(self, receive=True):
        with self._condition:
            if receive:
                self._recv_open = False
            self._send_open = False
            self._condition.notify_all()

    def recv_raw(self, numb):
        with self._condition:
            while not self._rx and self._recv_open:
                if not self._condition.wait(self.timeout):
                    return None
            if self._rx:
                data = self._rx.get(numb)
                self._mux._notify_flow(self)
                return data
            raise EOFError

    def send_raw(self, data):
        with self._condition:
            if not self._send_open:
                raise EOFError
        self._mux._send_data(self, data)
        with self._condition:
            self._stats['bytes_sent'] += len(data)
            self._stats['frames_sent'] += 1

    def connected_raw(self, direction):
        with self._condition:
            return (self._send_open if direction == 'send' else
                    self._recv_open if direction == 'recv' else
                    self._send_open or self._recv_open)

    def shutdown(self, direction='send'):
        direction = self.shutdown_directions[direction]
        with self._condition:
            if direction == 'send' and not self._send_open:
                return
            if direction == 'recv' and not self._recv_open:
                return
            if direction == 'send':
                self._send_open = False
            else:
                self._recv_open = False
            self._condition.notify_all()
        self._mux._send_control(self, b'C' if direction == 'send' else b'R')

    def close(self):
        self.shutdown('send')
        self.shutdown('recv')


class TubeMultiplexer(tube):
    _HEADER = struct.Struct('!BBHI')

    def __init__(self, underlying, max_channels=256,
                 high_water_mark=1048576, low_water_mark=262144):
        if not isinstance(underlying, tube):
            raise TypeError('underlying must be a tube')
        if not isinstance(max_channels, int) or not 1 <= max_channels <= 65535:
            raise ValueError('max_channels must be between 1 and 65535')
        if low_water_mark > high_water_mark:
            raise ValueError('low watermark cannot exceed high watermark')
        tube.__init__(self)
        self.underlying = underlying
        self._max_channels = max_channels
        self._high_water_mark = high_water_mark
        self._low_water_mark = low_water_mark
        self.channels = {}
        self._pending = {}
        self._accepted = []
        self._closed = False
        self._lock = threading.RLock()
        self._condition = threading.Condition(self._lock)
        self._writer = threading.Lock()
        self._reader = threading.Thread(target=self._reader_loop)
        self._reader.daemon = True
        self._reader.start()

    @property
    def high_water_mark(self):
        return self._high_water_mark

    @property
    def low_water_mark(self):
        return self._low_water_mark

    def _frame(self, kind, channel, payload=b''):
        return self._HEADER.pack(1, kind, channel, len(payload)) + payload

    def _write(self, data):
        with self._writer:
            self.underlying.send(data)

    def _send_control(self, channel, kind):
        self._write(self._frame(4 if kind == b'C' else 5,
                                channel.channel_id))

    def _send_data(self, channel, data):
        with self._condition:
            end = None if channel.timeout is None else time.time() + channel.timeout
            while channel._rx.over_high_water and not self._closed:
                remaining = None if end is None else end - time.time()
                if remaining is not None and remaining <= 0:
                    raise TimeoutError
                if not self._condition.wait(remaining):
                    raise TimeoutError
            if self._closed:
                raise EOFError
        self._write(self._frame(2, channel.channel_id, data))

    def _notify_flow(self, channel):
        with self._condition:
            self._condition.notify_all()

    def _reader_loop(self):
        try:
            while True:
                header = self.underlying.recvn(self._HEADER.size)
                version, kind, channel_id, length = self._HEADER.unpack(header)
                payload = self.underlying.recvn(length) if length else b''
                with self._condition:
                    channel = self.channels.get(channel_id)
                    if kind == 1:
                        if channel is None and len(self.channels) < self._max_channels:
                            channel = MuxChannel(self, channel_id)
                            self.channels[channel_id] = channel
                            self._accepted.append(channel)
                            self._write(self._frame(3, channel_id))
                            self._condition.notify_all()
                    elif kind == 3:
                        event = self._pending.pop(channel_id, None)
                        if event:
                            event.set()
                    elif kind == 2 and channel:
                        channel._deliver(payload)
                    elif kind in (4, 5) and channel:
                        channel._eof(receive=(kind == 5))
        except (EOFError, OSError):
            self._mark_closed()

    def _mark_closed(self):
        with self._condition:
            self._closed = True
            for channel in self.channels.values():
                channel._eof()
            self._condition.notify_all()

    def open_channel(self, channel_id=None, timeout=None):
        with self._condition:
            if self._closed:
                raise EOFError
            if channel_id is None:
                for candidate in range(1, 65536):
                    if candidate not in self.channels and candidate not in self._pending:
                        channel_id = candidate
                        break
            elif not isinstance(channel_id, int):
                raise TypeError('channel_id must be an integer')
            if channel_id is None or not 1 <= channel_id <= 65535:
                raise ValueError('channel_id must be between 1 and 65535')
            if channel_id in self.channels or channel_id in self._pending:
                raise ValueError('channel already exists')
            if len(self.channels) + len(self._pending) >= self._max_channels:
                raise ValueError('channel capacity exceeded')
            channel = MuxChannel(self, channel_id)
            event = threading.Event()
            self._pending[channel_id] = event
            self._write(self._frame(1, channel_id))
        if not event.wait(timeout):
            with self._condition:
                self._pending.pop(channel_id, None)
            raise TimeoutError
        with self._condition:
            if self._closed:
                raise EOFError
            self.channels[channel_id] = channel
            return channel

    def accept_channel(self, timeout=None):
        with self._condition:
            if self._closed:
                raise EOFError
            if not self._accepted and not self._condition.wait(timeout):
                return None
            if self._closed:
                raise EOFError
            return self._accepted.pop(0)

    def close(self):
        with self._condition:
            if self._closed:
                return
            self._closed = True
            for channel in self.channels.values():
                channel._eof()
            self._condition.notify_all()
        try:
            self.underlying.close()
        finally:
            self._reader.join(0.2)
