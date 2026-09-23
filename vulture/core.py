import ast
import os
import pkgutil
import re
import string
import sys
from fnmatch import fnmatch, fnmatchcase
from functools import partial
from pathlib import Path

from vulture import cache, lines, noqa, utils
from vulture.config import DEFAULTS, InputError, make_config
from vulture.reachability import Reachability
from vulture.utils import ExitCode

DEFAULT_CONFIDENCE = 60

IGNORED_VARIABLE_NAMES = {"object", "self"}
PYTEST_FUNCTION_NAMES = {
    "setup_module",
    "teardown_module",
    "setup_function",
    "teardown_function",
}
PYTEST_METHOD_NAMES = {
    "setup_class",
    "teardown_class",
    "setup_method",
    "teardown_method",
}

ERROR_CODES = {
    "attribute": "V101",
    "class": "V102",
    "function": "V103",
    "import": "V104",
    "method": "V105",
    "property": "V106",
    "variable": "V107",
    "unreachable_code": "V201",
}


def _get_unused_items(defined_items, used_names):
    unused_items = [
        item for item in set(defined_items) if item.name not in used_names
    ]
    unused_items.sort(key=lambda item: item.name.lower())
    return unused_items


def _is_special_name(name):
    return name.startswith("__") and name.endswith("__")


def _match(name, patterns, case=True):
    func = fnmatchcase if case else fnmatch
    return any(func(name, pattern) for pattern in patterns)


def _is_test_file(filename):
    return _match(
        filename.resolve(),
        ["*/test/*", "*/tests/*", "*/test*.py", "*[-_]test.py"],
        case=False,
    )


def _assigns_special_variable__all__(node):
    assert isinstance(node, ast.Assign)
    return isinstance(node.value, (ast.List, ast.Tuple)) and any(
        target.id == "__all__"
        for target in node.targets
        if isinstance(target, ast.Name)
    )


def _ignore_class(filename, class_name):
    return _is_test_file(filename) and "Test" in class_name


def _ignore_import(filename, import_name):
    """
    Ignore star-imported names since we can't detect whether they are used.
    Ignore imports from __init__.py files since they're commonly used to
    collect objects from a package.
    """
    return filename.name == "__init__.py" or import_name == "*"


def _ignore_function(filename, function_name):
    return (
        function_name in PYTEST_FUNCTION_NAMES
        or function_name.startswith("test_")
    ) and _is_test_file(filename)


def _ignore_method(filename, method_name):
    return _is_special_name(method_name) or (
        (method_name in PYTEST_METHOD_NAMES or method_name.startswith("test_"))
        and _is_test_file(filename)
    )


def _ignore_variable(filename, varname):
    """
    Ignore _ (Python idiom), _x (pylint convention) and
    __x__ (special variable or method), but not __x.
    """
    return (
        varname in IGNORED_VARIABLE_NAMES
        or (varname.startswith("_") and not varname.startswith("__"))
        or _is_special_name(varname)
    )


class Item:
    """
    Hold the name, type and location of defined code.
    """

    __slots__ = (
        "confidence",
        "filename",
        "first_lineno",
        "last_lineno",
        "message",
        "name",
        "typ",
    )

    def __init__(
        self,
        name,
        typ,
        filename,
        first_lineno,
        last_lineno,
        message="",
        confidence=DEFAULT_CONFIDENCE,
    ):
        self.name: str = name
        self.typ: str = typ
        self.filename: Path = filename
        self.first_lineno: int = first_lineno
        self.last_lineno: int = last_lineno
        self.message: str = message or f"unused {typ} '{name}'"
        self.confidence: int = confidence

    @property
    def size(self):
        assert self.last_lineno >= self.first_lineno
        return self.last_lineno - self.first_lineno + 1

    def get_report(self, add_size=False):
        if add_size:
            line_format = "line" if self.size == 1 else "lines"
            size_report = f", {self.size:d} {line_format}"
        else:
            size_report = ""
        return (
            f"{utils.format_path(self.filename)}:{self.first_lineno:d}: "
            f"{self.message} ({self.confidence}% confidence{size_report})"
        )

    def get_whitelist_string(self):
        filename = utils.format_path(self.filename)
        if self.typ == "unreachable_code":
            return f"# {self.message} ({filename}:{self.first_lineno})"
        prefix = ""
        if self.typ in ["attribute", "method", "property"]:
            prefix = "_."
        return (
            f"{prefix}{self.name}  # unused {self.typ} "
            f"({filename}:{self.first_lineno:d})"
        )

    def _tuple(self):
        return self.filename, self.first_lineno, self.name

    def __repr__(self):
        return repr(self.name)

    def __eq__(self, other):
        return self._tuple() == other._tuple()

    def __hash__(self):
        return hash(self._tuple())


class Vulture(ast.NodeVisitor):
    """Find dead code."""

    def __init__(
        self,
        verbose=False,
        ignore_names=None,
        ignore_decorators=None,
        cache_dir=None,
        cache_settings=None,
    ):
        self.verbose = verbose

        def get_list(typ):
            return utils.LoggingList(typ, self.verbose)

        self.defined_attrs = get_list("attribute")
        self.defined_classes = get_list("class")
        self.defined_funcs = get_list("function")
        self.defined_imports = get_list("import")
        self.defined_methods = get_list("method")
        self.defined_props = get_list("property")
        self.defined_vars = get_list("variable")
        self.unreachable_code = get_list("unreachable_code")

        self.used_names = utils.LoggingSet("name", self.verbose)

        self.ignore_names = ignore_names or []
        self.ignore_decorators = ignore_decorators or []

        self.filename = Path()
        self.code = []
        self.exit_code = ExitCode.NoDeadCode
        self.noqa_lines = {}

        report = partial(
            self._define,
            collection=self.unreachable_code,
            confidence=100,
        )
        self.reachability = Reachability(report=report)

        self._cache = None
        if cache_dir is not None:
            self._cache = cache.Cache(
                cache_dir,
                settings={
                    "ignore_names": self.ignore_names,
                    "ignore_decorators": self.ignore_decorators,
                    "cache_settings": cache_settings or {},
                },
            )
        self._cache_stats = {"scanned": set(), "reused": set()}
        # Only used while analyzing a single module for the cache.
        self._captured_errors = None
        self._imports = None
        self._rel_imports = None

    def _collections(self):
        return {
            collection.typ: collection
            for collection in (
                self.defined_attrs,
                self.defined_classes,
                self.defined_funcs,
                self.defined_imports,
                self.defined_methods,
                self.defined_props,
                self.defined_vars,
                self.unreachable_code,
            )
        }

    def scan(self, code, filename=""):
        filename = Path(filename)
        self.code = code.splitlines()
        self.noqa_lines = noqa.parse_noqa(self.code)
        self.filename = filename

        def handle_syntax_error(e):
            text = f' at "{e.text.strip()}"' if e.text else ""
            self._log(
                f"{utils.format_path(filename)}:{e.lineno}: {e.msg}{text}",
                file=sys.stderr,
                force=True,
            )
            self.exit_code = ExitCode.InvalidInput

        try:
            node = ast.parse(
                code, filename=str(self.filename), type_comments=True
            )
        except SyntaxError as err:
            handle_syntax_error(err)
        except ValueError as err:
            # ValueError is raised if source contains null bytes.
            self._log(
                f'{utils.format_path(filename)}: invalid source code "{err}"',
                file=sys.stderr,
                force=True,
            )
            self.exit_code = ExitCode.InvalidInput
        else:
            # When parsing type comments, visiting can throw SyntaxError.
            try:
                self.visit(node)
            except SyntaxError as err:
                handle_syntax_error(err)

        # Reset the reachability internals for every module to reduce memory
        # usage.
        self.reachability.reset()

    def scavenge(self, paths, exclude=None):
        def prepare_pattern(pattern):
            if not any(char in pattern for char in "*?["):
                pattern = f"*{pattern}*"
            return pattern

        exclude = [prepare_pattern(pattern) for pattern in (exclude or [])]

        def exclude_path(path):
            return _match(path, exclude, case=False)

        paths = [Path(path) for path in paths]

        modules = []
        for module in utils.get_modules(paths):
            if exclude_path(module):
                self._log("Excluded:", module)
            else:
                modules.append(module)

        if self._cache is None:
            for module in modules:
                self._scan_file(module)
                self._cache_stats["scanned"].add(cache.normalize_path(module))
            self._scan_whitelists(exclude_path)
            return

        self._cache.load(log=self._log)
        try:
            self._scan_modules_incrementally(modules)
            self._scan_whitelists(exclude_path)
        except KeyboardInterrupt:
            self._cache.save()
            raise
        self._cache.save()

    def _scan_file(self, module):
        self._log("Scanning:", module)
        try:
            module_string = utils.read_file(module)
        except utils.VultureInputException as err:
            self._log(
                f"Error: Could not read file {module} - {err}\n"
                f"Try to change the encoding to UTF-8.",
                file=sys.stderr,
                force=True,
            )
            self.exit_code = ExitCode.InvalidInput
        else:
            self.scan(module_string, filename=module)

    def _scan_whitelists(self, exclude_path):
        unique_imports = {item.name for item in self.defined_imports}
        for import_name in unique_imports:
            path = Path("whitelists") / (import_name + "_whitelist.py")
            if exclude_path(path):
                self._log("Excluded whitelist:", path)
            else:
                try:
                    module_data = pkgutil.get_data("vulture", str(path))
                    self._log("Included whitelist:", path)
                except OSError:
                    # Most imported modules don't have a whitelist.
                    continue
                assert module_data is not None
                module_string = module_data.decode("utf-8")
                if self._cache is None:
                    self.scan(module_string, filename=path)
                    continue
                key = path.as_posix()
                digest = cache.sha256(module_data)
                entry = self._cache.whitelists.get(key)
                if entry is not None and entry["sha256"] == digest:
                    self._merge(path, entry["result"], replay_errors=True)
                else:
                    result = self._analyze(
                        partial(self.scan, module_string, filename=path)
                    )
                    self._cache.store_whitelist(key, digest, result)
                    self._merge(path, result, replay_errors=False)

    def _scan_modules_incrementally(self, modules):
        """
        Analyze modules that changed (and modules that transitively import
        them) and reuse the cached results for all other modules.
        """
        store = self._cache
        keys = [cache.normalize_path(module) for module in modules]
        pending = dict(zip(keys, modules))
        digests = {
            key: cache.file_digest(path) for key, path in pending.items()
        }

        changed = {
            key
            for key, digest in digests.items()
            if digest is None
            or store.modules.get(key, {}).get("sha256") != digest
        }
        deleted = {
            key
            for key in store.modules
            if key not in pending and not os.path.exists(key)
        }
        old_results = {
            key: store.modules[key]["result"]
            for key in changed | deleted
            if key in store.modules
        }
        # Drop outdated entries first, so that an interrupted run never
        # leaves them behind.
        store.discard(changed | deleted)

        results = {}

        def analyze(key):
            results[key] = self._analyze(
                partial(self._scan_file, pending[key])
            )

        # Whitelist modules are analyzed first because the names they use
        # determine which other modules are affected by their changes.
        whitelist_names = set()
        for key in pending:
            if key in changed and cache.is_whitelist(key):
                analyze(key)
                whitelist_names.update(results[key]["used"])
        for key, result in old_results.items():
            if cache.is_whitelist(key):
                whitelist_names.update(result["used"])

        seeds = changed | deleted
        if whitelist_names:
            seeds |= {
                key
                for key in pending
                if key not in changed
                and whitelist_names
                & cache.defined_names(store.modules[key]["result"])
            }
        affected = cache.find_dependents(
            seeds,
            {
                key: store.modules[key]["result"]
                for key in pending
                if key not in changed
            },
            set(pending) | deleted,
        )
        store.discard(affected)

        def remember(key):
            if digests[key] is not None:
                store.store(key, digests[key], results[key])

        for key in list(results):
            remember(key)
        for key in pending:
            if key in affected and key not in results:
                analyze(key)
                remember(key)

        for key, module in zip(keys, modules):
            if key in results:
                self._merge(module, results[key], replay_errors=False)
            else:
                self._log("Using cached result:", module)
                self._merge(
                    module, store.modules[key]["result"], replay_errors=True
                )
        self._cache_stats["scanned"].update(results)
        self._cache_stats["reused"].update(set(pending) - set(results))

    def _analyze(self, scan_module):
        """
        Call scan_module() and return its results in a cacheable format.

        The defined items and used names are removed from this instance
        again, use _merge() to add them.
        """
        collections = self._collections()
        sizes = {typ: len(items) for typ, items in collections.items()}
        used_names = self.used_names
        self.used_names = utils.LoggingSet("name", self.verbose)
        self._captured_errors = []
        self._imports = set()
        self._rel_imports = set()
        try:
            scan_module()
            return {
                "defined": {
                    typ: [
                        [
                            item.name,
                            item.first_lineno,
                            item.last_lineno,
                            item.message,
                            item.confidence,
                        ]
                        for item in items[sizes[typ] :]
                    ]
                    for typ, items in collections.items()
                    if len(items) > sizes[typ]
                },
                "used": sorted(self.used_names),
                "errors": self._captured_errors,
                "imports": sorted(self._imports),
                "rel_imports": sorted(self._rel_imports),
            }
        finally:
            for typ, items in collections.items():
                del items[sizes[typ] :]
            self.used_names = used_names
            self._captured_errors = None
            self._imports = None
            self._rel_imports = None

    def _merge(self, filename, result, replay_errors):
        collections = self._collections()
        for typ, items in result["defined"].items():
            collections[typ].extend(
                Item(
                    name,
                    typ,
                    filename,
                    first_lineno,
                    last_lineno,
                    message=message,
                    confidence=confidence,
                )
                for name, first_lineno, last_lineno, message, confidence in (
                    items
                )
            )
        self.used_names.update(result["used"])
        if result["errors"]:
            if replay_errors:
                for message in result["errors"]:
                    self._log(message, file=sys.stderr, force=True)
            self.exit_code = ExitCode.InvalidInput

    def _record_import(self, node):
        if self._imports is None:
            return
        if isinstance(node, ast.Import):
            self._imports.update(alias.name for alias in node.names)
            return
        names = [alias.name for alias in node.names if alias.name != "*"]
        if not node.level:
            self._imports.add(node.module)
            self._imports.update(f"{node.module}.{name}" for name in names)
            return
        parents = self.filename.resolve().parents
        if node.level > len(parents):
            return
        package = parents[node.level - 1]
        base = package.joinpath(*(node.module or "").split("."))
        targets = [base] + [base / name for name in names]
        self._rel_imports.add(cache.normalize_path(package / "__init__.py"))
        for target in targets:
            self._rel_imports.add(
                cache.normalize_path(target.with_name(target.name + ".py"))
            )
            self._rel_imports.add(cache.normalize_path(target / "__init__.py"))

    def get_unused_code(
        self, min_confidence=0, sort_by_size=False
    ) -> list[Item]:
        """
        Return ordered list of unused Item objects.
        """
        if not 0 <= min_confidence <= 100:
            raise ValueError("min_confidence must be between 0 and 100.")

        def by_name(item):
            return str(item.filename).lower(), item.first_lineno

        def by_size(item):
            return item.size, *by_name(item)

        unused_code = (
            self.unused_attrs
            + self.unused_classes
            + self.unused_funcs
            + self.unused_imports
            + self.unused_methods
            + self.unused_props
            + self.unused_vars
            + self.unreachable_code
        )

        confidently_unused = [
            obj for obj in unused_code if obj.confidence >= min_confidence
        ]

        return sorted(
            confidently_unused, key=by_size if sort_by_size else by_name
        )

    def report(
        self, min_confidence=0, sort_by_size=False, make_whitelist=False
    ):
        """
        Print ordered list of Item objects to stdout.
        """
        for item in self.get_unused_code(
            min_confidence=min_confidence, sort_by_size=sort_by_size
        ):
            self._log(
                item.get_whitelist_string()
                if make_whitelist
                else item.get_report(add_size=sort_by_size),
                force=True,
            )
            self.exit_code = ExitCode.DeadCode
        return self.exit_code

    @property
    def unused_classes(self):
        return _get_unused_items(self.defined_classes, self.used_names)

    @property
    def unused_funcs(self):
        return _get_unused_items(self.defined_funcs, self.used_names)

    @property
    def unused_imports(self):
        return _get_unused_items(self.defined_imports, self.used_names)

    @property
    def unused_methods(self):
        return _get_unused_items(self.defined_methods, self.used_names)

    @property
    def unused_props(self):
        return _get_unused_items(self.defined_props, self.used_names)

    @property
    def unused_vars(self):
        return _get_unused_items(self.defined_vars, self.used_names)

    @property
    def unused_attrs(self):
        return _get_unused_items(self.defined_attrs, self.used_names)

    def _log(self, *args, file=None, force=False):
        if force and file is sys.stderr and self._captured_errors is not None:
            self._captured_errors.append(" ".join(map(str, args)))
        if self.verbose or force:
            file = file or sys.stdout
            try:
                print(*args, file=file)
            except UnicodeEncodeError:
                # Some terminals can't print Unicode symbols.
                x = " ".join(map(str, args))
                print(x.encode(), file=file)

    def _add_aliases(self, node):
        """
        We delegate to this method instead of using visit_alias() to have
        access to line numbers and to filter imports from __future__.
        """
        assert isinstance(node, (ast.Import, ast.ImportFrom))
        for name_and_alias in node.names:
            # Store only top-level module name ("os.path" -> "os").
            # We can't easily detect when "os.path" is used.
            name = name_and_alias.name.partition(".")[0]
            alias = name_and_alias.asname
            self._define(
                self.defined_imports,
                alias or name,
                node,
                confidence=90,
                ignore=_ignore_import,
            )
            if alias is not None:
                self.used_names.add(name_and_alias.name)

    def _define(
        self,
        collection,
        name,
        first_node,
        last_node=None,
        message="",
        confidence=DEFAULT_CONFIDENCE,
        ignore=None,
    ):
        def ignored(lineno):
            return (
                (ignore and ignore(self.filename, name))
                or _match(name, self.ignore_names)
                or noqa.ignore_line(self.noqa_lines, lineno, ERROR_CODES[typ])
            )

        last_node = last_node or first_node
        typ = collection.typ
        first_lineno = lines.get_first_line_number(first_node)

        if ignored(first_lineno):
            self._log(f'Ignoring {typ} "{name}"')
        else:
            collection.append(
                Item(
                    name,
                    typ,
                    self.filename,
                    first_lineno,
                    lines.get_last_line_number(last_node),
                    message=message,
                    confidence=confidence,
                )
            )

    def _define_variable(self, name, node, confidence=DEFAULT_CONFIDENCE):
        self._define(
            self.defined_vars,
            name,
            node,
            confidence=confidence,
            ignore=_ignore_variable,
        )

    def visit_arg(self, node):
        """Function argument"""
        self._define_variable(node.arg, node, confidence=100)

    def visit_AsyncFunctionDef(self, node):
        return self.visit_FunctionDef(node)

    def visit_Attribute(self, node):
        if isinstance(node.ctx, ast.Store):
            self._define(self.defined_attrs, node.attr, node)
        elif isinstance(node.ctx, ast.Load):
            self.used_names.add(node.attr)

    def visit_BinOp(self, node):
        """
        Parse variable names in old format strings:

        "%(my_var)s" % locals()
        """
        if (
            utils.is_ast_string(node.left)
            and isinstance(node.op, ast.Mod)
            and self._is_locals_call(node.right)
        ):
            self.used_names |= set(re.findall(r"%\((\w+)\)", node.left.value))

    def visit_Call(self, node):
        # Count getattr/hasattr(x, "some_attr", ...) as usage of some_attr.
        if isinstance(node.func, ast.Name) and (
            (node.func.id == "getattr" and 2 <= len(node.args) <= 3)
            or (node.func.id == "hasattr" and len(node.args) == 2)
        ):
            attr_name_arg = node.args[1]
            if utils.is_ast_string(attr_name_arg):
                self.used_names.add(attr_name_arg.value)

        # Parse variable names in new format strings:
        # "{my_var}".format(**locals())
        if (
            isinstance(node.func, ast.Attribute)
            and utils.is_ast_string(node.func.value)
            and node.func.attr == "format"
            and any(
                kw.arg is None and self._is_locals_call(kw.value)
                for kw in node.keywords
            )
        ):
            self._handle_new_format_string(node.func.value.value)

    def _handle_new_format_string(self, s):
        def is_identifier(name):
            return bool(re.match(r"[a-zA-Z_][a-zA-Z0-9_]*", name))

        parser = string.Formatter()
        try:
            names = [name for _, name, _, _ in parser.parse(s) if name]
        except ValueError:
            # Invalid format string.
            names = []

        for field_name in names:
            # Remove brackets and their contents: "a[0][b].c[d].e" -> "a.c.e",
            # then split the resulting string: "a.b.c" -> ["a", "b", "c"]
            vars = re.sub(r"\[\w*\]", "", field_name).split(".")
            for var in vars:
                if is_identifier(var):
                    self.used_names.add(var)

    @staticmethod
    def _is_locals_call(node):
        """Return True if the node is `locals()`."""
        return (
            isinstance(node, ast.Call)
            and isinstance(node.func, ast.Name)
            and node.func.id == "locals"
            and not node.args
            and not node.keywords
        )

    def visit_ClassDef(self, node):
        for decorator in node.decorator_list:
            if _match(
                utils.get_decorator_name(decorator), self.ignore_decorators
            ):
                self._log(
                    f'Ignoring class "{node.name}" (decorator whitelisted)'
                )
                break
        else:
            self._define(
                self.defined_classes, node.name, node, ignore=_ignore_class
            )

    def visit_FunctionDef(self, node):
        decorator_names = [
            utils.get_decorator_name(decorator)
            for decorator in node.decorator_list
        ]

        first_arg = node.args.args[0].arg if node.args.args else None

        if "@property" in decorator_names:
            typ = "property"
        elif (
            "@staticmethod" in decorator_names
            or "@classmethod" in decorator_names
            or first_arg == "self"
        ):
            typ = "method"
        else:
            typ = "function"

        if any(
            _match(name, self.ignore_decorators) for name in decorator_names
        ):
            self._log(f'Ignoring {typ} "{node.name}" (decorator whitelisted)')
        elif typ == "property":
            self._define(self.defined_props, node.name, node)
        elif typ == "method":
            self._define(
                self.defined_methods, node.name, node, ignore=_ignore_method
            )
        else:
            self._define(
                self.defined_funcs, node.name, node, ignore=_ignore_function
            )

    def visit_Import(self, node):
        self._add_aliases(node)
        self._record_import(node)

    def visit_ImportFrom(self, node):
        if node.module != "__future__":
            self._add_aliases(node)
            self._record_import(node)

    def visit_Name(self, node):
        if (
            isinstance(node.ctx, (ast.Load, ast.Del))
            and node.id not in IGNORED_VARIABLE_NAMES
        ):
            self.used_names.add(node.id)
        elif isinstance(node.ctx, (ast.Param, ast.Store)):
            self._define_variable(node.id, node)

    def visit_Assign(self, node):
        if _assigns_special_variable__all__(node):
            assert isinstance(node.value, (ast.List, ast.Tuple))
            for elt in node.value.elts:
                if utils.is_ast_string(elt):
                    self.used_names.add(elt.value)

    def visit_MatchClass(self, node):
        for kwd_attr in node.kwd_attrs:
            self.used_names.add(kwd_attr)

    def visit(self, node):
        # Visit children nodes first to allow recursive reachability analysis.
        self.generic_visit(node)

        self.reachability.visit(node)

        method = "visit_" + node.__class__.__name__
        visitor = getattr(self, method, None)
        if self.verbose:
            lineno = getattr(node, "lineno", 1)
            line = self.code[lineno - 1] if self.code else ""
            self._log(lineno, ast.dump(node), line)
        if visitor:
            visitor(node)

        # There isn't a clean subset of node types that might have type
        # comments, so just check all of them.
        type_comment = getattr(node, "type_comment", None)
        if type_comment is not None:
            mode = (
                "func_type"
                if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))
                else "eval"
            )
            self.visit(
                ast.parse(type_comment, filename="<type_comment>", mode=mode)
            )

    def generic_visit(self, node):
        """Called if no explicit visitor function exists for a node."""
        for _, value in ast.iter_fields(node):
            if isinstance(value, list):
                for item in value:
                    if isinstance(item, ast.AST):
                        self.visit(item)
            elif isinstance(value, ast.AST):
                self.visit(value)


def main():
    try:
        config = make_config()
    except InputError as e:
        print(e, file=sys.stderr)
        sys.exit(ExitCode.InvalidCmdlineArguments)

    use_cache = (
        config["cache"]
        or config["cache_clear"]
        or config["cache_dir"] != DEFAULTS["cache_dir"]
    )
    if config["cache_clear"]:
        cache.clear_cache(config["cache_dir"])

    vulture = Vulture(
        verbose=config["verbose"],
        ignore_names=config["ignore_names"],
        ignore_decorators=config["ignore_decorators"],
        cache_dir=config["cache_dir"] if use_cache else None,
    )
    vulture.scavenge(config["paths"], exclude=config["exclude"])
    sys.exit(
        vulture.report(
            min_confidence=config["min_confidence"],
            sort_by_size=config["sort_by_size"],
            make_whitelist=config["make_whitelist"],
        )
    )
