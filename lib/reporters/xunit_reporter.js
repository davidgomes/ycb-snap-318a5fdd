

var XmlDom = require('@xmldom/xmldom');
var indent = require('../utils/strutils').indent;

module.exports = class XUnitReporter {
  constructor(silent, out, config) {
    this.out = out || process.stdout;
    this.excludeStackTraces = config.get('xunit_exclude_stack');
    this.includeLauncherProperties = !!config.get('xunit_include_launcher_properties');
    this.launcherName = null;
    this.launcherStats = new Map();
    this.silent = silent;
    this.stoppedOnError = null;
    this.id = 1;
    this.total = 0;
    this.pass = 0;
    this.skipped = 0;
    this.todo = 0;
    this.results = [];
    this.startTime = new Date();
    this.endTime = null;
  }

  report(prefix, data) {
    this.results.push({
      launcher: prefix,
      result: data
    });
    this.display();
    this.total++;

    if (data.skipped) {
      this.skipped++;
    } else if (data.passed && !data.todo) {
      this.pass++;
    } else if (!data.passed && data.todo) {
      this.todo++;
    }

    this.updateLauncherStats(prefix, data);
  }

  setLauncherName(name) {
    this.launcherName = name;
  }

  updateLauncherStats(prefix, data) {
    let launcher = prefix || 'unknown';
    let stats = this.launcherStats.get(launcher);
    if (!stats) {
      stats = { total: 0, pass: 0, skipped: 0, todo: 0 };
      this.launcherStats.set(launcher, stats);
    }

    stats.total++;
    if (data.skipped) {
      stats.skipped++;
    } else if (data.passed && !data.todo) {
      stats.pass++;
    } else if (!data.passed && data.todo) {
      stats.todo++;
    }
  }

  getLauncherStats() {
    let result = {};

    this.launcherStats.forEach((stats, launcher) => {
      result[launcher] = {
        total: stats.total,
        pass: stats.pass,
        fail: stats.total - stats.pass - stats.skipped - stats.todo
      };
    });

    return result;
  }

  finish() {
    if (this.silent) {
      return;
    }
    this.endTime = new Date();
    this.out.write(this.summaryDisplay());
    this.out.write('\n');
  }

  summaryDisplay() {
    var doc = new XmlDom.DOMImplementation().createDocument('', 'testsuite');

    var rootNode = doc.documentElement;
    rootNode.setAttribute('name', 'Testem Tests');
    rootNode.setAttribute('tests', `${this.total}`);
    rootNode.setAttribute('skipped', `${this.skipped}`);
    rootNode.setAttribute('todo', `${this.todo}`);
    rootNode.setAttribute('failures', `${this.failures()}`);
    rootNode.setAttribute('timestamp', new Date().toString());
    rootNode.setAttribute('time', `${this.duration() }`);

    if (this.includeLauncherProperties) {
      rootNode.appendChild(this.getLauncherPropertiesNode(doc));
    }

    for (var i = 0, len = this.results.length; i < len; i++) {
      var testcaseNode = this.getTestResultNode(doc, this.results[i]);
      rootNode.appendChild(testcaseNode);
    }
    return doc.documentElement.toString();
  }

  getLauncherPropertiesNode(document) {
    var propertiesNode = document.createElement('properties');
    var addProperty = function(name, value) {
      var propertyNode = document.createElement('property');
      propertyNode.setAttribute('name', name);
      propertyNode.setAttribute('value', `${value}`);
      propertiesNode.appendChild(propertyNode);
    };

    if (this.launcherName) {
      addProperty('launcher', this.launcherName);
    }

    var stats = this.getLauncherStats();
    var launchers = Object.keys(stats);
    addProperty('launchers', launchers.join(','));
    launchers.forEach(function(launcher) {
      addProperty(`${launcher}_pass`, stats[launcher].pass);
      addProperty(`${launcher}_fail`, stats[launcher].fail);
    });

    return propertiesNode;
  }

  display() {
    // As the output is XML, the XUnitReporter can only write its results after all
    // tests have finished.
    return;
  }

  getTestResultNode(document, result) {
    var launcher = result.launcher;
    result = result.result;

    var resultNode = document.createElement('testcase');
    resultNode.setAttribute('classname', launcher);
    resultNode.setAttribute('name', result.name);
    resultNode.setAttribute('time', this._durationFromMs(result.runDuration));

    var error = result.error;
    if (error) {
      var errorNode = document.createElement('error');
      var errorMessage = '';
      var errorSection = '';

      if (Object.prototype.hasOwnProperty.call(error, 'actual') &&  Object.prototype.hasOwnProperty.call(error, 'expected')) {
        errorMessage = 'Assertion Failed';

        errorSection += 'Expected:\n';
        errorSection += indent(`${error.expected}`);
        errorSection += '\n\n';

        errorSection += 'Result:\n';
        errorSection += indent((error.negative ? 'NOT ' : '') + error.actual);
        errorSection += '\n\n';
      }

      if (error.stack && !this.excludeStackTraces) {
        errorSection += 'Source:\n';
        errorSection += error.stack;
      }

      if (errorSection) {
        var cdata = document.createCDATASection(errorSection);
        errorNode.appendChild(cdata);
      }

      errorNode.setAttribute('message', error.message || errorMessage);
      resultNode.appendChild(errorNode);
    } else if (result.skipped) {
      var skippedNode = document.createElement('skipped');
      resultNode.appendChild(skippedNode);
    } else if (result.todo) {
      var todoNode = document.createElement('todo');
      resultNode.appendChild(todoNode);
    } else if (!result.passed) {
      var failureNode = document.createElement('failure');
      resultNode.appendChild(failureNode);
    }

    return resultNode;
  }

  failures() {
    return this.total - this.pass - this.skipped - this.todo;
  }

  duration() {
    const endTime = this.endTime ? this.endTime.getTime() : 0;
    const startTime = this.startTime.getTime();

    return this._durationFromMs(endTime - startTime);
  }

  _durationFromMs(ms) {
    if (ms)
    {
      return (ms / 1000).toFixed(3);
    } else
    {
      return 0;
    }
  }
};
