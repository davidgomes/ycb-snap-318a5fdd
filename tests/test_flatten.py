from dataclasses import dataclass, field
from typing import Generic, Optional, TypeVar

import pytest
from typing_extensions import Annotated

from mashumaro import DataClassDictMixin, field_options
from mashumaro.codecs import BasicDecoder, BasicEncoder
from mashumaro.config import TO_DICT_ADD_OMIT_NONE_FLAG, BaseConfig
from mashumaro.exceptions import (
    ExtraKeysError,
    InvalidFieldValue,
    InvalidFlattenedField,
)
from mashumaro.mixins.json import DataClassJSONMixin
from mashumaro.types import Alias

T = TypeVar("T")


@dataclass
class Address(DataClassDictMixin):
    city: str
    street: str


@dataclass
class AliasedAddress(DataClassDictMixin):
    city: str
    zip_code: str = field(metadata=field_options(alias="zip"))

    class Config(BaseConfig):
        serialize_by_alias = True


def test_field_options_flatten_keys():
    assert field_options(
        flatten=True, flatten_prefix="p_", flatten_rename={"a": "b"}
    ) == {
        "serialize": None,
        "deserialize": None,
        "serialization_strategy": None,
        "alias": None,
        "flatten": True,
        "flatten_prefix": "p_",
        "flatten_rename": {"a": "b"},
    }


def test_flatten():
    @dataclass
    class Person(DataClassDictMixin):
        name: str
        address: Address = field(metadata=field_options(flatten=True))

    obj = Person("John", Address("Paris", "Main"))
    data = {"name": "John", "city": "Paris", "street": "Main"}
    assert obj.to_dict() == data
    assert Person.from_dict(data) == obj


def test_flatten_prefix_string():
    @dataclass
    class Person(DataClassDictMixin):
        address: Address = field(
            metadata=field_options(flatten=True, flatten_prefix="addr_")
        )

    obj = Person(Address("Paris", "Main"))
    data = {"addr_city": "Paris", "addr_street": "Main"}
    assert obj.to_dict() == data
    assert Person.from_dict(data) == obj


def test_flatten_prefix_true():
    @dataclass
    class Person(DataClassDictMixin):
        home: Address = field(
            metadata=field_options(flatten=True, flatten_prefix=True)
        )
        work: Address = field(
            metadata=field_options(flatten=True, flatten_prefix=True)
        )

    obj = Person(Address("Paris", "Main"), Address("Rome", "Via"))
    data = {
        "home_city": "Paris",
        "home_street": "Main",
        "work_city": "Rome",
        "work_street": "Via",
    }
    assert obj.to_dict() == data
    assert Person.from_dict(data) == obj


def test_flatten_rename():
    @dataclass
    class Person(DataClassDictMixin):
        name: str
        address: Address = field(
            metadata=field_options(
                flatten=True, flatten_rename={"city": "town"}
            )
        )

    obj = Person("John", Address("Paris", "Main"))
    data = {"name": "John", "town": "Paris", "street": "Main"}
    assert obj.to_dict() == data
    assert Person.from_dict(data) == obj


def test_flattened_child_keeps_its_config():
    @dataclass
    class Person(DataClassDictMixin):
        address: AliasedAddress = field(
            metadata=field_options(flatten=True, flatten_prefix="a_")
        )

    obj = Person(AliasedAddress("Paris", "75001"))
    data = {"a_city": "Paris", "a_zip": "75001"}
    assert obj.to_dict() == data
    assert Person.from_dict(data) == obj


def test_flattened_child_omit_none_config():
    @dataclass
    class Child(DataClassDictMixin):
        x: Optional[int] = None
        y: int = 1

        class Config(BaseConfig):
            omit_none = True

    @dataclass
    class Parent(DataClassDictMixin):
        child: Child = field(metadata=field_options(flatten=True))

    assert Parent(Child()).to_dict() == {"y": 1}
    assert Parent.from_dict({"y": 2}) == Parent(Child(y=2))


def test_flatten_passes_omit_none_flag():
    @dataclass
    class Child(DataClassDictMixin):
        x: Optional[int] = None

        class Config(BaseConfig):
            code_generation_options = [TO_DICT_ADD_OMIT_NONE_FLAG]

    @dataclass
    class Parent(DataClassDictMixin):
        child: Child = field(metadata=field_options(flatten=True))

        class Config(BaseConfig):
            code_generation_options = [TO_DICT_ADD_OMIT_NONE_FLAG]

    assert Parent(Child()).to_dict() == {"x": None}
    assert Parent(Child()).to_dict(omit_none=True) == {}


def test_nested_flatten():
    @dataclass
    class Inner(DataClassDictMixin):
        a: int

    @dataclass
    class Middle(DataClassDictMixin):
        b: int
        inner: Inner = field(
            metadata=field_options(flatten=True, flatten_prefix="in_")
        )

    @dataclass
    class Outer(DataClassDictMixin):
        middle: Middle = field(
            metadata=field_options(flatten=True, flatten_prefix="m_")
        )

        class Config(BaseConfig):
            forbid_extra_keys = True

    obj = Outer(Middle(1, Inner(2)))
    data = {"m_b": 1, "m_in_a": 2}
    assert obj.to_dict() == data
    assert Outer.from_dict(data) == obj
    with pytest.raises(ExtraKeysError):
        Outer.from_dict({**data, "in_a": 3})


def test_flatten_forbid_extra_keys():
    @dataclass
    class Person(DataClassDictMixin):
        name: str
        address: AliasedAddress = field(
            metadata=field_options(flatten=True, flatten_prefix="a_")
        )

        class Config(BaseConfig):
            forbid_extra_keys = True

    data = {"name": "John", "a_city": "Paris", "a_zip": "75001"}
    assert Person.from_dict(data) == Person(
        "John", AliasedAddress("Paris", "75001")
    )
    with pytest.raises(ExtraKeysError) as exc_info:
        Person.from_dict({**data, "address": {}})
    assert exc_info.value.extra_keys == {"address"}
    with pytest.raises(ExtraKeysError) as exc_info:
        Person.from_dict({**data, "city": "Rome"})
    assert exc_info.value.extra_keys == {"city"}


def test_flatten_child_with_forbid_extra_keys():
    @dataclass
    class Child(DataClassDictMixin):
        x: int

        class Config(BaseConfig):
            forbid_extra_keys = True

    @dataclass
    class Parent(DataClassDictMixin):
        y: int
        child: Child = field(metadata=field_options(flatten=True))

    assert Parent.from_dict({"x": 1, "y": 2}) == Parent(2, Child(1))


def test_flatten_allow_deserialization_not_by_alias_in_child():
    @dataclass
    class Child(DataClassDictMixin):
        x: int = field(metadata=field_options(alias="X"))

        class Config(BaseConfig):
            allow_deserialization_not_by_alias = True

    @dataclass
    class Parent(DataClassDictMixin):
        child: Child = field(
            metadata=field_options(flatten=True, flatten_prefix="c_")
        )

        class Config(BaseConfig):
            forbid_extra_keys = True

    assert Parent.from_dict({"c_X": 1}) == Parent(Child(1))
    assert Parent.from_dict({"c_x": 1}) == Parent(Child(1))


def test_optional_flattened_field():
    @dataclass
    class Person(DataClassDictMixin):
        name: str
        address: Optional[Address] = field(
            metadata=field_options(flatten=True, flatten_prefix=True)
        )

    obj = Person("John", None)
    assert obj.to_dict() == {"name": "John"}
    assert Person.from_dict({"name": "John"}) == obj

    obj = Person("John", Address("Paris", "Main"))
    data = {"name": "John", "address_city": "Paris", "address_street": "Main"}
    assert obj.to_dict() == data
    assert Person.from_dict(data) == obj


def test_optional_flattened_field_with_default():
    @dataclass
    class Person(DataClassDictMixin):
        name: str
        address: Optional[Address] = field(
            default=None, metadata=field_options(flatten=True)
        )

    assert Person("John").to_dict() == {"name": "John"}
    assert Person.from_dict({"name": "John"}) == Person("John")
    assert Person.from_dict(
        {"name": "John", "city": "Paris", "street": "Main"}
    ) == Person("John", Address("Paris", "Main"))


def test_flattened_field_with_default():
    @dataclass
    class Person(DataClassDictMixin):
        address: Address = field(
            default_factory=lambda: Address("Paris", "Main"),
            metadata=field_options(flatten=True),
        )

    assert Person.from_dict({}) == Person()
    assert Person.from_dict({"city": "Rome", "street": "Via"}) == Person(
        Address("Rome", "Via")
    )


def test_flattened_field_missing_child_keys():
    @dataclass
    class Person(DataClassDictMixin):
        address: Address = field(metadata=field_options(flatten=True))

    with pytest.raises(InvalidFieldValue):
        Person.from_dict({"city": "Paris"})


def test_flatten_omit_default():
    @dataclass
    class Person(DataClassDictMixin):
        name: str
        address: Address = field(
            default_factory=lambda: Address("Paris", "Main"),
            metadata=field_options(flatten=True),
        )

        class Config(BaseConfig):
            omit_default = True

    assert Person("John").to_dict() == {"name": "John"}
    assert Person("John", Address("Rome", "Via")).to_dict() == {
        "name": "John",
        "city": "Rome",
        "street": "Via",
    }


def test_flatten_generic_child():
    @dataclass
    class Box(Generic[T], DataClassDictMixin):
        value: T

    @dataclass
    class Parent(DataClassDictMixin):
        box: Box[int] = field(
            metadata=field_options(flatten=True, flatten_prefix=True)
        )

    assert Parent(Box(1)).to_dict() == {"box_value": 1}
    assert Parent.from_dict({"box_value": "2"}) == Parent(Box(2))


def test_flatten_json():
    @dataclass
    class Person(DataClassJSONMixin):
        name: str
        address: Address = field(metadata=field_options(flatten=True))

    obj = Person("John", Address("Paris", "Main"))
    assert Person.from_json(obj.to_json()) == obj


def test_flatten_with_codecs():
    @dataclass
    class Inner:
        a: int

    @dataclass
    class Outer:
        b: int
        inner: Inner = field(
            metadata=field_options(flatten=True, flatten_prefix=True)
        )

    obj = Outer(1, Inner(2))
    data = {"b": 1, "inner_a": 2}
    assert BasicEncoder(Outer).encode(obj) == data
    assert BasicDecoder(Outer).decode(data) == obj


def test_flatten_serialize_omit():
    @dataclass
    class Person(DataClassDictMixin):
        name: str
        address: Address = field(
            default_factory=lambda: Address("Paris", "Main"),
            metadata=field_options(flatten=True, serialize="omit"),
        )

    assert Person("John").to_dict() == {"name": "John"}


def test_flatten_collision_with_field():
    with pytest.raises(InvalidFlattenedField, match='"city" collides'):

        @dataclass
        class Person(DataClassDictMixin):
            city: str
            address: Address = field(metadata=field_options(flatten=True))


def test_flatten_collision_with_metadata_alias():
    with pytest.raises(InvalidFlattenedField, match='"city" collides'):

        @dataclass
        class Person(DataClassDictMixin):
            town: str = field(metadata=field_options(alias="city"))
            address: Address = field(metadata=field_options(flatten=True))


def test_flatten_collision_with_annotated_alias():
    with pytest.raises(InvalidFlattenedField, match='"city" collides'):

        @dataclass
        class Person(DataClassDictMixin):
            town: Annotated[str, Alias("city")]
            address: Address = field(metadata=field_options(flatten=True))


def test_flatten_collision_with_config_alias():
    with pytest.raises(InvalidFlattenedField, match='"city" collides'):

        @dataclass
        class Person(DataClassDictMixin):
            town: str
            address: Address = field(metadata=field_options(flatten=True))

            class Config(BaseConfig):
                aliases = {"town": "city"}


def test_flatten_collision_with_child_alias():
    with pytest.raises(InvalidFlattenedField, match='"zip" collides'):

        @dataclass
        class Person(DataClassDictMixin):
            zip: str
            address: AliasedAddress = field(
                metadata=field_options(flatten=True)
            )


def test_flatten_collision_between_flattened_fields():
    with pytest.raises(InvalidFlattenedField, match='"city" collides'):

        @dataclass
        class Person(DataClassDictMixin):
            home: Address = field(metadata=field_options(flatten=True))
            work: Address = field(metadata=field_options(flatten=True))


def test_flatten_collision_with_prefix():
    with pytest.raises(InvalidFlattenedField, match='"a_city" collides'):

        @dataclass
        class Person(DataClassDictMixin):
            a_city: str
            address: Address = field(
                metadata=field_options(flatten=True, flatten_prefix="a_")
            )


def test_flatten_rename_collision_within_child():
    with pytest.raises(InvalidFlattenedField, match='both map to "street"'):

        @dataclass
        class Person(DataClassDictMixin):
            address: Address = field(
                metadata=field_options(
                    flatten=True, flatten_rename={"city": "street"}
                )
            )


def test_flatten_rename_collision_with_parent_field():
    with pytest.raises(InvalidFlattenedField, match='"name" collides'):

        @dataclass
        class Person(DataClassDictMixin):
            name: str
            address: Address = field(
                metadata=field_options(
                    flatten=True, flatten_rename={"city": "name"}
                )
            )


def test_flatten_non_dataclass():
    with pytest.raises(InvalidFlattenedField, match="is not a dataclass"):

        @dataclass
        class Person(DataClassDictMixin):
            x: int = field(metadata=field_options(flatten=True))


def test_flatten_optional_non_dataclass():
    with pytest.raises(InvalidFlattenedField, match="is not a dataclass"):

        @dataclass
        class Person(DataClassDictMixin):
            x: Optional[dict] = field(metadata=field_options(flatten=True))


def test_flatten_prefix_and_rename_mutually_exclusive():
    with pytest.raises(InvalidFlattenedField, match="mutually exclusive"):

        @dataclass
        class Person(DataClassDictMixin):
            address: Address = field(
                metadata=field_options(
                    flatten=True,
                    flatten_prefix="a_",
                    flatten_rename={"city": "town"},
                )
            )


def test_flatten_rename_unknown_key():
    with pytest.raises(InvalidFlattenedField, match='unknown keys: "town"'):

        @dataclass
        class Person(DataClassDictMixin):
            address: Address = field(
                metadata=field_options(
                    flatten=True, flatten_rename={"town": "x"}
                )
            )


def test_flatten_rename_duplicate_target():
    with pytest.raises(
        InvalidFlattenedField, match='duplicate target key "x"'
    ):

        @dataclass
        class Person(DataClassDictMixin):
            address: Address = field(
                metadata=field_options(
                    flatten=True, flatten_rename={"city": "x", "street": "x"}
                )
            )


def test_flatten_rename_invalid_type():
    with pytest.raises(InvalidFlattenedField, match="mapping of strings"):

        @dataclass
        class Person(DataClassDictMixin):
            address: Address = field(
                metadata=field_options(
                    flatten=True, flatten_rename={"city": 1}
                )
            )


def test_flatten_prefix_invalid_type():
    with pytest.raises(InvalidFlattenedField, match="string or a boolean"):

        @dataclass
        class Person(DataClassDictMixin):
            address: Address = field(
                metadata=field_options(flatten=True, flatten_prefix=1)
            )


def test_flatten_options_without_flatten():
    with pytest.raises(InvalidFlattenedField, match="require flatten=True"):

        @dataclass
        class Person(DataClassDictMixin):
            address: Address = field(
                metadata=field_options(flatten_prefix="a_")
            )


def test_flattened_field_with_alias():
    with pytest.raises(InvalidFlattenedField, match="can't have an alias"):

        @dataclass
        class Person(DataClassDictMixin):
            address: Address = field(
                metadata=field_options(flatten=True, alias="addr")
            )


def test_flatten_with_serialization_strategy():
    with pytest.raises(InvalidFlattenedField, match="can't be combined"):

        @dataclass
        class Person(DataClassDictMixin):
            address: Address = field(
                metadata=field_options(flatten=True, serialize=str)
            )


@dataclass
class RecursiveNode(DataClassDictMixin):
    value: int
    next: Optional["RecursiveNode"] = field(
        default=None,
        metadata=field_options(flatten=True, flatten_prefix=True),
    )


def test_recursive_flatten():
    with pytest.raises(InvalidFlattenedField, match="recursive flattening"):
        RecursiveNode.from_dict({"value": 1})
