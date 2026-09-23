import pytest
from graphql import ExecutionResult, print_ast

from gql import Client, IncrementalExecutionResult, gql
from gql.dsl import DSLFragment, DSLQuery, DSLSchema, dsl_gql
from gql.incremental import IncrementalResultBuilder
from gql.transport.async_transport import AsyncTransport

from .conftest import strip_braces_spaces
from .starwars.schema import StarWarsSchema


def test_defer_and_stream_directives_in_dsl():
    ds = DSLSchema(StarWarsSchema)

    fragment = (
        DSLFragment("HeroDetails")
        .on(ds.Character)
        .select(ds.Character.name)
        .defer(label="details")
    )
    spread = fragment.spread().defer(label="later", if_=False)
    friends = ds.Character.friends.stream(label="friends", initial_count=1).select(
        ds.Character.name
    )
    query = DSLQuery(
        ds.Query.hero.select(
            fragment,
            spread,
            friends,
        )
    )
    document = dsl_gql(fragment, query)

    printed = strip_braces_spaces(print_ast(document.document))
    fragment_text, operation_text = printed.split("\n\n", 1)
    assert fragment_text.startswith("fragment HeroDetails on Character {")
    assert "@defer" not in fragment_text
    assert '...HeroDetails @defer(label: "details")' in operation_text
    assert '...HeroDetails @defer(if: false, label: "later")' in printed
    assert 'friends @stream(label: "friends", initialCount: 1) {' in printed


def test_stream_rejects_non_list_fields():
    from graphql import GraphQLError

    ds = DSLSchema(StarWarsSchema)
    with pytest.raises(GraphQLError, match="@stream"):
        ds.Character.name.stream()


def test_stream_kept_when_other_directives_are_added():
    ds = DSLSchema(StarWarsSchema)
    field = ds.Character.appearsIn.stream(initial_count=0).directives(
        ds("@include")(**{"if": True})
    )
    printed = strip_braces_spaces(str(field))
    assert printed == "appearsIn @include(if: true) @stream(initialCount: 0)"


def test_incremental_builder_accumulates_defer_and_stream():
    builder = IncrementalResultBuilder()

    first = builder.apply(
        {
            "data": {"hero": {"name": "Luke", "friends": [None], "home": None}},
            "hasNext": True,
            "extensions": {"request": 1},
            "errors": [{"message": "partial"}],
        }
    )
    assert first.data == {"hero": {"name": "Luke", "friends": [None], "home": None}}
    assert first.has_next is True
    assert first.extensions == {"request": 1}
    assert first.errors == [{"message": "partial"}]

    second = builder.apply(
        {
            "incremental": [
                {
                    "data": {"home": {"name": "Tatooine"}},
                    "path": ["hero"],
                    "errors": [{"message": "defer failed softly"}],
                },
                # Missing path is a root merge. A bad path must not stop the next item.
                {"data": {"extra": True}},
                {"data": {"nope": True}, "path": {"bad": True}},
                {
                    "items": [{"name": "Leia"}, {"name": "Han"}],
                    "path": ["hero", "friends", 0],
                },
                {
                    "items": [{"name": "late"}],
                    "path": ["hero", "friends", 3],
                    "errors": [{"message": "stream tail"}],
                },
            ],
            "hasNext": True,
            "extensions": {"request": 2},
        }
    )

    assert second.data == {
        "hero": {
            "name": "Luke",
            "home": {"name": "Tatooine"},
            "friends": [
                {"name": "Leia"},
                {"name": "Han"},
                None,
                {"name": "late"},
            ],
        },
        "extra": True,
    }
    assert second.extensions == {"request": 2}
    assert second.errors == [
        {"message": "defer failed softly"},
        {"message": "stream tail"},
    ]
    # Earlier payloads keep the data they had at the time.
    assert first.data == {"hero": {"name": "Luke", "friends": [None], "home": None}}

    # Null parent, nested list indexes, and field overwrite.
    third = builder.apply(
        {
            "incremental": [
                {"data": {"name": "Anakin"}, "path": ["hero"]},
                {"data": {"nested": {"ok": 1}}, "path": ["hero", "friends", 2]},
            ],
            "hasNext": False,
        }
    )
    assert third.data["hero"]["name"] == "Anakin"
    assert third.data["hero"]["home"] == {"name": "Tatooine"}
    assert third.data["hero"]["friends"][2] == {"nested": {"ok": 1}}
    assert third.has_next is False
    assert third.extensions is None
    assert third.errors is None

    empty = builder.apply({"incremental": [], "hasNext": True})
    assert empty.data == third.data
    assert empty.has_next is True
    assert empty.extensions is None

    heartbeat = builder.apply({"hasNext": False, "extensions": {"done": True}})
    assert heartbeat.data == third.data
    assert heartbeat.has_next is False
    assert heartbeat.extensions == {"done": True}

    # Top-level path (including []) merges like an incremental defer patch.
    rooted = IncrementalResultBuilder()
    rooted.apply({"data": {"hello": "world"}, "hasNext": True})
    merged = rooted.apply(
        {"data": {"test": "again"}, "path": [], "hasNext": False, "extensions": {}}
    )
    assert merged.data == {"hello": "world", "test": "again"}
    assert merged.extensions == {}


def test_null_overwrite_and_concurrent_patches():
    builder = IncrementalResultBuilder()
    builder.apply(
        {
            "data": {"left": {"keep": 1, "replace": "old"}, "items": ["a"]},
            "hasNext": True,
        }
    )
    result = builder.apply(
        {
            "incremental": [
                {"data": {"replace": None, "added": 2}, "path": ["left"]},
                {"items": ["c"], "path": ["items", 2]},
                {"items": ["b"], "path": ["items", 1]},
                {"data": {"right": True}},
            ],
            "hasNext": False,
        }
    )
    assert result.data == {
        "left": {"keep": 1, "replace": None, "added": 2},
        "items": ["a", "b", "c"],
        "right": True,
    }


class _ListTransport(AsyncTransport):
    def __init__(self, payloads):
        self.payloads = payloads

    async def connect(self):
        return None

    async def close(self):
        return None

    async def execute(self, request):
        raise AssertionError("execute_incremental should be used")

    async def subscribe(self, request):
        if False:  # pragma: no cover
            yield ExecutionResult()

    async def execute_incremental(self, request, **kwargs):
        for payload in self.payloads:
            yield payload


class _SingleTransport(AsyncTransport):
    async def connect(self):
        return None

    async def close(self):
        return None

    async def execute(self, request):
        return ExecutionResult(
            data={"hello": "world"},
            extensions={"once": True},
        )

    async def subscribe(self, request):
        if False:  # pragma: no cover
            yield ExecutionResult()


@pytest.mark.asyncio
async def test_session_execute_incremental_merges_payloads():
    transport = _ListTransport(
        [
            {
                "data": {"hero": {"name": "Luke"}},
                "hasNext": True,
                "extensions": {"n": 1},
                "errors": [{"message": "keep going"}],
            },
            {
                "incremental": [
                    {"data": {"id": "1000"}, "path": ["hero"]},
                    {"items": [{"name": "Leia"}], "path": ["hero", "friends", 0]},
                ],
                "hasNext": False,
                "extensions": {"n": 2},
            },
        ]
    )

    async with Client(transport=transport) as session:
        query = gql("{ hero { name } }")
        results = []
        async for result in session.execute_incremental(query):
            results.append(result)

    assert len(results) == 2
    assert isinstance(results[0], IncrementalExecutionResult)
    assert results[0].data == {"hero": {"name": "Luke"}}
    assert results[0].has_next is True
    assert results[0].extensions == {"n": 1}
    assert results[0].errors == [{"message": "keep going"}]
    assert results[1].data == {
        "hero": {"name": "Luke", "id": "1000", "friends": [{"name": "Leia"}]}
    }
    assert results[1].has_next is False
    assert results[1].extensions == {"n": 2}
    assert results[1].errors is None
    assert results[0].data == {"hero": {"name": "Luke"}}


@pytest.mark.asyncio
async def test_session_execute_incremental_single_response():
    async with Client(transport=_SingleTransport()) as session:
        results = []
        async for result in session.execute_incremental(gql("{ hello }")):
            results.append(result)

    assert len(results) == 1
    assert results[0].data == {"hello": "world"}
    assert results[0].has_next is False
    assert results[0].extensions == {"once": True}
    assert results[0].errors is None


@pytest.mark.websockets
def test_websocket_parser_forwards_incremental_payloads():
    from gql.transport.websockets import WebsocketsTransport

    transport = WebsocketsTransport(url="ws://example.com/graphql")

    transport.subprotocol = transport.GRAPHQLWS_SUBPROTOCOL
    answer_type, answer_id, result = transport._parse_answer(
        '{"type":"next","id":"3","payload":{"incremental":[],"hasNext":true}}'
    )
    assert answer_type == "data"
    assert answer_id == 3
    assert result is not None
    assert result.has_next is True
    assert result.raw_payload == {"incremental": [], "hasNext": True}

    transport.subprotocol = transport.APOLLO_SUBPROTOCOL
    answer_type, answer_id, result = transport._parse_answer(
        '{"type":"data","id":"4","payload":{"hasNext":false,"extensions":{"z":1}}}'
    )
    assert answer_type == "data"
    assert answer_id == 4
    assert result is not None
    assert result.has_next is False
    assert result.raw_payload["extensions"] == {"z": 1}

    # Ordinary payloads stay plain execution results.
    answer_type, answer_id, result = transport._parse_answer(
        '{"type":"data","id":"5","payload":{"data":{"ok":true}}}'
    )
    assert isinstance(result, ExecutionResult)
    assert not isinstance(result, IncrementalExecutionResult)
    assert result.data == {"ok": True}


def test_pending_id_payloads_merge_like_paths():
    builder = IncrementalResultBuilder()
    first = builder.apply(
        {
            "data": {"hero": {"name": "Luke"}, "friends": [{"name": "Leia"}]},
            "pending": [
                {"id": "0", "path": ["hero"], "label": "home"},
                {"id": "1", "path": ["friends"], "label": "friends"},
            ],
            "hasNext": True,
            "extensions": {"step": 1},
        }
    )
    assert first.data["friends"] == [{"name": "Leia"}]
    assert first.extensions == {"step": 1}

    second = builder.apply(
        {
            "hasNext": True,
            "incremental": [
                {"data": {"home": "Tatooine"}, "id": "0"},
                {
                    "data": {"nested": True},
                    "id": "0",
                    "subPath": ["extra"],
                },
            ],
            "extensions": {"step": 2},
        }
    )
    assert second.data["hero"]["home"] == "Tatooine"
    assert second.data["hero"]["extra"] == {"nested": True}
    assert second.extensions == {"step": 2}
    assert first.extensions == {"step": 1}

    third = builder.apply(
        {
            "hasNext": False,
            "incremental": [
                {"items": [{"name": "Han"}, {"name": "Chewie"}], "id": "1"}
            ],
        }
    )
    assert third.data["friends"] == [
        {"name": "Leia"},
        {"name": "Han"},
        {"name": "Chewie"},
    ]
    assert third.has_next is False
    assert third.extensions is None


@pytest.mark.asyncio
async def test_local_schema_execute_incremental():
    from graphql import (
        GraphQLDeferDirective,
        GraphQLField,
        GraphQLList,
        GraphQLObjectType,
        GraphQLSchema,
        GraphQLStreamDirective,
        GraphQLString,
        specified_directives,
    )

    from gql.transport.local_schema import LocalSchemaTransport

    friend = GraphQLObjectType(
        "Friend",
        lambda: {
            "name": GraphQLField(GraphQLString),
            "home": GraphQLField(GraphQLString),
        },
    )
    schema = GraphQLSchema(
        query=GraphQLObjectType(
            "Query",
            lambda: {
                "hero": GraphQLField(friend),
                "friends": GraphQLField(GraphQLList(friend)),
            },
        ),
        directives=[
            *specified_directives,
            GraphQLDeferDirective,
            GraphQLStreamDirective,
        ],
    )
    root = {
        "hero": {"name": "Luke", "home": "Tatooine"},
        "friends": [{"name": "Leia"}, {"name": "Han"}],
    }
    query = gql(
        """
        {
          hero {
            name
            ... @defer(label: "home") { home }
          }
          friends @stream(initialCount: 1) { name }
        }
        """
    )

    async with Client(schema=schema, transport=LocalSchemaTransport(schema)) as session:
        session.transport.schema = schema
        # Root value is a transport execute argument.
        results = []
        async for result in session.transport.execute_incremental(query, root):
            results.append(result)

    # The session API accumulates. Re-run through the session by a transport
    # that replays the payloads the local executor produced.
    transport = _ListTransport(results)
    async with Client(schema=schema, transport=transport) as session:
        accumulated = []
        async for result in session.execute_incremental(query):
            accumulated.append(result)

    assert accumulated[-1].has_next is False
    assert accumulated[-1].data["hero"]["name"] == "Luke"
    assert accumulated[-1].data["hero"]["home"] == "Tatooine"
    assert accumulated[-1].data["friends"] == [{"name": "Leia"}, {"name": "Han"}]


@pytest.mark.asyncio
async def test_session_execute_incremental_against_local_schema():
    from graphql import (
        GraphQLDeferDirective,
        GraphQLField,
        GraphQLObjectType,
        GraphQLSchema,
        GraphQLStreamDirective,
        GraphQLString,
        specified_directives,
    )

    from gql.transport.local_schema import LocalSchemaTransport

    class _RootTransport(LocalSchemaTransport):
        async def execute_incremental(self, request, *args, **kwargs):
            async for payload in super().execute_incremental(
                request, {"hero": {"name": "Leia", "home": "Alderaan"}}, **kwargs
            ):
                yield payload

    friend = GraphQLObjectType(
        "Friend",
        lambda: {
            "name": GraphQLField(GraphQLString),
            "home": GraphQLField(GraphQLString),
        },
    )
    schema = GraphQLSchema(
        query=GraphQLObjectType(
            "Query",
            lambda: {"hero": GraphQLField(friend)},
        ),
        directives=[
            *specified_directives,
            GraphQLDeferDirective,
            GraphQLStreamDirective,
        ],
    )
    query = gql(
        """
        {
          hero {
            name
            ... @defer { home }
          }
        }
        """
    )
    async with Client(schema=schema, transport=_RootTransport(schema)) as session:
        results = [result async for result in session.execute_incremental(query)]

    assert results[0].data == {"hero": {"name": "Leia"}}
    assert results[0].has_next is True
    assert results[-1].data == {"hero": {"name": "Leia", "home": "Alderaan"}}
    assert results[-1].has_next is False
    assert results[0].data == {"hero": {"name": "Leia"}}


@pytest.mark.aiohttp
@pytest.mark.asyncio
async def test_aiohttp_incremental_multipart(aiohttp_server):
    from aiohttp import web

    from gql.transport.aiohttp import INCREMENTAL_DELIVERY_ACCEPT, AIOHTTPTransport

    seen = {}

    async def handler(request):
        seen["accept"] = request.headers.get("Accept")
        response = web.StreamResponse(
            headers={
                "Content-Type": ("multipart/mixed;boundary=graphql;deferSpec=20220824")
            }
        )
        response.enable_chunked_encoding()
        await response.prepare(request)
        parts = [
            (
                "--graphql\r\nContent-Type: application/json\r\n\r\n"
                '{"data":{"hero":{"name":"Luke","friends":[null]}},"hasNext":true,'
                '"extensions":{"n":1}}\r\n'
            ),
            (
                "--graphql\r\nContent-Type: application/json\r\n\r\n"
                '{"incremental":[{"data":{"home":"Tatooine"},"path":["hero"]},'
                '{"items":[{"name":"Leia"}],"path":["hero","friends",0]}],'
                '"hasNext":false,"extensions":{"n":2}}\r\n'
            ),
            "--graphql--\r\n",
        ]
        for part in parts:
            await response.write(part.encode())
        await response.write_eof()
        return response

    app = web.Application()
    app.router.add_post("/", handler)
    server = await aiohttp_server(app)

    transport = AIOHTTPTransport(url=server.make_url("/"))
    async with Client(transport=transport) as session:
        results = [
            result
            async for result in session.execute_incremental(gql("{ hero { name } }"))
        ]

    assert seen["accept"] == INCREMENTAL_DELIVERY_ACCEPT
    assert results[0].data == {"hero": {"name": "Luke", "friends": [None]}}
    assert results[0].has_next is True
    assert results[0].extensions == {"n": 1}
    assert results[1].data == {
        "hero": {
            "name": "Luke",
            "home": "Tatooine",
            "friends": [{"name": "Leia"}],
        }
    }
    assert results[1].has_next is False
    assert results[1].extensions == {"n": 2}
