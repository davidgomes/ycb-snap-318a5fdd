"""Record an IPython session to a single bundle file and replay it later.

A session bundle is a ZIP archive (usually named ``*.ipybundle``) with two
members:

* ``metadata.json`` — format identifier, versions, and redaction patterns
* ``events.jsonl`` — one JSON object per executed cell, in execution order
"""

from __future__ import annotations

import errno
import json
import os
import platform
import sys
import traceback
import zipfile
from contextlib import contextmanager
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Iterator

from IPython.core import release

FORMAT_NAME = "ipython-session-bundle"
FORMAT_VERSION = 1
REDACTION_TOKEN = "<redacted>"
METADATA_NAME = "metadata.json"
EVENTS_NAME = "events.jsonl"

_RECORDER_ATTR = "_session_bundle_recorder"


class SessionBundleValidationError(Exception):
    """Raised when a session bundle fails validation in strict mode."""

    def __init__(self, bundle_path, errors):
        self.bundle_path = Path(bundle_path)
        self.errors = list(errors)
        message = f"Invalid session bundle: {self.bundle_path}"
        if self.errors:
            message += "\n" + "\n".join(f"- {err}" for err in self.errors)
        super().__init__(message)


def _as_path(path) -> Path:
    return Path(path).expanduser()


def _now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def _is_iso8601(value: Any) -> bool:
    if not isinstance(value, str) or not value.strip():
        return False
    text = value[:-1] + "+00:00" if value.endswith("Z") else value
    try:
        datetime.fromisoformat(text)
    except ValueError:
        return False
    return True


def _is_int(value: Any) -> bool:
    return isinstance(value, int) and not isinstance(value, bool)


def _normalize_redactions(redact) -> list[str]:
    if redact is None:
        return []
    if isinstance(redact, str):
        return [redact]
    return [p if isinstance(p, str) else str(p) for p in redact]


def _redact_text(text: str, patterns: list[str]) -> str:
    for pattern in patterns:
        if pattern:
            text = text.replace(pattern, REDACTION_TOKEN)
    return text


def _redact_obj(obj: Any, patterns: list[str]) -> Any:
    if not patterns:
        return obj
    if isinstance(obj, str):
        return _redact_text(obj, patterns)
    if isinstance(obj, list):
        return [_redact_obj(item, patterns) for item in obj]
    if isinstance(obj, dict):
        return {key: _redact_obj(value, patterns) for key, value in obj.items()}
    return obj


def _json_safe(value: Any) -> Any:
    if value is None or isinstance(value, (str, bool)):
        return value
    if isinstance(value, int):
        return value
    if isinstance(value, float):
        if value != value or value in (float("inf"), float("-inf")):
            return repr(value)
        return value
    if isinstance(value, dict):
        return {str(key): _json_safe(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [_json_safe(item) for item in value]
    return str(value)


def _prepare_path(path) -> tuple[Path, str]:
    """Return a filesystem path and the string reported to callers."""
    if isinstance(path, str):
        shown = os.path.expanduser(path)
    else:
        shown = str(Path(path).expanduser())
    return Path(shown), shown


def _encode_events(events: list[dict]) -> str:
    lines = [
        json.dumps(event, ensure_ascii=False, allow_nan=False) for event in events
    ]
    if not lines:
        return ""
    return "\n".join(lines) + "\n"


def _new_metadata(redactions: list[str]) -> dict[str, Any]:
    return {
        "format": FORMAT_NAME,
        "format_version": FORMAT_VERSION,
        "created_at": _now_iso(),
        "ipython_version": release.version,
        "python_version": platform.python_version(),
        "platform": platform.platform(),
        "redactions": list(redactions),
        "event_count": 0,
    }


def _file_exists_error(path) -> FileExistsError:
    return FileExistsError(errno.EEXIST, os.strerror(errno.EEXIST), os.fspath(path))


def save_session_bundle(path, meta, events, *, overwrite: bool = False) -> Path:
    """Write ``metadata.json`` and ``events.jsonl`` into a bundle at ``path``.

    Parameters
    ----------
    path : path-like
        Destination bundle path.
    meta : dict
        JSON-serializable metadata object.
    events : sequence of dict
        Cell events written as JSON lines.
    overwrite : bool, optional
        When False (default) and ``path`` already exists, raise
        ``FileExistsError``.

    Returns
    -------
    pathlib.Path
        The bundle path that was written.
    """
    path = _as_path(path)
    if path.exists() and not overwrite:
        raise _file_exists_error(path)
    if path.is_dir():
        raise _file_exists_error(path)
    if not isinstance(meta, dict):
        raise TypeError("meta must be a dict")
    if isinstance(events, (str, bytes)) or not isinstance(events, (list, tuple)):
        raise TypeError("events must be a list of event objects")

    payload = _encode_events(list(events))
    metadata_bytes = json.dumps(meta, ensure_ascii=False, indent=2, allow_nan=False).encode(
        "utf-8"
    )
    metadata_bytes += b"\n"

    path.parent.mkdir(parents=True, exist_ok=True)
    tmp_path = path.with_name(path.name + ".tmp")
    try:
        with zipfile.ZipFile(tmp_path, "w", compression=zipfile.ZIP_DEFLATED) as zf:
            zf.writestr(METADATA_NAME, metadata_bytes)
            zf.writestr(EVENTS_NAME, payload.encode("utf-8"))
        os.replace(tmp_path, path)
    finally:
        if tmp_path.exists():
            try:
                tmp_path.unlink()
            except OSError:
                pass
    return path


def load_session_bundle(path):
    """Load bundle metadata and cell events without executing them.

    Parameters
    ----------
    path : path-like
        Bundle to read.

    Returns
    -------
    tuple of (dict, list)
        ``(metadata, events)``.
    """
    path = _as_path(path)
    with zipfile.ZipFile(path, "r") as zf:
        metadata = json.loads(zf.read(METADATA_NAME).decode("utf-8"))
        raw = zf.read(EVENTS_NAME).decode("utf-8")
    events = []
    for line in raw.splitlines():
        if not line.strip():
            continue
        events.append(json.loads(line))
    return metadata, events


def _read_zip_members(path: Path):
    """Return ``(metadata, events, raw_events, errors)`` from a bundle."""
    errors: list[str] = []
    if not path.exists():
        return None, None, "", [f"bundle path does not exist: {path}"]
    if path.is_dir():
        return None, None, "", [f"bundle path is a directory: {path}"]
    try:
        zf = zipfile.ZipFile(path, "r")
    except zipfile.BadZipFile:
        return None, None, "", [f"bundle is not a valid zip archive: {path}"]
    except OSError as exc:
        return None, None, "", [f"cannot read bundle {path}: {exc}"]

    with zf:
        names = zf.namelist()
        if METADATA_NAME not in names:
            errors.append("bundle is missing metadata.json")
        if EVENTS_NAME not in names:
            errors.append("bundle is missing events.jsonl")

        metadata = None
        if METADATA_NAME in names:
            try:
                raw_meta = zf.read(METADATA_NAME).decode("utf-8")
                metadata = json.loads(raw_meta)
            except UnicodeDecodeError as exc:
                errors.append(f"metadata.json is not valid UTF-8: {exc}")
            except json.JSONDecodeError as exc:
                errors.append(f"metadata.json is not valid JSON: {exc}")
            else:
                if not isinstance(metadata, dict):
                    errors.append("metadata.json must contain a JSON object")
                    metadata = None

        events = None
        raw_events = ""
        if EVENTS_NAME in names:
            try:
                raw_events = zf.read(EVENTS_NAME).decode("utf-8")
            except UnicodeDecodeError as exc:
                errors.append(f"events.jsonl is not valid UTF-8: {exc}")
            else:
                events = []
                for lineno, line in enumerate(raw_events.splitlines(), start=1):
                    if not line.strip():
                        continue
                    try:
                        event = json.loads(line)
                    except json.JSONDecodeError as exc:
                        errors.append(
                            f"events.jsonl line {lineno} is not valid JSON: {exc}"
                        )
                        continue
                    if not isinstance(event, dict):
                        errors.append(
                            f"events.jsonl line {lineno} must be a JSON object"
                        )
                        continue
                    events.append(event)
    return metadata, events, raw_events, errors


def _validate_metadata(metadata: dict, events) -> list[str]:
    errors: list[str] = []
    required = (
        "format",
        "format_version",
        "created_at",
        "ipython_version",
        "python_version",
        "platform",
        "redactions",
    )
    for key in required:
        if key not in metadata:
            errors.append(f"metadata.json is missing required field {key!r}")

    if "format" in metadata and metadata["format"] != FORMAT_NAME:
        errors.append(
            "metadata.format must be 'ipython-session-bundle' "
            f"(got {metadata['format']!r})"
        )
    if "format_version" in metadata:
        version = metadata["format_version"]
        if not _is_int(version) or version < 1:
            errors.append(
                "metadata.format_version must be an integer >= 1 "
                f"(got {version!r})"
            )
    if "created_at" in metadata and not _is_iso8601(metadata["created_at"]):
        errors.append(
            "metadata.created_at must be an ISO-8601 timestamp "
            f"(got {metadata['created_at']!r})"
        )
    for key in ("ipython_version", "python_version", "platform"):
        if key in metadata and not isinstance(metadata[key], str):
            errors.append(f"metadata.{key} must be a string (got {metadata[key]!r})")
    if "redactions" in metadata:
        redactions = metadata["redactions"]
        if not isinstance(redactions, list) or not all(
            isinstance(item, str) for item in redactions
        ):
            errors.append("metadata.redactions must be a list of strings")
    if "event_count" in metadata:
        count = metadata["event_count"]
        if not _is_int(count):
            errors.append(
                f"metadata.event_count must be an integer (got {count!r})"
            )
        elif events is not None and count != len(events):
            errors.append(
                "metadata.event_count is "
                f"{count} but events.jsonl contains {len(events)} events"
            )
    return errors


def _validate_event(event: dict, index: int, expected_seq: int) -> list[str]:
    errors: list[str] = []
    label = f"event {index}"
    seq = event.get("seq", None)
    if "seq" in event and _is_int(seq):
        label = f"event seq {seq}"

    if event.get("type") != "cell":
        errors.append(f"{label}: type must be 'cell' (got {event.get('type')!r})")

    if "seq" not in event:
        errors.append(f"{label}: missing field 'seq'")
    elif not _is_int(seq):
        errors.append(f"{label}: seq must be an integer (got {seq!r})")
    elif seq != expected_seq:
        errors.append(
            f"{label}: seq must be contiguous starting at 1 "
            f"(expected {expected_seq}, got {seq})"
        )

    if "recorded_at" not in event:
        errors.append(f"{label}: missing field 'recorded_at'")
    elif not _is_iso8601(event["recorded_at"]):
        errors.append(
            f"{label}: recorded_at must be an ISO-8601 timestamp "
            f"(got {event['recorded_at']!r})"
        )

    if "execution_count" not in event:
        errors.append(f"{label}: missing field 'execution_count'")
    else:
        count = event["execution_count"]
        if count is not None and not _is_int(count):
            errors.append(
                f"{label}: execution_count must be an integer or null (got {count!r})"
            )

    for key in ("code", "stdout", "stderr"):
        if key not in event:
            errors.append(f"{label}: missing field {key!r}")
        elif not isinstance(event[key], str):
            errors.append(f"{label}: {key} must be a string (got {event[key]!r})")

    if "success" not in event:
        errors.append(f"{label}: missing field 'success'")
    elif not isinstance(event["success"], bool):
        errors.append(f"{label}: success must be a boolean (got {event['success']!r})")

    if "execute_result" not in event:
        errors.append(f"{label}: missing field 'execute_result'")
    elif not isinstance(event["execute_result"], dict):
        errors.append(
            f"{label}: execute_result must be an object "
            f"(got {type(event['execute_result']).__name__})"
        )
    elif event["execute_result"]:
        text = event["execute_result"].get("text/plain", None)
        if "text/plain" not in event["execute_result"]:
            errors.append(
                f"{label}: non-empty execute_result must include 'text/plain'"
            )
        elif not isinstance(text, str):
            errors.append(
                f"{label}: execute_result 'text/plain' must be a string (got {text!r})"
            )

    success = event.get("success")
    if success is False:
        error = event.get("error", None)
        if not isinstance(error, dict):
            errors.append(
                f"{label}: failed execution must include an error object "
                "with ename, evalue, and traceback"
            )
        else:
            ename = error.get("ename")
            evalue = error.get("evalue")
            tb = error.get("traceback")
            if not isinstance(ename, str):
                errors.append(f"{label}: error.ename must be a string")
            if not isinstance(evalue, str):
                errors.append(f"{label}: error.evalue must be a string")
            if not isinstance(tb, list) or not tb or not all(
                isinstance(item, str) for item in tb
            ):
                errors.append(
                    f"{label}: error.traceback must be a non-empty list of strings"
                )
    return errors


def _validate_events(events: list) -> list[str]:
    errors: list[str] = []
    for index, event in enumerate(events, start=1):
        errors.extend(_validate_event(event, index, expected_seq=index))
    return errors


def _validate_redactions(redactions, raw_events: str) -> list[str]:
    errors: list[str] = []
    if not isinstance(redactions, list):
        return errors
    for pattern in redactions:
        if isinstance(pattern, str) and pattern and pattern in raw_events:
            errors.append(
                f"redaction pattern {pattern!r} appears in events.jsonl"
            )
    return errors


def validate_session_bundle(path, *, strict: bool = True) -> list[str]:
    """Return human-readable schema and invariant errors for a bundle.

    Parameters
    ----------
    path : path-like
        Bundle to validate.
    strict : bool, optional
        When True (default) and any errors are found, raise
        ``SessionBundleValidationError``. When False, return the errors.

    Returns
    -------
    list of str
        Validation problems. Empty when the bundle is valid.
    """
    path = _as_path(path)
    metadata, events, raw_events, errors = _read_zip_members(path)
    if isinstance(metadata, dict):
        errors.extend(_validate_metadata(metadata, events))
        errors.extend(_validate_redactions(metadata.get("redactions"), raw_events))
    if events is not None:
        errors.extend(_validate_events(events))
    if strict and errors:
        raise SessionBundleValidationError(path, errors)
    return errors


def replay_session_bundle(shell, path, *, stop_on_error: bool = True, store_history: bool = True):
    """Re-execute recorded cells in ``shell``.

    Parameters
    ----------
    shell : InteractiveShell
        Shell that runs the recorded cells.
    path : path-like
        Bundle to replay.
    stop_on_error : bool, optional
        Stop after the first cell that fails. Default True.
    store_history : bool, optional
        When True (default), each replayed cell advances
        ``shell.execution_count`` by one. When False, replay does not
        advance it.

    Returns
    -------
    list
        ``ExecutionResult`` objects for the cells that were replayed.
    """
    validate_session_bundle(path, strict=True)
    _metadata, events = load_session_bundle(path)
    results = []
    for event in events:
        code = event.get("code") or ""
        before = shell.execution_count
        result = shell.run_cell(code, store_history=store_history)
        if store_history:
            if shell.execution_count == before:
                # Blank cells return before InteractiveShell assigns a count.
                shell.execution_count = before + 1
        results.append(result)
        if stop_on_error and (result is None or not result.success):
            break
    return results


@contextmanager
def session_bundle_recorder(shell, path, *, overwrite: bool = False, redact=None) -> Iterator[str]:
    """Record a session for the duration of a ``with`` block.

    Equivalent to calling ``start_session_bundle`` on enter and
    ``stop_session_bundle`` on exit, including ``overwrite`` and ``redact``.
    """
    bundle_path = shell.start_session_bundle(path, overwrite=overwrite, redact=redact)
    try:
        yield bundle_path
    finally:
        status = shell.session_bundle_status()
        if status["recording"] and status["path"] == bundle_path:
            shell.stop_session_bundle()


def get_session_bundle_recorder(shell) -> "SessionBundleRecorder":
    recorder = getattr(shell, _RECORDER_ATTR, None)
    if recorder is None:
        recorder = SessionBundleRecorder(shell)
        setattr(shell, _RECORDER_ATTR, recorder)
    return recorder


class SessionBundleRecorder:
    """Attach to one shell and append a cell event after each execution."""

    def __init__(self, shell):
        self.shell = shell
        self.recording = False
        self.path: Path | None = None
        self.path_str: str | None = None
        self.metadata: dict | None = None
        self.events: list[dict] = []
        self.redactions: list[str] = []
        self._seq = 0
        self._frames: list[dict] = []
        self._skip_result = None

    def status(self) -> dict:
        if self.recording and self.path_str is not None:
            return {"recording": True, "path": self.path_str}
        return {"recording": False, "path": None}

    def start(self, path, *, overwrite: bool = False, redact=None) -> str:
        if self.recording:
            raise RuntimeError("A session bundle recording is already active")
        path, shown = _prepare_path(path)
        if path.exists() and not overwrite:
            raise _file_exists_error(shown)
        if path.is_dir():
            raise _file_exists_error(shown)

        redactions = _normalize_redactions(redact)
        metadata = _new_metadata(redactions)
        save_session_bundle(path, metadata, [], overwrite=True)

        self.path = path
        self.path_str = shown
        self.metadata = metadata
        self.events = []
        self.redactions = redactions
        self._seq = 0
        self._frames = []
        hook = getattr(self.shell, "displayhook", None)
        self._skip_result = getattr(hook, "exec_result", None)
        self.recording = True
        self.shell.events.register("pre_run_cell", self._on_pre_run_cell)
        self.shell.events.register("post_run_cell", self._on_post_run_cell)
        return shown

    def stop(self) -> str:
        if not self.recording or self.path is None or self.metadata is None:
            raise RuntimeError("No session bundle recording is active")
        self.metadata["event_count"] = len(self.events)
        save_session_bundle(self.path, self.metadata, self.events, overwrite=True)
        path_str = self.path_str if self.path_str is not None else str(self.path)
        self._deactivate()
        return path_str

    def _deactivate(self) -> None:
        for event, callback in (
            ("pre_run_cell", self._on_pre_run_cell),
            ("post_run_cell", self._on_post_run_cell),
        ):
            try:
                self.shell.events.unregister(event, callback)
            except (ValueError, KeyError):
                pass
        self._restore_all_frames()
        self.recording = False
        self.path = None
        self.path_str = None
        self.metadata = None
        self.events = []
        self.redactions = []
        self._seq = 0
        self._skip_result = None

    def _suppress_stdio(self) -> bool:
        shell = self.shell
        hook = getattr(shell, "displayhook", None)
        publisher = getattr(shell, "display_pub", None)
        publishing = False
        if publisher is not None:
            is_publishing = getattr(publisher, "is_publishing", False)
            publishing = is_publishing() if callable(is_publishing) else bool(is_publishing)
        hook_active = False
        if hook is not None:
            is_active = getattr(hook, "is_active", False)
            hook_active = is_active() if callable(is_active) else bool(is_active)
        return bool(publishing or hook_active or getattr(shell, "showing_traceback", False))

    def _install_capture(self, frame: dict) -> None:
        saved = []
        for channel in ("stdout", "stderr"):
            stream = getattr(sys, channel)
            original = stream.write

            def write(data, *args, _channel=channel, _original=original, _frame=frame, **kwargs):
                result = _original(data, *args, **kwargs)
                if data and not self._suppress_stdio():
                    text = data if isinstance(data, str) else str(data)
                    _frame[_channel].append(text)
                return result

            stream.write = write
            saved.append((stream, original))
        frame["saved"] = saved

    def _restore_frame(self, frame: dict) -> None:
        for stream, original in reversed(frame.get("saved") or []):
            try:
                stream.write = original
            except Exception:
                pass
        frame["saved"] = []

    def _restore_all_frames(self) -> None:
        while self._frames:
            self._restore_frame(self._frames.pop())

    def _on_pre_run_cell(self, info) -> None:
        if not self.recording:
            return
        frame = {"stdout": [], "stderr": [], "saved": []}
        self._frames.append(frame)
        self._install_capture(frame)

    def _on_post_run_cell(self, result) -> None:
        frame = self._frames.pop() if self._frames else None
        if frame is not None:
            self._restore_frame(frame)
        if self._skip_result is not None and result is self._skip_result:
            self._skip_result = None
            return
        if not self.recording or result is None:
            return
        stdout = "".join(frame["stdout"]) if frame else ""
        stderr = "".join(frame["stderr"]) if frame else ""
        self._record_event(result, stdout, stderr)

    def _execution_count(self, result):
        if "execution_count" not in getattr(result, "__dict__", {}):
            return None
        value = result.execution_count
        if _is_int(value):
            return value
        return None

    def _execute_result(self, result) -> dict:
        if result is None or getattr(result, "result", None) is None:
            return {}
        try:
            format_dict, _md = self.shell.display_formatter.format(result.result)
        except Exception:
            format_dict = {"text/plain": repr(result.result)}
        if not isinstance(format_dict, dict):
            format_dict = {}
        else:
            format_dict = dict(format_dict)
        text = format_dict.get("text/plain")
        if not isinstance(text, str):
            try:
                text = repr(result.result)
            except Exception:
                text = ""
            format_dict["text/plain"] = text
        return _json_safe(format_dict)

    def _error_payload(self, result) -> dict:
        exc = result.error_in_exec or result.error_before_exec
        if exc is None:
            return {"ename": "Error", "evalue": "", "traceback": ["Error"]}
        try:
            payload = self.shell._format_exception_for_storage(exc)
        except Exception:
            payload = {
                "ename": type(exc).__name__,
                "evalue": str(exc),
                "traceback": traceback.format_exception(type(exc), exc, exc.__traceback__),
            }
        if not isinstance(payload, dict):
            payload = {}
        ename = payload.get("ename")
        if not isinstance(ename, str) or not ename:
            ename = type(exc).__name__
        evalue = payload.get("evalue")
        if not isinstance(evalue, str):
            evalue = str(exc)
        tb = payload.get("traceback") or []
        if isinstance(tb, str):
            tb = [tb]
        if not isinstance(tb, list):
            tb = [str(tb)]
        tb = [item if isinstance(item, str) else str(item) for item in tb]
        tb = [item for item in tb if item]
        if not tb:
            tb = [f"{ename}: {evalue}"]
        return {"ename": ename, "evalue": evalue, "traceback": tb}

    def _cell_code(self, result) -> str:
        info = getattr(result, "info", None)
        raw = getattr(info, "raw_cell", None) if info is not None else None
        if raw is None:
            return ""
        return raw if isinstance(raw, str) else str(raw)

    def _record_event(self, result, stdout: str, stderr: str) -> None:
        if self.metadata is None or self.path is None:
            return
        self._seq += 1
        event = {
            "type": "cell",
            "seq": self._seq,
            "recorded_at": _now_iso(),
            "execution_count": self._execution_count(result),
            "code": self._cell_code(result),
            "success": bool(result.success),
            "stdout": stdout,
            "stderr": stderr,
            "execute_result": self._execute_result(result),
        }
        if not result.success:
            event["error"] = self._error_payload(result)
        event = _redact_obj(event, self.redactions)
        self.events.append(event)
        self.metadata["event_count"] = len(self.events)
        self.metadata["redactions"] = list(self.redactions)
        save_session_bundle(self.path, self.metadata, self.events, overwrite=True)
