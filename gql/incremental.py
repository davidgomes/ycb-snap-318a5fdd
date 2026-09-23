"""Client-side accumulation of GraphQL incremental (@defer / @stream) payloads.

Wire format follows the incremental-delivery multipart response
(``deferSpec=20220824``): each payload may contain ``data``, ``incremental``,
``hasNext``, ``errors`` and ``extensions``. ``data`` on the yielded result is
the merged document so far. ``extensions`` and ``errors`` belong to the
payload that produced that result.
"""

import copy
from typing import Any, Dict, List, Optional, Union

from graphql import ExecutionResult

_MISSING = object()

_RESULT_KEYS = (
    "data",
    "errors",
    "incremental",
    "hasNext",
    "has_next",
    "pending",
    "completed",
    "items",
)


class IncrementalExecutionResult(ExecutionResult):
    """Execution result that also carries incremental-delivery fields.

    ``data`` is the accumulated GraphQL result (not the raw delta).
    ``has_next`` mirrors the payload ``hasNext`` flag.
    ``errors`` and ``extensions`` come from the payload that produced this
    result. ``incremental`` and ``payload`` keep the raw delta so transports
    can forward a single part without merging it themselves.
    """

    def __init__(
        self,
        data: Optional[Dict[str, Any]] = None,
        errors: Optional[List[Any]] = None,
        extensions: Optional[Dict[str, Any]] = None,
        has_next: bool = False,
        incremental: Optional[List[Any]] = None,
        payload: Optional[Dict[str, Any]] = None,
    ) -> None:
        super().__init__(data=data, errors=errors, extensions=extensions)
        self.has_next = has_next
        self.incremental = incremental
        self.payload = payload

    def __eq__(self, other: object) -> bool:
        if isinstance(other, ExecutionResult):
            return (
                other.data == self.data
                and other.errors == self.errors
                and other.extensions == self.extensions
            )
        return super().__eq__(other)


def is_graphql_payload(payload: Any) -> bool:
    """Return True when *payload* looks like a GraphQL or incremental result.

    An empty object is not a result. Payloads that only carry ``hasNext`` or an
    ``incremental`` array (including an empty one) are results.
    """

    if not isinstance(payload, dict):
        return False
    if any(key in payload for key in _RESULT_KEYS):
        return True
    return "path" in payload and ("data" in payload or "items" in payload)


def execution_result_from_payload(
    payload: Dict[str, Any],
) -> IncrementalExecutionResult:
    """Build a result that forwards one incremental or single payload."""

    if "hasNext" in payload:
        has_next = bool(payload.get("hasNext"))
    elif "has_next" in payload:
        has_next = bool(payload.get("has_next"))
    else:
        has_next = False

    return IncrementalExecutionResult(
        data=payload.get("data"),
        errors=payload.get("errors"),
        extensions=payload.get("extensions"),
        has_next=has_next,
        incremental=payload.get("incremental"),
        payload=payload,
    )


def payload_from_result(result: ExecutionResult) -> Dict[str, Any]:
    """Return the raw payload represented by a transport result."""

    raw_payload = getattr(result, "payload", None)
    if isinstance(raw_payload, dict):
        return raw_payload

    payload: Dict[str, Any] = {"data": result.data}
    if result.errors is not None:
        payload["errors"] = result.errors
    if result.extensions is not None:
        payload["extensions"] = result.extensions
    incremental = getattr(result, "incremental", None)
    if incremental is not None:
        payload["incremental"] = incremental
    payload["hasNext"] = bool(getattr(result, "has_next", False))
    return payload


def _as_index(value: Any) -> Optional[int]:
    if isinstance(value, bool) or not isinstance(value, int):
        return None
    return value


def _normalize_path(path: Any) -> Optional[List[Any]]:
    if path is None:
        return []
    if isinstance(path, tuple):
        path = list(path)
    if not isinstance(path, list):
        return None
    return path


def _container_for_next(nxt: Any) -> Union[dict, list]:
    if _as_index(nxt) is not None:
        return []
    return {}


def _navigate(root: Any, path: List[Any], *, terminal: Any = None) -> Any:
    """Walk *path*, replacing nulls so later patches can be merged.

    *terminal* is a constructor (``list`` or ``dict``) used when the final
    path entry is null. Stream patches use ``list`` so the parent list exists.
    """

    current = root
    last_i = len(path) - 1
    for i, key in enumerate(path):
        nxt = path[i + 1] if i + 1 < len(path) else None

        def _fill(existing: Any) -> Any:
            if existing is not None:
                return existing
            if i == last_i and terminal is not None:
                return terminal()
            return _container_for_next(nxt)

        if isinstance(current, dict):
            if key not in current or current[key] is None:
                current[key] = _fill(None)
            current = current[key]
            continue
        if isinstance(current, list):
            idx = _as_index(key)
            if idx is None or idx < 0:
                return None
            if idx >= len(current):
                current.extend([None] * (idx - len(current) + 1))
            if current[idx] is None:
                current[idx] = _fill(None)
            current = current[idx]
            continue
        return None
    return current


def _ensure_root(state: Any, path: List[Any]) -> Any:
    if state is not None or not path:
        return state
    if _as_index(path[0]) is not None:
        return []
    return {}


def _apply_defer(state: Any, path: List[Any], data: Any) -> Any:
    patch = copy.deepcopy(data)
    if not path:
        if isinstance(state, dict) and isinstance(patch, dict):
            state.update(patch)
            return state
        return patch

    state = _ensure_root(state, path)
    if state is None:
        return state

    parent_path, last = path[:-1], path[-1]
    parent = state if not parent_path else _navigate(state, parent_path)
    if isinstance(parent, list):
        idx = _as_index(last)
        if idx is None or idx < 0:
            return state
        if idx >= len(parent):
            parent.extend([None] * (idx - len(parent) + 1))
        existing = parent[idx]
        if isinstance(existing, dict) and isinstance(patch, dict):
            existing.update(patch)
        else:
            parent[idx] = patch
        return state
    if isinstance(parent, dict):
        existing = parent.get(last, _MISSING)
        if isinstance(existing, dict) and isinstance(patch, dict):
            existing.update(patch)
        else:
            parent[last] = patch
        return state
    return state


def _apply_stream(state: Any, path: List[Any], items: Any) -> Any:
    if not isinstance(items, list):
        return state
    copied = copy.deepcopy(items)
    if not path:
        if state is None:
            return copied
        if isinstance(state, list):
            state.extend(copied)
        return state

    index = _as_index(path[-1])
    if index is None or index < 0:
        return state

    parent_path = path[:-1]
    state = _ensure_root(state, parent_path or path)
    if state is None:
        return copied if not parent_path else state

    parent = state if not parent_path else _navigate(state, parent_path, terminal=list)
    if not isinstance(parent, list):
        return state

    end = index + len(copied)
    if end > len(parent):
        parent.extend([None] * (end - len(parent)))
    for offset, item in enumerate(copied):
        parent[index + offset] = item
    return state


def apply_incremental_item(state: Any, item: Any) -> Any:
    """Merge one defer or stream incremental item into *state*."""

    if not isinstance(item, dict):
        return state
    path = _normalize_path(item.get("path"))
    if path is None:
        return state
    # Stream payloads carry an items array. Defer payloads carry data.
    if "items" in item and item.get("items") is not None:
        return _apply_stream(state, path, item.get("items"))
    if "data" in item:
        return _apply_defer(state, path, item.get("data"))
    return state


def _iter_patch_items(payload: Dict[str, Any]) -> List[Any]:
    # A single legacy patch puts path/data/items on the payload itself.
    if (
        "incremental" not in payload
        and "path" in payload
        and ("data" in payload or "items" in payload)
    ):
        return [payload]
    incremental = payload.get("incremental")
    if isinstance(incremental, list):
        return incremental
    return []


def apply_incremental_payload(state: Any, payload: Dict[str, Any]) -> Any:
    """Merge one multipart or websocket payload into the accumulated data."""

    if not isinstance(payload, dict):
        return state

    legacy_patch = (
        "incremental" not in payload
        and "path" in payload
        and ("data" in payload or "items" in payload)
    )
    if not legacy_patch and "data" in payload and "path" not in payload:
        incoming = payload.get("data")
        if isinstance(state, dict) and isinstance(incoming, dict):
            state.update(copy.deepcopy(incoming))
        elif incoming is not None or state is None:
            # A later explicit null does not wipe data already accumulated.
            state = copy.deepcopy(incoming)

    for item in _iter_patch_items(payload):
        try:
            state = apply_incremental_item(state, item)
        except Exception:
            continue
    return state


def errors_from_payload(payload: Dict[str, Any]) -> Optional[List[Any]]:
    """Collect errors from this payload and its incremental items.

    Errors are not accumulated across payloads. A failure in one item does not
    drop errors or patches that follow it.
    """

    errors: List[Any] = []
    if "incremental" not in payload and "path" in payload:
        sources = [payload]
    else:
        sources = []
        top = payload.get("errors")
        if isinstance(top, list):
            errors.extend(top)
        elif top:
            errors.append(top)
        incremental = payload.get("incremental")
        if isinstance(incremental, list):
            sources.extend(incremental)

    for item in sources:
        if not isinstance(item, dict):
            continue
        item_errors = item.get("errors")
        if isinstance(item_errors, list):
            errors.extend(item_errors)
        elif item_errors:
            errors.append(item_errors)
    return errors or None


def has_next_from_payload(payload: Dict[str, Any]) -> bool:
    if "hasNext" in payload:
        return bool(payload.get("hasNext"))
    if "has_next" in payload:
        return bool(payload.get("has_next"))
    return False


def extensions_from_payload(payload: Dict[str, Any]) -> Optional[Dict[str, Any]]:
    if "extensions" not in payload:
        return None
    extensions = payload.get("extensions")
    if isinstance(extensions, dict):
        return extensions
    return None
