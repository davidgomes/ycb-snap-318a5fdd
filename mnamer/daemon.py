"""Watches directories and moves their top-level files into movie directories.

Unlike the interactive frontend this never queries metadata providers or prompts:
files keep their names and are simply relocated. The background daemon is a
detached `python -m mnamer.daemon` process tracked through a pid file next to
the state file.
"""

from __future__ import annotations

import dataclasses
import json
import os
import signal
import subprocess
import sys
import time
from collections.abc import Callable
from datetime import UTC, datetime
from fnmatch import fnmatch
from http.client import HTTPException
from pathlib import Path
from shutil import move
from typing import TYPE_CHECKING, Any
from urllib.request import Request, urlopen

from mnamer.exceptions import MnamerDaemonException
from mnamer.types import DaemonAction

if TYPE_CHECKING:
    from mnamer.setting_store import SettingStore

DAEMON_MODULE = "mnamer.daemon"
NO_LOGS_MESSAGE = "no logs available"
PARTIAL_SUFFIX = ".part"
POLL_INTERVAL_SECONDS = 1.0
STOP_TIMEOUT_SECONDS = 10.0
WEBHOOK_TIMEOUT_SECONDS = 5.0


@dataclasses.dataclass(frozen=True)
class WatchEntry:
    path: Path
    movie_directory: Path
    exclude: tuple[str, ...] = ()

    def excludes(self, filename: str) -> bool:
        return any(fnmatch(filename, pattern) for pattern in self.exclude)


@dataclasses.dataclass(frozen=True)
class DaemonOptions:
    state_path: Path
    watches: tuple[WatchEntry, ...] = ()
    config_path: Path | None = None
    batch_size: int | None = None
    stability_interval_ms: int = 1000
    stability_checks: int = 1
    notify_webhook: str | None = None

    def to_json(self) -> str:
        return json.dumps(dataclasses.asdict(self), default=str)

    @classmethod
    def from_json(cls, payload: str) -> DaemonOptions:
        data = json.loads(payload)
        watches = tuple(
            WatchEntry(
                Path(watch["path"]),
                Path(watch["movie_directory"]),
                tuple(watch["exclude"]),
            )
            for watch in data.pop("watches")
        )
        config_path = data.pop("config_path")
        return cls(
            state_path=Path(data.pop("state_path")),
            watches=watches,
            config_path=Path(config_path) if config_path else None,
            **data,
        )


@dataclasses.dataclass
class CycleReport:
    moved: list[tuple[Path, Path]] = dataclasses.field(default_factory=list)
    failed: list[tuple[Path, str]] = dataclasses.field(default_factory=list)


def log_path_for(state_path: Path) -> Path:
    return Path(f"{state_path}.log")


def pid_path_for(state_path: Path) -> Path:
    return Path(f"{state_path}.pid")


def load_watch_config(config_path: Path) -> list[WatchEntry]:
    """Parses and validates a daemon config file into watch entries."""
    if not config_path.is_file():
        raise MnamerDaemonException(f"daemon config not found: {config_path}")
    try:
        data = json.loads(config_path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as e:
        raise MnamerDaemonException(
            f"invalid daemon config structure: unreadable JSON ({e})"
        ) from e
    watch = data.get("watch") if isinstance(data, dict) else None
    if not isinstance(watch, list):
        raise MnamerDaemonException(
            "invalid daemon config structure: expected an object with a 'watch' array"
        )
    entries = []
    for index, item in enumerate(watch):
        location = f"watch[{index}]"
        if not isinstance(item, dict):
            raise MnamerDaemonException(
                f"invalid daemon config structure: {location} must be an object"
            )
        for key in ("path", "movie_directory"):
            value = item.get(key)
            if not isinstance(value, str) or not value.strip():
                raise MnamerDaemonException(
                    f"invalid daemon config structure: {location}.{key} must be a non-empty string"
                )
        exclude = item.get("exclude", [])
        if not isinstance(exclude, list) or not all(
            isinstance(pattern, str) for pattern in exclude
        ):
            raise MnamerDaemonException(
                f"invalid daemon config structure: {location}.exclude must be an array of strings"
            )
        entries.append(
            WatchEntry(
                _absolute(item["path"]),
                _absolute(item["movie_directory"]),
                tuple(exclude),
            )
        )
    return entries


def run_cycle(
    options: DaemonOptions,
    *,
    dry_run: bool = False,
    label: str = "run-once",
    record_idle: bool = True,
    should_stop: Callable[[], bool] = lambda: False,
) -> CycleReport:
    """
    Moves (or with dry_run, plans moving) stable top-level files from every watch
    directory, then records the cycle in the state and log files.
    """
    report = CycleReport()
    candidates = _candidates(options) if options.batch_size != 0 else []
    candidates = _stable_candidates(options, candidates, should_stop)
    if options.batch_size:
        candidates = candidates[: options.batch_size]
    reserved: set[Path] = set()
    for source, entry in candidates:
        if should_stop():
            break
        destination = _free_destination(entry.movie_directory / source.name, reserved)
        reserved.add(destination)
        if dry_run:
            report.moved.append((source, destination))
            continue
        try:
            destination.parent.mkdir(parents=True, exist_ok=True)
            move(source, destination)
        except OSError as e:
            report.failed.append((source, e.strerror or str(e)))
        else:
            report.moved.append((source, destination))
    if not dry_run and (record_idle or report.moved or report.failed):
        _record_cycle(options, report, label)
    return report


def serve(options: DaemonOptions) -> None:
    """Runs daemon cycles until SIGTERM or SIGINT is received."""
    stop_requested = False

    def request_stop(*_: Any) -> None:
        nonlocal stop_requested
        stop_requested = True

    def should_stop() -> bool:
        return stop_requested

    signal.signal(signal.SIGTERM, request_stop)
    signal.signal(signal.SIGINT, request_stop)
    _append_log(options.state_path, f"daemon started (pid {os.getpid()})")
    while not stop_requested:
        try:
            run_cycle(
                options, label="daemon", record_idle=False, should_stop=should_stop
            )
        except Exception as e:  # a failed cycle must not take the daemon down
            _append_log(options.state_path, f"daemon cycle failed: {e}")
        _sleep(POLL_INTERVAL_SECONDS, should_stop)
    _append_log(options.state_path, "daemon stopped")


def is_requested(settings: SettingStore) -> bool:
    return bool(
        settings.daemon or settings.daemon_run_once or settings.validate_daemon_config
    )


def run(settings: SettingStore) -> int:
    """Executes the requested daemon directive and returns its exit code."""
    try:
        if settings.validate_daemon_config:
            return _validate(settings)
        if settings.daemon:
            return _manage(settings.daemon, settings)
        return _run_once(_options(settings), settings.dry_run)
    except MnamerDaemonException as e:
        print(e)
        return 2


# directive handlers -----------------------------------------------------------


def _validate(settings: SettingStore) -> int:
    if not settings.daemon_config:
        raise MnamerDaemonException("--validate-daemon-config requires --daemon-config")
    entries = load_watch_config(_absolute(settings.daemon_config))
    print(f"daemon config is valid ({len(entries)} watch entries)")
    return 0


def _manage(action: DaemonAction, settings: SettingStore) -> int:
    state_path = _absolute(settings.daemon_state)
    if action is DaemonAction.STATUS:
        print("running" if _running_pid(state_path) else "not running")
    elif action is DaemonAction.STOP:
        print("daemon stopped" if _stop(state_path) else "daemon not running")
    elif action is DaemonAction.LOGS:
        _print_logs(state_path, settings.lines)
    elif action is DaemonAction.STATS:
        processed, last_epoch = _stats(state_path)
        print(f"processed={processed}, last_epoch={last_epoch}")
    else:
        options = _options(settings)
        if not options.watches:
            raise MnamerDaemonException(
                "no watch paths: use --watch, positional paths, or --daemon-config"
            )
        if action is DaemonAction.RESTART and _stop(state_path):
            print("daemon stopped")
        return _start(options)
    return 0


def _run_once(options: DaemonOptions, dry_run: bool) -> int:
    report = run_cycle(options, dry_run=dry_run)
    for source, destination in report.moved:
        print(f"{source} -> {destination}")
    for source, error in report.failed:
        print(f"failed to move {source}: {error}")
    return 0


def _options(settings: SettingStore) -> DaemonOptions:
    for name in ("batch_size", "stability_interval_ms", "stability_checks"):
        value = getattr(settings, name)
        if value is not None and value < 0:
            raise MnamerDaemonException(
                f"--{name.replace('_', '-')} must not be negative"
            )
    state_path = _absolute(settings.daemon_state)
    if state_path.is_dir():
        raise MnamerDaemonException(f"daemon state path is a directory: {state_path}")
    watches: list[WatchEntry] = []
    config_path = None
    if settings.daemon_config:
        config_path = _absolute(settings.daemon_config)
        watches += load_watch_config(config_path)
    cli_paths: list[str | Path] = [*settings.watch, *settings.targets]
    if cli_paths:
        if not settings.movie_directory:
            raise MnamerDaemonException(
                "--movie-directory is required for --watch and positional paths"
            )
        movie_directory = _absolute(settings.movie_directory)
        watches += [WatchEntry(_absolute(path), movie_directory) for path in cli_paths]
    return DaemonOptions(
        state_path=state_path,
        watches=tuple(watches),
        config_path=config_path,
        batch_size=settings.batch_size,
        stability_interval_ms=settings.stability_interval_ms,
        stability_checks=settings.stability_checks,
        notify_webhook=settings.notify_webhook,
    )


# scanning and moving ----------------------------------------------------------


def _candidates(options: DaemonOptions) -> list[tuple[Path, WatchEntry]]:
    state_path = _canonical(options.state_path)
    config_path = _canonical(options.config_path) if options.config_path else None

    def is_daemon_file(path: Path) -> bool:
        if path == config_path:
            return True
        return path.parent == state_path.parent and (
            path.name == state_path.name or path.name.startswith(f"{state_path.name}.")
        )

    found: list[tuple[Path, WatchEntry]] = []
    seen: set[Path] = set()
    for entry in options.watches:
        try:
            if not entry.path.is_dir():
                continue
            children = sorted(entry.path.iterdir())
            watch_directory = entry.path.resolve()
        except OSError:
            continue
        if watch_directory == entry.movie_directory.resolve():
            continue
        for child in children:
            if child.name.lower().endswith(PARTIAL_SUFFIX) or entry.excludes(
                child.name
            ):
                continue
            source = watch_directory / child.name
            if source in seen or is_daemon_file(source) or not child.is_file():
                continue
            seen.add(source)
            found.append((child, entry))
    return found


def _stable_candidates(
    options: DaemonOptions,
    candidates: list[tuple[Path, WatchEntry]],
    should_stop: Callable[[], bool],
) -> list[tuple[Path, WatchEntry]]:
    if not options.stability_checks or not candidates:
        return candidates
    initial = {source: _size(source) for source, _ in candidates}
    stable = {source for source, size in initial.items() if size is not None}
    for _ in range(options.stability_checks):
        if _sleep(options.stability_interval_ms / 1000, should_stop):
            return []
        stable = {source for source in stable if _size(source) == initial[source]}
    return [(source, entry) for source, entry in candidates if source in stable]


def _free_destination(destination: Path, reserved: set[Path]) -> Path:
    candidate = destination
    index = 1
    while candidate in reserved or os.path.lexists(candidate):
        candidate = destination.with_name(
            f"{destination.stem} ({index}){destination.suffix}"
        )
        index += 1
    return candidate


def _size(path: Path) -> int | None:
    try:
        return path.stat().st_size
    except OSError:
        return None


def _sleep(seconds: float, should_stop: Callable[[], bool]) -> bool:
    """Sleeps for up to seconds; returns True if interrupted by a stop request."""
    deadline = time.monotonic() + seconds
    while not should_stop():
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            return False
        time.sleep(min(remaining, 0.1))
    return True


# state, logs, and notifications -----------------------------------------------


def _read_state(state_path: Path) -> dict[str, Any]:
    try:
        state = json.loads(state_path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return {}
    return state if isinstance(state, dict) else {}


def _write_state(state_path: Path, state: dict[str, Any]) -> None:
    state_path.parent.mkdir(parents=True, exist_ok=True)
    temporary_path = Path(f"{state_path}.{os.getpid()}.tmp")
    temporary_path.write_text(json.dumps(state, indent=4, sort_keys=True), "utf-8")
    os.replace(temporary_path, state_path)


def _initialize_state(state_path: Path) -> None:
    state = _read_state(state_path)
    state.setdefault("processed", [])
    state.setdefault("updated_epoch", int(time.time()))
    state.setdefault("cycles", 0)
    _write_state(state_path, state)


def _record_cycle(options: DaemonOptions, report: CycleReport, label: str) -> None:
    state = _read_state(options.state_path)
    processed = state.get("processed")
    if not isinstance(processed, list):
        processed = []
    processed += [str(source) for source, _ in report.moved]
    state["processed"] = processed
    state["updated_epoch"] = int(time.time())
    state["cycles"] = _int(state.get("cycles")) + 1
    _write_state(options.state_path, state)
    webhook_error = None
    if options.notify_webhook and report.moved:
        webhook_error = _notify(options.notify_webhook, report.moved)
    summary = [f"{label}: moved={len(report.moved)} failed={len(report.failed)}"]
    summary += [f"{source} -> {destination}" for source, destination in report.moved]
    summary += [f"failed {source}: {error}" for source, error in report.failed]
    if webhook_error:
        summary.append(f"webhook failed: {webhook_error}")
    _append_log(options.state_path, "; ".join(summary))


def _stats(state_path: Path) -> tuple[int, int]:
    state = _read_state(state_path)
    processed = state.get("processed")
    return (
        len(processed) if isinstance(processed, list) else 0,
        _int(state.get("updated_epoch")),
    )


def _append_log(state_path: Path, message: str) -> None:
    log_path = log_path_for(state_path)
    log_path.parent.mkdir(parents=True, exist_ok=True)
    timestamp = datetime.now(UTC).strftime("%Y-%m-%dT%H:%M:%SZ")
    message = message.replace("\r", "\\r").replace("\n", "\\n")
    with log_path.open("a", encoding="utf-8") as fp:
        fp.write(f"{timestamp} {message}\n")


def _print_logs(state_path: Path, lines: int | None) -> None:
    if lines is not None and lines < 0:
        raise MnamerDaemonException("--lines must not be negative")
    content = ""
    if not state_path.is_dir():
        try:
            content = log_path_for(state_path).read_text("utf-8", errors="replace")
        except OSError:
            pass
    if not content.strip():
        print(NO_LOGS_MESSAGE)
        return
    entries = content.splitlines()
    if lines is not None:
        entries = entries[-lines:] if lines else []
    if entries:
        print("\n".join(entries))


def _notify(url: str, moved: list[tuple[Path, Path]]) -> str | None:
    """POSTs moved files to a webhook; returns an error description on failure."""
    if not url.startswith(("http://", "https://")):
        return f"unsupported webhook url: {url}"
    payload = {
        "event": "files_moved",
        "files": [
            {"source": str(source), "destination": str(destination)}
            for source, destination in moved
        ],
    }
    request = Request(
        url,
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urlopen(request, timeout=WEBHOOK_TIMEOUT_SECONDS):
            pass
    except (OSError, ValueError, HTTPException) as e:
        return str(e)
    return None


# process management -----------------------------------------------------------


def _start(options: DaemonOptions) -> int:
    running_pid = _running_pid(options.state_path)
    if running_pid:
        print(f"daemon already running (pid {running_pid})")
        return 0
    try:
        _initialize_state(options.state_path)
    except OSError as e:
        raise MnamerDaemonException(f"unable to initialize daemon state: {e}") from e
    environment = dict(os.environ)
    package_root = str(Path(__file__).resolve().parent.parent)
    environment["PYTHONPATH"] = os.pathsep.join(
        filter(None, (package_root, environment.get("PYTHONPATH")))
    )
    detach: dict[str, Any] = {}
    if sys.platform == "win32":
        detach["creationflags"] = (
            subprocess.DETACHED_PROCESS | subprocess.CREATE_NEW_PROCESS_GROUP
        )
    else:
        detach["start_new_session"] = True
    process = subprocess.Popen(
        [sys.executable, "-m", DAEMON_MODULE, options.to_json()],
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        env=environment,
        **detach,
    )
    pid_path_for(options.state_path).write_text(str(process.pid), "utf-8")
    print(f"daemon started (pid {process.pid})")
    return 0


def _stop(state_path: Path) -> bool:
    """Stops the daemon owning state_path; returns False if it wasn't running."""
    pid = _running_pid(state_path)
    if not pid:
        if not state_path.is_dir():
            pid_path_for(state_path).unlink(missing_ok=True)
        return False
    try:
        os.kill(pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    if not _wait_for_exit(pid, STOP_TIMEOUT_SECONDS) and sys.platform != "win32":
        try:
            os.kill(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        _wait_for_exit(pid, 1.0)
    pid_path_for(state_path).unlink(missing_ok=True)
    return True


def _running_pid(state_path: Path) -> int | None:
    if state_path.is_dir():
        return None
    try:
        pid = int(pid_path_for(state_path).read_text("utf-8").strip())
    except (OSError, ValueError):
        return None
    return pid if pid > 0 and _is_daemon_process(pid) else None


def _wait_for_exit(pid: int, timeout: float) -> bool:
    deadline = time.monotonic() + timeout
    while _is_daemon_process(pid):
        if time.monotonic() >= deadline:
            return False
        time.sleep(0.05)
    return True


def _is_daemon_process(pid: int) -> bool:
    if sys.platform == "win32":
        import ctypes

        process_query_limited_information = 0x1000
        still_active = 259
        kernel32 = ctypes.windll.kernel32
        handle = kernel32.OpenProcess(process_query_limited_information, False, pid)
        if not handle:
            return False
        exit_code = ctypes.c_ulong()
        kernel32.GetExitCodeProcess(handle, ctypes.byref(exit_code))
        kernel32.CloseHandle(handle)
        return exit_code.value == still_active
    try:
        if os.waitpid(pid, os.WNOHANG)[0] == pid:
            return False  # reaped our own exited child
    except ChildProcessError:
        pass
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        pass
    if not Path("/proc/self").exists():
        return True
    # guards against zombies and pids reused by unrelated processes
    try:
        arguments = Path("/proc", str(pid), "cmdline").read_bytes().split(b"\0")
    except OSError:
        return False
    return DAEMON_MODULE.encode() in arguments


def _absolute(path: str | Path) -> Path:
    return Path(path).expanduser().absolute()


def _canonical(path: Path) -> Path:
    return path.parent.resolve() / path.name


def _int(value: Any) -> int:
    return value if isinstance(value, int) and not isinstance(value, bool) else 0


if __name__ == "__main__":
    serve(DaemonOptions.from_json(sys.argv[-1]))
