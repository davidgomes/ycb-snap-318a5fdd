# SPDX-License-Identifier: Apache-2.0
"""Examples for taint-tracking injection plugins B620-B624."""
import os
import shlex
import subprocess
import sys
from os import system as os_system
from urllib.request import urlopen

import requests
from flask import make_response
from flask import render_template_string
from flask import request
from markupsafe import Markup
from markupsafe import escape as ms_escape

# B620 SQL: user input in query
qid = request.args.get("id")
cursor = type(
    "C",
    (),
    {
        "execute": lambda *a, **k: None,
        "executemany": lambda *a, **k: None,
    },
)()
cursor.execute("SELECT * FROM t WHERE id = " + qid)
cursor.executemany(f"SELECT * FROM t WHERE id = {qid}")
# parameterized: taint only in params
cursor.execute("SELECT * FROM t WHERE id = %s", (qid,))
# sanitized
cursor.execute("SELECT * FROM t WHERE id = %s" % int(qid))

# B621 shell
cmd = request.form["cmd"]
os.system(cmd)
os.popen(sys.argv[1])
os_system("ls " + request.cookies.get("c"))
subprocess.call(cmd, shell=True)
subprocess.run(cmd, shell=True)
subprocess.Popen(cmd, shell=True)
# safe: no shell
subprocess.call(["echo", cmd])
# sanitized
os.system(shlex.quote(cmd))

# B622 path
path = request.args["file"]
open(path)
open(os.path.basename(path))
# qualified open should not trigger B622
os.open(path, os.O_RDONLY)

# B623 SSRF
url = request.args.get("url")
requests.get(url)
requests.post(os.environ["CALLBACK"])
urlopen(url)

# B624 XSS
tmpl = request.args.get("q")
render_template_string(tmpl)
Markup(tmpl)
make_response(tmpl)
# sanitized
make_response(ms_escape(tmpl))

# propagation: concat, format, percent, +=, walrus, hops, nested
user = input()
built = "echo " + user
built2 = "echo {}".format(user)
built3 = "echo %s" % user
acc = "echo "
acc += user
os.system(built)
os.system(built2)
os.system(built3)
os.system(acc)
os.system(c := os.environ.get("CMD"))


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
