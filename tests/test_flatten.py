from dataclasses import dataclass, field
from datetime import date
from typing import Generic, List, Optional, TypeVar, Union

import pytest
from typing_extensions import Annotated

from mashumaro import DataClassDictMixin, field_options
from mashumaro.codecs import BasicDecoder, BasicEncoder
from mashumaro.config import (
    ADD_DIALECT_SUPPORT,
    TO_DICT_ADD_BY_ALIAS_FLAG,
    BaseConfig,
)
from mashumaro.dialect import Dialect
from mashumaro.exceptions import (
    ExtraKeysError,
    InvalidFieldValue,
    UnserializableField,
)
from mashumaro.mixins.msgpack import DataClassMessagePackMixin
from mashumaro.mixins.orjson import DataClassORJSONMixin
from mashumaro.types import Alias

T = TypeVar("T")


@dataclass
class Point(DataClassDictMixin):
    x: int
    y: int


@dataclass
class PlainPoint:
    x: int
    y: int


@dataclass
class AliasedPoint(DataClassDictMixin):
    x: int = field(metadata=field_options(alias="X"))
    y: int = 0


@dataclass
class GenericBox(Generic[T], DataClassDictMixin):
    value: T


def flat(**kwargs):
    return field(metadata=field_options(flatten=True, **kwargs))


@dataclass
class Node:
    value: int
    next: Optional["Node"] = flat(flatten_prefix="next_")


def test_flatten():
    @dataclass
    class DataClass(DataClassDictMixin):
        name: str
        point: Point = flat()

    obj = DataClass("a", Point(1, 2))
    assert obj.to_dict() == {"name": "a", "x": 1, "y": 2}
    assert DataClass.from_dict({"name": "a", "x": 1, "y": 2}) == obj


def test_flatten_prefix_string():
    @dataclass
    class DataClass(DataClassDictMixin):
        start: Point = flat(flatten_prefix="start.")
        end: Point = flat(flatten_prefix="end.")

    obj = DataClass(Point(1, 2), Point(3, 4))
    d = {"start.x": 1, "start.y": 2, "end.x": 3, "end.y": 4}
    assert obj.to_dict() == d
    assert DataClass.from_dict(d) == obj


def test_flatten_prefix_true():
    @dataclass
    class DataClass(DataClassDictMixin):
        start: Point = flat(flatten_prefix=True)
        end: Point = flat(flatten_prefix=True)

    obj = DataClass(Point(1, 2), Point(3, 4))
    d = {"start_x": 1, "start_y": 2, "end_x": 3, "end_y": 4}
    assert obj.to_dict() == d
    assert DataClass.from_dict(d) == obj


def test_flatten_prefix_false_means_no_prefix():
    @dataclass
    class DataClass(DataClassDictMixin):
        point: Point = flat(flatten_prefix=False)

    assert DataClass(Point(1, 2)).to_dict() == {"x": 1, "y": 2}


def test_flatten_rename():
    @dataclass
    class DataClass(DataClassDictMixin):
        x: int
        point: Point = flat(flatten_rename={"x": "point_x"})

    obj = DataClass(0, Point(1, 2))
    d = {"x": 0, "point_x": 1, "y": 2}
    assert obj.to_dict() == d
    assert DataClass.from_dict(d) == obj


def test_flatten_rename_by_alias():
    @dataclass
    class DataClass(DataClassDictMixin):
        point: AliasedPoint = flat(flatten_rename={"X": "px"})

        class Config(BaseConfig):
            code_generation_options = [TO_DICT_ADD_BY_ALIAS_FLAG]

    obj = DataClass(AliasedPoint(1, 2))
    assert obj.to_dict() == {"px": 1, "y": 2}
    assert obj.to_dict(by_alias=True) == {"px": 1, "y": 2}
    assert DataClass.from_dict({"px": 1, "y": 2}) == obj


def test_flatten_rename_by_field_name_of_aliased_field():
    @dataclass
    class DataClass(DataClassDictMixin):
        point: AliasedPoint = flat(flatten_rename={"x": "px"})

    obj = DataClass(AliasedPoint(1, 2))
    assert obj.to_dict() == {"px": 1, "y": 2}
    assert DataClass.from_dict({"px": 1, "y": 2}) == obj


def test_flatten_plain_dataclass_child():
    @dataclass
    class DataClass(DataClassDictMixin):
        point: PlainPoint = flat(flatten_prefix="p_")

    obj = DataClass(PlainPoint(1, 2))
    assert obj.to_dict() == {"p_x": 1, "p_y": 2}
    assert DataClass.from_dict({"p_x": 1, "p_y": 2}) == obj


def test_flatten_generic_child():
    @dataclass
    class DataClass(DataClassDictMixin):
        box: GenericBox[int] = flat(flatten_prefix="box_")

    obj = DataClass(GenericBox(1))
    assert obj.to_dict() == {"box_value": 1}
    assert DataClass.from_dict({"box_value": "1"}) == obj


def test_flatten_with_codecs():
    @dataclass
    class DataClass:
        name: str
        point: PlainPoint = flat(flatten_prefix=True)

    obj = DataClass("a", PlainPoint(1, 2))
    d = {"name": "a", "point_x": 1, "point_y": 2}
    assert BasicEncoder(DataClass).encode(obj) == d
    assert BasicDecoder(DataClass).decode(d) == obj


def test_flatten_passes_dialect_to_child():
    class OmitNoneDialect(Dialect):
        omit_none = True

    @dataclass
    class Child(DataClassDictMixin):
        a: Optional[int] = None

        class Config(BaseConfig):
            code_generation_options = [ADD_DIALECT_SUPPORT]

    @dataclass
    class DataClass(DataClassDictMixin):
        child: Child = flat(flatten_prefix=True)

        class Config(BaseConfig):
            code_generation_options = [ADD_DIALECT_SUPPORT]

    assert DataClass(Child()).to_dict() == {"child_a": None}
    assert DataClass(Child()).to_dict(dialect=OmitNoneDialect) == {}


def test_flatten_with_orjson_and_msgpack():
    @dataclass
    class Child(DataClassORJSONMixin, DataClassMessagePackMixin):
        d: date

    @dataclass
    class DataClass(DataClassORJSONMixin, DataClassMessagePackMixin):
        child: Child = flat(flatten_prefix=True)

    obj = DataClass(Child(date(2024, 1, 2)))
    assert obj.to_json() == '{"child_d":"2024-01-02"}'
    assert DataClass.from_json(obj.to_json()) == obj
    assert DataClass.from_msgpack(obj.to_msgpack()) == obj


def test_nested_flatten():
    @dataclass
    class Middle(DataClassDictMixin):
        z: int
        point: Point = flat(flatten_prefix="p_")

    @dataclass
    class DataClass(DataClassDictMixin):
        middle: Middle = flat(flatten_prefix="m_")

    obj = DataClass(Middle(0, Point(1, 2)))
    d = {"m_z": 0, "m_p_x": 1, "m_p_y": 2}
    assert obj.to_dict() == d
    assert DataClass.from_dict(d) == obj


def test_nested_flatten_rename_of_flattened_key():
    @dataclass
    class Middle(DataClassDictMixin):
        point: Point = flat(flatten_prefix="p_")

    @dataclass
    class DataClass(DataClassDictMixin):
        middle: Middle = flat(flatten_rename={"p_x": "px"})

    obj = DataClass(Middle(Point(1, 2)))
    assert obj.to_dict() == {"px": 1, "p_y": 2}
    assert DataClass.from_dict({"px": 1, "p_y": 2}) == obj


def test_flattened_child_keeps_own_config():
    @dataclass
    class Child(DataClassDictMixin):
        a: Optional[int] = field(
            default=None, metadata=field_options(alias="A")
        )
        b: Optional[int] = None

        class Config(BaseConfig):
            serialize_by_alias = True
            omit_none = True
            allow_deserialization_not_by_alias = True

    @dataclass
    class DataClass(DataClassDictMixin):
        child: Child = flat(flatten_prefix="child_")
        c: Optional[int] = None

    obj = DataClass(Child(1, None))
    assert obj.to_dict() == {"child_A": 1, "c": None}
    assert DataClass.from_dict({"child_A": 1}) == obj
    assert DataClass.from_dict({"child_a": 1}) == obj


def test_flatten_forbid_extra_keys():
    @dataclass
    class DataClass(DataClassDictMixin):
        name: str
        point: Point = flat(flatten_prefix="p_")

        class Config(BaseConfig):
            forbid_extra_keys = True

    d = {"name": "a", "p_x": 1, "p_y": 2}
    assert DataClass.from_dict(d) == DataClass("a", Point(1, 2))
    with pytest.raises(ExtraKeysError) as exc_info:
        DataClass.from_dict({**d, "x": 1})
    assert exc_info.value.extra_keys == {"x"}
    with pytest.raises(ExtraKeysError) as exc_info:
        DataClass.from_dict({**d, "point": {"x": 1, "y": 2}})
    assert exc_info.value.extra_keys == {"point"}


def test_flatten_forbid_extra_keys_with_renamed_and_aliased_keys():
    @dataclass
    class DataClass(DataClassDictMixin):
        point: AliasedPoint = flat(flatten_rename={"y": "py"})

        class Config(BaseConfig):
            forbid_extra_keys = True

    assert DataClass.from_dict({"X": 1, "py": 2}) == DataClass(
        AliasedPoint(1, 2)
    )
    with pytest.raises(ExtraKeysError) as exc_info:
        DataClass.from_dict({"X": 1, "y": 2})
    assert exc_info.value.extra_keys == {"y"}
    with pytest.raises(ExtraKeysError) as exc_info:
        DataClass.from_dict({"x": 1})
    assert exc_info.value.extra_keys == {"x"}


def test_flatten_forbid_extra_keys_with_nested_flatten():
    @dataclass
    class Middle(DataClassDictMixin):
        point: Point = flat(flatten_prefix="p_")

    @dataclass
    class DataClass(DataClassDictMixin):
        middle: Middle = flat(flatten_prefix="m_")

        class Config(BaseConfig):
            forbid_extra_keys = True

    assert DataClass.from_dict({"m_p_x": 1, "m_p_y": 2}) == DataClass(
        Middle(Point(1, 2))
    )
    with pytest.raises(ExtraKeysError):
        DataClass.from_dict({"m_p_x": 1, "m_p_y": 2, "m_point": {}})


def test_optional_flattened_field():
    @dataclass
    class DataClass(DataClassDictMixin):
        name: str
        point: Optional[Point] = flat(flatten_prefix=True)

    assert DataClass("a", None).to_dict() == {"name": "a"}
    assert DataClass.from_dict({"name": "a"}) == DataClass("a", None)
    obj = DataClass("a", Point(1, 2))
    assert obj.to_dict() == {"name": "a", "point_x": 1, "point_y": 2}
    assert DataClass.from_dict(obj.to_dict()) == obj


def test_optional_flattened_field_with_default():
    @dataclass
    class DataClass(DataClassDictMixin):
        name: str
        point: Optional[Point] = field(
            default=None, metadata=field_options(flatten=True)
        )
        other: Point = field(
            default_factory=lambda: Point(0, 0),
            metadata=field_options(flatten=True, flatten_prefix="o_"),
        )

    assert DataClass("a").to_dict() == {"name": "a", "o_x": 0, "o_y": 0}
    assert DataClass.from_dict({"name": "a"}) == DataClass("a")
    assert DataClass.from_dict(
        {"name": "a", "x": 1, "y": 2, "o_x": 3, "o_y": 4}
    ) == DataClass("a", Point(1, 2), Point(3, 4))


def test_optional_flattened_field_with_union_syntax():
    @dataclass
    class DataClass(DataClassDictMixin):
        point: Union[Point, None] = flat()

    assert DataClass(None).to_dict() == {}
    assert DataClass.from_dict({}) == DataClass(None)
    assert DataClass.from_dict({"x": 1, "y": 2}) == DataClass(Point(1, 2))


def test_annotated_flattened_field():
    @dataclass
    class DataClass(DataClassDictMixin):
        point: Annotated[Optional[Point], "meta"] = flat()

    assert DataClass(Point(1, 2)).to_dict() == {"x": 1, "y": 2}
    assert DataClass.from_dict({}) == DataClass(None)


def test_required_flattened_field_with_missing_keys():
    @dataclass
    class DataClass(DataClassDictMixin):
        point: Point = flat()

    with pytest.raises(InvalidFieldValue) as exc_info:
        DataClass.from_dict({"x": 1})
    assert exc_info.value.field_name == "point"


def test_flattened_field_with_omit_default():
    @dataclass
    class DataClass(DataClassDictMixin):
        point: Point = field(
            default_factory=lambda: Point(0, 0),
            metadata=field_options(flatten=True),
        )

        class Config(BaseConfig):
            omit_default = True

    assert DataClass().to_dict() == {}
    assert DataClass(Point(1, 0)).to_dict() == {"x": 1, "y": 0}


def test_flattened_field_with_serialize_omit():
    @dataclass
    class DataClass(DataClassDictMixin):
        name: str
        point: Point = field(
            default_factory=lambda: Point(0, 0),
            metadata=field_options(flatten=True, serialize="omit"),
        )

    assert DataClass("a", Point(1, 2)).to_dict() == {"name": "a"}


def test_flatten_prefix_and_rename_are_mutually_exclusive():
    with pytest.raises(UnserializableField, match="mutually exclusive"):

        @dataclass
        class DataClass(DataClassDictMixin):
            point: Point = flat(
                flatten_prefix="p_", flatten_rename={"x": "px"}
            )


@pytest.mark.parametrize(
    "options",
    [
        {"flatten_prefix": "p_"},
        {"flatten_prefix": True},
        {"flatten_rename": {}},
    ],
)
def test_flatten_options_require_flatten(options):
    with pytest.raises(UnserializableField, match='require "flatten"'):

        @dataclass
        class DataClass(DataClassDictMixin):
            point: Point = field(metadata=field_options(**options))


def test_flatten_prefix_invalid_type():
    with pytest.raises(UnserializableField, match="must be a string or True"):

        @dataclass
        class DataClass(DataClassDictMixin):
            point: Point = flat(flatten_prefix=1)


def test_flatten_rename_invalid_type():
    with pytest.raises(UnserializableField, match="must be a mapping"):

        @dataclass
        class DataClass(DataClassDictMixin):
            point: Point = flat(flatten_rename=[("x", "px")])


def test_flatten_rename_non_string_value():
    with pytest.raises(UnserializableField, match="must be strings"):

        @dataclass
        class DataClass(DataClassDictMixin):
            point: Point = flat(flatten_rename={"x": 1})


@pytest.mark.parametrize(
    "ftype", [int, List[Point], Union[Point, AliasedPoint], Optional[int]]
)
def test_flatten_non_dataclass_type(ftype):
    with pytest.raises(UnserializableField, match="only dataclass fields"):

        @dataclass
        class DataClass(DataClassDictMixin):
            value: ftype = flat()


def test_flatten_type_var_field():
    with pytest.raises(UnserializableField, match="only dataclass fields"):

        @dataclass
        class DataClass(Generic[T], DataClassDictMixin):
            value: T = flat()


def test_flatten_recursive():
    with pytest.raises(UnserializableField, match="recursive flattening"):

        @dataclass
        class DataClass(DataClassDictMixin):
            node: Node = flat()


def test_flatten_rename_unknown_key():
    with pytest.raises(
        UnserializableField, match='key "z" does not match any field of Point'
    ):

        @dataclass
        class DataClass(DataClassDictMixin):
            point: Point = flat(flatten_rename={"z": "pz"})


def test_flatten_rename_duplicate_keys_for_same_field():
    with pytest.raises(
        UnserializableField,
        match='keys "x" and "X" refer to the same field "x"',
    ):

        @dataclass
        class DataClass(DataClassDictMixin):
            point: AliasedPoint = flat(flatten_rename={"x": "a", "X": "b"})


def test_flatten_rename_duplicate_target_keys():
    with pytest.raises(
        UnserializableField,
        match="more than one field of the flattened dataclass",
    ):

        @dataclass
        class DataClass(DataClassDictMixin):
            point: Point = flat(flatten_rename={"x": "v", "y": "v"})


def test_flatten_rename_target_collides_with_child_key():
    with pytest.raises(
        UnserializableField,
        match="more than one field of the flattened dataclass",
    ):

        @dataclass
        class DataClass(DataClassDictMixin):
            point: Point = flat(flatten_rename={"x": "y"})


def test_flatten_collision_with_parent_field():
    with pytest.raises(
        UnserializableField,
        match='Field "point".*flattened key "x" collides with a key of '
        'field "x"',
    ):

        @dataclass
        class DataClass(DataClassDictMixin):
            x: int
            point: Point = flat()


def test_flatten_collision_with_parent_field_declared_after():
    with pytest.raises(UnserializableField, match='field "x"'):

        @dataclass
        class DataClass(DataClassDictMixin):
            point: Point = flat()
            x: int = 0


def test_flatten_collision_with_parent_field_options_alias():
    with pytest.raises(UnserializableField, match='key "x".*field "a"'):

        @dataclass
        class DataClass(DataClassDictMixin):
            a: int = field(metadata=field_options(alias="x"))
            point: Point = flat()


def test_flatten_collision_with_parent_annotated_alias():
    with pytest.raises(UnserializableField, match='key "y".*field "a"'):

        @dataclass
        class DataClass(DataClassDictMixin):
            a: Annotated[int, Alias("y")]
            point: Point = flat()


def test_flatten_collision_with_parent_config_alias():
    with pytest.raises(UnserializableField, match='key "x".*field "a"'):

        @dataclass
        class DataClass(DataClassDictMixin):
            a: int
            point: Point = flat()

            class Config(BaseConfig):
                aliases = {"a": "x"}


def test_flatten_collision_with_child_alias():
    with pytest.raises(UnserializableField, match='key "X".*field "X"'):

        @dataclass
        class DataClass(DataClassDictMixin):
            X: int
            point: AliasedPoint = flat()


def test_flatten_collision_with_child_annotated_alias():
    @dataclass
    class Child(DataClassDictMixin):
        a: Annotated[int, Alias("b")]

    with pytest.raises(UnserializableField, match='key "b".*field "b"'):

        @dataclass
        class DataClass(DataClassDictMixin):
            b: int
            child: Child = flat()


def test_flatten_collision_with_child_config_alias():
    @dataclass
    class Child(DataClassDictMixin):
        a: int

        class Config(BaseConfig):
            aliases = {"a": "b"}

    with pytest.raises(UnserializableField, match='key "b".*field "b"'):

        @dataclass
        class DataClass(DataClassDictMixin):
            b: int
            child: Child = flat()


def test_flatten_collision_between_child_field_name_and_parent_key():
    with pytest.raises(UnserializableField, match='key "x".*field "x"'):

        @dataclass
        class DataClass(DataClassDictMixin):
            x: int
            point: AliasedPoint = flat()


def test_flatten_collision_between_flattened_fields():
    with pytest.raises(
        UnserializableField,
        match='Field "end".*flattened key "x" collides with a key of '
        'flattened field "start"',
    ):

        @dataclass
        class DataClass(DataClassDictMixin):
            start: Point = flat()
            end: Point = flat()


def test_flatten_collision_between_prefix_and_parent_field():
    with pytest.raises(UnserializableField, match='key "p_x".*field "p_x"'):

        @dataclass
        class DataClass(DataClassDictMixin):
            p_x: int
            point: Point = flat(flatten_prefix="p_")


def test_flatten_collision_with_rename_target():
    with pytest.raises(UnserializableField, match='key "name".*field "name"'):

        @dataclass
        class DataClass(DataClassDictMixin):
            name: str
            point: Point = flat(flatten_rename={"x": "name"})


def test_flatten_collision_in_nested_flatten():
    @dataclass
    class Middle(DataClassDictMixin):
        point: Point = flat()

    with pytest.raises(UnserializableField, match='key "y".*field "y"'):

        @dataclass
        class DataClass(DataClassDictMixin):
            y: int
            middle: Middle = flat()


def test_flatten_collision_inside_plain_child():
    @dataclass
    class Child:
        x: int
        point: PlainPoint = flat()

    with pytest.raises(
        UnserializableField,
        match=r'Field "point" of type .*PlainPoint in .*Child',
    ):

        @dataclass
        class DataClass(DataClassDictMixin):
            child: Child = flat(flatten_prefix="c_")


def test_prefix_resolves_collision():
    @dataclass
    class DataClass(DataClassDictMixin):
        x: int
        point: Point = flat(flatten_prefix=True)

    obj = DataClass(0, Point(1, 2))
    assert obj.to_dict() == {"x": 0, "point_x": 1, "point_y": 2}
    assert DataClass.from_dict(obj.to_dict()) == obj
