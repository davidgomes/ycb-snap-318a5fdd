"""Client-side support for GraphQL ``@defer`` and ``@stream`` payloads.

Payloads follow the incremental-delivery shape used with
``deferSpec=20220824``: an optional initial ``data`` object, then
``incremental`` entries that carry either ``data`` (deferred fields) or
``items`` (streamed list elements) plus a ``path``.
"""

import copy
import logging
from typing import Any, Dict, List, Optional, Tuple

from graphql import (
    ExecutionResult,
    GraphQLDeferDirective,
    GraphQLSchema,
    GraphQLStreamDirective,
)

log = logging.getLogger(__name__)

# graphql-core ships these as experimental directives. They are not part of
# the default schema, so validation adds them when a document uses them.
GRAPHQL_DEFER_DIRECTIVE = GraphQLDeferDirective
GRAPHQL_STREAM_DIRECTIVE = GraphQLStreamDirective


def schema_with_incremental_directives(schema: GraphQLSchema) -> GraphQLSchema:
    """Return *schema* with ``@defer`` and ``@stream`` when they are missing.

    graphql-core does not ship these directives. Validation of an incremental
    document uses the returned schema; the caller's schema object is unchanged.
    """
    names = {directive.name for directive in schema.directives}
    extra = []
    if "defer" not in names:
        extra.append(GRAPHQL_DEFER_DIRECTIVE)
    if "stream" not in names:
        extra.append(GRAPHQL_STREAM_DIRECTIVE)
    if not extra:
        return schema
    return GraphQLSchema(
        query=schema.query_type,
        mutation=schema.mutation_type,
        subscription=schema.subscription_type,
        directives=tuple(schema.directives) + tuple(extra),
    )


class IncrementalExecutionResult(ExecutionResult):
    """One incremental payload after ``data`` has been merged.

    ``data`` is the accumulated result so far. ``extensions`` and ``errors``
    belong to this payload only. ``has_next`` is true when the server will
    send another payload.
    """

    __slots__ = ("has_next", "incremental", "has_data", "pending", "completed")

    def __init__(
        self,
        data: Optional[Dict[str, Any]] = None,
        errors: Optional[List[Any]] = None,
        extensions: Optional[Dict[str, Any]] = None,
        has_next: bool = False,
        incremental: Optional[List[Any]] = None,
        has_data: bool = False,
        pending: Optional[List[Any]] = None,
        completed: Optional[List[Any]] = None,
    ) -> None:
        super().__init__(data=data, errors=errors, extensions=extensions)
        self.has_next = has_next
        self.incremental = incremental
        self.has_data = has_data
        self.pending = pending
        self.completed = completed

    def __repr__(self) -> str:
        name = self.__class__.__name__
        ext = "" if self.extensions is None else f", extensions={self.extensions!r}"
        return (
            f"{name}(data={self.data!r}, errors={self.errors!r}{ext}, "
            f"has_next={self.has_next!r})"
        )

    def __eq__(self, other: object) -> bool:
        if isinstance(other, IncrementalExecutionResult):
            return (
                other.data == self.data
                and other.errors == self.errors
                and other.extensions == self.extensions
                and other.has_next == self.has_next
            )
        if isinstance(other, ExecutionResult):
            return (
                other.data == self.data
                and other.errors == self.errors
                and other.extensions == self.extensions
            )
        return super().__eq__(other)


def incremental_result_from_payload(
    payload: Dict[str, Any],
) -> IncrementalExecutionResult:
    """Build a raw (not yet merged) result from one GraphQL JSON payload."""
    has_next = payload.get("hasNext", False)
    return IncrementalExecutionResult(
        data=payload.get("data"),
        errors=payload.get("errors"),
        extensions=payload.get("extensions"),
        has_next=bool(has_next) if has_next is not None else False,
        incremental=payload.get("incremental"),
        has_data="data" in payload,
        pending=payload.get("pending"),
        completed=payload.get("completed"),
    )


def _dict_entry(entry: Any, fields: Tuple[str, ...]) -> Dict[str, Any]:
    """Copy selected attributes of a graphql-core incremental object."""
    if isinstance(entry, dict):
        return {key: entry[key] for key in fields if key in entry}
    converted: Dict[str, Any] = {}
    for key in fields:
        if hasattr(entry, key):
            converted[key] = getattr(entry, key)
        snake = "".join(f"_{char.lower()}" if char.isupper() else char for char in key)
        if snake != key and hasattr(entry, snake) and key not in converted:
            converted[key] = getattr(entry, snake)
    return converted


def chunk_from_graphql_result(result: Any) -> IncrementalExecutionResult:
    """Normalize a graphql-core execution result into one incremental chunk.

    Plain results, initial incremental results, and subsequent payloads are
    all returned as :class:`IncrementalExecutionResult`. Pending paths and
    incremental entries are kept so the session can merge them.
    """
    if isinstance(result, IncrementalExecutionResult):
        return result

    if isinstance(result, ExecutionResult):
        return IncrementalExecutionResult(
            data=result.data,
            errors=result.errors,
            extensions=result.extensions,
            has_next=bool(getattr(result, "has_next", False)),
            incremental=getattr(result, "incremental", None),
            has_data=getattr(result, "has_data", True),
            pending=getattr(result, "pending", None),
            completed=getattr(result, "completed", None),
        )

    has_data = hasattr(result, "data")
    incremental = getattr(result, "incremental", None)
    if incremental is not None:
        incremental = [
            _dict_entry(
                item,
                (
                    "id",
                    "data",
                    "items",
                    "path",
                    "subPath",
                    "errors",
                    "extensions",
                    "label",
                ),
            )
            for item in incremental
        ]
    pending = getattr(result, "pending", None)
    if pending is not None:
        pending = [_dict_entry(item, ("id", "path", "label")) for item in pending]
    completed = getattr(result, "completed", None)
    if completed is not None:
        completed = [_dict_entry(item, ("id", "errors", "path")) for item in completed]

    return IncrementalExecutionResult(
        data=getattr(result, "data", None) if has_data else None,
        errors=getattr(result, "errors", None),
        extensions=getattr(result, "extensions", None),
        has_next=bool(getattr(result, "has_next", False)),
        incremental=incremental,
        has_data=has_data,
        pending=pending,
        completed=completed,
    )


def is_graphql_incremental_payload(payload: Any) -> bool:
    """Return True when *payload* looks like a GraphQL incremental response."""
    if not isinstance(payload, dict):
        return False
    return any(key in payload for key in ("data", "errors", "incremental", "hasNext"))


def _is_index(segment: Any) -> bool:
    return isinstance(segment, int) and not isinstance(segment, bool)


def _get_child(parent: Any, segment: Any) -> Any:
    if _is_index(segment):
        if not isinstance(parent, list):
            raise TypeError(f"Cannot index {type(parent).__name__} with {segment!r}")
        index = int(segment)
        if index < 0:
            raise IndexError(index)
        if index >= len(parent):
            return None
        return parent[index]
    if not isinstance(parent, dict):
        raise TypeError(f"Cannot read field {segment!r} on {type(parent).__name__}")
    return parent.get(segment)


def _set_child(parent: Any, segment: Any, value: Any) -> None:
    if _is_index(segment):
        if not isinstance(parent, list):
            raise TypeError(f"Cannot index {type(parent).__name__} with {segment!r}")
        index = int(segment)
        if index < 0:
            raise IndexError(index)
        if index >= len(parent):
            parent.extend([None] * (index - len(parent) + 1))
        parent[index] = value
        return
    if not isinstance(parent, dict):
        raise TypeError(f"Cannot write field {segment!r} on {type(parent).__name__}")
    parent[segment] = value


def _ensure_container(parent: Any, segment: Any, next_segment: Any) -> Any:
    """Return the child at *segment*, creating a dict or list when it is null."""
    want_list = _is_index(next_segment)
    if _is_index(segment) and isinstance(parent, list) and int(segment) >= len(parent):
        _set_child(parent, segment, None)
    child = _get_child(parent, segment)
    if want_list and isinstance(child, list):
        return child
    if not want_list and isinstance(child, dict):
        return child
    if child is None:
        created: Any = [] if want_list else {}
        _set_child(parent, segment, created)
        return created
    raise TypeError(f"Cannot traverse incremental path through {child!r}")


def _merge_values(current: Any, incoming: Any) -> Any:
    """Deep-merge objects. Lists take the incoming length and merge by index."""
    if isinstance(current, dict) and isinstance(incoming, dict):
        merged = {key: copy.deepcopy(value) for key, value in current.items()}
        for key, value in incoming.items():
            if key in merged:
                merged[key] = _merge_values(merged[key], value)
            else:
                merged[key] = copy.deepcopy(value)
        return merged
    if isinstance(current, list) and isinstance(incoming, list):
        merged_list: List[Any] = []
        for index, item in enumerate(incoming):
            if index < len(current):
                merged_list.append(_merge_values(current[index], item))
            else:
                merged_list.append(copy.deepcopy(item))
        return merged_list
    return copy.deepcopy(incoming)


def _place(root: Any, path: List[Any], value: Any) -> Any:
    """Merge *value* into *root* at *path*, creating null containers along the way."""
    if not path:
        if root is None:
            return copy.deepcopy(value)
        return _merge_values(root, value)

    if root is None:
        root = [] if _is_index(path[0]) else {}

    parent = root
    for index, segment in enumerate(path[:-1]):
        parent = _ensure_container(parent, segment, path[index + 1])

    last = path[-1]
    current = _get_child(parent, last)
    if isinstance(current, dict) and isinstance(value, dict):
        placed: Any = _merge_values(current, value)
    elif isinstance(current, list) and isinstance(value, list):
        placed = _merge_values(current, value)
    else:
        placed = copy.deepcopy(value)
    _set_child(parent, last, placed)
    return root


def _normalize_path(
    item: Dict[str, Any], pending_paths: Dict[str, List[Any]]
) -> List[Any]:
    """Resolve where an incremental entry is merged.

    An explicit ``path`` wins. Otherwise an ``id`` is looked up in the
    ``pending`` paths published earlier (graphql-core incremental delivery).
    An entry with neither is a root-level merge (``[]``). ``subPath`` is
    appended only when a base path was found.
    """
    used_base = False
    if "path" in item and item.get("path") is not None:
        path = item["path"]
        if not isinstance(path, list):
            raise TypeError("Incremental path must be a list")
        resolved = list(path)
        used_base = True
    else:
        item_id = item.get("id")
        pending = pending_paths.get(str(item_id)) if item_id is not None else None
        if pending is not None:
            resolved = list(pending)
            used_base = True
        else:
            resolved = []
    if used_base:
        sub_path = item.get("subPath")
        if isinstance(sub_path, list):
            resolved.extend(sub_path)
    return resolved


def _apply_stream(root: Any, path: List[Any], items: Any) -> Any:
    """Write streamed items starting at the path's last index.

    The write is a range assignment: item ``i`` is stored at
    ``start + i``, extending the list with null when the index is past the
    end. Writing the same payload again overwrites those indexes.
    """
    if items is None:
        return root
    if not isinstance(items, list):
        raise TypeError("Stream incremental payload items must be a list")

    if path and _is_index(path[-1]):
        start = int(path[-1])
        if start < 0:
            raise IndexError(start)
        parent_path = path[:-1]
        for offset, item in enumerate(items):
            root = _place(root, [*parent_path, start + offset], item)
        return root

    # No trailing index: append to the list at this path. graphql-core publishes
    # the list field itself as the pending path and sends only the new items.
    return _append_stream_items(root, path, items)


def _append_stream_items(root: Any, path: List[Any], items: List[Any]) -> Any:
    """Append *items* to the list at *path*, creating null containers as needed."""
    if not path:
        if root is None:
            root = []
        if not isinstance(root, list):
            return root
        root.extend(copy.deepcopy(item) for item in items)
        return root

    if root is None:
        root = [] if _is_index(path[0]) else {}

    parent = root
    for index, segment in enumerate(path[:-1]):
        parent = _ensure_container(parent, segment, path[index + 1])

    last = path[-1]
    current = _get_child(parent, last)
    if current is None:
        current = []
        _set_child(parent, last, current)
    if not isinstance(current, list):
        raise TypeError(f"Cannot stream items into {current!r}")
    current.extend(copy.deepcopy(item) for item in items)
    return root


def _apply_incremental_item(
    root: Any, item: Any, pending_paths: Dict[str, List[Any]]
) -> Any:
    if not isinstance(item, dict):
        raise TypeError("Incremental entry must be an object")
    path = _normalize_path(item, pending_paths)
    # ``items`` marks a @stream payload, including ``items: null``.
    if "items" in item:
        return _apply_stream(root, path, item.get("items"))
    if "data" in item:
        data = item.get("data")
        # A null document must not erase fields already accumulated.
        if data is None and not path:
            return root
        return _place(root, path, data)
    return root


def _extend_errors(errors: List[Any], new_errors: Any) -> None:
    if not new_errors:
        return
    if isinstance(new_errors, list):
        errors.extend(new_errors)
    else:
        errors.append(new_errors)


def _pending_path(entry: Any) -> Optional[Tuple[str, List[Any]]]:
    """Return ``(id, path)`` from a pending entry, or None."""
    if isinstance(entry, dict):
        ident = entry.get("id")
        path = entry.get("path")
    else:
        ident = getattr(entry, "id", None)
        path = getattr(entry, "path", None)
    if ident is None or not isinstance(path, list):
        return None
    return str(ident), list(path)


def _completed_id(entry: Any) -> Optional[str]:
    if isinstance(entry, dict):
        ident = entry.get("id")
    else:
        ident = getattr(entry, "id", None)
    if ident is None:
        return None
    return str(ident)


class IncrementalResultAccumulator:
    """Merge incremental payloads into one accumulated ``data`` value."""

    def __init__(self) -> None:
        self.data: Any = None
        self._pending_paths: Dict[str, List[Any]] = {}

    def _remember_pending(self, pending: Any) -> None:
        if not pending:
            return
        for entry in pending:
            parsed = _pending_path(entry)
            if parsed is None:
                continue
            ident, path = parsed
            self._pending_paths[ident] = path

    def _forget_completed(self, completed: Any) -> None:
        if not completed:
            return
        for entry in completed:
            ident = _completed_id(entry)
            if ident is not None:
                self._pending_paths.pop(ident, None)

    def apply(self, chunk: ExecutionResult) -> IncrementalExecutionResult:
        """Merge *chunk* and return a snapshot of the accumulated result.

        GraphQL errors are kept on this payload and do not stop later entries.
        A single malformed incremental entry is skipped so the rest of the
        payload is still applied.
        """
        has_data = getattr(chunk, "has_data", True)
        errors: List[Any] = []
        _extend_errors(errors, getattr(chunk, "errors", None))

        if has_data and chunk.data is not None:
            try:
                self.data = _place(self.data, [], chunk.data)
            except Exception:
                log.debug("Failed to merge incremental data payload", exc_info=True)

        # Pending paths in this payload are visible to its incremental entries.
        self._remember_pending(getattr(chunk, "pending", None))

        incremental = getattr(chunk, "incremental", None)
        if isinstance(incremental, list):
            for item in incremental:
                if isinstance(item, dict):
                    _extend_errors(errors, item.get("errors"))
                try:
                    candidate = _apply_incremental_item(
                        copy.deepcopy(self.data) if self.data is not None else None,
                        item,
                        self._pending_paths,
                    )
                except Exception:
                    log.debug("Failed to apply incremental entry", exc_info=True)
                    continue
                self.data = candidate

        completed = getattr(chunk, "completed", None)
        if completed:
            for entry in completed:
                if isinstance(entry, dict):
                    _extend_errors(errors, entry.get("errors"))
                else:
                    _extend_errors(errors, getattr(entry, "errors", None))
        self._forget_completed(completed)

        snapshot = copy.deepcopy(self.data) if self.data is not None else None
        return IncrementalExecutionResult(
            data=snapshot,
            errors=errors or None,
            extensions=getattr(chunk, "extensions", None),
            has_next=bool(getattr(chunk, "has_next", False)),
            incremental=None,
            has_data=bool(has_data and chunk.data is not None),
        )
