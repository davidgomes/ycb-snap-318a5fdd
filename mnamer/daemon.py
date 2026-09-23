"""Watch-directory daemon: move top-level files into a movie directory."""

from __future__ import annotations

import json
import os
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request
from fnmatch import fnmatch
from pathlib import Path
from typing import Any

from mnamer.setting_store import SettingStore

_WORKER_ENV = "MNAMER_DAEMON_WORKER"
_DEFAULT_STATE = "daemon-state.json"


def daemon_requested(settings: SettingStore) -> bool:
    return bool(
        settings.daemon or settings.daemon_run_once or settings.validate_daemon_config
    )


def dispatch_daemon(settings: SettingStore) -> None:
    if os.environ.get(_WORKER_ENV) == "1":
        _worker_loop(settings)
        return
    if settings.validate_daemon_config:
        _validate_cli(settings)
        return
    action = settings.daemon
    if action == "start":
        _start(settings)
    elif action == "stop":
        _stop(settings)
    elif action == "status":
        _status(settings)
    elif action == "logs":
        _logs(settings)
    elif action == "stats":
        _stats(settings)
    elif action == "restart":
        _restart(settings)
    elif settings.daemon_run_once:
        _run_once(settings)
    else:
        print("unknown daemon action", file=sys.stderr)
        raise SystemExit(2)


def _state_path(settings: SettingStore) -> Path:
    raw = settings.daemon_state or _DEFAULT_STATE
    return Path(raw)


def _log_path(settings: SettingStore) -> Path:
    state = _state_path(settings)
    return Path(str(state) + ".log")


def _eprint(message: str) -> None:
    print(message, file=sys.stderr)


def _validate_cli(settings: SettingStore) -> None:
    if not settings.daemon_config:
        _eprint("missing --daemon-config")
        raise SystemExit(2)
    path = Path(settings.daemon_config)
    if not path.is_file():
        _eprint(f"daemon config not found: {path}")
        raise SystemExit(2)
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        _eprint("invalid daemon config structure")
        raise SystemExit(2) from None
    error = _config_structure_error(payload)
    if error:
        _eprint(error)
        raise SystemExit(2)
    raise SystemExit(0)


def _config_structure_error(payload: Any) -> str | None:
    if not isinstance(payload, dict) or "watch" not in payload:
        return "invalid daemon config structure: expected an object with a watch array"
    watches = payload["watch"]
    if not isinstance(watches, list):
        return "invalid daemon config structure: watch must be an array"
    for entry in watches:
        if not isinstance(entry, dict):
            return "invalid daemon config structure: each watch entry must be an object"
        path = entry.get("path", None)
        movie = entry.get("movie_directory", None)
        if "path" not in entry or not isinstance(path, str):
            return "invalid daemon config structure: watch path must be a string"
        if "movie_directory" not in entry or not isinstance(movie, str):
            return (
                "invalid daemon config structure: watch movie_directory must be a string"
            )
        if "exclude" in entry:
            exclude = entry["exclude"]
            if not isinstance(exclude, list) or not all(
                isinstance(item, str) for item in exclude
            ):
                return (
                    "invalid daemon config structure: exclude must be an array of strings"
                )
    return None


def _load_config_watches(settings: SettingStore) -> list[dict[str, Any]]:
    if not settings.daemon_config:
        return []
    path = Path(settings.daemon_config)
    if not path.is_file():
        return []
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return []
    if _config_structure_error(payload):
        return []
    return list(payload["watch"])


def _combined_watches(settings: SettingStore) -> list[tuple[Path, Path, list[str]]]:
    """CLI --watch and positionals share --movie-directory; config entries are appended."""
    watches: list[tuple[Path, Path, list[str]]] = []
    movie = settings.movie_directory
    cli_paths = list(settings.watch or []) + [str(p) for p in (settings.targets or [])]
    if movie is not None:
        for raw in cli_paths:
            watches.append((Path(raw), Path(movie), []))
    for entry in _load_config_watches(settings):
        watches.append(
            (
                Path(entry["path"]),
                Path(entry["movie_directory"]),
                list(entry.get("exclude") or []),
            )
        )
    # Drop placeholder entries that have no movie directory if real ones exist.
    usable = [(s, d, e) for s, d, e in watches if str(d) != ""]
    if usable:
        return usable
    return [(s, d, e) for s, d, e in watches if str(d) != ""]


def _has_watch(settings: SettingStore) -> bool:
    if settings.watch or settings.targets:
        return True
    return bool(_load_config_watches(settings))


def _read_state(path: Path) -> dict[str, Any]:
    if not path.is_file():
        return {}
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return {}
    return data if isinstance(data, dict) else {}


def _write_state(path: Path, data: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    text = json.dumps(data, indent=2, sort_keys=True)
    path.write_text(text + "\n", encoding="utf-8")


def _fresh_state(pid: int | None = None) -> dict[str, Any]:
    return {
        "pid": pid,
        "processed": [],
        "running": bool(pid),
        "seq": 0,
        "updated_epoch": int(time.time()),
    }


def _pid_alive(pid: int | None) -> bool:
    if not pid or pid <= 0:
        return False
    try:
        os.kill(pid, 0)
    except OSError:
        return False
    return True


def _is_running(settings: SettingStore) -> bool:
    path = _state_path(settings)
    if path.is_dir() or not path.exists():
        return False
    state = _read_state(path)
    return _pid_alive(state.get("pid"))


def _start(settings: SettingStore) -> None:
    if not _has_watch(settings):
        _eprint("no watch paths configured")
        raise SystemExit(2)
    path = _state_path(settings)
    if path.is_dir():
        _eprint("daemon state path is a directory")
        raise SystemExit(2)
    if _is_running(settings):
        print("running")
        raise SystemExit(0)
    # Initialize before the worker can process anything.
    existing = _read_state(path)
    state = _fresh_state(None)
    state["processed"] = list(existing.get("processed") or [])
    state["seq"] = int(existing.get("seq") or 0)
    state["running"] = True
    _write_state(path, state)
    env = os.environ.copy()
    env[_WORKER_ENV] = "1"
    proc = subprocess.Popen(
        [sys.executable, "-m", "mnamer", *sys.argv[1:]],
        cwd=os.getcwd(),
        env=env,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
    )
    current = _read_state(path)
    if not current:
        current = state
    current["pid"] = proc.pid
    current["running"] = True
    _write_state(path, current)
    raise SystemExit(0)


def _stop_process(settings: SettingStore) -> None:
    path = _state_path(settings)
    if path.is_dir() or not path.exists():
        return
    state = _read_state(path)
    pid = state.get("pid")
    if _pid_alive(pid):
        try:
            os.kill(pid, signal.SIGTERM)
        except OSError:
            pass
        deadline = time.time() + 3
        while time.time() < deadline and _pid_alive(pid):
            time.sleep(0.05)
        if _pid_alive(pid):
            try:
                os.kill(pid, signal.SIGKILL)
            except OSError:
                pass
    if state:
        state["pid"] = None
        state["running"] = False
        _write_state(path, state)


def _stop(settings: SettingStore) -> None:
    _stop_process(settings)
    raise SystemExit(0)


def _status(settings: SettingStore) -> None:
    print("running" if _is_running(settings) else "not running")
    raise SystemExit(0)


def _logs(settings: SettingStore) -> None:
    state = _state_path(settings)
    log = _log_path(settings)
    if state.is_dir() or not log.is_file():
        print("no logs available")
        raise SystemExit(0)
    text = log.read_text(encoding="utf-8")
    if text.strip() == "":
        print("no logs available")
        raise SystemExit(0)
    lines = text.splitlines()
    limit = settings.lines
    if limit is not None:
        if limit <= 0:
            raise SystemExit(0)
        lines = lines[-limit:]
    sys.stdout.write("\n".join(lines) + "\n")
    raise SystemExit(0)


def _stats(settings: SettingStore) -> None:
    path = _state_path(settings)
    state = {} if path.is_dir() else _read_state(path)
    processed = state.get("processed") or []
    count = len(processed) if isinstance(processed, list) else 0
    epoch = int(state.get("updated_epoch") or 0)
    print(f"processed={count}, last_epoch={epoch}")
    raise SystemExit(0)


def _restart(settings: SettingStore) -> None:
    if _is_running(settings):
        _stop_process(settings)
    _start(settings)


def _run_once(settings: SettingStore) -> None:
    dry = bool(settings.dry_run)
    if dry:
        _cycle(settings, dry_run=True)
        raise SystemExit(0)
    _cycle(settings, dry_run=False)
    raise SystemExit(0)


def _worker_loop(settings: SettingStore) -> None:
    stop = False

    def _handle(_signum, _frame):
        nonlocal stop
        stop = True

    signal.signal(signal.SIGTERM, _handle)
    signal.signal(signal.SIGINT, _handle)
    path = _state_path(settings)
    deadline = time.time() + 5
    while time.time() < deadline:
        if _read_state(path).get("pid") == os.getpid():
            break
        time.sleep(0.01)
    while not stop:
        try:
            _cycle(settings, dry_run=False)
        except Exception:
            _append_log(settings, "cycle error")
        for _ in range(10):
            if stop:
                break
            time.sleep(0.1)
    path = _state_path(settings)
    if path.is_file():
        state = _read_state(path)
        if state.get("pid") == os.getpid():
            state["running"] = False
            state["pid"] = None
            _write_state(path, state)


def _append_log(settings: SettingStore, line: str) -> None:
    log = _log_path(settings)
    log.parent.mkdir(parents=True, exist_ok=True)
    with log.open("a", encoding="utf-8") as handle:
        handle.write(line.rstrip("\n") + "\n")


def _touch_state(settings: SettingStore, new_paths: list[str]) -> None:
    path = _state_path(settings)
    if path.is_dir():
        return
    state = _read_state(path)
    if not state:
        state = _fresh_state(os.getpid() if os.environ.get(_WORKER_ENV) == "1" else None)
    processed = list(state.get("processed") or [])
    for item in new_paths:
        if item not in processed:
            processed.append(item)
    state["processed"] = processed
    state["updated_epoch"] = int(time.time())
    state["seq"] = int(state.get("seq") or 0) + 1
    # seq guarantees the file body changes even within the same epoch second
    _write_state(path, state)


def _cycle(settings: SettingStore, dry_run: bool) -> None:
    moved: list[tuple[Path, Path]] = []
    limit = settings.batch_size
    interval = settings.stability_interval_ms
    if interval is None:
        interval = 0
    checks = settings.stability_checks
    if checks is None or checks < 1:
        checks = 1
    for source_dir, movie_dir, excludes in _combined_watches(settings):
        if limit is not None and len(moved) >= limit:
            break
        if not source_dir.is_dir() or str(movie_dir) == "":
            continue
        if not dry_run:
            movie_dir.mkdir(parents=True, exist_ok=True)
        for source in sorted(p for p in source_dir.iterdir() if p.is_file()):
            if limit is not None and len(moved) >= limit:
                break
            if _excluded(source, excludes):
                continue
            if not _stable(source, interval, checks):
                continue
            destination = _destination(movie_dir, source.name)
            if destination is None:
                continue
            if dry_run:
                print(f"{source} -> {destination}")
                moved.append((source, destination))
                continue
            try:
                source.replace(destination)
            except OSError:
                continue
            moved.append((source, destination))
            _notify(settings, source, destination)
    if dry_run:
        return
    _touch_state(settings, [str(src) for src, _dst in moved])
    _append_log(
        settings,
        f"{int(time.time())} cycle processed={len(moved)}",
    )


def _excluded(path: Path, patterns: list[str]) -> bool:
    name = path.name
    if name.endswith(".part"):
        return True
    return any(fnmatch(name, pattern) for pattern in patterns)


def _stable(path: Path, interval_ms: int, checks: int) -> bool:
    try:
        previous = path.stat().st_size
    except OSError:
        return False
    for _ in range(max(checks - 1, 0)):
        if interval_ms > 0:
            time.sleep(interval_ms / 1000.0)
        try:
            current = path.stat().st_size
        except OSError:
            return False
        if current != previous:
            return False
        previous = current
    return True


def _destination(movie_dir: Path, name: str) -> Path | None:
    candidate = movie_dir / name
    if not candidate.exists():
        return candidate
    stem = Path(name).stem
    suffix = Path(name).suffix
    for index in range(1, 1000):
        alt = movie_dir / f"{stem} ({index}){suffix}"
        if not alt.exists():
            return alt
    return None


def _notify(settings: SettingStore, source: Path, destination: Path) -> None:
    url = settings.notify_webhook
    if not url:
        return
    body = json.dumps({"src": str(source), "dst": str(destination)}).encode("utf-8")
    request = urllib.request.Request(
        url,
        data=body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=2):
            return
    except (urllib.error.URLError, OSError, TimeoutError, ValueError):
        return
