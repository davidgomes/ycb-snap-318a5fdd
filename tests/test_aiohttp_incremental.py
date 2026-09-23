import asyncio
import json
from typing import Any, Dict, List

import pytest

from gql import Client, gql
from gql.transport.exceptions import (
    TransportClosed,
    TransportConnectionFailed,
    TransportProtocolError,
    TransportServerError,
)

# Marking all tests in this file with the aiohttp marker
pytestmark = pytest.mark.aiohttp

query_str = """
    query {
      hero {
        id
        ... @defer(label: "name") { name }
        friends @stream(initialCount: 1) { name }
      }
    }
"""

payloads: List[Dict[str, Any]] = [
    {
        "data": {"hero": {"id": "2001", "friends": [{"name": "Luke"}]}},
        "hasNext": True,
    },
    {
        "incremental": [
            {"data": {"name": "R2-D2"}, "path": ["hero"], "label": "name"},
        ],
        "hasNext": True,
    },
    {
        "incremental": [
            {"items": [{"name": "Han"}], "path": ["hero", "friends", 1]},
        ],
        "hasNext": True,
    },
    {"hasNext": False},
]

multipart_content_type = 'multipart/mixed;boundary="graphql";deferSpec=20220824'


def create_multipart_response(payloads, *, separator="\r\n", boundary="graphql"):
    parts = [
        (
            f"--{boundary}{separator}"
            f"Content-Type: application/json; charset=utf-8{separator}"
            f"{separator}"
            f"{payload if isinstance(payload, str) else json.dumps(payload)}"
            f"{separator}"
        )
        for payload in payloads
    ]
    parts.append(f"--{boundary}--{separator}")
    return parts


def create_apollo_style_multipart_response(payloads):
    """Parts preceded by a CRLF, as sent by the Apollo Router."""
    parts = [
        (
            "\r\n--graphql\r\n"
            "content-type: application/json; charset=utf-8\r\n"
            "\r\n"
            f"{json.dumps(payload)}"
        )
        for payload in payloads
    ]
    parts.append("\r\n--graphql--\r\n")
    return parts


@pytest.fixture
def incremental_server(aiohttp_server):
    from aiohttp import web

    async def create_server(
        parts,
        *,
        content_type=multipart_content_type,
        status=200,
        request_handler=lambda *args: None,
    ):
        async def handler(request):
            request_handler(request)
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
        return await aiohttp_server(app)

    return create_server


@pytest.mark.asyncio
async def test_aiohttp_incremental_multipart(incremental_server):
    from gql.transport.aiohttp import AIOHTTPTransport

    def assert_request_headers(request):
        accept_header = request.headers["accept"]
        assert "multipart/mixed" in accept_header
        assert "boundary=graphql" in accept_header
        assert "deferSpec=20220824" in accept_header
        assert "application/json" in accept_header

    server = await incremental_server(
        create_multipart_response(payloads), request_handler=assert_request_headers
    )
    transport = AIOHTTPTransport(url=server.make_url("/"))

    async with Client(transport=transport) as session:
        results = [r async for r in session.execute_incremental(gql(query_str))]

    assert len(results) == 4
    assert [r.has_next for r in results] == [True, True, True, False]

    assert results[0].data == {"hero": {"id": "2001", "friends": [{"name": "Luke"}]}}
    assert results[1].data == {
        "hero": {"id": "2001", "name": "R2-D2", "friends": [{"name": "Luke"}]}
    }
    assert results[3].data == {
        "hero": {
            "id": "2001",
            "name": "R2-D2",
            "friends": [{"name": "Luke"}, {"name": "Han"}],
        }
    }


@pytest.mark.asyncio
async def test_aiohttp_incremental_multipart_apollo_style(incremental_server):
    from gql.transport.aiohttp import AIOHTTPTransport

    server = await incremental_server(create_apollo_style_multipart_response(payloads))
    transport = AIOHTTPTransport(url=server.make_url("/"))

    async with Client(transport=transport) as session:
        results = [r async for r in session.execute_incremental(gql(query_str))]

    assert len(results) == 4
    assert results[3].has_next is False
    assert results[3].data["hero"]["friends"] == [{"name": "Luke"}, {"name": "Han"}]


@pytest.mark.asyncio
async def test_aiohttp_incremental_multipart_other_boundary_and_heartbeat(
    incremental_server,
):
    from gql.transport.aiohttp import AIOHTTPTransport

    parts = create_multipart_response(
        [payloads[0], {}, "", payloads[1], payloads[3]], boundary="-"
    )
    server = await incremental_server(
        parts, content_type='multipart/mixed; boundary="-"'
    )
    transport = AIOHTTPTransport(url=server.make_url("/"))

    async with Client(transport=transport) as session:
        results = [r async for r in session.execute_incremental(gql(query_str))]

    assert len(results) == 3
    assert results[2].has_next is False
    assert results[2].data["hero"]["name"] == "R2-D2"


@pytest.mark.asyncio
async def test_aiohttp_incremental_errors_and_extensions(incremental_server):
    from gql.transport.aiohttp import AIOHTTPTransport

    error = {"message": "Could not fetch name", "path": ["hero", "name"]}

    parts = create_multipart_response(
        [
            {**payloads[0], "extensions": {"cost": 1}},
            {
                "incremental": [
                    {"data": None, "path": ["hero"], "errors": [error]},
                    {"items": [{"name": "Han"}], "path": ["hero", "friends", 1]},
                ],
                "hasNext": False,
            },
        ]
    )
    server = await incremental_server(parts)
    transport = AIOHTTPTransport(url=server.make_url("/"))

    async with Client(transport=transport) as session:
        results = [r async for r in session.execute_incremental(gql(query_str))]

    assert results[0].extensions == {"cost": 1}
    assert results[0].errors is None
    assert results[1].extensions is None
    assert results[1].errors == [error]
    assert results[1].data["hero"]["friends"] == [{"name": "Luke"}, {"name": "Han"}]


@pytest.mark.asyncio
async def test_aiohttp_incremental_non_incremental_json_response(aiohttp_server):
    from aiohttp import web

    from gql.transport.aiohttp import AIOHTTPTransport

    async def handler(request):
        return web.json_response({"data": {"hero": {"id": "2001", "name": "R2-D2"}}})

    app = web.Application()
    app.router.add_route("POST", "/", handler)
    server = await aiohttp_server(app)
    transport = AIOHTTPTransport(url=server.make_url("/"))

    async with Client(transport=transport) as session:
        results = [r async for r in session.execute_incremental(gql(query_str))]

    assert len(results) == 1
    assert results[0].data == {"hero": {"id": "2001", "name": "R2-D2"}}
    assert results[0].has_next is False


@pytest.mark.asyncio
async def test_aiohttp_incremental_json_error_response(aiohttp_server):
    from aiohttp import web

    from gql.transport.aiohttp import AIOHTTPTransport

    error = {"message": "Unknown directive"}

    async def handler(request):
        return web.json_response({"errors": [error]}, status=400)

    app = web.Application()
    app.router.add_route("POST", "/", handler)
    server = await aiohttp_server(app)
    transport = AIOHTTPTransport(url=server.make_url("/"))

    async with Client(transport=transport) as session:
        results = [r async for r in session.execute_incremental(gql(query_str))]

    assert len(results) == 1
    assert results[0].errors == [error]
    assert results[0].data is None


@pytest.mark.asyncio
async def test_aiohttp_incremental_invalid_json_response(aiohttp_server):
    from aiohttp import web

    from gql.transport.aiohttp import AIOHTTPTransport

    async def handler(request):
        return web.json_response({"not_data": 1})

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

    server = await incremental_server(create_multipart_response(payloads), status=500)
    transport = AIOHTTPTransport(url=server.make_url("/"))

    async with Client(transport=transport) as session:
        with pytest.raises(TransportServerError):
            async for _ in session.execute_incremental(gql(query_str)):
                pass  # pragma: no cover


@pytest.mark.parametrize(
    "invalid_payload",
    ["not json", '["a list"]', '{"unknown": 1}'],
)
@pytest.mark.asyncio
async def test_aiohttp_incremental_invalid_part(incremental_server, invalid_payload):
    from gql.transport.aiohttp import AIOHTTPTransport

    parts = create_multipart_response([payloads[0], invalid_payload])
    server = await incremental_server(parts)
    transport = AIOHTTPTransport(url=server.make_url("/"))

    async with Client(transport=transport) as session:
        results = []
        with pytest.raises(TransportProtocolError):
            async for result in session.execute_incremental(gql(query_str)):
                results.append(result)

    assert len(results) == 1


@pytest.mark.asyncio
async def test_aiohttp_incremental_invalid_part_content_type(incremental_server):
    from gql.transport.aiohttp import AIOHTTPTransport

    parts = [
        "--graphql\r\nContent-Type: text/plain\r\n\r\nhello\r\n",
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
async def test_aiohttp_incremental_connection_failed():
    from gql.transport.aiohttp import AIOHTTPTransport

    transport = AIOHTTPTransport(url="http://127.0.0.1:1/")

    async with Client(transport=transport) as session:
        with pytest.raises(TransportConnectionFailed):
            async for _ in session.execute_incremental(gql(query_str)):
                pass  # pragma: no cover


@pytest.mark.asyncio
async def test_aiohttp_incremental_transport_not_connected():
    from gql.transport.aiohttp import AIOHTTPTransport

    transport = AIOHTTPTransport(url="http://127.0.0.1:1/")

    with pytest.raises(TransportClosed):
        async for _ in transport.execute_incremental(gql(query_str)):
            pass  # pragma: no cover
