

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
  constructor(app, stdout, reportPath) {
    this.total = 0;
    this.passed = 0;
    this.skipped = 0;
    this.todo = 0;
    this.finished = false;
    this.launcherFiles = new Map();
    this.app = app;
    this.stdout = stdout;
    this.reportFilePath = reportPath;

    let config = app.config;
    this.config = config;
    this.usePerLauncherFiles = !!(reportPath && ReportFile.hasLauncherTemplate(reportPath));

    if (reportPath && !this.usePerLauncherFiles) {
      this.reportFile = new ReportFile(reportPath);
    }

    let fileStream = this.reportFile && this.reportFile.outputStream;

    if (reportPath && config.get('xunit_intermediate_output') && config.get('reporter') === 'xunit') {
      this.reporters = [
        setupReporter('tap', stdout, config, app)
      ];
      if (fileStream) {
        this.reporters.push(setupReporter(config.get('reporter'), fileStream, config, app));
      }
    } else {
      this.reporters = [setupReporter(config.get('reporter'), stdout, config, app)];

      if (fileStream) {
        if (config.appMode === 'dev') {
          let devModeFileReporter = config.get('dev_mode_file_reporter');
          if (!devModeFileReporter) {
            log.warn('You configured a `report_file`, you may want to configure the `dev_mode_file_reporter` as well. Using the `tap` logger now.');
            devModeFileReporter = 'tap';
          }
          this.reporters.push(setupReporter(devModeFileReporter, fileStream, config, app));
        } else {
          this.reporters.push(setupReporter(config.get('reporter'), fileStream, config, app));
        }
      } else if (this.usePerLauncherFiles && config.appMode === 'dev' && !config.get('dev_mode_file_reporter')) {
        log.warn('You configured a `report_file`, you may want to configure the `dev_mode_file_reporter` as well. Using the `tap` logger now.');
      }
    }
  }

  _fileReporterName() {
    let config = this.config;
    if (config.get('xunit_intermediate_output') && config.get('reporter') === 'xunit') {
      return config.get('reporter');
    }
    if (config.appMode === 'dev') {
      return config.get('dev_mode_file_reporter') || 'tap';
    }
    return config.get('reporter');
  }

  _ensureLauncherReporter(launcherName) {
    if (!this.usePerLauncherFiles) {
      return null;
    }
    if (launcherName === undefined || launcherName === null || launcherName === '' || launcherName === 'testem') {
      return null;
    }
    if (this.launcherFiles.has(launcherName)) {
      return this.launcherFiles.get(launcherName);
    }

    let reportFile = new ReportFile(this.reportFilePath, { launcher: launcherName });
    let reporter = setupReporter(this._fileReporterName(), reportFile.outputStream, this.config, this.app);
    if (typeof reporter.setLauncherName === 'function') {
      reporter.setLauncherName(launcherName);
    }

    let entry = {
      reportFile: reportFile,
      reporter: reporter
    };
    this.launcherFiles.set(launcherName, entry);
    return entry;
  }

  _forwardToLauncher(fn, args) {
    let entry = this._ensureLauncherReporter(args[0]);
    if (entry && entry.reporter[fn]) {
      entry.reporter[fn].apply(entry.reporter, args);
    }
  }

  testStarted(name, data) {
    this.reporters.forEach(reporter => {
      if (reporter.testStarted) {
        reporter.testStarted(name, data);
      }
    });
    this._forwardToLauncher('testStarted', [name, data]);
  }

  close() {
    this.finish();

    let closers = [];
    if (this.reportFile) {
      closers.push(this.reportFile.close());
    }
    this.launcherFiles.forEach(entry => {
      closers.push(entry.reportFile.close());
    });

    if (closers.length === 0) {
      return;
    }
    if (closers.length === 1) {
      return closers[0];
    }
    return Bluebird.all(closers);
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
    this._forwardToLauncher('report', [name, result]);
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

    if (fn !== 'reportMetadata') {
      this._forwardToLauncher(fn, args);
    }
  };
}

function finishReporters() {
  if (this.finished) {
    return;
  }
  this.finished = true;

  this.reporters.forEach(reporter => {
    if (reporter.finish) {
      reporter.finish();
    }
  });

  this.launcherFiles.forEach(entry => {
    if (entry.reporter.finish) {
      entry.reporter.finish();
    }
  });
}

Reporter.prototype.finish = finishReporters;

['onStart', 'onEnd', 'reportMetadata'].forEach(fn => {
  Reporter.prototype[fn] = forwardToReporters(fn);
});

module.exports = Reporter;
