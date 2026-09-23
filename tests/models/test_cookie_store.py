from http.cookiejar import Cookie, CookieJar

import pytest

import httpx


def _response(url: str, set_cookie: str | list[str]) -> httpx.Response:
    request = httpx.Request("GET", url)
    if isinstance(set_cookie, str):
        return httpx.Response(200, headers={"set-cookie": set_cookie}, request=request)
    return httpx.Response(
        200,
        headers=[(b"set-cookie", value.encode("ascii")) for value in set_cookie],
        request=request,
    )


def _cookie_header(store: httpx.CookieStore, url: str) -> str | None:
    request = httpx.Request("GET", url, cookies=store)
    value = request.headers.get("cookie")
    return None if value is None else str(value)


def test_mapping_interface() -> None:
    store = httpx.CookieStore({"name": "value"})
    assert store["name"] == "value"
    assert "name" in store
    assert len(store) == 1
    assert dict(store) == {"name": "value"}
    assert bool(store) is True

    store["other"] = "x"
    assert store.get("other") == "x"
    del store["name"]
    assert "name" not in store
    assert len(store) == 1

    store.clear()
    assert len(store) == 0
    assert bool(store) is False
    with pytest.raises(KeyError):
        store["missing"]
    with pytest.raises(KeyError):
        del store["missing"]


def test_list_constructor_and_empty_value() -> None:
    store = httpx.CookieStore([("a", "1"), ("b", "")])
    assert store["a"] == "1"
    assert store["b"] == ""
    assert _cookie_header(store, "http://anywhere.test/x") == "a=1; b="


def test_limit_validation() -> None:
    httpx.CookieStore(max_cookies=None, max_cookies_per_domain=None)
    httpx.CookieStore(max_cookies=0, max_cookies_per_domain=0)

    bad_limits: list[object] = ["1", 1.5, 1.0, [], {}]
    for bad in bad_limits:
        with pytest.raises(TypeError):
            httpx.CookieStore(max_cookies=bad)  # type: ignore[arg-type]
        with pytest.raises(TypeError):
            httpx.CookieStore(max_cookies_per_domain=bad)  # type: ignore[arg-type]

    for bad_int in (-1, -5):
        with pytest.raises(ValueError):
            httpx.CookieStore(max_cookies=bad_int)
        with pytest.raises(ValueError):
            httpx.CookieStore(max_cookies_per_domain=bad_int)


def test_zero_limits_store_nothing() -> None:
    store = httpx.CookieStore(max_cookies=0)
    store.set("a", "1")
    assert len(store) == 0

    store = httpx.CookieStore(max_cookies_per_domain=0)
    store.set("a", "1", domain="example.com")
    assert len(store) == 0


def test_evict_oldest_per_domain_then_global() -> None:
    store = httpx.CookieStore(max_cookies=2, max_cookies_per_domain=1)
    store.set("b", "1", domain="b.test")
    store.set("a", "1", domain="a.test")
    store.set("a2", "2", domain="a.test")

    # Per-domain removes the older a.test cookie first. The global limit is
    # then already satisfied, so the older b.test cookie is kept.
    assert store.get("b", domain="b.test") == "1"
    assert store.get("a", domain="a.test") is None
    assert store.get("a2", domain="a.test") == "2"
    assert len(store) == 2


def test_global_eviction_after_per_domain() -> None:
    store = httpx.CookieStore(max_cookies=2, max_cookies_per_domain=2)
    store.set("a1", "1", domain="a.test")
    store.set("a2", "2", domain="a.test")
    store.set("b1", "3", domain="b.test")
    store.set("a3", "4", domain="a.test")

    assert "a1" not in store
    assert "a2" not in store
    assert store["b1"] == "3"
    assert store["a3"] == "4"


def test_replacement_is_newly_created_for_eviction() -> None:
    store = httpx.CookieStore(max_cookies=2)
    store.set("a", "1")
    store.set("b", "2")
    store.set("a", "3")
    store.set("c", "4")

    assert store.get("a") == "3"
    assert store.get("b") is None
    assert store.get("c") == "4"


def test_send_order_longer_path_then_older_creation() -> None:
    store = httpx.CookieStore()
    store.set("long", "1", path="/sub/x")
    store.set("short", "2", path="/sub")
    assert _cookie_header(store, "https://example.test/sub/x") == "long=1; short=2"

    store = httpx.CookieStore()
    store.set("old", "1", path="/sub")
    store.set("new", "2", path="/sub")
    assert _cookie_header(store, "https://example.test/sub") == "old=1; new=2"

    store.set("old", "3", path="/sub")
    assert _cookie_header(store, "https://example.test/sub") == "new=2; old=3"


def test_domain_empty_is_not_host_only() -> None:
    store = httpx.CookieStore()
    store.set("a", "b", domain="", path="/")
    store.update({"c": "d"})
    store.update([("e", "f")])

    for url in ("http://one.test/x", "https://two.example/y"):
        assert _cookie_header(store, url) == "a=b; c=d; e=f"


def test_host_only_and_domain_matching() -> None:
    store = httpx.CookieStore()
    store.extract_cookies(_response("https://www.example.com/foo/bar", "id=1"))

    assert _cookie_header(store, "https://www.example.com/foo") == "id=1"
    assert _cookie_header(store, "https://www.example.com/foo/baz") == "id=1"
    assert _cookie_header(store, "https://www.example.com/foobar") is None
    assert _cookie_header(store, "https://example.com/foo") is None
    assert _cookie_header(store, "https://other.www.example.com/foo") is None

    store = httpx.CookieStore()
    store.extract_cookies(
        _response(
            "https://WWW.Example.COM/account/settings",
            "id=1; Domain=Example.COM; Path=/account",
        )
    )
    assert _cookie_header(store, "https://api.example.com/account") == "id=1"
    assert _cookie_header(store, "https://example.com/account/x") == "id=1"
    assert _cookie_header(store, "https://example.com/other") is None
    assert _cookie_header(store, "https://notexample.com/account") is None
    assert store.get("id", domain=".example.com") == "1"

    rejected = httpx.CookieStore()
    rejected.extract_cookies(
        _response("https://www.example.com/", "id=1; Domain=other.com")
    )
    assert len(rejected) == 0


def test_path_default_and_matching() -> None:
    store = httpx.CookieStore()
    store.extract_cookies(
        _response("https://example.com/foo/bar", "id=1; Path=relative")
    )
    assert _cookie_header(store, "https://example.com/foo/x") == "id=1"
    assert _cookie_header(store, "https://example.com/other") is None

    store = httpx.CookieStore()
    store.extract_cookies(_response("https://example.com/foo/bar", "id=1; Path="))
    assert _cookie_header(store, "https://example.com/foo/x") == "id=1"
    assert _cookie_header(store, "https://example.com/other") is None

    store = httpx.CookieStore()
    store.extract_cookies(_response("https://example.com/", "id=1; Path=/sub"))
    assert _cookie_header(store, "https://example.com/sub") == "id=1"
    assert _cookie_header(store, "https://example.com/sub/x") == "id=1"
    assert _cookie_header(store, "https://example.com/submarine") is None


def test_secure_flag() -> None:
    store = httpx.CookieStore()
    store.extract_cookies(_response("https://example.com/", "id=1; Secure"))
    assert _cookie_header(store, "https://example.com/") == "id=1"
    assert _cookie_header(store, "http://example.com/") is None


def test_cookie_prefixes() -> None:
    http_request = "http://example.com/"
    https_request = "https://example.com/"

    store = httpx.CookieStore()
    store.extract_cookies(_response(http_request, "__Secure-id=1; Secure"))
    assert len(store) == 0

    store.extract_cookies(_response(https_request, "__Secure-id=1"))
    assert len(store) == 0

    store.extract_cookies(_response(https_request, "__Secure-id=1; Secure"))
    assert store["__Secure-id"] == "1"
    assert _cookie_header(store, "http://example.com/") is None
    assert _cookie_header(store, "https://www.example.com/") is None

    store = httpx.CookieStore()
    store.extract_cookies(_response(https_request, "__Host-id=1; Secure; Path=/"))
    assert store["__Host-id"] == "1"
    assert _cookie_header(store, "https://example.com/any") == "__Host-id=1"
    assert _cookie_header(store, "https://www.example.com/") is None

    rejected = httpx.CookieStore()
    rejected.extract_cookies(
        _response(https_request, "__Host-id=1; Secure; Path=/; Domain=example.com")
    )
    assert len(rejected) == 0

    rejected = httpx.CookieStore()
    rejected.extract_cookies(_response(https_request, "__Host-id=1; Secure; Path=/sub"))
    assert len(rejected) == 0

    rejected = httpx.CookieStore()
    rejected.extract_cookies(_response(https_request, "__Host-id=1; Path=/"))
    assert len(rejected) == 0

    rejected = httpx.CookieStore()
    rejected.extract_cookies(_response(http_request, "__Host-id=1; Secure; Path=/"))
    assert len(rejected) == 0

    # An omitted Path is not ``Path=/``, even when the default path would be ``/``.
    rejected = httpx.CookieStore()
    rejected.extract_cookies(_response(https_request, "__Host-id=1; Secure"))
    assert len(rejected) == 0

    nested = httpx.CookieStore()
    nested.extract_cookies(
        _response("https://example.com/foo/bar", "__Host-id=1; Secure")
    )
    assert len(nested) == 0


def test_expiry_rules() -> None:
    store = httpx.CookieStore()
    store.extract_cookies(
        _response(
            "https://example.com/",
            "id=1; Max-Age=1000; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
        )
    )
    assert store["id"] == "1"

    store.extract_cookies(
        _response(
            "https://example.com/",
            "id=2; Max-Age=0; Expires=Thu, 01 Jan 2099 00:00:00 GMT",
        )
    )
    assert "id" not in store

    store.extract_cookies(_response("https://example.com/", "id=1"))
    store.extract_cookies(_response("https://example.com/", "id=9; Max-Age=-1"))
    assert "id" not in store

    store.extract_cookies(_response("https://example.com/", "id=1; Expires=not-a-date"))
    assert store["id"] == "1"

    store.extract_cookies(
        _response("https://example.com/", "id=1; Expires=Thu, 01 Jan 1970 00:00:00 GMT")
    )
    assert "id" not in store

    store.extract_cookies(_response("https://example.com/", "id=1"))
    store.extract_cookies(_response("https://example.com/", "id=2; Max-Age=nope"))
    assert store["id"] == "1"

    store.extract_cookies(
        _response("https://example.com/", "id=1; Expires=Thu, 01 Jan 2099 00:00:00 GMT")
    )
    assert store["id"] == "1"
    assert _cookie_header(store, "https://example.com/") == "id=1"


def test_deletion_matches_name_domain_and_path() -> None:
    store = httpx.CookieStore()
    store.extract_cookies(
        _response(
            "https://example.com/sub/page",
            "id=keep; Path=/, id=drop; Path=/sub",
        )
    )
    assert store.get("id", path="/") == "keep"
    assert store.get("id", path="/sub") == "drop"

    store.extract_cookies(
        _response("https://example.com/sub/page", "id=x; Path=/sub; Max-Age=0")
    )
    assert store.get("id", path="/") == "keep"
    assert store.get("id", path="/sub") is None


def test_combined_set_cookie_and_malformed_values() -> None:
    store = httpx.CookieStore()
    store.extract_cookies(
        _response(
            "https://example.com/",
            [
                "a=1; Expires=Wed, 21 Oct 2099 07:28:00 GMT; Path=/, b=2; Path=/",
                "empty=",
                "",
                "   ",
                "novalue",
                "=missing-name",
                "bad=1; Domain",
                "bad=2; Domain=",
                "bad=3; Max-Age",
                "bad=4; Expires",
                "good=3; Unknown=1; HttpOnly; SameSite=Lax",
            ],
        )
    )
    assert store["a"] == "1"
    assert store["b"] == "2"
    assert store["empty"] == ""
    assert store["good"] == "3"
    assert "bad" not in store
    assert "novalue" not in store
    assert _cookie_header(store, "https://example.com/") == "a=1; b=2; empty=; good=3"


def test_cookie_conflict_and_clear() -> None:
    store = httpx.CookieStore()
    store.set("name", "1", domain="example.com", path="/one")
    store.set("name", "2", domain="example.com", path="/two")
    store.set("name", "3", domain="example.org", path="/")

    with pytest.raises(httpx.CookieConflict):
        store["name"]
    with pytest.raises(httpx.CookieConflict):
        store.get("name", domain="example.com")

    assert store.get("name", domain="example.com", path="/one") == "1"
    assert "name" in store

    store.delete("name", domain="example.org")
    assert store.get("name", domain="example.org") is None
    store.clear(domain="example.com", path="/one")
    assert len(store) == 1
    assert store.get("name", path="/two") == "2"
    store.clear(domain="example.com")
    assert len(store) == 0


def test_update_from_cookies_and_cookiejar() -> None:
    cookies = httpx.Cookies()
    cookies.set("plain", "1")
    cookies.set("scoped", "2", domain="example.com", path="/docs")
    store = httpx.CookieStore()
    store.update(cookies)

    assert _cookie_header(store, "http://anywhere.test/docs") == "plain=1"
    assert _cookie_header(store, "https://www.example.com/docs") == "scoped=2; plain=1"
    assert _cookie_header(store, "https://www.example.com/other") == "plain=1"

    jar = CookieJar()
    jar.set_cookie(
        Cookie(
            version=0,
            name="host",
            value="yes",
            port=None,
            port_specified=False,
            domain="example.com",
            domain_specified=False,
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
    secure_store = httpx.CookieStore()
    secure_store.update(jar)
    assert _cookie_header(secure_store, "https://example.com/") == "host=yes"
    assert _cookie_header(secure_store, "http://example.com/") is None
    assert _cookie_header(secure_store, "https://www.example.com/") is None

    other = httpx.CookieStore()
    other.set("n", "v", domain="example.org", path="/only")
    copied = httpx.CookieStore()
    copied.update(other)
    assert _cookie_header(copied, "https://example.org/only") == "n=v"
    assert _cookie_header(copied, "https://other.test/only") is None


def test_repr() -> None:
    store = httpx.CookieStore()
    store.set("a", "b", domain="example.com")
    assert repr(store) == "<CookieStore[<Cookie a=b for example.com />]>"
