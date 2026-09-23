"""Tests for IPython session bundles."""

import json
import zipfile
from pathlib import Path

import pytest

from IPython.core.sessionbundle import (
    SessionBundleValidationError,
    load_session_bundle,
    replay_session_bundle,
    save_session_bundle,
    session_bundle_recorder,
    validate_session_bundle,
)


def _shell():
    return get_ipython()


def _stop_if_recording(shell):
    if shell.session_bundle_status()["recording"]:
        shell.stop_session_bundle()


def test_status_when_idle():
    shell = _shell()
    _stop_if_recording(shell)
    assert shell.session_bundle_status() == {"recording": False, "path": None}
    assert shell.run_line_magic("session_bundle", "status") == {
        "recording": False,
        "path": None,
    }


def test_start_stop_records_stdout_not_displayhook(tmp_path):
    shell = _shell()
    _stop_if_recording(shell)
    bundle = tmp_path / "session.ipybundle"
    started = shell.run_line_magic("session_bundle", f"start {bundle}")
    try:
        assert shell.session_bundle_status() == {"recording": True, "path": started}
        shell.run_cell("print('hello')\n1 + 2", store_history=True)
        shell.run_cell("print('again', file=__import__('sys').stderr)", store_history=True)
    finally:
        stopped = shell.run_line_magic("session_bundle", "stop")

    assert started == stopped
    assert Path(started) == bundle.resolve()
    assert shell.session_bundle_status()["recording"] is False

    with zipfile.ZipFile(bundle) as archive:
        names = set(archive.namelist())
        raw_events = archive.read("events.jsonl").decode("utf-8")
    assert names == {"metadata.json", "events.jsonl"}

    metadata, events = load_session_bundle(bundle)
    assert metadata["format"] == "ipython-session-bundle"
    assert metadata["format_version"] >= 1
    assert metadata["redactions"] == []
    assert metadata["event_count"] == 2
    assert validate_session_bundle(bundle) == []

    assert [event["seq"] for event in events] == [1, 2]
    assert events[0]["code"] == "print('hello')\n1 + 2"
    assert events[0]["success"] is True
    assert events[0]["stdout"] == "hello\n"
    assert "3" not in events[0]["stdout"]
    assert events[0]["execute_result"]["text/plain"] == "3"
    assert events[0]["stderr"] == ""
    assert events[1]["stdout"] == ""
    assert events[1]["stderr"] == "again\n"
    assert "Out[" not in raw_events


def test_failure_records_error_without_traceback_in_stdout(tmp_path):
    shell = _shell()
    _stop_if_recording(shell)
    bundle = tmp_path / "err.ipybundle"
    shell.start_session_bundle(bundle)
    try:
        shell.run_cell("raise ValueError('boom')", store_history=True)
    finally:
        shell.stop_session_bundle()

    _metadata, events = load_session_bundle(bundle)
    event = events[0]
    assert event["success"] is False
    assert event["stdout"] == ""
    assert event["error"]["ename"] == "ValueError"
    assert "boom" in event["error"]["evalue"]
    assert event["error"]["traceback"]
    assert all(isinstance(line, str) and line for line in event["error"]["traceback"])
    assert validate_session_bundle(bundle) == []


def test_start_guards_and_overwrite(tmp_path):
    shell = _shell()
    _stop_if_recording(shell)
    bundle = tmp_path / "one.ipybundle"
    shell.start_session_bundle(bundle)
    try:
        shell.run_cell("x = 'first'", store_history=False)
        with pytest.raises(RuntimeError):
            shell.start_session_bundle(bundle, overwrite=True)
        with pytest.raises(RuntimeError):
            shell.run_line_magic("session_bundle", f"start {bundle} --overwrite")
    finally:
        shell.stop_session_bundle()

    with pytest.raises(FileExistsError):
        shell.start_session_bundle(bundle)
    with pytest.raises(FileExistsError):
        shell.run_line_magic("session_bundle", f"start {bundle}")

    shell.start_session_bundle(bundle, overwrite=True)
    try:
        shell.run_cell("x = 'second'", store_history=False)
    finally:
        shell.stop_session_bundle()

    _metadata, events = load_session_bundle(bundle)
    assert len(events) == 1
    assert "second" in events[0]["code"]
    assert "first" not in events[0]["code"]


def test_redaction_order_and_replay(tmp_path):
    shell = _shell()
    _stop_if_recording(shell)
    bundle = tmp_path / "redact.ipybundle"
    shell.run_line_magic(
        "session_bundle",
        f"start {bundle} --redact secret-one --redact secret-two",
    )
    try:
        shell.run_cell(
            "print('secret-one and secret-two')\n'secret-one'",
            store_history=True,
        )
        shell.run_cell("value = 7", store_history=True)
        shell.run_cell("raise RuntimeError('secret-two failed')", store_history=True)
    finally:
        shell.stop_session_bundle()

    with zipfile.ZipFile(bundle) as archive:
        raw_events = archive.read("events.jsonl").decode("utf-8")
    assert "secret-one" not in raw_events
    assert "secret-two" not in raw_events
    assert raw_events.count("<redacted>") >= 3

    metadata, events = load_session_bundle(bundle)
    assert metadata["redactions"] == ["secret-one", "secret-two"]
    assert "<redacted>" in events[0]["stdout"]
    assert "<redacted>" in events[0]["execute_result"]["text/plain"]
    assert "<redacted>" in events[2]["error"]["evalue"]
    assert validate_session_bundle(bundle) == []

    shell.user_ns.pop("value", None)
    before = shell.execution_count
    results = replay_session_bundle(shell, bundle, stop_on_error=True, store_history=True)
    assert len(results) == 3
    assert results[2].success is False
    assert shell.execution_count == before + 3
    assert shell.user_ns["value"] == 7

    before = shell.execution_count
    replay_session_bundle(shell, bundle, stop_on_error=False, store_history=False)
    assert shell.execution_count == before


def test_context_manager_and_history_flag(tmp_path):
    shell = _shell()
    _stop_if_recording(shell)
    bundle = tmp_path / "ctx.ipybundle"
    before = shell.execution_count
    with session_bundle_recorder(shell, bundle, redact=["token"]) as recorded:
        assert shell.session_bundle_status()["path"] == recorded
        shell.run_cell("print('token')", store_history=False)
        shell.run_cell("answer = 41 + 1", store_history=False)
    assert shell.execution_count == before
    assert shell.session_bundle_status() == {"recording": False, "path": None}

    metadata, events = load_session_bundle(recorded)
    assert metadata["redactions"] == ["token"]
    assert events[0]["stdout"] == "<redacted>\n"
    assert "token" not in json.dumps(events)

    shell.user_ns.pop("answer", None)
    replay_session_bundle(shell, recorded, store_history=False)
    assert shell.execution_count == before
    assert shell.user_ns["answer"] == 42


def test_save_load_and_validation(tmp_path):
    bundle = tmp_path / "manual.ipybundle"
    meta = {
        "format": "ipython-session-bundle",
        "format_version": 1,
        "created_at": "2026-09-23T12:00:00+00:00",
        "ipython_version": "0.0.0",
        "python_version": "3.12.0",
        "platform": "test",
        "redactions": [],
        "event_count": 1,
    }
    events = [
        {
            "type": "cell",
            "seq": 1,
            "recorded_at": "2026-09-23T12:00:01+00:00",
            "execution_count": None,
            "code": "print(1)",
            "success": True,
            "stdout": "1\n",
            "stderr": "",
            "execute_result": {},
        }
    ]
    saved = save_session_bundle(bundle, meta, events)
    assert saved == bundle.resolve()
    assert load_session_bundle(bundle) == (meta, events)
    assert validate_session_bundle(bundle, strict=True) == []

    with pytest.raises(FileExistsError):
        save_session_bundle(bundle, meta, events, overwrite=False)
    save_session_bundle(bundle, meta, events, overwrite=True)

    broken = dict(meta)
    broken["event_count"] = 5
    broken["format_version"] = 0
    bad_event = dict(events[0])
    bad_event["success"] = False
    bad_event["execute_result"] = {"text/html": "<b>x</b>"}
    save_session_bundle(bundle, broken, [bad_event], overwrite=True)

    errors = validate_session_bundle(bundle, strict=False)
    assert errors
    assert any("format_version" in error for error in errors)
    assert any("event_count" in error for error in errors)
    assert any("traceback" in error for error in errors)
    assert any("text/plain" in error for error in errors)

    with pytest.raises(SessionBundleValidationError) as excinfo:
        validate_session_bundle(bundle, strict=True)
    assert excinfo.value.bundle_path == bundle.resolve()
    assert excinfo.value.errors == errors
