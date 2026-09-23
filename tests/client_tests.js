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

  describe('handleAbortTests', function() {
    let posted, originalHandlers;

    beforeEach(function() {
      originalHandlers = Testem.evtHandlers;
      Testem.evtHandlers = {};
      posted = [];
      Testem._isIframeReady = true;
      Testem.iframe = {
        contentWindow: {
          postMessage: function(message) {
            posted.push(JSON.parse(message).data);
          }
        }
      };
    });

    afterEach(function() {
      Testem.aborted = false;
      Testem.evtHandlers = originalHandlers;
    });

    it('sets aborted and emits abort-tests and after-tests-complete directly', function() {
      Testem.handleAbortTests();

      expect(Testem.aborted).to.be.true();
      expect(posted).to.deep.equal([['abort-tests'], ['after-tests-complete']]);
    });

    it('queues the final events until the iframe is ready', function() {
      Testem._isIframeReady = false;
      Testem.emitMessageQueue = [];

      Testem.handleAbortTests();

      expect(Testem.emitMessageQueue.map(message => message.emitArgs)).to.deep.equal([['abort-tests'], ['after-tests-complete']]);
      Testem.emitMessageQueue = [];
    });

    it('blocks further emitMessage calls', function() {
      Testem.handleAbortTests();
      posted = [];

      Testem.emitMessage('test-result', { name: 'late' });
      Testem.emit('all-test-results');

      expect(posted).to.be.empty();
    });
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
});
