"""Unit tests for Runnable request coalescing."""

from __future__ import annotations

import asyncio
import threading
import time
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from collections.abc import AsyncIterator, Iterator

import pytest

from langchain_core.callbacks import BaseCallbackHandler
from langchain_core.runnables import (
    CoalesceBackend,
    CoalesceStats,
    InMemoryCoalesceBackend,
    RunnableLambda,
)
from langchain_core.runnables.base import Runnable
from langchain_core.runnables.coalesce import coalesce_key


class _Handler(BaseCallbackHandler):
    def __init__(self) -> None:
        self.starts = 0
        self.ends = 0
        self.errors = 0

    def on_chain_start(self, *_args: Any, **_kwargs: Any) -> None:
        self.starts += 1

    def on_chain_end(self, *_args: Any, **_kwargs: Any) -> None:
        self.ends += 1

    def on_chain_error(self, *_args: Any, **_kwargs: Any) -> None:
        self.errors += 1


def test_key_ignores_mapping_order() -> None:
    left = coalesce_key({"b": 1, "a": {"d": 2, "c": 3}})
    right = coalesce_key({"a": {"c": 3, "d": 2}, "b": 1})
    assert left == right
    assert coalesce_key({"a": 1}) != coalesce_key({"a": 2})


def test_concurrent_invoke_runs_once() -> None:
    started = threading.Event()
    release = threading.Event()
    calls: list[int] = []

    def _work(value: int) -> int:
        calls.append(value)
        started.set()
        assert release.wait(timeout=5)
        return value + 1

    runnable = RunnableLambda(_work).with_coalesce()
    results: list[int] = []

    def _call() -> None:
        results.append(runnable.invoke(1, config={"tags": ["x"]}))

    threads = [threading.Thread(target=_call) for _ in range(2)]
    for thread in threads:
        thread.start()
    assert started.wait(timeout=5)
    for _ in range(50):
        if runnable.coalesce_info().coalesced == 1:
            break
        time.sleep(0.01)
    assert runnable.coalesce_info().coalesced == 1
    release.set()
    for thread in threads:
        thread.join()

    assert calls == [1]
    assert sorted(results) == [2, 2]
    info = runnable.coalesce_info()
    assert info == CoalesceStats(active=0, coalesced=1, total=2)


def test_config_and_kwargs_do_not_split_keys() -> None:
    entered = threading.Event()
    release = threading.Event()
    calls = 0

    def _work(value: int) -> int:
        nonlocal calls
        calls += 1
        entered.set()
        release.wait(timeout=5)
        return value

    runnable = RunnableLambda(_work).with_coalesce()
    holder: list[int] = []

    def _first() -> None:
        holder.append(runnable.invoke(5, config={"metadata": {"a": 1}}))

    def _second() -> None:
        holder.append(runnable.invoke(5, config={"metadata": {"b": 2}}))

    leader = threading.Thread(target=_first)
    follower = threading.Thread(target=_second)
    leader.start()
    assert entered.wait(timeout=5)
    follower.start()
    for _ in range(50):
        if runnable.coalesce_info().coalesced == 1:
            break
        time.sleep(0.01)
    release.set()
    leader.join()
    follower.join()
    assert calls == 1
    assert holder == [5, 5]


def test_next_call_after_completion_runs_fresh() -> None:
    calls = 0

    def _work(value: int) -> int:
        nonlocal calls
        calls += 1
        return value

    runnable = RunnableLambda(_work).with_coalesce()
    assert runnable.invoke(1) == 1
    assert runnable.invoke(1) == 1
    assert calls == 2
    assert runnable.coalesce_info().active == 0


def test_stream_joiners_replay_chunks() -> None:
    started = threading.Event()
    release = threading.Event()
    calls = 0

    class _Stream(Runnable[int, str]):
        def invoke(self, input: int, _config: Any = None, **_kwargs: Any) -> str:
            return str(input)

        def stream(
            self, _input: int, _config: Any = None, **_kwargs: Any
        ) -> Iterator[str]:
            nonlocal calls
            calls += 1
            started.set()
            yield "a"
            assert release.wait(timeout=5)
            yield "b"

    runnable = _Stream().with_coalesce()
    leader: list[str] = []
    joined: list[str] = []

    def _lead() -> None:
        leader.extend(runnable.stream(1, config={"tags": ["leader"]}))

    def _join() -> None:
        joined.extend(runnable.stream(1, config={"tags": ["joiner"]}))

    thread = threading.Thread(target=_lead)
    thread.start()
    assert started.wait(timeout=5)
    joiner = threading.Thread(target=_join)
    joiner.start()
    for _ in range(50):
        if runnable.coalesce_info().coalesced == 1:
            break
        time.sleep(0.01)
    assert runnable.coalesce_info().coalesced == 1
    release.set()
    thread.join()
    joiner.join()
    assert leader == ["a", "b"]
    assert joined == ["a", "b"]
    assert calls == 1


def test_batch_coalesces_per_item_and_keeps_order() -> None:
    calls: list[int] = []

    def _work(value: int) -> int:
        calls.append(value)
        time.sleep(0.01)
        return value * 10

    runnable = RunnableLambda(_work).with_coalesce()
    assert runnable.batch([1, 2, 1, 2, 3]) == [10, 20, 10, 20, 30]
    assert sorted(calls) == [1, 2, 3]


def test_batch_as_completed_yields_duplicates_consecutively() -> None:
    def _work(value: int) -> int:
        time.sleep(0.05 if value == 1 else 0.01)
        return value

    runnable = RunnableLambda(_work).with_coalesce()
    inputs = [1, 2, 1, 3, 2]
    seen = list(runnable.batch_as_completed(inputs))
    positions: dict[int, list[int]] = {}
    for position, (index, output) in enumerate(seen):
        assert output == inputs[index]
        positions.setdefault(inputs[index], []).append(position)
    for spots in positions.values():
        assert max(spots) - min(spots) == len(spots) - 1


def test_joined_invoke_fires_callbacks() -> None:
    started = threading.Event()
    release = threading.Event()

    def _work(value: int) -> int:
        started.set()
        assert release.wait(timeout=5)
        return value + 1

    runnable = RunnableLambda(_work).with_coalesce()
    leader = threading.Thread(target=lambda: runnable.invoke(1))
    leader.start()
    assert started.wait(timeout=5)
    handler = _Handler()
    joined: list[int] = []

    def _follow() -> None:
        joined.append(runnable.invoke(1, config={"callbacks": [handler]}))

    follower = threading.Thread(target=_follow)
    follower.start()
    for _ in range(50):
        if runnable.coalesce_info().coalesced == 1:
            break
        time.sleep(0.01)
    release.set()
    leader.join()
    follower.join()
    assert joined == [2]
    assert handler.starts == 1
    assert handler.ends == 1
    assert handler.errors == 0


def test_clear_cancels_waiters_and_resets_stats() -> None:
    started = threading.Event()
    release = threading.Event()

    def _work(value: int) -> int:
        started.set()
        release.wait(timeout=5)
        return value

    runnable = RunnableLambda(_work).with_coalesce()
    leader = threading.Thread(target=lambda: runnable.invoke(1))
    leader.start()
    assert started.wait(timeout=5)
    errors: list[BaseException] = []

    def _join() -> None:
        try:
            runnable.invoke(1)
        except BaseException as exc:
            errors.append(exc)

    joiner = threading.Thread(target=_join)
    joiner.start()
    for _ in range(50):
        if runnable.coalesce_info().coalesced == 1:
            break
        time.sleep(0.01)
    assert runnable.coalesce_info().active == 1
    runnable.coalesce_clear()
    joiner.join(timeout=5)
    assert errors
    assert isinstance(errors[0], asyncio.CancelledError)
    assert runnable.coalesce_info() == CoalesceStats(0, 0, 0)
    release.set()
    leader.join(timeout=5)
    assert runnable.invoke(1) == 1


def test_wrappers_are_independent_unless_backend_is_shared() -> None:
    calls = 0
    started = threading.Event()
    release = threading.Event()

    def _work(value: int) -> int:
        nonlocal calls
        calls += 1
        started.set()
        release.wait(timeout=5)
        return value

    base = RunnableLambda(_work)
    first = base.with_coalesce()
    second = base.with_coalesce()
    thread = threading.Thread(target=lambda: first.invoke(1))
    thread.start()
    assert started.wait(timeout=5)
    release.set()
    assert second.invoke(1) == 1
    thread.join()
    assert calls == 2

    calls = 0
    started.clear()
    release.clear()
    backend = InMemoryCoalesceBackend()
    left = base.with_coalesce(backend=backend)
    right = base.with_coalesce(backend=backend)
    holder: list[int] = []
    thread = threading.Thread(target=lambda: holder.append(left.invoke(1)))
    follower = threading.Thread(target=lambda: holder.append(right.invoke(1)))
    thread.start()
    assert started.wait(timeout=5)
    follower.start()
    for _ in range(50):
        if left.coalesce_info().coalesced == 1:
            break
        time.sleep(0.01)
    release.set()
    thread.join()
    follower.join()
    assert holder == [1, 1]
    assert calls == 1
    assert left.coalesce_info() == right.coalesce_info()


def test_graph_delegates_to_bound() -> None:
    runnable = RunnableLambda(lambda value: value)
    wrapped = runnable.with_coalesce()
    assert wrapped.get_graph().to_json() == runnable.get_graph().to_json()


def test_transform_and_events_pass_through() -> None:
    calls = {"transform": 0, "events": 0}

    class _Inner(Runnable[int, int]):
        def invoke(self, input: int, _config: Any = None, **_kwargs: Any) -> int:
            return input + 1

        def transform(
            self, input: Iterator[int], _config: Any = None, **_kwargs: Any
        ) -> Iterator[int]:
            calls["transform"] += 1
            yield from (item + 1 for item in input)

        async def atransform(
            self, input: AsyncIterator[int], _config: Any = None, **_kwargs: Any
        ) -> AsyncIterator[int]:
            calls["transform"] += 1
            async for item in input:
                yield item + 1

        async def astream_events(
            self, input: Any, _config: Any = None, **_kwargs: Any
        ) -> AsyncIterator[dict[str, Any]]:
            calls["events"] += 1
            yield {"event": "on_chain_end", "data": {"output": input}}

    wrapped = _Inner().with_coalesce()
    assert list(wrapped.transform(iter([1, 2]))) == [2, 3]

    async def _exercise() -> None:
        assert [item async for item in wrapped.atransform(_agen([3]))] == [4]
        events = [event async for event in wrapped.astream_events(5, version="v2")]
        assert events[0]["data"]["output"] == 5

    asyncio.run(_exercise())
    assert calls == {"transform": 2, "events": 1}


def test_separate_methods_share_backend() -> None:
    started = threading.Event()
    release = threading.Event()
    calls = 0

    def _work(value: int) -> int:
        nonlocal calls
        calls += 1
        started.set()
        release.wait(timeout=5)
        return value + 3

    runnable = RunnableLambda(_work).with_coalesce()
    leader = threading.Thread(target=lambda: runnable.invoke(4))
    leader.start()
    assert started.wait(timeout=5)
    assert runnable.coalesce_info().active == 1

    async def _collect() -> list[int]:
        return [chunk async for chunk in runnable.astream(4)]

    async def _join_other_methods() -> None:
        invoke_result, stream_result = await asyncio.gather(
            runnable.ainvoke(4), _collect()
        )
        assert invoke_result == 7
        assert stream_result == [7]

    joiner = threading.Thread(target=lambda: asyncio.run(_join_other_methods()))
    joiner.start()
    for _ in range(50):
        if runnable.coalesce_info().coalesced >= 2:
            break
        time.sleep(0.01)
    release.set()
    leader.join()
    joiner.join()
    assert calls == 1


def test_in_memory_backend_threads() -> None:
    backend = InMemoryCoalesceBackend()
    assert isinstance(backend, CoalesceBackend)
    started = threading.Barrier(8)
    key = coalesce_key({"z": 1, "a": 2})

    def _worker() -> None:
        leader = backend.register(key)
        started.wait()
        if leader:
            time.sleep(0.05)
            backend.complete(key, result="ok")
        else:
            assert backend.join(key) == "ok"

    threads = [threading.Thread(target=_worker) for _ in range(8)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()
    assert backend.stats == CoalesceStats(active=0, coalesced=7, total=8)
    assert backend.is_active(key) is False
    assert backend.register(key) is True
    backend.complete(key, result=None)


async def _agen(items: list[int]) -> AsyncIterator[int]:
    for item in items:
        yield item


async def _async_batch_and_errors() -> None:
    calls: list[int] = []

    async def _work(value: int) -> int:
        calls.append(value)
        await asyncio.sleep(0.01)
        if value == 0:
            msg = "boom"
            raise ValueError(msg)
        return value

    runnable = RunnableLambda(_work).with_coalesce()
    assert await runnable.abatch([1, 1, 2]) == [1, 1, 2]
    assert sorted(calls) == [1, 2]
    with pytest.raises(ValueError, match="boom"):
        await runnable.abatch([0, 0])
    seen = [item async for item in runnable.abatch_as_completed([3, 4, 3])]
    positions: dict[int, list[int]] = {}
    for position, (_index, output) in enumerate(seen):
        positions.setdefault(output, []).append(position)
        assert output in {3, 4}
    assert max(positions[3]) - min(positions[3]) == 1


def test_async_batch_entrypoint() -> None:
    asyncio.run(_async_batch_and_errors())
