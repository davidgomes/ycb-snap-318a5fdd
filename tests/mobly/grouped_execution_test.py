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

import os
import shutil
import tempfile
import threading
import time
import unittest

from mobly import base_test
from mobly import config_parser
from mobly import expects
from mobly import records
from mobly import signals
from tests.lib import mock_controller


def _call_log():
  log = []
  lock = threading.Lock()

  def add(item):
    with lock:
      log.append(item)

  return log, add


class GroupedExecutionTest(unittest.TestCase):

  def setUp(self):
    self.tmp_dir = tempfile.mkdtemp()
    self.config = config_parser.TestRunConfig()
    self.config.summary_writer = records.TestSummaryWriter(
        os.path.join(self.tmp_dir, 'summary.yaml')
    )
    self.config.controller_configs = {}
    self.config.log_path = self.tmp_dir
    self.config.user_params = {}

  def tearDown(self):
    shutil.rmtree(self.tmp_dir)

  def _config_with(self, entries, config_name='MagicDevice'):
    config = self.config.copy()
    config.controller_configs = {config_name: entries}
    return config

  def test_no_entries_runs_tests_once_and_skips_group_hooks(self):
    log, add = _call_log()

    class MockTest(base_test.BaseTestClass):

      def global_setup(self):
        add('global_setup')

      def global_teardown(self):
        add('global_teardown')

      def group_setup(self, devices):
        add(('group_setup', devices))

      def group_teardown(self, devices):
        add(('group_teardown', devices))

      def test_one(self):
        add('test_one')
        try:
          _ = self.current_device
          raise AssertionError('current_device should be unavailable')
        except AttributeError:
          pass
        try:
          _ = self.current_device_id
          raise AssertionError('current_device_id should be unavailable')
        except AttributeError:
          pass
        # Synchronization is a no-op when there are no participants.
        started = time.monotonic()
        self.synchronized_step('unused', timeout=5)
        if time.monotonic() - started >= 1:
          raise AssertionError('synchronized_step blocked without participants')

      def setup_class(self):
        try:
          _ = self.current_device
          raise AssertionError('current_device should be unavailable')
        except AttributeError:
          pass

    bt = MockTest(self.config.copy())
    bt.run()
    self.assertEqual(
        log, ['global_setup', 'test_one', 'global_teardown']
    )
    self.assertEqual(
        [record.test_name for record in bt.results.passed], ['test_one']
    )

  def test_implicit_group_runs_each_test_once(self):
    log, add = _call_log()
    entries = [
        {'serial': '1', 'id': 'phone-a'},
        {'serial': '2', 'id': 'phone-b'},
    ]

    class MockTest(base_test.BaseTestClass):

      def setup_class(self):
        self.devices = self.register_controller(mock_controller)

      def group_setup(self, devices):
        add(
            (
                'group_setup',
                devices,
                self.current_device,
                self.current_device_id,
            )
        )

      def group_teardown(self, devices):
        add(
            (
                'group_teardown',
                devices,
                self.current_device,
                self.current_device_id,
            )
        )

      def test_one(self):
        add(('test_one', self.current_device, self.current_device_id))

      def test_two(self):
        add(('test_two', self.current_device_id))

    bt = MockTest(self._config_with(entries))
    bt.run()
    self.assertEqual(len(bt.results.passed), 2)
    setup_devices = log[0][1]
    self.assertEqual(setup_devices, bt.devices)
    self.assertIs(log[0][2], bt.devices[0])
    self.assertEqual(log[0][3], 'phone-a')
    self.assertEqual(log[1][0], 'test_one')
    self.assertIs(log[1][1], bt.devices[0])
    self.assertEqual(log[1][2], 'phone-a')
    self.assertEqual(log[2], ('test_two', 'phone-a'))
    self.assertEqual(log[3][0], 'group_teardown')
    self.assertIs(log[3][2], bt.devices[0])

  def test_unregistered_entries_are_used_raw(self):
    seen = {}

    class MockTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        seen['devices'] = devices
        seen['current'] = self.current_device

      def test_one(self):
        seen['test_device'] = self.current_device

    entries = ['alpha', 'beta']
    bt = MockTest(self._config_with(entries))
    bt.run()
    self.assertEqual(seen['devices'], ['alpha', 'beta'])
    self.assertEqual(seen['current'], 'alpha')
    self.assertEqual(seen['test_device'], 'alpha')
    self.assertEqual(len(bt.results.passed), 1)

  def test_explicit_groups_run_participants_concurrently(self):
    log, add = _call_log()
    entries = [
        {'id': 'a', 'group': 'left'},
        {'id': 'b', 'group': 'right'},
        {'id': 'c', 'group': 'left'},
    ]

    class MockTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        add(('setup', self.current_device_id, [d['id'] for d in devices]))

      def group_teardown(self, devices):
        add(('teardown', self.current_device_id, [d['id'] for d in devices]))

      def test_sync(self):
        add(('arrive', self.current_device_id))
        self.synchronized_step('gate', timeout=2)
        add(('passed', self.current_device_id))

    bt = MockTest(self._config_with(entries))
    bt.run()
    names = [record.test_name for record in bt.results.passed]
    self.assertEqual(names, ['test_sync', 'test_sync', 'test_sync'])
    self.assertNotIn('[', ''.join(names))
    # left group (a, c) then right group (b), in config order.
    self.assertEqual(log[0], ('setup', 'a', ['a', 'c']))
    left_events = log[1:5]
    arrives = [item for item in left_events if item[0] == 'arrive']
    passed = [item for item in left_events if item[0] == 'passed']
    self.assertEqual({item[1] for item in arrives}, {'a', 'c'})
    self.assertEqual({item[1] for item in passed}, {'a', 'c'})
    first_pass = left_events.index(passed[0])
    last_arrive = max(left_events.index(item) for item in arrives)
    self.assertLess(last_arrive, first_pass)
    self.assertEqual(log[5], ('teardown', 'a', ['a', 'c']))
    self.assertEqual(log[6], ('setup', 'b', ['b']))
    self.assertEqual(log[-1], ('teardown', 'b', ['b']))

  def test_expectation_failures_stay_on_the_participant_record(self):
    entries = [
        {'id': 'a', 'group': 'g'},
        {'id': 'b', 'group': 'g'},
    ]

    class MockTest(base_test.BaseTestClass):

      def test_expect(self):
        if self.current_device_id == 'a':
          expects.expect_true(False, 'fail-a')
        else:
          expects.expect_true(True, 'ok-b')

    bt = MockTest(self._config_with(entries))
    bt.run()
    self.assertEqual(len(bt.results.failed), 1)
    self.assertEqual(len(bt.results.passed), 1)
    self.assertEqual(bt.results.failed[0].test_name, 'test_expect')
    self.assertEqual(bt.results.passed[0].test_name, 'test_expect')
    self.assertIn('fail-a', bt.results.failed[0].details)
    self.assertIsNone(bt.results.passed[0].details)

  def test_global_setup_error_skips_tests_and_runs_global_teardown(self):
    log, add = _call_log()

    class MockTest(base_test.BaseTestClass):

      def global_setup(self):
        add('global_setup')
        raise RuntimeError('boom')

      def setup_class(self):
        add('setup_class')

      def test_one(self):
        add('test_one')

      def teardown_class(self):
        add('teardown_class')

      def global_teardown(self):
        add('global_teardown')

    bt = MockTest(self.config.copy())
    bt.run()
    self.assertEqual(log, ['global_setup', 'teardown_class', 'global_teardown'])
    self.assertEqual(bt.results.error[0].test_name, 'global_setup')
    self.assertIn('boom', bt.results.error[0].details)
    self.assertEqual(len(bt.results.skipped), 1)
    self.assertEqual(bt.results.skipped[0].test_name, 'test_one')
    self.assertFalse(bt.results.executed)

  def test_group_setup_false_skips_that_group_and_continues(self):
    log, add = _call_log()
    entries = [
        {'id': 'a', 'group': 'skip-me'},
        {'id': 'b', 'group': 'run-me'},
    ]

    class MockTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        add(('setup', self.current_device_id))
        if self.current_device_id == 'a':
          return False

      def group_teardown(self, devices):
        add(('teardown', self.current_device_id))

      def test_one(self):
        add(('test', self.current_device_id))

    bt = MockTest(self._config_with(entries))
    bt.run()
    self.assertEqual(
        log,
        [
            ('setup', 'a'),
            ('teardown', 'a'),
            ('setup', 'b'),
            ('test', 'b'),
            ('teardown', 'b'),
        ],
    )
    self.assertEqual(len(bt.results.passed), 1)
    self.assertEqual(bt.results.passed[0].test_name, 'test_one')

  def test_group_setup_error_skips_group_but_not_others(self):
    log, add = _call_log()
    entries = [
        {'id': 'a', 'group': 'bad'},
        {'id': 'b', 'group': 'good'},
    ]

    class MockTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        add(('setup', self.current_device_id))
        if self.current_device_id == 'a':
          raise RuntimeError('setup-failed')

      def group_teardown(self, devices):
        add(('teardown', self.current_device_id))

      def test_one(self):
        add(('test', self.current_device_id))
        raise AssertionError('test-failed')

    bt = MockTest(self._config_with(entries))
    bt.run()
    self.assertEqual(
        [item[0] for item in log if item[0] == 'test'], ['test']
    )
    self.assertIn(('teardown', 'a'), log)
    self.assertIn(('teardown', 'b'), log)
    self.assertTrue(
        any(record.test_name == 'group_setup' for record in bt.results.error)
    )
    self.assertEqual(bt.results.failed[0].test_name, 'test_one')

  def test_group_teardown_runs_when_tests_fail(self):
    log, add = _call_log()

    class MockTest(base_test.BaseTestClass):

      def group_teardown(self, devices):
        add('teardown')

      def test_one(self):
        add('test')
        raise AssertionError('nope')

    entries = [{'id': 'a'}]
    bt = MockTest(self._config_with(entries))
    bt.run()
    self.assertEqual(log, ['test', 'teardown'])
    self.assertEqual(len(bt.results.failed), 1)

  def test_synchronized_step_rejected_outside_allowed_phases(self):
    caught = {}

    class MockTest(base_test.BaseTestClass):

      def setup_class(self):
        try:
          self.synchronized_step('nope')
        except signals.TestError as err:
          caught['step'] = err
        try:
          with self.synchronized_context('nope'):
            pass
        except signals.TestError as err:
          caught['context'] = err

      def test_one(self):
        pass

    bt = MockTest(self.config.copy())
    bt.run()
    self.assertIn('synchronized_step', caught['step'].details)
    self.assertIn('synchronized_step', caught['context'].details)
    self.assertTrue(bt.results.is_all_pass)

  def test_negative_timeout_raises_value_error(self):
    class MockTest(base_test.BaseTestClass):

      def test_one(self):
        try:
          self.synchronized_step('gate', timeout=-1)
          raise AssertionError('negative timeout should raise ValueError')
        except ValueError:
          pass
        try:
          with self.synchronized_context('gate', timeout=-0.1):
            pass
          raise AssertionError('negative timeout should raise ValueError')
        except ValueError:
          pass

    bt = MockTest(self.config.copy())
    bt.run()
    self.assertTrue(bt.results.is_all_pass)

  def test_zero_timeout_raises_test_error(self):
    entries = [{'id': 'a', 'group': 'g'}, {'id': 'b', 'group': 'g'}]
    errors = []

    class MockTest(base_test.BaseTestClass):

      def test_one(self):
        try:
          self.synchronized_step('gate', timeout=0)
          raise AssertionError('timeout 0 should raise TestError')
        except signals.TestError as err:
          errors.append(err.details)

    bt = MockTest(self._config_with(entries))
    bt.run()
    self.assertEqual(len(errors), 2)
    for details in errors:
      self.assertIn('gate', details)
      self.assertIn('synchronized_step', details)
    self.assertTrue(bt.results.is_all_pass)

  def test_timeout_releases_other_waiters(self):
    entries = [{'id': 'a', 'group': 'g'}, {'id': 'b', 'group': 'g'}]
    details = []
    lock = threading.Lock()

    class MockTest(base_test.BaseTestClass):

      def test_one(self):
        if self.current_device_id == 'b':
          time.sleep(0.6)
        try:
          self.synchronized_step('gate', timeout=0.2)
        except signals.TestError as err:
          with lock:
            details.append(err.details)

    started = time.monotonic()
    bt = MockTest(self._config_with(entries))
    bt.run()
    elapsed = time.monotonic() - started
    self.assertLess(elapsed, 2)
    self.assertEqual(len(details), 2)
    for item in details:
      self.assertIn('gate', item)
    self.assertEqual(len(bt.results.passed), 2)

  def test_participant_exception_releases_waiters(self):
    entries = [{'id': 'a', 'group': 'g'}, {'id': 'b', 'group': 'g'}]

    class MockTest(base_test.BaseTestClass):

      def test_one(self):
        if self.current_device_id == 'a':
          raise RuntimeError('participant-a-failed')
        self.synchronized_step('gate', timeout=2)

    started = time.monotonic()
    bt = MockTest(self._config_with(entries))
    bt.run()
    self.assertLess(time.monotonic() - started, 2)
    self.assertEqual(len(bt.results.error), 2)
    messages = ' '.join(record.details for record in bt.results.error)
    self.assertIn('participant-a-failed', messages)
    self.assertIn('gate', messages)

  def test_synchronized_context_syncs_on_entry_only(self):
    entries = [{'id': 'a', 'group': 'g'}, {'id': 'b', 'group': 'g'}]
    log, add = _call_log()

    class MockTest(base_test.BaseTestClass):

      def test_one(self):
        with self.synchronized_context('gate', timeout=2):
          if self.current_device_id == 'a':
            time.sleep(0.4)
            add('a-inside-done')
          else:
            add('b-entered')
        add('%s-after' % self.current_device_id)

    bt = MockTest(self._config_with(entries))
    bt.run()
    self.assertLess(log.index('b-after'), log.index('a-inside-done'))
    self.assertTrue(bt.results.is_all_pass)

  def test_reused_sync_name_creates_a_new_barrier(self):
    entries = [{'id': 'a', 'group': 'g'}, {'id': 'b', 'group': 'g'}]
    log, add = _call_log()

    class MockTest(base_test.BaseTestClass):

      def test_one(self):
        self.synchronized_step('gate', timeout=2)
        if self.current_device_id == 'b':
          time.sleep(0.3)
        add(('arrive', self.current_device_id))
        self.synchronized_step('gate', timeout=2)
        add(('passed', self.current_device_id))

    bt = MockTest(self._config_with(entries))
    bt.run()
    arrives = [item for item in log if item[0] == 'arrive']
    passed = [item for item in log if item[0] == 'passed']
    self.assertEqual(len(arrives), 2)
    self.assertEqual(len(passed), 2)
    self.assertLess(
        max(log.index(item) for item in arrives), log.index(passed[0])
    )
    self.assertTrue(bt.results.is_all_pass)

  def test_sync_in_group_hooks_does_not_block(self):
    entries = [{'id': 'a', 'group': 'g'}, {'id': 'b', 'group': 'g'}]

    class MockTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        started = time.monotonic()
        self.synchronized_step('setup-gate', timeout=5)
        with self.synchronized_context('setup-ctx', timeout=5):
          pass
        if time.monotonic() - started > 1:
          raise AssertionError('group_setup synchronization blocked')

      def group_teardown(self, devices):
        self.synchronized_step('teardown-gate', timeout=5)

      def test_one(self):
        if self.current_device['id'] != self.current_device_id:
          raise AssertionError('current_device_id does not match the entry')

    bt = MockTest(self._config_with(entries))
    bt.run()
    self.assertEqual(len(bt.results.passed), 2)

  def test_mismatched_registration_uses_raw_entries(self):
    seen = {}

    class MockTest(base_test.BaseTestClass):

      def setup_class(self):
        # Two controller types are configured, but only one is registered,
        # so objects cannot be paired 1:1 with entries.
        self.register_controller(mock_controller, min_number=1)

      def group_setup(self, devices):
        seen['devices'] = list(devices)

      def test_one(self):
        pass

    config = self.config.copy()
    config.controller_configs = {
        mock_controller.MOBLY_CONTROLLER_CONFIG_NAME: [
            {'serial': '1', 'id': 'kept'}
        ],
        'OtherDevice': [{'id': 'other'}],
    }
    bt = MockTest(config)
    bt.run()
    self.assertEqual(
        seen['devices'],
        [{'serial': '1', 'id': 'kept'}, {'id': 'other'}],
    )
    self.assertTrue(bt.results.is_all_pass)

  def test_registered_objects_are_paired_when_counts_match(self):
    seen = {}

    class MockTest(base_test.BaseTestClass):

      def setup_class(self):
        self.devices = self.register_controller(mock_controller)

      def group_setup(self, devices):
        seen['devices'] = list(devices)
        seen['id'] = self.current_device_id

      def test_one(self):
        seen['test_id'] = self.current_device_id
        seen['test_device'] = self.current_device

    entries = [
        {'serial': '1', 'id': 'first'},
        {'serial': '2', 'id': 'second'},
    ]
    bt = MockTest(
        self._config_with(
            entries, mock_controller.MOBLY_CONTROLLER_CONFIG_NAME
        )
    )
    bt.run()
    self.assertIs(seen['devices'][0], bt.devices[0])
    self.assertIs(seen['devices'][1], bt.devices[1])
    self.assertEqual(seen['id'], 'first')
    self.assertEqual(seen['test_id'], 'first')
    self.assertIs(seen['test_device'], bt.devices[0])

  def test_explicit_partial_group_key_defaults_missing_group(self):
    log, add = _call_log()
    entries = [{'id': 'bare'}, {'id': 'named', 'group': 'other'}]

    class MockTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        add(('setup', self.current_device_id, [d['id'] for d in devices]))

      def test_one(self):
        add(('test', self.current_device_id))

      def group_teardown(self, devices):
        add(('teardown', self.current_device_id))

    bt = MockTest(self._config_with(entries))
    bt.run()
    self.assertEqual(log[0], ('setup', 'bare', ['bare']))
    self.assertEqual(log[1], ('test', 'bare'))
    self.assertEqual(log[2], ('teardown', 'bare'))
    self.assertEqual(log[3], ('setup', 'named', ['named']))
    self.assertEqual(log[4], ('test', 'named'))


if __name__ == '__main__':
  unittest.main()
