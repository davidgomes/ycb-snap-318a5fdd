import inspect
from typing import Annotated, get_type_hints

import anyio
import pytest
from fastapi import APIRouter, Depends, FastAPI, Header, HTTPException, Response
from fastapi.middleware.cors import CORSMiddleware
from fastapi.middleware.methods import ImplicitMethodTrackingMiddleware
from fastapi.testclient import TestClient


def test_implicit_head_preserves_get_behavior():
    app = FastAPI()
    calls = []

    def dep(x_token: Annotated[str, Header()]):
        calls.append(x_token)
        if x_token != "ok":
            raise HTTPException(403, headers={"X-Denied": "1"})

    @app.get("/items/{item_id}", status_code=201, dependencies=[Depends(dep)])
    def read(item_id: int, response: Response):
        response.headers["X-Item"] = str(item_id)
        return {"item_id": item_id}

    client = TestClient(app)
    r = client.head("/items/3", headers={"X-Token": "ok"})
    assert r.status_code == 201
    assert r.headers["x-item"] == "3"
    assert r.content == b""
    assert calls == ["ok"]
    r = client.head("/items/3", headers={"X-Token": "no"})
    assert r.status_code == 403
    assert r.headers["x-denied"] == "1"
    assert r.content == b""
    assert client.head("/items/abc", headers={"X-Token": "ok"}).status_code == 422
    assert client.head("/items/3").status_code == 422


def test_head_disabled_and_explicit_wins():
    app = FastAPI()

    @app.get("/off", auto_head=False)
    def off():
        return 1

    @app.get("/both")
    def both_get():
        return "get"

    @app.head("/both")
    def both_head(response: Response):
        response.headers["X-Explicit"] = "1"

    @app.post("/post-first")
    def pf():
        return 1

    @app.get("/post-first")
    def gf(response: Response):
        response.headers["X-Get"] = "1"

    client = TestClient(app)
    assert client.head("/off").status_code == 405
    assert client.head("/both").headers["x-explicit"] == "1"
    assert client.head("/post-first").headers["x-get"] == "1"
    assert client.options("/both").status_code == 405


def test_implicit_options_response_and_openapi():
    app = FastAPI(auto_options=True)

    @app.delete("/things/{tid}")
    def d(tid: int):
        return None

    @app.get("/things/{tid}")
    def g(tid: int):
        return None

    @app.post("/things/{tid}")
    def p(tid: int):
        return None

    client = TestClient(app)
    r = client.options("/things/1")
    assert r.status_code == 200
    assert r.headers["allow"] == "GET, HEAD, POST, DELETE, OPTIONS"
    body = r.json()
    openapi = client.get("/openapi.json").json()
    assert body["path"] == "/things/{tid}"
    assert body["methods"] == ["GET", "HEAD", "POST", "DELETE", "OPTIONS"]
    assert body["operations"] == openapi["paths"]["/things/{tid}"]
    assert set(openapi["paths"]["/things/{tid}"]) == {"get", "post", "delete"}


def test_options_enabled_by_any_operation_and_explicit_wins():
    app = FastAPI()

    @app.get("/a")
    def a_get():
        return 1

    @app.put("/a", auto_options=True)
    def a_put():
        return 1

    @app.get("/b", auto_options=True)
    def b_get():
        return 1

    @app.options("/b")
    def b_options():
        return "explicit"

    client = TestClient(app)
    assert client.options("/a").json()["methods"] == ["GET", "HEAD", "PUT", "OPTIONS"]
    assert client.options("/b").json() == "explicit"


def _router_app(route_kw, include_kw, router_kw, app_kw):
    app = FastAPI(**app_kw)
    router = APIRouter(**router_kw)

    @router.get("/x", **route_kw)
    def x():
        return 1

    app.include_router(router, prefix="/r", **include_kw)
    return TestClient(app)


@pytest.mark.parametrize(
    "route_kw,include_kw,router_kw,app_kw,expected",
    [
        ({}, {}, {}, {}, 200),
        ({}, {}, {}, {"auto_head": False}, 405),
        ({}, {}, {"auto_head": False}, {"auto_head": True}, 405),
        ({}, {"auto_head": True}, {"auto_head": False}, {}, 200),
        ({}, {"auto_head": False}, {"auto_head": True}, {}, 405),
        ({"auto_head": True}, {"auto_head": False}, {"auto_head": False}, {}, 200),
        ({"auto_head": False}, {}, {}, {"auto_head": True}, 405),
    ],
)
def test_head_precedence(route_kw, include_kw, router_kw, app_kw, expected):
    client = _router_app(route_kw, include_kw, router_kw, app_kw)
    assert client.head("/r/x").status_code == expected


@pytest.mark.parametrize(
    "route_kw,include_kw,router_kw,app_kw,expected",
    [
        ({}, {}, {}, {}, 405),
        ({}, {}, {}, {"auto_options": True}, 200),
        ({}, {}, {"auto_options": True}, {}, 200),
        ({}, {"auto_options": False}, {"auto_options": True}, {}, 405),
        ({"auto_options": True}, {"auto_options": False}, {}, {}, 200),
        ({"auto_options": False}, {}, {}, {"auto_options": True}, 405),
    ],
)
def test_options_precedence(route_kw, include_kw, router_kw, app_kw, expected):
    client = _router_app(route_kw, include_kw, router_kw, app_kw)
    assert client.options("/r/x").status_code == expected


def test_direct_app_route_uses_app_default():
    app = FastAPI(auto_head=False, auto_options=True)

    @app.get("/d")
    def d():
        return 1

    @app.api_route("/e", methods=["GET"], auto_head=True, auto_options=False)
    def e():
        return 1

    def f():
        return 1

    app.add_api_route("/f", f, auto_head=True)
    client = TestClient(app)
    assert client.head("/d").status_code == 405
    assert client.options("/d").json()["methods"] == ["GET", "OPTIONS"]
    assert client.head("/e").status_code == 200
    assert client.options("/e").status_code == 405
    assert client.head("/f").status_code == 200


def test_repeated_and_nested_inclusion():
    inner = APIRouter(auto_head=False)

    @inner.get("/i")
    def i():
        return 1

    outer = APIRouter()
    outer.include_router(inner, prefix="/one")
    outer.include_router(inner, prefix="/two", auto_head=True)
    app = FastAPI()
    app.include_router(outer, prefix="/o")
    app.include_router(outer, prefix="/p", auto_head=True)
    client = TestClient(app)
    assert client.head("/o/one/i").status_code == 405
    assert client.head("/o/two/i").status_code == 200
    # nearest non-omitted setting wins, so the inner router value is kept
    assert client.head("/p/one/i").status_code == 405
    assert client.head("/p/two/i").status_code == 200


def test_cors_preflight_unaffected():
    app = FastAPI(auto_options=True)
    app.add_middleware(CORSMiddleware, allow_origins=["https://a.example"])

    @app.get("/c")
    def c():
        return 1

    client = TestClient(app)
    r = client.options(
        "/c",
        headers={
            "Origin": "https://a.example",
            "Access-Control-Request-Method": "GET",
        },
    )
    assert r.status_code == 200
    assert r.headers["access-control-allow-origin"] == "https://a.example"
    assert r.text == "OK"
    assert client.options("/c").json()["path"] == "/c"


def test_tracking_middleware():
    app = FastAPI(auto_options=True)
    router = APIRouter(prefix="/r")

    @router.get("/t/{x}")
    def t(x: int):
        return 1

    @router.head("/h")
    def h():
        return None

    @router.options("/h")
    def ho():
        return None

    app.include_router(router)
    tracked = ImplicitMethodTrackingMiddleware(app)
    client = TestClient(tracked)
    client.head("/r/t/1")
    client.head("/r/t/2")
    client.options("/r/t/1")
    client.get("/r/t/1")
    client.head("/r/h")
    client.options("/r/h")
    stats = tracked.get_stats()
    assert stats == {"/r/t/{x}": {"head_hits": 2, "options_hits": 1}}
    stats["/r/t/{x}"]["head_hits"] = 99
    assert tracked.get_stats()["/r/t/{x}"]["head_hits"] == 2
    tracked.reset_stats()
    assert tracked.get_stats() == {}


def test_tracking_middleware_ignores_non_http():
    seen = []

    async def inner(scope, receive, send):
        seen.append(dict(scope))

    tracked = ImplicitMethodTrackingMiddleware(inner)
    anyio.run(tracked, {"type": "lifespan"}, None, None)
    assert "fastapi.implicit_method_stats" not in seen[0]
    assert tracked.get_stats() == {}


@pytest.mark.parametrize(
    "func",
    [
        FastAPI.__init__,
        APIRouter.__init__,
        FastAPI.get,
        APIRouter.trace,
        FastAPI.api_route,
        APIRouter.api_route,
        FastAPI.add_api_route,
        APIRouter.add_api_route,
        FastAPI.include_router,
        APIRouter.include_router,
    ],
)
def test_signatures_documented(func):
    hints = get_type_hints(func, include_extras=True)
    for name in ("auto_head", "auto_options"):
        assert name in inspect.signature(func).parameters
        assert hints[name].__metadata__
