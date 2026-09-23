"""
Persistent cache for incremental analysis.

The analysis results of a module (defined items, used names and imported
modules) only depend on its source code, its path, the analysis settings and
the runtime (cache format, Python and Vulture versions). The cache stores these
results under the normalized path of the module, so that subsequent runs only
re-analyze modules that changed, the modules that (transitively) import them,
and modules affected by changed whitelists.

The cache directory contains the cache itself (cache.json), a copy of it
(cache.json.bak) and a JSON file holding the SHA-256 checksum of cache.json
(cache.json.meta). All files are replaced atomically while holding a lock on
the cache directory, so concurrent Vulture processes never observe partially
written or mismatching files.
"""

import ast
import contextlib
import errno
import hashlib
import importlib.metadata
import json
import os
import pkgutil
import shutil
import sys
import tempfile
from collections import defaultdict
from pathlib import Path, PurePath

from vulture import utils
from vulture.version import __version__ as _source_version

if sys.platform == "win32":
    import msvcrt

    fcntl = None
else:
    import fcntl

__version__ = "1"

CACHE_FILENAME = "cache.json"
BACKUP_SUFFIX = ".bak"
META_SUFFIX = ".meta"
# Only used on Windows, where directories can't be locked.
LOCK_FILENAME = "cache.lock"


def normalize_path(path):
    """
    Return the absolute, normalized form of *path* that the cache uses as a
    key. Paths are case-insensitive on Windows.
    """
    try:
        path = Path(path).resolve()
    except (OSError, RuntimeError):
        path = Path(os.path.abspath(path))
    normalized = os.path.normcase(str(path))
    if sys.platform == "win32":
        normalized = normalized.lower()
    return normalized


def get_cache_path(cache_dir):
    return Path(cache_dir) / CACHE_FILENAME


def get_runtime_signature():
    """Cache entries are only valid for the runtime that created them."""
    try:
        vulture_version = importlib.metadata.version("vulture")
    except importlib.metadata.PackageNotFoundError:
        # Running from a source checkout that is not installed.
        vulture_version = _source_version
    return {
        "cache": __version__,
        "python": sys.version,
        "vulture": vulture_version,
    }


def clear_cache(cache_dir):
    """Remove all contents of *cache_dir*, but keep the directory itself."""
    cache_dir = Path(cache_dir)
    if not cache_dir.is_dir():
        return
    try:
        for child in cache_dir.iterdir():
            if child.is_dir() and not child.is_symlink():
                shutil.rmtree(child)
            else:
                child.unlink()
    except OSError as err:
        _warn(f"could not clear cache directory {cache_dir}: {err}")


def hash_bytes(data):
    return hashlib.sha256(data).hexdigest()


def hash_file(path):
    """Return the checksum of the file contents or None if unreadable."""
    try:
        return hash_bytes(Path(path).read_bytes())
    except OSError:
        return None


def get_whitelist_path(import_name):
    """Return the resource path of the bundled whitelist for a module."""
    return Path("whitelists") / (import_name + "_whitelist.py")


def get_import_targets(nodes, filename):
    """
    Return the modules imported by the ast.Import and ast.ImportFrom *nodes*
    of the module at *filename*.

    The result is a pair of sorted lists: the dotted names of absolutely
    imported modules and the path stems (paths without suffix) of relatively
    imported modules. Importing a module also imports its parent packages,
    and "from a import b" may import the submodule "a.b".
    """
    names = set()
    stems = set()
    for node in nodes:
        if isinstance(node, ast.Import):
            for alias in node.names:
                names.update(
                    ".".join(prefix)
                    for prefix in _get_prefixes(alias.name.split("."))
                )
            continue
        parts = node.module.split(".") if node.module else []
        imported = [alias.name for alias in node.names if alias.name != "*"]
        if node.level == 0:
            names.update(".".join(prefix) for prefix in _get_prefixes(parts))
            names.update(".".join([*parts, name]) for name in imported)
        else:
            package = Path(filename).parent
            for _ in range(node.level - 1):
                package = package.parent
            if not parts:
                stems.add(str(package))
            stems.update(
                str(package.joinpath(*prefix))
                for prefix in _get_prefixes(parts)
            )
            stems.update(
                str(package.joinpath(*parts, name)) for name in imported
            )
    return sorted(names), sorted(stems)


def _get_prefixes(parts):
    return [parts[:i] for i in range(1, len(parts) + 1)]


def _read_import_targets(path):
    try:
        tree = ast.parse(utils.read_file(path), filename=str(path))
    except (OSError, SyntaxError, ValueError, utils.VultureInputException):
        # Vulture reports the error when it analyzes the module.
        return [], []
    nodes = [
        node
        for node in ast.walk(tree)
        if isinstance(node, (ast.Import, ast.ImportFrom))
    ]
    return get_import_targets(nodes, path)


def _get_whitelist_digest(import_name):
    try:
        data = pkgutil.get_data(
            "vulture", str(get_whitelist_path(import_name))
        )
    except OSError:
        return None
    return None if data is None else hash_bytes(data)


def _is_whitelist(key):
    return "whitelist" in PurePath(key).name.lower()


def _get_stem(key):
    # Imports are matched case-insensitively, since false positives only
    # cause unnecessary re-analysis.
    path = PurePath(key.lower())
    return path.parent if path.name == "__init__.py" else path.with_suffix("")


class _ModuleIndex:
    """Find the modules that import statements refer to."""

    def __init__(self, keys):
        stems = {key: _get_stem(key) for key in keys}
        packages = {
            stem
            for key, stem in stems.items()
            if PurePath(key.lower()).name == "__init__.py"
        }
        self._keys_by_stem = defaultdict(set)
        self._modules_by_name = defaultdict(list)
        for key, stem in stems.items():
            # Number of components of the fully qualified module name, which
            # spans all enclosing packages known to the index.
            length = 1
            while (
                length < len(stem.parts)
                and PurePath(*stem.parts[:-length]) in packages
            ):
                length += 1
            self._keys_by_stem[str(stem)].add(key)
            self._modules_by_name[stem.name].append((stem.parts, length, key))

    def resolve(self, names, stems, importer):
        """
        Return the keys of the modules that the dotted *names* and path
        *stems* (see get_import_targets()) of the module *importer* may refer
        to.

        Since the import roots are unknown, a dotted name matches all modules
        whose path ends with it, as long as it contains the fully qualified
        module name or refers to a module next to the importer.
        """
        keys = set()
        for stem in stems:
            keys |= self._keys_by_stem.get(str(PurePath(stem.lower())), set())
        importer_dir = PurePath(importer.lower()).parent.parts
        for name in names:
            parts = tuple(name.lower().split("."))
            for stem_parts, length, key in self._modules_by_name.get(
                parts[-1], []
            ):
                if stem_parts[-len(parts) :] == parts and (
                    len(parts) >= length
                    or stem_parts[: -len(parts)] == importer_dir
                ):
                    keys.add(key)
        return keys


class Cache:
    """
    Store per-module analysis results in *cache_dir*.

    All results are discarded when the runtime signature or the *settings*
    differ from the ones that the cache was created with.

    Module entries have the following structure::

        {
            "path": <path of the analyzed module>,
            "sha256": <checksum of the module contents>,
            "defined": {<item type>: [[name, first_lineno, last_lineno,
                                       confidence, message], ...]},
            "used": [<used name>, ...],
            "imports": [<dotted name>, ...],
            "relative_imports": [<path stem>, ...],
        }

    An empty message stands for the default message of the item type.

    Entries without "defined" and "used" belong to modules that have not been
    analyzed successfully, e.g., because they are invalid or because the
    analysis was interrupted. Such modules are always analyzed, but they only
    affect the modules importing them if they changed. Their imports are
    omitted if they are unknown.
    """

    def __init__(self, cache_dir, settings=None):
        self.cache_dir = Path(cache_dir)
        self.path = get_cache_path(cache_dir)
        self.settings = json.loads(
            json.dumps(settings or {}, sort_keys=True, default=str)
        )
        self.modules = {}
        self.whitelists = {}
        self._signature = None
        self._loaded_checksum = None

    def load(self):
        """
        Load the cache. A missing or outdated cache is silently ignored. A
        corrupt or unreadable cache is reported and ignored.
        """
        self._signature = get_runtime_signature()
        self.modules = {}
        self.whitelists = {}
        self._loaded_checksum = None
        try:
            if not self.path.exists():
                return
            with _locked(self.cache_dir, exclusive=False):
                checksum, data = self._read()
        except (OSError, ValueError, RecursionError) as err:
            _warn(
                f"vulture cache is corrupted or unreadable ({self.path}: "
                f"{err}). Analyzing all files."
            )
            return
        if data is not None:
            self.modules = data["modules"]
            self.whitelists = data["whitelists"]
            self._loaded_checksum = checksum

    def get_stale_modules(self, current):
        """
        Return the keys of the modules that need to be analyzed.

        *current* maps the keys of all modules of this run to pairs of their
        path and checksum. Modules are stale if they are new or changed, if
        they (transitively) import changed, new or deleted modules, or if they
        are affected by a changed whitelist.
        """
        changed = set()
        unanalyzed = set()
        for key, (path, checksum) in current.items():
            entry = self.modules.get(key)
            if (
                entry is None
                or checksum is None
                or entry["sha256"] != checksum
                or entry["path"] != str(path)
            ):
                changed.add(key)
            elif "defined" not in entry:
                unanalyzed.add(key)
        deleted = {
            key
            for key, entry in self.modules.items()
            if key not in current and not os.path.exists(entry["path"])
        }
        unchanged = current.keys() - changed
        index = _ModuleIndex(current.keys() | deleted)

        # Bundled whitelists affect the modules importing the whitelisted
        # module, whitelist files affect the modules they import.
        changed_whitelists = {
            import_name
            for import_name, checksum in self.whitelists.items()
            if _get_whitelist_digest(import_name) != checksum
        }
        affected = {
            key
            for key in unchanged
            if changed_whitelists & self._get_imported_names(key)
        }
        for key in filter(_is_whitelist, changed | deleted):
            if key in self.modules:
                affected |= self._resolve_imports(index, key)
            if key in current:
                names, stems = _read_import_targets(current[key][0])
                affected |= index.resolve(names, stems, key)

        importers = defaultdict(set)
        unknown_imports = set()
        for key in unchanged:
            if "imports" in self.modules[key]:
                for imported in self._resolve_imports(index, key):
                    importers[imported].add(key)
            else:
                unknown_imports.add(key)
        outdated = changed | deleted
        if outdated:
            # Modules with unknown imports may import outdated modules.
            outdated |= unknown_imports
        queue = list(outdated)
        while queue:
            for importer in importers.get(queue.pop(), set()):
                if importer not in outdated:
                    outdated.add(importer)
                    queue.append(importer)

        return (outdated | affected | unanalyzed) & current.keys()

    def _resolve_imports(self, index, key):
        entry = self.modules[key]
        if "imports" not in entry:
            return set()
        return index.resolve(entry["imports"], entry["relative_imports"], key)

    def _get_imported_names(self, key):
        defined = self.modules[key].get("defined", {})
        return {row[0] for row in defined.get("import", [])}

    def _get_unanalyzed_entry(self, key, path, checksum):
        entry = {"path": str(path), "sha256": checksum}
        previous = self.modules.get(key)
        if (
            previous is not None
            and previous["sha256"] == checksum
            and "imports" in previous
        ):
            entry["imports"] = previous["imports"]
            entry["relative_imports"] = previous["relative_imports"]
        return entry

    def save(self, modules, whitelists, current):
        """
        Store the entries in *modules* and the checksums of the bundled
        *whitelists* (mapping import names to checksums).

        *current* maps the keys of all modules of this run to pairs of their
        path and checksum (see get_stale_modules()). Modules without entry
        are stored as unanalyzed. Entries of other modules are kept if their
        files still exist, including entries that concurrent Vulture
        processes stored in the meantime.
        """
        modules = dict(modules)
        for key, (path, checksum) in current.items():
            if key not in modules and checksum is not None:
                modules[key] = self._get_unanalyzed_entry(key, path, checksum)
        try:
            self.cache_dir.mkdir(parents=True, exist_ok=True)
            with _locked(self.cache_dir, exclusive=True):
                stored_modules, stored_whitelists = self._read_stored()
                merged = {
                    key: entry
                    for key, entry in stored_modules.items()
                    if key not in current and os.path.exists(entry["path"])
                }
                merged.update(modules)
                data = {
                    "modules": merged,
                    "settings": self.settings,
                    "signature": self._signature or get_runtime_signature(),
                    "whitelists": {**stored_whitelists, **whitelists},
                }
                self._write(data)
        except OSError as err:
            _warn(f"could not write vulture cache to {self.cache_dir}: {err}")

    def _read(self):
        """
        Return the checksum and the verified contents of the cache, or None
        instead of the contents if the cache is outdated.
        """
        contents = self.path.read_bytes()
        meta = json.loads(self._meta_path.read_bytes())
        checksum = hash_bytes(contents)
        if not isinstance(meta, dict) or meta.get("sha256") != checksum:
            raise ValueError("checksum mismatch")
        data = json.loads(contents)
        if not isinstance(data, dict):
            raise ValueError("invalid format")
        if (
            data.get("signature") != self._signature
            or data.get("settings") != self.settings
        ):
            return checksum, None
        _validate(data)
        return checksum, data

    def _read_stored(self):
        """Return the modules and whitelists currently stored on disk."""
        try:
            meta = json.loads(self._meta_path.read_bytes())
            if (
                isinstance(meta, dict)
                and self._loaded_checksum is not None
                and meta.get("sha256") == self._loaded_checksum
            ):
                return self.modules, self.whitelists
            _, data = self._read()
        except (OSError, ValueError, RecursionError):
            data = None
        if data is None:
            return {}, {}
        return data["modules"], data["whitelists"]

    def _write(self, data):
        contents = json.dumps(data, sort_keys=True, separators=(",", ":"))
        contents = contents.encode("utf-8")
        checksum = hash_bytes(contents)
        meta = json.dumps({"sha256": checksum}).encode("utf-8")
        _replace_files(
            [
                (
                    self.path.with_name(self.path.name + BACKUP_SUFFIX),
                    contents,
                ),
                (self.path, contents),
                (self._meta_path, meta),
            ]
        )
        self._loaded_checksum = checksum
        self.modules = data["modules"]
        self.whitelists = data["whitelists"]

    @property
    def _meta_path(self):
        return self.path.with_name(self.path.name + META_SUFFIX)


def _validate(data):
    """Raise ValueError if the cache contents are malformed."""
    modules = data.get("modules")
    whitelists = data.get("whitelists")
    if not isinstance(modules, dict) or not isinstance(whitelists, dict):
        raise ValueError("invalid format")
    if not all(isinstance(value, str) for value in whitelists.values()):
        raise ValueError("invalid whitelist entry")
    for entry in modules.values():
        if not _is_valid_entry(entry):
            raise ValueError("invalid module entry")


def _is_valid_entry(entry):
    def is_str_list(value):
        return isinstance(value, list) and all(
            isinstance(item, str) for item in value
        )

    def is_item_row(row):
        return (
            isinstance(row, list)
            and len(row) == 5
            and isinstance(row[0], str)
            and all(isinstance(number, int) for number in row[1:4])
            and isinstance(row[4], str)
        )

    if not (
        isinstance(entry, dict)
        and isinstance(entry.get("path"), str)
        and isinstance(entry.get("sha256"), str)
    ):
        return False
    has_imports = "imports" in entry or "relative_imports" in entry
    if has_imports and not (
        is_str_list(entry.get("imports"))
        and is_str_list(entry.get("relative_imports"))
    ):
        return False
    if "defined" not in entry and "used" not in entry:
        return True
    return (
        has_imports
        and isinstance(entry.get("defined"), dict)
        and all(
            isinstance(rows, list) and all(is_item_row(row) for row in rows)
            for rows in entry["defined"].values()
        )
        and is_str_list(entry.get("used"))
    )


def _replace_files(files):
    """
    Atomically replace each path in *files* (pairs of paths and contents).

    All contents are written to temporary files first to keep the time span
    in which the files don't match each other as short as possible.
    """
    temp_paths = []
    try:
        for path, contents in files:
            fd, temp_path = tempfile.mkstemp(
                dir=path.parent, prefix=f".{path.name}.", suffix=".tmp"
            )
            temp_paths.append(temp_path)
            with os.fdopen(fd, "wb") as f:
                f.write(contents)
                f.flush()
                os.fsync(f.fileno())
        for (path, _), temp_path in zip(files, temp_paths):
            os.replace(temp_path, path)
    finally:
        for temp_path in temp_paths:
            with contextlib.suppress(FileNotFoundError):
                os.remove(temp_path)


@contextlib.contextmanager
def _locked(directory, exclusive):
    """
    Hold an advisory lock on *directory*. Locking is best effort: if the
    lock can't be acquired (e.g., on some network file systems), proceed
    without it.
    """
    fd = _acquire_lock(directory, exclusive)
    try:
        yield
    finally:
        if fd is not None:
            _release_lock(fd)


def _acquire_lock(directory, exclusive):
    try:
        if fcntl is None:
            fd = os.open(
                os.path.join(directory, LOCK_FILENAME), os.O_RDWR | os.O_CREAT
            )
        else:
            fd = os.open(directory, os.O_RDONLY)
    except OSError:
        return None
    try:
        if fcntl is None:
            # Windows only supports exclusive locks. LK_LOCK gives up after
            # ten seconds, so keep trying.
            while True:
                try:
                    msvcrt.locking(fd, msvcrt.LK_LOCK, 1)
                    break
                except OSError as err:
                    if err.errno != errno.EDEADLOCK:
                        raise
        else:
            fcntl.flock(fd, fcntl.LOCK_EX if exclusive else fcntl.LOCK_SH)
    except OSError:
        os.close(fd)
        return None
    return fd


def _release_lock(fd):
    try:
        if fcntl is None:
            os.lseek(fd, 0, os.SEEK_SET)
            msvcrt.locking(fd, msvcrt.LK_UNLCK, 1)
    finally:
        # Closing the file descriptor releases flock() locks.
        os.close(fd)


def _warn(message):
    message = f"Warning: {message}"
    try:
        print(message, file=sys.stderr)
    except UnicodeEncodeError:
        print(message.encode(), file=sys.stderr)
