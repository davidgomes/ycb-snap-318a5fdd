

const fs = require('fs');
const path = require('path');
const mkdirp = require('mkdirp');
const Bluebird = require('bluebird');

module.exports = class ReportFile {
  constructor(reportFile, options) {
    this.file = ReportFile.expandPath(reportFile, options);
    mkdirp.sync(path.dirname(path.resolve(this.file)));
    this.outputStream = fs.createWriteStream(this.file, { flags: 'w+' });

    this.closePromise = new Bluebird.Promise((resolve, reject) => {
      this.outputStream.on('finish', resolve);
      this.outputStream.on('error', reject);
    });
    this.closed = false;
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
    if (!reportFile) {
      return null;
    }
    options = options || {};
    const date = options.date || new Date();
    const format = value => String(value).padStart(2, '0');
    const dateString = `${date.getFullYear()}-${format(date.getMonth() + 1)}-${format(date.getDate())}`;
    const timestamp = `${dateString}_${format(date.getHours())}-${format(date.getMinutes())}-${format(date.getSeconds())}`;
    return reportFile
      .replace(/<launcher>/g, ReportFile.sanitizeLauncherName(options.launcher))
      .replace(/<date>/g, dateString)
      .replace(/<timestamp>/g, timestamp);
  }

  static hasLauncherTemplate(path) {
    return typeof path === 'string' && path.indexOf('<launcher>') !== -1;
  }

  static hasDateTemplate(path) {
    return typeof path === 'string' && path.indexOf('<date>') !== -1;
  }

  static hasTimestampTemplate(path) {
    return typeof path === 'string' && path.indexOf('<timestamp>') !== -1;
  }

  static sanitizeLauncherName(name) {
    if (name === null || typeof name === 'undefined') {
      return 'unknown';
    }
    return String(name).replace(/[\/\\:*?"<>|()]/g, '_').replace(/\s+/g, '_');
  }
};
