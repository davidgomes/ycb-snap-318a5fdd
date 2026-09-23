"""Coalesce identical in-flight `Runnable` calls onto one execution.

Concurrent `invoke`, `stream`, and per-item `batch` calls that carry the same
input share a single execution. Configuration, extra keyword arguments, and
dictionary key order are not part of the coalescing identity. Once that
execution finishes, the next call with the same input runs again.
"""

from __future__ import annotations

import asyncio
import math
import threading
from abc import ABC, abstractmethod
from collections.abc import AsyncIterator, Hashable, Iterator, Mapping, Sequence
from concurrent.futures import FIRST_COMPLETED, wait
from contextvars import ContextVar
from dataclasses import fields, is_dataclass
from typing import TYPE_CHECKING, Any, Literal, cast, overload

from pydantic import BaseModel
from typing_extensions import NamedTuple, override

from langchain_core.runnables.base import Runnable
from langchain_core.runnables.config import (
    RunnableConfig,
    get_config_list,
    get_executor_for_config,
)
from langchain_core.runnables.utils import ConfigurableFieldSpec, Input, Output

if TYPE_CHECKING:
    from langchain_core.callbacks.manager import (
        AsyncCallbackManagerForChainRun,
        CallbackManagerForChainRun,
    )
    from langchain_core.runnables.graph import Graph
    from langchain_core.runnables.schema import StreamEvent
    from langchain_core.tracers.log_stream import RunLog, RunLogPatch

__all__ = (
    "CoalesceBackend",
    "CoalesceStats",
    "InMemoryCoalesceBackend",
)


class CoalesceStats(NamedTuple):
    """Snapshot of coalescing counters.

    Attributes:
        active: Number of executions currently in flight.
        coalesced: Number of callers that joined an in-flight execution.
        total: Number of callers observed, including leaders and joiners.
    """

    active: int
    coalesced: int
    total: int


class CoalesceBackend(ABC):
    """Tracks in-flight executions so identical callers can share one result.

    Sync and async methods share the same in-flight state. `register` /
    `aregister` elect a single leader. That leader must call `complete` /
    `acomplete`. Every other caller must `join` / `ajoin` and receives the
    leader's result or error.

    `join` and `complete` must run on the same caller that `register` returned
    for. Async counterparts follow the same rule.
    """

    @abstractmethod
    def register(self, key: Hashable) -> bool:
        """Register the caller for `key`.

        Args:
            key: Hashable identity of the request.

        Returns:
            `True` if this caller is the leader and must execute the request.
            `False` if an execution is already in flight and the caller must
            `join` it.
        """

    @abstractmethod
    def join(self, key: Hashable) -> Any:
        """Wait for the in-flight execution registered by this caller.

        Args:
            key: Hashable identity passed to `register`.

        Returns:
            The result published by the leader.

        Raises:
            BaseException: The leader's error, or `asyncio.CancelledError` if
                the flight was cleared.
        """

    @abstractmethod
    def complete(
        self,
        key: Hashable,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        """Publish the leader's outcome and wake joiners.

        After this returns, `key` is no longer active. The next `register`
        starts a fresh execution.

        Args:
            key: Hashable identity passed to `register`.
            result: Value returned to joiners when `error` is `None`.
            error: Failure raised to joiners. Takes precedence over `result`.
        """

    @abstractmethod
    def is_active(self, key: Hashable) -> bool:
        """Return whether `key` has an execution in flight.

        Args:
            key: Hashable identity of the request.

        Returns:
            `True` if a leader is still running for `key`.
        """

    @property
    @abstractmethod
    def stats(self) -> CoalesceStats:
        """Return a snapshot of coalescing counters."""

    @abstractmethod
    async def aregister(self, key: Hashable) -> bool:
        """Async version of `register`.

        Args:
            key: Hashable identity of the request.

        Returns:
            `True` if this caller is the leader.
        """

    @abstractmethod
    async def ajoin(self, key: Hashable) -> Any:
        """Async version of `join`.

        Args:
            key: Hashable identity passed to `aregister`.

        Returns:
            The result published by the leader.

        Raises:
            BaseException: The leader's error, or `asyncio.CancelledError` if
                the flight was cleared.
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

        Wakes both sync and async joiners, same as `complete`.

        Args:
            key: Hashable identity passed to `aregister`.
            result: Value returned to joiners when `error` is `None`.
            error: Failure raised to joiners. Takes precedence over `result`.
        """

    @abstractmethod
    async def ais_active(self, key: Hashable) -> bool:
        """Async version of `is_active`.

        Args:
            key: Hashable identity of the request.

        Returns:
            `True` if a leader is still running for `key`.
        """

    def clear(self) -> None:
        """Cancel waiters with `asyncio.CancelledError` and reset stats.

        Raises:
            NotImplementedError: If this backend cannot drop in-flight state.
        """
        msg = "coalesce backend does not support clear()"
        raise NotImplementedError(msg)


class _Flight:
    """One in-flight execution shared by a leader and its joiners."""

    def __init__(self, key: Hashable) -> None:
        self.key = key
        self.done = False
        self.cancelled = False
        self.result: Any = None
        self.error: BaseException | None = None
        self.sync_event = threading.Event()
        self.async_waiters: list[asyncio.Future[Any]] = []


_PENDING: ContextVar[dict[Hashable, list[_Flight]] | None] = ContextVar(
    "langchain_core_coalesce_pending",
    default=None,
)


def _push_pending(key: Hashable, flight: _Flight) -> None:
    current = _PENDING.get()
    updated: dict[Hashable, list[_Flight]] = dict(current) if current else {}
    stack = list(updated.get(key, ()))
    stack.append(flight)
    updated[key] = stack
    _PENDING.set(updated)


def _pop_pending(key: Hashable) -> _Flight | None:
    current = _PENDING.get()
    if not current or not current.get(key):
        return None
    updated = dict(current)
    stack = list(updated[key])
    flight = stack.pop()
    if stack:
        updated[key] = stack
    else:
        del updated[key]
    _PENDING.set(updated)
    return flight


def _clone_exception(exc: BaseException) -> BaseException:
    """Return a distinct exception instance with the same type and message."""
    try:
        cloned: BaseException = exc.__class__(*exc.args)
    except Exception:
        return exc
    cloned.__cause__ = exc.__cause__
    cloned.__context__ = exc.__context__
    cloned.__suppress_context__ = exc.__suppress_context__
    try:
        if exc.__dict__:
            cloned.__dict__.update(exc.__dict__)
    except Exception:
        return exc
    if exc.__traceback__ is not None:
        return cloned.with_traceback(exc.__traceback__)
    return cloned


def _outcome(flight: _Flight) -> Any:
    if flight.cancelled:
        raise asyncio.CancelledError
    if flight.error is not None:
        raise _clone_exception(flight.error)
    return flight.result


class _CoalescedResult:
    """Envelope so invoke values are not iterated as stream chunks."""

    __slots__ = ("kind", "payload")

    def __init__(self, kind: Literal["value", "chunks"], payload: Any) -> None:
        self.kind = kind
        self.payload = payload


def _value_from_result(result: Any) -> Any:
    if not isinstance(result, _CoalescedResult):
        return result
    if result.kind == "chunks":
        return _reduce_chunks(result.payload)
    return result.payload


def _chunks_from_result(result: Any) -> tuple[Any, ...]:
    if not isinstance(result, _CoalescedResult):
        return (result,)
    if result.kind == "chunks":
        return tuple(result.payload)
    return (result.payload,)


def _reduce_chunks(chunks: Sequence[Any]) -> Any:
    if not chunks:
        return None
    final: Any = chunks[0]
    for chunk in chunks[1:]:
        try:
            final = final + chunk
        except TypeError:
            final = chunk
    return final


def _input_key(value: Any) -> Hashable:
    """Return a hashable identity for `value`.

    Dictionary key order is ignored. Call configuration is not included.
    """
    return _freeze(value, set())


def _freeze(value: Any, seen: set[int]) -> Hashable:
    if isinstance(value, float):
        if math.isnan(value):
            return ("scalar", "float", "nan")
        return ("scalar", "float", value)
    if value is None or isinstance(value, (bool, int, str, bytes)):
        return ("scalar", type(value).__name__, value)

    identity = id(value)
    if identity in seen:
        return ("cycle", type(value).__qualname__, identity)

    if isinstance(value, BaseModel):
        seen.add(identity)
        try:
            data = {name: getattr(value, name) for name in type(value).model_fields}
            frozen = (
                "model",
                f"{type(value).__module__}.{type(value).__qualname__}",
                _freeze(data, seen),
            )
        finally:
            seen.discard(identity)
        return frozen

    if is_dataclass(value) and not isinstance(value, type):
        seen.add(identity)
        try:
            data = {field.name: getattr(value, field.name) for field in fields(value)}
            frozen = (
                "dataclass",
                f"{type(value).__module__}.{type(value).__qualname__}",
                _freeze(data, seen),
            )
        finally:
            seen.discard(identity)
        return frozen

    if isinstance(value, Mapping):
        seen.add(identity)
        try:
            pairs = [
                (_freeze(key, seen), _freeze(item, seen)) for key, item in value.items()
            ]
            pairs.sort(key=repr)
            return ("mapping", tuple(pairs))
        finally:
            seen.discard(identity)

    if isinstance(value, (set, frozenset)):
        seen.add(identity)
        try:
            members = [_freeze(item, seen) for item in value]
            members.sort(key=repr)
            return ("set", tuple(members))
        finally:
            seen.discard(identity)

    if isinstance(value, (list, tuple)):
        seen.add(identity)
        try:
            return (
                "seq",
                type(value).__name__,
                tuple(_freeze(item, seen) for item in value),
            )
        finally:
            seen.discard(identity)

    try:
        hash(value)
    except TypeError:
        return ("repr", type(value).__qualname__, repr(value))
    return ("obj", f"{type(value).__module__}.{type(value).__qualname__}", value)


class InMemoryCoalesceBackend(CoalesceBackend):
    """Thread-safe process-local coalescing backend.

    Sync and async callers share one map of in-flight executions, so an
    `invoke` and an `ainvoke` of the same key coalesce with each other.
    """

    def __init__(self) -> None:
        """Create an empty backend."""
        self._lock = threading.Lock()
        self._inflight: dict[Hashable, _Flight] = {}
        self._active = 0
        self._coalesced = 0
        self._total = 0

    @override
    def register(self, key: Hashable) -> bool:
        """Register the caller for `key`.

        Args:
            key: Hashable identity of the request.

        Returns:
            `True` if this caller is the leader.
        """
        with self._lock:
            flight = self._inflight.get(key)
            if flight is None or flight.done:
                flight = _Flight(key)
                self._inflight[key] = flight
                self._active += 1
                self._total += 1
                is_leader = True
            else:
                self._total += 1
                self._coalesced += 1
                is_leader = False
        _push_pending(key, flight)
        return is_leader

    @override
    def join(self, key: Hashable) -> Any:
        """Wait for this caller's in-flight execution.

        Args:
            key: Hashable identity passed to `register`.

        Returns:
            The leader's result.

        Raises:
            RuntimeError: If this caller did not `register` as a joiner.
            BaseException: The leader's error, or `asyncio.CancelledError` when
                the flight was cleared.
        """
        flight = _pop_pending(key)
        if flight is None:
            msg = "join() requires register() on the same caller"
            raise RuntimeError(msg)
        flight.sync_event.wait()
        return _outcome(flight)

    @override
    def complete(
        self,
        key: Hashable,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        """Publish the leader's outcome.

        Args:
            key: Hashable identity passed to `register`.
            result: Value returned to joiners when `error` is `None`.
            error: Failure raised to joiners.
        """
        flight = _pop_pending(key)
        if flight is None:
            return
        self._publish(flight, result=result, error=error)

    @override
    def is_active(self, key: Hashable) -> bool:
        """Return whether `key` is in flight.

        Args:
            key: Hashable identity of the request.

        Returns:
            `True` if a leader is still running for `key`.
        """
        with self._lock:
            flight = self._inflight.get(key)
            return flight is not None and not flight.done

    @property
    @override
    def stats(self) -> CoalesceStats:
        """Return a snapshot of coalescing counters."""
        with self._lock:
            return CoalesceStats(
                active=self._active,
                coalesced=self._coalesced,
                total=self._total,
            )

    @override
    async def aregister(self, key: Hashable) -> bool:
        """Register the caller for `key` without blocking the event loop.

        Args:
            key: Hashable identity of the request.

        Returns:
            `True` if this caller is the leader.
        """
        return self.register(key)

    @override
    async def ajoin(self, key: Hashable) -> Any:
        """Wait for this caller's in-flight execution.

        Args:
            key: Hashable identity passed to `aregister`.

        Returns:
            The leader's result.

        Raises:
            RuntimeError: If this caller did not `aregister` as a joiner.
            BaseException: The leader's error, or `asyncio.CancelledError` when
                the flight was cleared.
        """
        flight = _pop_pending(key)
        if flight is None:
            msg = "ajoin() requires aregister() on the same caller"
            raise RuntimeError(msg)
        loop = asyncio.get_running_loop()
        fut: asyncio.Future[Any] | None
        with self._lock:
            if flight.done:
                fut = None
            else:
                fut = loop.create_future()
                flight.async_waiters.append(fut)
        if fut is None:
            return _outcome(flight)
        return await fut

    @override
    async def acomplete(
        self,
        key: Hashable,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        """Publish the leader's outcome and wake sync and async joiners.

        Args:
            key: Hashable identity passed to `aregister`.
            result: Value returned to joiners when `error` is `None`.
            error: Failure raised to joiners.
        """
        self.complete(key, result=result, error=error)

    @override
    async def ais_active(self, key: Hashable) -> bool:
        """Return whether `key` is in flight.

        Args:
            key: Hashable identity of the request.

        Returns:
            `True` if a leader is still running for `key`.
        """
        return self.is_active(key)

    @override
    def clear(self) -> None:
        """Cancel every waiter with `asyncio.CancelledError` and reset stats."""
        with self._lock:
            flights = list(self._inflight.values())
            self._inflight.clear()
            self._active = 0
            self._coalesced = 0
            self._total = 0
            wake: list[tuple[_Flight, list[asyncio.Future[Any]]]] = []
            for flight in flights:
                if flight.done:
                    continue
                flight.done = True
                flight.cancelled = True
                waiters = list(flight.async_waiters)
                flight.async_waiters.clear()
                wake.append((flight, waiters))
        for flight, waiters in wake:
            flight.sync_event.set()
            for fut in waiters:
                self._settle_future(fut, flight)

    def _publish(
        self,
        flight: _Flight,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        with self._lock:
            if flight.done:
                return
            flight.done = True
            flight.result = result
            flight.error = error
            if self._inflight.get(flight.key) is flight:
                self._inflight.pop(flight.key, None)
                self._active -= 1
            waiters = list(flight.async_waiters)
            flight.async_waiters.clear()
        flight.sync_event.set()
        for fut in waiters:
            self._settle_future(fut, flight)

    def _settle_future(self, fut: asyncio.Future[Any], flight: _Flight) -> None:
        if fut.done():
            return

        def _apply() -> None:
            if fut.done():
                return
            if flight.cancelled:
                fut.cancel()
                return
            if flight.error is not None:
                fut.set_exception(_clone_exception(flight.error))
                return
            fut.set_result(flight.result)

        loop = fut.get_loop()
        try:
            running = asyncio.get_running_loop()
        except RuntimeError:
            running = None
        if running is loop:
            _apply()
            return
        try:
            loop.call_soon_threadsafe(_apply)
        except RuntimeError:
            return


class RunnableCoalesce(Runnable[Input, Output]):
    """`Runnable` that shares one in-flight execution per input value.

    `transform`, `atransform`, and event streaming delegate to the wrapped
    `Runnable` without coalescing. Graph inspection delegates as well, so the
    wrapper does not appear as its own node.
    """

    def __init__(
        self, *, bound: Runnable[Input, Output], backend: CoalesceBackend
    ) -> None:
        """Wrap `bound` with request coalescing.

        Args:
            bound: Runnable that performs the real work.
            backend: Store that tracks in-flight executions.
        """
        self.bound = bound
        self.backend = backend

    def coalesce_info(self) -> CoalesceStats:
        """Return counters for this wrapper's backend.

        Returns:
            Active executions, joined callers, and total callers.
        """
        return self.backend.stats

    def coalesce_clear(self) -> None:
        """Cancel in-flight waiters with `asyncio.CancelledError` and reset stats.

        Leaders that have already started keep running. Callers blocked in
        `join` or `ajoin` raise `asyncio.CancelledError`.
        """
        self.backend.clear()

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

    def __getattr__(self, name: str) -> Any:
        bound = self.__dict__.get("bound")
        if bound is None:
            msg = f"{type(self).__name__} has no attribute {name!r}"
            raise AttributeError(msg)
        return getattr(bound, name)

    @override
    def invoke(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> Output:
        return self._call_with_config(self._invoke_coalesced, input, config, **kwargs)

    def _invoke_coalesced(
        self,
        value: Input,
        run_manager: CallbackManagerForChainRun,  # noqa: ARG002
        config: RunnableConfig,
        **kwargs: Any,
    ) -> Output:
        key = _input_key(value)
        if self.backend.register(key):
            try:
                result = self.bound.invoke(value, config, **kwargs)
            except BaseException as exc:
                self.backend.complete(key, error=exc)
                raise
            else:
                self.backend.complete(key, result=_CoalescedResult("value", result))
                return result
        return cast("Output", _value_from_result(self.backend.join(key)))

    @override
    async def ainvoke(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> Output:
        return await self._acall_with_config(
            self._ainvoke_coalesced, input, config, **kwargs
        )

    async def _ainvoke_coalesced(
        self,
        value: Input,
        run_manager: AsyncCallbackManagerForChainRun,  # noqa: ARG002
        config: RunnableConfig,
        **kwargs: Any,
    ) -> Output:
        key = _input_key(value)
        if await self.backend.aregister(key):
            try:
                result = await self.bound.ainvoke(value, config, **kwargs)
            except BaseException as exc:
                self.backend.complete(key, error=exc)
                raise
            else:
                self.backend.complete(key, result=_CoalescedResult("value", result))
                return result
        joined = await self.backend.ajoin(key)
        return cast("Output", _value_from_result(joined))

    @override
    def stream(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any | None,
    ) -> Iterator[Output]:
        yield from self._transform_stream_with_config(
            iter([input]),
            self._coalesced_stream,
            config,
            **kwargs,
        )

    def _coalesced_stream(
        self,
        inputs: Iterator[Input],
        run_manager: CallbackManagerForChainRun,  # noqa: ARG002
        config: RunnableConfig,
        **kwargs: Any,
    ) -> Iterator[Output]:
        try:
            value = next(inputs)
        except StopIteration:
            return
        key = _input_key(value)
        if self.backend.register(key):
            chunks: list[Output] = []
            published = False
            try:
                for chunk in self.bound.stream(value, config, **kwargs):
                    chunks.append(chunk)
                    yield chunk
            except GeneratorExit:
                raise
            except BaseException as exc:
                self.backend.complete(key, error=exc)
                published = True
                raise
            finally:
                if not published:
                    self.backend.complete(
                        key, result=_CoalescedResult("chunks", tuple(chunks))
                    )
            return
        result = self.backend.join(key)
        yield from _chunks_from_result(result)

    @override
    async def astream(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any | None,
    ) -> AsyncIterator[Output]:
        async for chunk in self._atransform_stream_with_config(
            _async_single(input),
            self._coalesced_astream,
            config,
            **kwargs,
        ):
            yield chunk

    async def _coalesced_astream(
        self,
        inputs: AsyncIterator[Input],
        run_manager: AsyncCallbackManagerForChainRun,  # noqa: ARG002
        config: RunnableConfig,
        **kwargs: Any,
    ) -> AsyncIterator[Output]:
        try:
            value = await anext(inputs)
        except StopAsyncIteration:
            return
        key = _input_key(value)
        if await self.backend.aregister(key):
            chunks: list[Output] = []
            published = False
            try:
                async for chunk in self.bound.astream(value, config, **kwargs):
                    chunks.append(chunk)
                    yield chunk
            except GeneratorExit:
                raise
            except BaseException as exc:
                self.backend.complete(key, error=exc)
                published = True
                raise
            finally:
                if not published:
                    self.backend.complete(
                        key, result=_CoalescedResult("chunks", tuple(chunks))
                    )
            return
        result = await self.backend.ajoin(key)
        for chunk in _chunks_from_result(result):
            yield chunk

    @overload
    def batch_as_completed(
        self,
        inputs: Sequence[Input],
        config: RunnableConfig | Sequence[RunnableConfig] | None = None,
        *,
        return_exceptions: Literal[False] = False,
        **kwargs: Any | None,
    ) -> Iterator[tuple[int, Output]]: ...

    @overload
    def batch_as_completed(
        self,
        inputs: Sequence[Input],
        config: RunnableConfig | Sequence[RunnableConfig] | None = None,
        *,
        return_exceptions: Literal[True],
        **kwargs: Any | None,
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
        keys, groups = _group_inputs(inputs)
        emitted: set[Hashable] = set()

        def _one(index: int) -> Output:
            return self.invoke(inputs[index], configs[index], **kwargs)

        with get_executor_for_config(configs[0]) as executor:
            futures = {
                executor.submit(_one, index): index for index in range(len(inputs))
            }
            pending = set(futures)
            try:
                while pending:
                    done, pending = wait(pending, return_when=FIRST_COMPLETED)
                    for future in done:
                        index = futures[future]
                        key = keys[index]
                        try:
                            outcome: Output | Exception = future.result()
                        except Exception as exc:
                            outcome = exc
                            if key not in emitted and not return_exceptions:
                                raise
                        if key in emitted:
                            continue
                        emitted.add(key)
                        for group_index in groups[key]:
                            yield group_index, outcome
            finally:
                for future in pending:
                    future.cancel()

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
        keys, groups = _group_inputs(inputs)
        emitted: set[Hashable] = set()
        max_concurrency = configs[0].get("max_concurrency")
        semaphore = (
            asyncio.Semaphore(max_concurrency) if max_concurrency is not None else None
        )

        async def _one(index: int) -> Output | Exception:
            try:
                if semaphore is not None:
                    async with semaphore:
                        return await self.ainvoke(
                            inputs[index], configs[index], **kwargs
                        )
                return await self.ainvoke(inputs[index], configs[index], **kwargs)
            except Exception as exc:
                if not return_exceptions:
                    raise
                return exc

        task_to_index: dict[asyncio.Task[Output | Exception], int] = {}
        for index in range(len(inputs)):
            task = asyncio.create_task(_one(index))
            task_to_index[task] = index
        pending = set(task_to_index)
        try:
            while pending:
                done, pending = await asyncio.wait(
                    pending, return_when=asyncio.FIRST_COMPLETED
                )
                for task in done:
                    index = task_to_index[task]
                    key = keys[index]
                    try:
                        outcome: Output | Exception = task.result()
                    except Exception as exc:
                        outcome = exc
                        if key not in emitted and not return_exceptions:
                            raise
                    if key in emitted:
                        continue
                    emitted.add(key)
                    for group_index in groups[key]:
                        yield group_index, outcome
        finally:
            for task in pending:
                task.cancel()
            if pending:
                await asyncio.gather(*pending, return_exceptions=True)

    @override
    def transform(
        self,
        input: Iterator[Input],
        config: RunnableConfig | None = None,
        **kwargs: Any | None,
    ) -> Iterator[Output]:
        yield from self.bound.transform(input, config, **kwargs)

    @override
    async def atransform(
        self,
        input: AsyncIterator[Input],
        config: RunnableConfig | None = None,
        **kwargs: Any | None,
    ) -> AsyncIterator[Output]:
        async for chunk in self.bound.atransform(input, config, **kwargs):
            yield chunk

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
        if diff:
            async for item in self.bound.astream_log(
                input,
                config,
                diff=True,
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
            return
        async for item in self.bound.astream_log(
            input,
            config,
            diff=False,
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

    @override
    async def astream_events(
        self,
        input: Any,
        config: RunnableConfig | None = None,
        *,
        version: Literal["v1", "v2"] = "v2",
        include_names: Sequence[str] | None = None,
        include_types: Sequence[str] | None = None,
        include_tags: Sequence[str] | None = None,
        exclude_names: Sequence[str] | None = None,
        exclude_types: Sequence[str] | None = None,
        exclude_tags: Sequence[str] | None = None,
        **kwargs: Any,
    ) -> AsyncIterator[StreamEvent]:
        async for event in self.bound.astream_events(
            input,
            config,
            version=version,
            include_names=include_names,
            include_types=include_types,
            include_tags=include_tags,
            exclude_names=exclude_names,
            exclude_types=exclude_types,
            exclude_tags=exclude_tags,
            **kwargs,
        ):
            yield event


def _group_inputs(
    inputs: Sequence[Input],
) -> tuple[list[Hashable], dict[Hashable, list[int]]]:
    keys: list[Hashable] = []
    groups: dict[Hashable, list[int]] = {}
    for index, item in enumerate(inputs):
        key = _input_key(item)
        keys.append(key)
        groups.setdefault(key, []).append(index)
    return keys, groups


async def _async_single(value: Input) -> AsyncIterator[Input]:
    yield value
