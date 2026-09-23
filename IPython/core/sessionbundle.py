"""Record an IPython session to a single ``.ipybundle`` file and replay it."""

from __future__ import annotations

import json
import platform
import sys
import traceback
import zipfile
from contextlib import contextmanager
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Iterator

from IPython.core.release import __version__ as ipython_version

FORMAT_NAME = "ipython-session-bundle"
FORMAT_VERSION = 1
REDACTION_TOKEN = "<redacted>"

_METADATA_REQUIRED = (
    "format",
    "format_version",
    "created_at",
    "ipython_version",
    "python_version",
    "platform",
    "redactions",
)

_EVENT_REQUIRED = (
    "type",
    "seq",
    "recorded_at",
    "execution_count",
    "code",
    "success",
    "stdout",
    "stderr",
    "execute_result",
)


class SessionBundleValidationError(Exception):
    """Raised when a session bundle fails strict validation."""

    def __init__(self, bundle_path, errors):
        self.bundle_path = Path(bundle_path)
        self.errors = list(errors)
        joined = "; ".join(self.errors) if self.errors else "invalid session bundle"
        super().__init__(f"{self.bundle_path}: {joined}")


def _utcnow_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def _is_iso8601(value: str) -> bool:
    if not isinstance(value, str) or not value:
        return False
    text = value[:-1] + "+00:00" if value.endswith("Z") else value
    try:
        datetime.fromisoformat(text)
    except ValueError:
        return False
    return True


def _redact_text(text: str, patterns: list[str]) -> str:
    for pattern in patterns:
        if pattern:
            text = text.replace(pattern, REDACTION_TOKEN)
    return text


def _json_default(value: Any) -> str:
    return repr(value)


def _metadata(redactions: list[str], event_count: int | None = None) -> dict:
    meta = {
        "format": FORMAT_NAME,
        "format_version": FORMAT_VERSION,
        "created_at": _utcnow_iso(),
        "ipython_version": ipython_version,
        "python_version": platform.python_version(),
        "platform": platform.platform(),
        "redactions": list(redactions),
    }
    if event_count is not None:
        meta["event_count"] = event_count
    return meta


def save_session_bundle(path, meta, events, *, overwrite: bool = False) -> Path:
    """Write ``metadata.json`` and ``events.jsonl`` into a bundle at ``path``."""
    bundle_path = Path(path)
    if bundle_path.exists() and not overwrite:
        raise FileExistsError(bundle_path)

    patterns = [p for p in (meta.get("redactions") or []) if isinstance(p, str)]
    lines = []
    for event in events:
        line = json.dumps(event, ensure_ascii=False, default=_json_default)
        line = _redact_text(line, patterns)
        lines.append(line)
    payload = "\n".join(lines)
    if lines:
        payload += "\n"
    meta_bytes = json.dumps(meta, ensure_ascii=False, indent=2).encode("utf-8")

    if bundle_path.exists() and overwrite and bundle_path.is_file():
        bundle_path.unlink()

    bundle_path.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(bundle_path, "w", compression=zipfile.ZIP_DEFLATED) as zf:
        zf.writestr("metadata.json", meta_bytes)
        zf.writestr("events.jsonl", payload.encode("utf-8"))
    return bundle_path


def load_session_bundle(path) -> tuple[dict, list]:
    """Load bundle metadata and events without executing recorded code."""
    bundle_path = Path(path)
    with zipfile.ZipFile(bundle_path, "r") as zf:
        metadata = json.loads(zf.read("metadata.json").decode("utf-8"))
        raw = zf.read("events.jsonl").decode("utf-8")
    events = []
    for line in raw.splitlines():
        if not line.strip():
            continue
        events.append(json.loads(line))
    return metadata, events


def _check_execute_result(value, prefix: str, errors: list[str]) -> None:
    if not isinstance(value, dict):
        errors.append(f"{prefix} execute_result must be an object")
        return
    if not value:
        return
    text = value.get("text/plain", None)
    if "text/plain" not in value or not isinstance(text, str):
        errors.append(f"{prefix} execute_result must include text/plain as a string")


def validate_session_bundle(path, *, strict: bool = True) -> list[str]:
    """Return human-readable schema or invariant errors for a bundle."""
    bundle_path = Path(path)
    errors: list[str] = []

    if not bundle_path.exists():
        errors.append(f"bundle does not exist: {bundle_path}")
        if strict:
            raise SessionBundleValidationError(bundle_path, errors)
        return errors
    if not zipfile.is_zipfile(bundle_path):
        errors.append("bundle is not a zip archive")
        if strict:
            raise SessionBundleValidationError(bundle_path, errors)
        return errors

    try:
        with zipfile.ZipFile(bundle_path, "r") as zf:
            names = set(zf.namelist())
            if "metadata.json" not in names:
                errors.append("missing metadata.json")
                metadata = None
            else:
                try:
                    metadata = json.loads(zf.read("metadata.json").decode("utf-8"))
                except (UnicodeDecodeError, json.JSONDecodeError) as exc:
                    errors.append(f"metadata.json is not valid JSON: {exc}")
                    metadata = None
            if "events.jsonl" not in names:
                errors.append("missing events.jsonl")
                raw_events = None
            else:
                try:
                    raw_events = zf.read("events.jsonl").decode("utf-8")
                except UnicodeDecodeError as exc:
                    errors.append(f"events.jsonl is not valid UTF-8: {exc}")
                    raw_events = None
    except zipfile.BadZipFile as exc:
        errors.append(f"bundle is not a readable zip archive: {exc}")
        if strict:
            raise SessionBundleValidationError(bundle_path, errors)
        return errors

    if metadata is None:
        if not any("metadata.json" in err or err.startswith("missing metadata") for err in errors):
            errors.append("metadata.json must be an object")
    elif not isinstance(metadata, dict):
        errors.append("metadata.json must be an object")
    else:
        for key in _METADATA_REQUIRED:
            if key not in metadata:
                errors.append(f"metadata missing {key}")
        if metadata.get("format") != FORMAT_NAME:
            errors.append(
                f"metadata format must be {FORMAT_NAME!r}"
            )
        version = metadata.get("format_version")
        if not isinstance(version, int) or isinstance(version, bool) or version < 1:
            errors.append("metadata format_version must be an integer >= 1")
        if not _is_iso8601(metadata.get("created_at")):
            errors.append("metadata created_at must be an ISO-8601 timestamp")
        for key in ("ipython_version", "python_version", "platform"):
            if key in metadata and not isinstance(metadata.get(key), str):
                errors.append(f"metadata {key} must be a string")
        redactions = metadata.get("redactions")
        if not isinstance(redactions, list) or not all(isinstance(p, str) for p in redactions):
            errors.append("metadata redactions must be a list of strings")
        if "event_count" in metadata:
            count = metadata["event_count"]
            if not isinstance(count, int) or isinstance(count, bool):
                errors.append("metadata event_count must be an integer")

    events: list = []
    if raw_events is not None:
        for lineno, line in enumerate(raw_events.splitlines(), start=1):
            if not line.strip():
                continue
            try:
                event = json.loads(line)
            except json.JSONDecodeError as exc:
                errors.append(f"events.jsonl line {lineno} is not valid JSON: {exc}")
                continue
            if not isinstance(event, dict):
                errors.append(f"events.jsonl line {lineno} must be an object")
                continue
            events.append(event)

    if isinstance(metadata, dict) and "event_count" in metadata:
        count = metadata["event_count"]
        if isinstance(count, int) and not isinstance(count, bool) and count != len(events):
            errors.append(
                f"metadata event_count {count} does not match {len(events)} events"
            )

    expected_seq = 1
    for index, event in enumerate(events):
        prefix = f"event {index + 1}"
        for key in _EVENT_REQUIRED:
            if key not in event:
                errors.append(f"{prefix} missing {key}")
        if event.get("type") != "cell":
            errors.append(f"{prefix} type must be 'cell'")
        seq = event.get("seq")
        if not isinstance(seq, int) or isinstance(seq, bool) or seq != expected_seq:
            errors.append(
                f"{prefix} seq must be {expected_seq} (contiguous, starting at 1)"
            )
        expected_seq += 1
        if not _is_iso8601(event.get("recorded_at")):
            errors.append(f"{prefix} recorded_at must be an ISO-8601 timestamp")
        execution_count = event.get("execution_count", 0)
        if execution_count is not None and (
            not isinstance(execution_count, int) or isinstance(execution_count, bool)
        ):
            errors.append(f"{prefix} execution_count must be an integer or null")
        if not isinstance(event.get("code"), str):
            errors.append(f"{prefix} code must be a string")
        if not isinstance(event.get("success"), bool):
            errors.append(f"{prefix} success must be a boolean")
        if not isinstance(event.get("stdout"), str):
            errors.append(f"{prefix} stdout must be a string")
        if not isinstance(event.get("stderr"), str):
            errors.append(f"{prefix} stderr must be a string")
        _check_execute_result(event.get("execute_result"), prefix, errors)
        success = event.get("success")
        if success is False:
            err = event.get("error")
            if not isinstance(err, dict):
                errors.append(f"{prefix} must include error when success is false")
            else:
                if not isinstance(err.get("ename"), str):
                    errors.append(f"{prefix} error.ename must be a string")
                if not isinstance(err.get("evalue"), str):
                    errors.append(f"{prefix} error.evalue must be a string")
                tb = err.get("traceback")
                if (
                    not isinstance(tb, list)
                    or not tb
                    or not all(isinstance(item, str) for item in tb)
                ):
                    errors.append(
                        f"{prefix} error.traceback must be a non-empty list of strings"
                    )

    if strict and errors:
        raise SessionBundleValidationError(bundle_path, errors)
    return errors


def _normalize_redact(redact) -> list[str]:
    if redact is None:
        return []
    if isinstance(redact, str):
        return [redact]
    return [str(item) for item in redact]


def _execute_result_payload(shell, result) -> dict:
    value = getattr(result, "result", None)
    if value is None:
        return {}
    try:
        format_dict, _md = shell.display_formatter.format(value)
    except Exception:
        format_dict = {"text/plain": repr(value)}
    if not format_dict:
        return {}
    text = format_dict.get("text/plain")
    if not isinstance(text, str):
        text = "" if text is None else str(text)
    payload = {"text/plain": text}
    return payload


def _error_payload(exc: BaseException) -> dict:
    tb = traceback.format_exception(type(exc), exc, exc.__traceback__)
    tb = [str(line) for line in tb if line is not None]
    if not tb:
        tb = [f"{type(exc).__name__}: {exc}"]
    return {
        "ename": type(exc).__name__,
        "evalue": str(exc),
        "traceback": tb,
    }


class _SessionBundleRecorder:
    def __init__(self, shell, path: Path, redactions: list[str]):
        self.shell = shell
        self.path = path
        self.redactions = list(redactions)
        self.events: list[dict] = []
        self.metadata = _metadata(self.redactions, event_count=0)
        self._armed = False
        self.accepting = False
        self._stdout: list[str] = []
        self._stderr: list[str] = []
        self.shell.events.register("pre_run_cell", self._pre_run_cell)
        self.shell.events.register("post_run_cell", self._post_run_cell)

    def capture(self, channel: str, data) -> None:
        if not data:
            return
        bucket = self._stdout if channel == "stdout" else self._stderr
        bucket.append(data if isinstance(data, str) else str(data))

    def close(self) -> None:
        events = self.shell.events
        for name, callback in (
            ("pre_run_cell", self._pre_run_cell),
            ("post_run_cell", self._post_run_cell),
        ):
            try:
                events.unregister(name, callback)
            except (ValueError, KeyError):
                pass

    def _pre_run_cell(self, info) -> None:
        self._armed = True
        self.accepting = True
        self._stdout = []
        self._stderr = []
        self._cell_store_history = bool(getattr(info, "store_history", False))

    def _post_run_cell(self, result) -> None:
        if not self._armed or result is None:
            self._armed = False
            self.accepting = False
            return
        self._armed = False
        self.accepting = False
        info = getattr(result, "info", None)
        code = ""
        if info is not None:
            code = info.raw_cell or ""
        store_history = bool(getattr(info, "store_history", False))
        execution_count = result.execution_count if store_history else None
        success = bool(result.success)
        event = {
            "type": "cell",
            "seq": len(self.events) + 1,
            "recorded_at": _utcnow_iso(),
            "execution_count": execution_count,
            "code": code,
            "success": success,
            "stdout": "".join(self._stdout),
            "stderr": "".join(self._stderr),
            "execute_result": _execute_result_payload(self.shell, result),
        }
        if not success:
            exc = result.error_in_exec or result.error_before_exec
            if exc is None:
                exc = RuntimeError("cell failed")
            event["error"] = _error_payload(exc)
        self.events.append(event)
        self.metadata["event_count"] = len(self.events)
        save_session_bundle(
            self.path, self.metadata, self.events, overwrite=True
        )


def start_session_bundle(shell, path, *, overwrite: bool = False, redact=None) -> str:
    """Begin recording ``shell`` into a session bundle at ``path``."""
    active = getattr(shell, "_session_bundle_recorder", None)
    if active is not None:
        raise RuntimeError("a session bundle recording is already active")

    redactions = _normalize_redact(redact)
    bundle_path = Path(path).expanduser()
    if bundle_path.exists() and not overwrite:
        raise FileExistsError(bundle_path)
    if bundle_path.exists() and bundle_path.is_dir():
        raise FileExistsError(bundle_path)

    meta = _metadata(redactions, event_count=0)
    save_session_bundle(bundle_path, meta, [], overwrite=True)
    recorder = _SessionBundleRecorder(shell, bundle_path, redactions)
    recorder.metadata = meta
    shell._session_bundle_recorder = recorder
    return str(bundle_path)


def stop_session_bundle(shell) -> str:
    """Stop the active recording and return the bundle path."""
    recorder = getattr(shell, "_session_bundle_recorder", None)
    if recorder is None:
        raise RuntimeError("no session bundle recording is active")
    recorder.metadata["event_count"] = len(recorder.events)
    save_session_bundle(
        recorder.path, recorder.metadata, recorder.events, overwrite=True
    )
    path = str(recorder.path)
    recorder.close()
    shell._session_bundle_recorder = None
    return path


def session_bundle_status(shell) -> dict:
    """Return ``{"recording": bool, "path": str | None}``."""
    recorder = getattr(shell, "_session_bundle_recorder", None)
    if recorder is None:
        return {"recording": False, "path": None}
    return {"recording": True, "path": str(recorder.path)}


def replay_session_bundle(shell, path, *, stop_on_error: bool = True, store_history: bool = True):
    """Re-execute recorded cells in ``shell``."""
    errors = validate_session_bundle(path, strict=False)
    fatal = [
        err
        for err in errors
        if err.startswith("bundle ") or err.startswith("missing ")
    ]
    if fatal:
        raise SessionBundleValidationError(path, errors)
    _metadata_obj, events = load_session_bundle(path)
    results = []
    for event in events:
        if event.get("type") != "cell":
            continue
        result = shell.run_cell(event.get("code", ""), store_history=store_history)
        results.append(result)
        if stop_on_error and result is not None and not result.success:
            break
    return results


@contextmanager
def session_bundle_recorder(shell, path, *, overwrite: bool = False, redact=None) -> Iterator[str]:
    """Record ``shell`` for the duration of the ``with`` block."""
    bundle_path = start_session_bundle(
        shell, path, overwrite=overwrite, redact=redact
    )
    try:
        yield bundle_path
    finally:
        if getattr(shell, "_session_bundle_recorder", None) is not None:
            stop_session_bundle(shell)
