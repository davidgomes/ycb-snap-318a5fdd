from dataclasses import dataclass
from typing import Any, Callable

_UNSET = object()

@dataclass(frozen=True)
class DataVar:
    """A state-owned variable declaration."""

    default: Any = _UNSET
    factory: Callable[[], Any] | None = None
    type: type | tuple[type, ...] | None = None

    def __post_init__(self):
        if self.default is not _UNSET and self.factory is not None:
            from .exceptions import InvalidDefinition

            raise InvalidDefinition("'default' and 'factory' cannot be specified together.")

    def create(self):
        value = (
            self.factory()
            if self.factory is not None
            else None if self.default is _UNSET else self.default
        )
        if self.type is not None and not isinstance(value, self.type):
            from .exceptions import InvalidDefinition

            raise InvalidDefinition(f"Data value {value!r} does not match {self.type!r}.")
        return value


@dataclass(frozen=True)
class DataChangeInfo:
    state_id: str
    key: str
    old_value: Any
    new_value: Any
