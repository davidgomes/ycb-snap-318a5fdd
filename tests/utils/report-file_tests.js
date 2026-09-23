

const Bluebird = require('bluebird');
const expect = require('chai').expect;
const fs = require('fs');
const path = require('path');
const tmp = require('tmp');
const Writable = require('stream').Writable;

const tmpNameAsync = Bluebird.promisify(tmp.tmpName);

const ReportFile = require('../../lib/utils/report-file');

describe('ReportFile', function() {
  const date = new Date(2024, 0, 5, 9, 7, 3);

  describe('expandPath', function() {
    it('expands <date> to YYYY-MM-DD', function() {
      expect(ReportFile.expandPath('reports/<date>.xml', { date })).to.equal('reports/2024-01-05.xml');
    });

    it('expands <timestamp> to YYYY-MM-DD_HH-MM-SS', function() {
      expect(ReportFile.expandPath('reports/<timestamp>.xml', { date })).to.equal('reports/2024-01-05_09-07-03.xml');
    });

    it('expands <launcher> with a sanitized launcher name', function() {
      expect(ReportFile.expandPath('reports/<launcher>-<date>.xml', { launcher: 'Chrome 120.0', date }))
        .to.equal('reports/Chrome_120.0-2024-01-05.xml');
    });

    it('expands every occurrence of a template', function() {
      expect(ReportFile.expandPath('<launcher>/<launcher>.tap', { launcher: 'Firefox' })).to.equal('Firefox/Firefox.tap');
    });

    it('uses the current date when no date is given', function() {
      let now = new Date();
      let expected = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`;
      expect(ReportFile.expandPath('<date>.xml')).to.equal(`${expected}.xml`);
    });

    it('leaves <launcher> untouched when no launcher is given', function() {
      expect(ReportFile.expandPath('<launcher>.xml', { date })).to.equal('<launcher>.xml');
    });

    it('leaves paths without templates untouched', function() {
      expect(ReportFile.expandPath('report.xml', { launcher: 'Chrome', date })).to.equal('report.xml');
    });
  });

  describe('template detection', function() {
    it('detects each template', function() {
      expect(ReportFile.hasLauncherTemplate('r/<launcher>.xml')).to.be.true();
      expect(ReportFile.hasDateTemplate('r/<date>.xml')).to.be.true();
      expect(ReportFile.hasTimestampTemplate('r/<timestamp>.xml')).to.be.true();
    });

    it('returns false without templates or path', function() {
      expect(ReportFile.hasLauncherTemplate('r/report.xml')).to.be.false();
      expect(ReportFile.hasDateTemplate(undefined)).to.be.false();
      expect(ReportFile.hasTimestampTemplate(null)).to.be.false();
    });
  });

  describe('sanitizeLauncherName', function() {
    it('replaces each unsafe character with one underscore', function() {
      expect(ReportFile.sanitizeLauncherName('a/b\\c:d*e?f"g<h>i|j(k)')).to.equal('a_b_c_d_e_f_g_h_i_j_k_');
      expect(ReportFile.sanitizeLauncherName('a//b')).to.equal('a__b');
    });

    it('collapses consecutive whitespace into one underscore', function() {
      expect(ReportFile.sanitizeLauncherName('Headless  Chrome\t 120')).to.equal('Headless_Chrome_120');
    });

    it('returns "unknown" for null and undefined', function() {
      expect(ReportFile.sanitizeLauncherName(null)).to.equal('unknown');
      expect(ReportFile.sanitizeLauncherName(undefined)).to.equal('unknown');
    });
  });

  describe('constructor', function() {
    let dir;

    beforeEach(function() {
      dir = tmp.dirSync({ unsafeCleanup: true });
    });

    afterEach(function() {
      dir.removeCallback();
    });

    it('expands templates and creates parent directories', function() {
      let template = path.join(dir.name, 'nested', '<date>', '<launcher>.xml');
      let reportFile = new ReportFile(template, { launcher: 'Chrome (headless)', date });

      let expected = path.join(dir.name, 'nested', '2024-01-05', 'Chrome__headless_.xml');
      expect(reportFile.getFilePath()).to.equal(expected);

      reportFile.outputStream.write('hello');
      return reportFile.close().then(function() {
        expect(fs.readFileSync(expected, 'utf-8')).to.equal('hello');
      });
    });

    it('returns the plain path when there are no templates', function() {
      let file = path.join(dir.name, 'report.tap');
      let reportFile = new ReportFile(file);

      expect(reportFile.getFilePath()).to.equal(file);
      return reportFile.close();
    });
  });

  describe('close', function() {
    it('resolves when all data has been written', function() {

      let noopStream = new Writable();
      noopStream._write = function(chunk, encoding, done) {
        done();
      };

      let finished = false;

      return tmpNameAsync().then(function(path) {
        return new ReportFile(path, noopStream);
      }).then(function(reportFile) {
        expect(reportFile.closePromise).to.exist();

        reportFile.outputStream.on('finish', function() {
          finished = true;
        });

        return reportFile.close();
      }).then(function() {
        expect(finished).to.be.true();
      });
    });
  });
});
