import pytest

import tomlkit

from tomlkit import document
from tomlkit import dumps
from tomlkit import inline_table
from tomlkit import key
from tomlkit import parse
from tomlkit import table
from tomlkit.convert import to_dotted_keys
from tomlkit.convert import to_inline_table
from tomlkit.convert import to_standard_table
from tomlkit.convert import to_super_table
from tomlkit.exceptions import ConversionError
from tomlkit.exceptions import TOMLKitError
from tomlkit.items import InlineTable
from tomlkit.items import Table


def convert(fn, content, path, **kwargs):
    doc = parse(content)
    before = doc.unwrap()

    assert fn(path, doc, **kwargs) is doc

    output = dumps(doc)
    reparsed = parse(output)
    assert doc.unwrap() == before
    assert reparsed.unwrap() == before
    assert dumps(reparsed) == output

    return doc


def test_conversions_are_exported_from_the_package():
    assert tomlkit.to_inline_table is to_inline_table
    assert tomlkit.to_standard_table is to_standard_table
    assert tomlkit.to_dotted_keys is to_dotted_keys
    assert tomlkit.to_super_table is to_super_table


def test_conversion_error_carries_the_key_path():
    with pytest.raises(ConversionError) as exc_info:
        to_inline_table("a.missing", parse("[a]\nx = 1\n"))

    assert isinstance(exc_info.value, TOMLKitError)
    assert exc_info.value.key_path == "a.missing"
    assert "a.missing" in str(exc_info.value)


@pytest.mark.parametrize(
    "fn", [to_inline_table, to_standard_table, to_dotted_keys, to_super_table]
)
@pytest.mark.parametrize(
    "content, path",
    [
        ("a = 1\n", "b"),
        ("[a]\nx = 1\n", "a.y"),
        ("a = 1\n", "a.b"),
        ('a = "s"\n', "a.b.c"),
        ("[[a]]\nx = 1\n", "a.x"),
        ("a = [{x = 1}]\n", "a.x"),
        ("a = 1\n", ""),
        ("a = 1\n", "a..b"),
        ("a = 1\n", "a = 1"),
    ],
)
def test_invalid_key_paths_raise(fn, content, path):
    doc = parse(content)

    with pytest.raises(ConversionError) as exc_info:
        fn(path, doc)

    assert exc_info.value.key_path == path
    assert dumps(doc) == content


def test_quoted_key_path_segments():
    doc = convert(to_dotted_keys, '["my server"]\n"host name" = 1\n', '"my server"')

    assert dumps(doc) == '"my server"."host name" = 1\n'


def test_to_inline_table():
    doc = convert(
        to_inline_table, '[server]\nhost = "localhost"\nport = 8080\n', "server"
    )

    assert dumps(doc) == 'server = {host = "localhost", port = 8080}\n'
    assert isinstance(doc["server"], InlineTable)


def test_to_inline_table_converts_nested_tables():
    doc = convert(
        to_inline_table,
        "[a]\nx = 1\n\n[a.b]\ny = 2\n\n[a.b.c]\nz = 3\n",
        "a",
    )

    assert dumps(doc) == "a = {x = 1, b = {y = 2, c = {z = 3}}}\n"
    assert isinstance(doc["a"]["b"], InlineTable)
    assert isinstance(doc["a"]["b"]["c"], InlineTable)


def test_to_inline_table_keeps_the_header_comment():
    content = """\
title = "x"

[server]  # main
# inner comment
host = "a"  # host comment
port = 1

[db]
name = "d"
"""
    expected = """\
title = "x"

server = {host = "a", port = 1}  # main

[db]
name = "d"
"""
    doc = convert(to_inline_table, content, "server")

    assert dumps(doc) == expected


def test_to_inline_table_moves_the_value_before_other_tables():
    content = 'title = "x"\n\n[db]\nname = "d"\n\n[server]\nhost = "a"\n'
    doc = convert(to_inline_table, content, "server")

    assert dumps(doc) == 'title = "x"\nserver = {host = "a"}\n\n[db]\nname = "d"\n'


def test_to_inline_table_nested_in_table():
    content = "[t]\nq = 1\n\n[t.s]\nx = 1\n\n[t.u]\ny = 2\n"
    doc = convert(to_inline_table, content, "t.s")

    assert dumps(doc) == "[t]\nq = 1\n\ns = {x = 1}\n\n[t.u]\ny = 2\n"


def test_to_inline_table_in_implicit_parent():
    content = "[a.b]  # comment\nx = 1\n[a.c]\ny = 2\n"
    doc = convert(to_inline_table, content, "a.b")

    assert dumps(doc) == "[a]\nb = {x = 1}  # comment\n\n[a.c]\ny = 2\n"


def test_to_inline_table_next_to_dotted_keys_defining_the_parent():
    content = 'x.d = "s"\n\n[x.srv]\nport = 1\n'
    doc = convert(to_inline_table, content, "x.srv")

    assert dumps(doc) == 'x.d = "s"\nx.srv = {port = 1}\n'


def test_to_inline_table_in_the_header_of_an_out_of_order_parent():
    content = "[a]\nk = 1\n\n[z]\nw = 1\n\n[a.b]\nx = 1\n"
    doc = convert(to_inline_table, content, "a.b")

    assert dumps(doc) == "[a]\nk = 1\nb = {x = 1}\n\n[z]\nw = 1\n"


def test_to_inline_table_merges_table_fragments():
    doc = convert(to_inline_table, "b.c = 1\nb.d = 2\nz = 0\n", "b")
    assert dumps(doc) == "b = {c = 1, d = 2}\nz = 0\n"

    doc = convert(to_inline_table, "a.b.c = 1\na.b.d = 2\n", "a.b")
    assert dumps(doc) == "a.b = {c = 1, d = 2}\n"

    doc = convert(to_inline_table, "[a]\nb.c = 1\nb.d = 2\ne = 3\n", "a")
    assert dumps(doc) == "a = {b = {c = 1, d = 2}, e = 3}\n"

    doc = convert(to_inline_table, "a.x = 1\n\n[a.b]\nc = 2\n[q]\nw = 1\n", "a")
    assert dumps(doc) == "a = {x = 1, b = {c = 2}}\n\n[q]\nw = 1\n"


def test_to_inline_table_merges_out_of_order_tables():
    content = "[a.b]\nx = 1\n[z]\nk = 1\n[a.c]\ny = 2\n"
    doc = convert(to_inline_table, content, "a")

    assert dumps(doc) == "a = {b = {x = 1}, c = {y = 2}}\n\n[z]\nk = 1\n"


def test_to_inline_table_inside_inline_table():
    doc = convert(to_inline_table, "x = {a.b = 1, a.c = 2, z = 3}\n", "x.a")

    assert dumps(doc) == "x = {a = {b = 1, c = 2}, z = 3}\n"


def test_to_inline_table_empty_table():
    doc = convert(to_inline_table, "[a]\n\n[b]\nx = 1\n", "a")

    assert dumps(doc) == "a = {}\n\n[b]\nx = 1\n"


def test_to_inline_table_is_a_noop_for_inline_tables():
    content = "a = { x = 1 }  # c\n"
    doc = convert(to_inline_table, content, "a")

    assert dumps(doc) == content


@pytest.mark.parametrize(
    "content, path",
    [
        ("a = 1\n", "a"),
        ("[[a]]\nx = 1\n", "a"),
        ("[a]\nx = 1\n[[a.b]]\ny = 1\n", "a"),
        ("[a]\n[a.b]\n[[a.b.c]]\ny = 1\n", "a"),
    ],
)
def test_to_inline_table_errors(content, path):
    doc = parse(content)

    with pytest.raises(ConversionError):
        to_inline_table(path, doc)

    assert dumps(doc) == content


def test_to_standard_table():
    doc = convert(
        to_standard_table,
        'server = {host = "localhost", port = 8080}  # main\n',
        "server",
    )

    assert dumps(doc) == '[server]  # main\nhost = "localhost"\nport = 8080\n'
    assert isinstance(doc["server"], Table)


def test_to_standard_table_converts_nested_inline_tables():
    content = "a = 1\nsrv = {h = 1, tls = {c = 2}}\nb = 2\n\n[db]\nn = 1\n"
    expected = "a = 1\nb = 2\n\n[srv]\nh = 1\n\n[srv.tls]\nc = 2\n\n[db]\nn = 1\n"
    doc = convert(to_standard_table, content, "srv")

    assert dumps(doc) == expected
    assert isinstance(doc["srv"]["tls"], Table)


def test_to_standard_table_places_the_table_before_other_tables():
    doc = convert(to_standard_table, "k = {a = 1}\n[t]\nx = 1\n", "k")

    assert dumps(doc) == "[k]\na = 1\n\n[t]\nx = 1\n"


def test_to_standard_table_nested_in_table():
    content = "[t]\nx = 1\ns = {y = 2}\n\n[t.u]\nz = 3\n"
    doc = convert(to_standard_table, content, "t.s")

    assert dumps(doc) == "[t]\nx = 1\n\n[t.s]\ny = 2\n\n[t.u]\nz = 3\n"


def test_to_standard_table_for_a_dotted_key_value():
    doc = convert(to_standard_table, "a.x = 1\na.b = {c = 1}  # cc\n", "a.b")

    assert dumps(doc) == "a.x = 1\n\n[a.b]  # cc\nc = 1\n"
    assert doc["a"]["b"]["c"] == 1


def test_to_standard_table_keeps_dotted_keys():
    doc = convert(to_standard_table, "s = {a.b = 1, c = 2}\n", "s")

    assert dumps(doc) == "[s]\na.b = 1\nc = 2\n"


def test_to_standard_table_empty_inline_table():
    doc = convert(to_standard_table, "s = {}\n", "s")

    assert dumps(doc) == "[s]\n"


def test_to_standard_table_is_a_noop_for_tables():
    content = "[a]\nx = 1\n"
    doc = convert(to_standard_table, content, "a")

    assert dumps(doc) == content


@pytest.mark.parametrize(
    "content, path",
    [
        ("a = 1\n", "a"),
        ("[a]\nx = 1\n", "a.x"),
        ("x = {a = {b = 1}}\n", "x.a"),
        ("a = [{b = 1}]\n", "a"),
    ],
)
def test_to_standard_table_errors(content, path):
    doc = parse(content)

    with pytest.raises(ConversionError):
        to_standard_table(path, doc)

    assert dumps(doc) == content


def test_to_dotted_keys():
    content = """\
[server]  # main
host = "a"   # host comment
# port section
port = 1

[server.tls]  # tls
cert = "c"

[db]
name = "d"
"""
    expected = """\
# main
server.host = "a"   # host comment
# port section
server.port = 1

# tls
server.tls.cert = "c"

[db]
name = "d"
"""
    doc = convert(to_dotted_keys, content, "server")

    assert dumps(doc) == expected


@pytest.mark.parametrize(
    "max_depth, expected",
    [
        (
            None,
            'server.host = "a"\nserver.tls.cert = "c"\nserver.tls.x.y = 1\n',
        ),
        (
            1,
            'server.host = "a"\nserver.tls = {cert = "c", x = {y = 1}}\n',
        ),
        (
            2,
            'server.host = "a"\nserver.tls.cert = "c"\nserver.tls.x = {y = 1}\n',
        ),
        (
            3,
            'server.host = "a"\nserver.tls.cert = "c"\nserver.tls.x.y = 1\n',
        ),
    ],
)
def test_to_dotted_keys_max_depth(max_depth, expected):
    content = '[server]\nhost = "a"\n[server.tls]\ncert = "c"\n[server.tls.x]\ny = 1\n'
    doc = convert(to_dotted_keys, content, "server", max_depth=max_depth)

    assert dumps(doc) == expected


def test_to_dotted_keys_keeps_nested_header_comments_on_inline_values():
    content = '[server]\nhost = "a"\n[server.tls]  # tls\ncert = "c"\n'
    doc = convert(to_dotted_keys, content, "server", max_depth=1)

    assert dumps(doc) == 'server.host = "a"\nserver.tls = {cert = "c"}  # tls\n'


def test_to_dotted_keys_from_inline_table():
    doc = convert(to_dotted_keys, "srv = {a = 1, b = {c = 2}}  # s\nz = 1\n", "srv")
    assert dumps(doc) == "# s\nsrv.a = 1\nsrv.b.c = 2\nz = 1\n"

    doc = convert(to_dotted_keys, "srv = {a = 1, b = {c = 2}}\n", "srv", max_depth=1)
    assert dumps(doc) == "srv.a = 1\nsrv.b = {c = 2}\n"


def test_to_dotted_keys_writes_into_the_parent_container():
    doc = convert(to_dotted_keys, "[a.b]\nx = 1\n", "a.b")
    assert dumps(doc) == "[a]\nb.x = 1\n"

    doc = convert(to_dotted_keys, "x = {a = {b = 1}, c = 2}\n", "x.a")
    assert dumps(doc) == "x = {a.b = 1, c = 2}\n"

    doc = convert(to_dotted_keys, "[t]\nq = 1\n\n[t.s]\nx = 1\n", "t.s")
    assert dumps(doc) == "[t]\nq = 1\n\ns.x = 1\n"


def test_to_dotted_keys_next_to_dotted_keys_defining_the_parent():
    content = 'x.d = "s"\n\n[x.srv]\nport = 1\n'
    doc = convert(to_dotted_keys, content, "x.srv")

    assert dumps(doc) == 'x.d = "s"\nx.srv.port = 1\n'


def test_to_dotted_keys_places_values_before_tables():
    content = 'name = "n"\n\n[b]\ny = 1\n\n[a]\nx = 1\n'
    doc = convert(to_dotted_keys, content, "a")

    assert dumps(doc) == 'name = "n"\na.x = 1\n\n[b]\ny = 1\n'


def test_to_dotted_keys_keeps_empty_tables():
    doc = convert(to_dotted_keys, "[a]\n[b]\nx = 1\n", "a")
    assert dumps(doc) == "a = {}\n\n[b]\nx = 1\n"

    doc = convert(to_dotted_keys, "[a]\nq = 1\n[a.e]\n", "a")
    assert dumps(doc) == "a.q = 1\na.e = {}\n"


@pytest.mark.parametrize(
    "content, path, max_depth",
    [
        ("a = 1\n", "a", None),
        ("a = [1]\n", "a", None),
        ("[[a]]\nx = 1\n", "a", None),
        ("[a]\n[[a.b]]\nx = 1\n", "a", None),
        ("[a]\nx = 1\n", "a", 0),
        ("[a]\nx = 1\n", "a", -1),
        ("[a]\nx = 1\n", "a", 1.5),
    ],
)
def test_to_dotted_keys_errors(content, path, max_depth):
    doc = parse(content)

    with pytest.raises(ConversionError) as exc_info:
        to_dotted_keys(path, doc, max_depth=max_depth)

    assert exc_info.value.key_path == path
    assert dumps(doc) == content


def test_to_super_table():
    doc = convert(
        to_super_table, 'server.host = "localhost"\nserver.port = 8080\n', "server"
    )

    assert dumps(doc) == '[server]\nhost = "localhost"\nport = 8080\n'
    assert isinstance(doc["server"], Table)


def test_to_super_table_migrates_comments():
    content = """\
x = 1
# Server
server.host = "a"
# about port
server.port = 1
other = 2
# about z
server.z = 3

[t]
q = 1
"""
    expected = """\
x = 1
other = 2
# about z

[server] # Server
host = "a"
# about port
port = 1
z = 3

[t]
q = 1
"""
    doc = convert(to_super_table, content, "server")

    assert dumps(doc) == expected


def test_to_super_table_only_uses_an_adjacent_comment():
    content = "# not adjacent\n\na.x = 1\n"
    doc = convert(to_super_table, content, "a")

    assert dumps(doc) == "# not adjacent\n\n[a]\nx = 1\n"


def test_to_super_table_with_a_nested_prefix():
    content = 'tool.poetry.name = "x"\ntool.poetry.version = "1"\ntool.other = 1\n'
    doc = convert(to_super_table, content, "tool.poetry")

    assert dumps(doc) == 'tool.other = 1\n\n[tool.poetry]\nname = "x"\nversion = "1"\n'


def test_to_super_table_keeps_longer_dotted_keys():
    doc = convert(to_super_table, "a.b.c = 1\na.b.d = 2\n", "a")

    assert dumps(doc) == "[a]\nb.c = 1\nb.d = 2\n"


def test_to_super_table_inside_a_table():
    content = "[t]\nq = 1\nb.x = 1\nb.y = 2\n\n[t.z]\nw = 1\n"
    doc = convert(to_super_table, content, "t.b")

    assert dumps(doc) == "[t]\nq = 1\n\n[t.b]\nx = 1\ny = 2\n\n[t.z]\nw = 1\n"


def test_to_super_table_merges_into_an_implicit_table():
    doc = convert(to_super_table, "a.b.x = 1\n\n[a.b.q]\nw = 2\n", "a.b")

    assert dumps(doc) == "[a.b]\nx = 1\n\n[a.b.q]\nw = 2\n"


@pytest.mark.parametrize(
    "content, prefix",
    [
        ("a = 1\n", "b"),
        ("a = 1\n", "a"),
        ("a.b = 1\n", "a.b"),
        ("a = {b = 1}\n", "a"),
        ("[a]\nb = 1\n", "a"),
        ("x = {a.b = 1}\n", "x.a"),
        ("a.b = 1\n", "a.b.c"),
    ],
)
def test_to_super_table_errors(content, prefix):
    doc = parse(content)

    with pytest.raises(ConversionError) as exc_info:
        to_super_table(prefix, doc)

    assert exc_info.value.key_path == prefix
    assert dumps(doc) == content


def test_inline_and_standard_tables_round_trip():
    content = (
        'title = "x"\n\n[server]  # main\nhost = "a"\n\n[server.tls]\ncert = "c"\n'
    )
    doc = convert(to_inline_table, content, "server")
    doc = convert(to_standard_table, dumps(doc), "server")

    assert dumps(doc) == content


def test_dotted_keys_and_super_table_round_trip():
    content = '[server]  # main\nhost = "a"\nport = 1\n'
    doc = parse(content)
    to_dotted_keys("server", doc)
    to_super_table("server", doc)

    assert dumps(doc) == content


def build_document():
    doc = document()
    doc.add("title", "x")

    server = table()
    server.add("host", "a")
    tls = table()
    tls.add("cert", "c")
    server.add("tls", tls)
    server.comment("main")
    doc.add("server", server)

    db = table()
    db.add("name", "d")
    doc.add("db", db)

    return doc


@pytest.mark.parametrize(
    "conversions, expected",
    [
        (
            [(to_inline_table, "server")],
            (
                'title = "x"\nserver = {host = "a", tls = {cert = "c"}} # main\n\n'
                '[db]\nname = "d"\n'
            ),
        ),
        (
            [(to_dotted_keys, "server")],
            (
                'title = "x"\n# main\nserver.host = "a"\nserver.tls.cert = "c"\n\n'
                '[db]\nname = "d"\n'
            ),
        ),
        (
            [(to_inline_table, "server"), (to_standard_table, "server")],
            (
                'title = "x"\n\n[server] # main\nhost = "a"\n\n'
                '[server.tls]\ncert = "c"\n\n[db]\nname = "d"\n'
            ),
        ),
    ],
)
def test_conversions_on_built_documents(conversions, expected):
    doc = build_document()
    before = doc.unwrap()
    for fn, path in conversions:
        assert fn(path, doc) is doc

    assert dumps(doc) == expected
    assert doc.unwrap() == before
    assert parse(dumps(doc)).unwrap() == before


def test_to_super_table_on_built_documents():
    doc = document()
    doc.append(key(["a", "b"]), 1)
    doc.append(key(["a", "c"]), 2)
    doc.add("z", 3)

    to_super_table("a", doc)

    assert dumps(doc) == "z = 3\n\n[a]\nb = 1\nc = 2\n"


def test_to_standard_table_on_built_documents():
    doc = document()
    t = inline_table()
    t.update({"x": 1, "y": {"z": 2}})
    doc.add("t", t)

    to_standard_table("t", doc)

    assert dumps(doc) == "[t]\nx = 1\n\n[t.y]\nz = 2\n"
    assert isinstance(doc["t"]["y"], Table)


def test_document_stays_editable_after_conversion():
    doc = parse("a.x = 1\na.b = {c = 1}\n")
    to_standard_table("a.b", doc)

    doc["a"]["b"]["d"] = 2
    doc["a"]["y"] = 3

    assert doc.unwrap() == {"a": {"x": 1, "y": 3, "b": {"c": 1, "d": 2}}}
    assert parse(dumps(doc)).unwrap() == doc.unwrap()
