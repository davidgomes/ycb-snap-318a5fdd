
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

function fileReporterFor(config) {
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
  constructor(app, stdout, path) {
    this.total = 0;
    this.passed = 0;
    this.skipped = 0;
    this.todo = 0;
    this.finished = false;
    this.launcherFiles = new Map();
    this.app = app;
    this.stdout = stdout;
    this.reportFilePath = path;
    this.reportDate = new Date();
    this.fileReporterName = null;

    let config = app.config;
    this.partitionByLauncher = !!(path && ReportFile.hasLauncherTemplate(path));

    if (path && !this.partitionByLauncher) {
      this.reportFile = new ReportFile(path, { date: this.reportDate });
    }

    if (path && !this.partitionByLauncher && config.get('xunit_intermediate_output') && config.get('reporter') === 'xunit') {
      this.reporters = [
        setupReporter('tap', stdout, config, app),
        setupReporter(config.get('reporter'), this.reportFile.outputStream, config, app)
      ];
    } else if (this.partitionByLauncher && config.get('xunit_intermediate_output') && config.get('reporter') === 'xunit') {
      this.reporters = [setupReporter('tap', stdout, config, app)];
      this.fileReporterName = 'xunit';
    } else {
      this.reporters = [setupReporter(config.get('reporter'), stdout, config, app)];

      if (path && !this.partitionByLauncher) {
        this.reporters.push(setupReporter(fileReporterFor(config), this.reportFile.outputStream, config, app));
      } else if (this.partitionByLauncher) {
        this.fileReporterName = fileReporterFor(config);
      }
    }
  }

  _ensureLauncherFile(launcherName) {
    if (!this.partitionByLauncher) {
      return null;
    }
    if (typeof launcherName !== 'string' || launcherName.length === 0 || launcherName === INTERNAL_LAUNCHER) {
      return null;
    }

    let key = ReportFile.sanitizeLauncherName(launcherName);
    if (!key || key === INTERNAL_LAUNCHER) {
      return null;
    }
    if (this.launcherFiles.has(key)) {
      return this.launcherFiles.get(key);
    }

    let config = this.app.config;
    let reportFile = new ReportFile(this.reportFilePath, {
      launcher: launcherName,
      date: this.reportDate
    });
    let reporter = setupReporter(this.fileReporterName, reportFile.outputStream, config, this.app);
    if (reporter && typeof reporter.setLauncherName === 'function') {
      reporter.setLauncherName(launcherName);
    }

    let entry = {
      reportFile: reportFile,
      reporter: reporter,
      launcherName: launcherName
    };
    this.launcherFiles.set(key, entry);
    return entry;
  }

  testStarted(name, data) {
    this.reporters.forEach(reporter => {
      if (reporter.testStarted) {
        reporter.testStarted(name, data);
      }
    });

    let entry = this._ensureLauncherFile(name);
    if (entry && entry.reporter.testStarted) {
      entry.reporter.testStarted(name, data);
    }
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

    let entry = this._ensureLauncherFile(name);
    if (entry) {
      entry.reporter.report(name, result);
    }
  }

  finish() {
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
      if (entry.reporter && entry.reporter.finish) {
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

    if (!this.partitionByLauncher) {
      return;
    }

    if (fn === 'reportMetadata') {
      this.launcherFiles.forEach(entry => {
        if (entry.reporter[fn]) {
          entry.reporter[fn].apply(entry.reporter, args);
        }
      });
      return;
    }

    let entry = this._ensureLauncherFile(args[0]);
    if (entry && entry.reporter[fn]) {
      entry.reporter[fn].apply(entry.reporter, args);
    }
  };
}

['onStart', 'onEnd', 'reportMetadata'].forEach(fn => {
  Reporter.prototype[fn] = forwardToReporters(fn);
});

module.exports = Reporter;
