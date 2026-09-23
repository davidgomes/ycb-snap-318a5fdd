import asyncio
import json
from typing import List

import pytest

from gql import Client, gql
from gql.incremental import IncrementalExecutionResult
from gql.transport.exceptions import TransportProtocolError

from .conftest import WebSocketServerHelper

# Marking all tests in this file with the websockets marker
pytestmark = pytest.mark.websockets

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
        "extensions": {"cost": 1},
    },
    {
        "incremental": [
            {
                "data": None,
                "path": ["hero"],
                "label": "HeroName",
                "errors": [{"message": "cannot get name", "path": ["hero", "name"]}],
            },
            {"items": [{"name": "Leia Organa"}], "path": ["hero", "friends", 2]},
        ],
        "hasNext": True,
    },
    {"hasNext": False},
]

final_data = {
    "hero": {
        "id": "2001",
        "friends": [
            {"name": "Luke Skywalker"},
            {"name": "Han Solo"},
            {"name": "Leia Organa"},
        ],
    }
}

# Messages received by the server
logged_messages: List[str] = []


def incremental_server_factory(payloads, *, complete=True):
    """Server answering each query with the payloads for both the apollo
    and the graphql-ws protocols."""

    async def incremental_server(ws):
        from websockets.exceptions import ConnectionClosed

        logged_messages.clear()

        try:
            await WebSocketServerHelper.send_connection_ack(ws)

            async for message in ws:
                logged_messages.append(message)
                json_message = json.loads(message)
                query_id = json_message.get("id")

                if json_message["type"] in ("start", "subscribe"):
                    data_type = (
                        "next" if json_message["type"] == "subscribe" else "data"
                    )

                    for payload in payloads:
                        await ws.send(
                            json.dumps(
                                {"type": data_type, "id": query_id, "payload": payload}
                            )
                        )

                    if complete:
                        await WebSocketServerHelper.send_complete(ws, query_id)

                elif json_message["type"] == "stop":
                    await WebSocketServerHelper.send_complete(ws, query_id)

        except ConnectionClosed:
            pass

    return incremental_server


incremental_server = incremental_server_factory(incremental_payloads)


async def wait_for_logged_messages(count):
    for _ in range(100):
        if len(logged_messages) >= count:
            return
        await asyncio.sleep(0.01)


def check_incremental_results(results):
    assert len(results) == 4
    assert all(isinstance(result, IncrementalExecutionResult) for result in results)
    assert [result.has_next for result in results] == [True, True, True, False]

    assert results[0].data == {
        "hero": {"id": "2001", "friends": [{"name": "Luke Skywalker"}]}
    }
    assert results[1].data == {
        "hero": {
            "id": "2001",
            "friends": [{"name": "Luke Skywalker"}, {"name": "Han Solo"}],
        }
    }
    assert results[2].data == results[3].data == final_data

    assert [result.extensions for result in results] == [None, {"cost": 1}, None, None]
    assert [result.errors for result in results] == [
        None,
        None,
        [{"message": "cannot get name", "path": ["hero", "name"]}],
        None,
    ]


@pytest.mark.asyncio
@pytest.mark.parametrize("server", [incremental_server], indirect=True)
async def test_websocket_incremental(client_and_server):

    session, server = client_and_server

    results = [result async for result in session.execute_incremental(gql(query_str))]

    check_incremental_results(results)

    query_message = json.loads(logged_messages[0])
    assert query_message["type"] == "start"
    assert "@defer" in query_message["payload"]["query"]

    # No stop message sent as the server completed the query
    assert len(logged_messages) == 1


@pytest.mark.asyncio
@pytest.mark.parametrize("graphqlws_server", [incremental_server], indirect=True)
async def test_graphqlws_incremental(client_and_graphqlws_server):

    session, server = client_and_graphqlws_server

    results = [result async for result in session.execute_incremental(gql(query_str))]

    check_incremental_results(results)

    query_message = json.loads(logged_messages[0])
    assert query_message["type"] == "subscribe"
    assert "@stream" in query_message["payload"]["query"]

    # No complete message sent as the server completed the query
    assert len(logged_messages) == 1


@pytest.mark.aiohttp
@pytest.mark.asyncio
@pytest.mark.parametrize("graphqlws_server", [incremental_server], indirect=True)
async def test_aiohttp_websocket_graphqlws_incremental(
    client_and_aiohttp_websocket_graphql_server,
):

    session, server = client_and_aiohttp_websocket_graphql_server

    results = [result async for result in session.execute_incremental(gql(query_str))]

    check_incremental_results(results)


@pytest.mark.aiohttp
@pytest.mark.asyncio
@pytest.mark.parametrize("server", [incremental_server], indirect=True)
async def test_aiohttp_websocket_incremental(aiohttp_client_and_server):

    session, server = aiohttp_client_and_server

    results = [result async for result in session.execute_incremental(gql(query_str))]

    check_incremental_results(results)


@pytest.mark.asyncio
@pytest.mark.parametrize("graphqlws_server", [incremental_server], indirect=True)
async def test_graphqlws_incremental_payloads_with_subscribe_and_execute(
    client_and_graphqlws_server,
):

    session, server = client_and_graphqlws_server

    # Payloads without data are ignored by subscribe
    results = [result async for result in session.subscribe(gql(query_str))]

    assert results == [
        {"hero": {"id": "2001", "friends": [{"name": "Luke Skywalker"}]}}
    ]

    # execute returns the initial payload
    result = await session.execute(gql(query_str))

    assert result == {"hero": {"id": "2001", "friends": [{"name": "Luke Skywalker"}]}}


non_incremental_server = incremental_server_factory(
    [{"data": {"hero": {"id": "2001"}}, "extensions": {"cost": 1}}]
)


@pytest.mark.asyncio
@pytest.mark.parametrize("server", [non_incremental_server], indirect=True)
async def test_websocket_incremental_non_incremental_response(server):
    from gql.transport.websockets import WebsocketsTransport

    url = f"ws://{server.hostname}:{server.port}/graphql"
    transport = WebsocketsTransport(url=url)

    async with Client(transport=transport) as session:
        results = [
            result async for result in session.execute_incremental(gql(query_str))
        ]

    assert len(results) == 1
    assert results[0].data == {"hero": {"id": "2001"}}
    assert results[0].extensions == {"cost": 1}
    assert results[0].has_next is False


server_incremental_stopped_by_client = incremental_server_factory(
    incremental_payloads[:1], complete=False
)


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "server", [server_incremental_stopped_by_client], indirect=True
)
async def test_websocket_incremental_break(client_and_server):

    session, server = client_and_server

    generator = session.execute_incremental(gql(query_str))

    async for result in generator:
        assert result.has_next
        break

    await generator.aclose()

    await wait_for_logged_messages(2)
    assert json.loads(logged_messages[1])["type"] == "stop"


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "graphqlws_server", [server_incremental_stopped_by_client], indirect=True
)
async def test_graphqlws_incremental_break(client_and_graphqlws_server):

    session, server = client_and_graphqlws_server

    generator = session.execute_incremental(gql(query_str))

    async for result in generator:
        assert result.has_next
        break

    await generator.aclose()

    await wait_for_logged_messages(2)
    assert json.loads(logged_messages[1])["type"] == "complete"


invalid_payload_server = incremental_server_factory([{"foo": "bar"}])


@pytest.mark.asyncio
@pytest.mark.parametrize("graphqlws_server", [invalid_payload_server], indirect=True)
async def test_graphqlws_incremental_invalid_payload(client_and_graphqlws_server):

    session, server = client_and_graphqlws_server

    with pytest.raises(TransportProtocolError):
        async for _ in session.execute_incremental(gql(query_str)):
            pass  # pragma: no cover
