import http.cookiejar

import pytest

import httpx


def make_response(url: str, *set_cookies: str) -> httpx.Response:
    return httpx.Response(
        200,
        headers=[("Set-Cookie", value) for value in set_cookies],
        request=httpx.Request("GET", url),
    )


def cookie_header(store: httpx.CookieStore, url: str) -> str | None:
    request = httpx.Request("GET", url)
    store.set_cookie_header(request)
    value: str | None = request.headers.get("Cookie")
    return value


def test_limits_validation() -> None:
    with pytest.raises(TypeError):
        httpx.CookieStore(max_cookies="1")  # type: ignore
    with pytest.raises(TypeError):
        httpx.CookieStore(max_cookies_per_domain=1.5)  # type: ignore
    with pytest.raises(ValueError):
        httpx.CookieStore(max_cookies=-1)
    with pytest.raises(ValueError):
        httpx.CookieStore(max_cookies_per_domain=-1)
    httpx.CookieStore(max_cookies=0, max_cookies_per_domain=None)


def test_extract_combined_header_with_expires_comma() -> None:
    store = httpx.CookieStore()
    store.extract_cookies(
        make_response(
            "https://example.com/",
            "a=1; Expires=Wed, 21 Oct 2099 07:28:00 GMT, b=2; Path=/",
        )
    )
    assert cookie_header(store, "https://example.com/") == "a=1; b=2"


def test_extract_ignores_malformed_and_valueless_attributes() -> None:
    store = httpx.CookieStore()
    store.extract_cookies(
        make_response(
            "https://example.com/",
            "",
            "novalue",
            "=nameless",
            "d=1; Domain",
            "m=1; Max-Age",
            "e=1; Expires",
            "empty=",
            "bad_expires=1; Expires=not-a-date; Unknown=thing",
        )
    )
    assert list(store) == ["empty", "bad_expires"]
    assert store["empty"] == ""


def test_host_only_and_domain_cookies() -> None:
    store = httpx.CookieStore()
    store.extract_cookies(make_response("https://example.com/", "host=1"))
    store.extract_cookies(
        make_response("https://www.Example.com/", "dom=1; Domain=EXAMPLE.com")
    )
    store.extract_cookies(make_response("https://example.com/", "evil=1; Domain=x.org"))
    assert cookie_header(store, "https://example.com/") == "host=1; dom=1"
    assert cookie_header(store, "https://sub.example.com/") == "dom=1"
    assert cookie_header(store, "https://other.com/") is None


def test_path_defaults_and_matching() -> None:
    store = httpx.CookieStore()
    store.extract_cookies(
        make_response(
            "https://example.com/sub/page",
            "default=1",
            "rel=1; Path=relative",
            "root=1; Path=/",
        )
    )
    assert store.get("default", path="/sub") == "1"
    assert store.get("rel", path="/sub") == "1"
    assert cookie_header(store, "https://example.com/sub") == "default=1; rel=1; root=1"
    assert (
        cookie_header(store, "https://example.com/sub/x") == "default=1; rel=1; root=1"
    )
    assert cookie_header(store, "https://example.com/submarine") == "root=1"


def test_secure_and_prefixes() -> None:
    store = httpx.CookieStore()
    store.extract_cookies(
        make_response(
            "http://example.com/",
            "s=1; Secure",
            "__Secure-a=1; Secure",
            "__Host-a=1; Secure; Path=/",
        )
    )
    assert list(store) == ["s"]
    assert cookie_header(store, "http://example.com/") is None
    assert cookie_header(store, "https://example.com/") == "s=1"

    store.extract_cookies(
        make_response(
            "https://example.com/",
            "__Secure-a=1; Secure",
            "__Secure-b=1",
            "__Host-a=1; Secure; Path=/",
            "__Host-b=1; Secure; Path=/; Domain=example.com",
            "__Host-c=1; Secure; Path=/x",
        )
    )
    assert list(store) == ["s", "__Secure-a", "__Host-a"]


def test_expiry() -> None:
    store = httpx.CookieStore()
    url = "https://example.com/"
    store.extract_cookies(make_response(url, "a=1", "b=1", "c=1"))
    store.extract_cookies(
        make_response(
            url,
            "a=; Max-Age=0",
            "b=; Expires=Wed, 21 Oct 2015 07:28:00 GMT",
            "c=2; Max-Age=100; Expires=Wed, 21 Oct 2015 07:28:00 GMT",
            "d=1; Max-Age=-1",
        )
    )
    assert dict(store) == {"c": "2"}


def test_replacement_and_ordering() -> None:
    store = httpx.CookieStore()
    url = "https://example.com/a/b"
    store.extract_cookies(make_response(url, "x=1; Path=/", "y=1; Path=/", "z=1"))
    store.extract_cookies(make_response(url, "x=2; Path=/"))
    assert list(store) == ["y", "z", "x"]
    assert cookie_header(store, url) == "z=1; y=1; x=2"


def test_eviction() -> None:
    store = httpx.CookieStore(max_cookies=3, max_cookies_per_domain=2)
    store.extract_cookies(make_response("https://a.com/", "a=1", "b=1", "c=1"))
    assert list(store) == ["b", "c"]
    store.extract_cookies(make_response("https://b.com/", "d=1", "e=1"))
    assert list(store) == ["c", "d", "e"]
    store.extract_cookies(make_response("https://a.com/", "c=2"))
    assert list(store) == ["d", "e", "c"]


def test_cookie_conflict() -> None:
    store = httpx.CookieStore()
    store.set("name", "1", domain="a.com")
    store.set("name", "2", domain="b.com")
    with pytest.raises(httpx.CookieConflict):
        store["name"]
    assert store.get("name", domain="b.com") == "2"
    store.delete("name", domain="a.com")
    assert store["name"] == "2"


def test_mapping_api() -> None:
    store = httpx.CookieStore()
    store["a"] = "1"
    store.set("b", "2", domain="example.com", path="/p")
    assert len(store) == 2
    assert store.get("missing", "default") == "default"
    del store["a"]
    with pytest.raises(KeyError):
        del store["a"]
    store.clear(domain="example.com")
    assert not store


def test_update_accepts_all_cookie_types() -> None:
    jar = http.cookiejar.CookieJar()
    httpx.Cookies(jar).set("jar", "1")
    store = httpx.CookieStore()
    store.update(httpx.CookieStore({"store": "1"}))
    store.update(httpx.Cookies({"cookies": "1"}))
    store.update(jar)
    store.update({"dict": "1"})
    store.update([("list", "1")])
    assert (
        cookie_header(store, "http://anywhere.org/x")
        == "store=1; cookies=1; jar=1; dict=1; list=1"
    )


def test_client_persists_cookie_store() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/login":
            return httpx.Response(
                302, headers={"Set-Cookie": "session=abc", "Location": "/home"}
            )
        return httpx.Response(200, json={"cookie": request.headers.get("Cookie")})

    store = httpx.CookieStore()
    transport = httpx.MockTransport(handler)
    with httpx.Client(
        transport=transport, cookies=store, follow_redirects=True
    ) as client:
        response = client.get("http://example.com/login")
        assert response.json() == {"cookie": "session=abc"}
        assert client.cookies is store
        request = client.build_request(
            "GET", "http://example.com/", cookies={"extra": "1"}
        )
        assert request.headers["Cookie"] == "session=abc; extra=1"
        response = client.get("http://other.com/")
        assert response.json() == {"cookie": None}
    assert dict(store) == {"session": "abc"}
