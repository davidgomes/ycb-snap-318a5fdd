

const Bluebird = require('bluebird');
const expect = require('chai').expect;
const sinon = require('sinon');
const tmp = require('tmp');
const fs = require('fs');
const path = require('path');
const rimraf = require('rimraf');
const PassThrough = require('stream').PassThrough;

const tmpNameAsync = Bluebird.promisify(tmp.tmpName);
const tmpDirAsync = Bluebird.promisify(tmp.dir);

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

  describe('with a <launcher> report_file template', function() {
    let dir;

    function mockConfigApp(options) {
      return {
        config: {
          get: function(key) {
            return options[key];
          }
        }
      };
    }

    function readReport(name) {
      return fs.readFileSync(path.join(dir, name), 'utf-8');
    }

    beforeEach(function() {
      return tmpDirAsync().then(function(tmpDir) {
        dir = tmpDir;
      });
    });

    afterEach(function() {
      rimraf.sync(dir);
    });

    it('writes each launcher to its own file and everything to stdout', function() {
      let reporter = new Reporter(mockConfigApp({ reporter: 'tap' }), stream, path.join(dir, 'report-<launcher>.tap'));

      expect(reporter.reportFile).to.be.undefined();

      reporter.report('Headless Chrome', { name: 'chrome passes', passed: true });
      reporter.report('Firefox', { name: 'firefox fails', passed: false });
      reporter.report('Headless Chrome', { name: 'chrome skips', skipped: true });

      return reporter.close().then(function() {
        let stdout = stream.read().toString();
        expect(stdout).to.contain('chrome passes');
        expect(stdout).to.contain('firefox fails');
        expect(stdout).to.match(/# tests 3/);

        let chrome = readReport('report-Headless_Chrome.tap');
        expect(chrome).to.contain('chrome passes');
        expect(chrome).to.contain('chrome skips');
        expect(chrome).not.to.contain('firefox fails');
        expect(chrome).to.match(/# tests 2/);

        let firefox = readReport('report-Firefox.tap');
        expect(firefox).to.contain('firefox fails');
        expect(firefox).not.to.contain('chrome');
        expect(firefox).to.match(/# tests 1/);
      });
    });

    it('does not create a file for the internal testem launcher', function() {
      let reporter = new Reporter(mockConfigApp({ reporter: 'tap' }), stream, path.join(dir, 'report-<launcher>.tap'));

      reporter.onStart('testem', { launcherId: 0 });
      reporter.report('testem', { name: 'hook failed', passed: false });
      reporter.onEnd('testem', { launcherId: 0 });
      reporter.report('Firefox', { name: 'firefox passes', passed: true });

      return reporter.close().then(function() {
        expect(fs.readdirSync(dir)).to.deep.equal(['report-Firefox.tap']);
        expect(readReport('report-Firefox.tap')).not.to.contain('hook failed');
        expect(stream.read().toString()).to.contain('hook failed');
      });
    });

    it('shares one file between launcher names that sanitize identically', function() {
      let reporter = new Reporter(mockConfigApp({ reporter: 'tap' }), stream, path.join(dir, '<launcher>.tap'));

      reporter.report('a:b', { name: 'first', passed: true });
      reporter.report('a/b', { name: 'second', passed: true });

      return reporter.close().then(function() {
        expect(fs.readdirSync(dir)).to.deep.equal(['a_b.tap']);
        expect(readReport('a_b.tap')).to.match(/# tests 2/);
      });
    });

    it('creates the file when a launcher starts', function() {
      let reporter = new Reporter(mockConfigApp({ reporter: 'tap' }), stream, path.join(dir, '<launcher>.tap'));

      reporter.onStart('Firefox', { launcherId: 1 });

      return reporter.close().then(function() {
        expect(readReport('Firefox.tap')).to.match(/# tests 0/);
      });
    });

    it('only finishes reporters once', function() {
      let reporter = new Reporter(mockConfigApp({ reporter: 'tap' }), stream, path.join(dir, '<launcher>.tap'));

      reporter.report('Firefox', { name: 'firefox passes', passed: true });
      reporter.finish();
      reporter.finish();

      return reporter.close().then(function() {
        expect(stream.read().toString().match(/# tests/g)).to.have.lengthOf(1);
        expect(readReport('Firefox.tap').match(/# tests/g)).to.have.lengthOf(1);
      });
    });

    it('resolves close after every launcher file is written', function() {
      let reporter = new Reporter(mockConfigApp({ reporter: 'xunit' }), stream, path.join(dir, 'nested', '<launcher>.xml'));
      let launchers = ['Chrome', 'Firefox', 'Safari', 'Edge'];

      launchers.forEach(function(launcher) {
        reporter.report(launcher, { name: `${launcher} test`, passed: true });
      });

      return reporter.close().then(function() {
        launchers.forEach(function(launcher) {
          expect(fs.readFileSync(path.join(dir, 'nested', `${launcher}.xml`), 'utf-8')).to.match(/<\/testsuite>/);
        });
      });
    });

    it('names the launcher in per-launcher xunit files', function() {
      let reporter = new Reporter(mockConfigApp({
        reporter: 'xunit',
        xunit_include_launcher_properties: true
      }), stream, path.join(dir, '<launcher>.xml'));

      reporter.report('Headless Chrome', { name: 'chrome passes', passed: true });

      return reporter.close().then(function() {
        let output = readReport('Headless_Chrome.xml');
        expect(output).to.contain('<property name="launcher" value="Headless Chrome"/>');
        expect(output).to.contain('<property name="Headless Chrome_pass" value="1"/>');
      });
    });

    it('writes tap to stdout and xunit files with xunit_intermediate_output', function() {
      let reporter = new Reporter(mockConfigApp({
        reporter: 'xunit',
        xunit_intermediate_output: true
      }), stream, path.join(dir, '<launcher>.xml'));

      reporter.report('Firefox', { name: 'firefox passes', passed: true });

      return reporter.close().then(function() {
        expect(stream.read().toString()).to.match(/# tests 1/);
        expect(readReport('Firefox.xml')).to.match(/<testsuite name/);
      });
    });

    it('uses a single file for date templates without <launcher>', function() {
      let reporter = new Reporter(mockConfigApp({ reporter: 'tap' }), stream, path.join(dir, 'report-<date>.tap'));

      reporter.report('Chrome', { name: 'chrome passes', passed: true });
      reporter.report('Firefox', { name: 'firefox passes', passed: true });

      return reporter.close().then(function() {
        let files = fs.readdirSync(dir);
        expect(files).to.have.lengthOf(1);
        expect(files[0]).to.match(/^report-\d{4}-\d{2}-\d{2}\.tap$/);
        expect(readReport(files[0])).to.match(/# tests 2/);
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
});
