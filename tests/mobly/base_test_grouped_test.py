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

import collections
import os
import shutil
import tempfile
import threading
import time
import unittest
from unittest import mock

from mobly import asserts
from mobly import base_test
from mobly import config_parser
from mobly import expects
from mobly import records
from mobly import signals
from tests.lib import mock_controller

MSG_EXPECTED_EXCEPTION = 'This is an expected exception.'
CONFIG_NAME = mock_controller.MOBLY_CONTROLLER_CONFIG_NAME


class BaseTestGroupedTest(unittest.TestCase):

  def setUp(self):
    self.tmp_dir = tempfile.mkdtemp()
    self.config = config_parser.TestRunConfig()
    self.config.summary_writer = records.TestSummaryWriter(
        os.path.join(self.tmp_dir, 'summary.yaml')
    )
    self.config.controller_configs = {}
    self.config.log_path = self.tmp_dir
    self.calls = []
    self.lock = threading.Lock()

  def tearDown(self):
    shutil.rmtree(self.tmp_dir)

  def record_call(self, *call):
    with self.lock:
      self.calls.append(call)

  def run_test_class(self, test_class, entries=None):
    if entries is not None:
      self.config.controller_configs[CONFIG_NAME] = entries
    test_instance = test_class(self.config)
    test_instance.run()
    return test_instance.results

  def assert_raises_device_context_error(self, test_instance):
    for attr_name in ('current_device', 'current_device_id'):
      with self.assertRaises(AttributeError):
        getattr(test_instance, attr_name)
      with self.assertRaises(RuntimeError):
        getattr(test_instance, attr_name)
      self.assertFalse(hasattr(test_instance, attr_name))

  def test_hook_order_implicit(self):
    test = self

    class MockTest(base_test.BaseTestClass):

      def setup_class(self):
        test.record_call('setup_class')

      def global_setup(self):
        test.record_call('global_setup')

      def group_setup(self, devices):
        test.record_call('group_setup', devices)

      def setup_test(self):
        test.record_call('setup_test')

      def test_a(self):
        test.record_call('test_a')

      def teardown_test(self):
        test.record_call('teardown_test')

      def group_teardown(self, devices):
        test.record_call('group_teardown', devices)

      def global_teardown(self):
        test.record_call('global_teardown')

      def teardown_class(self):
        test.record_call('teardown_class')

    results = self.run_test_class(MockTest, ['a', 'b'])
    self.assertEqual(
        self.calls,
        [
            ('setup_class',),
            ('global_setup',),
            ('group_setup', ['a', 'b']),
            ('setup_test',),
            ('test_a',),
            ('teardown_test',),
            ('group_teardown', ['a', 'b']),
            ('global_teardown',),
            ('teardown_class',),
        ],
    )
    self.assertEqual(
        results.summary_str(),
        'Error 0, Executed 1, Failed 0, Passed 1, Requested 1, Skipped 0',
    )

  def test_no_entries(self):
    test = self

    class MockTest(base_test.BaseTestClass):

      def global_setup(self):
        test.record_call('global_setup')

      def group_setup(self, devices):
        test.record_call('group_setup')

      def test_a(self):
        test.assert_raises_device_context_error(self)
        self.synchronized_step('step')
        with self.synchronized_context('context'):
          pass
        test.record_call('test_a')

      def test_b(self):
        test.record_call('test_b')

      def group_teardown(self, devices):
        test.record_call('group_teardown')

      def global_teardown(self):
        test.record_call('global_teardown')

    results = self.run_test_class(MockTest)
    self.assertEqual(
        self.calls,
        [('global_setup',), ('test_a',), ('test_b',), ('global_teardown',)],
    )
    self.assertEqual(len(results.passed), 2)
    self.assertTrue(results.is_all_pass)

  def test_empty_entry_list_has_no_entries(self):
    test = self

    class MockTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        test.record_call('group_setup')

      def test_a(self):
        test.record_call('test_a')

    results = self.run_test_class(MockTest, [])
    self.assertEqual(self.calls, [('test_a',)])
    self.assertEqual(len(results.passed), 1)

  def test_implicit_runs_each_test_once(self):
    test = self

    class MockTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        test.record_call(
            'group_setup', devices, self.current_device, self.current_device_id
        )

      def test_a(self):
        test.record_call('test_a', self.current_device, self.current_device_id)

      def test_b(self):
        test.record_call('test_b')

      def group_teardown(self, devices):
        test.record_call('group_teardown', devices, self.current_device)

    entries = [{'id': 1, 'magic': 'x'}, 'raw', {'magic': 'y'}]
    results = self.run_test_class(MockTest, entries)
    self.assertEqual(
        self.calls,
        [
            ('group_setup', entries, entries[0], 1),
            ('test_a', entries[0], 1),
            ('test_b',),
            ('group_teardown', entries, entries[0]),
        ],
    )
    self.assertEqual(
        [record.test_name for record in results.passed], ['test_a', 'test_b']
    )

  def test_implicit_uses_registered_objects(self):
    test = self

    class MockTest(base_test.BaseTestClass):

      def setup_class(self):
        self.magic_devices = self.register_controller(mock_controller)

      def group_setup(self, devices):
        test.record_call('group_setup', devices, self.magic_devices)

      def test_a(self):
        test.record_call('test_a', self.current_device, self.current_device_id)

    entries = [
        {'serial': 's1', 'id': 'first', 'magic': 1},
        {'serial': 's2', 'magic': 2},
    ]
    results = self.run_test_class(MockTest, entries)
    self.assertTrue(results.is_all_pass)
    (_, devices, magic_devices), (_, current_device, current_id) = self.calls
    self.assertEqual(devices, magic_devices)
    self.assertIsInstance(devices[0], mock_controller.MagicDevice)
    self.assertIs(current_device, magic_devices[0])
    self.assertEqual(current_id, 'first')

  def test_uses_raw_entries_if_objects_do_not_pair(self):
    test = self

    class MockTest(base_test.BaseTestClass):

      def setup_class(self):
        self.register_controller(mock_controller)

      def group_setup(self, devices):
        test.record_call('group_setup', devices)

      def test_a(self):
        pass

    entries = [{'serial': 's1', 'group': 'g'}, {'serial': 's2', 'group': 'g'}]
    create_one = lambda configs: [mock_controller.MagicDevice(configs[0])]
    with mock.patch.object(mock_controller, 'create', side_effect=create_one):
      self.run_test_class(MockTest, entries)
    self.assertEqual(self.calls, [('group_setup', entries)])

  def test_explicit_runs_tests_per_participant_concurrently(self):
    test = self

    class MockTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        test.record_call(
            'group_setup', devices, self.current_device, self.current_device_id
        )

      def test_a(self):
        asserts.assert_equal(self.current_test_info.name, 'test_a')
        # Only returns if all participants of the group execute concurrently.
        self.synchronized_step('start', timeout=5)
        test.record_call(
            'test_a', self.current_device['name'], self.current_device_id
        )

      def group_teardown(self, devices):
        test.record_call(
            'group_teardown',
            devices,
            self.current_device,
            self.current_device_id,
        )

    entries = [
        {'group': 'g1', 'id': 1, 'name': 'a'},
        {'group': 'g2', 'id': 2, 'name': 'b'},
        {'group': 'g1', 'id': 3, 'name': 'c'},
        {'name': 'd'},
    ]
    g1 = [entries[0], entries[2]]
    results = self.run_test_class(MockTest, entries)
    self.assertEqual(self.calls[0], ('group_setup', g1, entries[0], 1))
    self.assertCountEqual(
        self.calls[1:3], [('test_a', 'a', 1), ('test_a', 'c', 3)]
    )
    self.assertEqual(
        self.calls[3:],
        [
            ('group_teardown', g1, entries[0], 1),
            ('group_setup', [entries[1]], entries[1], 2),
            ('test_a', 'b', 2),
            ('group_teardown', [entries[1]], entries[1], 2),
            ('group_setup', [entries[3]], entries[3], None),
            ('test_a', 'd', None),
            ('group_teardown', [entries[3]], entries[3], None),
        ],
    )
    self.assertEqual(
        [record.test_name for record in results.passed], ['test_a'] * 4
    )
    self.assertEqual(results.error, [])

  def test_explicit_attributes_expectation_failures_to_participants(self):

    class MockTest(base_test.BaseTestClass):

      def test_a(self):
        self.synchronized_step('start', timeout=5)
        expects.expect_true(
            self.current_device_id != 2, 'failed on %s' % self.current_device_id
        )
        # Keep both participants in the test until both have checked.
        self.synchronized_step('checked', timeout=5)

    entries = [{'group': 'g', 'id': 1}, {'group': 'g', 'id': 2}]
    results = self.run_test_class(MockTest, entries)
    self.assertEqual(len(results.passed), 1)
    self.assertEqual(len(results.failed), 1)
    self.assertEqual(results.failed[0].test_name, 'test_a')
    self.assertEqual(results.failed[0].details, 'failed on 2')
    self.assertEqual(results.passed[0].extra_errors, {})

  def test_device_context_unavailable_outside_group_stages(self):
    test = self

    class MockTest(base_test.BaseTestClass):

      def setup_class(self):
        test.assert_raises_device_context_error(self)

      def global_setup(self):
        test.assert_raises_device_context_error(self)

      def setup_test(self):
        test.assert_raises_device_context_error(self)

      def test_a(self):
        test.record_call('test_a', self.current_device_id)

      def teardown_test(self):
        test.assert_raises_device_context_error(self)

      def global_teardown(self):
        test.assert_raises_device_context_error(self)

      def teardown_class(self):
        test.assert_raises_device_context_error(self)

    for entries in ([{'id': 7}], [{'id': 7, 'group': 'g'}]):
      self.calls = []
      results = self.run_test_class(MockTest, entries)
      self.assertEqual(results.error, [])
      self.assertEqual(self.calls, [('test_a', 7)])

  def test_synchronized_step_not_allowed_outside_group_stages(self):
    test = self
    errors = []

    def check(test_instance):
      for sync in (
          test_instance.synchronized_step,
          lambda name: test_instance.synchronized_context(name).__enter__(),
      ):
        with test.assertRaises(signals.TestError) as context:
          sync('step')
        errors.append(context.exception.details)

    class MockTest(base_test.BaseTestClass):

      def setup_class(self):
        check(self)

      def global_setup(self):
        check(self)

      def setup_test(self):
        check(self)

      def test_a(self):
        pass

      def global_teardown(self):
        check(self)

    results = self.run_test_class(MockTest, [{'group': 'g'}, {'group': 'g'}])
    self.assertEqual(results.error, [])
    self.assertEqual(len(results.passed), 2)
    # 5 checks: setup_class, global_setup, global_teardown, and setup_test of
    # each participant.
    self.assertEqual(len(errors), 5 * 2)
    for details in errors:
      self.assertIn('synchronized_step', details)

  def test_synchronized_step_never_blocks_in_group_hooks(self):
    test = self

    class MockTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        self.synchronized_step('setup')
        with self.synchronized_context('setup_context', timeout=1):
          test.record_call('group_setup')

      def test_a(self):
        pass

      def group_teardown(self, devices):
        self.synchronized_step('teardown')
        test.record_call('group_teardown')

    results = self.run_test_class(MockTest, [{'group': 'g'}, {'group': 'g'}])
    self.assertEqual(self.calls, [('group_setup',), ('group_teardown',)])
    self.assertEqual(results.error, [])

  def test_synchronized_step_is_no_op_in_implicit_tests(self):

    class MockTest(base_test.BaseTestClass):

      def test_a(self):
        self.synchronized_step('step')

    results = self.run_test_class(MockTest, ['a', 'b'])
    self.assertEqual(len(results.passed), 1)

  def test_synchronized_step_barrier_is_reusable(self):
    test = self

    class MockTest(base_test.BaseTestClass):

      def test_a(self):
        for i in range(3):
          test.record_call('before', i)
          self.synchronized_step('loop', timeout=5)
          with test.lock:
            test.assertEqual(
                sum(1 for call in test.calls if call == ('before', i)), 2
            )
          self.synchronized_step('after', timeout=5)

      def test_b(self):
        self.synchronized_step('loop', timeout=5)

    results = self.run_test_class(MockTest, [{'group': 'g'}, {'group': 'g'}])
    self.assertEqual(len(results.passed), 4, results.error + results.failed)

  def test_synchronized_context_syncs_on_entry_only(self):
    test = self
    exited = threading.Event()

    class MockTest(base_test.BaseTestClass):

      def test_a(self):
        with self.synchronized_context('context', timeout=5):
          test.record_call('entered', self.current_device_id)
          if self.current_device_id == 1:
            # Participant 2 can only exit first if exiting does not sync.
            asserts.assert_true(exited.wait(5), 'Exiting synchronized.')
        exited.set()

    results = self.run_test_class(
        MockTest, [{'group': 'g', 'id': 1}, {'group': 'g', 'id': 2}]
    )
    self.assertCountEqual(self.calls, [('entered', 1), ('entered', 2)])
    self.assertEqual(len(results.passed), 2, results.error + results.failed)

  def test_synchronized_step_invalid_timeout(self):
    test = self

    class MockTest(base_test.BaseTestClass):

      def test_a(self):
        with test.assertRaises(ValueError):
          self.synchronized_step('negative', timeout=-1)
        with test.assertRaises(signals.TestError) as context:
          self.synchronized_step('zero', timeout=0)
        test.assertIn('zero', context.exception.details)

    for entries in (None, ['a'], [{'group': 'g'}, {'group': 'g'}]):
      results = self.run_test_class(MockTest, entries)
      self.assertTrue(results.is_all_pass)

  def test_synchronized_step_timeout_releases_waiters(self):
    test = self
    release_slow = threading.Event()

    class MockTest(base_test.BaseTestClass):

      def test_a(self):
        if self.current_device_id == 'slow':
          release_slow.wait(5)
          return
        start = time.monotonic()
        try:
          self.synchronized_step('meet', timeout=0.2)
        finally:
          test.record_call('waited', time.monotonic() - start)
          release_slow.set()

    results = self.run_test_class(
        MockTest,
        [
            {'group': 'g', 'id': 'fast1'},
            {'group': 'g', 'id': 'fast2'},
            {'group': 'g', 'id': 'slow'},
        ],
    )
    self.assertEqual(len(results.passed), 1)
    self.assertEqual(len(results.error), 2)
    for record in results.error:
      self.assertEqual(record.termination_signal_type, 'TestError')
      self.assertIn('"meet"', record.details)
    for _, waited in self.calls:
      self.assertLess(waited, 2)

  def test_synchronized_step_released_when_participant_fails(self):

    class MockTest(base_test.BaseTestClass):

      def test_a(self):
        if self.current_device_id == 1:
          raise Exception(MSG_EXPECTED_EXCEPTION)
        # Would wait forever if the failed participant was not accounted for.
        self.synchronized_step('never')

    results = self.run_test_class(
        MockTest, [{'group': 'g', 'id': 1}, {'group': 'g', 'id': 2}]
    )
    self.assertEqual(len(results.error), 2)
    failed_details, sync_details = sorted(
        (record.details for record in results.error),
        key=lambda details: details != MSG_EXPECTED_EXCEPTION,
    )
    self.assertEqual(failed_details, MSG_EXPECTED_EXCEPTION)
    self.assertIn('"never"', sync_details)

  def test_synchronized_step_released_when_participant_leaves_iteration(self):
    iterations = collections.Counter()
    lock = self.lock

    class MockTest(base_test.BaseTestClass):

      @base_test.repeat(count=2)
      def test_a(self):
        with lock:
          iterations[self.current_device_id] += 1
          iteration = iterations[self.current_device_id]
        if iteration == 2:
          self.synchronized_step('second_iteration', timeout=5)
        elif self.current_device_id == 1:
          # Participant 2 moves on to the next iteration without this step.
          self.synchronized_step('first_iteration')

    results = self.run_test_class(
        MockTest, [{'group': 'g', 'id': 1}, {'group': 'g', 'id': 2}]
    )
    self.assertEqual(len(results.error), 1, results.error)
    self.assertEqual(results.error[0].test_name, 'test_a_0')
    self.assertIn('"first_iteration"', results.error[0].details)
    self.assertCountEqual(
        [record.test_name for record in results.passed],
        ['test_a_0', 'test_a_1', 'test_a_1'],
    )

  def test_global_setup_failure(self):
    test = self

    class MockTest(base_test.BaseTestClass):

      def global_setup(self):
        raise Exception(MSG_EXPECTED_EXCEPTION)

      def group_setup(self, devices):
        test.record_call('group_setup')

      def test_a(self):
        test.record_call('test_a')

      def group_teardown(self, devices):
        test.record_call('group_teardown')

      def global_teardown(self):
        test.record_call('global_teardown')

      def teardown_class(self):
        test.record_call('teardown_class')

      def on_fail(self, record):
        test.record_call('on_fail', record.test_name)

    results = self.run_test_class(MockTest, [{'group': 'g'}])
    self.assertEqual(
        self.calls,
        [
            ('on_fail', 'global_setup'),
            ('global_teardown',),
            ('teardown_class',),
        ],
    )
    self.assertEqual(len(results.error), 1)
    self.assertEqual(results.error[0].test_name, 'global_setup')
    self.assertEqual(results.error[0].details, MSG_EXPECTED_EXCEPTION)
    self.assertEqual(results.executed, [])

  def test_group_setup_failure_skips_group(self):
    test = self

    class MockTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        test.record_call('group_setup', self.current_device_id)
        if self.current_device_id == 'fail':
          raise Exception(MSG_EXPECTED_EXCEPTION)
        if self.current_device_id == 'false':
          return False

      def test_a(self):
        test.record_call('test_a', self.current_device_id)

      def group_teardown(self, devices):
        test.record_call('group_teardown', self.current_device_id)

      def global_teardown(self):
        test.record_call('global_teardown')

    entries = [
        {'group': 'g1', 'id': 'fail'},
        {'group': 'g1', 'id': 'fail2'},
        {'group': 'g2', 'id': 'false'},
        {'group': 'g3', 'id': 'ok'},
    ]
    results = self.run_test_class(MockTest, entries)
    self.assertEqual(
        self.calls,
        [
            ('group_setup', 'fail'),
            ('group_teardown', 'fail'),
            ('group_setup', 'false'),
            ('group_teardown', 'false'),
            ('group_setup', 'ok'),
            ('test_a', 'ok'),
            ('group_teardown', 'ok'),
            ('global_teardown',),
        ],
    )
    self.assertEqual(
        [record.test_name for record in results.error],
        ['group_setup', 'group_setup'],
    )
    self.assertEqual(results.error[0].details, MSG_EXPECTED_EXCEPTION)
    self.assertIn('returned False', results.error[1].details)
    self.assertEqual(len(results.skipped), 3)
    self.assertEqual(len(results.passed), 1)

  def test_group_teardown_runs_after_failed_tests(self):
    test = self

    class MockTest(base_test.BaseTestClass):

      def test_a(self):
        asserts.fail('Test failure.')

      def test_b(self):
        raise Exception(MSG_EXPECTED_EXCEPTION)

      def group_teardown(self, devices):
        test.record_call('group_teardown', devices)

    for entries in (['a'], [{'group': 'g'}, {'group': 'g'}]):
      self.calls = []
      results = self.run_test_class(MockTest, entries)
      self.assertEqual(self.calls, [('group_teardown', entries)])
      self.assertEqual(len(results.failed), len(entries))
      self.assertEqual(len(results.error), len(entries))

  def test_group_teardown_failure_is_recorded(self):
    test = self

    class MockTest(base_test.BaseTestClass):

      def test_a(self):
        pass

      def group_teardown(self, devices):
        test.record_call('group_teardown', self.current_device_id)
        raise Exception(MSG_EXPECTED_EXCEPTION)

    results = self.run_test_class(
        MockTest, [{'group': 'g1', 'id': 1}, {'group': 'g2', 'id': 2}]
    )
    self.assertEqual(self.calls, [('group_teardown', 1), ('group_teardown', 2)])
    self.assertEqual(
        [record.test_name for record in results.error],
        ['group_teardown', 'group_teardown'],
    )
    self.assertEqual(len(results.passed), 2)

  def test_abort_class_in_explicit_group(self):
    test = self

    class MockTest(base_test.BaseTestClass):

      def test_a(self):
        if self.current_device_id == 1:
          asserts.abort_class(MSG_EXPECTED_EXCEPTION)

      def test_b(self):
        test.record_call('test_b')

      def group_teardown(self, devices):
        test.record_call('group_teardown')

      def global_teardown(self):
        test.record_call('global_teardown')

    results = self.run_test_class(
        MockTest,
        [{'group': 'g1', 'id': 1}, {'group': 'g1', 'id': 2}, {'group': 'g2'}],
    )
    self.assertEqual(self.calls, [('group_teardown',), ('global_teardown',)])
    self.assertEqual(len(results.failed), 1)
    self.assertEqual(len(results.passed), 1)
    self.assertEqual(
        [record.test_name for record in results.skipped], ['test_b']
    )


if __name__ == '__main__':
  unittest.main()
