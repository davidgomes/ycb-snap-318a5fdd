"""Single-active-consumer, priority, and cancel-notification behavior."""
from __future__ import annotations

from kombu import Connection, Consumer, Exchange, Queue
from kombu.transport import virtual


def _connection():
    return Connection(
        transport='memory',
        transport_options={'polling_interval': 0.01},
    )


def _publish(channel, queue, body):
    channel.basic_publish(channel.prepare_message(body), '', queue)


class test_single_active_consumer:

    def setup_method(self):
        self.conn = _connection()
        self.channel = self.conn.channel()
        self.queues = []

    def teardown_method(self):
        state = self.conn.transport.state
        for name in self.queues:
            state.disable_sac(name)
            state.queue_consumers.pop(name, None)
        state.clear_consumer_state()
        for channel in (self.channel, *getattr(self, '_channels', [])):
            qos = getattr(channel, '_qos', None)
            if qos is not None:
                qos._on_collect.cancel()

    def _queue(self, name, sac=False):
        self.queues.append(name)
        arguments = {'x-single-active-consumer': True} if sac else None
        self.channel.queue_declare(name, arguments=arguments)
        return name

    def _consume(self, channel, queue, tag, priority=0, on_cancel=None,
                 no_ack=True):
        received = []

        def callback(message):
            received.append(message)

        channel.basic_consume(
            queue, no_ack, callback, tag,
            arguments={'x-priority': priority},
            on_cancel=on_cancel,
        )
        return received

    def test_priority_order_and_equal_priority_keeps_registration_order(self):
        queue = self._queue('order')
        self._consume(self.channel, queue, 'low', priority=0)
        self._consume(self.channel, queue, 'high', priority=5)
        self._consume(self.channel, queue, 'mid', priority=0)
        self._consume(self.channel, queue, 'high-later', priority=5)

        tags = [info['consumer_tag'] for info in
                self.channel.consumer_info(queue)]
        assert tags == ['high', 'high-later', 'low', 'mid']
        assert self.channel.consumer_priority_map(queue) == {
            'high': 5, 'high-later': 5, 'low': 0, 'mid': 0,
        }
        assert self.channel.get_consumer_priority('mid') == 0
        assert self.channel.get_consumer_priority('missing') is None
        assert self.channel.get_active_consumer(queue) == 'high'
        assert [row['is_active'] for row in self.channel.consumer_info(queue)] == [
            True, False, False, False,
        ]

    def test_redeclare_does_not_clear_sac(self):
        queue = self._queue('sticky', sac=True)
        assert self.channel.is_single_active_consumer(queue)
        self.channel.queue_declare(queue)
        self.channel.queue_declare(queue, arguments={})
        self.channel.queue_declare(
            queue, arguments={'x-single-active-consumer': False},
        )
        assert self.channel.is_single_active_consumer(queue) is True
        self.channel.queue_delete(queue)
        self.channel.queue_declare(queue)
        assert self.channel.is_single_active_consumer(queue) is False

    def test_sac_only_active_receives_until_promoted(self):
        queue = self._queue('sac-deliver', sac=True)
        cancelled = []
        first = self._consume(
            self.channel, queue, 'first', priority=1,
            on_cancel=lambda tag: cancelled.append(tag),
        )
        second = self._consume(
            self.channel, queue, 'second', priority=1,
            on_cancel=lambda tag: cancelled.append(('eq', tag)),
        )
        assert cancelled == []
        assert self.channel.get_active_consumer(queue) == 'first'
        assert self.channel.get_standby_consumers(queue) == ['second']
        status = self.channel.get_sac_status(queue)
        assert status == {
            'queue': queue,
            'active': 'first',
            'standby': ['second'],
            'consumer_count': 2,
        }

        _publish(self.channel, queue, 'one')
        self.channel.drain_events(timeout=1)
        assert len(first) == 1
        assert second == []

        self.channel.basic_cancel('first')
        assert cancelled == ['first']
        assert self.channel.get_active_consumer(queue) == 'second'
        assert self.channel.get_standby_consumers(queue) == []

        _publish(self.channel, queue, 'two')
        self.channel.drain_events(timeout=1)
        assert len(second) == 1
        assert len(first) == 1

    def test_higher_priority_demotes_and_equal_does_not(self):
        queue = self._queue('sac-pri', sac=True)
        cancelled = []

        def on_cancel(tag):
            cancelled.append(tag)

        self._consume(self.channel, queue, 'low', priority=1, on_cancel=on_cancel)
        self._consume(self.channel, queue, 'same', priority=1, on_cancel=on_cancel)
        assert cancelled == []
        assert self.channel.get_active_consumer(queue) == 'low'

        def boom(tag):
            cancelled.append(tag)
            raise RuntimeError('demote failed')

        # Replace low's callback by cancelling and re-registering is unnecessary;
        # register a higher-priority consumer whose predecessor raises.
        self.channel.basic_cancel('low')
        cancelled.clear()
        # same was promoted
        assert self.channel.get_active_consumer(queue) == 'same'

        self._consume(
            self.channel, queue, 'low2', priority=1,
            on_cancel=boom,
        )
        assert self.channel.get_active_consumer(queue) == 'same'
        high = self._consume(
            self.channel, queue, 'high', priority=9, on_cancel=on_cancel,
        )
        assert 'same' in cancelled or any(
            item == 'same' for item in cancelled
        )
        # boom is on low2, who is not active, so it must not have run.
        assert cancelled == ['same']
        assert self.channel.get_active_consumer(queue) == 'high'
        assert self.channel.get_standby_consumers(queue) == ['same', 'low2']
        assert self.channel.get_consumer_count(queue) == 3

        _publish(self.channel, queue, 'x')
        self.channel.drain_events(timeout=1)
        assert len(high) == 1

    def test_on_cancel_exception_does_not_propagate(self):
        queue = self._queue('sac-exc', sac=True)

        def boom(tag):
            raise RuntimeError(tag)

        self._consume(self.channel, queue, 'a', on_cancel=boom)
        self.channel.basic_cancel('a')
        self._consume(self.channel, queue, 'b', on_cancel=boom)
        self._consume(self.channel, queue, 'c', priority=3, on_cancel=boom)
        assert self.channel.get_active_consumer(queue) == 'c'
        self.channel.queue_delete(queue)
        assert self.channel.get_consumer_count(queue) == 0
        assert self.channel.is_single_active_consumer(queue) is False

    def test_queue_delete_notifies_every_consumer(self):
        queue = self._queue('sac-del', sac=True)
        seen = []
        self._consume(
            self.channel, queue, 'a', priority=2,
            on_cancel=lambda tag: seen.append(tag),
        )
        self._consume(
            self.channel, queue, 'b', priority=1,
            on_cancel=lambda tag: seen.append(tag),
        )
        self.channel.queue_delete(queue)
        assert seen == ['a', 'b']
        assert self.channel.consumer_info(queue) == []
        assert queue not in self.channel.connection._callbacks

    def test_promote_consumer(self):
        queue = self._queue('sac-promote', sac=True)
        plain = self._queue('plain')
        cancelled = []
        high_messages = self._consume(
            self.channel, queue, 'high', priority=10,
            on_cancel=lambda tag: cancelled.append(tag),
        )
        low_messages = self._consume(
            self.channel, queue, 'low', priority=1,
            on_cancel=lambda tag: cancelled.append(tag),
        )
        self._consume(self.channel, plain, 'other', priority=0)

        assert self.channel.promote_consumer(plain, 'other') is False
        assert self.channel.promote_consumer(queue, 'high') is False
        assert self.channel.promote_consumer(queue, 'missing') is False
        assert self.channel.promote_consumer(queue, 'low') is True
        assert cancelled == []
        assert self.channel.get_active_consumer(queue) == 'low'
        assert self.channel.get_standby_consumers(queue) == ['high']
        assert self.channel.promote_consumer(queue, 'low') is False

        _publish(self.channel, queue, 'promoted')
        self.channel.drain_events(timeout=1)
        assert len(low_messages) == 1
        assert high_messages == []

    def test_manual_promote_then_higher_priority_takes_over(self):
        queue = self._queue('sac-repromote', sac=True)
        cancelled = []
        self._consume(
            self.channel, queue, 'high', priority=10,
            on_cancel=lambda tag: cancelled.append(tag),
        )
        self._consume(
            self.channel, queue, 'low', priority=1,
            on_cancel=lambda tag: cancelled.append(tag),
        )
        assert self.channel.promote_consumer(queue, 'low') is True
        self._consume(
            self.channel, queue, 'mid', priority=5,
            on_cancel=lambda tag: cancelled.append(tag),
        )
        assert cancelled == ['low']
        assert self.channel.get_active_consumer(queue) == 'mid'
        assert self.channel.get_standby_consumers(queue) == ['high', 'low']

    def test_channel_close_promotes_highest_standby(self):
        queue = self._queue('sac-close', sac=True)
        other = self.conn.channel()
        self._channels = [other]
        cancelled = []
        self._consume(
            self.channel, queue, 'active', priority=1,
            on_cancel=lambda tag: cancelled.append(tag),
        )
        self._consume(
            other, queue, 'standby-low', priority=0,
            on_cancel=lambda tag: cancelled.append(tag),
        )
        third = self.conn.channel()
        self._channels.append(third)
        self._consume(
            third, queue, 'standby-high', priority=4,
            on_cancel=lambda tag: cancelled.append(tag),
        )
        # priority 4 is higher than the active priority 1, so it takes over.
        assert self.channel.get_active_consumer(queue) == 'standby-high'
        assert 'active' in cancelled
        cancelled.clear()
        third.close()
        assert 'standby-high' in cancelled
        assert other.get_active_consumer(queue) == 'active'
        assert 'active' not in cancelled

    def test_events_and_snapshot(self):
        queue = self._queue('sac-events', sac=True)
        self._consume(self.channel, queue, 'a', priority=1)
        self._consume(self.channel, queue, 'b', priority=1)
        self._consume(self.channel, queue, 'c', priority=5)
        types = [event['type'] for event in self.channel.consumer_events(queue)]
        assert types == [
            'registered', 'activated',
            'registered',
            'registered', 'demoted', 'activated',
        ]
        for event in self.channel.consumer_events(queue):
            assert set(event) == {
                'type', 'queue', 'consumer_tag', 'priority', 'timestamp',
            }
            assert isinstance(event['timestamp'], float)
        assert [event['type'] for event in
                self.channel.consumer_events(queue, event_type='activated')] == [
            'activated', 'activated',
        ]
        self.channel.basic_cancel('c')
        promoted = self.channel.consumer_events(queue, event_type='promoted')
        assert [event['consumer_tag'] for event in promoted] == ['a']
        self.channel.clear_consumer_events()
        assert self.channel.consumer_events() == []

        self._consume(self.channel, queue, 'd', priority=0)
        snapshot = self.channel.consumer_registry_snapshot()
        assert [row['consumer_tag'] for row in snapshot[queue]] == ['a', 'b', 'd']
        assert snapshot[queue][0]['is_active'] is True
        assert self.channel.consumer_tags == ['a', 'b', 'd']

    def test_non_sac_priority_dispatch_and_prefetch(self):
        queue = self._queue('pri-dispatch')
        low_ch = self.conn.channel()
        mid_ch = self.conn.channel()
        self._channels = [low_ch, mid_ch]
        high = self._consume(self.channel, queue, 'high', priority=10, no_ack=False)
        low = self._consume(low_ch, queue, 'low', priority=1, no_ack=False)
        self.channel.basic_qos(prefetch_count=1)
        low_ch.basic_qos(prefetch_count=10)

        _publish(low_ch, queue, 'm1')
        low_ch.drain_events(timeout=1)
        assert len(high) == 1
        assert low == []

        _publish(low_ch, queue, 'm2')
        low_ch.drain_events(timeout=1)
        assert len(high) == 1
        assert len(low) == 1

        # Same priority keeps registration order when both can consume.
        other = self._queue('same-pri')
        first_ch = self.conn.channel()
        second_ch = self.conn.channel()
        self._channels.extend([first_ch, second_ch])
        first = self._consume(first_ch, other, 'first', priority=3, no_ack=True)
        second = self._consume(second_ch, other, 'second', priority=3, no_ack=True)
        _publish(second_ch, other, 'm3')
        second_ch.drain_events(timeout=1)
        assert len(first) == 1
        assert second == []
        assert mid_ch.get_sac_status(other) is None
        assert mid_ch.get_standby_consumers(other) == []

    def test_cancelling_standby_keeps_active_delivery(self):
        queue = self._queue('sac-standby-cancel', sac=True)
        cancelled = []
        active = self._consume(
            self.channel, queue, 'active', priority=1,
            on_cancel=lambda tag: cancelled.append(tag),
        )
        self._consume(
            self.channel, queue, 'standby', priority=1,
            on_cancel=lambda tag: cancelled.append(tag),
        )
        self.channel.basic_cancel('standby')
        assert cancelled == ['standby']
        assert self.channel.get_active_consumer(queue) == 'active'
        _publish(self.channel, queue, 'still-active')
        self.channel.drain_events(timeout=1)
        assert len(active) == 1

    def test_callbacks_dispatch_not_last_registered(self):
        queue = self._queue('dispatch')
        calls = []
        self.channel.basic_consume(
            queue, True, lambda message: calls.append('first'), 'first',
            arguments={'x-priority': 5},
        )
        self.channel.basic_consume(
            queue, True, lambda message: calls.append('second'), 'second',
            arguments={'x-priority': 1},
        )
        dispatcher = self.channel.connection._callbacks[queue]
        assert dispatcher.__name__ == '_dispatch'
        _publish(self.channel, queue, 'z')
        self.channel.drain_events(timeout=1)
        assert calls == ['first']

    def test_shared_state_across_channels_and_cleared_on_new_transport(self):
        queue = self._queue('shared', sac=True)
        other = self.conn.channel()
        self._channels = [other]
        self._consume(self.channel, queue, 'a', priority=1)
        self._consume(other, queue, 'b', priority=0)
        assert other.get_consumer_count() == 2
        assert other.get_consumer_count(queue) == 2
        assert len(self.channel.list_consumers()) == 1
        assert self.channel.list_consumers()[0]['consumer_tag'] == 'a'
        assert {row['consumer_tag'] for row in other.list_consumers()} == {'b'}
        assert other.consumer_events(queue)

        fresh = _connection()
        try:
            fresh_channel = fresh.channel()
            assert fresh_channel.get_consumer_count() == 0
            assert fresh_channel.consumer_events() == []
            # Queue SAC flag is broker state and survives the new connection.
            assert fresh_channel.is_single_active_consumer(queue) is True
        finally:
            fresh.close()

    def test_consumer_and_queue_helpers(self):
        exchange = Exchange('sac-ex', type='direct')
        plain = Queue('plain-q', exchange)
        assert plain.consumer_priority == 0
        assert plain.is_single_active_consumer is False

        prioritized = Queue.with_consumer_priority(
            'pri-q', exchange, priority=7, routing_key='pri-q',
            consumer_arguments={'x-extra': 1},
        )
        assert prioritized.consumer_priority == 7
        assert prioritized.consumer_arguments['x-extra'] == 1
        assert prioritized.consumer_arguments['x-priority'] == 7

        sac = Queue.with_single_active_consumer(
            'sac-q', exchange, routing_key='sac-q',
            queue_arguments={'x-message-ttl': 5},
        )
        assert sac.durable is True
        assert sac.is_single_active_consumer is True
        assert sac.queue_arguments['x-message-ttl'] == 5

        both = Queue.with_priority_and_sac(
            'both-q', 'ex', priority=4, routing_key='both-q',
        )
        assert both.is_single_active_consumer is True
        assert both.consumer_priority == 4
        assert both.durable is True

        seen = []
        holder = {}

        def on_message(body, message):
            holder['body'] = body

        consumer = Consumer(
            self.channel,
            sac,
            callbacks=[on_message],
            on_cancel=lambda tag: seen.append(('init', tag)),
        )
        assert consumer.on_cancel_notify(
            lambda tag: seen.append(('extra', tag)),
        ) is consumer
        consumer.consume()
        assert consumer.consuming_from_sac(sac) is True
        assert consumer.consuming_from_sac('missing') is False
        assert consumer.is_active_on(sac) is True
        assert consumer.active_consumer_tags

        standby = Consumer(
            self.channel,
            Queue.with_priority_and_sac(
                'sac-q', exchange, priority=0, routing_key='sac-q',
            ),
            callbacks=[on_message],
        )
        # Queue is already SAC from the first declare. A second consumer with
        # equal default priority stays standby. The classmethod queue is the
        # same name, so declare is a redeclare and keeps SAC.
        standby.consume()
        assert standby.consuming_from_sac('sac-q') is True
        assert standby.is_active_on('sac-q') is False
        assert standby.active_consumer_tags == []

        tag = consumer.active_consumer_tags[0]
        consumer.cancel()
        assert ('init', tag) in seen
        assert ('extra', tag) in seen
        assert seen.count(('init', tag)) == 1


def test_broker_state_clear_resets_sac_and_consumers():
    state = virtual.BrokerState()
    state.enable_sac('q')
    state.queue_consumers['q'] = []
    state.consumers['t'] = {'queue': 'q'}
    state.consumer_events.append({'type': 'registered'})
    state.clear_consumer_state()
    assert state.is_sac('q') is True
    assert state.consumers == {}
    assert state.consumer_events == []
    state.clear()
    assert state.is_sac('q') is False
