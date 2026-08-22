'use strict';

const Bluebird = require('bluebird');
const EventEmitter = require('events').EventEmitter;
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

function isCountableFailure(result) {
  return !!(result && !result.skipped && !result.todo && !result.passed);
}

function testName(result) {
  return typeof result.name === 'string' ? result.name : String(result.name);
}

function parseBailThreshold(value) {
  if (value === undefined || value === false) {
    return 0;
  }
  if (value === true) {
    return 1;
  }
  if (typeof value === 'number' && Number.isInteger(value) && value > 0) {
    return value;
  }

  log.warn('bail_on_test_failure', 'Invalid value `' + value + '`; defaulting to false.');
  return 0;
}

class Reporter extends EventEmitter {
  constructor(app, stdout, path) {
    super();

    this.app = app;
    this.total = 0;
    this.passed = 0;
    this.skipped = 0;
    this.todo = 0;

    this._initBailState(app && app.config);

    if (path) {
      this.reportFile = new ReportFile(path);
    }

    let config = app.config;

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

  _initBailState(config) {
    let value;
    if (config && typeof config.get === 'function') {
      value = config.get('bail_on_test_failure');
    }

    this.bailThreshold = parseBailThreshold(value);
    this.bailed = false;
    this.bailReason = null;
    this.bailLauncher = null;
    this.testsRanBeforeBail = 0;
    this.suppressedAfterBail = 0;
    this.failedTests = [];
    this.failuresByLauncher = {};
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

  hasBailed() {
    return !!this.bailed;
  }

  getBailReport() {
    return {
      testsRanBeforeBail: this.testsRanBeforeBail,
      bailLauncher: this.bailLauncher,
      failuresByLauncher: this.failuresByLauncher,
      failedTests: this.failedTests.slice()
    };
  }

  resetBailState() {
    this.bailed = false;
    this.bailReason = null;
    this.bailLauncher = null;
    this.testsRanBeforeBail = 0;
    this.suppressedAfterBail = 0;
    this.failedTests = [];
    this.failuresByLauncher = {};
    this.total = 0;
    this.passed = 0;
    this.skipped = 0;
    this.todo = 0;

    if (this.reporters) {
      this.reporters.forEach(reporter => {
        if (typeof reporter.reset === 'function') {
          reporter.reset();
        }
      });
    }
  }

  _notifySubReportersBail() {
    let report = {
      bailReason: this.bailReason,
      testsRanBeforeBail: this.testsRanBeforeBail,
      suppressedAfterBail: this.suppressedAfterBail,
      failedCount: this.failedTests.length
    };

    if (this.reporters) {
      this.reporters.forEach(reporter => {
        if (typeof reporter.onBail === 'function') {
          reporter.onBail(report);
        }
      });
    }
  }

  _syncSuppressed() {
    if (this.reporters) {
      this.reporters.forEach(reporter => {
        if (reporter) {
          reporter.suppressedAfterBail = this.suppressedAfterBail;
        }
      });
    }
  }

  report(name, result) {
    if (this.bailed) {
      this.suppressedAfterBail++;
      this._syncSuppressed();
      return;
    }

    this.total++;
    if (result.skipped) {
      this.skipped++;
    } else if (result.passed && !result.todo) {
      this.passed++;
    } else if (!result.passed && result.todo) {
      this.todo++;
    }

    if (isCountableFailure(result)) {
      this.failedTests.push(testName(result));
      if (!Object.prototype.hasOwnProperty.call(this.failuresByLauncher, name)) {
        this.failuresByLauncher[name] = 0;
      }
      this.failuresByLauncher[name]++;
    }

    this.reporters.forEach(reporter => {
      reporter.report(name, result);
    });

    if (this.bailThreshold && isCountableFailure(result) && this.failedTests.length >= this.bailThreshold) {
      this.bailed = true;
      this.bailReason = testName(result);
      this.bailLauncher = name;
      this.testsRanBeforeBail = this.total;
      this._notifySubReportersBail();
      this.emit('test-failure', name, result);
      if (this.listenerCount('test-failure') === 0 && this.app && typeof this.app.abortRunners === 'function') {
        this.app.abortRunners();
      }
    }
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
