"""
Blend If ranges.

Photoshop stores each layer's Blend If sliders in
:py:class:`~psd_tools.psd.layer_and_mask.LayerBlendingRanges` as raw uint16
pairs. This module exposes those sliders as editable endpoints and can turn
them into a per-pixel visibility weight.

Each uint16 packs one split slider: the low byte is the left handle and the
high byte is the right handle, both in ``0..255``. A slider that is not split
stores the same value in both bytes. The first pair is "This Layer" (black,
then white). The second pair is "Underlying Layer".

Example::

    ranges = layer.blend_ranges
    ranges.composite.this_layer_black = (32, 64)  # split shadow slider
    layer.blend_ranges = ranges
"""

from __future__ import annotations

from collections.abc import Callable, Iterable, Iterator, Sequence
from numbers import Integral, Real
from typing import Any

import numpy as np
from PIL import Image

from psd_tools.psd.layer_and_mask import LayerBlendingRanges

# Rec. 601 luminosity, as used by the composite gray Blend If slider.
_LUMA_WEIGHTS = (0.299, 0.587, 0.114)
_FULL_BLACK = (0, 0)
_FULL_WHITE = (255, 255)


def _normalize_byte(value: object) -> int:
    """Return ``value`` as an integer in ``0..255``."""
    if isinstance(value, (bool, np.bool_)):
        raise TypeError(f"Blend If stop must be an integer, got {value!r}")
    if isinstance(value, Integral):
        byte = int(value)
    elif isinstance(value, Real):
        number = float(value)
        if not number.is_integer():
            raise ValueError(f"Blend If stop must be an integer, got {value!r}")
        byte = int(number)
    else:
        raise TypeError(f"Blend If stop must be an integer, got {value!r}")
    if not 0 <= byte <= 255:
        raise ValueError(f"Blend If stop must be in 0..255, got {byte}")
    return byte


def _normalize_handle(value: object) -> tuple[int, int]:
    """Return a ``(left_handle, right_handle)`` pair in ``0..255``."""
    if isinstance(value, (str, bytes)) or not isinstance(value, Sequence):
        raise TypeError(f"Blend If handle must be a (left, right) pair, got {value!r}")
    if len(value) != 2:
        raise ValueError(f"Blend If handle must be a (left, right) pair, got {value!r}")
    return (_normalize_byte(value[0]), _normalize_byte(value[1]))


def _pack_handle(handle: tuple[int, int]) -> int:
    """Pack a split slider into a uint16 (low byte = left, high byte = right)."""
    left, right = handle
    return ((right & 0xFF) << 8) | (left & 0xFF)


def _unpack_handle(value: int) -> tuple[int, int]:
    """Unpack a uint16 split slider into ``(left_handle, right_handle)``."""
    return (int(value) & 0xFF, (int(value) >> 8) & 0xFF)


def _as_color_array(color: Any) -> np.ndarray:
    """Return ``color`` as a float array of shape ``(H, W, C)``."""
    array = np.asarray(color)
    if array.ndim == 2:
        array = array[:, :, np.newaxis]
    if array.ndim != 3 or array.shape[2] == 0:
        raise ValueError(
            "color array must have shape (H, W) or (H, W, C), got "
            f"{np.asarray(color).shape}"
        )
    return array


def _luminosity(color: np.ndarray) -> np.ndarray:
    """Luminosity plane, shape ``(H, W)``.

    Three or more channels use ``0.299*R + 0.587*G + 0.114*B``. A single
    channel is already gray. Two channels use the same weights renormalized
    over the first two planes.
    """
    channels = color.shape[2]
    if channels >= 3:
        return (
            color[:, :, 0] * _LUMA_WEIGHTS[0]
            + color[:, :, 1] * _LUMA_WEIGHTS[1]
            + color[:, :, 2] * _LUMA_WEIGHTS[2]
        )
    if channels == 1:
        return color[:, :, 0]
    total = _LUMA_WEIGHTS[0] + _LUMA_WEIGHTS[1]
    return color[:, :, 0] * (_LUMA_WEIGHTS[0] / total) + color[:, :, 1] * (
        _LUMA_WEIGHTS[1] / total
    )


def _endpoint_weight(
    values: np.ndarray, handle: tuple[int, int], *, hidden_below: bool
) -> np.ndarray:
    """Linear weight for one Blend If endpoint.

    ``hidden_below`` is the black slider: samples below the range fade out.
    The white slider fades out above the range. Equal handles are a step.
    Split handles fade linearly between the two positions.
    """
    left, right = handle
    if left == right:
        threshold = np.float32(left) / np.float32(255)
        if hidden_below:
            return np.where(
                values < threshold, np.float32(0.0), np.float32(1.0)
            ).astype(np.float32, copy=False)
        return np.where(values > threshold, np.float32(0.0), np.float32(1.0)).astype(
            np.float32, copy=False
        )

    lo = np.float32(left) / np.float32(255)
    hi = np.float32(right) / np.float32(255)
    if hidden_below:
        weight = (values - lo) / (hi - lo)
    else:
        weight = (hi - values) / (hi - lo)
    return np.clip(weight, 0.0, 1.0).astype(np.float32, copy=False)


def _slider_weight(
    values: np.ndarray, black: tuple[int, int], white: tuple[int, int]
) -> np.ndarray:
    """Visibility for one color plane. ``values`` are in ``[0, 1]``."""
    plane = np.asarray(values, dtype=np.float32)
    if black == _FULL_BLACK and white == _FULL_WHITE:
        return np.ones(plane.shape, dtype=np.float32)
    black_weight = _endpoint_weight(plane, black, hidden_below=True)
    white_weight = _endpoint_weight(plane, white, hidden_below=False)
    return np.minimum(black_weight, white_weight)


class BlendRangeChannel:
    """Blend If sliders for one channel.

    Each endpoint is a ``(left_handle, right_handle)`` tuple in ``0..255``.
    When the handles differ, the slider is split and visibility fades linearly
    between them.

    :param this_layer_black: This Layer black slider. Default ``(0, 0)``.
    :param this_layer_white: This Layer white slider. Default ``(255, 255)``.
    :param underlying_black: Underlying Layer black slider. Default ``(0, 0)``.
    :param underlying_white: Underlying Layer white slider. Default ``(255, 255)``.
    """

    def __init__(
        self,
        this_layer_black: tuple[int, int] = _FULL_BLACK,
        this_layer_white: tuple[int, int] = _FULL_WHITE,
        underlying_black: tuple[int, int] = _FULL_BLACK,
        underlying_white: tuple[int, int] = _FULL_WHITE,
    ) -> None:
        self._on_change: Callable[[], None] | None = None
        self._this_layer_black = _normalize_handle(this_layer_black)
        self._this_layer_white = _normalize_handle(this_layer_white)
        self._underlying_black = _normalize_handle(underlying_black)
        self._underlying_white = _normalize_handle(underlying_white)

    @property
    def this_layer_black(self) -> tuple[int, int]:
        """This Layer black slider, ``(left_handle, right_handle)``."""
        return self._this_layer_black

    @this_layer_black.setter
    def this_layer_black(self, value: tuple[int, int]) -> None:
        self._set_handle("_this_layer_black", value)

    @property
    def this_layer_white(self) -> tuple[int, int]:
        """This Layer white slider, ``(left_handle, right_handle)``."""
        return self._this_layer_white

    @this_layer_white.setter
    def this_layer_white(self, value: tuple[int, int]) -> None:
        self._set_handle("_this_layer_white", value)

    @property
    def underlying_black(self) -> tuple[int, int]:
        """Underlying Layer black slider, ``(left_handle, right_handle)``."""
        return self._underlying_black

    @underlying_black.setter
    def underlying_black(self, value: tuple[int, int]) -> None:
        self._set_handle("_underlying_black", value)

    @property
    def underlying_white(self) -> tuple[int, int]:
        """Underlying Layer white slider, ``(left_handle, right_handle)``."""
        return self._underlying_white

    @underlying_white.setter
    def underlying_white(self, value: tuple[int, int]) -> None:
        self._set_handle("_underlying_white", value)

    @property
    def this_layer_black_split(self) -> bool:
        """True when the This Layer black slider handles differ."""
        return self._this_layer_black[0] != self._this_layer_black[1]

    @property
    def this_layer_white_split(self) -> bool:
        """True when the This Layer white slider handles differ."""
        return self._this_layer_white[0] != self._this_layer_white[1]

    @property
    def underlying_black_split(self) -> bool:
        """True when the Underlying Layer black slider handles differ."""
        return self._underlying_black[0] != self._underlying_black[1]

    @property
    def underlying_white_split(self) -> bool:
        """True when the Underlying Layer white slider handles differ."""
        return self._underlying_white[0] != self._underlying_white[1]

    @property
    def is_default(self) -> bool:
        """True when every slider sits at the full ``0..255`` range."""
        return (
            self._this_layer_black == _FULL_BLACK
            and self._this_layer_white == _FULL_WHITE
            and self._underlying_black == _FULL_BLACK
            and self._underlying_white == _FULL_WHITE
        )

    @classmethod
    def default(cls) -> BlendRangeChannel:
        """Return a channel whose sliders cover the full range."""
        return cls()

    @classmethod
    def from_values(
        cls,
        this_layer_black: int = 0,
        this_layer_white: int = 255,
        underlying_black: int = 0,
        underlying_white: int = 255,
    ) -> BlendRangeChannel:
        """Create a channel with unsplit sliders.

        Each argument is a single stop in ``0..255``. Both handles of that
        slider are set to the stop. Omitted arguments keep the full range.
        """

        def stop(value: int) -> tuple[int, int]:
            byte = _normalize_byte(value)
            return (byte, byte)

        return cls(
            this_layer_black=stop(this_layer_black),
            this_layer_white=stop(this_layer_white),
            underlying_black=stop(underlying_black),
            underlying_white=stop(underlying_white),
        )

    @classmethod
    def from_raw(cls, raw_pair: Sequence[Sequence[int]]) -> BlendRangeChannel:
        """Parse one raw channel range.

        ``raw_pair`` is a 2-element sequence of ``(black_uint16, white_uint16)``
        pairs. Index 0 is This Layer and index 1 is Underlying Layer. Each
        uint16 stores a split slider: low byte = left handle, high byte =
        right handle.
        """
        if len(raw_pair) != 2:
            raise ValueError(
                f"blend range channel must contain exactly 2 pairs, got {len(raw_pair)}"
            )
        this_layer, underlying = raw_pair
        if len(this_layer) != 2 or len(underlying) != 2:
            raise ValueError(
                "each blend range pair must be (black_uint16, white_uint16)"
            )
        return cls(
            this_layer_black=_unpack_handle(int(this_layer[0])),
            this_layer_white=_unpack_handle(int(this_layer[1])),
            underlying_black=_unpack_handle(int(underlying[0])),
            underlying_white=_unpack_handle(int(underlying[1])),
        )

    def to_raw(self) -> list[tuple[int, int]]:
        """Return ``[(this_black, this_white), (underlying_black, underlying_white)]``."""
        return [
            (
                _pack_handle(self._this_layer_black),
                _pack_handle(self._this_layer_white),
            ),
            (
                _pack_handle(self._underlying_black),
                _pack_handle(self._underlying_white),
            ),
        ]

    def describe(self) -> str:
        """Return a non-empty description of the four sliders."""

        def slider(name: str, handle: tuple[int, int]) -> str:
            if handle[0] == handle[1]:
                return f"{name} {handle[0]}"
            return f"{name} {handle[0]}-{handle[1]}"

        return (
            "This Layer "
            f"{slider('black', self._this_layer_black)}, "
            f"{slider('white', self._this_layer_white)}; "
            "Underlying "
            f"{slider('black', self._underlying_black)}, "
            f"{slider('white', self._underlying_white)}"
        )

    def _weight(self, source: np.ndarray, backdrop: np.ndarray) -> np.ndarray:
        """Per-pixel weight from This Layer and Underlying Layer sliders."""
        this_weight = _slider_weight(
            source, self._this_layer_black, self._this_layer_white
        )
        underlying_weight = _slider_weight(
            backdrop, self._underlying_black, self._underlying_white
        )
        return this_weight * underlying_weight

    def _set_handle(self, name: str, value: tuple[int, int]) -> None:
        handle = _normalize_handle(value)
        if handle == getattr(self, name):
            return
        setattr(self, name, handle)
        if self._on_change is not None:
            self._on_change()

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, BlendRangeChannel):
            return NotImplemented
        return (
            self._this_layer_black == other._this_layer_black
            and self._this_layer_white == other._this_layer_white
            and self._underlying_black == other._underlying_black
            and self._underlying_white == other._underlying_white
        )

    def __repr__(self) -> str:
        return (
            f"{self.__class__.__name__}("
            f"this_layer_black={self._this_layer_black}, "
            f"this_layer_white={self._this_layer_white}, "
            f"underlying_black={self._underlying_black}, "
            f"underlying_white={self._underlying_white})"
        )


class BlendRanges:
    """Composite gray Blend If range plus one range per color channel.

    ``len``, indexing (including negative indices), and iteration yield
    ``channels`` only. The composite gray range is available as
    :py:attr:`composite`.

    :param composite: Gray / luminosity range.
    :param channels: Per-channel ranges, in document channel order.
    """

    def __init__(
        self, composite: BlendRangeChannel, channels: Iterable[BlendRangeChannel]
    ) -> None:
        if not isinstance(composite, BlendRangeChannel):
            raise TypeError(
                f"composite must be a BlendRangeChannel, got {type(composite).__name__}"
            )
        self._composite = composite
        self._channels: list[BlendRangeChannel] = []
        for channel in channels:
            if not isinstance(channel, BlendRangeChannel):
                raise TypeError(
                    "channels must contain BlendRangeChannel instances, "
                    f"got {type(channel).__name__}"
                )
            self._channels.append(channel)
        self._on_change: Callable[[], None] | None = None

    @property
    def composite(self) -> BlendRangeChannel:
        """Gray Blend If range. Luminosity is ``0.299*R + 0.587*G + 0.114*B``."""
        return self._composite

    @composite.setter
    def composite(self, value: BlendRangeChannel) -> None:
        if not isinstance(value, BlendRangeChannel):
            raise TypeError(
                f"composite must be a BlendRangeChannel, got {type(value).__name__}"
            )
        self._composite = value
        value._on_change = self._on_change
        self._emit()

    @property
    def channels(self) -> list[BlendRangeChannel]:
        """Per-channel Blend If ranges. Does not include :py:attr:`composite`."""
        return self._channels

    @channels.setter
    def channels(self, value: Iterable[BlendRangeChannel]) -> None:
        updated: list[BlendRangeChannel] = []
        for channel in value:
            if not isinstance(channel, BlendRangeChannel):
                raise TypeError(
                    "channels must contain BlendRangeChannel instances, "
                    f"got {type(channel).__name__}"
                )
            channel._on_change = self._on_change
            updated.append(channel)
        self._channels = updated
        self._emit()

    @property
    def channel_count(self) -> int:
        """Number of per-channel ranges."""
        return len(self._channels)

    @property
    def is_default(self) -> bool:
        """True when the composite range and every channel are at full range."""
        return self._composite.is_default and all(
            channel.is_default for channel in self._channels
        )

    def __len__(self) -> int:
        return len(self._channels)

    def __getitem__(self, index: int) -> BlendRangeChannel:
        return self._channels[index]

    def __iter__(self) -> Iterator[BlendRangeChannel]:
        return iter(self._channels)

    @classmethod
    def from_raw(cls, raw_blending_ranges: LayerBlendingRanges) -> BlendRanges:
        """Create ranges from a low-level :py:class:`LayerBlendingRanges`.

        Null composite data becomes a full-range composite channel. Null
        channel data becomes an empty channel list.
        """
        composite_raw = raw_blending_ranges.composite_ranges
        channel_raw = raw_blending_ranges.channel_ranges
        if composite_raw is None:
            composite = BlendRangeChannel.default()
        else:
            composite = BlendRangeChannel.from_raw(composite_raw)
        channels: list[BlendRangeChannel] = []
        if channel_raw:
            channels = [BlendRangeChannel.from_raw(item) for item in channel_raw]
        return cls(composite, channels)

    @classmethod
    def from_channels(
        cls, composite: BlendRangeChannel, channels: Iterable[BlendRangeChannel]
    ) -> BlendRanges:
        """Create ranges from an explicit composite channel and channel list."""
        return cls(composite, channels)

    def apply_to_raw(self, raw: LayerBlendingRanges) -> None:
        """Write these sliders onto a low-level blending-ranges record."""
        if not isinstance(raw, LayerBlendingRanges):
            raise TypeError(
                f"raw must be a LayerBlendingRanges, got {type(raw).__name__}"
            )
        raw.composite_ranges = self._composite.to_raw()
        raw.channel_ranges = [channel.to_raw() for channel in self._channels]

    def describe(self) -> str:
        """Return a non-empty description of the composite and channel ranges."""
        lines = [f"composite: {self._composite.describe()}"]
        if not self._channels:
            lines.append("channels: none")
        else:
            for index, channel in enumerate(self._channels):
                lines.append(f"channel {index}: {channel.describe()}")
        return "\n".join(lines)

    def compute_visibility(self, source_color: Any, backdrop_color: Any) -> np.ndarray:
        """Return a Blend If weight of shape ``(H, W, 1)`` in ``[0, 1]``.

        ``source_color`` and ``backdrop_color`` are float arrays in ``[0, 1]``
        with shape ``(H, W)`` or ``(H, W, C)``. The composite slider reads
        luminosity (``0.299*R + 0.587*G + 0.114*B`` when at least three
        channels are present). Each per-channel slider reads that channel.
        "This Layer" uses the source and "Underlying Layer" uses the backdrop.
        Split sliders fade linearly. Weights are multiplied together.
        """
        source = _as_color_array(source_color)
        backdrop = _as_color_array(backdrop_color)
        if source.shape[:2] != backdrop.shape[:2]:
            raise ValueError(
                "source_color and backdrop_color must have the same height and "
                f"width, got {source.shape[:2]} and {backdrop.shape[:2]}"
            )
        height, width = source.shape[:2]
        if self.is_default:
            return np.ones((height, width, 1), dtype=np.float32)

        weight = np.ones((height, width), dtype=np.float32)
        if not self._composite.is_default:
            source_luma = np.asarray(_luminosity(source), dtype=np.float32)
            backdrop_luma = np.asarray(_luminosity(backdrop), dtype=np.float32)
            weight *= self._composite._weight(source_luma, backdrop_luma)

        source_planes, backdrop_planes = _align_channels(source, backdrop)
        count = min(
            len(self._channels), source_planes.shape[2], backdrop_planes.shape[2]
        )
        for index in range(count):
            channel = self._channels[index]
            if channel.is_default:
                continue
            weight *= channel._weight(
                source_planes[:, :, index], backdrop_planes[:, :, index]
            )
        return np.clip(weight, 0.0, 1.0).astype(np.float32, copy=False)[
            :, :, np.newaxis
        ]

    def to_pil_mask(self, source_color: Any, backdrop_color: Any) -> Image.Image:
        """Return :py:meth:`compute_visibility` as an ``'L'`` mode image."""
        weight = self.compute_visibility(source_color, backdrop_color)
        plane = np.clip(weight[:, :, 0] * 255.0, 0, 255).astype(np.uint8)
        return Image.fromarray(plane, mode="L")

    def _listen(self, callback: Callable[[], None] | None) -> None:
        """Notify ``callback`` when a slider on this object changes."""
        self._on_change = callback
        self._composite._on_change = callback
        for channel in self._channels:
            channel._on_change = callback

    def _emit(self) -> None:
        if self._on_change is not None:
            self._on_change()

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, BlendRanges):
            return NotImplemented
        return self._composite == other._composite and self._channels == other._channels

    def __repr__(self) -> str:
        return (
            f"{self.__class__.__name__}("
            f"composite={self._composite!r}, channels={self._channels!r})"
        )


def _align_channels(
    source: np.ndarray, backdrop: np.ndarray
) -> tuple[np.ndarray, np.ndarray]:
    """Repeat a single-channel plane so per-channel sliders have a partner."""
    source_count = source.shape[2]
    backdrop_count = backdrop.shape[2]
    if source_count == 1 and backdrop_count > 1:
        source = np.repeat(source, backdrop_count, axis=2)
    elif backdrop_count == 1 and source_count > 1:
        backdrop = np.repeat(backdrop, source_count, axis=2)
    return source, backdrop
