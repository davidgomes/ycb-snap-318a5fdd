from datetime import datetime, timezone

from fastapi import APIRouter, FastAPI, Response
from fastapi.middleware.deprecation import DeprecationTrackingMiddleware
from fastapi.testclient import TestClient

SUNSET = datetime(2026, 12, 31, 23, 59, 59, tzinfo=timezone.utc)
DEP_DATE = datetime(2025, 1, 1, 0, 0, 0, tzinfo=timezone.utc)


def test_deprecated_true_emits_deprecation_true():
    app = FastAPI()

    @app.get("/old", deprecated=True)
    def old():
        return {"ok": True}

    client = TestClient(app)
    response = client.get("/old")
    assert response.headers["deprecation"] == "true"
    assert "sunset" not in response.headers
    assert "link" not in response.headers


def test_sunset_and_openapi_extensions():
    app = FastAPI()

    @app.get("/old", deprecated=True, sunset=SUNSET, successor_url="/new")
    def old():
        return {"ok": True}

    client = TestClient(app)
    response = client.get("/old")
    assert response.headers["deprecation"] == "true"
    assert response.headers["sunset"] == "Thu, 31 Dec 2026 23:59:59 GMT"
    assert response.headers["link"] == '</new>; rel="successor-version"'

    schema = client.get("/openapi.json").json()
    operation = schema["paths"]["/old"]["get"]
    assert operation["deprecated"] is True
    assert operation["x-sunset"] == SUNSET.isoformat()
    assert operation["x-successor-url"] == "/new"


def test_deprecation_date_takes_precedence():
    app = FastAPI()

    @app.get(
        "/old",
        deprecated=True,
        deprecation_date=DEP_DATE,
        successor_url="https://example.com/v2",
    )
    def old():
        return {"ok": True}

    client = TestClient(app)
    response = client.get("/old")
    assert response.headers["deprecation"] == "Wed, 01 Jan 2025 00:00:00 GMT"
    assert response.headers["link"] == '<https://example.com/v2>; rel="successor-version"'

    schema = client.get("/openapi.json").json()
    operation = schema["paths"]["/old"]["get"]
    assert operation["x-deprecation-date"] == DEP_DATE.isoformat()


def test_preserve_existing_headers_and_merge_link():
    app = FastAPI()

    @app.get("/old", deprecated=True, sunset=SUNSET, successor_url="/v2")
    def old():
        return Response(
            content=b'{"ok":true}',
            media_type="application/json",
            headers={
                "Deprecation": "Sat, 01 Jan 2000 00:00:00 GMT",
                "Sunset": "Sun, 02 Jan 2000 00:00:00 GMT",
                "Link": '<https://example.com/docs>; rel="describedby"',
            },
        )

    client = TestClient(app)
    response = client.get("/old")
    assert response.headers["deprecation"] == "Sat, 01 Jan 2000 00:00:00 GMT"
    assert response.headers["sunset"] == "Sun, 02 Jan 2000 00:00:00 GMT"
    assert (
        response.headers["link"]
        == '<https://example.com/docs>; rel="describedby", </v2>; rel="successor-version"'
    )


def test_router_and_include_router_inheritance():
    inner = APIRouter(sunset=SUNSET)
    mid = APIRouter(deprecated=True)

    @inner.get("/from-inner")
    def from_inner():
        return {"ok": True}

    @mid.get("/from-mid")
    def from_mid():
        return {"ok": True}

    mid.include_router(inner, prefix="/inner")
    app = FastAPI()
    app.include_router(mid, successor_url="/v2")

    client = TestClient(app)
    inner_response = client.get("/inner/from-inner")
    assert inner_response.headers["deprecation"] == "true"
    assert inner_response.headers["sunset"] == "Thu, 31 Dec 2026 23:59:59 GMT"
    assert inner_response.headers["link"] == '</v2>; rel="successor-version"'

    mid_response = client.get("/from-mid")
    assert mid_response.headers["deprecation"] == "true"
    assert "sunset" not in mid_response.headers
    assert mid_response.headers["link"] == '</v2>; rel="successor-version"'


def test_include_router_overrides_included_router_defaults():
    router = APIRouter(deprecated=True, successor_url="/old-successor")

    @router.get("/item")
    def item():
        return {"ok": True}

    @router.get("/kept", successor_url="/kept-successor")
    def kept():
        return {"ok": True}

    app = FastAPI()
    app.include_router(router, successor_url="/new-successor")

    client = TestClient(app)
    assert client.get("/item").headers["link"] == '</new-successor>; rel="successor-version"'
    assert client.get("/kept").headers["link"] == '</kept-successor>; rel="successor-version"'


def test_fastapi_constructor_defaults():
    app = FastAPI(deprecated=True, sunset=SUNSET)

    @app.get("/legacy")
    def legacy():
        return {"ok": True}

    client = TestClient(app)
    response = client.get("/legacy")
    assert response.headers["deprecation"] == "true"
    assert response.headers["sunset"] == "Thu, 31 Dec 2026 23:59:59 GMT"


def test_add_api_route_inherits_router_defaults():
    router = APIRouter(deprecated=True, deprecation_date=DEP_DATE)
    router.add_api_route("/added", lambda: {"ok": True})
    app = FastAPI()
    app.include_router(router)
    client = TestClient(app)
    response = client.get("/added")
    assert response.headers["deprecation"] == "Wed, 01 Jan 2025 00:00:00 GMT"


def test_deprecation_tracking_middleware():
    app = FastAPI()

    @app.get("/plain")
    def plain():
        return {"ok": True}

    @app.get("/deprecated", deprecated=True)
    def deprecated_route():
        return {"ok": True}

    @app.get("/dated", deprecation_date=DEP_DATE)
    def dated():
        return {"ok": True}

    @app.get("/sunset", sunset=SUNSET)
    def sunset_only():
        return {"ok": True}

    middleware = DeprecationTrackingMiddleware(app)
    client = TestClient(middleware)
    client.get("/plain")
    client.get("/deprecated")
    client.get("/dated")
    client.get("/sunset")
    client.get("/sunset")

    stats = middleware.get_stats()
    assert stats["/deprecated"] == {"deprecated_hits": 1, "sunset_hits": 0}
    assert stats["/dated"] == {"deprecated_hits": 1, "sunset_hits": 0}
    assert stats["/sunset"] == {"deprecated_hits": 0, "sunset_hits": 2}
    assert "/plain" not in stats

    copied = middleware.get_stats()
    copied["/sunset"]["sunset_hits"] = 99
    assert middleware.get_stats()["/sunset"]["sunset_hits"] == 2

    middleware.reset_stats()
    assert middleware.get_stats() == {}


def test_middleware_skips_websocket_scope():
    async def dummy_app(scope, receive, send):
        return None

    middleware = DeprecationTrackingMiddleware(dummy_app)

    async def receive():
        return {"type": "websocket.connect"}

    async def send(message):
        return None

    import anyio

    async def run():
        await middleware({"type": "websocket", "path": "/deprecated"}, receive, send)

    anyio.run(run)
    assert middleware.get_stats() == {}
