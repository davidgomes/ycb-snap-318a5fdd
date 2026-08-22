from fastapi import APIRouter, Depends, FastAPI, Header, Response
from fastapi.middleware.cors import CORSMiddleware
from fastapi.middleware.methods import ImplicitMethodTrackingMiddleware
from fastapi.testclient import TestClient


def test_implicit_head_default_on_get() -> None:
    app = FastAPI()

    @app.get("/items/{item_id}")
    def read_item(item_id: str, x_token: str = Header()):
        return {"item_id": item_id, "token": x_token}

    client = TestClient(app)
    response = client.head("/items/foo", headers={"x-token": "secret"})
    assert response.status_code == 200
    assert response.content == b""
    assert "item_id" not in response.text


def test_implicit_head_keeps_validation_and_headers() -> None:
    app = FastAPI()

    def add_header(response: Response) -> None:
        response.headers["x-dep"] = "yes"

    @app.get("/items/{item_id}", dependencies=[Depends(add_header)])
    def read_item(item_id: str, q: int):
        return {"item_id": item_id, "q": q}

    client = TestClient(app)
    missing = client.head("/items/foo")
    assert missing.status_code == 422

    app2 = FastAPI()

    @app2.get("/plain", dependencies=[Depends(add_header)], status_code=201)
    def plain():
        return {"ok": True}

    client2 = TestClient(app2)
    response = client2.head("/plain")
    assert response.status_code == 201
    assert response.content == b""
    assert response.headers["x-dep"] == "yes"


def test_explicit_head_wins() -> None:
    app = FastAPI()

    @app.get("/items")
    def read_items():
        return {"n": 1}

    @app.head("/items")
    def head_items():
        return Response(headers={"x-explicit": "1"})

    client = TestClient(app)
    response = client.head("/items")
    assert response.headers["x-explicit"] == "1"


def test_auto_head_false_on_app() -> None:
    app = FastAPI(auto_head=False)

    @app.get("/items")
    def read_items():
        return {"n": 1}

    client = TestClient(app)
    response = client.head("/items")
    assert response.status_code == 405


def test_route_overrides_app_auto_head() -> None:
    app = FastAPI(auto_head=False)

    @app.get("/on", auto_head=True)
    def on():
        return {"ok": True}

    @app.get("/off")
    def off():
        return {"ok": True}

    client = TestClient(app)
    assert client.head("/on").status_code == 200
    assert client.head("/off").status_code == 405


def test_include_precedence_route_include_router() -> None:
    router = APIRouter(auto_head=True, auto_options=False)

    @router.get("/a")
    def a():
        return {"p": "a"}

    @router.get("/b", auto_head=False)
    def b():
        return {"p": "b"}

    @router.get("/c")
    def c():
        return {"p": "c"}

    @router.get("/d", auto_head=True)
    def d():
        return {"p": "d"}

    app = FastAPI(auto_head=False, auto_options=False)
    app.include_router(router, prefix="/inc", auto_head=False, auto_options=True)

    client = TestClient(app)
    # route omitted, include False, router True -> include wins over router
    assert client.head("/inc/a").status_code == 405
    # explicit route False
    assert client.head("/inc/b").status_code == 405
    # same as a
    assert client.head("/inc/c").status_code == 405
    # explicit route True beats include False
    assert client.head("/inc/d").status_code == 200
    options = client.options("/inc/a")
    assert options.status_code == 200
    assert options.json()["path"] == "/inc/a"


def test_include_uses_router_when_include_omitted() -> None:
    router = APIRouter(auto_options=True)

    @router.get("/x")
    def x():
        return {"ok": True}

    app = FastAPI(auto_options=False)
    app.include_router(router)
    client = TestClient(app)
    response = client.options("/x")
    assert response.status_code == 200
    assert response.headers["allow"]
    assert client.head("/x").status_code == 200


def test_repeated_inclusion() -> None:
    router = APIRouter()

    @router.get("/item")
    def item():
        return {"ok": True}

    app = FastAPI()
    app.include_router(router, prefix="/v1", auto_options=True)
    app.include_router(router, prefix="/v2", auto_options=True)
    client = TestClient(app)
    assert client.get("/v1/item").json() == {"ok": True}
    assert client.get("/v2/item").json() == {"ok": True}
    assert client.options("/v1/item").json()["path"] == "/v1/item"
    assert client.options("/v2/item").json()["path"] == "/v2/item"


def test_method_ordering_and_one_options_per_path() -> None:
    app = FastAPI()

    @app.post("/mixed", auto_options=True)
    def create():
        return {"m": "post"}

    @app.get("/mixed", auto_options=True)
    def read():
        return {"m": "get"}

    @app.patch("/mixed")
    def patch():
        return {"m": "patch"}

    implicit_options = [
        route
        for route in app.router.routes
        if getattr(route, "is_implicit_options", False)
        and getattr(route, "path", None) == "/mixed"
    ]
    assert len(implicit_options) == 1

    client = TestClient(app)
    body = client.options("/mixed").json()
    assert body["path"] == "/mixed"
    assert body["methods"] == ["GET", "HEAD", "POST", "PATCH", "OPTIONS"]


def test_options_operations_match_openapi() -> None:
    app = FastAPI()

    @app.get("/docs-path", auto_options=True)
    def read_docs_path():
        return {"ok": True}

    @app.post("/docs-path", auto_options=True)
    def write_docs_path():
        return {"ok": True}

    client = TestClient(app)
    openapi = client.get("/openapi.json").json()
    path_item = openapi["paths"]["/docs-path"]
    assert "head" not in path_item
    assert "options" not in path_item
    options = client.options("/docs-path").json()
    assert options["operations"] == {
        key: value
        for key, value in path_item.items()
        if key not in {"head", "options"}
    }


def test_explicit_options_wins() -> None:
    app = FastAPI()

    @app.get("/items", auto_options=True)
    def read_items():
        return {"n": 1}

    @app.options("/items")
    def options_items():
        return Response(headers={"x-explicit-options": "1"})

    client = TestClient(app)
    response = client.options("/items")
    assert response.headers["x-explicit-options"] == "1"
    implicit = [
        route
        for route in app.router.routes
        if getattr(route, "is_implicit_options", False)
        and getattr(route, "path", None) == "/items"
    ]
    assert implicit == []


def test_cors_preflight_still_works() -> None:
    app = FastAPI()

    @app.get("/items", auto_options=True)
    def read_items():
        return {"n": 1}

    app.add_middleware(
        CORSMiddleware,
        allow_origins=["https://example.com"],
        allow_methods=["*"],
        allow_headers=["*"],
    )
    client = TestClient(app)
    response = client.options(
        "/items",
        headers={
            "Origin": "https://example.com",
            "Access-Control-Request-Method": "GET",
        },
    )
    assert response.status_code == 200
    assert response.headers["access-control-allow-origin"] == "https://example.com"


def test_docs_surface() -> None:
    app = FastAPI()

    @app.get("/items", auto_options=True)
    def read_items():
        return {"n": 1}

    client = TestClient(app)
    assert client.get("/docs").status_code == 200
    schema = client.get("/openapi.json").json()
    assert "get" in schema["paths"]["/items"]
    assert "head" not in schema["paths"]["/items"]
    assert "options" not in schema["paths"]["/items"]


def test_middleware_stats_implicit_only() -> None:
    app = FastAPI()

    @app.get("/tracked", auto_options=True)
    def tracked():
        return {"ok": True}

    @app.head("/explicit-head")
    def explicit_head():
        return Response()

    @app.options("/explicit-options")
    def explicit_options():
        return Response()

    tracking = ImplicitMethodTrackingMiddleware(app)
    client = TestClient(tracking)

    assert tracking.get_stats() == {}
    client.head("/tracked")
    client.head("/tracked")
    client.options("/tracked")
    client.head("/explicit-head")
    client.options("/explicit-options")
    client.get("/tracked")

    stats = tracking.get_stats()
    assert stats["/tracked"]["head_hits"] == 2
    assert stats["/tracked"]["options_hits"] == 1
    assert "/explicit-head" not in stats
    assert "/explicit-options" not in stats

    copied = tracking.get_stats()
    copied["/tracked"]["head_hits"] = 99
    assert tracking.get_stats()["/tracked"]["head_hits"] == 2

    tracking.reset_stats()
    assert tracking.get_stats() == {}


def test_middleware_ignores_non_http() -> None:
    import anyio

    hits = {"called": 0}

    async def inner(scope, receive, send):
        hits["called"] += 1

    middleware = ImplicitMethodTrackingMiddleware(inner)

    async def run() -> None:
        await middleware({"type": "lifespan"}, None, None)  # type: ignore[arg-type]
        await middleware({"type": "websocket", "path": "/ws"}, None, None)  # type: ignore[arg-type]

    anyio.run(run)
    assert hits["called"] == 2
    assert middleware.get_stats() == {}


def test_add_api_route_and_api_route() -> None:
    app = FastAPI(auto_options=False)

    def via_add():
        return {"via": "add"}

    app.add_api_route("/add", via_add, auto_options=True)

    @app.api_route("/api", methods=["GET"], auto_options=True)
    def via_api():
        return {"via": "api"}

    client = TestClient(app)
    assert client.head("/add").status_code == 200
    assert client.options("/add").json()["methods"][0] == "GET"
    assert client.options("/api").status_code == 200
