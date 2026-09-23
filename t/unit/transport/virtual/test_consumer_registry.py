from __future__ import annotations

import socket
from unittest.mock import Mock

import pytest

from kombu import Connection, Consumer, Exchange, Producer, Queue

SAC = {'x-single-active-consumer': True}


def prio(n):
    return {'x-priority': n}


@pytest.fixture
def conn():
    with Connection('memory://') as c:
        yield c


@pytest.fixture
def chan(conn):
    return conn.channel()


def consume(chan, queue, tag, priority=None, on_cancel=None, callback=None):
    chan.basic_consume(
        queue, True, callback or Mock(), tag,
        arguments=prio(priority) if priority is not None else None,
        on_cancel=on_cancel,
    )


class test_SingleActiveConsumer:

    def test_first_registered_is_active(self, chan):
        chan.queue_declare('q', arguments=SAC)
        consume(chan, 'q', 'a')
        consume(chan, 'q', 'b')
        assert chan.is_single_active_consumer('q')
        assert chan.get_active_consumer('q') == 'a'
        assert chan.get_standby_consumers('q') == ['b']
        assert chan.get_sac_status('q') == {
            'queue': 'q', 'active': 'a', 'standby': ['b'],
            'consumer_count': 2,
        }

    def test_redeclare_keeps_sac(self, chan):
        chan.queue_declare('q', arguments=SAC)
        chan.queue_declare('q')
        assert chan.is_single_active_consumer('q')

    def test_non_sac(self, chan):
        chan.queue_declare('q')
        consume(chan, 'q', 'a', 1)
        consume(chan, 'q', 'b', 5)
        assert not chan.is_single_active_consumer('q')
        assert chan.get_sac_status('q') is None
        assert chan.get_active_consumer('q') == 'b'
        assert chan.get_standby_consumers('q') == []

    def test_cancel_promotes_highest_priority_standby(self, chan):
        chan.queue_declare('q', arguments=SAC)
        on_cancel = Mock()
        consume(chan, 'q', 'a', 0, on_cancel=on_cancel)
        consume(chan, 'q', 'b', 1)
        consume(chan, 'q', 'c', 3)
        # higher-priority newcomers demote the active consumer.
        assert chan.get_active_consumer('q') == 'c'
        chan.basic_cancel('c')
        assert chan.get_active_consumer('q') == 'b'
        chan.basic_cancel('b')
        assert chan.get_active_consumer('q') == 'a'
        assert chan.consumer_events('q', 'promoted')[-1]['consumer_tag'] == 'a'

    def test_demotion_notifies(self, chan):
        chan.queue_declare('q', arguments=SAC)
        on_cancel = Mock()
        consume(chan, 'q', 'low', 1, on_cancel=on_cancel)
        consume(chan, 'q', 'same', 1)
        on_cancel.assert_not_called()
        consume(chan, 'q', 'high', 5)
        on_cancel.assert_called_once_with('low')
        assert chan.get_active_consumer('q') == 'high'
        assert chan.get_standby_consumers('q') == ['low', 'same']
        types = [e['type'] for e in chan.consumer_events('q')]
        assert types == [
            'registered', 'activated', 'registered',
            'registered', 'demoted', 'activated',
        ]

    def test_channel_close_promotes_on_other_channel(self, conn):
        c1, c2 = conn.channel(), conn.channel()
        c1.queue_declare('q', arguments=SAC)
        on_cancel = Mock()
        consume(c1, 'q', 'a', on_cancel=on_cancel)
        consume(c2, 'q', 'b')
        assert c2.get_active_consumer('q') == 'a'
        c1.close()
        on_cancel.assert_called_once_with('a')
        assert c2.get_active_consumer('q') == 'b'
        assert c2.get_sac_status('q')['standby'] == []

    def test_promote_consumer(self, chan):
        chan.queue_declare('q', arguments=SAC)
        on_cancel = Mock()
        consume(chan, 'q', 'a', on_cancel=on_cancel)
        consume(chan, 'q', 'b')
        assert not chan.promote_consumer('q', 'a')
        assert chan.promote_consumer('q', 'b')
        assert chan.get_active_consumer('q') == 'b'
        on_cancel.assert_called_once_with('a')
        chan.queue_declare('plain')
        consume(chan, 'plain', 'c')
        assert not chan.promote_consumer('plain', 'c')

    def test_sac_delivery_only_to_active(self, conn):
        c1, c2 = conn.channel(), conn.channel()
        c1.queue_declare('q', arguments=SAC)
        cb1, cb2 = Mock(), Mock()
        consume(c1, 'q', 'a', callback=cb1)
        consume(c2, 'q', 'b', callback=cb2)
        for i in range(3):
            c1.basic_publish(c1.prepare_message(str(i)), '', 'q')
        for _ in range(3):
            conn.drain_events(timeout=0.1)
        assert cb1.call_count == 3
        cb2.assert_not_called()
        c1.basic_cancel('a')
        c1.basic_publish(c1.prepare_message('x'), '', 'q')
        conn.drain_events(timeout=0.1)
        assert cb2.call_count == 1


class test_ConsumerPriority:

    def test_ordering_and_introspection(self, chan):
        chan.queue_declare('q')
        consume(chan, 'q', 'a', 1)
        consume(chan, 'q', 'b', 5)
        consume(chan, 'q', 'c', 1)
        consume(chan, 'q', 'd')
        info = chan.consumer_info('q')
        assert [i['consumer_tag'] for i in info] == ['b', 'a', 'c', 'd']
        assert set(info[0]) == {'queue', 'consumer_tag', 'priority',
                                'is_active'}
        assert chan.get_consumer_count('q') == 4
        assert chan.get_consumer_count() == 4
        assert chan.get_consumer_priority('b') == 5
        assert chan.get_consumer_priority('d') == 0
        assert chan.get_consumer_priority('nope') is None
        assert chan.consumer_priority_map('q') == {
            'a': 1, 'b': 5, 'c': 1, 'd': 0}
        assert chan.consumer_tags == ['a', 'b', 'c', 'd']
        assert len(chan.list_consumers()) == 4
        snap = chan.consumer_registry_snapshot()
        assert snap['q'][0] == {
            'consumer_tag': 'b', 'priority': 5, 'is_active': True}

    def test_delivery_prefers_priority_then_falls_back(self, conn):
        low, high = conn.channel(), conn.channel()
        low.queue_declare('q')
        cb_low, cb_high = Mock(), Mock()
        high.basic_qos(prefetch_count=1)
        low.basic_consume('q', False, cb_low, 'low', arguments=prio(1))
        high.basic_consume('q', False, cb_high, 'high', arguments=prio(9))
        for i in range(2):
            low.basic_publish(low.prepare_message(str(i)), '', 'q')
        conn.drain_events(timeout=0.1)
        assert cb_high.call_count == 1
        conn.drain_events(timeout=0.1)
        assert cb_low.call_count == 1
        with pytest.raises(socket.timeout):
            conn.drain_events(timeout=0.1)


class test_CancelNotifications:

    def test_basic_cancel_swallows_errors(self, chan):
        chan.queue_declare('q')
        on_cancel = Mock(side_effect=RuntimeError())
        consume(chan, 'q', 'a', on_cancel=on_cancel)
        chan.basic_cancel('a')
        on_cancel.assert_called_once_with('a')
        assert chan.get_consumer_count('q') == 0

    def test_queue_delete_notifies_all(self, chan):
        chan.queue_declare('q', arguments=SAC)
        cbs = Mock(), Mock()
        consume(chan, 'q', 'a', on_cancel=cbs[0])
        consume(chan, 'q', 'b', on_cancel=cbs[1])
        chan.queue_delete('q')
        cbs[0].assert_called_once_with('a')
        cbs[1].assert_called_once_with('b')
        assert chan.get_consumer_count('q') == 0
        assert not chan.is_single_active_consumer('q')
        assert chan.consumer_tags == []

    def test_events_and_clear(self, chan):
        chan.queue_declare('q')
        consume(chan, 'q', 'a', 2)
        chan.basic_cancel('a')
        events = chan.consumer_events(event_type='cancelled')
        assert len(events) == 1
        assert set(events[0]) == {'type', 'queue', 'consumer_tag',
                                  'priority', 'timestamp'}
        assert events[0]['priority'] == 2
        chan.clear_consumer_events()
        assert chan.consumer_events() == []

    def test_state_shared_across_channels(self, conn):
        c1, c2 = conn.channel(), conn.channel()
        c1.queue_declare('q')
        consume(c1, 'q', 'a')
        assert c2.get_consumer_count('q') == 1
        assert c2.list_consumers() == []

    def test_new_transport_clears_consumers(self, conn):
        chan = conn.channel()
        chan.queue_declare('q', arguments=SAC)
        consume(chan, 'q', 'a')
        with Connection('memory://') as other:
            other_chan = other.channel()
            assert other_chan.get_consumer_count('q') == 0
            assert not other_chan.is_single_active_consumer('q')


class test_ConsumerAndQueue:

    def test_queue_helpers(self):
        ex = Exchange('ex')
        q = Queue.with_consumer_priority('q', ex, priority=3)
        assert q.consumer_priority == 3
        assert not q.is_single_active_consumer
        assert Queue('x', ex).consumer_priority == 0
        q = Queue.with_single_active_consumer('q', ex)
        assert q.is_single_active_consumer
        assert q.durable
        q = Queue.with_priority_and_sac('q', ex, priority=7, durable=False)
        assert q.is_single_active_consumer
        assert q.consumer_priority == 7
        assert not q.durable

    def test_consumer_sac(self, conn):
        ex = Exchange('ex', 'direct')
        q = Queue.with_single_active_consumer('sacq', ex, routing_key='k')
        notified = []
        c1 = Consumer(conn.channel(), [q], on_cancel=notified.append)
        c2 = Consumer(conn.channel(), [q])
        high = Queue.with_priority_and_sac('sacq', ex, priority=5,
                                           routing_key='k')
        c3 = Consumer(conn.channel(), [high]).on_cancel_notify(Mock())
        assert len(c1.cancel_notify_callbacks) == 1
        assert c2.cancel_notify_callbacks == []
        c1.consume()
        c2.consume()
        assert c1.consuming_from_sac(q)
        assert c1.is_active_on('sacq')
        assert not c2.is_active_on(q)
        c3.consume()
        assert c3.is_active_on('sacq')
        assert notified == c1.active_consumer_tags
        c3.cancel()
        assert c1.is_active_on('sacq')

        received = []
        c1.register_callback(lambda body, m: received.append(body))
        Producer(conn.channel()).publish('hi', exchange=ex, routing_key='k')
        conn.drain_events(timeout=0.1)
        assert received == ['hi']
