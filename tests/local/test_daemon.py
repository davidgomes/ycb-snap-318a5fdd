import json
import os
import subprocess
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest

from mnamer.frontends import Cli
from mnamer.setting_store import SettingStore

pytestmark = pytest.mark.local

REPO = Path(__file__).resolve().parents[2]


def _run(
    *args: str, cwd: Path, timeout: float = 30
) -> subprocess.CompletedProcess[str]:
    env = os.environ.copy()
    env.pop("MNAMER_DAEMON_WORKER", None)
    prior = env.get("PYTHONPATH", "")
    env["PYTHONPATH"] = str(REPO) if not prior else str(REPO) + os.pathsep + prior
    return subprocess.run(
        [sys.executable, "-m", "mnamer", *args],
        cwd=cwd,
        env=env,
        capture_output=True,
        text=True,
        timeout=timeout,
        check=False,
    )


def _stop(state: Path, cwd: Path) -> None:
    _run("--daemon", "stop", "--daemon-state", str(state), cwd=cwd, timeout=15)
    if not state.is_file():
        return
    try:
        pid = json.loads(state.read_text(encoding="utf-8")).get("pid")
    except (OSError, json.JSONDecodeError):
        return
    if isinstance(pid, int) and pid > 0:
        try:
            os.kill(pid, 15)
        except OSError:
            pass


def test_settings_parse_daemon_flags_with_batch(monkeypatch: pytest.MonkeyPatch):
    monkeypatch.setattr(
        sys,
        "argv",
        [
            "mnamer",
            "--batch",
            "--daemon",
            "stats",
            "--daemon-run-once",
            "--dry-run",
            "--validate-daemon-config",
            "--daemon-state",
            "custom-state.json",
            "--daemon-config",
            "daemon.json",
            "--watch",
            "alpha",
            "beta",
            "gamma",
            "--movie-directory",
            "movies",
            "--stability-interval-ms",
            "25",
            "--stability-checks",
            "3",
            "--batch-size",
            "0",
            "--lines",
            "4",
            "--notify-webhook",
            "http://example.test/hook",
            "positional-one",
            "positional-two",
        ],
    )
    settings = SettingStore()
    settings.load()
    assert settings.batch is True
    assert settings.daemon == "stats"
    assert settings.daemon_run_once is True
    assert settings.dry_run is True
    assert settings.validate_daemon_config is True
    assert settings.daemon_state == "custom-state.json"
    assert settings.daemon_config == "daemon.json"
    assert settings.watch == ["alpha", "beta", "gamma"]
    assert [path.name for path in settings.targets] == [
        "positional-one",
        "positional-two",
    ]
    assert settings.movie_directory is not None
    assert settings.stability_interval_ms == 25
    assert settings.stability_checks == 3
    assert settings.batch_size == 0
    assert settings.lines == 4
    assert settings.notify_webhook == "http://example.test/hook"


def test_run_once_moves_top_level_files_and_keeps_names(tmp_path: Path):
    watch = tmp_path / "watch"
    movies = tmp_path / "movies"
    nested = watch / "nested"
    watch.mkdir()
    nested.mkdir()
    state = tmp_path / "daemon-state.json"
    (watch / "Keep Name.mkv").write_text("video", encoding="utf-8")
    (watch / "file.part").write_text("partial", encoding="utf-8")
    (watch / "clip.mkv.part").write_text("partial2", encoding="utf-8")
    (watch / "partition.mkv").write_text("part-word", encoding="utf-8")
    (watch / "my.part.mkv").write_text("embedded", encoding="utf-8")
    (watch / "part.mkv").write_text("prefix", encoding="utf-8")
    (nested / "hidden.mkv").write_text("nested", encoding="utf-8")

    result = _run(
        "--batch",
        "--daemon-run-once",
        "--watch",
        str(watch),
        "--movie-directory",
        str(movies),
        "--daemon-state",
        str(state),
        cwd=tmp_path,
    )
    assert result.returncode == 0, result.stderr + result.stdout
    assert (movies / "Keep Name.mkv").read_text(encoding="utf-8") == "video"
    assert (movies / "partition.mkv").read_text(encoding="utf-8") == "part-word"
    assert (movies / "my.part.mkv").read_text(encoding="utf-8") == "embedded"
    assert (movies / "part.mkv").read_text(encoding="utf-8") == "prefix"
    assert (watch / "file.part").is_file()
    assert (watch / "clip.mkv.part").is_file()
    assert (nested / "hidden.mkv").is_file()
    assert not (watch / "Keep Name.mkv").exists()

    payload = json.loads(state.read_text(encoding="utf-8"))
    assert payload["processed"]
    assert isinstance(payload["updated_epoch"], int)
    assert payload["updated_epoch"] > 0
    log_text = Path(str(state) + ".log").read_text(encoding="utf-8")
    assert len(log_text.splitlines()) == 1

    logs = _run("--daemon", "logs", "--daemon-state", str(state), cwd=tmp_path)
    assert logs.returncode == 0
    assert logs.stdout.strip() == log_text.strip()
    assert "no logs available" not in logs.stdout

    stats = _run("--daemon", "stats", "--daemon-state", str(state), cwd=tmp_path)
    assert stats.returncode == 0
    assert stats.stdout.strip() == f"processed=4, last_epoch={payload['updated_epoch']}"


def test_run_once_without_files_updates_state_and_log(tmp_path: Path):
    watch = tmp_path / "watch"
    movies = tmp_path / "movies"
    watch.mkdir()
    state = tmp_path / "daemon-state.json"
    first = _run(
        "--daemon-run-once",
        "--watch",
        str(watch),
        "--movie-directory",
        str(movies),
        "--daemon-state",
        str(state),
        cwd=tmp_path,
    )
    assert first.returncode == 0, first.stdout
    before = state.read_text(encoding="utf-8")
    second = _run(
        "--daemon-run-once",
        "--watch",
        str(watch),
        "--movie-directory",
        str(movies),
        "--daemon-state",
        str(state),
        cwd=tmp_path,
    )
    assert second.returncode == 0, second.stdout
    after = state.read_text(encoding="utf-8")
    assert before != after
    assert json.loads(after)["processed"] == []
    lines = Path(str(state) + ".log").read_text(encoding="utf-8").splitlines()
    assert len(lines) == 2
    tailed = _run(
        "--daemon",
        "logs",
        "--lines",
        "1",
        "--daemon-state",
        str(state),
        cwd=tmp_path,
    )
    assert tailed.stdout.strip() == lines[-1]
    all_logs = _run("--daemon", "logs", "--daemon-state", str(state), cwd=tmp_path)
    assert all_logs.stdout.strip().splitlines() == lines


def test_logs_when_missing_empty_or_state_is_directory(tmp_path: Path):
    missing = tmp_path / "missing.json"
    result = _run("--daemon", "logs", "--daemon-state", str(missing), cwd=tmp_path)
    assert result.returncode == 0
    assert result.stdout.strip() == "no logs available"

    empty_state = tmp_path / "empty-state.json"
    empty_state.write_text("{}\n", encoding="utf-8")
    empty_log = Path(str(empty_state) + ".log")
    empty_log.write_text("", encoding="utf-8")
    empty = _run("--daemon", "logs", "--daemon-state", str(empty_state), cwd=tmp_path)
    assert empty.stdout.strip() == "no logs available"

    directory = tmp_path / "state-dir"
    directory.mkdir()
    Path(str(directory) + ".log").write_text("should be ignored\n", encoding="utf-8")
    logs = _run("--daemon", "logs", "--daemon-state", str(directory), cwd=tmp_path)
    status = _run("--daemon", "status", "--daemon-state", str(directory), cwd=tmp_path)
    stop = _run("--daemon", "stop", "--daemon-state", str(directory), cwd=tmp_path)
    stats = _run("--daemon", "stats", "--daemon-state", str(directory), cwd=tmp_path)
    assert logs.stdout.strip() == "no logs available"
    assert status.stdout.strip() == "not running"
    assert stop.returncode == 0
    assert stats.returncode == 0
    assert stats.stdout.strip() == "processed=0, last_epoch=0"


def test_batch_size_is_global_and_zero_moves_nothing(tmp_path: Path):
    first = tmp_path / "one"
    second = tmp_path / "two"
    movies = tmp_path / "movies"
    first.mkdir()
    second.mkdir()
    (first / "a.mkv").write_text("a", encoding="utf-8")
    (first / "b.mkv").write_text("b", encoding="utf-8")
    (second / "c.mkv").write_text("c", encoding="utf-8")
    state = tmp_path / "state.json"

    capped = _run(
        "--daemon-run-once",
        "--watch",
        str(first),
        str(second),
        "--movie-directory",
        str(movies),
        "--daemon-state",
        str(state),
        "--batch-size",
        "2",
        cwd=tmp_path,
    )
    assert capped.returncode == 0, capped.stdout
    assert len(list(movies.iterdir())) == 2
    assert (
        sum(
            path.exists()
            for path in (first / "a.mkv", first / "b.mkv", second / "c.mkv")
        )
        == 1
    )

    blocked = _run(
        "--daemon-run-once",
        "--watch",
        str(first),
        str(second),
        "--movie-directory",
        str(movies),
        "--daemon-state",
        str(state),
        "--batch-size",
        "0",
        cwd=tmp_path,
    )
    assert blocked.returncode == 0, blocked.stdout
    assert (
        sum(
            path.exists()
            for path in (first / "a.mkv", first / "b.mkv", second / "c.mkv")
        )
        == 1
    )
    first_body = state.read_text(encoding="utf-8")
    again = _run(
        "--daemon-run-once",
        "--watch",
        str(first),
        str(second),
        "--movie-directory",
        str(movies),
        "--daemon-state",
        str(state),
        "--batch-size",
        "0",
        cwd=tmp_path,
    )
    assert again.returncode == 0
    assert state.read_text(encoding="utf-8") != first_body


def test_exclude_patterns_and_combined_watch_sources(tmp_path: Path):
    from_config = tmp_path / "from-config"
    from_cli = tmp_path / "from-cli"
    from_positional = tmp_path / "from-positional"
    movies_config = tmp_path / "movies-config"
    movies_cli = tmp_path / "movies-cli"
    for path in (from_config, from_cli, from_positional):
        path.mkdir()
    (from_config / "keep.mkv").write_text("keep", encoding="utf-8")
    (from_config / "skip.tmp").write_text("tmp", encoding="utf-8")
    (from_config / "skip.partial").write_text("partial", encoding="utf-8")
    (from_cli / "cli.mkv").write_text("cli", encoding="utf-8")
    (from_positional / "positional.mkv").write_text("positional", encoding="utf-8")
    (tmp_path / "missing-watch").mkdir()
    missing = tmp_path / "does-not-exist"
    config = tmp_path / "daemon.json"
    config.write_text(
        json.dumps(
            {
                "watch": [
                    {
                        "exclude": ["*.tmp", "*.partial"],
                        "movie_directory": str(movies_config),
                        "path": str(from_config),
                    }
                ]
            }
        ),
        encoding="utf-8",
    )
    state = tmp_path / "state.json"
    result = _run(
        "--daemon-run-once",
        "--daemon-config",
        str(config),
        "--watch",
        str(from_cli),
        str(missing),
        "--movie-directory",
        str(movies_cli),
        str(from_positional),
        "--daemon-state",
        str(state),
        cwd=tmp_path,
    )
    assert result.returncode == 0, result.stderr + result.stdout
    assert (movies_config / "keep.mkv").read_text(encoding="utf-8") == "keep"
    assert (from_config / "skip.tmp").is_file()
    assert (from_config / "skip.partial").is_file()
    assert (movies_cli / "cli.mkv").is_file()
    assert (movies_cli / "positional.mkv").is_file()


def test_destination_collision_does_not_overwrite(tmp_path: Path):
    watch = tmp_path / "watch"
    movies = tmp_path / "movies"
    watch.mkdir()
    movies.mkdir()
    (movies / "a.mkv").write_bytes(b"original")
    (watch / "a.mkv").write_bytes(b"incoming")
    result = _run(
        "--daemon-run-once",
        "--watch",
        str(watch),
        "--movie-directory",
        str(movies),
        "--daemon-state",
        str(tmp_path / "state.json"),
        cwd=tmp_path,
    )
    assert result.returncode == 0, result.stdout
    assert (movies / "a.mkv").read_bytes() == b"original"
    assert (movies / "a (1).mkv").read_bytes() == b"incoming"
    assert not (watch / "a.mkv").exists()


def test_dry_run_reports_moves_without_side_effects(tmp_path: Path):
    watch = tmp_path / "watch"
    movies = tmp_path / "movies"
    watch.mkdir()
    src = watch / "movie.mkv"
    src.write_text("data", encoding="utf-8")
    state = tmp_path / "state.json"
    result = _run(
        "--daemon-run-once",
        "--dry-run",
        "--watch",
        str(watch),
        "--movie-directory",
        str(movies),
        "--daemon-state",
        str(state),
        cwd=tmp_path,
    )
    assert result.returncode == 0, result.stdout
    expected = f"{src.resolve()} -> {(movies / src.name).resolve()}"
    assert result.stdout.strip() == expected
    assert src.is_file()
    assert not movies.exists()
    assert not state.exists()
    assert not Path(str(state) + ".log").exists()


def test_stability_skips_files_that_change_size(tmp_path: Path):
    watch = tmp_path / "watch"
    movies = tmp_path / "movies"
    watch.mkdir()
    changing = watch / "changing.mkv"
    stable = watch / "stable.mkv"
    changing.write_bytes(b"x")
    stable.write_bytes(b"stable")
    stop = threading.Event()

    def churn() -> None:
        while not stop.is_set():
            with changing.open("ab") as handle:
                handle.write(b"y")
            time.sleep(0.02)

    worker = threading.Thread(target=churn)
    worker.start()
    try:
        result = _run(
            "--daemon-run-once",
            "--watch",
            str(watch),
            "--movie-directory",
            str(movies),
            "--daemon-state",
            str(tmp_path / "state.json"),
            "--stability-checks",
            "4",
            "--stability-interval-ms",
            "40",
            cwd=tmp_path,
            timeout=15,
        )
    finally:
        stop.set()
        worker.join(timeout=2)
    assert result.returncode == 0, result.stdout
    assert changing.exists()
    assert not (movies / "changing.mkv").exists()
    assert (movies / "stable.mkv").read_bytes() == b"stable"


def test_webhook_failure_is_non_fatal_and_success_is_notified(tmp_path: Path):
    watch = tmp_path / "watch"
    movies = tmp_path / "movies"
    watch.mkdir()
    (watch / "one.mkv").write_text("one", encoding="utf-8")
    failed = _run(
        "--daemon-run-once",
        "--watch",
        str(watch),
        "--movie-directory",
        str(movies),
        "--daemon-state",
        str(tmp_path / "state-a.json"),
        "--notify-webhook",
        "http://127.0.0.1:1/hook",
        cwd=tmp_path,
    )
    assert failed.returncode == 0, failed.stdout
    assert (movies / "one.mkv").is_file()

    seen: list[dict[str, str]] = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self) -> None:  # noqa: N802
            length = int(self.headers.get("Content-Length", "0"))
            seen.append(json.loads(self.rfile.read(length).decode("utf-8")))
            self.send_response(204)
            self.end_headers()

        def log_message(self, fmt: str, *args: object) -> None:
            return

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever)
    thread.start()
    try:
        (watch / "two.mkv").write_text("two", encoding="utf-8")
        port = server.server_address[1]
        notified = _run(
            "--daemon-run-once",
            "--watch",
            str(watch),
            "--movie-directory",
            str(movies),
            "--daemon-state",
            str(tmp_path / "state-b.json"),
            "--notify-webhook",
            f"http://127.0.0.1:{port}/hook",
            cwd=tmp_path,
        )
    finally:
        server.shutdown()
        thread.join(timeout=5)
        server.server_close()
    assert notified.returncode == 0, notified.stdout
    assert (movies / "two.mkv").is_file()
    assert seen and seen[0]["event"] == "moved"
    assert seen[0]["destination"].endswith("two.mkv")


def test_validate_daemon_config_exit_codes(tmp_path: Path):
    missing_flag = _run("--validate-daemon-config", cwd=tmp_path)
    assert missing_flag.returncode == 2
    assert "config" in missing_flag.stdout

    absent = _run(
        "--validate-daemon-config",
        "--daemon-config",
        str(tmp_path / "nope.json"),
        cwd=tmp_path,
    )
    assert absent.returncode == 2
    assert "config" in absent.stdout

    invalid = tmp_path / "invalid.json"
    invalid.write_text("{", encoding="utf-8")
    bad_json = _run(
        "--validate-daemon-config",
        "--daemon-config",
        str(invalid),
        cwd=tmp_path,
    )
    assert bad_json.returncode == 2
    assert "structure" in bad_json.stdout
    assert "config" in bad_json.stdout

    cases = [
        {"watch": {}},
        {"watch": [{"movie_directory": "/movies"}]},
        {"watch": [{"path": 1, "movie_directory": "/movies"}]},
        {"watch": [{"path": "/watch", "movie_directory": None}]},
        {
            "watch": [
                {"path": "/watch", "movie_directory": "/movies", "exclude": "*.tmp"}
            ]
        },
        {"watch": [{"path": "/watch", "movie_directory": "/movies", "exclude": [1]}]},
        ["watch"],
    ]
    for index, payload in enumerate(cases):
        path = tmp_path / f"bad-{index}.json"
        path.write_text(json.dumps(payload), encoding="utf-8")
        result = _run(
            "--validate-daemon-config",
            "--daemon-config",
            str(path),
            cwd=tmp_path,
        )
        assert result.returncode == 2, payload
        assert "structure" in result.stdout
        assert "config" in result.stdout

    valid = tmp_path / "valid.json"
    valid.write_text(json.dumps({"watch": []}), encoding="utf-8")
    ok = _run(
        "--validate-daemon-config",
        "--daemon-config",
        str(valid),
        cwd=tmp_path,
    )
    assert ok.returncode == 0, ok.stdout
    assert ok.stdout.strip() == ""

    populated = tmp_path / "populated.json"
    populated.write_text(
        json.dumps(
            {
                "watch": [
                    {
                        "exclude": ["*.tmp", "*.partial"],
                        "movie_directory": str(tmp_path / "movies"),
                        "path": str(tmp_path / "watch"),
                    }
                ]
            }
        ),
        encoding="utf-8",
    )
    populated_result = _run(
        "--validate-daemon-config",
        "--daemon-config",
        str(populated),
        cwd=tmp_path,
    )
    assert populated_result.returncode == 0


def test_start_requires_watch_and_runs_in_background(tmp_path: Path):
    state = tmp_path / "daemon-state.json"
    missing = _run("--daemon", "start", "--daemon-state", str(state), cwd=tmp_path)
    assert missing.returncode == 2
    assert missing.returncode != 1
    assert "watch" in missing.stdout

    watch = tmp_path / "watch"
    movies = tmp_path / "movies"
    watch.mkdir()
    src = watch / "later.mkv"
    src.write_text("later", encoding="utf-8")
    started = time.monotonic()
    try:
        started_proc = _run(
            "--daemon",
            "start",
            "--watch",
            str(watch),
            "--movie-directory",
            str(movies),
            "--daemon-state",
            str(state),
            "--stability-checks",
            "4",
            "--stability-interval-ms",
            "1500",
            cwd=tmp_path,
            timeout=10,
        )
        elapsed = time.monotonic() - started
        assert started_proc.returncode == 0, started_proc.stderr + started_proc.stdout
        assert elapsed < 1.5
        payload = json.loads(state.read_text(encoding="utf-8"))
        assert isinstance(payload["processed"], list)
        assert isinstance(payload["updated_epoch"], int)
        status = _run("--daemon", "status", "--daemon-state", str(state), cwd=tmp_path)
        assert status.stdout.strip() == "running"
    finally:
        _stop(state, tmp_path)

    idle = tmp_path / "idle"
    idle.mkdir()
    idle_state = tmp_path / "idle-state.json"
    try:
        restart = _run(
            "--daemon",
            "restart",
            "--watch",
            str(idle),
            "--movie-directory",
            str(movies),
            "--daemon-state",
            str(idle_state),
            cwd=tmp_path,
        )
        assert restart.returncode == 0, restart.stdout
        running = _run(
            "--daemon", "status", "--daemon-state", str(idle_state), cwd=tmp_path
        )
        assert running.stdout.strip() == "running"
        first_pid = json.loads(idle_state.read_text(encoding="utf-8"))["pid"]
        again = _run(
            "--daemon",
            "restart",
            "--watch",
            str(idle),
            "--movie-directory",
            str(movies),
            "--daemon-state",
            str(idle_state),
            cwd=tmp_path,
        )
        assert again.returncode == 0, again.stdout
        second_pid = json.loads(idle_state.read_text(encoding="utf-8"))["pid"]
        assert first_pid != second_pid
        stopped = _run(
            "--daemon", "stop", "--daemon-state", str(idle_state), cwd=tmp_path
        )
        assert stopped.returncode == 0
        quiet = _run(
            "--daemon", "status", "--daemon-state", str(idle_state), cwd=tmp_path
        )
        assert quiet.stdout.strip() == "not running"
        idempotent = _run(
            "--daemon", "stop", "--daemon-state", str(idle_state), cwd=tmp_path
        )
        assert idempotent.returncode == 0
    finally:
        _stop(idle_state, tmp_path)


def test_stats_without_state_file(tmp_path: Path):
    result = _run(
        "--batch",
        "--daemon",
        "stats",
        "--daemon-state",
        str(tmp_path / "absent.json"),
        cwd=tmp_path,
    )
    assert result.returncode == 0
    assert result.stdout.strip() == "processed=0, last_epoch=0"


def test_cli_object_dispatches_daemon_without_targets(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str], tmp_path: Path
):
    state = tmp_path / "daemon-state.json"
    monkeypatch.setattr(
        sys,
        "argv",
        ["mnamer", "--daemon", "status", "--daemon-state", str(state)],
    )
    settings = SettingStore()
    settings.load()
    Cli(settings).launch()
    captured = capsys.readouterr()
    assert captured.out.strip() == "not running"
