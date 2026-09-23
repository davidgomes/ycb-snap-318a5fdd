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


def _coerce_priority(value, default=0):
    """Return an integer consumer priority."""
    if value is None:
        return default
    try:
        return int(value)
    except (TypeError, ValueError):
        return default


def _is_sac_enabled(value):
    """Return true when a queue argument enables single-active-consumer."""
    if isinstance(value, str):
        return value.strip().lower() in {'1', 'true', 'yes', 'on'}
    return bool(value)


def _channel_can_consume(channel):
    """Return true when `channel` can still accept a delivery."""
    if channel is None or getattr(channel, 'closed', False):
        return False
    # Use the existing QoS object only. Touching ``channel.qos`` would
    # create one as a side effect of consumer selection.
    qos = getattr(channel, '_qos', None)
    if qos is None:
        return True
    try:
        return bool(qos.can_consume())
    except Exception:
        return False


def _requeue_virtual_message(connection, queue, raw_message):
    """Put a message back when no eligible consumer can take it."""
    channels = list(getattr(connection, 'channels', []) or [])
    for channel in channels:
        if channel is None or getattr(channel, 'closed', False):
            continue
        try:
            channel._put(queue, raw_message)
        except Exception:
            continue
        else:
            return


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
    """Broker state holds exchanges, queues and bindings."""

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

    #: Queues declared with ``x-single-active-consumer``.
    sac_queues = None

    #: Priority-ordered consumer registrations, shared across channels.
    queue_consumers = None

    #: consumer tag → registration.
    consumers = None

    #: Active consumer tag for single-active-consumer queues.
    active_consumers = None

    #: Consumer lifecycle events.
    consumer_events = None

    def __init__(self, exchanges=None):
        self.exchanges = {} if exchanges is None else exchanges
        self.bindings = {}
        self.queue_index = defaultdict(set)
        self._init_consumer_state()

    def _init_consumer_state(self):
        self.sac_queues = set()
        self.queue_consumers = defaultdict(list)
        self.consumers = {}
        self.active_consumers = {}
        self.consumer_events = []
        self._consumer_seq = count()

    def clear(self):
        self.exchanges.clear()
        self.bindings.clear()
        self.queue_index.clear()
        self.clear_consumers()

    def clear_consumers(self):
        """Drop consumer registrations, SAC flags and lifecycle events.

        Exchanges, queues and bindings are left intact. Transports that
        share one :class:`BrokerState` across connections call this so
        consumer registrations do not leak into the next connection.
        """
        if getattr(self, 'queue_consumers', None) is None:
            self._init_consumer_state()
            return
        self.sac_queues.clear()
        self.queue_consumers.clear()
        self.consumers.clear()
        self.active_consumers.clear()
        self.consumer_events.clear()

    def enable_single_active_consumer(self, queue):
        """Mark `queue` as single-active-consumer.

        Redeclaring a queue without the argument must not clear this flag,
        so this method only ever adds the queue.
        """
        self.sac_queues.add(queue)
        if queue not in self.active_consumers:
            consumers = self.queue_consumers.get(queue) or []
            if consumers:
                self.active_consumers[queue] = consumers[0]['consumer_tag']

    def is_single_active_consumer(self, queue):
        return queue in self.sac_queues

    def add_consumer(self, record):
        """Insert `record`, highest priority first.

        Equal priorities keep registration order. Returns the insert index.
        """
        queue = record['queue']
        consumers = self.queue_consumers[queue]
        priority = record['priority']
        index = len(consumers)
        for position, existing in enumerate(consumers):
            if existing['priority'] < priority:
                index = position
                break
        record['seq'] = next(self._consumer_seq)
        consumers.insert(index, record)
        self.consumers[record['consumer_tag']] = record
        return index

    def remove_consumer(self, consumer_tag):
        """Remove a consumer registration. Returns the record, if any."""
        record = self.consumers.pop(consumer_tag, None)
        if record is None:
            return None
        queue = record['queue']
        consumers = self.queue_consumers.get(queue)
        if consumers:
            self.queue_consumers[queue] = [
                item for item in consumers
                if item['consumer_tag'] != consumer_tag
            ]
            if not self.queue_consumers[queue]:
                self.queue_consumers.pop(queue, None)
        if self.active_consumers.get(queue) == consumer_tag:
            self.active_consumers.pop(queue, None)
        return record

    def drop_queue_consumers(self, queue):
        """Remove every consumer on `queue` and return their records."""
        records = list(self.queue_consumers.pop(queue, []))
        self.active_consumers.pop(queue, None)
        for record in records:
            self.consumers.pop(record['consumer_tag'], None)
        return records

    def record_consumer_event(self, event_type, queue, consumer_tag, priority):
        self.consumer_events.append({
            'type': event_type,
            'queue': queue,
            'consumer_tag': consumer_tag,
            'priority': priority,
            'timestamp': time(),
        })

    def select_consumer_for_delivery(self, queue):
        """Pick the consumer that should receive the next message.

        Single-active-consumer queues deliver only to the active consumer,
        and only while that consumer's channel can still consume. Other
        queues walk registrations from highest priority to lowest and pick
        the first consumer whose channel :meth:`QoS.can_consume` is true.
        """
        consumers = self.queue_consumers.get(queue) or []
        if not consumers:
            return None
        if queue in self.sac_queues:
            active_tag = self.active_consumers.get(queue)
            record = self.consumers.get(active_tag) if active_tag else None
            if record is not None and _channel_can_consume(record['channel']):
                return record
            return None
        for record in consumers:
            if _channel_can_consume(record['channel']):
                return record
        return None

    def iter_consumer_records(self, queue=None):
        if queue is not None:
            return list(self.queue_consumers.get(queue) or [])
        records = [
            record
            for consumers in self.queue_consumers.values()
            for record in consumers
        ]
        records.sort(key=lambda record: (-record['priority'], record['seq']))
        return records

    def consumer_is_active(self, queue, consumer_tag):
        consumers = self.queue_consumers.get(queue) or []
        if not consumers:
            return False
        if queue in self.sac_queues:
            return self.active_consumers.get(queue) == consumer_tag
        return consumers[0]['consumer_tag'] == consumer_tag

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
        # Consumers exist but none can take the message (SAC active is
        # blocked, or every priority level is at its prefetch limit).
        if (self.state.queue_consumers.get(queue)
                and self.state.select_consumer_for_delivery(queue) is None):
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
            self._new_queue(queue, **kwargs)
        arguments = kwargs.get('arguments') or {}
        if _is_sac_enabled(arguments.get('x-single-active-consumer')):
            # Once enabled, SAC stays enabled for the lifetime of the queue.
            # Redeclaring without the argument must not clear it.
            self.state.enable_single_active_consumer(queue)
        return queue_declare_ok_t(queue, self._size(queue), 0)

    def queue_delete(self, queue, if_unused=False, if_empty=False, **kwargs):
        """Delete queue."""
        if if_empty and self._size(queue):
            return
        self._drop_queue_consumers(queue)
        for exchange, routing_key, args in self.state.queue_bindings(queue):
            meta = self.typeof(exchange).prepare_bind(
                queue, exchange, routing_key, args,
            )
            self._delete(queue, exchange, *meta, **kwargs)
        self.state.queue_bindings_delete(queue)
        self.state.sac_queues.discard(queue)

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

        Consumer priority is read from ``x-priority`` in consumer
        ``arguments`` (default 0). Registrations are ordered highest
        priority first; equal priorities keep registration order.

        On a single-active-consumer queue only the first registration is
        active. A later registration with a strictly higher priority
        demotes the active consumer and invokes its ``on_cancel`` callback.
        """
        arguments = kwargs.get('arguments') or {}
        priority = _coerce_priority(arguments.get('x-priority', 0))
        on_cancel = kwargs.get('on_cancel')

        existing = self.state.consumers.get(consumer_tag)
        if existing is not None:
            owner = existing.get('channel')
            if owner is not None and consumer_tag in owner._consumers:
                owner.basic_cancel(consumer_tag)
            else:
                self.state.remove_consumer(consumer_tag)

        self._tag_to_queue[consumer_tag] = queue
        self._active_queues.append(queue)

        def _callback(raw_message, _callback=callback, _no_ack=no_ack):
            message = self.Message(raw_message, channel=self)
            if not _no_ack:
                self.qos.append(message, message.delivery_tag)
            if _callback is not None:
                return _callback(message)

        record = {
            'queue': queue,
            'consumer_tag': consumer_tag,
            'priority': priority,
            'channel': self,
            'on_cancel': on_cancel,
            'callback': _callback,
        }
        index = self.state.add_consumer(record)
        self._consumers.add(consumer_tag)
        self._install_queue_dispatcher(queue)
        self.state.record_consumer_event(
            'registered', queue, consumer_tag, priority)
        self._activate_registered_consumer(queue, record, index)
        self._reset_cycle()

    def basic_cancel(self, consumer_tag):
        """Cancel consumer by consumer tag.

        Invokes the consumer's ``on_cancel`` callback when one was
        registered. Exceptions from that callback are swallowed. On a
        single-active-consumer queue, cancelling the active consumer
        promotes the highest-priority standby.
        """
        if consumer_tag in self._consumers:
            self._consumers.remove(consumer_tag)
            self._reset_cycle()
            queue = self._tag_to_queue.pop(consumer_tag, None)
            try:
                self._active_queues.remove(queue)
            except ValueError:
                pass
            record = None
            if self.connection is not None:
                record = self.state.remove_consumer(consumer_tag)
            if record is not None:
                queue = record['queue']
                self.state.record_consumer_event(
                    'cancelled', queue, consumer_tag, record['priority'])
                if (queue in self.state.sac_queues
                        and queue not in self.state.active_consumers):
                    self._promote_next_consumer(queue)
                self._notify_on_cancel(record)
                self._sync_queue_callback(queue)
            elif self.connection is not None:
                self.connection._callbacks.pop(queue, None)

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
            for consumer in list(self._consumers):
                self.basic_cancel(consumer)
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
        """Publish a callback that selects the consumer at delivery time."""
        if self.connection is None:
            return
        state = self.state
        connection = self.connection

        def _dispatch(raw_message, _queue=queue, _state=state,
                      _connection=connection):
            record = _state.select_consumer_for_delivery(_queue)
            if record is None:
                _requeue_virtual_message(_connection, _queue, raw_message)
                return None
            return record['callback'](raw_message)

        connection._callbacks[queue] = _dispatch

    def _sync_queue_callback(self, queue):
        if self.connection is None:
            return
        if self.state.queue_consumers.get(queue):
            self._install_queue_dispatcher(queue)
        else:
            self.connection._callbacks.pop(queue, None)

    def _notify_on_cancel(self, record):
        callback = record.get('on_cancel') if record else None
        if callback is None:
            return
        try:
            callback(record['consumer_tag'])
        except Exception:
            pass

    def _activate_registered_consumer(self, queue, record, index):
        """Make a newly registered consumer active when the rules allow it."""
        tag = record['consumer_tag']
        priority = record['priority']
        if queue in self.state.sac_queues:
            current_tag = self.state.active_consumers.get(queue)
            current = (self.state.consumers.get(current_tag)
                       if current_tag and current_tag != tag else None)
            if current is None:
                self.state.active_consumers[queue] = tag
                self.state.record_consumer_event(
                    'activated', queue, tag, priority)
                return
            if priority > current['priority']:
                self.state.record_consumer_event(
                    'demoted', queue, current_tag, current['priority'])
                self.state.active_consumers[queue] = tag
                self.state.record_consumer_event(
                    'activated', queue, tag, priority)
                self._notify_on_cancel(current)
            return
        if index == 0:
            self.state.record_consumer_event(
                'activated', queue, tag, priority)

    def _promote_next_consumer(self, queue):
        """Promote the highest-priority remaining SAC consumer."""
        if queue not in self.state.sac_queues:
            return None
        remaining = self.state.queue_consumers.get(queue) or []
        if not remaining:
            self.state.active_consumers.pop(queue, None)
            return None
        record = remaining[0]
        self.state.active_consumers[queue] = record['consumer_tag']
        self.state.record_consumer_event(
            'promoted', queue, record['consumer_tag'], record['priority'])
        return record['consumer_tag']

    def _drop_queue_consumers(self, queue):
        """Cancel every consumer on `queue` before the queue is removed."""
        records = self.state.drop_queue_consumers(queue)
        for record in records:
            self.state.record_consumer_event(
                'cancelled', queue, record['consumer_tag'], record['priority'])
            self._notify_on_cancel(record)
            self._unlink_consumer(record)
        if self.connection is not None:
            self.connection._callbacks.pop(queue, None)

    def _unlink_consumer(self, record):
        """Detach a consumer from the channel that registered it."""
        channel = record.get('channel')
        tag = record['consumer_tag']
        if channel is None:
            return
        if tag in channel._consumers:
            channel._consumers.remove(tag)
        channel._tag_to_queue.pop(tag, None)
        try:
            channel._active_queues.remove(record['queue'])
        except ValueError:
            pass
        if channel is self or not getattr(channel, 'closed', False):
            channel._reset_cycle()
        connection = getattr(channel, 'connection', None)
        if connection is not None and not (
                self.state.queue_consumers.get(record['queue'])):
            connection._callbacks.pop(record['queue'], None)

    def _consumer_info_dict(self, record):
        return {
            'queue': record['queue'],
            'consumer_tag': record['consumer_tag'],
            'priority': record['priority'],
            'is_active': self.state.consumer_is_active(
                record['queue'], record['consumer_tag']),
        }

    def promote_consumer(self, queue, consumer_tag):
        """Promote `consumer_tag` to be the active consumer of a SAC queue.

        Returns
        -------
            bool: True when the consumer was promoted. False when the
                queue is not single-active-consumer, the tag is unknown,
                or the consumer is already active.
        """
        if queue not in self.state.sac_queues:
            return False
        record = self.state.consumers.get(consumer_tag)
        if record is None or record['queue'] != queue:
            return False
        current_tag = self.state.active_consumers.get(queue)
        if current_tag == consumer_tag:
            return False
        current = self.state.consumers.get(current_tag) if current_tag else None
        if current is not None:
            self.state.record_consumer_event(
                'demoted', queue, current_tag, current['priority'])
        self.state.active_consumers[queue] = consumer_tag
        self.state.record_consumer_event(
            'promoted', queue, consumer_tag, record['priority'])
        return True

    def consumer_info(self, queue=None):
        """Return consumer registrations ordered by priority.

        Each item has keys ``queue``, ``consumer_tag``, ``priority`` and
        ``is_active``. Highest priority comes first. Equal priorities keep
        registration order.
        """
        return [
            self._consumer_info_dict(record)
            for record in self.state.iter_consumer_records(queue)
        ]

    def get_consumer_count(self, queue=None):
        """Return the number of registered consumers."""
        if queue is None:
            return sum(
                len(consumers)
                for consumers in self.state.queue_consumers.values()
            )
        return len(self.state.queue_consumers.get(queue) or [])

    def get_active_consumer(self, queue):
        """Return the active consumer tag for `queue`.

        Single-active-consumer queues report the promoted consumer. Other
        queues report the highest-priority registration.
        """
        consumers = self.state.queue_consumers.get(queue) or []
        if not consumers:
            return None
        if queue in self.state.sac_queues:
            return self.state.active_consumers.get(queue)
        return consumers[0]['consumer_tag']

    def get_sac_status(self, queue):
        """Return single-active-consumer status for `queue`.

        Non-SAC queues return the same keys with ``None`` values.
        """
        if queue not in self.state.sac_queues:
            return {
                'queue': queue,
                'active': None,
                'standby': None,
                'consumer_count': None,
            }
        active = self.state.active_consumers.get(queue)
        standby = [
            record['consumer_tag']
            for record in self.state.queue_consumers.get(queue) or []
            if record['consumer_tag'] != active
        ]
        return {
            'queue': queue,
            'active': active,
            'standby': standby,
            'consumer_count': self.get_consumer_count(queue),
        }

    def get_standby_consumers(self, queue):
        """Return standby consumer tags for a single-active-consumer queue."""
        if queue not in self.state.sac_queues:
            return []
        active = self.state.active_consumers.get(queue)
        return [
            record['consumer_tag']
            for record in self.state.queue_consumers.get(queue) or []
            if record['consumer_tag'] != active
        ]

    def get_consumer_priority(self, consumer_tag):
        """Return the priority of `consumer_tag`, or ``None`` if unknown."""
        record = self.state.consumers.get(consumer_tag)
        if record is None:
            return None
        return record['priority']

    def is_single_active_consumer(self, queue):
        """Return true if `queue` was declared as single-active-consumer."""
        return self.state.is_single_active_consumer(queue)

    def list_consumers(self):
        """Return this channel's consumers, ordered by priority.

        Items use the same keys as :meth:`consumer_info`.
        """
        records = [
            record for record in self.state.iter_consumer_records()
            if record.get('channel') is self
        ]
        return [self._consumer_info_dict(record) for record in records]

    @property
    def consumer_tags(self):
        """This channel's consumer tags, sorted."""
        return sorted(self._consumers)

    def consumer_priority_map(self, queue):
        """Return a ``consumer_tag → priority`` map for `queue`."""
        return {
            record['consumer_tag']: record['priority']
            for record in self.state.queue_consumers.get(queue) or []
        }

    def consumer_registry_snapshot(self):
        """Return priority-ordered registrations keyed by queue.

        Each entry has keys ``consumer_tag``, ``priority`` and ``is_active``.
        """
        snapshot = {}
        for queue, consumers in self.state.queue_consumers.items():
            snapshot[queue] = [
                {
                    'consumer_tag': record['consumer_tag'],
                    'priority': record['priority'],
                    'is_active': self.state.consumer_is_active(
                        queue, record['consumer_tag']),
                }
                for record in consumers
            ]
        return snapshot

    def consumer_events(self, queue=None, event_type=None):
        """Return consumer lifecycle events.

        Event ``type`` is one of ``registered``, ``activated``, ``demoted``,
        ``cancelled`` and ``promoted``. Each event has keys ``type``,
        ``queue``, ``consumer_tag``, ``priority`` and ``timestamp``.
        """
        events = self.state.consumer_events
        if queue is not None:
            events = [event for event in events if event['queue'] == queue]
        if event_type is not None:
            events = [event for event in events if event['type'] == event_type]
        return [dict(event) for event in events]

    def clear_consumer_events(self):
        """Clear the consumer lifecycle event log."""
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
