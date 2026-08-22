"""High-level access to Photoshop layer blend ranges."""

from __future__ import annotations

from typing import Any, Callable, Iterable

import numpy as np
from attrs import define, field
from PIL import Image


Range = tuple[int, int]


def _validate_range(instance: Any, attribute: Any, value: Range) -> None:
    if len(value) != 2 or any(not 0 <= int(x) <= 255 for x in value):
        raise ValueError(f"{attribute.name} must contain two values in [0, 255]")


@define
class BlendRangeChannel:
    """The four split-slider ranges for one blend-if channel."""

    this_layer_black: Range = field(factory=lambda: (0, 0), validator=_validate_range)
    this_layer_white: Range = field(
        factory=lambda: (255, 255), validator=_validate_range
    )
    underlying_black: Range = field(factory=lambda: (0, 0), validator=_validate_range)
    underlying_white: Range = field(
        factory=lambda: (255, 255), validator=_validate_range
    )
    _on_change: Callable[[], None] | None = field(
        default=None, init=False, repr=False, eq=False
    )

    def __attrs_post_init__(self) -> None:
        for attribute in (
            "this_layer_black",
            "this_layer_white",
            "underlying_black",
            "underlying_white",
        ):
            value = getattr(self, attribute)
            setattr(self, attribute, (int(value[0]), int(value[1])))

    def __setattr__(self, name: str, value: Any) -> None:
        super().__setattr__(name, value)
        if name != "_on_change" and hasattr(self, "_on_change"):
            callback = self._on_change
            if callback is not None:
                callback()

    @classmethod
    def from_raw(cls, raw_pair: list[tuple[int, int]]) -> BlendRangeChannel:
        if len(raw_pair) != 2 or any(len(pair) != 2 for pair in raw_pair):
            raise ValueError("A blend range channel must contain exactly two pairs")

        def decode(value: int) -> Range:
            return value & 0xFF, (value >> 8) & 0xFF

        return cls(
            this_layer_black=decode(raw_pair[0][0]),
            this_layer_white=decode(raw_pair[0][1]),
            underlying_black=decode(raw_pair[1][0]),
            underlying_white=decode(raw_pair[1][1]),
        )

    @classmethod
    def default(cls) -> BlendRangeChannel:
        return cls()

    @classmethod
    def from_values(
        cls,
        this_layer_black: int = 0,
        this_layer_white: int = 255,
        underlying_black: int = 0,
        underlying_white: int = 255,
    ) -> BlendRangeChannel:
        return cls(
            (this_layer_black, this_layer_black),
            (this_layer_white, this_layer_white),
            (underlying_black, underlying_black),
            (underlying_white, underlying_white),
        )

    def to_raw(self) -> list[tuple[int, int]]:
        def encode(value: Range) -> int:
            return int(value[0]) | (int(value[1]) << 8)

        return [
            (encode(self.this_layer_black), encode(self.this_layer_white)),
            (encode(self.underlying_black), encode(self.underlying_white)),
        ]

    @property
    def is_default(self) -> bool:
        return (
            self.this_layer_black == (0, 0)
            and self.this_layer_white == (255, 255)
            and self.underlying_black == (0, 0)
            and self.underlying_white == (255, 255)
        )

    @property
    def this_layer_black_split(self) -> bool:
        return self.this_layer_black[0] != self.this_layer_black[1]

    @property
    def this_layer_white_split(self) -> bool:
        return self.this_layer_white[0] != self.this_layer_white[1]

    @property
    def underlying_black_split(self) -> bool:
        return self.underlying_black[0] != self.underlying_black[1]

    @property
    def underlying_white_split(self) -> bool:
        return self.underlying_white[0] != self.underlying_white[1]

    def describe(self) -> str:
        return (
            f"This Layer black={self.this_layer_black}, white={self.this_layer_white}; "
            f"Underlying Layer black={self.underlying_black}, "
            f"white={self.underlying_white}"
        )


@define
class BlendRanges:
    """Composite and per-channel blend-if ranges."""

    composite: BlendRangeChannel = field(factory=BlendRangeChannel.default)
    channels: list[BlendRangeChannel] = field(factory=list)
    _raw: Any = field(default=None, init=False, repr=False, eq=False)

    def __attrs_post_init__(self) -> None:
        self._bind_channels()

    def _bind_channels(self) -> None:
        for channel in [self.composite, *self.channels]:
            channel._on_change = self._sync_raw

    def _sync_raw(self) -> None:
        if self._raw is not None:
            self.apply_to_raw(self._raw)

    @property
    def channel_count(self) -> int:
        return len(self.channels)

    def __len__(self) -> int:
        return len(self.channels)

    def __getitem__(self, index: int) -> BlendRangeChannel:
        return self.channels[index]

    def __iter__(self) -> Iterable[BlendRangeChannel]:
        return iter(self.channels)

    @classmethod
    def from_raw(cls, raw_blending_ranges: Any) -> BlendRanges:
        if (
            raw_blending_ranges is None
            or raw_blending_ranges.composite_ranges is None
            or raw_blending_ranges.channel_ranges is None
        ):
            result = cls()
            if raw_blending_ranges is not None:
                result._raw = raw_blending_ranges
            return result
        result = cls(
            BlendRangeChannel.from_raw(raw_blending_ranges.composite_ranges[0:2]),
            [
                BlendRangeChannel.from_raw(channel)
                for channel in raw_blending_ranges.channel_ranges
            ],
        )
        result._raw = raw_blending_ranges
        return result

    @classmethod
    def from_channels(
        cls, composite: BlendRangeChannel, channels: list[BlendRangeChannel]
    ) -> BlendRanges:
        return cls(composite, channels)

    def apply_to_raw(self, raw: Any) -> None:
        raw.composite_ranges = self.composite.to_raw()
        raw.channel_ranges = [channel.to_raw() for channel in self.channels]
        self._raw = raw

    @property
    def is_default(self) -> bool:
        return self.composite.is_default and all(
            channel.is_default for channel in self.channels
        )

    def describe(self) -> str:
        return f"Composite: {self.composite.describe()}; {len(self)} channel ranges"

    @staticmethod
    def _ramp(values: np.ndarray, handles: Range, ascending: bool) -> np.ndarray:
        left, right = (x / 255.0 for x in handles)
        if left == right:
            return (values >= left if ascending else values <= right).astype(np.float32)
        if ascending:
            return np.clip((values - left) / (right - left), 0.0, 1.0)
        return np.clip((right - values) / (right - left), 0.0, 1.0)

    @classmethod
    def _weight(cls, source: np.ndarray, backdrop: np.ndarray, channel: BlendRangeChannel):
        weight = cls._ramp(source, channel.this_layer_black, True)
        weight = np.minimum(weight, cls._ramp(source, channel.this_layer_white, False))
        weight = np.minimum(
            weight, cls._ramp(backdrop, channel.underlying_black, True)
        )
        return np.minimum(
            weight, cls._ramp(backdrop, channel.underlying_white, False)
        )

    def compute_visibility(
        self, source_color: np.ndarray, backdrop_color: np.ndarray
    ) -> np.ndarray:
        source = np.asarray(source_color, dtype=np.float32)
        backdrop = np.asarray(backdrop_color, dtype=np.float32)
        if source.ndim == 2:
            source = source[:, :, None]
        if backdrop.ndim == 2:
            backdrop = backdrop[:, :, None]

        if source.shape[2] >= 3:
            source_composite = np.dot(source[:, :, :3], (0.299, 0.587, 0.114))
            backdrop_composite = np.dot(
                backdrop[:, :, :3], (0.299, 0.587, 0.114)
            )
        else:
            source_composite = source[:, :, 0]
            backdrop_composite = backdrop[:, :, 0]
        weight = self._weight(source_composite, backdrop_composite, self.composite)
        for index, channel in enumerate(self.channels):
            if index >= source.shape[2] or index >= backdrop.shape[2]:
                break
            weight = np.minimum(
                weight,
                self._weight(source[:, :, index], backdrop[:, :, index], channel),
            )
        return np.clip(weight[:, :, None], 0.0, 1.0)

    def to_pil_mask(
        self, source_color: np.ndarray, backdrop_color: np.ndarray
    ) -> Image.Image:
        return Image.fromarray(
            np.rint(self.compute_visibility(source_color, backdrop_color)[:, :, 0] * 255)
            .astype(np.uint8),
            "L",
        )
