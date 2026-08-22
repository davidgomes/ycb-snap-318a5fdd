

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

function setupFileReporter(reporterName, reportFile, config, app, launcherName) {
  let reporter = setupReporter(reporterName, reportFile.outputStream, config, app);

  if (launcherName && reporter.setLauncherName) {
    reporter.setLauncherName(launcherName);
  }

  return reporter;
}


class Reporter {
  constructor(app, stdout, path) {
    this.total = 0;
    this.passed = 0;
    this.skipped = 0;
    this.todo = 0;
    this._finished = false;

    let config = app.config;
    this.config = config;
    this.app = app;
    this.path = path;
    this.usePerLauncherFiles = !!(path && ReportFile.hasLauncherTemplate(path));
    this.reportFiles = {};
    this.launcherReporters = {};
    this.fileReporterName = this.getFileReporterName();

    if (path && !this.usePerLauncherFiles) {
      this.reportFile = new ReportFile(path);
    }

    if (path && config.get('xunit_intermediate_output') && config.get('reporter') === 'xunit') {
      this.reporters = [
        setupReporter('tap', stdout, config, app)
      ];

      if (!this.usePerLauncherFiles) {
        this.reporters.push(
          setupReporter(config.get('reporter'), this.reportFile.outputStream, config, app)
        );
      }
    } else {
      this.reporters = [setupReporter(config.get('reporter'), stdout, config, app)];

      if (path && !this.usePerLauncherFiles) {
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

  getFileReporterName() {
    if (this.config.appMode === 'dev') {
      let devModeFileReporter = this.config.get('dev_mode_file_reporter');
      if (!devModeFileReporter) {
        devModeFileReporter = 'tap';
      }
      return devModeFileReporter;
    }

    return this.config.get('reporter');
  }

  getOrCreateLauncherReporter(launcherName) {
    if (this.launcherReporters[launcherName]) {
      return this.launcherReporters[launcherName];
    }

    let reportFile = new ReportFile(this.path, { launcher: launcherName });
    this.reportFiles[launcherName] = reportFile;

    let reporter = setupFileReporter(
      this.fileReporterName,
      reportFile,
      this.config,
      this.app,
      launcherName
    );

    this.launcherReporters[launcherName] = reporter;
    return reporter;
  }

  routeToLauncherReporter(launcherName, fn, args) {
    if (!this.usePerLauncherFiles || launcherName === INTERNAL_LAUNCHER) {
      return;
    }

    let launcherReporter = this.getOrCreateLauncherReporter(launcherName);
    if (launcherReporter[fn]) {
      launcherReporter[fn].apply(launcherReporter, args);
    }
  }

  testStarted(name, data) {
    this.reporters.forEach(reporter => {
      if (reporter.testStarted) {
        reporter.testStarted(name, data);
      }
    });

    this.routeToLauncherReporter(name, 'testStarted', [name, data]);
  }

  close() {
    this.finish();

    if (this.usePerLauncherFiles) {
      let closePromises = Object.keys(this.reportFiles).map(launcherName => {
        return this.reportFiles[launcherName].close();
      });

      if (closePromises.length === 0) {
        return Bluebird.resolve();
      }

      return Bluebird.all(closePromises);
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

    this.reporters.forEach(reporter => {
      reporter.report(name, result);
    });

    this.routeToLauncherReporter(name, 'report', [name, result]);
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

    Object.keys(this.launcherReporters).forEach(launcherName => {
      let reporter = this.launcherReporters[launcherName];
      if (reporter.finish) {
        reporter.finish();
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

    let launcherName = args[0];
    this.routeToLauncherReporter(launcherName, fn, args);
  };
}

['onStart', 'onEnd', 'reportMetadata'].forEach(fn => {
  Reporter.prototype[fn] = forwardToReporters(fn);
});

module.exports = Reporter;
