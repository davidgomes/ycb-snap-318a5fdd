
const fs = require('fs');
const path = require('path');
const mkdirp = require('mkdirp');
const PassThrough = require('stream').PassThrough;
const Bluebird = require('bluebird');

function pad(n) {
  return String(n).padStart(2, '0');
}

function formatDate(date) {
  return date.getFullYear() + '-' + pad(date.getMonth() + 1) + '-' + pad(date.getDate());
}

function formatTimestamp(date) {
  return formatDate(date) + '_' + pad(date.getHours()) + '-' + pad(date.getMinutes()) + '-' + pad(date.getSeconds());
}

function isStream(value) {
  return value && typeof value === 'object' && (typeof value.pipe === 'function' || typeof value.write === 'function') && !Object.prototype.hasOwnProperty.call(value, 'launcher') && !Object.prototype.hasOwnProperty.call(value, 'date');
}

module.exports = class ReportFile {
  constructor(reportFile, options) {
    if (isStream(options)) {
      options = {};
    }
    options = options || {};

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

  static sanitizeLauncherName(name) {
    if (name === null || name === undefined) {
      return 'unknown';
    }
    return String(name)
      .replace(/[/\\:*?"<>|()]/g, '_')
      .replace(/\s+/g, '_');
  }

  static hasLauncherTemplate(filePath) {
    return typeof filePath === 'string' && filePath.indexOf('<launcher>') !== -1;
  }

  static hasDateTemplate(filePath) {
    return typeof filePath === 'string' && filePath.indexOf('<date>') !== -1;
  }

  static hasTimestampTemplate(filePath) {
    return typeof filePath === 'string' && filePath.indexOf('<timestamp>') !== -1;
  }

  hasDateTemplate(filePath) {
    return ReportFile.hasDateTemplate(filePath);
  }

  hasTimestampTemplate(filePath) {
    return ReportFile.hasTimestampTemplate(filePath);
  }

  static expandPath(filePath, options) {
    if (!filePath) {
      return filePath;
    }
    options = options || {};
    let date = options.date || new Date();
    let expanded = String(filePath);
    if (Object.prototype.hasOwnProperty.call(options, 'launcher')) {
      let launcher = ReportFile.sanitizeLauncherName(options.launcher);
      expanded = expanded.replace(/<launcher>/g, launcher);
    }
    expanded = expanded.replace(/<date>/g, formatDate(date));
    expanded = expanded.replace(/<timestamp>/g, formatTimestamp(date));
    return expanded;
  }

  getFilePath() {
    return this.file;
  }

  close() {
    if (!this.closed) {
      this.closed = true;
      this.outputStream.end();
    }

    return this.closePromise;
  }
};
