
const fs = require('fs');
const path = require('path');
const mkdirp = require('mkdirp');
const Bluebird = require('bluebird');

const LAUNCHER_UNSAFE_CHARS = /[/\\:*?"<>|()]/g;
const KNOWN_TEMPLATES = {
  launcher: true,
  date: true,
  timestamp: true
};

function pad2(value) {
  return (value < 10 ? '0' : '') + value;
}

function formatDate(date) {
  return date.getFullYear() + '-' + pad2(date.getMonth() + 1) + '-' + pad2(date.getDate());
}

function formatTimestamp(date) {
  return formatDate(date) + '_' + pad2(date.getHours()) + '-' + pad2(date.getMinutes()) + '-' + pad2(date.getSeconds());
}

function templateOptions(options) {
  if (!options || typeof options !== 'object' || typeof options.write === 'function') {
    return {};
  }
  return options;
}

module.exports = class ReportFile {
  constructor(reportFile, options) {
    let opts = templateOptions(options);
    this.templatePath = reportFile;
    this.file = ReportFile.expandPath(reportFile, opts);

    mkdirp.sync(path.dirname(path.resolve(this.file)));

    this.outputStream = fs.createWriteStream(this.file, { flags: 'w+' });
    this._closeStarted = false;

    this.closePromise = new Bluebird.Promise((resolve, reject) => {
      this.outputStream.on('finish', resolve);
      this.outputStream.on('error', reject);
    });
  }

  getFilePath() {
    return this.file;
  }

  hasLauncherTemplate(filePath) {
    return ReportFile.hasLauncherTemplate(filePath === undefined ? this.templatePath : filePath);
  }

  hasDateTemplate(filePath) {
    return ReportFile.hasDateTemplate(filePath === undefined ? this.templatePath : filePath);
  }

  hasTimestampTemplate(filePath) {
    return ReportFile.hasTimestampTemplate(filePath === undefined ? this.templatePath : filePath);
  }

  close() {
    if (!this._closeStarted) {
      this._closeStarted = true;
      this.outputStream.end();
    }

    return this.closePromise;
  }

  static sanitizeLauncherName(name) {
    if (name === null || name === undefined) {
      return 'unknown';
    }

    return String(name).replace(LAUNCHER_UNSAFE_CHARS, '_').replace(/\s+/g, '_');
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

  static expandPath(filePath, options) {
    let opts = templateOptions(options);
    let date = opts.date instanceof Date && !isNaN(opts.date.getTime()) ? opts.date : new Date();
    let launcher = ReportFile.sanitizeLauncherName(opts.launcher);

    return String(filePath)
      .replace(/<launcher>/g, launcher)
      .replace(/<date>/g, formatDate(date))
      .replace(/<timestamp>/g, formatTimestamp(date));
  }

  static knownTemplates() {
    return KNOWN_TEMPLATES;
  }
};
