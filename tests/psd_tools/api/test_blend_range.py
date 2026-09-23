from typing import Any

import numpy as np
import pytest

from psd_tools.api.blend_range import BlendRangeChannel, BlendRanges
from psd_tools.psd.layer_and_mask import LayerBlendingRanges

FULL_RANGE = [(0, 65535), (0, 65535)]


def _color(*values: Any) -> np.ndarray:
    """Build a 1-row (1, W, C) color array from per-pixel channel values."""
    return np.array([values], dtype=np.float32)


def test_channel_default() -> None:
    channel = BlendRangeChannel.default()
    assert channel.this_layer_black == (0, 0)
    assert channel.this_layer_white == (255, 255)
    assert channel.underlying_black == (0, 0)
    assert channel.underlying_white == (255, 255)
    assert channel.is_default
    assert channel.to_raw() == FULL_RANGE
    assert channel == BlendRangeChannel()


def test_channel_from_values() -> None:
    channel = BlendRangeChannel.from_values(10, 200, 30, 240)
    assert channel.this_layer_black == (10, 10)
    assert channel.this_layer_white == (200, 200)
    assert channel.underlying_black == (30, 30)
    assert channel.underlying_white == (240, 240)
    assert not channel.is_default
    assert not any(
        (
            channel.this_layer_black_split,
            channel.this_layer_white_split,
            channel.underlying_black_split,
            channel.underlying_white_split,
        )
    )

    assert BlendRangeChannel.from_values().is_default
    partial = BlendRangeChannel.from_values(underlying_white=128)
    assert partial.this_layer_black == (0, 0)
    assert partial.this_layer_white == (255, 255)
    assert partial.underlying_black == (0, 0)
    assert partial.underlying_white == (128, 128)


def test_channel_from_raw_split() -> None:
    # Low byte is the left handle, high byte is the right handle.
    raw = [(50 << 8 | 25, 255 << 8 | 200), (0, 240 << 8 | 240)]
    channel = BlendRangeChannel.from_raw(raw)
    assert channel.this_layer_black == (25, 50)
    assert channel.this_layer_white == (200, 255)
    assert channel.underlying_black == (0, 0)
    assert channel.underlying_white == (240, 240)
    assert channel.this_layer_black_split
    assert channel.this_layer_white_split
    assert not channel.underlying_black_split
    assert not channel.underlying_white_split
    assert channel.to_raw() == raw


def test_channel_from_raw_default() -> None:
    assert BlendRangeChannel.from_raw(FULL_RANGE).is_default


@pytest.mark.parametrize(
    "raw",
    [
        [(0, 65535)],
        [(0, 65535), (0, 65535), (0, 65535)],
        [(0, 65535, 0), (0, 65535)],
    ],
)
def test_channel_from_raw_invalid(raw: Any) -> None:
    with pytest.raises(ValueError):
        BlendRangeChannel.from_raw(raw)


def test_channel_mutable() -> None:
    channel = BlendRangeChannel.default()
    channel.this_layer_black = (10, 20)
    channel.underlying_white = [128, 255]  # type: ignore[assignment]
    assert channel.this_layer_black == (10, 20)
    assert channel.underlying_white == (128, 255)
    assert channel.this_layer_black_split
    assert channel.underlying_white_split
    assert not channel.is_default
    assert channel.to_raw() == [(20 << 8 | 10, 65535), (0, 255 << 8 | 128)]


@pytest.mark.parametrize("value", [(0, 256), (-1, 0), (1,), (1, 2, 3), 5])
def test_channel_invalid_handles(value: Any) -> None:
    with pytest.raises(ValueError):
        BlendRangeChannel(this_layer_black=value)
    channel = BlendRangeChannel.default()
    with pytest.raises(ValueError):
        channel.underlying_white = value


def test_channel_describe() -> None:
    assert BlendRangeChannel.default().describe()
    description = BlendRangeChannel((25, 50), (255, 255), (0, 0), (200, 200)).describe()
    assert "25/50" in description
    assert "200" in description


def test_blend_ranges_from_raw_default() -> None:
    raw = LayerBlendingRanges()
    blend_ranges = BlendRanges.from_raw(raw)
    assert blend_ranges.composite.is_default
    assert blend_ranges.channel_count == 4
    assert len(blend_ranges) == 4
    assert blend_ranges.is_default
    assert all(channel.is_default for channel in blend_ranges)


@pytest.mark.parametrize(
    "raw",
    [LayerBlendingRanges(None, None), None],  # type: ignore[arg-type]
)
def test_blend_ranges_from_raw_null(raw: Any) -> None:
    blend_ranges = BlendRanges.from_raw(raw)
    assert blend_ranges.channels == []
    assert blend_ranges.channel_count == 0
    assert len(blend_ranges) == 0
    assert list(blend_ranges) == []
    assert blend_ranges.composite == BlendRangeChannel.default()
    assert blend_ranges.is_default


def test_blend_ranges_indexing() -> None:
    channels = [BlendRangeChannel.from_values(this_layer_black=i) for i in (1, 2, 3)]
    composite = BlendRangeChannel.from_values(this_layer_white=100)
    blend_ranges = BlendRanges.from_channels(composite, channels)
    assert blend_ranges.composite is composite
    assert len(blend_ranges) == 3
    assert blend_ranges.channel_count == 3
    assert blend_ranges[0] is channels[0]
    assert blend_ranges[-1] is channels[2]
    assert blend_ranges[-3] is channels[0]
    with pytest.raises(IndexError):
        blend_ranges[3]
    assert list(blend_ranges) == channels
    assert composite not in list(blend_ranges)
    assert BlendRanges(composite, channels) == blend_ranges


def test_blend_ranges_apply_to_raw() -> None:
    raw = LayerBlendingRanges()
    blend_ranges = BlendRanges.from_raw(raw)
    blend_ranges.composite.this_layer_black = (10, 40)
    blend_ranges[1].underlying_white = (180, 220)
    blend_ranges.channels.pop()
    assert not blend_ranges.is_default

    blend_ranges.apply_to_raw(raw)
    assert raw.composite_ranges == [(40 << 8 | 10, 65535), (0, 65535)]
    assert len(raw.channel_ranges) == 3
    assert raw.channel_ranges[0] == FULL_RANGE
    assert raw.channel_ranges[1] == [(0, 65535), (0, 220 << 8 | 180)]
    assert BlendRanges.from_raw(raw) == blend_ranges


def test_blend_ranges_apply_to_null_raw() -> None:
    raw = LayerBlendingRanges(None, None)  # type: ignore[arg-type]
    BlendRanges.from_channels(
        BlendRangeChannel.from_values(this_layer_black=64), []
    ).apply_to_raw(raw)
    assert raw.composite_ranges == [(64 << 8 | 64, 65535), (0, 65535)]
    assert raw.channel_ranges == []


def test_blend_ranges_is_default() -> None:
    blend_ranges = BlendRanges.from_raw(LayerBlendingRanges())
    assert blend_ranges.is_default
    blend_ranges[-1].underlying_black = (1, 1)
    assert not blend_ranges.is_default
    blend_ranges[-1].underlying_black = (0, 0)
    blend_ranges.composite.this_layer_white = (254, 255)
    assert not blend_ranges.is_default


def test_blend_ranges_invalid_members() -> None:
    with pytest.raises(TypeError):
        BlendRanges(composite=None)  # type: ignore[arg-type]
    with pytest.raises(TypeError):
        BlendRanges(channels=[None])  # type: ignore[list-item]


def test_blend_ranges_describe() -> None:
    assert BlendRanges().describe()
    blend_ranges = BlendRanges.from_raw(LayerBlendingRanges())
    description = blend_ranges.describe()
    assert description
    assert len(description.splitlines()) == 1 + blend_ranges.channel_count


def test_compute_visibility_shape_and_default() -> None:
    blend_ranges = BlendRanges.from_raw(LayerBlendingRanges())
    source = np.random.default_rng(0).random((4, 5, 3), dtype=np.float32)
    backdrop = np.random.default_rng(1).random((4, 5, 3), dtype=np.float32)
    visibility = blend_ranges.compute_visibility(source, backdrop)
    assert visibility.shape == (4, 5, 1)
    assert np.all(visibility == 1.0)


def test_compute_visibility_composite_luminosity() -> None:
    # Luminosities: black 0, red 76.2, green 149.7, blue 29.1, white 255.
    source = _color((0, 0, 0), (1, 0, 0), (0, 1, 0), (0, 0, 1), (1, 1, 1))
    backdrop = np.zeros_like(source)
    blend_ranges = BlendRanges(BlendRangeChannel.from_values(this_layer_black=50))
    visibility = blend_ranges.compute_visibility(source, backdrop)
    assert visibility[0, :, 0].tolist() == [0.0, 1.0, 1.0, 0.0, 1.0]

    blend_ranges.composite = BlendRangeChannel.from_values(this_layer_white=100)
    visibility = blend_ranges.compute_visibility(source, backdrop)
    assert visibility[0, :, 0].tolist() == [1.0, 1.0, 0.0, 1.0, 0.0]


def test_compute_visibility_underlying_uses_backdrop() -> None:
    source = _color((1, 1, 1), (1, 1, 1), (0, 0, 0))
    backdrop = _color((0.1, 0.1, 0.1), (0.9, 0.9, 0.9), (0.9, 0.9, 0.9))
    blend_ranges = BlendRanges(BlendRangeChannel.from_values(underlying_white=128))
    visibility = blend_ranges.compute_visibility(source, backdrop)
    assert visibility[0, :, 0].tolist() == [1.0, 0.0, 0.0]

    blend_ranges.composite = BlendRangeChannel.from_values(underlying_black=128)
    visibility = blend_ranges.compute_visibility(source, backdrop)
    assert visibility[0, :, 0].tolist() == [0.0, 1.0, 1.0]


def test_compute_visibility_per_channel() -> None:
    source = _color((1, 0, 0), (0, 1, 0), (0, 0, 1))
    backdrop = _color((0, 0, 0), (0, 0, 0), (1, 1, 1))
    default = BlendRangeChannel.default
    blend_ranges = BlendRanges(
        default(),
        [
            BlendRangeChannel.from_values(this_layer_white=128),
            default(),
            BlendRangeChannel.from_values(underlying_white=128),
        ],
    )
    visibility = blend_ranges.compute_visibility(source, backdrop)
    # Red source is too bright; the last backdrop is too bright in blue.
    assert visibility[0, :, 0].tolist() == [0.0, 1.0, 0.0]


def test_compute_visibility_ignores_extra_channels() -> None:
    hidden = BlendRangeChannel.from_values(this_layer_black=255, underlying_black=255)
    default = BlendRangeChannel.default
    blend_ranges = BlendRanges(default(), [default(), default(), default(), hidden])
    source = _color((0.5, 0.5, 0.5))
    visibility = blend_ranges.compute_visibility(source, source)
    assert visibility[0, :, 0].tolist() == [1.0]


def test_compute_visibility_split_fades_linearly() -> None:
    values = np.linspace(0.0, 1.0, 11, dtype=np.float32)
    source = np.repeat(values.reshape(1, -1, 1), 3, axis=2)
    backdrop = np.zeros_like(source)

    fade_in = BlendRanges(BlendRangeChannel(this_layer_black=(0, 255)))
    np.testing.assert_allclose(
        fade_in.compute_visibility(source, backdrop)[0, :, 0], values, atol=1e-5
    )

    fade_out = BlendRanges(BlendRangeChannel(this_layer_white=(0, 255)))
    np.testing.assert_allclose(
        fade_out.compute_visibility(source, backdrop)[0, :, 0],
        1.0 - values,
        atol=1e-5,
    )

    window = BlendRanges(BlendRangeChannel(underlying_black=(51, 102)))
    visibility = window.compute_visibility(backdrop, source)[0, :, 0]
    np.testing.assert_allclose(
        visibility, [0.0, 0.0, 0.0, 0.5, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0], atol=1e-5
    )


def test_compute_visibility_combines_sliders() -> None:
    source = _color((0.5, 0.5, 0.5))
    backdrop = _color((0.5, 0.5, 0.5))
    blend_ranges = BlendRanges(
        BlendRangeChannel(this_layer_black=(0, 255), underlying_white=(0, 255))
    )
    visibility = blend_ranges.compute_visibility(source, backdrop)
    np.testing.assert_allclose(visibility[0, 0, 0], 0.25, atol=1e-5)


def test_compute_visibility_exact_8bit_thresholds() -> None:
    source = (np.arange(256, dtype=np.float32) / 255.0).reshape(1, -1, 1)
    blend_ranges = BlendRanges(
        BlendRangeChannel.from_values(this_layer_black=50, this_layer_white=200)
    )
    visibility = blend_ranges.compute_visibility(source, source)[0, :, 0]
    expected = [1.0 if 50 <= value <= 200 else 0.0 for value in range(256)]
    assert visibility.tolist() == expected


def test_compute_visibility_broadcasts_single_channel() -> None:
    source = _color((1, 1, 1), (0, 0, 0))
    backdrop = _color((1,), (0,))
    blend_ranges = BlendRanges(BlendRangeChannel.from_values(underlying_black=128))
    visibility = blend_ranges.compute_visibility(source, backdrop)
    assert visibility.shape == (1, 2, 1)
    assert visibility[0, :, 0].tolist() == [1.0, 0.0]

    grayscale = blend_ranges.compute_visibility(source[:, :, 0], backdrop[:, :, 0])
    assert grayscale.shape == (1, 2, 1)
    assert grayscale[0, :, 0].tolist() == [1.0, 0.0]


def test_compute_visibility_invalid_shape() -> None:
    color = np.zeros(3, dtype=np.float32)
    with pytest.raises(ValueError):
        BlendRanges().compute_visibility(color, color)


def test_to_pil_mask() -> None:
    values = np.linspace(0.0, 1.0, 6, dtype=np.float32)
    source = np.repeat(values.reshape(1, -1, 1), 3, axis=2)
    blend_ranges = BlendRanges(BlendRangeChannel(this_layer_black=(0, 255)))
    mask = blend_ranges.to_pil_mask(source, np.zeros_like(source))
    assert mask.mode == "L"
    assert mask.size == (6, 1)
    assert np.asarray(mask)[0].tolist() == [0, 51, 102, 153, 204, 255]
