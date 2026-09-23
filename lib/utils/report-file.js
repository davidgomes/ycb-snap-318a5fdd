

const fs = require('fs');
const path = require('path');
const mkdirp = require('mkdirp');
const Bluebird = require('bluebird');

const LAUNCHER_TEMPLATE = '<launcher>';
const DATE_TEMPLATE = '<date>';
const TIMESTAMP_TEMPLATE = '<timestamp>';

function pad2(n) {
  return n < 10 ? '0' + n : String(n);
}

function formatDate(date) {
  return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}`;
}

function formatTimestamp(date) {
  return `${formatDate(date)}_${pad2(date.getHours())}-${pad2(date.getMinutes())}-${pad2(date.getSeconds())}`;
}

class ReportFile {
  constructor(reportFile, options) {
    options = options || {};
    this.file = ReportFile.expandPath(reportFile, options);

    mkdirp.sync(path.dirname(path.resolve(this.file)));

    this.outputStream = fs.createWriteStream(this.file, { flags: 'w+' });

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
    if (!filePath) {
      return filePath;
    }
    options = options || {};
    const date = options.date || new Date();
    let result = String(filePath)
      .split(DATE_TEMPLATE).join(formatDate(date))
      .split(TIMESTAMP_TEMPLATE).join(formatTimestamp(date));
    if (options.launcher !== undefined) {
      result = result.split(LAUNCHER_TEMPLATE).join(ReportFile.sanitizeLauncherName(options.launcher));
    }
    return result;
  }

  static hasLauncherTemplate(filePath) {
    return !!filePath && String(filePath).indexOf(LAUNCHER_TEMPLATE) !== -1;
  }

  static hasDateTemplate(filePath) {
    return !!filePath && String(filePath).indexOf(DATE_TEMPLATE) !== -1;
  }

  static hasTimestampTemplate(filePath) {
    return !!filePath && String(filePath).indexOf(TIMESTAMP_TEMPLATE) !== -1;
  }

  static sanitizeLauncherName(name) {
    if (name === null || name === undefined) {
      return 'unknown';
    }
    return String(name).replace(/[/\\:*?"<>|()]/g, '_').replace(/\s+/g, '_');
  }
}

ReportFile.KNOWN_TEMPLATES = [LAUNCHER_TEMPLATE, DATE_TEMPLATE, TIMESTAMP_TEMPLATE];

module.exports = ReportFile;
