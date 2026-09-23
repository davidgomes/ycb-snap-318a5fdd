from dataclasses import dataclass, field
from typing import Generic, List, Optional, TypeVar, Union

import pytest
import typing_extensions
from typing_extensions import Annotated

from mashumaro import DataClassDictMixin, field_options
from mashumaro.codecs import BasicDecoder, BasicEncoder
from mashumaro.config import (
    TO_DICT_ADD_BY_ALIAS_FLAG,
    TO_DICT_ADD_OMIT_NONE_FLAG,
    BaseConfig,
)
from mashumaro.exceptions import (
    ExtraKeysError,
    InvalidFlattenedField,
    MissingField,
    UnserializableField,
)
from mashumaro.mixins.json import DataClassJSONMixin
from mashumaro.types import Alias

T = TypeVar("T")


@dataclass
class Address:
    city: str
    zip: str


@dataclass
class AliasedAddress(DataClassDictMixin):
    city: str
    zip: str = field(metadata=field_options(alias="postal"))

    class Config(BaseConfig):
        serialize_by_alias = True


@dataclass
class Geo:
    lat: float
    lon: float


@dataclass
class Location:
    name: str
    geo: Geo = field(
        metadata=field_options(flatten=True, flatten_prefix="geo_")
    )


@dataclass
class Person(DataClassDictMixin):
    name: str
    address: Address = field(metadata=field_options(flatten=True))


@dataclass
class Box(Generic[T]):
    item: T


def test_flatten():
    obj = Person("Bob", Address("Paris", "75001"))
    data = {"name": "Bob", "city": "Paris", "zip": "75001"}
    assert obj.to_dict() == data
    assert Person.from_dict(data) == obj


def test_flatten_prefix():
    @dataclass
    class DataClass(DataClassDictMixin):
        home: Address = field(
            metadata=field_options(flatten=True, flatten_prefix="h_")
        )
        work: Address = field(
            metadata=field_options(flatten=True, flatten_prefix=True)
        )

    obj = DataClass(Address("Paris", "1"), Address("Rome", "2"))
    data = {
        "h_city": "Paris",
        "h_zip": "1",
        "work_city": "Rome",
        "work_zip": "2",
    }
    assert obj.to_dict() == data
    assert DataClass.from_dict(data) == obj


def test_flatten_rename():
    @dataclass
    class DataClass(DataClassDictMixin):
        address: Address = field(
            metadata=field_options(
                flatten=True, flatten_rename={"city": "town"}
            )
        )

    obj = DataClass(Address("Paris", "1"))
    assert obj.to_dict() == {"town": "Paris", "zip": "1"}
    assert DataClass.from_dict({"town": "Paris", "zip": "1"}) == obj


def test_flatten_rename_swap():
    @dataclass
    class DataClass(DataClassDictMixin):
        address: Address = field(
            metadata=field_options(
                flatten=True, flatten_rename={"city": "zip", "zip": "city"}
            )
        )

    obj = DataClass(Address("Paris", "1"))
    assert obj.to_dict() == {"zip": "Paris", "city": "1"}
    assert DataClass.from_dict({"zip": "Paris", "city": "1"}) == obj


@pytest.mark.parametrize("rename_key", ["zip", "postal"])
def test_flatten_rename_by_field_name_or_alias(rename_key):
    @dataclass
    class DataClass(DataClassDictMixin):
        address: AliasedAddress = field(
            metadata=field_options(
                flatten=True, flatten_rename={rename_key: "code"}
            )
        )

    obj = DataClass(AliasedAddress("Paris", "1"))
    assert obj.to_dict() == {"city": "Paris", "code": "1"}
    assert DataClass.from_dict({"city": "Paris", "code": "1"}) == obj


def test_flattened_dataclass_keeps_own_config():
    @dataclass
    class Child(DataClassDictMixin):
        a: Optional[int] = None
        b: int = field(default=5, metadata=field_options(alias="B"))

        class Config(BaseConfig):
            omit_none = True
            omit_default = True
            serialize_by_alias = True

    @dataclass
    class Parent(DataClassDictMixin):
        child: Child = field(
            metadata=field_options(flatten=True, flatten_prefix="c_")
        )
        x: Optional[int] = None

    assert Parent(Child()).to_dict() == {"x": None}
    assert Parent(Child(2, 3), 1).to_dict() == {"c_a": 2, "c_B": 3, "x": 1}
    assert Parent.from_dict({"x": 1, "c_a": 2, "c_B": 3}) == Parent(
        Child(2, 3), 1
    )
    assert Parent.from_dict({}) == Parent(Child())


def test_flattened_dataclass_code_generation_flags():
    @dataclass
    class Child(DataClassDictMixin):
        a: Optional[int] = field(default=None, metadata={"alias": "A"})

        class Config(BaseConfig):
            code_generation_options = [
                TO_DICT_ADD_OMIT_NONE_FLAG,
                TO_DICT_ADD_BY_ALIAS_FLAG,
            ]

    @dataclass
    class Parent(DataClassDictMixin):
        b: Optional[int] = None
        child: Child = field(
            default_factory=Child, metadata=field_options(flatten=True)
        )

        class Config(BaseConfig):
            code_generation_options = [
                TO_DICT_ADD_OMIT_NONE_FLAG,
                TO_DICT_ADD_BY_ALIAS_FLAG,
            ]

    assert Parent().to_dict() == {"b": None, "a": None}
    assert Parent().to_dict(omit_none=True) == {}
    assert Parent(1, Child(2)).to_dict(by_alias=True) == {"b": 1, "A": 2}


def test_optional_flattened_field():
    @dataclass
    class DataClass(DataClassDictMixin):
        name: str
        address: Optional[Address] = field(
            metadata=field_options(flatten=True)
        )
        geo: Optional[Geo] = field(
            default=None, metadata=field_options(flatten=True)
        )

    obj = DataClass("x", None)
    assert obj.to_dict() == {"name": "x"}
    assert DataClass.from_dict({"name": "x"}) == obj

    obj = DataClass("x", Address("Paris", "1"), Geo(1.0, 2.0))
    data = {"name": "x", "city": "Paris", "zip": "1", "lat": 1.0, "lon": 2.0}
    assert obj.to_dict() == data
    assert DataClass.from_dict(data) == obj


def test_flattened_field_with_default():
    @dataclass
    class DataClass(DataClassDictMixin):
        address: Address = field(
            default_factory=lambda: Address("Paris", "1"),
            metadata=field_options(flatten=True),
        )

    assert DataClass.from_dict({}) == DataClass()
    assert DataClass.from_dict({"city": "Rome", "zip": "2"}) == DataClass(
        Address("Rome", "2")
    )


def test_flattened_field_with_omit_default():
    @dataclass
    class DataClass(DataClassDictMixin):
        address: Address = field(
            default_factory=lambda: Address("Paris", "1"),
            metadata=field_options(flatten=True),
        )

        class Config(BaseConfig):
            omit_default = True

    assert DataClass().to_dict() == {}
    assert DataClass(Address("Rome", "1")).to_dict() == {
        "city": "Rome",
        "zip": "1",
    }


def test_required_flattened_field_with_all_defaults():
    @dataclass
    class Child:
        a: int = 1
        b: int = 2

    @dataclass
    class DataClass(DataClassDictMixin):
        child: Child = field(metadata=field_options(flatten=True))

    assert DataClass.from_dict({}) == DataClass(Child())
    assert DataClass.from_dict({"b": 3}) == DataClass(Child(b=3))


def test_missing_key_of_flattened_field():
    with pytest.raises(MissingField) as exc_info:
        Person.from_dict({"name": "Bob", "city": "Paris"})
    assert exc_info.value.field_name == "zip"
    assert exc_info.value.holder_class is Address


def test_nested_flattening():
    @dataclass
    class Event(DataClassDictMixin):
        title: str
        location: Location = field(
            metadata=field_options(
                flatten=True, flatten_rename={"geo_lat": "latitude"}
            )
        )

    obj = Event("x", Location("home", Geo(1.0, 2.0)))
    data = {"title": "x", "name": "home", "latitude": 1.0, "geo_lon": 2.0}
    assert obj.to_dict() == data
    assert Event.from_dict(data) == obj


def test_forbid_extra_keys_with_flattened_field():
    @dataclass
    class DataClass(DataClassDictMixin):
        name: str
        address: AliasedAddress = field(
            metadata=field_options(flatten=True, flatten_prefix="a_")
        )

        class Config(BaseConfig):
            forbid_extra_keys = True

    data = {"name": "x", "a_city": "Paris", "a_postal": "1"}
    assert DataClass.from_dict(data) == DataClass(
        "x", AliasedAddress("Paris", "1")
    )
    with pytest.raises(ExtraKeysError) as exc_info:
        DataClass.from_dict({**data, "a_zip": "1", "city": "Paris"})
    assert exc_info.value.extra_keys == {"a_zip", "city"}
    assert exc_info.value.target_type is DataClass


def test_forbid_extra_keys_with_flattened_allow_not_by_alias():
    @dataclass
    class Child:
        a: int = field(metadata=field_options(alias="A"))

        class Config(BaseConfig):
            allow_deserialization_not_by_alias = True

    @dataclass
    class DataClass(DataClassDictMixin):
        child: Child = field(metadata=field_options(flatten=True))

        class Config(BaseConfig):
            forbid_extra_keys = True

    assert DataClass.from_dict({"A": 1}) == DataClass(Child(1))
    assert DataClass.from_dict({"a": 1}) == DataClass(Child(1))


def test_flattened_dataclass_with_forbid_extra_keys():
    @dataclass
    class Child(DataClassDictMixin):
        a: int

        class Config(BaseConfig):
            forbid_extra_keys = True

    @dataclass
    class DataClass(DataClassDictMixin):
        b: int
        child: Child = field(metadata=field_options(flatten=True))

    assert DataClass.from_dict({"a": 1, "b": 2, "c": 3}) == DataClass(
        2, Child(1)
    )


def test_flattened_generic_dataclass():
    @dataclass
    class DataClass(DataClassDictMixin):
        box: Box[int] = field(
            metadata=field_options(flatten=True, flatten_prefix=True)
        )

    assert DataClass(Box(1)).to_dict() == {"box_item": 1}
    assert DataClass.from_dict({"box_item": "2"}) == DataClass(Box(2))


def test_flattened_type_var_field():
    TGeo = TypeVar("TGeo", bound=Geo)

    @dataclass
    class Geo3D(Geo):
        alt: float = 0.0

    @dataclass
    class GenericHolder(Generic[TGeo], DataClassDictMixin):
        geo: TGeo = field(metadata=field_options(flatten=True))

    @dataclass
    class Concrete(GenericHolder[Geo3D]):
        pass

    assert GenericHolder(Geo(1.0, 2.0)).to_dict() == {"lat": 1.0, "lon": 2.0}
    obj = Concrete(Geo3D(1.0, 2.0, 3.0))
    assert obj.to_dict() == {"lat": 1.0, "lon": 2.0, "alt": 3.0}
    assert Concrete.from_dict(obj.to_dict()) == obj


def test_flattened_type_var_with_default():
    TGeo = typing_extensions.TypeVar("TGeo", default=Geo)

    @dataclass
    class GenericHolder(Generic[TGeo], DataClassDictMixin):
        geo: TGeo = field(metadata=field_options(flatten=True))

    obj = GenericHolder(Geo(1.0, 2.0))
    assert obj.to_dict() == {"lat": 1.0, "lon": 2.0}
    assert GenericHolder.from_dict({"lat": 1.0, "lon": 2.0}) == obj


def test_flattened_annotated_field():
    @dataclass
    class DataClass(DataClassDictMixin):
        a: Annotated[Optional[Address], "meta"] = field(
            default=None, metadata=field_options(flatten=True)
        )
        b: Optional[Annotated[Geo, "meta"]] = field(
            default=None, metadata=field_options(flatten=True)
        )

    assert DataClass().to_dict() == {}
    obj = DataClass(Address("Paris", "1"), Geo(1.0, 2.0))
    data = {"city": "Paris", "zip": "1", "lat": 1.0, "lon": 2.0}
    assert obj.to_dict() == data
    assert DataClass.from_dict(data) == obj


def test_flatten_with_codecs():
    @dataclass
    class DataClass:
        x: int
        geo: Optional[Geo] = field(
            default=None,
            metadata=field_options(flatten=True, flatten_prefix=True),
        )

    decoder = BasicDecoder(DataClass)
    encoder = BasicEncoder(DataClass)
    obj = DataClass(1, Geo(1.0, 2.0))
    assert encoder.encode(obj) == {"x": 1, "geo_lat": 1.0, "geo_lon": 2.0}
    assert decoder.decode({"x": 1, "geo_lat": 1.0, "geo_lon": 2.0}) == obj
    assert decoder.decode({"x": 1}) == DataClass(1)


def test_flatten_with_json_mixin():
    @dataclass
    class DataClass(DataClassJSONMixin):
        x: int
        geo: Geo = field(metadata=field_options(flatten=True))

    obj = DataClass(1, Geo(1.0, 2.0))
    assert obj.to_json() == '{"x": 1, "lat": 1.0, "lon": 2.0}'
    assert DataClass.from_json(obj.to_json()) == obj


@dataclass
class _ForwardRefHolder(DataClassDictMixin):
    x: int
    later: "_DefinedLater" = field(metadata=field_options(flatten=True))


@dataclass
class _DefinedLater:
    y: int


def test_flatten_with_forward_reference():
    obj = _ForwardRefHolder(1, _DefinedLater(2))
    assert _ForwardRefHolder.from_dict({"x": 1, "y": 2}) == obj
    assert obj.to_dict() == {"x": 1, "y": 2}


def _make_class(field_type, *, fields=None, config=None, **options):
    namespace = {
        "__annotations__": {**(fields or {}), "flat": field_type},
        "flat": field(metadata=field_options(**options)),
    }
    if config is not None:
        namespace["Config"] = config
    return dataclass(type("DataClass", (DataClassDictMixin,), namespace))


@pytest.mark.parametrize(
    ["field_type", "match"],
    [
        (int, "only dataclasses are supported"),
        (List[Address], "only dataclasses are supported"),
        (Union[Address, Geo], "only dataclasses are supported"),
        (T, "only dataclasses are supported"),
    ],
)
def test_flatten_non_dataclass(field_type, match):
    with pytest.raises(InvalidFlattenedField, match=match):
        _make_class(field_type, flatten=True)


@pytest.mark.parametrize(
    ["fields", "config", "match"],
    [
        ({"city": str}, None, 'key "city" collides with field "city"'),
        (
            {"x": Annotated[str, Alias("city")]},
            None,
            'key "city" collides with field "x"',
        ),
        (
            {"x": str},
            type("Config", (BaseConfig,), {"aliases": {"x": "zip"}}),
            'key "zip" collides with field "x"',
        ),
    ],
)
def test_flattened_key_collides_with_field(fields, config, match):
    with pytest.raises(InvalidFlattenedField, match=match):
        _make_class(Address, fields=fields, config=config, flatten=True)


def test_flattened_key_collides_with_metadata_alias():
    with pytest.raises(
        InvalidFlattenedField, match='key "city" collides with field "x"'
    ):

        @dataclass
        class DataClass(DataClassDictMixin):
            x: str = field(metadata=field_options(alias="city"))
            address: Address = field(metadata=field_options(flatten=True))


def test_flattened_key_collides_with_later_field():
    with pytest.raises(InvalidFlattenedField) as exc_info:

        @dataclass
        class DataClass(DataClassDictMixin):
            address: Address = field(metadata=field_options(flatten=True))
            zip: str = "0"

    exc = exc_info.value
    assert exc.field_name == "address"
    assert str(exc) == (
        f'Field "address" of type Address in {exc.holder_class_name} '
        'can\'t be flattened: key "zip" collides with field "zip"'
    )


def test_flattened_alias_collides_with_field():
    with pytest.raises(
        InvalidFlattenedField, match='key "postal" collides with field "x"'
    ):
        _make_class(
            AliasedAddress,
            fields={"x": Annotated[str, Alias("postal")]},
            flatten=True,
        )


def test_flattened_fields_collide():
    with pytest.raises(InvalidFlattenedField) as exc_info:

        @dataclass
        class DataClass(DataClassDictMixin):
            a: Address = field(metadata=field_options(flatten=True))
            b: Address = field(metadata=field_options(flatten=True))

    assert exc_info.value.field_name == "b"
    assert exc_info.value.msg == 'key "city" collides with flattened field "a"'
    assert isinstance(exc_info.value, UnserializableField)


def test_flattened_fields_with_same_prefix_collide():
    with pytest.raises(
        InvalidFlattenedField,
        match='key "x_city" collides with flattened field "a"',
    ):

        @dataclass
        class DataClass(DataClassDictMixin):
            a: Address = field(
                metadata=field_options(flatten=True, flatten_prefix="x_")
            )
            b: Address = field(
                metadata=field_options(flatten=True, flatten_prefix="x_")
            )


@pytest.mark.parametrize(
    ["options", "match"],
    [
        (
            {"flatten": True, "flatten_prefix": "x", "flatten_rename": {}},
            "mutually exclusive",
        ),
        ({"flatten_prefix": "x"}, '"flatten_prefix" option requires'),
        (
            {"flatten_rename": {"city": "x"}},
            '"flatten_rename" option requires',
        ),
        (
            {"flatten": True, "flatten_prefix": 1},
            "must be a string or True",
        ),
        (
            {"flatten": True, "flatten_rename": {"city": 1}},
            "must be a mapping with string values",
        ),
        (
            {"flatten": True, "flatten_rename": {"town": "x", "city": "y"}},
            "has keys that are not defined in Address: town",
        ),
        (
            {"flatten": True, "flatten_rename": {"city": "zip"}},
            'results in duplicate key "zip"',
        ),
        (
            {"flatten": True, "flatten_rename": {"city": "x", "zip": "x"}},
            'results in duplicate key "x"',
        ),
    ],
)
def test_invalid_flatten_options(options, match):
    with pytest.raises(InvalidFlattenedField, match=match):
        _make_class(Address, **options)


def test_flatten_rename_duplicate_keys_for_same_field():
    with pytest.raises(
        InvalidFlattenedField,
        match='duplicate keys "zip" and "postal" for the same field',
    ):
        _make_class(
            AliasedAddress,
            flatten=True,
            flatten_rename={"zip": "a", "postal": "b"},
        )


def test_flatten_rename_collides_with_field():
    with pytest.raises(
        InvalidFlattenedField, match='key "name" collides with field "name"'
    ):
        _make_class(
            Address,
            fields={"name": str},
            flatten=True,
            flatten_rename={"city": "name"},
        )


@dataclass
class _Node(DataClassDictMixin):
    value: int
    next: Optional["_Node"] = field(
        default=None,
        metadata=field_options(flatten=True, flatten_prefix="next_"),
    )


def test_recursive_flattening():
    with pytest.raises(InvalidFlattenedField, match="recursive flattening"):
        _Node.from_dict({"value": 1})


def test_flatten_validation_with_lazy_compilation():
    @dataclass
    class DataClass(DataClassDictMixin):
        x: int = field(metadata=field_options(flatten=True))

        class Config(BaseConfig):
            lazy_compilation = True

    with pytest.raises(InvalidFlattenedField):
        DataClass.from_dict({})
