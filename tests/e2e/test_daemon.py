import json
import sys
import time
from pathlib import Path

import pytest

pytestmark = [pytest.mark.e2e, pytest.mark.usefixtures("setup_test_dir")]

FAST = ("--stability-checks", "0")


@pytest.fixture
def e2e_run(e2e_run):
    """Runs each invocation with a fresh argv, allowing several per test."""

    def fn(*args):
        del sys.argv[1:]
        return e2e_run(*args)

    return fn


@pytest.fixture
def daemon_dirs():
    Path("watch").mkdir()
    Path("movies").mkdir()
    return Path("watch").absolute(), Path("movies").absolute()


def _write_config(watch: list[dict], path: str = "daemon.json") -> str:
    Path(path).write_text(json.dumps({"watch": watch}))
    return path


def test_run_once__moves_top_level_files(e2e_run, daemon_dirs, setup_test_files):
    watch, movies = daemon_dirs
    setup_test_files("watch/a.mkv", "watch/nested/b.mkv", "watch/c.part")
    setup_test_files("watch/partial.film.mkv")
    result = e2e_run(
        "--daemon-run-once", "--watch", "watch", "--movie-directory", "movies", *FAST
    )
    assert result.code == 0
    assert sorted(p.name for p in movies.iterdir()) == ["a.mkv", "partial.film.mkv"]
    assert (watch / "nested" / "b.mkv").exists()
    assert (watch / "c.part").exists()


def test_run_once__combines_watch_and_positional(e2e_run, setup_test_files):
    setup_test_files("w1/a.mkv", "w2/b.mkv", "w3/c.mkv")
    result = e2e_run(
        "--batch",
        "--daemon-run-once",
        "--watch",
        "w1",
        "w2",
        "--movie-directory",
        "m",
        "w3",
        *FAST,
    )
    assert result.code == 0
    assert sorted(p.name for p in Path("m").iterdir()) == ["a.mkv", "b.mkv", "c.mkv"]


def test_run_once__config_exclude_and_cli_combined(e2e_run, setup_test_files):
    setup_test_files("cfg/a.mkv", "cfg/b.tmp", "cfg/c.partial", "cli/d.mkv")
    config = _write_config(
        [{"path": "cfg", "movie_directory": "m1", "exclude": ["*.tmp", "*.partial"]}]
    )
    result = e2e_run(
        "--daemon-run-once",
        "--daemon-config",
        config,
        "--watch",
        "cli",
        "--movie-directory",
        "m2",
        *FAST,
    )
    assert result.code == 0
    assert [p.name for p in Path("m1").iterdir()] == ["a.mkv"]
    assert [p.name for p in Path("m2").iterdir()] == ["d.mkv"]
    assert Path("cfg/b.tmp").exists() and Path("cfg/c.partial").exists()


def test_run_once__never_overwrites(e2e_run, setup_test_files):
    setup_test_files("w/a.mkv", "m/a.mkv")
    Path("m/a.mkv").write_text("original")
    result = e2e_run(
        "--daemon-run-once", "--watch", "w", "--movie-directory", "m", *FAST
    )
    assert result.code == 0
    assert Path("m/a.mkv").read_text() == "original"
    assert not Path("w/a.mkv").exists()
    assert len(list(Path("m").iterdir())) == 2


@pytest.mark.parametrize("size,expected", ((0, 0), (1, 1), (2, 2)))
def test_run_once__batch_size_is_global(e2e_run, setup_test_files, size, expected):
    setup_test_files("w1/a.mkv", "w1/b.mkv", "w2/c.mkv")
    result = e2e_run(
        "--daemon-run-once", "--watch", "w1", "w2", "--movie-directory", "m",
        "--batch-size", str(size), *FAST,
    )  # fmt: skip
    assert result.code == 0
    moved = list(Path("m").iterdir()) if Path("m").exists() else []
    assert len(moved) == expected


def test_run_once__skips_nonexistent_watch(e2e_run, setup_test_files):
    setup_test_files("w/a.mkv")
    result = e2e_run(
        "--daemon-run-once", "--watch", "missing", "w", "--movie-directory", "m", *FAST
    )
    assert result.code == 0
    assert Path("m/a.mkv").exists()


def test_run_once__updates_state_and_log_each_cycle(e2e_run):
    e2e_run("--daemon-run-once", *FAST)
    first = Path("daemon-state.json").read_text()
    e2e_run("--daemon-run-once", *FAST)
    second = Path("daemon-state.json").read_text()
    assert first and second and first != second
    assert len(Path("daemon-state.json.log").read_text().splitlines()) == 2
    result = e2e_run("--daemon", "logs", "--lines", "1")
    assert result.code == 0
    assert len(result.out.splitlines()) == 1
    assert "cycle=2" in result.out


def test_run_once__dry_run(e2e_run, setup_test_files):
    setup_test_files("w/a.mkv", "w/b.mkv")
    result = e2e_run(
        "--daemon-run-once",
        "--dry-run",
        "--watch",
        "w",
        "--movie-directory",
        "m",
        *FAST,
    )
    assert result.code == 0
    lines = result.out.splitlines()
    assert len(lines) == 2
    assert all(" -> " in line for line in lines)
    assert Path("w/a.mkv").exists() and Path("w/b.mkv").exists()
    assert not Path("daemon-state.json").exists()
    assert not Path("daemon-state.json.log").exists()


def test_run_once__skips_unstable_files(e2e_run, setup_test_files, monkeypatch):
    from mnamer import daemon

    setup_test_files("w/growing.mkv", "w/steady.mkv")
    original = daemon._file_size

    def growing_size(path: Path):
        if path.name == "growing.mkv":
            return time.monotonic_ns()
        return original(path)

    monkeypatch.setattr(daemon, "_file_size", growing_size)
    result = e2e_run(
        "--daemon-run-once", "--watch", "w", "--movie-directory", "m",
        "--stability-interval-ms", "10", "--stability-checks", "2",
    )  # fmt: skip
    assert result.code == 0
    assert [p.name for p in Path("m").iterdir()] == ["steady.mkv"]


def test_run_once__webhook_failure_is_non_fatal(e2e_run, setup_test_files):
    setup_test_files("w/a.mkv")
    result = e2e_run(
        "--daemon-run-once", "--watch", "w", "--movie-directory", "m",
        "--notify-webhook", "http://127.0.0.1:9/hook", *FAST,
    )  # fmt: skip
    assert result.code == 0
    assert Path("m/a.mkv").exists()


def test_logs__missing(e2e_run):
    result = e2e_run("--daemon", "logs")
    assert result.code == 0
    assert result.out == "no logs available"


def test_stats__empty(e2e_run):
    result = e2e_run("--daemon", "stats")
    assert result.code == 0
    assert result.out == "processed=0, last_epoch=0"


def test_state_path_is_directory(e2e_run):
    Path("state").mkdir()
    for command, expected in (("status", "not running"), ("logs", "no logs available")):
        result = e2e_run("--daemon", command, "--daemon-state", "state")
        assert result.code == 0
        assert result.out == expected
    assert e2e_run("--daemon", "stop", "--daemon-state", "state").code == 0


def test_start__requires_watch(e2e_run):
    assert e2e_run("--daemon", "start").code == 2
    config = _write_config([])
    assert e2e_run("--daemon", "start", "--daemon-config", config).code == 2


def test_lifecycle(e2e_run):
    Path("w").mkdir()
    args = ("--watch", "w", "--movie-directory", "m", *FAST)
    try:
        started = time.monotonic()
        result = e2e_run("--daemon", "start", *args)
        assert result.code == 0
        assert time.monotonic() - started < 5
        assert json.loads(Path("daemon-state.json").read_text())
        assert e2e_run("--daemon", "status").out == "running"
        Path("w/a.mkv").touch()
        deadline = time.monotonic() + 10
        while not Path("m/a.mkv").exists() and time.monotonic() < deadline:
            time.sleep(0.1)
        assert Path("m/a.mkv").exists()
        assert e2e_run("--daemon", "restart", *args).code == 0
        assert e2e_run("--daemon", "status").out == "running"
    finally:
        assert e2e_run("--daemon", "stop").code == 0
    assert e2e_run("--daemon", "status").out == "not running"
    assert e2e_run("--daemon", "stop").code == 0
    assert e2e_run("--daemon", "stats").out.startswith("processed=1, last_epoch=")


def test_validate__requires_config(e2e_run):
    assert e2e_run("--validate-daemon-config").code == 2


def test_validate__config_not_found(e2e_run):
    assert e2e_run("--validate-daemon-config", "--daemon-config", "nope.json").code == 2


@pytest.mark.parametrize(
    "watch",
    (
        [{"movie_directory": "m"}],
        [{"path": 1, "movie_directory": "m"}],
        [{"path": "w"}],
        [{"path": "w", "movie_directory": "m", "exclude": "*.tmp"}],
        [{"path": "w", "movie_directory": "m", "exclude": [1]}],
    ),
)
def test_validate__invalid(e2e_run, watch):
    result = e2e_run(
        "--validate-daemon-config", "--daemon-config", _write_config(watch)
    )
    assert result.code == 2
    assert "config" in result.out and "structure" in result.out


@pytest.mark.parametrize(
    "watch",
    ([], [{"path": "w", "movie_directory": "m", "exclude": ["*.tmp"]}]),
)
def test_validate__valid(e2e_run, watch):
    result = e2e_run(
        "--validate-daemon-config", "--daemon-config", _write_config(watch)
    )
    assert result.code == 0
