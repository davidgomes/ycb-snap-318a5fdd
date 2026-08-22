import io
from pathlib import Path

import numpy as np
import pytest

from psd_tools.api.blend_range import BlendRangeChannel, BlendRanges
from psd_tools.api.psd_image import PSDImage
from psd_tools.psd.layer_and_mask import LayerBlendingRanges

from ..utils import check_write_read, full_name


def test_blend_range_channel_from_raw_round_trip() -> None:
    raw = [(0x0028, 0xFFFF), (0x0064, 0xC8C8)]
    channel = BlendRangeChannel.from_raw(raw)
    assert channel.this_layer_black == (40, 0)
    assert channel.this_layer_white == (255, 255)
    assert channel.underlying_black == (100, 0)
    assert channel.underlying_white == (200, 200)
    assert channel.to_raw() == raw


def test_blend_range_channel_default() -> None:
    channel = BlendRangeChannel.default()
    assert channel.is_default
    assert not channel.this_layer_black_split
    assert channel.describe() == "BlendRangeChannel(default)"


def test_blend_range_channel_from_values() -> None:
    channel = BlendRangeChannel.from_values(
        this_layer_black=10,
        this_layer_white=240,
        underlying_black=20,
        underlying_white=230,
    )
    assert channel.this_layer_black == (10, 10)
    assert channel.this_layer_white == (240, 240)
    assert not channel.is_default
    assert "This Layer:" in channel.describe()


def test_blend_range_channel_split_properties() -> None:
    channel = BlendRangeChannel(
        this_layer_black=(10, 20),
        this_layer_white=(240, 250),
        underlying_black=(0, 0),
        underlying_white=(255, 255),
    )
    assert channel.this_layer_black_split
    assert channel.this_layer_white_split
    assert not channel.underlying_black_split
    assert not channel.underlying_white_split


def test_blend_ranges_from_raw_null() -> None:
    raw = LayerBlendingRanges(None, None)  # type: ignore[arg-type]
    ranges = BlendRanges.from_raw(raw)
    assert ranges.is_default
    assert ranges.channel_count == 0
    assert len(ranges) == 0
    assert ranges.describe() == "BlendRanges(default)"


def test_blend_ranges_indexing_and_iteration() -> None:
    channels = [
        BlendRangeChannel.from_values(this_layer_black=10),
        BlendRangeChannel.from_values(this_layer_black=20),
    ]
    ranges = BlendRanges.from_channels(BlendRangeChannel.default(), channels)
    assert ranges.channel_count == 2
    assert len(ranges) == 2
    assert ranges[-1].this_layer_black == (20, 20)
    assert [ch.this_layer_black[0] for ch in ranges] == [10, 20]


def test_blend_ranges_apply_to_raw() -> None:
    raw = LayerBlendingRanges(None, None)  # type: ignore[arg-type]
    ranges = BlendRanges.from_channels(
        BlendRangeChannel.from_values(this_layer_black=32),
        [BlendRangeChannel.from_values(this_layer_white=200)],
    )
    ranges.apply_to_raw(raw)
    assert (
        raw.composite_ranges
        == BlendRangeChannel.from_values(this_layer_black=32).to_raw()
    )
    assert raw.channel_ranges == [
        BlendRangeChannel.from_values(this_layer_white=200).to_raw()
    ]


def test_compute_visibility_default() -> None:
    ranges = BlendRanges.from_channels(BlendRangeChannel.default(), [])
    source = np.ones((2, 2, 3), dtype=np.float32)
    backdrop = np.zeros((2, 2, 3), dtype=np.float32)
    weight = ranges.compute_visibility(source, backdrop)
    assert weight.shape == (2, 2, 1)
    np.testing.assert_allclose(weight, 1.0)


def test_compute_visibility_this_layer_black() -> None:
    ranges = BlendRanges.from_channels(
        BlendRangeChannel.from_values(this_layer_black=128),
        [],
    )
    source = np.array([[[0.0, 0.0, 0.0]], [[1.0, 1.0, 1.0]]], dtype=np.float32)
    backdrop = np.zeros_like(source)
    weight = ranges.compute_visibility(source, backdrop)
    assert weight[0, 0, 0] == 0.0
    assert weight[1, 0, 0] == 1.0


def test_compute_visibility_split_slider() -> None:
    channel = BlendRangeChannel(
        this_layer_black=(100, 200),
        this_layer_white=(255, 255),
        underlying_black=(0, 0),
        underlying_white=(255, 255),
    )
    ranges = BlendRanges.from_channels(BlendRangeChannel.default(), [channel])
    source = np.array([[[150 / 255.0, 0.0, 0.0]]], dtype=np.float32)
    backdrop = np.zeros_like(source)
    weight = ranges.compute_visibility(source, backdrop)
    assert weight[0, 0, 0] == pytest.approx(0.5)


def test_to_pil_mask() -> None:
    ranges = BlendRanges.from_channels(
        BlendRangeChannel.from_values(this_layer_black=128),
        [],
    )
    source = np.array([[[0.0, 0.0, 0.0]], [[1.0, 1.0, 1.0]]], dtype=np.float32)
    backdrop = np.zeros_like(source)
    mask = ranges.to_pil_mask(source, backdrop)
    assert mask.mode == "L"
    assert mask.size == (1, 2)
    assert mask.getpixel((0, 0)) == 0
    assert mask.getpixel((0, 1)) == 255


def test_layer_blending_ranges_validation() -> None:
    with pytest.raises(ValueError, match="composite_ranges"):
        LayerBlendingRanges([(0, 1)], []).write(io.BytesIO())  # type: ignore[arg-type]
    with pytest.raises(ValueError, match="channel range"):
        LayerBlendingRanges(
            [(0, 65535), (0, 65535)],
            [[(0, 1)]],
        ).write(io.BytesIO())


def test_layer_blend_ranges_round_trip(tmp_path: Path) -> None:
    psd = PSDImage.open(full_name("layers/pixel-layer.psd"))
    layer = psd[0]
    ranges = BlendRanges.from_channels(
        BlendRangeChannel.from_values(this_layer_black=64, this_layer_white=200),
        [
            BlendRangeChannel.from_values(underlying_black=32),
            BlendRangeChannel.default(),
            BlendRangeChannel.default(),
        ],
    )
    layer.blend_ranges = ranges
    out = tmp_path / "blend-ranges.psd"
    psd.save(str(out))

    psd2 = PSDImage.open(out)
    loaded = psd2[0].blend_ranges
    assert loaded.composite.this_layer_black == (64, 64)
    assert loaded.composite.this_layer_white == (200, 200)
    assert loaded.channels[0].underlying_black == (32, 32)
    assert loaded.channel_count == 3


def test_layer_blending_ranges_write_read() -> None:
    check_write_read(
        LayerBlendingRanges(
            [(0, 1), (0, 1)],
            [
                [(0, 1), (0, 1)],
                [(0, 1), (0, 1)],
                [(0, 1), (0, 1)],
            ],
        )
    )
