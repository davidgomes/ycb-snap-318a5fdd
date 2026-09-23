"""Coalesce concurrent identical `Runnable` calls onto one execution."""

from __future__ import annotations

import asyncio
import threading
from abc import ABC, abstractmethod
from collections.abc import (
    AsyncIterator,
    Hashable,
    Iterator,
    Mapping,
    Sequence,
)
from concurrent.futures import as_completed
from contextlib import suppress
from dataclasses import asdict, dataclass, is_dataclass
from typing import TYPE_CHECKING, Any, Literal, cast, overload

from pydantic import BaseModel
from typing_extensions import override

from langchain_core.runnables.base import Runnable
from langchain_core.runnables.config import (
    RunnableConfig,
    ensure_config,
    get_async_callback_manager_for_config,
    get_callback_manager_for_config,
    get_config_list,
    get_executor_for_config,
)
from langchain_core.runnables.utils import (
    ConfigurableFieldSpec,
    Input,
    Output,
    gather_with_concurrency,
)

if TYPE_CHECKING:
    from langchain_core.callbacks.manager import (
        AsyncCallbackManagerForChainRun,
        CallbackManagerForChainRun,
    )
    from langchain_core.runnables.graph import Graph
    from langchain_core.runnables.schema import StreamEvent
    from langchain_core.tracers.log_stream import RunLog, RunLogPatch

_CallerToken = tuple[str, int]
_StackKey = tuple[_CallerToken, Hashable]


def _type_name(value: object) -> str:
    value_type = type(value)
    return f"{value_type.__module__}.{value_type.__qualname__}"


def _freeze(value: Any) -> Hashable:
    """Build an order-insensitive hashable image of `value`.

    Dictionary key order is ignored. Config and call kwargs are not part of the
    image; callers simply do not pass them in.
    """
    if isinstance(value, BaseModel):
        try:
            dumped = value.model_dump()
        except Exception:
            return ("repr", _type_name(value), repr(value))
        return ("model", _type_name(value), _freeze(dumped))
    if is_dataclass(value) and not isinstance(value, type):
        try:
            dumped = asdict(value)
        except Exception:
            return ("repr", _type_name(value), repr(value))
        return ("dataclass", _type_name(value), _freeze(dumped))
    if isinstance(value, (str, bytes)):
        return ("atom", _type_name(value), value)
    if isinstance(value, bytearray):
        return ("atom", _type_name(value), bytes(value))
    if isinstance(value, Mapping):
        frozen_items = [(_freeze(key), _freeze(item)) for key, item in value.items()]
        frozen_items.sort(key=lambda item: repr(item[0]))
        return ("mapping", tuple(frozen_items))
    if isinstance(value, (set, frozenset)):
        frozen_members = [_freeze(item) for item in value]
        frozen_members.sort(key=repr)
        return (_type_name(value), tuple(frozen_members))
    if isinstance(value, Sequence) and not isinstance(value, (str, bytes, bytearray)):
        return (_type_name(value), tuple(_freeze(item) for item in value))
    try:
        hash(value)
    except TypeError:
        return ("repr", _type_name(value), repr(value))
    return ("atom", _type_name(value), value)


def _coalesce_key(value: Any) -> Hashable:
    """Return the coalescing key for an input value."""
    return _freeze(value)


def _reduce_chunks(chunks: Sequence[Any]) -> Any:
    """Fold stream chunks the way `RunnableGenerator.invoke` does."""
    if not chunks:
        return None
    final: Any = chunks[0]
    for chunk in chunks[1:]:
        try:
            final = final + chunk
        except TypeError:
            final = chunk
    return final


def _extend_output(current: Any, chunk: Any, *, started: bool) -> tuple[Any, bool]:
    if not started:
        return chunk, True
    try:
        return current + chunk, True
    except TypeError:
        return chunk, True


@dataclass(slots=True)
class _InvokeResult:
    """Single value produced by an `invoke` leader."""

    value: Any


@dataclass(slots=True)
class _StreamResult:
    """Chunks produced by a `stream` leader.

    `error` is raised by joiners after the chunks have been replayed.
    """

    chunks: list[Any]
    error: BaseException | None = None


def _unwrap_invoke(result: Any) -> Any:
    """Adapt a shared payload to a single `invoke` return value."""
    if isinstance(result, _InvokeResult):
        return result.value
    if isinstance(result, _StreamResult):
        if result.error is not None:
            raise result.error
        return _reduce_chunks(result.chunks)
    return result


@dataclass(frozen=True, slots=True)
class CoalesceStats:
    """Snapshot of request-coalescing counters.

    Attributes:
        active: Keys with an execution still in flight.
        coalesced: Calls that joined an in-flight execution.
        total: Calls observed since the last reset.
    """

    active: int
    coalesced: int
    total: int


class CoalesceBackend(ABC):
    """In-flight state for coalesced `Runnable` calls.

    `register` returns `True` when the caller owns the execution and `False`
    when the caller must `join` it. `complete` publishes the owner's outcome.
    A later `register` for the same key starts a fresh execution once the
    previous one has completed.

    Async methods mirror the sync API so sync and async callers can share one
    backend.
    """

    @abstractmethod
    def register(self, key: Hashable) -> bool:
        """Join an in-flight execution or become its owner.

        Args:
            key: Opaque coalescing key. Equal keys share one execution.

        Returns:
            `True` when this caller must run the execution and then `complete`.
            `False` when this caller must `join` instead.
        """

    @abstractmethod
    def join(self, key: Hashable) -> Any:
        """Wait for the execution this caller registered against.

        Args:
            key: Key passed to the matching `register` call.

        Returns:
            The value passed to `complete` as `result`.

        Raises:
            BaseException: The error passed to `complete`, or
                `asyncio.CancelledError` when the execution was cleared.
        """

    @abstractmethod
    def complete(
        self,
        key: Hashable,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        """Publish the outcome of the caller's owned execution.

        Args:
            key: Key passed to the matching `register` call.
            result: Successful output. Ignored when `error` is set.
            error: Failure to raise from `join`.
        """

    @abstractmethod
    def is_active(self, key: Hashable) -> bool:
        """Return whether `key` has an execution in flight.

        Args:
            key: Coalescing key.

        Returns:
            `True` when an owner has registered and has not yet completed.
        """

    @property
    @abstractmethod
    def stats(self) -> CoalesceStats:
        """Return a snapshot of `active`, `coalesced`, and `total` counters."""

    @abstractmethod
    async def aregister(self, key: Hashable) -> bool:
        """Async version of `register`.

        Args:
            key: Opaque coalescing key.

        Returns:
            `True` when this caller owns the execution.
        """

    @abstractmethod
    async def ajoin(self, key: Hashable) -> Any:
        """Async version of `join`.

        Args:
            key: Key passed to the matching `aregister` call.

        Returns:
            The value passed to `acomplete` as `result`.

        Raises:
            BaseException: The error passed to `acomplete`, or
                `asyncio.CancelledError` when the execution was cleared.
        """

    @abstractmethod
    async def acomplete(
        self,
        key: Hashable,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        """Async version of `complete`.

        Args:
            key: Key passed to the matching `aregister` call.
            result: Successful output. Ignored when `error` is set.
            error: Failure to raise from `ajoin`.
        """

    @abstractmethod
    async def ais_active(self, key: Hashable) -> bool:
        """Async version of `is_active`.

        Args:
            key: Coalescing key.

        Returns:
            `True` when an owner has registered and has not yet completed.
        """

    def clear(self) -> None:
        """Cancel waiters and reset counters.

        Raises:
            NotImplementedError: When this backend cannot drop in-flight state.
        """
        msg = f"{type(self).__name__} does not implement clear()"
        raise NotImplementedError(msg)


class _Flight:
    """One in-flight or recently finished execution."""

    def __init__(self) -> None:
        self.done = False
        self.cancelled = False
        self.result: Any = None
        self.error: BaseException | None = None
        self.sync_waiters: list[threading.Event] = []
        self.async_waiters: list[
            tuple[asyncio.AbstractEventLoop, asyncio.Future[Any]]
        ] = []


def _caller_token() -> _CallerToken:
    """Identify the task or thread that must pair `register` with `join`."""
    try:
        task = asyncio.current_task()
    except RuntimeError:
        task = None
    if task is not None:
        return ("task", id(task))
    return ("thread", threading.get_ident())


def _resolve_future(fut: asyncio.Future[Any], flight: _Flight) -> None:
    if fut.done():
        return
    if flight.cancelled:
        fut.cancel()
        return
    if flight.error is not None:
        fut.set_exception(flight.error)
        return
    fut.set_result(flight.result)


class InMemoryCoalesceBackend(CoalesceBackend):
    """Process-local coalescing backend.

    The lock guards the maps; waiters block on a `threading.Event` or an
    `asyncio.Future` so sync and async callers can share one flight.
    """

    def __init__(self) -> None:
        """Create an empty, thread-safe backend."""
        self._lock = threading.Lock()
        self._flights: dict[Hashable, _Flight] = {}
        self._leader_stacks: dict[_StackKey, list[_Flight]] = {}
        self._joiner_stacks: dict[_StackKey, list[_Flight]] = {}
        self._cancelled_joiners: dict[_StackKey, int] = {}
        self._active = 0
        self._coalesced = 0
        self._total = 0

    def _stack_key(self, key: Hashable) -> _StackKey:
        return (_caller_token(), key)

    def _push(
        self,
        stacks: dict[_StackKey, list[_Flight]],
        key: Hashable,
        flight: _Flight,
    ) -> None:
        stacks.setdefault(self._stack_key(key), []).append(flight)

    def _pop(
        self,
        stacks: dict[_StackKey, list[_Flight]],
        key: Hashable,
    ) -> _Flight | None:
        stack_key = self._stack_key(key)
        stack = stacks.get(stack_key)
        if not stack:
            return None
        flight = stack.pop(0)
        if not stack:
            stacks.pop(stack_key, None)
        return flight

    def _outcome(self, flight: _Flight) -> Any:
        if flight.cancelled:
            raise asyncio.CancelledError
        if flight.error is not None:
            raise flight.error
        return flight.result

    def _wake_locked(self, flight: _Flight) -> None:
        sync_waiters = flight.sync_waiters
        async_waiters = flight.async_waiters
        flight.sync_waiters = []
        flight.async_waiters = []
        for event in sync_waiters:
            event.set()
        for loop, fut in async_waiters:
            loop.call_soon_threadsafe(_resolve_future, fut, flight)

    @override
    def register(self, key: Hashable) -> bool:
        with self._lock:
            self._total += 1
            current = self._flights.get(key)
            if current is not None and not current.done and not current.cancelled:
                self._coalesced += 1
                self._push(self._joiner_stacks, key, current)
                return False
            flight = _Flight()
            self._flights[key] = flight
            self._active += 1
            self._push(self._leader_stacks, key, flight)
            return True

    @override
    def join(self, key: Hashable) -> Any:
        with self._lock:
            flight = self._pop(self._joiner_stacks, key)
            if flight is None:
                stack_key = self._stack_key(key)
                cancelled = self._cancelled_joiners.get(stack_key, 0)
                if cancelled:
                    if cancelled == 1:
                        self._cancelled_joiners.pop(stack_key, None)
                    else:
                        self._cancelled_joiners[stack_key] = cancelled - 1
                    raise asyncio.CancelledError
                msg = "join() called without a matching register()"
                raise RuntimeError(msg)
            if flight.done:
                return self._outcome(flight)
            event = threading.Event()
            flight.sync_waiters.append(event)
        event.wait()
        with self._lock:
            return self._outcome(flight)

    @override
    def complete(
        self,
        key: Hashable,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        with self._lock:
            flight = self._pop(self._leader_stacks, key)
            if flight is None or flight.done:
                return
            flight.result = result
            flight.error = error
            flight.done = True
            if self._flights.get(key) is flight:
                del self._flights[key]
                self._active -= 1
            self._wake_locked(flight)

    @override
    def is_active(self, key: Hashable) -> bool:
        with self._lock:
            flight = self._flights.get(key)
            return flight is not None and not flight.done and not flight.cancelled

    def caller_is_leader(self, key: Hashable) -> bool:
        """Return whether this caller already owns `key`.

        Nested calls from the owner run directly so they do not wait on the
        execution they themselves are leading.

        Args:
            key: Coalescing key.

        Returns:
            `True` when this caller is the owner of the in-flight execution.
        """
        with self._lock:
            return bool(self._leader_stacks.get(self._stack_key(key)))

    @property
    @override
    def stats(self) -> CoalesceStats:
        with self._lock:
            return CoalesceStats(
                active=self._active,
                coalesced=self._coalesced,
                total=self._total,
            )

    @override
    async def aregister(self, key: Hashable) -> bool:
        return self.register(key)

    @override
    async def ajoin(self, key: Hashable) -> Any:
        loop = asyncio.get_running_loop()
        with self._lock:
            flight = self._pop(self._joiner_stacks, key)
            if flight is None:
                stack_key = self._stack_key(key)
                cancelled = self._cancelled_joiners.get(stack_key, 0)
                if cancelled:
                    if cancelled == 1:
                        self._cancelled_joiners.pop(stack_key, None)
                    else:
                        self._cancelled_joiners[stack_key] = cancelled - 1
                    raise asyncio.CancelledError
                msg = "ajoin() called without a matching aregister()"
                raise RuntimeError(msg)
            if flight.done:
                return self._outcome(flight)
            fut: asyncio.Future[Any] = loop.create_future()
            flight.async_waiters.append((loop, fut))
        return await fut

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

    @override
    def clear(self) -> None:
        """Cancel every waiter with `asyncio.CancelledError` and reset stats."""
        with self._lock:
            flights = list(self._flights.values())
            seen = {id(flight) for flight in flights}
            for stacks in (self._leader_stacks, self._joiner_stacks):
                for stack in stacks.values():
                    for flight in stack:
                        if id(flight) not in seen:
                            seen.add(id(flight))
                            flights.append(flight)
            for stack_key, stack in self._joiner_stacks.items():
                if stack:
                    self._cancelled_joiners[stack_key] = self._cancelled_joiners.get(
                        stack_key, 0
                    ) + len(stack)
            self._leader_stacks.clear()
            self._joiner_stacks.clear()
            self._flights.clear()
            self._active = 0
            self._coalesced = 0
            self._total = 0
            for flight in flights:
                flight.cancelled = True
                if not flight.done:
                    flight.done = True
                    flight.error = asyncio.CancelledError()
                    self._wake_locked(flight)


class RunnableCoalesce(Runnable[Input, Output]):
    """`Runnable` that shares one in-flight execution per input value.

    Use `Runnable.with_coalesce` rather than constructing this class directly.
    """

    def __init__(
        self,
        bound: Runnable[Input, Output],
        *,
        backend: CoalesceBackend | None = None,
    ) -> None:
        """Wrap `bound` so identical concurrent calls share one execution.

        Args:
            bound: `Runnable` that performs the real work.
            backend: In-flight store.

                When omitted, a new `InMemoryCoalesceBackend` is created.
        """
        self.bound = bound
        self.backend = backend if backend is not None else InMemoryCoalesceBackend()
        self.name = bound.name if hasattr(bound, "name") else None

    def coalesce_info(self) -> CoalesceStats:
        """Return the backend's `active`, `coalesced`, and `total` counters.

        Returns:
            A snapshot taken under the backend lock.
        """
        return self.backend.stats

    def coalesce_clear(self) -> None:
        """Cancel waiters with `asyncio.CancelledError` and reset counters."""
        self.backend.clear()

    def _caller_is_leader(self, key: Hashable) -> bool:
        checker = getattr(self.backend, "caller_is_leader", None)
        if not callable(checker):
            return False
        return bool(checker(key))

    @override
    def get_name(self, suffix: str | None = None, *, name: str | None = None) -> str:
        return self.bound.get_name(suffix, name=name)

    @property
    @override
    def InputType(self) -> type[Input]:
        return self.bound.InputType

    @property
    @override
    def OutputType(self) -> type[Output]:
        return self.bound.OutputType

    @override
    def get_input_schema(self, config: RunnableConfig | None = None) -> type[BaseModel]:
        return self.bound.get_input_schema(config)

    @override
    def get_output_schema(
        self, config: RunnableConfig | None = None
    ) -> type[BaseModel]:
        return self.bound.get_output_schema(config)

    @property
    @override
    def config_specs(self) -> list[ConfigurableFieldSpec]:
        return self.bound.config_specs

    @override
    def get_graph(self, config: RunnableConfig | None = None) -> Graph:
        return self.bound.get_graph(config)

    @override
    def __repr__(self) -> str:
        return f"RunnableCoalesce(bound={self.bound!r})"

    def _chain_start_sync(
        self,
        input_: Input,
        config: RunnableConfig | None,
    ) -> CallbackManagerForChainRun:
        ensured = ensure_config(config)
        callback_manager = get_callback_manager_for_config(ensured)
        return callback_manager.on_chain_start(
            None,
            input_,
            name=ensured.get("run_name") or self.get_name(),
            run_id=ensured.pop("run_id", None),
        )

    async def _chain_start_async(
        self,
        input_: Input,
        config: RunnableConfig | None,
    ) -> AsyncCallbackManagerForChainRun:
        ensured = ensure_config(config)
        callback_manager = get_async_callback_manager_for_config(ensured)
        return await callback_manager.on_chain_start(
            None,
            input_,
            name=ensured.get("run_name") or self.get_name(),
            run_id=ensured.pop("run_id", None),
        )

    def _follow_invoke(
        self,
        key: Hashable,
        input_: Input,
        config: RunnableConfig | None,
    ) -> Output:
        run_manager: CallbackManagerForChainRun | None = None
        try:
            run_manager = self._chain_start_sync(input_, config)
            value = _unwrap_invoke(self.backend.join(key))
        except BaseException as exc:
            if run_manager is None:
                with suppress(BaseException):
                    self.backend.join(key)
            else:
                run_manager.on_chain_error(exc)
            raise
        run_manager.on_chain_end(value)
        return cast("Output", value)

    async def _afollow_invoke(
        self,
        key: Hashable,
        input_: Input,
        config: RunnableConfig | None,
    ) -> Output:
        run_manager: AsyncCallbackManagerForChainRun | None = None
        try:
            run_manager = await self._chain_start_async(input_, config)
            value = _unwrap_invoke(await self.backend.ajoin(key))
        except BaseException as exc:
            if run_manager is None:
                with suppress(BaseException):
                    await self.backend.ajoin(key)
            else:
                await run_manager.on_chain_error(exc)
            raise
        await run_manager.on_chain_end(value)
        return cast("Output", value)

    @override
    def invoke(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> Output:
        key = _coalesce_key(input)
        if self._caller_is_leader(key):
            return self.bound.invoke(input, config, **kwargs)
        if not self.backend.register(key):
            return self._follow_invoke(key, input, config)
        try:
            value = self.bound.invoke(input, config, **kwargs)
        except BaseException as exc:
            self.backend.complete(key, error=exc)
            raise
        self.backend.complete(key, result=_InvokeResult(value))
        return value

    @override
    async def ainvoke(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> Output:
        key = _coalesce_key(input)
        if self._caller_is_leader(key):
            return await self.bound.ainvoke(input, config, **kwargs)
        if not await self.backend.aregister(key):
            return await self._afollow_invoke(key, input, config)
        try:
            value = await self.bound.ainvoke(input, config, **kwargs)
        except BaseException as exc:
            await self.backend.acomplete(key, error=exc)
            raise
        await self.backend.acomplete(key, result=_InvokeResult(value))
        return value

    def _follow_stream(
        self,
        key: Hashable,
        input_: Input,
        config: RunnableConfig | None,
    ) -> Iterator[Output]:
        try:
            run_manager = self._chain_start_sync(input_, config)
        except BaseException:
            with suppress(BaseException):
                self.backend.join(key)
            raise
        final: Any = None
        started = False
        pending_error: BaseException | None = None
        try:
            result = self.backend.join(key)
            if isinstance(result, _StreamResult):
                for chunk in result.chunks:
                    yield cast("Output", chunk)
                    final, started = _extend_output(final, chunk, started=started)
                pending_error = result.error
            else:
                value = _unwrap_invoke(result)
                final = value
                yield cast("Output", value)
        except GeneratorExit:
            run_manager.on_chain_end(final)
            raise
        except BaseException as exc:
            run_manager.on_chain_error(exc)
            raise
        if pending_error is not None:
            run_manager.on_chain_error(pending_error)
            raise pending_error
        run_manager.on_chain_end(final)

    async def _afollow_stream(
        self,
        key: Hashable,
        input_: Input,
        config: RunnableConfig | None,
    ) -> AsyncIterator[Output]:
        try:
            run_manager = await self._chain_start_async(input_, config)
        except BaseException:
            with suppress(BaseException):
                await self.backend.ajoin(key)
            raise
        final: Any = None
        started = False
        pending_error: BaseException | None = None
        try:
            result = await self.backend.ajoin(key)
            if isinstance(result, _StreamResult):
                for chunk in result.chunks:
                    yield cast("Output", chunk)
                    final, started = _extend_output(final, chunk, started=started)
                pending_error = result.error
            else:
                value = _unwrap_invoke(result)
                final = value
                yield cast("Output", value)
        except GeneratorExit:
            await run_manager.on_chain_end(final)
            raise
        except BaseException as exc:
            await run_manager.on_chain_error(exc)
            raise
        if pending_error is not None:
            await run_manager.on_chain_error(pending_error)
            raise pending_error
        await run_manager.on_chain_end(final)

    @override
    def stream(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> Iterator[Output]:
        key = _coalesce_key(input)
        if self._caller_is_leader(key):
            yield from self.bound.stream(input, config, **kwargs)
            return
        if not self.backend.register(key):
            yield from self._follow_stream(key, input, config)
            return
        chunks: list[Any] = []
        try:
            for chunk in self.bound.stream(input, config, **kwargs):
                chunks.append(chunk)
                yield chunk
        except GeneratorExit:
            self.backend.complete(key, result=_StreamResult(chunks))
            raise
        except BaseException as exc:
            self.backend.complete(key, result=_StreamResult(chunks, error=exc))
            raise
        self.backend.complete(key, result=_StreamResult(chunks))

    @override
    async def astream(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> AsyncIterator[Output]:
        key = _coalesce_key(input)
        if self._caller_is_leader(key):
            async for chunk in self.bound.astream(input, config, **kwargs):
                yield chunk
            return
        if not await self.backend.aregister(key):
            async for chunk in self._afollow_stream(key, input, config):
                yield chunk
            return
        chunks: list[Any] = []
        try:
            async for chunk in self.bound.astream(input, config, **kwargs):
                chunks.append(chunk)
                yield chunk
        except GeneratorExit:
            self.backend.complete(key, result=_StreamResult(chunks))
            raise
        except BaseException as exc:
            await self.backend.acomplete(key, result=_StreamResult(chunks, error=exc))
            raise
        await self.backend.acomplete(key, result=_StreamResult(chunks))

    def _finish_sync(
        self,
        key: Hashable,
        leader_flags: Sequence[bool],
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        for is_leader in leader_flags:
            if not is_leader:
                continue
            if error is not None:
                self.backend.complete(key, error=error)
            else:
                self.backend.complete(key, result=result)

    def _drain_sync_joiners(self, key: Hashable, leader_flags: Sequence[bool]) -> None:
        for is_leader in leader_flags:
            if is_leader:
                continue
            with suppress(BaseException):
                self.backend.join(key)

    def _close_sync_joiners(
        self,
        managers: Sequence[CallbackManagerForChainRun],
        *,
        value: Any = None,
        error: BaseException | None = None,
    ) -> None:
        for run_manager in managers:
            if error is not None:
                run_manager.on_chain_error(error)
            else:
                run_manager.on_chain_end(value)

    def _run_sync_group(
        self,
        inputs: Sequence[Input],
        configs: Sequence[RunnableConfig],
        indices: Sequence[int],
        kwargs: dict[str, Any],
    ) -> Output:
        if len(indices) == 1:
            index = indices[0]
            return self.invoke(inputs[index], configs[index], **kwargs)

        key = _coalesce_key(inputs[indices[0]])
        if self._caller_is_leader(key):
            return self.bound.invoke(inputs[indices[0]], configs[indices[0]], **kwargs)
        leader_flags = [self.backend.register(key) for _ in indices]
        managers: list[CallbackManagerForChainRun] = []
        settled = False
        try:
            for is_leader, index in zip(leader_flags, indices, strict=True):
                if not is_leader:
                    managers.append(
                        self._chain_start_sync(inputs[index], configs[index])
                    )
            if any(leader_flags):
                leader_index = indices[leader_flags.index(True)]
                try:
                    value = self.bound.invoke(
                        inputs[leader_index],
                        configs[leader_index],
                        **kwargs,
                    )
                except BaseException as exc:
                    self._finish_sync(key, leader_flags, error=exc)
                    self._drain_sync_joiners(key, leader_flags)
                    self._close_sync_joiners(managers, error=exc)
                    settled = True
                    raise
                payload = _InvokeResult(value)
                self._finish_sync(key, leader_flags, result=payload)
                self._drain_sync_joiners(key, leader_flags)
                self._close_sync_joiners(managers, value=value)
                settled = True
                return value

            first_error: BaseException | None = None
            joined: Any = None
            for _ in indices:
                try:
                    joined = _unwrap_invoke(self.backend.join(key))
                except BaseException as exc:
                    if first_error is None:
                        first_error = exc
            if first_error is not None:
                self._close_sync_joiners(managers, error=first_error)
                settled = True
                raise first_error
            self._close_sync_joiners(managers, value=joined)
            settled = True
            return cast("Output", joined)
        finally:
            if not settled:
                with suppress(BaseException):
                    self._finish_sync(
                        key,
                        leader_flags,
                        error=RuntimeError("coalesced batch group failed"),
                    )
                with suppress(BaseException):
                    self._drain_sync_joiners(key, leader_flags)

    async def _finish_async(
        self,
        key: Hashable,
        leader_flags: Sequence[bool],
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        for is_leader in leader_flags:
            if not is_leader:
                continue
            if error is not None:
                await self.backend.acomplete(key, error=error)
            else:
                await self.backend.acomplete(key, result=result)

    async def _drain_async_joiners(
        self,
        key: Hashable,
        leader_flags: Sequence[bool],
    ) -> None:
        for is_leader in leader_flags:
            if is_leader:
                continue
            with suppress(BaseException):
                await self.backend.ajoin(key)

    async def _close_async_joiners(
        self,
        managers: Sequence[AsyncCallbackManagerForChainRun],
        *,
        value: Any = None,
        error: BaseException | None = None,
    ) -> None:
        for run_manager in managers:
            if error is not None:
                await run_manager.on_chain_error(error)
            else:
                await run_manager.on_chain_end(value)

    async def _run_async_group(
        self,
        inputs: Sequence[Input],
        configs: Sequence[RunnableConfig],
        indices: Sequence[int],
        kwargs: dict[str, Any],
    ) -> Output:
        if len(indices) == 1:
            index = indices[0]
            return await self.ainvoke(inputs[index], configs[index], **kwargs)

        key = _coalesce_key(inputs[indices[0]])
        if self._caller_is_leader(key):
            return await self.bound.ainvoke(
                inputs[indices[0]], configs[indices[0]], **kwargs
            )
        leader_flags = [await self.backend.aregister(key) for _ in indices]
        managers: list[AsyncCallbackManagerForChainRun] = []
        settled = False
        try:
            for is_leader, index in zip(leader_flags, indices, strict=True):
                if not is_leader:
                    managers.append(
                        await self._chain_start_async(inputs[index], configs[index])
                    )
            if any(leader_flags):
                leader_index = indices[leader_flags.index(True)]
                try:
                    value = await self.bound.ainvoke(
                        inputs[leader_index],
                        configs[leader_index],
                        **kwargs,
                    )
                except BaseException as exc:
                    await self._finish_async(key, leader_flags, error=exc)
                    await self._drain_async_joiners(key, leader_flags)
                    await self._close_async_joiners(managers, error=exc)
                    settled = True
                    raise
                payload = _InvokeResult(value)
                await self._finish_async(key, leader_flags, result=payload)
                await self._drain_async_joiners(key, leader_flags)
                await self._close_async_joiners(managers, value=value)
                settled = True
                return value

            first_error: BaseException | None = None
            joined: Any = None
            for _ in indices:
                try:
                    joined = _unwrap_invoke(await self.backend.ajoin(key))
                except BaseException as exc:
                    if first_error is None:
                        first_error = exc
            if first_error is not None:
                await self._close_async_joiners(managers, error=first_error)
                settled = True
                raise first_error
            await self._close_async_joiners(managers, value=joined)
            settled = True
            return cast("Output", joined)
        finally:
            if not settled:
                with suppress(BaseException):
                    await self._finish_async(
                        key,
                        leader_flags,
                        error=RuntimeError("coalesced batch group failed"),
                    )
                with suppress(BaseException):
                    await self._drain_async_joiners(key, leader_flags)

    def _group_indices(self, inputs: Sequence[Input]) -> dict[Hashable, list[int]]:
        groups: dict[Hashable, list[int]] = {}
        for index, value in enumerate(inputs):
            groups.setdefault(_coalesce_key(value), []).append(index)
        return groups

    def _scatter(
        self,
        results: list[Any],
        indices: Sequence[int],
        value: Any,
        *,
        return_exceptions: bool,
    ) -> None:
        if isinstance(value, Exception) and not return_exceptions:
            raise value
        for index in indices:
            results[index] = value

    @override
    def batch(
        self,
        inputs: list[Input],
        config: RunnableConfig | list[RunnableConfig] | None = None,
        *,
        return_exceptions: bool = False,
        **kwargs: Any,
    ) -> list[Output]:
        if not inputs:
            return []
        configs = get_config_list(config, len(inputs))
        groups = list(self._group_indices(inputs).values())
        results: list[Any] = [None] * len(inputs)

        def run_group(indices: list[int]) -> tuple[list[int], Any]:
            try:
                return indices, self._run_sync_group(inputs, configs, indices, kwargs)
            except Exception as exc:
                return indices, exc

        if len(groups) == 1:
            indices, value = run_group(groups[0])
            self._scatter(results, indices, value, return_exceptions=return_exceptions)
            return cast("list[Output]", results)

        with get_executor_for_config(configs[0]) as executor:
            futures = [executor.submit(run_group, indices) for indices in groups]
            for future in futures:
                indices, value = future.result()
                self._scatter(
                    results,
                    indices,
                    value,
                    return_exceptions=return_exceptions,
                )
        return cast("list[Output]", results)

    @override
    async def abatch(
        self,
        inputs: list[Input],
        config: RunnableConfig | list[RunnableConfig] | None = None,
        *,
        return_exceptions: bool = False,
        **kwargs: Any,
    ) -> list[Output]:
        if not inputs:
            return []
        configs = get_config_list(config, len(inputs))
        groups = list(self._group_indices(inputs).values())
        results: list[Any] = [None] * len(inputs)

        async def run_group(indices: list[int]) -> tuple[list[int], Any]:
            try:
                value = await self._run_async_group(inputs, configs, indices, kwargs)
            except Exception as exc:
                return indices, exc
            return indices, value

        max_concurrency = configs[0].get("max_concurrency")
        grouped = await gather_with_concurrency(
            max_concurrency,
            *(run_group(indices) for indices in groups),
        )
        for indices, value in grouped:
            self._scatter(results, indices, value, return_exceptions=return_exceptions)
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
        configs = get_config_list(config, len(inputs))
        groups = list(self._group_indices(inputs).values())

        def run_group(indices: list[int]) -> tuple[list[int], Any]:
            try:
                return indices, self._run_sync_group(inputs, configs, indices, kwargs)
            except Exception as exc:
                return indices, exc

        def emit(indices: Sequence[int], value: Any) -> Iterator[tuple[int, Any]]:
            if isinstance(value, Exception) and not return_exceptions:
                raise value
            for index in indices:
                yield index, value

        if len(groups) == 1:
            indices, value = run_group(groups[0])
            yield from emit(indices, value)
            return

        with get_executor_for_config(configs[0]) as executor:
            futures = [executor.submit(run_group, indices) for indices in groups]
            for future in as_completed(futures):
                indices, value = future.result()
                yield from emit(indices, value)

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
        configs = get_config_list(config, len(inputs))
        groups = list(self._group_indices(inputs).values())
        max_concurrency = configs[0].get("max_concurrency")
        semaphore = asyncio.Semaphore(max_concurrency) if max_concurrency else None

        async def run_group(indices: list[int]) -> tuple[list[int], Any]:
            try:
                if semaphore is not None:
                    async with semaphore:
                        value = await self._run_async_group(
                            inputs, configs, indices, kwargs
                        )
                else:
                    value = await self._run_async_group(
                        inputs, configs, indices, kwargs
                    )
            except Exception as exc:
                return indices, exc
            return indices, value

        async def emit(
            indices: Sequence[int], value: Any
        ) -> AsyncIterator[tuple[int, Any]]:
            if isinstance(value, Exception) and not return_exceptions:
                raise value
            for index in indices:
                yield index, value

        tasks = [asyncio.create_task(run_group(indices)) for indices in groups]
        try:
            for task in asyncio.as_completed(tasks):
                indices, value = await task
                async for item in emit(indices, value):
                    yield item
        finally:
            for task in tasks:
                if not task.done():
                    task.cancel()

    @override
    def transform(
        self,
        input: Iterator[Input],
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> Iterator[Output]:
        yield from self.bound.transform(input, config, **kwargs)

    @override
    async def atransform(
        self,
        input: AsyncIterator[Input],
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> AsyncIterator[Output]:
        async for chunk in self.bound.atransform(input, config, **kwargs):
            yield chunk

    @override
    async def astream_events(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> AsyncIterator[StreamEvent]:
        async for event in self.bound.astream_events(input, config, **kwargs):
            yield event

    @overload
    def astream_log(
        self,
        input: Any,
        config: RunnableConfig | None = None,
        *,
        diff: Literal[True] = True,
        with_streamed_output_list: bool = True,
        include_names: Sequence[str] | None = None,
        include_types: Sequence[str] | None = None,
        include_tags: Sequence[str] | None = None,
        exclude_names: Sequence[str] | None = None,
        exclude_types: Sequence[str] | None = None,
        exclude_tags: Sequence[str] | None = None,
        **kwargs: Any,
    ) -> AsyncIterator[RunLogPatch]: ...

    @overload
    def astream_log(
        self,
        input: Any,
        config: RunnableConfig | None = None,
        *,
        diff: Literal[False],
        with_streamed_output_list: bool = True,
        include_names: Sequence[str] | None = None,
        include_types: Sequence[str] | None = None,
        include_tags: Sequence[str] | None = None,
        exclude_names: Sequence[str] | None = None,
        exclude_types: Sequence[str] | None = None,
        exclude_tags: Sequence[str] | None = None,
        **kwargs: Any,
    ) -> AsyncIterator[RunLog]: ...

    @override
    async def astream_log(
        self,
        input: Any,
        config: RunnableConfig | None = None,
        *,
        diff: bool = True,
        with_streamed_output_list: bool = True,
        include_names: Sequence[str] | None = None,
        include_types: Sequence[str] | None = None,
        include_tags: Sequence[str] | None = None,
        exclude_names: Sequence[str] | None = None,
        exclude_types: Sequence[str] | None = None,
        exclude_tags: Sequence[str] | None = None,
        **kwargs: Any,
    ) -> AsyncIterator[RunLogPatch] | AsyncIterator[RunLog]:
        # Overloads on `diff` are not visible through the implementation signature.
        async for item in self.bound.astream_log(  # type: ignore[call-overload,misc]
            input,
            config,
            diff=diff,
            with_streamed_output_list=with_streamed_output_list,
            include_names=include_names,
            include_types=include_types,
            include_tags=include_tags,
            exclude_names=exclude_names,
            exclude_types=exclude_types,
            exclude_tags=exclude_tags,
            **kwargs,
        ):
            yield item


__all__ = [
    "CoalesceBackend",
    "CoalesceStats",
    "InMemoryCoalesceBackend",
]
