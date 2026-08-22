"""High-level support for Photoshop's Blend If sliders."""

from __future__ import annotations

from collections.abc import Iterator, Sequence

import numpy as np
from attrs import define, field
from PIL import Image

from psd_tools.psd.layer_and_mask import LayerBlendingRanges


Range = tuple[int, int]


def _range(value: Range) -> Range:
    if len(value) != 2 or not all(0 <= x <= 255 for x in value):
        raise ValueError("Blend range handles must be two values in [0, 255]")
    return (int(value[0]), int(value[1]))


@define
class BlendRangeChannel:
    this_layer_black: Range = field(default=(0, 255), converter=_range)
    this_layer_white: Range = field(default=(0, 255), converter=_range)
    underlying_black: Range = field(default=(0, 255), converter=_range)
    underlying_white: Range = field(default=(0, 255), converter=_range)

    @classmethod
    def from_raw(cls, raw_pair: Sequence[Sequence[int]]) -> BlendRangeChannel:
        if len(raw_pair) != 2 or any(len(x) != 2 for x in raw_pair):
            raise ValueError("A blend range must contain exactly two pairs")
        values = [(x[0] & 255, x[0] >> 8, x[1] & 255, x[1] >> 8) for x in raw_pair]
        return cls(values[0][0:2], values[1][0:2], values[0][2:4], values[1][2:4])

    def to_raw(self) -> list[tuple[int, int]]:
        return [
            (self.this_layer_black[0] | self.this_layer_black[1] << 8,
             self.this_layer_white[0] | self.this_layer_white[1] << 8),
            (self.underlying_black[0] | self.underlying_black[1] << 8,
             self.underlying_white[0] | self.underlying_white[1] << 8),
        ]

    @classmethod
    def default(cls) -> BlendRangeChannel:
        return cls()

    @classmethod
    def from_values(
        cls, this_layer_black: Range = (0, 255), this_layer_white: Range = (0, 255),
        underlying_black: Range = (0, 255), underlying_white: Range = (0, 255),
    ) -> BlendRangeChannel:
        return cls(this_layer_black, this_layer_white, underlying_black, underlying_white)

    @property
    def is_default(self) -> bool:
        return all(x == (0, 255) for x in (
            self.this_layer_black, self.this_layer_white,
            self.underlying_black, self.underlying_white))

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
        return f"BlendRangeChannel(this={self.this_layer_black}/{self.this_layer_white}, under={self.underlying_black}/{self.underlying_white})"


@define
class BlendRanges:
    composite: BlendRangeChannel = field(factory=BlendRangeChannel.default)
    channels: list[BlendRangeChannel] = field(factory=list)

    @property
    def channel_count(self) -> int:
        return len(self.channels)

    def __len__(self) -> int:
        return len(self.channels)

    def __getitem__(self, index: int) -> BlendRangeChannel:
        return self.channels[index]

    def __iter__(self) -> Iterator[BlendRangeChannel]:
        return iter(self.channels)

    @classmethod
    def from_raw(cls, raw_blending_ranges: LayerBlendingRanges) -> BlendRanges:
        if raw_blending_ranges.composite_ranges is None:
            return cls()
        return cls(
            BlendRangeChannel.from_raw(raw_blending_ranges.composite_ranges),
            [BlendRangeChannel.from_raw(x) for x in (raw_blending_ranges.channel_ranges or [])],
        )

    @classmethod
    def from_channels(cls, composite: BlendRangeChannel, channels: list[BlendRangeChannel]) -> BlendRanges:
        return cls(composite, channels)

    def apply_to_raw(self, raw: LayerBlendingRanges) -> None:
        raw.composite_ranges = self.composite.to_raw()
        raw.channel_ranges = [x.to_raw() for x in self.channels]

    @property
    def is_default(self) -> bool:
        return self.composite.is_default and all(x.is_default for x in self.channels)

    def describe(self) -> str:
        return f"BlendRanges(composite={self.composite.describe()}, channels={len(self.channels)})"

    def compute_visibility(self, source_color: np.ndarray, backdrop_color: np.ndarray) -> np.ndarray:
        result = np.ones(source_color.shape[:2] + (1,), dtype=np.float32)
        source_luma = np.sum(source_color[..., :3] * (0.299, 0.587, 0.114), axis=2, keepdims=True)
        backdrop_luma = np.sum(backdrop_color[..., :3] * (0.299, 0.587, 0.114), axis=2, keepdims=True)
        for channel, source, backdrop in zip(
            [self.composite, *self.channels],
            [source_luma, *[source_color[..., i:i+1] for i in range(len(self.channels))]],
            [backdrop_luma, *[backdrop_color[..., i:i+1] for i in range(len(self.channels))]],
        ):
            source_weight = _slider_weight(source, channel.this_layer_black, channel.this_layer_white)
            under_weight = _slider_weight(backdrop, channel.underlying_black, channel.underlying_white)
            result *= np.minimum(source_weight, under_weight)
        return np.clip(result, 0, 1)

    def to_pil_mask(self, source_color: np.ndarray, backdrop_color: np.ndarray) -> Image.Image:
        return Image.fromarray((self.compute_visibility(source_color, backdrop_color)[..., 0] * 255).astype(np.uint8), "L")


def _slider_weight(value: np.ndarray, black: Range, white: Range) -> np.ndarray:
    value = np.asarray(value)
    low = np.where(value < black[1], (value - black[0]) / max(black[1] - black[0], 1), 1.0)
    high = np.where(value > white[0], (white[1] - value) / max(white[1] - white[0], 1), 1.0)
    return np.clip(np.minimum(low, high), 0, 1)
