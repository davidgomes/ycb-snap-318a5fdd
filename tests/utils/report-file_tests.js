

const Bluebird = require('bluebird');
const expect = require('chai').expect;
const tmp = require('tmp');
const fs = require('fs');
const path = require('path');
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

  describe('templates', function() {
    it('detects launcher, date, and timestamp templates', function() {
      expect(ReportFile.hasLauncherTemplate('out/<launcher>.xml')).to.equal(true);
      expect(ReportFile.hasLauncherTemplate('out/<date>.xml')).to.equal(false);
      expect(ReportFile.hasLauncherTemplate()).to.equal(false);
      expect(ReportFile.hasDateTemplate('out/<date>.xml')).to.equal(true);
      expect(ReportFile.hasDateTemplate('out/<launcher>.xml')).to.equal(false);
      expect(ReportFile.hasTimestampTemplate('out/<timestamp>.xml')).to.equal(true);
      expect(ReportFile.hasTimestampTemplate('out/<date>.xml')).to.equal(false);
    });

    it('expands launcher, date, and timestamp', function() {
      let date = new Date(2024, 4, 6, 7, 8, 9);
      let expanded = ReportFile.expandPath('reports/<launcher>/<date>_<timestamp>.xml', {
        launcher: 'Chrome (Headless)',
        date: date
      });

      expect(expanded).to.equal('reports/Chrome__Headless_/2024-05-06_2024-05-06_07-08-09.xml');
    });

    it('uses the current date when date is omitted', function() {
      let before = new Date();
      let expanded = ReportFile.expandPath('out/<date>.xml');
      let after = new Date();
      let match = expanded.match(/^out\/(\d{4}-\d{2}-\d{2})\.xml$/);

      expect(match).to.not.equal(null);
      expect(match[1] >= formatLocalDate(before) && match[1] <= formatLocalDate(after)).to.equal(true);
    });

    it('sanitizes launcher names for the filesystem', function() {
      expect(ReportFile.sanitizeLauncherName(null)).to.equal('unknown');
      expect(ReportFile.sanitizeLauncherName(undefined)).to.equal('unknown');
      expect(ReportFile.sanitizeLauncherName('Chrome  Headless')).to.equal('Chrome_Headless');
      expect(ReportFile.sanitizeLauncherName('a/b\\c:d*e?f"g<h>i|j(k)l')).to.equal('a_b_c_d_e_f_g_h_i_j_k_l');
      expect(ReportFile.sanitizeLauncherName('a//b')).to.equal('a__b');
    });

    it('returns the expanded path and creates parent directories', function() {
      let date = new Date(2024, 0, 2, 3, 4, 5);

      return tmpDirAsync({ unsafeCleanup: true }).then(function(dir) {
        let template = path.join(dir, '<date>', '<launcher>.tap');
        let reportFile = new ReportFile(template, {
          launcher: 'Firefox Nightly',
          date: date
        });

        expect(reportFile.getFilePath()).to.equal(path.join(dir, '2024-01-02', 'Firefox_Nightly.tap'));
        expect(fs.existsSync(path.dirname(reportFile.getFilePath()))).to.equal(true);

        return reportFile.close();
      });
    });
  });
});

function formatLocalDate(date) {
  let month = String(date.getMonth() + 1);
  let day = String(date.getDate());
  if (month.length < 2) {
    month = '0' + month;
  }
  if (day.length < 2) {
    day = '0' + day;
  }
  return date.getFullYear() + '-' + month + '-' + day;
}
