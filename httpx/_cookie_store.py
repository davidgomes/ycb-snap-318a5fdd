"""
Deterministic cookie container.

``CookieStore`` follows the cookie storage and sending rules used by modern
clients: host-only versus domain cookies, default-path and path-match, the
``Secure`` flag, ``__Secure-`` / ``__Host-`` prefixes, and ``Max-Age`` /
``Expires``. It is intentionally separate from :class:`httpx.Cookies`, which
keeps the stdlib cookie jar behavior.
"""

from __future__ import annotations

import re
import time
import typing
from collections.abc import Mapping
from http import cookiejar as cookiejar_module
from http.cookiejar import Cookie, CookieJar

from ._exceptions import CookieConflict

if typing.TYPE_CHECKING:
    from ._models import Request, Response
    from ._types import CookieTypes


_COOKIE_NAME_RE = re.compile(r"^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$")
# A comma separates two combined Set-Cookie values when the following text
# looks like ``name=``. ``Expires`` dates also contain a comma, but the text
# after that comma is a date token, not a cookie-pair.
_COOKIE_PAIR_RE = re.compile(r"\s*[!#$%&'*+\-.^_`|~0-9A-Za-z]+\s*=")
_VALUE_REQUIRED_ATTRIBUTES = {"domain", "max-age", "expires"}


def _validate_limit(name: str, value: int | None) -> int | None:
    if value is None:
        return None
    # ``bool`` is an ``int`` subclass, but it is not a cookie limit.
    if isinstance(value, bool) or not isinstance(value, int):
        raise TypeError(f"{name} must be an int or None")
    if value < 0:
        raise ValueError(f"{name} must be non-negative")
    return value


def _unquote(value: str) -> str:
    if len(value) >= 2 and value.startswith('"') and value.endswith('"'):
        return value[1:-1]
    return value


def _normalize_host(host: str) -> str:
    return host.strip().lower().rstrip(".")


def _normalize_domain_attribute(domain: str) -> str:
    """Lower-case a Domain attribute and strip one leading dot."""
    normalized = domain.strip().lower().rstrip(".")
    if normalized.startswith("."):
        normalized = normalized[1:]
    return normalized


def _normalize_lookup_domain(domain: str) -> str:
    if domain == "":
        return ""
    return _normalize_domain_attribute(domain)


def _is_ip_address(host: str) -> bool:
    if ":" in host:
        return True
    parts = host.split(".")
    if len(parts) != 4:
        return False
    for part in parts:
        if not part.isdigit() or int(part) > 255:
            return False
    return True


def _domain_matches(host: str, domain: str) -> bool:
    """Return whether ``host`` domain-matches ``domain`` (RFC 6265)."""
    host = _normalize_host(host)
    domain = _normalize_domain_attribute(domain)
    if not host or not domain:
        return False
    if host == domain:
        return True
    # Suffix matching does not apply to IP addresses.
    if _is_ip_address(host) or _is_ip_address(domain):
        return False
    return host.endswith("." + domain)


def _default_path(request_path: str) -> str:
    """Default cookie path for a request URI path (RFC 6265)."""
    if not request_path.startswith("/"):
        return "/"
    slash = request_path.rfind("/")
    if slash <= 0:
        return "/"
    return request_path[:slash]


def _path_matches(request_path: str, cookie_path: str) -> bool:
    """Return whether ``request_path`` path-matches ``cookie_path``."""
    if request_path == cookie_path:
        return True
    if not cookie_path or not request_path.startswith(cookie_path):
        return False
    if cookie_path.endswith("/"):
        return True
    return (
        len(request_path) > len(cookie_path) and request_path[len(cookie_path)] == "/"
    )


def _parse_expires(value: str) -> float | None:
    """Return an HTTP-date as a UTC timestamp, or ``None`` if it is invalid."""
    # ``http2time`` is public, but the type stubs omit it.
    parsed = cookiejar_module.http2time(value)  # type: ignore[attr-defined]
    if parsed is None:
        return None
    return float(parsed)


def _parse_max_age(value: str) -> int | None:
    """Parse a Max-Age delta. ``None`` means the cookie must be ignored."""
    if value.startswith("-"):
        digits = value[1:]
    else:
        digits = value
    if not digits or not digits.isdigit():
        return None
    if value.startswith("-"):
        return -int(digits)
    return int(digits)


def _split_set_cookie_header(header_value: str) -> list[str]:
    """Split a Set-Cookie header that may contain more than one cookie."""
    parts: list[str] = []
    start = 0
    in_quotes = False
    index = 0
    length = len(header_value)
    while index < length:
        char = header_value[index]
        if char == '"':
            in_quotes = not in_quotes
            index += 1
            continue
        starts_cookie = _COOKIE_PAIR_RE.match(header_value, index + 1)
        if char == "," and not in_quotes and starts_cookie:
            parts.append(header_value[start:index])
            start = index + 1
            index = start
            continue
        index += 1
    parts.append(header_value[start:])
    return [part for part in parts if part.strip()]


class _ParsedSetCookie(typing.NamedTuple):
    name: str
    value: str
    domain: str | None
    path: str | None
    secure: bool
    has_max_age: bool
    max_age: int | None
    expires: str | None


def _parse_set_cookie(cookie_string: str) -> _ParsedSetCookie | None:
    """Parse one Set-Cookie string. ``None`` means ignore it."""
    text = cookie_string.strip()
    if not text:
        return None

    pair, separator, remainder = text.partition(";")
    pair = pair.strip()
    if "=" not in pair:
        return None
    raw_name, raw_value = pair.split("=", 1)
    name = raw_name.strip()
    value = _unquote(raw_value.strip())
    if not name or _COOKIE_NAME_RE.fullmatch(name) is None:
        return None

    domain: str | None = None
    path: str | None = None
    secure = False
    has_max_age = False
    max_age: int | None = None
    expires: str | None = None

    if separator:
        for attribute in remainder.split(";"):
            attribute = attribute.strip()
            if not attribute:
                continue
            if "=" in attribute:
                raw_attr_name, raw_attr_value = attribute.split("=", 1)
                attr_name = raw_attr_name.strip().lower()
                attr_value: str | None = _unquote(raw_attr_value.strip())
            else:
                attr_name = attribute.lower()
                attr_value = None

            if attr_name in _VALUE_REQUIRED_ATTRIBUTES and (
                attr_value is None or attr_value == ""
            ):
                return None

            if attr_name == "domain":
                domain = attr_value
            elif attr_name == "max-age":
                if attr_value is None:
                    return None
                parsed_age = _parse_max_age(attr_value)
                if parsed_age is None:
                    return None
                has_max_age = True
                max_age = parsed_age
            elif attr_name == "expires":
                expires = attr_value
            elif attr_name == "path":
                if attr_value and attr_value.startswith("/"):
                    path = attr_value
                else:
                    # Empty or relative Path uses the default path.
                    path = None
            elif attr_name == "secure":
                secure = True
            # Unknown attributes are ignored.

    return _ParsedSetCookie(
        name=name,
        value=value,
        domain=domain,
        path=path,
        secure=secure,
        has_max_age=has_max_age,
        max_age=max_age,
        expires=expires,
    )


class _StoredCookie:
    def __init__(
        self,
        *,
        name: str,
        value: str,
        domain: str,
        path: str,
        host_only: bool,
        secure: bool,
        expires: float | None,
    ) -> None:
        self.name = name
        self.value = value
        self.domain = domain
        self.path = path
        self.host_only = host_only
        self.secure = secure
        self.expires = expires
        self.created = 0

    @property
    def key(self) -> tuple[str, str, str]:
        return (self.name, self.domain, self.path)

    def clone(self) -> _StoredCookie:
        return _StoredCookie(
            name=self.name,
            value=self.value,
            domain=self.domain,
            path=self.path,
            host_only=self.host_only,
            secure=self.secure,
            expires=self.expires,
        )

    def to_stdlib(self) -> Cookie:
        if self.host_only:
            domain = self.domain
            domain_specified = False
        elif self.domain == "":
            domain = ""
            domain_specified = False
        else:
            domain = self.domain
            domain_specified = True
        if self.expires is None or self.expires == float("inf"):
            expires: int | None = None
            discard = True
        else:
            expires = int(self.expires)
            discard = False
        return Cookie(
            0,
            self.name,
            self.value,
            None,
            False,
            domain,
            domain_specified,
            False,
            self.path,
            True,
            self.secure,
            expires,
            discard,
            None,
            None,
            {},
        )


class CookieStore(typing.MutableMapping[str, str]):
    """
    A mutable mapping of cookie names to values with deterministic storage.

    Cookies are kept in creation order. Replacing a cookie with the same name,
    domain, and path makes it the newest cookie. When ``max_cookies_per_domain``
    or ``max_cookies`` is exceeded, the oldest cookies are evicted, per-domain
    limit first and then the global limit.

    A cookie stored with ``domain=""`` (the default for :meth:`set` and for
    mapping or list inputs) is not host-only: it is sent to any host whose
    path and scheme match. A cookie extracted without a ``Domain`` attribute
    is host-only and is sent only to the exact host that set it.

    **Parameters:**

    * **cookies** - *(optional)* Initial cookies. Accepts another
      ``CookieStore``, ``Cookies``, ``CookieJar``, ``dict``, or list of pairs.
    * **max_cookies** - *(optional)* Maximum number of stored cookies, or
      ``None`` for no limit.
    * **max_cookies_per_domain** - *(optional)* Maximum number of stored
      cookies for a single domain, or ``None`` for no limit.
    """

    def __init__(
        self,
        cookies: CookieTypes | None = None,
        *,
        max_cookies: int | None = None,
        max_cookies_per_domain: int | None = None,
    ) -> None:
        self.max_cookies = _validate_limit("max_cookies", max_cookies)
        self.max_cookies_per_domain = _validate_limit(
            "max_cookies_per_domain", max_cookies_per_domain
        )
        self._cookies: dict[tuple[str, str, str], _StoredCookie] = {}
        self._created = 0
        if cookies is not None:
            self.update(cookies)

    def extract_cookies(self, response: Response) -> None:
        """
        Load cookies from the response ``Set-Cookie`` headers.
        """
        request = response.request
        host = _normalize_host(request.url.host)
        request_path = request.url.path or "/"
        https = request.url.scheme == "https"
        for header_value in response.headers.get_list("set-cookie"):
            for cookie_string in _split_set_cookie_header(header_value):
                parsed = _parse_set_cookie(cookie_string)
                if parsed is not None:
                    self._store_parsed(
                        parsed,
                        host=host,
                        request_path=request_path,
                        https=https,
                    )

    def set_cookie_header(self, request: Request) -> None:
        """
        Set the ``Cookie`` header on ``request`` for cookies that match it.

        Cookies are ordered by longer path first, then older creation first.
        ``Secure`` cookies are sent only on ``https`` requests.
        """
        matched = self._cookies_for_request(request)
        if not matched:
            return
        request.headers["Cookie"] = "; ".join(
            f"{cookie.name}={cookie.value}" for cookie in matched
        )

    def set(self, name: str, value: str, domain: str = "", path: str = "/") -> None:
        """
        Set a cookie value by name. May optionally include domain and path.

        ``domain=""`` stores a cookie that is sent to any host, subject to
        path and scheme rules. It is not a host-only cookie.
        """
        stored_domain = _normalize_domain_attribute(domain) if domain else ""
        stored_path = path if path else "/"
        self._remember(
            _StoredCookie(
                name=name,
                value=value,
                domain=stored_domain,
                path=stored_path,
                host_only=False,
                secure=False,
                expires=None,
            )
        )

    def get(  # type: ignore[override]
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
        matches = self._matching(name=name, domain=domain, path=path)
        if not matches:
            return default
        if len(matches) > 1:
            message = f"Multiple cookies exist with name={name}"
            raise CookieConflict(message)
        return matches[0].value

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
        for cookie in self._matching(name=name, domain=domain, path=path):
            self._cookies.pop(cookie.key, None)

    def clear(self, domain: str | None = None, path: str | None = None) -> None:
        """
        Delete all cookies. Optionally include a domain and path in
        order to only delete a subset of all the cookies.
        """
        for cookie in self._matching(domain=domain, path=path):
            self._cookies.pop(cookie.key, None)

    def update(self, cookies: CookieTypes | None = None) -> None:  # type: ignore[override]
        """
        Update this store from another cookie collection.

        Accepts a ``CookieStore``, ``httpx.Cookies``, ``CookieJar``,
        ``dict[str, str]``, or ``list[tuple[str, str]]``. Mapping and list
        inputs are stored with ``domain=""`` and are not host-only.
        """
        if cookies is None:
            return
        if isinstance(cookies, CookieStore):
            incoming = [cookie.clone() for cookie in cookies._live_cookies()]
            for cookie in incoming:
                self._remember(cookie)
            return

        from ._models import Cookies as HTTPXCookies

        if isinstance(cookies, HTTPXCookies):
            for stdlib_cookie in list(cookies.jar):
                self._remember_stdlib(stdlib_cookie)
            return
        if isinstance(cookies, CookieJar):
            for stdlib_cookie in list(cookies):
                self._remember_stdlib(stdlib_cookie)
            return
        if isinstance(cookies, Mapping):
            for name, value in cookies.items():
                self.set(name, value)
            return
        if isinstance(cookies, (list, tuple)):
            for name, value in cookies:
                self.set(name, value)
            return
        raise TypeError(
            "CookieStore.update() expected a CookieStore, Cookies, CookieJar, "
            f"dict, or list of pairs; got {type(cookies).__name__}"
        )

    def __setitem__(self, name: str, value: str) -> None:
        self.set(name, value)

    def __getitem__(self, name: str) -> str:
        value = self.get(name)
        if value is None:
            raise KeyError(name)
        return value

    def __delitem__(self, name: str) -> None:
        matches = self._matching(name=name)
        if not matches:
            raise KeyError(name)
        for cookie in matches:
            self._cookies.pop(cookie.key, None)

    def __iter__(self) -> typing.Iterator[str]:
        self._purge_expired()
        for cookie in self._cookies.values():
            yield cookie.name

    def __len__(self) -> int:
        self._purge_expired()
        return len(self._cookies)

    def __contains__(self, name: object) -> bool:
        if not isinstance(name, str):
            return False
        self._purge_expired()
        return any(cookie.name == name for cookie in self._cookies.values())

    def __repr__(self) -> str:
        self._purge_expired()
        cookies_repr = ", ".join(
            f"<Cookie {cookie.name}={cookie.value} for {cookie.domain} />"
            for cookie in self._cookies.values()
        )
        return f"<CookieStore[{cookies_repr}]>"

    def _stdlib_cookies(self) -> list[Cookie]:
        self._purge_expired()
        return [cookie.to_stdlib() for cookie in self._cookies.values()]

    def _live_cookies(self) -> list[_StoredCookie]:
        self._purge_expired()
        return list(self._cookies.values())

    def _remember(self, cookie: _StoredCookie) -> None:
        self._purge_expired()
        if cookie.expires is not None and cookie.expires <= time.time():
            self._cookies.pop(cookie.key, None)
            return
        self._created += 1
        cookie.created = self._created
        # Reinsert so iteration follows creation order, including replacements.
        self._cookies.pop(cookie.key, None)
        self._cookies[cookie.key] = cookie
        self._evict()

    def _remember_stdlib(self, cookie: Cookie) -> None:
        if not cookie.name:
            return
        value = "" if cookie.value is None else cookie.value
        raw_domain = cookie.domain or ""
        if cookie.domain_specified:
            host_only = False
            domain = _normalize_domain_attribute(raw_domain)
        elif raw_domain == "":
            host_only = False
            domain = ""
        else:
            host_only = True
            domain = _normalize_host(raw_domain)
        if cookie.expires is None:
            expires: float | None = None
        else:
            expires = float(cookie.expires)
        self._remember(
            _StoredCookie(
                name=cookie.name,
                value=value,
                domain=domain,
                path=cookie.path or "/",
                host_only=host_only,
                secure=bool(cookie.secure),
                expires=expires,
            )
        )

    def _store_parsed(
        self,
        parsed: _ParsedSetCookie,
        *,
        host: str,
        request_path: str,
        https: bool,
    ) -> None:
        if parsed.domain is None:
            domain = host
            host_only = True
            domain_specified = False
        else:
            domain = _normalize_domain_attribute(parsed.domain)
            if not domain or not _domain_matches(host, domain):
                return
            host_only = False
            domain_specified = True

        # ``__Host-`` requires an explicit ``Path=/`` attribute. An omitted or
        # invalid path uses the default path, which does not satisfy the prefix.
        if parsed.name.startswith("__Secure-") and not (parsed.secure and https):
            return
        if parsed.name.startswith("__Host-") and not (
            parsed.secure and https and not domain_specified and parsed.path == "/"
        ):
            return

        path = parsed.path if parsed.path is not None else _default_path(request_path)

        expires: float | None
        if parsed.has_max_age:
            assert parsed.max_age is not None
            if parsed.max_age <= 0:
                self._cookies.pop((parsed.name, domain, path), None)
                return
            try:
                expires = time.time() + parsed.max_age
            except OverflowError:
                expires = float("inf")
        elif parsed.expires is not None:
            parsed_time = _parse_expires(parsed.expires)
            if parsed_time is None:
                expires = None
            elif parsed_time <= time.time():
                self._cookies.pop((parsed.name, domain, path), None)
                return
            else:
                expires = parsed_time
        else:
            expires = None

        self._remember(
            _StoredCookie(
                name=parsed.name,
                value=parsed.value,
                domain=domain,
                path=path,
                host_only=host_only,
                secure=parsed.secure,
                expires=expires,
            )
        )

    def _cookies_for_request(self, request: Request) -> list[_StoredCookie]:
        self._purge_expired()
        host = _normalize_host(request.url.host)
        path = request.url.path or "/"
        https = request.url.scheme == "https"
        matched: list[_StoredCookie] = []
        for cookie in self._cookies.values():
            if cookie.secure and not https:
                continue
            if not _path_matches(path, cookie.path):
                continue
            if cookie.host_only:
                if host != cookie.domain:
                    continue
            elif cookie.domain == "":
                pass
            elif not _domain_matches(host, cookie.domain):
                continue
            matched.append(cookie)
        matched.sort(key=lambda cookie: (-len(cookie.path), cookie.created))
        return matched

    def _matching(
        self,
        name: str | None = None,
        domain: str | None = None,
        path: str | None = None,
    ) -> list[_StoredCookie]:
        self._purge_expired()
        domain_norm = _normalize_lookup_domain(domain) if domain is not None else None
        matched: list[_StoredCookie] = []
        for cookie in self._cookies.values():
            if name is not None and cookie.name != name:
                continue
            if domain_norm is not None and cookie.domain != domain_norm:
                continue
            if path is not None and cookie.path != path:
                continue
            matched.append(cookie)
        return matched

    def _purge_expired(self) -> None:
        now = time.time()
        expired = [
            key
            for key, cookie in self._cookies.items()
            if cookie.expires is not None and cookie.expires <= now
        ]
        for key in expired:
            del self._cookies[key]

    def _evict(self) -> None:
        per_domain = self.max_cookies_per_domain
        if per_domain is not None:
            grouped: dict[str, list[_StoredCookie]] = {}
            for cookie in self._cookies.values():
                grouped.setdefault(cookie.domain, []).append(cookie)
            for group in grouped.values():
                overflow = len(group) - per_domain
                if overflow <= 0:
                    continue
                group.sort(
                    key=lambda cookie: (cookie.created, cookie.name, cookie.path)
                )
                for cookie in group[:overflow]:
                    self._cookies.pop(cookie.key, None)

        total = self.max_cookies
        if total is None:
            return
        while len(self._cookies) > total:
            oldest = min(
                self._cookies.values(),
                key=lambda cookie: (cookie.created, cookie.name, cookie.path),
            )
            self._cookies.pop(oldest.key, None)
