#
# SPDX-License-Identifier: Apache-2.0
"""Tests for tainted-input injection checks."""

import ast
from types import SimpleNamespace
from unittest import TestCase

from bandit.core.taint import TaintAnalyzer
from bandit.plugins import injection_taint


class TaintTests(TestCase):
    def _issues(self, source, plugin):
        tree = ast.parse(source)
        analyzer = TaintAnalyzer(tree)
        issues = []
        for node in ast.walk(tree):
            if isinstance(node, ast.Call):
                context = SimpleNamespace(
                    _context={"taint": analyzer},
                    node=node,
                )
                result = plugin(context)
                if result is not None:
                    issues.append(result)
        return issues

    def test_sources_reach_each_sink_and_parameters_are_safe(self):
        source = """
import os as operating_system
import requests as http
from flask import make_response as response
from flask import render_template_string as render
from markupsafe import Markup as Mark
from subprocess import run as launch
from urllib.request import urlopen as fetch

user = input()
query = "SELECT * FROM users WHERE name = '" + user
cursor.execute(query, (user,))
cursor.executemany(query, params=(user,))
cursor.execute("SELECT * FROM users WHERE id = ?", (user,))
operating_system.system(user)
operating_system.popen(user)
launch(user, shell=True)
open(user)
http.get(user)
http.post(user)
fetch(user)
render(user)
response(user)
Mark(user)
"""
        expected = {
            "sql_injection": 2,
            "shell_injection": 3,
            "path_traversal": 1,
            "ssrf": 3,
            "xss": 3,
        }
        for name, count in expected.items():
            with self.subTest(plugin=name):
                self.assertEqual(
                    count,
                    len(self._issues(source, getattr(injection_taint, name))),
                )

    def test_request_sources_and_propagation(self):
        source = """
from flask import request

args = request.args
value = args.get("value")
value += " suffix"
formatted = f"{value}"
formatted = "{}".format(formatted)
query = "SELECT {}".format(formatted)

def execute_query(first):
    second = first
    cursor.execute(second)

execute_query(query)
"""
        issues = self._issues(source, injection_taint.sql_injection)
        self.assertEqual(1, len(issues))

    def test_safe_conversions_and_escaping(self):
        source = """
import flask
import markupsafe
import os
import shlex
from subprocess import run

raw = input()
number = int(raw)
quoted = shlex.quote(raw)
base = os.path.basename(raw)
html = flask.escape(raw)
safe_html = markupsafe.escape(raw)
open(number)
run(quoted, shell=True)
"""
        for plugin in (
            injection_taint.shell_injection,
            injection_taint.path_traversal,
            injection_taint.xss,
        ):
            with self.subTest(plugin=plugin.__name__):
                self.assertEqual(0, len(self._issues(source, plugin)))

    def test_sys_argv_and_environment_sources(self):
        source = """
import os
import sys

path = sys.argv[1]
url = os.environ.get("URL")
open(path)
requests.get(url)
"""
        self.assertEqual(
            1,
            len(self._issues(source, injection_taint.path_traversal)),
        )
        self.assertEqual(
            1,
            len(self._issues(source, injection_taint.ssrf)),
        )
