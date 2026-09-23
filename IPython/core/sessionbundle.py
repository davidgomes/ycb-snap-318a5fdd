"""Record an IPython session to a single file and replay it later.

A session bundle (``.ipybundle``) is a ZIP archive with two members:

``metadata.json``
    Information about the recording environment (IPython/Python versions,
    platform, creation time, redaction patterns, ...).

``events.jsonl``
    One JSON object per line, one line per executed cell, in execution order.
    Each event holds the cell source, its captured ``stdout``/``stderr``, the
    expression result (``execute_result``) and error details if it failed.

Recording is controlled with the ``%session_bundle`` magic, with
:meth:`~IPython.core.interactiveshell.InteractiveShell.start_session_bundle` /
:meth:`~IPython.core.interactiveshell.InteractiveShell.stop_session_bundle`, or
with the :func:`session_bundle_recorder` context manager.
"""

from __future__ import annotations

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
from typing import TYPE_CHECKING, Any, Iterable, Iterator, Optional, Union

from IPython.core import release

if TYPE_CHECKING:
    from IPython.core.interactiveshell import ExecutionInfo, ExecutionResult, InteractiveShell

__all__ = [
    "FORMAT_NAME",
    "FORMAT_VERSION",
    "REDACTED",
    "SessionBundleValidationError",
    "load_session_bundle",
    "replay_session_bundle",
    "save_session_bundle",
    "session_bundle_recorder",
    "validate_session_bundle",
]

FORMAT_NAME = "ipython-session-bundle"
FORMAT_VERSION = 1
METADATA_NAME = "metadata.json"
EVENTS_NAME = "events.jsonl"
REDACTED = "<redacted>"

PathLike = Union[str, "os.PathLike[str]"]

_ANSI_RE = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]")


class SessionBundleValidationError(ValueError):
    """Raised by :func:`validate_session_bundle` when a bundle is invalid."""

    def __init__(self, bundle_path: Path, errors: list[str]):
        self.bundle_path = Path(bundle_path)
        self.errors = list(errors)
        details = "\n".join(f"  - {e}" for e in self.errors)
        super().__init__(f"Invalid session bundle {str(self.bundle_path)!r}:\n{details}")


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


def _bundle_path(path: PathLike) -> Path:
    return Path(os.fspath(path)).expanduser().absolute()


def _make_metadata(redactions: list[str]) -> dict[str, Any]:
    return {
        "format": FORMAT_NAME,
        "format_version": FORMAT_VERSION,
        "created_at": _now(),
        "ipython_version": release.version,
        "python_version": platform.python_version(),
        "platform": platform.platform(),
        "redactions": list(redactions),
        "event_count": 0,
    }


def _redact(obj: Any, patterns: list[str]) -> Any:
    if not patterns:
        return obj
    if isinstance(obj, str):
        for pattern in patterns:
            obj = obj.replace(pattern, REDACTED)
        return obj
    if isinstance(obj, dict):
        return {_redact(k, patterns): _redact(v, patterns) for k, v in obj.items()}
    if isinstance(obj, (list, tuple)):
        return [_redact(v, patterns) for v in obj]
    return obj


def save_session_bundle(
    path: PathLike,
    meta: dict[str, Any],
    events: Iterable[dict[str, Any]],
    *,
    overwrite: bool = False,
) -> Path:
    """Write ``meta`` and ``events`` to a session bundle at ``path``.

    The archive is written to a temporary file first and then moved into
    place, so an existing bundle is never left half-written.

    Returns the :class:`~pathlib.Path` of the written bundle.

    Raises
    ------
    FileExistsError
        If ``path`` exists and ``overwrite`` is False.
    """
    target = _bundle_path(path)
    if not overwrite and target.exists():
        raise FileExistsError(f"Session bundle already exists: {str(target)!r}")
    if target.is_dir():
        raise IsADirectoryError(f"Session bundle path is a directory: {str(target)!r}")

    events_text = "".join(
        json.dumps(event, ensure_ascii=False, default=repr) + "\n" for event in events
    )
    meta_text = json.dumps(meta, ensure_ascii=False, indent=2, default=repr) + "\n"

    target.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp_name = tempfile.mkstemp(
        prefix=f".{target.name}.", suffix=".tmp", dir=target.parent
    )
    try:
        with os.fdopen(fd, "wb") as fh:
            with zipfile.ZipFile(fh, "w", compression=zipfile.ZIP_DEFLATED) as zf:
                zf.writestr(METADATA_NAME, meta_text.encode("utf-8"))
                zf.writestr(EVENTS_NAME, events_text.encode("utf-8"))
        os.replace(tmp_name, target)
    except BaseException:
        try:
            os.unlink(tmp_name)
        except OSError:
            pass
        raise
    return target


def _read_members(path: Path) -> tuple[str, str]:
    with zipfile.ZipFile(path, "r") as zf:
        names = set(zf.namelist())
        missing = [n for n in (METADATA_NAME, EVENTS_NAME) if n not in names]
        if missing:
            raise ValueError(
                f"Session bundle {str(path)!r} is missing: {', '.join(missing)}"
            )
        return (
            zf.read(METADATA_NAME).decode("utf-8"),
            zf.read(EVENTS_NAME).decode("utf-8"),
        )


def load_session_bundle(path: PathLike) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    """Read a session bundle without executing anything.

    Returns ``(metadata, events)``.
    """
    bundle = _bundle_path(path)
    meta_text, events_text = _read_members(bundle)
    meta = json.loads(meta_text)
    events = [json.loads(line) for line in events_text.splitlines() if line.strip()]
    return meta, events


def _is_iso8601(value: Any) -> bool:
    if not isinstance(value, str):
        return False
    try:
        # ``fromisoformat`` only accepts a trailing ``Z`` on Python >= 3.11.
        datetime.fromisoformat(value[:-1] + "+00:00" if value.endswith("Z") else value)
    except ValueError:
        return False
    return True


def _is_int(value: Any) -> bool:
    return isinstance(value, int) and not isinstance(value, bool)


def _validate_metadata(meta: Any) -> list[str]:
    if not isinstance(meta, dict):
        return [f"{METADATA_NAME}: must be a JSON object"]
    errors = []
    if meta.get("format") != FORMAT_NAME:
        errors.append(f"{METADATA_NAME}: 'format' must be {FORMAT_NAME!r}")
    version = meta.get("format_version")
    if not _is_int(version) or version < 1:
        errors.append(f"{METADATA_NAME}: 'format_version' must be an integer >= 1")
    if not _is_iso8601(meta.get("created_at")):
        errors.append(f"{METADATA_NAME}: 'created_at' must be an ISO-8601 timestamp")
    for key in ("ipython_version", "python_version", "platform"):
        if not isinstance(meta.get(key), str):
            errors.append(f"{METADATA_NAME}: {key!r} must be a string")
    redactions = meta.get("redactions")
    if not isinstance(redactions, list) or not all(
        isinstance(r, str) for r in redactions
    ):
        errors.append(f"{METADATA_NAME}: 'redactions' must be a list of strings")
    return errors


def _validate_event(event: Any, where: str) -> list[str]:
    if not isinstance(event, dict):
        return [f"{where}: event must be a JSON object"]
    errors = []
    if event.get("type") != "cell":
        errors.append(f"{where}: 'type' must be 'cell'")
    if not _is_int(event.get("seq")):
        errors.append(f"{where}: 'seq' must be an integer")
    if not _is_iso8601(event.get("recorded_at")):
        errors.append(f"{where}: 'recorded_at' must be an ISO-8601 timestamp")
    count = event.get("execution_count", ...)
    if count is ... or not (count is None or _is_int(count)):
        errors.append(f"{where}: 'execution_count' must be an integer or null")
    for key in ("code", "stdout", "stderr"):
        if not isinstance(event.get(key), str):
            errors.append(f"{where}: {key!r} must be a string")
    success = event.get("success")
    if not isinstance(success, bool):
        errors.append(f"{where}: 'success' must be a boolean")

    result = event.get("execute_result")
    if not isinstance(result, dict):
        errors.append(f"{where}: 'execute_result' must be an object")
    elif result and not isinstance(result.get("text/plain"), str):
        errors.append(
            f"{where}: non-empty 'execute_result' must have a string 'text/plain'"
        )

    if success is False:
        error = event.get("error")
        if not isinstance(error, dict):
            errors.append(f"{where}: failed cell must have an 'error' object")
        else:
            for key in ("ename", "evalue"):
                if not isinstance(error.get(key), str):
                    errors.append(f"{where}: 'error.{key}' must be a string")
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


def validate_session_bundle(path: PathLike, *, strict: bool = True) -> list[str]:
    """Check a session bundle against the format schema and invariants.

    Returns a list of human-readable error strings (empty if the bundle is
    valid).

    Raises
    ------
    SessionBundleValidationError
        If ``strict`` is True and any error is found.
    """
    bundle = _bundle_path(path)
    errors: list[str] = []
    meta: Any = None
    events: list[Any] = []
    events_text = ""

    try:
        meta_text, events_text = _read_members(bundle)
    except FileNotFoundError:
        errors.append(f"bundle does not exist: {str(bundle)!r}")
    except zipfile.BadZipFile:
        errors.append("bundle is not a valid ZIP archive")
    except (OSError, ValueError) as e:
        errors.append(str(e))
    else:
        try:
            meta = json.loads(meta_text)
        except ValueError as e:
            errors.append(f"{METADATA_NAME}: invalid JSON ({e})")
        else:
            errors.extend(_validate_metadata(meta))

        for lineno, line in enumerate(events_text.splitlines(), start=1):
            where = f"{EVENTS_NAME} line {lineno}"
            if not line.strip():
                errors.append(f"{where}: blank line")
                continue
            try:
                event = json.loads(line)
            except ValueError as e:
                errors.append(f"{where}: invalid JSON ({e})")
                continue
            events.append(event)
            errors.extend(_validate_event(event, where))

        seqs = [e.get("seq") if isinstance(e, dict) else None for e in events]
        if seqs != list(range(1, len(events) + 1)):
            errors.append(
                f"{EVENTS_NAME}: 'seq' values must start at 1 and be contiguous"
            )

        if isinstance(meta, dict):
            if "event_count" in meta and (
                not _is_int(meta["event_count"]) or meta["event_count"] != len(events)
            ):
                errors.append(
                    f"{METADATA_NAME}: 'event_count' ({meta['event_count']!r}) does not"
                    f" match the number of events ({len(events)})"
                )
            redactions = meta.get("redactions")
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
    path: PathLike,
    *,
    stop_on_error: bool = True,
    store_history: bool = True,
) -> list[ExecutionResult]:
    """Re-execute the cells recorded in a session bundle in ``shell``.

    Cells run in ``seq`` order. With ``store_history=True`` every replayed
    cell advances ``shell.execution_count`` by one; with
    ``store_history=False`` the execution count is left untouched.

    If ``stop_on_error`` is True, replay stops after the first cell that
    fails.

    Returns the list of :class:`~IPython.core.interactiveshell.ExecutionResult`
    for the cells that were executed.
    """
    _, events = load_session_bundle(path)
    events = sorted(
        (e for e in events if e.get("type") == "cell"), key=lambda e: e.get("seq", 0)
    )
    results = []
    for event in events:
        code = event.get("code", "")
        result = shell.run_cell(code, store_history=store_history)
        if store_history and (not code or code.isspace()):
            # run_cell does not count blank cells; keep one count per cell.
            shell.execution_count += 1
        results.append(result)
        if stop_on_error and not result.success:
            break
    return results


class _StreamCapture:
    """Patch ``sys.stdout.write`` / ``sys.stderr.write`` to record writes.

    Writes made while the display hook, the display publisher, or the
    traceback printer are active are not recorded: expression results are
    recorded in ``execute_result`` and errors in ``error``.
    """

    def __init__(self, shell: InteractiveShell):
        self.shell = shell
        self.chunks: dict[str, list[str]] = {"stdout": [], "stderr": []}
        self._patched: list[tuple[Any, Any, Any]] = []

    def _should_skip(self) -> bool:
        shell = self.shell
        return bool(
            getattr(shell.display_pub, "is_publishing", False)
            or getattr(shell.displayhook, "is_active", False)
            or getattr(shell, "showing_traceback", False)
        )

    def start(self) -> None:
        for channel in ("stdout", "stderr"):
            stream = getattr(sys, channel, None)
            if stream is None:
                continue
            try:
                original = stream.write
            except AttributeError:
                continue
            chunks = self.chunks[channel]

            def write(data, *args, _original=original, _chunks=chunks, **kwargs):
                result = _original(data, *args, **kwargs)
                if data and not self._should_skip():
                    if isinstance(data, bytes):
                        data = data.decode("utf-8", "replace")
                    _chunks.append(str(data))
                return result

            try:
                stream.write = write
            except (AttributeError, TypeError):
                continue
            self._patched.append((stream, original, write))

    def stop(self) -> None:
        while self._patched:
            stream, original, write = self._patched.pop()
            if stream.write is write:
                stream.write = original

    def text(self, channel: str) -> str:
        return "".join(self.chunks[channel])


class SessionBundleRecorder:
    """Record executed cells of an :class:`InteractiveShell` to a bundle.

    Hooks into the ``pre_run_cell`` / ``post_run_cell`` events. Only top-level
    cells are recorded: cells run from within another cell (for example by
    ``%run`` or a direct ``shell.run_cell`` call) are part of the outer cell.
    The cell that starts the recording and the one that stops it are not
    recorded.

    The bundle is rewritten after every recorded cell so that an interrupted
    session still leaves a readable bundle behind.
    """

    def __init__(
        self,
        shell: InteractiveShell,
        path: PathLike,
        *,
        overwrite: bool = False,
        redact: Optional[Iterable[str]] = None,
    ):
        if isinstance(redact, str):
            redact = [redact]
        patterns = list(redact or [])
        for pattern in patterns:
            if not isinstance(pattern, str) or not pattern:
                raise ValueError(
                    f"Redaction patterns must be non-empty strings, got {pattern!r}"
                )
        self.shell = shell
        self.path = _bundle_path(path)
        self.overwrite = overwrite
        self.redactions = patterns
        self.metadata = _make_metadata(patterns)
        self.events: list[dict[str, Any]] = []
        self.active = False
        self._cells: list[ExecutionInfo] = []
        self._capture: Optional[_StreamCapture] = None

    def start(self) -> Path:
        save_session_bundle(self.path, self.metadata, [], overwrite=self.overwrite)
        self.shell.events.register("pre_run_cell", self._pre_run_cell)
        self.shell.events.register("post_run_cell", self._post_run_cell)
        self.active = True
        return self.path

    def stop(self) -> Path:
        if not self.active:
            return self.path
        self.active = False
        for event, callback in (
            ("pre_run_cell", self._pre_run_cell),
            ("post_run_cell", self._post_run_cell),
        ):
            try:
                self.shell.events.unregister(event, callback)
            except ValueError:
                pass
        self._stop_capture()
        self._cells.clear()
        self._flush()
        return self.path

    def _stop_capture(self) -> Optional[_StreamCapture]:
        capture, self._capture = self._capture, None
        if capture is not None:
            capture.stop()
        return capture

    def _pre_run_cell(self, info: ExecutionInfo) -> None:
        if not self.active:
            return
        self._cells.append(info)
        if len(self._cells) == 1:
            self._capture = _StreamCapture(self.shell)
            self._capture.start()

    def _post_run_cell(self, result: Optional[ExecutionResult]) -> None:
        if not self.active or not self._cells or result is None:
            return
        info = getattr(result, "info", None)
        top = self._cells[-1]
        if info is not top and getattr(info, "raw_cell", None) != top.raw_cell:
            # post_run_cell for a cell whose pre_run_cell we did not see
            # (e.g. an empty nested cell).
            return
        self._cells.pop()
        if self._cells:
            return
        capture = self._stop_capture()
        assert capture is not None
        self._record(top, result, capture)

    def _format_execute_result(self, value: Any) -> dict[str, Any]:
        if value is None:
            return {}
        data: dict[str, Any] = {}
        try:
            format_dict, _ = self.shell.display_formatter.format(value)
        except Exception:
            format_dict = {}
        for mime, content in (format_dict or {}).items():
            if isinstance(content, bytes):
                continue
            try:
                json.dumps(content)
            except (TypeError, ValueError):
                continue
            data[mime] = content
        if not isinstance(data.get("text/plain"), str):
            try:
                data["text/plain"] = repr(value)
            except Exception:
                data["text/plain"] = ""
        return data

    def _format_error(self, exc: BaseException) -> dict[str, Any]:
        try:
            error = self.shell._format_exception_for_storage(exc)
        except Exception:
            error = {}
        ename = error.get("ename") or type(exc).__name__
        evalue = error.get("evalue")
        if not isinstance(evalue, str):
            evalue = str(exc)
        traceback = [
            _ANSI_RE.sub("", line)
            for line in (error.get("traceback") or [])
            if isinstance(line, str)
        ]
        if not traceback:
            traceback = [f"{ename}: {evalue}" if evalue else ename]
        return {"ename": ename, "evalue": evalue, "traceback": traceback}

    def _record(
        self, info: ExecutionInfo, result: ExecutionResult, capture: _StreamCapture
    ) -> None:
        code = info.raw_cell if isinstance(info.raw_cell, str) else ""
        if not code.strip():
            return
        event: dict[str, Any] = {
            "type": "cell",
            "seq": len(self.events) + 1,
            "recorded_at": _now(),
            "execution_count": result.execution_count,
            "code": code,
            "success": bool(result.success),
            "stdout": capture.text("stdout"),
            "stderr": capture.text("stderr"),
            "execute_result": self._format_execute_result(result.result),
        }
        exc = result.error_before_exec or result.error_in_exec
        if not event["success"]:
            if exc is not None:
                event["error"] = self._format_error(exc)
            else:
                event["error"] = {
                    "ename": "Error",
                    "evalue": "",
                    "traceback": ["Error: cell execution failed"],
                }
        self.events.append(_redact(event, self.redactions))
        self._flush()

    def _flush(self) -> None:
        self.metadata["event_count"] = len(self.events)
        save_session_bundle(self.path, self.metadata, self.events, overwrite=True)


@contextmanager
def session_bundle_recorder(
    shell: InteractiveShell,
    path: PathLike,
    *,
    overwrite: bool = False,
    redact: Optional[Iterable[str]] = None,
) -> Iterator[str]:
    """Record cells run in ``shell`` to a bundle for the duration of a block.

    Equivalent to calling ``shell.start_session_bundle(path, ...)`` on enter
    and ``shell.stop_session_bundle()`` on exit. Yields the bundle path.
    """
    bundle = shell.start_session_bundle(path, overwrite=overwrite, redact=redact)
    try:
        yield bundle
    finally:
        shell.stop_session_bundle()
