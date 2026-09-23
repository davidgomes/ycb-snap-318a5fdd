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


class GroupedExecutionTest(unittest.TestCase):

  def setUp(self):
    self.tmp_dir = tempfile.mkdtemp()
    self.config = config_parser.TestRunConfig()
    self.summary_file = os.path.join(self.tmp_dir, 'summary.yaml')
    self.config.summary_writer = records.TestSummaryWriter(self.summary_file)
    self.config.controller_configs = {}
    self.config.log_path = self.tmp_dir

  def tearDown(self):
    shutil.rmtree(self.tmp_dir)

  def test_no_entries_runs_tests_once_and_global_hooks(self):
    calls = []

    class MyTest(base_test.BaseTestClass):

      def global_setup(self):
        calls.append('global_setup')

      def global_teardown(self):
        calls.append('global_teardown')

      def group_setup(self, devices):
        calls.append('group_setup')

      def group_teardown(self, devices):
        calls.append('group_teardown')

      def test_once(self):
        calls.append('test')
        self.synchronized_step('ignored')
        with self.synchronized_context('ignored-ctx'):
          pass
        try:
          _ = self.current_device
          calls.append('device-present')
        except AttributeError:
          calls.append('device-absent')
        try:
          _ = self.current_device_id
          calls.append('id-present')
        except AttributeError:
          calls.append('id-absent')

    test = MyTest(self.config)
    test.run()
    self.assertEqual(
        calls,
        [
            'global_setup',
            'test',
            'device-absent',
            'id-absent',
            'global_teardown',
        ],
    )
    self.assertEqual([r.test_name for r in test.results.passed], ['test_once'])

  def test_device_context_and_sync_rejected_outside_allowed_phases(self):
    test = base_test.BaseTestClass(self.config)
    with self.assertRaises(AttributeError):
      _ = test.current_device
    with self.assertRaises(AttributeError):
      _ = test.current_device_id
    with self.assertRaises(signals.TestError) as ctx:
      test.synchronized_step('outside')
    self.assertIn('synchronized_step', ctx.exception.details)
    with self.assertRaises(signals.TestError) as ctx:
      with test.synchronized_context('outside'):
        pass
    self.assertIn('synchronized_step', ctx.exception.details)
    with self.assertRaises(ValueError):
      test.synchronized_step('outside', timeout=-1)

  def test_sync_rejected_in_setup_test(self):

    class MyTest(base_test.BaseTestClass):

      def setup_test(self):
        self.synchronized_step('not-here')

      def test_body(self):
        pass

    test = MyTest(self.config)
    test.run()
    self.assertEqual(len(test.results.error), 1)
    self.assertIn('synchronized_step', test.results.error[0].details)
    self.assertEqual(test.results.passed, [])

  def test_implicit_group_runs_each_test_once(self):
    seen = {}

    class MyTest(base_test.BaseTestClass):

      def setup_class(self):
        self.registered = self.register_controller(mock_controller)

      def group_setup(self, devices):
        seen['setup'] = list(devices)
        seen['setup_device'] = self.current_device
        seen['setup_id'] = self.current_device_id
        started = time.monotonic()
        self.synchronized_step('setup-sync', timeout=2)
        seen['setup_sync_s'] = time.monotonic() - started

      def group_teardown(self, devices):
        seen['teardown'] = list(devices)
        seen['teardown_id'] = self.current_device_id

      def test_once(self):
        seen['test_device'] = self.current_device
        seen['test_id'] = self.current_device_id
        self.synchronized_step('test-sync')

    self.config.controller_configs[
        mock_controller.MOBLY_CONTROLLER_CONFIG_NAME
    ] = [
        {'serial': '1', 'id': 'phone-a'},
        {'serial': '2', 'id': 'phone-b'},
    ]
    test = MyTest(self.config)
    test.run()
    self.assertEqual(seen['setup'], test.registered)
    self.assertEqual(seen['teardown'], test.registered)
    self.assertIs(seen['setup_device'], test.registered[0])
    self.assertEqual(seen['setup_id'], 'phone-a')
    self.assertIs(seen['test_device'], test.registered[0])
    self.assertEqual(seen['test_id'], 'phone-a')
    self.assertEqual(seen['teardown_id'], 'phone-a')
    self.assertLess(seen['setup_sync_s'], 0.5)
    self.assertEqual(len(test.results.passed), 1)
    self.assertEqual(test.results.passed[0].test_name, 'test_once')

  def test_explicit_groups_run_tests_per_participant(self):
    calls = []
    lock = threading.Lock()
    state = {'in_test': 0, 'max_in_test': 0}

    class MyTest(base_test.BaseTestClass):

      def setup_class(self):
        self.registered = self.register_controller(mock_controller)

      def group_setup(self, devices):
        calls.append(('setup', self.current_device_id, devices))

      def group_teardown(self, devices):
        calls.append(('teardown', self.current_device_id, len(devices)))

      def test_parallel(self):
        with lock:
          state['in_test'] += 1
          state['max_in_test'] = max(state['max_in_test'], state['in_test'])
        self.synchronized_step('ready', timeout=2)
        if self.current_device_id == 'a':
          time.sleep(0.3)
        started = time.monotonic()
        self.synchronized_step('ready', timeout=2)
        elapsed = time.monotonic() - started
        with lock:
          calls.append(('test', self.current_device_id, elapsed))
          state['in_test'] -= 1
        if self.current_device_id == 'a':
          expects.expect_true(False, 'fail-a')
        elif self.current_device_id == 'b':
          expects.expect_true(False, 'fail-b')

    self.config.controller_configs[
        mock_controller.MOBLY_CONTROLLER_CONFIG_NAME
    ] = [
        {'serial': '1', 'id': 'a', 'group': 'g1'},
        {'serial': '2', 'id': 'b', 'group': 'g1'},
        {'serial': '3', 'id': 'c', 'group': 'g2'},
    ]
    test = MyTest(self.config)
    test.run()
    self.assertEqual(state['max_in_test'], 2)
    setup_ids = [item[1] for item in calls if item[0] == 'setup']
    self.assertEqual(setup_ids, ['a', 'c'])
    self.assertIs(calls[0][2][0], test.registered[0])
    self.assertIs(calls[0][2][1], test.registered[1])
    teardown_ids = [item[1] for item in calls if item[0] == 'teardown']
    self.assertEqual(teardown_ids, ['a', 'c'])
    by_id = {
        item[1]: item[2] for item in calls if item[0] == 'test'
    }
    self.assertLess(by_id['a'], 0.2)
    self.assertGreaterEqual(by_id['b'], 0.2)
    self.assertEqual(by_id['c'] < 0.2, True)
    names = [record.test_name for record in test.results.executed]
    self.assertEqual(names, ['test_parallel', 'test_parallel', 'test_parallel'])
    self.assertNotIn('[', ''.join(names))
    failed = {self._record_text(record) for record in test.results.failed}
    self.assertTrue(any('fail-a' in text for text in failed))
    self.assertTrue(any('fail-b' in text for text in failed))
    a_text = [
        self._record_text(record)
        for record in test.results.failed
        if 'fail-a' in self._record_text(record)
    ]
    b_text = [
        self._record_text(record)
        for record in test.results.failed
        if 'fail-b' in self._record_text(record)
    ]
    self.assertEqual(len(a_text), 1)
    self.assertEqual(len(b_text), 1)
    self.assertNotIn('fail-b', a_text[0])
    self.assertNotIn('fail-a', b_text[0])

  def test_timeout_releases_waiters(self):

    class MyTest(base_test.BaseTestClass):

      def test_sync(self):
        if self.current_device_id == 'b':
          time.sleep(0.6)
        self.synchronized_step('gate', timeout=0.2)

    self.config.controller_configs['Device'] = [
        {'id': 'a', 'group': 'g'},
        {'id': 'b', 'group': 'g'},
    ]
    test = MyTest(self.config)
    test.run()
    self.assertEqual(len(test.results.error), 2)
    for record in test.results.error:
      self.assertEqual(record.test_name, 'test_sync')
      self.assertIn('gate', record.details)

  def test_timeout_validation_inside_test(self):

    class MyTest(base_test.BaseTestClass):

      def test_sync(self):
        try:
          self.synchronized_step('gate', timeout=-0.1)
        except ValueError:
          pass
        else:
          raise AssertionError('negative timeout should raise ValueError')
        try:
          self.synchronized_step('gate', timeout=0)
        except signals.TestError as err:
          if 'gate' not in err.details:
            raise AssertionError(err.details) from err
        else:
          raise AssertionError('zero timeout should raise TestError')

    self.config.controller_configs['Device'] = [
        {'id': 'a', 'group': 'g'},
    ]
    test = MyTest(self.config)
    test.run()
    self.assertEqual(len(test.results.passed), 1)

  def test_synchronized_context_syncs_on_entry_only(self):
    releases = []

    class MyTest(base_test.BaseTestClass):

      def test_sync(self):
        with self.synchronized_context('enter', timeout=2):
          if self.current_device_id == 'a':
            time.sleep(0.3)
        releases.append((self.current_device_id, time.monotonic()))

    self.config.controller_configs['Device'] = [
        {'id': 'a', 'group': 'g'},
        {'id': 'b', 'group': 'g'},
    ]
    test = MyTest(self.config)
    test.run()
    by_id = dict(releases)
    self.assertGreaterEqual(by_id['a'] - by_id['b'], 0.2)
    self.assertEqual(len(test.results.passed), 2)

  def test_global_setup_error_skips_tests_and_runs_global_teardown(self):
    calls = []

    class MyTest(base_test.BaseTestClass):

      def global_setup(self):
        calls.append('global_setup')
        raise RuntimeError('boom')

      def setup_class(self):
        calls.append('setup_class')

      def test_skipped(self):
        calls.append('test')

      def global_teardown(self):
        calls.append('global_teardown')

    test = MyTest(self.config)
    test.run()
    self.assertNotIn('test', calls)
    self.assertNotIn('setup_class', calls)
    self.assertIn('global_teardown', calls)
    self.assertEqual(test.results.error[0].test_name, 'global_setup')
    self.assertIn('boom', test.results.error[0].details)

  def test_group_setup_false_and_error_skip_only_that_group(self):
    calls = []

    class MyTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        calls.append(('setup', self.current_device_id))
        if self.current_device_id == 'bad':
          raise RuntimeError('setup failed')
        if self.current_device_id == 'skip':
          return False

      def group_teardown(self, devices):
        calls.append(('teardown', self.current_device_id))

      def test_work(self):
        calls.append(('test', self.current_device_id))
        raise RuntimeError('test failed')

    self.config.controller_configs['Device'] = [
        {'id': 'bad', 'group': 'bad'},
        {'id': 'skip', 'group': 'skip'},
        {'id': 'ok', 'group': 'ok'},
    ]
    test = MyTest(self.config)
    test.run()
    self.assertEqual(
        calls,
        [
            ('setup', 'bad'),
            ('teardown', 'bad'),
            ('setup', 'skip'),
            ('teardown', 'skip'),
            ('setup', 'ok'),
            ('test', 'ok'),
            ('teardown', 'ok'),
        ],
    )
    self.assertTrue(
        any(record.test_name == 'group_setup' for record in test.results.error)
    )
    executed_names = [record.test_name for record in test.results.executed]
    self.assertEqual(executed_names, ['test_work'])

  def test_unpaired_entries_use_raw_configs(self):
    seen = {'currents': []}

    class MyTest(base_test.BaseTestClass):

      def setup_class(self):
        self.register_controller(mock_controller)

      def group_setup(self, devices):
        seen['devices'] = list(devices)

      def test_raw(self):
        seen['currents'].append((self.current_device_id, self.current_device))

    raw_entries = [
        {'serial': '1', 'id': 'a', 'group': 'g'},
        {'id': 'b', 'group': 'g'},
    ]
    self.config.controller_configs[
        mock_controller.MOBLY_CONTROLLER_CONFIG_NAME
    ] = [raw_entries[0]]
    self.config.controller_configs['Other'] = [raw_entries[1]]
    test = MyTest(self.config)
    test.run()
    self.assertEqual(seen['devices'], raw_entries)
    self.assertEqual(
        sorted(seen['currents'], key=lambda item: item[0]),
        [('a', raw_entries[0]), ('b', raw_entries[1])],
    )

  def test_non_dict_entries_default_to_implicit_group(self):
    seen = {}

    class MyTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        seen['devices'] = list(devices)
        seen['id'] = self.current_device_id

      def test_raw(self):
        seen['test_count'] = seen.get('test_count', 0) + 1
        seen['current'] = self.current_device

    self.config.controller_configs['Device'] = ['left', 'right']
    test = MyTest(self.config)
    test.run()
    self.assertEqual(seen['devices'], ['left', 'right'])
    self.assertIsNone(seen['id'])
    self.assertEqual(seen['current'], 'left')
    self.assertEqual(seen['test_count'], 1)

  def _record_text(self, record):
    parts = [record.details or '']
    for error in record.extra_errors.values():
      parts.append(error.details or '')
    return '\n'.join(parts)
