from collections.abc import Callable
from typing import Any, Optional, TypeVar, Union

from typing_extensions import Literal

from mashumaro.types import SerializationStrategy

__all__ = [
    "field_options",
    "pass_through",
]


NamedTupleDeserializationEngine = Literal["as_dict", "as_list"]
DateTimeDeserializationEngine = Literal["ciso8601", "pendulum"]
AnyDeserializationEngine = Literal[
    NamedTupleDeserializationEngine, DateTimeDeserializationEngine
]

NamedTupleSerializationEngine = Literal["as_dict", "as_list"]
OmitSerializationEngine = Literal["omit"]
AnySerializationEngine = Union[
    NamedTupleSerializationEngine, OmitSerializationEngine
]


T = TypeVar("T")


def field_options(
    serialize: Optional[
        Union[AnySerializationEngine, Callable[[Any], Any]]
    ] = None,
    deserialize: Optional[
        Union[AnyDeserializationEngine, Callable[[Any], Any]]
    ] = None,
    serialization_strategy: Optional[SerializationStrategy] = None,
    alias: Optional[str] = None,
    flatten: bool = False,
    flatten_prefix: Optional[Union[str, bool]] = None,
    flatten_rename: Optional[dict[str, str]] = None,
    **kwargs: Any,
) -> dict[str, Any]:
    if flatten_prefix is not None and flatten_rename is not None:
        raise ValueError(
            "flatten_prefix and flatten_rename are mutually exclusive"
        )
    if (flatten_prefix is not None or flatten_rename is not None) and (
        not flatten
    ):
        raise ValueError(
            "flatten_prefix and flatten_rename require flatten=True"
        )
    result = {
        "serialize": serialize,
        "deserialize": deserialize,
        "serialization_strategy": serialization_strategy,
        "alias": alias,
        **kwargs,
    }
    if flatten:
        result["flatten"] = True
        if flatten_prefix is not None:
            result["flatten_prefix"] = flatten_prefix
        if flatten_rename is not None:
            result["flatten_rename"] = dict(flatten_rename)
    return result


class _PassThrough(SerializationStrategy):
    def __call__(self, *args: Any, **kwargs: Any) -> Any:
        raise NotImplementedError

    def serialize(self, value: T) -> T:
        return value

    def deserialize(self, value: T) -> T:
        return value


pass_through = _PassThrough()
