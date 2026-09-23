from __future__ import annotations

import dataclasses
import email.utils
import ipaddress
import re
import time
import typing
from http.cookiejar import Cookie, CookieJar

from ._exceptions import CookieConflict

if typing.TYPE_CHECKING:  # pragma: no cover
    from ._models import Request, Response
    from ._types import CookieTypes

__all__ = ["CookieStore"]


# Splits a header value holding several comma-joined cookies, without splitting
# on the comma that appears inside an `Expires=Wed, 09 Jun 2021 ...` attribute.
_COMBINED_COOKIES_RE = re.compile(r",(?=\s*[^;,=\s]+=)")
_MAX_AGE_RE = re.compile(r"-?[0-9]+")
_VALUE_REQUIRED_ATTRIBUTES = ("domain", "max-age", "expires")
_KNOWN_ATTRIBUTES = ("domain", "path", "max-age", "expires", "secure")
# Keeps `now + max_age` representable as a float.
_MAX_AGE_LIMIT = 2**53


@dataclasses.dataclass
class _StoredCookie:
    name: str
    value: str
    # An empty domain matches any host.
    domain: str
    path: str
    host_only: bool
    secure: bool
    expires: float | None

    @property
    def key(self) -> tuple[str, str, str]:
        return (self.name, self.domain, self.path)

    def is_expired(self, now: float) -> bool:
        return self.expires is not None and self.expires <= now

    def matches_host(self, host: str) -> bool:
        if not self.domain:
            return True
        if self.host_only:
            return host == self.domain
        return _domain_match(host, self.domain)

    def to_cookiejar_cookie(self) -> Cookie:
        domain_specified = bool(self.domain) and not self.host_only
        domain = f".{self.domain}" if domain_specified else self.domain
        return Cookie(
            version=0,
            name=self.name,
            value=self.value,
            port=None,
            port_specified=False,
            domain=domain,
            domain_specified=domain_specified,
            domain_initial_dot=domain_specified,
            path=self.path,
            path_specified=True,
            secure=self.secure,
            expires=None if self.expires is None else int(self.expires),
            discard=self.expires is None,
            comment=None,
            comment_url=None,
            rest={},
            rfc2109=False,
        )

    @classmethod
    def from_cookiejar_cookie(cls, cookie: Cookie) -> _StoredCookie:
        domain = _normalize_domain(cookie.domain)
        return cls(
            name=cookie.name,
            value="" if cookie.value is None else cookie.value,
            domain=domain,
            path=cookie.path or "/",
            host_only=bool(domain) and not cookie.domain_specified,
            secure=cookie.secure,
            expires=None if cookie.expires is None else float(cookie.expires),
        )


def _normalize_domain(domain: str) -> str:
    return domain.lstrip(".").lower()


def _is_ip_address(host: str) -> bool:
    try:
        ipaddress.ip_address(host)
    except ValueError:
        return False
    return True


def _domain_match(host: str, domain: str) -> bool:
    if host == domain:
        return True
    return host.endswith(f".{domain}") and not _is_ip_address(host)


def _path_match(request_path: str, cookie_path: str) -> bool:
    if request_path == cookie_path:
        return True
    if not request_path.startswith(cookie_path):
        return False
    return cookie_path.endswith("/") or request_path[len(cookie_path)] == "/"


def _default_path(request_path: str) -> str:
    if not request_path.startswith("/") or request_path.count("/") == 1:
        return "/"
    return request_path[: request_path.rfind("/")]


def _request_host(request: Request) -> str:
    return request.url.raw_host.decode("ascii").lower()


def _request_path(request: Request) -> str:
    return request.url.raw_path.decode("ascii").partition("?")[0]


def _parse_expires(value: str) -> float | None:
    try:
        parsed = email.utils.parsedate_tz(value)
        return None if parsed is None else float(email.utils.mktime_tz(parsed))
    except (OverflowError, ValueError):
        return None


def _parse_set_cookie(
    cookie_string: str,
) -> tuple[str, str, dict[str, str]] | None:
    """
    Parse a single cookie string into `(name, value, attributes)`, returning
    `None` if the cookie should be ignored.
    """
    name_value, *attribute_parts = cookie_string.split(";")
    if "=" not in name_value:
        return None
    name, _, value = name_value.partition("=")
    name, value = name.strip(), value.strip()
    if not name:
        return None

    attributes: dict[str, str] = {}
    for part in attribute_parts:
        attribute_name, _, attribute_value = part.partition("=")
        attribute_name = attribute_name.strip().lower()
        attribute_value = attribute_value.strip()
        if attribute_name in _VALUE_REQUIRED_ATTRIBUTES and not attribute_value:
            return None
        if attribute_name in _KNOWN_ATTRIBUTES:
            attributes[attribute_name] = attribute_value
    return name, value, attributes


def _validate_limit(name: str, value: int | None) -> int | None:
    if value is None:
        return None
    if isinstance(value, bool) or not isinstance(value, int):
        raise TypeError(f"{name} must be an int or None, not {type(value).__name__}")
    if value < 0:
        raise ValueError(f"{name} must not be negative, got {value}")
    return value


class CookieStore(typing.MutableMapping[str, str]):
    """
    A deterministic HTTP cookie store, as a mutable mapping of names to values.
    """

    def __init__(
        self,
        cookies: CookieTypes | None = None,
        *,
        max_cookies: int | None = None,
        max_cookies_per_domain: int | None = None,
    ) -> None:
        self._max_cookies = _validate_limit("max_cookies", max_cookies)
        self._max_cookies_per_domain = _validate_limit(
            "max_cookies_per_domain", max_cookies_per_domain
        )
        # Insertion order is creation order.
        self._cookies: dict[tuple[str, str, str], _StoredCookie] = {}
        self.update(cookies)

    def extract_cookies(self, response: Response) -> None:
        """
        Loads any cookies based on the response `Set-Cookie` headers.
        """
        request = response.request
        host = _request_host(request)
        request_path = _request_path(request)
        is_secure = request.url.scheme == "https"
        now = time.time()

        for header in response.headers.get_list("set-cookie"):
            for cookie_string in _COMBINED_COOKIES_RE.split(header):
                self._extract_cookie(cookie_string, host, request_path, is_secure, now)

    def _extract_cookie(
        self,
        cookie_string: str,
        host: str,
        request_path: str,
        is_secure: bool,
        now: float,
    ) -> None:
        parsed = _parse_set_cookie(cookie_string)
        if parsed is None:
            return
        name, value, attributes = parsed

        if "domain" in attributes:
            domain = _normalize_domain(attributes["domain"])
            if not domain or not _domain_match(host, domain):
                return
            host_only = False
        else:
            domain = host
            host_only = True

        path = attributes.get("path", "")
        if not path.startswith("/"):
            path = _default_path(request_path)

        secure = "secure" in attributes
        if name.startswith(("__Secure-", "__Host-")) and not (secure and is_secure):
            return
        if name.startswith("__Host-") and (
            "domain" in attributes or attributes.get("path") != "/"
        ):
            return

        expires: float | None = None
        max_age = attributes.get("max-age", "")
        if _MAX_AGE_RE.fullmatch(max_age):
            delta = int(max_age)
            if delta <= 0:
                self._cookies.pop((name, domain, path), None)
                return
            expires = now + min(delta, _MAX_AGE_LIMIT)
        elif "expires" in attributes:
            expires = _parse_expires(attributes["expires"])
            if expires is not None and expires <= now:
                self._cookies.pop((name, domain, path), None)
                return

        self._store(
            _StoredCookie(
                name=name,
                value=value,
                domain=domain,
                path=path,
                host_only=host_only,
                secure=secure,
                expires=expires,
            )
        )

    def set_cookie_header(self, request: Request) -> None:
        """
        Sets an appropriate 'Cookie:' HTTP header on the `Request`.
        """
        if "Cookie" in request.headers:
            return

        host = _request_host(request)
        request_path = _request_path(request)
        is_secure = request.url.scheme == "https"

        matching = [
            cookie
            for cookie in self._live_cookies()
            if (is_secure or not cookie.secure)
            and cookie.matches_host(host)
            and _path_match(request_path, cookie.path)
        ]
        if not matching:
            return
        # A stable sort, so cookies with equal path lengths remain oldest first.
        matching.sort(key=lambda cookie: -len(cookie.path))
        request.headers["Cookie"] = "; ".join(
            f"{cookie.name}={cookie.value}" for cookie in matching
        )

    def set(self, name: str, value: str, domain: str = "", path: str = "/") -> None:
        """
        Set a cookie value by name. May optionally include domain and path.
        """
        self._store(
            _StoredCookie(
                name=name,
                value=value,
                domain=_normalize_domain(domain),
                path=path,
                host_only=False,
                secure=False,
                expires=None,
            )
        )

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
        matches = self._select(name, domain, path)
        if len(matches) > 1:
            raise CookieConflict(f"Multiple cookies exist with name={name}")
        return matches[0].value if matches else default

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
            del self._cookies[cookie.key]

    def clear(self, domain: str | None = None, path: str | None = None) -> None:
        """
        Delete all cookies. Optionally include a domain and path in
        order to only delete a subset of all the cookies.
        """
        if domain is not None:
            domain = _normalize_domain(domain)
        self._cookies = {
            key: cookie
            for key, cookie in self._cookies.items()
            if not (
                (domain is None or cookie.domain == domain)
                and (path is None or cookie.path == path)
            )
        }

    def update(self, cookies: CookieTypes | None = None) -> None:  # type: ignore
        if cookies is None:
            return
        if isinstance(cookies, CookieStore):
            for cookie in cookies._live_cookies():
                self._store(dataclasses.replace(cookie))
        elif isinstance(cookies, dict):
            for name, value in cookies.items():
                self.set(name, value)
        elif isinstance(cookies, list):
            for name, value in cookies:
                self.set(name, value)
        else:
            jar = cookies if isinstance(cookies, CookieJar) else cookies.jar
            for jar_cookie in jar:
                if not jar_cookie.is_expired():
                    self._store(_StoredCookie.from_cookiejar_cookie(jar_cookie))

    def _as_cookiejar_cookies(self) -> list[Cookie]:
        return [cookie.to_cookiejar_cookie() for cookie in self._live_cookies()]

    def _live_cookies(self) -> list[_StoredCookie]:
        now = time.time()
        expired = [key for key, c in self._cookies.items() if c.is_expired(now)]
        for key in expired:
            del self._cookies[key]
        return list(self._cookies.values())

    def _select(
        self, name: str, domain: str | None, path: str | None
    ) -> list[_StoredCookie]:
        if domain is not None:
            domain = _normalize_domain(domain)
        return [
            cookie
            for cookie in self._live_cookies()
            if cookie.name == name
            and (domain is None or cookie.domain == domain)
            and (path is None or cookie.path == path)
        ]

    def _store(self, cookie: _StoredCookie) -> None:
        # Replacing a cookie moves it to the end, treating it as newly created.
        self._cookies.pop(cookie.key, None)
        self._cookies[cookie.key] = cookie
        self._enforce_limits(cookie.domain)

    def _enforce_limits(self, domain: str) -> None:
        live_cookies = self._live_cookies()
        if self._max_cookies_per_domain is not None:
            same_domain = [c for c in live_cookies if c.domain == domain]
            excess = len(same_domain) - self._max_cookies_per_domain
            for cookie in same_domain[: max(excess, 0)]:
                del self._cookies[cookie.key]
        if self._max_cookies is not None:
            excess = len(self._cookies) - self._max_cookies
            for key in list(self._cookies)[: max(excess, 0)]:
                del self._cookies[key]

    def __setitem__(self, name: str, value: str) -> None:
        return self.set(name, value)

    def __getitem__(self, name: str) -> str:
        value = self.get(name)
        if value is None:
            raise KeyError(name)
        return value

    def __delitem__(self, name: str) -> None:
        if not self._select(name, None, None):
            raise KeyError(name)
        self.delete(name)

    def __len__(self) -> int:
        return len(self._live_cookies())

    def __iter__(self) -> typing.Iterator[str]:
        return iter([cookie.name for cookie in self._live_cookies()])

    def __bool__(self) -> bool:
        return len(self) > 0

    def __repr__(self) -> str:
        cookies_repr = ", ".join(
            f"<Cookie {cookie.name}={cookie.value} for {cookie.domain} />"
            for cookie in self._live_cookies()
        )
        return f"<CookieStore[{cookies_repr}]>"
