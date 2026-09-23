from dataclasses import dataclass, field
from typing import Generic, Optional, TypeVar

import pytest
from typing_extensions import Annotated

from mashumaro import DataClassDictMixin, field_options
from mashumaro.config import BaseConfig
from mashumaro.exceptions import ExtraKeysError, FlattenError, MissingField
from mashumaro.mixins.json import DataClassJSONMixin
from mashumaro.types import Alias, SerializationStrategy


def test_field_options_flatten_keys_are_opt_in():
    assert "flatten" not in field_options()
    assert "flatten_prefix" not in field_options()
    assert "flatten_rename" not in field_options()
    assert field_options(flatten=True)["flatten"] is True
    assert (
        field_options(flatten=True, flatten_prefix="p_")["flatten_prefix"]
        == "p_"
    )
    assert (
        field_options(flatten=True, flatten_prefix=True)["flatten_prefix"]
        is True
    )
    assert field_options(flatten=True, flatten_rename={"a": "b"})[
        "flatten_rename"
    ] == {"a": "b"}


def test_flatten_round_trip():
    @dataclass
    class Child(DataClassDictMixin):
        x: int
        y: str

    @dataclass
    class Parent(DataClassDictMixin):
        name: str
        child: Child = field(metadata=field_options(flatten=True))

    instance = Parent("ada", Child(1, "b"))
    dumped = {"name": "ada", "x": 1, "y": "b"}
    assert instance.to_dict() == dumped
    assert "child" not in dumped
    assert Parent.from_dict(dumped) == instance


def test_flatten_prefix_string_and_auto():
    @dataclass
    class Child(DataClassDictMixin):
        x: int

    @dataclass
    class Parent(DataClassDictMixin):
        name: str
        home: Child = field(
            metadata=field_options(flatten=True, flatten_prefix="home_")
        )
        work: Child = field(
            metadata=field_options(flatten=True, flatten_prefix=True)
        )

    instance = Parent("ada", Child(1), Child(2))
    dumped = {"name": "ada", "home_x": 1, "work_x": 2}
    assert instance.to_dict() == dumped
    assert Parent.from_dict(dumped) == instance


def test_flatten_rename():
    @dataclass
    class Child(DataClassDictMixin):
        city: str
        zip_code: str

    @dataclass
    class Parent(DataClassDictMixin):
        name: str
        address: Child = field(
            metadata=field_options(
                flatten=True,
                flatten_rename={"city": "town", "zip_code": "postal"},
            )
        )

    instance = Parent("ada", Child("NYC", "10001"))
    dumped = {"name": "ada", "town": "NYC", "postal": "10001"}
    assert instance.to_dict() == dumped
    assert Parent.from_dict(dumped) == instance


def test_optional_flattened_field():
    @dataclass
    class Child(DataClassDictMixin):
        x: int
        y: int = 2

    @dataclass
    class Parent(DataClassDictMixin):
        name: str
        child: Optional[Child] = field(
            default=None, metadata=field_options(flatten=True)
        )

    assert Parent("ada", None).to_dict() == {"name": "ada"}
    assert Parent.from_dict({"name": "ada"}) == Parent("ada", None)
    assert Parent.from_dict({"name": "ada", "x": 1}) == Parent(
        "ada", Child(1, 2)
    )
    instance = Parent("ada", Child(3, 4))
    assert Parent.from_dict(instance.to_dict()) == instance
    with pytest.raises(MissingField):
        Parent.from_dict({"name": "ada", "y": 9})


def test_optional_without_default_still_requires_data():
    @dataclass
    class Child(DataClassDictMixin):
        x: int = 1

    @dataclass
    class Parent(DataClassDictMixin):
        child: Optional[Child] = field(metadata=field_options(flatten=True))

    with pytest.raises(MissingField):
        Parent.from_dict({})
    assert Parent.from_dict({"x": 5}) == Parent(Child(5))


def test_required_flatten_uses_child_defaults():
    @dataclass
    class Child(DataClassDictMixin):
        x: int = 1

    @dataclass
    class Parent(DataClassDictMixin):
        child: Child = field(metadata=field_options(flatten=True))

    assert Parent.from_dict({}) == Parent(Child(1))
    assert Parent.from_dict({"x": 4}) == Parent(Child(4))


def test_child_config_is_preserved():
    class AsStr(SerializationStrategy):
        def serialize(self, value: int) -> str:
            return str(value)

        def deserialize(self, value: str) -> int:
            return int(value)

    @dataclass
    class Child(DataClassDictMixin):
        y: int = field(
            metadata=field_options(
                alias="y_alias", serialization_strategy=AsStr()
            )
        )
        x: Optional[int] = None

        class Config(BaseConfig):
            omit_none = True
            serialize_by_alias = True

    @dataclass
    class Parent(DataClassDictMixin):
        name: str
        child: Child = field(metadata=field_options(flatten=True))

        class Config(BaseConfig):
            omit_none = False

    instance = Parent("ada", Child(7))
    assert instance.to_dict() == {"name": "ada", "y_alias": "7"}
    assert Parent.from_dict({"name": "ada", "y_alias": "7"}) == Parent(
        "ada", Child(7)
    )


def test_parent_omit_none_does_not_override_child():
    @dataclass
    class Child(DataClassDictMixin):
        x: Optional[int] = None

    @dataclass
    class Parent(DataClassDictMixin):
        child: Child = field(metadata=field_options(flatten=True))

        class Config(BaseConfig):
            omit_none = True

    assert Parent(Child(None)).to_dict() == {"x": None}
    assert Parent.from_dict({"x": None}) == Parent(Child(None))


def test_nested_flatten_keeps_inner_config():
    @dataclass
    class Grand(DataClassDictMixin):
        x: int

        class Config(BaseConfig):
            aliases = {"x": "gx"}
            serialize_by_alias = True

    @dataclass
    class Child(DataClassDictMixin):
        grand: Grand = field(
            metadata=field_options(flatten=True, flatten_prefix="g_")
        )
        y: int

    @dataclass
    class Parent(DataClassDictMixin):
        child: Child = field(
            metadata=field_options(flatten=True, flatten_prefix="c_")
        )
        name: str

    instance = Parent(Child(Grand(1), 2), "ada")
    dumped = {"c_g_gx": 1, "c_y": 2, "name": "ada"}
    assert instance.to_dict() == dumped
    assert Parent.from_dict(dumped) == instance


def test_forbid_extra_keys_accounts_for_flattened_keys():
    @dataclass
    class Child(DataClassDictMixin):
        x: int = field(metadata=field_options(alias="x_alias"))

        class Config(BaseConfig):
            forbid_extra_keys = True

    @dataclass
    class Parent(DataClassDictMixin):
        name: str
        child: Child = field(metadata=field_options(flatten=True))

        class Config(BaseConfig):
            forbid_extra_keys = True

    assert Parent.from_dict({"name": "ada", "x_alias": 1}) == Parent(
        "ada", Child(1)
    )
    with pytest.raises(ExtraKeysError) as exc_info:
        Parent.from_dict({"name": "ada", "x_alias": 1, "x": 1, "extra": 2})
    assert exc_info.value.extra_keys == {"x", "extra"}

    @dataclass
    class ChildAllowsFieldName(DataClassDictMixin):
        x: int = field(metadata=field_options(alias="x_alias"))

        class Config(BaseConfig):
            allow_deserialization_not_by_alias = True
            forbid_extra_keys = True

    @dataclass
    class ParentAllows(DataClassDictMixin):
        name: str
        child: ChildAllowsFieldName = field(
            metadata=field_options(flatten=True)
        )

        class Config(BaseConfig):
            forbid_extra_keys = True

    assert ParentAllows.from_dict({"name": "ada", "x": 3}) == ParentAllows(
        "ada", ChildAllowsFieldName(3)
    )


def test_child_forbid_extra_keys_does_not_see_parent_keys():
    @dataclass
    class Child(DataClassDictMixin):
        x: int

        class Config(BaseConfig):
            forbid_extra_keys = True

    @dataclass
    class Parent(DataClassDictMixin):
        name: str
        child: Child = field(metadata=field_options(flatten=True))

    assert Parent.from_dict({"name": "ada", "x": 1, "ignored": 2}) == Parent(
        "ada", Child(1)
    )


def test_alias_collisions_are_rejected():
    @dataclass
    class ChildMeta(DataClassDictMixin):
        x: int = field(metadata={"alias": "y"})

    with pytest.raises(FlattenError, match="collision"):

        @dataclass
        class _(DataClassDictMixin):
            y: int
            child: ChildMeta = field(metadata=field_options(flatten=True))

    @dataclass
    class ChildAnnotated(DataClassDictMixin):
        x: Annotated[int, Alias("y")]

    with pytest.raises(FlattenError, match="collision"):

        @dataclass
        class _(DataClassDictMixin):
            y: int
            child: ChildAnnotated = field(metadata=field_options(flatten=True))

    @dataclass
    class ChildConfig(DataClassDictMixin):
        x: int

        class Config(BaseConfig):
            aliases = {"x": "y"}

    with pytest.raises(FlattenError, match="collision"):

        @dataclass
        class _(DataClassDictMixin):
            y: int
            child: ChildConfig = field(metadata=field_options(flatten=True))

    @dataclass
    class ChildPlain(DataClassDictMixin):
        y: int

    with pytest.raises(FlattenError, match="collision"):

        @dataclass
        class _(DataClassDictMixin):
            x: int = field(metadata=field_options(alias="y"))
            child: ChildPlain = field(metadata=field_options(flatten=True))

    with pytest.raises(FlattenError, match="collision"):

        @dataclass
        class _(DataClassDictMixin):
            x: Annotated[int, Alias("y")]
            child: ChildPlain = field(metadata=field_options(flatten=True))

    with pytest.raises(FlattenError, match="collision"):

        @dataclass
        class _(DataClassDictMixin):
            x: int
            child: ChildPlain = field(metadata=field_options(flatten=True))

            class Config(BaseConfig):
                aliases = {"x": "y"}


def test_aliased_child_does_not_collide_with_field_name_when_serialized_by_alias():
    @dataclass
    class Child(DataClassDictMixin):
        x: int = field(metadata=field_options(alias="x_alias"))

        class Config(BaseConfig):
            serialize_by_alias = True

    @dataclass
    class Parent(DataClassDictMixin):
        x: int
        child: Child = field(metadata=field_options(flatten=True))

    instance = Parent(1, Child(2))
    assert instance.to_dict() == {"x": 1, "x_alias": 2}
    assert Parent.from_dict({"x": 1, "x_alias": 2}) == instance


def test_two_flattened_fields_collide_without_prefix():
    @dataclass
    class Child(DataClassDictMixin):
        x: int

    with pytest.raises(FlattenError, match="collision"):

        @dataclass
        class _(DataClassDictMixin):
            a: Child = field(metadata=field_options(flatten=True))
            b: Child = field(metadata=field_options(flatten=True))


def test_flatten_validation_errors():
    @dataclass
    class Child(DataClassDictMixin):
        x: int
        y: int

    with pytest.raises(FlattenError, match="not a dataclass"):

        @dataclass
        class _(DataClassDictMixin):
            value: int = field(metadata=field_options(flatten=True))

    with pytest.raises(FlattenError, match="not a dataclass"):

        @dataclass
        class _(DataClassDictMixin):
            value: Optional[int] = field(
                default=None, metadata=field_options(flatten=True)
            )

    with pytest.raises(FlattenError, match="not a dataclass"):

        @dataclass
        class _(DataClassDictMixin):
            value: list[Child] = field(metadata=field_options(flatten=True))

    with pytest.raises(FlattenError, match="mutually exclusive"):

        @dataclass
        class _(DataClassDictMixin):
            child: Child = field(
                metadata=field_options(
                    flatten=True,
                    flatten_prefix="p_",
                    flatten_rename={"x": "z"},
                )
            )

    with pytest.raises(FlattenError, match="require flatten=True"):

        @dataclass
        class _(DataClassDictMixin):
            child: Child = field(metadata=field_options(flatten_prefix="p_"))

    with pytest.raises(FlattenError, match="Invalid flatten_rename key"):

        @dataclass
        class _(DataClassDictMixin):
            child: Child = field(
                metadata=field_options(
                    flatten=True, flatten_rename={"missing": "z"}
                )
            )

    with pytest.raises(FlattenError, match="Duplicate flatten_rename"):

        @dataclass
        class _(DataClassDictMixin):
            child: Child = field(
                metadata=field_options(
                    flatten=True, flatten_rename={"x": "z", "y": "z"}
                )
            )

    with pytest.raises(FlattenError, match="collision"):

        @dataclass
        class _(DataClassDictMixin):
            name: str
            child: Child = field(
                metadata=field_options(
                    flatten=True, flatten_rename={"x": "name"}
                )
            )

    with pytest.raises(FlattenError, match="Invalid flatten_prefix"):

        @dataclass
        class _(DataClassDictMixin):
            child: Child = field(
                metadata={"flatten": True, "flatten_prefix": 1}
            )

    with pytest.raises(FlattenError, match="Invalid flatten value"):

        @dataclass
        class _(DataClassDictMixin):
            child: Child = field(metadata={"flatten": "yes"})

    with pytest.raises(FlattenError, match="Invalid flatten_rename"):

        @dataclass
        class _(DataClassDictMixin):
            child: Child = field(
                metadata={"flatten": True, "flatten_rename": ["x"]}
            )


def test_rename_invalid_when_target_field_is_itself_flattened():
    @dataclass
    class Grand(DataClassDictMixin):
        x: int

    @dataclass
    class Child(DataClassDictMixin):
        grand: Grand = field(metadata=field_options(flatten=True))
        y: int

    with pytest.raises(FlattenError, match="Invalid flatten_rename key"):

        @dataclass
        class _(DataClassDictMixin):
            child: Child = field(
                metadata=field_options(
                    flatten=True, flatten_rename={"grand": "g"}
                )
            )


@dataclass
class CycleNode(DataClassDictMixin):
    child: Optional["CycleNode"] = field(
        default=None, metadata=field_options(flatten=True)
    )


def test_cyclic_flatten_is_rejected():
    with pytest.raises(FlattenError, match="Cyclic flatten"):
        CycleNode.from_dict({})


def test_plain_dataclass_child():
    @dataclass
    class Child:
        x: int
        y: str = "z"

    @dataclass
    class Parent(DataClassDictMixin):
        child: Child = field(metadata=field_options(flatten=True))
        name: str

    instance = Parent(Child(1), "ada")
    assert instance.to_dict() == {"x": 1, "y": "z", "name": "ada"}
    assert Parent.from_dict({"x": 1, "name": "ada"}) == Parent(
        Child(1, "z"), "ada"
    )


def test_json_mixin_flatten():
    @dataclass
    class Child(DataClassJSONMixin):
        x: int

    @dataclass
    class Parent(DataClassJSONMixin):
        child: Child = field(metadata=field_options(flatten=True))

    text = Parent(Child(3)).to_json()
    assert Parent.from_json(text) == Parent(Child(3))


def test_inherited_flatten():
    @dataclass
    class Child(DataClassDictMixin):
        x: int

    @dataclass
    class Base(DataClassDictMixin):
        child: Child = field(metadata=field_options(flatten=True))

    @dataclass
    class Sub(Base):
        name: str

    instance = Sub(Child(1), "ada")
    assert instance.to_dict() == {"x": 1, "name": "ada"}
    assert Sub.from_dict({"x": 1, "name": "ada"}) == instance

    with pytest.raises(FlattenError, match="collision"):

        @dataclass
        class _(Base):
            x: int


def test_flatten_partial_rename_keeps_other_keys():
    @dataclass
    class Child(DataClassDictMixin):
        x: int
        y: int

    @dataclass
    class Parent(DataClassDictMixin):
        child: Child = field(
            metadata=field_options(flatten=True, flatten_rename={"x": "x2"})
        )

    instance = Parent(Child(1, 2))
    assert instance.to_dict() == {"x2": 1, "y": 2}
    assert Parent.from_dict({"x2": 1, "y": 2}) == instance


def test_omit_default_skips_unchanged_flattened_value():
    @dataclass
    class Child(DataClassDictMixin):
        x: int

    @dataclass
    class Parent(DataClassDictMixin):
        name: str = "ada"
        child: Child = field(
            default_factory=lambda: Child(1),
            metadata=field_options(flatten=True),
        )

        class Config(BaseConfig):
            omit_default = True

    assert Parent().to_dict() == {}
    assert Parent("bea", Child(2)).to_dict() == {"name": "bea", "x": 2}
    assert Parent.from_dict({}) == Parent()
    assert Parent.from_dict({"x": 3}) == Parent("ada", Child(3))


def test_lazy_compilation_validates_flatten_at_class_creation():
    with pytest.raises(FlattenError, match="not a dataclass"):

        @dataclass
        class _(DataClassDictMixin):
            value: int = field(metadata=field_options(flatten=True))

            class Config(BaseConfig):
                lazy_compilation = True


def test_annotated_and_generic_flatten():
    @dataclass
    class Child(DataClassDictMixin):
        x: int

    @dataclass
    class Parent(DataClassDictMixin):
        child: Annotated[Child, "note"] = field(
            metadata=field_options(flatten=True)
        )

    assert Parent.from_dict({"x": 4}) == Parent(Child(4))

    t = TypeVar("t")

    @dataclass
    class Box(DataClassDictMixin, Generic[t]):
        item: t

    @dataclass
    class Holder(DataClassDictMixin):
        box: Box[int] = field(
            metadata=field_options(flatten=True, flatten_prefix=True)
        )

    holder = Holder(Box(9))
    assert holder.to_dict() == {"box_item": 9}
    assert Holder.from_dict({"box_item": 9}) == holder


def test_slots_flatten():
    @dataclass(slots=True)
    class Child(DataClassDictMixin):
        x: int

    @dataclass(slots=True)
    class Parent(DataClassDictMixin):
        child: Child = field(metadata=field_options(flatten=True))
        name: str

    instance = Parent(Child(1), "ada")
    assert instance.to_dict() == {"x": 1, "name": "ada"}
    assert Parent.from_dict(instance.to_dict()) == instance
