import hashlib
import json
import pkgutil
import subprocess
import sys
from pathlib import Path
from textwrap import dedent

import pytest

from vulture import cache, core, utils
from vulture.config import _parse_args
from vulture.utils import ExitCode

from . import REPO


def write(path, code):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(dedent(code))
    return path


def run(paths, cache_dir, **kwargs):
    v = core.Vulture(cache_dir=cache_dir, **kwargs)
    v.scavenge(paths)
    return v


def keys(*paths):
    return {cache.normalize_path(path) for path in paths}


def get_results(v):
    return sorted(
        (
            str(item.filename),
            item.first_lineno,
            item.last_lineno,
            item.name,
            item.typ,
            item.message,
            item.confidence,
        )
        for item in v.get_unused_code()
    )


def load_cache(cache_dir):
    return json.loads(cache.get_cache_path(cache_dir).read_text())


def get_sections(cache_dir):
    """Return the keys of analyzed and unanalyzed modules."""
    data = load_cache(cache_dir)
    return set(data["modules"]), set(data["unanalyzed"])


def assert_cache_files(cache_dir):
    names = {path.name for path in cache_dir.iterdir()}
    assert {"cache.json", "cache.json.bak", "cache.json.meta"} <= names
    # Windows additionally uses a lock file.
    assert names <= {"cache.json", "cache.json.bak", "cache.json.meta"} | {
        cache.LOCK_FILENAME
    }


def check_checksum(cache_dir):
    cache_path = cache.get_cache_path(cache_dir)
    meta = json.loads(Path(f"{cache_path}.meta").read_text())
    assert (
        meta["sha256"] == hashlib.sha256(cache_path.read_bytes()).hexdigest()
    )


@pytest.fixture
def cache_dir(tmp_path):
    return tmp_path / "cache"


@pytest.fixture
def project(tmp_path):
    """
    main imports pkg.api, which imports pkg.core relatively. standalone
    doesn't import anything.
    """
    root = tmp_path / "project"
    write(
        root / "main.py",
        """\
        import sys

        from pkg import api

        def unused_main():
            return api.run(sys.argv)
        """,
    )
    write(root / "pkg" / "__init__.py", "")
    write(
        root / "pkg" / "api.py",
        """\
        from .core import compute

        def run(args):
            return compute(args)
        """,
    )
    write(
        root / "pkg" / "core.py",
        """\
        def compute(args):
            return args
            print("unreachable")

        class Unused:
            attr = 1
        """,
    )
    write(
        root / "standalone.py",
        """\
        import os

        def unused_standalone(x):  # noqa: V103
            unused_var = 1
        """,
    )
    return root


def test_normalize_path(tmp_path, monkeypatch):
    path = tmp_path / "a.py"
    assert cache.normalize_path(path) == str(path.resolve())
    assert cache.normalize_path(tmp_path / "sub" / ".." / "a.py") == str(
        path.resolve()
    )
    monkeypatch.chdir(tmp_path)
    assert cache.normalize_path("a.py") == cache.normalize_path(path)


def test_normalize_path_is_case_insensitive_on_windows(tmp_path, monkeypatch):
    monkeypatch.setattr(sys, "platform", "win32")
    normalized = cache.normalize_path(tmp_path / "Mixed" / "Case.PY")
    assert normalized == normalized.lower()
    assert normalized == cache.normalize_path(tmp_path / "mixed" / "case.py")


def test_get_cache_path(tmp_path):
    assert cache.get_cache_path(tmp_path) == tmp_path / "cache.json"
    assert cache.get_cache_path("dir") == Path("dir", "cache.json")


def test_runtime_signature(monkeypatch):
    monkeypatch.setattr(
        cache.importlib.metadata, "version", lambda _name: "1.2.3"
    )
    assert cache.get_runtime_signature() == {
        "cache": cache.__version__,
        "python": sys.version,
        "vulture": "1.2.3",
    }


def test_unchanged_modules_are_reused(project, cache_dir, capsys):
    expected = core.Vulture()
    expected.scavenge([project])
    all_modules = {
        cache.normalize_path(path) for path in project.rglob("*.py")
    }

    first = run([project], cache_dir)
    assert first._cache_stats == {"scanned": all_modules, "reused": set()}
    second = run([project], cache_dir)
    assert second._cache_stats == {"scanned": set(), "reused": all_modules}

    assert get_results(first) == get_results(second) == get_results(expected)
    assert get_results(expected)
    assert second.report() == expected.report() == ExitCode.DeadCode
    assert capsys.readouterr().err == ""
    assert set(load_cache(cache_dir)["modules"]) == all_modules


def test_changed_module_and_importers_are_rescanned(project, cache_dir):
    run([project], cache_dir)
    core_module = project / "pkg" / "core.py"
    core_module.write_text(core_module.read_text() + "\nnew_var = 2\n")

    v = run([project], cache_dir)
    assert v._cache_stats == {
        "scanned": keys(
            core_module, project / "pkg" / "api.py", project / "main.py"
        ),
        "reused": keys(
            project / "pkg" / "__init__.py", project / "standalone.py"
        ),
    }
    assert "new_var" in {item.name for item in v.unused_vars}


def test_touched_but_unchanged_module_is_reused(project, cache_dir):
    run([project], cache_dir)
    module = project / "standalone.py"
    module.write_text(module.read_text())
    v = run([project], cache_dir)
    assert not v._cache_stats["scanned"]


def test_import_cycle(tmp_path, cache_dir):
    a = write(tmp_path / "a.py", "import b\n")
    b = write(tmp_path / "b.py", "import a\n")
    c = write(tmp_path / "c.py", "import a\n")
    d = write(tmp_path / "d.py", "")
    run([a, b, c, d], cache_dir)
    b.write_text("import a\nx = 1\n")
    v = run([a, b, c, d], cache_dir)
    assert v._cache_stats == {"scanned": keys(a, b, c), "reused": keys(d)}


@pytest.mark.parametrize(
    "import_statement",
    [
        "from . import target",
        "from .target import name",
        "from .. import target",
        "from ..sub import target",
        "import pkg.sub.target",
        "from pkg.sub import target",
        "from pkg.sub.target import name",
    ],
)
def test_import_kinds(tmp_path, cache_dir, import_statement):
    package = tmp_path / "pkg"
    if import_statement != "from .. import target":
        package = package / "sub"
    target = write(package / "target.py", "name = 1\n")
    importer = write(
        tmp_path / "pkg" / "sub" / "importer.py", import_statement
    )
    other = write(tmp_path / "pkg" / "sub" / "other.py", "name = 2\n")
    run([tmp_path], cache_dir)
    target.write_text("name = 3\n")
    v = run([tmp_path], cache_dir)
    assert keys(target, importer) <= v._cache_stats["scanned"]
    assert keys(other) <= v._cache_stats["reused"]


def test_imports_respect_packages(tmp_path, cache_dir):
    top_level = write(tmp_path / "html" / "__init__.py", "")
    write(tmp_path / "lib" / "__init__.py", "")
    write(tmp_path / "lib" / "fmt" / "__init__.py", "")
    nested = write(tmp_path / "lib" / "fmt" / "html.py", "")
    importer = write(tmp_path / "importer.py", "import html\n")
    run([tmp_path], cache_dir)

    nested.write_text("x = 1\n")
    assert run([tmp_path], cache_dir)._cache_stats["scanned"] == keys(nested)
    top_level.write_text("x = 1\n")
    assert run([tmp_path], cache_dir)._cache_stats["scanned"] == keys(
        top_level, importer
    )


@pytest.mark.parametrize(
    "importer_path, import_statement, target_path",
    [
        # Script next to the imported module inside a package.
        ("pkg/script.py", "import helper", "pkg/helper.py"),
        # Namespace package without __init__.py.
        ("user.py", "from ns.pkg import mod", "ns/pkg/mod.py"),
    ],
)
def test_import_roots(
    tmp_path, cache_dir, importer_path, import_statement, target_path
):
    write(tmp_path / "pkg" / "__init__.py", "")
    write(tmp_path / "ns" / "pkg" / "__init__.py", "")
    importer = write(tmp_path / importer_path, import_statement)
    target = write(tmp_path / target_path, "")
    run([tmp_path], cache_dir)
    target.write_text("x = 1\n")
    assert run([tmp_path], cache_dir)._cache_stats["scanned"] == keys(
        importer, target
    )


def test_new_module_invalidates_importers(tmp_path, cache_dir):
    importer = write(tmp_path / "importer.py", "import helper\n")
    other = write(tmp_path / "other.py", "")
    run([tmp_path], cache_dir)
    helper = write(tmp_path / "helper.py", "")
    v = run([tmp_path], cache_dir)
    assert v._cache_stats == {
        "scanned": keys(importer, helper),
        "reused": keys(other),
    }


def test_deleted_and_renamed_modules(project, cache_dir):
    run([project], cache_dir)
    (project / "pkg" / "core.py").unlink()
    standalone = project / "standalone.py"
    renamed = standalone.rename(project / "renamed.py")

    v = run([project], cache_dir)
    assert v._cache_stats == {
        "scanned": keys(
            project / "pkg" / "api.py", project / "main.py", renamed
        ),
        "reused": keys(project / "pkg" / "__init__.py"),
    }
    modules = load_cache(cache_dir)["modules"]
    assert cache.normalize_path(renamed) in modules
    assert not keys(project / "pkg" / "core.py", standalone) & set(modules)


def test_entries_of_other_runs_are_kept(tmp_path, cache_dir):
    a = write(tmp_path / "a" / "a.py", "")
    b = write(tmp_path / "b" / "b.py", "")
    run([a.parent], cache_dir)
    run([b.parent], cache_dir)
    assert keys(a, b) == set(load_cache(cache_dir)["modules"])
    assert run([a.parent, b.parent], cache_dir)._cache_stats["reused"] == keys(
        a, b
    )


@pytest.mark.parametrize(
    "attribute, value",
    [
        ("__version__", "0"),
        ("sys.version", "0.0.0 (changed)"),
        ("importlib.metadata.version", lambda _name: "0.0"),
    ],
)
def test_runtime_signature_change_invalidates_cache(
    project, cache_dir, monkeypatch, capsys, attribute, value
):
    run([project], cache_dir)
    if attribute == "sys.version":
        monkeypatch.setattr(sys, "version", value)
    elif attribute == "__version__":
        monkeypatch.setattr(cache, "__version__", value)
    else:
        monkeypatch.setattr(cache.importlib.metadata, "version", value)
    v = run([project], cache_dir)
    assert not v._cache_stats["reused"]
    assert capsys.readouterr().err == ""
    assert run([project], cache_dir)._cache_stats["scanned"] == set()


@pytest.mark.parametrize(
    "kwargs",
    [
        {"cache_settings": {"option": "changed"}},
        {"ignore_names": ["unused_*"]},
        {"ignore_decorators": ["@app.route"]},
    ],
)
def test_settings_change_invalidates_cache(project, cache_dir, kwargs):
    run([project], cache_dir, cache_settings={"option": "value"})
    kwargs = {"cache_settings": {"option": "value"}, **kwargs}
    v = run([project], cache_dir, **kwargs)
    assert not v._cache_stats["reused"]
    assert run([project], cache_dir, **kwargs)._cache_stats["scanned"] == set()


def test_missing_cache_is_silent(project, cache_dir, capsys):
    run([project], cache_dir)
    cache.get_cache_path(cache_dir).unlink()
    v = run([project], cache_dir)
    assert not v._cache_stats["reused"]
    assert capsys.readouterr().err == ""


def corrupt_contents(cache_path):
    cache_path.write_text("{not json")


def change_contents(cache_path):
    data = json.loads(cache_path.read_text())
    data["modules"] = {}
    cache_path.write_text(json.dumps(data))


def remove_meta(cache_path):
    Path(f"{cache_path}.meta").unlink()


def corrupt_meta(cache_path):
    Path(f"{cache_path}.meta").write_text("[]")


def invalid_entry(cache_path):
    data = json.loads(cache_path.read_text())
    for entry in data["modules"].values():
        entry["defined"] = {"function": [["name", "1"]]}
    contents = json.dumps(data).encode()
    cache_path.write_bytes(contents)
    Path(f"{cache_path}.meta").write_text(
        json.dumps({"sha256": hashlib.sha256(contents).hexdigest()})
    )


@pytest.mark.parametrize(
    "corrupt",
    [
        corrupt_contents,
        change_contents,
        remove_meta,
        corrupt_meta,
        invalid_entry,
    ],
)
def test_corrupt_cache(project, cache_dir, capsys, corrupt):
    expected = get_results(run([project], cache_dir))
    corrupt(cache.get_cache_path(cache_dir))
    capsys.readouterr()

    v = run([project], cache_dir)
    assert "cache is corrupted or unreadable" in capsys.readouterr().err
    assert not v._cache_stats["reused"]
    assert get_results(v) == expected

    check_checksum(cache_dir)
    assert not run([project], cache_dir)._cache_stats["scanned"]
    assert capsys.readouterr().err == ""


def test_unreadable_cache(project, cache_dir, capsys):
    cache_dir.mkdir()
    cache.get_cache_path(cache_dir).mkdir()
    v = run([project], cache_dir)
    captured = capsys.readouterr().err
    assert "cache is corrupted or unreadable" in captured
    assert "could not write vulture cache" in captured
    assert v.report() == ExitCode.DeadCode


def test_backup_and_meta_are_written_on_every_save(project, cache_dir):
    cache_path = cache.get_cache_path(cache_dir)
    for _ in range(2):
        run([project], cache_dir)
        backup = Path(f"{cache_path}.bak")
        assert backup.read_bytes() == cache_path.read_bytes()
        check_checksum(cache_dir)
        assert_cache_files(cache_dir)


def test_keyboard_interrupt_saves_partial_cache(
    tmp_path, cache_dir, monkeypatch
):
    modules = [write(tmp_path / f"m{i}.py", f"x{i} = {i}\n") for i in range(4)]
    read_file = utils.read_file
    read_modules = []

    def interrupt_third_read(path):
        read_modules.append(path)
        if len(read_modules) == 3:
            raise KeyboardInterrupt
        return read_file(path)

    monkeypatch.setattr(utils, "read_file", interrupt_third_read)
    with pytest.raises(KeyboardInterrupt):
        run(modules, cache_dir)
    monkeypatch.undo()

    check_checksum(cache_dir)
    assert get_sections(cache_dir) == (keys(*modules[:2]), keys(*modules[2:]))
    v = run(modules, cache_dir)
    assert v._cache_stats == {
        "scanned": keys(*modules[2:]),
        "reused": keys(*modules[:2]),
    }


def test_resume_interrupted_first_run(tmp_path, cache_dir, monkeypatch):
    """
    Modules that the interrupted run didn't analyze only invalidate their
    importers if something changed, since their imports are unknown.
    """
    importer = write(tmp_path / "importer.py", "import late\n")
    other = write(tmp_path / "other.py", "")
    late = write(tmp_path / "late.py", "")
    read_file = utils.read_file

    def interrupt_late(path):
        if path == late:
            raise KeyboardInterrupt
        return read_file(path)

    monkeypatch.setattr(utils, "read_file", interrupt_late)
    with pytest.raises(KeyboardInterrupt):
        run([importer, other, late], cache_dir)
    monkeypatch.undo()

    assert get_sections(cache_dir) == (keys(importer, other), keys(late))
    assert run([importer, other, late], cache_dir)._cache_stats == {
        "scanned": keys(late),
        "reused": keys(importer, other),
    }


def test_keyboard_interrupt_keeps_valid_entries(
    tmp_path, cache_dir, monkeypatch
):
    modules = [write(tmp_path / f"m{i}.py", f"x{i} = {i}\n") for i in range(3)]
    importer = write(tmp_path / "importer.py", "import m0\n")
    modules.append(importer)
    run(modules, cache_dir)
    modules[0].write_text("changed = 1\n")

    def interrupt(_path):
        raise KeyboardInterrupt

    monkeypatch.setattr(utils, "read_file", interrupt)
    with pytest.raises(KeyboardInterrupt):
        run(modules, cache_dir)
    monkeypatch.undo()

    # Both modules that were outdated in the interrupted run are analyzed.
    assert run(modules, cache_dir)._cache_stats == {
        "scanned": keys(modules[0], importer),
        "reused": keys(modules[1], modules[2]),
    }


def test_unanalyzed_modules_with_unknown_imports(
    tmp_path, cache_dir, monkeypatch
):
    importer = write(tmp_path / "importer.py", "import late\n")
    changed = write(tmp_path / "changed.py", "")
    late = write(tmp_path / "late.py", "import changed\n")
    read_file = utils.read_file

    def interrupt_late(path):
        if path == late:
            raise KeyboardInterrupt
        return read_file(path)

    monkeypatch.setattr(utils, "read_file", interrupt_late)
    with pytest.raises(KeyboardInterrupt):
        run([importer, changed, late], cache_dir)
    monkeypatch.undo()

    # The imports of late.py are unknown, so the change could affect it.
    changed.write_text("x = 1\n")
    assert run([importer, changed, late], cache_dir)._cache_stats == {
        "scanned": keys(importer, changed, late),
        "reused": set(),
    }


def test_concurrent_processes(tmp_path, cache_dir):
    directories = [tmp_path / "a", tmp_path / "b"]
    modules = [
        write(directory / f"m{i}.py", f"import os\nx{i} = {i}\n")
        for directory in directories
        for i in range(30)
    ]
    processes = [
        subprocess.Popen(
            [
                sys.executable,
                "-m",
                "vulture",
                "--cache",
                f"--cache-dir={cache_dir}",
                str(directories[i % 2]),
            ],
            cwd=REPO,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
        )
        for i in range(8)
    ]
    for process in processes:
        _, stderr = process.communicate()
        assert process.returncode == ExitCode.DeadCode
        assert stderr == b""

    check_checksum(cache_dir)
    assert set(load_cache(cache_dir)["modules"]) == keys(*modules)
    v = run(directories, cache_dir)
    assert v._cache_stats == {"scanned": set(), "reused": keys(*modules)}


def test_bundled_whitelist_change(tmp_path, cache_dir, monkeypatch):
    uses_sys = write(tmp_path / "uses_sys.py", "import sys\n")
    other = write(tmp_path / "other.py", "import os\n")
    run([tmp_path], cache_dir)
    assert "sys" in load_cache(cache_dir)["whitelists"]

    get_data = pkgutil.get_data

    def get_changed_data(package, resource):
        data = get_data(package, resource)
        if resource == str(cache.get_whitelist_path("sys")) and data:
            data += b"\nsys.argv\n"
        return data

    monkeypatch.setattr(pkgutil, "get_data", get_changed_data)
    v = run([tmp_path], cache_dir)
    assert v._cache_stats == {"scanned": keys(uses_sys), "reused": keys(other)}
    assert not run([tmp_path], cache_dir)._cache_stats["scanned"]


def test_whitelist_file_change(tmp_path, cache_dir):
    module = write(
        tmp_path / "module.py",
        """\
        def foo():
            pass

        def bar():
            pass
        """,
    )
    whitelist = write(tmp_path / "whitelist.py", "")
    other = write(tmp_path / "other.py", "")
    v = run([tmp_path], cache_dir)
    assert {item.name for item in v.unused_funcs} == {"foo", "bar"}

    whitelist.write_text("from module import bar, foo\n\nfoo\nbar\n")
    v = run([tmp_path], cache_dir)
    assert v._cache_stats == {
        "scanned": keys(module, whitelist),
        "reused": keys(other),
    }
    assert not v.unused_funcs

    whitelist.unlink()
    v = run([tmp_path], cache_dir)
    assert v._cache_stats == {"scanned": keys(module), "reused": keys(other)}
    assert {item.name for item in v.unused_funcs} == {"foo", "bar"}


@pytest.mark.parametrize(
    "contents, message",
    [
        (b"def foo(:\n", "invalid.py:1:"),
        (b"# coding: ascii\nx = '\xc3\xa4'\n", "Could not read file"),
    ],
)
def test_invalid_modules_are_not_cached(
    tmp_path, cache_dir, capsys, contents, message
):
    invalid = tmp_path / "invalid.py"
    invalid.write_bytes(contents)
    importer = write(tmp_path / "importer.py", "import invalid\n")
    expected_stats = [
        {"scanned": keys(invalid, importer), "reused": set()},
        {"scanned": keys(invalid), "reused": keys(importer)},
    ]
    for stats in expected_stats:
        v = run([tmp_path], cache_dir)
        assert v._cache_stats == stats
        assert message in capsys.readouterr().err
        assert v.exit_code == ExitCode.InvalidInput
    assert get_sections(cache_dir) == (keys(importer), keys(invalid))

    invalid.write_text("x = 1\n")
    v = run([tmp_path], cache_dir)
    assert v._cache_stats == {
        "scanned": keys(invalid, importer),
        "reused": set(),
    }
    assert v.exit_code == ExitCode.NoDeadCode


def test_same_results_as_full_scan_for_vulture_package(cache_dir):
    paths = [REPO / "vulture"]
    expected = core.Vulture()
    expected.scavenge(paths)
    first = run(paths, cache_dir)
    second = run(paths, cache_dir)
    assert not second._cache_stats["scanned"]
    assert get_results(first) == get_results(second) == get_results(expected)
    assert first.used_names == second.used_names == expected.used_names


def test_cache_disabled_by_default(project, tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    v = core.Vulture()
    v.scavenge([project])
    assert not (tmp_path / ".vulture-cache").exists()
    assert v._cache_stats == {"scanned": set(), "reused": set()}


def test_clear_cache(cache_dir):
    (cache_dir / "subdir").mkdir(parents=True)
    (cache_dir / "subdir" / "file").write_text("")
    (cache_dir / "cache.json").write_text("")
    cache.clear_cache(cache_dir)
    assert cache_dir.is_dir()
    assert not list(cache_dir.iterdir())
    cache.clear_cache(cache_dir / "missing")


def test_cli_args():
    assert _parse_args(
        ["--cache", "--cache-clear", "--cache-dir=my/cache", "path"]
    ) == {
        "cache": True,
        "cache_clear": True,
        "cache_dir": "my/cache",
        "config": "pyproject.toml",
        "paths": ["path"],
    }


def call_main(args, monkeypatch):
    monkeypatch.setattr(sys, "argv", ["vulture", *args])
    with pytest.raises(SystemExit) as exit_info:
        core.main()
    return exit_info.value.code


def test_cli(project, tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    default_dir = tmp_path / ".vulture-cache"

    assert call_main([str(project)], monkeypatch) == ExitCode.DeadCode
    assert not default_dir.exists()

    assert (
        call_main(["--cache", str(project)], monkeypatch) == ExitCode.DeadCode
    )
    assert_cache_files(default_dir)
    assert (
        call_main(["--cache", str(project)], monkeypatch) == ExitCode.DeadCode
    )

    (default_dir / "stale-file").write_text("")
    assert (
        call_main(["--cache-clear", str(project)], monkeypatch)
        == ExitCode.DeadCode
    )
    assert not list(default_dir.iterdir())

    custom_dir = tmp_path / "custom"
    (custom_dir / "stale-dir").mkdir(parents=True)
    args = [
        "--cache",
        "--cache-clear",
        f"--cache-dir={custom_dir}",
        str(project),
    ]
    assert call_main(args, monkeypatch) == ExitCode.DeadCode
    assert not (custom_dir / "stale-dir").exists()
    check_checksum(custom_dir)
