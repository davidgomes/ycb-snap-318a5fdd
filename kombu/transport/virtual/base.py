"""Virtual transport implementation.

Emulates the AMQ API for non-AMQ transports.
"""

from __future__ import annotations

import base64
import socket
import sys
import warnings
from array import array
from collections import OrderedDict, defaultdict, namedtuple
from itertools import count
from multiprocessing.util import Finalize
from queue import Empty
from time import monotonic, sleep, time
from typing import TYPE_CHECKING

from amqp.protocol import queue_declare_ok_t

from kombu.exceptions import ChannelError, ResourceError
from kombu.log import get_logger
from kombu.transport import base
from kombu.utils.div import emergency_dump_state
from kombu.utils.encoding import bytes_to_str, str_to_bytes
from kombu.utils.scheduling import FairCycle
from kombu.utils.uuid import uuid

from .exchange import STANDARD_EXCHANGE_TYPES

if TYPE_CHECKING:
    from types import TracebackType

ARRAY_TYPE_H = 'H'

UNDELIVERABLE_FMT = """\
Message could not be delivered: No queues bound to exchange {exchange!r} \
using binding key {routing_key!r}.
"""

NOT_EQUIVALENT_FMT = """\
Cannot redeclare exchange {0!r} in vhost {1!r} with \
different type, durable, autodelete or arguments value.\
"""

W_NO_CONSUMERS = """\
Requeuing undeliverable message for queue %r: No consumers.\
"""

RESTORING_FMT = 'Restoring {0!r} unacknowledged message(s)'
RESTORE_PANIC_FMT = 'UNABLE TO RESTORE {0} MESSAGES: {1}'

logger = get_logger(__name__)

_SAC_ARGUMENT = 'x-single-active-consumer'
_PRIORITY_ARGUMENT = 'x-priority'
_SAC_TRUE_STRINGS = frozenset({'1', 'true', 'yes', 'on'})


def _argument(arguments, key, default=None):
    """Read one queue or consumer argument without assuming a dict."""
    if not arguments:
        return default
    getter = getattr(arguments, 'get', None)
    if getter is None:
        return default
    return getter(key, default)


def _sac_enabled(value):
    """Return true when a queue argument turns single-active-consumer on."""
    if isinstance(value, str):
        return value.strip().lower() in _SAC_TRUE_STRINGS
    return bool(value)


def _consumer_priority(arguments):
    """Consumer priority from ``x-priority`` (default 0)."""
    value = _argument(arguments, _PRIORITY_ARGUMENT, 0)
    try:
        return int(value)
    except (TypeError, ValueError):
        return 0


def _notify_on_cancel(callback, consumer_tag):
    """Invoke a cancel callback. Exceptions are swallowed."""
    if callback is None:
        return
    try:
        callback(consumer_tag)
    except Exception:
        pass


def _channel_can_consume(channel):
    """Return true when `channel` can still accept a delivery."""
    if channel is None or getattr(channel, 'closed', False):
        return False
    qos = getattr(channel, '_qos', None)
    if qos is None:
        # Prefetch defaults to unlimited until basic_qos creates a QoS.
        return True
    try:
        return qos.can_consume()
    except Exception:
        return False


#: Key format used for queue argument lookups in BrokerState.bindings.
binding_key_t = namedtuple('binding_key_t', (
    'queue', 'exchange', 'routing_key',
))

#: BrokerState.queue_bindings generates tuples in this format.
queue_binding_t = namedtuple('queue_binding_t', (
    'exchange', 'routing_key', 'arguments',
))


class Base64:
    """Base64 codec."""

    def encode(self, s):
        return bytes_to_str(base64.b64encode(str_to_bytes(s)))

    def decode(self, s):
        return base64.b64decode(str_to_bytes(s))


class NotEquivalentError(Exception):
    """Entity declaration is not equivalent to the previous declaration."""


class UndeliverableWarning(UserWarning):
    """The message could not be delivered to a queue."""


class BrokerState:
    """Broker state holds exchanges, queues and bindings.

    Consumer registrations are part of this state so every channel on the
    broker (and every connection sharing ``global_state``) sees one registry.
    """

    #: Mapping of exchange name to
    #: :class:`kombu.transport.virtual.exchange.ExchangeType`
    exchanges = None

    #: This is the actual bindings registry, used to store bindings and to
    #: test 'in' relationships in constant time.  It has the following
    #: structure::
    #:
    #:     {
    #:         (queue, exchange, routing_key): arguments,
    #:         # ...,
    #:     }
    bindings = None

    #: The queue index is used to access directly (constant time)
    #: all the bindings of a certain queue.  It has the following structure::
    #:
    #:     {
    #:         queue: {
    #:             (queue, exchange, routing_key),
    #:             # ...,
    #:         },
    #:         # ...,
    #:     }
    queue_index = None

    #: queue -> [consumer record, ...] ordered by priority (highest first).
    #: Equal priorities keep registration order.
    queue_consumers = None

    #: consumer_tag -> consumer record.
    consumers = None

    #: Queue names declared with ``x-single-active-consumer``.
    sac_queues = None

    #: queue -> active consumer tag for single-active-consumer queues.
    active_consumer = None

    #: Append-only consumer lifecycle events.
    consumer_events = None

    def __init__(self, exchanges=None):
        self.exchanges = {} if exchanges is None else exchanges
        self.bindings = {}
        self.queue_index = defaultdict(set)
        self.queue_consumers = {}
        self.consumers = {}
        self.sac_queues = set()
        self.active_consumer = {}
        self.consumer_events = []
        self._consumer_seq = count()

    def clear(self):
        self.exchanges.clear()
        self.bindings.clear()
        self.queue_index.clear()
        self.clear_consumer_state()
        self.sac_queues.clear()

    def clear_consumer_state(self):
        """Drop consumer registrations and lifecycle events.

        Queue single-active-consumer flags are kept: they belong to the
        queue declaration and must survive a new connection on transports
        that share this state. Callers that wipe the whole broker (``clear``)
        discard those flags separately.
        """
        self.queue_consumers.clear()
        self.consumers.clear()
        self.active_consumer.clear()
        self.consumer_events.clear()

    def is_sac(self, queue):
        return queue in self.sac_queues

    def enable_sac(self, queue):
        """Mark `queue` as single-active-consumer. Never clears the flag."""
        already = queue in self.sac_queues
        self.sac_queues.add(queue)
        if not already and queue not in self.active_consumer:
            consumers = self.queue_consumers.get(queue) or []
            if consumers:
                self.active_consumer[queue] = consumers[0]['consumer_tag']

    def disable_sac(self, queue):
        self.sac_queues.discard(queue)
        self.active_consumer.pop(queue, None)

    def consumers_for(self, queue):
        return list(self.queue_consumers.get(queue) or ())

    def all_consumers(self):
        records = list(self.consumers.values())
        records.sort(key=lambda record: (-record['priority'], record['seq']))
        return records

    def active_tag(self, queue):
        """Tag of the consumer considered active for `queue`."""
        consumers = self.queue_consumers.get(queue) or []
        if not consumers:
            return None
        if queue in self.sac_queues:
            tag = self.active_consumer.get(queue)
            record = self.consumers.get(tag) if tag is not None else None
            if record is not None and record['queue'] == queue:
                return tag
            return consumers[0]['consumer_tag']
        return consumers[0]['consumer_tag']

    def _emit(self, event_type, record):
        self.consumer_events.append({
            'type': event_type,
            'queue': record['queue'],
            'consumer_tag': record['consumer_tag'],
            'priority': record['priority'],
            'timestamp': time(),
        })

    def _forget_consumer(self, tag, queue):
        self.consumers.pop(tag, None)
        consumers = self.queue_consumers.get(queue)
        if consumers:
            self.queue_consumers[queue] = [
                record for record in consumers
                if record['consumer_tag'] != tag
            ]
            if not self.queue_consumers[queue]:
                self.queue_consumers.pop(queue, None)
        if self.active_consumer.get(queue) == tag:
            self.active_consumer.pop(queue, None)

    def register_consumer(self, record):
        """Insert `record` by priority.

        Returns the previously active record when a single-active-consumer
        queue demotes it (the caller fires ``on_cancel``). Otherwise None.
        """
        tag = record['consumer_tag']
        queue = record['queue']
        if tag in self.consumers:
            self._forget_consumer(tag, self.consumers[tag]['queue'])

        previous_active = self.active_tag(queue)
        consumers = self.queue_consumers.setdefault(queue, [])
        priority = record['priority']
        idx = 0
        while idx < len(consumers) and consumers[idx]['priority'] >= priority:
            idx += 1
        consumers.insert(idx, record)
        self.consumers[tag] = record
        self._emit('registered', record)

        if queue in self.sac_queues:
            if previous_active is None:
                self.active_consumer[queue] = tag
                self._emit('activated', record)
                return None
            previous = self.consumers.get(previous_active)
            if previous is not None and priority > previous['priority']:
                self.active_consumer[queue] = tag
                self._emit('demoted', previous)
                self._emit('activated', record)
                return previous
            if queue not in self.active_consumer and previous_active is not None:
                self.active_consumer[queue] = previous_active
            return None

        if consumers[0]['consumer_tag'] == tag:
            if previous_active is not None and previous_active != tag:
                previous = self.consumers.get(previous_active)
                if previous is not None:
                    self._emit('demoted', previous)
            self._emit('activated', record)
        return None

    def cancel_consumer(self, tag):
        """Remove `tag`.

        Returns ``(record, promoted)``. `promoted` is the standby that became
        active, or None.
        """
        record = self.consumers.get(tag)
        if record is None:
            return None, None
        queue = record['queue']
        was_active = self.active_tag(queue) == tag
        self._forget_consumer(tag, queue)
        self._emit('cancelled', record)
        promoted = None
        if was_active and queue in self.sac_queues:
            promoted = self._activate_next(queue)
        return record, promoted

    def _activate_next(self, queue):
        consumers = self.queue_consumers.get(queue) or []
        if not consumers:
            return None
        nxt = consumers[0]
        if queue in self.sac_queues:
            self.active_consumer[queue] = nxt['consumer_tag']
        self._emit('promoted', nxt)
        return nxt

    def promote_consumer(self, queue, tag):
        """Manually promote `tag` on a SAC queue.

        Returns ``(ok, demoted_record)``.
        """
        if queue not in self.sac_queues:
            return False, None
        record = self.consumers.get(tag)
        if record is None or record['queue'] != queue:
            return False, None
        current = self.active_tag(queue)
        if current == tag:
            return False, None
        demoted = self.consumers.get(current) if current is not None else None
        if demoted is not None:
            self._emit('demoted', demoted)
        self.active_consumer[queue] = tag
        self._emit('promoted', record)
        return True, demoted

    def select_delivery_consumer(self, queue):
        """Pick the consumer that should receive the next message."""
        records = self.queue_consumers.get(queue) or []
        if not records:
            return None
        if queue in self.sac_queues:
            record = self.consumers.get(self.active_tag(queue))
            if record is None:
                return None
            if _channel_can_consume(record.get('channel')):
                return record
            return None
        for record in records:
            if _channel_can_consume(record.get('channel')):
                return record
        return None

    def forget_if_present(self, record):
        """Drop `record` and emit ``cancelled`` when it is still registered."""
        tag = record['consumer_tag']
        if tag not in self.consumers:
            return False
        self._forget_consumer(tag, record['queue'])
        self._emit('cancelled', record)
        return True

    def has_binding(self, queue, exchange, routing_key):
        return (queue, exchange, routing_key) in self.bindings

    def binding_declare(self, queue, exchange, routing_key, arguments):
        key = binding_key_t(queue, exchange, routing_key)
        self.bindings.setdefault(key, arguments)
        self.queue_index[queue].add(key)

    def binding_delete(self, queue, exchange, routing_key):
        key = binding_key_t(queue, exchange, routing_key)
        try:
            del self.bindings[key]
        except KeyError:
            pass
        else:
            self.queue_index[queue].remove(key)

    def queue_bindings_delete(self, queue):
        try:
            bindings = self.queue_index.pop(queue)
        except KeyError:
            pass
        else:
            [self.bindings.pop(binding, None) for binding in bindings]

    def queue_bindings(self, queue):
        return (
            queue_binding_t(key.exchange, key.routing_key, self.bindings[key])
            for key in self.queue_index[queue]
        )


class QoS:
    """Quality of Service guarantees.

    Only supports `prefetch_count` at this point.

    Arguments:
    ---------
        channel (ChannelT): Connection channel.
        prefetch_count (int): Initial prefetch count (defaults to 0).
    """

    #: current prefetch count value
    prefetch_count = 0

    #: :class:`~collections.OrderedDict` of active messages.
    #: *NOTE*: Can only be modified by the consuming thread.
    _delivered = None

    #: acks can be done by other threads than the consuming thread.
    #: Instead of a mutex, which doesn't perform well here, we mark
    #: the delivery tags as dirty, so subsequent calls to append() can remove
    #: them.
    _dirty = None

    #: If disabled, unacked messages won't be restored at shutdown.
    restore_at_shutdown = True

    def __init__(self, channel, prefetch_count=0):
        self.channel = channel
        self.prefetch_count = prefetch_count or 0

        # Standard Python dictionaries do not support setting attributes
        # on the object, hence the use of OrderedDict
        self._delivered = OrderedDict()
        self._delivered.restored = False
        self._dirty = set()
        self._quick_ack = self._dirty.add
        self._quick_append = self._delivered.__setitem__
        self._on_collect = Finalize(
            self, self.restore_unacked_once, exitpriority=1,
        )

    def can_consume(self):
        """Return true if the channel can be consumed from.

        Used to ensure the client adhers to currently active
        prefetch limits.
        """
        pcount = self.prefetch_count
        return not pcount or len(self._delivered) - len(self._dirty) < pcount

    def can_consume_max_estimate(self):
        """Return the maximum number of messages allowed to be returned.

        Returns an estimated number of messages that a consumer may be allowed
        to consume at once from the broker.  This is used for services where
        bulk 'get message' calls are preferred to many individual 'get message'
        calls - like SQS.

        Returns
        -------
            int: greater than zero.
        """
        pcount = self.prefetch_count
        if pcount:
            return max(pcount - (len(self._delivered) - len(self._dirty)), 0)

    def append(self, message, delivery_tag):
        """Append message to transactional state."""
        if self._dirty:
            self._flush()
        self._quick_append(delivery_tag, message)

    def get(self, delivery_tag):
        return self._delivered[delivery_tag]

    def _flush(self):
        """Flush dirty (acked/rejected) tags from."""
        dirty = self._dirty
        delivered = self._delivered
        while 1:
            try:
                dirty_tag = dirty.pop()
            except KeyError:
                break
            delivered.pop(dirty_tag, None)

    def ack(self, delivery_tag):
        """Acknowledge message and remove from transactional state."""
        self._quick_ack(delivery_tag)

    def reject(self, delivery_tag, requeue=False):
        """Remove from transactional state and requeue message."""
        if requeue:
            self.channel._restore_at_beginning(self._delivered[delivery_tag])
        self._quick_ack(delivery_tag)

    def restore_unacked(self):
        """Restore all unacknowledged messages."""
        self._flush()
        delivered = self._delivered
        errors = []
        restore = self.channel._restore
        pop_message = delivered.popitem

        while delivered:
            try:
                _, message = pop_message()
            except KeyError:  # pragma: no cover
                break

            try:
                restore(message)
            except BaseException as exc:
                errors.append((exc, message))
        delivered.clear()
        return errors

    def restore_unacked_once(self, stderr=None):
        """Restore all unacknowledged messages at shutdown/gc collect.

        Note:
        ----
            Can only be called once for each instance, subsequent
            calls will be ignored.
        """
        self._on_collect.cancel()
        self._flush()
        stderr = sys.stderr if stderr is None else stderr
        state = self._delivered

        if not self.restore_at_shutdown or not self.channel.do_restore:
            return
        if getattr(state, 'restored', None):
            assert not state
            return
        try:
            if state:
                print(RESTORING_FMT.format(len(self._delivered)),
                      file=stderr)
                unrestored = self.restore_unacked()

                if unrestored:
                    errors, messages = list(zip(*unrestored))
                    print(RESTORE_PANIC_FMT.format(len(errors), errors),
                          file=stderr)
                    emergency_dump_state(messages, stderr=stderr)
        finally:
            state.restored = True

    def restore_visible(self, *args, **kwargs):
        """Restore any pending unacknowledged messages.

        To be filled in for visibility_timeout style implementations.

        Note:
        ----
            This is implementation optional, and currently only
            used by the Redis transport.
        """


class Message(base.Message):
    """Message object."""

    def __init__(self, payload, channel=None, **kwargs):
        self._raw = payload
        properties = payload['properties']
        body = payload.get('body')
        if body:
            body = channel.decode_body(body, properties.get('body_encoding'))
        super().__init__(
            body=body,
            channel=channel,
            delivery_tag=properties['delivery_tag'],
            content_type=payload.get('content-type'),
            content_encoding=payload.get('content-encoding'),
            headers=payload.get('headers'),
            properties=properties,
            delivery_info=properties.get('delivery_info'),
            postencode='utf-8',
            **kwargs)

    def serializable(self):
        props = self.properties
        body, _ = self.channel.encode_body(self.body,
                                           props.get('body_encoding'))
        headers = dict(self.headers)
        # remove compression header
        headers.pop('compression', None)
        return {
            'body': body,
            'properties': props,
            'content-type': self.content_type,
            'content-encoding': self.content_encoding,
            'headers': headers,
        }


class AbstractChannel:
    """Abstract channel interface.

    This is an abstract class defining the channel methods
    you'd usually want to implement in a virtual channel.

    Note:
    ----
        Do not subclass directly, but rather inherit
        from :class:`Channel`.
    """

    def _get(self, queue, timeout=None):
        """Get next message from `queue`."""
        raise NotImplementedError('Virtual channels must implement _get')

    def _put(self, queue, message):
        """Put `message` onto `queue`."""
        raise NotImplementedError('Virtual channels must implement _put')

    def _purge(self, queue):
        """Remove all messages from `queue`."""
        raise NotImplementedError('Virtual channels must implement _purge')

    def _size(self, queue):
        """Return the number of messages in `queue` as an :class:`int`."""
        return 0

    def _delete(self, queue, *args, **kwargs):
        """Delete `queue`.

        Note:
        ----
            This just purges the queue, if you need to do more you can
            override this method.
        """
        self._purge(queue)

    def _new_queue(self, queue, **kwargs):
        """Create new queue.

        Note:
        ----
            Your transport can override this method if it needs
            to do something whenever a new queue is declared.
        """

    def _has_queue(self, queue, **kwargs):
        """Verify that queue exists.

        Returns
        -------
            bool: Should return :const:`True` if the queue exists
                or :const:`False` otherwise.
        """
        return True

    def _poll(self, cycle, callback, timeout=None):
        """Poll a list of queues for available messages."""
        return cycle.get(callback)

    def _get_and_deliver(self, queue, callback):
        # Leave the message queued when every registered consumer is blocked
        # (prefetch full, or a single-active consumer that cannot take it).
        if self.state.queue_consumers.get(queue):
            if self.state.select_delivery_consumer(queue) is None:
                raise Empty()
        message = self._get(queue)
        callback(message, queue)


class Channel(AbstractChannel, base.StdChannel):
    """Virtual channel.

    Arguments:
    ---------
        connection (ConnectionT): The transport instance this
            channel is part of.
    """

    #: message class used.
    Message = Message

    #: QoS class used.
    QoS = QoS

    #: flag to restore unacked messages when channel
    #: goes out of scope.
    do_restore = True

    #: mapping of exchange types and corresponding classes.
    exchange_types = dict(STANDARD_EXCHANGE_TYPES)

    #: flag set if the channel supports fanout exchanges.
    supports_fanout = False

    #: Binary <-> ASCII codecs.
    codecs = {'base64': Base64()}

    #: Default body encoding.
    #: NOTE: ``transport_options['body_encoding']`` will override this value.
    body_encoding = 'base64'

    #: counter used to generate delivery tags for this channel.
    _delivery_tags = count(1)

    #: Optional queue where messages with no route is delivered.
    #: Set by ``transport_options['deadletter_queue']``.
    deadletter_queue = None

    # List of options to transfer from :attr:`transport_options`.
    from_transport_options = ('body_encoding', 'deadletter_queue')

    # Priority defaults
    default_priority = 0
    min_priority = 0
    max_priority = 9

    def __init__(self, connection, **kwargs):
        self.connection = connection
        self._consumers = set()
        self._cycle = None
        self._tag_to_queue = {}
        self._active_queues = []
        self._qos = None
        self.closed = False

        # instantiate exchange types
        self.exchange_types = {
            typ: cls(self) for typ, cls in self.exchange_types.items()
        }

        self.channel_id = self._get_free_channel_id()

        topts = self.connection.client.transport_options
        for opt_name in self.from_transport_options:
            try:
                setattr(self, opt_name, topts[opt_name])
            except KeyError:
                pass

    def exchange_declare(self, exchange=None, type='direct', durable=False,
                         auto_delete=False, arguments=None,
                         nowait=False, passive=False):
        """Declare exchange."""
        type = type or 'direct'
        exchange = exchange or 'amq.%s' % type
        if passive:
            if exchange not in self.state.exchanges:
                raise ChannelError(
                    'NOT_FOUND - no exchange {!r} in vhost {!r}'.format(
                        exchange, self.connection.client.virtual_host or '/'),
                    (50, 10), 'Channel.exchange_declare', '404',
                )
            return
        try:
            prev = self.state.exchanges[exchange]
            if not self.typeof(exchange).equivalent(prev, exchange, type,
                                                    durable, auto_delete,
                                                    arguments):
                raise NotEquivalentError(NOT_EQUIVALENT_FMT.format(
                    exchange, self.connection.client.virtual_host or '/'))
        except KeyError:
            self.state.exchanges[exchange] = {
                'type': type,
                'durable': durable,
                'auto_delete': auto_delete,
                'arguments': arguments or {},
                'table': [],
            }

    def exchange_delete(self, exchange, if_unused=False, nowait=False):
        """Delete `exchange` and all its bindings."""
        for rkey, _, queue in self.get_table(exchange):
            self.queue_delete(queue, if_unused=True, if_empty=True)
        self.state.exchanges.pop(exchange, None)

    def queue_declare(self, queue=None, passive=False, **kwargs):
        """Declare queue."""
        queue = queue or 'amq.gen-%s' % uuid()
        if passive and not self._has_queue(queue, **kwargs):
            raise ChannelError(
                'NOT_FOUND - no queue {!r} in vhost {!r}'.format(
                    queue, self.connection.client.virtual_host or '/'),
                (50, 10), 'Channel.queue_declare', '404',
            )
        else:
            # Once enabled, single-active-consumer survives a redeclare that
            # omits the argument (and a redeclare that sets it false).
            if not passive and _sac_enabled(
                _argument(kwargs.get('arguments'), _SAC_ARGUMENT),
            ):
                newly = not self.state.is_sac(queue)
                self.state.enable_sac(queue)
                if newly:
                    self._restrict_sac_polling(queue)
            self._new_queue(queue, **kwargs)
        return queue_declare_ok_t(queue, self._size(queue), 0)

    def queue_delete(self, queue, if_unused=False, if_empty=False, **kwargs):
        """Delete queue."""
        if if_empty and self._size(queue):
            return
        self._cancel_queue_consumers(queue)
        for exchange, routing_key, args in self.state.queue_bindings(queue):
            meta = self.typeof(exchange).prepare_bind(
                queue, exchange, routing_key, args,
            )
            self._delete(queue, exchange, *meta, **kwargs)
        self.state.queue_bindings_delete(queue)

    def after_reply_message_received(self, queue):
        self.queue_delete(queue)

    def exchange_bind(self, destination, source='', routing_key='',
                      nowait=False, arguments=None):
        raise NotImplementedError('transport does not support exchange_bind')

    def exchange_unbind(self, destination, source='', routing_key='',
                        nowait=False, arguments=None):
        raise NotImplementedError('transport does not support exchange_unbind')

    def queue_bind(self, queue, exchange=None, routing_key='',
                   arguments=None, **kwargs):
        """Bind `queue` to `exchange` with `routing key`."""
        exchange = exchange or 'amq.direct'
        if self.state.has_binding(queue, exchange, routing_key):
            return
        # Add binding:
        self.state.binding_declare(queue, exchange, routing_key, arguments)
        # Update exchange's routing table:
        table = self.state.exchanges[exchange].setdefault('table', [])
        meta = self.typeof(exchange).prepare_bind(
            queue, exchange, routing_key, arguments,
        )
        table.append(meta)
        if self.supports_fanout:
            self._queue_bind(exchange, *meta)

    def queue_unbind(self, queue, exchange=None, routing_key='',
                     arguments=None, **kwargs):
        # Remove queue binding:
        self.state.binding_delete(queue, exchange, routing_key)
        try:
            table = self.get_table(exchange)
        except KeyError:
            return
        binding_meta = self.typeof(exchange).prepare_bind(
            queue, exchange, routing_key, arguments,
        )
        # TODO: the complexity of this operation is O(number of bindings).
        # Should be optimized.  Modifying table in place.
        table[:] = [meta for meta in table if meta != binding_meta]

    def list_bindings(self):
        return ((queue, exchange, rkey)
                for exchange in self.state.exchanges
                for rkey, pattern, queue in self.get_table(exchange))

    def queue_purge(self, queue, **kwargs):
        """Remove all ready messages from queue."""
        return self._purge(queue)

    def _next_delivery_tag(self):
        return uuid()

    def basic_publish(self, message, exchange, routing_key, **kwargs):
        """Publish message."""
        self._inplace_augment_message(message, exchange, routing_key)
        if exchange:
            return self.typeof(exchange).deliver(
                message, exchange, routing_key, **kwargs
            )
        # anon exchange: routing_key is the destination queue
        return self._put(routing_key, message, **kwargs)

    def _inplace_augment_message(self, message, exchange, routing_key):
        message['body'], body_encoding = self.encode_body(
            message['body'], self.body_encoding,
        )
        props = message['properties']
        props.update(
            body_encoding=body_encoding,
            delivery_tag=self._next_delivery_tag(),
        )
        props['delivery_info'].update(
            exchange=exchange,
            routing_key=routing_key,
        )

    def basic_consume(self, queue, no_ack, callback, consumer_tag, **kwargs):
        """Consume from `queue`.

        Consumer priority is ``x-priority`` in consumer arguments (default 0).
        ``on_cancel`` is invoked with the consumer tag when this consumer is
        cancelled or demoted from a single-active-consumer queue. Consumers
        are ordered by priority, highest first; equal priority keeps
        registration order. On a single-active-consumer queue only the first
        consumer in that order receives messages.
        """
        arguments = kwargs.get('arguments')
        priority = _consumer_priority(arguments)
        on_cancel = kwargs.get('on_cancel')

        def _callback(raw_message):
            message = self.Message(raw_message, channel=self)
            if not no_ack:
                self.qos.append(message, message.delivery_tag)
            return callback(message)

        record = {
            'consumer_tag': consumer_tag,
            'queue': queue,
            'priority': priority,
            'channel': self,
            'callback': _callback,
            'on_cancel': on_cancel,
            'seq': next(self.state._consumer_seq),
        }
        demoted = self.state.register_consumer(record)
        self._tag_to_queue[consumer_tag] = queue
        self._consumers.add(consumer_tag)

        if demoted is not None:
            _notify_on_cancel(demoted.get('on_cancel'), demoted['consumer_tag'])
            self._stop_polling(demoted)

        if self.state.is_sac(queue):
            if self.state.active_tag(queue) == consumer_tag:
                self._start_polling(record)
        else:
            self._active_queues.append(queue)

        self._install_queue_dispatcher(queue)
        self._reset_cycle()

    def basic_cancel(self, consumer_tag):
        """Cancel consumer by consumer tag."""
        if consumer_tag in self._consumers:
            self._consumers.remove(consumer_tag)
            queue = self._tag_to_queue.pop(consumer_tag, None)
            record, promoted = self.state.cancel_consumer(consumer_tag)
            if record is not None:
                queue = queue or record['queue']
            if queue is not None and self.state.is_sac(queue):
                # A standby on the active consumer's channel must not
                # remove the queue from the poll list.
                active = self.state.consumers.get(
                    self.state.active_tag(queue) or '',
                )
                if active is None or active.get('channel') is not self:
                    self._stop_polling({'channel': self, 'queue': queue})
                if promoted is not None:
                    self._start_polling(promoted)
            else:
                try:
                    self._active_queues.remove(queue)
                except ValueError:
                    pass
            self._refresh_queue_callback(queue)
            if record is not None:
                _notify_on_cancel(
                    record.get('on_cancel'), record['consumer_tag'],
                )
            self._reset_cycle()

    def basic_get(self, queue, no_ack=False, **kwargs):
        """Get message by direct access (synchronous)."""
        try:
            message = self.Message(self._get(queue), channel=self)
            if not no_ack:
                self.qos.append(message, message.delivery_tag)
            return message
        except Empty:
            pass

    def basic_ack(self, delivery_tag, multiple=False):
        """Acknowledge message."""
        self.qos.ack(delivery_tag)

    def basic_recover(self, requeue=False):
        """Recover unacked messages."""
        if requeue:
            return self.qos.restore_unacked()
        raise NotImplementedError('Does not support recover(requeue=False)')

    def basic_reject(self, delivery_tag, requeue=False):
        """Reject message."""
        self.qos.reject(delivery_tag, requeue=requeue)

    def basic_qos(self, prefetch_size=0, prefetch_count=0,
                  apply_global=False):
        """Change QoS settings for this channel.

        Note:
        ----
            Only `prefetch_count` is supported.
        """
        self.qos.prefetch_count = prefetch_count

    def get_exchanges(self):
        return list(self.state.exchanges)

    def get_table(self, exchange):
        """Get table of bindings for `exchange`."""
        return self.state.exchanges[exchange]['table']

    def typeof(self, exchange, default='direct'):
        """Get the exchange type instance for `exchange`."""
        try:
            type = self.state.exchanges[exchange]['type']
        except KeyError:
            type = default
        return self.exchange_types[type]

    def _lookup(self, exchange, routing_key, default=None):
        """Find all queues matching `routing_key` for the given `exchange`.

        Returns
        -------
            list[str]: queue names -- must return `[default]`
                if default is set and no queues matched.
        """
        if default is None:
            default = self.deadletter_queue
        if not exchange:  # anon exchange
            return [routing_key or default]

        try:
            R = self.typeof(exchange).lookup(
                self.get_table(exchange),
                exchange, routing_key, default,
            )
        except KeyError:
            R = []

        if not R and default is not None:
            warnings.warn(UndeliverableWarning(UNDELIVERABLE_FMT.format(
                exchange=exchange, routing_key=routing_key)),
            )
            self._new_queue(default)
            R = [default]
        return R

    def _restore(self, message):
        """Redeliver message to its original destination."""
        delivery_info = message.delivery_info
        message = message.serializable()
        message['redelivered'] = True
        for queue in self._lookup(
            delivery_info['exchange'],
                delivery_info['routing_key']):
            self._put(queue, message)

    def _restore_at_beginning(self, message):
        return self._restore(message)

    def drain_events(self, timeout=None, callback=None):
        callback = callback or self.connection._deliver
        if self._consumers and self.qos.can_consume():
            if hasattr(self, '_get_many'):
                return self._get_many(self._active_queues, timeout=timeout)
            return self._poll(self.cycle, callback, timeout=timeout)
        raise Empty()

    def message_to_python(self, raw_message):
        """Convert raw message to :class:`Message` instance."""
        if not isinstance(raw_message, self.Message):
            return self.Message(payload=raw_message, channel=self)
        return raw_message

    def prepare_message(self, body, priority=None, content_type=None,
                        content_encoding=None, headers=None, properties=None):
        """Prepare message data."""
        properties = properties or {}
        properties.setdefault('delivery_info', {})
        properties.setdefault('priority', priority or self.default_priority)

        return {'body': body,
                'content-encoding': content_encoding,
                'content-type': content_type,
                'headers': headers or {},
                'properties': properties or {}}

    def flow(self, active=True):
        """Enable/disable message flow.

        Raises
        ------
            NotImplementedError: as flow
                is not implemented by the base virtual implementation.
        """
        raise NotImplementedError('virtual channels do not support flow.')

    def close(self):
        """Close channel.

        Cancel all consumers, and requeue unacked messages.
        """
        if not self.closed:
            self.closed = True
            self._cancel_local_consumers()
            if self._qos:
                self._qos.restore_unacked_once()
            if self._cycle is not None:
                self._cycle.close()
                self._cycle = None
            if self.connection is not None:
                self.connection.close_channel(self)
        self.exchange_types = None

    def encode_body(self, body, encoding=None):
        if encoding and encoding.lower() != 'utf-8':
            return self.codecs.get(encoding).encode(body), encoding
        return body, encoding

    def decode_body(self, body, encoding=None):
        if encoding and encoding.lower() != 'utf-8':
            return self.codecs.get(encoding).decode(body)
        return body

    def _reset_cycle(self):
        self._cycle = FairCycle(
            self._get_and_deliver, self._active_queues, Empty)

    def _install_queue_dispatcher(self, queue):
        """Point ``_callbacks[queue]`` at the consumer selected at delivery."""
        connection = self.connection
        if connection is None or queue is None:
            return
        state = self.state
        requeue_channel = self

        def _dispatch(raw_message):
            record = state.select_delivery_consumer(queue)
            callback = None if record is None else record.get('callback')
            if callback is None:
                if not getattr(requeue_channel, 'closed', False):
                    try:
                        requeue_channel._put(queue, raw_message)
                    except Exception:
                        pass
                return None
            return callback(raw_message)

        connection._callbacks[queue] = _dispatch

    def _refresh_queue_callback(self, queue):
        if queue is None or self.connection is None:
            return
        if self.state.queue_consumers.get(queue):
            self._install_queue_dispatcher(queue)
        else:
            self.connection._callbacks.pop(queue, None)

    def _start_polling(self, record):
        """Let the active SAC consumer's channel poll `queue`."""
        channel = record.get('channel')
        queue = record.get('queue')
        if channel is None or queue is None or getattr(channel, 'closed', False):
            return
        active = getattr(channel, '_active_queues', None)
        if active is not None and queue not in active:
            active.append(queue)

    def _stop_polling(self, record):
        """Stop a demoted consumer's channel from polling `queue`."""
        channel = record.get('channel')
        queue = record.get('queue')
        if channel is None or queue is None:
            return
        active = getattr(channel, '_active_queues', None)
        if active is None:
            return
        try:
            active.remove(queue)
        except ValueError:
            pass

    def _restrict_sac_polling(self, queue):
        active_tag = self.state.active_tag(queue)
        for record in self.state.consumers_for(queue):
            if record['consumer_tag'] == active_tag:
                self._start_polling(record)
            else:
                self._stop_polling(record)

    def _detach_channel_consumer(self, channel, tag, queue):
        consumers = getattr(channel, '_consumers', None)
        if consumers is not None and tag in consumers:
            consumers.remove(tag)
        tag_to_queue = getattr(channel, '_tag_to_queue', None)
        if tag_to_queue is not None:
            tag_to_queue.pop(tag, None)
        self._stop_polling({
            'channel': channel,
            'queue': queue,
        })

    def _cancel_queue_consumers(self, queue):
        """Notify and remove every consumer, then drop SAC status."""
        records = self.state.consumers_for(queue)
        for record in records:
            _notify_on_cancel(record.get('on_cancel'), record['consumer_tag'])
        connections = []
        for record in records:
            channel = record.get('channel')
            if channel is not None:
                self._detach_channel_consumer(
                    channel, record['consumer_tag'], queue,
                )
                conn = getattr(channel, 'connection', None)
                if conn is not None:
                    connections.append(conn)
            self.state.forget_if_present(record)
        # Drop anyone registered reentrantly from an on_cancel callback.
        for record in self.state.consumers_for(queue):
            self._detach_channel_consumer(
                record.get('channel'), record['consumer_tag'], queue,
            )
            self.state.consumers.pop(record['consumer_tag'], None)
        self.state.queue_consumers.pop(queue, None)
        self.state.disable_sac(queue)
        if self.connection is not None:
            connections.append(self.connection)
        for conn in connections:
            callbacks = getattr(conn, '_callbacks', None)
            if callbacks is not None:
                callbacks.pop(queue, None)

    def _cancel_local_consumers(self):
        """Cancel this channel's consumers, standbys before the active one.

        Cancelling standbys first lets SAC promotion select a consumer that
        is not about to be removed with this channel.
        """
        tags = list(self._consumers)
        active = []
        standby = []
        for tag in tags:
            record = self.state.consumers.get(tag)
            queue = None if record is None else record['queue']
            if queue is not None and self.state.active_tag(queue) == tag:
                active.append(tag)
            else:
                standby.append(tag)
        for tag in standby + active:
            if tag in self._consumers:
                self.basic_cancel(tag)

    def promote_consumer(self, queue, consumer_tag):
        """Make `consumer_tag` the active consumer of a SAC queue.

        Returns True if that consumer became active. Returns False when the
        queue is not single-active-consumer, the tag is unknown, or the
        consumer is already active.
        """
        ok, demoted = self.state.promote_consumer(queue, consumer_tag)
        if not ok:
            return False
        if demoted is not None:
            self._stop_polling(demoted)
        record = self.state.consumers.get(consumer_tag)
        if record is not None:
            self._start_polling(record)
        return True

    def _consumer_dict(self, record, active_tag):
        return {
            'queue': record['queue'],
            'consumer_tag': record['consumer_tag'],
            'priority': record['priority'],
            'is_active': record['consumer_tag'] == active_tag,
        }

    def consumer_info(self, queue=None):
        """Return consumer dicts ordered by priority (highest first)."""
        if queue is None:
            records = self.state.all_consumers()
        else:
            records = self.state.consumers_for(queue)
        active = {}
        info = []
        for record in records:
            qname = record['queue']
            if qname not in active:
                active[qname] = self.state.active_tag(qname)
            info.append(self._consumer_dict(record, active[qname]))
        return info

    def get_consumer_count(self, queue=None):
        """Return the number of registered consumers."""
        if queue is None:
            return len(self.state.consumers)
        return len(self.state.queue_consumers.get(queue) or ())

    def get_active_consumer(self, queue):
        """Return the active consumer tag.

        Non-SAC queues treat the highest-priority consumer as active.
        """
        return self.state.active_tag(queue)

    def get_sac_status(self, queue):
        """Return SAC status, or None when the queue is not SAC."""
        if not self.state.is_sac(queue):
            return None
        records = self.state.consumers_for(queue)
        active = self.state.active_tag(queue)
        return {
            'queue': queue,
            'active': active,
            'standby': [
                record['consumer_tag'] for record in records
                if record['consumer_tag'] != active
            ],
            'consumer_count': len(records),
        }

    def get_standby_consumers(self, queue):
        """Return standby consumer tags for a SAC queue (empty otherwise)."""
        status = self.get_sac_status(queue)
        if status is None:
            return []
        return list(status['standby'])

    def get_consumer_priority(self, consumer_tag):
        """Return the priority of `consumer_tag`, or None if unknown."""
        record = self.state.consumers.get(consumer_tag)
        if record is None:
            return None
        return record['priority']

    def is_single_active_consumer(self, queue):
        """Return True if `queue` is a single-active-consumer queue."""
        return self.state.is_sac(queue)

    def list_consumers(self):
        """Return this channel's consumers, ordered by priority."""
        return [
            info for info in self.consumer_info()
            if self.state.consumers.get(info['consumer_tag'], {}).get('channel') is self
        ]

    @property
    def consumer_tags(self):
        """This channel's consumer tags, sorted."""
        return sorted(self._consumers)

    def consumer_priority_map(self, queue):
        """Return ``{consumer_tag: priority}`` for `queue`."""
        return {
            record['consumer_tag']: record['priority']
            for record in self.state.queue_consumers.get(queue) or ()
        }

    def consumer_registry_snapshot(self):
        """Return ``{queue: [{consumer_tag, priority, is_active}, ...]}``."""
        snapshot = {}
        for queue, records in self.state.queue_consumers.items():
            active = self.state.active_tag(queue)
            snapshot[queue] = [
                {
                    'consumer_tag': record['consumer_tag'],
                    'priority': record['priority'],
                    'is_active': record['consumer_tag'] == active,
                }
                for record in records
            ]
        return snapshot

    def consumer_events(self, queue=None, event_type=None):
        """Return consumer lifecycle events.

        Each event has ``type``, ``queue``, ``consumer_tag``, ``priority``
        and ``timestamp``. Types are ``registered``, ``activated``,
        ``demoted``, ``cancelled`` and ``promoted``.
        """
        events = self.state.consumer_events
        if queue is not None:
            events = [event for event in events if event['queue'] == queue]
        if event_type is not None:
            events = [event for event in events if event['type'] == event_type]
        return [dict(event) for event in events]

    def clear_consumer_events(self):
        """Clear the consumer lifecycle log."""
        self.state.consumer_events.clear()

    def __enter__(self):
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc_val: BaseException | None,
        exc_tb: TracebackType | None
    ) -> None:
        self.close()

    @property
    def state(self):
        """Broker state containing exchanges and bindings."""
        return self.connection.state

    @property
    def qos(self):
        """:class:`QoS` manager for this channel."""
        if self._qos is None:
            self._qos = self.QoS(self)
        return self._qos

    @property
    def cycle(self):
        if self._cycle is None:
            self._reset_cycle()
        return self._cycle

    def _get_message_priority(self, message, reverse=False):
        """Get priority from message.

        The value is limited to within a boundary of 0 to 9.

        Note:
        ----
            Higher value has more priority.
        """
        try:
            priority = max(
                min(int(message['properties']['priority']),
                    self.max_priority),
                self.min_priority,
            )
        except (TypeError, ValueError, KeyError):
            priority = self.default_priority

        return (self.max_priority - priority) if reverse else priority

    def _get_free_channel_id(self):
        # Cast to a set for fast lookups, and keep stored as an array
        # for lower memory usage.
        used_channel_ids = set(self.connection._used_channel_ids)

        for channel_id in range(1, self.connection.channel_max + 1):
            if channel_id not in used_channel_ids:
                self.connection._used_channel_ids.append(channel_id)
                return channel_id

        raise ResourceError(
            'No free channel ids, current={}, channel_max={}'.format(
                len(self.connection.channels),
                self.connection.channel_max), (20, 10),
        )


class Management(base.Management):
    """Base class for the AMQP management API."""

    def __init__(self, transport):
        super().__init__(transport)
        self.channel = transport.client.channel()

    def get_bindings(self):
        return [{'destination': q, 'source': e, 'routing_key': r}
                for q, e, r in self.channel.list_bindings()]

    def close(self):
        self.channel.close()


class Transport(base.Transport):
    """Virtual transport.

    Arguments:
    ---------
        client (kombu.Connection): The client this is a transport for.
    """

    Channel = Channel
    Cycle = FairCycle
    Management = Management

    #: :class:`~kombu.utils.scheduling.FairCycle` instance
    #: used to fairly drain events from channels (set by constructor).
    cycle = None

    #: port number used when no port is specified.
    default_port = None

    #: active channels.
    channels = None

    #: queue/callback map.
    _callbacks = None

    #: Time to sleep between unsuccessful polls.
    polling_interval = 1.0

    #: Max number of channels
    channel_max = 65535

    implements = base.Transport.implements.extend(
        asynchronous=False,
        exchange_type=frozenset(['direct', 'topic']),
        heartbeats=False,
    )

    def __init__(self, client, **kwargs):
        self.client = client
        # :class:`BrokerState` containing declared exchanges and bindings.
        self.state = BrokerState()
        self.channels = []
        self._avail_channels = []
        self._callbacks = {}
        self.cycle = self.Cycle(self._drain_channel, self.channels, Empty)
        polling_interval = client.transport_options.get('polling_interval')
        if polling_interval is not None:
            self.polling_interval = polling_interval
        self._used_channel_ids = array(ARRAY_TYPE_H)

    def create_channel(self, connection):
        try:
            return self._avail_channels.pop()
        except IndexError:
            channel = self.Channel(connection)
            self.channels.append(channel)
            return channel

    def close_channel(self, channel):
        try:
            try:
                self._used_channel_ids.remove(channel.channel_id)
            except ValueError:
                # channel id already removed
                pass
            try:
                self.channels.remove(channel)
            except ValueError:
                pass
        finally:
            channel.connection = None

    def establish_connection(self):
        # creates channel to verify connection.
        # this channel is then used as the next requested channel.
        # (returned by ``create_channel``).
        self._avail_channels.append(self.create_channel(self))
        return self  # for drain events

    def close_connection(self, connection):
        self.cycle.close()
        for chan_list in self._avail_channels, self.channels:
            while chan_list:
                try:
                    channel = chan_list.pop()
                except LookupError:  # pragma: no cover
                    pass
                else:
                    channel.close()

    def drain_events(self, connection, timeout=None):
        time_start = monotonic()
        get = self.cycle.get
        polling_interval = self.polling_interval
        if timeout and polling_interval and polling_interval > timeout:
            polling_interval = timeout
        while 1:
            try:
                get(self._deliver, timeout=timeout)
            except Empty:
                if timeout is not None and monotonic() - time_start >= timeout:
                    raise socket.timeout()
                if polling_interval is not None:
                    sleep(polling_interval)
            else:
                break

    def _deliver(self, message, queue):
        if not queue:
            raise KeyError(
                'Received message without destination queue: {}'.format(
                    message))
        try:
            callback = self._callbacks[queue]
        except KeyError:
            logger.warning(W_NO_CONSUMERS, queue)
            self._reject_inbound_message(message)
        else:
            callback(message)

    def _reject_inbound_message(self, raw_message):
        for channel in self.channels:
            if channel:
                message = channel.Message(raw_message, channel=channel)
                channel.qos.append(message, message.delivery_tag)
                channel.basic_reject(message.delivery_tag, requeue=True)
                break

    def on_message_ready(self, channel, message, queue):
        if not queue or queue not in self._callbacks:
            raise KeyError(
                'Message for queue {!r} without consumers: {}'.format(
                    queue, message))
        self._callbacks[queue](message)

    def _drain_channel(self, channel, callback, timeout=None):
        return channel.drain_events(callback=callback, timeout=timeout)

    @property
    def default_connection_params(self):
        return {'port': self.default_port, 'hostname': 'localhost'}
