

const fs = require('fs');
const path = require('path');
const mkdirp = require('mkdirp');
const PassThrough = require('stream').PassThrough;
const Bluebird = require('bluebird');

function pad2(value) {
  let text = String(value);
  return text.length < 2 ? '0' + text : text;
}

function resolveDate(date) {
  if (date === undefined || date === null) {
    return new Date();
  }

  let resolved = date instanceof Date ? date : new Date(date);
  if (isNaN(resolved.getTime())) {
    return new Date();
  }

  return resolved;
}

function formatDate(date) {
  return date.getFullYear() + '-' + pad2(date.getMonth() + 1) + '-' + pad2(date.getDate());
}

function formatTimestamp(date) {
  return formatDate(date) + '_' + pad2(date.getHours()) + '-' + pad2(date.getMinutes()) + '-' + pad2(date.getSeconds());
}

function isTemplateOptions(options) {
  if (!options || typeof options !== 'object') {
    return false;
  }

  // Existing callers pass a stream as the second argument; ignore it.
  if (typeof options.write === 'function') {
    return false;
  }

  return true;
}

module.exports = class ReportFile {
  constructor(reportFile, options) {
    let templateOptions = isTemplateOptions(options) ? options : {};
    this.file = ReportFile.expandPath(reportFile, templateOptions);

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
    this.outputStream.end();

    return this.closePromise;
  }

  getFilePath() {
    return this.file;
  }

  static expandPath(filePath, options) {
    if (!filePath) {
      return filePath;
    }

    options = options || {};
    let date = resolveDate(options.date);
    let launcher = ReportFile.sanitizeLauncherName(options.launcher);

    return String(filePath)
      .replace(/<launcher>/g, function() {
        return launcher;
      })
      .replace(/<date>/g, formatDate(date))
      .replace(/<timestamp>/g, formatTimestamp(date));
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

  static sanitizeLauncherName(name) {
    if (name === undefined || name === null) {
      return 'unknown';
    }

    return String(name)
      .replace(/[\\/:*?"<>|()]/g, '_')
      .replace(/\s+/g, '_');
  }
};
