#
# SPDX-License-Identifier: Apache-2.0
import json
import os
import tempfile
import time

import testtools

from bandit.core import cache
from bandit.core import issue


class CacheHelperTests(testtools.TestCase):
    def test_parse_size_limit(self):
        self.assertIsNone(cache.parse_size_limit(None))
        self.assertIsNone(cache.parse_size_limit(""))
        self.assertEqual(100, cache.parse_size_limit(100))
        self.assertEqual(2048, cache.parse_size_limit("2KB"))
        self.assertEqual(1024 * 1024, cache.parse_size_limit("1MB"))
        self.assertEqual(10, cache.parse_size_limit("10"))

    def test_as_bool(self):
        self.assertFalse(cache.as_bool(None))
        self.assertTrue(cache.as_bool("true"))
        self.assertTrue(cache.as_bool(True))
        self.assertFalse(cache.as_bool("off"))

    def test_build_config_key_includes_options_and_profile(self):
        key_a = cache.build_config_key(
            {
                "tests": "B101",
                "skips": "B201",
                "severity": 2,
                "confidence": 1,
                "profile_name": "secure",
                "profile": {"include": {"B101"}, "exclude": set()},
            }
        )
        key_b = cache.build_config_key(
            {
                "tests": "B101",
                "skips": "B201",
                "severity": 3,
                "confidence": 1,
                "profile_name": "secure",
                "profile": {"include": {"B101"}, "exclude": set()},
            }
        )
        key_c = cache.build_config_key(
            {
                "tests": "B101",
                "skips": "B201",
                "severity": 2,
                "confidence": 1,
                "profile_name": "other",
                "profile": {"include": {"B101"}, "exclude": set()},
            }
        )
        key_d = cache.build_config_key(
            {
                "tests": "B101",
                "skips": "B201",
                "severity": 2,
                "confidence": 1,
                "profile_name": "secure",
                "profile": {"include": {"B102"}, "exclude": set()},
            }
        )
        self.assertNotEqual(key_a, key_b)
        self.assertNotEqual(key_a, key_c)
        self.assertNotEqual(key_a, key_d)
        self.assertEqual(
            key_a,
            cache.build_config_key(
                {
                    "tests": "B101",
                    "skips": "B201",
                    "severity": 2,
                    "confidence": 1,
                    "profile_name": "secure",
                    "profile": {"include": {"B101"}, "exclude": set()},
                }
            ),
        )

    def test_extract_imports(self):
        source = "import os\nfrom localmod import thing\n"
        modules = cache.extract_imports(source)
        self.assertIn("os", modules)
        self.assertIn("localmod", modules)

    def test_circular_imports_do_not_loop(self):
        temp = tempfile.mkdtemp()
        self.addCleanup(self._rmtree, temp)
        file_a = os.path.join(temp, "circ_a.py")
        file_b = os.path.join(temp, "circ_b.py")
        with open(file_a, "w") as handle:
            handle.write("import circ_b\n")
        with open(file_b, "w") as handle:
            handle.write("import circ_a\n")
        deps = cache.collect_dependencies(file_a, "import circ_b\n")
        paths = {item["path"] for item in deps}
        self.assertIn(cache.normalize_path(file_b), paths)
        # Walking A -> B -> A must terminate and include each file once.
        self.assertEqual(1, len(paths))

    def _rmtree(self, path):
        for root, dirs, files in os.walk(path, topdown=False):
            for name in files:
                os.remove(os.path.join(root, name))
            for name in dirs:
                os.rmdir(os.path.join(root, name))
        os.rmdir(path)


class IncrementalCacheTests(testtools.TestCase):
    def setUp(self):
        super().setUp()
        self.cache_dir = tempfile.mkdtemp()
        self.addCleanup(self._rmtree, self.cache_dir)
        self.cache = cache.IncrementalCache(
            cache_dir=self.cache_dir,
            config_key="cfg",
            create=True,
        )

    def _rmtree(self, path):
        if not os.path.isdir(path):
            return
        for root, dirs, files in os.walk(path, topdown=False):
            for name in files:
                try:
                    os.remove(os.path.join(root, name))
                except OSError:
                    pass
            for name in dirs:
                try:
                    os.rmdir(os.path.join(root, name))
                except OSError:
                    pass
        try:
            os.rmdir(path)
        except OSError:
            pass

    def _store_sample(self, path, content="print(1)\n"):
        with open(path, "w") as handle:
            handle.write(content)
        file_hash = cache.hash_file(path)
        self.cache.store(
            path,
            file_hash,
            results=[],
            metrics={"loc": 1},
            score={"SEVERITY": [0, 0, 0, 0], "CONFIDENCE": [0, 0, 0, 0]},
        )
        self.cache.save()
        return file_hash

    def test_directory_created_if_missing(self):
        missing = os.path.join(self.cache_dir, "nested", "cache")
        cache.IncrementalCache(cache_dir=missing, create=True)
        self.assertTrue(os.path.isdir(missing))

    def test_lookup_hit_and_file_changed(self):
        target = os.path.join(self.cache_dir, "sample.py")
        file_hash = self._store_sample(target, "x = 1\n")
        entry, reason = self.cache.lookup(target, file_hash=file_hash)
        self.assertIsNotNone(entry)
        self.assertIsNone(reason)

        with open(target, "w") as handle:
            handle.write("x = 2\n")
        entry, reason = self.cache.lookup(target)
        self.assertIsNone(entry)
        self.assertEqual("file_changed", reason)

    def test_config_changed_and_not_cached(self):
        target = os.path.join(self.cache_dir, "sample.py")
        self._store_sample(target)
        other = cache.IncrementalCache(
            cache_dir=self.cache_dir, config_key="other"
        )
        entry, reason = other.lookup(target)
        self.assertIsNone(entry)
        self.assertEqual("config_changed", reason)

        missing = os.path.join(self.cache_dir, "nope.py")
        with open(missing, "w") as handle:
            handle.write("pass\n")
        entry, reason = self.cache.lookup(missing)
        self.assertIsNone(entry)
        self.assertEqual("not_cached", reason)

    def test_expiry_zero_expires_all(self):
        target = os.path.join(self.cache_dir, "sample.py")
        self._store_sample(target)
        expired = cache.IncrementalCache(
            cache_dir=self.cache_dir,
            config_key="cfg",
            expiry_days=0,
        )
        entry, reason = expired.lookup(target)
        self.assertIsNone(entry)
        self.assertEqual("expired", reason)

    def test_clear_cache_missing_directory_is_noop(self):
        missing = os.path.join(self.cache_dir, "does-not-exist")
        empty = cache.IncrementalCache(cache_dir=missing)
        empty.clear()
        self.assertFalse(os.path.isdir(missing))

    def test_corrupted_entries_are_discarded(self):
        target = os.path.join(self.cache_dir, "sample.py")
        self._store_sample(target)
        with open(self.cache.cache_file, encoding="utf-8") as handle:
            payload = json.load(handle)
        payload["entries"][0]["checksum"] = "not-valid"
        with open(self.cache.cache_file, "w", encoding="utf-8") as handle:
            json.dump(payload, handle)
        reloaded = cache.IncrementalCache(
            cache_dir=self.cache_dir, config_key="cfg"
        )
        self.assertEqual({}, reloaded.entries)

    def test_export_and_import_merge(self):
        target = os.path.join(self.cache_dir, "sample.py")
        self._store_sample(target)
        export_path = os.path.join(self.cache_dir, "export.json")
        self.cache.export_to(export_path)
        with open(export_path, encoding="utf-8") as handle:
            payload = json.load(handle)
        self.assertEqual(cache.CACHE_FORMAT_VERSION, payload["format_version"])

        other_dir = os.path.join(self.cache_dir, "other")
        other = cache.IncrementalCache(cache_dir=other_dir, create=True)
        merged = other.import_from(export_path)
        self.assertEqual(1, merged)
        self.assertEqual(1, len(other.list_files()))

    def test_import_incompatible_or_malformed_is_discarded(self):
        bad_version = os.path.join(self.cache_dir, "bad-version.json")
        with open(bad_version, "w") as handle:
            json.dump({"format_version": 99, "entries": []}, handle)
        self.assertEqual(0, self.cache.import_from(bad_version))

        malformed = os.path.join(self.cache_dir, "malformed.json")
        with open(malformed, "w") as handle:
            handle.write("{not json")
        self.assertEqual(0, self.cache.import_from(malformed))

    def test_list_files_and_stats_and_prune(self):
        target = os.path.join(self.cache_dir, "sample.py")
        self._store_sample(target)
        files = self.cache.list_files()
        self.assertEqual(1, len(files))
        stats = self.cache.stats()
        self.assertIn("cache_file_size_bytes", stats)
        self.assertGreater(stats["cache_file_size_bytes"], 0)

        self.cache.entries[cache.normalize_path(target)]["created_at"] = (
            time.time() - 10 * 86400
        )
        removed = self.cache.prune(1)
        self.assertEqual(1, removed)
        self.assertEqual([], self.cache.list_files())

    def test_force_rescan_bypasses_lookup(self):
        target = os.path.join(self.cache_dir, "sample.py")
        file_hash = self._store_sample(target)
        entry, reason = self.cache.lookup(
            target, file_hash=file_hash, force_rescan=True
        )
        self.assertIsNone(entry)
        self.assertEqual("not_cached", reason)

    def test_serialize_issue_roundtrip(self):
        item = issue.Issue("MEDIUM", text="demo")
        item.fname = "demo.py"
        item.test = "demo_test"
        item.test_id = "B000"
        item.lineno = 1
        item.linerange = [1]
        data = cache.serialize_issue(item)
        restored = cache.deserialize_issue(data)
        self.assertEqual(item.fname, restored.fname)
        self.assertEqual(item.text, restored.text)
