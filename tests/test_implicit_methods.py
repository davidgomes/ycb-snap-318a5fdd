import pytest
from starlette.websockets import WebSocketDisconnect

from fastapi import APIRouter, Depends, FastAPI, Response
from fastapi.middleware.cors import CORSMiddleware
from fastapi.middleware.methods import ImplicitMethodTrackingMiddleware
from fastapi.testclient import TestClient


def test_implicit_head_preserves_get_behavior_without_body():
    seen: list[str] = []

    def marker():
        seen.append("dep")
        return "ok"

    app = FastAPI()

    @app.get("/items/{item_id}", status_code=201)
    def read_item(item_id: int, q: int, _mark: str = Depends(marker)):
        return {"item_id": item_id, "q": q, "mark": _mark}

    client = TestClient(app)
    response = client.head("/items/3?q=4", headers={"x-test": "1"})
    assert response.status_code == 201
    assert response.content == b""
    assert response.headers["content-type"].startswith("application/json")
    assert seen == ["dep"]
    invalid = client.head("/items/nope?q=4")
    assert invalid.status_code == 422
    assert invalid.headers["content-type"].startswith("application/json")


def test_explicit_head_and_options_win():
    app = FastAPI(auto_options=True)

    @app.get("/items")
    def read_items():
        return {"source": "get"}

    @app.head("/items")
    def head_items(response: Response):
        response.headers["x-source"] = "head"
        return {"source": "head"}

    @app.options("/items")
    def options_items():
        return {"source": "options"}

    client = TestClient(app)
    head = client.head("/items")
    assert head.status_code == 200
    assert head.headers["x-source"] == "head"
    options = client.options("/items")
    assert options.json() == {"source": "options"}


def test_auto_head_app_default_and_route_override():
    disabled = FastAPI(auto_head=False)

    @disabled.get("/items")
    def read_items():
        return {"ok": True}

    client = TestClient(disabled)
    assert client.head("/items").status_code == 405
    assert client.get("/items").json() == {"ok": True}

    @disabled.get("/other", auto_head=True)
    def read_other():
        return {"ok": True}

    assert client.head("/other").status_code == 200
    assert client.head("/other").content == b""


def test_included_router_precedence_layers():
    app = FastAPI(auto_head=False, auto_options=True)

    route_level = APIRouter()

    @route_level.get("/route", auto_head=False, auto_options=True)
    def route_op():
        return {"layer": "route"}

    include_level = APIRouter(auto_head=True, auto_options=False)

    @include_level.get("/include")
    def include_op():
        return {"layer": "include"}

    router_level = APIRouter(auto_head=False, auto_options=True)

    @router_level.get("/router")
    def router_op():
        return {"layer": "router"}

    omitted = APIRouter()

    @omitted.get("/omitted")
    def omitted_op():
        return {"layer": "omitted"}

    app.include_router(route_level, auto_head=True, auto_options=False)
    app.include_router(include_level, auto_head=False, auto_options=True)
    app.include_router(router_level)
    app.include_router(omitted)

    client = TestClient(app)
    assert client.head("/route").status_code == 405
    assert client.options("/route").status_code == 200
    assert client.head("/include").status_code == 405
    assert client.options("/include").status_code == 200
    assert client.head("/router").status_code == 405
    assert client.options("/router").status_code == 200
    assert client.head("/omitted").status_code == 200
    assert client.options("/omitted").status_code == 405


def test_repeated_and_nested_inclusion():
    app = FastAPI(auto_head=False, auto_options=True)
    inner = APIRouter()

    @inner.get("/items")
    def read_items():
        return {"ok": True}

    app.include_router(inner, prefix="/off", auto_head=False, auto_options=False)
    app.include_router(inner, prefix="/on", auto_head=True, auto_options=True)

    mid = APIRouter()
    nested = APIRouter(auto_head=False, auto_options=False)

    @nested.get("/nested")
    def read_nested():
        return {"ok": True}

    mid.include_router(nested)
    app.include_router(mid, auto_head=True, auto_options=True)

    client = TestClient(app)
    assert client.head("/off/items").status_code == 405
    assert client.options("/off/items").status_code == 405
    assert client.head("/on/items").status_code == 200
    assert client.options("/on/items").status_code == 200
    assert client.head("/nested").status_code == 405
    assert client.options("/nested").status_code == 405


def test_options_payload_order_and_single_response():
    app = FastAPI()

    @app.trace("/items/{item_id}")
    def trace_item(item_id: int):
        return {"item_id": item_id}

    @app.post("/items/{item_id}", auto_options=True)
    def create_item(item_id: int):
        return {"item_id": item_id}

    @app.get("/items/{item_id}")
    def read_item(item_id: int):
        return {"item_id": item_id}

    @app.delete("/items/{item_id}")
    def delete_item(item_id: int):
        return {"item_id": item_id}

    @app.patch("/items/{item_id}")
    def patch_item(item_id: int):
        return {"item_id": item_id}

    @app.put("/items/{item_id}")
    def update_item(item_id: int):
        return {"item_id": item_id}

    client = TestClient(app)
    response = client.options("/items/7")
    assert response.status_code == 200
    body = response.json()
    assert body["path"] == "/items/{item_id}"
    assert body["methods"] == [
        "GET",
        "HEAD",
        "POST",
        "PUT",
        "PATCH",
        "DELETE",
        "OPTIONS",
        "TRACE",
    ]
    assert response.headers["allow"] == ", ".join(body["methods"])
    schema = app.openapi()["paths"]["/items/{item_id}"]
    assert body["operations"] == {
        key: value
        for key, value in schema.items()
        if key not in {"head", "options"}
    }
    assert "head" not in schema
    assert "options" not in schema
    assert set(body["operations"]) == {
        "get",
        "post",
        "put",
        "patch",
        "delete",
        "trace",
    }


def test_cors_preflight_and_docs_still_work():
    app = FastAPI()
    app.add_middleware(
        CORSMiddleware,
        allow_origins=["http://example.com"],
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
            "Origin": "http://example.com",
            "Access-Control-Request-Method": "GET",
        },
    )
    assert preflight.status_code == 200
    assert preflight.headers["access-control-allow-origin"] == "http://example.com"
    assert "access-control-allow-methods" in preflight.headers
    assert preflight.headers["content-type"].startswith("text/plain")
    plain = client.options("/items")
    assert plain.json()["path"] == "/items"
    docs = client.get("/docs")
    assert docs.status_code == 200
    assert "swagger" in docs.text.lower()
    openapi = client.get("/openapi.json")
    assert openapi.status_code == 200
    assert "head" not in openapi.json()["paths"]["/items"]
    assert "options" not in openapi.json()["paths"]["/items"]


def test_middleware_instance_stats_directly():
    app = FastAPI()

    @app.get("/items/{item_id}", auto_options=True)
    def read_item(item_id: int):
        return {"item_id": item_id}

    @app.get("/explicit")
    def explicit_get():
        return {"ok": True}

    @app.head("/explicit")
    def explicit_head():
        return {"ok": True}

    tracker = ImplicitMethodTrackingMiddleware(app)
    client = TestClient(tracker)
    assert client.head("/items/5").status_code == 200
    assert client.options("/items/5").status_code == 200
    assert client.get("/items/5").status_code == 200
    assert client.head("/explicit").status_code == 200
    with pytest.raises(WebSocketDisconnect):
        with client.websocket_connect("/nope"):
            pass

    stats = tracker.get_stats()
    assert stats == {"/items/{item_id}": {"head_hits": 1, "options_hits": 1}}
    stats["/items/{item_id}"]["head_hits"] = 99
    assert tracker.get_stats()["/items/{item_id}"]["head_hits"] == 1
    assert tracker.reset_stats() == {
        "/items/{item_id}": {"head_hits": 1, "options_hits": 1}
    }
    assert tracker.get_stats() == {}
