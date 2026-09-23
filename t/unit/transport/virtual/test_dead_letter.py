from __future__ import annotations

from time import time

import pytest

from kombu import Connection, Exchange, Producer, Queue
from kombu.transport import virtual
from kombu.utils.uuid import uuid


def test_BrokerState_queue_properties():
    s = virtual.BrokerState()
    assert s.queue_properties_get('q') == {}
    s.queue_properties_set('q', message_ttl=1000, max_length=2)
    assert s.queue_properties_get('q') == {
        'message_ttl': 1000, 'max_length': 2,
    }
    s.queue_properties_set('q', max_length=3)
    assert s.queue_properties_get('q') == {'max_length': 3}
    s.queue_properties_delete('q')
    assert s.queue_properties_get('q') == {}
    s.queue_properties_delete('q')

    s.queue_properties_set('q', max_length=3)
    s.queue_bindings_delete('q')
    assert s.queue_properties_get('q') == {}

    s.queue_properties_set('q', max_length=3)
    s.clear()
    assert s.queue_properties == {}


class test_Queue_dead_letter:

    def test_attributes(self):
        q = Queue('q', dead_letter_exchange='dlx', dead_letter_routing_key='k')
        assert q.has_dead_letter_exchange
        assert q.effective_dead_letter_exchange == 'dlx'
        assert q.effective_dead_letter_routing_key == 'k'

    def test_queue_arguments(self):
        q = Queue('q', routing_key='rk', queue_arguments={
            'x-dead-letter-exchange': 'dlx', 'x-message-ttl': 1500,
        })
        assert q.has_dead_letter_exchange
        assert q.effective_dead_letter_exchange == 'dlx'
        assert q.effective_dead_letter_routing_key == 'rk'
        assert q.effective_message_ttl == 1.5

    def test_no_dead_letter(self):
        q = Queue('q')
        assert not q.has_dead_letter_exchange
        assert q.effective_dead_letter_exchange is None
        assert q.effective_message_ttl is None
        assert Queue('q', message_ttl=3).effective_message_ttl == 3

    def test_with_dead_letter(self):
        q = Queue.with_dead_letter('q', 'dlx', 'k', durable=False)
        assert q.name == 'q'
        assert q.dead_letter_exchange == 'dlx'
        assert q.dead_letter_routing_key == 'k'
        assert not q.durable

    def test_from_dict(self):
        q = Queue.from_dict('q', exchange='ex', dead_letter_exchange='dlx',
                            dead_letter_routing_key='k')
        assert q.dead_letter_exchange == 'dlx'
        assert q.dead_letter_routing_key == 'k'


class DeadLetterCase:

    def setup_method(self):
        self.conn = Connection(transport='memory')
        self.channel = self.conn.channel()
        self.channel.state.clear()
        self.prefix = uuid()

    def teardown_method(self):
        self.channel.state.clear()
        self.conn.close()

    def name(self, n):
        return f'{self.prefix}.{n}'

    def declare(self, name, exchange=None, routing_key=None, **kwargs):
        exchange = exchange or Exchange(self.name('ex'), 'direct')
        q = Queue(self.name(name), exchange,
                  routing_key or self.name(name), **kwargs)
        q(self.channel).declare()
        return q

    def publish(self, body, queue, **kwargs):
        Producer(self.channel).publish(
            body, exchange=queue.exchange, routing_key=queue.routing_key,
            **kwargs)

    def expire_all(self, queue):
        for message in self.channel.queues[queue].queue:
            message['properties']['x-expires-at'] = time() - 1


class test_queue_arguments(DeadLetterCase):

    def test_prepare_queue_arguments(self):
        args = self.channel.prepare_queue_arguments(
            {}, dead_letter_exchange='dlx', dead_letter_routing_key='k',
            message_ttl=1.5, max_length=10, max_length_bytes=100,
            expires=2, max_priority=5,
        )
        assert args == {
            'x-dead-letter-exchange': 'dlx',
            'x-dead-letter-routing-key': 'k',
            'x-message-ttl': 1500,
            'x-max-length': 10,
            'x-max-length-bytes': 100,
            'x-expires': 2000,
            'x-max-priority': 5,
        }

    def test_declare_stores_properties(self):
        q = self.declare('q', dead_letter_exchange='dlx', message_ttl=2,
                         max_length=4)
        assert self.channel.get_queue_properties(q.name) == {
            'dead_letter_exchange': 'dlx',
            'message_ttl': 2000,
            'max_length': 4,
        }
        assert self.channel.queue_properties_for_declare(q.name) == {
            'x-dead-letter-exchange': 'dlx',
            'x-message-ttl': 2000,
            'x-max-length': 4,
        }

    def test_redeclare_replaces(self):
        self.channel.queue_declare(self.name('q'), arguments={
            'x-max-length': 1, 'x-message-ttl': 10,
        })
        self.channel.queue_declare(self.name('q'), arguments={
            'x-max-length': 2,
        })
        assert self.channel.get_queue_properties(self.name('q')) == {
            'max_length': 2,
        }

    def test_delete_removes_properties(self):
        q = self.declare('q', max_length=4)
        self.channel.queue_delete(q.name)
        assert self.channel.get_queue_properties(q.name) == {}


class test_ttl(DeadLetterCase):

    def test_prepare_message_expiration(self):
        before = time()
        m = self.channel.prepare_message('x', properties={
            'expiration': '2000',
        })
        assert before + 2 <= m['properties']['x-expires-at'] <= time() + 2

    def test_message_ttl_remaining(self):
        c = self.channel
        assert c.message_ttl_remaining({'properties': {}}) is None
        m = c.prepare_message('x', properties={'expiration': '10000'})
        assert 9 < c.message_ttl_remaining(m) <= 10
        m['properties']['x-expires-at'] = time() - 5
        assert c.message_ttl_remaining(m) < 0

    def test_queue_ttl_applied(self):
        q = self.declare('q', message_ttl=10)
        self.publish('x', q)
        raw = self.channel.queues[q.name].queue[0]
        assert 9 < self.channel.message_ttl_remaining(raw) <= 10

    def test_message_expiration_takes_precedence(self):
        q = self.declare('q', message_ttl=10)
        self.publish('x', q, expiration=100)
        raw = self.channel.queues[q.name].queue[0]
        assert 99 < self.channel.message_ttl_remaining(raw) <= 100

    def test_independent_expiry_per_queue(self):
        ex = Exchange(self.name('ex'), 'direct')
        q1 = self.declare('q1', ex, 'rk', message_ttl=10)
        q2 = self.declare('q2', ex, 'rk', message_ttl=100)
        self.publish('x', q1)
        r1 = self.channel.queues[q1.name].queue[0]
        r2 = self.channel.queues[q2.name].queue[0]
        assert 9 < self.channel.message_ttl_remaining(r1) <= 10
        assert 99 < self.channel.message_ttl_remaining(r2) <= 100

    def test_basic_get_skips_expired(self):
        dlx = Exchange(self.name('dlx'), 'direct')
        dlq = self.declare('dlq', dlx, 'rk')
        q = self.declare('q', routing_key='rk',
                         dead_letter_exchange=dlx.name)
        self.publish('a', q)
        self.publish('b', q)
        self.expire_all(q.name)
        self.publish('c', q)

        message = self.channel.basic_get(q.name, no_ack=True)
        assert message.body == b'c'
        assert message.delivery_info['queue'] == q.name
        assert self.channel.basic_get(q.name) is None

        dead = [self.channel.basic_get(dlq.name, no_ack=True)
                for _ in range(2)]
        assert [m.body for m in dead] == [b'a', b'b']
        assert dead[0].headers['x-death'][0]['reason'] == 'expired'
        assert 'expiration' not in dead[0].properties
        assert 'x-expires-at' not in dead[0].properties

    def test_basic_get_all_expired(self):
        q = self.declare('q')
        self.publish('a', q, expiration=10)
        self.expire_all(q.name)
        assert self.channel.basic_get(q.name) is None

    def test_drain_expired(self):
        q = self.declare('q')
        self.publish('a', q, expiration=10)
        self.expire_all(q.name)
        self.publish('b', q, expiration=10)
        self.publish('c', q)
        assert self.channel.drain_expired(q.name) == 1
        assert [self.channel.basic_get(q.name, no_ack=True).body
                for _ in range(2)] == [b'b', b'c']

    def test_expire_messages(self):
        q = self.declare('q')
        self.publish('a', q, expiration=10)
        self.publish('b', q)
        self.expire_all(q.name)
        assert self.channel.expire_messages(q.name) == 2
        assert self.channel._size(q.name) == 0


class test_max_length(DeadLetterCase):

    def test_evicts_oldest(self):
        dlx = Exchange(self.name('dlx'), 'direct')
        dlq = self.declare('dlq', dlx, 'rk')
        q = self.declare('q', routing_key='rk', max_length=2,
                         dead_letter_exchange=dlx.name)
        for body in 'abc':
            self.publish(body, q)
        assert self.channel._size(q.name) == 2
        assert self.channel.basic_get(q.name, no_ack=True).body == b'b'
        dead = self.channel.basic_get(dlq.name, no_ack=True)
        assert dead.body == b'a'
        assert dead.headers['x-death'][0]['reason'] == 'maxlen'


class test_dead_letter(DeadLetterCase):

    def setup_method(self):
        super().setup_method()
        self.dlx = Exchange(self.name('dlx'), 'direct')

    def test_reject_routes_to_dlx(self):
        dlq = self.declare('dlq', self.dlx, 'rk')
        q = self.declare('q', routing_key='rk',
                         dead_letter_exchange=self.dlx.name)
        self.publish('x', q)
        message = self.channel.basic_get(q.name)
        message.reject()

        dead = self.channel.basic_get(dlq.name)
        assert dead.body == b'x'
        assert dead.delivery_info['exchange'] == self.dlx.name
        assert dead.delivery_info['routing_key'] == 'rk'
        death = dead.headers['x-death'][0]
        assert death['queue'] == q.name
        assert death['reason'] == 'rejected'
        assert death['exchange'] == q.exchange.name
        assert death['routing-key'] == 'rk'
        assert death['count'] == 1
        assert death['time']
        assert dead.headers['x-first-death-reason'] == 'rejected'
        assert dead.headers['x-first-death-queue'] == q.name
        assert dead.headers['x-first-death-exchange'] == q.exchange.name
        assert self.channel.qos.redelivery_count(dead.delivery_tag) == 1

    def test_reject_requeue(self):
        q = self.declare('q', dead_letter_exchange=self.dlx.name)
        self.publish('x', q)
        self.channel.basic_get(q.name).reject(requeue=True)
        assert self.channel.basic_get(q.name, no_ack=True).body == b'x'

    def test_dead_letter_routing_key_override(self):
        dlq = self.declare('dlq', self.dlx, 'dead')
        q = self.declare('q', dead_letter_exchange=self.dlx.name,
                         dead_letter_routing_key='dead')
        self.publish('x', q)
        self.channel.basic_get(q.name).reject()
        dead = self.channel.basic_get(dlq.name, no_ack=True)
        assert dead.delivery_info['routing_key'] == 'dead'

    def test_no_dlx_discards(self):
        q = self.declare('q')
        self.publish('x', q)
        self.channel.basic_get(q.name).reject()
        assert self.channel._size(q.name) == 0

    def test_missing_dlx_exchange(self):
        q = self.declare('q', dead_letter_exchange='missing')
        self.publish('x', q)
        self.channel.basic_get(q.name).reject()
        assert self.channel._size(q.name) == 0

    def test_x_death_count_and_append(self):
        dlq = self.declare('dlq', self.dlx, 'rk')
        q = self.declare('q', routing_key='rk',
                         dead_letter_exchange=self.dlx.name)
        message = self.channel.prepare_message('x', headers={'x-death': [{
            'queue': q.name, 'reason': 'rejected', 'exchange': '',
            'routing-key': 'rk', 'count': 1, 'time': 0,
        }], 'x-first-death-reason': 'rejected'})
        message['properties']['delivery_info'].update(
            exchange=q.exchange.name, routing_key='rk')
        self.channel.dead_letter(message, q.name, 'rejected')
        dead = self.channel._get(dlq.name)
        assert len(dead['headers']['x-death']) == 1
        assert dead['headers']['x-death'][0]['count'] == 2

        self.channel.dead_letter(message, q.name, 'expired')
        dead = self.channel._get(dlq.name)
        deaths = dead['headers']['x-death']
        assert [d['reason'] for d in deaths] == ['rejected', 'expired']
        assert dead['headers']['x-first-death-reason'] == 'rejected'

    def test_cycle_detection(self):
        dlx2 = Exchange(self.name('dlx2'), 'direct')
        q1 = self.declare('q1', dlx2, 'rk', dead_letter_exchange=self.dlx.name)
        q2 = self.declare('q2', self.dlx, 'rk', dead_letter_exchange=dlx2.name)
        self.publish('x', q1, expiration=10)
        self.expire_all(q1.name)
        assert self.channel.drain_expired(q1.name) == 1
        assert self.channel._size(q2.name) == 1
        self.expire_all(q2.name)
        assert self.channel.drain_expired(q2.name) == 1
        assert self.channel._size(q1.name) == 0
        assert self.channel._size(q2.name) == 0

    @pytest.mark.parametrize('max_hops,delivered', [(1, False), (2, True)])
    def test_max_hops(self, max_hops, delivered):
        self.channel.dead_letter_max_hops = max_hops
        dlq = self.declare('dlq', self.dlx, 'rk')
        q = self.declare('q', routing_key='rk',
                         dead_letter_exchange=self.dlx.name)
        message = self.channel.prepare_message('x', headers={'x-death': [{
            'queue': 'other', 'reason': 'rejected', 'count': 1,
        }]})
        message['properties']['delivery_info']['routing_key'] = 'rk'
        self.channel.dead_letter(message, q.name, 'rejected')
        assert bool(self.channel._size(dlq.name)) is delivered
