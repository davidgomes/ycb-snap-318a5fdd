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
from tomlkit.items import InlineTable
from tomlkit.items import Table


def convert(fn, src, *args, **kwargs):
    doc = parse(src)
    expected = doc.unwrap()

    assert fn(*args, doc, **kwargs) is doc

    out = dumps(doc)
    reparsed = parse(out)
    assert doc.unwrap() == expected
    assert reparsed.unwrap() == expected
    assert dumps(reparsed) == out

    return doc, out


def test_functions_are_exported():
    from tomlkit import convert

    for name in [
        "to_inline_table",
        "to_standard_table",
        "to_dotted_keys",
        "to_super_table",
    ]:
        assert getattr(tomlkit, name) is getattr(convert, name)
        assert name in tomlkit.__all__


def test_conversion_error():
    assert issubclass(ConversionError, TOMLKitError)

    with pytest.raises(ConversionError) as exc:
        to_inline_table("a.b", parse("[a]\nx = 1\n"))

    assert exc.value.key_path == "a.b"


def test_to_inline_table():
    doc, out = convert(to_inline_table, "[a]\nx = 1\ny = 'two'\n", "a")

    assert out == "a = {x = 1, y = 'two'}\n"
    assert isinstance(doc["a"], InlineTable)


def test_to_inline_table_nested_tables():
    src = """x = 1

[a]
k = 1

[a.sub]
m = 2

[a.sub.deep]
n = 3

[b]
z = 0
"""
    doc, out = convert(to_inline_table, src, "a")

    assert (
        out
        == """x = 1

a = {k = 1, sub = {m = 2, deep = {n = 3}}}

[b]
z = 0
"""
    )
    assert isinstance(doc["a"]["sub"]["deep"], InlineTable)


def test_to_inline_table_sub_table():
    src = "[b.c]\nm = 2\n\n[b.d]\nn = 1\n"
    _, out = convert(to_inline_table, src, "b.c")

    assert out == "[b]\nc = {m = 2}\n\n[b.d]\nn = 1\n"


def test_to_inline_table_moves_before_other_tables():
    src = "[a]\nk = 1\n\n# about b\n[b]\nn = 3\n"
    _, out = convert(to_inline_table, src, "b")

    assert out == "# about b\nb = {n = 3}\n\n[a]\nk = 1\n"


def test_to_inline_table_migrates_comments():
    src = "[a]  # header\n# inner\nx = 1  # value\n"
    _, out = convert(to_inline_table, src, "a")

    assert out == "# inner\n# value\na = {x = 1}  # header\n"


def test_to_inline_table_noop_on_inline_table():
    src = "a = {x = 1}\n"
    _, out = convert(to_inline_table, src, "a")

    assert out == src


@pytest.mark.parametrize("key_path", ["x", "a.x"])
def test_to_inline_table_rejects_non_tables(key_path):
    doc = parse("x = 1\n[a]\nx = [1, 2]\n")

    with pytest.raises(ConversionError) as exc:
        to_inline_table(key_path, doc)

    assert exc.value.key_path == key_path


def test_to_inline_table_rejects_array_of_tables():
    src = "[a]\nx = 1\n[a.b]\n[[a.b.c]]\ny = 1\n"
    doc = parse(src)

    with pytest.raises(ConversionError):
        to_inline_table("a", doc)

    assert dumps(doc) == src


def test_to_standard_table():
    doc, out = convert(to_standard_table, "a = {x = 1, y = 'two'}\n", "a")

    assert out == "[a]\nx = 1\ny = 'two'\n"
    assert isinstance(doc["a"], Table)


def test_to_standard_table_comment_becomes_header_comment():
    _, out = convert(to_standard_table, "a = {x = 1}  # about a\n", "a")

    assert out == "[a]  # about a\nx = 1\n"


def test_to_standard_table_nested_inline_tables():
    src = """title = "t"
v = {p = 1, q = {r = 2}}
w = 2

[z]
y = 1
"""
    doc, out = convert(to_standard_table, src, "v")

    assert (
        out
        == """title = "t"
w = 2

[v]
p = 1

[v.q]
r = 2

[z]
y = 1
"""
    )
    assert isinstance(doc["v"]["q"], Table)


def test_to_standard_table_inside_table():
    src = "[u]\nv = {p = 1}\nw = 2\n\n[u.x]\ny = 1\n"
    _, out = convert(to_standard_table, src, "u.v")

    assert out == "[u]\nw = 2\n\n[u.v]\np = 1\n\n[u.x]\ny = 1\n"


def test_to_standard_table_noop_on_table():
    src = "[a]\nx = 1\n"
    _, out = convert(to_standard_table, src, "a")

    assert out == src


def test_to_standard_table_errors():
    doc = parse("x = 1\nv = {p = {q = 1}}\n")

    with pytest.raises(ConversionError):
        to_standard_table("x", doc)

    # A header table cannot live inside an inline table.
    with pytest.raises(ConversionError):
        to_standard_table("v.p", doc)


def test_to_dotted_keys_from_table():
    src = """x = 1

[a]  # about a
k = 1  # k
s = "str"

[a.sub]
m = 2

[b]
n = 3
"""
    _, out = convert(to_dotted_keys, src, "a")

    assert (
        out
        == """x = 1

# about a
a.k = 1  # k
a.s = "str"
a.sub.m = 2

[b]
n = 3
"""
    )


def test_to_dotted_keys_from_inline_table():
    src = "v = {p = 1, q = {r = 2}}  # about v\nw = 2\n"
    _, out = convert(to_dotted_keys, src, "v")

    assert out == "# about v\nv.p = 1\nv.q.r = 2\nw = 2\n"


def test_to_dotted_keys_into_parent_table():
    src = "[b.c]\nm = 2\n\n[b.d]\nn = 1\n"
    _, out = convert(to_dotted_keys, src, "b.c")

    assert out == "[b]\nc.m = 2\n\n[b.d]\nn = 1\n"


def test_to_dotted_keys_inside_inline_table():
    src = "v = {p = 1, q = {r = 2, s = {t = 3}}}\n"
    _, out = convert(to_dotted_keys, src, "v.q")

    assert out == "v = {p = 1, q.r = 2, q.s.t = 3}\n"


@pytest.mark.parametrize(
    "max_depth, expected",
    [
        (None, "a.x = 1\na.b.y = 2\na.b.c.z = 3\n"),
        (1, "a.x = 1\na.b = {y = 2, c = {z = 3}}\n"),
        (2, "a.x = 1\na.b.y = 2\na.b.c = {z = 3}\n"),
        (3, "a.x = 1\na.b.y = 2\na.b.c.z = 3\n"),
    ],
)
def test_to_dotted_keys_max_depth(max_depth, expected):
    src = "[a]\nx = 1\n\n[a.b]\ny = 2\n\n[a.b.c]\nz = 3\n"
    _, out = convert(to_dotted_keys, src, "a", max_depth=max_depth)

    assert out == expected


def test_to_dotted_keys_rejects_non_tables():
    doc = parse("x = 1\n[a]\ny = [{z = 1}]\n")

    for key_path in ["x", "a.y", "a.missing", "x.y"]:
        with pytest.raises(ConversionError) as exc:
            to_dotted_keys(key_path, doc)
        assert exc.value.key_path == key_path


def test_to_super_table():
    src = """x = 1
# about srv
srv.host = "h"
srv.port.num = 80  # port
y = 2
srv.name = "n"

[other]
k = 1
"""
    doc, out = convert(to_super_table, src, "srv")

    assert (
        out
        == """x = 1
y = 2

[srv] # about srv
host = "h"
port.num = 80  # port
name = "n"

[other]
k = 1
"""
    )
    assert isinstance(doc["srv"], Table)


def test_to_super_table_nested_prefix():
    src = "a.b = 1\na.c.d = 2\na.c.e = 3\n"
    _, out = convert(to_super_table, src, "a.c")

    assert out == "a.b = 1\n\n[a.c]\nd = 2\ne = 3\n"


def test_to_super_table_inside_table():
    src = "[t]\nx = 1\na.b = 1\na.c = 2\n"
    _, out = convert(to_super_table, src, "t.a")

    assert out == "[t]\nx = 1\n\n[t.a]\nb = 1\nc = 2\n"


def test_to_super_table_without_matches():
    doc = parse("x = 1\na.b = 2\n[t]\ny = 1\n")

    for key_path in ["x", "nope", "a.c", "t", "t.y"]:
        with pytest.raises(ConversionError) as exc:
            to_super_table(key_path, doc)
        assert exc.value.key_path == key_path


def test_round_trip_between_all_forms():
    src = """name = "app"

[server]  # main server
host = "localhost"

[server.tls]
cert = "c.pem"

[db]
url = "u"
"""
    doc = parse(src)
    expected = doc.unwrap()

    to_inline_table("server", doc)
    assert dumps(doc).startswith(
        'name = "app"\n\n'
        'server = {host = "localhost", tls = {cert = "c.pem"}}  # main server\n'
    )

    to_standard_table("server", doc)
    to_dotted_keys("server", doc)
    to_super_table("server", doc)

    assert doc.unwrap() == expected
    assert parse(dumps(doc)).unwrap() == expected
    assert "[server] # main server" in dumps(doc)


def test_document_built_programmatically():
    doc = tomlkit.document()
    doc["x"] = 1
    doc["tbl"] = {"a": 1, "nested": {"b": 2}}

    to_inline_table("tbl", doc)
    assert dumps(doc) == "x = 1\ntbl = {a = 1, nested = {b = 2}}\n"

    to_dotted_keys("tbl", doc)
    assert dumps(doc) == "x = 1\ntbl.a = 1\ntbl.nested.b = 2\n"

    to_super_table("tbl", doc)
    doc["tbl"]["c"] = 3
    assert parse(dumps(doc)).unwrap() == {
        "x": 1,
        "tbl": {"a": 1, "nested": {"b": 2}, "c": 3},
    }
