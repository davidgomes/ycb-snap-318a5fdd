from dataclasses import dataclass, field
from typing import Optional

import pytest
from typing_extensions import Annotated

from mashumaro import DataClassDictMixin, field_options
from mashumaro.config import BaseConfig
from mashumaro.exceptions import BadFlatten, ExtraKeysError, MissingField
from mashumaro.types import Alias


@dataclass
class Point(DataClassDictMixin):
    x: int
    y: int


def test_flatten_merges_into_parent():
    @dataclass
    class Holder(DataClassDictMixin):
        point: Point = field(metadata=field_options(flatten=True))
        label: str

    obj = Holder(Point(1, 2), "p")
    assert obj.to_dict() == {"x": 1, "y": 2, "label": "p"}
    assert Holder.from_dict({"x": 1, "y": 2, "label": "p"}) == obj


def test_flatten_prefix_string_and_auto():
    @dataclass
    class Prefixed(DataClassDictMixin):
        point: Point = field(
            metadata=field_options(flatten=True, flatten_prefix="pt_")
        )

    @dataclass
    class Auto(DataClassDictMixin):
        point: Point = field(
            metadata=field_options(flatten=True, flatten_prefix=True)
        )

    assert Prefixed(Point(1, 2)).to_dict() == {"pt_x": 1, "pt_y": 2}
    assert Prefixed.from_dict({"pt_x": 1, "pt_y": 2}) == Prefixed(Point(1, 2))
    assert Auto(Point(3, 4)).to_dict() == {"point_x": 3, "point_y": 4}
    assert Auto.from_dict({"point_x": 3, "point_y": 4}) == Auto(Point(3, 4))


def test_flatten_rename():
    @dataclass
    class Renamed(DataClassDictMixin):
        point: Point = field(
            metadata=field_options(
                flatten=True, flatten_rename={"x": "px", "y": "py"}
            )
        )

    assert Renamed(Point(1, 2)).to_dict() == {"px": 1, "py": 2}
    assert Renamed.from_dict({"px": 1, "py": 2}) == Renamed(Point(1, 2))


def test_prefix_and_rename_are_mutually_exclusive():
    with pytest.raises(BadFlatten, match="mutually exclusive"):

        @dataclass
        class Bad(DataClassDictMixin):
            point: Point = field(
                metadata=field_options(
                    flatten=True,
                    flatten_prefix="a_",
                    flatten_rename={"x": "px"},
                )
            )


def test_flatten_requires_dataclass():
    with pytest.raises(BadFlatten, match="not a dataclass"):

        @dataclass
        class Bad(DataClassDictMixin):
            point: int = field(metadata=field_options(flatten=True))


def test_invalid_and_duplicate_rename_keys():
    with pytest.raises(BadFlatten, match="invalid flatten_rename"):

        @dataclass
        class BadKey(DataClassDictMixin):
            point: Point = field(
                metadata=field_options(
                    flatten=True, flatten_rename={"nope": "px"}
                )
            )

    with pytest.raises(BadFlatten, match="duplicate flatten_rename"):

        @dataclass
        class Dup(DataClassDictMixin):
            point: Point = field(
                metadata=field_options(
                    flatten=True, flatten_rename={"x": "z", "y": "z"}
                )
            )


def test_collision_with_field_name_and_all_alias_kinds():
    with pytest.raises(BadFlatten, match="collision"):

        @dataclass
        class ByName(DataClassDictMixin):
            point: Point = field(metadata=field_options(flatten=True))
            x: int

    with pytest.raises(BadFlatten, match="collision"):

        @dataclass
        class ByMeta(DataClassDictMixin):
            point: Point = field(metadata=field_options(flatten=True))
            z: int = field(metadata=field_options(alias="x"))

    with pytest.raises(BadFlatten, match="collision"):

        @dataclass
        class ByAnnotated(DataClassDictMixin):
            point: Point = field(metadata=field_options(flatten=True))
            z: Annotated[int, Alias("y")]

    with pytest.raises(BadFlatten, match="collision"):

        @dataclass
        class ByConfig(DataClassDictMixin):
            point: Point = field(metadata=field_options(flatten=True))
            z: int

            class Config(BaseConfig):
                aliases = {"z": "x"}


def test_child_keeps_its_own_config():
    @dataclass
    class Inner(DataClassDictMixin):
        x: int = field(metadata=field_options(alias="ax"))
        y: Optional[int] = None

        class Config(BaseConfig):
            serialize_by_alias = True
            omit_none = True

    @dataclass
    class Outer(DataClassDictMixin):
        inner: Inner = field(metadata=field_options(flatten=True))
        label: str = "keep"

        class Config(BaseConfig):
            serialize_by_alias = False
            omit_none = False

    assert Outer(Inner(1, None), "keep").to_dict() == {
        "ax": 1,
        "label": "keep",
    }
    assert Outer.from_dict({"ax": 1, "label": "keep"}) == Outer(
        Inner(1, None), "keep"
    )
    assert Outer(Inner(1, 5), "keep").to_dict() == {
        "ax": 1,
        "y": 5,
        "label": "keep",
    }


def test_forbid_extra_keys_accounts_for_flattened_keys():
    @dataclass
    class Holder(DataClassDictMixin):
        point: Point = field(
            metadata=field_options(flatten=True, flatten_prefix="p_")
        )
        label: str

        class Config(BaseConfig):
            forbid_extra_keys = True

    assert Holder.from_dict({"p_x": 1, "p_y": 2, "label": "a"}) == Holder(
        Point(1, 2), "a"
    )
    with pytest.raises(ExtraKeysError):
        Holder.from_dict({"p_x": 1, "p_y": 2, "label": "a", "extra": 1})
    with pytest.raises(ExtraKeysError):
        Holder.from_dict({"point": {"x": 1, "y": 2}, "label": "a"})


def test_optional_flattened_field():
    @dataclass
    class Holder(DataClassDictMixin):
        point: Optional[Point] = field(
            default=None, metadata=field_options(flatten=True)
        )
        label: str = "l"

    assert Holder(None, "l").to_dict() == {"label": "l"}
    assert Holder.from_dict({"label": "l"}) == Holder(None, "l")
    assert Holder.from_dict({"x": 1, "y": 2, "label": "l"}) == Holder(
        Point(1, 2), "l"
    )
    with pytest.raises(MissingField):
        Holder.from_dict({"x": 1, "label": "l"})


def test_optional_without_default_roundtrip():
    @dataclass
    class Holder(DataClassDictMixin):
        point: Optional[Point] = field(metadata=field_options(flatten=True))
        label: str

    assert Holder.from_dict({"label": "l"}) == Holder(None, "l")
    assert Holder(None, "l").to_dict() == {"label": "l"}
    assert Holder(Point(1, 2), "l").to_dict() == {
        "x": 1,
        "y": 2,
        "label": "l",
    }


def test_child_alias_is_a_collision_even_if_not_serialized():
    @dataclass
    class Inner(DataClassDictMixin):
        x: int = field(metadata=field_options(alias="ax"))

    with pytest.raises(BadFlatten, match="collision"):

        @dataclass
        class Holder(DataClassDictMixin):
            inner: Inner = field(metadata=field_options(flatten=True))
            ax: int
