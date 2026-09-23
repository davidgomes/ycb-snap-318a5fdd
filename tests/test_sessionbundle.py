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
from IPython.terminal.interactiveshell import TerminalInteractiveShell


@pytest.fixture
def ip():
    shell = TerminalInteractiveShell.instance()
    if getattr(shell, "_session_bundle_recorder", None) is not None:
        shell.stop_session_bundle()
    yield shell
    if getattr(shell, "_session_bundle_recorder", None) is not None:
        shell.stop_session_bundle()


def test_record_replay_and_redact(ip, tmp_path: Path):
    bundle = tmp_path / "demo.ipybundle"
    ip.start_session_bundle(bundle, redact=["sekret"])
    assert ip.session_bundle_status() == {"recording": True, "path": str(bundle)}
    with pytest.raises(RuntimeError):
        ip.start_session_bundle(bundle, overwrite=True)

    ip.run_cell("print('hello')", store_history=True)
    ip.run_cell("1 + 1", store_history=True)
    ip.run_cell("print('sekret')", store_history=True)
    ip.run_cell("raise ValueError('sekret boom')", store_history=True)
    saved = ip.stop_session_bundle()
    assert saved == str(bundle)
    assert ip.session_bundle_status() == {"recording": False, "path": None}

    with pytest.raises(FileExistsError):
        ip.start_session_bundle(bundle)

    meta, events = load_session_bundle(bundle)
    assert meta["format"] == "ipython-session-bundle"
    assert meta["format_version"] >= 1
    assert meta["redactions"] == ["sekret"]
    assert meta["event_count"] == 4
    assert [e["seq"] for e in events] == [1, 2, 3, 4]
    assert events[0]["stdout"] == "hello\n"
    assert events[0]["execute_result"] == {}
    assert events[1]["stdout"] == ""
    assert events[1]["execute_result"]["text/plain"] == "2"
    raw = zipfile.ZipFile(bundle).read("events.jsonl").decode()
    assert "sekret" not in raw
    assert "<redacted>" in raw
    assert events[3]["success"] is False
    assert events[3]["error"]["ename"] == "ValueError"
    assert events[3]["error"]["traceback"]

    before = ip.execution_count
    replay_session_bundle(ip, bundle, stop_on_error=True, store_history=True)
    # three successes then the failing cell, each advances the counter
    assert ip.execution_count == before + 4

    before = ip.execution_count
    replay_session_bundle(ip, bundle, stop_on_error=False, store_history=False)
    assert ip.execution_count == before


def test_overwrite_and_context_and_validate(ip, tmp_path: Path):
    bundle = tmp_path / "again.ipybundle"
    bundle.write_text("not a bundle")
    with pytest.raises(FileExistsError):
        ip.start_session_bundle(bundle)
    ip.start_session_bundle(bundle, overwrite=True)
    ip.run_cell("x = 7", store_history=True)
    ip.stop_session_bundle()
    meta, events = load_session_bundle(bundle)
    assert len(events) == 1
    assert events[0]["code"] == "x = 7"

    other = tmp_path / "ctx.ipybundle"
    with session_bundle_recorder(ip, other, redact=["pw"]):
        ip.run_cell("print('pw')", store_history=False)
    meta, events = load_session_bundle(other)
    assert events[0]["execution_count"] is None
    assert "pw" not in zipfile.ZipFile(other).read("events.jsonl").decode()
    assert validate_session_bundle(other) == []

    bad_meta = dict(meta)
    bad_meta["event_count"] = 99
    broken = tmp_path / "broken.ipybundle"
    save_session_bundle(broken, bad_meta, events)
    errors = validate_session_bundle(broken, strict=False)
    assert errors
    with pytest.raises(SessionBundleValidationError) as excinfo:
        validate_session_bundle(broken, strict=True)
    assert excinfo.value.bundle_path == broken
    assert excinfo.value.errors == errors


def test_magic_status(ip, tmp_path: Path):
    bundle = tmp_path / "magic.ipybundle"
    assert ip.run_line_magic("session_bundle", "status") == {
        "recording": False,
        "path": None,
    }
    ip.run_line_magic("session_bundle", f"start {bundle} --redact alpha --redact beta")
    status = ip.run_line_magic("session_bundle", "status")
    assert status["recording"] is True
    ip.run_cell("print('alpha beta')", store_history=True)
    ip.run_line_magic("session_bundle", "stop")
    _meta, events = load_session_bundle(bundle)
    raw = zipfile.ZipFile(bundle).read("events.jsonl").decode()
    assert "alpha" not in raw and "beta" not in raw
    assert json.loads(raw)["stdout"] == "<redacted> <redacted>\n"
    assert events[0]["success"] is True
