"""Per-state data declarations and per-instance storage.

State data is declared on :class:`~statemachine.state.State` and stored on each
state machine instance. Values are created when a state is entered and removed
when it is exited.
"""

from __future__ import annotations

import copy
from dataclasses import dataclass
from typing import Any
from typing import Callable
from typing import Mapping

from .exceptions import InvalidDefinition
from .i18n import _

_MISSING = object()


@dataclass(frozen=True)
class DataChangeInfo:
    """A change to one state-data key during the current macrostep.

    Attributes:
        state_id: Identifier of the state that owns the key.
        key: Data key that changed.
        old_value: Value before the assignment.
        new_value: Value after the assignment.
    """

    state_id: str
    key: str
    old_value: Any
    new_value: Any


class DataVar:
    """A declared state-data variable.

    Provide ``default`` for a value that is deep-copied on every entry, or
    ``factory`` for a zero-argument callable invoked on every entry. Optional
    ``type`` is checked with :func:`isinstance` when a value is produced or
    assigned.

    Args:
        default: Value copied on each state entry.
        factory: Callable invoked with no arguments on each state entry.
        type: Optional type, or tuple of types, enforced on the value.
    """

    def __init__(
        self,
        default: Any = _MISSING,
        *,
        factory: Callable[[], Any] | None = None,
        type: type | tuple[type, ...] | None = None,
    ) -> None:
        if default is not _MISSING and factory is not None:
            raise InvalidDefinition(_("DataVar cannot specify both default and factory."))
        self._default = None if default is _MISSING else default
        self._has_default = default is not _MISSING
        self.factory = factory
        self.type = type

    @property
    def default(self) -> Any:
        """The declared default, or ``None`` when only a factory or type is set."""
        return self._default

    def produce(self) -> Any:
        """Build a fresh value for state entry."""
        if self.factory is not None:
            value = self.factory()
        elif self._has_default:
            value = copy.deepcopy(self._default)
        else:
            value = None
        self.check(value)
        return value

    def check(self, value: Any) -> None:
        """Raise :class:`InvalidDefinition` when ``value`` fails the declared type."""
        if self.type is not None and not isinstance(value, self.type):
            raise InvalidDefinition(
                _("Value {value!r} does not match data type {type!r}.").format(
                    value=value, type=self.type
                )
            )

    def annotation(self, key: str) -> str:
        """Diagram label for this variable, including ``key``."""
        type_part = f": {self._type_label()}" if self.type is not None else ""
        if self.factory is not None:
            factory_name = getattr(self.factory, "__name__", "factory")
            return f"{key}{type_part} = {factory_name}()"
        if self._has_default:
            return f"{key}{type_part} = {self._default!r}"
        if type_part:
            return f"{key}{type_part}"
        return key

    def _type_label(self) -> str:
        if isinstance(self.type, tuple):
            return " | ".join(item.__name__ for item in self.type)
        assert self.type is not None
        return self.type.__name__


def normalize_data(data: Mapping[str, Any] | None) -> dict[str, DataVar]:
    """Validate a ``data`` declaration and wrap each entry in :class:`DataVar`.

    Plain callables are treated as factories. Non-callables are defaults.
    """
    if data is None:
        return {}
    if not isinstance(data, dict):
        raise InvalidDefinition(
            _("State data must be a dict mapping string keys to default values.")
        )
    spec: dict[str, DataVar] = {}
    for key, value in data.items():
        if not isinstance(key, str):
            raise InvalidDefinition(
                _("State data keys must be strings, got {key!r}.").format(key=key)
            )
        if isinstance(value, DataVar):
            spec[key] = value
        elif callable(value):
            spec[key] = DataVar(factory=value)
        else:
            spec[key] = DataVar(default=value)
    return spec


class StateData:
    """Runtime data for one state machine instance."""

    def __init__(self) -> None:
        self.values: dict[str, dict[str, Any]] = {}
        self.history: dict[str, dict[str, dict[str, Any]]] = {}
        self.pending_restore: dict[str, dict[str, Any]] = {}
        self.changes: list[DataChangeInfo] = []
        self.saving_history_ids: list[str] = []

    def begin_macrostep(self) -> None:
        """Drop change records from the previous macrostep."""
        self.changes.clear()

    def begin_history_save(self) -> None:
        """Start a new history-save cycle for the states about to exit."""
        self.saving_history_ids = []

    def activate(self, state: Any) -> None:
        """Initialize data for ``state``, restoring a history snapshot when queued."""
        spec: dict[str, DataVar] = state._data_spec
        pending = self.pending_restore.pop(state.id, None)
        if not spec:
            return
        if pending is None:
            self.values[state.id] = {key: var.produce() for key, var in spec.items()}
            return
        restored: dict[str, Any] = {}
        for key, var in spec.items():
            if key in pending:
                value = copy.deepcopy(pending[key])
                var.check(value)
                restored[key] = value
            else:
                restored[key] = var.produce()
        self.values[state.id] = restored

    def deactivate(self, state: Any) -> None:
        """Remove ``state`` data after its exit callbacks have run."""
        data = self.values.get(state.id)
        if data is not None:
            for history_id in self.saving_history_ids:
                snapshots = self.history.get(history_id)
                if snapshots is not None and state.id in snapshots:
                    snapshots[state.id] = copy.deepcopy(data)
        self.values.pop(state.id, None)

    def scoped_values(self, state: Any) -> dict[str, Any]:
        """Merge ancestor data into ``state``, with the child shadowing ancestors.

        Only the parent chain is walked, so parallel regions do not see siblings.
        """
        chain: list[Any] = []
        current = state
        while current is not None:
            chain.append(current)
            current = current.parent
        merged: dict[str, Any] = {}
        for ancestor in reversed(chain):
            data = self.values.get(ancestor.id)
            if data:
                merged.update(data)
        return merged

    def scoped(self, state: Any) -> "ScopedStateData":
        """Writable view of :meth:`scoped_values`.

        Assignments update the nearest ancestor that declared the key and are
        recorded in the macrostep change log.
        """
        return ScopedStateData(self, state)

    def owner_of(self, state: Any, key: str) -> Any:
        """Return the nearest ancestor of ``state`` that declares ``key``."""
        current = state
        while current is not None:
            if key in getattr(current, "_data_spec", {}):
                return current
            current = current.parent
        return None

    def snapshot(self) -> dict[str, dict[str, Any]]:
        """Deep copy of every active state's data, keyed by state id."""
        return {state_id: copy.deepcopy(data) for state_id, data in self.values.items()}

    def change_log(self) -> list[DataChangeInfo]:
        """Copy of the change records for the current macrostep."""
        return list(self.changes)

    def set_value(self, state: Any, key: str, value: Any) -> None:
        """Assign ``key`` on an already-active state, enforcing its declaration."""
        spec: dict[str, DataVar] = state._data_spec
        if key not in spec:
            raise InvalidDefinition(
                _("State {state_id!r} has no data key {key!r}.").format(state_id=state.id, key=key)
            )
        spec[key].check(value)
        current = self.values.get(state.id)
        if current is None:
            raise InvalidDefinition(
                _("State {state_id!r} is not active.").format(state_id=state.id)
            )
        old = current[key]
        current[key] = value
        self.changes.append(
            DataChangeInfo(state_id=state.id, key=key, old_value=old, new_value=value)
        )

    def save_history(self, history_id: str, states: list[Any]) -> None:
        """Snapshot data for the states remembered by ``history_id``."""
        snapshots: dict[str, dict[str, Any]] = {}
        for remembered in states:
            data = self.values.get(remembered.id)
            if data is not None:
                snapshots[remembered.id] = copy.deepcopy(data)
        self.history[history_id] = snapshots
        self.saving_history_ids.append(history_id)

    def queue_restore(self, history_id: str, states: list[Any]) -> None:
        """Queue snapshots to apply when ``states`` are entered."""
        snapshots = self.history.get(history_id)
        if not snapshots:
            return
        for remembered in states:
            snap = snapshots.get(remembered.id)
            if snap is not None:
                self.pending_restore[remembered.id] = snap


class ScopedStateData(dict):
    """Merged state-data view that writes through to the declaring state."""

    def __init__(self, storage: StateData, state: Any) -> None:
        dict.__init__(self, storage.scoped_values(state))
        self._storage = storage
        self._state = state

    def __setitem__(self, key: str, value: Any) -> None:
        owner = self._storage.owner_of(self._state, key)
        if owner is None:
            raise InvalidDefinition(_("State data has no key {key!r}.").format(key=key))
        self._storage.set_value(owner, key, value)
        dict.__setitem__(self, key, value)
