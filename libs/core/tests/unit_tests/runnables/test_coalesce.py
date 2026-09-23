"""Tests for request coalescing via `Runnable.with_coalesce`."""

import asyncio
import threading
import time
from collections.abc import AsyncIterator, Generator, Iterator
from concurrent.futures import ThreadPoolExecutor
from typing import Any, cast

import pytest

from langchain_core.callbacks import BaseCallbackHandler
from langchain_core.runnables import (
    CoalesceBackend,
    CoalesceStats,
    InMemoryCoalesceBackend,
    RunnableGenerator,
    RunnableLambda,
)
from langchain_core.runnables.coalesce import RunnableCoalesce, _coalesce_key


class _ChainCounter(BaseCallbackHandler):
    def __init__(self) -> None:
        self.starts = 0
        self.ends = 0
        self.errors = 0
        self._lock = threading.Lock()

    def on_chain_start(self, *_args: Any, **_kwargs: Any) -> None:
        with self._lock:
            self.starts += 1

    def on_chain_end(self, *_args: Any, **_kwargs: Any) -> None:
        with self._lock:
            self.ends += 1

    def on_chain_error(self, *_args: Any, **_kwargs: Any) -> None:
        with self._lock:
            self.errors += 1


def _async_counter(delay: float = 0.05) -> tuple[RunnableLambda, list[Any]]:
    calls: list[Any] = []

    async def _double(x: Any) -> Any:
        calls.append(x)
        await asyncio.sleep(delay)
        return x["v"] * 2 if isinstance(x, dict) else x * 2

    return RunnableLambda(_double), calls


def _sync_counter(delay: float = 0.1) -> tuple[RunnableLambda, list[Any]]:
    calls: list[Any] = []
    lock = threading.Lock()

    def _double(x: int) -> int:
        with lock:
            calls.append(x)
        time.sleep(delay)
        return x * 2

    return RunnableLambda(_double), calls


def test_with_coalesce_returns_wrapper() -> None:
    runnable = RunnableLambda(lambda x: x).with_coalesce()
    assert isinstance(runnable, RunnableCoalesce)
    assert isinstance(runnable.backend, InMemoryCoalesceBackend)
    assert runnable.coalesce_info() == CoalesceStats(active=0, coalesced=0, total=0)


def test_key_ignores_dict_order_but_not_values() -> None:
    assert _coalesce_key({"a": 1, "b": {"c": 2, "d": 3}}) == _coalesce_key(
        {"b": {"d": 3, "c": 2}, "a": 1}
    )
    assert _coalesce_key({"a": 1}) != _coalesce_key({"a": 2})
    assert _coalesce_key(1) != _coalesce_key(value=True)
    assert _coalesce_key(1) != _coalesce_key("1")


def test_sync_invoke_coalesces_concurrent_calls() -> None:
    inner, calls = _sync_counter()
    runnable = inner.with_coalesce()
    with ThreadPoolExecutor(max_workers=5) as pool:
        results = list(pool.map(runnable.invoke, [3] * 5))
    assert results == [6] * 5
    assert len(calls) == 1
    assert runnable.coalesce_info() == CoalesceStats(active=0, coalesced=4, total=5)


def test_sync_invoke_runs_fresh_after_completion() -> None:
    inner, calls = _sync_counter(delay=0)
    runnable = inner.with_coalesce()
    assert runnable.invoke(1) == 2
    assert runnable.invoke(1) == 2
    assert len(calls) == 2
    assert runnable.coalesce_info().coalesced == 0


def test_sync_invoke_propagates_errors_to_joiners() -> None:
    calls = 0

    def _fail(_: int) -> int:
        nonlocal calls
        calls += 1
        time.sleep(0.1)
        msg = "boom"
        raise ValueError(msg)

    runnable = RunnableLambda(_fail).with_coalesce()

    def _call(x: int) -> BaseException | None:
        try:
            runnable.invoke(x)
        except ValueError as e:
            return e
        return None

    with ThreadPoolExecutor(max_workers=3) as pool:
        errors = list(pool.map(_call, [1, 1, 1]))
    assert all(isinstance(e, ValueError) for e in errors)
    assert calls == 1
    assert runnable.coalesce_info().active == 0


async def test_ainvoke_coalesces_and_ignores_config_and_order() -> None:
    inner, calls = _async_counter()
    runnable = inner.with_coalesce()
    results = await asyncio.gather(
        runnable.ainvoke({"v": 2, "w": 1}),
        runnable.ainvoke({"w": 1, "v": 2}, {"tags": ["x"]}),
        runnable.ainvoke({"v": 2, "w": 1}, {"metadata": {"a": 1}}),
    )
    assert list(results) == [4, 4, 4]
    assert len(calls) == 1
    assert runnable.coalesce_info() == CoalesceStats(active=0, coalesced=2, total=3)


async def test_ainvoke_different_inputs_run_separately() -> None:
    inner, calls = _async_counter()
    runnable = inner.with_coalesce()
    assert list(await asyncio.gather(runnable.ainvoke(1), runnable.ainvoke(2))) == [
        2,
        4,
    ]
    assert len(calls) == 2


async def test_ainvoke_propagates_errors() -> None:
    async def _fail(_: int) -> int:
        await asyncio.sleep(0.05)
        msg = "boom"
        raise ValueError(msg)

    runnable = RunnableLambda(_fail).with_coalesce()
    results = await asyncio.gather(
        runnable.ainvoke(1), runnable.ainvoke(1), return_exceptions=True
    )
    assert all(isinstance(r, ValueError) for r in results)
    assert runnable.coalesce_info().active == 0


async def test_joined_callers_fire_chain_callbacks() -> None:
    inner, _ = _async_counter()
    runnable = inner.with_coalesce()
    leader, joiner = _ChainCounter(), _ChainCounter()
    task = asyncio.create_task(runnable.ainvoke(1, {"callbacks": [leader]}))
    await asyncio.sleep(0.01)
    assert await runnable.ainvoke(1, {"callbacks": [joiner]}) == 2
    await task
    assert (joiner.starts, joiner.ends) == (1, 1)
    assert leader.starts >= 1


def test_sync_joined_callers_fire_chain_error_callbacks() -> None:
    def _fail(_: int) -> int:
        time.sleep(0.1)
        msg = "boom"
        raise ValueError(msg)

    runnable = RunnableLambda(_fail).with_coalesce()
    handler = _ChainCounter()
    with ThreadPoolExecutor(max_workers=1) as pool:
        future = pool.submit(runnable.invoke, 1)
        time.sleep(0.03)
        with pytest.raises(ValueError, match="boom"):
            runnable.invoke(1, {"callbacks": [handler]})
        with pytest.raises(ValueError, match="boom"):
            future.result()
    assert (handler.starts, handler.errors) == (1, 1)


def _slow_gen(calls: list[int]) -> RunnableGenerator:
    def _gen(inputs: Iterator[int]) -> Iterator[str]:
        value = next(iter(inputs))
        calls.append(value)
        for ch in "abc":
            time.sleep(0.03)
            yield ch

    async def _agen(inputs: AsyncIterator[int]) -> AsyncIterator[str]:
        value = await anext(inputs)
        calls.append(value)
        for ch in "abc":
            await asyncio.sleep(0.02)
            yield ch

    return RunnableGenerator(_gen, _agen)


def test_stream_joiners_replay_all_chunks() -> None:
    calls: list[int] = []
    runnable = _slow_gen(calls).with_coalesce()
    with ThreadPoolExecutor(max_workers=3) as pool:
        results = list(pool.map(lambda x: list(runnable.stream(x)), [1, 1, 1]))
    assert results == [["a", "b", "c"]] * 3
    assert len(calls) == 1


async def test_astream_joiners_replay_all_chunks() -> None:
    calls: list[int] = []
    runnable = _slow_gen(calls).with_coalesce()

    async def _collect() -> list[str]:
        return [chunk async for chunk in runnable.astream(1)]

    first = asyncio.create_task(_collect())
    await asyncio.sleep(0.03)  # the leader has already emitted a chunk
    second = await _collect()
    assert await first == ["a", "b", "c"]
    assert second == ["a", "b", "c"]
    assert len(calls) == 1


async def test_invoke_joins_in_flight_stream() -> None:
    calls: list[int] = []
    runnable = _slow_gen(calls).with_coalesce()

    async def _collect() -> list[str]:
        return [chunk async for chunk in runnable.astream(1)]

    stream_task = asyncio.create_task(_collect())
    await asyncio.sleep(0.01)
    assert await runnable.ainvoke(1) == "abc"
    assert await stream_task == ["a", "b", "c"]
    assert len(calls) == 1


async def test_astream_joins_in_flight_invoke() -> None:
    inner, calls = _async_counter()
    runnable = inner.with_coalesce()
    task = asyncio.create_task(runnable.ainvoke(5))
    await asyncio.sleep(0.01)
    assert [c async for c in runnable.astream(5)] == [10]
    assert await task == 10
    assert len(calls) == 1


def test_abandoned_stream_releases_joiners() -> None:
    calls: list[int] = []
    runnable = _slow_gen(calls).with_coalesce()
    stream = cast("Generator[str, None, None]", runnable.stream(1))
    assert next(stream) == "a"
    with ThreadPoolExecutor(max_workers=1) as pool:
        future = pool.submit(runnable.invoke, 1)
        time.sleep(0.03)
        stream.close()
        with pytest.raises(RuntimeError, match="abandoned"):
            future.result(timeout=2)
    assert runnable.coalesce_info().active == 0


def test_batch_coalesces_per_item_and_preserves_order() -> None:
    inner, calls = _sync_counter(delay=0)
    runnable = inner.with_coalesce()
    assert runnable.batch([1, 2, 1, 3, 2]) == [2, 4, 2, 6, 4]
    assert sorted(calls) == [1, 2, 3]
    assert runnable.coalesce_info() == CoalesceStats(active=0, coalesced=2, total=5)


def test_batch_return_exceptions() -> None:
    def _maybe_fail(x: int) -> int:
        if x == 2:
            msg = "two"
            raise ValueError(msg)
        return x

    runnable = RunnableLambda(_maybe_fail).with_coalesce()
    results = runnable.batch([1, 2, 2], return_exceptions=True)
    assert results[0] == 1
    assert isinstance(results[1], ValueError)
    assert isinstance(results[2], ValueError)
    with pytest.raises(ValueError, match="two"):
        runnable.batch([1, 2])
    assert runnable.coalesce_info().active == 0


async def test_abatch_coalesces_per_item_and_preserves_order() -> None:
    inner, calls = _async_counter()
    runnable = inner.with_coalesce()
    assert await runnable.abatch([1, 2, 1, 3, 2]) == [2, 4, 2, 6, 4]
    assert sorted(calls) == [1, 2, 3]


async def test_abatch_joins_in_flight_invoke() -> None:
    inner, calls = _async_counter()
    runnable = inner.with_coalesce()
    task = asyncio.create_task(runnable.ainvoke(1))
    await asyncio.sleep(0.01)
    assert await runnable.abatch([1, 2]) == [2, 4]
    assert await task == 2
    assert sorted(calls) == [1, 2]


def test_batch_as_completed_yields_duplicates_consecutively() -> None:
    inner, calls = _sync_counter(delay=0)
    runnable = inner.with_coalesce()
    results = list(runnable.batch_as_completed([1, 2, 1, 2, 1]))
    assert sorted(results) == [(0, 2), (1, 4), (2, 2), (3, 4), (4, 2)]
    assert sorted(calls) == [1, 2]
    order = [index for index, _ in results]
    for group in ([0, 2, 4], [1, 3]):
        positions = sorted(order.index(i) for i in group)
        assert positions == list(range(positions[0], positions[0] + len(group)))


async def test_abatch_as_completed_yields_duplicates_consecutively() -> None:
    inner, calls = _async_counter()
    runnable = inner.with_coalesce()
    results = [r async for r in runnable.abatch_as_completed([1, 2, 1, 2, 1])]
    assert sorted(results) == [(0, 2), (1, 4), (2, 2), (3, 4), (4, 2)]
    assert sorted(calls) == [1, 2]
    order = [index for index, _ in results]
    for group in ([0, 2, 4], [1, 3]):
        positions = sorted(order.index(i) for i in group)
        assert positions == list(range(positions[0], positions[0] + len(group)))


async def test_coalesce_clear_cancels_waiters_and_resets_stats() -> None:
    gate = asyncio.Event()

    async def _wait(x: int) -> int:
        await gate.wait()
        return x

    runnable = RunnableLambda(_wait).with_coalesce()
    leader = asyncio.create_task(runnable.ainvoke(1))
    await asyncio.sleep(0.01)
    joiner = asyncio.create_task(runnable.ainvoke(1))
    await asyncio.sleep(0.01)
    assert runnable.coalesce_info() == CoalesceStats(active=1, coalesced=1, total=2)

    runnable.coalesce_clear()
    with pytest.raises(asyncio.CancelledError):
        await joiner
    assert runnable.coalesce_info() == CoalesceStats(active=0, coalesced=0, total=0)

    gate.set()
    assert await leader == 1
    assert runnable.coalesce_info().active == 0


async def test_separate_wrappers_are_independent_unless_sharing_backend() -> None:
    inner, calls = _async_counter()
    first, second = inner.with_coalesce(), inner.with_coalesce()
    await asyncio.gather(first.ainvoke(1), second.ainvoke(1))
    assert len(calls) == 2

    shared = InMemoryCoalesceBackend()
    first, second = (
        inner.with_coalesce(backend=shared),
        inner.with_coalesce(backend=shared),
    )
    await asyncio.gather(first.ainvoke(1), second.ainvoke(1))
    assert len(calls) == 3
    assert shared.stats == CoalesceStats(active=0, coalesced=1, total=2)


def test_transform_passes_through() -> None:
    calls: list[int] = []
    runnable = _slow_gen(calls).with_coalesce()
    assert list(runnable.transform(iter([1]))) == ["a", "b", "c"]
    assert list(runnable.transform(iter([1]))) == ["a", "b", "c"]
    assert runnable.coalesce_info().total == 0


async def test_astream_events_passes_through() -> None:
    inner, _ = _async_counter(delay=0)
    runnable = inner.with_coalesce()
    events = [e async for e in runnable.astream_events(1, version="v2")]
    assert events[-1]["event"] == "on_chain_end"
    assert events[-1]["data"]["output"] == 2
    assert runnable.coalesce_info().total == 0


def test_graph_delegates_to_bound() -> None:
    inner = RunnableLambda(lambda x: x)
    runnable = inner.with_coalesce()
    assert runnable.get_graph().to_json() == inner.get_graph().to_json()
    assert runnable.get_name() == inner.get_name()


def test_backend_protocol_and_sync_async_interop() -> None:
    backend = InMemoryCoalesceBackend()
    assert isinstance(backend, CoalesceBackend)
    assert backend.register("k") is True
    assert backend.is_active("k")
    assert backend.register("k") is False

    async def _join() -> Any:
        assert await backend.ais_active("k")
        return await backend.ajoin("k")

    with ThreadPoolExecutor(max_workers=1) as pool:
        future = pool.submit(asyncio.run, _join())
        time.sleep(0.05)
        backend.complete("k", result=42)
        assert future.result(timeout=2) == 42
    assert not backend.is_active("k")
    assert backend.stats == CoalesceStats(active=0, coalesced=1, total=2)


async def test_default_async_methods_delegate_to_sync() -> None:
    class _DictBackend(CoalesceBackend):
        def __init__(self) -> None:
            self.results: dict[str, Any] = {}
            self.active: set[str] = set()

        def register(self, key: str) -> bool:
            if key in self.active:
                return False
            self.active.add(key)
            return True

        def join(self, key: str) -> Any:
            return self.results[key]

        def complete(
            self,
            key: str,
            *,
            result: Any = None,
            error: BaseException | None = None,  # noqa: ARG002
        ) -> None:
            self.active.discard(key)
            self.results[key] = result

        def is_active(self, key: str) -> bool:
            return key in self.active

        @property
        def stats(self) -> CoalesceStats:
            return CoalesceStats(len(self.active), 0, 0)

    backend = _DictBackend()
    assert await backend.aregister("k") is True
    assert await backend.ais_active("k")
    await backend.acomplete("k", result=1)
    assert await backend.ajoin("k") == 1
    with pytest.raises(NotImplementedError):
        backend.clear()

    def _increment(x: int) -> int:
        return x + 1

    runnable = RunnableLambda(_increment).with_coalesce(backend=backend)
    assert await runnable.ainvoke(1) == 2
