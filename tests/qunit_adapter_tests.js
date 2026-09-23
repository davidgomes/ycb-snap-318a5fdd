'use strict';

const expect = require('chai').expect;
const sinon = require('sinon');
const qunitAdapter = require('../public/testem/qunit_adapter');

function replaceGlobals(newGlobals, originalGlobals) {
  for (let key in newGlobals) {
    originalGlobals[key] = global[key];
    global[key] = newGlobals[key];
  }
}

function restoreGlobals(originalGlobals) {
  for (let key in originalGlobals) {
    global[key] = originalGlobals[key];
  }
}

describe('qunitAdapter', function() {
  let globals, _emit, hooks, QUnit;

  let testParams = { module: 'foo', name: 'bar' };
  let doneParams = { failed: 1, passed: 0, skipped: false, todo: false, total: 1, runtime: 3, testId: 'abc' };

  beforeEach(function() {
    globals = {};
    hooks = {};
    _emit = sinon.stub();
    QUnit = {
      config: { queue: [function() {}, function() {}] },
      log: function(fn) { hooks.log = fn; },
      testStart: function(fn) { hooks.testStart = fn; },
      testDone: function(fn) { hooks.testDone = fn; },
      done: function(fn) { hooks.done = fn; }
    };
    replaceGlobals({ emit: _emit, QUnit: QUnit }, globals);
  });

  afterEach(function() {
    restoreGlobals(globals);
  });

  describe('when Testem is not defined', function() {
    beforeEach(function() {
      qunitAdapter();
    });

    it('reports results as usual', function() {
      expect(typeof Testem).to.equal('undefined');

      hooks.testStart(testParams);
      hooks.log({ result: false, message: 'nope', actual: 1, expected: 2 });
      hooks.testDone(doneParams);
      hooks.done({ runtime: 3 });

      expect(_emit.args.map(args => args[0])).to.deep.equal([
        'tests-start',
        'test-result',
        'all-test-results'
      ]);
      expect(_emit.secondCall.args[1].items).to.have.length(1);
      expect(QUnit.config.queue).to.have.length(2);
    });
  });

  describe('when Testem has been aborted', function() {
    beforeEach(function() {
      replaceGlobals({ Testem: { aborted: false } }, globals);
      qunitAdapter();
    });

    it('suppresses events, clears the queue and signals "all-test-results" once', function() {
      hooks.testStart(testParams);
      global.Testem.aborted = true;
      hooks.log({ result: false, message: 'nope' });
      hooks.testDone(doneParams);
      hooks.testStart(testParams);
      hooks.done({ runtime: 3 });

      expect(_emit.args.map(args => args[0])).to.deep.equal([
        'tests-start',
        'all-test-results'
      ]);
      expect(QUnit.config.queue).to.have.length(0);
    });

    it('does not signal "all-test-results" again after the run is done', function() {
      hooks.done({ runtime: 3 });
      global.Testem.aborted = true;
      hooks.testStart(testParams);
      hooks.done({ runtime: 3 });

      expect(_emit).to.have.been.calledOnceWithExactly('all-test-results');
    });
  });
});
