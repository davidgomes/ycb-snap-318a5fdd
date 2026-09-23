"""Incremental analysis cache for Vulture.

The on-disk cache lives in a directory (``.vulture-cache/`` by default) and
stores three files:

* ``cache.json`` — analysis results for previously scanned modules
* ``cache.json.bak`` — backup written on every successful save
* ``cache.json.meta`` — ``{"sha256": "<hex>"}`` checksum of ``cache.json``

Only files whose contents changed, and files that transitively import those
files, are re-analyzed on the next run. A changed runtime signature (cache
format version, Python version, vulture version, or ``cache_settings``)
discards every entry and forces a full scan.
"""

import ast
import hashlib
import importlib
import importlib.metadata
import importlib.util
import json
import os
import pkgutil
import shutil
import sys
from contextlib import contextmanager, suppress
from pathlib import Path

__version__ = "1"

_CACHE_FILENAME = "cache.json"
_META_FILENAME = "cache.json.meta"
_BACKUP_FILENAME = "cache.json.bak"
_LOCK_FILENAME = ".lock"

CORRUPT_CACHE_MESSAGE = "cache is corrupted or unreadable"


def normalize_path(path):
    """Return a stable cache key for *path*.

    The path is made absolute and symlinks are resolved. On Windows the key
    is case-folded so ``Foo.py`` and ``foo.py`` share one entry.
    """
    try:
        resolved = Path(path).resolve()
    except OSError:
        resolved = Path(os.path.abspath(os.fspath(path)))
    text = os.fspath(resolved)
    # Case-fold on Windows so differently cased paths share one cache entry.
    if sys.platform == "win32" or os.name == "nt":
        text = os.path.normcase(text).lower()
    return text


def get_cache_path(cache_dir):
    """Return the path of the main cache file inside *cache_dir*."""
    return Path(cache_dir) / _CACHE_FILENAME


def get_meta_path(cache_dir):
    return Path(cache_dir) / _META_FILENAME


def get_backup_path(cache_dir):
    return Path(cache_dir) / _BACKUP_FILENAME


def _package_version():
    """Return the vulture distribution version.

    ``importlib.metadata.version`` is the source of truth for an installed
    copy. A source checkout that has not been installed falls back to
    ``vulture/version.py`` without importing the package ``__init__``.
    """
    try:
        return importlib.metadata.version("vulture")
    except importlib.metadata.PackageNotFoundError:
        version_path = Path(__file__).resolve().parent / "version.py"
        spec = importlib.util.spec_from_file_location(
            "_vulture_version_fallback", version_path
        )
        if spec is None or spec.loader is None:
            return "0"
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module.__version__


def _canonical_settings(cache_settings):
    if not cache_settings:
        return {}
    return json.loads(json.dumps(cache_settings, sort_keys=True))


def build_signature(cache_settings, ignore_names, ignore_decorators):
    """Signature that invalidates the whole cache when it changes."""
    return {
        "cache_version": __version__,
        "python_version": sys.version,
        "vulture_version": _package_version(),
        "cache_settings": _canonical_settings(cache_settings),
        "ignore_names": list(ignore_names or []),
        "ignore_decorators": list(ignore_decorators or []),
    }


def sha256_bytes(data):
    return hashlib.sha256(data).hexdigest()


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _optional_sha256(path):
    try:
        return sha256_file(path)
    except OSError:
        return None


def _warn_corrupt():
    print(f"Warning: {CORRUPT_CACHE_MESSAGE}", file=sys.stderr)


def _path_components(path):
    parts = list(path.parts)
    anchor = path.anchor
    if parts and (parts[0] == anchor or parts[0] in (os.sep, "/", "\\")):
        parts = parts[1:]
    return parts


def module_names(path):
    """Module names a file might be imported as.

    Includes the filename stem and every trailing dotted suffix whose parts
    are identifiers (``pkg/mod.py`` → ``mod`` and ``pkg.mod``).
    """
    path = Path(path)
    try:
        path = path.resolve()
    except OSError:
        path = Path(os.path.abspath(os.fspath(path)))
    if path.name == "__init__.py":
        components = _path_components(path.parent)
    else:
        components = _path_components(path.with_suffix(""))
    names = set()
    for index in range(len(components)):
        parts = components[index:]
        if parts and all(part.isidentifier() for part in parts):
            names.add(".".join(parts))
    return names


def containing_package(path):
    """Package name used to resolve relative imports in *path*."""
    path = Path(path)
    try:
        path = path.resolve()
    except OSError:
        path = Path(os.path.abspath(os.fspath(path)))
    directory = path.parent
    parts = []
    while (directory / "__init__.py").is_file():
        parts.append(directory.name)
        directory = directory.parent
    return ".".join(reversed(parts))


def resolve_relative(module, level, package):
    """Resolve a possibly-relative import to an absolute module name."""
    if not level:
        return module or ""
    if not package:
        return ""
    bits = package.rsplit(".", level - 1)
    if len(bits) < level:
        return ""
    base = bits[0]
    if module:
        return f"{base}.{module}" if base else module
    return base


def extract_imports(source, filename):
    """Return absolute module names imported by *source*.

    ``from pkg.mod import name`` records both ``pkg.mod`` and ``pkg.mod.name``
    so a submodule import invalidates the parent module's importers.
    """
    try:
        tree = ast.parse(source)
    except (SyntaxError, ValueError):
        return []
    package = containing_package(filename)
    found = []
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            found.extend(alias.name for alias in node.names if alias.name)
        elif isinstance(node, ast.ImportFrom):
            if node.level == 0 and node.module == "__future__":
                continue
            base = resolve_relative(node.module, node.level or 0, package)
            if base:
                found.append(base)
            for alias in node.names:
                if not alias.name or alias.name == "*":
                    continue
                if base:
                    found.append(f"{base}.{alias.name}")
                else:
                    found.append(alias.name)
    return sorted(set(found))


def build_module_index(paths):
    """Map module name → set of normalized paths."""
    index = {}
    for path in paths:
        norm = normalize_path(path)
        for name in module_names(path):
            index.setdefault(name, set()).add(norm)
    return index


def files_imported_by(imports, index):
    """Normalized paths loaded by *imports* (the module and its parents)."""
    hit = set()
    for name in imports:
        if not name:
            continue
        parts = name.split(".")
        for size in range(1, len(parts) + 1):
            hit.update(index.get(".".join(parts[:size]), ()))
    return hit


def importer_graph(modules, index):
    """Map a normalized path to the normalized paths that import it."""
    reverse = {}
    for norm, entry in modules.items():
        imported = files_imported_by(entry.get("imports") or [], index)
        for target in imported:
            if target != norm:
                reverse.setdefault(target, set()).add(norm)
    return reverse


def transitive_importers(seeds, reverse):
    """*seeds* plus every module that reaches them through imports."""
    seen = set()
    stack = list(seeds)
    while stack:
        current = stack.pop()
        if current in seen:
            continue
        seen.add(current)
        stack.extend(reverse.get(current, ()))
    return seen


def _whitelist_dir():
    return Path(__file__).resolve().parent / "whitelists"


def whitelist_hashes():
    """Content hashes of files under ``vulture/whitelists``.

    ``*_whitelist.py`` files are keyed by the import name they satisfy
    (``sys_whitelist.py`` → ``sys``). Other modules, such as
    ``whitelist_utils.py``, keep their filename as the key.
    """
    root = _whitelist_dir()
    hashes = {}
    if not root.is_dir():
        return hashes
    for path in sorted(root.glob("*.py")):
        name = path.name
        suffix = "_whitelist.py"
        if name.endswith(suffix):
            name = name[: -len(suffix)]
        hashes[name] = sha256_file(path)
    return hashes


def changed_whitelist_names(old_hashes, new_hashes):
    """Import names whose whitelist (or shared whitelist helper) changed."""
    keys = set(old_hashes) | set(new_hashes)
    changed = {
        key for key in keys if old_hashes.get(key) != new_hashes.get(key)
    }
    support_changed = any(key.endswith(".py") for key in changed)
    names = {key for key in changed if not key.endswith(".py")}
    if support_changed:
        names |= {key for key in keys if not key.endswith(".py")}
    return names


def read_whitelist(import_name):
    """Return whitelist source bytes, or ``None`` when there is no file."""
    path = _whitelist_dir() / f"{import_name}_whitelist.py"
    if path.is_file():
        return path.read_bytes()
    rel = f"whitelists/{import_name}_whitelist.py"
    try:
        data = pkgutil.get_data("vulture", rel)
    except OSError:
        return None
    return data


@contextmanager
def exclusive_lock(cache_dir):
    """Exclusive lock so concurrent Vulture processes cannot tear the cache."""
    directory = Path(cache_dir)
    directory.mkdir(parents=True, exist_ok=True)
    lock_path = directory / _LOCK_FILENAME
    handle = open(lock_path, "a+b")
    try:
        if sys.platform == "win32":
            import msvcrt

            handle.seek(0, os.SEEK_END)
            if handle.tell() == 0:
                handle.write(b"\0")
                handle.flush()
            handle.seek(0)
            msvcrt.locking(handle.fileno(), msvcrt.LK_LOCK, 1)
        else:
            import fcntl

            fcntl.flock(handle.fileno(), fcntl.LOCK_EX)
        yield
    finally:
        try:
            if sys.platform == "win32":
                import msvcrt

                handle.seek(0)
                msvcrt.locking(handle.fileno(), msvcrt.LK_UNLCK, 1)
            else:
                import fcntl

                fcntl.flock(handle.fileno(), fcntl.LOCK_UN)
        finally:
            handle.close()


def _remove_path(path):
    if path.is_dir() and not path.is_symlink():
        shutil.rmtree(path)
    else:
        path.unlink()


def clear_cache(cache_dir):
    """Remove all contents of *cache_dir*. The directory itself is kept."""
    directory = Path(cache_dir)
    if not directory.exists():
        return
    if not directory.is_dir():
        directory.unlink()
        return
    with exclusive_lock(directory):
        for child in list(directory.iterdir()):
            if child.name == _LOCK_FILENAME:
                continue
            _remove_path(child)
    lock_path = directory / _LOCK_FILENAME
    lock_path.unlink(missing_ok=True)


def _atomic_write(path, data):
    """Write *data* to *path* via a temporary file and ``os.replace``."""
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f".{path.name}.{os.getpid()}.tmp")
    try:
        with open(temporary, "wb") as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    except BaseException:
        if temporary.exists():
            with suppress(OSError):
                temporary.unlink()
        raise


class AnalysisCache:
    """Load, update, and store per-module analysis results."""

    def __init__(
        self, cache_dir, cache_settings, ignore_names, ignore_decorators
    ):
        self.cache_dir = Path(cache_dir)
        self.cache_path = get_cache_path(self.cache_dir)
        self.meta_path = get_meta_path(self.cache_dir)
        self.backup_path = get_backup_path(self.cache_dir)
        self.cache_settings = cache_settings
        self.ignore_names = ignore_names
        self.ignore_decorators = ignore_decorators
        self.data = self.empty_data()
        self.ready = False

    def signature(self):
        return build_signature(
            self.cache_settings, self.ignore_names, self.ignore_decorators
        )

    def empty_data(self):
        return {
            "signature": self.signature(),
            "modules": {},
            "whitelist_hashes": {},
            "whitelist_results": {},
        }

    def load(self):
        """Load the cache.

        A missing file starts a silent full scan. A checksum mismatch, invalid
        JSON, or unreadable file warns and starts a full scan. A signature
        mismatch discards cached modules without a warning.
        """
        self.ready = False
        self.data = self.empty_data()
        if not self.cache_path.exists():
            self.ready = True
            return
        try:
            raw = self.cache_path.read_bytes()
            meta = json.loads(self.meta_path.read_text(encoding="utf-8"))
            expected = meta["sha256"]
            if not isinstance(expected, str) or expected != sha256_bytes(raw):
                raise ValueError(CORRUPT_CACHE_MESSAGE)
            parsed = json.loads(raw.decode("utf-8"))
            if (
                not isinstance(parsed, dict)
                or not isinstance(parsed.get("modules"), dict)
                or not isinstance(parsed.get("signature"), dict)
            ):
                raise ValueError(CORRUPT_CACHE_MESSAGE)
        except Exception:
            _warn_corrupt()
            self.data = self.empty_data()
            self.ready = True
            return

        if parsed["signature"] != self.signature():
            self.data = self.empty_data()
            self.ready = True
            return

        parsed.setdefault("whitelist_hashes", {})
        parsed.setdefault("whitelist_results", {})
        self.data = parsed
        self.ready = True

    def partition(self, paths):
        """Split *paths* into ``(to_scan, to_reuse)``.

        Entries for deleted files are removed. Content changes and whitelist
        changes select what must be analyzed again. Files that import a
        changed file, directly or transitively, are scanned too.
        """
        modules = self.data.setdefault("modules", {})
        ordered = []
        seen = set()
        for path in paths:
            norm = normalize_path(path)
            if norm in seen:
                continue
            seen.add(norm)
            ordered.append((Path(path), norm))
        current = {norm for _, norm in ordered}

        deleted = {key for key in modules if not Path(key).is_file()}

        hashes = {}
        for path, norm in ordered:
            hashes[norm] = _optional_sha256(path)
        for key in modules:
            if key in hashes or key in deleted:
                continue
            hashes[key] = _optional_sha256(key)

        content_changed = set(deleted)
        for key, entry in modules.items():
            if key in deleted:
                continue
            digest = hashes.get(key)
            if digest is None or digest != entry.get("sha256"):
                content_changed.add(key)
        for norm in current:
            if norm not in modules:
                content_changed.add(norm)

        new_whitelist_hashes = whitelist_hashes()
        whitelist_names = changed_whitelist_names(
            self.data.get("whitelist_hashes") or {}, new_whitelist_hashes
        )
        whitelist_affected = set()
        if whitelist_names:
            for key, entry in modules.items():
                imported = set(entry.get("import_names") or [])
                if imported & whitelist_names:
                    whitelist_affected.add(key)

        # Build the import graph before dropping entries so importers of a
        # deleted or changed file can still be found.
        index_paths = [Path(key) for key in modules]
        index_paths.extend(path for path, _norm in ordered)
        index = build_module_index(index_paths)
        reverse = importer_graph(modules, index)
        affected = transitive_importers(content_changed, reverse)

        invalid = {
            key
            for key, entry in modules.items()
            if entry.get("invalid") and key in current
        }

        to_scan_norms = (affected | whitelist_affected | invalid) & current
        for key in deleted:
            modules.pop(key, None)
        # Drop analyses we are about to redo so an interrupted scan cannot
        # reload them as if they were still fresh.
        for norm in to_scan_norms:
            modules.pop(norm, None)

        to_scan = []
        to_reuse = []
        for path, norm in ordered:
            if norm in to_scan_norms:
                to_scan.append(path)
            else:
                to_reuse.append(path)
        self._pending_whitelist_hashes = new_whitelist_hashes
        return to_scan, to_reuse

    def get(self, path):
        return self.data["modules"][normalize_path(path)]

    def record(self, path, entry):
        self.data["modules"][normalize_path(path)] = entry

    def finish_whitelists(self):
        if hasattr(self, "_pending_whitelist_hashes"):
            self.data["whitelist_hashes"] = self._pending_whitelist_hashes

    def save(self):
        """Atomically write the cache, its backup, and the checksum file."""
        if not self.ready:
            return
        self.data["signature"] = self.signature()
        payload = (
            json.dumps(self.data, indent=2, sort_keys=True) + "\n"
        ).encode("utf-8")
        digest = sha256_bytes(payload)
        meta_payload = (
            json.dumps({"sha256": digest}, indent=2, sort_keys=True) + "\n"
        ).encode("utf-8")
        self.cache_dir.mkdir(parents=True, exist_ok=True)
        # Backup the previous cache when there is one; otherwise back up the
        # payload we are about to publish so the first save still writes .bak.
        if self.cache_path.is_file():
            previous = self.cache_path.read_bytes()
            _atomic_write(self.backup_path, previous)
        else:
            _atomic_write(self.backup_path, payload)
        _atomic_write(self.cache_path, payload)
        _atomic_write(self.meta_path, meta_payload)
