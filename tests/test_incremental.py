from typing import Any, AsyncGenerator, Dict, List

import pytest
from graphql import ExecutionResult

from gql import Client, gql
from gql.graphql_request import GraphQLRequest
from gql.incremental import (
    IncrementalDataMerger,
    IncrementalExecutionResult,
    add_incremental_directives,
    payload_to_execution_result,
)
from gql.transport.async_transport import AsyncTransport
from gql.transport.exceptions import TransportConnectionFailed

from .starwars.schema import StarWarsSchema


class PayloadsTransport(AsyncTransport):
    """Async transport returning the provided raw payloads."""

    def __init__(self, payloads: List[Dict[str, Any]]):
        self.payloads = payloads
        self.received_requests: List[GraphQLRequest] = []

    async def connect(self):
        pass

    async def close(self):
        pass

    async def execute(self, request: GraphQLRequest) -> ExecutionResult:
        raise NotImplementedError  # pragma: no cover

    def subscribe(
        self, request: GraphQLRequest
    ) -> AsyncGenerator[ExecutionResult, None]:
        raise NotImplementedError  # pragma: no cover

    async def execute_incremental(
        self, request: GraphQLRequest
    ) -> AsyncGenerator[IncrementalExecutionResult, None]:
        self.received_requests.append(request)
        for payload in self.payloads:
            yield IncrementalExecutionResult.from_payload(payload)


query = gql(
    """
    query {
      hero {
        id
        ... @defer { name }
      }
    }
"""
)


async def run(payloads: List[Dict[str, Any]]) -> List[IncrementalExecutionResult]:
    transport = PayloadsTransport(payloads)

    async with Client(transport=transport) as session:
        return [result async for result in session.execute_incremental(query)]


@pytest.mark.asyncio
async def test_incremental_defer():
    results = await run(
        [
            {"data": {"hero": {"id": "2001"}}, "hasNext": True},
            {
                "incremental": [{"data": {"name": "R2-D2"}, "path": ["hero"]}],
                "hasNext": False,
            },
        ]
    )

    assert len(results) == 2

    assert results[0].data == {"hero": {"id": "2001"}}
    assert results[0].has_next is True
    assert results[0].errors is None

    assert results[1].data == {"hero": {"id": "2001", "name": "R2-D2"}}
    assert results[1].has_next is False
    assert results[1].incremental == [{"data": {"name": "R2-D2"}, "path": ["hero"]}]


@pytest.mark.asyncio
async def test_incremental_results_are_snapshots():
    results = await run(
        [
            {"data": {"hero": {"id": "2001"}}, "hasNext": True},
            {
                "incremental": [{"data": {"name": "R2-D2"}, "path": ["hero"]}],
                "hasNext": False,
            },
        ]
    )

    # The first result must not be modified by the merge of the second payload
    assert results[0].data == {"hero": {"id": "2001"}}


@pytest.mark.asyncio
async def test_incremental_stream():
    results = await run(
        [
            {
                "data": {"hero": {"friends": [{"name": "Luke"}]}},
                "hasNext": True,
            },
            {
                "incremental": [
                    {"items": [{"name": "Han"}], "path": ["hero", "friends", 1]}
                ],
                "hasNext": True,
            },
            {
                "incremental": [
                    {
                        "items": [{"name": "Leia"}, {"name": "C-3PO"}],
                        "path": ["hero", "friends", 2],
                        "label": "friends",
                    }
                ],
                "hasNext": False,
            },
        ]
    )

    assert results[1].data == {"hero": {"friends": [{"name": "Luke"}, {"name": "Han"}]}}
    assert results[2].data == {
        "hero": {
            "friends": [
                {"name": "Luke"},
                {"name": "Han"},
                {"name": "Leia"},
                {"name": "C-3PO"},
            ]
        }
    }


@pytest.mark.asyncio
async def test_incremental_stream_with_gap_and_overwrite():
    results = await run(
        [
            {"data": {"list": [1, 2]}, "hasNext": True},
            {"incremental": [{"items": [5], "path": ["list", 4]}], "hasNext": True},
            {
                "incremental": [{"items": [20, 3], "path": ["list", 1]}],
                "hasNext": False,
            },
        ]
    )

    assert results[1].data == {"list": [1, 2, None, None, 5]}
    assert results[2].data == {"list": [1, 20, 3, None, 5]}


@pytest.mark.asyncio
async def test_incremental_nested_path_through_list():
    results = await run(
        [
            {
                "data": {"hero": {"friends": [{"id": "1000"}, {"id": "1002"}]}},
                "hasNext": True,
            },
            {
                "incremental": [
                    {"data": {"name": "Han"}, "path": ["hero", "friends", 1]},
                    {"data": {"name": "Luke"}, "path": ["hero", "friends", 0]},
                ],
                "hasNext": False,
            },
        ]
    )

    assert results[1].data == {
        "hero": {
            "friends": [
                {"id": "1000", "name": "Luke"},
                {"id": "1002", "name": "Han"},
            ]
        }
    }


@pytest.mark.asyncio
async def test_incremental_deep_merge_null_values_and_overwrites():
    results = await run(
        [
            {
                "data": {
                    "hero": {
                        "id": "2001",
                        "name": "old name",
                        "details": {"a": 1},
                        "friends": [{"id": "1"}, {"id": "2"}],
                    }
                },
                "hasNext": True,
            },
            {
                "incremental": [
                    {
                        "data": {
                            "name": "R2-D2",
                            "primaryFunction": None,
                            "details": {"b": 2},
                            "friends": [{"name": "Luke"}, {"name": "Han"}],
                        },
                        "path": ["hero"],
                    }
                ],
                "hasNext": False,
            },
        ]
    )

    assert results[1].data == {
        "hero": {
            "id": "2001",
            "name": "R2-D2",
            "primaryFunction": None,
            "details": {"a": 1, "b": 2},
            "friends": [{"id": "1", "name": "Luke"}, {"id": "2", "name": "Han"}],
        }
    }


@pytest.mark.asyncio
async def test_incremental_without_path_is_root_merge():
    results = await run(
        [
            {"data": {"hero": {"id": "2001"}}, "hasNext": True},
            {"incremental": [{"data": {"version": 3}}], "hasNext": False},
        ]
    )

    assert results[1].data == {"hero": {"id": "2001"}, "version": 3}


@pytest.mark.asyncio
async def test_incremental_root_merge_without_initial_data():
    results = await run(
        [
            {"hasNext": True},
            {"incremental": [{"data": {"version": 3}, "path": []}], "hasNext": False},
        ]
    )

    assert results[0].data is None
    assert results[1].data == {"version": 3}


@pytest.mark.asyncio
async def test_incremental_concurrent_defer_and_stream():
    results = await run(
        [
            {
                "data": {"hero": {"id": "2001", "friends": []}},
                "hasNext": True,
            },
            {
                "incremental": [
                    {"data": {"name": "R2-D2"}, "path": ["hero"], "label": "name"},
                    {"items": [{"name": "Luke"}], "path": ["hero", "friends", 0]},
                ],
                "hasNext": True,
            },
            {
                "incremental": [
                    {"items": [{"name": "Han"}], "path": ["hero", "friends", 1]},
                    {
                        "data": {"appearsIn": ["NEWHOPE"]},
                        "path": ["hero"],
                        "label": "appears",
                    },
                ],
                "hasNext": False,
            },
        ]
    )

    assert results[2].data == {
        "hero": {
            "id": "2001",
            "name": "R2-D2",
            "friends": [{"name": "Luke"}, {"name": "Han"}],
            "appearsIn": ["NEWHOPE"],
        }
    }


@pytest.mark.asyncio
async def test_incremental_errors_do_not_halt_subsequent_items():
    error1 = {"message": "name error", "path": ["hero", "name"]}
    error2 = {"message": "friend error", "path": ["hero", "friends", 1]}

    results = await run(
        [
            {"data": {"hero": {"id": "2001", "friends": [{"name": "Luke"}]}}},
            {
                "incremental": [
                    {"data": None, "path": ["hero"], "errors": [error1]},
                    {"items": None, "path": ["hero", "friends", 1], "errors": [error2]},
                    {"data": {"appearsIn": ["JEDI"]}, "path": ["hero"]},
                ],
                "hasNext": True,
            },
            {
                "incremental": [
                    {"items": [{"name": "Han"}], "path": ["hero", "friends", 1]}
                ],
                "hasNext": False,
            },
        ]
    )

    assert len(results) == 3

    assert results[0].has_next is False
    assert results[1].errors == [error1, error2]
    assert results[1].data == {
        "hero": {"id": "2001", "friends": [{"name": "Luke"}], "appearsIn": ["JEDI"]}
    }

    assert results[2].errors is None
    assert results[2].data == {
        "hero": {
            "id": "2001",
            "friends": [{"name": "Luke"}, {"name": "Han"}],
            "appearsIn": ["JEDI"],
        }
    }


@pytest.mark.asyncio
async def test_incremental_top_level_errors_are_kept():
    error = {"message": "top level error"}

    results = await run(
        [
            {"data": {"hero": None}, "errors": [error], "hasNext": True},
            {
                "incremental": [{"data": {"name": "R2-D2"}, "path": ["hero"]}],
                "hasNext": False,
            },
        ]
    )

    assert results[0].errors == [error]
    assert results[1].errors is None

    # Deferred data cannot be merged in a null parent
    assert results[1].data == {"hero": None}


@pytest.mark.asyncio
async def test_incremental_extensions_are_per_payload():
    results = await run(
        [
            {"data": {"hero": {"id": "2001"}}, "hasNext": True, "extensions": {"a": 1}},
            {"incremental": [], "hasNext": True},
            {
                "incremental": [{"data": {"name": "R2-D2"}, "path": ["hero"]}],
                "extensions": {"b": 2},
                "hasNext": False,
            },
        ]
    )

    assert [r.extensions for r in results] == [{"a": 1}, None, {"b": 2}]


@pytest.mark.asyncio
async def test_incremental_empty_and_has_next_only_payloads():
    results = await run(
        [
            {"data": {"hero": {"id": "2001"}}, "hasNext": True},
            {"incremental": [], "hasNext": True},
            {
                "incremental": [{"data": {"name": "R2-D2"}, "path": ["hero"]}],
                "hasNext": True,
            },
            {"hasNext": False},
        ]
    )

    assert len(results) == 4
    assert [r.has_next for r in results] == [True, True, True, False]
    assert results[1].data == {"hero": {"id": "2001"}}
    assert results[3].data == {"hero": {"id": "2001", "name": "R2-D2"}}


@pytest.mark.asyncio
async def test_incremental_non_incremental_response():
    results = await run([{"data": {"hero": {"id": "2001", "name": "R2-D2"}}}])

    assert len(results) == 1
    assert results[0].data == {"hero": {"id": "2001", "name": "R2-D2"}}
    assert results[0].has_next is False
    assert results[0].errors is None
    assert results[0].incremental is None


@pytest.mark.asyncio
async def test_incremental_invalid_items_are_ignored():
    results = await run(
        [
            {"data": {"hero": {"id": "2001", "name": "R2-D2"}}, "hasNext": True},
            {
                "incremental": [
                    "not a dict",
                    {"items": [1], "path": []},
                    {"items": [1], "path": ["hero", "name"]},
                    {"items": [1], "path": ["hero", "unknown", 0]},
                    {"data": {"x": 1}, "path": ["hero", "name"]},
                    {"data": {"x": 1}, "path": ["hero", 0]},
                    {"data": {"x": 1}, "path": ["hero", "unknown", "deep"]},
                ],
                "hasNext": False,
            },
        ]
    )

    assert results[1].data == {"hero": {"id": "2001", "name": "R2-D2"}}


@pytest.mark.asyncio
async def test_incremental_validates_with_schema():
    transport = PayloadsTransport([{"data": {"hero": {"id": "2001"}}}])

    client = Client(transport=transport, schema=StarWarsSchema)

    async with client as session:
        with pytest.raises(Exception) as exc_info:
            async for _ in session.execute_incremental(gql("{ hero { unknown } }")):
                pass  # pragma: no cover

        assert "Cannot query field 'unknown'" in str(exc_info.value)

    assert transport.received_requests == []


@pytest.mark.asyncio
async def test_incremental_validation_allows_defer_and_stream():
    transport = PayloadsTransport([{"data": {"hero": {"id": "2001"}}}])

    client = Client(transport=transport, schema=StarWarsSchema)

    request = gql(
        """
        query {
          hero {
            id
            ... @defer(label: "name") { name }
            friends @stream(initialCount: 1) { name }
          }
        }
    """
    )

    async with client as session:
        results = [r async for r in session.execute_incremental(request)]

        # Regular execution still validates against the client schema only
        with pytest.raises(Exception, match="Unknown directive '@defer'"):
            await session.execute(request)

    assert len(results) == 1
    assert "@defer" not in {d.name for d in StarWarsSchema.directives}


def test_add_incremental_directives():
    schema = add_incremental_directives(StarWarsSchema)

    assert schema is not StarWarsSchema
    assert {"defer", "stream"} <= {d.name for d in schema.directives}
    assert schema.query_type is StarWarsSchema.query_type

    # No copy if the directives are already present
    assert add_incremental_directives(schema) is schema


@pytest.mark.asyncio
async def test_incremental_validation_schema_is_cached():
    transport = PayloadsTransport([{"data": {"hero": {"id": "2001"}}}])

    client = Client(transport=transport, schema=StarWarsSchema)

    async with client as session:
        async for _ in session.execute_incremental(query):
            pass

        assert client._incremental_schema is not None
        cached_schema = client._incremental_schema[1]

        async for _ in session.execute_incremental(query):
            pass

    assert client._incremental_schema[1] is cached_schema


@pytest.mark.asyncio
async def test_incremental_serialize_variables():
    transport = PayloadsTransport([{"data": {"hero": {"id": "2001"}}}])

    client = Client(transport=transport, schema=StarWarsSchema)

    request = gql(
        """
        query ($episode: Episode) {
          hero(episode: $episode) {
            id
            ... @defer { name }
          }
        }
    """
    )
    request.variable_values = {"episode": 5}

    async with client as session:
        async for _ in session.execute_incremental(request, serialize_variables=True):
            pass

    assert transport.received_requests[0].variable_values == {"episode": "EMPIRE"}


@pytest.mark.asyncio
async def test_incremental_subsequent_payload_with_data():
    results = await run(
        [
            {"data": {"hero": {"id": "2001"}}, "hasNext": True},
            {"data": {"hero": {"name": "R2-D2"}}, "hasNext": False},
        ]
    )

    assert results[1].data == {"hero": {"id": "2001", "name": "R2-D2"}}


@pytest.mark.asyncio
async def test_incremental_not_supported_by_transport():
    client = Client(schema=StarWarsSchema)

    async with client as session:
        with pytest.raises(NotImplementedError) as exc_info:
            async for _ in session.execute_incremental(gql("{ hero { id } }")):
                pass  # pragma: no cover

    assert "execute_incremental" in str(exc_info.value)


@pytest.mark.asyncio
async def test_incremental_reconnecting_session_requests_reconnect():
    class FailingTransport(PayloadsTransport):
        async def execute_incremental(self, request):
            yield IncrementalExecutionResult.from_payload(self.payloads[0])
            raise TransportConnectionFailed("connection lost")

    transport = FailingTransport([{"data": {"hero": {"id": "2001"}}, "hasNext": True}])
    client = Client(transport=transport)

    session = await client.connect_async(reconnecting=True)

    try:
        results = []
        with pytest.raises(TransportConnectionFailed):
            async for result in session.execute_incremental(query):
                results.append(result)

        assert len(results) == 1
        assert session._reconnect_request_event.is_set()
    finally:
        await client.close_async()


def test_incremental_merger_directly():
    merger = IncrementalDataMerger()

    first = merger.merge(
        IncrementalExecutionResult(data={"a": {"b": 1}}, has_next=True)
    )
    second = merger.merge(
        IncrementalExecutionResult(
            incremental=[{"data": {"c": 2}, "path": ["a"]}], has_next=False
        )
    )

    assert first.data == {"a": {"b": 1}}
    assert second.data == {"a": {"b": 1, "c": 2}}
    assert merger.data == {"a": {"b": 1, "c": 2}}


def test_payload_to_execution_result():
    result = payload_to_execution_result({"data": {"a": 1}})
    assert type(result) is ExecutionResult
    assert result.data == {"a": 1}

    result = payload_to_execution_result({"data": {"a": 1}, "hasNext": True})
    assert isinstance(result, IncrementalExecutionResult)
    assert result.has_next is True

    result = payload_to_execution_result({"incremental": [], "hasNext": False})
    assert isinstance(result, IncrementalExecutionResult)
    assert result.data is None
    assert result.incremental == []

    assert "has_next=False" in repr(result)
