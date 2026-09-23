

const fs = require('fs');
const path = require('path');
const mkdirp = require('mkdirp');
const PassThrough = require('stream').PassThrough;
const Bluebird = require('bluebird');

function pad2(number) {
  return String(number).padStart(2, '0');
}

function formatDate(date) {
  return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}`;
}

function formatTimestamp(date) {
  return `${formatDate(date)}_${pad2(date.getHours())}-${pad2(date.getMinutes())}-${pad2(date.getSeconds())}`;
}

function containsTemplate(filePath, name) {
  return typeof filePath === 'string' && filePath.indexOf(`<${name}>`) !== -1;
}

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

  getFilePath() {
    return this.file;
  }

  close() {
    this.outputStream.end();

    return this.closePromise;
  }

  static expandPath(filePath, options) {
    options = options || {};
    let date = options.date ? new Date(options.date) : new Date();

    // Function replacers keep `$` sequences in launcher names from being treated as replacement patterns.
    return filePath
      .replace(/<launcher>/g, () => ReportFile.sanitizeLauncherName(options.launcher))
      .replace(/<date>/g, () => formatDate(date))
      .replace(/<timestamp>/g, () => formatTimestamp(date));
  }

  static hasLauncherTemplate(filePath) {
    return containsTemplate(filePath, 'launcher');
  }

  static hasDateTemplate(filePath) {
    return containsTemplate(filePath, 'date');
  }

  static hasTimestampTemplate(filePath) {
    return containsTemplate(filePath, 'timestamp');
  }

  static sanitizeLauncherName(name) {
    if (name === null || name === undefined) {
      return 'unknown';
    }

    return String(name)
      .replace(/[/\\:*?"<>|()]/g, '_')
      .replace(/\s+/g, '_');
  }
};
