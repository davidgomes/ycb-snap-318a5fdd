"""Incremental analysis cache for Vulture."""

import hashlib
import importlib
import importlib.metadata
import json
import os
import sys
from pathlib import Path

__version__ = "1"

_CACHE_FILENAME = "cache.json"
_LOCK_FILENAME = ".lock"
_CORRUPT_WARNING = "cache is corrupted or unreadable"


def normalize_path(path):
    """Return a stable, absolute path string.

    On Windows the result is case-folded so that paths differing only by
    case refer to the same cache entry.
    """
    try:
        resolved = str(Path(path).resolve())
    except OSError:
        resolved = os.path.abspath(str(path))
    if os.name == "nt":
        resolved = os.path.normcase(resolved)
    return resolved


def get_cache_path(cache_dir):
    """Return the path of the main cache file inside *cache_dir*."""
    return Path(cache_dir) / _CACHE_FILENAME


def runtime_signature():
    """Identity of the interpreter and Vulture build that produced a cache."""
    return {
        "cache_version": __version__,
        "python": sys.version,
        "vulture": _vulture_version(),
    }


def _vulture_version():
    try:
        return importlib.metadata.version("vulture")
    except importlib.metadata.PackageNotFoundError:
        from vulture.version import __version__ as package_version

        return package_version


def canonicalize_settings(settings):
    """Return a JSON-stable copy of cache settings."""
    if not settings:
        return {}
    canonical = {}
    for key in sorted(settings):
        value = settings[key]
        if isinstance(value, list):
            canonical[key] = sorted(value)
        else:
            canonical[key] = value
    return canonical


def file_sha256(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def empty_cache(settings=None):
    return {
        "signature": runtime_signature(),
        "settings": canonicalize_settings(settings),
        "modules": {},
    }


def signature_matches(data, settings):
    if not isinstance(data, dict):
        return False
    if data.get("signature") != runtime_signature():
        return False
    return data.get("settings") == canonicalize_settings(settings)


def resolve_relative(module_name, is_init, level, module):
    """Resolve a relative import to an absolute module name."""
    parts = module_name.split(".") if module_name else []
    if not is_init and parts:
        parts = parts[:-1]
    drop = level - 1
    if drop:
        if drop > len(parts):
            return None
        parts = parts[:-drop]
    if module:
        parts.extend(str(module).split("."))
    if not parts:
        return None
    return ".".join(parts)


def dependency_paths(import_names, name_to_paths, whitelist_by_top):
    """Map absolute import names to normalized file paths."""
    deps = set()
    for abs_name in import_names:
        if not abs_name:
            continue
        for modname, paths in name_to_paths.items():
            if not modname:
                continue
            if abs_name == modname or abs_name.startswith(modname + "."):
                deps.update(paths)
        top = abs_name.split(".", 1)[0]
        whitelist = whitelist_by_top.get(top)
        if whitelist is not None:
            deps.add(normalize_path(whitelist))
    return deps


class _FileLock:
    """Exclusive lock so concurrent Vulture processes cannot tear the cache."""

    def __init__(self, cache_dir):
        self._dir = Path(cache_dir)
        self._fd = None

    def __enter__(self):
        self._dir.mkdir(parents=True, exist_ok=True)
        lock_path = self._dir / _LOCK_FILENAME
        self._fd = os.open(str(lock_path), os.O_CREAT | os.O_RDWR, 0o644)
        if os.name == "nt":
            import msvcrt

            if os.path.getsize(lock_path) < 1:
                os.write(self._fd, b"\0")
            os.lseek(self._fd, 0, os.SEEK_SET)
            msvcrt.locking(self._fd, msvcrt.LK_LOCK, 1)
        else:
            import fcntl

            fcntl.flock(self._fd, fcntl.LOCK_EX)
        return self

    def __exit__(self, _exc_type, _exc, _tb):
        if self._fd is None:
            return False
        if os.name == "nt":
            import msvcrt

            os.lseek(self._fd, 0, os.SEEK_SET)
            try:
                msvcrt.locking(self._fd, msvcrt.LK_UNLCK, 1)
            except OSError:
                pass
        else:
            import fcntl

            fcntl.flock(self._fd, fcntl.LOCK_UN)
        os.close(self._fd)
        self._fd = None
        return False


def clear_cache(cache_dir):
    """Remove every file in *cache_dir* except the lock file."""
    directory = Path(cache_dir)
    with _FileLock(directory):
        _clear_contents(directory)


def _clear_contents(directory):
    directory = Path(directory)
    if not directory.exists():
        return
    for child in directory.iterdir():
        if child.name == _LOCK_FILENAME:
            continue
        if child.is_dir() and not child.is_symlink():
            for nested in sorted(child.rglob("*"), reverse=True):
                if nested.is_dir() and not nested.is_symlink():
                    nested.rmdir()
                else:
                    nested.unlink()
            child.rmdir()
        else:
            child.unlink()


def load_cache(cache_dir):
    """Load the cache.

    Returns ``None`` when no cache file exists. Prints a warning and returns
    an empty cache when the file is unreadable or its checksum does not
    match ``cache.json.meta``.
    """
    path = get_cache_path(cache_dir)
    if not path.is_file():
        return None
    try:
        payload = path.read_bytes()
        meta_path = path.parent / (path.name + ".meta")
        meta = json.loads(meta_path.read_text(encoding="utf-8"))
        actual = hashlib.sha256(payload).hexdigest()
        if not isinstance(meta, dict) or meta.get("sha256") != actual:
            raise ValueError("checksum mismatch")
        data = json.loads(payload.decode("utf-8"))
        if not isinstance(data, dict) or not isinstance(
            data.get("modules"), dict
        ):
            raise ValueError("invalid cache")
    except Exception:
        print(f"Warning: {_CORRUPT_WARNING}", file=sys.stderr)
        return empty_cache()
    return data


def save_cache(cache_dir, data):
    """Atomically write the cache, its backup, and its checksum."""
    directory = Path(cache_dir)
    directory.mkdir(parents=True, exist_ok=True)
    path = get_cache_path(directory)
    payload = json.dumps(data, sort_keys=True).encode("utf-8")
    digest = hashlib.sha256(payload).hexdigest()
    meta = json.dumps({"sha256": digest}).encode("utf-8")
    _atomic_write(path, payload)
    _atomic_write(path.parent / (path.name + ".bak"), payload)
    _atomic_write(path.parent / (path.name + ".meta"), meta)


def _atomic_write(path, payload):
    temporary = path.with_name(path.name + ".tmp")
    with open(temporary, "wb") as handle:
        handle.write(payload)
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(temporary, path)


def analysis_hash(entry):
    payload = {
        "items": entry.get("items", {}),
        "source_sha256": entry.get("source_sha256", ""),
        "used_names": entry.get("used_names", []),
    }
    encoded = json.dumps(payload, sort_keys=True).encode("utf-8")
    return hashlib.sha256(encoded).hexdigest()


def module_name_for(path, roots):
    """Best-effort dotted module name of *path* relative to *roots*."""
    path = Path(path).resolve()
    ordered = sorted(
        (Path(root).resolve() for root in roots),
        key=lambda root: len(str(root)),
        reverse=True,
    )
    for root in ordered:
        base = root if root.is_dir() else root.parent
        try:
            relative = path.relative_to(base)
        except ValueError:
            continue
        parts = list(relative.with_suffix("").parts)
        if parts and parts[-1] == "__init__":
            parts = parts[:-1]
        return ".".join(parts)
    stem = path.stem
    if stem == "__init__":
        return path.parent.name
    return stem


def is_package_init(path):
    return Path(path).name == "__init__.py"


def entry_dependencies_changed(entry, modules, hash_cache):
    depends_on = entry.get("depends_on")
    if not isinstance(depends_on, dict):
        return True
    for dep, recorded in depends_on.items():
        if not os.path.exists(dep):
            return True
        dep_entry = modules.get(dep)
        if not isinstance(dep_entry, dict):
            return True
        if dep_entry.get("analysis_hash") != recorded:
            return True
        try:
            current = hash_cache.setdefault(dep, file_sha256(dep))
        except OSError:
            return True
        if current != dep_entry.get("source_sha256"):
            return True
    return False


def files_to_rescan(modules, candidates, hash_cache):
    """Return cache keys that must be analyzed again.

    *candidates* maps a normalized path to a source path that exists.
    A file is rescanned when its contents changed, a dependency's
    analysis changed, or a (transitive) dependency is itself rescanned.
    """
    rescan = set()
    for key, source in candidates.items():
        entry = modules.get(key)
        if not isinstance(entry, dict):
            rescan.add(key)
            continue
        try:
            current = hash_cache.setdefault(key, file_sha256(source))
        except OSError:
            rescan.add(key)
            continue
        if current != entry.get("source_sha256"):
            rescan.add(key)
            continue
        if entry_dependencies_changed(entry, modules, hash_cache):
            rescan.add(key)

    changed = True
    while changed:
        changed = False
        for key, entry in modules.items():
            if key in rescan or not isinstance(entry, dict):
                continue
            depends_on = entry.get("depends_on")
            if isinstance(depends_on, dict) and any(
                dep in rescan for dep in depends_on
            ):
                rescan.add(key)
                changed = True
    return rescan


def prune_missing_modules(modules):
    """Drop entries whose files were deleted or renamed."""
    stale = [key for key in modules if not os.path.exists(key)]
    for key in stale:
        del modules[key]
