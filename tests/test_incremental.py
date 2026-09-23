import json

import pytest
from graphql import ExecutionResult, GraphQLError, print_ast

from gql import Client, gql
from gql.dsl import DSLFragment, DSLQuery, DSLSchema, dsl_gql
from gql.incremental import (
    IncrementalExecutionResult,
    IncrementalResultAccumulator,
    incremental_result_from_payload,
)
from gql.transport.async_transport import AsyncTransport
from gql.transport.exceptions import TransportClosed
from gql.transport.websockets import WebsocketsTransport

from .conftest import WebSocketServerHelper
from .starwars.schema import StarWarsSchema


def _apply_all(payloads):
    accumulator = IncrementalResultAccumulator()
    return [
        accumulator.apply(incremental_result_from_payload(payload))
        for payload in payloads
    ]


def test_incremental_merge_defer_stream_and_payload_rules():
    results = _apply_all(
        [
            {
                "data": {
                    "hero": {
                        "name": "R2",
                        "friend": None,
                        "friends": [{"name": "Luke"}],
                    }
                },
                "hasNext": True,
                "extensions": {"chunk": 1},
            },
            {
                "incremental": [
                    {"path": ["hero"], "data": {"id": "2001", "name": "R2-D2"}},
                    {"path": ["hero", "friend"], "data": {"name": "Leia"}},
                    {
                        "path": ["hero", "friends", 1],
                        "items": [{"name": "Han"}, {"name": "Chewie"}],
                    },
                    {
                        "path": ["hero", "friends", 0],
                        "data": {"id": "1000"},
                        "errors": [{"message": "partial"}],
                    },
                    {"path": "bad", "data": {"ignored": True}},
                    {"data": {"extra": True}},
                ],
                "hasNext": True,
                "extensions": {"chunk": 2},
                "errors": [{"message": "top"}],
            },
            {"incremental": [], "hasNext": True, "extensions": {"chunk": 3}},
            {"hasNext": False},
        ]
    )

    assert results[0].data == {
        "hero": {"name": "R2", "friend": None, "friends": [{"name": "Luke"}]}
    }
    assert results[0].has_next is True
    assert results[0].extensions == {"chunk": 1}
    assert results[0].errors is None

    assert results[1].data == {
        "extra": True,
        "hero": {
            "name": "R2-D2",
            "id": "2001",
            "friend": {"name": "Leia"},
            "friends": [
                {"name": "Luke", "id": "1000"},
                {"name": "Han"},
                {"name": "Chewie"},
            ],
        },
    }
    assert results[1].extensions == {"chunk": 2}
    assert results[1].errors == [{"message": "top"}, {"message": "partial"}]
    assert results[1].has_next is True

    # Empty incremental and hasNext-only payloads still yield, and extensions
    # stay on the payload that carried them.
    assert results[2].data == results[1].data
    assert results[2].extensions == {"chunk": 3}
    assert results[2].errors is None
    assert results[2].has_next is True
    assert results[3].data == results[1].data
    assert results[3].extensions is None
    assert results[3].has_next is False


def test_stream_range_null_gaps_and_field_overwrite():
    results = _apply_all(
        [
            {"data": {"nums": None, "hero": {"name": "old"}}, "hasNext": True},
            {
                "incremental": [
                    {"path": ["nums", 2], "items": ["c"]},
                    {"path": ["hero"], "data": {"name": None, "id": "1"}},
                ],
                "hasNext": True,
            },
            {
                "incremental": [{"path": ["nums", 2], "items": ["c"]}],
                "hasNext": False,
            },
        ]
    )

    assert results[1].data == {
        "nums": [None, None, "c"],
        "hero": {"name": None, "id": "1"},
    }
    # Writing the same stream range again overwrites those indexes.
    assert results[2].data["nums"] == [None, None, "c"]


def test_nested_stream_path_and_non_incremental_payload():
    results = _apply_all(
        [
            {
                "data": {"hero": {"friends": [None, {"pets": [{"n": 1}]}]}},
                "hasNext": True,
            },
            {
                "incremental": [
                    {
                        "path": ["hero", "friends", 1, "pets", 1],
                        "items": [{"n": 2}],
                    }
                ],
                "hasNext": False,
            },
        ]
    )
    assert results[1].data["hero"]["friends"][1]["pets"] == [{"n": 1}, {"n": 2}]

    single = _apply_all([{"data": {"a": 1}, "extensions": {"ok": True}}])
    assert single[0].data == {"a": 1}
    assert single[0].has_next is False
    assert single[0].extensions == {"ok": True}
    assert isinstance(single[0], IncrementalExecutionResult)


def test_pending_id_stream_append_and_null_field():
    results = _apply_all(
        [
            {
                "data": {"hero": {"name": "R2", "friend": {"name": "Luke"}}},
                "pending": [
                    {"id": "0", "path": ["hero", "friends"]},
                    {"id": "1", "path": ["hero"]},
                ],
                "hasNext": True,
            },
            {
                "incremental": [
                    {"id": "0", "items": [{"name": "Leia"}]},
                    {"id": "1", "data": {"id": "2001"}},
                    {"path": ["hero", "friend"], "data": None},
                ],
                "completed": [{"id": "0"}, {"id": "1"}],
                "hasNext": False,
                "extensions": {"final": True},
            },
        ]
    )

    assert results[1].data == {
        "hero": {
            "name": "R2",
            "id": "2001",
            "friend": None,
            "friends": [{"name": "Leia"}],
        }
    }
    assert results[1].extensions == {"final": True}


def test_null_data_does_not_wipe_accumulated_result():
    results = _apply_all(
        [
            {"data": {"a": 1}, "hasNext": True},
            {
                "data": None,
                "errors": [{"message": "failed"}],
                "hasNext": False,
            },
        ]
    )
    assert results[1].data == {"a": 1}
    assert results[1].errors == [{"message": "failed"}]


class _ChunkTransport(AsyncTransport):
    def __init__(self, chunks):
        self.chunks = chunks

    async def connect(self):
        return None

    async def close(self):
        return None

    async def execute(self, request):
        return incremental_result_from_payload(self.chunks[0])

    async def subscribe(self, request):
        for chunk in self.chunks:
            yield incremental_result_from_payload(chunk)
        return

    async def execute_incremental(self, request, **kwargs):
        for chunk in self.chunks:
            yield incremental_result_from_payload(chunk)


@pytest.mark.asyncio
async def test_session_execute_incremental_merges_chunks():
    transport = _ChunkTransport(
        [
            {
                "data": {"hero": {"name": "R2"}},
                "hasNext": True,
                "extensions": {"part": "initial"},
            },
            {
                "incremental": [{"path": ["hero"], "data": {"id": "2001"}}],
                "hasNext": False,
                "extensions": {"part": "deferred"},
            },
        ]
    )

    query = gql("{ hero { name id } }")
    async with Client(transport=transport) as session:
        results = [result async for result in session.execute_incremental(query)]

    assert [result.data for result in results] == [
        {"hero": {"name": "R2"}},
        {"hero": {"name": "R2", "id": "2001"}},
    ]
    assert [result.extensions for result in results] == [
        {"part": "initial"},
        {"part": "deferred"},
    ]
    assert results[0].has_next is True
    assert results[1].has_next is False


@pytest.mark.asyncio
async def test_local_schema_defer_and_stream_match_full_result():
    full = gql("{ hero { name id friends { name } } }")
    incremental = gql("""
        {
          hero {
            name
            ... @defer(label: "id") { id }
            friends @stream(initialCount: 0) { name }
          }
        }
        """)
    async with Client(schema=StarWarsSchema) as session:
        expected = await session.execute(full)
        results = [result async for result in session.execute_incremental(incremental)]

    assert len(results) > 1
    assert results[0].has_next is True
    assert results[0].data["hero"]["name"] == "R2-D2"
    assert "id" not in results[0].data["hero"]
    assert results[-1].has_next is False
    assert results[-1].data == expected
    assert results[-1].errors is None


@pytest.mark.asyncio
async def test_execute_incremental_non_incremental_local_schema():
    query = gql("{ hero { name } }")
    async with Client(schema=StarWarsSchema) as session:
        results = [result async for result in session.execute_incremental(query)]

    assert len(results) == 1
    assert results[0].data == {"hero": {"name": "R2-D2"}}
    assert results[0].has_next is False
    assert results[0].errors is None


def test_incremental_directives_validate_against_schema_without_them():
    client = Client(schema=StarWarsSchema)
    query = gql("""
        query {
          hero {
            name
            ...HeroFriends @defer(label: "friends")
          }
        }
        fragment HeroFriends on Character {
          friends @stream(initialCount: 0) { name }
        }
        """)
    client.validate(query)


def test_dsl_defer_and_stream():
    ds = DSLSchema(StarWarsSchema)
    with pytest.raises(GraphQLError, match="only be used on list fields"):
        ds.Character.name.stream()

    fragment = (
        DSLFragment("HeroFields")
        .on(ds.Character)
        .select(ds.Character.name)
        .defer(label="slow")
    )
    deferred = dsl_gql(DSLQuery(ds.Query.hero.select(fragment)), fragment)
    deferred_text = print_ast(deferred.document)
    assert "...HeroFields @defer(label: " in deferred_text
    assert 'label: "slow"' in deferred_text
    assert "fragment HeroFields on Character" in deferred_text
    fragment_text = deferred_text.split("fragment HeroFields", 1)[1]
    assert "@defer" not in fragment_text

    spread_fragment = (
        DSLFragment("HeroFields2").on(ds.Character).select(ds.Character.name)
    )
    streamed = ds.Character.friends.select(ds.Character.name).stream(
        label="friends", initial_count=1
    )
    query = dsl_gql(
        DSLQuery(
            ds.Query.hero.select(
                spread_fragment.spread().defer(label="later", if_=False),
                streamed,
            )
        ),
        spread_fragment,
    )
    text = print_ast(query.document)
    assert '@defer(label: "later", if: false)' in text
    assert '@stream(label: "friends", initialCount: 1)' in text


def _multipart_parts(payloads, *, separator="\r\n"):
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
def incremental_server(aiohttp_server):
    from aiohttp import web

    async def create_server(parts, *, content_type, request_handler=lambda *_: None):
        async def handler(request):
            request_handler(request)
            response = web.StreamResponse()
            response.headers["Content-Type"] = content_type
            response.enable_chunked_encoding()
            await response.prepare(request)
            for part in parts:
                await response.write(part.encode())
            await response.write_eof()
            return response

        app = web.Application()
        app.router.add_route("POST", "/", handler)
        return await aiohttp_server(app)

    return create_server


@pytest.mark.asyncio
@pytest.mark.aiohttp
async def test_aiohttp_incremental_multipart(incremental_server):
    from gql.transport.aiohttp import AIOHTTPTransport

    seen = {}

    def capture(request):
        seen["accept"] = request.headers["accept"]

    payloads = [
        {
            "data": {"hero": {"name": "R2", "friends": [{"name": "Luke"}]}},
            "hasNext": True,
            "extensions": {"part": 1},
        },
        "not-json",
        {},
        {
            "incremental": [
                {
                    "path": ["hero", "friends", 1],
                    "items": [{"name": "Leia"}],
                    "errors": [{"message": "slow"}],
                }
            ],
            "hasNext": True,
            "extensions": {"part": 2},
        },
        {"hasNext": False, "extensions": {"part": 3}},
    ]
    # A non-JSON part is written as raw text between boundaries.
    parts = []
    separator = "\r\n"
    encoded = [payloads[0], payloads[3], payloads[4]]
    parts.extend(_multipart_parts(encoded[:1], separator=separator)[:-1])
    parts.append(
        (
            f"--graphql{separator}"
            f"Content-Type: application/json{separator}"
            f"{separator}"
            f"not-json{separator}"
        )
    )
    parts.append(
        (
            f"--graphql{separator}"
            f"Content-Type: application/json{separator}"
            f"{separator}"
            f"{{}}{separator}"
        )
    )
    parts.extend(_multipart_parts(encoded[1:], separator=separator))

    server = await incremental_server(
        parts,
        content_type=("multipart/mixed; boundary=graphql; deferSpec=20220824"),
        request_handler=capture,
    )
    transport = AIOHTTPTransport(url=server.make_url("/"))
    query = gql("{ hero { name friends { name } } }")

    async with Client(transport=transport) as session:
        results = [result async for result in session.execute_incremental(query)]

    accept = seen["accept"]
    assert "multipart/mixed" in accept
    assert "boundary=graphql" in accept
    assert "deferSpec=20220824" in accept
    assert [result.extensions for result in results] == [
        {"part": 1},
        {"part": 2},
        {"part": 3},
    ]
    assert results[0].data["hero"]["friends"] == [{"name": "Luke"}]
    assert results[1].data["hero"]["friends"] == [
        {"name": "Luke"},
        {"name": "Leia"},
    ]
    assert results[1].errors == [{"message": "slow"}]
    assert results[2].data == results[1].data
    assert results[2].errors is None
    assert results[2].has_next is False


@pytest.mark.asyncio
@pytest.mark.aiohttp
async def test_aiohttp_incremental_json_fallback(incremental_server):
    from gql.transport.aiohttp import AIOHTTPTransport

    body = json.dumps({"data": {"hero": {"name": "R2-D2"}}, "extensions": {"ok": True}})
    server = await incremental_server([body], content_type="application/json")
    transport = AIOHTTPTransport(url=server.make_url("/"))
    query = gql("{ hero { name } }")
    async with Client(transport=transport) as session:
        results = [result async for result in session.execute_incremental(query)]

    assert results[0].data == {"hero": {"name": "R2-D2"}}
    assert results[0].extensions == {"ok": True}
    assert results[0].has_next is False


@pytest.mark.asyncio
@pytest.mark.aiohttp
async def test_aiohttp_execute_incremental_requires_connection():
    from gql.transport.aiohttp import AIOHTTPTransport

    transport = AIOHTTPTransport(url="http://example.invalid/graphql")
    with pytest.raises(TransportClosed):
        async for _result in transport.execute_incremental(gql("{ hero { name } }")):
            pass


def test_websocket_parser_forwards_incremental_payloads():
    transport = WebsocketsTransport(url="ws://localhost/graphql")

    transport.subprotocol = transport.APOLLO_SUBPROTOCOL
    answer_type, answer_id, result = transport._parse_answer(
        json.dumps(
            {
                "type": "data",
                "id": "1",
                "payload": {
                    "incremental": [{"path": ["hero"], "data": {"id": "1"}}],
                    "hasNext": True,
                },
            }
        )
    )
    assert answer_type == "data"
    assert answer_id == 1
    assert isinstance(result, IncrementalExecutionResult)
    assert result.has_next is True
    assert result.incremental is not None
    assert result.incremental[0]["data"] == {"id": "1"}

    classic_type, _, classic = transport._parse_answer(
        json.dumps(
            {"type": "data", "id": "1", "payload": {"data": {"hero": {"name": "R2"}}}}
        )
    )
    assert classic_type == "data"
    assert type(classic) is ExecutionResult
    assert classic.data == {"hero": {"name": "R2"}}

    transport.subprotocol = transport.GRAPHQLWS_SUBPROTOCOL
    next_type, next_id, streamed = transport._parse_answer(
        json.dumps(
            {
                "type": "next",
                "id": "4",
                "payload": {
                    "hasNext": False,
                    "incremental": [
                        {"path": ["friends", 0], "items": [{"name": "Leia"}]}
                    ],
                },
            }
        )
    )
    assert next_type == "data"
    assert next_id == 4
    assert isinstance(streamed, IncrementalExecutionResult)
    assert streamed.has_next is False
    assert streamed.incremental is not None
    assert streamed.incremental[0]["items"] == [{"name": "Leia"}]


async def _server_incremental(ws):
    await WebSocketServerHelper.send_connection_ack(ws)
    message = json.loads(await ws.recv())
    query_id = message["id"]
    payloads = [
        {
            "data": {"hero": {"name": "R2"}},
            "hasNext": True,
            "extensions": {"part": 1},
        },
        {
            "incremental": [{"path": ["hero"], "data": {"id": "2001"}}],
            "hasNext": False,
            "extensions": {"part": 2},
            "errors": [{"message": "late"}],
        },
    ]
    for payload in payloads:
        await ws.send(json.dumps({"type": "data", "id": query_id, "payload": payload}))
    await WebSocketServerHelper.send_complete(ws, query_id)
    await WebSocketServerHelper.wait_connection_terminate(ws)


@pytest.mark.asyncio
@pytest.mark.websockets
@pytest.mark.parametrize("server", [_server_incremental], indirect=True)
async def test_websocket_execute_incremental(client_and_server):
    session, _server = client_and_server
    query = gql("{ hero { name id } }")
    results = [result async for result in session.execute_incremental(query)]

    assert results[0].data == {"hero": {"name": "R2"}}
    assert results[0].extensions == {"part": 1}
    assert results[0].has_next is True
    assert results[0].errors is None
    assert results[1].data == {"hero": {"name": "R2", "id": "2001"}}
    assert results[1].extensions == {"part": 2}
    assert results[1].errors == [{"message": "late"}]
    assert results[1].has_next is False
