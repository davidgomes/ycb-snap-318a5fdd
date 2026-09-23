"""Request coalescing for `Runnable` objects.

Coalescing deduplicates concurrent identical requests: while an execution for a
given input is in flight, further calls with the same input wait for that execution
and receive its result instead of running the underlying `Runnable` again.
"""

from __future__ import annotations

import asyncio
import collections
import dataclasses
import hashlib
import threading
from abc import ABC, abstractmethod
from collections.abc import AsyncIterator, Iterator, Mapping, Sequence
from typing import Any, Literal, cast, overload

from pydantic import BaseModel, Field
from typing_extensions import override

from langchain_core.runnables.base import RunnableBindingBase
from langchain_core.runnables.config import (
    RunnableConfig,
    ensure_config,
    get_async_callback_manager_for_config,
    get_callback_manager_for_config,
    get_config_list,
    run_in_executor,
)
from langchain_core.runnables.utils import Input, Output


@dataclasses.dataclass(frozen=True)
class CoalesceStats:
    """Snapshot of the counters kept by a `CoalesceBackend`."""

    active: int = 0
    """Number of keys with an execution currently in flight."""

    coalesced: int = 0
    """Number of calls that joined an in-flight execution instead of running."""

    total: int = 0
    """Total number of calls registered with the backend."""


class CoalesceBackend(ABC):
    """Tracks in-flight executions so identical concurrent requests share one.

    Callers holding a coalescing key follow this protocol:

    1. Call `register(key)`. `True` means the caller owns a new execution and must
        eventually call `complete(key, ...)`, including on failure. `False` means
        an execution for `key` is already in flight.
    2. Callers that got `False` call `join(key)` to wait for that execution and
        receive its result (or have its error raised).

    Once `complete` is called for a key, the next `register` for it starts a fresh
    execution.

    The default async methods delegate to their sync counterparts, running `join`
    in an executor so it does not block the event loop. Backends that can wait
    natively should override them.
    """

    @abstractmethod
    def register(self, key: str) -> bool:
        """Register a call for `key`.

        Args:
            key: The coalescing key of the call.

        Returns:
            `True` if the caller should run the execution, `False` if it should
                join the one already in flight.
        """

    @abstractmethod
    def join(self, key: str) -> Any:
        """Block until the in-flight execution for `key` completes.

        Args:
            key: The coalescing key passed to a `register` call that returned
                `False`.

        Returns:
            The result passed to `complete`.

        Raises:
            BaseException: The error passed to `complete`, if any.
        """

    @abstractmethod
    def complete(
        self,
        key: str,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        """Mark the in-flight execution for `key` as finished and wake its joiners.

        Args:
            key: The coalescing key of the execution.
            result: The result to hand to joiners.
            error: The error to raise in joiners; takes precedence over `result`.
        """

    @abstractmethod
    def is_active(self, key: str) -> bool:
        """Return whether an execution for `key` is currently in flight.

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
        """Drop all in-flight state, cancel waiting joiners, and reset stats.

        Raises:
            NotImplementedError: If the backend does not support clearing.
        """
        msg = f"{type(self).__name__} does not support clear()."
        raise NotImplementedError(msg)

    async def aregister(self, key: str) -> bool:
        """Async version of `register`.

        Args:
            key: The coalescing key of the call.

        Returns:
            `True` if the caller should run the execution, `False` if it should
                join the one already in flight.
        """
        return self.register(key)

    async def ajoin(self, key: str) -> Any:
        """Async version of `join`.

        Args:
            key: The coalescing key passed to a `register` call that returned
                `False`.

        Returns:
            The result passed to `complete`.
        """
        return await run_in_executor(None, self.join, key)

    async def acomplete(
        self,
        key: str,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        """Async version of `complete`.

        Args:
            key: The coalescing key of the execution.
            result: The result to hand to joiners.
            error: The error to raise in joiners; takes precedence over `result`.
        """
        self.complete(key, result=result, error=error)

    async def ais_active(self, key: str) -> bool:
        """Async version of `is_active`.

        Args:
            key: The coalescing key to check.

        Returns:
            `True` if an execution for `key` is in flight.
        """
        return self.is_active(key)


_Waiter = tuple[asyncio.AbstractEventLoop, "asyncio.Future[None]"]


class _InFlight:
    """One execution tracked by `InMemoryCoalesceBackend`."""

    __slots__ = ("done", "error", "event", "pending_joins", "result", "waiters")

    def __init__(self) -> None:
        self.done = False
        self.result: Any = None
        self.error: BaseException | None = None
        self.event = threading.Event()
        # Registered joiners that have not called `join` yet.
        self.pending_joins = 0
        self.waiters: list[_Waiter] = []

    def settle(self, result: Any, error: BaseException | None) -> list[_Waiter]:
        """Record the outcome; must be called with the backend lock held."""
        self.result = result
        self.error = error
        self.done = True
        self.event.set()
        waiters, self.waiters = self.waiters, []
        return waiters

    def outcome(self) -> Any:
        if self.error is not None:
            raise self.error
        return self.result


def _resolve_future(future: asyncio.Future[None]) -> None:
    if not future.done():
        future.set_result(None)


def _cancel_future(future: asyncio.Future[None]) -> None:
    if not future.done():
        future.cancel()


def _notify(waiters: list[_Waiter], *, cancel: bool) -> None:
    callback = _cancel_future if cancel else _resolve_future
    for loop, future in waiters:
        try:
            loop.call_soon_threadsafe(callback, future)
        except RuntimeError:
            # The waiter's event loop is closed; nobody is left to wake.
            continue


class InMemoryCoalesceBackend(CoalesceBackend):
    """Thread-safe, process-local `CoalesceBackend`.

    Sync and async callers may share one instance: sync joiners block on a
    `threading.Event` and async joiners await a future on their own event loop.
    """

    def __init__(self) -> None:
        """Create an empty backend."""
        self._lock = threading.Lock()
        self._active: dict[str, _InFlight] = {}
        # Completed executions whose registered joiners have not joined yet,
        # oldest first.
        self._draining: dict[str, collections.deque[_InFlight]] = {}
        self._coalesced = 0
        self._total = 0

    @override
    def register(self, key: str) -> bool:
        with self._lock:
            self._total += 1
            entry = self._active.get(key)
            if entry is None:
                # Late joiners of older executions will join this one instead,
                # which keeps abandoned registrations from pinning stale results.
                self._draining.pop(key, None)
                self._active[key] = _InFlight()
                return True
            entry.pending_joins += 1
            self._coalesced += 1
            return False

    def _claim(self, key: str) -> _InFlight:
        """Pick the execution a joiner waits on; must hold the lock."""
        queue = self._draining.get(key)
        if queue:
            entry = queue[0]
            entry.pending_joins -= 1
            if entry.pending_joins <= 0:
                queue.popleft()
                if not queue:
                    del self._draining[key]
            return entry
        entry = self._active.get(key)
        if entry is None:
            msg = f"No in-flight execution to join for key {key!r}."
            raise KeyError(msg)
        if entry.pending_joins > 0:
            entry.pending_joins -= 1
        return entry

    @override
    def join(self, key: str) -> Any:
        with self._lock:
            entry = self._claim(key)
        entry.event.wait()
        return entry.outcome()

    @override
    async def ajoin(self, key: str) -> Any:
        future: asyncio.Future[None] | None = None
        with self._lock:
            entry = self._claim(key)
            if not entry.done:
                loop = asyncio.get_running_loop()
                future = loop.create_future()
                entry.waiters.append((loop, future))
        if future is not None:
            await future
        return entry.outcome()

    @override
    def complete(
        self,
        key: str,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        with self._lock:
            entry = self._active.pop(key, None)
            if entry is None:
                return
            if entry.pending_joins > 0:
                self._draining.setdefault(key, collections.deque()).append(entry)
            waiters = entry.settle(result, error)
        _notify(waiters, cancel=False)

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
        """Cancel all in-flight executions' joiners and reset stats.

        Waiting joiners, and registered joiners that have not joined yet, receive
        `asyncio.CancelledError`. Owners of cleared executions may still call
        `complete`; it becomes a no-op.
        """
        waiters: list[_Waiter] = []
        with self._lock:
            for key, entry in self._active.items():
                waiters.extend(entry.settle(None, asyncio.CancelledError()))
                if entry.pending_joins > 0:
                    self._draining.setdefault(key, collections.deque()).append(entry)
            self._active.clear()
            self._coalesced = 0
            self._total = 0
        _notify(waiters, cancel=True)


def _type_name(value: Any) -> str:
    return f"{type(value).__module__}.{type(value).__qualname__}"


def _canonicalize(value: Any, seen: frozenset[int]) -> Any:
    """Build a hashable, order-insensitive representation of `value`."""
    if value is None or isinstance(value, (bool, int, float, complex, str, bytes)):
        return (type(value).__qualname__, value)
    if id(value) in seen:
        return ("<cycle>",)
    seen |= {id(value)}
    if isinstance(value, BaseModel):
        return (_type_name(value), _canonicalize(dict(value), seen))
    if dataclasses.is_dataclass(value) and not isinstance(value, type):
        fields = tuple(
            (field.name, _canonicalize(getattr(value, field.name), seen))
            for field in dataclasses.fields(value)
        )
        return (_type_name(value), fields)
    if isinstance(value, Mapping):
        items = [(_canonicalize(k, seen), _canonicalize(v, seen)) for k, v in value.items()]
        return ("mapping", tuple(sorted(items, key=repr)))
    if isinstance(value, (list, tuple)):
        return (_type_name(value), tuple(_canonicalize(v, seen) for v in value))
    if isinstance(value, (set, frozenset)):
        members = sorted((_canonicalize(v, seen) for v in value), key=repr)
        return (_type_name(value), tuple(members))
    return (_type_name(value), repr(value))


def _coalesce_key(input_: Any) -> str:
    """Derive the coalescing key from an input value alone."""
    canonical = repr(_canonicalize(input_, frozenset()))
    return hashlib.sha256(canonical.encode("utf-8", "surrogatepass")).hexdigest()


@dataclasses.dataclass(frozen=True)
class _StreamResult:
    """Result shared by a streaming leader: every chunk it produced, in order."""

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


def _to_output(result: Any) -> Any:
    if isinstance(result, _StreamResult):
        return _aggregate(result.chunks)
    return result


def _to_chunks(result: Any) -> tuple[Any, ...]:
    if isinstance(result, _StreamResult):
        return result.chunks
    return (result,)


def _abandoned_error() -> RuntimeError:
    return RuntimeError(
        "The coalesced execution was abandoned by its caller before it finished."
    )


def _emit(
    index: int, output: Any, *, return_exceptions: bool
) -> tuple[int, Any | Exception]:
    if not return_exceptions and isinstance(output, Exception):
        raise output
    return index, output


class RunnableCoalesce(RunnableBindingBase[Input, Output]):  # type: ignore[no-redef]
    """Deduplicate concurrent calls to a `Runnable` that share the same input.

    While an execution for an input is in flight, further calls with an equal
    input wait for it and receive its result (or error). Once it completes, the
    next call runs fresh. The key depends only on the input value: config,
    call kwargs, and dictionary key order are ignored.

    `invoke`, `stream`, `batch`, `batch_as_completed`, and their async versions
    all coalesce through the same backend, so they see each other's in-flight
    executions. Joined stream calls replay every chunk from the beginning once
    the leading execution finishes. `transform`, `atransform`, and
    `astream_events` pass straight through to the wrapped `Runnable`.

    Joined calls do not run the wrapped `Runnable`, so they fire their own
    chain start and end callbacks.

    Use it through `Runnable.with_coalesce`.
    """

    backend: CoalesceBackend = Field(default_factory=InMemoryCoalesceBackend)
    """Backend tracking in-flight executions; share it to coalesce across wrappers."""

    @classmethod
    @override
    def is_lc_serializable(cls) -> bool:
        return False

    def coalesce_info(self) -> CoalesceStats:
        """Return the backend's coalescing stats.

        Returns:
            The current `CoalesceStats`.
        """
        return self.backend.stats

    def coalesce_clear(self) -> None:
        """Cancel waiting joiners with `asyncio.CancelledError` and reset stats."""
        self.backend.clear()

    def _item_configs(
        self, config: RunnableConfig | Sequence[RunnableConfig] | None, length: int
    ) -> list[RunnableConfig]:
        return [self._merge_configs(c) for c in get_config_list(config, length)]

    def _join(self, key: str, input_: Input, config: RunnableConfig) -> Any:
        config = ensure_config(config)
        run_manager = get_callback_manager_for_config(config).on_chain_start(
            None,
            input_,
            name=config.get("run_name") or self.get_name(),
            run_id=config.pop("run_id", None),
        )
        try:
            result = self.backend.join(key)
            output = _to_output(result)
        except BaseException as e:
            run_manager.on_chain_error(e)
            raise
        run_manager.on_chain_end(output)
        return result

    async def _ajoin(self, key: str, input_: Input, config: RunnableConfig) -> Any:
        config = ensure_config(config)
        run_manager = await get_async_callback_manager_for_config(
            config
        ).on_chain_start(
            None,
            input_,
            name=config.get("run_name") or self.get_name(),
            run_id=config.pop("run_id", None),
        )
        try:
            result = await self.backend.ajoin(key)
            output = _to_output(result)
        except BaseException as e:
            await run_manager.on_chain_error(e)
            raise
        await run_manager.on_chain_end(output)
        return result

    def _join_item(
        self, key: str, input_: Input, config: RunnableConfig
    ) -> Output | Exception:
        try:
            return cast("Output", _to_output(self._join(key, input_, config)))
        except Exception as e:
            return e

    async def _ajoin_item(
        self, key: str, input_: Input, config: RunnableConfig
    ) -> Output | Exception:
        try:
            return cast("Output", _to_output(await self._ajoin(key, input_, config)))
        except Exception as e:
            return e

    def _settle(self, key: str, output: Any) -> None:
        if isinstance(output, Exception):
            self.backend.complete(key, error=output)
        else:
            self.backend.complete(key, result=output)

    async def _asettle(self, key: str, output: Any) -> None:
        if isinstance(output, Exception):
            await self.backend.acomplete(key, error=output)
        else:
            await self.backend.acomplete(key, result=output)

    @override
    def invoke(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any | None,
    ) -> Output:
        key = _coalesce_key(input)
        merged = self._merge_configs(config)
        if not self.backend.register(key):
            return cast("Output", _to_output(self._join(key, input, merged)))
        try:
            output = self.bound.invoke(input, merged, **{**self.kwargs, **kwargs})
        except BaseException as e:
            self.backend.complete(key, error=e)
            raise
        self.backend.complete(key, result=output)
        return output

    @override
    async def ainvoke(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any | None,
    ) -> Output:
        key = _coalesce_key(input)
        merged = self._merge_configs(config)
        if not await self.backend.aregister(key):
            return cast("Output", _to_output(await self._ajoin(key, input, merged)))
        try:
            output = await self.bound.ainvoke(
                input, merged, **{**self.kwargs, **kwargs}
            )
        except BaseException as e:
            await self.backend.acomplete(key, error=e)
            raise
        await self.backend.acomplete(key, result=output)
        return output

    @override
    def stream(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any | None,
    ) -> Iterator[Output]:
        key = _coalesce_key(input)
        merged = self._merge_configs(config)
        if not self.backend.register(key):
            yield from _to_chunks(self._join(key, input, merged))
            return
        chunks: list[Output] = []
        try:
            for chunk in self.bound.stream(input, merged, **{**self.kwargs, **kwargs}):
                chunks.append(chunk)
                yield chunk
        except GeneratorExit:
            self.backend.complete(key, error=_abandoned_error())
            raise
        except BaseException as e:
            self.backend.complete(key, error=e)
            raise
        self.backend.complete(key, result=_StreamResult(tuple(chunks)))

    @override
    async def astream(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any | None,
    ) -> AsyncIterator[Output]:
        key = _coalesce_key(input)
        merged = self._merge_configs(config)
        if not await self.backend.aregister(key):
            for chunk in _to_chunks(await self._ajoin(key, input, merged)):
                yield chunk
            return
        chunks: list[Output] = []
        try:
            async for chunk in self.bound.astream(
                input, merged, **{**self.kwargs, **kwargs}
            ):
                chunks.append(chunk)
                yield chunk
        except GeneratorExit:
            await self.backend.acomplete(key, error=_abandoned_error())
            raise
        except BaseException as e:
            await self.backend.acomplete(key, error=e)
            raise
        await self.backend.acomplete(key, result=_StreamResult(tuple(chunks)))

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
        configs = self._item_configs(config, len(inputs))
        keys = [_coalesce_key(input_) for input_ in inputs]
        leads = [self.backend.register(key) for key in keys]
        leaders = [i for i, lead in enumerate(leads) if lead]

        results: dict[int, Output | Exception] = {}
        if leaders:
            try:
                outputs = self.bound.batch(
                    [inputs[i] for i in leaders],
                    [configs[i] for i in leaders],
                    return_exceptions=True,
                    **{**self.kwargs, **kwargs},
                )
            except BaseException as e:
                for i in leaders:
                    self.backend.complete(keys[i], error=e)
                raise
            for i, output in zip(leaders, outputs, strict=True):
                self._settle(keys[i], output)
                results[i] = output
        for i, lead in enumerate(leads):
            if not lead:
                results[i] = self._join_item(keys[i], inputs[i], configs[i])

        ordered = [results[i] for i in range(len(inputs))]
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
        configs = self._item_configs(config, len(inputs))
        keys = [_coalesce_key(input_) for input_ in inputs]
        leads = [await self.backend.aregister(key) for key in keys]
        leaders = [i for i, lead in enumerate(leads) if lead]
        results: dict[int, Output | Exception] = {}

        async def run_leaders() -> None:
            if not leaders:
                return
            try:
                outputs = await self.bound.abatch(
                    [inputs[i] for i in leaders],
                    [configs[i] for i in leaders],
                    return_exceptions=True,
                    **{**self.kwargs, **kwargs},
                )
            except BaseException as e:
                for i in leaders:
                    await self.backend.acomplete(keys[i], error=e)
                raise
            for i, output in zip(leaders, outputs, strict=True):
                await self._asettle(keys[i], output)
                results[i] = output

        async def run_joiner(i: int) -> None:
            results[i] = await self._ajoin_item(keys[i], inputs[i], configs[i])

        await asyncio.gather(
            run_leaders(),
            *(run_joiner(i) for i, lead in enumerate(leads) if not lead),
        )

        ordered = [results[i] for i in range(len(inputs))]
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
        configs = self._item_configs(config, len(inputs))
        keys = [_coalesce_key(input_) for input_ in inputs]
        leads = [self.backend.register(key) for key in keys]
        leaders = [i for i, lead in enumerate(leads) if lead]
        led_keys = {keys[i] for i in leaders}
        # Joiners grouped by key, so duplicates are yielded consecutively.
        followers: dict[str, list[int]] = collections.defaultdict(list)
        external: dict[str, list[int]] = {}
        for i, lead in enumerate(leads):
            if lead:
                continue
            if keys[i] in led_keys:
                followers[keys[i]].append(i)
            else:
                external.setdefault(keys[i], []).append(i)

        pending = set(leaders)
        try:
            if leaders:
                for position, output in self.bound.batch_as_completed(
                    [inputs[i] for i in leaders],
                    [configs[i] for i in leaders],
                    return_exceptions=True,
                    **{**self.kwargs, **kwargs},
                ):
                    i = leaders[position]
                    pending.discard(i)
                    self._settle(keys[i], output)
                    yield _emit(i, output, return_exceptions=return_exceptions)
                    for j in followers[keys[i]]:
                        joined = self._join_item(keys[j], inputs[j], configs[j])
                        yield _emit(j, joined, return_exceptions=return_exceptions)
        finally:
            for i in pending:
                self.backend.complete(keys[i], error=_abandoned_error())

        for indices in external.values():
            for j in indices:
                joined = self._join_item(keys[j], inputs[j], configs[j])
                yield _emit(j, joined, return_exceptions=return_exceptions)

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
        configs = self._item_configs(config, len(inputs))
        keys = [_coalesce_key(input_) for input_ in inputs]
        leads = [await self.backend.aregister(key) for key in keys]
        leaders = [i for i, lead in enumerate(leads) if lead]
        led_keys = {keys[i] for i in leaders}
        followers: dict[str, list[int]] = collections.defaultdict(list)
        external: dict[str, list[int]] = {}
        for i, lead in enumerate(leads):
            if lead:
                continue
            if keys[i] in led_keys:
                followers[keys[i]].append(i)
            else:
                external.setdefault(keys[i], []).append(i)

        async def join_group(
            indices: list[int],
        ) -> list[tuple[int, Output | Exception]]:
            outputs = await asyncio.gather(
                *(self._ajoin_item(keys[j], inputs[j], configs[j]) for j in indices)
            )
            return list(zip(indices, outputs, strict=True))

        # External joins wait on other callers, so start them right away.
        external_tasks = [
            asyncio.ensure_future(join_group(indices)) for indices in external.values()
        ]
        pending = set(leaders)
        try:
            if leaders:
                async for position, output in self.bound.abatch_as_completed(
                    [inputs[i] for i in leaders],
                    [configs[i] for i in leaders],
                    return_exceptions=True,
                    **{**self.kwargs, **kwargs},
                ):
                    i = leaders[position]
                    pending.discard(i)
                    await self._asettle(keys[i], output)
                    yield _emit(i, output, return_exceptions=return_exceptions)
                    for j in followers[keys[i]]:
                        joined = await self._ajoin_item(keys[j], inputs[j], configs[j])
                        yield _emit(j, joined, return_exceptions=return_exceptions)
            for next_group in asyncio.as_completed(external_tasks):
                for j, joined in await next_group:
                    yield _emit(j, joined, return_exceptions=return_exceptions)
        finally:
            for i in pending:
                await self.backend.acomplete(keys[i], error=_abandoned_error())
            for task in external_tasks:
                task.cancel()
