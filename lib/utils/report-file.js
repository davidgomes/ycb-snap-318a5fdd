

const fs = require('fs');
const path = require('path');
const mkdirp = require('mkdirp');
const PassThrough = require('stream').PassThrough;
const Bluebird = require('bluebird');

const LAUNCHER_TEMPLATE = '<launcher>';
const DATE_TEMPLATE = '<date>';
const TIMESTAMP_TEMPLATE = '<timestamp>';

function pad2(n) {
  return String(n).padStart(2, '0');
}

function formatDate(date) {
  return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}`;
}

function formatTimestamp(date) {
  return `${formatDate(date)}_${pad2(date.getHours())}-${pad2(date.getMinutes())}-${pad2(date.getSeconds())}`;
}

function includes(filePath, template) {
  return typeof filePath === 'string' && filePath.indexOf(template) !== -1;
}

module.exports = class ReportFile {
  constructor(reportFile, options) {
    let expandOptions = {};
    if (options && typeof options === 'object') {
      expandOptions = { launcher: options.launcher, date: options.date };
    }

    this.template = reportFile;
    this.launcher = expandOptions.launcher;
    this.file = ReportFile.expandPath(reportFile, expandOptions);

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
    if (typeof filePath !== 'string') {
      return filePath;
    }

    options = options || {};
    let date = options.date === undefined || options.date === null ? new Date() : new Date(options.date);

    let expanded = filePath
      .split(DATE_TEMPLATE).join(formatDate(date))
      .split(TIMESTAMP_TEMPLATE).join(formatTimestamp(date));

    if (options.launcher !== undefined && options.launcher !== null) {
      expanded = expanded.split(LAUNCHER_TEMPLATE).join(ReportFile.sanitizeLauncherName(options.launcher));
    }

    return expanded;
  }

  static hasLauncherTemplate(filePath) {
    return includes(filePath, LAUNCHER_TEMPLATE);
  }

  static hasDateTemplate(filePath) {
    return includes(filePath, DATE_TEMPLATE);
  }

  static hasTimestampTemplate(filePath) {
    return includes(filePath, TIMESTAMP_TEMPLATE);
  }

  static sanitizeLauncherName(name) {
    if (name === undefined || name === null) {
      return 'unknown';
    }

    return String(name)
      .replace(/[/\\:*?"<>|()]/g, '_')
      .replace(/\s+/g, '_');
  }
};

module.exports.TEMPLATE_VARIABLES = ['launcher', 'date', 'timestamp'];
