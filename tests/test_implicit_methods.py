from fastapi import APIRouter, Depends, FastAPI, Response
from fastapi.middleware.cors import CORSMiddleware
from fastapi.middleware.methods import ImplicitMethodTrackingMiddleware
from fastapi.testclient import TestClient
from starlette.websockets import WebSocket


def test_direct_app_defaults_and_route_override():
    app = FastAPI(auto_head=False, auto_options=True)

    @app.get("/items/{item_id}")
    def read_item(item_id: int, q: int, response: Response):
        response.headers["x-item"] = str(item_id)
        return {"item_id": item_id, "q": q}

    @app.get("/forced", auto_head=True, auto_options=False)
    def forced():
        return {"ok": True}

    client = TestClient(app)

    # Omitted direct routes use the app values: HEAD off, OPTIONS on.
    assert client.head("/items/5?q=2").status_code == 405
    options = client.options("/items/5")
    body = options.json()
    assert options.status_code == 200
    assert body["path"] == "/items/{item_id}"
    assert body["methods"] == ["GET", "OPTIONS"]
    assert options.headers["allow"] == "GET, OPTIONS"
    schema_ops = app.openapi()["paths"]["/items/{item_id}"]
    assert body["operations"] == {
        key: value
        for key, value in schema_ops.items()
        if key not in {"head", "options"}
    }
    assert "head" not in schema_ops
    assert "options" not in schema_ops

    # A route value overrides the app default.
    assert client.head("/forced").status_code == 200
    assert client.head("/forced").content == b""
    assert client.options("/forced").status_code == 405

    default_app = FastAPI()

    @default_app.get("/items/{item_id}", status_code=201)
    def default_item(item_id: int, q: int, response: Response):
        response.headers["x-item"] = str(item_id)
        return {"item_id": item_id, "q": q}

    default_client = TestClient(default_app)
    missing = default_client.head("/items/5")
    assert missing.status_code == 422
    assert missing.content == b""
    head = default_client.head("/items/5?q=2")
    get = default_client.get("/items/5?q=2")
    assert head.status_code == 201
    assert head.content == b""
    assert head.headers["x-item"] == "5"
    assert head.headers["content-length"] == get.headers["content-length"]
    assert get.json() == {"item_id": 5, "q": 2}
    assert default_client.options("/items/5").status_code == 405


def test_dependency_status_and_explicit_operations_win():
    app = FastAPI()

    def mark(response: Response):
        response.headers["x-dep"] = "yes"
        return "dep"

    @app.get("/created", status_code=201)
    def created(value: str = Depends(mark)):
        return {"value": value}

    @app.get("/explicit")
    def explicit_get():
        return {"from": "get"}

    @app.head("/explicit")
    def explicit_head():
        return Response(status_code=204, headers={"x-explicit": "head"})

    @app.options("/explicit")
    def explicit_options():
        return Response(status_code=204, headers={"x-explicit": "options"})

    client = TestClient(app)
    head = client.head("/created")
    assert head.status_code == 201
    assert head.content == b""
    assert head.headers["x-dep"] == "yes"

    explicit = client.head("/explicit")
    assert explicit.status_code == 204
    assert explicit.headers["x-explicit"] == "head"
    options = client.options("/explicit")
    assert options.status_code == 204
    assert options.headers["x-explicit"] == "options"
    assert "path" not in options.headers


def test_included_router_precedence_and_repeated_inclusion():
    router = APIRouter(auto_head=False, auto_options=True)

    @router.get("/items")
    def read_items():
        return [{"name": "item"}]

    @router.post("/items", auto_options=False)
    def create_item():
        return {"ok": True}

    @router.get("/forced", auto_head=True, auto_options=False)
    def forced():
        return {"ok": True}

    app = FastAPI(auto_head=True, auto_options=True)
    app.include_router(router, prefix="/near")
    app.include_router(router, prefix="/over", auto_head=True, auto_options=False)

    other = APIRouter()

    @other.get("/plain")
    def plain():
        return {"ok": True}

    app.include_router(other, prefix="/a", auto_head=False, auto_options=False)
    app.include_router(other, prefix="/b", auto_head=True, auto_options=True)

    client = TestClient(app)

    # Included router values beat the app defaults.
    assert client.head("/near/items").status_code == 405
    near_options = client.options("/near/items")
    assert near_options.status_code == 200
    # POST disables its own flag, GET enables the path, so one OPTIONS remains.
    assert near_options.json()["methods"] == ["GET", "POST", "OPTIONS"]
    assert near_options.json()["path"] == "/near/items"
    assert near_options.headers["allow"] == "GET, POST, OPTIONS"

    # include_router overrides the included router when the route omits the flag.
    assert client.head("/over/items").status_code == 200
    assert client.head("/over/items").content == b""
    assert client.options("/over/items").status_code == 405

    # A route value beats both the include call and the router.
    assert client.head("/over/forced").status_code == 200
    assert client.options("/over/forced").status_code == 405

    # The same router can be included twice with different overrides.
    assert client.head("/a/plain").status_code == 405
    assert client.options("/a/plain").status_code == 405
    assert client.head("/b/plain").status_code == 200
    assert client.options("/b/plain").json()["methods"] == ["GET", "HEAD", "OPTIONS"]
    assert client.options("/b/plain").json()["path"] == "/b/plain"

    # Omitted include flags stay on the included router, not the app.
    parent = FastAPI(auto_head=False, auto_options=True)
    defaults = APIRouter()

    @defaults.get("/x")
    def read_x():
        return {"ok": True}

    parent.include_router(defaults)
    parent_client = TestClient(parent)
    assert parent_client.head("/x").status_code == 200
    assert parent_client.head("/x").content == b""
    assert parent_client.options("/x").status_code == 405

    # An explicit route flag beats a later include override.
    explicit = APIRouter(auto_head=False, auto_options=False)

    def added():
        return {"ok": True}

    explicit.add_api_route(
        "/z", added, methods=["GET"], auto_head=True, auto_options=True
    )
    parent.include_router(explicit, auto_head=False, auto_options=False)
    assert parent_client.head("/z").status_code == 200
    assert parent_client.options("/z").json()["methods"] == ["GET", "HEAD", "OPTIONS"]


def test_method_order_openapi_docs_and_cors():
    app = FastAPI(auto_options=True)
    app.add_middleware(
        CORSMiddleware,
        allow_origins=["https://example.com"],
        allow_methods=["*"],
        allow_headers=["*"],
    )

    @app.api_route("/mix", methods=["TRACE"])
    def trace_mix():
        return {"ok": True}

    @app.delete("/mix")
    def delete_mix():
        return {"ok": True}

    @app.patch("/mix")
    def patch_mix():
        return {"ok": True}

    @app.put("/mix")
    def put_mix():
        return {"ok": True}

    @app.post("/mix")
    def post_mix():
        return {"ok": True}

    @app.get("/mix")
    def get_mix():
        return {"ok": True}

    app.add_api_route(
        "/added", lambda: {"ok": True}, methods=["GET"], auto_options=True
    )

    client = TestClient(app)
    options = client.options("/mix")
    assert options.status_code == 200
    assert options.json()["methods"] == [
        "GET",
        "HEAD",
        "POST",
        "PUT",
        "PATCH",
        "DELETE",
        "OPTIONS",
        "TRACE",
    ]
    assert options.headers["allow"] == ", ".join(options.json()["methods"])
    schema_path = app.openapi()["paths"]["/mix"]
    assert options.json()["operations"] == {
        key: value
        for key, value in schema_path.items()
        if key not in {"head", "options"}
    }
    assert "head" not in schema_path
    assert "options" not in schema_path
    assert set(schema_path) == {"get", "post", "put", "patch", "delete", "trace"}

    docs = client.get("/docs")
    assert docs.status_code == 200
    assert "swagger" in docs.text.lower()
    redoc = client.get("/redoc")
    assert redoc.status_code == 200
    assert "redoc" in redoc.text.lower()
    openapi = client.get("/openapi.json")
    assert openapi.status_code == 200
    assert "/mix" in openapi.json()["paths"]
    assert "/added" in openapi.json()["paths"]

    preflight_headers = {
        "Origin": "https://example.com",
        "Access-Control-Request-Method": "GET",
        "Access-Control-Request-Headers": "X-Example",
    }
    preflight = client.options("/mix", headers=preflight_headers)
    assert preflight.status_code == 200
    assert preflight.text == "OK"
    assert preflight.headers["access-control-allow-origin"] == "https://example.com"
    assert "access-control-allow-methods" in preflight.headers

    cors_options = client.options("/mix", headers={"Origin": "https://example.com"})
    assert cors_options.status_code == 200
    assert cors_options.headers["content-type"].startswith("application/json")
    assert cors_options.json()["path"] == "/mix"
    assert cors_options.headers["access-control-allow-origin"] == "https://example.com"

    @app.get("/headed", auto_options=True)
    def headed_get():
        return {"from": "get"}

    @app.head("/headed")
    def headed_head():
        return Response(status_code=204, headers={"x-explicit": "head"})

    app.openapi_schema = None
    headed = client.options("/headed")
    assert headed.json()["methods"] == ["GET", "HEAD", "OPTIONS"]
    assert "head" not in headed.json()["operations"]
    assert set(headed.json()["operations"]) == {"get"}
    assert client.head("/headed").status_code == 204
    assert client.head("/headed").headers["x-explicit"] == "head"


def test_implicit_method_tracking_middleware():
    holder: dict[str, ImplicitMethodTrackingMiddleware] = {}

    class RecordingMiddleware(ImplicitMethodTrackingMiddleware):
        def __init__(self, app):
            super().__init__(app)
            holder["middleware"] = self

    app = FastAPI(root_path="/api", auto_options=True)
    app.add_middleware(RecordingMiddleware)

    @app.get("/items")
    def read_items():
        return {"ok": True}

    @app.post("/items", auto_options=False)
    def create_item():
        return {"ok": True}

    @app.get("/explicit")
    def explicit_get():
        return {"from": "get"}

    @app.head("/explicit")
    def explicit_head():
        return Response(status_code=204)

    @app.options("/explicit")
    def explicit_options():
        return Response(status_code=204)

    @app.websocket("/ws")
    async def ws(websocket: WebSocket):
        await websocket.accept()
        await websocket.close()

    client = TestClient(app)
    assert client.head("/items").status_code == 200
    assert client.head("/items").status_code == 200
    assert client.options("/items").status_code == 200
    assert client.get("/items").status_code == 200
    assert client.head("/explicit").status_code == 204
    assert client.options("/explicit").status_code == 204
    with client.websocket_connect("/ws"):
        pass

    middleware = holder["middleware"]
    stats = middleware.get_stats()
    assert stats == {"/api/items": {"head_hits": 2, "options_hits": 1}}
    stats["/api/items"]["head_hits"] = 99
    assert middleware.get_stats()["/api/items"]["head_hits"] == 2
    reset = middleware.reset_stats()
    assert reset == {"/api/items": {"head_hits": 2, "options_hits": 1}}
    assert middleware.get_stats() == {}
    assert client.options("/items").status_code == 200
    assert middleware.get_stats() == {"/api/items": {"head_hits": 0, "options_hits": 1}}
