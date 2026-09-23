'use strict';

const Bluebird = require('bluebird');
const EventEmitter = require('events').EventEmitter;
const log = require('npmlog');

const reporters = require('../reporters');
const isa = require('./isa');
const ReportFile = require('./report-file');

function isPositiveInteger(value) {
  return typeof value === 'number' && isFinite(value) && Math.floor(value) === value && value > 0;
}

function resolveBailThreshold(value) {
  if (value === true) {
    return 1;
  }
  if (value === false || typeof value === 'undefined' || value === null) {
    return 0;
  }
  if (isPositiveInteger(value)) {
    return value;
  }
  log.warn('bail_on_test_failure', 'invalid value ' + JSON.stringify(value) + '; defaulting to false');
  return 0;
}

function isBailFailure(result) {
  if (!result || result.skipped || result.todo) {
    return false;
  }
  return !result.passed;
}

function resetSubReporter(reporter) {
  if (!reporter) {
    return;
  }
  if (typeof reporter.resetBailState === 'function') {
    reporter.resetBailState();
  }
  reporter.bailed = false;
  reporter.bailReason = null;
  reporter.testsRanBeforeBail = 0;
  reporter.suppressedAfterBail = 0;
  reporter.bailedTests = 0;
  if (Array.isArray(reporter.results)) {
    reporter.results = [];
  }
  if (Array.isArray(reporter.errors)) {
    reporter.errors = [];
  }
  if (Array.isArray(reporter.logs)) {
    reporter.logs = [];
  }
  ['total', 'pass', 'passed', 'skipped', 'todo'].forEach(key => {
    if (typeof reporter[key] === 'number') {
      reporter[key] = 0;
    }
  });
  if (typeof reporter.id === 'number') {
    reporter.id = 1;
  }
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
    this.bailed = false;
    this.bailReason = null;
    this.bailLauncher = null;
    this.bailThreshold = 0;
    this.testsRanBeforeBail = 0;
    this.suppressedAfterBail = 0;
    this.failedTests = [];
    this.failuresByLauncher = {};
    this.app = app;

    let config = app.config;
    this.bailThreshold = resolveBailThreshold(config && typeof config.get === 'function' ? config.get('bail_on_test_failure') : false);

    this.on('test-failure', () => {
      if (this.app && typeof this.app.abortRunners === 'function') {
        this.app.abortRunners();
      }
    });

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
    return this.bailed === true;
  }

  getBailReport() {
    return {
      testsRanBeforeBail: this.testsRanBeforeBail,
      bailLauncher: this.bailLauncher,
      failuresByLauncher: Object.assign({}, this.failuresByLauncher),
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
      this.reporters.forEach(resetSubReporter);
    }
  }

  applyBailToSubReporters() {
    if (!this.hasBailed()) {
      return;
    }
    let report = this.getBailReport();
    this.reporters.forEach(reporter => {
      reporter.bailed = true;
      reporter.bailReason = this.bailReason;
      reporter.testsRanBeforeBail = report.testsRanBeforeBail;
      reporter.suppressedAfterBail = this.suppressedAfterBail;
      reporter.bailedTests = report.failedTests.length;
    });
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

    let didBail = false;
    if (isBailFailure(result)) {
      let testName = result.name === null || result.name === undefined ? '' : String(result.name);
      this.failedTests.push(testName);
      let launcherName = name === null || name === undefined ? '' : String(name);
      this.failuresByLauncher[launcherName] = (this.failuresByLauncher[launcherName] || 0) + 1;
      if (this.bailThreshold > 0 && this.failedTests.length >= this.bailThreshold) {
        this.bailed = true;
        this.bailReason = testName;
        this.bailLauncher = name === null || name === undefined ? null : name;
        this.testsRanBeforeBail = this.total;
        didBail = true;
      }
    }

    this.reporters.forEach(reporter => {
      reporter.report(name, result);
    });

    if (didBail) {
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

Reporter.prototype.finish = function() {
  this.applyBailToSubReporters();
  return forwardToReporters('finish').call(this);
};

module.exports = Reporter;
