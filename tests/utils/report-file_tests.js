

const Bluebird = require('bluebird');
const expect = require('chai').expect;
const fs = require('fs');
const path = require('path');
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

  describe('templates', function() {
    const fixedDate = new Date(2024, 0, 2, 3, 4, 5);

    it('detects launcher, date, and timestamp templates', function() {
      expect(ReportFile.hasLauncherTemplate('out/<launcher>.xml')).to.equal(true);
      expect(ReportFile.hasLauncherTemplate('out/<date>.xml')).to.equal(false);
      expect(ReportFile.hasDateTemplate('out/<date>/<launcher>.xml')).to.equal(true);
      expect(ReportFile.hasTimestampTemplate('out/<timestamp>.xml')).to.equal(true);
      expect(ReportFile.hasTimestampTemplate('out/<date>.xml')).to.equal(false);
    });

    it('expands date and timestamp with the provided date', function() {
      expect(ReportFile.expandPath('reports/<date>/<timestamp>-<launcher>.xml', {
        launcher: 'Headless Chrome',
        date: fixedDate
      })).to.equal('reports/2024-01-02/2024-01-02_03-04-05-Headless_Chrome.xml');
    });

    it('uses the current date when date is omitted', function() {
      let expanded = ReportFile.expandPath('out-<date>.xml', {});
      let now = new Date();
      let month = now.getMonth() + 1;
      let day = now.getDate();
      let expected = 'out-' + now.getFullYear() + '-' +
        (month < 10 ? '0' : '') + month + '-' +
        (day < 10 ? '0' : '') + day + '.xml';
      expect(expanded).to.equal(expected);
    });

    it('sanitizes launcher names for filenames', function() {
      expect(ReportFile.sanitizeLauncherName(null)).to.equal('unknown');
      expect(ReportFile.sanitizeLauncherName(undefined)).to.equal('unknown');
      expect(ReportFile.sanitizeLauncherName('Chrome  Headless')).to.equal('Chrome_Headless');
      expect(ReportFile.sanitizeLauncherName('a/b\\c:d*e?f"g<h>i|j(k)l')).to.equal('a_b_c_d_e_f_g_h_i_j_k_l');
    });

    it('expands templates, creates parent directories, and returns the file path', function() {
      return tmpDirAsync({ unsafeCleanup: true }).then(function(dir) {
        let template = path.join(dir, 'nested', '<date>', '<launcher>.xml');
        let reportFile = new ReportFile(template, { launcher: 'Firefox (Nightly)', date: fixedDate });
        let expected = path.join(dir, 'nested', '2024-01-02', 'Firefox__Nightly_.xml');

        expect(reportFile.getFilePath()).to.equal(expected);
        expect(fs.existsSync(path.dirname(expected))).to.equal(true);
        expect(reportFile.hasDateTemplate()).to.equal(true);
        expect(reportFile.hasTimestampTemplate(template)).to.equal(false);

        return reportFile.close();
      });
    });
  });
});
