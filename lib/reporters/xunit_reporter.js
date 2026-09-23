'use strict';

var XmlDom = require('@xmldom/xmldom');
var indent = require('../utils/strutils').indent;

module.exports = class XUnitReporter {
  constructor(silent, out, config) {
    this.out = out || process.stdout;
    this.excludeStackTraces = config.get('xunit_exclude_stack');
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
  }

  resetBailState() {
    this.id = 1;
    this.total = 0;
    this.pass = 0;
    this.skipped = 0;
    this.todo = 0;
    this.results = [];
    this.bailInfo = null;
    this.endTime = null;
    this.startTime = new Date();
  }

  finish() {
    if (this.silent) {
      return;
    }
    this.endTime = new Date();
    this.out.write(this.summaryDisplay());
    this.out.write('\n');
  }

  bailSummaryText() {
    return [
      'Bail out! ' + this.bailInfo.reason + ' ' + this.bailInfo.count,
      '# bailed',
      '# ran before bail ' + this.bailInfo.testsRanBeforeBail,
      '# suppressed ' + this.bailInfo.suppressedAfterBail
    ].join('\n');
  }

  summaryDisplay() {
    var doc = new XmlDom.DOMImplementation().createDocument('', 'testsuite');

    var rootNode = doc.documentElement;
    rootNode.setAttribute('name', 'Testem Tests');
    rootNode.setAttribute('tests', `${this.total}`);
    rootNode.setAttribute('skipped', `${this.skipped}`);
    rootNode.setAttribute('todo', `${this.todo}`);
    rootNode.setAttribute('failures', `${this.failures()}`);
    if (this.bailInfo) {
      rootNode.setAttribute('errors', `${this.errors()}`);
    }
    rootNode.setAttribute('timestamp', new Date().toString());
    rootNode.setAttribute('time', `${this.duration() }`);

    if (this.bailInfo) {
      rootNode.appendChild(this.getBailPropertiesNode(doc));
    }

    for (var i = 0, len = this.results.length; i < len; i++) {
      var testcaseNode = this.getTestResultNode(doc, this.results[i]);
      rootNode.appendChild(testcaseNode);
    }

    if (this.bailInfo) {
      rootNode.appendChild(this.getBailErrorNode(doc));
      rootNode.appendChild(this.getBailSystemOutNode(doc));
    }
    return doc.documentElement.toString();
  }

  getBailPropertiesNode(document) {
    var properties = document.createElement('properties');
    var entries = [
      ['bailReason', this.bailInfo.reason],
      ['testsBeforeBail', String(this.bailInfo.testsRanBeforeBail)],
      ['suppressedAfterBail', String(this.bailInfo.suppressedAfterBail)]
    ];

    entries.forEach(entry => {
      var property = document.createElement('property');
      property.setAttribute('name', entry[0]);
      property.setAttribute('value', entry[1]);
      properties.appendChild(property);
    });

    return properties;
  }

  getBailErrorNode(document) {
    var resultNode = document.createElement('testcase');
    resultNode.setAttribute('classname', 'Testem');
    resultNode.setAttribute('name', 'Bail out!');

    var errorNode = document.createElement('error');
    errorNode.setAttribute('message', 'Bail out! ' + this.bailInfo.reason + ' ' + this.bailInfo.count);
    errorNode.appendChild(document.createTextNode(this.bailSummaryText()));
    resultNode.appendChild(errorNode);
    return resultNode;
  }

  getBailSystemOutNode(document) {
    var systemOut = document.createElement('system-out');
    systemOut.appendChild(document.createTextNode(this.bailSummaryText()));
    return systemOut;
  }

  errors() {
    var count = 0;
    for (var i = 0; i < this.results.length; i++) {
      if (this.results[i].result && this.results[i].result.error) {
        count++;
      }
    }
    if (this.bailInfo) {
      count++;
    }
    return count;
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
