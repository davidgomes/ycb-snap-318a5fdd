import pytest

import tomlkit

from tomlkit import dumps
from tomlkit import parse
from tomlkit import to_dotted_keys
from tomlkit import to_inline_table
from tomlkit import to_standard_table
from tomlkit import to_super_table
from tomlkit.exceptions import ConversionError
from tomlkit.exceptions import TOMLKitError
from tomlkit.items import Comment
from tomlkit.items import InlineTable
from tomlkit.items import Table


def convert(fn, key_path, content, **kwargs):
    doc = parse(content)
    before = doc.unwrap()
    result = fn(key_path, doc, **kwargs)

    assert result is doc
    assert doc.unwrap() == before
    assert parse(dumps(doc)) == doc
    return doc


def test_exports():
    assert tomlkit.to_inline_table is to_inline_table
    assert tomlkit.to_standard_table is to_standard_table
    assert tomlkit.to_dotted_keys is to_dotted_keys
    assert tomlkit.to_super_table is to_super_table
    assert issubclass(ConversionError, TOMLKitError)


def test_to_inline_table():
    content = """\
x = 1

[t]  # header
k = 1
[t.sub]
m = 2

[u]
v = 3
"""
    doc = convert(to_inline_table, "t", content)

    assert isinstance(doc["t"], InlineTable)
    assert isinstance(doc["t"]["sub"], InlineTable)
    assert doc["t"].trivia.comment == "# header"
    assert (
        dumps(doc)
        == """\
x = 1
t = {k = 1, sub = {m = 2}}  # header

[u]
v = 3
"""
    )


def test_to_inline_table_nested_table():
    doc = convert(to_inline_table, "a.b", "[a.b]\nc = 1\n")

    assert dumps(doc) == "[a]\nb = {c = 1}\n"


def test_to_inline_table_merges_dotted_keys():
    doc = convert(to_inline_table, "u.p", "[u]\np.q = 1\np.r = 2\n")

    assert dumps(doc) == "[u]\np = {q = 1, r = 2}\n"


def test_to_inline_table_noop_on_inline_table():
    content = "t = {a = 1}  # c\n"
    doc = parse(content)

    assert to_inline_table("t", doc) is doc
    assert dumps(doc) == content


def test_to_inline_table_rejects_aot():
    content = "[t]\na = 1\n[[t.items]]\nb = 1\n"
    doc = parse(content)

    with pytest.raises(ConversionError) as e:
        to_inline_table("t", doc)

    assert e.value.key_path == "t"
    assert dumps(doc) == content


def test_to_standard_table():
    content = """\
x = 1
t = {k = 1, sub = {m = 2}}  # header

[u]
v = 3
"""
    doc = convert(to_standard_table, "t", content)

    assert isinstance(doc["t"], Table)
    assert isinstance(doc["t"]["sub"], Table)
    assert doc["t"].trivia.comment == "# header"
    assert (
        dumps(doc)
        == """\
x = 1

[t]  # header
k = 1

[t.sub]
m = 2

[u]
v = 3
"""
    )


def test_to_standard_table_noop_on_table():
    content = "[t]\na = 1\n"
    doc = parse(content)

    assert to_standard_table("t", doc) is doc
    assert dumps(doc) == content


def test_to_dotted_keys():
    content = """\
x = 1

[t]  # header
k = 1  # kc
[t.sub]
m = 2
[t.sub.deep]
z = 3
"""
    doc = convert(to_dotted_keys, "t", content)

    assert (
        dumps(doc)
        == """\
x = 1
# header
t.k = 1  # kc
t.sub.m = 2
t.sub.deep.z = 3

"""
    )
    assert isinstance(doc.body[1][1], Comment)
    assert doc["t"]["sub"]["deep"]["z"] == 3


def test_to_dotted_keys_max_depth():
    content = "[t]\nk = 1\n[t.sub]\nm = 2\n[t.sub.deep]\nz = 3\n"

    doc = convert(to_dotted_keys, "t", content, max_depth=1)
    assert dumps(doc) == "t.k = 1\nt.sub = {m = 2, deep = {z = 3}}\n"

    doc = convert(to_dotted_keys, "t", content, max_depth=2)
    assert dumps(doc) == "t.k = 1\nt.sub.m = 2\nt.sub.deep = {z = 3}\n"


def test_to_dotted_keys_inline_table():
    doc = convert(to_dotted_keys, "t", "t = {a = 1, b = {c = 2}}\n")

    assert dumps(doc) == "t.a = 1\nt.b.c = 2\n"


def test_to_dotted_keys_inside_inline_table():
    doc = convert(to_dotted_keys, "x.a", "x = {a = {b = 1, c = 2}, z = 3}\n")

    assert dumps(doc) == "x = {a.b = 1, a.c = 2, z = 3}\n"


def test_to_dotted_keys_rejects_values():
    with pytest.raises(ConversionError):
        to_dotted_keys("x", parse("x = 1\n"))


def test_to_super_table():
    content = """\
z = 0
# group
a.b = 1
a.c.d = 2

[x]
y = 1
"""
    doc = convert(to_super_table, "a", content)

    assert isinstance(doc["a"], Table)
    assert doc["a"].trivia.comment == "# group"
    assert (
        dumps(doc)
        == """\
z = 0

[a] # group
b = 1
c.d = 2

[x]
y = 1
"""
    )


def test_to_super_table_nested_prefix():
    doc = convert(to_super_table, "a.c", "a.b = 1\na.c.d = 2\na.c.e = 3\n")

    assert dumps(doc) == "a.b = 1\n\n[a.c]\nd = 2\ne = 3\n"


def test_to_super_table_inside_table():
    doc = convert(to_super_table, "u.p", "[u]\nv = 0\np.q = 1\np.r = 2\n")

    assert dumps(doc) == "[u]\nv = 0\n\n[u.p]\nq = 1\nr = 2\n"


def test_to_super_table_no_match():
    with pytest.raises(ConversionError) as e:
        to_super_table("a", parse("[a]\nb = 1\n"))

    assert e.value.key_path == "a"


def test_round_trip_between_forms():
    content = "[t]  # header\nk = 1\n[t.sub]\nm = 2\n"
    doc = parse(content)
    expected = doc.unwrap()

    to_dotted_keys("t", doc)
    to_super_table("t", doc)
    to_inline_table("t", doc)
    to_standard_table("t", doc)

    assert doc.unwrap() == expected
    assert parse(dumps(doc)) == doc
    assert doc["t"].trivia.comment == "# header"


def test_out_of_order_table():
    content = "[a]\nx = 1\n[b]\ny = 2\n[a.c]\nz = 3\n"

    doc = convert(to_dotted_keys, "a.c", content)
    assert dumps(doc) == "[a]\nx = 1\nc.z = 3\n[b]\ny = 2\n"

    doc = convert(to_inline_table, "a", content)
    assert dumps(doc) == "a = {x = 1, c = {z = 3}}\n\n[b]\ny = 2\n"


def test_programmatic_document():
    doc = tomlkit.document()
    doc.add(tomlkit.key(["srv", "host"]), "h")
    doc.add(tomlkit.key(["srv", "port"]), 1)

    to_super_table("srv", doc)
    assert dumps(doc) == '[srv]\nhost = "h"\nport = 1\n'

    to_inline_table("srv", doc)
    assert dumps(doc) == 'srv = {host = "h", port = 1}\n'
    assert parse(dumps(doc)) == doc


@pytest.mark.parametrize(
    "fn, key_path, content",
    [
        (to_inline_table, "missing", "x = 1\n"),
        (to_inline_table, "x", "x = 1\n"),
        (to_inline_table, "x.y", "x = 1\n"),
        (to_standard_table, "x", "x = 1\n"),
        (to_standard_table, "a.b", "[a]\nb = [1]\n"),
        (to_dotted_keys, "missing.key", "[a]\nb = 1\n"),
        (to_dotted_keys, "a.b", "a.x = 1\n[a.b]\nc = 1\n"),
    ],
)
def test_conversion_errors(fn, key_path, content):
    doc = parse(content)

    with pytest.raises(ConversionError) as e:
        fn(key_path, doc)

    assert e.value.key_path == key_path
    assert dumps(doc) == content
