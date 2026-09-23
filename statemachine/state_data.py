from __future__ import annotations

from copy import deepcopy
from dataclasses import dataclass
from typing import Any
from typing import Callable
from typing import Dict
from typing import Mapping

from .exceptions import InvalidDefinition
from .i18n import _

_MISSING = object()


@dataclass
class DataChangeInfo:
    """A change to one key of a state's data during the current macrostep."""

    state_id: str
    key: str
    old_value: Any
    new_value: Any


class DataVar:
    """A declared state-data variable with an optional type and factory.

    Provide either ``default`` or ``factory``, not both. When ``type`` is set,
    assigned values must be instances of that type. A ``factory`` is called with
    no arguments on every state entry to produce a fresh value.
    """

    def __init__(
        self,
        default: Any = _MISSING,
        *,
        type: type | None = None,
        factory: Callable[[], Any] | None = None,
    ):
        if default is not _MISSING and factory is not None:
            raise InvalidDefinition(_("DataVar cannot define both default and factory."))
        self.type = type
        self.factory = factory
        self.has_default = default is not _MISSING
        self.default = None if default is _MISSING else default

    def produce(self) -> Any:
        if self.factory is not None:
            value = self.factory()
        elif self.has_default:
            value = deepcopy(self.default)
        else:
            value = None
        self.check(value)
        return value

    def check(self, value: Any) -> None:
        if self.type is not None and value is not None and not isinstance(value, self.type):
            raise InvalidDefinition(
                _("Value {value!r} for state data does not match type {type_name}.").format(
                    value=value, type_name=self.type.__name__
                )
            )


def validate_data_spec(data: Mapping[str, Any] | None) -> Dict[str, Any] | None:
    """Validate a ``State`` data declaration and return a shallow copy."""
    if data is None:
        return None
    if not isinstance(data, dict):
        raise InvalidDefinition(_("State data must be a dict of string keys to default values."))
    for key in data:
        if not isinstance(key, str):
            raise InvalidDefinition(_("State data keys must be strings."))
    return dict(data)


def materialize_data(spec: Mapping[str, Any]) -> Dict[str, Any]:
    """Build a fresh data dict from a declaration."""
    values: Dict[str, Any] = {}
    for key, item in spec.items():
        if isinstance(item, DataVar):
            values[key] = item.produce()
        elif callable(item):
            values[key] = item()
        else:
            values[key] = deepcopy(item)
    return values


def check_declared_value(spec_item: Any, value: Any) -> None:
    if isinstance(spec_item, DataVar):
        spec_item.check(value)
