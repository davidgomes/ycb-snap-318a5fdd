"""State-scoped data: declarations, per-instance storage and hierarchical views.

A :ref:`State` may own data declared with the ``data`` keyword. The declarations live on the
(shared) state definition, while the values live on each state machine instance, in a
:class:`StateDataStore`. Values exist only while their owning state is active.
"""

import copy
from collections.abc import Mapping
from collections.abc import MutableMapping
from dataclasses import dataclass
from typing import TYPE_CHECKING
from typing import Any
from typing import Callable
from typing import Dict
from typing import Iterable
from typing import Iterator
from typing import List
from typing import Tuple

from .exceptions import InvalidDefinition
from .i18n import _

if TYPE_CHECKING:
    from .state import State

_MISSING: Any = object()


def _is_type_spec(value: Any) -> bool:
    """Whether ``value`` is accepted as the second argument of :func:`isinstance`."""
    try:
        isinstance(None, value)
    except TypeError:
        return False
    return True


def _type_name(type_spec: Any) -> str:
    if isinstance(type_spec, tuple):
        return " | ".join(_type_name(item) for item in type_spec)
    return getattr(type_spec, "__name__", repr(type_spec))


def _callable_name(func: Callable) -> str:
    return getattr(func, "__name__", repr(func))


class DataVar:
    """Declares a state data variable.

    Args:
        default: Initial value of the variable. Each state entry receives a fresh deep copy,
            so mutable defaults are never shared. Defaults to ``None``.
        type: Optional type (anything accepted by :func:`isinstance`) enforced on the initial
            value and on every assignment made with :meth:`StateChart.set_state_data`.
        factory: Optional zero-argument callable invoked on every state entry to produce the
            initial value. Mutually exclusive with ``default``.

    >>> DataVar(0, type=int)
    DataVar(0, type=int)

    >>> DataVar(factory=list)
    DataVar(factory=list)

    """

    __slots__ = ("_default", "factory", "type")

    def __init__(
        self,
        default: Any = _MISSING,
        *,
        type: Any = None,
        factory: "Callable[[], Any] | None" = None,
    ):
        if default is not _MISSING and factory is not None:
            raise InvalidDefinition(_("DataVar accepts either 'default' or 'factory', not both."))
        if factory is not None and not callable(factory):
            raise InvalidDefinition(
                _("DataVar 'factory' must be callable, got {!r}.").format(factory)
            )
        if type is not None and not _is_type_spec(type):
            raise InvalidDefinition(
                _("DataVar 'type' must be a type or a tuple of types, got {!r}.").format(type)
            )
        self._default = default
        self.factory = factory
        self.type = type
        if default is not _MISSING:
            self.check(default, _("DataVar default"))

    @property
    def default(self) -> Any:
        """The declared default value, or ``None`` if not given."""
        return None if self._default is _MISSING else self._default

    def check(self, value: Any, label: str) -> None:
        """Raise :class:`InvalidDefinition` if ``value`` violates the declared ``type``."""
        if self.type is None or isinstance(value, self.type):
            return
        raise InvalidDefinition(
            _("{} expects {}, got {!r}.").format(label, _type_name(self.type), value)
        )

    def initial_value(self) -> Any:
        """Produce a fresh initial value for a new state entry."""
        if self.factory is not None:
            value = self.factory()
            self.check(value, _("DataVar factory result"))
            return value
        return copy.deepcopy(self.default)

    def describe(self, name: str) -> str:
        """Human-readable declaration, e.g. ``count: int = 0``."""
        annotation = f": {_type_name(self.type)}" if self.type is not None else ""
        if self.factory is not None:
            value = f"{_callable_name(self.factory)}()"
        else:
            value = repr(self.default)
        return f"{name}{annotation} = {value}"

    def __repr__(self) -> str:
        args = []
        if self._default is not _MISSING:
            args.append(repr(self._default))
        if self.factory is not None:
            args.append(f"factory={_callable_name(self.factory)}")
        if self.type is not None:
            args.append(f"type={_type_name(self.type)}")
        return f"{type(self).__name__}({', '.join(args)})"


def normalize_data(data: Any) -> Dict[str, DataVar]:
    """Validate a ``State(data=...)`` declaration and convert every entry to a :class:`DataVar`.

    Plain callables are treated as factories; any other value is a default.
    """
    if data is None:
        return {}
    if not isinstance(data, Mapping):
        raise InvalidDefinition(_("State 'data' must be a dict, got {!r}.").format(data))

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


@dataclass(frozen=True)
class DataChangeInfo:
    """Record of a state data assignment made during the current macrostep."""

    state_id: str
    """The id of the state that owns the variable."""

    key: str
    """The variable name."""

    old_value: Any
    """The value before the assignment."""

    new_value: Any
    """The assigned value."""


class StateDataStore:
    """Per-instance storage of the data owned by the active states, keyed by state id.

    Also keeps the data snapshots recorded for history pseudo-states and the log of
    assignments made during the current macrostep.
    """

    def __init__(self):
        self._active: Dict[str, Dict[str, Any]] = {}
        self._history: Dict[str, Dict[str, Dict[str, Any]]] = {}
        self._capture: Dict[str, List[str]] = {}
        self._changes: List[DataChangeInfo] = []

    def get(self, state_id: str) -> "Dict[str, Any] | None":
        return self._active.get(state_id)

    def snapshot(self) -> Dict[str, Dict[str, Any]]:
        return {state_id: dict(data) for state_id, data in self._active.items()}

    def view(self, state: "State") -> "StateDataView":
        return StateDataView(self, state)

    def activate(self, state: "State", restored: "Dict[str, Any] | None" = None) -> None:
        """Initialize the data of an entered state, from defaults or a history snapshot.

        A state entered without being exited first (internal self-transitions) keeps its data.
        """
        if not state.data or state.id in self._active:
            return
        if restored is not None:
            self._active[state.id] = dict(restored)
        else:
            self._active[state.id] = {key: var.initial_value() for key, var in state.data.items()}

    def activate_all(self, states: "Iterable[State]") -> None:
        """Initialize the data of active states that were not entered through the engine."""
        for state in states:
            self.activate(state)

    def deactivate(self, state_id: str) -> None:
        """Discard the data of an exited state, keeping it on the pending history snapshots."""
        data = self._active.pop(state_id, None)
        if data is None:
            return
        for history_id in self._capture.pop(state_id, ()):
            self._history[history_id][state_id] = data

    def begin_history(self, history_id: str, state_ids: Iterable[str]) -> None:
        """Start a new data snapshot for ``history_id``, filled as ``state_ids`` are exited."""
        self._history[history_id] = {}
        for state_id in state_ids:
            if state_id in self._active:
                self._capture.setdefault(state_id, []).append(history_id)

    def history_data(self, history_ids: Iterable[str]) -> Dict[str, Dict[str, Any]]:
        """Data recorded by the given history pseudo-states, keyed by state id."""
        result: Dict[str, Dict[str, Any]] = {}
        for history_id in history_ids:
            result.update(self._history.get(history_id, {}))
        return result

    def set(self, state: "State", key: str, value: Any) -> None:
        """Assign a declared variable of an active state, recording the change."""
        var = state.data.get(key)
        if var is None:
            raise InvalidDefinition(
                _("State '{}' does not declare the data variable '{}'.").format(state.id, key)
            )
        data = self._active.get(state.id)
        if data is None:
            raise InvalidDefinition(
                _("Cannot set data variable '{}' of the inactive state '{}'.").format(
                    key, state.id
                )
            )
        var.check(value, _("Data variable '{}' of state '{}'").format(key, state.id))
        old_value = data[key]
        data[key] = value
        self._changes.append(DataChangeInfo(state.id, key, old_value, value))

    @property
    def changes(self) -> List[DataChangeInfo]:
        return list(self._changes)

    def clear_changes(self) -> None:
        self._changes.clear()

    def checkpoint(self) -> Dict[str, Dict[str, Any]]:
        return dict(self._active)

    def rollback(self, checkpoint: Dict[str, Dict[str, Any]]) -> None:
        """Restore the active data to a previous :meth:`checkpoint`."""
        self._active.clear()
        self._active.update(checkpoint)
        self._capture.clear()


class StateDataView(MutableMapping):
    """Live view of the data visible from a state.

    Merges the data of the state with the data of its active ancestors; on key collision
    the innermost state wins. Parallel regions don't see each other's data, since only the
    ancestor chain is visible. Assignments are routed to the state that owns the key.
    """

    __slots__ = ("_store", "_state")

    def __init__(self, store: StateDataStore, state: "State"):
        self._store = store
        self._state = state

    def _scopes(self) -> "Iterator[Tuple[State, Dict[str, Any]]]":
        """Active ``(state, data)`` pairs from the innermost state outwards."""
        active = self._store._active
        state: "State | None" = self._state
        while state is not None:
            data = active.get(state.id)
            if data is not None:
                yield state, data
            state = state.parent

    def _merged(self) -> Dict[str, Any]:
        merged: Dict[str, Any] = {}
        for _state, data in reversed(list(self._scopes())):
            merged.update(data)
        return merged

    def __getitem__(self, key: str) -> Any:
        for _state, data in self._scopes():
            if key in data:
                return data[key]
        raise KeyError(key)

    def __setitem__(self, key: str, value: Any) -> None:
        for state, data in self._scopes():
            if key in data:
                self._store.set(state, key, value)
                return
        raise InvalidDefinition(_("There's no active state data variable named '{}'.").format(key))

    def __delitem__(self, key: str) -> None:
        raise InvalidDefinition(_("State data variables cannot be deleted: '{}'.").format(key))

    def __iter__(self) -> Iterator[str]:
        return iter(self._merged())

    def __len__(self) -> int:
        return len(self._merged())

    def __repr__(self) -> str:
        return f"{type(self).__name__}({self._merged()!r})"
