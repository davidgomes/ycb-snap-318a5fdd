import pytest

import httpx


def _app(request: httpx.Request) -> httpx.Response:
    if request.url.path == "/set":
        return httpx.Response(200, headers={"set-cookie": "id=1"})
    if request.url.path == "/set-domain":
        return httpx.Response(
            200, headers={"set-cookie": "shared=1; Domain=example.org; Path=/"}
        )
    if request.url.path == "/redirect":
        return httpx.Response(
            302,
            headers={"location": "/next", "set-cookie": "hop=1"},
        )
    return httpx.Response(200, json={"cookie": request.headers.get("cookie")})


def test_client_cookie_store_is_shared_and_host_only() -> None:
    store = httpx.CookieStore()
    client = httpx.Client(cookies=store, transport=httpx.MockTransport(_app))
    client.get("http://example.org/set")

    assert client.cookies is store
    assert store["id"] == "1"
    assert client.get("http://example.org/echo").json() == {"cookie": "id=1"}
    assert client.get("http://other.org/echo").json() == {"cookie": None}


def test_client_cookie_store_domain_and_redirect() -> None:
    store = httpx.CookieStore()
    client = httpx.Client(
        cookies=store,
        transport=httpx.MockTransport(_app),
        follow_redirects=True,
    )
    client.get("https://www.example.org/set-domain")
    assert client.get("https://example.org/echo").json() == {"cookie": "shared=1"}
    assert client.get("https://other.org/echo").json() == {"cookie": None}

    store.clear()
    redirected = client.get("http://example.org/redirect")
    assert redirected.json() == {"cookie": "hop=1"}
    assert client.get("http://example.org/echo").json() == {"cookie": "hop=1"}
    assert client.get("http://www.example.org/echo").json() == {"cookie": None}


@pytest.mark.anyio
async def test_async_client_accepts_cookie_store() -> None:
    store = httpx.CookieStore({"session": "abc"})
    transport = httpx.MockTransport(_app)
    async with httpx.AsyncClient(cookies=store, transport=transport) as client:
        response = await client.get("https://example.org/echo")
    assert response.json() == {"cookie": "session=abc"}
    assert store["session"] == "abc"
