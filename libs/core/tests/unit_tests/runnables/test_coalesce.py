"""Tests for request coalescing on runnables."""

from __future__ import annotations

import asyncio
import threading
import time
from typing import Any

import pytest

from langchain_core.callbacks import BaseCallbackHandler
from langchain_core.runnables import RunnableLambda
from langchain_core.runnables.coalesce import InMemoryCoalesceBackend, make_coalesce_key


def test_make_coalesce_key_ignores_dict_order() -> None:
    assert make_coalesce_key({"a": 1, "b": 2}) == make_coalesce_key({"b": 2, "a": 1})


def test_invoke_coalesces_concurrent_identical_requests() -> None:
    call_count = 0
    lock = threading.Lock()
    started = threading.Event()
    release = threading.Event()
    ready = threading.Barrier(3)

    def slow_add_one(x: int) -> int:
        nonlocal call_count
        with lock:
            call_count += 1
        started.set()
        release.wait(timeout=5)
        return x + 1

    backend = InMemoryCoalesceBackend()
    runnable = RunnableLambda(slow_add_one).with_coalesce(backend=backend)
    results: list[int] = []
    errors: list[BaseException] = []

    def worker() -> None:
        try:
            ready.wait(timeout=5)
            results.append(runnable.invoke(1))
        except BaseException as e:
            errors.append(e)

    threads = [threading.Thread(target=worker) for _ in range(3)]
    for thread in threads:
        thread.start()
    assert started.wait(timeout=5)
    deadline = time.time() + 5
    while backend.stats["total"] < 3 and time.time() < deadline:
        time.sleep(0.001)
    assert backend.stats["total"] == 3
    release.set()
    for thread in threads:
        thread.join(timeout=10)

    assert not errors
    assert results == [2, 2, 2]
    assert call_count == 1
    assert backend.stats["coalesced"] == 2
    assert backend.stats["total"] == 3


def test_invoke_runs_fresh_after_completion() -> None:
    call_count = 0

    def add_one(x: int) -> int:
        nonlocal call_count
        call_count += 1
        return x + 1

    runnable = RunnableLambda(add_one).with_coalesce()
    assert runnable.invoke(1) == 2
    assert runnable.invoke(1) == 2
    assert call_count == 2


def test_invoke_does_not_coalesce_different_inputs() -> None:
    call_count = 0

    def add_one(x: int) -> int:
        nonlocal call_count
        call_count += 1
        return x + 1

    runnable = RunnableLambda(add_one).with_coalesce()
    assert runnable.invoke(1) == 2
    assert runnable.invoke(2) == 3
    assert call_count == 2


def test_invoke_ignores_config_and_kwargs_for_coalescing() -> None:
    call_count = 0
    started = threading.Event()
    release = threading.Event()

    def echo(x: int, *, tag: str = "") -> int:
        nonlocal call_count
        call_count += 1
        started.set()
        release.wait(timeout=5)
        return x

    backend = InMemoryCoalesceBackend()
    runnable = RunnableLambda(echo).with_coalesce(backend=backend)
    results: list[int] = []

    def worker(config: dict[str, Any], tag: str) -> None:
        results.append(runnable.invoke(5, config=config, tag=tag))

    threads = [
        threading.Thread(
            target=worker,
            kwargs={"config": {"tags": ["a"]}, "tag": "first"},
        ),
        threading.Thread(
            target=worker,
            kwargs={"config": {"tags": ["b"]}, "tag": "second"},
        ),
    ]
    for thread in threads:
        thread.start()
    assert started.wait(timeout=5)
    release.set()
    for thread in threads:
        thread.join(timeout=10)

    assert results == [5, 5]
    assert call_count == 1


@pytest.mark.asyncio
async def test_ainvoke_coalesces_concurrent_identical_requests() -> None:
    call_count = 0
    start = asyncio.Event()

    async def slow_add_one(x: int) -> int:
        nonlocal call_count
        call_count += 1
        await start.wait()
        await asyncio.sleep(0.01)
        return x + 1

    backend = InMemoryCoalesceBackend()
    runnable = RunnableLambda(slow_add_one).with_coalesce(backend=backend)

    tasks = [asyncio.create_task(runnable.ainvoke(1)) for _ in range(3)]
    await asyncio.sleep(0.01)
    start.set()
    results = await asyncio.gather(*tasks)

    assert results == [2, 2, 2]
    assert call_count == 1
    assert backend.stats["coalesced"] == 2


def test_stream_replays_chunks_for_joined_callers() -> None:
    call_count = 0
    started = threading.Event()
    release = threading.Event()

    def gen(x: int) -> Any:
        nonlocal call_count
        call_count += 1
        started.set()
        release.wait(timeout=5)
        yield x
        yield x + 1

    backend = InMemoryCoalesceBackend()
    runnable = RunnableLambda(gen).with_coalesce(backend=backend)
    leader_chunks: list[int] = []
    joiner_chunks: list[int] = []

    def leader() -> None:
        leader_chunks.extend(runnable.stream(10))

    def joiner() -> None:
        joiner_chunks.extend(runnable.stream(10))

    leader_thread = threading.Thread(target=leader)
    joiner_thread = threading.Thread(target=joiner)
    leader_thread.start()
    joiner_thread.start()
    assert started.wait(timeout=5)
    release.set()
    leader_thread.join(timeout=10)
    joiner_thread.join(timeout=10)

    assert leader_chunks == [10, 11]
    assert joiner_chunks == [10, 11]
    assert call_count == 1


def test_batch_coalesces_per_item_and_preserves_order() -> None:
    call_count = 0
    lock = threading.Lock()

    def add_one(x: int) -> int:
        nonlocal call_count
        with lock:
            call_count += 1
        time.sleep(0.02)
        return x + 1

    backend = InMemoryCoalesceBackend()
    runnable = RunnableLambda(add_one).with_coalesce(backend=backend)
    outputs = runnable.batch([1, 2, 1, 3, 2])
    assert outputs == [2, 3, 2, 4, 3]
    assert call_count == 3


def test_batch_as_completed_yields_coalesced_duplicates_consecutively() -> None:
    call_count = 0
    lock = threading.Lock()

    def slow_add_one(x: int) -> int:
        nonlocal call_count
        with lock:
            call_count += 1
        time.sleep(0.05 - 0.01 * x)
        return x + 1

    backend = InMemoryCoalesceBackend()
    runnable = RunnableLambda(slow_add_one).with_coalesce(backend=backend)
    completed = list(runnable.batch_as_completed([3, 1, 3, 2, 1]))
    indices = [index for index, _ in completed]
    values = [value for _, value in completed]

    assert sorted(values) == [2, 2, 3, 4, 4]
    assert indices.index(0) + 1 == indices.index(2)
    assert indices.index(1) + 1 == indices.index(4)
    assert call_count == 3


def test_coalesce_info_and_clear() -> None:
    started = threading.Event()
    release = threading.Event()

    def slow_identity(x: int) -> int:
        started.set()
        release.wait(timeout=5)
        return x

    backend = InMemoryCoalesceBackend()
    runnable = RunnableLambda(slow_identity).with_coalesce(backend=backend)
    error: BaseException | None = None

    def leader() -> None:
        runnable.invoke(1)

    def joiner() -> None:
        nonlocal error
        try:
            runnable.invoke(1)
        except BaseException as e:
            error = e

    leader_thread = threading.Thread(target=leader)
    joiner_thread = threading.Thread(target=joiner)
    leader_thread.start()
    joiner_thread.start()
    assert started.wait(timeout=5)

    stats = runnable.coalesce_info()
    assert stats["active"] == 1
    assert stats["total"] >= 2

    runnable.coalesce_clear()
    joiner_thread.join(timeout=5)
    release.set()
    leader_thread.join(timeout=5)
    assert isinstance(error, asyncio.CancelledError)
    assert runnable.coalesce_info() == {"active": 0, "coalesced": 0, "total": 0}


def test_joined_callers_fire_chain_callbacks() -> None:
    events: list[str] = []
    lock = threading.Lock()

    class EventHandler(BaseCallbackHandler):
        def on_chain_start(
            self,
            serialized: dict[str, Any],
            inputs: dict[str, Any],
            *,
            run_id: Any,
            parent_run_id: Any | None = None,
            tags: list[str] | None = None,
            metadata: dict[str, Any] | None = None,
            **kwargs: Any,
        ) -> Any:
            del serialized, inputs, run_id, tags, metadata, kwargs
            if parent_run_id is None:
                with lock:
                    events.append("start")

        def on_chain_end(
            self,
            outputs: dict[str, Any],
            *,
            run_id: Any,
            parent_run_id: Any | None = None,
            **kwargs: Any,
        ) -> Any:
            del outputs, run_id, kwargs
            if parent_run_id is None:
                with lock:
                    events.append("end")

    started = threading.Event()
    release = threading.Event()
    ready = threading.Barrier(2)

    def slow_identity(x: int) -> int:
        started.set()
        release.wait(timeout=5)
        return x

    backend = InMemoryCoalesceBackend()
    runnable = RunnableLambda(slow_identity).with_coalesce(backend=backend)

    def worker() -> None:
        ready.wait(timeout=5)
        runnable.invoke(
            1,
            config={"callbacks": [EventHandler()], "run_name": "coalesce-wrapper"},
        )

    threads = [threading.Thread(target=worker) for _ in range(2)]
    for thread in threads:
        thread.start()
    assert started.wait(timeout=5)
    release.set()
    for thread in threads:
        thread.join(timeout=10)

    assert events.count("start") == 2
    assert events.count("end") == 2


def test_separate_wrappers_use_independent_backends() -> None:
    call_count = 0
    started = threading.Event()
    release = threading.Event()

    def slow_identity(x: int) -> int:
        nonlocal call_count
        call_count += 1
        started.set()
        release.wait(timeout=5)
        return x

    first = RunnableLambda(slow_identity).with_coalesce()
    second = RunnableLambda(slow_identity).with_coalesce()
    results: list[int] = []

    def worker(runnable: Any) -> None:
        results.append(runnable.invoke(1))

    threads = [
        threading.Thread(target=worker, args=(first,)),
        threading.Thread(target=worker, args=(second,)),
    ]
    for thread in threads:
        thread.start()
    assert started.wait(timeout=5)
    release.set()
    for thread in threads:
        thread.join(timeout=10)

    assert sorted(results) == [1, 1]
    assert call_count == 2


def test_shared_backend_coalesces_across_wrappers() -> None:
    call_count = 0
    started = threading.Event()
    release = threading.Event()

    def slow_identity(x: int) -> int:
        nonlocal call_count
        call_count += 1
        started.set()
        release.wait(timeout=5)
        return x

    backend = InMemoryCoalesceBackend()
    first = RunnableLambda(slow_identity).with_coalesce(backend=backend)
    second = RunnableLambda(slow_identity).with_coalesce(backend=backend)
    results: list[int] = []

    def worker(runnable: Any) -> None:
        results.append(runnable.invoke(1))

    threads = [
        threading.Thread(target=worker, args=(first,)),
        threading.Thread(target=worker, args=(second,)),
    ]
    for thread in threads:
        thread.start()
    assert started.wait(timeout=5)
    release.set()
    for thread in threads:
        thread.join(timeout=10)

    assert sorted(results) == [1, 1]
    assert call_count == 1


def test_get_graph_delegates_to_bound_runnable() -> None:
    runnable = RunnableLambda(lambda x: x).with_coalesce()
    graph = runnable.get_graph()
    assert graph.nodes
