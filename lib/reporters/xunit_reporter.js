'use strict';

var XmlDom = require('@xmldom/xmldom');
var indent = require('../utils/strutils').indent;

function appendCData(document, node, value) {
  String(value).split(']]>').forEach((part, index, parts) => {
    if (index > 0) {
      part = '>' + part;
    }
    if (index < parts.length - 1) {
      part += ']]';
    }
    node.appendChild(document.createCDATASection(part));
  });
}

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
    this.bailed = false;
    this.bailReason = null;
    this.testsRanBeforeBail = 0;
    this.suppressedAfterBail = 0;
    this.failedCount = 0;
  }

  onBail(report) {
    this.bailed = true;
    this.bailReason = report.bailReason;
    this.testsRanBeforeBail = report.testsRanBeforeBail;
    this.suppressedAfterBail = report.suppressedAfterBail;
    this.failedCount = report.failedCount;
  }

  reset() {
    this.id = 1;
    this.total = 0;
    this.pass = 0;
    this.skipped = 0;
    this.todo = 0;
    this.results = [];
    this.startTime = new Date();
    this.endTime = null;
    this.bailed = false;
    this.bailReason = null;
    this.testsRanBeforeBail = 0;
    this.suppressedAfterBail = 0;
    this.failedCount = 0;
    this.stoppedOnError = null;
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

    if (this.bailed) {
      rootNode.setAttribute('errors', '1');

      var errorNode = doc.createElement('error');
      errorNode.setAttribute('message', 'Bail out! ' + this.bailReason);
      rootNode.appendChild(errorNode);

      var propertiesNode = doc.createElement('properties');
      [
        ['bailReason', this.bailReason],
        ['testsBeforeBail', `${this.testsRanBeforeBail}`],
        ['suppressedAfterBail', `${this.suppressedAfterBail}`]
      ].forEach(function(pair) {
        var propertyNode = doc.createElement('property');
        propertyNode.setAttribute('name', pair[0]);
        propertyNode.setAttribute('value', pair[1] === null || pair[1] === undefined ? '' : `${pair[1]}`);
        propertiesNode.appendChild(propertyNode);
      });
      rootNode.appendChild(propertiesNode);

      var systemOut = doc.createElement('system-out');
      var summary = [
        'Bail out! ' + this.bailReason,
        '# bailed',
        '# ran before bail ' + this.testsRanBeforeBail,
        '# suppressed ' + this.suppressedAfterBail
      ].join('\n');
      appendCData(doc, systemOut, summary);
      rootNode.appendChild(systemOut);
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
        appendCData(document, errorNode, errorSection);
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
