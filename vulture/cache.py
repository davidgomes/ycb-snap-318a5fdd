"""Incremental analysis cache for Vulture.

The cache records per-file analysis so later runs can skip files whose
contents have not changed. A file is analyzed again when its contents
change, when a file it imports (directly or indirectly) changes, or when
a built-in whitelist it relies on changes.

``cache.json`` stores the cache. ``cache.json.meta`` stores a SHA-256
checksum of that file, and ``cache.json.bak`` is a copy written on every
successful save.
"""

import ast
import contextlib
import hashlib
import importlib
import importlib.metadata
import json
import os
import shutil
import sys
import tempfile
from pathlib import Path

__version__ = "1"

CORRUPT_CACHE_MESSAGE = "cache is corrupted or unreadable"

_CACHE_FILENAME = "cache.json"
_LOCK_FILENAME = "cache.lock"
_BACKUP_SUFFIX = ".bak"
_META_SUFFIX = ".meta"


def normalize_path(path):
    """Return a stable absolute path string.

    On Windows, :func:`os.path.normcase` folds case so equivalent paths
    share one cache entry.
    """
    text = os.path.abspath(os.fspath(path))
    with contextlib.suppress(OSError):
        text = os.path.realpath(text)
    return os.path.normcase(os.path.normpath(text))


def get_cache_path(cache_dir):
    """Return the path of the main cache file inside *cache_dir*."""
    return Path(cache_dir) / _CACHE_FILENAME


def clear_cache(cache_dir):
    """Remove every file and directory inside *cache_dir*.

    The directory itself is kept. A missing directory is ignored.
    """
    directory = Path(cache_dir)
    if not directory.exists():
        return
    if not directory.is_dir():
        raise NotADirectoryError(
            f"Cache directory is not a directory: {directory}"
        )
    for child in list(directory.iterdir()):
        if child.is_dir() and not child.is_symlink():
            shutil.rmtree(child)
        else:
            child.unlink()


def sha256_file(path):
    """Return the SHA-256 hex digest of a file's bytes."""
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def fingerprint_file(path):
    """Return size, mtime, and content hash for *path*."""
    file_path = Path(path)
    stat = file_path.stat()
    return {
        "mtime_ns": stat.st_mtime_ns,
        "sha256": sha256_file(file_path),
        "size": stat.st_size,
    }


def fingerprint_matches(entry, fingerprint):
    """Return True when *entry* was produced from the same bytes."""
    if not isinstance(entry, dict) or not isinstance(fingerprint, dict):
        return False
    stored = entry.get("fingerprint")
    if not isinstance(stored, dict):
        return False
    digest = stored.get("sha256")
    return bool(digest) and digest == fingerprint.get("sha256")


def entry_structure_ok(entry):
    """Return True when *entry* has the fields needed to restore analysis."""
    if not isinstance(entry, dict):
        return False
    return (
        isinstance(entry.get("items"), dict)
        and isinstance(entry.get("used_names"), list)
        and isinstance(entry.get("dependencies"), list)
        and isinstance(entry.get("import_names"), list)
        and isinstance(entry.get("whitelist_deps"), dict)
    )


def runtime_signature(
    cache_settings=None, ignore_names=(), ignore_decorators=()
):
    """Return the values that must match for a cache entry to be reusable.

    The signature covers the cache format, the running interpreter, the
    installed vulture version, user ``cache_settings``, and the ignore
    patterns that change analysis results.
    """
    settings = {} if cache_settings is None else _json_ready(cache_settings)
    signature = {
        "cache_settings": settings,
        "cache_version": __version__,
        "ignore_decorators": _as_sorted_list(ignore_decorators),
        "ignore_names": _as_sorted_list(ignore_names),
        "python": sys.version,
        "vulture": _vulture_version(),
    }
    return _json_ready(signature)


def load_cache(
    cache_dir,
    cache_settings=None,
    ignore_names=(),
    ignore_decorators=(),
):
    """Load cached modules, or return an empty mapping if none are usable.

    A missing cache is silent. A corrupt or unreadable cache, including a
    SHA-256 mismatch in ``cache.json.meta``, prints a warning to stderr.
    A signature or ``cache_settings`` change discards the cache without a
    warning so the caller performs a full scan.
    """
    directory = Path(cache_dir)
    if not get_cache_path(directory).is_file():
        return {}
    signature = runtime_signature(
        cache_settings,
        ignore_names=ignore_names,
        ignore_decorators=ignore_decorators,
    )
    with _CacheLock(directory / _LOCK_FILENAME):
        status, payload = _read_payload(directory)
    if status == "missing":
        return {}
    if status == "corrupt":
        print(f"Warning: {CORRUPT_CACHE_MESSAGE}", file=sys.stderr)
        return {}
    if payload.get("signature") != signature:
        return {}
    return payload["modules"]


def save_cache(
    cache_dir,
    modules,
    cache_settings=None,
    ignore_names=(),
    ignore_decorators=(),
    drop_keys=(),
):
    """Atomically store *modules* and refresh the backup and checksum files.

    Concurrent saves are serialized with a lock and merged so one process
    cannot leave a torn cache behind. Entries whose files no longer exist,
    and keys in *drop_keys*, are removed.
    """
    directory = Path(cache_dir)
    directory.mkdir(parents=True, exist_ok=True)
    signature = runtime_signature(
        cache_settings,
        ignore_names=ignore_names,
        ignore_decorators=ignore_decorators,
    )
    with _CacheLock(directory / _LOCK_FILENAME):
        status, payload = _read_payload(directory)
        if (
            status == "ok"
            and payload.get("signature") == signature
            and isinstance(payload.get("modules"), dict)
        ):
            merged = dict(payload["modules"])
        else:
            merged = {}
        for key in drop_keys:
            merged.pop(key, None)
        merged.update(modules)
        for key in list(merged):
            if not _path_exists(key):
                del merged[key]
        _write_payload(directory, {"modules": merged, "signature": signature})


def analysis_roots(paths):
    """Return directory roots used to resolve import names for *paths*."""
    roots = []
    seen = set()
    for raw in paths:
        path = Path(raw).resolve()
        if path.is_file():
            root = path.parent
        elif path.is_dir():
            root = path
        else:
            continue
        key = normalize_path(root)
        if key in seen:
            continue
        seen.add(key)
        roots.append(root)
    return roots


def build_module_index(files, roots):
    """Map dotted module names to normalized file paths."""
    index = {}
    for filename in files:
        for root in roots:
            parts = _relative_module_parts(filename, root)
            if not parts:
                continue
            index.setdefault(".".join(parts), normalize_path(filename))
    return index


def imported_modules(filename, source, index, roots, known_files):
    """Return normalized paths of local modules imported by *source*."""
    try:
        tree = ast.parse(source)
    except (SyntaxError, ValueError):
        return []
    importer = Path(filename)
    package = _package_parts(importer, roots)
    deps = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                _consider(
                    deps,
                    alias.name.split("."),
                    index,
                    importer,
                    known_files,
                )
        elif isinstance(node, ast.ImportFrom):
            _consider_from_import(
                deps, node, package, index, importer, known_files
            )
    self_key = normalize_path(importer)
    deps.discard(self_key)
    return sorted(dep for dep in deps if dep in known_files)


def transitive_importers(changed, dependency_map, current):
    """Return files in *current* that import a changed file, directly or not.

    *changed* itself is not included. Cycles are ignored after the first
    visit.
    """
    current = set(current)
    reverse = {}
    for module, deps in dependency_map.items():
        if module not in current or not isinstance(deps, list):
            continue
        for dep in deps:
            reverse.setdefault(dep, set()).add(module)
    seen = set(changed)
    stack = list(changed)
    importers = set()
    while stack:
        item = stack.pop()
        for importer in reverse.get(item, ()):
            if importer in seen or importer not in current:
                continue
            seen.add(importer)
            importers.add(importer)
            stack.append(importer)
    return importers


def partition_modules(current_keys, loaded, fingerprints, dependency_map):
    """Split *current_keys* into paths to scan and paths to reuse.

    Source changes pull in transitive importers. Built-in whitelist
    changes invalidate only the modules that import the whitelisted name.
    """
    current = set(current_keys)
    source_changed = set()
    structurally_bad = set()
    for key in current:
        entry = loaded.get(key) if isinstance(loaded, dict) else None
        fingerprint = fingerprints.get(key)
        if not fingerprint_matches(entry, fingerprint):
            source_changed.add(key)
        elif not entry_structure_ok(entry):
            structurally_bad.add(key)
    to_scan = set(source_changed)
    to_scan.update(
        transitive_importers(source_changed, dependency_map, current)
    )
    to_scan.update(key for key in structurally_bad if key in current)
    for key in current - to_scan:
        if not whitelists_are_fresh(loaded.get(key)):
            to_scan.add(key)
    return to_scan, current - to_scan


def builtin_whitelist_path(import_name):
    """Return the built-in whitelist file for *import_name*, if any."""
    if not isinstance(import_name, str) or not import_name.isidentifier():
        return None
    path = (
        Path(__file__).resolve().parent
        / "whitelists"
        / f"{import_name}_whitelist.py"
    )
    if path.is_file():
        return path
    return None


def whitelist_digests(import_names, memo=None):
    """Map whitelist import names to the SHA-256 of the whitelist file."""
    if memo is None:
        memo = {}
    digests = {}
    for name in import_names:
        if name in memo:
            digest = memo[name]
        else:
            path = builtin_whitelist_path(name)
            if path is None:
                digest = None
            else:
                try:
                    digest = sha256_file(path)
                except OSError:
                    digest = None
            memo[name] = digest
        if digest is not None:
            digests[name] = digest
    return digests


def whitelists_are_fresh(entry):
    """Return True when stored whitelist checksums still match disk."""
    if not isinstance(entry, dict):
        return False
    names = entry.get("import_names")
    stored = entry.get("whitelist_deps")
    if not isinstance(names, list) or not isinstance(stored, dict):
        return False
    return whitelist_digests(names) == stored


class _CacheLock:
    """Exclusive lock so concurrent vulture processes cannot tear the cache."""

    def __init__(self, path):
        self.path = Path(path)
        self._handle = None

    def __enter__(self):
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self._handle = open(self.path, "a+b")
        if os.name == "nt":
            import msvcrt

            self._handle.seek(0)
            if not self._handle.read(1):
                self._handle.seek(0)
                self._handle.write(b"\0")
                self._handle.flush()
            self._handle.seek(0)
            msvcrt.locking(self._handle.fileno(), msvcrt.LK_LOCK, 1)
        else:
            import fcntl

            fcntl.flock(self._handle.fileno(), fcntl.LOCK_EX)
        return self

    def __exit__(self, _exc_type, _exc, _tb):
        try:
            if self._handle is not None:
                if os.name == "nt":
                    import msvcrt

                    self._handle.seek(0)
                    msvcrt.locking(self._handle.fileno(), msvcrt.LK_UNLCK, 1)
                else:
                    import fcntl

                    fcntl.flock(self._handle.fileno(), fcntl.LOCK_UN)
        finally:
            if self._handle is not None:
                self._handle.close()
                self._handle = None
        return False


def _vulture_version():
    try:
        return importlib.metadata.version("vulture")
    except importlib.metadata.PackageNotFoundError:
        from vulture.version import __version__ as version

        return version


def _json_ready(value):
    return json.loads(json.dumps(value, sort_keys=True))


def _as_sorted_list(values):
    items = list(values or [])
    try:
        return sorted(items)
    except TypeError:
        return items


def _meta_path(cache_dir):
    cache_path = get_cache_path(cache_dir)
    return cache_path.parent / f"{cache_path.name}{_META_SUFFIX}"


def _backup_path(cache_dir):
    cache_path = get_cache_path(cache_dir)
    return cache_path.parent / f"{cache_path.name}{_BACKUP_SUFFIX}"


def _dump(obj):
    text = json.dumps(obj, ensure_ascii=False, indent=2, sort_keys=True)
    return (text + "\n").encode("utf-8")


def _read_payload(cache_dir):
    """Return ``(status, payload)``.

    *status* is ``"missing"``, ``"corrupt"``, or ``"ok"``. Checksum
    mismatches are corrupt.
    """
    cache_path = get_cache_path(cache_dir)
    if not cache_path.is_file():
        return "missing", None
    try:
        raw = cache_path.read_bytes()
        meta = json.loads(_meta_path(cache_dir).read_text(encoding="utf-8"))
        digest = meta["sha256"]
        if not isinstance(digest, str):
            return "corrupt", None
        if hashlib.sha256(raw).hexdigest() != digest:
            return "corrupt", None
        payload = json.loads(raw.decode("utf-8"))
        if not isinstance(payload, dict):
            return "corrupt", None
        if not isinstance(payload.get("modules"), dict):
            return "corrupt", None
    except (
        OSError,
        UnicodeError,
        json.JSONDecodeError,
        KeyError,
        TypeError,
        ValueError,
    ):
        return "corrupt", None
    else:
        return "ok", payload


def _write_payload(cache_dir, payload):
    raw = _dump(payload)
    digest = hashlib.sha256(raw).hexdigest()
    meta_raw = _dump({"sha256": digest})
    # Backup first so a crash cannot lose the only copy of this payload,
    # including the very first save when no previous cache exists.
    _atomic_write_bytes(_backup_path(cache_dir), raw)
    _atomic_write_bytes(get_cache_path(cache_dir), raw)
    _atomic_write_bytes(_meta_path(cache_dir), meta_raw)


def _atomic_write_bytes(path, data):
    path = Path(path)
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
        tmp_path.unlink(missing_ok=True)
        raise
    _fsync_directory(path.parent)


def _fsync_directory(directory):
    fd = os.open(directory, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def _path_exists(key):
    try:
        return os.path.exists(key)
    except OSError:
        return True


def _relative_module_parts(path, root):
    path = Path(path).resolve()
    root = Path(root).resolve()
    try:
        relative = path.relative_to(root)
    except ValueError:
        return None
    parts = list(relative.parts)
    if not parts:
        return None
    filename = parts[-1]
    if filename == "__init__.py":
        parts = parts[:-1]
    elif filename.endswith(".py"):
        parts[-1] = filename[:-3]
    else:
        parts[-1] = Path(filename).stem
    return parts


def _package_parts(path, roots):
    path = Path(path).resolve()
    best = None
    best_len = -1
    for root in roots:
        root_path = Path(root).resolve()
        parts = _relative_module_parts(path, root_path)
        if parts is None:
            continue
        if len(root_path.parts) < best_len:
            continue
        best_len = len(root_path.parts)
        best = parts if path.name == "__init__.py" else parts[:-1]
    return list(best or [])


def _consider(deps, parts, index, importer, known_files):
    if not parts:
        return
    matched = False
    for length in range(1, len(parts) + 1):
        resolved = index.get(".".join(parts[:length]))
        if resolved:
            deps.add(resolved)
            matched = True
    if matched or len(parts) != 1:
        return
    name = parts[0]
    if not name.isidentifier():
        return
    parent = Path(importer).resolve().parent
    for candidate in (parent / f"{name}.py", parent / name / "__init__.py"):
        key = normalize_path(candidate)
        if key in known_files:
            deps.add(key)


def _consider_from_import(deps, node, package, index, importer, known_files):
    level = node.level or 0
    if level:
        climb = level - 1
        base = [] if climb > len(package) else package[: len(package) - climb]
        module_parts = list(base)
        if node.module:
            module_parts.extend(node.module.split("."))
        if module_parts:
            _consider(deps, module_parts, index, importer, known_files)
        for alias in node.names:
            if alias.name == "*":
                continue
            _consider(
                deps,
                [*module_parts, alias.name],
                index,
                importer,
                known_files,
            )
        return
    module_parts = node.module.split(".") if node.module else []
    if module_parts:
        _consider(deps, module_parts, index, importer, known_files)
    for alias in node.names:
        if alias.name == "*":
            continue
        parts = [*module_parts, alias.name] if module_parts else [alias.name]
        _consider(deps, parts, index, importer, known_files)
