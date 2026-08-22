from __future__ import annotations

import asyncio
import dataclasses
import sys
from importlib.metadata import version
from pathlib import Path
from typing import TYPE_CHECKING, Dict, Mapping, Tuple

if sys.version_info >= (3, 11):
    from enum import StrEnum
else:
    from backports.strenum import StrEnum

from aiohttp import web
from jinja2 import Environment, PackageLoader, select_autoescape
from pydantic import Field

from .utils import APIParams, check_params

if TYPE_CHECKING:
    from ..monitor import Monitor


@dataclasses.dataclass
class WebUIContext:
    monitor: Monitor
    jenv: Environment


class TaskTypes(StrEnum):
    RUNNING = "running"
    TERMINATED = "terminated"


class TaskTypeParams(APIParams):
    task_type: TaskTypes = Field(default=TaskTypes.RUNNING)


class TaskIdParams(APIParams):
    task_id: str


class ListFilterParams(APIParams):
    filter: str = Field(default="")
    persistent: bool = Field(default=False)


class SnapshotSaveParams(APIParams):
    name: str | None = Field(default=None)


class SnapshotIdParams(APIParams):
    snapshot_id: int


class SnapshotTaskParams(SnapshotIdParams):
    task_id: str


class SnapshotDiffParams(APIParams):
    snapshot_id_1: int
    snapshot_id_2: int


@dataclasses.dataclass
class NavigationItem:
    title: str
    current: bool


nav_menus: Mapping[str, NavigationItem] = {
    "/": NavigationItem(
        title="Dashboard",
        current=False,
    ),
    "/about": NavigationItem(
        title="About",
        current=False,
    ),
    "/snapshots": NavigationItem(
        title="Snapshots",
        current=False,
    ),
}


ctx_key = web.AppKey("ctx_key", WebUIContext)


def get_navigation_info(
    route: str,
) -> Tuple[NavigationItem, Mapping[str, NavigationItem]]:
    nav_items: Dict[str, NavigationItem] = {}
    current_item = None
    for path, item in nav_menus.items():
        is_current = path == route
        nav_items[path] = NavigationItem(title=item.title, current=is_current)
        if is_current:
            current_item = item
    if current_item is None:
        raise web.HTTPNotFound
    return current_item, nav_items


async def show_list_page(request: web.Request) -> web.Response:
    ctx: WebUIContext = request.app[ctx_key]
    nav_info, nav_items = get_navigation_info(request.path)
    template = ctx.jenv.get_template("index.html")
    async with check_params(request, TaskTypeParams) as params:
        output = template.render(
            navigation=nav_items,
            page={
                "title": nav_info.title,
            },
            current_list_type=params.task_type,
            list_types=[
                {"id": TaskTypes.RUNNING, "title": "Running"},
                {"id": TaskTypes.TERMINATED, "title": "Terminated"},
            ],
        )
        return web.Response(body=output, content_type="text/html")


async def show_about_page(request: web.Request) -> web.Response:
    ctx: WebUIContext = request.app[ctx_key]
    nav_info, nav_items = get_navigation_info(request.path)
    template = ctx.jenv.get_template("about.html")
    output = template.render(
        navigation=nav_items,
        page={
            "title": nav_info.title,
        },
    )
    return web.Response(body=output, content_type="text/html")


async def show_snapshots_page(request: web.Request) -> web.Response:
    ctx: WebUIContext = request.app[ctx_key]
    nav_info, nav_items = get_navigation_info(request.path)
    template = ctx.jenv.get_template("snapshots.html")
    output = template.render(
        navigation=nav_items,
        page={
            "title": nav_info.title,
        },
    )
    return web.Response(body=output, content_type="text/html")


async def show_trace_page(request: web.Request) -> web.Response:
    ctx: WebUIContext = request.app[ctx_key]
    template = ctx.jenv.get_template("trace.html")
    async with check_params(request, TaskIdParams) as params:
        if request.path.startswith("/trace-running"):
            trace_data = ctx.monitor.format_running_task_stack(params.task_id)
        elif request.path.startswith("/trace-terminated"):
            trace_data = ctx.monitor.format_terminated_task_stack(params.task_id)
        else:
            raise RuntimeError("should not reach here")
        output = template.render(
            navigation=nav_menus,
            page={
                "title": f"Task trace for {params.task_id}",
            },
            trace_data=trace_data,
        )
        return web.Response(body=output, content_type="text/html")


async def get_version(request: web.Request) -> web.Response:
    return web.json_response(
        data={
            "value": version("aiomonitor"),
        }
    )


async def get_task_count(request: web.Request) -> web.Response:
    async with check_params(request, TaskTypeParams) as params:
        ctx: WebUIContext = request.app[ctx_key]
        if params.task_type == TaskTypes.RUNNING:
            count = len(asyncio.all_tasks(ctx.monitor._monitored_loop))
        elif params.task_type == TaskTypes.TERMINATED:
            count = len(ctx.monitor._terminated_history)
        else:
            raise RuntimeError("should not reach here")
        return web.json_response(
            data={
                "value": count,
            }
        )


async def get_live_task_list(request: web.Request) -> web.Response:
    ctx: WebUIContext = request.app[ctx_key]
    async with check_params(request, ListFilterParams) as params:
        tasks = ctx.monitor.format_running_task_list(
            params.filter,
            params.persistent,
        )
        return web.json_response(
            data={
                "tasks": [
                    {
                        "task_id": t.task_id,
                        "state": t.state,
                        "name": t.name,
                        "coro": t.coro,
                        "created_location": t.created_location,
                        "since": t.since,
                        "is_root": t.created_location == "-",
                    }
                    for t in tasks
                ]
            }
        )


async def get_terminated_task_list(request: web.Request) -> web.Response:
    ctx: WebUIContext = request.app[ctx_key]
    async with check_params(request, ListFilterParams) as params:
        tasks = ctx.monitor.format_terminated_task_list(
            params.filter,
            params.persistent,
        )
        return web.json_response(
            data={
                "tasks": [
                    {
                        "task_id": t.task_id,
                        "name": t.name,
                        "coro": t.coro,
                        "started_since": t.started_since,
                        "terminated_since": t.terminated_since,
                    }
                    for t in tasks
                ]
            }
        )


async def save_snapshot(request: web.Request) -> web.Response:
    ctx: WebUIContext = request.app[ctx_key]
    async with check_params(request, SnapshotSaveParams) as params:
        snapshot_id = await ctx.monitor.capture_snapshot(params.name)
        return web.json_response(data={"id": snapshot_id})


async def get_snapshot_list(request: web.Request) -> web.Response:
    ctx: WebUIContext = request.app[ctx_key]
    return web.json_response(
        data={
            "snapshots": [dataclasses.asdict(item) for item in ctx.monitor.list_snapshots()]
        }
    )


def _snapshot_task_json(task: object) -> dict[str, str]:
    return {
        key: str(getattr(task, key))
        for key in (
            "task_id",
            "state",
            "name",
            "coro",
            "created_location",
            "since",
        )
        if hasattr(task, key)
    }


async def get_snapshot_tasks(request: web.Request) -> web.Response:
    ctx: WebUIContext = request.app[ctx_key]
    async with check_params(request, SnapshotIdParams) as params:
        try:
            tasks = ctx.monitor.format_snapshot_task_list(params.snapshot_id)
        except KeyError:
            raise web.HTTPNotFound(
                content_type="application/json",
                text='{"msg": "Snapshot not found"}',
            ) from None
        return web.json_response(
            data={"tasks": [_snapshot_task_json(task) for task in tasks]}
        )


async def get_snapshot_trace(request: web.Request) -> web.Response:
    ctx: WebUIContext = request.app[ctx_key]
    async with check_params(request, SnapshotTaskParams) as params:
        try:
            trace = ctx.monitor.format_snapshot_task_stack(
                params.snapshot_id,
                params.task_id,
            )
        except KeyError:
            raise web.HTTPNotFound(
                content_type="application/json",
                text='{"msg": "Snapshot or task not found"}',
            ) from None
        return web.json_response(
            data={
                "trace": [
                    {"type": item.type, "content": item.content}
                    for item in trace
                ]
            }
        )


async def get_snapshot_diff(request: web.Request) -> web.Response:
    ctx: WebUIContext = request.app[ctx_key]
    async with check_params(request, SnapshotDiffParams) as params:
        try:
            diff = ctx.monitor.format_snapshot_diff(
                params.snapshot_id_1,
                params.snapshot_id_2,
            )
        except KeyError:
            raise web.HTTPNotFound(
                content_type="application/json",
                text='{"msg": "Snapshot not found"}',
            ) from None
        return web.json_response(
            data={
                field: [_snapshot_task_json(task) for task in getattr(diff, field)]
                for field in ("added", "removed", "common")
            }
        )


async def delete_snapshot(request: web.Request) -> web.Response:
    ctx: WebUIContext = request.app[ctx_key]
    async with check_params(request, SnapshotIdParams) as params:
        try:
            ctx.monitor.delete_snapshot(params.snapshot_id)
        except KeyError:
            raise web.HTTPNotFound(
                content_type="application/json",
                text='{"msg": "Snapshot not found"}',
            ) from None
        return web.json_response(data={"msg": "Snapshot deleted"})


async def cancel_task(request: web.Request) -> web.Response:
    ctx: WebUIContext = request.app[ctx_key]
    async with check_params(request, TaskIdParams) as params:
        try:
            coro_repr = await ctx.monitor.cancel_monitored_task(params.task_id)
            return web.json_response(
                data={
                    "msg": f"Successfully cancelled {params.task_id}",
                    "detail": coro_repr,
                },
            )
        except ValueError as e:
            return web.json_response(
                status=404,
                data={"msg": repr(e)},
            )


async def init_webui(monitor: Monitor) -> web.Application:
    jenv = Environment(
        loader=PackageLoader("aiomonitor.webui"), autoescape=select_autoescape()
    )
    app = web.Application()
    app[ctx_key] = WebUIContext(
        monitor=monitor,
        jenv=jenv,
    )
    app.router.add_route("GET", "/", show_list_page)
    app.router.add_route("GET", "/about", show_about_page)
    app.router.add_route("GET", "/snapshots", show_snapshots_page)
    app.router.add_route("GET", "/trace-running", show_trace_page)
    app.router.add_route("GET", "/trace-terminated", show_trace_page)
    app.router.add_route("GET", "/api/version", get_version)
    app.router.add_route("POST", "/api/task-count", get_task_count)
    app.router.add_route("POST", "/api/live-tasks", get_live_task_list)
    app.router.add_route("POST", "/api/terminated-tasks", get_terminated_task_list)
    app.router.add_route("POST", "/api/snapshot", save_snapshot)
    app.router.add_route("GET", "/api/snapshot", get_snapshot_list)
    app.router.add_route("POST", "/api/snapshot/tasks", get_snapshot_tasks)
    app.router.add_route("POST", "/api/snapshot/trace", get_snapshot_trace)
    app.router.add_route("POST", "/api/snapshot/diff", get_snapshot_diff)
    app.router.add_route("DELETE", "/api/snapshot", delete_snapshot)
    app.router.add_route("DELETE", "/api/task", cancel_task)
    app.router.add_static("/static", Path(__file__).parent / "static")
    return app
