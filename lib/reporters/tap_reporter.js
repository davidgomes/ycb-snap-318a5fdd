'use strict';

const displayutils = require('../utils/displayutils');

module.exports = class TapReporter {
  constructor(silent, out, config) {
    this.out = out || process.stdout;
    this.silent = silent;
    this.quietLogs = !!config.get('tap_quiet_logs');
    this.failsOnly = !!config.get('tap_failed_tests_only');
    this.strictSpecCompliance = !!config.get('tap_strict_spec_compliance');
    this.stoppedOnError = null;
    this.id = 1;
    this.total = 0;
    this.pass = 0;
    this.skipped = 0;
    this.todo = 0;
    this.results = [];
    this.errors = [];
    this.logs = [];
    this.logProcessor = config.get('tap_log_processor');
    this.bailed = false;
    this.bailReason = null;
    this.testsRanBeforeBail = 0;
    this.suppressedAfterBail = 0;
    this.failedCount = 0;
  }

  onBail(report) {
    this.bailed = true;
    this.bailReason = report.bailReason;
    this.testsRanBeforeBail = report.testsRanBeforeBail;
    this.suppressedAfterBail = report.suppressedAfterBail;
    this.failedCount = report.failedCount;
  }

  reset() {
    this.id = 1;
    this.total = 0;
    this.pass = 0;
    this.skipped = 0;
    this.todo = 0;
    this.results = [];
    this.errors = [];
    this.logs = [];
    this.bailed = false;
    this.bailReason = null;
    this.testsRanBeforeBail = 0;
    this.suppressedAfterBail = 0;
    this.failedCount = 0;
    this.stoppedOnError = null;
  }

  report(prefix, data) {
    this.results.push({
      launcher: prefix,
      result: data
    });
    this.display(prefix, data);
    this.total++;

    if (data.skipped) {
      this.skipped++;
    } else if (data.passed && !data.todo) {
      this.pass++;
    } else if (!data.passed && data.todo) {
      this.todo++;
    }
  }

  summaryDisplay() {
    return displayutils.summaryDisplay.call(this);
  }

  /*
   * Based on current settings in this object, will the given value be
   * displayed by 'display'?
   */
  willDisplay(result) {
    let show = !this.silent && !!result && (!this.failsOnly || result.error);
    return show;
  }

  /*
   * Display a formatted message for the result, but only if
   * we've configured to do that.
   */
  display(prefix, result) {
    if (this.willDisplay(result)) {
      this.out.write(displayutils.resultString(this.id++, prefix, result, this.quietLogs, this.strictSpecCompliance, this.logProcessor));
    }
  }

  finish() {
    if (this.silent) {
      return;
    }
    if (this.bailed) {
      this.out.write('Bail out! ' + this.bailReason + ' (' + this.failedCount + ')\n');
    }
    this.out.write('\n' + this.summaryDisplay() + '\n');
  }
};
