import ast
import pkgutil
import re
import string
import sys
from fnmatch import fnmatch, fnmatchcase
from functools import partial
from pathlib import Path

from vulture import cache as analysis_cache
from vulture import lines, noqa, utils
from vulture.config import InputError, make_config
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

_ITEM_COLLECTIONS = (
    "defined_attrs",
    "defined_classes",
    "defined_funcs",
    "defined_imports",
    "defined_methods",
    "defined_props",
    "defined_vars",
    "unreachable_code",
)

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


def _item_to_dict(item):
    return {
        "confidence": item.confidence,
        "filename": str(item.filename),
        "first_lineno": item.first_lineno,
        "last_lineno": item.last_lineno,
        "message": item.message,
        "name": item.name,
        "typ": item.typ,
    }


def _item_from_dict(data):
    return Item(
        data["name"],
        data["typ"],
        Path(data["filename"]),
        data["first_lineno"],
        data["last_lineno"],
        message=data.get("message", ""),
        confidence=data.get("confidence", DEFAULT_CONFIDENCE),
    )


def _empty_cache_entry(digest, errors=None):
    return {
        "sha256": digest,
        "imports": [],
        "invalid_input": True,
        "errors": list(errors or []),
        "used_names": [],
        "items": {name: [] for name in _ITEM_COLLECTIONS},
    }


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
        self.module_imports = []
        self._scan_invalid = False
        self._scan_errors = []
        self._cache_stats = {"scanned": set(), "reused": set()}
        self.cache_dir = cache_dir
        self.cache_settings = cache_settings
        self._cache = (
            None
            if cache_dir is None
            else analysis_cache.AnalysisCache(cache_dir, cache_settings)
        )

        report = partial(
            self._define,
            collection=self.unreachable_code,
            confidence=100,
        )
        self.reachability = Reachability(report=report)

    def scan(self, code, filename=""):
        filename = Path(filename)
        self.code = code.splitlines()
        self.noqa_lines = noqa.parse_noqa(self.code)
        self.filename = filename
        self.module_imports = []
        self._scan_invalid = False
        self._scan_errors = []

        def handle_syntax_error(e):
            text = f' at "{e.text.strip()}"' if e.text else ""
            message = (
                f"{utils.format_path(filename)}:{e.lineno}: {e.msg}{text}"
            )
            self._record_scan_error(message)

        try:
            node = ast.parse(
                code, filename=str(self.filename), type_comments=True
            )
        except SyntaxError as err:
            handle_syntax_error(err)
        except ValueError as err:
            # ValueError is raised if source contains null bytes.
            self._record_scan_error(
                f'{utils.format_path(filename)}: invalid source code "{err}"'
            )
        else:
            # When parsing type comments, visiting can throw SyntaxError.
            try:
                self.visit(node)
            except SyntaxError as err:
                handle_syntax_error(err)

        # Reset the reachability internals for every module to reduce memory
        # usage.
        self.reachability.reset()

    def _record_scan_error(self, message):
        self._scan_errors.append(message)
        self._scan_invalid = True
        self._log(message, file=sys.stderr, force=True)
        self.exit_code = ExitCode.InvalidInput

    def _note_import(self, module_name):
        if not module_name or module_name == "__future__":
            return
        parts = module_name.split(".")
        for index in range(1, len(parts) + 1):
            name = ".".join(parts[:index])
            if name not in self.module_imports:
                self.module_imports.append(name)

    def _resolve_relative_import(self, module, level):
        path = self.filename
        if not isinstance(path, Path) or path == Path() or not path.name:
            return None
        parts = []
        current = path.parent
        while (current / "__init__.py").is_file():
            parts.append(current.name)
            current = current.parent
        parts.reverse()
        if level - 1 > len(parts):
            return None
        if level > 1:
            parts = parts[: len(parts) - (level - 1)]
        if module:
            parts.extend(module.split("."))
        if not parts:
            return None
        return ".".join(parts)

    def scavenge(self, paths, exclude=None):
        def prepare_pattern(pattern):
            if not any(char in pattern for char in "*?["):
                pattern = f"*{pattern}*"
            return pattern

        exclude = [prepare_pattern(pattern) for pattern in (exclude or [])]

        def exclude_path(path):
            return _match(path, exclude, case=False)

        paths = [Path(path) for path in paths]
        selected = []
        for module in utils.get_modules(paths):
            if exclude_path(module):
                self._log("Excluded:", module)
                continue
            selected.append(module)

        self._cache_stats = {"scanned": set(), "reused": set()}
        to_scan = list(selected)
        to_reuse = []
        if self._cache is not None:
            self._cache.load()
            if not self._cache.force_full:
                roots = analysis_cache.search_roots(paths)
                to_scan, to_reuse = analysis_cache.select_for_analysis(
                    selected,
                    roots,
                    self._cache.modules,
                    self._cache.changed_whitelists(),
                )

        for module in to_reuse:
            entry = self._cache.modules[analysis_cache.normalize_path(module)]
            self._restore_cache_entry(entry)
            self._cache_stats["reused"].add(
                analysis_cache.normalize_path(module)
            )
            self._log("Cached:", module)

        try:
            for module in to_scan:
                entry = self._analyze_module(module)
                if self._cache is not None and entry is not None:
                    self._cache.update_module(module, entry)
                self._cache_stats["scanned"].add(
                    analysis_cache.normalize_path(module)
                )
            self._scan_whitelists(exclude_path)
        except KeyboardInterrupt:
            if self._cache is not None:
                try:
                    self._cache.save()
                except Exception as exc:
                    self._log(
                        f"Warning: failed to save cache: {exc}",
                        file=sys.stderr,
                        force=True,
                    )
            raise

        if self._cache is not None:
            self._cache.save()

    def _analyze_module(self, module):
        """Scan *module* and return a JSON-ready cache entry."""
        self._log("Scanning:", module)
        try:
            module_string = utils.read_file(module)
            digest = analysis_cache.hash_file(module)
        except utils.VultureInputException as err:
            message = (
                f"Error: Could not read file {module} - {err}\n"
                f"Try to change the encoding to UTF-8."
            )
            self._log(message, file=sys.stderr, force=True)
            self.exit_code = ExitCode.InvalidInput
            try:
                digest = analysis_cache.hash_file(module)
            except OSError:
                digest = ""
            return _empty_cache_entry(digest, errors=[message])

        return self._scan_isolated(module_string, module, digest)

    def _scan_isolated(self, module_string, module, digest):
        """Scan one module without mixing its used names into other files."""
        previous_used = self.used_names
        local_used = utils.LoggingSet("name", self.verbose)
        self.used_names = local_used
        lengths = {
            name: len(getattr(self, name)) for name in _ITEM_COLLECTIONS
        }
        try:
            self.scan(module_string, filename=module)
            imports = list(self.module_imports)
            items = {}
            for name in _ITEM_COLLECTIONS:
                collection = getattr(self, name)
                items[name] = [
                    _item_to_dict(item) for item in collection[lengths[name] :]
                ]
            return {
                "sha256": digest,
                "imports": sorted(set(imports)),
                "invalid_input": self._scan_invalid,
                "errors": list(self._scan_errors),
                "used_names": sorted(local_used),
                "items": items,
            }
        finally:
            previous_used.update(local_used)
            self.used_names = previous_used

    def _restore_cache_entry(self, entry):
        for message in entry.get("errors") or []:
            self._log(message, file=sys.stderr, force=True)
        if entry.get("invalid_input"):
            self.exit_code = ExitCode.InvalidInput
        items = entry.get("items") or {}
        for name in _ITEM_COLLECTIONS:
            collection = getattr(self, name)
            for data in items.get(name) or []:
                collection.append(_item_from_dict(data))
        for name in entry.get("used_names") or []:
            self.used_names.add(name)

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
                self.scan(module_string, filename=path)

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
        for alias in node.names:
            self._note_import(alias.name)

    def visit_ImportFrom(self, node):
        if node.module != "__future__":
            self._add_aliases(node)
        if node.level:
            absolute = self._resolve_relative_import(node.module, node.level)
            if absolute:
                self._note_import(absolute)
                for alias in node.names:
                    if alias.name != "*":
                        self._note_import(f"{absolute}.{alias.name}")
        elif node.module and node.module != "__future__":
            self._note_import(node.module)
            for alias in node.names:
                if alias.name != "*":
                    self._note_import(f"{node.module}.{alias.name}")

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

    cache_dir = None
    if config["cache"] or config["cache_clear"]:
        cache_dir = config["cache_dir"]
        if config["cache_clear"]:
            analysis_cache.clear_cache(cache_dir)
    vulture = Vulture(
        verbose=config["verbose"],
        ignore_names=config["ignore_names"],
        ignore_decorators=config["ignore_decorators"],
        cache_dir=cache_dir,
        cache_settings={
            "ignore_decorators": config["ignore_decorators"],
            "ignore_names": config["ignore_names"],
        },
    )
    vulture.scavenge(config["paths"], exclude=config["exclude"])
    sys.exit(
        vulture.report(
            min_confidence=config["min_confidence"],
            sort_by_size=config["sort_by_size"],
            make_whitelist=config["make_whitelist"],
        )
    )
