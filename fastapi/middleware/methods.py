import copy

from starlette.types import ASGIApp, Message, Receive, Scope, Send


class ImplicitMethodTrackingMiddleware:
    """Count implicit `HEAD` and `OPTIONS` responses.

    Only HTTP requests served by FastAPI's implicit method handlers are
    recorded. Explicit `HEAD` and `OPTIONS` operations are ignored, as are
    non-HTTP ASGI scopes such as `websocket` and `lifespan`.

    `get_stats()` and `reset_stats()` return a deep copy of
    `{full_path: {"head_hits": int, "options_hits": int}}`. `full_path` is the
    request path, including the ASGI `root_path` when the app is mounted.
    `reset_stats()` returns that copy and then clears the counts.
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

    def _record(self, full_path: str, kind: str) -> None:
        entry = self._stats.setdefault(full_path, {"head_hits": 0, "options_hits": 0})
        if kind == "HEAD":
            entry["head_hits"] += 1
        elif kind == "OPTIONS":
            entry["options_hits"] += 1

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return

        async def send_wrapper(message: Message) -> None:
            if message["type"] == "http.response.start":
                kind = scope.get("fastapi_implicit_method")
                full_path = scope.get("fastapi_implicit_full_path")
                if kind in {"HEAD", "OPTIONS"} and isinstance(full_path, str):
                    self._record(full_path, kind)
            await send(message)

        await self.app(scope, receive, send_wrapper)
