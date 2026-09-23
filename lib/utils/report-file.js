
const fs = require('fs');
const path = require('path');
const mkdirp = require('mkdirp');
const PassThrough = require('stream').PassThrough;
const Bluebird = require('bluebird');

const UNSAFE_FILENAME_CHARS = /[/\\:*?"<>|()]/g;
const KNOWN_TEMPLATES = {
  launcher: true,
  date: true,
  timestamp: true
};

function pad2(value) {
  return value < 10 ? '0' + value : String(value);
}

function coerceDate(value) {
  if (value instanceof Date && !isNaN(value.getTime())) {
    return value;
  }
  if (value === undefined || value === null || value === '') {
    return new Date();
  }
  let parsed = new Date(value);
  if (isNaN(parsed.getTime())) {
    return new Date();
  }
  return parsed;
}

function formatDate(date) {
  return date.getFullYear() + '-' + pad2(date.getMonth() + 1) + '-' + pad2(date.getDate());
}

function formatTimestamp(date) {
  return formatDate(date) + '_' + pad2(date.getHours()) + '-' + pad2(date.getMinutes()) + '-' + pad2(date.getSeconds());
}

function containsTemplate(filePath, name) {
  return typeof filePath === 'string' && filePath.indexOf('<' + name + '>') !== -1;
}

module.exports = class ReportFile {
  constructor(reportFile, options) {
    options = options && typeof options === 'object' ? options : {};
    this.originalPath = reportFile;

    let expandOptions = {};
    if (Object.prototype.hasOwnProperty.call(options, 'launcher')) {
      expandOptions.launcher = options.launcher;
    } else if (ReportFile.hasLauncherTemplate(reportFile)) {
      expandOptions.launcher = null;
    }
    if (Object.prototype.hasOwnProperty.call(options, 'date')) {
      expandOptions.date = options.date;
    }

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

  hasLauncherTemplate(filePath) {
    if (arguments.length === 0) {
      return ReportFile.hasLauncherTemplate(this.originalPath);
    }
    return ReportFile.hasLauncherTemplate(filePath);
  }

  hasDateTemplate(filePath) {
    if (arguments.length === 0) {
      return ReportFile.hasDateTemplate(this.originalPath);
    }
    return ReportFile.hasDateTemplate(filePath);
  }

  hasTimestampTemplate(filePath) {
    if (arguments.length === 0) {
      return ReportFile.hasTimestampTemplate(this.originalPath);
    }
    return ReportFile.hasTimestampTemplate(filePath);
  }

  close() {
    this.outputStream.end();

    return this.closePromise;
  }

  static expandPath(filePath, options) {
    if (typeof filePath !== 'string') {
      return filePath;
    }

    options = options && typeof options === 'object' ? options : {};
    let date = coerceDate(options.date);
    let expanded = filePath;

    if (Object.prototype.hasOwnProperty.call(options, 'launcher')) {
      expanded = expanded.replace(/<launcher>/g, ReportFile.sanitizeLauncherName(options.launcher));
    }

    expanded = expanded.replace(/<date>/g, formatDate(date));
    expanded = expanded.replace(/<timestamp>/g, formatTimestamp(date));
    return expanded;
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

    return String(name).replace(UNSAFE_FILENAME_CHARS, '_').replace(/\s+/g, '_');
  }

  static knownTemplates() {
    return KNOWN_TEMPLATES;
  }
};
