import copy
from starlette.types import ASGIApp, Receive, Scope, Send

IMPLICIT_METHOD_SCOPE_KEY = "fastapi_implicit_method"


class ImplicitMethodTrackingMiddleware:
    """Track hits for implicit HEAD and OPTIONS responses only."""

    def __init__(self, app: ASGIApp) -> None:
        self.app = app
        self._stats: dict[str, dict[str, int]] = {}

    def get_stats(self) -> dict[str, dict[str, int]]:
        return copy.deepcopy(self._stats)

    def reset_stats(self) -> None:
        self._stats.clear()

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return
        await self.app(scope, receive, send)
        implicit = scope.get(IMPLICIT_METHOD_SCOPE_KEY)
        if implicit not in {"head", "options"}:
            return
        full_path = f"{scope.get('root_path', '')}{scope.get('path', '')}"
        counts = self._stats.setdefault(
            full_path, {"head_hits": 0, "options_hits": 0}
        )
        if implicit == "head":
            counts["head_hits"] += 1
        else:
            counts["options_hits"] += 1
