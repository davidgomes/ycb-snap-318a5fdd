#
# SPDX-License-Identifier: Apache-2.0
"""Tainted-input injection checks."""

import ast

import bandit
from bandit.core import issue
from bandit.core import test_properties as test


def _analyzer(context):
    return context._context.get("taint")


def _call_name(context):
    analyzer = _analyzer(context)
    if analyzer is not None:
        return analyzer.call_name(context.node)
    return context.call_function_name_qual


def _tainted(context, node):
    analyzer = _analyzer(context)
    return analyzer is not None and analyzer.is_tainted(node)


def _argument(context, names=()):
    if context.node.args:
        return context.node.args[0]
    for keyword in context.node.keywords:
        if keyword.arg in names:
            return keyword.value
    return None


def _finding(context, cwe, text):
    return bandit.Issue(
        severity=bandit.HIGH,
        confidence=bandit.MEDIUM,
        cwe=cwe,
        text=text,
    )


@test.checks("Call")
@test.test_id("B620")
def sql_injection(context):
    """Flag tainted values used as SQL query strings."""
    name = _call_name(context)
    if name and name.rsplit(".", 1)[-1] in {"execute", "executemany"}:
        query = _argument(context, {"query", "sql", "operation"})
        if _tainted(context, query):
            return _finding(
                context,
                issue.Cwe.SQL_INJECTION,
                "Tainted input used in a SQL query.",
            )


def _shell_true(context):
    for keyword in context.node.keywords:
        if keyword.arg == "shell":
            return (
                isinstance(keyword.value, ast.Constant)
                and keyword.value.value is True
            )
    return False


@test.checks("Call")
@test.test_id("B621")
def shell_injection(context):
    """Flag tainted values passed to shell command execution."""
    name = _call_name(context)
    if name in {"os.system", "os.popen"}:
        command = _argument(context, {"command", "cmd"})
    elif name in {
        "subprocess.call",
        "subprocess.run",
        "subprocess.Popen",
    } and _shell_true(context):
        command = _argument(context, {"args", "command"})
    else:
        return None

    if _tainted(context, command):
        return _finding(
            context,
            issue.Cwe.OS_COMMAND_INJECTION,
            "Tainted input used in a shell command.",
        )


@test.checks("Call")
@test.test_id("B622")
def path_traversal(context):
    """Flag tainted paths passed to the unqualified ``open`` builtin."""
    if _call_name(context) == "open" and _tainted(
        context, _argument(context, {"file", "path"})
    ):
        return _finding(
            context,
            issue.Cwe.PATH_TRAVERSAL,
            "Tainted input used in a file path.",
        )


@test.checks("Call")
@test.test_id("B623")
def ssrf(context):
    """Flag tainted URLs passed to outbound HTTP request functions."""
    if _call_name(context) in {
        "requests.get",
        "requests.post",
        "urllib.request.urlopen",
    } and _tainted(context, _argument(context, {"url"})):
        return _finding(
            context,
            issue.Cwe.SSRF,
            "Tainted input used in an outbound request URL.",
        )


@test.checks("Call")
@test.test_id("B624")
def xss(context):
    """Flag tainted values rendered without an escaping operation."""
    if _call_name(context) in {
        "render_template_string",
        "flask.render_template_string",
        "make_response",
        "flask.make_response",
        "markupsafe.Markup",
    } and _tainted(
        context,
        _argument(
            context, {"body", "response", "s", "source", "template"}
        ),
    ):
        return _finding(
            context,
            issue.Cwe.XSS,
            "Tainted input used in an HTML response.",
        )
