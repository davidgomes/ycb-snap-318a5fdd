# Copyright 2026 Google Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
"""Tests for grouped execution and synchronization in `BaseTestClass`."""

import os
import shutil
import tempfile
import threading
import time
import unittest
from unittest import mock

from mobly import base_test
from mobly import config_parser
from mobly import expects
from mobly import records
from mobly import signals
from tests.lib import mock_controller


class GroupedExecutionTest(unittest.TestCase):

  def setUp(self):
    self.tmp_dir = tempfile.mkdtemp()
    self.configs = config_parser.TestRunConfig()
    self.configs.summary_writer = records.TestSummaryWriter(
        os.path.join(self.tmp_dir, 'summary.yaml')
    )
    self.configs.controller_configs = {}
    self.configs.log_path = self.tmp_dir
    self.configs.user_params = {}
    self.configs.reporter = mock.MagicMock()

  def tearDown(self):
    shutil.rmtree(self.tmp_dir)

  def _run(self, test_cls, test_names=None):
    instance = test_cls(self.configs)
    instance.run(test_names=test_names)
    return instance

  def test_no_entries(self):
    outer = self
    calls = []

    class MockTest(base_test.BaseTestClass):

      def global_setup(self):
        calls.append('global_setup')

      def group_setup(self, devices):
        calls.append('group_setup')

      def group_teardown(self, devices):
        calls.append('group_teardown')

      def global_teardown(self):
        calls.append('global_teardown')

      def test_a(self):
        calls.append('test_a')
        self.synchronized_step('noop')
        with outer.assertRaises((AttributeError, RuntimeError)):
          _ = self.current_device
        with outer.assertRaises((AttributeError, RuntimeError)):
          _ = self.current_device_id
        calls.append('checked')

    instance = self._run(MockTest)
    self.assertEqual(calls, ['global_setup', 'test_a', 'checked', 'global_teardown'])
    self.assertEqual(len(instance.results.passed), 1)

  def test_implicit_mode(self):
    calls = []

    class MockTest(base_test.BaseTestClass):

      def setup_class(self):
        self.register_controller(mock_controller)

      def group_setup(self, devices):
        calls.append(('group_setup', [d.magic for d in devices]))
        calls.append(('group_setup_device', self.current_device.magic))

      def group_teardown(self, devices):
        calls.append(('group_teardown', len(devices)))

      def test_a(self):
        self.synchronized_step('noop', timeout=1)
        calls.append(('test_a', self.current_device.magic,
                      self.current_device_id))

    self.configs.controller_configs = {
        'MagicDevice': [{'serial': 1, 'id': 'x'}, {'serial': 2}]
    }
    instance = self._run(MockTest)
    self.assertEqual(
        calls,
        [
            ('group_setup', [{'id': 'x'}, {}]),
            ('group_setup_device', {'id': 'x'}),
            ('test_a', {'id': 'x'}, 'x'),
            ('group_teardown', 2),
        ],
    )
    self.assertEqual(len(instance.results.passed), 1)

  def test_raw_entries_when_objects_not_paired(self):
    received = []

    class MockTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        received.extend(devices)

      def test_a(self):
        pass

    self.configs.controller_configs = {'Foo': ['a', 'b']}
    self._run(MockTest)
    self.assertEqual(received, ['a', 'b'])

  def test_explicit_mode(self):
    lock = threading.Lock()
    calls = []

    class MockTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        with lock:
          calls.append(('group_setup', self.current_device_id, len(devices)))
        self.synchronized_step('never_blocks')

      def group_teardown(self, devices):
        with lock:
          calls.append(('group_teardown', self.current_device_id))

      def test_a(self):
        with lock:
          calls.append(('test_a', self.current_device_id))

    self.configs.controller_configs = {
        'Foo': [
            {'group': 'g1', 'id': 'a'},
            {'group': 'g1', 'id': 'b'},
            {'group': 'g2', 'id': 'c'},
            {'id': 'd'},
        ]
    }
    instance = self._run(MockTest)
    self.assertEqual(calls[0], ('group_setup', 'a', 2))
    self.assertEqual(
        sorted(calls[1:3]), [('test_a', 'a'), ('test_a', 'b')]
    )
    self.assertEqual(
        calls[3:],
        [
            ('group_teardown', 'a'),
            ('group_setup', 'c', 1),
            ('test_a', 'c'),
            ('group_teardown', 'c'),
            ('group_setup', 'd', 1),
            ('test_a', 'd'),
            ('group_teardown', 'd'),
        ],
    )
    self.assertEqual(len(instance.results.passed), 4)
    for record in instance.results.passed:
      self.assertEqual(record.test_name, 'test_a')

  def test_explicit_mode_synchronizes_participants(self):
    order = []
    lock = threading.Lock()

    class MockTest(base_test.BaseTestClass):

      def test_a(self):
        if self.current_device_id == 'slow':
          time.sleep(0.2)
        with lock:
          order.append(('before', self.current_device_id))
        with self.synchronized_context('sync', timeout=5):
          with lock:
            order.append(('after', self.current_device_id))
        self.synchronized_step('sync', timeout=5)

    self.configs.controller_configs = {
        'Foo': [{'group': 'g', 'id': 'fast'}, {'group': 'g', 'id': 'slow'}]
    }
    instance = self._run(MockTest)
    self.assertEqual([o[0] for o in order[:2]], ['before', 'before'])
    self.assertEqual([o[0] for o in order[2:]], ['after', 'after'])
    self.assertEqual(len(instance.results.passed), 2)

  def test_expect_failure_attributed_to_participant(self):

    class MockTest(base_test.BaseTestClass):

      def test_a(self):
        if self.current_device_id == 'bad':
          expects.expect_true(False, 'bad device')
        self.synchronized_step('done', timeout=5)

    self.configs.controller_configs = {
        'Foo': [{'group': 'g', 'id': 'good'}, {'group': 'g', 'id': 'bad'}]
    }
    instance = self._run(MockTest)
    self.assertEqual(len(instance.results.passed), 1)
    self.assertEqual(len(instance.results.failed), 1)
    self.assertIn('bad device', instance.results.failed[0].details)

  def test_sync_timeout(self):

    class MockTest(base_test.BaseTestClass):

      def test_a(self):
        if self.current_device_id == 'a':
          self.synchronized_step('lonely', timeout=0.2)
        else:
          time.sleep(1)

    self.configs.controller_configs = {
        'Foo': [{'group': 'g', 'id': 'a'}, {'group': 'g', 'id': 'b'}]
    }
    instance = self._run(MockTest)
    self.assertEqual(len(instance.results.error), 1)
    self.assertIn('lonely', instance.results.error[0].details)

  def test_sync_released_when_participant_fails(self):

    class MockTest(base_test.BaseTestClass):

      def test_a(self):
        if self.current_device_id == 'a':
          self.synchronized_step('never_reached_by_b')
        else:
          raise Exception('boom')

    self.configs.controller_configs = {
        'Foo': [{'group': 'g', 'id': 'a'}, {'group': 'g', 'id': 'b'}]
    }
    instance = self._run(MockTest)
    details = sorted(r.details for r in instance.results.error)
    self.assertEqual(len(details), 2)
    self.assertTrue(any('never_reached_by_b' in d for d in details))

  def test_sync_invalid_timeout(self):
    outer = self
    errors = []

    class MockTest(base_test.BaseTestClass):

      def test_a(self):
        with outer.assertRaises(ValueError):
          self.synchronized_step('s', timeout=-1)
        with outer.assertRaises(signals.TestError):
          self.synchronized_step('s', timeout=0)
        errors.append('checked')

    self._run(MockTest)
    self.assertEqual(errors, ['checked'])

  def test_sync_and_device_outside_allowed_contexts(self):
    errors = []

    class MockTest(base_test.BaseTestClass):

      def _check(self):
        for func in (
            lambda: self.synchronized_step('s'),
            lambda: self.synchronized_context('s').__enter__(),
        ):
          try:
            func()
          except signals.TestError as e:
            errors.append('synchronized_step' in str(e.details))
        for attr in ('current_device', 'current_device_id'):
          try:
            getattr(self, attr)
          except (AttributeError, RuntimeError):
            errors.append(True)

      def setup_class(self):
        self._check()

      def global_setup(self):
        self._check()

      def setup_test(self):
        self._check()

      def global_teardown(self):
        self._check()

      def test_a(self):
        pass

    self.configs.controller_configs = {'Foo': [{'group': 'g'}]}
    self._run(MockTest)
    self.assertEqual(errors, [True] * 16)

  def test_global_setup_failure(self):
    calls = []

    class MockTest(base_test.BaseTestClass):

      def global_setup(self):
        raise Exception('global failed')

      def group_setup(self, devices):
        calls.append('group_setup')

      def global_teardown(self):
        calls.append('global_teardown')

      def test_a(self):
        calls.append('test_a')

    self.configs.controller_configs = {'Foo': ['a']}
    instance = self._run(MockTest)
    self.assertEqual(calls, ['global_teardown'])
    self.assertEqual(instance.results.error[0].test_name, 'global_setup')
    self.assertEqual(len(instance.results.executed), 0)

  def test_group_setup_failure_continues_other_groups(self):
    calls = []
    lock = threading.Lock()

    class MockTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        if self.current_device_id == 'a':
          raise Exception('group failed')
        if self.current_device_id == 'b':
          return False

      def group_teardown(self, devices):
        with lock:
          calls.append(('group_teardown', self.current_device_id))

      def test_a(self):
        with lock:
          calls.append(('test_a', self.current_device_id))

    self.configs.controller_configs = {
        'Foo': [
            {'group': 'g1', 'id': 'a'},
            {'group': 'g2', 'id': 'b'},
            {'group': 'g3', 'id': 'c'},
        ]
    }
    instance = self._run(MockTest)
    self.assertEqual(
        calls,
        [
            ('group_teardown', 'a'),
            ('group_teardown', 'b'),
            ('test_a', 'c'),
            ('group_teardown', 'c'),
        ],
    )
    self.assertEqual(
        [r.test_name for r in instance.results.error],
        ['group_setup', 'group_setup'],
    )
    self.assertEqual(len(instance.results.skipped), 2)
    self.assertEqual(len(instance.results.passed), 1)

  def test_group_teardown_runs_when_tests_fail(self):
    calls = []

    class MockTest(base_test.BaseTestClass):

      def group_teardown(self, devices):
        calls.append('group_teardown')

      def test_a(self):
        raise Exception('test failed')

    self.configs.controller_configs = {'Foo': [{'group': 'g'}]}
    instance = self._run(MockTest)
    self.assertEqual(calls, ['group_teardown'])
    self.assertEqual(len(instance.results.error), 1)

  def test_barrier_reuse_after_completion(self):
    count = []
    lock = threading.Lock()

    class MockTest(base_test.BaseTestClass):

      def test_a(self):
        for _ in range(3):
          self.synchronized_step('loop', timeout=5)
          with lock:
            count.append(1)

    self.configs.controller_configs = {
        'Foo': [{'group': 'g'}, {'group': 'g'}, {'group': 'g'}]
    }
    instance = self._run(MockTest)
    self.assertEqual(len(count), 9)
    self.assertEqual(len(instance.results.passed), 3)


if __name__ == '__main__':
  unittest.main()
