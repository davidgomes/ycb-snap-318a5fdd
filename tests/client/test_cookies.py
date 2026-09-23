from http.cookiejar import Cookie, CookieJar

import pytest

import httpx


def get_and_set_cookies(request: httpx.Request) -> httpx.Response:
    if request.url.path == "/echo_cookies":
        data = {"cookies": request.headers.get("cookie")}
        return httpx.Response(200, json=data)
    elif request.url.path == "/set_cookie":
        return httpx.Response(200, headers={"set-cookie": "example-name=example-value"})
    else:
        raise NotImplementedError()  # pragma: no cover


def test_set_cookie() -> None:
    """
    Send a request including a cookie.
    """
    url = "http://example.org/echo_cookies"
    cookies = {"example-name": "example-value"}

    client = httpx.Client(
        cookies=cookies, transport=httpx.MockTransport(get_and_set_cookies)
    )
    response = client.get(url)

    assert response.status_code == 200
    assert response.json() == {"cookies": "example-name=example-value"}


def test_set_per_request_cookie_is_deprecated() -> None:
    """
    Sending a request including a per-request cookie is deprecated.
    """
    url = "http://example.org/echo_cookies"
    cookies = {"example-name": "example-value"}

    client = httpx.Client(transport=httpx.MockTransport(get_and_set_cookies))
    with pytest.warns(DeprecationWarning):
        response = client.get(url, cookies=cookies)

    assert response.status_code == 200
    assert response.json() == {"cookies": "example-name=example-value"}


def test_set_cookie_with_cookiejar() -> None:
    """
    Send a request including a cookie, using a `CookieJar` instance.
    """

    url = "http://example.org/echo_cookies"
    cookies = CookieJar()
    cookie = Cookie(
        version=0,
        name="example-name",
        value="example-value",
        port=None,
        port_specified=False,
        domain="",
        domain_specified=False,
        domain_initial_dot=False,
        path="/",
        path_specified=True,
        secure=False,
        expires=None,
        discard=True,
        comment=None,
        comment_url=None,
        rest={"HttpOnly": ""},
        rfc2109=False,
    )
    cookies.set_cookie(cookie)

    client = httpx.Client(
        cookies=cookies, transport=httpx.MockTransport(get_and_set_cookies)
    )
    response = client.get(url)

    assert response.status_code == 200
    assert response.json() == {"cookies": "example-name=example-value"}


def test_setting_client_cookies_to_cookiejar() -> None:
    """
    Send a request including a cookie, using a `CookieJar` instance.
    """

    url = "http://example.org/echo_cookies"
    cookies = CookieJar()
    cookie = Cookie(
        version=0,
        name="example-name",
        value="example-value",
        port=None,
        port_specified=False,
        domain="",
        domain_specified=False,
        domain_initial_dot=False,
        path="/",
        path_specified=True,
        secure=False,
        expires=None,
        discard=True,
        comment=None,
        comment_url=None,
        rest={"HttpOnly": ""},
        rfc2109=False,
    )
    cookies.set_cookie(cookie)

    client = httpx.Client(
        cookies=cookies, transport=httpx.MockTransport(get_and_set_cookies)
    )
    response = client.get(url)

    assert response.status_code == 200
    assert response.json() == {"cookies": "example-name=example-value"}


def test_set_cookie_with_cookies_model() -> None:
    """
    Send a request including a cookie, using a `Cookies` instance.
    """

    url = "http://example.org/echo_cookies"
    cookies = httpx.Cookies()
    cookies["example-name"] = "example-value"

    client = httpx.Client(transport=httpx.MockTransport(get_and_set_cookies))
    client.cookies = cookies
    response = client.get(url)

    assert response.status_code == 200
    assert response.json() == {"cookies": "example-name=example-value"}


def test_get_cookie() -> None:
    url = "http://example.org/set_cookie"

    client = httpx.Client(transport=httpx.MockTransport(get_and_set_cookies))
    response = client.get(url)

    assert response.status_code == 200
    assert response.cookies["example-name"] == "example-value"
    assert client.cookies["example-name"] == "example-value"


def test_cookie_persistence() -> None:
    """
    Ensure that Client instances persist cookies between requests.
    """
    client = httpx.Client(transport=httpx.MockTransport(get_and_set_cookies))

    response = client.get("http://example.org/echo_cookies")
    assert response.status_code == 200
    assert response.json() == {"cookies": None}

    response = client.get("http://example.org/set_cookie")
    assert response.status_code == 200
    assert response.cookies["example-name"] == "example-value"
    assert client.cookies["example-name"] == "example-value"

    response = client.get("http://example.org/echo_cookies")
    assert response.status_code == 200
    assert response.json() == {"cookies": "example-name=example-value"}


def cookie_store_app(request: httpx.Request) -> httpx.Response:
    if request.url.path == "/set_cookies":
        headers = [
            ("set-cookie", "host=1"),
            ("set-cookie", "domain=2; Domain=example.org"),
            ("set-cookie", "sub=3; Path=/sub"),
            ("set-cookie", "secure=4; Secure"),
        ]
        return httpx.Response(200, headers=headers)
    elif request.url.path == "/redirect":
        return httpx.Response(
            302, headers={"location": "/echo_cookies", "set-cookie": "redirect=1"}
        )
    return httpx.Response(200, json={"cookies": request.headers.get("cookie")})


def test_client_with_cookie_store() -> None:
    """
    Cookies are extracted into, and sent from, the client's `CookieStore`.
    """
    store = httpx.CookieStore()
    client = httpx.Client(
        cookies=store, transport=httpx.MockTransport(cookie_store_app)
    )
    assert client.cookies is store

    client.get("https://example.org/set_cookies")
    assert dict(store) == {"host": "1", "domain": "2", "sub": "3", "secure": "4"}

    response = client.get("https://example.org/sub/page")
    assert response.json() == {"cookies": "sub=3; host=1; domain=2; secure=4"}

    response = client.get("http://example.org/submarine")
    assert response.json() == {"cookies": "host=1; domain=2"}

    response = client.get("https://www.example.org/sub")
    assert response.json() == {"cookies": "domain=2"}

    response = client.get("https://example.com/")
    assert response.json() == {"cookies": None}


def test_setting_client_cookies_to_cookie_store() -> None:
    store = httpx.CookieStore()
    store.set("example-name", "example-value")

    client = httpx.Client(transport=httpx.MockTransport(cookie_store_app))
    client.cookies = store
    assert client.cookies is store

    response = client.get("http://example.org/echo_cookies")
    assert response.json() == {"cookies": "example-name=example-value"}


def test_cookie_store_on_redirect() -> None:
    store = httpx.CookieStore()
    client = httpx.Client(
        cookies=store, transport=httpx.MockTransport(cookie_store_app)
    )

    response = client.get("https://example.org/redirect", follow_redirects=True)
    assert response.json() == {"cookies": "redirect=1"}
    assert dict(store) == {"redirect": "1"}


def test_per_request_cookie_store_is_merged() -> None:
    store = httpx.CookieStore()
    store.set("request", "2")

    client = httpx.Client(
        cookies={"client": "1"}, transport=httpx.MockTransport(cookie_store_app)
    )
    with pytest.warns(DeprecationWarning):
        response = client.get("http://example.org/echo_cookies", cookies=store)

    assert response.json() == {"cookies": "client=1; request=2"}
    assert dict(client.cookies) == {"client": "1"}


def test_per_request_cookies_are_merged_with_client_cookie_store() -> None:
    store = httpx.CookieStore()
    store.set("client", "1")

    client = httpx.Client(
        cookies=store, transport=httpx.MockTransport(cookie_store_app)
    )
    with pytest.warns(DeprecationWarning):
        response = client.get(
            "http://example.org/echo_cookies", cookies={"request": "2"}
        )

    assert response.json() == {"cookies": "client=1; request=2"}
    assert dict(store) == {"client": "1"}


@pytest.mark.anyio
async def test_async_client_with_cookie_store() -> None:
    store = httpx.CookieStore()
    transport = httpx.MockTransport(cookie_store_app)
    async with httpx.AsyncClient(cookies=store, transport=transport) as client:
        await client.get("https://example.org/set_cookies")
        response = await client.get("https://example.org/sub/page")

    assert response.json() == {"cookies": "sub=3; host=1; domain=2; secure=4"}
    assert client.cookies is store
