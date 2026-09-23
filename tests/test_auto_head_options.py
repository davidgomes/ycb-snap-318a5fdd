import inspect
from typing import Annotated, get_args, get_origin

import anyio
import pytest
from annotated_doc import Doc
from fastapi import APIRouter, Depends, FastAPI, HTTPException, Response, WebSocket
from fastapi.middleware.cors import CORSMiddleware
from fastapi.middleware.methods import ImplicitMethodTrackingMiddleware
from fastapi.routing import APIRoute
from fastapi.testclient import TestClient


def make_router(**router_kwargs) -> APIRouter:
    router = APIRouter(**router_kwargs)

    @router.get("/default")
    def default():
        return {"ok": True}

    @router.get("/on", auto_head=True, auto_options=True)
    def on():
        return {"ok": True}

    @router.get("/off", auto_head=False, auto_options=False)
    def off():
        return {"ok": True}  # pragma: no cover

    return router


def head_status(client: TestClient, path: str) -> int:
    return client.head(path).status_code


def options_status(client: TestClient, path: str) -> int:
    return client.options(path).status_code


def test_implicit_head_preserves_get_behavior():
    calls = []

    def dep(response: Response, token: str | None = None):
        calls.append(token)
        if token == "bad":
            raise HTTPException(status_code=403, headers={"X-Reason": "bad-token"})
        response.headers["X-Dep"] = "yes"

    app = FastAPI()

    @app.get("/items/{item_id}", status_code=201, dependencies=[Depends(dep)])
    def read_item(item_id: int, response: Response):
        response.headers["X-Item"] = str(item_id)
        return {"item_id": item_id}

    client = TestClient(app)
    get_response = client.get("/items/3")
    response = client.head("/items/3")
    assert response.status_code == 201
    assert response.content == b""
    assert response.headers["x-item"] == "3"
    assert response.headers["x-dep"] == "yes"
    assert response.headers["content-type"] == "application/json"
    assert response.headers["content-length"] == get_response.headers["content-length"]

    response = client.head("/items/abc")
    assert response.status_code == 422
    assert response.content == b""

    response = client.head("/items/3", params={"token": "bad"})
    assert response.status_code == 403
    assert response.headers["x-reason"] == "bad-token"
    assert response.content == b""
    assert calls == [None, None, None, "bad"]
    assert client.get("/items/abc").status_code == 422
    assert client.get("/items/3", params={"token": "bad"}).status_code == 403


def test_implicit_head_streaming_response_has_no_body():
    app = FastAPI()

    @app.get("/stream")
    def stream():
        yield {"n": 1}
        yield {"n": 2}

    client = TestClient(app)
    assert client.get("/stream").text.count("\n") == 2
    response = client.head("/stream")
    assert response.status_code == 200
    assert response.content == b""
    assert response.headers["content-type"] == "application/jsonl"


def test_defaults():
    app = FastAPI()

    @app.get("/items")
    def read_items():
        return []

    @app.post("/things")
    def create_thing():
        return {}  # pragma: no cover

    client = TestClient(app)
    assert head_status(client, "/items") == 200
    assert options_status(client, "/items") == 405
    # auto_head only applies to GET operations
    assert head_status(client, "/things") == 405


def test_direct_app_routes_use_app_values_as_outermost_defaults():
    app = FastAPI(auto_head=False, auto_options=True)

    @app.get("/default")
    def default():
        return {}  # pragma: no cover

    @app.get("/route-on", auto_head=True)
    def route_on():
        return {}

    @app.get("/route-off", auto_options=False)
    def route_off():
        return {}  # pragma: no cover

    def endpoint():
        return {}

    app.add_api_route("/added", endpoint, auto_head=True, auto_options=False)

    @app.api_route(
        "/api-route", methods=["GET", "POST"], auto_head=True, include_in_schema=False
    )
    def api_route():
        return {}

    client = TestClient(app)
    assert head_status(client, "/default") == 405
    assert options_status(client, "/default") == 200
    assert head_status(client, "/route-on") == 200
    assert head_status(client, "/route-off") == 405
    assert options_status(client, "/route-off") == 405
    assert head_status(client, "/added") == 200
    assert options_status(client, "/added") == 405
    assert head_status(client, "/api-route") == 200
    assert client.options("/api-route").json()["methods"] == [
        "GET",
        "HEAD",
        "POST",
        "OPTIONS",
    ]


def test_routes_parameter_resolves_against_app():
    def endpoint():
        return {}

    app = FastAPI(
        routes=[
            APIRoute("/plain", endpoint),
            APIRoute("/explicit", endpoint, auto_head=False),
        ],
        auto_options=True,
    )
    client = TestClient(app)
    assert head_status(client, "/plain") == 200
    assert options_status(client, "/plain") == 200
    assert head_status(client, "/explicit") == 405


@pytest.mark.parametrize(
    ("app_kwargs", "router_kwargs", "include_kwargs", "expected"),
    [
        # route omitted, include omitted, router omitted: app is the outermost default
        ({}, {}, {}, {"head": 200, "options": 405}),
        (
            {"auto_head": False, "auto_options": True},
            {},
            {},
            {"head": 405, "options": 200},
        ),
        # router layer beats the app
        (
            {"auto_head": False, "auto_options": True},
            {"auto_head": True, "auto_options": False},
            {},
            {"head": 200, "options": 405},
        ),
        # include layer beats the router and the app
        (
            {"auto_head": True, "auto_options": False},
            {"auto_head": True, "auto_options": False},
            {"auto_head": False, "auto_options": True},
            {"head": 405, "options": 200},
        ),
        (
            {},
            {"auto_head": False, "auto_options": True},
            {"auto_head": True, "auto_options": False},
            {"head": 200, "options": 405},
        ),
    ],
)
def test_included_router_precedence(
    app_kwargs, router_kwargs, include_kwargs, expected
):
    app = FastAPI(**app_kwargs)
    app.include_router(make_router(**router_kwargs), **include_kwargs)
    client = TestClient(app)
    assert head_status(client, "/default") == expected["head"]
    assert options_status(client, "/default") == expected["options"]
    # the route layer beats every other layer
    assert head_status(client, "/on") == 200
    assert options_status(client, "/on") == 200
    assert head_status(client, "/off") == 405
    assert options_status(client, "/off") == 405


def test_included_router_value_is_not_frozen_into_route():
    router = make_router(auto_head=False)
    # The route keeps its own (omitted) value, not the router's
    route = next(r for r in router.routes if r.path == "/default")
    assert isinstance(route, APIRoute)
    app = FastAPI()
    app.include_router(router, auto_head=True)
    assert head_status(TestClient(app), "/default") == 200


def test_repeated_inclusion():
    router = make_router()
    app = FastAPI()
    app.include_router(router, prefix="/a", auto_options=True)
    app.include_router(router, prefix="/b", auto_head=False)
    app.include_router(router, prefix="/c")
    client = TestClient(app)
    assert head_status(client, "/a/default") == 200
    assert options_status(client, "/a/default") == 200
    assert head_status(client, "/b/default") == 405
    assert options_status(client, "/b/default") == 405
    assert head_status(client, "/c/default") == 200
    assert options_status(client, "/c/default") == 405
    for prefix in ("/a", "/b", "/c"):
        assert head_status(client, f"{prefix}/on") == 200
        assert head_status(client, f"{prefix}/off") == 405
    # The included router itself is unchanged
    assert len(router.routes) == 3


def test_nested_inclusion_uses_nearest_setting():
    inner = make_router()
    middle = APIRouter(auto_options=True)
    middle.include_router(inner, prefix="/inner")
    outer = APIRouter(auto_head=False)
    outer.include_router(middle, prefix="/middle")
    outer.include_router(inner, prefix="/direct", auto_head=True)
    app = FastAPI(auto_options=False)
    app.include_router(outer, prefix="/outer")
    client = TestClient(app)
    assert head_status(client, "/outer/middle/inner/default") == 405
    assert options_status(client, "/outer/middle/inner/default") == 200
    assert head_status(client, "/outer/direct/default") == 200
    assert options_status(client, "/outer/direct/default") == 405

    inner_override = make_router()
    middle_override = APIRouter(auto_options=True)
    middle_override.include_router(inner_override, auto_options=False)
    app = FastAPI()
    app.include_router(middle_override)
    assert options_status(TestClient(app), "/default") == 405


def test_router_served_directly_uses_its_own_values():
    client = TestClient(make_router())
    assert options_status(client, "/default") == 405
    assert options_status(client, "/on") == 200

    router = make_router(auto_head=False, auto_options=True)
    client = TestClient(router)
    assert head_status(client, "/default") == 405
    response = client.options("/default")
    assert response.status_code == 200
    assert response.json() == {
        "path": "/default",
        "methods": ["GET", "OPTIONS"],
        "operations": {
            "get": {
                "summary": "Default",
                "operationId": "default_default_get",
                "responses": {
                    "200": {
                        "description": "Successful Response",
                        "content": {"application/json": {"schema": {}}},
                    }
                },
            }
        },
    }


@pytest.mark.parametrize("explicit_first", [True, False])
def test_explicit_head_and_options_win(explicit_first: bool):
    app = FastAPI(auto_options=True)

    def explicit_head(response: Response):
        response.headers["X-Explicit"] = "head"

    def explicit_options():
        return {"explicit": True}

    def read():
        return {"get": True}  # pragma: no cover

    if explicit_first:
        app.head("/items")(explicit_head)
        app.options("/items")(explicit_options)
        app.get("/items")(read)
    else:
        app.get("/items")(read)
        app.head("/items")(explicit_head)
        app.options("/items")(explicit_options)

    client = TestClient(app)
    response = client.head("/items")
    assert response.status_code == 200
    assert response.headers["x-explicit"] == "head"
    response = client.options("/items")
    assert response.json() == {"explicit": True}
    assert "allow" not in response.headers


def test_explicit_head_is_listed_in_implicit_options():
    app = FastAPI(auto_options=True, auto_head=False)

    @app.get("/items")
    def read():
        return {}  # pragma: no cover

    @app.head("/items")
    def head():
        return None  # pragma: no cover

    response = TestClient(app).options("/items")
    assert response.json()["methods"] == ["GET", "HEAD", "OPTIONS"]
    assert set(response.json()["operations"]) == {"get"}


def test_method_ordering_and_allow_header():
    app = FastAPI(auto_options=True)

    def endpoint():
        return {}  # pragma: no cover

    for method in ["TRACE", "DELETE", "PATCH", "PUT", "POST"]:
        app.add_api_route("/items", endpoint, methods=[method])
    app.add_api_route(
        "/items", endpoint, methods=["PURGE", "GET"], include_in_schema=False
    )
    response = TestClient(app).options("/items")
    methods = [
        "GET",
        "HEAD",
        "POST",
        "PUT",
        "PATCH",
        "DELETE",
        "OPTIONS",
        "TRACE",
        "PURGE",
    ]
    assert response.status_code == 200
    assert response.headers["content-type"] == "application/json"
    assert response.json()["methods"] == methods
    assert response.headers["allow"] == ", ".join(methods)


def test_head_not_listed_when_disabled():
    app = FastAPI(auto_options=True)

    @app.get("/items", auto_head=False)
    def read():
        return {}  # pragma: no cover

    @app.delete("/items")
    def delete():
        return {}  # pragma: no cover

    response = TestClient(app).options("/items")
    assert response.json()["methods"] == ["GET", "DELETE", "OPTIONS"]
    assert response.headers["allow"] == "GET, DELETE, OPTIONS"


def test_one_options_response_per_path_when_any_operation_enables_it():
    app = FastAPI()

    @app.get("/items/{item_id}")
    def read(item_id: int):
        return {}  # pragma: no cover

    @app.put("/items/{item_id}", auto_options=True)
    def replace(item_id: int):
        return {}  # pragma: no cover

    @app.get("/other")
    def other():
        return {}  # pragma: no cover

    client = TestClient(app)
    response = client.options("/items/1")
    assert response.status_code == 200
    assert response.json()["path"] == "/items/{item_id}"
    assert response.json()["methods"] == ["GET", "HEAD", "PUT", "OPTIONS"]
    assert options_status(client, "/other") == 405


def test_options_operations_match_openapi():
    app = FastAPI(auto_options=True)
    router = APIRouter(prefix="/users", tags=["users"])

    @router.get("/{user_id}", response_model=dict[str, int])
    def read_user(user_id: int):
        return {"user_id": user_id}  # pragma: no cover

    @router.patch("/{user_id}", deprecated=True)
    def update_user(user_id: int, name: str):
        return {}  # pragma: no cover

    @router.get("/{user_id}/hidden", include_in_schema=False)
    def hidden(user_id: int):
        return {}  # pragma: no cover

    app.include_router(router, prefix="/v1")
    client = TestClient(app)
    openapi = client.get("/openapi.json").json()
    path_item = openapi["paths"]["/v1/users/{user_id}"]
    assert set(path_item) == {"get", "patch"}

    response = client.options("/v1/users/5")
    assert response.json() == {
        "path": "/v1/users/{user_id}",
        "methods": ["GET", "HEAD", "PATCH", "OPTIONS"],
        "operations": path_item,
    }
    response = client.options("/v1/users/5/hidden")
    assert response.json() == {
        "path": "/v1/users/{user_id}/hidden",
        "methods": ["GET", "HEAD", "OPTIONS"],
        "operations": {},
    }


def test_options_operations_exclude_explicit_head_and_options():
    app = FastAPI(auto_options=True)

    @app.get("/items")
    def read():
        return {}  # pragma: no cover

    @app.head("/items")
    def head():
        return None  # pragma: no cover

    client = TestClient(app)
    openapi_paths = client.get("/openapi.json").json()["paths"]["/items"]
    assert set(openapi_paths) == {"get", "head"}
    assert client.options("/items").json()["operations"] == {
        "get": openapi_paths["get"]
    }


def test_openapi_output_is_unchanged():
    def build(**kwargs) -> dict:
        app = FastAPI(**kwargs)

        @app.get("/items/{item_id}")
        def read(item_id: int):
            return {}  # pragma: no cover

        @app.post("/items/{item_id}")
        def create(item_id: int):
            return {}  # pragma: no cover

        return app.openapi()

    enabled = build(auto_head=True, auto_options=True)
    disabled = build(auto_head=False, auto_options=False)
    assert enabled == disabled
    assert set(enabled["paths"]["/items/{item_id}"]) == {"get", "post"}


def test_cors_preflight_is_handled_by_middleware():
    app = FastAPI(auto_options=True)
    app.add_middleware(
        CORSMiddleware,
        allow_origins=["https://example.com"],
        allow_methods=["GET"],
    )

    @app.get("/items")
    def read():
        return {}  # pragma: no cover

    client = TestClient(app)
    response = client.options(
        "/items",
        headers={
            "Origin": "https://example.com",
            "Access-Control-Request-Method": "GET",
        },
    )
    assert response.status_code == 200
    assert response.text == "OK"
    assert response.headers["access-control-allow-origin"] == "https://example.com"
    assert "GET" in response.headers["access-control-allow-methods"]

    response = client.options("/items", headers={"Origin": "https://example.com"})
    assert response.status_code == 200
    assert response.json()["methods"] == ["GET", "HEAD", "OPTIONS"]
    assert response.headers["access-control-allow-origin"] == "https://example.com"


def test_docs_surface_is_unaffected():
    app = FastAPI(auto_options=True)

    @app.get("/items")
    def read():
        return {}  # pragma: no cover

    client = TestClient(app)
    assert client.get("/docs").status_code == 200
    assert client.get("/redoc").status_code == 200
    assert client.get("/openapi.json").status_code == 200
    assert client.head("/openapi.json").status_code == 200
    assert client.options("/docs").status_code == 405
    assert client.options("/openapi.json").status_code == 405
    assert client.head("/missing").status_code == 404
    assert client.options("/missing").status_code == 404


def _doc_for(func, name: str) -> str:
    annotation = inspect.signature(func).parameters[name].annotation
    assert get_origin(annotation) is Annotated
    docs = [arg for arg in get_args(annotation) if isinstance(arg, Doc)]
    assert len(docs) == 1
    return docs[0].documentation


@pytest.mark.parametrize(
    "func",
    [
        FastAPI.__init__,
        FastAPI.add_api_route,
        FastAPI.api_route,
        FastAPI.include_router,
        APIRouter.__init__,
        APIRouter.add_api_route,
        APIRouter.api_route,
        APIRouter.include_router,
        APIRoute.__init__,
        *[
            getattr(cls, method)
            for cls in (FastAPI, APIRouter)
            for method in (
                "get",
                "put",
                "post",
                "delete",
                "options",
                "head",
                "patch",
                "trace",
            )
        ],
    ],
)
def test_public_signatures_document_new_parameters(func):
    assert "HEAD" in _doc_for(func, "auto_head")
    assert "OPTIONS" in _doc_for(func, "auto_options")


def test_tracking_middleware_counts_implicit_hits_only():
    app = FastAPI(auto_options=True)
    router = APIRouter(prefix="/users")

    @router.get("/{user_id}")
    def read_user(user_id: int):
        return {}

    @app.get("/items")
    def read_items():
        return {}

    @app.get("/explicit")
    def read_explicit():
        return {}  # pragma: no cover

    @app.head("/explicit")
    def head_explicit():
        return None

    @app.options("/explicit")
    def options_explicit():
        return {}

    @app.websocket("/ws")
    async def ws(websocket: WebSocket):
        await websocket.accept()
        await websocket.send_text("hi")
        await websocket.close()

    app.include_router(router, prefix="/v1")
    tracker = ImplicitMethodTrackingMiddleware(app)
    with TestClient(tracker) as client:
        assert tracker.get_stats() == {}
        client.head("/items")
        client.head("/items")
        client.options("/items")
        client.head("/v1/users/1")
        client.head("/v1/users/2")
        client.head("/v1/users/abc")
        client.get("/items")
        client.head("/explicit")
        client.options("/explicit")
        client.head("/missing")
        with client.websocket_connect("/ws") as websocket:
            assert websocket.receive_text() == "hi"

        stats = tracker.get_stats()
        assert stats == {
            "/items": {"head_hits": 2, "options_hits": 1},
            "/v1/users/{user_id}": {"head_hits": 3, "options_hits": 0},
        }
        stats["/items"]["head_hits"] = 100
        assert tracker.get_stats()["/items"]["head_hits"] == 2

        reset = tracker.reset_stats()
        assert reset == {
            "/items": {"head_hits": 2, "options_hits": 1},
            "/v1/users/{user_id}": {"head_hits": 3, "options_hits": 0},
        }
        assert tracker.get_stats() == {}
        reset["/items"]["head_hits"] = 0
        client.options("/items")
        assert tracker.get_stats() == {"/items": {"head_hits": 0, "options_hits": 1}}


def test_tracking_middleware_via_add_middleware_and_mounted_app():
    sub_app = FastAPI(auto_options=True)

    @sub_app.get("/items")
    def read_sub_items():
        return {}

    app = FastAPI()

    @app.get("/items")
    def read_items():
        return {}

    app.mount("/sub", sub_app)
    app.add_middleware(ImplicitMethodTrackingMiddleware)
    client = TestClient(app)
    client.head("/items")
    client.head("/sub/items")
    response = client.options("/sub/items")
    assert response.json()["path"] == "/items"
    assert response.json()["operations"] == sub_app.openapi()["paths"]["/items"]

    middleware = app.middleware_stack
    while not isinstance(middleware, ImplicitMethodTrackingMiddleware):
        middleware = middleware.app
    assert middleware.get_stats() == {
        "/items": {"head_hits": 1, "options_hits": 0},
        "/sub/items": {"head_hits": 1, "options_hits": 1},
    }


def test_tracking_middleware_ignores_non_http_scopes():
    received = []

    async def inner(scope, receive, send):
        received.append(dict(scope))

    async def run() -> None:
        await tracker({"type": "websocket", "path": "/"}, None, None)
        await tracker({"type": "lifespan"}, None, None)

    tracker = ImplicitMethodTrackingMiddleware(inner)
    anyio.run(run)
    assert received == [{"type": "websocket", "path": "/"}, {"type": "lifespan"}]
    assert tracker.get_stats() == {}
