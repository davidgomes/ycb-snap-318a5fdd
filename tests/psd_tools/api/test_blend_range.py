"""Tests for Blend If ranges and their effect on compositing."""

import io

import numpy as np
import pytest
from PIL import Image

from psd_tools.api.blend_range import BlendRangeChannel, BlendRanges
from psd_tools.api.psd_image import PSDImage
from psd_tools.composite import composite
from psd_tools.psd.layer_and_mask import LayerBlendingRanges


def _pack(left: int, right: int) -> int:
    return ((right & 0xFF) << 8) | (left & 0xFF)


def test_channel_default_and_from_values() -> None:
    default = BlendRangeChannel.default()
    assert default.is_default
    assert default == BlendRangeChannel.from_values()
    assert default.this_layer_black == (0, 0)
    assert default.this_layer_white == (255, 255)
    assert default.underlying_black == (0, 0)
    assert default.underlying_white == (255, 255)
    assert default.this_layer_black_split is False
    assert default.this_layer_white_split is False
    assert default.underlying_black_split is False
    assert default.underlying_white_split is False
    assert default.to_raw() == [(0, 65535), (0, 65535)]
    assert default.describe()


def test_channel_from_values_unsplit() -> None:
    channel = BlendRangeChannel.from_values(
        this_layer_black=32,
        this_layer_white=200,
        underlying_black=10,
        underlying_white=240,
    )
    assert channel.this_layer_black == (32, 32)
    assert channel.this_layer_white == (200, 200)
    assert channel.underlying_black == (10, 10)
    assert channel.underlying_white == (240, 240)
    assert channel.is_default is False
    assert channel.this_layer_black_split is False
    assert channel.to_raw() == [
        (_pack(32, 32), _pack(200, 200)),
        (_pack(10, 10), _pack(240, 240)),
    ]


def test_channel_raw_roundtrip_split_sliders() -> None:
    raw = [(0x280A, 0xFFC8), (0x0000, 0xFF00)]
    channel = BlendRangeChannel.from_raw(raw)
    assert channel.this_layer_black == (0x0A, 0x28)
    assert channel.this_layer_white == (0xC8, 0xFF)
    assert channel.underlying_black == (0, 0)
    assert channel.underlying_white == (0, 255)
    assert channel.this_layer_black_split is True
    assert channel.this_layer_white_split is True
    assert channel.underlying_black_split is False
    assert channel.underlying_white_split is True
    assert channel.to_raw() == [(0x280A, 0xFFC8), (0x0000, 0xFF00)]
    assert channel.describe()


def test_channel_handles_are_mutable() -> None:
    channel = BlendRangeChannel.default()
    channel.this_layer_black = (10, 40)
    channel.this_layer_white = [200, 220]  # type: ignore[assignment]
    channel.underlying_black = (0, 0)
    channel.underlying_white = (255, 255)
    assert channel.this_layer_black == (10, 40)
    assert channel.this_layer_white == (200, 220)
    assert channel.this_layer_black_split is True
    assert channel.this_layer_white_split is True
    assert channel.is_default is False
    assert channel.to_raw()[0] == (_pack(10, 40), _pack(200, 220))


def test_channel_rejects_out_of_range_handles() -> None:
    channel = BlendRangeChannel.default()
    with pytest.raises(ValueError):
        channel.this_layer_black = (0, 256)
    with pytest.raises(ValueError):
        BlendRangeChannel.from_values(this_layer_white=300)


def test_ranges_channels_exclude_composite() -> None:
    composite_channel = BlendRangeChannel.default()
    red = BlendRangeChannel.from_values(this_layer_black=20)
    green = BlendRangeChannel.from_values(this_layer_white=200)
    blue = BlendRangeChannel.default()
    ranges = BlendRanges.from_channels(composite_channel, [red, green, blue])
    assert ranges.channel_count == 3
    assert len(ranges) == 3
    assert ranges[0] is red
    assert ranges[-1] is blue
    assert list(ranges) == [red, green, blue]
    assert ranges.composite is composite_channel
    assert all(channel is not ranges.composite for channel in ranges)
    assert ranges.is_default is False
    assert ranges.describe()

    ranges.channels = [blue]
    assert ranges.channel_count == 1
    assert ranges[0] is blue
    assert ranges[-1] is blue


def test_ranges_from_null_raw() -> None:
    raw = LayerBlendingRanges(None, None)  # type: ignore[arg-type]
    ranges = BlendRanges.from_raw(raw)
    assert ranges.channel_count == 0
    assert len(ranges) == 0
    assert list(ranges) == []
    assert ranges.composite.is_default
    assert ranges.is_default
    assert ranges.describe()


def test_ranges_apply_to_raw_roundtrip() -> None:
    composite_channel = BlendRangeChannel.from_values(this_layer_black=16)
    composite_channel.this_layer_black = (16, 48)
    red = BlendRangeChannel.from_values(underlying_white=128)
    ranges = BlendRanges(composite_channel, [red, BlendRangeChannel.default()])
    raw = LayerBlendingRanges()
    ranges.apply_to_raw(raw)
    assert raw.composite_ranges == composite_channel.to_raw()
    assert raw.channel_ranges == [red.to_raw(), BlendRangeChannel.default().to_raw()]

    restored = BlendRanges.from_raw(LayerBlendingRanges.frombytes(raw.tobytes()))
    assert restored == ranges
    assert restored[0].underlying_white == (128, 128)
    assert restored.composite.this_layer_black == (16, 48)
    assert restored.composite.this_layer_black_split is True


def test_compute_visibility_luminosity_and_split() -> None:
    # Gray ramp: 0, 128, 255. Black slider split from 64 to 192.
    source = np.array(
        [[[0, 0, 0], [128, 128, 128], [255, 255, 255]]], dtype=np.float32
    ) / np.float32(255)
    backdrop = np.ones_like(source)
    composite_channel = BlendRangeChannel.default()
    composite_channel.this_layer_black = (64, 192)
    ranges = BlendRanges.from_channels(
        composite_channel,
        [BlendRangeChannel.default() for _ in range(3)],
    )
    weight = ranges.compute_visibility(source, backdrop)
    assert weight.shape == (1, 3, 1)
    assert weight.dtype == np.float32
    expected = np.array([0.0, (128 - 64) / (192 - 64), 1.0], dtype=np.float32)
    assert np.allclose(weight[0, :, 0], expected)

    # Pure red's luminosity is 0.299, so a full split on the gray slider
    # keeps that fraction rather than the red channel itself (which is 1).
    red = np.array([[[1.0, 0.0, 0.0]]], dtype=np.float32)
    gray = BlendRangeChannel.default()
    gray.this_layer_black = (0, 255)
    luma_ranges = BlendRanges.from_channels(gray, [])
    red_weight = luma_ranges.compute_visibility(red, np.ones_like(red))
    assert red_weight.shape == (1, 1, 1)
    assert float(red_weight[0, 0, 0]) == pytest.approx(0.299, rel=1e-5)


def test_compute_visibility_underlying_and_per_channel() -> None:
    source = np.ones((2, 2, 3), dtype=np.float32)
    backdrop = np.zeros((2, 2, 3), dtype=np.float32)
    backdrop[1, :, :] = 1.0

    # Underlying white slider drops pixels sitting on a bright backdrop.
    composite_channel = BlendRangeChannel.from_values(underlying_white=10)
    ranges = BlendRanges.from_channels(composite_channel, [])
    weight = ranges.compute_visibility(source, backdrop)
    assert np.allclose(weight[0, :, 0], 1.0)
    assert np.allclose(weight[1, :, 0], 0.0)

    # Per-channel "this layer" red slider hides the red plane only as a
    # whole-pixel weight. Green and blue stay in range, red does not.
    red_only = np.zeros((1, 1, 3), dtype=np.float32)
    red_only[0, 0, 0] = 1.0
    red_channel = BlendRangeChannel.from_values(this_layer_white=10)
    channel_ranges = BlendRanges.from_channels(
        BlendRangeChannel.default(),
        [red_channel, BlendRangeChannel.default(), BlendRangeChannel.default()],
    )
    hidden = channel_ranges.compute_visibility(red_only, np.zeros_like(red_only))
    assert float(hidden[0, 0, 0]) == 0.0

    green = np.zeros((1, 1, 3), dtype=np.float32)
    green[0, 0, 1] = 1.0
    visible = channel_ranges.compute_visibility(green, np.zeros_like(green))
    assert float(visible[0, 0, 0]) == 1.0


def test_to_pil_mask_mode() -> None:
    source = np.zeros((4, 5, 3), dtype=np.float32)
    source[:, 2:, :] = 1.0
    channel = BlendRangeChannel.from_values(this_layer_black=128)
    ranges = BlendRanges.from_channels(channel, [])
    mask = ranges.to_pil_mask(source, np.ones_like(source))
    assert mask.mode == "L"
    assert mask.size == (5, 4)
    pixels = np.asarray(mask)
    assert pixels[:, :2].max() == 0
    assert pixels[:, 2:].min() == 255


def test_layer_blend_ranges_persist_through_save() -> None:
    psd = PSDImage.new("RGB", (8, 8))
    layer = psd.create_pixel_layer(Image.new("RGB", (8, 8), (255, 0, 0)), name="Red")
    composite_channel = BlendRangeChannel.default()
    composite_channel.this_layer_black = (12, 36)
    red = BlendRangeChannel.from_values(this_layer_white=180, underlying_black=4)
    ranges = BlendRanges.from_channels(
        composite_channel,
        [red, BlendRangeChannel.default(), BlendRangeChannel.default()],
    )
    layer.blend_ranges = ranges

    # In-place edits on the live object write through to the record.
    live = layer.blend_ranges
    live[0].underlying_white = (90, 140)
    assert layer.blend_ranges[0].underlying_white == (90, 140)
    assert layer.blend_ranges.composite.this_layer_black == (12, 36)

    buffer = io.BytesIO()
    psd.save(buffer)
    buffer.seek(0)
    restored = PSDImage.open(buffer)[0]
    assert restored.blend_ranges.composite.this_layer_black == (12, 36)
    assert restored.blend_ranges.composite.this_layer_black_split is True
    assert restored.blend_ranges.channel_count == 3
    assert restored.blend_ranges[0].this_layer_white == (180, 180)
    assert restored.blend_ranges[0].underlying_black == (4, 4)
    assert restored.blend_ranges[0].underlying_white == (90, 140)
    assert restored.blend_ranges[0].underlying_white_split is True
    assert restored.blend_ranges[-1].is_default


def test_composite_applies_blend_if() -> None:
    psd = PSDImage.new("RGB", (3, 1))
    gradient = Image.new("RGB", (3, 1))
    gradient.putpixel((0, 0), (0, 0, 0))
    gradient.putpixel((1, 0), (128, 128, 128))
    gradient.putpixel((2, 0), (255, 255, 255))
    layer = psd.create_pixel_layer(gradient, name="Ramp")

    color, shape, alpha = composite(psd, force=True)
    assert np.allclose(alpha, 1.0)

    composite_channel = BlendRangeChannel.default()
    composite_channel.this_layer_black = (64, 192)
    layer.blend_ranges = BlendRanges.from_channels(
        composite_channel,
        [BlendRangeChannel.default() for _ in range(3)],
    )
    color, shape, alpha = composite(psd, force=True)
    expected = np.array([0.0, (128 - 64) / (192 - 64), 1.0], dtype=np.float32)
    assert alpha.shape == (1, 3, 1)
    assert np.allclose(alpha[0, :, 0], expected, atol=1e-5)
    # Fully hidden pixel does not keep the layer color.
    assert shape[0, 0, 0] == pytest.approx(0.0)
    # Fully visible white pixel stays white.
    assert np.allclose(color[0, 2], 1.0)
