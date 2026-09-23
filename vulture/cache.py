"""
Persistent cache for incremental Vulture runs.

The cache is stored as ``cache.json`` inside the cache directory. Next to it,
``cache.json.meta`` holds the SHA-256 checksum of ``cache.json`` and
``cache.json.bak`` holds a copy of the last successfully saved cache.
"""

import contextlib
import fnmatch
import hashlib
import importlib
import importlib.metadata
import json
import os
import shutil
import sys
import tempfile
from pathlib import Path

from vulture.version import __version__ as _vulture_version

__version__ = "1"

CACHE_FILENAME = "cache.json"
LOCK_FILENAME = "cache.lock"
DEFAULT_CACHE_DIR = ".vulture-cache"

CORRUPT_WARNING = "cache is corrupted or unreadable"


class CacheError(Exception):
    pass


def normalize_path(path):
    """Return an absolute, normalized string version of *path*.

    Paths are compared case-insensitively on Windows.
    """
    normalized = os.path.normpath(os.path.abspath(os.fspath(path)))
    if sys.platform == "win32":
        normalized = normalized.lower()
    return normalized


def get_cache_path(cache_dir):
    return Path(cache_dir) / CACHE_FILENAME


def _get_meta_path(cache_path):
    return cache_path.with_name(cache_path.name + ".meta")


def _get_backup_path(cache_path):
    return cache_path.with_name(cache_path.name + ".bak")


def _get_vulture_version():
    try:
        return importlib.metadata.version("vulture")
    except importlib.metadata.PackageNotFoundError:
        return _vulture_version


def get_runtime_signature():
    return {
        "cache_version": __version__,
        "python": sys.version,
        "vulture": _get_vulture_version(),
    }


def hash_text(text):
    return hashlib.sha256(text.encode("utf-8", "surrogatepass")).hexdigest()


def is_whitelist(path):
    return fnmatch.fnmatch(os.path.basename(path).lower(), "*whitelist*.py")


def _module_parts(path):
    parts = list(Path(path).parts[1:])
    if not parts:
        return []
    stem = parts[-1]
    if stem.endswith(".py"):
        stem = stem[:-3]
    parts[-1] = stem
    if stem == "__init__":
        parts.pop()
    return parts


def _build_module_index(files):
    """Map every dotted tail of each file's module path to the files."""
    index = {}
    for path in files:
        parts = _module_parts(path)
        for start in range(len(parts)):
            index.setdefault(".".join(parts[start:]), set()).add(path)
    return index


def _resolve_import(path, name, index, files):
    if sys.platform == "win32":
        name = name.lower()
    level = len(name) - len(name.lstrip("."))
    dotted = [part for part in name[level:].split(".") if part]
    if level == 0:
        prefixes = (
            ".".join(dotted[:end]) for end in range(1, len(dotted) + 1)
        )
        return set().union(*(index.get(prefix, ()) for prefix in prefixes))
    base = Path(path).parent
    for _ in range(level - 1):
        base = base.parent
    targets = set()
    for end in range(len(dotted) + 1):
        target = base.joinpath(*dotted[:end])
        for candidate in (target / "__init__.py", target.with_suffix(".py")):
            if end == 0 and candidate.name != "__init__.py":
                continue
            key = normalize_path(candidate)
            if key in files:
                targets.add(key)
    return targets


def get_dependents(seeds, entries, files):
    """Return *seeds* plus all files in *entries* transitively importing them.

    *entries* maps normalized paths to cache entries with an "imports" list.
    *files* is the set of all normalized paths that imports may refer to.
    """
    files = set(files)
    index = _build_module_index(files)
    importers = {}
    for path, entry in entries.items():
        for name in entry.get("imports", []):
            if not isinstance(name, str):
                continue
            for target in _resolve_import(path, name, index, files):
                if target != path:
                    importers.setdefault(target, set()).add(path)
    result = set(seeds)
    stack = list(result)
    while stack:
        for importer in importers.get(stack.pop(), ()):
            if importer not in result:
                result.add(importer)
                stack.append(importer)
    return result


def clear_cache(cache_dir):
    """Remove all contents of *cache_dir* (but keep the directory)."""
    cache_dir = Path(cache_dir)
    if not cache_dir.is_dir():
        return
    for child in cache_dir.iterdir():
        if child.is_dir() and not child.is_symlink():
            shutil.rmtree(child, ignore_errors=True)
        else:
            with contextlib.suppress(OSError):
                child.unlink()


@contextlib.contextmanager
def _locked(cache_dir, exclusive):
    """Inter-process lock guarding reads and writes of the cache files."""
    cache_dir.mkdir(parents=True, exist_ok=True)
    lock_file = open(cache_dir / LOCK_FILENAME, "a+b")
    try:
        if os.name == "nt":
            import msvcrt

            lock_file.seek(0)
            # msvcrt only offers exclusive locks; LK_LOCK retries for ~10s.
            while True:
                try:
                    msvcrt.locking(lock_file.fileno(), msvcrt.LK_LOCK, 1)
                    break
                except OSError:
                    continue
            try:
                yield
            finally:
                lock_file.seek(0)
                msvcrt.locking(lock_file.fileno(), msvcrt.LK_UNLCK, 1)
        else:
            import fcntl

            mode = fcntl.LOCK_EX if exclusive else fcntl.LOCK_SH
            fcntl.flock(lock_file.fileno(), mode)
            try:
                yield
            finally:
                fcntl.flock(lock_file.fileno(), fcntl.LOCK_UN)
    finally:
        lock_file.close()


def _read_verified(cache_path):
    """Return the parsed cache or None if it doesn't exist.

    Raise CacheError if the cache is corrupt or cannot be read.
    """
    if not cache_path.exists():
        return None
    try:
        raw = cache_path.read_bytes()
        meta = json.loads(_get_meta_path(cache_path).read_text("utf-8"))
        if not isinstance(meta, dict):
            raise CacheError("invalid metadata")
        if meta.get("sha256") != hashlib.sha256(raw).hexdigest():
            raise CacheError("checksum mismatch")
        data = json.loads(raw.decode("utf-8"))
    except (OSError, ValueError) as err:
        raise CacheError(str(err)) from err
    if not isinstance(data, dict) or not isinstance(data.get("modules"), dict):
        raise CacheError("invalid cache structure")
    for entry in data["modules"].values():
        if not isinstance(entry, dict):
            raise CacheError("invalid module entry")
    return data


def _atomic_write(path, data):
    fd, tmp_name = tempfile.mkstemp(
        dir=path.parent, prefix=path.name + ".", suffix=".tmp"
    )
    try:
        with os.fdopen(fd, "wb") as f:
            f.write(data)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp_name, path)
    except BaseException:
        with contextlib.suppress(OSError):
            os.unlink(tmp_name)
        raise


class Cache:
    def __init__(self, cache_dir, settings=None):
        self.cache_dir = Path(cache_dir)
        self.path = get_cache_path(self.cache_dir)
        self.signature = get_runtime_signature()
        # Round-trip through JSON so tuples etc. compare equal after loading.
        self.settings = json.loads(
            json.dumps(settings or {}, sort_keys=True, default=str)
        )

    def _is_compatible(self, data):
        return (
            data.get("signature") == self.signature
            and data.get("settings") == self.settings
        )

    def load(self):
        """Return the cached module entries usable for this run.

        A missing cache silently yields an empty mapping. A corrupt cache
        prints a warning and also yields an empty mapping.
        """
        try:
            with _locked(self.cache_dir, exclusive=False):
                data = _read_verified(self.path)
        except (CacheError, OSError) as err:
            print(
                f"vulture: warning: {CORRUPT_WARNING} ({err}), "
                f"performing a full scan",
                file=sys.stderr,
            )
            return {}
        if data is None or not self._is_compatible(data):
            return {}
        return data["modules"]

    def save(self, modules, owned):
        """Persist *modules*.

        Entries for files in *owned* (all files handled by this run) are
        taken from *modules*. Entries written concurrently by other
        processes for other files are kept, as long as those files exist.
        """
        with _locked(self.cache_dir, exclusive=True):
            try:
                current = _read_verified(self.path)
            except CacheError:
                current = None
            merged = {}
            if current is not None and self._is_compatible(current):
                merged.update(
                    (path, entry)
                    for path, entry in current["modules"].items()
                    if path not in owned
                )
            merged.update(modules)
            merged = {
                path: entry
                for path, entry in merged.items()
                if os.path.isfile(path)
            }
            data = {
                "signature": self.signature,
                "settings": self.settings,
                "modules": merged,
            }
            raw = json.dumps(data, sort_keys=True).encode("utf-8")
            meta = json.dumps(
                {"sha256": hashlib.sha256(raw).hexdigest()}
            ).encode("utf-8")
            _atomic_write(self.path, raw)
            _atomic_write(_get_meta_path(self.path), meta)
            _atomic_write(_get_backup_path(self.path), raw)
