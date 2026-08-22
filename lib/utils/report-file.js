
const fs = require('fs');
const path = require('path');
const mkdirp = require('mkdirp');
const Bluebird = require('bluebird');

const KNOWN_TEMPLATES = ['launcher', 'date', 'timestamp'];
const LAUNCHER_TEMPLATE = /<launcher>/;
const DATE_TEMPLATE = /<date>/;
const TIMESTAMP_TEMPLATE = /<timestamp>/;
const ANY_TEMPLATE = /<(launcher|date|timestamp)>/;
const UNKNOWN_TEMPLATE = /<([a-zA-Z_][a-zA-Z0-9_]*)>/g;

function pad2(n) {
  return String(n).padStart(2, '0');
}

function formatDate(date) {
  return [
    date.getFullYear(),
    pad2(date.getMonth() + 1),
    pad2(date.getDate())
  ].join('-');
}

function formatTimestamp(date) {
  return [
    formatDate(date),
    pad2(date.getHours()) + '-' + pad2(date.getMinutes()) + '-' + pad2(date.getSeconds())
  ].join('_');
}

module.exports = class ReportFile {
  static hasLauncherTemplate(reportPath) {
    return !!(reportPath && LAUNCHER_TEMPLATE.test(reportPath));
  }

  static hasDateTemplate(reportPath) {
    return !!(reportPath && DATE_TEMPLATE.test(reportPath));
  }

  static hasTimestampTemplate(reportPath) {
    return !!(reportPath && TIMESTAMP_TEMPLATE.test(reportPath));
  }

  static hasAnyTemplate(reportPath) {
    return !!(reportPath && ANY_TEMPLATE.test(reportPath));
  }

  static sanitizeLauncherName(name) {
    if (name == null) {
      return 'unknown';
    }

    return String(name)
      .replace(/[/\\:*?"<>|()]/g, '_')
      .replace(/\s+/g, '_');
  }

  static expandPath(reportPath, options) {
    options = options || {};
    const date = options.date || new Date();
    const launcher = options.launcher != null ?
      ReportFile.sanitizeLauncherName(options.launcher) :
      '';

    return reportPath
      .replace(/<launcher>/g, launcher)
      .replace(/<date>/g, formatDate(date))
      .replace(/<timestamp>/g, formatTimestamp(date));
  }

  static findUnknownTemplates(reportPath) {
    if (!reportPath) {
      return [];
    }

    const unknown = [];
    let match;

    UNKNOWN_TEMPLATE.lastIndex = 0;
    while ((match = UNKNOWN_TEMPLATE.exec(reportPath)) !== null) {
      if (KNOWN_TEMPLATES.indexOf(match[1]) === -1) {
        unknown.push('<' + match[1] + '>');
      }
    }

    return unknown;
  }

  constructor(reportFile, options) {
    if (typeof options !== 'object' || options === null) {
      options = {};
    }

    this.templatePath = reportFile;
    this.file = ReportFile.expandPath(reportFile, options);

    mkdirp.sync(path.dirname(path.resolve(this.file)));

    this.outputStream = fs.createWriteStream(this.file, { flags: 'w+' });

    let alreadyEnded = false;
    function finish(data) {
      if (!alreadyEnded) {
        alreadyEnded = true;
        this.outputStream.end(data);
      }
    }

    this.outputStream.on('end', finish.bind(this));
    this.outputStream.on('error', finish.bind(this));

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
};
