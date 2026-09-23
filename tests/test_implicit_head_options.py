from collections.abc import AsyncIterable, Iterable

import pytest
from fastapi import (
    APIRouter,
    BackgroundTasks,
    Depends,
    FastAPI,
    Header,
    HTTPException,
    Request,
    Response,
)
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import EventSourceResponse, FileResponse, StreamingResponse
from fastapi.testclient import TestClient
from pydantic import BaseModel
from starlette.responses import PlainTextResponse


class Item(BaseModel):
    name: str


def assert_implicit_options(app: FastAPI, path: str, url: str, methods: list[str]):
    response = TestClient(app).options(url)
    assert response.status_code == 200, response.text
    assert response.headers["allow"] == ", ".join(methods)
    expected_operations = {
        key: value
        for key, value in app.openapi()["paths"][path].items()
        if key not in ("head", "options")
    }
    assert response.json() == {
        "path": path,
        "methods": methods,
        "operations": expected_operations,
    }


def test_implicit_head_mirrors_get():
    calls: list[str] = []

    def verify_token(x_token: str = Header()):
        calls.append(x_token)
        if x_token != "secret":
            raise HTTPException(
                status_code=401, detail="Invalid token", headers={"X-Error": "token"}
            )

    app = FastAPI()

    @app.get("/items/{item_id}", status_code=202, dependencies=[Depends(verify_token)])
    def read_item(item_id: int, response: Response):
        response.headers["X-Item-Id"] = str(item_id)
        return {"item_id": item_id}

    client = TestClient(app)
    get_response = client.get("/items/3", headers={"X-Token": "secret"})
    head_response = client.head("/items/3", headers={"X-Token": "secret"})
    assert get_response.status_code == 202
    assert head_response.status_code == 202
    assert head_response.content == b""
    assert head_response.headers == get_response.headers
    assert head_response.headers["x-item-id"] == "3"
    assert calls == ["secret", "secret"]

    unauthorized = client.head("/items/3", headers={"X-Token": "wrong"})
    assert unauthorized.status_code == 401
    assert unauthorized.headers["x-error"] == "token"
    assert unauthorized.content == b""

    invalid = client.head("/items/abc", headers={"X-Token": "secret"})
    assert invalid.status_code == 422
    assert invalid.content == b""

    missing_header = client.head("/items/3")
    assert missing_header.status_code == 422
    assert missing_header.content == b""


def test_implicit_head_request_method_and_background_tasks():
    tasks: list[str] = []

    app = FastAPI()

    @app.get("/log")
    def log(request: Request, background_tasks: BackgroundTasks):
        background_tasks.add_task(tasks.append, request.method)
        return {"ok": True}

    response = TestClient(app).head("/log")
    assert response.status_code == 200
    assert response.content == b""
    assert tasks == ["HEAD"]


def test_implicit_head_streaming_is_not_iterated():
    produced: list[int] = []
    tasks: list[str] = []

    def add_task(background_tasks: BackgroundTasks):
        background_tasks.add_task(tasks.append, "jsonl")

    app = FastAPI()

    @app.get("/jsonl", dependencies=[Depends(add_task)])
    async def jsonl() -> AsyncIterable[Item]:
        for index in range(3):
            produced.append(index)
            yield Item(name=str(index))

    @app.get("/sync-jsonl")
    def sync_jsonl() -> Iterable[Item]:
        for index in range(3):  # pragma: no cover
            produced.append(index)
            yield Item(name=str(index))

    @app.get("/sse", response_class=EventSourceResponse)
    async def sse() -> AsyncIterable[Item]:
        while True:
            yield Item(name="forever")

    client = TestClient(app)
    response = client.head("/jsonl")
    assert response.status_code == 200
    assert response.headers["content-type"] == "application/jsonl"
    assert response.content == b""
    assert tasks == ["jsonl"]

    response = client.head("/sync-jsonl")
    assert response.status_code == 200
    assert response.content == b""
    assert produced == []

    assert len(client.get("/jsonl").text.splitlines()) == 3
    assert produced == [0, 1, 2]

    response = client.head("/sse")
    assert response.status_code == 200
    assert response.headers["content-type"].startswith("text/event-stream")
    assert response.content == b""


def test_implicit_head_streamed_exception_handler_response():
    class TeapotError(Exception):
        pass

    app = FastAPI()

    @app.exception_handler(TeapotError)
    async def teapot_handler(request: Request, exc: TeapotError):
        async def content():
            yield b"short and "
            yield b"stout"

        return StreamingResponse(content(), status_code=418)

    @app.get("/teapot")
    def teapot():
        raise TeapotError()

    client = TestClient(app)
    assert client.get("/teapot").content == b"short and stout"
    response = client.head("/teapot")
    assert response.status_code == 418
    assert response.content == b""


def test_implicit_head_file_response(tmp_path):
    file_path = tmp_path / "data.txt"
    file_path.write_text("some file content")

    app = FastAPI()

    @app.get("/file")
    def read_file():
        return FileResponse(file_path)

    response = TestClient(app).head("/file")
    assert response.status_code == 200
    assert response.headers["content-length"] == str(len("some file content"))
    assert response.content == b""


def test_explicit_head_and_options_win():
    app = FastAPI(auto_options=True)

    @app.get("/items/{item_id}")
    def read_item(item_id: str):
        return {"item_id": item_id}

    @app.head("/items/{item_id}")
    def head_item(item_id: str):
        return Response(headers={"X-Explicit": "head"})

    @app.options("/items/{item_id}")
    def options_item(item_id: str):
        return {"explicit": "options"}

    client = TestClient(app)
    head_response = client.head("/items/foo")
    assert head_response.status_code == 200
    assert head_response.headers["x-explicit"] == "head"
    options_response = client.options("/items/foo")
    assert options_response.json() == {"explicit": "options"}
    assert "allow" not in options_response.headers


def test_explicit_catch_all_options_wins():
    app = FastAPI(auto_options=True)

    @app.get("/items/")
    def read_items():
        return []

    @app.options("/{full_path:path}")
    def catch_all_options(full_path: str):
        return {"catch_all": full_path}

    response = TestClient(app).options("/items/")
    assert response.json() == {"catch_all": "items/"}


def test_explicit_head_in_same_route_methods():
    app = FastAPI()

    @app.api_route("/items/", methods=["GET", "HEAD"])
    def read_items(response: Response):
        response.headers["X-Handler"] = "explicit"
        return ["explicit"]

    response = TestClient(app).head("/items/")
    assert response.status_code == 200
    assert response.headers["x-handler"] == "explicit"


def test_implicit_head_uses_the_route_serving_get():
    app = FastAPI()

    @app.post("/users/{user_id}")
    def update_user(user_id: str):
        return {}  # pragma: no cover

    @app.get("/users/me")
    def read_me(response: Response):
        response.headers["X-Handler"] = "me"
        return {}

    @app.get("/users/{user_id}")
    def read_user(user_id: str, response: Response):
        response.headers["X-Handler"] = "user"
        return {}  # pragma: no cover

    response = TestClient(app).head("/users/me")
    assert response.headers["x-handler"] == "me"


def test_head_disabled_on_get_target_falls_back_to_405():
    app = FastAPI()

    @app.get("/users/me", auto_head=False)
    def read_me():
        return {}  # pragma: no cover

    @app.get("/users/{user_id}")
    def read_user(user_id: str):
        return {}  # pragma: no cover

    response = TestClient(app).head("/users/me")
    assert response.status_code == 405


def test_implicit_options_response():
    app = FastAPI(auto_options=True)

    @app.trace("/items/{item_id}")
    def trace_item(item_id: str):
        return None  # pragma: no cover

    @app.delete("/items/{item_id}")
    def delete_item(item_id: str):
        return None  # pragma: no cover

    @app.patch("/items/{item_id}")
    def patch_item(item_id: str, item: Item):
        return item  # pragma: no cover

    @app.put("/items/{item_id}")
    def put_item(item_id: str, item: Item):
        return item  # pragma: no cover

    @app.post("/items/{item_id}", response_model=Item, tags=["items"])
    def post_item(item_id: str, item: Item):
        return item  # pragma: no cover

    @app.get("/items/{item_id}", summary="Read an item")
    def read_item(item_id: str):
        return {"item_id": item_id}  # pragma: no cover

    assert_implicit_options(
        app,
        "/items/{item_id}",
        "/items/foo",
        ["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE"],
    )


def test_implicit_options_method_order_with_custom_methods():
    app = FastAPI(auto_options=True)

    @app.api_route(
        "/dav", methods=["PROPFIND", "POST", "MKCOL", "GET"], include_in_schema=False
    )
    def dav():
        return {}  # pragma: no cover

    response = TestClient(app).options("/dav")
    assert response.json()["methods"] == [
        "GET",
        "HEAD",
        "POST",
        "OPTIONS",
        "MKCOL",
        "PROPFIND",
    ]
    assert response.headers["allow"] == "GET, HEAD, POST, OPTIONS, MKCOL, PROPFIND"


def test_implicit_options_excludes_explicit_head_operation():
    app = FastAPI(auto_options=True)

    @app.get("/items/")
    def read_items():
        return []  # pragma: no cover

    @app.head("/items/")
    def head_items():
        return None  # pragma: no cover

    assert "head" in app.openapi()["paths"]["/items/"]
    assert_implicit_options(app, "/items/", "/items/", ["GET", "HEAD", "OPTIONS"])


def test_implicit_options_without_head():
    app = FastAPI(auto_head=False, auto_options=True)

    @app.get("/items/")
    def read_items():
        return []  # pragma: no cover

    @app.post("/items/")
    def create_item(item: Item):
        return item  # pragma: no cover

    assert_implicit_options(app, "/items/", "/items/", ["GET", "POST", "OPTIONS"])


def test_implicit_options_enabled_by_any_operation_on_the_path():
    app = FastAPI()

    @app.get("/items/")
    def read_items():
        return []  # pragma: no cover

    @app.post("/items/", auto_options=True)
    def create_item(item: Item):
        return item  # pragma: no cover

    @app.get("/users/")
    def read_users():
        return []  # pragma: no cover

    client = TestClient(app)
    assert_implicit_options(
        app, "/items/", "/items/", ["GET", "HEAD", "POST", "OPTIONS"]
    )
    assert client.options("/users/").status_code == 405


def test_implicit_options_follows_first_matching_path():
    app = FastAPI()

    @app.get("/users/me")
    def read_me():
        return {}  # pragma: no cover

    @app.get("/users/{user_id}", auto_options=True)
    def read_user(user_id: str):
        return {}  # pragma: no cover

    client = TestClient(app)
    assert client.options("/users/me").status_code == 405
    assert client.options("/users/rick").json()["path"] == "/users/{user_id}"


def test_implicit_options_operations_exclude_hidden_operations():
    app = FastAPI(auto_options=True)

    @app.get("/items/")
    def read_items():
        return []  # pragma: no cover

    @app.post("/items/", include_in_schema=False)
    def create_item():
        return {}  # pragma: no cover

    response = TestClient(app).options("/items/")
    assert response.json()["methods"] == ["GET", "HEAD", "POST", "OPTIONS"]
    assert list(response.json()["operations"]) == ["get"]


def test_implicit_options_uses_custom_openapi():
    app = FastAPI(auto_options=True)

    @app.get("/items/")
    def read_items():
        return []  # pragma: no cover

    def custom_openapi():
        return {"paths": {"/items/": {"get": {"summary": "Custom"}}}}

    app.openapi = custom_openapi  # type: ignore[method-assign]

    response = TestClient(app).options("/items/")
    assert response.json()["operations"] == {"get": {"summary": "Custom"}}


def test_openapi_has_no_implicit_operations():
    app = FastAPI(auto_options=True)

    @app.get("/items/")
    def read_items():
        return []  # pragma: no cover

    @app.post("/items/")
    def create_item(item: Item):
        return item  # pragma: no cover

    client = TestClient(app)
    assert list(client.get("/openapi.json").json()["paths"]["/items/"]) == [
        "get",
        "post",
    ]
    assert client.head("/items/").status_code == 200
    assert client.options("/items/").status_code == 200
    assert list(client.get("/openapi.json").json()["paths"]["/items/"]) == [
        "get",
        "post",
    ]


def test_non_get_routes_have_no_implicit_head():
    app = FastAPI()

    @app.post("/items/", auto_head=True)
    def create_item():
        return {}  # pragma: no cover

    response = TestClient(app).head("/items/")
    assert response.status_code == 405


def test_app_values_are_outermost_defaults():
    app = FastAPI(auto_head=False, auto_options=True)

    @app.get("/default")
    def default():
        return {}  # pragma: no cover

    @app.get("/overridden", auto_head=True, auto_options=False)
    def overridden():
        return {}

    def not_decorated():
        return {}

    app.add_api_route("/add-api-route", not_decorated, auto_head=True)

    @app.api_route("/api-route", methods=["GET"], auto_options=False)
    def api_route():
        return {}  # pragma: no cover

    client = TestClient(app)
    assert client.head("/default").status_code == 405
    assert client.options("/default").status_code == 200
    assert client.head("/overridden").status_code == 200
    assert client.options("/overridden").status_code == 405
    assert client.head("/add-api-route").status_code == 200
    assert client.options("/add-api-route").json()["methods"] == [
        "GET",
        "HEAD",
        "OPTIONS",
    ]
    assert client.head("/api-route").status_code == 405
    assert client.options("/api-route").status_code == 405


@pytest.mark.parametrize(
    ("app_value", "router_value", "include_value", "route_value", "expected"),
    [
        # Route level wins over everything else
        (False, False, False, True, True),
        (True, True, True, False, False),
        # Include level wins over the router and the app
        (False, False, True, None, True),
        (True, True, False, None, False),
        # Router level wins over the app
        (False, True, None, None, True),
        (True, False, None, None, False),
        # App level is the last fallback
        (False, None, None, None, False),
        (True, None, None, None, True),
        # Built-in default
        (None, None, None, None, True),
    ],
)
def test_auto_head_precedence(
    app_value, router_value, include_value, route_value, expected
):
    def settings(value: bool | None) -> dict[str, bool]:
        return {} if value is None else {"auto_head": value}

    app = FastAPI(**settings(app_value))
    router = APIRouter(**settings(router_value))

    @router.get("/items/", **settings(route_value))
    def read_items():
        return []

    app.include_router(router, **settings(include_value))

    response = TestClient(app).head("/items/")
    assert response.status_code == (200 if expected else 405)


@pytest.mark.parametrize(
    ("app_value", "router_value", "include_value", "route_value", "expected"),
    [
        (False, False, False, True, True),
        (True, True, True, False, False),
        (False, False, True, None, True),
        (True, True, False, None, False),
        (False, True, None, None, True),
        (True, False, None, None, False),
        (False, None, None, None, False),
        (True, None, None, None, True),
        (None, None, None, None, False),
    ],
)
def test_auto_options_precedence(
    app_value, router_value, include_value, route_value, expected
):
    def settings(value: bool | None) -> dict[str, bool]:
        return {} if value is None else {"auto_options": value}

    app = FastAPI(**settings(app_value))
    router = APIRouter(**settings(router_value))

    @router.get("/items/", **settings(route_value))
    def read_items():
        return []  # pragma: no cover

    app.include_router(router, **settings(include_value))

    response = TestClient(app).options("/items/")
    assert response.status_code == (200 if expected else 405)


def test_nested_include_precedence():
    app = FastAPI(auto_head=False)
    outer = APIRouter(auto_head=False)
    inner = APIRouter()
    configured_inner = APIRouter(auto_head=False)

    @inner.get("/omitted")
    def omitted():
        return {}

    @configured_inner.get("/configured")
    def configured():
        return {}  # pragma: no cover

    outer.include_router(inner)
    outer.include_router(configured_inner)
    app.include_router(outer, prefix="/enabled", auto_head=True)
    app.include_router(outer, prefix="/disabled")

    client = TestClient(app)
    # Nothing was set for the inner route, so the outer include_router() wins
    assert client.head("/enabled/omitted").status_code == 200
    # The inner router value is nearer than the outer include_router()
    assert client.head("/enabled/configured").status_code == 405
    assert client.head("/disabled/omitted").status_code == 405


def test_repeated_inclusion():
    app = FastAPI(auto_options=True)
    router = APIRouter()

    @router.get("/items/")
    def read_items():
        return []

    app.include_router(router, prefix="/v1", auto_head=False, auto_options=False)
    app.include_router(router, prefix="/v2")
    app.include_router(router, prefix="/v3", auto_head=True, auto_options=True)

    client = TestClient(app)
    assert client.head("/v1/items/").status_code == 405
    assert client.options("/v1/items/").status_code == 405
    assert client.head("/v2/items/").status_code == 200
    assert client.options("/v2/items/").json()["path"] == "/v2/items/"
    assert client.head("/v3/items/").status_code == 200
    assert client.options("/v3/items/").json()["methods"] == ["GET", "HEAD", "OPTIONS"]
    # The included router itself is not modified by the inclusions
    assert len(router.routes) == 1
    other_app = FastAPI()
    other_app.include_router(router)
    other_client = TestClient(other_app)
    assert other_client.head("/items/").status_code == 200
    assert other_client.options("/items/").status_code == 405


def test_router_include_router_between_routers():
    parent = APIRouter(auto_options=True)
    child = APIRouter()

    @child.get("/items/")
    def read_items():
        return []  # pragma: no cover

    parent.include_router(child, prefix="/child", auto_head=False)

    client = TestClient(parent)
    assert client.head("/child/items/").status_code == 405
    response = client.options("/child/items/")
    assert response.status_code == 200
    assert response.json()["methods"] == ["GET", "OPTIONS"]
    assert list(response.json()["operations"]) == ["get"]


def test_mounted_router_uses_its_own_operations():
    router = APIRouter(auto_options=True)

    @router.get("/items/")
    def read_items():
        return []  # pragma: no cover

    app = FastAPI()

    @app.get("/items/")
    def read_app_items():
        return []  # pragma: no cover

    app.mount("/mounted", router)

    response = TestClient(app).options("/mounted/items/")
    assert response.status_code == 200
    assert response.json()["operations"]["get"]["operationId"] == (
        "read_items_items__get"
    )


def test_mounted_sub_application():
    sub_app = FastAPI(auto_options=True)

    @sub_app.get("/items/{item_id}")
    def read_item(item_id: str):
        return {"item_id": item_id}

    app = FastAPI()
    app.mount("/sub", sub_app)

    client = TestClient(app)
    assert client.head("/sub/items/foo").status_code == 200
    assert_implicit_options(
        sub_app, "/items/{item_id}", "/items/foo", ["GET", "HEAD", "OPTIONS"]
    )
    response = client.options("/sub/items/foo")
    assert response.json()["path"] == "/items/{item_id}"


def test_starlette_routes_are_not_changed():
    app = FastAPI(auto_options=True)

    async def plain(request):
        return PlainTextResponse("plain")

    app.add_route("/plain", plain, methods=["GET"])

    @app.post("/plain")
    def post_plain():
        return {}  # pragma: no cover

    client = TestClient(app)
    assert client.head("/plain").status_code == 200
    response = client.options("/plain")
    assert response.status_code == 200
    assert response.json()["methods"] == ["GET", "HEAD", "POST", "OPTIONS"]


def test_redirect_slashes_for_implicit_methods():
    app = FastAPI(auto_options=True)

    @app.get("/items")
    def read_items():
        return []

    client = TestClient(app)
    assert client.head("/items/").status_code == 200
    assert client.options("/items/").status_code == 200
    response = client.head("/items/", follow_redirects=False)
    assert response.status_code == 307


def test_not_found_for_implicit_methods():
    app = FastAPI(auto_options=True)

    @app.get("/items/")
    def read_items():
        return []  # pragma: no cover

    client = TestClient(app)
    assert client.head("/missing").status_code == 404
    assert client.options("/missing").status_code == 404


def test_cors_preflight_is_not_affected():
    app = FastAPI(auto_options=True)
    app.add_middleware(
        CORSMiddleware,
        allow_origins=["https://example.com"],
        allow_methods=["GET", "POST"],
    )

    @app.get("/items/")
    def read_items():
        return []  # pragma: no cover

    client = TestClient(app)
    preflight = client.options(
        "/items/",
        headers={
            "Origin": "https://example.com",
            "Access-Control-Request-Method": "POST",
        },
    )
    assert preflight.status_code == 200
    assert preflight.text == "OK"
    assert preflight.headers["access-control-allow-origin"] == "https://example.com"
    assert "allow" not in preflight.headers

    simple = client.options("/items/", headers={"Origin": "https://example.com"})
    assert simple.status_code == 200
    assert simple.json()["methods"] == ["GET", "HEAD", "OPTIONS"]
    assert simple.headers["access-control-allow-origin"] == "https://example.com"

    head_response = client.head("/items/", headers={"Origin": "https://example.com"})
    assert head_response.status_code == 200
    assert head_response.headers["access-control-allow-origin"] == "https://example.com"


def test_docs_surface():
    app = FastAPI(auto_options=True)

    @app.get("/items/")
    def read_items():
        return []  # pragma: no cover

    client = TestClient(app)
    for url in ("/docs", "/redoc", "/openapi.json", "/docs/oauth2-redirect"):
        assert client.head(url).status_code == 200, url
        assert client.options(url).status_code == 405, url
    assert list(client.get("/openapi.json").json()["paths"]) == ["/items/"]
