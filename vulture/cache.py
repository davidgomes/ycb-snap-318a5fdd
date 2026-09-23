"""
Persistent per-module analysis cache for vulture.
"""

import hashlib
import importlib.metadata
import json
import os
import shutil
import sys
import tempfile
from pathlib import Path

__version__ = "1"

CACHE_FILENAME = "cache.json"
CORRUPT_WARNING = (
    "vulture: warning: cache is corrupted or unreadable, "
    "performing a full scan"
)


class CacheCorruptError(Exception):
    pass


def normalize_path(path):
    """Return an absolute, normalized path string (case-insensitive on
    Windows)."""
    return os.path.normcase(os.path.normpath(os.path.abspath(str(path))))


def get_cache_path(cache_dir):
    return Path(cache_dir) / CACHE_FILENAME


def _meta_path(cache_path):
    return cache_path.with_name(cache_path.name + ".meta")


def _backup_path(cache_path):
    return cache_path.with_name(cache_path.name + ".bak")


def _vulture_version():
    try:
        return importlib.metadata.version("vulture")
    except importlib.metadata.PackageNotFoundError:
        from vulture.version import __version__ as version

        return version


def get_runtime_signature():
    return {
        "cache_version": __version__,
        "python": sys.version,
        "vulture": _vulture_version(),
    }


def hash_bytes(data):
    return hashlib.sha256(data).hexdigest()


def clear_cache(cache_dir):
    cache_dir = Path(cache_dir)
    if not cache_dir.is_dir():
        return
    for child in cache_dir.iterdir():
        if child.is_dir() and not child.is_symlink():
            shutil.rmtree(child, ignore_errors=True)
        else:
            try:
                child.unlink()
            except OSError:
                pass


def _read_verified(cache_path):
    raw = cache_path.read_bytes()
    meta = json.loads(_meta_path(cache_path).read_text(encoding="utf-8"))
    if not isinstance(meta, dict) or meta.get("sha256") != hash_bytes(raw):
        raise CacheCorruptError("checksum mismatch")
    data = json.loads(raw.decode("utf-8"))
    if not isinstance(data, dict) or not isinstance(
        data.get("modules"), dict
    ):
        raise CacheCorruptError("invalid structure")
    return data


def load_cache(cache_dir, settings):
    """
    Return the cached "modules" dict, or an empty dict if the cache is
    missing, stale or corrupt (a warning is printed in the latter case).
    """
    cache_path = get_cache_path(cache_dir)
    if not cache_path.exists():
        return {}
    try:
        data = _read_verified(cache_path)
    except (OSError, ValueError, CacheCorruptError):
        print(CORRUPT_WARNING, file=sys.stderr)
        return {}
    if (
        data.get("signature") != get_runtime_signature()
        or data.get("settings") != settings
    ):
        return {}
    return data["modules"]


def _atomic_write(path, data):
    fd, tmp = tempfile.mkstemp(
        dir=str(path.parent), prefix=path.name + ".", suffix=".tmp"
    )
    try:
        with os.fdopen(fd, "wb") as f:
            f.write(data)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, path)
    except BaseException:
        try:
            os.unlink(tmp)
        except OSError:
            pass
        raise


def save_cache(cache_dir, settings, modules):
    cache_path = get_cache_path(cache_dir)
    cache_path.parent.mkdir(parents=True, exist_ok=True)
    raw = json.dumps(
        {
            "signature": get_runtime_signature(),
            "settings": settings,
            "modules": modules,
        },
        sort_keys=True,
    ).encode("utf-8")
    meta = json.dumps({"sha256": hash_bytes(raw)}).encode("utf-8")
    # Readers verify cache.json against the meta checksum, so a reader
    # racing a writer sees at worst a mismatch (full rescan), never bad data.
    _atomic_write(cache_path, raw)
    _atomic_write(_meta_path(cache_path), meta)
    _atomic_write(_backup_path(cache_path), raw)


def resolve_imports(entries):
    """
    Given {norm_path: entry} where entry has "imports" (list of
    [module, level]) and "path", return {norm_path: set(norm_paths imported)}.
    """
    by_suffix = {}
    for norm in entries:
        p = Path(norm)
        parts = list(p.with_suffix("").parts)
        if parts and parts[-1] == "__init__":
            parts = parts[:-1]
        for i in range(len(parts)):
            by_suffix.setdefault(".".join(parts[i:]), set()).add(norm)

    graph = {}
    for norm, entry in entries.items():
        deps = set()
        base = Path(norm).parent
        for module, level in entry.get("imports", []):
            if level:
                d = base
                for _ in range(level - 1):
                    d = d.parent
                target = d.joinpath(*module.split(".")) if module else d
                for cand in (
                    target.with_suffix(".py"),
                    target / "__init__.py",
                ):
                    c = normalize_path(cand)
                    if c in entries:
                        deps.add(c)
            else:
                name = module
                while name:
                    deps |= by_suffix.get(name, set())
                    name = name.rpartition(".")[0]
        deps.discard(norm)
        graph[norm] = deps
    return graph
