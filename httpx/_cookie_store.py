"""Deterministic cookie container with explicit matching and expiry rules."""

from __future__ import annotations

import email.utils
import re
import time
import typing
from dataclasses import dataclass
from http.cookiejar import CookieJar

from ._exceptions import CookieConflict

if typing.TYPE_CHECKING:  # pragma: no cover
    from http.cookiejar import Cookie

    from ._models import Request, Response

# A comma starts another cookie when it is followed by a cookie-name "=".
# Commas inside Expires dates (for example "Wed, 21 Oct 2015 ...") do not.
_COMBINED_COOKIE_RE = re.compile(r",(?=\s*[!#$%&'*+\-.0-9A-Z^_`a-z|~]+=)")


def _validate_limit(name: str, value: int | None) -> None:
    if value is None:
        return
    if type(value) is not int:
        raise TypeError(f"{name} must be an int or None, got {type(value).__name__}")
    if value < 0:
        raise ValueError(f"{name} must be >= 0")


def _default_path(request_path: str) -> str:
    if not request_path or not request_path.startswith("/"):
        return "/"
    slash = request_path.rfind("/")
    if slash <= 0:
        return "/"
    return request_path[:slash]


def _domain_matches(host: str, domain: str, host_only: bool) -> bool:
    host = host.lower().rstrip(".")
    if not host_only and domain == "":
        return True
    domain = domain.lower().rstrip(".")
    if host == domain:
        return True
    if host_only or not domain:
        return False
    return host.endswith("." + domain)


def _path_matches(request_path: str, cookie_path: str) -> bool:
    if request_path == cookie_path:
        return True
    prefix = cookie_path if cookie_path.endswith("/") else cookie_path + "/"
    return request_path.startswith(prefix)


def _parse_expires(value: str) -> float | None:
    try:
        parsed = email.utils.parsedate_to_datetime(value)
    except (TypeError, ValueError, IndexError, OverflowError):
        return None
    if parsed is None:
        return None
    return parsed.timestamp()


@dataclass
class _ParsedCookie:
    delete: bool
    cookie: _StoredCookie


@dataclass
class _StoredCookie:
    name: str
    value: str
    domain: str
    path: str
    host_only: bool
    secure: bool
    expires: float | None
    created: int


class CookieStore(typing.MutableMapping[str, str]):
    """
    A cookie container with deterministic storage, matching, and eviction.

    Cookies added with `set()` or via mapping and list inputs use an empty
    domain and are sent to any host that matches path and scheme rules.
    Cookies extracted from `Set-Cookie` follow host-only, domain, path,
    secure, prefix, and expiry rules.
    """

    def __init__(
        self,
        cookies: typing.Any = None,
        *,
        max_cookies: int | None = None,
        max_cookies_per_domain: int | None = None,
    ) -> None:
        _validate_limit("max_cookies", max_cookies)
        _validate_limit("max_cookies_per_domain", max_cookies_per_domain)
        self.max_cookies = max_cookies
        self.max_cookies_per_domain = max_cookies_per_domain
        self._cookies: list[_StoredCookie] = []
        self._created = 0
        if cookies:
            self.update(cookies)

    def extract_cookies(self, response: Response) -> None:
        """
        Load cookies from the response `Set-Cookie` headers.
        """
        request = response.request
        host = request.url.host
        request_path = request.url.path or "/"
        secure_origin = request.url.scheme == "https"
        for header_name, header_value in response.headers.multi_items():
            if header_name.lower() != "set-cookie":
                continue
            for cookie_str in _split_set_cookie(header_value):
                parsed = _parse_set_cookie(
                    cookie_str,
                    host=host,
                    request_path=request_path,
                    secure_origin=secure_origin,
                )
                if parsed is None:
                    continue
                if parsed.delete:
                    self._remove_identity(
                        parsed.cookie.name,
                        parsed.cookie.domain,
                        parsed.cookie.path,
                    )
                    continue
                self._insert(parsed.cookie)

    def set_cookie_header(self, request: Request) -> None:
        """
        Set the `Cookie` header for cookies that match `request`.
        """
        self._purge_expired()
        host = request.url.host
        request_path = request.url.path or "/"
        secure = request.url.scheme == "https"
        matches = [
            cookie
            for cookie in self._cookies
            if (not cookie.secure or secure)
            and _domain_matches(host, cookie.domain, cookie.host_only)
            and _path_matches(request_path, cookie.path)
        ]
        matches.sort(key=lambda cookie: (-len(cookie.path), cookie.created))
        if not matches:
            return
        request.headers["Cookie"] = "; ".join(
            f"{cookie.name}={cookie.value}" for cookie in matches
        )

    def set(self, name: str, value: str, domain: str = "", path: str = "/") -> None:
        """
        Set a cookie value by name. May optionally include domain and path.

        An empty domain is not host-only: the cookie is sent to any host that
        matches the path and scheme rules.
        """
        stored_domain = domain.lstrip(".").lower() if domain else ""
        self._insert(
            _StoredCookie(
                name=name,
                value=value,
                domain=stored_domain,
                path=path or "/",
                host_only=False,
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
        self._purge_expired()
        value = None
        found = False
        for cookie in self._cookies:
            if cookie.name != name:
                continue
            if domain is not None and cookie.domain != _normalize_domain(domain):
                continue
            if path is not None and cookie.path != path:
                continue
            if found:
                message = f"Multiple cookies exist with name={name}"
                raise CookieConflict(message)
            value = cookie.value
            found = True
        if not found:
            return default
        return value

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
        domain_key = None if domain is None else _normalize_domain(domain)
        self._cookies = [
            cookie
            for cookie in self._cookies
            if not (
                cookie.name == name
                and (domain_key is None or cookie.domain == domain_key)
                and (path is None or cookie.path == path)
            )
        ]

    def clear(self, domain: str | None = None, path: str | None = None) -> None:
        """
        Delete all cookies. Optionally include a domain and path in
        order to only delete a subset of all the cookies.
        """
        if domain is None and path is None:
            self._cookies = []
            return
        domain_key = None if domain is None else _normalize_domain(domain)
        self._cookies = [
            cookie
            for cookie in self._cookies
            if not (
                (domain_key is None or cookie.domain == domain_key)
                and (path is None or cookie.path == path)
            )
        ]

    def update(self, cookies: typing.Any = None) -> None:  # type: ignore[override]
        """
        Update from another cookie container or from name/value pairs.
        """
        if not cookies:
            return
        if isinstance(cookies, CookieStore):
            existing = cookies._live_cookies()
            for cookie in sorted(existing, key=lambda item: item.created):
                self._insert(_copy_cookie(cookie))
            return
        from ._models import Cookies

        if isinstance(cookies, Cookies):
            for stdlib_cookie in cookies.jar:
                self._insert(_from_stdlib_cookie(stdlib_cookie))
            return
        if isinstance(cookies, CookieJar):
            for stdlib_cookie in cookies:
                self._insert(_from_stdlib_cookie(stdlib_cookie))
            return
        if isinstance(cookies, dict):
            for key, value in cookies.items():
                self.set(key, value)
            return
        if isinstance(cookies, list):
            for key, value in cookies:
                self.set(key, value)
            return
        raise TypeError(
            "cookies must be a CookieStore, Cookies, CookieJar, dict, or list of pairs"
        )

    def __setitem__(self, name: str, value: str) -> None:
        self.set(name, value)

    def __getitem__(self, name: str) -> str:
        value = self.get(name)
        if value is None:
            raise KeyError(name)
        return value

    def __delitem__(self, name: str) -> None:
        if name not in self:
            raise KeyError(name)
        self.delete(name)

    def __iter__(self) -> typing.Iterator[str]:
        return (cookie.name for cookie in self._live_cookies())

    def __len__(self) -> int:
        return len(self._live_cookies())

    def __bool__(self) -> bool:
        return len(self) > 0

    def __repr__(self) -> str:
        cookies_repr = ", ".join(
            f"<Cookie {cookie.name}={cookie.value} for {cookie.domain or '(any)'} />"
            for cookie in self._live_cookies()
        )
        return f"<CookieStore[{cookies_repr}]>"

    def _next_created(self) -> int:
        self._created += 1
        return self._created

    def _live_cookies(self) -> list[_StoredCookie]:
        self._purge_expired()
        return list(self._cookies)

    def _purge_expired(self) -> None:
        now = time.time()
        self._cookies = [
            cookie
            for cookie in self._cookies
            if cookie.expires is None or cookie.expires > now
        ]

    def _remove_identity(self, name: str, domain: str, path: str) -> None:
        self._cookies = [
            cookie
            for cookie in self._cookies
            if not (
                cookie.name == name and cookie.domain == domain and cookie.path == path
            )
        ]

    def _insert(self, cookie: _StoredCookie) -> None:
        self._purge_expired()
        self._remove_identity(cookie.name, cookie.domain, cookie.path)
        if cookie.expires is not None and cookie.expires <= time.time():
            return
        cookie.created = self._next_created()
        self._cookies.append(cookie)
        self._evict()

    def _evict(self) -> None:
        per_domain = self.max_cookies_per_domain
        if per_domain is not None:
            by_domain: dict[str, list[_StoredCookie]] = {}
            for cookie in self._cookies:
                by_domain.setdefault(cookie.domain, []).append(cookie)
            remove: set[int] = set()
            for group in by_domain.values():
                if len(group) <= per_domain:
                    continue
                group.sort(key=lambda item: item.created)
                remove.update(id(item) for item in group[: len(group) - per_domain])
            if remove:
                self._cookies = [
                    cookie for cookie in self._cookies if id(cookie) not in remove
                ]
        limit = self.max_cookies
        if limit is not None and len(self._cookies) > limit:
            ordered = sorted(self._cookies, key=lambda item: item.created)
            remove_ids = {id(item) for item in ordered[: len(ordered) - limit]}
            self._cookies = [
                cookie for cookie in self._cookies if id(cookie) not in remove_ids
            ]


def _normalize_domain(domain: str) -> str:
    return domain.lstrip(".").lower()


def _copy_cookie(cookie: _StoredCookie) -> _StoredCookie:
    return _StoredCookie(
        name=cookie.name,
        value=cookie.value,
        domain=cookie.domain,
        path=cookie.path,
        host_only=cookie.host_only,
        secure=cookie.secure,
        expires=cookie.expires,
        created=0,
    )


def _from_stdlib_cookie(cookie: Cookie) -> _StoredCookie:
    domain = cookie.domain or ""
    if cookie.domain_specified:
        host_only = False
        domain = domain.lstrip(".").lower()
    elif domain == "":
        host_only = False
    else:
        host_only = True
        domain = domain.lower()
    expires = None if cookie.expires is None else float(cookie.expires)
    return _StoredCookie(
        name=cookie.name,
        value=cookie.value or "",
        domain=domain,
        path=cookie.path or "/",
        host_only=host_only,
        secure=bool(cookie.secure),
        expires=expires,
        created=0,
    )


def _split_set_cookie(header_value: str) -> list[str]:
    return [part.strip() for part in _COMBINED_COOKIE_RE.split(header_value)]


def _parse_set_cookie(
    cookie_str: str,
    *,
    host: str,
    request_path: str,
    secure_origin: bool,
) -> _ParsedCookie | None:
    if not cookie_str or "=" not in cookie_str.split(";", 1)[0]:
        return None
    pieces = [part.strip() for part in cookie_str.split(";")]
    name, value = pieces[0].split("=", 1)
    name = name.strip()
    value = value.strip()
    if not name:
        return None
    if len(value) >= 2 and value[0] == '"' and value[-1] == '"':
        value = value[1:-1]

    domain_attr: str | None = None
    path_attr: str | None = None
    secure = False
    max_age: int | None = None
    max_age_set = False
    expires_at: float | None = None
    expires_set = False

    for piece in pieces[1:]:
        if not piece:
            continue
        if "=" in piece:
            attr_name, attr_value = piece.split("=", 1)
            attr_name = attr_name.strip().lower()
            attr_value = attr_value.strip()
            has_value = attr_value != ""
        else:
            attr_name = piece.strip().lower()
            attr_value = ""
            has_value = False

        if attr_name == "domain":
            if not has_value:
                return None
            domain_attr = attr_value
        elif attr_name == "max-age":
            if not has_value:
                return None
            max_age_set = True
            try:
                max_age = int(attr_value)
            except ValueError:
                max_age = None
        elif attr_name == "expires":
            if not has_value:
                return None
            expires_set = True
            expires_at = _parse_expires(attr_value)
        elif attr_name == "path":
            path_attr = attr_value
        elif attr_name == "secure":
            secure = True

    if name.startswith("__Host-"):
        if (
            not secure
            or not secure_origin
            or domain_attr is not None
            or path_attr != "/"
        ):
            return None
    elif name.startswith("__Secure-"):
        if not secure or not secure_origin:
            return None

    if domain_attr is None:
        host_only = True
        domain = host.lower().rstrip(".")
    else:
        domain = domain_attr.lstrip(".").lower().rstrip(".")
        host_only = False
        if not domain or not _domain_matches(host, domain, host_only=False):
            return None

    if path_attr is None or path_attr == "" or not path_attr.startswith("/"):
        path = _default_path(request_path)
    else:
        path = path_attr

    now = time.time()
    if max_age_set and max_age is not None:
        if max_age <= 0:
            return _ParsedCookie(
                True,
                _session_cookie(name, value, domain, path, host_only, secure),
            )
        expires = now + max_age
    elif expires_set and expires_at is not None:
        if expires_at <= now:
            return _ParsedCookie(
                True, _session_cookie(name, value, domain, path, host_only, secure)
            )
        expires = expires_at
    else:
        expires = None

    stored = _StoredCookie(
        name=name,
        value=value,
        domain=domain,
        path=path,
        host_only=host_only,
        secure=secure,
        expires=expires,
        created=0,
    )
    return _ParsedCookie(False, stored)


def _session_cookie(
    name: str,
    value: str,
    domain: str,
    path: str,
    host_only: bool,
    secure: bool,
) -> _StoredCookie:
    return _StoredCookie(
        name=name,
        value=value,
        domain=domain,
        path=path,
        host_only=host_only,
        secure=secure,
        expires=None,
        created=0,
    )
