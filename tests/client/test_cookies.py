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
    if request.url.path.endswith("/echo_cookies"):
        return httpx.Response(200, json={"cookies": request.headers.get("cookie")})
    elif request.url.path == "/login":
        login_headers = [
            ("set-cookie", "session=abc; Path=/"),
            ("set-cookie", "scoped=1; Path=/app, shared=2; Domain=example.org"),
        ]
        return httpx.Response(200, headers=login_headers)
    elif request.url.path == "/redirect":
        redirect_headers = {
            "location": "http://sub.example.org/echo_cookies",
            "set-cookie": "redirected=yes; Domain=example.org",
        }
        return httpx.Response(302, headers=redirect_headers)
    else:
        raise NotImplementedError()  # pragma: no cover


def test_client_with_cookie_store() -> None:
    store = httpx.CookieStore()
    client = httpx.Client(
        cookies=store, transport=httpx.MockTransport(cookie_store_app)
    )
    assert client.cookies is store

    client.get("http://example.org/login")
    assert sorted(store) == ["scoped", "session", "shared"]

    response = client.get("http://example.org/app/echo_cookies")
    assert response.json() == {"cookies": "scoped=1; session=abc; shared=2"}

    response = client.get("http://sub.example.org/echo_cookies")
    assert response.json() == {"cookies": "shared=2"}


def test_client_cookie_store_setter() -> None:
    store = httpx.CookieStore({"a": "1"})
    client = httpx.Client(transport=httpx.MockTransport(cookie_store_app))
    client.cookies = store
    assert client.cookies is store

    response = client.get("http://example.org/echo_cookies")
    assert response.json() == {"cookies": "a=1"}


def test_client_cookie_store_across_redirects() -> None:
    store = httpx.CookieStore()
    client = httpx.Client(
        cookies=store,
        follow_redirects=True,
        transport=httpx.MockTransport(cookie_store_app),
    )
    response = client.get("http://example.org/redirect")
    assert response.json() == {"cookies": "redirected=yes"}
    assert store["redirected"] == "yes"


def test_per_request_cookie_store() -> None:
    client = httpx.Client(
        cookies={"a": "1"}, transport=httpx.MockTransport(cookie_store_app)
    )
    store = httpx.CookieStore({"b": "2"})
    with pytest.warns(DeprecationWarning):
        response = client.get("http://example.org/echo_cookies", cookies=store)
    assert response.json() == {"cookies": "a=1; b=2"}
    assert isinstance(client.cookies, httpx.Cookies)

    client = httpx.Client(
        cookies=store, transport=httpx.MockTransport(cookie_store_app)
    )
    with pytest.warns(DeprecationWarning):
        response = client.get("http://example.org/echo_cookies", cookies={"c": "3"})
    assert response.json() == {"cookies": "b=2; c=3"}
    assert list(store) == ["b"]


@pytest.mark.anyio
async def test_async_client_with_cookie_store() -> None:
    store = httpx.CookieStore()
    async with httpx.AsyncClient(
        cookies=store, transport=httpx.MockTransport(cookie_store_app)
    ) as client:
        await client.get("http://example.org/login")
        response = await client.get("http://example.org/echo_cookies")

    assert response.json() == {"cookies": "session=abc; shared=2"}
    assert sorted(store) == ["scoped", "session", "shared"]
