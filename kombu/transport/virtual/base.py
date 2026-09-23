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

_QUEUE_ARGUMENT_TO_PROPERTY = {
    'x-dead-letter-exchange': 'dead_letter_exchange',
    'x-dead-letter-routing-key': 'dead_letter_routing_key',
    'x-message-ttl': 'message_ttl',
    'x-max-length': 'max_length',
    'x-max-length-bytes': 'max_length_bytes',
    'x-expires': 'expires',
    'x-max-priority': 'max_priority',
}
_PROPERTY_TO_QUEUE_ARGUMENT = {
    value: key for key, value in _QUEUE_ARGUMENT_TO_PROPERTY.items()
}


def _queue_argument_to_property(key):
    try:
        return _QUEUE_ARGUMENT_TO_PROPERTY[key]
    except KeyError:
        return key[2:].replace('-', '_')


def _property_to_queue_argument(key):
    try:
        return _PROPERTY_TO_QUEUE_ARGUMENT[key]
    except KeyError:
        return 'x-' + str(key).replace('_', '-')


def _has_per_message_expiration(properties):
    expiration = properties.get('expiration')
    if isinstance(expiration, bool) or not isinstance(
            expiration, (str, int, float)):
        return False
    return expiration != ''


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

    #: Per-queue declaration properties (DLX, TTL, max-length, ...), keyed
    #: by queue name.  Values use the short property names (for example
    #: ``dead_letter_exchange`` and ``message_ttl``).
    queue_properties = None

    def __init__(self, exchanges=None):
        self.exchanges = {} if exchanges is None else exchanges
        self.bindings = {}
        self.queue_index = defaultdict(set)
        self.queue_properties = {}

    def clear(self):
        self.exchanges.clear()
        self.bindings.clear()
        self.queue_index.clear()
        self.queue_properties.clear()

    def queue_properties_set(self, queue, **props):
        """Replace stored properties for ``queue``.

        A later call replaces the previous mapping; it does not merge.
        """
        self.queue_properties[queue] = dict(props)

    def queue_properties_get(self, queue):
        """Return a copy of ``queue`` properties, or an empty dict."""
        props = self.queue_properties.get(queue)
        if not props:
            return {}
        return dict(props)

    def queue_properties_delete(self, queue):
        """Remove stored properties for ``queue``."""
        self.queue_properties.pop(queue, None)

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
        self.queue_properties_delete(queue)

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
        """Remove from transactional state and requeue message.

        When ``requeue`` is false and the message was consumed from a queue
        with a dead letter exchange, the message is routed there with reason
        ``"rejected"``.
        """
        if requeue:
            self.channel._restore_at_beginning(self._delivered[delivery_tag])
        else:
            self._dead_letter_rejected(delivery_tag)
        self._quick_ack(delivery_tag)

    def redelivery_count(self, delivery_tag):
        """Return the total ``x-death`` count for ``delivery_tag``.

        Unknown tags, and messages that have never been dead-lettered,
        return ``0``.
        """
        try:
            message = self._delivered[delivery_tag]
        except KeyError:
            return 0
        return self.channel._death_total(message)

    def _dead_letter_rejected(self, delivery_tag):
        try:
            message = self._delivered[delivery_tag]
        except KeyError:
            return
        raw = getattr(message, '_raw', None)
        if not isinstance(raw, dict):
            raw = message if isinstance(message, dict) else None
        if raw is None:
            return
        queue = None
        delivery_info = getattr(message, 'delivery_info', None)
        if isinstance(delivery_info, dict):
            queue = delivery_info.get('queue')
        if not queue:
            properties = raw.get('properties')
            if isinstance(properties, dict):
                info = properties.get('delivery_info')
                if isinstance(info, dict):
                    queue = info.get('queue')
        if queue:
            self.channel.dead_letter(raw, queue, 'rejected')

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
        message = self._pop_ready(queue)
        self._stamp_delivery_queue(message, queue)
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

    #: Maximum cumulative ``x-death`` count before a dead-lettered message
    #: is discarded.  ``None`` disables the cap.
    dead_letter_max_hops = None

    # List of options to transfer from :attr:`transport_options`.
    from_transport_options = (
        'body_encoding', 'deadletter_queue', 'dead_letter_max_hops',
    )

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
            if not passive:
                arguments = kwargs.get('arguments') or {}
                self.state.queue_properties_set(
                    queue, **self._properties_from_arguments(arguments),
                )
        return queue_declare_ok_t(queue, self._size(queue), 0)

    def queue_delete(self, queue, if_unused=False, if_empty=False, **kwargs):
        """Delete queue."""
        if if_empty and self._size(queue):
            return
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
        return self.put(routing_key, message, **kwargs)

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
        """Consume from `queue`."""
        self._tag_to_queue[consumer_tag] = queue
        self._active_queues.append(queue)

        def _callback(raw_message):
            self._stamp_delivery_queue(raw_message, queue)
            message = self.Message(raw_message, channel=self)
            if not no_ack:
                self.qos.append(message, message.delivery_tag)
            return callback(message)

        self.connection._callbacks[queue] = _callback
        self._consumers.add(consumer_tag)

        self._reset_cycle()

    def basic_cancel(self, consumer_tag):
        """Cancel consumer by consumer tag."""
        if consumer_tag in self._consumers:
            self._consumers.remove(consumer_tag)
            self._reset_cycle()
            queue = self._tag_to_queue.pop(consumer_tag, None)
            try:
                self._active_queues.remove(queue)
            except ValueError:
                pass
            self.connection._callbacks.pop(queue, None)

    def basic_get(self, queue, no_ack=False, **kwargs):
        """Get message by direct access (synchronous).

        Expired messages are dead-lettered and skipped.  When every queued
        message is expired, :const:`None` is returned.
        """
        try:
            raw = self._pop_ready(queue)
        except Empty:
            return None
        self._stamp_delivery_queue(raw, queue)
        message = self.Message(raw, channel=self)
        if not no_ack:
            self.qos.append(message, message.delivery_tag)
        return message

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
        self._stamp_expiration(properties)

        return {'body': body,
                'content-encoding': content_encoding,
                'content-type': content_type,
                'headers': headers or {},
                'properties': properties or {}}

    def prepare_queue_arguments(self, arguments, **kwargs):
        """Convert queue option keywords into RabbitMQ ``x-*`` arguments.

        ``message_ttl`` and ``expires`` are seconds and are stored as
        integer milliseconds.  Dead-letter, length, and priority options
        are renamed to their ``x-*`` counterparts.
        """
        return base.to_rabbitmq_queue_arguments(arguments or {}, **kwargs)

    def get_queue_properties(self, queue):
        """Return the stored properties for ``queue`` (empty if unset)."""
        return self.state.queue_properties_get(queue)

    def queue_properties_for_declare(self, queue):
        """Rebuild ``x-*`` queue arguments from stored properties."""
        arguments = {}
        for key, value in self.get_queue_properties(queue).items():
            if value is None:
                continue
            arguments[_property_to_queue_argument(key)] = value
        return arguments

    def put(self, queue, message, **kwargs):
        """Enqueue ``message`` on ``queue``, enforcing TTL and max-length.

        Per-message ``expiration`` wins over the queue ``x-message-ttl``.
        Each destination gets its own copy, so queues with different TTLs
        record independent ``x-expires-at`` timestamps.  When
        ``x-max-length`` is set, the oldest messages are dead-lettered
        with reason ``"maxlen"`` before the new message is inserted.
        """
        if isinstance(message, dict):
            message = self._clone_message(message)
            self._apply_queue_message_ttl(queue, message)
        self._evict_overflow(queue)
        return self._put(queue, message, **kwargs)

    def message_ttl_remaining(self, message):
        """Return remaining TTL in seconds.

        :const:`None` when the message has no expiry.  Negative once the
        absolute ``x-expires-at`` timestamp is in the past.
        """
        properties = self._properties_of(message)
        expires_at = properties.get('x-expires-at')
        if expires_at is None:
            return None
        try:
            return float(expires_at) - time()
        except (TypeError, ValueError):
            return None

    def _is_expired(self, message, now=None):
        remaining = self.message_ttl_remaining(message)
        if remaining is None:
            return False
        if now is None:
            return remaining <= 0
        properties = self._properties_of(message)
        try:
            return float(properties['x-expires-at']) <= now
        except (TypeError, ValueError, KeyError):
            return False

    def drain_expired(self, queue):
        """Dead-letter expired messages and leave the rest in ``queue``.

        Returns
        -------
            int: Number of expired messages removed.
        """
        kept = []
        expired = 0
        while True:
            try:
                message = self._get(queue)
            except Empty:
                break
            if self._is_expired(message):
                self.dead_letter(message, queue, 'expired')
                expired += 1
            else:
                kept.append(message)
        for message in kept:
            self._put(queue, message)
        return expired

    def dead_letter(self, message, queue, reason):
        """Route ``message`` to the dead letter exchange configured for ``queue``.

        ``reason`` is ``"rejected"``, ``"expired"``, or ``"maxlen"``.
        Messages are discarded when the queue has no DLX, the exchange is
        not declared, the hop limit is exceeded, or cycle detection refuses
        every destination.  ``x-dead-letter-routing-key`` replaces the
        original routing key when it is set; otherwise the original routing
        key is preserved.  ``expiration`` and ``x-expires-at`` are cleared
        so the destination queue can apply its own TTL.
        """
        if not isinstance(message, dict):
            return
        props = self.get_queue_properties(queue)
        dlx = props.get('dead_letter_exchange')
        if not dlx or dlx not in self.state.exchanges:
            return
        if self._dead_letter_hop_limit_reached(message):
            return

        routed = self._clone_message(message)
        properties = routed.get('properties')
        if not isinstance(properties, dict):
            properties = {}
            routed['properties'] = properties
        delivery_info = properties.get('delivery_info')
        if not isinstance(delivery_info, dict):
            delivery_info = {}
            properties['delivery_info'] = delivery_info

        original_exchange = delivery_info.get('exchange')
        original_routing_key = delivery_info.get('routing_key')
        headers = routed.get('headers')
        if not isinstance(headers, dict):
            headers = {}
            routed['headers'] = headers
        self._record_death(
            headers, queue, reason, original_exchange, original_routing_key,
        )
        properties.pop('expiration', None)
        properties.pop('x-expires-at', None)

        if props.get('dead_letter_routing_key') is not None:
            routing_key = props['dead_letter_routing_key']
        elif original_routing_key is not None:
            routing_key = original_routing_key
        else:
            routing_key = ''
        delivery_info['exchange'] = dlx
        delivery_info['routing_key'] = routing_key

        try:
            table = self.get_table(dlx)
        except KeyError:
            return
        destinations = self.typeof(dlx).lookup(
            table, dlx, routing_key, None,
        )
        visited = self._visited_queues(routed)
        for dest in destinations:
            if not dest or dest in visited:
                continue
            self.put(dest, routed)

    def _pop_ready(self, queue):
        """Return the next unexpired message, dead-lettering expired ones."""
        while True:
            raw = self._get(queue)
            if self._is_expired(raw):
                self.dead_letter(raw, queue, 'expired')
                continue
            return raw

    def _stamp_delivery_queue(self, message, queue):
        if not isinstance(message, dict):
            return message
        properties = message.get('properties')
        if not isinstance(properties, dict):
            properties = {}
            message['properties'] = properties
        delivery_info = properties.get('delivery_info')
        if not isinstance(delivery_info, dict):
            delivery_info = {}
            properties['delivery_info'] = delivery_info
        delivery_info['queue'] = queue
        return message

    def _stamp_expiration(self, properties):
        expiration = properties.get('expiration')
        if isinstance(expiration, bool) or not isinstance(
                expiration, (str, int, float)):
            return
        if expiration == '':
            return
        try:
            properties['x-expires-at'] = time() + float(expiration) / 1000.0
        except (TypeError, ValueError):
            return

    def _apply_queue_message_ttl(self, queue, message):
        properties = message.get('properties')
        if not isinstance(properties, dict):
            properties = {}
            message['properties'] = properties
        if _has_per_message_expiration(properties):
            if properties.get('x-expires-at') is None:
                self._stamp_expiration(properties)
            return
        ttl_ms = self.get_queue_properties(queue).get('message_ttl')
        if ttl_ms is None:
            return
        try:
            ttl_ms = float(ttl_ms)
        except (TypeError, ValueError):
            return
        properties['expiration'] = str(int(ttl_ms))
        properties['x-expires-at'] = time() + ttl_ms / 1000.0

    def _evict_overflow(self, queue):
        props = self.get_queue_properties(queue)
        if props.get('max_length') is None:
            return
        try:
            max_length = int(props['max_length'])
        except (TypeError, ValueError):
            return
        guard = 0
        while self._size(queue) >= max_length:
            guard += 1
            if guard > 100000:  # pragma: no cover
                break
            try:
                evicted = self._get(queue)
            except Empty:
                break
            self.dead_letter(evicted, queue, 'maxlen')

    def _dead_letter_hop_limit_reached(self, message):
        limit = self.dead_letter_max_hops
        if limit is None:
            return False
        try:
            limit = int(limit)
        except (TypeError, ValueError):
            return False
        return self._death_total(message) >= limit

    def _death_total(self, message):
        total = 0
        for entry in self._iter_x_death(message):
            try:
                total += int(entry.get('count', 1))
            except (TypeError, ValueError):
                continue
        return total

    def _iter_x_death(self, message):
        headers = self._headers_of(message)
        deaths = headers.get('x-death')
        if not isinstance(deaths, list):
            return
        for entry in deaths:
            if isinstance(entry, dict):
                yield entry

    def _visited_queues(self, message):
        return {
            entry.get('queue') for entry in self._iter_x_death(message)
            if entry.get('queue')
        }

    def _record_death(self, headers, queue, reason, exchange, routing_key):
        now = time()
        if 'x-first-death-reason' not in headers:
            headers['x-first-death-reason'] = reason
            headers['x-first-death-queue'] = queue
            headers['x-first-death-exchange'] = exchange
        deaths = headers.get('x-death')
        if not isinstance(deaths, list):
            deaths = []
        else:
            deaths = [
                dict(entry) if isinstance(entry, dict) else entry
                for entry in deaths
            ]
        for entry in deaths:
            if (isinstance(entry, dict) and entry.get('queue') == queue and
                    entry.get('reason') == reason):
                entry['count'] = int(entry.get('count', 1)) + 1
                entry['time'] = now
                break
        else:
            deaths.append({
                'queue': queue,
                'reason': reason,
                'exchange': exchange,
                'routing-key': routing_key,
                'count': 1,
                'time': now,
            })
        headers['x-death'] = deaths

    def _clone_message(self, message):
        if not isinstance(message, dict):
            return message
        cloned = dict(message)
        headers = cloned.get('headers')
        if isinstance(headers, dict):
            headers = dict(headers)
            deaths = headers.get('x-death')
            if isinstance(deaths, list):
                headers['x-death'] = [
                    dict(entry) if isinstance(entry, dict) else entry
                    for entry in deaths
                ]
            cloned['headers'] = headers
        properties = cloned.get('properties')
        if isinstance(properties, dict):
            properties = dict(properties)
            delivery_info = properties.get('delivery_info')
            if isinstance(delivery_info, dict):
                properties['delivery_info'] = dict(delivery_info)
            cloned['properties'] = properties
        return cloned

    def _properties_of(self, message):
        if isinstance(message, dict):
            properties = message.get('properties')
        else:
            properties = getattr(message, 'properties', None)
        return properties if isinstance(properties, dict) else {}

    def _headers_of(self, message):
        if isinstance(message, dict):
            headers = message.get('headers')
        else:
            headers = getattr(message, 'headers', None)
        return headers if isinstance(headers, dict) else {}

    @staticmethod
    def _properties_from_arguments(arguments):
        properties = {}
        if not arguments:
            return properties
        for key, value in arguments.items():
            if isinstance(key, str) and key.startswith('x-'):
                properties[_queue_argument_to_property(key)] = value
        return properties

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
