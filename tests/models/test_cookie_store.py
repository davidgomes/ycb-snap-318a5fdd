from __future__ import annotations

import datetime
import types
from http.cookiejar import Cookie, CookieJar

import pytest

import httpx
from httpx._cookies import _parse_cookie_date


def extract(store: httpx.CookieStore, url: str, *set_cookies: str) -> None:
    request = httpx.Request("GET", url)
    headers = [("Set-Cookie", set_cookie) for set_cookie in set_cookies]
    response = httpx.Response(200, headers=headers, request=request)
    store.extract_cookies(response)


def cookie_header(store: httpx.CookieStore, url: str) -> str | None:
    request = httpx.Request("GET", url)
    store.set_cookie_header(request)
    return request.headers["Cookie"] if "Cookie" in request.headers else None


def jar_cookie(
    name: str,
    value: str | None,
    domain: str = "",
    domain_specified: bool = False,
    secure: bool = False,
    expires: int | None = None,
) -> Cookie:
    return Cookie(
        version=0,
        name=name,
        value=value,
        port=None,
        port_specified=False,
        domain=domain,
        domain_specified=domain_specified,
        domain_initial_dot=domain.startswith("."),
        path="/",
        path_specified=True,
        secure=secure,
        expires=expires,
        discard=True,
        comment=None,
        comment_url=None,
        rest={},
    )


def test_cookie_store_mapping_interface():
    store = httpx.CookieStore()
    store["name"] = "value"
    store["empty"] = ""

    assert store["name"] == "value"
    assert store["empty"] == ""
    assert store.get("empty") == ""
    assert store.get("missing") is None
    assert store.get("missing", "default") == "default"
    assert "name" in store
    assert "missing" not in store
    assert len(store) == 2
    assert dict(store) == {"name": "value", "empty": ""}
    assert bool(store) is True

    with pytest.raises(KeyError):
        store["missing"]
    with pytest.raises(KeyError):
        del store["missing"]

    del store["name"]
    assert dict(store) == {"empty": ""}

    for name in store:
        del store[name]
    assert len(store) == 0
    assert bool(store) is False


def test_cookie_store_limits():
    store = httpx.CookieStore(max_cookies=10, max_cookies_per_domain=0)
    assert store.max_cookies == 10
    assert store.max_cookies_per_domain == 0
    assert httpx.CookieStore().max_cookies is None
    assert httpx.CookieStore().max_cookies_per_domain is None


@pytest.mark.parametrize("limit", ["10", 1.5, True])
def test_cookie_store_limits_must_be_ints(limit):
    with pytest.raises(TypeError):
        httpx.CookieStore(max_cookies=limit)
    with pytest.raises(TypeError):
        httpx.CookieStore(max_cookies_per_domain=limit)


def test_cookie_store_limits_must_not_be_negative():
    with pytest.raises(ValueError):
        httpx.CookieStore(max_cookies=-1)
    with pytest.raises(ValueError):
        httpx.CookieStore(max_cookies_per_domain=-1)


def test_extract_multiple_set_cookie_headers():
    store = httpx.CookieStore()
    extract(store, "https://example.com/", "a=1", "b=2")
    assert dict(store) == {"a": "1", "b": "2"}
    assert cookie_header(store, "https://example.com/") == "a=1; b=2"


@pytest.mark.parametrize(
    "set_cookie",
    [
        "a=1, b=2, c=3",
        "a=1; Expires=Wed, 21 Oct 2099 07:28:00 GMT, b=2, c=3",
        "a=1; expires=Tuesday, 08-Sep-2099 18:33:35 GMT; Path=/, b=2; Secure, c=3",
        "a=1, , b=2,c=3,",
    ],
)
def test_extract_combined_set_cookie_header(set_cookie):
    store = httpx.CookieStore()
    extract(store, "https://example.com/", set_cookie)
    assert dict(store) == {"a": "1", "b": "2", "c": "3"}


def test_combined_set_cookie_header_keeps_expires():
    store = httpx.CookieStore()
    extract(
        store,
        "https://example.com/",
        "a=1; Expires=Wed, 21 Oct 2015 07:28:00 GMT, b=2",
    )
    assert dict(store) == {"b": "2"}


@pytest.mark.parametrize(
    "set_cookie",
    [
        "",
        "   ",
        ";",
        "novalue",
        "novalue; Path=/",
        "=value",
        " =value",
        "a=1; Domain",
        "a=1; Domain=",
        "a=1; Max-Age",
        "a=1; Max-Age= ",
        "a=1; Expires",
        "a=1; expires=",
        "a=1\x00",
        "a=\x7f",
    ],
)
def test_extract_ignores_malformed_cookies(set_cookie):
    store = httpx.CookieStore()
    extract(store, "https://example.com/", set_cookie)
    assert len(store) == 0


def test_extract_ignores_unknown_attributes():
    store = httpx.CookieStore()
    extract(store, "https://example.com/", "a=1; SameSite=Lax; HttpOnly; Foo; Bar=baz;")
    assert dict(store) == {"a": "1"}


def test_empty_cookie_value():
    store = httpx.CookieStore()
    extract(store, "https://example.com/", "a=", "b= ; Path=/")
    assert dict(store) == {"a": "", "b": ""}
    assert cookie_header(store, "https://example.com/") == "a=; b="


def test_host_only_cookie():
    store = httpx.CookieStore()
    extract(store, "https://Example.com/", "a=1")

    assert store.get("a", domain="example.com") == "1"
    assert cookie_header(store, "https://example.com/") == "a=1"
    assert cookie_header(store, "https://EXAMPLE.COM/") == "a=1"
    assert cookie_header(store, "https://www.example.com/") is None
    assert cookie_header(store, "https://other.com/") is None


@pytest.mark.parametrize(
    "domain", ["example.com", ".example.com", "EXAMPLE.com", ".Example.COM"]
)
def test_domain_cookie(domain):
    store = httpx.CookieStore()
    extract(store, "https://www.example.com/", f"a=1; Domain={domain}")

    assert store.get("a", domain="example.com") == "1"
    assert cookie_header(store, "https://example.com/") == "a=1"
    assert cookie_header(store, "https://www.example.com/") == "a=1"
    assert cookie_header(store, "https://a.b.example.com/") == "a=1"
    assert cookie_header(store, "https://notexample.com/") is None
    assert cookie_header(store, "https://example.org/") is None


def test_domain_cookie_replaces_host_only_cookie():
    store = httpx.CookieStore()
    extract(store, "https://example.com/", "a=1", "a=2; Domain=example.com")
    assert len(store) == 1
    assert cookie_header(store, "https://www.example.com/") == "a=2"


@pytest.mark.parametrize(
    "url, set_cookie",
    [
        ("https://example.com/", "a=1; Domain=www.example.com"),
        ("https://example.com/", "a=1; Domain=example.org"),
        ("https://myexample.com/", "a=1; Domain=example.com"),
        ("https://127.0.0.1/", "a=1; Domain=0.0.1"),
    ],
)
def test_domain_must_match_request_host(url, set_cookie):
    store = httpx.CookieStore()
    extract(store, url, set_cookie)
    assert len(store) == 0


def test_domain_cookie_for_ip_address():
    store = httpx.CookieStore()
    extract(store, "https://127.0.0.1/", "a=1; Domain=127.0.0.1")
    assert cookie_header(store, "https://127.0.0.1/") == "a=1"


@pytest.mark.parametrize(
    "request_path, set_cookie, cookie_path",
    [
        ("/", "a=1", "/"),
        ("/page", "a=1", "/"),
        ("/sub/", "a=1", "/sub"),
        ("/sub/page", "a=1", "/sub"),
        ("/a/b/page?query=/x/y", "a=1", "/a/b"),
        ("/sub/page", "a=1; Path=", "/sub"),
        ("/sub/page", "a=1; Path=relative", "/sub"),
        ("/sub/page", "a=1; Path=/", "/"),
        ("/sub/page", "a=1; Path=/other", "/other"),
        ("/sub/page", "a=1; Path=/x; Path=/y", "/y"),
    ],
)
def test_cookie_path(request_path, set_cookie, cookie_path):
    store = httpx.CookieStore()
    extract(store, f"https://example.com{request_path}", set_cookie)
    assert store.get("a", path=cookie_path) == "1"


@pytest.mark.parametrize(
    "cookie_path, request_path, matches",
    [
        ("/sub", "/sub", True),
        ("/sub", "/sub/", True),
        ("/sub", "/sub/x", True),
        ("/sub", "/submarine", False),
        ("/sub", "/", False),
        ("/sub", "/other/sub", False),
        ("/sub/", "/sub/x", True),
        ("/sub/", "/sub", False),
        ("/", "/anything", True),
    ],
)
def test_path_matching(cookie_path, request_path, matches):
    store = httpx.CookieStore()
    extract(store, "https://example.com/", f"a=1; Path={cookie_path}")
    expected = "a=1" if matches else None
    assert cookie_header(store, f"https://example.com{request_path}") == expected


def test_secure_cookie_is_only_sent_over_https():
    store = httpx.CookieStore()
    extract(store, "https://example.com/", "secure=1; Secure", "plain=2")
    extract(store, "http://example.com/", "insecure-origin=3; Secure=ignored")

    assert cookie_header(store, "https://example.com/") == (
        "secure=1; plain=2; insecure-origin=3"
    )
    assert cookie_header(store, "http://example.com/") == "plain=2"


@pytest.mark.parametrize(
    "url, set_cookie, stored",
    [
        ("https://example.com/", "__Secure-a=1; Secure", True),
        ("https://example.com/", "__Secure-a=1; Secure; Domain=example.com", True),
        ("https://example.com/", "__Secure-a=1", False),
        ("http://example.com/", "__Secure-a=1; Secure", False),
        ("https://example.com/", "__secure-a=1", False),
        ("https://example.com/", "__Host-a=1; Secure; Path=/", True),
        ("https://example.com/sub/page", "__Host-a=1; Secure; Path=/", True),
        ("https://example.com/", "__Host-a=1; Path=/", False),
        ("http://example.com/", "__Host-a=1; Secure; Path=/", False),
        ("https://example.com/", "__Host-a=1; Secure", False),
        ("https://example.com/", "__Host-a=1; Secure; Path=/sub", False),
        (
            "https://example.com/",
            "__Host-a=1; Secure; Path=/; Domain=example.com",
            False,
        ),
        ("https://example.com/", "__HOST-a=1; Secure; Path=/; Domain=.", False),
    ],
)
def test_cookie_prefixes(url, set_cookie, stored):
    store = httpx.CookieStore()
    extract(store, url, set_cookie)
    assert len(store) == (1 if stored else 0)


def test_max_age_takes_precedence_over_expires():
    store = httpx.CookieStore()
    extract(
        store,
        "https://example.com/",
        "a=1; Expires=Wed, 21 Oct 2015 07:28:00 GMT; Max-Age=100",
        "b=2; Max-Age=0; Expires=Wed, 21 Oct 2099 07:28:00 GMT",
        "c=3; Max-Age=invalid; Expires=Wed, 21 Oct 2015 07:28:00 GMT",
        "d=4; Max-Age=+100",
    )
    assert dict(store) == {"a": "1", "d": "4"}


@pytest.mark.parametrize(
    "deletion",
    [
        "a=deleted; Max-Age=0",
        "a=deleted; Max-Age=-1",
        "a=deleted; Max-Age=-" + "9" * 400,
        "a=deleted; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
        "a=deleted; Expires=Wed, 21 Oct 2015 07:28:00 GMT",
        "a=deleted; Expires=Wed, 21 Oct 2015 07:28:00 GMT; Expires=nonsense",
    ],
)
def test_expired_cookie_deletes_existing_cookie(deletion):
    store = httpx.CookieStore()
    extract(store, "https://example.com/", "a=1", "b=2")
    extract(store, "https://example.com/", deletion)
    assert dict(store) == {"b": "2"}


def test_expired_cookie_only_deletes_matching_cookie():
    store = httpx.CookieStore()
    extract(store, "https://example.com/", "a=1; Path=/sub")
    extract(store, "https://example.com/", "a=deleted; Path=/; Max-Age=0")
    assert store.get("a", path="/sub") == "1"


@pytest.mark.parametrize(
    "set_cookie",
    [
        "a=1; Expires=nonsense",
        "a=1; Expires=Wed, 30 Feb 2099 07:28:00 GMT",
        "a=1; Max-Age=" + "9" * 400,
        "a=1; Max-Age=100",
    ],
)
def test_cookie_is_stored_with_invalid_or_future_expiry(set_cookie):
    store = httpx.CookieStore()
    extract(store, "https://example.com/", set_cookie)
    assert dict(store) == {"a": "1"}


def test_cookies_expire_over_time(monkeypatch):
    clock = types.SimpleNamespace(time=lambda: 1_000_000.0)
    monkeypatch.setattr("httpx._cookies.time", clock)

    store = httpx.CookieStore()
    extract(store, "https://example.com/", "a=1; Max-Age=10", "b=2")
    assert list(store) == ["a", "b"]

    clock.time = lambda: 1_000_010.0
    assert list(store) == ["b"]
    assert cookie_header(store, "https://example.com/") == "b=2"


def utc(year: int, month: int, day: int, hour: int, minute: int, second: int) -> float:
    date = datetime.datetime(
        year, month, day, hour, minute, second, tzinfo=datetime.timezone.utc
    )
    return date.timestamp()


@pytest.mark.parametrize(
    "value, expected",
    [
        ("Wed, 21 Oct 2015 07:28:00 GMT", utc(2015, 10, 21, 7, 28, 0)),
        ("Sunday, 06-Nov-94 08:49:37 GMT", utc(1994, 11, 6, 8, 49, 37)),
        ("Sun Nov  6 08:49:37 1994", utc(1994, 11, 6, 8, 49, 37)),
        ("Thu, 01 Jan 1970 00:00:00 GMT", 0.0),
        ("01 JANUARY 69 1:2:3", utc(2069, 1, 1, 1, 2, 3)),
        ("01 Jan 70 00:00:00", utc(1970, 1, 1, 0, 0, 0)),
        ("01 Jan 1601 00:00:00", utc(1601, 1, 1, 0, 0, 0)),
        ("01 Jan 1600 00:00:00", None),
        ("01 Jan 100 00:00:00", None),
        ("01 Jan 2015 24:00:00", None),
        ("01 Jan 2015 00:60:00", None),
        ("01 Jan 2015 00:00:60", None),
        ("00 Jan 2015 00:00:00", None),
        ("30 Feb 2015 00:00:00", None),
        ("01 Jan 2015", None),
        ("Jan 2015 00:00:00", None),
        ("01 2015 00:00:00", None),
        ("01 Jan 00:00:00", None),
        ("nonsense", None),
        ("", None),
    ],
)
def test_parse_cookie_date(value, expected):
    assert _parse_cookie_date(value) == expected


def test_cookie_header_ordering():
    store = httpx.CookieStore()
    extract(
        store,
        "https://example.com/",
        "short=1; Path=/",
        "long=2; Path=/a/b",
        "medium=3; Path=/a",
        "short2=4; Path=/",
    )
    assert cookie_header(store, "https://example.com/a/b/c") == (
        "long=2; medium=3; short=1; short2=4"
    )

    extract(store, "https://example.com/", "short=5; Path=/")
    assert cookie_header(store, "https://example.com/a/b/c") == (
        "long=2; medium=3; short2=4; short=5"
    )


def test_set_cookie_header_does_not_override_explicit_header():
    store = httpx.CookieStore()
    store.set("a", "1")
    request = httpx.Request("GET", "https://example.com/", headers={"Cookie": "b=2"})
    store.set_cookie_header(request)
    assert request.headers["Cookie"] == "b=2"


def test_request_with_cookie_store():
    store = httpx.CookieStore()
    request = httpx.Request("GET", "https://example.com/", cookies=store)
    assert "Cookie" not in request.headers

    store.set("a", "1")
    request = httpx.Request("GET", "https://example.com/", cookies=store)
    assert request.headers["Cookie"] == "a=1"


def test_cookie_conflict():
    store = httpx.CookieStore()
    extract(store, "https://example.com/", "name=root")
    extract(store, "https://example.com/sub/page", "name=sub")

    assert len(store) == 2
    assert "name" in store
    with pytest.raises(httpx.CookieConflict):
        store["name"]
    with pytest.raises(httpx.CookieConflict):
        store.get("name")
    with pytest.raises(httpx.CookieConflict):
        store.get("name", domain="example.com")
    assert store.get("name", path="/sub") == "sub"
    assert store.get("name", domain="example.com", path="/") == "root"


def test_set_get_delete_with_domain_and_path():
    store = httpx.CookieStore()
    store.set("name", "a", domain="example.com")
    store.set("name", "b", domain=".Example.ORG")
    store.set("name", "c", domain="example.com", path="/sub")

    with pytest.raises(httpx.CookieConflict):
        store["name"]
    assert store.get("name", domain="example.org") == "b"
    assert store.get("name", domain=".EXAMPLE.org") == "b"
    assert store.get("name", domain="example.com", path="/sub") == "c"
    assert store.get("name", domain="example.net") is None

    store.delete("name", domain="example.com", path="/sub")
    assert len(store) == 2
    store.delete("name", domain="example.com")
    assert store["name"] == "b"
    store.delete("name")
    assert len(store) == 0


def test_set_replaces_matching_cookie():
    store = httpx.CookieStore()
    store.set("a", "1")
    store.set("b", "2")
    store.set("a", "3")
    assert list(store.items()) == [("b", "2"), ("a", "3")]


def test_clear():
    store = httpx.CookieStore()
    store.set("a", "1", domain="example.com")
    store.set("b", "2", domain="example.com", path="/sub")
    store.set("c", "3", domain="example.org", path="/sub")
    store.set("d", "4", domain="example.org")

    store.clear(domain="example.com", path="/sub")
    assert list(store) == ["a", "c", "d"]
    store.clear(path="/sub")
    assert list(store) == ["a", "d"]
    store.clear(domain=".EXAMPLE.com")
    assert list(store) == ["d"]
    store.clear()
    assert len(store) == 0


def test_max_cookies_per_domain_evicts_oldest():
    store = httpx.CookieStore(max_cookies_per_domain=2)
    extract(store, "https://a.com/", "a1=1", "a2=2")
    extract(store, "https://b.com/", "b1=1")
    extract(store, "https://a.com/", "a3=3")
    assert list(store) == ["a2", "b1", "a3"]


def test_max_cookies_evicts_oldest():
    store = httpx.CookieStore(max_cookies=3)
    extract(store, "https://a.com/", "a1=1", "a2=2")
    extract(store, "https://b.com/", "b1=1", "b2=2")
    assert list(store) == ["a2", "b1", "b2"]


def test_per_domain_limit_is_applied_before_global_limit():
    store = httpx.CookieStore(max_cookies=3, max_cookies_per_domain=2)
    extract(store, "https://a.com/", "a1=1", "a2=2")
    extract(store, "https://b.com/", "b1=1")
    extract(store, "https://a.com/", "a3=3")
    assert list(store) == ["a2", "b1", "a3"]

    extract(store, "https://b.com/", "b2=2")
    assert list(store) == ["b1", "a3", "b2"]


def test_replaced_cookie_is_treated_as_newly_created():
    store = httpx.CookieStore(max_cookies=2)
    extract(store, "https://example.com/", "a=1", "b=2", "a=3", "c=4")
    assert dict(store) == {"a": "3", "c": "4"}


def test_expired_cookies_do_not_count_towards_limits(monkeypatch):
    clock = types.SimpleNamespace(time=lambda: 1_000_000.0)
    monkeypatch.setattr("httpx._cookies.time", clock)

    store = httpx.CookieStore(max_cookies=2)
    extract(store, "https://example.com/", "a=1", "b=2; Max-Age=10")
    clock.time = lambda: 1_000_010.0
    extract(store, "https://example.com/", "c=3")
    assert dict(store) == {"a": "1", "c": "3"}


def test_zero_limits_store_nothing():
    store = httpx.CookieStore(max_cookies=0)
    store.set("a", "1")
    assert len(store) == 0

    store = httpx.CookieStore(max_cookies_per_domain=0)
    extract(store, "https://example.com/", "a=1")
    assert len(store) == 0


def test_limits_apply_to_update():
    store = httpx.CookieStore(max_cookies=2)
    store.update({"a": "1", "b": "2", "c": "3"})
    assert dict(store) == {"b": "2", "c": "3"}


@pytest.mark.parametrize(
    "cookies",
    [
        {"a": "1", "b": "2"},
        [("a", "1"), ("b", "2")],
        httpx.Cookies({"a": "1", "b": "2"}),
    ],
)
def test_update_cookies_without_domain_match_any_host(cookies):
    store = httpx.CookieStore()
    store.update(cookies)
    assert dict(store) == {"a": "1", "b": "2"}
    assert cookie_header(store, "http://example.com/") == "a=1; b=2"
    assert cookie_header(store, "https://sub.example.org/path") == "a=1; b=2"


def test_set_without_domain_matches_any_host():
    store = httpx.CookieStore()
    store.set("a", "1")
    store.set("b", "2", path="/sub")
    assert cookie_header(store, "http://example.com/") == "a=1"
    assert cookie_header(store, "https://other.org/sub/x") == "b=2; a=1"


def test_update_from_cookie_store():
    source = httpx.CookieStore()
    extract(source, "https://example.com/", "host=1", "secure=2; Secure")
    source.set("any", "3")

    store = httpx.CookieStore()
    store.set("existing", "0")
    store.update(source)

    assert dict(store) == {"existing": "0", "host": "1", "secure": "2", "any": "3"}
    assert cookie_header(store, "https://example.com/") == (
        "existing=0; host=1; secure=2; any=3"
    )
    assert cookie_header(store, "http://example.com/") == "existing=0; host=1; any=3"
    assert cookie_header(store, "https://www.example.com/") == "existing=0; any=3"


def test_update_from_cookies():
    cookies = httpx.Cookies()
    cookies.set("domain", "1", domain="example.com")
    cookies.set("path", "2", domain="example.com", path="/sub")

    store = httpx.CookieStore()
    store.update(cookies)

    assert store.get("path", domain="example.com", path="/sub") == "2"
    assert cookie_header(store, "https://www.example.com/sub") == "path=2; domain=1"
    assert cookie_header(store, "https://example.org/") is None


def test_update_from_cookiejar():
    jar = CookieJar()
    jar.set_cookie(jar_cookie("any", "1"))
    jar.set_cookie(jar_cookie("host", "2", domain="example.com"))
    jar.set_cookie(
        jar_cookie(
            "domain", "3", domain=".example.com", domain_specified=True, secure=True
        )
    )
    jar.set_cookie(jar_cookie("novalue", None, domain="example.com"))
    jar.set_cookie(jar_cookie("expired", "4", expires=1))

    store = httpx.CookieStore()
    store.update(jar)

    assert dict(store) == {"any": "1", "host": "2", "domain": "3", "novalue": ""}
    assert cookie_header(store, "http://www.example.com/") == "any=1"
    assert cookie_header(store, "https://www.example.com/") == "any=1; domain=3"


def test_update_with_none():
    store = httpx.CookieStore()
    store.set("a", "1")
    store.update(None)
    assert dict(store) == {"a": "1"}


def test_cookies_from_cookie_store():
    store = httpx.CookieStore()
    extract(
        store,
        "https://example.com/",
        "host=1",
        "domain=2; Domain=example.com",
        "secure=3; Secure; Max-Age=100",
    )
    store.set("any", "4")

    cookies = httpx.Cookies(store)
    assert dict(cookies) == {"host": "1", "domain": "2", "secure": "3", "any": "4"}
    assert cookies.get("domain", domain="example.com") == "2"

    jar = {cookie.name: cookie for cookie in cookies.jar}
    assert not jar["host"].domain_specified
    assert jar["domain"].domain_specified
    assert jar["secure"].secure
    assert jar["secure"].expires is not None
    assert jar["any"].expires is None

    more_cookies = httpx.Cookies()
    more_cookies.update(store)
    assert dict(more_cookies) == dict(cookies)


def test_cookie_store_repr():
    store = httpx.CookieStore()
    extract(store, "https://example.com/", "a=1")
    store.set("b", "2")
    store.set("c", "3", domain="example.org")

    assert repr(store) == (
        "<CookieStore[<Cookie a=1 for example.com />,"
        " <Cookie b=2 for  />,"
        " <Cookie c=3 for example.org />]>"
    )
