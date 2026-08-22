"""Record and replay IPython sessions as portable ZIP bundles."""

from __future__ import annotations

import datetime as _datetime
import io
import json
import platform
import sys
import tempfile
import zipfile
from pathlib import Path

from IPython import __version__


class SessionBundleValidationError(ValueError):
    def __init__(self, bundle_path, errors):
        self.bundle_path = Path(bundle_path)
        self.errors = list(errors)
        super().__init__("\n".join(self.errors))


def _now():
    return _datetime.datetime.now(_datetime.timezone.utc).isoformat()


def save_session_bundle(path, meta, events, *, overwrite=False):
    path = Path(path)
    if path.exists() and not overwrite:
        raise FileExistsError(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    metadata = dict(meta)
    metadata.setdefault("event_count", len(events))
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as f:
        temporary = Path(f.name)
    try:
        with zipfile.ZipFile(temporary, "w", zipfile.ZIP_DEFLATED) as zf:
            zf.writestr("metadata.json", json.dumps(metadata, indent=2))
            zf.writestr("events.jsonl", "".join(json.dumps(e) + "\n" for e in events))
        temporary.replace(path)
    finally:
        temporary.unlink(missing_ok=True)
    return path


def load_session_bundle(path):
    with zipfile.ZipFile(path) as zf:
        return (json.loads(zf.read("metadata.json")),
                [json.loads(line) for line in zf.read("events.jsonl").decode().splitlines()])


def validate_session_bundle(path, *, strict=True):
    path = Path(path)
    errors = []
    try:
        metadata, events = load_session_bundle(path)
    except Exception as exc:
        errors.append(f"cannot read bundle: {exc}")
        if strict:
            raise SessionBundleValidationError(path, errors)
        return errors
    required_meta = ("format", "format_version", "created_at", "ipython_version",
                     "python_version", "platform", "redactions")
    for key in required_meta:
        if key not in metadata:
            errors.append(f"metadata missing {key!r}")
    if metadata.get("format") != "ipython-session-bundle":
        errors.append("metadata format must be 'ipython-session-bundle'")
    if not isinstance(metadata.get("format_version"), int) or metadata.get("format_version", 0) < 1:
        errors.append("metadata format_version must be an integer >= 1")
    if not isinstance(metadata.get("redactions"), list) or not all(isinstance(x, str) for x in metadata.get("redactions", [])):
        errors.append("metadata redactions must be a list of strings")
    if "event_count" in metadata and metadata["event_count"] != len(events):
        errors.append("metadata event_count does not match events")
    for index, event in enumerate(events, 1):
        for key in ("type", "seq", "recorded_at", "execution_count", "code", "success", "stdout", "stderr", "execute_result"):
            if key not in event:
                errors.append(f"event {index} missing {key!r}")
        if event.get("type") != "cell" or event.get("seq") != index:
            errors.append(f"event {index} has invalid type or sequence")
        if not isinstance(event.get("code"), str) or not isinstance(event.get("success"), bool):
            errors.append(f"event {index} has invalid code or success")
        result = event.get("execute_result", {})
        if result and not isinstance(result.get("text/plain"), str):
            errors.append(f"event {index} execute_result must contain text/plain")
        if not event.get("success") and not isinstance(event.get("error", {}).get("traceback"), list):
            errors.append(f"event {index} failure must include traceback")
    if strict and errors:
        raise SessionBundleValidationError(path, errors)
    return errors


def replay_session_bundle(shell, path, *, stop_on_error=True, store_history=True):
    _, events = load_session_bundle(path)
    results = []
    for event in events:
        result = shell.run_cell(event["code"], store_history=store_history)
        results.append(result)
        if stop_on_error and not result.success:
            break
    return results


class _Recorder:
    def __init__(self, shell, path, overwrite=False, redact=None):
        self.shell, self.path = shell, Path(path)
        self.overwrite = overwrite
        self.redactions = list(redact or [])
        self.events = []
        self._stdout = self._stderr = None
        self._pre = self._post = None

    def start(self):
        if getattr(self.shell, "_session_bundle_recorder", None):
            raise RuntimeError("a session bundle recording is already active")
        if self.path.exists() and not self.overwrite:
            raise FileExistsError(self.path)
        self._pre = self._capture
        self._post = self._finish
        self.shell.events.register("pre_run_cell", self._pre)
        self.shell.events.register("post_run_cell", self._post)
        return str(self.path)

    def _capture(self, info):
        self._stdout, self._stderr = io.StringIO(), io.StringIO()
        self._old_stdout, self._old_stderr = sys.stdout, sys.stderr
        sys.stdout, sys.stderr = self._Tee(self._old_stdout, self._stdout), self._Tee(self._old_stderr, self._stderr)

    class _Tee:
        def __init__(self, original, capture):
            self.original, self.capture = original, capture
        def write(self, value):
            self.original.write(value)
            return self.capture.write(value)
        def flush(self):
            self.original.flush()
            self.capture.flush()
        def __getattr__(self, name):
            return getattr(self.original, name)

    def _finish(self, result):
        sys.stdout, sys.stderr = self._old_stdout, self._old_stderr
        error = None
        exc = result.error_before_exec or result.error_in_exec
        if exc:
            error = {"ename": type(exc).__name__, "evalue": str(exc),
                     "traceback": traceback_lines(exc)}
        event = {"type": "cell", "seq": len(self.events) + 1, "recorded_at": _now(),
                 "execution_count": result.execution_count, "code": result.info.raw_cell,
                 "success": result.success, "stdout": self._stdout.getvalue(),
                 "stderr": self._stderr.getvalue(),
                 "execute_result": {"text/plain": repr(result.result)} if result.result is not None else {}}
        if error:
            event["error"] = error
        for key, value in list(event.items()):
            if isinstance(value, str):
                for pattern in self.redactions:
                    value = value.replace(pattern, "<redacted>")
                event[key] = value
        self.events.append(event)

    def stop(self):
        self.shell.events.unregister("pre_run_cell", self._pre)
        self.shell.events.unregister("post_run_cell", self._post)
        self.shell._session_bundle_recorder = None
        meta = {"format": "ipython-session-bundle", "format_version": 1, "created_at": _now(),
                "ipython_version": __version__, "python_version": sys.version,
                "platform": platform.platform(), "redactions": self.redactions}
        save_session_bundle(self.path, meta, self.events, overwrite=True)
        return str(self.path)

    def __enter__(self):
        self.start()
        return self

    def __exit__(self, *args):
        self.stop()


def traceback_lines(exc):
    import traceback
    return traceback.format_exception(type(exc), exc, exc.__traceback__)


def session_bundle_recorder(shell, path, *, overwrite=False, redact=None):
    return _Recorder(shell, path, overwrite, redact)
