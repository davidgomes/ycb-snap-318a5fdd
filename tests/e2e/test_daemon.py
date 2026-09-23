import json
import sys
from pathlib import Path

import pytest

pytestmark = pytest.mark.e2e


@pytest.fixture
def run(e2e_run):
    def fn(*args):
        del sys.argv[1:]
        return e2e_run(*args)

    return fn


@pytest.fixture
def dirs(tmp_path: Path):
    watch = tmp_path / "watch"
    movies = tmp_path / "movies"
    watch.mkdir()
    movies.mkdir()
    return watch, movies, tmp_path / "state.json"


def test_start__no_watch(run, dirs):
    _, _, state = dirs
    result = run("--daemon", "start", "--daemon-state", str(state))
    assert result.code == 2


def test_run_once__moves_top_level(run, dirs):
    watch, movies, state = dirs
    (watch / "a.mkv").touch()
    (watch / "b.part").touch()
    (watch / "partial.mkv").touch()
    (watch / "nested").mkdir()
    (watch / "nested" / "c.mkv").touch()
    (movies / "a.mkv").touch()
    result = run(
        "--daemon-run-once",
        "--batch",
        "--watch",
        str(watch),
        "--movie-directory",
        str(movies),
        "--daemon-state",
        str(state),
    )
    assert result.code == 0
    assert sorted(p.name for p in movies.iterdir()) == [
        "a (1).mkv",
        "a.mkv",
        "partial.mkv",
    ]
    assert (watch / "b.part").exists()
    assert (watch / "nested" / "c.mkv").exists()
    data = json.loads(state.read_text())
    assert len(data["processed"]) == 2
    assert Path(f"{state}.log").read_text()


def test_run_once__dry_run(run, dirs):
    watch, movies, state = dirs
    (watch / "a.mkv").touch()
    result = run(
        "--daemon-run-once",
        "--dry-run",
        str(watch),
        "--movie-directory",
        str(movies),
        "--daemon-state",
        str(state),
    )
    assert result.code == 0
    assert result.out == f"{watch / 'a.mkv'} -> {movies / 'a.mkv'}"
    assert (watch / "a.mkv").exists()
    assert not state.exists()


def test_run_once__batch_size_and_exclude(run, dirs, tmp_path):
    watch, movies, state = dirs
    for name in ("a.mkv", "b.mkv", "c.tmp"):
        (watch / name).touch()
    config = tmp_path / "config.json"
    config.write_text(
        json.dumps(
            {
                "watch": [
                    {
                        "path": str(watch),
                        "movie_directory": str(movies),
                        "exclude": ["*.tmp"],
                    }
                ]
            }
        )
    )
    args = ("--daemon-config", str(config), "--daemon-state", str(state))
    assert run("--daemon-run-once", "--batch-size", "0", *args).code == 0
    assert not list(movies.iterdir())
    assert run("--daemon-run-once", "--batch-size", "1", *args).code == 0
    assert len(list(movies.iterdir())) == 1
    assert run("--daemon-run-once", *args).code == 0
    assert sorted(p.name for p in movies.iterdir()) == ["a.mkv", "b.mkv"]
    assert (watch / "c.tmp").exists()


@pytest.mark.parametrize(
    "config",
    ('{"watch": [{"path": 1, "movie_directory": "m"}]}', "{", '{"watch": {}}'),
)
def test_validate__invalid(run, tmp_path, config):
    path = tmp_path / "config.json"
    path.write_text(config)
    result = run("--validate-daemon-config", "--daemon-config", str(path))
    assert result.code == 2
    assert "config" in result.out


def test_validate__missing(run, tmp_path):
    assert run("--validate-daemon-config").code == 2
    missing = str(tmp_path / "missing.json")
    assert run("--validate-daemon-config", "--daemon-config", missing).code == 2


def test_validate__valid(run, tmp_path):
    path = tmp_path / "config.json"
    path.write_text('{"watch": []}')
    result = run("--validate-daemon-config", "--daemon-config", str(path))
    assert result.code == 0


def test_state_path_is_directory(run, tmp_path):
    args = ("--daemon-state", str(tmp_path))
    assert run("--daemon", "status", *args).out == "not running"
    assert run("--daemon", "logs", *args).out == "no logs available"
    assert run("--daemon", "stop", *args).code == 0


def test_logs_and_stats(run, dirs):
    watch, movies, state = dirs
    args = ("--daemon-state", str(state))
    assert run("--daemon", "logs", *args).out == "no logs available"
    for _ in range(3):
        run("--daemon-run-once", str(watch), "--movie-directory", str(movies), *args)
    assert len(run("--daemon", "logs", *args).out.splitlines()) == 3
    assert len(run("--daemon", "logs", "--lines", "2", *args).out.splitlines()) == 2
    stats = run("--daemon", "stats", *args)
    assert stats.code == 0
    assert "processed=0" in stats.out
    assert "last_epoch=" in stats.out


def test_lifecycle(run, dirs):
    watch, movies, state = dirs
    args = ("--daemon-state", str(state))
    start = ("--watch", str(watch), "--movie-directory", str(movies), *args)
    try:
        assert run("--daemon", "start", *start).code == 0
        assert json.loads(state.read_text())
        assert run("--daemon", "status", *args).out == "running"
        assert run("--daemon", "restart", *start).code == 0
        assert run("--daemon", "status", *args).out == "running"
    finally:
        assert run("--daemon", "stop", *args).code == 0
    assert run("--daemon", "status", *args).out == "not running"
    assert run("--daemon", "stop", *args).code == 0
