#
# SPDX-License-Identifier: Apache-2.0
"""Incremental analysis cache for Bandit.

This module is intentionally free of imports from bandit.cli and
bandit.core.manager so circular imports cannot loop at import time.
"""

import ast
import hashlib
import json
import logging
import os
import re
import time

from bandit.core import issue as b_issue

LOG = logging.getLogger(__name__)

CACHE_FORMAT_VERSION = 1
CACHE_FILENAME = "bandit-cache.json"
DEFAULT_CACHE_DIR = ".bandit_cache"

INVALIDATION_REASONS = (
    "file_changed",
    "config_changed",
    "expired",
    "not_cached",
)


def default_cache_info():
    """Return an empty cache_info structure."""
    return {
        "total_files": 0,
        "cache_hits": 0,
        "cache_misses": 0,
        "invalidation_counts": {
            "file_changed": 0,
            "config_changed": 0,
            "expired": 0,
            "not_cached": 0,
        },
        "invalidation_details": [],
    }


def format_verbose_cache_summary(cache_info):
    """Build the verbose cache summary text."""
    hits = cache_info.get("cache_hits", 0)
    misses = cache_info.get("cache_misses", 0)
    lines = [f"Files cached: {hits}, Files scanned: {misses}"]
    counts = cache_info.get("invalidation_counts") or {}
    reason_bits = []
    for reason in INVALIDATION_REASONS:
        reason_bits.append(f"{reason}={counts.get(reason, 0)}")
    lines.append("Invalidation reasons: " + ", ".join(reason_bits))
    for detail in cache_info.get("invalidation_details") or []:
        path = detail.get("path", "")
        reason = detail.get("reason", "")
        if path and reason:
            lines.append(f"  {path}: {reason}")
    return "\n".join(lines)


def parse_size_limit(value):
    """Parse a cache size limit into bytes.

    Accepts integers (bytes) or strings with optional K/KB/M/MB/G/GB suffix.
    """
    if value is None or value == "":
        return None
    if isinstance(value, bool):
        return None
    if isinstance(value, (int, float)):
        return int(value)
    text = str(value).strip().upper()
    if not text:
        return None
    match = re.match(r"^(\d+)\s*([KMGTB]*)$", text)
    if not match:
        try:
            return int(text)
        except ValueError:
            return None
    number = int(match.group(1))
    suffix = match.group(2)
    multipliers = {
        "": 1,
        "B": 1,
        "K": 1024,
        "KB": 1024,
        "M": 1024 * 1024,
        "MB": 1024 * 1024,
        "G": 1024 * 1024 * 1024,
        "GB": 1024 * 1024 * 1024,
    }
    return number * multipliers.get(suffix, 1)


def as_bool(value, default=False):
    """Coerce config/CLI values to bool."""
    if value is None:
        return default
    if isinstance(value, bool):
        return value
    if isinstance(value, str):
        return value.strip().lower() in ("1", "true", "yes", "on")
    return bool(value)


def normalize_path(path):
    """Return a stable absolute path for cache keys."""
    if not path or path == "-":
        return path
    return os.path.normpath(os.path.abspath(path))


def hash_bytes(data):
    """Return a hex SHA-256 digest of *data*."""
    if isinstance(data, str):
        data = data.encode("utf-8")
    return hashlib.sha256(data).hexdigest()


def hash_file(path):
    """Return a hex SHA-256 digest of a file's contents."""
    with open(path, "rb") as handle:
        return hash_bytes(handle.read())


def _as_sorted_list(value):
    if value is None:
        return []
    if isinstance(value, str):
        items = [item.strip() for item in value.split(",") if item.strip()]
        return sorted(items)
    return sorted(str(item) for item in value)


def _normalize_profile(profile):
    if not profile:
        return {"include": [], "exclude": []}
    include = profile.get("include") or []
    exclude = profile.get("exclude") or []
    return {
        "include": _as_sorted_list(include),
        "exclude": _as_sorted_list(exclude),
    }


def build_config_key(options):
    """Build a cache key from analysis options and profile data."""
    payload = {
        "tests": _as_sorted_list(options.get("tests")),
        "skips": _as_sorted_list(options.get("skips")),
        "severity": options.get("severity"),
        "confidence": options.get("confidence"),
        "profile_name": options.get("profile_name") or "",
        "profile_content": _normalize_profile(options.get("profile")),
    }
    raw = json.dumps(payload, sort_keys=True, default=str)
    return hash_bytes(raw)


def extract_imports(source, filename="<unknown>"):
    """Extract imported module names from Python source.

    Syntax errors yield an empty list rather than raising.
    """
    if isinstance(source, bytes):
        try:
            source = source.decode("utf-8")
        except UnicodeDecodeError:
            source = source.decode("utf-8", errors="replace")
    try:
        tree = ast.parse(source, filename=filename)
    except (SyntaxError, ValueError, TypeError):
        return []

    modules = []
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                if alias.name:
                    modules.append(alias.name)
        elif isinstance(node, ast.ImportFrom):
            module = node.module
            if module:
                modules.append(module)
            elif node.level:
                # relative import of the form ``from . import x``
                for alias in node.names:
                    if alias.name:
                        modules.append(alias.name)
    return modules


def resolve_import(module_name, from_file):
    """Resolve a module name to a project file path if possible."""
    if not module_name or not from_file or from_file == "-":
        return None
    parts = [part for part in module_name.split(".") if part]
    if not parts:
        return None

    search_dirs = []
    start_dir = os.path.dirname(os.path.abspath(from_file))
    if start_dir:
        search_dirs.append(start_dir)
        parent = os.path.dirname(start_dir)
        if parent and parent != start_dir:
            search_dirs.append(parent)
    cwd = os.getcwd()
    if cwd not in search_dirs:
        search_dirs.append(cwd)

    for search in search_dirs:
        candidate = os.path.join(search, *parts) + ".py"
        if os.path.isfile(candidate):
            return normalize_path(candidate)
        package = os.path.join(search, *parts, "__init__.py")
        if os.path.isfile(package):
            return normalize_path(package)
    return None


def collect_dependencies(fname, source, visited=None):
    """Collect local import dependencies without looping on cycles.

    *visited* is a set of normalized paths already walked. Circular
    imports are skipped rather than followed again.
    """
    if visited is None:
        visited = set()
    if not fname or fname == "-":
        return []

    norm = normalize_path(fname)
    if norm in visited:
        return []
    visited.add(norm)

    dependencies = []
    for module_name in extract_imports(source, fname):
        dep_path = resolve_import(module_name, fname)
        if not dep_path or not os.path.isfile(dep_path):
            continue
        if dep_path in visited:
            continue
        try:
            dep_hash = hash_file(dep_path)
        except OSError:
            continue
        dependencies.append({"path": dep_path, "hash": dep_hash})
        try:
            with open(dep_path, "rb") as handle:
                dep_source = handle.read()
        except OSError:
            continue
        dependencies.extend(
            collect_dependencies(dep_path, dep_source, visited)
        )
    return dependencies


def _entry_checksum(entry):
    payload = {key: value for key, value in entry.items() if key != "checksum"}
    raw = json.dumps(
        payload, sort_keys=True, default=str, separators=(",", ":")
    )
    return hash_bytes(raw)


def _validate_entry(entry):
    """Return a copy of *entry* if it is intact, else None."""
    if not isinstance(entry, dict):
        return None
    required = (
        "path",
        "file_hash",
        "config_key",
        "created_at",
        "results",
        "checksum",
    )
    for field in required:
        if field not in entry:
            return None
    expected = _entry_checksum(entry)
    if entry.get("checksum") != expected:
        return None
    return entry


def serialize_issue(issue_obj):
    """Serialize an Issue to a JSON-safe dict."""
    if hasattr(issue_obj, "as_dict"):
        return issue_obj.as_dict(with_code=True)
    if isinstance(issue_obj, dict):
        return issue_obj
    raise TypeError("Cannot serialize issue of type %s" % type(issue_obj))


def deserialize_issue(data):
    """Restore an Issue from a cached dict."""
    return b_issue.issue_from_dict(data)


class IncrementalCache:
    """On-disk incremental analysis cache with integrity checks."""

    def __init__(
        self,
        cache_dir=None,
        expiry_days=None,
        size_limit=None,
        config_key="",
        create=False,
    ):
        self.cache_dir = os.path.abspath(cache_dir or DEFAULT_CACHE_DIR)
        self.expiry_days = expiry_days
        self.size_limit = parse_size_limit(size_limit)
        self.config_key = config_key or ""
        self.entries = {}
        if create:
            self.ensure_dir()
        self.load()

    @property
    def cache_file(self):
        return os.path.join(self.cache_dir, CACHE_FILENAME)

    def ensure_dir(self):
        os.makedirs(self.cache_dir, exist_ok=True)

    def load(self):
        """Load cache entries, discarding anything corrupted."""
        self.entries = {}
        path = self.cache_file
        if not os.path.isfile(path):
            return
        try:
            with open(path, encoding="utf-8") as handle:
                payload = json.load(handle)
        except (OSError, ValueError, TypeError, json.JSONDecodeError):
            LOG.warning("Discarding corrupted cache file: %s", path)
            return
        if not isinstance(payload, dict):
            return
        if payload.get("format_version") != CACHE_FORMAT_VERSION:
            LOG.warning("Discarding incompatible cache format: %s", path)
            return
        raw_entries = payload.get("entries") or []
        if isinstance(raw_entries, dict):
            raw_entries = list(raw_entries.values())
        for item in raw_entries:
            entry = _validate_entry(item)
            if entry is None:
                LOG.debug("Discarding corrupted cache entry")
                continue
            key = normalize_path(entry.get("path"))
            if key:
                self.entries[key] = entry

    def save(self):
        """Persist cache entries, honoring the size limit."""
        self.ensure_dir()
        self._enforce_size_limit()
        payload = {
            "format_version": CACHE_FORMAT_VERSION,
            "entries": list(self.entries.values()),
        }
        path = self.cache_file
        tmp_path = path + ".tmp"
        with open(tmp_path, "w", encoding="utf-8") as handle:
            json.dump(payload, handle, sort_keys=True, indent=2)
        os.replace(tmp_path, path)

    def _estimate_size(self, entries):
        payload = {
            "format_version": CACHE_FORMAT_VERSION,
            "entries": list(entries),
        }
        return len(
            json.dumps(payload, sort_keys=True, default=str).encode("utf-8")
        )

    def _enforce_size_limit(self):
        if not self.size_limit:
            return
        items = sorted(
            self.entries.values(),
            key=lambda item: item.get("created_at", 0),
            reverse=True,
        )
        kept = []
        for item in items:
            trial = kept + [item]
            if self._estimate_size(trial) <= self.size_limit or not kept:
                kept.append(item)
        self.entries = {
            normalize_path(item.get("path")): item
            for item in kept
            if item.get("path")
        }

    def _is_expired(self, entry):
        if self.expiry_days is None:
            return False
        try:
            expiry_days = float(self.expiry_days)
        except (TypeError, ValueError):
            return False
        if expiry_days == 0:
            return True
        created = entry.get("created_at") or 0
        age_days = (time.time() - created) / 86400.0
        return age_days > expiry_days

    def _dependencies_changed(self, entry):
        for dep in entry.get("dependencies") or []:
            if not isinstance(dep, dict):
                return True
            dep_path = dep.get("path")
            dep_hash = dep.get("hash")
            if not dep_path or not dep_hash:
                return True
            if not os.path.isfile(dep_path):
                return True
            try:
                current = hash_file(dep_path)
            except OSError:
                return True
            if current != dep_hash:
                return True
        return False

    def lookup(self, fname, file_hash=None, force_rescan=False):
        """Look up a cached result.

        Returns ``(entry_or_None, reason)``. *reason* is None on a hit.
        """
        if not fname or fname == "-" or force_rescan:
            return None, "not_cached"
        key = normalize_path(fname)
        entry = self.entries.get(key)
        if entry is None:
            return None, "not_cached"
        if self._is_expired(entry):
            return None, "expired"
        if entry.get("config_key") != self.config_key:
            return None, "config_changed"
        if file_hash is None:
            try:
                file_hash = hash_file(fname)
            except OSError:
                return None, "file_changed"
        if entry.get("file_hash") != file_hash:
            return None, "file_changed"
        if self._dependencies_changed(entry):
            return None, "file_changed"
        return entry, None

    def store(
        self,
        fname,
        file_hash,
        results,
        metrics=None,
        score=None,
        dependencies=None,
        original_path=None,
    ):
        """Store a scan result for *fname*."""
        if not fname or fname == "-":
            return
        key = normalize_path(fname)
        entry = {
            "path": original_path or fname,
            "path_key": key,
            "file_hash": file_hash,
            "config_key": self.config_key,
            "created_at": time.time(),
            "results": results,
            "metrics": metrics or {},
            "score": score or {},
            "dependencies": dependencies or [],
        }
        entry["checksum"] = _entry_checksum(entry)
        self.entries[key] = entry

    def clear(self):
        """Remove the cache directory contents.

        Missing directories are a no-op.
        """
        self.entries = {}
        if not os.path.isdir(self.cache_dir):
            return
        if os.path.isfile(self.cache_file):
            try:
                os.remove(self.cache_file)
            except OSError:
                pass
        # leave the directory in place if it exists

    def list_files(self):
        """Return cached file paths (one per entry)."""
        paths = []
        for entry in self.entries.values():
            path = entry.get("path") or entry.get("path_key")
            if path:
                paths.append(path)
        return sorted(set(paths))

    def prune(self, days):
        """Remove entries older than *days*. ``0`` removes all entries."""
        try:
            days = float(days)
        except (TypeError, ValueError):
            return 0
        if days == 0:
            removed = len(self.entries)
            self.entries = {}
            if self.entries_existed_or_dir():
                self.save()
            return removed
        cutoff = time.time() - (days * 86400.0)
        keep = {}
        removed = 0
        for key, entry in self.entries.items():
            created = entry.get("created_at") or 0
            if created < cutoff:
                removed += 1
            else:
                keep[key] = entry
        self.entries = keep
        if os.path.isdir(self.cache_dir):
            self.save()
        return removed

    def entries_existed_or_dir(self):
        return os.path.isdir(self.cache_dir)

    def cache_file_size_bytes(self):
        """Return the on-disk size of the cache."""
        total = 0
        if not os.path.isdir(self.cache_dir):
            return 0
        for root, _, files in os.walk(self.cache_dir):
            for name in files:
                path = os.path.join(root, name)
                try:
                    total += os.path.getsize(path)
                except OSError:
                    continue
        return total

    def stats(self):
        """Return a stats dict including cache_file_size_bytes."""
        return {
            "cached_files": len(self.entries),
            "cache_file_size_bytes": self.cache_file_size_bytes(),
        }

    def export_to(self, dest_path):
        """Export the cache to a JSON file with format_version."""
        payload = {
            "format_version": CACHE_FORMAT_VERSION,
            "entries": list(self.entries.values()),
        }
        dest_dir = os.path.dirname(os.path.abspath(dest_path))
        if dest_dir:
            os.makedirs(dest_dir, exist_ok=True)
        with open(dest_path, "w", encoding="utf-8") as handle:
            json.dump(payload, handle, sort_keys=True, indent=2)
        return dest_path

    def import_from(self, src_path):
        """Import and merge entries from an exported cache file.

        Incompatible format_version or malformed input is discarded.
        Returns the number of entries merged.
        """
        try:
            with open(src_path, encoding="utf-8") as handle:
                payload = json.load(handle)
        except (OSError, ValueError, TypeError, json.JSONDecodeError):
            LOG.warning("Discarding malformed cache import: %s", src_path)
            return 0
        if not isinstance(payload, dict):
            return 0
        if payload.get("format_version") != CACHE_FORMAT_VERSION:
            LOG.warning(
                "Discarding incompatible cache import format: %s", src_path
            )
            return 0
        raw_entries = payload.get("entries") or []
        if isinstance(raw_entries, dict):
            raw_entries = list(raw_entries.values())
        merged = 0
        for item in raw_entries:
            entry = _validate_entry(item)
            if entry is None:
                continue
            key = normalize_path(entry.get("path"))
            if not key:
                continue
            self.entries[key] = entry
            merged += 1
        if merged:
            self.ensure_dir()
            self.save()
        return merged


class CacheStats:
    """Per-run incremental cache counters."""

    def __init__(self):
        self.hits = 0
        self.misses = 0
        self.invalidation_counts = {
            reason: 0 for reason in INVALIDATION_REASONS
        }
        self.invalidation_details = []

    def record_hit(self, fname):
        self.hits += 1

    def record_miss(self, fname, reason):
        self.misses += 1
        if reason in self.invalidation_counts:
            self.invalidation_counts[reason] += 1
        self.invalidation_details.append({"path": fname, "reason": reason})

    def as_cache_info(self, total_files=0):
        info = default_cache_info()
        info["total_files"] = total_files
        info["cache_hits"] = self.hits
        info["cache_misses"] = self.misses
        info["invalidation_counts"] = dict(self.invalidation_counts)
        info["invalidation_details"] = list(self.invalidation_details)
        return info
