"""Record an IPython session to a single file and replay it later.

A session bundle (conventionally a ``.ipybundle`` file) is a ZIP archive with
two members:

``metadata.json``
    Describes the recording: ``format``, ``format_version``, ``created_at``,
    ``ipython_version``, ``python_version``, ``platform``, the literal strings
    that were redacted (``redactions``) and the number of recorded events
    (``event_count``).

``events.jsonl``
    One JSON object per line and per executed cell, in execution order, with
    the cell's ``code``, whether it succeeded, what it wrote to ``stdout`` and
    ``stderr``, its expression result (``execute_result``, a MIME bundle) and,
    for failed cells, the ``error`` that was raised.

Recording is driven by the ``%session_bundle`` magic, by
:meth:`~IPython.core.interactiveshell.InteractiveShell.start_session_bundle` /
:meth:`~IPython.core.interactiveshell.InteractiveShell.stop_session_bundle`, or
by the :func:`session_bundle_recorder` context manager.
"""

from __future__ import annotations

import base64
import errno
import json
import os
import platform
import re
import secrets
import sys
import traceback
import zipfile
import zlib
from contextlib import contextmanager
from dataclasses import dataclass, field
from datetime import datetime, timezone
from functools import partial
from pathlib import Path
from typing import TYPE_CHECKING, Any, Callable, Iterable, Iterator
from warnings import warn

from IPython.core import release

if TYPE_CHECKING:
    from IPython.core.interactiveshell import (
        ExecutionInfo,
        ExecutionResult,
        InteractiveShell,
    )

__all__ = [
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

StrPath = str | os.PathLike[str]

_MISSING = object()


class SessionBundleValidationError(ValueError):
    """Raised when a session bundle does not follow the bundle format.

    Attributes
    ----------
    bundle_path : pathlib.Path
        The bundle that failed validation.
    errors : list of str
        Human-readable descriptions of every problem found.
    """

    def __init__(self, bundle_path: StrPath, errors: list[str]):
        self.bundle_path = Path(bundle_path)
        self.errors = list(errors)
        details = "\n".join(f"  - {error}" for error in self.errors)
        super().__init__(f"Invalid session bundle {self.bundle_path}:\n{details}")


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


def _bundle_path(path: StrPath) -> Path:
    # Absolute, so that %cd during a recording does not move the bundle.
    return Path(os.path.abspath(os.path.expanduser(os.fspath(path))))


def _dump_event(event: dict[str, Any]) -> str:
    return json.dumps(event, ensure_ascii=False)


def _write_bundle(
    path: Path, metadata: dict[str, Any], event_lines: Iterable[str], *, overwrite: bool
) -> None:
    if not overwrite and os.path.lexists(path):
        raise FileExistsError(
            errno.EEXIST,
            "Session bundle already exists (use overwrite=True or --overwrite to replace it)",
            str(path),
        )
    metadata_text = json.dumps(metadata, indent=2, ensure_ascii=False) + "\n"
    events_text = "".join(f"{line}\n" for line in event_lines)
    # Write next to the target and rename, so readers never see a partial bundle.
    tmp_path = path.with_name(f".{path.name}.{secrets.token_hex(4)}.tmp")
    try:
        with zipfile.ZipFile(tmp_path, "x", compression=zipfile.ZIP_DEFLATED) as zf:
            zf.writestr(METADATA_NAME, metadata_text)
            zf.writestr(EVENTS_NAME, events_text)
        os.replace(tmp_path, path)
    except BaseException:
        tmp_path.unlink(missing_ok=True)
        raise


def save_session_bundle(
    path: StrPath,
    meta: dict[str, Any],
    events: Iterable[dict[str, Any]],
    *,
    overwrite: bool = False,
) -> Path:
    """Write ``meta`` and ``events`` as a session bundle at ``path``.

    ``meta`` becomes ``metadata.json`` and each event one line of
    ``events.jsonl``; neither is validated (see
    :func:`validate_session_bundle`).

    Parameters
    ----------
    path : str or path-like
        Location of the bundle.
    meta : dict
        The bundle metadata.
    events : iterable of dict
        The cell events, in execution order.
    overwrite : bool
        Replace ``path`` if it exists instead of raising
        :class:`FileExistsError`.

    Returns
    -------
    pathlib.Path
        The absolute path of the written bundle.
    """
    bundle_path = _bundle_path(path)
    _write_bundle(
        bundle_path, meta, [_dump_event(event) for event in events], overwrite=overwrite
    )
    return bundle_path


@dataclass
class _RawBundle:
    path: Path
    metadata: Any = None
    # (line number in events.jsonl, event) pairs
    events: list[tuple[int, Any]] = field(default_factory=list)
    events_text: str | None = None
    errors: list[str] = field(default_factory=list)


def _read_bundle(path: StrPath) -> _RawBundle:
    """Read and parse a bundle, collecting structural problems in ``errors``.

    Raises :class:`OSError` if the file itself cannot be opened.
    """
    bundle = _RawBundle(_bundle_path(path))
    texts: dict[str, str] = {}
    try:
        with zipfile.ZipFile(bundle.path) as zf:
            names = set(zf.namelist())
            for name in (METADATA_NAME, EVENTS_NAME):
                if name not in names:
                    bundle.errors.append(f"missing {name}")
                    continue
                try:
                    texts[name] = zf.read(name).decode("utf-8")
                except UnicodeDecodeError as e:
                    bundle.errors.append(f"{name} is not valid UTF-8 ({e})")
    except (zipfile.BadZipFile, zlib.error, EOFError, NotImplementedError) as e:
        bundle.errors.append(f"not a readable ZIP archive ({e})")
        return bundle

    if METADATA_NAME in texts:
        try:
            bundle.metadata = json.loads(texts[METADATA_NAME])
        except json.JSONDecodeError as e:
            bundle.errors.append(f"{METADATA_NAME} is not valid JSON ({e})")
        else:
            if not isinstance(bundle.metadata, dict):
                bundle.errors.append(f"{METADATA_NAME} must contain a JSON object")

    bundle.events_text = texts.get(EVENTS_NAME)
    if bundle.events_text is not None:
        # Only split on "\n": JSON escapes it inside strings, but not other
        # characters that str.splitlines() treats as line breaks.
        for lineno, line in enumerate(bundle.events_text.split("\n"), start=1):
            if not line.strip():
                continue
            try:
                event = json.loads(line)
            except json.JSONDecodeError as e:
                bundle.errors.append(f"{EVENTS_NAME} line {lineno}: invalid JSON ({e})")
                continue
            if not isinstance(event, dict):
                bundle.errors.append(
                    f"{EVENTS_NAME} line {lineno}: event must be a JSON object"
                )
                continue
            bundle.events.append((lineno, event))
    return bundle


def load_session_bundle(path: StrPath) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    """Read a session bundle without executing any of its code.

    Parameters
    ----------
    path : str or path-like
        Location of the bundle.

    Returns
    -------
    (metadata, events) : tuple of (dict, list of dict)
        The parsed ``metadata.json`` and the events of ``events.jsonl``, in
        order.

    Raises
    ------
    SessionBundleValidationError
        If the file is not a ZIP archive or its members cannot be parsed.
        Use :func:`validate_session_bundle` for a full schema check.
    """
    bundle = _read_bundle(path)
    if bundle.errors:
        raise SessionBundleValidationError(bundle.path, bundle.errors)
    return bundle.metadata, [event for _, event in bundle.events]


def _short_repr(value: Any) -> str:
    text = repr(value)
    return text if len(text) <= 60 else text[:57] + "..."


def _is_int(value: Any) -> bool:
    return isinstance(value, int) and not isinstance(value, bool)


def _is_str(value: Any) -> bool:
    return isinstance(value, str)


def _is_timestamp(value: Any) -> bool:
    if not isinstance(value, str):
        return False
    try:
        datetime.fromisoformat(value)
    except ValueError:
        return False
    return True


def _check_field(
    obj: dict[str, Any],
    key: str,
    is_valid: Callable[[Any], bool],
    expected: str,
    errors: list[str],
    where: str,
) -> bool:
    value = obj.get(key, _MISSING)
    if value is _MISSING:
        errors.append(f"{where}: missing required field {key!r}")
        return False
    if not is_valid(value):
        errors.append(f"{where}: {key!r} must be {expected}, got {_short_repr(value)}")
        return False
    return True


def _metadata_errors(metadata: dict[str, Any], event_count: int) -> list[str]:
    errors: list[str] = []
    check = partial(_check_field, metadata, errors=errors, where=METADATA_NAME)
    check("format", lambda v: v == FORMAT_NAME, repr(FORMAT_NAME))
    check("format_version", lambda v: _is_int(v) and v >= 1, "an integer >= 1")
    check("created_at", _is_timestamp, "an ISO-8601 timestamp")
    for key in ("ipython_version", "python_version", "platform"):
        check(key, _is_str, "a string")
    check(
        "redactions",
        lambda v: isinstance(v, list) and all(map(_is_str, v)),
        "a list of strings",
    )
    if "event_count" in metadata:
        check(
            "event_count",
            lambda v: _is_int(v) and v == event_count,
            f"the number of events in {EVENTS_NAME} ({event_count})",
        )
    return errors


def _event_errors(event: dict[str, Any], lineno: int, expected_seq: int) -> list[str]:
    errors: list[str] = []
    where = f"{EVENTS_NAME} line {lineno}"
    check = partial(_check_field, event, errors=errors, where=where)
    check("type", lambda v: v == "cell", "'cell'")
    check(
        "seq",
        lambda v: _is_int(v) and v == expected_seq,
        f"{expected_seq} (seq starts at 1 and is contiguous)",
    )
    check("recorded_at", _is_timestamp, "an ISO-8601 timestamp")
    check("execution_count", lambda v: v is None or _is_int(v), "an integer or null")
    check("code", _is_str, "a string")
    check("success", lambda v: isinstance(v, bool), "a boolean")
    check("stdout", _is_str, "a string")
    check("stderr", _is_str, "a string")
    if check("execute_result", lambda v: isinstance(v, dict), "an object"):
        execute_result = event["execute_result"]
        if execute_result and not _is_str(execute_result.get("text/plain")):
            errors.append(
                f"{where}: a non-empty 'execute_result' must have a 'text/plain' string"
            )
    if event.get("success") is False:
        if "error" not in event:
            errors.append(f"{where}: failed cells must have an 'error' field")
        elif check("error", lambda v: isinstance(v, dict), "an object"):
            error_check = partial(
                _check_field, event["error"], errors=errors, where=f"{where}: error"
            )
            error_check("ename", _is_str, "a string")
            error_check("evalue", _is_str, "a string")
            error_check(
                "traceback",
                lambda v: isinstance(v, list) and len(v) > 0 and all(map(_is_str, v)),
                "a non-empty list of strings",
            )
    return errors


def _redaction_errors(metadata: dict[str, Any], events_text: str) -> list[str]:
    redactions = metadata.get("redactions")
    if not isinstance(redactions, list):
        return []
    # Ignore the markers themselves, in case a pattern is part of "<redacted>".
    unredacted_parts = events_text.split(REDACTED)
    errors = []
    for index, pattern in enumerate(redactions, start=1):
        if not _is_str(pattern) or not pattern:
            continue
        if any(pattern in part for part in unredacted_parts):
            # Refer to the pattern by position: it is what should stay secret.
            errors.append(
                f"{EVENTS_NAME}: redacted pattern #{index} appears in the recorded events"
            )
    return errors


def _bundle_errors(bundle: _RawBundle) -> list[str]:
    errors = list(bundle.errors)
    if isinstance(bundle.metadata, dict):
        errors.extend(_metadata_errors(bundle.metadata, len(bundle.events)))
        if bundle.events_text is not None:
            errors.extend(_redaction_errors(bundle.metadata, bundle.events_text))
    for expected_seq, (lineno, event) in enumerate(bundle.events, start=1):
        errors.extend(_event_errors(event, lineno, expected_seq))
    return errors


def validate_session_bundle(path: StrPath, *, strict: bool = True) -> list[str]:
    """Check a session bundle against the bundle format.

    Parameters
    ----------
    path : str or path-like
        Location of the bundle.
    strict : bool
        Raise :class:`SessionBundleValidationError` if any problem is found,
        instead of only returning the problems.

    Returns
    -------
    list of str
        Human-readable descriptions of the problems found; empty for a valid
        bundle.
    """
    try:
        bundle = _read_bundle(path)
    except OSError as e:
        errors = [f"cannot read bundle ({e})"]
    else:
        errors = _bundle_errors(bundle)
    if strict and errors:
        raise SessionBundleValidationError(_bundle_path(path), errors)
    return errors


def replay_session_bundle(
    shell: InteractiveShell,
    path: StrPath,
    *,
    stop_on_error: bool = True,
    store_history: bool = True,
) -> list[ExecutionResult]:
    """Re-execute the cells recorded in a session bundle.

    The bundle is validated before anything runs. Cells are then run in
    order with ``shell.run_cell``; recorded outputs are not compared with the
    new ones. Redacted cells run with ``<redacted>`` in place of the redacted
    text.

    Parameters
    ----------
    shell : InteractiveShell
        The shell to execute the cells in.
    path : str or path-like
        Location of the bundle.
    stop_on_error : bool
        Stop after the first cell that fails during the replay.
    store_history : bool
        Store the replayed cells in the shell's history, advancing
        ``shell.execution_count`` once per replayed cell.

    Returns
    -------
    list of ExecutionResult
        The results of the cells that were executed.

    Raises
    ------
    SessionBundleValidationError
        If the bundle is invalid; nothing is executed in that case.
    """
    bundle = _read_bundle(path)
    errors = _bundle_errors(bundle)
    if errors:
        raise SessionBundleValidationError(bundle.path, errors)
    results = []
    for _, event in bundle.events:
        code = event["code"]
        if not code or code.isspace():
            # run_cell ignores blank cells entirely.
            continue
        result = shell.run_cell(code, store_history=store_history)
        results.append(result)
        if stop_on_error and (result is None or not result.success):
            break
    return results


@contextmanager
def session_bundle_recorder(
    shell: InteractiveShell,
    path: StrPath,
    *,
    overwrite: bool = False,
    redact: str | Iterable[str] | None = None,
) -> Iterator[str]:
    """Record the cells run by ``shell`` inside a ``with`` block.

    Equivalent to calling ``shell.start_session_bundle(path, overwrite=...,
    redact=...)`` on entry and ``shell.stop_session_bundle()`` on exit. The
    bundle path is bound by ``as``.
    """
    bundle_path = shell.start_session_bundle(path, overwrite=overwrite, redact=redact)
    recorder = shell._session_bundle_recorder
    try:
        yield bundle_path
    finally:
        # The block may have stopped this recording (and started another) itself.
        if shell._session_bundle_recorder is recorder:
            shell.stop_session_bundle()


def _normalize_redactions(redact: str | Iterable[str] | None) -> list[str]:
    if redact is None:
        return []
    patterns = [redact] if isinstance(redact, str) else list(redact)
    for pattern in patterns:
        if not isinstance(pattern, str):
            raise TypeError(
                f"redaction patterns must be strings, not {type(pattern).__name__}"
            )
        if not pattern:
            raise ValueError("redaction patterns must be non-empty")
    return patterns


class _AttributeWrapper:
    """Shadow ``obj.<name>`` with a wrapper around it, as an instance attribute."""

    def __init__(self, obj: Any, name: str, wrap: Callable[[Any], Any]):
        self._obj = obj
        self._name = name
        self._saved = vars(obj).get(name, _MISSING)
        self._wrapper = wrap(getattr(obj, name))
        setattr(obj, name, self._wrapper)

    def restore(self) -> None:
        if vars(self._obj).get(self._name, _MISSING) is not self._wrapper:
            # Someone wrapped it again after us; don't unwrap them.
            return
        if self._saved is _MISSING:
            delattr(self._obj, self._name)
        else:
            setattr(self._obj, self._name, self._saved)


class _CellCapture:
    """What a top-level cell writes to stdout/stderr and its expression result."""

    def __init__(self, shell: InteractiveShell, info: ExecutionInfo):
        self.shell = shell
        self.info = info
        # Cells run by this cell through run_cell, whose post_run_cell is pending.
        self.nested = 0
        self.stdout: list[str] = []
        self.stderr: list[str] = []
        self.format_dict: dict[str, Any] = {}
        self._active = True
        self._wrappers: list[_AttributeWrapper] = []
        for obj, name, wrap in (
            (sys.stdout, "write", partial(self._wrap_write, self.stdout)),
            (sys.stderr, "write", partial(self._wrap_write, self.stderr)),
            (shell.displayhook, "log_output", self._wrap_log_output),
        ):
            try:
                self._wrappers.append(_AttributeWrapper(obj, name, wrap))
            except (AttributeError, TypeError):
                # Objects without instance attributes can't be wrapped; what
                # goes through them is not recorded.
                pass

    def _is_user_output(self) -> bool:
        # Expression results and tracebacks are recorded in their own fields.
        shell = self.shell
        return not any(
            [
                getattr(shell.display_pub, "is_publishing", False),
                getattr(shell.displayhook, "is_active", False),
                getattr(shell, "showing_traceback", False),
            ]
        )

    def _wrap_write(
        self, chunks: list[str], write: Callable[..., Any]
    ) -> Callable[..., Any]:
        def recording_write(data: Any, *args: Any, **kwargs: Any) -> Any:
            result = write(data, *args, **kwargs)
            if self._active and isinstance(data, str) and self._is_user_output():
                chunks.append(data)
            return result

        return recording_write

    def _wrap_log_output(self, log_output: Callable[..., Any]) -> Callable[..., Any]:
        def recording_log_output(format_dict: dict[str, Any]) -> Any:
            if self._active:
                self.format_dict = format_dict
            return log_output(format_dict)

        return recording_log_output

    def close(self) -> None:
        self._active = False
        for wrapper in reversed(self._wrappers):
            wrapper.restore()


class SessionBundleRecorder:
    """Record the cells executed by a shell into a session bundle.

    The bundle is written when recording starts and rewritten after every
    recorded cell, so it stays readable if the session ends abruptly. Only
    top-level, non-silent cells are recorded; cells they run through
    ``run_cell`` are part of their output. Use
    :meth:`~IPython.core.interactiveshell.InteractiveShell.start_session_bundle`
    rather than creating recorders directly.
    """

    def __init__(
        self,
        shell: InteractiveShell,
        path: StrPath,
        *,
        overwrite: bool = False,
        redact: str | Iterable[str] | None = None,
    ):
        self.shell = shell
        self.path = _bundle_path(path)
        self.overwrite = overwrite
        self.redactions = _normalize_redactions(redact)
        self._redact: Callable[[str], str] = lambda text: text
        if self.redactions:
            # Longest first, so a pattern containing another one is replaced whole.
            alternatives = sorted(set(self.redactions), key=len, reverse=True)
            regex = re.compile("|".join(map(re.escape, alternatives)))
            self._redact = partial(regex.sub, REDACTED)
        self._metadata: dict[str, Any] = {}
        self._event_lines: list[str] = []
        self._cell: _CellCapture | None = None

    def start(self) -> None:
        self._metadata = {
            "format": FORMAT_NAME,
            "format_version": FORMAT_VERSION,
            "created_at": _now(),
            "ipython_version": release.version,
            "python_version": platform.python_version(),
            "platform": platform.platform(),
            "redactions": list(self.redactions),
            "event_count": 0,
        }
        self._write(overwrite=self.overwrite)
        self.shell.events.register("pre_run_cell", self._pre_run_cell)
        self.shell.events.register("post_run_cell", self._post_run_cell)

    def stop(self) -> None:
        self.shell.events.unregister("pre_run_cell", self._pre_run_cell)
        self.shell.events.unregister("post_run_cell", self._post_run_cell)
        if self._cell is not None:
            # Stopped from inside a cell: that cell is not recorded.
            self._cell.close()
            self._cell = None
        self._write(overwrite=True)

    def _write(self, *, overwrite: bool) -> None:
        self._metadata["event_count"] = len(self._event_lines)
        _write_bundle(self.path, self._metadata, self._event_lines, overwrite=overwrite)

    def _pre_run_cell(self, info: ExecutionInfo) -> None:
        if self._cell is not None:
            self._cell.nested += 1
            return
        self._cell = _CellCapture(self.shell, info)

    def _post_run_cell(self, result: ExecutionResult | None) -> None:
        cell = self._cell
        if cell is None:
            # e.g. the cell that started the recording
            return
        info = getattr(result, "info", None)
        if info is not cell.info:
            raw_cell = getattr(info, "raw_cell", None)
            if info is not None and (not raw_cell or raw_cell.isspace()):
                # Blank cells fire post_run_cell without pre_run_cell.
                return
            if cell.nested:
                cell.nested -= 1
                return
        self._cell = None
        cell.close()
        if result is None:
            return
        self._event_lines.append(_dump_event(self._cell_event(cell, result)))
        try:
            self._write(overwrite=True)
        except OSError as e:
            warn(f"Could not update session bundle {self.path}: {e}", stacklevel=2)

    def _cell_event(
        self, cell: _CellCapture, result: ExecutionResult
    ) -> dict[str, Any]:
        event: dict[str, Any] = {
            "type": "cell",
            "seq": len(self._event_lines) + 1,
            "recorded_at": _now(),
            "execution_count": (
                result.execution_count if cell.info.store_history else None
            ),
            "code": self._clean(cell.info.raw_cell),
            "success": result.success,
            "stdout": self._clean("".join(cell.stdout)),
            "stderr": self._clean("".join(cell.stderr)),
            "execute_result": {},
        }
        if cell.format_dict:
            execute_result = {
                str(mime): self._clean(data) for mime, data in cell.format_dict.items()
            }
            if not _is_str(execute_result.get("text/plain")):
                execute_result["text/plain"] = ""
            event["execute_result"] = execute_result
        exc = (
            result.error_before_exec
            if result.error_before_exec is not None
            else result.error_in_exec
        )
        if exc is not None:
            try:
                stb = list(self.shell._format_exception_for_storage(exc)["traceback"])
            except Exception:
                stb = []
            if not stb:
                stb = traceback.format_exception_only(type(exc), exc)
            event["error"] = {
                "ename": self._clean(type(exc).__name__),
                "evalue": self._clean(str(exc)),
                "traceback": [self._clean(str(line)) for line in stb],
            }
        return event

    def _clean(self, value: Any) -> Any:
        """Make ``value`` JSON-serializable, with redacted text."""
        if isinstance(value, str):
            if not value.isascii():
                # Lone surrogates (e.g. from surrogateescape streams) can't be
                # encoded as UTF-8.
                value = value.encode("utf-8", "backslashreplace").decode("utf-8")
            return self._redact(value)
        if value is None or isinstance(value, (bool, int, float)):
            return value
        if isinstance(value, bytes):
            return self._redact(base64.b64encode(value).decode("ascii"))
        if isinstance(value, dict):
            return {self._clean(str(k)): self._clean(v) for k, v in value.items()}
        if isinstance(value, (list, tuple)):
            return [self._clean(v) for v in value]
        return self._clean(repr(value))
