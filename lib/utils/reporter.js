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


function normalizeBailThreshold(value) {
  if (value === false || value === undefined || value === null) {
    return 0;
  }
  if (value === true) {
    return 1;
  }
  if (typeof value === 'number' && Number.isInteger(value) && value > 0) {
    return value;
  }

  log.warn('bail_on_test_failure', 'invalid value %j, defaulting to false', value);
  return 0;
}

function isCountedFailure(result) {
  return !!(result && !result.skipped && !result.todo && !result.passed);
}

function failureName(result) {
  if (!result || result.name === undefined || result.name === null || result.name === '') {
    return 'unknown';
  }
  return String(result.name);
}

class Reporter extends EventEmitter {
  constructor(app, stdout, path) {
    super();

    this.total = 0;
    this.passed = 0;
    this.skipped = 0;
    this.todo = 0;
    this.bailReason = null;
    this.bailLauncher = null;
    this._bailed = false;
    this._testsRanBeforeBail = 0;
    this._suppressedAfterBail = 0;
    this._failureCount = 0;
    this._failedTests = [];
    this._failuresByLauncher = {};

    let config = app && app.config;
    let bailSetting = false;
    if (config && typeof config.get === 'function') {
      bailSetting = config.get('bail_on_test_failure');
    }
    this._bailThreshold = normalizeBailThreshold(bailSetting);

    if (path) {
      this.reportFile = new ReportFile(path);
    }

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
    return this._bailed === true;
  }

  getBailReport() {
    let failuresByLauncher = {};
    Object.keys(this._failuresByLauncher).forEach(launcher => {
      failuresByLauncher[launcher] = this._failuresByLauncher[launcher];
    });

    return {
      testsRanBeforeBail: this._testsRanBeforeBail,
      bailLauncher: this._bailed ? this.bailLauncher : null,
      failuresByLauncher: failuresByLauncher,
      failedTests: this._failedTests.slice()
    };
  }

  resetBailState() {
    this._bailed = false;
    this.bailReason = null;
    this.bailLauncher = null;
    this._testsRanBeforeBail = 0;
    this._suppressedAfterBail = 0;
    this._failureCount = 0;
    this._failedTests = [];
    this._failuresByLauncher = {};
    this.total = 0;
    this.passed = 0;
    this.skipped = 0;
    this.todo = 0;

    if (!this.reporters) {
      return;
    }

    this.reporters.forEach(reporter => {
      reporter.bailInfo = null;
      if (typeof reporter.resetBailState === 'function') {
        reporter.resetBailState();
      }
    });
  }

  finish() {
    if (this.hasBailed()) {
      this._publishBailInfo();
    }

    this.reporters.forEach(reporter => {
      if (reporter.finish) {
        reporter.finish();
      }
    });
  }

  _publishBailInfo() {
    let info = {
      reason: this.bailReason,
      count: this._failureCount,
      testsRanBeforeBail: this._testsRanBeforeBail,
      suppressedAfterBail: this._suppressedAfterBail
    };

    this.reporters.forEach(reporter => {
      reporter.bailInfo = info;
    });
  }

  _recordFailure(name, result) {
    if (this._bailThreshold <= 0) {
      return false;
    }

    let testName = failureName(result);
    let launcherKey = name === undefined || name === null ? 'unknown' : String(name);

    this._failureCount++;
    this._failedTests.push(testName);
    this._failuresByLauncher[launcherKey] = (this._failuresByLauncher[launcherKey] || 0) + 1;

    if (this._failureCount >= this._bailThreshold) {
      this._bailed = true;
      this.bailReason = testName;
      this.bailLauncher = name === undefined || name === null ? null : name;
      this._testsRanBeforeBail = this.total;
      this._publishBailInfo();
      return true;
    }

    return false;
  }

  report(name, result) {
    if (this.hasBailed()) {
      this._suppressedAfterBail++;
      this._publishBailInfo();
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

    let bailedNow = false;
    if (isCountedFailure(result)) {
      bailedNow = this._recordFailure(name, result);
    }

    this.reporters.forEach(reporter => {
      reporter.report(name, result);
    });

    if (bailedNow) {
      this.emit('test-failure', name, result);
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

['onStart', 'onEnd', 'reportMetadata'].forEach(fn => {
  Reporter.prototype[fn] = forwardToReporters(fn);
});

module.exports = Reporter;
