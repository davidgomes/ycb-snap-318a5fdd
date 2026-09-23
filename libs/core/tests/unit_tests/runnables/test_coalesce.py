"""Unit tests for request coalescing on Runnable."""

from __future__ import annotations

import asyncio
import threading
import time
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from collections.abc import AsyncIterator, Iterator

import pytest
from pydantic import BaseModel

from langchain_core.callbacks import BaseCallbackHandler
from langchain_core.runnables import (
    CoalesceBackend,
    CoalesceStats,
    InMemoryCoalesceBackend,
    RunnableLambda,
)
from langchain_core.runnables.coalesce import RunnableCoalesce


class _Handler(BaseCallbackHandler):
    """Count chain start and end events."""

    def __init__(self) -> None:
        self.starts = 0
        self.ends = 0
        self.errors = 0

    def on_chain_start(self, *_args: object, **_kwargs: object) -> None:
        self.starts += 1

    def on_chain_end(self, *_args: object, **_kwargs: object) -> None:
        self.ends += 1

    def on_chain_error(self, *_args: object, **_kwargs: object) -> None:
        self.errors += 1


def _hold(release: threading.Event, started: threading.Event, calls: list[Any]) -> Any:
    def _fn(value: Any) -> Any:
        calls.append(value)
        started.set()
        assert release.wait(2)
        return ("out", value)

    return _fn


def test_invoke_coalesces_identical_inputs() -> None:
    release = threading.Event()
    started = threading.Event()
    calls: list[Any] = []
    runnable = RunnableLambda(_hold(release, started, calls)).with_coalesce()
    results: list[Any] = []

    def _call(config: dict[str, Any]) -> None:
        results.append(runnable.invoke({"b": 1, "a": 2}, config))

    first = threading.Thread(target=_call, args=({"tags": ["a"]},))
    second = threading.Thread(target=_call, args=({"tags": ["b"]},))
    first.start()
    assert started.wait(2)
    second.start()
    _wait_until(lambda: runnable.coalesce_info().coalesced == 1)
    info = runnable.coalesce_info()
    assert info == CoalesceStats(active=1, coalesced=1, total=2)
    assert runnable.coalesce_info().active == 1
    release.set()
    first.join(2)
    second.join(2)
    assert results == [("out", {"b": 1, "a": 2}), ("out", {"b": 1, "a": 2})]
    assert calls == [{"b": 1, "a": 2}]
    assert runnable.coalesce_info() == CoalesceStats(active=0, coalesced=1, total=2)
    assert runnable.invoke({"a": 2, "b": 1}) == ("out", {"a": 2, "b": 1})
    assert len(calls) == 2


def test_joined_invoke_fires_callbacks() -> None:
    release = threading.Event()
    started = threading.Event()
    calls: list[Any] = []
    runnable = RunnableLambda(_hold(release, started, calls)).with_coalesce()
    leader_handler = _Handler()
    joiner_handler = _Handler()

    def _lead() -> None:
        runnable.invoke("x", {"callbacks": [leader_handler]})

    def _join() -> None:
        runnable.invoke("x", {"callbacks": [joiner_handler], "metadata": {"n": 2}})

    leader = threading.Thread(target=_lead)
    leader.start()
    assert started.wait(2)
    joiner = threading.Thread(target=_join)
    joiner.start()
    _wait_until(lambda: runnable.coalesce_info().coalesced == 1)
    release.set()
    leader.join(2)
    joiner.join(2)
    assert leader_handler.starts >= 1
    assert leader_handler.ends >= 1
    assert joiner_handler.starts == 1
    assert joiner_handler.ends == 1
    assert joiner_handler.errors == 0


def test_separate_wrappers_do_not_share_unless_backend_is_shared() -> None:
    release = threading.Event()
    started = threading.Event()
    calls: list[Any] = []
    inner = RunnableLambda(_hold(release, started, calls))
    left = inner.with_coalesce()
    right = inner.with_coalesce()
    shared_calls: list[Any] = []
    shared_started = threading.Event()
    shared_release = threading.Event()
    backend = InMemoryCoalesceBackend()
    shared_inner = RunnableLambda(_hold(shared_release, shared_started, shared_calls))
    one = shared_inner.with_coalesce(backend=backend)
    two = shared_inner.with_coalesce(backend=backend)

    independent = threading.Thread(target=right.invoke, args=("v",))
    threading.Thread(target=left.invoke, args=("v",)).start()
    assert started.wait(2)
    independent.start()
    _wait_until(lambda: len(calls) == 2)
    release.set()
    independent.join(2)

    shared_holder: dict[str, Any] = {}

    def _shared() -> None:
        shared_holder["value"] = two.invoke("v")

    threading.Thread(target=one.invoke, args=("v",)).start()
    assert shared_started.wait(2)
    follower = threading.Thread(target=_shared)
    follower.start()
    _wait_until(lambda: one.coalesce_info().coalesced == 1)
    shared_release.set()
    follower.join(2)
    assert shared_holder["value"] == ("out", "v")
    assert shared_calls == ["v"]


def test_stream_replays_chunks_from_the_start() -> None:
    release = threading.Event()
    started = threading.Event()
    calls = 0

    def _gen(value: str) -> Iterator[str]:
        nonlocal calls
        calls += 1
        yield value + "-1"
        started.set()
        assert release.wait(2)
        yield value + "-2"

    runnable = RunnableLambda(_gen).with_coalesce()
    collected: list[list[str]] = []

    def _consume() -> None:
        collected.append(list(runnable.stream("q", {"tags": ["s"]})))

    leader = threading.Thread(target=_consume)
    leader.start()
    assert started.wait(2)
    joiner = threading.Thread(target=_consume)
    joiner.start()
    _wait_until(lambda: runnable.coalesce_info().coalesced == 1)
    release.set()
    leader.join(2)
    joiner.join(2)
    assert calls == 1
    assert collected == [["q-1", "q-2"], ["q-1", "q-2"]]


def test_batch_coalesces_per_item_and_preserves_order() -> None:
    calls: list[str] = []
    gate = threading.Semaphore(1)

    def _fn(value: str) -> str:
        with gate:
            calls.append(value)
            time.sleep(0.01)
            return value.upper()

    runnable = RunnableLambda(_fn).with_coalesce()
    inputs = ["b", "a", "b", "c", "a"]
    assert runnable.batch(inputs, {"max_concurrency": 2}) == ["B", "A", "B", "C", "A"]
    assert sorted(calls) == ["a", "b", "c"]
    pairs = list(runnable.batch_as_completed(["a", "b", "a", "b"]))
    values = dict(pairs)
    assert values == {0: "A", 1: "B", 2: "A", 3: "B"}
    assert sorted(value for _, value in pairs) == ["A", "A", "B", "B"]
    for key in ("A", "B"):
        positions = [pos for pos, (_, value) in enumerate(pairs) if value == key]
        assert positions[1] == positions[0] + 1


def test_invoke_error_is_shared() -> None:
    release = threading.Event()
    started = threading.Event()

    def _fn(_value: int) -> int:
        started.set()
        assert release.wait(2)
        msg = "nope"
        raise ValueError(msg)

    runnable = RunnableLambda(_fn).with_coalesce()
    errors: list[BaseException] = []

    def _call() -> None:
        try:
            runnable.invoke(1)
        except BaseException as exc:
            errors.append(exc)

    leader = threading.Thread(target=_call)
    leader.start()
    assert started.wait(2)
    joiner = threading.Thread(target=_call)
    joiner.start()
    _wait_until(lambda: runnable.coalesce_info().coalesced == 1)
    release.set()
    leader.join(2)
    joiner.join(2)
    assert len(errors) == 2
    assert all(isinstance(err, ValueError) for err in errors)


def test_transform_is_not_coalesced() -> None:
    calls: list[str] = []

    def _fn(value: str) -> str:
        calls.append(value)
        return value

    runnable = RunnableLambda(_fn).with_coalesce()

    def _chunks() -> Iterator[str]:
        yield "a"
        yield "b"

    assert list(runnable.transform(_chunks())) == ["ab"]
    assert list(runnable.transform(_chunks())) == ["ab"]
    assert calls == ["ab", "ab"]


def test_graph_delegates_to_bound_runnable() -> None:
    inner = RunnableLambda(lambda value: value)
    wrapped = inner.with_coalesce()
    assert isinstance(wrapped, RunnableCoalesce)
    assert [type(node.data) for node in wrapped.get_graph().nodes.values()] == [
        type(node.data) for node in inner.get_graph().nodes.values()
    ]
    assert wrapped.get_name() == inner.get_name()


def test_backend_contract_and_clear() -> None:
    backend = InMemoryCoalesceBackend()
    assert backend.register("k") is True
    assert backend.register("k") is False
    assert backend.is_active("k") is True
    assert backend.stats == CoalesceStats(active=1, coalesced=1, total=2)

    def _join() -> None:
        with pytest.raises(asyncio.CancelledError):
            backend.join("k")

    slot_event = backend._active["k"].event
    original_wait = slot_event.wait

    def _wait(timeout: float | None = None) -> bool:
        backend.clear()
        return original_wait(timeout)

    slot_event.wait = _wait  # type: ignore[method-assign]
    _join()
    assert backend.stats == CoalesceStats(active=0, coalesced=0, total=0)
    assert backend.is_active("k") is False
    assert backend.register("k") is True
    backend.complete("k", result="done")
    assert backend.join("k") == "done"


def test_in_memory_backend_is_thread_safe() -> None:
    backend = InMemoryCoalesceBackend()
    errors: list[BaseException] = []

    def _worker() -> None:
        try:
            for step in range(40):
                key = step % 4
                if backend.register(key):
                    time.sleep(0.001)
                    backend.complete(key, result=key)
                else:
                    assert backend.join(key) == key
        except BaseException as exc:
            errors.append(exc)

    threads = [threading.Thread(target=_worker) for _ in range(8)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join(5)
    assert errors == []
    assert backend.stats.active == 0


class _Item(BaseModel):
    name: str
    n: int


@pytest.mark.asyncio
async def test_async_invoke_stream_batch_and_clear() -> None:
    release = asyncio.Event()
    started = asyncio.Event()
    calls: list[Any] = []

    async def _fn(value: Any) -> Any:
        calls.append(value)
        started.set()
        await release.wait()
        return ("async", value)

    runnable = RunnableLambda(_fn).with_coalesce()
    leader = asyncio.create_task(runnable.ainvoke(_Item(name="a", n=1)))
    await started.wait()
    joiner = asyncio.create_task(
        runnable.ainvoke(_Item(n=1, name="a"), {"tags": ["other"]})
    )
    await _await_until(lambda: runnable.coalesce_info().coalesced == 1)
    assert runnable.coalesce_info().coalesced == 1
    runnable.coalesce_clear()
    with pytest.raises(asyncio.CancelledError):
        await joiner
    release.set()
    assert await leader == ("async", _Item(name="a", n=1))
    assert len(calls) == 1
    assert runnable.coalesce_info() == CoalesceStats(active=0, coalesced=0, total=0)

    stream_release = asyncio.Event()
    stream_started = asyncio.Event()

    async def _gen(value: str) -> AsyncIterator[str]:
        calls.append(value)
        yield value
        stream_started.set()
        await stream_release.wait()
        yield value + value

    streaming = RunnableLambda(_gen).with_coalesce()
    first = asyncio.create_task(_collect(streaming.astream("z")))
    await stream_started.wait()
    second_task = asyncio.create_task(
        _collect(streaming.astream("z", {"metadata": {"k": 1}}))
    )
    await _await_until(lambda: streaming.coalesce_info().coalesced == 1)
    stream_release.set()
    assert await first == ["z", "zz"]
    assert await second_task == ["z", "zz"]
    assert calls.count("z") == 1

    batched = RunnableLambda(_fn).with_coalesce()
    # Previous `_fn` waits on the already-set release event.
    output = await batched.abatch(["a", "b", "a"], {"max_concurrency": 1})
    assert output == [("async", "a"), ("async", "b"), ("async", "a")]
    pairs = [
        item
        async for item in batched.abatch_as_completed(
            ["a", "b", "a"],
            return_exceptions=False,
        )
    ]
    grouped: dict[str, list[int]] = {"a": [], "b": []}
    positions: dict[str, list[int]] = {"a": [], "b": []}
    for position, (index, value) in enumerate(pairs):
        assert isinstance(value, tuple)
        grouped[value[1]].append(index)
        positions[value[1]].append(position)
    assert grouped == {"a": [0, 2], "b": [1]}
    assert positions["a"][1] == positions["a"][0] + 1


async def _collect(stream: AsyncIterator[str]) -> list[str]:
    return [chunk async for chunk in stream]


@pytest.mark.asyncio
async def test_sync_and_async_share_backend() -> None:
    release = threading.Event()
    started = threading.Event()
    calls: list[int] = []

    def _fn(value: int) -> int:
        calls.append(value)
        started.set()
        assert release.wait(2)
        return value + 5

    runnable = RunnableLambda(_fn).with_coalesce()
    holder: dict[str, int] = {}

    def _lead() -> None:
        holder["value"] = runnable.invoke(3)

    leader = threading.Thread(target=_lead)
    leader.start()
    assert started.wait(2)
    pending = asyncio.create_task(runnable.ainvoke(3, {"tags": ["async"]}))
    await _await_until(lambda: runnable.coalesce_info().coalesced == 1)
    assert len(calls) == 1
    release.set()
    assert await pending == 8
    leader.join(2)
    assert holder["value"] == 8


@pytest.mark.asyncio
async def test_atransform_and_astream_events_pass_through() -> None:
    calls: list[str] = []

    async def _fn(value: str) -> str:
        calls.append(value)
        return value

    runnable = RunnableLambda(_fn).with_coalesce()

    async def _chunks() -> AsyncIterator[str]:
        yield "a"
        yield "b"

    assert [item async for item in runnable.atransform(_chunks())]
    assert [item async for item in runnable.atransform(_chunks())]
    assert len(calls) == 2
    events = [
        event
        async for event in runnable.astream_events("c", version="v2")
        if event["event"] == "on_chain_end"
    ]
    assert events
    assert calls[-1] == "c"


def _wait_until(predicate: Any) -> None:
    deadline = time.monotonic() + 2
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(0.01)
    msg = "Timed out waiting for coalesce state."
    raise AssertionError(msg)


async def _await_until(predicate: Any) -> None:
    deadline = time.monotonic() + 2
    while time.monotonic() < deadline:
        if predicate():
            return
        await asyncio.sleep(0.01)
    msg = "Timed out waiting for coalesce state."
    raise AssertionError(msg)


def test_custom_backend_async_defaults() -> None:
    class _Backend(CoalesceBackend):
        def __init__(self) -> None:
            self._lock = threading.Lock()
            self._active: dict[
                Any, tuple[threading.Event, Any, BaseException | None]
            ] = {}
            self._finished: dict[
                Any, tuple[threading.Event, Any, BaseException | None]
            ] = {}
            self._coalesced = 0
            self._total = 0

        def register(self, key: Any) -> bool:
            with self._lock:
                self._total += 1
                if key in self._active:
                    self._coalesced += 1
                    return False
                self._active[key] = (threading.Event(), None, None)
                return True

        def join(self, key: Any) -> Any:
            slot = self._active.get(key) or self._finished[key]
            event = slot[0]
            event.wait()
            _, result, error = self._finished.get(key, slot)
            if error is not None:
                raise error
            return result

        def complete(
            self,
            key: Any,
            *,
            result: Any = None,
            error: BaseException | None = None,
        ) -> None:
            event = self._active.pop(key)[0]
            finished = (event, result, error)
            self._active[key] = finished
            event.set()
            self._active.pop(key, None)
            self._finished[key] = finished

        def is_active(self, key: Any) -> bool:
            slot = self._active.get(key)
            return slot is not None and not slot[0].is_set()

        @property
        def stats(self) -> CoalesceStats:
            return CoalesceStats(
                active=sum(1 for slot in self._active.values() if not slot[0].is_set()),
                coalesced=self._coalesced,
                total=self._total,
            )

    calls: list[str] = []

    def _fn(value: str) -> str:
        calls.append(value)
        return value * 2

    runnable = RunnableLambda(_fn).with_coalesce(backend=_Backend())
    assert runnable.invoke("ab") == "abab"
    assert runnable.batch(["ab", "ab"]) == ["abab", "abab"]
    assert calls == ["ab", "ab"]


def test_batch_return_exceptions() -> None:
    def _fn(value: str) -> str:
        if value == "bad":
            msg = "bad"
            raise RuntimeError(msg)
        return value

    runnable = RunnableLambda(_fn).with_coalesce()
    results = runnable.batch(["ok", "bad", "ok"], return_exceptions=True)
    assert results[0] == "ok"
    assert isinstance(results[1], RuntimeError)
    assert results[2] == "ok"
