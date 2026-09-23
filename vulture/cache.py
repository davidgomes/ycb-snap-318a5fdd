"""Incremental analysis cache for Vulture.

The on-disk cache lives in ``cache_dir``:

* ``cache.json`` — analysis results, keyed by normalized path under ``modules``
* ``cache.json.bak`` — backup written on every successful save
* ``cache.json.meta`` — ``{"sha256": "<hex digest of cache.json>"}``

A cache is reused only when its runtime signature and ``cache_settings`` match
the current process. Changed files, files that transitively import them, and
files affected by a whitelist change are analyzed again. Everything else is
restored from ``modules``.
"""

import hashlib
import importlib
import importlib.metadata
import json
import os
import sys
import tempfile
from collections import defaultdict
from contextlib import contextmanager, suppress
from pathlib import Path

try:
    import fcntl
except ImportError:  # pragma: no cover - Windows fallback
    fcntl = None

__version__ = "1"

CACHE_FILENAME = "cache.json"
BACKUP_FILENAME = "cache.json.bak"
META_FILENAME = "cache.json.meta"
LOCK_FILENAME = ".lock"

CORRUPT_CACHE_MESSAGE = "cache is corrupted or unreadable"

_WHITELIST_SUFFIX = "_whitelist.py"


class CacheError(Exception):
    """Base class for cache load failures."""


class CacheMissing(CacheError):
    """No cache file exists yet."""


class CacheCorrupt(CacheError):
    """The cache file or its checksum cannot be trusted."""


def normalize_path(path):
    """Return a stable cache key for *path*.

    Paths are resolved to absolute form. On Windows the key is
    case-insensitive.
    """
    try:
        resolved = Path(path).resolve()
    except OSError:
        resolved = Path(path).absolute()
    text = str(resolved)
    if sys.platform == "win32":
        text = os.path.normcase(text)
    return text


def get_cache_path(cache_dir):
    """Return the path of the main cache file inside *cache_dir*."""
    return Path(cache_dir) / CACHE_FILENAME


def _backup_path(cache_dir):
    return Path(cache_dir) / BACKUP_FILENAME


def _meta_path(cache_dir):
    return Path(cache_dir) / META_FILENAME


def _package_version():
    """Return the installed vulture distribution version."""
    try:
        return importlib.metadata.version("vulture")
    except importlib.metadata.PackageNotFoundError:
        from vulture.version import __version__ as vulture_version

        return vulture_version


def runtime_signature():
    """Return the signature that invalidates the cache when it changes.

    The signature is ``cache.__version__``, ``sys.version`` and the vulture
    package version (via :func:`importlib.metadata.version`).
    """
    return {
        "cache_version": __version__,
        "python": sys.version,
        "vulture": _package_version(),
    }


def hash_file(path):
    """Return the SHA-256 hex digest of *path*."""
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def canonicalize_settings(cache_settings):
    """Return a JSON-stable copy of *cache_settings*."""
    if not cache_settings:
        return {}
    return json.loads(json.dumps(cache_settings, sort_keys=True))


def clear_cache(cache_dir):
    """Remove every entry inside *cache_dir*, keeping the directory itself."""
    directory = Path(cache_dir)
    if not directory.exists():
        return
    for child in list(directory.iterdir()):
        if child.is_dir() and not child.is_symlink():
            _remove_tree(child)
        else:
            child.unlink()


def _remove_tree(path):
    for child in path.iterdir():
        if child.is_dir() and not child.is_symlink():
            _remove_tree(child)
        else:
            child.unlink()
    path.rmdir()


def whitelist_hashes():
    """Return ``{import_name: sha256}`` for bundled whitelist modules."""
    directory = Path(__file__).resolve().parent / "whitelists"
    hashes = {}
    if not directory.is_dir():
        return hashes
    for path in sorted(directory.glob(f"*{_WHITELIST_SUFFIX}")):
        import_name = path.name[: -len(_WHITELIST_SUFFIX)]
        try:
            hashes[import_name] = hash_file(path)
        except OSError:
            continue
    return hashes


def changed_whitelist_names(stored, current):
    """Return whitelist import names whose content changed."""
    stored = stored or {}
    current = current or {}
    names = set(stored) | set(current)
    return {name for name in names if stored.get(name) != current.get(name)}


def _canonical_module_name(path):
    """Return *path*'s package-qualified module name, if it has one."""
    resolved = Path(path).resolve()
    if resolved.name == "__init__.py":
        parts = []
        current = resolved.parent
    elif resolved.suffix == ".py":
        parts = [resolved.stem]
        current = resolved.parent
    else:
        return None
    package = []
    while (current / "__init__.py").is_file():
        package.append(current.name)
        current = current.parent
    if not package:
        return None
    package.reverse()
    full = package + parts
    if not full or not all(part.isidentifier() for part in full):
        return None
    return ".".join(full)


def module_names_for(path, roots):
    """Return Python module names *path* may be imported as."""
    resolved = Path(path).resolve()
    names = set()
    canonical = _canonical_module_name(resolved)
    if canonical:
        names.add(canonical)
    for root in roots:
        try:
            relative = resolved.relative_to(Path(root).resolve())
        except ValueError:
            continue
        if relative.name == "__init__.py":
            parts = list(relative.parts[:-1])
        elif relative.suffix == ".py":
            parts = list(relative.with_suffix("").parts)
        else:
            parts = list(relative.parts)
        if parts and all(part.isidentifier() for part in parts):
            names.add(".".join(parts))
    return names


def build_import_index(files, roots):
    """Map module names to the normalized paths of *files*."""
    index = defaultdict(set)
    for path in files:
        normalized = normalize_path(path)
        for name in module_names_for(path, roots):
            index[name].add(normalized)
    return index


def search_roots(paths):
    """Directories that define top-level module names for *paths*."""
    roots = []
    for path in paths:
        resolved = Path(path).resolve()
        if resolved.is_dir():
            roots.append(resolved)
        else:
            roots.append(resolved.parent)
    return roots


def _entry_is_fresh(entry, path):
    if not isinstance(entry, dict):
        return False
    digest = entry.get("sha256")
    if not isinstance(digest, str) or not digest:
        return False
    try:
        return hash_file(path) == digest
    except OSError:
        return False


def _imports_whitelist(entry, changed_names):
    if not changed_names or not isinstance(entry, dict):
        return False
    for imported in entry.get("imports") or []:
        if not isinstance(imported, str):
            continue
        top_level = imported.split(".", 1)[0]
        if imported in changed_names or top_level in changed_names:
            return True
    return False


def transitive_importer_paths(seeds, entries, index):
    """Return cached modules that import *seeds*, directly or indirectly."""
    reverse = defaultdict(set)
    for path, entry in entries.items():
        if not isinstance(entry, dict):
            continue
        for name in entry.get("imports") or []:
            if not isinstance(name, str):
                continue
            for dependency in index.get(name, ()):
                if dependency != path:
                    reverse[dependency].add(path)

    seen = set(seeds)
    stack = list(seeds)
    found = set()
    while stack:
        current = stack.pop()
        for importer in reverse.get(current, ()):
            if importer in seen:
                continue
            seen.add(importer)
            found.add(importer)
            stack.append(importer)
    return found


def select_for_analysis(files, roots, loaded_modules, changed_whitelists):
    """Split *files* into ``(to_scan, to_reuse)`` path lists.

    *loaded_modules* maps normalized paths to cache entries.
    *changed_whitelists* is a set of import names whose whitelist changed.
    """
    index = build_import_index(files, roots)
    fresh = []
    changed = []
    by_key = {}
    for path in files:
        key = normalize_path(path)
        by_key[key] = path
        entry = loaded_modules.get(key) if loaded_modules else None
        if _entry_is_fresh(entry, path):
            fresh.append(key)
        else:
            changed.append(key)

    affected = set(changed)
    for key in fresh:
        entry = loaded_modules.get(key) if loaded_modules else None
        if _imports_whitelist(entry, changed_whitelists):
            affected.add(key)

    entries = {}
    if loaded_modules:
        for key in by_key:
            if key in loaded_modules:
                entries[key] = loaded_modules[key]
    affected |= transitive_importer_paths(affected, entries, index)

    to_scan = [by_key[key] for key in by_key if key in affected]
    to_reuse = [by_key[key] for key in by_key if key not in affected]
    # Preserve the original file order.
    order = {normalize_path(path): index for index, path in enumerate(files)}
    to_scan.sort(key=lambda path: order[normalize_path(path)])
    to_reuse.sort(key=lambda path: order[normalize_path(path)])
    return to_scan, to_reuse


def _atomic_write(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp_name = tempfile.mkstemp(
        dir=str(path.parent), prefix=f".{path.name}.", suffix=".tmp"
    )
    tmp_path = Path(tmp_name)
    try:
        with os.fdopen(fd, "wb") as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(tmp_path, path)
    except BaseException:
        with suppress(OSError):
            tmp_path.unlink()
        raise


@contextmanager
def _exclusive_lock(cache_dir):
    directory = Path(cache_dir)
    directory.mkdir(parents=True, exist_ok=True)
    lock_path = directory / LOCK_FILENAME
    handle = open(lock_path, "a+")
    try:
        if fcntl is not None:
            fcntl.flock(handle.fileno(), fcntl.LOCK_EX)
        yield
    finally:
        if fcntl is not None:
            fcntl.flock(handle.fileno(), fcntl.LOCK_UN)
        handle.close()


def _sha256_bytes(data):
    return hashlib.sha256(data).hexdigest()


class AnalysisCache:
    """Load and store incremental analysis results."""

    def __init__(self, cache_dir, cache_settings=None):
        self.cache_dir = Path(cache_dir)
        self.cache_settings = canonicalize_settings(cache_settings)
        self.modules = {}
        self.whitelist_hashes = {}
        self.force_full = True
        self.updated = {}

    def load(self):
        """Populate this cache from disk.

        A missing cache triggers a silent full scan. A corrupt cache prints a
        warning to stderr and triggers a full scan. Signature or
        ``cache_settings`` changes also trigger a full scan, without a warning.
        """
        self.modules = {}
        self.updated = {}
        self.whitelist_hashes = {}
        self.force_full = True
        try:
            with _exclusive_lock(self.cache_dir):
                data = _read_verified(self.cache_dir)
        except CacheMissing:
            return
        except CacheCorrupt:
            cache_path = get_cache_path(self.cache_dir)
            print(
                f"Warning: {CORRUPT_CACHE_MESSAGE}: {cache_path}",
                file=sys.stderr,
                flush=True,
            )
            return

        if data.get("signature") != runtime_signature():
            return
        if data.get("cache_settings") != self.cache_settings:
            return

        modules = data.get("modules")
        if not isinstance(modules, dict):
            return
        self.modules = modules
        stored_whitelists = data.get("whitelist_hashes") or {}
        self.whitelist_hashes = stored_whitelists
        self.force_full = False

    def changed_whitelists(self):
        if self.force_full:
            return set()
        return changed_whitelist_names(
            self.whitelist_hashes, whitelist_hashes()
        )

    def update_module(self, path, entry):
        self.updated[normalize_path(path)] = entry

    def save(self):
        """Atomically write the cache, its backup and its checksum file."""
        with _exclusive_lock(self.cache_dir):
            modules = {}
            disk = _read_compatible(
                self.cache_dir, runtime_signature(), self.cache_settings
            )
            if disk is not None:
                disk_modules = disk.get("modules")
                if isinstance(disk_modules, dict):
                    modules.update(disk_modules)
            elif not self.force_full:
                modules.update(self.modules)
            modules.update(self.updated)
            modules = {
                key: value
                for key, value in modules.items()
                if _path_still_exists(key)
            }
            payload = {
                "cache_settings": self.cache_settings,
                "modules": modules,
                "signature": runtime_signature(),
                "whitelist_hashes": whitelist_hashes(),
            }
            raw = (
                json.dumps(payload, indent=2, sort_keys=True) + "\n"
            ).encode("utf-8")
            checksum = _sha256_bytes(raw)
            meta_text = json.dumps({"sha256": checksum}, sort_keys=True)
            meta = (meta_text + "\n").encode("utf-8")
            cache_path = get_cache_path(self.cache_dir)
            backup = _backup_path(self.cache_dir)
            if cache_path.is_file():
                _atomic_write(backup, cache_path.read_bytes())
            else:
                _atomic_write(backup, raw)
            _atomic_write(cache_path, raw)
            _atomic_write(_meta_path(self.cache_dir), meta)
            self.modules = modules
            self.updated = {}
            self.force_full = False


def _path_still_exists(normalized):
    """Return True when a cache key still points at a file.

    Keys are absolute. On Windows they may be case-normalized; ``exists`` is
    case-insensitive there, so a renamed-only-by-case path still matches.
    """
    return Path(normalized).is_file()


def _read_verified(cache_dir):
    """Return the decoded cache, or raise if it is missing or corrupt."""
    cache_path = get_cache_path(cache_dir)
    if not cache_path.is_file():
        raise CacheMissing(str(cache_path))
    meta_path = _meta_path(cache_dir)
    try:
        raw = cache_path.read_bytes()
        meta = json.loads(meta_path.read_text(encoding="utf-8"))
        digest = meta.get("sha256") if isinstance(meta, dict) else None
        if not isinstance(digest, str):
            raise CacheCorrupt("metadata")
        if digest != _sha256_bytes(raw):
            raise CacheCorrupt("checksum")
        data = json.loads(raw.decode("utf-8"))
    except CacheCorrupt:
        raise
    except (OSError, UnicodeError, json.JSONDecodeError, TypeError) as exc:
        raise CacheCorrupt(str(exc)) from exc
    if not isinstance(data, dict) or not isinstance(data.get("modules"), dict):
        raise CacheCorrupt("structure")
    return data


def _read_compatible(cache_dir, signature, cache_settings):
    """Return cache data when it matches *signature* and *cache_settings*."""
    try:
        data = _read_verified(cache_dir)
    except CacheError:
        return None
    if data.get("signature") != signature:
        return None
    if data.get("cache_settings") != cache_settings:
        return None
    return data
