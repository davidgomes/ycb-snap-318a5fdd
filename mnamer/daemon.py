"""Watch-folder daemon which relocates top-level files into a movie directory."""

from __future__ import annotations

import dataclasses
import datetime as dt
import json
import os
import shutil
import signal
import subprocess
import sys
import time
import urllib.request
from fnmatch import fnmatch
from pathlib import Path
from typing import Any

from mnamer.setting_store import SettingStore

EXIT_ERROR = 2
NO_LOGS = "no logs available"
PARTIAL_SUFFIX = ".part"
POLL_INTERVAL_S = 1.0
STOP_TIMEOUT_S = 10.0
WEBHOOK_TIMEOUT_S = 3.0
MAX_UNIQUE_ATTEMPTS = 10_000

# Spawns the daemon in its own session and reports its pid, so the daemon is
# reparented away from the caller and never lingers as the caller's zombie.
_LAUNCHER = (
    "import subprocess, sys; "
    "print(subprocess.Popen(sys.argv[1:], stdin=subprocess.DEVNULL, "
    "stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, "
    "start_new_session=True).pid)"
)


class DaemonConfigError(Exception):
    pass


@dataclasses.dataclass(frozen=True)
class WatchEntry:
    path: Path
    movie_directory: Path | None
    exclude: tuple[str, ...] = ()

    def as_dict(self) -> dict[str, Any]:
        return {
            "path": str(self.path),
            "movie_directory": (
                str(self.movie_directory) if self.movie_directory else None
            ),
            "exclude": list(self.exclude),
        }

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> WatchEntry:
        movie_directory = d.get("movie_directory")
        return cls(
            path=Path(d["path"]),
            movie_directory=Path(movie_directory) if movie_directory else None,
            exclude=tuple(d.get("exclude") or ()),
        )


@dataclasses.dataclass
class DaemonOptions:
    watches: list[WatchEntry]
    state_path: Path
    stability_interval_ms: int
    stability_checks: int
    batch_size: int | None
    notify_webhook: str | None

    @property
    def log_path(self) -> Path:
        return log_path_for(self.state_path)

    def as_dict(self) -> dict[str, Any]:
        return {
            "watches": [watch.as_dict() for watch in self.watches],
            "state_path": str(self.state_path),
            "stability_interval_ms": self.stability_interval_ms,
            "stability_checks": self.stability_checks,
            "batch_size": self.batch_size,
            "notify_webhook": self.notify_webhook,
        }

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> DaemonOptions:
        return cls(
            watches=[WatchEntry.from_dict(watch) for watch in d["watches"]],
            state_path=Path(d["state_path"]),
            stability_interval_ms=d["stability_interval_ms"],
            stability_checks=d["stability_checks"],
            batch_size=d["batch_size"],
            notify_webhook=d["notify_webhook"],
        )


@dataclasses.dataclass(frozen=True)
class Move:
    source: Path
    destination: Path

    def __str__(self) -> str:
        return f"{self.source} -> {self.destination}"


# paths ------------------------------------------------------------------------


def log_path_for(state_path: Path) -> Path:
    return Path(f"{state_path}.log")


def pid_path_for(state_path: Path) -> Path:
    return Path(f"{state_path}.pid")


def _is_daemon_file(path: Path, state_path: Path) -> bool:
    """Prevents the daemon from relocating its own state, log, or pid files."""
    return path.parent == state_path.parent and (
        path.name == state_path.name or path.name.startswith(f"{state_path.name}.")
    )


# configuration ----------------------------------------------------------------


def parse_daemon_config(data: Any) -> list[WatchEntry]:
    prefix = "invalid daemon config structure"
    if not isinstance(data, dict) or not isinstance(data.get("watch"), list):
        raise DaemonConfigError(f'{prefix}: expected an object with a "watch" array')
    entries = []
    for index, entry in enumerate(data["watch"]):
        where = f"watch[{index}]"
        if not isinstance(entry, dict):
            raise DaemonConfigError(f"{prefix}: {where} must be an object")
        for key in ("path", "movie_directory"):
            if not isinstance(entry.get(key), str) or not entry[key]:
                raise DaemonConfigError(f'{prefix}: {where} "{key}" must be a string')
        exclude = entry.get("exclude", [])
        if not isinstance(exclude, list) or not all(
            isinstance(pattern, str) for pattern in exclude
        ):
            raise DaemonConfigError(
                f'{prefix}: {where} "exclude" must be an array of strings'
            )
        entries.append(
            WatchEntry(
                path=Path(entry["path"]).resolve(),
                movie_directory=Path(entry["movie_directory"]).resolve(),
                exclude=tuple(exclude),
            )
        )
    return entries


def load_daemon_config(path: str | Path) -> list[WatchEntry]:
    config_path = Path(path)
    if not config_path.is_file():
        raise DaemonConfigError(f"daemon config not found: {config_path}")
    try:
        data = json.loads(config_path.read_text())
    except (OSError, UnicodeDecodeError, ValueError) as e:
        raise DaemonConfigError(
            f"invalid daemon config structure: could not parse JSON ({e})"
        ) from e
    return parse_daemon_config(data)


def options_from_settings(settings: SettingStore) -> DaemonOptions:
    watches: list[WatchEntry] = []
    for path in [*settings.watch, *settings.targets]:
        watches.append(WatchEntry(Path(path).resolve(), settings.movie_directory))
    if settings.daemon_config:
        watches.extend(load_daemon_config(settings.daemon_config))
    return DaemonOptions(
        watches=list(dict.fromkeys(watches)),
        state_path=Path(settings.daemon_state).absolute(),
        stability_interval_ms=max(settings.stability_interval_ms, 0),
        stability_checks=max(settings.stability_checks, 0),
        batch_size=settings.batch_size,
        notify_webhook=settings.notify_webhook,
    )


# state ------------------------------------------------------------------------


def read_state(state_path: Path) -> dict[str, Any]:
    try:
        data = json.loads(state_path.read_text())
    except (OSError, UnicodeDecodeError, ValueError):
        return {}
    return data if isinstance(data, dict) else {}


def _write_state(state_path: Path, state: dict[str, Any]) -> None:
    state_path.parent.mkdir(parents=True, exist_ok=True)
    tmp_path = Path(f"{state_path}.tmp")
    tmp_path.write_text(json.dumps(state, indent=2, sort_keys=True))
    os.replace(tmp_path, state_path)


def _initialize_state(state_path: Path) -> None:
    state = read_state(state_path)
    processed = state.get("processed")
    state["processed"] = processed if isinstance(processed, list) else []
    state.setdefault("cycles", 0)
    state["updated_epoch"] = int(time.time())
    _write_state(state_path, state)


def _append_log(log_path: Path, line: str) -> None:
    timestamp = dt.datetime.now(dt.UTC).strftime("%Y-%m-%dT%H:%M:%SZ")
    with log_path.open("a") as fp:
        fp.write(f"{timestamp} {line}\n")


# processing -------------------------------------------------------------------


def _file_size(path: Path) -> int | None:
    try:
        return path.stat().st_size
    except OSError:
        return None


def _unique_destination(destination: Path, reserved: set[Path]) -> Path | None:
    candidate = destination
    for attempt in range(1, MAX_UNIQUE_ATTEMPTS):
        if candidate not in reserved and not os.path.lexists(candidate):
            return candidate
        candidate = destination.with_name(
            f"{destination.stem} ({attempt}){destination.suffix}"
        )
    return None


def _scan(options: DaemonOptions) -> list[tuple[Path, WatchEntry]]:
    candidates: dict[Path, WatchEntry] = {}
    for watch in options.watches:
        if not watch.movie_directory or not watch.path.is_dir():
            continue
        if watch.path == watch.movie_directory:
            continue
        try:
            children = sorted(watch.path.iterdir())
        except OSError:
            continue
        for child in children:
            if child in candidates or not child.is_file():
                continue
            if child.name.endswith(PARTIAL_SUFFIX):
                continue
            if any(fnmatch(child.name, pattern) for pattern in watch.exclude):
                continue
            if _is_daemon_file(child, options.state_path):
                continue
            candidates[child] = watch
    return list(candidates.items())


def _filter_stable(
    candidates: list[tuple[Path, WatchEntry]], interval_ms: int, checks: int
) -> list[tuple[Path, WatchEntry]]:
    sizes = {path: _file_size(path) for path, _ in candidates}
    unstable = {path for path, size in sizes.items() if size is None}
    if candidates:
        for _ in range(checks):
            time.sleep(interval_ms / 1000)
            for path, _ in candidates:
                size = _file_size(path)
                if size is None or size != sizes[path]:
                    unstable.add(path)
                sizes[path] = size
    return [(path, watch) for path, watch in candidates if path not in unstable]


def plan_cycle(options: DaemonOptions) -> list[Move]:
    """Determines which files would be moved in a single cycle."""
    if options.batch_size is not None and options.batch_size <= 0:
        return []
    candidates = _filter_stable(
        _scan(options), options.stability_interval_ms, options.stability_checks
    )
    moves: list[Move] = []
    reserved: set[Path] = set()
    for path, watch in candidates:
        if options.batch_size is not None and len(moves) >= options.batch_size:
            break
        assert watch.movie_directory
        destination = _unique_destination(watch.movie_directory / path.name, reserved)
        if destination is None:
            continue
        reserved.add(destination)
        moves.append(Move(path, destination))
    return moves


def _relocate(move: Move) -> bool:
    try:
        move.destination.parent.mkdir(parents=True, exist_ok=True)
        if os.path.lexists(move.destination):
            return False
        shutil.move(str(move.source), str(move.destination))
    except OSError:
        return False
    return True


def _notify(url: str, moves: list[Move]) -> bool:
    payload = {
        "event": "files_moved",
        "files": [
            {"source": str(move.source), "destination": str(move.destination)}
            for move in moves
        ],
    }
    request = urllib.request.Request(
        url,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=WEBHOOK_TIMEOUT_S):
            return True
    except Exception:
        return False


def run_cycle(options: DaemonOptions) -> list[Move]:
    """Moves stable files, then records the cycle in the state and log files."""
    planned = plan_cycle(options)
    moved = [move for move in planned if _relocate(move)]
    state = read_state(options.state_path)
    processed = state.get("processed")
    processed = processed if isinstance(processed, list) else []
    processed.extend(str(move.source) for move in moved)
    cycles = state.get("cycles")
    state.update(
        processed=processed,
        updated_epoch=int(time.time()),
        cycles=(cycles if isinstance(cycles, int) else 0) + 1,
        last_cycle={
            "moved": [
                {"source": str(move.source), "destination": str(move.destination)}
                for move in moved
            ],
            "failed": len(planned) - len(moved),
        },
    )
    _write_state(options.state_path, state)
    line = (
        f"cycle={state['cycles']} moved={len(moved)} failed={len(planned) - len(moved)}"
    )
    if moved:
        line += " files=[" + "; ".join(str(move) for move in moved) + "]"
    if moved and options.notify_webhook:
        line += " webhook=" + (
            "ok" if _notify(options.notify_webhook, moved) else "failed"
        )
    _append_log(options.log_path, line)
    return moved


def serve(options: DaemonOptions) -> None:  # pragma: no cover
    """Runs processing cycles until terminated; executed in the daemon process."""
    stopping = False

    def request_stop(*_):
        nonlocal stopping
        stopping = True

    signal.signal(signal.SIGTERM, request_stop)
    signal.signal(signal.SIGINT, request_stop)
    pid_path = pid_path_for(options.state_path)
    try:
        while not stopping:
            try:
                run_cycle(options)
            except Exception:
                pass
            deadline = time.monotonic() + POLL_INTERVAL_S
            while not stopping and time.monotonic() < deadline:
                time.sleep(0.05)
    finally:
        if _read_pid(pid_path) == os.getpid():
            pid_path.unlink(missing_ok=True)


# process management -----------------------------------------------------------


def _read_pid(pid_path: Path) -> int | None:
    try:
        return int(pid_path.read_text().strip())
    except (OSError, ValueError):
        return None


def _is_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    # where procfs is available, reject zombies and recycled pids
    proc = Path("/proc", str(pid))
    try:
        if proc.joinpath("stat").read_text().rsplit(")", 1)[1].split()[0] == "Z":
            return False
        return b"mnamer.daemon" in proc.joinpath("cmdline").read_bytes()
    except (OSError, IndexError):
        return True


def running_pid(state_path: Path) -> int | None:
    if state_path.is_dir():
        return None
    pid = _read_pid(pid_path_for(state_path))
    return pid if pid and _is_alive(pid) else None


def _spawn(options: DaemonOptions) -> int:
    package_root = str(Path(__file__).resolve().parent.parent)
    env = dict(os.environ)
    env["PYTHONPATH"] = os.pathsep.join(
        filter(None, [package_root, env.get("PYTHONPATH")])
    )
    command = [sys.executable, "-m", "mnamer.daemon", json.dumps(options.as_dict())]
    result = subprocess.run(
        [sys.executable, "-c", _LAUNCHER, *command],
        capture_output=True,
        check=True,
        env=env,
        stdin=subprocess.DEVNULL,
        text=True,
    )
    return int(result.stdout.strip())


def _stop(state_path: Path) -> bool:
    """Stops a running daemon; returns whether one was running."""
    pid = running_pid(state_path)
    if pid is None:
        if not state_path.is_dir():
            pid_path_for(state_path).unlink(missing_ok=True)
        return False
    try:
        os.kill(pid, signal.SIGTERM)
        deadline = time.monotonic() + STOP_TIMEOUT_S
        while _is_alive(pid) and time.monotonic() < deadline:
            time.sleep(0.05)
        if _is_alive(pid):
            os.kill(pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    pid_path_for(state_path).unlink(missing_ok=True)
    return True


# commands ---------------------------------------------------------------------


def _cmd_start(settings: SettingStore) -> int:
    try:
        options = options_from_settings(settings)
    except DaemonConfigError as e:
        print(e)
        return EXIT_ERROR
    if not options.watches:
        print(
            "no watch directories specified; use --watch, targets, or --daemon-config"
        )
        return EXIT_ERROR
    if options.state_path.is_dir():
        print(f"daemon state path is a directory: {options.state_path}")
        return EXIT_ERROR
    pid = running_pid(options.state_path)
    if pid is not None:
        print(f"already running (pid {pid})")
        return 0
    _initialize_state(options.state_path)
    pid = _spawn(options)
    pid_path_for(options.state_path).write_text(str(pid))
    print(f"started (pid {pid})")
    return 0


def _cmd_stop(state_path: Path) -> int:
    print("stopped" if _stop(state_path) else "not running")
    return 0


def _cmd_restart(settings: SettingStore, state_path: Path) -> int:
    if _stop(state_path):
        print("stopped")
    return _cmd_start(settings)


def _cmd_status(state_path: Path) -> int:
    print("running" if running_pid(state_path) else "not running")
    return 0


def _cmd_logs(state_path: Path, lines: int | None) -> int:
    log_path = log_path_for(state_path)
    content = ""
    if not state_path.is_dir() and log_path.is_file():
        try:
            content = log_path.read_text()
        except (OSError, UnicodeDecodeError):
            content = ""
    if not content.strip():
        print(NO_LOGS)
        return 0
    log_lines = content.splitlines()
    if lines is not None:
        log_lines = log_lines[-lines:] if lines > 0 else []
    if log_lines:
        print("\n".join(log_lines))
    return 0


def _cmd_stats(state_path: Path) -> int:
    state = {} if state_path.is_dir() else read_state(state_path)
    processed = state.get("processed")
    processed_count = len(processed) if isinstance(processed, list) else 0
    try:
        last_epoch = int(state.get("updated_epoch") or 0)
    except (TypeError, ValueError):
        last_epoch = 0
    print(f"processed={processed_count}, last_epoch={last_epoch}")
    return 0


def _cmd_validate(settings: SettingStore) -> int:
    if not settings.daemon_config:
        print("--validate-daemon-config requires --daemon-config <path>")
        return EXIT_ERROR
    try:
        entries = load_daemon_config(settings.daemon_config)
    except DaemonConfigError as e:
        print(e)
        return EXIT_ERROR
    print(f"daemon config is valid ({len(entries)} watch entries)")
    return 0


def _cmd_run_once(settings: SettingStore) -> int:
    try:
        options = options_from_settings(settings)
    except DaemonConfigError as e:
        print(e)
        return EXIT_ERROR
    if settings.dry_run:
        for move in plan_cycle(options):
            print(move)
        return 0
    if options.state_path.is_dir():
        print(f"daemon state path is a directory: {options.state_path}")
        return EXIT_ERROR
    moved = run_cycle(options)
    for move in moved:
        print(move)
    print(f"processed {len(moved)} file(s)")
    return 0


def dispatch(settings: SettingStore) -> int | None:
    """Runs the requested daemon directive, returning its exit code, if any."""
    state_path = Path(settings.daemon_state).absolute()
    if settings.daemon == "start":
        return _cmd_start(settings)
    if settings.daemon == "stop":
        return _cmd_stop(state_path)
    if settings.daemon == "restart":
        return _cmd_restart(settings, state_path)
    if settings.daemon == "status":
        return _cmd_status(state_path)
    if settings.daemon == "logs":
        return _cmd_logs(state_path, settings.lines)
    if settings.daemon == "stats":
        return _cmd_stats(state_path)
    if settings.validate_daemon_config:
        return _cmd_validate(settings)
    if settings.daemon_run_once:
        return _cmd_run_once(settings)
    return None


if __name__ == "__main__":  # pragma: no cover
    serve(DaemonOptions.from_dict(json.loads(sys.argv[1])))
