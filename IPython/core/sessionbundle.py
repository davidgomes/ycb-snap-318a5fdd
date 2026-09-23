"""Record an IPython session to a bundle and replay it later.

A session bundle is a ZIP archive (typically ``*.ipybundle``) with two members:

* ``metadata.json`` — format, versions, and redaction patterns
* ``events.jsonl`` — one JSON object per executed cell
"""

from __future__ import annotations

import json
import platform
import shutil
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
METADATA_NAME = "metadata.json"
EVENTS_NAME = "events.jsonl"

_REQUIRED_METADATA = (
    "format",
    "format_version",
    "created_at",
    "ipython_version",
    "python_version",
    "platform",
    "redactions",
)


class SessionBundleValidationError(Exception):
    """Raised when a session bundle fails strict validation."""

    def __init__(self, bundle_path, errors):
        self.bundle_path = Path(bundle_path).expanduser().resolve()
        self.errors = list(errors)
        detail = "; ".join(self.errors) if self.errors else "invalid session bundle"
        super().__init__(f"{self.bundle_path}: {detail}")


def _apply_patterns(text: str, patterns: list[str]) -> str:
    for pattern in patterns:
        if pattern:
            text = text.replace(pattern, REDACTION_TOKEN)
    return text


def _redact_obj(obj: Any, patterns: list[str]) -> Any:
    """Replace literal patterns in strings. Mapping keys are left unchanged."""
    if not patterns:
        return obj
    if isinstance(obj, str):
        return _apply_patterns(obj, patterns)
    if isinstance(obj, list):
        return [_redact_obj(item, patterns) for item in obj]
    if isinstance(obj, dict):
        return {key: _redact_obj(value, patterns) for key, value in obj.items()}
    return obj


def _normalize_patterns(redact) -> list[str]:
    if redact is None:
        return []
    if isinstance(redact, str):
        return [redact]
    return [str(pattern) for pattern in redact]


def _is_iso8601(value: Any) -> bool:
    if not isinstance(value, str) or not value.strip():
        return False
    candidate = value[:-1] + "+00:00" if value.endswith("Z") else value
    try:
        datetime.fromisoformat(candidate)
    except ValueError:
        return False
    return True


def _is_int(value: Any) -> bool:
    return isinstance(value, int) and not isinstance(value, bool)


def _json_safe(value: Any) -> Any:
    try:
        json.dumps(value)
    except (TypeError, ValueError):
        return str(value)
    return value


def _new_metadata(redactions: list[str]) -> dict[str, Any]:
    return {
        "format": FORMAT_NAME,
        "format_version": FORMAT_VERSION,
        "created_at": datetime.now(timezone.utc).isoformat(),
        "ipython_version": ipython_version,
        "python_version": platform.python_version(),
        "platform": platform.platform(),
        "redactions": list(redactions),
        "event_count": 0,
    }


def _bundle_path(path) -> Path:
    return Path(path).expanduser()


def save_session_bundle(path, meta, events, *, overwrite: bool = False) -> Path:
    """Write ``metadata.json`` and ``events.jsonl`` into a bundle at ``path``.

    Returns the bundle :class:`~pathlib.Path`. Raises :class:`FileExistsError`
    when the target exists and ``overwrite`` is false.
    """
    bundle = _bundle_path(path)
    if bundle.exists() and not overwrite:
        raise FileExistsError(bundle)
    if bundle.exists() and bundle.is_dir():
        shutil.rmtree(bundle)
    if bundle.parent and not bundle.parent.exists():
        bundle.parent.mkdir(parents=True, exist_ok=True)

    lines = [
        json.dumps(event, ensure_ascii=False, separators=(",", ":")) for event in events
    ]
    events_text = ("\n".join(lines) + "\n") if lines else ""
    metadata_text = json.dumps(meta, ensure_ascii=False, indent=2) + "\n"

    with zipfile.ZipFile(bundle, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        archive.writestr(METADATA_NAME, metadata_text)
        archive.writestr(EVENTS_NAME, events_text)
    return bundle.resolve()


def _read_bundle(path) -> tuple[Path, dict[str, Any], list[dict[str, Any]], str]:
    bundle = _bundle_path(path)
    if not bundle.is_file():
        raise FileNotFoundError(f"session bundle not found: {bundle}")
    try:
        archive = zipfile.ZipFile(bundle, "r")
    except zipfile.BadZipFile as exc:
        raise ValueError(f"not a session bundle zip archive: {bundle}") from exc
    with archive:
        names = set(archive.namelist())
        if METADATA_NAME not in names or EVENTS_NAME not in names:
            missing = [
                name
                for name in (METADATA_NAME, EVENTS_NAME)
                if name not in names
            ]
            raise ValueError(
                f"session bundle {bundle} is missing {', '.join(missing)}"
            )
        metadata_raw = archive.read(METADATA_NAME).decode("utf-8")
        events_raw = archive.read(EVENTS_NAME).decode("utf-8")
    try:
        metadata = json.loads(metadata_raw)
    except json.JSONDecodeError as exc:
        raise ValueError(f"metadata.json is not valid JSON: {exc}") from exc
    events: list[dict[str, Any]] = []
    for lineno, line in enumerate(events_raw.splitlines(), start=1):
        if not line.strip():
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ValueError(f"events.jsonl line {lineno} is not valid JSON: {exc}") from exc
        events.append(event)
    return bundle.resolve(), metadata, events, events_raw


def load_session_bundle(path) -> tuple[dict[str, Any], list[Any]]:
    """Load ``(metadata, events)`` from a bundle without executing code."""
    _bundle, metadata, events, _raw = _read_bundle(path)
    return metadata, events


def validate_session_bundle(path, *, strict: bool = True) -> list[str]:
    """Return human-readable schema and invariant errors for a bundle.

    When ``strict`` is true and any errors are found, raise
    :class:`SessionBundleValidationError`. When ``strict`` is false, return
    the errors without raising.
    """
    bundle = _bundle_path(path)
    errors: list[str] = []
    try:
        resolved, metadata, events, events_raw = _read_bundle(bundle)
    except (FileNotFoundError, ValueError, OSError) as exc:
        errors.append(str(exc))
        if strict and errors:
            raise SessionBundleValidationError(bundle, errors) from exc
        return errors

    if not isinstance(metadata, dict):
        errors.append("metadata.json must contain a JSON object")
        metadata = {}

    for key in _REQUIRED_METADATA:
        if key not in metadata:
            errors.append(f"metadata.json is missing required field {key!r}")

    if "format" in metadata and metadata["format"] != FORMAT_NAME:
        errors.append(
            f"metadata.format must be {FORMAT_NAME!r}, got {metadata['format']!r}"
        )

    version = metadata.get("format_version", None)
    if "format_version" in metadata and (not _is_int(version) or version < 1):
        errors.append(
            f"metadata.format_version must be an integer >= 1, got {version!r}"
        )

    if "created_at" in metadata and not _is_iso8601(metadata.get("created_at")):
        errors.append(
            "metadata.created_at must be an ISO-8601 timestamp, "
            f"got {metadata.get('created_at')!r}"
        )

    for key in ("ipython_version", "python_version", "platform"):
        if key in metadata and not isinstance(metadata.get(key), str):
            errors.append(f"metadata.{key} must be a string")

    redactions = metadata.get("redactions", None)
    if "redactions" in metadata and (
        not isinstance(redactions, list)
        or not all(isinstance(item, str) for item in redactions)
    ):
        errors.append("metadata.redactions must be a list of strings")
        redactions = []

    if "event_count" in metadata:
        event_count = metadata.get("event_count")
        if not _is_int(event_count):
            errors.append(
                f"metadata.event_count must be an integer, got {event_count!r}"
            )
        elif event_count != len(events):
            errors.append(
                "metadata.event_count must equal the number of events "
                f"({len(events)}), got {event_count}"
            )

    if not isinstance(events, list):
        errors.append("events.jsonl must contain a list of cell events")
        events = []

    for index, event in enumerate(events, start=1):
        prefix = f"event {index}"
        if not isinstance(event, dict):
            errors.append(f"{prefix} must be a JSON object")
            continue
        if event.get("type") != "cell":
            errors.append(f"{prefix}: type must be 'cell', got {event.get('type')!r}")
        seq = event.get("seq")
        if seq != index:
            errors.append(f"{prefix}: seq must be {index}, got {seq!r}")
        if "recorded_at" not in event or not _is_iso8601(event.get("recorded_at")):
            errors.append(f"{prefix}: recorded_at must be an ISO-8601 timestamp")
        if "execution_count" not in event:
            errors.append(f"{prefix}: missing execution_count")
        else:
            count = event.get("execution_count")
            if count is not None and not _is_int(count):
                errors.append(
                    f"{prefix}: execution_count must be an integer or null, got {count!r}"
                )
        if "code" not in event or not isinstance(event.get("code"), str):
            errors.append(f"{prefix}: code must be a string")
        success = event.get("success", None)
        if not isinstance(success, bool):
            errors.append(f"{prefix}: success must be a boolean")
        for stream in ("stdout", "stderr"):
            if stream not in event or not isinstance(event.get(stream), str):
                errors.append(f"{prefix}: {stream} must be a string")
        execute_result = event.get("execute_result", None)
        if "execute_result" not in event or not isinstance(execute_result, dict):
            errors.append(f"{prefix}: execute_result must be an object")
        elif execute_result:
            plain = execute_result.get("text/plain", None)
            if "text/plain" not in execute_result or not isinstance(plain, str):
                errors.append(
                    f"{prefix}: execute_result must include 'text/plain' as a string "
                    "when it is non-empty"
                )
        if success is False:
            error = event.get("error")
            if not isinstance(error, dict):
                errors.append(
                    f"{prefix}: success is false but error must include ename, "
                    "evalue, and a non-empty traceback"
                )
            else:
                if not isinstance(error.get("ename"), str):
                    errors.append(f"{prefix}: error.ename must be a string")
                if not isinstance(error.get("evalue"), str):
                    errors.append(f"{prefix}: error.evalue must be a string")
                tb = error.get("traceback")
                if (
                    not isinstance(tb, list)
                    or not tb
                    or not all(isinstance(item, str) for item in tb)
                ):
                    errors.append(
                        f"{prefix}: error.traceback must be a non-empty list of strings"
                    )

    if isinstance(redactions, list):
        for pattern in redactions:
            if isinstance(pattern, str) and pattern and pattern in events_raw:
                errors.append(
                    f"redaction pattern {pattern!r} appears in events.jsonl"
                )

    if strict and errors:
        raise SessionBundleValidationError(resolved, errors)
    return errors


def _execute_result_payload(shell, result) -> dict[str, Any]:
    value = getattr(result, "result", None)
    if value is None:
        return {}
    try:
        format_dict, _metadata = shell.display_formatter.format(value)
    except Exception:
        format_dict = {"text/plain": repr(value)}
    if not format_dict:
        return {}
    payload: dict[str, Any] = {}
    for key, item in format_dict.items():
        safe = _json_safe(item)
        if isinstance(safe, str) or safe is item:
            payload[str(key)] = safe
        else:
            payload[str(key)] = safe
    if "text/plain" not in payload:
        payload["text/plain"] = repr(value)
    elif not isinstance(payload["text/plain"], str):
        payload["text/plain"] = str(payload["text/plain"])
    return payload


def _error_payload(shell, result) -> dict[str, Any] | None:
    if result.success:
        return None
    exc = result.error_in_exec or result.error_before_exec
    if exc is None:
        return {
            "ename": "Error",
            "evalue": "",
            "traceback": ["Error: cell failed without an exception"],
        }
    payload = None
    try:
        payload = shell._format_exception_for_storage(exc)
    except Exception:
        payload = None
    if not isinstance(payload, dict):
        payload = {
            "ename": type(exc).__name__,
            "evalue": str(exc),
            "traceback": traceback.format_exception(type(exc), exc, exc.__traceback__),
        }
    ename = payload.get("ename")
    evalue = payload.get("evalue")
    tb = payload.get("traceback")
    if not isinstance(ename, str) or not ename:
        ename = type(exc).__name__
    if not isinstance(evalue, str):
        evalue = str(exc)
    if not isinstance(tb, list):
        tb = []
    tb = [item if isinstance(item, str) else str(item) for item in tb]
    tb = [item for item in tb if item != ""]
    if not tb:
        formatted = traceback.format_exception(type(exc), exc, exc.__traceback__)
        tb = [item for item in formatted if isinstance(item, str) and item]
    if not tb:
        tb = [f"{ename}: {evalue}"]
    return {"ename": ename, "evalue": evalue, "traceback": tb}


class SessionBundleRecorder:
    """Capture cells executed by one :class:`~IPython.core.interactiveshell.InteractiveShell`."""

    def __init__(self, shell):
        self.shell = shell
        self.path: Path | None = None
        self.metadata: dict[str, Any] | None = None
        self.events: list[dict[str, Any]] = []
        self.redactions: list[str] = []
        self._recording = False
        self._frames: list[dict[str, Any]] = []

    def status(self) -> dict[str, Any]:
        if self._recording and self.path is not None:
            return {"recording": True, "path": str(self.path)}
        return {"recording": False, "path": None}

    def start(self, path, *, overwrite: bool = False, redact=None) -> str:
        if self._recording:
            raise RuntimeError(
                f"A session bundle recording is already active: {self.path}"
            )
        patterns = _normalize_patterns(redact)
        metadata = _new_metadata(patterns)
        bundle = save_session_bundle(path, metadata, [], overwrite=overwrite)
        self.path = bundle
        self.metadata = metadata
        self.events = []
        self.redactions = patterns
        self._frames = []
        self._recording = True
        self.shell.events.register("pre_run_cell", self._on_pre_run_cell)
        self.shell.events.register("post_run_cell", self._on_post_run_cell)
        return str(bundle)

    def stop(self) -> str:
        if not self._recording or self.path is None or self.metadata is None:
            raise RuntimeError("No session bundle recording is active")
        self._discard_open_frames()
        self.metadata["event_count"] = len(self.events)
        bundle = save_session_bundle(
            self.path, self.metadata, self.events, overwrite=True
        )
        self._unregister()
        self._recording = False
        saved = str(bundle)
        self.path = None
        self.metadata = None
        self.events = []
        self.redactions = []
        return saved

    def _unregister(self) -> None:
        for event, callback in (
            ("pre_run_cell", self._on_pre_run_cell),
            ("post_run_cell", self._on_post_run_cell),
        ):
            try:
                self.shell.events.unregister(event, callback)
            except (ValueError, KeyError):
                pass

    def _on_pre_run_cell(self, info) -> None:
        if not self._recording:
            return
        frame: dict[str, Any] = {"stdout": [], "stderr": [], "installed": []}
        self._install_capture(frame)
        self._frames.append(frame)

    def _on_post_run_cell(self, result) -> None:
        if not self._recording or not self._frames:
            return
        frame = self._frames.pop()
        self._uninstall_capture(frame)
        if result is None or getattr(result, "info", None) is None:
            return
        if self.metadata is None or self.path is None:
            return
        try:
            event = self._build_event(result, frame)
            self.events.append(event)
            self.metadata["event_count"] = len(self.events)
            save_session_bundle(
                self.path, self.metadata, self.events, overwrite=True
            )
        except Exception:
            traceback.print_exc()

    def _build_event(self, result, frame: dict[str, Any]) -> dict[str, Any]:
        info = result.info
        count = result.execution_count
        if not _is_int(count):
            count = None
        event: dict[str, Any] = {
            "type": "cell",
            "seq": len(self.events) + 1,
            "recorded_at": datetime.now(timezone.utc).isoformat(),
            "execution_count": count,
            "code": info.raw_cell if isinstance(info.raw_cell, str) else str(info.raw_cell),
            "success": bool(result.success),
            "stdout": "".join(frame["stdout"]),
            "stderr": "".join(frame["stderr"]),
            "execute_result": _execute_result_payload(self.shell, result),
        }
        error = _error_payload(self.shell, result)
        if error is not None:
            event["error"] = error
        event = _redact_obj(event, self.redactions)
        # Patterns can collide with structural fields; put those back.
        event["type"] = "cell"
        event["seq"] = len(self.events) + 1
        event["success"] = bool(result.success)
        if not _is_int(event.get("execution_count")) and event.get("execution_count") is not None:
            event["execution_count"] = count
        return event

    def _install_capture(self, frame: dict[str, Any]) -> None:
        shell = self.shell
        for channel, key in (("stdout", "stdout"), ("stderr", "stderr")):
            stream = getattr(sys, channel)
            original = stream.write
            chunks = frame[key]

            def write(data, *args, _original=original, _chunks=chunks, _shell=shell, **kwargs):
                result = _original(data, *args, **kwargs)
                if getattr(_shell.displayhook, "is_active", False):
                    return result
                if getattr(_shell, "showing_traceback", False):
                    return result
                publisher = getattr(_shell, "display_pub", None)
                if publisher is not None and getattr(publisher, "is_publishing", False):
                    return result
                if data:
                    _chunks.append(data if isinstance(data, str) else str(data))
                return result

            stream.write = write
            frame["installed"].append((stream, original))

    def _uninstall_capture(self, frame: dict[str, Any]) -> None:
        for stream, original in reversed(frame["installed"]):
            if getattr(stream, "write", None) is not original:
                stream.write = original
        frame["installed"].clear()

    def _discard_open_frames(self) -> None:
        while self._frames:
            frame = self._frames.pop()
            self._uninstall_capture(frame)


def _recorder(shell) -> SessionBundleRecorder:
    recorder = getattr(shell, "_session_bundle_recorder", None)
    if recorder is None or recorder.shell is not shell:
        recorder = SessionBundleRecorder(shell)
        shell._session_bundle_recorder = recorder
    return recorder


def start_session_bundle(shell, path, *, overwrite: bool = False, redact=None) -> str:
    return _recorder(shell).start(path, overwrite=overwrite, redact=redact)


def stop_session_bundle(shell) -> str:
    return _recorder(shell).stop()


def session_bundle_status(shell) -> dict[str, Any]:
    return _recorder(shell).status()


def replay_session_bundle(
    shell,
    path,
    *,
    stop_on_error: bool = True,
    store_history: bool = True,
):
    """Re-execute recorded cells in ``shell``.

    When ``store_history`` is true, each replayed cell advances
    ``shell.execution_count``. When it is false, the execution count is left
    unchanged. Returns the list of :class:`~IPython.core.interactiveshell.ExecutionResult`
    objects for the cells that were replayed.
    """
    validate_session_bundle(path, strict=True)
    _metadata, events = load_session_bundle(path)
    results = []
    for event in events:
        result = shell.run_cell(event["code"], store_history=store_history)
        results.append(result)
        if stop_on_error and result is not None and not result.success:
            break
    return results


@contextmanager
def session_bundle_recorder(
    shell, path, *, overwrite: bool = False, redact=None
) -> Iterator[str]:
    """Record ``shell`` for the duration of the ``with`` block.

    Equivalent to :meth:`InteractiveShell.start_session_bundle` on enter and
    :meth:`InteractiveShell.stop_session_bundle` on exit.
    """
    bundle = shell.start_session_bundle(path, overwrite=overwrite, redact=redact)
    try:
        yield bundle
    finally:
        if shell.session_bundle_status().get("recording"):
            shell.stop_session_bundle()
