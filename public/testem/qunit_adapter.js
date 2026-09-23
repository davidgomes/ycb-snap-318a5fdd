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
  var abortSignaled = false;

  function clearQueue() {
    if (typeof QUnit !== 'undefined' && QUnit.config && QUnit.config.queue && typeof QUnit.config.queue.length === 'number') {
      QUnit.config.queue.length = 0;
    }
  }

  function suppressBecauseAborted() {
    if (typeof Testem !== 'undefined' && Testem.aborted) {
      clearQueue();
      if (!abortSignaled) {
        abortSignaled = true;
        emit('all-test-results');
      }
      return true;
    }
    return false;
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
    if (suppressBecauseAborted()) {
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
    if (suppressBecauseAborted()) {
      return;
    }
    currentTest = {
      id: id++,
      name: (params.module ? params.module + ': ' : '') + params.name,
      items: []
    };
    if (suppressBecauseAborted()) {
      return;
    }
    emit('tests-start', currentTest);
  });
  QUnit.testDone(function(params) {
    if (suppressBecauseAborted()) {
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

    if (suppressBecauseAborted()) {
      return;
    }
    emit('test-result', currentTest);
  });
  QUnit.done(function(params) {
    if (suppressBecauseAborted()) {
      return;
    }
    results.runDuration = params.runtime;
    if (suppressBecauseAborted()) {
      return;
    }
    emit('all-test-results');
  });

}

// Exporting this as a module so that it can be unit tested in Node.
if (typeof module !== 'undefined') {
  module.exports = qunitAdapter;
}
