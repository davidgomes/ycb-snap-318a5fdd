from dataclasses import dataclass
from typing import Any, Callable, Optional

from .exceptions import InvalidDefinition

@dataclass(frozen=True)
class DataVar:
    """A state data declaration with optional type checking or factory."""

    default: Any = None
    type: Optional[Any] = None
    factory: Optional[Callable[[], Any]] = None

    def __post_init__(self):
        if self.default is not None and self.factory is not None:
            raise InvalidDefinition("DataVar cannot specify both default and factory")

    def create(self) -> Any:
        value = self.factory() if self.factory is not None else self.default
        if self.type is not None and not isinstance(value, self.type):
            raise InvalidDefinition(f"DataVar default must be an instance of {self.type!r}")
        return value


@dataclass(frozen=True)
class DataChangeInfo:
    state_id: str
    key: str
    old_value: Any
    new_value: Any
