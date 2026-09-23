import pytest

import tomlkit

from tomlkit.exceptions import ConversionError
from tomlkit.items import InlineTable
from tomlkit.items import Table


def _rt(doc):
    again = tomlkit.parse(tomlkit.dumps(doc))
    assert again.unwrap() == doc.unwrap()
    return again


def test_exports():
    assert tomlkit.to_inline_table is not None
    assert tomlkit.to_standard_table is not None
    assert tomlkit.to_dotted_keys is not None
    assert tomlkit.to_super_table is not None


def test_to_inline_table_nested_and_comment():
    doc = tomlkit.parse(
        """\
[server] # main
host = "localhost" # h
port = 8080

[server.tls]
enabled = true
"""
    )
    same = tomlkit.to_inline_table("server", doc)
    assert same is doc
    assert isinstance(doc["server"], InlineTable)
    text = doc.as_string()
    assert "server" in text
    assert "{" in text
    _rt(doc)
    assert doc.unwrap() == {
        "server": {"host": "localhost", "port": 8080, "tls": {"enabled": True}}
    }


def test_to_inline_noop_and_errors():
    doc = tomlkit.parse('a = { b = 1 }\n')
    assert tomlkit.to_inline_table("a", doc) is doc
    assert isinstance(doc["a"], InlineTable)

    doc = tomlkit.parse("a = 1\n")
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_inline_table("a", doc)
    assert exc.value.key_path == "a"

    doc = tomlkit.parse("a = 1\n")
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_inline_table("a.b", doc)
    assert exc.value.key_path == "a.b"

    doc = tomlkit.parse("[a]\nb = 1\n")
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_inline_table("missing", doc)
    assert exc.value.key_path == "missing"

    doc = tomlkit.parse(
        """\
[a]
[[a.items]]
name = "x"
"""
    )
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_inline_table("a", doc)
    assert exc.value.key_path == "a"


def test_to_standard_table_moves_comment():
    doc = tomlkit.parse('point = { x = 1, y = 2 } # origin\n')
    assert tomlkit.to_standard_table("point", doc) is doc
    assert isinstance(doc["point"], Table)
    text = doc.as_string()
    assert "[point]" in text
    assert "# origin" in text
    _rt(doc)
    assert doc.unwrap() == {"point": {"x": 1, "y": 2}}

    doc = tomlkit.parse("[point]\nx = 1\n")
    assert tomlkit.to_standard_table("point", doc) is doc
    assert isinstance(doc["point"], Table)

    doc = tomlkit.parse("a = 1\n")
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_standard_table("a", doc)
    assert exc.value.key_path == "a"


def test_nested_inline_becomes_nested_tables():
    doc = tomlkit.parse("a = { b = 1, c = { d = 2 } }\n")
    tomlkit.to_standard_table("a", doc)
    _rt(doc)
    assert doc.unwrap() == {"a": {"b": 1, "c": {"d": 2}}}
    assert isinstance(doc["a"], Table)
    assert isinstance(doc["a"]["c"], Table)


def test_to_dotted_keys_depth_and_comment():
    doc = tomlkit.parse(
        """\
[server] # svc
host = "localhost"
port = 80

[server.tls]
enabled = true
"""
    )
    tomlkit.to_dotted_keys("server", doc)
    _rt(doc)
    assert doc.unwrap() == {
        "server": {"host": "localhost", "port": 80, "tls": {"enabled": True}}
    }
    text = doc.as_string()
    assert "# svc" in text
    assert "server.host" in text
    assert "server.tls.enabled" in text

    doc = tomlkit.parse(
        """\
[server]
host = "localhost"

[server.tls]
enabled = true
"""
    )
    tomlkit.to_dotted_keys("server", doc, max_depth=1)
    _rt(doc)
    assert doc.unwrap()["server"]["host"] == "localhost"
    assert doc.unwrap()["server"]["tls"]["enabled"] is True
    assert "server.host" in doc.as_string()
    assert "[server.tls]" in doc.as_string()

    doc = tomlkit.parse("a = 1\n")
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_dotted_keys("a", doc)
    assert exc.value.key_path == "a"


def test_to_super_table_groups_and_comment():
    doc = tomlkit.parse(
        """\
# grouped
server.tls.enabled = true
server.tls.port = 443
server.host = "localhost"
"""
    )
    assert tomlkit.to_super_table("server.tls", doc) is doc
    _rt(doc)
    assert doc.unwrap() == {
        "server": {"tls": {"enabled": True, "port": 443}, "host": "localhost"}
    }
    text = doc.as_string()
    assert "[server.tls]" in text
    assert "# grouped" in text
    assert "enabled" in text

    doc = tomlkit.parse('a = 1\n')
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_super_table("a", doc)
    assert exc.value.key_path == "a"


def test_round_trip_all_forms():
    original = {
        "name": "app",
        "server": {"host": "localhost", "port": 1, "tls": {"on": True}},
    }
    doc = tomlkit.parse(
        """\
name = "app"

[server]
host = "localhost"
port = 1

[server.tls]
on = true
"""
    )
    tomlkit.to_inline_table("server", doc)
    _rt(doc)
    tomlkit.to_standard_table("server", doc)
    _rt(doc)
    tomlkit.to_dotted_keys("server", doc)
    _rt(doc)
    tomlkit.to_super_table("server", doc)
    _rt(doc)
    assert doc.unwrap() == original


def test_conversions_do_not_fall_into_neighbor_tables():
    doc = tomlkit.parse(
        """\
[other]
x = 1

[server]
host = "localhost"
"""
    )
    tomlkit.to_dotted_keys("server", doc)
    _rt(doc)
    assert doc.unwrap() == {"other": {"x": 1}, "server": {"host": "localhost"}}

    doc = tomlkit.parse(
        """\
point = { x = 1, y = 2 }
name = "app"
"""
    )
    tomlkit.to_standard_table("point", doc)
    _rt(doc)
    assert doc.unwrap() == {"point": {"x": 1, "y": 2}, "name": "app"}


def test_quoted_key_path():
    doc = tomlkit.parse('[a."b.c"]\nd = 1\n')
    tomlkit.to_inline_table('a."b.c"', doc)
    _rt(doc)
    assert doc.unwrap() == {"a": {"b.c": {"d": 1}}}
