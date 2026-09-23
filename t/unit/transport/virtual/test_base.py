from __future__ import annotations

import io
import socket
import warnings
from array import array
from time import monotonic
from unittest.mock import ANY, MagicMock, Mock, patch

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


def test_BrokerState_clear():
    s = virtual.BrokerState()
    s.declare_single_active_consumer('q')
    s.register_consumer(virtual.ConsumerRecord('q', 'tag', channel=Mock()))

    s.clear_consumers()
    assert not s.consumers
    assert not s.active_consumers
    assert not s.consumer_events
    assert s.is_single_active_consumer('q')

    s.register_consumer(virtual.ConsumerRecord('q', 'tag', channel=Mock()))
    s.clear()
    assert not s.consumers
    assert not s.is_single_active_consumer('q')


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


SAC_ARGUMENTS = {'x-single-active-consumer': True}


class test_ConsumerRegistry:

    def setup_method(self):
        self.connection = client()
        self.transport = self.connection.transport
        self.c1 = self.connection.channel()
        self.c2 = self.connection.channel()

    def teardown_method(self):
        for channel in (self.c1, self.c2):
            if channel._qos is not None:
                channel._qos._on_collect.cancel()

    def consume(self, tag, queue='sac', channel=None, priority=None,
                callback=None, on_cancel=None, no_ack=True):
        arguments = None if priority is None else {'x-priority': priority}
        (channel or self.c1).basic_consume(
            queue, no_ack, callback or Mock(name=tag), tag,
            arguments=arguments, on_cancel=on_cancel,
        )

    def deliver(self, queue):
        message = self.c1.prepare_message('body')
        self.c1._inplace_augment_message(message, 'exchange', queue)
        self.transport._callbacks[queue](message)

    def event_types(self, **kwargs):
        return [(event['type'], event['consumer_tag'])
                for event in self.c1.consumer_events(**kwargs)]

    def test_queue_declare_single_active_consumer(self):
        assert not self.c1.is_single_active_consumer('sac')
        self.c1.queue_declare('sac', arguments=SAC_ARGUMENTS)
        assert self.c1.is_single_active_consumer('sac')
        assert self.c2.is_single_active_consumer('sac')

        self.c2.queue_declare('sac')
        self.c2.queue_declare('sac', arguments={'x-max-priority': 10})
        assert self.c1.is_single_active_consumer('sac')

    def test_queue_declare_passive_does_not_enable_sac(self):
        self.c1.queue_declare('sac', passive=True, arguments=SAC_ARGUMENTS)
        assert not self.c1.is_single_active_consumer('sac')

    def test_consumers_ordered_by_priority(self):
        self.consume('a', queue='q', priority=1)
        self.consume('b', queue='q', priority=5)
        self.consume('c', queue='q', channel=self.c2, priority=1)
        self.consume('d', queue='q')

        assert self.c2.consumer_info('q') == [
            {'queue': 'q', 'consumer_tag': 'b', 'priority': 5,
             'is_active': True},
            {'queue': 'q', 'consumer_tag': 'a', 'priority': 1,
             'is_active': False},
            {'queue': 'q', 'consumer_tag': 'c', 'priority': 1,
             'is_active': False},
            {'queue': 'q', 'consumer_tag': 'd', 'priority': 0,
             'is_active': False},
        ]
        assert self.c1.consumer_priority_map('q') == {
            'b': 5, 'a': 1, 'c': 1, 'd': 0,
        }
        assert self.c1.get_active_consumer('q') == 'b'
        assert self.c1.get_standby_consumers('q') == ['a', 'c', 'd']
        assert self.c1.get_sac_status('q') is None
        assert not self.event_types(event_type='activated')

    def test_invalid_consumer_priority_defaults_to_zero(self):
        self.consume('a', queue='q', priority='high')
        self.c1.basic_consume('q', True, Mock(), 'b', arguments={})
        self.c1.basic_consume('q', True, Mock(), 'c')
        assert self.c1.consumer_priority_map('q') == {'a': 0, 'b': 0, 'c': 0}

    def test_sac_first_consumer_is_active(self):
        self.c1.queue_declare('sac', arguments=SAC_ARGUMENTS)
        self.consume('a')
        self.consume('b', channel=self.c2)

        assert self.c2.get_active_consumer('sac') == 'a'
        assert self.c2.get_standby_consumers('sac') == ['b']
        assert self.c1.get_sac_status('sac') == {
            'queue': 'sac', 'active': 'a', 'standby': ['b'],
            'consumer_count': 2,
        }
        assert [i['is_active'] for i in self.c1.consumer_info('sac')] == [
            True, False,
        ]
        assert self.event_types() == [
            ('registered', 'a'), ('activated', 'a'), ('registered', 'b'),
        ]

    def test_sac_higher_priority_consumer_demotes_active(self):
        on_cancel = Mock(name='on_cancel')
        self.c1.queue_declare('sac', arguments=SAC_ARGUMENTS)
        self.consume('a', priority=1, on_cancel=on_cancel)
        self.consume('b', channel=self.c2, priority=1)
        on_cancel.assert_not_called()

        self.consume('c', channel=self.c2, priority=2)
        on_cancel.assert_called_once_with('a')
        assert self.c1.get_active_consumer('sac') == 'c'
        assert self.c1.get_standby_consumers('sac') == ['a', 'b']
        assert self.c1.get_consumer_count('sac') == 3
        assert self.event_types()[-3:] == [
            ('registered', 'c'), ('demoted', 'a'), ('activated', 'c'),
        ]

    def test_sac_equal_priority_consumer_does_not_demote_active(self):
        on_cancel = Mock(name='on_cancel')
        self.c1.queue_declare('sac', arguments=SAC_ARGUMENTS)
        self.consume('a', priority=3, on_cancel=on_cancel)
        self.consume('b', channel=self.c2, priority=3)
        on_cancel.assert_not_called()
        assert self.c1.get_active_consumer('sac') == 'a'

    def test_sac_declared_after_consumers_activates_first(self):
        self.consume('a', priority=1)
        self.consume('b', priority=2)
        self.c1.queue_declare('sac', arguments=SAC_ARGUMENTS)
        assert self.c1.get_active_consumer('sac') == 'b'
        assert self.c1.get_standby_consumers('sac') == ['a']

    def test_basic_cancel_promotes_highest_priority_standby(self):
        on_cancel = Mock(name='on_cancel')
        self.c1.queue_declare('sac', arguments=SAC_ARGUMENTS)
        self.consume('a', priority=5, on_cancel=on_cancel)
        self.consume('b', channel=self.c2, priority=1)
        self.consume('c', channel=self.c2, priority=3)

        self.c1.basic_cancel('a')
        on_cancel.assert_called_once_with('a')
        assert self.c1.get_sac_status('sac') == {
            'queue': 'sac', 'active': 'c', 'standby': ['b'],
            'consumer_count': 2,
        }
        assert self.event_types()[-2:] == [
            ('cancelled', 'a'), ('promoted', 'c'),
        ]

        self.c2.basic_cancel('b')
        assert self.c1.get_active_consumer('sac') == 'c'
        self.c2.basic_cancel('c')
        assert self.c1.get_sac_status('sac') == {
            'queue': 'sac', 'active': None, 'standby': [],
            'consumer_count': 0,
        }
        assert self.c1.is_single_active_consumer('sac')

    def test_basic_cancel_without_on_cancel(self):
        self.consume('a', queue='q')
        self.c1.basic_cancel('a')
        assert self.c1.get_consumer_count('q') == 0
        assert 'q' not in self.transport._callbacks

    def test_basic_cancel_on_cancel_error_does_not_propagate(self):
        on_cancel = Mock(name='on_cancel', side_effect=KeyError('boom'))
        self.consume('a', queue='q', on_cancel=on_cancel)
        self.c1.basic_cancel('a')
        on_cancel.assert_called_once_with('a')
        assert self.c1.get_consumer_count('q') == 0

    def test_basic_cancel_unknown_tag_does_not_notify(self):
        on_cancel = Mock(name='on_cancel')
        self.consume('a', queue='q', on_cancel=on_cancel)
        self.c2.basic_cancel('a')
        on_cancel.assert_not_called()
        assert self.c1.get_consumer_count('q') == 1

    def test_close_cancels_consumers_and_promotes_standby(self):
        on_cancel = Mock(name='on_cancel')
        self.c1.queue_declare('sac', arguments=SAC_ARGUMENTS)
        self.consume('a', priority=5, on_cancel=on_cancel)
        self.consume('b', priority=4, on_cancel=on_cancel)
        self.consume('c', channel=self.c2, priority=1, on_cancel=on_cancel)
        self.consume('d', queue='q', on_cancel=on_cancel)

        self.c1.close()
        assert sorted(c[0][0] for c in on_cancel.call_args_list) == [
            'a', 'b', 'd',
        ]
        assert self.c2.get_sac_status('sac') == {
            'queue': 'sac', 'active': 'c', 'standby': [],
            'consumer_count': 1,
        }
        assert self.c2.get_consumer_count() == 1
        assert self.c2.consumer_events(event_type='promoted') == [{
            'type': 'promoted', 'queue': 'sac', 'consumer_tag': 'c',
            'priority': 1, 'timestamp': ANY,
        }]

    def test_queue_delete_notifies_every_consumer(self):
        on_cancel = Mock(name='on_cancel')
        self.c1.queue_declare('sac', arguments=SAC_ARGUMENTS)
        self.consume('a', on_cancel=on_cancel)
        self.consume('b', channel=self.c2, on_cancel=on_cancel)
        self.consume('c', queue='other', on_cancel=on_cancel)

        self.c1.queue_delete('sac')
        assert sorted(c[0][0] for c in on_cancel.call_args_list) == ['a', 'b']
        assert self.c1.get_consumer_count('sac') == 0
        assert self.c1.consumer_tags == ['c']
        assert self.c2.consumer_tags == []
        assert 'sac' not in self.c2._active_queues
        assert 'sac' not in self.transport._callbacks
        assert not self.c1.is_single_active_consumer('sac')
        assert not self.c1.consumer_events(event_type='promoted')
        assert self.event_types(event_type='cancelled') == [
            ('cancelled', 'a'), ('cancelled', 'b'),
        ]

    def test_queue_delete_if_empty_keeps_consumers(self):
        on_cancel = Mock(name='on_cancel')
        self.consume('a', queue='q', on_cancel=on_cancel)
        self.c1._size = Mock(return_value=3)
        self.c1.queue_delete('q', if_empty=True)
        on_cancel.assert_not_called()
        assert self.c1.get_consumer_count('q') == 1

    def test_promote_consumer(self):
        on_cancel = Mock(name='on_cancel')
        self.c1.queue_declare('sac', arguments=SAC_ARGUMENTS)
        self.consume('a', priority=5, on_cancel=on_cancel)
        self.consume('b', channel=self.c2, priority=1)

        assert self.c1.promote_consumer('sac', 'b')
        on_cancel.assert_called_once_with('a')
        assert self.c1.get_active_consumer('sac') == 'b'
        assert self.c1.get_standby_consumers('sac') == ['a']
        assert self.event_types()[-2:] == [
            ('demoted', 'a'), ('promoted', 'b'),
        ]

        assert not self.c1.promote_consumer('sac', 'b')
        assert not self.c1.promote_consumer('sac', 'unknown')
        on_cancel.assert_called_once()

    def test_promote_consumer_non_sac(self):
        self.consume('a', queue='q', priority=5)
        self.consume('b', queue='q')
        assert not self.c1.promote_consumer('q', 'b')
        assert self.c1.get_active_consumer('q') == 'a'

    def test_state_is_shared_across_channels(self):
        self.consume('a', queue='q')
        self.consume('b', queue='q', channel=self.c2, priority=2)
        for channel in (self.c1, self.c2):
            assert channel.get_consumer_count('q') == 2
            assert channel.get_active_consumer('q') == 'b'
        assert self.c1.list_consumers() == [
            {'queue': 'q', 'consumer_tag': 'a', 'priority': 0,
             'is_active': False},
        ]
        assert self.c2.list_consumers() == [
            {'queue': 'q', 'consumer_tag': 'b', 'priority': 2,
             'is_active': True},
        ]

    def test_introspection(self):
        self.c1.queue_declare('sac', arguments=SAC_ARGUMENTS)
        self.consume('z', priority=1)
        self.consume('m', channel=self.c2, priority=3)
        self.consume('b', queue='q', priority=2)

        assert self.c1.consumer_tags == ['b', 'z']
        assert self.c1.get_consumer_count() == 3
        assert self.c1.get_consumer_count('missing') == 0
        assert self.c1.get_consumer_priority('m') == 3
        assert self.c1.get_consumer_priority('b') == 2
        assert self.c1.get_consumer_priority('missing') is None
        assert self.c1.get_active_consumer('missing') is None
        assert self.c1.get_standby_consumers('missing') == []
        assert [i['consumer_tag'] for i in self.c1.consumer_info()] == [
            'm', 'b', 'z',
        ]
        assert self.c1.list_consumers() == [
            {'queue': 'q', 'consumer_tag': 'b', 'priority': 2,
             'is_active': True},
            {'queue': 'sac', 'consumer_tag': 'z', 'priority': 1,
             'is_active': False},
        ]
        assert self.c2.consumer_registry_snapshot() == {
            'sac': [
                {'consumer_tag': 'm', 'priority': 3, 'is_active': True},
                {'consumer_tag': 'z', 'priority': 1, 'is_active': False},
            ],
            'q': [
                {'consumer_tag': 'b', 'priority': 2, 'is_active': True},
            ],
        }

    def test_consumer_events(self):
        self.c1.queue_declare('sac', arguments=SAC_ARGUMENTS)
        self.consume('a', priority=2)
        self.consume('b', queue='q')
        self.c1.basic_cancel('a')

        events = self.c2.consumer_events()
        assert [e['type'] for e in events] == [
            'registered', 'activated', 'registered', 'cancelled',
        ]
        assert events[0] == {
            'type': 'registered', 'queue': 'sac', 'consumer_tag': 'a',
            'priority': 2, 'timestamp': ANY,
        }
        assert all(isinstance(e['timestamp'], float) for e in events)
        assert self.event_types(queue='q') == [('registered', 'b')]
        assert self.event_types(queue='sac', event_type='cancelled') == [
            ('cancelled', 'a'),
        ]

        events[0]['type'] = 'changed'
        assert self.c1.consumer_events()[0]['type'] == 'registered'

        self.c2.clear_consumer_events()
        assert self.c1.consumer_events() == []

    def test_consume_same_tag_replaces_registration(self):
        self.consume('a', queue='q', priority=1)
        self.consume('a', queue='q', priority=4)
        assert self.c1.consumer_priority_map('q') == {'a': 4}

    def test_delivery_to_active_sac_consumer(self):
        a, b = Mock(name='a'), Mock(name='b')
        self.c1.queue_declare('sac', arguments=SAC_ARGUMENTS)
        self.consume('a', callback=a)
        self.consume('b', channel=self.c2, callback=b)

        self.deliver('sac')
        a.assert_called_once()
        assert a.call_args[0][0].channel is self.c1
        b.assert_not_called()

        self.c1.basic_cancel('a')
        self.deliver('sac')
        b.assert_called_once()
        assert b.call_args[0][0].channel is self.c2

    def test_delivery_respects_priority_and_prefetch(self):
        high, low = Mock(name='high'), Mock(name='low')
        self.consume('low', queue='q', channel=self.c2, callback=low,
                     no_ack=False)
        self.consume('high', queue='q', priority=10, callback=high,
                     no_ack=False)
        self.c1.basic_qos(prefetch_count=1)
        self.c2.basic_qos(prefetch_count=1)

        self.deliver('q')
        assert high.call_count == 1
        assert low.call_count == 0

        self.deliver('q')
        assert high.call_count == 1
        assert low.call_count == 1

        # every channel is full: highest priority consumer gets it.
        self.deliver('q')
        assert high.call_count == 2

    def test_cancel_keeps_callback_for_remaining_consumers(self):
        a, b = Mock(name='a'), Mock(name='b')
        self.consume('a', queue='q', callback=a)
        self.consume('b', queue='q', channel=self.c2, callback=b)

        self.c1.basic_cancel('a')
        assert 'q' in self.transport._callbacks
        self.deliver('q')
        b.assert_called_once()
        a.assert_not_called()

        self.c2.basic_cancel('b')
        assert 'q' not in self.transport._callbacks

    def test_delivery_without_consumers_requeues(self):
        self.transport._reject_inbound_message = Mock()
        self.transport._deliver_to_consumer('q', {'foo': 'bar'})
        self.transport._reject_inbound_message.assert_called_once_with(
            {'foo': 'bar'})

    def test_delivery_falls_back_to_transport_channels(self):
        a = Mock(name='a')
        self.consume('a', queue='q', callback=a)
        self.transport.state.clear_consumers()
        self.deliver('q')
        a.assert_called_once()

    def test_standby_channel_does_not_poll_sac_queue(self):
        self.c1.queue_declare('sac', arguments=SAC_ARGUMENTS)
        self.consume('a')
        self.consume('b', channel=self.c2)
        self.c1._get = Mock(name='c1._get')
        self.c2._get = Mock(name='c2._get')

        with pytest.raises(virtual.Empty):
            self.c2._get_and_deliver('sac', Mock())
        self.c2._get.assert_not_called()

        callback = Mock(name='callback')
        self.c1._get_and_deliver('sac', callback)
        callback.assert_called_once_with(self.c1._get.return_value, 'sac')

        self.c1.basic_cancel('a')
        self.c2._get_and_deliver('sac', callback)
        self.c2._get.assert_called_once_with('sac')
