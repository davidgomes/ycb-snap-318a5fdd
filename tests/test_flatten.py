from dataclasses import dataclass, field
from typing import Optional

import pytest
from typing_extensions import Annotated

from mashumaro import DataClassDictMixin, field_options
from mashumaro.config import BaseConfig
from mashumaro.exceptions import ExtraKeysError
from mashumaro.types import Alias


@dataclass
class Address(DataClassDictMixin):
    city: str
    zip_code: str = field(metadata=field_options(alias="zip"))


@dataclass
class Point:
    x: int
    y: int = 0


def test_flatten_roundtrip():
    @dataclass
    class User(DataClassDictMixin):
        name: str
        address: Address = field(metadata=field_options(flatten=True))

    obj = User("a", Address("c", "z"))
    assert obj.to_dict() == {"name": "a", "city": "c", "zip_code": "z"}
    assert User.from_dict({"name": "a", "city": "c", "zip": "z"}) == obj


def test_flatten_prefix():
    @dataclass
    class A(DataClassDictMixin):
        p: Point = field(metadata=field_options(flatten=True, flatten_prefix=True))
        q: Point = field(
            metadata=field_options(flatten=True, flatten_prefix="q.")
        )

    obj = A(Point(1, 2), Point(3, 4))
    d = {"p_x": 1, "p_y": 2, "q.x": 3, "q.y": 4}
    assert obj.to_dict() == d
    assert A.from_dict(d) == obj


def test_flatten_rename():
    @dataclass
    class A(DataClassDictMixin):
        p: Point = field(
            metadata=field_options(flatten=True, flatten_rename={"x": "px"})
        )

    assert A(Point(1, 2)).to_dict() == {"px": 1, "y": 2}
    assert A.from_dict({"px": 1}) == A(Point(1, 0))


def test_flatten_optional():
    @dataclass
    class A(DataClassDictMixin):
        p: Optional[Point] = field(
            default=None, metadata=field_options(flatten=True)
        )

    assert A().to_dict() == {}
    assert A.from_dict({}) == A()
    assert A.from_dict({"x": 1}) == A(Point(1))

    @dataclass
    class B(DataClassDictMixin):
        p: Optional[Point] = field(metadata=field_options(flatten=True))

    assert B.from_dict({}) == B(None)


def test_child_keeps_config():
    @dataclass
    class Child(DataClassDictMixin):
        a: int

        class Config(BaseConfig):
            aliases = {"a": "A"}
            serialize_by_alias = True

    @dataclass
    class P(DataClassDictMixin):
        c: Child = field(metadata=field_options(flatten=True))

        class Config(BaseConfig):
            forbid_extra_keys = True

    assert P(Child(1)).to_dict() == {"A": 1}
    assert P.from_dict({"A": 1}) == P(Child(1))
    with pytest.raises(ExtraKeysError):
        P.from_dict({"A": 1, "b": 2})


@pytest.mark.parametrize(
    "opts",
    [
        {"flatten": True},
        {"flatten": True, "flatten_rename": {"x": "z"}},
    ],
)
def test_collision(opts):
    with pytest.raises(ValueError):

        @dataclass
        class A(DataClassDictMixin):
            z: Annotated[int, Alias("x")]
            p: Point = field(metadata=field_options(**opts))


def test_collision_between_flattened():
    with pytest.raises(ValueError):

        @dataclass
        class A(DataClassDictMixin):
            p: Point = field(metadata=field_options(flatten=True))
            q: Point = field(metadata=field_options(flatten=True))


def test_invalid():
    with pytest.raises(ValueError):
        field_options(flatten=True, flatten_prefix="a", flatten_rename={})
    with pytest.raises(ValueError):

        @dataclass
        class A(DataClassDictMixin):
            p: int = field(metadata=field_options(flatten=True))

    with pytest.raises(ValueError):

        @dataclass
        class B(DataClassDictMixin):
            p: Point = field(
                metadata=field_options(
                    flatten=True, flatten_rename={"nope": "a"}
                )
            )

    with pytest.raises(ValueError):

        @dataclass
        class C(DataClassDictMixin):
            a: Address = field(
                metadata=field_options(
                    flatten=True,
                    flatten_rename={"zip": "a", "zip_code": "b"},
                )
            )
