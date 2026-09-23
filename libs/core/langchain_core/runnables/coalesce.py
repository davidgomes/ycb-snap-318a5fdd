"""Request coalescing for `Runnable` objects.

Coalescing deduplicates concurrent identical requests: while an execution for a
given input is in flight, every other call with an equal input waits for that
execution and receives its result instead of running the `Runnable` again.
"""

from __future__ import annotations

import asyncio
import dataclasses
import threading
from abc import ABC, abstractmethod
from collections import deque
from collections.abc import (
    AsyncIterator,
    Awaitable,
    Callable,
    Hashable,
    Iterator,
    Mapping,
    Sequence,
)
from concurrent.futures import FIRST_COMPLETED, wait
from functools import partial
from typing import Any, Literal, NamedTuple, TypeVar, cast, overload

from pydantic import BaseModel, Field
from typing_extensions import override

from langchain_core.runnables.base import RunnableBindingBase
from langchain_core.runnables.config import (
    RunnableConfig,
    get_config_list,
    get_executor_for_config,
    run_in_executor,
)
from langchain_core.runnables.utils import Input, Output, gated_coro

T = TypeVar("T")


class CoalesceStats(NamedTuple):
    """Snapshot of the counters tracked by a `CoalesceBackend`."""

    active: int
    """Number of keys with an execution currently in flight."""

    coalesced: int
    """Number of registrations that joined an in-flight execution."""

    total: int
    """Number of registrations, whether they started or joined an execution."""


class CoalesceBackend(ABC):
    """Tracks in-flight executions so that identical concurrent calls share one.

    Callers follow this protocol for a key:

    1. Call `register(key)`. `True` means the caller owns the execution and must
        eventually call `complete(key, ...)`. `False` means an execution is
        already in flight and the caller must call `join(key)` exactly once.
    2. The owner runs the work and calls `complete` with its result or error.
        This releases every joined caller and frees the key, so the next
        `register(key)` starts a fresh execution.

    Keys are opaque hashable values.

    The async methods run their sync counterparts in an executor by default.
    Subclasses should override them when a native async implementation exists.
    """

    @abstractmethod
    def register(self, key: Hashable) -> bool:
        """Register a call for `key`.

        Args:
            key: The coalescing key of the call.

        Returns:
            `True` if the caller owns a new execution, `False` if it must join
            the execution already in flight.
        """

    @abstractmethod
    def join(self, key: Hashable) -> Any:
        """Wait for the in-flight execution for `key` to complete.

        Args:
            key: The coalescing key the caller registered with.

        Returns:
            The result passed to `complete`.

        Raises:
            BaseException: The error passed to `complete`, if any.
        """

    @abstractmethod
    def complete(
        self,
        key: Hashable,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        """Complete the execution for `key` and release its joined callers.

        Args:
            key: The coalescing key of the execution.
            result: The result to hand to joined callers.
            error: The error to raise in joined callers. Takes precedence over
                `result` when set.
        """

    @abstractmethod
    def is_active(self, key: Hashable) -> bool:
        """Check whether an execution for `key` is in flight.

        Args:
            key: The coalescing key to check.

        Returns:
            `True` if an execution for `key` is in flight.
        """

    @property
    @abstractmethod
    def stats(self) -> CoalesceStats:
        """Current counters of the backend."""

    def clear(self) -> None:
        """Cancel every waiting caller and reset the stats.

        Waiting callers receive `asyncio.CancelledError`.

        Raises:
            NotImplementedError: If the backend does not support clearing.
        """
        msg = f"{type(self).__name__} does not support clear()."
        raise NotImplementedError(msg)

    async def aregister(self, key: Hashable) -> bool:
        """Async version of `register`.

        Args:
            key: The coalescing key of the call.

        Returns:
            `True` if the caller owns a new execution, `False` if it must join
            the execution already in flight.
        """
        return await run_in_executor(None, self.register, key)

    async def ajoin(self, key: Hashable) -> Any:
        """Async version of `join`.

        Args:
            key: The coalescing key the caller registered with.

        Returns:
            The result passed to `complete`.
        """
        return await run_in_executor(None, self.join, key)

    async def acomplete(
        self,
        key: Hashable,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        """Async version of `complete`.

        Args:
            key: The coalescing key of the execution.
            result: The result to hand to joined callers.
            error: The error to raise in joined callers.
        """
        await run_in_executor(None, self.complete, key, result=result, error=error)

    async def ais_active(self, key: Hashable) -> bool:
        """Async version of `is_active`.

        Args:
            key: The coalescing key to check.

        Returns:
            `True` if an execution for `key` is in flight.
        """
        return await run_in_executor(None, self.is_active, key)


class _Execution:
    """State shared by the owner and the joined callers of one execution."""

    __slots__ = ("async_waiters", "done", "error", "event", "pending", "result")

    def __init__(self) -> None:
        self.event = threading.Event()
        self.async_waiters: list[tuple[asyncio.AbstractEventLoop, asyncio.Future]] = []
        # Callers that registered as joiners but have not called `join` yet.
        self.pending = 0
        self.done = False
        self.result: Any = None
        self.error: BaseException | None = None

    def settle(self, result: Any, error: BaseException | None) -> None:
        self.result = result
        self.error = error
        self.done = True
        self.event.set()
        waiters, self.async_waiters = self.async_waiters, []
        for loop, future in waiters:
            try:
                loop.call_soon_threadsafe(_wake, future)
            except RuntimeError:
                # The waiter's event loop is closed; nobody is left to wake.
                continue

    def unwrap(self) -> Any:
        if self.error is not None:
            raise self.error
        return self.result


def _wake(future: asyncio.Future) -> None:
    if not future.done():
        future.set_result(None)


class InMemoryCoalesceBackend(CoalesceBackend):
    """Thread-safe `CoalesceBackend` that keeps its state in process memory.

    Sync and async callers can be mixed freely: a sync owner running in a thread
    releases async joiners on their event loops, and vice versa.
    """

    def __init__(self) -> None:
        """Create an empty backend."""
        self._lock = threading.Lock()
        self._active: dict[Hashable, _Execution] = {}
        # Completed executions whose joiners registered before completion but
        # have not called `join` yet.
        self._settled: dict[Hashable, deque[_Execution]] = {}
        self._coalesced = 0
        self._total = 0

    @override
    def register(self, key: Hashable) -> bool:
        with self._lock:
            self._total += 1
            execution = self._active.get(key)
            if execution is None:
                self._active[key] = _Execution()
                return True
            execution.pending += 1
            self._coalesced += 1
            return False

    def _claim(self, key: Hashable) -> _Execution:
        settled = self._settled.get(key)
        if settled:
            finished = settled[0]
            finished.pending -= 1
            if finished.pending <= 0:
                settled.popleft()
                if not settled:
                    del self._settled[key]
            return finished
        active = self._active.get(key)
        if active is None:
            msg = f"No execution in flight for key {key!r}."
            raise KeyError(msg)
        active.pending = max(active.pending - 1, 0)
        return active

    @override
    def join(self, key: Hashable) -> Any:
        with self._lock:
            execution = self._claim(key)
        execution.event.wait()
        return execution.unwrap()

    @override
    async def ajoin(self, key: Hashable) -> Any:
        waiter: asyncio.Future | None = None
        with self._lock:
            execution = self._claim(key)
            if not execution.done:
                loop = asyncio.get_running_loop()
                waiter = loop.create_future()
                execution.async_waiters.append((loop, waiter))
        if waiter is not None:
            await waiter
        return execution.unwrap()

    def _settle_locked(
        self,
        key: Hashable,
        execution: _Execution,
        result: Any,
        error: BaseException | None,
    ) -> None:
        execution.settle(result, error)
        if execution.pending > 0:
            self._settled.setdefault(key, deque()).append(execution)

    @override
    def complete(
        self,
        key: Hashable,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        with self._lock:
            execution = self._active.pop(key, None)
            if execution is not None:
                self._settle_locked(key, execution, result, error)

    @override
    def is_active(self, key: Hashable) -> bool:
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
            for key, execution in self._active.items():
                self._settle_locked(key, execution, None, asyncio.CancelledError())
            self._active.clear()
            self._coalesced = 0
            self._total = 0

    @override
    async def aregister(self, key: Hashable) -> bool:
        return self.register(key)

    @override
    async def acomplete(
        self,
        key: Hashable,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        self.complete(key, result=result, error=error)

    @override
    async def ais_active(self, key: Hashable) -> bool:
        return self.is_active(key)


def _coalesce_key(value: Any) -> Hashable:
    """Build a hashable key that identifies `value` by content.

    Every node is tagged with its type so that e.g. `1`, `1.0` and `True` stay
    distinct, mappings and sets ignore iteration order, and unhashable leaves
    fall back to object identity.
    """
    if isinstance(value, Mapping):
        items = frozenset(
            (_coalesce_key(k), _coalesce_key(v)) for k, v in value.items()
        )
        return (type(value), items)
    if isinstance(value, (list, tuple)):
        return (type(value), tuple(_coalesce_key(item) for item in value))
    if isinstance(value, (set, frozenset)):
        return (type(value), frozenset(_coalesce_key(item) for item in value))
    if isinstance(value, BaseModel):
        return (type(value), _coalesce_key(dict(value)))
    if dataclasses.is_dataclass(value) and not isinstance(value, type):
        fields = tuple(
            (field.name, _coalesce_key(getattr(value, field.name)))
            for field in dataclasses.fields(value)
        )
        return (type(value), fields)
    try:
        hash(value)
    except TypeError:
        return (type(value), "id", id(value))
    return (type(value), value)


class _OwnerAbandonedError(Exception):
    """The owner stopped before producing an outcome, e.g. it was cancelled."""


def _owner_error(error: BaseException) -> BaseException:
    return error if isinstance(error, Exception) else _OwnerAbandonedError()


def _aggregate(chunks: list[Any]) -> Any:
    if not chunks:
        return None
    output = chunks[0]
    for chunk in chunks[1:]:
        try:
            output = output + chunk
        except TypeError:
            return chunks[-1]
    return output


class _CoalescedResult:
    """Outcome an owner hands to `CoalesceBackend.complete`.

    Holds either a final output or the chunks of a streamed execution, so that
    invoke-style and stream-style callers can join each other.
    """

    __slots__ = ("_chunks", "_output")

    def __init__(self, output: Any = None, chunks: list[Any] | None = None) -> None:
        self._output = output
        self._chunks = chunks

    @property
    def output(self) -> Any:
        return self._output if self._chunks is None else _aggregate(self._chunks)

    @property
    def chunks(self) -> list[Any]:
        return [self._output] if self._chunks is None else self._chunks


def _capture(func: Callable[..., T], *args: Any) -> T | Exception:
    try:
        return func(*args)
    except Exception as e:
        return e


async def _acapture(awaitable: Awaitable[T]) -> T | Exception:
    try:
        return await awaitable
    except Exception as e:
        return e


async def _aiter_one(value: T) -> AsyncIterator[T]:
    yield value


def _group_indices(keys: list[Hashable], owned: list[bool]) -> list[list[int]]:
    """Group input indices by key, putting groups that own an execution first."""
    groups: dict[Hashable, list[int]] = {}
    for index, key in enumerate(keys):
        groups.setdefault(key, []).append(index)
    return sorted(groups.values(), key=lambda group: not any(owned[i] for i in group))


class RunnableCoalesce(RunnableBindingBase[Input, Output]):  # type: ignore[no-redef]
    """Coalesce concurrent identical calls to a `Runnable` into one execution.

    While a call for a given input is in flight, other calls with an equal input
    wait for it and receive its result (or error) instead of running the bound
    `Runnable` again. Once the execution completes, the next call with that input
    runs fresh: results are not cached.

    `invoke`, `stream`, `batch`, `batch_as_completed` and their async versions
    all coalesce through the same backend, so e.g. an `ainvoke` can join an
    in-flight `astream`. Invoke-style callers joining a stream receive the
    chunks added together, and stream-style callers replay every chunk from the
    beginning once the execution completes. `transform`, `atransform` and
    `astream_events` pass through without coalescing.

    Inputs are compared by value only: config and call kwargs are ignored and
    mappings match regardless of key order. Joined callers share the owner's
    output object and emit their own chain start and end callbacks.

    The easiest way to create one is `Runnable.with_coalesce()`.

    Example:
        ```python
        import asyncio

        from langchain_core.runnables import RunnableLambda


        async def fetch(query: str) -> str:
            await asyncio.sleep(1)
            return query.upper()


        runnable = RunnableLambda(fetch).with_coalesce()


        async def main() -> None:
            # `fetch` runs once; both callers receive "HI".
            await asyncio.gather(runnable.ainvoke("hi"), runnable.ainvoke("hi"))
            print(runnable.coalesce_info())
            # CoalesceStats(active=0, coalesced=1, total=2)


        asyncio.run(main())
        ```
    """

    backend: CoalesceBackend = Field(default_factory=InMemoryCoalesceBackend)
    """Tracks in-flight executions. Wrappers sharing a backend coalesce together."""

    @classmethod
    @override
    def is_lc_serializable(cls) -> bool:
        return False

    def coalesce_info(self) -> CoalesceStats:
        """Get the current counters of the backend.

        Returns:
            The backend's stats.
        """
        return self.backend.stats

    def coalesce_clear(self) -> None:
        """Cancel every waiting caller and reset the backend's stats.

        Waiting callers receive `asyncio.CancelledError`. Executions already
        running are not interrupted, but new calls no longer join them.
        """
        self.backend.clear()

    def _call_kwargs(self, kwargs: Mapping[str, Any]) -> dict[str, Any]:
        return {**self.kwargs, **kwargs}

    def _configs(
        self,
        config: RunnableConfig | Sequence[RunnableConfig] | None,
        length: int,
    ) -> list[RunnableConfig]:
        return [self._merge_configs(c) for c in get_config_list(config, length)]

    def _own_invoke(
        self,
        key: Hashable,
        input_: Input,
        config: RunnableConfig,
        call_kwargs: dict[str, Any],
    ) -> Output:
        try:
            output = self.bound.invoke(input_, config, **self._call_kwargs(call_kwargs))
        except BaseException as e:
            self.backend.complete(key, error=_owner_error(e))
            raise
        self.backend.complete(key, result=_CoalescedResult(output))
        return output

    async def _aown_invoke(
        self,
        key: Hashable,
        input_: Input,
        config: RunnableConfig,
        call_kwargs: dict[str, Any],
    ) -> Output:
        try:
            output = await self.bound.ainvoke(
                input_, config, **self._call_kwargs(call_kwargs)
            )
        except BaseException as e:
            await self.backend.acomplete(key, error=_owner_error(e))
            raise
        await self.backend.acomplete(key, result=_CoalescedResult(output))
        return output

    def _join_invoke(
        self,
        input_: Input,
        config: RunnableConfig,
        *,
        key: Hashable,
        call_kwargs: dict[str, Any],
    ) -> Output:
        while True:
            try:
                outcome = self.backend.join(key)
            except _OwnerAbandonedError:
                if self.backend.register(key):
                    return self._own_invoke(key, input_, config, call_kwargs)
            else:
                return cast("Output", outcome.output)

    async def _ajoin_invoke(
        self,
        input_: Input,
        config: RunnableConfig,
        *,
        key: Hashable,
        call_kwargs: dict[str, Any],
    ) -> Output:
        while True:
            try:
                outcome = await self.backend.ajoin(key)
            except _OwnerAbandonedError:
                if await self.backend.aregister(key):
                    return await self._aown_invoke(key, input_, config, call_kwargs)
            else:
                return cast("Output", outcome.output)

    def _join_with_callbacks(
        self,
        key: Hashable,
        input_: Input,
        config: RunnableConfig,
        call_kwargs: dict[str, Any],
    ) -> Output:
        join = partial(self._join_invoke, key=key, call_kwargs=call_kwargs)
        return self._call_with_config(join, input_, config)

    async def _ajoin_with_callbacks(
        self,
        key: Hashable,
        input_: Input,
        config: RunnableConfig,
        call_kwargs: dict[str, Any],
    ) -> Output:
        join = partial(self._ajoin_invoke, key=key, call_kwargs=call_kwargs)
        return await self._acall_with_config(join, input_, config)

    @override
    def invoke(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> Output:
        key = _coalesce_key(input)
        merged = self._merge_configs(config)
        if self.backend.register(key):
            return self._own_invoke(key, input, merged, kwargs)
        return self._join_with_callbacks(key, input, merged, kwargs)

    @override
    async def ainvoke(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> Output:
        key = _coalesce_key(input)
        merged = self._merge_configs(config)
        if await self.backend.aregister(key):
            return await self._aown_invoke(key, input, merged, kwargs)
        return await self._ajoin_with_callbacks(key, input, merged, kwargs)

    def _own_stream(
        self,
        key: Hashable,
        input_: Input,
        config: RunnableConfig,
        call_kwargs: dict[str, Any],
    ) -> Iterator[Output]:
        chunks: list[Output] = []
        try:
            for chunk in self.bound.stream(
                input_, config, **self._call_kwargs(call_kwargs)
            ):
                chunks.append(chunk)
                yield chunk
        except BaseException as e:
            self.backend.complete(key, error=_owner_error(e))
            raise
        self.backend.complete(key, result=_CoalescedResult(chunks=chunks))

    async def _aown_stream(
        self,
        key: Hashable,
        input_: Input,
        config: RunnableConfig,
        call_kwargs: dict[str, Any],
    ) -> AsyncIterator[Output]:
        chunks: list[Output] = []
        try:
            async for chunk in self.bound.astream(
                input_, config, **self._call_kwargs(call_kwargs)
            ):
                chunks.append(chunk)
                yield chunk
        except BaseException as e:
            await self.backend.acomplete(key, error=_owner_error(e))
            raise
        await self.backend.acomplete(key, result=_CoalescedResult(chunks=chunks))

    def _join_stream(
        self,
        inputs: Iterator[Input],
        config: RunnableConfig,
        *,
        key: Hashable,
        call_kwargs: dict[str, Any],
    ) -> Iterator[Output]:
        input_ = next(inputs)
        while True:
            try:
                outcome = self.backend.join(key)
            except _OwnerAbandonedError:
                if self.backend.register(key):
                    yield from self._own_stream(key, input_, config, call_kwargs)
                    return
            else:
                yield from outcome.chunks
                return

    async def _ajoin_stream(
        self,
        inputs: AsyncIterator[Input],
        config: RunnableConfig,
        *,
        key: Hashable,
        call_kwargs: dict[str, Any],
    ) -> AsyncIterator[Output]:
        input_ = await anext(inputs)
        while True:
            try:
                outcome = await self.backend.ajoin(key)
            except _OwnerAbandonedError:
                if await self.backend.aregister(key):
                    async for chunk in self._aown_stream(
                        key, input_, config, call_kwargs
                    ):
                        yield chunk
                    return
            else:
                for chunk in outcome.chunks:
                    yield chunk
                return

    @override
    def stream(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any | None,
    ) -> Iterator[Output]:
        key = _coalesce_key(input)
        merged = self._merge_configs(config)
        if self.backend.register(key):
            yield from self._own_stream(key, input, merged, kwargs)
        else:
            join = partial(self._join_stream, key=key, call_kwargs=kwargs)
            yield from self._transform_stream_with_config(iter([input]), join, merged)

    @override
    async def astream(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any | None,
    ) -> AsyncIterator[Output]:
        key = _coalesce_key(input)
        merged = self._merge_configs(config)
        if await self.backend.aregister(key):
            async for chunk in self._aown_stream(key, input, merged, kwargs):
                yield chunk
        else:
            join = partial(self._ajoin_stream, key=key, call_kwargs=kwargs)
            async for chunk in self._atransform_stream_with_config(
                _aiter_one(input), join, merged
            ):
                yield chunk

    def _own_batch(
        self,
        keys: list[Hashable],
        inputs: list[Input],
        configs: list[RunnableConfig],
        call_kwargs: dict[str, Any],
    ) -> list[Output | Exception]:
        try:
            results = cast(
                "list[Output | Exception]",
                self.bound.batch(
                    inputs,
                    configs,
                    return_exceptions=True,
                    **self._call_kwargs(call_kwargs),
                ),
            )
        except Exception as e:
            results = [e for _ in inputs]
        except BaseException as e:
            for key in keys:
                self.backend.complete(key, error=_owner_error(e))
            raise
        for key, result in zip(keys, results, strict=True):
            if isinstance(result, Exception):
                self.backend.complete(key, error=result)
            else:
                self.backend.complete(key, result=_CoalescedResult(result))
        return results

    async def _aown_batch(
        self,
        keys: list[Hashable],
        inputs: list[Input],
        configs: list[RunnableConfig],
        call_kwargs: dict[str, Any],
    ) -> list[Output | Exception]:
        try:
            results = cast(
                "list[Output | Exception]",
                await self.bound.abatch(
                    inputs,
                    configs,
                    return_exceptions=True,
                    **self._call_kwargs(call_kwargs),
                ),
            )
        except Exception as e:
            results = [e for _ in inputs]
        except BaseException as e:
            for key in keys:
                await self.backend.acomplete(key, error=_owner_error(e))
            raise
        for key, result in zip(keys, results, strict=True):
            if isinstance(result, Exception):
                await self.backend.acomplete(key, error=result)
            else:
                await self.backend.acomplete(key, result=_CoalescedResult(result))
        return results

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
        configs = self._configs(config, len(inputs))
        keys = [_coalesce_key(input_) for input_ in inputs]
        owned = [self.backend.register(key) for key in keys]
        owners = [i for i, is_owner in enumerate(owned) if is_owner]
        outputs: dict[int, Output | Exception] = {}
        # Owners run before any join so that in-batch duplicates never wait on
        # an execution that has not started yet.
        if owners:
            results = self._own_batch(
                [keys[i] for i in owners],
                [inputs[i] for i in owners],
                [configs[i] for i in owners],
                kwargs,
            )
            outputs.update(zip(owners, results, strict=True))
        for i, is_owner in enumerate(owned):
            if not is_owner:
                outputs[i] = _capture(
                    self._join_with_callbacks, keys[i], inputs[i], configs[i], kwargs
                )
        ordered = [outputs[i] for i in range(len(inputs))]
        if not return_exceptions:
            for output in ordered:
                if isinstance(output, Exception):
                    raise output
        return cast("list[Output]", ordered)

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
        configs = self._configs(config, len(inputs))
        keys = [_coalesce_key(input_) for input_ in inputs]
        owned = [await self.backend.aregister(key) for key in keys]
        owners = [i for i, is_owner in enumerate(owned) if is_owner]
        joiners = [i for i, is_owner in enumerate(owned) if not is_owner]
        outputs: dict[int, Output | Exception] = {}
        if owners:
            results = await self._aown_batch(
                [keys[i] for i in owners],
                [inputs[i] for i in owners],
                [configs[i] for i in owners],
                kwargs,
            )
            outputs.update(zip(owners, results, strict=True))
        joined = await asyncio.gather(
            *(
                _acapture(
                    self._ajoin_with_callbacks(keys[i], inputs[i], configs[i], kwargs)
                )
                for i in joiners
            )
        )
        outputs.update(zip(joiners, joined, strict=True))
        ordered = [outputs[i] for i in range(len(inputs))]
        if not return_exceptions:
            for output in ordered:
                if isinstance(output, Exception):
                    raise output
        return cast("list[Output]", ordered)

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
        configs = self._configs(config, len(inputs))
        keys = [_coalesce_key(input_) for input_ in inputs]
        owned = [self.backend.register(key) for key in keys]

        def run_group(group: list[int]) -> list[tuple[int, Output | Exception]]:
            owner = next((i for i in group if owned[i]), None)
            results: list[tuple[int, Output | Exception]] = []
            if owner is not None:
                output = _capture(
                    self._own_invoke, keys[owner], inputs[owner], configs[owner], kwargs
                )
                results.append((owner, output))
            results.extend(
                (
                    i,
                    _capture(
                        self._join_with_callbacks,
                        keys[i],
                        inputs[i],
                        configs[i],
                        kwargs,
                    ),
                )
                for i in group
                if i != owner
            )
            return results

        # Remaining groups are not cancelled on early exit: other callers may be
        # waiting on the executions this batch owns.
        with get_executor_for_config(configs[0]) as executor:
            futures = {
                executor.submit(run_group, group)
                for group in _group_indices(keys, owned)
            }
            while futures:
                done, futures = wait(futures, return_when=FIRST_COMPLETED)
                for future in done:
                    for index, output in future.result():
                        if not return_exceptions and isinstance(output, Exception):
                            raise output
                        yield index, output

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
        configs = self._configs(config, len(inputs))
        keys = [_coalesce_key(input_) for input_ in inputs]
        owned = [await self.backend.aregister(key) for key in keys]
        max_concurrency = configs[0].get("max_concurrency")
        semaphore = asyncio.Semaphore(max_concurrency) if max_concurrency else None

        async def run_group(group: list[int]) -> list[tuple[int, Output | Exception]]:
            owner = next((i for i in group if owned[i]), None)
            results: list[tuple[int, Output | Exception]] = []
            if owner is not None:
                execution = self._aown_invoke(
                    keys[owner], inputs[owner], configs[owner], kwargs
                )
                if semaphore:
                    execution = gated_coro(semaphore, execution)
                results.append((owner, await _acapture(execution)))
            for i in group:
                if i != owner:
                    joined = self._ajoin_with_callbacks(
                        keys[i], inputs[i], configs[i], kwargs
                    )
                    results.append((i, await _acapture(joined)))
            return results

        for group_results in asyncio.as_completed(
            [run_group(group) for group in _group_indices(keys, owned)]
        ):
            for index, output in await group_results:
                if not return_exceptions and isinstance(output, Exception):
                    raise output
                yield index, output
