/*

 jasmine2_adapter.js
 ==================

 Testem's adapter for Jasmine. It works by adding a custom reporter.

 */

/* globals emit, jasmine, Testem */
/* globals module */
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
  var abortSignaled = false;

  function suppressBecauseAborted() {
    if (typeof Testem !== 'undefined' && Testem.aborted) {
      if (!abortSignaled) {
        abortSignaled = true;
        emit('all-test-results');
      }
      return true;
    }
    return false;
  }

  function Jasmine2AdapterReporter() {

    this.jasmineStarted = function() {
      if (suppressBecauseAborted()) {
        return;
      }
      emit('tests-start');
    };

    this.specStarted = function(spec) {
      if (suppressBecauseAborted()) {
        return;
      }
      var currentTest = {
        name: spec.fullName
      };
      if (suppressBecauseAborted()) {
        return;
      }
      emit('tests-start', currentTest);
    };

    this.specDone = function(spec) {
      if (suppressBecauseAborted()) {
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

      if (suppressBecauseAborted()) {
        return;
      }
      emit('test-result', test);
    };

    this.jasmineDone = function() {
      if (suppressBecauseAborted()) {
        return;
      }
      emit('all-test-results');
    };

  }

  jasmine.getEnv().addReporter(new Jasmine2AdapterReporter());
}

// Exporting this as a module so that it can be unit tested in Node.
if (typeof module !== 'undefined') {
  module.exports = jasmine2Adapter;
}
