import json

import pytest
from graphql import build_schema, print_ast

from gql import Client, gql
from gql.dsl import DSLFragment, DSLQuery, DSLSchema, dsl_gql
from gql.incremental import IncrementalResultAccumulator
from gql.transport.aiohttp import AIOHTTPTransport
from gql.transport.exceptions import TransportProtocolError
from gql.transport.websockets import WebsocketsTransport
from gql.transport.websockets_protocol import WebsocketsProtocolTransportBase


def test_accumulator_merges_defer_stream_and_keeps_payload_extensions():
    acc = IncrementalResultAccumulator()

    first = acc.apply(
        {
            "data": {
                "hero": {"name": "Luke", "friends": [{"name": "Leia"}], "bio": None}
            },
            "hasNext": True,
            "extensions": {"part": 1},
        }
    )
    assert first.data["hero"]["name"] == "Luke"
    assert first.has_next is True
    assert first.extensions == {"part": 1}
    assert first.errors is None

    second = acc.apply(
        {
            "incremental": [
                {
                    "path": ["hero"],
                    "data": {"bio": "A farmboy", "home": None},
                    "errors": [{"message": "slow field"}],
                },
                {
                    "path": ["hero", "friends", 1],
                    "items": [{"name": "Han"}, None],
                },
            ],
            "hasNext": True,
            "extensions": {"part": 2},
        }
    )
    assert second.data["hero"]["bio"] == "A farmboy"
    assert second.data["hero"]["home"] is None
    assert second.data["hero"]["friends"] == [
        {"name": "Leia"},
        {"name": "Han"},
        None,
    ]
    assert second.extensions == {"part": 2}
    assert first.extensions == {"part": 1}
    assert second.errors == [{"message": "slow field"}]
    assert second.has_next is True
    # Earlier snapshot is not mutated by later payloads.
    assert "bio" not in first.data["hero"] or first.data["hero"]["bio"] is None

    third = acc.apply(
        {
            "incremental": [
                {"data": {"extra": True}},
                {
                    "path": ["missing", "nope"],
                    "data": {"x": 1},
                    "errors": [{"message": "bad path"}],
                },
                {"path": ["hero", "friends", 0], "items": [{"name": "inserted"}]},
            ],
            "hasNext": False,
        }
    )
    assert third.data["extra"] is True
    assert third.data["hero"]["friends"][0] == {"name": "inserted"}
    assert third.errors == [{"message": "bad path"}]
    assert third.has_next is False
    assert third.extensions is None


def test_empty_incremental_and_has_next_only_still_yield():
    acc = IncrementalResultAccumulator()
    pending = acc.apply({"data": {"a": 1}, "hasNext": True})
    empty = acc.apply({"incremental": [], "hasNext": True, "extensions": {"e": True}})
    done = acc.apply({"hasNext": False})

    assert pending.data == {"a": 1}
    assert empty.data == {"a": 1}
    assert empty.extensions == {"e": True}
    assert empty.has_next is True
    assert done.data == {"a": 1}
    assert done.has_next is False
    assert done.extensions is None


def test_non_incremental_payload():
    result = IncrementalResultAccumulator().apply(
        {"data": {"ok": True}, "errors": [{"message": "warn"}]}
    )
    assert result.data == {"ok": True}
    assert result.has_next is False
    assert result.errors == [{"message": "warn"}]


def test_dsl_defer_and_stream():
    schema = build_schema("""
        type Query {
          hero: Character
        }
        type Character {
          name: String
          friends: [Character]
        }
        """)
    ds = DSLSchema(schema)
    fragment = (
        DSLFragment("HeroName")
        .on(ds.Character)
        .select(ds.Character.name)
        .defer(label="later")
    )
    spread = fragment.spread().defer(label="spread-label")
    query = dsl_gql(
        fragment,
        DSLQuery(
            ds.Query.hero.select(
                spread,
                ds.Character.friends.select(ds.Character.name).stream(
                    label="friends", initial_count=1
                ),
            )
        ),
    )
    printed = print_ast(query.document)
    assert "@defer(label: " in printed
    assert "spread-label" in printed
    assert "@stream(label: " in printed
    assert "initialCount: 1" in printed

    direct = dsl_gql(fragment, DSLQuery(ds.Query.hero.select(fragment)))
    assert "@defer(label: " in print_ast(direct.document)
    assert "later" in print_ast(direct.document)

    with pytest.raises(Exception):
        ds.Character.name.stream()


def test_graphqlws_forwards_incremental_payload():
    transport = WebsocketsTransport(url="ws://example.test/graphql")
    transport.subprotocol = WebsocketsProtocolTransportBase.GRAPHQLWS_SUBPROTOCOL
    payload = {
        "hasNext": True,
        "incremental": [{"path": ["hero"], "data": {"name": "Leia"}}],
        "extensions": {"trace": 1},
    }
    answer_type, answer_id, execution_result = transport._parse_answer(
        json.dumps({"id": "1", "type": "next", "payload": payload})
    )
    assert answer_type == "data"
    assert answer_id == 1
    assert execution_result is not None
    assert execution_result.incremental_payload == payload
    assert execution_result.extensions == {"trace": 1}

    has_next_only = {"hasNext": False}
    _, _, result = transport._parse_answer(
        json.dumps({"id": "2", "type": "next", "payload": has_next_only})
    )
    assert result.incremental_payload == has_next_only

    with pytest.raises(TransportProtocolError):
        transport._parse_answer(json.dumps({"id": "4", "type": "next", "payload": {}}))

    transport.subprotocol = WebsocketsProtocolTransportBase.APOLLO_SUBPROTOCOL
    _, _, apollo_result = transport._parse_answer(
        json.dumps({"id": "3", "type": "data", "payload": payload})
    )
    assert apollo_result.incremental_payload == payload


pytestmark_aiohttp = pytest.mark.aiohttp


@pytest.mark.asyncio
@pytest.mark.aiohttp
async def test_aiohttp_incremental_multipart_and_json(aiohttp_server):
    from aiohttp import web

    parts = [
        {
            "data": {"hero": {"name": None, "friends": []}},
            "hasNext": True,
            "extensions": {"n": 1},
        },
        {
            "incremental": [
                {
                    "path": ["hero"],
                    "data": {"name": "Luke"},
                    "errors": [{"message": "e1"}],
                },
                {"path": ["hero", "friends", 0], "items": [{"name": "Leia"}]},
            ],
            "hasNext": True,
            "extensions": {"n": 2},
        },
        {"incremental": [], "hasNext": True},
        {"hasNext": False, "extensions": {"n": 4}},
    ]

    def multipart_body():
        chunks = []
        for part in parts:
            chunks.append(
                "--graphql\r\n"
                "Content-Type: application/json\r\n"
                "\r\n"
                f"{json.dumps(part)}\r\n"
            )
        chunks.append("--graphql--\r\n")
        return "".join(chunks)

    async def multipart_handler(request):
        assert "deferSpec=20220824" in request.headers["Accept"]
        body = multipart_body().encode()
        return web.Response(
            body=body,
            headers={
                "Content-Type": ("multipart/mixed;boundary=graphql;deferSpec=20220824")
            },
        )

    async def json_handler(request):
        return web.json_response(
            {"data": {"hero": {"name": "Solo"}}, "extensions": {"j": 1}}
        )

    app = web.Application()
    app.router.add_post("/multipart", multipart_handler)
    app.router.add_post("/json", json_handler)
    server = await aiohttp_server(app)

    transport = AIOHTTPTransport(url=f"http://{server.host}:{server.port}/multipart")
    query = gql("{ hero { name friends { name } } }")
    async with Client(transport=transport) as session:
        results = [item async for item in session.execute_incremental(query)]

    assert len(results) == 4
    assert results[0].data == {"hero": {"name": None, "friends": []}}
    assert results[0].extensions == {"n": 1}
    assert results[0].has_next is True
    assert results[1].data == {"hero": {"name": "Luke", "friends": [{"name": "Leia"}]}}
    assert results[1].errors == [{"message": "e1"}]
    assert results[1].extensions == {"n": 2}
    assert results[2].data["hero"]["name"] == "Luke"
    assert results[2].has_next is True
    assert results[3].has_next is False
    assert results[3].extensions == {"n": 4}
    assert results[0].data["hero"]["name"] is None

    json_transport = AIOHTTPTransport(url=f"http://{server.host}:{server.port}/json")
    async with Client(transport=json_transport) as session:
        json_results = [
            item async for item in session.execute_incremental(gql("{ hero { name } }"))
        ]

    assert len(json_results) == 1
    assert json_results[0].data == {"hero": {"name": "Solo"}}
    assert json_results[0].has_next is False
    assert json_results[0].extensions == {"j": 1}
