import http.cookiejar

import pytest

import httpx


def extract(store, url, *set_cookies):
    request = httpx.Request("GET", url)
    headers = [("set-cookie", value) for value in set_cookies]
    store.extract_cookies(httpx.Response(200, headers=headers, request=request))


def cookie_header(store, url):
    request = httpx.Request("GET", url)
    store.set_cookie_header(request)
    return request.headers.get("cookie")


def test_limits_validation():
    with pytest.raises(TypeError):
        httpx.CookieStore(max_cookies="1")  # type: ignore
    with pytest.raises(ValueError):
        httpx.CookieStore(max_cookies_per_domain=-1)


def test_combined_header_with_expires_comma():
    store = httpx.CookieStore()
    extract(
        store,
        "https://example.com/",
        "a=1; Expires=Wed, 21 Oct 2099 07:28:00 GMT, b=2",
    )
    assert cookie_header(store, "https://example.com/") == "a=1; b=2"


def test_malformed_and_valueless_attributes():
    store = httpx.CookieStore()
    extract(store, "https://example.com/", "", "novalue", "a=1; Domain", "b=; Foo=bar")
    assert dict(store) == {"b": ""}


def test_host_only_and_domain():
    store = httpx.CookieStore()
    extract(store, "https://a.example.com/", "h=1", "d=2; Domain=EXAMPLE.com")
    extract(store, "https://a.example.com/", "x=3; Domain=other.com")
    assert cookie_header(store, "https://a.example.com/") == "h=1; d=2"
    assert cookie_header(store, "https://b.a.example.com/") == "d=2"


def test_path_matching_and_order():
    store = httpx.CookieStore()
    extract(store, "https://example.com/sub/page", "a=1", "b=2; Path=/")
    assert cookie_header(store, "https://example.com/sub/x") == "a=1; b=2"
    assert cookie_header(store, "https://example.com/submarine") == "b=2"


def test_secure_and_prefixes():
    store = httpx.CookieStore()
    extract(store, "http://example.com/", "__Secure-a=1; Secure")
    extract(
        store,
        "https://example.com/",
        "s=1; Secure",
        "__Host-b=2; Secure; Path=/; Domain=example.com",
        "__Host-c=3; Secure; Path=/",
    )
    assert cookie_header(store, "http://example.com/") is None
    assert cookie_header(store, "https://example.com/") == "s=1; __Host-c=3"


def test_expiry():
    store = httpx.CookieStore()
    extract(store, "https://example.com/", "a=1", "b=2", "c=3; Expires=garbage")
    extract(
        store,
        "https://example.com/",
        "a=1; Max-Age=0",
        "b=2; Expires=Wed, 21 Oct 2000 07:28:00 GMT",
        "d=4; Max-Age=100; Expires=Wed, 21 Oct 2000 07:28:00 GMT",
    )
    assert dict(store) == {"c": "3", "d": "4"}


def test_eviction_and_replacement_order():
    store = httpx.CookieStore(max_cookies=3, max_cookies_per_domain=2)
    extract(store, "https://a.com/", "a=1", "b=2")
    extract(store, "https://a.com/", "a=9")
    extract(store, "https://a.com/", "c=3")
    assert dict(store) == {"a": "9", "c": "3"}
    extract(store, "https://b.com/", "d=4", "e=5")
    assert dict(store) == {"c": "3", "d": "4", "e": "5"}


def test_conflict():
    store = httpx.CookieStore()
    store.set("a", "1", domain="x.com")
    store.set("a", "2", domain="y.com")
    with pytest.raises(httpx.CookieConflict):
        store["a"]
    assert store.get("a", domain="x.com") == "1"


def test_update_forms():
    jar = http.cookiejar.CookieJar()
    cookies = httpx.Cookies({"c": "3"})
    store = httpx.CookieStore()
    store.update({"a": "1"})
    store.update([("b", "2")])
    store.update(cookies)
    store.update(jar)
    store.update(httpx.CookieStore({"d": "4"}))
    assert cookie_header(store, "http://any.host/") == "a=1; b=2; c=3; d=4"


def test_client_persistence():
    def handler(request):
        return httpx.Response(200, headers={"set-cookie": "s=1"}, json=None)

    store = httpx.CookieStore()
    with httpx.Client(cookies=store, transport=httpx.MockTransport(handler)) as c:
        c.get("http://example.com/")
        response = c.get("http://example.com/")
        assert response.request.headers["cookie"] == "s=1"
    assert c.cookies is store
