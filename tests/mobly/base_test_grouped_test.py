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
"""Tests for grouped execution and synchronization in `base_test`."""

import os
import shutil
import tempfile
import threading
import time
import unittest

from mobly import asserts
from mobly import base_test
from mobly import config_parser
from mobly import expects
from mobly import records
from mobly import signals
from tests.lib import mock_controller


def _ids(records_list):
  return sorted(record.test_name for record in records_list)


class BaseTestGroupedTest(unittest.TestCase):

  def setUp(self):
    self.tmp_dir = tempfile.mkdtemp()
    self.configs = config_parser.TestRunConfig()
    self.configs.summary_writer = records.TestSummaryWriter(
        os.path.join(self.tmp_dir, 'summary.yaml')
    )
    self.configs.controller_configs = {}
    self.configs.log_path = self.tmp_dir
    self.configs.user_params = {}

  def tearDown(self):
    shutil.rmtree(self.tmp_dir)

  def _set_entries(self, entries):
    self.configs.controller_configs = {
        mock_controller.MOBLY_CONTROLLER_CONFIG_NAME: entries
    }

  def test_no_entries_runs_global_hooks_only(self):
    calls = []

    class MockBaseTest(base_test.BaseTestClass):

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
        for attr in ('current_device', 'current_device_id'):
          try:
            getattr(self, attr)
            calls.append(f'{attr} available')
          except AttributeError:
            pass
        self.synchronized_step('step')
        with self.synchronized_context('context'):
          pass

    bt_cls = MockBaseTest(self.configs)
    bt_cls.run()
    self.assertEqual(calls, ['global_setup', 'test_a', 'global_teardown'])
    self.assertEqual(len(bt_cls.results.passed), 1)

  def test_implicit_group_with_registered_objects(self):
    self._set_entries([
        {'serial': 1, 'id': 'a'},
        {'serial': 2, 'id': 'b'},
    ])
    calls = []

    class MockBaseTest(base_test.BaseTestClass):

      def setup_class(self):
        self.objects = self.register_controller(mock_controller)

      def group_setup(self, devices):
        calls.append(('group_setup', devices, self.current_device_id))
        asserts.assert_is(self.current_device, devices[0])
        self.synchronized_step('never_blocks')

      def group_teardown(self, devices):
        calls.append(('group_teardown', devices, self.current_device_id))

      def test_a(self):
        calls.append(('test_a', self.current_device, self.current_device_id))
        self.synchronized_step('noop')

    bt_cls = MockBaseTest(self.configs)
    bt_cls.run()
    objects = bt_cls.objects
    self.assertEqual(
        calls,
        [
            ('group_setup', objects, 'a'),
            ('test_a', objects[0], 'a'),
            ('group_teardown', objects, 'a'),
        ],
    )
    self.assertEqual(len(bt_cls.results.passed), 1)

  def test_implicit_group_uses_raw_entries_without_registration(self):
    entries = [{'id': 'a'}, 'raw']
    self._set_entries(entries)
    seen = []

    class MockBaseTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        seen.append(devices)

      def test_a(self):
        seen.append(self.current_device)

    MockBaseTest(self.configs).run()
    self.assertEqual(seen, [entries, entries[0]])

  def test_explicit_groups_run_tests_per_participant(self):
    self._set_entries([
        {'serial': 1, 'id': 'a', 'group': 'g1'},
        {'serial': 2, 'id': 'b', 'group': 'g2'},
        {'serial': 3, 'id': 'c', 'group': 'g1'},
        {'serial': 4, 'id': 'd'},
    ])
    lock = threading.Lock()
    group_calls = []
    test_calls = []

    class MockBaseTest(base_test.BaseTestClass):

      def setup_class(self):
        self.register_controller(mock_controller)

      def group_setup(self, devices):
        ids = [d.magic['id'] for d in devices]
        group_calls.append(('setup', ids, self.current_device_id))

      def group_teardown(self, devices):
        ids = [d.magic['id'] for d in devices]
        group_calls.append(('teardown', ids, self.current_device_id))

      def test_a(self):
        with lock:
          test_calls.append((
              self.current_device_id,
              self.current_device.magic['id'],
              self.current_test_info.name,
          ))

    bt_cls = MockBaseTest(self.configs)
    bt_cls.run()
    self.assertEqual(
        group_calls,
        [
            ('setup', ['a', 'c'], 'a'),
            ('teardown', ['a', 'c'], 'a'),
            ('setup', ['b'], 'b'),
            ('teardown', ['b'], 'b'),
            ('setup', ['d'], 'd'),
            ('teardown', ['d'], 'd'),
        ],
    )
    self.assertEqual(
        sorted(test_calls),
        [(i, i, 'test_a') for i in ('a', 'b', 'c', 'd')],
    )
    self.assertEqual(_ids(bt_cls.results.passed), ['test_a'] * 4)

  def test_explicit_expectation_attributed_to_participant(self):
    self._set_entries([
        {'id': 'a', 'group': 'g'},
        {'id': 'b', 'group': 'g'},
    ])

    class MockBaseTest(base_test.BaseTestClass):

      def test_a(self):
        # Force both participants to record concurrently.
        self.synchronized_step('start', timeout=5)
        expects.expect_equal(self.current_device_id, 'a', 'wrong device')
        self.synchronized_step('end', timeout=5)

    bt_cls = MockBaseTest(self.configs)
    bt_cls.run()
    self.assertEqual(len(bt_cls.results.passed), 1)
    self.assertEqual(len(bt_cls.results.failed), 1)
    failed = bt_cls.results.failed[0]
    self.assertEqual(failed.test_name, 'test_a')
    self.assertIn("'b' != 'a'", failed.details)
    self.assertEqual(bt_cls.results.passed[0].extra_errors, {})

  def test_synchronized_step_blocks_until_all_arrive(self):
    self._set_entries([
        {'id': 'fast', 'group': 'g'},
        {'id': 'slow', 'group': 'g'},
    ])
    events = []
    lock = threading.Lock()

    class MockBaseTest(base_test.BaseTestClass):

      def test_a(self):
        for i in range(3):
          if self.current_device_id == 'slow':
            time.sleep(0.05)
          with lock:
            events.append(('before', i, self.current_device_id))
          self.synchronized_step('step', timeout=5)
          with lock:
            events.append(('after', i, self.current_device_id))

    bt_cls = MockBaseTest(self.configs)
    bt_cls.run()
    self.assertEqual(len(bt_cls.results.passed), 2)
    for i in range(3):
      befores = [n for n, e in enumerate(events) if e[:2] == ('before', i)]
      afters = [n for n, e in enumerate(events) if e[:2] == ('after', i)]
      self.assertLess(max(befores), min(afters))

  def test_synchronized_context_syncs_on_entry(self):
    self._set_entries([
        {'id': 'a', 'group': 'g'},
        {'id': 'b', 'group': 'g'},
    ])
    arrived = []

    class MockBaseTest(base_test.BaseTestClass):

      def test_a(self):
        arrived.append(self.current_device_id)
        with self.synchronized_context('ctx', timeout=5):
          asserts.assert_equal(len(arrived), 2)

    bt_cls = MockBaseTest(self.configs)
    bt_cls.run()
    self.assertEqual(len(bt_cls.results.passed), 2)

  def test_synchronized_step_timeout(self):
    self._set_entries([
        {'id': 'a', 'group': 'g'},
        {'id': 'b', 'group': 'g'},
    ])

    class MockBaseTest(base_test.BaseTestClass):

      def test_a(self):
        if self.current_device_id == 'b':
          time.sleep(0.5)
        self.synchronized_step('my_step', timeout=0.1)

    bt_cls = MockBaseTest(self.configs)
    bt_cls.run()
    self.assertEqual(len(bt_cls.results.error), 2)
    for record in bt_cls.results.error:
      self.assertIn('my_step', record.details)

  def test_synchronized_step_released_when_participant_fails(self):
    self._set_entries([
        {'id': 'a', 'group': 'g'},
        {'id': 'b', 'group': 'g'},
    ])

    class MockBaseTest(base_test.BaseTestClass):

      def test_a(self):
        if self.current_device_id == 'b':
          time.sleep(0.05)
          raise Exception('b failed')
        self.synchronized_step('my_step')

    bt_cls = MockBaseTest(self.configs)
    bt_cls.run()
    details = sorted(record.details for record in bt_cls.results.error)
    self.assertEqual(len(details), 2)
    self.assertIn('my_step', details[0])
    self.assertEqual(details[1], 'b failed')

  def test_synchronized_step_invalid_timeout(self):
    captured = {}

    class MockBaseTest(base_test.BaseTestClass):

      def test_a(self):
        try:
          self.synchronized_step('step', timeout=-1)
        except ValueError as e:
          captured['negative'] = e
        try:
          self.synchronized_step('zero_step', timeout=0)
        except signals.TestError as e:
          captured['zero'] = e

    MockBaseTest(self.configs).run()
    self.assertIsInstance(captured['negative'], ValueError)
    self.assertIn('zero_step', captured['zero'].details)

  def test_context_unavailable_outside_allowed_stages(self):
    self._set_entries([{'id': 'a', 'group': 'g'}])
    captured = []

    def check(test):
      for attr in ('current_device', 'current_device_id'):
        try:
          getattr(test, attr)
          captured.append(f'{attr} available')
        except (AttributeError, RuntimeError):
          pass
      for func in (
          lambda: test.synchronized_step('s'),
          lambda: test.synchronized_context('s').__enter__(),
      ):
        try:
          func()
          captured.append('sync allowed')
        except signals.TestError as e:
          if 'synchronized_step' not in e.details:
            captured.append(e.details)

    class MockBaseTest(base_test.BaseTestClass):

      def setup_class(self):
        check(self)

      def global_setup(self):
        check(self)

      def setup_test(self):
        check(self)

      def teardown_test(self):
        check(self)

      def global_teardown(self):
        check(self)

      def teardown_class(self):
        check(self)

      def test_a(self):
        pass

    bt_cls = MockBaseTest(self.configs)
    bt_cls.run()
    self.assertEqual(captured, [])
    self.assertEqual(len(bt_cls.results.passed), 1)

  def test_global_setup_error(self):
    self._set_entries([{'id': 'a'}])
    calls = []

    class MockBaseTest(base_test.BaseTestClass):

      def global_setup(self):
        raise Exception('global_setup failed')

      def group_setup(self, devices):
        calls.append('group_setup')

      def global_teardown(self):
        calls.append('global_teardown')

      def test_a(self):
        calls.append('test_a')

    bt_cls = MockBaseTest(self.configs)
    bt_cls.run()
    self.assertEqual(calls, ['global_teardown'])
    self.assertEqual(len(bt_cls.results.error), 1)
    self.assertEqual(bt_cls.results.error[0].test_name, 'global_setup')
    self.assertEqual(bt_cls.results.passed, [])
    self.assertEqual(_ids(bt_cls.results.skipped), ['test_a'])

  def test_group_setup_failure_skips_only_that_group(self):
    self._set_entries([
        {'id': 'a', 'group': 'broken'},
        {'id': 'b', 'group': 'false'},
        {'id': 'c', 'group': 'ok'},
    ])
    calls = []

    class MockBaseTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        if self.current_device_id == 'a':
          raise Exception('group_setup failed')
        if self.current_device_id == 'b':
          return False

      def group_teardown(self, devices):
        calls.append(('group_teardown', self.current_device_id))

      def test_a(self):
        calls.append(('test_a', self.current_device_id))

    bt_cls = MockBaseTest(self.configs)
    bt_cls.run()
    self.assertEqual(
        calls,
        [
            ('group_teardown', 'a'),
            ('group_teardown', 'b'),
            ('test_a', 'c'),
            ('group_teardown', 'c'),
        ],
    )
    self.assertEqual(len(bt_cls.results.error), 1)
    self.assertEqual(bt_cls.results.error[0].test_name, 'group_setup')
    self.assertEqual(_ids(bt_cls.results.skipped), ['test_a', 'test_a'])
    self.assertEqual(_ids(bt_cls.results.passed), ['test_a'])

  def test_group_teardown_runs_when_tests_fail(self):
    self._set_entries([{'id': 'a'}])
    calls = []

    class MockBaseTest(base_test.BaseTestClass):

      def group_teardown(self, devices):
        calls.append('group_teardown')

      def test_a(self):
        asserts.fail('failed')

    bt_cls = MockBaseTest(self.configs)
    bt_cls.run()
    self.assertEqual(calls, ['group_teardown'])
    self.assertEqual(_ids(bt_cls.results.failed), ['test_a'])


if __name__ == '__main__':
  unittest.main()
