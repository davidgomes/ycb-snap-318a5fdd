"""
High-level Blend If (layer blending range) API.

Photoshop stores Blend If sliders as uint16 pairs. Each uint16 encodes a
possibly split handle: the low byte is the left handle and the high byte is
the right handle (0-255). When the handles are not split, both bytes are equal.
"""

from __future__ import annotations

from collections.abc import Callable, Iterator, Sequence
import numpy as np
from PIL import Image

from psd_tools.psd.layer_and_mask import LayerBlendingRanges

HandlePair = tuple[int, int]
RawChannel = list[tuple[int, int]]

_DEFAULT_BLACK: HandlePair = (0, 0)
_DEFAULT_WHITE: HandlePair = (255, 255)
_LUMINOSITY_WEIGHTS = (0.299, 0.587, 0.114)


def _clamp_handle(value: int) -> int:
    if not 0 <= value <= 255:
        raise ValueError(f"Blend range handle must be in [0, 255], got {value}")
    return int(value)


def _as_handle(value: Sequence[int]) -> HandlePair:
    if len(value) != 2:
        raise ValueError(f"Handle pair must have 2 values, got {value!r}")
    return (_clamp_handle(value[0]), _clamp_handle(value[1]))


def _split_u16(value: int) -> HandlePair:
    return (int(value) & 0xFF, (int(value) >> 8) & 0xFF)


def _join_u16(pair: HandlePair) -> int:
    left, right = pair
    return (left & 0xFF) | ((right & 0xFF) << 8)


def _slider_weight(
    values: np.ndarray, black: HandlePair, white: HandlePair
) -> np.ndarray:
    """Linear fade using split Blend If handles. ``values`` is in [0, 1]."""
    black_left, black_right = black[0] / 255.0, black[1] / 255.0
    white_left, white_right = white[0] / 255.0, white[1] / 255.0

    if black_right > black_left:
        fade_in = np.clip((values - black_left) / (black_right - black_left), 0.0, 1.0)
    else:
        fade_in = (values >= black_left).astype(np.float32)

    if white_right > white_left:
        fade_out = np.clip((white_right - values) / (white_right - white_left), 0.0, 1.0)
    else:
        fade_out = (values <= white_left).astype(np.float32)

    return (fade_in * fade_out).astype(np.float32)


def _luminosity(color: np.ndarray) -> np.ndarray:
    if color.shape[-1] >= 3:
        r = color[..., 0:1]
        g = color[..., 1:2]
        b = color[..., 2:3]
        return (
            _LUMINOSITY_WEIGHTS[0] * r
            + _LUMINOSITY_WEIGHTS[1] * g
            + _LUMINOSITY_WEIGHTS[2] * b
        ).astype(np.float32)
    return np.asarray(color[..., 0:1], dtype=np.float32)


class BlendRangeChannel:
    """Blend If sliders for one channel (composite gray or a color channel)."""

    def __init__(
        self,
        this_layer_black: Sequence[int] = _DEFAULT_BLACK,
        this_layer_white: Sequence[int] = _DEFAULT_WHITE,
        underlying_black: Sequence[int] = _DEFAULT_BLACK,
        underlying_white: Sequence[int] = _DEFAULT_WHITE,
    ) -> None:
        self._this_layer_black = _as_handle(this_layer_black)
        self._this_layer_white = _as_handle(this_layer_white)
        self._underlying_black = _as_handle(underlying_black)
        self._underlying_white = _as_handle(underlying_white)
        self._on_change: Callable[[], None] | None = None

    def _changed(self) -> None:
        if self._on_change is not None:
            self._on_change()

    @property
    def this_layer_black(self) -> HandlePair:
        return self._this_layer_black

    @this_layer_black.setter
    def this_layer_black(self, value: Sequence[int]) -> None:
        self._this_layer_black = _as_handle(value)
        self._changed()

    @property
    def this_layer_white(self) -> HandlePair:
        return self._this_layer_white

    @this_layer_white.setter
    def this_layer_white(self, value: Sequence[int]) -> None:
        self._this_layer_white = _as_handle(value)
        self._changed()

    @property
    def underlying_black(self) -> HandlePair:
        return self._underlying_black

    @underlying_black.setter
    def underlying_black(self, value: Sequence[int]) -> None:
        self._underlying_black = _as_handle(value)
        self._changed()

    @property
    def underlying_white(self) -> HandlePair:
        return self._underlying_white

    @underlying_white.setter
    def underlying_white(self, value: Sequence[int]) -> None:
        self._underlying_white = _as_handle(value)
        self._changed()

    @property
    def this_layer_black_split(self) -> bool:
        return self._this_layer_black[0] != self._this_layer_black[1]

    @property
    def this_layer_white_split(self) -> bool:
        return self._this_layer_white[0] != self._this_layer_white[1]

    @property
    def underlying_black_split(self) -> bool:
        return self._underlying_black[0] != self._underlying_black[1]

    @property
    def underlying_white_split(self) -> bool:
        return self._underlying_white[0] != self._underlying_white[1]

    @property
    def is_default(self) -> bool:
        return (
            self._this_layer_black == _DEFAULT_BLACK
            and self._this_layer_white == _DEFAULT_WHITE
            and self._underlying_black == _DEFAULT_BLACK
            and self._underlying_white == _DEFAULT_WHITE
        )

    @classmethod
    def default(cls) -> BlendRangeChannel:
        return cls()

    @classmethod
    def from_values(
        cls,
        this_layer_black: int = 0,
        this_layer_white: int = 255,
        underlying_black: int = 0,
        underlying_white: int = 255,
    ) -> BlendRangeChannel:
        """Create a channel with non-split sliders (each handle is ``(v, v)``)."""
        return cls(
            this_layer_black=(this_layer_black, this_layer_black),
            this_layer_white=(this_layer_white, this_layer_white),
            underlying_black=(underlying_black, underlying_black),
            underlying_white=(underlying_white, underlying_white),
        )

    @classmethod
    def from_raw(cls, raw_pair: RawChannel | None) -> BlendRangeChannel:
        """Parse a 2-element list of ``(black_uint16, white_uint16)`` pairs."""
        if not raw_pair:
            return cls.default()
        if len(raw_pair) != 2:
            raise ValueError(
                f"Channel range must have exactly 2 pairs, got {len(raw_pair)}"
            )
        this_black, this_white = raw_pair[0]
        underlying_black, underlying_white = raw_pair[1]
        return cls(
            this_layer_black=_split_u16(this_black),
            this_layer_white=_split_u16(this_white),
            underlying_black=_split_u16(underlying_black),
            underlying_white=_split_u16(underlying_white),
        )

    def to_raw(self) -> RawChannel:
        return [
            (_join_u16(self._this_layer_black), _join_u16(self._this_layer_white)),
            (_join_u16(self._underlying_black), _join_u16(self._underlying_white)),
        ]

    def compute_weight(
        self, source_values: np.ndarray, backdrop_values: np.ndarray
    ) -> np.ndarray:
        this_layer = _slider_weight(
            source_values, self._this_layer_black, self._this_layer_white
        )
        underlying = _slider_weight(
            backdrop_values, self._underlying_black, self._underlying_white
        )
        return (this_layer * underlying).astype(np.float32)

    def describe(self) -> str:
        return (
            "This Layer black {0}-{1}, white {2}-{3}; "
            "Underlying Layer black {4}-{5}, white {6}-{7}"
        ).format(
            *self._this_layer_black,
            *self._this_layer_white,
            *self._underlying_black,
            *self._underlying_white,
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
    """Blend If data for a layer: composite (gray) plus per-channel ranges."""

    def __init__(
        self,
        composite: BlendRangeChannel | None = None,
        channels: list[BlendRangeChannel] | None = None,
    ) -> None:
        self._composite = (
            composite if composite is not None else BlendRangeChannel.default()
        )
        self._channels = list(channels) if channels is not None else []
        self._raw: LayerBlendingRanges | None = None
        self._on_updated: Callable[[], None] | None = None
        self._bind_children()

    def _bind_children(self) -> None:
        self._composite._on_change = self._changed
        for channel in self._channels:
            channel._on_change = self._changed

    def _changed(self) -> None:
        if self._raw is not None:
            self.apply_to_raw(self._raw)
        if self._on_updated is not None:
            self._on_updated()

    def _attach(
        self,
        raw: LayerBlendingRanges,
        on_updated: Callable[[], None] | None = None,
    ) -> None:
        self._raw = raw
        self._on_updated = on_updated
        self._bind_children()

    @property
    def composite(self) -> BlendRangeChannel:
        return self._composite

    @composite.setter
    def composite(self, value: BlendRangeChannel) -> None:
        self._composite = value
        self._bind_children()
        self._changed()

    @property
    def channels(self) -> list[BlendRangeChannel]:
        return self._channels

    @channels.setter
    def channels(self, value: list[BlendRangeChannel]) -> None:
        self._channels = list(value)
        self._bind_children()
        self._changed()

    @property
    def channel_count(self) -> int:
        return len(self._channels)

    def __len__(self) -> int:
        return len(self._channels)

    def __iter__(self) -> Iterator[BlendRangeChannel]:
        return iter(self._channels)

    def __getitem__(self, index: int) -> BlendRangeChannel:
        return self._channels[index]

    @property
    def is_default(self) -> bool:
        return self._composite.is_default and all(ch.is_default for ch in self._channels)

    @classmethod
    def from_raw(cls, raw_blending_ranges: LayerBlendingRanges | None) -> BlendRanges:
        if raw_blending_ranges is None:
            return cls(BlendRangeChannel.default(), [])
        composite_raw = raw_blending_ranges.composite_ranges
        channel_raw = raw_blending_ranges.channel_ranges
        if composite_raw is None and channel_raw is None:
            return cls(BlendRangeChannel.default(), [])
        composite = BlendRangeChannel.from_raw(composite_raw)
        channels = [
            BlendRangeChannel.from_raw(channel) for channel in (channel_raw or [])
        ]
        return cls(composite, channels)

    @classmethod
    def from_channels(
        cls,
        composite: BlendRangeChannel,
        channels: list[BlendRangeChannel] | None = None,
    ) -> BlendRanges:
        return cls(composite, channels or [])

    def apply_to_raw(self, raw: LayerBlendingRanges) -> None:
        raw.composite_ranges = self._composite.to_raw()
        raw.channel_ranges = [channel.to_raw() for channel in self._channels]

    def compute_visibility(
        self, source_color: np.ndarray, backdrop_color: np.ndarray
    ) -> np.ndarray:
        """
        Return a Blend If weight array of shape ``(H, W, 1)`` in ``[0, 1]``.

        ``source_color`` and ``backdrop_color`` are float arrays in ``[0, 1]``.
        The composite (gray) range uses luminosity; per-channel ranges use the
        matching color component. This Layer uses source values; Underlying
        Layer uses backdrop values.
        """
        source = np.asarray(source_color, dtype=np.float32)
        backdrop = np.asarray(backdrop_color, dtype=np.float32)
        if source.ndim == 2:
            source = source[..., np.newaxis]
        if backdrop.ndim == 2:
            backdrop = backdrop[..., np.newaxis]

        weight = self._composite.compute_weight(
            _luminosity(source), _luminosity(backdrop)
        )
        channel_count = min(len(self._channels), source.shape[-1], backdrop.shape[-1])
        for index in range(channel_count):
            weight = weight * self._channels[index].compute_weight(
                source[..., index : index + 1],
                backdrop[..., index : index + 1],
            )
        return np.clip(weight, 0.0, 1.0).astype(np.float32)

    def to_pil_mask(
        self, source_color: np.ndarray, backdrop_color: np.ndarray
    ) -> Image.Image:
        weight = self.compute_visibility(source_color, backdrop_color)
        pixels = np.squeeze(np.clip(weight, 0.0, 1.0) * 255.0, axis=2).astype(np.uint8)
        return Image.fromarray(pixels, mode="L")

    def describe(self) -> str:
        lines = [f"Composite: {self._composite.describe()}"]
        if not self._channels:
            lines.append("Channels: none")
        else:
            for index, channel in enumerate(self._channels):
                lines.append(f"Channel {index}: {channel.describe()}")
        return "\n".join(lines)

    def __repr__(self) -> str:
        return (
            f"{self.__class__.__name__}(composite={self._composite!r}, "
            f"channels={self._channels!r})"
        )
