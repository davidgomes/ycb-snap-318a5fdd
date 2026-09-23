import copy

from starlette.types import ASGIApp, Receive, Scope, Send


class ImplicitMethodTrackingMiddleware:
    """Count implicit HEAD and OPTIONS responses by route path.

    Only HTTP requests handled by an implicit method are recorded. Websocket
    and lifespan scopes are forwarded unchanged.
    """

    def __init__(self, app: ASGIApp) -> None:
        self.app = app
        self._stats: dict[str, dict[str, int]] = {}

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return
        try:
            await self.app(scope, receive, send)
        finally:
            self._record(scope)

    def _record(self, scope: Scope) -> None:
        kind = scope.get("fastapi_implicit_method")
        if kind not in {"HEAD", "OPTIONS"}:
            return
        path = scope.get("fastapi_implicit_path")
        if not isinstance(path, str):
            return
        entry = self._stats.setdefault(path, {"head_hits": 0, "options_hits": 0})
        if kind == "HEAD":
            entry["head_hits"] += 1
        else:
            entry["options_hits"] += 1

    def get_stats(self) -> dict[str, dict[str, int]]:
        return copy.deepcopy(self._stats)

    def reset_stats(self) -> dict[str, dict[str, int]]:
        snapshot = copy.deepcopy(self._stats)
        self._stats.clear()
        return snapshot
