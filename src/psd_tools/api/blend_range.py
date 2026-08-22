"""
Blend range ("Blend If") API.

Layers store blend range data as raw uint16 tuples in the PSD file. This module
provides typed access to those values and helpers for compositing.
"""

from __future__ import annotations

from collections.abc import Iterator
from typing import TYPE_CHECKING

import numpy as np
from PIL import Image

if TYPE_CHECKING:
    from psd_tools.psd.layer_and_mask import LayerBlendingRanges

_LUMINOSITY = np.array([0.299, 0.587, 0.114], dtype=np.float32)


def _decode_uint16(value: int) -> tuple[int, int]:
    value &= 0xFFFF
    return (value & 0xFF, (value >> 8) & 0xFF)


def _encode_uint16(left: int, right: int) -> int:
    return (int(left) & 0xFF) | ((int(right) & 0xFF) << 8)


def _channel_weight(
    values: np.ndarray, left: int, right: int, *, high: bool = False
) -> np.ndarray:
    scale = values * 255.0
    left = float(left)
    right = float(right)
    if left == right:
        if high:
            return np.where(scale <= left, 1.0, 0.0).astype(np.float32)
        return np.where(scale >= right, 1.0, 0.0).astype(np.float32)
    if high:
        return np.clip((right - scale) / (right - left), 0.0, 1.0).astype(np.float32)
    return np.clip((scale - left) / (right - left), 0.0, 1.0).astype(np.float32)


class BlendRangeChannel:
    """Blend range settings for one composite or color channel."""

    __slots__ = (
        "this_layer_black",
        "this_layer_white",
        "underlying_black",
        "underlying_white",
    )

    def __init__(
        self,
        this_layer_black: tuple[int, int] = (0, 0),
        this_layer_white: tuple[int, int] = (255, 255),
        underlying_black: tuple[int, int] = (0, 0),
        underlying_white: tuple[int, int] = (255, 255),
    ) -> None:
        self.this_layer_black = this_layer_black
        self.this_layer_white = this_layer_white
        self.underlying_black = underlying_black
        self.underlying_white = underlying_white

    @classmethod
    def default(cls) -> BlendRangeChannel:
        """Return a channel with the default full-range slider positions."""
        return cls.from_values()

    @classmethod
    def from_values(
        cls,
        this_layer_black: tuple[int, int] = (0, 0),
        this_layer_white: tuple[int, int] = (255, 255),
        underlying_black: tuple[int, int] = (0, 0),
        underlying_white: tuple[int, int] = (255, 255),
    ) -> BlendRangeChannel:
        """Create a channel without split sliders."""
        return cls(
            this_layer_black=this_layer_black,
            this_layer_white=this_layer_white,
            underlying_black=underlying_black,
            underlying_white=underlying_white,
        )

    @classmethod
    def from_raw(cls, raw_pair: list[tuple[int, int]]) -> BlendRangeChannel:
        """Parse a raw blend range pair list from ``LayerBlendingRanges``."""
        this_layer = raw_pair[0]
        underlying = raw_pair[1]
        return cls(
            this_layer_black=_decode_uint16(this_layer[0]),
            this_layer_white=_decode_uint16(this_layer[1]),
            underlying_black=_decode_uint16(underlying[0]),
            underlying_white=_decode_uint16(underlying[1]),
        )

    def to_raw(self) -> list[tuple[int, int]]:
        """Convert back to raw uint16 tuple pairs."""
        return [
            (
                _encode_uint16(*self.this_layer_black),
                _encode_uint16(*self.this_layer_white),
            ),
            (
                _encode_uint16(*self.underlying_black),
                _encode_uint16(*self.underlying_white),
            ),
        ]

    @property
    def is_default(self) -> bool:
        """Return True when all slider handles are at default positions."""
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
        """Return a human-readable summary of the channel settings."""
        parts = [
            f"this layer black={self.this_layer_black}",
            f"white={self.this_layer_white}",
            f"underlying black={self.underlying_black}",
            f"white={self.underlying_white}",
        ]
        return ", ".join(parts)

    def compute_visibility(
        self, source: np.ndarray, backdrop: np.ndarray
    ) -> np.ndarray:
        """Compute visibility weight for a single channel or luminosity plane."""
        source = np.asarray(source, dtype=np.float32)
        backdrop = np.asarray(backdrop, dtype=np.float32)
        if source.ndim == 2:
            source = source[..., np.newaxis]
        if backdrop.ndim == 2:
            backdrop = backdrop[..., np.newaxis]

        weight = _channel_weight(
            source, self.this_layer_black[0], self.this_layer_black[1]
        )
        weight *= _channel_weight(
            source,
            self.this_layer_white[0],
            self.this_layer_white[1],
            high=True,
        )
        weight *= _channel_weight(
            backdrop, self.underlying_black[0], self.underlying_black[1]
        )
        weight *= _channel_weight(
            backdrop,
            self.underlying_white[0],
            self.underlying_white[1],
            high=True,
        )
        return weight


class BlendRanges:
    """Blend range settings for a layer."""

    __slots__ = ("channels", "composite")

    def __init__(
        self,
        composite: BlendRangeChannel | None = None,
        channels: list[BlendRangeChannel] | None = None,
    ) -> None:
        self.composite = composite if composite is not None else BlendRangeChannel.default()
        self.channels = list(channels) if channels is not None else []

    @classmethod
    def from_raw(cls, raw_blending_ranges: LayerBlendingRanges) -> BlendRanges:
        """Create from a low-level ``LayerBlendingRanges`` object."""
        if (
            raw_blending_ranges.composite_ranges is None
            and raw_blending_ranges.channel_ranges is None
        ):
            return cls(composite=BlendRangeChannel.default(), channels=[])

        composite = BlendRangeChannel.default()
        if raw_blending_ranges.composite_ranges is not None:
            composite = BlendRangeChannel.from_raw(raw_blending_ranges.composite_ranges)

        channels: list[BlendRangeChannel] = []
        if raw_blending_ranges.channel_ranges is not None:
            channels = [
                BlendRangeChannel.from_raw(channel)
                for channel in raw_blending_ranges.channel_ranges
            ]
        return cls(composite=composite, channels=channels)

    @classmethod
    def from_channels(
        cls,
        composite: BlendRangeChannel,
        channels: list[BlendRangeChannel],
    ) -> BlendRanges:
        """Create from explicit channel objects."""
        return cls(composite=composite, channels=list(channels))

    def apply_to_raw(self, raw: LayerBlendingRanges) -> None:
        """Write these settings back to a ``LayerBlendingRanges`` object."""
        raw.composite_ranges = self.composite.to_raw()
        raw.channel_ranges = [channel.to_raw() for channel in self.channels]

    @property
    def channel_count(self) -> int:
        return len(self.channels)

    def __len__(self) -> int:
        return len(self.channels)

    def __iter__(self) -> Iterator[BlendRangeChannel]:
        return iter(self.channels)

    def __getitem__(self, index: int) -> BlendRangeChannel:
        return self.channels[index]

    @property
    def is_default(self) -> bool:
        """Return True when composite and all channels are at default positions."""
        if not self.composite.is_default:
            return False
        return all(channel.is_default for channel in self.channels)

    def describe(self) -> str:
        """Return a human-readable summary of all blend ranges."""
        parts = [f"composite: {self.composite.describe()}"]
        for index, channel in enumerate(self.channels):
            parts.append(f"channel {index}: {channel.describe()}")
        return "; ".join(parts)

    def compute_visibility(
        self,
        source_color: np.ndarray,
        backdrop_color: np.ndarray,
    ) -> np.ndarray:
        """
        Compute per-pixel blend-if visibility weights.

        Parameters are float arrays in ``[0, 1]`` with shape ``(H, W, C)``.
        Returns an array with shape ``(H, W, 1)`` in ``[0, 1]``.
        """
        source_color = np.asarray(source_color, dtype=np.float32)
        backdrop_color = np.asarray(backdrop_color, dtype=np.float32)
        if self.is_default:
            return np.ones(
                (source_color.shape[0], source_color.shape[1], 1),
                dtype=np.float32,
            )

        if source_color.ndim == 2:
            source_color = source_color[..., np.newaxis]
        if backdrop_color.ndim == 2:
            backdrop_color = backdrop_color[..., np.newaxis]

        if source_color.shape[2] == 1:
            source_luma = source_color[..., 0:1]
            backdrop_luma = backdrop_color[..., 0:1]
        else:
            source_luma = source_color @ _LUMINOSITY[: source_color.shape[2]]
            backdrop_luma = backdrop_color @ _LUMINOSITY[: backdrop_color.shape[2]]
        weight = self.composite.compute_visibility(
            source_luma[..., np.newaxis], backdrop_luma[..., np.newaxis]
        )

        channel_count = min(len(self.channels), source_color.shape[2])
        for index in range(channel_count):
            channel = self.channels[index]
            channel_weight = channel.compute_visibility(
                source_color[..., index : index + 1],
                backdrop_color[..., index : index + 1],
            )
            weight *= channel_weight

        return np.clip(weight, 0.0, 1.0)

    def to_pil_mask(
        self,
        source_color: np.ndarray,
        backdrop_color: np.ndarray,
    ) -> Image.Image:
        """Convert visibility weights to a PIL ``'L'`` mode image."""
        weight = self.compute_visibility(source_color, backdrop_color)
        return Image.fromarray((weight[..., 0] * 255.0).astype(np.uint8), "L")
