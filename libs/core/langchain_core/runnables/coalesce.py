"""Request coalescing for concurrent `Runnable` calls."""

from __future__ import annotations

import asyncio
import queue
import threading
from abc import ABC, abstractmethod
from collections import deque
from collections.abc import AsyncIterator, Hashable, Iterator, Mapping, Sequence
from typing import Any, NamedTuple, cast

from typing_extensions import override

from langchain_core.runnables.base import Runnable
from langchain_core.runnables.config import (
    RunnableConfig,
    get_config_list,
    get_executor_for_config,
)
from langchain_core.runnables.utils import Input, Output


class CoalesceStats(NamedTuple):
    """Counts for a coalesce backend.

    Attributes:
        active: Executions that have not finished.
        coalesced: Callers that joined an in-flight execution.
        total: Callers that registered, including leaders and joiners.
    """

    active: int
    coalesced: int
    total: int


class _Slot:
    """One in-flight execution and the joiners waiting on it."""

    def __init__(self) -> None:
        self.event = threading.Event()
        self.remaining = 0
        self.done = False
        self.result: Any = None
        self.error: BaseException | None = None
        self.futures: list[asyncio.Future[Any]] = []


class _Outcome:
    """Result produced by a leader, shared with every joiner."""

    def __init__(self, kind: str, value: Any) -> None:
        self.kind = kind
        self.value = value


class CoalesceBackend(ABC):
    """Store of in-flight calls keyed by an opaque coalescing key.

    `register` returns `True` for the caller that should execute and `False` for
    callers that should wait. `join` waits for that execution. `complete` publishes
    its result or error. A finished key accepts a new execution on the next
    `register`.
    """

    @abstractmethod
    def register(self, key: Hashable) -> bool:
        """Reserve `key` or join the execution that already holds it.

        Args:
            key: Opaque coalescing key.

        Returns:
            `True` if the caller owns the execution, `False` if it must wait.
        """

    @abstractmethod
    def join(self, key: Hashable) -> Any:
        """Wait for the in-flight execution of `key`.

        Args:
            key: Opaque coalescing key previously passed to `register`.

        Returns:
            The value passed to `complete`.

        Raises:
            BaseException: The error passed to `complete`, or
                `asyncio.CancelledError` when the backend is cleared.
        """

    @abstractmethod
    def complete(
        self,
        key: Hashable,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        """Publish the outcome of the in-flight execution of `key`.

        Args:
            key: Opaque coalescing key.
            result: Value received by joiners when `error` is omitted.
            error: Failure received by joiners. Waiters are cancelled with
                `asyncio.CancelledError` when that error is used.
        """

    @abstractmethod
    def is_active(self, key: Hashable) -> bool:
        """Return whether `key` has an execution that has not finished.

        Args:
            key: Opaque coalescing key.

        Returns:
            `True` when an execution of `key` is still running.
        """

    @property
    @abstractmethod
    def stats(self) -> CoalesceStats:
        """Current coalescing counts."""

    async def aregister(self, key: Hashable) -> bool:
        """Async counterpart of `register`.

        Args:
            key: Opaque coalescing key.

        Returns:
            `True` if the caller owns the execution, `False` if it must wait.
        """
        return self.register(key)

    async def ajoin(self, key: Hashable) -> Any:
        """Async counterpart of `join`.

        Args:
            key: Opaque coalescing key previously passed to `register`.

        Returns:
            The value passed to `complete`.
        """
        return await asyncio.to_thread(self.join, key)

    async def acomplete(
        self,
        key: Hashable,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        """Async counterpart of `complete`.

        Args:
            key: Opaque coalescing key.
            result: Value received by joiners when `error` is omitted.
            error: Failure received by joiners.
        """
        await asyncio.to_thread(self.complete, key, result=result, error=error)

    async def ais_active(self, key: Hashable) -> bool:
        """Async counterpart of `is_active`.

        Args:
            key: Opaque coalescing key.

        Returns:
            `True` when an execution of `key` is still running.
        """
        return self.is_active(key)

    def clear(self) -> None:
        """Cancel waiters and reset stats.

        Raises:
            NotImplementedError: When this backend cannot drop in-flight state.
        """
        msg = "coalesce_clear is not supported by this backend"
        raise NotImplementedError(msg)


class InMemoryCoalesceBackend(CoalesceBackend):
    """Thread-safe process-local coalesce backend.

    Sync and async callers share the same in-flight map. Completed executions are
    removed so the next `register` for that key starts a new one. Joiners that
    registered before completion still receive the finished outcome.
    """

    def __init__(self) -> None:
        """Create an empty in-memory backend."""
        self._lock = threading.Lock()
        self._slots: dict[Hashable, deque[_Slot]] = {}
        self._coalesced = 0
        self._total = 0

    @override
    def register(self, key: Hashable) -> bool:
        with self._lock:
            self._total += 1
            generations = self._slots.setdefault(key, deque())
            current = generations[-1] if generations else None
            if current is not None and not current.done:
                current.remaining += 1
                self._coalesced += 1
                return False
            generations.append(_Slot())
            return True

    def _slot_for_join(self, key: Hashable) -> _Slot:
        generations = self._slots.get(key)
        if not generations:
            raise asyncio.CancelledError
        for slot in generations:
            if slot.remaining > 0:
                return slot
        raise asyncio.CancelledError

    def _discard_if_finished(self, key: Hashable, slot: _Slot) -> None:
        if not slot.done or slot.remaining > 0:
            return
        generations = self._slots.get(key)
        if generations is None or slot not in generations:
            return
        generations.remove(slot)
        if not generations:
            del self._slots[key]

    @override
    def join(self, key: Hashable) -> Any:
        with self._lock:
            slot = self._slot_for_join(key)
            if slot.done:
                error = slot.error
                result = slot.result
                slot.remaining -= 1
                self._discard_if_finished(key, slot)
                if error is not None:
                    raise error
                return result
        slot.event.wait()
        with self._lock:
            error = slot.error
            result = slot.result
            slot.remaining -= 1
            self._discard_if_finished(key, slot)
        if error is not None:
            raise error
        return result

    def _resolve_future(
        self,
        future: asyncio.Future[Any],
        result: Any,
        error: BaseException | None,
    ) -> None:
        if future.done():
            return
        if error is not None:
            future.set_exception(error)
        else:
            future.set_result(result)

    def _wake(
        self,
        slot: _Slot,
        *,
        result: Any,
        error: BaseException | None,
    ) -> None:
        futures = list(slot.futures)
        slot.futures.clear()
        for future in futures:
            loop = future.get_loop()
            try:
                running = asyncio.get_running_loop()
            except RuntimeError:
                running = None
            delivered_error = error
            if running is loop:
                self._resolve_future(future, result, delivered_error)
            else:
                loop.call_soon_threadsafe(
                    self._resolve_future, future, result, delivered_error
                )

    @override
    def complete(
        self,
        key: Hashable,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        with self._lock:
            generations = self._slots.get(key)
            if not generations:
                return
            slot = next((item for item in generations if not item.done), None)
            if slot is None:
                return
            slot.result = result
            slot.error = error
            slot.done = True
            slot.event.set()
            if slot.remaining == 0:
                self._discard_if_finished(key, slot)
        self._wake(slot, result=result, error=error)

    @override
    def is_active(self, key: Hashable) -> bool:
        with self._lock:
            generations = self._slots.get(key)
            if not generations:
                return False
            return any(not slot.done for slot in generations)

    @property
    @override
    def stats(self) -> CoalesceStats:
        with self._lock:
            active = sum(
                1
                for generations in self._slots.values()
                for slot in generations
                if not slot.done
            )
            return CoalesceStats(
                active=active,
                coalesced=self._coalesced,
                total=self._total,
            )

    @override
    async def ajoin(self, key: Hashable) -> Any:
        loop = asyncio.get_running_loop()
        future: asyncio.Future[Any] = loop.create_future()
        with self._lock:
            slot = self._slot_for_join(key)
            if slot.done:
                error = slot.error
                result = slot.result
                slot.remaining -= 1
                self._discard_if_finished(key, slot)
                if error is not None:
                    raise error
                return result
            slot.futures.append(future)
        try:
            return await future
        finally:
            with self._lock:
                slot.remaining -= 1
                self._discard_if_finished(key, slot)

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
    def clear(self) -> None:
        with self._lock:
            slots = [
                slot for generations in self._slots.values() for slot in generations
            ]
            for slot in slots:
                slot.done = True
                slot.error = asyncio.CancelledError()
                slot.event.set()
            self._slots.clear()
            self._coalesced = 0
            self._total = 0
        for slot in slots:
            self._wake(slot, result=None, error=asyncio.CancelledError())


def _freeze(value: Any) -> Hashable:
    """Build a hashable identity that ignores mapping key order."""
    if isinstance(value, Mapping):
        items = tuple(
            sorted(
                ((_freeze(key), _freeze(item)) for key, item in value.items()),
                key=lambda pair: repr(pair[0]),
            )
        )
        return ("mapping", items)
    if isinstance(value, (list, tuple)):
        return (type(value).__name__, tuple(_freeze(item) for item in value))
    if isinstance(value, (set, frozenset)):
        frozen = tuple(sorted((_freeze(item) for item in value), key=repr))
        return (type(value).__name__, frozen)
    if isinstance(value, (str, int, float, bool, bytes, type(None))):
        return ("scalar", type(value).__name__, value)
    dump = getattr(value, "model_dump", None)
    if callable(dump):
        return ("model", type(value).__qualname__, _freeze(dump()))
    try:
        hash(value)
    except TypeError:
        return ("repr", type(value).__qualname__, repr(value))
    return ("atom", type(value).__qualname__, value)


def coalesce_key(value: Any) -> Hashable:
    """Return the coalescing key for an input value.

    Args:
        value: Runnable input. Config and kwargs are not part of the key.

    Returns:
        A hashable identity. Two mappings with the same pairs match even when
        their keys were inserted in different orders.
    """
    return _freeze(value)


def _as_value(outcome: _Outcome) -> Any:
    if outcome.kind == "value":
        return outcome.value
    chunks = list(outcome.value)
    if not chunks:
        return None
    reduced = chunks[0]
    for chunk in chunks[1:]:
        try:
            reduced = reduced + chunk
        except TypeError:
            reduced = chunk
    return reduced


def _as_chunks(outcome: _Outcome) -> list[Any]:
    if outcome.kind == "chunks":
        return list(outcome.value)
    return [outcome.value]


class RunnableWithCoalesce(Runnable[Input, Output]):
    """`Runnable` that shares one in-flight execution per input value."""

    def __init__(
        self, *, bound: Runnable[Input, Output], backend: CoalesceBackend
    ) -> None:
        """Create a coalescing wrapper.

        Args:
            bound: Runnable that performs the real work.
            backend: In-flight state shared by every coalesced method.
        """
        self.bound = bound
        self.backend = backend

    def coalesce_info(self) -> CoalesceStats:
        """Return counts for the shared backend.

        Returns:
            Active executions, joined callers, and total registrations.
        """
        return self.backend.stats

    def coalesce_clear(self) -> None:
        """Cancel waiters with `asyncio.CancelledError` and reset backend stats."""
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

    @property
    @override
    def config_specs(self) -> list[Any]:
        return self.bound.config_specs

    @override
    def get_input_schema(self, config: RunnableConfig | None = None) -> type[Any]:
        return self.bound.get_input_schema(config)

    @override
    def get_output_schema(self, config: RunnableConfig | None = None) -> type[Any]:
        return self.bound.get_output_schema(config)

    @override
    def get_graph(self, config: RunnableConfig | None = None) -> Any:
        return self.bound.get_graph(config)

    def _join_value(self, key: Hashable) -> Output:
        outcome = cast("_Outcome", self.backend.join(key))
        return cast("Output", _as_value(outcome))

    async def _ajoin_value(self, key: Hashable) -> Output:
        outcome = cast("_Outcome", await self.backend.ajoin(key))
        return cast("Output", _as_value(outcome))

    @override
    def invoke(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> Output:
        key = coalesce_key(input)
        if self.backend.register(key):
            try:
                result = self.bound.invoke(input, config, **kwargs)
            except BaseException as exc:
                self.backend.complete(key, error=exc)
                raise
            self.backend.complete(key, result=_Outcome("value", result))
            return result
        return self._call_with_config(
            lambda _input: self._join_value(key), input, config
        )

    @override
    async def ainvoke(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> Output:
        key = coalesce_key(input)
        if await self.backend.aregister(key):
            try:
                result = await self.bound.ainvoke(input, config, **kwargs)
            except BaseException as exc:
                await self.backend.acomplete(key, error=exc)
                raise
            await self.backend.acomplete(key, result=_Outcome("value", result))
            return result

        async def _wait(_input: Input) -> Output:
            return await self._ajoin_value(key)

        return await self._acall_with_config(_wait, input, config)

    def _lead_stream(
        self,
        key: Hashable,
        input: Input,
        config: RunnableConfig | None,
        kwargs: dict[str, Any],
    ) -> Iterator[Output]:
        chunks: list[Output] = []
        try:
            for chunk in self.bound.stream(input, config, **kwargs):
                chunks.append(chunk)
                yield chunk
        except GeneratorExit:
            self.backend.complete(key, result=_Outcome("chunks", list(chunks)))
            raise
        except BaseException as exc:
            self.backend.complete(key, error=exc)
            raise
        else:
            self.backend.complete(key, result=_Outcome("chunks", list(chunks)))

    async def _alead_stream(
        self,
        key: Hashable,
        input: Input,
        config: RunnableConfig | None,
        kwargs: dict[str, Any],
    ) -> AsyncIterator[Output]:
        chunks: list[Output] = []
        try:
            async for chunk in self.bound.astream(input, config, **kwargs):
                chunks.append(chunk)
                yield chunk
        except GeneratorExit:
            await self.backend.acomplete(key, result=_Outcome("chunks", list(chunks)))
            raise
        except BaseException as exc:
            await self.backend.acomplete(key, error=exc)
            raise
        else:
            await self.backend.acomplete(key, result=_Outcome("chunks", list(chunks)))

    def _join_stream(
        self,
        key: Hashable,
        input: Input,
        config: RunnableConfig | None,
    ) -> Iterator[Output]:
        def _replay(_inputs: Iterator[Input]) -> Iterator[Output]:
            outcome = cast("_Outcome", self.backend.join(key))
            yield from cast("list[Output]", _as_chunks(outcome))

        yield from self._transform_stream_with_config(iter([input]), _replay, config)

    async def _ajoin_stream(
        self,
        key: Hashable,
        input: Input,
        config: RunnableConfig | None,
    ) -> AsyncIterator[Output]:
        async def _replay(_inputs: AsyncIterator[Input]) -> AsyncIterator[Output]:
            outcome = cast("_Outcome", await self.backend.ajoin(key))
            for chunk in _as_chunks(outcome):
                yield cast("Output", chunk)

        async for chunk in self._atransform_stream_with_config(
            _async_iter([input]), _replay, config
        ):
            yield chunk

    @override
    def stream(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> Iterator[Output]:
        key = coalesce_key(input)
        if self.backend.register(key):
            yield from self._lead_stream(key, input, config, kwargs)
            return
        yield from self._join_stream(key, input, config)

    @override
    async def astream(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> AsyncIterator[Output]:
        key = coalesce_key(input)
        if await self.backend.aregister(key):
            async for chunk in self._alead_stream(key, input, config, kwargs):
                yield chunk
            return
        async for chunk in self._ajoin_stream(key, input, config):
            yield chunk

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
        async for chunk in self.bound.astream_log(
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
        keys = [coalesce_key(item) for item in inputs]
        is_leader = [self.backend.register(key) for key in keys]
        cached: dict[Hashable, Output | BaseException] = {}

        def run_leader(index: int) -> None:
            key = keys[index]
            try:
                result = self.bound.invoke(inputs[index], configs[index], **kwargs)
            except BaseException as exc:
                cached[key] = exc
                self.backend.complete(key, error=exc)
                return
            cached[key] = result
            self.backend.complete(key, result=_Outcome("value", result))

        leader_indices = [index for index, leader in enumerate(is_leader) if leader]
        if len(leader_indices) == 1:
            run_leader(leader_indices[0])
        elif leader_indices:
            with get_executor_for_config(configs[0]) as executor:
                futures = [
                    executor.submit(run_leader, index) for index in leader_indices
                ]
                for future in futures:
                    future.result()

        outputs: list[Output] = []
        first_error: BaseException | None = None
        for index, leader in enumerate(is_leader):
            if leader:
                value: Output | BaseException = cached[keys[index]]
            else:
                try:
                    value = self._call_with_config(
                        lambda _item, key=keys[index]: self._join_value(key),
                        inputs[index],
                        configs[index],
                    )
                except BaseException as exc:
                    value = exc
            if isinstance(value, BaseException):
                first_error = first_error or value
                if return_exceptions and isinstance(value, Exception):
                    outputs.append(cast("Output", value))
                    continue
                if return_exceptions:
                    raise value
                continue
            outputs.append(value)
        if first_error is not None and not return_exceptions:
            raise first_error
        return outputs

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
        keys = [coalesce_key(item) for item in inputs]
        is_leader = [await self.backend.aregister(key) for key in keys]
        cached: dict[Hashable, Output | BaseException] = {}

        async def run_leader(index: int) -> None:
            key = keys[index]
            try:
                result = await self.bound.ainvoke(
                    inputs[index], configs[index], **kwargs
                )
            except BaseException as exc:
                cached[key] = exc
                await self.backend.acomplete(key, error=exc)
                return
            cached[key] = result
            await self.backend.acomplete(key, result=_Outcome("value", result))

        await asyncio.gather(
            *(run_leader(index) for index, leader in enumerate(is_leader) if leader)
        )

        outputs: list[Output] = []
        first_error: BaseException | None = None
        for index, leader in enumerate(is_leader):
            if leader:
                value: Output | BaseException = cached[keys[index]]
            else:

                async def _wait(_item: Input, key: Hashable = keys[index]) -> Output:
                    return await self._ajoin_value(key)

                try:
                    value = await self._acall_with_config(
                        _wait, inputs[index], configs[index]
                    )
                except BaseException as exc:
                    value = exc
            if isinstance(value, BaseException):
                first_error = first_error or value
                if return_exceptions and isinstance(value, Exception):
                    outputs.append(cast("Output", value))
                    continue
                if return_exceptions:
                    raise value
                continue
            outputs.append(value)
        if first_error is not None and not return_exceptions:
            raise first_error
        return outputs

    @override
    def batch_as_completed(
        self,
        inputs: Sequence[Input],
        config: RunnableConfig | Sequence[RunnableConfig] | None = None,
        *,
        return_exceptions: bool = False,
        **kwargs: Any,
    ) -> Iterator[tuple[int, Output | Exception]]:
        if not inputs:
            return
        input_list = list(inputs)
        configs = get_config_list(config, len(input_list))
        keys = [coalesce_key(item) for item in input_list]
        groups: dict[Hashable, list[int]] = {}
        is_leader = [False] * len(input_list)
        for index, key in enumerate(keys):
            groups.setdefault(key, []).append(index)
            if self.backend.register(key):
                is_leader[index] = True

        cached: dict[Hashable, Output | BaseException] = {}
        done: queue.Queue[Hashable] = queue.Queue()
        leader_keys = {keys[index] for index, leader in enumerate(is_leader) if leader}
        external_keys = [key for key in groups if key not in leader_keys]

        def run_leader(index: int) -> None:
            key = keys[index]
            try:
                result = self.bound.invoke(input_list[index], configs[index], **kwargs)
            except BaseException as exc:
                cached[key] = exc
                self.backend.complete(key, error=exc)
            else:
                cached[key] = result
                self.backend.complete(key, result=_Outcome("value", result))
            done.put(key)

        def wait_external(key: Hashable) -> None:
            while self.backend.is_active(key):
                threading.Event().wait(0.005)
            done.put(key)

        leader_indices = [index for index, leader in enumerate(is_leader) if leader]
        with get_executor_for_config(configs[0] if configs else None) as executor:
            for index in leader_indices:
                executor.submit(run_leader, index)
            for key in external_keys:
                executor.submit(wait_external, key)
            pending = set(leader_keys) | set(external_keys)
            while pending:
                key = done.get()
                if key not in pending:
                    continue
                pending.discard(key)
                for index in groups[key]:
                    yield from self._yield_completed(
                        index=index,
                        key=key,
                        leader=is_leader[index],
                        inputs=input_list,
                        configs=configs,
                        cached=cached,
                        return_exceptions=return_exceptions,
                    )

    @override
    async def abatch_as_completed(
        self,
        inputs: Sequence[Input],
        config: RunnableConfig | Sequence[RunnableConfig] | None = None,
        *,
        return_exceptions: bool = False,
        **kwargs: Any,
    ) -> AsyncIterator[tuple[int, Output | Exception]]:
        if not inputs:
            return
        input_list = list(inputs)
        configs = get_config_list(config, len(input_list))
        keys = [coalesce_key(item) for item in input_list]
        groups: dict[Hashable, list[int]] = {}
        is_leader = [False] * len(input_list)
        for index, key in enumerate(keys):
            groups.setdefault(key, []).append(index)
            if await self.backend.aregister(key):
                is_leader[index] = True

        cached: dict[Hashable, Output | BaseException] = {}
        done: asyncio.Queue[Hashable] = asyncio.Queue()
        leader_keys = {keys[index] for index, leader in enumerate(is_leader) if leader}
        external_keys = [key for key in groups if key not in leader_keys]

        async def run_leader(index: int) -> None:
            key = keys[index]
            try:
                result = await self.bound.ainvoke(
                    input_list[index], configs[index], **kwargs
                )
            except BaseException as exc:
                cached[key] = exc
                await self.backend.acomplete(key, error=exc)
            else:
                cached[key] = result
                await self.backend.acomplete(key, result=_Outcome("value", result))
            await done.put(key)

        async def wait_external(key: Hashable) -> None:
            while await self.backend.ais_active(key):  # noqa: ASYNC110
                await asyncio.sleep(0.005)
            await done.put(key)

        tasks = [
            asyncio.create_task(run_leader(index))
            for index, leader in enumerate(is_leader)
            if leader
        ]
        tasks.extend(asyncio.create_task(wait_external(key)) for key in external_keys)
        pending = set(leader_keys) | set(external_keys)
        try:
            while pending:
                key = await done.get()
                if key not in pending:
                    continue
                pending.discard(key)
                for index in groups[key]:
                    if is_leader[index]:
                        value: Output | BaseException = cached[key]
                        if isinstance(value, Exception) and not return_exceptions:
                            raise value
                        yield index, cast("Output | Exception", value)
                        continue
                    try:

                        async def _wait(
                            _item: Input, item_key: Hashable = key
                        ) -> Output:
                            return await self._ajoin_value(item_key)

                        output = await self._acall_with_config(
                            _wait, input_list[index], configs[index]
                        )
                    except Exception as exc:
                        if not return_exceptions:
                            raise
                        yield index, exc
                    else:
                        yield index, output
        finally:
            for task in tasks:
                task.cancel()

    def _yield_completed(
        self,
        *,
        index: int,
        key: Hashable,
        leader: bool,
        inputs: list[Input],
        configs: list[RunnableConfig],
        cached: dict[Hashable, Output | BaseException],
        return_exceptions: bool,
    ) -> Iterator[tuple[int, Output | Exception]]:
        if leader:
            value = cached[key]
            if isinstance(value, Exception) and not return_exceptions:
                raise value
            yield index, cast("Output | Exception", value)
            return
        try:
            output = self._call_with_config(
                lambda _item, item_key=key: self._join_value(item_key),
                inputs[index],
                configs[index],
            )
        except Exception as exc:
            if not return_exceptions:
                raise
            yield index, exc
            return
        yield index, output


async def _async_iter(items: list[Input]) -> AsyncIterator[Input]:
    for item in items:
        yield item
