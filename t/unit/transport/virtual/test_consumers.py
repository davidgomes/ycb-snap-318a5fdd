from __future__ import annotations

from unittest.mock import Mock

import pytest

from kombu import Connection, Consumer, Exchange, Producer, Queue
from kombu.transport import filesystem, memory, pyro


def client():
    return Connection(transport='kombu.transport.virtual:Transport')


def sac_args():
    return {'x-single-active-consumer': True}


def prio(priority):
    return {'x-priority': priority}


class test_ConsumerRegistry:

    def setup_method(self):
        self.connection = client()
        self.channel = self.connection.channel()

    def consume(self, tag, queue='q', priority=None, channel=None,
                on_cancel=None, callback=None):
        channel = channel or self.channel
        channel.basic_consume(
            queue, False, callback or Mock(name=tag), tag,
            arguments=prio(priority) if priority is not None else None,
            on_cancel=on_cancel,
        )

    def test_priority_ordering_is_stable(self):
        self.consume('low', priority=1)
        self.consume('high', priority=5)
        self.consume('default')
        self.consume('high2', priority=5)
        info = self.channel.consumer_info('q')
        assert [c['consumer_tag'] for c in info] == [
            'high', 'high2', 'low', 'default',
        ]
        assert set(info[0]) == {
            'queue', 'consumer_tag', 'priority', 'is_active'}
        assert self.channel.consumer_priority_map('q') == {
            'high': 5, 'high2': 5, 'low': 1, 'default': 0,
        }
        assert self.channel.get_consumer_priority('default') == 0
        assert self.channel.get_consumer_priority('unknown') is None
        assert self.channel.get_consumer_count('q') == 4
        assert self.channel.get_consumer_count() == 4
        assert self.channel.get_consumer_count('other') == 0

    def test_non_sac_active_is_highest_priority(self):
        self.consume('a', priority=1)
        self.consume('b', priority=3)
        assert not self.channel.is_single_active_consumer('q')
        assert self.channel.get_active_consumer('q') == 'b'
        assert self.channel.get_sac_status('q') is None
        assert self.channel.get_standby_consumers('q') == []
        assert self.channel.get_active_consumer('none') is None

    def test_sac_first_consumer_active(self):
        self.channel.queue_declare('q', arguments=sac_args())
        self.consume('a')
        self.consume('b')
        assert self.channel.is_single_active_consumer('q')
        assert self.channel.get_sac_status('q') == {
            'queue': 'q', 'active': 'a', 'standby': ['b'],
            'consumer_count': 2,
        }
        assert [c['is_active'] for c in self.channel.consumer_info('q')] == [
            True, False,
        ]

    def test_redeclare_keeps_sac(self):
        self.channel.queue_declare('q', arguments=sac_args())
        self.channel.queue_declare('q')
        self.channel.queue_declare('q', arguments={})
        assert self.channel.is_single_active_consumer('q')

    def test_cancel_notifies_and_promotes(self):
        self.channel.queue_declare('q', arguments=sac_args())
        on_cancel = Mock()
        self.consume('a', on_cancel=on_cancel)
        self.consume('b', priority=0)
        self.consume('c', priority=0)
        self.channel.basic_cancel('a')
        on_cancel.assert_called_once_with('a')
        assert self.channel.get_active_consumer('q') == 'b'
        assert self.channel.get_standby_consumers('q') == ['c']

    def test_cancel_promotes_highest_priority_standby(self):
        self.channel.queue_declare('q', arguments=sac_args())
        self.consume('a', priority=10)
        self.consume('b', priority=1)
        self.consume('c', priority=5)
        self.channel.basic_cancel('a')
        assert self.channel.get_active_consumer('q') == 'c'

    def test_on_cancel_exception_does_not_propagate(self):
        self.consume('a', on_cancel=Mock(side_effect=RuntimeError()))
        self.channel.basic_cancel('a')
        assert self.channel.get_consumer_count('q') == 0

    def test_higher_priority_newcomer_demotes_active(self):
        self.channel.queue_declare('q', arguments=sac_args())
        on_cancel = Mock()
        self.consume('low', priority=1, on_cancel=on_cancel)
        self.consume('same', priority=1)
        on_cancel.assert_not_called()
        assert self.channel.get_active_consumer('q') == 'low'
        self.consume('high', priority=2)
        on_cancel.assert_called_once_with('low')
        assert self.channel.get_active_consumer('q') == 'high'
        assert self.channel.get_consumer_count('q') == 3

    def test_close_cancels_and_promotes_on_other_channel(self):
        self.channel.queue_declare('q', arguments=sac_args())
        other = self.connection.channel()
        on_cancel = Mock()
        self.consume('a', priority=5, on_cancel=on_cancel)
        self.consume('a2', priority=4)
        self.consume('b', channel=other)
        self.channel.close()
        on_cancel.assert_called_once_with('a')
        assert other.get_active_consumer('q') == 'b'
        assert other.consumer_events(event_type='promoted')[-1][
            'consumer_tag'] == 'b'

    def test_queue_delete_notifies_every_consumer(self):
        self.channel.queue_declare('q', arguments=sac_args())
        other = self.connection.channel()
        cancels = [Mock(), Mock()]
        self.consume('a', on_cancel=cancels[0])
        self.consume('b', channel=other, on_cancel=cancels[1])
        self.channel.queue_delete('q')
        cancels[0].assert_called_once_with('a')
        cancels[1].assert_called_once_with('b')
        assert self.channel.get_consumer_count('q') == 0
        assert 'q' not in self.channel._active_queues
        assert 'q' not in other._active_queues
        assert 'q' not in self.connection.transport._callbacks

    def test_promote_consumer(self):
        self.channel.queue_declare('q', arguments=sac_args())
        on_cancel = Mock()
        self.consume('a', on_cancel=on_cancel)
        self.consume('b')
        assert not self.channel.promote_consumer('q', 'a')
        assert not self.channel.promote_consumer('q', 'unknown')
        assert self.channel.promote_consumer('q', 'b')
        assert self.channel.get_active_consumer('q') == 'b'
        on_cancel.assert_called_once_with('a')
        self.consume('x', queue='plain')
        self.consume('y', queue='plain')
        assert not self.channel.promote_consumer('plain', 'y')

    def test_list_consumers_and_tags(self):
        other = self.connection.channel()
        self.consume('b', queue='q1')
        self.consume('a', queue='q2', priority=2)
        self.consume('c', queue='q1', channel=other)
        assert self.channel.consumer_tags == ['a', 'b']
        assert [c['consumer_tag'] for c in self.channel.list_consumers()] == [
            'a', 'b',
        ]
        assert self.channel.consumer_registry_snapshot() == {
            'q1': [
                {'consumer_tag': 'b', 'priority': 0, 'is_active': True},
                {'consumer_tag': 'c', 'priority': 0, 'is_active': False},
            ],
            'q2': [{'consumer_tag': 'a', 'priority': 2, 'is_active': True}],
        }

    def test_state_shared_across_channels(self):
        other = self.connection.channel()
        self.consume('a')
        assert other.consumer_info('q')[0]['consumer_tag'] == 'a'

    def test_consumer_events(self):
        self.channel.queue_declare('q', arguments=sac_args())
        self.consume('a', priority=1)
        self.consume('b', priority=2)
        self.consume('c', priority=0)
        self.channel.basic_cancel('b')
        types = [(e['type'], e['consumer_tag'])
                 for e in self.channel.consumer_events('q')]
        assert types == [
            ('registered', 'a'), ('activated', 'a'),
            ('registered', 'b'), ('demoted', 'a'), ('activated', 'b'),
            ('registered', 'c'),
            ('cancelled', 'b'), ('promoted', 'a'),
        ]
        event = self.channel.consumer_events(event_type='cancelled')[0]
        assert set(event) == {
            'type', 'queue', 'consumer_tag', 'priority', 'timestamp'}
        assert event['priority'] == 2
        assert self.channel.consumer_events(queue='other') == []
        self.channel.clear_consumer_events()
        assert self.channel.consumer_events() == []


class test_Delivery:

    def setup_method(self):
        self.connection = Connection(transport='memory')
        self.exchange = Exchange('sac_ex', 'direct')

    def teardown_method(self):
        self.connection.release()

    def publish(self, n, routing_key):
        producer = Producer(self.connection.channel(), self.exchange)
        for i in range(n):
            producer.publish({'i': i}, routing_key=routing_key)

    def drain(self, n):
        for _ in range(n):
            self.connection.drain_events(timeout=1)

    def test_sac_delivers_only_to_active(self):
        queue = Queue.with_single_active_consumer(
            'sac_q', self.exchange, routing_key='sac_q')
        received = {'a': [], 'b': []}
        c1 = Consumer(self.connection.channel(), [queue],
                      callbacks=[lambda b, m: (received['a'].append(b),
                                               m.ack())])
        c2 = Consumer(self.connection.channel(), [queue],
                      callbacks=[lambda b, m: (received['b'].append(b),
                                               m.ack())])
        c1.consume()
        c2.consume()
        assert c1.consuming_from_sac('sac_q')
        assert c1.is_active_on(queue)
        assert not c2.is_active_on(queue)
        assert c2.active_consumer_tags == []

        self.publish(3, 'sac_q')
        self.drain(3)
        assert len(received['a']) == 3
        assert received['b'] == []

        c1.cancel()
        assert c2.is_active_on('sac_q')
        self.publish(2, 'sac_q')
        self.drain(2)
        assert len(received['b']) == 2

    def test_priority_dispatch_with_prefetch(self):
        queue = Queue('prio_q', self.exchange, routing_key='prio_q')
        received = {'high': [], 'low': []}
        high = Consumer(
            self.connection.channel(),
            [Queue.with_consumer_priority(
                'prio_q', self.exchange, priority=10, routing_key='prio_q')],
            callbacks=[lambda b, m: received['high'].append(m)],
            prefetch_count=1)
        low = Consumer(self.connection.channel(), [queue],
                       callbacks=[lambda b, m: received['low'].append(m)])
        low.consume()
        high.consume()
        self.publish(3, 'prio_q')
        self.drain(3)
        # high is limited to one unacked message, the rest go to low.
        assert len(received['high']) == 1
        assert len(received['low']) == 2

    def test_consumer_on_cancel(self):
        queue = Queue('cancel_q', self.exchange, routing_key='cancel_q')
        on_cancel = Mock()
        other = Mock()
        consumer = Consumer(self.connection.channel(), [queue],
                            on_cancel=on_cancel)
        assert consumer.on_cancel_notify(other) is consumer
        consumer.consume()
        tag = consumer._active_tags['cancel_q']
        consumer.channel.queue_delete('cancel_q')
        on_cancel.assert_called_once_with(tag)
        other.assert_called_once_with(tag)

    def test_consumer_without_on_cancel(self):
        consumer = Consumer(self.connection.channel(), [])
        assert consumer.cancel_notify_callbacks == []


@pytest.mark.parametrize('module', [memory, filesystem, pyro])
def test_new_transport_clears_global_consumer_state(module):
    Transport = module.Transport
    state = Transport.global_state
    state.mark_single_active_consumer('leak_q')
    state.consumer_events.append({'type': 'registered'})
    state.consumers['leak_q'] = [Mock()]
    Transport(client=Mock(transport_options={}))
    assert not state.consumers
    assert not state.sac_queues
    assert not state.consumer_events


def test_memory_registrations_do_not_leak():
    conn1 = Connection(transport='memory')
    chan = conn1.channel()
    chan.queue_declare('leak_q2', arguments=sac_args())
    chan.basic_consume('leak_q2', False, Mock(), 'tag')
    conn2 = Connection(transport='memory')
    assert conn2.channel().consumer_info('leak_q2') == []


class test_QueueEntity:

    def test_properties(self):
        q = Queue('q')
        assert not q.is_single_active_consumer
        assert q.consumer_priority == 0

    def test_with_consumer_priority(self):
        q = Queue.with_consumer_priority(
            'q', 'ex', priority=3, consumer_arguments={'x-foo': 1})
        assert q.consumer_priority == 3
        assert q.consumer_arguments == {'x-foo': 1, 'x-priority': 3}
        assert not q.is_single_active_consumer

    def test_with_single_active_consumer(self):
        q = Queue.with_single_active_consumer('q', 'ex', durable=False)
        assert q.is_single_active_consumer
        assert not q.durable
        assert q.exchange.name == 'ex'

    def test_with_priority_and_sac(self):
        q = Queue.with_priority_and_sac('q', 'ex', priority=7,
                                        routing_key='rk')
        assert q.is_single_active_consumer
        assert q.consumer_priority == 7
        assert q.durable
        assert q.routing_key == 'rk'
