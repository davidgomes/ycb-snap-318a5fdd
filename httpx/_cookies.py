from __future__ import annotations

import datetime
import ipaddress
import re
import time
import typing
from collections.abc import Mapping
from http.cookiejar import Cookie, CookieJar

from ._exceptions import CookieConflict
from ._types import CookieTypes

if typing.TYPE_CHECKING:  # pragma: no cover
    from ._models import Request, Response
    from ._urls import URL

__all__ = ["CookieStore"]


_WHITESPACE = " \t"

# A comma only separates two cookies when it is followed by a `name=` pair,
# so that the comma in `Expires=Wed, 09 Jun 2021 10:18:14 GMT` is preserved.
_COOKIE_SEPARATOR = re.compile(r",(?=[^;,]*=)")

_CONTROL_CHARACTERS = re.compile(r"[\x00-\x08\x0a-\x1f\x7f]")

# A cookie with any of these attributes but no attribute value is ignored.
_VALUE_REQUIRED = ("domain", "max-age", "expires")

_MAX_AGE = re.compile(r"-?[0-9]+")

# The cookie-date grammar from RFC 6265, section 5.1.1.
_DATE_DELIMITERS = re.compile(r"[\x09\x20-\x2f\x3b-\x40\x5b-\x60\x7b-\x7e]+")
_DATE_TIME = re.compile(r"([0-9]{1,2}):([0-9]{1,2}):([0-9]{1,2})(?![0-9])")
_DATE_DAY_OF_MONTH = re.compile(r"[0-9]{1,2}(?![0-9])")
_DATE_YEAR = re.compile(r"[0-9]{2,4}(?![0-9])")
_DATE_MONTHS = [
    "jan",
    "feb",
    "mar",
    "apr",
    "may",
    "jun",
    "jul",
    "aug",
    "sep",
    "oct",
    "nov",
    "dec",
]


def _parse_cookie_date(value: str) -> float | None:
    """
    Parse an `Expires` attribute into a unix timestamp, using the lenient
    algorithm from RFC 6265, section 5.1.1. Returns `None` if invalid.
    """
    hms: tuple[int, int, int] | None = None
    day: int | None = None
    month: int | None = None
    year: int | None = None

    for token in _DATE_DELIMITERS.split(value):
        time_match = _DATE_TIME.match(token)
        day_match = _DATE_DAY_OF_MONTH.match(token)
        year_match = _DATE_YEAR.match(token)
        if hms is None and time_match:
            hms = (int(time_match[1]), int(time_match[2]), int(time_match[3]))
        elif day is None and day_match:
            day = int(day_match[0])
        elif month is None and token[:3].lower() in _DATE_MONTHS:
            month = _DATE_MONTHS.index(token[:3].lower()) + 1
        elif year is None and year_match:
            year = int(year_match[0])

    if hms is None or day is None or month is None or year is None:
        return None

    if 70 <= year <= 99:
        year += 1900
    elif year <= 69:
        year += 2000

    hour, minute, second = hms
    if year < 1601 or hour > 23 or minute > 59 or second > 59:
        return None

    try:
        date = datetime.datetime(
            year, month, day, hour, minute, second, tzinfo=datetime.timezone.utc
        )
    except ValueError:  # Days that don't exist, such as "0 Jan" or "30 Feb".
        return None
    return date.timestamp()


def _normalize_domain(domain: str) -> str:
    domain = domain.lower()
    return domain[1:] if domain.startswith(".") else domain


def _is_ip_address(host: str) -> bool:
    try:
        ipaddress.ip_address(host)
    except ValueError:
        return False
    return True


def _domain_match(host: str, domain: str) -> bool:
    """
    Domain matching, as defined by RFC 6265, section 5.1.3.
    """
    if host == domain:
        return True
    return host.endswith("." + domain) and not _is_ip_address(host)


def _default_path(request_path: str) -> str:
    """
    The default cookie path, as defined by RFC 6265, section 5.1.4.
    """
    if not request_path.startswith("/") or request_path.count("/") == 1:
        return "/"
    return request_path[: request_path.rindex("/")]


def _path_match(request_path: str, cookie_path: str) -> bool:
    """
    Path matching, as defined by RFC 6265, section 5.1.4.
    """
    if not request_path.startswith(cookie_path):
        return False
    return (
        len(request_path) == len(cookie_path)
        or cookie_path.endswith("/")
        or request_path[len(cookie_path)] == "/"
    )


def _validate_limit(name: str, value: int | None) -> int | None:
    if value is None:
        return None
    if isinstance(value, bool) or not isinstance(value, int):
        raise TypeError(f"{name} must be an int or None, not {type(value).__name__}")
    if value < 0:
        raise ValueError(f"{name} must not be negative, got {value}")
    return value


class _StoredCookie(typing.NamedTuple):
    name: str
    value: str
    # The request host for host-only cookies. Empty for cookies that were
    # set without a domain, which match any host.
    domain: str
    path: str
    host_only: bool = False
    secure: bool = False
    expires: float | None = None

    @property
    def key(self) -> tuple[str, str, str]:
        return (self.name, self.domain, self.path)

    def is_expired(self, now: float) -> bool:
        return self.expires is not None and self.expires <= now

    def matches(self, host: str, path: str, is_secure: bool) -> bool:
        if self.host_only:
            host_matches = host == self.domain
        else:
            host_matches = not self.domain or _domain_match(host, self.domain)
        return (
            host_matches
            and _path_match(path, self.path)
            and (is_secure or not self.secure)
        )

    @classmethod
    def from_cookiejar(cls, cookie: Cookie) -> _StoredCookie:
        domain = _normalize_domain(cookie.domain)
        return cls(
            name=cookie.name,
            value=cookie.value or "",
            domain=domain,
            path=cookie.path,
            host_only=bool(domain) and not cookie.domain_specified,
            secure=cookie.secure,
            expires=cookie.expires,
        )

    def to_cookiejar(self) -> Cookie:
        return Cookie(
            version=0,
            name=self.name,
            value=self.value,
            port=None,
            port_specified=False,
            domain=self.domain,
            domain_specified=bool(self.domain) and not self.host_only,
            domain_initial_dot=False,
            path=self.path,
            path_specified=True,
            secure=self.secure,
            expires=None if self.expires is None else int(self.expires),
            discard=self.expires is None,
            comment=None,
            comment_url=None,
            rest={},
        )


class CookieStore(typing.MutableMapping[str, str]):
    """
    HTTP Cookies, as a mutable mapping, with deterministic RFC 6265 handling
    of domains, paths, expiry, `Secure`, and the `__Secure-`/`__Host-` prefixes.
    """

    def __init__(
        self,
        max_cookies: int | None = None,
        max_cookies_per_domain: int | None = None,
    ) -> None:
        self._max_cookies = _validate_limit("max_cookies", max_cookies)
        self._max_cookies_per_domain = _validate_limit(
            "max_cookies_per_domain", max_cookies_per_domain
        )
        # Insertion order is creation order, which determines both eviction
        # and the order of cookies with equal path lengths in `Cookie` headers.
        self._cookies: dict[tuple[str, str, str], _StoredCookie] = {}

    @property
    def max_cookies(self) -> int | None:
        return self._max_cookies

    @property
    def max_cookies_per_domain(self) -> int | None:
        return self._max_cookies_per_domain

    def extract_cookies(self, response: Response) -> None:
        """
        Loads any cookies based on the response `Set-Cookie` headers.
        """
        url = response.request.url
        now = time.time()
        for header in response.headers.get_list("set-cookie"):
            for set_cookie in _COOKIE_SEPARATOR.split(header):
                self._extract_cookie(set_cookie, url, now)

    def set_cookie_header(self, request: Request) -> None:
        """
        Sets an appropriate 'Cookie:' HTTP header on the `Request`.
        """
        if "cookie" in request.headers:
            return

        host = request.url.raw_host.decode("ascii")
        path = request.url.raw_path.decode("ascii").partition("?")[0]
        is_secure = request.url.scheme == "https"
        cookies = [
            cookie
            for cookie in self._live_cookies()
            if cookie.matches(host, path, is_secure)
        ]
        if cookies:
            # The sort is stable, so equal path lengths keep their creation order.
            cookies.sort(key=lambda cookie: len(cookie.path), reverse=True)
            request.headers["Cookie"] = "; ".join(
                f"{cookie.name}={cookie.value}" for cookie in cookies
            )

    def set(self, name: str, value: str, domain: str = "", path: str = "/") -> None:
        """
        Set a cookie value by name. May optionally include domain and path.

        Cookies set without a domain are sent to any host.
        """
        cookie = _StoredCookie(name, value, _normalize_domain(domain), path)
        self._store(cookie, time.time())

    def get(  # type: ignore
        self,
        name: str,
        default: str | None = None,
        domain: str | None = None,
        path: str | None = None,
    ) -> str | None:
        """
        Get a cookie by name. May optionally include domain and path
        in order to specify exactly which cookie to retrieve.
        """
        cookie = self._select_one(name, domain, path)
        return default if cookie is None else cookie.value

    def delete(
        self,
        name: str,
        domain: str | None = None,
        path: str | None = None,
    ) -> None:
        """
        Delete a cookie by name. May optionally include domain and path
        in order to specify exactly which cookie to delete.
        """
        for cookie in self._select(name, domain, path):
            self._cookies.pop(cookie.key, None)

    def clear(self, domain: str | None = None, path: str | None = None) -> None:
        """
        Delete all cookies. Optionally include a domain and path in
        order to only delete a subset of all the cookies.
        """
        for cookie in self._select(None, domain, path):
            self._cookies.pop(cookie.key, None)

    def update(self, cookies: CookieTypes | None = None) -> None:  # type: ignore
        from ._models import Cookies

        now = time.time()
        if cookies is None:
            return
        elif isinstance(cookies, CookieStore):
            for cookie in cookies._live_cookies():
                self._store(cookie, now)
        elif isinstance(cookies, (Cookies, CookieJar)):
            jar = cookies.jar if isinstance(cookies, Cookies) else cookies
            for jar_cookie in jar:
                self._store(_StoredCookie.from_cookiejar(jar_cookie), now)
        else:
            items = cookies.items() if isinstance(cookies, Mapping) else cookies
            for name, value in items:
                self.set(name, value)

    def _extract_cookie(self, set_cookie: str, url: URL, now: float) -> None:
        """
        Store a single cookie, following the parsing and storage models from
        RFC 6265, sections 5.2 and 5.3. Malformed cookies are ignored.
        """
        if _CONTROL_CHARACTERS.search(set_cookie):
            return

        name_value, *attributes = set_cookie.split(";")
        name, separator, value = name_value.partition("=")
        name, value = name.strip(_WHITESPACE), value.strip(_WHITESPACE)
        if not separator or not name:
            return

        domain: str | None = None
        path: str | None = None
        max_age: int | None = None
        expires: float | None = None
        secure = False
        for attribute in attributes:
            attribute_name, _, attribute_value = attribute.partition("=")
            attribute_name = attribute_name.strip(_WHITESPACE).lower()
            attribute_value = attribute_value.strip(_WHITESPACE)
            if not attribute_value and attribute_name in _VALUE_REQUIRED:
                return
            if attribute_name == "domain":
                domain = _normalize_domain(attribute_value)
            elif attribute_name == "path":
                path = attribute_value
            elif attribute_name == "max-age":
                if _MAX_AGE.fullmatch(attribute_value):
                    max_age = int(attribute_value)
            elif attribute_name == "expires":
                parsed_expires = _parse_cookie_date(attribute_value)
                if parsed_expires is not None:
                    expires = parsed_expires
            elif attribute_name == "secure":
                secure = True

        request_host = url.raw_host.decode("ascii")
        if domain and not _domain_match(request_host, domain):
            return

        request_path = url.raw_path.decode("ascii").partition("?")[0]
        cookie_path = (
            path if path and path.startswith("/") else _default_path(request_path)
        )

        prefix = name.lower()
        if prefix.startswith(("__secure-", "__host-")):
            if not secure or url.scheme != "https":
                return
        if prefix.startswith("__host-"):
            if domain is not None or path is None or cookie_path != "/":
                return

        if max_age is not None:
            try:
                expires = now + max_age
            except OverflowError:  # An absurdly large number of seconds.
                expires = None if max_age > 0 else now

        cookie = _StoredCookie(
            name=name,
            value=value,
            domain=domain or request_host,
            path=cookie_path,
            host_only=not domain,
            secure=secure,
            expires=expires,
        )
        self._store(cookie, now)

    def _store(self, cookie: _StoredCookie, now: float) -> None:
        self._remove_expired(now)
        # A replaced cookie is treated as newly created, so it moves to the end.
        self._cookies.pop(cookie.key, None)
        if cookie.is_expired(now):
            return
        self._cookies[cookie.key] = cookie

        if self._max_cookies_per_domain is not None:
            same_domain = [key for key in self._cookies if key[1] == cookie.domain]
            excess = len(same_domain) - self._max_cookies_per_domain
            for key in same_domain[: max(excess, 0)]:
                del self._cookies[key]

        if self._max_cookies is not None:
            excess = len(self._cookies) - self._max_cookies
            for key in list(self._cookies)[: max(excess, 0)]:
                del self._cookies[key]

    def _remove_expired(self, now: float) -> None:
        for cookie in list(self._cookies.values()):
            if cookie.is_expired(now):
                self._cookies.pop(cookie.key, None)

    def _live_cookies(self) -> list[_StoredCookie]:
        self._remove_expired(time.time())
        return list(self._cookies.values())

    def _select(
        self, name: str | None, domain: str | None, path: str | None
    ) -> list[_StoredCookie]:
        if domain is not None:
            domain = _normalize_domain(domain)
        return [
            cookie
            for cookie in self._live_cookies()
            if (name is None or cookie.name == name)
            and (domain is None or cookie.domain == domain)
            and (path is None or cookie.path == path)
        ]

    def _select_one(
        self, name: str, domain: str | None, path: str | None
    ) -> _StoredCookie | None:
        cookies = self._select(name, domain, path)
        if len(cookies) > 1:
            raise CookieConflict(f"Multiple cookies exist with name={name}")
        return cookies[0] if cookies else None

    def __setitem__(self, name: str, value: str) -> None:
        return self.set(name, value)

    def __getitem__(self, name: str) -> str:
        cookie = self._select_one(name, None, None)
        if cookie is None:
            raise KeyError(name)
        return cookie.value

    def __delitem__(self, name: str) -> None:
        if name not in self:
            raise KeyError(name)
        self.delete(name)

    def __contains__(self, name: object) -> bool:
        return any(cookie.name == name for cookie in self._live_cookies())

    def __len__(self) -> int:
        return len(self._live_cookies())

    def __iter__(self) -> typing.Iterator[str]:
        return iter([cookie.name for cookie in self._live_cookies()])

    def __repr__(self) -> str:
        cookies_repr = ", ".join(
            [
                f"<Cookie {cookie.name}={cookie.value} for {cookie.domain} />"
                for cookie in self._live_cookies()
            ]
        )

        return f"<CookieStore[{cookies_repr}]>"
