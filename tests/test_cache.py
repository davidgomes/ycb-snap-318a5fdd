"""Tests for the incremental analysis cache."""

import ast
import hashlib
import importlib.metadata
import json
import subprocess
import sys
import textwrap
from pathlib import Path

import pytest

from vulture import cache as analysis_cache
from vulture import core
from vulture.config import _parse_args, make_config
from vulture.utils import ExitCode

from . import REPO


def _reports(vulture):
    return [item.get_report() for item in vulture.get_unused_code()]


def _norm(path):
    return analysis_cache.normalize_path(path)


def _meta(cache_dir):
    meta_path = cache_dir / "cache.json.meta"
    return json.loads(meta_path.read_text(encoding="utf-8"))


def _scavenge(paths, cache_dir=None, cache_settings=None):
    vulture = core.Vulture(cache_dir=cache_dir, cache_settings=cache_settings)
    vulture.scavenge(paths)
    return vulture


def _write(path, source):
    path.write_text(textwrap.dedent(source))


def test_normalize_path_and_cache_file():
    sample = Path("/Tmp/SomeFile.py")
    normalized = _norm(sample)
    assert isinstance(normalized, str)
    if sys.platform == "win32":
        assert normalized == _norm(str(sample).upper())
        assert normalized == _norm(str(sample).lower())
    else:
        assert normalized.endswith("SomeFile.py")

    cache_dir = Path("custom-cache")
    cache_path = analysis_cache.get_cache_path(cache_dir)
    assert isinstance(cache_path, Path)
    assert cache_path == cache_dir / "cache.json"


def test_importlib_imported_at_module_scope():
    source = (REPO / "vulture" / "cache.py").read_text(encoding="utf-8")
    tree = ast.parse(source)
    imported = False
    for node in tree.body:
        if isinstance(node, ast.Import):
            for alias in node.names:
                if alias.name.split(".", 1)[0] == "importlib":
                    imported = True
    assert imported
    signature = analysis_cache.runtime_signature()
    assert signature["cache_version"] == analysis_cache.__version__
    assert signature["python"] == sys.version
    assert "vulture" in signature


def test_cache_reuses_unchanged_files_and_matches_results(tmp_path):
    package = tmp_path / "pkg"
    package.mkdir()
    _write(
        package / "a.py",
        """\
        def live():
            return 1

        def dead_a():
            return 2

        def unused_after_return():
            return 3
            dead = 4
        """,
    )
    _write(
        package / "b.py",
        """\
        import a
        a.live()

        def dead_b():
            return 5
        """,
    )
    _write(
        package / "c.py",
        """\
        import b

        def dead_c():
            return 6
        """,
    )
    paths = [package]
    fresh = _scavenge(paths)
    cache_dir = tmp_path / "cache"
    first = _scavenge(paths, cache_dir=cache_dir)
    second = _scavenge(paths, cache_dir=cache_dir)

    assert _reports(fresh) == _reports(first) == _reports(second)
    assert fresh.exit_code == first.exit_code == second.exit_code
    assert first._cache_stats["reused"] == set()
    assert second._cache_stats["scanned"] == set()
    assert second._cache_stats["reused"] == {
        _norm(package / name) for name in ("a.py", "b.py", "c.py")
    }
    for stats in (first._cache_stats, second._cache_stats):
        assert set(stats) == {"scanned", "reused"}
        assert all(isinstance(paths, set) for paths in stats.values())

    cache_path = analysis_cache.get_cache_path(cache_dir)
    raw = cache_path.read_bytes()
    loaded = json.loads(raw)
    assert set(loaded["modules"]) == second._cache_stats["reused"]
    assert _meta(cache_dir)["sha256"] == hashlib.sha256(raw).hexdigest()
    assert (cache_dir / "cache.json.bak").is_file()


def test_changed_file_and_transitive_importers_are_rescanned(tmp_path):
    _write(tmp_path / "a.py", "def live():\n    return 1\n")
    _write(
        tmp_path / "b.py",
        "import a\na.live()\ndef dead_b():\n    return 2\n",
    )
    _write(tmp_path / "c.py", "import b\ndef dead_c():\n    return 3\n")
    _write(tmp_path / "d.py", "def dead_d():\n    return 4\n")
    cache_dir = tmp_path / "cache"
    paths = [tmp_path / name for name in ("a.py", "b.py", "c.py", "d.py")]
    _scavenge(paths, cache_dir=cache_dir)

    _write(tmp_path / "a.py", "def live():\n    return 9\n")
    rerun = _scavenge(paths, cache_dir=cache_dir)
    scanned = rerun._cache_stats["scanned"]
    reused = rerun._cache_stats["reused"]
    assert _norm(tmp_path / "a.py") in scanned
    assert _norm(tmp_path / "b.py") in scanned
    assert _norm(tmp_path / "c.py") in scanned
    assert _norm(tmp_path / "d.py") in reused

    reference = _scavenge(paths)
    assert _reports(rerun) == _reports(reference)


def test_relative_imports_invalidate_importers(tmp_path):
    package = tmp_path / "pkg"
    sub = package / "sub"
    sub.mkdir(parents=True)
    _write(package / "__init__.py", "")
    _write(sub / "__init__.py", "")
    _write(sub / "a.py", "def live():\n    return 1\n")
    _write(sub / "b.py", "from .a import live\nlive()\n")
    _write(package / "c.py", "from pkg.sub import b\n")
    _write(package / "d.py", "def alone():\n    return 0\n")
    cache_dir = tmp_path / "cache"
    _scavenge([package], cache_dir=cache_dir)
    _write(sub / "a.py", "def live():\n    return 2\n")
    rerun = _scavenge([package], cache_dir=cache_dir)
    scanned = rerun._cache_stats["scanned"]
    reused = rerun._cache_stats["reused"]
    assert _norm(sub / "a.py") in scanned
    assert _norm(sub / "b.py") in scanned
    assert _norm(package / "c.py") in scanned
    assert _norm(package / "d.py") in reused


def test_dependency_change_updates_reused_definitions(tmp_path):
    _write(
        tmp_path / "a.py",
        "def live():\n    return 1\n\ndef dead_a():\n    return 2\n",
    )
    _write(tmp_path / "b.py", "import a\na.live()\n")
    cache_dir = tmp_path / "cache"
    paths = [tmp_path / "a.py", tmp_path / "b.py"]
    _scavenge(paths, cache_dir=cache_dir)
    _write(tmp_path / "b.py", "import a\ndef dead_b():\n    return 3\n")
    rerun = _scavenge(paths, cache_dir=cache_dir)
    names = {item.name for item in rerun.get_unused_code()}
    assert "live" in names
    assert "dead_a" in names
    reused = rerun._cache_stats["reused"]
    scanned = rerun._cache_stats["scanned"]
    assert _norm(tmp_path / "a.py") in reused
    assert _norm(tmp_path / "b.py") in scanned
    assert _reports(rerun) == _reports(_scavenge(paths))


def test_deleted_and_renamed_files_are_pruned(tmp_path):
    kept = tmp_path / "kept.py"
    removed = tmp_path / "removed.py"
    _write(kept, "def kept():\n    return 1\n")
    _write(removed, "def removed():\n    return 2\n")
    cache_dir = tmp_path / "cache"
    _scavenge([kept, removed], cache_dir=cache_dir)
    removed.unlink()
    renamed = tmp_path / "renamed.py"
    _write(renamed, "def renamed():\n    return 3\n")
    _scavenge([kept, renamed], cache_dir=cache_dir)
    modules = json.loads(
        analysis_cache.get_cache_path(cache_dir).read_text(encoding="utf-8")
    )["modules"]
    assert _norm(removed) not in modules
    assert _norm(kept) in modules
    assert _norm(renamed) in modules


def test_missing_cache_is_silent(tmp_path, capsys):
    path = tmp_path / "a.py"
    _write(path, "def a():\n    return 1\n")
    vulture = _scavenge([path], cache_dir=tmp_path / "missing-cache")
    captured = capsys.readouterr()
    assert "cache is corrupted or unreadable" not in captured.err
    assert vulture._cache_stats["reused"] == set()
    assert _norm(path) in vulture._cache_stats["scanned"]


def test_corrupt_cache_warns_and_rescans(tmp_path, capsys):
    path = tmp_path / "a.py"
    _write(path, "def a():\n    return 1\n")
    cache_dir = tmp_path / "cache"
    _scavenge([path], cache_dir=cache_dir)
    cache_path = analysis_cache.get_cache_path(cache_dir)
    cache_path.write_text("{not json", encoding="utf-8")
    vulture = _scavenge([path], cache_dir=cache_dir)
    captured = capsys.readouterr()
    assert "cache is corrupted or unreadable" in captured.err
    assert _norm(path) in vulture._cache_stats["scanned"]
    assert vulture._cache_stats["reused"] == set()
    reloaded = json.loads(cache_path.read_text(encoding="utf-8"))
    assert _norm(path) in reloaded["modules"]


def test_checksum_mismatch_is_corruption(tmp_path, capsys):
    path = tmp_path / "a.py"
    _write(path, "def a():\n    return 1\n")
    cache_dir = tmp_path / "cache"
    _scavenge([path], cache_dir=cache_dir)
    meta_path = cache_dir / "cache.json.meta"
    meta_path.write_text(
        json.dumps({"sha256": "0" * 64}) + "\n", encoding="utf-8"
    )
    vulture = _scavenge([path], cache_dir=cache_dir)
    captured = capsys.readouterr()
    assert "cache is corrupted or unreadable" in captured.err
    assert vulture._cache_stats["reused"] == set()
    raw = analysis_cache.get_cache_path(cache_dir).read_bytes()
    meta = json.loads(meta_path.read_text(encoding="utf-8"))
    assert meta["sha256"] == hashlib.sha256(raw).hexdigest()


def test_signature_and_settings_changes_rescan(tmp_path, monkeypatch):
    path = tmp_path / "a.py"
    _write(path, "def a():\n    return 1\n")
    cache_dir = tmp_path / "cache"
    _scavenge([path], cache_dir=cache_dir, cache_settings={"mode": "a"})

    changed_settings = _scavenge(
        [path], cache_dir=cache_dir, cache_settings={"mode": "b"}
    )
    assert changed_settings._cache_stats["reused"] == set()
    scanned = changed_settings._cache_stats["scanned"]
    assert _norm(path) in scanned

    _scavenge([path], cache_dir=cache_dir, cache_settings={"mode": "b"})
    monkeypatch.setattr(analysis_cache, "__version__", "other-cache-version")
    changed_version = _scavenge(
        [path], cache_dir=cache_dir, cache_settings={"mode": "b"}
    )
    assert changed_version._cache_stats["reused"] == set()

    monkeypatch.undo()
    _scavenge([path], cache_dir=cache_dir, cache_settings={"mode": "b"})
    monkeypatch.setattr(sys, "version", sys.version + " mutated")
    changed_python = _scavenge(
        [path], cache_dir=cache_dir, cache_settings={"mode": "b"}
    )
    assert changed_python._cache_stats["reused"] == set()

    monkeypatch.undo()
    _scavenge([path], cache_dir=cache_dir, cache_settings={"mode": "b"})

    def fake_version(distribution):
        assert distribution == "vulture"
        return "999.0.0"

    monkeypatch.setattr(importlib.metadata, "version", fake_version)
    changed_package = _scavenge(
        [path], cache_dir=cache_dir, cache_settings={"mode": "b"}
    )
    assert changed_package._cache_stats["reused"] == set()
    stored = json.loads(
        analysis_cache.get_cache_path(cache_dir).read_text(encoding="utf-8")
    )
    assert stored["signature"]["vulture"] == "999.0.0"


def test_whitelist_change_invalidates_importers(tmp_path, monkeypatch):
    _write(
        tmp_path / "importer.py",
        "import sys\ndef unused():\n    return 1\n",
    )
    _write(tmp_path / "other.py", "def other():\n    return 2\n")
    cache_dir = tmp_path / "cache"
    paths = [tmp_path / "importer.py", tmp_path / "other.py"]
    _scavenge(paths, cache_dir=cache_dir)
    current = analysis_cache.whitelist_hashes()
    tweaked = dict(current)
    tweaked["sys"] = "f" * 64

    monkeypatch.setattr(analysis_cache, "whitelist_hashes", lambda: tweaked)
    rerun = _scavenge(paths, cache_dir=cache_dir)
    assert _norm(tmp_path / "importer.py") in rerun._cache_stats["scanned"]
    assert _norm(tmp_path / "other.py") in rerun._cache_stats["reused"]


def test_backup_tracks_previous_cache(tmp_path):
    path = tmp_path / "a.py"
    _write(path, "def a():\n    return 1\n")
    cache_dir = tmp_path / "cache"
    _scavenge([path], cache_dir=cache_dir)
    first = analysis_cache.get_cache_path(cache_dir).read_bytes()
    assert (cache_dir / "cache.json.bak").read_bytes() == first
    _write(path, "def a():\n    return 2\n")
    _scavenge([path], cache_dir=cache_dir)
    assert (cache_dir / "cache.json.bak").read_bytes() == first
    assert analysis_cache.get_cache_path(cache_dir).read_bytes() != first


def test_keyboard_interrupt_saves_partial_cache(tmp_path, monkeypatch):
    first = tmp_path / "first.py"
    second = tmp_path / "second.py"
    _write(first, "def first():\n    return 1\n")
    _write(second, "def second():\n    return 2\n")
    cache_dir = tmp_path / "cache"
    original = core.Vulture.scan
    state = {"raised": False}

    def scan(self, code, filename=""):
        if Path(filename).name == "second.py" and not state["raised"]:
            state["raised"] = True
            raise KeyboardInterrupt
        return original(self, code, filename)

    monkeypatch.setattr(core.Vulture, "scan", scan)
    with pytest.raises(KeyboardInterrupt):
        _scavenge([first, second], cache_dir=cache_dir)

    raw = analysis_cache.get_cache_path(cache_dir).read_bytes()
    assert _meta(cache_dir)["sha256"] == hashlib.sha256(raw).hexdigest()
    modules = json.loads(raw)["modules"]
    assert _norm(first) in modules
    assert _norm(second) not in modules
    assert (cache_dir / "cache.json.bak").is_file()

    monkeypatch.setattr(core.Vulture, "scan", original)
    rerun = _scavenge([first, second], cache_dir=cache_dir)
    assert _norm(first) in rerun._cache_stats["reused"]
    assert _norm(second) in rerun._cache_stats["scanned"]


def test_concurrent_processes_keep_a_valid_cache(tmp_path):
    files = []
    for index in range(4):
        path = tmp_path / f"m{index}.py"
        _write(path, f"def f{index}():\n    return {index}\n")
        files.append(path)
    cache_dir = tmp_path / "cache"
    processes = [
        subprocess.Popen(
            [
                sys.executable,
                "-m",
                "vulture",
                "--cache",
                f"--cache-dir={cache_dir}",
                str(path),
            ],
            cwd=REPO,
        )
        for path in files
    ]
    codes = [process.wait() for process in processes]
    assert all(
        code in (ExitCode.NoDeadCode, ExitCode.DeadCode) for code in codes
    )
    raw = analysis_cache.get_cache_path(cache_dir).read_bytes()
    assert _meta(cache_dir)["sha256"] == hashlib.sha256(raw).hexdigest()
    modules = json.loads(raw)["modules"]
    expected = {_norm(path) for path in files}
    assert expected <= set(modules)


def test_cache_clear_removes_directory_contents(tmp_path):
    cache_dir = tmp_path / "cache"
    nested = cache_dir / "nested"
    nested.mkdir(parents=True)
    (cache_dir / "sentinel.txt").write_text("stale", encoding="utf-8")
    (nested / "old.json").write_text("{}", encoding="utf-8")
    path = tmp_path / "a.py"
    _write(path, "def a():\n    return 1\n")
    completed = subprocess.run(
        [
            sys.executable,
            "-m",
            "vulture",
            "--cache-clear",
            f"--cache-dir={cache_dir}",
            str(path),
        ],
        cwd=REPO,
        check=False,
    )
    assert completed.returncode in (ExitCode.NoDeadCode, ExitCode.DeadCode)
    assert not (cache_dir / "sentinel.txt").exists()
    assert not nested.exists()
    assert analysis_cache.get_cache_path(cache_dir).is_file()


def test_cli_cache_options(tmp_path, monkeypatch):
    parsed = _parse_args(
        ["--cache", "--cache-clear", "--cache-dir=stored", "path.py"]
    )
    assert parsed["cache"] is True
    assert parsed["cache_clear"] is True
    assert parsed["cache_dir"] == "stored"

    monkeypatch.chdir(tmp_path)
    config = make_config(["--cache", "path.py"])
    assert config["cache"] is True
    assert config["cache_clear"] is False
    assert config["cache_dir"] == ".vulture-cache/"


def test_clear_cache_keeps_directory(tmp_path):
    cache_dir = tmp_path / "cache"
    cache_dir.mkdir()
    (cache_dir / "cache.json").write_text("{}", encoding="utf-8")
    analysis_cache.clear_cache(cache_dir)
    assert cache_dir.is_dir()
    assert list(cache_dir.iterdir()) == []
    analysis_cache.clear_cache(tmp_path / "absent")
