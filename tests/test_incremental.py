import asyncio
import json

import pytest
from graphql import ExecutionResult, build_schema, print_ast

from gql import Client, gql
from gql.dsl import DSLFragment, DSLQuery, DSLSchema, dsl_gql
from gql.incremental import IncrementalAccumulator, IncrementalPayload

from .conftest import WebSocketServerHelper

query_str = """
    query {
      person {
        name
        ... @defer { friends { name } }
      }
    }
"""


def apply_all(payloads):
    acc = IncrementalAccumulator()
    return [acc.apply(IncrementalPayload.from_dict(p)) for p in payloads]


def test_defer_merge_and_extensions():
    results = apply_all(
        [
            {
                "data": {"person": {"name": "A"}},
                "hasNext": True,
                "extensions": {"a": 1},
            },
            {
                "incremental": [
                    {"data": {"friends": [{"name": "B"}]}, "path": ["person"]}
                ],
                "hasNext": False,
                "extensions": {"b": 2},
            },
        ]
    )
    assert results[0].data == {"person": {"name": "A"}}
    assert results[0].has_next is True
    assert results[0].extensions == {"a": 1}
    assert results[1].data == {"person": {"name": "A", "friends": [{"name": "B"}]}}
    assert results[1].has_next is False
    assert results[1].extensions == {"b": 2}


def test_stream_nested_null_overwrite_and_errors():
    results = apply_all(
        [
            {
                "data": {"a": {"list": [{"x": 1}]}, "b": None, "c": 1},
                "hasNext": True,
            },
            {
                "incremental": [
                    {"items": [{"x": 2}, {"x": 3}], "path": ["a", "list", 1]},
                    {"data": {"y": 5}, "path": ["a", "list", 0]},
                    {"data": {"z": 1}, "path": ["b"]},
                    {"data": None, "path": ["a"], "errors": [{"message": "oops"}]},
                    {"data": {"c": 2}},
                ],
                "hasNext": True,
            },
            {"incremental": [], "hasNext": True},
            {"hasNext": False},
        ]
    )
    assert len(results) == 4
    assert results[1].data == {
        "a": {"list": [{"x": 1, "y": 5}, {"x": 2}, {"x": 3}]},
        "b": None,
        "c": 2,
    }
    assert results[1].errors == [{"message": "oops"}]
    assert results[2].errors is None
    assert results[3].has_next is False


def test_non_incremental_result():
    acc = IncrementalAccumulator()
    result = acc.apply(ExecutionResult(data={"a": 1}))
    assert result.data == {"a": 1}
    assert result.has_next is False


def test_dsl_defer_and_stream():
    schema = build_schema(
        """
        type Person { name: String friends: [Person!]! }
        type Query { person: Person }
        """
    )
    ds = DSLSchema(schema)

    frag = DSLFragment("F").on(ds.Person).select(ds.Person.name)
    spread_frag = DSLFragment("G").on(ds.Person).select(ds.Person.name)
    query = DSLQuery(
        ds.Query.person.select(
            frag.defer(label="f"),
            spread_frag.spread().defer(),
            ds.Person.friends.stream(label="s", initial_count=1).select(ds.Person.name),
        )
    )
    printed = print_ast(dsl_gql(query, frag, spread_frag).document)
    assert '...F @defer(label: "f")' in printed
    assert "...G @defer" in printed
    assert 'friends @stream(initialCount: 1, label: "s")' in printed
    assert "fragment F on Person {" in printed

    with pytest.raises(Exception):
        ds.Person.name.stream()


@pytest.mark.aiohttp
@pytest.mark.asyncio
async def test_aiohttp_execute_incremental(aiohttp_server):
    from aiohttp import web

    from gql.transport.aiohttp import AIOHTTPTransport

    payloads = [
        {"data": {"person": {"name": "A"}}, "hasNext": True},
        {
            "incremental": [{"data": {"friends": []}, "path": ["person"]}],
            "hasNext": False,
        },
    ]

    async def handler(request):
        assert "deferSpec=20220824" in request.headers["accept"]
        response = web.StreamResponse()
        response.headers["Content-Type"] = (
            'multipart/mixed; boundary="graphql"; deferSpec=20220824'
        )
        await response.prepare(request)
        for p in payloads:
            await response.write(
                (
                    "\r\n--graphql\r\nContent-Type: application/json\r\n\r\n"
                    f"{json.dumps(p)}"
                ).encode()
            )
            await asyncio.sleep(0)
        await response.write(b"\r\n--graphql--\r\n")
        await response.write_eof()
        return response

    app = web.Application()
    app.router.add_route("POST", "/", handler)
    server = await aiohttp_server(app)

    transport = AIOHTTPTransport(url=str(server.make_url("/")))
    async with Client(transport=transport) as session:
        results = [r async for r in session.execute_incremental(gql(query_str))]

    assert [r.has_next for r in results] == [True, False]
    assert results[-1].data == {"person": {"name": "A", "friends": []}}


@pytest.mark.aiohttp
@pytest.mark.asyncio
async def test_aiohttp_execute_incremental_json(aiohttp_server):
    from aiohttp import web

    from gql.transport.aiohttp import AIOHTTPTransport

    async def handler(request):
        return web.json_response({"data": {"person": {"name": "A"}}})

    app = web.Application()
    app.router.add_route("POST", "/", handler)
    server = await aiohttp_server(app)

    transport = AIOHTTPTransport(url=str(server.make_url("/")))
    async with Client(transport=transport) as session:
        results = [r async for r in session.execute_incremental(gql(query_str))]

    assert len(results) == 1
    assert results[0].data == {"person": {"name": "A"}}
    assert results[0].has_next is False


async def server_incremental(ws):
    await WebSocketServerHelper.send_connection_ack(ws)
    await ws.recv()
    for payload in [
        {"data": {"person": {"name": "A"}}, "hasNext": True},
        {
            "incremental": [{"data": {"friends": []}, "path": ["person"]}],
            "hasNext": False,
        },
    ]:
        await ws.send(json.dumps({"type": "next", "id": "1", "payload": payload}))
    await WebSocketServerHelper.send_complete(ws, 1)
    await WebSocketServerHelper.wait_connection_terminate(ws)
    await ws.wait_closed()


@pytest.mark.websockets
@pytest.mark.asyncio
@pytest.mark.parametrize("graphqlws_server", [server_incremental], indirect=True)
async def test_websocket_execute_incremental(client_and_graphqlws_server):
    session, _ = client_and_graphqlws_server

    results = [r async for r in session.execute_incremental(gql(query_str))]

    assert [r.has_next for r in results] == [True, False]
    assert results[-1].data == {"person": {"name": "A", "friends": []}}
