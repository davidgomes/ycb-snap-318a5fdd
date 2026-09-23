import copy
import threading

from starlette.types import ASGIApp, Receive, Scope, Send


class ImplicitMethodTrackingMiddleware:
    """
    Count the requests served by implicit HEAD and OPTIONS responses (see the
    `auto_head` and `auto_options` parameters), per full request path.

    Explicit HEAD and OPTIONS *path operations*, and any other requests, are not
    counted.

    ## Example

    ```python
    from fastapi import FastAPI
    from fastapi.middleware.methods import ImplicitMethodTrackingMiddleware
    from fastapi.testclient import TestClient

    app = FastAPI(auto_options=True)


    @app.get("/items/")
    def read_items():
        return [{"name": "Empanada"}, {"name": "Arepa"}]


    tracked_app = ImplicitMethodTrackingMiddleware(app)
    client = TestClient(tracked_app)
    client.head("/items/")
    client.options("/items/")
    print(tracked_app.get_stats())
    # {'/items/': {'head_hits': 1, 'options_hits': 1}}
    ```
    """

    def __init__(self, app: ASGIApp) -> None:
        self.app = app
        self._stats: dict[str, dict[str, int]] = {}
        self._lock = threading.Lock()

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return
        path: str = scope["path"]
        root_path: str = scope.get("root_path", "")
        full_path = path if path.startswith(root_path) else root_path + path

        def track(method: str) -> None:
            counter = "head_hits" if method == "HEAD" else "options_hits"
            with self._lock:
                path_stats = self._stats.setdefault(
                    full_path, {"head_hits": 0, "options_hits": 0}
                )
                path_stats[counter] += 1

        scope["fastapi_implicit_method_trackers"] = [
            *scope.get("fastapi_implicit_method_trackers", ()),
            track,
        ]
        await self.app(scope, receive, send)

    def get_stats(self) -> dict[str, dict[str, int]]:
        """
        Return a copy of the implicit HEAD and OPTIONS hits per full path.
        """
        with self._lock:
            return copy.deepcopy(self._stats)

    def reset_stats(self) -> dict[str, dict[str, int]]:
        """
        Clear all the counts, returning a copy of the hits per full path recorded
        until now.
        """
        with self._lock:
            stats = copy.deepcopy(self._stats)
            self._stats.clear()
        return stats
