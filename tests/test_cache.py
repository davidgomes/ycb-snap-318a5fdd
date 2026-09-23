import json
import os
import subprocess
import sys
from pathlib import Path

import pytest

from vulture import cache
from vulture.core import Vulture


def run(tmp_path, paths=None, **kwargs):
    v = Vulture(cache_dir=tmp_path / "cache", **kwargs)
    v.scavenge(paths or [tmp_path / "src"])
    return v


def names(v):
    return sorted(item.name for item in v.get_unused_code())


@pytest.fixture
def src(tmp_path):
    src = tmp_path / "src"
    src.mkdir()
    (src / "a.py").write_text("def unused_a(): pass\ndef used_a(): pass\n")
    (src / "b.py").write_text("from a import used_a\nused_a()\n")
    (src / "c.py").write_text("import b\ndef unused_c(): pass\n")
    (src / "d.py").write_text("def unused_d(): pass\n")
    return src


def key(path):
    return cache.normalize_path(path)


def test_first_run_scans_everything(tmp_path, src):
    v = run(tmp_path)
    assert v._cache_stats["reused"] == set()
    assert len(v._cache_stats["scanned"]) == 4
    cache_path = cache.get_cache_path(tmp_path / "cache")
    assert cache_path.name == "cache.json"
    data = json.loads(cache_path.read_text())
    assert set(data["modules"]) == {key(p) for p in src.glob("*.py")}
    assert cache_path.with_name("cache.json.bak").is_file()
    meta = json.loads(cache_path.with_name("cache.json.meta").read_text())
    assert "sha256" in meta


def test_unchanged_run_reuses_everything(tmp_path, src):
    expected = names(run(tmp_path))
    v = run(tmp_path)
    assert v._cache_stats["scanned"] == set()
    assert len(v._cache_stats["reused"]) == 4
    assert names(v) == expected


def test_transitive_importers_are_rescanned(tmp_path, src):
    run(tmp_path)
    (src / "a.py").write_text("def unused_a2(): pass\ndef used_a(): pass\n")
    v = run(tmp_path)
    assert v._cache_stats["scanned"] == {
        key(src / "a.py"),
        key(src / "b.py"),
        key(src / "c.py"),
    }
    assert v._cache_stats["reused"] == {key(src / "d.py")}
    assert "unused_a2" in names(v)
    assert "unused_a" not in names(v)


def test_settings_change_triggers_full_scan(tmp_path, src):
    run(tmp_path, cache_settings={"x": 1})
    v = run(tmp_path, cache_settings={"x": 2})
    assert len(v._cache_stats["scanned"]) == 4
    v = run(tmp_path, ignore_names=["unused_d"], cache_settings={"x": 2})
    assert len(v._cache_stats["scanned"]) == 4
    assert "unused_d" not in names(v)


def test_signature_change_triggers_full_scan(tmp_path, src, monkeypatch):
    run(tmp_path)
    monkeypatch.setattr(cache, "__version__", "other")
    v = run(tmp_path)
    assert len(v._cache_stats["scanned"]) == 4


def test_corrupt_cache(tmp_path, src, capsys):
    run(tmp_path)
    capsys.readouterr()
    cache.get_cache_path(tmp_path / "cache").write_text("{garbage")
    v = run(tmp_path)
    assert "cache is corrupted or unreadable" in capsys.readouterr().err
    assert len(v._cache_stats["scanned"]) == 4


def test_checksum_mismatch(tmp_path, src, capsys):
    run(tmp_path)
    capsys.readouterr()
    cache_path = cache.get_cache_path(tmp_path / "cache")
    data = json.loads(cache_path.read_text())
    data["modules"] = {}
    cache_path.write_text(json.dumps(data))
    v = run(tmp_path)
    assert "cache is corrupted or unreadable" in capsys.readouterr().err
    assert len(v._cache_stats["scanned"]) == 4


def test_missing_cache_is_silent(tmp_path, src, capsys):
    run(tmp_path)
    assert capsys.readouterr().err == ""


def test_deleted_files_are_removed(tmp_path, src):
    run(tmp_path)
    (src / "d.py").rename(src / "e.py")
    v = run(tmp_path)
    assert v._cache_stats["scanned"] == {key(src / "e.py")}
    data = json.loads(cache.get_cache_path(tmp_path / "cache").read_text())
    assert key(src / "d.py") not in data["modules"]
    assert key(src / "e.py") in data["modules"]


def test_whitelist_change_invalidates_affected_modules(tmp_path, src):
    (src / "my_whitelist.py").write_text("")
    run(tmp_path)
    (src / "my_whitelist.py").write_text("unused_d\n")
    v = run(tmp_path)
    assert v._cache_stats["scanned"] == {
        key(src / "my_whitelist.py"),
        key(src / "d.py"),
    }
    assert "unused_d" not in names(v)


def test_keyboard_interrupt_saves_partial_cache(tmp_path, src, monkeypatch):
    original_scan = Vulture.scan
    calls = []

    def scan(self, code, filename=""):
        calls.append(filename)
        if len(calls) == 3:
            raise KeyboardInterrupt
        original_scan(self, code, filename)

    monkeypatch.setattr(Vulture, "scan", scan)
    with pytest.raises(KeyboardInterrupt):
        run(tmp_path)
    monkeypatch.setattr(Vulture, "scan", original_scan)
    data = json.loads(cache.get_cache_path(tmp_path / "cache").read_text())
    assert len(data["modules"]) == 2
    v = run(tmp_path)
    assert v._cache_stats["reused"]
    stats = v._cache_stats["reused"] | v._cache_stats["scanned"]
    assert len(stats) == 4


def test_relative_imports(tmp_path):
    pkg = tmp_path / "src" / "pkg"
    pkg.mkdir(parents=True)
    (pkg / "__init__.py").write_text("")
    (pkg / "x.py").write_text("X = 1\n")
    (pkg / "y.py").write_text("from .x import X\n")
    (pkg / "z.py").write_text("Z = 1\n")
    run(tmp_path)
    (pkg / "x.py").write_text("X = 2\n")
    v = run(tmp_path)
    assert v._cache_stats["scanned"] == {key(pkg / "x.py"), key(pkg / "y.py")}


def test_concurrent_processes(tmp_path, src):
    for i in range(20):
        (src / f"m{i}.py").write_text(f"def f{i}(): pass\n")
    repo = Path(__file__).resolve().parent.parent
    env = {**os.environ, "PYTHONPATH": str(repo)}
    cmd = [
        sys.executable,
        "-m",
        "vulture",
        "--cache",
        f"--cache-dir={tmp_path / 'cache'}",
        str(src),
    ]
    procs = [
        subprocess.Popen(cmd, env=env, stdout=subprocess.DEVNULL)
        for _ in range(6)
    ]
    for proc in procs:
        proc.wait()
    v = run(tmp_path)
    assert len(v._cache_stats["reused"]) == 24


def test_normalize_path_windows(monkeypatch):
    monkeypatch.setattr(sys, "platform", "win32")
    assert cache.normalize_path("/Foo/BAR.py") == cache.normalize_path(
        "/foo/bar.py"
    )


def test_cli_cache_and_clear(tmp_path, src):
    cache_dir = tmp_path / "cache"
    repo = Path(__file__).resolve().parent.parent
    env = {**os.environ, "PYTHONPATH": str(repo)}

    def call(*args):
        subprocess.run(
            [
                sys.executable,
                "-m",
                "vulture",
                f"--cache-dir={cache_dir}",
                *args,
                str(src),
            ],
            check=False,
            env=env,
        )

    call("--cache")
    assert cache.get_cache_path(cache_dir).is_file()
    call("--cache-clear")
    assert list(cache_dir.iterdir()) == []
