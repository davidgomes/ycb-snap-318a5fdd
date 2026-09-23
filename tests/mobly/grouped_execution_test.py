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
    self.config.summary_writer = records.TestSummaryWriter(
        os.path.join(self.tmp_dir, 'summary.yaml')
    )
    self.config.log_path = self.tmp_dir
    self.config.controller_configs = {}

  def tearDown(self):
    shutil.rmtree(self.tmp_dir)

  def test_no_entries_runs_tests_once_and_globals_only(self):
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

      def test_one(self):
        calls.append('test')
        try:
          _ = self.current_device
        except AttributeError:
          calls.append('no-device')
        else:
          calls.append('device-present')

    bt = MockTest(self.config)
    bt.run()
    self.assertEqual(
        calls,
        ['global_setup', 'test', 'no-device', 'global_teardown'],
    )
    self.assertEqual(len(bt.results.passed), 1)
    self.assertEqual(bt.results.passed[0].test_name, 'test_one')

  def test_implicit_group_runs_each_test_once(self):
    self.config.controller_configs = {
        'AndroidDevice': [{'serial': 'a', 'id': 'phone'}, {'serial': 'b'}]
    }
    seen = {}

    class MockTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        seen['setup_devices'] = list(devices)
        seen['setup_current'] = self.current_device
        seen['setup_id'] = self.current_device_id
        self.synchronized_step('ignored')

      def test_one(self):
        seen['test_device'] = self.current_device
        seen['test_id'] = self.current_device_id
        self.synchronized_step('also-ignored', timeout=5)

      def group_teardown(self, devices):
        seen['teardown'] = list(devices)
        seen['teardown_current'] = self.current_device

    bt = MockTest(self.config)
    bt.run()
    self.assertEqual(len(bt.results.passed), 1)
    self.assertEqual(seen['setup_devices'], [{'serial': 'a', 'id': 'phone'}, {'serial': 'b'}])
    self.assertEqual(seen['setup_current'], {'serial': 'a', 'id': 'phone'})
    self.assertEqual(seen['setup_id'], 'phone')
    self.assertEqual(seen['test_device'], seen['setup_current'])
    self.assertEqual(seen['teardown'], seen['setup_devices'])

  def test_explicit_groups_run_participants_concurrently(self):
    self.config.controller_configs = {
        'AndroidDevice': [
            {'group': 'alpha', 'id': 'a1'},
            {'group': 'alpha', 'id': 'a2'},
            {'group': 'beta', 'id': 'b1'},
        ]
    }
    order = []
    lock = threading.Lock()
    release = threading.Event()

    class MockTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        with lock:
          order.append(('setup', self.current_device_id, [d['id'] for d in devices]))

      def test_one(self):
        self.synchronized_step('gate')
        with lock:
          order.append(('test', self.current_device_id))
        release.wait(2)

      def group_teardown(self, devices):
        with lock:
          order.append(('teardown', self.current_device_id))

    bt = MockTest(self.config)

    def _release_later():
      release.set()

    timer = threading.Timer(0.2, _release_later)
    timer.start()
    bt.run()
    timer.cancel()
    names = [record.test_name for record in bt.results.passed]
    self.assertEqual(names, ['test_one', 'test_one', 'test_one'])
    self.assertEqual(order[0], ('setup', 'a1', ['a1', 'a2']))
    self.assertCountEqual(order[1:3], [('test', 'a1'), ('test', 'a2')])
    self.assertEqual(order[3][0], 'teardown')
    self.assertEqual(order[4], ('setup', 'b1', ['b1']))
    self.assertEqual(order[5], ('test', 'b1'))
    self.assertEqual(order[6][0], 'teardown')

  def test_uses_registered_objects_when_paired(self):
    self.config.controller_configs = {
        mock_controller.MOBLY_CONTROLLER_CONFIG_NAME: [
            {'serial': 'magic1', 'group': 'g', 'id': 'one'},
            {'serial': 'magic2', 'group': 'g', 'id': 'two'},
        ]
    }
    seen = []

    class MockTest(base_test.BaseTestClass):

      def setup_class(self):
        self.devices = self.register_controller(mock_controller)

      def test_one(self):
        seen.append(self.current_device)

    bt = MockTest(self.config)
    bt.run()
    self.assertEqual(seen, list(bt.devices))
    self.assertEqual(len(bt.results.passed), 2)

  def test_group_setup_false_skips_tests_and_runs_teardown(self):
    self.config.controller_configs = {'AndroidDevice': [{'serial': 1}, {'serial': 2}]}
    calls = []

    class MockTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        calls.append('setup')
        return False

      def test_one(self):
        calls.append('test')

      def group_teardown(self, devices):
        calls.append('teardown')

    bt = MockTest(self.config)
    bt.run()
    self.assertEqual(calls, ['setup', 'teardown'])
    self.assertEqual(bt.results.executed, [])

  def test_group_setup_error_skips_that_group_only(self):
    self.config.controller_configs = {
        'AndroidDevice': [
            {'group': 'bad', 'id': 'b'},
            {'group': 'good', 'id': 'g'},
        ]
    }
    calls = []

    class MockTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        if devices[0]['id'] == 'b':
          raise RuntimeError('boom')
        calls.append('setup-good')

      def test_one(self):
        calls.append(self.current_device_id)

      def group_teardown(self, devices):
        calls.append('teardown-%s' % devices[0]['id'])

    bt = MockTest(self.config)
    bt.run()
    self.assertEqual(calls, ['teardown-b', 'setup-good', 'g', 'teardown-g'])
    self.assertEqual(bt.results.error[0].test_name, 'group_setup')
    self.assertEqual([r.test_name for r in bt.results.passed], ['test_one'])

  def test_global_setup_error_skips_tests_and_runs_global_teardown(self):
    calls = []

    class MockTest(base_test.BaseTestClass):

      def global_setup(self):
        raise RuntimeError('nope')

      def test_one(self):
        calls.append('test')

      def global_teardown(self):
        calls.append('global_teardown')

    bt = MockTest(self.config)
    bt.run()
    self.assertEqual(calls, ['global_teardown'])
    self.assertEqual(bt.results.error[0].test_name, 'global_setup')
    self.assertEqual(len(bt.results.skipped), 1)
    self.assertEqual(bt.results.executed, [])

  def test_sync_outside_test_raises(self):
    calls = []

    class MockTest(base_test.BaseTestClass):

      def setup_class(self):
        try:
          self.synchronized_step('x')
        except signals.TestError as err:
          calls.append(err.details)
        else:
          calls.append('no-error')

      def test_one(self):
        pass

    bt = MockTest(self.config)
    bt.run()
    self.assertTrue(bt.results.is_all_pass)
    self.assertIn('synchronized_step', calls[0])

  def test_sync_timeout_validation_and_timeout(self):
    self.config.controller_configs = {
        'AndroidDevice': [
            {'group': 'g', 'id': 'a'},
            {'group': 'g', 'id': 'b'},
        ]
    }

    errors = []

    class MockTest(base_test.BaseTestClass):

      def test_one(self):
        try:
          self.synchronized_step('neg', timeout=-1)
        except ValueError:
          errors.append('value')
        else:
          errors.append('no-value')
        try:
          self.synchronized_step('zero', timeout=0)
        except signals.TestError as err:
          errors.append(err.details)
        if self.current_device_id == 'a':
          try:
            self.synchronized_step('slow', timeout=0.2)
          except signals.TestError as err:
            errors.append(err.details)
            raise
          else:
            errors.append('no-timeout')

    bt = MockTest(self.config)
    bt.run()
    self.assertEqual(errors.count('value'), 2)
    self.assertTrue(any('zero' in item for item in errors))
    self.assertTrue(any('slow' in item for item in errors))
    self.assertEqual(len(bt.results.error), 1)
    self.assertEqual(len(bt.results.passed), 1)

  def test_expects_attributed_per_participant(self):
    self.config.controller_configs = {
        'AndroidDevice': [
            {'group': 'g', 'id': 'a'},
            {'group': 'g', 'id': 'b'},
        ]
    }

    class MockTest(base_test.BaseTestClass):

      def test_one(self):
        if self.current_device_id == 'a':
          expects.expect_true(False, 'only-a')
        self.synchronized_step('done', timeout=2)

    bt = MockTest(self.config)
    bt.run()
    failed = [r for r in bt.results.failed]
    passed = [r for r in bt.results.passed]
    self.assertEqual(len(failed), 1)
    self.assertEqual(len(passed), 1)
    self.assertIn('only-a', failed[0].details)
    self.assertEqual(failed[0].test_name, 'test_one')

  def test_synchronized_context_syncs_on_entry_only(self):
    self.config.controller_configs = {
        'AndroidDevice': [
            {'group': 'g', 'id': 'a'},
            {'group': 'g', 'id': 'b'},
        ]
    }
    entered = []

    class MockTest(base_test.BaseTestClass):

      def test_one(self):
        with self.synchronized_context('enter', timeout=2):
          entered.append(self.current_device_id)
          if self.current_device_id == 'a':
            return

    bt = MockTest(self.config)
    bt.run()
    self.assertCountEqual(entered, ['a', 'b'])
    self.assertEqual(len(bt.results.passed), 2)

  def test_group_teardown_runs_when_test_fails(self):
    self.config.controller_configs = {'AndroidDevice': [{'id': 'a'}]}
    calls = []

    class MockTest(base_test.BaseTestClass):

      def test_one(self):
        calls.append('test')
        raise AssertionError('fail')

      def group_teardown(self, devices):
        calls.append('teardown')

    bt = MockTest(self.config)
    bt.run()
    self.assertEqual(calls, ['test', 'teardown'])
    self.assertEqual(len(bt.results.failed), 1)


if __name__ == '__main__':
  unittest.main()
