"""
Deterministic cookie container.

``CookieStore`` implements the cookie storage and sending rules needed for
modern clients: host-only cookies, domain and path matching, the ``Secure``
flag, cookie prefixes, and ``Max-Age`` / ``Expires`` handling. It is opt-in.
Existing ``Cookies`` behavior is unchanged.
"""

from __future__ import annotations

import ipaddress
import re
import time
import typing
from dataclasses import dataclass
from datetime import timezone
from email.utils import parsedate_to_datetime
from http.cookiejar import Cookie, CookieJar

from ._exceptions import CookieConflict
from ._types import CookieTypes
from ._urls import URL

if typing.TYPE_CHECKING:  # pragma: no cover
    from ._models import Request, Response


_COOKIE_PAIR_SPLIT = re.compile(r",(?=[^;,]*?=)")
_MAX_AGE_RE = re.compile(r"[+-]?\d+")


def _validate_limit(value: int | None, name: str) -> int | None:
    if value is None:
        return None
    # ``bool`` is a subclass of ``int`` but is not a cookie limit.
    if isinstance(value, bool) or not isinstance(value, int):
        raise TypeError(f"{name} must be an int or None")
    if value < 0:
        raise ValueError(f"{name} must be >= 0")
    return value


def _normalize_domain(domain: str) -> str:
    """Lower-case a domain and drop a single leading dot."""
    normalized = domain.strip().lower()
    if normalized.startswith("."):
        normalized = normalized[1:]
    return normalized.rstrip(".")


def _normalize_host(host: str) -> str:
    return host.strip().lower().rstrip(".")


def _is_ip_address(host: str) -> bool:
    try:
        ipaddress.ip_address(host)
    except ValueError:
        return False
    return True


def _domain_matches(host: str, domain: str) -> bool:
    """Return True if ``host`` domain-matches ``domain`` (RFC 6265)."""
    host_name = _normalize_host(host)
    domain_name = _normalize_domain(domain)
    if not host_name or not domain_name:
        return False
    if host_name == domain_name:
        return True
    # Suffix matching does not apply to IP addresses.
    if _is_ip_address(host_name):
        return False
    return host_name.endswith("." + domain_name)


def _default_path(request_path: str) -> str:
    """Default cookie path for a request path (RFC 6265)."""
    if not request_path or not request_path.startswith("/"):
        return "/"
    slash = request_path.rfind("/")
    if slash <= 0:
        return "/"
    return request_path[:slash]


def _path_matches(cookie_path: str, request_path: str) -> bool:
    """Return True if ``request_path`` path-matches ``cookie_path``."""
    if request_path == cookie_path:
        return True
    if not cookie_path or not request_path.startswith(cookie_path):
        return False
    if cookie_path.endswith("/"):
        return True
    remainder = request_path[len(cookie_path) : len(cookie_path) + 1]
    return remainder == "/"


def _unquote(value: str) -> str:
    if len(value) >= 2 and value.startswith('"') and value.endswith('"'):
        return value[1:-1]
    return value


def _parse_expires(value: str) -> float | None:
    """Parse an ``Expires`` value into a UTC timestamp, or None if invalid."""
    try:
        parsed = parsedate_to_datetime(value)
    except (TypeError, ValueError, IndexError, OverflowError):
        return None
    if parsed is None:
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    try:
        return parsed.timestamp()
    except (OverflowError, OSError, ValueError):
        return None


def _split_set_cookie_header(header_value: str) -> list[str]:
    """
    Split a ``Set-Cookie`` header that may contain several cookies.

    Commas inside an ``Expires`` attribute are not separators. A comma starts
    a new cookie only when the following segment looks like ``name=value``.
    """
    parts: list[str] = []
    start = 0
    for match in _COOKIE_PAIR_SPLIT.finditer(header_value):
        parts.append(header_value[start : match.start()])
        start = match.end()
    parts.append(header_value[start:])
    return [part.strip() for part in parts if part.strip()]


@dataclass
class _StoredCookie:
    name: str
    value: str
    domain: str
    path: str
    secure: bool
    expires_at: float | None
    host_only: bool
    any_host: bool
    creation_order: int


@dataclass
class _ParsedSetCookie:
    name: str
    value: str
    domain: str | None
    path: str | None
    secure: bool
    expires_at: float | None
    delete: bool


def _parse_set_cookie(cookie_string: str, now: float) -> _ParsedSetCookie | None:
    """
    Parse one cookie string.

    Returns None when the cookie is empty, malformed, or when ``Domain``,
    ``Max-Age``, or ``Expires`` is present without a value. Unknown attributes
    are ignored. An invalid ``Expires`` date does not reject the cookie.
    """
    parts = [part.strip() for part in cookie_string.split(";")]
    if not parts or not parts[0] or "=" not in parts[0]:
        return None

    name, value = parts[0].split("=", 1)
    name = name.strip()
    value = _unquote(value.strip())
    if not name or any(char.isspace() or char in ",;" for char in name):
        return None

    domain: str | None = None
    path: str | None = None
    path_specified = False
    secure = False
    has_max_age = False
    max_age: int | None = None
    has_expires = False
    expires_at: float | None = None

    for attribute in parts[1:]:
        if not attribute:
            continue
        if "=" in attribute:
            attr_name, attr_value = attribute.split("=", 1)
            attr_name = attr_name.strip().lower()
            attr_value = _unquote(attr_value.strip())
        else:
            attr_name = attribute.strip().lower()
            attr_value = None

        if not attr_name:
            continue
        if attr_name == "domain":
            if not attr_value:
                return None
            domain = attr_value
        elif attr_name == "max-age":
            if not attr_value or _MAX_AGE_RE.fullmatch(attr_value) is None:
                return None
            has_max_age = True
            max_age = int(attr_value)
        elif attr_name == "expires":
            if not attr_value:
                return None
            has_expires = True
            expires_at = _parse_expires(attr_value)
        elif attr_name == "path":
            path_specified = True
            path = attr_value if attr_value is not None else ""
        elif attr_name == "secure":
            secure = True
        # Unknown attributes, including HttpOnly and SameSite, are ignored.

    delete = False
    stored_expires: float | None = None
    if has_max_age:
        assert max_age is not None
        if max_age <= 0:
            delete = True
        else:
            stored_expires = now + max_age
    elif has_expires and expires_at is not None:
        if expires_at <= now:
            delete = True
        else:
            stored_expires = expires_at

    if path_specified and path is not None and path.startswith("/"):
        cookie_path: str | None = path
    else:
        cookie_path = None

    return _ParsedSetCookie(
        name=name,
        value=value,
        domain=domain,
        path=cookie_path,
        secure=secure,
        expires_at=stored_expires,
        delete=delete,
    )


class CookieStore(typing.MutableMapping[str, str]):
    """
    A mutable mapping of cookie names to values with deterministic storage.

    Cookies extracted from responses keep the domain, path, and secure scope
    required to build the correct ``Cookie`` header on later requests.

    ``max_cookies`` and ``max_cookies_per_domain`` limit how many cookies are
    retained. When a limit is exceeded, the oldest cookies (by creation order)
    are evicted. Per-domain eviction runs before the global limit. Replacing
    a cookie with the same name, domain, and path counts as a new creation.

    Args:
        cookies: Optional initial cookies. Accepts another ``CookieStore``,
            ``Cookies``, ``CookieJar``, ``dict[str, str]``, or a list of
            ``(name, value)`` pairs.
        max_cookies: Maximum cookies in the store, or None for no limit.
        max_cookies_per_domain: Maximum cookies for a single domain, or None.
    """

    def __init__(
        self,
        cookies: CookieTypes | None = None,
        *,
        max_cookies: int | None = None,
        max_cookies_per_domain: int | None = None,
    ) -> None:
        self.max_cookies = _validate_limit(max_cookies, "max_cookies")
        self.max_cookies_per_domain = _validate_limit(
            max_cookies_per_domain, "max_cookies_per_domain"
        )
        self._cookies: list[_StoredCookie] = []
        self._creation_order = 0
        if cookies is not None:
            self.update(cookies)

    def extract_cookies(self, response: Response) -> None:
        """
        Store cookies from the response ``Set-Cookie`` headers.

        Several cookies may be combined into one header value. A comma inside
        an ``Expires`` date is not treated as a separator.
        """
        request_url = response.request.url
        now = time.time()
        for header_value in response.headers.get_list("set-cookie"):
            for cookie_string in _split_set_cookie_header(header_value):
                parsed = _parse_set_cookie(cookie_string, now)
                if parsed is not None:
                    self._store_parsed(parsed, request_url)

    def set_cookie_header(self, request: Request) -> None:
        """Set the ``Cookie`` header for cookies that match ``request``."""
        matched = [
            cookie
            for cookie in self._active(time.time())
            if self._cookie_matches(cookie, request.url)
        ]
        if not matched:
            return
        matched.sort(key=lambda cookie: (-len(cookie.path), cookie.creation_order))
        request.headers["Cookie"] = "; ".join(
            f"{cookie.name}={cookie.value}" for cookie in matched
        )

    def set(self, name: str, value: str, domain: str = "", path: str = "/") -> None:
        """
        Set a cookie by name.

        ``domain=""`` is not host-only: the cookie is sent to any host whose
        path and scheme match. A non-empty domain is sent to that domain and
        its subdomains.
        """
        if domain:
            stored_domain = _normalize_domain(domain)
            any_host = not stored_domain
            host_only = False
        else:
            stored_domain = ""
            any_host = True
            host_only = False
        if not path:
            path = "/"
        self._remember(
            _StoredCookie(
                name=name,
                value=value,
                domain=stored_domain,
                path=path,
                secure=False,
                expires_at=None,
                host_only=host_only,
                any_host=any_host,
                creation_order=0,
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
        Return a cookie value by name.

        ``domain`` and ``path`` select a single cookie when several share a
        name. If more than one cookie still matches, raise ``CookieConflict``.
        """
        matches = self._select(name, domain, path)
        if not matches:
            return default
        if len(matches) > 1:
            raise CookieConflict(f"Multiple cookies exist with name={name}")
        return matches[0].value

    def delete(
        self,
        name: str,
        domain: str | None = None,
        path: str | None = None,
    ) -> None:
        """Delete cookies matching ``name`` and the optional domain and path."""
        domain_key = None if domain is None else _normalize_domain(domain)
        self._cookies = [
            cookie
            for cookie in self._active(time.time())
            if not (
                cookie.name == name
                and (domain_key is None or cookie.domain == domain_key)
                and (path is None or cookie.path == path)
            )
        ]

    def clear(self, domain: str | None = None, path: str | None = None) -> None:
        """
        Delete cookies.

        ``domain`` and ``path`` restrict the deletion. ``path`` requires
        ``domain``.
        """
        if path is not None:
            assert domain is not None
        if domain is None:
            self._cookies = []
            return
        domain_key = _normalize_domain(domain)
        self._cookies = [
            cookie
            for cookie in self._cookies
            if not (
                cookie.domain == domain_key and (path is None or cookie.path == path)
            )
        ]

    def update(self, cookies: CookieTypes | None = None) -> None:  # type: ignore[override]
        """
        Merge cookies into the store.

        Accepts another ``CookieStore``, ``httpx.Cookies``,
        ``http.cookiejar.CookieJar``, ``dict[str, str]``, or
        ``list[tuple[str, str]]``. Mapping and list inputs are stored with
        ``domain=""`` and are not host-only.
        """
        if cookies is None:
            return
        if isinstance(cookies, CookieStore):
            incoming = sorted(
                cookies._cookies, key=lambda cookie: cookie.creation_order
            )
            now = time.time()
            for stored in incoming:
                if stored.expires_at is not None and stored.expires_at <= now:
                    self._remove_exact(stored.name, stored.domain, stored.path)
                    continue
                self._remember(
                    _StoredCookie(
                        name=stored.name,
                        value=stored.value,
                        domain=stored.domain,
                        path=stored.path,
                        secure=stored.secure,
                        expires_at=stored.expires_at,
                        host_only=stored.host_only,
                        any_host=stored.any_host,
                        creation_order=0,
                    )
                )
            return
        if isinstance(cookies, CookieJar):
            for stdlib_cookie in cookies:
                self._remember_stdlib(stdlib_cookie)
            return

        from ._models import Cookies

        if isinstance(cookies, Cookies):
            for stdlib_cookie in cookies.jar:
                self._remember_stdlib(stdlib_cookie)
            return
        if isinstance(cookies, typing.Mapping):
            for name, value in cookies.items():
                self.set(name, value)
            return
        if isinstance(cookies, (list, tuple)):
            for name, value in cookies:
                self.set(name, value)
            return
        raise TypeError(
            "CookieStore.update() expected a CookieStore, Cookies, CookieJar, "
            f"dict, or list of pairs, got {type(cookies).__name__}"
        )

    def __setitem__(self, name: str, value: str) -> None:
        self.set(name, value)

    def __getitem__(self, name: str) -> str:
        value = self.get(name)
        if value is None:
            raise KeyError(name)
        return value

    def __delitem__(self, name: str) -> None:
        self.delete(name)

    def __iter__(self) -> typing.Iterator[str]:
        return (cookie.name for cookie in self._active(time.time()))

    def __len__(self) -> int:
        return len(self._active(time.time()))

    def __bool__(self) -> bool:
        return bool(self._active(time.time()))

    def __contains__(self, name: object) -> bool:
        if not isinstance(name, str):
            return False
        return any(cookie.name == name for cookie in self._active(time.time()))

    def __repr__(self) -> str:
        rendered = ", ".join(
            (
                f"<Cookie {cookie.name}={cookie.value} "
                f"for {cookie.domain or '*'}{cookie.path} />"
            )
            for cookie in self._active(time.time())
        )
        return f"<CookieStore[{rendered}]>"

    def _for_stdlib(self) -> list[tuple[str, str, str, str]]:
        """Name, value, domain, and path tuples for the legacy cookie jar."""
        exported: list[tuple[str, str, str, str]] = []
        for cookie in self._active(time.time()):
            domain = "" if cookie.any_host else cookie.domain
            exported.append((cookie.name, cookie.value, domain, cookie.path))
        return exported

    def _store_parsed(self, parsed: _ParsedSetCookie, request_url: URL) -> None:
        host = _normalize_host(request_url.host)
        if parsed.domain is not None:
            domain = _normalize_domain(parsed.domain)
            if not domain or not _domain_matches(host, domain):
                return
            host_only = False
            any_host = False
        else:
            domain = host
            host_only = True
            any_host = False

        if parsed.path is not None:
            path = parsed.path
        else:
            path = _default_path(request_url.path)
        secure_origin = request_url.scheme == "https"
        if parsed.name.startswith("__Host-"):
            if not (
                parsed.secure
                and secure_origin
                and parsed.domain is None
                and path == "/"
            ):
                return
        elif parsed.name.startswith("__Secure-") and not (
            parsed.secure and secure_origin
        ):
            return

        if parsed.delete:
            self._remove_exact(parsed.name, domain, path)
            return

        self._remember(
            _StoredCookie(
                name=parsed.name,
                value=parsed.value,
                domain=domain,
                path=path,
                secure=parsed.secure,
                expires_at=parsed.expires_at,
                host_only=host_only,
                any_host=any_host,
                creation_order=0,
            )
        )

    def _remember_stdlib(self, cookie: Cookie) -> None:
        if not cookie.name:
            return
        domain = cookie.domain or ""
        path = cookie.path or "/"
        expires_at = cookie.expires
        if expires_at is not None and expires_at <= time.time():
            if cookie.domain_specified:
                stored_domain = _normalize_domain(domain)
            elif domain:
                stored_domain = _normalize_host(domain)
            else:
                stored_domain = ""
            self._remove_exact(cookie.name, stored_domain, path)
            return

        if cookie.domain_specified:
            stored_domain = _normalize_domain(domain)
            host_only = False
            any_host = not stored_domain
        elif domain:
            stored_domain = _normalize_host(domain)
            host_only = True
            any_host = False
        else:
            stored_domain = ""
            host_only = False
            any_host = True

        self._remember(
            _StoredCookie(
                name=cookie.name,
                value="" if cookie.value is None else cookie.value,
                domain=stored_domain,
                path=path,
                secure=bool(cookie.secure),
                expires_at=float(expires_at) if expires_at is not None else None,
                host_only=host_only,
                any_host=any_host,
                creation_order=0,
            )
        )

    def _remember(self, cookie: _StoredCookie) -> None:
        now = time.time()
        self._purge(now)
        self._cookies = [
            existing for existing in self._cookies if not _same_cookie(existing, cookie)
        ]
        self._creation_order += 1
        cookie.creation_order = self._creation_order
        self._cookies.append(cookie)
        self._evict(cookie.domain)

    def _remove_exact(self, name: str, domain: str, path: str) -> None:
        self._cookies = [
            cookie
            for cookie in self._cookies
            if not (
                cookie.name == name and cookie.domain == domain and cookie.path == path
            )
        ]

    def _evict(self, domain: str) -> None:
        per_domain = self.max_cookies_per_domain
        if per_domain is not None:
            self._evict_matching(lambda cookie: cookie.domain == domain, per_domain)
        total = self.max_cookies
        if total is not None:
            self._evict_matching(lambda cookie: True, total)

    def _evict_matching(
        self,
        predicate: typing.Callable[[_StoredCookie], bool],
        limit: int,
    ) -> None:
        while True:
            matched = [cookie for cookie in self._cookies if predicate(cookie)]
            if len(matched) <= limit:
                return
            oldest = min(matched, key=lambda cookie: cookie.creation_order)
            self._cookies = [cookie for cookie in self._cookies if cookie is not oldest]

    def _purge(self, now: float) -> None:
        self._cookies = [
            cookie
            for cookie in self._cookies
            if cookie.expires_at is None or cookie.expires_at > now
        ]

    def _active(self, now: float) -> list[_StoredCookie]:
        self._purge(now)
        return sorted(self._cookies, key=lambda cookie: cookie.creation_order)

    def _select(
        self,
        name: str,
        domain: str | None,
        path: str | None,
    ) -> list[_StoredCookie]:
        domain_key = None if domain is None else _normalize_domain(domain)
        return [
            cookie
            for cookie in self._active(time.time())
            if cookie.name == name
            and (domain_key is None or cookie.domain == domain_key)
            and (path is None or cookie.path == path)
        ]

    def _cookie_matches(self, cookie: _StoredCookie, url: URL) -> bool:
        if cookie.secure and url.scheme != "https":
            return False
        if not _path_matches(cookie.path, url.path):
            return False
        if cookie.any_host:
            return True
        host = _normalize_host(url.host)
        if cookie.host_only:
            return host == cookie.domain
        return _domain_matches(host, cookie.domain)


def _same_cookie(left: _StoredCookie, right: _StoredCookie) -> bool:
    return (
        left.name == right.name
        and left.domain == right.domain
        and left.path == right.path
    )
