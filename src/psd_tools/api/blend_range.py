"""
Typed blend range (Blend-If) API.

Provides typed access to the Photoshop "Blend If" sliders that control
how a layer blends with underlying layers based on luminosity or
individual channel values.

Each blend range defines a "This Layer" and "Underlying Layer" range
with support for split sliders. When a slider is split, the blending
transitions smoothly between the two handle positions.

Example::

    layer = psd[0]
    br = layer.blend_ranges
    print(br.composite.this_layer_black)  # (0, 0) = not split
    print(br.composite.this_layer_white)  # (255, 255) = not split

    # Check if any blend-if is active
    print(br.is_default)
"""

from __future__ import annotations

import logging
from typing import TYPE_CHECKING, Iterator

import numpy as np
from attrs import define

from psd_tools.psd.layer_and_mask import LayerBlendingRanges

if TYPE_CHECKING:
    from PIL.Image import Image as PILImage

logger = logging.getLogger(__name__)


def _decode_split(value: int) -> tuple[int, int]:
    """Decode a uint16 blend range value into a (low, high) split pair."""
    return (value & 0xFF, (value >> 8) & 0xFF)


def _encode_split(pair: tuple[int, int]) -> int:
    """Encode a (low, high) split pair into a uint16 blend range value."""
    return ((pair[1] & 0xFF) << 8) | (pair[0] & 0xFF)


@define
class BlendRangeChannel:
    """Blend-If slider pair for a single channel."""

    this_layer_black: tuple[int, int]
    this_layer_white: tuple[int, int]
    underlying_black: tuple[int, int]
    underlying_white: tuple[int, int]

    @classmethod
    def from_raw(cls, raw_pair: list[tuple[int, int]]) -> BlendRangeChannel:
        """Create from a raw channel range pair."""
        this_layer = raw_pair[0]
        underlying = raw_pair[1]
        return cls(
            this_layer_black=_decode_split(this_layer[0]),
            this_layer_white=_decode_split(this_layer[1]),
            underlying_black=_decode_split(underlying[0]),
            underlying_white=_decode_split(underlying[1]),
        )

    def to_raw(self) -> list[tuple[int, int]]:
        """Convert back to the raw channel range pair format."""
        return [
            (
                _encode_split(self.this_layer_black),
                _encode_split(self.this_layer_white),
            ),
            (
                _encode_split(self.underlying_black),
                _encode_split(self.underlying_white),
            ),
        ]

    @classmethod
    def default(cls) -> BlendRangeChannel:
        """Return a default full-range channel (no blending restriction)."""
        return cls(
            this_layer_black=(0, 0),
            this_layer_white=(255, 255),
            underlying_black=(0, 0),
            underlying_white=(255, 255),
        )

    @classmethod
    def from_values(
        cls,
        this_layer_black: int = 0,
        this_layer_white: int = 255,
        underlying_black: int = 0,
        underlying_white: int = 255,
    ) -> BlendRangeChannel:
        """Create from simple (non-split) slider values."""
        return cls(
            this_layer_black=(this_layer_black, this_layer_black),
            this_layer_white=(this_layer_white, this_layer_white),
            underlying_black=(underlying_black, underlying_black),
            underlying_white=(underlying_white, underlying_white),
        )

    @property
    def is_default(self) -> bool:
        """True when the blend range has no effect (full range, no split)."""
        return (
            self.this_layer_black == (0, 0)
            and self.this_layer_white == (255, 255)
            and self.underlying_black == (0, 0)
            and self.underlying_white == (255, 255)
        )

    @property
    def this_layer_black_split(self) -> bool:
        """True when the This Layer black slider is split."""
        return self.this_layer_black[0] != self.this_layer_black[1]

    @property
    def this_layer_white_split(self) -> bool:
        """True when the This Layer white slider is split."""
        return self.this_layer_white[0] != self.this_layer_white[1]

    @property
    def underlying_black_split(self) -> bool:
        """True when the Underlying Layer black slider is split."""
        return self.underlying_black[0] != self.underlying_black[1]

    @property
    def underlying_white_split(self) -> bool:
        """True when the Underlying Layer white slider is split."""
        return self.underlying_white[0] != self.underlying_white[1]

    def describe(self) -> str:
        """Return a human-readable description of this blend range."""
        if self.is_default:
            return "BlendRangeChannel(default)"

        def _fmt(name: str, val: tuple[int, int]) -> str:
            if val[0] == val[1]:
                return f"{name}={val[0]}"
            return f"{name}={val[0]}-{val[1]} (split)"

        lines = [
            "This Layer:",
            f"  {_fmt('black', self.this_layer_black)}, "
            f"{_fmt('white', self.this_layer_white)}",
            "Underlying Layer:",
            f"  {_fmt('black', self.underlying_black)}, "
            f"{_fmt('white', self.underlying_white)}",
        ]
        return "\n".join(lines)


@define
class BlendRanges:
    """Collection of blend ranges for a layer."""

    composite: BlendRangeChannel
    channels: list[BlendRangeChannel]

    def __len__(self) -> int:
        return len(self.channels)

    def __getitem__(self, index: int) -> BlendRangeChannel:
        if index < 0:
            index += len(self.channels)
        return self.channels[index]

    def __iter__(self) -> Iterator[BlendRangeChannel]:
        return iter(self.channels)

    @property
    def channel_count(self) -> int:
        return len(self.channels)

    @property
    def is_default(self) -> bool:
        if not self.composite.is_default:
            return False
        return all(ch.is_default for ch in self.channels)

    def describe(self) -> str:
        if self.is_default:
            return "BlendRanges(default)"
        lines = [f"Composite: {self.composite.describe()}"]
        for i, ch in enumerate(self.channels):
            lines.append(f"Channel {i}: {ch.describe()}")
        return "\n".join(lines)

    @classmethod
    def from_channels(
        cls,
        composite: BlendRangeChannel,
        channels: list[BlendRangeChannel],
    ) -> BlendRanges:
        return cls(composite=composite, channels=list(channels))

    @classmethod
    def from_raw(cls, raw: LayerBlendingRanges) -> BlendRanges:
        if raw.composite_ranges is None or raw.channel_ranges is None:
            return cls(
                composite=BlendRangeChannel.default(),
                channels=[],
            )

        composite = BlendRangeChannel.from_raw(raw.composite_ranges)
        channels = [BlendRangeChannel.from_raw(ch) for ch in raw.channel_ranges]
        return cls(composite=composite, channels=channels)

    def apply_to_raw(self, raw: LayerBlendingRanges) -> None:
        raw.composite_ranges = self.composite.to_raw()
        raw.channel_ranges = [ch.to_raw() for ch in self.channels]

    def compute_visibility(
        self,
        source_color: np.ndarray,
        backdrop_color: np.ndarray,
    ) -> np.ndarray:
        """Compute per-pixel blend-if visibility weights."""
        h, w = source_color.shape[:2]
        weight = np.ones((h, w, 1), dtype=np.float32)

        if self.is_default:
            return weight

        comp = self.composite
        if not comp.is_default:
            src_gray = _luminance(source_color)
            src_byte = np.clip(src_gray * 255.0, 0, 255)
            weight *= _channel_weight(
                src_byte,
                comp.this_layer_black,
                comp.this_layer_white,
            )
            bg_gray = _luminance(backdrop_color)
            bg_byte = np.clip(bg_gray * 255.0, 0, 255)
            weight *= _channel_weight(
                bg_byte,
                comp.underlying_black,
                comp.underlying_white,
            )

        num_ch = min(len(self.channels), source_color.shape[2])
        for i in range(num_ch):
            ch = self.channels[i]
            if ch.is_default:
                continue
            src_byte = np.clip(source_color[:, :, i : i + 1] * 255.0, 0, 255)
            weight *= _channel_weight(
                src_byte,
                ch.this_layer_black,
                ch.this_layer_white,
            )
            bg_byte = np.clip(backdrop_color[:, :, i : i + 1] * 255.0, 0, 255)
            weight *= _channel_weight(
                bg_byte,
                ch.underlying_black,
                ch.underlying_white,
            )

        return weight

    def to_pil_mask(
        self,
        source_color: np.ndarray,
        backdrop_color: np.ndarray,
    ) -> PILImage:
        from PIL import Image

        weight = self.compute_visibility(source_color, backdrop_color)
        mask_byte = (np.squeeze(weight, axis=2) * 255).astype(np.uint8)
        return Image.fromarray(mask_byte, mode="L")


def _luminance(color: np.ndarray) -> np.ndarray:
    if color.shape[2] >= 3:
        return (
            0.299 * color[:, :, 0:1]
            + 0.587 * color[:, :, 1:2]
            + 0.114 * color[:, :, 2:3]
        )
    return color[:, :, 0:1]


def _channel_weight(
    values: np.ndarray,
    black: tuple[int, int],
    white: tuple[int, int],
) -> np.ndarray:
    weight = np.ones_like(values)

    b_lo, b_hi = float(min(black)), float(max(black))
    w_lo, w_hi = float(min(white)), float(max(white))

    if b_lo == b_hi:
        weight = np.where(values < b_lo, 0.0, weight)
    else:
        below = values < b_lo
        fade = (values >= b_lo) & (values <= b_hi)
        fade_val = np.where(
            b_hi > b_lo,
            (values - b_lo) / (b_hi - b_lo),
            1.0,
        )
        weight = np.where(below, 0.0, weight)
        weight = np.where(fade, fade_val, weight)

    if w_lo == w_hi:
        weight = np.where(values > w_hi, 0.0, weight)
    else:
        above = values > w_hi
        fade = (values >= w_lo) & (values <= w_hi)
        fade_val = np.where(
            w_hi > w_lo,
            1.0 - (values - w_lo) / (w_hi - w_lo),
            0.0,
        )
        weight = np.where(above, 0.0, weight)
        weight = np.where(fade, fade_val, weight)

    return weight
