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


def _config(controller_configs, log_path):
  config = config_parser.TestRunConfig()
  config.controller_configs = controller_configs
  config.log_path = log_path
  config.summary_writer = records.TestSummaryWriter(
      os.path.join(log_path, 'summary.yaml')
  )
  config.user_params = {}
  return config


class GroupedExecutionTest(unittest.TestCase):

  def setUp(self):
    self.tmp_dir = tempfile.mkdtemp()

  def tearDown(self):
    shutil.rmtree(self.tmp_dir)

  def test_no_entries_runs_tests_once_and_global_hooks(self):
    calls = []

    class MockBaseTest(base_test.BaseTestClass):

      def global_setup(self):
        calls.append('global_setup')
        try:
          _ = self.current_device
          calls.append('device-visible')
        except (AttributeError, RuntimeError):
          calls.append('device-hidden')

      def global_teardown(self):
        calls.append('global_teardown')

      def group_setup(self, devices):
        calls.append('group_setup')

      def group_teardown(self, devices):
        calls.append('group_teardown')

      def setup_class(self):
        calls.append('setup_class')

      def teardown_class(self):
        calls.append('teardown_class')

      def test_one(self):
        calls.append('test_one')
        for _name in ('current_device', 'current_device_id'):
          try:
            getattr(self, _name)
            calls.append('device-visible')
          except (AttributeError, RuntimeError):
            calls.append('device-hidden')
        # No participants to wait for.
        self.synchronized_step('unused', timeout=0.01)

      def test_two(self):
        calls.append('test_two')

    bt = MockBaseTest(_config({}, self.tmp_dir))
    bt.run()
    self.assertEqual(
        calls,
        [
            'setup_class',
            'global_setup',
            'device-hidden',
            'test_one',
            'device-hidden',
            'device-hidden',
            'test_two',
            'global_teardown',
            'teardown_class',
        ],
    )
    self.assertEqual(
        [record.test_name for record in bt.results.passed],
        ['test_one', 'test_two'],
    )

  def test_current_device_outside_run_raises(self):
    bt = base_test.BaseTestClass(_config({}, self.tmp_dir))
    with self.assertRaises(AttributeError):
      _ = bt.current_device
    with self.assertRaises(AttributeError):
      _ = bt.current_device_id
    with self.assertRaises(signals.TestError) as ctx:
      bt.synchronized_step('gate')
    self.assertIn('synchronized_step', ctx.exception.details)
    with self.assertRaises(signals.TestError) as ctx:
      with bt.synchronized_context('gate'):
        pass
    self.assertIn('synchronized_step', ctx.exception.details)

  def test_sync_rejected_in_global_and_class_hooks(self):
    seen = {}

    class MockBaseTest(base_test.BaseTestClass):

      def setup_class(self):
        try:
          self.synchronized_step('nope')
        except signals.TestError as exc:
          seen['setup'] = exc.details
        else:
          seen['setup'] = ''

      def global_setup(self):
        try:
          with self.synchronized_context('nope'):
            pass
        except signals.TestError as exc:
          seen['global'] = exc.details
        else:
          seen['global'] = ''

      def test_one(self):
        seen['test'] = True

    bt = MockBaseTest(_config({}, self.tmp_dir))
    bt.run()
    self.assertIn('synchronized_step', seen['setup'])
    self.assertIn('synchronized_step', seen['global'])
    self.assertTrue(seen['test'])

  def test_global_setup_error_skips_tests_and_runs_global_teardown(self):
    calls = []

    class MockBaseTest(base_test.BaseTestClass):

      def global_setup(self):
        calls.append('global_setup')
        raise RuntimeError('boom')

      def global_teardown(self):
        calls.append('global_teardown')

      def test_one(self):
        calls.append('test_one')

      def teardown_class(self):
        calls.append('teardown_class')

    bt = MockBaseTest(_config({}, self.tmp_dir))
    bt.run()
    self.assertEqual(calls, ['global_setup', 'global_teardown', 'teardown_class'])
    self.assertTrue(any(record.test_name == 'global_setup' for record in bt.results.error))
    self.assertEqual(bt.results.error[0].details, 'boom')
    self.assertEqual(len(bt.results.skipped), 1)
    self.assertEqual(len(bt.results.executed), 0)

  def test_implicit_group_runs_each_test_once(self):
    calls = []

    class MockBaseTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        calls.append(
            (
                'setup',
                list(devices),
                self.current_device,
                self.current_device_id,
            )
        )
        self.synchronized_step('during-setup', timeout=0.05)

      def group_teardown(self, devices):
        calls.append(('teardown', self.current_device, list(devices)))

      def test_one(self):
        calls.append(('test', self.current_device, self.current_device_id))
        self.synchronized_step('during-test', timeout=0.05)

    entries = [{'serial': 'a', 'id': None}, 'raw-entry']
    bt = MockBaseTest(
        _config({'MagicDevice': entries}, self.tmp_dir)
    )
    bt.run()
    self.assertEqual(calls[0][0], 'setup')
    self.assertEqual(calls[0][1], entries)
    self.assertEqual(calls[0][2], entries[0])
    self.assertIsNone(calls[0][3])
    self.assertEqual(calls[1], ('test', entries[0], None))
    self.assertEqual(calls[2][0], 'teardown')
    self.assertEqual(calls[2][1], entries[0])
    self.assertEqual(len(bt.results.passed), 1)
    self.assertEqual(bt.results.passed[0].test_name, 'test_one')

  def test_registered_objects_are_used_when_paired(self):
    seen = {}

    class MockBaseTest(base_test.BaseTestClass):

      def setup_class(self):
        self.devices = self.register_controller(mock_controller)

      def group_setup(self, devices):
        seen['devices'] = list(devices)
        seen['setup_id'] = self.current_device_id

      def test_one(self):
        seen['current'] = self.current_device
        seen['current_id'] = self.current_device_id

    configs = [
        {'serial': '1', 'id': 'phone-a', 'magic': 'a'},
        {'serial': '2', 'id': 'phone-b', 'magic': 'b'},
    ]
    bt = MockBaseTest(_config({'MagicDevice': configs}, self.tmp_dir))
    bt.run()
    self.assertEqual(len(seen['devices']), 2)
    self.assertIsInstance(seen['devices'][0], mock_controller.MagicDevice)
    self.assertIs(seen['current'], seen['devices'][0])
    self.assertEqual(seen['current_id'], 'phone-a')
    self.assertEqual(seen['setup_id'], 'phone-a')

  def test_explicit_groups_run_participants_concurrently(self):
    order = []
    lock = threading.Lock()

    class MockBaseTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        with lock:
          order.append(('setup', self.current_device['id'], [d['id'] for d in devices]))

      def group_teardown(self, devices):
        with lock:
          order.append(('teardown', [d['id'] for d in devices]))

      def test_sync(self):
        with lock:
          order.append(('start', self.current_device_id))
        if self.current_device_id == 'a1':
          time.sleep(0.2)
        self.synchronized_step('gate', timeout=2)
        with lock:
          order.append(('end', self.current_device_id))

    entries = [
        {'id': 'b1', 'group': 'beta'},
        {'id': 'a1', 'group': 'alpha'},
        {'id': 'a2', 'group': 'alpha'},
        {'group': 'beta', 'id': 'b2'},
    ]
    bt = MockBaseTest(_config({'MagicDevice': entries}, self.tmp_dir))
    bt.run()
    names = [record.test_name for record in bt.results.passed]
    self.assertEqual(names, ['test_sync', 'test_sync', 'test_sync', 'test_sync'])
    self.assertTrue(all('[' not in name for name in names))
    # alpha is seen first after beta? b1 is first entry, so beta then alpha.
    self.assertEqual(order[0][0], 'setup')
    self.assertEqual(order[0][1], 'b1')
    self.assertEqual(order[0][2], ['b1', 'b2'])
    beta_events = [item for item in order if item[0] in ('start', 'end') and item[1].startswith('b')]
    # The slow path is alpha; beta has no sleep. Both starts happen before ends
    # because of the barrier. b1 and b2 do not sleep, but they still sync.
    beta_starts = [item for item in beta_events if item[0] == 'start']
    beta_ends = [item for item in beta_events if item[0] == 'end']
    self.assertEqual(len(beta_starts), 2)
    self.assertEqual(len(beta_ends), 2)
    first_end = order.index(beta_ends[0])
    last_start = max(order.index(item) for item in beta_starts)
    self.assertLess(last_start, first_end)
    alpha_setup = next(item for item in order if item[0] == 'setup' and item[1] == 'a1')
    self.assertEqual(alpha_setup[2], ['a1', 'a2'])
    # Teardown of beta happens before alpha setup.
    beta_teardown_at = order.index(('teardown', ['b1', 'b2']))
    alpha_setup_at = order.index(alpha_setup)
    self.assertLess(beta_teardown_at, alpha_setup_at)

  def test_explicit_expectation_failures_stay_on_their_participant(self):
    class MockBaseTest(base_test.BaseTestClass):

      def test_expect(self):
        expects.expect_true(
            False,
            'bad-%s' % self.current_device_id,
            extras=self.current_device_id,
        )

    entries = [
        {'id': 'left', 'group': 'g'},
        {'id': 'right', 'group': 'g'},
    ]
    bt = MockBaseTest(_config({'MagicDevice': entries}, self.tmp_dir))
    bt.run()
    self.assertEqual(len(bt.results.failed), 2)
    details = sorted(record.details for record in bt.results.failed)
    self.assertEqual(details, ['bad-left', 'bad-right'])
    extras = sorted(record.extras for record in bt.results.failed)
    self.assertEqual(extras, ['left', 'right'])
    for record in bt.results.failed:
      self.assertEqual(record.test_name, 'test_expect')
      self.assertNotIn('[', record.test_name)

  def test_group_setup_false_skips_group_and_continues(self):
    calls = []

    class MockBaseTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        calls.append(('setup', devices[0]['group']))
        if devices[0]['group'] == 'bad':
          return False

      def group_teardown(self, devices):
        calls.append(('teardown', devices[0]['group']))

      def test_one(self):
        calls.append(('test', self.current_device_id))

    entries = [
        {'id': 'bad-1', 'group': 'bad'},
        {'id': 'ok-1', 'group': 'ok'},
    ]
    bt = MockBaseTest(_config({'MagicDevice': entries}, self.tmp_dir))
    bt.run()
    self.assertEqual(
        calls,
        [
            ('setup', 'bad'),
            ('teardown', 'bad'),
            ('setup', 'ok'),
            ('test', 'ok-1'),
            ('teardown', 'ok'),
        ],
    )
    self.assertEqual(len(bt.results.passed), 1)

  def test_group_setup_error_skips_group_and_continues(self):
    calls = []

    class MockBaseTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        calls.append(('setup', devices[0]['id']))
        if devices[0]['id'] == 'bad':
          raise RuntimeError('group failed')

      def group_teardown(self, devices):
        calls.append(('teardown', devices[0]['id']))

      def test_one(self):
        calls.append(('test', self.current_device_id))

    entries = [
        {'id': 'bad', 'group': 'bad'},
        {'id': 'ok', 'group': 'ok'},
    ]
    bt = MockBaseTest(_config({'MagicDevice': entries}, self.tmp_dir))
    bt.run()
    self.assertIn(('teardown', 'bad'), calls)
    self.assertIn(('test', 'ok'), calls)
    self.assertNotIn(('test', 'bad'), calls)
    self.assertTrue(any(record.test_name == 'group_setup' for record in bt.results.error))

  def test_group_teardown_runs_when_test_fails(self):
    calls = []

    class MockBaseTest(base_test.BaseTestClass):

      def group_teardown(self, devices):
        calls.append('teardown')

      def test_one(self):
        calls.append('test')
        raise AssertionError('nope')

    bt = MockBaseTest(_config({'MagicDevice': [{'id': 'only'}]}, self.tmp_dir))
    bt.run()
    self.assertEqual(calls, ['test', 'teardown'])
    self.assertEqual(len(bt.results.failed), 1)

  def test_synchronized_step_reuses_a_fresh_barrier(self):
    order = []
    lock = threading.Lock()

    class MockBaseTest(base_test.BaseTestClass):

      def test_sync(self):
        self.synchronized_step('gate', timeout=2)
        if self.current_device_id == 'a':
          time.sleep(0.2)
        with lock:
          order.append(('mid', self.current_device_id))
        self.synchronized_step('gate', timeout=2)
        with lock:
          order.append(('done', self.current_device_id))

    entries = [
        {'id': 'a', 'group': 'g'},
        {'id': 'b', 'group': 'g'},
    ]
    bt = MockBaseTest(_config({'MagicDevice': entries}, self.tmp_dir))
    bt.run()
    self.assertEqual(len(bt.results.passed), 2)
    mids = [item for item in order if item[0] == 'mid']
    dones = [item for item in order if item[0] == 'done']
    self.assertLess(max(order.index(item) for item in mids), min(order.index(item) for item in dones))

  def test_synchronized_context_syncs_on_entry_only(self):
    done = []
    lock = threading.Lock()

    class MockBaseTest(base_test.BaseTestClass):

      def test_sync(self):
        with self.synchronized_context('gate', timeout=2):
          if self.current_device_id == 'a':
            time.sleep(0.3)
          else:
            time.sleep(0.05)
        with lock:
          done.append((self.current_device_id, time.monotonic()))

    entries = [
        {'id': 'a', 'group': 'g'},
        {'id': 'b', 'group': 'g'},
    ]
    bt = MockBaseTest(_config({'MagicDevice': entries}, self.tmp_dir))
    bt.run()
    self.assertEqual(len(bt.results.passed), 2)
    times = {device_id: stamp for device_id, stamp in done}
    self.assertGreater(times['a'] - times['b'], 0.15)

  def test_sync_timeout_releases_peer_and_names_the_step(self):
    class MockBaseTest(base_test.BaseTestClass):

      def test_sync(self):
        if self.current_device_id == 'slow':
          time.sleep(0.4)
        self.synchronized_step('gate', timeout=0.1)

    entries = [
        {'id': 'slow', 'group': 'g'},
        {'id': 'fast', 'group': 'g'},
    ]
    bt = MockBaseTest(_config({'MagicDevice': entries}, self.tmp_dir))
    started = time.monotonic()
    bt.run()
    elapsed = time.monotonic() - started
    self.assertLess(elapsed, 2.5)
    self.assertEqual(len(bt.results.error), 2)
    for record in bt.results.error:
      self.assertIn('gate', record.details)
      self.assertEqual(record.test_name, 'test_sync')

  def test_timeout_zero_and_negative(self):
    class MockBaseTest(base_test.BaseTestClass):

      def test_zero(self):
        self.synchronized_step('gate', timeout=0)

      def test_negative(self):
        self.synchronized_step('gate', timeout=-1)

    entries = [
        {'id': 'a', 'group': 'g'},
        {'id': 'b', 'group': 'g'},
    ]
    bt = MockBaseTest(_config({'MagicDevice': entries}, self.tmp_dir))
    started = time.monotonic()
    bt.run()
    self.assertLess(time.monotonic() - started, 2.5)
    zero_records = [record for record in bt.results.error if record.test_name == 'test_zero']
    negative_records = [
        record for record in bt.results.error if record.test_name == 'test_negative'
    ]
    self.assertEqual(len(zero_records), 2)
    self.assertTrue(all('gate' in record.details for record in zero_records))
    self.assertEqual(len(negative_records), 2)
    self.assertTrue(
        all(record.termination_signal_type == 'ValueError' for record in negative_records)
    )

  def test_non_dict_entry_joins_default_group_in_explicit_mode(self):
    seen = []

    class MockBaseTest(base_test.BaseTestClass):

      def group_setup(self, devices):
        seen.append(('setup', self.current_device_id, devices))

      def test_one(self):
        seen.append(('test', self.current_device_id, self.current_device))

    entries = ['plain', {'id': 'named', 'group': 'other'}]
    bt = MockBaseTest(_config({'MagicDevice': entries}, self.tmp_dir))
    bt.run()
    setups = [item for item in seen if item[0] == 'setup']
    self.assertEqual(setups[0][1], None)
    self.assertEqual(setups[0][2], ['plain'])
    self.assertEqual(setups[1][1], 'named')
    tests = [item for item in seen if item[0] == 'test']
    self.assertEqual(tests[0], ('test', None, 'plain'))
    self.assertEqual(tests[1][1], 'named')

  def test_unpaired_registered_objects_fall_back_to_raw_entries(self):
    seen = {}

    class MockBaseTest(base_test.BaseTestClass):

      def setup_class(self):
        # Only the first controller is registered, so counts cannot pair.
        self.register_controller(mock_controller)

      def group_setup(self, devices):
        seen['devices'] = list(devices)

    configs = {
        'MagicDevice': [{'serial': '1', 'id': 'a', 'group': 'g'}],
        'OtherDevice': [{'id': 'b', 'group': 'g'}],
    }
    bt = MockBaseTest(_config(configs, self.tmp_dir))
    bt.run()
    self.assertEqual(seen['devices'][0]['id'], 'a')
    self.assertNotIsInstance(seen['devices'][0], mock_controller.MagicDevice)

  def test_participant_records_have_distinct_signatures(self):
    class MockBaseTest(base_test.BaseTestClass):

      def test_one(self):
        pass

    entries = [
        {'id': 'a', 'group': 'g'},
        {'id': 'b', 'group': 'g'},
    ]
    bt = MockBaseTest(_config({'MagicDevice': entries}, self.tmp_dir))
    bt.run()
    signatures = [record.signature for record in bt.results.passed]
    self.assertEqual(len(signatures), 2)
    self.assertEqual(len(set(signatures)), 2)
    for signature in signatures:
      self.assertTrue(signature.startswith('test_one-'))

  def test_sync_can_be_reused_after_a_caught_timeout(self):
    met = []
    lock = threading.Lock()

    class MockBaseTest(base_test.BaseTestClass):

      def test_sync(self):
        try:
          if self.current_device_id == 'slow':
            time.sleep(0.3)
          self.synchronized_step('gate', timeout=0.05)
        except signals.TestError as exc:
          if 'gate' not in exc.details:
            raise
        self.synchronized_step('next', timeout=2)
        with lock:
          met.append(self.current_device_id)

    entries = [
        {'id': 'slow', 'group': 'g'},
        {'id': 'fast', 'group': 'g'},
    ]
    bt = MockBaseTest(_config({'MagicDevice': entries}, self.tmp_dir))
    started = time.monotonic()
    bt.run()
    self.assertLess(time.monotonic() - started, 2.5)
    self.assertEqual(sorted(met), ['fast', 'slow'])
    self.assertEqual(len(bt.results.passed), 2)

  def test_peer_exception_releases_the_waiting_participant(self):
    class MockBaseTest(base_test.BaseTestClass):

      def test_sync(self):
        if self.current_device_id == 'bad':
          raise RuntimeError('boom-bad')
        self.synchronized_step('gate', timeout=2)

    entries = [
        {'id': 'bad', 'group': 'g'},
        {'id': 'good', 'group': 'g'},
    ]
    bt = MockBaseTest(_config({'MagicDevice': entries}, self.tmp_dir))
    started = time.monotonic()
    bt.run()
    self.assertLess(time.monotonic() - started, 2)
    details = [record.details for record in bt.results.error]
    self.assertTrue(any('boom-bad' in detail for detail in details))
    self.assertTrue(any('gate' in detail for detail in details))


if __name__ == '__main__':
  unittest.main()
