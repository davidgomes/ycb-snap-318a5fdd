"""Watch directories and move finished files into a movie directory.

Scanning is top-level only. File names are preserved, metadata providers are
not queried, and the user is never prompted.
"""

from __future__ import annotations

import fnmatch
import json
import os
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

from mnamer.setting_store import SettingStore

_WORKER_ENV = "MNAMER_DAEMON_WORKER"
_POLL_SECONDS = 0.2
_STOP = False


class DaemonConfigError(Exception):
    """Raised when a daemon config file or watch setup cannot be used."""


def daemon_requested(settings: SettingStore) -> bool:
    """Return True when this process should run daemon behavior."""
    return bool(
        os.environ.get(_WORKER_ENV) == "1"
        or settings.validate_daemon_config
        or settings.daemon_run_once
        or settings.daemon
    )


def dispatch(settings: SettingStore) -> None:
    """Run the daemon command selected by CLI flags."""
    if os.environ.get(_WORKER_ENV) == "1":
        _run_worker(settings)
        return
    if settings.validate_daemon_config:
        _validate_command(settings)
        return
    if settings.daemon_run_once:
        _run_once(settings)
        return
    command = settings.daemon
    if command == "start":
        _start(settings)
    elif command == "stop":
        _stop(settings)
    elif command == "status":
        _status(settings)
    elif command == "logs":
        _logs(settings)
    elif command == "stats":
        _stats(settings)
    elif command == "restart":
        _restart(settings)
    else:
        _fail("error: unknown daemon command")


def _fail(message: str) -> None:
    print(message, flush=True)
    raise SystemExit(2)


def _state_path(settings: SettingStore) -> Path:
    raw = settings.daemon_state or "daemon-state.json"
    return Path(raw).expanduser()


def _log_path(state_path: Path) -> Path:
    return Path(str(state_path) + ".log")


def _on_signal(signum: int, _frame: object) -> None:
    del signum
    global _STOP
    _STOP = True


def _sleep_seconds(seconds: float) -> None:
    remaining = seconds
    while remaining > 0 and not _STOP:
        step = min(0.05, remaining)
        time.sleep(step)
        remaining -= step


def load_daemon_config(path: str) -> list[dict[str, Any]]:
    """Load and structurally validate a daemon watch config file."""
    config_path = Path(path).expanduser()
    if not config_path.is_file():
        raise DaemonConfigError(f"error: daemon config not found: {config_path}")
    try:
        text = config_path.read_text(encoding="utf-8")
    except OSError as exc:
        raise DaemonConfigError(
            f"error: daemon config not found: {config_path}"
        ) from exc
    try:
        data = json.loads(text)
    except json.JSONDecodeError as exc:
        raise DaemonConfigError(
            "error: invalid daemon config structure: expected JSON object"
        ) from exc
    if not isinstance(data, dict) or "watch" not in data:
        raise DaemonConfigError(
            "error: invalid daemon config structure: expected object with watch array"
        )
    watch = data["watch"]
    if not isinstance(watch, list):
        raise DaemonConfigError(
            "error: invalid daemon config structure: watch must be an array"
        )
    entries: list[dict[str, Any]] = []
    for index, entry in enumerate(watch):
        if not isinstance(entry, dict):
            raise DaemonConfigError(
                "error: invalid daemon config structure: "
                f"watch[{index}] must be an object"
            )
        if "path" not in entry or not isinstance(entry.get("path"), str):
            raise DaemonConfigError(
                "error: invalid daemon config structure: "
                f"watch[{index}].path must be a string"
            )
        if "movie_directory" not in entry or not isinstance(
            entry.get("movie_directory"), str
        ):
            raise DaemonConfigError(
                "error: invalid daemon config structure: "
                f"watch[{index}].movie_directory must be a string"
            )
        exclude: list[str] = []
        if "exclude" in entry:
            raw_exclude = entry["exclude"]
            if not isinstance(raw_exclude, list) or not all(
                isinstance(item, str) for item in raw_exclude
            ):
                raise DaemonConfigError(
                    "error: invalid daemon config structure: "
                    f"watch[{index}].exclude must be an array of strings"
                )
            exclude = list(raw_exclude)
        entries.append(
            {
                "exclude": exclude,
                "movie_directory": entry["movie_directory"],
                "path": entry["path"],
            }
        )
    return entries


class _Watch:
    def __init__(self, path: Path, movie_directory: Path, exclude: tuple[str, ...]):
        self.path = path
        self.movie_directory = movie_directory
        self.exclude = exclude


def collect_watches(settings: SettingStore) -> list[_Watch]:
    """Combine daemon-config entries with --watch and positional paths."""
    watches: list[_Watch] = []
    if settings.daemon_config:
        for entry in load_daemon_config(settings.daemon_config):
            watches.append(
                _Watch(
                    Path(entry["path"]).expanduser(),
                    Path(entry["movie_directory"]).expanduser(),
                    tuple(entry["exclude"]),
                )
            )
    cli_paths = [str(path) for path in (settings.watch or [])]
    cli_paths.extend(str(path) for path in (settings.targets or []))
    if cli_paths:
        if not settings.movie_directory:
            raise DaemonConfigError(
                "error: movie directory is required for watch paths"
            )
        movie_directory = Path(settings.movie_directory)
        for raw_path in cli_paths:
            watches.append(_Watch(Path(raw_path).expanduser(), movie_directory, ()))
    return _dedupe_watches(watches)


def _dedupe_watches(watches: list[_Watch]) -> list[_Watch]:
    seen: set[str] = set()
    unique: list[_Watch] = []
    for watch in watches:
        try:
            key = str(watch.path.resolve())
        except OSError:
            key = str(watch.path)
        if key in seen:
            continue
        seen.add(key)
        unique.append(watch)
    return unique


def _watches_for_run(settings: SettingStore, *, require: bool) -> list[_Watch]:
    try:
        watches = collect_watches(settings)
    except DaemonConfigError as exc:
        _fail(str(exc))
    if require and not watches:
        _fail("error: no watch configured")
    return watches


def _validate_command(settings: SettingStore) -> None:
    if not settings.daemon_config:
        _fail("error: --validate-daemon-config requires --daemon-config")
    try:
        load_daemon_config(settings.daemon_config)
    except DaemonConfigError as exc:
        _fail(str(exc))


def _coerce_pid(value: object) -> int | None:
    if isinstance(value, bool) or not isinstance(value, int):
        return None
    if value <= 0:
        return None
    return value


def _pid_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    except OSError:
        return False
    try:
        waited, _status = os.waitpid(pid, os.WNOHANG)
    except ChildProcessError:
        return True
    except OSError:
        return True
    return waited != pid


def _read_state(path: Path) -> dict[str, Any] | None:
    if not path.is_file():
        return None
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError, UnicodeError):
        return None
    if not isinstance(data, dict):
        return None
    return data


def _bump_epoch(data: dict[str, Any]) -> None:
    now = int(time.time())
    prev = data.get("updated_epoch")
    if not isinstance(prev, int) or isinstance(prev, bool):
        prev = 0
    data["updated_epoch"] = now if now > prev else prev + 1
    cycle = data.get("cycle")
    if not isinstance(cycle, int) or isinstance(cycle, bool):
        cycle = 0
    data["cycle"] = cycle + 1


def _write_state(path: Path, data: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    processed = data.get("processed")
    if not isinstance(processed, list):
        processed = []
    payload = {
        "cycle": int(data.get("cycle") or 0),
        "pid": _coerce_pid(data.get("pid")),
        "processed": [str(item) for item in processed],
        "updated_epoch": int(data.get("updated_epoch") or 0),
    }
    text = json.dumps(payload, indent=2, sort_keys=True) + "\n"
    temporary = Path(str(path) + ".tmp")
    temporary.write_text(text, encoding="utf-8")
    os.replace(temporary, path)


def _blank_state() -> dict[str, Any]:
    return {"cycle": 0, "pid": None, "processed": [], "updated_epoch": 0}


def _is_running(state_path: Path) -> bool:
    if state_path.is_dir() or not state_path.is_file():
        return False
    data = _read_state(state_path)
    if not data:
        return False
    pid = _coerce_pid(data.get("pid"))
    return bool(pid and _pid_alive(pid))


def _reserved_paths(state_path: Path) -> set[Path]:
    reserved = {
        state_path,
        _log_path(state_path),
        Path(str(state_path) + ".tmp"),
    }
    resolved: set[Path] = set()
    for path in reserved:
        try:
            resolved.add(path.resolve())
        except OSError:
            continue
    return resolved


def _is_partial(path: Path) -> bool:
    return path.name.endswith(".part")


def _excluded(path: Path, patterns: tuple[str, ...]) -> bool:
    full = str(path)
    for pattern in patterns:
        if fnmatch.fnmatch(path.name, pattern) or fnmatch.fnmatch(full, pattern):
            return True
    return False


def _list_candidates(
    watches: list[_Watch], state_path: Path
) -> list[tuple[Path, Path]]:
    reserved = _reserved_paths(state_path)
    found: list[tuple[Path, Path]] = []
    seen: set[Path] = set()
    for watch in watches:
        directory = watch.path
        if not directory.exists() or not directory.is_dir():
            continue
        try:
            entries = list(directory.iterdir())
        except OSError:
            continue
        for entry in entries:
            if entry.is_symlink() or not entry.is_file():
                continue
            if _is_partial(entry) or _excluded(entry, watch.exclude):
                continue
            try:
                resolved = entry.resolve()
            except OSError:
                continue
            if resolved in reserved or resolved in seen:
                continue
            seen.add(resolved)
            found.append((entry, watch.movie_directory))
    found.sort(key=lambda item: str(item[0]))
    return found


def _is_stable(path: Path, checks: int, interval_ms: int) -> bool:
    samples = 1 if checks <= 1 else checks
    previous: int | None = None
    for index in range(samples):
        if _STOP:
            return False
        try:
            size = path.stat().st_size
        except OSError:
            return False
        if previous is not None and size != previous:
            return False
        previous = size
        if index < samples - 1 and interval_ms > 0:
            _sleep_seconds(interval_ms / 1000.0)
            if _STOP:
                return False
    return True


def _allocate_destination(directory: Path, filename: str, reserved: set[str]) -> Path:
    stem = Path(filename).stem
    suffix = Path(filename).suffix
    number = 0
    while number < 10000:
        name = filename if number == 0 else f"{stem} ({number}){suffix}"
        candidate = directory / name
        key = os.path.normcase(str(candidate))
        if key not in reserved and not candidate.exists():
            reserved.add(key)
            return candidate
        number += 1
    raise OSError(f"could not allocate a destination for {filename}")


def _copy_exclusive(src: Path, dst: Path) -> None:
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    fd = os.open(dst, flags, 0o644)
    try:
        with os.fdopen(fd, "wb") as out, src.open("rb") as src_handle:
            while True:
                chunk = src_handle.read(1024 * 1024)
                if not chunk:
                    break
                out.write(chunk)
            out.flush()
            os.fsync(out.fileno())
    except Exception:
        dst.unlink(missing_ok=True)
        raise
    try:
        os.chmod(dst, src.stat().st_mode & 0o777)
    except OSError:
        pass


def _safe_move(src: Path, dst: Path) -> None:
    dst.parent.mkdir(parents=True, exist_ok=True)
    if dst.exists():
        raise FileExistsError(dst)
    linked = False
    try:
        os.link(src, dst)
        linked = True
    except FileExistsError:
        raise
    except OSError:
        _copy_exclusive(src, dst)
    try:
        os.unlink(src)
    except OSError:
        if linked:
            dst.unlink(missing_ok=True)
        raise


def _place_file(src: Path, movie_directory: Path, reserved: set[str]) -> Path | None:
    natural = movie_directory / src.name
    try:
        if natural.resolve() == src.resolve():
            return None
    except OSError:
        pass
    error: Exception | None = None
    for _attempt in range(5):
        dest = _allocate_destination(movie_directory, src.name, reserved)
        try:
            _safe_move(src, dest)
            return dest
        except FileExistsError as exc:
            error = exc
            continue
    raise OSError(error or "move failed")


def _notify(url: str | None, src: Path, dst: Path) -> None:
    if not url:
        return
    body = json.dumps(
        {
            "destination": str(dst),
            "event": "moved",
            "source": str(src),
        }
    ).encode("utf-8")
    request = urllib.request.Request(
        url,
        data=body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=3) as response:
            response.read()
    except (urllib.error.URLError, TimeoutError, OSError, ValueError):
        return


def _record_cycle(state_path: Path, new_paths: list[str]) -> None:
    data = _read_state(state_path) or _blank_state()
    processed = data.get("processed")
    if not isinstance(processed, list):
        processed = []
    for item in new_paths:
        if item not in processed:
            processed.append(item)
    data["processed"] = processed
    _bump_epoch(data)
    _write_state(state_path, data)


def _append_log(state_path: Path, moved_names: list[str]) -> None:
    data = _read_state(state_path) or {}
    epoch = data.get("updated_epoch", 0)
    line = f"{epoch} moved={len(moved_names)}"
    if moved_names:
        line = f"{line} {','.join(moved_names)}"
    log_path = _log_path(state_path)
    log_path.parent.mkdir(parents=True, exist_ok=True)
    with log_path.open("a", encoding="utf-8") as handle:
        handle.write(line + "\n")


def run_cycle(settings: SettingStore, *, dry_run: bool) -> None:
    """Scan every watch directory once and move stable files."""
    watches = _watches_for_run(settings, require=False)
    state_path = _state_path(settings)
    if not dry_run and state_path.is_dir():
        _fail("error: daemon state path is a directory")
    candidates = _list_candidates(watches, state_path)
    batch_size = settings.batch_size
    move_limit: int | None = None
    if batch_size is not None:
        if batch_size <= 0:
            candidates = []
        else:
            move_limit = batch_size
    checks = 1 if settings.stability_checks is None else int(settings.stability_checks)
    interval = (
        0
        if settings.stability_interval_ms is None
        else int(settings.stability_interval_ms)
    )
    reserved: set[str] = set()
    moved: list[tuple[Path, Path]] = []
    accepted = 0
    for src, movie_directory in candidates:
        if _STOP or (move_limit is not None and accepted >= move_limit):
            break
        natural = movie_directory / src.name
        try:
            if natural.resolve() == src.resolve():
                continue
        except OSError:
            pass
        if not _is_stable(src, checks, interval):
            continue
        if dry_run:
            dest = _allocate_destination(movie_directory, src.name, reserved)
            print(f"{src.resolve()} -> {dest.resolve()}", flush=True)
            accepted += 1
            continue
        try:
            source_id = str(src.resolve())
            dest = _place_file(src, movie_directory, reserved)
        except OSError:
            continue
        if dest is None:
            continue
        moved.append((Path(source_id), dest))
        accepted += 1
        _notify(settings.notify_webhook, Path(source_id), dest)
    if dry_run:
        return
    _record_cycle(state_path, [str(src) for src, _dest in moved])
    _append_log(state_path, [src.name for src, _dest in moved])


def _run_once(settings: SettingStore) -> None:
    run_cycle(settings, dry_run=bool(settings.dry_run))


def _status(settings: SettingStore) -> None:
    if _is_running(_state_path(settings)):
        print("running", flush=True)
    else:
        print("not running", flush=True)


def _stats(settings: SettingStore) -> None:
    count = 0
    epoch = 0
    data = _read_state(_state_path(settings))
    if data:
        processed = data.get("processed")
        if isinstance(processed, list):
            count = len(processed)
        elif isinstance(processed, int) and not isinstance(processed, bool):
            count = processed
        updated = data.get("updated_epoch")
        if isinstance(updated, int) and not isinstance(updated, bool):
            epoch = updated
    print(f"processed={count}, last_epoch={epoch}", flush=True)


def _logs(settings: SettingStore) -> None:
    state_path = _state_path(settings)
    if state_path.is_dir():
        print("no logs available", flush=True)
        return
    log_path = _log_path(state_path)
    if not log_path.is_file():
        print("no logs available", flush=True)
        return
    try:
        text = log_path.read_text(encoding="utf-8")
    except OSError:
        print("no logs available", flush=True)
        return
    if text.strip() == "":
        print("no logs available", flush=True)
        return
    all_lines = text.splitlines()
    limit = settings.lines
    if limit is None:
        selected = all_lines
    elif limit <= 0:
        selected = []
    else:
        selected = all_lines[-limit:]
    if selected:
        print("\n".join(selected), flush=True)


def _stop(settings: SettingStore) -> None:
    state_path = _state_path(settings)
    if state_path.is_dir() or not state_path.exists():
        return
    data = _read_state(state_path)
    if data is None:
        return
    pid = _coerce_pid(data.get("pid"))
    if pid and _pid_alive(pid):
        try:
            os.kill(pid, signal.SIGTERM)
        except OSError:
            pass
        deadline = time.time() + 5
        while time.time() < deadline and _pid_alive(pid):
            time.sleep(0.05)
        if _pid_alive(pid):
            try:
                os.kill(pid, signal.SIGKILL)
            except OSError:
                pass
            _pid_alive(pid)
    if data.get("pid") is not None:
        data["pid"] = None
        try:
            _write_state(state_path, data)
        except OSError:
            return


def _worker_args(settings: SettingStore) -> list[str]:
    checks = 1 if settings.stability_checks is None else int(settings.stability_checks)
    interval = (
        0
        if settings.stability_interval_ms is None
        else int(settings.stability_interval_ms)
    )
    args = [
        "--config-ignore",
        "--daemon-state",
        str(_state_path(settings)),
        "--stability-checks",
        str(checks),
        "--stability-interval-ms",
        str(interval),
    ]
    if settings.daemon_config:
        args.extend(["--daemon-config", str(settings.daemon_config)])
    if settings.movie_directory:
        args.extend(["--movie-directory", str(settings.movie_directory)])
    if settings.batch_size is not None:
        args.extend(["--batch-size", str(int(settings.batch_size))])
    if settings.notify_webhook:
        args.extend(["--notify-webhook", str(settings.notify_webhook)])
    if settings.batch:
        args.append("--batch")
    cli_paths = [str(path) for path in (settings.watch or [])]
    cli_paths.extend(str(path) for path in (settings.targets or []))
    if cli_paths:
        args.append("--watch")
        args.extend(cli_paths)
    return args


def _spawn(settings: SettingStore) -> int:
    if os.environ.get(_WORKER_ENV) == "1":
        _fail("error: daemon worker cannot start another daemon")
    command = [sys.executable, "-m", "mnamer", *_worker_args(settings)]
    env = os.environ.copy()
    root = str(Path(__file__).resolve().parent.parent)
    prior = env.get("PYTHONPATH", "")
    env["PYTHONPATH"] = root if not prior else root + os.pathsep + prior
    env[_WORKER_ENV] = "1"
    try:
        process = subprocess.Popen(
            command,
            cwd=os.getcwd(),
            env=env,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            start_new_session=True,
        )
    except OSError as exc:
        _fail(f"error: failed to start daemon: {exc}")
    return process.pid


def _initialize_state(state_path: Path, pid: int) -> None:
    data = _read_state(state_path) or _blank_state()
    if not isinstance(data.get("processed"), list):
        data["processed"] = []
    data["pid"] = pid
    _bump_epoch(data)
    _write_state(state_path, data)


def _wait_for_pid(state_path: Path, pid: int) -> bool:
    deadline = time.time() + 10
    while time.time() < deadline and not _STOP:
        data = _read_state(state_path)
        if data and _coerce_pid(data.get("pid")) == pid:
            return True
        time.sleep(0.01)
    return False


def _run_worker(settings: SettingStore) -> None:
    signal.signal(signal.SIGTERM, _on_signal)
    signal.signal(signal.SIGINT, _on_signal)
    state_path = _state_path(settings)
    if not _wait_for_pid(state_path, os.getpid()):
        return
    while not _STOP:
        try:
            run_cycle(settings, dry_run=False)
        except SystemExit:
            raise
        except Exception as exc:
            try:
                _append_log(state_path, [f"error:{exc}"])
            except OSError:
                pass
            raise SystemExit(2) from exc
        _sleep_seconds(_POLL_SECONDS)


def _start(settings: SettingStore) -> None:
    state_path = _state_path(settings)
    if state_path.is_dir():
        _fail("error: daemon state path is a directory")
    _watches_for_run(settings, require=True)
    if _is_running(state_path):
        return
    pid = _spawn(settings)
    try:
        _initialize_state(state_path, pid)
    except OSError as exc:
        try:
            os.kill(pid, signal.SIGTERM)
        except OSError:
            pass
        _fail(f"error: could not initialize daemon state: {exc}")


def _restart(settings: SettingStore) -> None:
    state_path = _state_path(settings)
    if state_path.is_dir():
        _fail("error: daemon state path is a directory")
    _watches_for_run(settings, require=True)
    _stop(settings)
    _start(settings)
