'use strict';

const expect = require('chai').expect;
const sinon = require('sinon');
const Testem = require('../public/testem/testem_client');

describe('Testem Client', function() {
  it('passes new socket to each custom adapter', function() {
    let socket1, socket2;

    Testem.useCustomAdapter(function(socket) {
      socket1 = socket;
    });

    Testem.useCustomAdapter(function(socket) {
      socket2 = socket;
    });

    expect(socket1).to.not.equal(socket2);
  });

  it('doesn\'t decycle build-in messages', function() {
    let decycleDepth = 10;

    global.decycle = sinon.spy();

    Testem._isIframeReady = true;

    Testem.useCustomAdapter(function(socket) {
      socket.iframe = {
        contentWindow: {
          postMessage: function() {}
        }
      };

      socket.decycleDepth = decycleDepth;
      socket.emitMessage('test');
    });

    sinon.assert.notCalled(global.decycle);
  });

  it('emits message with custom decycle depth to iframe for user messages', function() {
    let decycleDepth = 10;

    global.decycle = sinon.spy();

    Testem._isIframeReady = true;

    Testem.useCustomAdapter(function(socket) {
      socket.iframe = {
        contentWindow: {
          postMessage: function() {}
        }
      };

      socket.decycleDepth = decycleDepth;
      socket.emitMessage('browser-console', 'log', 'test');
    });

    sinon.assert.calledWithExactly(global.decycle, sinon.match.any, decycleDepth + 1);
  });

  it('drains message with custom decycle depth from queue', function() {
    let decycleDepth = 10;

    global.decycle = sinon.spy();

    Testem.emitMessageQueue = [];
    Testem._isIframeReady = false;

    Testem.useCustomAdapter(function(socket) {
      socket.iframe = {
        contentWindow: {
          postMessage: function() {}
        }
      };

      socket.decycleDepth = decycleDepth;
      socket.emitMessage('browser-console', 'log', 'test');
    });

    expect(Testem.emitMessageQueue).to.not.be.empty();

    Testem.drainMessageQueue();

    sinon.assert.calledWithExactly(global.decycle, sinon.match.any, decycleDepth + 1);
  });

  it('runs registered hooks after all tests finished', function(done) {
    let firstCalled = false;
    let secondCalled = false;
    Testem.afterTests(function(config, data, cb) {
      firstCalled = true;
      cb();
    });

    Testem.afterTests(function(config, data, cb) {
      secondCalled = true;
      cb();
    });

    Testem.on('after-tests-complete', function() {
      expect(firstCalled).to.be.true();
      expect(secondCalled).to.be.true();
      done();
    });
    Testem.runAfterTests();
  });

  describe('handleAbortTests', function() {
    let postMessage;

    beforeEach(function() {
      Testem.aborted = false;
      Testem._isIframeReady = true;
      postMessage = sinon.spy();
      Testem.iframe = {
        contentWindow: {
          postMessage: postMessage
        }
      };
    });

    afterEach(function() {
      Testem.aborted = false;
      delete Testem.iframe;
    });

    function sentEvents() {
      return postMessage.args.map(function(args) {
        return JSON.parse(args[0]).data[0];
      });
    }

    it('starts off not aborted', function() {
      expect(Testem.aborted).to.be.false();
    });

    it('sets aborted and directly emits abort-tests and after-tests-complete', function() {
      Testem.handleAbortTests();

      expect(Testem.aborted).to.be.true();
      expect(sentEvents()).to.deep.equal(['abort-tests', 'after-tests-complete']);
    });

    it('blocks further emitMessage calls', function() {
      Testem.handleAbortTests();
      Testem.emitMessage('test-result', { name: 'after abort' });
      Testem.emitMessage('all-test-results');

      expect(sentEvents()).to.deep.equal(['abort-tests', 'after-tests-complete']);
    });

    it('only emits once when called repeatedly', function() {
      Testem.handleAbortTests();
      Testem.handleAbortTests();

      expect(sentEvents()).to.deep.equal(['abort-tests', 'after-tests-complete']);
    });

    it('enqueues the abort messages until the iframe is ready', function() {
      Testem._isIframeReady = false;
      Testem.emitMessageQueue = [];

      Testem.handleAbortTests();

      expect(postMessage).not.to.have.been.called();
      expect(Testem.emitMessageQueue.map(function(message) {
        return message.emitArgs[0];
      })).to.deep.equal(['abort-tests', 'after-tests-complete']);

      Testem.drainMessageQueue();
      expect(sentEvents()).to.deep.equal(['abort-tests', 'after-tests-complete']);
    });
  });
});
