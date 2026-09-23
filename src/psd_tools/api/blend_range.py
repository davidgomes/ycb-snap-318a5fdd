"""
Blend If (layer blending ranges).

Photoshop stores each layer's Blend If sliders in
:py:class:`~psd_tools.psd.layer_and_mask.LayerBlendingRanges` as raw uint16
pairs. This module exposes those sliders as mutable handles and can turn
them into a per-pixel visibility weight.

Each slider is a ``(left_handle, right_handle)`` pair in the range 0-255.
When the handles differ, the slider is split and visibility fades linearly
between them. A uint16 encodes that split: the low byte is the left handle
and the high byte is the right handle.

Example::

    layer.blend_ranges.composite.this_layer_black = (32, 32)
    layer.blend_ranges.channels[0].this_layer_white = (200, 240)
"""

from __future__ import annotations

from collections.abc import Iterator, Sequence

import numpy as np
from attrs import define, field
from PIL import Image
from typing_extensions import Self

from psd_tools.psd.layer_and_mask import LayerBlendingRanges

# ITU-R BT.601 luma, matching Photoshop's Gray Blend If slider.
_LUMA_R = np.float32(0.299)
_LUMA_G = np.float32(0.587)
_LUMA_B = np.float32(0.114)


def _decode_split(value: int) -> tuple[int, int]:
    """Decode a uint16 slider into ``(left_handle, right_handle)``."""
    value = int(value)
    return (value & 0xFF, (value >> 8) & 0xFF)


def _encode_split(pair: tuple[int, int]) -> int:
    """Encode ``(left_handle, right_handle)`` into a uint16 slider."""
    left = int(pair[0]) & 0xFF
    right = int(pair[1]) & 0xFF
    return (right << 8) | left


def _matches(pair: tuple[int, int], left: int, right: int) -> bool:
    return int(pair[0]) == left and int(pair[1]) == right


def _as_hwc(color: np.ndarray) -> np.ndarray:
    """Return a float32 ``(H, W, C)`` array."""
    array = np.asarray(color, dtype=np.float32)
    if array.ndim == 2:
        return array[:, :, np.newaxis]
    if array.ndim != 3:
        raise ValueError(
            "color arrays must have shape (H, W) or (H, W, C), "
            f"got {array.shape}"
        )
    return array


def _luminosity(color: np.ndarray) -> np.ndarray:
    """Gray Blend If sample, shape ``(H, W, 1)``."""
    if color.shape[2] >= 3:
        return (
            _LUMA_R * color[:, :, 0:1]
            + _LUMA_G * color[:, :, 1:2]
            + _LUMA_B * color[:, :, 2:3]
        )
    return color[:, :, 0:1]


def _channel_weight(
    values: np.ndarray,
    black: tuple[int, int],
    white: tuple[int, int],
) -> np.ndarray:
    """Visibility for one black/white slider pair.

    ``values`` are channel samples in 0-255. The black slider hides darker
    pixels and the white slider hides brighter pixels. A split slider fades
    linearly between its handles. Where the two ramps overlap, the stricter
    ramp wins.
    """
    b_lo = float(min(black))
    b_hi = float(max(black))
    w_lo = float(min(white))
    w_hi = float(max(white))

    if b_lo == b_hi:
        black_weight = np.where(values < b_lo, np.float32(0.0), np.float32(1.0))
    else:
        black_weight = np.clip((values - b_lo) / (b_hi - b_lo), 0.0, 1.0)

    if w_lo == w_hi:
        white_weight = np.where(values > w_hi, np.float32(0.0), np.float32(1.0))
    else:
        white_weight = np.clip((w_hi - values) / (w_hi - w_lo), 0.0, 1.0)

    return np.minimum(black_weight, white_weight)


def _scaled_bytes(values: np.ndarray) -> np.ndarray:
    """Map ``[0, 1]`` samples to the 0-255 slider domain."""
    return np.clip(values * np.float32(255.0), 0.0, 255.0)


@define
class BlendRangeChannel:
    """Blend If sliders for one channel.

    ``this_layer_*`` sliders read the layer being composited. ``underlying_*``
    sliders read the backdrop. Each value is a ``(left_handle, right_handle)``
    tuple from 0 to 255. Equal handles are a hard step; unequal handles are a
    split slider.

    .. py:attribute:: this_layer_black

        This Layer black slider.

    .. py:attribute:: this_layer_white

        This Layer white slider.

    .. py:attribute:: underlying_black

        Underlying Layer black slider.

    .. py:attribute:: underlying_white

        Underlying Layer white slider.
    """

    this_layer_black: tuple[int, int] = (0, 0)
    this_layer_white: tuple[int, int] = (255, 255)
    underlying_black: tuple[int, int] = (0, 0)
    underlying_white: tuple[int, int] = (255, 255)

    @classmethod
    def default(cls) -> Self:
        """Return a channel that does not hide any pixels."""
        return cls()

    @classmethod
    def from_values(
        cls,
        this_layer_black: int = 0,
        this_layer_white: int = 255,
        underlying_black: int = 0,
        underlying_white: int = 255,
    ) -> Self:
        """Create a channel from non-split slider positions.

        Each argument is a single 0-255 position applied to both handles.
        Omitted arguments keep the full 0-255 range.
        """
        return cls(
            (int(this_layer_black), int(this_layer_black)),
            (int(this_layer_white), int(this_layer_white)),
            (int(underlying_black), int(underlying_black)),
            (int(underlying_white), int(underlying_white)),
        )

    @classmethod
    def from_raw(cls, raw_pair: Sequence[tuple[int, int]]) -> Self:
        """Parse a raw channel range.

        ``raw_pair`` is a 2-element sequence of ``(black_uint16, white_uint16)``
        pairs. The first pair is This Layer and the second is Underlying Layer.
        Each uint16 stores a split slider: low byte = left handle, high byte =
        right handle.
        """
        if len(raw_pair) != 2:
            raise ValueError(
                "blend range channel must contain exactly 2 pairs, "
                f"got {len(raw_pair)}"
            )
        this_layer, underlying = raw_pair
        if len(this_layer) != 2 or len(underlying) != 2:
            raise ValueError(
                "each blend range pair must be (black_uint16, white_uint16)"
            )
        return cls(
            this_layer_black=_decode_split(this_layer[0]),
            this_layer_white=_decode_split(this_layer[1]),
            underlying_black=_decode_split(underlying[0]),
            underlying_white=_decode_split(underlying[1]),
        )

    def to_raw(self) -> list[tuple[int, int]]:
        """Convert to ``[(this_black, this_white), (underlying_black, underlying_white)]``."""
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

    @property
    def is_default(self) -> bool:
        """True when every slider sits at the full 0-255 range."""
        return (
            _matches(self.this_layer_black, 0, 0)
            and _matches(self.this_layer_white, 255, 255)
            and _matches(self.underlying_black, 0, 0)
            and _matches(self.underlying_white, 255, 255)
        )

    @property
    def this_layer_black_split(self) -> bool:
        """True when the This Layer black slider is split."""
        return int(self.this_layer_black[0]) != int(self.this_layer_black[1])

    @property
    def this_layer_white_split(self) -> bool:
        """True when the This Layer white slider is split."""
        return int(self.this_layer_white[0]) != int(self.this_layer_white[1])

    @property
    def underlying_black_split(self) -> bool:
        """True when the Underlying Layer black slider is split."""
        return int(self.underlying_black[0]) != int(self.underlying_black[1])

    @property
    def underlying_white_split(self) -> bool:
        """True when the Underlying Layer white slider is split."""
        return int(self.underlying_white[0]) != int(self.underlying_white[1])

    def describe(self) -> str:
        """Return a readable summary of the four sliders."""

        def slider(name: str, pair: tuple[int, int], split: bool) -> str:
            if split:
                return f"{name} {int(pair[0])}-{int(pair[1])}"
            return f"{name} {int(pair[0])}"

        return (
            "This Layer "
            f"{slider('black', self.this_layer_black, self.this_layer_black_split)}, "
            f"{slider('white', self.this_layer_white, self.this_layer_white_split)}; "
            "Underlying "
            f"{slider('black', self.underlying_black, self.underlying_black_split)}, "
            f"{slider('white', self.underlying_white, self.underlying_white_split)}"
        )


def _copy_channels(
    channels: Sequence[BlendRangeChannel],
) -> list[BlendRangeChannel]:
    return list(channels)


@define
class BlendRanges:
    """Blend If settings for the composite gray slider and each color channel.

    ``len``, indexing (including negative indexes), and iteration yield color
    channels only. The gray slider is available as :py:attr:`composite`.

    .. py:attribute:: composite

        Gray Blend If channel. Luminosity is ``0.299*R + 0.587*G + 0.114*B``.

    .. py:attribute:: channels

        Per-channel Blend If settings, in document channel order.
    """

    composite: BlendRangeChannel
    channels: list[BlendRangeChannel] = field(factory=list, converter=_copy_channels)

    @classmethod
    def from_raw(cls, raw_blending_ranges: LayerBlendingRanges) -> Self:
        """Create blend ranges from a low-level ``LayerBlendingRanges``.

        An empty (null) range block becomes a full-range composite slider and
        an empty channel list.
        """
        if (
            raw_blending_ranges.composite_ranges is None
            or raw_blending_ranges.channel_ranges is None
        ):
            return cls(composite=BlendRangeChannel.default(), channels=[])
        return cls(
            composite=BlendRangeChannel.from_raw(raw_blending_ranges.composite_ranges),
            channels=[
                BlendRangeChannel.from_raw(channel)
                for channel in raw_blending_ranges.channel_ranges
            ],
        )

    @classmethod
    def from_channels(
        cls,
        composite: BlendRangeChannel,
        channels: Sequence[BlendRangeChannel],
    ) -> Self:
        """Create blend ranges from an explicit composite slider and channels."""
        return cls(composite=composite, channels=list(channels))

    def apply_to_raw(self, raw: LayerBlendingRanges) -> None:
        """Write these sliders back onto a low-level ``LayerBlendingRanges``."""
        raw.composite_ranges = self.composite.to_raw()
        raw.channel_ranges = [channel.to_raw() for channel in self.channels]

    @property
    def channel_count(self) -> int:
        """Number of per-channel ranges, not counting the composite slider."""
        return len(self.channels)

    @property
    def is_default(self) -> bool:
        """True when the composite slider and every channel are full range."""
        return self.composite.is_default and all(
            channel.is_default for channel in self.channels
        )

    def describe(self) -> str:
        """Return a readable summary of the composite slider and channels."""
        parts = [f"Composite: {self.composite.describe()}"]
        if self.channels:
            for index, channel in enumerate(self.channels):
                parts.append(f"Channel {index}: {channel.describe()}")
        else:
            parts.append("Channels: none")
        return "; ".join(parts)

    def compute_visibility(
        self,
        source_color: np.ndarray,
        backdrop_color: np.ndarray,
    ) -> np.ndarray:
        """Return per-pixel Blend If weights.

        :param source_color: Layer color samples in ``[0, 1]``, shape
            ``(H, W, C)`` or ``(H, W)``.
        :param backdrop_color: Backdrop color samples in ``[0, 1]``,
            broadcastable to ``source_color``.
        :return: Weight array of shape ``(H, W, 1)`` in ``[0, 1]``.
        """
        source = _as_hwc(source_color)
        backdrop = _as_hwc(backdrop_color)
        source_b, backdrop_b = np.broadcast_arrays(source, backdrop)
        source = np.asarray(source_b, dtype=np.float32)
        backdrop = np.asarray(backdrop_b, dtype=np.float32)

        weight = np.ones((source.shape[0], source.shape[1], 1), dtype=np.float32)
        if not self.composite.is_default:
            weight *= _channel_weight(
                _scaled_bytes(_luminosity(source)),
                self.composite.this_layer_black,
                self.composite.this_layer_white,
            )
            weight *= _channel_weight(
                _scaled_bytes(_luminosity(backdrop)),
                self.composite.underlying_black,
                self.composite.underlying_white,
            )

        limit = min(len(self.channels), source.shape[2], backdrop.shape[2])
        for index in range(limit):
            channel = self.channels[index]
            if channel.is_default:
                continue
            weight *= _channel_weight(
                _scaled_bytes(source[:, :, index : index + 1]),
                channel.this_layer_black,
                channel.this_layer_white,
            )
            weight *= _channel_weight(
                _scaled_bytes(backdrop[:, :, index : index + 1]),
                channel.underlying_black,
                channel.underlying_white,
            )

        result = np.clip(weight, 0.0, 1.0).astype(np.float32, copy=False)
        if result.ndim == 2:
            result = result[:, :, np.newaxis]
        return np.ascontiguousarray(result[:, :, :1])

    def to_pil_mask(
        self,
        source_color: np.ndarray,
        backdrop_color: np.ndarray,
    ) -> Image.Image:
        """Render :py:meth:`compute_visibility` as an ``'L'`` mode image.

        White (255) is fully visible and black (0) is fully hidden.
        """
        weight = self.compute_visibility(source_color, backdrop_color)
        mask = np.squeeze(weight, axis=2)
        mask_byte = np.ascontiguousarray((mask * 255.0).astype(np.uint8))
        return Image.fromarray(mask_byte, mode="L")

    def __len__(self) -> int:
        return len(self.channels)

    def __iter__(self) -> Iterator[BlendRangeChannel]:
        return iter(self.channels)

    def __getitem__(self, index: int) -> BlendRangeChannel:
        return self.channels[index]
