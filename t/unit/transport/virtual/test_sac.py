"""Single-active-consumer and consumer priority behavior."""
from __future__ import annotations

from kombu import Connection, Exchange, Queue
from kombu.transport import filesystem, memory, pyro


class test_virtual_consumer_priority:

    def setup_method(self):
        self.connection = Connection(transport='memory')
        self.connection.transport.polling_interval = 0.01
        self.channel = self.connection.channel()
        self.exchange = 'sac-exchange'
        self.queue = 'sac-queue'
        self.channel.exchange_declare(self.exchange)
        self.channel.queue_declare(
            self.queue,
            arguments={'x-single-active-consumer': True},
        )
        self.channel.queue_bind(self.queue, self.exchange, self.queue)

    def _consume(self, channel, tag, priority=0, on_cancel=None, queue=None):
        received = []

        def callback(message):
            received.append(message.body)

        channel.basic_consume(
            queue or self.queue,
            False,
            callback,
            tag,
            arguments={'x-priority': priority},
            on_cancel=on_cancel,
        )
        return received

    def _publish(self, body, queue=None):
        queue = queue or self.queue
        exchange = self.exchange if queue == self.queue else queue
        message = self.channel.prepare_message(body)
        self.channel.basic_publish(message, exchange, queue)

    def test_sac_sticks_when_redeclared_without_argument(self):
        assert self.channel.is_single_active_consumer(self.queue) is True
        self.channel.queue_declare(self.queue)
        assert self.channel.is_single_active_consumer(self.queue) is True
        plain = 'plain-queue'
        self.channel.queue_declare(plain)
        assert self.channel.is_single_active_consumer(plain) is False
        assert self.channel.get_sac_status(plain) is None

    def test_priority_order_and_equal_priority_does_not_demote(self):
        cancelled = []
        self._consume(
            self.channel, 'low', priority=1,
            on_cancel=lambda tag: cancelled.append(tag),
        )
        other = self.connection.channel()
        self._consume(
            other, 'same', priority=1,
            on_cancel=lambda tag: cancelled.append(tag),
        )
        assert self.channel.get_active_consumer(self.queue) == 'low'
        assert cancelled == []
        assert self.channel.get_standby_consumers(self.queue) == ['same']

        self._consume(
            other, 'high', priority=9,
            on_cancel=lambda tag: cancelled.append(('high', tag)),
        )
        assert cancelled == ['low']
        assert self.channel.get_active_consumer(self.queue) == 'high'
        assert self.channel.get_consumer_priority('high') == 9
        assert self.channel.get_consumer_priority('missing') is None
        assert self.channel.get_consumer_priority('low') == 1

        info = self.channel.consumer_info(self.queue)
        assert [item['consumer_tag'] for item in info] == [
            'high', 'low', 'same',
        ]
        assert [item['is_active'] for item in info] == [True, False, False]
        assert info[0]['queue'] == self.queue
        assert self.channel.consumer_priority_map(self.queue) == {
            'high': 9, 'low': 1, 'same': 1,
        }
        assert self.channel.get_consumer_count(self.queue) == 3
        assert self.channel.get_consumer_count() == 3

        status = self.channel.get_sac_status(self.queue)
        assert status['queue'] == self.queue
        assert status['active'] == 'high'
        assert status['standby'] == ['low', 'same']
        assert status['consumer_count'] == 3

        snapshot = self.channel.consumer_registry_snapshot()
        assert snapshot[self.queue][0]['consumer_tag'] == 'high'
        assert snapshot[self.queue][0]['is_active'] is True

        types = [
            event['type']
            for event in self.channel.consumer_events(self.queue)
        ]
        assert types == [
            'registered', 'activated',
            'registered',
            'registered', 'demoted', 'activated',
        ]
        promoted = self.channel.consumer_events(event_type='promoted')
        assert promoted == []
        for event in self.channel.consumer_events():
            assert isinstance(event['timestamp'], float)
            assert set(event) == {
                'type', 'queue', 'consumer_tag', 'priority', 'timestamp',
            }

    def test_cancel_promotes_highest_standby_and_swallows_errors(self):
        def boom(tag):
            raise RuntimeError(tag)

        self._consume(self.channel, 'low', priority=1, on_cancel=boom)
        other = self.connection.channel()
        self._consume(other, 'mid', priority=5, on_cancel=boom)
        third = self.connection.channel()
        notified = []
        self._consume(
            third, 'high', priority=8,
            on_cancel=lambda tag: notified.append(tag),
        )
        assert self.channel.get_active_consumer(self.queue) == 'high'

        self._publish('one')
        self.connection.drain_events(timeout=1)
        # message consumed by the active consumer; publish another after cancel
        assert third.basic_cancel('high') is None
        assert notified == ['high']
        assert self.channel.get_active_consumer(self.queue) == 'mid'
        assert [
            event['type']
            for event in self.channel.consumer_events(event_type='promoted')
        ] == ['promoted']

        assert self.channel.promote_consumer(self.queue, 'low') is True
        assert self.channel.get_active_consumer(self.queue) == 'low'
        assert self.channel.promote_consumer(self.queue, 'low') is False
        assert self.channel.promote_consumer(self.queue, 'mid') is True
        plain = 'not-sac'
        self.channel.queue_declare(plain)
        self._consume(self.channel, 'plain-c', queue=plain)
        assert self.channel.promote_consumer(plain, 'plain-c') is False

    def test_delivery_follows_active_consumer_and_priority_qos(self):
        low_messages = self._consume(self.channel, 'low', priority=1)
        high_channel = self.connection.channel()
        high_messages = self._consume(high_channel, 'high', priority=5)
        self._publish('only-high')
        self.connection.drain_events(timeout=1)
        assert high_messages == [b'only-high']
        assert low_messages == []

        high_channel.basic_cancel('high')
        self._publish('now-low')
        self.connection.drain_events(timeout=1)
        assert low_messages == [b'now-low']

    def test_non_sac_falls_through_when_prefetch_is_full(self):
        name = 'priority-qos'
        for channel in (self.channel,):
            channel.exchange_declare(name)
            channel.queue_declare(name)
            channel.queue_bind(name, name, name)
        high = self.connection.channel()
        low = self.connection.channel()
        high.exchange_declare(name)
        low.exchange_declare(name)
        high_messages = self._consume(high, 'q-high', priority=10, queue=name)
        low_messages = self._consume(low, 'q-low', priority=1, queue=name)
        high.basic_qos(prefetch_count=1)
        low.basic_qos(prefetch_count=10)

        message_a = high.prepare_message('a')
        message_b = high.prepare_message('b')
        high.basic_publish(message_a, name, name)
        high.basic_publish(message_b, name, name)
        self.connection.drain_events(timeout=1)
        self.connection.drain_events(timeout=1)
        assert high_messages == [b'a']
        assert low_messages == [b'b']
        assert high.get_active_consumer(name) == 'q-high'
        assert low.list_consumers()[0]['consumer_tag'] == 'q-low'
        assert 'q-high' not in low.consumer_tags
        assert low.consumer_tags == ['q-low']

    def test_same_channel_non_sac_prefers_highest_priority_callback(self):
        name = 'same-channel-priority'
        self.channel.exchange_declare(name)
        self.channel.queue_declare(name)
        self.channel.queue_bind(name, name, name)
        low_messages = self._consume(
            self.channel, 'c-low', priority=0, queue=name,
        )
        high_messages = self._consume(
            self.channel, 'c-high', priority=3, queue=name,
        )
        self._publish('m1', queue=name)
        self._publish('m2', queue=name)
        self.channel.drain_events()
        self.channel.drain_events()
        assert high_messages == [b'm1', b'm2']
        assert low_messages == []
        assert self.channel.consumer_tags == ['c-high', 'c-low']

    def test_queue_delete_and_close_notify_every_consumer(self):
        cancelled = []

        def record(tag):
            cancelled.append(tag)

        self._consume(self.channel, 'a', priority=1, on_cancel=record)
        other = self.connection.channel()
        self._consume(other, 'b', priority=2, on_cancel=record)
        # Registering the higher-priority consumer demotes ``a``.
        cancelled.clear()
        self.channel.queue_delete(self.queue)
        assert sorted(cancelled) == ['a', 'b']
        assert self.channel.get_consumer_count(self.queue) == 0
        assert self.channel.is_single_active_consumer(self.queue) is False
        types = [
            event['type'] for event in self.channel.consumer_events(self.queue)
        ]
        assert types.count('cancelled') == 2

        self.channel.clear_consumer_events()
        assert self.channel.consumer_events() == []

        self.channel.queue_declare(
            self.queue, arguments={'x-single-active-consumer': True},
        )
        cancelled.clear()
        self._consume(self.channel, 'c1', priority=1, on_cancel=record)
        self._consume(other, 'c2', priority=4, on_cancel=record)
        other.close()
        assert 'c2' in cancelled
        assert self.channel.get_active_consumer(self.queue) == 'c1'

    def test_global_state_clears_consumer_registrations(self):
        self._consume(self.channel, 'linger', priority=1)
        assert self.channel.get_consumer_count() == 1
        again = Connection(transport='memory')
        assert again.transport.state is self.connection.transport.state
        assert again.channel().get_consumer_count() == 0
        again.channel().queue_declare(self.queue)
        assert again.channel().is_single_active_consumer(self.queue) is True

        for module in (filesystem, pyro):
            first = Connection(transport=module.Transport)
            state = first.transport.state
            state.sac_queues.add('global-sac')
            state.consumers['global-sac'] = [{
                'consumer_tag': 'tag',
                'priority': 2,
                'channel': object(),
            }]
            state.consumers_by_tag['tag'] = state.consumers['global-sac'][0]
            state.active_consumers['global-sac'] = 'tag'
            state.consumer_events.append({'type': 'registered'})
            assert len(state.consumers_by_tag) == 1
            second = Connection(transport=module.Transport)
            assert second.transport.state is state
            assert len(second.transport.state.consumers_by_tag) == 0
            assert second.transport.state.consumer_events == []
            assert 'global-sac' in second.transport.state.sac_queues

        assert memory.Transport.global_state is self.connection.transport.state
