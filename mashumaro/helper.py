from collections.abc import Callable
from typing import Any, Optional, TypeVar, Union

from typing_extensions import Literal

from mashumaro.types import SerializationStrategy

__all__ = [
    "field_options",
    "pass_through",
]


def _flatten_dict(
    value: dict[str, Any], prefix: Union[str, None], rename: Optional[dict[str, str]]
) -> dict[str, Any]:
    result = {}
    for key, item in value.items():
        output_key = rename.get(key, key) if rename else key
        result[(prefix or "") + output_key] = item
    return result


def _unflatten_dict(
    value: dict[str, Any], keys: set[str], prefix: Union[str, None],
    rename: Optional[dict[str, str]],
) -> dict[str, Any]:
    result = {}
    reverse = {v: k for k, v in (rename or {}).items()}
    for key in keys:
        output_key = (prefix or "") + (rename.get(key, key) if rename else key)
        if output_key in value:
            result[key] = value[output_key]
        elif (prefix or "") + reverse.get(key, key) in value:
            result[key] = value[(prefix or "") + reverse.get(key, key)]
    return result


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
    flatten_prefix: Union[str, bool, None] = None,
    flatten_rename: Optional[dict[str, str]] = None,
    **kwargs: Any,
) -> dict[str, Any]:
    options = {
        "serialize": serialize,
        "deserialize": deserialize,
        "serialization_strategy": serialization_strategy,
        "alias": alias,
        **kwargs,
    }
    if flatten:
        options["flatten"] = True
    if flatten_prefix is not None:
        options["flatten_prefix"] = flatten_prefix
    if flatten_rename is not None:
        options["flatten_rename"] = flatten_rename
    return options


class _PassThrough(SerializationStrategy):
    def __call__(self, *args: Any, **kwargs: Any) -> Any:
        raise NotImplementedError

    def serialize(self, value: T) -> T:
        return value

    def deserialize(self, value: T) -> T:
        return value


pass_through = _PassThrough()
