import asyncio
from inspect import isawaitable
from typing import Any, AsyncGenerator, Awaitable, Dict, cast

from graphql import ExecutionResult, GraphQLSchema, execute, subscribe
from graphql.execution.execute import experimental_execute_incrementally

from gql.incremental import execution_result_to_payload
from gql.transport import AsyncTransport

from ..graphql_request import GraphQLRequest


class LocalSchemaTransport(AsyncTransport):
    """A transport for executing GraphQL queries against a local schema."""

    def __init__(
        self,
        schema: GraphQLSchema,
    ):
        """Initialize the transport with the given local schema.

        :param schema: Local schema as GraphQLSchema object
        """
        self.schema = schema

    async def connect(self):
        """No connection needed on local transport"""
        pass

    async def close(self):
        """No close needed on local transport"""
        pass

    async def execute(
        self,
        request: GraphQLRequest,
        *args: Any,
        **kwargs: Any,
    ) -> ExecutionResult:
        """Execute the provided request for on a local GraphQL Schema."""

        inner_kwargs = {
            "variable_values": request.variable_values,
            "operation_name": request.operation_name,
            **kwargs,
        }

        result_or_awaitable = execute(
            self.schema,
            request.document,
            *args,
            **inner_kwargs,
        )

        execution_result: ExecutionResult

        if isawaitable(result_or_awaitable):
            result_or_awaitable = cast(Awaitable[ExecutionResult], result_or_awaitable)
            execution_result = await result_or_awaitable
        else:
            result_or_awaitable = cast(ExecutionResult, result_or_awaitable)
            execution_result = result_or_awaitable

        return execution_result

    async def execute_incremental(
        self,
        request: GraphQLRequest,
        *args: Any,
        **kwargs: Any,
    ) -> AsyncGenerator[Dict[str, Any], None]:
        """Execute a query and yield each ``@defer`` / ``@stream`` payload.

        Non-incremental queries yield a single payload. Payloads are the
        formatted JSON objects produced by graphql-core, either path-based or
        ``pending``/``id`` based.
        """

        inner_kwargs = {
            "variable_values": request.variable_values,
            "operation_name": request.operation_name,
            **kwargs,
        }

        result = experimental_execute_incrementally(
            self.schema,
            request.document,
            *args,
            **inner_kwargs,
        )
        result = await self._await_if_necessary(result)

        if isinstance(result, ExecutionResult):
            yield execution_result_to_payload(result)
            return

        yield result.initial_result.formatted
        async for subsequent in result.subsequent_results:
            yield subsequent.formatted

    @staticmethod
    async def _await_if_necessary(obj):
        """This method is necessary to work with
        graphql-core versions < and >= 3.3.0a3"""
        return await obj if asyncio.iscoroutine(obj) else obj

    async def subscribe(
        self,
        request: GraphQLRequest,
        *args: Any,
        **kwargs: Any,
    ) -> AsyncGenerator[ExecutionResult, None]:
        """Send a subscription and receive the results using an async generator

        The results are sent as an ExecutionResult object
        """

        inner_kwargs = {
            "variable_values": request.variable_values,
            "operation_name": request.operation_name,
            **kwargs,
        }

        subscribe_result = await self._await_if_necessary(
            subscribe(
                self.schema,
                request.document,
                *args,
                **inner_kwargs,
            )
        )

        if isinstance(subscribe_result, ExecutionResult):
            yield subscribe_result

        else:
            async for result in subscribe_result:
                yield result
