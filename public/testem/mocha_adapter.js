/*

mocha_adapter.js
================

Testem`s adapter for Mocha. It works by monkey-patching `Runner.prototype.emit`.

*/

/* globals mocha, emit, Mocha, Testem */
/* globals module */
/* exported mochaAdapter */
'use strict';

function mochaAdapter() {

  var results = {
    failed: 0,
    passed: 0,
    total: 0,
    pending: 0,
    tests: []
  };
  var id = 1;
  var Runner;
  var ended = false;
  var waiting = 0;

  try {
    Runner = mocha.Runner || Mocha.Runner;
  } catch (e) {
    console.error('Testem: failed to register adapter for mocha.');
  }

  function getFullName(test) {
    var name = '';
    while (test) {
      name = test.title + ' ' + name;
      test = test.parent;
    }
    return name.replace(/^ /, '');
  }

  /* Store a reference to the global setTimeout function, in case it's
  	 * manipulated by test helpers */
  var _setTimeout = setTimeout;

  var oEmit = Runner.prototype.emit;
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

  Runner.prototype.emit = function(evt, test, err) {
    var name = getFullName(test);
    if (isTestemAborted()) {
      signalAbortedAllResults();
      oEmit.apply(this, arguments);
      return;
    }
    if (evt === 'start') {
      emitEvent('tests-start', { name: name });
    } else if (evt === 'end') {
      if (waiting === 0) {
        emitEvent('all-test-results');
      }
      ended = true;
    } else if (evt === 'test end') {
      waiting++;
      _setTimeout(function() {
        if (isTestemAborted()) {
          waiting--;
          signalAbortedAllResults();
          return;
        }
        waiting--;
        if (test.state === 'passed') {
          testPass(test);
        } else if (test.pending) {
          testPending(test);
        }
        if (ended && waiting === 0) {
          emitEvent('all-test-results');
        }
      }, 0);
    } else if (evt === 'fail') {
      if (isTestemAborted()) {
        signalAbortedAllResults();
      } else {
        testFail(test, err);
      }
    }

    oEmit.apply(this, arguments);

    function testPass(test) {
      if (isTestemAborted()) {
        signalAbortedAllResults();
        return;
      }
      var tst = {
        passed: 1,
        failed: 0,
        total: 1,
        pending: 0,
        id: id++,
        name: name,
        runDuration: test.duration,
        items: []
      };
      results.passed++;
      results.total++;
      results.tests.push(tst);
      emitEvent('test-result', tst);
    }

    function makeFailingTest(test, err) {
      err = err || test.err;
      var items = [{
        passed: false,
        message: err.message,
        stack: (err && err.stack) ? err.stack : undefined
      }];
      var tst = {
        passed: 0,
        failed: 1,
        total: 1,
        pending: 0,
        id: id++,
        name: name,
        runDuration: test.duration,
        items: items
      };
      return tst;
    }

    function testFail(test, err) {
      if (isTestemAborted()) {
        signalAbortedAllResults();
        return;
      }
      var tst = makeFailingTest(test, err);
      results.failed++;
      results.total++;
      results.tests.push(tst);
      emitEvent('test-result', tst);

    }

    function testPending() {
      if (isTestemAborted()) {
        signalAbortedAllResults();
        return;
      }
      var tst = {
        passed: 0,
        failed: 0,
        total: 1,
        pending: 1,
        id: id++,
        name: name,
        items: []
      };
      results.total++;
      results.tests.push(tst);
      emitEvent('test-result', tst);
    }
  };

}

// Exporting this as a module so that it can be unit tested in Node.
if (typeof module !== 'undefined') {
  module.exports = mochaAdapter;
}
