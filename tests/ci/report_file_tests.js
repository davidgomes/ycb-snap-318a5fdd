

const fs = require('fs');
const App = require('../../lib/app');
const Config = require('../../lib/config');
const Bluebird = require('bluebird');
const expect = require('chai').expect;
const rimraf = require('rimraf');
const path = require('path');
const PassThrough = require('stream').PassThrough;
const ReportFile = require('../../lib/utils/report-file');
const tmp = require('tmp');

const FakeReporter = require('../support/fake_reporter');

const tmpDirAsync = Bluebird.promisify(tmp.dir);
const tmpFileAsync = Bluebird.promisify(tmp.file);
const rimrafAsync = Bluebird.promisify(rimraf);

describe('report file output', function() {
  this.timeout(30000);

  let reportDir, filename;
  beforeEach(function() {
    return tmpDirAsync({
      keep: true
    }).then(dir => {
      reportDir = dir;

      return tmpFileAsync({
        dir: dir,
        name: 'test-reports.xml',
        keep: true,
        discardDescriptor: true
      });
    }).then(filePath => {
      filename = filePath;
    });
  });

  afterEach(function() {
    return rimrafAsync(reportDir);
  });

  it('allows passing in report_file from config', function(done) {
    let dir = path.join('tests/fixtures/success-skipped');

    let config = new Config('ci', {
      file: path.join(dir, 'testem.json'),
      port: 0,
      cwd: dir,
      reporter: new FakeReporter(),
      stdout_stream: new PassThrough(),
      report_file: filename,
      launch_in_ci: ['Headless Firefox']
    });

    let app = new App(config, () => {
      expect(app.reportFileName).to.eq(filename);

      // fileStream already closed
      done();
    });
    app.start();
  });

  it('doesn\'t create a file if the report_file parameter is not passed in', function(done) {
    tmp.tmpName((err, filename) => {
      if (err) {
        return done(err);
      }

      let config = new Config('ci', {
        reporter: new FakeReporter(),
        stdout_stream: new PassThrough()
      });
      let app = new App(config, () => {
        fs.stat(filename, err => {
          try {
            expect(err).not.eql(null);
            expect(err.code).to.eq('ENOENT');
          } catch (e) {
            done(e);
          } finally {
            done();
          }
        });
      });
      app.start();
      app.exit();
    });
  });

  it('writes one report file per launcher when report_file uses <launcher>', function(done) {
    let stdout = new PassThrough();
    let perLauncherDir = path.join(reportDir, 'per-launcher');
    let config = new Config('ci', {
      port: 0,
      reporter: 'tap',
      stdout_stream: stdout,
      report_file: path.join(perLauncherDir, '<launcher>.tap'),
      launchers: {
        'Passing Process': { command: 'node -e "process.exit(0)"' },
        'Failing (Process)': { command: 'node -e "process.exit(1)"' }
      },
      launch_in_ci: ['Passing Process', 'Failing (Process)']
    });

    let app = new App(config, exitCode => {
      try {
        expect(exitCode).to.eq(1);
        expect(fs.readdirSync(perLauncherDir).sort()).to.deep.equal(['Failing__Process_.tap', 'Passing_Process.tap']);

        let passing = fs.readFileSync(path.join(perLauncherDir, 'Passing_Process.tap'), 'utf-8');
        expect(passing).to.match(/^ok 1 Passing Process/m);
        expect(passing).to.match(/# tests 1/);
        expect(passing).not.to.contain('Failing');

        let failing = fs.readFileSync(path.join(perLauncherDir, 'Failing__Process_.tap'), 'utf-8');
        expect(failing).to.match(/^not ok 1 Failing \(Process\)/m);
        expect(failing).not.to.contain('Passing');

        expect(stdout.read().toString()).to.match(/# tests 2/);
        done();
      } catch (e) {
        done(e);
      }
    });
    app.start();
  });

  it('fails without creating a file when report_file has an unknown template', function(done) {
    let stdout = new PassThrough();
    let config = new Config('ci', {
      reporter: 'tap',
      stdout_stream: stdout,
      report_file: path.join(reportDir, 'report-<browser>.tap')
    });

    let app = new App(config, exitCode => {
      try {
        expect(exitCode).to.eq(1);
        expect(stdout.read().toString()).to.contain('Unknown template variable "<browser>"');
        expect(fs.readdirSync(reportDir)).to.deep.equal(['test-reports.xml']);
        done();
      } catch (e) {
        done(e);
      }
    });
    app.start();
  });

  it('writes out results to the file', function(done) {
    let reportFile = new ReportFile(filename);
    let reportStream = reportFile.outputStream;

    reportFile.outputStream.on('finish', function() {
      fs.readFile(filename, (err, data) => {
        if (err) {
          return done(err);
        }

        expect(data).to.match(/test data/);
        done();
      });
    });
    reportStream.write('test data');
    reportStream.end();
  });

  it('creates folders in the path if they don\'t exist', function() {
    let name = 'nested/test/folders/test-reports.xml';
    let nestedFilename = path.join(reportDir, name);
    let nestedDir = path.dirname(nestedFilename);
    let filename = path.basename(nestedFilename);

    // tmp.file no longer makes folders in the path for us,
    // so we need to do it ourselves before creating the file
    const mkdirAsync = Bluebird.promisify(fs.mkdir);
    mkdirAsync(nestedDir, { recursive: true })
      .then(function() {
        return tmpFileAsync({
          dir: nestedDir,
          name: filename
        });
      }).then (function() {
        return new Promise(resolve => {
          let reportFile = new ReportFile(nestedFilename);
          reportFile.outputStream.on('finish', () => {
            fs.stat(nestedFilename, resolve);
          });
          reportFile.outputStream.end();
        });
      });
  });
});
