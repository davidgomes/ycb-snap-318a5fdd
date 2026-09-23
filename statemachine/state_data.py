"""Per-state data declarations and the scoped view injected into callbacks."""

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

_UNSET = object()


class DataVar:
    """Declaration of one variable owned by a :class:`~statemachine.state.State`.

    A :class:`DataVar` replaces a plain default in a state's ``data`` mapping when
    the variable needs a type check, a factory, or an explicit default that is
    itself callable.

    Args:
        default: Value copied on each entry. Mutually exclusive with ``factory``.
        factory: Zero-argument callable invoked on each entry to build a fresh value.
        type: Optional type (or tuple of types) enforced for the default and for
            later assignments.

    Raises:
        InvalidDefinition: When both ``default`` and ``factory`` are given, or when
            ``default`` does not match ``type``.
    """

    def __init__(
        self,
        default: Any = _UNSET,
        *,
        factory: Callable[[], Any] | None = None,
        type: Any = None,
    ) -> None:
        if default is not _UNSET and factory is not None:
            raise InvalidDefinition(_("DataVar cannot specify both default and factory."))
        self.factory = factory
        self.type = type
        self._has_default = default is not _UNSET
        self.default = None if default is _UNSET else default
        if self._has_default:
            self.check_type(self.default, key=None)

    def check_type(self, value: Any, *, key: str | None = None) -> None:
        """Raise :class:`InvalidDefinition` when ``value`` is not an instance of ``type``."""
        expected = self.type
        if expected is None or isinstance(value, expected):
            return
        if key is None:
            raise InvalidDefinition(
                _("Value {value!r} does not match type {expected!r}.").format(
                    value=value, expected=expected
                )
            )
        raise InvalidDefinition(
            _("Value {value!r} for data key {key!r} does not match type {expected!r}.").format(
                value=value, key=key, expected=expected
            )
        )

    def produce(self) -> Any:
        """Return a fresh value for one state entry."""
        if self.factory is not None:
            value = self.factory()
        elif self._has_default:
            value = copy.deepcopy(self.default)
        else:
            value = None
        self.check_type(value, key=None)
        return value


def normalize_data_decl(data: Any) -> Dict[str, DataVar]:
    """Validate a ``State(data=...)`` mapping and wrap every entry as a :class:`DataVar`.

    Plain callables are treated as factories. Non-callables are copied as defaults.
    """
    if data is None:
        return {}
    if not isinstance(data, dict):
        raise InvalidDefinition(_("State data must be a dict mapping strings to default values."))
    normalized: Dict[str, DataVar] = {}
    for key, value in data.items():
        if not isinstance(key, str):
            raise InvalidDefinition(
                _("State data keys must be strings, got {key!r}.").format(key=key)
            )
        if isinstance(value, DataVar):
            normalized[key] = value
        elif callable(value):
            normalized[key] = DataVar(factory=value)
        else:
            normalized[key] = DataVar(default=value)
    return normalized


@dataclass(frozen=True)
class DataChangeInfo:
    """One assignment to active state data recorded during the current macrostep."""

    state_id: str
    key: str
    old_value: Any
    new_value: Any


class ObservedStateData(dict):
    """Live data mapping for one active state.

    Assignments validate declared keys and :class:`DataVar` types, and append a
    :class:`DataChangeInfo` on the owning machine when the value changes.
    """

    def __init__(self, state_id: str, decl: Mapping[str, DataVar], machine: Any) -> None:
        super().__init__()
        self._state_id = state_id
        self._decl = decl
        self._machine = machine
        self._recording = True

    def __setitem__(self, key: str, value: Any) -> None:
        spec = self._decl.get(key)
        if spec is None:
            raise InvalidDefinition(
                _("Data key {key!r} is not declared on state {state!r}.").format(
                    key=key, state=self._state_id
                )
            )
        spec.check_type(value, key=key)
        had_old = key in self
        old = self[key] if had_old else _UNSET
        super().__setitem__(key, value)
        if not self._recording:
            return
        if had_old and old == value:
            return
        old_value = None if old is _UNSET else old
        self._machine._record_data_change(self._state_id, key, old_value, value)

    def __reduce__(self):
        # Persist as a plain dict so the machine reference is not pickled.
        return (dict, (dict(self),))

    def __deepcopy__(self, memo: dict) -> dict:
        return copy.deepcopy(dict(self), memo)


class ScopedStateData(MutableMapping):
    """Hierarchical view of active state data for one callback.

    The state being entered, exited, or transitioned is the first layer. Ancestor
    data is visible behind it, and a child's key hides the same key on a parent.
    Parallel siblings are not ancestors, so their data is not included.
    """

    def __init__(self, machine: Any, state: Any) -> None:
        self._machine = machine
        self._state = state

    def _layers(self) -> List[ObservedStateData]:
        from .state import InstanceState

        layers: List[ObservedStateData] = []
        current = self._state
        if isinstance(current, InstanceState):
            current = current._state
        while current is not None:
            data = self._machine._state_data.get(current.id)
            if data is not None:
                layers.append(data)
            parent = current.parent
            if isinstance(parent, InstanceState):
                parent = parent._state
            current = parent
        return layers

    def __getitem__(self, key: str) -> Any:
        for layer in self._layers():
            if key in layer:
                return layer[key]
        raise KeyError(key)

    def __setitem__(self, key: str, value: Any) -> None:
        for layer in self._layers():
            if key in layer:
                layer[key] = value
                return
        state_id = getattr(self._state, "id", None)
        raise InvalidDefinition(
            _("Data key {key!r} is not declared in the active scope of {state!r}.").format(
                key=key, state=state_id
            )
        )

    def __delitem__(self, key: str) -> None:
        for layer in self._layers():
            if key in layer:
                del layer[key]
                return
        raise KeyError(key)

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

    def copy(self) -> dict:
        """Return a shallow snapshot of the merged scope."""
        return self._merged()

    def _merged(self) -> dict:
        merged: dict = {}
        for layer in reversed(self._layers()):
            merged.update(layer)
        return merged
