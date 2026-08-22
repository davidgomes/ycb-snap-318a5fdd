"""Record and replay IPython sessions as portable ZIP bundles."""

from __future__ import annotations

import json
import platform
import re
import sys
import traceback
import zipfile
from contextlib import contextmanager
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from IPython import __version__


class SessionBundleValidationError(ValueError):
    def __init__(self, bundle_path: Path, errors: list[str]):
        self.bundle_path, self.errors = Path(bundle_path), errors
        super().__init__("Invalid session bundle: " + "; ".join(errors))


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


def save_session_bundle(path, meta, events, *, overwrite=False) -> Path:
    path = Path(path)
    if path.exists() and not overwrite:
        raise FileExistsError(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    meta = dict(meta)
    meta.setdefault("event_count", len(events))
    with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as bundle:
        bundle.writestr("metadata.json", json.dumps(meta, ensure_ascii=False))
        bundle.writestr(
            "events.jsonl",
            "".join(json.dumps(event, ensure_ascii=False) + "\n" for event in events),
        )
    return path


def load_session_bundle(path):
    with zipfile.ZipFile(path) as bundle:
        metadata = json.loads(bundle.read("metadata.json"))
        events = [
            json.loads(line)
            for line in bundle.read("events.jsonl").decode("utf-8").splitlines()
            if line
        ]
    return metadata, events


def validate_session_bundle(path, *, strict=True):
    path = Path(path)
    errors = []
    try:
        metadata, events = load_session_bundle(path)
    except Exception as exc:
        errors.append(f"could not read bundle: {exc}")
        metadata, events = {}, []
    required_meta = ("format", "format_version", "created_at", "ipython_version",
                     "python_version", "platform", "redactions")
    for key in required_meta:
        if key not in metadata:
            errors.append(f"metadata missing {key}")
    if metadata.get("format") != "ipython-session-bundle":
        errors.append("metadata format is invalid")
    if not isinstance(metadata.get("format_version"), int) or metadata.get("format_version", 0) < 1:
        errors.append("metadata format_version must be an integer >= 1")
    if not isinstance(metadata.get("redactions"), list):
        errors.append("metadata redactions must be a list")
    if "event_count" in metadata and metadata["event_count"] != len(events):
        errors.append("metadata event_count does not match events")
    for index, event in enumerate(events, 1):
        for key in ("type", "seq", "recorded_at", "execution_count", "code",
                    "success", "stdout", "stderr", "execute_result"):
            if key not in event:
                errors.append(f"event {index} missing {key}")
        if event.get("type") != "cell" or event.get("seq") != index:
            errors.append(f"event {index} has invalid sequence or type")
        if not isinstance(event.get("code"), str) or not isinstance(event.get("success"), bool):
            errors.append(f"event {index} has invalid code or success")
        result = event.get("execute_result", {})
        if result and not isinstance(result.get("text/plain"), str):
            errors.append(f"event {index} execute_result lacks text/plain")
        if not event.get("success") and (
            not isinstance(event.get("error"), dict)
            or not isinstance(event["error"].get("traceback"), list)
            or not event["error"]["traceback"]
        ):
            errors.append(f"event {index} has invalid error")
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
    def __init__(self, shell, path, overwrite, redact):
        self.shell, self.path = shell, Path(path)
        self.overwrite, self.redactions = overwrite, list(redact or [])
        self.events, self._pre, self._post = [], None, None

    def start(self):
        if getattr(self.shell, "_session_bundle_recorder", None):
            raise RuntimeError("a session bundle recording is already active")
        if self.path.exists() and not self.overwrite:
            raise FileExistsError(self.path)
        self._pre = lambda info: setattr(self, "_current", info)
        self._post = self._record
        self.shell.events.register("pre_run_cell", self._pre)
        self.shell.events.register("post_run_cell", self._post)
        self.shell._session_bundle_recorder = self
        return str(self.path)

    def _record(self, result):
        info = getattr(self, "_current", None)
        if info is None:
            return
        stdout = "".join(x.bundle["stream"] for x in [] if False)
        count = result.execution_count
        outputs = self.shell.history_manager.outputs.get(count, [])
        stdout = "".join(x.bundle["stream"] for x in outputs if x.output_type == "out_stream")
        stderr = "".join(x.bundle["stream"] for x in outputs if x.output_type == "err_stream")
        event = {"type": "cell", "seq": len(self.events) + 1, "recorded_at": _now(),
                 "execution_count": count, "code": info.raw_cell, "success": result.success,
                 "stdout": stdout, "stderr": stderr, "execute_result": {}}
        if result.result is not None:
            event["execute_result"] = {"text/plain": repr(result.result)}
        error = result.error_in_exec or result.error_before_exec
        if error:
            event["error"] = {"ename": type(error).__name__, "evalue": str(error),
                              "traceback": traceback.format_exception(type(error), error, error.__traceback__)}
        self.events.append(event)

    def stop(self):
        self.shell.events.unregister("pre_run_cell", self._pre)
        self.shell.events.unregister("post_run_cell", self._post)
        self.shell._session_bundle_recorder = None
        patterns = self.redactions
        for event in self.events:
            for key in ("code", "stdout", "stderr"):
                for pattern in patterns:
                    event[key] = event[key].replace(pattern, "<redacted>")
            event["execute_result"] = {
                key: re.sub("|".join(map(re.escape, patterns)), "<redacted>", value)
                if patterns else value
                for key, value in event["execute_result"].items()
            }
        meta = {"format": "ipython-session-bundle", "format_version": 1, "created_at": _now(),
                "ipython_version": __version__, "python_version": sys.version,
                "platform": platform.platform(), "redactions": self.redactions}
        save_session_bundle(self.path, meta, self.events, overwrite=True)
        return str(self.path)


@contextmanager
def session_bundle_recorder(shell, path, *, overwrite=False, redact=None):
    recorder = _Recorder(shell, path, overwrite, redact)
    recorder.start()
    try:
        yield recorder
    finally:
        recorder.stop()
