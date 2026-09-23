'use strict';

process.stdout.write([
  'TAP version 13',
  'not ok 1 first failure',
  'not ok 2 second failure',
  ''
].join('\n'));

// Keep running until killed so tests can verify the run was aborted.
setInterval(function() {}, 1000);
