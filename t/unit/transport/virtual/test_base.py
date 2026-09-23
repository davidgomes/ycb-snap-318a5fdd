from __future__ import annotations

import io
import socket
import warnings
from array import array
from time import monotonic, time
from unittest.mock import MagicMock, Mock, patch

import pytest

from kombu import Connection
from kombu.compression import compress
from kombu.exceptions import ChannelError, ResourceError
from kombu.transport import virtual
from kombu.utils.uuid import uuid

PRINT_FQDN = 'builtins.print'


def client(**kwargs):
    return Connection(transport='kombu.transport.virtual:Transport', **kwargs)


def memory_client():
    return Connection(transport='memory')


def test_BrokerState():
    s = virtual.BrokerState()
    assert hasattr(s, 'exchanges')

    t = virtual.BrokerState(exchanges=16)
    assert t.exchanges == 16


class test_QoS:

    def setup_method(self):
        self.q = virtual.QoS(client().channel(), prefetch_count=10)

    def teardown_method(self):
        self.q._on_collect.cancel()

    def test_constructor(self):
        assert self.q.channel
        assert self.q.prefetch_count
        assert not self.q._delivered.restored
        assert self.q._on_collect

    def test_restore_visible__interface(self):
        qos = virtual.QoS(client().channel())
        qos.restore_visible()

    def test_can_consume(self, stdouts):
        stderr = io.StringIO()
        _restored = []

        class RestoreChannel(virtual.Channel):
            do_restore = True

            def _restore(self, message):
                _restored.append(message)

        assert self.q.can_consume()
        for i in range(self.q.prefetch_count - 1):
            self.q.append(i, uuid())
            assert self.q.can_consume()
        self.q.append(i + 1, uuid())
        assert not self.q.can_consume()

        tag1 = next(iter(self.q._delivered))
        self.q.ack(tag1)
        assert self.q.can_consume()

        tag2 = uuid()
        self.q.append(i + 2, tag2)
        assert not self.q.can_consume()
        self.q.reject(tag2)
        assert self.q.can_consume()

        self.q.channel = RestoreChannel(self.q.channel.connection)
        tag3 = uuid()
        self.q.append(i + 3, tag3)
        self.q.reject(tag3, requeue=True)
        self.q._flush()
        assert self.q._delivered
        assert not self.q._delivered.restored
        self.q.restore_unacked_once(stderr=stderr)
        assert _restored == [11, 9, 8, 7, 6, 5, 4, 3, 2, 1]
        assert self.q._delivered.restored
        assert not self.q._delivered

        self.q.restore_unacked_once(stderr=stderr)
        self.q._delivered.restored = False
        self.q.restore_unacked_once(stderr=stderr)

        assert stderr.getvalue()
        assert not stdouts.stdout.getvalue()

        self.q.restore_at_shutdown = False
        self.q.restore_unacked_once()

    def test_get(self):
        self.q._delivered['foo'] = 1
        assert self.q.get('foo') == 1


class test_Message:

    def test_create(self):
        c = client().channel()
        data = c.prepare_message('the quick brown fox...')
        tag = data['properties']['delivery_tag'] = uuid()
        message = c.message_to_python(data)
        assert isinstance(message, virtual.Message)
        assert message is c.message_to_python(message)
        if message.errors:
            message._reraise_error()

        assert message.body == b'the quick brown fox...'
        assert message.delivery_tag, tag

    def test_create_no_body(self):
        virtual.Message(channel=Mock(), payload={
            'body': None,
            'properties': {'delivery_tag': 1},
        })

    def test_serializable(self):
        c = client().channel()
        body, content_type = compress('the quick brown fox...', 'gzip')
        data = c.prepare_message(body, headers={'compression': content_type})
        tag = data['properties']['delivery_tag'] = uuid()
        message = c.message_to_python(data)
        dict_ = message.serializable()
        assert dict_['body'] == b'the quick brown fox...'
        assert dict_['properties']['delivery_tag'] == tag
        assert 'compression' not in dict_['headers']


class test_AbstractChannel:

    def test_get(self):
        with pytest.raises(NotImplementedError):
            virtual.AbstractChannel()._get('queue')

    def test_put(self):
        with pytest.raises(NotImplementedError):
            virtual.AbstractChannel()._put('queue', 'm')

    def test_size(self):
        assert virtual.AbstractChannel()._size('queue') == 0

    def test_purge(self):
        with pytest.raises(NotImplementedError):
            virtual.AbstractChannel()._purge('queue')

    def test_delete(self):
        with pytest.raises(NotImplementedError):
            virtual.AbstractChannel()._delete('queue')

    def test_new_queue(self):
        assert virtual.AbstractChannel()._new_queue('queue') is None

    def test_has_queue(self):
        assert virtual.AbstractChannel()._has_queue('queue')

    def test_poll(self):
        cycle = Mock(name='cycle')
        assert virtual.AbstractChannel()._poll(cycle, Mock())
        cycle.get.assert_called()


class test_Channel:

    def setup_method(self):
        self.channel = client().channel()

    def teardown_method(self):
        if self.channel._qos is not None:
            self.channel._qos._on_collect.cancel()

    def test_get_free_channel_id(self):
        conn = client()
        channel = conn.channel()
        assert channel.channel_id == 1
        assert channel._get_free_channel_id() == 2

    def test_get_free_channel_id__exceeds_channel_max(self):
        conn = client()
        conn.transport.channel_max = 2
        channel = conn.channel()
        channel._get_free_channel_id()
        with pytest.raises(ResourceError):
            channel._get_free_channel_id()

    def test_exchange_bind_interface(self):
        with pytest.raises(NotImplementedError):
            self.channel.exchange_bind('dest', 'src', 'key')

    def test_exchange_unbind_interface(self):
        with pytest.raises(NotImplementedError):
            self.channel.exchange_unbind('dest', 'src', 'key')

    def test_queue_unbind_interface(self):
        self.channel.queue_unbind('dest', 'ex', 'key')

    def test_management(self):
        m = self.channel.connection.client.get_manager()
        assert m
        m.get_bindings()
        m.close()

    def test_exchange_declare(self):
        c = self.channel

        with pytest.raises(ChannelError):
            c.exchange_declare('test_exchange_declare', 'direct',
                               durable=True, auto_delete=True, passive=True)
        c.exchange_declare('test_exchange_declare', 'direct',
                           durable=True, auto_delete=True)
        c.exchange_declare('test_exchange_declare', 'direct',
                           durable=True, auto_delete=True, passive=True)
        assert 'test_exchange_declare' in c.state.exchanges
        # can declare again with same values
        c.exchange_declare('test_exchange_declare', 'direct',
                           durable=True, auto_delete=True)
        assert 'test_exchange_declare' in c.state.exchanges

        # using different values raises NotEquivalentError
        with pytest.raises(virtual.NotEquivalentError):
            c.exchange_declare('test_exchange_declare', 'direct',
                               durable=False, auto_delete=True)

    def test_exchange_delete(self, ex='test_exchange_delete'):

        class PurgeChannel(virtual.Channel):
            purged = []

            def _purge(self, queue):
                self.purged.append(queue)

        c = PurgeChannel(self.channel.connection)

        c.exchange_declare(ex, 'direct', durable=True, auto_delete=True)
        assert ex in c.state.exchanges
        assert not c.state.has_binding(ex, ex, ex)  # no bindings yet
        c.exchange_delete(ex)
        assert ex not in c.state.exchanges

        c.exchange_declare(ex, 'direct', durable=True, auto_delete=True)
        c.queue_declare(ex)
        c.queue_bind(ex, ex, ex)
        assert c.state.has_binding(ex, ex, ex)
        c.exchange_delete(ex)
        assert not c.state.has_binding(ex, ex, ex)
        assert ex in c.purged

    def test_queue_delete__if_empty(self, n='test_queue_delete__if_empty'):
        class PurgeChannel(virtual.Channel):
            purged = []
            size = 30

            def _purge(self, queue):
                self.purged.append(queue)

            def _size(self, queue):
                return self.size

        c = PurgeChannel(self.channel.connection)
        c.exchange_declare(n)
        c.queue_declare(n)
        c.queue_bind(n, n, n)
        # tests code path that returns if queue already bound.
        c.queue_bind(n, n, n)

        c.queue_delete(n, if_empty=True)
        assert c.state.has_binding(n, n, n)

        c.size = 0
        c.queue_delete(n, if_empty=True)
        assert not c.state.has_binding(n, n, n)
        assert n in c.purged

    def test_queue_purge(self, n='test_queue_purge'):

        class PurgeChannel(virtual.Channel):
            purged = []

            def _purge(self, queue):
                self.purged.append(queue)

        c = PurgeChannel(self.channel.connection)
        c.exchange_declare(n)
        c.queue_declare(n)
        c.queue_bind(n, n, n)
        c.queue_purge(n)
        assert n in c.purged

    def test_basic_publish__anon_exchange(self):
        c = memory_client().channel()
        msg = MagicMock(name='msg')
        c.encode_body = Mock(name='c.encode_body')
        c.encode_body.return_value = (1, 2)
        c._put = Mock(name='c._put')
        c.basic_publish(msg, None, 'rkey', kw=1)
        c._put.assert_called_with('rkey', msg, kw=1)

    def test_basic_publish_unique_delivery_tags(self, n='test_uniq_tag'):
        c1 = memory_client().channel()
        c2 = memory_client().channel()

        for c in (c1, c2):
            c.exchange_declare(n)
            c.queue_declare(n)
            c.queue_bind(n, n, n)
        m1 = c1.prepare_message('George Costanza')
        m2 = c2.prepare_message('Elaine Marie Benes')
        c1.basic_publish(m1, n, n)
        c2.basic_publish(m2, n, n)

        r1 = c1.message_to_python(c1.basic_get(n))
        r2 = c2.message_to_python(c2.basic_get(n))

        assert r1.delivery_tag != r2.delivery_tag
        with pytest.raises(ValueError):
            int(r1.delivery_tag)
        with pytest.raises(ValueError):
            int(r2.delivery_tag)

    def test_basic_publish__get__consume__restore(self,
                                                  n='test_basic_publish'):
        c = memory_client().channel()

        c.exchange_declare(n)
        c.queue_declare(n)
        c.queue_bind(n, n, n)
        c.queue_declare(n + '2')
        c.queue_bind(n + '2', n, n)
        messages = []
        c.connection._deliver = Mock(name='_deliver')

        def on_deliver(message, queue):
            messages.append(message)
        c.connection._deliver.side_effect = on_deliver

        m = c.prepare_message('nthex quick brown fox...')
        c.basic_publish(m, n, n)

        r1 = c.message_to_python(c.basic_get(n))
        assert r1
        assert r1.body == b'nthex quick brown fox...'
        assert c.basic_get(n) is None

        consumer_tag = uuid()

        c.basic_consume(n + '2', False,
                        consumer_tag=consumer_tag, callback=lambda *a: None)
        assert n + '2' in c._active_queues
        c.drain_events()
        r2 = c.message_to_python(messages[-1])
        assert r2.body == b'nthex quick brown fox...'
        assert r2.delivery_info['exchange'] == n
        assert r2.delivery_info['routing_key'] == n
        with pytest.raises(virtual.Empty):
            c.drain_events()
        c.basic_cancel(consumer_tag)

        c._restore(r2)
        r3 = c.message_to_python(c.basic_get(n))
        assert r3
        assert r3.body == b'nthex quick brown fox...'
        assert c.basic_get(n) is None

    def test_basic_ack(self):

        class MockQoS(virtual.QoS):
            was_acked = False

            def ack(self, delivery_tag):
                self.was_acked = True

        self.channel._qos = MockQoS(self.channel)
        self.channel.basic_ack('foo')
        assert self.channel._qos.was_acked

    def test_basic_recover__requeue(self):

        class MockQoS(virtual.QoS):
            was_restored = False

            def restore_unacked(self):
                self.was_restored = True

        self.channel._qos = MockQoS(self.channel)
        self.channel.basic_recover(requeue=True)
        assert self.channel._qos.was_restored

    def test_restore_unacked_raises_BaseException(self):
        q = self.channel.qos
        q._flush = Mock()
        q._delivered = {1: 1}

        q.channel._restore = Mock()
        q.channel._restore.side_effect = SystemExit

        errors = q.restore_unacked()
        assert isinstance(errors[0][0], SystemExit)
        assert errors[0][1] == 1
        assert not q._delivered

    @patch('kombu.transport.virtual.base.emergency_dump_state')
    @patch(PRINT_FQDN)
    def test_restore_unacked_once_when_unrestored(self, print_,
                                                  emergency_dump_state):
        q = self.channel.qos
        q._flush = Mock()

        class State(dict):
            restored = False

        q._delivered = State({1: 1})
        ru = q.restore_unacked = Mock()
        exc = None
        try:
            raise KeyError()
        except KeyError as exc_:
            exc = exc_
        ru.return_value = [(exc, 1)]

        self.channel.do_restore = True
        q.restore_unacked_once()
        print_.assert_called()
        emergency_dump_state.assert_called()

    def test_basic_recover(self):
        with pytest.raises(NotImplementedError):
            self.channel.basic_recover(requeue=False)

    def test_basic_reject(self):

        class MockQoS(virtual.QoS):
            was_rejected = False

            def reject(self, delivery_tag, requeue=False):
                self.was_rejected = True

        self.channel._qos = MockQoS(self.channel)
        self.channel.basic_reject('foo')
        assert self.channel._qos.was_rejected

    def test_basic_qos(self):
        self.channel.basic_qos(prefetch_count=128)
        assert self.channel._qos.prefetch_count == 128

    def test_lookup__undeliverable(self, n='test_lookup__undeliverable'):
        warnings.resetwarnings()
        with warnings.catch_warnings(record=True) as log:
            assert self.channel._lookup(n, n, 'ae.undeliver') == [
                'ae.undeliver',
            ]
            assert log
            assert 'could not be delivered' in log[0].message.args[0]

    def test_context(self):
        with self.channel as x:
            assert x is self.channel
        assert x.closed

    def test_cycle_property(self):
        assert self.channel.cycle

    def test_flow(self):
        with pytest.raises(NotImplementedError):
            self.channel.flow(False)

    def test_close_when_no_connection(self):
        self.channel.connection = None
        self.channel.close()
        assert self.channel.closed

    def test_drain_events_has_get_many(self):
        c = self.channel
        c._get_many = Mock()
        c._poll = Mock()
        c._consumers = [1]
        c._qos = Mock()
        c._qos.can_consume.return_value = True

        c.drain_events(timeout=10.0)
        c._get_many.assert_called_with(c._active_queues, timeout=10.0)

    def test_get_exchanges(self):
        self.channel.exchange_declare(exchange='unique_name')
        assert self.channel.get_exchanges()

    def test_basic_cancel_not_in_active_queues(self):
        c = self.channel
        c._consumers.add('x')
        c._tag_to_queue['x'] = 'foo'
        c._active_queues = Mock()
        c._active_queues.remove.side_effect = ValueError()

        c.basic_cancel('x')
        c._active_queues.remove.assert_called_with('foo')

    def test_basic_cancel_unknown_ctag(self):
        assert self.channel.basic_cancel('unknown-tag') is None

    def test_list_bindings(self):
        c = self.channel
        c.exchange_declare(exchange='unique_name')
        c.queue_declare(queue='q')
        c.queue_bind(queue='q', exchange='unique_name', routing_key='rk')

        assert ('q', 'unique_name', 'rk') in list(c.list_bindings())

    def test_after_reply_message_received(self):
        c = self.channel
        c.queue_delete = Mock()
        c.after_reply_message_received('foo')
        c.queue_delete.assert_called_with('foo')

    def test_queue_delete_unknown_queue(self):
        assert self.channel.queue_delete('xiwjqjwel') is None

    def test_queue_declare_passive(self):
        has_queue = self.channel._has_queue = Mock()
        has_queue.return_value = False
        with pytest.raises(ChannelError):
            self.channel.queue_declare(queue='21wisdjwqe', passive=True)

    def test_get_message_priority(self):

        def _message(priority):
            return self.channel.prepare_message(
                'the message with priority', priority=priority,
            )

        assert self.channel._get_message_priority(_message(5)) == 5
        assert self.channel._get_message_priority(
            _message(self.channel.min_priority - 10)
        ) == self.channel.min_priority
        assert self.channel._get_message_priority(
            _message(self.channel.max_priority + 10),
        ) == self.channel.max_priority
        assert self.channel._get_message_priority(
            _message('foobar'),
        ) == self.channel.default_priority
        assert self.channel._get_message_priority(
            _message(2), reverse=True,
        ) == self.channel.max_priority - 2


def test_BrokerState_queue_properties():
    s = virtual.BrokerState()
    assert s.queue_properties_get('q') == {}

    s.queue_properties_set('q', message_ttl=1000, max_length=2)
    assert s.queue_properties_get('q') == {
        'message_ttl': 1000, 'max_length': 2,
    }
    s.queue_properties_set('q', dead_letter_exchange='dlx')
    assert s.queue_properties_get('q') == {'dead_letter_exchange': 'dlx'}

    s.queue_properties_delete('q')
    assert s.queue_properties_get('q') == {}
    s.queue_properties_delete('q')

    s.queue_properties_set('q', max_length=1)
    s.binding_declare('q', 'e', 'rk', None)
    s.queue_bindings_delete('q')
    assert s.queue_properties_get('q') == {}

    s.queue_properties_set('q', max_length=1)
    s.clear()
    assert s.queue_properties == {}


class test_DeadLettering:

    def setup_method(self):
        self.channel = memory_client().channel()
        self.prefix = uuid()
        self.queue = self.name('queue')
        self.dlq = self.name('dlq')
        self.exchange = self.name('exchange')
        self.dlx = self.name('dlx')
        self.channel.exchange_declare(self.exchange)
        self.channel.exchange_declare(self.dlx)
        self.channel.queue_declare(self.dlq)
        self.channel.queue_bind(self.dlq, self.dlx, 'dead')

    def teardown_method(self):
        state = self.channel.state
        for exchange in [e for e in state.exchanges
                         if e.startswith(self.prefix)]:
            self.channel.exchange_delete(exchange)
        for queue in [q for q in state.queue_properties
                      if q.startswith(self.prefix)]:
            self.channel.queue_delete(queue)
        self.channel.close()

    def name(self, suffix):
        return f'{self.prefix}.{suffix}'

    def declare(self, queue=None, routing_key='rk', **kwargs):
        queue = queue or self.queue
        self.channel.queue_declare(
            queue, arguments=self.channel.prepare_queue_arguments({}, **kwargs),
        )
        self.channel.queue_bind(queue, self.exchange, routing_key)
        return queue

    def publish(self, body='hello', routing_key='rk', exchange=None,
                **properties):
        message = self.channel.prepare_message(body, properties=properties)
        self.channel.basic_publish(
            message, self.exchange if exchange is None else exchange,
            routing_key,
        )
        return message

    def expire_all(self, queue):
        for message in self.channel.queues[queue].queue:
            message['properties']['x-expires-at'] = time() - 1

    def test_prepare_queue_arguments(self):
        assert self.channel.prepare_queue_arguments(
            {'x-foo': 1},
            dead_letter_exchange='dlx',
            dead_letter_routing_key='drk',
            message_ttl=1.5,
            max_length=10,
            max_length_bytes=1024,
            expires=2,
            max_priority=5,
        ) == {
            'x-foo': 1,
            'x-dead-letter-exchange': 'dlx',
            'x-dead-letter-routing-key': 'drk',
            'x-message-ttl': 1500,
            'x-max-length': 10,
            'x-max-length-bytes': 1024,
            'x-expires': 2000,
            'x-max-priority': 5,
        }
        assert self.channel.prepare_queue_arguments(None) == {}

    def test_queue_declare_stores_properties(self):
        self.declare(dead_letter_exchange=self.dlx, message_ttl=1,
                     max_length=3)
        assert self.channel.get_queue_properties(self.queue) == {
            'dead_letter_exchange': self.dlx,
            'message_ttl': 1000,
            'max_length': 3,
        }
        assert self.channel.queue_properties_for_declare(self.queue) == {
            'x-dead-letter-exchange': self.dlx,
            'x-message-ttl': 1000,
            'x-max-length': 3,
        }

        self.channel.queue_declare(self.queue, passive=True)
        assert self.channel.get_queue_properties(self.queue)

        self.channel.queue_declare(
            self.queue, arguments={'x-max-length': 5})
        assert self.channel.get_queue_properties(self.queue) == {
            'max_length': 5,
        }

        self.channel.queue_delete(self.queue)
        assert self.channel.get_queue_properties(self.queue) == {}

    def test_prepare_message_expiration(self):
        before = time()
        message = self.channel.prepare_message(
            'x', properties={'expiration': '1500'})
        expires_at = message['properties']['x-expires-at']
        assert before + 1.5 <= expires_at <= time() + 1.5
        assert 'x-expires-at' not in (
            self.channel.prepare_message('x')['properties'])

    def test_message_ttl_remaining(self):
        message = self.channel.prepare_message(
            'x', properties={'expiration': '10000'})
        assert 9 < self.channel.message_ttl_remaining(message) <= 10
        assert self.channel.message_ttl_remaining(
            self.channel.prepare_message('x')) is None
        message['properties']['x-expires-at'] = time() - 1
        assert self.channel.message_ttl_remaining(message) < 0

    def test_put_applies_queue_ttl(self):
        self.declare(message_ttl=10)
        self.channel.put(self.queue, self.channel.prepare_message('x'))
        raw = self.channel.queues[self.queue].queue[0]
        assert 9 < self.channel.message_ttl_remaining(raw) <= 10

    def test_put_message_expiration_takes_precedence(self):
        self.declare(message_ttl=10)
        self.channel.put(self.queue, self.channel.prepare_message(
            'x', properties={'expiration': '100000'}))
        raw = self.channel.queues[self.queue].queue[0]
        assert self.channel.message_ttl_remaining(raw) > 10

    def test_publish_independent_expiry_per_queue(self):
        q1 = self.declare(self.name('q1'), message_ttl=10)
        q2 = self.declare(self.name('q2'), message_ttl=100)
        q3 = self.declare(self.name('q3'))
        self.publish()
        ttl = self.channel.message_ttl_remaining
        assert 9 < ttl(self.channel.queues[q1].queue[0]) <= 10
        assert 99 < ttl(self.channel.queues[q2].queue[0]) <= 100
        assert ttl(self.channel.queues[q3].queue[0]) is None

    def test_basic_get_skips_expired(self):
        self.declare(dead_letter_exchange=self.dlx,
                     dead_letter_routing_key='dead')
        self.publish('old')
        self.expire_all(self.queue)
        self.publish('new')

        message = self.channel.basic_get(self.queue, no_ack=True)
        assert message.body == b'new'
        assert message.delivery_info['queue'] == self.queue

        dead = self.channel.basic_get(self.dlq, no_ack=True)
        assert dead.body == b'old'
        assert dead.delivery_info['queue'] == self.dlq
        assert dead.headers['x-death'][0]['reason'] == 'expired'

    def test_basic_get_all_expired(self):
        self.declare()
        self.publish()
        self.publish()
        self.expire_all(self.queue)
        assert self.channel.basic_get(self.queue) is None
        assert self.channel._size(self.queue) == 0

    def test_basic_consume_sets_queue_and_skips_expired(self):
        self.declare()
        self.publish('old')
        self.expire_all(self.queue)
        self.publish('new')
        received = []
        self.channel.basic_consume(
            self.queue, True, received.append, consumer_tag='tag')
        callback = self.channel.connection._callbacks[self.queue]
        while self.channel._size(self.queue):
            callback(self.channel._get(self.queue))
        assert [m.body for m in received] == [b'new']
        assert received[0].delivery_info['queue'] == self.queue

    def test_drain_expired(self):
        self.declare(dead_letter_exchange=self.dlx,
                     dead_letter_routing_key='dead')
        self.publish('a')
        self.publish('b')
        self.expire_all(self.queue)
        self.publish('c')
        assert self.channel.drain_expired(self.queue) == 2
        assert self.channel._size(self.queue) == 1
        assert self.channel._size(self.dlq) == 2
        assert virtual.Channel.drain_expired(self.channel, self.queue) == 0

    def test_generic_drain_expired(self):
        self.declare()
        self.publish('a')
        self.expire_all(self.queue)
        self.publish('b')
        self.publish('c')
        assert virtual.Channel.drain_expired(self.channel, self.queue) == 1
        assert [
            self.channel.basic_get(self.queue, no_ack=True).body
            for _ in range(2)
        ] == [b'b', b'c']

    def test_max_length_evicts_oldest(self):
        self.declare(max_length=2, dead_letter_exchange=self.dlx,
                     dead_letter_routing_key='dead')
        for body in ('a', 'b', 'c'):
            self.publish(body)
        assert [
            self.channel.basic_get(self.queue, no_ack=True).body
            for _ in range(2)
        ] == [b'b', b'c']
        dead = self.channel.basic_get(self.dlq, no_ack=True)
        assert dead.body == b'a'
        assert dead.headers['x-death'][0]['reason'] == 'maxlen'

    def test_dead_letter_without_dlx_discards(self):
        self.declare()
        self.channel.dead_letter(
            self.publish_raw(), self.queue, 'rejected')
        assert self.channel._size(self.dlq) == 0

    def test_dead_letter_missing_exchange(self):
        self.declare(dead_letter_exchange=self.name('missing'))
        self.channel.dead_letter(
            self.publish_raw(), self.queue, 'rejected')
        assert self.channel._size(self.dlq) == 0

    def test_dead_letter_invalid_reason(self):
        with pytest.raises(ValueError):
            self.channel.dead_letter(self.publish_raw(), self.queue, 'foo')

    def publish_raw(self, routing_key='rk', **properties):
        message = self.channel.prepare_message('x', properties=properties)
        self.channel._inplace_augment_message(
            message, self.exchange, routing_key)
        return message

    def test_dead_letter_headers_and_routing(self):
        self.declare(dead_letter_exchange=self.dlx,
                     dead_letter_routing_key='dead')
        raw = self.publish_raw(expiration='100000')
        self.channel.dead_letter(raw, self.queue, 'rejected')

        dead = self.channel.basic_get(self.dlq, no_ack=True)
        assert dead.delivery_info['exchange'] == self.dlx
        assert dead.delivery_info['routing_key'] == 'dead'
        assert 'expiration' not in dead.properties
        assert 'x-expires-at' not in dead.properties
        [death] = dead.headers['x-death']
        assert death['queue'] == self.queue
        assert death['reason'] == 'rejected'
        assert death['exchange'] == self.exchange
        assert death['routing-key'] == 'rk'
        assert death['count'] == 1
        assert isinstance(death['time'], float)
        assert dead.headers['x-first-death-reason'] == 'rejected'
        assert dead.headers['x-first-death-queue'] == self.queue
        assert dead.headers['x-first-death-exchange'] == self.exchange

    def test_dead_letter_preserves_routing_key(self):
        self.declare(dead_letter_exchange=self.dlx)
        self.channel.dead_letter(
            self.publish_raw(routing_key='dead'), self.queue, 'rejected')
        dead = self.channel.basic_get(self.dlq, no_ack=True)
        assert dead.delivery_info['routing_key'] == 'dead'

    def test_x_death_count_and_first_death(self):
        self.declare(dead_letter_exchange=self.dlx,
                     dead_letter_routing_key='dead')
        raw = self.publish_raw()
        self.channel.dead_letter(raw, self.queue, 'rejected')
        dead = self.channel.basic_get(self.dlq, no_ack=True)
        self.channel.dead_letter(dead, self.queue, 'rejected')
        dead = self.channel.basic_get(self.dlq, no_ack=True)
        self.channel.dead_letter(dead, self.queue, 'expired')
        dead = self.channel.basic_get(self.dlq, no_ack=True)

        deaths = dead.headers['x-death']
        assert [(d['reason'], d['count']) for d in deaths] == [
            ('rejected', 2), ('expired', 1),
        ]
        assert dead.headers['x-first-death-reason'] == 'rejected'

    def test_dead_letter_cycle_is_dropped(self):
        self.declare(routing_key='rk', dead_letter_exchange=self.exchange)
        self.channel.dead_letter(self.publish_raw(), self.queue, 'expired')
        assert self.channel._size(self.queue) == 0

    def test_dead_letter_max_hops(self):
        self.channel.dead_letter_max_hops = 2
        self.declare(dead_letter_exchange=self.dlx,
                     dead_letter_routing_key='dead')
        message = self.publish_raw()
        for _ in range(2):
            self.channel.dead_letter(message, self.queue, 'rejected')
            message = self.channel.basic_get(self.dlq, no_ack=True)
            assert message is not None
        self.channel.dead_letter(message, self.queue, 'rejected')
        assert self.channel.basic_get(self.dlq) is None

    def test_reject_routes_to_dlx(self):
        self.declare(dead_letter_exchange=self.dlx,
                     dead_letter_routing_key='dead')
        self.publish()
        message = self.channel.basic_get(self.queue)
        assert self.channel.qos.redelivery_count(message.delivery_tag) == 0
        message.reject()

        dead = self.channel.basic_get(self.dlq)
        assert dead.headers['x-death'][0]['reason'] == 'rejected'
        assert self.channel.qos.redelivery_count(dead.delivery_tag) == 1
        assert self.channel.qos.redelivery_count('unknown') == 0

    def test_reject_requeue(self):
        self.declare(dead_letter_exchange=self.dlx,
                     dead_letter_routing_key='dead')
        self.publish()
        message = self.channel.basic_get(self.queue)
        message.requeue()
        assert self.channel._size(self.queue) == 1
        assert self.channel._size(self.dlq) == 0


class test_Transport:

    def setup_method(self):
        self.transport = client().transport

    def test_state_is_transport_specific(self):
        # Tests that each Transport of Connection instance
        # has own state attribute
        conn1 = client()
        conn2 = client()
        assert conn1.transport.state != conn2.transport.state

    def test_custom_polling_interval(self):
        x = client(transport_options={'polling_interval': 32.3})
        assert x.transport.polling_interval == 32.3

    def test_timeout_over_polling_interval(self):
        x = client(transport_options=dict(polling_interval=60))
        start = monotonic()
        with pytest.raises(socket.timeout):
            x.transport.drain_events(x, timeout=.5)
            assert monotonic() - start < 60

    def test_close_connection(self):
        c1 = self.transport.create_channel(self.transport)
        c2 = self.transport.create_channel(self.transport)
        assert len(self.transport.channels) == 2
        self.transport.close_connection(self.transport)
        assert not self.transport.channels
        del c1  # so pyflakes doesn't complain
        del c2

    def test_create_channel(self):
        """Ensure create_channel can create channels successfully."""
        assert self.transport.channels == []
        created_channel = self.transport.create_channel(self.transport)
        assert self.transport.channels == [created_channel]

    def test_close_channel(self):
        """Ensure close_channel actually removes the channel and updates
        _used_channel_ids.
        """
        assert self.transport._used_channel_ids == array('H')
        created_channel = self.transport.create_channel(self.transport)
        assert self.transport._used_channel_ids == array('H', (1,))
        self.transport.close_channel(created_channel)
        assert self.transport.channels == []
        assert self.transport._used_channel_ids == array('H')

    def test_drain_channel(self):
        channel = self.transport.create_channel(self.transport)
        with pytest.raises(virtual.Empty):
            self.transport._drain_channel(channel, Mock())

    def test__deliver__no_queue(self):
        with pytest.raises(KeyError):
            self.transport._deliver(Mock(name='msg'), queue=None)

    def test__reject_inbound_message(self):
        channel = Mock(name='channel')
        self.transport.channels = [None, channel]
        self.transport._reject_inbound_message({'foo': 'bar'})
        channel.Message.assert_called_with({'foo': 'bar'}, channel=channel)
        channel.qos.append.assert_called_with(
            channel.Message(), channel.Message().delivery_tag,
        )
        channel.basic_reject.assert_called_with(
            channel.Message().delivery_tag, requeue=True,
        )

    def test_on_message_ready(self):
        channel = Mock(name='channel')
        msg = Mock(name='msg')
        callback = Mock(name='callback')
        self.transport._callbacks = {'q1': callback}
        self.transport.on_message_ready(channel, msg, queue='q1')
        callback.assert_called_with(msg)

    def test_on_message_ready__no_queue(self):
        with pytest.raises(KeyError):
            self.transport.on_message_ready(
                Mock(name='channel'), Mock(name='msg'), queue=None)

    def test_on_message_ready__no_callback(self):
        self.transport._callbacks = {}
        with pytest.raises(KeyError):
            self.transport.on_message_ready(
                Mock(name='channel'), Mock(name='msg'), queue='q1')
