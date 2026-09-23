"""Background mover that relocates stable media without renaming or network lookups."""

from __future__ import annotations

import fcntl
import json
import os
import shutil
import signal
import subprocess
import sys
import time
import urllib.request
from collections.abc import Iterator
from contextlib import contextmanager
from fnmatch import fnmatch
from pathlib import Path
from typing import Any

NO_LOGS = "no logs available"
POLL_SECONDS = 0.2


def requested(settings: Any) -> bool:
    return bool(
        getattr(settings, "daemon", None)
        or getattr(settings, "daemon_run_once", False)
        or getattr(settings, "validate_daemon_config", False)
        or getattr(settings, "dry_run", False)
    )


def handle(settings: Any) -> int:
    try:
        return _dispatch(settings)
    except SystemExit as exc:
        code = exc.code
        if code is None:
            return 0
        return code if isinstance(code, int) else 2
    except Exception as exc:
        print(f"daemon error: {exc}", file=sys.stderr)
        return 2


def _dispatch(settings: Any) -> int:
    if settings.validate_daemon_config:
        return validate_config(settings.daemon_config)
    if settings.daemon_run_once or (settings.dry_run and not settings.daemon):
        return run_once(settings)
    command = settings.daemon
    if command == "start":
        return start(settings)
    if command == "stop":
        return stop(settings)
    if command == "status":
        return status(settings)
    if command == "logs":
        return logs(settings)
    if command == "stats":
        return stats(settings)
    if command == "restart":
        stop(settings)
        return start(settings)
    print("unknown daemon command", file=sys.stderr)
    return 2


class Watch:
    def __init__(self, path: Path, movie_directory: Path, exclude: list[str]):
        self.path = path
        self.movie_directory = movie_directory
        self.exclude = exclude


def validate_config(config_path: str | None) -> int:
    _watches, error = _config_watches(config_path, required=True)
    if error:
        print(error, file=sys.stderr)
        return 2
    return 0


def _config_watches(
    config_path: str | None, *, required: bool
) -> tuple[list[Watch], str | None]:
    if not config_path:
        if required:
            return [], "missing daemon config path; --daemon-config is required"
        return [], None
    path = Path(config_path)
    if not path.is_file():
        return [], f"daemon config not found: {config_path}"
    try:
        raw = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError):
        return [], "invalid daemon config structure"
    error = _structure_error(raw)
    if error:
        return [], error
    watches = [
        Watch(
            Path(entry["path"]),
            Path(entry["movie_directory"]),
            list(entry.get("exclude") or []),
        )
        for entry in raw["watch"]
    ]
    return watches, None


def _structure_error(raw: Any) -> str | None:
    if not isinstance(raw, dict) or not isinstance(raw.get("watch"), list):
        return "invalid daemon config structure"
    for entry in raw["watch"]:
        if not isinstance(entry, dict):
            return "invalid daemon config structure"
        path = entry.get("path", None)
        movie_directory = entry.get("movie_directory", None)
        if "path" not in entry or not isinstance(path, str):
            return "invalid daemon config structure: path must be a string"
        if "movie_directory" not in entry or not isinstance(movie_directory, str):
            return "invalid daemon config structure: movie_directory must be a string"
        if "exclude" in entry:
            exclude = entry["exclude"]
            if not isinstance(exclude, list) or not all(
                isinstance(item, str) for item in exclude
            ):
                return "invalid daemon config structure: exclude must be an array of strings"
    return None


def _flatten_watch(value: Any) -> list[str]:
    if not value:
        return []
    paths: list[str] = []
    for item in value:
        if isinstance(item, (list, tuple)):
            paths.extend(str(path) for path in item)
        else:
            paths.append(str(item))
    return paths


def collect_watches(settings: Any) -> tuple[list[Watch], str | None]:
    watches, error = _config_watches(settings.daemon_config, required=False)
    if error:
        return [], error
    cli_paths = _flatten_watch(getattr(settings, "watch", None))
    cli_paths.extend(str(target) for target in (settings.targets or []))
    if cli_paths:
        movie_directory = settings.movie_directory
        if not movie_directory:
            return [], "watch paths require movie_directory"
        for raw_path in cli_paths:
            watches.append(Watch(Path(raw_path), Path(movie_directory), []))
    return watches, None


def _state_path(settings: Any) -> Path:
    raw = getattr(settings, "daemon_state", None) or "daemon-state.json"
    return Path(raw)


def _log_path(state_path: Path) -> Path:
    return Path(str(state_path) + ".log")


def _pid_path(state_path: Path) -> Path:
    return Path(str(state_path) + ".pid")


def _lock_path(state_path: Path) -> Path:
    return Path(str(state_path) + ".lock")


def _reserved(state_path: Path) -> set[Path]:
    return {
        state_path.resolve(),
        _log_path(state_path).resolve(),
        _pid_path(state_path).resolve(),
        _lock_path(state_path).resolve(),
        Path(str(state_path) + ".tmp").resolve(),
    }


@contextmanager
def _locked(state_path: Path) -> Iterator[None]:
    lock_path = _lock_path(state_path)
    lock_path.parent.mkdir(parents=True, exist_ok=True)
    with lock_path.open("a+", encoding="utf-8") as handle:
        fcntl.flock(handle.fileno(), fcntl.LOCK_EX)
        try:
            yield
        finally:
            fcntl.flock(handle.fileno(), fcntl.LOCK_UN)


def _blank_state() -> dict[str, Any]:
    return {"processed": [], "updated_epoch": 0}


def _read_state(state_path: Path) -> dict[str, Any]:
    if not state_path.is_file():
        return _blank_state()
    try:
        data = json.loads(state_path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError):
        return _blank_state()
    if not isinstance(data, dict):
        return _blank_state()
    processed = data.get("processed")
    if not isinstance(processed, list):
        processed = []
    epoch = data.get("updated_epoch", 0)
    if not isinstance(epoch, int) or isinstance(epoch, bool):
        epoch = 0
    return {"processed": [str(item) for item in processed], "updated_epoch": epoch}


def _write_state(state_path: Path, data: dict[str, Any]) -> None:
    state_path.parent.mkdir(parents=True, exist_ok=True)
    payload = {
        "processed": list(data.get("processed") or []),
        "updated_epoch": int(data.get("updated_epoch") or 0),
    }
    text = json.dumps(payload, indent=2, sort_keys=True) + "\n"
    temporary = Path(str(state_path) + ".tmp")
    temporary.write_text(text, encoding="utf-8")
    temporary.replace(state_path)


def _touch_state(state_path: Path, *, preserve: bool) -> None:
    with _locked(state_path):
        data = _read_state(state_path) if preserve else _blank_state()
        if not isinstance(data.get("processed"), list):
            data["processed"] = []
        data["updated_epoch"] = time.time_ns()
        _write_state(state_path, data)


def _alive(pid: int) -> bool:
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
    return True


def _read_pid(state_path: Path) -> int | None:
    pid_file = _pid_path(state_path)
    if not pid_file.is_file():
        return None
    try:
        return int(pid_file.read_text(encoding="utf-8").strip())
    except (OSError, ValueError):
        return None


def _write_pid(state_path: Path, pid: int) -> None:
    _pid_path(state_path).write_text(f"{pid}\n", encoding="utf-8")


def _clear_pid(state_path: Path) -> None:
    try:
        _pid_path(state_path).unlink()
    except FileNotFoundError:
        return
    except OSError:
        return


def is_running(state_path: Path) -> bool:
    if state_path.is_dir():
        return False
    pid = _read_pid(state_path)
    if pid is None:
        return False
    if _alive(pid):
        return True
    _clear_pid(state_path)
    return False


def _append_log(state_path: Path, line: str) -> None:
    log_path = _log_path(state_path)
    log_path.parent.mkdir(parents=True, exist_ok=True)
    with log_path.open("a", encoding="utf-8") as handle:
        handle.write(line.rstrip("\n") + "\n")
        handle.flush()


def _record_cycle(state_path: Path, new_paths: list[str], moved: int) -> None:
    with _locked(state_path):
        data = _read_state(state_path)
        processed = list(data["processed"])
        for path in new_paths:
            if path not in processed:
                processed.append(path)
        epoch = time.time_ns()
        data["processed"] = processed
        data["updated_epoch"] = epoch
        _write_state(state_path, data)
    _append_log(
        state_path,
        f"cycle updated_epoch={epoch} processed={len(processed)} moved={moved}",
    )


def _notify(url: str | None, moved: list[dict[str, str]]) -> None:
    if not url or not moved:
        return
    try:
        body = json.dumps({"moved": moved}).encode("utf-8")
        request = urllib.request.Request(
            url,
            data=body,
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with urllib.request.urlopen(request, timeout=3) as response:
            response.read()
    except Exception:
        return


def _excluded(name: str, patterns: list[str]) -> bool:
    return any(fnmatch(name, pattern) for pattern in patterns)


def _is_stable(path: Path, checks: int, interval_ms: int) -> bool:
    if checks <= 1:
        try:
            path.stat()
        except OSError:
            return False
        return True
    try:
        previous = path.stat().st_size
    except OSError:
        return False
    for _ in range(checks - 1):
        if interval_ms > 0:
            time.sleep(interval_ms / 1000.0)
        try:
            size = path.stat().st_size
        except OSError:
            return False
        if size != previous:
            return False
        previous = size
    return True


def _allocate_destination(
    source: Path, directory: Path, reserved_names: set[str]
) -> Path | None:
    stem = Path(source.name).stem
    suffix = Path(source.name).suffix
    number = 0
    while True:
        if number == 0:
            candidate = directory / source.name
        else:
            candidate = directory / f"{stem} ({number}){suffix}"
        number += 1
        try:
            if candidate.exists() and candidate.resolve() == source.resolve():
                return None
        except OSError:
            return None
        key = str(candidate)
        if key in reserved_names or candidate.exists():
            continue
        reserved_names.add(key)
        return candidate


def _iter_sources(watch: Watch) -> list[Path]:
    path = watch.path
    if not path.exists():
        return []
    if path.is_file():
        return [path]
    if not path.is_dir():
        return []
    children = [child for child in path.iterdir() if child.is_file()]
    children.sort(key=lambda item: item.name)
    return children


def plan_moves(
    watches: list[Watch],
    *,
    state_path: Path,
    stability_checks: int,
    stability_interval_ms: int,
    batch_size: int | None,
) -> list[tuple[Path, Path]]:
    limit = None if batch_size is None else max(batch_size, 0)
    reserved_files = _reserved(state_path)
    planned: list[tuple[Path, Path]] = []
    reserved_names: set[str] = set()
    for watch in watches:
        if limit is not None and len(planned) >= limit:
            break
        for source in _iter_sources(watch):
            if limit is not None and len(planned) >= limit:
                break
            try:
                if source.resolve() in reserved_files:
                    continue
            except OSError:
                continue
            if source.name.endswith(".part"):
                continue
            if _excluded(source.name, watch.exclude):
                continue
            if not _is_stable(source, stability_checks, stability_interval_ms):
                continue
            destination = _allocate_destination(
                source, watch.movie_directory, reserved_names
            )
            if destination is None:
                continue
            planned.append((source, destination))
    return planned


def run_cycle(
    watches: list[Watch],
    state_path: Path,
    *,
    stability_checks: int,
    stability_interval_ms: int,
    batch_size: int | None,
    notify_webhook: str | None,
    dry_run: bool,
) -> int:
    planned = plan_moves(
        watches,
        state_path=state_path,
        stability_checks=stability_checks,
        stability_interval_ms=stability_interval_ms,
        batch_size=batch_size,
    )
    if dry_run:
        for source, destination in planned:
            print(f"{source} -> {destination}", flush=True)
        return 0
    moved: list[dict[str, str]] = []
    for source, destination in planned:
        try:
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.move(str(source), str(destination))
        except OSError:
            continue
        moved.append({"src": str(source.resolve()), "dst": str(destination.resolve())})
    _record_cycle(
        state_path,
        [item["src"] for item in moved],
        len(moved),
    )
    _notify(notify_webhook, moved)
    return 0


def _cycle_kwargs(settings: Any) -> dict[str, Any]:
    checks = settings.stability_checks
    interval = settings.stability_interval_ms
    return {
        "stability_checks": 1 if checks is None else int(checks),
        "stability_interval_ms": 0 if interval is None else int(interval),
        "batch_size": settings.batch_size,
        "notify_webhook": settings.notify_webhook,
    }


def run_once(settings: Any) -> int:
    watches, error = collect_watches(settings)
    if error:
        print(error, file=sys.stderr)
        return 2
    if not watches:
        print("no watch paths configured", file=sys.stderr)
        return 2
    state_path = _state_path(settings)
    if state_path.is_dir():
        print("daemon state path is a directory", file=sys.stderr)
        return 2
    return run_cycle(
        watches,
        state_path,
        dry_run=bool(settings.dry_run),
        **_cycle_kwargs(settings),
    )


def _payload(settings: Any, watches: list[Watch], state_path: Path) -> dict[str, Any]:
    kwargs = _cycle_kwargs(settings)
    return {
        "watches": [
            {
                "path": str(watch.path),
                "movie_directory": str(watch.movie_directory),
                "exclude": list(watch.exclude),
            }
            for watch in watches
        ],
        "state_path": str(state_path),
        **kwargs,
    }


def start(settings: Any) -> int:
    state_path = _state_path(settings)
    if state_path.is_dir():
        print("daemon state path is a directory", file=sys.stderr)
        return 2
    watches, error = collect_watches(settings)
    if error:
        print(error, file=sys.stderr)
        return 2
    if not watches:
        print("no watch paths configured", file=sys.stderr)
        return 2
    _touch_state(state_path, preserve=True)
    if is_running(state_path):
        return 0
    payload = _payload(settings, watches, state_path)
    with _locked(state_path):
        if is_running(state_path):
            return 0
        env = os.environ.copy()
        root = str(Path(__file__).resolve().parent.parent)
        current_pythonpath = env.get("PYTHONPATH")
        env["PYTHONPATH"] = (
            root if not current_pythonpath else root + os.pathsep + current_pythonpath
        )
        process = subprocess.Popen(
            [sys.executable, "-m", "mnamer.daemon", "--worker", json.dumps(payload)],
            stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            start_new_session=True,
            cwd=os.getcwd(),
            close_fds=True,
            env=env,
        )
        _write_pid(state_path, process.pid)
    return 0


def stop(settings: Any) -> int:
    state_path = _state_path(settings)
    if state_path.is_dir():
        return 0
    pid = _read_pid(state_path)
    _clear_pid(state_path)
    if pid is None or pid == os.getpid() or not _alive(pid):
        return 0
    try:
        os.kill(pid, signal.SIGTERM)
    except OSError:
        return 0
    for _ in range(20):
        if not _alive(pid):
            return 0
        time.sleep(0.1)
    try:
        os.kill(pid, signal.SIGKILL)
    except OSError:
        return 0
    return 0


def status(settings: Any) -> int:
    state_path = _state_path(settings)
    if is_running(state_path):
        print("running")
    else:
        print("not running")
    return 0


def logs(settings: Any) -> int:
    state_path = _state_path(settings)
    log_path = _log_path(state_path)
    if state_path.is_dir() or not log_path.is_file():
        print(NO_LOGS)
        return 0
    try:
        text = log_path.read_text(encoding="utf-8")
    except OSError:
        print(NO_LOGS)
        return 0
    if not text.strip():
        print(NO_LOGS)
        return 0
    rows = text.splitlines()
    limit = settings.lines
    if limit is not None:
        count = int(limit)
        rows = [] if count <= 0 else rows[-count:]
    sys.stdout.write("\n".join(rows))
    if rows:
        sys.stdout.write("\n")
    return 0


def stats(settings: Any) -> int:
    state_path = _state_path(settings)
    if state_path.is_dir():
        print("processed=0, last_epoch=0")
        return 0
    data = _read_state(state_path)
    print(f"processed={len(data['processed'])}, last_epoch={data['updated_epoch']}")
    return 0


def _watches_from_payload(payload: dict[str, Any]) -> list[Watch]:
    watches: list[Watch] = []
    for entry in payload.get("watches") or []:
        watches.append(
            Watch(
                Path(entry["path"]),
                Path(entry["movie_directory"]),
                list(entry.get("exclude") or []),
            )
        )
    return watches


def _pid_matches(state_path: Path) -> bool:
    return _read_pid(state_path) == os.getpid()


def run_forever(payload: dict[str, Any]) -> None:
    state_path = Path(payload["state_path"])
    deadline = time.time() + 5
    while time.time() < deadline and not _pid_matches(state_path):
        time.sleep(0.01)
    if not _pid_matches(state_path):
        return
    watches = _watches_from_payload(payload)
    raw_checks = payload.get("stability_checks")
    checks = 1 if raw_checks is None else int(raw_checks)
    raw_interval = payload.get("stability_interval_ms")
    interval = 0 if raw_interval is None else int(raw_interval)
    batch_size = payload.get("batch_size")
    webhook = payload.get("notify_webhook")
    while _pid_matches(state_path):
        try:
            run_cycle(
                watches,
                state_path,
                stability_checks=checks,
                stability_interval_ms=interval,
                batch_size=batch_size,
                notify_webhook=webhook,
                dry_run=False,
            )
        except Exception as exc:
            try:
                _append_log(state_path, f"cycle error: {exc}")
            except Exception:
                pass
        for _ in range(4):
            if not _pid_matches(state_path):
                return
            time.sleep(POLL_SECONDS / 4)


def worker_main(argv: list[str] | None = None) -> int:
    args = list(sys.argv[1:] if argv is None else argv)
    if len(args) < 2 or args[0] != "--worker":
        print("daemon worker expects a payload", file=sys.stderr)
        return 2
    try:
        payload = json.loads(args[1])
    except json.JSONDecodeError:
        print("invalid daemon worker payload", file=sys.stderr)
        return 2
    if not isinstance(payload, dict):
        print("invalid daemon worker payload", file=sys.stderr)
        return 2
    run_forever(payload)
    return 0


if __name__ == "__main__":  # pragma: no cover
    raise SystemExit(worker_main())
