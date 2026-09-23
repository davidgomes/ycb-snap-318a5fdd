import io

import numpy as np
import pytest
from PIL import Image

from psd_tools import PSDImage
from psd_tools.api.blend_range import BlendRangeChannel, BlendRanges
from psd_tools.psd.layer_and_mask import LayerBlendingRanges


def test_channel_roundtrip_and_splits() -> None:
    raw = [(10 | (40 << 8), 200 | (255 << 8)), (0, 65535)]
    channel = BlendRangeChannel.from_raw(raw)
    assert channel.this_layer_black == (10, 40)
    assert channel.this_layer_white == (200, 255)
    assert channel.underlying_black == (0, 0)
    assert channel.underlying_white == (255, 255)
    assert channel.this_layer_black_split
    assert channel.this_layer_white_split
    assert not channel.underlying_black_split
    assert not channel.underlying_white_split
    assert channel.to_raw() == [raw[0], raw[1]]
    assert not channel.is_default
    assert channel.describe()


def test_default_and_from_values() -> None:
    channel = BlendRangeChannel.default()
    assert channel.is_default
    assert channel.to_raw() == [(0, 65535), (0, 65535)]
    built = BlendRangeChannel.from_values(32, 200)
    assert built.this_layer_black == (32, 32)
    assert built.this_layer_white == (200, 200)
    assert built.underlying_black == (0, 0)
    assert not built.this_layer_black_split


def test_ranges_null_and_sequence() -> None:
    empty = BlendRanges.from_raw(LayerBlendingRanges(None, None))  # type: ignore[arg-type]
    assert empty.channel_count == 0
    assert len(empty) == 0
    assert list(empty) == []
    assert empty.composite.is_default
    assert empty.is_default
    assert empty.describe()

    composite = BlendRangeChannel.from_values(this_layer_black=(0, 10))
    channels = [
        BlendRangeChannel.default(),
        BlendRangeChannel.from_values(underlying_white=128),
    ]
    ranges = BlendRanges.from_channels(composite, channels)
    assert ranges.channel_count == 2
    assert ranges[1] is channels[1]
    assert ranges[-1] is channels[1]
    assert list(ranges) == channels
    assert not ranges.is_default

    raw = LayerBlendingRanges()
    ranges.apply_to_raw(raw)
    again = BlendRanges.from_raw(raw)
    assert again.composite.this_layer_black == (0, 10)
    assert again[1].underlying_white == (128, 128)


def test_compute_visibility_and_mask() -> None:
    # This-layer gray: hide below 128 with a split from 64 to 128.
    composite = BlendRangeChannel.from_values(this_layer_black=(64, 128))
    red = BlendRangeChannel.from_values(underlying_white=(128, 255))
    ranges = BlendRanges.from_channels(composite, [red, BlendRangeChannel.default()])

    source = np.zeros((1, 3, 3), dtype=np.float32)
    source[0, 0] = (0.0, 0.0, 0.0)  # luma 0
    source[0, 1] = (96 / 255.0, 96 / 255.0, 96 / 255.0)  # midpoint of split
    source[0, 2] = (1.0, 1.0, 1.0)
    backdrop = np.full((1, 3, 3), 0.5, dtype=np.float32)
    backdrop[0, 2, 0] = 1.0  # red at the white handle, fully hidden

    weight = ranges.compute_visibility(source, backdrop)
    assert weight.shape == (1, 3, 1)
    assert weight[0, 0, 0] == pytest.approx(0.0)
    assert weight[0, 1, 0] == pytest.approx((96 - 64) / (128 - 64))
    assert weight[0, 2, 0] == pytest.approx(0.0)

    mask = ranges.to_pil_mask(source, backdrop)
    assert mask.mode == "L"
    assert mask.size == (3, 1)


def test_layer_blend_ranges_roundtrip(tmp_path) -> None:
    psd = PSDImage.new(mode="RGB", size=(4, 4))
    layer = psd.create_pixel_layer(Image.new("RGB", (4, 4), (255, 0, 0)), name="A")
    ranges = BlendRanges.from_channels(
        BlendRangeChannel.from_values(this_layer_white=(200, 240)),
        [
            BlendRangeChannel.from_values(10, 250),
            BlendRangeChannel.default(),
            BlendRangeChannel.default(),
        ],
    )
    layer.blend_ranges = ranges
    path = tmp_path / "blend.psd"
    psd.save(path)
    opened = PSDImage.open(path)
    restored = opened[0].blend_ranges
    assert restored.composite.this_layer_white == (200, 240)
    assert restored[0].this_layer_black == (10, 10)
    assert restored.composite.this_layer_white_split

    buf = io.BytesIO()
    opened.save(buf)


def test_composite_applies_blend_if() -> None:
    psd = PSDImage.new(mode="RGB", size=(2, 1))
    pixels = Image.new("RGB", (2, 1))
    pixels.putpixel((0, 0), (0, 0, 0))
    pixels.putpixel((1, 0), (255, 255, 255))
    layer = psd.create_pixel_layer(pixels, name="pixels")
    layer.blend_ranges = BlendRanges.from_channels(
        BlendRangeChannel.from_values(this_layer_black=128),
        [BlendRangeChannel.default() for _ in range(3)],
    )
    image = psd.composite(color=(0.0, 0.0, 1.0), alpha=1.0)
    assert image is not None
    # Dark source pixel is hidden, so the blue backdrop remains.
    assert image.getpixel((0, 0))[:3] == (0, 0, 255)
    # Bright source pixel replaces the backdrop.
    assert image.getpixel((1, 0))[:3] == (255, 255, 255)
