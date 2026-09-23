

var XmlDom = require('@xmldom/xmldom');
var indent = require('../utils/strutils').indent;

module.exports = class XUnitReporter {
  constructor(silent, out, config) {
    this.out = out || process.stdout;
    this.excludeStackTraces = config.get('xunit_exclude_stack');
    this.includeLauncherProperties = !!config.get('xunit_include_launcher_properties');
    this.silent = silent;
    this.stoppedOnError = null;
    this.id = 1;
    this.total = 0;
    this.pass = 0;
    this.skipped = 0;
    this.todo = 0;
    this.results = [];
    this.launcherStats = {};
    this.launcherName = null;
    this.finished = false;
    this.startTime = new Date();
    this.endTime = null;
  }

  setLauncherName(name) {
    this.launcherName = name;
  }

  getLauncherStats() {
    var stats = {};
    var names = Object.keys(this.launcherStats);
    for (var i = 0; i < names.length; i++) {
      var name = names[i];
      var entry = this.launcherStats[name];
      stats[name] = {
        total: entry.total,
        pass: entry.pass,
        fail: entry.fail
      };
    }
    return stats;
  }

  report(prefix, data) {
    this.results.push({
      launcher: prefix,
      result: data
    });
    this.display();
    this.total++;
    this._recordLauncher(prefix, data);

    if (data.skipped) {
      this.skipped++;
    } else if (data.passed && !data.todo) {
      this.pass++;
    } else if (!data.passed && data.todo) {
      this.todo++;
    }
  }

  _recordLauncher(prefix, data) {
    var name = prefix === undefined || prefix === null || prefix === '' ? 'unknown' : String(prefix);
    if (!this.launcherStats[name]) {
      this.launcherStats[name] = { total: 0, pass: 0, fail: 0 };
    }

    var stats = this.launcherStats[name];
    stats.total++;

    if (data.skipped) {
      return;
    }
    if (data.passed && !data.todo) {
      stats.pass++;
      return;
    }
    if (!data.passed && data.todo) {
      return;
    }

    stats.fail++;
  }

  finish() {
    if (this.silent || this.finished) {
      return;
    }
    this.finished = true;
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
      rootNode.appendChild(this._launcherPropertiesNode(doc));
    }

    for (var i = 0, len = this.results.length; i < len; i++) {
      var testcaseNode = this.getTestResultNode(doc, this.results[i]);
      rootNode.appendChild(testcaseNode);
    }
    return doc.documentElement.toString();
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

  _launcherPropertiesNode(doc) {
    var propertiesNode = doc.createElement('properties');
    var stats = this.getLauncherStats();
    var names = Object.keys(stats);
    var launcherValue = this.launcherName;
    if (launcherValue === undefined || launcherValue === null || launcherValue === '') {
      launcherValue = names.join(',');
    }

    for (var i = 0; i < names.length; i++) {
      var name = names[i];
      this._appendProperty(doc, propertiesNode, name + '_pass', String(stats[name].pass));
      this._appendProperty(doc, propertiesNode, name + '_fail', String(stats[name].fail));
    }

    this._appendProperty(doc, propertiesNode, 'launcher', String(launcherValue));
    this._appendProperty(doc, propertiesNode, 'launchers', names.join(','));

    return propertiesNode;
  }

  _appendProperty(doc, parent, name, value) {
    var propertyNode = doc.createElement('property');
    propertyNode.setAttribute('name', name);
    propertyNode.setAttribute('value', value);
    parent.appendChild(propertyNode);
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
