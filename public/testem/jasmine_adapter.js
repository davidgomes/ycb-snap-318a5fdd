/*

jasmine_adapter.js
==================

Testem's adapter for Jasmine. It works by adding a custom reporter.

*/

/* globals emit, jasmine, Testem */
/* exported jasmineAdapter */
'use strict';

function jasmineAdapter() {

  var results = {
    failed: 0,
    passed: 0,
    total: 0,
    tests: []
  };
  var signaledAllResults = false;

  function isAborted() {
    return typeof Testem !== 'undefined' && Testem.aborted;
  }

  function emitAllResultsOnce() {
    if (signaledAllResults) {
      return;
    }
    signaledAllResults = true;
    emit('all-test-results');
  }

  function JasmineAdapterReporter() {}
  JasmineAdapterReporter.prototype.reportRunnerStarting = function() {
    if (isAborted()) {
      emitAllResultsOnce();
      return;
    }
    emit('tests-start');
  };

  JasmineAdapterReporter.prototype.reportSpecStarting = function(spec) {
    if (isAborted()) {
      emitAllResultsOnce();
      return;
    }
    var currentTest = {
      name: spec.getFullName()
    };
    emit('tests-start', currentTest);
  };

  JasmineAdapterReporter.prototype.reportSpecResults = function(spec) {
    if (isAborted()) {
      emitAllResultsOnce();
      return;
    }
    if (spec.results().skipped) {
      return;
    }
    var test = {
      passed: 0,
      failed: 0,
      total: 0,
      id: spec.id + 1,
      name: spec.getFullName(),
      items: []
    };

    var items = spec.results().getItems();

    for (var i = 0, len = items.length; i < len; i++) {
      var item = items[i];
      if (item.type === 'log') {
        continue;
      }
      var passed = item.passed();
      test.total++;
      if (passed) {
        test.passed++;
      } else {
        test.failed++;
      }
      test.items.push({
        passed: passed,
        message: item.message,
        stack: item.trace.stack ? item.trace.stack : undefined
      });
    }

    results.total++;
    if (test.failed > 0) {
      results.failed++;
    } else {
      results.passed++;
    }

    if (isAborted()) {
      emitAllResultsOnce();
      return;
    }
    emit('test-result', test);
  };
  JasmineAdapterReporter.prototype.reportRunnerResults = function() {
    if (isAborted()) {
      emitAllResultsOnce();
      return;
    }
    emitAllResultsOnce();
  };
  jasmine.getEnv().addReporter(new JasmineAdapterReporter());

}
