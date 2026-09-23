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

function isFailure(result) {
  if (!result || result.skipped || result.todo) {
    return false;
  }
  return !result.passed;
}

function testName(result) {
  if (!result || result.name === null || result.name === undefined) {
    return '';
  }
  return String(result.name);
}

function launcherKey(name) {
  if (name === null || name === undefined) {
    return '';
  }
  return String(name);
}

// false disables bail. true bails on the first failure. A positive integer
// bails on the Nth failure. Anything else warns and disables bail.
function resolveBailThreshold(value) {
  if (value === false || value === null || value === undefined) {
    return 0;
  }
  if (value === true) {
    return 1;
  }
  if (typeof value === 'number' && Number.isInteger(value) && value > 0) {
    return value;
  }

  log.warn('bail_on_test_failure', 'invalid value ' + JSON.stringify(value) + '; defaulting to false');
  return 0;
}

function resetSubReporter(reporter) {
  reporter.bailed = false;
  reporter.bailReason = null;
  reporter.bailLauncher = null;
  reporter.bailedTests = 0;
  reporter.testsRanBeforeBail = 0;
  reporter.suppressedAfterBail = 0;
  reporter.total = 0;
  reporter.pass = 0;
  reporter.passed = 0;
  reporter.skipped = 0;
  reporter.todo = 0;
  reporter.id = 1;
  reporter.currentLineChars = 0;
  reporter.endTime = null;
  if (reporter.startTime instanceof Date) {
    reporter.startTime = new Date();
  }
  if (Array.isArray(reporter.results)) {
    reporter.results.length = 0;
  }
  if (Array.isArray(reporter.errors)) {
    reporter.errors.length = 0;
  }
  if (Array.isArray(reporter.logs)) {
    reporter.logs.length = 0;
  }
}

class Reporter extends EventEmitter {
  constructor(app, stdout, path) {
    super();

    this.app = app;
    this.total = 0;
    this.passed = 0;
    this.skipped = 0;
    this.todo = 0;
    this.bailed = false;
    this.bailReason = null;
    this.bailLauncher = null;
    this.bailThreshold = 0;
    this.failureCount = 0;
    this.testsRanBeforeBail = 0;
    this.suppressedAfterBail = 0;
    this.failuresByLauncher = {};
    this.failedTests = [];

    if (path) {
      this.reportFile = new ReportFile(path);
    }

    let config = app.config;
    let bailValue = false;
    if (config && typeof config.get === 'function') {
      bailValue = config.get('bail_on_test_failure');
    }
    this.bailThreshold = resolveBailThreshold(bailValue);

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

  hasBailed() {
    return this.bailed === true;
  }

  getBailReport() {
    let failuresByLauncher = {};
    Object.keys(this.failuresByLauncher).forEach(key => {
      failuresByLauncher[key] = this.failuresByLauncher[key];
    });

    return {
      testsRanBeforeBail: this.testsRanBeforeBail,
      bailLauncher: this.bailed ? this.bailLauncher : null,
      failuresByLauncher: failuresByLauncher,
      failedTests: this.failedTests.slice()
    };
  }

  resetBailState() {
    this.bailed = false;
    this.bailReason = null;
    this.bailLauncher = null;
    this.failureCount = 0;
    this.testsRanBeforeBail = 0;
    this.suppressedAfterBail = 0;
    this.failuresByLauncher = {};
    this.failedTests = [];
    this.total = 0;
    this.passed = 0;
    this.skipped = 0;
    this.todo = 0;

    if (this.reporters) {
      this.reporters.forEach(resetSubReporter);
    }
  }

  _applyBailToSubReporters() {
    let bailedTests = this.failureCount;
    this.reporters.forEach(reporter => {
      reporter.bailed = this.bailed;
      reporter.bailReason = this.bailReason;
      reporter.bailLauncher = this.bailed ? this.bailLauncher : null;
      reporter.bailedTests = bailedTests;
      reporter.testsRanBeforeBail = this.testsRanBeforeBail;
      reporter.suppressedAfterBail = this.suppressedAfterBail;
    });
  }

  _forwardResult(name, result) {
    this.reporters.forEach(reporter => {
      reporter.report(name, result);
    });
  }

  report(name, result) {
    if (this.bailed) {
      this.suppressedAfterBail++;
      this._applyBailToSubReporters();
      return;
    }

    this.total++;
    this.testsRanBeforeBail = this.total;
    if (result.skipped) {
      this.skipped++;
    } else if (result.passed && !result.todo) {
      this.passed++;
    } else if (!result.passed && result.todo) {
      this.todo++;
    }

    let failure = isFailure(result);
    if (failure) {
      let nameText = testName(result);
      let key = launcherKey(name);
      this.failureCount++;
      this.failedTests.push(nameText);
      this.failuresByLauncher[key] = (this.failuresByLauncher[key] || 0) + 1;
    }

    this._forwardResult(name, result);

    if (failure && this.bailThreshold > 0 && this.failureCount >= this.bailThreshold) {
      this.bailed = true;
      this.bailReason = testName(result);
      this.bailLauncher = (name === null || name === undefined) ? null : name;
      this.testsRanBeforeBail = this.total;
      this._applyBailToSubReporters();
      this.emit('test-failure', name, result);
      if (this.app && typeof this.app.abortRunners === 'function') {
        this.app.abortRunners();
      }
    }
  }

  finish() {
    this._applyBailToSubReporters();

    this.reporters.forEach(reporter => {
      if (reporter.finish) {
        reporter.finish();
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

['onStart', 'onEnd', 'reportMetadata'].forEach(fn => {
  Reporter.prototype[fn] = forwardToReporters(fn);
});

module.exports = Reporter;
