'use strict';

const Bluebird = require('bluebird');
const expect = require('chai').expect;
const sinon = require('sinon');
const tmp = require('tmp');
const fs = require('fs');
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

  describe('bail_on_test_failure', function() {
    const log = require('npmlog');

    function bailApp(bailValue, subReporter) {
      return {
        config: {
          get: function(key) {
            switch (key) {
              case 'reporter':
                return subReporter || new FakeReporter();
              case 'bail_on_test_failure':
                return bailValue;
            }
          }
        }
      };
    }

    const fail = name => ({ name: name, passed: false });
    const pass = name => ({ name: name, passed: true });

    it('is disabled by default', function() {
      let reporter = new Reporter(bailApp(undefined), stream);

      reporter.report('chrome', fail('a'));
      reporter.report('chrome', fail('b'));

      expect(reporter.hasBailed()).to.be.false();
      expect(reporter.bailOnTestFailure).to.be.false();
    });

    it('bails on the first failure when true', function() {
      let sub = new FakeReporter();
      let reporter = new Reporter(bailApp(true, sub), stream);
      let onFailure = sinon.spy();
      reporter.on('test-failure', onFailure);

      reporter.report('chrome', pass('a'));
      reporter.report('chrome', fail('b'));

      expect(reporter.hasBailed()).to.be.true();
      expect(reporter.bailReason).to.equal('b');
      expect(onFailure).to.have.been.calledOnceWith('chrome', sinon.match({ name: 'b' }));
      expect(sub.results).to.have.lengthOf(2);
    });

    it('bails on the Nth failure and ignores skipped and todo tests', function() {
      let reporter = new Reporter(bailApp(2), stream);

      reporter.report('chrome', fail('a'));
      reporter.report('chrome', { name: 'skipped', skipped: true });
      reporter.report('chrome', { name: 'todo', passed: false, todo: true });
      expect(reporter.hasBailed()).to.be.false();

      reporter.report('firefox', fail('b'));
      expect(reporter.hasBailed()).to.be.true();
      expect(reporter.bailReason).to.equal('b');
    });

    it('gates results reported after the bail from sub-reporters', function() {
      let sub = new FakeReporter();
      let reporter = new Reporter(bailApp(1, sub), stream);
      let onFailure = sinon.spy();
      reporter.on('test-failure', onFailure);

      reporter.report('chrome', fail('a'));
      reporter.report('chrome', fail('b'));
      reporter.report('chrome', pass('c'));

      expect(sub.results).to.have.lengthOf(1);
      expect(onFailure).to.have.been.calledOnce();
      expect(reporter.getBailReport().suppressedAfterBail).to.equal(2);
    });

    it('exposes a bail report', function() {
      let reporter = new Reporter(bailApp(2), stream);

      expect(reporter.getBailReport()).to.include({ bailLauncher: null, testsRanBeforeBail: 0 });

      reporter.report('chrome', pass('a'));
      reporter.report('chrome', fail('b'));
      reporter.report('firefox', fail('c'));
      reporter.report('firefox', fail('d'));

      let report = reporter.getBailReport();
      expect(report.testsRanBeforeBail).to.equal(3);
      expect(report.bailLauncher).to.equal('firefox');
      expect(report.failuresByLauncher).to.deep.equal({ chrome: 1, firefox: 1 });
      expect(Object.getPrototypeOf(report.failuresByLauncher)).to.equal(Object.prototype);
      expect(report.failedTests).to.deep.equal(['b', 'c']);
    });

    it('resets bail state so sub-reporters receive results again', function() {
      let sub = new FakeReporter();
      let reporter = new Reporter(bailApp(1, sub), stream);

      reporter.report('chrome', fail('a'));
      reporter.report('chrome', pass('b'));
      reporter.resetBailState();

      expect(reporter.hasBailed()).to.be.false();
      expect(reporter.bailReason).to.be.null();
      expect(reporter.getBailReport()).to.deep.include({
        bailLauncher: null,
        testsRanBeforeBail: 0,
        failuresByLauncher: {},
        failedTests: []
      });

      reporter.report('chrome', pass('c'));
      reporter.report('chrome', fail('d'));

      expect(sub.results.map(r => r.result.name)).to.deep.equal(['a', 'c', 'd']);
      expect(reporter.getBailReport().testsRanBeforeBail).to.equal(2);
      expect(reporter.bailReason).to.equal('d');
    });

    it('only shows post-reset bail activity in TAP output', function() {
      let reporter = new Reporter(bailApp(1, 'tap'), stream);

      reporter.report('chrome', fail('a'));
      reporter.report('chrome', pass('b'));
      reporter.resetBailState();
      reporter.report('chrome', pass('c'));
      reporter.finish();

      let output = stream.read().toString();
      expect(output).to.not.match(/Bail out!/);
      expect(output).to.not.match(/# bailed/);
      expect(output).to.match(/ok 2 chrome - \[undefined ms\] - c/);
    });

    [0, -1, 1.5, '2', 'yes', NaN].forEach(function(value) {
      it(`warns and disables bailing for invalid value ${require('util').inspect(value)}`, function() {
        let warn = sandbox.stub(log, 'warn');
        let reporter = new Reporter(bailApp(value), stream);

        expect(warn).to.have.been.calledWith('bail_on_test_failure', sinon.match.string);
        expect(reporter.bailOnTestFailure).to.be.false();

        reporter.report('chrome', fail('a'));
        expect(reporter.hasBailed()).to.be.false();
      });
    });

    it('does not warn for valid values', function() {
      let warn = sandbox.stub(log, 'warn');

      [true, false, 3, undefined].forEach(value => new Reporter(bailApp(value), stream));

      expect(warn).to.not.have.been.called();
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
});
