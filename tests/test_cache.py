import json
from pathlib import Path

import pytest

from vulture import cache
from vulture.core import Vulture


def _names(vulture):
    return sorted(item.name for item in vulture.get_unused_code())


def test_normalize_path_is_absolute():
    normalized = cache.normalize_path("vulture/core.py")
    assert Path(normalized).is_absolute()


def test_get_cache_path():
    assert cache.get_cache_path(".vulture-cache") == Path(
        ".vulture-cache/cache.json"
    )


def test_missing_cache_is_silent(tmp_path, capsys):
    assert cache.load_cache(tmp_path / "missing") is None
    captured = capsys.readouterr()
    assert captured.err == ""
    assert captured.out == ""


def test_corrupt_cache_warns(tmp_path, capsys):
    cache_dir = tmp_path / "cache"
    cache_dir.mkdir()
    path = cache.get_cache_path(cache_dir)
    path.write_text("{not json", encoding="utf-8")
    (cache_dir / "cache.json.meta").write_text(
        json.dumps({"sha256": "0"}), encoding="utf-8"
    )
    loaded = cache.load_cache(cache_dir)
    assert loaded["modules"] == {}
    assert "cache is corrupted or unreadable" in capsys.readouterr().err


def test_checksum_mismatch_is_corrupt(tmp_path, capsys):
    cache_dir = tmp_path / "cache"
    data = cache.empty_cache()
    cache.save_cache(cache_dir, data)
    path = cache.get_cache_path(cache_dir)
    path.write_text(path.read_text(encoding="utf-8") + " ", encoding="utf-8")
    loaded = cache.load_cache(cache_dir)
    assert loaded["modules"] == {}
    assert "cache is corrupted or unreadable" in capsys.readouterr().err


def test_save_writes_backup_and_meta(tmp_path):
    cache_dir = tmp_path / "cache"
    data = cache.empty_cache({"ignore_names": ["b", "a"]})
    cache.save_cache(cache_dir, data)
    path = cache.get_cache_path(cache_dir)
    payload = path.read_bytes()
    backup = (cache_dir / "cache.json.bak").read_bytes()
    meta = json.loads((cache_dir / "cache.json.meta").read_text())
    assert backup == payload
    assert meta["sha256"] == __import__("hashlib").sha256(payload).hexdigest()
    loaded = cache.load_cache(cache_dir)
    assert loaded["modules"] == {}
    assert loaded["settings"]["ignore_names"] == ["a", "b"]


def test_cache_clear_removes_contents(tmp_path):
    cache_dir = tmp_path / "cache"
    cache.save_cache(cache_dir, cache.empty_cache())
    (cache_dir / "extra.txt").write_text("x", encoding="utf-8")
    cache.clear_cache(cache_dir)
    assert cache_dir.is_dir()
    remaining = {path.name for path in cache_dir.iterdir()}
    assert "cache.json" not in remaining
    assert "extra.txt" not in remaining


def test_reuses_unchanged_files(tmp_path):
    project = tmp_path / "proj"
    project.mkdir()
    module = project / "mod.py"
    module.write_text("def used():\n    return 1\nused()\n", encoding="utf-8")
    cache_dir = tmp_path / "vcache"
    settings = {"ignore_names": [], "ignore_decorators": [], "exclude": []}

    first = Vulture(cache_dir=cache_dir, cache_settings=settings)
    first.scavenge([project])
    assert _names(first) == []
    assert cache.normalize_path(module) in first._cache_stats["scanned"]
    assert first._cache_stats["reused"] == set()

    second = Vulture(cache_dir=cache_dir, cache_settings=settings)
    second.scavenge([project])
    assert _names(second) == []
    assert cache.normalize_path(module) in second._cache_stats["reused"]
    assert cache.normalize_path(module) not in second._cache_stats["scanned"]


def test_changed_file_and_importers_are_rescanned(tmp_path):
    project = tmp_path / "proj"
    pkg = project / "pkg"
    pkg.mkdir(parents=True)
    (pkg / "__init__.py").write_text("", encoding="utf-8")
    leaf = pkg / "leaf.py"
    mid = pkg / "mid.py"
    top = pkg / "top.py"
    other = project / "other.py"
    leaf.write_text("VALUE = 1\n", encoding="utf-8")
    mid.write_text("from pkg.leaf import VALUE\n", encoding="utf-8")
    top.write_text("import pkg.mid\n", encoding="utf-8")
    other.write_text("OTHER = 1\n", encoding="utf-8")
    cache_dir = tmp_path / "vcache"
    settings = {"ignore_names": [], "ignore_decorators": [], "exclude": []}

    Vulture(cache_dir=cache_dir, cache_settings=settings).scavenge([project])
    leaf.write_text("VALUE = 2\n", encoding="utf-8")

    again = Vulture(cache_dir=cache_dir, cache_settings=settings)
    again.scavenge([project])
    scanned = again._cache_stats["scanned"]
    reused = again._cache_stats["reused"]
    assert cache.normalize_path(leaf) in scanned
    assert cache.normalize_path(mid) in scanned
    assert cache.normalize_path(top) in scanned
    assert cache.normalize_path(other) in reused


def test_deleted_file_is_removed_from_cache(tmp_path):
    project = tmp_path / "proj"
    project.mkdir()
    kept = project / "kept.py"
    gone = project / "gone.py"
    kept.write_text("KEPT = 1\n", encoding="utf-8")
    gone.write_text("GONE = 1\n", encoding="utf-8")
    cache_dir = tmp_path / "vcache"
    settings = {"ignore_names": [], "ignore_decorators": [], "exclude": []}
    Vulture(cache_dir=cache_dir, cache_settings=settings).scavenge([project])
    gone.unlink()
    Vulture(cache_dir=cache_dir, cache_settings=settings).scavenge([project])
    stored = cache.load_cache(cache_dir)["modules"]
    assert cache.normalize_path(gone) not in stored
    assert cache.normalize_path(kept) in stored


def test_settings_change_rescans(tmp_path):
    project = tmp_path / "proj"
    project.mkdir()
    module = project / "mod.py"
    module.write_text("def keep_me():\n    pass\n", encoding="utf-8")
    cache_dir = tmp_path / "vcache"
    Vulture(
        cache_dir=cache_dir,
        ignore_names=[],
        cache_settings={"ignore_names": []},
    ).scavenge([project])
    again = Vulture(
        cache_dir=cache_dir,
        ignore_names=["keep_me"],
        cache_settings={"ignore_names": ["keep_me"]},
    )
    again.scavenge([project])
    assert cache.normalize_path(module) in again._cache_stats["scanned"]
    assert _names(again) == []


def test_cache_matches_uncached_results(tmp_path):
    project = tmp_path / "proj"
    project.mkdir()
    (project / "a.py").write_text(
        "import os\n\ndef dead():\n    pass\n", encoding="utf-8"
    )
    (project / "b.py").write_text(
        "from a import dead\n", encoding="utf-8"
    )
    plain = Vulture()
    plain.scavenge([project])
    cache_dir = tmp_path / "vcache"
    cached = Vulture(cache_dir=cache_dir, cache_settings={})
    cached.scavenge([project])
    assert _names(cached) == _names(plain)


def test_keyboard_interrupt_saves_partial_cache(tmp_path, monkeypatch):
    project = tmp_path / "proj"
    project.mkdir()
    first = project / "first.py"
    second = project / "second.py"
    first.write_text("A = 1\n", encoding="utf-8")
    second.write_text("B = 1\n", encoding="utf-8")
    cache_dir = tmp_path / "vcache"
    vulture = Vulture(cache_dir=cache_dir, cache_settings={})
    original = vulture._scan_and_store
    calls = {"n": 0}

    def interrupt(self_key, source, display, module_name, is_init):
        calls["n"] += 1
        if calls["n"] > 1:
            raise KeyboardInterrupt
        return original(self_key, source, display, module_name, is_init)

    monkeypatch.setattr(vulture, "_scan_and_store", interrupt)
    with pytest.raises(KeyboardInterrupt):
        vulture.scavenge([project])
    path = cache.get_cache_path(cache_dir)
    assert path.is_file()
    assert (cache_dir / "cache.json.bak").is_file()
    assert (cache_dir / "cache.json.meta").is_file()
    loaded = cache.load_cache(cache_dir)
    assert loaded is not None
    assert loaded["modules"]


def test_whitelist_change_rescans_importer(tmp_path):
    project = tmp_path / "proj"
    project.mkdir()
    module = project / "uses_sys.py"
    module.write_text("import sys\n", encoding="utf-8")
    cache_dir = tmp_path / "vcache"
    settings = {}
    Vulture(cache_dir=cache_dir, cache_settings=settings).scavenge([project])

    stored = cache.load_cache(cache_dir)["modules"]
    entry = stored[cache.normalize_path(module)]
    whitelist_keys = [
        dep for dep in entry["depends_on"] if dep.endswith("_whitelist.py")
    ]
    assert whitelist_keys
    target = Path(whitelist_keys[0])
    original = target.read_text(encoding="utf-8")
    target.write_text(original + "\n# cache bust\n", encoding="utf-8")
    try:
        again = Vulture(cache_dir=cache_dir, cache_settings=settings)
        again.scavenge([project])
    finally:
        target.write_text(original, encoding="utf-8")
    assert cache.normalize_path(module) in again._cache_stats["scanned"]
    assert cache.normalize_path(target) in again._cache_stats["scanned"]
