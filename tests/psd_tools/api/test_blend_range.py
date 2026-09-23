import io

import numpy as np
import pytest

from psd_tools.api.blend_range import BlendRangeChannel, BlendRanges
from psd_tools.api.psd_image import PSDImage
from psd_tools.psd.layer_and_mask import LayerBlendingRanges

from ..utils import full_name


def test_channel_default_and_from_values() -> None:
    channel = BlendRangeChannel.default()
    assert channel.is_default
    assert channel.this_layer_black == (0, 0)
    assert channel.this_layer_white == (255, 255)
    assert channel.underlying_black == (0, 0)
    assert channel.underlying_white == (255, 255)
    assert channel.this_layer_black_split is False
    assert channel.this_layer_white_split is False
    assert channel.underlying_black_split is False
    assert channel.underlying_white_split is False
    assert channel.to_raw() == [(0, 65535), (0, 65535)]
    assert channel.describe()

    partial = BlendRangeChannel.from_values(this_layer_black=64)
    assert partial.this_layer_black == (64, 64)
    assert partial.this_layer_white == (255, 255)
    assert partial.underlying_black == (0, 0)
    assert partial.underlying_white == (255, 255)
    assert partial.this_layer_black_split is False
    assert partial.is_default is False

    explicit = BlendRangeChannel.from_values(10, 200, 20, 230)
    assert explicit.this_layer_black == (10, 10)
    assert explicit.this_layer_white == (200, 200)
    assert explicit.underlying_black == (20, 20)
    assert explicit.underlying_white == (230, 230)


def test_channel_split_roundtrip() -> None:
    # low byte = left handle, high byte = right handle.
    raw = [(0x1E0A, 0xF080), (0x0000, 0xFFC0)]
    channel = BlendRangeChannel.from_raw(raw)
    assert channel.this_layer_black == (0x0A, 0x1E)
    assert channel.this_layer_white == (0x80, 0xF0)
    assert channel.underlying_black == (0, 0)
    assert channel.underlying_white == (0xC0, 0xFF)
    assert channel.this_layer_black_split is True
    assert channel.this_layer_white_split is True
    assert channel.underlying_black_split is False
    assert channel.underlying_white_split is True
    assert channel.to_raw() == [tuple(pair) for pair in raw]
    assert "This Layer" in channel.describe()

    channel.this_layer_black = (1, 2)
    assert channel.this_layer_black_split is True
    assert channel.to_raw()[0][0] == (2 << 8) | 1


def test_ranges_indexing_ignores_composite() -> None:
    composite = BlendRangeChannel.from_values(this_layer_black=3)
    channels = [
        BlendRangeChannel.from_values(this_layer_white=10),
        BlendRangeChannel.from_values(this_layer_white=20),
        BlendRangeChannel.from_values(this_layer_white=30),
    ]
    ranges = BlendRanges.from_channels(composite, channels)
    assert ranges.channel_count == 3
    assert len(ranges) == 3
    assert ranges[0].this_layer_white == (10, 10)
    assert ranges[-1].this_layer_white == (30, 30)
    assert [channel.this_layer_white[0] for channel in ranges] == [10, 20, 30]
    assert ranges.composite is composite
    assert ranges.is_default is False
    assert ranges.describe()

    assert BlendRanges(BlendRangeChannel.default(), []).is_default
    assert BlendRanges(
        BlendRangeChannel.default(),
        [BlendRangeChannel.default(), BlendRangeChannel.default()],
    ).is_default


def test_ranges_null_raw_and_apply() -> None:
    null = LayerBlendingRanges(None, None)  # type: ignore[arg-type]
    ranges = BlendRanges.from_raw(null)
    assert ranges.channel_count == 0
    assert ranges.composite.is_default
    assert ranges.is_default
    assert "Channels: none" in ranges.describe()

    raw = LayerBlendingRanges()
    edited = BlendRanges.from_channels(
        BlendRangeChannel.from_values(this_layer_black=40, underlying_white=180),
        [
            BlendRangeChannel(this_layer_black=(8, 24), this_layer_white=(200, 240)),
            BlendRangeChannel.default(),
        ],
    )
    edited.apply_to_raw(raw)
    loaded = BlendRanges.from_raw(raw)
    assert loaded.composite.this_layer_black == (40, 40)
    assert loaded.composite.underlying_white == (180, 180)
    assert loaded.channel_count == 2
    assert loaded[0].this_layer_black == (8, 24)
    assert loaded[0].this_layer_black_split is True
    assert loaded[0].this_layer_white == (200, 240)
    assert loaded[-1].is_default


def test_compute_visibility_and_mask() -> None:
    full = BlendRanges.from_channels(BlendRangeChannel.default(), [])
    source = np.zeros((2, 2, 3), dtype=np.float32)
    backdrop = np.ones((2, 2, 3), dtype=np.float32)
    weight = full.compute_visibility(source, backdrop)
    assert weight.shape == (2, 2, 1)
    assert weight.dtype == np.float32
    assert np.all(weight == 1.0)

    # Split This Layer black slider: 0 at 0, 1 at 255, linear between.
    gray = BlendRangeChannel.default()
    gray.this_layer_black = (0, 255)
    ranges = BlendRanges.from_channels(gray, [])
    ramp = np.zeros((1, 3, 3), dtype=np.float32)
    ramp[0, :, :] = np.array([0.0, 0.5, 1.0], dtype=np.float32)[:, np.newaxis]
    faded = ranges.compute_visibility(ramp, np.ones_like(ramp))
    assert faded.shape == (1, 3, 1)
    np.testing.assert_allclose(faded[0, :, 0], [0.0, 0.5, 1.0], atol=1e-5)

    # Hard This Layer threshold uses luminosity, not a single channel.
    # Pure blue luma is 0.114 -> about 29, pure red is 0.299 -> about 76.
    cutoff = BlendRangeChannel.from_values(this_layer_black=50)
    luma = BlendRanges.from_channels(cutoff, [])
    colors = np.zeros((1, 2, 3), dtype=np.float32)
    colors[0, 0] = (0.0, 0.0, 1.0)
    colors[0, 1] = (1.0, 0.0, 0.0)
    hidden = luma.compute_visibility(colors, np.ones_like(colors))
    assert hidden[0, 0, 0] == pytest.approx(0.0)
    assert hidden[0, 1, 0] == pytest.approx(1.0)

    # Underlying Layer reads the backdrop.
    underlying = BlendRangeChannel.from_values(underlying_black=128)
    under = BlendRanges.from_channels(underlying, [])
    src = np.ones((1, 2, 3), dtype=np.float32)
    back = np.zeros((1, 2, 3), dtype=np.float32)
    back[0, 1] = 1.0
    under_weight = under.compute_visibility(src, back)
    assert under_weight[0, 0, 0] == pytest.approx(0.0)
    assert under_weight[0, 1, 0] == pytest.approx(1.0)

    # Per-channel sliders use that channel, even when gray would pass.
    green = BlendRangeChannel.from_values(this_layer_white=200)
    per_channel = BlendRanges.from_channels(
        BlendRangeChannel.default(),
        [BlendRangeChannel.default(), green, BlendRangeChannel.default()],
    )
    green_pixel = np.zeros((1, 1, 3), dtype=np.float32)
    green_pixel[0, 0] = (0.0, 1.0, 0.0)
    assert per_channel.compute_visibility(green_pixel, np.ones_like(green_pixel))[
        0, 0, 0
    ] == pytest.approx(0.0)

    mask = under.to_pil_mask(src, back)
    assert mask.mode == "L"
    assert mask.size == (2, 1)
    assert mask.getpixel((0, 0)) == 0
    assert mask.getpixel((1, 0)) == 255


def test_layer_blend_ranges_persist_through_save() -> None:
    psd = PSDImage.open(full_name("layers/pixel-layer.psd"))
    layer = psd[0]
    assert layer.blend_ranges.describe()
    assert isinstance(layer.blend_ranges, BlendRanges)

    ranges = BlendRanges.from_channels(
        BlendRangeChannel(this_layer_black=(12, 48), underlying_white=(210, 250)),
        [
            BlendRangeChannel.from_values(this_layer_black=30, this_layer_white=220),
            BlendRangeChannel.default(),
            BlendRangeChannel.default(),
        ],
    )
    layer.blend_ranges = ranges

    output = io.BytesIO()
    psd.save(output)
    output.seek(0)
    restored = PSDImage.open(output)[0].blend_ranges
    assert restored.composite.this_layer_black == (12, 48)
    assert restored.composite.this_layer_black_split is True
    assert restored.composite.underlying_white == (210, 250)
    assert restored.composite.underlying_white_split is True
    assert restored.channel_count == 3
    assert restored[0].this_layer_black == (30, 30)
    assert restored[0].this_layer_white == (220, 220)
    assert restored[-1].is_default


def test_null_fixture_ranges_are_empty() -> None:
    layer = PSDImage.open(full_name("1layer.psd"))[0]
    ranges = layer.blend_ranges
    assert ranges.channel_count == 0
    assert list(ranges) == []
    assert ranges.composite.is_default
    assert ranges.is_default
