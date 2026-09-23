"""Tests for IPython.core.sessionbundle and the %session_bundle magic."""

import json
import platform
import zipfile
from datetime import datetime
from pathlib import Path

import pytest

from IPython.core import release
from IPython.core.error import UsageError
from IPython.core.sessionbundle import (
    SessionBundleValidationError,
    load_session_bundle,
    replay_session_bundle,
    save_session_bundle,
    session_bundle_recorder,
    validate_session_bundle,
)


@pytest.fixture
def ip():
    shell = get_ipython()
    yield shell
    if shell.session_bundle_status()["recording"]:
        shell.stop_session_bundle()


def record(ip, path, cells, **kwargs):
    with session_bundle_recorder(ip, path, **kwargs):
        for cell in cells:
            ip.run_cell(cell, store_history=True)
    return load_session_bundle(path)


def read_member(path, name):
    with zipfile.ZipFile(path) as zf:
        return zf.read(name).decode("utf-8")


def make_meta(**overrides):
    meta = {
        "format": "ipython-session-bundle",
        "format_version": 1,
        "created_at": "2026-01-01T00:00:00+00:00",
        "ipython_version": "9.0.0",
        "python_version": "3.12.0",
        "platform": "test-platform",
        "redactions": [],
    }
    meta.update(overrides)
    return meta


def make_event(seq, **overrides):
    event = {
        "type": "cell",
        "seq": seq,
        "recorded_at": "2026-01-01T00:00:00+00:00",
        "execution_count": seq,
        "code": f"x = {seq}",
        "success": True,
        "stdout": "",
        "stderr": "",
        "execute_result": {},
    }
    event.update(overrides)
    return event


def test_magic_start_status_stop(ip, tmp_path):
    path = tmp_path / "session.ipybundle"
    status = ip.run_line_magic("session_bundle", "status")
    assert status == {"recording": False, "path": None}

    assert ip.run_line_magic("session_bundle", f"start {path}") == str(path)
    status = ip.run_line_magic("session_bundle", "status")
    assert status == {"recording": True, "path": str(path)}
    assert ip.session_bundle_status() == status

    ip.run_cell("a = 1", store_history=True)
    assert ip.run_line_magic("session_bundle", "stop") == str(path)
    status = ip.run_line_magic("session_bundle", "status")
    assert status == {"recording": False, "path": None}

    with zipfile.ZipFile(path) as zf:
        assert sorted(zf.namelist()) == ["events.jsonl", "metadata.json"]
    _, events = load_session_bundle(path)
    assert [event["code"] for event in events] == ["a = 1"]


@pytest.mark.parametrize(
    "line", ["", "start", "restart x.ipybundle", "stop extra", "status --overwrite"]
)
def test_magic_usage_errors(ip, line):
    with pytest.raises(UsageError):
        ip.run_line_magic("session_bundle", line)
    assert not ip.session_bundle_status()["recording"]


def test_start_while_recording_raises(ip, tmp_path):
    first = tmp_path / "first.ipybundle"
    second = tmp_path / "second.ipybundle"
    ip.start_session_bundle(first)
    with pytest.raises(RuntimeError):
        ip.run_line_magic("session_bundle", f"start {second}")
    with pytest.raises(RuntimeError):
        ip.start_session_bundle(second)
    assert ip.session_bundle_status() == {"recording": True, "path": str(first)}
    assert not second.exists()


def test_stop_without_recording_raises(ip):
    with pytest.raises(RuntimeError):
        ip.stop_session_bundle()
    with pytest.raises(RuntimeError):
        ip.run_line_magic("session_bundle", "stop")


def test_start_on_existing_path_requires_overwrite(ip, tmp_path):
    path = tmp_path / "session.ipybundle"
    record(ip, path, ["old = 1"])

    with pytest.raises(FileExistsError):
        ip.run_line_magic("session_bundle", f"start {path}")
    with pytest.raises(FileExistsError):
        ip.start_session_bundle(path)
    assert not ip.session_bundle_status()["recording"]
    assert [e["code"] for e in load_session_bundle(path)[1]] == ["old = 1"]

    ip.run_line_magic("session_bundle", f"start {path} --overwrite")
    assert load_session_bundle(path)[1] == []
    ip.run_cell("new = 2", store_history=True)
    ip.run_line_magic("session_bundle", "stop")

    _, events = load_session_bundle(path)
    assert [(e["seq"], e["code"]) for e in events] == [(1, "new = 2")]


def test_relative_path_is_made_absolute(ip, tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    assert ip.start_session_bundle("rel.ipybundle") == str(tmp_path / "rel.ipybundle")
    assert ip.stop_session_bundle() == str(tmp_path / "rel.ipybundle")


def test_metadata(ip, tmp_path):
    meta, events = record(ip, tmp_path / "s.ipybundle", ["a = 1", "b = 2"])
    assert meta["format"] == "ipython-session-bundle"
    assert meta["format_version"] >= 1
    datetime.fromisoformat(meta["created_at"])
    assert meta["ipython_version"] == release.version
    assert meta["python_version"] == platform.python_version()
    assert isinstance(meta["platform"], str) and meta["platform"]
    assert meta["redactions"] == []
    assert meta["event_count"] == len(events) == 2


def test_bundle_is_updated_after_each_cell(ip, tmp_path):
    path = tmp_path / "s.ipybundle"
    ip.start_session_bundle(path)
    ip.run_cell("a = 1", store_history=True)
    meta, events = load_session_bundle(path)
    assert meta["event_count"] == 1
    assert [e["code"] for e in events] == ["a = 1"]
    assert validate_session_bundle(path) == []


def test_cell_events_capture_outputs(ip, tmp_path):
    _, events = record(
        ip,
        tmp_path / "s.ipybundle",
        [
            "print('hello')",
            "40 + 2",
            "import sys\nn = sys.stderr.write('oops\\n')",
            "print('both')\n'value'",
            "1 + 1;",
        ],
    )
    assert [e["seq"] for e in events] == [1, 2, 3, 4, 5]
    assert all(e["type"] == "cell" and e["success"] is True for e in events)
    for event in events:
        datetime.fromisoformat(event["recorded_at"])
    counts = [e["execution_count"] for e in events]
    assert all(isinstance(c, int) for c in counts)
    assert counts == sorted(counts) and len(set(counts)) == len(counts)

    hello, answer, stderr, both, quiet = events
    assert hello["stdout"] == "hello\n"
    assert hello["execute_result"] == {}
    assert answer["stdout"] == ""
    assert answer["execute_result"]["text/plain"] == "42"
    assert stderr["stdout"] == ""
    assert stderr["stderr"] == "oops\n"
    assert both["stdout"] == "both\n"
    assert both["execute_result"]["text/plain"] == "'value'"
    assert quiet["execute_result"] == {}


def test_failed_cells_record_error(ip, tmp_path):
    _, events = record(
        ip,
        tmp_path / "s.ipybundle",
        ["1/0", "def f(:", "%this_magic_does_not_exist", "ok = 1"],
    )
    zero, syntax, usage, ok = events
    for event, ename in [
        (zero, "ZeroDivisionError"),
        (syntax, "SyntaxError"),
        (usage, "UsageError"),
    ]:
        assert event["success"] is False
        assert event["error"]["ename"] == ename
        assert isinstance(event["error"]["evalue"], str)
        traceback = event["error"]["traceback"]
        assert traceback and all(isinstance(line, str) for line in traceback)
    assert zero["error"]["evalue"] == "division by zero"
    assert zero["stdout"] == ""
    assert ok["success"] is True and "error" not in ok


def test_cell_without_history_has_null_execution_count(ip, tmp_path):
    path = tmp_path / "s.ipybundle"
    with session_bundle_recorder(ip, path):
        ip.run_cell("no_history = 1", store_history=False)
        ip.run_cell("with_history = 1", store_history=True)
    no_history, with_history = load_session_bundle(path)[1]
    assert no_history["execution_count"] is None
    assert isinstance(with_history["execution_count"], int)


def test_magic_cells_controlling_the_recording_are_not_recorded(ip, tmp_path):
    path = tmp_path / "s.ipybundle"
    ip.run_cell(f"%session_bundle start {path}", store_history=True)
    ip.run_cell("%session_bundle status", store_history=True)
    ip.run_cell("%session_bundle stop", store_history=True)
    _, events = load_session_bundle(path)
    assert [e["code"] for e in events] == ["%session_bundle status"]
    assert "'recording': True" in events[0]["execute_result"]["text/plain"]


def test_nested_cells_belong_to_the_outer_cell(ip, tmp_path):
    _, events = record(
        ip,
        tmp_path / "s.ipybundle",
        ["get_ipython().run_cell(\"print('inner')\")\nprint('outer')", "after = 1"],
    )
    assert [e["code"] for e in events][1] == "after = 1"
    assert events[0]["stdout"] == "inner\nouter\n"
    assert [e["seq"] for e in events] == [1, 2]


def test_redaction(ip, tmp_path):
    path = tmp_path / "s.ipybundle"
    meta, events = record(
        ip,
        path,
        [
            "secret = 'hunter2'",
            "import sys\nn = sys.stdout.write('hun')\nn = sys.stdout.write('ter2\\n')",
            "print('TOK' + 'EN-123', file=sys.stderr)",
            "secret",
            "raise ValueError(secret)",
        ],
        redact=["hunter2", "TOKEN-123"],
    )
    text = read_member(path, "events.jsonl")
    assert "hunter2" not in text
    assert "TOKEN-123" not in text
    assert meta["redactions"] == ["hunter2", "TOKEN-123"]

    assign, split_write, stderr, value, error = events
    assert assign["code"] == "secret = '<redacted>'"
    assert split_write["stdout"] == "<redacted>\n"
    assert stderr["stderr"] == "<redacted>\n"
    assert value["execute_result"]["text/plain"] == "'<redacted>'"
    assert error["error"]["evalue"] == "<redacted>"
    assert validate_session_bundle(path) == []


def test_redaction_patterns_keep_their_order_and_prefer_longest(ip, tmp_path):
    path = tmp_path / "s.ipybundle"
    ip.run_line_magic(
        "session_bundle", f"start {path} --redact zeta --redact zeta-long"
    )
    ip.run_cell("print('zeta zeta-long')", store_history=True)
    ip.run_line_magic("session_bundle", "stop")
    meta, events = load_session_bundle(path)
    assert meta["redactions"] == ["zeta", "zeta-long"]
    assert events[0]["stdout"] == "<redacted> <redacted>\n"


def test_empty_redaction_pattern_is_rejected(ip, tmp_path):
    path = tmp_path / "s.ipybundle"
    with pytest.raises(ValueError):
        ip.start_session_bundle(path, redact=["ok", ""])
    assert not ip.session_bundle_status()["recording"]
    assert not path.exists()


def test_recorder_context_manager(ip, tmp_path):
    path = tmp_path / "s.ipybundle"
    with session_bundle_recorder(ip, path, redact="hunter2") as bundle_path:
        assert bundle_path == str(path)
        assert ip.session_bundle_status() == {"recording": True, "path": str(path)}
        ip.run_cell("print('hunter2')", store_history=True)
    assert not ip.session_bundle_status()["recording"]
    meta, events = load_session_bundle(path)
    assert meta["redactions"] == ["hunter2"]
    assert events[0]["stdout"] == "<redacted>\n"

    with pytest.raises(FileExistsError):
        with session_bundle_recorder(ip, path):
            pass
    with pytest.raises(KeyError):
        with session_bundle_recorder(ip, path, overwrite=True):
            ip.run_cell("fresh = 1", store_history=True)
            raise KeyError("boom")
    assert not ip.session_bundle_status()["recording"]
    assert [e["code"] for e in load_session_bundle(path)[1]] == ["fresh = 1"]

    with session_bundle_recorder(ip, path, overwrite=True):
        ip.stop_session_bundle()
    assert not ip.session_bundle_status()["recording"]


def test_load_does_not_execute_code(ip, tmp_path):
    path = tmp_path / "s.ipybundle"
    record(ip, path, ["loaded_marker = 'executed'"])
    ip.user_ns["loaded_marker"] = "untouched"
    meta, events = load_session_bundle(path)
    assert meta["format"] == "ipython-session-bundle"
    assert events[0]["code"] == "loaded_marker = 'executed'"
    assert ip.user_ns["loaded_marker"] == "untouched"


def test_replay_advances_execution_count_only_with_history(ip, tmp_path):
    path = tmp_path / "s.ipybundle"
    record(ip, path, ["replayed = 1", "replayed += 1", "print(replayed)"])

    ip.user_ns.pop("replayed")
    count = ip.execution_count
    results = replay_session_bundle(ip, path)
    assert ip.execution_count == count + 3
    assert ip.user_ns["replayed"] == 2
    assert len(results) == 3 and all(r.success for r in results)

    count = ip.execution_count
    results = replay_session_bundle(ip, path, store_history=False)
    assert ip.execution_count == count
    assert ip.user_ns["replayed"] == 2
    assert len(results) == 3


def test_replay_stop_on_error(ip, tmp_path):
    path = tmp_path / "s.ipybundle"
    record(ip, path, ["before_error = 1", "1/0", "after_error = 1"])

    ip.user_ns.pop("before_error")
    ip.user_ns.pop("after_error")
    results = replay_session_bundle(ip, path)
    assert [r.success for r in results] == [True, False]
    assert ip.user_ns["before_error"] == 1
    assert "after_error" not in ip.user_ns

    results = replay_session_bundle(ip, path, stop_on_error=False)
    assert [r.success for r in results] == [True, False, True]
    assert ip.user_ns["after_error"] == 1


def test_replay_refuses_invalid_bundle(ip, tmp_path):
    path = tmp_path / "s.ipybundle"
    save_session_bundle(
        path, make_meta(), [make_event(2, code="should_not_run = 1")]
    )
    with pytest.raises(SessionBundleValidationError):
        replay_session_bundle(ip, path)
    assert "should_not_run" not in ip.user_ns


def test_save_session_bundle(tmp_path):
    path = tmp_path / "s.ipybundle"
    meta = make_meta(event_count=2)
    events = [make_event(1, code="café = '☕'"), make_event(2)]
    assert save_session_bundle(path, meta, events) == path
    assert load_session_bundle(path) == (meta, events)
    assert validate_session_bundle(path) == []
    assert json.loads(read_member(path, "metadata.json")) == meta
    lines = read_member(path, "events.jsonl").splitlines()
    assert [json.loads(line) for line in lines] == events

    with pytest.raises(FileExistsError):
        save_session_bundle(path, make_meta(), [])
    assert load_session_bundle(path) == (meta, events)
    assert save_session_bundle(path, make_meta(), [], overwrite=True) == path
    assert load_session_bundle(path) == (make_meta(), [])


def test_validate_recorded_bundle(ip, tmp_path):
    path = tmp_path / "s.ipybundle"
    record(ip, path, ["print(1)", "2", "1/0"], redact=["abc"])
    assert validate_session_bundle(path) == []
    assert validate_session_bundle(path, strict=False) == []


def test_validate_reports_problems(tmp_path):
    path = tmp_path / "bad.ipybundle"
    meta = make_meta(format="nope", format_version=0, redactions=["s3cret"], event_count=5)
    events = [
        make_event(1, execute_result={"text/html": "<b>1</b>"}),
        make_event(3, stdout="leaked s3cret"),
        make_event(4, success=False),
        make_event(5, success=False, error={"ename": "E", "evalue": "", "traceback": []}),
        make_event(6, recorded_at="yesterday", code=None),
    ]
    save_session_bundle(path, meta, events)

    errors = validate_session_bundle(path, strict=False)
    assert all(isinstance(error, str) for error in errors)
    expected_fragments = [
        "'format'",
        "'format_version'",
        "'event_count'",
        "redacted pattern #1",
        "line 1: a non-empty 'execute_result' must have a 'text/plain' string",
        "line 2: 'seq' must be 2",
        "line 3: failed cells must have an 'error' field",
        "line 4: error: 'traceback' must be a non-empty list of strings",
        "line 5: 'recorded_at' must be an ISO-8601 timestamp",
        "line 5: 'code' must be a string",
    ]
    for fragment in expected_fragments:
        assert any(fragment in error for error in errors), fragment
    assert not any("s3cret" in error for error in errors)

    with pytest.raises(SessionBundleValidationError) as excinfo:
        validate_session_bundle(path)
    assert excinfo.value.bundle_path == path
    assert isinstance(excinfo.value.bundle_path, Path)
    assert excinfo.value.errors == errors


def test_validate_missing_fields(tmp_path):
    path = tmp_path / "bad.ipybundle"
    save_session_bundle(path, {"format": "ipython-session-bundle"}, [{"type": "cell"}])
    errors = validate_session_bundle(path, strict=False)
    for field in ["format_version", "created_at", "redactions", "platform"]:
        assert f"metadata.json: missing required field {field!r}" in errors
    for field in ["seq", "recorded_at", "execution_count", "code", "success"]:
        assert f"events.jsonl line 1: missing required field {field!r}" in errors


def test_validate_structural_problems(tmp_path):
    not_zip = tmp_path / "not-a-zip.ipybundle"
    not_zip.write_text("hello")
    errors = validate_session_bundle(not_zip, strict=False)
    assert len(errors) == 1 and "ZIP" in errors[0]
    with pytest.raises(SessionBundleValidationError) as excinfo:
        validate_session_bundle(not_zip)
    assert excinfo.value.errors == errors
    with pytest.raises(SessionBundleValidationError):
        load_session_bundle(not_zip)

    partial_bundle = tmp_path / "partial.ipybundle"
    with zipfile.ZipFile(partial_bundle, "w") as zf:
        zf.writestr("metadata.json", json.dumps(make_meta()))
        zf.writestr("events.jsonl", "{not json}\n[1]\n")
    errors = validate_session_bundle(partial_bundle, strict=False)
    assert any("line 1: invalid JSON" in error for error in errors)
    assert any("line 2: event must be a JSON object" in error for error in errors)

    only_metadata = tmp_path / "only-metadata.ipybundle"
    with zipfile.ZipFile(only_metadata, "w") as zf:
        zf.writestr("metadata.json", json.dumps(make_meta()))
    assert validate_session_bundle(only_metadata, strict=False) == ["missing events.jsonl"]

    missing = tmp_path / "missing.ipybundle"
    assert len(validate_session_bundle(missing, strict=False)) == 1
    with pytest.raises(SessionBundleValidationError):
        validate_session_bundle(missing)
    with pytest.raises(FileNotFoundError):
        load_session_bundle(missing)
