from copy import deepcopy
from dataclasses import dataclass
from typing import Any
from typing import Callable
from typing import Dict
from typing import Mapping

from .exceptions import InvalidDefinition
from .i18n import _

_MISSING: Any = object()


class DataVar:
    """Declares a state-scoped data variable.

    Args:
        default: The default value. A fresh deep copy is produced on every state entry.
        factory: A callable producing the initial value on every state entry.
        type: Optional type (or tuple of types) enforced on initialization and updates.
            ``None`` values are always accepted.

    ``default`` and ``factory`` are mutually exclusive.
    """

    def __init__(
        self,
        default: Any = _MISSING,
        *,
        factory: "Callable[[], Any] | None" = None,
        type: "type | tuple | None" = None,
    ):
        if default is not _MISSING and factory is not None:
            raise InvalidDefinition(_("DataVar accepts either 'default' or 'factory', not both."))
        if factory is not None and not callable(factory):
            raise InvalidDefinition(_("DataVar 'factory' must be callable."))
        self.default = None if default is _MISSING else default
        self.factory = factory
        self.type = type
        if factory is None:
            self.validate("default", self.default)

    def __repr__(self):
        if self.factory is not None:
            return f"DataVar(factory={self.factory!r}, type={self.type!r})"
        return f"DataVar({self.default!r}, type={self.type!r})"

    def describe(self) -> str:
        if self.factory is not None:
            return f"{getattr(self.factory, '__name__', repr(self.factory))}()"
        return repr(self.default)

    def new_value(self) -> Any:
        return self.factory() if self.factory is not None else deepcopy(self.default)

    def validate(self, key: str, value: Any):
        if self.type is not None and value is not None and not isinstance(value, self.type):
            raise InvalidDefinition(
                _("Invalid type for {!r}: expected {}, got {}.").format(
                    key, self.type, type(value).__name__
                )
            )


@dataclass(frozen=True)
class DataChangeInfo:
    """A record of a state data change made through ``set_state_data``."""

    state_id: str
    key: str
    old_value: Any
    new_value: Any


def normalize_data(data: Any) -> Dict[str, DataVar]:
    """Validate a ``data`` declaration and convert every entry into a :class:`DataVar`."""
    if data is None:
        return {}
    if not isinstance(data, dict):
        raise InvalidDefinition(
            _("State 'data' must be a dict, got {}.").format(type(data).__name__)
        )
    result: Dict[str, DataVar] = {}
    for key, value in data.items():
        if not isinstance(key, str):
            raise InvalidDefinition(_("State 'data' keys must be strings, got {!r}.").format(key))
        if isinstance(value, DataVar):
            result[key] = value
        elif callable(value):
            result[key] = DataVar(factory=value)
        else:
            result[key] = DataVar(value)
    return result


def initial_values(spec: Mapping[str, DataVar]) -> Dict[str, Any]:
    values = {}
    for key, var in spec.items():
        value = var.new_value()
        var.validate(key, value)
        values[key] = value
    return values
