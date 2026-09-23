

const displayutils = require('../utils/displayutils');

module.exports = class TapReporter {
  constructor(silent, out, config) {
    this.out = out || process.stdout;
    this.silent = silent;
    this.quietLogs = !!config.get('tap_quiet_logs');
    this.failsOnly = !!config.get('tap_failed_tests_only');
    this.strictSpecCompliance = !!config.get('tap_strict_spec_compliance');
    this.showLauncherSummary = !!config.get('tap_show_launcher_summary');
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
    let summary = displayutils.summaryDisplay.call(this);

    if (this.showLauncherSummary) {
      summary = this.launcherSummaryDisplay() + '\n' + summary;
    }

    return summary;
  }

  getLauncherStats() {
    let stats = {};

    this.results.forEach(entry => {
      let launcher = entry.launcher === undefined || entry.launcher === null ? 'unknown' : String(entry.launcher);
      let result = entry.result;
      let stat = stats[launcher] || (stats[launcher] = { total: 0, pass: 0, fail: 0, skip: 0, todo: 0 });

      stat.total++;
      if (result.skipped) {
        stat.skip++;
      } else if (result.passed && !result.todo) {
        stat.pass++;
      } else if (!result.passed && result.todo) {
        stat.todo++;
      } else {
        stat.fail++;
      }
    });

    return stats;
  }

  launcherSummaryDisplay() {
    let stats = this.getLauncherStats();
    let lines = ['# Per-launcher summary'];

    Object.keys(stats).forEach(launcher => {
      let stat = stats[launcher];
      lines.push(`# ${launcher}: ${stat.total} tests, ${stat.pass} pass, ${stat.fail} fail, ${stat.skip} skip`);
    });

    return lines.join('\n');
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
    this.out.write('\n' + this.summaryDisplay() + '\n');
  }
};
