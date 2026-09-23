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

# Device group used when a config entry does not name one.
DEFAULT_DEVICE_GROUP = 'default'

# Execution phases in which synchronization and current_device are valid.
_PHASE_GROUP_SETUP = STAGE_NAME_GROUP_SETUP
_PHASE_GROUP_TEARDOWN = STAGE_NAME_GROUP_TEARDOWN
_PHASE_TEST = 'test'
_SYNC_ALLOWED_PHASES = (
    _PHASE_GROUP_SETUP,
    _PHASE_GROUP_TEARDOWN,
    _PHASE_TEST,
)

_DeviceParticipant = collections.namedtuple(
    '_DeviceParticipant',
    ['device', 'device_id', 'group_name', 'entry'],
)

# Attribute names
ATTR_REPEAT_CNT = '_repeat_count'
ATTR_MAX_RETRY_CNT = '_max_retry_count'
ATTR_MAX_CONSEC_ERROR = '_max_consecutive_error'


class Error(Exception):
  """Raised for exceptions that occurred in BaseTestClass."""


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
    current_device: The device for the active group phase or test method.
      Available only inside `group_setup`, `group_teardown`, and test
      methods.
    current_device_id: Id of `current_device` from its controller config
      entry. Available in the same phases as `current_device`.
  """

  # Explicitly set the type since we set this to `None` in between
  # test cases executions when there's no active test. However, since
  # it is safe for clients to call at any point during normal execution
  # of a Mobly test, we avoid using the `Optional` type hint for convenience.
  current_test_info: runtime_test_info.RuntimeTestInfo

  TAG = None

  @property
  def current_test_info(self):
    """Runtime info for the stage running on the calling thread."""
    return getattr(self._thread_local, 'current_test_info', None)

  @current_test_info.setter
  def current_test_info(self, value):
    self._thread_local.current_test_info = value

  @property
  def current_device(self):
    """Device for the active group phase or the executing participant.

    Available only inside `group_setup`, `group_teardown`, and test methods.

    Raises:
      AttributeError: If accessed outside those phases, or when the run has
        no controller config entries.
    """
    if not getattr(self._thread_local, 'device_context_active', False):
      raise AttributeError(
          "'%s' object has no attribute 'current_device'"
          % type(self).__name__
      )
    return getattr(self._thread_local, 'current_device_value', None)

  @property
  def current_device_id(self):
    """Config id of `current_device`.

    Available only inside `group_setup`, `group_teardown`, and test methods.
    The value is `None` when the config entry does not set `id`.

    Raises:
      AttributeError: If accessed outside those phases, or when the run has
        no controller config entries.
    """
    if not getattr(self._thread_local, 'device_context_active', False):
      raise AttributeError(
          "'%s' object has no attribute 'current_device_id'"
          % type(self).__name__
      )
    return getattr(self._thread_local, 'current_device_id_value', None)

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
    # Per-thread execution context. Grouped tests run participants
    # concurrently, so device and stage state cannot live on the instance.
    self._thread_local = threading.local()
    self._barrier_lock = threading.Lock()
    # (instance, group, hook, step) -> round slot. A finished or failed
    # round is closed so the next call builds a new barrier.
    self._barriers = {}
    # (instance, group, hook) -> Event set when the test body fails and
    # waiters in that hook must be released.
    self._hook_aborts = {}
    self._signature_lock = threading.Lock()
    self._used_signatures = set()

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
      self._clean_up()

  def teardown_class(self):
    """Teardown function that will be called after all the selected tests in
    the test class have been executed.

    Errors raised from `teardown_class` do not trigger `on_fail`.

    Implementation is optional.
    """

  def global_setup(self):
    """Setup run once before any device group or test method.

    Executed after a successful `setup_class`. Failures are recorded under
    `global_setup`, skip every test method, and still run `global_teardown`.

    `current_device` is not available in this phase.

    Implementation is optional.
    """

  def global_teardown(self):
    """Teardown run once after device groups finish.

    Runs after tests (and after a failed `global_setup`) and before
    `teardown_class`. Failures are recorded under `global_teardown`.

    `current_device` is not available in this phase.

    Implementation is optional.
    """

  def group_setup(self, devices):
    """Setup run once for a device group before that group's tests.

    Args:
      devices: list of devices in this group. Objects are the registered
        controller instances when they pair 1:1 with config entries;
        otherwise they are the raw config entries.

    Returns:
      `False` to skip this group's tests. Any other value continues.

    `current_device` is the first device in `devices`.

    Implementation is optional.
    """

  def group_teardown(self, devices):
    """Teardown run once for a device group after that group's tests.

    Runs even when `group_setup` failed or returned `False`, and even when
    tests in the group failed.

    Args:
      devices: list of devices passed to `group_setup` for this group.

    `current_device` is the first device in `devices`.

    Implementation is optional.
    """

  def synchronized_step(self, name, timeout=None):
    """Blocks until every participant in the current group reaches `name`.

    Allowed only in `group_setup`, `group_teardown`, and test methods. In
    group phases this returns immediately. In test methods it synchronizes
    participants only in explicit group mode; otherwise it returns
    immediately.

    The barrier identity is `(this instance, group, current hook or test
    name, name)`. After the participants complete a step, a later call with
    the same name uses a new barrier.

    Args:
      name: Hashable step name. Included in errors raised by this method.
      timeout: Seconds to wait. `None` waits indefinitely. `0` fails
        immediately. Negative values are rejected.

    Raises:
      signals.TestError: If called outside an allowed phase, if `timeout`
        is `0`, or if the step times out or fails. Context errors include
        the substring `synchronized_step`. Timeout and failure errors
        mention `name`.
      ValueError: If `timeout` is negative.
    """
    self._assert_sync_allowed()
    self._validate_sync_timeout(name, timeout)
    if not self._sync_should_block():
      return
    barrier = self._begin_sync(name)
    if barrier is None:
      raise signals.TestError('Synchronization point "%s" failed.' % name)
    start = time.monotonic()
    try:
      barrier.wait(timeout)
    except threading.BrokenBarrierError:
      self._finish_sync_failure(name)
      if timeout is not None and (time.monotonic() - start) >= timeout:
        raise signals.TestError(
            'Synchronization point "%s" timed out.' % name
        )
      raise signals.TestError('Synchronization point "%s" failed.' % name)
    except signals.TestError:
      self._finish_sync_failure(name)
      raise
    except Exception as e:
      self._finish_sync_failure(name)
      raise signals.TestError(
          'Synchronization point "%s" failed: %s' % (name, e)
      ) from e

  @contextlib.contextmanager
  def synchronized_context(self, name, timeout=None):
    """Synchronizes participants on entry, then runs the body.

    The exit of the context does not synchronize. The same phase, timeout,
    and error rules as `synchronized_step` apply. Errors raised because the
    call is not allowed include the substring `synchronized_step`.

    Args:
      name: Step name passed to `synchronized_step`.
      timeout: Timeout passed to `synchronized_step`.

    Yields:
      None.
    """
    self.synchronized_step(name, timeout=timeout)
    yield

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

  def _exec_one_test_with_retry(self, test_name, test_method, max_count):
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

    previous_record = self.exec_one_test(test_name, test_method)

    if not should_retry(previous_record):
      return

    for i in range(max_count - 1):
      retry_name = f'{test_name}_retry_{i+1}'
      new_record = records.TestResultRecord(retry_name, self.TAG)
      new_record.retry_parent = previous_record
      new_record.parent = (previous_record, records.TestParentType.RETRY)
      previous_record = self.exec_one_test(retry_name, test_method, new_record)
      if not should_retry(previous_record):
        break

  def _exec_one_test_with_repeat(
      self, test_name, test_method, repeat_count, max_consecutive_error
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
          new_test_name, test_method, new_record
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

  def exec_one_test(self, test_name, test_method, record=None):
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
    self._reserve_record_signature(tr_record)
    self.current_test_info = runtime_test_info.RuntimeTestInfo(
        test_name, self.log_path, tr_record
    )
    expects.recorder.reset_internal_states(tr_record)
    logging.info('%s %s', TEST_CASE_TOKEN, test_name)
    # Did teardown_test throw an error.
    teardown_test_failed = False
    # Arm the barrier key before setup so a setup failure releases
    # participants already waiting inside the test method.
    self._thread_local.sync_hook_name = test_name
    try:
      try:
        try:
          self._setup_test(test_name)
        except signals.TestFailure as e:
          self._abort_current_synchronization()
          _, _, traceback = sys.exc_info()
          raise signals.TestError(e.details, e.extras).with_traceback(
              traceback
          )
        except Exception:
          self._abort_current_synchronization()
          raise
        with self._test_method_context(test_name):
          try:
            test_method()
          except signals.TestPass:
            raise
          except Exception:
            self._abort_current_synchronization()
            raise
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
      self._thread_local.sync_phase = None
      self._thread_local.sync_hook_name = None
      self._thread_local.device_context_active = False
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
        self._publish_test_record(tr_record)
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

  def _publish_test_record(self, tr_record):
    """Stores a finished test record.

    Concurrent participants append to a thread-local list so the caller can
    publish records in participant order. Every other execution publishes
    immediately.
    """
    deferred = getattr(self._thread_local, 'deferred_records', None)
    if deferred is not None:
      deferred.append(tr_record)
      return
    self.results.add_record(tr_record)
    self.summary_writer.dump(
        tr_record.to_dict(), records.TestSummaryEntryType.RECORD
    )

  @contextlib.contextmanager
  def _test_method_context(self, test_name):
    """Activates test-method device and synchronization context."""
    tl = self._thread_local
    tl.sync_phase = _PHASE_TEST
    tl.sync_hook_name = test_name
    if getattr(tl, 'enable_device_context', False):
      tl.current_device_value = getattr(tl, 'context_device', None)
      tl.current_device_id_value = getattr(tl, 'context_device_id', None)
      tl.device_context_active = True
    else:
      tl.device_context_active = False
    try:
      yield
    finally:
      tl.sync_phase = None
      tl.device_context_active = False

  def _assert_sync_allowed(self):
    phase = getattr(self._thread_local, 'sync_phase', None)
    if phase not in _SYNC_ALLOWED_PHASES:
      raise signals.TestError(
          'synchronized_step cannot be called outside of group_setup, '
          'group_teardown, or a test method.'
      )

  def _validate_sync_timeout(self, name, timeout):
    if timeout is None:
      return
    if timeout < 0:
      raise ValueError(
          'timeout for synchronized step "%s" must be >= 0, got %s.'
          % (name, timeout)
      )
    if timeout == 0:
      if self._sync_should_block():
        self._finish_sync_failure(name)
      raise signals.TestError(
          'Synchronization point "%s" failed because timeout is 0.' % name
      )

  def _sync_should_block(self):
    """True when this thread must wait for the rest of its group."""
    tl = self._thread_local
    if getattr(tl, 'sync_phase', None) != _PHASE_TEST:
      return False
    if not getattr(tl, 'explicit_sync', False):
      return False
    return getattr(tl, 'sync_parties', 0) >= 1

  def _hook_key(self):
    tl = self._thread_local
    group_name = getattr(tl, 'sync_group', None)
    hook_name = getattr(tl, 'sync_hook_name', None)
    if group_name is None or hook_name is None:
      return None
    return (self, group_name, hook_name)

  def _barrier_key(self, name):
    hook_key = self._hook_key()
    if hook_key is None:
      return None
    return hook_key + (name,)

  def _hook_aborted(self):
    key = self._hook_key()
    if key is None:
      return False
    event = self._hook_aborts.get(key)
    return event is not None and event.is_set()

  def _new_sync_slot(self, parties):
    return {
        'barrier': threading.Barrier(parties),
        'failed': False,
        'seen': 0,
        'parties': parties,
        'closed': False,
    }

  def _begin_sync(self, name):
    """Returns the barrier to wait on, or None if this round already failed."""
    key = self._barrier_key(name)
    if key is None:
      return None
    parties = self._thread_local.sync_parties
    with self._barrier_lock:
      if self._hook_aborted():
        return None
      slot = self._barriers.get(key)
      if slot is None or slot['closed']:
        slot = self._new_sync_slot(parties)
        self._barriers[key] = slot
      if slot['failed']:
        slot['seen'] += 1
        if slot['seen'] >= slot['parties']:
          slot['closed'] = True
        return None
      return slot['barrier']

  def _finish_sync_failure(self, name):
    """Releases waiters on `name` and closes the round once everyone sees it."""
    key = self._barrier_key(name)
    if key is None:
      return
    parties = getattr(self._thread_local, 'sync_parties', 0) or 1
    with self._barrier_lock:
      slot = self._barriers.get(key)
      if slot is None or slot['closed']:
        slot = self._new_sync_slot(parties)
        self._barriers[key] = slot
      slot['failed'] = True
      slot['seen'] += 1
      if slot['seen'] >= slot['parties']:
        slot['closed'] = True
      barrier = slot['barrier']
    barrier.abort()

  def _abort_current_synchronization(self):
    """Releases participants waiting anywhere in the current hook."""
    hook_key = self._hook_key()
    if hook_key is None:
      return
    _instance, group_name, hook_name = hook_key
    with self._barrier_lock:
      event = self._hook_aborts.get(hook_key)
      if event is None:
        event = threading.Event()
        self._hook_aborts[hook_key] = event
      event.set()
      victims = []
      for barrier_key, slot in self._barriers.items():
        if (
            barrier_key[0] is self
            and barrier_key[1] == group_name
            and barrier_key[2] == hook_name
            and not slot['closed']
        ):
          slot['failed'] = True
          victims.append(slot['barrier'])
    for barrier in victims:
      barrier.abort()

  def _reset_sync_state(self):
    with self._barrier_lock:
      slots = list(self._barriers.values())
      self._barriers.clear()
      self._hook_aborts.clear()
    for slot in slots:
      slot['barrier'].abort()

  def _reserve_record_signature(self, record):
    """Gives concurrently started records distinct signatures."""
    with self._signature_lock:
      while record.signature in self._used_signatures:
        record.begin_time += 1
        record.signature = '%s-%s' % (record.test_name, record.begin_time)
      self._used_signatures.add(record.signature)

  def _collect_config_entries(self):
    """Returns `(config_name, entry)` pairs from controller configs."""
    configs = self.controller_configs or {}
    if isinstance(configs, list):
      return [(None, entry) for entry in configs]
    if not isinstance(configs, dict):
      return []
    keyed = []
    for config_name, value in configs.items():
      if isinstance(value, list):
        for entry in value:
          keyed.append((config_name, entry))
    return keyed

  def _group_and_id_from_entry(self, entry):
    if isinstance(entry, dict):
      if 'group' in entry and entry['group'] is not None:
        group_name = entry['group']
      else:
        group_name = DEFAULT_DEVICE_GROUP
      device_id = entry['id'] if 'id' in entry else None
    else:
      group_name = DEFAULT_DEVICE_GROUP
      device_id = None
    return group_name, device_id

  def _registered_objects_by_config_name(self):
    manager = self._controller_manager
    objects_by_name = {}
    for ref_name, module in manager._controller_modules.items():
      config_name = module.MOBLY_CONTROLLER_CONFIG_NAME
      objects_by_name[config_name] = list(
          manager._controller_objects.get(ref_name, [])
      )
    return objects_by_name

  def _pair_devices(self, keyed_entries, objects_by_name):
    """Pairs config entries with registered objects when lengths match."""
    entries = [entry for _config_name, entry in keyed_entries]
    if not keyed_entries or not objects_by_name:
      return list(entries)
    counts = {}
    for config_name, _entry in keyed_entries:
      counts[config_name] = counts.get(config_name, 0) + 1
    total_objects = sum(len(objs) for objs in objects_by_name.values())
    can_pair = total_objects == len(keyed_entries)
    if can_pair:
      for config_name, count in counts.items():
        objs = objects_by_name.get(config_name)
        if objs is None or len(objs) != count:
          can_pair = False
          break
    if not can_pair:
      return list(entries)
    cursors = {name: 0 for name in objects_by_name}
    devices = []
    for config_name, _entry in keyed_entries:
      index = cursors[config_name]
      devices.append(objects_by_name[config_name][index])
      cursors[config_name] = index + 1
    return devices

  def _build_participants(self, keyed_entries):
    objects_by_name = self._registered_objects_by_config_name()
    devices = self._pair_devices(keyed_entries, objects_by_name)
    participants = []
    for (_config_name, entry), device in zip(keyed_entries, devices):
      group_name, device_id = self._group_and_id_from_entry(entry)
      participants.append(
          _DeviceParticipant(device, device_id, group_name, entry)
      )
    return participants

  def _entries_use_explicit_groups(self, keyed_entries):
    return any(
        isinstance(entry, dict) and 'group' in entry
        for _config_name, entry in keyed_entries
    )

  def _group_participants(self, participants):
    groups = collections.OrderedDict()
    for participant in participants:
      groups.setdefault(participant.group_name, []).append(participant)
    return groups

  def _record_stage_error(self, record, exception):
    if exception is None:
      record.test_error()
    else:
      record.test_error(exception)
    record.update_record()
    self.results.add_class_error(record)
    self.summary_writer.dump(
        record.to_dict(), records.TestSummaryEntryType.RECORD
    )

  @contextlib.contextmanager
  def _group_phase(self, phase_name, group_name, participants):
    participant = participants[0]
    record = records.TestResultRecord(phase_name, self.TAG)
    record.test_begin()
    self.current_test_info = runtime_test_info.RuntimeTestInfo(
        phase_name, self.log_path, record
    )
    expects.recorder.reset_internal_states(record)
    tl = self._thread_local
    tl.sync_phase = phase_name
    tl.sync_hook_name = phase_name
    tl.sync_group = group_name
    tl.explicit_sync = False
    tl.sync_parties = len(participants)
    tl.device_context_active = True
    tl.current_device_value = participant.device
    tl.current_device_id_value = participant.device_id
    try:
      with self._log_test_stage(phase_name):
        yield record
    finally:
      tl.sync_phase = None
      tl.sync_hook_name = None
      tl.device_context_active = False

  def _run_group_setup(self, group_name, participants):
    devices = [participant.device for participant in participants]
    with self._group_phase(
        STAGE_NAME_GROUP_SETUP, group_name, participants
    ) as record:
      try:
        result = self.group_setup(devices)
      except signals.TestAbortSignal:
        raise
      except Exception as exc:
        logging.exception(
            'Error in %s for group %s.', STAGE_NAME_GROUP_SETUP, group_name
        )
        self._record_stage_error(record, exc)
        return False
      if result is False:
        logging.warning(
            '%s for group %s returned False; skipping tests in this group.',
            STAGE_NAME_GROUP_SETUP,
            group_name,
        )
        return False
      if expects.recorder.has_error:
        self._record_stage_error(record, None)
        return False
      return True

  def _run_group_teardown(self, group_name, participants):
    devices = [participant.device for participant in participants]
    with self._group_phase(
        STAGE_NAME_GROUP_TEARDOWN, group_name, participants
    ) as record:
      try:
        self.group_teardown(devices)
      except signals.TestAbortSignal:
        raise
      except Exception as exc:
        logging.exception(
            'Error in %s for group %s.',
            STAGE_NAME_GROUP_TEARDOWN,
            group_name,
        )
        self._record_stage_error(record, exc)
        return
      if expects.recorder.has_error:
        self._record_stage_error(record, None)

  def _run_test_batch(self, tests):
    for test_name, test_method in tests:
      max_consecutive_error = getattr(test_method, ATTR_MAX_CONSEC_ERROR, 0)
      repeat_count = getattr(test_method, ATTR_REPEAT_CNT, 0)
      max_retry_count = getattr(test_method, ATTR_MAX_RETRY_CNT, 0)
      if max_retry_count:
        self._exec_one_test_with_retry(test_name, test_method, max_retry_count)
      elif repeat_count:
        self._exec_one_test_with_repeat(
            test_name, test_method, repeat_count, max_consecutive_error
        )
      else:
        self.exec_one_test(test_name, test_method)

  def _execute_tests_sequential(self, tests, participant, group_name):
    tl = self._thread_local
    tl.explicit_sync = False
    tl.sync_group = group_name
    tl.sync_parties = 1
    tl.deferred_records = None
    if participant is None:
      tl.enable_device_context = False
    else:
      tl.enable_device_context = True
      tl.context_device = participant.device
      tl.context_device_id = participant.device_id
    try:
      self._run_test_batch(tests)
    finally:
      tl.enable_device_context = False
      tl.device_context_active = False
      tl.sync_phase = None
      tl.sync_hook_name = None
      tl.sync_group = None

  def _participant_worker(
      self,
      participant,
      group_name,
      parties,
      test_name,
      test_method,
      injected_record,
      bucket,
      errors,
      error_lock,
  ):
    tl = self._thread_local
    tl.explicit_sync = True
    tl.sync_group = group_name
    tl.sync_parties = parties
    tl.enable_device_context = True
    tl.context_device = participant.device
    tl.context_device_id = participant.device_id
    tl.deferred_records = []
    try:
      try:
        self.exec_one_test(test_name, test_method, injected_record)
      except signals.TestAbortSignal as exc:
        self._abort_current_synchronization()
        with error_lock:
          errors.append(exc)
      except Exception as exc:
        self._abort_current_synchronization()
        with error_lock:
          errors.append(exc)
    finally:
      bucket.extend(tl.deferred_records)
      tl.deferred_records = None
      tl.enable_device_context = False
      tl.device_context_active = False
      tl.sync_phase = None
      tl.sync_hook_name = None

  def _exec_concurrent_once(
      self,
      test_name,
      test_method,
      group_name,
      participants,
      previous_records,
      parent_type,
  ):
    errors = []
    error_lock = threading.Lock()
    buckets = [[] for _participant in participants]
    threads = []
    for index, participant in enumerate(participants):
      injected = None
      if previous_records is not None:
        injected = records.TestResultRecord(test_name, self.TAG)
        previous = previous_records[index]
        if previous is not None and parent_type is not None:
          injected.parent = (previous, parent_type)
          if parent_type == records.TestParentType.RETRY:
            injected.retry_parent = previous
      thread = threading.Thread(
          target=self._participant_worker,
          args=(
              participant,
              group_name,
              len(participants),
              test_name,
              test_method,
              injected,
              buckets[index],
              errors,
              error_lock,
          ),
          name='%s-%s' % (test_name, index),
      )
      threads.append(thread)
    for thread in threads:
      thread.start()
    for thread in threads:
      thread.join()
    participant_records = []
    for bucket in buckets:
      for tr_record in bucket:
        self.results.add_record(tr_record)
        self.summary_writer.dump(
            tr_record.to_dict(), records.TestSummaryEntryType.RECORD
        )
      participant_records.append(bucket[-1] if bucket else None)
    for error in errors:
      if isinstance(error, signals.TestAbortSignal):
        raise error
    return participant_records

  def _record_failed(self, record):
    if record is None:
      return True
    return record.result in (
        records.TestResultEnums.TEST_RESULT_FAIL,
        records.TestResultEnums.TEST_RESULT_ERROR,
    )

  def _exec_concurrent_retry(
      self, test_name, test_method, max_count, group_name, participants
  ):
    previous = None
    for attempt in range(max_count):
      if attempt == 0:
        name = test_name
        parent_type = None
        previous_for_call = None
      else:
        name = '%s_retry_%s' % (test_name, attempt)
        parent_type = records.TestParentType.RETRY
        previous_for_call = previous
      previous = self._exec_concurrent_once(
          name,
          test_method,
          group_name,
          participants,
          previous_for_call,
          parent_type,
      )
      if previous and not any(self._record_failed(record) for record in previous):
        return

  def _exec_concurrent_repeat(
      self,
      test_name,
      test_method,
      repeat_count,
      max_consecutive_error,
      group_name,
      participants,
  ):
    if not max_consecutive_error:
      max_consecutive_error = repeat_count
    consecutive_error_count = 0
    previous = None
    for index in range(repeat_count):
      name = '%s_%s' % (test_name, index)
      if index == 0:
        parent_type = None
        previous_for_call = None
      else:
        parent_type = records.TestParentType.REPEAT
        previous_for_call = previous
      previous = self._exec_concurrent_once(
          name,
          test_method,
          group_name,
          participants,
          previous_for_call,
          parent_type,
      )
      if any(self._record_failed(record) for record in previous):
        consecutive_error_count += 1
      else:
        consecutive_error_count = 0
      if consecutive_error_count == max_consecutive_error:
        logging.error(
            'Repeated test case "%s" has consecutively failed %d iterations, '
            'aborting the remaining %d iterations.',
            test_name,
            consecutive_error_count,
            repeat_count - 1 - index,
        )
        return

  def _execute_tests_explicit(self, tests, group_name, participants):
    for test_name, test_method in tests:
      repeat_count = getattr(test_method, ATTR_REPEAT_CNT, 0)
      max_retry_count = getattr(test_method, ATTR_MAX_RETRY_CNT, 0)
      max_consecutive_error = getattr(test_method, ATTR_MAX_CONSEC_ERROR, 0)
      if max_retry_count:
        self._exec_concurrent_retry(
            test_name, test_method, max_retry_count, group_name, participants
        )
      elif repeat_count:
        self._exec_concurrent_repeat(
            test_name,
            test_method,
            repeat_count,
            max_consecutive_error,
            group_name,
            participants,
        )
      else:
        self._exec_concurrent_once(
            test_name, test_method, group_name, participants, None, None
        )

  def _run_one_group(self, group_name, participants, tests, explicit):
    try:
      setup_ok = self._run_group_setup(group_name, participants)
    except signals.TestAbortSignal:
      self._run_group_teardown(group_name, participants)
      raise
    try:
      if not setup_ok:
        return
      if explicit:
        self._execute_tests_explicit(tests, group_name, participants)
      else:
        self._execute_tests_sequential(tests, participants[0], group_name)
    finally:
      self._run_group_teardown(group_name, participants)

  def _execute_tests_with_groups(self, tests):
    self._reset_sync_state()
    keyed_entries = self._collect_config_entries()
    if not keyed_entries:
      self._execute_tests_sequential(tests, None, None)
      return
    participants = self._build_participants(keyed_entries)
    explicit = self._entries_use_explicit_groups(keyed_entries)
    groups = self._group_participants(participants)
    if not explicit:
      group_name, group_participants = next(iter(groups.items()))
      self._run_one_group(group_name, group_participants, tests, False)
      return
    for group_name, group_participants in groups.items():
      self._run_one_group(group_name, group_participants, tests, True)

  def _exec_global_setup(self):
    """Runs global_setup.

    Returns:
      False when setup failed and no tests should run.
    """
    stage_name = STAGE_NAME_GLOBAL_SETUP
    record = records.TestResultRecord(stage_name, self.TAG)
    record.test_begin()
    self.current_test_info = runtime_test_info.RuntimeTestInfo(
        stage_name, self.log_path, record
    )
    expects.recorder.reset_internal_states(record)
    self._thread_local.device_context_active = False
    self._thread_local.sync_phase = None
    try:
      with self._log_test_stage(stage_name):
        self.global_setup()
    except signals.TestAbortSignal:
      raise
    except Exception as exc:
      logging.exception('Error in %s#%s.', self.TAG, stage_name)
      record.test_error(exc)
      self._exec_procedure_func(self._on_fail, record)
      record.update_record()
      self.summary_writer.dump(
          record.to_dict(), records.TestSummaryEntryType.RECORD
      )
      self.results.add_class_error(record)
      self._skip_remaining_tests(exc)
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

  def _exec_global_teardown(self):
    stage_name = STAGE_NAME_GLOBAL_TEARDOWN
    record = records.TestResultRecord(stage_name, self.TAG)
    record.test_begin()
    self.current_test_info = runtime_test_info.RuntimeTestInfo(
        stage_name, self.log_path, record
    )
    expects.recorder.reset_internal_states(record)
    self._thread_local.device_context_active = False
    self._thread_local.sync_phase = None
    try:
      with self._log_test_stage(stage_name):
        self.global_teardown()
    except signals.TestAbortAll as exc:
      setattr(exc, 'results', self.results)
      raise exc
    except Exception as exc:
      logging.exception('Error encountered in %s.', stage_name)
      record.test_error(exc)
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
      setup_class_result = self._setup_class()
      if setup_class_result:
        return setup_class_result
      try:
        if self._exec_global_setup():
          self._execute_tests_with_groups(tests)
        return self.results
      finally:
        self._exec_global_teardown()
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
