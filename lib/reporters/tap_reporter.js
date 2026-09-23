

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
    this.launcherStats = {};
    this.finished = false;
    this.logProcessor = config.get('tap_log_processor');
  }

  report(prefix, data) {
    this.results.push({
      launcher: prefix,
      result: data
    });
    this.display(prefix, data);
    this.total++;
    this._recordLauncher(prefix, data);

    if (data.skipped) {
      this.skipped++;
    } else if (data.passed && !data.todo) {
      this.pass++;
    } else if (!data.passed && data.todo) {
      this.todo++;
    }
  }

  _recordLauncher(prefix, data) {
    let name = prefix === undefined || prefix === null || prefix === '' ? 'unknown' : String(prefix);
    if (!this.launcherStats[name]) {
      this.launcherStats[name] = { total: 0, pass: 0, fail: 0, skip: 0 };
    }

    let stats = this.launcherStats[name];
    stats.total++;

    if (data.skipped) {
      stats.skip++;
    } else if (data.passed && !data.todo) {
      stats.pass++;
    } else if (!data.todo || data.passed) {
      stats.fail++;
    }
  }

  summaryDisplay() {
    let summary = displayutils.summaryDisplay.call(this);
    if (!this.showLauncherSummary) {
      return summary;
    }

    let names = Object.keys(this.launcherStats);
    if (!names.length) {
      return summary;
    }

    let lines = ['# Per-launcher summary'];
    names.forEach(name => {
      let stats = this.launcherStats[name];
      lines.push('# ' + name + ': ' + stats.total + ' tests, ' + stats.pass + ' pass, ' + stats.fail + ' fail, ' + stats.skip + ' skip');
    });
    let section = lines.join('\n');

    if (summary.lastIndexOf('\n# ok') === summary.length - 5) {
      return summary.slice(0, -5) + '\n' + section + '\n\n# ok';
    }

    return summary + '\n' + section;
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
    if (this.silent || this.finished) {
      return;
    }
    this.finished = true;
    this.out.write('\n' + this.summaryDisplay() + '\n');
  }
};
