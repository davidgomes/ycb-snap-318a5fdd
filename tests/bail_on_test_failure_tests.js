'use strict';

const expect = require('chai').expect;
const sinon = require('sinon');
const PassThrough = require('stream').PassThrough;
const EventEmitter = require('events').EventEmitter;
const log = require('npmlog');
const Bluebird = require('bluebird');

const Config = require('../lib/config');
const Reporter = require('../lib/utils/reporter');
const App = require('../lib/app');
const Server = require('../lib/server');
const BrowserTestRunner = require('../lib/runners/browser_test_runner');
const ProcessTestRunner = require('../lib/runners/process_test_runner');
const TapProcessTestRunner = require('../lib/runners/tap_process_test_runner');
const Launcher = require('../lib/launcher');
const FakeSocket = require('./support/fake_socket');
const Testem = require('../public/testem/testem_client');
const mochaAdapter = require('../public/testem/mocha_adapter');
const jasmine2Adapter = require('../public/testem/jasmine2_adapter');
const qunitAdapter = require('../public/testem/qunit_adapter');

function appWith(bail, reporterName) {
  return {
    config: new Config('ci', {
      bail_on_test_failure: bail,
      reporter: reporterName || 'tap'
    })
  };
}

function readStream(stream) {
  let chunk = stream.read();
  return chunk ? chunk.toString() : '';
}

describe('bail_on_test_failure', function() {
  describe('config default', function() {
    it('defaults to false', function() {
      let config = new Config('ci', {});
      expect(config.get('bail_on_test_failure')).to.equal(false);
    });
  });

  describe('Reporter', function() {
    let sandbox;

    beforeEach(function() {
      sandbox = sinon.createSandbox();
    });

    afterEach(function() {
      sandbox.restore();
    });

    it('is an EventEmitter', function() {
      let reporter = new Reporter(appWith(false), new PassThrough());
      expect(reporter).to.be.instanceof(EventEmitter);
    });

    ['0', '-1', '1.5', '"nope"', '[]'].forEach(function(encoded) {
      it('warns and disables bail for invalid value ' + encoded, function() {
        let value = JSON.parse(encoded);
        let warn = sandbox.spy(log, 'warn');
        let stream = new PassThrough();
        let reporter = new Reporter(appWith(value), stream);
        expect(warn).to.have.been.calledWith('bail_on_test_failure');
        expect(reporter.hasBailed()).to.equal(false);

        reporter.report('Chrome', {name: 'bad', passed: false});
        reporter.report('Chrome', {name: 'later', passed: true});
        expect(reporter.hasBailed()).to.equal(false);
        reporter.finish();
        let output = readStream(stream);
        expect(output).to.contain('later');
        expect(output).to.not.contain('Bail out!');
      });
    });

    it('treats true as a threshold of one', function() {
      let stream = new PassThrough();
      let reporter = new Reporter(appWith(true), stream);
      let failure = {name: 'breaks', passed: false};
      let seen = null;
      reporter.on('test-failure', function(launcher, result) {
        seen = {launcher: launcher, result: result};
      });

      expect(reporter.getBailReport().bailLauncher).to.equal(null);
      reporter.report('Chrome', {name: 'ok', passed: true});
      reporter.report('Chrome', {name: 'skip me', passed: false, skipped: true});
      reporter.report('Chrome', {name: 'todo me', passed: false, todo: true});
      expect(reporter.hasBailed()).to.equal(false);

      reporter.report('Chrome', failure);
      reporter.report('Chrome', {name: 'after', passed: false});

      expect(reporter.hasBailed()).to.equal(true);
      expect(reporter.bailReason).to.equal('breaks');
      expect(seen.launcher).to.equal('Chrome');
      expect(seen.result).to.equal(failure);

      let report = reporter.getBailReport();
      expect(report.testsRanBeforeBail).to.equal(4);
      expect(report.bailLauncher).to.equal('Chrome');
      expect(report.failuresByLauncher).to.deep.equal({Chrome: 1});
      expect(report.failedTests).to.deep.equal(['breaks']);
      expect(report.failuresByLauncher).to.not.be.instanceof(Map);

      reporter.finish();
      let output = readStream(stream);
      expect(output).to.contain('Bail out! breaks # 1');
      expect(output).to.contain('# bailed');
      expect(output).to.contain('# ran before bail 4');
      expect(output).to.contain('# suppressed 1');
      expect(output).to.not.contain('after');
    });

    it('treats a positive integer as the failure threshold', function() {
      let stream = new PassThrough();
      let reporter = new Reporter(appWith(2, 'dot'), stream);
      reporter.report('Chrome', {name: 'first', passed: false});
      expect(reporter.hasBailed()).to.equal(false);
      reporter.report('Firefox', {name: 'second', passed: false});
      reporter.report('Firefox', {name: 'third', passed: false});
      expect(reporter.bailReason).to.equal('second');
      let report = reporter.getBailReport();
      expect(report.bailLauncher).to.equal('Firefox');
      expect(report.testsRanBeforeBail).to.equal(2);
      expect(report.failuresByLauncher).to.deep.equal({Chrome: 1, Firefox: 1});
      expect(report.failedTests).to.deep.equal(['first', 'second']);
      reporter.finish();
      let output = readStream(stream);
      expect(output).to.contain('Bail out! second # 2');
      expect(output).to.contain('# ran before bail 2');
      expect(output).to.contain('# suppressed 1');
    });

    it('resetBailState clears bail state and limits later output to post-reset activity', function() {
      let stream = new PassThrough();
      let reporter = new Reporter(appWith(true), stream);
      reporter.report('Chrome', {name: 'breaks', passed: false});
      reporter.report('Chrome', {name: 'suppressed', passed: false});
      reporter.resetBailState();
      expect(reporter.hasBailed()).to.equal(false);
      expect(reporter.bailReason).to.equal(null);
      expect(reporter.getBailReport().bailLauncher).to.equal(null);
      expect(reporter.getBailReport().failedTests).to.deep.equal([]);
      expect(reporter.getBailReport().failuresByLauncher).to.deep.equal({});
      expect(reporter.getBailReport().testsRanBeforeBail).to.equal(0);

      stream.read();
      reporter.report('Chrome', {name: 'fresh', passed: true});
      reporter.finish();
      let output = readStream(stream);
      expect(output).to.contain('fresh');
      expect(output).to.not.contain('breaks');
      expect(output).to.not.contain('Bail out!');
      expect(output).to.contain('# tests 1');
    });

    it('emits teamcity bail statistics', function() {
      let stream = new PassThrough();
      let reporter = new Reporter(appWith(true, 'teamcity'), stream);
      reporter.report('Chrome', {name: 'breaks', passed: false});
      reporter.report('Chrome', {name: 'later', passed: true});
      reporter.finish();
      let output = readStream(stream);
      expect(output).to.contain('##teamcity[message text=\'Bail out! breaks # 1\' status=\'ERROR\']');
      expect(output).to.contain('##teamcity[buildStatisticValue key=\'bailedTests\' value=\'1\']');
      expect(output).to.contain('##teamcity[buildStatisticValue key=\'testsBeforeBail\' value=\'1\']');
      expect(output).to.contain('##teamcity[buildStatisticValue key=\'suppressedAfterBail\' value=\'1\']');
      expect(output).to.contain('##teamcity[buildProblem description=\'Bail out! breaks # 1\'');
      expect(output).to.not.contain('later');
    });

    it('adds xunit bail markup', function() {
      let stream = new PassThrough();
      let reporter = new Reporter(appWith(true, 'xunit'), stream);
      reporter.report('Chrome', {name: 'breaks', passed: false, error: {message: 'nope'}});
      reporter.report('Chrome', {name: 'later', passed: false});
      reporter.finish();
      let output = readStream(stream);
      expect(output).to.match(/errors="1"/);
      expect(output).to.contain('<property name="bailReason" value="breaks"');
      expect(output).to.contain('<property name="testsBeforeBail" value="1"');
      expect(output).to.contain('<property name="suppressedAfterBail" value="1"');
      expect(output).to.contain('<error message="Bail out! breaks # 1"');
      expect(output).to.contain('<system-out>');
      expect(output).to.contain('Bail out! breaks # 1');
      expect(output).to.contain('# bailed');
      expect(output).to.not.contain('later');
    });
  });

  describe('abort', function() {
    it('server broadcastAbort emits abort-tests once and tolerates missing io', function() {
      let server = new Server(new Config('ci', {}));
      expect(function() {
        server.broadcastAbort();
        server.broadcastAbort();
      }).not.to.throw();

      let emit = sinon.spy();
      server.io = {emit: emit};
      server.broadcastAbort();
      server.broadcastAbort();
      expect(emit).to.have.been.calledOnce();
      expect(emit).to.have.been.calledWith('abort-tests');

      server.resetAbort();
      server.broadcastAbort();
      expect(emit).to.have.been.calledTwice();
    });

    it('browser runner abort is an idempotent promise and suppresses later results', function() {
      let calls = [];
      let reporter = {
        report: function(name, result) {
          calls.push(result.name);
        },
        onStart: function() {},
        onEnd: function() {},
        reportMetadata: function() {}
      };
      let config = new Config('ci', {});
      let launcher = new Launcher('Chrome', {protocol: 'browser'}, config);
      let runner = new BrowserTestRunner(launcher, reporter, 0, true, config);
      let socket = new FakeSocket();
      let abortEmit = sinon.spy(socket, 'emit');
      runner.tryAttach('Chrome', launcher.id, socket);

      let first = runner.abort();
      let second = runner.abort();
      expect(first).to.equal(second);
      return first.then(function() {
        expect(abortEmit.withArgs('abort-tests')).to.have.callCount(1);
        runner.onTestResult({name: 'later', failed: 1});
        runner.onGlobalError('boom', 'http://x', 1);
        expect(calls).to.deep.equal([]);
      });
    });

    it('process and tap runners abort idempotently and suppress results', function() {
      let reporter = {
        report: sinon.spy(),
        onStart: function() {},
        onEnd: function() {}
      };
      let config = new Config('ci', {});
      let launcher = new Launcher('node', {protocol: 'process'}, config);
      let runner = new ProcessTestRunner(launcher, reporter);
      let first = runner.abort();
      expect(runner.abort()).to.equal(first);
      return first.then(function() {
        runner.onProcessError(new Error('nope'));
        expect(reporter.report).to.not.have.been.called();
      }).then(function() {
        let tapLauncher = new Launcher('node', {protocol: 'tap'}, config);
        let tapRunner = new TapProcessTestRunner(tapLauncher, reporter);
        return tapRunner.abort().then(function() {
          tapRunner.onTestResult({name: 'nope', passed: false});
          expect(reporter.report).to.not.have.been.called();
        });
      });
    });

    it('app.abortRunners broadcasts and aborts every runner once', function() {
      let app = new App(new Config('ci', {}));
      let emit = sinon.spy();
      app.server.io = {emit: emit};
      let runner = {abort: sinon.spy(function() { return Bluebird.resolve(); })};
      app.runners = [runner, runner];
      let pending = app.abortRunners();
      expect(app.abortRunners()).to.equal(pending);
      return pending.then(function() {
        expect(emit).to.have.been.calledOnce();
        expect(emit).to.have.been.calledWith('abort-tests');
        expect(runner.abort).to.have.been.calledTwice();
        app.resetBailState();
        expect(app.server._abortBroadcasted).to.equal(false);
        let again = app.abortRunners();
        return again.then(function() {
          expect(emit).to.have.been.calledTwice();
        });
      });
    });
  });

  describe('getExitCode', function() {
    it('returns a bail error from bailReason and testsRanBeforeBail', function() {
      let app = new App(new Config('ci', {}));
      app.reporter = {
        hasBailed: function() { return true; },
        bailReason: 'breaks',
        getBailReport: function() {
          return {
            testsRanBeforeBail: 4,
            bailLauncher: 'should-not-be-used',
            failuresByLauncher: {Chrome: 9},
            failedTests: ['other']
          };
        },
        hasPassed: function() { return false; },
        hasTests: function() { return true; }
      };
      let err = app.getExitCode();
      expect(err.message).to.contain('breaks');
      expect(err.message).to.contain('4');
      expect(err.message).to.not.contain('Not all tests passed');
      expect(err.message).to.not.contain('should-not-be-used');
    });
  });

  describe('browser adapters', function() {
    let globals;

    function replaceGlobals(next) {
      globals = {};
      Object.keys(next).forEach(function(key) {
        globals[key] = global[key];
        global[key] = next[key];
      });
    }

    afterEach(function() {
      if (!globals) {
        return;
      }
      Object.keys(globals).forEach(function(key) {
        global[key] = globals[key];
      });
      globals = null;
      Testem.aborted = false;
    });

    it('mocha suppresses events once aborted and signals all-test-results once', function() {
      function Runner() {}
      Runner.prototype.emit = function() {};
      let emitted = [];
      let scheduled = [];
      replaceGlobals({
        mocha: {Runner: Runner},
        Mocha: {Runner: Runner},
        emit: function() {
          emitted.push(Array.prototype.slice.call(arguments));
        },
        setTimeout: function(fn) {
          scheduled.push(fn);
          return 1;
        },
        Testem: {aborted: false}
      });
      mochaAdapter();
      let runner = new Runner();
      runner.emit('start', {title: 'suite'});
      expect(emitted[0][0]).to.equal('tests-start');

      global.Testem.aborted = true;
      emitted.length = 0;
      runner.emit('fail', {title: 'nope', duration: 1}, {message: 'x'});
      runner.emit('test end', {title: 'nope', state: 'passed'});
      expect(scheduled).to.have.length(0);
      expect(emitted.map(function(args) { return args[0]; })).to.deep.equal(['all-test-results']);
    });

    it('mocha guards inside a deferred callback that was scheduled before abort', function() {
      function Runner() {}
      Runner.prototype.emit = function() {};
      let emitted = [];
      let scheduled = [];
      replaceGlobals({
        mocha: {Runner: Runner},
        Mocha: {Runner: Runner},
        emit: function() {
          emitted.push(Array.prototype.slice.call(arguments));
        },
        setTimeout: function(fn) {
          scheduled.push(fn);
          return 1;
        },
        Testem: {aborted: false}
      });
      mochaAdapter();
      let runner = new Runner();
      runner.emit('test end', {
        title: 'ok',
        state: 'passed',
        duration: 1,
        parent: {title: 'suite'}
      });
      expect(scheduled).to.have.length(1);
      global.Testem.aborted = true;
      scheduled[0]();
      expect(emitted.map(function(args) { return args[0]; })).to.deep.equal(['all-test-results']);
    });

    it('jasmine2 suppresses specs once aborted and signals once', function() {
      let emitted = [];
      let reporter;
      replaceGlobals({
        emit: function() {
          emitted.push(Array.prototype.slice.call(arguments));
        },
        jasmine: {
          getEnv: function() {
            return {
              addReporter: function(r) {
                reporter = r;
              }
            };
          }
        },
        Testem: {aborted: true}
      });
      jasmine2Adapter();
      reporter.jasmineStarted();
      reporter.specStarted({fullName: 'hidden'});
      reporter.specDone({
        id: 0,
        fullName: 'hidden',
        status: 'failed',
        failedExpectations: [{message: 'x', passed: false}]
      });
      reporter.jasmineDone();
      expect(emitted.map(function(args) { return args[0]; })).to.deep.equal(['all-test-results']);
    });

    it('qunit clears its queue and signals all-test-results once', function() {
      let emitted = [];
      let hooks = {};
      let queue = [function() {}, function() {}];
      replaceGlobals({
        emit: function() {
          emitted.push(Array.prototype.slice.call(arguments));
        },
        QUnit: {
          config: {queue: queue},
          log: function(fn) { hooks.log = fn; },
          testStart: function(fn) { hooks.testStart = fn; },
          testDone: function(fn) { hooks.testDone = fn; },
          done: function(fn) { hooks.done = fn; }
        },
        Testem: {aborted: true}
      });
      qunitAdapter();
      hooks.testStart({name: 'hidden', module: 'mod'});
      hooks.testDone({failed: 1, passed: 0, total: 1, runtime: 1});
      hooks.done({runtime: 1});
      hooks.log({result: false, message: 'nope'});
      expect(queue).to.have.length(0);
      expect(emitted.map(function(args) { return args[0]; })).to.deep.equal(['all-test-results']);
    });
  });

  describe('client handleAbortTests', function() {
    afterEach(function() {
      Testem.aborted = false;
      Testem._allowEmitWhileAborted = false;
      Testem._isIframeReady = false;
      Testem.emitMessageQueue = [];
    });

    it('sets aborted, emits abort events, and blocks later emitMessage', function() {
      let sent = [];
      Testem._isIframeReady = true;
      Testem.iframe = {
        contentWindow: {
          postMessage: function(message) {
            sent.push(message);
          }
        }
      };
      Testem.evtHandlers = {};
      Testem.handleAbortTests();
      expect(Testem.aborted).to.equal(true);
      expect(sent.join('\n')).to.contain('abort-tests');
      expect(sent.join('\n')).to.contain('after-tests-complete');
      let before = sent.length;
      Testem.emitMessage('test-result', {name: 'later'});
      expect(sent).to.have.length(before);
    });
  });
});
