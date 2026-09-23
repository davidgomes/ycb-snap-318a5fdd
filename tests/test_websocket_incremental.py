import asyncio
import json
from typing import Any, Callable, Dict, List

import pytest

from gql import Client, gql

from .conftest import WebSocketServerHelper

# Marking all tests in this file with the websockets marker
pytestmark = pytest.mark.websockets

query_str = """
    query {
      hero {
        id
        ... @defer { name }
        friends @stream(initialCount: 1) { name }
      }
    }
"""

payloads: List[Dict[str, Any]] = [
    {
        "data": {"hero": {"id": "2001", "friends": [{"name": "Luke"}]}},
        "hasNext": True,
        "extensions": {"step": 0},
    },
    {
        "incremental": [
            {"data": {"name": "R2-D2"}, "path": ["hero"]},
            {"items": [{"name": "Han"}], "path": ["hero", "friends", 1]},
        ],
        "hasNext": True,
    },
    {
        "incremental": [
            {
                "items": None,
                "path": ["hero", "friends", 2],
                "errors": [{"message": "friend error"}],
            }
        ],
        "hasNext": True,
    },
    {"hasNext": False},
]

expected_final_data = {
    "hero": {
        "id": "2001",
        "name": "R2-D2",
        "friends": [{"name": "Luke"}, {"name": "Han"}],
    }
}

logged_messages: List[str] = []


def incremental_server_factory(
    message_type: str, payloads: List[Dict[str, Any]] = payloads
) -> Callable:
    async def server_handler(ws):
        from websockets.exceptions import ConnectionClosed

        logged_messages.clear()

        try:
            await WebSocketServerHelper.send_connection_ack(ws)

            query_message = json.loads(await ws.recv())
            assert query_message["type"] in ("subscribe", "start")
            query_id = query_message["id"]

            for payload in payloads:
                await ws.send(
                    json.dumps(
                        {"type": message_type, "id": query_id, "payload": payload}
                    )
                )
                await asyncio.sleep(0)

            await WebSocketServerHelper.send_complete(ws, query_id)

            async for message in ws:
                logged_messages.append(message)
        except ConnectionClosed:
            pass

    return server_handler


graphqlws_handler = incremental_server_factory("next")
apollo_handler = incremental_server_factory("data")


def check_results(results):
    assert len(results) == 4
    assert [r.has_next for r in results] == [True, True, True, False]

    assert results[0].data == {"hero": {"id": "2001", "friends": [{"name": "Luke"}]}}
    assert results[0].extensions == {"step": 0}

    assert results[1].data == expected_final_data
    assert results[1].extensions is None
    assert results[1].errors is None

    assert results[2].errors == [{"message": "friend error"}]
    assert results[3].data == expected_final_data


@pytest.mark.asyncio
@pytest.mark.parametrize("graphqlws_server", [graphqlws_handler], indirect=True)
async def test_graphqlws_incremental(graphqlws_server):
    from gql.transport.websockets import WebsocketsTransport

    transport = WebsocketsTransport(
        url=f"ws://{graphqlws_server.hostname}:" f"{graphqlws_server.port}/graphql"
    )

    async with Client(transport=transport) as session:
        results = [r async for r in session.execute_incremental(gql(query_str))]

    check_results(results)


@pytest.mark.asyncio
@pytest.mark.parametrize("graphqlws_server", [graphqlws_handler], indirect=True)
async def test_graphqlws_incremental_aiohttp_websockets(graphqlws_server):
    from gql.transport.aiohttp_websockets import AIOHTTPWebsocketsTransport

    transport = AIOHTTPWebsocketsTransport(
        url=f"ws://{graphqlws_server.hostname}:{graphqlws_server.port}/graphql"
    )

    async with Client(transport=transport) as session:
        results = [r async for r in session.execute_incremental(gql(query_str))]

    check_results(results)


@pytest.mark.asyncio
@pytest.mark.parametrize("server", [apollo_handler], indirect=True)
async def test_apollo_websocket_incremental(client_and_server):
    session, server = client_and_server

    results = [r async for r in session.execute_incremental(gql(query_str))]

    check_results(results)


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "graphqlws_server",
    [incremental_server_factory("next", [{"data": {"hero": {"id": "2001"}}}])],
    indirect=True,
)
async def test_graphqlws_incremental_non_incremental_response(graphqlws_server):
    from gql.transport.websockets import WebsocketsTransport

    transport = WebsocketsTransport(
        url=f"ws://{graphqlws_server.hostname}:{graphqlws_server.port}/graphql"
    )

    async with Client(transport=transport) as session:
        results = [r async for r in session.execute_incremental(gql(query_str))]

    assert len(results) == 1
    assert results[0].data == {"hero": {"id": "2001"}}
    assert results[0].has_next is False


@pytest.mark.asyncio
@pytest.mark.parametrize("graphqlws_server", [graphqlws_handler], indirect=True)
async def test_graphqlws_incremental_early_stop(graphqlws_server):
    from gql.transport.websockets import WebsocketsTransport

    transport = WebsocketsTransport(
        url=f"ws://{graphqlws_server.hostname}:{graphqlws_server.port}/graphql"
    )

    async with Client(transport=transport) as session:
        generator = session.execute_incremental(gql(query_str))

        async for result in generator:
            assert result.has_next is True
            break

        await generator.aclose()

    assert json.loads(logged_messages[0]) == {"id": "1", "type": "complete"}


@pytest.mark.asyncio
@pytest.mark.parametrize("graphqlws_server", [graphqlws_handler], indirect=True)
async def test_graphqlws_execute_with_incremental_initial_payload(graphqlws_server):
    from gql.transport.websockets import WebsocketsTransport

    transport = WebsocketsTransport(
        url=f"ws://{graphqlws_server.hostname}:{graphqlws_server.port}/graphql"
    )

    async with Client(transport=transport) as session:
        result = await session.execute(gql(query_str))

    assert result == {"hero": {"id": "2001", "friends": [{"name": "Luke"}]}}
