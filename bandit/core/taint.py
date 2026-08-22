# SPDX-License-Identifier: Apache-2.0
"""Intra-file taint tracking for user-controlled data flowing into sinks."""
import ast

from bandit.core import utils

SOURCE_REQUEST_ATTRS = frozenset({"args", "form", "cookies"})
SANITIZERS = frozenset(
    {
        "int",
        "shlex.quote",
        "os.path.basename",
        "flask.escape",
        "markupsafe.escape",
    }
)
SQL_SINK_NAMES = frozenset({"execute", "executemany"})
SHELL_OS_SINKS = frozenset({"os.system", "os.popen"})
SHELL_SUBPROCESS_SINKS = frozenset(
    {"subprocess.call", "subprocess.run", "subprocess.Popen"}
)
SSRF_SINKS = frozenset(
    {"requests.get", "requests.post", "urllib.request.urlopen"}
)
XSS_RENDER = "render_template_string"
XSS_MARKUP = "markupsafe.Markup"
XSS_RESPONSE = "make_response"


def _qualname(node, aliases):
    if isinstance(node, ast.Name):
        return aliases.get(node.id, node.id)
    if isinstance(node, ast.Attribute):
        base = _qualname(node.value, aliases)
        if not base:
            return ""
        name = f"{base}.{node.attr}"
        return aliases.get(name, name)
    return ""


def _const_true(node):
    return isinstance(node, ast.Constant) and node.value is True


def _has_shell_true(call):
    for kw in call.keywords:
        if kw.arg == "shell" and _const_true(kw.value):
            return True
    return False


def _target_names(target):
    names = []
    if isinstance(target, ast.Name):
        names.append(target.id)
    elif isinstance(target, ast.Tuple):
        for elt in target.elts:
            names.extend(_target_names(elt))
    elif isinstance(target, ast.List):
        for elt in target.elts:
            names.extend(_target_names(elt))
    elif isinstance(target, ast.Starred):
        names.extend(_target_names(target.value))
    return names


class _Func:
    def __init__(self, node, parent, qual):
        self.node = node
        self.parent = parent
        self.qual = qual
        self.name = node.name
        params = []
        args = node.args
        for a in list(args.posonlyargs) + list(args.args):
            params.append(a.arg)
        if args.vararg:
            params.append(args.vararg.arg)
        for a in args.kwonlyargs:
            params.append(a.arg)
        if args.kwarg:
            params.append(args.kwarg.arg)
        self.params = params
        self.globals = set()
        self.nonlocals = set()
        for child in ast.walk(node):
            if isinstance(child, ast.Global):
                self.globals.update(child.names)
            elif isinstance(child, ast.Nonlocal):
                self.nonlocals.update(child.names)


class TaintAnalyzer:
    def __init__(self, source):
        self.tree = ast.parse(source)
        self.aliases = {}
        self.funcs = []
        self.funcs_by_node = {}
        self.tainted = {}  # scope key -> set of names
        self.returns_tainted = set()
        self.alias_objects = {}  # scope, name -> qualname
        self.findings = {}  # test_id -> set of (lineno, col_offset)

    def run(self):
        self._collect_imports(self.tree)
        self._collect_funcs(self.tree, None, "")
        self.tainted[None] = set()
        for fn in self.funcs:
            self.tainted[fn.node] = set()
        for _ in range(24):
            before = self._state()
            self._visit_body(self.tree.body, None)
            if self._state() == before:
                break
        return self.findings

    def _state(self):
        return (
            tuple(sorted((id(k) if k is not None else 0, tuple(sorted(v)))
                         for k, v in self.tainted.items())),
            tuple(sorted(id(x) for x in self.returns_tainted)),
        )

    def _collect_imports(self, tree):
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                for alias in node.names:
                    name = alias.asname or alias.name.split(".")[0]
                    self.aliases[alias.asname or alias.name] = alias.name
                    if alias.asname:
                        self.aliases[alias.asname] = alias.name
                    else:
                        self.aliases[name] = alias.name.split(".")[0]
            elif isinstance(node, ast.ImportFrom) and node.module:
                for alias in node.names:
                    local = alias.asname or alias.name
                    self.aliases[local] = f"{node.module}.{alias.name}"

    def _collect_funcs(self, node, parent, prefix):
        body = getattr(node, "body", None)
        if body is None:
            return
        for child in body:
            if isinstance(child, (ast.FunctionDef, ast.AsyncFunctionDef)):
                qual = f"{prefix}.{child.name}" if prefix else child.name
                fn = _Func(child, parent, qual)
                self.funcs.append(fn)
                self.funcs_by_node[child] = fn
                self._collect_funcs(child, fn, qual)
            elif isinstance(child, ast.ClassDef):
                cqual = f"{prefix}.{child.name}" if prefix else child.name
                self._collect_funcs(child, parent, cqual)

    def _scope_lookup(self, name, scope):
        cur = scope
        while True:
            names = self.tainted.get(cur, set())
            if name in names:
                return True
            if cur is None:
                return False
            fn = self.funcs_by_node.get(cur)
            cur = fn.parent.node if fn and fn.parent else None

    def _mark(self, name, scope, tainted):
        if not name:
            return
        target = scope
        fn = self.funcs_by_node.get(scope) if scope is not None else None
        if fn:
            if name in fn.globals:
                target = None
            elif name in fn.nonlocals and fn.parent:
                target = fn.parent.node
        bucket = self.tainted.setdefault(target, set())
        if tainted:
            bucket.add(name)
        elif name in bucket and target == scope and not (
            fn and (name in fn.globals or name in fn.nonlocals)
        ):
            # Do not un-taint across iterations; once tainted stays tainted.
            pass

    def _bind_object(self, name, scope, qual):
        self.alias_objects[(scope, name)] = qual

    def _object_qual(self, name, scope):
        cur = scope
        while True:
            q = self.alias_objects.get((cur, name))
            if q:
                return q
            if cur is None:
                return self.aliases.get(name, name)
            fn = self.funcs_by_node.get(cur)
            cur = fn.parent.node if fn and fn.parent else None

    def _expr_qual(self, node, scope):
        if isinstance(node, ast.Name):
            return self._object_qual(node.id, scope)
        if isinstance(node, ast.Attribute):
            base = self._expr_qual(node.value, scope)
            if not base:
                return ""
            return f"{base}.{node.attr}"
        return _qualname(node, self.aliases)

    def _is_source_qual(self, qual):
        if not qual:
            return False
        if qual in {"sys.argv", "os.environ"}:
            return True
        parts = qual.split(".")
        if parts[-1] in SOURCE_REQUEST_ATTRS and "request" in parts:
            return True
        return False

    def _is_source_expr(self, node, scope):
        if isinstance(node, ast.Call):
            q = self._call_qual(node, scope)
            if q == "input" or q.endswith(".input"):
                return True
            if isinstance(node.func, ast.Attribute) and node.func.attr == "get":
                recv = node.func.value
                if self._is_source_expr(recv, scope):
                    return True
                rq = self._expr_qual(recv, scope)
                if self._is_source_qual(rq):
                    return True
            return False
        if isinstance(node, ast.Subscript):
            return self._is_source_expr(node.value, scope) or self._is_source_qual(
                self._expr_qual(node.value, scope)
            )
        if isinstance(node, ast.Attribute):
            return self._is_source_qual(self._expr_qual(node, scope))
        if isinstance(node, ast.Name):
            return self._is_source_qual(self._object_qual(node.id, scope))
        return False

    def _call_qual(self, call, scope):
        if isinstance(call.func, ast.Name):
            local = call.func.id
            mapped = self._object_qual(local, scope)
            return mapped
        if isinstance(call.func, ast.Attribute):
            return self._expr_qual(call.func, scope)
        return utils.get_call_name(call, self.aliases)

    def is_tainted(self, node, scope):
        if node is None:
            return False
        if self._is_source_expr(node, scope):
            return True
        if isinstance(node, ast.Name):
            return self._scope_lookup(node.id, scope)
        if isinstance(node, ast.NamedExpr):
            tainted = self.is_tainted(node.value, scope)
            for name in _target_names(node.target):
                self._mark(name, scope, tainted)
            return tainted
        if isinstance(node, ast.BinOp):
            if isinstance(node.op, (ast.Add, ast.Mod)):
                return self.is_tainted(node.left, scope) or self.is_tainted(
                    node.right, scope
                )
            return False
        if isinstance(node, ast.JoinedStr):
            return any(self.is_tainted(v, scope) for v in node.values)
        if isinstance(node, ast.FormattedValue):
            return self.is_tainted(node.value, scope)
        if isinstance(node, ast.Call):
            return self._call_tainted(node, scope)
        if isinstance(node, ast.Attribute):
            if self.is_tainted(node.value, scope):
                return True
            return self._is_source_qual(self._expr_qual(node, scope))
        if isinstance(node, ast.Subscript):
            return self.is_tainted(node.value, scope) or self.is_tainted(
                node.slice, scope
            )
        if isinstance(node, ast.Slice):
            return any(
                self.is_tainted(p, scope)
                for p in (node.lower, node.upper, node.step)
            )
        if isinstance(node, (ast.List, ast.Tuple, ast.Set)):
            return any(self.is_tainted(e, scope) for e in node.elts)
        if isinstance(node, ast.Dict):
            return any(
                self.is_tainted(x, scope)
                for x in list(node.keys) + list(node.values)
                if x is not None
            )
        if isinstance(node, ast.IfExp):
            return self.is_tainted(node.body, scope) or self.is_tainted(
                node.orelse, scope
            )
        if isinstance(node, ast.UnaryOp):
            return self.is_tainted(node.operand, scope)
        if isinstance(node, ast.BoolOp):
            return any(self.is_tainted(v, scope) for v in node.values)
        if isinstance(node, ast.Compare):
            return self.is_tainted(node.left, scope) or any(
                self.is_tainted(c, scope) for c in node.comparators
            )
        if isinstance(node, ast.Starred):
            return self.is_tainted(node.value, scope)
        if isinstance(node, ast.Await):
            return self.is_tainted(node.value, scope)
        if isinstance(node, ast.Yield) or isinstance(node, ast.YieldFrom):
            return self.is_tainted(node.value, scope)
        if isinstance(node, ast.Lambda):
            return self.is_tainted(node.body, scope)
        return False

    def _match_func(self, call, scope):
        if isinstance(call.func, ast.Name):
            name = call.func.id
            cur = scope
            while True:
                # search sibling functions in this scope
                parent_body = None
                if cur is None:
                    parent_body = self.tree.body
                else:
                    parent_body = self.funcs_by_node[cur].node.body
                for stmt in parent_body:
                    if (
                        isinstance(stmt, (ast.FunctionDef, ast.AsyncFunctionDef))
                        and stmt.name == name
                    ):
                        return self.funcs_by_node.get(stmt)
                if cur is None:
                    break
                fn = self.funcs_by_node.get(cur)
                cur = fn.parent.node if fn and fn.parent else None
            for fn in self.funcs:
                if fn.name == name:
                    return fn
        return None

    def _call_tainted(self, call, scope):
        qual = self._call_qual(call, scope)
        args_tainted = []
        for arg in call.args:
            args_tainted.append(self.is_tainted(arg, scope))
        kw_tainted = []
        for kw in call.keywords:
            kw_tainted.append(self.is_tainted(kw.value, scope))
        any_tainted = any(args_tainted) or any(kw_tainted)

        if qual in SANITIZERS or qual == "int" or qual == "builtins.int":
            return False

        self._record_sinks(call, scope, qual)

        callee = self._match_func(call, scope)
        if callee:
            for i, arg in enumerate(call.args):
                if i < len(callee.params) and args_tainted[i]:
                    self._mark(callee.params[i], callee.node, True)
            used = set()
            for kw in call.keywords:
                if kw.arg and kw.arg in callee.params:
                    used.add(kw.arg)
                    if self.is_tainted(kw.value, scope):
                        self._mark(kw.arg, callee.node, True)
            if callee.node in self.returns_tainted:
                return True
            return False

        if isinstance(call.func, ast.Attribute) and call.func.attr == "format":
            return self.is_tainted(call.func.value, scope) or any_tainted

        return any_tainted

    def _sink_arg_tainted(self, call, scope, index=0, keywords=()):
        if index < len(call.args) and self.is_tainted(call.args[index], scope):
            return True
        for kw in call.keywords:
            if kw.arg in keywords and self.is_tainted(kw.value, scope):
                return True
        return False

    def _add_finding(self, test_id, call):
        self.findings.setdefault(test_id, set()).add(
            (call.lineno, call.col_offset)
        )

    def _record_sinks(self, call, scope, qual):
        simple = qual.split(".")[-1]

        # B620 SQL
        if simple in SQL_SINK_NAMES:
            if self._sink_arg_tainted(
                call, scope, 0, ("query", "sql", "operation")
            ):
                self._add_finding("B620", call)

        # B621 shell
        if qual in SHELL_OS_SINKS or (
            simple in {"system", "popen"}
            and (qual.endswith(".system") or qual.endswith(".popen"))
        ):
            if self._sink_arg_tainted(call, scope, 0, ("command", "cmd")):
                self._add_finding("B621", call)
        if qual in SHELL_SUBPROCESS_SINKS:
            if _has_shell_true(call) and self._sink_arg_tainted(
                call, scope, 0, ("args", "cmd", "command")
            ):
                self._add_finding("B621", call)

        # B622 path: builtin open only (unqualified)
        if qual == "open":
            if self._sink_arg_tainted(call, scope, 0, ("file", "file=")):
                self._add_finding("B622", call)

        # B623 SSRF
        if qual in SSRF_SINKS or (
            simple in {"get", "post", "urlopen"}
            and (
                qual.endswith("requests.get")
                or qual.endswith("requests.post")
                or qual.endswith("urllib.request.urlopen")
                or qual == "urllib.request.urlopen"
            )
        ):
            if self._sink_arg_tainted(call, scope, 0, ("url",)):
                self._add_finding("B623", call)

        # B624 XSS
        if simple == "render_template_string" or qual.endswith(
            "render_template_string"
        ):
            if self._sink_arg_tainted(
                call, scope, 0, ("source", "template")
            ):
                self._add_finding("B624", call)
        if qual == XSS_MARKUP:
            if self._sink_arg_tainted(call, scope, 0, ()):
                self._add_finding("B624", call)
        if simple == XSS_RESPONSE or qual.endswith(".make_response"):
            if self._sink_arg_tainted(call, scope, 0, ()):
                self._add_finding("B624", call)

    def _assign_from(self, target, value, scope):
        tainted = self.is_tainted(value, scope)
        if isinstance(value, ast.Name):
            q = self._object_qual(value.id, scope)
            for name in _target_names(target):
                self._bind_object(name, scope, q)
        elif isinstance(value, ast.Attribute):
            q = self._expr_qual(value, scope)
            for name in _target_names(target):
                self._bind_object(name, scope, q)
        for name in _target_names(target):
            self._mark(name, scope, tainted)

    def _visit_stmt(self, stmt, scope):
        if isinstance(stmt, (ast.FunctionDef, ast.AsyncFunctionDef)):
            fn = self.funcs_by_node.get(stmt)
            self._visit_body(stmt.body, stmt)
            for node in ast.walk(stmt):
                if isinstance(node, ast.Return) and self.is_tainted(
                    node.value, stmt
                ):
                    self.returns_tainted.add(stmt)
            return
        if isinstance(stmt, ast.ClassDef):
            self._visit_body(stmt.body, scope)
            return
        if isinstance(stmt, ast.Assign):
            for t in stmt.targets:
                self._assign_from(t, stmt.value, scope)
            return
        if isinstance(stmt, ast.AnnAssign) and stmt.value is not None:
            if stmt.target:
                self._assign_from(stmt.target, stmt.value, scope)
            return
        if isinstance(stmt, ast.AugAssign):
            if isinstance(stmt.op, ast.Add) and isinstance(stmt.target, ast.Name):
                if self.is_tainted(stmt.value, scope) or self._scope_lookup(
                    stmt.target.id, scope
                ):
                    self._mark(stmt.target.id, scope, True)
            else:
                self.is_tainted(stmt.value, scope)
            return
        if isinstance(stmt, ast.Expr):
            self.is_tainted(stmt.value, scope)
            return
        if isinstance(stmt, ast.Return):
            if self.is_tainted(stmt.value, scope) and scope is not None:
                self.returns_tainted.add(scope)
            return
        if isinstance(stmt, ast.For):
            if self.is_tainted(stmt.iter, scope):
                for name in _target_names(stmt.target):
                    self._mark(name, scope, True)
            self._visit_body(stmt.body, scope)
            self._visit_body(stmt.orelse, scope)
            return
        if isinstance(stmt, ast.While):
            self.is_tainted(stmt.test, scope)
            self._visit_body(stmt.body, scope)
            self._visit_body(stmt.orelse, scope)
            return
        if isinstance(stmt, ast.If):
            self.is_tainted(stmt.test, scope)
            self._visit_body(stmt.body, scope)
            self._visit_body(stmt.orelse, scope)
            return
        if isinstance(stmt, ast.With):
            for item in stmt.items:
                self.is_tainted(item.context_expr, scope)
                if item.optional_vars is not None and self.is_tainted(
                    item.context_expr, scope
                ):
                    for name in _target_names(item.optional_vars):
                        self._mark(name, scope, True)
            self._visit_body(stmt.body, scope)
            return
        if isinstance(stmt, ast.Try):
            self._visit_body(stmt.body, scope)
            for h in stmt.handlers:
                self._visit_body(h.body, scope)
            self._visit_body(stmt.orelse, scope)
            self._visit_body(stmt.finalbody, scope)
            return
        if isinstance(stmt, (ast.Delete, ast.Pass, ast.Import, ast.ImportFrom,
                             ast.Global, ast.Nonlocal, ast.Break, ast.Continue,
                             ast.Raise, ast.Assert)):
            return
        for child in ast.iter_child_nodes(stmt):
            if isinstance(child, ast.AST):
                self.is_tainted(child, scope) if hasattr(child, "col_offset") else None

    def _visit_body(self, body, scope):
        for stmt in body:
            self._visit_stmt(stmt, scope)


_CACHE = {}


def _source_from_context(context):
    data = context.file_data
    if isinstance(data, (str, bytes)):
        return data
    fname = context.filename
    if fname:
        with open(fname, "rb") as handle:
            return handle.read()
    return None


def findings_for(source):
    cached = _CACHE.get(source)
    if cached is None:
        cached = TaintAnalyzer(source).run()
        _CACHE[source] = cached
    return cached


def call_is_tainted_sink(context, test_id):
    data = _source_from_context(context)
    if not data:
        return False
    findings = findings_for(data)
    node = context.node
    return (node.lineno, node.col_offset) in findings.get(test_id, ())
