import asyncio
import threading
import time
from collections.abc import AsyncGenerator, AsyncIterator, Callable, Iterator
from concurrent.futures import ThreadPoolExecutor
from typing import Any, cast
from uuid import UUID

import pytest
from typing_extensions import override

from langchain_core.callbacks import BaseCallbackHandler
from langchain_core.runnables import (
    CoalesceBackend,
    CoalesceStats,
    InMemoryCoalesceBackend,
    RunnableConfig,
    RunnableGenerator,
    RunnableLambda,
)
from langchain_core.runnables import __all__ as runnables_all
from langchain_core.runnables.coalesce import RunnableCoalesce, _coalesce_key


def _wait_until(predicate: Callable[[], bool], timeout: float = 5) -> None:
    deadline = time.monotonic() + timeout
    while not predicate():
        if time.monotonic() > deadline:
            msg = "Timed out waiting for condition."
            raise AssertionError(msg)
        time.sleep(0.001)


async def _await_until(predicate: Callable[[], bool], timeout: float = 5) -> None:
    deadline = time.monotonic() + timeout
    while not predicate():
        if time.monotonic() > deadline:
            msg = "Timed out waiting for condition."
            raise AssertionError(msg)
        await asyncio.sleep(0.001)


class _Gated:
    """Callable whose executions block until `release` is called."""

    def __init__(self, fn: Callable[[Any], Any] = lambda x: x) -> None:
        self.fn = fn
        self.calls: list[Any] = []
        self.gate = threading.Event()
        self._lock = threading.Lock()

    def release(self) -> None:
        self.gate.set()

    def __call__(self, x: Any) -> Any:
        with self._lock:
            self.calls.append(x)
        assert self.gate.wait(5)
        return self.fn(x)

    async def acall(self, x: Any) -> Any:
        with self._lock:
            self.calls.append(x)
        assert await asyncio.to_thread(self.gate.wait, 5)
        return self.fn(x)


class _RecordingHandler(BaseCallbackHandler):
    def __init__(self) -> None:
        self.starts: list[tuple[str | None, Any]] = []
        self.ends: list[Any] = []
        self.errors: list[BaseException] = []

    @override
    def on_chain_start(
        self,
        serialized: dict[str, Any] | None,
        inputs: Any,
        *,
        run_id: UUID,
        parent_run_id: UUID | None = None,
        **kwargs: Any,
    ) -> None:
        if parent_run_id is None:
            self.starts.append((kwargs.get("name"), inputs))

    @override
    def on_chain_end(
        self,
        outputs: Any,
        *,
        run_id: UUID,
        parent_run_id: UUID | None = None,
        **kwargs: Any,
    ) -> None:
        if parent_run_id is None:
            self.ends.append(outputs)

    @override
    def on_chain_error(
        self,
        error: BaseException,
        *,
        run_id: UUID,
        parent_run_id: UUID | None = None,
        **kwargs: Any,
    ) -> None:
        if parent_run_id is None:
            self.errors.append(error)


def test_only_backend_types_are_exported() -> None:
    assert {"CoalesceBackend", "CoalesceStats", "InMemoryCoalesceBackend"} <= set(
        runnables_all
    )
    assert "RunnableCoalesce" not in runnables_all


def test_with_coalesce_returns_wrapper() -> None:
    backend = InMemoryCoalesceBackend()
    runnable = RunnableLambda(lambda x: x)
    wrapped = runnable.with_coalesce(backend=backend)
    assert isinstance(wrapped, RunnableCoalesce)
    assert wrapped.backend is backend
    assert wrapped.bound is runnable
    assert isinstance(runnable.with_coalesce().backend, InMemoryCoalesceBackend)


def test_invoke_coalesces_concurrent_threads() -> None:
    gated = _Gated(lambda x: x * 2)
    wrapped = RunnableLambda(gated).with_coalesce()
    with ThreadPoolExecutor(max_workers=5) as pool:
        futures = [pool.submit(wrapped.invoke, 3) for _ in range(5)]
        _wait_until(lambda: wrapped.coalesce_info().coalesced == 4)
        gated.release()
        results = [f.result(timeout=5) for f in futures]
    assert results == [6] * 5
    assert gated.calls == [3]
    assert wrapped.coalesce_info() == CoalesceStats(active=0, coalesced=4, total=5)


async def test_ainvoke_coalesces_concurrent_tasks() -> None:
    calls = 0

    async def slow(x: int) -> int:
        nonlocal calls
        calls += 1
        await asyncio.sleep(0.05)
        return x + 1

    wrapped = RunnableLambda(slow).with_coalesce()
    results = await asyncio.gather(*(wrapped.ainvoke(1) for _ in range(5)))
    assert results == [2] * 5
    assert calls == 1
    assert wrapped.coalesce_info() == CoalesceStats(active=0, coalesced=4, total=5)


async def test_key_ignores_config_kwargs_and_dict_order() -> None:
    calls = 0

    async def slow(x: dict[str, Any]) -> dict[str, Any]:
        nonlocal calls
        calls += 1
        await asyncio.sleep(0.05)
        return x

    wrapped = RunnableLambda(slow).with_coalesce()
    results = await asyncio.gather(
        wrapped.ainvoke({"a": 1, "b": [1, {"x": 1, "y": 2}]}),
        wrapped.ainvoke(
            {"b": [1, {"y": 2, "x": 1}], "a": 1},
            {"tags": ["other"], "metadata": {"m": 1}, "run_name": "renamed"},
        ),
        wrapped.ainvoke({"a": 1, "b": [1, {"x": 1, "y": 2}]}, extra="ignored"),
    )
    assert calls == 1
    assert all(r == {"a": 1, "b": [1, {"x": 1, "y": 2}]} for r in results)


def test_key_distinguishes_different_values() -> None:
    assert _coalesce_key({"a": 1, "b": 2}) == _coalesce_key({"b": 2, "a": 1})
    assert _coalesce_key({"s": {1, 2, 3}}) == _coalesce_key({"s": {3, 2, 1}})
    distinct = [1, True, 1.0, "1", [1], (1,), {1: 1}, {"1": 1}, None]
    assert len({_coalesce_key(v) for v in distinct}) == len(distinct)
    cyclic: list[Any] = []
    cyclic.append(cyclic)
    assert isinstance(_coalesce_key(cyclic), str)


def test_completed_execution_does_not_coalesce_next_call() -> None:
    calls: list[int] = []

    def record(x: int) -> int:
        calls.append(x)
        return x

    wrapped = RunnableLambda(record).with_coalesce()
    assert wrapped.invoke(1) == 1
    assert wrapped.invoke(1) == 1
    assert calls == [1, 1]
    assert wrapped.coalesce_info() == CoalesceStats(active=0, coalesced=0, total=2)


async def test_leader_error_propagates_to_joiners_then_runs_fresh() -> None:
    calls = 0

    async def failing(x: int) -> int:
        nonlocal calls
        calls += 1
        await asyncio.sleep(0.05)
        if calls == 1:
            msg = "boom"
            raise ValueError(msg)
        return x

    wrapped = RunnableLambda(failing).with_coalesce()
    results = await asyncio.gather(
        *(wrapped.ainvoke(1) for _ in range(3)), return_exceptions=True
    )
    assert all(isinstance(r, ValueError) for r in results)
    assert calls == 1
    assert await wrapped.ainvoke(1) == 1
    assert calls == 2


def test_stream_joiner_replays_all_chunks_threads() -> None:
    started = threading.Event()
    gate = threading.Event()
    calls = 0

    def gen(inputs: Iterator[str]) -> Iterator[str]:
        nonlocal calls
        calls += 1
        for _ in inputs:
            pass
        yield "a"
        started.set()
        assert gate.wait(5)
        yield "b"
        yield "c"

    wrapped = RunnableGenerator(gen).with_coalesce()
    with ThreadPoolExecutor(max_workers=2) as pool:
        leader = pool.submit(lambda: list(wrapped.stream("x")))
        assert started.wait(5)
        joiner = pool.submit(lambda: list(wrapped.stream("x")))
        _wait_until(lambda: wrapped.coalesce_info().coalesced == 1)
        gate.set()
        assert leader.result(timeout=5) == ["a", "b", "c"]
        assert joiner.result(timeout=5) == ["a", "b", "c"]
    assert calls == 1


async def test_astream_joiner_replays_all_chunks() -> None:
    first_chunk = asyncio.Event()
    calls = 0

    async def agen(inputs: AsyncIterator[str]) -> AsyncIterator[str]:
        nonlocal calls
        calls += 1
        async for _ in inputs:
            pass
        yield "a"
        first_chunk.set()
        await asyncio.sleep(0.05)
        yield "b"
        yield "c"

    wrapped = RunnableGenerator(agen).with_coalesce()

    async def consume() -> list[str]:
        return [chunk async for chunk in wrapped.astream("x")]

    leader = asyncio.create_task(consume())
    await first_chunk.wait()
    joiner = asyncio.create_task(consume())
    assert await leader == ["a", "b", "c"]
    assert await joiner == ["a", "b", "c"]
    assert calls == 1
    assert wrapped.coalesce_info().coalesced == 1


async def test_invoke_and_stream_share_in_flight_state() -> None:
    calls = 0

    async def agen(inputs: AsyncIterator[str]) -> AsyncIterator[str]:
        nonlocal calls
        calls += 1
        async for _ in inputs:
            pass
        for chunk in ("he", "ll", "o"):
            await asyncio.sleep(0.02)
            yield chunk

    wrapped = RunnableGenerator(agen).with_coalesce()

    async def consume() -> list[str]:
        return [chunk async for chunk in wrapped.astream("x")]

    stream_task = asyncio.create_task(consume())
    await _await_until(lambda: wrapped.coalesce_info().active == 1)
    assert await wrapped.ainvoke("x") == "hello"
    assert await stream_task == ["he", "ll", "o"]
    assert calls == 1

    invoke_task = asyncio.create_task(wrapped.ainvoke("y"))
    await _await_until(lambda: wrapped.coalesce_info().active == 1)
    assert [chunk async for chunk in wrapped.astream("y")] == ["hello"]
    assert await invoke_task == "hello"
    assert calls == 2


def test_sync_invoke_joins_async_leader() -> None:
    gated = _Gated()
    wrapped = RunnableLambda(gated, afunc=gated.acall).with_coalesce()

    async def run_leader() -> Any:
        return await wrapped.ainvoke("k")

    with ThreadPoolExecutor(max_workers=2) as pool:
        leader = pool.submit(asyncio.run, run_leader())
        _wait_until(lambda: wrapped.coalesce_info().active == 1)
        joiner = pool.submit(wrapped.invoke, "k")
        _wait_until(lambda: wrapped.coalesce_info().coalesced == 1)
        gated.release()
        assert leader.result(timeout=5) == "k"
        assert joiner.result(timeout=5) == "k"
    assert gated.calls == ["k"]


def test_batch_coalesces_per_item_and_preserves_order() -> None:
    calls: list[int] = []

    def times_ten(x: int) -> int:
        calls.append(x)
        return x * 10

    wrapped = RunnableLambda(times_ten).with_coalesce()
    assert wrapped.batch([1, 2, 1, 3, 2]) == [10, 20, 10, 30, 20]
    assert sorted(calls) == [1, 2, 3]
    assert wrapped.coalesce_info() == CoalesceStats(active=0, coalesced=2, total=5)


def test_batch_joins_external_in_flight_invoke() -> None:
    gated = _Gated(lambda x: x * 10)
    wrapped = RunnableLambda(gated).with_coalesce()
    with ThreadPoolExecutor(max_workers=2) as pool:
        leader = pool.submit(wrapped.invoke, 1)
        _wait_until(lambda: gated.calls == [1])
        batch = pool.submit(wrapped.batch, [2, 1])
        _wait_until(lambda: wrapped.coalesce_info().coalesced == 1)
        gated.release()
        assert batch.result(timeout=5) == [20, 10]
        assert leader.result(timeout=5) == 10
    assert sorted(gated.calls) == [1, 2]


def test_batch_return_exceptions() -> None:
    def maybe_fail(x: int) -> int:
        if x == 2:
            msg = "two"
            raise ValueError(msg)
        return x

    wrapped = RunnableLambda(maybe_fail).with_coalesce()
    results = wrapped.batch([1, 2, 2], return_exceptions=True)
    assert results[0] == 1
    assert isinstance(results[1], ValueError)
    assert results[2] is results[1]
    with pytest.raises(ValueError, match="two"):
        wrapped.batch([1, 2])


async def test_abatch_coalesces_per_item_and_joins_external() -> None:
    calls: list[int] = []

    async def slow(x: int) -> int:
        calls.append(x)
        await asyncio.sleep(0.05)
        return x * 10

    wrapped = RunnableLambda(slow).with_coalesce()
    leader = asyncio.create_task(wrapped.ainvoke(4))
    await _await_until(lambda: calls == [4])
    assert await wrapped.abatch([1, 4, 2, 1]) == [10, 40, 20, 10]
    assert await leader == 40
    assert sorted(calls) == [1, 2, 4]
    assert wrapped.coalesce_info() == CoalesceStats(active=0, coalesced=2, total=5)


def test_batch_as_completed_yields_duplicates_consecutively() -> None:
    calls: list[str] = []

    def upper(x: str) -> str:
        calls.append(x)
        return x.upper()

    wrapped = RunnableLambda(upper).with_coalesce()
    results = list(wrapped.batch_as_completed(["a", "b", "a", "c", "b", "a"]))
    assert sorted(results) == [
        (0, "A"),
        (1, "B"),
        (2, "A"),
        (3, "C"),
        (4, "B"),
        (5, "A"),
    ]
    indices = [i for i, _ in results]
    for group in ([0, 2, 5], [1, 4]):
        start = indices.index(group[0])
        assert indices[start : start + len(group)] == group
    assert sorted(calls) == ["a", "b", "c"]


async def test_abatch_as_completed_yields_duplicates_consecutively() -> None:
    calls: list[str] = []

    async def slow(x: str) -> str:
        calls.append(x)
        await asyncio.sleep({"a": 0.03, "b": 0.01, "c": 0.02}[x])
        return x.upper()

    wrapped = RunnableLambda(slow).with_coalesce()
    results = [
        item async for item in wrapped.abatch_as_completed(["a", "b", "a", "c", "b"])
    ]
    assert results == [(1, "B"), (4, "B"), (3, "C"), (0, "A"), (2, "A")]
    assert sorted(calls) == ["a", "b", "c"]


async def test_abatch_as_completed_joins_external() -> None:
    calls: list[str] = []

    async def slow(x: str) -> str:
        calls.append(x)
        await asyncio.sleep(0.05)
        return x.upper()

    wrapped = RunnableLambda(slow).with_coalesce()
    leader = asyncio.create_task(wrapped.ainvoke("z"))
    await _await_until(lambda: calls == ["z"])
    results = [
        item
        async for item in wrapped.abatch_as_completed(
            ["z", "y", "z"], return_exceptions=True
        )
    ]
    assert sorted(results) == [(0, "Z"), (1, "Y"), (2, "Z")]
    assert await leader == "Z"
    assert sorted(calls) == ["y", "z"]


def test_joined_callers_fire_chain_callbacks() -> None:
    gated = _Gated(lambda x: x + 1)
    wrapped = RunnableLambda(gated, name="adder").with_coalesce()
    leader_handler = _RecordingHandler()
    joiner_handler = _RecordingHandler()
    with ThreadPoolExecutor(max_workers=2) as pool:
        leader = pool.submit(wrapped.invoke, 1, {"callbacks": [leader_handler]})
        _wait_until(lambda: gated.calls == [1])
        joiner = pool.submit(wrapped.invoke, 1, {"callbacks": [joiner_handler]})
        _wait_until(lambda: wrapped.coalesce_info().coalesced == 1)
        gated.release()
        assert leader.result(timeout=5) == 2
        assert joiner.result(timeout=5) == 2
    assert leader_handler.starts == [("adder", 1)]
    assert leader_handler.ends == [2]
    assert joiner_handler.starts == [("adder", 1)]
    assert joiner_handler.ends == [2]


async def test_joined_async_callers_fire_chain_callbacks() -> None:
    async def failing(_: int) -> int:
        await asyncio.sleep(0.05)
        msg = "nope"
        raise ValueError(msg)

    wrapped = RunnableLambda(failing).with_coalesce()
    ok = RunnableLambda(lambda x: x).with_coalesce()
    handler = _RecordingHandler()
    config: RunnableConfig = {"callbacks": [handler], "run_name": "joined"}

    leader = asyncio.create_task(wrapped.ainvoke(1))
    await _await_until(lambda: wrapped.coalesce_info().active == 1)
    with pytest.raises(ValueError, match="nope"):
        await wrapped.ainvoke(1, config)
    with pytest.raises(ValueError, match="nope"):
        await leader
    assert handler.starts == [("joined", 1)]
    assert len(handler.errors) == 1

    batch_handler = _RecordingHandler()
    assert await ok.abatch([5, 5], {"callbacks": [batch_handler]}) == [5, 5]
    assert len(batch_handler.starts) == 2
    assert batch_handler.ends == [5, 5]


async def test_coalesce_clear_cancels_waiters_and_resets_stats() -> None:
    release = asyncio.Event()

    async def blocked(x: int) -> int:
        await release.wait()
        return x

    wrapped = RunnableLambda(blocked).with_coalesce()
    leader = asyncio.create_task(wrapped.ainvoke(1))
    await _await_until(lambda: wrapped.coalesce_info().active == 1)
    joiner = asyncio.create_task(wrapped.ainvoke(1))
    await _await_until(lambda: wrapped.coalesce_info().coalesced == 1)
    await asyncio.sleep(0.01)

    wrapped.coalesce_clear()
    assert wrapped.coalesce_info() == CoalesceStats(active=0, coalesced=0, total=0)
    with pytest.raises(asyncio.CancelledError):
        await joiner

    release.set()
    assert await leader == 1
    assert wrapped.coalesce_info() == CoalesceStats(active=0, coalesced=0, total=0)
    assert await wrapped.ainvoke(1) == 1


def test_coalesce_clear_cancels_sync_waiters() -> None:
    gated = _Gated()
    wrapped = RunnableLambda(gated).with_coalesce()
    with ThreadPoolExecutor(max_workers=2) as pool:
        leader = pool.submit(wrapped.invoke, 1)
        _wait_until(lambda: gated.calls == [1])
        joiner = pool.submit(wrapped.invoke, 1)
        _wait_until(lambda: wrapped.coalesce_info().coalesced == 1)
        time.sleep(0.01)
        wrapped.coalesce_clear()
        with pytest.raises(asyncio.CancelledError):
            joiner.result(timeout=5)
        gated.release()
        assert leader.result(timeout=5) == 1


async def test_separate_wrappers_coalesce_independently() -> None:
    calls = 0

    async def slow(x: int) -> int:
        nonlocal calls
        calls += 1
        await asyncio.sleep(0.05)
        return x

    runnable = RunnableLambda(slow)
    first, second = runnable.with_coalesce(), runnable.with_coalesce()
    await asyncio.gather(first.ainvoke(1), second.ainvoke(1))
    assert calls == 2

    backend = InMemoryCoalesceBackend()
    shared_a = runnable.with_coalesce(backend=backend)
    shared_b = runnable.with_coalesce(backend=backend)
    await asyncio.gather(shared_a.ainvoke(1), shared_b.ainvoke(1))
    assert calls == 3
    assert shared_a.coalesce_info() == shared_b.coalesce_info() == backend.stats


def test_graph_and_metadata_delegate_to_bound() -> None:
    runnable = RunnableLambda(lambda x: x, name="inner") | RunnableLambda(
        lambda x: x, name="outer"
    )
    wrapped = runnable.with_coalesce()
    graph, bound_graph = wrapped.get_graph(), runnable.get_graph()
    assert [n.name for n in graph.nodes.values()] == [
        n.name for n in bound_graph.nodes.values()
    ]
    assert len(graph.edges) == len(bound_graph.edges)
    assert wrapped.get_name() == runnable.get_name()
    assert wrapped.InputType == runnable.InputType
    assert wrapped.OutputType == runnable.OutputType


async def test_transform_and_event_streaming_pass_through() -> None:
    def gen(inputs: Iterator[str]) -> Iterator[str]:
        for item in inputs:
            yield item.upper()

    async def agen(inputs: AsyncIterator[str]) -> AsyncIterator[str]:
        async for item in inputs:
            yield item.upper()

    async def ainputs() -> AsyncIterator[str]:
        for item in ("a", "b"):
            yield item

    wrapped = RunnableGenerator(gen, agen).with_coalesce()
    assert list(wrapped.transform(iter(["a", "b"]))) == ["A", "B"]
    assert [c async for c in wrapped.atransform(ainputs())] == ["A", "B"]
    events = [e async for e in wrapped.astream_events("a", version="v2")]
    assert events[0]["event"] == "on_chain_start"
    assert events[-1]["event"] == "on_chain_end"
    assert wrapped.coalesce_info().total == 0


async def test_leader_stream_closed_early_fails_joiners() -> None:
    async def agen(inputs: AsyncIterator[int]) -> AsyncIterator[int]:
        async for _ in inputs:
            pass
        for i in range(3):
            await asyncio.sleep(0.02)
            yield i

    wrapped = RunnableGenerator(agen).with_coalesce()
    leader_stream = cast("AsyncGenerator[int, None]", wrapped.astream(1))
    assert await leader_stream.__anext__() == 0
    joiner = asyncio.create_task(wrapped.ainvoke(1))
    await _await_until(lambda: wrapped.coalesce_info().coalesced == 1)
    await leader_stream.aclose()
    with pytest.raises(RuntimeError, match="abandoned"):
        await joiner
    assert await wrapped.ainvoke(1) == 3


def test_in_memory_backend_is_thread_safe() -> None:
    backend = InMemoryCoalesceBackend()
    barrier = threading.Barrier(16)

    def register() -> bool:
        barrier.wait()
        return backend.register("k")

    with ThreadPoolExecutor(max_workers=16) as pool:
        leads = list(pool.map(lambda _: register(), range(16)))
    assert leads.count(True) == 1
    assert backend.stats == CoalesceStats(active=1, coalesced=15, total=16)
    assert backend.is_active("k")
    backend.complete("k", result="done")
    assert not backend.is_active("k")
    with ThreadPoolExecutor(max_workers=15) as pool:
        assert list(pool.map(lambda _: backend.join("k"), range(15))) == ["done"] * 15


async def test_in_memory_backend_protocol() -> None:
    backend = InMemoryCoalesceBackend()
    assert await backend.aregister("k")
    assert not await backend.aregister("k")
    assert await backend.ais_active("k")
    await backend.acomplete("k", error=ValueError("bad"))
    assert not await backend.ais_active("k")
    with pytest.raises(ValueError, match="bad"):
        await backend.ajoin("k")
    with pytest.raises(KeyError):
        backend.join("missing")
    backend.complete("missing", result=1)
    assert backend.stats == CoalesceStats(active=0, coalesced=1, total=2)


class _DictBackend(CoalesceBackend):
    """Minimal backend implementing only the sync interface."""

    def __init__(self) -> None:
        self.cond = threading.Condition()
        self.active: set[str] = set()
        self.results: dict[str, tuple[Any, BaseException | None]] = {}
        self.counts = [0, 0]

    def register(self, key: str) -> bool:
        with self.cond:
            self.counts[1] += 1
            if key in self.active:
                self.counts[0] += 1
                return False
            self.active.add(key)
            return True

    def join(self, key: str) -> Any:
        with self.cond:
            self.cond.wait_for(lambda: key in self.results)
            result, error = self.results[key]
        if error is not None:
            raise error
        return result

    def complete(
        self, key: str, *, result: Any = None, error: BaseException | None = None
    ) -> None:
        with self.cond:
            self.active.discard(key)
            self.results[key] = (result, error)
            self.cond.notify_all()

    def is_active(self, key: str) -> bool:
        return key in self.active

    @property
    def stats(self) -> CoalesceStats:
        return CoalesceStats(len(self.active), self.counts[0], self.counts[1])


async def test_custom_backend_uses_default_async_methods() -> None:
    calls = 0

    async def slow(x: int) -> int:
        nonlocal calls
        calls += 1
        await asyncio.sleep(0.05)
        return x

    backend = _DictBackend()
    wrapped = RunnableLambda(slow).with_coalesce(backend=backend)
    assert list(await asyncio.gather(wrapped.ainvoke(7), wrapped.ainvoke(7))) == [7, 7]
    assert calls == 1
    assert wrapped.coalesce_info() == CoalesceStats(active=0, coalesced=1, total=2)
    with pytest.raises(NotImplementedError):
        wrapped.coalesce_clear()
