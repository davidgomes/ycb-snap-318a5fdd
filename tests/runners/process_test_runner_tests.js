'use strict';

var expect = require('chai').expect;
var path = require('path');

var Config = require('../../lib/config');
var Launcher = require('../../lib/launcher.js');
var ProcessTestRunner = require('../../lib/runners/process_test_runner');

var FakeReporter = require('../support/fake_reporter');

describe('ProcessTestRunner', function() {
  var reporter, config;

  beforeEach(function() {
    reporter = new FakeReporter();
    config = new Config('ci', {
      reporter: reporter
    });
  });

  describe('abort', function() {
    var runner;

    beforeEach(function() {
      var settings = {
        exe: 'node',
        args: [path.join(__dirname, '../fixtures/processes/just-running.js')]
      };
      var launcher = new Launcher('node-running', settings, config);
      runner = new ProcessTestRunner(launcher, reporter);
    });

    it('kills the process, finishes the run and suppresses its result', function() {
      var started = new Promise(function(resolve) {
        runner.launcher.processCtl.once('processStarted', resolve);
      });
      var run = runner.start();

      return started.then(function() {
        var first = runner.abort();
        expect(runner.abort()).to.equal(first);
        return Promise.all([first, run]);
      }).then(function() {
        expect(runner.aborted).to.equal(true);
        expect(reporter.results).to.deep.equal([]);
      });
    });

    it('resolves when the runner was never started', function() {
      return runner.abort().then(function() {
        expect(reporter.results).to.deep.equal([]);
      });
    });
  });

  it('calls onStart & onEnd', function(done) {
    var settings = {
      exe: 'node',
      args: [path.join(__dirname, '../fixtures/processes/stdout.js')]
    };
    var launcher = new Launcher('node-stdout', settings, config);
    var runner = new ProcessTestRunner(launcher, reporter);

    var startCalled = false;
    reporter.onStart = function(name, opts) {
      expect(name).to.equal('node-stdout');
      expect(opts).to.deep.equal({ launcherId: launcher.id });
      startCalled = true;
    };
    var endCalled = false;
    reporter.onEnd = function(name, opts) {
      expect(name).to.equal('node-stdout');
      expect(opts).to.deep.equal({ launcherId: launcher.id });
      endCalled = true;
    };
    runner.start(function() {
      expect(startCalled).to.equal(true);
      expect(endCalled).to.equal(true);
      done();
    });
  });

  it('reads stdout into messages', function(done) {
    var settings = {
      exe: 'node',
      args: [path.join(__dirname, '../fixtures/processes/stdout.js')]
    };
    var launcher = new Launcher('node-stdout', settings, config);
    var runner = new ProcessTestRunner(launcher, reporter);

    runner.start(function() {
      expect(reporter.results).to.deep.equal([{
        result: {
          launcherId: launcher.id,
          logs: [{
            text: 'foobar',
            type: 'log'
          }],
          name: 'error',
          failed: 0,
          passed: 1,
          testContext: {},
        }
      }]);
      done();
    });
  });

  it('handles failing processes', function(done) {
    var settings = {
      exe: 'node',
      args: [path.join(__dirname, '../fixtures/processes/stderr.js')]
    };
    var launcher = new Launcher('node-stderr', settings, config);
    var runner = new ProcessTestRunner(launcher, reporter);

    runner.start(function() {
      expect(reporter.results).to.deep.equal([{
        result: {
          launcherId: launcher.id,
          logs: [{
            text: 'Non-zero exit code: 1',
            type: 'error'
          },
          {
            text: 'foobar',
            type: 'error'
          }],
          name: 'error',
          failed: 1,
          passed: 0,
          testContext: {},
          error: {
            message: 'Non-zero exit code: 1\nStderr: \n foobar\n'
          }
        }
      }]);
      done();
    });
  });

  it('handles non existing processes', function(done) {
    var settings = {
      exe: 'nope-not-existing'
    };
    var launcher = new Launcher('nope-fail', settings, config);
    var runner = new ProcessTestRunner(launcher, reporter);

    runner.start(function() {
      var expectedMessage = runner.lastErr + '\n';

      if (runner.lastStderr) {
        expectedMessage += 'Stderr: \n ' + runner.lastStderr + '\n';
      }

      var expectedLogs = [{
        text: runner.lastErr.toString(),
        type: 'error'
      }];

      if (runner.lastStderr) {
        expectedLogs.push({
          text: runner.lastStderr,
          type: 'error'
        });
      }

      expect(reporter.results).to.deep.equal([{
        result: {
          launcherId: launcher.id,
          logs: expectedLogs,
          name: 'error',
          failed: 1,
          passed: 0,
          testContext: {},
          error: {
            message: expectedMessage
          }
        }
      }]);
      done();
    });
  });
});
