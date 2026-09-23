import copy

from starlette.types import ASGIApp, Receive, Scope, Send

# Set by the router only while it is serving an implicit HEAD or OPTIONS response.
IMPLICIT_METHOD_SCOPE_KEY = "fastapi_implicit_method"


def request_full_path(scope: Scope) -> str:
    root_path = scope.get("root_path") or ""
    path = scope.get("path") or ""
    if not isinstance(root_path, str):
        root_path = ""
    if not isinstance(path, str):
        path = ""
    if root_path.endswith("/") and path.startswith("/"):
        return f"{root_path[:-1]}{path}"
    return f"{root_path}{path}"


class ImplicitMethodTrackingMiddleware:
    """
    Count implicit HEAD and OPTIONS responses.

    `get_stats()` and `reset_stats()` return a deep copy of
    `{full_path: {"head_hits": int, "options_hits": int}}`. `reset_stats()` also
    clears the counts. Only implicit responses are tracked. Non-HTTP scopes are
    forwarded and ignored.
    """

    def __init__(self, app: ASGIApp) -> None:
        self.app = app
        self._stats: dict[str, dict[str, int]] = {}

    def get_stats(self) -> dict[str, dict[str, int]]:
        return copy.deepcopy(self._stats)

    def reset_stats(self) -> dict[str, dict[str, int]]:
        snapshot = copy.deepcopy(self._stats)
        self._stats.clear()
        return snapshot

    def _record(self, scope: Scope) -> None:
        kind = scope.get(IMPLICIT_METHOD_SCOPE_KEY)
        if kind not in {"HEAD", "OPTIONS"}:
            return
        full_path = request_full_path(scope)
        bucket = self._stats.setdefault(full_path, {"head_hits": 0, "options_hits": 0})
        if kind == "HEAD":
            bucket["head_hits"] += 1
        else:
            bucket["options_hits"] += 1

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return
        try:
            await self.app(scope, receive, send)
        finally:
            self._record(scope)
