import pytest

import tomlkit

from tomlkit import parse
from tomlkit.exceptions import ConversionError
from tomlkit.items import InlineTable
from tomlkit.items import Table


def _stable(doc) -> None:
    rendered = tomlkit.dumps(doc)
    again = parse(rendered)
    assert tomlkit.dumps(again) == rendered
    assert again.unwrap() == doc.unwrap()


def test_to_inline_table_nests_and_moves_header_comment():
    doc = parse(
        """\
[server] # main
host = "localhost" # h
port = 8080

[server.db] # database
user = "root"
"""
    )
    assert tomlkit.to_inline_table("server", doc) is doc
    _stable(doc)
    assert isinstance(doc["server"], InlineTable)
    assert doc["server"].trivia.comment == "# main"
    assert doc.unwrap()["server"]["host"] == "localhost"
    assert doc["server"]["db"]["user"] == "root"
    assert "database" in doc.as_string()
    assert "[server]" not in doc.as_string()


def test_to_inline_table_noop_and_errors():
    doc = parse("point = {x = 1, y = 2}\n")
    assert tomlkit.to_inline_table("point", doc) is doc
    assert isinstance(doc["point"], InlineTable)

    original = 'name = "ada"\n'
    doc = parse(original)
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_inline_table("name", doc)
    assert exc.value.key_path == "name"
    assert doc.as_string() == original

    doc = parse("a = 1\n")
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_inline_table("a.b", doc)
    assert exc.value.key_path == "a.b"

    doc = parse("missing = 1\n")
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_inline_table("nope", doc)
    assert exc.value.key_path == "nope"

    doc = parse("[[arr]]\nx = 1\n")
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_inline_table("arr", doc)
    assert exc.value.key_path == "arr"

    doc = parse("[server]\nhost = 'x'\n\n[[server.arr]]\nx = 1\n")
    before = doc.as_string()
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_inline_table("server", doc)
    assert exc.value.key_path == "server"
    assert doc.as_string() == before


def test_to_standard_table_moves_inline_comment():
    doc = parse("point = { x = 1, y = 2 } # coord\n")
    assert tomlkit.to_standard_table("point", doc) is doc
    _stable(doc)
    assert isinstance(doc["point"], Table)
    assert doc["point"].trivia.comment == "# coord"
    assert doc.unwrap() == {"point": {"x": 1, "y": 2}}
    assert doc.as_string().splitlines()[0] == "[point] # coord"

    again = doc.as_string()
    assert tomlkit.to_standard_table("point", doc) is doc
    assert doc.as_string() == again


def test_to_standard_table_nested_inline():
    doc = parse("d = { e = 2, f = { g = 3 } }\n")
    tomlkit.to_standard_table("d", doc)
    _stable(doc)
    assert doc.unwrap() == {"d": {"e": 2, "f": {"g": 3}}}
    rendered = doc.as_string()
    assert "[d]" in rendered
    assert "[d.f]" in rendered


def test_to_standard_table_errors():
    doc = parse("a = 1\n")
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_standard_table("missing", doc)
    assert exc.value.key_path == "missing"
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_standard_table("a", doc)
    assert exc.value.key_path == "a"


def test_to_dotted_keys_flattens_and_keeps_header_comment():
    doc = parse(
        """\
[server] # main
host = "localhost" # h

[server.db] # database
user = "root"
"""
    )
    assert tomlkit.to_dotted_keys("server", doc) is doc
    _stable(doc)
    text = doc.as_string()
    assert text.startswith("# main\n")
    assert 'server.host = "localhost" # h\n' in text
    assert 'server.db.user = "root"' in text
    assert "database" in text
    assert doc.unwrap() == {"server": {"host": "localhost", "db": {"user": "root"}}}


def test_to_dotted_keys_max_depth():
    doc = parse(
        """\
[server]
host = "localhost"

[server.db]
user = "root"

[server.db.pool]
size = 4
"""
    )
    tomlkit.to_dotted_keys("server", doc, max_depth=1)
    _stable(doc)
    text = doc.as_string()
    assert 'server.host = "localhost"' in text
    assert "[server.db]" in text
    assert "server.db.user" not in text
    assert doc.unwrap()["server"]["db"]["pool"]["size"] == 4

    doc = parse(
        """\
[server]
host = "localhost"

[server.db]
user = "root"

[server.db.pool]
size = 4
"""
    )
    tomlkit.to_dotted_keys("server", doc, max_depth=2)
    _stable(doc)
    text = doc.as_string()
    assert 'server.db.user = "root"' in text
    assert "[server.db.pool]" in text
    assert "server.db.pool.size" not in text


def test_to_dotted_keys_from_inline_and_errors():
    doc = parse("point = { x = 1, y = 2 } # coord\n")
    tomlkit.to_dotted_keys("point", doc)
    _stable(doc)
    assert doc.as_string().startswith("# coord\n")
    assert doc.unwrap() == {"point": {"x": 1, "y": 2}}

    doc = parse("a = 1\n")
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_dotted_keys("a", doc)
    assert exc.value.key_path == "a"
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_dotted_keys("a", doc, max_depth=0)
    assert exc.value.key_path == "a"


def test_to_super_table_groups_dotted_keys_and_comment():
    doc = parse(
        """\
# header
a.b.c = 1
a.b.d = 2 # d
a.e = 3
"""
    )
    assert tomlkit.to_super_table("a.b", doc) is doc
    _stable(doc)
    text = doc.as_string()
    assert "[a.b] # header" in text
    assert "c = 1" in text
    assert "d = 2 # d" in text
    assert "a.e = 3" in text
    assert doc.unwrap() == {"a": {"b": {"c": 1, "d": 2}, "e": 3}}


def test_to_super_table_inside_parent_and_missing():
    doc = parse(
        """\
[server]
host = "localhost"
db.user = "root"
db.timeout = 30
"""
    )
    tomlkit.to_super_table("db", doc)
    _stable(doc)
    text = doc.as_string()
    assert "[server.db]" in text
    assert doc.unwrap() == {
        "server": {"host": "localhost", "db": {"user": "root", "timeout": 30}}
    }

    doc = parse("a = 1\n")
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_super_table("a.b", doc)
    assert exc.value.key_path == "a.b"


def test_round_trip_between_representations():
    source = parse(
        """\
[point] # coord
x = 1
y = 2
"""
    )
    original = source.unwrap()
    tomlkit.to_inline_table("point", source)
    tomlkit.to_standard_table("point", source)
    _stable(source)
    assert source.unwrap() == original
    assert source["point"].trivia.comment == "# coord"

    source = parse(
        """\
[point] # coord
x = 1
y = 2
"""
    )
    tomlkit.to_dotted_keys("point", source)
    tomlkit.to_super_table("point", source)
    _stable(source)
    assert source.unwrap() == original
    assert source["point"].trivia.comment == "# coord"
