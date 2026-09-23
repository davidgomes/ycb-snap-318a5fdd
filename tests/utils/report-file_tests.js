

const Bluebird = require('bluebird');
const expect = require('chai').expect;
const fs = require('fs');
const path = require('path');
const tmp = require('tmp');
const Writable = require('stream').Writable;

const tmpNameAsync = Bluebird.promisify(tmp.tmpName);

const ReportFile = require('../../lib/utils/report-file');

describe('ReportFile', function() {
  describe('templates', function() {
    it('expands launcher, date, and timestamp', function() {
      let date = new Date(2020, 0, 2, 3, 4, 5);
      let expanded = ReportFile.expandPath('out/<date>/<timestamp>/<launcher>.xml', {
        launcher: 'Chrome Headless',
        date: date
      });
      expect(expanded).to.equal('out/2020-01-02/2020-01-02_03-04-05/Chrome_Headless.xml');
    });

    it('sanitizes filesystem-unsafe launcher names', function() {
      expect(ReportFile.sanitizeLauncherName('a/b\\c:d*e?f"g<h>i|j(k)')).to.equal('a_b_c_d_e_f_g_h_i_j_k_');
      expect(ReportFile.sanitizeLauncherName(null)).to.equal('unknown');
      expect(ReportFile.sanitizeLauncherName(undefined)).to.equal('unknown');
    });

    it('detects template variables', function() {
      expect(ReportFile.hasLauncherTemplate('a/<launcher>.xml')).to.equal(true);
      expect(ReportFile.hasDateTemplate('a/<date>.xml')).to.equal(true);
      expect(ReportFile.hasTimestampTemplate('a/<timestamp>.xml')).to.equal(true);
    });

    it('writes to the expanded path and creates parent directories', function() {
      let dir = tmp.dirSync({ unsafeCleanup: true }).name;
      let date = new Date(2021, 5, 7);
      let reportFile = new ReportFile(path.join(dir, '<date>', '<launcher>.txt'), {
        launcher: 'Firefox',
        date: date
      });
      expect(reportFile.getFilePath()).to.equal(path.join(dir, '2021-06-07', 'Firefox.txt'));
      reportFile.outputStream.write('ok\n');
      return reportFile.close().then(function() {
        expect(fs.readFileSync(reportFile.getFilePath(), 'utf8')).to.equal('ok\n');
      });
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
