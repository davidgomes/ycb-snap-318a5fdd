from starlette.types import ASGIApp, Receive, Scope, Send


class DeprecationTrackingMiddleware:
    def __init__(self, app: ASGIApp) -> None:
        self.app = app
        self._stats: dict[str, dict[str, int]] = {}

    def get_stats(self) -> dict[str, dict[str, int]]:
        return {
            path: {"deprecated_hits": values["deprecated_hits"], "sunset_hits": values["sunset_hits"]}
            for path, values in self._stats.items()
        }

    def reset_stats(self) -> None:
        self._stats.clear()

    def _record(self, scope: Scope) -> None:
        route = scope.get("route")
        if route is None:
            return
        deprecated = getattr(route, "deprecated", None)
        deprecation_date = getattr(route, "deprecation_date", None)
        sunset = getattr(route, "sunset", None)
        is_deprecated = bool(deprecated) or deprecation_date is not None
        is_sunset = sunset is not None
        if not is_deprecated and not is_sunset:
            return
        path = scope.get("path", "")
        entry = self._stats.setdefault(
            path, {"deprecated_hits": 0, "sunset_hits": 0}
        )
        if is_deprecated:
            entry["deprecated_hits"] += 1
        if is_sunset:
            entry["sunset_hits"] += 1

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return
        await self.app(scope, receive, send)
        self._record(scope)
