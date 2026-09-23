/*

qunit_adapter.js
================

Testem's QUnit adapter. Works by using QUnit's hooks:

* `testStart`
* `testDone`
* `moduleStart`
* `moduleEnd`
* `done`
* `log`

*/

/* globals QUnit, emit, Testem */
/* globals module */
/* exported qunitAdapter */
'use strict';

function qunitAdapter() {

  var results = {
    failed: 0,
    passed: 0,
    skipped: 0,
    todo: 0,
    total: 0,
    tests: []
  };
  var currentTest;
  var id = 1;
  var allTestResultsSent = false;

  function isAborted() {
    return typeof Testem !== 'undefined' && !!Testem && !!Testem.aborted;
  }

  function emitAllTestResults() {
    allTestResultsSent = true;
    emit('all-test-results');
  }

  function clearQueue() {
    if (QUnit.config && QUnit.config.queue) {
      QUnit.config.queue.length = 0;
    }
  }

  // Returns true when the run was aborted, after stopping QUnit and signaling completion once.
  function checkAborted() {
    if (!isAborted()) {
      return false;
    }
    clearQueue();
    if (!allTestResultsSent) {
      emitAllTestResults();
    }
    return true;
  }

  function lineNumber(e) {
    return e.line || e.lineNumber;
  }

  function sourceFile(e) {
    return e.sourceURL || e.fileName;
  }

  function message(e) {
    var msg = (e.name && e.message) ? (e.name + ': ' + e.message) : e.toString();
    return msg;
  }

  function stacktrace(e) {
    if (e.stack) {
      return e.stack;
    }
    return undefined;
  }

  QUnit.log(function(params, e) {
    if (checkAborted()) {
      return;
    }
    if (e) {
      currentTest.items.push({
        passed: params.result,
        line: lineNumber(e),
        file: sourceFile(e),
        stack: stacktrace(e) || params.source,
        message: message(e)
      });
    } else {
      if (params.result) {
        currentTest.items.push({
          passed: params.result,
          message: params.message
        });
      } else {
        currentTest.items.push({
          passed: params.result,
          actual: params.actual,
          expected: params.expected,
          stack: params.source,
          message: params.message,
          negative: params.negative
        });
      }

    }

  });
  QUnit.testStart(function(params) {
    if (checkAborted()) {
      return;
    }
    currentTest = {
      id: id++,
      name: (params.module ? params.module + ': ' : '') + params.name,
      items: []
    };
    emit('tests-start', currentTest);
  });
  QUnit.testDone(function(params) {
    if (checkAborted()) {
      return;
    }
    currentTest.failed = params.failed;
    currentTest.passed = params.passed;
    currentTest.skipped = params.skipped;
    currentTest.todo = params.todo;
    currentTest.total = params.total;
    currentTest.runDuration = params.runtime;
    currentTest.testId = params.testId;

    results.total++;

    if (currentTest.skipped) {
      results.skipped++;
    } else if (results.failed > 0 && !results.todo) {
      results.failed++;
    } else {
      results.passed++;
    }

    results.tests.push(currentTest);

    emit('test-result', currentTest);
  });
  QUnit.done(function(params) {
    if (checkAborted()) {
      return;
    }
    results.runDuration = params.runtime;
    emitAllTestResults();
  });

}

// Exporting this as a module so that it can be unit tested in Node.
if (typeof module !== 'undefined') {
  module.exports = qunitAdapter;
}
