'use strict';

var TapConsumer = require('../tap_consumer');
var log = require('npmlog');
var Bluebird = require('bluebird');

var toResult = require('./to-result');

class TapProcessTestRunner {
  constructor(launcher, reporter) {
    this.launcher = launcher;
    this.reporter = reporter;
    this.launcherId = this.launcher.id;
    this.finished = false;
    this.aborted = false;
    this._abortPromise = null;
    log.info(this.launcher.name);
  }

  abort() {
    if (this._abortPromise) {
      return this._abortPromise;
    }
    this.aborted = true;
    let wasFinished = this.finished;
    this.finished = true;
    this._abortPromise = Bluebird.resolve(this.exit()).then(() => {
      if (!wasFinished && this.onFinish) {
        this.onEnd();
        this.onFinish();
      }
    });
    return this._abortPromise;
  }

  start(onFinish) {
    this.onStart();
    this.finished = false;

    this.tapConsumer = new TapConsumer();
    this.tapConsumer.on('test-result', this.onTestResult.bind(this));
    this.tapConsumer.on('all-test-results', this.onAllTestResults.bind(this));

    return new Bluebird.Promise((resolve, reject) => {
      this.onFinish = resolve;
      this.launcher.start().then(tapProcess => {
        this.process = tapProcess;
        this.process.once('processError', this.onProcessError.bind(this));
        this.process.process.stdout.pipe(this.tapConsumer.stream);
      }).catch(reject);
    }).asCallback(onFinish);
  }

  exit() {
    if (!this.process) {
      return Bluebird.resolve();
    }

    return this.process.kill();
  }

  onTestResult(test) {
    if (this.aborted) {
      return;
    }
    test.launcherId = this.launcherId;
    this.reporter.report(this.launcher.name, test);
  }

  onAllTestResults() {
    setTimeout(() => { // Workaround Node 0.10 finishing stdout before receiving process error
      if (!this.aborted) {
        this.wrapUp();
      }
    }, 100);
  }

  wrapUp() {
    if (this.aborted || this.finished) {
      return;
    }
    this.finished = true;
    this.process = null;
    this.onEnd();
    this.onFinish();
  }

  name() {
    return this.launcher.name;
  }

  onProcessError(err) {
    if (this.aborted) {
      return;
    }
    var result = toResult(this.launcherId, err, 0, this.process);
    this.reporter.report(this.launcher.name, result);
    this.wrapUp();
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
}

module.exports = TapProcessTestRunner;
