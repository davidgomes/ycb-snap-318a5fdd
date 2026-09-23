from __future__ import annotations

import time
import typing
from http.cookiejar import Cookie, CookieJar

import pytest

import httpx


def extract(
    store: httpx.CookieStore, url: str, *set_cookie_headers: str
) -> httpx.CookieStore:
    request = httpx.Request("GET", url)
    headers = [("set-cookie", header) for header in set_cookie_headers]
    response = httpx.Response(200, headers=headers, request=request)
    store.extract_cookies(response)
    return store


def cookie_header(store: httpx.CookieStore, url: str) -> str | None:
    request = httpx.Request("GET", url)
    store.set_cookie_header(request)
    header: str | None = request.headers.get("Cookie")
    return header


def make_jar_cookie(name: str, value: str | None, **kwargs: typing.Any) -> Cookie:
    options: dict[str, typing.Any] = {
        "version": 0,
        "name": name,
        "value": value,
        "port": None,
        "port_specified": False,
        "domain": "",
        "domain_specified": False,
        "domain_initial_dot": False,
        "path": "/",
        "path_specified": True,
        "secure": False,
        "expires": None,
        "discard": True,
        "comment": None,
        "comment_url": None,
        "rest": {},
        "rfc2109": False,
    }
    options.update(kwargs)
    return Cookie(**options)


def test_exported() -> None:
    assert httpx.CookieStore.__module__ == "httpx"


@pytest.mark.parametrize("value", ["1", 1.5, True, [1]])
@pytest.mark.parametrize("option", ["max_cookies", "max_cookies_per_domain"])
def test_limits_must_be_ints(option: str, value: typing.Any) -> None:
    with pytest.raises(TypeError):
        httpx.CookieStore(**{option: value})


@pytest.mark.parametrize("value", [-1, -100])
@pytest.mark.parametrize("option", ["max_cookies", "max_cookies_per_domain"])
def test_limits_must_not_be_negative(option: str, value: typing.Any) -> None:
    with pytest.raises(ValueError):
        httpx.CookieStore(**{option: value})


def test_limits_accept_none_and_zero() -> None:
    store = httpx.CookieStore(max_cookies=None, max_cookies_per_domain=None)
    store["a"] = "1"
    assert dict(store) == {"a": "1"}

    store = httpx.CookieStore(max_cookies=0)
    store["a"] = "1"
    assert len(store) == 0


def test_host_only_cookie() -> None:
    store = extract(httpx.CookieStore(), "http://example.com/", "a=1")

    assert cookie_header(store, "http://example.com/") == "a=1"
    assert cookie_header(store, "http://EXAMPLE.com/other") == "a=1"
    assert cookie_header(store, "http://sub.example.com/") is None
    assert cookie_header(store, "http://other.com/") is None


def test_domain_cookie() -> None:
    store = extract(
        httpx.CookieStore(), "http://www.example.com/", "a=1; Domain=Example.COM"
    )

    assert store.get("a", domain="example.com") == "1"
    assert cookie_header(store, "http://example.com/") == "a=1"
    assert cookie_header(store, "http://www.example.com/") == "a=1"
    assert cookie_header(store, "http://deep.sub.example.com/") == "a=1"
    assert cookie_header(store, "http://notexample.com/") is None


def test_domain_cookie_with_leading_dot() -> None:
    store = extract(
        httpx.CookieStore(), "http://example.com/", "a=1; Domain=.example.com"
    )
    assert cookie_header(store, "http://sub.example.com/") == "a=1"


@pytest.mark.parametrize(
    "domain", ["other.com", "sub.example.com", "ample.com", ".", "com.example.com"]
)
def test_domain_cookie_must_domain_match_request_host(domain: str) -> None:
    store = extract(httpx.CookieStore(), "http://example.com/", f"a=1; Domain={domain}")
    assert len(store) == 0


def test_domain_cookie_on_ip_address() -> None:
    store = extract(httpx.CookieStore(), "http://10.0.0.1/", "a=1; Domain=10.0.0.1")
    assert cookie_header(store, "http://10.0.0.1/") == "a=1"

    store = extract(httpx.CookieStore(), "http://10.0.0.1/", "a=1; Domain=0.0.1")
    assert len(store) == 0


def test_multiple_set_cookie_headers() -> None:
    store = extract(httpx.CookieStore(), "http://example.com/", "a=1", "b=2")
    assert cookie_header(store, "http://example.com/") == "a=1; b=2"


def test_combined_set_cookie_header() -> None:
    store = extract(
        httpx.CookieStore(),
        "http://example.com/",
        "a=1; Expires=Wed, 09 Jun 2100 10:18:14 GMT; Path=/, b=2, c=; HttpOnly, d=4",
    )
    assert dict(store.items()) == {"a": "1", "b": "2", "c": "", "d": "4"}


@pytest.mark.parametrize(
    "header",
    [
        "",
        "   ",
        ";",
        "novalue",
        "=value",
        " =value",
        "a=1; Domain",
        "a=1; Domain=",
        "a=1; Max-Age",
        "a=1; max-age=",
        "a=1; Expires",
        "a=1; EXPIRES= ",
    ],
)
def test_ignored_cookies(header: str) -> None:
    store = extract(httpx.CookieStore(), "http://example.com/", header)
    assert len(store) == 0


def test_empty_values_and_unknown_attributes() -> None:
    store = extract(
        httpx.CookieStore(),
        "http://example.com/",
        "a=; Unknown; Other=thing; SameSite=Lax; HttpOnly;;",
        ' b = "quoted value" ',
    )
    assert store["a"] == ""
    assert store["b"] == '"quoted value"'
    assert cookie_header(store, "http://example.com/") == 'a=; b="quoted value"'


@pytest.mark.parametrize(
    "url,path_attribute,expected_path",
    [
        ("http://example.com", "", "/"),
        ("http://example.com/", "", "/"),
        ("http://example.com/a", "", "/"),
        ("http://example.com/a/", "", "/a"),
        ("http://example.com/a/b/c?x=/y/z", "", "/a/b"),
        ("http://example.com/a/b/c", "; Path=", "/a/b"),
        ("http://example.com/a/b/c", "; Path=relative", "/a/b"),
        ("http://example.com/a/b/c", "; Path=/x/y", "/x/y"),
    ],
)
def test_default_path(url: str, path_attribute: str, expected_path: str) -> None:
    store = extract(httpx.CookieStore(), url, f"a=1{path_attribute}")
    assert store.get("a", path=expected_path) == "1"


def test_path_matching() -> None:
    store = extract(httpx.CookieStore(), "http://example.com/", "a=1; Path=/sub")
    assert cookie_header(store, "http://example.com/sub") == "a=1"
    assert cookie_header(store, "http://example.com/sub/") == "a=1"
    assert cookie_header(store, "http://example.com/sub/x") == "a=1"
    assert cookie_header(store, "http://example.com/submarine") is None
    assert cookie_header(store, "http://example.com/") is None

    store = extract(httpx.CookieStore(), "http://example.com/", "a=1; Path=/sub/")
    assert cookie_header(store, "http://example.com/sub/x") == "a=1"
    assert cookie_header(store, "http://example.com/sub") is None


def test_secure_cookies_only_sent_over_https() -> None:
    store = extract(httpx.CookieStore(), "https://example.com/", "a=1; Secure", "b=2")
    assert cookie_header(store, "https://example.com/") == "a=1; b=2"
    assert cookie_header(store, "http://example.com/") == "b=2"


@pytest.mark.parametrize(
    "url,header,stored",
    [
        ("https://example.com/", "__Secure-a=1; Secure", True),
        ("https://example.com/", "__Secure-a=1; Secure; Domain=example.com", True),
        ("https://example.com/", "__Secure-a=1", False),
        ("http://example.com/", "__Secure-a=1; Secure", False),
        ("https://example.com/", "__Host-a=1; Secure; Path=/", True),
        ("https://example.com/", "__Host-a=1; Path=/", False),
        ("http://example.com/", "__Host-a=1; Secure; Path=/", False),
        ("https://example.com/", "__Host-a=1; Secure", False),
        ("https://example.com/", "__Host-a=1; Secure; Path=/sub", False),
        (
            "https://example.com/",
            "__Host-a=1; Secure; Path=/; Domain=example.com",
            False,
        ),
    ],
)
def test_cookie_prefixes(url: str, header: str, stored: bool) -> None:
    store = extract(httpx.CookieStore(), url, header)
    assert len(store) == (1 if stored else 0)


def test_max_age_takes_precedence_over_expires() -> None:
    store = extract(
        httpx.CookieStore(),
        "http://example.com/",
        "a=1; Max-Age=100; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
    )
    assert store["a"] == "1"

    extract(
        store,
        "http://example.com/",
        "a=2; Expires=Wed, 09 Jun 2100 10:18:14 GMT; Max-Age=0",
    )
    assert len(store) == 0


@pytest.mark.parametrize("max_age", ["0", "-1", "-100"])
def test_max_age_non_positive_deletes(max_age: str) -> None:
    store = extract(httpx.CookieStore(), "http://example.com/", "a=1", "b=1")
    extract(store, "http://example.com/", f"a=2; Max-Age={max_age}")
    assert dict(store) == {"b": "1"}


def test_expiry_deletes_only_matching_cookie() -> None:
    store = extract(httpx.CookieStore(), "http://example.com/", "a=1", "a=2; Path=/sub")
    extract(
        store, "http://example.com/", "a=; Path=/sub; Max-Age=0", "a=; Domain=x.org"
    )
    assert store.get("a", path="/") == "1"
    assert len(store) == 1


def test_expires_in_past_deletes() -> None:
    store = extract(httpx.CookieStore(), "http://example.com/", "a=1")
    extract(store, "http://example.com/", "a=; Expires=Thu, 01 Jan 1970 00:00:00 GMT")
    assert len(store) == 0


@pytest.mark.parametrize(
    "attributes",
    [
        "Expires=not a date",
        "Expires=Wed, 09 Jun 99999999999 10:18:14 GMT",
        "Max-Age=abc",
        "Max-Age=1.5",
        "Max-Age=99999999999999999999999999999999999999999999999",
    ],
)
def test_invalid_expiry_still_stores(attributes: str) -> None:
    store = extract(httpx.CookieStore(), "http://example.com/", f"a=1; {attributes}")
    assert store["a"] == "1"


def test_invalid_max_age_falls_back_to_expires() -> None:
    store = extract(
        httpx.CookieStore(),
        "http://example.com/",
        "a=1; Max-Age=abc; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
    )
    assert len(store) == 0


def test_cookies_expire_over_time(monkeypatch: pytest.MonkeyPatch) -> None:
    now = time.time()
    monkeypatch.setattr(time, "time", lambda: now)
    store = extract(
        httpx.CookieStore(),
        "http://example.com/",
        "a=1; Max-Age=10",
        "b=2; Expires=Wed, 09 Jun 2100 10:18:14 GMT",
    )
    assert cookie_header(store, "http://example.com/") == "a=1; b=2"

    monkeypatch.setattr(time, "time", lambda: now + 10)
    assert cookie_header(store, "http://example.com/") == "b=2"
    assert list(store) == ["b"]


def test_send_order() -> None:
    store = extract(
        httpx.CookieStore(),
        "http://example.com/",
        "a=1",
        "b=2; Path=/x/y",
        "c=3; Path=/x",
        "d=4; Path=/x/y",
        "e=5",
    )
    assert cookie_header(store, "http://example.com/x/y/z") == (
        "b=2; d=4; c=3; a=1; e=5"
    )


def test_replacement_is_treated_as_newly_created() -> None:
    store = extract(httpx.CookieStore(), "http://example.com/", "a=1", "b=2")
    extract(store, "http://example.com/", "a=3")
    assert cookie_header(store, "http://example.com/") == "b=2; a=3"
    assert list(store) == ["b", "a"]


def test_host_only_and_domain_cookie_with_same_host_replace() -> None:
    store = extract(httpx.CookieStore(), "http://example.com/", "a=1")
    extract(store, "http://example.com/", "a=2; Domain=example.com")
    assert len(store) == 1
    assert cookie_header(store, "http://sub.example.com/") == "a=2"


def test_existing_cookie_header_is_preserved() -> None:
    store = httpx.CookieStore({"a": "1"})
    request = httpx.Request("GET", "http://example.com/", headers={"Cookie": "x=y"})
    store.set_cookie_header(request)
    assert request.headers["Cookie"] == "x=y"


def test_request_with_cookie_store() -> None:
    store = httpx.CookieStore({"a": "1"})
    request = httpx.Request("GET", "http://example.com/", cookies=store)
    assert request.headers["Cookie"] == "a=1"

    request = httpx.Request("GET", "http://example.com/", cookies=httpx.CookieStore())
    assert "Cookie" not in request.headers


def test_cookie_conflict() -> None:
    store = extract(httpx.CookieStore(), "http://a.example.com/", "name=a")
    extract(store, "http://b.example.com/", "name=b")
    extract(store, "http://b.example.com/", "name=c; Path=/sub")

    with pytest.raises(httpx.CookieConflict):
        store["name"]
    with pytest.raises(httpx.CookieConflict):
        store.get("name")
    with pytest.raises(httpx.CookieConflict):
        store.get("name", domain="b.example.com")

    assert store.get("name", domain="a.example.com") == "a"
    assert store.get("name", domain="b.example.com", path="/") == "b"
    assert store.get("name", path="/sub") == "c"


def test_mapping_interface() -> None:
    store = httpx.CookieStore()
    assert not store
    assert store.get("missing") is None
    assert store.get("missing", "default") == "default"
    with pytest.raises(KeyError):
        store["missing"]
    with pytest.raises(KeyError):
        del store["missing"]

    store["a"] = "1"
    store.set("b", "2", domain=".Example.com", path="/sub")
    assert store
    assert len(store) == 2
    assert list(store) == ["a", "b"]
    assert store["a"] == "1"
    assert store.get("b", domain="example.com", path="/sub") == "2"
    assert repr(store) == (
        "<CookieStore[<Cookie a=1 for  />, <Cookie b=2 for example.com />]>"
    )

    del store["a"]
    assert "a" not in store
    assert store.pop("b") == "2"
    assert not store


def test_delete() -> None:
    store = httpx.CookieStore()
    store.set("a", "1", domain="one.com")
    store.set("a", "2", domain="two.com")
    store.set("a", "3", domain="two.com", path="/sub")
    store.set("b", "4", domain="two.com")

    store.delete("a", domain="two.com", path="/sub")
    assert len(store) == 3
    store.delete("a", domain="two.com")
    assert store.get("a") == "1"
    store.delete("a")
    store.delete("missing")
    assert dict(store) == {"b": "4"}


def test_clear() -> None:
    store = httpx.CookieStore()
    store.set("a", "1", domain="one.com")
    store.set("b", "2", domain="two.com")
    store.set("c", "3", domain="two.com", path="/sub")
    store.set("d", "4", domain="three.com", path="/sub")

    store.clear(domain="TWO.com", path="/sub")
    assert list(store) == ["a", "b", "d"]
    store.clear(path="/sub")
    assert list(store) == ["a", "b"]
    store.clear(domain="two.com")
    assert list(store) == ["a"]
    store.clear()
    assert len(store) == 0


def test_update_from_mapping_and_list_sends_to_any_host() -> None:
    store = httpx.CookieStore()
    store.update({"a": "1"})
    store.update([("b", "2")])
    store.update(None)
    store.set("c", "3")

    for url in ("http://example.com/", "https://other.org/some/path"):
        assert cookie_header(store, url) == "a=1; b=2; c=3"


def test_set_path_rules_still_apply() -> None:
    store = httpx.CookieStore()
    store.set("a", "1", path="/sub")
    assert cookie_header(store, "http://example.com/sub/x") == "a=1"
    assert cookie_header(store, "http://example.com/other") is None


def test_update_from_cookie_store() -> None:
    source = extract(
        httpx.CookieStore(),
        "https://example.com/",
        "a=1; Secure",
        "b=2; Domain=example.com",
        "c=3; Path=/sub",
    )
    store = httpx.CookieStore(source)
    assert list(store) == ["a", "b", "c"]
    assert cookie_header(store, "https://example.com/sub") == "c=3; a=1; b=2"
    assert cookie_header(store, "http://sub.example.com/") == "b=2"

    store["d"] = "4"
    assert "d" not in source


def test_update_from_cookies_and_cookiejar() -> None:
    cookies = httpx.Cookies()
    cookies.set("a", "1")
    cookies.set("b", "2", domain="example.com")
    store = httpx.CookieStore(cookies)
    assert cookie_header(store, "http://other.org/") == "a=1"
    assert cookie_header(store, "http://sub.example.com/") == "a=1; b=2"

    jar = CookieJar()
    jar.set_cookie(make_jar_cookie("host", "x", domain="example.com"))
    jar.set_cookie(make_jar_cookie("empty", None))
    jar.set_cookie(make_jar_cookie("secure", "s", secure=True))
    jar.set_cookie(make_jar_cookie("future", "f", expires=int(time.time()) + 100))
    jar.set_cookie(make_jar_cookie("expired", "e", expires=1))
    store = httpx.CookieStore()
    store.update(jar)
    assert sorted(store) == ["empty", "future", "host", "secure"]
    assert store["empty"] == ""
    header = cookie_header(store, "http://sub.example.com/")
    assert header is not None
    assert sorted(header.split("; ")) == ["empty=", "future=f"]
    header = cookie_header(store, "http://example.com/")
    assert header is not None
    assert sorted(header.split("; ")) == ["empty=", "future=f", "host=x"]


def test_cookies_from_cookie_store() -> None:
    store = extract(
        httpx.CookieStore(),
        "https://example.com/",
        "host=1",
        "domain=2; Domain=example.com; Max-Age=100",
        "secure=3; Secure",
    )
    store.set("any", "4")
    cookies = httpx.Cookies(store)
    assert dict(cookies) == {"host": "1", "domain": "2", "secure": "3", "any": "4"}

    request = httpx.Request("GET", "http://other.org/")
    cookies.set_cookie_header(request)
    assert request.headers["Cookie"] == "any=4"

    request = httpx.Request("GET", "https://example.com/")
    cookies.set_cookie_header(request)
    assert sorted(request.headers["Cookie"].split("; ")) == [
        "any=4",
        "domain=2",
        "host=1",
        "secure=3",
    ]

    assert dict(httpx.CookieStore(cookies)) == dict(store)


def test_max_cookies_per_domain() -> None:
    store = httpx.CookieStore(max_cookies_per_domain=2)
    extract(store, "http://one.com/", "a=1", "b=2")
    extract(store, "http://two.com/", "c=3", "d=4")
    extract(store, "http://one.com/", "e=5")
    assert list(store) == ["b", "c", "d", "e"]

    extract(store, "http://one.com/", "b=6")
    extract(store, "http://one.com/", "f=7")
    assert list(store) == ["c", "d", "b", "f"]


def test_max_cookies() -> None:
    store = httpx.CookieStore(max_cookies=3)
    extract(store, "http://one.com/", "a=1", "b=2")
    extract(store, "http://two.com/", "c=3")
    extract(store, "http://one.com/", "a=4")
    extract(store, "http://three.com/", "d=5")
    assert list(store) == ["c", "a", "d"]

    store["e"] = "6"
    assert list(store) == ["a", "d", "e"]


def test_per_domain_limit_applies_before_global_limit() -> None:
    store = httpx.CookieStore(max_cookies=3, max_cookies_per_domain=2)
    extract(store, "http://two.com/", "x=0")
    extract(store, "http://one.com/", "a=1", "b=2")
    extract(store, "http://one.com/", "c=3")
    assert list(store) == ["x", "b", "c"]

    extract(store, "http://three.com/", "d=4")
    assert list(store) == ["b", "c", "d"]


def test_expired_cookies_do_not_count_towards_limits(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    now = time.time()
    monkeypatch.setattr(time, "time", lambda: now)
    store = httpx.CookieStore(max_cookies=2)
    extract(store, "http://example.com/", "a=1; Max-Age=1", "b=2")

    monkeypatch.setattr(time, "time", lambda: now + 5)
    extract(store, "http://example.com/", "c=3")
    assert list(store) == ["b", "c"]
