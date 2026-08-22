

const fs = require('fs');
const path = require('path');
const mkdirp = require('mkdirp');
const PassThrough = require('stream').PassThrough;
const Bluebird = require('bluebird');

module.exports = class ReportFile {
  constructor(reportFile, options) {
    this.file = ReportFile.expandPath(reportFile, options);

    this.outputStream = new PassThrough();

    mkdirp.sync(path.dirname(path.resolve(this.file)));

    this.outputStream = fs.createWriteStream(this.file, { flags: 'w+' });

    let alreadyEnded = false;
    function finish(data) {
      if (!alreadyEnded) {
        alreadyEnded = true;
        this.outputStream.end(data);
      }
    }

    this.outputStream.on('end', finish);
    this.outputStream.on('error', finish);

    this.closePromise = new Bluebird.Promise((resolve, reject) => {
      this.outputStream.on('finish', resolve);
      this.outputStream.on('error', reject);
    });
  }

  close() {
    if (!this.closed) {
      this.closed = true;
      this.outputStream.end();
    }

    return this.closePromise;
  }

  getFilePath() {
    return this.file;
  }

  static expandPath(reportFile, options) {
    if (!reportFile) return null;
    options = options || {};
    const date = options.date || new Date();
    const pad = value => String(value).padStart(2, '0');
    const dateString = `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
    const timestamp = `${dateString}_${pad(date.getHours())}-${pad(date.getMinutes())}-${pad(date.getSeconds())}`;
    return reportFile.replace(/<launcher>/g, this.sanitizeLauncherName(options.launcher))
      .replace(/<date>/g, dateString).replace(/<timestamp>/g, timestamp);
  }

  static hasLauncherTemplate(reportFile) { return !!reportFile && reportFile.indexOf('<launcher>') !== -1; }
  static hasDateTemplate(reportFile) { return !!reportFile && reportFile.indexOf('<date>') !== -1; }
  static hasTimestampTemplate(reportFile) { return !!reportFile && reportFile.indexOf('<timestamp>') !== -1; }
  static sanitizeLauncherName(name) {
    if (name === null || typeof name === 'undefined') return 'unknown';
    return String(name).replace(/[\/\\:*?"<>|()]/g, '_').replace(/\s+/g, '_');
  }
};
