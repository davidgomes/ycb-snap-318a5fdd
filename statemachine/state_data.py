"""State-scoped data: declarations, per-instance storage and hierarchical scoping."""

import copy
from dataclasses import dataclass
from itertools import chain
from typing import TYPE_CHECKING
from typing import Any
from typing import Callable
from typing import Dict
from typing import Iterator
from typing import List
from typing import MutableMapping
from typing import Tuple

from .exceptions import InvalidDefinition
from .i18n import _

if TYPE_CHECKING:
    from .state import State


class _Missing:
    def __repr__(self):
        return "MISSING"


MISSING: Any = _Missing()


class DataVar:
    """Declares a state data variable with an optional type and default or factory.

    Args:
        default: The default value. A fresh (deep) copy is used on each state entry.
        type: Optional type (or tuple of types) enforced on assignment.
        factory: Optional callable producing a fresh value on each state entry.
            Cannot be combined with ``default``.

    >>> DataVar(0, type=int).create()
    0
    >>> DataVar(factory=list).create()
    []
    """

    __slots__ = ("default", "type", "factory")

    def __init__(
        self,
        default: Any = MISSING,
        *,
        type: "type | Tuple[type, ...] | None" = None,
        factory: "Callable[[], Any] | None" = None,
    ):
        if default is not MISSING and factory is not None:
            raise InvalidDefinition(_("DataVar cannot declare both 'default' and 'factory'."))
        if factory is not None and not callable(factory):
            raise InvalidDefinition(_("DataVar 'factory' must be callable."))
        self.default = default
        self.type = type
        self.factory = factory
        if default is not MISSING:
            self.validate("default", default)

    def __repr__(self):
        parts = []
        if self.default is not MISSING:
            parts.append(f"default={self.default!r}")
        if self.factory is not None:
            parts.append(f"factory={_callable_name(self.factory)}")
        if self.type is not None:
            parts.append(f"type={_type_name(self.type)}")
        return f"DataVar({', '.join(parts)})"

    def __eq__(self, other):
        return (
            isinstance(other, DataVar)
            and self.default == other.default
            and self.type == other.type
            and self.factory == other.factory
        )

    __hash__ = None  # type: ignore[assignment]

    def create(self) -> Any:
        """Return a fresh initial value for this variable."""
        if self.factory is not None:
            value = self.factory()
            self.validate("factory result", value)
            return value
        if self.default is MISSING:
            return None
        return copy.deepcopy(self.default)

    def validate(self, key: str, value: Any):
        """Raise :ref:`InvalidDefinition` if ``value`` does not match the declared type."""
        if self.type is not None and not isinstance(value, self.type):
            raise InvalidDefinition(
                _("Invalid value {!r} for '{}': expected {}, got {}.").format(
                    value, key, _type_name(self.type), type(value).__name__
                )
            )

    def describe(self, name: str) -> str:
        """Human-readable declaration, used by diagrams (e.g. ``count: int = 0``)."""
        annotation = f": {_type_name(self.type)}" if self.type is not None else ""
        if self.factory is not None:
            return f"{name}{annotation} = {_callable_name(self.factory)}()"
        if self.default is MISSING:
            return f"{name}{annotation}"
        return f"{name}{annotation} = {self.default!r}"


def _type_name(type_: Any) -> str:
    if isinstance(type_, tuple):
        return " | ".join(_type_name(t) for t in type_)
    return getattr(type_, "__name__", repr(type_))


def _callable_name(func: Callable) -> str:
    return getattr(func, "__name__", repr(func))


@dataclass(frozen=True)
class DataChangeInfo:
    """A record of a state data assignment made during the current macrostep."""

    state_id: str
    key: str
    old_value: Any
    new_value: Any


def normalize_data(data: Any) -> "Dict[str, DataVar] | None":
    """Validate a ``data`` declaration and convert every value into a :class:`DataVar`.

    Plain callables are treated as factories; any other value is used as a default.
    """
    if data is None:
        return None
    if not isinstance(data, dict):
        raise InvalidDefinition(
            _("State 'data' must be a dict, got {}.").format(type(data).__name__)
        )
    spec: Dict[str, DataVar] = {}
    for key, value in data.items():
        if not isinstance(key, str):
            raise InvalidDefinition(_("State 'data' keys must be strings, got {!r}.").format(key))
        if isinstance(value, DataVar):
            spec[key] = value
        elif callable(value):
            spec[key] = DataVar(factory=value)
        else:
            spec[key] = DataVar(default=value)
    return spec


class StateDataStore:
    """Per-machine storage of the data owned by each active state."""

    def __init__(self):
        self.active: Dict[str, Dict[str, Any]] = {}
        self.history: Dict[str, Dict[str, Dict[str, Any]]] = {}
        self.changes: List[DataChangeInfo] = []

    def enter(self, state: "State", restored: "Dict[str, Any] | None" = None):
        spec = state.data_spec
        if spec is None:
            return
        if restored is not None:
            self.active[state.id] = dict(restored)
        else:
            self.active[state.id] = {key: var.create() for key, var in spec.items()}

    def exit(self, state: "State") -> "Dict[str, Any] | None":
        return self.active.pop(state.id, None)

    def get(self, state: "State") -> "Dict[str, Any] | None":
        return self.active.get(state.id)

    def set(self, state: "State", key: str, value: Any):
        spec = state.data_spec
        if spec is None or key not in spec:
            raise InvalidDefinition(
                _("State '{}' does not declare a data key '{}'.").format(state.id, key)
            )
        data = self.active.get(state.id)
        if data is None:
            raise InvalidDefinition(_("State '{}' is not active.").format(state.id))
        spec[key].validate(key, value)
        old_value = data[key]
        data[key] = value
        self.changes.append(DataChangeInfo(state.id, key, old_value, value))

    def save_history(self, history_id: str, snapshot: Dict[str, Dict[str, Any]]):
        if snapshot:
            self.history[history_id] = snapshot
        else:
            self.history.pop(history_id, None)

    def history_snapshot(self, history_id: str) -> Dict[str, Dict[str, Any]]:
        return self.history.get(history_id, {})

    def snapshot(self) -> Dict[str, Dict[str, Any]]:
        return {state_id: dict(data) for state_id, data in self.active.items()}

    def clear_changes(self):
        self.changes.clear()

    def scope(self, state: "State | None") -> "ScopedStateData":
        return ScopedStateData(self, state)


class ScopedStateData(MutableMapping):
    """Live view over the data of a state merged with the data of its ancestors.

    Lookups resolve from the innermost state outwards, so a child shadows its
    ancestors on key collisions. Parallel siblings are never part of the chain.
    Assignments are routed to the nearest active state that declares the key.
    """

    __slots__ = ("_store", "_state")

    def __init__(self, store: StateDataStore, state: "State | None"):
        self._store = store
        self._state = state

    def _scopes(self) -> Iterator[Tuple["State", Dict[str, Any]]]:
        state = self._state
        if state is None:
            return
        active = self._store.active
        for s in chain([state], state.ancestors()):
            data = active.get(s.id)
            if data is not None:
                yield s, data

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

    def __setitem__(self, key: str, value: Any):
        for state, data in self._scopes():
            if key in data:
                self._store.set(state, key, value)
                return
        raise InvalidDefinition(
            _("No active state in scope declares a data key '{}'.").format(key)
        )

    def __delitem__(self, key: str):
        raise InvalidDefinition(_("State data keys cannot be deleted: '{}'.").format(key))

    def __iter__(self) -> Iterator[str]:
        return iter(self._merged())

    def __len__(self) -> int:
        return len(self._merged())

    def __contains__(self, key: object) -> bool:
        return any(key in data for _state, data in self._scopes())

    def __repr__(self):
        return repr(self._merged())
