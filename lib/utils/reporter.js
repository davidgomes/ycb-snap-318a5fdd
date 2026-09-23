'use strict';

const EventEmitter = require('events').EventEmitter;
const util = require('util');
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

function parseBailThreshold(value) {
  if (value === undefined || value === null || value === false) {
    return false;
  }
  if (value === true) {
    return 1;
  }
  if (typeof value === 'number' && Number.isInteger(value) && value > 0) {
    return value;
  }

  log.warn('bail_on_test_failure', `Invalid value ${util.inspect(value)}, expected true, false or a positive integer. Defaulting to false.`);
  return false;
}

function isBailableFailure(result) {
  return !result.passed && !result.skipped && !result.todo;
}

// Sub-reporters keep their own tallies; zero the ones the built-in reporters
// share so their finish output only covers results reported after a reset.
function resetSubReporterState(reporter) {
  ['total', 'pass', 'skipped', 'todo'].forEach(key => {
    if (typeof reporter[key] === 'number') {
      reporter[key] = 0;
    }
  });
  ['results', 'errors', 'logs'].forEach(key => {
    if (Array.isArray(reporter[key])) {
      reporter[key] = [];
    }
  });
  if (typeof reporter.id === 'number') {
    reporter.id = 1;
  }
  if (reporter.startTime instanceof Date) {
    reporter.startTime = new Date();
  }
  if ('bailInfo' in reporter) {
    reporter.bailInfo = null;
  }
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

    this.bailThreshold = parseBailThreshold(config.get('bail_on_test_failure'));
    this.clearBailState();

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

  finish() {
    let bailInfo = this.hasBailed() ? this.getBailInfo() : null;

    this.reporters.forEach(reporter => {
      if (bailInfo && reporter.reportBail) {
        reporter.reportBail(bailInfo);
      }
      if (reporter.finish) {
        reporter.finish();
      }
    });
  }

  hasTests() {
    return this.total > 0;
  }

  hasPassed() {
    return this.total <= ((this.passed || 0) + (this.skipped || 0) + (this.todo || 0));
  }

  hasBailed() {
    return this.bailed;
  }

  getBailReport() {
    return {
      testsRanBeforeBail: this.testsRanBeforeBail,
      bailLauncher: this.bailLauncher,
      failuresByLauncher: Object.assign({}, this.failuresByLauncher),
      failedTests: this.failedTests.slice()
    };
  }

  getBailInfo() {
    return {
      reason: this.bailReason,
      launcher: this.bailLauncher,
      failureCount: this.failedTests.length,
      testsRanBeforeBail: this.testsRanBeforeBail,
      suppressedAfterBail: this.suppressedAfterBail
    };
  }

  clearBailState() {
    this.bailed = false;
    this.bailReason = null;
    this.bailLauncher = null;
    this.testsRanBeforeBail = 0;
    this.suppressedAfterBail = 0;
    this.failuresByLauncher = {};
    this.failedTests = [];
  }

  resetBailState() {
    this.total = 0;
    this.passed = 0;
    this.skipped = 0;
    this.todo = 0;
    this.clearBailState();
    this.reporters.forEach(resetSubReporterState);
  }

  report(name, result) {
    if (this.bailed) {
      this.suppressedAfterBail++;
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

    let bailing = false;
    if (isBailableFailure(result)) {
      bailing = this.recordFailure(name, result);
    }

    this.reporters.forEach(reporter => {
      reporter.report(name, result);
    });

    if (bailing) {
      this.emit('test-failure', name, result);
    }
  }

  recordFailure(name, result) {
    this.failedTests.push(result.name);
    let launcherFailures = Object.prototype.hasOwnProperty.call(this.failuresByLauncher, name) ? this.failuresByLauncher[name] : 0;
    this.failuresByLauncher[name] = launcherFailures + 1;

    if (!this.bailThreshold || this.failedTests.length < this.bailThreshold) {
      return false;
    }

    this.bailed = true;
    this.bailReason = result.name;
    this.bailLauncher = name;
    this.testsRanBeforeBail = this.total;
    return true;
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
