import asyncio
import json

import pytest
from graphql import print_ast

from gql import Client, gql
from gql.incremental import IncrementalResult, IncrementalResultAccumulator

from .conftest import WebSocketServerHelper
from .starwars.schema import StarWarsSchema

query_str = """
    query {
      hero {
        id
        ... @defer(label: "details") {
          name
        }
        friends @stream(initialCount: 1) {
          name
        }
      }
    }
"""

payloads = [
    {
        "data": {"hero": {"id": "2001", "friends": [{"name": "Luke"}]}},
        "hasNext": True,
    },
    {
        "incremental": [
            {"data": {"name": "R2-D2"}, "path": ["hero"], "label": "details"},
        ],
        "hasNext": True,
        "extensions": {"cost": 1},
    },
    {
        "incremental": [
            {
                "items": [{"name": "Han"}, {"name": "Leia"}],
                "path": ["hero", "friends", 1],
            }
        ],
        "hasNext": False,
    },
]

expected_results = [
    IncrementalResult(
        data={"hero": {"id": "2001", "friends": [{"name": "Luke"}]}},
        has_next=True,
    ),
    IncrementalResult(
        data={"hero": {"id": "2001", "name": "R2-D2", "friends": [{"name": "Luke"}]}},
        extensions={"cost": 1},
        has_next=True,
    ),
    IncrementalResult(
        data={
            "hero": {
                "id": "2001",
                "name": "R2-D2",
                "friends": [{"name": "Luke"}, {"name": "Han"}, {"name": "Leia"}],
            }
        },
        has_next=False,
    ),
]


def accumulate(*payloads):
    accumulator = IncrementalResultAccumulator()
    return [accumulator.add_payload(payload) for payload in payloads]


def test_incremental_accumulate_defer_and_stream():
    assert accumulate(*payloads) == expected_results


def test_incremental_results_are_snapshots():
    results = accumulate(*payloads)
    assert "name" not in results[0].data["hero"]
    assert len(results[1].data["hero"]["friends"]) == 1


def test_incremental_non_incremental_response():
    [result] = accumulate({"data": {"hero": {"id": "1"}}})
    assert result == IncrementalResult(data={"hero": {"id": "1"}}, has_next=False)


def test_incremental_missing_path_is_root():
    results = accumulate(
        {"data": {"a": 1}, "hasNext": True},
        {"incremental": [{"data": {"b": 2}}], "hasNext": False},
    )
    assert results[1].data == {"a": 1, "b": 2}


def test_incremental_nested_path_through_lists():
    results = accumulate(
        {"data": {"hero": {"friends": [{"id": "1"}, {"id": "2"}]}}, "hasNext": True},
        {
            "incremental": [
                {
                    "data": {"name": "Han", "ship": {"name": "Falcon"}},
                    "path": ["hero", "friends", 1],
                },
                {"data": {"ship": {"speed": 10}}, "path": ["hero", "friends", 1]},
            ],
            "hasNext": False,
        },
    )
    assert results[1].data == {
        "hero": {
            "friends": [
                {"id": "1"},
                {"id": "2", "name": "Han", "ship": {"name": "Falcon", "speed": 10}},
            ]
        }
    }


def test_incremental_null_values_and_overwrites():
    results = accumulate(
        {
            "data": {"hero": {"id": "1", "name": "old", "friends": None}},
            "hasNext": True,
        },
        {
            "incremental": [
                {"data": {"name": "new", "age": None}, "path": ["hero"]},
                # Cannot stream into a null list, must not halt other items
                {"items": [{"id": "2"}], "path": ["hero", "friends", 0]},
                {"data": None, "path": ["hero"]},
                {"data": {"extra": True}, "path": ["hero"]},
            ],
            "hasNext": False,
        },
    )
    assert results[1].data == {
        "hero": {"id": "1", "name": "new", "friends": None, "age": None, "extra": True}
    }


def test_incremental_errors_do_not_halt_items():
    error1 = {"message": "error 1", "path": ["hero", "name"]}
    error2 = {"message": "error 2"}
    results = accumulate(
        {"data": {"hero": {"id": "1"}}, "hasNext": True},
        {
            "incremental": [
                {"data": {"name": None}, "path": ["hero"], "errors": [error1]},
                {"data": {"age": 3}, "path": ["hero"]},
            ],
            "errors": [error2],
            "hasNext": True,
        },
        {"incremental": [{"data": {"x": 1}, "path": ["hero"]}], "hasNext": False},
    )
    assert results[1].errors == [error2, error1]
    assert results[1].data == {"hero": {"id": "1", "name": None, "age": 3}}
    assert results[2].errors is None
    assert results[2].data == {"hero": {"id": "1", "name": None, "age": 3, "x": 1}}


def test_incremental_empty_and_has_next_only_payloads():
    results = accumulate(
        {"data": {"a": 1}, "hasNext": True},
        {"incremental": [], "hasNext": True},
        {"hasNext": False},
    )
    assert len(results) == 3
    assert results[1] == IncrementalResult(data={"a": 1}, has_next=True)
    assert results[2] == IncrementalResult(data={"a": 1}, has_next=False)


def test_incremental_concurrent_defer_and_stream():
    results = accumulate(
        {"data": {"a": {"list": [1]}, "b": {}}, "hasNext": True},
        {
            "incremental": [
                {"items": [2, 3], "path": ["a", "list", 1]},
                {"data": {"x": 1}, "path": ["b"], "label": "b"},
                {"data": {"y": 2}, "path": ["a"], "label": "a"},
                {"items": [4], "path": ["a", "list", 3]},
            ],
            "hasNext": False,
        },
    )
    assert results[1].data == {"a": {"list": [1, 2, 3, 4], "y": 2}, "b": {"x": 1}}


def test_incremental_extensions_are_per_payload():
    results = accumulate(
        {"data": {}, "hasNext": True, "extensions": {"a": 1}},
        {"incremental": [], "hasNext": True, "extensions": {"b": 2}},
        {"hasNext": False},
    )
    assert [r.extensions for r in results] == [{"a": 1}, {"b": 2}, None]


@pytest.mark.asyncio
async def test_incremental_local_schema_fallback():
    client = Client(schema=StarWarsSchema)

    async with client as session:
        results = [
            r async for r in session.execute_incremental(gql("{ hero { id name } }"))
        ]

    assert results == [
        IncrementalResult(data={"hero": {"id": "2001", "name": "R2-D2"}})
    ]


def multipart_body(payloads):
    parts = [
        "--graphql\r\n"
        "Content-Type: application/json; charset=utf-8\r\n"
        "\r\n"
        f"{json.dumps(payload)}\r\n"
        for payload in payloads
    ]
    parts.append("--graphql--\r\n")
    return parts


@pytest.fixture
def incremental_http_server(aiohttp_server):
    from aiohttp import web

    async def create_server(parts, content_type, accept_headers):
        async def handler(request):
            accept_headers.append(request.headers["accept"])
            response = web.StreamResponse()
            response.headers["Content-Type"] = content_type
            response.enable_chunked_encoding()
            await response.prepare(request)
            for part in parts:
                await response.write(part.encode())
                await asyncio.sleep(0)
            await response.write_eof()
            return response

        app = web.Application()
        app.router.add_route("POST", "/", handler)
        return await aiohttp_server(app)

    return create_server


@pytest.mark.aiohttp
@pytest.mark.asyncio
async def test_incremental_aiohttp_multipart(incremental_http_server):
    from gql.transport.aiohttp import AIOHTTPTransport

    accept_headers = []
    server = await incremental_http_server(
        multipart_body(payloads),
        'multipart/mixed; boundary="graphql"; deferSpec=20220824',
        accept_headers,
    )
    transport = AIOHTTPTransport(url=server.make_url("/"))

    async with Client(transport=transport) as session:
        results = [r async for r in session.execute_incremental(gql(query_str))]

    assert results == expected_results
    assert "multipart/mixed" in accept_headers[0]
    assert "boundary=graphql" in accept_headers[0]
    assert "deferSpec=20220824" in accept_headers[0]
    assert "application/json" in accept_headers[0]


@pytest.mark.aiohttp
@pytest.mark.asyncio
async def test_incremental_aiohttp_json_response(incremental_http_server):
    from gql.transport.aiohttp import AIOHTTPTransport

    server = await incremental_http_server(
        [json.dumps({"data": {"hero": {"id": "1"}}})], "application/json", []
    )
    transport = AIOHTTPTransport(url=server.make_url("/"))

    async with Client(transport=transport) as session:
        results = [r async for r in session.execute_incremental(gql(query_str))]

    assert results == [IncrementalResult(data={"hero": {"id": "1"}})]


def ws_incremental_server_factory(next_type):
    async def server(ws):
        await WebSocketServerHelper.send_connection_ack(ws)
        query = json.loads(await ws.recv())
        query_id = query["id"]
        for payload in payloads:
            await ws.send(
                json.dumps({"type": next_type, "id": query_id, "payload": payload})
            )
        await WebSocketServerHelper.send_complete(ws, query_id)
        await ws.wait_closed()

    return server


@pytest.mark.websockets
@pytest.mark.asyncio
@pytest.mark.parametrize(
    "graphqlws_server", [ws_incremental_server_factory("next")], indirect=True
)
async def test_incremental_graphqlws(client_and_graphqlws_server):
    session, _ = client_and_graphqlws_server

    results = [r async for r in session.execute_incremental(gql(query_str))]

    assert results == expected_results


@pytest.mark.websockets
@pytest.mark.asyncio
@pytest.mark.parametrize(
    "server", [ws_incremental_server_factory("data")], indirect=True
)
async def test_incremental_apollo_websockets(client_and_server):
    session, _ = client_and_server

    results = [r async for r in session.execute_incremental(gql(query_str))]

    assert results == expected_results


def test_dsl_defer_and_stream():
    from gql.dsl import DSLFragment, DSLQuery, DSLSchema, dsl_gql

    ds = DSLSchema(StarWarsSchema)

    name_fragment = (
        DSLFragment("NameFragment").on(ds.Character).select(ds.Character.name)
    )
    id_fragment = DSLFragment("IdFragment").on(ds.Character).select(ds.Character.id)

    query = DSLQuery(
        ds.Query.hero.select(
            name_fragment.defer(label="name"),
            id_fragment.spread().defer(),
            ds.Character.friends.stream(label="friends", initial_count=2).select(
                ds.Character.name
            ),
            ds.Character.appearsIn.stream(),
        )
    )

    request = dsl_gql(query, name_fragment, id_fragment)

    assert (
        print_ast(request.document)
        == """{
  hero {
    ...NameFragment @defer(label: "name")
    ...IdFragment @defer
    friends @stream(label: "friends", initialCount: 2) {
      name
    }
    appearsIn @stream
  }
}

fragment NameFragment on Character {
  name
}

fragment IdFragment on Character {
  id
}"""
    )


def test_dsl_stream_on_non_list_field():
    from graphql import GraphQLError

    from gql.dsl import DSLSchema

    ds = DSLSchema(StarWarsSchema)

    with pytest.raises(GraphQLError, match="list fields"):
        ds.Character.name.stream()
