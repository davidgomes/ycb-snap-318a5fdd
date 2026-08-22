import io

import numpy as np
import pytest
from psd_tools.api.blend_range import BlendRangeChannel, BlendRanges
from psd_tools.api.layers import PixelLayer
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
    assert not channel.this_layer_black_split
    assert not channel.this_layer_white_split
    assert not channel.underlying_black_split
    assert not channel.underlying_white_split
    assert channel.describe()

    custom = BlendRangeChannel.from_values(10, 200, 5, 240)
    assert custom.this_layer_black == (10, 10)
    assert custom.this_layer_white == (200, 200)
    assert custom.underlying_black == (5, 5)
    assert custom.underlying_white == (240, 240)
    assert not custom.is_default


def test_channel_from_raw_split_roundtrip() -> None:
    # low byte = left, high byte = right: 20|80<<8 and 180|220<<8
    this_black = 20 | (80 << 8)
    this_white = 180 | (220 << 8)
    und_black = 0
    und_white = 65535
    channel = BlendRangeChannel.from_raw([(this_black, this_white), (und_black, und_white)])
    assert channel.this_layer_black == (20, 80)
    assert channel.this_layer_white == (180, 220)
    assert channel.this_layer_black_split
    assert channel.this_layer_white_split
    assert not channel.underlying_black_split
    assert not channel.underlying_white_split
    assert channel.to_raw() == [(this_black, this_white), (und_black, und_white)]
    channel.this_layer_black = (15, 40)
    assert channel.this_layer_black == (15, 40)


def test_blend_ranges_sequence_and_null() -> None:
    raw = LayerBlendingRanges(None, None)  # type: ignore[arg-type]
    ranges = BlendRanges.from_raw(raw)
    assert ranges.channel_count == 0
    assert len(ranges) == 0
    assert list(ranges) == []
    assert ranges.composite.is_default
    assert ranges.is_default
    assert ranges.describe()

    composite = BlendRangeChannel.from_values(0, 200)
    channels = [
        BlendRangeChannel.from_values(0, 255),
        BlendRangeChannel.from_values(10, 250),
    ]
    ranges = BlendRanges.from_channels(composite, channels)
    assert len(ranges) == 2
    assert ranges[0] is channels[0]
    assert ranges[-1] is channels[1]
    assert not ranges.is_default


def test_blend_ranges_apply_to_raw() -> None:
    ranges = BlendRanges.from_channels(
        BlendRangeChannel.from_values(8, 240),
        [BlendRangeChannel.from_values(1, 254)],
    )
    raw = LayerBlendingRanges()
    ranges.apply_to_raw(raw)
    restored = BlendRanges.from_raw(raw)
    assert restored.composite.this_layer_black == (8, 8)
    assert restored.composite.this_layer_white == (240, 240)
    assert restored[0].this_layer_black == (1, 1)


def test_compute_visibility_and_pil_mask() -> None:
    source = np.zeros((2, 2, 3), dtype=np.float32)
    source[0, 0] = (1.0, 1.0, 1.0)
    source[1, 1] = (0.0, 0.0, 0.0)
    backdrop = np.full((2, 2, 3), 0.5, dtype=np.float32)

    hidden_highlights = BlendRanges.from_channels(
        BlendRangeChannel.from_values(0, 128),
        [],
    )
    weight = hidden_highlights.compute_visibility(source, backdrop)
    assert weight.shape == (2, 2, 1)
    assert weight[0, 0, 0] == pytest.approx(0.0)
    assert weight[1, 1, 0] == pytest.approx(1.0)

    mask = hidden_highlights.to_pil_mask(source, backdrop)
    assert mask.mode == "L"
    assert mask.size == (2, 2)

    split = BlendRangeChannel(
        this_layer_black=(0, 0),
        this_layer_white=(64, 192),
        underlying_black=(0, 0),
        underlying_white=(255, 255),
    )
    mid = np.full((1, 1, 3), 128 / 255.0, dtype=np.float32)
    weight = BlendRanges.from_channels(split, []).compute_visibility(mid, mid)
    assert 0.0 < float(weight[0, 0, 0]) < 1.0


def test_layer_blend_ranges_persist(tmp_path) -> None:
    psd = PSDImage.open(full_name("layers/pixel-layer.psd"))
    layer = psd[0]
    assert isinstance(layer, PixelLayer)
    layer.blend_ranges = BlendRanges.from_channels(
        BlendRangeChannel.from_values(20, 200),
        [
            BlendRangeChannel.from_values(0, 255),
            BlendRangeChannel.from_values(5, 250),
            BlendRangeChannel.from_values(10, 240),
        ],
    )
    layer.blend_ranges.composite.this_layer_black = (12, 18)

    buf = io.BytesIO()
    psd.save(buf)
    buf.seek(0)
    reloaded = PSDImage.open(buf)[0]
    assert reloaded.blend_ranges.composite.this_layer_black == (12, 18)
    assert reloaded.blend_ranges.composite.this_layer_white == (200, 200)
    assert reloaded.blend_ranges[2].this_layer_white == (240, 240)


def test_layer_blending_ranges_write_validates() -> None:
    with pytest.raises(ValueError):
        LayerBlendingRanges([(0, 1)], [[(0, 1), (0, 1)]]).write(io.BytesIO())
    with pytest.raises(ValueError):
        LayerBlendingRanges(
            [(0, 1), (0, 1)],
            [[(0, 1)]],
        ).write(io.BytesIO())
