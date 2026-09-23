from __future__ import annotations

import pytest

import tomlkit

from tomlkit import parse
from tomlkit.convert import to_dotted_keys
from tomlkit.convert import to_inline_table
from tomlkit.convert import to_standard_table
from tomlkit.convert import to_super_table
from tomlkit.exceptions import ConversionError
from tomlkit.items import InlineTable
from tomlkit.items import Table


def _rt(doc):
    again = parse(tomlkit.dumps(doc))
    assert again.unwrap() == doc.unwrap()
    return again


def test_functions_are_exported_from_package_and_convert_module():
    assert tomlkit.to_inline_table is to_inline_table
    assert tomlkit.to_standard_table is to_standard_table
    assert tomlkit.to_dotted_keys is to_dotted_keys
    assert tomlkit.to_super_table is to_super_table


def test_to_inline_table_converts_nested_tables_and_preserves_values():
    doc = parse(
        """
[foo]
a = 1
b = 2

[foo.bar]
c = 3

[foo.bar.baz]
d = 4
"""
    )
    assert to_inline_table("foo", doc) is doc
    assert isinstance(doc["foo"], InlineTable)
    _rt(doc)
    assert doc.unwrap() == {"foo": {"a": 1, "b": 2, "bar": {"c": 3, "baz": {"d": 4}}}}
    assert "foo = {" in doc.as_string()
    assert "[foo" not in doc.as_string()


def test_to_inline_table_is_noop_for_inline_table():
    source = "foo = {a = 1, b = {c = 2}}\n"
    doc = parse(source)
    assert to_inline_table("foo", doc) is doc
    assert to_inline_table("foo.b", doc) is doc
    assert doc.as_string() == source


def test_to_inline_table_migrates_header_and_value_comments():
    doc = parse(
        """
[foo] # header
a = 1 # aa

[foo.bar] # bar
c = 3
"""
    )
    to_inline_table("foo", doc)
    rendered = doc.as_string()
    assert "# header" in rendered
    assert "# aa" in rendered
    assert "# bar" in rendered
    _rt(doc)
    assert doc["foo"].trivia.comment == "# header"


def test_to_inline_table_rejects_aot_descendants():
    doc = parse(
        """
[foo]
a = 1

[[foo.bar]]
b = 2
"""
    )
    with pytest.raises(ConversionError) as exc:
        to_inline_table("foo", doc)
    assert exc.value.key_path == "foo"
    assert isinstance(exc.value, tomlkit.exceptions.TOMLKitError)


def test_to_inline_table_rejects_missing_and_non_tables():
    doc = parse(
        """
a = 1

[foo]
b = 2
"""
    )
    with pytest.raises(ConversionError) as missing:
        to_inline_table("nope", doc)
    assert missing.value.key_path == "nope"

    with pytest.raises(ConversionError) as nested:
        to_inline_table("a.b", doc)
    assert nested.value.key_path == "a.b"

    with pytest.raises(ConversionError) as scalar:
        to_inline_table("a", doc)
    assert scalar.value.key_path == "a"

    with pytest.raises(ConversionError) as mid:
        to_inline_table("foo.b.c", doc)
    assert mid.value.key_path == "foo.b.c"


def test_to_standard_table_converts_nested_inline_tables_and_comment():
    doc = parse("foo = {a = 1, b = {c = 2, d = {e = 3}}} # hi\n")
    assert to_standard_table("foo", doc) is doc
    assert isinstance(doc["foo"], Table)
    rendered = doc.as_string()
    assert "[foo] # hi" in rendered
    assert "[foo.b]" in rendered
    assert "[foo.b.d]" in rendered
    assert doc["foo"].trivia.comment == "# hi"
    _rt(doc)
    assert doc.unwrap() == {"foo": {"a": 1, "b": {"c": 2, "d": {"e": 3}}}}


def test_to_standard_table_is_noop_for_standard_table():
    source = "[foo]\na = 1\n"
    doc = parse(source)
    assert to_standard_table("foo", doc) is doc
    assert isinstance(doc["foo"], Table)
    _rt(doc)


def test_to_standard_table_nested_under_standard_parent():
    doc = parse(
        """
[parent]
before = 1
foo = {a = 1, b = {c = 2}} # hi
after = 2
"""
    )
    to_standard_table("parent.foo", doc)
    rendered = doc.as_string()
    assert "[parent.foo] # hi" in rendered
    assert "[parent.foo.b]" in rendered
    assert "before = 1" in rendered
    assert "after = 2" in rendered
    _rt(doc)
    assert doc.unwrap()["parent"]["after"] == 2
    assert doc.unwrap()["parent"]["foo"]["b"]["c"] == 2


def test_to_standard_table_rejects_non_inline():
    doc = parse("a = 1\n")
    with pytest.raises(ConversionError) as exc:
        to_standard_table("a", doc)
    assert exc.value.key_path == "a"
    with pytest.raises(ConversionError) as missing:
        to_standard_table("missing.child", doc)
    assert missing.value.key_path == "missing.child"


def test_to_dotted_keys_flattens_table_and_moves_header_comment():
    doc = parse(
        """
[foo] # header
a = 1 # aa
b = 2

[foo.bar] # bar
c = 3
"""
    )
    assert to_dotted_keys("foo", doc) is doc
    rendered = doc.as_string()
    assert "foo.a = 1 # aa" in rendered
    assert "foo.b = 2" in rendered
    assert "foo.bar.c = 3" in rendered
    assert "[foo]" not in rendered
    assert rendered.strip().startswith("# header")
    assert "# bar" in rendered
    _rt(doc)


def test_to_dotted_keys_max_depth_limits_flattening():
    doc = parse(
        """
[foo]
a = 1

[foo.bar]
b = 1

[foo.bar.baz]
c = 1
"""
    )
    to_dotted_keys("foo", doc, max_depth=1)
    rendered = doc.as_string()
    assert "foo.a = 1" in rendered
    assert "[foo.bar]" in rendered
    assert "[foo.bar.baz]" in rendered
    assert "foo.bar.b =" not in rendered
    _rt(doc)

    doc = parse(
        """
[foo]
a = 1

[foo.bar]
b = 1

[foo.bar.baz]
c = 1
"""
    )
    to_dotted_keys("foo", doc, max_depth=2)
    rendered = doc.as_string()
    assert "foo.a = 1" in rendered
    assert "foo.bar.b = 1" in rendered
    assert "[foo.bar.baz]" in rendered
    assert "foo.bar.baz.c" not in rendered
    _rt(doc)


def test_to_dotted_keys_flattens_inline_table():
    doc = parse("foo = {a = 1, b = {c = 2}} # hi\nbar = 3\n")
    to_dotted_keys("foo", doc)
    rendered = doc.as_string()
    assert "foo.a = 1" in rendered
    assert "foo.b.c = 2" in rendered
    assert "# hi" in rendered
    assert "bar = 3" in rendered
    _rt(doc)

    doc = parse("foo = {a = 1, b = {c = 2}}\n")
    to_dotted_keys("foo", doc, max_depth=1)
    rendered = doc.as_string()
    assert "foo.a = 1" in rendered
    assert "foo.b = {c = 2}" in rendered
    _rt(doc)


def test_to_dotted_keys_rejects_scalars():
    doc = parse("a = 1\n")
    with pytest.raises(ConversionError) as exc:
        to_dotted_keys("a", doc)
    assert exc.value.key_path == "a"
    with pytest.raises(ConversionError) as missing:
        to_dotted_keys("no.such", doc)
    assert missing.value.key_path == "no.such"


def test_to_super_table_groups_dotted_keys_and_takes_preceding_comment():
    doc = parse(
        """
# header
foo.a = 1 # aa
foo.b = 2
foo.bar.c = 3
other = 4
"""
    )
    assert to_super_table("foo", doc) is doc
    rendered = doc.as_string()
    assert "[foo] # header" in rendered
    assert "a = 1 # aa" in rendered
    assert "b = 2" in rendered
    assert "bar.c = 3" in rendered
    assert "other = 4" in rendered
    _rt(doc)
    assert doc.unwrap() == {
        "foo": {"a": 1, "b": 2, "bar": {"c": 3}},
        "other": 4,
    }


def test_to_super_table_nested_prefix():
    doc = parse(
        """
foo.bar.a = 1
foo.e = 2
foo.bar.b = 3
"""
    )
    to_super_table("foo.bar", doc)
    rendered = doc.as_string()
    assert "[foo.bar]" in rendered
    assert "foo.e = 2" in rendered
    _rt(doc)
    assert doc.unwrap() == {"foo": {"bar": {"a": 1, "b": 3}, "e": 2}}


def test_to_super_table_inside_parent_table():
    doc = parse(
        """
[parent]
# note
foo.a = 1
foo.b = 2
z = 3
"""
    )
    to_super_table("parent.foo", doc)
    rendered = doc.as_string()
    assert "[parent.foo] # note" in rendered
    assert "z = 3" in rendered
    _rt(doc)
    assert doc.unwrap() == {"parent": {"foo": {"a": 1, "b": 2}, "z": 3}}


def test_to_super_table_requires_matching_dotted_keys():
    doc = parse(
        """
[foo]
a = 1
"""
    )
    with pytest.raises(ConversionError) as exc:
        to_super_table("foo", doc)
    assert exc.value.key_path == "foo"

    doc = parse("a = 1\n")
    with pytest.raises(ConversionError) as missing:
        to_super_table("a.b", doc)
    assert missing.value.key_path == "a.b"

    with pytest.raises(ConversionError) as absent:
        to_super_table("missing", doc)
    assert absent.value.key_path == "missing"


def test_round_trip_between_dotted_and_super_table():
    doc = parse(
        """
[foo] # header
a = 1
b = 2
"""
    )
    to_dotted_keys("foo", doc)
    to_super_table("foo", doc)
    rendered = doc.as_string()
    assert "[foo] # header" in rendered
    assert "a = 1" in rendered
    assert "b = 2" in rendered
    _rt(doc)


def test_to_standard_table_hoists_inline_parent_without_capturing_siblings():
    doc = parse("foo = {a = {b = 1}, c = 2} # outer\n")
    to_standard_table("foo.a", doc)
    rendered = doc.as_string()
    assert "[foo] # outer" in rendered
    assert "[foo.a]" in rendered
    _rt(doc)
    assert doc.unwrap() == {"foo": {"a": {"b": 1}, "c": 2}}


def test_to_inline_table_merges_dotted_children():
    doc = parse(
        """
[foo]
a = 1
b.x = 1
b.y = 2
"""
    )
    to_inline_table("foo", doc)
    assert doc.unwrap() == {"foo": {"a": 1, "b": {"x": 1, "y": 2}}}
    _rt(doc)
    assert "b = {x = 1, y = 2}" in doc.as_string()


def test_quoted_key_path():
    doc = parse(
        """
[foo."bar baz"]
a = 1
"""
    )
    to_inline_table('foo."bar baz"', doc)
    assert doc.unwrap() == {"foo": {"bar baz": {"a": 1}}}
    _rt(doc)
