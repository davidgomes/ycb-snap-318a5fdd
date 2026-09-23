"""Request coalescing for concurrent `Runnable` calls."""

from __future__ import annotations

import asyncio
import dataclasses
import threading
from abc import ABC, abstractmethod
from collections import defaultdict
from collections.abc import AsyncIterator, Iterator, Mapping, Sequence
from concurrent.futures import FIRST_COMPLETED, ThreadPoolExecutor, wait
from contextvars import ContextVar, copy_context
from typing import TYPE_CHECKING, Any, Literal, NamedTuple, cast, overload

from pydantic import BaseModel
from typing_extensions import override

from langchain_core.runnables.base import Runnable
from langchain_core.runnables.config import RunnableConfig, get_config_list
from langchain_core.runnables.utils import (
    ConfigurableFieldSpec,
    Input,
    Output,
)

if TYPE_CHECKING:
    from langchain_core.callbacks.manager import (
        AsyncCallbackManagerForChainRun,
        CallbackManagerForChainRun,
    )

_SLOTS: ContextVar[dict[tuple[int, Any], _Slot] | None] = ContextVar(
    "coalesce_slots",
    default=None,
)


class CoalesceStats(NamedTuple):
    """Snapshot of coalesce activity.

    Attributes:
        active: Number of keys with an execution still in flight.
        coalesced: Number of callers that joined an in-flight execution.
        total: Number of `register` calls since the last reset.
    """

    active: int
    coalesced: int
    total: int


class CoalesceBackend(ABC):
    """Tracks in-flight calls so identical work can be shared.

    `register` returns `True` for the caller that owns the execution and
    `False` for callers that must wait. `join` blocks until `complete`
    publishes a result or an error. `stats` counts in-flight keys, joined
    callers, and registrations.
    """

    @abstractmethod
    def register(self, key: Any) -> bool:
        """Register interest in `key`.

        Args:
            key: Opaque coalescing key.

        Returns:
            `True` if this caller should run the execution, `False` if it
            should wait for the in-flight one.
        """

    @abstractmethod
    def join(self, key: Any) -> Any:
        """Wait for the in-flight execution of `key`.

        Args:
            key: Opaque coalescing key previously passed to `register`.

        Returns:
            The value passed to `complete` as `result`.

        Raises:
            BaseException: The error passed to `complete`, or
                `asyncio.CancelledError` if waiters were cancelled.
        """

    @abstractmethod
    def complete(
        self,
        key: Any,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        """Publish the outcome of an in-flight execution.

        Args:
            key: Opaque coalescing key.
            result: Value returned to waiters when `error` is omitted.
            error: Failure raised to the owner and to waiters.
        """

    @abstractmethod
    def is_active(self, key: Any) -> bool:
        """Return whether `key` has an execution in flight.

        Args:
            key: Opaque coalescing key.

        Returns:
            `True` when a registration for `key` has not been completed.
        """

    @property
    @abstractmethod
    def stats(self) -> CoalesceStats:
        """Return a snapshot of active, coalesced, and total counts."""

    def clear(self) -> None:
        """Cancel waiters and reset counters.

        Backends that store waiters override this. The default refuses so a
        caller is not told that state was cleared when it was not.

        Raises:
            NotImplementedError: When the backend cannot cancel waiters.
        """
        msg = f"{type(self).__name__} does not support clear()."
        raise NotImplementedError(msg)

    async def aregister(self, key: Any) -> bool:
        """Async counterpart of `register`.

        Args:
            key: Opaque coalescing key.

        Returns:
            Whether this caller owns the execution.
        """
        return self.register(key)

    async def ajoin(self, key: Any) -> Any:
        """Async counterpart of `join`.

        Args:
            key: Opaque coalescing key previously passed to `aregister`.

        Returns:
            The completed result.
        """
        return await asyncio.to_thread(self.join, key)

    async def acomplete(
        self,
        key: Any,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        """Async counterpart of `complete`.

        Args:
            key: Opaque coalescing key.
            result: Value returned to waiters when `error` is omitted.
            error: Failure raised to waiters.
        """
        self.complete(key, result=result, error=error)

    async def ais_active(self, key: Any) -> bool:
        """Async counterpart of `is_active`.

        Args:
            key: Opaque coalescing key.

        Returns:
            Whether `key` still has an execution in flight.
        """
        return self.is_active(key)


class _Slot:
    """One in-flight execution shared by a leader and its joiners."""

    __slots__ = ("async_waiters", "cancelled", "done", "error", "event", "result")

    def __init__(self) -> None:
        self.event = threading.Event()
        self.result: Any = None
        self.error: BaseException | None = None
        self.cancelled = False
        self.done = False
        self.async_waiters: list[asyncio.Future[Any]] = []


def _remember(backend_id: int, key: Any, slot: _Slot) -> None:
    current = _SLOTS.get()
    updated = dict(current or {})
    updated[(backend_id, key)] = slot
    _SLOTS.set(updated)


def _recall(backend_id: int, key: Any) -> _Slot | None:
    current = _SLOTS.get()
    if not current:
        return None
    return current.get((backend_id, key))


def _deliver(slot: _Slot, fut: asyncio.Future[Any]) -> None:
    if fut.done():
        return
    if slot.cancelled:
        fut.cancel()
        return
    if slot.error is not None:
        fut.set_exception(slot.error)
        return
    fut.set_result(slot.result)


def _schedule_delivery(slot: _Slot, fut: asyncio.Future[Any]) -> None:
    loop = fut.get_loop()
    try:
        running = asyncio.get_running_loop()
    except RuntimeError:
        running = None
    if running is loop:
        _deliver(slot, fut)
    else:
        loop.call_soon_threadsafe(_deliver, slot, fut)


def _outcome(slot: _Slot) -> Any:
    if slot.cancelled:
        raise asyncio.CancelledError
    if slot.error is not None:
        raise slot.error
    return slot.result


class InMemoryCoalesceBackend(CoalesceBackend):
    """Thread-safe process-local coalesce backend."""

    def __init__(self) -> None:
        """Create an empty backend."""
        self._lock = threading.Lock()
        self._active: dict[Any, _Slot] = {}
        self._coalesced = 0
        self._total = 0

    def _register_slot(self, key: Any) -> tuple[bool, _Slot]:
        with self._lock:
            self._total += 1
            slot = self._active.get(key)
            if slot is None:
                slot = _Slot()
                self._active[key] = slot
                return True, slot
            self._coalesced += 1
            return False, slot

    @override
    def register(self, key: Any) -> bool:
        leader, slot = self._register_slot(key)
        _remember(id(self), key, slot)
        return leader

    def _slot_for(self, key: Any) -> _Slot:
        slot = _recall(id(self), key)
        if slot is not None:
            return slot
        with self._lock:
            active = self._active.get(key)
        if active is None:
            msg = f"No in-flight coalesce entry for key {key!r}."
            raise KeyError(msg)
        return active

    @override
    def join(self, key: Any) -> Any:
        slot = self._slot_for(key)
        slot.event.wait()
        return _outcome(slot)

    def _finish(
        self,
        key: Any,
        slot: _Slot | None,
        *,
        result: Any,
        error: BaseException | None,
        cancelled: bool,
    ) -> None:
        waiters: list[asyncio.Future[Any]]
        with self._lock:
            if slot is None:
                slot = self._active.get(key)
            if slot is None or slot.done:
                return
            slot.result = result
            slot.error = error
            slot.cancelled = cancelled
            slot.done = True
            if self._active.get(key) is slot:
                del self._active[key]
            waiters = list(slot.async_waiters)
            slot.async_waiters.clear()
            slot.event.set()
        for fut in waiters:
            _schedule_delivery(slot, fut)

    @override
    def complete(
        self,
        key: Any,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        self._finish(
            key,
            _recall(id(self), key),
            result=result,
            error=error,
            cancelled=False,
        )

    @override
    def is_active(self, key: Any) -> bool:
        with self._lock:
            slot = self._active.get(key)
            return slot is not None and not slot.done

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
    async def aregister(self, key: Any) -> bool:
        leader, slot = await asyncio.to_thread(self._register_slot, key)
        _remember(id(self), key, slot)
        return leader

    @override
    async def acomplete(
        self,
        key: Any,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        slot = _recall(id(self), key)
        await asyncio.to_thread(
            self._finish,
            key,
            slot,
            result=result,
            error=error,
            cancelled=False,
        )

    @override
    async def ais_active(self, key: Any) -> bool:
        return await asyncio.to_thread(self.is_active, key)

    @override
    def clear(self) -> None:
        """Cancel every waiter with `asyncio.CancelledError` and reset stats."""
        with self._lock:
            slots = list(self._active.values())
            self._active.clear()
            self._coalesced = 0
            self._total = 0
            pending: list[tuple[_Slot, list[asyncio.Future[Any]]]] = []
            for slot in slots:
                if slot.done:
                    continue
                slot.cancelled = True
                slot.done = True
                slot.error = asyncio.CancelledError()
                waiters = list(slot.async_waiters)
                slot.async_waiters.clear()
                slot.event.set()
                pending.append((slot, waiters))
        for slot, waiters in pending:
            for fut in waiters:
                _schedule_delivery(slot, fut)

    @override
    async def ajoin(self, key: Any) -> Any:
        slot = _recall(id(self), key)
        if slot is None:
            slot = await asyncio.to_thread(self._slot_for, key)
        loop = asyncio.get_running_loop()
        fut: asyncio.Future[Any] = loop.create_future()

        def _subscribe() -> tuple[bool, bool, BaseException | None, Any]:
            with self._lock:
                if slot.done:
                    return True, slot.cancelled, slot.error, slot.result
                slot.async_waiters.append(fut)
                return False, False, None, None

        done, cancelled, error, result = await asyncio.to_thread(_subscribe)
        waiting = not done
        if not waiting:
            if cancelled:
                fut.cancel()
                raise asyncio.CancelledError
            if error is not None:
                fut.cancel()
                raise error
            fut.cancel()
            return result
        try:
            return await fut
        finally:
            if not fut.done():
                fut.cancel()


class _CoalescePayload:
    """Result shared across invoke-style and stream-style callers."""

    __slots__ = ("chunks", "value")

    def __init__(self, value: Any, chunks: list[Any]) -> None:
        self.value = value
        self.chunks = chunks


def _aggregate(chunks: list[Any]) -> Any:
    if not chunks:
        return None
    final = chunks[0]
    supported = True
    for chunk in chunks[1:]:
        if supported:
            try:
                final = final + chunk
            except TypeError:
                final = chunk
                supported = False
        else:
            final = chunk
    return final


def _value_payload(value: Any) -> _CoalescePayload:
    return _CoalescePayload(value, [value])


def _stream_payload(chunks: list[Any]) -> _CoalescePayload:
    return _CoalescePayload(_aggregate(chunks), list(chunks))


def _payload_value(result: Any) -> Any:
    if isinstance(result, _CoalescePayload):
        return result.value
    return result


def _payload_chunks(result: Any) -> list[Any]:
    if isinstance(result, _CoalescePayload):
        return list(result.chunks)
    return [result]


def _freeze(value: Any) -> Any:
    """Build an order-insensitive, hashable identity for an input value."""
    if (
        isinstance(value, bool)
        or value is None
        or isinstance(value, (str, int, float, bytes))
    ):
        return ("scalar", type(value).__name__, value)
    if isinstance(value, Mapping):
        items = tuple(
            sorted(
                ((_freeze(key), _freeze(item)) for key, item in value.items()),
                key=repr,
            )
        )
        return ("mapping", items)
    if isinstance(value, (list, tuple)):
        return (type(value).__name__, tuple(_freeze(item) for item in value))
    if isinstance(value, (set, frozenset)):
        return ("set", tuple(sorted((_freeze(item) for item in value), key=repr)))
    if isinstance(value, BaseModel):
        return ("model", type(value).__qualname__, _freeze(value.model_dump()))
    if dataclasses.is_dataclass(value) and not isinstance(value, type):
        return (
            "dataclass",
            type(value).__qualname__,
            _freeze(dataclasses.asdict(value)),
        )
    try:
        hash(value)
    except TypeError:
        return ("repr", type(value).__qualname__, repr(value))
    return ("hashable", type(value).__qualname__, value)


def _coalesce_key(value: Any) -> Any:
    return _freeze(value)


def _pool_size(config: RunnableConfig | None, leaders: int) -> int:
    if leaders <= 0:
        return 1
    raw = None if config is None else config.get("max_concurrency")
    if isinstance(raw, int) and raw > 0:
        return min(raw, leaders)
    return min(32, leaders)


class RunnableCoalesce(Runnable[Input, Output]):
    """Share one in-flight execution among identical concurrent inputs."""

    def __init__(
        self,
        *,
        bound: Runnable[Input, Output],
        backend: CoalesceBackend,
    ) -> None:
        """Wrap `bound` so identical concurrent calls share one execution.

        Args:
            bound: Runnable that performs the real work.
            backend: In-flight state shared by every entrypoint on this wrapper.
        """
        self.bound = bound
        self._backend = backend
        self.name: str | None = None

    @override
    def get_name(self, suffix: str | None = None, *, name: str | None = None) -> str:
        """Return the wrapped runnable's name.

        Args:
            suffix: Optional suffix appended by the wrapped runnable.
            name: Optional explicit name.

        Returns:
            The delegated runnable name.
        """
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
        self,
        config: RunnableConfig | None = None,
    ) -> type[BaseModel]:
        return self.bound.get_output_schema(config)

    @property
    @override
    def config_specs(self) -> list[ConfigurableFieldSpec]:
        return self.bound.config_specs

    @override
    def get_graph(self, config: RunnableConfig | None = None) -> Any:
        """Return the wrapped runnable's graph unchanged.

        Args:
            config: Config forwarded to the wrapped runnable.

        Returns:
            The graph of the wrapped runnable.
        """
        return self.bound.get_graph(config)

    def coalesce_info(self) -> CoalesceStats:
        """Return the backend's active, coalesced, and total counts."""
        return self._backend.stats

    def coalesce_clear(self) -> None:
        """Cancel waiters with `asyncio.CancelledError` and reset stats."""
        self._backend.clear()

    def _sync_value(
        self,
        input_: Input,
        config: RunnableConfig,
        **kwargs: Any,
    ) -> Output:
        key = _coalesce_key(input_)
        if self._backend.register(key):
            try:
                result = self.bound.invoke(input_, config, **kwargs)
            except BaseException as exc:
                self._backend.complete(key, error=exc)
                raise
            self._backend.complete(key, result=_value_payload(result))
            return result
        return cast("Output", _payload_value(self._backend.join(key)))

    async def _async_value(
        self,
        input_: Input,
        config: RunnableConfig,
        **kwargs: Any,
    ) -> Output:
        key = _coalesce_key(input_)
        if await self._backend.aregister(key):
            try:
                result = await self.bound.ainvoke(input_, config, **kwargs)
            except BaseException as exc:
                await self._backend.acomplete(key, error=exc)
                raise
            await self._backend.acomplete(key, result=_value_payload(result))
            return result
        return cast("Output", _payload_value(await self._backend.ajoin(key)))

    @override
    def invoke(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> Output:
        """Coalesce concurrent `invoke` calls that share an input value.

        Args:
            input: Input passed to the wrapped runnable.
            config: Config for this caller. It is not part of the coalescing key.
            **kwargs: Extra arguments for the leader's execution.

        Returns:
            The shared execution result.
        """

        def _call(
            input_: Input,
            run_manager: CallbackManagerForChainRun,
            config: RunnableConfig,
            **call_kwargs: Any,
        ) -> Output:
            del run_manager
            return self._sync_value(input_, config, **call_kwargs)

        return self._call_with_config(_call, input, config, **kwargs)

    @override
    async def ainvoke(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> Output:
        """Coalesce concurrent `ainvoke` calls that share an input value.

        Args:
            input: Input passed to the wrapped runnable.
            config: Config for this caller. It is not part of the coalescing key.
            **kwargs: Extra arguments for the leader's execution.

        Returns:
            The shared execution result.
        """

        async def _call(
            input_: Input,
            run_manager: AsyncCallbackManagerForChainRun,
            config: RunnableConfig,
            **call_kwargs: Any,
        ) -> Output:
            del run_manager
            return await self._async_value(input_, config, **call_kwargs)

        return await self._acall_with_config(_call, input, config, **kwargs)

    def _sync_stream(
        self,
        input_: Input,
        config: RunnableConfig,
        **kwargs: Any,
    ) -> Iterator[Output]:
        key = _coalesce_key(input_)
        if self._backend.register(key):
            produced: list[Output] = []
            try:
                for chunk in self.bound.stream(input_, config, **kwargs):
                    produced.append(chunk)
                    yield chunk
            except GeneratorExit:
                msg = "Coalesced stream closed before completion."
                self._backend.complete(key, error=RuntimeError(msg))
                raise
            except BaseException as exc:
                self._backend.complete(key, error=exc)
                raise
            else:
                self._backend.complete(key, result=_stream_payload(produced))
        else:
            yield from cast("list[Output]", _payload_chunks(self._backend.join(key)))

    async def _async_stream(
        self,
        input_: Input,
        config: RunnableConfig,
        **kwargs: Any,
    ) -> AsyncIterator[Output]:
        key = _coalesce_key(input_)
        if await self._backend.aregister(key):
            produced: list[Output] = []
            try:
                async for chunk in self.bound.astream(input_, config, **kwargs):
                    produced.append(chunk)
                    yield chunk
            except GeneratorExit:
                msg = "Coalesced stream closed before completion."
                await self._backend.acomplete(key, error=RuntimeError(msg))
                raise
            except BaseException as exc:
                await self._backend.acomplete(key, error=exc)
                raise
            else:
                await self._backend.acomplete(key, result=_stream_payload(produced))
        else:
            for chunk in _payload_chunks(await self._backend.ajoin(key)):
                chunk_out: Output = chunk
                yield chunk_out

    @override
    def stream(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> Iterator[Output]:
        """Coalesce concurrent streams and replay every chunk to joiners.

        Args:
            input: Input passed to the wrapped runnable.
            config: Config for this caller. It is not part of the coalescing key.
            **kwargs: Extra arguments for the leader's execution.

        Yields:
            Output chunks. Joiners receive the leader's chunks from the start.
        """
        call_kwargs = dict(kwargs)

        def _transformer(
            chunks: Iterator[Input],
            run_manager: CallbackManagerForChainRun,
            config: RunnableConfig,
        ) -> Iterator[Output]:
            del run_manager
            try:
                input_ = next(chunks)
            except StopIteration:
                return
            yield from self._sync_stream(input_, config, **call_kwargs)

        yield from self._transform_stream_with_config(
            iter([input]),
            _transformer,
            config,
        )

    @override
    async def astream(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> AsyncIterator[Output]:
        """Coalesce concurrent async streams and replay every chunk to joiners.

        Args:
            input: Input passed to the wrapped runnable.
            config: Config for this caller. It is not part of the coalescing key.
            **kwargs: Extra arguments for the leader's execution.

        Yields:
            Output chunks. Joiners receive the leader's chunks from the start.
        """
        call_kwargs = dict(kwargs)

        async def _single() -> AsyncIterator[Input]:
            yield input

        async def _transformer(
            chunks: AsyncIterator[Input],
            run_manager: AsyncCallbackManagerForChainRun,
            config: RunnableConfig,
        ) -> AsyncIterator[Output]:
            del run_manager
            input_ = await anext(chunks, None)
            if input_ is None:
                return
            async for chunk in self._async_stream(input_, config, **call_kwargs):
                yield chunk

        async for chunk in self._atransform_stream_with_config(
            _single(),
            _transformer,
            config,
        ):
            yield chunk

    def _run_sync_group(
        self,
        inputs: Sequence[Input],
        configs: list[RunnableConfig],
        **kwargs: Any,
    ) -> tuple[list[Any], list[bool], dict[Any, Any]]:
        """Register every item, run leaders, and return per-key outcomes.

        Returns:
            Keys aligned with `inputs`, leader flags, and each key's outcome.
            Outcomes may be exceptions when a leader fails.
        """
        keys = [_coalesce_key(item) for item in inputs]
        flags: list[bool] = []
        leaders: dict[Any, int] = {}
        for index, key in enumerate(keys):
            is_leader = self._backend.register(key)
            flags.append(is_leader)
            if is_leader:
                leaders[key] = index

        outcomes: dict[Any, Any] = {}

        def run_leader(index: int) -> Any:
            key = keys[index]
            try:
                result = self.bound.invoke(inputs[index], configs[index], **kwargs)
            except Exception as exc:
                self._backend.complete(key, error=exc)
                return exc
            except BaseException as exc:
                self._backend.complete(key, error=exc)
                raise
            self._backend.complete(key, result=_value_payload(result))
            return result

        if leaders:
            config_for_pool = configs[0] if configs else None
            worker_count = _pool_size(config_for_pool, len(leaders))
            with ThreadPoolExecutor(max_workers=worker_count) as pool:
                futures = []
                for key, index in leaders.items():
                    ctx = copy_context()
                    futures.append((key, pool.submit(ctx.run, run_leader, index)))
                for key, fut in futures:
                    try:
                        outcomes[key] = fut.result()
                    except Exception as exc:
                        outcomes[key] = exc

        resolved: dict[Any, Any] = {}
        for key in keys:
            if key in resolved:
                continue
            if key in outcomes and not isinstance(outcomes[key], Exception):
                resolved[key] = outcomes[key]
                continue
            if key in outcomes and isinstance(outcomes[key], Exception):
                resolved[key] = outcomes[key]
                continue
            try:
                resolved[key] = _payload_value(self._backend.join(key))
            except Exception as exc:
                resolved[key] = exc
        return keys, flags, resolved

    @override
    def batch(
        self,
        inputs: list[Input],
        config: RunnableConfig | list[RunnableConfig] | None = None,
        *,
        return_exceptions: bool = False,
        **kwargs: Any,
    ) -> list[Output]:
        """Coalesce each batch item and return results in input order.

        Args:
            inputs: Inputs to run. Equal values share one execution.
            config: Config or per-input configs. Not part of the coalescing key.
            return_exceptions: Return exceptions in place of raising them.
            **kwargs: Extra arguments forwarded to each leader execution.

        Returns:
            Outputs aligned with `inputs`.
        """
        if not inputs:
            return []

        def _execute(
            batch_inputs: list[Input],
            run_manager: list[CallbackManagerForChainRun],
            config: list[RunnableConfig],
            **call_kwargs: Any,
        ) -> list[Output | Exception]:
            del run_manager
            _keys, _flags, resolved = self._run_sync_group(
                batch_inputs,
                config,
                **call_kwargs,
            )
            return [resolved[key] for key in _keys]

        return self._batch_with_config(
            _execute,
            inputs,
            config,
            return_exceptions=return_exceptions,
            **kwargs,
        )

    @override
    async def abatch(
        self,
        inputs: list[Input],
        config: RunnableConfig | list[RunnableConfig] | None = None,
        *,
        return_exceptions: bool = False,
        **kwargs: Any,
    ) -> list[Output]:
        """Coalesce each async batch item and return results in input order.

        Args:
            inputs: Inputs to run. Equal values share one execution.
            config: Config or per-input configs. Not part of the coalescing key.
            return_exceptions: Return exceptions in place of raising them.
            **kwargs: Extra arguments forwarded to each leader execution.

        Returns:
            Outputs aligned with `inputs`.
        """
        if not inputs:
            return []

        async def _execute(
            batch_inputs: list[Input],
            run_manager: list[AsyncCallbackManagerForChainRun],
            config: list[RunnableConfig],
            **call_kwargs: Any,
        ) -> list[Output | Exception]:
            del run_manager
            keys = [_coalesce_key(item) for item in batch_inputs]
            leaders: dict[Any, int] = {}
            for index, key in enumerate(keys):
                if await self._backend.aregister(key):
                    leaders[key] = index  # noqa: PERF403

            async def run_leader(index: int) -> Any:
                key = keys[index]
                try:
                    result = await self.bound.ainvoke(
                        batch_inputs[index],
                        config[index],
                        **call_kwargs,
                    )
                except Exception as exc:
                    await self._backend.acomplete(key, error=exc)
                    return exc
                except BaseException as exc:
                    await self._backend.acomplete(key, error=exc)
                    raise
                await self._backend.acomplete(key, result=_value_payload(result))
                return result

            raw_limit = config[0].get("max_concurrency") if config else None
            semaphore = (
                asyncio.Semaphore(raw_limit)
                if isinstance(raw_limit, int) and raw_limit > 0
                else None
            )

            async def guarded(index: int) -> Any:
                if semaphore is None:
                    return await run_leader(index)
                async with semaphore:
                    return await run_leader(index)

            outcomes: dict[Any, Any] = {}
            if leaders:
                pairs = list(leaders.items())
                gathered = await asyncio.gather(*(guarded(index) for _, index in pairs))
                for (key, _), result in zip(pairs, gathered, strict=True):
                    outcomes[key] = result

            resolved: dict[Any, Any] = {}
            for key in keys:
                if key in resolved:
                    continue
                if key in outcomes:
                    resolved[key] = outcomes[key]
                    continue
                try:
                    resolved[key] = _payload_value(await self._backend.ajoin(key))
                except Exception as exc:
                    resolved[key] = exc
            return [resolved[key] for key in keys]

        return await self._abatch_with_config(
            _execute,
            inputs,
            config,
            return_exceptions=return_exceptions,
            **kwargs,
        )

    def _yield_group(
        self,
        key: Any,
        indices: list[int],
        inputs: Sequence[Input],
        configs: list[RunnableConfig],
        flags: list[bool],
        leaders: dict[Any, int],
        outcome: Any,
        failed: BaseException | None,
        *,
        return_exceptions: bool,
        kwargs: dict[str, Any],
    ) -> Iterator[tuple[int, Output | Exception]]:
        if failed is not None and not return_exceptions:
            raise failed
        for index in indices:
            if flags[index] and leaders.get(key) == index:
                yield (
                    index,
                    cast(
                        "Output | Exception",
                        failed if failed is not None else outcome,
                    ),
                )
                continue

            def _join(
                input_: Input,
                run_manager: CallbackManagerForChainRun,
                config: RunnableConfig,
                **call_kwargs: Any,
            ) -> Output:
                del input_, run_manager, config, call_kwargs
                return cast("Output", _payload_value(self._backend.join(key)))

            try:
                value: Output | Exception = self._call_with_config(
                    _join,
                    inputs[index],
                    configs[index],
                    **kwargs,
                )
            except Exception as exc:
                if not return_exceptions:
                    raise
                value = exc
            yield index, value

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
        """Coalesce batch items and yield duplicate inputs consecutively.

        Args:
            inputs: Inputs to run. Equal values share one execution.
            config: Config or per-input configs. Not part of the coalescing key.
            return_exceptions: Yield exceptions instead of raising them.
            **kwargs: Extra arguments forwarded to each leader execution.

        Yields:
            `(index, output)` pairs. Indices that share an input are adjacent.
        """
        if not inputs:
            return
        config_list = get_config_list(config, len(inputs))
        keys, flags, leaders = self._prepare_sync(inputs)
        groups: dict[Any, list[int]] = defaultdict(list)
        for index, key in enumerate(keys):
            groups[key].append(index)
        call_kwargs = dict(kwargs)

        def run_key(key: Any) -> Any:
            index = leaders.get(key)
            if index is None:
                return _payload_value(self._backend.join(key))

            def _call(
                input_: Input,
                run_manager: CallbackManagerForChainRun,
                config: RunnableConfig,
                **inner: Any,
            ) -> Output:
                del run_manager
                try:
                    result = self.bound.invoke(input_, config, **inner)
                except BaseException as exc:
                    self._backend.complete(key, error=exc)
                    raise
                self._backend.complete(key, result=_value_payload(result))
                return result

            return self._call_with_config(
                _call,
                inputs[index],
                config_list[index],
                **call_kwargs,
            )

        max_workers = _pool_size(config_list[0] if config_list else None, len(groups))
        with ThreadPoolExecutor(max_workers=max_workers) as pool:
            future_map = {}
            for key in groups:
                ctx = copy_context()
                future_map[pool.submit(ctx.run, run_key, key)] = key
            pending = set(future_map)
            try:
                while pending:
                    done, pending = wait(pending, return_when=FIRST_COMPLETED)
                    for fut in done:
                        key = future_map[fut]
                        try:
                            outcome = fut.result()
                            failed = None
                        except Exception as exc:
                            outcome = None
                            failed = exc
                        yield from self._yield_group(
                            key,
                            groups[key],
                            inputs,
                            config_list,
                            flags,
                            leaders,
                            outcome,
                            failed,
                            return_exceptions=return_exceptions,
                            kwargs=call_kwargs,
                        )
            finally:
                for fut in pending:
                    fut.cancel()

    def _prepare_sync(
        self,
        inputs: Sequence[Input],
    ) -> tuple[list[Any], list[bool], dict[Any, int]]:
        keys = [_coalesce_key(item) for item in inputs]
        flags: list[bool] = []
        leaders: dict[Any, int] = {}
        for index, key in enumerate(keys):
            is_leader = self._backend.register(key)
            flags.append(is_leader)
            if is_leader:
                leaders[key] = index
        return keys, flags, leaders

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
        """Coalesce async batch items and yield duplicate inputs consecutively.

        Args:
            inputs: Inputs to run. Equal values share one execution.
            config: Config or per-input configs. Not part of the coalescing key.
            return_exceptions: Yield exceptions instead of raising them.
            **kwargs: Extra arguments forwarded to each leader execution.

        Yields:
            `(index, output)` pairs. Indices that share an input are adjacent.
        """
        if not inputs:
            return
        config_list = get_config_list(config, len(inputs))
        keys: list[Any] = []
        flags: list[bool] = []
        leaders: dict[Any, int] = {}
        for index, item in enumerate(inputs):
            key = _coalesce_key(item)
            keys.append(key)
            is_leader = await self._backend.aregister(key)
            flags.append(is_leader)
            if is_leader:
                leaders[key] = index
        groups: dict[Any, list[int]] = defaultdict(list)
        for index, key in enumerate(keys):
            groups[key].append(index)
        call_kwargs = dict(kwargs)

        async def run_key(key: Any) -> Any:
            index = leaders.get(key)
            if index is None:
                return _payload_value(await self._backend.ajoin(key))

            async def _call(
                input_: Input,
                run_manager: AsyncCallbackManagerForChainRun,
                config: RunnableConfig,
                **inner: Any,
            ) -> Output:
                del run_manager
                try:
                    result = await self.bound.ainvoke(input_, config, **inner)
                except BaseException as exc:
                    await self._backend.acomplete(key, error=exc)
                    raise
                await self._backend.acomplete(key, result=_value_payload(result))
                return result

            return await self._acall_with_config(
                _call,
                inputs[index],
                config_list[index],
                **call_kwargs,
            )

        tasks = {asyncio.create_task(run_key(key)): key for key in groups}
        pending = set(tasks)
        try:
            while pending:
                done, pending = await asyncio.wait(
                    pending,
                    return_when=asyncio.FIRST_COMPLETED,
                )
                for task in done:
                    key = tasks[task]
                    try:
                        outcome = task.result()
                        failed = None
                    except Exception as exc:
                        outcome = None
                        failed = exc
                    if failed is not None and not return_exceptions:
                        raise failed
                    for index in groups[key]:
                        if flags[index] and leaders.get(key) == index:
                            yield (
                                index,
                                cast(
                                    "Output | Exception",
                                    failed if failed is not None else outcome,
                                ),
                            )
                            continue

                        async def _join(
                            input_: Input,
                            run_manager: AsyncCallbackManagerForChainRun,
                            config: RunnableConfig,
                            *,
                            joined_key: Any = key,
                            **inner: Any,
                        ) -> Output:
                            del input_, run_manager, config, inner
                            joined = await self._backend.ajoin(joined_key)
                            return cast("Output", _payload_value(joined))

                        try:
                            value: Output | Exception = await self._acall_with_config(
                                _join,
                                inputs[index],
                                config_list[index],
                                **call_kwargs,
                            )
                        except Exception as exc:
                            if not return_exceptions:
                                raise
                            value = exc
                        yield index, value
        finally:
            for task in pending:
                task.cancel()

    @override
    def transform(
        self,
        input: Iterator[Input],
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> Iterator[Output]:
        """Delegate to the wrapped runnable without coalescing.

        Args:
            input: Input chunks.
            config: Config forwarded unchanged.
            **kwargs: Extra arguments forwarded unchanged.

        Yields:
            Chunks from the wrapped runnable.
        """
        yield from self.bound.transform(input, config, **kwargs)

    @override
    async def atransform(
        self,
        input: AsyncIterator[Input],
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> AsyncIterator[Output]:
        """Delegate to the wrapped runnable without coalescing.

        Args:
            input: Input chunks.
            config: Config forwarded unchanged.
            **kwargs: Extra arguments forwarded unchanged.

        Yields:
            Chunks from the wrapped runnable.
        """
        async for chunk in self.bound.atransform(input, config, **kwargs):
            yield chunk

    @override
    async def astream_events(
        self,
        input: Any,
        config: RunnableConfig | None = None,
        *,
        version: str = "v2",
        include_names: Sequence[str] | None = None,
        include_types: Sequence[str] | None = None,
        include_tags: Sequence[str] | None = None,
        exclude_names: Sequence[str] | None = None,
        exclude_types: Sequence[str] | None = None,
        exclude_tags: Sequence[str] | None = None,
        **kwargs: Any,
    ) -> AsyncIterator[Any]:
        """Delegate event streaming without coalescing.

        Args:
            input: Input forwarded to the wrapped runnable.
            config: Config forwarded unchanged.
            version: Event schema version.
            include_names: Names to include.
            include_types: Types to include.
            include_tags: Tags to include.
            exclude_names: Names to exclude.
            exclude_types: Types to exclude.
            exclude_tags: Tags to exclude.
            **kwargs: Extra arguments forwarded unchanged.

        Yields:
            Events from the wrapped runnable.
        """
        async for event in self.bound.astream_events(
            input,
            config,
            version=version,  # type: ignore[arg-type]
            include_names=include_names,
            include_types=include_types,
            include_tags=include_tags,
            exclude_names=exclude_names,
            exclude_types=exclude_types,
            exclude_tags=exclude_tags,
            **kwargs,
        ):
            yield event

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
    ) -> AsyncIterator[Any]:
        """Delegate log streaming without coalescing.

        Args:
            input: Input forwarded to the wrapped runnable.
            config: Config forwarded unchanged.
            diff: Whether to stream log diffs.
            with_streamed_output_list: Whether to include streamed output.
            include_names: Names to include.
            include_types: Types to include.
            include_tags: Tags to include.
            exclude_names: Names to exclude.
            exclude_types: Types to exclude.
            exclude_tags: Tags to exclude.
            **kwargs: Extra arguments forwarded unchanged.

        Yields:
            Log entries from the wrapped runnable.
        """
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
