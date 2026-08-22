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
    this._abortPromise = null;
    this._exitPromise = null;
  }

  abort() {
    if (this._abortPromise) {
      return this._abortPromise;
    }
    this.aborted = true;
    let wasFinished = this.finished;
    this.finished = true;
    let waitForLaunch = this._launchPromise || Bluebird.resolve();
    this._abortPromise = waitForLaunch.then(() => this.exit()).then(() => {
      if (!wasFinished && this.onFinish) {
        this.onFinish();
      }
    });
    return this._abortPromise;
  }

  start(onFinish) {
    if (this.aborted) {
      return Bluebird.resolve().asCallback(onFinish);
    }
    this.onStart();
    this.finished = false;

    return new Bluebird.Promise((resolve, reject) => {
      this.onFinish = resolve;
      this._launchPromise = this.launcher.start().then(testProcess => {
        if (this.aborted) {
          this.process = testProcess;
          return this.exit();
        }
        this.process = testProcess;
        this.process.once('processExit', this.onProcessExit.bind(this));
        this.process.once('processError', this.onProcessError.bind(this));
      }).catch(reject);
    }).asCallback(onFinish);
  }

  exit() {
    if (this._exitPromise) {
      return this._exitPromise;
    }
    if (!this.process) {
      return Bluebird.resolve();
    }

    this._exitPromise = this.process.kill();
    return this._exitPromise;
  }

  onProcessExit(code, stdout, stderr) {
    this.finish(null, code, stdout, stderr);
  }

  name() {
    return this.launcher.name;
  }

  onProcessError(err, stdout, stderr) {
    if (this.aborted) {
      return;
    }
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
    if (this.aborted || this.finished) {
      return;
    }
    this.finished = true;
    var runnerProcess = this.process;
    this.process = null;

    var result = toResult(this.launcherId, err, code, runnerProcess);
    this.reporter.report(this.launcher.name, result);
    this.onEnd();

    this.onFinish();
  }
}

module.exports = ProcessTestRunner;
