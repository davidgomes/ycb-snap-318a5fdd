/*

 jasmine2_adapter.js
 ==================

 Testem's adapter for Jasmine. It works by adding a custom reporter.

 */

/* globals emit, jasmine, Testem */
/* exported jasmine2Adapter */
'use strict';

function jasmine2Adapter() {

  var results = {
    failed: 0,
    passed: 0,
    total: 0,
    pending: 0,
    tests: []
  };

  var allResultsSent = false;

  function isTestemAborted() {
    return typeof Testem !== 'undefined' && Testem.aborted;
  }

  function signalAbortedAllResults() {
    if (allResultsSent) {
      return;
    }
    allResultsSent = true;
    emit('all-test-results');
  }

  function emitEvent(event, payload) {
    if (isTestemAborted()) {
      signalAbortedAllResults();
      return;
    }
    if (arguments.length > 1) {
      emit(event, payload);
    } else {
      emit(event);
    }
  }

  function Jasmine2AdapterReporter() {

    this.jasmineStarted = function() {
      if (isTestemAborted()) {
        signalAbortedAllResults();
        return;
      }
      emitEvent('tests-start');
    };

    this.specStarted = function(spec) {
      if (isTestemAborted()) {
        signalAbortedAllResults();
        return;
      }
      var currentTest = {
        name: spec.fullName
      };
      emitEvent('tests-start', currentTest);
    };

    this.specDone = function(spec) {
      if (isTestemAborted()) {
        signalAbortedAllResults();
        return;
      }

      var test = {
        passed: 0,
        failed: 0,
        total: 0,
        pending: 0,
        id: spec.id + 1,
        name: spec.fullName,
        items: []
      };

      var i, l, failedExpectations, item;

      if (spec.status === 'passed') {
        test.passed++;
        test.total++;
        results.passed++;
      } else if (spec.status === 'pending') {
        test.pending++;
        test.total++;
        results.pending++;
      } else {
        failedExpectations = spec.failedExpectations;
        for (i = 0, l = failedExpectations.length; i < l; i++) {
          item = failedExpectations[i];
          test.items.push({
            passed: item.passed,
            message: item.message,
            stack: item.stack || undefined
          });
        }
        test.failed++;
        results.failed++;
        test.total++;
      }

      results.total++;

      emitEvent('test-result', test);
    };

    this.jasmineDone = function() {
      if (isTestemAborted()) {
        signalAbortedAllResults();
        return;
      }
      emitEvent('all-test-results');
    };

  }

  jasmine.getEnv().addReporter(new Jasmine2AdapterReporter());
}
