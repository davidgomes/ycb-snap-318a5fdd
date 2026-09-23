"""Blend If (layer blending ranges) API."""

from __future__ import annotations

from typing import Iterator, Sequence

import numpy as np
from PIL import Image

from psd_tools.psd.layer_and_mask import LayerBlendingRanges

Handle = tuple[int, int]
RawPair = tuple[int, int]

_DEFAULT_BLACK: Handle = (0, 0)
_DEFAULT_WHITE: Handle = (255, 255)
_LUMA = (0.299, 0.587, 0.114)


def _pack_handle(handle: Handle) -> int:
    left, right = int(handle[0]), int(handle[1])
    return (left & 0xFF) | ((right & 0xFF) << 8)


def _unpack_handle(value: int) -> Handle:
    return (int(value) & 0xFF, (int(value) >> 8) & 0xFF)


def _as_handle(value: int | Handle) -> Handle:
    if isinstance(value, int):
        return (value, value)
    return (int(value[0]), int(value[1]))


class BlendRangeChannel:
    """One Blend If channel: this layer and the underlying layer.

    Each slider is a ``(left_handle, right_handle)`` pair in 0-255. Equal
    handles are a hard step; a split fades linearly between them.
    """

    def __init__(
        self,
        this_layer_black: Handle = _DEFAULT_BLACK,
        this_layer_white: Handle = _DEFAULT_WHITE,
        underlying_black: Handle = _DEFAULT_BLACK,
        underlying_white: Handle = _DEFAULT_WHITE,
    ) -> None:
        self.this_layer_black = _as_handle(this_layer_black)
        self.this_layer_white = _as_handle(this_layer_white)
        self.underlying_black = _as_handle(underlying_black)
        self.underlying_white = _as_handle(underlying_white)

    @classmethod
    def default(cls) -> BlendRangeChannel:
        """Full-range channel (every value visible)."""
        return cls()

    @classmethod
    def from_values(
        cls,
        this_layer_black: int | Handle = _DEFAULT_BLACK,
        this_layer_white: int | Handle = _DEFAULT_WHITE,
        underlying_black: int | Handle = _DEFAULT_BLACK,
        underlying_white: int | Handle = _DEFAULT_WHITE,
    ) -> BlendRangeChannel:
        """Create a channel. Integer stops are stored as non-split handles."""
        return cls(
            _as_handle(this_layer_black),
            _as_handle(this_layer_white),
            _as_handle(underlying_black),
            _as_handle(underlying_white),
        )

    @classmethod
    def from_raw(cls, raw_pair: Sequence[Sequence[int]]) -> BlendRangeChannel:
        """Parse ``[(this_black, this_white), (underlying_black, underlying_white)]``.

        Each uint16 stores a split slider: low byte is the left handle, high
        byte is the right handle.
        """
        this_layer = raw_pair[0]
        underlying = raw_pair[1]
        return cls(
            _unpack_handle(this_layer[0]),
            _unpack_handle(this_layer[1]),
            _unpack_handle(underlying[0]),
            _unpack_handle(underlying[1]),
        )

    def to_raw(self) -> list[RawPair]:
        """Two ``(black_uint16, white_uint16)`` pairs."""
        return [
            (_pack_handle(self.this_layer_black), _pack_handle(self.this_layer_white)),
            (_pack_handle(self.underlying_black), _pack_handle(self.underlying_white)),
        ]

    @property
    def is_default(self) -> bool:
        return (
            self.this_layer_black == _DEFAULT_BLACK
            and self.this_layer_white == _DEFAULT_WHITE
            and self.underlying_black == _DEFAULT_BLACK
            and self.underlying_white == _DEFAULT_WHITE
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
            "BlendRangeChannel("
            f"this_layer_black={self.this_layer_black}, "
            f"this_layer_white={self.this_layer_white}, "
            f"underlying_black={self.underlying_black}, "
            f"underlying_white={self.underlying_white})"
        )

    def __repr__(self) -> str:
        return self.describe()


def _slider_weight(values: np.ndarray, black: Handle, white: Handle) -> np.ndarray:
    """Visibility in ``[0, 1]`` for values in ``[0, 1]``."""
    v = np.asarray(values, dtype=np.float32) * np.float32(255.0)
    b0, b1 = float(black[0]), float(black[1])
    w0, w1 = float(white[0]), float(white[1])

    if b0 == b1:
        black_w = np.where(v < b0, np.float32(0.0), np.float32(1.0))
    else:
        black_w = np.clip((v - b0) / (b1 - b0), 0.0, 1.0)

    if w0 == w1:
        white_w = np.where(v > w1, np.float32(0.0), np.float32(1.0))
    else:
        white_w = np.clip((w1 - v) / (w1 - w0), 0.0, 1.0)

    return (black_w * white_w).astype(np.float32, copy=False)


class BlendRanges:
    """Composite gray range plus per-channel Blend If ranges."""

    def __init__(
        self,
        composite: BlendRangeChannel,
        channels: Sequence[BlendRangeChannel] | None = None,
    ) -> None:
        self.composite = composite
        self.channels = list(channels or [])

    @classmethod
    def from_raw(cls, raw_blending_ranges: LayerBlendingRanges) -> BlendRanges:
        composite_raw = raw_blending_ranges.composite_ranges
        channel_raw = raw_blending_ranges.channel_ranges
        if composite_raw is None:
            return cls(BlendRangeChannel.default(), [])
        channels = [
            BlendRangeChannel.from_raw(channel) for channel in (channel_raw or [])
        ]
        return cls(BlendRangeChannel.from_raw(composite_raw), channels)

    @classmethod
    def from_channels(
        cls,
        composite: BlendRangeChannel,
        channels: Sequence[BlendRangeChannel],
    ) -> BlendRanges:
        return cls(composite, channels)

    def apply_to_raw(self, raw: LayerBlendingRanges) -> None:
        raw.composite_ranges = self.composite.to_raw()
        raw.channel_ranges = [channel.to_raw() for channel in self.channels]

    @property
    def channel_count(self) -> int:
        return len(self.channels)

    def __len__(self) -> int:
        return len(self.channels)

    def __getitem__(self, index: int) -> BlendRangeChannel:
        return self.channels[index]

    def __iter__(self) -> Iterator[BlendRangeChannel]:
        return iter(self.channels)

    @property
    def is_default(self) -> bool:
        return self.composite.is_default and all(
            channel.is_default for channel in self.channels
        )

    def describe(self) -> str:
        parts = ", ".join(channel.describe() for channel in self.channels)
        return f"BlendRanges(composite={self.composite.describe()}, channels=[{parts}])"

    def __repr__(self) -> str:
        return self.describe()

    def _plane(self, color: np.ndarray, index: int | None) -> np.ndarray:
        """Select a channel plane, or Rec. 601 luminosity when ``index`` is None."""
        array = np.asarray(color, dtype=np.float32)
        if array.ndim == 2:
            return array
        if index is None:
            if array.shape[-1] >= 3:
                r, g, b = array[..., 0], array[..., 1], array[..., 2]
                return (
                    np.float32(_LUMA[0]) * r
                    + np.float32(_LUMA[1]) * g
                    + np.float32(_LUMA[2]) * b
                )
            return array[..., 0]
        if index >= array.shape[-1]:
            return np.ones(array.shape[:2], dtype=np.float32)
        return array[..., index]

    def compute_visibility(
        self,
        source_color: np.ndarray,
        backdrop_color: np.ndarray,
    ) -> np.ndarray:
        """Blend If weight of shape ``(H, W, 1)`` in ``[0, 1]``.

        ``source_color`` and ``backdrop_color`` are float images in ``[0, 1]``.
        The composite range uses luminosity ``0.299 R + 0.587 G + 0.114 B``.
        Per-channel ranges use that channel. This Layer reads the source;
        Underlying Layer reads the backdrop. Factors are multiplied.
        """
        source = np.asarray(source_color, dtype=np.float32)
        backdrop = np.asarray(backdrop_color, dtype=np.float32)
        if source.ndim == 2:
            source = source[..., np.newaxis]
        if backdrop.ndim == 2:
            backdrop = backdrop[..., np.newaxis]

        weight = _slider_weight(
            self._plane(source, None),
            self.composite.this_layer_black,
            self.composite.this_layer_white,
        ) * _slider_weight(
            self._plane(backdrop, None),
            self.composite.underlying_black,
            self.composite.underlying_white,
        )
        for index, channel in enumerate(self.channels):
            if channel.is_default:
                continue
            if index >= source.shape[-1] and index >= backdrop.shape[-1]:
                continue
            weight = weight * _slider_weight(
                self._plane(source, index),
                channel.this_layer_black,
                channel.this_layer_white,
            )
            weight = weight * _slider_weight(
                self._plane(backdrop, index),
                channel.underlying_black,
                channel.underlying_white,
            )
        return np.clip(weight, 0.0, 1.0).astype(np.float32)[..., np.newaxis]

    def to_pil_mask(
        self,
        source_color: np.ndarray,
        backdrop_color: np.ndarray,
    ) -> Image.Image:
        """Visibility as an ``L`` mode image (0 hidden, 255 visible)."""
        weight = self.compute_visibility(source_color, backdrop_color)
        data = np.clip(np.round(weight[..., 0] * 255.0), 0, 255).astype(np.uint8)
        return Image.fromarray(data, mode="L")
