"""
A lightweight watch daemon which moves files found at the top level of watch
directories into a movie directory, keeping their names. It never uses the
network (besides an optional webhook) and never prompts.
"""

import fnmatch
import json
import os
import shutil
import signal
import subprocess
import sys
import time
import urllib.request
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any

from mnamer.exceptions import MnamerException
from mnamer.setting_store import SettingStore

DEFAULT_STABILITY_INTERVAL_MS = 500
DEFAULT_STABILITY_CHECKS = 0
LOOP_INTERVAL_SECONDS = 2.0
WEBHOOK_TIMEOUT_SECONDS = 5


class DaemonConfigError(MnamerException):
    """Raised when a daemon config file is missing or malformed."""


@dataclass
class Watch:
    path: str
    movie_directory: str | None
    exclude: list[str] = field(default_factory=list)


@dataclass
class Options:
    watches: list[Watch]
    state_path: str
    stability_interval_ms: int
    stability_checks: int
    batch_size: int | None
    notify_webhook: str | None

    def to_json(self) -> str:
        return json.dumps(asdict(self))

    @classmethod
    def from_json(cls, payload: str) -> "Options":
        data = json.loads(payload)
        data["watches"] = [Watch(**w) for w in data["watches"]]
        return cls(**data)


def is_daemon_request(settings: SettingStore) -> bool:
    return bool(
        settings.daemon or settings.daemon_run_once or settings.validate_daemon_config
    )


def dispatch(settings: SettingStore) -> int:
    """Runs the requested daemon directive and returns the process exit code."""
    try:
        if settings.validate_daemon_config:
            return _validate(settings)
        if settings.daemon_run_once:
            return _run_once(settings)
        command = settings.daemon
        if command == "start":
            return _start(settings)
        if command == "stop":
            return _stop(settings)
        if command == "restart":
            _stop(settings, quiet=True)
            return _start(settings)
        if command == "status":
            print("running" if _running_pid(_state_path(settings)) else "not running")
            return 0
        if command == "logs":
            return _logs(settings)
        if command == "stats":
            return _stats(settings)
    except MnamerException as e:
        print(e)
        return 2
    print(f"unknown daemon command: {command}")
    return 2


# settings ---------------------------------------------------------------------


def _state_path(settings: SettingStore) -> Path:
    return Path(settings.daemon_state or "daemon-state.json").absolute()


def _log_path(state_path: Path) -> Path:
    return Path(f"{state_path}.log")


def _int_option(value: str | None, name: str, default: int | None) -> int | None:
    if value is None:
        return default
    try:
        number = int(value)
    except (TypeError, ValueError):
        raise MnamerException(f"{name} must be an integer") from None
    if number < 0:
        raise MnamerException(f"{name} must not be negative")
    return number


def load_config(path: str) -> list[Watch]:
    """Loads and validates a daemon config file, returning its watches."""
    config_path = Path(path)
    if not config_path.is_file():
        raise DaemonConfigError(f"daemon config not found: {path}")
    try:
        data = json.loads(config_path.read_text())
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as e:
        raise DaemonConfigError(
            f"invalid daemon config structure: could not parse JSON ({e})"
        ) from None
    if not isinstance(data, dict):
        raise DaemonConfigError(
            "invalid daemon config structure: top level must be an object"
        )
    entries = data.get("watch")
    if not isinstance(entries, list):
        raise DaemonConfigError(
            "invalid daemon config structure: 'watch' must be an array"
        )
    watches = []
    for i, entry in enumerate(entries):
        if not isinstance(entry, dict):
            raise DaemonConfigError(
                f"invalid daemon config structure: watch[{i}] must be an object"
            )
        for key in ("path", "movie_directory"):
            if not isinstance(entry.get(key), str):
                raise DaemonConfigError(
                    f"invalid daemon config structure: watch[{i}].{key} must be a string"
                )
        exclude = entry.get("exclude", [])
        if not isinstance(exclude, list) or not all(
            isinstance(pattern, str) for pattern in exclude
        ):
            raise DaemonConfigError(
                f"invalid daemon config structure: watch[{i}].exclude must be an array of strings"
            )
        watches.append(Watch(entry["path"], entry["movie_directory"], exclude))
    return watches


def _options(settings: SettingStore) -> Options:
    movie_directory = (
        str(settings.movie_directory) if settings.movie_directory else None
    )
    watches = [
        Watch(str(path), movie_directory)
        for path in [*settings.watch, *settings.targets]
    ]
    if settings.daemon_config:
        watches += load_config(settings.daemon_config)
    for watch in watches:
        watch.path = str(Path(watch.path).absolute())
        if watch.movie_directory:
            watch.movie_directory = str(Path(watch.movie_directory).absolute())
    return Options(
        watches=watches,
        state_path=str(_state_path(settings)),
        stability_interval_ms=_int_option(
            settings.stability_interval_ms,
            "--stability-interval-ms",
            DEFAULT_STABILITY_INTERVAL_MS,
        ),
        stability_checks=_int_option(
            settings.stability_checks, "--stability-checks", DEFAULT_STABILITY_CHECKS
        ),
        batch_size=_int_option(settings.batch_size, "--batch-size", None),
        notify_webhook=settings.notify_webhook,
    )


# state ------------------------------------------------------------------------


def _read_state(state_path: Path) -> dict[str, Any]:
    try:
        data = json.loads(state_path.read_text())
    except (OSError, UnicodeDecodeError, json.JSONDecodeError):
        return {}
    return data if isinstance(data, dict) else {}


def _write_state(state_path: Path, state: dict[str, Any]) -> None:
    state_path.parent.mkdir(parents=True, exist_ok=True)
    tmp_path = state_path.with_name(f".{state_path.name}.{os.getpid()}.tmp")
    tmp_path.write_text(json.dumps(state, indent=2, sort_keys=True))
    os.replace(tmp_path, state_path)


def _init_state(state: dict[str, Any]) -> dict[str, Any]:
    state.setdefault("processed", [])
    state.setdefault("cycles", 0)
    state.setdefault("updated_epoch", int(time.time()))
    state.setdefault("pid", None)
    return state


def _append_log(state_path: Path, message: str) -> None:
    timestamp = time.strftime("%Y-%m-%dT%H:%M:%S")
    with _log_path(state_path).open("a") as fp:
        fp.write(f"{timestamp} {message}\n")


def _pid_alive(pid: int) -> bool:
    try:
        # reap the process if it is our own exited child
        os.waitpid(pid, os.WNOHANG)
    except (ChildProcessError, OSError):
        pass
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    try:
        stat = Path(f"/proc/{pid}/stat").read_text()
        return stat.rsplit(")", 1)[1].split()[0] != "Z"
    except (OSError, IndexError):
        return True


def _running_pid(state_path: Path) -> int | None:
    if not state_path.is_file():
        return None
    pid = _read_state(state_path).get("pid")
    if isinstance(pid, int) and pid > 0 and _pid_alive(pid):
        return pid
    return None


# processing -------------------------------------------------------------------


def _is_stable(path: Path, checks: int, interval_ms: int) -> bool:
    try:
        size = path.stat().st_size
        for _ in range(checks):
            time.sleep(interval_ms / 1000)
            if path.stat().st_size != size:
                return False
    except OSError:
        return False
    return True


def _unique_destination(directory: Path, name: str, taken: set[Path]) -> Path:
    destination = directory / name
    stem, suffix = Path(name).stem, Path(name).suffix
    counter = 1
    while destination.exists() or destination in taken:
        destination = directory / f"{stem} ({counter}){suffix}"
        counter += 1
    return destination


def _candidates(watch: Watch) -> list[Path]:
    directory = Path(watch.path)
    if not directory.is_dir():
        return []
    files = []
    for entry in sorted(directory.iterdir()):
        if not entry.is_file() or entry.name.endswith(".part"):
            continue
        if any(fnmatch.fnmatch(entry.name, p) for p in watch.exclude):
            continue
        files.append(entry)
    return files


def _plan(options: Options) -> list[tuple[Path, Path]]:
    """Determines (source, destination) pairs for a single cycle."""
    moves: list[tuple[Path, Path]] = []
    taken: set[Path] = set()
    for watch in options.watches:
        if not watch.movie_directory:
            continue
        movie_directory = Path(watch.movie_directory)
        for source in _candidates(watch):
            if options.batch_size is not None and len(moves) >= options.batch_size:
                return moves
            if not _is_stable(
                source, options.stability_checks, options.stability_interval_ms
            ):
                continue
            destination = _unique_destination(movie_directory, source.name, taken)
            if destination == source:
                continue
            taken.add(destination)
            moves.append((source, destination))
    return moves


def _move(source: Path, destination: Path) -> bool:
    try:
        destination.parent.mkdir(parents=True, exist_ok=True)
        if destination.exists():
            return False
        shutil.move(str(source), str(destination))
    except OSError:
        return False
    return True


def _notify(url: str, payload: dict[str, Any]) -> None:
    try:
        request = urllib.request.Request(
            url,
            data=json.dumps(payload).encode(),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        urllib.request.urlopen(request, timeout=WEBHOOK_TIMEOUT_SECONDS).close()
    except Exception:  # noqa: BLE001 - webhook failures are never fatal
        pass


def run_cycle(
    options: Options, dry_run: bool = False, owner_pid: int | None = None
) -> list[tuple[Path, Path]]:
    """Runs one scan cycle, returning the (source, destination) pairs moved."""
    moves = _plan(options)
    if dry_run:
        return moves
    moved = [(src, dst) for src, dst in moves if _move(src, dst)]
    state_path = Path(options.state_path)
    state = _init_state(_read_state(state_path))
    if owner_pid:
        state["pid"] = owner_pid
    state["processed"].extend(str(src) for src, _ in moved)
    state["cycles"] += 1
    state["updated_epoch"] = int(time.time())
    state["updated_at"] = time.time()
    _write_state(state_path, state)
    _append_log(
        state_path,
        f"cycle={state['cycles']} moved={len(moved)} "
        f"failed={len(moves) - len(moved)} watches={len(options.watches)}",
    )
    for src, dst in moved:
        _append_log(state_path, f"moved {src} -> {dst}")
    if options.notify_webhook and moved:
        _notify(
            options.notify_webhook,
            {"moved": [{"source": str(s), "destination": str(d)} for s, d in moved]},
        )
    return moved


# commands ---------------------------------------------------------------------


def _validate(settings: SettingStore) -> int:
    if not settings.daemon_config:
        raise DaemonConfigError("--validate-daemon-config requires --daemon-config")
    watches = load_config(settings.daemon_config)
    print(f"daemon config valid ({len(watches)} watch entries)")
    return 0


def _run_once(settings: SettingStore) -> int:
    options = _options(settings)
    if settings.dry_run:
        for src, dst in run_cycle(options, dry_run=True):
            print(f"{src} -> {dst}")
        return 0
    if Path(options.state_path).is_dir():
        raise MnamerException(f"state path is a directory: {options.state_path}")
    for src, dst in run_cycle(options):
        print(f"{src} -> {dst}")
    return 0


def _start(settings: SettingStore) -> int:
    options = _options(settings)
    if not options.watches:
        raise MnamerException("no watch paths given; use --watch or --daemon-config")
    state_path = Path(options.state_path)
    if state_path.is_dir():
        raise MnamerException(f"state path is a directory: {options.state_path}")
    pid = _running_pid(state_path)
    if pid:
        print(f"already running (pid {pid})")
        return 0
    state = _init_state(_read_state(state_path))
    state["pid"] = None
    state["started_epoch"] = int(time.time())
    _write_state(state_path, state)
    package_root = str(Path(__file__).resolve().parent.parent)
    env = dict(os.environ)
    env["PYTHONPATH"] = os.pathsep.join(
        p for p in (package_root, env.get("PYTHONPATH")) if p
    )
    process = subprocess.Popen(
        [sys.executable, "-m", "mnamer.daemon", options.to_json()],
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
        env=env,
    )
    state["pid"] = process.pid
    _write_state(state_path, state)
    _append_log(state_path, f"daemon started (pid {process.pid})")
    print(f"daemon started (pid {process.pid})")
    return 0


def _stop(settings: SettingStore, quiet: bool = False) -> int:
    state_path = _state_path(settings)
    pid = _running_pid(state_path)
    if not pid:
        if not quiet:
            print("not running")
        return 0
    try:
        os.kill(pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    deadline = time.time() + 5
    while _pid_alive(pid) and time.time() < deadline:
        time.sleep(0.05)
    if _pid_alive(pid):
        try:
            os.kill(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
    state = _read_state(state_path)
    if state:
        state["pid"] = None
        _write_state(state_path, state)
        _append_log(state_path, f"daemon stopped (pid {pid})")
    if not quiet:
        print("stopped")
    return 0


def _logs(settings: SettingStore) -> int:
    state_path = _state_path(settings)
    log_path = _log_path(state_path)
    lines: list[str] = []
    if not state_path.is_dir() and log_path.is_file():
        try:
            lines = log_path.read_text().splitlines()
        except (OSError, UnicodeDecodeError):
            lines = []
    if not lines:
        print("no logs available")
        return 0
    count = _int_option(settings.lines, "--lines", None)
    if count is not None:
        lines = lines[-count:] if count else []
    for line in lines:
        print(line)
    return 0


def _stats(settings: SettingStore) -> int:
    state_path = _state_path(settings)
    state = _read_state(state_path) if state_path.is_file() else {}
    processed = state.get("processed")
    epoch = state.get("updated_epoch")
    print(f"processed={len(processed) if isinstance(processed, list) else 0}")
    print(f"last_epoch={int(epoch) if isinstance(epoch, int | float) else 0}")
    return 0


# background loop --------------------------------------------------------------


def serve(options: Options) -> None:  # pragma: no cover
    running = True

    def _terminate(*_):
        nonlocal running
        running = False

    signal.signal(signal.SIGTERM, _terminate)
    signal.signal(signal.SIGINT, _terminate)
    state_path = Path(options.state_path)
    pid = os.getpid()
    while running:
        state = _read_state(state_path)
        if state.get("pid") not in (None, pid):
            break
        try:
            run_cycle(options, owner_pid=pid)
        except Exception as e:  # noqa: BLE001 - keep the daemon alive
            try:
                _append_log(state_path, f"cycle error: {e}")
            except OSError:
                pass
        deadline = time.time() + LOOP_INTERVAL_SECONDS
        while running and time.time() < deadline:
            time.sleep(0.1)


if __name__ == "__main__":  # pragma: no cover
    serve(Options.from_json(sys.argv[1]))
