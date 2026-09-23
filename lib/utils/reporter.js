

const Bluebird = require('bluebird');
const log = require('npmlog');

const reporters = require('../reporters');
const isa = require('./isa');
const ReportFile = require('./report-file');

const INTERNAL_LAUNCHER = 'testem';

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
    this.app = app;
    this.config = config;

    this.perLauncher = !!path && ReportFile.hasLauncherTemplate(path);

    if (this.perLauncher) {
      this.reportFilePath = path;
      this.reportDate = new Date();
      this.launcherReportFiles = new Map();
      this.launcherReporters = new Map();
    } else if (path) {
      this.reportFile = new ReportFile(path);
    }

    let fileStream = this.reportFile && this.reportFile.outputStream;

    if (path && config.get('xunit_intermediate_output') && config.get('reporter') === 'xunit') {
      this.fileReporterName = config.get('reporter');
      this.reporters = [setupReporter('tap', stdout, config, app)];
      if (fileStream) {
        this.reporters.push(setupReporter(this.fileReporterName, fileStream, config, app));
      }
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

        if (fileStream) {
          this.reporters.push(setupReporter(this.fileReporterName, fileStream, config, app));
        }
      }
    }
  }

  launcherReporterFor(launcher, create) {
    if (!this.perLauncher || !launcher || launcher === INTERNAL_LAUNCHER) {
      return;
    }

    let reporter = this.launcherReporters.get(launcher);
    if (!reporter && create && !this.finished) {
      let reportFile = new ReportFile(this.reportFilePath, {
        launcher,
        date: this.reportDate
      });
      reporter = setupReporter(this.fileReporterName, reportFile.outputStream, this.config, this.app);
      if (typeof reporter.setLauncherName === 'function') {
        reporter.setLauncherName(launcher);
      }
      this.launcherReportFiles.set(launcher, reportFile);
      this.launcherReporters.set(launcher, reporter);
    }

    return reporter;
  }

  forward(fn, launcher, args, createLauncherReporter) {
    this.reporters.forEach(reporter => {
      if (reporter[fn]) {
        reporter[fn].apply(reporter, args);
      }
    });

    let launcherReporter = this.launcherReporterFor(launcher, createLauncherReporter);
    if (launcherReporter && launcherReporter[fn]) {
      launcherReporter[fn].apply(launcherReporter, args);
    }
  }

  testStarted(name, data) {
    this.forward('testStarted', name, [name, data], true);
  }

  onStart(name, data) {
    this.forward('onStart', name, [name, data], true);
  }

  onEnd(name, data) {
    this.forward('onEnd', name, [name, data], false);
  }

  reportMetadata(tag, metadata) {
    this.forward('reportMetadata', null, [tag, metadata], false);
  }

  finish() {
    if (this.finished) {
      return;
    }
    this.finished = true;

    this.forward('finish', null, [], false);

    if (this.perLauncher) {
      this.launcherReporters.forEach(reporter => {
        if (reporter.finish) {
          reporter.finish();
        }
      });
    }
  }

  close() {
    this.finish();

    if (this.perLauncher) {
      let closing = [];
      this.launcherReportFiles.forEach(reportFile => closing.push(reportFile.close()));
      return Bluebird.all(closing);
    }

    if (this.reportFile) {
      return this.reportFile.close();
    }
  }

  getReportFilePaths() {
    if (this.perLauncher) {
      return Array.from(this.launcherReportFiles.values()).map(reportFile => reportFile.getFilePath());
    }

    return this.reportFile ? [this.reportFile.getFilePath()] : [];
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

    this.forward('report', name, [name, result], true);
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

module.exports = Reporter;
