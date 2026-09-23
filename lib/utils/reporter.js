
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

function fileReporterName(config) {
  if (config.get('xunit_intermediate_output') && config.get('reporter') === 'xunit') {
    return config.get('reporter');
  }

  if (config.appMode === 'dev') {
    let devModeFileReporter = config.get('dev_mode_file_reporter');
    if (!devModeFileReporter) {
      log.warn('You configured a `report_file`, you may want to configure the `dev_mode_file_reporter` as well. Using the `tap` logger now.');
      devModeFileReporter = 'tap';
    }
    return devModeFileReporter;
  }

  return config.get('reporter');
}


class Reporter {
  constructor(app, stdout, reportPath) {
    this.total = 0;
    this.passed = 0;
    this.skipped = 0;
    this.todo = 0;
    this.app = app;
    this.config = app.config;
    this.reportPath = reportPath;
    this.reportDate = new Date();
    this.launcherFiles = new Map();
    this._finished = false;
    this.useLauncherFiles = !!(reportPath && ReportFile.hasLauncherTemplate(reportPath));

    let config = this.config;

    if (reportPath && !this.useLauncherFiles) {
      this.reportFile = new ReportFile(reportPath, { date: this.reportDate });
    }

    if (this.useLauncherFiles && config.get('xunit_intermediate_output') && config.get('reporter') === 'xunit') {
      this.reporters = [setupReporter('tap', stdout, config, app)];
      this._fileReporterName = 'xunit';
    } else if (reportPath && !this.useLauncherFiles && config.get('xunit_intermediate_output') && config.get('reporter') === 'xunit') {
      this.reporters = [
        setupReporter('tap', stdout, config, app),
        setupReporter(config.get('reporter'), this.reportFile.outputStream, config, app)
      ];
    } else {
      this.reporters = [setupReporter(config.get('reporter'), stdout, config, app)];

      if (reportPath && !this.useLauncherFiles) {
        this.reporters.push(setupReporter(fileReporterName(config), this.reportFile.outputStream, config, app));
      } else if (this.useLauncherFiles) {
        this._fileReporterName = fileReporterName(config);
      }
    }
  }

  _ensureLauncherReporter(launcherName) {
    if (!this.useLauncherFiles || launcherName === null || launcherName === undefined || launcherName === 'testem') {
      return null;
    }

    if (this.launcherFiles.has(launcherName)) {
      return this.launcherFiles.get(launcherName);
    }

    let reportFile = new ReportFile(this.reportPath, {
      launcher: launcherName,
      date: this.reportDate
    });
    let reporter = setupReporter(this._fileReporterName, reportFile.outputStream, this.config, this.app);
    if (typeof reporter.setLauncherName === 'function') {
      reporter.setLauncherName(launcherName);
    }

    let entry = { reportFile: reportFile, reporter: reporter };
    this.launcherFiles.set(launcherName, entry);
    return entry;
  }

  _forward(fn, args, launcherName) {
    this.reporters.forEach(reporter => {
      if (reporter[fn]) {
        reporter[fn].apply(reporter, args);
      }
    });

    let entry = this._ensureLauncherReporter(launcherName);
    if (entry && entry.reporter[fn]) {
      entry.reporter[fn].apply(entry.reporter, args);
    }
  }

  testStarted(name, data) {
    this._forward('testStarted', [name, data], name);
  }

  onStart(name, data) {
    this._forward('onStart', [name, data], name);
  }

  onEnd(name, data) {
    this._forward('onEnd', [name, data], name);
  }

  reportMetadata(tag, metadata) {
    let args = [tag, metadata];
    this.reporters.forEach(reporter => {
      if (reporter.reportMetadata) {
        reporter.reportMetadata.apply(reporter, args);
      }
    });
    this.launcherFiles.forEach(entry => {
      if (entry.reporter.reportMetadata) {
        entry.reporter.reportMetadata.apply(entry.reporter, args);
      }
    });
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

    if (closers.length) {
      return Bluebird.all(closers);
    }

    if (this.useLauncherFiles) {
      return Bluebird.resolve();
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

    let entry = this._ensureLauncherReporter(name);
    if (entry) {
      entry.reporter.report(name, result);
    }
  }

  finish() {
    if (this._finished) {
      return;
    }
    this._finished = true;

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
