'use strict';

const Bluebird = require('bluebird');
const expect = require('chai').expect;
const sinon = require('sinon');
const PassThrough = require('stream').PassThrough;
const log = require('npmlog');

const Config = require('../lib/config');
const Reporter = require('../lib/utils/reporter');
const App = require('../lib/app');
const Server = require('../lib/server');
const TapReporter = require('../lib/reporters/tap_reporter');
const DotReporter = require('../lib/reporters/dot_reporter');
const TeamcityReporter = require('../lib/reporters/teamcity_reporter');
const XUnitReporter = require('../lib/reporters/xunit_reporter');
const FakeReporter = require('./support/fake_reporter');
const ProcessTestRunner = require('../lib/runners/process_test_runner');
const BrowserTestRunner = require('../lib/runners/browser_test_runner');

function appWith(bail) {
  return {
    config: new Config('ci', {
      reporter: new FakeReporter(),
      bail_on_test_failure: bail
    }),
    abortRunners: sinon.spy(function() {
      return Bluebird.resolve();
    })
  };
}

describe('bail_on_test_failure', function() {
  it('defaults to false', function() {
    let config = new Config('ci', {});
    expect(config.get('bail_on_test_failure')).to.equal(false);
  });

  describe('Reporter validation', function() {
    let warn;

    beforeEach(function() {
      warn = sinon.stub(log, 'warn');
    });

    afterEach(function() {
      warn.restore();
    });

    [0, -1, 1.5, '2', 'true', ''].forEach(function(value) {
      it('warns and disables for ' + JSON.stringify(value), function() {
        let reporter = new Reporter(appWith(value), new PassThrough());
        expect(warn.called).to.equal(true);
        expect(warn.firstCall.args[0]).to.equal('bail_on_test_failure');
        expect(warn.firstCall.args[1]).to.match(/invalid/i);
        reporter.report('chrome', { name: 'a', passed: false });
        expect(reporter.hasBailed()).to.equal(false);
        expect(reporter.getBailReport().bailLauncher).to.equal(null);
      });
    });

    it('treats true as a threshold of one', function() {
      let reporter = new Reporter(appWith(true), new PassThrough());
      expect(warn.called).to.equal(false);
      reporter.report('chrome', { name: 'bad', passed: false });
      expect(reporter.hasBailed()).to.equal(true);
      expect(reporter.bailReason).to.equal('bad');
    });

    it('treats a positive integer as the threshold', function() {
      let reporter = new Reporter(appWith(2), new PassThrough());
      expect(warn.called).to.equal(false);
      reporter.report('chrome', { name: 'bad-1', passed: false });
      expect(reporter.hasBailed()).to.equal(false);
      reporter.report('firefox', { name: 'bad-2', passed: false });
      expect(reporter.hasBailed()).to.equal(true);
      expect(reporter.bailReason).to.equal('bad-2');
      let report = reporter.getBailReport();
      expect(report.bailLauncher).to.equal('firefox');
      expect(report.testsRanBeforeBail).to.equal(2);
      expect(report.failedTests).to.deep.equal(['bad-1', 'bad-2']);
      expect(report.failuresByLauncher).to.deep.equal({ chrome: 1, firefox: 1 });
      expect(Object.getPrototypeOf(report.failuresByLauncher)).to.equal(Object.prototype);
    });
  });

  describe('bail behavior', function() {
    it('ignores skipped and todo failures and emits test-failure once', function() {
      let app = appWith(1);
      let reporter = new Reporter(app, new PassThrough());
      let failure = sinon.spy();
      reporter.on('test-failure', failure);

      reporter.report('chrome', { name: 'skip', passed: false, skipped: true });
      reporter.report('chrome', { name: 'todo', passed: false, todo: true });
      reporter.report('chrome', { name: 'ok', passed: true });
      expect(reporter.hasBailed()).to.equal(false);

      let result = { name: 'boom', passed: false };
      reporter.report('chrome', result);
      expect(failure.calledOnce).to.equal(true);
      expect(failure.firstCall.args[0]).to.equal('chrome');
      expect(failure.firstCall.args[1]).to.equal(result);
      expect(app.abortRunners.calledOnce).to.equal(true);

      reporter.report('chrome', { name: 'later', passed: false });
      expect(reporter.getBailReport().failedTests).to.deep.equal(['boom']);
      expect(reporter.getBailReport().testsRanBeforeBail).to.equal(4);
      expect(reporter.reporters[0].results.map(function(entry) {
        return entry.result.name;
      })).to.deep.equal(['skip', 'todo', 'ok', 'boom']);
    });

    it('resetBailState clears bail state so later results are reported', function() {
      let reporter = new Reporter(appWith(true), new PassThrough());
      reporter.report('chrome', { name: 'boom', passed: false });
      reporter.report('chrome', { name: 'hidden', passed: false });
      reporter.resetBailState();
      expect(reporter.hasBailed()).to.equal(false);
      expect(reporter.bailReason).to.equal(null);
      expect(reporter.getBailReport()).to.deep.equal({
        testsRanBeforeBail: 0,
        bailLauncher: null,
        failuresByLauncher: {},
        failedTests: []
      });
      reporter.report('chrome', { name: 'after', passed: true });
      expect(reporter.reporters[0].results.map(function(entry) {
        return entry.result.name;
      })).to.deep.equal(['after']);
    });
  });

  describe('sub-reporter finish output', function() {
    function bailReporter(Type) {
      let stream = new PassThrough();
      let config = new Config('ci', {
        reporter: Type === TapReporter ? 'tap' : (Type === DotReporter ? 'dot' : (Type === XUnitReporter ? 'xunit' : 'teamcity')),
        bail_on_test_failure: true
      });
      let app = { config: config };
      let reporter = new Reporter(app, stream);
      reporter.report('chrome', { name: 'ok', passed: true, runDuration: 1 });
      reporter.report('chrome', { name: 'bad', passed: false, runDuration: 2, error: { message: 'nope' } });
      reporter.report('chrome', { name: 'later', passed: false, runDuration: 3, error: { message: 'nope' } });
      reporter.finish();
      return stream.read().toString();
    }

    it('prints TAP bail summary', function() {
      let output = bailReporter(TapReporter);
      expect(output).to.contain('Bail out! bad 2');
      expect(output).to.contain('# bailed\n');
      expect(output).to.contain('# ran before bail 2\n');
      expect(output).to.contain('# suppressed 1\n');
      expect(output).to.not.contain('later');
    });

    it('prints Dot bail summary', function() {
      let output = bailReporter(DotReporter);
      expect(output).to.contain('Bail out! bad 2');
      expect(output).to.contain('# bailed\n');
      expect(output).to.contain('# ran before bail 2\n');
      expect(output).to.contain('# suppressed 1\n');
    });

    it('prints TeamCity bail service messages', function() {
      let output = bailReporter(TeamcityReporter);
      expect(output).to.contain('##teamcity[message text=\'Bail out!\' status=\'ERROR\']');
      expect(output).to.contain('##teamcity[buildStatisticValue key=\'bailedTests\' value=\'1\']');
      expect(output).to.contain('##teamcity[buildStatisticValue key=\'testsBeforeBail\' value=\'2\']');
      expect(output).to.contain('##teamcity[buildStatisticValue key=\'suppressedAfterBail\' value=\'1\']');
      expect(output).to.match(/##teamcity\[buildProblem description='bad' identity='bail'\]/);
    });

    it('prints XUnit bail metadata', function() {
      let output = bailReporter(XUnitReporter);
      expect(output).to.contain('errors="1"');
      expect(output).to.contain('<property name="bailReason" value="bad"');
      expect(output).to.contain('<property name="testsBeforeBail" value="2"');
      expect(output).to.contain('<property name="suppressedAfterBail" value="1"');
      expect(output).to.contain('<error message="Bail out! bad"');
      expect(output).to.contain('<system-out>');
      expect(output).to.contain('Bail out! bad');
    });
  });

  describe('abort', function() {
    it('server broadcastAbort is idempotent and tolerates missing io', function() {
      let server = new Server(new Config('ci', {}));
      expect(function() {
        server.broadcastAbort();
      }).not.to.throw();
      let emit = sinon.spy();
      server.io = { emit: emit };
      server.resetAbort();
      server.broadcastAbort();
      server.broadcastAbort();
      expect(emit.calledOnce).to.equal(true);
      expect(emit.firstCall.args).to.deep.equal(['abort-tests']);
    });

    it('app abortRunners broadcasts and aborts runners once', function() {
      let app = new App(new Config('ci', {}));
      let runner = { abort: sinon.spy(function() { return Bluebird.resolve(); }) };
      app.runners = [runner];
      app.server.broadcastAbort = sinon.spy();
      return app.abortRunners().then(function() {
        expect(app.server.broadcastAbort.calledOnce).to.equal(true);
        expect(runner.abort.calledOnce).to.equal(true);
        return app.abortRunners();
      }).then(function() {
        expect(app.server.broadcastAbort.calledOnce).to.equal(true);
        expect(runner.abort.calledOnce).to.equal(true);
      });
    });

    it('app resetBailState clears abort tracking', function() {
      let app = new App(new Config('ci', {}));
      app.reporter = {
        resetBailState: sinon.spy(),
        hasPassed: function() { return true; },
        hasTests: function() { return true; }
      };
      app.server.resetAbort = sinon.spy();
      app._runnersAborted = true;
      app.resetBailState();
      expect(app._runnersAborted).to.equal(false);
      expect(app.reporter.resetBailState.calledOnce).to.equal(true);
      expect(app.server.resetAbort.calledOnce).to.equal(true);
    });

    it('process runner abort is idempotent and suppresses results', function() {
      let reporter = new FakeReporter();
      let calls = 0;
      let runner = new ProcessTestRunner({
        id: 1,
        name: 'node',
        kill: function() { return Bluebird.resolve(); }
      }, reporter);
      runner.process = { kill: function() { return Bluebird.resolve(); } };
      runner.exit = function() { return Bluebird.resolve(); };
      return runner.abort().then(function() {
        calls++;
        runner.onProcessError(new Error('nope'));
        runner.onProcessExit(1);
        expect(reporter.results).to.deep.equal([]);
        return runner.abort();
      }).then(function() {
        expect(calls).to.equal(1);
      });
    });

    it('browser runner abort emits abort-tests once', function() {
      let emit = sinon.spy();
      let runner = new BrowserTestRunner({ id: 1, name: 'Chrome', config: new Config('ci', {}) }, new FakeReporter(), 0, false, new Config('ci', {}));
      runner.socket = { emit: emit };
      runner.pending = true;
      runner.onFinish = function() {};
      return runner.abort().then(function() {
        expect(emit.calledOnce).to.equal(true);
        expect(emit.firstCall.args[0]).to.equal('abort-tests');
        runner.onTestResult({ name: 'x', failed: 1, passed: false });
        expect(runner.reporter.total).to.equal(0);
        return runner.abort();
      }).then(function() {
        expect(emit.calledOnce).to.equal(true);
      });
    });
  });

  describe('getExitCode', function() {
    it('returns a bail error distinct from a normal failure', function() {
      let app = new App(new Config('ci', {}));
      app.reporter = {
        hasBailed: function() { return true; },
        bailReason: 'bad test',
        getBailReport: function() {
          return { testsRanBeforeBail: 3, bailLauncher: 'chrome', failuresByLauncher: { chrome: 1 }, failedTests: ['bad test'] };
        },
        hasPassed: function() { return false; },
        hasTests: function() { return true; }
      };
      let err = app.getExitCode();
      expect(err).to.be.an('error');
      expect(err.message).to.contain('bad test');
      expect(err.message).to.contain('3');
      expect(err.message).to.not.equal('Not all tests passed.');
    });
  });
});
