

const fs = require('fs');
const path = require('path');
const mkdirp = require('mkdirp');
const { PassThrough, Stream } = require('stream');
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

function includesTemplate(filePath, templateVar) {
  return typeof filePath === 'string' && filePath.indexOf(templateVar) !== -1;
}

class ReportFile {
  constructor(reportFile, options) {
    options = (options && typeof options === 'object' && !(options instanceof Stream)) ? options : {};

    this.originalPath = reportFile;
    this.launcher = options.launcher;
    this.file = ReportFile.expandPath(reportFile, {
      launcher: options.launcher,
      date: options.date
    });

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
    if (!this.closed) {
      this.closed = true;
      this.outputStream.end();
    }

    return this.closePromise;
  }

  static expandPath(filePath, options) {
    if (typeof filePath !== 'string') {
      return filePath;
    }

    options = options || {};
    let date = options.date || new Date();
    let expanded = filePath;

    if (options.launcher !== undefined) {
      expanded = expanded.split(LAUNCHER_TEMPLATE).join(ReportFile.sanitizeLauncherName(options.launcher));
    }

    return expanded
      .split(TIMESTAMP_TEMPLATE).join(formatTimestamp(date))
      .split(DATE_TEMPLATE).join(formatDate(date));
  }

  static hasLauncherTemplate(filePath) {
    return includesTemplate(filePath, LAUNCHER_TEMPLATE);
  }

  static hasDateTemplate(filePath) {
    return includesTemplate(filePath, DATE_TEMPLATE);
  }

  static hasTimestampTemplate(filePath) {
    return includesTemplate(filePath, TIMESTAMP_TEMPLATE);
  }

  static sanitizeLauncherName(name) {
    if (name === null || name === undefined) {
      return 'unknown';
    }

    return String(name)
      .replace(/[/\\:*?"<>|()]/g, '_')
      .replace(/\s+/g, '_');
  }
}

ReportFile.TEMPLATE_VARIABLES = ['launcher', 'date', 'timestamp'];

module.exports = ReportFile;
