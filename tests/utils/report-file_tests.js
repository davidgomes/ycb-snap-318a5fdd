

const Bluebird = require('bluebird');
const expect = require('chai').expect;
const tmp = require('tmp');
const Writable = require('stream').Writable;

const tmpNameAsync = Bluebird.promisify(tmp.tmpName);

const ReportFile = require('../../lib/utils/report-file');

describe('ReportFile', function() {
  describe('templates', function() {
    it('expands launcher, date, and timestamp', function() {
      let date = new Date(2024, 0, 2, 3, 4, 5);
      let expanded = ReportFile.expandPath('reports/<launcher>-<date>-<timestamp>.xml', {
        launcher: 'Headless Firefox',
        date: date
      });

      expect(expanded).to.equal('reports/Headless_Firefox-2024-01-02-2024-01-02_03-04-05.xml');
    });

    it('uses the current date when one is not provided', function() {
      let expanded = ReportFile.expandPath('out-<date>.xml', {});
      expect(expanded).to.match(/^out-\d{4}-\d{2}-\d{2}\.xml$/);
    });

    it('detects template variables', function() {
      expect(ReportFile.hasLauncherTemplate('a/<launcher>.xml')).to.equal(true);
      expect(ReportFile.hasDateTemplate('a/<date>.xml')).to.equal(true);
      expect(ReportFile.hasTimestampTemplate('a/<timestamp>.xml')).to.equal(true);
      expect(ReportFile.hasLauncherTemplate('plain.xml')).to.equal(false);
    });

    it('sanitizes launcher names for the filesystem', function() {
      expect(ReportFile.sanitizeLauncherName(null)).to.equal('unknown');
      expect(ReportFile.sanitizeLauncherName(undefined)).to.equal('unknown');
      expect(ReportFile.sanitizeLauncherName('a/b\\c:d*e?f"g<h>i|j(k)l')).to.equal('a_b_c_d_e_f_g_h_i_j_k_l');
      expect(ReportFile.sanitizeLauncherName('foo   bar\tbaz')).to.equal('foo_bar_baz');
    });

    it('exposes the expanded path and creates parent directories', function() {
      let date = new Date(2024, 5, 7, 8, 9, 10);
      return tmpNameAsync().then(function(dir) {
        let template = dir + '/nested/<launcher>/out-<date>.xml';
        let reportFile = new ReportFile(template, { launcher: 'Chrome/Canary', date: date });
        expect(reportFile.getFilePath()).to.equal(dir + '/nested/Chrome_Canary/out-2024-06-07.xml');
        expect(reportFile.hasDateTemplate()).to.equal(true);
        expect(reportFile.hasTimestampTemplate()).to.equal(false);
        return reportFile.close();
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
