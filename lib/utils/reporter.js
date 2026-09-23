'use strict';

const Bluebird = require('bluebird');
const EventEmitter = require('events').EventEmitter;
const log = require('npmlog');

const reporters = require('../reporters');
const isa = require('./isa');
const ReportFile = require('./report-file');

function isRealFailure(result) {
  return !!result && !result.skipped && !result.todo && !result.passed;
}

function bailThreshold(value) {
  if (value === false || value === undefined || value === null) {
    return false;
  }
  if (value === true) {
    return 1;
  }
  if (typeof value === 'number' && Number.isInteger(value) && value > 0) {
    return value;
  }

  log.warn('bail_on_test_failure', 'invalid value ' + JSON.stringify(value) + '; defaulting to false');
  return false;
}

function resetSubReporter(reporter) {
  reporter.total = 0;
  reporter.pass = 0;
  reporter.skipped = 0;
  reporter.todo = 0;
  reporter.id = 1;
  reporter.results = [];
  reporter.errors = [];
  reporter.logs = [];
  reporter.bailed = false;
  reporter.bailReason = null;
  reporter.bailCount = 0;
  reporter.testsRanBeforeBail = 0;
  reporter.suppressedAfterBail = 0;
  reporter.stoppedOnError = null;
}

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


class Reporter extends EventEmitter {
  constructor(app, stdout, path) {
    super();

    this.total = 0;
    this.passed = 0;
    this.skipped = 0;
    this.todo = 0;
    this.bailReason = null;
    this._bailed = false;
    this._bailThreshold = false;
    this._failureCount = 0;
    this._testsRanBeforeBail = 0;
    this._suppressedAfterBail = 0;
    this._bailLauncher = null;
    this._failuresByLauncher = {};
    this._failedTests = [];

    if (path) {
      this.reportFile = new ReportFile(path);
    }

    let config = app.config;
    this._bailThreshold = bailThreshold(config.get('bail_on_test_failure'));

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
    return this._bailed;
  }

  getBailReport() {
    let failuresByLauncher = {};
    Object.keys(this._failuresByLauncher).forEach(launcher => {
      failuresByLauncher[launcher] = this._failuresByLauncher[launcher].slice();
    });

    return {
      testsRanBeforeBail: this._bailed ? this._testsRanBeforeBail : 0,
      bailLauncher: this._bailed ? this._bailLauncher : null,
      failuresByLauncher: failuresByLauncher,
      failedTests: this._failedTests.slice()
    };
  }

  resetBailState() {
    this.bailReason = null;
    this._bailed = false;
    this._failureCount = 0;
    this._testsRanBeforeBail = 0;
    this._suppressedAfterBail = 0;
    this._bailLauncher = null;
    this._failuresByLauncher = {};
    this._failedTests = [];
    this.total = 0;
    this.passed = 0;
    this.skipped = 0;
    this.todo = 0;

    this.reporters.forEach(resetSubReporter);
  }

  _publishBailState() {
    this.reporters.forEach(reporter => {
      reporter.bailed = this._bailed;
      reporter.bailReason = this.bailReason;
      reporter.bailCount = this._failureCount;
      reporter.testsRanBeforeBail = this._bailed ? this._testsRanBeforeBail : 0;
      reporter.suppressedAfterBail = this._suppressedAfterBail;
    });
  }

  report(name, result) {
    if (this._bailed) {
      this._suppressedAfterBail++;
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

    if (isRealFailure(result)) {
      this._failureCount++;
      let testName = result.name;
      this._failedTests.push(testName);
      if (!this._failuresByLauncher[name]) {
        this._failuresByLauncher[name] = [];
      }
      this._failuresByLauncher[name].push(testName);

      if (this._bailThreshold && this._failureCount >= this._bailThreshold) {
        this._bailed = true;
        this.bailReason = testName;
        this._bailLauncher = name;
        this._testsRanBeforeBail = this.total;
        this.emit('test-failure', name, result);
      }
    }

    this.reporters.forEach(reporter => {
      reporter.report(name, result);
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

Reporter.prototype.finish = function() {
  this._publishBailState();
  forwardToReporters('finish').call(this);
};

['onStart', 'onEnd', 'reportMetadata'].forEach(fn => {
  Reporter.prototype[fn] = forwardToReporters(fn);
});

module.exports = Reporter;
