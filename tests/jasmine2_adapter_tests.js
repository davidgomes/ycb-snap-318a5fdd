'use strict';

const expect = require('chai').expect;
const sinon = require('sinon');
const jasmine2Adapter = require('../public/testem/jasmine2_adapter');

describe('jasmine2Adapter', function() {
  let reporter, originals;

  beforeEach(function() {
    originals = { emit: global.emit, jasmine: global.jasmine, Testem: global.Testem };
    global.emit = sinon.stub();
    global.jasmine = {
      getEnv: () => ({ addReporter: r => { reporter = r; } })
    };
    delete global.Testem;
    jasmine2Adapter();
  });

  afterEach(function() {
    Object.keys(originals).forEach(key => {
      if (originals[key] === undefined) {
        delete global[key];
      } else {
        global[key] = originals[key];
      }
    });
  });

  const spec = { id: 0, fullName: 'a spec', status: 'passed', failedExpectations: [] };

  it('reports results when Testem is not defined', function() {
    reporter.jasmineStarted();
    reporter.specStarted(spec);
    reporter.specDone(spec);
    reporter.jasmineDone();

    expect(global.emit.args.map(args => args[0])).to.deep.equal(['tests-start', 'tests-start', 'test-result', 'all-test-results']);
  });

  it('suppresses events once aborted and signals "all-test-results" once', function() {
    reporter.jasmineStarted();
    global.Testem = { aborted: true };

    reporter.specStarted(spec);
    reporter.specDone(spec);
    reporter.jasmineDone();

    expect(global.emit.args.map(args => args[0])).to.deep.equal(['tests-start', 'all-test-results']);
  });
});
