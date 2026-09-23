"""
Blend range ("Blend If") API.

Each layer stores blend ranges for the composite (gray) channel and for each
color channel. Every range consists of a "This Layer" slider and an
"Underlying Layer" slider, each with a black and a white handle. A handle can
be split into two halves (left, right) to produce a linear fade.
"""

from __future__ import annotations

from typing import TYPE_CHECKING, Iterator

import numpy as np
from attrs import define, field

if TYPE_CHECKING:
    from PIL import Image

    from psd_tools.psd.layer_and_mask import LayerBlendingRanges

Handle = tuple[int, int]

_BLACK: Handle = (0, 0)
_WHITE: Handle = (255, 255)


def _decode(value: int) -> Handle:
    return (value & 0xFF, (value >> 8) & 0xFF)


def _encode(handle: Handle) -> int:
    left, right = handle
    return (int(left) & 0xFF) | ((int(right) & 0xFF) << 8)


def _as_handle(value: int | Handle) -> Handle:
    if isinstance(value, (tuple, list)):
        return (int(value[0]), int(value[1]))
    return (int(value), int(value))


def _slider_weight(values: np.ndarray, black: Handle, white: Handle) -> np.ndarray:
    """Weight in [0, 1] for values in [0, 255] given black and white handles."""
    b0, b1 = float(min(black)), float(max(black))
    w0, w1 = float(min(white)), float(max(white))
    if b1 > b0:
        low = np.clip((values - b0) / (b1 - b0), 0.0, 1.0)
    else:
        low = (values >= b0).astype(np.float32)
    if w1 > w0:
        high = np.clip((w1 - values) / (w1 - w0), 0.0, 1.0)
    else:
        high = (values <= w0).astype(np.float32)
    return (low * high).astype(np.float32)


@define(repr=False)
class BlendRangeChannel:
    """
    Blend range of a single channel.

    Each attribute is a ``(left_handle, right_handle)`` tuple in 0-255.
    """

    this_layer_black: Handle = field(default=_BLACK)
    this_layer_white: Handle = field(default=_WHITE)
    underlying_black: Handle = field(default=_BLACK)
    underlying_white: Handle = field(default=_WHITE)

    @classmethod
    def default(cls) -> "BlendRangeChannel":
        return cls()

    @classmethod
    def from_values(
        cls,
        this_layer_black: int = 0,
        this_layer_white: int = 255,
        underlying_black: int = 0,
        underlying_white: int = 255,
    ) -> "BlendRangeChannel":
        return cls(
            _as_handle(this_layer_black),
            _as_handle(this_layer_white),
            _as_handle(underlying_black),
            _as_handle(underlying_white),
        )

    @classmethod
    def from_raw(cls, raw_pair: list[tuple[int, int]]) -> "BlendRangeChannel":
        (src_black, src_white), (dst_black, dst_white) = raw_pair
        return cls(
            _decode(src_black),
            _decode(src_white),
            _decode(dst_black),
            _decode(dst_white),
        )

    def to_raw(self) -> list[tuple[int, int]]:
        return [
            (_encode(self.this_layer_black), _encode(self.this_layer_white)),
            (_encode(self.underlying_black), _encode(self.underlying_white)),
        ]

    @property
    def is_default(self) -> bool:
        return (
            tuple(self.this_layer_black) == _BLACK
            and tuple(self.this_layer_white) == _WHITE
            and tuple(self.underlying_black) == _BLACK
            and tuple(self.underlying_white) == _WHITE
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

    def compute_weight(self, source: np.ndarray, backdrop: np.ndarray) -> np.ndarray:
        """Weight for source/backdrop values in [0, 1]."""
        weight = np.ones_like(source, dtype=np.float32)
        if (self.this_layer_black, self.this_layer_white) != (_BLACK, _WHITE):
            weight = weight * _slider_weight(
                source * 255.0, self.this_layer_black, self.this_layer_white
            )
        if (self.underlying_black, self.underlying_white) != (_BLACK, _WHITE):
            weight = weight * _slider_weight(
                backdrop * 255.0, self.underlying_black, self.underlying_white
            )
        return weight

    @staticmethod
    def _describe_slider(black: Handle, white: Handle) -> str:
        def fmt(h: Handle) -> str:
            return "%d" % h[0] if h[0] == h[1] else "%d/%d" % h

        return "%s-%s" % (fmt(black), fmt(white))

    def describe(self) -> str:
        return "This Layer: %s, Underlying Layer: %s" % (
            self._describe_slider(self.this_layer_black, self.this_layer_white),
            self._describe_slider(self.underlying_black, self.underlying_white),
        )

    def __repr__(self) -> str:
        return "%s(%s)" % (self.__class__.__name__, self.describe())


@define(repr=False)
class BlendRanges:
    """Blend ranges of a layer: composite (gray) range and per-channel ranges."""

    composite: BlendRangeChannel = field(factory=BlendRangeChannel)
    channels: list[BlendRangeChannel] = field(factory=list)

    @classmethod
    def from_raw(cls, raw: "LayerBlendingRanges") -> "BlendRanges":
        if raw.composite_ranges is None:
            composite = BlendRangeChannel.default()
        else:
            composite = BlendRangeChannel.from_raw(raw.composite_ranges)
        channels = [BlendRangeChannel.from_raw(c) for c in (raw.channel_ranges or [])]
        return cls(composite, channels)

    @classmethod
    def from_channels(
        cls, composite: BlendRangeChannel, channels: list[BlendRangeChannel]
    ) -> "BlendRanges":
        return cls(composite, list(channels))

    def apply_to_raw(self, raw: "LayerBlendingRanges") -> None:
        raw.composite_ranges = self.composite.to_raw()
        raw.channel_ranges = [c.to_raw() for c in self.channels]

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
        return self.composite.is_default and all(c.is_default for c in self.channels)

    def describe(self) -> str:
        lines = ["Gray: %s" % self.composite.describe()]
        for i, c in enumerate(self.channels):
            lines.append("Channel %d: %s" % (i, c.describe()))
        return "\n".join(lines)

    def __repr__(self) -> str:
        return "%s(composite=%r, channels=%r)" % (
            self.__class__.__name__,
            self.composite,
            self.channels,
        )

    def compute_visibility(
        self, source_color: np.ndarray, backdrop_color: np.ndarray
    ) -> np.ndarray:
        """
        Compute blend-if visibility weights.

        :param source_color: float array in [0, 1] of shape (H, W) or (H, W, C).
        :param backdrop_color: float array in [0, 1] broadcastable to source.
        :return: float32 array of shape (H, W, 1) in [0, 1].
        """
        source = np.asarray(source_color, dtype=np.float32)
        backdrop = np.asarray(backdrop_color, dtype=np.float32)
        if source.ndim == 2:
            source = source[:, :, np.newaxis]
        if backdrop.ndim == 2:
            backdrop = backdrop[:, :, np.newaxis]
        num = max(source.shape[2], backdrop.shape[2])
        shape = source.shape[:2] + (num,)
        source = np.broadcast_to(source, shape)
        backdrop = np.broadcast_to(backdrop, shape)

        weight = np.ones(source.shape[:2] + (1,), dtype=np.float32)
        if not self.composite.is_default:
            weight = weight * self.composite.compute_weight(
                _luminosity(source), _luminosity(backdrop)
            )
        for i, channel in enumerate(self.channels[:num]):
            if channel.is_default:
                continue
            weight = weight * channel.compute_weight(
                source[:, :, i : i + 1], backdrop[:, :, i : i + 1]
            )
        return np.clip(weight, 0.0, 1.0)

    def to_pil_mask(
        self, source_color: np.ndarray, backdrop_color: np.ndarray
    ) -> "Image.Image":
        from PIL import Image

        weight = self.compute_visibility(source_color, backdrop_color)
        return Image.fromarray(np.round(weight[:, :, 0] * 255.0).astype(np.uint8))


def _luminosity(color: np.ndarray) -> np.ndarray:
    if color.shape[2] >= 3:
        return (
            0.299 * color[:, :, 0:1]
            + 0.587 * color[:, :, 1:2]
            + 0.114 * color[:, :, 2:3]
        )
    return color[:, :, 0:1]
