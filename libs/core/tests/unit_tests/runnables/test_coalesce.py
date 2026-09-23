"""Unit tests for request coalescing."""

from __future__ import annotations

import asyncio
import threading
import time
from typing import TYPE_CHECKING, Any

import pytest
from pydantic import BaseModel
from typing_extensions import override

from langchain_core import runnables
from langchain_core.callbacks import BaseCallbackHandler
from langchain_core.runnables import (
    CoalesceBackend,
    CoalesceStats,
    InMemoryCoalesceBackend,
    RunnableLambda,
)

if TYPE_CHECKING:
    from collections.abc import AsyncIterator, Callable, Iterator, Sequence
    from uuid import UUID

    from langchain_core.runnables.base import Runnable

_TIMEOUT = 5.0


def _wait_for_total(runnable: Any, expected: int) -> None:
    deadline = time.monotonic() + _TIMEOUT
    while runnable.coalesce_info().total < expected:
        if time.monotonic() > deadline:
            msg = "timed out waiting for coalesced callers"
            raise TimeoutError(msg)
        time.sleep(0.001)


def _gather(functions: Sequence[Callable[[], Any]]) -> list[Any]:
    results: list[Any] = [None] * len(functions)
    errors: list[BaseException | None] = [None] * len(functions)

    def _target(index: int, fn: Callable[[], Any]) -> None:
        try:
            results[index] = fn()
        except BaseException as exc:
            errors[index] = exc

    threads = [
        threading.Thread(target=_target, args=(index, fn))
        for index, fn in enumerate(functions)
    ]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join(_TIMEOUT)
        if thread.is_alive():
            msg = "coalesced caller did not finish"
            raise AssertionError(msg)
    for error in errors:
        if error is not None:
            raise error
    return results


def _graph_structure(
    runnable: Runnable[Any, Any],
) -> tuple[list[str], list[tuple[str, str]]]:
    graph = runnable.get_graph()
    names = [node.name for node in graph.nodes.values()]
    edges = [
        (graph.nodes[edge.source].name, graph.nodes[edge.target].name)
        for edge in graph.edges
    ]
    return names, edges


class _Recorder(BaseCallbackHandler):
    def __init__(self) -> None:
        super().__init__()
        self.events: list[str] = []

    @override
    def on_chain_start(
        self,
        serialized: dict[str, Any],
        inputs: dict[str, Any],
        *,
        run_id: UUID,
        **kwargs: Any,
    ) -> None:
        self.events.append("start")

    @override
    def on_chain_end(
        self,
        outputs: dict[str, Any],
        *,
        run_id: UUID,
        **kwargs: Any,
    ) -> None:
        self.events.append("end")


def test_exports() -> None:
    assert issubclass(InMemoryCoalesceBackend, CoalesceBackend)
    stats = CoalesceStats(active=1, coalesced=2, total=3)
    assert stats == (1, 2, 3)
    assert stats.active == 1
    assert "RunnableCoalesce" not in runnables.__all__
    assert runnables.CoalesceBackend is CoalesceBackend
    assert runnables.InMemoryCoalesceBackend is InMemoryCoalesceBackend


def test_backend_register_join_complete_and_refresh() -> None:
    backend = InMemoryCoalesceBackend()
    assert backend.stats == CoalesceStats(0, 0, 0)
    assert backend.is_active("missing") is False
    assert backend.register("k") is True
    assert backend.is_active("k") is True
    assert backend.stats == CoalesceStats(1, 0, 1)

    found: dict[str, Any] = {}

    def _joiner() -> None:
        assert backend.register("k") is False
        found["value"] = backend.join("k")

    thread = threading.Thread(target=_joiner)
    thread.start()
    deadline = time.monotonic() + _TIMEOUT
    while backend.stats.coalesced < 1:
        if time.monotonic() > deadline:
            msg = "joiner did not register"
            raise AssertionError(msg)
        time.sleep(0.001)
    assert backend.stats == CoalesceStats(1, 1, 2)
    backend.complete("k", result={"n": 1})
    thread.join(_TIMEOUT)
    assert not thread.is_alive()
    assert found["value"] == {"n": 1}
    assert backend.is_active("k") is False
    assert backend.stats == CoalesceStats(0, 1, 2)

    assert backend.register("k") is True
    backend.complete("k", result=None)
    assert backend.is_active("k") is False
    assert backend.stats.active == 0


def test_backend_join_receives_error() -> None:
    backend = InMemoryCoalesceBackend()
    assert backend.register("k") is True
    error: BaseException | None = None

    def _joiner() -> None:
        nonlocal error
        assert backend.register("k") is False
        try:
            backend.join("k")
        except BaseException as exc:
            error = exc

    thread = threading.Thread(target=_joiner)
    thread.start()
    deadline = time.monotonic() + _TIMEOUT
    while backend.stats.coalesced < 1:
        if time.monotonic() > deadline:
            msg = "joiner did not register"
            raise AssertionError(msg)
        time.sleep(0.001)
    backend.complete("k", error=ValueError("boom"))
    thread.join(_TIMEOUT)
    assert not thread.is_alive()
    assert isinstance(error, ValueError)
    assert error.args == ("boom",)
    assert error is not None


def test_backend_clear_cancels_sync_waiters() -> None:
    backend = InMemoryCoalesceBackend()
    assert backend.register("k") is True
    error: BaseException | None = None
    registered = threading.Event()

    def _joiner() -> None:
        nonlocal error
        assert backend.register("k") is False
        registered.set()
        try:
            backend.join("k")
        except BaseException as exc:
            error = exc

    thread = threading.Thread(target=_joiner)
    thread.start()
    assert registered.wait(_TIMEOUT)
    backend.clear()
    thread.join(_TIMEOUT)
    assert not thread.is_alive()
    assert isinstance(error, asyncio.CancelledError)
    assert backend.stats == CoalesceStats(0, 0, 0)
    assert backend.is_active("k") is False
    backend.complete("k", result="late")
    assert backend.stats == CoalesceStats(0, 0, 0)
    assert backend.register("k") is True
    backend.complete("k", result="fresh")
    assert backend.stats == CoalesceStats(0, 0, 1)


def test_backend_clear_then_complete_resets_active_count() -> None:
    backend = InMemoryCoalesceBackend()
    assert backend.register("k") is True
    backend.clear()
    backend.complete("k", result=1)
    assert backend.stats == CoalesceStats(0, 0, 0)
    assert backend.register("k") is True
    assert backend.is_active("k") is True
    backend.complete("k", result=2)
    assert backend.stats == CoalesceStats(0, 0, 1)


async def test_backend_clear_cancels_async_waiters() -> None:
    backend = InMemoryCoalesceBackend()
    assert await backend.aregister("k") is True

    async def _joiner() -> Any:
        assert await backend.aregister("k") is False
        return await backend.ajoin("k")

    task = asyncio.create_task(_joiner())
    deadline = time.monotonic() + _TIMEOUT
    while backend.stats.coalesced < 1:
        if time.monotonic() > deadline:
            msg = "async joiner did not register"
            raise AssertionError(msg)
        await asyncio.sleep(0)
    await asyncio.sleep(0)
    backend.clear()
    with pytest.raises(asyncio.CancelledError):
        await task
    assert backend.stats == CoalesceStats(0, 0, 0)
    assert await backend.ais_active("k") is False


def test_backend_thread_safety_single_leader() -> None:
    backend = InMemoryCoalesceBackend()
    count = 16
    started = threading.Barrier(count)
    leaders = 0
    leaders_lock = threading.Lock()
    results: list[Any] = []
    results_lock = threading.Lock()

    def _worker() -> None:
        nonlocal leaders
        started.wait(_TIMEOUT)
        if backend.register("same"):
            with leaders_lock:
                leaders += 1
            deadline = time.monotonic() + _TIMEOUT
            while backend.stats.total < count:
                if time.monotonic() > deadline:
                    msg = "not all callers registered"
                    raise TimeoutError(msg)
                time.sleep(0.001)
            backend.complete("same", result="ok")
        else:
            value = backend.join("same")
            with results_lock:
                results.append(value)

    threads = [threading.Thread(target=_worker) for _ in range(count)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join(_TIMEOUT)
        assert not thread.is_alive()
    assert leaders == 1
    assert results == ["ok"] * (count - 1)
    assert backend.is_active("same") is False
    assert backend.stats == CoalesceStats(0, count - 1, count)


async def test_backend_async_single_leader() -> None:
    backend = InMemoryCoalesceBackend()
    count = 12
    leaders = 0

    async def _worker() -> str:
        nonlocal leaders
        if await backend.aregister("k"):
            leaders += 1
            deadline = time.monotonic() + _TIMEOUT
            while backend.stats.total < count:
                if time.monotonic() > deadline:
                    msg = "not all async callers registered"
                    raise TimeoutError(msg)
                await asyncio.sleep(0)
            await backend.acomplete("k", result="ok")
            return "leader"
        joined = await backend.ajoin("k")
        assert joined == "ok"
        return "ok"

    results = await asyncio.gather(*[_worker() for _ in range(count)])
    assert leaders == 1
    assert results.count("leader") == 1
    assert results.count("ok") == count - 1
    assert await backend.ais_active("k") is False
    assert backend.stats == CoalesceStats(0, count - 1, count)


def test_invoke_coalesces_concurrent_calls_and_refreshes() -> None:
    calls = {"n": 0}
    holder: dict[str, Any] = {}
    observed: list[CoalesceStats] = []

    def _fn(value: str) -> str:
        calls["n"] += 1
        _wait_for_total(holder["runnable"], 2)
        observed.append(holder["runnable"].coalesce_info())
        return value.upper()

    runnable = RunnableLambda(_fn).with_coalesce()
    holder["runnable"] = runnable
    assert _gather([lambda: runnable.invoke("ab"), lambda: runnable.invoke("ab")]) == [
        "AB",
        "AB",
    ]
    assert calls["n"] == 1
    assert observed[0].active == 1
    assert observed[0].coalesced == 1
    assert observed[0].total == 2
    assert runnable.coalesce_info() == CoalesceStats(0, 1, 2)
    assert runnable.invoke("ab") == "AB"
    assert calls["n"] == 2
    assert runnable.coalesce_info().coalesced == 1


def test_coalescing_key_ignores_config_kwargs_and_dict_order() -> None:
    calls = {"n": 0}
    holder: dict[str, Any] = {}

    def _fn(value: Any, **kwargs: Any) -> Any:
        calls["n"] += 1
        _wait_for_total(holder["runnable"], 2)
        assert kwargs["extra"] in {1, 2}
        return value

    runnable = RunnableLambda(_fn).with_coalesce()
    holder["runnable"] = runnable
    left = {"b": 1, "a": 2, "nested": {"d": 4, "c": 3}}
    right = {"nested": {"c": 3, "d": 4}, "a": 2, "b": 1}
    results = _gather(
        [
            lambda: runnable.invoke(left, config={"tags": ["one"]}, extra=1),
            lambda: runnable.invoke(right, config={"tags": ["two"]}, extra=2),
        ]
    )
    assert calls["n"] == 1
    assert results[0] == results[1] == left or results[0] == results[1] == right


def test_reordered_sequences_do_not_coalesce() -> None:
    calls = {"n": 0}
    holder: dict[str, Any] = {}
    release = threading.Event()

    def _fn(value: list[int]) -> list[int]:
        calls["n"] += 1
        if calls["n"] == 2:
            release.set()
        assert release.wait(_TIMEOUT)
        return value

    runnable = RunnableLambda(_fn).with_coalesce()
    holder["runnable"] = runnable
    results = _gather(
        [
            lambda: runnable.invoke([1, 2]),
            lambda: runnable.invoke([2, 1]),
        ]
    )
    assert calls["n"] == 2
    assert results == [[1, 2], [2, 1]]


def test_pydantic_and_sets_coalesce_by_value() -> None:
    class Payload(BaseModel):
        b: int
        a: str

    calls = {"n": 0}
    holder: dict[str, Any] = {}

    def _fn(value: Any) -> Any:
        calls["n"] += 1
        _wait_for_total(holder["runnable"], 2)
        return value

    runnable = RunnableLambda(_fn).with_coalesce()
    holder["runnable"] = runnable
    _gather(
        [
            lambda: runnable.invoke(Payload(b=1, a="x")),
            lambda: runnable.invoke(Payload(a="x", b=1)),
        ]
    )
    assert calls["n"] == 1

    calls["n"] = 0
    other = RunnableLambda(_fn).with_coalesce()
    holder["runnable"] = other
    _gather(
        [
            lambda: other.invoke({1, 2}),
            lambda: other.invoke({2, 1}),
        ]
    )
    assert calls["n"] == 1


def test_invoke_shares_errors() -> None:
    calls = {"n": 0}
    holder: dict[str, Any] = {}

    def _fn(value: str) -> str:
        calls["n"] += 1
        _wait_for_total(holder["runnable"], 2)
        msg = f"boom:{value}"
        raise ValueError(msg)

    runnable = RunnableLambda(_fn).with_coalesce()
    holder["runnable"] = runnable
    errors: list[BaseException] = []
    lock = threading.Lock()

    def _call() -> None:
        try:
            runnable.invoke("x")
        except BaseException as exc:
            with lock:
                errors.append(exc)

    threads = [threading.Thread(target=_call), threading.Thread(target=_call)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join(_TIMEOUT)
        assert not thread.is_alive()
    assert calls["n"] == 1
    assert len(errors) == 2
    assert all(
        isinstance(err, ValueError) and err.args == ("boom:x",) for err in errors
    )
    assert errors[0] is not errors[1]


async def test_ainvoke_coalesces() -> None:
    calls = {"n": 0}
    holder: dict[str, Any] = {}

    async def _fn(value: str) -> str:
        calls["n"] += 1
        deadline = time.monotonic() + _TIMEOUT
        while holder["runnable"].coalesce_info().total < 2:
            if time.monotonic() > deadline:
                msg = "timed out waiting for async callers"
                raise TimeoutError(msg)
            await asyncio.sleep(0)
        return value.upper()

    runnable = RunnableLambda(_fn).with_coalesce()
    holder["runnable"] = runnable
    results = await asyncio.gather(runnable.ainvoke("ab"), runnable.ainvoke("ab"))
    # `gather` is typed as a tuple and returns a list at runtime.
    assert list(results) == ["AB", "AB"]
    assert calls["n"] == 1


def test_sync_and_async_share_one_execution() -> None:
    calls = {"n": 0}
    holder: dict[str, Any] = {}

    def _fn(value: str) -> str:
        calls["n"] += 1
        _wait_for_total(holder["runnable"], 2)
        return f"{value}-ok"

    runnable = RunnableLambda(_fn).with_coalesce()
    holder["runnable"] = runnable

    async def _run() -> tuple[str, str]:
        async_task = asyncio.create_task(runnable.ainvoke("x"))
        sync_task = asyncio.create_task(asyncio.to_thread(runnable.invoke, "x"))
        return await async_task, await sync_task

    assert asyncio.run(_run()) == ("x-ok", "x-ok")
    assert calls["n"] == 1


def test_stream_replays_chunks_from_the_beginning() -> None:
    calls = {"n": 0}
    holder: dict[str, Any] = {}

    def _gen(value: str) -> Iterator[str]:
        calls["n"] += 1
        yield f"{value}-1"
        _wait_for_total(holder["runnable"], 2)
        yield f"{value}-2"

    runnable = RunnableLambda(_gen).with_coalesce()
    holder["runnable"] = runnable
    leader_chunks: list[str] = []
    joiner_chunks: list[str] = []
    got_first = threading.Event()

    def _leader() -> None:
        iterator = runnable.stream("v")
        leader_chunks.append(next(iterator))
        got_first.set()
        leader_chunks.extend(iterator)

    def _joiner() -> None:
        assert got_first.wait(_TIMEOUT)
        joiner_chunks.extend(runnable.stream("v"))

    threads = [threading.Thread(target=_leader), threading.Thread(target=_joiner)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join(_TIMEOUT)
        assert not thread.is_alive()
    assert leader_chunks == ["v-1", "v-2"]
    assert joiner_chunks == ["v-1", "v-2"]
    assert calls["n"] == 1


async def test_astream_replays_chunks_from_the_beginning() -> None:
    calls = {"n": 0}
    holder: dict[str, Any] = {}
    got_first = asyncio.Event()

    async def _gen(value: str) -> AsyncIterator[str]:
        calls["n"] += 1
        yield f"{value}-1"
        deadline = time.monotonic() + _TIMEOUT
        while holder["runnable"].coalesce_info().total < 2:
            if time.monotonic() > deadline:
                msg = "timed out waiting for stream joiner"
                raise TimeoutError(msg)
            await asyncio.sleep(0)
        yield f"{value}-2"

    runnable = RunnableLambda(_gen).with_coalesce()
    holder["runnable"] = runnable

    async def _leader() -> list[str]:
        iterator = runnable.astream("v")
        first = await anext(iterator)
        got_first.set()
        rest = [chunk async for chunk in iterator]
        return [first, *rest]

    async def _joiner() -> list[str]:
        await asyncio.wait_for(got_first.wait(), timeout=_TIMEOUT)
        return [chunk async for chunk in runnable.astream("v")]

    leader_task = asyncio.create_task(_leader())
    joiner_task = asyncio.create_task(_joiner())
    assert await leader_task == ["v-1", "v-2"]
    assert await joiner_task == ["v-1", "v-2"]
    assert calls["n"] == 1


def test_stream_and_invoke_share_one_execution() -> None:
    calls = {"n": 0}
    holder: dict[str, Any] = {}
    started = threading.Event()

    def _gen(value: str) -> Iterator[str]:
        calls["n"] += 1
        started.set()
        yield f"{value}-1"
        _wait_for_total(holder["runnable"], 2)
        yield f"{value}-2"

    runnable = RunnableLambda(_gen).with_coalesce()
    holder["runnable"] = runnable

    def _stream() -> list[str]:
        return list(runnable.stream("a"))

    def _invoke() -> str:
        assert started.wait(_TIMEOUT)
        return runnable.invoke("a")

    chunks, value = _gather([_stream, _invoke])
    assert chunks == ["a-1", "a-2"]
    assert value == "a-1a-2"
    assert calls["n"] == 1


def test_batch_coalesces_per_item_and_preserves_order() -> None:
    calls: dict[str, int] = {}
    holder: dict[str, Any] = {}
    lock = threading.Lock()

    def _fn(value: str) -> str:
        with lock:
            calls[value] = calls.get(value, 0) + 1
        _wait_for_total(holder["runnable"], 4)
        return value.upper()

    runnable = RunnableLambda(_fn).with_coalesce()
    holder["runnable"] = runnable
    assert runnable.batch(["b", "a", "b", "a"]) == ["B", "A", "B", "A"]
    assert calls == {"a": 1, "b": 1}


async def test_abatch_coalesces_per_item_and_preserves_order() -> None:
    calls: dict[str, int] = {}
    holder: dict[str, Any] = {}

    async def _fn(value: str) -> str:
        calls[value] = calls.get(value, 0) + 1
        deadline = time.monotonic() + _TIMEOUT
        while holder["runnable"].coalesce_info().total < 3:
            if time.monotonic() > deadline:
                msg = "timed out waiting for batch callers"
                raise TimeoutError(msg)
            await asyncio.sleep(0)
        return value.upper()

    runnable = RunnableLambda(_fn).with_coalesce()
    holder["runnable"] = runnable
    assert await runnable.abatch(["a", "b", "a"]) == ["A", "B", "A"]
    assert calls == {"a": 1, "b": 1}


def test_batch_return_exceptions_shares_errors() -> None:
    calls: dict[str, int] = {}
    holder: dict[str, Any] = {}
    lock = threading.Lock()

    def _fn(value: str) -> str:
        with lock:
            calls[value] = calls.get(value, 0) + 1
        _wait_for_total(holder["runnable"], 3)
        if value == "bad":
            msg = "bad"
            raise ValueError(msg)
        return value

    runnable = RunnableLambda(_fn).with_coalesce()
    holder["runnable"] = runnable
    result = runnable.batch(["ok", "bad", "bad"], return_exceptions=True)
    assert result[0] == "ok"
    assert isinstance(result[1], ValueError)
    assert isinstance(result[2], ValueError)
    assert calls == {"ok": 1, "bad": 1}


def test_batch_as_completed_yields_duplicates_consecutively() -> None:
    calls: dict[str, int] = {}
    holder: dict[str, Any] = {}
    lock = threading.Lock()

    def _fn(value: str) -> str:
        with lock:
            calls[value] = calls.get(value, 0) + 1
        _wait_for_total(holder["runnable"], 5)
        return value

    runnable = RunnableLambda(_fn).with_coalesce()
    holder["runnable"] = runnable
    inputs = ["a", "b", "a", "c", "b"]
    pairs = list(runnable.batch_as_completed(inputs))
    assert sorted(index for index, _ in pairs) == [0, 1, 2, 3, 4]
    assert all(inputs[index] == value for index, value in pairs)
    positions = [
        position for position, (index, _) in enumerate(pairs) if inputs[index] == "a"
    ]
    assert positions == list(range(positions[0], positions[0] + 2))
    positions = [
        position for position, (index, _) in enumerate(pairs) if inputs[index] == "b"
    ]
    assert positions == list(range(positions[0], positions[0] + 2))
    assert calls == {"a": 1, "b": 1, "c": 1}


async def test_abatch_as_completed_yields_duplicates_consecutively() -> None:
    calls: dict[str, int] = {}
    holder: dict[str, Any] = {}

    async def _fn(value: str) -> str:
        calls[value] = calls.get(value, 0) + 1
        deadline = time.monotonic() + _TIMEOUT
        while holder["runnable"].coalesce_info().total < 4:
            if time.monotonic() > deadline:
                msg = "timed out waiting for batch callers"
                raise TimeoutError(msg)
            await asyncio.sleep(0)
        return value

    runnable = RunnableLambda(_fn).with_coalesce()
    holder["runnable"] = runnable
    inputs = ["a", "b", "a", "a"]
    pairs = [item async for item in runnable.abatch_as_completed(inputs)]
    assert sorted(index for index, _ in pairs) == [0, 1, 2, 3]
    assert all(inputs[index] == value for index, value in pairs)
    positions = [
        position for position, (index, _) in enumerate(pairs) if inputs[index] == "a"
    ]
    assert positions == list(range(positions[0], positions[0] + 3))
    assert calls == {"a": 1, "b": 1}


def test_joined_callers_fire_chain_callbacks() -> None:
    holder: dict[str, Any] = {}
    leader_handler = _Recorder()
    joiner_handler = _Recorder()

    def _fn(value: str) -> str:
        _wait_for_total(holder["runnable"], 2)
        return value

    runnable = RunnableLambda(_fn).with_coalesce()
    holder["runnable"] = runnable
    _gather(
        [
            lambda: runnable.invoke("x", config={"callbacks": [leader_handler]}),
            lambda: runnable.invoke("x", config={"callbacks": [joiner_handler]}),
        ]
    )
    assert "start" in leader_handler.events
    assert "end" in leader_handler.events
    assert joiner_handler.events[0] == "start"
    assert joiner_handler.events[-1] == "end"
    assert joiner_handler.events.count("start") == 1
    assert joiner_handler.events.count("end") == 1


def test_stream_joiner_fires_chain_callbacks() -> None:
    holder: dict[str, Any] = {}
    joiner_handler = _Recorder()

    def _gen(value: str) -> Iterator[str]:
        yield value
        _wait_for_total(holder["runnable"], 2)
        yield value

    runnable = RunnableLambda(_gen).with_coalesce()
    holder["runnable"] = runnable

    def _leader() -> list[str]:
        return list(runnable.stream("z"))

    def _joiner() -> list[str]:
        return list(runnable.stream("z", config={"callbacks": [joiner_handler]}))

    leader, joiner = _gather([_leader, _joiner])
    assert leader == ["z", "z"]
    assert joiner == ["z", "z"]
    assert joiner_handler.events[0] == "start"
    assert joiner_handler.events[-1] == "end"


def test_transform_and_events_are_not_coalesced() -> None:
    calls = {"n": 0}
    entered = threading.Event()

    def _fn(value: int) -> int:
        calls["n"] += 1
        if calls["n"] == 1:
            if not entered.wait(_TIMEOUT):
                msg = "second transform did not run"
                raise TimeoutError(msg)
        else:
            entered.set()
        return value

    runnable = RunnableLambda(_fn).with_coalesce()

    def _transform() -> list[int]:
        return list(runnable.transform(iter([1])))

    assert _gather([_transform, _transform]) == [[1], [1]]
    assert calls["n"] == 2


async def test_atransform_and_astream_events_are_not_coalesced() -> None:
    calls = {"n": 0}
    first = asyncio.Event()
    second = asyncio.Event()

    async def _fn(value: int) -> int:
        calls["n"] += 1
        if calls["n"] == 1:
            first.set()
            await asyncio.wait_for(second.wait(), timeout=_TIMEOUT)
        else:
            second.set()
        return value

    runnable = RunnableLambda(_fn).with_coalesce()

    async def _transform() -> list[int]:
        async def _inputs() -> AsyncIterator[int]:
            yield 1

        return [item async for item in runnable.atransform(_inputs())]

    transformed = await asyncio.gather(_transform(), _transform())
    # `gather` is typed as a tuple and returns a list at runtime.
    assert list(transformed) == [[1], [1]]
    assert calls["n"] == 2

    calls["n"] = 0
    first.clear()
    second.clear()

    async def _events() -> list[Any]:
        return [event async for event in runnable.astream_events(1, version="v2")]

    first_events, second_events = await asyncio.gather(_events(), _events())
    assert calls["n"] == 2
    assert "on_chain_start" in [event["event"] for event in first_events]
    assert "on_chain_end" in [event["event"] for event in first_events]
    assert "on_chain_start" in [event["event"] for event in second_events]
    assert all(event["name"] != "RunnableCoalesce" for event in first_events)


def test_graph_delegation_is_transparent() -> None:
    def _left(value: int) -> int:
        return value + 1

    def _right(value: int) -> int:
        return value + 2

    base = RunnableLambda(_left)
    wrapped = base.with_coalesce()
    other = RunnableLambda(_right)
    assert _graph_structure(wrapped) == _graph_structure(base)
    assert wrapped.get_graph().to_json() == base.get_graph().to_json()
    assert _graph_structure(wrapped | other) == _graph_structure(base | other)
    assert all(node.data is not wrapped for node in wrapped.get_graph().nodes.values())
    assert all(
        node.data is not wrapped
        for node in (wrapped | other).get_graph().nodes.values()
    )
    assert "Coalesce" not in "".join(_graph_structure(wrapped | other)[0])


def test_wrappers_are_independent_unless_they_share_a_backend() -> None:
    calls = {"n": 0}
    entered = threading.Event()

    def _fn(value: str) -> str:
        calls["n"] += 1
        if calls["n"] == 1:
            if not entered.wait(_TIMEOUT):
                msg = "second wrapper did not run"
                raise TimeoutError(msg)
        else:
            entered.set()
        return value

    base = RunnableLambda(_fn)
    _gather(
        [
            lambda: base.with_coalesce().invoke("x"),
            lambda: base.with_coalesce().invoke("x"),
        ]
    )
    assert calls["n"] == 2

    calls["n"] = 0
    backend = InMemoryCoalesceBackend()
    shared_holder: dict[str, Any] = {}

    def _shared(value: str) -> str:
        calls["n"] += 1
        _wait_for_total(shared_holder["runnable"], 2)
        return value

    shared = RunnableLambda(_shared)
    first = shared.with_coalesce(backend=backend)
    second = shared.with_coalesce(backend=backend)
    shared_holder["runnable"] = first
    assert _gather([lambda: first.invoke("x"), lambda: second.invoke("x")]) == [
        "x",
        "x",
    ]
    assert calls["n"] == 1
    assert first.coalesce_info() == second.coalesce_info() == CoalesceStats(0, 1, 2)


def test_coalesce_clear_cancels_waiters_and_resets_stats() -> None:
    calls = {"n": 0}
    started = threading.Event()
    release = threading.Event()

    def _fn(value: str) -> str:
        calls["n"] += 1
        started.set()
        if not release.wait(_TIMEOUT):
            msg = "leader was not released"
            raise TimeoutError(msg)
        return value

    runnable = RunnableLambda(_fn).with_coalesce()
    leader_result: dict[str, Any] = {}
    joiner_error: list[BaseException] = []

    def _leader() -> None:
        leader_result["value"] = runnable.invoke("x")

    def _joiner() -> None:
        try:
            runnable.invoke("x")
        except BaseException as exc:
            joiner_error.append(exc)

    leader = threading.Thread(target=_leader)
    joiner = threading.Thread(target=_joiner)
    leader.start()
    assert started.wait(_TIMEOUT)
    joiner.start()
    deadline = time.monotonic() + _TIMEOUT
    while runnable.coalesce_info().coalesced < 1:
        if time.monotonic() > deadline:
            msg = "joiner did not register"
            raise AssertionError(msg)
        time.sleep(0.001)
    runnable.coalesce_clear()
    assert runnable.coalesce_info() == CoalesceStats(0, 0, 0)
    release.set()
    leader.join(_TIMEOUT)
    joiner.join(_TIMEOUT)
    assert not leader.is_alive()
    assert not joiner.is_alive()
    assert leader_result["value"] == "x"
    assert len(joiner_error) == 1
    assert isinstance(joiner_error[0], asyncio.CancelledError)
    assert calls["n"] == 1
    assert runnable.invoke("x") == "x"
    assert calls["n"] == 2


def test_with_coalesce_backend_argument_is_keyword_only() -> None:
    runnable = RunnableLambda(lambda value: value)

    def _call() -> None:
        runnable.with_coalesce(InMemoryCoalesceBackend())  # type: ignore[misc]

    with pytest.raises(TypeError):
        _call()
