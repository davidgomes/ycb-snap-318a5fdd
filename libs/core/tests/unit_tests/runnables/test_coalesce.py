import asyncio
import threading
import time
from collections.abc import AsyncIterator, Iterator
from typing import Any, cast

import pytest

from langchain_core.callbacks import BaseCallbackHandler
from langchain_core.runnables import (
    CoalesceBackend,
    CoalesceStats,
    InMemoryCoalesceBackend,
    Runnable,
    RunnableGenerator,
    RunnableLambda,
)
from langchain_core.runnables.coalesce import RunnableCoalesce


def _wrap(runnable: Runnable) -> RunnableCoalesce:
    return cast("RunnableCoalesce", runnable.with_coalesce())


def _slow_counter(delay: float = 0.1) -> tuple[RunnableLambda, list[Any]]:
    calls: list[Any] = []

    def fn(x: Any) -> Any:
        calls.append(x)
        time.sleep(delay)
        return {"out": x}

    async def afn(x: Any) -> Any:
        calls.append(x)
        await asyncio.sleep(delay)
        return {"out": x}

    return RunnableLambda(fn, afunc=afn), calls


def test_invoke_threads_coalesce() -> None:
    runnable, calls = _slow_counter()
    wrapped = _wrap(runnable)
    results: list[Any] = []

    def call() -> None:
        results.append(wrapped.invoke({"a": 1, "b": 2}))

    threads = [threading.Thread(target=call) for _ in range(5)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    assert len(calls) == 1
    assert results == [{"out": {"a": 1, "b": 2}}] * 5
    assert wrapped.coalesce_info() == CoalesceStats(active=0, coalesced=4, total=5)

    wrapped.invoke({"a": 1, "b": 2})
    assert len(calls) == 2


async def test_ainvoke_coalesce_key_ignores_order_and_config() -> None:
    runnable, calls = _slow_counter()
    wrapped = _wrap(runnable)
    results = await asyncio.gather(
        wrapped.ainvoke({"a": 1, "b": 2}),
        wrapped.ainvoke({"b": 2, "a": 1}, {"tags": ["x"]}),
        wrapped.ainvoke({"a": 2}),
    )
    assert len(calls) == 2
    assert results[0] == results[1]


async def test_errors_propagate_to_joiners() -> None:
    async def boom(_: int) -> int:
        await asyncio.sleep(0.05)
        msg = "boom"
        raise ValueError(msg)

    wrapped = _wrap(RunnableLambda(boom))
    results = await asyncio.gather(
        wrapped.ainvoke(1), wrapped.ainvoke(1), return_exceptions=True
    )
    assert all(isinstance(r, ValueError) for r in results)
    assert wrapped.coalesce_info().active == 0


def test_stream_joiner_replays_chunks() -> None:
    calls = []

    def gen(inputs: Iterator[str]) -> Iterator[str]:
        for x in inputs:
            calls.append(x)
            for c in x:
                time.sleep(0.03)
                yield c

    wrapped = RunnableGenerator(gen).with_coalesce()
    results: list[list[str]] = []

    def call() -> None:
        results.append(list(wrapped.stream("abc")))

    threads = [threading.Thread(target=call) for _ in range(3)]
    for t in threads:
        t.start()
        time.sleep(0.01)
    for t in threads:
        t.join()
    assert calls == ["abc"]
    assert results == [["a", "b", "c"]] * 3


async def test_astream_and_ainvoke_share_backend() -> None:
    calls = []

    async def gen(inputs: AsyncIterator[str]) -> AsyncIterator[str]:
        async for x in inputs:
            calls.append(x)
            for c in x:
                await asyncio.sleep(0.02)
                yield c

    wrapped = RunnableGenerator(gen).with_coalesce()

    async def collect() -> list[str]:
        return [c async for c in wrapped.astream("abc")]

    streamed, invoked = await asyncio.gather(collect(), wrapped.ainvoke("abc"))
    assert calls == ["abc"]
    assert streamed == ["a", "b", "c"]
    assert invoked == "abc"


def test_batch_coalesces_per_item_and_preserves_order() -> None:
    runnable, calls = _slow_counter()
    wrapped = _wrap(runnable)
    assert wrapped.batch([1, 2, 1, 3, 2]) == [{"out": x} for x in [1, 2, 1, 3, 2]]
    assert sorted(calls) == [1, 2, 3]
    assert wrapped.coalesce_info().coalesced == 2


async def test_abatch_coalesces() -> None:
    runnable, calls = _slow_counter()
    wrapped = _wrap(runnable)
    assert await wrapped.abatch([1, 1, 2]) == [{"out": 1}, {"out": 1}, {"out": 2}]
    assert sorted(calls) == [1, 2]


def test_batch_as_completed_consecutive() -> None:
    runnable, calls = _slow_counter()
    wrapped = _wrap(runnable)
    out = list(wrapped.batch_as_completed([1, 2, 1]))
    assert sorted(out) == [(0, {"out": 1}), (1, {"out": 2}), (2, {"out": 1})]
    idxs = [i for i, _ in out]
    assert abs(idxs.index(0) - idxs.index(2)) == 1
    assert sorted(calls) == [1, 2]


async def test_abatch_as_completed_consecutive() -> None:
    runnable, calls = _slow_counter()
    wrapped = _wrap(runnable)
    out = [item async for item in wrapped.abatch_as_completed([1, 2, 1])]
    idxs = [i for i, _ in out]
    assert sorted(idxs) == [0, 1, 2]
    assert abs(idxs.index(0) - idxs.index(2)) == 1
    assert sorted(calls) == [1, 2]


def test_joined_callers_fire_callbacks() -> None:
    class Recorder(BaseCallbackHandler):
        def __init__(self) -> None:
            self.starts = 0
            self.ends = 0

        def on_chain_start(self, *_: Any, **kwargs: Any) -> None:
            if kwargs.get("parent_run_id") is None:
                self.starts += 1

        def on_chain_end(self, *_: Any, **kwargs: Any) -> None:
            if kwargs.get("parent_run_id") is None:
                self.ends += 1

    runnable, calls = _slow_counter()
    wrapped = _wrap(runnable)
    recorder = Recorder()
    wrapped.batch([1, 1, 1], {"callbacks": [recorder]})
    assert len(calls) == 1
    assert recorder.starts == 3
    assert recorder.ends == 3


async def test_clear_cancels_waiters() -> None:
    event = asyncio.Event()

    async def wait(x: int) -> int:
        await event.wait()
        return x

    wrapped = _wrap(RunnableLambda(wait))
    owner = asyncio.ensure_future(wrapped.ainvoke(1))
    await asyncio.sleep(0.01)
    joiner = asyncio.ensure_future(wrapped.ainvoke(1))
    await asyncio.sleep(0.01)
    wrapped.coalesce_clear()
    with pytest.raises(asyncio.CancelledError):
        await joiner
    assert wrapped.coalesce_info() == CoalesceStats(0, 0, 0)
    event.set()
    assert await owner == 1


def test_separate_and_shared_backends() -> None:
    runnable, calls = _slow_counter()
    a = runnable.with_coalesce()
    b = runnable.with_coalesce()
    threads = [
        threading.Thread(target=a.invoke, args=(1,)),
        threading.Thread(target=b.invoke, args=(1,)),
    ]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    assert len(calls) == 2

    backend = InMemoryCoalesceBackend()
    assert isinstance(backend, CoalesceBackend)
    a = runnable.with_coalesce(backend=backend)
    b = runnable.with_coalesce(backend=backend)
    threads = [
        threading.Thread(target=a.invoke, args=(1,)),
        threading.Thread(target=b.invoke, args=(1,)),
    ]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    assert len(calls) == 3


def test_graph_delegates() -> None:
    runnable, _ = _slow_counter()
    wrapped_graph = runnable.with_coalesce().get_graph()
    graph = runnable.get_graph()
    assert [n.name for n in wrapped_graph.nodes.values()] == [
        n.name for n in graph.nodes.values()
    ]
    assert len(wrapped_graph.edges) == len(graph.edges)


def test_backend_api() -> None:
    backend = InMemoryCoalesceBackend()
    assert backend.register("k") is True
    assert backend.is_active("k")
    assert backend.register("k") is False
    backend.complete("k", result=42)
    assert not backend.is_active("k")
    assert backend.join("k") == 42
    assert backend.stats == CoalesceStats(active=0, coalesced=1, total=2)


async def test_backend_async_api() -> None:
    backend = InMemoryCoalesceBackend()
    assert await backend.aregister("k") is True
    assert await backend.aregister("k") is False
    assert await backend.ais_active("k")
    task = asyncio.ensure_future(backend.ajoin("k"))
    await asyncio.sleep(0)
    await backend.acomplete("k", error=ValueError("x"))
    with pytest.raises(ValueError, match="x"):
        await task
