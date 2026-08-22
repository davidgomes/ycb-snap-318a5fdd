from dataclasses import dataclass
from typing import Dict, List

import pytest
from attrs import define, field as attrs_field
from typing_extensions import TypedDict

from cattrs import BaseConverter, Converter, PartialResult, partial_structure
from cattrs.errors import ClassValidationError, ForbiddenExtraKeysError


@define
class Point:
    x: int
    y: int = 0


@define
class Box:
    point: Point
    label: str


@dataclass
class DCPoint:
    x: int
    y: int = 0


class TDPoint(TypedDict):
    x: int
    y: int


class TDOptional(TypedDict, total=False):
    x: int
    y: int


@define
class WithInitFalse:
    x: int
    hidden: int = attrs_field(init=False, default=99)


@define
class WithList:
    nums: List[int]
    name: str


@define
class WithDict:
    mapping: Dict[str, int]
    name: str


@pytest.fixture(params=[BaseConverter, Converter])
def converter(request):
    return request.param()


@define
class RequiredPair:
    x: int
    y: int


def test_partial_missing_required(converter):
    res = converter.partial_structure({"x": 1}, RequiredPair)
    assert res.value is None
    assert not res.is_complete
    assert res.structured_fields == frozenset({"x"})
    assert res.failed_fields == frozenset({"y"})
    assert "y" in res.error_map
    assert res.errors is not None


def test_partial_missing_optional_default_produces_value(converter):
    res = converter.partial_structure({"x": 1}, Point)
    assert res.value == Point(1, 0)
    assert not res.is_complete
    assert res.structured_fields == frozenset({"x"})
    assert res.failed_fields == frozenset({"y"})


def test_partial_missing_with_default(converter):
    res = converter.partial_structure({}, Point)
    assert res.value == Point(x=0, y=0) or res.value is None
    # x is required without a usable fallback from input; y has default
    # x has no default, so value is None
    assert res.value is None
    assert res.structured_fields == frozenset()
    assert res.failed_fields == frozenset({"x", "y"})


def test_partial_default_fallback_when_optional_missing():
    @define
    class BothDefault:
        a: int = 1
        b: int = 2

    res = BaseConverter().partial_structure({}, BothDefault)
    assert res.value == BothDefault()
    assert not res.is_complete
    assert res.structured_fields == frozenset()
    assert res.failed_fields == frozenset({"a", "b"})
    assert res.errors is not None


def test_partial_success(converter):
    res = converter.partial_structure({"x": 3, "y": 4}, Point)
    assert res.value == Point(3, 4)
    assert res.is_complete
    assert res.structured_fields == frozenset({"x", "y"})
    assert res.failed_fields == frozenset()
    assert res.errors is None
    assert res.error_map == {}


def test_partial_type_error(converter):
    res = converter.partial_structure({"x": "nope", "y": 2}, Point)
    assert res.value is None
    assert not res.is_complete
    assert res.structured_fields == frozenset({"y"})
    assert res.failed_fields == frozenset({"x"})
    assert "x" in res.error_map


def test_nested_partial_uses_value_and_marks_failed(converter):
    res = converter.partial_structure(
        {"point": {"x": 1, "y": 2}, "label": "ok"}, Box
    )
    assert res.is_complete
    assert res.value == Box(Point(1, 2), "ok")

    res = converter.partial_structure({"point": {"x": 1}, "label": "ok"}, Box)
    # Inner Point.y has a default, so a nested value is produced and used,
    # but the parent field is still marked failed.
    assert res.value == Box(Point(1, 0), "ok")
    assert "point" in res.failed_fields
    assert res.structured_fields == frozenset({"label"})


def test_nested_no_value_is_normal_failure(converter):
    @define
    class Inner:
        x: int
        y: int

    @define
    class Outer:
        inner: Inner
        z: int

    res = converter.partial_structure({"inner": {"x": 1}, "z": 2}, Outer)
    assert res.value is None
    assert res.structured_fields == frozenset({"z"})
    assert res.failed_fields == frozenset({"inner"})
    assert "inner" in res.error_map


def test_nested_partial_with_defaults():
    @define
    class Inner:
        x: int = 0
        y: int = 0

    @define
    class Outer:
        inner: Inner
        z: int

    conv = BaseConverter()
    res = conv.partial_structure({"inner": {"x": 5}, "z": 9}, Outer)
    assert res.value == Outer(Inner(5, 0), 9)
    assert not res.is_complete
    assert res.structured_fields == frozenset({"z"})
    assert res.failed_fields == frozenset({"inner"})
    assert "inner" in res.error_map


def test_list_is_atomic(converter):
    res = converter.partial_structure({"nums": [1, "bad", 3], "name": "n"}, WithList)
    assert res.value is None
    assert res.structured_fields == frozenset({"name"})
    assert res.failed_fields == frozenset({"nums"})
    assert "nums" in res.error_map


def test_dict_is_atomic(converter):
    res = converter.partial_structure(
        {"mapping": {"a": 1, "b": "no"}, "name": "n"}, WithDict
    )
    assert res.structured_fields == frozenset({"name"})
    assert res.failed_fields == frozenset({"mapping"})


def test_init_false_excluded(converter):
    res = converter.partial_structure({"x": 1}, WithInitFalse)
    assert res.value == WithInitFalse(1)
    assert res.is_complete
    assert res.structured_fields == frozenset({"x"})
    assert "hidden" not in res.structured_fields
    assert "hidden" not in res.failed_fields


def test_forbid_extra_keys():
    conv = Converter(forbid_extra_keys=True)
    res = conv.partial_structure({"x": 1, "y": 2, "z": 3}, Point)
    assert res.value == Point(1, 2)
    assert not res.is_complete
    assert res.structured_fields == frozenset({"x", "y"})
    assert res.failed_fields == frozenset()
    assert isinstance(res.errors, ClassValidationError)
    assert any(isinstance(e, ForbiddenExtraKeysError) for e in res.errors.exceptions)


def test_detailed_validation_false():
    conv = BaseConverter(detailed_validation=False)
    res = conv.partial_structure({"x": "nope", "y": 1}, Point)
    assert not res.is_complete
    assert "x" in res.error_map
    assert res.errors is res.error_map["x"]
    assert not isinstance(res.errors, ClassValidationError)


def test_refine_when_value_was_none():
    conv = BaseConverter()
    first = conv.partial_structure({"x": 1}, RequiredPair)
    assert first.value is None
    refined = first.refine({"y": 2})
    assert refined.value == RequiredPair(1, 2)
    assert refined.is_complete


def test_refine_fixes_failed_preserves_structured():
    conv = BaseConverter()
    first = conv.partial_structure({"x": 1, "y": "bad"}, Point)
    assert first.structured_fields == frozenset({"x"})
    refined = first.refine({"x": 99, "y": 4})
    assert refined.value == Point(1, 4)
    assert refined.is_complete
    assert refined.structured_fields == frozenset({"x", "y"})


def test_refine_nested():
    @define
    class Inner:
        x: int = 0
        y: int = 0

    @define
    class Outer:
        inner: Inner
        z: int

    conv = BaseConverter()
    first = conv.partial_structure({"inner": {"x": 1}, "z": 3}, Outer)
    assert first.value == Outer(Inner(1, 0), 3)
    assert not first.is_complete
    refined = first.refine({"inner": {"y": 2}})
    assert refined.value == Outer(Inner(1, 2), 3)
    assert refined.is_complete


def test_dataclass(converter):
    res = converter.partial_structure({"x": 8}, DCPoint)
    assert res.value == DCPoint(8, 0)
    assert not res.is_complete
    assert res.structured_fields == frozenset({"x"})
    assert res.failed_fields == frozenset({"y"})

    res = converter.partial_structure({"x": 8, "y": 1}, DCPoint)
    assert res.value == DCPoint(8, 1)
    assert res.is_complete


@dataclass
class DCRequired:
    x: int
    y: int


def test_dataclass_required_missing(converter):
    res = converter.partial_structure({"x": 8}, DCRequired)
    assert res.value is None
    assert res.structured_fields == frozenset({"x"})
    assert res.failed_fields == frozenset({"y"})


def test_typeddict(converter):
    res = converter.partial_structure({"x": 1}, TDPoint)
    assert res.value is None
    assert res.structured_fields == frozenset({"x"})
    assert res.failed_fields == frozenset({"y"})

    res = converter.partial_structure({"x": 1, "y": 2}, TDPoint)
    assert res.value == {"x": 1, "y": 2}
    assert res.is_complete


def test_typeddict_optional_absent(converter):
    res = converter.partial_structure({}, TDOptional)
    assert res.value == {}
    assert not res.is_complete
    assert res.failed_fields == frozenset({"x", "y"})
    assert res.structured_fields == frozenset()


def test_typeddict_refine(converter):
    first = converter.partial_structure({"x": 1}, TDPoint)
    refined = first.refine({"y": 2})
    assert refined.value == {"x": 1, "y": 2}
    assert refined.is_complete


def test_top_level_export():
    res = partial_structure({"x": 1, "y": 2}, Point)
    assert isinstance(res, PartialResult)
    assert res.value == Point(1, 2)


def test_export():
    import cattrs

    assert cattrs.PartialResult is PartialResult
