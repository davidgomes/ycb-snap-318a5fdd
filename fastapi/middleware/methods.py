import copy
import threading

from starlette.types import ASGIApp, Receive, Scope, Send

IMPLICIT_METHOD_RECORDERS_KEY = "fastapi_implicit_method_recorders"

_STAT_KEYS = {"HEAD": "head_hits", "OPTIONS": "options_hits"}


class ImplicitMethodTrackingMiddleware:
    """
    Count the requests answered by implicit `HEAD` and `OPTIONS` handlers
    (see the `auto_head` and `auto_options` parameters), per full path.

    Explicit `HEAD` and `OPTIONS` *path operations* are not counted.

    ## Example

    ```python
    from fastapi import FastAPI
    from fastapi.middleware.methods import ImplicitMethodTrackingMiddleware

    app = FastAPI(auto_options=True)
    tracker = ImplicitMethodTrackingMiddleware(app)
    # Serve `tracker` instead of `app`, then:
    tracker.get_stats()  # {"/items": {"head_hits": 1, "options_hits": 0}}
    ```
    """

    def __init__(self, app: ASGIApp) -> None:
        self.app = app
        self._stats: dict[str, dict[str, int]] = {}
        self._lock = threading.Lock()

    def get_stats(self) -> dict[str, dict[str, int]]:
        with self._lock:
            return copy.deepcopy(self._stats)

    def reset_stats(self) -> dict[str, dict[str, int]]:
        with self._lock:
            stats = copy.deepcopy(self._stats)
            self._stats.clear()
        return stats

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return
        base_root_path: str = scope.get("root_path", "")

        def record(method: str, path: str, root_path: str) -> None:
            if root_path.startswith(base_root_path):
                path = root_path[len(base_root_path) :].rstrip("/") + path
            with self._lock:
                counts = self._stats.setdefault(
                    path, {"head_hits": 0, "options_hits": 0}
                )
                counts[_STAT_KEYS[method]] += 1

        scope[IMPLICIT_METHOD_RECORDERS_KEY] = [
            *scope.get(IMPLICIT_METHOD_RECORDERS_KEY, ()),
            record,
        ]
        await self.app(scope, receive, send)
