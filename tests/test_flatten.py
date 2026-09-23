from dataclasses import dataclass, field
from typing import Optional

import pytest
from typing_extensions import Annotated

from mashumaro import DataClassDictMixin, field_options
from mashumaro.config import BaseConfig
from mashumaro.exceptions import BadFlatten, ExtraKeysError, MissingField
from mashumaro.types import Alias, SerializationStrategy


@dataclass
class Point(DataClassDictMixin):
    x: int
    y: int


def test_flatten_merges_nested_dataclass():
    @dataclass
    class Row(DataClassDictMixin):
        point: Point = field(metadata=field_options(flatten=True))
        name: str

    row = Row(Point(1, 2), "a")
    assert row.to_dict() == {"x": 1, "y": 2, "name": "a"}
    assert Row.from_dict({"x": 1, "y": 2, "name": "a"}) == row


def test_flatten_prefix_string_and_auto():
    @dataclass
    class Row(DataClassDictMixin):
        point: Point = field(
            metadata=field_options(flatten=True, flatten_prefix="pt_")
        )
        other: Point = field(
            metadata=field_options(flatten=True, flatten_prefix=True)
        )

    row = Row(Point(1, 2), Point(3, 4))
    assert row.to_dict() == {
        "pt_x": 1,
        "pt_y": 2,
        "other_x": 3,
        "other_y": 4,
    }
    assert Row.from_dict(row.to_dict()) == row


def test_flatten_rename():
    @dataclass
    class Row(DataClassDictMixin):
        point: Point = field(
            metadata=field_options(
                flatten=True, flatten_rename={"x": "px", "y": "py"}
            )
        )

    row = Row(Point(1, 2))
    assert row.to_dict() == {"px": 1, "py": 2}
    assert Row.from_dict({"px": 1, "py": 2}) == row


def test_flatten_prefix_and_rename_are_mutually_exclusive():
    with pytest.raises(BadFlatten, match="mutually exclusive"):

        @dataclass
        class Row(DataClassDictMixin):
            point: Point = field(
                metadata=field_options(
                    flatten=True,
                    flatten_prefix="p_",
                    flatten_rename={"x": "px"},
                )
            )


def test_flatten_rejects_non_dataclass():
    with pytest.raises(BadFlatten, match="not a dataclass"):

        @dataclass
        class Row(DataClassDictMixin):
            point: int = field(metadata=field_options(flatten=True))


def test_flatten_collision_with_field_name_and_alias_forms():
    with pytest.raises(BadFlatten, match="collide"):

        @dataclass
        class Row(DataClassDictMixin):
            point: Point = field(metadata=field_options(flatten=True))
            x: int

    with pytest.raises(BadFlatten, match="collide"):

        @dataclass
        class RowAlias(DataClassDictMixin):
            point: Point = field(metadata=field_options(flatten=True))
            z: int = field(metadata=field_options(alias="x"))

    with pytest.raises(BadFlatten, match="collide"):

        @dataclass
        class RowAnnotated(DataClassDictMixin):
            point: Point = field(metadata=field_options(flatten=True))
            z: Annotated[int, Alias("y")]

    with pytest.raises(BadFlatten, match="collide"):

        @dataclass
        class RowConfig(DataClassDictMixin):
            point: Point = field(metadata=field_options(flatten=True))
            z: int

            class Config(BaseConfig):
                aliases = {"z": "x"}


def test_flatten_collision_with_child_alias():
    @dataclass
    class Named(DataClassDictMixin):
        x: int = field(metadata=field_options(alias="xx"))

        class Config(BaseConfig):
            serialize_by_alias = True

    with pytest.raises(BadFlatten, match="collide"):

        @dataclass
        class Row(DataClassDictMixin):
            point: Named = field(metadata=field_options(flatten=True))
            xx: int


def test_invalid_and_duplicate_rename_keys():
    with pytest.raises(BadFlatten, match="Invalid flatten_rename key"):

        @dataclass
        class Row(DataClassDictMixin):
            point: Point = field(
                metadata=field_options(
                    flatten=True, flatten_rename={"missing": "px"}
                )
            )

    with pytest.raises(BadFlatten, match="Duplicate flatten_rename target"):

        @dataclass
        class RowDup(DataClassDictMixin):
            point: Point = field(
                metadata=field_options(
                    flatten=True, flatten_rename={"x": "z", "y": "z"}
                )
            )


def test_flattened_child_keeps_its_config():
    class Upper(SerializationStrategy):
        def serialize(self, value: str) -> str:
            return value.upper()

        def deserialize(self, value: str) -> str:
            return value.lower()

    @dataclass
    class Inner(DataClassDictMixin):
        label: str = field(
            metadata=field_options(
                alias="label_name", serialization_strategy=Upper()
            )
        )
        note: Optional[str] = None

        class Config(BaseConfig):
            serialize_by_alias = True
            omit_none = True

    @dataclass
    class Row(DataClassDictMixin):
        inner: Inner = field(metadata=field_options(flatten=True))
        n: int

    row = Row(Inner("ab", None), 1)
    assert row.to_dict() == {"label_name": "AB", "n": 1}
    assert Row.from_dict({"label_name": "AB", "n": 1}) == Row(Inner("ab"), 1)


def test_forbid_extra_keys_accounts_for_flattened_keys():
    @dataclass
    class Row(DataClassDictMixin):
        point: Point = field(
            metadata=field_options(flatten=True, flatten_prefix=True)
        )
        name: str

        class Config(BaseConfig):
            forbid_extra_keys = True

    assert Row.from_dict({"point_x": 1, "point_y": 2, "name": "a"}) == Row(
        Point(1, 2), "a"
    )
    with pytest.raises(ExtraKeysError) as exc_info:
        Row.from_dict(
            {"point_x": 1, "point_y": 2, "name": "a", "point": {}, "extra": 1}
        )
    assert exc_info.value.extra_keys == {"point", "extra"}


def test_optional_flattened_field():
    @dataclass
    class Row(DataClassDictMixin):
        point: Optional[Point] = field(
            default=None, metadata=field_options(flatten=True)
        )
        name: str = "n"

    assert Row(None, "n").to_dict() == {"name": "n"}
    assert Row.from_dict({"name": "n"}) == Row(None, "n")
    assert Row.from_dict({"x": 1, "y": 2, "name": "n"}) == Row(
        Point(1, 2), "n"
    )
    with pytest.raises(MissingField):
        Row.from_dict({"x": 1, "name": "n"})


def test_flatten_without_mixin_on_child():
    @dataclass
    class Plain:
        x: int
        y: str

    @dataclass
    class Row(DataClassDictMixin):
        plain: Plain = field(metadata=field_options(flatten=True))

    row = Row(Plain(1, "a"))
    assert row.to_dict() == {"x": 1, "y": "a"}
    assert Row.from_dict({"x": 1, "y": "a"}) == row


def test_partial_rename_keeps_other_child_keys():
    @dataclass
    class Row(DataClassDictMixin):
        point: Point = field(
            metadata=field_options(flatten=True, flatten_rename={"x": "px"})
        )

    row = Row(Point(1, 2))
    assert row.to_dict() == {"px": 1, "y": 2}
    assert Row.from_dict({"px": 1, "y": 2}) == row
