"""Record an IPython session to a bundle and replay it later.

A bundle is a ZIP archive (typically ``*.ipybundle``) with two members:

* ``metadata.json`` — session metadata
* ``events.jsonl`` — one JSON object per line, in execution order

Bundles can be loaded without executing any recorded code, validated, and
replayed into a shell.
"""

from __future__ import annotations

import errno
import json
import os
import platform
import shutil
import sys
import tempfile
import traceback
import zipfile
from contextlib import contextmanager
from datetime import datetime, timezone
from pathlib import Path

from IPython.core.release import __version__ as IPYTHON_VERSION

FORMAT_NAME = "ipython-session-bundle"
FORMAT_VERSION = 1
METADATA_NAME = "metadata.json"
EVENTS_NAME = "events.jsonl"
REDACTED = "<redacted>"

__all__ = [
    "FORMAT_NAME",
    "FORMAT_VERSION",
    "SessionBundleValidationError",
    "load_session_bundle",
    "replay_session_bundle",
    "save_session_bundle",
    "session_bundle_recorder",
    "validate_session_bundle",
]


class SessionBundleValidationError(Exception):
    """Raised by :func:`validate_session_bundle` when ``strict`` is true.

    Attributes
    ----------
    bundle_path : pathlib.Path
        Bundle that failed validation.
    errors : list of str
        Human-readable descriptions of the schema or invariant violations.
    """

    def __init__(self, bundle_path, errors):
        self.bundle_path = Path(bundle_path)
        self.errors = list(errors)
        detail = "; ".join(self.errors) if self.errors else "unknown error"
        super().__init__(f"Invalid session bundle at {self.bundle_path}: {detail}")


def _normalize_path(path) -> Path:
    return Path(path).expanduser()


def _now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def _is_int(value) -> bool:
    return isinstance(value, int) and not isinstance(value, bool)


def _is_iso8601(value) -> bool:
    if not isinstance(value, str):
        return False
    candidate = value.strip()
    if not candidate:
        return False
    if candidate.endswith(("Z", "z")):
        candidate = candidate[:-1] + "+00:00"
    try:
        datetime.fromisoformat(candidate)
    except ValueError:
        return False
    return True


def _normalize_redactions(redact) -> list[str]:
    if redact is None:
        return []
    if isinstance(redact, str):
        return [redact]
    if isinstance(redact, bytes):
        return [redact.decode("utf-8", "replace")]
    patterns: list[str] = []
    for pattern in redact:
        if isinstance(pattern, bytes):
            patterns.append(pattern.decode("utf-8", "replace"))
        elif isinstance(pattern, str):
            patterns.append(pattern)
        else:
            patterns.append(str(pattern))
    return patterns


def _redact_text(text: str, patterns: list[str]) -> str:
    for pattern in patterns:
        if pattern:
            text = text.replace(pattern, REDACTED)
    return text


def _redact_value(value, patterns: list[str]):
    """Replace literal ``patterns`` in every string of a JSON-like structure."""
    if not patterns:
        return value
    if isinstance(value, str):
        return _redact_text(value, patterns)
    if isinstance(value, list):
        return [_redact_value(item, patterns) for item in value]
    if isinstance(value, dict):
        redacted = {}
        for key, item in value.items():
            new_key = _redact_text(key, patterns) if isinstance(key, str) else key
            redacted[new_key] = _redact_value(item, patterns)
        return redacted
    return value


def _apply_redactions(event: dict, patterns: list[str]) -> dict:
    """Redact an event, including any literal leftover in its JSON form.

    String values are replaced first so the encoded line stays valid JSON.
    A second pass scrubs the serialized line when that still parses, which
    covers secrets that only show up once encoded.
    """
    if not patterns or not any(patterns):
        return event
    redacted = _redact_value(event, patterns)
    if not isinstance(redacted, dict):
        return event
    line = json.dumps(redacted, ensure_ascii=False)
    scrubbed = _redact_text(line, patterns)
    if scrubbed == line:
        return redacted
    try:
        parsed = json.loads(scrubbed)
    except json.JSONDecodeError:
        return redacted
    if isinstance(parsed, dict):
        return parsed
    return redacted


def _file_exists_error(path: Path) -> FileExistsError:
    return FileExistsError(
        errno.EEXIST,
        f"File exists: {path}",
        str(path),
    )


def _prepare_destination(path: Path, *, overwrite: bool) -> None:
    """Reject an existing target, or make an overwrite possible.

    Any existing path raises ``FileExistsError`` when ``overwrite`` is false,
    including directories. With ``overwrite``, a file is left in place so it
    can be replaced atomically; a directory is removed so a bundle file can
    be written at that path.
    """
    if not path.exists():
        return
    if not overwrite:
        raise _file_exists_error(path)
    if path.is_dir():
        resolved = path.resolve()
        root = Path(path.anchor).resolve()
        if resolved == root:
            raise _file_exists_error(path)
        shutil.rmtree(path)


def _coerce_text(data) -> str:
    if isinstance(data, str):
        return data
    if isinstance(data, bytes):
        return data.decode("utf-8", "replace")
    return str(data)


def _execute_result_payload(shell, result) -> dict:
    value = getattr(result, "result", None)
    if value is None:
        return {}
    try:
        format_dict, _metadata = shell.display_formatter.format(value)
    except Exception:
        format_dict = {"text/plain": repr(value)}
    if not isinstance(format_dict, dict):
        format_dict = {"text/plain": repr(value)}
    payload: dict = {}
    for key, item in format_dict.items():
        if not isinstance(key, str):
            continue
        if isinstance(item, str):
            payload[key] = item
            continue
        try:
            json.dumps(item)
        except TypeError:
            payload[key] = str(item)
        else:
            payload[key] = item
    plain = payload.get("text/plain")
    if not isinstance(plain, str):
        payload["text/plain"] = repr(value)
    return payload


def _error_payload(result) -> dict:
    exc = getattr(result, "error_in_exec", None) or getattr(
        result, "error_before_exec", None
    )
    if not isinstance(exc, BaseException):
        text = "execution failed" if exc is None else str(exc)
        return {
            "ename": "Error" if exc is None else type(exc).__name__,
            "evalue": text,
            "traceback": [text if text.endswith("\n") else text + "\n"],
        }
    lines = [
        line if isinstance(line, str) else str(line)
        for line in traceback.format_exception(type(exc), exc, exc.__traceback__)
    ]
    lines = [line for line in lines if line]
    if not lines:
        lines = [f"{type(exc).__name__}: {exc}\n"]
    return {
        "ename": type(exc).__name__,
        "evalue": str(exc),
        "traceback": lines,
    }


def load_session_bundle(path):
    """Load a session bundle without executing recorded code.

    Parameters
    ----------
    path : str or pathlib.Path
        Bundle archive to read.

    Returns
    -------
    tuple
        ``(metadata, events)`` where ``metadata`` is the decoded
        ``metadata.json`` object and ``events`` is a list of event objects
        from ``events.jsonl``, in file order.
    """
    bundle_path = _normalize_path(path)
    with zipfile.ZipFile(bundle_path) as archive:
        metadata = json.loads(archive.read(METADATA_NAME).decode("utf-8"))
        events_text = archive.read(EVENTS_NAME).decode("utf-8")
    events = []
    for line in events_text.splitlines():
        if not line.strip():
            continue
        events.append(json.loads(line))
    return metadata, events


def save_session_bundle(path, meta, events, *, overwrite=False) -> Path:
    """Write ``metadata.json`` and ``events.jsonl`` into a bundle at ``path``.

    Parameters
    ----------
    path : str or pathlib.Path
        Destination archive. Parent directories are created when needed.
    meta : dict
        JSON-serializable metadata object written to ``metadata.json``.
    events : sequence
        JSON-serializable event objects written to ``events.jsonl``, one per line.
    overwrite : bool, keyword-only
        When false, raise ``FileExistsError`` if ``path`` already exists.

    Returns
    -------
    pathlib.Path
        Path of the bundle that was written.
    """
    bundle_path = _normalize_path(path)
    _prepare_destination(bundle_path, overwrite=overwrite)
    bundle_path.parent.mkdir(parents=True, exist_ok=True)
    metadata_text = json.dumps(meta, indent=2, ensure_ascii=False) + "\n"
    event_lines = [
        json.dumps(event, ensure_ascii=False) for event in events
    ]
    events_text = ("\n".join(event_lines) + "\n") if event_lines else ""
    _write_bundle_members(
        bundle_path,
        {
            METADATA_NAME: metadata_text.encode("utf-8"),
            EVENTS_NAME: events_text.encode("utf-8"),
        },
    )
    return bundle_path


def _write_bundle_members(bundle_path: Path, members: dict[str, bytes]) -> None:
    temporary = tempfile.NamedTemporaryFile(
        prefix=f".{bundle_path.name}.",
        suffix=".tmp",
        dir=bundle_path.parent,
        delete=False,
    )
    temporary_path = Path(temporary.name)
    try:
        with temporary:
            with zipfile.ZipFile(
                temporary,
                mode="w",
                compression=zipfile.ZIP_DEFLATED,
            ) as archive:
                for name, payload in members.items():
                    archive.writestr(name, payload)
        os.replace(temporary_path, bundle_path)
    except Exception:
        try:
            temporary_path.unlink()
        except OSError:
            pass
        raise


def validate_session_bundle(path, *, strict=True):
    """Validate a session bundle.

    Parameters
    ----------
    path : str or pathlib.Path
        Bundle archive to validate.
    strict : bool, keyword-only
        When true and any problems are found, raise
        :class:`SessionBundleValidationError`. When false, return the error
        list without raising.

    Returns
    -------
    list of str
        Human-readable error messages. Empty when the bundle is valid.
    """
    bundle_path = _normalize_path(path)
    errors = _collect_validation_errors(bundle_path)
    if strict and errors:
        raise SessionBundleValidationError(bundle_path, errors)
    return errors


def _collect_validation_errors(bundle_path: Path) -> list[str]:
    if not bundle_path.exists():
        return [f"session bundle does not exist: {bundle_path}"]
    if bundle_path.is_dir():
        return [f"session bundle path is a directory: {bundle_path}"]
    try:
        archive = zipfile.ZipFile(bundle_path)
    except zipfile.BadZipFile:
        return [f"session bundle is not a valid ZIP archive: {bundle_path}"]
    except OSError as exc:
        return [f"session bundle cannot be opened: {exc}"]

    errors: list[str] = []
    metadata = None
    events_text = None
    with archive:
        names = set(archive.namelist())
        if METADATA_NAME not in names:
            errors.append(f"missing {METADATA_NAME}")
        else:
            metadata, metadata_errors = _read_json_member(archive, METADATA_NAME)
            errors.extend(metadata_errors)
        if EVENTS_NAME not in names:
            errors.append(f"missing {EVENTS_NAME}")
        else:
            try:
                events_text = archive.read(EVENTS_NAME).decode("utf-8")
            except UnicodeDecodeError:
                errors.append(f"{EVENTS_NAME} is not valid UTF-8")

    event_slots: list[tuple[int, object]] = []
    event_line_count = 0
    if events_text is not None:
        for lineno, line in enumerate(events_text.splitlines(), start=1):
            if not line.strip():
                continue
            event_line_count += 1
            try:
                event_slots.append((lineno, json.loads(line)))
            except json.JSONDecodeError as exc:
                errors.append(
                    f"{EVENTS_NAME} line {lineno} is not valid JSON: {exc.msg}"
                )

    if isinstance(metadata, dict):
        _validate_metadata(metadata, event_line_count, errors)
    elif metadata is not None:
        errors.append(f"{METADATA_NAME} must contain a JSON object")

    parsed_index = 0
    for lineno, event in event_slots:
        parsed_index += 1
        _validate_event(event, parsed_index, lineno, errors)
    return errors


def _read_json_member(archive: zipfile.ZipFile, name: str):
    try:
        raw = archive.read(name)
    except OSError as exc:
        return None, [f"{name} cannot be read: {exc}"]
    try:
        text = raw.decode("utf-8")
    except UnicodeDecodeError:
        return None, [f"{name} is not valid UTF-8"]
    try:
        return json.loads(text), []
    except json.JSONDecodeError as exc:
        return None, [f"{name} is not valid JSON: {exc.msg}"]


def _validate_metadata(metadata: dict, event_line_count: int, errors: list[str]) -> None:
    if metadata.get("format") != FORMAT_NAME:
        errors.append(
            "metadata.format must be "
            f"{FORMAT_NAME!r}, got {metadata.get('format')!r}"
        )
    if "format_version" not in metadata:
        errors.append("metadata.format_version is required")
    else:
        version = metadata["format_version"]
        if not _is_int(version) or version < 1:
            errors.append("metadata.format_version must be an integer >= 1")
    if "created_at" not in metadata:
        errors.append("metadata.created_at is required")
    elif not _is_iso8601(metadata["created_at"]):
        errors.append("metadata.created_at must be an ISO-8601 timestamp")
    for key in ("ipython_version", "python_version", "platform"):
        if key not in metadata:
            errors.append(f"metadata.{key} is required")
        elif not isinstance(metadata[key], str):
            errors.append(f"metadata.{key} must be a string")
    if "redactions" not in metadata:
        errors.append("metadata.redactions is required")
    else:
        redactions = metadata["redactions"]
        if not isinstance(redactions, list) or any(
            not isinstance(item, str) for item in redactions
        ):
            errors.append("metadata.redactions must be a list of strings")
    if "event_count" in metadata:
        count = metadata["event_count"]
        if not _is_int(count):
            errors.append("metadata.event_count must be an integer")
        elif count != event_line_count:
            errors.append(
                "metadata.event_count must equal the number of events "
                f"({event_line_count}), got {count}"
            )


def _validate_event(event, index: int, lineno: int, errors: list[str]) -> None:
    label = f"{EVENTS_NAME} line {lineno}"
    if not isinstance(event, dict):
        errors.append(f"{label}: event must be a JSON object")
        return
    if event.get("type") != "cell":
        errors.append(f"{label}: type must be 'cell', got {event.get('type')!r}")
    if event.get("seq") != index:
        errors.append(
            f"{label}: seq must be {index} "
            "(contiguous, starting at 1, in execution order), "
            f"got {event.get('seq')!r}"
        )
    if "recorded_at" not in event:
        errors.append(f"{label}: recorded_at is required")
    elif not _is_iso8601(event["recorded_at"]):
        errors.append(f"{label}: recorded_at must be an ISO-8601 timestamp")
    if "execution_count" not in event:
        errors.append(f"{label}: execution_count is required")
    elif not (event["execution_count"] is None or _is_int(event["execution_count"])):
        errors.append(f"{label}: execution_count must be an integer or null")
    for key in ("code", "stdout", "stderr"):
        if key not in event:
            errors.append(f"{label}: {key} is required")
        elif not isinstance(event[key], str):
            errors.append(f"{label}: {key} must be a string")
    if "success" not in event:
        errors.append(f"{label}: success is required")
        success = None
    elif not isinstance(event["success"], bool):
        errors.append(f"{label}: success must be a boolean")
        success = None
    else:
        success = event["success"]
    if "execute_result" not in event:
        errors.append(f"{label}: execute_result is required")
    else:
        execute_result = event["execute_result"]
        if not isinstance(execute_result, dict):
            errors.append(f"{label}: execute_result must be an object")
        elif execute_result and not isinstance(execute_result.get("text/plain"), str):
            errors.append(
                f"{label}: execute_result must include 'text/plain' as a string "
                "when non-empty"
            )
    if success is False and "error" not in event:
        errors.append(f"{label}: error is required when success is false")
    if "error" in event:
        _validate_error(event["error"], label, errors)


def _validate_error(error, label: str, errors: list[str]) -> None:
    if not isinstance(error, dict):
        errors.append(f"{label}: error must be an object")
        return
    for key in ("ename", "evalue"):
        if key not in error:
            errors.append(f"{label}: error.{key} is required")
        elif not isinstance(error[key], str):
            errors.append(f"{label}: error.{key} must be a string")
    if "traceback" not in error:
        errors.append(f"{label}: error.traceback is required")
        return
    traceback_lines = error["traceback"]
    if (
        not isinstance(traceback_lines, list)
        or not traceback_lines
        or any(not isinstance(line, str) for line in traceback_lines)
    ):
        errors.append(
            f"{label}: error.traceback must be a non-empty list of strings"
        )


def replay_session_bundle(shell, path, *, stop_on_error=True, store_history=True):
    """Re-execute the cells recorded in a session bundle.

    Parameters
    ----------
    shell : InteractiveShell
        Shell that runs each recorded cell.
    path : str or pathlib.Path
        Bundle archive to replay.
    stop_on_error : bool, keyword-only
        Stop after the first cell that fails. Default True.
    store_history : bool, keyword-only
        When true, each replayed cell advances ``shell.execution_count`` once.
        When false, replay does not advance it. Default True.

    Returns
    -------
    list
        :class:`~IPython.core.interactiveshell.ExecutionResult` for every cell
        that was replayed, including a failing cell that stopped the replay.
    """
    _metadata, events = load_session_bundle(path)
    results = []
    for event in events:
        if not isinstance(event, dict) or event.get("type") != "cell":
            continue
        code = event.get("code", "")
        if not isinstance(code, str):
            code = "" if code is None else str(code)
        # Empty cells return before IPython advances execution_count. The
        # replay contract is still one increment per cell when history is stored.
        before = shell.execution_count
        result = shell.run_cell(code, store_history=store_history)
        if store_history and shell.execution_count == before:
            shell.execution_count = before + 1
        results.append(result)
        if stop_on_error and (result is None or not result.success):
            break
    return results


@contextmanager
def session_bundle_recorder(shell, path, *, overwrite=False, redact=None):
    """Record ``shell`` for the duration of the ``with`` block.

    Equivalent to calling ``start_session_bundle`` on enter and
    ``stop_session_bundle`` on exit. ``overwrite`` and ``redact`` are passed
    through to ``start_session_bundle``. The bundle path is yielded.
    """
    bundle_path = shell.start_session_bundle(
        path, overwrite=overwrite, redact=redact
    )
    try:
        yield bundle_path
    finally:
        status = shell.session_bundle_status()
        if status["recording"]:
            shell.stop_session_bundle()


class SessionBundleRecorder:
    """Record cells executed by one :class:`~IPython.core.interactiveshell.InteractiveShell`."""

    def __init__(self, shell):
        self.shell = shell
        self.recording = False
        self._path: str | None = None
        self._bundle_path: Path | None = None
        self.redactions: list[str] = []
        self.created_at: str | None = None
        self.events: list[dict] = []
        self._seq = 0
        self._depth = 0
        self._tb_depth = 0
        self._stdout_chunks: list[str] = []
        self._stderr_chunks: list[str] = []
        self._patches: list[tuple] = []
        self._callbacks_on = False
        self._tb_wrapped = False
        self._tb_original = None
        self._tb_wrapper = None

    def start(self, path, *, overwrite=False, redact=None) -> str:
        if self.recording:
            raise RuntimeError(
                f"A session bundle recording is already active: {self._path}"
            )
        bundle_path = _normalize_path(path)
        _prepare_destination(bundle_path, overwrite=overwrite)
        bundle_path.parent.mkdir(parents=True, exist_ok=True)

        self.redactions = _normalize_redactions(redact)
        self.created_at = _now_iso()
        self.events = []
        self._seq = 0
        self._path = str(bundle_path)
        self._bundle_path = bundle_path
        self._reset_capture_state()
        self.recording = True
        try:
            self._install_traceback_wrapper()
            self._ensure_callbacks()
            # Replace any previous archive immediately so overwrite starts fresh.
            save_session_bundle(
                bundle_path,
                self._build_metadata(),
                [],
                overwrite=True,
            )
        except Exception:
            self.recording = False
            self._remove_callbacks()
            self._reset_capture_state()
            self._restore_traceback_wrapper()
            self._path = None
            self._bundle_path = None
            raise
        return self._path

    def stop(self) -> str:
        if not self.recording or self._bundle_path is None or self._path is None:
            raise RuntimeError("No session bundle recording is active")
        path = self._path
        bundle_path = self._bundle_path
        metadata = self._build_metadata()
        events = list(self.events)
        self.recording = False
        self._remove_callbacks()
        self._reset_capture_state()
        self._restore_traceback_wrapper()
        try:
            save_session_bundle(bundle_path, metadata, events, overwrite=True)
        except Exception:
            self.recording = True
            self._install_traceback_wrapper()
            self._ensure_callbacks()
            raise
        self.events = []
        self._path = None
        self._bundle_path = None
        self.redactions = []
        self.created_at = None
        return path

    def status(self) -> dict:
        if not self.recording:
            return {"recording": False, "path": None}
        return {"recording": True, "path": self._path}

    def _build_metadata(self) -> dict:
        return {
            "format": FORMAT_NAME,
            "format_version": FORMAT_VERSION,
            "created_at": self.created_at or _now_iso(),
            "ipython_version": IPYTHON_VERSION,
            "python_version": platform.python_version(),
            "platform": platform.platform(),
            "redactions": list(self.redactions),
            "event_count": len(self.events),
        }

    def _ensure_callbacks(self) -> None:
        if self._callbacks_on:
            return
        self.shell.events.register("pre_run_cell", self._on_pre_run_cell)
        self.shell.events.register("post_run_cell", self._on_post_run_cell)
        self._callbacks_on = True

    def _remove_callbacks(self) -> None:
        if not self._callbacks_on:
            return
        for event_name, callback in (
            ("pre_run_cell", self._on_pre_run_cell),
            ("post_run_cell", self._on_post_run_cell),
        ):
            try:
                self.shell.events.unregister(event_name, callback)
            except ValueError:
                pass
        self._callbacks_on = False

    def _reset_capture_state(self) -> None:
        self._uninstall_stream_patches()
        self._depth = 0
        self._tb_depth = 0
        self._stdout_chunks = []
        self._stderr_chunks = []

    def _install_traceback_wrapper(self) -> None:
        if self._tb_wrapped:
            return
        original = self.shell._showtraceback

        def _showtraceback(etype, evalue, stb):
            self._tb_depth += 1
            try:
                return original(etype, evalue, stb)
            finally:
                self._tb_depth -= 1

        self._tb_original = original
        self._tb_wrapper = _showtraceback
        self.shell._showtraceback = _showtraceback
        self._tb_wrapped = True

    def _restore_traceback_wrapper(self) -> None:
        if not self._tb_wrapped:
            return
        if getattr(self.shell, "_showtraceback", None) is self._tb_wrapper:
            self.shell._showtraceback = self._tb_original
        self._tb_wrapped = False
        self._tb_wrapper = None
        self._tb_original = None

    def _install_stream_patches(self) -> None:
        self._patch_stream(sys.stdout, self._stdout_chunks, filter_display=True)
        self._patch_stream(sys.stderr, self._stderr_chunks, filter_display=False)

    def _patch_stream(self, stream, chunks: list[str], *, filter_display: bool) -> None:
        original = stream.write

        def write(data, *args, **kwargs):
            result = original(data, *args, **kwargs)
            if filter_display and self._should_skip_stdout():
                return result
            if data:
                chunks.append(_coerce_text(data))
            return result

        stream.write = write
        self._patches.append((stream, original, write))

    def _uninstall_stream_patches(self) -> None:
        for stream, original, wrapper in reversed(self._patches):
            if getattr(stream, "write", None) is wrapper:
                stream.write = original
        self._patches = []

    def _should_skip_stdout(self) -> bool:
        if self._tb_depth > 0 or getattr(self.shell, "showing_traceback", False):
            return True
        displayhook = getattr(self.shell, "displayhook", None)
        if displayhook is not None and getattr(displayhook, "is_active", False):
            return True
        publisher = getattr(self.shell, "display_pub", None)
        if publisher is not None and getattr(publisher, "is_publishing", False):
            return True
        return False

    def _on_pre_run_cell(self, _info) -> None:
        if not self.recording:
            return
        self._depth += 1
        if self._depth == 1:
            self._stdout_chunks = []
            self._stderr_chunks = []
            self._install_stream_patches()

    def _on_post_run_cell(self, result) -> None:
        if self._depth <= 0:
            return
        self._depth -= 1
        if self._depth != 0:
            return
        try:
            stdout = "".join(self._stdout_chunks)
            stderr = "".join(self._stderr_chunks)
            if self.recording:
                self._record_cell(result, stdout, stderr)
        finally:
            self._uninstall_stream_patches()
            self._stdout_chunks = []
            self._stderr_chunks = []

    def _record_cell(self, result, stdout: str, stderr: str) -> None:
        if result is None:
            return
        info = getattr(result, "info", None)
        raw = getattr(info, "raw_cell", "") if info is not None else ""
        if not isinstance(raw, str) or not raw.strip():
            return
        success = bool(result.success)
        count = getattr(result, "execution_count", None)
        if not _is_int(count):
            count = None
        event = {
            "type": "cell",
            "seq": self._seq + 1,
            "recorded_at": _now_iso(),
            "execution_count": count,
            "code": raw,
            "success": success,
            "stdout": stdout if isinstance(stdout, str) else _coerce_text(stdout),
            "stderr": stderr if isinstance(stderr, str) else _coerce_text(stderr),
            "execute_result": _execute_result_payload(self.shell, result),
        }
        if not success:
            event["error"] = _error_payload(result)
        event = _apply_redactions(event, self.redactions)
        self._seq += 1
        self.events.append(event)
        self._persist()

    def _persist(self) -> None:
        """Write the events recorded so far into the bundle archive."""
        if self._bundle_path is None:
            return
        save_session_bundle(
            self._bundle_path,
            self._build_metadata(),
            self.events,
            overwrite=True,
        )
