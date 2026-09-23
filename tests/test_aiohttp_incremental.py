import asyncio
import json

import pytest

from gql import Client, gql
from gql.graphql_request import GraphQLRequest
from gql.transport.exceptions import (
    TransportClosed,
    TransportConnectionFailed,
    TransportProtocolError,
    TransportServerError,
)

from .starwars.schema import StarWarsSchema

# Marking all tests in this file with the aiohttp marker
pytestmark = pytest.mark.aiohttp

query_str = """
    query {
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

incremental_payloads = [
    {
        "data": {"hero": {"id": "2001", "friends": [{"name": "Luke Skywalker"}]}},
        "hasNext": True,
    },
    {
        "incremental": [
            {"items": [{"name": "Han Solo"}], "path": ["hero", "friends", 1]},
        ],
        "hasNext": True,
    },
    {
        "incremental": [
            {"data": {"name": "R2-D2"}, "path": ["hero"], "label": "HeroName"},
            {"items": [{"name": "Leia Organa"}], "path": ["hero", "friends", 2]},
        ],
        "hasNext": False,
    },
]

final_data = {
    "hero": {
        "id": "2001",
        "name": "R2-D2",
        "friends": [
            {"name": "Luke Skywalker"},
            {"name": "Han Solo"},
            {"name": "Leia Organa"},
        ],
    }
}

incremental_content_type = 'multipart/mixed; boundary="graphql"; deferSpec=20220824'


def create_multipart_parts(payloads):
    """Create the parts of a multipart response streaming the payloads."""
    parts = []

    for payload in payloads:
        body = payload if isinstance(payload, str) else json.dumps(payload)
        parts.append(
            "--graphql\r\n"
            "Content-Type: application/json; charset=utf-8\r\n"
            "\r\n"
            f"{body}\r\n"
        )

    parts.append("--graphql--\r\n")

    return parts


@pytest.fixture
def incremental_server(aiohttp_server):
    from aiohttp import web

    async def create_server(
        parts,
        *,
        content_type=incremental_content_type,
        status=200,
        request_handler=None,
    ):
        async def handler(request):
            if request_handler is not None:
                await request_handler(request)
            response = web.StreamResponse(status=status)
            response.headers["Content-Type"] = content_type
            response.enable_chunked_encoding()
            await response.prepare(request)
            for part in parts:
                await response.write(part.encode())
                await asyncio.sleep(0)  # force the chunk to be written
            await response.write_eof()
            return response

        app = web.Application()
        app.router.add_route("POST", "/", handler)
        server = await aiohttp_server(app)
        return server

    return create_server


@pytest.mark.asyncio
async def test_aiohttp_incremental_defer_and_stream(incremental_server):
    from gql.transport.aiohttp import AIOHTTPTransport

    received_requests = []

    async def request_handler(request):
        received_requests.append(
            {"headers": request.headers, "body": await request.json()}
        )

    server = await incremental_server(
        create_multipart_parts(incremental_payloads), request_handler=request_handler
    )
    transport = AIOHTTPTransport(url=server.make_url("/"))

    async with Client(transport=transport) as session:
        results = [
            result async for result in session.execute_incremental(gql(query_str))
        ]

    # Verify the Accept header follows the incremental delivery spec
    accept_header = received_requests[0]["headers"]["Accept"]
    assert "multipart/mixed" in accept_header
    assert "boundary=graphql" in accept_header
    assert "deferSpec=20220824" in accept_header
    assert "application/json" in accept_header
    assert "@defer" in received_requests[0]["body"]["query"]

    assert len(results) == 3
    assert [result.has_next for result in results] == [True, True, False]

    assert results[0].data == {
        "hero": {"id": "2001", "friends": [{"name": "Luke Skywalker"}]}
    }
    assert results[1].data == {
        "hero": {
            "id": "2001",
            "friends": [{"name": "Luke Skywalker"}, {"name": "Han Solo"}],
        }
    }
    assert results[2].data == final_data
    assert results[2].incremental is not None
    assert results[2].incremental[0]["label"] == "HeroName"

    assert transport.response_headers is not None
    assert "multipart/mixed" in transport.response_headers["Content-Type"]


@pytest.mark.asyncio
async def test_aiohttp_incremental_unquoted_boundary(incremental_server):
    from gql.transport.aiohttp import AIOHTTPTransport

    server = await incremental_server(
        create_multipart_parts(incremental_payloads),
        content_type="multipart/mixed;boundary=graphql;deferSpec=20220824",
    )
    transport = AIOHTTPTransport(url=server.make_url("/"))

    async with Client(transport=transport) as session:
        results = [
            result async for result in session.execute_incremental(gql(query_str))
        ]

    assert len(results) == 3
    assert results[-1].data == final_data


@pytest.mark.asyncio
async def test_aiohttp_incremental_errors_do_not_halt(incremental_server):
    from gql.transport.aiohttp import AIOHTTPTransport

    payloads = [
        {"data": {"hero": {"id": "2001", "friends": []}}, "hasNext": True},
        {
            "incremental": [
                {
                    "data": None,
                    "path": ["hero"],
                    "errors": [
                        {"message": "cannot get name", "path": ["hero", "name"]}
                    ],
                },
                {"items": [{"name": "Luke Skywalker"}], "path": ["hero", "friends", 0]},
            ],
            "hasNext": True,
        },
        {
            "incremental": [
                {"items": [{"name": "Han Solo"}], "path": ["hero", "friends", 1]},
            ],
            "hasNext": False,
        },
    ]

    server = await incremental_server(create_multipart_parts(payloads))
    transport = AIOHTTPTransport(url=server.make_url("/"))

    async with Client(transport=transport) as session:
        results = [
            result async for result in session.execute_incremental(gql(query_str))
        ]

    assert len(results) == 3
    assert results[1].errors == [
        {"message": "cannot get name", "path": ["hero", "name"]}
    ]
    assert results[2].errors is None
    assert results[2].data == {
        "hero": {
            "id": "2001",
            "friends": [{"name": "Luke Skywalker"}, {"name": "Han Solo"}],
        }
    }


@pytest.mark.asyncio
async def test_aiohttp_incremental_has_next_only_and_ignored_parts(incremental_server):
    from gql.transport.aiohttp import AIOHTTPTransport

    parts = create_multipart_parts(
        [
            incremental_payloads[0],
            "{}",  # heartbeat
            "   ",  # empty body
            "{invalid json}",
            {"foo": "bar"},  # neither data, errors, incremental nor hasNext
            '["not", "a", "dict"]',
            {"incremental": [], "hasNext": True},
            {"hasNext": False},
        ]
    )

    server = await incremental_server(parts)
    transport = AIOHTTPTransport(url=server.make_url("/"))

    async with Client(transport=transport) as session:
        results = [
            result async for result in session.execute_incremental(gql(query_str))
        ]

    assert len(results) == 3
    assert [result.has_next for result in results] == [True, True, False]
    assert results[0].data == results[1].data == results[2].data


@pytest.mark.asyncio
async def test_aiohttp_incremental_non_incremental_response(aiohttp_server):
    from aiohttp import web

    from gql.transport.aiohttp import AIOHTTPTransport

    async def handler(request):
        return web.Response(
            text=json.dumps(
                {"data": {"hero": {"id": "2001"}}, "extensions": {"cost": 1}}
            ),
            content_type="application/json",
        )

    app = web.Application()
    app.router.add_route("POST", "/", handler)
    server = await aiohttp_server(app)
    transport = AIOHTTPTransport(url=server.make_url("/"))

    async with Client(transport=transport) as session:
        results = [
            result async for result in session.execute_incremental(gql(query_str))
        ]

    assert len(results) == 1
    assert results[0].data == {"hero": {"id": "2001"}}
    assert results[0].extensions == {"cost": 1}
    assert results[0].has_next is False


@pytest.mark.asyncio
async def test_aiohttp_incremental_invalid_json_response(aiohttp_server):
    from aiohttp import web

    from gql.transport.aiohttp import AIOHTTPTransport

    async def handler(request):
        return web.Response(text="<p>hello</p>", content_type="text/html")

    app = web.Application()
    app.router.add_route("POST", "/", handler)
    server = await aiohttp_server(app)
    transport = AIOHTTPTransport(url=server.make_url("/"))

    async with Client(transport=transport) as session:
        with pytest.raises(TransportProtocolError):
            async for _ in session.execute_incremental(gql(query_str)):
                pass  # pragma: no cover


@pytest.mark.asyncio
async def test_aiohttp_incremental_server_error(incremental_server):
    from gql.transport.aiohttp import AIOHTTPTransport

    server = await incremental_server(
        create_multipart_parts(incremental_payloads), status=500
    )
    transport = AIOHTTPTransport(url=server.make_url("/"))

    async with Client(transport=transport) as session:
        with pytest.raises(TransportServerError) as exc_info:
            async for _ in session.execute_incremental(gql(query_str)):
                pass  # pragma: no cover

    assert exc_info.value.code == 500


@pytest.mark.asyncio
async def test_aiohttp_incremental_wrong_part_content_type(incremental_server):
    from gql.transport.aiohttp import AIOHTTPTransport

    parts = [
        "--graphql\r\nContent-Type: text/html\r\n\r\n<p>hello</p>\r\n",
        "--graphql--\r\n",
    ]

    server = await incremental_server(parts)
    transport = AIOHTTPTransport(url=server.make_url("/"))

    async with Client(transport=transport) as session:
        with pytest.raises(TransportProtocolError) as exc_info:
            async for _ in session.execute_incremental(gql(query_str)):
                pass  # pragma: no cover

    assert "Unexpected part content-type" in str(exc_info.value)


@pytest.mark.asyncio
async def test_aiohttp_incremental_invalid_multipart(incremental_server):
    from gql.transport.aiohttp import AIOHTTPTransport

    # LF separators instead of the CRLF separators required by the spec
    parts = [part.replace("\r\n", "\n") for part in create_multipart_parts([{}])]

    server = await incremental_server(parts)
    transport = AIOHTTPTransport(url=server.make_url("/"))

    async with Client(transport=transport) as session:
        with pytest.raises(TransportConnectionFailed):
            async for _ in session.execute_incremental(gql(query_str)):
                pass  # pragma: no cover


@pytest.mark.asyncio
async def test_aiohttp_incremental_transport_not_connected():
    from gql.transport.aiohttp import AIOHTTPTransport

    transport = AIOHTTPTransport(url="http://localhost/")

    with pytest.raises(TransportClosed):
        async for _ in transport.execute_incremental(GraphQLRequest(query_str)):
            pass  # pragma: no cover


@pytest.mark.asyncio
async def test_aiohttp_incremental_extra_args_headers(incremental_server):
    from gql.transport.aiohttp import AIOHTTPTransport

    received_headers = []

    async def request_handler(request):
        received_headers.append(request.headers)

    server = await incremental_server(
        create_multipart_parts(incremental_payloads), request_handler=request_handler
    )
    transport = AIOHTTPTransport(url=server.make_url("/"))

    extra_headers = {"x-custom": "value"}

    async with Client(transport=transport) as session:
        results = [
            result
            async for result in session.execute_incremental(
                gql(query_str), extra_args={"headers": extra_headers}
            )
        ]

    assert results[-1].data == final_data
    assert received_headers[0]["x-custom"] == "value"
    assert "deferSpec=20220824" in received_headers[0]["Accept"]
    assert extra_headers == {"x-custom": "value"}


@pytest.mark.asyncio
async def test_aiohttp_incremental_dsl_with_schema(incremental_server):
    from gql.dsl import DSLInlineFragment, DSLQuery, DSLSchema, dsl_gql
    from gql.transport.aiohttp import AIOHTTPTransport

    received_queries = []

    async def request_handler(request):
        received_queries.append((await request.json())["query"])

    payloads = [
        {"data": {"hero": {"id": "2001", "friends": []}}, "hasNext": True},
        {
            "incremental": [
                {
                    "data": {"name": "R2-D2", "appearsIn": ["NEWHOPE", "JEDI"]},
                    "path": ["hero"],
                    "label": "details",
                },
                {"items": [{"name": "Luke Skywalker"}], "path": ["hero", "friends", 0]},
            ],
            "hasNext": False,
        },
    ]

    server = await incremental_server(
        create_multipart_parts(payloads), request_handler=request_handler
    )
    transport = AIOHTTPTransport(url=server.make_url("/"))

    # The StarWars schema does not define the @defer and @stream directives
    client = Client(transport=transport, schema=StarWarsSchema, parse_results=True)
    ds = DSLSchema(StarWarsSchema)

    query = dsl_gql(
        DSLQuery(
            ds.Query.hero.select(
                ds.Character.id,
                DSLInlineFragment()
                .on(ds.Character)
                .select(ds.Character.name, ds.Character.appearsIn)
                .defer(label="details"),
                ds.Character.friends.stream().select(ds.Character.name),
            )
        )
    )

    async with client as session:
        results = [result async for result in session.execute_incremental(query)]

    assert '... on Character @defer(label: "details")' in received_queries[0]
    assert "friends @stream" in received_queries[0]

    assert results[-1].data == {
        "hero": {
            "id": "2001",
            "name": "R2-D2",
            "appearsIn": [4, 6],
            "friends": [{"name": "Luke Skywalker"}],
        }
    }
