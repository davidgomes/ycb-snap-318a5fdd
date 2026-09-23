import json
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
from unittest.mock import patch
from urllib.error import URLError

import pytest

from mnamer import daemon
from mnamer.daemon import DaemonOptions, WatchEntry, load_watch_config, run_cycle
from mnamer.exceptions import MnamerDaemonException

pytestmark = pytest.mark.local


@pytest.fixture
def workspace(tmp_path: Path) -> Path:
    for directory in ("watch", "other", "movies"):
        (tmp_path / directory).mkdir()
    return tmp_path


def make_files(root: Path, *names: str) -> None:
    for name in names:
        path = root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(name)


def make_options(workspace: Path, *watches: WatchEntry, **kwargs) -> DaemonOptions:
    kwargs.setdefault("stability_checks", 0)
    return DaemonOptions(
        state_path=workspace / "state.json",
        watches=watches or (WatchEntry(workspace / "watch", workspace / "movies"),),
        **kwargs,
    )


def read_state(workspace: Path) -> dict:
    return json.loads((workspace / "state.json").read_text())


def read_log(workspace: Path) -> list[str]:
    return (workspace / "state.json.log").read_text().splitlines()


def write_config(path: Path, payload) -> Path:
    path.write_text(payload if isinstance(payload, str) else json.dumps(payload))
    return path


def test_load_watch_config(tmp_path: Path):
    config = write_config(
        tmp_path / "config.json",
        {
            "watch": [
                {"path": "/in", "movie_directory": "/out", "exclude": ["*.tmp"]},
                {"path": "/in2", "movie_directory": "/out2"},
            ]
        },
    )
    assert load_watch_config(config) == [
        WatchEntry(Path("/in"), Path("/out"), ("*.tmp",)),
        WatchEntry(Path("/in2"), Path("/out2")),
    ]


def test_load_watch_config__empty_watch_is_valid(tmp_path: Path):
    config = write_config(tmp_path / "config.json", {"watch": []})
    assert load_watch_config(config) == []


def test_load_watch_config__not_found(tmp_path: Path):
    with pytest.raises(MnamerDaemonException, match="config not found"):
        load_watch_config(tmp_path / "missing.json")


@pytest.mark.parametrize(
    "payload",
    (
        "{not json",
        [],
        {},
        {"watch": {"path": "/in", "movie_directory": "/out"}},
        {"watch": ["/in"]},
        {"watch": [{"movie_directory": "/out"}]},
        {"watch": [{"path": 1, "movie_directory": "/out"}]},
        {"watch": [{"path": "/in"}]},
        {"watch": [{"path": "/in", "movie_directory": None}]},
        {"watch": [{"path": "/in", "movie_directory": "/out", "exclude": "*.tmp"}]},
        {"watch": [{"path": "/in", "movie_directory": "/out", "exclude": [1]}]},
    ),
    ids=(
        "malformed-json",
        "not-an-object",
        "missing-watch",
        "watch-not-array",
        "entry-not-object",
        "missing-path",
        "non-string-path",
        "missing-movie-directory",
        "null-movie-directory",
        "exclude-not-array",
        "exclude-non-string",
    ),
)
def test_load_watch_config__invalid_structure(tmp_path: Path, payload):
    config = write_config(tmp_path / "config.json", payload)
    with pytest.raises(MnamerDaemonException, match="config structure"):
        load_watch_config(config)


def test_daemon_options__json_round_trip(workspace: Path):
    options = make_options(
        workspace,
        WatchEntry(workspace / "watch", workspace / "movies", ("*.tmp",)),
        config_path=workspace / "config.json",
        batch_size=3,
        notify_webhook="http://localhost/hook",
    )
    assert DaemonOptions.from_json(options.to_json()) == options


def test_run_cycle__moves_top_level_files_keeping_names(workspace: Path):
    make_files(workspace / "watch", "movie.mkv", "notes.txt", "nested/deep.mkv")
    report = run_cycle(make_options(workspace))
    assert sorted(path.name for path in (workspace / "movies").iterdir()) == [
        "movie.mkv",
        "notes.txt",
    ]
    assert (workspace / "movies" / "movie.mkv").read_text() == "movie.mkv"
    assert (workspace / "watch" / "nested" / "deep.mkv").exists()
    assert len(report.moved) == 2
    assert not report.failed


def test_run_cycle__skips_only_part_suffix(workspace: Path):
    make_files(workspace / "watch", "a.mkv.part", "b.part.mkv", "party.mkv", "part")
    run_cycle(make_options(workspace))
    assert sorted(path.name for path in (workspace / "watch").iterdir()) == [
        "a.mkv.part"
    ]


def test_run_cycle__exclude_patterns_are_per_watch(workspace: Path):
    make_files(workspace / "watch", "a.mkv", "b.tmp", "c.partial")
    make_files(workspace / "other", "d.tmp")
    options = make_options(
        workspace,
        WatchEntry(workspace / "watch", workspace / "movies", ("*.tmp", "*.partial")),
        WatchEntry(workspace / "other", workspace / "movies"),
    )
    run_cycle(options)
    assert sorted(path.name for path in (workspace / "movies").iterdir()) == [
        "a.mkv",
        "d.tmp",
    ]


def test_run_cycle__missing_watch_is_skipped(workspace: Path):
    make_files(workspace / "watch", "a.mkv")
    options = make_options(
        workspace,
        WatchEntry(workspace / "missing", workspace / "movies"),
        WatchEntry(workspace / "watch", workspace / "movies"),
    )
    assert len(run_cycle(options).moved) == 1


def test_run_cycle__never_overwrites(workspace: Path):
    make_files(workspace / "watch", "a.mkv")
    (workspace / "movies" / "a.mkv").write_text("original")
    report = run_cycle(make_options(workspace))
    assert (workspace / "movies" / "a.mkv").read_text() == "original"
    assert (workspace / "movies" / "a (1).mkv").read_text() == "a.mkv"
    assert report.moved == [
        (workspace / "watch" / "a.mkv", workspace / "movies" / "a (1).mkv")
    ]


def test_run_cycle__same_name_from_two_watches(workspace: Path):
    make_files(workspace / "watch", "a.mkv")
    make_files(workspace / "other", "a.mkv")
    options = make_options(
        workspace,
        WatchEntry(workspace / "watch", workspace / "movies"),
        WatchEntry(workspace / "other", workspace / "movies"),
    )
    run_cycle(options)
    assert sorted(path.name for path in (workspace / "movies").iterdir()) == [
        "a (1).mkv",
        "a.mkv",
    ]


def test_run_cycle__batch_size_is_global(workspace: Path):
    make_files(workspace / "watch", "a.mkv", "b.mkv")
    make_files(workspace / "other", "c.mkv", "d.mkv")
    options = make_options(
        workspace,
        WatchEntry(workspace / "watch", workspace / "movies"),
        WatchEntry(workspace / "other", workspace / "movies"),
        batch_size=3,
    )
    assert len(run_cycle(options).moved) == 3
    assert len(list((workspace / "movies").iterdir())) == 3


def test_run_cycle__batch_size_zero_moves_nothing(workspace: Path):
    make_files(workspace / "watch", "a.mkv")
    report = run_cycle(make_options(workspace, batch_size=0))
    assert not report.moved
    assert (workspace / "watch" / "a.mkv").exists()
    assert read_state(workspace)["cycles"] == 1


def test_run_cycle__skips_files_still_growing(workspace: Path):
    make_files(workspace / "watch", "growing.mkv", "done.mkv")
    growing = workspace / "watch" / "growing.mkv"

    def fake_sleep(seconds, should_stop):
        with growing.open("a") as fp:
            fp.write("more")
        return False

    options = make_options(workspace, stability_checks=2, stability_interval_ms=10)
    with patch.object(daemon, "_sleep", side_effect=fake_sleep) as mock_sleep:
        report = run_cycle(options)
    assert mock_sleep.call_count == 2
    assert [source.name for source, _ in report.moved] == ["done.mkv"]
    assert growing.exists()


def test_run_cycle__stability_checks_use_interval(workspace: Path):
    make_files(workspace / "watch", "a.mkv")
    options = make_options(workspace, stability_checks=3, stability_interval_ms=250)
    with patch.object(daemon, "_sleep", return_value=False) as mock_sleep:
        assert len(run_cycle(options).moved) == 1
    assert [call.args[0] for call in mock_sleep.call_args_list] == [0.25] * 3


def test_run_cycle__dry_run_changes_nothing(workspace: Path):
    make_files(workspace / "watch", "a.mkv")
    report = run_cycle(make_options(workspace), dry_run=True)
    assert report.moved == [
        (workspace / "watch" / "a.mkv", workspace / "movies" / "a.mkv")
    ]
    assert (workspace / "watch" / "a.mkv").exists()
    assert not (workspace / "movies" / "a.mkv").exists()
    assert not (workspace / "state.json").exists()
    assert not (workspace / "state.json.log").exists()


def test_run_cycle__records_state_and_log_every_cycle(workspace: Path):
    make_files(workspace / "watch", "a.mkv")
    options = make_options(workspace)
    run_cycle(options)
    first = (workspace / "state.json").read_text()
    run_cycle(options)
    state = read_state(workspace)
    assert (workspace / "state.json").read_text() != first
    assert state["processed"] == [str(workspace / "watch" / "a.mkv")]
    assert state["cycles"] == 2
    assert isinstance(state["updated_epoch"], int)
    log = read_log(workspace)
    assert len(log) == 2
    assert "moved=1" in log[0]
    assert "moved=0" in log[1]


def test_run_cycle__idle_cycles_not_recorded_when_requested(workspace: Path):
    run_cycle(make_options(workspace), record_idle=False)
    assert not (workspace / "state.json").exists()
    assert not (workspace / "state.json.log").exists()


def test_run_cycle__does_not_move_own_files(workspace: Path):
    watch = workspace / "watch"
    make_files(watch, "a.mkv", "config.json")
    options = DaemonOptions(
        state_path=watch / "state.json",
        watches=(WatchEntry(watch, workspace / "movies"),),
        config_path=watch / "config.json",
        stability_checks=0,
    )
    run_cycle(options)
    make_files(watch, "b.mkv")
    run_cycle(options)
    assert sorted(path.name for path in watch.iterdir()) == [
        "config.json",
        "state.json",
        "state.json.log",
    ]


def test_run_cycle__skips_watch_that_is_the_movie_directory(workspace: Path):
    make_files(workspace / "movies", "a.mkv")
    options = make_options(
        workspace, WatchEntry(workspace / "movies", workspace / "movies")
    )
    assert not run_cycle(options).moved
    assert [path.name for path in (workspace / "movies").iterdir()] == ["a.mkv"]


def test_run_cycle__webhook_receives_moves(workspace: Path):
    received = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            length = int(self.headers["Content-Length"])
            received.append(json.loads(self.rfile.read(length)))
            self.send_response(204)
            self.end_headers()

        def log_message(self, *args):
            pass

    server = HTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.handle_request)
    thread.start()
    make_files(workspace / "watch", "a.mkv")
    url = f"http://127.0.0.1:{server.server_port}/hook"
    try:
        run_cycle(make_options(workspace, notify_webhook=url))
    finally:
        thread.join(timeout=5)
        server.server_close()
    assert received == [
        {
            "event": "files_moved",
            "files": [
                {
                    "source": str(workspace / "watch" / "a.mkv"),
                    "destination": str(workspace / "movies" / "a.mkv"),
                }
            ],
        }
    ]


def test_run_cycle__webhook_failure_is_not_fatal(workspace: Path):
    make_files(workspace / "watch", "a.mkv")
    options = make_options(workspace, notify_webhook="http://localhost/hook")
    with patch.object(daemon, "urlopen", side_effect=URLError("refused")):
        report = run_cycle(options)
    assert len(report.moved) == 1
    assert read_state(workspace)["processed"] == [str(workspace / "watch" / "a.mkv")]
    assert "webhook failed" in read_log(workspace)[-1]
