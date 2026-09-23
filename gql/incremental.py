"""Support for the incremental delivery of results (@defer and @stream directives).

The payloads follow the incremental delivery format used by
``multipart/mixed;deferSpec=20220824``::

    {"data": {...}, "hasNext": true}
    {"incremental": [{"data": {...}, "path": [...]}], "hasNext": true}
    {"incremental": [{"items": [...], "path": [..., 3]}], "hasNext": false}
"""

import copy
import logging
from typing import Any, Dict, List, Optional, Sequence, Union

from graphql import ExecutionResult

log = logging.getLogger(__name__)

Path = Sequence[Union[str, int]]

INCREMENTAL_KEYS = ("incremental", "hasNext")


class IncrementalPayloadExecutionResult(ExecutionResult):
    """ExecutionResult keeping the raw payload received from the server,
    used by transports to forward incremental delivery payloads."""

    __slots__ = ("payload",)

    def __init__(self, payload: Dict[str, Any]):
        super().__init__(
            data=payload.get("data"),
            errors=payload.get("errors"),
            extensions=payload.get("extensions"),
        )
        self.payload = payload


def is_incremental_payload(payload: Dict[str, Any]) -> bool:
    return any(key in payload for key in INCREMENTAL_KEYS)


def execution_result_to_payload(result: ExecutionResult) -> Dict[str, Any]:
    if isinstance(result, IncrementalPayloadExecutionResult):
        return result.payload

    payload: Dict[str, Any] = {"data": result.data}
    if result.errors is not None:
        payload["errors"] = result.errors
    if result.extensions is not None:
        payload["extensions"] = result.extensions
    return payload


class IncrementalResult:
    """Result yielded by :meth:`execute_incremental
    <gql.client.AsyncClientSession.execute_incremental>`.

    :ivar data: the data accumulated from all the payloads received so far
    :ivar errors: the errors received in this payload
        (including the errors of its incremental items)
    :ivar extensions: the extensions received in this payload
    :ivar has_next: True if the server will send more payloads
    """

    __slots__ = ("data", "errors", "extensions", "has_next")

    def __init__(
        self,
        data: Optional[Dict[str, Any]] = None,
        errors: Optional[List[Any]] = None,
        extensions: Optional[Dict[str, Any]] = None,
        has_next: bool = False,
    ):
        self.data = data
        self.errors = errors
        self.extensions = extensions
        self.has_next = has_next

    def __repr__(self) -> str:
        return (
            f"{self.__class__.__name__}(data={self.data!r}, errors={self.errors!r}, "
            f"extensions={self.extensions!r}, has_next={self.has_next!r})"
        )

    def __eq__(self, other: Any) -> bool:
        if not isinstance(other, IncrementalResult):
            return NotImplemented
        return (
            self.data == other.data
            and self.errors == other.errors
            and self.extensions == other.extensions
            and self.has_next == other.has_next
        )


def _deep_merge(target: Dict[str, Any], source: Dict[str, Any]) -> None:
    for key, value in source.items():
        existing = target.get(key)
        if isinstance(existing, dict) and isinstance(value, dict):
            _deep_merge(existing, value)
        else:
            target[key] = copy.deepcopy(value)


def _navigate(data: Any, path: Path, create: bool) -> Any:
    """Return the element of data at the given path.

    Missing object keys are created if create is True.
    Raises LookupError if the path cannot be followed.
    """
    current = data
    for key in path:
        if isinstance(key, int):
            if not isinstance(current, list) or not -len(current) <= key < len(
                current
            ):
                raise LookupError(f"Cannot access index {key}")
            current = current[key]
        else:
            if not isinstance(current, dict):
                raise LookupError(f"Cannot access key {key!r}")
            if key not in current:
                if not create:
                    raise LookupError(f"Missing key {key!r}")
                current[key] = {}
            current = current[key]
    return current


class IncrementalResultAccumulator:
    """Accumulates the incremental delivery payloads into a single data dict."""

    def __init__(self) -> None:
        self.data: Optional[Dict[str, Any]] = None

    def _root(self) -> Dict[str, Any]:
        if self.data is None:
            self.data = {}
        return self.data

    def _apply_defer(self, path: Path, item_data: Any) -> None:
        if item_data is None:
            return

        if len(path) == 0:
            _deep_merge(self._root(), item_data)
            return

        target = _navigate(self._root(), path, create=True)

        if not isinstance(target, dict):
            raise LookupError(f"Cannot merge deferred data at path {list(path)}")

        _deep_merge(target, item_data)

    def _apply_stream(self, path: Path, items: Any) -> None:
        if not items:
            return

        if len(path) == 0 or not isinstance(path[-1], int):
            raise LookupError(f"Invalid stream path {list(path)}")

        parent = _navigate(self._root(), path[:-1], create=False)

        if not isinstance(parent, list):
            raise LookupError(f"Cannot stream items at path {list(path)}")

        start = path[-1]
        if start > len(parent):
            parent.extend([None] * (start - len(parent)))

        parent[start : start + len(items)] = copy.deepcopy(list(items))

    def add_payload(self, payload: Dict[str, Any]) -> IncrementalResult:
        """Apply a received payload and return the corresponding result."""

        errors: List[Any] = list(payload.get("errors") or [])

        if "data" in payload:
            payload_data = payload["data"]
            if self.data is None or payload_data is None:
                self.data = copy.deepcopy(payload_data)
            else:
                _deep_merge(self.data, payload_data)

        for item in payload.get("incremental") or []:
            errors.extend(item.get("errors") or [])

            path = item.get("path") or []

            try:
                if "items" in item:
                    self._apply_stream(path, item["items"])
                elif "data" in item:
                    self._apply_defer(path, item["data"])
            except LookupError as e:
                log.warning(f"Ignoring incremental item {item!r}: {e}")

        return IncrementalResult(
            data=copy.deepcopy(self.data),
            errors=errors or None,
            extensions=payload.get("extensions"),
            has_next=bool(payload.get("hasNext", False)),
        )
