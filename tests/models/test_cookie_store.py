import http.cookiejar

import pytest

import httpx


def _response(url: str, headers: list[tuple[bytes, bytes]]) -> httpx.Response:
    request = httpx.Request("GET", url)
    return httpx.Response(200, headers=headers, request=request)


def test_limits_validation() -> None:
    with pytest.raises(TypeError):
        httpx.CookieStore(max_cookies="3")  # type: ignore[arg-type]
    with pytest.raises(TypeError):
        httpx.CookieStore(max_cookies_per_domain=1.5)  # type: ignore[arg-type]
    with pytest.raises(TypeError):
        httpx.CookieStore(max_cookies=True)  # type: ignore[arg-type]
    with pytest.raises(ValueError):
        httpx.CookieStore(max_cookies=-1)
    with pytest.raises(ValueError):
        httpx.CookieStore(max_cookies_per_domain=-1)
    store = httpx.CookieStore(max_cookies=None, max_cookies_per_domain=None)
    assert store.max_cookies is None


def test_evicts_oldest_per_domain_then_global() -> None:
    store = httpx.CookieStore(max_cookies=2, max_cookies_per_domain=2)
    store.set("a", "1", domain="example.org")
    store.set("b", "2", domain="example.org")
    store.set("c", "3", domain="example.org")
    assert store.get("a", domain="example.org") is None
    assert store.get("b", domain="example.org") == "2"
    assert store.get("c", domain="example.org") == "3"

    store.set("d", "4", domain="example.com")
    assert len(store) == 2
    assert store.get("b", domain="example.org") is None
    assert store.get("c", domain="example.org") == "3"
    assert store.get("d", domain="example.com") == "4"


def test_replacement_is_newly_created() -> None:
    store = httpx.CookieStore(max_cookies=2)
    store.set("a", "1", domain="example.org", path="/")
    store.set("b", "2", domain="example.org", path="/")
    store.set("a", "3", domain="example.org", path="/")
    store.set("c", "4", domain="example.org", path="/other")
    assert store.get("b") is None
    assert store.get("a") == "3"
    assert store.get("c") == "4"


def test_extract_combined_set_cookie_and_expires_comma() -> None:
    header = (
        b"a=1; Expires=Tue, 08-Sep-2099 18:33:35 GMT; Path=/; Domain=example.org, "
        b"b=2; Path=/sub"
    )
    response = _response(
        "https://www.example.org/sub/index.html",
        [(b"Set-Cookie", header), (b"Set-Cookie", b"c=")],
    )
    store = httpx.CookieStore()
    store.extract_cookies(response)
    assert store.get("a", domain="example.org") == "1"
    assert store.get("b", domain="www.example.org", path="/sub") == "2"
    assert store.get("c", domain="www.example.org") == ""
    assert len(store) == 3


def test_ignores_malformed_and_valueless_attributes() -> None:
    response = _response(
        "https://example.org/docs/page",
        [
            (b"Set-Cookie", b""),
            (b"Set-Cookie", b"not-a-cookie"),
            (b"Set-Cookie", b"bad=1; Domain"),
            (b"Set-Cookie", b"bad2=1; Max-Age"),
            (b"Set-Cookie", b"bad3=1; Expires="),
            (b"Set-Cookie", b"ok=1; Unknown=x; Path=relative"),
        ],
    )
    store = httpx.CookieStore()
    store.extract_cookies(response)
    assert list(store) == ["ok"]
    assert store.get("ok", path="/docs") == "1"


def test_host_only_domain_and_path_matching() -> None:
    response = _response(
        "https://www.example.org/sub/index",
        [
            (b"Set-Cookie", b"host=1; Path=/sub"),
            (b"Set-Cookie", b"shared=1; Domain=example.org; Path=/sub"),
        ],
    )
    store = httpx.CookieStore()
    store.extract_cookies(response)

    host_request = httpx.Request("GET", "https://www.example.org/sub")
    store.set_cookie_header(host_request)
    assert host_request.headers["cookie"] == "host=1; shared=1"

    child = httpx.Request("GET", "https://www.example.org/sub/x")
    store.set_cookie_header(child)
    assert "host=1" in child.headers["cookie"]

    submarine = httpx.Request("GET", "https://www.example.org/submarine")
    store.set_cookie_header(submarine)
    assert "cookie" not in submarine.headers

    parent = httpx.Request("GET", "https://example.org/sub")
    store.set_cookie_header(parent)
    assert parent.headers["cookie"] == "shared=1"

    other = httpx.Request("GET", "https://www.example.org/sub")
    rejected = _response(
        "https://www.example.org/",
        [(b"Set-Cookie", b"nope=1; Domain=other.org")],
    )
    store.extract_cookies(rejected)
    store.set_cookie_header(other)
    assert "nope" not in other.headers.get("cookie", "")


def test_secure_and_prefixes() -> None:
    response = _response(
        "https://example.org/",
        [
            (b"Set-Cookie", b"plain=1; Secure"),
            (b"Set-Cookie", b"__Secure-id=1; Secure"),
            (b"Set-Cookie", b"__Host-id=1; Secure; Path=/"),
            (b"Set-Cookie", b"__Secure-no=1"),
            (b"Set-Cookie", b"__Host-dom=1; Secure; Path=/; Domain=example.org"),
            (b"Set-Cookie", b"__Host-path=1; Secure; Path=/sub"),
        ],
    )
    store = httpx.CookieStore()
    store.extract_cookies(response)
    assert store.get("__Secure-id") == "1"
    assert store.get("__Host-id") == "1"
    assert store.get("__Secure-no") is None
    assert store.get("__Host-dom") is None
    assert store.get("__Host-path") is None

    http_request = httpx.Request("GET", "http://example.org/")
    store.set_cookie_header(http_request)
    assert "cookie" not in http_request.headers

    insecure_origin = _response(
        "http://example.org/",
        [(b"Set-Cookie", b"__Secure-id=2; Secure")],
    )
    store.extract_cookies(insecure_origin)
    assert store.get("__Secure-id") == "1"


def test_expiry_rules() -> None:
    response = _response(
        "https://example.org/",
        [
            (
                b"Set-Cookie",
                b"keep=1; Max-Age=3600; Expires=Tue, 08-Sep-1999 18:33:35 GMT",
            ),
            (b"Set-Cookie", b"session=1; Expires=not-a-date"),
            (b"Set-Cookie", b"gone=1; Expires=Tue, 08-Sep-1999 18:33:35 GMT"),
        ],
    )
    store = httpx.CookieStore()
    store.set("gone", "old", domain="example.org")
    store.set("drop", "1", domain="example.org")
    store.extract_cookies(response)
    assert store.get("keep") == "1"
    assert store.get("session") == "1"
    assert store.get("gone") is None

    deletion = _response(
        "https://example.org/",
        [(b"Set-Cookie", b"drop=0; Max-Age=0"), (b"Set-Cookie", b"keep=0; Max-Age=-5")],
    )
    store.extract_cookies(deletion)
    assert store.get("drop") is None
    assert store.get("keep") is None
    assert store.get("session") == "1"


def test_send_order_and_conflict() -> None:
    store = httpx.CookieStore()
    store.set("a", "root", path="/")
    store.set("a", "deep", path="/sub/dir")
    store.set("a", "mid", path="/sub")
    request = httpx.Request("GET", "https://example.org/sub/dir/page")
    store.set_cookie_header(request)
    assert request.headers["cookie"] == "a=deep; a=mid; a=root"
    with pytest.raises(httpx.CookieConflict):
        store["a"]
    assert store.get("a", path="/sub") == "mid"


def test_update_accepts_cookie_inputs() -> None:
    store = httpx.CookieStore()
    store.update({"n": "1"})
    store.update([("m", "2")])

    cookies = httpx.Cookies()
    cookies.set("jar", "3", domain="example.org", path="/p")
    store.update(cookies)

    cookiejar = http.cookiejar.CookieJar()
    cookiejar.set_cookie(
        http.cookiejar.Cookie(
            version=0,
            name="stdlib",
            value="4",
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
    store.update(cookiejar)

    other = httpx.CookieStore()
    other.set("copied", "5", domain="example.org")
    store.update(other)

    request = httpx.Request("GET", "http://anywhere.example/p")
    store.set_cookie_header(request)
    header = request.headers["cookie"]
    assert "n=1" in header
    assert "m=2" in header
    assert "jar" not in header
    assert "stdlib" not in header

    secure_request = httpx.Request("GET", "https://www.example.org/p")
    store.set_cookie_header(secure_request)
    secure_header = secure_request.headers["cookie"]
    assert "stdlib=4" in secure_header
    assert "copied=5" in secure_header
    assert "jar=3" in secure_header


def test_mapping_and_clear() -> None:
    store = httpx.CookieStore({"a": "1"})
    store["b"] = "2"
    assert dict(store.items()) == {"a": "1", "b": "2"}
    del store["a"]
    store.clear()
    assert list(store) == []
    assert bool(store) is False


def test_client_uses_cookie_store() -> None:
    store = httpx.CookieStore()

    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/set":
            return httpx.Response(
                200,
                headers=[(b"set-cookie", b"host=1; Path=/")],
            )
        return httpx.Response(200, json={"cookie": request.headers.get("cookie")})

    client = httpx.Client(cookies=store, transport=httpx.MockTransport(handler))
    client.get("https://example.org/set")
    assert store.get("host", domain="example.org") == "1"
    sent = client.get("https://example.org/echo")
    assert sent.json()["cookie"] == "host=1"
    other = client.get("https://other.test/echo")
    assert other.json()["cookie"] is None

    client.cookies = {"loose": "1"}
    assert isinstance(client.cookies, httpx.Cookies)
