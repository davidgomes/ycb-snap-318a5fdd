#
# SPDX-License-Identifier: Apache-2.0
"""Small, conservative intra-file taint analysis for injection checks."""

import ast

from bandit.core import utils


class TaintAnalyzer:
    """Find values derived from common user-controlled Python inputs.

    This intentionally does not try to model Python's complete type system.
    Unknown calls propagate taint, while a small set of well-known conversion
    and escaping functions terminates it.  The conservative behavior is useful
    for security checks and keeps the analysis independent of imported code.
    """

    _REQUEST_FIELDS = {"args", "form", "cookies"}
    _SAFE_CALLS = {
        "int",
        "builtins.int",
        "shlex.quote",
        "os.path.basename",
        "flask.escape",
        "markupsafe.escape",
    }

    def __init__(self, tree):
        self.tree = tree
        self.import_aliases = {}
        self.tainted_names = set()
        self.functions = {}
        self._collect_symbols()
        self._analyze()

    def _collect_symbols(self):
        for node in ast.walk(self.tree):
            if isinstance(node, ast.Import):
                for item in node.names:
                    self.import_aliases[item.asname or item.name] = item.name
            elif isinstance(node, ast.ImportFrom) and node.module:
                for item in node.names:
                    if item.name == "*":
                        continue
                    self.import_aliases[item.asname or item.name] = (
                        f"{node.module}.{item.name}"
                    )
            elif isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
                self.functions.setdefault(node.name, []).append(node)

    def _analyze(self):
        # Assignment order should not make a real data flow disappear. A few
        # fixed-point passes also allow taint to cross nested function calls.
        passes = max(2, sum(isinstance(n, ast.Assign) for n in ast.walk(self.tree)))
        for _ in range(passes):
            before = len(self.tainted_names)
            for node in ast.walk(self.tree):
                if isinstance(node, ast.Assign):
                    self._assign(node.targets, node.value)
                elif isinstance(node, (ast.AnnAssign, ast.NamedExpr)):
                    target = node.target
                    self._assign_target(target, self._expr_tainted(node.value))
                elif isinstance(node, ast.AugAssign):
                    self._assign_target(
                        node.target,
                        self._expr_tainted(node.target)
                        or self._expr_tainted(node.value),
                    )
                elif isinstance(node, ast.Call):
                    self._bind_call_parameters(node)
            if len(self.tainted_names) == before:
                break

    def _assign(self, targets, value):
        tainted = self._expr_tainted(value)
        for target in targets:
            self._assign_target(target, tainted)

    def _assign_target(self, target, tainted):
        if isinstance(target, ast.Name):
            if tainted:
                self.tainted_names.add(target.id)
        elif isinstance(target, (ast.Tuple, ast.List)):
            for item in target.elts:
                self._assign_target(item, tainted)
        elif isinstance(target, ast.Starred):
            self._assign_target(target.value, tainted)

    def _bind_call_parameters(self, call):
        name = self.call_name(call)
        functions = self.functions.get(name)
        if not functions:
            return

        positional = list(call.args)
        keyword_args = {
            keyword.arg: keyword.value
            for keyword in call.keywords
            if keyword.arg is not None
        }
        for function in functions:
            arguments = function.args
            parameters = (
                list(getattr(arguments, "posonlyargs", []))
                + list(arguments.args)
            )
            for index, argument in enumerate(positional):
                if index < len(parameters) and self._expr_tainted(argument):
                    self.tainted_names.add(parameters[index].arg)
            for parameter in parameters:
                value = keyword_args.get(parameter.arg)
                if value is not None and self._expr_tainted(value):
                    self.tainted_names.add(parameter.arg)
            if arguments.vararg and any(
                self._expr_tainted(argument)
                for argument in positional[len(parameters) :]
            ):
                self.tainted_names.add(arguments.vararg.arg)

    def _expr_tainted(self, node, seen=None):
        if node is None:
            return False
        if seen is None:
            seen = set()
        node_id = id(node)
        if node_id in seen:
            return False
        seen.add(node_id)

        if self._is_source(node):
            return True
        if isinstance(node, ast.Name):
            return node.id in self.tainted_names
        if isinstance(node, ast.Call):
            if self.call_name(node) in self._SAFE_CALLS:
                return False
            if (
                isinstance(node.func, ast.Attribute)
                and self._expr_tainted(node.func.value, seen)
            ):
                return True
            if any(self._expr_tainted(arg, seen) for arg in node.args):
                return True
            if any(
                self._expr_tainted(keyword.value, seen)
                for keyword in node.keywords
            ):
                return True
            function = self.functions.get(self.call_name(node))
            return bool(
                function
                and any(
                    self._expr_tainted(return_node.value, seen)
                    for definition in function
                    for return_node in ast.walk(definition)
                    if isinstance(return_node, ast.Return)
                )
            )
        if isinstance(node, ast.Constant):
            return False
        return any(
            self._expr_tainted(child, seen)
            for child in ast.iter_child_nodes(node)
        )

    def _is_source(self, node):
        if isinstance(node, ast.Call):
            return self.call_name(node) in {"input", "builtins.input"} or (
                self._is_request_or_environment_access(node.func)
            )
        if isinstance(node, (ast.Attribute, ast.Subscript)):
            value = node.value if isinstance(node, ast.Subscript) else node
            return self._is_request_or_environment_access(value)
        return False

    def _is_request_or_environment_access(self, node):
        name = self.qualified_name(node)
        parts = name.split(".")
        for index, part in enumerate(parts[:-1]):
            if part == "request" and parts[index + 1] in self._REQUEST_FIELDS:
                return True
        for root, field in (("os", "environ"), ("sys", "argv")):
            if len(parts) >= 2 and parts[-2:] == [root, field]:
                return True
        return any(
            parts[index : index + 2] == [root, field]
            for index in range(len(parts) - 1)
            for root, field in (("os", "environ"), ("sys", "argv"))
        )

    def qualified_name(self, node):
        return utils._get_attr_qual_name(node, self.import_aliases)

    def call_name(self, node):
        return utils.get_call_name(node, self.import_aliases)

    def is_tainted(self, node):
        return self._expr_tainted(node)
