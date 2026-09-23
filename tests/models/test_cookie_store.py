from __future__ import annotations

import time
from http.cookiejar import Cookie, CookieJar

import pytest

import httpx


def extract(
    store: httpx.CookieStore,
    url: str,
    set_cookie: str | list[tuple[bytes, bytes]],
) -> None:
    request = httpx.Request("GET", url)
    if isinstance(set_cookie, str):
        response = httpx.Response(
            200, headers={"set-cookie": set_cookie}, request=request
        )
    else:
        response = httpx.Response(200, headers=set_cookie, request=request)
    store.extract_cookies(response)


def cookie_header(store: httpx.CookieStore, url: str) -> str | None:
    request = httpx.Request("GET", url, cookies=store)
    header = request.headers.get("cookie")
    return None if header is None else str(header)


def test_cookie_store_is_public() -> None:
    assert httpx.CookieStore is not None
    assert "CookieStore" in httpx.__all__


def test_mapping_interface() -> None:
    store = httpx.CookieStore({"name": "value"})
    assert store["name"] == "value"
    assert "name" in store
    assert len(store) == 1
    assert dict(store) == {"name": "value"}
    assert bool(store) is True

    store["other"] = "v"
    assert store.get("other") == "v"
    del store["name"]
    assert "name" not in store
    assert len(store) == 1
    store.delete("other")
    assert len(store) == 0
    assert bool(store) is False
    assert store.get("missing", default="fallback") == "fallback"


def test_limits_validate_types() -> None:
    httpx.CookieStore(max_cookies=0, max_cookies_per_domain=0)
    httpx.CookieStore(max_cookies=None, max_cookies_per_domain=None)

    with pytest.raises(TypeError):
        httpx.CookieStore(max_cookies="1")  # type: ignore[arg-type]
    with pytest.raises(TypeError):
        httpx.CookieStore(max_cookies=1.5)  # type: ignore[arg-type]
    with pytest.raises(TypeError):
        httpx.CookieStore(max_cookies=True)
    with pytest.raises(TypeError):
        httpx.CookieStore(max_cookies_per_domain=False)
    with pytest.raises(ValueError):
        httpx.CookieStore(max_cookies=-1)
    with pytest.raises(ValueError):
        httpx.CookieStore(max_cookies_per_domain=-5)


def test_zero_limits_store_nothing() -> None:
    store = httpx.CookieStore(max_cookies=0)
    store.set("a", "1", domain="a.test")
    assert len(store) == 0

    store = httpx.CookieStore(max_cookies_per_domain=0)
    store.set("a", "1", domain="a.test")
    assert len(store) == 0


def test_evict_oldest_globally_then_per_domain_first() -> None:
    store = httpx.CookieStore(max_cookies=2)
    store.set("a", "1", domain="a.test")
    store.set("b", "2", domain="b.test")
    store.set("c", "3", domain="c.test")
    assert store.get("a", domain="a.test") is None
    assert store.get("b", domain="b.test") == "2"
    assert store.get("c", domain="c.test") == "3"

    store = httpx.CookieStore(max_cookies_per_domain=1)
    store.set("a", "1", domain="a.test")
    store.set("b", "2", domain="b.test")
    store.set("c", "3", domain="a.test")
    assert store.get("a", domain="a.test") is None
    assert store.get("b", domain="b.test") == "2"
    assert store.get("c", domain="a.test") == "3"

    # Per-domain eviction runs before the global limit, so the oldest cookie
    # on another domain is kept when the overflowing domain can drop its own.
    store = httpx.CookieStore(max_cookies=3, max_cookies_per_domain=1)
    store.set("b", "1", domain="b.test")
    store.set("a", "1", domain="a.test")
    store.set("c", "1", domain="c.test")
    store.set("a2", "1", domain="a.test")
    assert len(store) == 3
    assert store.get("b", domain="b.test") == "1"
    assert store.get("a", domain="a.test") is None
    assert store.get("a2", domain="a.test") == "1"
    assert store.get("c", domain="c.test") == "1"


def test_replacement_is_newly_created_for_eviction() -> None:
    store = httpx.CookieStore(max_cookies=2)
    extract(store, "https://example.com/", "a=1")
    extract(store, "https://example.com/", "b=2")
    extract(store, "https://example.com/", "a=9")
    extract(store, "https://example.com/", "c=3")
    assert "b" not in store
    assert store["a"] == "9"
    assert store["c"] == "3"


def test_name_conflict_requires_domain_or_path() -> None:
    store = httpx.CookieStore()
    store.set("name", "1", domain="example.com", path="/one")
    store.set("name", "2", domain="example.org", path="/two")

    with pytest.raises(httpx.CookieConflict):
        store["name"]
    with pytest.raises(httpx.CookieConflict):
        store.get("name")

    assert store.get("name", domain="example.com") == "1"
    assert store.get("name", domain="example.org", path="/two") == "2"
    store.delete("name", domain="example.com", path="/one")
    assert store["name"] == "2"
    store.clear(domain="example.org")
    assert len(store) == 0


def test_clear_path_requires_domain() -> None:
    store = httpx.CookieStore()
    with pytest.raises(AssertionError):
        store.clear(path="/")


def test_send_order_is_longer_path_then_older_creation() -> None:
    store = httpx.CookieStore()
    store.set("root", "1", path="/")
    store.set("mid", "2", path="/foo")
    store.set("deep", "3", path="/foo/bar")
    store.set("root2", "4", path="/")
    assert (
        cookie_header(store, "https://example.com/foo/bar/baz")
        == "deep=3; mid=2; root=1; root2=4"
    )


def test_domain_empty_is_not_host_only() -> None:
    store = httpx.CookieStore()
    store.set("a", "1")
    store.update({"b": "2"})
    store.update([("c", "3")])
    for url in ("https://example.com/x", "http://other.test/y"):
        assert cookie_header(store, url) == "a=1; b=2; c=3"


def test_host_only_and_domain_matching() -> None:
    store = httpx.CookieStore()
    extract(store, "https://www.example.com/a", "id=1")
    assert cookie_header(store, "https://www.example.com/a") == "id=1"
    assert cookie_header(store, "https://www.example.com/a/b") == "id=1"
    assert cookie_header(store, "https://foo.www.example.com/a") is None
    assert cookie_header(store, "https://example.com/a") is None

    extract(store, "https://www.example.com/a", "shared=1; Domain=Example.COM")
    assert cookie_header(store, "https://www.example.com/") == "id=1; shared=1"
    assert cookie_header(store, "https://example.com/") == "shared=1"
    assert cookie_header(store, "https://API.Example.COM/x") == "shared=1"
    assert cookie_header(store, "https://notexample.com/") is None
    assert cookie_header(store, "https://example.org/") is None

    rejected = httpx.CookieStore()
    extract(rejected, "https://www.example.com/", "nope=1; Domain=other.com")
    assert len(rejected) == 0


def test_path_matching_and_default_path() -> None:
    store = httpx.CookieStore()
    store.set("a", "1", path="/sub")
    assert cookie_header(store, "https://example.com/sub") == "a=1"
    assert cookie_header(store, "https://example.com/sub/x") == "a=1"
    assert cookie_header(store, "https://example.com/submarine") is None
    assert cookie_header(store, "https://example.com/") is None

    defaulted = httpx.CookieStore()
    extract(defaulted, "https://example.com/foo/bar/baz?q=1", "p=1")
    assert cookie_header(defaulted, "https://example.com/foo/bar") == "p=1"
    assert cookie_header(defaulted, "https://example.com/foo/bar/x") == "p=1"
    assert cookie_header(defaulted, "https://example.com/foo/barb") is None
    assert cookie_header(defaulted, "https://example.com/foo") is None

    extract(defaulted, "https://example.com/foo/bar/baz", "q=1; Path=noslash")
    extract(defaulted, "https://example.com/foo/bar/baz", "r=1; Path=")
    assert cookie_header(defaulted, "https://example.com/foo/bar") == "p=1; q=1; r=1"

    trailing = httpx.CookieStore()
    extract(trailing, "https://example.com/foo/bar/", "s=1")
    assert cookie_header(trailing, "https://example.com/foo/bar/baz") == "s=1"
    assert cookie_header(trailing, "https://example.com/foo/bar") == "s=1"
    assert cookie_header(trailing, "https://example.com/foo/barbar") is None


def test_secure_flag_and_prefixes() -> None:
    store = httpx.CookieStore()
    extract(store, "http://example.com/", "a=1; Secure")
    assert store["a"] == "1"
    assert cookie_header(store, "http://example.com/") is None
    assert cookie_header(store, "https://example.com/") == "a=1"

    secure_prefix = httpx.CookieStore()
    extract(secure_prefix, "http://example.com/", "__Secure-a=1; Secure")
    extract(secure_prefix, "https://example.com/", "__Secure-b=1")
    extract(
        secure_prefix,
        "https://example.com/",
        "__Secure-c=1; Secure; Domain=example.com",
    )
    assert len(secure_prefix) == 1
    assert secure_prefix["__Secure-c"] == "1"
    assert cookie_header(secure_prefix, "https://www.example.com/") == "__Secure-c=1"
    assert cookie_header(secure_prefix, "http://www.example.com/") is None

    host_prefix = httpx.CookieStore()
    extract(host_prefix, "https://example.com/", "__Host-ok=1; Secure; Path=/")
    extract(host_prefix, "https://example.com/foo/bar", "__Host-path=1; Secure")
    extract(
        host_prefix,
        "https://example.com/",
        "__Host-dom=1; Secure; Path=/; Domain=example.com",
    )
    extract(host_prefix, "http://example.com/", "__Host-http=1; Secure; Path=/")
    extract(host_prefix, "https://example.com/", "__Host-nosecure=1; Path=/")
    assert list(host_prefix) == ["__Host-ok"]
    assert cookie_header(host_prefix, "https://example.com/any") == "__Host-ok=1"
    assert cookie_header(host_prefix, "https://www.example.com/any") is None
    assert cookie_header(host_prefix, "http://example.com/any") is None

    root_default = httpx.CookieStore()
    extract(root_default, "https://example.com/file", "__Host-root=1; Secure")
    assert root_default["__Host-root"] == "1"


def test_expiry_rules(monkeypatch: pytest.MonkeyPatch) -> None:
    store = httpx.CookieStore()
    extract(store, "https://example.com/", "a=1")
    extract(store, "https://example.com/", "a=2; Max-Age=0")
    assert "a" not in store

    extract(store, "https://example.com/", "a=1")
    extract(store, "https://example.com/", "a=2; Max-Age=-1")
    assert "a" not in store

    extract(store, "https://example.com/", "a=1")
    extract(
        store,
        "https://example.com/",
        "a=2; Expires=Thu, 01 Jan 1970 00:00:01 GMT",
    )
    assert "a" not in store

    extract(
        store,
        "https://example.com/",
        "keep=1; Max-Age=3600; Expires=Thu, 01 Jan 1970 00:00:01 GMT",
    )
    assert store["keep"] == "1"

    extract(store, "https://example.com/", "session=1; Expires=not-a-date")
    assert store["session"] == "1"

    now = 1_700_000_000.0
    monkeypatch.setattr(time, "time", lambda: now)
    # Patch the module global used by CookieStore, not just time.time lookups
    # that have already been bound.
    monkeypatch.setattr("httpx._cookie_store.time.time", lambda: now)
    timed = httpx.CookieStore()
    extract(timed, "https://example.com/", "t=1; Max-Age=5")
    assert timed["t"] == "1"
    monkeypatch.setattr("httpx._cookie_store.time.time", lambda: now + 4)
    assert cookie_header(timed, "https://example.com/") == "t=1"
    monkeypatch.setattr("httpx._cookie_store.time.time", lambda: now + 5)
    assert "t" not in timed
    assert cookie_header(timed, "https://example.com/") is None


def test_deletion_matches_name_domain_and_path() -> None:
    store = httpx.CookieStore()
    extract(store, "https://www.example.com/", "a=host")
    extract(store, "https://www.example.com/", "a=dom; Domain=example.com")
    extract(store, "https://www.example.com/", "a=gone; Max-Age=0")
    assert store.get("a", domain="www.example.com") is None
    assert store.get("a", domain="example.com") == "dom"

    extract(store, "https://example.com/foo", "p=1; Path=/foo")
    extract(store, "https://example.com/foo", "p=1; Path=/bar")
    extract(store, "https://example.com/foo", "p=x; Path=/foo; Max-Age=0")
    assert store.get("p", path="/foo") is None
    assert store.get("p", path="/bar") == "1"


def test_parse_set_cookie_header_values() -> None:
    store = httpx.CookieStore()
    combined = (
        "1P_JAR=2020-08-09-18; expires=Tue, 08-Sep-2099 18:33:35 GMT; "
        "path=/; domain=.example.org; Secure, "
        "NID=204=abc; expires=Mon, 08-Feb-2099 18:33:35 GMT; path=/; "
        "domain=.example.org; HttpOnly"
    )
    extract(store, "https://www.example.org/", combined)
    assert store.get("1P_JAR", domain="example.org") == "2020-08-09-18"
    assert store.get("NID", domain="example.org") == "204=abc"
    assert cookie_header(store, "https://example.org/") == (
        "1P_JAR=2020-08-09-18; NID=204=abc"
    )
    assert cookie_header(store, "http://example.org/") == "NID=204=abc"

    messy_headers = [
        (b"set-cookie", b"not a cookie"),
        (b"set-cookie", b"a=1"),
        (b"set-cookie", b"b=2; Domain"),
        (b"set-cookie", b"c=3; Domain="),
        (b"set-cookie", b"d=4; Max-Age"),
        (b"set-cookie", b"e=5; Max-Age="),
        (b"set-cookie", b"f=6; Expires"),
        (b"set-cookie", b"g=7; Expires="),
        (b"set-cookie", b"h=8; Max-Age=nope"),
        (b"set-cookie", b"i=9; Expires=not-a-date; SameSite=Lax; HttpOnly"),
        (b"set-cookie", b"j="),
        (b"set-cookie", b""),
    ]
    messy = httpx.CookieStore()
    extract(messy, "https://example.com/", messy_headers)
    assert sorted(messy) == ["a", "i", "j"]
    assert messy["j"] == ""
    assert messy["i"] == "9"

    kept = httpx.CookieStore()
    extract(kept, "https://example.com/", "a=1")
    extract(kept, "https://example.com/", "a=2; Domain")
    extract(kept, "https://example.com/", "a=2; Expires=")
    extract(kept, "https://example.com/", "a=2; Max-Age=")
    assert kept["a"] == "1"


def test_ip_address_does_not_suffix_match() -> None:
    store = httpx.CookieStore()
    extract(store, "http://127.0.0.1/", "a=1")
    assert cookie_header(store, "http://127.0.0.1/x") == "a=1"
    assert cookie_header(store, "http://127.0.0.2/x") is None

    rejected = httpx.CookieStore()
    extract(rejected, "http://127.0.0.1/", "a=1; Domain=0.0.1")
    assert len(rejected) == 0

    exact = httpx.CookieStore()
    extract(exact, "http://127.0.0.1/", "a=1; Domain=127.0.0.1")
    assert cookie_header(exact, "http://127.0.0.1/") == "a=1"
    assert cookie_header(exact, "http://127.0.0.2/") is None


def test_update_accepts_cookie_inputs() -> None:
    original = httpx.CookieStore()
    extract(original, "https://www.example.com/", "id=1; Secure")
    copied = httpx.CookieStore()
    copied.update(original)
    assert cookie_header(copied, "https://www.example.com/") == "id=1"
    assert cookie_header(copied, "http://www.example.com/") is None
    assert cookie_header(copied, "https://example.com/") is None

    cookies = httpx.Cookies()
    cookies.set("n", "v", domain="example.org", path="/only")
    from_cookies = httpx.CookieStore()
    from_cookies.update(cookies)
    assert cookie_header(from_cookies, "http://www.example.org/only") == "n=v"
    assert cookie_header(from_cookies, "http://example.org/only/x") == "n=v"
    assert cookie_header(from_cookies, "http://example.org/onlyother") is None
    assert cookie_header(from_cookies, "http://other.org/only") is None

    jar = CookieJar()
    jar.set_cookie(
        Cookie(
            version=0,
            name="s",
            value="1",
            port=None,
            port_specified=False,
            domain="example.org",
            domain_specified=True,
            domain_initial_dot=False,
            path="/",
            path_specified=True,
            secure=True,
            expires=None,
            discard=True,
            comment=None,
            comment_url=None,
            rest={"HttpOnly": ""},
            rfc2109=False,
        )
    )
    from_jar = httpx.CookieStore()
    from_jar.update(jar)
    assert cookie_header(from_jar, "https://example.org/") == "s=1"
    assert cookie_header(from_jar, "http://example.org/") is None

    legacy = httpx.Cookies(original)
    assert legacy.get("id") == "1"


def echo_app(request: httpx.Request) -> httpx.Response:
    if request.url.path == "/set":
        return httpx.Response(200, headers={"set-cookie": "id=1"})
    if request.url.path == "/redirect":
        return httpx.Response(
            302,
            headers={
                "set-cookie": "id=1",
                "location": "https://example.com/land",
            },
        )
    if request.url.path == "/redirect-same":
        return httpx.Response(
            302,
            headers={
                "set-cookie": "id=1",
                "location": "https://www.example.com/land",
            },
        )
    return httpx.Response(200, json={"cookie": request.headers.get("cookie")})


def test_client_uses_cookie_store() -> None:
    store = httpx.CookieStore()
    with httpx.Client(cookies=store, transport=httpx.MockTransport(echo_app)) as client:
        assert client.cookies is store
        client.get("https://www.example.com/set")
        assert store["id"] == "1"
        same = client.get("https://www.example.com/echo")
        assert same.json() == {"cookie": "id=1"}
        other = client.get("https://example.com/echo")
        assert other.json() == {"cookie": None}
        child = client.get("https://foo.www.example.com/echo")
        assert child.json() == {"cookie": None}

    with httpx.Client(transport=httpx.MockTransport(echo_app)) as client:
        client.cookies = store
        assert client.cookies is store


def test_client_redirect_keeps_host_only_scope() -> None:
    store = httpx.CookieStore()
    with httpx.Client(
        cookies=store,
        transport=httpx.MockTransport(echo_app),
        follow_redirects=True,
    ) as client:
        crossed = client.get("https://www.example.com/redirect")
        assert crossed.json() == {"cookie": None}
        assert store["id"] == "1"

    store = httpx.CookieStore()
    with httpx.Client(
        cookies=store,
        transport=httpx.MockTransport(echo_app),
        follow_redirects=True,
    ) as client:
        stayed = client.get("https://www.example.com/redirect-same")
        assert stayed.json() == {"cookie": "id=1"}


class _AsyncEcho(httpx.AsyncBaseTransport):
    async def handle_async_request(self, request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, headers={"set-cookie": "id=1"})


@pytest.mark.anyio
async def test_async_client_cookie_store() -> None:
    store = httpx.CookieStore()
    async with httpx.AsyncClient(cookies=store, transport=_AsyncEcho()) as client:
        response = await client.get("https://example.com/set")
        assert response.status_code == 200
        assert client.cookies is store
    assert store["id"] == "1"
    assert cookie_header(store, "https://example.com/next") == "id=1"
    assert cookie_header(store, "https://other.example.com/next") is None
