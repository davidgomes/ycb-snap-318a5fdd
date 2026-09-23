"""
Persistent cache for incremental analysis.

The cache stores the per-module analysis results (defined items, used names,
error messages and imports) keyed by normalized file path. On subsequent runs
only modules whose contents changed, and the modules that transitively import
them, are analyzed again.
"""

import contextlib
import hashlib
import importlib.metadata
import json
import ntpath
import os
import shutil
import sys
import tempfile
from collections import defaultdict
from pathlib import Path

from vulture.version import __version__ as _source_version

try:
    import fcntl
except ImportError:  # pragma: no cover (Windows)
    fcntl = None

try:
    import msvcrt
except ImportError:
    msvcrt = None

#: Version of the cache format. Bump when the layout of cached data changes.
__version__ = "1"

DEFAULT_CACHE_DIR = ".vulture-cache"
CACHE_FILENAME = "cache.json"
BACKUP_SUFFIX = ".bak"
META_SUFFIX = ".meta"
LOCK_SUFFIX = ".lock"

ITEM_TYPES = (
    "attribute",
    "class",
    "function",
    "import",
    "method",
    "property",
    "variable",
    "unreachable_code",
)

CORRUPTION_WARNING = (
    "Warning: vulture cache is corrupted or unreadable ({reason}). "
    "Performing a full scan."
)


def normalize_path(path):
    """Return a canonical string for *path* that can be used as a cache key.

    Paths are made absolute and symlinks are resolved. On Windows, paths are
    compared case-insensitively, so they are lower-cased as well.
    """
    path = os.path.realpath(os.fspath(path))
    if os.name == "nt" or sys.platform == "win32":
        path = ntpath.normcase(path)
    return path


def get_cache_path(cache_dir):
    """Return the path of the main cache file inside *cache_dir*."""
    return Path(cache_dir) / CACHE_FILENAME


def _sibling(cache_path, suffix):
    return cache_path.with_name(cache_path.name + suffix)


def _vulture_version():
    try:
        return importlib.metadata.version("vulture")
    except importlib.metadata.PackageNotFoundError:
        return _source_version


def get_runtime_signature():
    """Cached results are only valid for the runtime that produced them."""
    return {
        "cache": __version__,
        "python": sys.version,
        "vulture": _vulture_version(),
    }


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def file_digest(path):
    try:
        with open(path, "rb") as f:
            return sha256(f.read())
    except OSError:
        return None


def clear_cache(cache_dir):
    """Remove all contents of *cache_dir* (but keep the directory)."""
    cache_dir = Path(cache_dir)
    if not cache_dir.is_dir():
        return
    for child in cache_dir.iterdir():
        if child.is_dir() and not child.is_symlink():
            shutil.rmtree(child, ignore_errors=True)
        else:
            with contextlib.suppress(FileNotFoundError):
                child.unlink()


def _jsonable(value):
    return json.loads(json.dumps(value, sort_keys=True, default=repr))


@contextlib.contextmanager
def _locked(cache_path, exclusive):
    """Serialize access to the cache files across processes."""
    try:
        handle = open(_sibling(cache_path, LOCK_SUFFIX), "a+b")
    except OSError:
        handle = None
    if handle is None:
        # We cannot create a lock file (e.g., read-only directory). Readers
        # still verify the checksum, so a concurrent write is detected.
        yield
        return
    with handle:
        if fcntl is not None:
            fcntl.flock(
                handle.fileno(), fcntl.LOCK_EX if exclusive else fcntl.LOCK_SH
            )
        elif msvcrt is not None:  # pragma: no cover (Windows)
            while True:
                handle.seek(0)
                try:
                    msvcrt.locking(handle.fileno(), msvcrt.LK_LOCK, 1)
                    break
                except OSError:
                    continue
        try:
            yield
        finally:
            if fcntl is not None:
                fcntl.flock(handle.fileno(), fcntl.LOCK_UN)
            elif msvcrt is not None:  # pragma: no cover (Windows)
                handle.seek(0)
                msvcrt.locking(handle.fileno(), msvcrt.LK_UNLCK, 1)


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


def _is_str_list(value):
    return isinstance(value, list) and all(isinstance(v, str) for v in value)


def _is_valid_item(item):
    return (
        isinstance(item, list)
        and len(item) == 5
        and isinstance(item[0], str)
        and isinstance(item[1], int)
        and isinstance(item[2], int)
        and isinstance(item[3], str)
        and isinstance(item[4], int)
    )


def _is_valid_result(result):
    if not isinstance(result, dict):
        return False
    defined = result.get("defined")
    return (
        isinstance(defined, dict)
        and all(
            typ in ITEM_TYPES
            and isinstance(items, list)
            and all(_is_valid_item(item) for item in items)
            for typ, items in defined.items()
        )
        and all(
            _is_str_list(result.get(key))
            for key in ("used", "errors", "imports", "rel_imports")
        )
    )


def _is_valid_entries(entries):
    return isinstance(entries, dict) and all(
        isinstance(key, str)
        and isinstance(entry, dict)
        and isinstance(entry.get("sha256"), str)
        and _is_valid_result(entry.get("result"))
        for key, entry in entries.items()
    )


class CacheError(Exception):
    pass


def _read(cache_path):
    """Return the verified cache contents or None if there is no cache.

    Raise CacheError if the cache exists but cannot be used.
    """
    if not cache_path.exists():
        return None
    try:
        raw = cache_path.read_bytes()
        meta = json.loads(_sibling(cache_path, META_SUFFIX).read_bytes())
    except (OSError, ValueError) as err:
        raise CacheError(str(err)) from err
    if not isinstance(meta, dict) or meta.get("sha256") != sha256(raw):
        raise CacheError("checksum mismatch")
    try:
        data = json.loads(raw)
    except ValueError as err:
        raise CacheError(str(err)) from err
    if not (
        isinstance(data, dict)
        and _is_valid_entries(data.get("modules"))
        and _is_valid_entries(data.get("whitelists", {}))
    ):
        raise CacheError("invalid cache structure")
    return data


class Cache:
    """In-memory view of the on-disk cache."""

    def __init__(self, cache_dir, settings=None):
        self.cache_dir = Path(cache_dir)
        self.cache_path = get_cache_path(self.cache_dir)
        self.settings = _jsonable(settings or {})
        self.signature = get_runtime_signature()
        self.modules = {}
        self.whitelists = {}
        # Keys that were deliberately dropped during this run and must not
        # be resurrected from the on-disk cache when merging.
        self._dropped = set()

    def _is_compatible(self, data):
        return (
            data.get("signature") == self.signature
            and data.get("settings") == self.settings
        )

    def load(self, log=None):
        """Load cached entries. Missing or outdated caches are ignored."""
        if not self.cache_path.parent.is_dir():
            return
        try:
            with _locked(self.cache_path, exclusive=False):
                data = _read(self.cache_path)
        except (CacheError, OSError) as err:
            print(CORRUPTION_WARNING.format(reason=err), file=sys.stderr)
            return
        if data is None:
            return
        if not self._is_compatible(data):
            if log:
                log("Cache was created with different settings. Ignoring it.")
            return
        self.modules = data["modules"]
        self.whitelists = data.get("whitelists", {})

    def store(self, key, digest, result):
        self.modules[key] = {"sha256": digest, "result": result}
        self._dropped.discard(key)

    def store_whitelist(self, key, digest, result):
        self.whitelists[key] = {"sha256": digest, "result": result}

    def discard(self, keys):
        for key in keys:
            self.modules.pop(key, None)
            self._dropped.add(key)

    def _merge_from_disk(self):
        """Keep entries that concurrent vulture processes wrote meanwhile."""
        try:
            data = _read(self.cache_path)
        except (CacheError, OSError):
            return
        if data is None or not self._is_compatible(data):
            return
        for key, entry in data["modules"].items():
            if (
                key not in self.modules
                and key not in self._dropped
                and os.path.exists(key)
            ):
                self.modules[key] = entry
        for key, entry in data.get("whitelists", {}).items():
            self.whitelists.setdefault(key, entry)

    def save(self):
        """Atomically write the cache, its backup and its checksum file."""
        try:
            self.cache_dir.mkdir(parents=True, exist_ok=True)
            with _locked(self.cache_path, exclusive=True):
                self._merge_from_disk()
                data = json.dumps(
                    {
                        "version": __version__,
                        "signature": self.signature,
                        "settings": self.settings,
                        "modules": self.modules,
                        "whitelists": self.whitelists,
                    },
                    sort_keys=True,
                    separators=(",", ":"),
                ).encode("utf-8")
                meta = json.dumps({"sha256": sha256(data)}).encode("utf-8")
                _atomic_write(_sibling(self.cache_path, BACKUP_SUFFIX), data)
                _atomic_write(self.cache_path, data)
                _atomic_write(_sibling(self.cache_path, META_SUFFIX), meta)
        except OSError as err:
            print(
                f"Warning: could not write vulture cache to "
                f"{self.cache_dir}: {err}",
                file=sys.stderr,
            )


def _module_names(path):
    """Return all dotted names under which the file *path* may be imported."""
    parts = Path(path).with_suffix("").parts[1:]
    if parts and parts[-1] == "__init__":
        parts = parts[:-1]
    return {".".join(parts[i:]) for i in range(len(parts))}


def _prefixes(dotted_name):
    parts = dotted_name.split(".")
    return {".".join(parts[: i + 1]) for i in range(len(parts))}


def find_dependents(seeds, imports, known_paths):
    """Return *seeds* plus all modules that transitively import any of them.

    :param seeds: Normalized paths of changed, added or deleted modules.
    :param imports: Maps normalized module paths to their analysis result.
    :param known_paths: All normalized module paths imports may resolve to.
    """
    index = defaultdict(set)
    for path in known_paths:
        for name in _module_names(path):
            index[name].add(path)

    importers = defaultdict(set)
    for module, result in imports.items():
        deps = {
            dep
            for dotted_name in result["imports"]
            for prefix in _prefixes(dotted_name)
            for dep in index.get(prefix, ())
        }
        deps.update(p for p in result["rel_imports"] if p in known_paths)
        deps.discard(module)
        for dep in deps:
            importers[dep].add(module)

    affected = set(seeds)
    stack = list(seeds)
    while stack:
        for importer in importers.get(stack.pop(), ()):
            if importer not in affected:
                affected.add(importer)
                stack.append(importer)
    return affected


def is_whitelist(path):
    return "whitelist" in Path(path).name.lower()


def defined_names(result):
    return {item[0] for items in result["defined"].values() for item in items}
