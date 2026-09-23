"""Support for the incremental delivery of results
using the :code:`@defer` and :code:`@stream` directives.

The server first sends an initial payload containing the non-deferred data,
followed by subsequent payloads containing an :code:`incremental` list
with the deferred data (:code:`data` key) or the streamed list items
(:code:`items` key), each with the :code:`path` where it should be merged.
"""

import copy
import logging
from typing import Any, Dict, List, Mapping, Optional, Union

from graphql import ExecutionResult

log = logging.getLogger(__name__)

Path = List[Union[str, int]]


def is_incremental_payload(payload: Mapping[str, Any]) -> bool:
    """Check if a payload received from the server is part of an
    incremental delivery (contains the :code:`incremental` or :code:`hasNext` keys).
    """
    return "incremental" in payload or "hasNext" in payload


class IncrementalExecutionResult(ExecutionResult):
    """ExecutionResult for requests using the :code:`@defer` or :code:`@stream`
    directives.

    At the transport level, it represents a single payload received
    from the server.

    When yielded by :meth:`execute_incremental
    <gql.client.AsyncClientSession.execute_incremental>`:

    - :code:`data` contains the data accumulated from all the payloads
      received so far
    - :code:`errors` contains the errors of this payload,
      including the errors of its incremental items
    - :code:`extensions` contains the extensions of this payload
    - :code:`has_next` indicates if more payloads are expected
    - :code:`incremental` contains the raw incremental items of this payload
    """

    __slots__ = ("has_next", "incremental")

    def __init__(
        self,
        data: Optional[Dict[str, Any]] = None,
        errors: Optional[List[Any]] = None,
        extensions: Optional[Dict[str, Any]] = None,
        has_next: bool = False,
        incremental: Optional[List[Dict[str, Any]]] = None,
    ) -> None:
        super().__init__(data=data, errors=errors, extensions=extensions)
        self.has_next = has_next
        self.incremental = incremental

    @classmethod
    def from_payload(cls, payload: Mapping[str, Any]) -> "IncrementalExecutionResult":
        """Create an instance from a payload received from the server."""
        return cls(
            data=payload.get("data"),
            errors=payload.get("errors"),
            extensions=payload.get("extensions"),
            has_next=bool(payload.get("hasNext", False)),
            incremental=payload.get("incremental"),
        )

    def __repr__(self) -> str:
        ext = "" if self.extensions is None else f", extensions={self.extensions!r}"
        return (
            f"{self.__class__.__name__}(data={self.data!r}, errors={self.errors!r}"
            f"{ext}, has_next={self.has_next!r})"
        )

    def __eq__(self, other: object) -> bool:
        if isinstance(other, dict):
            return (
                super().__eq__(other)
                and other.get("hasNext", self.has_next) == self.has_next
            )
        if isinstance(other, IncrementalExecutionResult):
            return super().__eq__(other) and other.has_next == self.has_next
        return super().__eq__(other)


def _merge_values(existing: Any, new: Any) -> Any:
    if isinstance(existing, dict) and isinstance(new, dict):
        _merge_objects(existing, new)
        return existing

    if isinstance(existing, list) and isinstance(new, list):
        if len(existing) == len(new):
            for index, value in enumerate(new):
                existing[index] = _merge_values(existing[index], value)
            return existing

    return copy.deepcopy(new)


def _merge_objects(target: Dict[str, Any], source: Dict[str, Any]) -> None:
    for key, value in source.items():
        if key in target:
            target[key] = _merge_values(target[key], value)
        else:
            target[key] = copy.deepcopy(value)


def _is_index(key: Any) -> bool:
    return isinstance(key, int) and not isinstance(key, bool)


class IncrementalResultAccumulator:
    """Merge the payloads of an incremental delivery into a single result.

    Each call to :meth:`add` with a payload received from the transport returns an
    :class:`IncrementalExecutionResult` containing a snapshot of the data
    accumulated so far.
    """

    def __init__(self) -> None:
        self.data: Optional[Dict[str, Any]] = None

    def add(self, result: ExecutionResult) -> IncrementalExecutionResult:
        """Merge a payload received from the transport.

        Payloads without incremental delivery information (for servers not
        supporting :code:`@defer` and :code:`@stream`) are accepted and considered
        to be the last payload.
        """
        errors: List[Any] = list(result.errors or [])

        if result.data is not None:
            if self.data is None:
                self.data = copy.deepcopy(result.data)
            else:
                self._merge_data([], result.data)

        incremental = getattr(result, "incremental", None)

        if isinstance(incremental, list):
            for item in incremental:
                if not isinstance(item, Mapping):
                    log.warning(f"Ignoring invalid incremental item: {item!r}")
                    continue

                item_errors = item.get("errors")
                if item_errors:
                    errors.extend(item_errors)

                path = item.get("path")
                if path is None:
                    path = []
                elif not isinstance(path, list):
                    log.warning(
                        f"Ignoring incremental item with invalid path: {path!r}"
                    )
                    continue

                if "items" in item:
                    self._merge_items(path, item["items"])
                else:
                    self._merge_data(path, item.get("data"))

        elif incremental is not None:
            log.warning(f"Ignoring invalid incremental field: {incremental!r}")

        return IncrementalExecutionResult(
            data=copy.deepcopy(self.data),
            errors=errors or None,
            extensions=result.extensions,
            has_next=bool(getattr(result, "has_next", False)),
            incremental=incremental,
        )

    def _resolve(self, path: Path) -> Any:
        current: Any = self.data

        for key in path:
            if isinstance(current, dict) and isinstance(key, str):
                current = current.get(key)
            elif isinstance(current, list) and _is_index(key):
                assert isinstance(key, int)
                if not 0 <= key < len(current):
                    return None
                current = current[key]
            else:
                return None

        return current

    def _merge_data(self, path: Path, data: Any) -> None:
        """Merge the data of a deferred fragment into the object at the path."""
        if data is None:
            return

        if not isinstance(data, dict):
            log.warning(f"Ignoring invalid deferred data at path {path!r}: {data!r}")
            return

        if not path and self.data is None:
            self.data = {}

        target = self._resolve(path)

        if not isinstance(target, dict):
            log.warning(f"Unable to merge deferred data: no object at path {path!r}")
            return

        _merge_objects(target, data)

    def _merge_items(self, path: Path, items: Any) -> None:
        """Insert streamed items into the list at the path.

        The last element of the path is the index of the first item in the list.
        """
        if items is None:
            return

        if not isinstance(items, list):
            log.warning(f"Ignoring invalid streamed items at path {path!r}: {items!r}")
            return

        start: Optional[int] = None
        list_path = path

        if path and _is_index(path[-1]):
            start = int(path[-1])
            list_path = path[:-1]

        target = self._resolve(list_path)

        if not isinstance(target, list):
            log.warning(f"Unable to merge streamed items: no list at path {path!r}")
            return

        if start is None:
            start = len(target)

        if start < 0:
            log.warning(f"Ignoring streamed items with invalid path: {path!r}")
            return

        # Items which failed to be delivered are null in the resulting list
        if start > len(target):
            target.extend([None] * (start - len(target)))

        end = start + len(items)
        target[start:end] = copy.deepcopy(items)
