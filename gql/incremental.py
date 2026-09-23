"""Client-side accumulation for GraphQL @defer and @stream payloads."""

import copy
from typing import Any, Dict, List, Optional


class IncrementalResult:
    """One accumulated incremental execution payload.

    ``data`` is the merged result so far, not the raw delta.
    ``extensions`` and ``errors`` come from this payload only.
    """

    def __init__(
        self,
        data: Optional[Any],
        errors: Optional[List[Any]],
        extensions: Optional[Any],
        has_next: bool,
    ) -> None:
        self.data = data
        self.errors = errors
        self.extensions = extensions
        self.has_next = has_next


class IncrementalResultAccumulator:
    """Fold incremental delivery payloads into accumulated results."""

    def __init__(self) -> None:
        self.data: Optional[Any] = None

    def apply(self, payload: Optional[Dict[str, Any]]) -> IncrementalResult:
        if not isinstance(payload, dict):
            payload = {}

        if "data" in payload:
            self.data = _merge_root(self.data, copy.deepcopy(payload.get("data")))

        errors: List[Any] = []
        payload_errors = payload.get("errors")
        if payload_errors:
            errors.extend(payload_errors)

        incremental = payload.get("incremental")
        if isinstance(incremental, list):
            for item in incremental:
                if not isinstance(item, dict):
                    continue
                try:
                    _apply_incremental_item(self, item)
                except Exception:
                    # A bad incremental item must not stop later items or payloads.
                    pass
                item_errors = item.get("errors")
                if item_errors:
                    errors.extend(item_errors)

        return IncrementalResult(
            data=copy.deepcopy(self.data),
            errors=errors or None,
            extensions=payload.get("extensions"),
            has_next=bool(payload.get("hasNext", False)),
        )


def _merge_root(current: Optional[Any], incoming: Optional[Any]) -> Optional[Any]:
    if incoming is None:
        return None if current is None else current
    if current is None:
        return incoming
    if isinstance(current, dict) and isinstance(incoming, dict):
        current.update(incoming)
        return current
    return incoming


def _apply_incremental_item(
    accumulator: IncrementalResultAccumulator, item: Dict[str, Any]
) -> None:
    path = item.get("path", [])
    if path is None:
        path = []
    if not isinstance(path, list):
        return

    if "items" in item:
        _apply_stream(accumulator, path, item.get("items"))
        return

    if "data" in item:
        _apply_defer(accumulator, path, copy.deepcopy(item.get("data")))


def _apply_defer(
    accumulator: IncrementalResultAccumulator, path: List[Any], data: Any
) -> None:
    if not path:
        accumulator.data = _merge_root(accumulator.data, data)
        return

    if accumulator.data is None:
        return

    parent = _navigate(accumulator.data, path)
    if isinstance(parent, dict) and isinstance(data, dict):
        parent.update(data)
        return

    if data is None:
        _assign(accumulator.data, path, None)
        return

    _assign(accumulator.data, path, data)


def _apply_stream(
    accumulator: IncrementalResultAccumulator, path: List[Any], items: Any
) -> None:
    if accumulator.data is None or not isinstance(items, list):
        return

    copied = [copy.deepcopy(item) for item in items]
    if path and isinstance(path[-1], int):
        target = _navigate(accumulator.data, path[:-1])
        index = path[-1]
    else:
        target = _navigate(accumulator.data, path)
        index = len(target) if isinstance(target, list) else 0

    if not isinstance(target, list):
        return

    target[index:index] = copied


def _navigate(root: Any, path: List[Any]) -> Any:
    current = root
    for segment in path:
        if isinstance(segment, int):
            current = current[segment]
        else:
            current = current[segment]
    return current


def _assign(root: Any, path: List[Any], value: Any) -> None:
    parent = _navigate(root, path[:-1]) if len(path) > 1 else root
    key = path[-1]
    if isinstance(parent, dict) or (isinstance(parent, list) and isinstance(key, int)):
        parent[key] = value
