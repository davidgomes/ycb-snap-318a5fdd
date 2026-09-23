"""Record an IPython session to a single file and replay it later.

A session bundle (conventionally ``*.ipybundle``) is a ZIP archive with two
members:

``metadata.json``
    A JSON object describing the recording (format, versions, platform, the
    redaction patterns that were applied, ...).

``events.jsonl``
    One JSON object per line, one line per executed cell, in execution order.
    Each event holds the cell source, whether it succeeded, what it wrote to
    ``sys.stdout`` / ``sys.stderr``, its expression result (as a mimebundle)
    and, on failure, the error details.

Recording is usually driven through the ``%session_bundle`` magic or the
:meth:`~IPython.core.interactiveshell.InteractiveShell.start_session_bundle` /
:meth:`~IPython.core.interactiveshell.InteractiveShell.stop_session_bundle`
methods; this module provides the recorder plus helpers to save, load, validate
and replay bundles.
"""

# Copyright (c) IPython Development Team.
# Distributed under the terms of the Modified BSD License.

from __future__ import annotations

import base64
import json
import os
import platform
import re
import sys
import tempfile
import zipfile
from contextlib import contextmanager
from datetime import datetime, timezone
from pathlib import Path
from typing import TYPE_CHECKING, Any, Callable, Iterable, Iterator, Optional

from IPython.core import release

if TYPE_CHECKING:
    from IPython.core.interactiveshell import (
        ExecutionInfo,
        ExecutionResult,
        InteractiveShell,
    )

__all__ = [
    "BUNDLE_FORMAT",
    "BUNDLE_FORMAT_VERSION",
    "REDACTED",
    "SessionBundleRecorder",
    "SessionBundleValidationError",
    "load_session_bundle",
    "replay_session_bundle",
    "save_session_bundle",
    "session_bundle_recorder",
    "validate_session_bundle",
]

BUNDLE_FORMAT = "ipython-session-bundle"
BUNDLE_FORMAT_VERSION = 1
METADATA_NAME = "metadata.json"
EVENTS_NAME = "events.jsonl"
REDACTED = "<redacted>"

_ANSI_ESCAPE = re.compile(r"\x1b\[[0-9;?]*[ -/]*[@-~]")


class SessionBundleValidationError(ValueError):
    """Raised when a session bundle does not follow the bundle format."""

    def __init__(self, bundle_path: "str | os.PathLike[str]", errors: list[str]):
        self.bundle_path = Path(bundle_path)
        self.errors = list(errors)
        summary = "; ".join(self.errors) if self.errors else "unknown error"
        super().__init__(f"Invalid session bundle {self.bundle_path}: {summary}")


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


def _bundle_path(path: "str | os.PathLike[str]") -> Path:
    return Path(os.fspath(path)).expanduser().absolute()


def _jsonable(obj: Any) -> Any:
    """Convert ``obj`` into something :func:`json.dumps` accepts."""
    if obj is None or isinstance(obj, (str, bool, int, float)):
        return obj
    if isinstance(obj, dict):
        return {str(k): _jsonable(v) for k, v in obj.items()}
    if isinstance(obj, (list, tuple)):
        return [_jsonable(v) for v in obj]
    if isinstance(obj, (bytes, bytearray)):
        return base64.b64encode(bytes(obj)).decode("ascii")
    return str(obj)


class _Redactor:
    """Replace literal patterns with :data:`REDACTED` in every string of an event."""

    # Replacing a pattern can create a new occurrence of another pattern across
    # the boundary with the placeholder, so we repeat, but only a bounded number
    # of times since pathological pattern sets could otherwise loop forever.
    max_passes = 10

    def __init__(self, patterns: Iterable[str]):
        self.patterns = list(patterns)
        self._regex = self._compile(self.patterns)
        self._repeat_regex = self._compile(
            p for p in self.patterns if p not in REDACTED
        )

    @staticmethod
    def _compile(patterns: Iterable[str]) -> Optional[re.Pattern[str]]:
        unique = sorted(set(patterns), key=len, reverse=True)
        if not unique:
            return None
        return re.compile("|".join(re.escape(p) for p in unique))

    def redact_text(self, text: str) -> str:
        if self._regex is None:
            return text
        text = self._regex.sub(REDACTED, text)
        if self._repeat_regex is not None:
            for _ in range(self.max_passes):
                new = self._repeat_regex.sub(REDACTED, text)
                if new == text:
                    break
                text = new
        return text

    def __call__(self, obj: Any) -> Any:
        if self._regex is None:
            return obj
        if isinstance(obj, str):
            return self.redact_text(obj)
        if isinstance(obj, dict):
            return {self(k): self(v) for k, v in obj.items()}
        if isinstance(obj, list):
            return [self(v) for v in obj]
        return obj


def _normalize_redact(redact: "Iterable[str] | str | None") -> list[str]:
    if redact is None:
        return []
    if isinstance(redact, str):
        redact = [redact]
    patterns = []
    for pattern in redact:
        if not isinstance(pattern, str):
            raise TypeError(f"redaction patterns must be strings, got {pattern!r}")
        if not pattern:
            raise ValueError("redaction patterns must be non-empty strings")
        patterns.append(pattern)
    return patterns


def _make_metadata(redactions: list[str]) -> dict[str, Any]:
    return {
        "format": BUNDLE_FORMAT,
        "format_version": BUNDLE_FORMAT_VERSION,
        "created_at": _now(),
        "ipython_version": release.version,
        "python_version": platform.python_version(),
        "platform": platform.platform(),
        "redactions": list(redactions),
    }


def _format_error(shell: InteractiveShell, exc: BaseException) -> dict[str, Any]:
    ename = type(exc).__name__
    evalue = str(exc)
    try:
        stb = shell._format_exception_for_storage(exc)["traceback"]
    except Exception:
        stb = []
    lines = []
    for entry in stb or []:
        lines.append(_ANSI_ESCAPE.sub("", str(entry)))
    if not any(line.strip() for line in lines):
        lines = [f"{ename}: {evalue}" if evalue else ename]
    return {"ename": ename, "evalue": evalue, "traceback": lines}


def _patch_attribute(
    obj: Any, name: str, make_replacement: Callable[[Any], Any]
) -> Optional[Callable[[], None]]:
    """Replace ``obj.name`` on the instance and return a function undoing it.

    Returns ``None`` (and leaves ``obj`` untouched) if the attribute cannot be
    set on the instance.
    """
    try:
        had_instance_attr = name in vars(obj)
    except TypeError:
        had_instance_attr = False
    original = getattr(obj, name)
    try:
        setattr(obj, name, make_replacement(original))
    except (AttributeError, TypeError):
        return None

    def restore() -> None:
        try:
            if had_instance_attr:
                setattr(obj, name, original)
            else:
                delattr(obj, name)
        except (AttributeError, TypeError):
            pass

    return restore


class _CellCapture:
    """Collects the outputs of a single cell while it runs."""

    # Shell methods whose output describes an error; it is recorded in the
    # event's ``error`` rather than in ``stdout`` / ``stderr``.
    _error_display_methods = (
        "showtraceback",
        "_showtraceback",
        "showsyntaxerror",
        "showindentationerror",
    )

    def __init__(self, shell: InteractiveShell, info: ExecutionInfo):
        self.shell = shell
        self.code = info.raw_cell if isinstance(info.raw_cell, str) else ""
        self.stdout: list[str] = []
        self.stderr: list[str] = []
        self.execute_result: dict[str, Any] = {}
        self._restorers: list[Callable[[], None]] = []
        self._showing_error = 0

    def _make_error_display(self, original: Callable[..., Any]) -> Callable[..., Any]:
        def show(*args: Any, **kwargs: Any) -> Any:
            self._showing_error += 1
            try:
                return original(*args, **kwargs)
            finally:
                self._showing_error -= 1

        return show

    def _is_side_channel_output(self) -> bool:
        """Whether the shell itself (not user code) is currently writing."""
        shell = self.shell
        display_pub = getattr(shell, "display_pub", None)
        displayhook = getattr(shell, "displayhook", None)
        return bool(
            self._showing_error
            or getattr(display_pub, "is_publishing", False)
            or getattr(displayhook, "is_active", False)
            or getattr(shell, "showing_traceback", False)
        )

    def _make_write(self, sink: list[str]) -> Callable[[Any], Callable[..., Any]]:
        def make(original_write: Callable[..., Any]) -> Callable[..., Any]:
            def write(data: Any, *args: Any, **kwargs: Any) -> Any:
                result = original_write(data, *args, **kwargs)
                if data and not self._is_side_channel_output():
                    if isinstance(data, bytes):
                        data = data.decode("utf-8", "replace")
                    sink.append(str(data))
                return result

            return write

        return make

    def _make_compute_format_data(
        self, original: Callable[..., Any]
    ) -> Callable[..., Any]:
        def compute_format_data(result: Any) -> Any:
            computed = original(result)
            try:
                format_dict = computed[0]
            except (TypeError, IndexError, KeyError):
                format_dict = None
            if isinstance(format_dict, dict):
                self.execute_result = dict(format_dict)
            return computed

        return compute_format_data

    def install(self) -> None:
        for stream_name, sink in (("stdout", self.stdout), ("stderr", self.stderr)):
            stream = getattr(sys, stream_name, None)
            if stream is None:
                continue
            restore = _patch_attribute(stream, "write", self._make_write(sink))
            if restore is not None:
                self._restorers.append(restore)
        for name in self._error_display_methods:
            if hasattr(self.shell, name):
                restore = _patch_attribute(self.shell, name, self._make_error_display)
                if restore is not None:
                    self._restorers.append(restore)
        displayhook = getattr(self.shell, "displayhook", None)
        if displayhook is not None and hasattr(displayhook, "compute_format_data"):
            restore = _patch_attribute(
                displayhook, "compute_format_data", self._make_compute_format_data
            )
            if restore is not None:
                self._restorers.append(restore)

    def uninstall(self) -> None:
        while self._restorers:
            self._restorers.pop()()

    def to_event(self, seq: int, result: Optional[ExecutionResult]) -> dict[str, Any]:
        execute_result = _jsonable(self.execute_result) if self.execute_result else {}
        if execute_result and not isinstance(execute_result.get("text/plain"), str):
            text = execute_result.get("text/plain")
            execute_result["text/plain"] = "" if text is None else str(text)

        execution_count = getattr(result, "execution_count", None)
        if not isinstance(execution_count, int) or isinstance(execution_count, bool):
            execution_count = None

        event: dict[str, Any] = {
            "type": "cell",
            "seq": seq,
            "recorded_at": _now(),
            "execution_count": execution_count,
            "code": self.code,
            "success": bool(result is not None and result.success),
            "stdout": "".join(self.stdout),
            "stderr": "".join(self.stderr),
            "execute_result": execute_result,
        }
        if not event["success"]:
            exc = None
            if result is not None:
                exc = result.error_before_exec or result.error_in_exec
            if exc is not None:
                event["error"] = _format_error(self.shell, exc)
            else:
                event["error"] = {
                    "ename": "UnknownError",
                    "evalue": "cell execution did not produce a result",
                    "traceback": ["UnknownError: cell execution did not produce a result"],
                }
        return event


class SessionBundleRecorder:
    """Record the cells executed by a shell into a session bundle.

    The bundle on disk is rewritten after every recorded cell, so it is always
    a complete, loadable bundle even if the session ends abruptly.

    Most users should use ``%session_bundle`` or
    :meth:`InteractiveShell.start_session_bundle` rather than this class.
    """

    def __init__(
        self,
        shell: InteractiveShell,
        path: "str | os.PathLike[str]",
        *,
        overwrite: bool = False,
        redact: "Iterable[str] | str | None" = None,
    ):
        self.shell = shell
        self.path = _bundle_path(path)
        self.overwrite = overwrite
        self.redactions = _normalize_redact(redact)
        self._redactor = _Redactor(self.redactions)
        self.metadata: dict[str, Any] = {}
        self.events: list[dict[str, Any]] = []
        self.recording = False
        self._capture: Optional[_CellCapture] = None
        self._nesting = 0

    def start(self) -> Path:
        if self.recording:
            raise RuntimeError(f"Already recording a session bundle to {self.path}")
        if self.path.exists() and not self.overwrite:
            raise FileExistsError(
                f"Session bundle {self.path} already exists; "
                "use overwrite=True (or --overwrite) to replace it"
            )
        self.metadata = _make_metadata(self.redactions)
        self.events = []
        self._write(overwrite=self.overwrite)
        self.shell.events.register("pre_run_cell", self._pre_run_cell)
        self.shell.events.register("post_run_cell", self._post_run_cell)
        self.recording = True
        return self.path

    def stop(self) -> Path:
        if not self.recording:
            raise RuntimeError("No session bundle is being recorded")
        self.recording = False
        for event, callback in (
            ("pre_run_cell", self._pre_run_cell),
            ("post_run_cell", self._post_run_cell),
        ):
            try:
                self.shell.events.unregister(event, callback)
            except ValueError:
                pass
        # When stopped from within a cell (e.g. ``%session_bundle stop``) that
        # cell is still running: drop it without recording.
        self._discard_capture()
        self._write(overwrite=True)
        return self.path

    def _discard_capture(self) -> None:
        if self._capture is not None:
            self._capture.uninstall()
            self._capture = None
        self._nesting = 0

    def _pre_run_cell(self, info: ExecutionInfo) -> None:
        if self._capture is not None:
            # A cell run from within a recorded cell is part of that cell.
            self._nesting += 1
            return
        capture = _CellCapture(self.shell, info)
        capture.install()
        self._capture = capture

    def _post_run_cell(self, result: Optional[ExecutionResult]) -> None:
        capture = self._capture
        if capture is None:
            # The cell started before recording began (e.g. the cell running
            # ``%session_bundle start``); it is not part of the recording.
            return
        if self._nesting:
            self._nesting -= 1
            return
        self._capture = None
        capture.uninstall()
        event = capture.to_event(len(self.events) + 1, result)
        self.events.append(self._redactor(event))
        self._write(overwrite=True)

    def _write(self, *, overwrite: bool) -> None:
        metadata = dict(self.metadata, event_count=len(self.events))
        save_session_bundle(self.path, metadata, self.events, overwrite=overwrite)

    def status(self) -> dict[str, Any]:
        return {"recording": self.recording, "path": str(self.path)}


def save_session_bundle(
    path: "str | os.PathLike[str]",
    meta: dict[str, Any],
    events: Iterable[dict[str, Any]],
    *,
    overwrite: bool = False,
) -> Path:
    """Write ``meta`` and ``events`` to a session bundle at ``path``.

    The bundle is written to a temporary file first and then moved into place,
    so readers never observe a partially written bundle.

    Parameters
    ----------
    path
        Destination of the bundle.
    meta
        Content of ``metadata.json``.
    events
        Events written, one JSON object per line, to ``events.jsonl``.
    overwrite
        Replace ``path`` if it exists. If false and ``path`` exists,
        :class:`FileExistsError` is raised.

    Returns
    -------
    Path of the written bundle.
    """
    bundle = _bundle_path(path)
    if not overwrite and bundle.exists():
        raise FileExistsError(f"Session bundle {bundle} already exists")

    metadata_text = json.dumps(_jsonable(meta), indent=2, ensure_ascii=False) + "\n"
    events_text = "".join(
        json.dumps(_jsonable(event), ensure_ascii=False) + "\n" for event in events
    )

    fd, tmp_name = tempfile.mkstemp(
        prefix=f".{bundle.name}.", suffix=".tmp", dir=bundle.parent
    )
    try:
        with os.fdopen(fd, "wb") as fh:
            with zipfile.ZipFile(fh, "w", compression=zipfile.ZIP_DEFLATED) as zf:
                zf.writestr(METADATA_NAME, metadata_text)
                zf.writestr(EVENTS_NAME, events_text)
        os.replace(tmp_name, bundle)
    except BaseException:
        try:
            os.unlink(tmp_name)
        except OSError:
            pass
        raise
    return bundle


def _read_bundle(
    path: "str | os.PathLike[str]",
) -> tuple[Optional[Any], Optional[str], list[Any], list[str]]:
    """Read a bundle without interpreting it.

    Returns ``(metadata, events_text, events, errors)`` where ``errors`` lists
    problems preventing the bundle from being read or parsed.
    """
    bundle = _bundle_path(path)
    errors: list[str] = []
    if not bundle.exists():
        return None, None, [], [f"{bundle}: file does not exist"]
    try:
        with zipfile.ZipFile(bundle) as zf:
            names = set(zf.namelist())
            members = {}
            for name in (METADATA_NAME, EVENTS_NAME):
                if name not in names:
                    errors.append(f"{name}: missing from bundle")
                else:
                    members[name] = zf.read(name)
    except (zipfile.BadZipFile, OSError) as e:
        return None, None, [], [f"{bundle}: not a readable ZIP archive ({e})"]

    metadata = None
    if METADATA_NAME in members:
        try:
            metadata = json.loads(members[METADATA_NAME].decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError) as e:
            errors.append(f"{METADATA_NAME}: invalid JSON ({e})")

    events_text = None
    events: list[Any] = []
    if EVENTS_NAME in members:
        try:
            events_text = members[EVENTS_NAME].decode("utf-8")
        except UnicodeDecodeError as e:
            errors.append(f"{EVENTS_NAME}: not valid UTF-8 ({e})")
        else:
            lines = events_text.split("\n")
            if lines and lines[-1] == "":
                lines.pop()
            for lineno, line in enumerate(lines, start=1):
                if not line.strip():
                    errors.append(f"{EVENTS_NAME} line {lineno}: empty line")
                    continue
                try:
                    events.append(json.loads(line))
                except json.JSONDecodeError as e:
                    errors.append(f"{EVENTS_NAME} line {lineno}: invalid JSON ({e})")
    return metadata, events_text, events, errors


def load_session_bundle(
    path: "str | os.PathLike[str]",
) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    """Read a session bundle without executing anything.

    Returns
    -------
    (metadata, events)
        The parsed ``metadata.json`` object and the list of events.

    Raises
    ------
    SessionBundleValidationError
        If the bundle cannot be read or parsed. Use
        :func:`validate_session_bundle` for a full schema check.
    """
    metadata, _, events, errors = _read_bundle(path)
    if not errors and not isinstance(metadata, dict):
        errors.append(f"{METADATA_NAME}: must be a JSON object")
    if errors:
        raise SessionBundleValidationError(_bundle_path(path), errors)
    return metadata, events


def _is_int(value: Any) -> bool:
    return isinstance(value, int) and not isinstance(value, bool)


def _is_iso_timestamp(value: Any) -> bool:
    if not isinstance(value, str):
        return False
    try:
        datetime.fromisoformat(value)
    except ValueError:
        return False
    return True


def _validate_metadata(metadata: Any, event_total: int) -> list[str]:
    where = METADATA_NAME
    if not isinstance(metadata, dict):
        return [f"{where}: must be a JSON object"]
    errors = []
    if metadata.get("format") != BUNDLE_FORMAT:
        errors.append(
            f"{where}: 'format' must be {BUNDLE_FORMAT!r}, got {metadata.get('format')!r}"
        )
    version = metadata.get("format_version")
    if not _is_int(version) or version < 1:
        errors.append(f"{where}: 'format_version' must be an integer >= 1, got {version!r}")
    if not _is_iso_timestamp(metadata.get("created_at")):
        errors.append(
            f"{where}: 'created_at' must be an ISO-8601 timestamp, "
            f"got {metadata.get('created_at')!r}"
        )
    for key in ("ipython_version", "python_version", "platform"):
        if not isinstance(metadata.get(key), str):
            errors.append(f"{where}: {key!r} must be a string, got {metadata.get(key)!r}")
    redactions = metadata.get("redactions")
    if not isinstance(redactions, list) or not all(
        isinstance(r, str) for r in redactions
    ):
        errors.append(f"{where}: 'redactions' must be a list of strings, got {redactions!r}")
    if "event_count" in metadata:
        count = metadata["event_count"]
        if not _is_int(count):
            errors.append(f"{where}: 'event_count' must be an integer, got {count!r}")
        elif count != event_total:
            errors.append(
                f"{where}: 'event_count' is {count} but {EVENTS_NAME} "
                f"contains {event_total} events"
            )
    return errors


def _validate_event(event: Any, index: int, expected_seq: int) -> list[str]:
    where = f"{EVENTS_NAME} event {index}"
    if not isinstance(event, dict):
        return [f"{where}: must be a JSON object"]
    errors = []
    if event.get("type") != "cell":
        errors.append(f"{where}: 'type' must be 'cell', got {event.get('type')!r}")
    seq = event.get("seq")
    if not _is_int(seq):
        errors.append(f"{where}: 'seq' must be an integer, got {seq!r}")
    elif seq != expected_seq:
        errors.append(f"{where}: 'seq' must be {expected_seq}, got {seq}")
    if not _is_iso_timestamp(event.get("recorded_at")):
        errors.append(
            f"{where}: 'recorded_at' must be an ISO-8601 timestamp, "
            f"got {event.get('recorded_at')!r}"
        )
    if "execution_count" not in event:
        errors.append(f"{where}: 'execution_count' is missing")
    elif event["execution_count"] is not None and not _is_int(event["execution_count"]):
        errors.append(
            f"{where}: 'execution_count' must be an integer or null, "
            f"got {event['execution_count']!r}"
        )
    for key in ("code", "stdout", "stderr"):
        if not isinstance(event.get(key), str):
            errors.append(f"{where}: {key!r} must be a string, got {event.get(key)!r}")
    success = event.get("success")
    if not isinstance(success, bool):
        errors.append(f"{where}: 'success' must be a boolean, got {success!r}")
    execute_result = event.get("execute_result")
    if not isinstance(execute_result, dict):
        errors.append(
            f"{where}: 'execute_result' must be an object, got {execute_result!r}"
        )
    elif execute_result and not isinstance(execute_result.get("text/plain"), str):
        errors.append(
            f"{where}: non-empty 'execute_result' must have a 'text/plain' string"
        )
    if success is False:
        error = event.get("error")
        if not isinstance(error, dict):
            errors.append(f"{where}: failed cell must have an 'error' object")
        else:
            for key in ("ename", "evalue"):
                if not isinstance(error.get(key), str):
                    errors.append(
                        f"{where}: 'error.{key}' must be a string, got {error.get(key)!r}"
                    )
            tb = error.get("traceback")
            if (
                not isinstance(tb, list)
                or not tb
                or not all(isinstance(line, str) for line in tb)
            ):
                errors.append(
                    f"{where}: 'error.traceback' must be a non-empty list of strings"
                )
    return errors


def validate_session_bundle(
    path: "str | os.PathLike[str]", *, strict: bool = True
) -> list[str]:
    """Check that the bundle at ``path`` follows the session bundle format.

    Parameters
    ----------
    path
        Bundle to check.
    strict
        If true, raise :class:`SessionBundleValidationError` when problems are
        found instead of returning them.

    Returns
    -------
    A list of human-readable descriptions of every problem found; empty if the
    bundle is valid.
    """
    bundle = _bundle_path(path)
    metadata, events_text, events, errors = _read_bundle(bundle)
    if metadata is not None:
        errors.extend(_validate_metadata(metadata, len(events)))
    for index, event in enumerate(events, start=1):
        errors.extend(_validate_event(event, index, expected_seq=index))
    if events_text is not None and isinstance(metadata, dict):
        redactions = metadata.get("redactions")
        if isinstance(redactions, list):
            for pattern in redactions:
                if isinstance(pattern, str) and pattern and pattern in events_text:
                    errors.append(
                        f"{EVENTS_NAME}: contains redacted pattern {pattern!r}"
                    )
    if strict and errors:
        raise SessionBundleValidationError(bundle, errors)
    return errors


def replay_session_bundle(
    shell: InteractiveShell,
    path: "str | os.PathLike[str]",
    *,
    stop_on_error: bool = True,
    store_history: bool = True,
) -> list[ExecutionResult]:
    """Re-execute the cells recorded in a bundle in ``shell``.

    Parameters
    ----------
    shell
        Shell in which the cells are run.
    path
        Bundle to replay.
    stop_on_error
        Stop after the first cell that fails when replayed.
    store_history
        Passed to :meth:`InteractiveShell.run_cell`; when true every replayed
        cell is stored in history and advances ``shell.execution_count``.

    Returns
    -------
    The :class:`~IPython.core.interactiveshell.ExecutionResult` of every
    replayed cell, in order.
    """
    _, events = load_session_bundle(path)
    results = []
    for event in events:
        if not isinstance(event, dict) or event.get("type") != "cell":
            continue
        code = event.get("code")
        if not isinstance(code, str) or not code.strip():
            continue
        result = shell.run_cell(code, store_history=store_history)
        results.append(result)
        if stop_on_error and not result.success:
            break
    return results


@contextmanager
def session_bundle_recorder(
    shell: InteractiveShell,
    path: "str | os.PathLike[str]",
    *,
    overwrite: bool = False,
    redact: "Iterable[str] | str | None" = None,
) -> Iterator[str]:
    """Record the cells run in ``shell`` within a ``with`` block.

    Equivalent to calling :meth:`InteractiveShell.start_session_bundle` on
    enter and :meth:`InteractiveShell.stop_session_bundle` on exit. Yields the
    bundle path.
    """
    bundle = shell.start_session_bundle(path, overwrite=overwrite, redact=redact)
    try:
        yield bundle
    finally:
        status = shell.session_bundle_status()
        if status["recording"] and status["path"] == bundle:
            shell.stop_session_bundle()
