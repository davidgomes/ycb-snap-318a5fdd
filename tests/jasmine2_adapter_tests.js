'use strict';

const expect = require('chai').expect;
const sinon = require('sinon');
const jasmine2Adapter = require('../public/testem/jasmine2_adapter');

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

describe('jasmine2Adapter', function() {
  let globals, _emit, reporter;

  let passedSpec = { id: 0, fullName: 'foo passes', status: 'passed', failedExpectations: [] };
  let failedSpec = {
    id: 1,
    fullName: 'foo fails',
    status: 'failed',
    failedExpectations: [{ passed: false, message: 'nope', stack: 'trace' }]
  };

  beforeEach(function() {
    globals = {};
    _emit = sinon.stub();
    replaceGlobals({
      emit: _emit,
      jasmine: {
        getEnv: function() {
          return {
            addReporter: function(r) {
              reporter = r;
            }
          };
        }
      }
    }, globals);
  });

  afterEach(function() {
    restoreGlobals(globals);
  });

  describe('when Testem is not defined', function() {
    beforeEach(function() {
      jasmine2Adapter();
    });

    it('reports results as usual', function() {
      expect(typeof Testem).to.equal('undefined');

      reporter.jasmineStarted();
      reporter.specStarted(passedSpec);
      reporter.specDone(passedSpec);
      reporter.jasmineDone();

      expect(_emit.args.map(args => args[0])).to.deep.equal([
        'tests-start',
        'tests-start',
        'test-result',
        'all-test-results'
      ]);
    });
  });

  describe('when Testem has been aborted', function() {
    beforeEach(function() {
      replaceGlobals({ Testem: { aborted: false } }, globals);
      jasmine2Adapter();
    });

    it('suppresses events and signals "all-test-results" once', function() {
      reporter.jasmineStarted();
      reporter.specStarted(failedSpec);
      global.Testem.aborted = true;
      reporter.specDone(failedSpec);
      reporter.specStarted(passedSpec);
      reporter.specDone(passedSpec);
      reporter.jasmineDone();

      expect(_emit.args.map(args => args[0])).to.deep.equal([
        'tests-start',
        'tests-start',
        'all-test-results'
      ]);
    });

    it('does not signal "all-test-results" again after the run is done', function() {
      reporter.jasmineDone();
      global.Testem.aborted = true;
      reporter.specDone(passedSpec);
      reporter.jasmineDone();

      expect(_emit).to.have.been.calledOnceWithExactly('all-test-results');
    });
  });
});
