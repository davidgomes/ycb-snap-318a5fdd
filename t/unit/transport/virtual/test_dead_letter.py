from __future__ import annotations

from time import time

from kombu import Connection, Exchange, Queue


class _Memory:
    """Isolated memory channel with a direct dead-letter exchange."""

    def __init__(self):
        self.connection = Connection('memory://')
        self.channel = self.connection.channel()
        self.names = []

    def close(self):
        for name in self.names:
            self.channel.queue_delete(name)
        self.channel.close()
        self.connection.close()

    def declare_exchange(self, name, type='direct'):
        self.channel.exchange_declare(name, type=type, durable=False)
        return name

    def declare_queue(self, name, exchange, routing_key, arguments=None):
        self.names.append(name)
        self.channel.queue_declare(name, arguments=arguments or {})
        self.channel.queue_bind(name, exchange, routing_key)
        return name

    def publish(self, exchange, routing_key, body, expiration=None):
        properties = {}
        if expiration is not None:
            properties['expiration'] = expiration
        message = self.channel.prepare_message(body, properties=properties)
        self.channel.basic_publish(message, exchange, routing_key)
        return message

    def raw_bodies(self, queue):
        bodies = []
        for item in list(self.channel._queue_for(queue).queue):
            bodies.append(self.channel.message_to_python(item).body)
        return bodies


def test_broker_state_queue_properties_roundtrip_and_clear():
    connection = Connection(transport='kombu.transport.virtual:Transport')
    state = connection.transport.state
    state.queue_properties_set('orders', dead_letter_exchange='dlx', message_ttl=1500)
    assert state.queue_properties_get('orders') == {
        'dead_letter_exchange': 'dlx',
        'message_ttl': 1500,
    }
    assert state.queue_properties_get('missing') == {}

    # A second set replaces the previous mapping.
    state.queue_properties_set('orders', max_length=3)
    assert state.queue_properties_get('orders') == {'max_length': 3}
    mutated = state.queue_properties_get('orders')
    mutated['max_length'] = 9
    assert state.queue_properties_get('orders') == {'max_length': 3}

    state.queue_properties_delete('orders')
    assert state.queue_properties_get('orders') == {}

    state.queue_properties_set('orders', message_ttl=5)
    state.clear()
    assert state.queue_properties_get('orders') == {}


def test_declare_stores_replaces_and_drops_queue_properties():
    harness = _Memory()
    try:
        harness.declare_exchange('ex')
        harness.declare_queue('src', 'ex', 'src', arguments={
            'x-dead-letter-exchange': 'dlx',
            'x-dead-letter-routing-key': 'parked',
            'x-message-ttl': 2500,
            'x-max-length': 4,
            'x-max-length-bytes': 100,
            'x-expires': 60000,
            'x-max-priority': 9,
            'x-queue-type': 'classic',
            'ignored': 'nope',
        })
        assert harness.channel.get_queue_properties('src') == {
            'dead_letter_exchange': 'dlx',
            'dead_letter_routing_key': 'parked',
            'message_ttl': 2500,
            'max_length': 4,
            'max_length_bytes': 100,
            'expires': 60000,
            'max_priority': 9,
            'queue_type': 'classic',
        }
        assert harness.channel.queue_properties_for_declare('src') == {
            'x-dead-letter-exchange': 'dlx',
            'x-dead-letter-routing-key': 'parked',
            'x-message-ttl': 2500,
            'x-max-length': 4,
            'x-max-length-bytes': 100,
            'x-expires': 60000,
            'x-max-priority': 9,
            'x-queue-type': 'classic',
        }

        harness.channel.queue_declare('src', arguments={
            'x-dead-letter-exchange': 'other',
        })
        assert harness.channel.get_queue_properties('src') == {
            'dead_letter_exchange': 'other',
        }

        harness.channel.queue_delete('src')
        assert harness.channel.get_queue_properties('src') == {}
    finally:
        harness.close()


def test_prepare_queue_arguments_and_per_message_expiration():
    harness = _Memory()
    try:
        prepared = harness.channel.prepare_queue_arguments(
            {'x-queue-mode': 'lazy'},
            dead_letter_exchange='dlx',
            dead_letter_routing_key='rk',
            message_ttl=1.5,
            expires=2,
            max_length=10,
            max_length_bytes=64,
            max_priority=3,
        )
        assert prepared == {
            'x-queue-mode': 'lazy',
            'x-dead-letter-exchange': 'dlx',
            'x-dead-letter-routing-key': 'rk',
            'x-message-ttl': 1500,
            'x-expires': 2000,
            'x-max-length': 10,
            'x-max-length-bytes': 64,
            'x-max-priority': 3,
        }

        before = time()
        message = harness.channel.prepare_message(
            'body', properties={'expiration': '2500'},
        )
        after = time()
        expires_at = message['properties']['x-expires-at']
        assert before + 2.5 <= expires_at <= after + 2.5
        remaining = harness.channel.message_ttl_remaining(message)
        assert remaining is not None
        assert 2.4 < remaining <= 2.5

        unset = harness.channel.prepare_message('body')
        assert harness.channel.message_ttl_remaining(unset) is None
        message['properties']['x-expires-at'] = time() - 5
        assert harness.channel.message_ttl_remaining(message) < 0
    finally:
        harness.close()


def test_queue_ttl_is_per_destination_and_yields_to_message_expiration():
    harness = _Memory()
    try:
        harness.declare_exchange('ex')
        harness.declare_queue('fast', 'ex', 'rk', arguments={'x-message-ttl': 1000})
        harness.declare_queue('slow', 'ex', 'rk', arguments={'x-message-ttl': 5000})
        harness.publish('ex', 'rk', 'queued')

        fast = harness.channel._queue_for('fast').queue[0]
        slow = harness.channel._queue_for('slow').queue[0]
        assert fast is not slow
        assert fast['properties']['expiration'] == '1000'
        assert slow['properties']['expiration'] == '5000'
        delta = slow['properties']['x-expires-at'] - fast['properties']['x-expires-at']
        assert 3.5 < delta < 4.5

        harness.publish('ex', 'rk', 'override', expiration='1500')
        overridden = harness.channel._queue_for('fast').queue[-1]
        remaining = harness.channel.message_ttl_remaining(overridden)
        assert overridden['properties']['expiration'] == '1500'
        assert 1.3 < remaining < 1.6
        slow_override = harness.channel._queue_for('slow').queue[-1]
        slow_remaining = harness.channel.message_ttl_remaining(slow_override)
        assert 1.3 < slow_remaining < 1.6
    finally:
        harness.close()


def test_max_length_dead_letters_oldest_messages():
    harness = _Memory()
    try:
        harness.declare_exchange('ex')
        harness.declare_exchange('dlx')
        harness.declare_queue('src', 'ex', 'src', arguments={
            'x-max-length': 2,
            'x-dead-letter-exchange': 'dlx',
            'x-dead-letter-routing-key': 'overflow',
        })
        harness.declare_queue('overflow', 'dlx', 'overflow')

        for body in ('a', 'b', 'c'):
            harness.publish('ex', 'src', body)

        assert harness.raw_bodies('src') == [b'b', b'c']
        dead = harness.channel._queue_for('overflow').queue[0]
        assert harness.channel.message_to_python(dead).body == b'a'
        assert dead['properties'].get('expiration') is None
        assert dead['properties'].get('x-expires-at') is None
        assert dead['properties']['delivery_info']['exchange'] == 'dlx'
        assert dead['properties']['delivery_info']['routing_key'] == 'overflow'
        death = dead['headers']['x-death']
        assert death == [{
            'queue': 'src',
            'reason': 'maxlen',
            'exchange': 'ex',
            'routing-key': 'src',
            'count': 1,
            'time': death[0]['time'],
        }]
        assert isinstance(death[0]['count'], int)
        assert isinstance(death[0]['time'], float)
        assert dead['headers']['x-first-death-reason'] == 'maxlen'
        assert dead['headers']['x-first-death-queue'] == 'src'
        assert dead['headers']['x-first-death-exchange'] == 'ex'
    finally:
        harness.close()


def test_expired_messages_are_skipped_drained_and_dead_lettered():
    harness = _Memory()
    try:
        harness.declare_exchange('ex')
        harness.declare_exchange('dlx')
        harness.declare_queue('src', 'ex', 'src', arguments={
            'x-dead-letter-exchange': 'dlx',
        })
        harness.declare_queue('parked', 'dlx', 'src')

        stale = harness.channel.prepare_message('stale', properties={'expiration': '5000'})
        fresh = harness.channel.prepare_message('fresh', properties={'expiration': '5000'})
        later = harness.channel.prepare_message('later', properties={'expiration': '5000'})
        for message in (stale, fresh, later):
            harness.channel._inplace_augment_message(message, 'ex', 'src')
        stale['properties']['x-expires-at'] = time() - 10
        later['properties']['x-expires-at'] = time() - 1
        # Bypass put() so the absolute expiry is preserved.
        harness.channel._put('src', stale)
        harness.channel._put('src', fresh)
        harness.channel._put('src', later)

        got = harness.channel.basic_get('src')
        assert got is not None
        assert got.body == b'fresh'
        assert got.delivery_info['queue'] == 'src'
        assert harness.channel._size('src') == 1
        got.ack()

        assert harness.channel.drain_expired('src') == 1
        assert harness.raw_bodies('src') == []
        assert harness.channel.expire_messages('src') == 0
        assert harness.channel.basic_get('src') is None

        dead_bodies = harness.raw_bodies('parked')
        assert dead_bodies == [b'stale', b'later']
        for raw in harness.channel._queue_for('parked').queue:
            assert raw['headers']['x-death'][0]['reason'] == 'expired'
            assert raw['properties']['delivery_info']['exchange'] == 'dlx'
            assert raw['properties']['delivery_info']['routing_key'] == 'src'
            assert 'expiration' not in raw['properties']
            assert 'x-expires-at' not in raw['properties']
    finally:
        harness.close()


def test_basic_get_returns_none_when_every_message_is_expired():
    harness = _Memory()
    try:
        harness.declare_exchange('ex')
        harness.declare_queue('src', 'ex', 'src')
        message = harness.channel.prepare_message('gone', properties={'expiration': '1'})
        harness.channel._inplace_augment_message(message, 'ex', 'src')
        message['properties']['x-expires-at'] = time() - 1
        harness.channel._put('src', message)
        assert harness.channel.basic_get('src') is None
        assert harness.channel._size('src') == 0
    finally:
        harness.close()


def test_basic_consume_stamps_queue_and_dead_letter_headers_accumulate():
    harness = _Memory()
    try:
        harness.declare_exchange('ex')
        harness.declare_exchange('dlx')
        harness.declare_queue('src', 'ex', 'orders', arguments={
            'x-dead-letter-exchange': 'dlx',
        })
        harness.declare_queue('again', 'dlx', 'orders', arguments={
            'x-dead-letter-exchange': 'dlx',
            'x-dead-letter-routing-key': 'final',
        })
        harness.declare_queue('final', 'dlx', 'final')
        harness.publish('ex', 'orders', 'payload')

        received = []
        harness.channel.basic_consume(
            'src', no_ack=False, callback=received.append, consumer_tag='ctag',
        )
        harness.channel.drain_events()
        message = received[0]
        assert message.delivery_info['queue'] == 'src'
        tag = message.delivery_tag
        assert harness.channel.qos.redelivery_count(tag) == 0
        assert harness.channel.qos.redelivery_count('missing') == 0
        message.reject(requeue=False)

        second = harness.channel.basic_get('again')
        assert second is not None
        assert second.delivery_info['queue'] == 'again'
        assert second.delivery_info['routing_key'] == 'orders'
        assert harness.channel.qos.redelivery_count(second.delivery_tag) == 1
        first_death = dict(second.headers['x-death'][0])
        assert first_death['queue'] == 'src'
        assert first_death['reason'] == 'rejected'
        assert first_death['count'] == 1
        assert second.headers['x-first-death-reason'] == 'rejected'
        assert second.headers['x-first-death-queue'] == 'src'
        assert second.headers['x-first-death-exchange'] == 'ex'
        second.reject(requeue=False)

        third = harness.channel.basic_get('final')
        assert third is not None
        assert third.delivery_info['routing_key'] == 'final'
        deaths = third.headers['x-death']
        assert [item['queue'] for item in deaths] == ['src', 'again']
        assert [item['reason'] for item in deaths] == ['rejected', 'rejected']
        assert third.headers['x-first-death-queue'] == 'src'
        assert third.headers['x-first-death-reason'] == 'rejected'
        assert third.headers['x-first-death-exchange'] == 'ex'
        assert harness.channel.qos.redelivery_count(third.delivery_tag) == 2
    finally:
        harness.close()


def test_reject_requeue_restores_without_dead_lettering():
    harness = _Memory()
    try:
        harness.declare_exchange('ex')
        harness.declare_exchange('dlx')
        harness.declare_queue('src', 'ex', 'src', arguments={
            'x-dead-letter-exchange': 'dlx',
        })
        harness.declare_queue('parked', 'dlx', 'src')
        harness.publish('ex', 'src', 'keep')
        message = harness.channel.basic_get('src')
        message.reject(requeue=True)
        restored = harness.channel.basic_get('src')
        assert restored is not None
        assert restored.body == b'keep'
        assert harness.raw_bodies('parked') == []
    finally:
        harness.close()


def test_cycle_detection_missing_exchange_and_hop_limit():
    harness = _Memory()
    try:
        harness.declare_exchange('ex')
        harness.declare_exchange('dlx')
        harness.declare_queue('src', 'ex', 'src', arguments={
            'x-max-length': 1,
            'x-dead-letter-exchange': 'dlx',
            'x-dead-letter-routing-key': 'src',
        })
        # DLX is bound back to the source queue: the evicted message must
        # not be delivered there a second time.
        harness.channel.queue_bind('src', 'dlx', 'src')
        harness.publish('ex', 'src', 'first')
        harness.publish('ex', 'src', 'second')
        assert harness.raw_bodies('src') == [b'second']

        harness.declare_queue('orphan', 'ex', 'orphan', arguments={
            'x-dead-letter-exchange': 'missing-exchange',
        })
        harness.publish('ex', 'orphan', 'dropped')
        message = harness.channel.basic_get('orphan')
        message.reject(requeue=False)
        assert harness.channel._size('orphan') == 0

        harness.channel.dead_letter_max_hops = 1
        harness.declare_queue('limited', 'ex', 'limited', arguments={
            'x-dead-letter-exchange': 'dlx',
            'x-dead-letter-routing-key': 'limited-dlq',
        })
        harness.declare_queue('limited-dlq', 'dlx', 'limited-dlq')
        first = harness.channel.prepare_message('hop')
        harness.channel._inplace_augment_message(first, 'ex', 'limited')
        harness.channel.dead_letter(first, 'limited', 'rejected')
        assert harness.channel._size('limited-dlq') == 1
        routed = harness.channel._queue_for('limited-dlq').get(block=False)
        harness.channel.dead_letter(routed, 'limited', 'rejected')
        assert harness.channel._size('limited-dlq') == 0
    finally:
        harness.close()


def test_same_queue_and_reason_increments_death_count():
    harness = _Memory()
    try:
        harness.declare_exchange('dlx')
        harness.declare_queue('src', 'dlx', 'unused', arguments={
            'x-dead-letter-exchange': 'dlx',
            'x-dead-letter-routing-key': 'counted',
        })
        harness.declare_queue('counted', 'dlx', 'counted')
        message = harness.channel.prepare_message('body')
        harness.channel._inplace_augment_message(message, 'ex', 'original')
        harness.channel.dead_letter(message, 'src', 'expired')
        routed = harness.channel._queue_for('counted').get(block=False)
        harness.channel.dead_letter(routed, 'src', 'expired')
        routed = harness.channel._queue_for('counted').get(block=False)
        death = routed['headers']['x-death']
        assert len(death) == 1
        assert death[0]['queue'] == 'src'
        assert death[0]['reason'] == 'expired'
        assert death[0]['count'] == 2
        assert routed['headers']['x-first-death-reason'] == 'expired'

        harness.channel.dead_letter(routed, 'src', 'rejected')
        routed = harness.channel._queue_for('counted').get(block=False)
        assert [item['reason'] for item in routed['headers']['x-death']] == [
            'expired', 'rejected',
        ]
        assert routed['headers']['x-first-death-reason'] == 'expired'
    finally:
        harness.close()


def test_queue_entity_dead_letter_helpers_and_declare():
    queue = Queue.with_dead_letter(
        'work', 'dlx', dead_letter_routing_key='parked',
        exchange=Exchange('work'), routing_key='work',
        message_ttl=12.5, max_length=7,
    )
    assert queue.dead_letter_exchange == 'dlx'
    assert queue.dead_letter_routing_key == 'parked'
    assert queue.has_dead_letter_exchange
    assert queue.effective_dead_letter_exchange == 'dlx'
    assert queue.effective_dead_letter_routing_key == 'parked'
    assert queue.effective_message_ttl == 12.5

    from_arguments = Queue(
        'from-args',
        exchange=Exchange('ex'),
        routing_key='rk',
        queue_arguments={
            'x-dead-letter-exchange': 'dlx-args',
            'x-dead-letter-routing-key': 'args-key',
            'x-message-ttl': 2500,
        },
    )
    assert from_arguments.has_dead_letter_exchange
    assert from_arguments.effective_dead_letter_exchange == 'dlx-args'
    assert from_arguments.effective_dead_letter_routing_key == 'args-key'
    assert from_arguments.effective_message_ttl == 2.5

    fallback = Queue('plain', exchange=Exchange('ex'), routing_key='mine')
    assert not fallback.has_dead_letter_exchange
    assert fallback.effective_dead_letter_exchange is None
    assert fallback.effective_dead_letter_routing_key == 'mine'
    assert fallback.effective_message_ttl is None

    loaded = Queue.from_dict(
        'loaded',
        exchange='ex',
        routing_key='rk',
        dead_letter_exchange='from-dict',
        dead_letter_routing_key='from-key',
    )
    assert loaded.dead_letter_exchange == 'from-dict'
    assert loaded.dead_letter_routing_key == 'from-key'
    assert loaded.effective_dead_letter_exchange == 'from-dict'

    connection = Connection('memory://')
    channel = connection.channel()
    try:
        queue(channel).declare()
        props = channel.get_queue_properties('work')
        assert props['dead_letter_exchange'] == 'dlx'
        assert props['dead_letter_routing_key'] == 'parked'
        assert props['message_ttl'] == 12500
        assert props['max_length'] == 7
    finally:
        channel.queue_delete('work')
        channel.close()
        connection.close()


def test_topic_exchange_applies_queue_ttl():
    harness = _Memory()
    try:
        harness.declare_exchange('topics', type='topic')
        harness.declare_queue(
            'us', 'topics', 'stock.us.*', arguments={'x-message-ttl': 2000},
        )
        harness.publish('topics', 'stock.us.nasdaq', 'quote')
        message = harness.channel._queue_for('us').queue[0]
        assert message['properties']['expiration'] == '2000'
        assert message['properties']['x-expires-at'] > time()
    finally:
        harness.close()


def test_no_dead_letter_exchange_discards_rejected_message():
    harness = _Memory()
    try:
        harness.declare_exchange('ex')
        harness.declare_queue('src', 'ex', 'src')
        harness.publish('ex', 'src', 'bye')
        message = harness.channel.basic_get('src')
        message.reject(requeue=False)
        assert harness.channel.basic_get('src') is None
        assert harness.channel._size('src') == 0
    finally:
        harness.close()
