"""Tests for the incremental analysis cache."""

import hashlib
import json
import subprocess
import sys
from pathlib import Path

import pytest

from vulture import cache
from vulture.cache import (
    get_backup_path,
    get_cache_path,
    get_meta_path,
    normalize_path,
)
from vulture.core import Vulture
from vulture.utils import ExitCode

from . import REPO, call_vulture


def _write(path, text):
    path.write_text(text, encoding="utf-8")


def _reports(vulture):
    return [item.get_report() for item in vulture.get_unused_code()]


def _load(cache_dir):
    raw = get_cache_path(cache_dir).read_bytes()
    meta = json.loads(get_meta_path(cache_dir).read_text(encoding="utf-8"))
    assert meta["sha256"] == hashlib.sha256(raw).hexdigest()
    assert get_backup_path(cache_dir).is_file()
    return json.loads(raw.decode("utf-8"))


def test_normalize_path_is_absolute(tmp_path):
    path = tmp_path / "Foo.py"
    _write(path, "x = 1\n")
    normalized = normalize_path(path)
    assert Path(normalized).is_absolute()
    assert normalize_path(str(path)) == normalized
    assert normalize_path(path) == normalize_path(path.resolve())


def test_get_cache_path():
    assert get_cache_path(".vulture-cache") == Path(".vulture-cache") / (
        "cache.json"
    )
    assert get_cache_path(Path("/tmp/vulture")) == Path(
        "/tmp/vulture/cache.json"
    )


def test_importlib_is_module_level():
    assert hasattr(cache, "importlib")
    source = Path(cache.__file__).read_text(encoding="utf-8")
    assert "import importlib" in source
    assert "importlib.metadata.version" in source


def test_missing_cache_is_silent_full_scan(tmp_path, capsys):
    path = tmp_path / "mod.py"
    _write(path, "def unused():\n    pass\n")
    cache_dir = tmp_path / "cache"
    vulture = Vulture(cache_dir=cache_dir)
    vulture.scavenge([path])
    captured = capsys.readouterr()
    assert "cache is corrupted or unreadable" not in captured.err
    assert vulture._cache_stats["reused"] == set()
    assert vulture._cache_stats["scanned"] == {normalize_path(path)}
    data = _load(cache_dir)
    assert normalize_path(path) in data["modules"]
    assert "cache_version" in data["signature"]
    assert data["signature"]["python_version"] == sys.version
    assert "vulture_version" in data["signature"]


def test_second_run_reuses_and_matches_fresh_results(tmp_path):
    path = tmp_path / "mod.py"
    _write(
        path,
        "import sys\n\ndef unused():\n    pass\n\ndef used():\n"
        "    return sys.version\nused()\n",
    )
    cache_dir = tmp_path / "cache"
    first = Vulture(cache_dir=cache_dir, cache_settings={"mode": "a"})
    first.scavenge([path])
    second = Vulture(cache_dir=cache_dir, cache_settings={"mode": "a"})
    second.scavenge([path])
    fresh = Vulture()
    fresh.scavenge([path])
    assert second._cache_stats["scanned"] == set()
    assert second._cache_stats["reused"] == {normalize_path(path)}
    assert _reports(second) == _reports(fresh)
    assert _reports(first) == _reports(fresh)


def test_changed_file_and_transitive_importers_are_rescanned(tmp_path):
    package = tmp_path / "pkg"
    package.mkdir()
    _write(package / "__init__.py", "")
    _write(package / "a.py", "def foo():\n    return 1\n")
    _write(package / "b.py", "from pkg.a import foo\nfoo()\n")
    _write(package / "c.py", "import pkg.b\n")
    _write(package / "d.py", "def bar():\n    return 2\n")
    cache_dir = tmp_path / "cache"
    paths = [package]
    Vulture(cache_dir=cache_dir).scavenge(paths)
    _write(package / "a.py", "def foo():\n    return 3\n")
    again = Vulture(cache_dir=cache_dir)
    again.scavenge(paths)
    scanned = again._cache_stats["scanned"]
    reused = again._cache_stats["reused"]
    assert normalize_path(package / "a.py") in scanned
    assert normalize_path(package / "b.py") in scanned
    assert normalize_path(package / "c.py") in scanned
    assert normalize_path(package / "d.py") in reused
    assert normalize_path(package / "__init__.py") in reused
    fresh = Vulture()
    fresh.scavenge(paths)
    assert _reports(again) == _reports(fresh)


def test_cache_settings_change_rescans_everything(tmp_path, capsys):
    path = tmp_path / "mod.py"
    _write(path, "def unused():\n    pass\n")
    cache_dir = tmp_path / "cache"
    Vulture(cache_dir=cache_dir, cache_settings={"generation": 1}).scavenge(
        [path]
    )
    again = Vulture(cache_dir=cache_dir, cache_settings={"generation": 2})
    again.scavenge([path])
    captured = capsys.readouterr()
    assert "cache is corrupted or unreadable" not in captured.err
    assert again._cache_stats["reused"] == set()
    assert again._cache_stats["scanned"] == {normalize_path(path)}


def test_signature_change_rescans_without_warning(
    tmp_path, capsys, monkeypatch
):
    path = tmp_path / "mod.py"
    _write(path, "def unused():\n    pass\n")
    cache_dir = tmp_path / "cache"
    Vulture(cache_dir=cache_dir).scavenge([path])
    monkeypatch.setattr(cache, "__version__", cache.__version__ + ".changed")
    again = Vulture(cache_dir=cache_dir)
    again.scavenge([path])
    captured = capsys.readouterr()
    assert "cache is corrupted or unreadable" not in captured.err
    assert again._cache_stats["scanned"] == {normalize_path(path)}
    assert again._cache_stats["reused"] == set()


def test_corrupt_cache_warns_and_rescans(tmp_path, capsys):
    path = tmp_path / "mod.py"
    _write(path, "def unused():\n    pass\n")
    cache_dir = tmp_path / "cache"
    Vulture(cache_dir=cache_dir).scavenge([path])
    get_cache_path(cache_dir).write_text("{not json", encoding="utf-8")
    again = Vulture(cache_dir=cache_dir)
    again.scavenge([path])
    captured = capsys.readouterr()
    assert "cache is corrupted or unreadable" in captured.err
    assert again._cache_stats["scanned"] == {normalize_path(path)}
    assert again._cache_stats["reused"] == set()
    _load(cache_dir)


def test_checksum_mismatch_is_corruption(tmp_path, capsys):
    path = tmp_path / "mod.py"
    _write(path, "def unused():\n    pass\n")
    cache_dir = tmp_path / "cache"
    Vulture(cache_dir=cache_dir).scavenge([path])
    raw = get_cache_path(cache_dir).read_bytes()
    meta = {"sha256": hashlib.sha256(b"other").hexdigest()}
    get_meta_path(cache_dir).write_text(json.dumps(meta), encoding="utf-8")
    # Keep the original bytes so only the recorded checksum is wrong.
    assert hashlib.sha256(raw).hexdigest() != meta["sha256"]
    again = Vulture(cache_dir=cache_dir)
    again.scavenge([path])
    captured = capsys.readouterr()
    assert "cache is corrupted or unreadable" in captured.err
    assert again._cache_stats["reused"] == set()
    _load(cache_dir)


def test_deleted_and_renamed_files_are_pruned(tmp_path):
    kept = tmp_path / "kept.py"
    removed = tmp_path / "removed.py"
    _write(kept, "def kept():\n    return 1\nkept()\n")
    _write(removed, "def removed():\n    return 2\nremoved()\n")
    cache_dir = tmp_path / "cache"
    Vulture(cache_dir=cache_dir).scavenge([kept, removed])
    removed.unlink()
    renamed = tmp_path / "renamed.py"
    # A new file stands in for a rename: the old path must disappear.
    _write(renamed, "def renamed():\n    return 3\nrenamed()\n")
    Vulture(cache_dir=cache_dir).scavenge([kept, renamed])
    modules = _load(cache_dir)["modules"]
    assert normalize_path(removed) not in modules
    assert normalize_path(kept) in modules
    assert normalize_path(renamed) in modules


def test_whitelist_change_invalidates_affected_module(tmp_path):
    affected = tmp_path / "uses_sys.py"
    other = tmp_path / "plain.py"
    _write(affected, "import sys\n\ndef unused():\n    pass\n")
    _write(other, "def also_unused():\n    pass\n")
    cache_dir = tmp_path / "cache"
    Vulture(cache_dir=cache_dir).scavenge([affected, other])
    data = _load(cache_dir)
    data["whitelist_hashes"]["sys"] = "0" * 64
    payload = (json.dumps(data, indent=2, sort_keys=True) + "\n").encode()
    get_cache_path(cache_dir).write_bytes(payload)
    meta = {"sha256": hashlib.sha256(payload).hexdigest()}
    get_meta_path(cache_dir).write_text(
        json.dumps(meta, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    again = Vulture(cache_dir=cache_dir)
    again.scavenge([affected, other])
    assert normalize_path(affected) in again._cache_stats["scanned"]
    assert normalize_path(other) in again._cache_stats["reused"]


def test_keyboard_interrupt_saves_partial_cache(tmp_path):
    first = tmp_path / "first.py"
    second = tmp_path / "second.py"
    _write(first, "def foo():\n    return 1\nfoo()\n")
    _write(second, "def bar():\n    return 2\nbar()\n")
    cache_dir = tmp_path / "cache"
    calls = {"n": 0}

    class Boom(Vulture):
        def scan(self, code, filename=""):
            calls["n"] += 1
            if calls["n"] >= 2:
                raise KeyboardInterrupt
            super().scan(code, filename)

    vulture = Boom(cache_dir=cache_dir)
    with pytest.raises(KeyboardInterrupt):
        vulture.scavenge([first, second])
    data = _load(cache_dir)
    assert normalize_path(first) in data["modules"]
    assert normalize_path(second) not in data["modules"]

    follow = Vulture(cache_dir=cache_dir)
    follow.scavenge([first, second])
    assert normalize_path(first) in follow._cache_stats["reused"]
    assert normalize_path(second) in follow._cache_stats["scanned"]


def test_concurrent_processes_leave_a_valid_cache(tmp_path):
    path = tmp_path / "mod.py"
    _write(path, "def foo():\n    return 1\nfoo()\n")
    cache_dir = tmp_path / "cache"
    code = (
        "import sys\n"
        f"sys.path.insert(0, {str(REPO)!r})\n"
        "from vulture.core import Vulture\n"
        f"v = Vulture(cache_dir={str(cache_dir)!r})\n"
        f"v.scavenge([{str(path)!r}])\n"
    )
    processes = [
        subprocess.Popen([sys.executable, "-c", code]) for _ in range(4)
    ]
    for process in processes:
        assert process.wait() == 0
    data = _load(cache_dir)
    assert normalize_path(path) in data["modules"]


def test_cli_cache_and_cache_clear(tmp_path):
    path = tmp_path / "mod.py"
    _write(path, "def foo():\n    return 1\nfoo()\n")
    cache_dir = tmp_path / "cli-cache"
    cache_dir.mkdir()
    junk = cache_dir / "junk.txt"
    _write(junk, "x")
    assert (
        call_vulture(
            [
                str(path),
                "--cache",
                "--cache-clear",
                "--cache-dir",
                str(cache_dir),
            ]
        )
        == ExitCode.NoDeadCode
    )
    assert not junk.exists()
    assert get_cache_path(cache_dir).is_file()
    assert get_backup_path(cache_dir).is_file()
    assert get_meta_path(cache_dir).is_file()
    assert (
        call_vulture([str(path), "--cache", "--cache-dir", str(cache_dir)])
        == ExitCode.NoDeadCode
    )


def test_cli_cache_clear_without_cache(tmp_path):
    path = tmp_path / "mod.py"
    _write(path, "def foo():\n    return 1\nfoo()\n")
    cache_dir = tmp_path / "cleared"
    cache_dir.mkdir()
    _write(cache_dir / "junk.txt", "x")
    assert (
        call_vulture(
            [str(path), "--cache-clear", "--cache-dir", str(cache_dir)]
        )
        == ExitCode.NoDeadCode
    )
    assert list(cache_dir.iterdir()) == []
