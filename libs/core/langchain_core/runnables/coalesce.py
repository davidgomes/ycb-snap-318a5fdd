"""Request coalescing for `Runnable` objects.

Coalescing deduplicates concurrent identical requests: when several callers invoke
a coalesced `Runnable` with the same input while an execution for that input is
in flight, only one execution runs and every caller receives its result.
"""

from __future__ import annotations

import asyncio
import dataclasses
import hashlib
import json
import threading
from abc import ABC, abstractmethod
from collections import deque
from collections.abc import AsyncIterator, Iterator, Mapping, Sequence
from typing import TYPE_CHECKING, Any, Literal, NamedTuple, cast, overload

from pydantic import BaseModel
from typing_extensions import override

from langchain_core.runnables.base import RunnableBindingBase
from langchain_core.runnables.config import (
    RunnableConfig,
    get_config_list,
    run_in_executor,
)
from langchain_core.runnables.utils import Input, Output

if TYPE_CHECKING:
    from collections.abc import Callable

    from langchain_core.runnables.graph import Graph


class CoalesceStats(NamedTuple):
    """Snapshot of a coalescing backend's counters."""

    active: int
    """Number of executions currently in flight."""
    coalesced: int
    """Number of calls that joined an in-flight execution instead of running."""
    total: int
    """Total number of calls registered with the backend."""


class CoalesceBackend(ABC):
    """Tracks in-flight executions so that identical concurrent calls can share one.

    A caller first calls `register`. If it returns `True` the caller is the leader:
    it must run the work and then call `complete` with the result or error. If it
    returns `False` an execution for that key is already in flight and the caller
    must call `join` to wait for, and receive, that execution's outcome.

    Once an execution is completed its key is no longer active, so the next
    `register` for that key starts a fresh execution.
    """

    @abstractmethod
    def register(self, key: str) -> bool:
        """Register a call for `key`.

        Args:
            key: The coalescing key of the call.

        Returns:
            `True` if the caller should run the execution, `False` if it should
            join an execution already in flight.
        """

    @abstractmethod
    def join(self, key: str) -> Any:
        """Block until the in-flight execution for `key` completes.

        Args:
            key: The coalescing key previously passed to `register`.

        Returns:
            The result the leader passed to `complete`.

        Raises:
            BaseException: The error the leader passed to `complete`.
        """

    @abstractmethod
    def complete(
        self, key: str, *, result: Any = None, error: BaseException | None = None
    ) -> None:
        """Mark the execution for `key` as finished and release its joiners.

        Args:
            key: The coalescing key of the execution.
            result: The result to hand to joiners.
            error: If set, joiners raise this error instead of returning `result`.
        """

    @abstractmethod
    def is_active(self, key: str) -> bool:
        """Return whether an execution for `key` is currently in flight."""

    @property
    @abstractmethod
    def stats(self) -> CoalesceStats:
        """Current counters of the backend."""

    def clear(self) -> None:
        """Cancel all waiters with `asyncio.CancelledError` and reset the stats."""
        msg = f"{type(self).__name__} does not support clear()."
        raise NotImplementedError(msg)

    async def aregister(self, key: str) -> bool:
        """Async version of `register`."""
        return await run_in_executor(None, self.register, key)

    async def ajoin(self, key: str) -> Any:
        """Async version of `join`."""
        return await run_in_executor(None, self.join, key)

    async def acomplete(
        self, key: str, *, result: Any = None, error: BaseException | None = None
    ) -> None:
        """Async version of `complete`."""
        await run_in_executor(None, self.complete, key, result=result, error=error)

    async def ais_active(self, key: str) -> bool:
        """Async version of `is_active`."""
        return await run_in_executor(None, self.is_active, key)


class _Execution:
    __slots__ = ("async_waiters", "done", "error", "pending_joins", "result")

    def __init__(self) -> None:
        self.done = threading.Event()
        self.result: Any = None
        self.error: BaseException | None = None
        self.pending_joins = 0
        self.async_waiters: list[tuple[asyncio.AbstractEventLoop, asyncio.Future]] = []

    def outcome(self) -> Any:
        if self.error is not None:
            raise self.error
        return self.result


def _wake(future: asyncio.Future) -> None:
    if not future.done():
        future.set_result(None)


class InMemoryCoalesceBackend(CoalesceBackend):
    """Thread-safe, process-local `CoalesceBackend`.

    Sync and async callers, including callers on different threads or event
    loops, can share one instance.
    """

    def __init__(self) -> None:
        """Create an empty backend."""
        self._lock = threading.Lock()
        self._active: dict[str, _Execution] = {}
        # Completed executions whose registered joiners have not called `join` yet.
        self._unclaimed: dict[str, deque[_Execution]] = {}
        self._coalesced = 0
        self._total = 0

    @override
    def register(self, key: str) -> bool:
        with self._lock:
            self._total += 1
            execution = self._active.get(key)
            if execution is None:
                # Joiners registered on an earlier execution that have not claimed
                # it yet fall back to this fresh one, so stale results never leak.
                self._unclaimed.pop(key, None)
                self._active[key] = _Execution()
                return True
            execution.pending_joins += 1
            self._coalesced += 1
            return False

    def _claim(self, key: str) -> _Execution:
        with self._lock:
            unclaimed = self._unclaimed.get(key)
            if unclaimed:
                execution = unclaimed[0]
                execution.pending_joins -= 1
                if execution.pending_joins <= 0:
                    unclaimed.popleft()
                    if not unclaimed:
                        del self._unclaimed[key]
                return execution
            active = self._active.get(key)
            if active is None:
                msg = f"No in-flight execution to join for key {key!r}."
                raise ValueError(msg)
            active.pending_joins = max(0, active.pending_joins - 1)
            return active

    @override
    def join(self, key: str) -> Any:
        execution = self._claim(key)
        execution.done.wait()
        return execution.outcome()

    @override
    async def ajoin(self, key: str) -> Any:
        execution = self._claim(key)
        with self._lock:
            if execution.done.is_set():
                return execution.outcome()
            loop = asyncio.get_running_loop()
            future = loop.create_future()
            execution.async_waiters.append((loop, future))
        await future
        return execution.outcome()

    def _finish(
        self, key: str, execution: _Execution, result: Any, error: BaseException | None
    ) -> list[tuple[asyncio.AbstractEventLoop, asyncio.Future]]:
        """Resolve `execution`. Must be called with the lock held."""
        execution.result = result
        execution.error = error
        if execution.pending_joins > 0:
            self._unclaimed.setdefault(key, deque()).append(execution)
        execution.done.set()
        waiters = execution.async_waiters
        execution.async_waiters = []
        return waiters

    @staticmethod
    def _wake_async_waiters(
        waiters: list[tuple[asyncio.AbstractEventLoop, asyncio.Future]],
    ) -> None:
        for loop, future in waiters:
            try:
                loop.call_soon_threadsafe(_wake, future)
            except RuntimeError:
                # The waiter's event loop has been closed.
                continue

    @override
    def complete(
        self, key: str, *, result: Any = None, error: BaseException | None = None
    ) -> None:
        with self._lock:
            execution = self._active.pop(key, None)
            if execution is None:
                return
            waiters = self._finish(key, execution, result, error)
        self._wake_async_waiters(waiters)

    @override
    def is_active(self, key: str) -> bool:
        with self._lock:
            return key in self._active

    @property
    @override
    def stats(self) -> CoalesceStats:
        with self._lock:
            return CoalesceStats(
                active=len(self._active),
                coalesced=self._coalesced,
                total=self._total,
            )

    @override
    def clear(self) -> None:
        with self._lock:
            waiters = []
            for key, execution in self._active.items():
                waiters.extend(
                    self._finish(key, execution, None, asyncio.CancelledError())
                )
            self._active.clear()
            self._coalesced = 0
            self._total = 0
        self._wake_async_waiters(waiters)

    @override
    async def aregister(self, key: str) -> bool:
        return self.register(key)

    @override
    async def acomplete(
        self, key: str, *, result: Any = None, error: BaseException | None = None
    ) -> None:
        self.complete(key, result=result, error=error)

    @override
    async def ais_active(self, key: str) -> bool:
        return self.is_active(key)


def _canonicalize(value: Any) -> Any:
    """Convert `value` to a JSON-compatible structure independent of dict ordering."""
    if value is None or isinstance(value, (bool, int, float, str)):
        return value
    if isinstance(value, Mapping):
        items = [[_canonicalize(k), _canonicalize(v)] for k, v in value.items()]
        items.sort(key=lambda item: json.dumps(item[0], sort_keys=True))
        return {"mapping": items}
    if isinstance(value, (list, tuple)):
        return [_canonicalize(item) for item in value]
    if isinstance(value, (set, frozenset)):
        members = [_canonicalize(item) for item in value]
        return {"set": sorted(members, key=lambda m: json.dumps(m, sort_keys=True))}
    if isinstance(value, (bytes, bytearray)):
        return {"bytes": bytes(value).hex()}
    type_name = f"{type(value).__module__}.{type(value).__qualname__}"
    if isinstance(value, BaseModel):
        return {"model": type_name, "data": _canonicalize(value.model_dump())}
    if dataclasses.is_dataclass(value) and not isinstance(value, type):
        return {"model": type_name, "data": _canonicalize(dataclasses.asdict(value))}
    return {"object": type_name, "repr": repr(value)}


def _coalesce_key(value: Any) -> str:
    canonical = json.dumps(
        _canonicalize(value), sort_keys=True, separators=(",", ":"), default=repr
    )
    return hashlib.sha256(canonical.encode()).hexdigest()


class _StreamedChunks(NamedTuple):
    """Result stored by a streaming leader so joiners can replay every chunk."""

    chunks: tuple[Any, ...]


def _aggregate(chunks: Sequence[Any]) -> Any:
    final: Any = None
    for index, chunk in enumerate(chunks):
        if index == 0:
            final = chunk
            continue
        try:
            final = final + chunk
        except TypeError:
            final = chunk
    return final


def _as_output(value: Any) -> Any:
    if isinstance(value, _StreamedChunks):
        return _aggregate(value.chunks)
    return value


def _as_chunks(value: Any) -> tuple[Any, ...]:
    if isinstance(value, _StreamedChunks):
        return value.chunks
    return (value,)


def _abandoned_error() -> RuntimeError:
    return RuntimeError(
        "The coalesced execution was abandoned before it completed; "
        "joined callers cannot receive its result."
    )


class RunnableCoalesce(RunnableBindingBase[Input, Output]):  # type: ignore[no-redef]
    """Coalesce concurrent identical calls to a `Runnable` into one execution.

    Create one with `Runnable.with_coalesce`. The coalescing key is derived from
    the input value only; config and kwargs are ignored, and dictionary key order
    does not matter. Joined callers still emit their own chain start/end callbacks.
    """

    backend: CoalesceBackend
    """Backend tracking in-flight executions. Wrappers sharing a backend coalesce
    with each other."""

    @classmethod
    @override
    def is_lc_serializable(cls) -> bool:
        return False

    @override
    def get_graph(self, config: RunnableConfig | None = None) -> Graph:
        return self.bound.get_graph(config)

    def coalesce_info(self) -> CoalesceStats:
        """Return the current coalescing stats of the backend."""
        return self.backend.stats

    def coalesce_clear(self) -> None:
        """Cancel waiting callers with `asyncio.CancelledError` and reset stats."""
        self.backend.clear()

    def _resolver(self, key: str) -> Callable[[Input], Output]:
        return lambda _input: _as_output(self.backend.join(key))

    def _aresolver(self, key: str) -> Callable[[Input], Any]:
        async def _resolve(_input: Input) -> Any:
            return _as_output(await self.backend.ajoin(key))

        return _resolve

    @override
    def invoke(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any | None,
    ) -> Output:
        key = _coalesce_key(input)
        if not self.backend.register(key):
            return self._call_with_config(self._resolver(key), input, config)
        try:
            result = self.bound.invoke(
                input, self._merge_configs(config), **{**self.kwargs, **kwargs}
            )
        except BaseException as e:
            self.backend.complete(key, error=e)
            raise
        self.backend.complete(key, result=result)
        return result

    @override
    async def ainvoke(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any | None,
    ) -> Output:
        key = _coalesce_key(input)
        if not await self.backend.aregister(key):
            return await self._acall_with_config(self._aresolver(key), input, config)
        try:
            result = await self.bound.ainvoke(
                input, self._merge_configs(config), **{**self.kwargs, **kwargs}
            )
        except BaseException as e:
            await self.backend.acomplete(key, error=e)
            raise
        await self.backend.acomplete(key, result=result)
        return result

    @override
    def stream(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any | None,
    ) -> Iterator[Output]:
        key = _coalesce_key(input)
        if not self.backend.register(key):
            chunks: list[Any] = []

            def _resolve(_input: Input) -> Any:
                value = self.backend.join(key)
                chunks.extend(_as_chunks(value))
                return _as_output(value)

            self._call_with_config(_resolve, input, config)
            yield from chunks
            return

        collected: list[Output] = []
        completed = False
        try:
            for chunk in self.bound.stream(
                input, self._merge_configs(config), **{**self.kwargs, **kwargs}
            ):
                collected.append(chunk)
                yield chunk
        except GeneratorExit:
            completed = True
            self.backend.complete(key, error=_abandoned_error())
            raise
        except BaseException as e:
            completed = True
            self.backend.complete(key, error=e)
            raise
        finally:
            if not completed:
                self.backend.complete(key, result=_StreamedChunks(tuple(collected)))

    @override
    async def astream(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any | None,
    ) -> AsyncIterator[Output]:
        key = _coalesce_key(input)
        if not await self.backend.aregister(key):
            chunks: list[Any] = []

            async def _resolve(_input: Input) -> Any:
                value = await self.backend.ajoin(key)
                chunks.extend(_as_chunks(value))
                return _as_output(value)

            await self._acall_with_config(_resolve, input, config)
            for chunk in chunks:
                yield chunk
            return

        collected: list[Output] = []
        completed = False
        try:
            async for chunk in self.bound.astream(
                input, self._merge_configs(config), **{**self.kwargs, **kwargs}
            ):
                collected.append(chunk)
                yield chunk
        except GeneratorExit:
            completed = True
            await self.backend.acomplete(key, error=_abandoned_error())
            raise
        except BaseException as e:
            completed = True
            await self.backend.acomplete(key, error=e)
            raise
        finally:
            if not completed:
                await self.backend.acomplete(
                    key, result=_StreamedChunks(tuple(collected))
                )

    def _batch_configs(
        self,
        inputs: Sequence[Input],
        config: RunnableConfig | Sequence[RunnableConfig] | None,
    ) -> list[RunnableConfig]:
        return [
            self._merge_configs(conf)
            for conf in get_config_list(
                cast("RunnableConfig | list[RunnableConfig] | None", config),
                len(inputs),
            )
        ]

    @staticmethod
    def _group(keys: list[str], leaders: list[bool]) -> dict[str, list[int]]:
        """Map each key to its item indices, leader first when there is one."""
        groups: dict[str, list[int]] = {}
        for index, key in enumerate(keys):
            group = groups.setdefault(key, [])
            if leaders[index]:
                group.insert(0, index)
            else:
                group.append(index)
        return groups

    def _join_item(
        self, key: str, input_: Input, config: RunnableConfig
    ) -> Output | Exception:
        try:
            return self._call_with_config(self._resolver(key), input_, config)
        except Exception as e:
            return e

    async def _ajoin_item(
        self, key: str, input_: Input, config: RunnableConfig
    ) -> Output | Exception:
        try:
            return await self._acall_with_config(self._aresolver(key), input_, config)
        except Exception as e:
            return e

    @override
    def batch(
        self,
        inputs: list[Input],
        config: RunnableConfig | list[RunnableConfig] | None = None,
        *,
        return_exceptions: bool = False,
        **kwargs: Any | None,
    ) -> list[Output]:
        if not inputs:
            return []
        configs = self._batch_configs(inputs, config)
        keys = [_coalesce_key(input_) for input_ in inputs]
        leaders = [self.backend.register(key) for key in keys]
        leader_indices = [i for i, is_leader in enumerate(leaders) if is_leader]
        results: list[Output | Exception | None] = [None] * len(inputs)

        pending = set(leader_indices)
        try:
            if leader_indices:
                outputs = self.bound.batch(
                    [inputs[i] for i in leader_indices],
                    [configs[i] for i in leader_indices],
                    return_exceptions=True,
                    **{**self.kwargs, **kwargs},
                )
                for index, output in zip(leader_indices, outputs, strict=False):
                    results[index] = output
                    pending.discard(index)
                    if isinstance(output, Exception):
                        self.backend.complete(keys[index], error=output)
                    else:
                        self.backend.complete(keys[index], result=output)
        except BaseException as e:
            for index in pending:
                self.backend.complete(keys[index], error=e)
            raise

        for index, is_leader in enumerate(leaders):
            if not is_leader:
                results[index] = self._join_item(
                    keys[index], inputs[index], configs[index]
                )

        if not return_exceptions:
            for item in results:
                if isinstance(item, Exception):
                    raise item
        return cast("list[Output]", results)

    @override
    async def abatch(
        self,
        inputs: list[Input],
        config: RunnableConfig | list[RunnableConfig] | None = None,
        *,
        return_exceptions: bool = False,
        **kwargs: Any | None,
    ) -> list[Output]:
        if not inputs:
            return []
        configs = self._batch_configs(inputs, config)
        keys = [_coalesce_key(input_) for input_ in inputs]
        leaders = [await self.backend.aregister(key) for key in keys]
        leader_indices = [i for i, is_leader in enumerate(leaders) if is_leader]
        results: list[Output | Exception | None] = [None] * len(inputs)

        pending = set(leader_indices)
        try:
            if leader_indices:
                outputs = await self.bound.abatch(
                    [inputs[i] for i in leader_indices],
                    [configs[i] for i in leader_indices],
                    return_exceptions=True,
                    **{**self.kwargs, **kwargs},
                )
                for index, output in zip(leader_indices, outputs, strict=False):
                    results[index] = output
                    pending.discard(index)
                    if isinstance(output, Exception):
                        await self.backend.acomplete(keys[index], error=output)
                    else:
                        await self.backend.acomplete(keys[index], result=output)
        except BaseException as e:
            for index in pending:
                await self.backend.acomplete(keys[index], error=e)
            raise

        joiner_indices = [i for i, is_leader in enumerate(leaders) if not is_leader]
        joined = await asyncio.gather(
            *(self._ajoin_item(keys[i], inputs[i], configs[i]) for i in joiner_indices)
        )
        for index, result in zip(joiner_indices, joined, strict=False):
            results[index] = result

        if not return_exceptions:
            for item in results:
                if isinstance(item, Exception):
                    raise item
        return cast("list[Output]", results)

    @overload
    def batch_as_completed(
        self,
        inputs: Sequence[Input],
        config: RunnableConfig | Sequence[RunnableConfig] | None = None,
        *,
        return_exceptions: Literal[False] = False,
        **kwargs: Any,
    ) -> Iterator[tuple[int, Output]]: ...

    @overload
    def batch_as_completed(
        self,
        inputs: Sequence[Input],
        config: RunnableConfig | Sequence[RunnableConfig] | None = None,
        *,
        return_exceptions: Literal[True],
        **kwargs: Any,
    ) -> Iterator[tuple[int, Output | Exception]]: ...

    @override
    def batch_as_completed(
        self,
        inputs: Sequence[Input],
        config: RunnableConfig | Sequence[RunnableConfig] | None = None,
        *,
        return_exceptions: bool = False,
        **kwargs: Any | None,
    ) -> Iterator[tuple[int, Output | Exception]]:
        if not inputs:
            return
        configs = self._batch_configs(inputs, config)
        keys = [_coalesce_key(input_) for input_ in inputs]
        leaders = [self.backend.register(key) for key in keys]
        groups = self._group(keys, leaders)
        leader_indices = [i for i, is_leader in enumerate(leaders) if is_leader]

        def _emit(index: int, result: Output | Exception) -> tuple[int, Any]:
            if isinstance(result, Exception) and not return_exceptions:
                raise result
            return index, result

        pending = set(leader_indices)
        try:
            if leader_indices:
                for position, output in self.bound.batch_as_completed(
                    [inputs[i] for i in leader_indices],
                    [configs[i] for i in leader_indices],
                    return_exceptions=True,
                    **{**self.kwargs, **kwargs},
                ):
                    index = leader_indices[position]
                    key = keys[index]
                    pending.discard(index)
                    if isinstance(output, Exception):
                        self.backend.complete(key, error=output)
                    else:
                        self.backend.complete(key, result=output)
                    yield _emit(index, output)
                    for dup in groups[key][1:]:
                        yield _emit(
                            dup, self._join_item(key, inputs[dup], configs[dup])
                        )
        except GeneratorExit:
            for index in pending:
                self.backend.complete(keys[index], error=_abandoned_error())
            raise
        except BaseException as e:
            for index in pending:
                self.backend.complete(keys[index], error=e)
            raise

        for key, indices in groups.items():
            if leaders[indices[0]]:
                continue
            for index in indices:
                yield _emit(index, self._join_item(key, inputs[index], configs[index]))

    @overload
    def abatch_as_completed(
        self,
        inputs: Sequence[Input],
        config: RunnableConfig | Sequence[RunnableConfig] | None = None,
        *,
        return_exceptions: Literal[False] = False,
        **kwargs: Any | None,
    ) -> AsyncIterator[tuple[int, Output]]: ...

    @overload
    def abatch_as_completed(
        self,
        inputs: Sequence[Input],
        config: RunnableConfig | Sequence[RunnableConfig] | None = None,
        *,
        return_exceptions: Literal[True],
        **kwargs: Any | None,
    ) -> AsyncIterator[tuple[int, Output | Exception]]: ...

    @override
    async def abatch_as_completed(
        self,
        inputs: Sequence[Input],
        config: RunnableConfig | Sequence[RunnableConfig] | None = None,
        *,
        return_exceptions: bool = False,
        **kwargs: Any | None,
    ) -> AsyncIterator[tuple[int, Output | Exception]]:
        if not inputs:
            return
        configs = self._batch_configs(inputs, config)
        keys = [_coalesce_key(input_) for input_ in inputs]
        leaders = [await self.backend.aregister(key) for key in keys]
        groups = self._group(keys, leaders)
        leader_indices = [i for i, is_leader in enumerate(leaders) if is_leader]

        def _emit(index: int, result: Output | Exception) -> tuple[int, Any]:
            if isinstance(result, Exception) and not return_exceptions:
                raise result
            return index, result

        pending = set(leader_indices)
        try:
            if leader_indices:
                async for position, output in self.bound.abatch_as_completed(
                    [inputs[i] for i in leader_indices],
                    [configs[i] for i in leader_indices],
                    return_exceptions=True,
                    **{**self.kwargs, **kwargs},
                ):
                    index = leader_indices[position]
                    key = keys[index]
                    pending.discard(index)
                    if isinstance(output, Exception):
                        await self.backend.acomplete(key, error=output)
                    else:
                        await self.backend.acomplete(key, result=output)
                    yield _emit(index, output)
                    for dup in groups[key][1:]:
                        joined = await self._ajoin_item(key, inputs[dup], configs[dup])
                        yield _emit(dup, joined)
        except GeneratorExit:
            for index in pending:
                await self.backend.acomplete(keys[index], error=_abandoned_error())
            raise
        except BaseException as e:
            for index in pending:
                await self.backend.acomplete(keys[index], error=e)
            raise

        for key, indices in groups.items():
            if leaders[indices[0]]:
                continue
            for index in indices:
                joined = await self._ajoin_item(key, inputs[index], configs[index])
                yield _emit(index, joined)
