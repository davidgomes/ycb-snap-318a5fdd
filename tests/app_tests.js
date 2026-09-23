'use strict';

const expect = require('chai').expect;
const sinon = require('sinon');
const fireworm = require('fireworm');
const Bluebird = require('bluebird');
const path = require('path');

const Config = require('../lib/config');
const App = require('../lib/app');
const RunTimeout = require('../lib/utils/run-timeout');

const FakeReporter = require('./support/fake_reporter');

describe('App', function() {
  let app, config, sandbox;

  beforeEach(function() {
    sandbox = sinon.createSandbox();
  });

  afterEach(function() {
    sandbox.restore();
  });

  describe('triggerRun', function() {
    let finish;
    beforeEach(function(done) {
      config = new Config('dev', {}, {
        reporter: new FakeReporter()
      });
      app = new App(config, function() {
        if (finish) { finish(); }
        else { done(); }
      });
      sandbox.spy(app, 'triggerRun');
      sandbox.spy(app, 'stopRunners');
      sandbox.stub(app, 'singleRun').callsFake(function() {
        return Bluebird.resolve().delay(50);
      });
      app.once('testRun', done);
      app.start();
    });

    afterEach(function(done) {
      finish = done;
      app.exit();
    });

    it('triggers a run on start', function() {
      expect(app.triggerRun.calledWith('Start')).to.be.true();
    });

    it('can only be executed once at the same time', function() {
      app.currentRun = Bluebird.resolve();

      app.triggerRun('one');
      app.triggerRun('two');
      expect(app.stopRunners).to.have.been.calledOnce();
    });
  });

  describe('singleRun', function() {
    let runner;
    beforeEach(function() {
      config = new Config('dev', {}, {
        reporter: new FakeReporter()
      });
      app = new App(config);
      runner = {
        start: function() {
          return Bluebird.resolve().delay(100).then(function() {
            if (this.killed) {
              throw new Error('Killed');
            }

            return;
          }.bind(this));
        },
        exit: function() {
          this.killed = true;

          return Bluebird.resolve();
        }
      };
      app.runners = [runner];

      sandbox.spy(runner, 'start');
      sandbox.spy(runner, 'exit');
    });

    it('times out slow runners', function() {
      return Bluebird.using(RunTimeout.with(0.005), function(timeout) {
        timeout.on('timeout', function() {
          app.killRunners();
        });

        return app.singleRun(timeout);
      }).then(function() {
        expect('Should never be called').to.be.true();
      }, function(err) {
        expect(err.message).to.eq('Killed');
        expect(err.hideFromReporter).not.to.exist();
        expect(runner.start).to.have.been.called();
        expect(runner.exit).to.have.been.called();
      });
    });

    it('doesn\'t start additional runners when timed out', function() {
      return Bluebird.using(RunTimeout.with(0), function(timeout) {
        timeout.on('timeout', function() {
          app.killRunners();
        });
        timeout.setTimedOut();

        return app.singleRun(timeout);
      }).then(function() {
        expect('Should never be called').to.be.true();
      }, function(err) {
        expect(err.message).to.eq('Run timed out.');
        expect(err.hideFromReporter).not.to.exist();
        expect(runner.start).to.not.have.been.called();
        expect(runner.exit).to.have.been.called();
      });
    });

    it('resolves when restarting', function() {
      app.restarting = true;

      return Bluebird.using(RunTimeout.with(app.config.get('timeout')), function(timeout) {
        timeout.on('timeout', function() {
          app.killRunners();
        });
        return app.singleRun(timeout);
      }).then(function() {
        expect(runner.start).to.not.have.been.called();
        expect(runner.exit).to.not.have.been.called();
      });
    });

    it('rejects when exiting', function() {
      app.exited = true;

      return Bluebird.using(RunTimeout.with(app.config.get('timeout')), function(timeout) {
        timeout.timedOut = true;
        timeout.on('timeout', function() {
          app.killRunners();
        });
        return app.singleRun(timeout);
      }).then(function() {
        expect('Should never be called').to.be.true();
      }, function(err) {
        expect(err.message).to.eq('Run canceled.');
        expect(err.hideFromReporter).to.be.true();
        expect(runner.start).to.not.have.been.called();
        expect(runner.exit).to.not.have.been.called();
      });
    });
  });

  describe('pause running', function() {
    beforeEach(function(done) {
      config = new Config('dev', {}, {
        reporter: new FakeReporter()
      });
      app = new App(config, function() {});
      app.start(done);
    });

    afterEach(function(done) {
      app.exit(null, done);
    });

    it('starts off not paused', function() {
      expect(app.paused).to.be.false();
    });

    it('doesn\'t run tests when reset and paused', function() {
      app.paused = true;
      let runHook = sandbox.spy(app, 'runHook');

      return app.runTests().then(function() {
        expect(runHook.called).to.be.false();
      });
    });

    it('runs tests when reset and not paused', function() {
      let runHook = sandbox.spy(app, 'runHook');

      return app.runTests().then(function() {
        expect(runHook.called).to.be.true();
      });
    });
  });

  describe('file watching', function() {
    beforeEach(function() {
      sandbox.stub(Config.prototype, 'readConfigFile').callsFake(function(file, cb) {
        cb();
      });
    });

    it('adds a watch', function(done) {
      let add = sandbox.spy(fireworm.prototype, 'add');
      let srcFiles = ['test.js'];
      config = new Config('dev', {}, {
        src_files: srcFiles,
        reporter: new FakeReporter()
      });
      app = new App(config, function() {
        done();
      });
      app.start(function() {
        expect(add.getCall(0).args[0]).to.eq(srcFiles);
        app.exit();
      });
    });

    it('triggers a test run on change', function(done) {
      let srcFiles = ['test.js'];
      config = new Config('dev', {}, {
        src_files: srcFiles,
        reporter: new FakeReporter()
      });
      app = new App(config, function() {
        done();
      });
      app.start(function() {
        sandbox.spy(app, 'triggerRun');
        app.fileWatcher.onFileChanged.call(app.fileWatcher, 'test.js');
        expect(app.triggerRun.calledWith('File changed: test.js')).to.be.true();
        app.exit();
      });
    });

    it('creates no watcher', function(done) {
      config = new Config('dev', {}, {
        src_files: ['test.js'],
        disable_watching: true,
        reporter: new FakeReporter()
      });
      app = new App(config, function() {
        done();
      });
      app.start(function() {
        expect(app.fileWatcher).to.eq(undefined);
        app.exit();
      });
    });
  });

  describe('start', function() {
    let finish;
    let onExitCb;
    let onExitFinished;

    beforeEach(function() {
      onExitFinished = false;
      onExitCb = sinon.stub().callsFake(function(config, data, callback) {
        setTimeout(function() {
          callback(null);
          onExitFinished = true;
        }, 10);
      });
      config = new Config('dev', {}, {
        reporter: new FakeReporter(),
        on_exit: onExitCb
      });
      app = new App(config, function() {
        expect(onExitCb.called).to.be.true();
        expect(onExitFinished).to.be.true();
        finish();
      });
      app.once('testRun', app.exit);
    });

    it('calls on_exit hook on success', function(done) {
      finish = done;
      sandbox.stub(app, 'waitForTests').usingPromise(Bluebird.Promise).resolves();
      app.start();
    });

    it('calls on_exit hook on failure and waits for it to finish', function(done) {
      finish = done;
      sandbox.stub(app, 'waitForTests').usingPromise(Bluebird.Promise).rejects();
      app.start();
    });
  });

  describe('abortRunners', function() {
    let runners;

    beforeEach(function() {
      config = new Config('dev', {}, {
        reporter: new FakeReporter()
      });
      app = new App(config);
      runners = [
        { abort: sandbox.stub().usingPromise(Bluebird.Promise).resolves() },
        { abort: sandbox.stub().usingPromise(Bluebird.Promise).resolves() },
        { exit: sandbox.stub() }
      ];
      app.runners = runners;
      sandbox.stub(app.server, 'broadcastAbort');
    });

    it('broadcasts the abort and aborts all runners', function() {
      return app.abortRunners().then(function() {
        expect(app.server.broadcastAbort).to.have.been.calledOnce();
        expect(runners[0].abort).to.have.been.calledOnce();
        expect(runners[1].abort).to.have.been.calledOnce();
        expect(runners[2].exit).not.to.have.been.called();
      });
    });

    it('is idempotent', function() {
      let first = app.abortRunners();
      let second = app.abortRunners();

      expect(second).to.equal(first);
      expect(app.server.broadcastAbort).to.have.been.calledOnce();
      expect(runners[0].abort).to.have.been.calledOnce();
      return second;
    });

    it('does not start runners once aborted', function() {
      let runner = { start: sandbox.stub().usingPromise(Bluebird.Promise).resolves() };
      app.runners = [runner];
      app.abortRunners();

      return Bluebird.using(RunTimeout.with(0), function(timeout) {
        return app.singleRun(timeout);
      }).then(function() {
        expect(runner.start).not.to.have.been.called();
      });
    });
  });

  describe('resetBailState', function() {
    beforeEach(function() {
      config = new Config('dev', {}, {
        reporter: new FakeReporter()
      });
      app = new App(config);
      app.reporter = { resetBailState: sandbox.spy() };
      app.runners = [{ abort: sandbox.stub().usingPromise(Bluebird.Promise).resolves() }];
      sandbox.stub(app.server, 'broadcastAbort');
      sandbox.spy(app.server, 'resetAbort');
    });

    it('resets the reporter, abort tracking and the server broadcast state', function() {
      app.abortRunners();
      app.resetBailState();

      expect(app.reporter.resetBailState).to.have.been.calledOnce();
      expect(app.server.resetAbort).to.have.been.calledOnce();

      app.abortRunners();
      expect(app.server.broadcastAbort).to.have.been.calledTwice();
      expect(app.runners[0].abort).to.have.been.calledTwice();
    });

    it('works before the reporter is created', function() {
      delete app.reporter;

      expect(() => app.resetBailState()).not.to.throw();
      expect(app.server.resetAbort).to.have.been.calledOnce();
    });
  });

  describe('bailing on test failure', function() {
    afterEach(function(done) {
      app.exit(null, function() {
        done();
      });
    });

    it('aborts the runners when the reporter bails', function(done) {
      config = new Config('dev', {}, {
        reporter: new FakeReporter()
      });
      app = new App(config, function() {});
      app.start(function() {
        sandbox.stub(app, 'abortRunners');

        app.reporter.emit('test-failure', 'Chrome', { name: 'failed test' });

        expect(app.abortRunners).to.have.been.calledOnce();
        done();
      });
    });

    it('resets the bail state when a new run starts after an abort', function(done) {
      config = new Config('dev', {}, {
        reporter: new FakeReporter()
      });
      app = new App(config, function() {});
      app.start(function() {
        app.abortRunners();
        sandbox.spy(app, 'resetBailState');

        app.runTests().then(function() {
          expect(app.resetBailState).to.have.been.calledOnce();
          expect(app.runnersAborted).to.be.false();
          done();
        }).catch(done);
      });
    });
  });

  describe('bail_on_test_failure in ci mode', function() {
    it('stops the run on the first failure and exits with a bail error', function(done) {
      let reporter = new FakeReporter();
      let tapFailures = {
        exe: 'node',
        args: [path.join(__dirname, 'fixtures/processes/tap-failures.js')],
        protocol: 'tap'
      };
      config = new Config('ci', {
        port: 0,
        cwd: path.join('tests/fixtures/basic_test'),
        launchers: {
          FirstTap: tapFailures,
          SecondTap: tapFailures
        },
        launch_in_ci: ['FirstTap', 'SecondTap'],
        bail_on_test_failure: true,
        reporter: reporter
      });

      app = new App(config, function(exitCode) {
        try {
          expect(exitCode).to.equal(1);
          expect(reporter.results.map(r => r.result.name)).to.deep.equal(['first failure']);
          expect(app.reporter.hasBailed()).to.be.true();
          expect(app.reporter.getBailReport().bailLauncher).to.equal('FirstTap');
          expect(app.getExitCode().message).to.equal('Bail out! first failure (1 test ran before bail)');
          done();
        } catch (e) {
          done(e);
        }
      });
      app.start();
    });
  });

  describe('onBrowserRelogin', function() {
    let tryAttachCalled;

    beforeEach(function() {
      config = new Config('dev', {}, {
        reporter: new FakeReporter()
      });
      app = new App(config);
      tryAttachCalled = false;
      app.runners = [
        {
          launcherId: 1,
          socket: {},
          tryAttach: () => {
            tryAttachCalled = true;
          },
          clearTimeouts: () => { }
        },
        {
          launcherId: 2,
          socket: null,
          tryAttach: () => {
            tryAttachCalled = true;
          },
          clearTimeouts: () => { }
        },
        {
          launcherId: 3,
          tryAttach: () => {
            tryAttachCalled = true;
          },
          clearTimeouts: () => { }
        }
      ];
    });

    it('does not call tryAttach for an existing browser with existing socket', function() {
      app.onBrowserRelogin('fakeBrowser', 1, {});
      expect(tryAttachCalled).to.be.false();
    });

    it('calls tryAttach for an existing browser with null socket', function() {
      app.onBrowserRelogin('fakeBrowser', 2, {});
      expect(tryAttachCalled).to.be.true();
    });
  });
});
