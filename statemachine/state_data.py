from __future__ import annotations

import copy
from dataclasses import dataclass
from typing import TYPE_CHECKING
from typing import Any
from typing import Callable
from typing import Dict

from .exceptions import InvalidDefinition
from .i18n import _

if TYPE_CHECKING:
    from .state import State


def _is_factory(value: Any) -> bool:
    return callable(value) and not isinstance(value, (type, DataVar))


class DataVar:
    """Declarative state data variable with optional type and factory."""

    def __init__(
        self,
        default: Any = None,
        *,
        factory: Callable[[], Any] | None = None,
        type: type | None = None,
    ):
        if factory is not None and default is not None:
            raise InvalidDefinition(
                _("DataVar cannot specify both 'default' and 'factory'.")
            )
        self.default = default
        self.factory = factory
        self.type = type


@dataclass(frozen=True)
class DataChangeInfo:
    """Record of a state data mutation during the current macrostep."""

    state_id: str
    key: str
    old_value: Any
    new_value: Any


def validate_data_schema(data: Any) -> Dict[str, Any]:
    """Validate and normalize a state's ``data`` declaration."""
    if not isinstance(data, dict):
        raise InvalidDefinition(_("State 'data' must be a dict with string keys."))
    for key in data:
        if not isinstance(key, str):
            raise InvalidDefinition(_("State 'data' must be a dict with string keys."))
    return data


def create_data_value(spec: Any) -> Any:
    """Create a fresh runtime value from a data schema entry."""
    if isinstance(spec, DataVar):
        if spec.factory is not None:
            value = spec.factory()
        elif spec.default is not None:
            value = copy.deepcopy(spec.default)
        else:
            value = None
        if spec.type is not None and value is not None and not isinstance(value, spec.type):
            raise InvalidDefinition(
                _("Data value for key does not match declared type {type!r}.").format(
                    type=spec.type
                )
            )
        return value
    if _is_factory(spec):
        return spec()
    return copy.deepcopy(spec)


def init_state_data(state: State) -> Dict[str, Any]:
    """Initialize a fresh data dict from a state's schema."""
    schema = getattr(state, "_data_schema", None)
    if not schema:
        return {}
    return {key: create_data_value(spec) for key, spec in schema.items()}


def validate_set_value(state: State, key: str, value: Any) -> None:
    """Validate a programmatic data assignment against the schema."""
    schema = getattr(state, "_data_schema", None)
    if not schema or key not in schema:
        raise InvalidDefinition(
            _("Key {key!r} is not declared in state {state!r} data.").format(
                key=key, state=state.id
            )
        )
    spec = schema[key]
    if isinstance(spec, DataVar) and spec.type is not None and not isinstance(value, spec.type):
        raise InvalidDefinition(
            _("Value for {key!r} does not match declared type {type!r}.").format(
                key=key, type=spec.type
            )
        )


def format_data_vars(schema: Dict[str, Any]) -> list[str]:
    """Format data variable names for diagram annotations."""
    labels: list[str] = []
    for key, spec in schema.items():
        if isinstance(spec, DataVar) and spec.type is not None:
            labels.append(f"{key}: {spec.type.__name__}")
        else:
            labels.append(key)
    return labels
