from __future__ import annotations

import dataclasses
import email.utils
import itertools
import re
import time
import typing
from http.cookiejar import CookieJar

from ._exceptions import CookieConflict

if typing.TYPE_CHECKING:  # pragma: no cover
    from ._models import Request, Response

__all__ = ["CookieStore"]

_SPLIT_COOKIES = re.compile(r",(?=\s*[^;,\s=]+\s*=)")


@dataclasses.dataclass
class _StoredCookie:
    name: str
    value: str
    domain: str
    path: str
    host_only: bool
    secure: bool
    expires: float | None
    created: int


def _check_limit(name: str, value: object) -> int | None:
    if value is None:
        return None
    if isinstance(value, bool) or not isinstance(value, int):
        raise TypeError(f"{name} must be an int or None")
    if value < 0:
        raise ValueError(f"{name} must not be negative")
    return value


def _default_path(request_path: str) -> str:
    if not request_path.startswith("/") or request_path.count("/") <= 1:
        return "/"
    return request_path[: request_path.rfind("/")]


def _path_match(request_path: str, cookie_path: str) -> bool:
    if request_path == cookie_path:
        return True
    return request_path.startswith(cookie_path) and (
        cookie_path.endswith("/") or request_path[len(cookie_path)] == "/"
    )


def _domain_match(host: str, domain: str) -> bool:
    return host == domain or host.endswith("." + domain)


class CookieStore(typing.MutableMapping[str, str]):
    """
    A deterministic cookie container, usable anywhere `cookies=` is accepted.
    """

    def __init__(
        self,
        cookies: typing.Any = None,
        *,
        max_cookies: int | None = None,
        max_cookies_per_domain: int | None = None,
    ) -> None:
        self.max_cookies = _check_limit("max_cookies", max_cookies)
        self.max_cookies_per_domain = _check_limit(
            "max_cookies_per_domain", max_cookies_per_domain
        )
        self._cookies: dict[tuple[str, str, str], _StoredCookie] = {}
        self._counter = itertools.count()
        if cookies is not None:
            self.update(cookies)

    def _purge_expired(self) -> None:
        now = time.time()
        for key, cookie in list(self._cookies.items()):
            if cookie.expires is not None and cookie.expires <= now:
                del self._cookies[key]

    def _store(
        self,
        name: str,
        value: str,
        domain: str,
        path: str,
        host_only: bool = False,
        secure: bool = False,
        expires: float | None = None,
    ) -> None:
        key = (name, domain, path)
        self._cookies.pop(key, None)
        self._cookies[key] = _StoredCookie(
            name, value, domain, path, host_only, secure, expires, next(self._counter)
        )
        self._evict(domain)

    def _evict(self, domain: str) -> None:
        if self.max_cookies_per_domain is not None:
            same = [c for c in self._cookies.values() if c.domain == domain]
            same.sort(key=lambda c: c.created)
            for cookie in same[: max(0, len(same) - self.max_cookies_per_domain)]:
                del self._cookies[(cookie.name, cookie.domain, cookie.path)]
        if self.max_cookies is not None:
            all_cookies = sorted(self._cookies.values(), key=lambda c: c.created)
            for cookie in all_cookies[: max(0, len(all_cookies) - self.max_cookies)]:
                del self._cookies[(cookie.name, cookie.domain, cookie.path)]

    def extract_cookies(self, response: Response) -> None:
        """
        Store any cookies from the response `Set-Cookie` headers.
        """
        request = response.request
        for header in response.headers.get_list("set-cookie"):
            for cookie_string in _SPLIT_COOKIES.split(header):
                self._extract_one(cookie_string, request)

    def _extract_one(self, cookie_string: str, request: Request) -> None:
        parts = cookie_string.split(";")
        name, sep, value = parts[0].partition("=")
        name, value = name.strip(), value.strip()
        if not sep or not name:
            return

        domain_attr: str | None = None
        path_attr: str | None = None
        max_age: int | None = None
        expires: float | None = None
        secure = False
        for attr in parts[1:]:
            key, _, attr_value = attr.partition("=")
            key, attr_value = key.strip().lower(), attr_value.strip()
            if key in ("domain", "max-age", "expires") and not attr_value:
                return
            if key == "domain":
                domain_attr = attr_value.lstrip(".").lower()
            elif key == "path":
                path_attr = attr_value
            elif key == "secure":
                secure = True
            elif key == "max-age":
                if re.fullmatch(r"-?\d+", attr_value):
                    max_age = int(attr_value)
            elif key == "expires":
                try:
                    parsed = email.utils.parsedate_to_datetime(attr_value)
                    expires = parsed.timestamp()
                except (TypeError, ValueError, IndexError, OverflowError):
                    pass

        url = request.url
        host = url.host.lower()
        is_https = url.scheme == "https"

        if domain_attr:
            if not _domain_match(host, domain_attr):
                return
            domain, host_only = domain_attr, False
        else:
            domain, host_only = host, True

        if not path_attr or not path_attr.startswith("/"):
            path = _default_path(url.path)
        else:
            path = path_attr

        if name.startswith("__Secure-") and not (secure and is_https):
            return
        if name.startswith("__Host-") and not (
            secure and is_https and domain_attr is None and path == "/"
        ):
            return

        if max_age is not None:
            expires = time.time() + max_age if max_age > 0 else 0.0
        if expires is not None and expires <= time.time():
            self._cookies.pop((name, domain, path), None)
            return

        self._store(name, value, domain, path, host_only, secure, expires)

    def set_cookie_header(self, request: Request) -> None:
        """
        Set a `Cookie` header on the request, if there are matching cookies.
        """
        if "cookie" in request.headers:
            return
        self._purge_expired()
        url = request.url
        host = url.host.lower()
        request_path = url.path or "/"
        matched = [
            cookie
            for cookie in self._cookies.values()
            if (
                cookie.domain == ""
                or (cookie.host_only and host == cookie.domain)
                or (not cookie.host_only and _domain_match(host, cookie.domain))
            )
            and _path_match(request_path, cookie.path)
            and (not cookie.secure or url.scheme == "https")
        ]
        if not matched:
            return
        matched.sort(key=lambda c: (-len(c.path), c.created))
        request.headers["Cookie"] = "; ".join(f"{c.name}={c.value}" for c in matched)

    def _select(
        self, name: str, domain: str | None = None, path: str | None = None
    ) -> list[_StoredCookie]:
        self._purge_expired()
        return [
            cookie
            for cookie in self._cookies.values()
            if cookie.name == name
            and (domain is None or cookie.domain == domain)
            and (path is None or cookie.path == path)
        ]

    def set(self, name: str, value: str, domain: str = "", path: str = "/") -> None:
        """
        Set a cookie value by name. May optionally include domain and path.
        """
        self._store(name, value, domain.lstrip(".").lower(), path)

    def get(  # type: ignore
        self,
        name: str,
        default: str | None = None,
        domain: str | None = None,
        path: str | None = None,
    ) -> str | None:
        matches = self._select(name, domain, path)
        if len(matches) > 1:
            raise CookieConflict(f"Multiple cookies exist with name={name}")
        return matches[0].value if matches else default

    def delete(
        self, name: str, domain: str | None = None, path: str | None = None
    ) -> None:
        for cookie in self._select(name, domain, path):
            del self._cookies[(cookie.name, cookie.domain, cookie.path)]

    def clear(self, domain: str | None = None, path: str | None = None) -> None:
        for key in list(self._cookies):
            if (domain is None or key[1] == domain) and (
                path is None or key[2] == path
            ):
                del self._cookies[key]

    def update(self, cookies: typing.Any = None) -> None:  # type: ignore
        from ._models import Cookies

        if cookies is None:
            return
        if isinstance(cookies, CookieStore):
            cookies._purge_expired()
            for c in sorted(cookies._cookies.values(), key=lambda c: c.created):
                self._store(
                    c.name, c.value, c.domain, c.path, c.host_only, c.secure, c.expires
                )
        elif isinstance(cookies, (Cookies, CookieJar)):
            jar = cookies.jar if isinstance(cookies, Cookies) else cookies
            for jc in jar:
                self._store(
                    jc.name,
                    jc.value or "",
                    jc.domain.lstrip(".").lower(),
                    jc.path or "/",
                    host_only=bool(jc.domain) and not jc.domain_specified,
                    secure=jc.secure,
                    expires=jc.expires,
                )
        elif isinstance(cookies, dict):
            for key, value in cookies.items():
                self.set(key, value)
        else:
            for key, value in cookies:
                self.set(key, value)

    def __getitem__(self, name: str) -> str:
        value = self.get(name)
        if value is None:
            raise KeyError(name)
        return value

    def __setitem__(self, name: str, value: str) -> None:
        self.set(name, value)

    def __delitem__(self, name: str) -> None:
        if not self._select(name):
            raise KeyError(name)
        self.delete(name)

    def __iter__(self) -> typing.Iterator[str]:
        self._purge_expired()
        return iter(dict.fromkeys(c.name for c in self._cookies.values()))

    def __len__(self) -> int:
        return len(list(iter(self)))

    def __bool__(self) -> bool:
        return len(self) > 0

    def __repr__(self) -> str:
        items = ", ".join(
            f"<Cookie {c.name}={c.value} for {c.domain or '*'}{c.path}>"
            for c in self._cookies.values()
        )
        return f"<CookieStore[{items}]>"
