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


@pytest.fixture
def shell():
    ip = get_ipython()
    if ip.session_bundle_status()["recording"]:
        ip.stop_session_bundle()
    return ip


def _events(path):
    _meta, events = load_session_bundle(path)
    return events


def test_status_before_recording(shell):
    assert shell.session_bundle_status() == {"recording": False, "path": None}
    assert shell.run_line_magic("session_bundle", "status") == {
        "recording": False,
        "path": None,
    }


def test_record_stdout_result_and_replay(shell, tmp_path):
    path = tmp_path / "session.ipybundle"
    ns = "session_bundle_value"
    shell.user_ns.pop(ns, None)
    before = shell.execution_count

    started = shell.run_line_magic("session_bundle", f"start {path}")
    assert started == str(path)
    assert shell.session_bundle_status() == {"recording": True, "path": str(path)}

    shell.run_cell("print('hello')\n1 + 1", store_history=True)
    shell.run_cell(f"{ns} = 7", store_history=True)
    shell.run_cell("print('err', file=__import__('sys').stderr)", store_history=True)

    stopped = shell.run_line_magic("session_bundle", "stop")
    assert stopped == str(path)
    assert shell.session_bundle_status() == {"recording": False, "path": None}
    assert validate_session_bundle(path) == []

    meta, events = load_session_bundle(path)
    assert meta["format"] == "ipython-session-bundle"
    assert meta["format_version"] >= 1
    assert meta["redactions"] == []
    assert meta["event_count"] == 3
    assert [event["seq"] for event in events] == [1, 2, 3]
    assert events[0]["stdout"] == "hello\n"
    assert "2" not in events[0]["stdout"]
    assert "Out[" not in events[0]["stdout"]
    assert events[0]["execute_result"]["text/plain"] == "2"
    assert events[0]["success"] is True
    assert events[0]["execution_count"] == before
    assert events[1]["code"] == f"{ns} = 7"
    assert events[1]["execute_result"] == {}
    assert events[1]["stdout"] == ""
    assert events[2]["stderr"] == "err\n"
    assert events[2]["stdout"] == ""

    # Loading does not execute.
    shell.user_ns.pop(ns, None)
    load_session_bundle(path)
    assert ns not in shell.user_ns

    shell.user_ns.pop(ns, None)
    count = shell.execution_count
    replay_session_bundle(shell, path, store_history=True)
    assert shell.user_ns[ns] == 7
    assert shell.execution_count == count + len(events)

    shell.user_ns.pop(ns, None)
    count = shell.execution_count
    replay_session_bundle(shell, path, store_history=False)
    assert shell.user_ns[ns] == 7
    assert shell.execution_count == count


def test_failure_records_error_and_stop_on_error(shell, tmp_path):
    path = tmp_path / "errors.ipybundle"
    shell.start_session_bundle(path)
    shell.run_cell("print('before')\nraise RuntimeError('boom')")
    shell.run_cell("after_error = 1")
    shell.stop_session_bundle()

    events = _events(path)
    assert events[0]["success"] is False
    assert events[0]["stdout"] == "before\n"
    assert "Traceback" not in events[0]["stdout"]
    error = events[0]["error"]
    assert error["ename"] == "RuntimeError"
    assert "boom" in error["evalue"]
    assert isinstance(error["traceback"], list) and error["traceback"]
    assert all(isinstance(line, str) and line for line in error["traceback"])
    assert events[1]["success"] is True

    shell.user_ns.pop("after_error", None)
    replay_session_bundle(shell, path, stop_on_error=True, store_history=False)
    assert "after_error" not in shell.user_ns

    replay_session_bundle(shell, path, stop_on_error=False, store_history=False)
    assert shell.user_ns["after_error"] == 1
    assert validate_session_bundle(path, strict=True) == []


def test_redaction_is_literal_and_ordered(shell, tmp_path):
    path = tmp_path / "redact.ipybundle"
    shell.start_session_bundle(path, redact=["ab", "secret"])
    shell.run_cell("print('secret ab secret')\n'secret'")
    shell.run_cell("raise ValueError('secret')")
    shell.stop_session_bundle()

    meta, events = load_session_bundle(path)
    assert meta["redactions"] == ["ab", "secret"]
    raw = zipfile.ZipFile(path).read("events.jsonl").decode()
    assert "secret" not in raw
    assert "ab" not in raw
    assert "<redacted>" in raw
    assert events[0]["stdout"] == "<redacted> <redacted> <redacted>\n"
    assert events[0]["code"] == "print('<redacted> <redacted> <redacted>')\n'<redacted>'"
    assert "secret" not in events[0]["execute_result"]["text/plain"]
    assert "secret" not in events[1]["error"]["evalue"]
    assert all("secret" not in line for line in events[1]["error"]["traceback"])


def test_redact_magic_order_and_literal_dot(shell, tmp_path):
    path = tmp_path / "magic-redact.ipybundle"
    shell.run_line_magic(
        "session_bundle",
        f"start {path} --redact a.b --redact aaa --redact bbb",
    )
    shell.run_cell("print('aaa bbb a.b axb')")
    shell.run_line_magic("session_bundle", "stop")
    meta, events = load_session_bundle(path)
    assert meta["redactions"] == ["a.b", "aaa", "bbb"]
    assert events[0]["stdout"] == "<redacted> <redacted> <redacted> axb\n"
    raw = zipfile.ZipFile(path).read("events.jsonl").decode()
    assert "a.b" not in raw
    assert "axb" in raw


def test_start_guards_and_overwrite(shell, tmp_path):
    path = tmp_path / "once.ipybundle"
    shell.start_session_bundle(path)
    shell.run_cell("first = 1")
    with pytest.raises(RuntimeError):
        shell.start_session_bundle(path, overwrite=True)
    with pytest.raises(RuntimeError):
        shell.run_line_magic("session_bundle", f"start {path} --overwrite")
    assert shell.session_bundle_status()["recording"] is True
    shell.stop_session_bundle()

    with pytest.raises(FileExistsError):
        shell.start_session_bundle(path)
    with pytest.raises(FileExistsError):
        shell.run_line_magic("session_bundle", f"start {path}")

    shell.start_session_bundle(path, overwrite=True)
    shell.run_cell("second = 2")
    shell.stop_session_bundle()
    events = _events(path)
    assert len(events) == 1
    assert events[0]["seq"] == 1
    assert "second = 2" in events[0]["code"]
    assert "first = 1" not in events[0]["code"]


def test_save_refuses_existing_file(tmp_path):
    path = tmp_path / "saved.ipybundle"
    meta = {
        "format": "ipython-session-bundle",
        "format_version": 1,
        "created_at": "2026-09-23T00:00:00+00:00",
        "ipython_version": "0",
        "python_version": "3.12.0",
        "platform": "test",
        "redactions": [],
        "event_count": 0,
    }
    written = save_session_bundle(path, meta, [])
    assert written == path
    assert isinstance(written, Path)
    with pytest.raises(FileExistsError):
        save_session_bundle(path, meta, [])
    save_session_bundle(path, meta, [], overwrite=True)


def test_context_manager_stops_on_error(shell, tmp_path):
    path = tmp_path / "ctx.ipybundle"
    with pytest.raises(RuntimeError, match="from-body"):
        with session_bundle_recorder(shell, path, redact=["hide-me"]):
            shell.run_cell("print('hide-me')")
            raise RuntimeError("from-body")
    assert shell.session_bundle_status()["recording"] is False
    meta, events = load_session_bundle(path)
    assert meta["redactions"] == ["hide-me"]
    assert events[0]["stdout"] == "<redacted>\n"
    assert "hide-me" not in zipfile.ZipFile(path).read("events.jsonl").decode()


def test_validation_errors(tmp_path):
    path = tmp_path / "bad.ipybundle"
    meta = {
        "format": "nope",
        "format_version": 0,
        "created_at": "yesterday",
        "ipython_version": "0",
        "python_version": "3",
        "platform": "test",
        "redactions": ["LEAK"],
        "event_count": 2,
    }
    event = {
        "type": "not-cell",
        "seq": 4,
        "recorded_at": "nope",
        "execution_count": "1",
        "code": "LEAK",
        "success": False,
        "stdout": "",
        "stderr": "",
        "execute_result": {"image/png": "x"},
    }
    save_session_bundle(path, meta, [event])
    errors = validate_session_bundle(path, strict=False)
    assert errors
    assert all(isinstance(item, str) and item for item in errors)
    blob = "\n".join(errors)
    assert "format" in blob
    assert "format_version" in blob
    assert "seq" in blob
    assert "traceback" in blob
    assert "text/plain" in blob
    assert "LEAK" in blob or "redaction" in blob

    with pytest.raises(SessionBundleValidationError) as exc_info:
        validate_session_bundle(path, strict=True)
    err = exc_info.value
    assert err.bundle_path == path
    assert err.errors == errors

    missing = tmp_path / "missing.ipybundle"
    soft = validate_session_bundle(missing, strict=False)
    assert soft and all(isinstance(item, str) for item in soft)
    with pytest.raises(SessionBundleValidationError) as missing_info:
        validate_session_bundle(missing)
    assert missing_info.value.bundle_path == missing
    assert missing_info.value.errors == soft


def test_bundle_members_are_json(shell, tmp_path):
    path = tmp_path / "members.ipybundle"
    with session_bundle_recorder(shell, path, overwrite=True):
        shell.run_cell("print('z')")
    with zipfile.ZipFile(path) as zf:
        assert set(zf.namelist()) == {"metadata.json", "events.jsonl"}
        metadata = json.loads(zf.read("metadata.json"))
        lines = zf.read("events.jsonl").decode().splitlines()
    assert metadata["format"] == "ipython-session-bundle"
    assert len(lines) == 1
    assert json.loads(lines[0])["stdout"] == "z\n"


def test_stop_without_recording_raises(shell):
    with pytest.raises(RuntimeError):
        shell.stop_session_bundle()
    with pytest.raises(RuntimeError):
        shell.run_line_magic("session_bundle", "stop")


def test_magic_cells_are_not_recorded_and_usage(shell, tmp_path):
    path = tmp_path / "via-magic.ipybundle"
    quoted = tmp_path / "quoted bundle.ipybundle"
    shell.run_cell(f"%session_bundle start {path}")
    shell.run_cell("magic_recorded = 5")
    shell.run_cell("%session_bundle stop")
    events = _events(path)
    assert len(events) == 1
    assert events[0]["code"] == "magic_recorded = 5"
    assert shell.session_bundle_status()["recording"] is False

    shell.run_cell(f"%session_bundle start '{quoted}' --overwrite --redact 'a b'")
    shell.run_cell("print('a b')")
    shell.run_cell("%session_bundle stop")
    meta, events = load_session_bundle(quoted)
    assert meta["redactions"] == ["a b"]
    assert events[0]["stdout"] == "<redacted>\n"
    assert validate_session_bundle(quoted) == []

    with pytest.raises(Exception):
        shell.run_line_magic("session_bundle", "")
    with pytest.raises(Exception):
        shell.run_line_magic("session_bundle", "start")


def test_syntax_error_semicolon_and_failed_replay_count(shell, tmp_path):
    path = tmp_path / "syntax.ipybundle"
    shell.start_session_bundle(path)
    shell.run_cell("(", store_history=True)
    shell.run_cell("1 + 1;", store_history=True)
    shell.run_cell("print('kept')\nraise ZeroDivisionError('nope')", store_history=True)
    shell.stop_session_bundle()
    assert validate_session_bundle(path) == []
    events = _events(path)
    assert events[0]["success"] is False
    assert events[0]["error"]["ename"] in {"SyntaxError", "IndentationError"}
    assert events[0]["error"]["traceback"]
    assert events[1]["success"] is True
    assert events[1]["execute_result"] == {}
    assert events[1]["stdout"] == ""
    assert events[2]["success"] is False
    assert events[2]["stdout"] == "kept\n"
    assert events[2]["error"]["ename"] == "ZeroDivisionError"

    before = shell.execution_count
    replay_session_bundle(shell, path, stop_on_error=True, store_history=True)
    # Syntax error stops the replay, and that cell still consumes one count.
    assert shell.execution_count == before + 1


def test_blank_cell_replay_advances_count_only_with_history(shell, tmp_path):
    path = tmp_path / "blank.ipybundle"
    shell.start_session_bundle(path)
    shell.run_cell("   \n")
    shell.run_cell("blank_marker = 1")
    shell.stop_session_bundle()
    events = _events(path)
    assert len(events) == 2
    assert events[0]["execution_count"] is None
    assert events[0]["success"] is True

    shell.user_ns.pop("blank_marker", None)
    before = shell.execution_count
    replay_session_bundle(shell, path, store_history=True)
    assert shell.execution_count == before + 2
    assert shell.user_ns["blank_marker"] == 1

    before = shell.execution_count
    replay_session_bundle(shell, path, store_history=False)
    assert shell.execution_count == before
