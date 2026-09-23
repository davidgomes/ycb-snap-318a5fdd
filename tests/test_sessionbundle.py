import json
import zipfile

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
    yield ip
    if ip.session_bundle_status()["recording"]:
        ip.stop_session_bundle()


def _events_text(path):
    with zipfile.ZipFile(path) as zf:
        return zf.read("events.jsonl").decode("utf-8")


def test_record_and_load(shell, tmp_path):
    path = tmp_path / "s.ipybundle"
    shell.run_cell(f"%session_bundle start {path}", store_history=True)
    assert shell.run_cell("%session_bundle status").result == {
        "recording": True,
        "path": str(path),
    }
    shell.run_cell("x = 20\nprint('hello')", store_history=True)
    shell.run_cell("x + 1", store_history=True)
    shell.run_cell("import sys; sys.stderr.write('warn\\n')", store_history=True)
    shell.run_cell("1/0", store_history=True)
    assert shell.run_cell("%session_bundle stop").result == str(path)
    assert shell.session_bundle_status() == {"recording": False, "path": None}

    meta, events = load_session_bundle(path)
    assert meta["format"] == "ipython-session-bundle"
    assert meta["format_version"] >= 1
    assert meta["redactions"] == []
    assert meta["event_count"] == len(events) == 5
    assert [e["seq"] for e in events] == [1, 2, 3, 4, 5]

    # The "status" cell is recorded; "start" and "stop" are not.
    assert events[0]["code"] == "%session_bundle status"
    assert events[1]["code"] == "x = 20\nprint('hello')"
    assert events[1]["stdout"] == "hello\n"
    assert events[1]["execute_result"] == {}
    assert events[2]["stdout"] == ""
    assert events[2]["execute_result"]["text/plain"] == "21"
    assert events[3]["stderr"] == "warn\n"
    assert events[4]["success"] is False
    assert events[4]["error"]["ename"] == "ZeroDivisionError"
    assert events[4]["error"]["traceback"]
    assert events[4]["stdout"] == ""
    assert all(e["success"] for e in events[:4])
    assert validate_session_bundle(path) == []


def test_start_twice_and_overwrite(shell, tmp_path):
    path = tmp_path / "s.ipybundle"
    shell.start_session_bundle(path)
    with pytest.raises(RuntimeError):
        shell.start_session_bundle(tmp_path / "other.ipybundle")
    shell.run_cell("a = 1", store_history=True)
    shell.stop_session_bundle()

    with pytest.raises(FileExistsError):
        shell.run_line_magic("session_bundle", f"start {path}")
    shell.run_line_magic("session_bundle", f"start {path} --overwrite")
    shell.stop_session_bundle()
    _, events = load_session_bundle(path)
    assert events == []


def test_redaction(shell, tmp_path):
    path = tmp_path / "s.ipybundle"
    shell.run_line_magic(
        "session_bundle", f"start {path} --redact hunter2 --redact s3cr3t"
    )
    shell.run_cell("pw = 'hunter2'\nprint(pw, 's3cr3t')", store_history=True)
    shell.run_cell("pw", store_history=True)
    shell.run_cell("raise ValueError(pw)", store_history=True)
    shell.stop_session_bundle()

    meta, events = load_session_bundle(path)
    assert meta["redactions"] == ["hunter2", "s3cr3t"]
    text = _events_text(path)
    assert "hunter2" not in text and "s3cr3t" not in text
    assert events[0]["stdout"] == "<redacted> <redacted>\n"
    assert validate_session_bundle(path) == []


def test_recorder_and_replay(shell, tmp_path):
    path = tmp_path / "s.ipybundle"
    with session_bundle_recorder(shell, path) as bundle:
        assert bundle == str(path)
        shell.run_cell("replay_value = 41", store_history=True)
        shell.run_cell("replay_value += 1", store_history=True)
    assert not shell.session_bundle_status()["recording"]

    shell.user_ns.pop("replay_value")
    before = shell.execution_count
    results = replay_session_bundle(shell, path)
    assert len(results) == 2
    assert shell.user_ns["replay_value"] == 42
    assert shell.execution_count == before + 2

    before = shell.execution_count
    replay_session_bundle(shell, path, store_history=False)
    assert shell.execution_count == before


def test_replay_stop_on_error(shell, tmp_path):
    path = tmp_path / "s.ipybundle"
    with session_bundle_recorder(shell, path):
        shell.run_cell("undefined_name_xyz", store_history=True)
        shell.run_cell("after_error = True", store_history=True)
    shell.user_ns.pop("after_error")
    results = replay_session_bundle(shell, path)
    assert len(results) == 1
    assert "after_error" not in shell.user_ns
    replay_session_bundle(shell, path, stop_on_error=False)
    assert shell.user_ns.pop("after_error") is True


def test_save_and_validate(tmp_path):
    path = tmp_path / "b.ipybundle"
    meta = {
        "format": "ipython-session-bundle",
        "format_version": 1,
        "created_at": "2024-01-01T00:00:00+00:00",
        "ipython_version": "9",
        "python_version": "3",
        "platform": "x",
        "redactions": [],
        "event_count": 1,
    }
    event = {
        "type": "cell",
        "seq": 1,
        "recorded_at": "2024-01-01T00:00:00+00:00",
        "execution_count": 1,
        "code": "1",
        "success": True,
        "stdout": "",
        "stderr": "",
        "execute_result": {"text/plain": "1"},
    }
    assert save_session_bundle(path, meta, [event]) == path
    assert validate_session_bundle(path) == []
    with pytest.raises(FileExistsError):
        save_session_bundle(path, meta, [event])

    bad = dict(event, seq=2, success=False)
    save_session_bundle(path, dict(meta, event_count=3), [bad], overwrite=True)
    errors = validate_session_bundle(path, strict=False)
    assert any("seq" in e for e in errors)
    assert any("event_count" in e for e in errors)
    assert any("error" in e for e in errors)
    with pytest.raises(SessionBundleValidationError) as excinfo:
        validate_session_bundle(path)
    assert excinfo.value.bundle_path == path
    assert excinfo.value.errors == errors
