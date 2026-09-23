"""Watch-directory daemon that moves stable files without renaming them."""

from __future__ import annotations

import fcntl
import fnmatch
import json
import os
import shutil
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from pathlib import Path

from mnamer.setting_store import SettingStore

NO_LOGS = "no logs available"
_STOP = False


@dataclass
class Watch:
    path: Path
    movie_directory: Path | None
    exclude: list[str]


class DaemonConfigError(Exception):
    """Raised when a daemon config file is missing or malformed."""


def daemon_requested(settings: SettingStore) -> bool:
    return bool(
        settings.daemon or settings.daemon_run_once or settings.validate_daemon_config
    )


def dispatch(settings: SettingStore) -> None:
    try:
        _dispatch(settings)
    except DaemonConfigError as exc:
        print(str(exc))
        raise SystemExit(2) from None


def _dispatch(settings: SettingStore) -> None:
    if settings.validate_daemon_config:
        validate_config(settings)
        return
    if settings.daemon_run_once:
        run_once(settings)
        return
    command = settings.daemon
    if command == "start":
        start(settings)
    elif command == "stop":
        stop(settings)
    elif command == "status":
        status(settings)
    elif command == "logs":
        logs(settings)
    elif command == "stats":
        stats(settings)
    elif command == "restart":
        restart(settings)
    else:
        print("invalid daemon command")
        raise SystemExit(2)


def validate_config(settings: SettingStore) -> None:
    if not settings.daemon_config:
        print("missing daemon config path")
        raise SystemExit(2)
    try:
        parse_config_file(Path(settings.daemon_config).expanduser())
    except DaemonConfigError as exc:
        print(str(exc))
        raise SystemExit(2) from None


def start(settings: SettingStore) -> None:
    state_path = state_path_for(settings)
    if state_path.is_dir():
        print("invalid daemon state path")
        raise SystemExit(2)
    watches = collect_watches(settings)
    if not watches:
        print("no watch configured")
        raise SystemExit(2)
    if is_running(state_path):
        return
    ensure_state(state_path)
    spawn_worker(state_path)


def stop(settings: SettingStore) -> None:
    state_path = state_path_for(settings)
    if not state_path.is_file():
        return
    try:
        data = load_state(state_path)
    except OSError:
        return
    pid = data.get("pid")
    if isinstance(pid, int) and pid > 0 and pid_alive(pid):
        try:
            os.kill(pid, signal.SIGTERM)
        except OSError:
            pass
        deadline = time.time() + 5
        while time.time() < deadline and pid_alive(pid):
            time.sleep(0.05)
        if pid_alive(pid):
            try:
                os.kill(pid, signal.SIGKILL)
            except OSError:
                pass
            deadline = time.time() + 1
            while time.time() < deadline and pid_alive(pid):
                time.sleep(0.05)
    try:
        locked_update(state_path, lambda data: data.__setitem__("pid", None))
    except OSError:
        return


def restart(settings: SettingStore) -> None:
    state_path = state_path_for(settings)
    if is_running(state_path):
        stop(settings)
    start(settings)


def status(settings: SettingStore) -> None:
    if is_running(state_path_for(settings)):
        print("running")
    else:
        print("not running")


def stats(settings: SettingStore) -> None:
    count, epoch = read_stats(state_path_for(settings))
    print(f"processed={count}, last_epoch={epoch}")


def logs(settings: SettingStore) -> None:
    state_path = state_path_for(settings)
    if state_path.is_dir():
        print(NO_LOGS)
        return
    log_path = log_path_for(state_path)
    if not log_path.is_file():
        print(NO_LOGS)
        return
    try:
        text = log_path.read_text(encoding="utf-8")
    except OSError:
        print(NO_LOGS)
        return
    if not text.strip():
        print(NO_LOGS)
        return
    lines = text.splitlines()
    limit = settings.lines
    if limit is not None:
        if limit <= 0:
            return
        lines = lines[-limit:]
    sys.stdout.write("\n".join(lines) + ("\n" if lines else ""))


def run_once(settings: SettingStore) -> None:
    execute_cycle(settings, dry_run=settings.dry_run)


def worker_entry() -> None:
    signal.signal(signal.SIGHUP, signal.SIG_IGN)
    signal.signal(signal.SIGTERM, _request_stop)
    signal.signal(signal.SIGINT, _request_stop)
    settings = SettingStore()
    settings.load()
    state_path = state_path_for(settings)
    try:
        locked_update(state_path, lambda data: data.__setitem__("pid", os.getpid()))
    except OSError:
        pass
    while not _STOP:
        try:
            execute_cycle(settings, dry_run=False)
        except SystemExit:
            raise
        except Exception:
            try:
                append_log(state_path, "cycle error")
            except OSError:
                pass
        for _ in range(4):
            if _STOP:
                break
            time.sleep(0.05)


def daemonize() -> None:
    """Detach, record the child pid, and exec the worker. Used by ``start``."""
    state_raw = os.environ.get("MNAMER_DAEMON_STATE", "")
    state_path = Path(state_raw) if state_raw else None
    signal.signal(signal.SIGHUP, signal.SIG_IGN)
    pid = os.fork()
    if pid > 0:
        code = 0
        try:
            if state_path is not None:
                locked_update(state_path, lambda data: data.__setitem__("pid", pid))
        except Exception:
            code = 1
        os._exit(code)
    try:
        os.setsid()
    except OSError:
        pass
    os.environ["MNAMER_DAEMON_WORKER"] = "1"
    args = [sys.executable, "-m", "mnamer", *sys.argv[1:]]
    os.execv(sys.executable, args)


def state_path_for(settings: SettingStore) -> Path:
    raw = settings.daemon_state or "daemon-state.json"
    path = Path(raw).expanduser()
    if not path.is_absolute():
        path = Path.cwd() / path
    return path


def log_path_for(state_path: Path) -> Path:
    return Path(str(state_path) + ".log")


def collect_watches(settings: SettingStore) -> list[Watch]:
    watches: list[Watch] = []
    if settings.daemon_config:
        watches.extend(parse_config_file(Path(settings.daemon_config).expanduser()))
    movie = settings.movie_directory
    movie_path = Path(movie) if movie else None
    raw_paths: list[object] = []
    if settings.watch:
        raw_paths.extend(settings.watch)
    if settings.targets:
        raw_paths.extend(settings.targets)
    for raw in raw_paths:
        text = str(raw).strip()
        if not text:
            continue
        watches.append(Watch(Path(text), movie_path, []))
    return watches


def parse_config_file(path: Path) -> list[Watch]:
    if not path.is_file():
        raise DaemonConfigError(f"daemon config not found: {path}")
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        raise DaemonConfigError("invalid daemon config structure: not JSON") from exc
    except OSError as exc:
        raise DaemonConfigError("invalid daemon config structure: unreadable") from exc
    if not isinstance(data, dict) or "watch" not in data:
        raise DaemonConfigError(
            "invalid daemon config structure: expected an object with watch"
        )
    entries = data["watch"]
    if not isinstance(entries, list):
        raise DaemonConfigError(
            "invalid daemon config structure: watch must be an array"
        )
    watches: list[Watch] = []
    for index, entry in enumerate(entries):
        if not isinstance(entry, dict):
            raise DaemonConfigError(
                f"invalid daemon config structure: watch entry {index} is not an object"
            )
        watch_path = entry.get("path", None)
        movie_directory = entry.get("movie_directory", None)
        if "path" not in entry or not isinstance(watch_path, str):
            raise DaemonConfigError(
                "invalid daemon config structure: path must be a string"
            )
        if "movie_directory" not in entry or not isinstance(movie_directory, str):
            raise DaemonConfigError(
                "invalid daemon config structure: movie_directory must be a string"
            )
        exclude: list[str] = []
        if "exclude" in entry:
            raw_exclude = entry["exclude"]
            if not isinstance(raw_exclude, list) or not all(
                isinstance(item, str) for item in raw_exclude
            ):
                raise DaemonConfigError(
                    "invalid daemon config structure: exclude must be an array of strings"
                )
            exclude = list(raw_exclude)
        watches.append(Watch(Path(watch_path), Path(movie_directory), exclude))
    return watches


def plan_moves(
    settings: SettingStore, watches: list[Watch], state_path: Path
) -> list[tuple[Path, Path]]:
    if settings.batch_size == 0:
        return []
    planned: list[tuple[Path, Path]] = []
    seen: set[str] = set()
    reserved: set[Path] = set()
    protected = _protected_paths(state_path)
    for watch in watches:
        for src in _top_level_files(watch.path):
            try:
                key = str(src.resolve())
            except OSError:
                key = str(src)
            if key in seen or key in protected:
                continue
            seen.add(key)
            if src.name.lower().endswith(".part"):
                continue
            if _excluded(src.name, watch.exclude):
                continue
            if watch.movie_directory is None:
                continue
            if not _is_stable(
                src, settings.stability_interval_ms, settings.stability_checks
            ):
                continue
            dest = _allocate_dest(watch.movie_directory, src.name, reserved)
            try:
                if src.resolve() == dest.resolve():
                    continue
            except OSError:
                pass
            planned.append((src, dest))
            if settings.batch_size is not None and len(planned) >= settings.batch_size:
                return planned
    return planned


def execute_cycle(settings: SettingStore, *, dry_run: bool) -> None:
    state_path = state_path_for(settings)
    if state_path.is_dir():
        print("invalid daemon state path")
        raise SystemExit(2)
    watches = collect_watches(settings)
    planned = plan_moves(settings, watches, state_path)
    if dry_run:
        for src, dest in planned:
            print(f"{display_path(src)} -> {display_path(dest)}")
        return
    moved = perform_moves(planned)
    commit_cycle(state_path, moved)
    if settings.notify_webhook and moved:
        notify_webhook(settings.notify_webhook, moved)


def perform_moves(planned: list[tuple[Path, Path]]) -> list[str]:
    moved: list[str] = []
    for src, dest in planned:
        try:
            if not src.is_file():
                continue
            try:
                recorded = str(src.resolve())
            except OSError:
                recorded = str(src)
            dest.parent.mkdir(parents=True, exist_ok=True)
            if dest.exists():
                continue
            shutil.move(str(src), str(dest))
        except OSError:
            continue
        moved.append(recorded)
    return moved


def commit_cycle(state_path: Path, moved: list[str]) -> None:
    epoch_box: dict[str, int] = {}

    def mutate(data: dict) -> None:
        paths = data.get("processed_paths")
        if not isinstance(paths, list):
            paths = data.get("processed")
        if not isinstance(paths, list):
            paths = []
        paths.extend(moved)
        data["processed_paths"] = paths
        data.pop("processed", None)
        epoch = int(time.time())
        data["updated_epoch"] = epoch
        generation = data.get("generation", 0)
        if isinstance(generation, bool) or not isinstance(generation, int):
            generation = 0
        data["generation"] = generation + 1
        epoch_box["epoch"] = epoch

    locked_update(state_path, mutate)
    append_log(
        state_path,
        f"cycle processed={len(moved)} updated_epoch={epoch_box.get('epoch', 0)}",
    )


def ensure_state(state_path: Path) -> None:
    def mutate(data: dict) -> None:
        paths = data.get("processed_paths")
        if not isinstance(paths, list):
            paths = data.get("processed")
        if not isinstance(paths, list):
            paths = []
        data["processed_paths"] = paths
        data.pop("processed", None)
        epoch = data.get("updated_epoch", 0)
        if not isinstance(epoch, int) or isinstance(epoch, bool):
            data["updated_epoch"] = 0
        pid = data.get("pid")
        if not (isinstance(pid, int) and not isinstance(pid, bool) and pid_alive(pid)):
            data["pid"] = None

    locked_update(state_path, mutate)


def spawn_worker(state_path: Path) -> None:
    env = os.environ.copy()
    env["MNAMER_DAEMON_STATE"] = str(state_path)
    env.pop("MNAMER_DAEMON_WORKER", None)
    root = str(Path(__file__).resolve().parents[1])
    env["PYTHONPATH"] = os.pathsep.join(
        item for item in (root, env.get("PYTHONPATH", "")) if item
    )
    proc = subprocess.Popen(
        [
            sys.executable,
            "-c",
            "from mnamer.daemon import daemonize; daemonize()",
            *sys.argv[1:],
        ],
        env=env,
        cwd=os.getcwd(),
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
        close_fds=True,
    )
    try:
        code = proc.wait(timeout=10)
    except subprocess.TimeoutExpired:
        print("failed to start daemon")
        raise SystemExit(2) from None
    if code != 0:
        print("failed to start daemon")
        raise SystemExit(2)


def is_running(state_path: Path) -> bool:
    if not state_path.is_file():
        return False
    try:
        data = json.loads(state_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return False
    if not isinstance(data, dict):
        return False
    pid = data.get("pid")
    return (
        isinstance(pid, int)
        and not isinstance(pid, bool)
        and pid > 0
        and pid_alive(pid)
    )


def read_stats(state_path: Path) -> tuple[int, int]:
    if not state_path.is_file():
        return 0, 0
    try:
        data = json.loads(state_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return 0, 0
    if not isinstance(data, dict):
        return 0, 0
    paths = data.get("processed_paths")
    if not isinstance(paths, list):
        paths = data.get("processed")
    if not isinstance(paths, list):
        paths = []
    epoch = data.get("updated_epoch", 0)
    if isinstance(epoch, bool) or not isinstance(epoch, int):
        try:
            epoch = int(epoch)
        except (TypeError, ValueError):
            epoch = 0
    return len(paths), epoch


def load_state(path: Path) -> dict:
    if path.is_file():
        try:
            data = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            data = None
        if isinstance(data, dict):
            paths = data.get("processed_paths")
            if not isinstance(paths, list):
                paths = data.get("processed")
            if not isinstance(paths, list):
                paths = []
            data["processed_paths"] = list(paths)
            data.pop("processed", None)
            if not isinstance(data.get("updated_epoch"), int) or isinstance(
                data.get("updated_epoch"), bool
            ):
                data["updated_epoch"] = 0
            if "pid" not in data:
                data["pid"] = None
            return data
    return {"pid": None, "processed_paths": [], "updated_epoch": 0}


def save_state(path: Path, data: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    payload = json.dumps(data, indent=2, sort_keys=True) + "\n"
    tmp = Path(str(path) + ".tmp")
    tmp.write_text(payload, encoding="utf-8")
    os.replace(tmp, path)


def locked_update(path: Path, mutator) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    lock_path = Path(str(path) + ".lock")
    with lock_path.open("a+") as handle:
        fcntl.flock(handle, fcntl.LOCK_EX)
        try:
            data = load_state(path)
            mutator(data)
            save_state(path, data)
        finally:
            fcntl.flock(handle, fcntl.LOCK_UN)


def append_log(state_path: Path, line: str) -> None:
    log_path = log_path_for(state_path)
    with log_path.open("a", encoding="utf-8") as handle:
        handle.write(line.rstrip("\n") + "\n")


def notify_webhook(url: str, moved: list[str]) -> None:
    try:
        body = json.dumps({"processed": moved}).encode("utf-8")
        request = urllib.request.Request(
            url,
            data=body,
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with urllib.request.urlopen(request, timeout=3) as response:
            response.read()
    except (OSError, urllib.error.URLError, ValueError, TimeoutError):
        return


def _request_stop(signum: int, frame) -> None:  # noqa: ARG001
    global _STOP
    _STOP = True


def _top_level_files(path: Path) -> list[Path]:
    try:
        if not path.exists():
            return []
        if path.is_file():
            return [path]
        if not path.is_dir():
            return []
        children = list(path.iterdir())
    except OSError:
        return []
    files: list[Path] = []
    for child in children:
        try:
            if child.is_file():
                files.append(child)
        except OSError:
            continue
    files.sort(key=lambda item: item.name)
    return files


def _excluded(name: str, patterns: list[str]) -> bool:
    return any(fnmatch.fnmatch(name, pattern) for pattern in patterns)


def _is_stable(path: Path, interval_ms: int | None, checks: int | None) -> bool:
    count = 1 if checks is None else checks
    if count <= 1:
        return True
    interval = 0.0
    if interval_ms is not None and interval_ms > 0:
        interval = interval_ms / 1000.0
    try:
        size = path.stat().st_size
    except OSError:
        return False
    for _ in range(count - 1):
        if interval:
            time.sleep(interval)
        try:
            new_size = path.stat().st_size
        except OSError:
            return False
        if new_size != size:
            return False
        size = new_size
    return True


def _allocate_dest(directory: Path, name: str, reserved: set[Path]) -> Path:
    candidate = directory / name
    stem = Path(name).stem
    suffix = Path(name).suffix
    number = 1
    while _dest_taken(candidate, reserved):
        candidate = directory / f"{stem} ({number}){suffix}"
        number += 1
        if number > 10000:
            break
    reserved.add(candidate)
    return candidate


def _dest_taken(candidate: Path, reserved: set[Path]) -> bool:
    if candidate in reserved:
        return True
    try:
        return candidate.exists()
    except OSError:
        return True


def _protected_paths(state_path: Path) -> set[str]:
    names = [
        state_path,
        log_path_for(state_path),
        Path(str(state_path) + ".lock"),
        Path(str(state_path) + ".tmp"),
    ]
    protected: set[str] = set()
    for path in names:
        try:
            protected.add(str(path.resolve()))
        except OSError:
            protected.add(str(path))
    return protected


def display_path(path: Path) -> str:
    try:
        return str(path.resolve())
    except OSError:
        return str(path.absolute())


def pid_alive(pid: int) -> bool:
    if pid <= 0:
        return False
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    except OSError:
        return False
    else:
        return True
