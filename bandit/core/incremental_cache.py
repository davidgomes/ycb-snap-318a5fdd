#
# SPDX-License-Identifier: Apache-2.0
"""Incremental analysis cache for bandit."""
import hashlib
import json
import logging
import os
import shutil
import time
from datetime import datetime, timezone

LOG = logging.getLogger(__name__)

CACHE_FORMAT_VERSION = 1
INDEX_FILENAME = "cache_index.json"
ENTRIES_DIR = "entries"


class CacheStats:
    """Track cache hit/miss and invalidation statistics."""

    def __init__(self):
        self.cache_hits = 0
        self.cache_misses = 0
        self.files_scanned = 0
        self.files_cached = 0
        self.invalidation_counts = {
            "file_changed": 0,
            "config_changed": 0,
            "expired": 0,
            "not_cached": 0,
        }
        self.invalidation_reasons = []

    @property
    def total_files(self):
        return self.cache_hits + self.cache_misses


def build_cache_key(severity, confidence, profile_name, profile):
    """Build a cache key from analysis options and profile content."""
    include = profile.get("include") or set()
    exclude = profile.get("exclude") or set()
    blacklist = profile.get("blacklist") or {}
    key_data = {
        "severity": severity,
        "confidence": confidence,
        "profile_name": profile_name or "",
        "profile_include": sorted(include),
        "profile_exclude": sorted(exclude),
        "profile_blacklist": json.dumps(blacklist, sort_keys=True),
    }
    raw = json.dumps(key_data, sort_keys=True)
    return hashlib.sha256(raw.encode("utf-8")).hexdigest()


def _file_fingerprint(path):
    """Return mtime and size for a file."""
    stat = os.stat(path)
    return stat.st_mtime, stat.st_size


def _compute_checksum(data):
    """Compute integrity checksum for a cache entry payload."""
    encoded = json.dumps(data, sort_keys=True).encode("utf-8")
    return hashlib.sha256(encoded).hexdigest()


def _entry_key(file_path, cache_key):
    """Stable filename for a cache entry."""
    digest = hashlib.sha256(
        f"{file_path}:{cache_key}".encode("utf-8")
    ).hexdigest()
    return digest


class IncrementalCache:
    """File-level incremental analysis cache."""

    def __init__(
        self,
        cache_dir,
        cache_key,
        expiry_days=None,
        size_limit=None,
        enabled=False,
        force_rescan=False,
    ):
        self.cache_dir = cache_dir
        self.cache_key = cache_key
        self.expiry_days = expiry_days
        self.size_limit = size_limit
        self.enabled = enabled
        self.force_rescan = force_rescan
        self.stats = CacheStats()
        self._index = {}
        self._dirty = False

        if self.enabled and self.cache_dir:
            self._ensure_cache_dir()
            self._load_index()

    def _ensure_cache_dir(self):
        if not os.path.isdir(self.cache_dir):
            os.makedirs(self.cache_dir, exist_ok=True)
        entries_path = os.path.join(self.cache_dir, ENTRIES_DIR)
        if not os.path.isdir(entries_path):
            os.makedirs(entries_path, exist_ok=True)

    def _index_path(self):
        return os.path.join(self.cache_dir, INDEX_FILENAME)

    def _entry_path(self, entry_id):
        return os.path.join(self.cache_dir, ENTRIES_DIR, f"{entry_id}.json")

    def _load_index(self):
        index_path = self._index_path()
        if not os.path.isfile(index_path):
            self._index = {}
            return
        try:
            with open(index_path, encoding="utf-8") as f:
                data = json.load(f)
            if not isinstance(data, dict):
                LOG.debug("Cache index is not a dict, discarding")
                self._index = {}
                return
            self._index = data.get("entries", {})
        except (OSError, json.JSONDecodeError, TypeError) as exc:
            LOG.debug("Failed to load cache index: %s", exc)
            self._index = {}

    def _save_index(self):
        if not self.enabled or not self.cache_dir:
            return
        index_path = self._index_path()
        payload = {
            "format_version": CACHE_FORMAT_VERSION,
            "entries": self._index,
        }
        with open(index_path, "w", encoding="utf-8") as f:
            json.dump(payload, f, sort_keys=True, indent=2)
        self._dirty = False

    def _load_entry_file(self, entry_id):
        path = self._entry_path(entry_id)
        if not os.path.isfile(path):
            return None
        try:
            with open(path, encoding="utf-8") as f:
                data = json.load(f)
        except (OSError, json.JSONDecodeError):
            return None
        if not isinstance(data, dict):
            return None
        stored_checksum = data.pop("checksum", None)
        computed = _compute_checksum(data)
        if stored_checksum != computed:
            LOG.debug("Cache entry checksum mismatch for %s", entry_id)
            self._remove_entry(entry_id)
            return None
        data["checksum"] = stored_checksum
        return data

    def _write_entry_file(self, entry_id, data):
        payload = dict(data)
        payload["checksum"] = _compute_checksum(
            {k: v for k, v in payload.items() if k != "checksum"}
        )
        path = self._entry_path(entry_id)
        with open(path, "w", encoding="utf-8") as f:
            json.dump(payload, f, sort_keys=True)

    def _remove_entry(self, entry_id):
        path = self._entry_path(entry_id)
        if os.path.isfile(path):
            os.remove(path)
        self._index = {
            k: v for k, v in self._index.items() if v.get("entry_id") != entry_id
        }
        self._dirty = True

    def _is_expired(self, created_at):
        if self.expiry_days is None:
            return False
        if self.expiry_days == 0:
            return True
        try:
            created = datetime.fromisoformat(created_at)
            if created.tzinfo is None:
                created = created.replace(tzinfo=timezone.utc)
        except (ValueError, TypeError):
            return True
        age_days = (datetime.now(timezone.utc) - created).total_seconds() / 86400
        return age_days > self.expiry_days

    def _record_invalidation(self, reason, file_path):
        if reason in self.stats.invalidation_counts:
            self.stats.invalidation_counts[reason] += 1
        self.stats.invalidation_reasons.append((file_path, reason))

    def lookup(self, file_path):
        """Return cached entry data or None."""
        if not self.enabled or file_path == "-":
            return None

        if self.force_rescan:
            self.stats.cache_misses += 1
            self.stats.files_scanned += 1
            return None

        entry_id = _entry_key(file_path, self.cache_key)
        meta = self._index.get(file_path)
        if not meta or meta.get("entry_id") != entry_id:
            self._record_invalidation("not_cached", file_path)
            self.stats.cache_misses += 1
            self.stats.files_scanned += 1
            return None

        if meta.get("cache_key") != self.cache_key:
            self._record_invalidation("config_changed", file_path)
            self._remove_entry(entry_id)
            self.stats.cache_misses += 1
            self.stats.files_scanned += 1
            return None

        if self._is_expired(meta.get("created_at", "")):
            self._record_invalidation("expired", file_path)
            self._remove_entry(entry_id)
            self.stats.cache_misses += 1
            self.stats.files_scanned += 1
            return None

        try:
            mtime, size = _file_fingerprint(file_path)
        except OSError:
            self._record_invalidation("file_changed", file_path)
            self.stats.cache_misses += 1
            self.stats.files_scanned += 1
            return None

        if meta.get("mtime") != mtime or meta.get("size") != size:
            self._record_invalidation("file_changed", file_path)
            self._remove_entry(entry_id)
            self.stats.cache_misses += 1
            self.stats.files_scanned += 1
            return None

        entry = self._load_entry_file(entry_id)
        if entry is None:
            self._record_invalidation("not_cached", file_path)
            self.stats.cache_misses += 1
            self.stats.files_scanned += 1
            return None

        self.stats.cache_hits += 1
        self.stats.files_cached += 1
        return entry

    def store(self, file_path, results_data, metrics_data, score_data):
        """Store analysis results for a file."""
        if not self.enabled or file_path == "-":
            return

        try:
            mtime, size = _file_fingerprint(file_path)
        except OSError:
            return

        entry_id = _entry_key(file_path, self.cache_key)
        created_at = datetime.now(timezone.utc).isoformat()
        entry = {
            "file_path": file_path,
            "cache_key": self.cache_key,
            "mtime": mtime,
            "size": size,
            "created_at": created_at,
            "results": results_data,
            "metrics": metrics_data,
            "score": score_data,
        }
        self._write_entry_file(entry_id, entry)
        self._index[file_path] = {
            "entry_id": entry_id,
            "cache_key": self.cache_key,
            "mtime": mtime,
            "size": size,
            "created_at": created_at,
        }
        self._dirty = True
        self._enforce_size_limit()

    def finalize(self):
        """Persist index and prune if needed."""
        if self._dirty:
            self._save_index()

    def _enforce_size_limit(self):
        if not self.size_limit:
            return
        total = self.get_cache_size_bytes()
        if total <= self.size_limit:
            return
        # Remove oldest entries until under limit
        entries = sorted(
            self._index.items(),
            key=lambda x: x[1].get("created_at", ""),
        )
        for file_path, meta in entries:
            if self.get_cache_size_bytes() <= self.size_limit:
                break
            entry_id = meta.get("entry_id")
            if entry_id:
                self._remove_entry(entry_id)
            self._index.pop(file_path, None)
            self._dirty = True

    @staticmethod
    def get_directory_size(cache_dir):
        """Return total size in bytes of cache directory."""
        if not cache_dir or not os.path.isdir(cache_dir):
            return 0
        total = 0
        for root, _, files in os.walk(cache_dir):
            for name in files:
                try:
                    total += os.path.getsize(os.path.join(root, name))
                except OSError:
                    pass
        return total

    def get_cache_size_bytes(self):
        return self.get_directory_size(self.cache_dir)

    def list_cached_files(self):
        """Return sorted list of cached file paths for current cache key."""
        if not self.cache_dir or not os.path.isdir(self.cache_dir):
            return []
        return sorted(
            path
            for path, meta in self._index.items()
            if meta.get("cache_key") == self.cache_key
        )

    def clear(self):
        """Remove all cache data."""
        if not self.cache_dir or not os.path.isdir(self.cache_dir):
            return
        shutil.rmtree(self.cache_dir)
        self._index = {}
        self._dirty = False

    def prune(self, days):
        """Remove entries older than days."""
        if not self.cache_dir or not os.path.isdir(self.cache_dir):
            return 0
        removed = 0
        cutoff = time.time() - (days * 86400)
        to_remove = []
        for file_path, meta in list(self._index.items()):
            created_at = meta.get("created_at", "")
            try:
                created = datetime.fromisoformat(created_at)
                if created.tzinfo is None:
                    created = created.replace(tzinfo=timezone.utc)
                created_ts = created.timestamp()
            except (ValueError, TypeError):
                created_ts = 0
            if created_ts < cutoff:
                to_remove.append((file_path, meta.get("entry_id")))
        for file_path, entry_id in to_remove:
            if entry_id:
                self._remove_entry(entry_id)
            self._index.pop(file_path, None)
            removed += 1
            self._dirty = True
        if self._dirty:
            self._save_index()
        return removed

    def export_to_file(self, export_path):
        """Export cache entries to a JSON file."""
        entries = {}
        for file_path, meta in self._index.items():
            entry_id = meta.get("entry_id")
            if not entry_id:
                continue
            entry = self._load_entry_file(entry_id)
            if entry:
                entries[file_path] = entry
        payload = {
            "format_version": CACHE_FORMAT_VERSION,
            "entries": entries,
        }
        with open(export_path, "w", encoding="utf-8") as f:
            json.dump(payload, f, sort_keys=True, indent=2)

    def import_from_file(self, import_path):
        """Import and merge cache entries from an exported file."""
        if not os.path.isfile(import_path):
            return
        try:
            with open(import_path, encoding="utf-8") as f:
                data = json.load(f)
        except (OSError, json.JSONDecodeError):
            return
        if not isinstance(data, dict):
            return
        if data.get("format_version") != CACHE_FORMAT_VERSION:
            return
        entries = data.get("entries")
        if not isinstance(entries, dict):
            return
        self._ensure_cache_dir()
        for file_path, entry in entries.items():
            if not isinstance(entry, dict):
                continue
            entry_id = _entry_key(file_path, entry.get("cache_key", ""))
            stored = dict(entry)
            stored_checksum = stored.get("checksum")
            computed = _compute_checksum(
                {k: v for k, v in stored.items() if k != "checksum"}
            )
            if stored_checksum and stored_checksum != computed:
                continue
            stored["checksum"] = computed
            self._write_entry_file(entry_id, stored)
            self._index[file_path] = {
                "entry_id": entry_id,
                "cache_key": entry.get("cache_key"),
                "mtime": entry.get("mtime"),
                "size": entry.get("size"),
                "created_at": entry.get("created_at"),
            }
        self._dirty = True
        self._save_index()


def serialize_issues(issues):
    """Serialize issue objects to dicts for caching."""
    return [issue.as_dict(with_code=False) for issue in issues]


def deserialize_issues(issues_data):
    """Deserialize cached issue dicts to Issue objects."""
    from bandit.core.issue import issue_from_dict

    return [issue_from_dict(d) for d in issues_data]


def resolve_cache_settings(args, config):
    """Resolve incremental cache settings from CLI and config."""
    inc_config = config.get_option("incremental_analysis") or {}

    enabled = bool(getattr(args, "incremental", False))
    if getattr(args, "no_incremental", False):
        enabled = False
    if not getattr(args, "incremental", False) and not getattr(
        args, "no_incremental", False
    ):
        if inc_config.get("enabled"):
            enabled = True

    if getattr(args, "warm_cache", False):
        enabled = True

    cache_dir = getattr(args, "cache_dir", None)
    if not cache_dir:
        cache_dir = inc_config.get("cache_directory")
    if not cache_dir and enabled:
        cache_dir = os.path.join(os.path.expanduser("~"), ".bandit_cache")

    expiry_days = inc_config.get("cache_expiry_days")
    if expiry_days is not None:
        expiry_days = int(expiry_days)

    size_limit = getattr(args, "cache_size_limit", None)

    force_rescan = getattr(args, "force_rescan", False)
    if force_rescan and not enabled:
        force_rescan = False

    return {
        "enabled": enabled,
        "cache_dir": cache_dir,
        "expiry_days": expiry_days,
        "size_limit": size_limit,
        "force_rescan": force_rescan,
        "warm_cache": getattr(args, "warm_cache", False),
    }
