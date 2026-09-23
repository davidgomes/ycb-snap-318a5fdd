

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


const INTERNAL_LAUNCHER = 'testem';

class Reporter {
  constructor(app, stdout, path) {
    this.total = 0;
    this.passed = 0;
    this.skipped = 0;
    this.todo = 0;
    this.finished = false;

    this.app = app;
    let config = this.config = app.config;

    this.perLauncher = !!path && ReportFile.hasLauncherTemplate(path);

    let stdoutReporterName;
    if (path && config.get('xunit_intermediate_output') && config.get('reporter') === 'xunit') {
      stdoutReporterName = 'tap';
      this.fileReporterName = config.get('reporter');
    } else {
      stdoutReporterName = config.get('reporter');

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

    this.reporters = [setupReporter(stdoutReporterName, stdout, config, app)];

    if (this.perLauncher) {
      this.reportFileTemplate = path;
      this.reportDate = new Date();
      this.launcherFiles = new Map();
      this.launcherFilesByPath = new Map();
    } else if (path) {
      this.reportFile = new ReportFile(path);
      this.reporters.push(setupReporter(this.fileReporterName, this.reportFile.outputStream, config, app));
    }
  }

  getLauncherFile(launcher) {
    if (!this.perLauncher || this.finished || launcher === INTERNAL_LAUNCHER || launcher === undefined || launcher === null) {
      return undefined;
    }

    let entry = this.launcherFiles.get(launcher);
    if (entry) {
      return entry;
    }

    let filePath = ReportFile.expandPath(this.reportFileTemplate, { launcher: launcher, date: this.reportDate });
    entry = this.launcherFilesByPath.get(filePath);

    if (!entry) {
      let reportFile = new ReportFile(this.reportFileTemplate, { launcher: launcher, date: this.reportDate });
      let reporter = setupReporter(this.fileReporterName, reportFile.outputStream, this.config, this.app);
      if (typeof reporter.setLauncherName === 'function') {
        reporter.setLauncherName(launcher);
      }
      entry = { launcher: launcher, reportFile: reportFile, reporter: reporter };
      this.launcherFilesByPath.set(filePath, entry);
    }

    this.launcherFiles.set(launcher, entry);
    return entry;
  }

  getReportFiles() {
    if (this.perLauncher) {
      return Array.from(this.launcherFilesByPath.values()).map(entry => entry.reportFile);
    }

    return this.reportFile ? [this.reportFile] : [];
  }

  reportersFor(launcher) {
    let entry = this.getLauncherFile(launcher);
    return entry ? this.reporters.concat(entry.reporter) : this.reporters;
  }

  allReporters() {
    if (!this.perLauncher) {
      return this.reporters;
    }

    return this.reporters.concat(Array.from(this.launcherFilesByPath.values()).map(entry => entry.reporter));
  }

  testStarted(name, data) {
    this.reportersFor(name).forEach(reporter => {
      if (reporter.testStarted) {
        reporter.testStarted(name, data);
      }
    });
  }

  onStart(name, data) {
    this.reportersFor(name).forEach(reporter => {
      if (reporter.onStart) {
        reporter.onStart(name, data);
      }
    });
  }

  onEnd(name, data) {
    this.reportersFor(name).forEach(reporter => {
      if (reporter.onEnd) {
        reporter.onEnd(name, data);
      }
    });
  }

  reportMetadata(tag, metadata) {
    this.reporters.forEach(reporter => {
      if (reporter.reportMetadata) {
        reporter.reportMetadata(tag, metadata);
      }
    });
  }

  finish() {
    if (this.finished) {
      return;
    }

    let reporters = this.allReporters();
    this.finished = true;

    reporters.forEach(reporter => {
      if (reporter.finish) {
        reporter.finish();
      }
    });
  }

  close() {
    this.finish();

    if (this.perLauncher) {
      return Bluebird.all(this.getReportFiles().map(reportFile => reportFile.close()));
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

module.exports = Reporter;
