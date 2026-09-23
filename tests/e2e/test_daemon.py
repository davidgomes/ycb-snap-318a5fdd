import json
import os
import subprocess
import sys
import time
from collections.abc import Iterator
from pathlib import Path

import pytest

from mnamer import daemon

pytestmark = [
    pytest.mark.e2e,
    # the daemon is intentionally left running detached from the test process
    pytest.mark.filterwarnings("ignore:subprocess .* is still running:ResourceWarning"),
]

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]


@pytest.fixture
def workspace(tmp_path: Path, monkeypatch) -> Iterator[Path]:
    monkeypatch.chdir(tmp_path)
    for directory in ("watch", "other", "movies"):
        (tmp_path / directory).mkdir()
    yield tmp_path
    daemon._stop(tmp_path / "state.json")


@pytest.fixture
def cli(e2e_run, workspace: Path):
    """Runs mnamer with a fresh argv, using the workspace's state file."""

    def fn(*args: str):
        del sys.argv[1:]
        return e2e_run("--daemon-state", str(workspace / "state.json"), *args)

    return fn


def wait_for(predicate, timeout: float = 15.0) -> bool:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return True
        time.sleep(0.1)
    return False


def test_validate__requires_daemon_config(cli):
    result = cli("--validate-daemon-config")
    assert result.code == 2
    assert "--daemon-config" in result.out


def test_validate__config_not_found(cli, workspace: Path):
    result = cli("--validate-daemon-config", "--daemon-config", "missing.json")
    assert result.code == 2
    assert "config not found" in result.out


@pytest.mark.parametrize(
    "payload",
    ({"watch": []}, {"watch": [{"path": "/in", "movie_directory": "/out"}]}),
    ids=("empty", "entry"),
)
def test_validate__valid(cli, workspace: Path, payload):
    (workspace / "config.json").write_text(json.dumps(payload))
    result = cli("--validate-daemon-config", "--daemon-config", "config.json")
    assert result.code == 0
    assert "valid" in result.out


def test_validate__invalid(cli, workspace: Path):
    (workspace / "config.json").write_text(json.dumps({"watch": [{"path": "/in"}]}))
    result = cli("--validate-daemon-config", "--daemon-config", "config.json")
    assert result.code == 2
    assert "config structure" in result.out


def test_start__requires_watch(cli):
    result = cli("--daemon", "start", "--batch", "--movie-directory", "movies")
    assert result.code == 2
    assert "watch" in result.out


def test_start__requires_movie_directory_for_cli_watch(cli):
    result = cli("--daemon", "start", "--watch", "watch")
    assert result.code == 2
    assert "--movie-directory" in result.out


def test_start__invalid_config(cli, workspace: Path):
    (workspace / "config.json").write_text("[]")
    result = cli("--daemon", "start", "--daemon-config", "config.json")
    assert result.code == 2
    assert "config structure" in result.out


def test_lifecycle(cli, workspace: Path):
    (workspace / "watch" / "a.mkv").write_text("a")
    started_at = time.monotonic()
    result = cli(
        "--daemon",
        "start",
        "--batch",
        "--watch",
        "watch",
        "--movie-directory",
        "movies",
        "--stability-checks",
        "0",
    )
    assert time.monotonic() - started_at < 5
    assert result.code == 0
    assert json.loads((workspace / "state.json").read_text())
    assert cli("--daemon", "status").out == "running"

    assert wait_for(lambda: (workspace / "movies" / "a.mkv").exists())
    assert wait_for(lambda: cli("--daemon", "stats").out.startswith("processed=1,"))
    assert "daemon started" in cli("--daemon", "logs").out

    result = cli(
        "--daemon", "restart", "--watch", "watch", "--movie-directory", "movies"
    )
    assert result.code == 0
    assert "daemon stopped" in result.out
    assert cli("--daemon", "status").out == "running"

    result = cli("--daemon", "stop")
    assert result.code == 0
    assert result.out == "daemon stopped"
    assert cli("--daemon", "status").out == "not running"
    result = cli("--daemon", "stop")
    assert result.code == 0
    assert result.out == "daemon not running"


def test_restart__when_not_running_starts(cli):
    result = cli(
        "--daemon", "restart", "--watch", "watch", "--movie-directory", "movies"
    )
    assert result.code == 0
    assert "daemon stopped" not in result.out
    assert cli("--daemon", "status").out == "running"


def test_status__not_running(cli):
    result = cli("--daemon", "status")
    assert result.code == 0
    assert result.out == "not running"


@pytest.mark.usefixtures("workspace")
def test_state_path_is_directory(e2e_run):
    for action, expected in (
        ("status", "not running"),
        ("logs", "no logs available"),
        ("stop", "daemon not running"),
    ):
        del sys.argv[1:]
        result = e2e_run("--daemon", action, "--daemon-state", "watch")
        assert result.code == 0
        assert result.out == expected


def test_logs__none_available(cli):
    result = cli("--daemon", "logs")
    assert result.code == 0
    assert result.out == "no logs available"


def test_logs__after_run_once(cli):
    for _ in range(3):
        cli("--daemon-run-once", "--watch", "watch", "--movie-directory", "movies")
    assert len(cli("--daemon", "logs").out.splitlines()) == 3
    result = cli("--daemon", "logs", "--lines", "2")
    assert result.code == 0
    assert len(result.out.splitlines()) == 2
    assert "run-once" in result.out


def test_stats(cli, workspace: Path):
    assert cli("--daemon", "stats").out == "processed=0, last_epoch=0"
    (workspace / "watch" / "a.mkv").write_text("a")
    cli(
        "--daemon-run-once",
        "--watch",
        "watch",
        "--movie-directory",
        "movies",
        "--stability-checks",
        "0",
    )
    result = cli("--daemon", "stats")
    assert result.code == 0
    epoch = json.loads((workspace / "state.json").read_text())["updated_epoch"]
    assert result.out == f"processed=1, last_epoch={epoch}"


def test_run_once__combines_watch_positional_and_config(cli, workspace: Path):
    (workspace / "third").mkdir()
    for directory in ("watch", "other", "third"):
        (workspace / directory / f"{directory}.mkv").write_text(directory)
    (workspace / "watch" / "skip.tmp").write_text("")
    (workspace / "config.json").write_text(
        json.dumps(
            {
                "watch": [
                    {
                        "path": str(workspace / "watch"),
                        "movie_directory": str(workspace / "movies"),
                        "exclude": ["*.tmp"],
                    }
                ]
            }
        )
    )
    result = cli(
        "--daemon-run-once",
        "--daemon-config",
        "config.json",
        "--movie-directory",
        "movies",
        "--stability-checks",
        "0",
        "--watch",
        "other",
        "third",
    )
    assert result.code == 0
    assert sorted(path.name for path in (workspace / "movies").iterdir()) == [
        "other.mkv",
        "third.mkv",
        "watch.mkv",
    ]
    assert (workspace / "watch" / "skip.tmp").exists()


def test_run_once__dry_run(cli, workspace: Path):
    (workspace / "watch" / "a.mkv").write_text("a")
    (workspace / "watch" / "b.mkv").write_text("b")
    result = cli(
        "--daemon-run-once",
        "--dry-run",
        "--watch",
        "watch",
        "--movie-directory",
        "movies",
    )
    assert result.code == 0
    assert result.out.splitlines() == [
        f"{workspace / 'watch' / name} -> {workspace / 'movies' / name}"
        for name in ("a.mkv", "b.mkv")
    ]
    assert (workspace / "watch" / "a.mkv").exists()
    assert not (workspace / "state.json").exists()
    assert not (workspace / "state.json.log").exists()


def test_run_once__updates_state_without_files(cli, workspace: Path):
    args = ("--daemon-run-once", "--watch", "watch", "--movie-directory", "movies")
    assert cli(*args).code == 0
    first = (workspace / "state.json").read_text()
    assert json.loads(first)
    assert cli(*args).code == 0
    assert (workspace / "state.json").read_text() != first


@pytest.mark.parametrize(
    ("args", "code", "expected"),
    (
        (("--daemon", "start", "--batch"), 2, "watch"),
        (("--validate-daemon-config",), 2, "--daemon-config"),
        (("--daemon", "status", "--batch"), 0, "not running"),
    ),
    ids=("start-without-watch", "validate-without-config", "status"),
)
def test_entrypoint_exit_codes(workspace: Path, args, code, expected):
    environment = {**os.environ, "PYTHONPATH": str(REPOSITORY_ROOT)}
    process = subprocess.run(
        [sys.executable, "-m", "mnamer", *args],
        capture_output=True,
        cwd=workspace,
        env=environment,
        text=True,
        timeout=60,
    )
    assert process.returncode == code
    assert expected in process.stdout
