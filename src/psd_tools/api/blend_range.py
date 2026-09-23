"""
Blend range module.

Blend ranges are the "Blend If" sliders of Photoshop's blending options. They
hide the pixels of a layer depending on the values of the layer itself
("This Layer") or of the pixels beneath it ("Underlying Layer"). Each slider
has a black and a white handle, and each handle can be split into a left and a
right half so that pixels between the two halves fade in linearly instead of
being cut off.

Blend ranges are accessible from the layer's ``blend_ranges`` property. The
property returns a copy, so assign it back to apply changes::

    from psd_tools import PSDImage

    psdimage = PSDImage.open('example.psd')
    layer = psdimage[0]
    blend_ranges = layer.blend_ranges
    print(blend_ranges.describe())

    # Hide the dark pixels of this layer, fading between 32 and 96.
    blend_ranges.composite.this_layer_black = (32, 96)
    layer.blend_ranges = blend_ranges
    psdimage.save('output.psd')

Slider positions use Photoshop's 0-255 scale regardless of the color depth.
"""

from typing import Any, Iterable, Iterator, Sequence

import attrs
import numpy as np
from attrs import define, field
from attrs.validators import deep_iterable, instance_of
from PIL import Image
from typing_extensions import Self

from psd_tools.psd.layer_and_mask import LayerBlendingRanges

Handles = tuple[int, int]

# Tolerance for float rounding when comparing 8-bit values to unsplit sliders.
_EPSILON = 1e-3


def _to_handles(value: Iterable[int]) -> Handles:
    try:
        left, right = value
    except (TypeError, ValueError):
        raise ValueError(
            "Expected a (left_handle, right_handle) pair, got %r" % (value,)
        ) from None
    return int(left), int(right)


def _validate_handles(
    instance: Any, attribute: "attrs.Attribute[Handles]", value: Handles
) -> None:
    if not all(0 <= handle <= 255 for handle in value):
        raise ValueError(
            "%s handles must be in range [0, 255], got %r" % (attribute.name, value)
        )


def _unpack(value: int) -> Handles:
    return value & 0xFF, (value >> 8) & 0xFF


def _pack(handles: Handles) -> int:
    return handles[0] | (handles[1] << 8)


def _format(handles: Handles) -> str:
    if handles[0] == handles[1]:
        return "%d" % handles[0]
    return "%d/%d" % handles


def _black_weight(values: np.ndarray, handles: Handles) -> np.ndarray:
    left, right = sorted(handles)
    if left == right:
        return (values >= left - _EPSILON).astype(np.float32)
    return np.clip((values - left) / (right - left), 0.0, 1.0)


def _white_weight(values: np.ndarray, handles: Handles) -> np.ndarray:
    left, right = sorted(handles)
    if left == right:
        return (values <= right + _EPSILON).astype(np.float32)
    return np.clip((right - values) / (right - left), 0.0, 1.0)


def _as_color(color: np.ndarray) -> np.ndarray:
    array = np.asarray(color, dtype=np.float32)
    if array.ndim == 2:
        array = array[:, :, np.newaxis]
    if array.ndim != 3:
        raise ValueError("Expected an (H, W, C) color array, got %r" % (array.shape,))
    return array


def _luminosity(color: np.ndarray) -> np.ndarray:
    if color.shape[2] < 3:
        return color[:, :, :1]
    return (
        0.299 * color[:, :, 0:1] + 0.587 * color[:, :, 1:2] + 0.114 * color[:, :, 2:3]
    )


@define
class BlendRangeChannel:
    """
    Blend If sliders of a single channel.

    Each attribute is a ``(left_handle, right_handle)`` tuple in the 0-255
    range. The handles differ when the slider is split, in which case the
    visibility fades linearly between them.

    .. py:attribute:: this_layer_black

        Black slider of "This Layer". Layer pixels darker than it are hidden.

    .. py:attribute:: this_layer_white

        White slider of "This Layer". Layer pixels brighter than it are hidden.

    .. py:attribute:: underlying_black

        Black slider of "Underlying Layer". The layer is hidden where the
        backdrop is darker than it.

    .. py:attribute:: underlying_white

        White slider of "Underlying Layer". The layer is hidden where the
        backdrop is brighter than it.
    """

    this_layer_black: Handles = field(
        default=(0, 0), converter=_to_handles, validator=_validate_handles
    )
    this_layer_white: Handles = field(
        default=(255, 255), converter=_to_handles, validator=_validate_handles
    )
    underlying_black: Handles = field(
        default=(0, 0), converter=_to_handles, validator=_validate_handles
    )
    underlying_white: Handles = field(
        default=(255, 255), converter=_to_handles, validator=_validate_handles
    )

    @classmethod
    def default(cls) -> Self:
        """Create a channel whose sliders cover the full range."""
        return cls()

    @classmethod
    def from_values(
        cls,
        this_layer_black: int = 0,
        this_layer_white: int = 255,
        underlying_black: int = 0,
        underlying_white: int = 255,
    ) -> Self:
        """Create a channel with non-split sliders at the given positions."""
        return cls(
            (this_layer_black, this_layer_black),
            (this_layer_white, this_layer_white),
            (underlying_black, underlying_black),
            (underlying_white, underlying_white),
        )

    @classmethod
    def from_raw(cls, raw_pair: Sequence[Sequence[int]]) -> Self:
        """
        Create a channel from the raw ``[(black, white), (black, white)]`` data
        of :py:class:`~psd_tools.psd.layer_and_mask.LayerBlendingRanges`.

        The first pair is the "This Layer" range and the second pair is the
        "Underlying Layer" range. Each uint16 value encodes a split slider,
        where the low byte is the left handle and the high byte is the right
        handle.
        """
        if len(raw_pair) != 2 or any(len(pair) != 2 for pair in raw_pair):
            raise ValueError(
                "Expected two (black, white) pairs, got %r" % (list(raw_pair),)
            )
        (this_black, this_white), (underlying_black, underlying_white) = raw_pair
        return cls(
            _unpack(this_black),
            _unpack(this_white),
            _unpack(underlying_black),
            _unpack(underlying_white),
        )

    def to_raw(self) -> list[tuple[int, int]]:
        """Convert to the raw ``[(black, white), (black, white)]`` data."""
        return [
            (_pack(self.this_layer_black), _pack(self.this_layer_white)),
            (_pack(self.underlying_black), _pack(self.underlying_white)),
        ]

    @property
    def is_default(self) -> bool:
        """Whether all the sliders cover the full range."""
        return self == self.default()

    @property
    def this_layer_black_split(self) -> bool:
        """Whether the black slider of "This Layer" is split."""
        return self.this_layer_black[0] != self.this_layer_black[1]

    @property
    def this_layer_white_split(self) -> bool:
        """Whether the white slider of "This Layer" is split."""
        return self.this_layer_white[0] != self.this_layer_white[1]

    @property
    def underlying_black_split(self) -> bool:
        """Whether the black slider of "Underlying Layer" is split."""
        return self.underlying_black[0] != self.underlying_black[1]

    @property
    def underlying_white_split(self) -> bool:
        """Whether the white slider of "Underlying Layer" is split."""
        return self.underlying_white[0] != self.underlying_white[1]

    def describe(self) -> str:
        """Human-readable summary of the slider positions."""
        return "This Layer: %s - %s, Underlying Layer: %s - %s" % (
            _format(self.this_layer_black),
            _format(self.this_layer_white),
            _format(self.underlying_black),
            _format(self.underlying_white),
        )

    def _visibility(self, source: np.ndarray, backdrop: np.ndarray) -> np.ndarray:
        """Visibility for source and backdrop values in the 0-255 scale."""
        return (
            _black_weight(source, self.this_layer_black)
            * _white_weight(source, self.this_layer_white)
            * _black_weight(backdrop, self.underlying_black)
            * _white_weight(backdrop, self.underlying_white)
        )


def _to_channel_list(channels: Iterable[BlendRangeChannel]) -> list[BlendRangeChannel]:
    return list(channels)


@define
class BlendRanges:
    """
    Blend If settings of a layer.

    Length, indexing, and iteration refer to :py:attr:`channels` only.

    .. py:attribute:: composite

        :py:class:`BlendRangeChannel` of the composite gray, evaluated on the
        luminosity ``0.299 * R + 0.587 * G + 0.114 * B``.

    .. py:attribute:: channels

        List of :py:class:`BlendRangeChannel`, one per channel in the
        document's channel order, e.g., red, green, and blue.
    """

    composite: BlendRangeChannel = field(
        factory=BlendRangeChannel, validator=instance_of(BlendRangeChannel)
    )
    channels: list[BlendRangeChannel] = field(
        factory=list,
        converter=_to_channel_list,
        validator=deep_iterable(instance_of(BlendRangeChannel)),
    )

    @classmethod
    def from_raw(cls, raw_blending_ranges: LayerBlendingRanges | None) -> Self:
        """
        Create from :py:class:`~psd_tools.psd.layer_and_mask.LayerBlendingRanges`.

        Null ranges result in no channels and a full-range composite.
        """
        composite = BlendRangeChannel.default()
        channels: list[BlendRangeChannel] = []
        if raw_blending_ranges is not None:
            if raw_blending_ranges.composite_ranges is not None:
                composite = BlendRangeChannel.from_raw(
                    raw_blending_ranges.composite_ranges
                )
            if raw_blending_ranges.channel_ranges is not None:
                channels = [
                    BlendRangeChannel.from_raw(channel_range)
                    for channel_range in raw_blending_ranges.channel_ranges
                ]
        return cls(composite, channels)

    @classmethod
    def from_channels(
        cls, composite: BlendRangeChannel, channels: Iterable[BlendRangeChannel]
    ) -> Self:
        """Create from the composite and per-channel settings."""
        return cls(composite, channels)

    def apply_to_raw(self, raw: LayerBlendingRanges) -> None:
        """
        Write the settings back to
        :py:class:`~psd_tools.psd.layer_and_mask.LayerBlendingRanges`.
        """
        raw.composite_ranges = self.composite.to_raw()
        raw.channel_ranges = [channel.to_raw() for channel in self.channels]

    @property
    def channel_count(self) -> int:
        """Number of per-channel settings."""
        return len(self.channels)

    @property
    def is_default(self) -> bool:
        """Whether the composite and all the channels cover the full range."""
        return self.composite.is_default and all(
            channel.is_default for channel in self.channels
        )

    def describe(self) -> str:
        """Human-readable summary of the composite and per-channel sliders."""
        lines = ["Gray: %s" % self.composite.describe()]
        lines.extend(
            "Channel %d: %s" % (index, channel.describe())
            for index, channel in enumerate(self.channels)
        )
        return "\n".join(lines)

    def compute_visibility(
        self, source_color: np.ndarray, backdrop_color: np.ndarray
    ) -> np.ndarray:
        """
        Compute the per-pixel visibility of a layer.

        The composite range is evaluated on the luminosity and each channel
        range on the corresponding channel. "This Layer" sliders use the source
        values and "Underlying Layer" sliders use the backdrop values. Channel
        ranges beyond the number of color channels are ignored.

        :param source_color: Layer color of shape (H, W, C) in [0, 1].
        :param backdrop_color: Backdrop color of shape (H, W, C) in [0, 1].
            Single-channel arrays broadcast to the channels of the other.
        :return: Visibility of shape (H, W, 1) in [0, 1].
        """
        source, backdrop = np.broadcast_arrays(
            np.clip(_as_color(source_color), 0.0, 1.0),
            np.clip(_as_color(backdrop_color), 0.0, 1.0),
        )
        visibility = self.composite._visibility(
            255.0 * _luminosity(source), 255.0 * _luminosity(backdrop)
        )
        for index, channel in enumerate(self.channels[: source.shape[2]]):
            visibility = visibility * channel._visibility(
                255.0 * source[:, :, index : index + 1],
                255.0 * backdrop[:, :, index : index + 1],
            )
        return visibility.astype(np.float32)

    def to_pil_mask(
        self, source_color: np.ndarray, backdrop_color: np.ndarray
    ) -> Image.Image:
        """
        Compute the visibility as a PIL Image in ``'L'`` mode.

        See :py:meth:`compute_visibility` for the parameters.
        """
        visibility = self.compute_visibility(source_color, backdrop_color)
        return Image.fromarray(np.round(255.0 * visibility[:, :, 0]).astype(np.uint8))

    def __len__(self) -> int:
        return len(self.channels)

    def __getitem__(self, index: int) -> BlendRangeChannel:
        return self.channels[index]

    def __iter__(self) -> Iterator[BlendRangeChannel]:
        return iter(self.channels)
