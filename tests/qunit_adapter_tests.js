'use strict';

const expect = require('chai').expect;
const sinon = require('sinon');
const qunitAdapter = require('../public/testem/qunit_adapter');

describe('qunitAdapter', function() {
  let hooks, originals;

  beforeEach(function() {
    originals = { emit: global.emit, QUnit: global.QUnit, Testem: global.Testem };
    hooks = {};
    global.emit = sinon.stub();
    global.QUnit = { config: { queue: [function() {}, function() {}] } };
    ['log', 'testStart', 'testDone', 'done'].forEach(name => {
      global.QUnit[name] = fn => { hooks[name] = fn; };
    });
    delete global.Testem;
    qunitAdapter();
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

  function runTest() {
    hooks.testStart({ module: 'mod', name: 'test' });
    hooks.log({ result: true, message: 'ok' });
    hooks.testDone({ failed: 0, passed: 1, total: 1, runtime: 1 });
  }

  it('reports results when Testem is not defined', function() {
    runTest();
    hooks.done({ runtime: 1 });

    expect(global.emit.args.map(args => args[0])).to.deep.equal(['tests-start', 'test-result', 'all-test-results']);
    expect(global.QUnit.config.queue).to.have.lengthOf(2);
  });

  it('suppresses events once aborted, clears the queue and signals "all-test-results" once', function() {
    global.Testem = { aborted: true };

    runTest();
    hooks.done({ runtime: 1 });

    expect(global.emit.args.map(args => args[0])).to.deep.equal(['all-test-results']);
    expect(global.QUnit.config.queue).to.be.empty();
  });

  it('suppresses the result of a test aborted while running', function() {
    hooks.testStart({ name: 'test' });
    global.Testem = { aborted: true };
    hooks.log({ result: false, message: 'nope' });
    hooks.testDone({ failed: 1, passed: 0, total: 1, runtime: 1 });
    hooks.done({ runtime: 1 });

    expect(global.emit.args.map(args => args[0])).to.deep.equal(['tests-start', 'all-test-results']);
  });
});
