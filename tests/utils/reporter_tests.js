'use strict';

const Bluebird = require('bluebird');
const expect = require('chai').expect;
const sinon = require('sinon');
const tmp = require('tmp');
const fs = require('fs');
const log = require('npmlog');
const EventEmitter = require('events').EventEmitter;
const PassThrough = require('stream').PassThrough;

const tmpNameAsync = Bluebird.promisify(tmp.tmpName);

const Reporter = require('../../lib/utils/reporter');
const FakeReporter = require('../support/fake_reporter');
const TapReporter = require('../../lib/reporters/tap_reporter');
const XUnitReporter = require('../../lib/reporters/xunit_reporter');

const fsReadFileAsync = Bluebird.promisify(fs.readFile);
const fsUnlinkAsync = Bluebird.promisify(fs.unlink);

describe('Reporter', function() {
  function mockApp(reporter) {
    reporter = reporter || new FakeReporter();

    return {
      config: {
        get: function(key) {
          switch (key) {
            case 'reporter':
              return reporter;
          }
        }
      }
    };
  }

  let sandbox, stream;

  beforeEach(function() {
    sandbox = sinon.createSandbox();
    stream = new PassThrough();
  });

  afterEach(function() {
    sandbox.restore();
  });

  describe('"new"', function() {
    it('can report to a file', function() {
      let close;
      tmpNameAsync().then(function(path) {
        return new Reporter(mockApp(), stream, path);
      }).then(function(reporter) {
        expect(reporter.reportFile).to.exist();

        close = sandbox.spy(reporter.reportFile, 'close');

        return reporter.close();
      }).then(function() {
        expect(close).to.have.been.called();
      });
    });

    // Regresses https://github.com/testem/testem/issues/900
    it('uses file stream when reporting', function() {
      let tapReporterSpy = sandbox.spy(require('../../lib/reporters'), 'tap');
      let reporter = new Reporter(mockApp('tap'), stream, 'report.xml');

      expect(reporter.reportFile).to.not.be.undefined();

      sinon.assert.calledWithMatch(tapReporterSpy,
        sinon.match.any,
        sinon.match.same(reporter.reportFile.outputStream),
        sinon.match.any,
        sinon.match.any);

      return reporter.close().then(function() {
        return fsUnlinkAsync('report.xml');
      });
    });
  });

  describe('"with"', function() {
    let app = mockApp();

    it('can be used as a disposable which returns a reporter', function() {
      return Bluebird.using(Reporter.with(app, stream), function(reporter) {
        expect(reporter).to.be.an.instanceof(Reporter);
      });
    });

    it('closes the reporter when done', function() {
      let close;
      return Bluebird.using(Reporter.with(app, stream), function(reporter) {
        close = sandbox.spy(reporter, 'close');
      }).then(function() {
        expect(close).to.have.been.called();
      });
    });

    it('closes the reporter when promise is rejected with error hidden from the reporter', function() {
      let close;
      return Bluebird.using(Reporter.with(app, stream), function(reporter) {
        close = sandbox.spy(reporter, 'close');

        let mockError = new Error('Not all tests passed.');
        mockError.hideFromReporter = true;
        return Bluebird.reject(mockError);
      }).catch(function() {
        expect(close).to.have.been.called();
      });
    });

    it('logs an error when the wrapped promise was rejected', function() {
      let report;

      return Bluebird.using(Reporter.with(app, stream), function(reporter) {
        report = sandbox.spy(reporter, 'report');
        return Bluebird.reject(new Error('Tests failed.'));
      }).catch(function() {
        expect(report).to.have.been.calledWith(null, {
          error: { message: 'Tests failed.' }, name: 'Error', passed: false
        });
      });
    });
  });

  describe('new', function() {
    it('creates a reporter and writes to stream', function() {
      let reporter = new Reporter({
        config: {
          get: function(key) {
            switch (key) {
              case 'reporter':
                return 'tap';
            }
          }
        }
      }, stream);

      expect(reporter.reporters.length).to.eq(1);

      reporter.report('phantomjs', {
        name: 'it does <cool> "cool" \'cool\' stuff',
        passed: true
      });
      reporter.finish();

      let output = stream.read().toString();
      expect(output).to.match(/tests 1/);
    });

    it('creates two reporters and writes to stream and path when path provided', function() {
      return tmpNameAsync().then(function(path) {
        let stream = new PassThrough();
        let reporter = new Reporter({
          config: {
            get: function(key) {
              switch (key) {
                case 'reporter':
                  return 'tap';
              }
            }
          }
        }, stream, path);

        reporter.report('phantomjs', {
          name: 'it does <cool> "cool" \'cool\' stuff',
          passed: true
        });

        reporter.finish();

        return reporter.close().then(function() {
          let output = stream.read().toString();
          expect(output).to.match(/tests 1/);

          return fsReadFileAsync(path, 'utf-8');
        }).then(function(output) {
          expect(output).to.match(/tests 1/);
        });
      });
    });

    it('creates two reporters in dev mode if path is present and 2nd reporter is tap', function() {
      return tmpNameAsync().then(function(path) {
        let stream = new PassThrough();
        let reporter = new Reporter({
          config: {
            appMode: 'dev',
            get: function(key) {
              switch (key) {
                case 'reporter':
                  return FakeReporter;
                case 'path':
                  return 'dev';
                case 'url':
                  return 'abc';
              }
            }
          },
          on: () => {},
        }, stream, path);

        expect(reporter.reporters).to.have.lengthOf(2);
        expect(reporter.reporters[0]).to.be.an.instanceof(FakeReporter);
        expect(reporter.reporters[1]).to.be.an.instanceof(TapReporter);
      });
    });

    it('creates two reporters in dev mode if path is present and 2nd reporter is dev_mode_file_reporter', function() {
      return tmpNameAsync().then(function(path) {
        let stream = new PassThrough();
        let reporter = new Reporter({
          config: {
            appMode: 'dev',
            get: function(key) {
              switch (key) {
                case 'reporter':
                  return FakeReporter;
                case 'path':
                  return 'dev';
                case 'dev_mode_file_reporter':
                  return 'xunit';
                case 'url':
                  return 'abc';
              }
            }
          },
          on: () => {},
        }, stream, path);

        expect(reporter.reporters).to.have.lengthOf(2);
        expect(reporter.reporters[0]).to.be.an.instanceof(FakeReporter);
        expect(reporter.reporters[1]).to.be.an.instanceof(XUnitReporter);
      });
    });

    it('creates a reporter when custom reporter dependent on configs is provided', function() {
      class CustomReporter extends TapReporter {
      }

      let config = { get: sinon.stub() };
      config.get.withArgs('reporter').returns(CustomReporter);
      config.get.withArgs('tap_quiet_logs').returns(true);
      let app = { config: config };
      let reporter = new Reporter(app, stream);

      expect(reporter).to.be.ok();
      expect(reporter.reporters.length).to.equal(1);
      expect(reporter.reporters[0].quietLogs).to.be.true();
    });

    it('writes xml to stream and file with xunit reporter and intermediate output is enabled', function() {
      return tmpNameAsync().then(function(path) {
        let stream = new PassThrough();
        let reporter = new Reporter({
          config: {
            get: function(key) {
              switch (key) {
                case 'reporter':
                  return 'xunit';
                case 'xunit_intermediate_output':
                  return false;
              }
            }
          }
        }, stream, path);

        reporter.report('phantomjs', {
          name: 'it does <cool> "cool" \'cool\' stuff',
          passed: true
        });
        reporter.finish();

        return reporter.close().then(function() {
          let output = stream.read().toString();
          expect(output).to.match(/<testsuite name/);

          return fsReadFileAsync(path, 'utf-8');
        }).then(function(output) {
          expect(output).to.match(/<testsuite name/);
        });
      });
    });

    it('writes tap to stream and xml to file with xunit reporter intermediate output is enabled', function() {
      return tmpNameAsync().then(function(path) {
        let stream = new PassThrough();
        let reporter = new Reporter({
          config: {
            get: function(key) {
              switch (key) {
                case 'reporter':
                  return 'xunit';
                case 'xunit_intermediate_output':
                  return true;
              }
            }
          }
        }, stream, path);

        reporter.report('phantomjs', {
          name: 'it does <cool> "cool" \'cool\' stuff',
          passed: true
        });
        reporter.finish();

        return reporter.close().then(function() {
          let output = stream.read().toString();
          expect(output).to.match(/tests 1/);

          return fsReadFileAsync(path, 'utf-8');
        }).then(function(output) {
          expect(output).to.match(/<testsuite name/);
        });
      });
    });
  });

  describe('hasPassed', function() {
    let app = mockApp();
    let reporter;

    beforeEach(function() {
      reporter = new Reporter(app, stream);
    });

    it('returns true when all tests passed', function() {
      reporter.report('test', { passed: 1 });

      expect(reporter.hasPassed()).to.be.true();
    });

    it('returns true when all tests skipped', function() {
      let reporter = new Reporter(app, stream);

      reporter.report('test', { skipped: 1 });

      expect(reporter.hasPassed()).to.be.true();
    });

    it('returns true when all tests skipped or passed', function() {
      let reporter = new Reporter(app, stream);

      reporter.report('test', { passed: 1 });
      reporter.report('test', { skipped: 1 });

      expect(reporter.hasPassed()).to.be.true();
    });

    it('returns false when not all passed / skipped', function() {
      let reporter = new Reporter(app, stream);

      reporter.report('test', { passed: 1 });
      reporter.report('test', { skipped: 1 });
      reporter.report('test', { });

      expect(reporter.hasPassed()).to.be.false();
    });
  });

  describe('hasTests', function() {
    let app = mockApp();
    let reporter;

    beforeEach(function() {
      reporter = new Reporter(app, stream);
    });

    it('returns false without reported tests', function() {
      let reporter = new Reporter(app, stream);

      expect(reporter.hasTests()).to.be.false();
    });

    it('returns true when tests were reported', function() {
      reporter.report('test', {});

      expect(reporter.hasTests()).to.be.true();
    });
  });

  describe('bail_on_test_failure', function() {
    function bailApp(bailOnTestFailure, subReporter) {
      return {
        config: {
          get: function(key) {
            switch (key) {
              case 'reporter':
                return subReporter || new FakeReporter();
              case 'bail_on_test_failure':
                return bailOnTestFailure;
            }
          }
        }
      };
    }

    function fail(name) {
      return { name: name, passed: false, error: { message: name + ' failed' } };
    }

    function pass(name) {
      return { name: name, passed: true };
    }

    describe('config validation', function() {
      let warn;

      beforeEach(function() {
        warn = sandbox.stub(log, 'warn');
      });

      it('is disabled by default', function() {
        let reporter = new Reporter(bailApp(undefined), stream);

        reporter.report('Chrome', fail('a'));
        reporter.report('Chrome', fail('b'));

        expect(reporter.hasBailed()).to.be.false();
        expect(warn).not.to.have.been.called();
      });

      it('is disabled when false without warning', function() {
        let reporter = new Reporter(bailApp(false), stream);

        reporter.report('Chrome', fail('a'));

        expect(reporter.hasBailed()).to.be.false();
        expect(warn).not.to.have.been.called();
      });

      it('bails on the first failure when true', function() {
        let reporter = new Reporter(bailApp(true), stream);

        reporter.report('Chrome', fail('a'));

        expect(reporter.hasBailed()).to.be.true();
      });

      it('bails on the Nth failure for a positive integer N', function() {
        let reporter = new Reporter(bailApp(3), stream);

        reporter.report('Chrome', fail('a'));
        reporter.report('Chrome', fail('b'));
        expect(reporter.hasBailed()).to.be.false();

        reporter.report('Chrome', fail('c'));
        expect(reporter.hasBailed()).to.be.true();
        expect(reporter.bailReason).to.equal('c');
      });

      [0, -1, 1.5, '2', 'true', NaN].forEach(function(value) {
        it(`warns and disables bailing for ${String(value)} (${typeof value})`, function() {
          let reporter = new Reporter(bailApp(value), stream);

          reporter.report('Chrome', fail('a'));
          reporter.report('Chrome', fail('b'));

          expect(reporter.hasBailed()).to.be.false();
          expect(warn).to.have.been.calledOnce();
          expect(warn).to.have.been.calledWith('bail_on_test_failure');
        });
      });
    });

    describe('bailing', function() {
      let subReporter, reporter;

      beforeEach(function() {
        subReporter = new FakeReporter();
        reporter = new Reporter(bailApp(2, subReporter), stream);
      });

      it('is an EventEmitter', function() {
        expect(reporter).to.be.an.instanceof(EventEmitter);
      });

      it('does not count skipped or todo results as failures', function() {
        reporter.report('Chrome', { name: 'skipped', passed: false, skipped: true });
        reporter.report('Chrome', { name: 'todo', passed: false, todo: true });
        reporter.report('Chrome', fail('a'));

        expect(reporter.hasBailed()).to.be.false();
        expect(reporter.bailReason).to.be.null();
      });

      it('records the failing test name as bailReason', function() {
        reporter.report('Chrome', fail('a'));
        reporter.report('Firefox', fail('b'));

        expect(reporter.hasBailed()).to.be.true();
        expect(reporter.bailReason).to.equal('b');
      });

      it('emits test-failure once with the launcher name and result when bailing', function() {
        let listener = sinon.spy();
        reporter.on('test-failure', listener);

        let bailingResult = fail('b');
        reporter.report('Chrome', fail('a'));
        expect(listener).not.to.have.been.called();

        reporter.report('Firefox', bailingResult);
        reporter.report('Firefox', fail('c'));

        expect(listener).to.have.been.calledOnce();
        expect(listener).to.have.been.calledWithExactly('Firefox', bailingResult);
      });

      it('forwards the bailing result but gates subsequent results from sub-reporters', function() {
        reporter.report('Chrome', pass('a'));
        reporter.report('Chrome', fail('b'));
        reporter.report('Chrome', fail('c'));
        reporter.report('Chrome', pass('d'));
        reporter.report('Chrome', fail('e'));

        expect(subReporter.results.map(r => r.result.name)).to.deep.equal(['a', 'b', 'c']);
      });

      it('passes bail info to sub-reporters on finish', function() {
        subReporter.reportBail = sinon.spy();

        reporter.report('Chrome', pass('a'));
        reporter.report('Chrome', fail('b'));
        reporter.report('Firefox', fail('c'));
        reporter.report('Chrome', pass('d'));
        reporter.finish();

        expect(subReporter.reportBail).to.have.been.calledOnceWith({
          reason: 'c',
          launcher: 'Firefox',
          failureCount: 2,
          testsRanBeforeBail: 3,
          suppressedAfterBail: 1
        });
      });

      it('does not pass bail info to sub-reporters when it did not bail', function() {
        subReporter.reportBail = sinon.spy();

        reporter.report('Chrome', fail('a'));
        reporter.finish();

        expect(subReporter.reportBail).not.to.have.been.called();
      });
    });

    describe('getBailReport', function() {
      let reporter;

      beforeEach(function() {
        reporter = new Reporter(bailApp(3), stream);
      });

      it('returns an empty report before bailing', function() {
        expect(reporter.hasBailed()).to.be.false();
        expect(reporter.bailReason).to.be.null();
        expect(reporter.getBailReport()).to.deep.equal({
          testsRanBeforeBail: 0,
          bailLauncher: null,
          failuresByLauncher: {},
          failedTests: []
        });
      });

      it('reports failures per launcher and the failed test names', function() {
        reporter.report('Chrome', pass('a'));
        reporter.report('Chrome', fail('b'));
        reporter.report('Firefox', { name: 'skipped', skipped: true });
        reporter.report('Firefox', fail('c'));
        reporter.report('Chrome', fail('d'));
        reporter.report('Firefox', fail('after bail'));

        let report = reporter.getBailReport();

        expect(report).to.deep.equal({
          testsRanBeforeBail: 5,
          bailLauncher: 'Chrome',
          failuresByLauncher: { Chrome: 2, Firefox: 1 },
          failedTests: ['b', 'c', 'd']
        });
        expect(Object.getPrototypeOf(report.failuresByLauncher)).to.equal(Object.prototype);
      });

      it('returns copies of the tracked state', function() {
        reporter.report('Chrome', fail('a'));

        let report = reporter.getBailReport();
        report.failedTests.push('mutated');
        report.failuresByLauncher.Chrome = 99;

        expect(reporter.getBailReport().failedTests).to.deep.equal(['a']);
        expect(reporter.getBailReport().failuresByLauncher).to.deep.equal({ Chrome: 1 });
      });
    });

    describe('resetBailState', function() {
      it('clears all bail state', function() {
        let reporter = new Reporter(bailApp(1), stream);

        reporter.report('Chrome', fail('a'));
        reporter.report('Chrome', fail('b'));
        reporter.resetBailState();

        expect(reporter.hasBailed()).to.be.false();
        expect(reporter.bailReason).to.be.null();
        expect(reporter.getBailReport()).to.deep.equal({
          testsRanBeforeBail: 0,
          bailLauncher: null,
          failuresByLauncher: {},
          failedTests: []
        });
      });

      it('forwards results again and can bail again', function() {
        let subReporter = new FakeReporter();
        let reporter = new Reporter(bailApp(1, subReporter), stream);
        let listener = sinon.spy();
        reporter.on('test-failure', listener);

        reporter.report('Chrome', fail('a'));
        reporter.report('Chrome', pass('gated'));
        reporter.resetBailState();
        reporter.report('Firefox', pass('b'));
        reporter.report('Firefox', fail('c'));

        expect(subReporter.results.map(r => r.result.name)).to.deep.equal(['b', 'c']);
        expect(listener).to.have.been.calledTwice();
        expect(reporter.bailReason).to.equal('c');
        expect(reporter.getBailReport()).to.deep.equal({
          testsRanBeforeBail: 2,
          bailLauncher: 'Firefox',
          failuresByLauncher: { Firefox: 1 },
          failedTests: ['c']
        });
      });

      it('makes sub-reporter output reflect only post-reset activity', function() {
        let reporter = new Reporter(bailApp(1, 'tap'), stream);

        reporter.report('Chrome', fail('a'));
        reporter.report('Chrome', pass('gated'));
        reporter.resetBailState();
        stream.read();

        reporter.report('Chrome', pass('b'));
        reporter.finish();

        let output = stream.read().toString();
        expect(output).to.contain('ok 1 Chrome - [undefined ms] - b');
        expect(output).to.match(/# tests 1\n/);
        expect(output).to.match(/# pass {2}1\n/);
        expect(output).to.match(/# ok/);
        expect(output).not.to.contain('Bail out!');
        expect(output).not.to.contain('# bailed');
      });
    });

    it('writes the bail summary to the TAP output', function() {
      let reporter = new Reporter(bailApp(1, 'tap'), stream);

      reporter.report('Chrome', pass('a'));
      reporter.report('Chrome', fail('b'));
      reporter.report('Chrome', pass('c'));
      reporter.report('Chrome', fail('d'));
      reporter.finish();

      let lines = stream.read().toString().split('\n');
      let bailLine = lines.indexOf('Bail out! b (1 test failure)');

      expect(bailLine).to.be.above(-1);
      expect(lines.slice(bailLine + 1)).to.deep.equal([
        '1..2',
        '# tests 2',
        '# pass  1',
        '# skip  0',
        '# todo  0',
        '# fail  1',
        '# bailed',
        '# ran before bail 2',
        '# suppressed 2',
        ''
      ]);
    });
  });
});
