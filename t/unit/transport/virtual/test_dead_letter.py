"""Dead-letter, TTL, and max-length behavior for the virtual transport."""
from __future__ import annotations

from queue import Empty
from time import time

import pytest

from kombu import Connection, Exchange, Queue
from kombu.transport import virtual
from kombu.utils.uuid import uuid


def _raw(body, expires_at=None, expiration=None, exchange='', routing_key='rk',
         headers=None):
    props = {
        'delivery_tag': uuid(),
        'delivery_info': {'exchange': exchange, 'routing_key': routing_key},
        'priority': 0,
    }
    if expires_at is not None:
        props['x-expires-at'] = expires_at
    if expiration is not None:
        props['expiration'] = expiration
    return {
        'body': body,
        'headers': {} if headers is None else headers,
        'properties': props,
        'content-type': 'text/plain',
        'content-encoding': 'utf-8',
    }


class test_BrokerStateProperties:

    def test_set_get_replace_delete_and_clear(self):
        state = virtual.BrokerState()
        assert state.queue_properties_get('missing') == {}

        state.queue_properties_set('q', dead_letter_exchange='dlx', max_length=2)
        assert state.queue_properties_get('q') == {
            'dead_letter_exchange': 'dlx',
            'max_length': 2,
        }
        copied = state.queue_properties_get('q')
        copied['max_length'] = 9
        assert state.queue_properties_get('q')['max_length'] == 2

        state.queue_properties_set('q', message_ttl=1000)
        assert state.queue_properties_get('q') == {'message_ttl': 1000}

        state.queue_properties_delete('q')
        assert state.queue_properties_get('q') == {}

        state.queue_properties_set('q', max_length=1)
        state.clear()
        assert state.queue_properties_get('q') == {}

    def test_deleting_bindings_deletes_properties(self):
        state = virtual.BrokerState()
        state.binding_declare('q', 'ex', 'rk', None)
        state.queue_properties_set('q', max_length=4)
        state.queue_bindings_delete('q')
        assert list(state.queue_bindings('q')) == []
        assert state.queue_properties_get('q') == {}


class test_VirtualDeadLetter:

    def setup_method(self):
        self.conn = Connection(transport='memory')
        self.channel = self.conn.channel()
        self.names = []

    def teardown_method(self):
        state = self.channel.state
        for name in list(self.names):
            self.channel.queues.pop(name, None)
            state.queue_properties_delete(name)
            state.queue_index.pop(name, None)
        for name in list(state.exchanges):
            if str(name).startswith('dlxtest-'):
                state.exchanges.pop(name, None)
        try:
            self.conn.close()
        except Exception:
            pass

    def _name(self, suffix):
        name = f'dlxtest-{suffix}-{uuid()[:8]}'
        self.names.append(name)
        return name

    def _declare_dl_pair(self, *, queue_args=None, dl_routing_key='dead'):
        ex = self._name('ex')
        dlx = self._name('dlx')
        src = self._name('src')
        dlq = self._name('dlq')
        self.channel.exchange_declare(ex, type='direct')
        self.channel.exchange_declare(dlx, type='direct')
        arguments = {'x-dead-letter-exchange': dlx}
        if dl_routing_key is not None:
            arguments['x-dead-letter-routing-key'] = dl_routing_key
        if queue_args:
            arguments.update(queue_args)
        self.channel.queue_declare(src, arguments=arguments)
        self.channel.queue_declare(dlq)
        self.channel.queue_bind(src, ex, 'rk')
        self.channel.queue_bind(dlq, dlx, dl_routing_key or 'rk')
        return ex, dlx, src, dlq

    def test_prepare_queue_arguments_and_roundtrip(self):
        prepared = self.channel.prepare_queue_arguments(
            {'x-custom': 'keep'},
            dead_letter_exchange='dlx',
            dead_letter_routing_key='rk',
            message_ttl=1.5,
            expires=2,
            max_length=10,
            max_length_bytes=20,
            max_priority=3,
        )
        assert prepared == {
            'x-custom': 'keep',
            'x-dead-letter-exchange': 'dlx',
            'x-dead-letter-routing-key': 'rk',
            'x-message-ttl': 1500,
            'x-expires': 2000,
            'x-max-length': 10,
            'x-max-length-bytes': 20,
            'x-max-priority': 3,
        }
        queue = self._name('q')
        self.channel.queue_declare(queue, arguments=prepared)
        assert self.channel.get_queue_properties(queue) == {
            'dead_letter_exchange': 'dlx',
            'dead_letter_routing_key': 'rk',
            'message_ttl': 1500,
            'expires': 2000,
            'max_length': 10,
            'max_length_bytes': 20,
            'max_priority': 3,
        }
        assert self.channel.queue_properties_for_declare(queue) == {
            'x-dead-letter-exchange': 'dlx',
            'x-dead-letter-routing-key': 'rk',
            'x-message-ttl': 1500,
            'x-expires': 2000,
            'x-max-length': 10,
            'x-max-length-bytes': 20,
            'x-max-priority': 3,
        }

        self.channel.queue_declare(queue, arguments={
            'x-dead-letter-exchange': 'other',
        })
        assert self.channel.get_queue_properties(queue) == {
            'dead_letter_exchange': 'other',
        }

        self.channel.queue_declare(queue, passive=True)
        assert self.channel.get_queue_properties(queue) == {
            'dead_letter_exchange': 'other',
        }

    def test_queue_entity_declare_stores_properties(self):
        ex_name = self._name('entity-ex')
        queue_name = self._name('entity-q')
        queue = Queue.with_dead_letter(
            queue_name, 'dlx', 'dlk',
            exchange=Exchange(ex_name, type='direct'),
            routing_key='rk',
            message_ttl=1.5,
            max_length=4,
        )
        queue(self.channel).declare()
        assert self.channel.get_queue_properties(queue_name) == {
            'dead_letter_exchange': 'dlx',
            'dead_letter_routing_key': 'dlk',
            'message_ttl': 1500,
            'max_length': 4,
        }

    def test_prepare_message_stamps_expiration(self):
        before = time()
        message = self.channel.prepare_message(
            'hello', properties={'expiration': '1500'},
        )
        after = time()
        expires_at = message['properties']['x-expires-at']
        assert before + 1.5 <= expires_at <= after + 1.5
        assert message['properties']['expiration'] == '1500'
        assert self.channel.message_ttl_remaining(message) > 1.0

    def test_queue_ttl_and_per_message_precedence(self):
        ex, dlx, src, dlq = self._declare_dl_pair(
            queue_args={'x-message-ttl': 10000},
        )
        queued = self.channel.prepare_message('from-queue')
        self.channel.basic_publish(queued, ex, 'rk')
        stored = self.channel._get(src)
        remaining = self.channel.message_ttl_remaining(stored)
        assert remaining == pytest.approx(10, abs=0.5)
        assert 'expiration' not in stored['properties']

        per_message = self.channel.prepare_message(
            'from-message', properties={'expiration': '500'},
        )
        self.channel.basic_publish(per_message, ex, 'rk')
        stored = self.channel._get(src)
        assert self.channel.message_ttl_remaining(stored) == pytest.approx(
            0.5, abs=0.3)
        unset = _raw('plain')
        assert self.channel.message_ttl_remaining(unset) is None
        expired = _raw('old', expires_at=time() - 2)
        assert self.channel.message_ttl_remaining(expired) < 0

    def test_independent_ttl_per_destination_queue(self):
        ex = self._name('fan-ex')
        q1 = self._name('q1')
        q2 = self._name('q2')
        self.channel.exchange_declare(ex, type='direct')
        self.channel.queue_declare(q1, arguments={'x-message-ttl': 1000})
        self.channel.queue_declare(q2, arguments={'x-message-ttl': 4000})
        self.channel.queue_bind(q1, ex, 'rk')
        self.channel.queue_bind(q2, ex, 'rk')
        self.channel.basic_publish(self.channel.prepare_message('body'), ex, 'rk')
        first = self.channel._get(q1)
        second = self.channel._get(q2)
        assert first['properties'] is not second['properties']
        delta = (
            second['properties']['x-expires-at']
            - first['properties']['x-expires-at']
        )
        assert delta == pytest.approx(3, abs=0.5)

    def test_max_length_dead_letters_oldest(self):
        ex, dlx, src, dlq = self._declare_dl_pair(
            queue_args={'x-max-length': 2},
        )
        for body in ('a', 'b', 'c'):
            self.channel.basic_publish(
                self.channel.prepare_message(body), ex, 'rk',
            )
        assert self.channel._size(src) == 2
        dead = self.channel.basic_get(dlq)
        assert dead.body == b'a'
        death = dead.headers['x-death'][0]
        assert death['reason'] == 'maxlen'
        assert death['queue'] == src
        assert death['count'] == 1
        assert isinstance(death['count'], int)
        assert isinstance(death['time'], float)
        assert death['routing-key'] == 'rk'
        assert {key for key in death} >= {
            'queue', 'reason', 'exchange', 'routing-key', 'count', 'time',
        }
        assert self.channel.basic_get(src).body == b'b'
        assert self.channel.basic_get(src).body == b'c'

    def test_topic_publish_enforces_max_length(self):
        ex = self._name('topic')
        dlx = self._name('topic-dlx')
        src = self._name('topic-src')
        dlq = self._name('topic-dlq')
        self.channel.exchange_declare(ex, type='topic')
        self.channel.exchange_declare(dlx, type='direct')
        self.channel.queue_declare(src, arguments={
            'x-max-length': 1,
            'x-dead-letter-exchange': dlx,
            'x-dead-letter-routing-key': 'dead',
        })
        self.channel.queue_declare(dlq)
        self.channel.queue_bind(src, ex, 'orders.#')
        self.channel.queue_bind(dlq, dlx, 'dead')
        self.channel.basic_publish(
            self.channel.prepare_message('a'), ex, 'orders.new')
        self.channel.basic_publish(
            self.channel.prepare_message('b'), ex, 'orders.new')
        assert self.channel._size(src) == 1
        assert self.channel.basic_get(dlq).body == b'a'
        assert self.channel.basic_get(src).body == b'b'

    def test_basic_get_skips_expired_and_consume_sets_queue(self):
        ex, dlx, src, dlq = self._declare_dl_pair()
        self.channel._put(src, _raw('old', expires_at=time() - 5, exchange=ex,
                                    routing_key='rk'))
        self.channel._put(src, _raw('new', expires_at=time() + 30, exchange=ex,
                                    routing_key='rk'))
        message = self.channel.basic_get(src)
        assert message.body == b'new'
        assert message.delivery_info['queue'] == src
        dead = self.channel.basic_get(dlq)
        assert dead.body == b'old'
        assert dead.headers['x-death'][0]['reason'] == 'expired'
        assert dead.delivery_info['exchange'] == dlx
        assert dead.delivery_info['routing_key'] == 'dead'
        assert 'expiration' not in dead.properties
        assert dead.properties.get('x-expires-at') is None
        assert dead.headers['x-first-death-reason'] == 'expired'
        assert dead.headers['x-first-death-queue'] == src
        assert dead.headers['x-first-death-exchange'] == ex

        self.channel._put(src, _raw('gone', expires_at=time() - 1, exchange=ex,
                                    routing_key='rk'))
        assert self.channel.basic_get(src) is None
        assert self.channel.basic_get(dlq).body == b'gone'

        self.channel.basic_publish(
            self.channel.prepare_message('consumed'), ex, 'rk')
        received = []
        self.channel.basic_consume(src, True, received.append, 'ctag')
        self.channel.drain_events(timeout=1)
        assert received[0].delivery_info['queue'] == src
        assert received[0].body == b'consumed'

    def test_drain_expired_and_memory_expire_messages(self):
        ex, dlx, src, dlq = self._declare_dl_pair()
        self.channel._put(src, _raw('keep', expires_at=time() + 50))
        self.channel._put(src, _raw('drop1', expires_at=time() - 5))
        self.channel._put(src, _raw('keep2', expires_at=time() + 50))
        self.channel._put(src, _raw('drop2', expires_at=time() - 1))
        assert self.channel.drain_expired(src) == 2
        assert self.channel.basic_get(src).body == b'keep'
        assert self.channel.basic_get(src).body == b'keep2'
        assert self.channel.basic_get(src) is None
        assert self.channel.basic_get(dlq).body == b'drop1'
        assert self.channel.basic_get(dlq).body == b'drop2'

        self.channel._put(src, _raw('soon', expires_at=time() - 1))
        self.channel._put(src, _raw('later', expires_at=time() + 20))
        assert self.channel.expire_messages(src) == 1
        assert self.channel.basic_get(src).body == b'later'
        assert self.channel.basic_get(dlq).body == b'soon'

    def test_reject_dead_letters_and_requeue_restores(self):
        ex, dlx, src, dlq = self._declare_dl_pair()
        self.channel.basic_publish(
            self.channel.prepare_message('nope'), ex, 'rk')
        message = self.channel.basic_get(src)
        assert message.delivery_info['queue'] == src
        assert self.channel.qos.redelivery_count(message.delivery_tag) == 0
        message.reject(requeue=False)
        dead = self.channel.basic_get(dlq)
        assert dead.body == b'nope'
        assert dead.headers['x-death'][0]['reason'] == 'rejected'
        assert dead.headers['x-death'][0]['count'] == 1
        assert self.channel.qos.redelivery_count(dead.delivery_tag) == 1
        assert self.channel.qos.redelivery_count('missing-tag') == 0

        self.channel.basic_publish(
            self.channel.prepare_message('back'), ex, 'rk')
        again = self.channel.basic_get(src)
        again.reject(requeue=True)
        restored = self.channel.basic_get(src)
        assert restored.body == b'back'
        assert self.channel.basic_get(dlq) is None

    def test_x_death_count_and_first_death_headers(self):
        ex, dlx, src, dlq = self._declare_dl_pair()
        self.channel.basic_publish(
            self.channel.prepare_message('body'), ex, 'rk')
        original = self.channel._get(src)
        self.channel.dead_letter(original, src, 'expired')
        first = self.channel._get(dlq)
        assert first['headers']['x-death'][0]['count'] == 1
        assert first['headers']['x-first-death-reason'] == 'expired'
        assert first['headers']['x-first-death-queue'] == src
        assert first['headers']['x-first-death-exchange'] == ex

        self.channel.dead_letter(first, src, 'expired')
        second = self.channel._get(dlq)
        assert second['headers']['x-death'][0]['count'] == 2
        assert second['headers']['x-first-death-reason'] == 'expired'

        self.channel.dead_letter(second, src, 'rejected')
        third = self.channel._get(dlq)
        indexed = {
            (entry['queue'], entry['reason']): entry
            for entry in third['headers']['x-death']
        }
        assert indexed[(src, 'expired')]['count'] == 2
        assert indexed[(src, 'rejected')]['count'] == 1
        assert len(third['headers']['x-death']) == 2
        assert third['headers']['x-first-death-reason'] == 'expired'
        assert third['headers']['x-first-death-queue'] == src
        assert third['headers']['x-first-death-exchange'] == ex
        assert 'expiration' not in third['properties']
        assert third['properties'].get('x-expires-at') is None
        assert third['properties']['delivery_info']['exchange'] == dlx
        assert third['properties']['delivery_info']['routing_key'] == 'dead'

    def test_routing_key_preserved_without_override(self):
        ex, dlx, src, dlq = self._declare_dl_pair(dl_routing_key=None)
        self.channel.basic_publish(
            self.channel.prepare_message('keep-key'), ex, 'rk')
        self.channel.dead_letter(self.channel._get(src), src, 'rejected')
        dead = self.channel._get(dlq)
        assert dead['properties']['delivery_info']['exchange'] == dlx
        assert dead['properties']['delivery_info']['routing_key'] == 'rk'
        assert dead['headers']['x-death'][0]['routing-key'] == 'rk'

    def test_cycle_detection_and_max_hops(self):
        dlx = self._name('cycle-dlx')
        src = self._name('cycle-src')
        mid = self._name('cycle-mid')
        other = self._name('cycle-other')
        self.channel.exchange_declare(dlx, type='direct')
        self.channel.queue_declare(src, arguments={
            'x-dead-letter-exchange': dlx,
            'x-dead-letter-routing-key': 'to-mid',
        })
        self.channel.queue_declare(mid, arguments={
            'x-dead-letter-exchange': dlx,
            'x-dead-letter-routing-key': 'to-src',
        })
        self.channel.queue_declare(other)
        self.channel.queue_bind(mid, dlx, 'to-mid')
        self.channel.queue_bind(src, dlx, 'to-src')
        self.channel.queue_bind(other, dlx, 'to-src')

        self.channel._put(src, _raw('cycle', exchange='ex', routing_key='rk'))
        self.channel.dead_letter(self.channel._get(src), src, 'expired')
        assert self.channel._size(mid) == 1
        self.channel.dead_letter(self.channel._get(mid), mid, 'expired')
        assert self.channel._size(src) == 0
        landed = self.channel._get(other)
        queues = {entry['queue'] for entry in landed['headers']['x-death']}
        assert queues == {src, mid}

        self.channel.dead_letter_max_hops = 1
        self.channel._put(src, _raw('hop', exchange='ex', routing_key='rk'))
        self.channel.dead_letter(self.channel._get(src), src, 'rejected')
        assert self.channel._size(mid) == 1
        self.channel.dead_letter(self.channel._get(mid), mid, 'rejected')
        assert self.channel._size(src) == 0
        assert self.channel._size(other) == 0
        with pytest.raises(Empty):
            self.channel._get(other)

    def test_missing_dlx_is_discarded(self):
        src = self._name('nodlx')
        self.channel.queue_declare(src)
        self.channel._put(src, _raw('gone'))
        self.channel.dead_letter(self.channel._get(src), src, 'rejected')
        assert self.channel._size(src) == 0

        missing = self._name('missing-dlx')
        self.channel.queue_declare(missing, arguments={
            'x-dead-letter-exchange': self._name('absent'),
        })
        self.channel._put(missing, _raw('gone-too'))
        self.channel.dead_letter(
            self.channel._get(missing), missing, 'expired')
        assert self.channel._size(missing) == 0

    def test_queue_delete_drops_properties(self):
        queue = self._name('doomed')
        ex = self._name('doomed-ex')
        self.channel.exchange_declare(ex, type='direct')
        self.channel.queue_declare(queue, arguments={'x-max-length': 3})
        self.channel.queue_bind(queue, ex, 'rk')
        assert self.channel.get_queue_properties(queue)['max_length'] == 3
        self.channel.queue_delete(queue)
        assert self.channel.get_queue_properties(queue) == {}
