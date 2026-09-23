"""Tests for the incremental analysis cache."""

import hashlib
import json
import os
import subprocess
import sys
import threading
from pathlib import Path

import pytest

from vulture import cache
from vulture.core import Vulture
from vulture.utils import ExitCode

from . import REPO


def _reports(vulture):
    return [item.get_report() for item in vulture.get_unused_code()]


def _write(path, text):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)
    return path


def _scavenge(paths, cache_dir, cache_settings=None, **kwargs):
    vulture = Vulture(
        cache_dir=cache_dir, cache_settings=cache_settings, **kwargs
    )
    vulture.scavenge(paths)
    return vulture


def test_normalize_path_is_absolute_and_stable(tmp_path):
    path = _write(tmp_path / "Foo.py", "x = 1\n")
    normalized = cache.normalize_path(path)
    assert os.path.isabs(normalized)
    assert normalized == cache.normalize_path(os.fspath(path))
    assert normalized == os.path.normcase(
        os.path.normpath(os.path.realpath(path))
    )
    assert os.path.exists(normalized)


def test_get_cache_path_points_at_cache_json(tmp_path):
    path = cache.get_cache_path(tmp_path)
    assert isinstance(path, Path)
    assert path == tmp_path / "cache.json"


def test_importlib_imported_at_module_scope():
    assert hasattr(cache, "importlib")
    source = Path(cache.__file__).read_text(encoding="utf-8")
    assert "import importlib" in source
    assert "importlib.metadata.version" in source


def test_missing_cache_is_silent_full_scan(tmp_path, capsys):
    path = _write(tmp_path / "mod.py", "def unused():\n    return 1\n")
    vulture = _scavenge([path], tmp_path / "cache")
    captured = capsys.readouterr()
    assert cache.CORRUPT_CACHE_MESSAGE not in captured.err
    assert vulture._cache_stats["scanned"] == {cache.normalize_path(path)}
    assert vulture._cache_stats["reused"] == set()
    assert "unused function 'unused'" in _reports(vulture)[0]


def test_first_save_writes_backup_and_meta(tmp_path):
    path = _write(tmp_path / "mod.py", "def unused():\n    return 1\n")
    cache_dir = tmp_path / "cache"
    _scavenge([path], cache_dir)
    cache_path = cache.get_cache_path(cache_dir)
    backup = cache_dir / "cache.json.bak"
    meta = cache_dir / "cache.json.meta"
    assert cache_path.is_file()
    assert backup.is_file()
    assert meta.is_file()
    raw = cache_path.read_bytes()
    assert backup.read_bytes() == raw
    payload = json.loads(raw.decode("utf-8"))
    assert cache.normalize_path(path) in payload["modules"]
    meta_payload = json.loads(meta.read_text(encoding="utf-8"))
    assert meta_payload["sha256"] == hashlib.sha256(raw).hexdigest()


def test_second_run_reuses_unchanged_files(tmp_path):
    path = _write(tmp_path / "mod.py", "def unused():\n    return 1\n")
    cache_dir = tmp_path / "cache"
    first = _scavenge([path], cache_dir)
    second = _scavenge([path], cache_dir)
    assert first._cache_stats["reused"] == set()
    assert second._cache_stats["scanned"] == set()
    assert second._cache_stats["reused"] == {cache.normalize_path(path)}
    assert _reports(first) == _reports(second)


def test_cached_report_matches_uncached(tmp_path):
    root = tmp_path / "proj"
    _write(
        root / "a.py",
        "def unused_a():\n"
        "    return 1\n"
        "def used_a():\n"
        "    return 2\n"
        "used_a()\n"
        "import b\n",
    )
    _write(
        root / "b.py",
        "def unused_b():\n"
        "    return 1\n"
        "def used_b():\n"
        "    return used_b\n"
        "x = 1\n"
        "if False:\n"
        "    y = 2\n",
    )
    fresh = Vulture()
    fresh.scavenge([root])
    cached = _scavenge([root], tmp_path / "cache")
    again = _scavenge([root], tmp_path / "cache")
    assert _reports(cached) == _reports(fresh)
    assert _reports(again) == _reports(fresh)
    assert again._cache_stats["scanned"] == set()
    assert cached.exit_code == fresh.exit_code


def test_only_changed_file_and_importers_are_rescanned(tmp_path):
    root = tmp_path / "proj"
    files = {
        "a.py": "import b\n",
        "b.py": "import c\n",
        "c.py": "VALUE = 1\n",
        "d.py": "OTHER = 1\n",
    }
    for name, text in files.items():
        _write(root / name, text)
    cache_dir = tmp_path / "cache"
    _scavenge([root], cache_dir)
    _write(root / "c.py", "VALUE = 2\n")
    scanned = []
    original = Vulture.scan

    def track(self, code, filename=""):
        scanned.append(Path(filename).name)
        return original(self, code, filename)

    monkeypatch = pytest.MonkeyPatch()
    monkeypatch.setattr(Vulture, "scan", track)
    try:
        vulture = _scavenge([root], cache_dir)
    finally:
        monkeypatch.undo()
    assert set(scanned) == {"a.py", "b.py", "c.py"}
    keys = {
        "a.py": cache.normalize_path(root / "a.py"),
        "b.py": cache.normalize_path(root / "b.py"),
        "c.py": cache.normalize_path(root / "c.py"),
        "d.py": cache.normalize_path(root / "d.py"),
    }
    assert vulture._cache_stats["scanned"] == {
        keys["a.py"],
        keys["b.py"],
        keys["c.py"],
    }
    assert vulture._cache_stats["reused"] == {keys["d.py"]}


def test_importers_are_rescanned_not_importees(tmp_path):
    root = tmp_path / "proj"
    _write(root / "a.py", "import b\nVALUE = 1\n")
    _write(root / "b.py", "VALUE = 2\n")
    cache_dir = tmp_path / "cache"
    _scavenge([root], cache_dir)
    _write(root / "a.py", "import b\nVALUE = 3\n")
    vulture = _scavenge([root], cache_dir)
    assert vulture._cache_stats["scanned"] == {
        cache.normalize_path(root / "a.py")
    }
    assert vulture._cache_stats["reused"] == {
        cache.normalize_path(root / "b.py")
    }


def test_package_relative_imports_are_transitive(tmp_path):
    root = tmp_path / "proj"
    _write(root / "pkg" / "__init__.py", "from . import a\n")
    _write(root / "pkg" / "a.py", "from . import b\n")
    _write(root / "pkg" / "b.py", "VALUE = 1\n")
    _write(root / "pkg" / "c.py", "OTHER = 1\n")
    cache_dir = tmp_path / "cache"
    _scavenge([root], cache_dir)
    _write(root / "pkg" / "b.py", "VALUE = 2\n")
    vulture = _scavenge([root], cache_dir)
    scanned = vulture._cache_stats["scanned"]
    reused = vulture._cache_stats["reused"]
    assert cache.normalize_path(root / "pkg" / "b.py") in scanned
    assert cache.normalize_path(root / "pkg" / "a.py") in scanned
    assert cache.normalize_path(root / "pkg" / "__init__.py") in scanned
    assert reused == {cache.normalize_path(root / "pkg" / "c.py")}


def test_used_names_are_stored_per_file(tmp_path):
    root = tmp_path / "proj"
    kept = _write(root / "kept.py", "def foo():\n    return 1\nfoo()\n")
    _write(root / "other.py", "def foo():\n    return 2\nfoo()\n")
    cache_dir = tmp_path / "cache"
    _scavenge([root], cache_dir)
    (root / "other.py").unlink()
    vulture = _scavenge([kept], cache_dir)
    assert vulture._cache_stats["reused"] == {cache.normalize_path(kept)}
    assert _reports(vulture) == []


def test_deleted_and_renamed_files_are_removed(tmp_path):
    root = tmp_path / "proj"
    alpha = _write(root / "alpha.py", "VALUE = 1\n")
    beta = _write(root / "beta.py", "VALUE = 2\n")
    cache_dir = tmp_path / "cache"
    _scavenge([alpha, beta], cache_dir)
    beta.unlink()
    gamma = _write(root / "gamma.py", "VALUE = 2\n")
    _scavenge([alpha, gamma], cache_dir)
    payload = json.loads(cache.get_cache_path(cache_dir).read_text())
    modules = payload["modules"]
    assert cache.normalize_path(alpha) in modules
    assert cache.normalize_path(gamma) in modules
    assert cache.normalize_path(beta) not in modules


def test_cache_settings_change_rescans_everything(tmp_path, capsys):
    path = _write(tmp_path / "mod.py", "def unused():\n    return 1\n")
    cache_dir = tmp_path / "cache"
    _scavenge([path], cache_dir, cache_settings={"mode": "a"})
    capsys.readouterr()
    vulture = _scavenge([path], cache_dir, cache_settings={"mode": "b"})
    captured = capsys.readouterr()
    assert cache.CORRUPT_CACHE_MESSAGE not in captured.err
    assert vulture._cache_stats["scanned"] == {cache.normalize_path(path)}
    assert vulture._cache_stats["reused"] == set()


def test_runtime_signature_changes_rescan(tmp_path, capsys, monkeypatch):
    path = _write(tmp_path / "mod.py", "VALUE = 1\n")
    cache_dir = tmp_path / "cache"
    _scavenge([path], cache_dir)
    monkeypatch.setattr(cache, "__version__", "other-cache-version")
    capsys.readouterr()
    vulture = _scavenge([path], cache_dir)
    captured = capsys.readouterr()
    assert cache.CORRUPT_CACHE_MESSAGE not in captured.err
    assert vulture._cache_stats["scanned"] == {cache.normalize_path(path)}


def test_vulture_version_is_part_of_the_signature(tmp_path, monkeypatch):
    path = _write(tmp_path / "mod.py", "VALUE = 1\n")
    cache_dir = tmp_path / "cache"
    calls = []

    def fake_version(name):
        calls.append(name)
        return "1.2.3"

    monkeypatch.setattr(cache.importlib.metadata, "version", fake_version)
    _scavenge([path], cache_dir)
    assert calls
    assert set(calls) == {"vulture"}
    calls.clear()

    def fake_version_changed(name):
        calls.append(name)
        return "9.9.9"

    monkeypatch.setattr(
        cache.importlib.metadata, "version", fake_version_changed
    )
    vulture = _scavenge([path], cache_dir)
    assert calls
    assert set(calls) == {"vulture"}
    assert vulture._cache_stats["scanned"] == {cache.normalize_path(path)}


def test_corrupt_cache_warns_and_rescans(tmp_path, capsys):
    path = _write(tmp_path / "mod.py", "def unused():\n    return 1\n")
    cache_dir = tmp_path / "cache"
    fresh = _scavenge([path], cache_dir)
    cache_path = cache.get_cache_path(cache_dir)
    payload = json.loads(cache_path.read_text(encoding="utf-8"))
    payload["modules"]["not-a-real-file"] = {"broken": True}
    cache_path.write_text(json.dumps(payload), encoding="utf-8")
    capsys.readouterr()
    vulture = _scavenge([path], cache_dir)
    captured = capsys.readouterr()
    assert cache.CORRUPT_CACHE_MESSAGE in captured.err
    assert vulture._cache_stats["scanned"] == {cache.normalize_path(path)}
    assert vulture._cache_stats["reused"] == set()
    assert _reports(vulture) == _reports(fresh)


def test_checksum_mismatch_is_corruption(tmp_path, capsys):
    path = _write(tmp_path / "mod.py", "VALUE = 1\n")
    cache_dir = tmp_path / "cache"
    _scavenge([path], cache_dir)
    meta_path = cache_dir / "cache.json.meta"
    meta = json.loads(meta_path.read_text(encoding="utf-8"))
    meta["sha256"] = "0" * 64
    meta_path.write_text(json.dumps(meta), encoding="utf-8")
    capsys.readouterr()
    vulture = _scavenge([path], cache_dir)
    captured = capsys.readouterr()
    assert cache.CORRUPT_CACHE_MESSAGE in captured.err
    assert vulture._cache_stats["reused"] == set()


def test_unreadable_cache_warns(tmp_path, capsys):
    path = _write(tmp_path / "mod.py", "VALUE = 1\n")
    cache_dir = tmp_path / "cache"
    _scavenge([path], cache_dir)
    cache.get_cache_path(cache_dir).write_bytes(b"{")
    capsys.readouterr()
    _scavenge([path], cache_dir)
    captured = capsys.readouterr()
    assert cache.CORRUPT_CACHE_MESSAGE in captured.err


def test_whitelist_change_invalidates_affected_modules(tmp_path, monkeypatch):
    root = tmp_path / "proj"
    affected = _write(root / "affected.py", "import sys\n")
    importer = _write(root / "importer.py", "import affected\n")
    other = _write(root / "other.py", "VALUE = 1\n")
    cache_dir = tmp_path / "cache"
    _scavenge([root], cache_dir)
    whitelist = cache.builtin_whitelist_path("sys")
    real_sha = cache.sha256_file

    def fake_sha(path):
        digest = real_sha(path)
        if Path(path) == whitelist:
            return "0" * 64
        return digest

    monkeypatch.setattr(cache, "sha256_file", fake_sha)
    vulture = _scavenge([root], cache_dir)
    assert cache.normalize_path(affected) in vulture._cache_stats["scanned"]
    assert cache.normalize_path(importer) in vulture._cache_stats["reused"]
    assert cache.normalize_path(other) in vulture._cache_stats["reused"]


def test_keyboard_interrupt_saves_partial_cache_and_reraises(tmp_path):
    first = _write(tmp_path / "first.py", "VALUE = 1\n")
    second = _write(tmp_path / "second.py", "VALUE = 2\n")
    cache_dir = tmp_path / "cache"
    original = Vulture.scan
    calls = []

    def interrupt(self, code, filename=""):
        calls.append(Path(filename).name)
        if len(calls) >= 2:
            raise KeyboardInterrupt
        return original(self, code, filename)

    monkeypatch = pytest.MonkeyPatch()
    monkeypatch.setattr(Vulture, "scan", interrupt)
    vulture = Vulture(cache_dir=cache_dir)
    try:
        with pytest.raises(KeyboardInterrupt):
            vulture.scavenge([first, second])
    finally:
        monkeypatch.undo()
    cache_path = cache.get_cache_path(cache_dir)
    raw = cache_path.read_bytes()
    meta = json.loads((cache_dir / "cache.json.meta").read_text())
    assert meta["sha256"] == hashlib.sha256(raw).hexdigest()
    assert (cache_dir / "cache.json.bak").read_bytes() == raw
    payload = json.loads(raw.decode("utf-8"))
    assert cache.normalize_path(first) in payload["modules"]
    assert cache.normalize_path(second) not in payload["modules"]

    again = _scavenge([first, second], cache_dir)
    assert cache.normalize_path(first) in again._cache_stats["reused"]
    assert cache.normalize_path(second) in again._cache_stats["scanned"]


def test_concurrent_saves_do_not_corrupt_the_cache(tmp_path):
    cache_dir = tmp_path / "cache"
    paths = []
    for index in range(2):
        path = _write(tmp_path / f"m{index}.py", f"VALUE = {index}\n")
        paths.append(path)
    errors = []

    def run(path):
        try:
            _scavenge([path], cache_dir)
        except Exception as err:
            errors.append(err)

    threads = [threading.Thread(target=run, args=(path,)) for path in paths]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()
    assert errors == []
    cache_path = cache.get_cache_path(cache_dir)
    raw = cache_path.read_bytes()
    meta = json.loads(
        (cache_dir / "cache.json.meta").read_text(encoding="utf-8")
    )
    assert meta["sha256"] == hashlib.sha256(raw).hexdigest()
    payload = json.loads(raw.decode("utf-8"))
    assert (cache_dir / "cache.json.bak").is_file()
    for path in paths:
        assert cache.normalize_path(path) in payload["modules"]


def test_clear_cache_removes_contents(tmp_path):
    cache_dir = tmp_path / "cache"
    nested = cache_dir / "nested"
    nested.mkdir(parents=True)
    (nested / "child.txt").write_text("x")
    (cache_dir / "junk.txt").write_text("x")
    cache.clear_cache(cache_dir)
    assert cache_dir.is_dir()
    assert list(cache_dir.iterdir()) == []
    cache.clear_cache(tmp_path / "missing")


def test_cli_cache_flags(tmp_path):
    path = _write(tmp_path / "mod.py", "def unused():\n    return 1\n")
    cache_dir = tmp_path / "cli-cache"
    (cache_dir / "nested").mkdir(parents=True)
    (cache_dir / "nested" / "junk.txt").write_text("junk")
    completed = subprocess.run(
        [
            sys.executable,
            "-m",
            "vulture",
            str(path),
            "--cache",
            "--cache-clear",
            f"--cache-dir={cache_dir}",
        ],
        cwd=REPO,
        capture_output=True,
        text=True,
        check=False,
    )
    assert completed.returncode == ExitCode.DeadCode
    assert "unused function 'unused'" in completed.stdout
    assert not (cache_dir / "nested").exists()
    cache_path = cache.get_cache_path(cache_dir)
    assert cache_path.is_file()
    assert (cache_dir / "cache.json.bak").is_file()
    assert (cache_dir / "cache.json.meta").is_file()
    help_text = subprocess.run(
        [sys.executable, "-m", "vulture", "--help"],
        cwd=REPO,
        capture_output=True,
        text=True,
        check=False,
    )
    assert help_text.returncode == ExitCode.NoDeadCode
    assert "--cache" in help_text.stdout
    assert "--cache-clear" in help_text.stdout
    assert "--cache-dir" in help_text.stdout


def test_ignore_names_change_rescans(tmp_path):
    path = _write(tmp_path / "mod.py", "def unused():\n    return 1\n")
    cache_dir = tmp_path / "cache"
    first = _scavenge([path], cache_dir)
    assert _reports(first)
    second = _scavenge([path], cache_dir, ignore_names=["unused"])
    assert second._cache_stats["scanned"] == {cache.normalize_path(path)}
    assert _reports(second) == []
