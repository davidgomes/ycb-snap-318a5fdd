#
# SPDX-License-Identifier: Apache-2.0
import json
import os
import tempfile
import time
from unittest import mock

import fixtures
import testtools

from bandit.core import config
from bandit.core import incremental_cache as inc_cache
from bandit.core import manager


class IncrementalCacheTests(testtools.TestCase):
    def setUp(self):
        super().setUp()
        self.cache_dir = self.useFixture(fixtures.TempDir()).path
        self.profile = {
            "include": {"B101"},
            "exclude": set(),
        }
        self.cache_key = inc_cache.build_cache_key(
            1, 1, None, self.profile
        )

    def _make_cache(self, **kwargs):
        defaults = {
            "cache_dir": self.cache_dir,
            "cache_key": self.cache_key,
            "enabled": True,
        }
        defaults.update(kwargs)
        return inc_cache.IncrementalCache(**defaults)

    def test_build_cache_key_includes_profile(self):
        key_a = inc_cache.build_cache_key(1, 1, "p1", self.profile)
        other = {"include": {"B102"}, "exclude": set()}
        key_b = inc_cache.build_cache_key(1, 1, "p1", other)
        self.assertNotEqual(key_a, key_b)

    def test_store_and_lookup_unchanged_file(self):
        cache = self._make_cache()
        sample_file = os.path.join(self.cache_dir, "sample.py")
        with open(sample_file, "w", encoding="utf-8") as handle:
            handle.write("x = 1\n")

        cache.store(sample_file, [], {"loc": 1}, [])
        cache.finalize()

        cache2 = self._make_cache()
        entry = cache2.lookup(sample_file)
        self.assertIsNotNone(entry)
        self.assertEqual(1, cache2.stats.cache_hits)

    def test_file_change_invalidates(self):
        cache = self._make_cache()
        sample_file = os.path.join(self.cache_dir, "sample.py")
        with open(sample_file, "w", encoding="utf-8") as handle:
            handle.write("x = 1\n")

        cache.store(sample_file, [], {"loc": 1}, [])
        cache.finalize()

        time.sleep(0.01)
        with open(sample_file, "a", encoding="utf-8") as handle:
            handle.write("y = 2\n")

        cache2 = self._make_cache()
        entry = cache2.lookup(sample_file)
        self.assertIsNone(entry)
        self.assertEqual(1, cache2.stats.invalidation_counts["file_changed"])

    def test_expiry_zero_expires_all(self):
        cache = self._make_cache(expiry_days=0)
        sample_file = os.path.join(self.cache_dir, "sample.py")
        with open(sample_file, "w", encoding="utf-8") as handle:
            handle.write("x = 1\n")

        cache.store(sample_file, [], {"loc": 1}, [])
        cache.finalize()

        cache2 = self._make_cache(expiry_days=0)
        self.assertIsNone(cache2.lookup(sample_file))
        self.assertEqual(1, cache2.stats.invalidation_counts["expired"])

    def test_corrupted_entry_discarded(self):
        cache = self._make_cache()
        sample_file = os.path.join(self.cache_dir, "sample.py")
        with open(sample_file, "w", encoding="utf-8") as handle:
            handle.write("x = 1\n")

        cache.store(sample_file, [], {"loc": 1}, [])
        cache.finalize()

        entry_id = inc_cache._entry_key(sample_file, self.cache_key)
        entry_path = cache._entry_path(entry_id)
        with open(entry_path, "w", encoding="utf-8") as handle:
            handle.write("{}")

        cache2 = self._make_cache()
        self.assertIsNone(cache2.lookup(sample_file))

    def test_clear_cache_missing_dir_is_noop(self):
        missing = os.path.join(self.cache_dir, "missing")
        cache = inc_cache.IncrementalCache(
            missing, self.cache_key, enabled=True
        )
        cache.clear()

    def test_force_rescan_bypasses_lookup(self):
        cache = self._make_cache(force_rescan=True)
        sample_file = os.path.join(self.cache_dir, "sample.py")
        with open(sample_file, "w", encoding="utf-8") as handle:
            handle.write("x = 1\n")

        cache.store(sample_file, [], {"loc": 1}, [])
        cache.finalize()

        cache2 = self._make_cache(force_rescan=True)
        self.assertIsNone(cache2.lookup(sample_file))
        self.assertEqual(1, cache2.stats.cache_misses)

    def test_export_import_roundtrip(self):
        cache = self._make_cache()
        sample_file = os.path.join(self.cache_dir, "sample.py")
        with open(sample_file, "w", encoding="utf-8") as handle:
            handle.write("x = 1\n")

        cache.store(sample_file, [], {"loc": 1}, [])
        cache.finalize()

        export_path = os.path.join(self.cache_dir, "export.json")
        cache.export_to_file(export_path)

        with open(export_path, encoding="utf-8") as handle:
            exported = json.load(handle)
        self.assertEqual(inc_cache.CACHE_FORMAT_VERSION, exported["format_version"])

        other_dir = os.path.join(self.cache_dir, "other")
        imported = inc_cache.IncrementalCache(
            other_dir, self.cache_key, enabled=True
        )
        imported.import_from_file(export_path)
        entry = imported.lookup(sample_file)
        self.assertIsNotNone(entry)

    def test_import_bad_format_is_graceful(self):
        bad_path = os.path.join(self.cache_dir, "bad.json")
        with open(bad_path, "w", encoding="utf-8") as handle:
            handle.write('{"format_version": 999}')
        cache = self._make_cache()
        cache.import_from_file(bad_path)
        self.assertEqual([], cache.list_cached_files())

    def test_prune_cache(self):
        cache = self._make_cache()
        sample_file = os.path.join(self.cache_dir, "sample.py")
        with open(sample_file, "w", encoding="utf-8") as handle:
            handle.write("x = 1\n")
        cache.store(sample_file, [], {"loc": 1}, [])
        cache.finalize()
        removed = cache.prune(0)
        self.assertEqual(1, removed)

    def test_resolve_cache_settings_defaults(self):
        args = mock.Mock(
            incremental=False,
            no_incremental=False,
            warm_cache=False,
            cache_dir=None,
            cache_size_limit=None,
            force_rescan=True,
        )
        conf = config.BanditConfig()
        settings = inc_cache.resolve_cache_settings(args, conf)
        self.assertFalse(settings["enabled"])
        self.assertFalse(settings["force_rescan"])

    def test_manager_uses_cache(self):
        sample_file = os.path.join(self.cache_dir, "sample.py")
        with open(sample_file, "w", encoding="utf-8") as handle:
            handle.write("assert True\n")

        b_conf = config.BanditConfig()
        profile = {"include": {"B101"}, "exclude": set()}
        cache = self._make_cache()
        b_mgr = manager.BanditManager(
            b_conf,
            "file",
            profile=profile,
            incremental_cache=cache,
        )
        b_mgr.discover_files([sample_file])
        b_mgr.run_tests()
        first_count = len(b_mgr.results)

        cache2 = self._make_cache()
        b_mgr2 = manager.BanditManager(
            b_conf,
            "file",
            profile=profile,
            incremental_cache=cache2,
        )
        b_mgr2.discover_files([sample_file])
        b_mgr2.run_tests()
        self.assertEqual(first_count, len(b_mgr2.results))
        self.assertEqual(1, cache2.stats.cache_hits)

    def test_circular_import_safe(self):
        """Importing cache and manager together must not loop."""
        import importlib

        import bandit.core.incremental_cache
        import bandit.core.manager

        importlib.reload(bandit.core.manager)
        importlib.reload(bandit.core.incremental_cache)
        self.assertIsNotNone(bandit.core.incremental_cache.IncrementalCache)
