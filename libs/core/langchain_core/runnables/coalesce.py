"""`Runnable` that coalesces concurrent identical requests."""

from __future__ import annotations

import asyncio
import threading
from abc import ABC, abstractmethod
from collections import defaultdict
from concurrent.futures import FIRST_COMPLETED, Future, wait
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any, cast

from pydantic import ConfigDict, Field
from typing_extensions import TypedDict, override

from langchain_core.runnables.base import RunnableBindingBase
from langchain_core.runnables.config import (
    RunnableConfig,
    get_config_list,
    get_executor_for_config,
)
from langchain_core.runnables.utils import Input, Output

if TYPE_CHECKING:
    from collections.abc import AsyncIterator, Iterator, Sequence

    from langchain_core.callbacks.manager import (
        AsyncCallbackManagerForChainRun,
        CallbackManagerForChainRun,
    )


class CoalesceStats(TypedDict):
    """Statistics for request coalescing."""

    active: int
    """Number of in-flight coalesced executions."""

    coalesced: int
    """Number of callers that joined an existing in-flight execution."""

    total: int
    """Total number of register calls."""


def _freeze(value: Any) -> Any:
    """Convert an input value into a hashable coalescing key.

    Dictionary key ordering does not affect the resulting key.
    """
    if isinstance(value, dict):
        return frozenset((k, _freeze(v)) for k, v in value.items())
    if isinstance(value, list):
        return tuple(_freeze(v) for v in value)
    if isinstance(value, tuple):
        return tuple(_freeze(v) for v in value)
    if isinstance(value, set):
        return frozenset(_freeze(v) for v in value)
    return value


def make_coalesce_key(input_value: Input) -> Any:
    """Create a coalescing key from an input value."""
    return _freeze(input_value)


@dataclass
class _CoalesceEntry:
    """Internal in-flight or completed coalescing state for a key."""

    sync_event: threading.Event
    async_event: asyncio.Event
    result: Any | None = None
    error: BaseException | None = None
    chunks: list[Any] | None = None
    complete: bool = False
    cancelled: bool = False


class CoalesceBackend(ABC):
    """Backend that tracks in-flight coalesced executions."""

    @property
    @abstractmethod
    def stats(self) -> CoalesceStats:
        """Return coalescing statistics."""

    @abstractmethod
    def register(self, key: Any) -> bool:
        """Register interest in executing `key`.

        Returns:
            `True` if the caller should execute, `False` if it should join.
        """

    @abstractmethod
    def join(self, key: Any) -> None:
        """Block until the in-flight execution for `key` completes."""

    @abstractmethod
    def complete(
        self,
        key: Any,
        *,
        result: Any | None = None,
        error: BaseException | None = None,
        chunks: list[Any] | None = None,
    ) -> None:
        """Mark the in-flight execution for `key` as complete."""

    @abstractmethod
    def is_active(self, key: Any) -> bool:
        """Return whether `key` currently has an in-flight execution."""

    @abstractmethod
    async def aregister(self, key: Any) -> bool:
        """Async variant of `register`."""

    @abstractmethod
    async def ajoin(self, key: Any) -> None:
        """Async variant of `join`."""

    @abstractmethod
    async def acomplete(
        self,
        key: Any,
        *,
        result: Any | None = None,
        error: BaseException | None = None,
        chunks: list[Any] | None = None,
    ) -> None:
        """Async variant of `complete`."""

    @abstractmethod
    async def ais_active(self, key: Any) -> bool:
        """Async variant of `is_active`."""

    @abstractmethod
    def get_joined_value(
        self, key: Any
    ) -> tuple[Any | None, BaseException | None, list[Any] | None]:
        """Return the joined result, error, and stream chunks for `key`."""

    @abstractmethod
    def clear(self) -> None:
        """Cancel waiters and reset statistics."""


class InMemoryCoalesceBackend(CoalesceBackend):
    """Thread-safe in-memory coalescing backend."""

    def __init__(self) -> None:
        """Initialize an empty in-memory coalescing backend."""
        self._lock = threading.RLock()
        self._entries: dict[Any, _CoalesceEntry] = {}
        self._active = 0
        self._coalesced = 0
        self._total = 0

    @property
    @override
    def stats(self) -> CoalesceStats:
        with self._lock:
            return {
                "active": self._active,
                "coalesced": self._coalesced,
                "total": self._total,
            }

    def _get_entry(self, key: Any) -> _CoalesceEntry:
        with self._lock:
            entry = self._entries.get(key)
            if entry is None:
                msg = f"No in-flight coalesced execution for key {key!r}."
                raise KeyError(msg)
            return entry

    @override
    def register(self, key: Any) -> bool:
        with self._lock:
            self._total += 1
            entry = self._entries.get(key)
            if entry is not None and not entry.complete:
                self._coalesced += 1
                return False
            if entry is not None and entry.complete:
                del self._entries[key]
            self._entries[key] = _CoalesceEntry(
                sync_event=threading.Event(),
                async_event=asyncio.Event(),
            )
            self._active += 1
            return True

    @override
    def join(self, key: Any) -> None:
        entry = self._get_entry(key)
        while True:
            with self._lock:
                if entry.cancelled:
                    raise asyncio.CancelledError
                if entry.complete:
                    return
            entry.sync_event.wait(timeout=0.05)

    @override
    def complete(
        self,
        key: Any,
        *,
        result: Any | None = None,
        error: BaseException | None = None,
        chunks: list[Any] | None = None,
    ) -> None:
        with self._lock:
            entry = self._entries.get(key)
            if entry is None:
                return
            entry.result = result
            entry.error = error
            if chunks is not None:
                entry.chunks = chunks
            entry.complete = True
            self._active = max(0, self._active - 1)
            entry.sync_event.set()
            entry.async_event.set()

    @override
    def is_active(self, key: Any) -> bool:
        with self._lock:
            entry = self._entries.get(key)
            return entry is not None and not entry.complete

    @override
    async def aregister(self, key: Any) -> bool:
        return self.register(key)

    @override
    async def ajoin(self, key: Any) -> None:
        entry = self._get_entry(key)
        while True:
            with self._lock:
                if entry.cancelled:
                    raise asyncio.CancelledError
                if entry.complete:
                    return
            await entry.async_event.wait()

    @override
    async def acomplete(
        self,
        key: Any,
        *,
        result: Any | None = None,
        error: BaseException | None = None,
        chunks: list[Any] | None = None,
    ) -> None:
        self.complete(key, result=result, error=error, chunks=chunks)

    @override
    async def ais_active(self, key: Any) -> bool:
        return self.is_active(key)

    @override
    def get_joined_value(
        self, key: Any
    ) -> tuple[Any | None, BaseException | None, list[Any] | None]:
        with self._lock:
            entry = self._entries.get(key)
            if entry is None or not entry.complete:
                msg = f"No completed coalesced execution for key {key!r}."
                raise KeyError(msg)
            return entry.result, entry.error, entry.chunks

    @override
    def clear(self) -> None:
        with self._lock:
            for entry in self._entries.values():
                entry.cancelled = True
                entry.complete = True
                entry.sync_event.set()
                entry.async_event.set()
            self._entries.clear()
            self._active = 0
            self._coalesced = 0
            self._total = 0


class RunnableCoalesce(RunnableBindingBase[Input, Output]):  # type: ignore[no-redef]
    """Coalesce concurrent identical requests to the bound `Runnable`."""

    backend: CoalesceBackend = Field(default_factory=InMemoryCoalesceBackend)

    model_config = ConfigDict(arbitrary_types_allowed=True)

    def __init__(
        self,
        *,
        bound: RunnableBindingBase[Input, Output] | Any,
        backend: CoalesceBackend | None = None,
        kwargs: dict[str, Any] | None = None,
        config: RunnableConfig | None = None,
        **other_kwargs: Any,
    ) -> None:
        """Create a coalescing wrapper around a `Runnable`."""
        super().__init__(
            bound=bound,
            kwargs=kwargs or {},
            config=config or {},
            backend=backend or InMemoryCoalesceBackend(),
            **other_kwargs,
        )

    def coalesce_info(self) -> CoalesceStats:
        """Return coalescing statistics for this wrapper."""
        return self.backend.stats

    def coalesce_clear(self) -> None:
        """Cancel waiters and reset coalescing statistics."""
        self.backend.clear()

    @override
    def get_graph(self, config: RunnableConfig | None = None) -> Any:
        return self.bound.get_graph(self._merge_configs(config))

    def _raise_or_return(self, key: Any) -> Output:
        result, error, _chunks = self.backend.get_joined_value(key)
        if error is not None:
            raise error
        return cast("Output", result)

    def _invoke(
        self,
        input_: Input,
        run_manager: CallbackManagerForChainRun,
        config: RunnableConfig,
        **kwargs: Any,
    ) -> Output:
        del run_manager
        key = make_coalesce_key(input_)
        if self.backend.register(key):
            try:
                result = self.bound.invoke(
                    input_,
                    self._merge_configs(config),
                    **{**self.kwargs, **kwargs},
                )
            except BaseException as e:
                self.backend.complete(key, error=e)
                raise
            else:
                self.backend.complete(key, result=result)
                return result
        self.backend.join(key)
        return self._raise_or_return(key)

    @override
    def invoke(
        self, input: Input, config: RunnableConfig | None = None, **kwargs: Any
    ) -> Output:
        return self._call_with_config(self._invoke, input, config, **kwargs)

    async def _ainvoke(
        self,
        input_: Input,
        run_manager: AsyncCallbackManagerForChainRun,
        config: RunnableConfig,
        **kwargs: Any,
    ) -> Output:
        del run_manager
        key = make_coalesce_key(input_)
        if await self.backend.aregister(key):
            try:
                result = await self.bound.ainvoke(
                    input_,
                    self._merge_configs(config),
                    **{**self.kwargs, **kwargs},
                )
            except BaseException as e:
                await self.backend.acomplete(key, error=e)
                raise
            else:
                await self.backend.acomplete(key, result=result)
                return result
        await self.backend.ajoin(key)
        return self._raise_or_return(key)

    @override
    async def ainvoke(
        self, input: Input, config: RunnableConfig | None = None, **kwargs: Any
    ) -> Output:
        return await self._acall_with_config(self._ainvoke, input, config, **kwargs)

    @override
    def batch(
        self,
        inputs: list[Input],
        config: RunnableConfig | list[RunnableConfig] | None = None,
        *,
        return_exceptions: bool = False,
        **kwargs: Any,
    ) -> list[Output]:
        return super(RunnableBindingBase, self).batch(
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
        return await super(RunnableBindingBase, self).abatch(
            inputs,
            config,
            return_exceptions=return_exceptions,
            **kwargs,
        )

    @override
    def stream(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> Iterator[Output]:
        key = make_coalesce_key(input)
        if self.backend.register(key):
            chunks: list[Output] = []
            try:
                for chunk in self.bound.stream(
                    input,
                    self._merge_configs(config),
                    **{**self.kwargs, **kwargs},
                ):
                    chunks.append(chunk)
                    yield chunk
                self.backend.complete(key, chunks=cast("list[Any]", chunks))
            except BaseException as e:
                self.backend.complete(key, error=e)
                raise
        else:
            self.backend.join(key)
            _, error, replay_chunks = self.backend.get_joined_value(key)
            if error is not None:
                raise error
            yield from replay_chunks or []

    @override
    async def astream(
        self,
        input: Input,
        config: RunnableConfig | None = None,
        **kwargs: Any,
    ) -> AsyncIterator[Output]:
        key = make_coalesce_key(input)
        if await self.backend.aregister(key):
            chunks: list[Output] = []
            try:
                async for chunk in self.bound.astream(
                    input,
                    self._merge_configs(config),
                    **{**self.kwargs, **kwargs},
                ):
                    chunks.append(chunk)
                    yield chunk
                await self.backend.acomplete(key, chunks=cast("list[Any]", chunks))
            except BaseException as e:
                await self.backend.acomplete(key, error=e)
                raise
        else:
            await self.backend.ajoin(key)
            _, error, replay_chunks = self.backend.get_joined_value(key)
            if error is not None:
                raise error
            for chunk in replay_chunks or []:
                yield chunk

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

        configs = get_config_list(config, len(inputs))
        keys = [make_coalesce_key(input_) for input_ in inputs]
        key_to_indices: dict[Any, list[int]] = defaultdict(list)
        for index, key in enumerate(keys):
            key_to_indices[key].append(index)

        def invoke_group(
            key: Any, representative_index: int
        ) -> tuple[Any, int, Output | Exception]:
            input_ = inputs[representative_index]
            config_ = configs[representative_index]
            if return_exceptions:
                try:
                    out: Output | Exception = self.invoke(input_, config_, **kwargs)
                except Exception as e:
                    out = e
            else:
                out = self.invoke(input_, config_, **kwargs)
            return key, representative_index, out

        if len(inputs) == 1:
            key = keys[0]
            _, _, output = invoke_group(key, 0)
            yield 0, output
            return

        with get_executor_for_config(configs[0]) as executor:
            futures: dict[Future[tuple[Any, int, Output | Exception]], Any] = {
                executor.submit(invoke_group, key, indices[0]): key
                for key, indices in key_to_indices.items()
            }

            try:
                while futures:
                    done, pending = wait(futures, return_when=FIRST_COMPLETED)
                    futures = pending
                    for future in done:
                        key, _, output = future.result()
                        for index in key_to_indices[key]:
                            yield index, output
            finally:
                for future in futures:
                    future.cancel()

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

        configs = get_config_list(config, len(inputs))
        keys = [make_coalesce_key(input_) for input_ in inputs]
        key_to_indices: dict[Any, list[int]] = defaultdict(list)
        for index, key in enumerate(keys):
            key_to_indices[key].append(index)

        async def invoke_group(
            key: Any, representative_index: int
        ) -> tuple[Any, Output | Exception]:
            input_ = inputs[representative_index]
            config_ = configs[representative_index]
            if return_exceptions:
                try:
                    out: Output | Exception = await self.ainvoke(
                        input_, config_, **kwargs
                    )
                except Exception as e:
                    out = e
            else:
                out = await self.ainvoke(input_, config_, **kwargs)
            return key, out

        tasks = {
            asyncio.create_task(invoke_group(key, indices[0])): key
            for key, indices in key_to_indices.items()
        }

        try:
            for task in asyncio.as_completed(tasks):
                key, output = await task
                for index in key_to_indices[key]:
                    yield index, output
        finally:
            for task in tasks:
                if not task.done():
                    task.cancel()
