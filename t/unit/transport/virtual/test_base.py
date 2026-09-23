from __future__ import annotations

import io
import socket
import warnings
from array import array
from time import monotonic
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

    assert s.queue_properties == {}
    assert s.queue_properties_get('missing') == {}
    s.queue_properties_set(
        'q', message_ttl=5, dead_letter_exchange='dlx',
    )
    assert s.queue_properties_get('q') == {
        'message_ttl': 5,
        'dead_letter_exchange': 'dlx',
    }
    # Redeclare replaces; it does not merge.
    s.queue_properties_set('q', max_length=2)
    assert s.queue_properties_get('q') == {'max_length': 2}
    s.queue_properties_delete('q')
    assert s.queue_properties_get('q') == {}
    s.queue_properties_delete('q')

    s.queue_properties_set('q', expires=9)
    s.binding_declare('q', 'ex', 'rk', None)
    s.queue_bindings_delete('q')
    assert s.queue_properties_get('q') == {}
    assert not s.queue_index.get('q')

    s.queue_properties_set('q', expires=9)
    s.clear()
    assert s.queue_properties == {}
    assert s.queue_properties_get('q') == {}


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


class test_DeadLetterAndTTL:

    def setup_method(self):
        self.connection = memory_client()
        self.channel = self.connection.channel()
        self._cleanup = []

    def teardown_method(self):
        if self.channel._qos is not None:
            self.channel._qos._on_collect.cancel()
        for queue, exchange in self._cleanup:
            self.channel.queue_delete(queue)
            if exchange:
                self.channel.state.exchanges.pop(exchange, None)

    def _n(self, prefix):
        return f'dlttl-{prefix}-{uuid()[:8]}'

    def _track(self, queue, exchange=None):
        self._cleanup.append((queue, exchange))
        return queue

    def _bind(self, queue, exchange, routing_key, arguments=None,
              exchange_type='direct'):
        self._track(queue, exchange)
        if exchange not in self.channel.state.exchanges:
            self.channel.exchange_declare(exchange, exchange_type)
        self.channel.queue_declare(queue, arguments=arguments or {})
        self.channel.queue_bind(queue, exchange, routing_key)

    def _publish(self, exchange, routing_key, body='payload', properties=None):
        message = self.channel.prepare_message(body, properties=properties)
        self.channel.basic_publish(message, exchange, routing_key)
        return message

    def test_prepare_queue_arguments_and_redeclare(self):
        converted = self.channel.prepare_queue_arguments(
            {'extra': 1},
            dead_letter_exchange='dlx',
            dead_letter_routing_key='dead',
            message_ttl=1.5,
            expires=2,
            max_length=10,
            max_length_bytes=64,
            max_priority=3,
        )
        assert converted == {
            'extra': 1,
            'x-dead-letter-exchange': 'dlx',
            'x-dead-letter-routing-key': 'dead',
            'x-message-ttl': 1500,
            'x-expires': 2000,
            'x-max-length': 10,
            'x-max-length-bytes': 64,
            'x-max-priority': 3,
        }

        queue = self._track(self._n('q'))
        self.channel.queue_declare(queue, arguments={
            'x-dead-letter-exchange': 'dlx',
            'x-message-ttl': 1500,
            'x-max-length': 4,
        })
        assert self.channel.get_queue_properties(queue) == {
            'dead_letter_exchange': 'dlx',
            'message_ttl': 1500,
            'max_length': 4,
        }
        self.channel.queue_declare(queue, arguments={'x-max-length': 9})
        assert self.channel.get_queue_properties(queue) == {'max_length': 9}
        assert self.channel.queue_properties_for_declare(queue) == {
            'x-max-length': 9,
        }
        self.channel.queue_delete(queue)
        assert self.channel.get_queue_properties(queue) == {}

    def test_per_message_expiration_and_remaining_ttl(self):
        with patch('kombu.transport.virtual.base.time', return_value=1_000):
            message = self.channel.prepare_message(
                'body', properties={'expiration': '2500'},
            )
        assert message['properties']['expiration'] == '2500'
        assert message['properties']['x-expires-at'] == 1_002.5
        plain = self.channel.prepare_message('body')
        assert 'x-expires-at' not in plain['properties']
        assert self.channel.message_ttl_remaining(plain) is None

        with patch('kombu.transport.virtual.base.time', return_value=1_001):
            assert self.channel.message_ttl_remaining(message) == 1.5
        with patch('kombu.transport.virtual.base.time', return_value=1_004):
            assert self.channel.message_ttl_remaining(message) == -1.5

    def test_queue_ttl_and_per_message_precedence(self):
        exchange = self._n('ex')
        short_q = self._n('short')
        long_q = self._n('long')
        self._bind(short_q, exchange, 'rk', {'x-message-ttl': 1000})
        self._bind(long_q, exchange, 'rk', {'x-message-ttl': 5000})

        with patch('kombu.transport.virtual.base.time', return_value=5_000):
            self._publish(exchange, 'rk', 'ttl')
            short = self.channel.basic_get(short_q, no_ack=True)
            long = self.channel.basic_get(long_q, no_ack=True)

        assert short.properties['expiration'] == '1000'
        assert long.properties['expiration'] == '5000'
        assert short.properties['x-expires-at'] == 5_001
        assert long.properties['x-expires-at'] == 5_005
        assert short.properties is not long.properties
        assert short.delivery_info['queue'] == short_q
        assert long.delivery_info['queue'] == long_q

        prefer = self._n('prefer')
        self._bind(prefer, exchange, 'pref', {'x-message-ttl': 1000})
        with patch('kombu.transport.virtual.base.time', return_value=8_000):
            self._publish(
                exchange, 'pref', 'msg', properties={'expiration': '60000'},
            )
            message = self.channel.basic_get(prefer, no_ack=True)
        assert message.properties['expiration'] == '60000'
        assert message.properties['x-expires-at'] == 8_060

    def test_topic_exchange_applies_queue_ttl(self):
        exchange = self._n('topic')
        queue = self._n('tq')
        self._bind(
            queue, exchange, 'alpha.beta', {'x-message-ttl': 2000},
            exchange_type='topic',
        )
        with patch('kombu.transport.virtual.base.time', return_value=50):
            self._publish(exchange, 'alpha.beta', 'topic')
            message = self.channel.basic_get(queue, no_ack=True)
        assert message.properties['x-expires-at'] == 52
        assert message.delivery_info['queue'] == queue

    def test_basic_get_skips_expired_and_consume_stamps_queue(self):
        exchange = self._n('ex')
        queue = self._n('q')
        dlx = self._n('dlx')
        dlq = self._n('dlq')
        self._bind(dlq, dlx, 'rk')
        self._bind(queue, exchange, 'rk', {
            'x-dead-letter-exchange': dlx,
        })
        with patch('kombu.transport.virtual.base.time', return_value=10):
            self._publish(exchange, 'rk', 'old', properties={'expiration': '1'})
            self._publish(exchange, 'rk', 'new', properties={'expiration': '5000'})
        with patch('kombu.transport.virtual.base.time', return_value=12):
            live = self.channel.basic_get(queue, no_ack=True)
            dead = self.channel.basic_get(dlq, no_ack=True)
        assert live.body == b'new'
        assert live.delivery_info['queue'] == queue
        assert dead.body == b'old'
        assert dead.headers['x-death'][0]['reason'] == 'expired'
        assert dead.headers['x-first-death-reason'] == 'expired'
        assert dead.headers['x-first-death-queue'] == queue
        assert 'expiration' not in dead.properties
        assert 'x-expires-at' not in dead.properties

        with patch('kombu.transport.virtual.base.time', return_value=10):
            self._publish(
                exchange, 'rk', 'only-old', properties={'expiration': '1'},
            )
        with patch('kombu.transport.virtual.base.time', return_value=12):
            assert self.channel.basic_get(queue, no_ack=True) is None

        self._publish(exchange, 'rk', 'consumed')
        received = []
        self.channel.basic_consume(
            queue, no_ack=True, callback=received.append, consumer_tag='ctag',
        )
        self.channel.drain_events(timeout=1)
        self.channel.basic_cancel('ctag')
        assert received[0].delivery_info['queue'] == queue
        assert received[0].body == b'consumed'

    def test_drain_expired_leaves_survivors(self):
        exchange = self._n('ex')
        queue = self._n('q')
        dlx = self._n('dlx')
        dlq = self._n('dlq')
        self._bind(dlq, dlx, 'rk')
        self._bind(queue, exchange, 'rk', {'x-dead-letter-exchange': dlx})
        with patch('kombu.transport.virtual.base.time', return_value=100):
            self._publish(exchange, 'rk', 'a', properties={'expiration': '1'})
            self._publish(exchange, 'rk', 'keep', properties={'expiration': '10000'})
            self._publish(exchange, 'rk', 'b', properties={'expiration': '1'})
        with patch('kombu.transport.virtual.base.time', return_value=102):
            assert self.channel.drain_expired(queue) == 2
            survivor = self.channel.basic_get(queue, no_ack=True)
            assert self.channel.basic_get(queue, no_ack=True) is None
        assert survivor.body == b'keep'
        assert survivor.properties['expiration'] == '10000'
        dead = [
            self.channel.basic_get(dlq, no_ack=True).body,
            self.channel.basic_get(dlq, no_ack=True).body,
        ]
        assert dead == [b'a', b'b']
        assert self.channel.expire_messages(queue) == 0
        assert self.channel.expire_messages(self._n('missing')) == 0

    def test_max_length_dead_letters_oldest(self):
        exchange = self._n('ex')
        queue = self._n('q')
        dlx = self._n('dlx')
        dlq = self._n('dlq')
        self._bind(dlq, dlx, 'overflow')
        self._bind(queue, exchange, 'rk', {
            'x-dead-letter-exchange': dlx,
            'x-dead-letter-routing-key': 'overflow',
            'x-max-length': 2,
        })
        for body in ('m1', 'm2', 'm3'):
            self._publish(exchange, 'rk', body)
        assert self.channel.basic_get(queue, no_ack=True).body == b'm2'
        assert self.channel.basic_get(queue, no_ack=True).body == b'm3'
        dead = self.channel.basic_get(dlq, no_ack=True)
        assert dead.body == b'm1'
        entry = dead.headers['x-death'][0]
        assert entry['reason'] == 'maxlen'
        assert entry['count'] == 1
        assert isinstance(entry['count'], int)
        assert entry['queue'] == queue
        assert entry['routing-key'] == 'rk'
        assert dead.delivery_info['exchange'] == dlx
        assert dead.delivery_info['routing_key'] == 'overflow'
        assert dead.delivery_info['queue'] == dlq

    def test_reject_routes_to_dlx_and_redelivery_count(self):
        exchange = self._n('ex')
        queue = self._n('q')
        dlx = self._n('dlx')
        dlq = self._n('dlq')
        self._bind(dlq, dlx, 'rk')
        self._bind(queue, exchange, 'rk', {'x-dead-letter-exchange': dlx})
        self._publish(
            exchange, 'rk', 'reject-me', properties={'expiration': '60000'},
        )
        message = self.channel.basic_get(queue)
        assert message.delivery_info['queue'] == queue
        assert 'expiration' in message.properties
        self.channel.basic_reject(message.delivery_tag, requeue=False)
        dead = self.channel.basic_get(dlq)
        assert dead.body == b'reject-me'
        assert dead.headers['x-death'][0]['reason'] == 'rejected'
        assert dead.headers['x-death'][0]['exchange'] == exchange
        assert dead.headers['x-death'][0]['routing-key'] == 'rk'
        assert dead.headers['x-first-death-exchange'] == exchange
        assert dead.delivery_info['exchange'] == dlx
        assert dead.delivery_info['routing_key'] == 'rk'
        assert 'expiration' not in dead.properties
        assert 'x-expires-at' not in dead.properties
        assert self.channel.qos.redelivery_count(dead.delivery_tag) == 1
        assert self.channel.qos.redelivery_count('missing-tag') == 0

        self._publish(exchange, 'rk', 'back')
        again = self.channel.basic_get(queue)
        self.channel.basic_reject(again.delivery_tag, requeue=True)
        restored = self.channel.basic_get(queue, no_ack=True)
        assert restored.body == b'back'
        assert self.channel.basic_get(dlq, no_ack=True) is None

    def test_death_headers_count_and_first_death(self):
        dlx = self._n('dlx')
        dlq = self._n('dlq')
        source = self._track(self._n('src'))
        other = self._track(self._n('other'))
        self._bind(dlq, dlx, 'rk')
        self.channel.queue_declare(source, arguments={
            'x-dead-letter-exchange': dlx,
        })
        self.channel.queue_declare(other, arguments={
            'x-dead-letter-exchange': dlx,
        })
        raw = self.channel.prepare_message(
            'x', properties={'delivery_info': {
                'exchange': 'original', 'routing_key': 'rk',
            }},
        )
        self.channel.dead_letter(raw, source, 'rejected')
        first = self.channel.basic_get(dlq, no_ack=True)
        assert len(first.headers['x-death']) == 1
        assert first.headers['x-death'][0]['count'] == 1
        self.channel.dead_letter(first, source, 'rejected')
        second = self.channel.basic_get(dlq, no_ack=True)
        assert len(second.headers['x-death']) == 1
        assert second.headers['x-death'][0]['count'] == 2
        assert second.headers['x-first-death-reason'] == 'rejected'
        assert second.headers['x-first-death-queue'] == source

        self.channel.dead_letter(second, other, 'expired')
        third = self.channel.basic_get(dlq, no_ack=True)
        assert [entry['queue'] for entry in third.headers['x-death']] == [
            source, other,
        ]
        assert [entry['reason'] for entry in third.headers['x-death']] == [
            'rejected', 'expired',
        ]
        assert third.headers['x-first-death-reason'] == 'rejected'
        assert third.headers['x-first-death-queue'] == source
        assert third.headers['x-first-death-exchange'] == 'original'
        assert self.channel.basic_get(dlq, no_ack=True) is None

    def test_cycle_and_max_hops_and_missing_dlx(self):
        exchange = self._n('ex')
        dlx = self._n('dlx')
        queue = self._n('q')
        self.channel.exchange_declare(exchange, 'direct')
        self.channel.exchange_declare(dlx, 'direct')
        self._bind(queue, exchange, 'rk', {'x-dead-letter-exchange': dlx})
        self.channel.queue_bind(queue, dlx, 'rk')
        self._publish(exchange, 'rk', 'cycle')
        message = self.channel.basic_get(queue)
        self.channel.basic_reject(message.delivery_tag, requeue=False)
        assert self.channel.basic_get(queue, no_ack=True) is None

        left = self._n('left')
        right = self._n('right')
        end = self._n('end')
        hop_ex = self._n('hop-ex')
        dlx_left = self._n('dlx-left')
        dlx_right = self._n('dlx-right')
        self._bind(end, dlx_right, 'to-end')
        self._bind(right, dlx_left, 'to-right', {
            'x-dead-letter-exchange': dlx_right,
            'x-dead-letter-routing-key': 'to-end',
        })
        self._bind(left, hop_ex, 'rk', {
            'x-dead-letter-exchange': dlx_left,
            'x-dead-letter-routing-key': 'to-right',
        })
        self.channel.dead_letter_max_hops = 1
        self._publish(hop_ex, 'rk', 'hop')
        first = self.channel.basic_get(left)
        self.channel.basic_reject(first.delivery_tag, requeue=False)
        second = self.channel.basic_get(right)
        assert second is not None
        self.channel.basic_reject(second.delivery_tag, requeue=False)
        assert self.channel.basic_get(end, no_ack=True) is None
        assert self.channel.basic_get(right, no_ack=True) is None

        self.channel.dead_letter_max_hops = None
        nowhere = self._n('nowhere')
        self.channel.queue_declare(nowhere, arguments={
            'x-dead-letter-exchange': self._n('missing-ex'),
        })
        self._track(nowhere)
        self.channel.dead_letter(
            self.channel.prepare_message('gone'), nowhere, 'rejected',
        )
        plain = self._track(self._n('plain'))
        self.channel.queue_declare(plain)
        self.channel.dead_letter(
            self.channel.prepare_message('gone'), plain, 'expired',
        )

    def test_two_queue_cycle_stops(self):
        dlx = self._n('dlx')
        origin = self._n('ex')
        queue_a = self._n('a')
        queue_b = self._n('b')
        self.channel.exchange_declare(dlx, 'direct')
        self._bind(queue_a, origin, 'in', {
            'x-dead-letter-exchange': dlx,
            'x-dead-letter-routing-key': 'to-b',
        })
        self.channel.queue_bind(queue_a, dlx, 'to-a')
        self._bind(queue_b, dlx, 'to-b', {
            'x-dead-letter-exchange': dlx,
            'x-dead-letter-routing-key': 'to-a',
        })
        self._publish(origin, 'in', 'ping')
        message = self.channel.basic_get(queue_a)
        self.channel.basic_reject(message.delivery_tag, requeue=False)
        forwarded = self.channel.basic_get(queue_b)
        assert forwarded.body == b'ping'
        self.channel.basic_reject(forwarded.delivery_tag, requeue=False)
        assert self.channel.basic_get(queue_a, no_ack=True) is None
        assert self.channel.basic_get(queue_b, no_ack=True) is None
