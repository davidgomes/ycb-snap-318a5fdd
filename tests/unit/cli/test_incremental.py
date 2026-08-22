#
# SPDX-License-Identifier: Apache-2.0
import json
import os
from unittest import mock

import fixtures
import testtools

from bandit.cli import main as bandit
from bandit.core import cache as b_cache
from bandit.core import config
from bandit.core import manager as b_manager

SAFE_SOURCE = "x = 1\n"
UNSAFE_SOURCE = "import pickle\npickle.loads(b'data')\n"


class IncrementalCLTests(testtools.TestCase):
    def setUp(self):
        super().setUp()
        self.tempdir = self.useFixture(fixtures.TempDir()).path
        self.cache_dir = os.path.join(self.tempdir, "cache")
        self.target = os.path.join(self.tempdir, "target.py")
        with open(self.target, "w") as handle:
            handle.write(UNSAFE_SOURCE)

    def _run(self, extra_args, expect_exit=0):
        argv = ["bandit", "-f", "json"] + extra_args
        with mock.patch("sys.argv", argv):
            try:
                bandit.main()
            except SystemExit as exc:
                self.assertEqual(expect_exit, exc.code)
                return
            self.fail("bandit.main() did not exit")

    def test_clear_cache_missing_directory_is_noop(self):
        missing = os.path.join(self.tempdir, "missing-cache")
        self._run(["--clear-cache", "--cache-dir", missing], expect_exit=0)
        self.assertFalse(os.path.isdir(missing))

    def test_cache_summary_and_stats_without_targets(self):
        with mock.patch("bandit.cli.main.print") as mocked_print:
            self._run(
                ["--cache-summary", "--cache-dir", self.cache_dir],
                expect_exit=0,
            )
            mocked_print.assert_any_call("Cached files: 0")

        with mock.patch("bandit.cli.main.print") as mocked_print:
            self._run(
                ["--cache-stats", "--cache-dir", self.cache_dir],
                expect_exit=0,
            )
            printed = " ".join(
                str(call.args[0]) for call in mocked_print.call_args_list
            )
            self.assertIn("cache_file_size_bytes", printed)

    def test_list_cached_files_one_per_line(self):
        with mock.patch("bandit.cli.main.print") as mocked_print:
            self._run(
                ["--list-cached-files", "--cache-dir", self.cache_dir],
                expect_exit=0,
            )
            # empty cache prints nothing
            self.assertEqual(0, mocked_print.call_count)

    def test_import_malformed_and_incompatible_exit_zero(self):
        bad = os.path.join(self.tempdir, "bad.json")
        with open(bad, "w") as handle:
            handle.write("not-json")
        self._run(
            ["--import-cache", bad, "--cache-dir", self.cache_dir],
            expect_exit=0,
        )

        incompatible = os.path.join(self.tempdir, "old.json")
        with open(incompatible, "w") as handle:
            json.dump({"format_version": 99, "entries": []}, handle)
        self._run(
            [
                "--import-cache",
                incompatible,
                "--cache-dir",
                self.cache_dir,
            ],
            expect_exit=0,
        )

    def test_prune_cache_exit_zero(self):
        self._run(
            ["--prune-cache", "7", "--cache-dir", self.cache_dir],
            expect_exit=0,
        )

    def test_export_cache_includes_format_version(self):
        export_path = os.path.join(self.tempdir, "out.json")
        self._run(
            ["--export-cache", export_path, "--cache-dir", self.cache_dir],
            expect_exit=0,
        )
        with open(export_path, encoding="utf-8") as handle:
            payload = json.load(handle)
        self.assertIn("format_version", payload)

    def test_warm_cache_implies_incremental_empty_results(self):
        output = os.path.join(self.tempdir, "report.json")
        self._run(
            [
                "--warm-cache",
                "--cache-dir",
                self.cache_dir,
                "-o",
                output,
                self.target,
            ],
            expect_exit=0,
        )
        with open(output, encoding="utf-8") as handle:
            payload = json.load(handle)
        self.assertEqual([], payload["results"])
        self.assertIn("cache_info", payload)
        self.assertIn("cache_hits", payload["metrics"]["_totals"])
        self.assertIn("cache_misses", payload["metrics"]["_totals"])

    def test_incremental_hit_on_second_run(self):
        output = os.path.join(self.tempdir, "report.json")
        args = [
            "--incremental",
            "--cache-dir",
            self.cache_dir,
            "-o",
            output,
            self.target,
        ]
        self._run(args, expect_exit=1)
        self._run(args, expect_exit=1)
        with open(output, encoding="utf-8") as handle:
            payload = json.load(handle)
        self.assertGreaterEqual(payload["metrics"]["_totals"]["cache_hits"], 1)
        self.assertEqual(1, payload["cache_info"]["cache_hits"])
        self.assertIn("invalidation_counts", payload["cache_info"])
        for key in (
            "file_changed",
            "config_changed",
            "expired",
            "not_cached",
        ):
            self.assertIn(key, payload["cache_info"]["invalidation_counts"])

    def test_force_rescan_requires_incremental(self):
        output = os.path.join(self.tempdir, "report.json")
        # Populate cache
        self._run(
            [
                "--incremental",
                "--cache-dir",
                self.cache_dir,
                "-o",
                output,
                self.target,
            ],
            expect_exit=1,
        )
        # force-rescan without incremental is not effective (no cache use)
        self._run(
            [
                "--force-rescan",
                "--cache-dir",
                self.cache_dir,
                "-o",
                output,
                self.target,
            ],
            expect_exit=1,
        )
        with open(output, encoding="utf-8") as handle:
            payload = json.load(handle)
        self.assertEqual(0, payload["cache_info"]["cache_hits"])

        # force-rescan with incremental bypasses lookup (miss) but stores
        self._run(
            [
                "--incremental",
                "--force-rescan",
                "--cache-dir",
                self.cache_dir,
                "-o",
                output,
                self.target,
            ],
            expect_exit=1,
        )
        with open(output, encoding="utf-8") as handle:
            payload = json.load(handle)
        self.assertEqual(0, payload["cache_info"]["cache_hits"])
        self.assertGreaterEqual(payload["cache_info"]["cache_misses"], 1)

    def test_analysis_options_are_cache_key(self):
        output = os.path.join(self.tempdir, "report.json")
        self._run(
            [
                "--incremental",
                "--cache-dir",
                self.cache_dir,
                "-t",
                "B403",
                "-o",
                output,
                self.target,
            ],
            expect_exit=1,
        )
        self._run(
            [
                "--incremental",
                "--cache-dir",
                self.cache_dir,
                "-t",
                "B101",
                "-o",
                output,
                self.target,
            ],
            expect_exit=0,
        )
        with open(output, encoding="utf-8") as handle:
            payload = json.load(handle)
        self.assertEqual(0, payload["cache_info"]["cache_hits"])
        self.assertGreaterEqual(
            payload["cache_info"]["invalidation_counts"]["config_changed"],
            1,
        )

    def test_config_file_incremental_analysis_keys(self):
        cfg = os.path.join(self.tempdir, "bandit.yaml")
        with open(cfg, "w") as handle:
            handle.write(
                "incremental_analysis:\n"
                "  enabled: true\n"
                f"  cache_directory: {self.cache_dir}\n"
                "  cache_expiry_days: 30\n"
                "include:\n"
                "  - '*.py'\n"
            )
        output = os.path.join(self.tempdir, "report.json")
        self._run(
            ["-c", cfg, "-o", output, self.target],
            expect_exit=1,
        )
        self.assertTrue(os.path.isdir(self.cache_dir))
        self._run(
            ["-c", cfg, "-o", output, self.target],
            expect_exit=1,
        )
        with open(output, encoding="utf-8") as handle:
            payload = json.load(handle)
        self.assertGreaterEqual(payload["cache_info"]["cache_hits"], 1)

    def test_expiry_days_zero_from_config(self):
        cfg = os.path.join(self.tempdir, "bandit.yaml")
        with open(cfg, "w") as handle:
            handle.write(
                "incremental_analysis:\n"
                "  enabled: true\n"
                f"  cache_directory: {self.cache_dir}\n"
                "  cache_expiry_days: 0\n"
                "include:\n"
                "  - '*.py'\n"
            )
        output = os.path.join(self.tempdir, "report.json")
        self._run(
            ["-c", cfg, "-o", output, self.target],
            expect_exit=1,
        )
        self._run(
            ["-c", cfg, "-o", output, self.target],
            expect_exit=1,
        )
        with open(output, encoding="utf-8") as handle:
            payload = json.load(handle)
        self.assertGreaterEqual(
            payload["cache_info"]["invalidation_counts"]["expired"], 1
        )


class IncrementalManagerTests(testtools.TestCase):
    def test_unchanged_file_returns_cached_results(self):
        tempdir = self.useFixture(fixtures.TempDir()).path
        target = os.path.join(tempdir, "code.py")
        with open(target, "w") as handle:
            handle.write(UNSAFE_SOURCE)
        cache_dir = os.path.join(tempdir, "cache")
        options = {
            "tests": None,
            "skips": None,
            "severity": 1,
            "confidence": 1,
            "profile_name": "",
            "profile": {},
        }
        cache_manager = b_cache.IncrementalCache(
            cache_dir=cache_dir,
            config_key=b_cache.build_config_key(options),
            create=True,
        )
        conf = config.BanditConfig()
        mgr = b_manager.BanditManager(
            conf,
            "file",
            incremental=True,
            cache_manager=cache_manager,
            cache_key_options=options,
        )
        mgr.discover_files([target])
        mgr.run_tests()
        first = list(mgr.results)
        self.assertTrue(first)
        self.assertEqual(1, mgr.cache_info["cache_misses"])

        mgr2 = b_manager.BanditManager(
            conf,
            "file",
            incremental=True,
            cache_manager=b_cache.IncrementalCache(
                cache_dir=cache_dir,
                config_key=b_cache.build_config_key(options),
            ),
            cache_key_options=options,
        )
        mgr2.discover_files([target])
        mgr2.run_tests()
        self.assertEqual(1, mgr2.cache_info["cache_hits"])
        self.assertEqual(len(first), len(mgr2.results))
        self.assertEqual(first[0].test_id, mgr2.results[0].test_id)
