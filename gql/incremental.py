"""Client-side accumulation for GraphQL ``@defer`` and ``@stream`` payloads.

Payloads follow the incremental delivery format used with
``deferSpec=20220824``: an initial ``data`` object, then ``incremental``
items that carry either a ``data`` patch (``@defer``) or an ``items`` array
(``@stream``).
"""

import copy
from typing import Any, Dict, List, Optional, Tuple

from graphql import ExecutionResult


class IncrementalExecutionResult(ExecutionResult):
    """One step of an incremental GraphQL response.

    ``data`` is the result accumulated from the initial payload and every
    incremental patch received so far. ``extensions`` and ``errors`` belong
    to this payload only. ``has_next`` is true when the server will send
    further payloads.
    """

    def __init__(
        self,
        data: Optional[Dict[str, Any]] = None,
        errors: Optional[List[Any]] = None,
        extensions: Optional[Dict[str, Any]] = None,
        has_next: bool = False,
    ) -> None:
        super().__init__(data=data, errors=errors, extensions=extensions)
        self.has_next = has_next

    def __repr__(self) -> str:  # pragma: no cover
        return (
            "IncrementalExecutionResult("
            f"data={self.data!r}, errors={self.errors!r}, "
            f"extensions={self.extensions!r}, has_next={self.has_next!r})"
        )


def execution_result_to_payload(result: ExecutionResult) -> Dict[str, Any]:
    """Convert a transport execution result into an incremental payload dict.

    Incremental websocket messages keep the original JSON payload so fields
    such as ``incremental`` and ``hasNext`` are not dropped.
    """

    raw_payload = getattr(result, "raw_payload", None)
    if isinstance(raw_payload, dict):
        return raw_payload

    payload: Dict[str, Any] = {
        "data": result.data,
        "errors": result.errors,
        "extensions": result.extensions,
        "hasNext": bool(getattr(result, "has_next", False)),
    }
    incremental = getattr(result, "incremental", None)
    if incremental is not None:
        payload["incremental"] = incremental
    path = getattr(result, "path", None)
    if path is not None:
        payload["path"] = path
    return payload


def _is_index(value: Any) -> bool:
    return isinstance(value, int) and not isinstance(value, bool)


def _normalize_path(path: Any) -> List[Any]:
    if path is None:
        return []
    if isinstance(path, str) or _is_index(path):
        return [path]
    if isinstance(path, list):
        return list(path)
    raise TypeError(f"Incremental path must be a list, got {type(path)!r}")


def _as_error_list(errors: Any) -> List[Any]:
    if not errors:
        return []
    if isinstance(errors, list):
        return list(errors)
    return [errors]


def _merge_dict_in_place(target: Dict[str, Any], patch: Dict[str, Any]) -> None:
    """Deep-merge ``patch`` into ``target``. Dicts merge; other values overwrite."""

    for key, value in patch.items():
        current = target.get(key)
        if isinstance(current, dict) and isinstance(value, dict):
            _merge_dict_in_place(current, value)
        else:
            target[key] = copy.deepcopy(value)


def _get_child(container: Any, key: Any) -> Any:
    if _is_index(key):
        if isinstance(container, list) and 0 <= key < len(container):
            return container[key]
        return None
    if isinstance(container, dict):
        return container.get(key)
    return None


def _set_child(container: Any, key: Any, value: Any) -> None:
    if _is_index(key):
        if key < 0:
            raise ValueError("Incremental path index cannot be negative")
        if not isinstance(container, list):
            raise TypeError("Expected a list while applying an incremental path")
        while len(container) <= key:
            container.append(None)
        container[key] = value
        return

    if not isinstance(container, dict):
        raise TypeError("Expected an object while applying an incremental path")
    container[key] = value


def _ensure_path_parent(root: Any, path: List[Any]) -> Any:
    """Create containers along ``path`` and return ``(root, parent, last_key)``.

    The returned parent is the container that holds the final path key.
    Null or incompatible values along the path are replaced so later patches
    can still be applied.
    """

    if not path:
        return root, None, None

    if root is None:
        root = [] if _is_index(path[0]) else {}

    current = root
    for index, key in enumerate(path[:-1]):
        next_key = path[index + 1]
        if _is_index(key) and key < 0:
            raise ValueError("Incremental path index cannot be negative")

        factory: Any = list if _is_index(next_key) else dict
        if _is_index(key):
            if not isinstance(current, list):
                raise TypeError("Expected a list while applying an incremental path")
            while len(current) <= key:
                current.append(None)
            child = current[key]
            if not isinstance(child, factory):
                child = factory()
                current[key] = child
            current = child
        else:
            if not isinstance(current, dict):
                raise TypeError("Expected an object while applying an incremental path")
            child = current.get(key)
            if not isinstance(child, factory):
                child = factory()
                current[key] = child
            current = child

    return root, current, path[-1]


def merge_value_at_path(root: Any, path: List[Any], value: Any) -> Any:
    """Merge ``value`` into ``root`` at ``path``. An empty path merges at the root."""

    if not path:
        if isinstance(root, dict) and isinstance(value, dict):
            _merge_dict_in_place(root, value)
            return root
        return copy.deepcopy(value)

    root, parent, last_key = _ensure_path_parent(root, path)
    existing = _get_child(parent, last_key)
    if isinstance(existing, dict) and isinstance(value, dict):
        _merge_dict_in_place(existing, value)
    else:
        _set_child(parent, last_key, copy.deepcopy(value))
    return root


def stream_items_at_path(root: Any, path: List[Any], items: Any) -> Any:
    """Write ``items`` into the list located by ``path``.

    The last path entry is the index of the first item. Later items occupy
    the following indexes. Existing slots are overwritten, which keeps
    out-of-order stream patches aligned.
    """

    if not isinstance(items, list):
        raise TypeError("@stream incremental payload items must be a list")
    if not path or not _is_index(path[-1]) or path[-1] < 0:
        raise ValueError("@stream path must end with a non-negative index")

    start = path[-1]
    list_path = list(path[:-1])

    if not list_path:
        if not isinstance(root, list):
            root = []
        for offset, item in enumerate(items):
            _set_child(root, start + offset, copy.deepcopy(item))
        return root

    root, parent, last_key = _ensure_path_parent(root, list_path)
    target = _get_child(parent, last_key)
    if not isinstance(target, list):
        target = []
        _set_child(parent, last_key, target)

    for offset, item in enumerate(items):
        _set_child(target, start + offset, copy.deepcopy(item))
    return root


def append_items_at_path(root: Any, path: List[Any], items: Any) -> Any:
    """Append ``items`` to the list at ``path``.

    Used when a ``@stream`` patch identifies the list itself (the
    ``pending``/``id`` delivery format) instead of an insertion index.
    """

    if not isinstance(items, list):
        raise TypeError("@stream incremental payload items must be a list")

    if not path:
        if not isinstance(root, list):
            root = []
        root.extend(copy.deepcopy(item) for item in items)
        return root

    root, parent, last_key = _ensure_path_parent(root, path)
    target = _get_child(parent, last_key)
    if not isinstance(target, list):
        target = []
        _set_child(parent, last_key, target)
    target.extend(copy.deepcopy(item) for item in items)
    return root


class IncrementalResultBuilder:
    """Fold incremental delivery payloads into accumulated execution results."""

    def __init__(self) -> None:
        self.data: Any = None
        # id -> path, from ``pending`` entries in the newer incremental format.
        self._pending_paths: Dict[Any, List[Any]] = {}

    def apply(self, payload: Dict[str, Any]) -> IncrementalExecutionResult:
        """Apply one payload and return the accumulated result.

        GraphQL errors are recorded and do not stop later incremental items
        in the same payload. ``extensions`` are taken from this payload only.
        """

        if not isinstance(payload, dict):
            raise TypeError(
                "Incremental payload must be a dict, " f"got {type(payload)!r}"
            )

        errors = _as_error_list(payload.get("errors"))
        self._register_pending(payload.get("pending"))

        try:
            self._apply_top_level_data(payload)
        except (TypeError, ValueError, KeyError, IndexError, AttributeError):
            # A malformed top-level patch must not discard the rest of the payload.
            pass

        incremental = payload.get("incremental")
        if isinstance(incremental, list):
            for item in incremental:
                if not isinstance(item, dict):
                    continue
                errors.extend(_as_error_list(item.get("errors")))
                try:
                    self._apply_item(item)
                except (TypeError, ValueError, KeyError, IndexError, AttributeError):
                    continue

        extensions = payload.get("extensions") if "extensions" in payload else None
        has_next = payload.get("hasNext") is True

        return IncrementalExecutionResult(
            data=copy.deepcopy(self.data),
            errors=errors or None,
            extensions=extensions,
            has_next=has_next,
        )

    def _apply_top_level_data(self, payload: Dict[str, Any]) -> None:
        if "data" not in payload or payload.get("data") is None:
            return

        value = payload["data"]
        if "path" in payload:
            self.data = merge_value_at_path(
                self.data, _normalize_path(payload.get("path")), value
            )
            return

        if isinstance(self.data, dict) and isinstance(value, dict):
            _merge_dict_in_place(self.data, value)
        else:
            self.data = copy.deepcopy(value)

    def _register_pending(self, pending: Any) -> None:
        if not isinstance(pending, list):
            return
        for entry in pending:
            if not isinstance(entry, dict) or "id" not in entry or "path" not in entry:
                continue
            self._pending_paths[entry["id"]] = _normalize_path(entry.get("path"))

    def _item_path(self, item: Dict[str, Any]) -> Tuple[List[Any], bool]:
        """Return ``(path, from_pending_id)``.

        An explicit ``path`` wins. Otherwise an ``id`` published in ``pending``
        is expanded with an optional ``subPath``. An item with neither is a
        root-level merge.
        """

        if "path" in item:
            return _normalize_path(item.get("path")), False

        item_id = item.get("id")
        if item_id is not None and item_id in self._pending_paths:
            path = list(self._pending_paths[item_id])
            sub_path = item["subPath"] if "subPath" in item else item.get("sub_path")
            if sub_path is not None:
                path.extend(_normalize_path(sub_path))
            return path, True

        return [], False

    def _apply_item(self, item: Dict[str, Any]) -> None:
        path, from_pending = self._item_path(item)

        if "items" in item and item.get("items") is not None:
            # ``pending``/``id`` stream patches name the list. Path-based
            # patches end with the index of the first item.
            if from_pending and (not path or not _is_index(path[-1])):
                self.data = append_items_at_path(self.data, path, item.get("items"))
            else:
                self.data = stream_items_at_path(self.data, path, item.get("items"))
            return

        if "data" in item:
            self.data = merge_value_at_path(self.data, path, item.get("data"))
