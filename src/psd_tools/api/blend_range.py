"""
Blend range module.

Blend ranges correspond to the "Blend If" sliders in Photoshop's Layer Style
dialog. Each range restricts the visibility of a layer's pixels based on the
values of the layer itself ("This Layer") and of the pixels beneath it
("Underlying Layer").

Each slider has a black and a white end. Every end consists of two handles,
``(left_handle, right_handle)`` in the 0-255 range. When both handles share
the same position, the slider is a hard cut-off; when they are split, the
visibility fades linearly between the two handles.

Example::

    from psd_tools import PSDImage

    psd = PSDImage.open('example.psd')
    layer = psd[0]

    blend_ranges = layer.blend_ranges
    print(blend_ranges.describe())

    # Hide dark pixels of this layer with a soft transition.
    blend_ranges.composite.this_layer_black = (32, 96)
    layer.blend_ranges = blend_ranges
    psd.save('output.psd')
"""

from typing import Any, Iterator, Sequence

import numpy as np
from attrs import define, field
from PIL import Image

from psd_tools.psd.layer_and_mask import LayerBlendingRanges

Handles = tuple[int, int]

_BLACK: Handles = (0, 0)
_WHITE: Handles = (255, 255)

# Tolerance for float rounding when comparing [0, 1] values scaled to 0-255.
_EPSILON = 1e-3


def _to_handles(value: Any) -> Handles:
    if isinstance(value, (int, np.integer)):
        handles = (int(value), int(value))
    else:
        handles = tuple(int(x) for x in value)  # type: ignore[assignment]
    if len(handles) != 2:
        raise ValueError(f"Slider must have 2 handles, got {len(handles)}")
    for x in handles:
        if not 0 <= x <= 255:
            raise ValueError(f"Slider handle must be in range [0, 255], got {x}")
    return handles


def _decode(value: int) -> Handles:
    value = int(value)
    return (value & 0xFF, (value >> 8) & 0xFF)


def _encode(handles: Handles) -> int:
    return (handles[0] & 0xFF) | ((handles[1] & 0xFF) << 8)


def _format_handles(handles: Handles) -> str:
    if handles[0] == handles[1]:
        return str(handles[0])
    return "%d/%d" % handles


def _black_weight(values: np.ndarray, handles: Handles) -> np.ndarray:
    left, right = min(handles), max(handles)
    if left == right:
        return (values >= left - _EPSILON).astype(np.float32)
    return np.clip((values - left) / float(right - left), 0.0, 1.0)


def _white_weight(values: np.ndarray, handles: Handles) -> np.ndarray:
    left, right = min(handles), max(handles)
    if left == right:
        return (values <= right + _EPSILON).astype(np.float32)
    return np.clip((right - values) / float(right - left), 0.0, 1.0)


def _slider_weight(
    values: np.ndarray, black: Handles, white: Handles
) -> np.ndarray | None:
    if black == _BLACK and white == _WHITE:
        return None
    return _black_weight(values, black) * _white_weight(values, white)


@define(eq=True)
class BlendRangeChannel:
    """
    Blend range of a single channel (or the composite gray channel).

    Each attribute is a ``(left_handle, right_handle)`` tuple in the 0-255
    range, and is writable.

    .. py:attribute:: this_layer_black

        Black end of the "This Layer" slider.

    .. py:attribute:: this_layer_white

        White end of the "This Layer" slider.

    .. py:attribute:: underlying_black

        Black end of the "Underlying Layer" slider.

    .. py:attribute:: underlying_white

        White end of the "Underlying Layer" slider.
    """

    this_layer_black: Handles = field(default=_BLACK, converter=_to_handles)
    this_layer_white: Handles = field(default=_WHITE, converter=_to_handles)
    underlying_black: Handles = field(default=_BLACK, converter=_to_handles)
    underlying_white: Handles = field(default=_WHITE, converter=_to_handles)

    @classmethod
    def default(cls) -> "BlendRangeChannel":
        """Create a channel with full range, i.e., no blend-if effect."""
        return cls()

    @classmethod
    def from_values(
        cls,
        this_layer_black: int = 0,
        this_layer_white: int = 255,
        underlying_black: int = 0,
        underlying_white: int = 255,
    ) -> "BlendRangeChannel":
        """
        Create a channel with non-split sliders.

        :param this_layer_black: Black position of "This Layer" slider.
        :param this_layer_white: White position of "This Layer" slider.
        :param underlying_black: Black position of "Underlying Layer" slider.
        :param underlying_white: White position of "Underlying Layer" slider.
        """
        return cls(
            (this_layer_black, this_layer_black),
            (this_layer_white, this_layer_white),
            (underlying_black, underlying_black),
            (underlying_white, underlying_white),
        )

    @classmethod
    def from_raw(cls, raw_pair: Sequence[Sequence[int]]) -> "BlendRangeChannel":
        """
        Create from the raw pair of ``(black, white)`` uint16 values.

        The first pair is "This Layer" (source), the second is "Underlying
        Layer" (destination). Each uint16 encodes a slider end where the low
        byte is the left handle and the high byte is the right handle.
        """
        if len(raw_pair) != 2:
            raise ValueError(f"Blend range must have 2 pairs, got {len(raw_pair)}")
        (src_black, src_white), (dst_black, dst_white) = raw_pair
        return cls(
            _decode(src_black),
            _decode(src_white),
            _decode(dst_black),
            _decode(dst_white),
        )

    def to_raw(self) -> list[tuple[int, int]]:
        """Convert to the raw pair of ``(black, white)`` uint16 values."""
        return [
            (_encode(self.this_layer_black), _encode(self.this_layer_white)),
            (_encode(self.underlying_black), _encode(self.underlying_white)),
        ]

    @property
    def is_default(self) -> bool:
        """Whether all sliders are at the default full-range positions."""
        return (
            self.this_layer_black == _BLACK
            and self.this_layer_white == _WHITE
            and self.underlying_black == _BLACK
            and self.underlying_white == _WHITE
        )

    @property
    def this_layer_black_split(self) -> bool:
        """Whether the black end of "This Layer" slider is split."""
        return self.this_layer_black[0] != self.this_layer_black[1]

    @property
    def this_layer_white_split(self) -> bool:
        """Whether the white end of "This Layer" slider is split."""
        return self.this_layer_white[0] != self.this_layer_white[1]

    @property
    def underlying_black_split(self) -> bool:
        """Whether the black end of "Underlying Layer" slider is split."""
        return self.underlying_black[0] != self.underlying_black[1]

    @property
    def underlying_white_split(self) -> bool:
        """Whether the white end of "Underlying Layer" slider is split."""
        return self.underlying_white[0] != self.underlying_white[1]

    def compute_weight(
        self, source_values: np.ndarray, backdrop_values: np.ndarray
    ) -> np.ndarray | None:
        """
        Compute the visibility weight from channel values in the 0-255 range.

        :return: Weight array in [0, 1], or `None` when there is no effect.
        """
        weight_source = _slider_weight(
            source_values, self.this_layer_black, self.this_layer_white
        )
        weight_backdrop = _slider_weight(
            backdrop_values, self.underlying_black, self.underlying_white
        )
        if weight_source is None:
            return weight_backdrop
        if weight_backdrop is None:
            return weight_source
        return weight_source * weight_backdrop

    def describe(self) -> str:
        """Human-readable description of the slider positions."""
        return "This Layer: %s - %s, Underlying Layer: %s - %s" % (
            _format_handles(self.this_layer_black),
            _format_handles(self.this_layer_white),
            _format_handles(self.underlying_black),
            _format_handles(self.underlying_white),
        )


def _luminosity(color: np.ndarray) -> np.ndarray:
    if color.shape[2] >= 3:
        return 0.299 * color[:, :, 0] + 0.587 * color[:, :, 1] + 0.114 * color[:, :, 2]
    return color[:, :, 0]


def _as_color_array(value: Any) -> np.ndarray:
    array = np.asarray(value, dtype=np.float32)
    if array.ndim == 2:
        array = array[:, :, np.newaxis]
    if array.ndim != 3:
        raise ValueError(f"Expected (H, W) or (H, W, C) array, got {array.shape}")
    return array


@define(eq=True)
class BlendRanges:
    """
    Blend ranges ("Blend If" settings) of a layer.

    Length, indexing, and iteration operate on the per-channel ranges only;
    the composite gray range is available as :py:attr:`composite`.

    .. py:attribute:: composite

        :py:class:`BlendRangeChannel` for the composite gray channel.

    .. py:attribute:: channels

        List of :py:class:`BlendRangeChannel` for each color channel.
    """

    composite: BlendRangeChannel = field(factory=BlendRangeChannel.default)
    channels: list[BlendRangeChannel] = field(factory=list)

    @classmethod
    def from_raw(cls, raw_blending_ranges: LayerBlendingRanges) -> "BlendRanges":
        """Create from the low-level :py:class:`LayerBlendingRanges`."""
        composite_ranges = raw_blending_ranges.composite_ranges
        channel_ranges = raw_blending_ranges.channel_ranges
        composite = (
            BlendRangeChannel.from_raw(composite_ranges)
            if composite_ranges is not None
            else BlendRangeChannel.default()
        )
        channels = [BlendRangeChannel.from_raw(pair) for pair in channel_ranges or []]
        return cls(composite, channels)

    @classmethod
    def from_channels(
        cls,
        composite: BlendRangeChannel,
        channels: Sequence[BlendRangeChannel],
    ) -> "BlendRanges":
        """Create from explicit :py:class:`BlendRangeChannel` objects."""
        return cls(composite, list(channels))

    def apply_to_raw(self, raw: LayerBlendingRanges) -> None:
        """Write these ranges back to the low-level :py:class:`LayerBlendingRanges`."""
        if raw.composite_ranges is None and not self.channels and self.is_default:
            return
        raw.composite_ranges = self.composite.to_raw()
        raw.channel_ranges = [channel.to_raw() for channel in self.channels]

    @property
    def channel_count(self) -> int:
        """Number of per-channel ranges."""
        return len(self.channels)

    def __len__(self) -> int:
        return len(self.channels)

    def __getitem__(self, index: int) -> BlendRangeChannel:
        return self.channels[index]

    def __iter__(self) -> Iterator[BlendRangeChannel]:
        return iter(self.channels)

    @property
    def is_default(self) -> bool:
        """Whether the composite and all channel ranges are at defaults."""
        return self.composite.is_default and all(
            channel.is_default for channel in self.channels
        )

    def describe(self) -> str:
        """Human-readable description of all ranges."""
        lines = ["Gray: %s" % self.composite.describe()]
        for index, channel in enumerate(self.channels):
            lines.append("Channel %d: %s" % (index, channel.describe()))
        return "\n".join(lines)

    def compute_visibility(
        self, source_color: np.ndarray, backdrop_color: np.ndarray
    ) -> np.ndarray:
        """
        Compute the per-pixel visibility weight of the layer.

        The composite (gray) range is evaluated on luminosity
        (``0.299*R + 0.587*G + 0.114*B``), and each channel range is evaluated
        on the corresponding color channel. "This Layer" sliders use the
        source values and "Underlying Layer" sliders use the backdrop values.

        :param source_color: Layer color array of shape (H, W, C) in [0, 1].
        :param backdrop_color: Backdrop color array of shape (H, W, C) in [0, 1].
        :return: Weight array of shape (H, W, 1) in [0, 1].
        """
        source = _as_color_array(source_color)
        backdrop = _as_color_array(backdrop_color)
        if source.shape[2] != backdrop.shape[2]:
            channels = max(source.shape[2], backdrop.shape[2])
            if source.shape[2] == 1:
                source = np.repeat(source, channels, axis=2)
            if backdrop.shape[2] == 1:
                backdrop = np.repeat(backdrop, channels, axis=2)
        source = np.clip(source, 0.0, 1.0) * 255.0
        backdrop = np.clip(backdrop, 0.0, 1.0) * 255.0
        height, width = np.broadcast_shapes(source.shape[:2], backdrop.shape[:2])

        weight = np.ones((height, width), dtype=np.float32)
        if not self.composite.is_default:
            value = self.composite.compute_weight(
                _luminosity(source), _luminosity(backdrop)
            )
            if value is not None:
                weight = weight * value

        count = min(len(self.channels), source.shape[2], backdrop.shape[2])
        for index in range(count):
            channel = self.channels[index]
            if channel.is_default:
                continue
            value = channel.compute_weight(source[:, :, index], backdrop[:, :, index])
            if value is not None:
                weight = weight * value

        return np.clip(weight, 0.0, 1.0).astype(np.float32)[:, :, np.newaxis]

    def to_pil_mask(
        self, source_color: np.ndarray, backdrop_color: np.ndarray
    ) -> Image.Image:
        """
        Compute the visibility weight as a PIL image in ``'L'`` mode.

        See :py:meth:`compute_visibility` for the parameters.
        """
        weight = self.compute_visibility(source_color, backdrop_color)
        return Image.fromarray(
            np.round(weight[:, :, 0] * 255.0).astype(np.uint8)
        ).convert("L")
