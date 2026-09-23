import asyncio
from typing import Any, Dict, List, Union

import pytest
from graphql import ExecutionResult, GraphQLError

from gql import Client, GraphQLRequest, gql
from gql.incremental import (
    IncrementalExecutionResult,
    IncrementalResultAccumulator,
    is_incremental_payload,
)
from gql.transport import AsyncTransport
from gql.transport.exceptions import TransportConnectionFailed

from .starwars.schema import StarWarsSchema

hero_query = gql(
    """
    query HeroQuery {
      hero {
        id
        ... @defer(label: "HeroName") {
          name
        }
        friends @stream(initialCount: 1) {
          name
        }
      }
    }
    """
)


class IncrementalTransport(AsyncTransport):
    """Fake transport yielding the provided payloads for incremental requests."""

    def __init__(self, payloads: List[Union[Dict[str, Any], ExecutionResult]]):
        self.payloads = payloads
        self.requests: List[GraphQLRequest] = []
        self.kwargs: List[Dict[str, Any]] = []
        self.closed_generators = 0
        self.connect_count = 0
        self.fail_after = None

    async def connect(self):
        self.connect_count += 1

    async def close(self):
        pass

    async def execute(self, request, *args, **kwargs):
        raise NotImplementedError  # pragma: no cover

    def subscribe(self, request, *args, **kwargs):
        raise NotImplementedError  # pragma: no cover

    async def execute_incremental(self, request, **kwargs):
        self.requests.append(request)
        self.kwargs.append(kwargs)

        try:
            for index, payload in enumerate(self.payloads):
                if self.fail_after is not None and index == self.fail_after:
                    raise TransportConnectionFailed("connection lost")

                if isinstance(payload, ExecutionResult):
                    yield payload
                else:
                    yield IncrementalExecutionResult.from_payload(payload)
        finally:
            self.closed_generators += 1


async def collect_results(payloads, query=hero_query, **client_args):
    transport = IncrementalTransport(payloads)

    async with Client(transport=transport, **client_args) as session:
        return [result async for result in session.execute_incremental(query)]


@pytest.mark.asyncio
async def test_incremental_defer_and_stream():

    results = await collect_results(
        [
            {
                "data": {"hero": {"id": "2001", "friends": [{"name": "Luke"}]}},
                "hasNext": True,
            },
            {
                "incremental": [
                    {
                        "data": {"name": "R2-D2"},
                        "path": ["hero"],
                        "label": "HeroName",
                    },
                    {"items": [{"name": "Han"}], "path": ["hero", "friends", 1]},
                ],
                "hasNext": True,
            },
            {
                "incremental": [
                    {"items": [{"name": "Leia"}], "path": ["hero", "friends", 2]}
                ],
                "hasNext": False,
            },
        ]
    )

    assert len(results) == 3
    assert all(isinstance(result, IncrementalExecutionResult) for result in results)
    assert [result.has_next for result in results] == [True, True, False]
    assert all(result.errors is None for result in results)

    # The data of each result is a snapshot of the data accumulated so far
    assert results[0].data == {"hero": {"id": "2001", "friends": [{"name": "Luke"}]}}
    assert results[1].data == {
        "hero": {
            "id": "2001",
            "name": "R2-D2",
            "friends": [{"name": "Luke"}, {"name": "Han"}],
        }
    }
    assert results[2].data == {
        "hero": {
            "id": "2001",
            "name": "R2-D2",
            "friends": [{"name": "Luke"}, {"name": "Han"}, {"name": "Leia"}],
        }
    }

    assert results[0].incremental is None
    assert results[1].incremental is not None
    assert results[1].incremental[0]["label"] == "HeroName"


@pytest.mark.asyncio
async def test_incremental_item_without_path_is_merged_at_root():

    results = await collect_results(
        [
            {"data": {"hero": {"id": "2001"}}, "hasNext": True},
            {
                "incremental": [{"data": {"human": {"name": "Luke"}}}],
                "hasNext": False,
            },
        ]
    )

    assert results[-1].data == {"hero": {"id": "2001"}, "human": {"name": "Luke"}}


@pytest.mark.asyncio
async def test_incremental_nested_paths_through_lists():

    results = await collect_results(
        [
            {
                "data": {
                    "hero": {
                        "friends": [
                            {"id": "1000", "friends": [{"id": "1002"}]},
                            {"id": "1003", "friends": [{"id": "1000"}]},
                        ]
                    }
                },
                "hasNext": True,
            },
            {
                "incremental": [
                    {
                        "data": {"name": "Han"},
                        "path": ["hero", "friends", 0, "friends", 0],
                    },
                    {"data": {"name": "Leia"}, "path": ["hero", "friends", 1]},
                    {
                        "items": [{"id": "1004"}],
                        "path": ["hero", "friends", 1, "friends", 1],
                    },
                ],
                "hasNext": False,
            },
        ]
    )

    assert results[-1].data == {
        "hero": {
            "friends": [
                {"id": "1000", "friends": [{"id": "1002", "name": "Han"}]},
                {
                    "id": "1003",
                    "name": "Leia",
                    "friends": [{"id": "1000"}, {"id": "1004"}],
                },
            ]
        }
    }


@pytest.mark.asyncio
async def test_incremental_null_values():

    results = await collect_results(
        [
            {
                "data": {"hero": {"id": "2001", "friends": []}, "droid": None},
                "hasNext": True,
            },
            {
                "incremental": [
                    # null fields in deferred data
                    {"data": {"name": None}, "path": ["hero"]},
                    # null deferred data because of an error
                    {
                        "data": None,
                        "path": ["hero"],
                        "errors": [{"message": "deferred error"}],
                    },
                    # null parent object: nothing to merge
                    {"data": {"name": "R2-D2"}, "path": ["droid"]},
                    # path through a missing list index
                    {"data": {"name": "Luke"}, "path": ["hero", "friends", 3]},
                ],
                "hasNext": True,
            },
            {
                "incremental": [
                    # null items because of an error
                    {
                        "items": None,
                        "path": ["hero", "friends", 0],
                        "errors": [{"message": "stream error"}],
                    }
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

    assert results[1].data == {
        "hero": {"id": "2001", "name": None, "friends": []},
        "droid": None,
    }
    assert results[1].errors == [{"message": "deferred error"}]
    assert results[2].errors == [{"message": "stream error"}]
    assert results[3].errors is None

    # The item which failed is null in the list
    assert results[3].data == {
        "hero": {"id": "2001", "name": None, "friends": [None, {"name": "Han"}]},
        "droid": None,
    }


@pytest.mark.asyncio
async def test_incremental_field_overwrites_and_deep_merge():

    results = await collect_results(
        [
            {
                "data": {
                    "hero": {
                        "name": "old name",
                        "friend": {"id": "1000"},
                        "friends": [{"id": "1002"}, {"id": "1003"}],
                        "appearsIn": ["NEWHOPE"],
                    }
                },
                "hasNext": True,
            },
            {
                "incremental": [
                    {
                        "data": {
                            "name": "new name",
                            "friend": {"name": "Luke"},
                            "friends": [{"name": "Han"}, {"name": "Leia"}],
                            "appearsIn": ["NEWHOPE", "EMPIRE"],
                        },
                        "path": ["hero"],
                    }
                ],
                "hasNext": False,
            },
        ]
    )

    assert results[-1].data == {
        "hero": {
            "name": "new name",
            "friend": {"id": "1000", "name": "Luke"},
            "friends": [{"id": "1002", "name": "Han"}, {"id": "1003", "name": "Leia"}],
            "appearsIn": ["NEWHOPE", "EMPIRE"],
        }
    }


@pytest.mark.asyncio
async def test_incremental_concurrent_deferred_and_streamed_fields():

    results = await collect_results(
        [
            {
                "data": {
                    "hero": {"id": "2001", "friends": []},
                    "characters": [{"id": "1000"}],
                },
                "hasNext": True,
            },
            {
                "incremental": [
                    {"items": [{"name": "Luke"}], "path": ["hero", "friends", 0]},
                    {"data": {"name": "R2-D2"}, "path": ["hero"], "label": "A"},
                    {"items": [{"id": "1002"}], "path": ["characters", 1]},
                    {"data": {"name": "Luke"}, "path": ["characters", 0]},
                    {"items": [{"name": "Han"}], "path": ["hero", "friends", 1]},
                    {"data": {"id": "2001"}, "path": ["hero"], "label": "B"},
                ],
                "hasNext": False,
            },
        ]
    )

    assert results[-1].data == {
        "hero": {
            "id": "2001",
            "name": "R2-D2",
            "friends": [{"name": "Luke"}, {"name": "Han"}],
        },
        "characters": [{"id": "1000", "name": "Luke"}, {"id": "1002"}],
    }


@pytest.mark.asyncio
async def test_incremental_errors_do_not_halt_subsequent_items():

    results = await collect_results(
        [
            {
                "data": {"hero": {"id": "2001", "friends": [{"name": "Luke"}]}},
                "errors": [{"message": "initial error"}],
                "hasNext": True,
            },
            {
                "incremental": [
                    {
                        "data": None,
                        "path": ["hero"],
                        "errors": [{"message": "error 1", "path": ["hero", "name"]}],
                    },
                    {"items": [{"name": "Han"}], "path": ["hero", "friends", 1]},
                ],
                "errors": [{"message": "error 2"}],
                "hasNext": True,
            },
            {
                "incremental": [
                    {"data": {"name": "R2-D2"}, "path": ["hero"]},
                ],
                "hasNext": False,
            },
        ]
    )

    assert len(results) == 3

    assert results[0].errors == [{"message": "initial error"}]
    assert results[1].errors == [
        {"message": "error 2"},
        {"message": "error 1", "path": ["hero", "name"]},
    ]
    assert results[2].errors is None

    assert results[2].data == {
        "hero": {
            "id": "2001",
            "name": "R2-D2",
            "friends": [{"name": "Luke"}, {"name": "Han"}],
        }
    }


@pytest.mark.asyncio
async def test_incremental_extensions_are_not_accumulated():

    results = await collect_results(
        [
            {"data": {"hero": {"id": "2001"}}, "hasNext": True, "extensions": {"a": 1}},
            {
                "incremental": [
                    {
                        "data": {"name": "R2-D2"},
                        "path": ["hero"],
                        "extensions": {"c": 3},
                    }
                ],
                "hasNext": True,
            },
            {"incremental": [], "hasNext": False, "extensions": {"b": 2}},
        ]
    )

    assert [result.extensions for result in results] == [{"a": 1}, None, {"b": 2}]


@pytest.mark.asyncio
async def test_incremental_empty_incremental_and_has_next_only_payloads():

    results = await collect_results(
        [
            {"data": {"hero": {"id": "2001"}}, "hasNext": True},
            {"incremental": [], "hasNext": True},
            {"hasNext": True},
            {
                "incremental": [{"data": {"name": "R2-D2"}, "path": ["hero"]}],
                "hasNext": True,
            },
            {"hasNext": False},
        ]
    )

    assert len(results) == 5
    assert [result.has_next for result in results] == [True, True, True, True, False]
    assert results[1].data == results[2].data == {"hero": {"id": "2001"}}
    assert results[1].incremental == []
    assert results[2].incremental is None
    assert results[4].data == {"hero": {"id": "2001", "name": "R2-D2"}}


@pytest.mark.asyncio
async def test_incremental_has_next_only_first_payload():

    results = await collect_results(
        [
            {"hasNext": True},
            {"incremental": [{"data": {"hero": {"id": "2001"}}}], "hasNext": False},
        ]
    )

    assert [result.data for result in results] == [None, {"hero": {"id": "2001"}}]


@pytest.mark.asyncio
async def test_incremental_non_incremental_response():

    results = await collect_results(
        [ExecutionResult(data={"hero": {"id": "2001"}}, extensions={"a": 1})]
    )

    assert len(results) == 1
    assert results[0].data == {"hero": {"id": "2001"}}
    assert results[0].extensions == {"a": 1}
    assert results[0].has_next is False
    assert results[0].errors is None


@pytest.mark.asyncio
async def test_incremental_non_incremental_response_with_errors():

    results = await collect_results(
        [
            ExecutionResult(
                data=None, errors=[{"message": "error"}]  # type: ignore[list-item]
            )
        ]
    )

    assert len(results) == 1
    assert results[0].data is None
    assert results[0].errors == [{"message": "error"}]
    assert results[0].has_next is False


@pytest.mark.asyncio
async def test_incremental_local_schema_transport_fallback():
    """LocalSchemaTransport does not support incremental delivery:
    the request is executed normally and a single result is yielded."""

    client = Client(schema=StarWarsSchema)

    query = gql("{ hero { id name } }")

    async with client as session:
        results = [result async for result in session.execute_incremental(query)]

    assert len(results) == 1
    assert results[0].data == {"hero": {"id": "2001", "name": "R2-D2"}}
    assert results[0].has_next is False


@pytest.mark.asyncio
async def test_incremental_validation_with_schema_without_defer_and_stream():

    assert StarWarsSchema.get_directive("defer") is None
    assert StarWarsSchema.get_directive("stream") is None

    transport = IncrementalTransport(
        [{"data": {"hero": {"id": "2001", "friends": []}}, "hasNext": False}]
    )
    client = Client(transport=transport, schema=StarWarsSchema)

    async with client as session:
        results = [r async for r in session.execute_incremental(hero_query)]
        assert results[0].data == {"hero": {"id": "2001", "friends": []}}

        # The schema with the @defer and @stream directives is cached
        assert client._incremental_schema is not None
        incremental_schema = client._incremental_schema[1]
        assert incremental_schema.get_directive("defer") is not None
        assert incremental_schema.get_directive("stream") is not None
        assert StarWarsSchema.get_directive("defer") is None

        _ = [r async for r in session.execute_incremental(hero_query)]
        assert client._incremental_schema[1] is incremental_schema

        # Invalid queries are still rejected before being sent
        invalid_query = gql("{ hero { id ... @defer(invalid: true) { name } } }")
        with pytest.raises(GraphQLError):
            async for _ in session.execute_incremental(invalid_query):
                pass  # pragma: no cover

        invalid_query = gql("{ hero { unknownField @stream } }")
        with pytest.raises(GraphQLError):
            async for _ in session.execute_incremental(invalid_query):
                pass  # pragma: no cover

    assert len(transport.requests) == 2


@pytest.mark.asyncio
async def test_incremental_validation_with_schema_with_defer_and_stream():
    from graphql import GraphQLDeferDirective, GraphQLSchema, GraphQLStreamDirective

    schema_kwargs = StarWarsSchema.to_kwargs()
    schema_kwargs["directives"] = (
        *schema_kwargs["directives"],
        GraphQLDeferDirective,
        GraphQLStreamDirective,
    )
    schema = GraphQLSchema(**schema_kwargs)

    transport = IncrementalTransport(
        [{"data": {"hero": {"id": "2001", "friends": []}}, "hasNext": False}]
    )
    client = Client(transport=transport, schema=schema)

    async with client as session:
        results = [r async for r in session.execute_incremental(hero_query)]

    assert len(results) == 1
    assert client._incremental_schema is None


@pytest.mark.asyncio
async def test_incremental_parse_result():

    query = gql(
        """
        query {
          hero {
            id
            ... @defer {
              appearsIn
            }
          }
        }
        """
    )

    payloads = [
        {"data": {"hero": {"id": "2001"}}, "hasNext": True},
        {
            "incremental": [
                {"data": {"appearsIn": ["NEWHOPE", "EMPIRE"]}, "path": ["hero"]}
            ],
            "hasNext": False,
        },
    ]

    results = await collect_results(
        payloads, query=query, schema=StarWarsSchema, parse_results=True
    )

    assert results[0].data == {"hero": {"id": "2001"}}
    assert results[1].data == {"hero": {"id": "2001", "appearsIn": [4, 5]}}

    transport = IncrementalTransport(payloads)
    async with Client(transport=transport, schema=StarWarsSchema) as session:
        results = [
            result
            async for result in session.execute_incremental(query, parse_result=True)
        ]

    assert results[1].data == {"hero": {"id": "2001", "appearsIn": [4, 5]}}


@pytest.mark.asyncio
async def test_incremental_serialize_variables_and_transport_kwargs():

    query = gql(
        """
        query ($episode: Episode) {
          hero(episode: $episode) {
            id
          }
        }
        """
    )
    query.variable_values = {"episode": 5}

    transport = IncrementalTransport([{"data": {"hero": {"id": "1000"}}}])

    async with Client(transport=transport, schema=StarWarsSchema) as session:
        results = [
            result
            async for result in session.execute_incremental(
                query, serialize_variables=True, extra_args={"a": 1}
            )
        ]

    assert results[0].data == {"hero": {"id": "1000"}}
    assert transport.requests[0].variable_values == {"episode": "EMPIRE"}
    assert transport.kwargs[0] == {"extra_args": {"a": 1}}


@pytest.mark.asyncio
async def test_incremental_break_closes_transport_generator():

    transport = IncrementalTransport(
        [
            {"data": {"hero": {"id": "2001"}}, "hasNext": True},
            {"hasNext": False},
        ]
    )

    async with Client(transport=transport) as session:
        generator = session.execute_incremental(hero_query)
        async for result in generator:
            assert result.has_next
            break
        await generator.aclose()

    assert transport.closed_generators == 1


@pytest.mark.asyncio
async def test_incremental_reconnecting_session():

    transport = IncrementalTransport(
        [
            {"data": {"hero": {"id": "2001"}}, "hasNext": True},
            {"hasNext": False},
        ]
    )
    transport.fail_after = 1

    client = Client(transport=transport)
    session = await client.connect_async(reconnecting=True, retry_execute=False)

    try:
        assert transport.connect_count == 1

        results = []
        with pytest.raises(TransportConnectionFailed):
            async for result in session.execute_incremental(hero_query):
                results.append(result)

        assert len(results) == 1

        # A reconnection has been requested
        for _ in range(100):
            if transport.connect_count == 2:
                break
            await asyncio.sleep(0.01)
        assert transport.connect_count == 2

    finally:
        await client.close_async()


def test_incremental_execution_result():

    result = IncrementalExecutionResult.from_payload(
        {
            "incremental": [{"data": {"a": 1}, "path": []}],
            "hasNext": True,
            "extensions": {"b": 2},
        }
    )

    assert result.data is None
    assert result.errors is None
    assert result.extensions == {"b": 2}
    assert result.has_next is True
    assert result.incremental == [{"data": {"a": 1}, "path": []}]
    assert isinstance(result, ExecutionResult)

    assert repr(result) == (
        "IncrementalExecutionResult(data=None, errors=None, "
        "extensions={'b': 2}, has_next=True)"
    )
    assert repr(IncrementalExecutionResult(data={"a": 1})) == (
        "IncrementalExecutionResult(data={'a': 1}, errors=None, has_next=False)"
    )

    assert IncrementalExecutionResult(data={"a": 1}) == {"data": {"a": 1}}
    assert IncrementalExecutionResult(data={"a": 1}, has_next=True) == {
        "data": {"a": 1},
        "hasNext": True,
    }
    assert IncrementalExecutionResult(data={"a": 1}) != {
        "data": {"a": 1},
        "hasNext": True,
    }
    assert IncrementalExecutionResult(data={"a": 1}) == IncrementalExecutionResult(
        data={"a": 1}
    )
    assert IncrementalExecutionResult(data={"a": 1}) != IncrementalExecutionResult(
        data={"a": 1}, has_next=True
    )
    assert IncrementalExecutionResult(data={"a": 1}) == ({"a": 1}, None)


def test_is_incremental_payload():
    assert is_incremental_payload({"data": {}, "hasNext": True})
    assert is_incremental_payload({"incremental": []})
    assert is_incremental_payload({"hasNext": False})
    assert not is_incremental_payload({"data": {}})
    assert not is_incremental_payload({"errors": []})


def test_incremental_accumulator_does_not_modify_payloads():

    initial = {"hero": {"id": "2001", "friends": [{"name": "Luke"}]}}
    deferred = {"friend": {"name": "Han"}}
    items = [{"name": "Leia"}]

    accumulator = IncrementalResultAccumulator()

    result = accumulator.add(IncrementalExecutionResult(data=initial, has_next=True))
    assert result.data is not None
    result.data["hero"]["id"] = "modified"

    accumulator.add(
        IncrementalExecutionResult(
            incremental=[
                {"data": deferred, "path": ["hero"]},
                {"items": items, "path": ["hero", "friends", 1]},
            ]
        )
    )

    assert accumulator.data is not None
    accumulator.data["hero"]["friend"]["name"] = "modified"
    accumulator.data["hero"]["friends"][1]["name"] = "modified"

    assert initial == {"hero": {"id": "2001", "friends": [{"name": "Luke"}]}}
    assert deferred == {"friend": {"name": "Han"}}
    assert items == [{"name": "Leia"}]


def test_incremental_accumulator_top_level_data_in_subsequent_payload():

    accumulator = IncrementalResultAccumulator()

    accumulator.add(IncrementalExecutionResult(data={"hero": {"id": "2001"}}))
    result = accumulator.add(
        IncrementalExecutionResult(data={"hero": {"name": "R2-D2"}})
    )

    assert result.data == {"hero": {"id": "2001", "name": "R2-D2"}}


def test_incremental_accumulator_stream_path_without_index():

    accumulator = IncrementalResultAccumulator()

    accumulator.add(IncrementalExecutionResult(data={"friends": [{"name": "Luke"}]}))
    result = accumulator.add(
        IncrementalExecutionResult(
            incremental=[{"items": [{"name": "Han"}], "path": ["friends"]}]
        )
    )

    assert result.data == {"friends": [{"name": "Luke"}, {"name": "Han"}]}


def test_incremental_accumulator_invalid_items(caplog):

    accumulator = IncrementalResultAccumulator()

    initial_data = {"hero": {"id": "2001", "friends": [{"name": "Luke"}]}}

    accumulator.add(IncrementalExecutionResult(data=initial_data, has_next=True))

    invalid_items: List[Any] = [
        "not a dict",
        {"data": {"name": "R2-D2"}, "path": "hero"},
        {"data": ["not a dict"], "path": ["hero"]},
        {"data": {"name": "R2-D2"}, "path": ["hero", "id"]},
        {"data": {"name": "R2-D2"}, "path": ["hero", "friends", "0"]},
        {"data": {"name": "R2-D2"}, "path": ["hero", "friends", -1]},
        {"items": {"name": "Han"}, "path": ["hero", "friends", 1]},
        {"items": [{"name": "Han"}], "path": ["hero", 1]},
        {"items": [{"name": "Han"}], "path": ["hero", "friends", -1]},
        {"items": [{"name": "Han"}]},
    ]

    result = accumulator.add(
        IncrementalExecutionResult(incremental=invalid_items, has_next=True)
    )

    assert result.data == initial_data
    assert len(caplog.records) == len(invalid_items)

    result = accumulator.add(
        IncrementalExecutionResult(
            incremental={"not": "a list"}, has_next=False  # type: ignore[arg-type]
        )
    )

    assert result.data == initial_data
    assert "Ignoring invalid incremental field" in caplog.records[-1].getMessage()
