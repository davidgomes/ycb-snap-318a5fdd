import json
from typing import Any, AsyncGenerator, List

import pytest
from graphql import GraphQLError, print_ast

from gql import Client, gql
from gql.dsl import DSLFragment, DSLQuery, DSLSchema, dsl_gql
from gql.incremental import IncrementalExecutionResult, execution_result_from_payload
from gql.transport.async_transport import AsyncTransport
from gql.transport.exceptions import TransportProtocolError

from .starwars.schema import StarWarsSchema


class PayloadTransport(AsyncTransport):
    """Yields scripted incremental payloads."""

    def __init__(self, payloads: List[dict]):
        self.payloads = payloads

    async def connect(self) -> None:
        return None

    async def close(self) -> None:
        return None

    async def execute(self, request: Any, *args: Any, **kwargs: Any):
        return execution_result_from_payload(self.payloads[0])

    async def subscribe(self, request: Any) -> AsyncGenerator:
        if False:  # pragma: no cover
            yield None

    async def execute_incremental(
        self, request: Any, *args: Any, **kwargs: Any
    ) -> AsyncGenerator[IncrementalExecutionResult, None]:
        for payload in self.payloads:
            yield execution_result_from_payload(payload)


async def _collect(payloads: List[dict]) -> List[IncrementalExecutionResult]:
    transport = PayloadTransport(payloads)
    results = []
    async with Client(transport=transport) as session:
        query = gql("{ __typename }")
        async for result in session.execute_incremental(query):
            results.append(result)
    return results


@pytest.mark.asyncio
async def test_execute_incremental_accumulates_defer_and_extensions():
    results = await _collect(
        [
            {
                "data": {"hero": {"name": "R2-D2"}},
                "hasNext": True,
                "extensions": {"part": 1},
            },
            {
                "incremental": [
                    {"data": {"ship": "X-wing"}, "path": ["hero"]},
                ],
                "hasNext": False,
                "extensions": {"part": 2},
            },
        ]
    )

    assert results[0].data == {"hero": {"name": "R2-D2"}}
    assert results[0].has_next is True
    assert results[0].extensions == {"part": 1}
    assert results[0].errors is None

    assert results[1].data == {"hero": {"name": "R2-D2", "ship": "X-wing"}}
    assert results[1].has_next is False
    assert results[1].extensions == {"part": 2}
    # Earlier extensions are not carried forward, and the first snapshot is stable.
    assert results[0].extensions == {"part": 1}
    assert results[0].data == {"hero": {"name": "R2-D2"}}


@pytest.mark.asyncio
async def test_stream_nested_paths_nulls_and_overwrites():
    results = await _collect(
        [
            {
                "data": {
                    "hero": {
                        "name": "old",
                        "friend": None,
                        "friends": [{"name": "A", "appearsIn": ["NEWHOPE"]}],
                    }
                },
                "hasNext": True,
            },
            {
                "incremental": [
                    {"data": {"name": None}, "path": ["hero"]},
                    {"data": {"name": "Leia"}, "path": ["hero", "friend"]},
                    {
                        "items": ["EMPIRE"],
                        "path": ["hero", "friends", 0, "appearsIn", 1],
                    },
                    {"items": [{"name": "B"}], "path": ["hero", "friends", 1]},
                    "not-an-item",
                    {"data": {"ok": True}},
                ],
                "hasNext": False,
            },
        ]
    )

    assert results[1].data == {
        "hero": {
            "name": None,
            "friend": {"name": "Leia"},
            "friends": [
                {"name": "A", "appearsIn": ["NEWHOPE", "EMPIRE"]},
                {"name": "B"},
            ],
        },
        "ok": True,
    }


@pytest.mark.asyncio
async def test_root_level_merge_when_path_is_missing():
    results = await _collect(
        [
            {"data": {"a": 1}, "hasNext": True},
            {"incremental": [{"data": {"b": 2}}], "hasNext": False},
        ]
    )
    assert results[1].data == {"a": 1, "b": 2}


@pytest.mark.asyncio
async def test_empty_incremental_and_has_next_only_still_yield():
    results = await _collect(
        [
            {"data": {"a": 1}, "hasNext": True, "extensions": {"keep": False}},
            {"incremental": [], "hasNext": True},
            {"hasNext": False},
        ]
    )

    assert len(results) == 3
    assert results[1].data == {"a": 1}
    assert results[1].has_next is True
    assert results[1].extensions is None
    assert results[2].data == {"a": 1}
    assert results[2].has_next is False
    assert results[2].errors is None


@pytest.mark.asyncio
async def test_errors_do_not_halt_later_items_or_payloads():
    results = await _collect(
        [
            {
                "data": {"hero": {"name": "R2"}},
                "errors": [{"message": "first"}],
                "hasNext": True,
            },
            {
                "errors": [{"message": "second"}],
                "incremental": [
                    {
                        "data": {"dropped": True},
                        "path": ["missing", 0],
                        "errors": [{"message": "item"}],
                    },
                    {"data": {"ok": True}, "path": ["hero"]},
                ],
                "hasNext": False,
            },
        ]
    )

    assert results[0].errors == [{"message": "first"}]
    assert results[0].data == {"hero": {"name": "R2"}}
    assert results[1].errors == [
        {"message": "second"},
        {"message": "item"},
    ]
    assert results[1].data["hero"]["ok"] is True
    assert "first" not in json.dumps(results[1].errors)


@pytest.mark.asyncio
async def test_non_incremental_response_yields_once():
    results = await _collect(
        [{"data": {"hello": "world"}, "errors": [{"message": "partial"}]}]
    )
    assert len(results) == 1
    assert results[0].data == {"hello": "world"}
    assert results[0].has_next is False
    assert results[0].errors == [{"message": "partial"}]


def test_dsl_defer_and_stream():
    ds = DSLSchema(StarWarsSchema)
    fragment = (
        DSLFragment("HeroDetails")
        .on(ds.Character)
        .select(ds.Character.name)
        .defer(label="details")
    )
    spread = fragment.spread().defer(if_=False)
    friends = ds.Character.friends.select(ds.Character.name).stream(
        label="pals", initial_count=1
    )
    query = DSLQuery(
        ds.Query.hero.select(fragment, spread, friends),
    )
    printed = print_ast(dsl_gql(fragment, query).document)
    definition = printed.split("\n\n", 1)[0]

    assert "fragment HeroDetails on Character {" in definition
    assert "@defer" not in definition
    assert '...HeroDetails @defer(label: "details")' in printed
    assert "...HeroDetails @defer(if: false)" in printed
    assert 'friends @stream(label: "pals", initialCount: 1)' in printed

    bare = ds.Character.appearsIn.stream()
    assert "@stream" in str(bare)

    with pytest.raises(GraphQLError, match="list fields"):
        ds.Character.name.stream(initial_count=1)


def _defer_parts(payloads, *, separator="\r\n"):
    parts = []
    for payload in payloads:
        parts.append(
            (
                f"--graphql{separator}"
                f"Content-Type: application/json{separator}"
                f"{separator}"
                f"{json.dumps(payload)}{separator}"
            )
        )
    parts.append(f"--graphql--{separator}")
    return parts


@pytest.fixture
def defer_server(aiohttp_server):
    from aiohttp import web

    async def create_server(parts, *, content_type, request_holder):
        async def handler(request):
            request_holder["accept"] = request.headers.get("Accept")
            response = web.StreamResponse()
            response.headers["Content-Type"] = content_type
            if "multipart/mixed" in content_type:
                response.enable_chunked_encoding()
            await response.prepare(request)
            if isinstance(parts, list):
                for part in parts:
                    await response.write(part.encode())
            else:
                await response.write(parts.encode())
            await response.write_eof()
            return response

        app = web.Application()
        app.router.add_post("/", handler)
        return await aiohttp_server(app)

    return create_server


@pytest.mark.asyncio
@pytest.mark.aiohttp
async def test_aiohttp_incremental_multipart(defer_server):
    from gql.transport.aiohttp import AIOHTTPTransport

    payloads = [
        {
            "data": {"hero": {"name": "R2-D2", "friends": [{"name": "Luke"}]}},
            "hasNext": True,
            "extensions": {"trace": "init"},
        },
        {
            "incremental": [
                {"data": {"primaryFunction": "Astromech"}, "path": ["hero"]},
                {
                    "items": [{"name": "Leia"}],
                    "path": ["hero", "friends", 1],
                },
            ],
            "hasNext": True,
            "extensions": {"trace": "patch"},
        },
        {"incremental": [], "hasNext": True},
        {"hasNext": False},
    ]
    holder = {}
    server = await defer_server(
        _defer_parts(payloads),
        content_type="multipart/mixed;boundary=graphql;deferSpec=20220824",
        request_holder=holder,
    )
    transport = AIOHTTPTransport(url=str(server.make_url("/")))

    async with Client(transport=transport) as session:
        results = [
            result
            async for result in session.execute_incremental(gql("{ __typename }"))
        ]

    assert "boundary=graphql" in holder["accept"]
    assert "deferSpec=20220824" in holder["accept"]
    assert len(results) == 4
    assert results[0].extensions == {"trace": "init"}
    assert results[1].extensions == {"trace": "patch"}
    assert results[1].data["hero"]["primaryFunction"] == "Astromech"
    assert results[1].data["hero"]["friends"][1] == {"name": "Leia"}
    assert results[2].data == results[1].data
    assert results[2].has_next is True
    assert results[2].extensions is None
    assert results[3].has_next is False
    assert results[0].data["hero"]["friends"] == [{"name": "Luke"}]


@pytest.mark.asyncio
@pytest.mark.aiohttp
async def test_aiohttp_incremental_json_and_errors_do_not_stop_the_stream(defer_server):
    from gql.transport.aiohttp import AIOHTTPTransport

    holder = {}
    server = await defer_server(
        json.dumps({"data": {"hello": "world"}}),
        content_type="application/json",
        request_holder=holder,
    )
    transport = AIOHTTPTransport(url=str(server.make_url("/")))
    async with Client(transport=transport) as session:
        results = [
            result
            async for result in session.execute_incremental(gql("{ __typename }"))
        ]
    assert results[0].data == {"hello": "world"}
    assert results[0].has_next is False

    error_payloads = [
        {
            "data": {"hero": {"name": "R2"}},
            "errors": [{"message": "slow"}],
            "hasNext": True,
        },
        {
            "incremental": [{"data": {"ok": True}, "path": ["hero"]}],
            "hasNext": False,
        },
    ]
    server = await defer_server(
        _defer_parts(error_payloads),
        content_type='multipart/mixed; boundary="graphql"; deferSpec=20220824',
        request_holder=holder,
    )
    transport = AIOHTTPTransport(url=str(server.make_url("/")))
    async with Client(transport=transport) as session:
        results = [
            result
            async for result in session.execute_incremental(gql("{ __typename }"))
        ]
    assert results[0].errors == [{"message": "slow"}]
    assert results[1].data == {"hero": {"name": "R2", "ok": True}}
    assert results[1].errors is None


@pytest.mark.asyncio
@pytest.mark.aiohttp
async def test_aiohttp_incremental_rejects_subscription_multipart(defer_server):
    from gql.transport.aiohttp import AIOHTTPTransport

    server = await defer_server(
        _defer_parts([{"data": {"a": 1}}]),
        content_type="multipart/mixed;boundary=graphql;subscriptionSpec=1.0",
        request_holder={},
    )
    transport = AIOHTTPTransport(url=str(server.make_url("/")))
    async with Client(transport=transport) as session:
        with pytest.raises(TransportProtocolError):
            async for _result in session.execute_incremental(gql("{ __typename }")):
                pass


async def _incremental_ws_handler(ws):
    from .conftest import WebSocketServerHelper

    await WebSocketServerHelper.send_connection_ack(ws)
    message = json.loads(await ws.recv())
    assert message["type"] == "subscribe"
    payloads = [
        {
            "data": {"hero": {"name": "R2-D2"}},
            "hasNext": True,
            "extensions": {"step": 1},
        },
        {
            "hasNext": False,
            "extensions": {"step": 2},
            "incremental": [
                {
                    "data": {"primaryFunction": "Astromech"},
                    "path": ["hero"],
                    "errors": [{"message": "deferred blew up"}],
                },
                {"data": {"still": True}, "path": ["hero"]},
            ],
        },
    ]
    for payload in payloads:
        await ws.send(
            json.dumps({"id": message["id"], "type": "next", "payload": payload})
        )
    await ws.send(json.dumps({"id": message["id"], "type": "complete"}))
    try:
        while True:
            await ws.recv()
    except Exception:
        return


@pytest.mark.asyncio
@pytest.mark.websockets
@pytest.mark.parametrize("graphqlws_server", [_incremental_ws_handler], indirect=True)
async def test_websocket_incremental_delivery(graphqlws_server):
    from gql.transport.websockets import WebsocketsTransport

    url = f"ws://{graphqlws_server.hostname}:{graphqlws_server.port}/graphql"
    transport = WebsocketsTransport(
        url=url,
        subprotocols=[WebsocketsTransport.GRAPHQLWS_SUBPROTOCOL],
    )
    async with Client(transport=transport) as session:
        results = [
            result
            async for result in session.execute_incremental(gql("{ __typename }"))
        ]

    assert len(results) == 2
    assert results[0].data == {"hero": {"name": "R2-D2"}}
    assert results[0].extensions == {"step": 1}
    assert results[0].has_next is True
    assert results[1].extensions == {"step": 2}
    assert results[1].has_next is False
    assert results[1].errors == [{"message": "deferred blew up"}]
    assert results[1].data == {
        "hero": {
            "name": "R2-D2",
            "primaryFunction": "Astromech",
            "still": True,
        }
    }


async def _apollo_incremental_handler(ws):
    from .conftest import WebSocketServerHelper

    await WebSocketServerHelper.send_connection_ack(ws)
    message = json.loads(await ws.recv())
    assert message["type"] == "start"
    await ws.send(
        json.dumps(
            {
                "id": message["id"],
                "type": "data",
                "payload": {
                    "hasNext": True,
                    "incremental": [{"data": {"a": 1}}],
                },
            }
        )
    )
    await ws.send(
        json.dumps(
            {
                "id": message["id"],
                "type": "data",
                "payload": {"hasNext": False, "incremental": [{"data": {"b": 2}}]},
            }
        )
    )
    await WebSocketServerHelper.send_complete(ws, message["id"])
    try:
        await WebSocketServerHelper.wait_connection_terminate(ws)
    except Exception:
        return


@pytest.mark.asyncio
@pytest.mark.websockets
@pytest.mark.parametrize("server", [_apollo_incremental_handler], indirect=True)
async def test_apollo_websocket_forwards_incremental_payloads(server):
    from gql.transport.websockets import WebsocketsTransport

    url = f"ws://{server.hostname}:{server.port}/graphql"
    transport = WebsocketsTransport(
        url=url,
        subprotocols=[WebsocketsTransport.APOLLO_SUBPROTOCOL],
    )
    async with Client(transport=transport) as session:
        results = [
            result
            async for result in session.execute_incremental(gql("{ __typename }"))
        ]

    assert [result.data for result in results] == [{"a": 1}, {"a": 1, "b": 2}]
    assert results[0].has_next is True
    assert results[1].has_next is False
