"""Record an IPython session to a single ``.ipybundle`` file and replay it."""

from __future__ import annotations

import contextlib
import json
import os
import platform
import sys
import tempfile
import traceback
import zipfile
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Iterable, Optional

FORMAT_NAME = "ipython-session-bundle"
FORMAT_VERSION = 1
REDACTED = "<redacted>"

__all__ = [
    "SessionBundleValidationError",
    "SessionBundleRecorder",
    "load_session_bundle",
    "replay_session_bundle",
    "save_session_bundle",
    "validate_session_bundle",
    "session_bundle_recorder",
]


class SessionBundleValidationError(ValueError):
    def __init__(self, bundle_path, errors):
        self.bundle_path = Path(bundle_path)
        self.errors = list(errors)
        super().__init__(
            f"Invalid session bundle {self.bundle_path}:\n"
            + "\n".join(f"  - {e}" for e in self.errors)
        )


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


def _is_iso(value) -> bool:
    if not isinstance(value, str):
        return False
    try:
        datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return False
    return True


def _make_metadata(redactions: list[str]) -> dict:
    from IPython import __version__

    return {
        "format": FORMAT_NAME,
        "format_version": FORMAT_VERSION,
        "created_at": _now(),
        "ipython_version": __version__,
        "python_version": platform.python_version(),
        "platform": platform.platform(),
        "redactions": list(redactions),
    }


def save_session_bundle(path, meta, events, *, overwrite=False) -> Path:
    path = Path(path)
    if path.exists() and not overwrite:
        raise FileExistsError(str(path))
    events = list(events)
    meta = dict(meta)
    meta["event_count"] = len(events)
    body = "".join(json.dumps(e, ensure_ascii=False) + "\n" for e in events)
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(dir=path.parent, suffix=".tmp")
    os.close(fd)
    try:
        with zipfile.ZipFile(tmp, "w", zipfile.ZIP_DEFLATED) as zf:
            zf.writestr("metadata.json", json.dumps(meta, indent=2, ensure_ascii=False))
            zf.writestr("events.jsonl", body)
        os.replace(tmp, path)
    except BaseException:
        with contextlib.suppress(OSError):
            os.unlink(tmp)
        raise
    return path


def load_session_bundle(path) -> tuple[dict, list[dict]]:
    with zipfile.ZipFile(Path(path)) as zf:
        meta = json.loads(zf.read("metadata.json").decode("utf-8"))
        text = zf.read("events.jsonl").decode("utf-8")
    events = [json.loads(line) for line in text.splitlines() if line.strip()]
    return meta, events


def validate_session_bundle(path, *, strict=True) -> list[str]:
    path = Path(path)
    errors: list[str] = []
    try:
        with zipfile.ZipFile(path) as zf:
            names = set(zf.namelist())
            for required in ("metadata.json", "events.jsonl"):
                if required not in names:
                    errors.append(f"missing {required} in archive")
            meta = raw = None
            if "metadata.json" in names:
                try:
                    meta = json.loads(zf.read("metadata.json").decode("utf-8"))
                except ValueError as e:
                    errors.append(f"metadata.json is not valid JSON: {e}")
            if "events.jsonl" in names:
                raw = zf.read("events.jsonl").decode("utf-8")
    except (OSError, zipfile.BadZipFile) as e:
        errors.append(f"cannot open bundle as ZIP archive: {e}")
        meta = raw = None

    if meta is not None:
        if not isinstance(meta, dict):
            errors.append("metadata.json must be an object")
            meta = {}
        if meta.get("format") != FORMAT_NAME:
            errors.append(f"metadata.format must be {FORMAT_NAME!r}")
        fv = meta.get("format_version")
        if not isinstance(fv, int) or isinstance(fv, bool) or fv < 1:
            errors.append("metadata.format_version must be an integer >= 1")
        if not _is_iso(meta.get("created_at")):
            errors.append("metadata.created_at must be an ISO-8601 string")
        for key in ("ipython_version", "python_version", "platform"):
            if not isinstance(meta.get(key), str):
                errors.append(f"metadata.{key} must be a string")
        red = meta.get("redactions")
        if not isinstance(red, list) or not all(isinstance(r, str) for r in red):
            errors.append("metadata.redactions must be a list of strings")
            red = []
    else:
        red = []

    events: list = []
    if raw is not None:
        for lineno, line in enumerate(raw.splitlines(), 1):
            if not line.strip():
                continue
            try:
                events.append(json.loads(line))
            except ValueError as e:
                errors.append(f"events.jsonl line {lineno}: invalid JSON: {e}")
        for r in red:
            if r and r in raw:
                errors.append(f"redacted pattern {r!r} appears in events.jsonl")
        if meta is not None and "event_count" in meta:
            ec = meta["event_count"]
            if not isinstance(ec, int) or isinstance(ec, bool) or ec != len(events):
                errors.append(
                    f"metadata.event_count ({ec!r}) does not match number of events ({len(events)})"
                )

    for i, ev in enumerate(events, 1):
        p = f"event {i}"
        if not isinstance(ev, dict):
            errors.append(f"{p}: must be an object")
            continue
        if ev.get("type") != "cell":
            errors.append(f"{p}: type must be 'cell'")
        if ev.get("seq") != i:
            errors.append(f"{p}: seq must be {i}, got {ev.get('seq')!r}")
        if not _is_iso(ev.get("recorded_at")):
            errors.append(f"{p}: recorded_at must be an ISO-8601 string")
        ec = ev.get("execution_count")
        if ec is not None and (not isinstance(ec, int) or isinstance(ec, bool)):
            errors.append(f"{p}: execution_count must be int or null")
        for key in ("code", "stdout", "stderr"):
            if not isinstance(ev.get(key), str):
                errors.append(f"{p}: {key} must be a string")
        if not isinstance(ev.get("success"), bool):
            errors.append(f"{p}: success must be a boolean")
        er = ev.get("execute_result")
        if not isinstance(er, dict):
            errors.append(f"{p}: execute_result must be an object")
        elif er and not isinstance(er.get("text/plain"), str):
            errors.append(f"{p}: non-empty execute_result must include text/plain string")
        if ev.get("success") is False:
            err = ev.get("error")
            if not isinstance(err, dict):
                errors.append(f"{p}: failed cell must include error object")
            else:
                for key in ("ename", "evalue"):
                    if not isinstance(err.get(key), str):
                        errors.append(f"{p}: error.{key} must be a string")
                tb = err.get("traceback")
                if (
                    not isinstance(tb, list)
                    or not tb
                    or not all(isinstance(t, str) for t in tb)
                ):
                    errors.append(f"{p}: error.traceback must be a non-empty list of strings")

    if strict and errors:
        raise SessionBundleValidationError(path, errors)
    return errors


def replay_session_bundle(shell, path, *, stop_on_error=True, store_history=True):
    _, events = load_session_bundle(path)
    results = []
    for ev in events:
        if ev.get("type") != "cell":
            continue
        res = shell.run_cell(ev.get("code", ""), store_history=store_history)
        results.append(res)
        if stop_on_error and res is not None and not res.success:
            break
    return results


class _StreamCapture:
    def __init__(self, shell, channel):
        self.shell = shell
        self.channel = channel
        self.parts: list[str] = []
        self.paused = False

    def __enter__(self):
        self.stream = getattr(sys, self.channel)
        self.original = self.stream.write
        shell = self.shell

        def write(data, *args, **kwargs):
            result = self.original(data, *args, **kwargs)
            if data and not (
                self.paused
                or shell.display_pub.is_publishing
                or shell.displayhook.is_active
                or shell.showing_traceback
            ):
                self.parts.append(data if isinstance(data, str) else str(data))
            return result

        try:
            self.stream.write = write
            self.patched = True
        except AttributeError:
            self.patched = False
        return self

    def __exit__(self, *exc):
        if self.patched:
            try:
                del self.stream.write
            except AttributeError:
                self.stream.write = self.original

    @property
    def text(self):
        return "".join(self.parts)


class SessionBundleRecorder:
    def __init__(self, shell, path, redact: Optional[Iterable[str]] = None):
        self.shell = shell
        self.path = Path(path)
        self.redactions = [r for r in (redact or [])]
        self.meta = _make_metadata(self.redactions)
        self.events: list[dict] = []

    def _redact(self, value):
        if isinstance(value, str):
            for r in self.redactions:
                if r:
                    value = value.replace(r, REDACTED)
            return value
        if isinstance(value, list):
            return [self._redact(v) for v in value]
        if isinstance(value, dict):
            return {self._redact(k): self._redact(v) for k, v in value.items()}
        return value

    def save(self, overwrite=True):
        return save_session_bundle(self.path, self.meta, self.events, overwrite=overwrite)

    @contextlib.contextmanager
    def capture(self, raw_cell):
        shell = self.shell
        count = shell.execution_count
        with _StreamCapture(shell, "stdout") as out, _StreamCapture(shell, "stderr") as err:
            had_instance_attr = "_showtraceback" in shell.__dict__
            original_showtb = shell._showtraceback

            def showtb(*args, **kwargs):
                out.paused = err.paused = True
                try:
                    return original_showtb(*args, **kwargs)
                finally:
                    out.paused = err.paused = False

            shell._showtraceback = showtb
            holder: dict[str, Any] = {}
            try:
                yield holder
            finally:
                if had_instance_attr:
                    shell._showtraceback = original_showtb
                else:
                    del shell._showtraceback
        if shell._session_bundle is not self:
            return
        self.record(raw_cell, count, holder.get("result"), out.text, err.text)

    def record(self, code, count, result, stdout, stderr):
        shell = self.shell
        success = bool(result.success) if result is not None else False
        execute_result: dict = {}
        if result is not None and result.result is not None:
            try:
                data, _ = shell.display_formatter.format(result.result)
                execute_result = {
                    k: v for k, v in data.items() if isinstance(v, str)
                }
            except Exception:
                execute_result = {}
            if not isinstance(execute_result.get("text/plain"), str):
                try:
                    execute_result["text/plain"] = repr(result.result)
                except Exception:
                    execute_result["text/plain"] = ""
        ev: dict[str, Any] = {
            "type": "cell",
            "seq": len(self.events) + 1,
            "recorded_at": _now(),
            "execution_count": count if (result is not None and result.info.store_history) else None,
            "code": code,
            "success": success,
            "stdout": stdout,
            "stderr": stderr,
            "execute_result": execute_result,
        }
        if not success:
            exc = None
            if result is not None:
                exc = result.error_in_exec or result.error_before_exec
            if exc is not None:
                tb = traceback.format_exception(type(exc), exc, exc.__traceback__)
                ev["error"] = {
                    "ename": type(exc).__name__,
                    "evalue": str(exc),
                    "traceback": tb or [f"{type(exc).__name__}: {exc}"],
                }
            else:
                ev["error"] = {
                    "ename": "UnknownError",
                    "evalue": "",
                    "traceback": ["UnknownError: cell execution failed"],
                }
        self.events.append(self._redact(ev))
        self.save()


@contextlib.contextmanager
def session_bundle_recorder(shell, path, *, overwrite=False, redact=None):
    shell.start_session_bundle(path, overwrite=overwrite, redact=redact)
    try:
        yield
    finally:
        if shell.session_bundle_status()["recording"]:
            shell.stop_session_bundle()
