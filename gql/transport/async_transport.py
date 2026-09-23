import abc
from typing import Any, AsyncGenerator, Dict, List

from graphql import ExecutionResult

from ..graphql_request import GraphQLRequest
from ..incremental import IncrementalExecutionResult, execution_result_from_payload


class AsyncTransport(abc.ABC):
    @abc.abstractmethod
    async def connect(self):
        """Coroutine used to create a connection to the specified address"""
        raise NotImplementedError(
            "Any AsyncTransport subclass must implement connect method"
        )  # pragma: no cover

    @abc.abstractmethod
    async def close(self):
        """Coroutine used to Close an established connection"""
        raise NotImplementedError(
            "Any AsyncTransport subclass must implement close method"
        )  # pragma: no cover

    @abc.abstractmethod
    async def execute(
        self,
        request: GraphQLRequest,
    ) -> ExecutionResult:
        """Execute the provided request for either a remote or local GraphQL
        Schema."""
        raise NotImplementedError(
            "Any AsyncTransport subclass must implement execute method"
        )  # pragma: no cover

    async def execute_batch(
        self,
        reqs: List[GraphQLRequest],
        *args: Any,
        **kwargs: Any,
    ) -> List[ExecutionResult]:
        """Execute multiple GraphQL requests in a batch.

        Execute the provided requests for either a remote or local GraphQL Schema.

        :param reqs: GraphQL requests as a list of GraphQLRequest objects.
        :return: a list of ExecutionResult objects
        """
        raise NotImplementedError(
            "This Transport has not implemented the execute_batch method"
        )  # pragma: no cover

    async def execute_incremental(
        self,
        request: GraphQLRequest,
        *args: Any,
        **kwargs: Any,
    ) -> AsyncGenerator[IncrementalExecutionResult, None]:
        """Execute a request and yield incremental results.

        Transports that cannot stream incremental payloads yield the single
        result of :meth:`execute` with ``has_next`` set to False.
        """

        result = await self.execute(request, *args, **kwargs)
        if isinstance(result, IncrementalExecutionResult):
            yield result
            return

        payload: Dict[str, Any] = {
            "data": result.data,
            "hasNext": False,
        }
        if result.errors is not None:
            payload["errors"] = result.errors
        if result.extensions is not None:
            payload["extensions"] = result.extensions
        yield execution_result_from_payload(payload)

    @abc.abstractmethod
    def subscribe(
        self,
        request: GraphQLRequest,
    ) -> AsyncGenerator[ExecutionResult, None]:
        """Send a query and receive the results using an async generator

        The query can be a graphql query, mutation or subscription

        The results are sent as an ExecutionResult object
        """
        raise NotImplementedError(
            "Any AsyncTransport subclass must implement subscribe method"
        )  # pragma: no cover
