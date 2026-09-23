"""Tests for IPython.core.sessionbundle and the %session_bundle magic."""

import json
import zipfile
from datetime import datetime

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
def ip():
    shell = get_ipython()
    yield shell
    if shell.session_bundle_status()["recording"]:
        shell.stop_session_bundle()


@pytest.fixture
def bundle(tmp_path):
    return tmp_path / "session.ipybundle"


def _events_text(path):
    with zipfile.ZipFile(path) as zf:
        return zf.read("events.jsonl").decode("utf-8")


def _record(ip, path, cells, **kwargs):
    ip.start_session_bundle(path, **kwargs)
    for code in cells:
        ip.run_cell(code, store_history=True)
    ip.stop_session_bundle()
    return load_session_bundle(path)


def test_magic_start_status_stop(ip, bundle):
    assert ip.run_line_magic("session_bundle", "status") == {
        "recording": False,
        "path": None,
    }
    ip.run_line_magic("session_bundle", f"start {bundle}")
    assert ip.run_line_magic("session_bundle", "status") == {
        "recording": True,
        "path": str(bundle),
    }
    ip.run_cell("a = 1", store_history=True)
    assert ip.run_line_magic("session_bundle", "stop") == str(bundle)
    assert ip.session_bundle_status() == {"recording": False, "path": None}

    _, events = load_session_bundle(bundle)
    assert [e["code"] for e in events] == ["a = 1"]
    assert validate_session_bundle(bundle) == []


def test_recording_through_cells_skips_start_and_stop(ip, bundle):
    ip.run_cell(f"%session_bundle start {bundle}", store_history=True)
    ip.run_cell("b = 2", store_history=True)
    ip.run_cell("%session_bundle stop", store_history=True)
    _, events = load_session_bundle(bundle)
    assert [e["code"] for e in events] == ["b = 2"]


def test_start_while_recording_raises(ip, bundle, tmp_path):
    ip.start_session_bundle(bundle)
    with pytest.raises(RuntimeError):
        ip.start_session_bundle(tmp_path / "other.ipybundle")
    with pytest.raises(RuntimeError):
        ip.run_line_magic("session_bundle", f"start {tmp_path / 'other2.ipybundle'}")
    assert ip.session_bundle_status()["path"] == str(bundle)


def test_stop_without_recording_raises(ip):
    with pytest.raises(RuntimeError):
        ip.stop_session_bundle()


def test_existing_path_requires_overwrite(ip, bundle):
    _record(ip, bundle, ["c = 3"])
    with pytest.raises(FileExistsError):
        ip.start_session_bundle(bundle)
    with pytest.raises(FileExistsError):
        ip.run_line_magic("session_bundle", f"start {bundle}")
    assert not ip.session_bundle_status()["recording"]

    ip.run_line_magic("session_bundle", f"start {bundle} --overwrite")
    ip.run_cell("d = 4", store_history=True)
    ip.stop_session_bundle()
    _, events = load_session_bundle(bundle)
    assert [e["code"] for e in events] == ["d = 4"]


def test_metadata(ip, bundle):
    meta, _ = _record(ip, bundle, ["1"], redact=["zz-secret", "aa-secret"])
    assert meta["format"] == "ipython-session-bundle"
    assert meta["format_version"] >= 1
    datetime.fromisoformat(meta["created_at"])
    for key in ("ipython_version", "python_version", "platform"):
        assert isinstance(meta[key], str)
    assert meta["redactions"] == ["zz-secret", "aa-secret"]
    assert meta["event_count"] == 1


def test_event_contents(ip, bundle):
    count = ip.execution_count
    _, events = _record(
        ip,
        bundle,
        [
            "print('hello')",
            "import sys; _ = sys.stderr.write('oops\\n')",
            "40 + 2",
            "40 + 2;",
            "1/0",
            "def broken(:\n    pass",
        ],
    )
    assert [e["seq"] for e in events] == [1, 2, 3, 4, 5, 6]
    assert [e["execution_count"] for e in events] == list(range(count, count + 6))
    for e in events:
        assert e["type"] == "cell"
        datetime.fromisoformat(e["recorded_at"])

    printed, stderr, expr, quiet, zerodiv, syntax = events
    assert printed["stdout"] == "hello\n"
    assert printed["execute_result"] == {}
    assert stderr["stderr"] == "oops\n"
    assert stderr["stdout"] == ""
    assert expr["stdout"] == ""
    assert expr["execute_result"]["text/plain"] == "42"
    assert quiet["execute_result"] == {}

    for failed, ename in ((zerodiv, "ZeroDivisionError"), (syntax, "SyntaxError")):
        assert failed["success"] is False
        assert failed["error"]["ename"] == ename
        assert failed["error"]["traceback"]
        assert all(isinstance(line, str) for line in failed["error"]["traceback"])
        assert failed["stdout"] == ""
    assert zerodiv["error"]["evalue"] == "division by zero"
    assert all(e["success"] for e in events[:4])
    assert "error" not in printed
    assert validate_session_bundle(bundle) == []


def test_redaction(ip, bundle):
    ip.run_line_magic(
        "session_bundle", f"start {bundle} --redact hunter2 --redact 'top secret'"
    )
    ip.run_cell("pw = 'hunter2'", store_history=True)
    ip.run_cell("print(pw, 'top secret')", store_history=True)
    ip.run_cell("pw", store_history=True)
    ip.run_cell("raise ValueError(pw)", store_history=True)
    ip.stop_session_bundle()

    text = _events_text(bundle)
    assert "hunter2" not in text
    assert "top secret" not in text
    meta, events = load_session_bundle(bundle)
    assert meta["redactions"] == ["hunter2", "top secret"]
    assert events[0]["code"] == "pw = '<redacted>'"
    assert events[1]["stdout"] == "<redacted> <redacted>\n"
    assert events[2]["execute_result"]["text/plain"] == "'<redacted>'"
    assert events[3]["error"]["evalue"] == "<redacted>"
    assert validate_session_bundle(bundle) == []


def test_replay(ip, bundle):
    _record(ip, bundle, ["replay_x = 10", "replay_y = replay_x * 2"])
    ip.user_ns.pop("replay_x")
    ip.user_ns.pop("replay_y")

    count = ip.execution_count
    results = replay_session_bundle(ip, bundle)
    assert len(results) == 2
    assert ip.user_ns["replay_y"] == 20
    assert ip.execution_count == count + 2

    count = ip.execution_count
    replay_session_bundle(ip, bundle, store_history=False)
    assert ip.execution_count == count


def test_replay_stop_on_error(ip, bundle):
    _record(ip, bundle, ["replay_n = 0", "1/0", "replay_n = 1"])
    results = replay_session_bundle(ip, bundle)
    assert len(results) == 2
    assert ip.user_ns["replay_n"] == 0
    results = replay_session_bundle(ip, bundle, stop_on_error=False)
    assert len(results) == 3
    assert ip.user_ns["replay_n"] == 1


def test_load_does_not_execute(ip, bundle):
    _record(ip, bundle, ["load_marker = 1"])
    ip.user_ns.pop("load_marker")
    load_session_bundle(bundle)
    assert "load_marker" not in ip.user_ns


def test_context_manager(ip, bundle):
    with session_bundle_recorder(ip, bundle, redact=["xyzzy"]) as path:
        assert ip.session_bundle_status() == {"recording": True, "path": path}
        ip.run_cell("cm = 'xyzzy'", store_history=True)
    assert not ip.session_bundle_status()["recording"]
    assert "xyzzy" not in _events_text(bundle)

    with pytest.raises(FileExistsError), session_bundle_recorder(ip, bundle):
        pass
    with session_bundle_recorder(ip, bundle, overwrite=True):
        ip.run_cell("cm = 2", store_history=True)
    _, events = load_session_bundle(bundle)
    assert [e["code"] for e in events] == ["cm = 2"]


def _valid_event(seq=1, **overrides):
    event = {
        "type": "cell",
        "seq": seq,
        "recorded_at": "2024-01-01T00:00:00+00:00",
        "execution_count": seq,
        "code": "x = 1",
        "success": True,
        "stdout": "",
        "stderr": "",
        "execute_result": {},
    }
    event.update(overrides)
    return event


def _valid_meta(**overrides):
    meta = {
        "format": "ipython-session-bundle",
        "format_version": 1,
        "created_at": "2024-01-01T00:00:00+00:00",
        "ipython_version": "9.0",
        "python_version": "3.12.0",
        "platform": "linux",
        "redactions": [],
    }
    meta.update(overrides)
    return meta


def test_save_session_bundle(bundle):
    path = save_session_bundle(bundle, _valid_meta(), [_valid_event()])
    assert path == bundle
    with zipfile.ZipFile(bundle) as zf:
        assert set(zf.namelist()) == {"metadata.json", "events.jsonl"}
        assert json.loads(zf.read("metadata.json"))["format_version"] == 1
    assert validate_session_bundle(bundle) == []

    with pytest.raises(FileExistsError):
        save_session_bundle(bundle, _valid_meta(), [])
    save_session_bundle(bundle, _valid_meta(), [], overwrite=True)
    assert load_session_bundle(bundle)[1] == []


@pytest.mark.parametrize(
    "meta, events",
    [
        (_valid_meta(format="nope"), [_valid_event()]),
        (_valid_meta(format_version=0), [_valid_event()]),
        (_valid_meta(created_at="yesterday"), [_valid_event()]),
        (_valid_meta(redactions="secret"), [_valid_event()]),
        (_valid_meta(event_count=2), [_valid_event()]),
        (_valid_meta(), [_valid_event(seq=2)]),
        (_valid_meta(), [_valid_event(), _valid_event(seq=3)]),
        (_valid_meta(), [_valid_event(type="display")]),
        (_valid_meta(), [_valid_event(execution_count="1")]),
        (_valid_meta(), [_valid_event(execute_result={"text/html": "<b>"})]),
        (_valid_meta(), [_valid_event(success=False)]),
        (
            _valid_meta(),
            [
                _valid_event(
                    success=False,
                    error={"ename": "E", "evalue": "", "traceback": []},
                )
            ],
        ),
        (_valid_meta(redactions=["x = 1"]), [_valid_event()]),
    ],
)
def test_validate_detects_errors(bundle, meta, events):
    save_session_bundle(bundle, meta, events)
    errors = validate_session_bundle(bundle, strict=False)
    assert errors and all(isinstance(e, str) for e in errors)
    with pytest.raises(SessionBundleValidationError) as excinfo:
        validate_session_bundle(bundle)
    assert excinfo.value.errors == errors
    assert excinfo.value.bundle_path == bundle


def test_validate_not_a_zip(bundle):
    bundle.write_text("not a zip")
    errors = validate_session_bundle(bundle, strict=False)
    assert errors
    with pytest.raises(SessionBundleValidationError):
        load_session_bundle(bundle)
