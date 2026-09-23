

const expect = require('chai').expect;
const fs = require('fs');
const os = require('os');
const path = require('path');
const PassThrough = require('stream').PassThrough;

const ReportFile = require('../../lib/utils/report-file');
const Reporter = require('../../lib/utils/reporter');
const Launcher = require('../../lib/launcher');
const Config = require('../../lib/config');
const TapReporter = require('../../lib/reporters/tap_reporter');
const XUnitReporter = require('../../lib/reporters/xunit_reporter');

function fakeConfig(values) {
  return {
    get(key) {
      return values[key];
    }
  };
}

describe('per-launcher report files', function() {
  let tmpDir;

  beforeEach(function() {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'testem-per-launcher-'));
  });

  afterEach(function() {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  describe('ReportFile', function() {
    const date = new Date(2024, 0, 5, 7, 8, 9);

    it('detects templates', function() {
      expect(ReportFile.hasLauncherTemplate('a-<launcher>.xml')).to.be.true();
      expect(ReportFile.hasLauncherTemplate('a.xml')).to.be.false();
      expect(ReportFile.hasLauncherTemplate(undefined)).to.be.false();
      expect(ReportFile.hasDateTemplate('<date>.xml')).to.be.true();
      expect(ReportFile.hasTimestampTemplate('<timestamp>.xml')).to.be.true();
      expect(ReportFile.hasTimestampTemplate('<date>.xml')).to.be.false();
    });

    it('expands launcher, date and timestamp', function() {
      expect(ReportFile.expandPath('r/<launcher>-<date>-<timestamp>.xml', { launcher: 'Chrome 120', date }))
        .to.equal('r/Chrome_120-2024-01-05-2024-01-05_07-08-09.xml');
    });

    it('uses the current date when none is given', function() {
      expect(ReportFile.expandPath('<date>')).to.match(/^\d{4}-\d{2}-\d{2}$/);
    });

    it('sanitizes launcher names', function() {
      expect(ReportFile.sanitizeLauncherName('a/b\\c:d*e?f"g<h>i|j(k)')).to.equal('a_b_c_d_e_f_g_h_i_j_k_');
      expect(ReportFile.sanitizeLauncherName('Headless  \t Chrome')).to.equal('Headless_Chrome');
      expect(ReportFile.sanitizeLauncherName(null)).to.equal('unknown');
      expect(ReportFile.sanitizeLauncherName(undefined)).to.equal('unknown');
    });

    it('expands the path on construction and creates parent directories', function() {
      let reportFile = new ReportFile(path.join(tmpDir, 'nested', '<launcher>.tap'), { launcher: 'Firefox' });
      expect(reportFile.getFilePath()).to.equal(path.join(tmpDir, 'nested', 'Firefox.tap'));
      return reportFile.close().then(() => {
        expect(fs.existsSync(path.join(tmpDir, 'nested', 'Firefox.tap'))).to.be.true();
      });
    });
  });

  describe('Launcher', function() {
    it('provides sanitized names', function() {
      let launcher = new Launcher('Chrome (Headless)', { command: 'echo' }, new Config(null, {}));
      expect(launcher.getSanitizedName()).to.equal('Chrome__Headless_');
      expect(Launcher.sanitizeLauncherName(undefined)).to.equal('unknown');
    });
  });

  describe('Config', function() {
    it('detects templates', function() {
      let config = new Config(null, { report_file: 'out/<launcher>-<date>.xml' });
      expect(config.hasLauncherTemplate()).to.be.true();
      expect(config.hasDateTemplate()).to.be.true();
      expect(config.hasTimestampTemplate()).to.be.false();
      expect(config.hasAnyReportTemplate()).to.be.true();
      expect(new Config(null, { report_file: 'out.xml' }).hasAnyReportTemplate()).to.be.false();
      expect(new Config(null, {}).hasAnyReportTemplate()).to.be.false();
    });

    it('validates report_file templates', function() {
      expect(new Config(null, { report_file: 'out/<launcher>.xml' }).validateReportFile())
        .to.deep.equal({ valid: true, errors: [], warnings: [] });

      let unknown = new Config(null, { report_file: 'out/<browser>.xml' }).validateReportFile();
      expect(unknown.valid).to.be.false();
      expect(unknown.errors).to.have.lengthOf(1);

      let noExt = new Config(null, { report_file: 'out/<launcher>' }).validateReportFile();
      expect(noExt.valid).to.be.true();
      expect(noExt.warnings).to.have.lengthOf(1);
    });

    it('expands report_file', function() {
      expect(new Config(null, {}).getExpandedReportFile('Chrome')).to.be.null();
      expect(new Config(null, { report_file: 'out/<launcher>.xml' }).getExpandedReportFile('IE 11'))
        .to.equal('out/IE_11.xml');
    });
  });

  describe('Reporter', function() {
    it('writes one file per launcher, skips "testem", and sends everything to stdout', function() {
      let stdout = new PassThrough();
      let reportPath = path.join(tmpDir, 'results-<launcher>.tap');
      let reporter = new Reporter({ config: fakeConfig({ reporter: 'tap' }) }, stdout, reportPath);

      reporter.onStart('testem', {});
      reporter.report('testem', { name: 'hook', passed: true });
      reporter.report('Chrome 1.0', { name: 'a', passed: true });
      reporter.report('Firefox', { name: 'b', passed: false });
      reporter.report('Chrome 1.0', { name: 'c', skipped: true });

      reporter.finish();
      reporter.finish();

      return reporter.close().then(() => {
        expect(fs.readdirSync(tmpDir).sort()).to.deep.equal(['results-Chrome_1.0.tap', 'results-Firefox.tap']);

        let chrome = fs.readFileSync(path.join(tmpDir, 'results-Chrome_1.0.tap'), 'utf-8');
        expect(chrome).to.match(/# tests 2/);
        expect(chrome).to.not.match(/Firefox/);

        let firefox = fs.readFileSync(path.join(tmpDir, 'results-Firefox.tap'), 'utf-8');
        expect(firefox).to.match(/# tests 1/);
        expect(firefox).to.match(/# fail {2}1/);

        let output = stdout.read().toString();
        expect(output).to.match(/# tests 4/);
        expect(output.match(/# tests/g)).to.have.lengthOf(1);
      });
    });

    it('tags xunit files with their launcher', function() {
      let reportPath = path.join(tmpDir, '<launcher>.xml');
      let reporter = new Reporter({
        config: fakeConfig({ reporter: 'xunit', xunit_include_launcher_properties: true })
      }, new PassThrough(), reportPath);

      reporter.report('Safari', { name: 'a', passed: true });

      return reporter.close().then(() => {
        let xml = fs.readFileSync(path.join(tmpDir, 'Safari.xml'), 'utf-8');
        expect(xml).to.contain('<property name="launcher" value="Safari"/>');
        expect(xml).to.contain('<property name="Safari_pass" value="1"/>');
      });
    });
  });

  describe('TapReporter', function() {
    it('shows a per-launcher summary when enabled', function() {
      let out = new PassThrough();
      let reporter = new TapReporter(false, out, fakeConfig({ tap_show_launcher_summary: true }));
      reporter.report('Chrome', { name: 'a', passed: true });
      reporter.report('Chrome', { name: 'b', passed: false });
      reporter.report('Firefox', { name: 'c', skipped: true });
      reporter.finish();

      let output = out.read().toString();
      expect(output).to.contain('Per-launcher summary');
      expect(output).to.contain('Chrome: 2 tests, 1 pass, 1 fail, 0 skip');
      expect(output).to.contain('Firefox: 1 tests, 0 pass, 0 fail, 1 skip');
    });

    it('omits the per-launcher summary by default', function() {
      let out = new PassThrough();
      let reporter = new TapReporter(false, out, fakeConfig({}));
      reporter.report('Chrome', { name: 'a', passed: true });
      reporter.finish();

      expect(out.read().toString()).to.not.contain('Per-launcher summary');
    });
  });

  describe('XUnitReporter', function() {
    it('tracks launcher stats and emits properties when enabled', function() {
      let out = new PassThrough();
      let reporter = new XUnitReporter(false, out, fakeConfig({ xunit_include_launcher_properties: true }));
      reporter.report('Chrome', { name: 'a', passed: true });
      reporter.report('Chrome', { name: 'b', passed: false });
      reporter.report('Firefox', { name: 'c', passed: true });

      expect(reporter.getLauncherStats()).to.deep.equal({
        Chrome: { total: 2, pass: 1, fail: 1 },
        Firefox: { total: 1, pass: 1, fail: 0 }
      });

      reporter.finish();
      let xml = out.read().toString();
      expect(xml).to.contain('<property name="launchers" value="Chrome,Firefox"/>');
      expect(xml).to.contain('<property name="Chrome_fail" value="1"/>');
      expect(xml).to.not.contain('name="launcher"');
    });

    it('does not emit properties by default', function() {
      let out = new PassThrough();
      let reporter = new XUnitReporter(false, out, fakeConfig({}));
      reporter.setLauncherName('Chrome');
      reporter.report('Chrome', { name: 'a', passed: true });
      reporter.finish();

      expect(out.read().toString()).to.not.contain('<properties>');
    });
  });
});
