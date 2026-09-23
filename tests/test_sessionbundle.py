"""Tests for IPython session bundles."""

import json
import shlex
import zipfile
from datetime import datetime
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
from IPython.terminal.interactiveshell import TerminalInteractiveShell


def _shell():
    return TerminalInteractiveShell.instance()


@pytest.fixture
def shell():
    ip = _shell()
    yield ip
    controller = getattr(ip, "_session_bundle_recorder", None)
    if controller is not None and controller.recording:
        try:
            controller.stop()
        except Exception:
            controller.recording = False
            controller._remove_callbacks()
            controller._reset_capture_state()
            controller._restore_traceback_wrapper()


def _meta(**overrides):
    meta = {
        "format": "ipython-session-bundle",
        "format_version": 1,
        "created_at": "2026-09-23T00:00:00+00:00",
        "ipython_version": "9.12.0.dev",
        "python_version": "3.12.0",
        "platform": "test-platform",
        "redactions": [],
    }
    meta.update(overrides)
    return meta


def _event(seq=1, **overrides):
    event = {
        "type": "cell",
        "seq": seq,
        "recorded_at": "2026-09-23T00:00:01+00:00",
        "execution_count": seq,
        "code": "1 + 1",
        "success": True,
        "stdout": "",
        "stderr": "",
        "execute_result": {"text/plain": "2"},
    }
    event.update(overrides)
    return event


def _force_quiet(shell):
    controller = getattr(shell, "_session_bundle_recorder", None)
    if controller is not None and controller.recording:
        controller.stop()


def test_status_before_recording(shell):
    assert shell.session_bundle_status() == {"recording": False, "path": None}


def test_record_stdout_result_stderr_and_error(shell, tmp_path):
    path = tmp_path / "session.ipybundle"
    started = shell.start_session_bundle(path, redact=["SECRET", "other"])
    assert started == str(path)
    assert shell.session_bundle_status() == {"recording": True, "path": str(path)}
    try:
        shell.run_cell("print('hello SECRET')")
        shell.run_cell("123456")
        shell.run_cell("import sys; sys.stderr.write('err-SECRET')")
        shell.run_cell("raise ValueError('boom-SECRET')")
    finally:
        stopped = shell.stop_session_bundle()
    assert stopped == str(path)
    assert shell.session_bundle_status() == {"recording": False, "path": None}

    assert validate_session_bundle(path) == []
    meta, events = load_session_bundle(path)
    assert meta["format"] == "ipython-session-bundle"
    assert isinstance(meta["format_version"], int) and meta["format_version"] >= 1
    datetime.fromisoformat(meta["created_at"])
    assert meta["ipython_version"]
    assert meta["python_version"]
    assert meta["platform"]
    assert meta["redactions"] == ["SECRET", "other"]
    assert meta["event_count"] == 4
    assert [event["seq"] for event in events] == [1, 2, 3, 4]
    # Literal secret must not survive in the event stream.
    raw_events = zipfile.ZipFile(path).read("events.jsonl")
    assert b"SECRET" not in raw_events
    assert b"<redacted>" in raw_events
    assert b"SECRET" in zipfile.ZipFile(path).read("metadata.json")

    printed = events[0]
    assert printed["success"] is True
    assert printed["code"] == "print('hello <redacted>')"
    assert printed["stdout"] == "hello <redacted>\n"
    assert printed["stderr"] == ""
    assert printed["execute_result"] == {}
    assert "Out[" not in printed["stdout"]

    expr = events[1]
    assert expr["stdout"] == ""
    assert "Out[" not in json.dumps(expr)
    assert expr["execute_result"]["text/plain"] == "123456"
    assert expr["success"] is True

    err_stream = events[2]
    assert err_stream["stderr"] == "err-<redacted>"
    assert err_stream["stdout"] == ""
    assert err_stream["success"] is True

    failed = events[3]
    assert failed["success"] is False
    assert failed["error"]["ename"] == "ValueError"
    assert failed["error"]["evalue"] == "boom-<redacted>"
    assert isinstance(failed["error"]["traceback"], list)
    assert failed["error"]["traceback"]
    assert all(isinstance(line, str) for line in failed["error"]["traceback"])
    assert "Traceback" not in failed["stdout"]
    assert "SECRET" not in json.dumps(failed)


def test_stdout_excludes_displayhook(shell, tmp_path):
    path = tmp_path / "expr.ipybundle"
    shell.start_session_bundle(path)
    try:
        shell.run_cell("print('only-stdout')\n999001")
        shell.run_cell("def (:\n")
    finally:
        shell.stop_session_bundle()
    _meta, events = load_session_bundle(path)
    assert len(events) == 2
    assert events[0]["stdout"] == "only-stdout\n"
    assert events[0]["execute_result"]["text/plain"] == "999001"
    assert "999001" not in events[0]["stdout"]
    assert events[1]["success"] is False
    assert events[1]["error"]["ename"] in {"SyntaxError", "IndentationError"}
    assert events[1]["error"]["traceback"]
    assert "SyntaxError" not in events[1]["stdout"]
    assert events[1]["execute_result"] == {}


def test_start_raises_when_active_and_when_file_exists(shell, tmp_path):
    path = tmp_path / "one.ipybundle"
    other = tmp_path / "two.ipybundle"
    shell.start_session_bundle(path)
    try:
        with pytest.raises(RuntimeError):
            shell.start_session_bundle(other)
        with pytest.raises(RuntimeError):
            shell.run_line_magic("session_bundle", f"start {shlex.quote(str(other))}")
    finally:
        shell.stop_session_bundle()

    path.write_text("already here", encoding="utf-8")
    with pytest.raises(FileExistsError):
        shell.start_session_bundle(path)
    directory = tmp_path / "existing-dir.ipybundle"
    directory.mkdir()
    with pytest.raises(FileExistsError):
        shell.start_session_bundle(directory)
    shell.start_session_bundle(directory, overwrite=True)
    shell.stop_session_bundle()
    assert directory.is_file()
    assert validate_session_bundle(directory) == []
    with pytest.raises(FileExistsError):
        shell.run_line_magic("session_bundle", f"start {shlex.quote(str(path))}")

    shell.start_session_bundle(path, overwrite=True)
    try:
        shell.run_cell("fresh = 1")
    finally:
        shell.stop_session_bundle()
    _meta, events = load_session_bundle(path)
    assert len(events) == 1
    assert "fresh = 1" in events[0]["code"]
    assert path.read_bytes()[:2] == b"PK"


def test_magic_commands_and_skip_control_cells(shell, tmp_path):
    path = tmp_path / "magic.ipybundle"
    quoted = shlex.quote(str(path))
    shell.run_cell(f"%session_bundle start {quoted} --redact alpha --redact beta")
    shell.run_cell("alpha_beta = 'alpha beta'")
    status = shell.run_line_magic("session_bundle", "status")
    assert status == {"recording": True, "path": str(path)}
    shell.run_cell("%session_bundle stop")
    assert shell.session_bundle_status() == {"recording": False, "path": None}
    meta, events = load_session_bundle(path)
    assert meta["redactions"] == ["alpha", "beta"]
    assert len(events) == 1
    assert "session_bundle" not in events[0]["code"]
    assert "<redacted>" in events[0]["code"]
    assert "alpha" not in events[0]["code"]
    raw = zipfile.ZipFile(path).read("events.jsonl").decode("utf-8")
    assert "alpha" not in raw
    assert "beta" not in raw


def test_context_manager_records_and_stops_on_error(shell, tmp_path):
    path = tmp_path / "ctx.ipybundle"
    with pytest.raises(RuntimeError, match="from-body"):
        with session_bundle_recorder(shell, path, redact=["HIDE"]) as recorded:
            assert recorded == str(path)
            shell.run_cell("print('HIDE-me')")
            raise RuntimeError("from-body")
    assert shell.session_bundle_status()["recording"] is False
    meta, events = load_session_bundle(path)
    assert meta["redactions"] == ["HIDE"]
    assert events[0]["stdout"] == "<redacted>-me\n"


def test_replay_history_and_stop_on_error(shell, tmp_path):
    path = tmp_path / "replay.ipybundle"
    marker = "session_bundle_replay_marker"
    shell.user_ns.pop(marker, None)
    save_session_bundle(
        path,
        _meta(),
        [
            _event(1, code=f"raise RuntimeError('replay-stop')", success=False, execute_result={}, error={
                "ename": "RuntimeError",
                "evalue": "replay-stop",
                "traceback": ["RuntimeError: replay-stop\n"],
            }),
            _event(2, code=f"{marker} = 7", success=True, execute_result={}, execution_count=2),
        ],
    )
    loaded_meta, loaded_events = load_session_bundle(path)
    assert loaded_meta["format"] == "ipython-session-bundle"
    assert marker not in shell.user_ns

    before = shell.execution_count
    replay_session_bundle(shell, path, stop_on_error=True, store_history=False)
    assert shell.execution_count == before
    assert marker not in shell.user_ns

    before = shell.execution_count
    replay_session_bundle(shell, path, stop_on_error=True, store_history=True)
    assert shell.execution_count == before + 1
    assert marker not in shell.user_ns

    before = shell.execution_count
    replay_session_bundle(shell, path, stop_on_error=False, store_history=True)
    assert shell.execution_count == before + 2
    assert shell.user_ns[marker] == 7
    assert loaded_events[1]["code"] == f"{marker} = 7"


def test_replay_does_not_execute_during_load(shell, tmp_path):
    path = tmp_path / "noload.ipybundle"
    name = "session_bundle_load_side_effect"
    shell.user_ns.pop(name, None)
    save_session_bundle(
        path,
        _meta(),
        [_event(code=f"{name} = 12345", execute_result={})],
    )
    load_session_bundle(path)
    assert name not in shell.user_ns
    before = shell.execution_count
    replay_session_bundle(shell, path, store_history=True)
    assert shell.user_ns[name] == 12345
    assert shell.execution_count == before + 1
    before = shell.execution_count
    replay_session_bundle(shell, path, store_history=False)
    assert shell.execution_count == before


def test_replay_counts_blank_cells_and_bundle_updates_live(shell, tmp_path):
    live = tmp_path / "live.ipybundle"
    shell.start_session_bundle(live)
    try:
        shell.run_cell("print('partial')")
        meta, events = load_session_bundle(live)
        assert meta["event_count"] == 1
        assert events[0]["stdout"] == "partial\n"
    finally:
        shell.stop_session_bundle()

    blanks = tmp_path / "blanks.ipybundle"
    save_session_bundle(
        blanks,
        _meta(event_count=2),
        [
            _event(1, code="   ", execute_result={}, execution_count=None),
            _event(2, code="", execute_result={}, execution_count=None),
        ],
    )
    before = shell.execution_count
    replay_session_bundle(shell, blanks, store_history=True)
    assert shell.execution_count == before + 2
    before = shell.execution_count
    replay_session_bundle(shell, blanks, store_history=False)
    assert shell.execution_count == before


def test_save_overwrite_and_validation(tmp_path):
    path = tmp_path / "saved.ipybundle"
    events = [_event(), _event(seq=2, code="x = 1", execute_result={})]
    saved = save_session_bundle(path, _meta(event_count=2), events)
    assert saved == path
    assert validate_session_bundle(path, strict=True) == []
    with pytest.raises(FileExistsError):
        save_session_bundle(path, _meta(), events, overwrite=False)
    replaced = save_session_bundle(
        path,
        _meta(event_count=1, redactions=["later"]),
        [_event(code="y = 2")],
        overwrite=True,
    )
    assert replaced == path
    meta, loaded = load_session_bundle(path)
    assert meta["redactions"] == ["later"]
    assert len(loaded) == 1
    assert loaded[0]["code"] == "y = 2"

    bad = tmp_path / "bad.ipybundle"
    save_session_bundle(
        bad,
        _meta(format_version=0, event_count=2),
        [
            _event(seq=1, success=False, execute_result={"image/png": "zz"}),
            _event(seq=3),
        ],
    )
    errors = validate_session_bundle(bad, strict=False)
    assert errors
    assert any("format_version" in message for message in errors)
    assert any("text/plain" in message for message in errors)
    assert any("seq" in message for message in errors)
    assert any("error is required" in message for message in errors)
    with pytest.raises(SessionBundleValidationError) as excinfo:
        validate_session_bundle(bad, strict=True)
    assert excinfo.value.bundle_path == bad
    assert excinfo.value.errors == errors

    empty_tb = tmp_path / "empty-tb.ipybundle"
    save_session_bundle(
        empty_tb,
        _meta(),
        [
            _event(
                success=False,
                execute_result={},
                error={"ename": "ValueError", "evalue": "x", "traceback": []},
            )
        ],
    )
    tb_errors = validate_session_bundle(empty_tb, strict=False)
    assert any("traceback" in message for message in tb_errors)

    not_zip = tmp_path / "not-a-bundle.ipybundle"
    not_zip.write_text("hello", encoding="utf-8")
    assert validate_session_bundle(not_zip, strict=False)
    with pytest.raises(SessionBundleValidationError):
        validate_session_bundle(not_zip)

    missing = tmp_path / "missing.ipybundle"
    assert validate_session_bundle(missing, strict=False)
    future = tmp_path / "future.ipybundle"
    save_session_bundle(
        future,
        _meta(format_version=2, created_at="2026-09-23T00:00:00Z"),
        [_event()],
    )
    assert validate_session_bundle(future) == []


def test_stop_without_recording_raises(shell):
    _force_quiet(shell)
    with pytest.raises(RuntimeError):
        shell.stop_session_bundle()


def test_execution_count_null_is_valid(tmp_path):
    path = tmp_path / "null-count.ipybundle"
    save_session_bundle(
        path,
        _meta(),
        [_event(execution_count=None, execute_result={})],
    )
    assert validate_session_bundle(path) == []
    _meta_loaded, events = load_session_bundle(path)
    assert events[0]["execution_count"] is None
