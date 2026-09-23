"""Tests for request coalescing on `Runnable`."""

from __future__ import annotations

import asyncio
import threading
import time
from typing import TYPE_CHECKING, Any

import pytest

from langchain_core.messages import HumanMessage
from langchain_core.runnables import (
    CoalesceBackend,
    CoalesceStats,
    InMemoryCoalesceBackend,
    RunnableGenerator,
    RunnableLambda,
)

if TYPE_CHECKING:
    from collections.abc import AsyncIterator, Iterator

    from langchain_core.runnables.coalesce import RunnableCoalesce
from tests.unit_tests.fake.callbacks import (
    FakeAsyncCallbackHandler,
    FakeCallbackHandler,
)


def _wait_until(predicate: Any, *, timeout: float = 2.0) -> None:
    deadline = time.monotonic() + timeout
    while not predicate():
        if time.monotonic() > deadline:
            msg = "timed out waiting for coalescing state"
            raise TimeoutError(msg)
        time.sleep(0.005)


def test_backend_register_join_and_fresh_execution() -> None:
    backend = InMemoryCoalesceBackend()
    key = "alpha"

    assert backend.register(key) is True
    assert backend.is_active(key) is True
    assert backend.stats == CoalesceStats(active=1, coalesced=0, total=1)

    assert backend.register(key) is False
    assert backend.stats == CoalesceStats(active=1, coalesced=1, total=2)

    backend.complete(key, result="done")
    assert backend.is_active(key) is False
    assert backend.join(key) == "done"
    assert backend.stats == CoalesceStats(active=0, coalesced=1, total=2)

    assert backend.register(key) is True
    assert backend.register(key) is False
    assert backend.stats == CoalesceStats(active=1, coalesced=2, total=4)
    backend.complete(key, error=ValueError("boom"))
    with pytest.raises(ValueError, match="boom"):
        backend.join(key)


def test_backend_clear_cancels_joiner_and_resets_stats() -> None:
    backend = InMemoryCoalesceBackend()
    assert backend.register("k") is True
    assert backend.register("k") is False
    backend.clear()

    assert backend.stats == CoalesceStats(0, 0, 0)
    assert backend.is_active("k") is False
    with pytest.raises(asyncio.CancelledError):
        backend.join("k")

    assert backend.register("k") is True
    assert backend.stats == CoalesceStats(active=1, coalesced=0, total=1)
    backend.complete("k", result=1)
    assert backend.stats == CoalesceStats(active=0, coalesced=0, total=1)


def test_backend_is_thread_safe() -> None:
    backend = InMemoryCoalesceBackend()
    key = ("shared",)
    workers = 8

    def run() -> None:
        if backend.register(key):
            _wait_until(lambda: backend.stats.total >= workers)
            backend.complete(key, result=7)
        else:
            assert backend.join(key) == 7

    threads = [threading.Thread(target=run) for _ in range(workers)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()

    assert backend.stats == CoalesceStats(
        active=0, coalesced=workers - 1, total=workers
    )
    assert backend.is_active(key) is False


async def test_backend_async_methods_share_sync_state() -> None:
    backend = InMemoryCoalesceBackend()
    assert backend.register("k") is True
    assert await backend.ais_active("k") is True

    async def join() -> Any:
        assert await backend.aregister("k") is False
        return await backend.ajoin("k")

    joiner = asyncio.create_task(join())
    for _ in range(100):
        if backend.stats.coalesced == 1:
            break
        await asyncio.sleep(0)
    else:
        msg = "timed out waiting for the async joiner"
        raise TimeoutError(msg)
    await backend.acomplete("k", result={"ok": True})
    assert await joiner == {"ok": True}
    assert await backend.ais_active("k") is False


def test_abstract_backend_requires_implementation() -> None:
    with pytest.raises(TypeError):
        CoalesceBackend()  # type: ignore[abstract]


def test_coalesce_info_not_exported_wrapper_name() -> None:
    from langchain_core.runnables import __all__ as exported  # noqa: PLC0415

    assert "RunnableCoalesce" not in exported
    assert "CoalesceBackend" in exported
    assert "CoalesceStats" in exported
    assert "InMemoryCoalesceBackend" in exported


def _blocked_runnable() -> tuple[
    RunnableCoalesce[int, int], threading.Event, list[int], threading.Event
]:
    started = threading.Event()
    release = threading.Event()
    calls: list[int] = []

    def slow(value: int) -> int:
        calls.append(value)
        started.set()
        assert release.wait(timeout=2)
        return value + 1

    return RunnableLambda(slow).with_coalesce(), release, calls, started


def test_concurrent_invoke_runs_once() -> None:
    coalesced, release, calls, started = _blocked_runnable()
    results: list[int] = []

    def run(config_tag: str) -> None:
        results.append(coalesced.invoke(1, config={"tags": [config_tag]}))

    leader = threading.Thread(target=run, args=("a",))
    joiner = threading.Thread(target=run, args=("b",))
    leader.start()
    assert started.wait(timeout=2)
    joiner.start()
    _wait_until(lambda: coalesced.coalesce_info().coalesced == 1)
    assert coalesced.coalesce_info().active == 1
    release.set()
    leader.join()
    joiner.join()

    assert results == [2, 2]
    assert calls == [1]
    assert coalesced.coalesce_info() == CoalesceStats(active=0, coalesced=1, total=2)

    assert coalesced.invoke(1) == 2
    assert calls == [1, 1]
    assert coalesced.coalesce_info().total == 3
    assert coalesced.coalesce_info().coalesced == 1


def test_dict_key_order_and_kwargs_do_not_split_calls() -> None:
    started = threading.Event()
    release = threading.Event()
    calls: list[Any] = []

    def slow(value: dict[str, Any], **kwargs: Any) -> dict[str, Any]:
        calls.append((value, kwargs))
        started.set()
        assert release.wait(timeout=2)
        return {"n": value["n"], "flag": kwargs.get("flag")}

    coalesced = RunnableLambda(slow).with_coalesce()
    results: list[Any] = []

    def first() -> None:
        results.append(
            coalesced.invoke({"b": 1, "a": {"z": 2, "y": 3}, "n": 5}, flag=1)
        )

    def second() -> None:
        results.append(
            coalesced.invoke({"n": 5, "a": {"y": 3, "z": 2}, "b": 1}, flag=2)
        )

    leader = threading.Thread(target=first)
    joiner = threading.Thread(target=second)
    leader.start()
    assert started.wait(timeout=2)
    joiner.start()
    _wait_until(lambda: coalesced.coalesce_info().coalesced == 1)
    release.set()
    leader.join()
    joiner.join()

    assert calls[0][0]["n"] == 5
    assert len(calls) == 1
    assert results[0] == results[1]
    assert results[0]["n"] == 5


def test_distinct_inputs_do_not_coalesce() -> None:
    calls: list[Any] = []

    def record(value: Any) -> Any:
        calls.append(value)
        return value

    coalesced = RunnableLambda(record).with_coalesce()
    assert coalesced.invoke([1, 2]) == [1, 2]
    assert coalesced.invoke([2, 1]) == [2, 1]
    assert len(calls) == 2

    message_calls: list[Any] = []

    def record_message(value: HumanMessage) -> str:
        message_calls.append(value.content)
        return str(value.content)

    messages = RunnableLambda(record_message).with_coalesce()
    assert messages.invoke(HumanMessage(content="hi")) == "hi"
    assert messages.invoke(HumanMessage(content="hi")) == "hi"
    assert message_calls == ["hi", "hi"]


def test_separate_wrappers_are_independent_until_backend_is_shared() -> None:
    calls: list[str] = []
    started = threading.Event()
    release = threading.Event()

    def slow(value: int) -> int:
        calls.append("run")
        started.set()
        assert release.wait(timeout=2)
        return value

    raw = RunnableLambda(slow)
    left = raw.with_coalesce()
    right = raw.with_coalesce()

    def run(runnable: RunnableCoalesce[int, int]) -> None:
        assert runnable.invoke(1) == 1

    threads = [
        threading.Thread(target=run, args=(left,)),
        threading.Thread(target=run, args=(right,)),
    ]
    for thread in threads:
        thread.start()
    _wait_until(lambda: len(calls) == 2)
    release.set()
    for thread in threads:
        thread.join()
    assert left.coalesce_info().coalesced == 0
    assert right.coalesce_info().coalesced == 0

    backend = InMemoryCoalesceBackend()
    shared_calls: list[int] = []
    shared_started = threading.Event()
    shared_release = threading.Event()

    def shared_slow(value: int) -> int:
        shared_calls.append(value)
        shared_started.set()
        assert shared_release.wait(timeout=2)
        return value * 3

    shared_raw = RunnableLambda(shared_slow)
    one = shared_raw.with_coalesce(backend=backend)
    two = shared_raw.with_coalesce(backend=backend)
    results: list[int] = []

    def run_shared(runnable: RunnableCoalesce[int, int]) -> None:
        results.append(runnable.invoke(2))

    leader = threading.Thread(target=run_shared, args=(one,))
    joiner = threading.Thread(target=run_shared, args=(two,))
    leader.start()
    assert shared_started.wait(timeout=2)
    joiner.start()
    _wait_until(lambda: backend.stats.coalesced == 1)
    shared_release.set()
    leader.join()
    joiner.join()

    assert shared_calls == [2]
    assert sorted(results) == [6, 6]
    assert one.coalesce_info() == two.coalesce_info()
    assert one.coalesce_info().total == 2


def test_joined_invoke_fires_chain_callbacks() -> None:
    handler = FakeCallbackHandler()
    started = threading.Event()
    release = threading.Event()

    def slow(value: int) -> int:
        started.set()
        assert release.wait(timeout=2)
        return value + 5

    coalesced = RunnableLambda(slow).with_coalesce()

    def run() -> int:
        return coalesced.invoke(3, config={"callbacks": [handler]})

    leader = threading.Thread(target=run)
    leader.start()
    assert started.wait(timeout=2)
    joiner = threading.Thread(target=run)
    joiner.start()
    _wait_until(lambda: coalesced.coalesce_info().coalesced == 1)
    release.set()
    leader.join()
    joiner.join()

    assert handler.chain_starts == 2
    assert handler.chain_ends == 2
    assert handler.errors == 0


def test_joined_error_is_shared() -> None:
    handler = FakeCallbackHandler()
    started = threading.Event()
    release = threading.Event()

    def explode(value: int) -> int:
        started.set()
        assert release.wait(timeout=2)
        msg = f"bad {value}"
        raise ValueError(msg)

    coalesced = RunnableLambda(explode).with_coalesce()
    errors: list[BaseException] = []

    def run() -> None:
        try:
            coalesced.invoke(4, config={"callbacks": [handler]})
        except BaseException as exc:
            errors.append(exc)

    leader = threading.Thread(target=run)
    joiner = threading.Thread(target=run)
    leader.start()
    assert started.wait(timeout=2)
    joiner.start()
    _wait_until(lambda: coalesced.coalesce_info().total == 2)
    release.set()
    leader.join()
    joiner.join()

    assert len(errors) == 2
    assert all(isinstance(err, ValueError) for err in errors)
    assert handler.chain_starts == 2
    assert handler.errors == 2
    assert handler.chain_ends == 0


def test_stream_joiners_replay_all_chunks() -> None:
    started = threading.Event()
    release = threading.Event()
    calls: list[int] = []

    def generate(values: Iterator[int]) -> Iterator[int]:
        value = next(iter(values))
        calls.append(value)
        started.set()
        assert release.wait(timeout=2)
        yield value
        yield value + 10
        yield value + 20

    coalesced = RunnableGenerator(generate).with_coalesce()
    collected: list[list[int]] = []

    def consume() -> None:
        collected.append(list(coalesced.stream(3)))

    leader = threading.Thread(target=consume)
    leader.start()
    assert started.wait(timeout=2)
    joiner = threading.Thread(target=consume)
    joiner.start()
    _wait_until(lambda: coalesced.coalesce_info().coalesced == 1)
    release.set()
    leader.join()
    joiner.join()

    assert calls == [3]
    assert collected == [[3, 13, 23], [3, 13, 23]]

    assert list(coalesced.stream(3)) == [3, 13, 23]
    assert calls == [3, 3]


def test_batch_coalesces_per_item_and_preserves_order() -> None:
    calls: list[int] = []

    def double(value: int) -> int:
        calls.append(value)
        return value * 2

    coalesced = RunnableLambda(double).with_coalesce()
    assert coalesced.batch([1, 2, 1, 3, 2, 1]) == [2, 4, 2, 6, 4, 2]
    assert sorted(calls) == [1, 2, 3]
    info = coalesced.coalesce_info()
    assert info.active == 0
    assert info.total == 6
    assert info.coalesced == 3

    assert coalesced.batch([]) == []
    assert coalesced.coalesce_info().total == 6


def test_batch_return_exceptions_repeats_shared_error() -> None:
    def flaky(value: int) -> int:
        if value == 1:
            msg = "nope"
            raise ValueError(msg)
        return value

    coalesced = RunnableLambda(flaky).with_coalesce()
    output = coalesced.batch([1, 2, 1], return_exceptions=True)
    assert isinstance(output[0], ValueError)
    assert output[0] is output[2]
    assert output[1] == 2

    with pytest.raises(ValueError, match="nope"):
        coalesced.batch([1, 1])


def test_batch_as_completed_yields_duplicates_consecutively() -> None:
    def timed(value: str) -> str:
        time.sleep(0.05 if value == "slow" else 0.01)
        return value

    coalesced = RunnableLambda(timed).with_coalesce()
    inputs = ["slow", "fast", "slow", "fast", "other"]
    emitted = list(coalesced.batch_as_completed(inputs))
    positions: dict[str, list[int]] = {}
    for slot, (index, value) in enumerate(emitted):
        assert value == inputs[index]
        positions.setdefault(value, []).append(slot)

    for slots in positions.values():
        assert max(slots) - min(slots) == len(slots) - 1
    assert {index for index, _ in emitted} == set(range(len(inputs)))


async def test_async_invoke_stream_and_batch() -> None:
    calls: list[int] = []
    started = asyncio.Event()
    release = asyncio.Event()

    async def slow(value: int) -> int:
        calls.append(value)
        started.set()
        await release.wait()
        return value + 1

    coalesced = RunnableLambda(slow).with_coalesce()
    first = asyncio.create_task(coalesced.ainvoke(8))
    await started.wait()
    second = asyncio.create_task(coalesced.ainvoke(8, config={"tags": ["join"]}))
    for _ in range(200):
        if coalesced.coalesce_info().coalesced >= 1:
            break
        await asyncio.sleep(0)
    else:
        msg = "timed out waiting for the async joiner"
        raise TimeoutError(msg)
    release.set()
    assert await first == 9
    assert await second == 9
    assert calls == [8]

    async def chunks(values: AsyncIterator[int]) -> AsyncIterator[int]:
        value = await anext(values)
        yield value
        yield value + 1

    streamed = RunnableGenerator(chunks).with_coalesce()
    assert [item async for item in streamed.astream(4)] == [4, 5]

    batched = RunnableLambda(lambda value: value * 2).with_coalesce()
    assert await batched.abatch([1, 1, 3]) == [2, 2, 6]
    completed = [item async for item in batched.abatch_as_completed([5, 6, 5])]
    by_value: dict[int, list[int]] = {}
    for slot, (index, value) in enumerate(completed):
        by_value.setdefault(value, []).append(slot)
        assert value == [5, 6, 5][index] * 2
    for slots in by_value.values():
        assert max(slots) - min(slots) == len(slots) - 1


async def test_sync_and_async_share_backend() -> None:
    calls: list[str] = []
    started = threading.Event()
    release = threading.Event()

    def sync_slow(value: int) -> int:
        calls.append("sync")
        started.set()
        assert release.wait(timeout=2)
        return value + 1

    async def async_slow(value: int) -> int:
        calls.append("async")
        started.set()
        await asyncio.to_thread(release.wait)
        return value + 1

    coalesced = RunnableLambda(sync_slow, afunc=async_slow).with_coalesce()
    leader = asyncio.create_task(asyncio.to_thread(coalesced.invoke, 1))
    assert await asyncio.to_thread(started.wait)
    joiner = asyncio.create_task(coalesced.ainvoke(1))
    for _ in range(200):
        if coalesced.coalesce_info().coalesced >= 1:
            break
        await asyncio.sleep(0)
    else:
        msg = "timed out waiting for the async joiner"
        raise TimeoutError(msg)
    release.set()
    assert await leader == 2
    assert await joiner == 2
    assert calls == ["sync"]


async def test_coalesce_clear_cancels_async_waiter() -> None:
    started = asyncio.Event()
    release = asyncio.Event()

    async def slow(value: int) -> int:
        started.set()
        await release.wait()
        return value

    coalesced = RunnableLambda(slow).with_coalesce()
    leader = asyncio.create_task(coalesced.ainvoke(1))
    await started.wait()
    joiner = asyncio.create_task(coalesced.ainvoke(1))
    for _ in range(200):
        if coalesced.coalesce_info().coalesced >= 1:
            break
        await asyncio.sleep(0)
    else:
        msg = "timed out waiting for the async joiner"
        raise TimeoutError(msg)
    await asyncio.sleep(0)
    coalesced.coalesce_clear()
    with pytest.raises(asyncio.CancelledError):
        await joiner
    assert coalesced.coalesce_info() == CoalesceStats(0, 0, 0)
    release.set()
    assert await leader == 1
    assert coalesced.coalesce_info() == CoalesceStats(0, 0, 0)
    assert await coalesced.ainvoke(1) == 1


def test_transform_and_events_pass_through() -> None:
    calls: list[int] = []

    def generate(values: Iterator[int]) -> Iterator[int]:
        calls.append(1)
        for value in values:
            yield value + 1

    inner = RunnableGenerator(generate)
    coalesced = inner.with_coalesce()
    assert list(coalesced.transform(iter([1, 2, 3]))) == [2, 3, 4]
    assert list(coalesced.transform(iter([1, 2, 3]))) == [2, 3, 4]
    assert calls == [1, 1]

    sequence = RunnableLambda(lambda value: value + 1) | RunnableLambda(
        lambda value: value * 2
    )
    wrapped = sequence.with_coalesce()
    assert sequence.get_graph().to_json() == wrapped.get_graph().to_json()


async def test_event_stream_delegates_to_bound_runnable() -> None:
    async def add_one(value: int) -> int:
        return value + 1

    inner = RunnableLambda(add_one)
    wrapped = inner.with_coalesce()
    inner_events = [event async for event in inner.astream_events(1, version="v2")]
    wrapped_events = [event async for event in wrapped.astream_events(1, version="v2")]
    assert [event["event"] for event in inner_events] == [
        event["event"] for event in wrapped_events
    ]
    assert [event["event"] for event in wrapped_events].count("on_chain_start") == 1


async def test_async_joiner_callbacks() -> None:
    handler = FakeAsyncCallbackHandler()
    started = asyncio.Event()
    release = asyncio.Event()

    async def slow(value: int) -> int:
        started.set()
        await release.wait()
        return value

    coalesced = RunnableLambda(slow).with_coalesce()
    leader = asyncio.create_task(coalesced.ainvoke(2, config={"callbacks": [handler]}))
    await started.wait()
    joiner = asyncio.create_task(coalesced.ainvoke(2, config={"callbacks": [handler]}))
    for _ in range(200):
        if coalesced.coalesce_info().coalesced >= 1:
            break
        await asyncio.sleep(0)
    else:
        msg = "timed out waiting for the async joiner"
        raise TimeoutError(msg)
    release.set()
    assert await leader == 2
    assert await joiner == 2
    assert handler.chain_starts == 2
    assert handler.chain_ends == 2


def test_invoke_and_stream_share_one_flight() -> None:
    started = threading.Event()
    release = threading.Event()
    calls: list[str] = []

    def generate(values: Iterator[int]) -> Iterator[int]:
        value = next(iter(values))
        calls.append("stream")
        started.set()
        assert release.wait(timeout=2)
        yield value
        yield value + 1

    coalesced = RunnableGenerator(generate).with_coalesce()
    streamed: list[int] = []
    invoked: list[int] = []

    def consume_stream() -> None:
        streamed.extend(coalesced.stream(4))

    def consume_invoke() -> None:
        invoked.append(coalesced.invoke(4))

    leader = threading.Thread(target=consume_stream)
    joiner = threading.Thread(target=consume_invoke)
    leader.start()
    assert started.wait(timeout=2)
    joiner.start()
    _wait_until(lambda: coalesced.coalesce_info().coalesced == 1)
    release.set()
    leader.join()
    joiner.join()

    assert calls == ["stream"]
    assert streamed == [4, 5]
    assert invoked == [9]


def test_nested_call_from_leader_does_not_deadlock() -> None:
    holder: dict[str, RunnableCoalesce[int, int]] = {}

    def nested(value: int) -> int:
        if value == 1:
            return holder["runnable"].invoke(2)
        return value + 1

    coalesced = RunnableLambda(nested).with_coalesce()
    holder["runnable"] = coalesced
    assert coalesced.invoke(1) == 3
    assert coalesced.coalesce_info().coalesced == 0
