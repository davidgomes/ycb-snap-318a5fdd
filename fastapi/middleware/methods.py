import copy

from fastapi.routing import IMPLICIT_METHOD_STATS_SCOPE_KEY
from starlette.types import ASGIApp, Receive, Scope, Send


class ImplicitMethodTrackingMiddleware:
    """
    Count requests answered by implicit `HEAD` and `OPTIONS` handlers, keyed by
    the full path of the matched *path operation*.

    Explicitly declared `HEAD` and `OPTIONS` *path operations* are not counted.

    ## Example

    ```python
    from fastapi import FastAPI
    from fastapi.middleware.methods import ImplicitMethodTrackingMiddleware

    app = FastAPI(auto_options=True)
    tracked = ImplicitMethodTrackingMiddleware(app)

    # serve `tracked`, then inspect `tracked.get_stats()`
    ```
    """

    def __init__(self, app: ASGIApp) -> None:
        self.app = app
        self._stats: dict[str, dict[str, int]] = {}

    def _record(self, full_path: str, key: str) -> None:
        entry = self._stats.setdefault(full_path, {"head_hits": 0, "options_hits": 0})
        entry[key] += 1

    def get_stats(self) -> dict[str, dict[str, int]]:
        return copy.deepcopy(self._stats)

    def reset_stats(self) -> None:
        self._stats.clear()

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] == "http":
            scope[IMPLICIT_METHOD_STATS_SCOPE_KEY] = self._record
        await self.app(scope, receive, send)
