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

  it('handleAbortTests sets aborted and emits abort-tests and after-tests-complete', function() {
    let previousHandlers = Testem.evtHandlers;
    Testem.evtHandlers = {};
    Testem.aborted = false;
    let emit = sinon.spy(Testem, 'emit');

    Testem.handleAbortTests();

    expect(Testem.aborted).to.be.true();
    expect(emit).to.have.been.calledWith('abort-tests');
    expect(emit).to.have.been.calledWith('after-tests-complete');

    emit.restore();
    Testem.aborted = false;
    Testem.evtHandlers = previousHandlers;
  });

  it('blocks emitMessage after abort', function() {
    Testem.aborted = true;
    Testem._isIframeReady = true;
    let send = sinon.stub(Testem, 'emitMessageToIframe');

    Testem.emitMessage('test-result', { name: 'late' });

    expect(send).to.not.have.been.called();
    send.restore();
    Testem.aborted = false;
  });
});
