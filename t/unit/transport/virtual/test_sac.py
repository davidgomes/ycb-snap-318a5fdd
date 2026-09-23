"""Single-active-consumer and consumer priority behaviour."""
from __future__ import annotations

from unittest.mock import Mock

import pytest

from kombu import Connection, Consumer, Exchange, Queue
from kombu.transport import filesystem, memory, pyro
from kombu.transport.virtual.base import Empty


def _channel(connection=None):
    connection = connection or Connection('memory://')
    return connection, connection.channel()


def _declare_sac(channel, queue='sac'):
    channel.queue_declare(
        queue, arguments={'x-single-active-consumer': True},
    )
    return queue


def _consume(channel, queue, tag, priority=0, callback=None, on_cancel=None):
    channel.basic_consume(
        queue, True, callback or (lambda message: None), tag,
        arguments={'x-priority': priority},
        on_cancel=on_cancel,
    )


class test_queue_entity:

    def test_defaults(self):
        queue = Queue('q', Exchange('e'))
        assert queue.is_single_active_consumer is False
        assert queue.consumer_priority == 0

    def test_factories(self):
        priority = Queue.with_consumer_priority(
            'q', Exchange('e'), priority=7, routing_key='rk',
            consumer_arguments={'x-extra': 1},
        )
        assert priority.consumer_priority == 7
        assert priority.consumer_arguments['x-extra'] == 1
        assert priority.routing_key == 'rk'
        assert priority.is_single_active_consumer is False

        sac = Queue.with_single_active_consumer(
            'q', 'ex', durable=False, queue_arguments={'x-message-ttl': 5},
        )
        assert sac.durable is False
        assert sac.is_single_active_consumer is True
        assert sac.queue_arguments['x-message-ttl'] == 5

        both = Queue.with_priority_and_sac('q', 'ex', priority=3)
        assert both.durable is True
        assert both.is_single_active_consumer is True
        assert both.consumer_priority == 3


class test_registration:

    def test_priority_order_and_active_flag(self):
        _connection, channel = _channel()
        channel.queue_declare('q')
        _consume(channel, 'q', 'low', priority=1)
        _consume(channel, 'q', 'same', priority=1)
        _consume(channel, 'q', 'high', priority=9)
        _consume(channel, 'q', 'mid', priority=4)

        assert [item['consumer_tag'] for item in channel.consumer_info('q')] == [
            'high', 'mid', 'low', 'same',
        ]
        assert channel.get_active_consumer('q') == 'high'
        info = channel.consumer_info('q')
        assert info[0]['is_active'] is True
        assert all(item['is_active'] is False for item in info[1:])
        assert channel.get_consumer_priority('mid') == 4
        assert channel.get_consumer_priority('missing') is None
        assert channel.consumer_priority_map('q') == {
            'high': 9, 'mid': 4, 'low': 1, 'same': 1,
        }
        assert channel.consumer_tags == ['high', 'low', 'mid', 'same']
        assert channel.get_consumer_count('q') == 4
        assert channel.get_consumer_count() == 4
        assert channel.is_single_active_consumer('q') is False
        assert channel.get_sac_status('q') == {
            'queue': 'q',
            'active': None,
            'standby': None,
            'consumer_count': None,
        }
        assert channel.get_standby_consumers('q') == []

    def test_callbacks_dispatch_to_highest_priority(self):
        _connection, channel = _channel()
        seen = []
        channel.queue_declare('q')
        _consume(channel, 'q', 'low', 1, lambda message: seen.append('low'))
        _consume(channel, 'q', 'high', 5, lambda message: seen.append('high'))
        raw = {
            'body': 'x',
            'properties': {'delivery_tag': 'd'},
        }
        # Direct dispatch must not depend on which consumer registered last.
        channel.connection._callbacks['q'](raw)
        assert seen == ['high']

    def test_prefetch_falls_through_to_next_priority(self):
        connection, high = _channel()
        low = connection.channel()
        got = {'high': [], 'low': []}
        high.queue_declare('prio')
        high.queue_purge('prio')
        high.basic_qos(prefetch_count=1)
        low.basic_qos(prefetch_count=1)
        low.basic_consume(
            'prio', False, lambda message: got['low'].append(message.body),
            'low', arguments={'x-priority': 1},
        )
        high.basic_consume(
            'prio', False, lambda message: got['high'].append(message.body),
            'high', arguments={'x-priority': 10},
        )

        high.basic_publish(high.prepare_message('one'), '', 'prio')
        high.drain_events(timeout=1)
        assert got['high'] == [b'one']
        assert got['low'] == []

        high.basic_publish(high.prepare_message('two'), '', 'prio')
        with pytest.raises(Empty):
            high.drain_events(timeout=1)
        low.drain_events(timeout=1)
        assert got['low'] == [b'two']
        assert low.get_active_consumer('prio') == 'high'


class test_single_active_consumer:

    def test_only_active_receives_and_redeclare_keeps_sac(self):
        connection, active = _channel()
        standby = connection.channel()
        queue = _declare_sac(active)
        got = []
        _consume(active, queue, 'active', 5, lambda message: got.append('active'))
        _consume(standby, queue, 'standby', 1, lambda message: got.append('standby'))

        assert active.is_single_active_consumer(queue) is True
        assert active.get_active_consumer(queue) == 'active'
        assert active.get_standby_consumers(queue) == ['standby']
        assert standby.get_sac_status(queue) == {
            'queue': queue,
            'active': 'active',
            'standby': ['standby'],
            'consumer_count': 2,
        }
        assert standby.list_consumers() == [{
            'queue': queue,
            'consumer_tag': 'standby',
            'priority': 1,
            'is_active': False,
        }]
        assert active.list_consumers()[0]['consumer_tag'] == 'active'

        active.basic_publish(active.prepare_message('body'), '', queue)
        standby.drain_events(timeout=1)
        assert got == ['active']

        active.queue_declare(queue)
        assert active.is_single_active_consumer(queue) is True

    def test_higher_priority_demotes_and_equal_does_not(self):
        _connection, channel = _channel()
        queue = _declare_sac(channel)
        cancelled = []

        def on_cancel(tag):
            cancelled.append(tag)

        _consume(channel, queue, 'first', 1, on_cancel=on_cancel)
        _consume(channel, queue, 'equal', 1, on_cancel=on_cancel)
        assert cancelled == []
        assert channel.get_active_consumer(queue) == 'first'

        _consume(channel, queue, 'higher', 8, on_cancel=on_cancel)
        assert cancelled == ['first']
        assert channel.get_active_consumer(queue) == 'higher'
        assert channel.get_standby_consumers(queue) == ['first', 'equal']
        types = [event['type'] for event in channel.consumer_events(queue)]
        assert 'demoted' in types
        assert 'activated' in types

    def test_cancel_promotes_highest_standby(self):
        connection, channel = _channel()
        other = connection.channel()
        queue = _declare_sac(channel)
        cancelled = []
        _consume(channel, queue, 'low', 1)
        _consume(other, queue, 'mid', 4)
        _consume(
            channel, queue, 'high', 9,
            on_cancel=lambda tag: cancelled.append(tag),
        )
        assert channel.get_active_consumer(queue) == 'high'

        channel.basic_cancel('high')
        assert cancelled == ['high']
        assert channel.get_active_consumer(queue) == 'mid'
        promoted = channel.consumer_events(queue, event_type='promoted')
        assert [event['consumer_tag'] for event in promoted] == ['mid']

        def explode(tag):
            raise RuntimeError(tag)

        other.basic_cancel('mid')
        channel.basic_consume(
            queue, True, lambda message: None, 'boom',
            on_cancel=explode,
        )
        # 'boom' is priority 0, so 'low' stays active. Cancelling the
        # active consumer must swallow callback errors and promote.
        channel.basic_cancel('low')
        assert channel.get_active_consumer(queue) == 'boom'
        channel.basic_cancel('boom')
        assert channel.get_active_consumer(queue) is None

    def test_close_notifies_and_promotes(self):
        connection, channel = _channel()
        other = connection.channel()
        queue = _declare_sac(channel)
        cancelled = []
        _consume(channel, queue, 'active', 5, on_cancel=lambda tag: cancelled.append(tag))
        _consume(other, queue, 'standby', 1, on_cancel=lambda tag: cancelled.append(tag))
        channel.close()
        assert cancelled == ['active']
        assert other.get_active_consumer(queue) == 'standby'
        assert any(
            event['type'] == 'promoted' and event['consumer_tag'] == 'standby'
            for event in other.consumer_events(queue)
        )

    def test_queue_delete_notifies_every_consumer(self):
        connection, channel = _channel()
        other = connection.channel()
        queue = _declare_sac(channel)
        cancelled = []
        _consume(channel, queue, 'a', 2, on_cancel=lambda tag: cancelled.append(tag))
        _consume(other, queue, 'b', 1, on_cancel=lambda tag: cancelled.append(tag))
        channel.queue_delete(queue)
        assert cancelled == ['a', 'b']
        assert channel.get_consumer_count(queue) == 0
        assert channel.is_single_active_consumer(queue) is False
        assert queue not in channel.connection._callbacks

    def test_promote_consumer(self):
        _connection, channel = _channel()
        queue = _declare_sac(channel)
        channel.queue_declare('plain')
        _consume(channel, queue, 'high', 5)
        _consume(channel, queue, 'low', 1)
        _consume(channel, 'plain', 'plain', 1)
        assert channel.promote_consumer(queue, 'high') is False
        assert channel.promote_consumer('plain', 'plain') is False
        assert channel.promote_consumer(queue, 'missing') is False
        assert channel.promote_consumer(queue, 'low') is True
        assert channel.get_active_consumer(queue) == 'low'
        assert channel.get_standby_consumers(queue) == ['high']
        demoted = channel.consumer_events(queue, event_type='demoted')
        assert demoted[-1]['consumer_tag'] == 'high'

    def test_event_log_filters_and_clear(self):
        _connection, channel = _channel()
        queue = _declare_sac(channel)
        _consume(channel, queue, 'only', 1)
        events = channel.consumer_events()
        assert events[0]['type'] == 'registered'
        assert events[1]['type'] == 'activated'
        assert events[0]['queue'] == queue
        assert events[0]['consumer_tag'] == 'only'
        assert events[0]['priority'] == 1
        assert isinstance(events[0]['timestamp'], float)
        channel.clear_consumer_events()
        assert channel.consumer_events() == []

    def test_registry_snapshot(self):
        _connection, channel = _channel()
        queue = _declare_sac(channel)
        _consume(channel, queue, 'a', 1)
        _consume(channel, queue, 'b', 3)
        assert channel.consumer_registry_snapshot() == {
            queue: [
                {'consumer_tag': 'b', 'priority': 3, 'is_active': True},
                {'consumer_tag': 'a', 'priority': 1, 'is_active': False},
            ],
        }

    def test_new_global_transport_clears_consumer_state(self):
        _connection, channel = _channel()
        queue = _declare_sac(channel)
        _consume(channel, queue, 'tag', 1)
        assert channel.get_consumer_count() == 1

        client = Mock()
        client.transport_options = {}
        memory.Transport(client)
        assert channel.get_consumer_count() == 0
        assert channel.is_single_active_consumer(queue) is False
        assert channel.consumer_events() == []

        def _assert_clears(transport_cls):
            first = transport_cls(client)
            first.state.sac_queues.add('shared')
            first.state.consumers['tag'] = {'queue': 'shared'}
            first.state.consumer_events.append({'type': 'registered'})
            second = transport_cls(client)
            assert second.state is first.state
            assert second.state.consumers == {}
            assert second.state.sac_queues == set()
            assert second.state.consumer_events == []

        _assert_clears(memory.Transport)
        _assert_clears(filesystem.Transport)
        _assert_clears(pyro.Transport)


class test_messaging_consumer:

    def test_cancel_callbacks_and_activity(self):
        connection = Connection('memory://')
        channel = connection.channel()
        queue = Queue.with_priority_and_sac('jobs', Exchange('jobs'), priority=4)
        seen = []
        consumer = Consumer(
            channel, queue, on_cancel=lambda tag: seen.append(('init', tag)),
        )
        consumer.on_cancel_notify(lambda tag: seen.append(('extra', tag)))
        assert consumer.on_cancel_notify(lambda tag: seen.append(('more', tag))) is consumer
        consumer.consume()
        tag = consumer._active_tags['jobs']
        assert consumer.consuming_from_sac(queue) is True
        assert consumer.consuming_from_sac('jobs') is True
        assert consumer.is_active_on('jobs') is True
        assert consumer.active_consumer_tags == [tag]
        assert queue.consumer_priority == 4

        other = connection.channel()
        demoted = []
        other.basic_consume(
            'jobs', True, lambda message: None, 'bigger',
            arguments={'x-priority': 10},
            on_cancel=lambda tag: demoted.append(tag),
        )
        assert consumer.is_active_on(queue) is False
        assert consumer.active_consumer_tags == []
        assert seen[0][0] == 'init'
        assert seen[0][1] == tag

        consumer.cancel()
        assert not consumer._active_tags
        assert tag in [item[1] for item in seen]
