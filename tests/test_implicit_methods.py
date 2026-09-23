import asyncio
import inspect

from fastapi import APIRouter, Depends, FastAPI
from fastapi.middleware.cors import CORSMiddleware
from fastapi.middleware.methods import (
    IMPLICIT_METHOD_SCOPE_KEY,
    ImplicitMethodTrackingMiddleware,
)
from fastapi.responses import JSONResponse
from fastapi.testclient import TestClient


def _messages(app, method: str, path: str, headers: list[tuple[bytes, bytes]] | None = None):
    collected: list[dict] = []

    async def receive():
        return {"type": "http.request", "body": b"", "more_body": False}

    async def send(message):
        collected.append(message)

    scope = {
        "type": "http",
        "asgi": {"version": "3.0", "spec_version": "2.3"},
        "http_version": "1.1",
        "method": method,
        "scheme": "http",
        "path": path,
        "raw_path": path.encode(),
        "query_string": b"",
        "headers": headers or [],
        "client": ("127.0.0.1", 50000),
        "server": ("testserver", 80),
    }
    asyncio.run(app(scope, receive, send))
    return collected


def _body(messages: list[dict]) -> bytes:
    chunks = [
        message.get("body", b"")
        for message in messages
        if message["type"] == "http.response.body"
    ]
    return b"".join(chunks)


def _status(messages: list[dict]) -> int:
    for message in messages:
        if message["type"] == "http.response.start":
            return message["status"]
    raise AssertionError(messages)


def test_signatures_use_annotated_doc():
    for owner in (FastAPI, APIRouter):
        for name in (
            "__init__",
            "get",
            "put",
            "post",
            "delete",
            "options",
            "head",
            "patch",
            "trace",
            "api_route",
            "add_api_route",
            "include_router",
        ):
            params = inspect.signature(getattr(owner, name)).parameters
            for param_name in ("auto_head", "auto_options"):
                annotation = params[param_name].annotation
                assert annotation.__metadata__
                assert params[param_name].default is None


def test_default_head_on_and_options_off():
    app = FastAPI()

    @app.get("/items")
    def items():
        return {"ok": True}

    client = TestClient(app)
    head = client.head("/items")
    assert head.status_code == 200
    assert head.content == b""
    options = client.options("/items")
    assert options.status_code == 405
    schema = client.get("/openapi.json").json()
    assert "head" not in schema["paths"]["/items"]
    assert "options" not in schema["paths"]["/items"]


def test_implicit_head_preserves_get_behavior_without_body():
    called = {"dependency": 0}

    def track():
        called["dependency"] += 1

    app = FastAPI()

    @app.get("/items/{item_id}", status_code=201, dependencies=[Depends(track)])
    def items(item_id: int):
        return JSONResponse(
            {"item_id": item_id},
            status_code=201,
            headers={"x-marker": "yes"},
        )

    client = TestClient(app)
    get_response = client.get("/items/5")
    head_messages = _messages(app, "HEAD", "/items/5")
    invalid_messages = _messages(app, "HEAD", "/items/nope")
    assert get_response.status_code == 201
    assert get_response.headers["x-marker"] == "yes"
    assert _status(head_messages) == 201
    assert _body(head_messages) == b""
    assert called["dependency"] == 3
    head = client.head("/items/5")
    assert head.headers["x-marker"] == "yes"
    assert head.headers["content-length"] == get_response.headers["content-length"]
    assert _status(invalid_messages) == 422
    assert _body(invalid_messages) == b""


def test_explicit_head_and_options_win():
    app = FastAPI()

    @app.get("/items", auto_options=True)
    def get_items():
        return {"from": "get"}

    @app.head("/items")
    def head_items():
        return JSONResponse({"from": "head"}, headers={"x-explicit": "head"})

    @app.options("/items")
    def options_items():
        return JSONResponse({"from": "options"}, headers={"x-explicit": "options"})

    head_messages = _messages(app, "HEAD", "/items")
    options_messages = _messages(app, "OPTIONS", "/items")
    assert _status(head_messages) == 200
    assert b"head" in _body(head_messages)
    assert _status(options_messages) == 200
    assert b"options" in _body(options_messages)
    client = TestClient(app)
    assert client.head("/items").headers["x-explicit"] == "head"
    assert client.options("/items").headers["x-explicit"] == "options"


def test_auto_head_can_be_disabled():
    app = FastAPI(auto_head=False)

    @app.get("/items")
    def items():
        return {"ok": True}

    @app.get("/enabled", auto_head=True)
    def enabled():
        return {"ok": True}

    client = TestClient(app)
    assert client.head("/items").status_code == 405
    assert client.get("/items").status_code == 200
    assert client.head("/enabled").status_code == 200
    assert _body(_messages(app, "HEAD", "/enabled")) == b""


def test_options_metadata_order_and_openapi():
    app = FastAPI()

    @app.trace("/items", auto_options=True)
    def trace_items():
        return {"ok": True}

    @app.delete("/items")
    def delete_items():
        return {"ok": True}

    @app.patch("/items")
    def patch_items():
        return {"ok": True}

    @app.put("/items")
    def put_items():
        return {"ok": True}

    @app.post("/items")
    def post_items():
        return {"ok": True}

    @app.get("/items", auto_head=False)
    def get_items():
        return {"ok": True}

    client = TestClient(app)
    response = client.options("/items")
    assert response.status_code == 200
    payload = response.json()
    assert payload["path"] == "/items"
    assert payload["methods"] == [
        "GET",
        "POST",
        "PUT",
        "PATCH",
        "DELETE",
        "OPTIONS",
        "TRACE",
    ]
    assert response.headers["allow"] == ", ".join(payload["methods"])
    schema = app.openapi()
    assert payload["operations"] == {
        key: value
        for key, value in schema["paths"]["/items"].items()
        if key not in {"head", "options"}
    }
    assert "head" not in schema["paths"]["/items"]
    assert "options" not in schema["paths"]["/items"]
    again = client.options("/items")
    assert again.json() == payload


def test_one_options_response_when_any_operation_enables_it():
    app = FastAPI()

    @app.get("/items", auto_options=False)
    def get_items():
        return {"ok": True}

    @app.post("/items", auto_options=True)
    def post_items():
        return {"ok": True}

    @app.get("/other")
    def other():
        return {"ok": True}

    client = TestClient(app)
    response = client.options("/items")
    assert response.status_code == 200
    assert response.json()["methods"] == ["GET", "HEAD", "POST", "OPTIONS"]
    assert client.options("/other").status_code == 405


def test_direct_app_routes_use_app_defaults():
    app = FastAPI(auto_head=False, auto_options=True)

    @app.get("/items")
    def items():
        return {"ok": True}

    client = TestClient(app)
    assert client.head("/items").status_code == 405
    options = client.options("/items")
    assert options.status_code == 200
    assert options.json()["methods"] == ["GET", "OPTIONS"]


def test_included_router_precedence_layers():
    app = FastAPI(auto_head=False, auto_options=True)
    omitted = APIRouter()

    @omitted.get("/items")
    def omitted_items():
        return {"ok": True}

    router_defaults = APIRouter(auto_head=False, auto_options=True)

    @router_defaults.get("/items")
    def router_items():
        return {"ok": True}

    include_override = APIRouter(auto_head=False, auto_options=False)

    @include_override.get("/items")
    def include_items():
        return {"ok": True}

    route_override = APIRouter(auto_head=True, auto_options=True)

    @route_override.get("/items", auto_head=False, auto_options=False)
    def route_items():
        return {"ok": True}

    app.include_router(omitted, prefix="/omitted")
    app.include_router(router_defaults, prefix="/router")
    app.include_router(include_override, prefix="/include", auto_head=True, auto_options=True)
    app.include_router(route_override, prefix="/route", auto_head=True, auto_options=True)
    client = TestClient(app)

    # Included routes ignore the app values and use the feature defaults.
    assert client.head("/omitted/items").status_code == 200
    assert client.options("/omitted/items").status_code == 405
    # Router constructor is the next nearest setting.
    assert client.head("/router/items").status_code == 405
    assert client.options("/router/items").status_code == 200
    # include_router is nearer than the router.
    assert client.head("/include/items").status_code == 200
    assert client.options("/include/items").status_code == 200
    # The route is nearer than include_router.
    assert client.head("/route/items").status_code == 405
    assert client.options("/route/items").status_code == 405


def test_repeated_inclusion_resolves_each_copy():
    router = APIRouter()

    @router.get("/items")
    def items():
        return {"ok": True}

    assert router.routes[0].auto_head is None
    app = FastAPI()
    app.include_router(router, prefix="/on", auto_head=True, auto_options=True)
    app.include_router(router, prefix="/off", auto_head=False, auto_options=False)
    app.include_router(router, prefix="/default")
    assert router.routes[0].auto_head is None
    assert router.routes[0].auto_options is None
    client = TestClient(app)
    assert client.head("/on/items").status_code == 200
    assert client.options("/on/items").status_code == 200
    assert client.head("/off/items").status_code == 405
    assert client.options("/off/items").status_code == 405
    assert client.head("/default/items").status_code == 200
    assert client.options("/default/items").status_code == 405


def test_nested_include_resolves_against_the_included_router():
    child = APIRouter()

    @child.get("/child")
    def child_route():
        return {"ok": True}

    parent = APIRouter(auto_head=False)

    @parent.get("/parent")
    def parent_route():
        return {"ok": True}

    parent.include_router(child)
    app = FastAPI()
    app.include_router(parent)
    client = TestClient(app)
    assert client.head("/child").status_code == 200
    assert client.head("/parent").status_code == 405


def test_parameterized_options_path_matches_openapi_and_head_validates():
    app = FastAPI()

    @app.get("/items/{item_id}", auto_options=True)
    def get_item(item_id: int, q: int):
        return {"item_id": item_id, "q": q}

    @app.post("/items/{item_id}")
    def post_item(item_id: int):
        return {"item_id": item_id}

    client = TestClient(app)
    options = client.options("/items/5")
    payload = options.json()
    schema = app.openapi()
    assert options.status_code == 200
    assert payload["path"] == "/items/{item_id}"
    assert payload["methods"] == ["GET", "HEAD", "POST", "OPTIONS"]
    assert payload["operations"] == {
        key: value
        for key, value in schema["paths"]["/items/{item_id}"].items()
        if key not in {"head", "options"}
    }
    invalid = _messages(app, "HEAD", "/items/nope")
    assert _status(invalid) == 422
    assert _body(invalid) == b""
    missing_query = _messages(app, "HEAD", "/items/5")
    assert _status(missing_query) == 422
    assert _body(missing_query) == b""


def test_mounted_app_stats_use_the_request_path():
    sub = FastAPI()

    @sub.get("/items", auto_options=True)
    def items():
        return {"ok": True}

    app = FastAPI()
    app.mount("/api", sub)
    middleware = ImplicitMethodTrackingMiddleware(app)
    client = TestClient(middleware)
    assert client.head("/api/items").status_code == 200
    assert client.head("/api/items").content == b""
    options = client.options("/api/items")
    assert options.status_code == 200
    assert options.json()["path"] == "/items"
    assert middleware.get_stats() == {
        "/api/items": {"head_hits": 2, "options_hits": 1},
    }


def test_docs_surface_and_openapi_stay_available():
    app = FastAPI()

    @app.get("/items", auto_options=True)
    def items():
        return {"ok": True}

    client = TestClient(app)
    assert client.get("/docs").status_code == 200
    assert client.get("/redoc").status_code == 200
    assert client.get("/openapi.json").status_code == 200
    assert client.head("/docs").status_code == 200
    assert client.head("/openapi.json").status_code == 200
    assert "FastAPI" in client.get("/docs").text
    schema = client.get("/openapi.json").json()
    assert schema["paths"]["/items"]["get"]["operationId"]
    options = client.options("/items")
    assert options.json()["operations"] == schema["paths"]["/items"]


def test_cors_preflight_is_not_the_metadata_response():
    app = FastAPI()
    app.add_middleware(
        CORSMiddleware,
        allow_origins=["http://example.com"],
        allow_methods=["GET", "POST"],
        allow_headers=["X-Test"],
    )

    @app.get("/items", auto_options=True)
    def items():
        return {"ok": True}

    client = TestClient(app)
    preflight = client.options(
        "/items",
        headers={
            "Origin": "http://example.com",
            "Access-Control-Request-Method": "GET",
        },
    )
    assert preflight.status_code == 200
    assert preflight.text == "OK"
    assert preflight.headers["access-control-allow-origin"] == "http://example.com"
    metadata = client.options("/items")
    assert metadata.status_code == 200
    assert metadata.json()["path"] == "/items"
    assert "access-control-allow-origin" not in metadata.headers


def test_trailing_slash_redirect_still_applies_to_head():
    app = FastAPI()

    @app.get("/items/")
    def items():
        return {"ok": True}

    client = TestClient(app, follow_redirects=False)
    response = client.head("/items")
    assert response.status_code == 307
    assert response.headers["location"].endswith("/items/")


def test_middleware_stats_track_implicit_hits_only():
    app = FastAPI()

    @app.get("/items/{item_id}", auto_options=True)
    def items(item_id: str):
        return {"item_id": item_id}

    @app.head("/explicit")
    def explicit_head():
        return {"ok": True}

    @app.get("/explicit", auto_options=False)
    def explicit_get():
        return {"ok": True}

    @app.websocket("/ws")
    async def ws():
        pass

    middleware = ImplicitMethodTrackingMiddleware(app)
    client = TestClient(middleware)
    assert client.head("/items/abc").status_code == 200
    assert client.head("/items/abc").status_code == 200
    assert client.options("/items/abc").status_code == 200
    assert client.head("/explicit").status_code == 200
    assert client.get("/items/abc").status_code == 200
    stats = middleware.get_stats()
    assert stats == {
        "/items/abc": {"head_hits": 2, "options_hits": 1},
    }
    stats["/items/abc"]["head_hits"] = 0
    assert middleware.get_stats()["/items/abc"]["head_hits"] == 2
    reset = middleware.reset_stats()
    assert reset["/items/abc"]["options_hits"] == 1
    assert middleware.get_stats() == {}
    assert client.options("/items/abc").status_code == 200
    assert middleware.get_stats() == {
        "/items/abc": {"head_hits": 0, "options_hits": 1},
    }

    async def websocket_receive():
        return {"type": "websocket.connect"}

    async def websocket_send(message):
        return None

    asyncio.run(
        middleware(
            {"type": "websocket", "path": "/ws", "headers": [], "query_string": b""},
            websocket_receive,
            websocket_send,
        )
    )
    assert "/ws" not in middleware.get_stats()
    assert IMPLICIT_METHOD_SCOPE_KEY == "fastapi.implicit_method"
