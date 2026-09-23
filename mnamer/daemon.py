"""Background daemon that moves top-level files from watch dirs to movie dirs."""

import fnmatch
import json
import os
import shutil
import signal
import subprocess
import sys
import time
import urllib.request
from pathlib import Path
from typing import Any

from mnamer.setting_store import SettingStore

EXIT_ERROR = 2
LOOP_INTERVAL_S = 2.0


class ConfigError(Exception):
    pass


def load_config(path: str) -> list[dict[str, Any]]:
    config_path = Path(path)
    if not config_path.is_file():
        raise ConfigError(f"config not found: {path}")
    try:
        data = json.loads(config_path.read_text())
    except (OSError, ValueError) as e:
        raise ConfigError(f"invalid config JSON: {e}") from e
    if not isinstance(data, dict) or not isinstance(data.get("watch"), list):
        raise ConfigError("invalid config structure: expected object with 'watch' array")
    for i, entry in enumerate(data["watch"]):
        if not isinstance(entry, dict):
            raise ConfigError(f"invalid config structure: watch[{i}] must be an object")
        for key in ("path", "movie_directory"):
            if not isinstance(entry.get(key), str):
                raise ConfigError(
                    f"invalid config structure: watch[{i}].{key} must be a string"
                )
        exclude = entry.get("exclude")
        if exclude is not None and not (
            isinstance(exclude, list) and all(isinstance(x, str) for x in exclude)
        ):
            raise ConfigError(
                f"invalid config structure: watch[{i}].exclude must be an array of strings"
            )
    return data["watch"]


def build_options(settings: SettingStore) -> dict[str, Any]:
    movie_dir = str(settings.movie_directory) if settings.movie_directory else None
    watches: list[dict[str, Any]] = []
    for path in [*(settings.watch or []), *(str(t) for t in settings.targets)]:
        watches.append({"path": path, "movie_directory": movie_dir, "exclude": []})
    if settings.daemon_config:
        watches.extend(load_config(settings.daemon_config))
    return {
        "watch": watches,
        "state": settings.daemon_state,
        "interval_ms": settings.stability_interval_ms or 0,
        "checks": settings.stability_checks or 0,
        "batch_size": settings.batch_size,
        "webhook": settings.notify_webhook,
    }


def _pid_path(state: str) -> Path:
    return Path(state + ".pid")


def _log_path(state: str) -> Path:
    return Path(state + ".log")


def _read_state(state: str) -> dict[str, Any]:
    try:
        data = json.loads(Path(state).read_text())
        if isinstance(data, dict):
            return data
    except (OSError, ValueError):
        pass
    return {"processed": [], "updated_epoch": 0, "cycles": 0}


def _write_state(state: str, data: dict[str, Any]) -> None:
    try:
        Path(state).write_text(json.dumps(data, indent=2))
    except OSError:
        pass


def _log(state: str, message: str) -> None:
    try:
        with open(_log_path(state), "a") as fp:
            fp.write(f"{time.strftime('%Y-%m-%dT%H:%M:%S')} {message}\n")
    except OSError:
        pass


def _is_stable(path: Path, interval_ms: int, checks: int) -> bool:
    try:
        size = path.stat().st_size
        for _ in range(checks):
            time.sleep(interval_ms / 1000)
            new_size = path.stat().st_size
            if new_size != size:
                return False
    except OSError:
        return False
    return True


def _unique_destination(dest: Path) -> Path:
    if not dest.exists():
        return dest
    for i in range(1, 10000):
        candidate = dest.with_name(f"{dest.stem} ({i}){dest.suffix}")
        if not candidate.exists():
            return candidate
    raise FileExistsError(dest)


def _notify(url: str | None, payload: dict[str, Any]) -> None:
    if not url:
        return
    try:
        request = urllib.request.Request(
            url,
            data=json.dumps(payload).encode(),
            headers={"Content-Type": "application/json"},
        )
        urllib.request.urlopen(request, timeout=5).close()
    except Exception:
        pass


def run_once(options: dict[str, Any], dry_run: bool = False) -> list[tuple[str, str]]:
    moves: list[tuple[str, str]] = []
    batch_size = options.get("batch_size")
    remaining = batch_size if batch_size is not None else -1
    for watch in options["watch"]:
        if remaining == 0:
            break
        src_dir = Path(watch["path"])
        movie_dir = watch.get("movie_directory")
        if not movie_dir or not src_dir.is_dir():
            continue
        excludes = watch.get("exclude") or []
        for entry in sorted(src_dir.iterdir()):
            if remaining == 0:
                break
            if not entry.is_file() or entry.name.endswith(".part"):
                continue
            if any(fnmatch.fnmatch(entry.name, pat) for pat in excludes):
                continue
            if not _is_stable(entry, options["interval_ms"], options["checks"]):
                continue
            dest_dir = Path(movie_dir)
            if (dest_dir / entry.name).resolve() == entry.resolve():
                continue
            try:
                dest = _unique_destination(dest_dir / entry.name)
            except FileExistsError:
                continue
            if dry_run:
                print(f"{entry} -> {dest}")
            else:
                try:
                    dest_dir.mkdir(parents=True, exist_ok=True)
                    shutil.move(str(entry), str(dest))
                except OSError:
                    continue
            moves.append((str(entry), str(dest)))
            remaining -= 1
    if dry_run:
        return moves
    state = options["state"]
    data = _read_state(state)
    data.setdefault("processed", []).extend(src for src, _ in moves)
    data["updated_epoch"] = int(time.time())
    data["cycles"] = data.get("cycles", 0) + 1
    data["last_cycle_ns"] = time.time_ns()
    _write_state(state, data)
    _log(state, f"cycle moved={len(moves)}")
    for src, dst in moves:
        _log(state, f"moved {src} -> {dst}")
        _notify(options.get("webhook"), {"source": src, "destination": dst})
    return moves


def _loop(options_json: str) -> None:
    options = json.loads(options_json)
    running = True

    def _stop(*_):
        nonlocal running
        running = False

    signal.signal(signal.SIGTERM, _stop)
    while running:
        try:
            run_once(options)
        except Exception as e:
            _log(options["state"], f"error {e}")
        slept = 0.0
        while running and slept < LOOP_INTERVAL_S:
            time.sleep(0.1)
            slept += 0.1


def _running_pid(state: str) -> int | None:
    if Path(state).is_dir():
        return None
    try:
        pid = int(_pid_path(state).read_text().strip())
        os.kill(pid, 0)
        return pid
    except (OSError, ValueError):
        return None


def start(settings: SettingStore) -> int:
    options = build_options(settings)
    if not options["watch"]:
        print("no watch paths configured", file=sys.stderr)
        return EXIT_ERROR
    state = options["state"]
    if _running_pid(state):
        print("running")
        return 0
    data = _read_state(state)
    data.setdefault("updated_epoch", int(time.time()))
    data["started_epoch"] = int(time.time())
    _write_state(state, data)
    _log(state, "daemon started")
    process = subprocess.Popen(
        [
            sys.executable,
            "-c",
            "import sys; from mnamer.daemon import _loop; _loop(sys.argv[1])",
            json.dumps(options),
        ],
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
    )
    try:
        _pid_path(state).write_text(str(process.pid))
    except OSError:
        pass
    print("started")
    return 0


def stop(state: str) -> int:
    pid = _running_pid(state)
    if pid:
        try:
            os.kill(pid, signal.SIGTERM)
            for _ in range(50):
                os.kill(pid, 0)
                time.sleep(0.1)
        except OSError:
            pass
        _log(state, "daemon stopped")
    if not Path(state).is_dir():
        try:
            _pid_path(state).unlink()
        except OSError:
            pass
    print("stopped")
    return 0


def logs(state: str, lines: int | None) -> int:
    content = ""
    if not Path(state).is_dir():
        try:
            content = _log_path(state).read_text()
        except OSError:
            content = ""
    if not content:
        print("no logs available")
        return 0
    rows = content.splitlines()
    if lines is not None:
        rows = rows[-lines:] if lines > 0 else []
    for row in rows:
        print(row)
    return 0


def stats(state: str) -> int:
    data = _read_state(state)
    print(
        f"processed={len(data.get('processed', []))}, "
        f"last_epoch={int(data.get('updated_epoch', 0))}"
    )
    return 0


def handle(settings: SettingStore) -> int | None:
    """Dispatches daemon directives; returns an exit code or None if not applicable."""
    state = settings.daemon_state
    if settings.validate_daemon_config:
        if not settings.daemon_config:
            print("--validate-daemon-config requires --daemon-config", file=sys.stderr)
            return EXIT_ERROR
        try:
            load_config(settings.daemon_config)
        except ConfigError as e:
            print(str(e), file=sys.stderr)
            return EXIT_ERROR
        print("config valid")
        return 0
    if settings.daemon_run_once:
        try:
            options = build_options(settings)
        except ConfigError as e:
            print(str(e), file=sys.stderr)
            return EXIT_ERROR
        run_once(options, dry_run=settings.dry_run)
        return 0
    command = settings.daemon
    if command is None:
        return None
    if command in ("start", "restart"):
        if command == "restart":
            if _running_pid(state):
                stop(state)
        try:
            return start(settings)
        except ConfigError as e:
            print(str(e), file=sys.stderr)
            return EXIT_ERROR
    if command == "stop":
        return stop(state)
    if command == "status":
        print("running" if _running_pid(state) else "not running")
        return 0
    if command == "logs":
        return logs(state, settings.lines)
    if command == "stats":
        return stats(state)
    return EXIT_ERROR
