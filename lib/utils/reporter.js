

const Bluebird = require('bluebird');
const log = require('npmlog');

const reporters = require('../reporters');
const isa = require('./isa');
const ReportFile = require('./report-file');

// App-level results (hooks, run errors) use this name; they go to stdout only.
const INTERNAL_LAUNCHER_NAME = 'testem';

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
    this.finished = false;

    let config = app.config;
    let stdoutReporterName = config.get('reporter');
    let fileReporterName = config.get('reporter');

    if (path && config.get('xunit_intermediate_output') && config.get('reporter') === 'xunit') {
      stdoutReporterName = 'tap';
    } else if (path && config.appMode === 'dev') {
      fileReporterName = config.get('dev_mode_file_reporter');
      if (!fileReporterName) {
        log.warn('You configured a `report_file`, you may want to configure the `dev_mode_file_reporter` as well. Using the `tap` logger now.');
        fileReporterName = 'tap';
      }
    }

    this.reporters = [setupReporter(stdoutReporterName, stdout, config, app)];

    if (path && ReportFile.hasLauncherTemplate(path)) {
      this.app = app;
      this.fileReporterName = fileReporterName;
      this.reportFilePath = path;
      this.reportDate = new Date();
      this.launcherReports = new Map();
    } else if (path) {
      this.reportFile = new ReportFile(path);
      this.reporters.push(setupReporter(fileReporterName, this.reportFile.outputStream, config, app));
    }
  }

  // Lazily creates the file reporter for a launcher. Keyed by the expanded
  // path so launcher names that sanitize identically share one file.
  launcherReporter(name) {
    if (!this.launcherReports || name === INTERNAL_LAUNCHER_NAME) {
      return;
    }

    let options = { launcher: name, date: this.reportDate };
    let filePath = ReportFile.expandPath(this.reportFilePath, options);
    let launcherReport = this.launcherReports.get(filePath);

    if (!launcherReport) {
      let reportFile = new ReportFile(this.reportFilePath, options);
      let reporter = setupReporter(this.fileReporterName, reportFile.outputStream, this.app.config, this.app);
      if (typeof reporter.setLauncherName === 'function') {
        reporter.setLauncherName(name);
      }

      launcherReport = { reportFile: reportFile, reporter: reporter };
      this.launcherReports.set(filePath, launcherReport);
    }

    return launcherReport.reporter;
  }

  reportersFor(name) {
    let launcherReporter = this.launcherReporter(name);

    return launcherReporter ? this.reporters.concat(launcherReporter) : this.reporters;
  }

  allReporters() {
    if (!this.launcherReports) {
      return this.reporters;
    }

    return this.reporters.concat(Array.from(this.launcherReports.values(), launcherReport => launcherReport.reporter));
  }

  testStarted(name, data) {
    this.reportersFor(name).forEach(reporter => {
      if (reporter.testStarted) {
        reporter.testStarted(name, data);
      }
    });
  }

  finish() {
    if (this.finished) {
      return;
    }
    this.finished = true;

    this.allReporters().forEach(reporter => {
      if (reporter.finish) {
        reporter.finish();
      }
    });
  }

  close() {
    this.finish();

    if (this.launcherReports) {
      return Bluebird.all(Array.from(this.launcherReports.values(), launcherReport => launcherReport.reportFile.close()));
    }

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
    this.total++;
    if (result.skipped) {
      this.skipped++;
    } else if (result.passed && !result.todo) {
      this.passed++;
    } else if (!result.passed && result.todo) {
      this.todo++;
    }

    this.reportersFor(name).forEach(reporter => {
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

function forwardToReporters(fn, selectReporters) {
  return function() {
    let args = new Array(arguments.length);
    for (let i = 0; i < args.length; ++i) {
      args[i] = arguments[i];
    }

    selectReporters.apply(this, args).forEach(reporter => {
      if (reporter[fn]) {
        reporter[fn].apply(reporter, args);
      }
    });
  };
}

['onStart', 'onEnd'].forEach(fn => {
  Reporter.prototype[fn] = forwardToReporters(fn, Reporter.prototype.reportersFor);
});

// Metadata events don't identify their launcher, so every file receives them.
Reporter.prototype.reportMetadata = forwardToReporters('reportMetadata', Reporter.prototype.allReporters);

module.exports = Reporter;
