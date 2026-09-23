from dataclasses import dataclass, field
from typing import Optional

import pytest
from typing_extensions import Annotated

from mashumaro import DataClassDictMixin, field_options
from mashumaro.config import BaseConfig
from mashumaro.exceptions import BadFlatten, ExtraKeysError, MissingField
from mashumaro.types import Alias, SerializationStrategy


@dataclass
class Address(DataClassDictMixin):
    street: str
    city: str


@dataclass
class AddressWithAlias(DataClassDictMixin):
    street: str = field(metadata=field_options(alias="street_name"))
    city: str

    class Config(BaseConfig):
        serialize_by_alias = True


def test_flatten_merges_nested_fields():
    @dataclass
    class Person(DataClassDictMixin):
        name: str
        address: Address = field(metadata=field_options(flatten=True))

    person = Person("Ann", Address("Main", "Rome"))
    data = {"name": "Ann", "street": "Main", "city": "Rome"}
    assert person.to_dict() == data
    assert Person.from_dict(data) == person


def test_flatten_prefix_string():
    @dataclass
    class Person(DataClassDictMixin):
        name: str
        address: Address = field(
            metadata=field_options(flatten=True, flatten_prefix="addr_")
        )

    person = Person("Ann", Address("Main", "Rome"))
    data = {"name": "Ann", "addr_street": "Main", "addr_city": "Rome"}
    assert person.to_dict() == data
    assert Person.from_dict(data) == person


def test_flatten_prefix_true_uses_field_name():
    @dataclass
    class Person(DataClassDictMixin):
        name: str
        address: Address = field(
            metadata=field_options(flatten=True, flatten_prefix=True)
        )

    person = Person("Ann", Address("Main", "Rome"))
    data = {
        "name": "Ann",
        "address_street": "Main",
        "address_city": "Rome",
    }
    assert person.to_dict() == data
    assert Person.from_dict(data) == person


def test_flatten_rename():
    @dataclass
    class Person(DataClassDictMixin):
        name: str
        address: Address = field(
            metadata=field_options(
                flatten=True,
                flatten_rename={"street": "road", "city": "town"},
            )
        )

    person = Person("Ann", Address("Main", "Rome"))
    data = {"name": "Ann", "road": "Main", "town": "Rome"}
    assert person.to_dict() == data
    assert Person.from_dict(data) == person


def test_flatten_partial_rename_keeps_other_keys():
    @dataclass
    class Person(DataClassDictMixin):
        address: Address = field(
            metadata=field_options(
                flatten=True, flatten_rename={"street": "road"}
            )
        )

    person = Person(Address("Main", "Rome"))
    assert person.to_dict() == {"road": "Main", "city": "Rome"}
    assert Person.from_dict({"road": "Main", "city": "Rome"}) == person


def test_optional_flattened_field_absent_and_present():
    @dataclass
    class Person(DataClassDictMixin):
        name: str
        address: Optional[Address] = field(
            default=None, metadata=field_options(flatten=True)
        )

    assert Person("Ann").to_dict() == {"name": "Ann"}
    assert Person.from_dict({"name": "Ann"}) == Person("Ann")
    person = Person("Ann", Address("Main", "Rome"))
    data = {"name": "Ann", "street": "Main", "city": "Rome"}
    assert person.to_dict() == data
    assert Person.from_dict(data) == person


def test_optional_flattened_field_without_default():
    @dataclass
    class Person(DataClassDictMixin):
        address: Optional[Address] = field(
            metadata=field_options(flatten=True)
        )
        name: str

    assert Person(None, "Ann").to_dict() == {"name": "Ann"}
    assert Person.from_dict({"name": "Ann"}) == Person(None, "Ann")


def test_optional_flattened_field_partial_required_child():
    @dataclass
    class Person(DataClassDictMixin):
        address: Optional[Address] = field(
            default=None, metadata=field_options(flatten=True)
        )

    with pytest.raises(MissingField):
        Person.from_dict({"street": "Main"})


def test_flattened_child_keeps_alias_and_omit_config():
    @dataclass
    class Inner(DataClassDictMixin):
        a: Optional[int] = field(
            default=None, metadata=field_options(alias="a_alias")
        )
        b: int = 1

        class Config(BaseConfig):
            serialize_by_alias = True
            omit_none = True

    @dataclass
    class Outer(DataClassDictMixin):
        inner: Inner = field(metadata=field_options(flatten=True))
        c: int = 2

    assert Outer(Inner(None, 1), 2).to_dict() == {"b": 1, "c": 2}
    assert Outer(Inner(5, 1), 2).to_dict() == {"a_alias": 5, "b": 1, "c": 2}
    assert Outer.from_dict({"a_alias": 5, "b": 1, "c": 2}) == Outer(
        Inner(5, 1), 2
    )
    assert Outer.from_dict({"b": 1, "c": 2}) == Outer(Inner(None, 1), 2)


def test_flattened_child_omit_default():
    @dataclass
    class Inner(DataClassDictMixin):
        a: int = 1
        b: int = 2

        class Config(BaseConfig):
            omit_default = True

    @dataclass
    class Outer(DataClassDictMixin):
        inner: Inner = field(metadata=field_options(flatten=True))

    assert Outer(Inner(1, 3)).to_dict() == {"b": 3}
    assert Outer.from_dict({"b": 3}) == Outer(Inner(1, 3))


def test_flattened_child_serialization_strategy():
    class Upper(SerializationStrategy):
        def serialize(self, value: str) -> str:
            return value.upper()

        def deserialize(self, value: str) -> str:
            return value.lower()

    @dataclass
    class Inner(DataClassDictMixin):
        name: str = field(
            metadata=field_options(serialization_strategy=Upper())
        )

    @dataclass
    class Outer(DataClassDictMixin):
        inner: Inner = field(metadata=field_options(flatten=True))

    assert Outer(Inner("ab")).to_dict() == {"name": "AB"}
    assert Outer.from_dict({"name": "AB"}) == Outer(Inner("ab"))


def test_nested_flatten_keeps_child_prefix():
    @dataclass
    class Geo(DataClassDictMixin):
        lat: float
        lon: float

    @dataclass
    class Place(DataClassDictMixin):
        street: str
        geo: Geo = field(
            metadata=field_options(flatten=True, flatten_prefix="geo_")
        )

    @dataclass
    class Person(DataClassDictMixin):
        name: str
        place: Place = field(
            metadata=field_options(flatten=True, flatten_prefix=True)
        )

    person = Person("Ann", Place("Main", Geo(1.5, 2.5)))
    data = {
        "name": "Ann",
        "place_street": "Main",
        "place_geo_lat": 1.5,
        "place_geo_lon": 2.5,
    }
    assert person.to_dict() == data
    assert Person.from_dict(data) == person


def test_forbid_extra_keys_allows_flattened_keys():
    @dataclass
    class Person(DataClassDictMixin):
        name: str
        address: Address = field(metadata=field_options(flatten=True))

        class Config(BaseConfig):
            forbid_extra_keys = True

    data = {"name": "Ann", "street": "Main", "city": "Rome"}
    assert Person.from_dict(data) == Person("Ann", Address("Main", "Rome"))
    with pytest.raises(ExtraKeysError) as exc_info:
        Person.from_dict({**data, "extra": 1})
    assert exc_info.value.extra_keys == {"extra"}


def test_forbid_extra_keys_with_prefix_and_alias():
    @dataclass
    class Person(DataClassDictMixin):
        address: AddressWithAlias = field(
            metadata=field_options(flatten=True, flatten_prefix="addr_")
        )

        class Config(BaseConfig):
            forbid_extra_keys = True

    assert Person.from_dict(
        {"addr_street_name": "Main", "addr_city": "Rome"}
    ) == Person(AddressWithAlias("Main", "Rome"))
    with pytest.raises(ExtraKeysError) as exc_info:
        Person.from_dict(
            {
                "addr_street_name": "Main",
                "addr_city": "Rome",
                "addr_street": "Main",
            }
        )
    assert exc_info.value.extra_keys == {"addr_street"}


def test_child_allow_deserialization_not_by_alias():
    @dataclass
    class Inner(DataClassDictMixin):
        a: int = field(metadata=field_options(alias="a_alias"))

        class Config(BaseConfig):
            serialize_by_alias = True
            allow_deserialization_not_by_alias = True

    @dataclass
    class Outer(DataClassDictMixin):
        inner: Inner = field(
            metadata=field_options(flatten=True, flatten_prefix="i_")
        )

    assert Outer.from_dict({"i_a_alias": 1}) == Outer(Inner(1))
    assert Outer.from_dict({"i_a": 1}) == Outer(Inner(1))
    assert Outer(Inner(1)).to_dict() == {"i_a_alias": 1}


def test_prefix_and_rename_are_mutually_exclusive():
    with pytest.raises(BadFlatten, match="mutually exclusive"):

        @dataclass
        class Person(DataClassDictMixin):
            address: Address = field(
                metadata=field_options(
                    flatten=True,
                    flatten_prefix="addr_",
                    flatten_rename={"street": "road"},
                )
            )


def test_flatten_non_dataclass_is_rejected():
    with pytest.raises(BadFlatten, match="not a dataclass"):

        @dataclass
        class Person(DataClassDictMixin):
            name: int = field(metadata=field_options(flatten=True))


def test_flatten_optional_non_dataclass_is_rejected():
    with pytest.raises(BadFlatten, match="not a dataclass"):

        @dataclass
        class Person(DataClassDictMixin):
            name: Optional[int] = field(metadata=field_options(flatten=True))


def test_flatten_prefix_without_flatten_is_rejected():
    with pytest.raises(BadFlatten, match="flatten is not enabled"):

        @dataclass
        class Person(DataClassDictMixin):
            address: Address = field(
                metadata=field_options(flatten_prefix="addr_")
            )


def test_collision_with_parent_field():
    with pytest.raises(BadFlatten, match="collides"):

        @dataclass
        class Person(DataClassDictMixin):
            city: str
            address: Address = field(metadata=field_options(flatten=True))


def test_collision_between_flattened_fields():
    with pytest.raises(BadFlatten, match="collides"):

        @dataclass
        class Person(DataClassDictMixin):
            home: Address = field(metadata=field_options(flatten=True))
            work: Address = field(metadata=field_options(flatten=True))


def test_prefix_avoids_collision():
    @dataclass
    class Person(DataClassDictMixin):
        home: Address = field(
            metadata=field_options(flatten=True, flatten_prefix=True)
        )
        work: Address = field(
            metadata=field_options(flatten=True, flatten_prefix=True)
        )

    person = Person(Address("Main", "Rome"), Address("Oak", "Milan"))
    data = {
        "home_street": "Main",
        "home_city": "Rome",
        "work_street": "Oak",
        "work_city": "Milan",
    }
    assert person.to_dict() == data
    assert Person.from_dict(data) == person


def test_collision_with_metadata_alias():
    with pytest.raises(BadFlatten, match="collides"):

        @dataclass
        class Person(DataClassDictMixin):
            city_name: str = field(metadata=field_options(alias="city"))
            address: Address = field(metadata=field_options(flatten=True))


def test_collision_with_annotated_alias():
    with pytest.raises(BadFlatten, match="collides"):

        @dataclass
        class Person(DataClassDictMixin):
            city_name: Annotated[str, Alias("city")]
            address: Address = field(metadata=field_options(flatten=True))


def test_collision_with_config_aliases():
    with pytest.raises(BadFlatten, match="collides"):

        @dataclass
        class Person(DataClassDictMixin):
            city_name: str
            address: Address = field(metadata=field_options(flatten=True))

            class Config(BaseConfig):
                aliases = {"city_name": "city"}


def test_collision_with_child_metadata_alias():
    with pytest.raises(BadFlatten, match="collides"):

        @dataclass
        class Person(DataClassDictMixin):
            street_name: str
            address: AddressWithAlias = field(
                metadata=field_options(flatten=True)
            )


def test_collision_with_child_annotated_alias():
    @dataclass
    class Inner(DataClassDictMixin):
        street: Annotated[str, Alias("road")]

    with pytest.raises(BadFlatten, match="collides"):

        @dataclass
        class Person(DataClassDictMixin):
            road: str
            inner: Inner = field(metadata=field_options(flatten=True))


def test_collision_with_child_config_alias():
    @dataclass
    class Inner(DataClassDictMixin):
        street: str

        class Config(BaseConfig):
            aliases = {"street": "road"}

    with pytest.raises(BadFlatten, match="collides"):

        @dataclass
        class Person(DataClassDictMixin):
            road: str
            inner: Inner = field(metadata=field_options(flatten=True))


def test_no_collision_when_serialize_by_alias_hides_field_name():
    @dataclass
    class Inner(DataClassDictMixin):
        a: int = field(metadata=field_options(alias="a_alias"))

        class Config(BaseConfig):
            serialize_by_alias = True

    @dataclass
    class Outer(DataClassDictMixin):
        a: int
        inner: Inner = field(metadata=field_options(flatten=True))

    obj = Outer(1, Inner(2))
    assert obj.to_dict() == {"a": 1, "a_alias": 2}
    assert Outer.from_dict({"a": 1, "a_alias": 2}) == obj


def test_invalid_rename_key():
    with pytest.raises(BadFlatten, match="Invalid flatten_rename key"):

        @dataclass
        class Person(DataClassDictMixin):
            address: Address = field(
                metadata=field_options(
                    flatten=True, flatten_rename={"nope": "road"}
                )
            )


def test_duplicate_rename_target():
    with pytest.raises(BadFlatten, match="Duplicate flatten_rename target"):

        @dataclass
        class Person(DataClassDictMixin):
            address: Address = field(
                metadata=field_options(
                    flatten=True,
                    flatten_rename={"street": "place", "city": "place"},
                )
            )


def test_rename_overrides_child_alias_for_both_directions():
    @dataclass
    class Person(DataClassDictMixin):
        address: AddressWithAlias = field(
            metadata=field_options(
                flatten=True,
                flatten_rename={"street": "road", "city": "town"},
            )
        )

    person = Person(AddressWithAlias("Main", "Rome"))
    data = {"road": "Main", "town": "Rome"}
    assert person.to_dict() == data
    assert Person.from_dict(data) == person


def test_rename_collision_with_sibling_field():
    with pytest.raises(BadFlatten, match="collides"):

        @dataclass
        class Person(DataClassDictMixin):
            city: str
            address: Address = field(
                metadata=field_options(
                    flatten=True, flatten_rename={"street": "city"}
                )
            )
