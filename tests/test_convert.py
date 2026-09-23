import pytest

import tomlkit

from tomlkit.exceptions import ConversionError
from tomlkit.exceptions import TOMLKitError
from tomlkit.items import InlineTable
from tomlkit.items import Table


def _rt(doc):
    rendered = doc.as_string()
    parsed = tomlkit.parse(rendered)
    assert parsed.unwrap() == doc.unwrap()
    assert tomlkit.parse(parsed.as_string()).unwrap() == doc.unwrap()
    return rendered


def test_exports_and_error_type():
    assert tomlkit.to_inline_table is not None
    assert tomlkit.to_standard_table is not None
    assert tomlkit.to_dotted_keys is not None
    assert tomlkit.to_super_table is not None
    err = ConversionError("a.b", "nope")
    assert isinstance(err, TOMLKitError)
    assert err.key_path == "a.b"


def test_to_inline_table_nested_and_comment():
    doc = tomlkit.parse(
        """
name = "app"

[server] # srv
host = "localhost"
port = 8080

[server.db] # database
name = "app"
port = 5432

[other]
x = 1
"""
    )
    original = doc.unwrap()
    result = tomlkit.to_inline_table("server", doc)
    assert result is doc
    assert isinstance(doc["server"], InlineTable)
    assert doc["other"]["x"] == 1
    text = _rt(doc)
    assert "server = {" in text
    assert "# srv" in text
    assert doc.unwrap() == original


def test_to_inline_table_noop_and_errors():
    doc = tomlkit.parse('point = { x = 1, y = 2 }\nname = "ok"\n')
    before = doc.as_string()
    assert tomlkit.to_inline_table("point", doc) is doc
    assert doc.as_string() == before

    missing = tomlkit.parse("a = 1\n")
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_inline_table("missing", missing)
    assert exc.value.key_path == "missing"

    scalar = tomlkit.parse("a = 1\n")
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_inline_table("a", scalar)
    assert exc.value.key_path == "a"

    nested = tomlkit.parse("a = 1\n")
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_inline_table("a.b", nested)
    assert exc.value.key_path == "a.b"

    aot = tomlkit.parse(
        """
[fruit]
name = "apple"

[[fruit.varieties]]
name = "fuji"
"""
    )
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_inline_table("fruit", aot)
    assert exc.value.key_path == "fruit"

    with pytest.raises(ConversionError) as exc:
        tomlkit.to_inline_table("fruit.varieties", aot)
    assert exc.value.key_path == "fruit.varieties"


def test_to_standard_table_moves_comment_and_nested():
    doc = tomlkit.parse(
        """
name = "app"
nested = { a = { b = 1, c = 2 }, d = 3, e = { f = 4 } } # cmt
z = 1
"""
    )
    original = doc.unwrap()
    assert tomlkit.to_standard_table("nested", doc) is doc
    assert isinstance(doc["nested"], Table)
    text = _rt(doc)
    assert "[nested]" in text
    assert "# cmt" in text
    assert "[nested.a]" in text
    assert "[nested.e]" in text
    assert doc.unwrap() == original

    again = tomlkit.parse("point = { x = 1, y = 2 } # pt\n")
    tomlkit.to_standard_table("point", again)
    assert isinstance(again["point"], Table)
    tomlkit.to_standard_table("point", again)
    assert isinstance(again["point"], Table)
    _rt(again)


def test_to_standard_table_inside_parent_table():
    doc = tomlkit.parse(
        """
[server]
host = "localhost"
db = { name = "app", port = 5432 } # database
port = 1
"""
    )
    original = doc.unwrap()
    tomlkit.to_standard_table("server.db", doc)
    assert isinstance(doc["server"]["db"], Table)
    text = _rt(doc)
    assert "[server.db]" in text
    assert "# database" in text
    assert doc.unwrap() == original


def test_to_dotted_keys_depth_and_comment():
    doc = tomlkit.parse(
        """
[other]
x = 1

[fruit] # tasty
apple = "red" # a
banana = "yellow"

[fruit.detail]
color = "green" # g

[fruit.detail.origin]
country = "US"
"""
    )
    original = doc.unwrap()
    tomlkit.to_dotted_keys("fruit", doc)
    text = _rt(doc)
    assert "# tasty" in text
    assert 'fruit.apple = "red" # a' in text
    assert 'fruit.detail.color = "green" # g' in text
    assert 'fruit.detail.origin.country = "US"' in text
    assert doc.unwrap() == original
    assert doc["other"]["x"] == 1

    limited = tomlkit.parse(
        """
[a]
b = 1

[a.c]
d = 2

[a.c.e]
f = 3
"""
    )
    values = limited.unwrap()
    tomlkit.to_dotted_keys("a", limited, max_depth=1)
    rendered = _rt(limited)
    assert "a.b = 1" in rendered
    assert "[a.c]" in rendered
    assert "f = 3" in rendered
    assert "a.c.d" not in rendered
    assert limited.unwrap() == values

    depth_two = tomlkit.parse(
        """
[a]
b = 1

[a.c]
d = 2

[a.c.e]
f = 3
"""
    )
    tomlkit.to_dotted_keys("a", depth_two, max_depth=2)
    rendered = _rt(depth_two)
    assert "a.b = 1" in rendered
    assert "a.c.d = 2" in rendered
    assert "[a.c.e]" in rendered
    assert depth_two["a"]["c"]["e"]["f"] == 3


def test_to_dotted_keys_errors_and_inline():
    doc = tomlkit.parse("point = { x = 1, y = { z = 3 } } # pt\nkeep = 1\n")
    original = doc.unwrap()
    tomlkit.to_dotted_keys("point", doc)
    text = _rt(doc)
    assert "# pt" in text
    assert "point.x = 1" in text
    assert "point.y.z = 3" in text
    assert doc.unwrap() == original

    bad = tomlkit.parse("a = 1\n")
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_dotted_keys("a", bad)
    assert exc.value.key_path == "a"

    missing = tomlkit.parse("[a]\nb = { c = 1 }\n")
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_dotted_keys("nope", missing)
    assert exc.value.key_path == "nope"


def test_to_super_table_groups_dotted_keys_and_comment():
    doc = tomlkit.parse(
        """
# colors
physical.color = "orange" # c1
physical.shape = "round"
site."google.com" = true
"""
    )
    original = doc.unwrap()
    assert tomlkit.to_super_table("physical", doc) is doc
    text = _rt(doc)
    assert "[physical]" in text
    assert "# colors" in text
    assert 'color = "orange" # c1' in text
    assert 'shape = "round"' in text
    assert doc["physical"]["color"] == "orange"
    assert doc["site"]["google.com"] is True
    assert doc.unwrap() == original

    nested = tomlkit.parse(
        """
a.b.c = 1
a.b.d = 2
a.e = 3
"""
    )
    tomlkit.to_super_table("a.b", nested)
    rendered = _rt(nested)
    assert "[a.b]" in rendered
    assert nested["a"]["b"]["c"] == 1
    assert nested["a"]["b"]["d"] == 2
    assert nested["a"]["e"] == 3

    inside = tomlkit.parse(
        """
[server]
host = "localhost"
# db settings
db.name = "app" # n
db.port = 5432
"""
    )
    tomlkit.to_super_table("server.db", inside)
    rendered = _rt(inside)
    assert "[server.db]" in rendered
    assert "# db settings" in rendered
    assert inside["server"]["db"]["name"] == "app"
    assert inside["server"]["host"] == "localhost"
    assert "# n" in rendered


def test_to_super_table_errors():
    doc = tomlkit.parse('name = "app"\n[server]\nhost = "localhost"\n')
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_super_table("missing", doc)
    assert exc.value.key_path == "missing"

    with pytest.raises(ConversionError) as exc:
        tomlkit.to_super_table("server.db", doc)
    assert exc.value.key_path == "server.db"

    scalar = tomlkit.parse('server = "nope"\n')
    with pytest.raises(ConversionError) as exc:
        tomlkit.to_super_table("server.db", scalar)
    assert exc.value.key_path == "server.db"


def test_quoted_key_path_and_sibling_inline_preserved():
    doc = tomlkit.parse(
        """
a = { b = { c = 1 }, e = { f = 2 }, d = 3 }
"""
    )
    tomlkit.to_standard_table("a.b", doc)
    _rt(doc)
    assert isinstance(doc["a"], Table)
    assert isinstance(doc["a"]["b"], Table)
    assert isinstance(doc["a"]["e"], InlineTable)
    assert doc["a"]["d"] == 3
    assert doc["a"]["b"]["c"] == 1
    assert doc["a"]["e"]["f"] == 2


def test_round_trip_between_forms():
    source = """
[package]
name = "demo"

[package.author]
first = "Ada"
last = "Lovelace"
"""
    doc = tomlkit.parse(source)
    values = doc.unwrap()
    tomlkit.to_inline_table("package", doc)
    assert doc.unwrap() == values
    _rt(doc)
    tomlkit.to_standard_table("package", doc)
    assert isinstance(doc["package"], Table)
    assert isinstance(doc["package"]["author"], Table)
    assert doc.unwrap() == values
    _rt(doc)
    tomlkit.to_dotted_keys("package.author", doc, max_depth=1)
    assert doc["package"]["author"]["first"] == "Ada"
    _rt(doc)
    tomlkit.to_super_table("package.author", doc)
    assert isinstance(doc["package"]["author"], Table)
    assert doc.unwrap() == values
    _rt(doc)


def test_standard_table_keeps_dotted_keys_on_their_own_lines():
    doc = tomlkit.parse("t = { a.b = 1, c = 2, d.e = 3, f = 4 } # t\n")
    tomlkit.to_standard_table("t", doc)
    text = _rt(doc)
    assert "[t] # t" in text
    assert "a.b = 1\n" in text
    assert "d.e = 3\n" in text
    assert doc["t"]["c"] == 2
    assert doc["t"]["d"]["e"] == 3


def test_dotted_keys_inside_inline_parent_round_trip():
    doc = tomlkit.parse("point = { inner = { x = 1, y = 2 }, z = 3 }\n")
    values = doc.unwrap()
    tomlkit.to_dotted_keys("point.inner", doc)
    assert doc.unwrap() == values
    text = _rt(doc)
    assert "inner.x = 1" in text
    assert "inner.y = 2" in text
    assert doc["point"]["z"] == 3


def test_super_table_promotes_enclosing_inline_table():
    doc = tomlkit.parse("point = { x.y = 1, x.z = 2, w = 3 }\n")
    values = doc.unwrap()
    tomlkit.to_super_table("point.x", doc)
    assert doc.unwrap() == values
    text = _rt(doc)
    assert "[point.x]" in text
    assert "y = 1" in text
    assert doc["point"]["w"] == 3
    assert isinstance(doc["point"], Table)
    assert isinstance(doc["point"]["x"], Table)


def test_dotted_keys_preserve_empty_tables():
    doc = tomlkit.parse(
        """
[server] # hi

[other]
x = 1

[a]
b = 1

[a.c]

[a.d]
e = 2
"""
    )
    values = doc.unwrap()
    tomlkit.to_dotted_keys("server", doc)
    assert doc["server"].unwrap() == {}
    assert "# hi" in doc.as_string()
    tomlkit.to_dotted_keys("a", doc)
    assert doc.unwrap() == values
    text = _rt(doc)
    assert "[a.c]" in text
    assert "a.b = 1" in text
    assert doc["a"]["c"].unwrap() == {}
