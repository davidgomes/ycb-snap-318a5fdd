# Copyright 2016 Google Inc.
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

import collections
import contextlib
import copy
import functools
import inspect
import logging
import os
import re
import sys
import threading
import time

from mobly import controller_manager
from mobly import expects
from mobly import records
from mobly import runtime_test_info
from mobly import signals
from mobly import utils

# Macro strings for test result reporting.
TEST_CASE_TOKEN = '[Test]'
RESULT_LINE_TEMPLATE = TEST_CASE_TOKEN + ' %s %s'
TEST_SELECTOR_REGEX_PREFIX = 're:'

TEST_STAGE_BEGIN_LOG_TEMPLATE = '[{parent_token}]#{child_token} >>> BEGIN >>>'
TEST_STAGE_END_LOG_TEMPLATE = '[{parent_token}]#{child_token} <<< END <<<'

# Names of execution stages, in the order they happen during test runs.
STAGE_NAME_PRE_RUN = 'pre_run'
STAGE_NAME_SETUP_CLASS = 'setup_class'
STAGE_NAME_SETUP_TEST = 'setup_test'
STAGE_NAME_TEARDOWN_TEST = 'teardown_test'
STAGE_NAME_TEARDOWN_CLASS = 'teardown_class'
STAGE_NAME_CLEAN_UP = 'clean_up'
STAGE_NAME_GLOBAL_SETUP = 'global_setup'
STAGE_NAME_GLOBAL_TEARDOWN = 'global_teardown'
STAGE_NAME_GROUP_SETUP = 'group_setup'
STAGE_NAME_GROUP_TEARDOWN = 'group_teardown'

# Grouped-execution modes selected from controller config entries.
_GROUP_MODE_NONE = 'none'
_GROUP_MODE_IMPLICIT = 'implicit'
_GROUP_MODE_EXPLICIT = 'explicit'

_SYNC_PHASE_GROUP = 'group'
_SYNC_PHASE_TEST = 'test'

# Attribute names
ATTR_REPEAT_CNT = '_repeat_count'
ATTR_MAX_RETRY_CNT = '_max_retry_count'
ATTR_MAX_CONSEC_ERROR = '_max_consecutive_error'


class Error(Exception):
  """Raised for exceptions that occurred in BaseTestClass."""


class _BarrierBroken(Exception):
  """Internal signal that a synchronization barrier did not complete."""

  def __init__(self, reason):
    super().__init__(reason)
    self.reason = reason


class _SyncBarrier:
  """One-shot rendezvous for participants in a single grouped test."""

  def __init__(self, parties):
    self.parties = parties
    self._cond = threading.Condition()
    self._arrived = 0
    self._finished = False
    self.reason = None
    self._on_finish = None

  def set_on_finish(self, callback):
    """Registers a callback invoked once when the barrier finishes."""
    self._on_finish = callback

  def _mark_finished(self, reason):
    """Marks the barrier finished. Caller must hold the condition."""
    if self._finished:
      return False
    self._finished = True
    self.reason = reason
    try:
      if self._on_finish is not None:
        self._on_finish(self)
    finally:
      self._cond.notify_all()
    return True

  def abort(self, reason):
    """Releases every waiter and prevents later arrivals from blocking."""
    with self._cond:
      self._mark_finished(reason)

  def wait(self, timeout):
    """Waits until every party arrives.

    Args:
      timeout: seconds to wait, or None to wait indefinitely.

    Raises:
      _BarrierBroken: the barrier timed out, was aborted, or was already
        finished unsuccessfully.
    """
    with self._cond:
      if self._finished:
        if self.reason != 'completed':
          raise _BarrierBroken(self.reason or 'broken')
        raise _BarrierBroken('completed')
      self._arrived += 1
      if self._arrived >= self.parties:
        self._mark_finished('completed')
        return
      deadline = None if timeout is None else time.monotonic() + timeout
      while not self._finished:
        remaining = None
        if deadline is not None:
          remaining = deadline - time.monotonic()
          if remaining <= 0:
            self._mark_finished('timeout')
            raise _BarrierBroken('timeout')
        self._cond.wait(timeout=remaining)
      if self.reason != 'completed':
        raise _BarrierBroken(self.reason or 'broken')


class _WaveControl:
  """Cancellation flag for one parallel test wave."""

  def __init__(self):
    self.reason = None


_Participant = collections.namedtuple(
    'Participant', ['group', 'device_id', 'device']
)
_DeviceContext = collections.namedtuple(
    'DeviceContext',
    ['active', 'device', 'device_id', 'group_name', 'parties'],
)


def repeat(count, max_consecutive_error=None):
  """Decorator for repeating a test case multiple times.

  The BaseTestClass will execute the test cases annotated with this decorator
  the specified number of time.

  This decorator only stores the information needed for the repeat. It does not
  execute the repeat.

  Args:
    count: int, the total number of times to execute the decorated test case.
    max_consecutive_error: int, the maximum number of consecutively failed
      iterations allowed. If reached, the remaining iterations is abandoned.
      By default this is not enabled.

  Returns:
    The wrapped test function.

  Raises:
    ValueError, if the user input is invalid.
  """
  if count <= 1:
    raise ValueError(
        f'The `count` for `repeat` must be larger than 1, got "{count}".'
    )

  if max_consecutive_error is not None and max_consecutive_error > count:
    raise ValueError(
        f'The `max_consecutive_error` ({max_consecutive_error}) for `repeat` '
        f'must be smaller than `count` ({count}).'
    )

  def _outer_decorator(func):
    setattr(func, ATTR_REPEAT_CNT, count)
    setattr(func, ATTR_MAX_CONSEC_ERROR, max_consecutive_error)

    @functools.wraps(func)
    def _wrapper(*args):
      func(*args)

    return _wrapper

  return _outer_decorator


def retry(max_count):
  """Decorator for retrying a test case until it passes.

  The BaseTestClass will keep executing the test cases annotated with this
  decorator until the test passes, or the maxinum number of iterations have
  been met.

  This decorator only stores the information needed for the retry. It does not
  execute the retry.

  Args:
    max_count: int, the maximum number of times to execute the decorated test
      case.

  Returns:
    The wrapped test function.

  Raises:
    ValueError, if the user input is invalid.
  """
  if max_count <= 1:
    raise ValueError(
        f'The `max_count` for `retry` must be larger than 1, got "{max_count}".'
    )

  def _outer_decorator(func):
    setattr(func, ATTR_MAX_RETRY_CNT, max_count)

    @functools.wraps(func)
    def _wrapper(*args):
      func(*args)

    return _wrapper

  return _outer_decorator


class BaseTestClass:
  """Base class for all test classes to inherit from.

  This class gets all the controller objects from test_runner and executes
  the tests requested within itself.

  Most attributes of this class are set at runtime based on the configuration
  provided.

  The default logger in logging module is set up for each test run. If you
  want to log info to the test run output file, use `logging` directly, like
  `logging.info`.

  Attributes:
    tests: A list of strings, each representing a test method name.
    TAG: A string used to refer to a test class. Default is the test class
      name.
    results: A records.TestResult object for aggregating test results from
      the execution of tests.
    controller_configs: dict, controller configs provided by the user via
      test bed config.
    current_test_info: RuntimeTestInfo, runtime information on the test
      currently being executed.
    root_output_path: string, storage path for output files associated with
      the entire test run. A test run can have multiple test class
      executions. This includes the test summary and Mobly log files.
    log_path: string, storage path for files specific to a single test
      class execution.
    test_bed_name: [Deprecated, use 'testbed_name' instead]
      string, the name of the test bed used by a test run.
    testbed_name: string, the name of the test bed used by a test run.
    user_params: dict, custom parameters from user, to be consumed by
      the test logic.
    current_device: The active participant device. Available only inside
      `group_setup`, `group_teardown`, and test methods.
    current_device_id: Id of `current_device` from its controller config
      entry. Available in the same phases as `current_device`.
  """

  # Explicitly set the type since we set this to `None` in between
  # test cases executions when there's no active test. However, since
  # it is safe for clients to call at any point during normal execution
  # of a Mobly test, we avoid using the `Optional` type hint for convenience.
  current_test_info: runtime_test_info.RuntimeTestInfo

  TAG = None

  def __init__(self, configs):
    """Constructor of BaseTestClass.

    The constructor takes a config_parser.TestRunConfig object and which has
    all the information needed to execute this test class, like log_path
    and controller configurations. For details, see the definition of class
    config_parser.TestRunConfig.

    Args:
      configs: A config_parser.TestRunConfig object.
    """
    self.tests = []
    self._tls = threading.local()
    self._barrier_lock = threading.Lock()
    self._barriers = {}
    self._group_execution_mode = _GROUP_MODE_NONE
    self._sync_wave = None
    class_identifier = self.__class__.__name__
    if configs.test_class_name_suffix:
      class_identifier = '%s_%s' % (
          class_identifier,
          configs.test_class_name_suffix,
      )
    if self.TAG is None:
      self.TAG = class_identifier
    # Set params.
    self.root_output_path = configs.log_path
    self.log_path = os.path.join(self.root_output_path, class_identifier)
    utils.create_dir(self.log_path)
    # Deprecated, use 'testbed_name'
    self.test_bed_name = configs.test_bed_name
    self.testbed_name = configs.testbed_name
    self.user_params = configs.user_params
    self.results = records.TestResult()
    self.summary_writer = configs.summary_writer
    self._generated_test_table = collections.OrderedDict()
    self._controller_manager = controller_manager.ControllerManager(
        class_name=self.TAG, controller_configs=configs.controller_configs
    )
    self.controller_configs = self._controller_manager.controller_configs

  @property
  def current_test_info(self):
    """Runtime info for the test or stage running on this thread."""
    return getattr(self._tls, 'current_test_info', None)

  @current_test_info.setter
  def current_test_info(self, value):
    self._tls.current_test_info = value

  @property
  def current_device(self):
    """Device for the active group hook or test method.

    Raises:
      AttributeError: accessed outside `group_setup`, `group_teardown`, or
        a test method, or when the run has no controller config entries.
    """
    if not getattr(self._tls, 'device_context', False):
      raise AttributeError(
          "'%s' object has no attribute 'current_device'"
          % type(self).__name__
      )
    return self._tls.current_device

  @property
  def current_device_id(self):
    """Config id for `current_device`.

    Raises:
      AttributeError: accessed outside `group_setup`, `group_teardown`, or
        a test method, or when the run has no controller config entries.
    """
    if not getattr(self._tls, 'device_context', False):
      raise AttributeError(
          "'%s' object has no attribute 'current_device_id'"
          % type(self).__name__
      )
    return self._tls.current_device_id

  def unpack_userparams(
      self, req_param_names=None, opt_param_names=None, **kwargs
  ):
    """An optional function that unpacks user defined parameters into
    individual variables.

    After unpacking, the params can be directly accessed with self.xxx.

    If a required param is not provided, an exception is raised. If an
    optional param is not provided, a warning line will be logged.

    To provide a param, add it in the config file or pass it in as a kwarg.
    If a param appears in both the config file and kwarg, the value in the
    config file is used.

    User params from the config file can also be directly accessed in
    self.user_params.

    Args:
      req_param_names: A list of names of the required user params.
      opt_param_names: A list of names of the optional user params.
      **kwargs: Arguments that provide default values.
        e.g. unpack_userparams(required_list, opt_list, arg_a='hello')
        self.arg_a will be 'hello' unless it is specified again in
        required_list or opt_list.

    Raises:
      Error: A required user params is not provided.
    """
    req_param_names = req_param_names or []
    opt_param_names = opt_param_names or []
    for k, v in kwargs.items():
      if k in self.user_params:
        v = self.user_params[k]
      setattr(self, k, v)
    for name in req_param_names:
      if hasattr(self, name):
        continue
      if name not in self.user_params:
        raise Error(
            'Missing required user param "%s" in test configuration.' % name
        )
      setattr(self, name, self.user_params[name])
    for name in opt_param_names:
      if hasattr(self, name):
        continue
      if name in self.user_params:
        setattr(self, name, self.user_params[name])
      else:
        logging.warning(
            'Missing optional user param "%s" in configuration, continue.', name
        )

  def register_controller(self, module, required=True, min_number=1):
    """Loads a controller module and returns its loaded devices.

    A Mobly controller module is a Python lib that can be used to control
    a device, service, or equipment. To be Mobly compatible, a controller
    module needs to have the following members:

    .. code-block:: python

      def create(configs):
        [Required] Creates controller objects from configurations.

        Args:
          configs: A list of serialized data like string/dict. Each
            element of the list is a configuration for a controller
            object.

        Returns:
          A list of objects.

      def destroy(objects):
        [Required] Destroys controller objects created by the create
        function. Each controller object shall be properly cleaned up
        and all the resources held should be released, e.g. memory
        allocation, sockets, file handlers etc.

        Args:
          A list of controller objects created by the create function.

      def get_info(objects):
        [Optional] Gets info from the controller objects used in a test
        run. The info will be included in test_summary.yaml under
        the key 'ControllerInfo'. Such information could include unique
        ID, version, or anything that could be useful for describing the
        test bed and debugging.

        Args:
          objects: A list of controller objects created by the create
            function.

        Returns:
          A list of json serializable objects: each represents the
            info of a controller object. The order of the info
            object should follow that of the input objects.

    Registering a controller module declares a test class's dependency the
    controller. If the module config exists and the module matches the
    controller interface, controller objects will be instantiated with
    corresponding configs. The module should be imported first.

    Args:
      module: A module that follows the controller module interface.
      required: A bool. If True, failing to register the specified
        controller module raises exceptions. If False, the objects
        failed to instantiate will be skipped.
      min_number: An integer that is the minimum number of controller
        objects to be created. Default is one, since you should not
        register a controller module without expecting at least one
        object.

    Returns:
      A list of controller objects instantiated from controller_module, or
      None if no config existed for this controller and it was not a
      required controller.

    Raises:
      ControllerError:
        * The controller module has already been registered.
        * The actual number of objects instantiated is less than the
        * `min_number`.
        * `required` is True and no corresponding config can be found.
        * Any other error occurred in the registration process.
    """
    return self._controller_manager.register_controller(
        module, required, min_number
    )

  def _record_controller_info(self):
    # Collect controller information and write to test result.
    for record in self._controller_manager.get_controller_info_records():
      self.results.add_controller_info_record(record)
      self.summary_writer.dump(
          record.to_dict(), records.TestSummaryEntryType.CONTROLLER_INFO
      )

  def _pre_run(self):
    """Proxy function to guarantee the base implementation of `pre_run` is
    called.

    Returns:
      True if setup is successful, False otherwise.
    """
    stage_name = STAGE_NAME_PRE_RUN
    record = records.TestResultRecord(stage_name, self.TAG)
    record.test_begin()
    self.current_test_info = runtime_test_info.RuntimeTestInfo(
        stage_name, self.log_path, record
    )
    try:
      with self._log_test_stage(stage_name):
        self.pre_run()
      return True
    except Exception as e:
      logging.exception('%s failed for %s.', stage_name, self.TAG)
      record.test_error(e)
      self.results.add_class_error(record)
      self.summary_writer.dump(
          record.to_dict(), records.TestSummaryEntryType.RECORD
      )
      return False

  def pre_run(self):
    """Preprocesses that need to be done before setup_class.

    This phase is used to do pre-test processes like generating tests.
    This is the only place `self.generate_tests` should be called.

    If this function throws an error, the test class will be marked failure
    and the "Requested" field will be 0 because the number of tests
    requested is unknown at this point.
    """

  def _setup_class(self):
    """Proxy function to guarantee the base implementation of setup_class
    is called.

    Returns:
      If `self.results` is returned instead of None, this means something
      has gone wrong, and the rest of the test class should not execute.
    """
    # Setup for the class.
    class_record = records.TestResultRecord(STAGE_NAME_SETUP_CLASS, self.TAG)
    class_record.test_begin()
    self.current_test_info = runtime_test_info.RuntimeTestInfo(
        STAGE_NAME_SETUP_CLASS, self.log_path, class_record
    )
    expects.recorder.reset_internal_states(class_record)
    try:
      with self._log_test_stage(STAGE_NAME_SETUP_CLASS):
        self.setup_class()
    except signals.TestAbortSignal:
      # Throw abort signals to outer try block for handling.
      raise
    except Exception as e:
      # Setup class failed for unknown reasons.
      # Fail the class and skip all tests.
      logging.exception('Error in %s#setup_class.', self.TAG)
      class_record.test_error(e)
      self.results.add_class_error(class_record)
      self._exec_procedure_func(self._on_fail, class_record)
      class_record.update_record()
      self.summary_writer.dump(
          class_record.to_dict(), records.TestSummaryEntryType.RECORD
      )
      self._skip_remaining_tests(e)
      return self.results
    if expects.recorder.has_error:
      self._exec_procedure_func(self._on_fail, class_record)
      class_record.test_error()
      class_record.update_record()
      self.summary_writer.dump(
          class_record.to_dict(), records.TestSummaryEntryType.RECORD
      )
      self.results.add_class_error(class_record)
      self._skip_remaining_tests(class_record.termination_signal.exception)
      return self.results

  def setup_class(self):
    """Setup function that will be called before executing any test in the
    class.

    To signal setup failure, use asserts or raise your own exception.

    Errors raised from `setup_class` will trigger `on_fail`.

    Implementation is optional.
    """

  def _teardown_class(self):
    """Proxy function to guarantee the base implementation of
    teardown_class is called.
    """
    stage_name = STAGE_NAME_TEARDOWN_CLASS
    record = records.TestResultRecord(stage_name, self.TAG)
    record.test_begin()
    self.current_test_info = runtime_test_info.RuntimeTestInfo(
        stage_name, self.log_path, record
    )
    expects.recorder.reset_internal_states(record)
    try:
      with self._log_test_stage(stage_name):
        self.teardown_class()
    except signals.TestAbortAll as e:
      setattr(e, 'results', self.results)
      raise
    except Exception as e:
      logging.exception('Error encountered in %s.', stage_name)
      record.test_error(e)
      record.update_record()
      self.results.add_class_error(record)
      self.summary_writer.dump(
          record.to_dict(), records.TestSummaryEntryType.RECORD
      )
    else:
      if expects.recorder.has_error:
        record.test_error()
        record.update_record()
        self.results.add_class_error(record)
        self.summary_writer.dump(
            record.to_dict(), records.TestSummaryEntryType.RECORD
        )
    finally:
      try:
        self._run_global_teardown()
      finally:
        self._clean_up()

  def teardown_class(self):
    """Teardown function that will be called after all the selected tests in
    the test class have been executed.

    Errors raised from `teardown_class` do not trigger `on_fail`.

    Implementation is optional.
    """

  def global_setup(self):
    """Hook that runs once before `setup_class` and any group or test.

    Errors raised here are recorded under `global_setup`. No tests run, and
    `global_teardown` still runs.

    Implementation is optional.
    """

  def global_teardown(self):
    """Hook that runs once after class teardown and before controller cleanup.

    Implementation is optional.
    """

  def group_setup(self, devices):
    """Hook that runs once per device group before that group's tests.

    Args:
      devices: list of participants in this group. Registered controller
        objects are used when they pair 1:1 with config entries; otherwise
        the raw config entries are passed.

    Returns:
      False to skip this group's tests. `group_teardown` still runs.

    Implementation is optional.
    """

  def group_teardown(self, devices):
    """Hook that runs once per device group after that group's tests.

    Runs even when `group_setup` fails or returns False, and even when tests
    fail.

    Args:
      devices: list of participants in this group. Same objects that were
        passed to `group_setup`.

    Implementation is optional.
    """

  @contextlib.contextmanager
  def _log_test_stage(self, stage_name):
    """Logs the begin and end of a test stage.

    This context adds two log lines meant for clarifying the boundary of
    each execution stage in Mobly log.

    Args:
      stage_name: string, name of the stage to log.
    """
    parent_token = self.current_test_info.name
    # If the name of the stage is the same as the test name, in which case
    # the stage is class-level instead of test-level, use the class's
    # reference tag as the parent token instead.
    if parent_token == stage_name:
      parent_token = self.TAG
    logging.debug(
        TEST_STAGE_BEGIN_LOG_TEMPLATE.format(
            parent_token=parent_token, child_token=stage_name
        )
    )
    try:
      yield
    finally:
      logging.debug(
          TEST_STAGE_END_LOG_TEMPLATE.format(
              parent_token=parent_token, child_token=stage_name
          )
      )

  def _setup_test(self, test_name):
    """Proxy function to guarantee the base implementation of setup_test is
    called.
    """
    with self._log_test_stage(STAGE_NAME_SETUP_TEST):
      self.setup_test()

  def setup_test(self):
    """Setup function that will be called every time before executing each
    test method in the test class.

    To signal setup failure, use asserts or raise your own exception.

    Implementation is optional.
    """

  def _teardown_test(self, test_name):
    """Proxy function to guarantee the base implementation of teardown_test
    is called.
    """
    with self._log_test_stage(STAGE_NAME_TEARDOWN_TEST):
      self.teardown_test()

  def teardown_test(self):
    """Teardown function that will be called every time a test method has
    been executed.

    Implementation is optional.
    """

  def _on_fail(self, record):
    """Proxy function to guarantee the base implementation of on_fail is
    called.

    Args:
      record: records.TestResultRecord, a copy of the test record for
          this test, containing all information of the test execution
          including exception objects.
    """
    self.on_fail(record)

  def on_fail(self, record):
    """A function that is executed upon a test failure.

    User implementation is optional.

    Args:
      record: records.TestResultRecord, a copy of the test record for
        this test, containing all information of the test execution
        including exception objects.
    """

  def _on_pass(self, record):
    """Proxy function to guarantee the base implementation of on_pass is
    called.

    Args:
      record: records.TestResultRecord, a copy of the test record for
        this test, containing all information of the test execution
        including exception objects.
    """
    msg = record.details
    if msg:
      logging.info(msg)
    self.on_pass(record)

  def on_pass(self, record):
    """A function that is executed upon a test passing.

    Implementation is optional.

    Args:
      record: records.TestResultRecord, a copy of the test record for
        this test, containing all information of the test execution
        including exception objects.
    """

  def _on_skip(self, record):
    """Proxy function to guarantee the base implementation of on_skip is
    called.

    Args:
      record: records.TestResultRecord, a copy of the test record for
        this test, containing all information of the test execution
        including exception objects.
    """
    logging.info('Reason to skip: %s', record.details)
    logging.info(RESULT_LINE_TEMPLATE, record.test_name, record.result)
    self.on_skip(record)

  def on_skip(self, record):
    """A function that is executed upon a test being skipped.

    Implementation is optional.

    Args:
      record: records.TestResultRecord, a copy of the test record for
        this test, containing all information of the test execution
        including exception objects.
    """

  def _exec_procedure_func(self, func, tr_record):
    """Executes a procedure function like on_pass, on_fail etc.

    This function will alter the 'Result' of the test's record if
    exceptions happened when executing the procedure function, but
    prevents procedure functions from altering test records themselves
    by only passing in a copy.

    This will let signals.TestAbortAll through so abort_all works in all
    procedure functions.

    Args:
      func: The procedure function to be executed.
      tr_record: The TestResultRecord object associated with the test
        executed.
    """
    func_name = func.__name__
    procedure_name = func_name[1:] if func_name[0] == '_' else func_name
    with self._log_test_stage(procedure_name):
      try:
        # Pass a copy of the record instead of the actual object so that it
        # will not be modified.
        func(copy.deepcopy(tr_record))
      except signals.TestAbortSignal:
        raise
      except Exception as e:
        logging.exception(
            'Exception happened when executing %s for %s.',
            procedure_name,
            self.current_test_info.name,
        )
        tr_record.add_error(procedure_name, e)

  def record_data(self, content):
    """Record an entry in test summary file.

    Sometimes additional data need to be recorded in summary file for
    debugging or post-test analysis.

    Each call adds a new entry to the summary file, with no guarantee of
    its position among the summary file entries.

    The content should be a dict. If absent, timestamp field is added for
    ease of parsing later.

    Args:
      content: dict, the data to add to summary file.
    """
    if 'timestamp' not in content:
      content = content.copy()
      content['timestamp'] = utils.get_current_epoch_time()
    self.summary_writer.dump(content, records.TestSummaryEntryType.USER_DATA)

  def synchronized_step(self, name, timeout=None):
    """Blocks until every participant in the current group reaches `name`.

    Allowed only in `group_setup`, `group_teardown`, and test methods. In
    group hooks this returns immediately. In test methods it synchronizes
    only in explicit grouped mode; otherwise it returns immediately.

    A barrier is identified by `(instance, group, hook or test name, name)`.
    After every participant passes it, the next call with the same name
    creates a new barrier.

    Args:
      name: string, barrier name.
      timeout: seconds to wait. `None` waits indefinitely. `0` is rejected.
        Negative values are rejected.

    Raises:
      ValueError: `timeout` is negative.
      signals.TestError: called outside an allowed phase, `timeout` is 0, or
        the barrier timed out or was aborted. Illegal-phase errors include
        the substring `synchronized_step`. Timeout and abort errors include
        `name`.
    """
    if timeout is not None and timeout < 0:
      raise ValueError(
          'timeout for synchronized_step must be non-negative, got %s.'
          % timeout
      )
    phase = getattr(self._tls, 'sync_phase', None)
    if phase not in (_SYNC_PHASE_GROUP, _SYNC_PHASE_TEST):
      raise signals.TestError(
          'synchronized_step is only allowed in group_setup, '
          'group_teardown, and test methods.'
      )
    if timeout == 0:
      raise signals.TestError(
          "synchronized_step '%s' failed: timeout must be positive, got 0."
          % name
      )
    if (
        phase == _SYNC_PHASE_GROUP
        or self._group_execution_mode != _GROUP_MODE_EXPLICIT
    ):
      return
    self._wait_barrier(name, timeout)

  @contextlib.contextmanager
  def synchronized_context(self, name, timeout=None):
    """Synchronizes participants when the context is entered.

    Exit does not synchronize. The same phase and timeout rules as
    `synchronized_step` apply.

    Args:
      name: string, barrier name.
      timeout: seconds to wait, or None.

    Yields:
      None.
    """
    self.synchronized_step(name, timeout=timeout)
    yield

  def _sync_error_message(self, name, reason):
    if reason == 'timeout':
      return "synchronized_step '%s' timed out." % name
    return "synchronized_step '%s' failed: %s." % (name, reason)

  def _wave_cancel_reason(self):
    wave = self._sync_wave
    if wave is None:
      return None
    return wave.reason

  def _discard_barrier(self, key, barrier):
    with self._barrier_lock:
      if self._barriers.get(key) is barrier:
        del self._barriers[key]

  def _begin_barrier_wait(self, key, parties):
    with self._barrier_lock:
      reason = self._wave_cancel_reason()
      if reason is not None:
        return None, reason
      barrier = self._barriers.get(key)
      if barrier is None:
        barrier = _SyncBarrier(parties)
        barrier.set_on_finish(
            lambda finished, key=key: self._discard_barrier(key, finished)
        )
        self._barriers[key] = barrier
      return barrier, None

  def _abort_all_barriers(self, reason):
    with self._barrier_lock:
      barriers = list(self._barriers.values())
    for barrier in barriers:
      barrier.abort(reason)

  def _cancel_sync_wave(self, reason):
    """Releases participants blocked in the current explicit test wave."""
    if self._sync_wave is None:
      return
    with self._barrier_lock:
      if self._sync_wave.reason is None:
        self._sync_wave.reason = reason
      else:
        reason = self._sync_wave.reason
      barriers = list(self._barriers.values())
    for barrier in barriers:
      barrier.abort(reason)

  def _wait_barrier(self, name, timeout):
    info = self.current_test_info
    hook_name = info.name if info is not None else None
    group_name = getattr(self._tls, 'group_name', None)
    parties = getattr(self._tls, 'sync_parties', 0) or 0
    if parties <= 0:
      return
    key = (self, group_name, hook_name, name)
    barrier, cancel_reason = self._begin_barrier_wait(key, parties)
    if barrier is None:
      raise signals.TestError(self._sync_error_message(name, cancel_reason))
    try:
      barrier.wait(timeout)
    except _BarrierBroken as exc:
      raise signals.TestError(
          self._sync_error_message(name, exc.reason)
      ) from exc
    except Exception as exc:
      self._abort_all_barriers(str(exc))
      raise signals.TestError(
          self._sync_error_message(name, exc)
      ) from exc

  def _enter_test_method_context(self, device_context):
    """Enables sync and, when active, the current device for a test body."""
    self._tls.sync_phase = _SYNC_PHASE_TEST
    if device_context is None:
      self._tls.group_name = None
      self._tls.sync_parties = 0
      self._tls.device_context = False
      return
    self._tls.group_name = device_context.group_name
    self._tls.sync_parties = device_context.parties
    if device_context.active:
      self._tls.device_context = True
      self._tls.current_device = device_context.device
      self._tls.current_device_id = device_context.device_id
    else:
      self._tls.device_context = False

  def _exit_execution_context(self):
    self._tls.sync_phase = None
    self._tls.device_context = False
    self._tls.group_name = None
    self._tls.sync_parties = 0
    self._tls.current_device = None
    self._tls.current_device_id = None

  def _enter_group_hook_context(self, participant):
    self._tls.sync_phase = _SYNC_PHASE_GROUP
    self._tls.group_name = participant.group
    self._tls.sync_parties = 0
    self._tls.device_context = True
    self._tls.current_device = participant.device
    self._tls.current_device_id = participant.device_id

  def _commit_test_record(self, tr_record):
    self.results.add_record(tr_record)
    self.summary_writer.dump(
        tr_record.to_dict(), records.TestSummaryEntryType.RECORD
    )

  def _exec_one_test_with_retry(
      self, test_name, test_method, max_count, device_context=None, on_record=None
  ):
    """Executes one test and retry the test if needed.

    Repeatedly execute a test case until it passes or the maximum count of
    iteration has been reached.

    Args:
      test_name: string, Name of the test.
      test_method: function, The test method to execute.
      max_count: int, the maximum number of iterations to execute the test for.
    """

    def should_retry(record):
      return record.result in [
          records.TestResultEnums.TEST_RESULT_FAIL,
          records.TestResultEnums.TEST_RESULT_ERROR,
      ]

    previous_record = self.exec_one_test(
        test_name,
        test_method,
        device_context=device_context,
        on_record=on_record,
    )

    if not should_retry(previous_record):
      return

    for i in range(max_count - 1):
      retry_name = f'{test_name}_retry_{i+1}'
      new_record = records.TestResultRecord(retry_name, self.TAG)
      new_record.retry_parent = previous_record
      new_record.parent = (previous_record, records.TestParentType.RETRY)
      previous_record = self.exec_one_test(
          retry_name,
          test_method,
          new_record,
          device_context=device_context,
          on_record=on_record,
      )
      if not should_retry(previous_record):
        break

  def _exec_one_test_with_repeat(
      self,
      test_name,
      test_method,
      repeat_count,
      max_consecutive_error,
      device_context=None,
      on_record=None,
  ):
    """Repeatedly execute a test case.

    This method performs the action defined by the `repeat` decorator.

    If the number of consecutive failures reach the threshold set by
    `max_consecutive_error`, the remaining iterations will be abandoned.

    Args:
      test_name: string, Name of the test.
      test_method: function, The test method to execute.
      repeat_count: int, the number of times to repeat the test case.
      max_consecutive_error: int, the maximum number of consecutive iterations
        allowed to fail before abandoning the remaining iterations.
    """

    consecutive_error_count = 0

    # If max_consecutive_error is not set by user, it is considered the same as
    # the repeat_count.
    if max_consecutive_error == 0:
      max_consecutive_error = repeat_count

    previous_record = None
    for i in range(repeat_count):
      new_test_name = f'{test_name}_{i}'
      new_record = records.TestResultRecord(new_test_name, self.TAG)
      if i > 0:
        new_record.parent = (previous_record, records.TestParentType.REPEAT)
      previous_record = self.exec_one_test(
          new_test_name,
          test_method,
          new_record,
          device_context=device_context,
          on_record=on_record,
      )
      if previous_record.result in [
          records.TestResultEnums.TEST_RESULT_FAIL,
          records.TestResultEnums.TEST_RESULT_ERROR,
      ]:
        consecutive_error_count += 1
      else:
        consecutive_error_count = 0

      if consecutive_error_count == max_consecutive_error:
        logging.error(
            'Repeated test case "%s" has consecutively failed %d iterations, '
            'aborting the remaining %d iterations.',
            test_name,
            consecutive_error_count,
            repeat_count - 1 - i,
        )
        return

  def exec_one_test(
      self,
      test_name,
      test_method,
      record=None,
      device_context=None,
      on_record=None,
  ):
    """Executes one test and update test results.

    Executes setup_test, the test method, and teardown_test; then creates a
    records.TestResultRecord object with the execution information and adds
    the record to the test class's test results.

    Args:
      test_name: string, Name of the test.
      test_method: function, The test method to execute.
      record: records.TestResultRecord, optional arg for injecting a record
        object to use for this test execution. If not set, a new one is created
        created. This is meant for passing information between consecutive test
        case execution for retry purposes. Do NOT abuse this for "magical"
        features.

    Returns:
      TestResultRecord, the test result record object of the test execution.
      This object is strictly for read-only purposes. Modifying this record
      will not change what is reported in the test run's summary yaml file.
    """
    tr_record = record or records.TestResultRecord(test_name, self.TAG)
    tr_record.uid = getattr(test_method, 'uid', None)
    tr_record.test_begin()
    self.current_test_info = runtime_test_info.RuntimeTestInfo(
        test_name, self.log_path, tr_record
    )
    expects.recorder.reset_internal_states(tr_record)
    logging.info('%s %s', TEST_CASE_TOKEN, test_name)
    # Did teardown_test throw an error.
    teardown_test_failed = False
    try:
      try:
        try:
          self._setup_test(test_name)
        except signals.TestFailure as e:
          _, _, traceback = sys.exc_info()
          raise signals.TestError(e.details, e.extras).with_traceback(traceback)
        self._enter_test_method_context(device_context)
        try:
          test_method()
        finally:
          self._exit_execution_context()
      except (signals.TestPass, signals.TestAbortSignal, signals.TestSkip):
        raise
      except Exception:
        logging.exception(
            'Exception occurred in %s.', self.current_test_info.name
        )
        raise
      finally:
        before_count = expects.recorder.error_count
        try:
          self._teardown_test(test_name)
        except signals.TestAbortSignal:
          raise
        except Exception as e:
          logging.exception(
              'Exception occurred in %s of %s.',
              STAGE_NAME_TEARDOWN_TEST,
              self.current_test_info.name,
          )
          tr_record.test_error()
          tr_record.add_error(STAGE_NAME_TEARDOWN_TEST, e)
          teardown_test_failed = True
        else:
          # Check if anything failed by `expects`.
          if before_count < expects.recorder.error_count:
            tr_record.test_error()
            teardown_test_failed = True
    except (signals.TestFailure, AssertionError) as e:
      tr_record.test_fail(e)
    except signals.TestSkip as e:
      # Test skipped.
      tr_record.test_skip(e)
    except signals.TestAbortSignal as e:
      # Abort signals, pass along.
      tr_record.test_fail(e)
      raise
    except signals.TestPass as e:
      # Explicit test pass.
      tr_record.test_pass(e)
    except Exception as e:
      # Exception happened during test.
      tr_record.test_error(e)
    else:
      # No exception is thrown from test and teardown, if `expects` has
      # error, the test should fail with the first error in `expects`.
      if expects.recorder.has_error and not teardown_test_failed:
        tr_record.test_fail()
      # Otherwise the test passed.
      elif not teardown_test_failed:
        tr_record.test_pass()
    finally:
      tr_record.update_record()
      try:
        if tr_record.result in (
            records.TestResultEnums.TEST_RESULT_ERROR,
            records.TestResultEnums.TEST_RESULT_FAIL,
        ):
          self._exec_procedure_func(self._on_fail, tr_record)
        elif tr_record.result == records.TestResultEnums.TEST_RESULT_PASS:
          self._exec_procedure_func(self._on_pass, tr_record)
        elif tr_record.result == records.TestResultEnums.TEST_RESULT_SKIP:
          self._exec_procedure_func(self._on_skip, tr_record)
      finally:
        logging.info(
            RESULT_LINE_TEMPLATE, tr_record.test_name, tr_record.result
        )
        if on_record is not None:
          on_record(tr_record)
        else:
          self._commit_test_record(tr_record)
        self.current_test_info = None
    return tr_record

  def _assert_function_names_in_stack(self, expected_func_names):
    """Asserts that the current stack contains any of the given function names."""
    current_frame = inspect.currentframe()
    caller_frames = inspect.getouterframes(current_frame, 2)
    for caller_frame in caller_frames[2:]:
      if caller_frame[3] in expected_func_names:
        return
    raise Error(
        f"'{caller_frames[1][3]}' cannot be called outside of the "
        f'following functions: {expected_func_names}.'
    )

  def generate_tests(self, test_logic, name_func, arg_sets, uid_func=None):
    """Generates tests in the test class.

    This function has to be called inside a test class's `self.pre_run`.

    Generated tests are not written down as methods, but as a list of
    parameter sets. This way we reduce code repetition and improve test
    scalability.

    Users can provide an optional function to specify the UID of each test.
    Not all generated tests are required to have UID.

    Args:
      test_logic: function, the common logic shared by all the generated
        tests.
      name_func: function, generate a test name according to a set of
        test arguments. This function should take the same arguments as
        the test logic function.
      arg_sets: a list of tuples, each tuple is a set of arguments to be
        passed to the test logic function and name function.
      uid_func: function, an optional function that takes the same
        arguments as the test logic function and returns a string that
        is the corresponding UID.
    """
    self._assert_function_names_in_stack([STAGE_NAME_PRE_RUN])
    root_msg = 'During test generation of "%s":' % test_logic.__name__
    for args in arg_sets:
      test_name = name_func(*args)
      if test_name in self.get_existing_test_names():
        raise Error(
            '%s Test name "%s" already exists, cannot be duplicated!'
            % (root_msg, test_name)
        )
      test_func = functools.partial(test_logic, *args)
      # If the `test_logic` method is decorated by `retry` or `repeat`
      # decorators, copy the attributes added by the decorators to the
      # generated test methods as well, so the generated test methods
      # also have the retry/repeat behavior.
      for attr_name in (
          ATTR_MAX_RETRY_CNT,
          ATTR_MAX_CONSEC_ERROR,
          ATTR_REPEAT_CNT,
      ):
        attr = getattr(test_logic, attr_name, None)
        if attr is not None:
          setattr(test_func, attr_name, attr)
      if uid_func is not None:
        uid = uid_func(*args)
        if uid is None:
          logging.warning('%s UID for arg set %s is None.', root_msg, args)
        else:
          setattr(test_func, 'uid', uid)
      self._generated_test_table[test_name] = test_func

  def _safe_exec_func(self, func, *args):
    """Executes a function with exception safeguard.

    This will let signals.TestAbortAll through so abort_all works in all
    procedure functions.

    Args:
      func: Function to be executed.
      args: Arguments to be passed to the function.

    Returns:
      Whatever the function returns.
    """
    try:
      return func(*args)
    except signals.TestAbortAll:
      raise
    except Exception:
      logging.exception(
          'Exception happened when executing %s in %s.', func.__name__, self.TAG
      )

  def get_existing_test_names(self):
    """Gets the names of existing tests in the class.

    A method in the class is considered a test if its name starts with
    'test_*'.

    Note this only gets the names of tests that already exist. If
    `generate_tests` has not happened when this was called, the
    generated tests won't be listed.

    Returns:
      A list of strings, each is a test method name.
    """
    test_names = []
    for name, _ in inspect.getmembers(type(self), callable):
      if name.startswith('test_'):
        test_names.append(name)
    return test_names + list(self._generated_test_table.keys())

  def _get_test_methods(self, test_names):
    """Resolves test method names to bound test methods.

    Args:
      test_names: A list of strings, each string is a test method name or a
        regex for matching test names.

    Returns:
      A list of tuples of (string, function). String is the test method
      name, function is the actual python method implementing its logic.

    Raises:
      Error: The test name does not follow naming convention 'test_*'.
        This can only be caused by user input.
    """
    test_methods = []
    # Process the test name selector one by one.
    for test_name in test_names:
      if test_name.startswith(TEST_SELECTOR_REGEX_PREFIX):
        # process the selector as a regex.
        regex_matching_methods = self._get_regex_matching_test_methods(
            test_name.removeprefix(TEST_SELECTOR_REGEX_PREFIX)
        )
        test_methods += regex_matching_methods
        continue
      # process the selector as a regular test name string.
      self._assert_valid_test_name(test_name)
      if test_name not in self.get_existing_test_names():
        raise Error(f'{self.TAG} does not have test method {test_name}.')
      if hasattr(self, test_name):
        test_method = getattr(self, test_name)
      elif test_name in self._generated_test_table:
        test_method = self._generated_test_table[test_name]
      test_methods.append((test_name, test_method))
    return test_methods

  def _get_regex_matching_test_methods(self, test_name_regex):
    matching_name_tuples = []
    for name, method in inspect.getmembers(self, callable):
      if (
          name.startswith('test_')
          and re.fullmatch(test_name_regex, name) is not None
      ):
        matching_name_tuples.append((name, method))
    for name, method in self._generated_test_table.items():
      if re.fullmatch(test_name_regex, name) is not None:
        self._assert_valid_test_name(name)
        matching_name_tuples.append((name, method))
    if not matching_name_tuples:
      raise Error(
          f'{test_name_regex} does not match with any valid test case '
          f'in {self.TAG}, abort!'
      )
    return matching_name_tuples

  def _assert_valid_test_name(self, test_name):
    if not test_name.startswith('test_'):
      raise Error(
          'Test method name %s does not follow naming '
          'convention test_*, abort.' % test_name
      )

  def _skip_remaining_tests(self, exception):
    """Marks any requested test that has not been executed in a class as
    skipped.

    This is useful for handling abort class signal.

    Args:
      exception: The exception object that was thrown to trigger the
        skip.
    """
    for test_name in self.results.requested:
      if not self.results.is_test_executed(test_name):
        test_record = records.TestResultRecord(test_name, self.TAG)
        test_record.test_skip(exception)
        self.results.add_record(test_record)
        self.summary_writer.dump(
            test_record.to_dict(), records.TestSummaryEntryType.RECORD
        )

  def _record_stage_exception(self, record, exception, trigger_on_fail):
    """Records a stage failure and optionally runs `on_fail`."""
    logging.exception('Error in %s#%s.', self.TAG, record.test_name)
    if trigger_on_fail:
      record.test_error(exception)
      self._exec_procedure_func(self._on_fail, record)
    else:
      record.test_error(exception)
    record.update_record()
    self.results.add_class_error(record)
    self.summary_writer.dump(
        record.to_dict(), records.TestSummaryEntryType.RECORD
    )

  def _run_global_setup(self):
    """Runs `global_setup`. Returns False when tests must not run."""
    stage_name = STAGE_NAME_GLOBAL_SETUP
    record = records.TestResultRecord(stage_name, self.TAG)
    record.test_begin()
    self.current_test_info = runtime_test_info.RuntimeTestInfo(
        stage_name, self.log_path, record
    )
    expects.recorder.reset_internal_states(record)
    try:
      try:
        with self._log_test_stage(stage_name):
          self.global_setup()
      except signals.TestAbortSignal:
        raise
      except Exception as e:
        self._record_stage_exception(record, e, trigger_on_fail=True)
        self._skip_remaining_tests(e)
        return False
      if expects.recorder.has_error:
        self._exec_procedure_func(self._on_fail, record)
        record.test_error()
        record.update_record()
        self.summary_writer.dump(
            record.to_dict(), records.TestSummaryEntryType.RECORD
        )
        self.results.add_class_error(record)
        self._skip_remaining_tests(record.termination_signal.exception)
        return False
      return True
    finally:
      self.current_test_info = None

  def _run_global_teardown(self):
    """Runs `global_teardown` and records failures without aborting cleanup."""
    stage_name = STAGE_NAME_GLOBAL_TEARDOWN
    record = records.TestResultRecord(stage_name, self.TAG)
    record.test_begin()
    self.current_test_info = runtime_test_info.RuntimeTestInfo(
        stage_name, self.log_path, record
    )
    expects.recorder.reset_internal_states(record)
    try:
      with self._log_test_stage(stage_name):
        self.global_teardown()
    except signals.TestAbortAll as e:
      setattr(e, 'results', self.results)
      raise
    except Exception as e:
      self._record_stage_exception(record, e, trigger_on_fail=False)
    else:
      if expects.recorder.has_error:
        record.test_error()
        record.update_record()
        self.results.add_class_error(record)
        self.summary_writer.dump(
            record.to_dict(), records.TestSummaryEntryType.RECORD
        )
    finally:
      self.current_test_info = None

  def _collect_config_entries(self):
    """Flattens controller config entries in config iteration order."""
    entries = []
    configs = self.controller_configs or {}
    if not isinstance(configs, dict):
      return entries
    for value in configs.values():
      if isinstance(value, (list, tuple)):
        entries.extend(value)
    return entries

  def _devices_for_entries(self, entries):
    """Pairs config entries with registered objects when possible.

    Objects are preferred when every controller config list lines up 1:1 with
    the objects created from it. Otherwise, if the flattened object list has
    the same length as the entries, those objects are used in registration
    order. Any other shape uses the raw config entries.

    Group and id still come from the config entries in both cases.
    """
    configs = self.controller_configs or {}
    by_config = self._controller_manager.get_objects_by_config_name()
    config_lists = []
    if isinstance(configs, dict):
      for config_name, value in configs.items():
        if isinstance(value, (list, tuple)):
          config_lists.append((config_name, list(value)))
    if config_lists and all(
        config_name in by_config
        and len(by_config[config_name]) == len(config_entries)
        for config_name, config_entries in config_lists
    ):
      devices = []
      for config_name, _ in config_lists:
        devices.extend(by_config[config_name])
      return devices
    objects = self._controller_manager.get_registered_objects()
    if len(objects) == len(entries):
      return list(objects)
    return list(entries)

  def _resolve_participants(self):
    """Builds grouped participants from controller config entries.

    Returns:
      A tuple `(mode, groups)`. `groups` is an ordered mapping of group name
      to a list of `_Participant`. `mode` is none, implicit, or explicit.
    """
    entries = self._collect_config_entries()
    if not entries:
      return _GROUP_MODE_NONE, collections.OrderedDict()
    explicit = any(
        isinstance(entry, dict) and 'group' in entry for entry in entries
    )
    devices = self._devices_for_entries(entries)
    participants = []
    for entry, device in zip(entries, devices):
      if isinstance(entry, dict):
        group = entry['group'] if 'group' in entry else 'default'
        device_id = entry['id'] if 'id' in entry else None
      else:
        group = 'default'
        device_id = None
      participants.append(_Participant(group, device_id, device))
    groups = collections.OrderedDict()
    if not explicit:
      groups['default'] = participants
      return _GROUP_MODE_IMPLICIT, groups
    for participant in participants:
      groups.setdefault(participant.group, []).append(participant)
    return _GROUP_MODE_EXPLICIT, groups

  def _call_group_hook(self, stage_name, func, participants):
    """Calls `group_setup` or `group_teardown` once for a group.

    Returns:
      A tuple `(ok, value)`. `ok` is False when the hook raises or records an
      expectation failure. `value` is the hook's return value, or None when
      it failed. Abort signals propagate.
    """
    record = records.TestResultRecord(stage_name, self.TAG)
    record.test_begin()
    self.current_test_info = runtime_test_info.RuntimeTestInfo(
        stage_name, self.log_path, record
    )
    expects.recorder.reset_internal_states(record)
    devices = [participant.device for participant in participants]
    self._enter_group_hook_context(participants[0])
    ok = True
    result = None
    try:
      try:
        with self._log_test_stage(stage_name):
          result = func(devices)
      except signals.TestAbortSignal:
        raise
      except Exception as e:
        ok = False
        trigger_on_fail = stage_name == STAGE_NAME_GROUP_SETUP
        self._record_stage_exception(
            record, e, trigger_on_fail=trigger_on_fail
        )
      else:
        if expects.recorder.has_error:
          ok = False
          if stage_name == STAGE_NAME_GROUP_SETUP:
            self._exec_procedure_func(self._on_fail, record)
          record.test_error()
          record.update_record()
          self.results.add_class_error(record)
          self.summary_writer.dump(
              record.to_dict(), records.TestSummaryEntryType.RECORD
          )
      return ok, result
    finally:
      self._exit_execution_context()
      self.current_test_info = None

  def _dispatch_one_test(
      self, test_name, test_method, device_context=None, on_record=None
  ):
    max_consecutive_error = getattr(test_method, ATTR_MAX_CONSEC_ERROR, 0)
    repeat_count = getattr(test_method, ATTR_REPEAT_CNT, 0)
    max_retry_count = getattr(test_method, ATTR_MAX_RETRY_CNT, 0)
    if max_retry_count:
      self._exec_one_test_with_retry(
          test_name,
          test_method,
          max_retry_count,
          device_context=device_context,
          on_record=on_record,
      )
    elif repeat_count:
      self._exec_one_test_with_repeat(
          test_name,
          test_method,
          repeat_count,
          max_consecutive_error,
          device_context=device_context,
          on_record=on_record,
      )
    else:
      self.exec_one_test(
          test_name,
          test_method,
          device_context=device_context,
          on_record=on_record,
      )

  def _run_tests_sequential(self, tests, device_context):
    for test_name, test_method in tests:
      self._dispatch_one_test(test_name, test_method, device_context)

  def _run_tests_explicit(self, tests, participants):
    for test_name, test_method in tests:
      self._run_one_test_explicit(test_name, test_method, participants)

  def _run_one_test_explicit(self, test_name, test_method, participants):
    buckets = [[] for _ in participants]
    errors = []
    error_lock = threading.Lock()
    self._sync_wave = _WaveControl()

    def _worker(index, participant):
      device_context = _DeviceContext(
          active=True,
          device=participant.device,
          device_id=participant.device_id,
          group_name=participant.group,
          parties=len(participants),
      )

      def _on_record(record):
        buckets[index].append(record)

      try:
        self._dispatch_one_test(
            test_name, test_method, device_context, on_record=_on_record
        )
      except signals.TestAbortSignal as exc:
        with error_lock:
          errors.append(exc)
      except Exception as exc:
        logging.exception(
            'Grouped execution failed for %s participant %s.',
            test_name,
            participant.device_id,
        )
        with error_lock:
          errors.append(exc)
      finally:
        for record in buckets[index]:
          if record.result != records.TestResultEnums.TEST_RESULT_PASS:
            self._cancel_sync_wave(
                'participant finished with %s' % record.result
            )
            break

    threads = []
    for index, participant in enumerate(participants):
      thread = threading.Thread(
          target=_worker,
          args=(index, participant),
          name='mobly-%s-%s-%s'
          % (participant.group, test_name, index),
      )
      threads.append(thread)
      thread.start()
    for thread in threads:
      thread.join()
    self._sync_wave = None
    for bucket in buckets:
      for record in bucket:
        self._commit_test_record(record)
    for exc in errors:
      if isinstance(exc, signals.TestAbortSignal):
        raise exc
    for exc in errors:
      raise exc

  def _run_grouped_tests(self, tests):
    """Runs tests once, or once per participant when groups are explicit."""
    mode, groups = self._resolve_participants()
    self._group_execution_mode = mode
    try:
      if mode == _GROUP_MODE_NONE:
        self._run_tests_sequential(tests, device_context=None)
        return
      for group_name, participants in groups.items():
        try:
          setup_ok, setup_result = self._call_group_hook(
              STAGE_NAME_GROUP_SETUP, self.group_setup, participants
          )
          # An exception, expectation failure, or explicit False skips tests.
          if not setup_ok or setup_result is False:
            continue
          if mode == _GROUP_MODE_EXPLICIT:
            self._run_tests_explicit(tests, participants)
          else:
            first = participants[0]
            device_context = _DeviceContext(
                active=True,
                device=first.device,
                device_id=first.device_id,
                group_name=group_name,
                parties=len(participants),
            )
            self._run_tests_sequential(tests, device_context)
        finally:
          self._call_group_hook(
              STAGE_NAME_GROUP_TEARDOWN, self.group_teardown, participants
          )
    finally:
      self._group_execution_mode = _GROUP_MODE_NONE
      self._sync_wave = None

  def run(self, test_names=None):
    """Runs tests within a test class.

    One of these test method lists will be executed, shown here in priority
    order:

    1. The test_names list, which is passed from cmd line. Invalid names
       are guarded by cmd line arg parsing.
    2. The self.tests list defined in test class. Invalid names are
       ignored.
    3. All function that matches test method naming convention in the test
       class.

    Args:
      test_names: A list of string that are test method names requested in
        cmd line.

    Returns:
      The test results object of this class.
    """
    logging.log_path = self.log_path
    # Executes pre-setup procedures, like generating test methods.
    if not self._pre_run():
      return self.results
    logging.info('==========> %s <==========', self.TAG)
    # Devise the actual test methods to run in the test class.
    if not test_names:
      if self.tests:
        # Specified by run list in class.
        test_names = list(self.tests)
      else:
        # No test method specified by user, execute all in test class.
        test_names = self.get_existing_test_names()
    self.results.requested = test_names
    self.summary_writer.dump(
        self.results.requested_test_names_dict(),
        records.TestSummaryEntryType.TEST_NAME_LIST,
    )
    tests = self._get_test_methods(test_names)
    try:
      if not self._run_global_setup():
        return self.results
      setup_class_result = self._setup_class()
      if setup_class_result:
        return setup_class_result
      self._run_grouped_tests(tests)
      return self.results
    except signals.TestAbortClass as e:
      e.details = 'Test class aborted due to: %s' % e.details
      self._skip_remaining_tests(e)
      return self.results
    except signals.TestAbortAll as e:
      e.details = 'All remaining tests aborted due to: %s' % e.details
      self._skip_remaining_tests(e)
      # Piggy-back test results on this exception object so we don't lose
      # results from this test class.
      setattr(e, 'results', self.results)
      raise e
    finally:
      self._teardown_class()
      logging.info(
          'Summary for test class %s: %s', self.TAG, self.results.summary_str()
      )

  def _clean_up(self):
    """The final stage of a test class execution."""
    stage_name = STAGE_NAME_CLEAN_UP
    record = records.TestResultRecord(stage_name, self.TAG)
    record.test_begin()
    self.current_test_info = runtime_test_info.RuntimeTestInfo(
        stage_name, self.log_path, record
    )
    expects.recorder.reset_internal_states(record)
    with self._log_test_stage(stage_name):
      # Write controller info and summary to summary file.
      self._record_controller_info()
      self._controller_manager.unregister_controllers()
      if expects.recorder.has_error:
        record.test_error()
        record.update_record()
        self.results.add_class_error(record)
        self.summary_writer.dump(
            record.to_dict(), records.TestSummaryEntryType.RECORD
        )
