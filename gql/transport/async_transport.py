import abc
from typing import Any, AsyncGenerator, List

from graphql import ExecutionResult

from ..graphql_request import GraphQLRequest
from ..incremental import IncrementalExecutionResult


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
    ) -> AsyncGenerator[ExecutionResult, None]:
        """Execute a request and yield a single, non-incremental result.

        Transports that can receive ``@defer`` / ``@stream`` payloads override
        this method. The default reads one execution result and yields it with
        ``has_next`` false so callers can treat every transport the same way.
        """
        result = await self.execute(request, *args, **kwargs)
        has_next = bool(getattr(result, "has_next", False))
        yield IncrementalExecutionResult(
            data=result.data,
            errors=result.errors,
            extensions=result.extensions,
            has_next=has_next,
            incremental=getattr(result, "incremental", None),
            has_data=getattr(result, "has_data", True),
            pending=getattr(result, "pending", None),
            completed=getattr(result, "completed", None),
        )

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
