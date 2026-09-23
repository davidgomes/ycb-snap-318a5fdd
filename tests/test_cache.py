import hashlib
import json
import subprocess
import sys
from textwrap import dedent

import pytest

from vulture import cache, core
from vulture.utils import ExitCode

from . import REPO


def write(path, code):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(dedent(code))
    return path


@pytest.fixture
def project(tmp_path):
    src = tmp_path / "src"
    write(src / "base.py", "def base_func(): pass\n")
    write(src / "middle.py", "from base import base_func\ndef mid(): pass\n")
    write(src / "top.py", "import middle\nmiddle.mid()\n")
    write(src / "other.py", "def other_func(): pass\n")
    return src


def run(paths, cache_dir, **kwargs):
    v = core.Vulture(cache_dir=cache_dir, **kwargs)
    v.scavenge([str(path) for path in paths])
    return v


def unused(v):
    return sorted(
        (str(item.filename), item.name, item.typ, item.first_lineno)
        for item in v.get_unused_code()
    )


def keys(*paths):
    return {cache.normalize_path(path) for path in paths}


def all_modules(project):
    return keys(*project.rglob("*.py"))


def test_first_run_writes_cache_backup_and_meta(project, tmp_path):
    cache_dir = tmp_path / "cache"
    v = run([project], cache_dir)
    assert v._cache_stats["scanned"] == all_modules(project)
    assert v._cache_stats["reused"] == set()

    cache_path = cache.get_cache_path(cache_dir)
    assert cache_path == cache_dir / "cache.json"
    raw = cache_path.read_bytes()
    assert (cache_dir / "cache.json.bak").read_bytes() == raw
    meta = json.loads((cache_dir / "cache.json.meta").read_text())
    assert meta["sha256"] == hashlib.sha256(raw).hexdigest()
    assert set(json.loads(raw)["modules"]) == all_modules(project)


def test_unchanged_run_reuses_everything(project, tmp_path):
    cache_dir = tmp_path / "cache"
    fresh = core.Vulture()
    fresh.scavenge([str(project)])
    first = run([project], cache_dir)
    second = run([project], cache_dir)
    assert second._cache_stats["scanned"] == set()
    assert second._cache_stats["reused"] == all_modules(project)
    assert unused(first) == unused(second) == unused(fresh)
    assert second.used_names == fresh.used_names


def test_change_rescans_transitive_importers(project, tmp_path):
    cache_dir = tmp_path / "cache"
    run([project], cache_dir)
    write(project / "base.py", "def base_func(): pass\ndef new_func(): pass\n")
    v = run([project], cache_dir)
    assert v._cache_stats["scanned"] == keys(
        project / "base.py", project / "middle.py", project / "top.py"
    )
    assert v._cache_stats["reused"] == keys(project / "other.py")
    assert "new_func" in {item.name for item in v.unused_funcs}


def test_relative_imports_are_tracked(tmp_path):
    pkg = tmp_path / "pkg"
    write(pkg / "__init__.py", "")
    write(pkg / "a.py", "def f(): pass\n")
    write(pkg / "b.py", "from .a import f\nf()\n")
    write(pkg / "sub" / "c.py", "from .. import b\n")
    write(pkg / "d.py", "x = 1\n")
    cache_dir = tmp_path / "cache"
    run([pkg], cache_dir)
    write(pkg / "a.py", "def f(): pass\ndef g(): pass\n")
    v = run([pkg], cache_dir)
    assert v._cache_stats["scanned"] == keys(
        pkg / "a.py", pkg / "b.py", pkg / "sub" / "c.py"
    )


def test_new_module_rescans_importers(project, tmp_path):
    cache_dir = tmp_path / "cache"
    write(project / "user.py", "import helper\n")
    run([project], cache_dir)
    write(project / "helper.py", "def helper_func(): pass\n")
    v = run([project], cache_dir)
    assert v._cache_stats["scanned"] == keys(
        project / "helper.py", project / "user.py"
    )


def test_deleted_and_renamed_files_are_removed(project, tmp_path):
    cache_dir = tmp_path / "cache"
    run([project], cache_dir)
    (project / "base.py").unlink()
    (project / "other.py").rename(project / "renamed.py")
    v = run([project], cache_dir)
    assert v._cache_stats["scanned"] == keys(
        project / "middle.py", project / "top.py", project / "renamed.py"
    )
    modules = json.loads(cache.get_cache_path(cache_dir).read_text())[
        "modules"
    ]
    assert set(modules) == all_modules(project)


def test_whitelist_change_invalidates_affected_modules(project, tmp_path):
    cache_dir = tmp_path / "cache"
    whitelist = write(tmp_path / "whitelist.py", "other_func\n")
    v = run([project, whitelist], cache_dir)
    assert "other_func" not in {item.name for item in v.unused_funcs}
    write(whitelist, "base_func\n")
    v = run([project, whitelist], cache_dir)
    assert v._cache_stats["scanned"] == keys(
        whitelist,
        project / "base.py",
        project / "other.py",
        project / "middle.py",
        project / "top.py",
    )
    assert "other_func" in {item.name for item in v.unused_funcs}


def test_missing_cache_is_silent(project, tmp_path, capsys):
    v = run([project], tmp_path / "does" / "not" / "exist")
    assert v._cache_stats["scanned"] == all_modules(project)
    assert capsys.readouterr().err == ""


@pytest.mark.parametrize(
    "corrupt",
    [
        lambda d: (d / "cache.json").write_text("{not json"),
        lambda d: (d / "cache.json").write_text('{"modules": {}}'),
        lambda d: (d / "cache.json.meta").write_text("garbage"),
        lambda d: (d / "cache.json.meta").unlink(),
    ],
)
def test_corrupt_cache_warns_and_rescans(project, tmp_path, capsys, corrupt):
    cache_dir = tmp_path / "cache"
    run([project], cache_dir)
    corrupt(cache_dir)
    capsys.readouterr()
    v = run([project], cache_dir)
    assert "cache is corrupted or unreadable" in capsys.readouterr().err
    assert v._cache_stats["scanned"] == all_modules(project)
    # The rescan writes a valid cache again.
    assert run([project], cache_dir)._cache_stats["scanned"] == set()


def test_invalid_structure_with_valid_checksum(project, tmp_path, capsys):
    cache_dir = tmp_path / "cache"
    run([project], cache_dir)
    raw = b'{"modules": {"x": {"sha256": 1}}}'
    (cache_dir / "cache.json").write_bytes(raw)
    (cache_dir / "cache.json.meta").write_text(
        json.dumps({"sha256": hashlib.sha256(raw).hexdigest()})
    )
    v = run([project], cache_dir)
    assert "cache is corrupted or unreadable" in capsys.readouterr().err
    assert v._cache_stats["scanned"] == all_modules(project)


@pytest.mark.parametrize(
    "patch",
    [
        lambda mp: mp.setattr(cache, "__version__", "other"),
        lambda mp: mp.setattr(sys, "version", "other"),
        lambda mp: mp.setattr(
            cache.importlib.metadata, "version", lambda name: "0.0"
        ),
    ],
)
def test_runtime_signature_change(project, tmp_path, monkeypatch, patch):
    cache_dir = tmp_path / "cache"
    run([project], cache_dir)
    patch(monkeypatch)
    v = run([project], cache_dir)
    assert v._cache_stats["scanned"] == all_modules(project)


def test_settings_change_triggers_full_rescan(project, tmp_path):
    cache_dir = tmp_path / "cache"
    run([project], cache_dir, cache_settings={"a": 1})
    v = run([project], cache_dir, cache_settings={"a": 2})
    assert v._cache_stats["scanned"] == all_modules(project)
    v = run([project], cache_dir, cache_settings={"a": 2})
    assert v._cache_stats["scanned"] == set()
    v = run([project], cache_dir, cache_settings={"a": 2}, ignore_names=["x"])
    assert v._cache_stats["scanned"] == all_modules(project)


def test_errors_are_replayed_from_cache(tmp_path, capsys):
    bad = write(tmp_path / "src" / "bad.py", "def (:\n")
    cache_dir = tmp_path / "cache"
    first = run([bad], cache_dir)
    first_err = capsys.readouterr().err
    second = run([bad], cache_dir)
    assert second._cache_stats["reused"] == keys(bad)
    assert capsys.readouterr().err == first_err
    assert first.exit_code == second.exit_code == ExitCode.InvalidInput


def test_keyboard_interrupt_saves_partial_cache(
    project, tmp_path, monkeypatch
):
    cache_dir = tmp_path / "cache"
    original_scan = core.Vulture.scan
    calls = []

    def interrupting_scan(self, code, filename=""):
        calls.append(filename)
        if len(calls) == 3:
            raise KeyboardInterrupt
        original_scan(self, code, filename)

    monkeypatch.setattr(core.Vulture, "scan", interrupting_scan)
    with pytest.raises(KeyboardInterrupt):
        run([project], cache_dir)
    monkeypatch.undo()

    raw = cache.get_cache_path(cache_dir).read_bytes()
    meta = json.loads((cache_dir / "cache.json.meta").read_text())
    assert meta["sha256"] == hashlib.sha256(raw).hexdigest()
    assert set(json.loads(raw)["modules"]) == keys(*calls[:2])

    fresh = core.Vulture()
    fresh.scavenge([str(project)])
    v = run([project], cache_dir)
    assert v._cache_stats["reused"] <= keys(*calls[:2])
    assert v._cache_stats["scanned"] >= keys(*calls[2:])
    assert unused(v) == unused(fresh)


def test_concurrent_processes_do_not_corrupt_cache(project, tmp_path):
    cache_dir = tmp_path / "cache"
    cmd = [
        sys.executable,
        "-m",
        "vulture",
        f"--cache-dir={cache_dir}",
        str(project),
    ]
    procs = [
        subprocess.Popen(cmd, cwd=REPO, stderr=subprocess.PIPE)
        for _ in range(6)
    ]
    for proc in procs:
        _, err = proc.communicate()
        assert b"corrupted" not in err
    result = subprocess.run(cmd, cwd=REPO, stderr=subprocess.PIPE, check=False)
    assert result.stderr == b""
    assert run([project], cache_dir)._cache_stats["scanned"] == set()


def test_cli_cache_and_cache_clear(project, tmp_path):
    cache_dir = tmp_path / "cache"

    def call(*args):
        return subprocess.call(
            [sys.executable, "-m", "vulture", *args, str(project)],
            cwd=REPO,
        )

    assert call("--cache", f"--cache-dir={cache_dir}") == ExitCode.DeadCode
    assert cache.get_cache_path(cache_dir).is_file()
    stray = write(cache_dir / "sub" / "stray.txt", "x")
    assert call("--cache-clear", f"--cache-dir={cache_dir}") == (
        ExitCode.DeadCode
    )
    assert not stray.exists()
    assert cache.get_cache_path(cache_dir).is_file()


def test_no_cache_by_default(project, tmp_path):
    v = core.Vulture()
    v.scavenge([str(project)])
    assert v._cache_stats["scanned"] == all_modules(project)
    assert v._cache_stats["reused"] == set()


def test_normalize_path(tmp_path, monkeypatch):
    path = tmp_path / "Dir" / "File.py"
    assert cache.normalize_path(path) == str(path.resolve())
    assert cache.normalize_path(str(path)) == cache.normalize_path(path)
    monkeypatch.setattr(sys, "platform", "win32")
    assert cache.normalize_path(path) == cache.normalize_path(path).lower()
