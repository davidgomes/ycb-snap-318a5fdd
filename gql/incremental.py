"""Support for incremental delivery (@defer and @stream directives)."""

import copy
from typing import Any, Dict, List, Optional

from graphql import ExecutionResult


class IncrementalPayload(ExecutionResult):
    """Raw payload received from a transport which may contain
    incremental delivery information (``incremental`` and ``hasNext`` keys)."""

    def __init__(
        self,
        data: Optional[Dict[str, Any]] = None,
        errors: Optional[List[Any]] = None,
        extensions: Optional[Dict[str, Any]] = None,
        incremental: Optional[List[Dict[str, Any]]] = None,
        has_next: bool = False,
    ):
        super().__init__(data=data, errors=errors, extensions=extensions)
        self.incremental = incremental
        self.has_next = has_next

    @classmethod
    def from_dict(cls, payload: Dict[str, Any]) -> "IncrementalPayload":
        return cls(
            data=payload.get("data"),
            errors=payload.get("errors"),
            extensions=payload.get("extensions"),
            incremental=payload.get("incremental"),
            has_next=bool(payload.get("hasNext", False)),
        )


def is_incremental_payload(payload: Dict[str, Any]) -> bool:
    return "incremental" in payload or "hasNext" in payload


class IncrementalResult:
    """Result yielded by
    :meth:`execute_incremental <gql.client.AsyncClientSession.execute_incremental>`.

    ``data`` is the accumulated data from all payloads received so far,
    ``errors`` and ``extensions`` are those of the current payload."""

    def __init__(
        self,
        data: Optional[Dict[str, Any]],
        has_next: bool,
        errors: Optional[List[Any]] = None,
        extensions: Optional[Dict[str, Any]] = None,
    ):
        self.data = data
        self.has_next = has_next
        self.errors = errors
        self.extensions = extensions

    def __repr__(self) -> str:
        return (
            f"<IncrementalResult data={self.data!r} has_next={self.has_next} "
            f"errors={self.errors!r} extensions={self.extensions!r}>"
        )


def _navigate(data: Any, path: List[Any]) -> Any:
    target = data
    for key in path:
        if target is None:
            return None
        if isinstance(key, int):
            if not isinstance(target, list) or key >= len(target):
                return None
        elif not isinstance(target, dict):
            return None
        else:
            if key not in target:
                target[key] = {}
        target = target[key]
    return target


def _deep_merge(target: Dict[str, Any], source: Dict[str, Any]) -> None:
    for key, value in source.items():
        existing = target.get(key)
        if isinstance(existing, dict) and isinstance(value, dict):
            _deep_merge(existing, value)
        else:
            target[key] = copy.deepcopy(value)


class IncrementalAccumulator:
    """Accumulates the data of incremental payloads."""

    def __init__(self) -> None:
        self.data: Optional[Dict[str, Any]] = None

    def apply_item(self, item: Dict[str, Any]) -> List[Any]:
        """Apply one incremental item and return its errors."""
        errors = list(item.get("errors") or [])
        path = list(item.get("path") or [])

        if self.data is None:
            self.data = {}

        if "items" in item:
            items = item.get("items") or []
            if not path or not isinstance(path[-1], int):
                return errors
            index = path[-1]
            parent = _navigate(self.data, path[:-1])
            if isinstance(parent, list):
                for offset, value in enumerate(items):
                    pos = index + offset
                    if pos < len(parent):
                        parent[pos] = copy.deepcopy(value)
                    else:
                        parent.append(copy.deepcopy(value))
        elif "data" in item:
            item_data = item.get("data")
            if item_data is None:
                return errors
            target = _navigate(self.data, path)
            if isinstance(target, dict):
                _deep_merge(target, item_data)

        return errors

    def apply(self, payload: ExecutionResult) -> IncrementalResult:
        errors: List[Any] = list(payload.errors or [])

        if payload.data is not None:
            if self.data is None:
                self.data = copy.deepcopy(payload.data)
            else:
                _deep_merge(self.data, payload.data)

        incremental = getattr(payload, "incremental", None) or []
        for item in incremental:
            errors.extend(self.apply_item(item))

        return IncrementalResult(
            data=copy.deepcopy(self.data),
            has_next=bool(getattr(payload, "has_next", False)),
            errors=errors or None,
            extensions=payload.extensions,
        )
