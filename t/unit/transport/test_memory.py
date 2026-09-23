from __future__ import annotations

import socket
from unittest.mock import patch

import pytest

from kombu import Connection, Consumer, Exchange, Producer, Queue


class test_MemoryTransport:

    def setup_method(self):
        self.c = Connection(transport='memory')
        self.e = Exchange('test_transport_memory')
        self.q = Queue('test_transport_memory',
                       exchange=self.e,
                       routing_key='test_transport_memory')
        self.q2 = Queue('test_transport_memory2',
                        exchange=self.e,
                        routing_key='test_transport_memory2')
        self.fanout = Exchange('test_transport_memory_fanout', type='fanout')
        self.q3 = Queue('test_transport_memory_fanout1',
                        exchange=self.fanout)
        self.q4 = Queue('test_transport_memory_fanout2',
                        exchange=self.fanout)

    def test_driver_version(self):
        assert self.c.transport.driver_version()

    def test_produce_consume_noack(self):
        channel = self.c.channel()
        producer = Producer(channel, self.e)
        consumer = Consumer(channel, self.q, no_ack=True)

        for i in range(10):
            producer.publish({'foo': i}, routing_key='test_transport_memory')

        _received = []

        def callback(message_data, message):
            _received.append(message)

        consumer.register_callback(callback)
        consumer.consume()

        while 1:
            if len(_received) == 10:
                break
            self.c.drain_events()

        assert len(_received) == 10

    def test_produce_consume_fanout(self):
        producer = self.c.Producer()
        consumer = self.c.Consumer([self.q3, self.q4])

        producer.publish(
            {'hello': 'world'},
            declare=consumer.queues,
            exchange=self.fanout,
        )

        assert self.q3(self.c).get().payload == {'hello': 'world'}
        assert self.q4(self.c).get().payload == {'hello': 'world'}
        assert self.q3(self.c).get() is None
        assert self.q4(self.c).get() is None

    def test_produce_consume(self):
        channel = self.c.channel()
        producer = Producer(channel, self.e)
        consumer1 = Consumer(channel, self.q)
        consumer2 = Consumer(channel, self.q2)
        self.q2(channel).declare()

        for i in range(10):
            producer.publish({'foo': i}, routing_key='test_transport_memory')
        for i in range(10):
            producer.publish({'foo': i}, routing_key='test_transport_memory2')

        _received1 = []
        _received2 = []

        def callback1(message_data, message):
            _received1.append(message)
            message.ack()

        def callback2(message_data, message):
            _received2.append(message)
            message.ack()

        consumer1.register_callback(callback1)
        consumer2.register_callback(callback2)

        consumer1.consume()
        consumer2.consume()

        while 1:
            if len(_received1) + len(_received2) == 20:
                break
            self.c.drain_events()

        assert len(_received1) + len(_received2) == 20

        # compression
        producer.publish({'compressed': True},
                         routing_key='test_transport_memory',
                         compression='zlib')
        m = self.q(channel).get()
        assert m.payload == {'compressed': True}

        # queue.delete
        for i in range(10):
            producer.publish({'foo': i}, routing_key='test_transport_memory')
        assert self.q(channel).get()
        self.q(channel).delete()
        self.q(channel).declare()
        assert self.q(channel).get() is None

        # queue.purge
        for i in range(10):
            producer.publish({'foo': i}, routing_key='test_transport_memory2')
        assert self.q2(channel).get()
        self.q2(channel).purge()
        assert self.q2(channel).get() is None

    def test_drain_events(self):
        with pytest.raises(socket.timeout):
            self.c.drain_events(timeout=0.1)

        c1 = self.c.channel()
        c2 = self.c.channel()

        with pytest.raises(socket.timeout):
            self.c.drain_events(timeout=0.1)

        del c1  # so pyflakes doesn't complain.
        del c2

    def test_drain_events_unregistered_queue(self):
        c1 = self.c.channel()
        producer = self.c.Producer()
        consumer = self.c.Consumer([self.q2])

        producer.publish(
            {'hello': 'world'},
            declare=consumer.queues,
            routing_key=self.q2.routing_key,
            exchange=self.q2.exchange,
        )
        message = consumer.queues[0].get()._raw

        class Cycle:

            def get(self, callback, timeout=None):
                return (message, 'foo'), c1

        self.c.transport.cycle = Cycle()
        self.c.drain_events()

    def test_queue_for(self):
        chan = self.c.channel()
        chan.queues.clear()

        x = chan._queue_for('foo')
        assert x
        assert chan._queue_for('foo') is x

    # see the issue
    # https://github.com/celery/kombu/issues/1050
    def test_producer_on_return(self):
        def on_return(_exception, _exchange, _routing_key, _message):
            pass
        channel = self.c.channel()
        producer = Producer(channel, on_return=on_return)
        consumer = self.c.Consumer([self.q3])

        producer.publish(
            {'hello': 'on return'},
            declare=consumer.queues,
            exchange=self.fanout,
        )

        assert self.q3(self.c).get().payload == {'hello': 'on return'}
        assert self.q3(self.c).get() is None


class test_MemoryTransport_dead_letter:

    def setup_method(self):
        self.c = Connection(transport='memory')
        self.channel = self.c.channel()
        self.channel.queues.clear()
        self.channel.state.clear()
        self.e = Exchange('test_memory_dlx_work')
        self.dlx = Exchange('test_memory_dlx')
        self.dlq = Queue('test_memory_dlq', self.dlx,
                         routing_key='test_memory_dlx_work')
        self.dlq(self.channel).declare()

    def teardown_method(self):
        self.channel.queues.clear()
        self.channel.state.clear()

    def work_queue(self, **kwargs):
        queue = Queue.with_dead_letter(
            'test_memory_dlx_work', self.dlx,
            exchange=self.e, routing_key='test_memory_dlx_work', **kwargs)
        queue(self.channel).declare()
        return queue(self.channel)

    def publish(self, body, **kwargs):
        Producer(self.channel, self.e).publish(
            body, routing_key='test_memory_dlx_work', **kwargs)

    def test_reject(self):
        work = self.work_queue()
        self.publish({'hello': 'world'})
        message = work.get()
        assert message.delivery_info['queue'] == work.name
        message.reject()

        assert work.get() is None
        dead = self.dlq(self.channel).get()
        assert dead.payload == {'hello': 'world'}
        assert dead.delivery_info['exchange'] == self.dlx.name
        assert dead.delivery_info['routing_key'] == 'test_memory_dlx_work'
        assert dead.headers['x-first-death-reason'] == 'rejected'
        assert dead.headers['x-first-death-queue'] == work.name

    def test_message_ttl(self):
        work = self.work_queue(message_ttl=1)
        with patch('kombu.transport.virtual.base.time') as time_:
            time_.return_value = 1000.0
            self.publish({'n': 1})
            self.publish({'n': 2}, expiration=10)
            time_.return_value = 1005.0
            assert self.channel.expire_messages(work.name) == 1
            assert work.get().payload == {'n': 2}

        dead = self.dlq(self.channel).get()
        assert dead.payload == {'n': 1}
        assert dead.headers['x-death'][0]['reason'] == 'expired'

    def test_max_length(self):
        work = self.work_queue(max_length=2)
        for i in range(3):
            self.publish({'n': i})
        assert [work.get().payload['n'] for _ in range(2)] == [1, 2]
        dead = self.dlq(self.channel).get()
        assert dead.payload == {'n': 0}
        assert dead.headers['x-death'][0]['reason'] == 'maxlen'

    def test_fanout_max_length(self):
        fanout = Exchange('test_memory_dlx_fanout', type='fanout')
        queue = Queue('test_memory_dlx_fanout_q', fanout, max_length=1)
        queue(self.channel).declare()
        producer = Producer(self.channel, fanout)
        producer.publish({'n': 1})
        producer.publish({'n': 2})
        assert queue(self.channel).get().payload == {'n': 2}
        assert queue(self.channel).get() is None
