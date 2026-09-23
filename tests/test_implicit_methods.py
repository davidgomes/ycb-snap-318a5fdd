import inspect
from typing import Annotated, Any, get_args, get_origin

import anyio
from annotated_doc import Doc
from fastapi import APIRouter, Depends, FastAPI, Response
from fastapi.middleware.cors import CORSMiddleware
from fastapi.middleware.methods import ImplicitMethodTrackingMiddleware
from fastapi.testclient import TestClient

METHOD_ORDER = ["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE"]


async def _raw_http(
    app: Any, method: str, path: str
) -> tuple[int, dict[str, str], bytes]:
    messages: list[dict[str, Any]] = []

    async def receive() -> dict[str, Any]:
        return {"type": "http.request", "body": b"", "more_body": False}

    async def send(message: dict[str, Any]) -> None:
        messages.append(message)

    scope = {
        "type": "http",
        "asgi": {"version": "3.0"},
        "http_version": "1.1",
        "method": method,
        "scheme": "http",
        "path": path,
        "raw_path": path.encode(),
        "query_string": b"",
        "headers": [],
        "client": ("127.0.0.1", 50000),
        "server": ("testserver", 80),
    }
    await app(scope, receive, send)
    start = next(
        message for message in messages if message["type"] == "http.response.start"
    )
    headers = {
        key.decode("latin-1"): value.decode("latin-1")
        for key, value in start["headers"]
    }
    body = b"".join(
        message.get("body", b"")
        for message in messages
        if message["type"] == "http.response.body"
    )
    return start["status"], headers, body


def _annotation_is_doc(annotation: object) -> bool:
    if get_origin(annotation) is not Annotated:
        return False
    args = get_args(annotation)
    return len(args) >= 2 and isinstance(args[1], Doc)


def test_public_signatures_use_annotated_doc() -> None:
    targets = [
        FastAPI.__init__,
        APIRouter.__init__,
        FastAPI.add_api_route,
        APIRouter.add_api_route,
        FastAPI.api_route,
        APIRouter.api_route,
        FastAPI.include_router,
        APIRouter.include_router,
        FastAPI.get,
        APIRouter.get,
        FastAPI.post,
        APIRouter.options,
    ]
    for func in targets:
        signature = inspect.signature(func)
        for name, default in ("auto_head", True), ("auto_options", False):
            param = signature.parameters[name]
            assert _annotation_is_doc(param.annotation)
            assert param.default.value is default


def test_direct_app_head_preserves_get_behavior() -> None:
    calls: list[str] = []

    def mark(value: str = "dep") -> str:
        calls.append(value)
        return value

    app = FastAPI()

    @app.get("/items/{item_id}", status_code=201)
    def read_item(
        item_id: int, q: int, marker: Annotated[str, Depends(mark)], response: Response
    ):
        response.headers["X-Marker"] = marker
        response.headers["X-Item"] = str(item_id)
        return {"item_id": item_id, "q": q}

    client = TestClient(app)
    get_response = client.get("/items/3?q=4")
    head_response = client.request("HEAD", "/items/3?q=4")

    assert get_response.status_code == 201
    assert get_response.json() == {"item_id": 3, "q": 4}
    assert head_response.status_code == 201
    assert head_response.content == b""
    assert head_response.headers["x-marker"] == "dep"
    assert head_response.headers["x-item"] == "3"
    assert head_response.headers["content-type"].startswith("application/json")
    assert (
        head_response.headers["content-length"]
        == get_response.headers["content-length"]
    )
    assert calls == ["dep", "dep"]

    invalid = client.request("HEAD", "/items/3")
    assert invalid.status_code == 422
    assert invalid.content == b""

    schema = app.openapi()
    assert "head" not in schema["paths"]["/items/{item_id}"]
    assert "options" not in schema["paths"]["/items/{item_id}"]


def test_direct_app_values_are_outermost_defaults() -> None:
    app = FastAPI(auto_head=False, auto_options=True)

    @app.get("/default")
    def default_route():
        return {"ok": True}

    @app.get("/explicit", auto_head=True, auto_options=False)
    def explicit_route():
        return {"ok": True}

    client = TestClient(app)
    assert client.request("HEAD", "/default").status_code == 405
    assert client.options("/default").status_code == 200
    assert client.request("HEAD", "/explicit").status_code == 200
    assert client.request("HEAD", "/explicit").content == b""
    assert client.options("/explicit").status_code == 405


def test_included_router_precedence_layers() -> None:
    app = FastAPI(auto_head=False, auto_options=True)

    route_wins = APIRouter(auto_head=True, auto_options=True)

    @route_wins.get("/items", auto_head=False, auto_options=False)
    def route_level():
        return {"layer": "route"}

    include_wins = APIRouter(auto_head=True, auto_options=False)

    @include_wins.get("/items")
    def include_level():
        return {"layer": "include"}

    router_wins = APIRouter(auto_head=False, auto_options=True)

    @router_wins.get("/items")
    def router_level():
        return {"layer": "router"}

    omitted = APIRouter()

    @omitted.get("/items")
    def omitted_level():
        return {"layer": "default"}

    app.include_router(route_wins, prefix="/route", auto_head=True, auto_options=True)
    app.include_router(
        include_wins, prefix="/include", auto_head=False, auto_options=True
    )
    app.include_router(router_wins, prefix="/router")
    app.include_router(omitted, prefix="/omitted")

    client = TestClient(app)
    assert client.request("HEAD", "/route/items").status_code == 405
    assert client.options("/route/items").status_code == 405

    assert client.request("HEAD", "/include/items").status_code == 405
    assert client.options("/include/items").status_code == 200

    assert client.request("HEAD", "/router/items").status_code == 405
    assert client.options("/router/items").status_code == 200

    # App-level False/True does not apply to an included router that omits both.
    assert client.request("HEAD", "/omitted/items").status_code == 200
    assert client.options("/omitted/items").status_code == 405


def test_nested_include_can_set_an_omitted_route() -> None:
    inner = APIRouter()

    @inner.get("/items")
    def read_items():
        return {"ok": True}

    explicit = APIRouter()

    @explicit.get("/items", auto_head=True, auto_options=False)
    def read_explicit():
        return {"ok": True}

    outer = APIRouter()
    outer.include_router(inner, prefix="/open")
    outer.include_router(explicit, prefix="/fixed")

    app = FastAPI(auto_head=True, auto_options=False)
    app.include_router(outer, auto_head=False, auto_options=True)
    client = TestClient(app)

    assert client.request("HEAD", "/open/items").status_code == 405
    assert client.options("/open/items").status_code == 200
    assert client.request("HEAD", "/fixed/items").status_code == 200
    assert client.options("/fixed/items").status_code == 405


def test_repeated_inclusion_keeps_each_include_setting() -> None:
    router = APIRouter()

    @router.get("/items")
    def read_items():
        return {"ok": True}

    app = FastAPI(auto_head=False, auto_options=False)
    app.include_router(router, prefix="/on", auto_head=True, auto_options=True)
    app.include_router(router, prefix="/off", auto_head=False, auto_options=False)

    client = TestClient(app)
    assert client.request("HEAD", "/on/items").status_code == 200
    assert client.options("/on/items").status_code == 200
    assert client.request("HEAD", "/off/items").status_code == 405
    assert client.options("/off/items").status_code == 405
    assert client.get("/on/items").json() == {"ok": True}
    assert client.get("/off/items").json() == {"ok": True}


def test_explicit_head_and_options_win() -> None:
    app = FastAPI()

    @app.get("/items", auto_options=True)
    def read_items():
        return {"source": "get"}

    @app.head("/items")
    def head_items(response: Response):
        response.headers["X-From"] = "explicit-head"
        return {"source": "head"}

    @app.options("/choices")
    def options_choices():
        return {"source": "explicit-options"}

    @app.get("/choices", auto_options=True)
    def read_choices():
        return {"source": "get"}

    client = TestClient(app)
    head_response = client.request("HEAD", "/items")
    assert head_response.status_code == 200
    assert head_response.headers["x-from"] == "explicit-head"
    status, headers, body = anyio.run(_raw_http, app, "HEAD", "/items")
    assert status == 200
    assert headers["x-from"] == "explicit-head"
    assert body != b""
    plain = FastAPI()

    @plain.get("/only")
    def only_get():
        return {"source": "get"}

    implicit_status, _, implicit_body = anyio.run(_raw_http, plain, "HEAD", "/only")
    assert implicit_status == 200
    assert implicit_body == b""

    options_response = client.options("/choices")
    assert options_response.json() == {"source": "explicit-options"}
    assert "operations" not in options_response.json()


def test_options_payload_order_openapi_and_single_response() -> None:
    app = FastAPI()

    @app.trace("/items/{item_id}")
    def trace_item(item_id: int):
        return {"item_id": item_id}

    @app.delete("/items/{item_id}", auto_options=False)
    def delete_item(item_id: int):
        return {"item_id": item_id}

    @app.patch("/items/{item_id}")
    def patch_item(item_id: int):
        return {"item_id": item_id}

    @app.put("/items/{item_id}")
    def put_item(item_id: int):
        return {"item_id": item_id}

    @app.post("/items/{item_id}")
    def post_item(item_id: int):
        return {"item_id": item_id}

    @app.get("/items/{item_id}", auto_options=True)
    def get_item(item_id: int):
        return {"item_id": item_id}

    @app.head("/items/{item_id}")
    def head_item(item_id: int):
        return {"item_id": item_id}

    client = TestClient(app)
    response = client.options("/items/7")
    assert response.status_code == 200
    body = response.json()
    assert body["path"] == "/items/{item_id}"
    assert body["methods"] == METHOD_ORDER
    assert response.headers["allow"] == ", ".join(METHOD_ORDER)

    schema_path = app.openapi()["paths"]["/items/{item_id}"]
    expected_operations = {
        key: value
        for key, value in schema_path.items()
        if key not in {"head", "options"}
    }
    assert body["operations"] == expected_operations
    assert "head" in schema_path
    assert "head" not in body["operations"]
    assert "options" not in body["operations"]
    assert set(body["operations"]) == {"get", "post", "put", "patch", "delete", "trace"}


def test_cors_preflight_and_docs_surface() -> None:
    app = FastAPI()
    app.add_middleware(
        CORSMiddleware,
        allow_origins=["https://example.com"],
        allow_methods=["*"],
        allow_headers=["*"],
    )

    @app.get("/items", auto_options=True)
    def read_items():
        return {"ok": True}

    client = TestClient(app)
    preflight = client.options(
        "/items",
        headers={
            "Origin": "https://example.com",
            "Access-Control-Request-Method": "POST",
            "Access-Control-Request-Headers": "X-Example",
        },
    )
    assert preflight.status_code == 200
    assert preflight.text == "OK"
    assert preflight.headers["access-control-allow-origin"] == "https://example.com"
    assert "operations" not in preflight.text

    plain = client.options("/items")
    assert plain.json()["path"] == "/items"
    assert plain.json()["methods"] == ["GET", "HEAD", "OPTIONS"]
    assert "get" in plain.json()["operations"]

    docs = client.get("/docs")
    assert docs.status_code == 200
    assert "swagger" in docs.text.lower()
    redoc = client.get("/redoc")
    assert redoc.status_code == 200
    assert "redoc" in redoc.text.lower()
    openapi = client.get("/openapi.json")
    assert openapi.status_code == 200
    assert "head" not in openapi.json()["paths"]["/items"]
    assert client.request("HEAD", "/docs").status_code == 200


def test_middleware_stats_track_implicit_hits_only() -> None:
    app = FastAPI()

    @app.get("/items/{item_id}", auto_options=True)
    def read_item(item_id: int):
        return {"item_id": item_id}

    @app.head("/explicit")
    def explicit_head():
        return {"source": "head"}

    @app.get("/explicit")
    def explicit_get():
        return {"source": "get"}

    @app.options("/explicit")
    def explicit_options():
        return {"source": "options"}

    tracker = ImplicitMethodTrackingMiddleware(app)
    client = TestClient(tracker)

    assert client.request("HEAD", "/items/4").status_code == 200
    assert client.options("/items/4").status_code == 200
    assert client.get("/items/4").status_code == 200
    assert client.request("HEAD", "/items/4").content == b""
    assert client.request("HEAD", "/explicit").status_code == 200
    assert client.options("/explicit").json() == {"source": "options"}

    stats = tracker.get_stats()
    assert stats == {"/items/4": {"head_hits": 2, "options_hits": 1}}
    stats["/items/4"]["head_hits"] = 99
    assert tracker.get_stats()["/items/4"]["head_hits"] == 2

    cleared = tracker.reset_stats()
    assert cleared == {"/items/4": {"head_hits": 2, "options_hits": 1}}
    assert tracker.get_stats() == {}
    assert tracker.reset_stats() == {}


def test_middleware_ignores_non_http_scopes() -> None:
    import anyio

    events: list[str] = []

    async def inner(scope, receive, send):
        events.append(scope["type"])
        if scope["type"] == "lifespan":
            await send({"type": "lifespan.startup.complete"})
            await send({"type": "lifespan.shutdown.complete"})

    tracker = ImplicitMethodTrackingMiddleware(inner)

    async def receive():
        return {"type": "lifespan.startup"}

    async def send(message):
        events.append(message["type"])

    async def exercise() -> None:
        await tracker({"type": "lifespan"}, receive, send)
        await tracker({"type": "websocket", "path": "/ws"}, receive, send)

    anyio.run(exercise)
    assert events[0] == "lifespan"
    assert "websocket" in events
    assert tracker.get_stats() == {}
