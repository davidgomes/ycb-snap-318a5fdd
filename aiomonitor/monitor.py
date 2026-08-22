from __future__ import annotations

import asyncio
import contextlib
import contextvars
import copy
import functools
import logging
import sys
import textwrap
import threading
import time
import traceback
import weakref
from asyncio.coroutines import _format_coroutine  # type: ignore
from datetime import timedelta
from types import TracebackType
from typing import (
    Any,
    Awaitable,
    Coroutine,
    Dict,
    Final,
    Generator,
    List,
    Optional,
    Sequence,
    Type,
    TypeVar,
    cast,
)

import janus
from aiohttp import web
from prompt_toolkit.contrib.telnet.server import TelnetServer

from .exceptions import MissingTask
from .task import TracedTask, persistent_coro
from .termui.commands import interact
from .types import (
    CancellationChain,
    FormatItemTypes,
    FormattedLiveTaskInfo,
    FormattedSnapshotDiff,
    FormattedStackItem,
    FormattedTerminatedTaskInfo,
    Snapshot,
    SnapshotSummary,
    SnapshotTask,
    SnapshotTaskStackInfo,
    TerminatedTaskInfo,
)
from .utils import (
    _extract_stack_from_exception,
    _extract_stack_from_frame,
    _extract_stack_from_task,
    _filter_stack,
    _format_filename,
    _format_task,
    _format_terminated_task,
    _format_timedelta,
    get_default_args,
)
from .webui.app import init_webui

__all__ = (
    "Monitor",
    "start_monitor",
)

log = logging.getLogger(__name__)

MONITOR_HOST: Final = "127.0.0.1"
MONITOR_TERMUI_PORT: Final = 20101
MONITOR_WEBUI_PORT: Final = 20102
CONSOLE_PORT: Final = 20103

T = TypeVar("T")
T_co = TypeVar("T_co", covariant=True)


def task_by_id(
    taskid: int, loop: asyncio.AbstractEventLoop
) -> "Optional[asyncio.Task[Any]]":
    tasks = asyncio.all_tasks(loop=loop)
    return next(filter(lambda t: id(t) == taskid, tasks), None)


async def cancel_task(task: "asyncio.Task[Any]") -> None:
    with contextlib.suppress(asyncio.CancelledError):
        task.cancel()
        await task


class Monitor:
    prompt: str
    """
    The string that prompts you to enter a command, defaults to ``"monitor >>> "``
    """

    _event_loop_thread_id: Optional[int] = None

    console_locals: Dict[str, Any]
    _termui_tasks: weakref.WeakSet[asyncio.Task[Any]]

    _created_traceback_chains: weakref.WeakKeyDictionary[
        asyncio.Task[Any],
        weakref.ReferenceType[asyncio.Task[Any]],
    ]
    _created_tracebacks: weakref.WeakKeyDictionary[
        asyncio.Task[Any], List[traceback.FrameSummary]
    ]
    _terminated_tasks: Dict[str, TerminatedTaskInfo]
    _terminated_history: List[str]
    _termination_info_queue: janus.Queue[TerminatedTaskInfo]
    _canceller_chain: Dict[str, str]
    _canceller_stacks: Dict[str, List[traceback.FrameSummary] | None]
    _cancellation_chain_queue: janus.Queue[CancellationChain]
    _snapshots: Dict[int, Snapshot]
    _next_snapshot_id: int
    _max_snapshots: int

    def __init__(
        self,
        loop: asyncio.AbstractEventLoop,
        *,
        host: str = MONITOR_HOST,
        termui_port: int = MONITOR_TERMUI_PORT,
        webui_port: int = MONITOR_WEBUI_PORT,
        console_port: int = CONSOLE_PORT,
        console_enabled: bool = True,
        hook_task_factory: bool = False,
        max_termination_history: int = 1000,
        max_snapshots: int = 10,
        locals: Optional[Dict[str, Any]] = None,
    ) -> None:
        self._monitored_loop = loop or asyncio.get_running_loop()
        self._host = host
        self._termui_port = termui_port
        self._webui_port = webui_port
        self._console_port = console_port
        self._console_enabled = console_enabled
        if locals is None:
            self.console_locals = {"__name__": "__console__", "__doc__": None}
        else:
            self.console_locals = locals

        self.prompt = "monitor >>> "
        log.info(
            "Starting aiomonitor at telnet://%(host)s:%(tport)d and http://%(host)s:%(wport)d",
            {
                "host": host,
                "tport": termui_port,
                "wport": webui_port,
            },
        )

        self._closed = False
        self._started = False
        self._termui_tasks = weakref.WeakSet()

        self._hook_task_factory = hook_task_factory
        self._created_traceback_chains = weakref.WeakKeyDictionary()
        self._created_tracebacks = weakref.WeakKeyDictionary()
        self._terminated_tasks = {}
        self._canceller_chain = {}
        self._canceller_stacks = {}
        self._terminated_history = []
        self._max_termination_history = max_termination_history
        self._snapshots = {}
        self._next_snapshot_id = 1
        self._max_snapshots = max(1, max_snapshots)

        self._ui_started = threading.Event()
        self._ui_thread = threading.Thread(target=self._ui_main, args=(), daemon=True)

    @property
    def host(self) -> str:
        """
        The current hostname to bind the monitor server.
        """
        return self._host

    @property
    def port(self) -> int:
        """
        The port number to bind the monitor server.
        """
        return self._termui_port

    def __repr__(self) -> str:
        name = self.__class__.__name__
        return "<{name}: {host}:{port}>".format(
            name=name, host=self._host, port=self._termui_port
        )

    def start(self) -> None:
        """
        Starts monitoring thread, where telnet server is executed.
        """
        assert not self._closed
        assert not self._started
        self._started = True
        self._original_task_factory = self._monitored_loop.get_task_factory()
        if self._hook_task_factory:
            self._monitored_loop.set_task_factory(self._create_task)
        self._event_loop_thread_id = threading.get_ident()
        self._ui_thread.start()
        self._ui_started.wait()

    @property
    def closed(self) -> bool:
        """
        A flag indicates if monitor was closed, currntly instance of
        :class:`Monitor` can not be reused. For new monitor, new instance
        should be created.
        """
        return self._closed

    def __enter__(self) -> Monitor:
        if not self._started:
            self.start()
        return self

    # exc_type should be Optional[Type[BaseException]], but
    # this runs into https://github.com/python/typing/issues/266
    # on Python 3.5.
    def __exit__(
        self,
        exc_type: Any,
        exc_value: Optional[BaseException],
        traceback: Optional[TracebackType],
    ) -> None:
        self.close()

    def close(self) -> None:
        """
        Joins background thread, and cleans up resources.
        """
        assert self._started, "The monitor must have been started to close it."
        if not self._closed:
            self._ui_loop.call_soon_threadsafe(
                self._ui_forever_future.cancel,
            )
            self._monitored_loop.set_task_factory(self._original_task_factory)
            self._ui_thread.join()
            self._closed = True

    @staticmethod
    def _copy_stack(
        stack: Optional[List[traceback.FrameSummary]],
    ) -> Optional[List[traceback.FrameSummary]]:
        return None if stack is None else list(stack)

    @staticmethod
    def _snapshot_task_repr(task: "asyncio.Task[Any]") -> str:
        return _format_task(task)

    def _get_snapshot(self, snapshot_id: str | int) -> Snapshot:
        try:
            snapshot_id_ = int(snapshot_id)
        except (TypeError, ValueError):
            raise KeyError(snapshot_id) from None
        return self._snapshots[snapshot_id_]

    def _snapshot_task_item(
        self,
        task: "asyncio.Task[Any]",
        captured_at: float,
    ) -> SnapshotTask:
        task_id = str(id(task))
        if isinstance(task, TracedTask):
            coro_repr = _format_coroutine(task._orig_coro).partition(" ")[0]
            since = _format_timedelta(
                timedelta(seconds=captured_at - task._started_at)
            )
        else:
            coro_repr = _format_coroutine(task.get_coro()).partition(" ")[0]
            since = "-"
        creation_stack = self._created_tracebacks.get(task)
        if not creation_stack:
            created_location = "-"
        else:
            creation_stack = _filter_stack(creation_stack)
            if not creation_stack:
                created_location = "-"
            else:
                fn = _format_filename(creation_stack[-1].filename)
                created_location = f"{fn}:{creation_stack[-1].lineno}"
        return SnapshotTask(
            task_id=task_id,
            state=task._state,
            name=task.get_name(),
            coro=coro_repr,
            created_location=created_location,
            since=since,
            task_repr=self._snapshot_task_repr(task),
            creation_stack=self._copy_stack(creation_stack),
            task_stack=_extract_stack_from_task(task),
            creation_chain=(),
        )

    async def capture_snapshot(self, name: Optional[str] = None) -> int:
        """
        Capture the current running and terminated task state.

        Snapshot contents are copies of the task metadata and stack frames at
        capture time, so later task changes do not alter an existing snapshot.
        """
        captured_at = time.perf_counter()
        all_tasks = asyncio.all_tasks(loop=self._monitored_loop)
        tasks = {
            str(id(task)): self._snapshot_task_item(task, captured_at)
            for task in sorted(
                all_tasks,
                key=id,
            )
        }
        for task in all_tasks:
            task_item = tasks[str(id(task))]
            chain: List[SnapshotTaskStackInfo] = []
            current_task: Optional[asyncio.Task[Any]] = task
            while current_task is not None:
                current_item = tasks.get(str(id(current_task)))
                if current_item is None:
                    break
                chain.append(
                    SnapshotTaskStackInfo(
                        task_repr=current_item.task_repr,
                        creation_stack=self._copy_stack(
                            current_item.creation_stack
                        ),
                    )
                )
                task_ref = self._created_traceback_chains.get(current_task)
                current_task = task_ref() if task_ref is not None else None
            task_item.creation_chain = tuple(chain)

        snapshot_id = self._next_snapshot_id
        self._next_snapshot_id += 1
        snapshot = Snapshot(
            id=snapshot_id,
            name=name,
            captured_at=captured_at,
            tasks=tasks,
            terminated_tasks=copy.deepcopy(self._terminated_tasks),
        )
        self._snapshots[snapshot_id] = snapshot
        while len(self._snapshots) > self._max_snapshots:
            unnamed = (
                candidate
                for candidate in self._snapshots.values()
                if candidate.name is None
            )
            oldest = next(unnamed, None)
            if oldest is None:
                break
            del self._snapshots[oldest.id]
        return snapshot_id

    def list_snapshots(self) -> Sequence[SnapshotSummary]:
        return [
            SnapshotSummary(
                id=snapshot.id,
                name=snapshot.name,
                running_count=len(snapshot.tasks),
                terminated_count=len(snapshot.terminated_tasks),
            )
            for snapshot in self._snapshots.values()
        ]

    def get_snapshot(self, snapshot_id: str | int) -> Snapshot:
        return self._get_snapshot(snapshot_id)

    def delete_snapshot(self, snapshot_id: str | int) -> None:
        snapshot = self._get_snapshot(snapshot_id)
        del self._snapshots[snapshot.id]

    def format_snapshot_task_list(
        self,
        snapshot_id: str | int,
    ) -> Sequence[FormattedLiveTaskInfo]:
        snapshot = self._get_snapshot(snapshot_id)
        return [
            FormattedLiveTaskInfo(
                task_id=task.task_id,
                state=task.state,
                name=task.name,
                coro=task.coro,
                created_location=task.created_location,
                since=task.since,
            )
            for task in snapshot.tasks.values()
        ]

    def format_snapshot_terminated_task_list(
        self,
        snapshot_id: str | int,
    ) -> Sequence[FormattedTerminatedTaskInfo]:
        snapshot = self._get_snapshot(snapshot_id)
        tasks = sorted(
            snapshot.terminated_tasks.values(),
            key=lambda info: info.terminated_at,
            reverse=True,
        )
        return [
            FormattedTerminatedTaskInfo(
                task_id=str(item.id),
                name=item.name,
                coro=item.coro,
                started_since=_format_timedelta(
                    timedelta(seconds=snapshot.captured_at - item.started_at)
                ),
                terminated_since=_format_timedelta(
                    timedelta(seconds=snapshot.captured_at - item.terminated_at)
                ),
            )
            for item in tasks
        ]

    def format_snapshot_task_stack(
        self,
        snapshot_id: str | int,
        task_id: str | int,
    ) -> Sequence[FormattedStackItem]:
        snapshot = self._get_snapshot(snapshot_id)
        try:
            task_id_ = str(int(task_id))
        except (TypeError, ValueError):
            raise KeyError(task_id) from None
        task = snapshot.tasks[task_id_]
        depth = 0
        formatted_stack_list = []
        prev_task: Optional[SnapshotTaskStackInfo] = None
        for chain_task in reversed(task.creation_chain):
            if depth == 0:
                formatted_stack_list.append(
                    FormattedStackItem(
                        FormatItemTypes.HEADER,
                        (
                            "Stack of the root task or coroutine scheduled "
                            "in the event loop (most recent call last)"
                        ),
                    )
                )
            else:
                assert prev_task is not None
                formatted_stack_list.append(
                    FormattedStackItem(
                        FormatItemTypes.HEADER,
                        (
                            "Stack of %s when creating the next task "
                            "(most recent call last)" % prev_task.task_repr
                        ),
                    )
                )
            stack = chain_task.creation_stack
            if stack is None:
                formatted_stack_list.append(
                    FormattedStackItem(
                        FormatItemTypes.CONTENT,
                        (
                            "No stack available (maybe it is a native code, "
                            "a synchronous callback function, "
                            "or the event loop itself)"
                        ),
                    )
                )
            else:
                stack = _filter_stack(stack) if stack else stack
                formatted_stack_list.append(
                    FormattedStackItem(
                        FormatItemTypes.CONTENT,
                        textwrap.dedent("".join(traceback.format_list(stack))),
                    )
                )
            prev_task = chain_task
            depth += 1
        formatted_stack_list.append(
            FormattedStackItem(
                FormatItemTypes.HEADER,
                "Stack of %s (most recent call last)" % task.task_repr,
            )
        )
        if not task.task_stack:
            formatted_stack_list.append(
                FormattedStackItem(
                    FormatItemTypes.CONTENT,
                    "No stack available for %s" % task.task_repr,
                )
            )
        else:
            formatted_stack_list.append(
                FormattedStackItem(
                    FormatItemTypes.CONTENT,
                    textwrap.dedent(
                        "".join(traceback.format_list(task.task_stack))
                    ),
                )
            )
        return formatted_stack_list

    def format_snapshot_diff(
        self,
        snapshot_id_1: str | int,
        snapshot_id_2: str | int,
    ) -> FormattedSnapshotDiff:
        snapshot_1 = self._get_snapshot(snapshot_id_1)
        snapshot_2 = self._get_snapshot(snapshot_id_2)
        ids_1 = set(snapshot_1.tasks)
        ids_2 = set(snapshot_2.tasks)

        def format_task(task: SnapshotTask) -> FormattedLiveTaskInfo:
            return FormattedLiveTaskInfo(
                task_id=task.task_id,
                state=task.state,
                name=task.name,
                coro=task.coro,
                created_location=task.created_location,
                since=task.since,
            )

        return FormattedSnapshotDiff(
            added=[
                format_task(snapshot_2.tasks[task_id])
                for task_id in sorted(ids_2 - ids_1, key=int)
            ],
            removed=[
                format_task(snapshot_1.tasks[task_id])
                for task_id in sorted(ids_1 - ids_2, key=int)
            ],
            common=[
                format_task(snapshot_2.tasks[task_id])
                for task_id in sorted(ids_1 & ids_2, key=int)
            ],
        )

    def format_running_task_list(
        self, filter_: str, persistent: bool
    ) -> Sequence[FormattedLiveTaskInfo]:
        all_running_tasks = asyncio.all_tasks(loop=self._monitored_loop)
        tasks = []
        for task in sorted(all_running_tasks, key=id):
            taskid = str(id(task))
            if isinstance(task, TracedTask):
                coro_repr = _format_coroutine(task._orig_coro).partition(" ")[0]
                if persistent and task._orig_coro not in persistent_coro:
                    continue
            else:
                coro_repr = _format_coroutine(task.get_coro()).partition(" ")[0]
                if persistent:
                    # untracked tasks should be skipped when showing persistent ones only
                    continue
            if filter_ and (
                filter_ not in coro_repr and filter_ not in task.get_name()
            ):
                continue
            creation_stack = self._created_tracebacks.get(task)
            # Some values are masked as "-" when they are unavailable
            # if it's the root task/coro or if the task factory is not applied.
            if not creation_stack:
                created_location = "-"
            else:
                creation_stack = _filter_stack(creation_stack)
                fn = _format_filename(creation_stack[-1].filename)
                lineno = creation_stack[-1].lineno
                created_location = f"{fn}:{lineno}"
            if isinstance(task, TracedTask):
                running_since = _format_timedelta(
                    timedelta(
                        seconds=(time.perf_counter() - task._started_at),
                    )
                )
            else:
                running_since = "-"
            tasks.append(
                FormattedLiveTaskInfo(
                    taskid,
                    task._state,
                    task.get_name(),
                    coro_repr,
                    created_location,
                    running_since,
                )
            )
        return tasks

    def format_terminated_task_list(
        self, filter_: str, persistent: bool
    ) -> Sequence[FormattedTerminatedTaskInfo]:
        terminated_tasks = self._terminated_tasks.values()
        tasks = []
        for item in sorted(
            terminated_tasks,
            key=lambda info: info.terminated_at,
            reverse=True,
        ):
            if persistent and not item.persistent:
                continue
            if filter_ and (filter_ not in item.coro and filter_ not in item.name):
                continue
            started_since = _format_timedelta(
                timedelta(seconds=time.perf_counter() - item.started_at)
            )
            terminated_since = _format_timedelta(
                timedelta(seconds=time.perf_counter() - item.terminated_at)
            )
            tasks.append(
                FormattedTerminatedTaskInfo(
                    str(item.id),
                    item.name,
                    item.coro,
                    started_since,
                    terminated_since,
                )
            )
        return tasks

    async def cancel_monitored_task(self, task_id: str | int) -> str:
        task_id_ = int(task_id)
        task = task_by_id(task_id_, self._monitored_loop)
        if task is not None:
            if self._monitored_loop == asyncio.get_running_loop():
                await cancel_task(task)
            else:
                fut = asyncio.wrap_future(
                    asyncio.run_coroutine_threadsafe(
                        cancel_task(task), loop=self._monitored_loop
                    )
                )
                await fut
            if isinstance(task, TracedTask):
                coro_repr = _format_coroutine(task._orig_coro).partition(" ")[0]
            else:
                coro_repr = _format_coroutine(task.get_coro()).partition(" ")[0]
            return coro_repr
        else:
            raise ValueError("Invalid or non-existent task ID", task_id)

    def format_running_task_stack(
        self,
        task_id: str | int,
    ) -> Sequence[FormattedStackItem]:
        depth = 0
        task_id_ = int(task_id)
        task = task_by_id(task_id_, self._monitored_loop)
        if task is None:
            raise MissingTask(task_id_)
        task_chain: List[asyncio.Task[Any]] = []
        while task is not None:
            task_chain.append(task)
            task_ref = self._created_traceback_chains.get(task)
            task = task_ref() if task_ref is not None else None
        prev_task = None
        formatted_stack_list = []
        for task in reversed(task_chain):
            if depth == 0:
                formatted_stack_list.append(
                    FormattedStackItem(
                        FormatItemTypes.HEADER,
                        (
                            "Stack of the root task or coroutine scheduled "
                            "in the event loop (most recent call last)"
                        ),
                    )
                )
            elif depth > 0:
                assert prev_task is not None
                formatted_stack_list.append(
                    FormattedStackItem(
                        FormatItemTypes.HEADER,
                        (
                            "Stack of %s when creating the next task "
                            "(most recent call last)" % _format_task(prev_task)
                        ),
                    )
                )
            stack = self._created_tracebacks.get(task)
            if stack is None:
                formatted_stack_list.append(
                    FormattedStackItem(
                        FormatItemTypes.CONTENT,
                        (
                            "No stack available (maybe it is a native code, "
                            "a synchronous callback function, "
                            "or the event loop itself)"
                        ),
                    )
                )
            else:
                stack = _filter_stack(stack)
                formatted_stack_list.append(
                    FormattedStackItem(
                        FormatItemTypes.CONTENT,
                        textwrap.dedent("".join(traceback.format_list(stack))),
                    )
                )
            prev_task = task
            depth += 1
        task = task_chain[0]
        formatted_stack_list.append(
            FormattedStackItem(
                FormatItemTypes.HEADER,
                "Stack of %s (most recent call last)" % _format_task(task),
            )
        )
        stack = _extract_stack_from_task(task)
        if not stack:
            formatted_stack_list.append(
                FormattedStackItem(
                    FormatItemTypes.CONTENT,
                    "No stack available for %s" % _format_task(task),
                )
            )
        else:
            formatted_stack_list.append(
                FormattedStackItem(
                    FormatItemTypes.CONTENT,
                    textwrap.dedent("".join(traceback.format_list(stack))),
                )
            )
        return formatted_stack_list

    def format_terminated_task_stack(
        self,
        trace_id: str,
    ) -> Sequence[FormattedStackItem]:
        depth = 0
        tinfo_chain: List[TerminatedTaskInfo] = []
        while trace_id is not None:
            tinfo_chain.append(self._terminated_tasks[trace_id])
            trace_id = self._canceller_chain.get(trace_id)  # type: ignore
        prev_tinfo = None
        formatted_stack_list = []
        for tinfo in reversed(tinfo_chain):
            if depth == 0:
                formatted_stack_list.append(
                    FormattedStackItem(
                        FormatItemTypes.HEADER,
                        (
                            "Stack of the root task or coroutine "
                            "scheduled in the event loop"
                            "(most recent call last)"
                        ),
                    )
                )
            elif depth > 0:
                assert prev_tinfo is not None
                formatted_stack_list.append(
                    FormattedStackItem(
                        FormatItemTypes.HEADER,
                        (
                            "Stack of %s when creating the next task "
                            "(most recent call last)"
                            % _format_terminated_task(prev_tinfo)
                        ),
                    )
                )
            stack = tinfo.canceller_stack
            if stack is None:
                formatted_stack_list.append(
                    FormattedStackItem(
                        FormatItemTypes.CONTENT,
                        (
                            "No stack available "
                            "(maybe it is a self-raised cancellation or exception)"
                        ),
                    )
                )
            else:
                stack = _filter_stack(stack)
                formatted_stack_list.append(
                    FormattedStackItem(
                        FormatItemTypes.CONTENT,
                        textwrap.dedent("".join(traceback.format_list(stack))),
                    )
                )
            prev_tinfo = tinfo
            depth += 1
        tinfo = tinfo_chain[0]
        formatted_stack_list.append(
            FormattedStackItem(
                FormatItemTypes.HEADER,
                "Stack of %s (most recent call last)" % _format_terminated_task(tinfo),
            )
        )
        stack = tinfo.termination_stack
        if not stack:
            formatted_stack_list.append(
                FormattedStackItem(
                    FormatItemTypes.CONTENT,
                    (
                        "No stack available for %s (the task has run to completion)"
                        % _format_terminated_task(tinfo)
                    ),
                )
            )
        else:
            formatted_stack_list.append(
                FormattedStackItem(
                    FormatItemTypes.CONTENT,
                    textwrap.dedent("".join(traceback.format_list(stack))),
                )
            )
        return formatted_stack_list

    async def _coro_wrapper(self, coro: Awaitable[T_co]) -> T_co:
        myself = asyncio.current_task()
        assert isinstance(myself, TracedTask)
        try:
            return await coro
        except BaseException as e:
            myself._termination_stack = _extract_stack_from_exception(e)[:-1]
            raise

    def _create_task(
        self,
        loop: asyncio.AbstractEventLoop,
        coro: Coroutine[Any, Any, T_co] | Generator[Any, None, T_co],
        *,
        name: str | None = None,
        context: contextvars.Context | None = None,
    ) -> asyncio.Future[T_co]:
        assert loop is self._monitored_loop
        try:
            parent_task = asyncio.current_task()
        except RuntimeError:
            parent_task = None
        persistent = coro in persistent_coro
        task = TracedTask(
            self._coro_wrapper(coro),  # type: ignore
            termination_info_queue=self._termination_info_queue.sync_q,
            cancellation_chain_queue=self._cancellation_chain_queue.sync_q,
            persistent=persistent,
            loop=self._monitored_loop,
            name=name,  # since Python 3.8
            context=context,  # since Python 3.11
        )
        task._orig_coro = cast(Coroutine[Any, Any, T_co], coro)
        self._created_tracebacks[task] = _extract_stack_from_frame(sys._getframe())[
            :-1
        ]  # strip this wrapper method
        if parent_task is not None:
            self._created_traceback_chains[task] = weakref.ref(parent_task)
        return task

    def _ui_main(self) -> None:
        asyncio.run(self._ui_main_async())

    async def _ui_main_async(self) -> None:
        loop = asyncio.get_running_loop()
        self._termination_info_queue = janus.Queue()
        self._cancellation_chain_queue = janus.Queue()
        self._ui_loop = loop
        self._ui_forever_future = loop.create_future()
        self._ui_termination_handler_task = loop.create_task(
            self._ui_handle_termination_updates()
        )
        self._ui_cancellation_handler_task = loop.create_task(
            self._ui_handle_cancellation_updates()
        )
        telnet_server = TelnetServer(
            interact=functools.partial(interact, self),
            host=self._host,
            port=self._termui_port,
        )
        webui_app = await init_webui(self)
        webui_runner = web.AppRunner(webui_app)
        await webui_runner.setup()
        webui_site = web.TCPSite(
            webui_runner,
            str(self._host),
            self._webui_port,
            reuse_port=True,
        )
        await webui_site.start()
        telnet_server.start()
        await asyncio.sleep(0)
        self._ui_started.set()
        try:
            await self._ui_forever_future
        except asyncio.CancelledError:
            pass
        finally:
            termui_tasks = {*self._termui_tasks}
            for termui_task in termui_tasks:
                termui_task.cancel()
            await asyncio.gather(*termui_tasks, return_exceptions=True)
            self._ui_termination_handler_task.cancel()
            self._ui_cancellation_handler_task.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await self._ui_termination_handler_task
            with contextlib.suppress(asyncio.CancelledError):
                await self._ui_cancellation_handler_task
            await telnet_server.stop()
            await webui_runner.cleanup()

    async def _ui_handle_termination_updates(self) -> None:
        while True:
            try:
                update: TerminatedTaskInfo = (
                    await self._termination_info_queue.async_q.get()
                )
            except asyncio.CancelledError:
                return
            self._terminated_tasks[update.id] = update
            if not update.persistent:
                self._terminated_history.append(update.id)
            # canceller stack is already put in _ui_handle_cancellation_updates()
            if canceller_stack := self._canceller_stacks.pop(update.id, None):
                update.canceller_stack = canceller_stack
            while len(self._terminated_history) > self._max_termination_history:
                removed_id = self._terminated_history.pop(0)
                self._terminated_tasks.pop(removed_id, None)
                self._canceller_chain.pop(removed_id, None)
                self._canceller_stacks.pop(removed_id, None)

    async def _ui_handle_cancellation_updates(self) -> None:
        while True:
            try:
                update: CancellationChain = (
                    await self._cancellation_chain_queue.async_q.get()
                )
            except asyncio.CancelledError:
                return
            self._canceller_stacks[update.target_id] = update.canceller_stack
            self._canceller_chain[update.target_id] = update.canceller_id


def start_monitor(
    loop: asyncio.AbstractEventLoop,
    *,
    monitor_cls: Type[Monitor] = Monitor,
    host: str = MONITOR_HOST,
    port: int = MONITOR_TERMUI_PORT,  # kept the name for backward compatibility
    console_port: int = CONSOLE_PORT,
    webui_port: int = MONITOR_WEBUI_PORT,
    console_enabled: bool = True,
    hook_task_factory: bool = False,
    max_termination_history: Optional[int] = None,
    max_snapshots: Optional[int] = None,
    locals: Optional[Dict[str, Any]] = None,
) -> Monitor:
    """
    Factory function, creates instance of :class:`Monitor` and starts
    monitoring thread.

    :param Type[Monitor] monitor: Monitor class to use
    :param str host: hostname to serve monitor telnet server
    :param int port: monitor port (terminal UI), by default 20101
    :param int webui_port: monitor port (web UI), by default 20102
    :param int console_port: python REPL port, by default 20103
    :param bool console_enabled: flag indicates if python REPL is requred
        to start with instance of monitor.
    :param dict locals: dictionary with variables exposed in python console
        environment
    """
    m = monitor_cls(
        loop,
        host=host,
        termui_port=port,
        webui_port=webui_port,
        console_port=console_port,
        console_enabled=console_enabled,
        hook_task_factory=hook_task_factory,
        max_termination_history=(
            max_termination_history
            if max_termination_history is not None
            else get_default_args(monitor_cls.__init__)["max_termination_history"]
        ),
        max_snapshots=(
            max_snapshots
            if max_snapshots is not None
            else get_default_args(monitor_cls.__init__)["max_snapshots"]
        ),
        locals=locals,
    )
    m.start()
    return m
