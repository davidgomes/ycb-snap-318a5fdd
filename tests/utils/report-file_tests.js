

const Bluebird = require('bluebird');
const expect = require('chai').expect;
const fs = require('fs');
const path = require('path');
const rimraf = require('rimraf');
const tmp = require('tmp');
const Writable = require('stream').Writable;

const tmpNameAsync = Bluebird.promisify(tmp.tmpName);
const tmpDirAsync = Bluebird.promisify(tmp.dir);

const ReportFile = require('../../lib/utils/report-file');

describe('ReportFile', function() {
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

  describe('sanitizeLauncherName', function() {
    it('replaces each filesystem-unsafe character with one underscore', function() {
      expect(ReportFile.sanitizeLauncherName('a/b\\c:d*e?f"g<h>i|j(k)l')).to.equal('a_b_c_d_e_f_g_h_i_j_k_l');
      expect(ReportFile.sanitizeLauncherName('a//b')).to.equal('a__b');
    });

    it('collapses consecutive whitespace into one underscore', function() {
      expect(ReportFile.sanitizeLauncherName('Headless  Chrome')).to.equal('Headless_Chrome');
      expect(ReportFile.sanitizeLauncherName('Chrome\t\n 120.0')).to.equal('Chrome_120.0');
    });

    it('sanitizes realistic browser names', function() {
      expect(ReportFile.sanitizeLauncherName('Chrome 120.0 (Mac OS X)')).to.equal('Chrome_120.0__Mac_OS_X_');
    });

    it('returns "unknown" for null or undefined', function() {
      expect(ReportFile.sanitizeLauncherName(null)).to.equal('unknown');
      expect(ReportFile.sanitizeLauncherName(undefined)).to.equal('unknown');
    });
  });

  describe('template detection', function() {
    it('detects <launcher>', function() {
      expect(ReportFile.hasLauncherTemplate('out/<launcher>.xml')).to.be.true();
      expect(ReportFile.hasLauncherTemplate('out/report.xml')).to.be.false();
    });

    it('detects <date>', function() {
      expect(ReportFile.hasDateTemplate('out/<date>.xml')).to.be.true();
      expect(ReportFile.hasDateTemplate('out/<timestamp>.xml')).to.be.false();
    });

    it('detects <timestamp>', function() {
      expect(ReportFile.hasTimestampTemplate('out/<timestamp>.xml')).to.be.true();
      expect(ReportFile.hasTimestampTemplate('out/<date>.xml')).to.be.false();
    });

    it('returns false for missing paths', function() {
      expect(ReportFile.hasLauncherTemplate(undefined)).to.be.false();
      expect(ReportFile.hasDateTemplate(null)).to.be.false();
      expect(ReportFile.hasTimestampTemplate('')).to.be.false();
    });
  });

  describe('expandPath', function() {
    let date = new Date(2024, 0, 5, 9, 7, 3);

    it('expands <launcher> with a sanitized name', function() {
      expect(ReportFile.expandPath('out/<launcher>.xml', { launcher: 'Headless Chrome' })).to.equal('out/Headless_Chrome.xml');
    });

    it('expands <launcher> to "unknown" when no launcher is given', function() {
      expect(ReportFile.expandPath('out/<launcher>.xml')).to.equal('out/unknown.xml');
    });

    it('does not interpret replacement patterns in launcher names', function() {
      expect(ReportFile.expandPath('<launcher>.xml', { launcher: 'a$&b' })).to.equal('a$&b.xml');
    });

    it('expands <date> as YYYY-MM-DD', function() {
      expect(ReportFile.expandPath('out/<date>.xml', { date: date })).to.equal('out/2024-01-05.xml');
    });

    it('expands <timestamp> as YYYY-MM-DD_HH-MM-SS', function() {
      expect(ReportFile.expandPath('out/<timestamp>.xml', { date: date })).to.equal('out/2024-01-05_09-07-03.xml');
    });

    it('expands every occurrence of every template', function() {
      expect(ReportFile.expandPath('<date>/<launcher>-<timestamp>-<launcher>.xml', { launcher: 'Firefox', date: date }))
        .to.equal('2024-01-05/Firefox-2024-01-05_09-07-03-Firefox.xml');
    });

    it('uses the current date when none is given', function() {
      expect(ReportFile.expandPath('<date>.xml')).to.match(/^\d{4}-\d{2}-\d{2}\.xml$/);
      expect(ReportFile.expandPath('<timestamp>.xml')).to.match(/^\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}\.xml$/);
    });

    it('leaves paths without templates unchanged', function() {
      expect(ReportFile.expandPath('out/report.xml', { launcher: 'Chrome', date: date })).to.equal('out/report.xml');
    });
  });

  describe('with template options', function() {
    let dir;

    beforeEach(function() {
      return tmpDirAsync().then(function(tmpDir) {
        dir = tmpDir;
      });
    });

    afterEach(function() {
      rimraf.sync(dir);
    });

    it('writes to the expanded path, creating parent directories', function() {
      let template = path.join(dir, '<date>', 'nested', 'report-<launcher>.xml');
      let reportFile = new ReportFile(template, { launcher: 'Headless Chrome', date: new Date(2024, 0, 5) });
      let expected = path.join(dir, '2024-01-05', 'nested', 'report-Headless_Chrome.xml');

      expect(reportFile.getFilePath()).to.equal(expected);

      reportFile.outputStream.write('data');

      return reportFile.close().then(function() {
        expect(fs.readFileSync(expected, 'utf-8')).to.equal('data');
      });
    });

    it('returns the path as given when it has no templates', function() {
      let file = path.join(dir, 'report.xml');
      let reportFile = new ReportFile(file);

      expect(reportFile.getFilePath()).to.equal(file);

      return reportFile.close();
    });
  });
});
