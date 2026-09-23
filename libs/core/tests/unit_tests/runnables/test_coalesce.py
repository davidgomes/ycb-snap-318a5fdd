import asyncio
import threading
import time
from collections.abc import AsyncIterator, Callable, Hashable, Iterator
from concurrent.futures import Future, ThreadPoolExecutor
from typing import Any

import pytest
from typing_extensions import override

from langchain_core.messages import AIMessage, HumanMessage
from langchain_core.runnables import (
    CoalesceBackend,
    CoalesceStats,
    InMemoryCoalesceBackend,
    Runnable,
    RunnableConfig,
    RunnableGenerator,
    RunnableLambda,
)
from langchain_core.runnables.coalesce import RunnableCoalesce
from langchain_core.utils.aiter import aclosing
from tests.unit_tests.fake.callbacks import FakeCallbackHandler

TIMEOUT = 5


def _wait_until(predicate: Callable[[], bool]) -> None:
    deadline = time.monotonic() + TIMEOUT
    while not predicate():
        if time.monotonic() > deadline:
            msg = "Timed out waiting for condition."
            raise AssertionError(msg)
        time.sleep(0.001)


async def _await_until(predicate: Callable[[], bool]) -> None:
    deadline = time.monotonic() + TIMEOUT
    while not predicate():
        if time.monotonic() > deadline:
            msg = "Timed out waiting for condition."
            raise AssertionError(msg)
        await asyncio.sleep(0.001)


class _GatedDouble:
    """Doubles its input once `release` is set, recording every execution."""

    def __init__(self) -> None:
        self.release = threading.Event()
        self.calls: list[Any] = []
        self._lock = threading.Lock()

    def __call__(self, x: int) -> int:
        with self._lock:
            self.calls.append(x)
        assert self.release.wait(TIMEOUT)
        return x * 2


class _AsyncGatedDouble:
    def __init__(self) -> None:
        self.release = asyncio.Event()
        self.calls: list[Any] = []

    async def __call__(self, x: int) -> int:
        self.calls.append(x)
        await asyncio.wait_for(self.release.wait(), TIMEOUT)
        return x * 2


class _Recorder(Runnable[Any, int]):
    """Records the input and kwargs of every execution."""

    def __init__(self) -> None:
        self.release = threading.Event()
        self.calls: list[tuple[Any, dict[str, Any]]] = []

    @override
    def invoke(
        self, input: Any, config: RunnableConfig | None = None, **kwargs: Any
    ) -> int:
        self.calls.append((input, kwargs))
        assert self.release.wait(TIMEOUT)
        return len(self.calls)


async def _collect(iterator: AsyncIterator[Any]) -> list[Any]:
    return [chunk async for chunk in iterator]


def _double(x: int) -> int:
    return x * 2


def _recording(calls: list[Any]) -> RunnableLambda[Any, Any]:
    def record(x: Any) -> Any:
        calls.append(x)
        return x

    return RunnableLambda(record)


# --- InMemoryCoalesceBackend ---


def test_backend_register_join_complete() -> None:
    backend = InMemoryCoalesceBackend()

    assert backend.register("k") is True
    assert backend.is_active("k")
    assert backend.register("k") is False
    assert backend.stats == CoalesceStats(active=1, coalesced=1, total=2)

    with ThreadPoolExecutor(1) as executor:
        joined = executor.submit(backend.join, "k")
        backend.complete("k", result=42)
        assert joined.result(timeout=TIMEOUT) == 42

    assert not backend.is_active("k")
    assert backend.stats == CoalesceStats(active=0, coalesced=1, total=2)
    assert backend.register("k") is True


def test_backend_join_after_owner_completed() -> None:
    backend = InMemoryCoalesceBackend()
    backend.register("k")
    backend.register("k")
    backend.register("k")

    backend.complete("k", result="done")

    assert backend.join("k") == "done"
    assert backend.join("k") == "done"
    with pytest.raises(KeyError):
        backend.join("k")


def test_backend_join_raises_owner_error() -> None:
    backend = InMemoryCoalesceBackend()
    backend.register("k")
    backend.register("k")
    error = ValueError("boom")

    backend.complete("k", error=error)

    with pytest.raises(ValueError, match="boom") as exc_info:
        backend.join("k")
    assert exc_info.value is error


def test_backend_complete_unknown_key_is_noop() -> None:
    backend = InMemoryCoalesceBackend()
    backend.complete("missing", result=1)
    assert backend.stats == CoalesceStats(active=0, coalesced=0, total=0)


def test_backend_register_is_thread_safe() -> None:
    backend = InMemoryCoalesceBackend()
    barrier = threading.Barrier(32)

    def register() -> bool:
        barrier.wait(TIMEOUT)
        return backend.register("k")

    with ThreadPoolExecutor(32) as executor:
        owned = list(executor.map(lambda _: register(), range(32)))

    assert owned.count(True) == 1
    assert backend.stats == CoalesceStats(active=1, coalesced=31, total=32)


async def test_backend_async_methods() -> None:
    backend = InMemoryCoalesceBackend()

    assert await backend.aregister("k") is True
    assert await backend.aregister("k") is False
    assert await backend.ais_active("k")

    joined = asyncio.create_task(backend.ajoin("k"))
    await asyncio.sleep(0)
    await backend.acomplete("k", result=[1, 2])

    assert await joined == [1, 2]
    assert not await backend.ais_active("k")


async def test_backend_sync_owner_releases_async_joiner() -> None:
    backend = InMemoryCoalesceBackend()
    backend.register("k")
    backend.register("k")

    joined = asyncio.create_task(backend.ajoin("k"))
    await asyncio.sleep(0)
    thread = threading.Thread(
        target=backend.complete, args=("k",), kwargs={"result": 7}
    )
    thread.start()

    assert await asyncio.wait_for(joined, TIMEOUT) == 7
    thread.join()


async def test_backend_clear_cancels_waiters_and_resets_stats() -> None:
    backend = InMemoryCoalesceBackend()
    backend.register("k")
    backend.register("k")
    backend.register("k")

    with ThreadPoolExecutor(1) as executor:
        sync_joined = executor.submit(backend.join, "k")
        async_joined = asyncio.create_task(backend.ajoin("k"))
        await asyncio.sleep(0)

        backend.clear()

        with pytest.raises(asyncio.CancelledError):
            sync_joined.result(timeout=TIMEOUT)
        with pytest.raises(asyncio.CancelledError):
            await asyncio.wait_for(async_joined, TIMEOUT)

    assert backend.stats == CoalesceStats(active=0, coalesced=0, total=0)
    assert not backend.is_active("k")
    assert backend.register("k") is True


class _SyncOnlyBackend(CoalesceBackend):
    """Implements only the abstract methods, delegating to an in-memory backend."""

    def __init__(self) -> None:
        self.inner = InMemoryCoalesceBackend()
        self.registered: list[Hashable] = []

    @override
    def register(self, key: Hashable) -> bool:
        self.registered.append(key)
        return self.inner.register(key)

    @override
    def join(self, key: Hashable) -> Any:
        return self.inner.join(key)

    @override
    def complete(
        self,
        key: Hashable,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        self.inner.complete(key, result=result, error=error)

    @override
    def is_active(self, key: Hashable) -> bool:
        return self.inner.is_active(key)

    @property
    @override
    def stats(self) -> CoalesceStats:
        return self.inner.stats


async def test_backend_default_async_methods_delegate_to_sync() -> None:
    backend = _SyncOnlyBackend()

    assert await backend.aregister("k") is True
    assert await backend.aregister("k") is False
    assert await backend.ais_active("k")
    joined = asyncio.create_task(backend.ajoin("k"))
    await backend.acomplete("k", result="ok")

    assert await asyncio.wait_for(joined, TIMEOUT) == "ok"
    assert not await backend.ais_active("k")
    with pytest.raises(NotImplementedError):
        backend.clear()


# --- RunnableCoalesce ---


def test_with_coalesce_returns_wrapper() -> None:
    bound = RunnableLambda(lambda x: x)
    backend = InMemoryCoalesceBackend()

    wrapper = bound.with_coalesce(backend=backend)

    assert isinstance(wrapper, RunnableCoalesce)
    assert wrapper.bound is bound
    assert wrapper.backend is backend
    assert isinstance(bound.with_coalesce().backend, InMemoryCoalesceBackend)


def test_invoke_coalesces_concurrent_calls() -> None:
    func = _GatedDouble()
    wrapper = RunnableLambda(func).with_coalesce()

    with ThreadPoolExecutor(5) as executor:
        futures = [executor.submit(wrapper.invoke, 3) for _ in range(5)]
        _wait_until(lambda: wrapper.coalesce_info().coalesced == 4)
        func.release.set()
        results = [f.result(timeout=TIMEOUT) for f in futures]

    assert results == [6] * 5
    assert func.calls == [3]
    assert wrapper.coalesce_info() == CoalesceStats(active=0, coalesced=4, total=5)


async def test_ainvoke_coalesces_concurrent_calls() -> None:
    func = _AsyncGatedDouble()
    wrapper = RunnableLambda(func).with_coalesce()

    tasks = [asyncio.create_task(wrapper.ainvoke(3)) for _ in range(5)]
    await _await_until(lambda: wrapper.coalesce_info().coalesced == 4)
    func.release.set()

    assert await asyncio.gather(*tasks) == [6] * 5
    assert func.calls == [3]
    assert wrapper.coalesce_info() == CoalesceStats(active=0, coalesced=4, total=5)


def test_completed_execution_is_not_reused() -> None:
    calls: list[int] = []
    wrapper = _recording(calls).with_coalesce()

    assert wrapper.invoke(1) == 1
    assert wrapper.invoke(1) == 1

    assert calls == [1, 1]
    assert wrapper.coalesce_info() == CoalesceStats(active=0, coalesced=0, total=2)


def test_key_ignores_config_kwargs_and_dict_order() -> None:
    recorder = _Recorder()
    wrapper = recorder.with_coalesce()

    with ThreadPoolExecutor(3) as executor:
        owner = executor.submit(
            wrapper.invoke, {"a": 1, "b": [1, 2]}, {"tags": ["owner"]}, flag=True
        )
        _wait_until(lambda: len(recorder.calls) == 1)
        joiners = [
            executor.submit(
                wrapper.invoke, {"b": [1, 2], "a": 1}, {"metadata": {"x": 1}}
            ),
            executor.submit(wrapper.invoke, {"a": 1, "b": [1, 2]}, flag=False),
        ]
        _wait_until(lambda: wrapper.coalesce_info().coalesced == 2)
        recorder.release.set()

        assert owner.result(timeout=TIMEOUT) == 1
        assert [j.result(timeout=TIMEOUT) for j in joiners] == [1, 1]

    assert recorder.calls == [({"a": 1, "b": [1, 2]}, {"flag": True})]


def test_key_distinguishes_input_values() -> None:
    calls: list[Any] = []
    wrapper = _recording(calls).with_coalesce()

    wrapper.batch([1, True, 1.0, "1", (1,), [1]])
    assert len(calls) == 6

    calls.clear()
    wrapper.batch([{"a": {"b": 1, "c": 2}}, {"a": {"c": 2, "b": 1}}])
    assert len(calls) == 1

    calls.clear()
    wrapper.batch([HumanMessage("hi"), HumanMessage("hi"), AIMessage("hi")])
    assert calls == [HumanMessage("hi"), AIMessage("hi")]

    calls.clear()
    unhashable = [object(), bytearray(b"x"), bytearray(b"x")]
    wrapper.batch([unhashable[0], unhashable[0], unhashable[1], unhashable[2]])
    assert len(calls) == 3


def test_error_propagates_to_joiners_and_next_call_runs_fresh() -> None:
    release = threading.Event()
    calls: list[int] = []

    def fail(x: int) -> int:
        calls.append(x)
        assert release.wait(TIMEOUT)
        msg = "boom"
        raise ValueError(msg)

    wrapper = RunnableLambda(fail).with_coalesce()

    with ThreadPoolExecutor(3) as executor:
        futures = [executor.submit(wrapper.invoke, 1) for _ in range(3)]
        _wait_until(lambda: wrapper.coalesce_info().coalesced == 2)
        release.set()
        for future in futures:
            with pytest.raises(ValueError, match="boom"):
                future.result(timeout=TIMEOUT)

    assert calls == [1]
    with pytest.raises(ValueError, match="boom"):
        wrapper.invoke(1)
    assert calls == [1, 1]


def _gated_generator() -> tuple[RunnableGenerator, threading.Event, threading.Event]:
    first_chunk_sent = threading.Event()
    release = threading.Event()

    def generate(inputs: Iterator[int]) -> Iterator[str]:
        for _ in inputs:
            pass
        yield "a"
        first_chunk_sent.set()
        assert release.wait(TIMEOUT)
        yield "b"
        yield "c"

    return RunnableGenerator(generate), first_chunk_sent, release


def test_stream_joiner_replays_all_chunks() -> None:
    generator, first_chunk_sent, release = _gated_generator()
    wrapper = generator.with_coalesce()

    with ThreadPoolExecutor(2) as executor:
        owner = executor.submit(lambda: list(wrapper.stream(1)))
        assert first_chunk_sent.wait(TIMEOUT)
        joiner = executor.submit(lambda: list(wrapper.stream(1)))
        _wait_until(lambda: wrapper.coalesce_info().coalesced == 1)
        release.set()

        assert owner.result(timeout=TIMEOUT) == ["a", "b", "c"]
        assert joiner.result(timeout=TIMEOUT) == ["a", "b", "c"]

    assert wrapper.coalesce_info() == CoalesceStats(active=0, coalesced=1, total=2)


async def test_astream_joiner_replays_all_chunks() -> None:
    calls = 0
    first_chunk_sent = asyncio.Event()
    release = asyncio.Event()

    async def generate(inputs: AsyncIterator[int]) -> AsyncIterator[str]:
        nonlocal calls
        async for _ in inputs:
            pass
        calls += 1
        yield "a"
        first_chunk_sent.set()
        await asyncio.wait_for(release.wait(), TIMEOUT)
        yield "b"
        yield "c"

    wrapper = RunnableGenerator(generate).with_coalesce()

    owner = asyncio.create_task(_collect(wrapper.astream(1)))
    await asyncio.wait_for(first_chunk_sent.wait(), TIMEOUT)
    joiner = asyncio.create_task(_collect(wrapper.astream(1)))
    await _await_until(lambda: wrapper.coalesce_info().coalesced == 1)
    release.set()

    assert await owner == ["a", "b", "c"]
    assert await joiner == ["a", "b", "c"]
    assert calls == 1


def test_invoke_joins_in_flight_stream() -> None:
    generator, first_chunk_sent, release = _gated_generator()
    wrapper = generator.with_coalesce()

    with ThreadPoolExecutor(2) as executor:
        owner = executor.submit(lambda: list(wrapper.stream(1)))
        assert first_chunk_sent.wait(TIMEOUT)
        joiner = executor.submit(wrapper.invoke, 1)
        _wait_until(lambda: wrapper.coalesce_info().coalesced == 1)
        release.set()

        assert owner.result(timeout=TIMEOUT) == ["a", "b", "c"]
        assert joiner.result(timeout=TIMEOUT) == "abc"


def test_stream_joins_in_flight_invoke() -> None:
    func = _GatedDouble()
    wrapper = RunnableLambda(func).with_coalesce()

    with ThreadPoolExecutor(2) as executor:
        owner = executor.submit(wrapper.invoke, 4)
        _wait_until(lambda: func.calls == [4])
        joiner = executor.submit(lambda: list(wrapper.stream(4)))
        _wait_until(lambda: wrapper.coalesce_info().coalesced == 1)
        func.release.set()

        assert owner.result(timeout=TIMEOUT) == 8
        assert joiner.result(timeout=TIMEOUT) == [8]

    assert func.calls == [4]


async def test_async_methods_share_one_backend() -> None:
    func = _AsyncGatedDouble()
    wrapper = RunnableLambda(func).with_coalesce()

    owner = asyncio.create_task(wrapper.ainvoke(5))
    await _await_until(lambda: func.calls == [5])
    stream = asyncio.create_task(_collect(wrapper.astream(5)))
    batch = asyncio.create_task(wrapper.abatch([5, 5]))
    as_completed = asyncio.create_task(_collect(wrapper.abatch_as_completed([5])))
    await _await_until(lambda: wrapper.coalesce_info().coalesced == 4)
    func.release.set()

    assert await owner == 10
    assert await stream == [10]
    assert await batch == [10, 10]
    assert await as_completed == [(0, 10)]
    assert func.calls == [5]


async def test_sync_and_async_callers_share_one_backend() -> None:
    func = _GatedDouble()
    wrapper = RunnableLambda(func).with_coalesce()

    owner = asyncio.create_task(asyncio.to_thread(wrapper.invoke, 2))
    await _await_until(lambda: func.calls == [2])
    joiner = asyncio.create_task(wrapper.ainvoke(2))
    await _await_until(lambda: wrapper.coalesce_info().coalesced == 1)
    func.release.set()

    assert await asyncio.wait_for(joiner, TIMEOUT) == 4
    assert await asyncio.wait_for(owner, TIMEOUT) == 4
    assert func.calls == [2]


def test_batch_coalesces_per_item_and_preserves_order() -> None:
    calls: list[int] = []
    lock = threading.Lock()

    def double(x: int) -> int:
        with lock:
            calls.append(x)
        return x * 2

    wrapper = RunnableLambda(double).with_coalesce()

    assert wrapper.batch([1, 2, 1, 3, 2]) == [2, 4, 2, 6, 4]
    assert sorted(calls) == [1, 2, 3]
    assert wrapper.coalesce_info() == CoalesceStats(active=0, coalesced=2, total=5)


async def test_abatch_coalesces_per_item_and_preserves_order() -> None:
    calls: list[int] = []

    async def double(x: int) -> int:
        calls.append(x)
        await asyncio.sleep(0)
        return x * 2

    wrapper = RunnableLambda(double).with_coalesce()

    assert await wrapper.abatch([1, 2, 1, 3, 2]) == [2, 4, 2, 6, 4]
    assert sorted(calls) == [1, 2, 3]
    assert wrapper.coalesce_info() == CoalesceStats(active=0, coalesced=2, total=5)


def test_batch_joins_execution_in_flight_elsewhere() -> None:
    func = _GatedDouble()
    wrapper = RunnableLambda(func).with_coalesce()

    with ThreadPoolExecutor(2) as executor:
        owner = executor.submit(wrapper.invoke, 1)
        _wait_until(lambda: func.calls == [1])
        batch = executor.submit(wrapper.batch, [1, 2, 1])
        _wait_until(lambda: sorted(func.calls) == [1, 2])
        func.release.set()

        assert owner.result(timeout=TIMEOUT) == 2
        assert batch.result(timeout=TIMEOUT) == [2, 4, 2]

    assert sorted(func.calls) == [1, 2]


def _fail_on_bad(x: str) -> str:
    if x == "bad":
        msg = "bad input"
        raise ValueError(msg)
    return x.upper()


def test_batch_errors() -> None:
    wrapper = RunnableLambda(_fail_on_bad).with_coalesce()

    results = wrapper.batch(["ok", "bad", "bad"], return_exceptions=True)
    assert results[0] == "OK"
    assert isinstance(results[1], ValueError)
    assert results[2] is results[1]

    with pytest.raises(ValueError, match="bad input"):
        wrapper.batch(["ok", "bad"])
    assert wrapper.coalesce_info().active == 0


async def test_abatch_errors() -> None:
    wrapper = RunnableLambda(_fail_on_bad).with_coalesce()

    results = await wrapper.abatch(["ok", "bad", "bad"], return_exceptions=True)
    assert results[0] == "OK"
    assert isinstance(results[1], ValueError)
    assert results[2] is results[1]

    with pytest.raises(ValueError, match="bad input"):
        await wrapper.abatch(["ok", "bad"])
    assert wrapper.coalesce_info().active == 0


def _assert_duplicates_consecutive(
    results: list[tuple[int, Any]], duplicates: list[int]
) -> None:
    positions = [i for i, (index, _) in enumerate(results) if index in duplicates]
    assert positions == list(range(positions[0], positions[0] + len(duplicates)))
    assert results[positions[0]][0] == duplicates[0]


def test_batch_as_completed_yields_duplicates_consecutively() -> None:
    calls: list[int] = []
    lock = threading.Lock()

    def double(x: int) -> int:
        with lock:
            calls.append(x)
        return x * 2

    wrapper = RunnableLambda(double).with_coalesce()

    results = list(wrapper.batch_as_completed([1, 2, 1, 3, 1]))

    assert sorted(results) == [(0, 2), (1, 4), (2, 2), (3, 6), (4, 2)]
    _assert_duplicates_consecutive(results, [0, 2, 4])
    assert sorted(calls) == [1, 2, 3]
    assert wrapper.coalesce_info() == CoalesceStats(active=0, coalesced=2, total=5)


async def test_abatch_as_completed_yields_duplicates_consecutively() -> None:
    calls: list[int] = []

    async def double(x: int) -> int:
        calls.append(x)
        await asyncio.sleep(0.01 * x)
        return x * 2

    wrapper = RunnableLambda(double).with_coalesce()

    results = await _collect(wrapper.abatch_as_completed([3, 1, 3, 2, 3]))

    assert sorted(results) == [(0, 6), (1, 2), (2, 6), (3, 4), (4, 6)]
    _assert_duplicates_consecutive(results, [0, 2, 4])
    assert sorted(calls) == [1, 2, 3]


def test_batch_as_completed_errors() -> None:
    wrapper = RunnableLambda(_fail_on_bad).with_coalesce()

    results = dict(
        wrapper.batch_as_completed(["ok", "bad", "bad"], return_exceptions=True)
    )
    assert results[0] == "OK"
    assert isinstance(results[1], ValueError)
    assert results[2] is results[1]

    with pytest.raises(ValueError, match="bad input"):
        list(wrapper.batch_as_completed(["bad", "ok"]))
    assert wrapper.coalesce_info().active == 0


async def test_abatch_as_completed_errors() -> None:
    wrapper = RunnableLambda(_fail_on_bad).with_coalesce()

    results = dict(
        await _collect(
            wrapper.abatch_as_completed(["ok", "bad", "bad"], return_exceptions=True)
        )
    )
    assert results[0] == "OK"
    assert isinstance(results[1], ValueError)
    assert results[2] is results[1]

    with pytest.raises(ValueError, match="bad input"):
        await _collect(wrapper.abatch_as_completed(["bad", "ok"]))
    assert wrapper.coalesce_info().active == 0


def test_joined_callers_fire_chain_callbacks() -> None:
    func = _GatedDouble()
    wrapper = RunnableLambda(func).with_coalesce()
    owner_handler = FakeCallbackHandler()
    invoke_handler = FakeCallbackHandler()
    stream_handler = FakeCallbackHandler()
    batch_handler = FakeCallbackHandler()

    with ThreadPoolExecutor(4) as executor:
        owner = executor.submit(wrapper.invoke, 1, {"callbacks": [owner_handler]})
        _wait_until(lambda: func.calls == [1])
        joiners: list[Future[Any]] = [
            executor.submit(wrapper.invoke, 1, {"callbacks": [invoke_handler]}),
            executor.submit(
                lambda: list(wrapper.stream(1, {"callbacks": [stream_handler]}))
            ),
            executor.submit(wrapper.batch, [1, 1], {"callbacks": [batch_handler]}),
        ]
        _wait_until(lambda: wrapper.coalesce_info().coalesced == 4)
        func.release.set()
        owner.result(timeout=TIMEOUT)
        for joiner in joiners:
            joiner.result(timeout=TIMEOUT)

    assert func.calls == [1]
    for handler, runs in [
        (owner_handler, 1),
        (invoke_handler, 1),
        (stream_handler, 1),
        (batch_handler, 2),
    ]:
        assert handler.chain_starts == runs
        assert handler.chain_ends == runs


async def test_async_joined_callers_fire_chain_callbacks() -> None:
    func = _AsyncGatedDouble()
    wrapper = RunnableLambda(func).with_coalesce()
    invoke_handler = FakeCallbackHandler()
    stream_handler = FakeCallbackHandler()

    owner = asyncio.create_task(wrapper.ainvoke(1))
    await _await_until(lambda: func.calls == [1])
    joiners = [
        asyncio.create_task(wrapper.ainvoke(1, {"callbacks": [invoke_handler]})),
        asyncio.create_task(
            _collect(wrapper.astream(1, {"callbacks": [stream_handler]}))
        ),
    ]
    await _await_until(lambda: wrapper.coalesce_info().coalesced == 2)
    func.release.set()
    await asyncio.gather(owner, *joiners)

    for handler in (invoke_handler, stream_handler):
        assert handler.chain_starts == 1
        assert handler.chain_ends == 1


def test_joined_callers_fire_chain_error_callbacks() -> None:
    release = threading.Event()

    def fail(_: int) -> int:
        assert release.wait(TIMEOUT)
        msg = "boom"
        raise ValueError(msg)

    wrapper = RunnableLambda(fail).with_coalesce()
    handler = FakeCallbackHandler()

    with ThreadPoolExecutor(2) as executor:
        owner = executor.submit(wrapper.invoke, 1)
        _wait_until(lambda: wrapper.coalesce_info().total == 1)
        joiner = executor.submit(wrapper.invoke, 1, {"callbacks": [handler]})
        _wait_until(lambda: wrapper.coalesce_info().coalesced == 1)
        release.set()
        for future in (owner, joiner):
            with pytest.raises(ValueError, match="boom"):
                future.result(timeout=TIMEOUT)

    assert handler.chain_starts == 1
    assert handler.errors == 1


def test_coalesce_clear_cancels_waiters() -> None:
    func = _GatedDouble()
    wrapper = RunnableLambda(func).with_coalesce()

    with ThreadPoolExecutor(2) as executor:
        owner = executor.submit(wrapper.invoke, 1)
        _wait_until(lambda: func.calls == [1])
        joiner = executor.submit(wrapper.invoke, 1)
        _wait_until(lambda: wrapper.coalesce_info().coalesced == 1)

        wrapper.coalesce_clear()

        with pytest.raises(asyncio.CancelledError):
            joiner.result(timeout=TIMEOUT)
        assert wrapper.coalesce_info() == CoalesceStats(active=0, coalesced=0, total=0)
        func.release.set()
        assert owner.result(timeout=TIMEOUT) == 2


async def test_coalesce_clear_cancels_async_waiters() -> None:
    func = _AsyncGatedDouble()
    wrapper = RunnableLambda(func).with_coalesce()

    owner = asyncio.create_task(wrapper.ainvoke(1))
    await _await_until(lambda: func.calls == [1])
    joiner = asyncio.create_task(wrapper.ainvoke(1))
    await _await_until(lambda: wrapper.coalesce_info().coalesced == 1)

    wrapper.coalesce_clear()

    with pytest.raises(asyncio.CancelledError):
        await joiner
    assert wrapper.coalesce_info() == CoalesceStats(active=0, coalesced=0, total=0)
    func.release.set()
    assert await owner == 2


async def test_cancelled_owner_hands_execution_to_joiner() -> None:
    calls = 0
    never = asyncio.Event()

    async def double(x: int) -> int:
        nonlocal calls
        calls += 1
        if calls == 1:
            await never.wait()
        return x * 2

    wrapper = RunnableLambda(double).with_coalesce()

    owner = asyncio.create_task(wrapper.ainvoke(1))
    await _await_until(lambda: calls == 1)
    joiner = asyncio.create_task(wrapper.ainvoke(1))
    await _await_until(lambda: wrapper.coalesce_info().coalesced == 1)
    owner.cancel()

    assert await asyncio.wait_for(joiner, TIMEOUT) == 2
    assert calls == 2
    with pytest.raises(asyncio.CancelledError):
        await owner


async def test_abandoned_stream_owner_hands_execution_to_joiner() -> None:
    calls = 0
    release = asyncio.Event()

    async def generate(inputs: AsyncIterator[int]) -> AsyncIterator[str]:
        nonlocal calls
        async for _ in inputs:
            pass
        calls += 1
        yield "a"
        await asyncio.wait_for(release.wait(), TIMEOUT)
        yield "b"

    wrapper = RunnableGenerator(generate).with_coalesce()

    async with aclosing(wrapper.astream(1)) as owner_stream:
        assert await anext(owner_stream) == "a"
        joiner = asyncio.create_task(_collect(wrapper.astream(1)))
        await _await_until(lambda: wrapper.coalesce_info().coalesced == 1)
    release.set()

    assert await asyncio.wait_for(joiner, TIMEOUT) == ["a", "b"]
    assert calls == 2


def test_graph_and_schema_delegate_to_bound() -> None:
    bound = RunnableLambda(_double) | RunnableLambda(str)
    wrapper = bound.with_coalesce()

    assert wrapper.get_graph().draw_ascii() == bound.get_graph().draw_ascii()
    assert wrapper.get_name() == bound.get_name()
    assert wrapper.get_input_jsonschema() == bound.get_input_jsonschema()
    assert wrapper.get_output_jsonschema() == bound.get_output_jsonschema()


def test_separate_wrappers_coalesce_independently() -> None:
    func = _GatedDouble()
    first = RunnableLambda(func).with_coalesce()
    second = RunnableLambda(func).with_coalesce()

    with ThreadPoolExecutor(2) as executor:
        futures = [executor.submit(first.invoke, 1), executor.submit(second.invoke, 1)]
        _wait_until(lambda: len(func.calls) == 2)
        func.release.set()
        assert [f.result(timeout=TIMEOUT) for f in futures] == [2, 2]

    assert first.coalesce_info().coalesced == 0
    assert second.coalesce_info().coalesced == 0


def test_wrappers_sharing_a_backend_coalesce_together() -> None:
    func = _GatedDouble()
    backend = InMemoryCoalesceBackend()
    first = RunnableLambda(func).with_coalesce(backend=backend)
    second = RunnableLambda(func).with_coalesce(backend=backend)

    with ThreadPoolExecutor(2) as executor:
        futures: list[Future[int]] = [executor.submit(first.invoke, 1)]
        _wait_until(lambda: func.calls == [1])
        futures.append(executor.submit(second.invoke, 1))
        _wait_until(lambda: backend.stats.coalesced == 1)
        func.release.set()
        assert [f.result(timeout=TIMEOUT) for f in futures] == [2, 2]

    assert func.calls == [1]
    assert first.coalesce_info() == second.coalesce_info()


def test_custom_backend_is_used() -> None:
    backend = _SyncOnlyBackend()
    wrapper = RunnableLambda(_double).with_coalesce(backend=backend)

    assert wrapper.batch([1, 1, 2]) == [2, 2, 4]
    assert len(backend.registered) == 3
    assert wrapper.coalesce_info() == CoalesceStats(active=0, coalesced=1, total=3)


async def test_transform_and_event_streaming_pass_through() -> None:
    wrapper = RunnableLambda(_double).with_coalesce()

    assert list(wrapper.transform(iter([1]))) == [2]

    async def one() -> AsyncIterator[int]:
        yield 1

    assert await _collect(wrapper.atransform(one())) == [2]
    events = await _collect(wrapper.astream_events(1, version="v2"))
    assert events[-1]["event"] == "on_chain_end"
    assert events[-1]["data"]["output"] == 2
    assert wrapper.coalesce_info().total == 0
