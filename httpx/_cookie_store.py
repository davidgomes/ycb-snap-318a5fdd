"""
Deterministic cookie container.

`CookieStore` follows the cookie matching rules callers need for modern
clients: host-only cookies, domain and path matching, the `Secure` flag,
`__Host-` / `__Secure-` prefixes, and `Max-Age` / `Expires` handling.
"""

from __future__ import annotations

import time
import typing
from datetime import timezone
from email.utils import parsedate_to_datetime
from http.cookiejar import Cookie, CookieJar

from ._exceptions import CookieConflict
from ._types import CookieTypes

if typing.TYPE_CHECKING:
    from ._models import Request, Response

_VALUELESS_ATTRS = frozenset({"domain", "max-age", "expires"})


class _StoredCookie:
    def __init__(
        self,
        *,
        name: str,
        value: str,
        domain: str,
        host_only: bool,
        path: str,
        secure: bool,
        expires: float | None,
        created: int,
    ) -> None:
        self.name = name
        self.value = value
        self.domain = domain
        self.host_only = host_only
        self.path = path
        self.secure = secure
        self.expires = expires
        self.created = created

    def clone(self) -> _StoredCookie:
        return _StoredCookie(
            name=self.name,
            value=self.value,
            domain=self.domain,
            host_only=self.host_only,
            path=self.path,
            secure=self.secure,
            expires=self.expires,
            created=self.created,
        )

    def expired(self, now: float) -> bool:
        return self.expires is not None and self.expires <= now


def _check_limit(name: str, value: int | None) -> None:
    if value is None:
        return
    if isinstance(value, bool) or not isinstance(value, int):
        raise TypeError(f"{name} must be an int or None")
    if value < 0:
        raise ValueError(f"{name} must be non-negative")


def _normalize_domain(domain: str) -> str:
    normalized = domain.strip().lower()
    if normalized.endswith("."):
        normalized = normalized[:-1]
    while normalized.startswith("."):
        normalized = normalized[1:]
    return normalized


def _is_ip_address(host: str) -> bool:
    if ":" in host:
        return True
    parts = host.split(".")
    if len(parts) != 4:
        return False
    for part in parts:
        if not part.isdigit():
            return False
        number = int(part)
        if number > 255:
            return False
    return True


def _domain_matches(host: str, cookie_domain: str) -> bool:
    """Return whether `host` domain-matches `cookie_domain` (RFC 6265)."""
    host = _normalize_domain(host)
    cookie_domain = _normalize_domain(cookie_domain)
    if not host or not cookie_domain:
        return False
    if host == cookie_domain:
        return True
    if _is_ip_address(host):
        return False
    return host.endswith("." + cookie_domain)


def _default_cookie_path(request_path: str) -> str:
    if not request_path or not request_path.startswith("/"):
        return "/"
    slash = request_path.rfind("/")
    if slash <= 0:
        return "/"
    return request_path[:slash]


def _path_matches(request_path: str, cookie_path: str) -> bool:
    if not request_path.startswith("/"):
        request_path = "/"
    if request_path == cookie_path:
        return True
    if not request_path.startswith(cookie_path):
        return False
    if cookie_path.endswith("/"):
        return True
    remainder = request_path[len(cookie_path) :]
    return remainder.startswith("/")


def _split_set_cookie_header(header: str) -> list[str]:
    """
    Split a `Set-Cookie` value that may contain several cookies.

    Commas inside `Expires` dates are not separators. A comma starts a new
    cookie only when the following token looks like `name=value`.
    """
    cookies: list[str] = []
    pos = 0
    length = len(header)
    while pos < length:
        start = pos
        while True:
            while pos < length and header[pos].isspace():
                pos += 1
            if pos >= length:
                break
            if header[pos] != ",":
                pos += 1
                continue
            last_comma = pos
            pos += 1
            while pos < length and header[pos].isspace():
                pos += 1
            next_start = pos
            while pos < length and header[pos] not in "=;,":
                pos += 1
            if pos < length and header[pos] == "=":
                cookies.append(header[start:last_comma])
                pos = next_start
                start = pos
            else:
                pos = last_comma + 1
        tail = header[start:]
        if tail.strip():
            cookies.append(tail)
        break
    return [part.strip() for part in cookies if part.strip()]


def _parse_expires(value: str) -> float | None:
    """Return a UTC timestamp, or None when the date is invalid."""
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


def _parse_max_age(value: str) -> int | None:
    """
    Parse a Max-Age attribute.

    Returns the integer age, or None when the value is not a valid age
    (the whole cookie must be ignored).
    """
    if not value:
        return None
    first = value[0]
    digits = value[1:] if first == "-" else value
    if first == "-":
        if not digits or not digits.isdigit():
            return None
    elif not value.isdigit():
        return None
    try:
        return int(value)
    except ValueError:
        return None


class _ParsedSetCookie(typing.NamedTuple):
    name: str
    value: str
    domain: str | None
    path: str
    secure: bool
    expires: float | None
    delete: bool
    host_prefix_path_ok: bool
    domain_attribute: bool


def _parse_set_cookie(cookie_string: str, request_path: str) -> _ParsedSetCookie | None:
    cookie_string = cookie_string.strip()
    if not cookie_string or "=" not in cookie_string:
        return None

    parts = [part.strip() for part in cookie_string.split(";")]
    name_value = parts[0]
    if "=" not in name_value:
        return None
    name, value = name_value.split("=", 1)
    name = name.strip()
    value = value.strip()
    if not name:
        return None

    secure = False
    domain: str | None = None
    domain_attribute = False
    path: str | None = None
    path_specified = False
    max_age: int | None = None
    max_age_specified = False
    expires_at: float | None = None
    expires_specified = False
    expires_valid = False

    for part in parts[1:]:
        if not part:
            continue
        if "=" in part:
            attr_name, attr_value = part.split("=", 1)
            attr_name = attr_name.strip().lower()
            attr_value = attr_value.strip()
            if len(attr_value) >= 2 and attr_value[0] == attr_value[-1] == '"':
                attr_value = attr_value[1:-1]
        else:
            attr_name = part.strip().lower()
            attr_value = None

        if attr_name in _VALUELESS_ATTRS and not attr_value:
            return None
        if attr_name == "secure":
            secure = True
        elif attr_name == "domain" and attr_value is not None:
            domain_attribute = True
            domain = attr_value
        elif attr_name == "path" and attr_value is not None:
            path_specified = True
            path = attr_value
        elif attr_name == "max-age" and attr_value is not None:
            max_age_specified = True
            max_age = _parse_max_age(attr_value)
            if max_age is None:
                return None
        elif attr_name == "expires" and attr_value is not None:
            expires_specified = True
            expires_at = _parse_expires(attr_value)
            expires_valid = expires_at is not None

    if path is None or not path.startswith("/"):
        cookie_path = _default_cookie_path(request_path)
        explicit_root_path = False
    else:
        cookie_path = path
        explicit_root_path = path_specified and path == "/"

    now = time.time()
    delete = False
    expires: float | None = None
    if max_age_specified:
        assert max_age is not None
        if max_age <= 0:
            delete = True
        else:
            expires = now + max_age
    elif expires_specified and expires_valid:
        assert expires_at is not None
        if expires_at <= now:
            delete = True
        else:
            expires = expires_at

    stored_domain = _normalize_domain(domain) if domain_attribute and domain else None
    if domain_attribute and not stored_domain:
        return None

    return _ParsedSetCookie(
        name=name,
        value=value,
        domain=stored_domain,
        path=cookie_path,
        secure=secure,
        expires=expires,
        delete=delete,
        host_prefix_path_ok=explicit_root_path,
        domain_attribute=domain_attribute,
    )


class CookieStore(typing.MutableMapping[str, str]):
    """
    A deterministic cookie store.

    Use this anywhere `cookies=` is accepted. Unlike `Cookies`, domain, path,
    secure, and prefix rules are applied when cookies are stored and sent.
    """

    def __init__(
        self,
        cookies: CookieTypes | None = None,
        *,
        max_cookies: int | None = None,
        max_cookies_per_domain: int | None = None,
    ) -> None:
        _check_limit("max_cookies", max_cookies)
        _check_limit("max_cookies_per_domain", max_cookies_per_domain)
        self.max_cookies = max_cookies
        self.max_cookies_per_domain = max_cookies_per_domain
        self._cookies: list[_StoredCookie] = []
        self._next_created = 0
        if cookies is not None:
            self.update(cookies)

    def extract_cookies(self, response: Response) -> None:
        """
        Load cookies from the response `Set-Cookie` headers.
        """
        request = response.request
        url = request.url
        host = url.host
        path = url.path or "/"
        https = url.scheme == "https"
        for header_value in response.headers.get_list("set-cookie"):
            for cookie_string in _split_set_cookie_header(header_value):
                self._store_set_cookie(
                    cookie_string,
                    host=host,
                    path=path,
                    https=https,
                )

    def set_cookie_header(self, request: Request) -> None:
        """
        Set the `Cookie` header on `request` for cookies that match its URL.
        """
        self._purge_expired()
        url = request.url
        host = url.host
        path = url.path or "/"
        https = url.scheme == "https"
        matching = [
            cookie
            for cookie in self._cookies
            if self._cookie_matches(cookie, host=host, path=path, https=https)
        ]
        if not matching:
            return
        matching.sort(key=lambda cookie: (-len(cookie.path), cookie.created))
        request.headers["Cookie"] = "; ".join(
            f"{cookie.name}={cookie.value}" for cookie in matching
        )

    def set(self, name: str, value: str, domain: str = "", path: str = "/") -> None:
        """
        Set a cookie value by name. May optionally include domain and path.

        `domain=""` stores a cookie that is sent to any host (it is not
        host-only). Host-only cookies are created only when a response omits
        the `Domain` attribute.
        """
        cookie_path = path if path else "/"
        if domain:
            stored_domain = _normalize_domain(domain)
            host_only = False
        else:
            stored_domain = ""
            host_only = False
        self._add(
            _StoredCookie(
                name=name,
                value=value,
                domain=stored_domain,
                host_only=host_only,
                path=cookie_path,
                secure=False,
                expires=None,
                created=0,
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
        matches = self._select(name, domain=domain, path=path)
        if len(matches) > 1:
            raise CookieConflict(f"Multiple cookies exist with name={name}")
        if not matches:
            return default
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
        self._purge_expired()
        self._cookies = [
            cookie
            for cookie in self._cookies
            if not self._lookup_match(cookie, name, domain=domain, path=path)
        ]

    def clear(self, domain: str | None = None, path: str | None = None) -> None:
        """
        Delete all cookies. Optionally include a domain and path in
        order to only delete a subset of all the cookies.
        """
        if domain is None and path is None:
            self._cookies = []
            return
        if path is not None:
            assert domain is not None
        normalized = _normalize_domain(domain) if domain is not None else None
        self._cookies = [
            cookie
            for cookie in self._cookies
            if not (
                (normalized is None or cookie.domain == normalized)
                and (path is None or cookie.path == path)
            )
        ]

    def update(self, cookies: CookieTypes | None = None) -> None:  # type: ignore[override]
        """
        Update this store from another cookie container or mapping.
        """
        if cookies is None:
            return
        if isinstance(cookies, CookieStore):
            for stored in list(cookies._cookies):
                if stored.expired(time.time()):
                    self._remove(stored.name, stored.domain, stored.path)
                    continue
                self._add(stored.clone())
            return
        from ._models import Cookies

        if isinstance(cookies, Cookies):
            for morsel in cookies.jar:
                self._add_stdlib_cookie(morsel)
            return
        if isinstance(cookies, CookieJar):
            for morsel in cookies:
                self._add_stdlib_cookie(morsel)
            return
        if isinstance(cookies, dict):
            for name, value in cookies.items():
                self.set(name, value)
            return
        if isinstance(cookies, list):
            for name, value in cookies:
                self.set(name, value)
            return
        raise TypeError(
            "cookies must be a CookieStore, Cookies, CookieJar, dict, or list"
        )

    def copy(self) -> CookieStore:
        clone = CookieStore(
            max_cookies=self.max_cookies,
            max_cookies_per_domain=self.max_cookies_per_domain,
        )
        now = time.time()
        clone._cookies = [
            cookie.clone() for cookie in self._cookies if not cookie.expired(now)
        ]
        clone._next_created = self._next_created
        return clone

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
        self._purge_expired()
        return (cookie.name for cookie in self._cookies)

    def __len__(self) -> int:
        self._purge_expired()
        return len(self._cookies)

    def __repr__(self) -> str:
        self._purge_expired()
        cookies_repr = ", ".join(
            f"<Cookie {cookie.name}={cookie.value} for {cookie.domain or '*'}"
            f"{' (host-only)' if cookie.host_only else ''} />"
            for cookie in self._cookies
        )
        return f"<CookieStore[{cookies_repr}]>"

    def _store_set_cookie(
        self,
        cookie_string: str,
        *,
        host: str,
        path: str,
        https: bool,
    ) -> None:
        parsed = _parse_set_cookie(cookie_string, path)
        if parsed is None:
            return
        if parsed.domain_attribute:
            assert parsed.domain is not None
            if not _domain_matches(host, parsed.domain):
                return
            stored_domain = parsed.domain
            host_only = False
        else:
            stored_domain = _normalize_domain(host)
            host_only = True

        if not self._prefix_allowed(parsed, https=https):
            return

        if parsed.delete:
            self._remove(parsed.name, stored_domain, parsed.path)
            return

        self._add(
            _StoredCookie(
                name=parsed.name,
                value=parsed.value,
                domain=stored_domain,
                host_only=host_only,
                path=parsed.path,
                secure=parsed.secure,
                expires=parsed.expires,
                created=0,
            )
        )

    def _prefix_allowed(self, parsed: _ParsedSetCookie, *, https: bool) -> bool:
        name = parsed.name
        if name.startswith("__Host-"):
            return (
                parsed.secure
                and https
                and not parsed.domain_attribute
                and parsed.host_prefix_path_ok
            )
        if name.startswith("__Secure-"):
            return parsed.secure and https
        return True

    def _add(self, cookie: _StoredCookie) -> None:
        self._purge_expired()
        self._remove(cookie.name, cookie.domain, cookie.path)
        cookie.created = self._allocate_created()
        self._cookies.append(cookie)
        self._evict_domain(cookie.domain)
        self._evict_global()

    def _add_stdlib_cookie(self, cookie: Cookie) -> None:
        if not cookie.name:
            return
        value = "" if cookie.value is None else cookie.value
        path = cookie.path or "/"
        secure = bool(cookie.secure)
        raw_domain = cookie.domain or ""
        if cookie.domain_specified and raw_domain:
            domain = _normalize_domain(raw_domain)
            host_only = False
        elif raw_domain == "":
            domain = ""
            host_only = False
        else:
            domain = _normalize_domain(raw_domain)
            host_only = True
        expires = None if cookie.expires is None else float(cookie.expires)
        if expires is not None and expires <= time.time():
            self._remove(cookie.name, domain, path)
            return
        self._add(
            _StoredCookie(
                name=cookie.name,
                value=value,
                domain=domain,
                host_only=host_only,
                path=path,
                secure=secure,
                expires=expires,
                created=0,
            )
        )

    def _allocate_created(self) -> int:
        created = self._next_created
        self._next_created += 1
        return created

    def _remove(self, name: str, domain: str, path: str) -> None:
        self._cookies = [
            cookie
            for cookie in self._cookies
            if not (
                cookie.name == name and cookie.domain == domain and cookie.path == path
            )
        ]

    def _purge_expired(self) -> None:
        now = time.time()
        self._cookies = [cookie for cookie in self._cookies if not cookie.expired(now)]

    def _evict_domain(self, domain: str) -> None:
        limit = self.max_cookies_per_domain
        if limit is None:
            return
        while True:
            in_domain = [cookie for cookie in self._cookies if cookie.domain == domain]
            if len(in_domain) <= limit:
                return
            oldest = min(in_domain, key=lambda cookie: cookie.created)
            self._cookies.remove(oldest)

    def _evict_global(self) -> None:
        limit = self.max_cookies
        if limit is None:
            return
        while len(self._cookies) > limit:
            oldest = min(self._cookies, key=lambda cookie: cookie.created)
            self._cookies.remove(oldest)

    def _cookie_matches(
        self,
        cookie: _StoredCookie,
        *,
        host: str,
        path: str,
        https: bool,
    ) -> bool:
        if cookie.secure and not https:
            return False
        if not _path_matches(path, cookie.path):
            return False
        if cookie.host_only:
            return _normalize_domain(host) == cookie.domain
        if cookie.domain == "":
            return True
        return _domain_matches(host, cookie.domain)

    def _select(
        self,
        name: str,
        *,
        domain: str | None,
        path: str | None,
    ) -> list[_StoredCookie]:
        self._purge_expired()
        return [
            cookie
            for cookie in self._cookies
            if self._lookup_match(cookie, name, domain=domain, path=path)
        ]

    def _lookup_match(
        self,
        cookie: _StoredCookie,
        name: str,
        *,
        domain: str | None,
        path: str | None,
    ) -> bool:
        if cookie.name != name:
            return False
        if domain is not None and cookie.domain != _normalize_domain(domain):
            return False
        if path is not None and cookie.path != path:
            return False
        return True
