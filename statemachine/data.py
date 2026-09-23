"""Per-state data declarations, runtime values, and change records.

State data is owned by a :class:`~statemachine.statemachine.StateChart` instance.
Declarations (defaults, :class:`DataVar`, and factories) live on the shared
:class:`~statemachine.state.State`. Entering a state materializes a fresh value
mapping; leaving the state drops it.
"""

from __future__ import annotations

import copy
from collections.abc import Mapping
from collections.abc import MutableMapping
from dataclasses import dataclass
from typing import Any
from typing import Callable
from typing import Dict
from typing import Iterator
from typing import List

from .exceptions import InvalidDefinition
from .i18n import _

_MISSING = object()


class DataVar:
    """A declared state-data variable with an optional type and factory.

    Provide either ``default`` or ``factory``, not both. A ``default`` is
    deep-copied on every entry. A ``factory`` is called on every entry to
    produce a fresh value. When ``type`` is set, values are checked with
    :func:`isinstance` on entry and on :meth:`StateChart.set_state_data`.

    Plain callables placed directly in a state's ``data`` dict (without
    :class:`DataVar`) are also treated as factories.
    """

    def __init__(
        self,
        default: Any = _MISSING,
        *,
        factory: Callable[[], Any] | None = None,
        type: Any = None,
    ) -> None:
        if default is not _MISSING and factory is not None:
            raise InvalidDefinition(_("DataVar cannot define both default and factory."))
        if factory is not None and not callable(factory):
            raise InvalidDefinition(_("DataVar factory must be callable."))
        self.default = None if default is _MISSING else default
        self.has_default = default is not _MISSING
        self.factory = factory
        self.type = type

    def __repr__(self) -> str:
        parts = []
        if self.has_default:
            parts.append(f"default={self.default!r}")
        if self.factory is not None:
            parts.append(f"factory={self.factory!r}")
        if self.type is not None:
            parts.append(f"type={self.type!r}")
        return f"DataVar({', '.join(parts)})"

    def validate(self, key: str, value: Any) -> None:
        """Raise :class:`InvalidDefinition` when ``value`` fails the type constraint."""
        if self.type is None:
            return
        try:
            matches = isinstance(value, self.type)
        except TypeError as err:
            raise InvalidDefinition(_("Invalid type constraint for '{}'.").format(key)) from err
        if not matches:
            raise InvalidDefinition(
                _("Value {!r} for '{}' is not an instance of {}.").format(
                    value, key, _type_label(self.type)
                )
            )


@dataclass(frozen=True)
class DataChangeInfo:
    """A single state-data assignment recorded during the current macrostep."""

    state_id: str
    key: str
    old_value: Any
    new_value: Any


def validate_data_declaration(data: Any) -> "Dict[str, Any] | None":
    """Validate a ``State(data=...)`` declaration.

    Returns the dict, or ``None`` when the state declares no data.
    """
    if data is None:
        return None
    if not isinstance(data, dict) or any(not isinstance(key, str) for key in data):
        raise InvalidDefinition(_("State data must be a dict with string keys."))
    return data


def materialize_state_data(declaration: Mapping[str, Any]) -> Dict[str, Any]:
    """Build a fresh runtime mapping from a state's data declaration."""
    return {key: _produce_value(key, spec) for key, spec in declaration.items()}


def format_data_item(key: str, spec: Any) -> str:
    """Format one declared variable for diagram annotations."""
    if isinstance(spec, DataVar):
        type_label = _type_label(spec.type) if spec.type is not None else ""
        if spec.factory is not None:
            produced = f"{_callable_name(spec.factory)}()"
            if type_label:
                return f"{key}: {type_label} = {produced}"
            return f"{key} = {produced}"
        if spec.has_default:
            if type_label:
                return f"{key}: {type_label} = {spec.default!r}"
            return f"{key} = {spec.default!r}"
        if type_label:
            return f"{key}: {type_label}"
        return key
    if callable(spec):
        return f"{key} = {_callable_name(spec)}()"
    return f"{key} = {spec!r}"


def snapshot_value(value: Any) -> Any:
    """Copy a value for change records and history snapshots."""
    try:
        return copy.deepcopy(value)
    except Exception:
        return value


class ScopedStateData(MutableMapping[str, Any]):
    """Live merged view of ancestor state data for one callback.

    Child keys shadow ancestor keys. Parallel regions are isolated because a
    sibling region is not on the ancestor chain. Assignments write through to
    the innermost state that already owns the key.
    """

    def __init__(self, machine: Any, state: Any) -> None:
        self._machine = machine
        state_ids: List[str] = []
        current = state
        while current is not None:
            state_ids.append(current.id)
            current = current.parent
        state_ids.reverse()
        self._state_ids = state_ids

    def _merged(self) -> Dict[str, Any]:
        merged: Dict[str, Any] = {}
        root = self._machine._root_data
        if root:
            merged.update(root)
        data_map = self._machine._state_data
        for state_id in self._state_ids:
            layer = data_map.get(state_id)
            if layer:
                merged.update(layer)
        return merged

    def __getitem__(self, key: str) -> Any:
        return self._merged()[key]

    def __setitem__(self, key: str, value: Any) -> None:
        machine = self._machine
        for state_id in reversed(self._state_ids):
            layer = machine._state_data.get(state_id)
            if layer is not None and key in layer:
                machine.set_state_data(state_id, key, value)
                return
        if key in machine._root_data:
            old = machine._root_data[key]
            machine._root_data[key] = value
            machine._record_data_change("__root__", key, old, value)
            return
        raise InvalidDefinition(
            _("'{}' is not a declared data key in the active state scope.").format(key)
        )

    def __delitem__(self, key: str) -> None:
        raise InvalidDefinition(_("State data keys cannot be removed."))

    def __iter__(self) -> Iterator[str]:
        return iter(self._merged())

    def __len__(self) -> int:
        return len(self._merged())

    def __eq__(self, other: object) -> bool:
        if isinstance(other, Mapping):
            return self._merged() == dict(other)
        return NotImplemented

    def __repr__(self) -> str:
        return repr(self._merged())


def _produce_value(key: str, spec: Any) -> Any:
    if isinstance(spec, DataVar):
        if spec.factory is not None:
            value = spec.factory()
        elif spec.has_default:
            value = copy.deepcopy(spec.default)
        else:
            value = None
        spec.validate(key, value)
        return value
    if callable(spec):
        return spec()
    return copy.deepcopy(spec)


def _callable_name(func: Callable[..., Any]) -> str:
    return getattr(func, "__name__", "factory")


def _type_label(expected: Any) -> str:
    if isinstance(expected, tuple):
        return " | ".join(getattr(item, "__name__", str(item)) for item in expected)
    return getattr(expected, "__name__", str(expected))
