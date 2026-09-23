"""Incremental delivery of GraphQL results using the @defer and @stream directives.

The response format follows the `deferSpec=20220824` version of the
incremental delivery specification: an initial payload containing ``data``
and ``hasNext``, followed by subsequent payloads containing an ``incremental``
list of deferred (``data``) or streamed (``items``) results with their ``path``.
"""

import copy
import logging
from typing import Any, Dict, List, Optional, Sequence, Union

from graphql import (
    ExecutionResult,
    GraphQLDeferDirective,
    GraphQLSchema,
    GraphQLStreamDirective,
)

log = logging.getLogger(__name__)

INCREMENTAL_PAYLOAD_KEYS = ("incremental", "hasNext")


class IncrementalExecutionResult(ExecutionResult):
    """Result of a single payload of an incremental delivery response.

    When returned by the transports, the attributes contain the raw values
    of the received payload.

    When yielded by
    :meth:`execute_incremental <gql.client.AsyncClientSession.execute_incremental>`,
    :code:`data` contains the data accumulated from all the payloads received
    so far, while :code:`errors`, :code:`extensions` and :code:`incremental`
    only concern the latest payload.
    """

    __slots__ = "has_next", "incremental"

    has_next: bool
    incremental: Optional[List[Dict[str, Any]]]

    def __init__(
        self,
        data: Optional[Dict[str, Any]] = None,
        errors: Optional[List[Any]] = None,
        extensions: Optional[Dict[str, Any]] = None,
        *,
        has_next: bool = False,
        incremental: Optional[List[Dict[str, Any]]] = None,
    ) -> None:
        super().__init__(data=data, errors=errors, extensions=extensions)
        self.has_next = has_next
        self.incremental = incremental

    @classmethod
    def from_payload(cls, payload: Dict[str, Any]) -> "IncrementalExecutionResult":
        """Create an instance from a received JSON payload."""
        return cls(
            data=payload.get("data"),
            errors=payload.get("errors"),
            extensions=payload.get("extensions"),
            has_next=bool(payload.get("hasNext", False)),
            incremental=payload.get("incremental"),
        )

    def __repr__(self) -> str:
        return (
            f"{self.__class__.__name__}(data={self.data!r}, errors={self.errors!r}, "
            f"extensions={self.extensions!r}, has_next={self.has_next!r}, "
            f"incremental={self.incremental!r})"
        )


def add_incremental_directives(schema: GraphQLSchema) -> GraphQLSchema:
    """Return the schema, or a copy of the schema including the @defer and
    @stream directives if they are not already defined."""
    directive_names = {directive.name for directive in schema.directives}

    missing_directives = [
        directive
        for directive in (GraphQLDeferDirective, GraphQLStreamDirective)
        if directive.name not in directive_names
    ]

    if not missing_directives:
        return schema

    schema_kwargs = schema.to_kwargs()
    schema_kwargs["directives"] = (*schema.directives, *missing_directives)

    return GraphQLSchema(**schema_kwargs)


def is_incremental_payload(payload: Dict[str, Any]) -> bool:
    """Returns True if the payload contains incremental delivery keys."""
    return any(key in payload for key in INCREMENTAL_PAYLOAD_KEYS)


def payload_to_execution_result(payload: Dict[str, Any]) -> ExecutionResult:
    """Convert a received JSON payload to an ExecutionResult,
    keeping the incremental delivery fields if present."""
    if is_incremental_payload(payload):
        return IncrementalExecutionResult.from_payload(payload)

    return ExecutionResult(
        data=payload.get("data"),
        errors=payload.get("errors"),
        extensions=payload.get("extensions"),
    )


def _merge_values(existing: Any, new: Any) -> Any:
    if isinstance(existing, dict) and isinstance(new, dict):
        for key, value in new.items():
            existing[key] = _merge_values(existing.get(key), value)
        return existing

    if isinstance(existing, list) and isinstance(new, list):
        if len(existing) == len(new):
            return [_merge_values(e, n) for e, n in zip(existing, new)]

    return new


class IncrementalDataMerger:
    """Accumulate the data of the successive payloads of an incremental
    delivery response."""

    def __init__(self) -> None:
        self.data: Optional[Dict[str, Any]] = None

    def _get_at_path(self, path: Sequence[Union[str, int]]) -> Any:
        if self.data is None:
            self.data = {}

        current: Any = self.data

        for key in path:
            if isinstance(current, dict) and isinstance(key, str):
                current = current.get(key)
            elif (
                isinstance(current, list)
                and isinstance(key, int)
                and not isinstance(key, bool)
                and 0 <= key < len(current)
            ):
                current = current[key]
            else:
                return None

        return current

    def _merge_deferred_data(
        self, path: Sequence[Union[str, int]], data: Optional[Dict[str, Any]]
    ) -> None:
        if data is None:
            return

        target = self._get_at_path(path)

        if not isinstance(target, dict):
            log.warning(f"Cannot merge deferred data at path {list(path)!r}")
            return

        _merge_values(target, copy.deepcopy(data))

    def _merge_stream_items(
        self, path: Sequence[Union[str, int]], items: Optional[List[Any]]
    ) -> None:
        if items is None:
            return

        start = path[-1] if path else None

        if not isinstance(start, int) or isinstance(start, bool) or start < 0:
            log.warning(f"Invalid path for streamed items: {list(path)!r}")
            return

        target = self._get_at_path(path[:-1])

        if not isinstance(target, list):
            log.warning(f"Cannot merge streamed items at path {list(path)!r}")
            return

        for index, item in enumerate(copy.deepcopy(items), start=start):
            if index < len(target):
                target[index] = item
            else:
                target.extend([None] * (index - len(target)))
                target.append(item)

    def merge(self, result: IncrementalExecutionResult) -> IncrementalExecutionResult:
        """Merge a received payload into the accumulated data.

        :param result: the payload received from the transport
        :return: a new IncrementalExecutionResult containing a copy of the
            accumulated data and the errors, extensions and incremental items
            of this payload only.
        """
        errors: List[Any] = list(result.errors or [])

        if result.data is not None:
            if self.data is None:
                self.data = copy.deepcopy(result.data)
            else:
                _merge_values(self.data, copy.deepcopy(result.data))

        for item in result.incremental or []:
            if not isinstance(item, dict):
                log.warning(f"Ignoring invalid incremental item: {item!r}")
                continue

            if item.get("errors"):
                errors.extend(item["errors"])

            path = item.get("path") or []

            if "items" in item:
                self._merge_stream_items(path, item["items"])
            elif "data" in item:
                self._merge_deferred_data(path, item["data"])

        return IncrementalExecutionResult(
            data=copy.deepcopy(self.data),
            errors=errors or None,
            extensions=result.extensions,
            has_next=result.has_next,
            incremental=result.incremental,
        )
