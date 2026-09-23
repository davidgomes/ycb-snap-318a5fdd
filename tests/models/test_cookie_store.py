from http.cookiejar import Cookie, CookieJar

import pytest

import httpx


def _response(url: str, set_cookie: str | list[str]) -> httpx.Response:
    if isinstance(set_cookie, str):
        headers: list[tuple[str, str]] = [("set-cookie", set_cookie)]
    else:
        headers = [("set-cookie", value) for value in set_cookie]
    return httpx.Response(200, headers=headers, request=httpx.Request("GET", url))


def _set(store: httpx.CookieStore, url: str, set_cookie: str | list[str]) -> None:
    store.extract_cookies(_response(url, set_cookie))


def _cookie_header(store: httpx.CookieStore, url: str) -> str | None:
    request = httpx.Request("GET", url)
    store.set_cookie_header(request)
    return request.headers.get("cookie")


def test_cookie_store_mapping_round_trip() -> None:
    store = httpx.CookieStore({"name": "value"})
    assert store["name"] == "value"
    assert "name" in store
    assert len(store) == 1
    assert dict(store) == {"name": "value"}
    assert bool(store) is True

    store["other"] = ""
    assert store["other"] == ""

    del store["name"]
    assert "name" not in store
    assert len(store) == 1
    assert bool(store) is True

    store.clear()
    assert len(store) == 0
    assert bool(store) is False


def test_limits_validate_types() -> None:
    httpx.CookieStore(max_cookies=0, max_cookies_per_domain=0)
    httpx.CookieStore(max_cookies=None, max_cookies_per_domain=None)

    with pytest.raises(TypeError):
        httpx.CookieStore(max_cookies=1.5)  # type: ignore[arg-type]
    with pytest.raises(TypeError):
        httpx.CookieStore(max_cookies="2")  # type: ignore[arg-type]
    with pytest.raises(TypeError):
        httpx.CookieStore(max_cookies_per_domain=True)  # type: ignore[arg-type]
    with pytest.raises(ValueError):
        httpx.CookieStore(max_cookies=-1)
    with pytest.raises(ValueError):
        httpx.CookieStore(max_cookies_per_domain=-1)


def test_evicts_oldest_per_domain_then_global() -> None:
    store = httpx.CookieStore(max_cookies=2, max_cookies_per_domain=2)
    store.set("a", "1", domain="one.test")
    store.set("b", "1", domain="one.test")
    store.set("c", "1", domain="two.test")
    assert "a" not in store
    assert store.get("b", domain="one.test") == "1"
    assert store.get("c", domain="two.test") == "1"

    store = httpx.CookieStore(max_cookies=5, max_cookies_per_domain=1)
    store.set("a", "1", domain="one.test")
    store.set("b", "2", domain="one.test")
    store.set("c", "3", domain="two.test")
    assert store.get("a", domain="one.test") is None
    assert store.get("b", domain="one.test") == "2"
    assert store.get("c", domain="two.test") == "3"

    store = httpx.CookieStore(max_cookies=1, max_cookies_per_domain=2)
    store.set("a", "1", domain="one.test")
    store.set("b", "1", domain="two.test")
    assert "a" not in store
    assert store.get("b", domain="two.test") == "1"


def test_replacement_is_newly_created_for_eviction() -> None:
    store = httpx.CookieStore(max_cookies=2)
    _set(store, "https://example.com/", "a=1; Path=/")
    _set(store, "https://example.com/", "b=1; Path=/")
    _set(store, "https://example.com/", "a=2; Path=/")
    _set(store, "https://example.com/", "c=1; Path=/")
    assert "b" not in store
    assert store["a"] == "2"
    assert store["c"] == "1"


def test_set_cookie_parsing_rules() -> None:
    store = httpx.CookieStore()
    _set(
        store,
        "https://www.example.com/foo/bar",
        [
            "empty=; Path=/",
            "not-a-cookie",
            "   ",
            "=novalue",
            "skip=1; Domain",
            "skip=1; Domain=",
            "skip=1; Max-Age",
            "skip=1; Max-Age=",
            "skip=1; Expires",
            "skip=1; Expires=",
            "kept=1; Unknown=yes; HttpOnly; Path=/",
        ],
    )
    assert list(store) == ["empty", "kept"]
    assert store["empty"] == ""
    assert store["kept"] == "1"

    combined = httpx.CookieStore()
    _set(
        combined,
        "https://example.com/",
        "a=1; Expires=Tue, 08-Sep-2099 18:33:35 GMT; Path=/, b=2; Path=/",
    )
    assert combined["a"] == "1"
    assert combined["b"] == "2"

    equals = httpx.CookieStore()
    _set(equals, "https://example.com/", "a=b=c, d=e; Path=/")
    assert equals["a"] == "b=c"
    assert equals["d"] == "e"

    dotted = httpx.CookieStore()
    _set(dotted, "https://www.example.com/", "n=1; Domain=.Example.COM; Path=/")
    assert _cookie_header(dotted, "https://a.example.com/") == "n=1"

    single = httpx.CookieStore()
    _set(
        single,
        "https://example.com/",
        "a=1; Expires=Tue, 08-Sep-2099 18:33:35 GMT; Path=/",
    )
    assert single["a"] == "1"


def test_domain_and_path_matching() -> None:
    store = httpx.CookieStore()
    _set(store, "https://www.example.com/foo/bar", "host=1")
    assert _cookie_header(store, "https://www.example.com/foo/bar") == "host=1"
    assert _cookie_header(store, "https://www.example.com/foo") == "host=1"
    assert _cookie_header(store, "https://www.example.com/") is None
    assert _cookie_header(store, "https://example.com/foo/bar") is None
    assert _cookie_header(store, "https://sub.www.example.com/foo/bar") is None

    domain_store = httpx.CookieStore()
    _set(
        domain_store,
        "https://www.Example.com/",
        "shared=1; Domain=EXAMPLE.com; Path=/",
    )
    assert _cookie_header(domain_store, "https://www.example.com/") == "shared=1"
    assert _cookie_header(domain_store, "https://example.com/") == "shared=1"
    assert _cookie_header(domain_store, "https://other.example.com/x") == "shared=1"
    assert _cookie_header(domain_store, "https://example.org/") is None
    assert _cookie_header(domain_store, "https://notexample.com/") is None

    rejected = httpx.CookieStore()
    _set(rejected, "https://evil.com/", "nope=1; Domain=example.com")
    assert len(rejected) == 0

    paths = httpx.CookieStore()
    paths.set("n", "1", path="/sub")
    assert _cookie_header(paths, "https://example.com/sub") == "n=1"
    assert _cookie_header(paths, "https://example.com/sub/x") == "n=1"
    assert _cookie_header(paths, "https://example.com/submarine") is None

    defaulted = httpx.CookieStore()
    _set(defaulted, "https://example.com/foo/bar", "p=1; Path=relative")
    assert _cookie_header(defaulted, "https://example.com/foo/baz") == "p=1"
    assert _cookie_header(defaulted, "https://example.com/foo") == "p=1"
    assert _cookie_header(defaulted, "https://example.com/") is None


def test_secure_and_prefixes() -> None:
    secure = httpx.CookieStore()
    _set(secure, "https://example.com/", "a=1; Secure; Path=/")
    assert _cookie_header(secure, "https://example.com/") == "a=1"
    assert _cookie_header(secure, "http://example.com/") is None

    rejected = httpx.CookieStore()
    _set(rejected, "http://example.com/", "__Secure-a=1; Secure; Path=/")
    _set(rejected, "https://example.com/", "__Secure-b=1; Path=/")
    _set(
        rejected,
        "https://example.com/",
        "__Host-c=1; Secure; Path=/; Domain=example.com",
    )
    _set(rejected, "https://example.com/foo", "__Host-d=1; Secure")
    _set(rejected, "https://example.com/", "__Host-e=1; Secure; Path=/sub")
    assert len(rejected) == 0

    accepted = httpx.CookieStore()
    _set(accepted, "https://example.com/", "__Secure-a=1; Secure; Path=/")
    _set(accepted, "https://example.com/foo", "__Host-b=1; Secure; Path=/")
    assert _cookie_header(accepted, "https://example.com/foo") == (
        "__Secure-a=1; __Host-b=1"
    )
    assert _cookie_header(accepted, "http://example.com/foo") is None
    assert accepted.get("__Host-b", domain="example.com") == "1"


def test_expiry_rules() -> None:
    store = httpx.CookieStore()
    _set(store, "https://example.com/", "a=1; Path=/")
    _set(store, "https://example.com/", "a=2; Path=/; Max-Age=0")
    assert "a" not in store

    _set(store, "https://example.com/", "b=1; Path=/")
    _set(
        store,
        "https://example.com/",
        "b=2; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
    )
    assert "b" not in store

    _set(store, "https://example.com/", "c=1; Path=/; Expires=not-a-date")
    assert store["c"] == "1"

    _set(
        store,
        "https://example.com/",
        "d=1; Path=/; Max-Age=3600; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
    )
    assert store["d"] == "1"
    _set(store, "https://example.com/", "d=1; Path=/; Max-Age=-5")
    assert "d" not in store

    _set(store, "https://example.com/", "e=1; Path=/; Max-Age=abc")
    assert "e" not in store


def test_send_order_and_name_conflict() -> None:
    store = httpx.CookieStore()
    store.set("old", "1", path="/")
    store.set("nested", "1", path="/a/b")
    store.set("newer", "1", path="/")
    assert (
        _cookie_header(store, "https://example.com/a/b") == "nested=1; old=1; newer=1"
    )

    store.set("shared", "root", domain="example.com", path="/")
    store.set("shared", "deep", domain="example.com", path="/a")
    with pytest.raises(httpx.CookieConflict):
        store["shared"]
    assert store.get("shared", domain="example.com", path="/a") == "deep"
    store.delete("shared", domain="example.com", path="/")
    assert store["shared"] == "deep"
    store.clear(domain="example.com")
    assert "shared" not in store


def test_update_accepts_cookie_inputs() -> None:
    store = httpx.CookieStore()
    store.update({"a": "1"})
    store.update([("b", "2")])
    store.update(httpx.CookieStore({"c": "3"}))

    cookies = httpx.Cookies()
    cookies.set("d", "4", domain="example.com", path="/docs")
    store.update(cookies)

    jar = CookieJar()
    jar.set_cookie(
        Cookie(
            version=0,
            name="e",
            value="5",
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
            rest={},
            rfc2109=False,
        )
    )
    store.update(jar)

    assert _cookie_header(store, "http://anywhere.test/") == "a=1; b=2; c=3"
    assert _cookie_header(store, "https://example.com/docs") == "d=4; a=1; b=2; c=3"
    assert _cookie_header(store, "http://example.com/docs") == "d=4; a=1; b=2; c=3"
    assert _cookie_header(store, "https://www.example.org/") == "a=1; b=2; c=3; e=5"
    assert _cookie_header(store, "http://www.example.org/") == "a=1; b=2; c=3"
    assert store.get("d", domain="example.com", path="/docs") == "4"


def test_unspecified_domain_is_not_host_only() -> None:
    store = httpx.CookieStore()
    store.set("a", "1", domain="", path="/")
    assert _cookie_header(store, "http://one.test/x") == "a=1"
    assert _cookie_header(store, "https://two.test/x") == "a=1"


def test_request_cookies_argument() -> None:
    store = httpx.CookieStore([("a", "1"), ("b", "2")])
    request = httpx.Request("GET", "http://example.com/sub", cookies=store)
    assert request.headers["cookie"] == "a=1; b=2"
