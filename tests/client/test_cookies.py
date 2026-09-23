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


def _cookie_store_app(request: httpx.Request) -> httpx.Response:
    if request.url.path == "/set":
        return httpx.Response(200, headers={"set-cookie": "session=abc; Path=/"})
    if request.url.path == "/start":
        return httpx.Response(
            302,
            headers={"set-cookie": "session=abc", "location": "/next"},
        )
    if request.url.path == "/away":
        return httpx.Response(
            302,
            headers={
                "set-cookie": "session=abc",
                "location": "http://other.test/landed",
            },
        )
    return httpx.Response(
        200,
        json={"cookie": request.headers.get("cookie"), "host": request.url.host},
    )


def test_client_cookie_store_persistence() -> None:
    store = httpx.CookieStore()
    client = httpx.Client(
        cookies=store, transport=httpx.MockTransport(_cookie_store_app)
    )
    assert client.cookies is store

    response = client.get("http://example.org/echo")
    assert response.json()["cookie"] is None

    response = client.get("http://example.org/set")
    assert store["session"] == "abc"
    assert client.cookies["session"] == "abc"

    response = client.get("http://example.org/echo")
    assert response.json()["cookie"] == "session=abc"

    response = client.get("http://other.test/echo")
    assert response.json()["cookie"] is None


def test_client_cookie_store_redirect() -> None:
    client = httpx.Client(
        cookies=httpx.CookieStore(),
        transport=httpx.MockTransport(_cookie_store_app),
        follow_redirects=True,
    )
    response = client.get("http://example.org/start")
    assert response.json()["cookie"] == "session=abc"
    assert response.json()["host"] == "example.org"

    response = client.get("http://example.org/away")
    assert response.json()["host"] == "other.test"
    assert response.json()["cookie"] is None
    assert client.cookies["session"] == "abc"


@pytest.mark.anyio
async def test_async_client_cookie_store() -> None:
    store = httpx.CookieStore()
    async with httpx.AsyncClient(
        cookies=store, transport=httpx.MockTransport(_cookie_store_app)
    ) as client:
        assert client.cookies is store
        response = await client.get("https://example.org/set")
        assert response.status_code == 200
        response = await client.get("https://example.org/echo")
        assert response.json()["cookie"] == "session=abc"
        response = await client.get("http://example.org/echo")
        assert response.json()["cookie"] == "session=abc"
