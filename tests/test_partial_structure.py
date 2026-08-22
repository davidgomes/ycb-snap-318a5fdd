from dataclasses import dataclass, field
from typing import Dict, List, TypedDict

import pytest
from attrs import define, field as attrs_field

from cattrs import (
    BaseConverter,
    ClassValidationError,
    Converter,
    PartialResult,
    partial_structure,
)


@define
class Inner:
    a: int
    b: int


@define
class InnerDef:
    a: int
    b: int = 0


@define
class Outer:
    inner: Inner
    x: int


@define
class WithDefaults:
    a: int
    b: int = 2


@define
class WithInitFalse:
    a: int
    hidden: int = attrs_field(init=False, default=9)


@dataclass
class DcInner:
    a: int
    b: int = 0


@dataclass
class DcOuter:
    inner: DcInner
    x: int
    skipped: int = field(init=False, default=5)


class TdRequired(TypedDict):
    a: int
    b: int


class TdPartial(TypedDict, total=False):
    a: int
    b: int


@pytest.fixture(params=[BaseConverter, Converter])
def converter(request):
    return request.param()


def test_export():
    assert callable(partial_structure)
    r = partial_structure({"a": 1, "b": 2}, Inner)
    assert isinstance(r, PartialResult)
    assert r.is_complete
    assert r.value == Inner(1, 2)
    assert r.structured_fields == frozenset({"a", "b"})
    assert r.failed_fields == frozenset()
    assert r.errors is None
    assert r.error_map == {}


def test_missing_required_makes_value_none(converter):
    r = converter.partial_structure({"a": 1}, Inner)
    assert r.value is None
    assert not r.is_complete
    assert r.structured_fields == frozenset({"a"})
    assert r.failed_fields == frozenset({"b"})
    assert "b" in r.error_map
    assert isinstance(r.error_map["b"], KeyError)


def test_missing_with_default_still_failed(converter):
    r = converter.partial_structure({"a": 1}, WithDefaults)
    assert r.value == WithDefaults(1, 2)
    assert not r.is_complete
    assert r.structured_fields == frozenset({"a"})
    assert r.failed_fields == frozenset({"b"})


def test_type_error_on_field(converter):
    r = converter.partial_structure({"a": "nope", "b": 2}, Inner)
    assert r.value is None
    assert r.structured_fields == frozenset({"b"})
    assert r.failed_fields == frozenset({"a"})
    assert "a" in r.error_map


def test_init_false_excluded(converter):
    r = converter.partial_structure({"a": 1}, WithInitFalse)
    assert r.value == WithInitFalse(1)
    assert r.structured_fields == frozenset({"a"})
    assert r.failed_fields == frozenset()
    assert r.is_complete
    r2 = converter.partial_structure({"a": 1, "hidden": 3}, WithInitFalse)
    assert "hidden" not in r2.structured_fields
    assert "hidden" not in r2.failed_fields


def test_nested_partial_uses_value_and_marks_failed(converter):
    r = converter.partial_structure({"inner": {"a": 1}, "x": 5}, Outer)
    # Inner.b required, no value -> parent field normal failure, Outer.value None
    assert r.value is None
    assert r.structured_fields == frozenset({"x"})
    assert "inner" in r.failed_fields


def test_nested_partial_with_defaults(converter):
    @define
    class O:
        inner: InnerDef
        x: int

    r = converter.partial_structure({"inner": {"a": 1}, "x": 5}, O)
    assert r.value == O(InnerDef(1, 0), 5)
    assert r.structured_fields == frozenset({"x"})
    assert r.failed_fields == frozenset({"inner"})
    assert r.value.inner.a == 1
    assert r.value.inner.b == 0


def test_collections_are_atomic(converter):
    @define
    class C:
        items: List[int]
        mapping: Dict[str, int]

    r = converter.partial_structure(
        {"items": [1, "bad"], "mapping": {"k": 1}}, C
    )
    assert r.value is None
    assert "items" in r.failed_fields
    assert r.structured_fields == frozenset({"mapping"})

    r2 = converter.partial_structure(
        {"items": [1, 2], "mapping": {"k": "bad"}}, C
    )
    assert "mapping" in r2.failed_fields
    assert r2.structured_fields == frozenset({"items"})


def test_refine_fills_failed(converter):
    r = converter.partial_structure({"a": 1}, Inner)
    assert r.value is None
    r2 = r.refine({"b": 2})
    assert r2.is_complete
    assert r2.value == Inner(1, 2)
    assert r2.structured_fields == frozenset({"a", "b"})
    assert r2.failed_fields == frozenset()


def test_refine_preserves_structured(converter):
    r = converter.partial_structure({"a": 1}, Inner)
    r2 = r.refine({"a": 99, "b": 2})
    assert r2.value == Inner(1, 2)


def test_refine_nested(converter):
    @define
    class O:
        inner: InnerDef
        x: int

    r = converter.partial_structure({"inner": {"a": 1}, "x": 5}, O)
    assert not r.is_complete
    r2 = r.refine({"inner": {"b": 7}})
    assert r2.is_complete
    assert r2.value == O(InnerDef(1, 7), 5)


def test_dataclasses(converter):
    r = converter.partial_structure({"inner": {"a": 3}, "x": 1}, DcOuter)
    assert r.value == DcOuter(DcInner(3, 0), 1)
    assert "skipped" not in r.structured_fields
    assert "skipped" not in r.failed_fields
    assert r.failed_fields == frozenset({"inner"})
    r2 = r.refine({"inner": {"b": 4}})
    assert r2.is_complete
    assert r2.value.inner == DcInner(3, 4)


def test_typeddict_required(converter):
    r = converter.partial_structure({"a": 1}, TdRequired)
    assert r.value is None
    assert r.structured_fields == frozenset({"a"})
    assert r.failed_fields == frozenset({"b"})
    r2 = r.refine({"b": 2})
    assert r2.is_complete
    assert r2.value == {"a": 1, "b": 2}


def test_typeddict_optional_absent_is_failed(converter):
    r = converter.partial_structure({"a": 1}, TdPartial)
    assert r.value == {"a": 1}
    assert r.structured_fields == frozenset({"a"})
    assert r.failed_fields == frozenset({"b"})
    assert not r.is_complete


def test_forbid_extra_keys_still_produces_value():
    c = Converter(forbid_extra_keys=True)
    r = c.partial_structure({"a": 1, "b": 2, "extra": 3}, Inner)
    assert r.value == Inner(1, 2)
    assert not r.is_complete
    assert r.failed_fields == frozenset()
    assert r.structured_fields == frozenset({"a", "b"})
    assert r.errors is not None


def test_detailed_validation_true():
    c = BaseConverter(detailed_validation=True)
    r = c.partial_structure({}, Inner)
    assert isinstance(r.errors, ClassValidationError)
    assert set(r.error_map) == {"a", "b"}


def test_detailed_validation_false():
    c = BaseConverter(detailed_validation=False)
    r = c.partial_structure({}, Inner)
    assert r.errors is not None
    assert not isinstance(r.errors, ClassValidationError)
    assert set(r.failed_fields) == {"a", "b"}
