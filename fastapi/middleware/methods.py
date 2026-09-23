import copy

from starlette.types import ASGIApp, Receive, Scope, Send

IMPLICIT_METHOD_SCOPE_KEY = "fastapi.implicit_method"


class ImplicitMethodTrackingMiddleware:
    """
    Count implicit HEAD and OPTIONS responses.

    `get_stats()` and `reset_stats()` return a deep copy of
    `{full_path: {"head_hits": int, "options_hits": int}}`. `full_path` is the
    ASGI request path. Only implicit responses are counted. Non-HTTP scopes are
    ignored. `reset_stats()` returns the counts from before the reset and then
    clears them.
    """

    def __init__(self, app: ASGIApp) -> None:
        self.app = app
        self._stats: dict[str, dict[str, int]] = {}

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope.get("type") != "http":
            await self.app(scope, receive, send)
            return
        try:
            await self.app(scope, receive, send)
        finally:
            self._record(scope)

    def get_stats(self) -> dict[str, dict[str, int]]:
        return copy.deepcopy(self._stats)

    def reset_stats(self) -> dict[str, dict[str, int]]:
        snapshot = self.get_stats()
        self._stats.clear()
        return snapshot

    def _record(self, scope: Scope) -> None:
        if scope.get("type") != "http":
            return
        kind = scope.get(IMPLICIT_METHOD_SCOPE_KEY)
        if kind == "head":
            field = "head_hits"
        elif kind == "options":
            field = "options_hits"
        else:
            return
        full_path = scope.get("path")
        if not isinstance(full_path, str):
            return
        entry = self._stats.setdefault(
            full_path, {"head_hits": 0, "options_hits": 0}
        )
        entry[field] += 1
