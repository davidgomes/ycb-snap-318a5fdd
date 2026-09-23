

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
    this.finished = false;
    this.app = app;
    this.reportPath = path;
    this.reportDate = new Date();
    this.perLauncher = !!path && ReportFile.hasLauncherTemplate(path);
    this.launcherFiles = {};

    let config = app.config;
    this.config = config;

    if (path && !this.perLauncher) {
      this.reportFile = new ReportFile(path, { date: this.reportDate });
    }

    if (path && config.get('xunit_intermediate_output') && config.get('reporter') === 'xunit') {
      this.fileReporterName = config.get('reporter');
      this.reporters = [setupReporter('tap', stdout, config, app)];
    } else {
      this.reporters = [setupReporter(config.get('reporter'), stdout, config, app)];

      if (path) {
        if (config.appMode === 'dev') {
          let devModeFileReporter = config.get('dev_mode_file_reporter');
          if (!devModeFileReporter) {
            log.warn('You configured a `report_file`, you may want to configure the `dev_mode_file_reporter` as well. Using the `tap` logger now.');
            devModeFileReporter = 'tap';
          }
          this.fileReporterName = devModeFileReporter;
        } else {
          this.fileReporterName = config.get('reporter');
        }
      }
    }

    if (this.reportFile) {
      this.reporters.push(setupReporter(this.fileReporterName, this.reportFile.outputStream, config, app));
    }
  }

  launcherReporter(launcherName) {
    if (!this.perLauncher || launcherName === 'testem' || this.finished) {
      return null;
    }
    const key = ReportFile.sanitizeLauncherName(launcherName);
    let entry = this.launcherFiles[key];
    if (!entry) {
      const file = new ReportFile(this.reportPath, { launcher: launcherName, date: this.reportDate });
      const reporter = setupReporter(this.fileReporterName, file.outputStream, this.config, this.app);
      if (reporter.setLauncherName) {
        reporter.setLauncherName(launcherName);
      }
      entry = this.launcherFiles[key] = { file, reporter };
    }
    return entry.reporter;
  }

  allReporters() {
    return this.reporters.concat(Object.keys(this.launcherFiles).map(k => this.launcherFiles[k].reporter));
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

    const files = Object.keys(this.launcherFiles).map(k => this.launcherFiles[k].file);
    if (this.reportFile) {
      files.push(this.reportFile);
    }
    return Bluebird.all(files.map(file => file.close()));
  }

  testStarted(name, data) {
    this.reporters.forEach(reporter => {
      if (reporter.testStarted) {
        reporter.testStarted(name, data);
      }
    });
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

    const launcherReporter = this.launcherReporter(name);
    if (launcherReporter) {
      launcherReporter.report(name, result);
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

    this.allReporters().forEach(reporter => {
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
