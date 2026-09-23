"""
Blend range ("Blend If") API.

Photoshop's "Blend If" sliders control which pixels of a layer are visible
depending on the values of the layer itself ("This Layer") and of the
pixels beneath it ("Underlying Layer"). Each slider has a black and a white
handle, and each handle can be split into a left and a right half to create
a linear fade.

In the file, every handle is stored as a uint16 where the low byte is the
left half and the high byte is the right half.

Example::

    from psd_tools import PSDImage

    psd = PSDImage.open('document.psd')
    layer = psd[0]
    ranges = layer.blend_ranges
    ranges.composite.this_layer_black = (32, 64)
    layer.blend_ranges = ranges
    psd.save('modified.psd')
"""

from __future__ import annotations

from typing import TYPE_CHECKING, Iterator, Sequence

import numpy as np
from attrs import define, field
from PIL import Image

if TYPE_CHECKING:
    from psd_tools.psd.layer_and_mask import LayerBlendingRanges

Handle = tuple[int, int]

_BLACK: Handle = (0, 0)
_WHITE: Handle = (255, 255)


def _to_handle(value: int | Sequence[int]) -> Handle:
    if isinstance(value, (int, np.integer)):
        left = right = int(value)
    else:
        if len(value) != 2:
            raise ValueError(f"Expected (left, right) pair, got {value!r}")
        left, right = int(value[0]), int(value[1])
    if not (0 <= left <= 255 and 0 <= right <= 255):
        raise ValueError(f"Handle values must be in range [0, 255], got {value!r}")
    return (left, right)


def _decode(value: int) -> Handle:
    return (value & 0xFF, (value >> 8) & 0xFF)


def _encode(handle: Handle) -> int:
    return (handle[1] << 8) | handle[0]


def _black_weight(values: np.ndarray, handle: Handle) -> np.ndarray:
    """Weight for a black handle: hide values below, fade across a split."""
    left, right = sorted(handle)
    if left == right:
        return (values >= left).astype(np.float32)
    return np.clip((values - left) / float(right - left), 0.0, 1.0).astype(np.float32)


def _white_weight(values: np.ndarray, handle: Handle) -> np.ndarray:
    """Weight for a white handle: hide values above, fade across a split."""
    left, right = sorted(handle)
    if left == right:
        return (values <= right).astype(np.float32)
    return np.clip((right - values) / float(right - left), 0.0, 1.0).astype(np.float32)


@define(eq=True)
class BlendRangeChannel:
    """
    Blend range sliders for a single channel (or the composite gray).

    Each attribute is a ``(left_handle, right_handle)`` tuple in [0, 255].
    When both halves are equal the handle is not split.

    .. py:attribute:: this_layer_black
    .. py:attribute:: this_layer_white
    .. py:attribute:: underlying_black
    .. py:attribute:: underlying_white
    """

    this_layer_black: Handle = field(default=_BLACK, converter=_to_handle)
    this_layer_white: Handle = field(default=_WHITE, converter=_to_handle)
    underlying_black: Handle = field(default=_BLACK, converter=_to_handle)
    underlying_white: Handle = field(default=_WHITE, converter=_to_handle)

    @classmethod
    def default(cls) -> BlendRangeChannel:
        """Full range: nothing is hidden."""
        return cls()

    @classmethod
    def from_values(
        cls,
        this_layer_black: int = 0,
        this_layer_white: int = 255,
        underlying_black: int = 0,
        underlying_white: int = 255,
    ) -> BlendRangeChannel:
        """Create a channel with non-split handles."""
        return cls(
            (this_layer_black, this_layer_black),
            (this_layer_white, this_layer_white),
            (underlying_black, underlying_black),
            (underlying_white, underlying_white),
        )

    @classmethod
    def from_raw(cls, raw_pair: Sequence[Sequence[int]]) -> BlendRangeChannel:
        """
        Create from raw ``[(black, white), (black, white)]`` uint16 pairs,
        where the first pair is this layer and the second the underlying layer.
        """
        if len(raw_pair) != 2 or any(len(x) != 2 for x in raw_pair):
            raise ValueError(f"Expected 2 (black, white) pairs, got {raw_pair!r}")
        (src_black, src_white), (dst_black, dst_white) = raw_pair
        return cls(
            _decode(src_black),
            _decode(src_white),
            _decode(dst_black),
            _decode(dst_white),
        )

    def to_raw(self) -> list[tuple[int, int]]:
        """Convert to raw ``[(black, white), (black, white)]`` uint16 pairs."""
        return [
            (_encode(self.this_layer_black), _encode(self.this_layer_white)),
            (_encode(self.underlying_black), _encode(self.underlying_white)),
        ]

    @property
    def is_default(self) -> bool:
        """Whether all handles are at their default positions."""
        return (
            self.this_layer_black == _BLACK
            and self.this_layer_white == _WHITE
            and self.underlying_black == _BLACK
            and self.underlying_white == _WHITE
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
        """Human-readable summary of the slider positions."""

        def fmt(handle: Handle) -> str:
            if handle[0] == handle[1]:
                return str(handle[0])
            return "%d/%d" % handle

        return "This Layer: %s-%s, Underlying Layer: %s-%s" % (
            fmt(self.this_layer_black),
            fmt(self.this_layer_white),
            fmt(self.underlying_black),
            fmt(self.underlying_white),
        )

    def compute_visibility(
        self, source: np.ndarray, backdrop: np.ndarray
    ) -> np.ndarray:
        """
        Visibility weight for single-channel source and backdrop values in [0, 1].
        """
        source = np.asarray(source, dtype=np.float32) * 255.0
        backdrop = np.asarray(backdrop, dtype=np.float32) * 255.0
        return (
            _black_weight(source, self.this_layer_black)
            * _white_weight(source, self.this_layer_white)
            * _black_weight(backdrop, self.underlying_black)
            * _white_weight(backdrop, self.underlying_white)
        )


def _luminosity(color: np.ndarray) -> np.ndarray:
    if color.shape[2] >= 3:
        return (
            0.299 * color[:, :, 0:1]
            + 0.587 * color[:, :, 1:2]
            + 0.114 * color[:, :, 2:3]
        )
    return color[:, :, 0:1]


def _as_3d(values: np.ndarray) -> np.ndarray:
    values = np.asarray(values, dtype=np.float32)
    if values.ndim == 2:
        values = values[:, :, np.newaxis]
    if values.ndim != 3:
        raise ValueError(f"Expected (H, W) or (H, W, C) array, got {values.shape}")
    return values


@define(eq=True)
class BlendRanges:
    """
    Blend ranges of a layer.

    ``len()``, indexing, and iteration operate on the per-channel ranges
    only; the composite gray range is available as :py:attr:`composite`.

    .. py:attribute:: composite

        :py:class:`BlendRangeChannel` for the composite gray.

    .. py:attribute:: channels

        List of :py:class:`BlendRangeChannel`, one per color channel.
    """

    composite: BlendRangeChannel = field(factory=BlendRangeChannel)
    channels: list[BlendRangeChannel] = field(factory=list)

    @classmethod
    def from_raw(cls, raw: LayerBlendingRanges) -> BlendRanges:
        """Create from :py:class:`~psd_tools.psd.layer_and_mask.LayerBlendingRanges`."""
        if raw.composite_ranges is None:
            composite = BlendRangeChannel.default()
        else:
            composite = BlendRangeChannel.from_raw(raw.composite_ranges)
        channels = [BlendRangeChannel.from_raw(x) for x in raw.channel_ranges or []]
        return cls(composite, channels)

    @classmethod
    def from_channels(
        cls,
        composite: BlendRangeChannel | None = None,
        channels: Sequence[BlendRangeChannel] | None = None,
    ) -> BlendRanges:
        """Create from explicit :py:class:`BlendRangeChannel` objects."""
        return cls(
            composite if composite is not None else BlendRangeChannel.default(),
            list(channels) if channels is not None else [],
        )

    def apply_to_raw(self, raw: LayerBlendingRanges) -> None:
        """Write these ranges into a raw ``LayerBlendingRanges`` in place."""
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
        """Whether the composite and every channel are at their defaults."""
        return self.composite.is_default and all(c.is_default for c in self.channels)

    def describe(self) -> str:
        """Human-readable summary of all ranges."""
        lines = ["Gray: %s" % self.composite.describe()]
        for i, channel in enumerate(self.channels):
            lines.append("Channel %d: %s" % (i, channel.describe()))
        return "\n".join(lines)

    def compute_visibility(
        self, source_color: np.ndarray, backdrop_color: np.ndarray
    ) -> np.ndarray:
        """
        Compute the blend-if visibility weight.

        :param source_color: layer color, float array in [0, 1] of shape
            ``(H, W)`` or ``(H, W, C)``.
        :param backdrop_color: underlying color, same layout as
            ``source_color``.
        :return: float32 array of shape ``(H, W, 1)`` in [0, 1].
        """
        source = _as_3d(source_color)
        backdrop = _as_3d(backdrop_color)
        if source.shape[2] != backdrop.shape[2]:
            if source.shape[2] == 1:
                source = np.repeat(source, backdrop.shape[2], axis=2)
            elif backdrop.shape[2] == 1:
                backdrop = np.repeat(backdrop, source.shape[2], axis=2)
            else:
                raise ValueError(
                    "Channel mismatch: %d vs %d" % (source.shape[2], backdrop.shape[2])
                )

        weight = np.ones(source.shape[:2] + (1,), dtype=np.float32)
        if not self.composite.is_default:
            weight *= self.composite.compute_visibility(
                _luminosity(source), _luminosity(backdrop)
            )
        for i, channel in enumerate(self.channels[: source.shape[2]]):
            if channel.is_default:
                continue
            weight *= channel.compute_visibility(
                source[:, :, i : i + 1], backdrop[:, :, i : i + 1]
            )
        return weight

    def to_pil_mask(
        self, source_color: np.ndarray, backdrop_color: np.ndarray
    ) -> Image.Image:
        """Compute the visibility weight as a PIL ``'L'`` mode image."""
        weight = self.compute_visibility(source_color, backdrop_color)
        return Image.fromarray(np.round(weight[:, :, 0] * 255.0).astype(np.uint8))
