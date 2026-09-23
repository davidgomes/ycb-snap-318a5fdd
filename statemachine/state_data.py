"""State-owned data: declarations, per-instance storage and scoped access.

A :ref:`State` may declare ``data``, a mapping of variable names to default values.
The data lives only while the state is active: it is initialized when the state is
entered, removed when the state is exited, and reset to the defaults on every
re-entry. Values are stored per state machine instance, never on the shared
:ref:`State` declaration.
"""

from copy import deepcopy
from dataclasses import dataclass
from typing import TYPE_CHECKING
from typing import Any
from typing import Callable
from typing import Dict
from typing import Iterable
from typing import Iterator
from typing import List
from typing import Mapping
from typing import MutableMapping

from .exceptions import InvalidDefinition
from .i18n import _

if TYPE_CHECKING:
    from .state import State

_MISSING: Any = object()


class DataVar:
    """Declares a state data variable with optional type enforcement.

    Args:
        default: The value assigned when the owning state is entered. Each entry
            receives a fresh (deep) copy of it. Defaults to ``None``.
        type: An optional type (or tuple of types). Values assigned through
            :meth:`StateChart.set_state_data` or the ``state_data`` callback
            parameter must be instances of it.
        factory: An optional callable that produces the initial value on every
            entry. Cannot be combined with ``default``.

    >>> DataVar(0, type=int)
    DataVar(default=0, type=int)

    >>> DataVar(factory=list)
    DataVar(factory=list)

    >>> DataVar(1, factory=list)
    Traceback (most recent call last):
    ...
    statemachine.exceptions.InvalidDefinition: DataVar accepts either 'default' or 'factory', not both.

    """

    __slots__ = ("default", "type", "factory")

    def __init__(
        self,
        default: Any = _MISSING,
        *,
        type: Any = None,
        factory: "Callable[[], Any] | None" = None,
    ):
        if default is not _MISSING and factory is not None:
            raise InvalidDefinition(
                _("DataVar accepts either 'default' or 'factory', not both.")
            )
        if factory is not None and not callable(factory):
            raise InvalidDefinition(_("DataVar 'factory' must be callable, got {!r}.").format(factory))

        self.default: Any = None if default is _MISSING else default
        self.type = type
        self.factory = factory
        if default is not _MISSING:
            self.validate(default)

    def __repr__(self):
        parts = []
        if self.factory is not None:
            parts.append(f"factory={_callable_name(self.factory)}")
        else:
            parts.append(f"default={self.default!r}")
        if self.type is not None:
            parts.append(f"type={_type_name(self.type)}")
        return f"{type(self).__name__}({', '.join(parts)})"

    def describe(self, key: str) -> str:
        """Human-readable declaration, used by diagrams (e.g. ``count: int = 0``)."""
        annotation = f": {_type_name(self.type)}" if self.type is not None else ""
        if self.factory is not None:
            initial = f"{_callable_name(self.factory)}()"
        else:
            initial = repr(self.default)
        return f"{key}{annotation} = {initial}"

    def validate(self, value: Any, key: str = "") -> None:
        """Raise :class:`InvalidDefinition` if ``value`` violates the declared ``type``."""
        if self.type is None or isinstance(value, self.type):
            return
        target = f" {key!r}" if key else ""
        raise InvalidDefinition(
            _("Data variable{} expects a value of type {}, got {!r}.").format(
                target, _type_name(self.type), value
            )
        )

    def new_value(self, key: str = "") -> Any:
        """Produce a fresh initial value for a new state entry."""
        if self.factory is None:
            return deepcopy(self.default)
        value = self.factory()
        self.validate(value, key)
        return value


def _type_name(type_: Any) -> str:
    if isinstance(type_, tuple):
        return " | ".join(_type_name(t) for t in type_)
    return getattr(type_, "__name__", repr(type_))


def _callable_name(func: Callable) -> str:
    return getattr(func, "__name__", repr(func))


def normalize_data(data: Any) -> Dict[str, DataVar]:
    """Convert a ``data`` declaration into a mapping of :class:`DataVar`.

    Plain values become defaults, plain callables become factories and
    :class:`DataVar` instances are kept as is.

    >>> normalize_data({"count": 0, "items": list})
    {'count': DataVar(default=0), 'items': DataVar(factory=list)}

    >>> normalize_data(["count"])
    Traceback (most recent call last):
    ...
    statemachine.exceptions.InvalidDefinition: State 'data' must be a dict mapping string keys to default values, got ['count'].

    """
    if data is None:
        return {}
    if not isinstance(data, Mapping):
        raise InvalidDefinition(
            _("State 'data' must be a dict mapping string keys to default values, got {!r}.").format(
                data
            )
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
            result[key] = DataVar(default=value)
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
    """Per-instance storage of the data owned by the active states.

    Only plain containers keyed by state id are kept, so the store survives pickling.
    """

    def __init__(self):
        self._active: Dict[str, Dict[str, Any]] = {}
        self._history: Dict[str, Dict[str, Dict[str, Any]]] = {}
        self._changes: List[DataChangeInfo] = []

    def get(self, state_id: str) -> "Dict[str, Any] | None":
        return self._active.get(state_id)

    @property
    def values(self) -> Dict[str, Dict[str, Any]]:
        return {state_id: dict(data) for state_id, data in self._active.items()}

    def enter(self, state: "State", snapshot: "Dict[str, Any] | None" = None) -> None:
        """Initialize the data of ``state``, from ``snapshot`` when recalled by history."""
        declarations = state.data
        if not declarations:
            return
        if snapshot is not None:
            self._active[state.id] = dict(snapshot)
        elif state.id not in self._active:
            # A state entered again without being exited (e.g. an internal
            # self-transition) keeps its data.
            self._active[state.id] = {
                key: var.new_value(key) for key, var in declarations.items()
            }

    def exit(self, state: "State") -> None:
        self._active.pop(state.id, None)

    def ensure(self, states: Iterable["State"]) -> None:
        """Initialize data for active states that were never entered by the engine.

        This happens when the configuration is restored from a model.
        """
        for state in states:
            self.enter(state)

    def set(self, state: "State", key: str, value: Any) -> None:
        """Assign ``value`` to an active, declared variable, recording the change."""
        state.data[key].validate(value, key)
        data = self._active[state.id]
        old_value = data.get(key)
        data[key] = value
        self._changes.append(DataChangeInfo(state.id, key, old_value, value))

    def save_history(self, history_id: str, states: Iterable["State"]) -> None:
        # Keep references to the live dicts: callbacks running on exit still update them.
        self._history[history_id] = {
            state.id: self._active[state.id] for state in states if state.id in self._active
        }

    def history_snapshot(self, history_id: str) -> Dict[str, Dict[str, Any]]:
        return self._history.get(history_id, {})

    def checkpoint(self) -> Dict[str, Dict[str, Any]]:
        return dict(self._active)

    def rollback(self, checkpoint: Dict[str, Dict[str, Any]]) -> None:
        self._active = checkpoint

    @property
    def changes(self) -> List[DataChangeInfo]:
        return list(self._changes)

    def clear_changes(self) -> None:
        self._changes.clear()

    def scope(self, state: "State | None") -> "ScopedStateData":
        return ScopedStateData(self, state)


class ScopedStateData(MutableMapping):
    """Mapping view of the data visible from a state.

    Merges the data of the state and its active ancestors; on key collisions the
    innermost state shadows its ancestors. Parallel regions are isolated because
    sibling regions are never ancestors of each other. Assignments are routed to
    the innermost active state that declares the key.
    """

    __slots__ = ("_store", "_state")

    def __init__(self, store: StateDataStore, state: "State | None"):
        self._store = store
        self._state = state

    def _chain(self) -> Iterator["tuple[State, Dict[str, Any]]"]:
        state = self._state
        while state is not None:
            data = self._store.get(state.id)
            if data is not None:
                yield state, data
            state = state.parent

    def __getitem__(self, key: str) -> Any:
        for _state, data in self._chain():
            if key in data:
                return data[key]
        raise KeyError(key)

    def __setitem__(self, key: str, value: Any) -> None:
        for state, data in self._chain():
            if key in data:
                self._store.set(state, key, value)
                return
        raise InvalidDefinition(
            _("Data variable {!r} is not declared on any active state in scope.").format(key)
        )

    def __delitem__(self, key: str) -> None:
        raise InvalidDefinition(_("Data variables cannot be deleted: {!r}.").format(key))

    def __iter__(self) -> Iterator[str]:
        return iter(self._merged())

    def __len__(self) -> int:
        return len(self._merged())

    def __contains__(self, key: object) -> bool:
        return any(key in data for _state, data in self._chain())

    def _merged(self) -> Dict[str, Any]:
        merged: Dict[str, Any] = {}
        for _state, data in reversed(list(self._chain())):
            merged.update(data)
        return merged

    def __repr__(self):
        return repr(self._merged())
