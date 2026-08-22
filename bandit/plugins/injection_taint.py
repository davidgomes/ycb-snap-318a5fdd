# SPDX-License-Identifier: Apache-2.0
r"""
=======================================================
B620-B624: Taint-tracking injection checks
=======================================================

These plugins flag user-controlled data that reaches dangerous sinks.
Unlike B608, they follow values through variables, string building,
calls, and nested functions.

Sources
-------

- ``request.args`` / ``request.form`` / ``request.cookies`` (``.get()``
  and subscript)
- ``sys.argv``
- ``input()``
- ``os.environ`` (``.get()`` and subscript)

Taint is propagated through concatenation, f-strings, ``%``,
``.format()``, ``+=``, ``:=``, calls, multi-hop assignments, and nested
functions. Import aliases are resolved for sinks.

The following are treated as safe:

- Parameterized queries (taint in params, not the query string)
- ``int()``, ``shlex.quote``, ``os.path.basename``
- ``flask.escape``, ``markupsafe.escape``

Plugins
-------

- **B620** SQL injection (CWE-89): ``execute``, ``executemany``
- **B621** shell injection (CWE-78): ``os.system``, ``os.popen``,
  ``subprocess.call`` / ``run`` / ``Popen`` with ``shell=True``
- **B622** path traversal (CWE-22): builtin ``open`` only
- **B623** SSRF (CWE-918): ``requests.get`` / ``post``,
  ``urllib.request.urlopen``
- **B624** XSS (CWE-79): ``render_template_string``,
  ``markupsafe.Markup`` (exact), ``make_response``

All findings use HIGH severity and MEDIUM confidence.

.. versionadded:: 1.8.7

"""
import bandit
from bandit.core import issue
from bandit.core import taint
from bandit.core import test_properties as test


def _issue(cwe, text):
    return bandit.Issue(
        severity=bandit.HIGH,
        confidence=bandit.MEDIUM,
        cwe=cwe,
        text=text,
    )


@test.checks("Call")
@test.test_id("B620")
def taint_sql_injection(context):
    """Flag user input reaching execute/executemany query strings."""
    if taint.call_is_tainted_sink(context, "B620"):
        return _issue(
            issue.Cwe.SQL_INJECTION,
            "Possible SQL injection: user-controlled data used in query.",
        )


@test.checks("Call")
@test.test_id("B621")
def taint_shell_injection(context):
    """Flag user input reaching shell-invoking process APIs."""
    if taint.call_is_tainted_sink(context, "B621"):
        return _issue(
            issue.Cwe.OS_COMMAND_INJECTION,
            "Possible shell injection: user-controlled data used in a "
            "shell command.",
        )


@test.checks("Call")
@test.test_id("B622")
def taint_path_traversal(context):
    """Flag user input reaching builtin open()."""
    if taint.call_is_tainted_sink(context, "B622"):
        return _issue(
            issue.Cwe.PATH_TRAVERSAL,
            "Possible path traversal: user-controlled data used in open().",
        )


@test.checks("Call")
@test.test_id("B623")
def taint_ssrf(context):
    """Flag user input reaching HTTP client URL arguments."""
    if taint.call_is_tainted_sink(context, "B623"):
        return _issue(
            issue.Cwe.SSRF,
            "Possible SSRF: user-controlled data used as a request URL.",
        )


@test.checks("Call")
@test.test_id("B624")
def taint_xss(context):
    """Flag user input reaching XSS sinks."""
    if taint.call_is_tainted_sink(context, "B624"):
        return _issue(
            issue.Cwe.XSS,
            "Possible XSS: user-controlled data used in a template or "
            "response.",
        )
