'use strict';
var Bluebird = require('bluebird');

var toResult = require('./to-result');

class ProcessTestRunner {
  constructor(launcher, reporter) {
    this.launcher = launcher;
    this.reporter = reporter;
    this.launcherId = this.launcher.id;
    this.finished = false;
    this.aborted = false;
    this.abortPromise = null;
  }

  start(onFinish) {
    this.onStart();
    this.finished = false;
    this.aborted = false;
    this.abortPromise = null;

    return new Bluebird.Promise((resolve, reject) => {
      this.onFinish = resolve;
      this.launcher.start().then(testProcess => {
        this.process = testProcess;
        this.process.once('processExit', this.onProcessExit.bind(this));
        this.process.once('processError', this.onProcessError.bind(this));
      }).catch(reject);
    }).asCallback(onFinish);
  }

  exit() {
    if (!this.process) {
      return Bluebird.resolve();
    }

    return this.process.kill();
  }

  abort() {
    if (this.abortPromise) {
      return this.abortPromise;
    }

    this.aborted = true;
    this.abortPromise = Bluebird.try(() => {
      let exited = this.exit();
      this.finish();
      return exited;
    }).then(() => {});
    return this.abortPromise;
  }

  onProcessExit(code, stdout, stderr) {
    this.finish(null, code, stdout, stderr);
  }

  name() {
    return this.launcher.name;
  }

  onProcessError(err, stdout, stderr) {
    this.lastErr = err;
    this.lastStderr = stderr;
    this.finish(err, 0, stdout, stderr);
  }

  onStart() {
    this.reporter.onStart(this.launcher.name, {
      launcherId: this.launcherId
    });
  }

  onEnd() {
    this.reporter.onEnd(this.launcher.name, {
      launcherId: this.launcherId
    });
  }

  finish(err, code) {
    if (this.finished) {
      return;
    }
    this.finished = true;
    var runnerProcess = this.process;
    this.process = null;

    if (!this.aborted) {
      var result = toResult(this.launcherId, err, code, runnerProcess);
      this.reporter.report(this.launcher.name, result);
    }
    this.onEnd();

    if (this.onFinish) {
      this.onFinish();
    }
  }
}

module.exports = ProcessTestRunner;
