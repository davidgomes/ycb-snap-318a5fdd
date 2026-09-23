import anyio
from fastapi import FastAPI, WebSocket
from fastapi.middleware.cors import CORSMiddleware
from fastapi.middleware.methods import ImplicitMethodTrackingMiddleware
from fastapi.testclient import TestClient
from starlette.types import Message, Receive, Scope, Send


def create_app() -> FastAPI:
    app = FastAPI(auto_options=True)

    @app.get("/items/{item_id}")
    def read_item(item_id: str):
        return {"item_id": item_id}

    @app.get("/explicit")
    def read_explicit():
        return {}  # pragma: no cover

    @app.head("/explicit")
    def head_explicit():
        return None

    @app.options("/explicit")
    def options_explicit():
        return {}

    @app.post("/create")
    def create():
        return {}  # pragma: no cover

    @app.websocket("/ws")
    async def websocket_endpoint(websocket: WebSocket):
        await websocket.accept()
        await websocket.send_text("hello")
        await websocket.close()

    return app


def test_tracks_only_implicit_hits():
    tracked_app = ImplicitMethodTrackingMiddleware(create_app())
    client = TestClient(tracked_app)

    assert client.head("/items/foo").status_code == 200
    assert client.head("/items/foo").status_code == 200
    assert client.options("/items/foo").status_code == 200
    assert client.head("/items/bar").status_code == 200
    assert client.get("/items/foo").status_code == 200
    assert client.head("/explicit").status_code == 200
    assert client.options("/explicit").status_code == 200
    assert client.head("/create").status_code == 405
    assert client.head("/missing").status_code == 404
    assert client.options("/missing").status_code == 404

    assert tracked_app.get_stats() == {
        "/items/foo": {"head_hits": 2, "options_hits": 1},
        "/items/bar": {"head_hits": 1, "options_hits": 0},
    }


def test_implicit_hits_are_tracked_on_errors():
    app = FastAPI()

    @app.get("/items/{item_id}")
    def read_item(item_id: int):
        return {"item_id": item_id}  # pragma: no cover

    tracked_app = ImplicitMethodTrackingMiddleware(app)
    client = TestClient(tracked_app)
    assert client.head("/items/abc").status_code == 422
    assert tracked_app.get_stats() == {
        "/items/abc": {"head_hits": 1, "options_hits": 0}
    }


def test_get_stats_returns_a_deep_copy():
    tracked_app = ImplicitMethodTrackingMiddleware(create_app())
    client = TestClient(tracked_app)
    client.head("/items/foo")

    stats = tracked_app.get_stats()
    stats["/items/foo"]["head_hits"] = 100
    stats["/other"] = {"head_hits": 1, "options_hits": 1}

    assert tracked_app.get_stats() == {
        "/items/foo": {"head_hits": 1, "options_hits": 0}
    }


def test_reset_stats():
    tracked_app = ImplicitMethodTrackingMiddleware(create_app())
    client = TestClient(tracked_app)
    client.head("/items/foo")
    client.options("/items/foo")

    stats = tracked_app.reset_stats()
    assert stats == {"/items/foo": {"head_hits": 1, "options_hits": 1}}
    assert tracked_app.get_stats() == {}
    stats["/items/foo"]["head_hits"] = 100
    assert tracked_app.reset_stats() == {}

    client.options("/items/foo")
    assert tracked_app.get_stats() == {
        "/items/foo": {"head_hits": 0, "options_hits": 1}
    }


def test_non_http_scopes_are_ignored():
    tracked_app = ImplicitMethodTrackingMiddleware(create_app())
    with TestClient(tracked_app) as client:
        with client.websocket_connect("/ws") as websocket:
            assert websocket.receive_text() == "hello"
        client.head("/items/foo")
    assert tracked_app.get_stats() == {
        "/items/foo": {"head_hits": 1, "options_hits": 0}
    }

    received_scopes: list[Scope] = []

    async def inner_app(scope: Scope, receive: Receive, send: Send) -> None:
        received_scopes.append(scope)

    async def receive() -> Message:
        return {}  # pragma: no cover

    async def send(message: Message) -> None:
        pass  # pragma: no cover

    middleware = ImplicitMethodTrackingMiddleware(inner_app)
    for scope_type in ("lifespan", "websocket"):
        anyio.run(middleware, {"type": scope_type, "path": "/"}, receive, send)
    assert all(
        "fastapi_implicit_method_trackers" not in scope for scope in received_scopes
    )
    assert len(received_scopes) == 2


def test_cors_preflight_is_not_tracked():
    app = create_app()
    app.add_middleware(
        CORSMiddleware, allow_origins=["https://example.com"], allow_methods=["*"]
    )
    tracked_app = ImplicitMethodTrackingMiddleware(app)
    client = TestClient(tracked_app)

    preflight = client.options(
        "/items/foo",
        headers={
            "Origin": "https://example.com",
            "Access-Control-Request-Method": "GET",
        },
    )
    assert preflight.text == "OK"
    assert tracked_app.get_stats() == {}

    client.options("/items/foo", headers={"Origin": "https://example.com"})
    assert tracked_app.get_stats() == {
        "/items/foo": {"head_hits": 0, "options_hits": 1}
    }


def test_full_path_for_mounted_application():
    sub_app = create_app()
    tracked_sub_app = ImplicitMethodTrackingMiddleware(sub_app)
    app = FastAPI()
    app.mount("/sub", tracked_sub_app)
    tracked_app = ImplicitMethodTrackingMiddleware(app)
    client = TestClient(tracked_app)

    client.head("/sub/items/foo")
    client.options("/sub/items/foo")
    expected = {"/sub/items/foo": {"head_hits": 1, "options_hits": 1}}
    assert tracked_app.get_stats() == expected
    assert tracked_sub_app.get_stats() == expected


def test_full_path_includes_root_path():
    app = create_app()
    tracked_app = ImplicitMethodTrackingMiddleware(app)
    client = TestClient(tracked_app, root_path="/api")

    assert client.head("/items/foo").status_code == 200
    assert tracked_app.get_stats() == {
        "/api/items/foo": {"head_hits": 1, "options_hits": 0}
    }


def test_add_middleware():
    app = create_app()
    app.add_middleware(ImplicitMethodTrackingMiddleware)
    client = TestClient(app)
    client.head("/items/foo")

    assert app.middleware_stack is not None
    middleware = app.middleware_stack
    while not isinstance(middleware, ImplicitMethodTrackingMiddleware):
        middleware = middleware.app  # type: ignore[attr-defined]
    assert middleware.get_stats() == {"/items/foo": {"head_hits": 1, "options_hits": 0}}
