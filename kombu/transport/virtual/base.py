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
    """Broker state holds exchanges, queues, bindings, and consumers."""

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

    def __init__(self, exchanges=None):
        self.exchanges = {} if exchanges is None else exchanges
        self.bindings = {}
        self.queue_index = defaultdict(set)
        # Queue names declared with ``x-single-active-consumer``.
        # Sticky: redeclare without the argument does not remove membership.
        self.sac_queues = set()
        # queue -> [consumer record, ...], highest priority first.
        # Equal priorities keep registration order.
        self.consumers = {}
        # consumer_tag -> consumer record
        self.consumers_by_tag = {}
        # queue -> consumer_tag of the SAC active consumer
        self.active_consumers = {}
        # Lifecycle events shared by every channel on this broker.
        self.consumer_events = []
        self.consumer_seq = count(1)

    def clear(self):
        self.exchanges.clear()
        self.bindings.clear()
        self.queue_index.clear()
        self.sac_queues.clear()
        self.clear_consumer_state()

    def clear_consumer_state(self):
        """Drop consumer registrations, active tags, and lifecycle events."""
        self.consumers.clear()
        self.consumers_by_tag.clear()
        self.active_consumers.clear()
        self.consumer_events.clear()

    def add_consumer(self, record):
        """Insert `record`, keeping highest priority first.

        Equal priorities keep registration order (the newcomer goes after
        existing consumers with the same priority).
        """
        queue = record['queue']
        consumers = self.consumers.setdefault(queue, [])
        priority = record['priority']
        index = len(consumers)
        for i, existing in enumerate(consumers):
            if existing['priority'] < priority:
                index = i
                break
        consumers.insert(index, record)
        self.consumers_by_tag[record['consumer_tag']] = record
        return record

    def remove_consumer(self, consumer_tag):
        """Remove a consumer record by tag. Returns the record or None."""
        record = self.consumers_by_tag.pop(consumer_tag, None)
        if record is None:
            return None
        queue = record['queue']
        remaining = [
            consumer for consumer in self.consumers.get(queue, [])
            if consumer['consumer_tag'] != consumer_tag
        ]
        if remaining:
            self.consumers[queue] = remaining
        else:
            self.consumers.pop(queue, None)
        if self.active_consumers.get(queue) == consumer_tag:
            self.active_consumers.pop(queue, None)
        return record

    def activate_highest(self, queue):
        """Make the highest-priority remaining consumer active on `queue`."""
        consumers = self.consumers.get(queue) or []
        if not consumers:
            self.active_consumers.pop(queue, None)
            return None
        chosen = consumers[0]
        self.active_consumers[queue] = chosen['consumer_tag']
        return chosen

    def consumer_is_active(self, record):
        """Return True when `record` is the active consumer for its queue."""
        queue = record['queue']
        if queue in self.sac_queues:
            return self.active_consumers.get(queue) == record['consumer_tag']
        consumers = self.consumers.get(queue) or []
        return bool(consumers) and (
            consumers[0]['consumer_tag'] == record['consumer_tag']
        )

    def iter_consumers(self, queue=None):
        """Return consumer records ordered by priority, then registration."""
        if queue is not None:
            return list(self.consumers.get(queue, []))
        records = [
            consumer
            for consumers in self.consumers.values()
            for consumer in consumers
        ]
        records.sort(key=lambda consumer: (
            -consumer['priority'], consumer['seq'],
        ))
        return records

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
        message = self._get(queue)
        callback(message, queue)


def _consumer_priority(arguments):
    """Return ``x-priority`` from consumer arguments, defaulting to 0."""
    if not isinstance(arguments, dict):
        return 0
    value = arguments.get('x-priority', 0)
    if value is None:
        return 0
    try:
        return int(value)
    except (TypeError, ValueError):
        return 0


def _consumer_channel_can_consume(record):
    """Return True when the consumer's channel may still receive a message."""
    channel = record.get('channel')
    if channel is None or getattr(channel, 'closed', False):
        return False
    return bool(channel.qos.can_consume())


def _select_consumer_for_delivery(state, queue):
    """Pick the consumer that should receive the next message on `queue`.

    Single-active-consumer queues always deliver to the active consumer.
    Other queues walk priority order and pick the first consumer whose
    channel can still consume. If every channel is at its prefetch limit,
    the highest-priority open consumer is used so the message is not dropped.
    """
    consumers = list(state.consumers.get(queue) or [])
    if not consumers:
        return None
    if queue in state.sac_queues:
        active = state.active_consumers.get(queue)
        for record in consumers:
            if record['consumer_tag'] == active:
                return record
        return None
    fallback = None
    for record in consumers:
        channel = record.get('channel')
        if channel is None or getattr(channel, 'closed', False):
            continue
        if fallback is None:
            fallback = record
        if _consumer_channel_can_consume(record):
            return record
    return fallback


def _channel_should_poll(state, channel, queue):
    """Return True when `channel` should dequeue messages from `queue`."""
    mine = [
        record for record in state.consumers.get(queue, [])
        if record['channel'] is channel
    ]
    if not mine:
        return False
    if queue in state.sac_queues:
        active = state.active_consumers.get(queue)
        return any(record['consumer_tag'] == active for record in mine)
    return True


def _requeue_unclaimed(state, queue, raw_message):
    """Put `raw_message` back when no consumer can take it."""
    for record in list(state.consumers.get(queue) or []):
        channel = record.get('channel')
        if channel is None or getattr(channel, 'closed', False):
            continue
        try:
            channel._put(queue, raw_message)
        except NotImplementedError:
            continue
        else:
            return


def _make_queue_dispatcher(state, queue):
    """Build a callback that routes to the consumer chosen at delivery time."""

    def _dispatch(raw_message):
        record = _select_consumer_for_delivery(state, queue)
        if record is None:
            _requeue_unclaimed(state, queue, raw_message)
            raise Empty()
        channel = record['channel']
        message = channel.Message(raw_message, channel=channel)
        if not record['no_ack']:
            channel.qos.append(message, message.delivery_tag)
        return record['callback'](message)

    return _dispatch


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
            self._remember_single_active_consumer(
                queue, kwargs.get('arguments'),
            )
        return queue_declare_ok_t(queue, self._size(queue), 0)

    def queue_delete(self, queue, if_unused=False, if_empty=False, **kwargs):
        """Delete queue."""
        if if_empty and self._size(queue):
            return
        if self.connection is not None:
            self._cancel_consumers_for_deleted_queue(queue)
        for exchange, routing_key, args in self.state.queue_bindings(queue):
            meta = self.typeof(exchange).prepare_bind(
                queue, exchange, routing_key, args,
            )
            self._delete(queue, exchange, *meta, **kwargs)
        self.state.queue_bindings_delete(queue)
        if self.connection is not None:
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
        ``arguments`` (default 0). Consumers are ordered highest priority
        first; equal priorities keep registration order. On a
        single-active-consumer queue only the active consumer receives
        messages. ``on_cancel`` is invoked with the consumer tag when this
        consumer is cancelled or demoted.
        """
        priority = _consumer_priority(kwargs.get('arguments'))
        on_cancel = kwargs.get('on_cancel')
        self._tag_to_queue[consumer_tag] = queue
        self._consumers.add(consumer_tag)

        state = self.state
        if consumer_tag in state.consumers_by_tag:
            state.remove_consumer(consumer_tag)

        record = {
            'queue': queue,
            'consumer_tag': consumer_tag,
            'priority': priority,
            'channel': self,
            'callback': callback,
            'on_cancel': on_cancel,
            'no_ack': no_ack,
            'seq': next(state.consumer_seq),
        }
        state.add_consumer(record)
        self._emit_consumer_event('registered', record)
        self._activate_registered_consumer(record)
        self._sync_queue_consumers(queue)
        self._reset_cycle()

    def basic_cancel(self, consumer_tag):
        """Cancel consumer by consumer tag."""
        if consumer_tag not in self._consumers:
            return None
        self._consumers.remove(consumer_tag)
        queue = self._tag_to_queue.pop(consumer_tag, None)
        state = self.state if self.connection is not None else None
        record = None
        if state is not None and queue is not None:
            record = state.consumers_by_tag.get(consumer_tag)
        if record is None:
            try:
                self._active_queues.remove(queue)
            except ValueError:
                pass
            if self.connection is not None and queue is not None:
                self.connection._callbacks.pop(queue, None)
            self._reset_cycle()
            return None

        self._safe_notify_cancel(record)
        self._emit_consumer_event('cancelled', record)
        if consumer_tag not in state.consumers_by_tag:
            self._sync_queue_consumers(queue)
            self._reset_cycle()
            return None

        was_active = state.consumer_is_active(record)
        queue = record['queue']
        state.remove_consumer(consumer_tag)
        if was_active and queue in state.sac_queues:
            promoted = state.activate_highest(queue)
            if promoted is not None:
                self._emit_consumer_event('promoted', promoted)
        self._sync_queue_consumers(queue)
        self._reset_cycle()
        return None

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

    def __enter__(self):
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc_val: BaseException | None,
        exc_tb: TracebackType | None
    ) -> None:
        self.close()

    def promote_consumer(self, queue, consumer_tag):
        """Promote `consumer_tag` to active on a single-active-consumer queue.

        Returns True when promotion happened. Returns False when the queue
        is not single-active-consumer, the tag is unknown, or it is already
        the active consumer.
        """
        state = self.state
        if queue not in state.sac_queues:
            return False
        record = state.consumers_by_tag.get(consumer_tag)
        if record is None or record['queue'] != queue:
            return False
        current = state.active_consumers.get(queue)
        if current == consumer_tag:
            return False
        if current is not None:
            previous = state.consumers_by_tag.get(current)
            if previous is not None:
                self._emit_consumer_event('demoted', previous)
        state.active_consumers[queue] = consumer_tag
        self._emit_consumer_event('promoted', record)
        self._sync_queue_consumers(queue)
        self._reset_cycle()
        return True

    def consumer_info(self, queue=None):
        """Return consumer dicts ordered by priority (highest first)."""
        return [
            self._consumer_info_dict(record)
            for record in self.state.iter_consumers(queue)
        ]

    def get_consumer_count(self, queue=None):
        """Return the number of registered consumers."""
        if queue is None:
            return len(self.state.consumers_by_tag)
        return len(self.state.consumers.get(queue, []))

    def get_active_consumer(self, queue):
        """Return the active consumer tag for `queue`.

        On a non-SAC queue the highest-priority consumer is considered
        active. Returns None when the queue has no consumers.
        """
        state = self.state
        if queue in state.sac_queues:
            return state.active_consumers.get(queue)
        consumers = state.consumers.get(queue) or []
        if not consumers:
            return None
        return consumers[0]['consumer_tag']

    def get_sac_status(self, queue):
        """Return SAC status for `queue`, or None when it is not SAC."""
        if queue not in self.state.sac_queues:
            return None
        consumers = self.state.consumers.get(queue, [])
        active = self.state.active_consumers.get(queue)
        standby = [
            record['consumer_tag'] for record in consumers
            if record['consumer_tag'] != active
        ]
        return {
            'queue': queue,
            'active': active,
            'standby': standby,
            'consumer_count': len(consumers),
        }

    def get_standby_consumers(self, queue):
        """Return consumer tags that are not the active consumer."""
        return [
            record['consumer_tag']
            for record in self.state.consumers.get(queue, [])
            if not self.state.consumer_is_active(record)
        ]

    def get_consumer_priority(self, consumer_tag):
        """Return the priority of `consumer_tag`, or None if unknown."""
        record = self.state.consumers_by_tag.get(consumer_tag)
        if record is None:
            return None
        return record['priority']

    def is_single_active_consumer(self, queue):
        """Return True if `queue` was declared as single-active-consumer."""
        return queue in self.state.sac_queues

    def list_consumers(self):
        """Return consumer dicts registered on this channel."""
        return [
            self._consumer_info_dict(record)
            for record in self.state.iter_consumers()
            if record['channel'] is self
        ]

    @property
    def consumer_tags(self):
        """Sorted consumer tags registered on this channel."""
        return sorted(self._consumers, key=str)

    def consumer_priority_map(self, queue):
        """Return a ``{consumer_tag: priority}`` map for `queue`."""
        return {
            record['consumer_tag']: record['priority']
            for record in self.state.consumers.get(queue, [])
        }

    def consumer_registry_snapshot(self):
        """Return the shared consumer registry keyed by queue."""
        snapshot = {}
        for queue, consumers in self.state.consumers.items():
            snapshot[queue] = [
                {
                    'consumer_tag': record['consumer_tag'],
                    'priority': record['priority'],
                    'is_active': self.state.consumer_is_active(record),
                }
                for record in consumers
            ]
        return snapshot

    def consumer_events(self, queue=None, event_type=None):
        """Return consumer lifecycle events, optionally filtered."""
        events = self.state.consumer_events
        if queue is not None:
            events = [event for event in events if event['queue'] == queue]
        if event_type is not None:
            events = [event for event in events if event['type'] == event_type]
        return [dict(event) for event in events]

    def clear_consumer_events(self):
        """Clear the shared consumer lifecycle log."""
        self.state.consumer_events.clear()

    def _remember_single_active_consumer(self, queue, arguments):
        if isinstance(arguments, dict) and arguments.get(
                'x-single-active-consumer'):
            self.state.sac_queues.add(queue)

    def _emit_consumer_event(self, event_type, record):
        self.state.consumer_events.append({
            'type': event_type,
            'queue': record['queue'],
            'consumer_tag': record['consumer_tag'],
            'priority': record['priority'],
            'timestamp': time(),
        })

    def _safe_notify_cancel(self, record):
        callback = record.get('on_cancel')
        if callback is None:
            return
        try:
            callback(record['consumer_tag'])
        except Exception:
            pass

    def _consumer_info_dict(self, record):
        return {
            'queue': record['queue'],
            'consumer_tag': record['consumer_tag'],
            'priority': record['priority'],
            'is_active': self.state.consumer_is_active(record),
        }

    def _activate_registered_consumer(self, record):
        state = self.state
        queue = record['queue']
        tag = record['consumer_tag']
        if queue not in state.sac_queues:
            consumers = state.consumers.get(queue) or []
            if consumers and consumers[0]['consumer_tag'] == tag:
                self._emit_consumer_event('activated', record)
            return

        current_tag = state.active_consumers.get(queue)
        current = (
            state.consumers_by_tag.get(current_tag) if current_tag else None
        )
        if current is None or current_tag == tag:
            state.active_consumers[queue] = tag
            self._emit_consumer_event('activated', record)
            return
        if record['priority'] > current['priority']:
            state.active_consumers[queue] = tag
            self._emit_consumer_event('demoted', current)
            self._emit_consumer_event('activated', record)
            self._safe_notify_cancel(current)

    def _cancel_consumers_for_deleted_queue(self, queue):
        """Notify and drop every consumer on `queue` before it is removed."""
        state = self.state
        records = list(state.consumers.get(queue, []))
        channels = []
        for record in records:
            channel = record['channel']
            tag = record['consumer_tag']
            if channel is None:
                continue
            channels.append(channel)
            if tag in channel._consumers:
                channel._consumers.remove(tag)
            channel._tag_to_queue.pop(tag, None)
        for record in records:
            self._safe_notify_cancel(record)
        for record in records:
            tag = record['consumer_tag']
            if tag in state.consumers_by_tag and (
                    state.consumers_by_tag[tag] is record):
                self._emit_consumer_event('cancelled', record)
                state.remove_consumer(tag)
        active = state.active_consumers.get(queue)
        live_tags = {
            consumer['consumer_tag']
            for consumer in state.consumers.get(queue, [])
        }
        if active not in live_tags:
            state.active_consumers.pop(queue, None)
        self._sync_queue_consumers(queue, extra_channels=channels)
        for channel in channels:
            if channel is not self and not getattr(channel, 'closed', False):
                channel._reset_cycle()
        self._reset_cycle()

    def _sync_queue_consumers(self, queue, extra_channels=None):
        """Align polling lists and delivery callbacks with broker state."""
        state = self.state
        channels = {id(self): self}
        if extra_channels:
            for channel in extra_channels:
                if channel is not None:
                    channels[id(channel)] = channel
        for record in state.consumers.get(queue, []):
            channel = record['channel']
            if channel is not None:
                channels[id(channel)] = channel

        for channel in channels.values():
            self._set_channel_polling(channel, queue)

        connections = {}
        for channel in channels.values():
            connection = getattr(channel, 'connection', None)
            if connection is not None:
                connections[id(connection)] = connection
        live_connection_ids = set()
        for record in state.consumers.get(queue, []):
            channel = record['channel']
            connection = None if channel is None else channel.connection
            if connection is not None:
                connections[id(connection)] = connection
                live_connection_ids.add(id(connection))

        callback = _make_queue_dispatcher(state, queue)
        for cid, connection in connections.items():
            if cid in live_connection_ids:
                connection._callbacks[queue] = callback
            else:
                connection._callbacks.pop(queue, None)

        for channel in channels.values():
            if channel is not self and not getattr(channel, 'closed', False):
                channel._reset_cycle()

    def _set_channel_polling(self, channel, queue):
        should = _channel_should_poll(self.state, channel, queue)
        active = channel._active_queues
        if should:
            if queue not in active:
                active.append(queue)
        else:
            while True:
                try:
                    active.remove(queue)
                except ValueError:
                    break

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
