

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
    this.stdout = stdout;
    this.reportPath = path;
    this.perLauncher = !!(path && ReportFile.hasLauncherTemplate(path));
    this.fileReporters = {};
    this.finished = false;

    if (path && !this.perLauncher) {
      this.reportFile = new ReportFile(path);
    }

    let config = app.config;

    if (path && config.get('xunit_intermediate_output') && config.get('reporter') === 'xunit') {
      this.reporters = [
        setupReporter('tap', stdout, config, app),
        setupReporter(config.get('reporter'), this.reportFile.outputStream, config, app)
      ];
    } else {
      this.reporters = [setupReporter(config.get('reporter'), stdout, config, app)];

      if (path && !this.perLauncher) {
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
    if (this.finished) {
      return this.closePromise;
    }
    this.finished = true;
    this.finish();

    const files = Object.keys(this.fileReporters).map(key => this.fileReporters[key].file.close());
    if (this.reportFile) files.push(this.reportFile.close());
    this.closePromise = Bluebird.all(files);
    return this.closePromise;
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
    if (this.perLauncher && name !== 'testem') {
      const fileReporter = this.getFileReporter(name);
      fileReporter.report(name, result);
    }
  }

  getFileReporter(name) {
    if (!this.fileReporters[name]) {
      const config = this.app.config;
      const file = new ReportFile(this.reportPath, { launcher: name });
      const reporterName = config.appMode === 'dev' ? (config.get('dev_mode_file_reporter') || 'tap') : config.get('reporter');
      this.fileReporters[name] = {
        file: file,
        reporter: setupReporter(reporterName, file.outputStream, config, this.app)
      };
      if (this.fileReporters[name].reporter.setLauncherName) {
        this.fileReporters[name].reporter.setLauncherName(name);
      }
    }
    return this.fileReporters[name].reporter;
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
    Object.keys(this.fileReporters || {}).forEach(name => {
      const reporter = this.fileReporters[name].reporter;
      if (name !== 'testem' && reporter[fn]) {
        reporter[fn].apply(reporter, args);
      }
    });
  };
}

['finish', 'onStart', 'onEnd', 'reportMetadata'].forEach(fn => {
  Reporter.prototype[fn] = forwardToReporters(fn);
});

module.exports = Reporter;
