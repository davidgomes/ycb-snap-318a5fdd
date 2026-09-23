"""
Typed blend range (Blend If) API.

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

from collections.abc import Iterator

import numpy as np
from attrs import define
from numpy.typing import NDArray
from PIL import Image

from psd_tools.psd.layer_and_mask import LayerBlendingRanges

Handle = tuple[int, int]
RawChannelRange = list[tuple[int, int]]


def _decode_split(value: int) -> Handle:
    """Decode a uint16 blend range value into a (left, right) split pair.

    Each uint16 encodes two slider handles: low byte is the left handle,
    high byte is the right handle. When not split, both are equal.
    """
    return (value & 0xFF, (value >> 8) & 0xFF)


def _encode_split(pair: Handle) -> int:
    """Encode a (left, right) split pair into a uint16 blend range value."""
    return (pair[1] & 0xFF) << 8 | (pair[0] & 0xFF)


def _luminosity(color: NDArray[np.floating]) -> NDArray[np.floating]:
    """Rec.601 luminosity, shape ``(H, W, 1)``, from a float color image."""
    if color.shape[2] >= 3:
        return (
            0.299 * color[:, :, 0:1]
            + 0.587 * color[:, :, 1:2]
            + 0.114 * color[:, :, 2:3]
        )
    return color[:, :, 0:1]


def _channel_weight(
    values: NDArray[np.floating],
    black: Handle,
    white: Handle,
) -> NDArray[np.floating]:
    """Compute blend-if weight for one slider pair.

    The black slider excludes dark pixels; the white slider excludes
    bright pixels. When a slider is split, the exclusion fades
    linearly between the two handle positions.

    :param values: Per-pixel channel values (0-255) as float array.
    :param black: ``(left_handle, right_handle)`` for the black slider.
    :param white: ``(left_handle, right_handle)`` for the white slider.
    :return: Weight array in [0, 1].
    """
    weight = np.ones_like(values)

    b_lo, b_hi = float(min(black)), float(max(black))
    w_lo, w_hi = float(min(white)), float(max(white))

    if b_lo == b_hi:
        weight = np.where(values < b_lo, 0.0, weight)
    else:
        below = values < b_lo
        fade = (values >= b_lo) & (values <= b_hi)
        fade_val = (values - b_lo) / (b_hi - b_lo)
        weight = np.where(below, 0.0, weight)
        weight = np.where(fade, fade_val, weight)

    if w_lo == w_hi:
        weight = np.where(values > w_hi, 0.0, weight)
    else:
        above = values > w_hi
        fade = (values >= w_lo) & (values <= w_hi)
        fade_val = 1.0 - (values - w_lo) / (w_hi - w_lo)
        weight = np.where(above, 0.0, weight)
        weight = np.where(fade, fade_val, weight)

    return weight


@define
class BlendRangeChannel:
    """Blend If slider pair for a single channel.

    Each channel has a "This Layer" and "Underlying Layer" range.
    Each range has a black and a white endpoint. When the slider is
    split, the two values in the tuple differ, creating a smooth
    transition zone.

    All handle values are in the 0-255 range.

    :param this_layer_black: (left_handle, right_handle) for the
        black point of the This Layer slider.
    :param this_layer_white: (left_handle, right_handle) for the
        white point of the This Layer slider.
    :param underlying_black: (left_handle, right_handle) for the
        black point of the Underlying Layer slider.
    :param underlying_white: (left_handle, right_handle) for the
        white point of the Underlying Layer slider.
    """

    this_layer_black: Handle
    this_layer_white: Handle
    underlying_black: Handle
    underlying_white: Handle

    @classmethod
    def from_raw(cls, raw_pair: list[tuple[int, int]]) -> BlendRangeChannel:
        """Create from a raw channel range pair.

        The raw format is ``[(this_black, this_white),
        (underlying_black, underlying_white)]`` where each value
        is a uint16 encoding a split slider.

        :param raw_pair: 2-element list as returned by the low-level
            ``LayerBlendingRanges`` parser.
        """
        this_layer = raw_pair[0]
        underlying = raw_pair[1]
        return cls(
            this_layer_black=_decode_split(this_layer[0]),
            this_layer_white=_decode_split(this_layer[1]),
            underlying_black=_decode_split(underlying[0]),
            underlying_white=_decode_split(underlying[1]),
        )

    def to_raw(self) -> RawChannelRange:
        """Convert back to the raw channel range pair format.

        :return: ``[(this_black, this_white),
            (underlying_black, underlying_white)]``
        """
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
        """Create from simple (non-split) slider values.

        Each value is a single 0-255 threshold. Use the constructor
        directly for split sliders.

        :param this_layer_black: This Layer black point.
        :param this_layer_white: This Layer white point.
        :param underlying_black: Underlying Layer black point.
        :param underlying_white: Underlying Layer white point.
        """
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
        """Return a human-readable description of this blend range.

        :return: Non-empty string.
        """
        if self.is_default:
            return "BlendRangeChannel(default)"

        def _fmt(name: str, val: Handle) -> str:
            if val[0] == val[1]:
                return f"{name}={val[0]}"
            return f"{name}={val[0]}-{val[1]} (split)"

        lines = [
            "This Layer:",
            (
                f"  {_fmt('black', self.this_layer_black)}, "
                f"{_fmt('white', self.this_layer_white)}"
            ),
            "Underlying Layer:",
            (
                f"  {_fmt('black', self.underlying_black)}, "
                f"{_fmt('white', self.underlying_white)}"
            ),
        ]
        return "\n".join(lines)


@define
class BlendRanges:
    """Collection of blend ranges for a layer.

    Contains a composite (gray) channel and per-color-channel blend
    ranges. The composite range controls overall luminosity blending,
    while per-channel ranges control individual color channel blending.

    ``len``, indexing, and iteration cover per-channel ranges only.

    :param composite: :py:class:`BlendRangeChannel` for the composite/gray.
    :param channels: List of :py:class:`BlendRangeChannel` for each
        color channel.
    """

    composite: BlendRangeChannel
    channels: list[BlendRangeChannel]

    def __len__(self) -> int:
        """Return the number of per-channel blend ranges."""
        return len(self.channels)

    def __getitem__(self, index: int) -> BlendRangeChannel:
        """Return the blend range for a specific channel.

        Supports negative indices.
        """
        if index < 0:
            index += len(self.channels)
        return self.channels[index]

    def __iter__(self) -> Iterator[BlendRangeChannel]:
        """Iterate over per-channel blend ranges."""
        return iter(self.channels)

    @property
    def channel_count(self) -> int:
        """Number of per-channel blend ranges."""
        return len(self.channels)

    @property
    def is_default(self) -> bool:
        """True when all blend ranges are at their defaults."""
        if not self.composite.is_default:
            return False
        return all(ch.is_default for ch in self.channels)

    def describe(self) -> str:
        """Return a human-readable description of all blend ranges.

        :return: Non-empty string.
        """
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
        """Create from explicit composite and per-channel ranges.

        :param composite: Composite/gray blend range.
        :param channels: Per-color-channel blend ranges.
        """
        return cls(composite=composite, channels=list(channels))

    def to_pil_mask(
        self,
        source_color: NDArray[np.floating],
        backdrop_color: NDArray[np.floating],
    ) -> Image.Image:
        """Generate a PIL Image mask from the blend-if visibility.

        White (255) = fully visible, black (0) = fully excluded.

        :param source_color: Source layer color as float ``(H, W, C)``
            array in [0, 1].
        :param backdrop_color: Backdrop color, same shape.
        :return: PIL ``Image`` in mode ``'L'``.
        """
        weight = self.compute_visibility(source_color, backdrop_color)
        mask_byte = (np.squeeze(weight, axis=2) * 255).astype(np.uint8)
        return Image.fromarray(mask_byte, mode="L")

    @classmethod
    def from_raw(cls, raw: LayerBlendingRanges) -> BlendRanges:
        """Create from the low-level
        :py:class:`~psd_tools.psd.layer_and_mask.LayerBlendingRanges`.
        """
        if raw.composite_ranges is None or raw.channel_ranges is None:
            return cls(
                composite=BlendRangeChannel.default(),
                channels=[],
            )

        composite = BlendRangeChannel.from_raw(raw.composite_ranges)
        channels = [BlendRangeChannel.from_raw(ch) for ch in raw.channel_ranges]
        return cls(composite=composite, channels=channels)

    def apply_to_raw(self, raw: LayerBlendingRanges) -> None:
        """Write blend ranges back to the low-level structure."""
        raw.composite_ranges = self.composite.to_raw()
        raw.channel_ranges = [ch.to_raw() for ch in self.channels]

    def compute_visibility(
        self,
        source_color: NDArray[np.floating],
        backdrop_color: NDArray[np.floating],
    ) -> NDArray[np.floating]:
        """Compute per-pixel blend-if visibility weights.

        This produces a float weight array in [0, 1] that indicates how
        visible each pixel of the source layer should be, given the
        current blend ranges and the backdrop colors.

        The composite (gray) range uses luminosity
        ``0.299*R + 0.587*G + 0.114*B``. Per-channel ranges use the
        matching channel. "This Layer" reads source values; "Underlying
        Layer" reads backdrop values. Split sliders fade linearly.
        Channel weights are multiplied together.

        :param source_color: Source layer pixels as a float
            ndarray of shape ``(H, W, C)`` with values in [0, 1].
        :param backdrop_color: Underlying layer pixels, same shape.
        :return: Weight ndarray of shape ``(H, W, 1)`` in [0, 1].
        """
        height, width = source_color.shape[:2]
        weight = np.ones((height, width, 1), dtype=np.float32)

        if self.is_default:
            return weight

        comp = self.composite
        if not comp.is_default:
            src_byte = np.clip(_luminosity(source_color) * 255.0, 0, 255)
            weight *= _channel_weight(
                src_byte,
                comp.this_layer_black,
                comp.this_layer_white,
            )
            bg_byte = np.clip(_luminosity(backdrop_color) * 255.0, 0, 255)
            weight *= _channel_weight(
                bg_byte,
                comp.underlying_black,
                comp.underlying_white,
            )

        num_ch = min(
            len(self.channels),
            source_color.shape[2],
            backdrop_color.shape[2],
        )
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
