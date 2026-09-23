"""Request coalescing for `Runnable` objects.

Concurrent calls with an identical input share a single execution of the
underlying `Runnable`; every caller receives the same result (or error).
"""

from __future__ import annotations

import asyncio
import json
import threading
from abc import ABC, abstractmethod
from collections.abc import AsyncIterator, Iterator, Sequence
from concurrent.futures import as_completed
from dataclasses import dataclass, field
from functools import reduce
from typing import TYPE_CHECKING, Any, NamedTuple, cast

from pydantic import ConfigDict, Field
from typing_extensions import override

from langchain_core.runnables.base import RunnableBindingBase
from langchain_core.runnables.config import (
    get_executor_for_config,
    patch_config,
)
from langchain_core.runnables.utils import Input, Output

if TYPE_CHECKING:
    from langchain_core.callbacks.manager import (
        AsyncCallbackManagerForChainRun,
        CallbackManagerForChainRun,
    )
    from langchain_core.runnables.config import RunnableConfig

__all__ = [
    "CoalesceBackend",
    "CoalesceStats",
    "InMemoryCoalesceBackend",
]


class CoalesceStats(NamedTuple):
    """Snapshot of coalescing statistics."""

    active: int
    """Number of executions currently in flight."""
    coalesced: int
    """Number of calls that joined an in-flight execution."""
    total: int
    """Total number of registered calls."""


class CoalesceBackend(ABC):
    """Tracks in-flight executions keyed by a coalescing key.

    A caller first calls `register`. If it returns `True`, the caller owns the
    execution and must eventually call `complete`. Otherwise the caller must call
    `join` to wait for the owner's result.
    """

    @abstractmethod
    def register(self, key: str) -> bool:
        """Register a call for `key`.

        Args:
            key: The coalescing key.

        Returns:
            `True` if the caller owns a new execution, `False` if it joined one.
        """

    @abstractmethod
    def join(self, key: str) -> Any:
        """Block until the execution for `key` completes.

        Args:
            key: The coalescing key.

        Returns:
            The owner's result.

        Raises:
            BaseException: The owner's error, if it failed.
        """

    @abstractmethod
    def complete(
        self,
        key: str,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        """Mark the execution for `key` as finished and release all joiners.

        Args:
            key: The coalescing key.
            result: The execution result.
            error: The execution error, if it failed.
        """

    @abstractmethod
    def is_active(self, key: str) -> bool:
        """Return whether an execution for `key` is in flight.

        Args:
            key: The coalescing key.
        """

    @property
    @abstractmethod
    def stats(self) -> CoalesceStats:
        """Current coalescing statistics."""

    def clear(self) -> None:
        """Cancel all waiters and reset statistics."""
        msg = f"{type(self).__name__} does not support clear()."
        raise NotImplementedError(msg)

    async def aregister(self, key: str) -> bool:
        """Async version of `register`."""
        return self.register(key)

    async def ajoin(self, key: str) -> Any:
        """Async version of `join`."""
        return await asyncio.get_running_loop().run_in_executor(None, self.join, key)

    async def acomplete(
        self,
        key: str,
        *,
        result: Any = None,
        error: BaseException | None = None,
    ) -> None:
        """Async version of `complete`."""
        self.complete(key, result=result, error=error)

    async def ais_active(self, key: str) -> bool:
        """Async version of `is_active`."""
        return self.is_active(key)


@dataclass
class _Entry:
    event: threading.Event = field(default_factory=threading.Event)
    pending_joiners: int = 0
    result: Any = None
    error: BaseException | None = None
    futures: list[tuple[asyncio.AbstractEventLoop, asyncio.Future[None]]] = field(
        default_factory=list
    )


def _resolve_future(future: asyncio.Future[None], cancel: bool) -> None:  # noqa: FBT001
    if future.done():
        return
    if cancel:
        future.cancel()
    else:
        future.set_result(None)


class InMemoryCoalesceBackend(CoalesceBackend):
    """Thread-safe, process-local `CoalesceBackend`.

    Works across threads and event loops, so sync and async callers can join the
    same execution.
    """

    def __init__(self) -> None:
        """Create an empty backend."""
        self._lock = threading.Lock()
        self._active: dict[str, _Entry] = {}
        # Completed entries that registered joiners have not collected yet.
        self._draining: dict[str, list[_Entry]] = {}
        self._coalesced = 0
        self._total = 0

    @override
    def register(self, key: str) -> bool:
        with self._lock:
            self._total += 1
            entry = self._active.get(key)
            if entry is None:
                self._active[key] = _Entry()
                return True
            entry.pending_joiners += 1
            self._coalesced += 1
            return False

    def _claim(self, key: str) -> _Entry:
        with self._lock:
            draining = self._draining.get(key)
            if draining:
                entry = draining[0]
                entry.pending_joiners -= 1
                if entry.pending_joiners <= 0:
                    draining.pop(0)
                    if not draining:
                        del self._draining[key]
                return entry
            active = self._active.get(key)
            if active is None:
                msg = f"No in-flight execution for key {key!r}."
                raise KeyError(msg)
            active.pending_joiners -= 1
            return active

    @staticmethod
    def _outcome(entry: _Entry) -> Any:
        if entry.error is not None:
            raise entry.error
        return entry.result

    @override
    def join(self, key: str) -> Any:
        entry = self._claim(key)
        entry.event.wait()
        return self._outcome(entry)

    @override
    async def ajoin(self, key: str) -> Any:
        loop = asyncio.get_running_loop()
        future: asyncio.Future[None] = loop.create_future()
        entry = self._claim(key)
        with self._lock:
            done = entry.event.is_set()
            if not done:
                entry.futures.append((loop, future))
        if not done:
            await future
        return self._outcome(entry)

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
            entry.result = result
            entry.error = error
            if entry.pending_joiners > 0:
                self._draining.setdefault(key, []).append(entry)
            entry.event.set()
            futures, entry.futures = entry.futures, []
        for loop, future in futures:
            loop.call_soon_threadsafe(_resolve_future, future, False)  # noqa: FBT003

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
            entries = list(self._active.values())
            for draining in self._draining.values():
                entries.extend(draining)
            self._active.clear()
            self._draining.clear()
            self._coalesced = 0
            self._total = 0
            futures = []
            for entry in entries:
                if not entry.event.is_set():
                    entry.error = asyncio.CancelledError()
                    entry.event.set()
                futures.extend(entry.futures)
                entry.futures = []
        for loop, future in futures:
            loop.call_soon_threadsafe(_resolve_future, future, True)  # noqa: FBT003


class _StreamResult(NamedTuple):
    chunks: list[Any]


def _json_default(obj: Any) -> Any:
    if hasattr(obj, "model_dump"):
        return {"__type__": type(obj).__qualname__, **obj.model_dump()}
    if isinstance(obj, (set, frozenset)):
        return sorted(repr(item) for item in obj)
    if isinstance(obj, tuple):
        return list(obj)
    return repr(obj)


def _make_key(input_: Any) -> str:
    try:
        return json.dumps(input_, sort_keys=True, default=_json_default)
    except (TypeError, ValueError):
        return repr(input_)


def _as_value(outcome: Any) -> Any:
    if isinstance(outcome, _StreamResult):
        if not outcome.chunks:
            return None
        try:
            return reduce(lambda a, b: a + b, outcome.chunks)
        except TypeError:
            return outcome.chunks[-1]
    return outcome


def _as_chunks(outcome: Any) -> list[Any]:
    if isinstance(outcome, _StreamResult):
        return outcome.chunks
    return [outcome]


class RunnableCoalesce(RunnableBindingBase[Input, Output]):  # type: ignore[no-redef]
    """Wrap a `Runnable` so concurrent identical inputs share one execution."""

    backend: CoalesceBackend = Field(default_factory=InMemoryCoalesceBackend)
    """Backend holding in-flight state."""

    model_config = ConfigDict(arbitrary_types_allowed=True)

    def coalesce_info(self) -> CoalesceStats:
        """Return the current coalescing statistics."""
        return self.backend.stats

    def coalesce_clear(self) -> None:
        """Cancel all waiters with `asyncio.CancelledError` and reset statistics."""
        self.backend.clear()

    def _child_config(self, config: RunnableConfig, run_manager: Any) -> RunnableConfig:
        return patch_config(config, callbacks=run_manager.get_child())

    def _invoke(
        self,
        input_: Input,
        run_manager: CallbackManagerForChainRun,
        config: RunnableConfig,
        *,
        coalesce_owner: bool | None = None,
        **kwargs: Any,
    ) -> Output:
        key = _make_key(input_)
        owner = self.backend.register(key) if coalesce_owner is None else coalesce_owner
        if not owner:
            return cast("Output", _as_value(self.backend.join(key)))
        try:
            result = self.bound.invoke(
                input_,
                self._child_config(config, run_manager),
                **{**self.kwargs, **kwargs},
            )
        except BaseException as e:
            self.backend.complete(key, error=e)
            raise
        self.backend.complete(key, result=result)
        return result

    async def _ainvoke(
        self,
        input_: Input,
        run_manager: AsyncCallbackManagerForChainRun,
        config: RunnableConfig,
        *,
        coalesce_owner: bool | None = None,
        **kwargs: Any,
    ) -> Output:
        key = _make_key(input_)
        owner = (
            await self.backend.aregister(key)
            if coalesce_owner is None
            else coalesce_owner
        )
        if not owner:
            return cast("Output", _as_value(await self.backend.ajoin(key)))
        try:
            result = await self.bound.ainvoke(
                input_,
                self._child_config(config, run_manager),
                **{**self.kwargs, **kwargs},
            )
        except BaseException as e:
            await self.backend.acomplete(key, error=e)
            raise
        await self.backend.acomplete(key, result=result)
        return result

    @override
    def invoke(
        self, input: Input, config: RunnableConfig | None = None, **kwargs: Any
    ) -> Output:
        return self._call_with_config(
            self._invoke, input, self._merge_configs(config), **kwargs
        )

    @override
    async def ainvoke(
        self, input: Input, config: RunnableConfig | None = None, **kwargs: Any
    ) -> Output:
        return await self._acall_with_config(
            self._ainvoke, input, self._merge_configs(config), **kwargs
        )

    def _stream(
        self,
        inputs: Iterator[Input],
        run_manager: CallbackManagerForChainRun,
        config: RunnableConfig,
        **kwargs: Any,
    ) -> Iterator[Output]:
        input_ = next(inputs)
        key = _make_key(input_)
        if not self.backend.register(key):
            yield from _as_chunks(self.backend.join(key))
            return
        chunks: list[Any] = []
        try:
            for chunk in self.bound.stream(
                input_,
                self._child_config(config, run_manager),
                **{**self.kwargs, **kwargs},
            ):
                chunks.append(chunk)
                yield chunk
        except BaseException as e:
            self.backend.complete(key, error=e)
            raise
        self.backend.complete(key, result=_StreamResult(chunks))

    async def _astream(
        self,
        inputs: AsyncIterator[Input],
        run_manager: AsyncCallbackManagerForChainRun,
        config: RunnableConfig,
        **kwargs: Any,
    ) -> AsyncIterator[Output]:
        input_ = await anext(inputs)
        key = _make_key(input_)
        if not await self.backend.aregister(key):
            for chunk in _as_chunks(await self.backend.ajoin(key)):
                yield chunk
            return
        chunks: list[Any] = []
        try:
            async for chunk in self.bound.astream(
                input_,
                self._child_config(config, run_manager),
                **{**self.kwargs, **kwargs},
            ):
                chunks.append(chunk)
                yield chunk
        except BaseException as e:
            await self.backend.acomplete(key, error=e)
            raise
        await self.backend.acomplete(key, result=_StreamResult(chunks))

    @override
    def stream(
        self, input: Input, config: RunnableConfig | None = None, **kwargs: Any
    ) -> Iterator[Output]:
        yield from self._transform_stream_with_config(
            iter([input]), self._stream, self._merge_configs(config), **kwargs
        )

    @override
    async def astream(
        self, input: Input, config: RunnableConfig | None = None, **kwargs: Any
    ) -> AsyncIterator[Output]:
        async def input_aiter() -> AsyncIterator[Input]:
            yield input

        async for chunk in self._atransform_stream_with_config(
            input_aiter(), self._astream, self._merge_configs(config), **kwargs
        ):
            yield chunk

    @staticmethod
    def _configs(config: Any, n: int) -> list[RunnableConfig | None]:
        if isinstance(config, Sequence):
            return list(config)
        return [config] * n

    async def _aregister_all(self, inputs: Sequence[Input]) -> list[bool]:
        return [await self.backend.aregister(_make_key(i)) for i in inputs]

    def _run_item(
        self,
        input_: Input,
        config: RunnableConfig | None,
        *,
        owner: bool,
        return_exceptions: bool,
        **kwargs: Any,
    ) -> Any:
        try:
            return self._call_with_config(
                self._invoke,
                input_,
                self._merge_configs(config),
                coalesce_owner=owner,
                **kwargs,
            )
        except Exception as e:
            if return_exceptions:
                return e
            raise

    async def _arun_item(
        self,
        input_: Input,
        config: RunnableConfig | None,
        *,
        owner: bool,
        return_exceptions: bool,
        **kwargs: Any,
    ) -> Any:
        try:
            return await self._acall_with_config(
                self._ainvoke,
                input_,
                self._merge_configs(config),
                coalesce_owner=owner,
                **kwargs,
            )
        except Exception as e:
            if return_exceptions:
                return e
            raise

    @staticmethod
    def _owners_first(owners: list[bool]) -> list[int]:
        return sorted(range(len(owners)), key=lambda idx: not owners[idx])

    def _submit_all(
        self,
        executor: Any,
        inputs: Sequence[Input],
        configs: list[RunnableConfig | None],
        *,
        return_exceptions: bool,
        **kwargs: Any,
    ) -> dict[Any, int]:
        owners = [self.backend.register(_make_key(i)) for i in inputs]
        # Owners are submitted first so joiners never starve them of workers.
        return {
            executor.submit(
                self._run_item,
                inputs[idx],
                configs[idx],
                owner=owners[idx],
                return_exceptions=return_exceptions,
                **kwargs,
            ): idx
            for idx in self._owners_first(owners)
        }

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
        output: list[Any] = [None] * len(inputs)
        with get_executor_for_config(configs[0]) as executor:
            futures = self._submit_all(
                executor,
                inputs,
                configs,
                return_exceptions=return_exceptions,
                **kwargs,
            )
            for future, idx in futures.items():
                output[idx] = future.result()
        return output

    @override
    def batch_as_completed(  # type: ignore[override]
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
        groups: dict[str, list[int]] = {}
        for idx, input_ in enumerate(inputs):
            groups.setdefault(_make_key(input_), []).append(idx)
        with get_executor_for_config(configs[0]) as executor:
            futures = self._submit_all(
                executor,
                inputs,
                configs,
                return_exceptions=return_exceptions,
                **kwargs,
            )
            by_idx = {idx: future for future, idx in futures.items()}
            for future in as_completed(futures):
                group = groups.pop(_make_key(inputs[futures[future]]), None)
                if group is None:
                    continue
                for idx in group:
                    yield idx, by_idx[idx].result()

    async def _agather(
        self,
        inputs: Sequence[Input],
        configs: list[RunnableConfig | None],
        *,
        return_exceptions: bool,
        **kwargs: Any,
    ) -> dict[int, asyncio.Task[Any]]:
        owners = await self._aregister_all(inputs)
        max_concurrency = (configs[0] or {}).get("max_concurrency")
        semaphore = asyncio.Semaphore(max_concurrency) if max_concurrency else None

        async def run(idx: int) -> Any:
            coro = self._arun_item(
                inputs[idx],
                configs[idx],
                owner=owners[idx],
                return_exceptions=return_exceptions,
                **kwargs,
            )
            if semaphore is None:
                return await coro
            async with semaphore:
                return await coro

        return {
            idx: asyncio.ensure_future(run(idx)) for idx in self._owners_first(owners)
        }

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
        tasks = await self._agather(
            inputs, configs, return_exceptions=return_exceptions, **kwargs
        )
        try:
            await asyncio.gather(*tasks.values())
        finally:
            for task in tasks.values():
                task.cancel()
        return [tasks[idx].result() for idx in range(len(inputs))]

    @override
    async def abatch_as_completed(  # type: ignore[override]
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
        groups: dict[str, list[int]] = {}
        for idx, input_ in enumerate(inputs):
            groups.setdefault(_make_key(input_), []).append(idx)
        tasks = await self._agather(
            inputs, configs, return_exceptions=return_exceptions, **kwargs
        )
        idx_of = {task: idx for idx, task in tasks.items()}
        pending = set(tasks.values())
        try:
            while pending:
                done, pending = await asyncio.wait(
                    pending, return_when=asyncio.FIRST_COMPLETED
                )
                for task in sorted(done, key=lambda t: idx_of[t]):
                    group = groups.pop(_make_key(inputs[idx_of[task]]), None)
                    if group is None:
                        continue
                    for idx in group:
                        yield idx, await tasks[idx]
        finally:
            for task in tasks.values():
                task.cancel()
