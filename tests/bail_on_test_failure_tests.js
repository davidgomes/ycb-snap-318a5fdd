'use strict';

const expect = require('chai').expect;
const sinon = require('sinon');
const Bluebird = require('bluebird');
const log = require('npmlog');
const PassThrough = require('stream').PassThrough;

const Config = require('../lib/config');
const App = require('../lib/app');
const Server = require('../lib/server');
const Reporter = require('../lib/utils/reporter');
const BrowserTestRunner = require('../lib/runners/browser_test_runner');
const ProcessTestRunner = require('../lib/runners/process_test_runner');
const TapProcessTestRunner = require('../lib/runners/tap_process_test_runner');
const Launcher = require('../lib/launcher');
const FakeReporter = require('./support/fake_reporter');
const FakeSocket = require('./support/fake_socket');
const Testem = require('../public/testem/testem_client');
const mochaAdapter = require('../public/testem/mocha_adapter');
const jasmine2Adapter = require('../public/testem/jasmine2_adapter');
const qunitAdapter = require('../public/testem/qunit_adapter');

function mockApp(options) {
  return {
    config: {
      get(key) {
        return options[key];
      }
    }
  };
}

function pass(name) {
  return { name, passed: true };
}

function fail(name) {
  return { name, passed: false, error: { message: `${name} failed` } };
}

describe('bail_on_test_failure', function() {
  let sandbox, stream;

  beforeEach(function() {
    sandbox = sinon.createSandbox();
    stream = new PassThrough();
  });

  afterEach(function() {
    sandbox.restore();
  });

  it('defaults to false', function() {
    expect(new Config('ci').get('bail_on_test_failure')).to.equal(false);
  });

  describe('Reporter config validation', function() {
    [true, false, 1, 3].forEach(value => {
      it(`accepts ${value}`, function() {
        let warn = sandbox.stub(log, 'warn');
        let reporter = new Reporter(mockApp({ reporter: new FakeReporter(), bail_on_test_failure: value }), stream);

        expect(reporter.bailOnTestFailure).to.equal(value);
        expect(warn).not.to.have.been.called();
      });
    });

    [0, -1, 1.5, '2', 'yes'].forEach(value => {
      it(`warns and defaults to false for ${JSON.stringify(value)}`, function() {
        let warn = sandbox.stub(log, 'warn');
        let reporter = new Reporter(mockApp({ reporter: new FakeReporter(), bail_on_test_failure: value }), stream);

        expect(reporter.bailOnTestFailure).to.equal(false);
        expect(warn).to.have.been.calledOnceWith('bail_on_test_failure');
      });
    });
  });

  describe('Reporter', function() {
    let subReporter;

    function createReporter(value) {
      subReporter = new FakeReporter();
      return new Reporter(mockApp({ reporter: subReporter, bail_on_test_failure: value }), stream);
    }

    it('is an EventEmitter', function() {
      expect(createReporter(true).on).to.be.a('function');
    });

    it('does not bail when disabled', function() {
      let reporter = createReporter(false);
      reporter.report('Chrome', fail('a'));
      reporter.report('Chrome', fail('b'));

      expect(reporter.hasBailed()).to.be.false();
      expect(subReporter.results).to.have.length(2);
    });

    it('bails on the first failure when true', function() {
      let reporter = createReporter(true);
      let listener = sandbox.spy();
      reporter.on('test-failure', listener);

      reporter.report('Chrome', pass('ok'));
      let failure = fail('broken');
      reporter.report('Chrome', failure);

      expect(reporter.hasBailed()).to.be.true();
      expect(reporter.bailReason).to.equal('broken');
      expect(listener).to.have.been.calledOnceWith('Chrome', failure);
    });

    it('bails on the Nth failure ignoring skipped and todo tests', function() {
      let reporter = createReporter(2);
      reporter.report('Chrome', fail('one'));
      reporter.report('Chrome', { name: 'skipped', skipped: true });
      reporter.report('Chrome', { name: 'todo', todo: true, passed: false });
      expect(reporter.hasBailed()).to.be.false();

      reporter.report('Firefox', fail('two'));
      expect(reporter.hasBailed()).to.be.true();
      expect(reporter.bailReason).to.equal('two');
    });

    it('suppresses results reported after bailing', function() {
      let reporter = createReporter(true);
      reporter.report('Chrome', fail('broken'));
      reporter.report('Chrome', pass('late'));
      reporter.report('Chrome', fail('late failure'));

      expect(subReporter.results).to.have.length(1);
      expect(reporter.getBailReport().suppressedAfterBail).to.equal(2);
    });

    it('exposes a bail report', function() {
      let reporter = createReporter(2);
      expect(reporter.getBailReport().bailLauncher).to.be.null();

      reporter.report('Chrome', pass('ok'));
      reporter.report('Chrome', fail('one'));
      reporter.report('Firefox', fail('two'));

      let report = reporter.getBailReport();
      expect(report.testsRanBeforeBail).to.equal(3);
      expect(report.bailLauncher).to.equal('Firefox');
      expect(Object.getPrototypeOf(report.failuresByLauncher)).to.equal(Object.prototype);
      expect(report.failuresByLauncher).to.deep.equal({ Chrome: 1, Firefox: 1 });
      expect(report.failedTests).to.deep.equal(['one', 'two']);
    });

    it('resets bail state', function() {
      let reporter = createReporter(true);
      reporter.report('Chrome', fail('broken'));
      reporter.resetBailState();

      expect(reporter.hasBailed()).to.be.false();
      expect(reporter.bailReason).to.be.null();
      let report = reporter.getBailReport();
      expect(report.bailLauncher).to.be.null();
      expect(report.failuresByLauncher).to.deep.equal({});
      expect(report.failedTests).to.deep.equal([]);
      expect(subReporter.results).to.have.length(0);

      reporter.report('Chrome', pass('after reset'));
      expect(subReporter.results).to.have.length(1);
    });
  });

  describe('reporter output', function() {
    function run(reporterName, extraConfig) {
      let reporter = new Reporter(mockApp(Object.assign({ reporter: reporterName, bail_on_test_failure: true }, extraConfig)), stream);
      reporter.report('Chrome', pass('first'));
      reporter.report('Chrome', fail('second'));
      reporter.report('Chrome', pass('third'));
      reporter.report('Chrome', pass('fourth'));
      reporter.finish();
      return { reporter, output: stream.read().toString() };
    }

    it('tap reports the bail in its summary', function() {
      let output = run('tap').output;

      expect(output).to.contain('Bail out! second (1 failure)');
      expect(output).to.contain('# bailed');
      expect(output).to.contain('# ran before bail 2');
      expect(output).to.contain('# suppressed 2');
      expect(output).not.to.contain('# ok');
      expect(output).not.to.contain('third');
    });

    it('tap summary only reflects post-reset activity', function() {
      let reporter = new Reporter(mockApp({ reporter: 'tap', bail_on_test_failure: true }), stream);
      reporter.report('Chrome', fail('broken'));
      reporter.resetBailState();
      reporter.report('Chrome', pass('fresh'));
      reporter.finish();
      let output = stream.read().toString();
      let summary = output.slice(output.lastIndexOf('1..'));

      expect(summary).to.contain('# tests 1');
      expect(summary).not.to.contain('Bail out!');
      expect(summary).to.contain('# ok');
    });

    it('dot reports the bail in its summary', function() {
      let output = run('dot').output;

      expect(output).to.contain('Bail out! second (1 failure)');
      expect(output).to.contain('# bailed');
      expect(output).to.contain('# ran before bail 2');
      expect(output).to.contain('# suppressed 2');
    });

    it('teamcity reports the bail as build statistics and problem', function() {
      let output = run('teamcity').output;

      expect(output).to.contain('##teamcity[message text=\'Bail out! second\' status=\'ERROR\']');
      expect(output).to.contain('##teamcity[buildStatisticValue key=\'bailedTests\' value=\'1\']');
      expect(output).to.contain('##teamcity[buildStatisticValue key=\'testsBeforeBail\' value=\'2\']');
      expect(output).to.contain('##teamcity[buildStatisticValue key=\'suppressedAfterBail\' value=\'2\']');
      expect(output).to.contain('##teamcity[buildProblem');
    });

    it('xunit reports the bail as error, properties and system-out', function() {
      let output = run('xunit').output;

      expect(output).to.contain('errors="1"');
      expect(output).to.match(/<testsuite[^>]*><properties>/);
      expect(output).to.contain('<property name="bailReason" value="second"/>');
      expect(output).to.contain('<property name="testsBeforeBail" value="2"/>');
      expect(output).to.contain('<property name="suppressedAfterBail" value="2"/>');
      expect(output).to.contain('<error message="Bail out! second (1 failure)"/>');
      expect(output).to.match(/<system-out><!\[CDATA\[Bail out! second[\s\S]*suppressed 2\]\]><\/system-out>/);
    });

    it('xunit output is unchanged without a bail', function() {
      let reporter = new Reporter(mockApp({ reporter: 'xunit' }), stream);
      reporter.report('Chrome', fail('second'));
      reporter.finish();
      let output = stream.read().toString();

      expect(output).not.to.contain('errors=');
      expect(output).not.to.contain('<properties>');
      expect(output).not.to.contain('<system-out>');
    });
  });

  describe('runners abort', function() {
    let config, launcher, reporter;

    beforeEach(function() {
      reporter = new FakeReporter();
      config = new Config('ci', { reporter });
      launcher = new Launcher('ci', { protocol: 'browser' }, config);
    });

    it('browser runner emits abort-tests, is idempotent and suppresses results', function() {
      let runner = new BrowserTestRunner(launcher, reporter, 0, false, config);
      let socket = new FakeSocket();
      runner.tryAttach('Chrome', launcher.id, socket);
      let socketEmit = sandbox.spy(socket, 'emit');

      let first = runner.abort();
      let second = runner.abort();

      expect(first).to.be.an.instanceof(Bluebird);
      expect(second).to.equal(first);
      expect(socketEmit.withArgs('abort-tests')).to.have.been.calledOnce();

      socket.emit('test-result', { failed: 1, name: 'late' });
      socket.emit('top-level-error', 'boom', 'http://x', 1);
      runner.reportResults(new Error('late error'), 1);
      expect(reporter.results).to.have.length(0);

      return first;
    });

    it('process runner kills the process and suppresses results', function() {
      let runner = new ProcessTestRunner(new Launcher('node', { protocol: 'process' }, config), reporter);
      let kill = sandbox.stub().resolves();
      runner.process = { kill };
      runner.onFinish = sandbox.spy();

      let first = runner.abort();
      expect(runner.abort()).to.equal(first);

      return first.then(() => {
        expect(kill).to.have.been.calledOnce();
        expect(runner.onFinish).to.have.been.calledOnce();
        runner.onProcessExit(1);
        expect(reporter.results).to.have.length(0);
      });
    });

    it('tap process runner kills the process and suppresses results', function() {
      let runner = new TapProcessTestRunner(new Launcher('tap', { protocol: 'tap' }, config), reporter);
      runner.process = { kill: sandbox.stub().resolves() };
      runner.onFinish = sandbox.spy();

      return runner.abort().then(() => {
        runner.onTestResult({ name: 'late', passed: false });
        runner.onProcessError(new Error('late'));
        expect(reporter.results).to.have.length(0);
        expect(runner.onFinish).to.have.been.calledOnce();
      });
    });
  });

  describe('Server', function() {
    it('broadcasts abort-tests once and tolerates missing io', function() {
      let server = new Server(new Config('ci'));
      expect(() => server.broadcastAbort()).not.to.throw();

      server.resetAbort();
      server.io = { emit: sinon.spy() };
      server.broadcastAbort();
      server.broadcastAbort();
      expect(server.io.emit).to.have.been.calledOnceWith('abort-tests');

      server.resetAbort();
      server.broadcastAbort();
      expect(server.io.emit).to.have.been.calledTwice();
    });
  });

  describe('App', function() {
    let app;

    beforeEach(function() {
      app = new App(new Config('ci', { reporter: new FakeReporter() }));
    });

    it('aborts all runners once and broadcasts abort', function() {
      let runner = { abort: sandbox.stub().resolves() };
      app.runners = [runner, { abort: sandbox.stub().resolves() }];
      sandbox.spy(app.server, 'broadcastAbort');

      let first = app.abortRunners();
      expect(app.abortRunners()).to.equal(first);

      return first.then(() => {
        expect(app.server.broadcastAbort).to.have.been.calledOnce();
        expect(runner.abort).to.have.been.calledOnce();
      });
    });

    it('resetBailState resets reporter, abort tracking and server', function() {
      app.reporter = { resetBailState: sandbox.spy() };
      app.runners = [{ abort: sandbox.stub().resolves() }];
      sandbox.spy(app.server, 'resetAbort');

      let first = app.abortRunners();
      app.resetBailState();

      expect(app.reporter.resetBailState).to.have.been.calledOnce();
      expect(app.server.resetAbort).to.have.been.calledOnce();
      expect(app.abortRunners()).not.to.equal(first);
    });

    it('returns a bail specific exit error', function() {
      app.reporter = {
        hasBailed: () => true,
        hasPassed: () => false,
        hasTests: () => true,
        bailReason: 'broken test',
        getBailReport: () => ({ testsRanBeforeBail: 4 })
      };

      let err = app.getExitCode();
      expect(err.message).to.contain('broken test');
      expect(err.message).to.contain('4');
      expect(err.message).not.to.match(/Not all tests passed/);
    });
  });

  describe('Testem client', function() {
    afterEach(function() {
      Testem.aborted = false;
      Testem.emitMessageQueue = [];
    });

    it('handleAbortTests emits abort-tests and after-tests-complete and blocks further messages', function() {
      Testem._isIframeReady = true;
      let emitted = [];
      sandbox.stub(Testem, 'emitMessageToIframe').callsFake(message => emitted.push(message.emitArgs[0]));

      Testem.handleAbortTests();
      Testem.handleAbortTests();
      Testem.emitMessage('test-result', {});

      expect(Testem.aborted).to.be.true();
      expect(emitted).to.deep.equal(['abort-tests', 'after-tests-complete']);
    });
  });

  describe('browser adapters', function() {
    let emit;

    beforeEach(function() {
      emit = sandbox.stub();
      global.emit = emit;
      global.Testem = { aborted: false };
    });

    afterEach(function() {
      delete global.emit;
      delete global.Testem;
      delete global.mocha;
      delete global.Mocha;
      delete global.jasmine;
      delete global.QUnit;
    });

    it('mocha suppresses events once aborted, including deferred ones', function() {
      function Runner() {}
      Runner.prototype.emit = sandbox.stub();
      global.mocha = { Runner };
      let timers = [];
      sandbox.stub(global, 'setTimeout').callsFake(fn => timers.push(fn));
      mochaAdapter();
      let runner = new Runner();

      runner.emit('test end', { state: 'passed', title: 'deferred' });
      global.Testem.aborted = true;
      timers.forEach(fn => fn());
      runner.emit('fail', { title: 'late' }, { message: 'x' });
      runner.emit('end', {});

      expect(emit.withArgs('test-result')).not.to.have.been.called();
      expect(emit.withArgs('all-test-results')).to.have.been.calledOnce();
    });

    it('jasmine2 suppresses events once aborted', function() {
      let jasmineReporter;
      global.jasmine = { getEnv: () => ({ addReporter: r => { jasmineReporter = r; } }) };
      jasmine2Adapter();

      global.Testem.aborted = true;
      jasmineReporter.specStarted({ fullName: 'a' });
      jasmineReporter.specDone({ id: 1, fullName: 'a', status: 'failed', failedExpectations: [] });
      jasmineReporter.jasmineDone();

      expect(emit.withArgs('test-result')).not.to.have.been.called();
      expect(emit.withArgs('tests-start')).not.to.have.been.called();
      expect(emit.withArgs('all-test-results')).to.have.been.calledOnce();
    });

    it('qunit suppresses events and clears its queue once aborted', function() {
      let hooks = {};
      global.QUnit = { config: { queue: [1, 2, 3] } };
      ['log', 'testStart', 'testDone', 'done'].forEach(name => {
        global.QUnit[name] = fn => { hooks[name] = fn; };
      });
      qunitAdapter();

      hooks.testStart({ name: 'first' });
      global.Testem.aborted = true;
      hooks.testDone({ failed: 1 });
      hooks.done({});

      expect(emit.withArgs('test-result')).not.to.have.been.called();
      expect(emit.withArgs('all-test-results')).to.have.been.calledOnce();
      expect(global.QUnit.config.queue).to.have.length(0);
    });
  });
});
