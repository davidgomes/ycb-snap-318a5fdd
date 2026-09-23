

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


class Reporter {
  constructor(app, stdout, path) {
    this.total = 0;
    this.passed = 0;
    this.skipped = 0;
    this.todo = 0;
    this.app = app;
    this.reportFilePath = path;
    this.launcherReportFiles = new Map();
    this.launcherReporters = new Map();
    this.perLauncher = !!(path && ReportFile.hasLauncherTemplate(path));
    this._finished = false;

    if (path && !this.perLauncher) {
      this.reportFile = new ReportFile(path);
    }

    let config = app.config;

    if (path && !this.perLauncher && config.get('xunit_intermediate_output') && config.get('reporter') === 'xunit') {
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
          if (!this.perLauncher) {
            this.reporters.push(setupReporter(devModeFileReporter, this.reportFile.outputStream, config, app));
          }
        } else if (!this.perLauncher) {
          this.reporters.push(setupReporter(config.get('reporter'), this.reportFile.outputStream, config, app));
        }
      }
    }
  }

  fileReporterName() {
    let config = this.app.config;
    if (config.appMode === 'dev') {
      return config.get('dev_mode_file_reporter') || 'tap';
    }
    return config.get('reporter');
  }

  getLauncherFileReporter(launcherName) {
    let key = ReportFile.sanitizeLauncherName(launcherName);
    if (this.launcherReporters.has(key)) {
      return this.launcherReporters.get(key);
    }

    let reportFile = new ReportFile(this.reportFilePath, { launcher: launcherName });
    let reporter = setupReporter(this.fileReporterName(), reportFile.outputStream, this.app.config, this.app);
    if (reporter.setLauncherName) {
      reporter.setLauncherName(launcherName);
    }
    this.launcherReportFiles.set(key, reportFile);
    this.launcherReporters.set(key, reporter);
    return reporter;
  }

  eachLauncherReporter(fn) {
    this.launcherReporters.forEach(fn);
  }

  testStarted(name, data) {
    this.reporters.forEach(reporter => {
      if (reporter.testStarted) {
        reporter.testStarted(name, data);
      }
    });
    if (this.perLauncher && name && name !== 'testem') {
      let reporter = this.getLauncherFileReporter(name);
      if (reporter.testStarted) {
        reporter.testStarted(name, data);
      }
    }
  }

  close() {
    this.finish();

    let closes = [];
    if (this.reportFile) {
      closes.push(this.reportFile.close());
    }
    this.launcherReportFiles.forEach(reportFile => {
      closes.push(reportFile.close());
    });
    if (closes.length) {
      return Bluebird.all(closes);
    }
  }

  hasTests() {
    return this.total > 0;
  }

  hasPassed() {
    return this.total <= ((this.passed || 0) + (this.skipped || 0) + (this.todo || 0));
  }

  report(name, result) {
    this.total++;
    if (result.skipped) {
      this.skipped++;
    } else if (result.passed && !result.todo) {
      this.passed++;
    } else if (!result.passed && result.todo) {
      this.todo++;
    }

    this.reporters.forEach(reporter => {
      reporter.report(name, result);
    });

    if (this.perLauncher && name && name !== 'testem') {
      this.getLauncherFileReporter(name).report(name, result);
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

    if (this.perLauncher && fn !== 'finish' && args.length && args[0] && args[0] !== 'testem') {
      let reporter = this.getLauncherFileReporter(args[0]);
      if (reporter[fn]) {
        reporter[fn].apply(reporter, args);
      }
    }
  };
}

['onStart', 'onEnd', 'reportMetadata'].forEach(fn => {
  Reporter.prototype[fn] = forwardToReporters(fn);
});

Reporter.prototype.finish = function() {
  if (this._finished) {
    return;
  }
  this._finished = true;
  forwardToReporters('finish').call(this);
  this.eachLauncherReporter(reporter => {
    if (reporter.finish) {
      reporter.finish();
    }
  });
};

module.exports = Reporter;
