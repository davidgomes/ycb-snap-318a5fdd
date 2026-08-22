# SPDX-License-Identifier: Apache-2.0
import unittest

from bandit.core import taint


SQL = '''
from flask import request
qid = request.args.get("id")
cursor.execute("select " + qid)
cursor.execute("select %s", (qid,))
cursor.execute("select %s" % int(qid))
'''

SHELL = '''
import os, sys, subprocess, shlex
from os import system as os_system
cmd = request.form["cmd"]
os.system(cmd)
os.popen(sys.argv[1])
os_system(os.environ.get("X"))
subprocess.call(cmd, shell=True)
subprocess.run(cmd, shell=True)
subprocess.Popen(cmd, shell=True)
subprocess.call(cmd)
os.system(shlex.quote(cmd))
'''

PATH = '''
from flask import request
import os
p = request.args["file"]
open(p)
open(os.path.basename(p))
os.open(p, 0)
'''

SSRF = '''
import os, requests
from urllib.request import urlopen
from flask import request
u = request.args.get("url")
requests.get(u)
requests.post(os.environ["C"])
urlopen(u)
'''

XSS = '''
from flask import request, render_template_string, make_response
from markupsafe import Markup, escape
t = request.args.get("q")
render_template_string(t)
Markup(t)
make_response(t)
make_response(escape(t))
'''

PROP = '''
from flask import request
import os
user = input()
os.system("echo " + user)
os.system(f"echo {user}")
os.system("echo %s" % user)
os.system("echo {}".format(user))
acc = "echo "
acc += user
os.system(acc)
os.system(c := request.cookies.get("c"))

def hop():
    return request.args["x"]
os.system(hop())

def nested():
    data = request.form.get("n")
    def inner():
        os.system(data)
    inner()
nested()

def callee(arg):
    os.system(arg)
callee(request.cookies["z"])
'''


class TaintAnalyzerTests(unittest.TestCase):
    def ids(self, source, test_id):
        return taint.TaintAnalyzer(source).run().get(test_id, set())

    def test_sql_taint_not_params_or_int(self):
        hits = self.ids(SQL, "B620")
        self.assertEqual(1, len(hits))

    def test_shell_sources_and_sanitizer(self):
        hits = self.ids(SHELL, "B621")
        self.assertEqual(6, len(hits))

    def test_path_unqualified_open_only(self):
        hits = self.ids(PATH, "B622")
        self.assertEqual(1, len(hits))

    def test_ssrf(self):
        hits = self.ids(SSRF, "B623")
        self.assertEqual(3, len(hits))

    def test_xss_exact_markup_and_escape(self):
        hits = self.ids(XSS, "B624")
        self.assertEqual(3, len(hits))

    def test_propagation(self):
        hits = self.ids(PROP, "B621")
        self.assertEqual(9, len(hits))
