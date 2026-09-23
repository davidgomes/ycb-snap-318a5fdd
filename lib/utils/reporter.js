'use strict';

const EventEmitter = require('events').EventEmitter;
const Bluebird = require('bluebird');
const log = require('npmlog');

const reporters = require('../reporters');
const isa = require('./isa');
const ReportFile = require('./report-file');

function setupReporter(name, out, config, app) {
  let reporter;

  if (isa(name, String)) {
    let TestReporter = reporters[name];
    if (TestReporter) {
      reporter = new TestReporter(false, out, config, app);
    }
  } else if (isa(name, Function)) {
    // name is a constructor function, ignore new-cap and instantiate
    // eslint-disable-next-line new-cap
    reporter = new name(false, out, config, app);
  } else {
    reporter = name;
  }

  if (!reporter) {
    throw new Error('Test reporter `' + name + '` not found.');
  }

  return reporter;
}

// Returns the number of failures that triggers a bail, or 0 when bailing is disabled.
function bailThresholdFrom(value) {
  if (value === undefined || value === null || value === false) {
    return 0;
  }
  if (value === true) {
    return 1;
  }
  if (Number.isInteger(value) && value > 0) {
    return value;
  }

  log.warn('bail_on_test_failure', `Invalid value ${JSON.stringify(value)}, expected true, false or a positive integer. Defaulting to false.`);
  return 0;
}

function isBailableFailure(result) {
  return !result.passed && !result.skipped && !result.todo;
}

function testName(result) {
  return result.name === undefined || result.name === null ? 'unknown test' : String(result.name);
}

class Reporter extends EventEmitter {
  constructor(app, stdout, path) {
    super();

    this.total = 0;
    this.passed = 0;
    this.skipped = 0;
    this.todo = 0;

    if (path) {
      this.reportFile = new ReportFile(path);
    }

    let config = app.config;

    this.bailThreshold = bailThresholdFrom(config.get('bail_on_test_failure'));
    this.bailOnTestFailure = this.bailThreshold > 0 ? this.bailThreshold : false;
    this._clearBailState();

    if (path && config.get('xunit_intermediate_output') && config.get('reporter') === 'xunit') {
      this.reporters = [
        setupReporter('tap', stdout, config, app),
        setupReporter(config.get('reporter'), this.reportFile.outputStream, config, app)
      ];
    } else {
      this.reporters = [setupReporter(config.get('reporter'), stdout, config, app)];

      if (path) {
        if (config.appMode === 'dev') {
          let devModeFileReporter = config.get('dev_mode_file_reporter');
          if (!devModeFileReporter) {
            log.warn('You configured a `report_file`, you may want to configure the `dev_mode_file_reporter` as well. Using the `tap` logger now.');
            devModeFileReporter = 'tap';
          }
          this.reporters.push(setupReporter(devModeFileReporter, this.reportFile.outputStream, config, app));
        } else {
          this.reporters.push(setupReporter(config.get('reporter'), this.reportFile.outputStream, config, app));
        }
      }
    }
  }

  testStarted(name, data) {
    this.reporters.forEach(reporter => {
      if (reporter.testStarted) {
        reporter.testStarted(name, data);
      }
    });
  }

  close() {
    this.finish();

    if (this.reportFile) {
      return this.reportFile.close();
    }
  }

  hasTests() {
    return this.total > 0;
  }

  hasPassed() {
    return this.total <= ((this.passed || 0) + (this.skipped || 0) + (this.todo || 0));
  }

  report(name, result) {
    if (this._bailed) {
      this._suppressedAfterBail++;
      this._forwardBailState();
      return;
    }

    this.total++;
    this._testsSinceReset++;
    if (result.skipped) {
      this.skipped++;
    } else if (result.passed && !result.todo) {
      this.passed++;
    } else if (!result.passed && result.todo) {
      this.todo++;
    }

    let shouldBail = false;
    if (isBailableFailure(result)) {
      this._recordFailure(name, result);
      shouldBail = this.bailThreshold > 0 && this._failedTests.length >= this.bailThreshold;
    }

    if (shouldBail) {
      this._bailed = true;
      this.bailReason = testName(result);
      this._bailLauncher = name === undefined ? null : name;
      this._testsRanBeforeBail = this._testsSinceReset;
    }

    this.reporters.forEach(reporter => {
      reporter.report(name, result);
    });

    if (shouldBail) {
      this._forwardBailState();
      this.emit('test-failure', name, result);
    }
  }

  hasBailed() {
    return this._bailed;
  }

  getBailReport() {
    return {
      bailed: this._bailed,
      bailReason: this.bailReason,
      bailLauncher: this._bailLauncher,
      testsRanBeforeBail: this._testsRanBeforeBail,
      suppressedAfterBail: this._suppressedAfterBail,
      failureCount: this._failedTests.length,
      failuresByLauncher: Object.assign({}, this._failuresByLauncher),
      failedTests: this._failedTests.slice()
    };
  }

  resetBailState() {
    this._clearBailState();

    this.reporters.forEach(reporter => {
      if (typeof reporter.resetBailState === 'function') {
        reporter.resetBailState();
      }
    });
  }

  _clearBailState() {
    this._bailed = false;
    this.bailReason = null;
    this._bailLauncher = null;
    this._testsSinceReset = 0;
    this._testsRanBeforeBail = 0;
    this._suppressedAfterBail = 0;
    this._failuresByLauncher = {};
    this._failedTests = [];
  }

  _recordFailure(name, result) {
    let launcher = String(name);
    let previous = Object.prototype.hasOwnProperty.call(this._failuresByLauncher, launcher) ? this._failuresByLauncher[launcher] : 0;

    this._failuresByLauncher[launcher] = previous + 1;
    this._failedTests.push(testName(result));
  }

  _forwardBailState() {
    let bailState = {
      reason: this.bailReason,
      launcher: this._bailLauncher,
      failureCount: this._failedTests.length,
      testsRanBeforeBail: this._testsRanBeforeBail,
      suppressedAfterBail: this._suppressedAfterBail
    };

    this.reporters.forEach(reporter => {
      if (typeof reporter.setBailState === 'function') {
        reporter.setBailState(bailState);
      }
    });
  }
}

Reporter.with = (app, stdout, path) => Bluebird.try(() => new Reporter(app, stdout, path)).disposer((reporter, promise) => {
  if (promise.isRejected()) {
    let err = promise.reason();

    if (!err.hideFromReporter) {
      reporter.report(null, {
        passed: false,
        name: err.name || 'unknown error',
        error: {
          message: err.message
        }
      });
    }
  }

  return reporter.close();
});

function forwardToReporters(fn) {
  return function() {
    let args = new Array(arguments.length);
    for (let i = 0; i < args.length; ++i) {
      args[i] = arguments[i];
    }

    this.reporters.forEach(reporter => {
      if (reporter[fn]) {
        reporter[fn].apply(reporter, args);
      }
    });
  };
}

['finish', 'onStart', 'onEnd', 'reportMetadata'].forEach(fn => {
  Reporter.prototype[fn] = forwardToReporters(fn);
});

module.exports = Reporter;
