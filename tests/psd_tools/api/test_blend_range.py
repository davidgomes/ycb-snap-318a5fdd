import io

import numpy as np
import pytest

from psd_tools.api.blend_range import BlendRangeChannel, BlendRanges
from psd_tools.api.psd_image import PSDImage
from psd_tools.psd.layer_and_mask import LayerBlendingRanges

from ..utils import full_name


def test_channel_default() -> None:
    channel = BlendRangeChannel.default()
    assert channel.this_layer_black == (0, 0)
    assert channel.this_layer_white == (255, 255)
    assert channel.underlying_black == (0, 0)
    assert channel.underlying_white == (255, 255)
    assert channel.is_default
    assert channel.to_raw() == [(0, 65535), (0, 65535)]
    assert channel.describe()


def test_channel_from_raw_split() -> None:
    # low byte = left handle, high byte = right handle.
    channel = BlendRangeChannel.from_raw([(0x4020, 0xE0C0), (0x0000, 0xFFFF)])
    assert channel.this_layer_black == (0x20, 0x40)
    assert channel.this_layer_white == (0xC0, 0xE0)
    assert channel.this_layer_black_split
    assert channel.this_layer_white_split
    assert not channel.underlying_black_split
    assert not channel.underlying_white_split
    assert not channel.is_default
    assert channel.to_raw() == [(0x4020, 0xE0C0), (0x0000, 0xFFFF)]


def test_channel_from_values() -> None:
    channel = BlendRangeChannel.from_values(this_layer_black=10, underlying_white=200)
    assert channel.this_layer_black == (10, 10)
    assert channel.this_layer_white == (255, 255)
    assert channel.underlying_black == (0, 0)
    assert channel.underlying_white == (200, 200)
    assert BlendRangeChannel.from_values().is_default


def test_channel_mutable() -> None:
    channel = BlendRangeChannel.default()
    channel.underlying_black = (5, 50)
    assert channel.underlying_black_split
    assert channel.to_raw()[1] == ((50 << 8) | 5, 0xFFFF)
    with pytest.raises(ValueError):
        channel.this_layer_black = (0, 256)


def test_blend_ranges_from_raw() -> None:
    raw = LayerBlendingRanges()
    ranges = BlendRanges.from_raw(raw)
    assert ranges.is_default
    assert ranges.channel_count == 4
    assert len(ranges) == 4
    assert ranges[-1] is ranges.channels[3]
    assert list(ranges) == ranges.channels
    assert ranges.describe()


def test_blend_ranges_null() -> None:
    ranges = BlendRanges.from_raw(LayerBlendingRanges(None, None))  # type: ignore[arg-type]
    assert ranges.channels == []
    assert ranges.composite.is_default
    assert ranges.is_default
    assert ranges.describe()


def test_blend_ranges_apply_to_raw() -> None:
    ranges = BlendRanges.from_channels(
        BlendRangeChannel.from_values(this_layer_black=100),
        [BlendRangeChannel.default(), BlendRangeChannel.from_values(10, 20, 30, 40)],
    )
    assert not ranges.is_default
    raw = LayerBlendingRanges()
    ranges.apply_to_raw(raw)
    assert raw.composite_ranges == [((100 << 8) | 100, 0xFFFF), (0, 0xFFFF)]
    assert len(raw.channel_ranges) == 2
    assert BlendRanges.from_raw(raw) == ranges


@pytest.mark.parametrize(
    "composite_ranges, channel_ranges",
    [
        ([(0, 65535)], [[(0, 65535), (0, 65535)]]),
        ([(0, 65535), (0, 65535)], [[(0, 65535)]]),
        ([(0, 65535), (0, 65535)], [[(0, 65535), (0, 65535), (0, 65535)]]),
    ],
)
def test_raw_write_validation(composite_ranges, channel_ranges) -> None:  # type: ignore[no-untyped-def]
    raw = LayerBlendingRanges(composite_ranges, channel_ranges)
    with pytest.raises(ValueError):
        raw.write(io.BytesIO())


def test_compute_visibility_composite() -> None:
    ranges = BlendRanges.from_channels(BlendRangeChannel.from_values(128))
    source = np.array([[[0.0, 0.0, 0.0], [1.0, 1.0, 1.0]]], dtype=np.float32)
    backdrop = np.zeros_like(source)
    weight = ranges.compute_visibility(source, backdrop)
    assert weight.shape == (1, 2, 1)
    np.testing.assert_allclose(weight[0, :, 0], [0.0, 1.0])


def test_compute_visibility_luminosity() -> None:
    # Pure green has luminosity 0.587 (~150), pure blue 0.114 (~29).
    ranges = BlendRanges.from_channels(BlendRangeChannel.from_values(100))
    source = np.array([[[0.0, 1.0, 0.0], [0.0, 0.0, 1.0]]], dtype=np.float32)
    weight = ranges.compute_visibility(source, np.zeros_like(source))
    np.testing.assert_allclose(weight[0, :, 0], [1.0, 0.0])


def test_compute_visibility_split_fade() -> None:
    composite = BlendRangeChannel(this_layer_black=(0, 200))
    ranges = BlendRanges.from_channels(composite)
    source = np.full((1, 1, 3), 100 / 255.0, dtype=np.float32)
    weight = ranges.compute_visibility(source, np.zeros_like(source))
    np.testing.assert_allclose(weight[0, 0, 0], 0.5, atol=1e-5)


def test_compute_visibility_underlying() -> None:
    composite = BlendRangeChannel(underlying_white=(100, 100))
    ranges = BlendRanges.from_channels(composite)
    source = np.zeros((1, 2, 3), dtype=np.float32)
    backdrop = np.array([[[0.0, 0.0, 0.0], [1.0, 1.0, 1.0]]], dtype=np.float32)
    weight = ranges.compute_visibility(source, backdrop)
    np.testing.assert_allclose(weight[0, :, 0], [1.0, 0.0])


def test_compute_visibility_channel() -> None:
    ranges = BlendRanges.from_channels(
        None,
        [
            BlendRangeChannel.from_values(this_layer_black=128),
            BlendRangeChannel.default(),
            BlendRangeChannel.default(),
        ],
    )
    source = np.array([[[1.0, 0.0, 0.0], [0.0, 1.0, 1.0]]], dtype=np.float32)
    weight = ranges.compute_visibility(source, np.zeros_like(source))
    np.testing.assert_allclose(weight[0, :, 0], [1.0, 0.0])


def test_to_pil_mask() -> None:
    ranges = BlendRanges.from_channels(BlendRangeChannel.from_values(128))
    source = np.array([[[0.0, 0.0, 0.0], [1.0, 1.0, 1.0]]], dtype=np.float32)
    mask = ranges.to_pil_mask(source, np.zeros_like(source))
    assert mask.mode == "L"
    assert mask.size == (2, 1)
    assert np.array(mask).tolist() == [[0, 255]]


def test_layer_blend_ranges_roundtrip() -> None:
    psd = PSDImage.open(full_name("layers/pixel-layer.psd"))
    layer = psd[0]
    assert layer.blend_ranges.is_default

    ranges = layer.blend_ranges
    ranges.composite.this_layer_black = (32, 64)
    ranges[0].underlying_white = (200, 220)
    layer.blend_ranges = ranges
    assert psd.is_updated()

    with io.BytesIO() as f:
        psd.save(f)
        f.seek(0)
        reloaded = PSDImage.open(f)
    result = reloaded[0].blend_ranges
    assert result.composite.this_layer_black == (32, 64)
    assert result[0].underlying_white == (200, 220)
    assert result == ranges


def test_layer_blend_ranges_type_error() -> None:
    layer = PSDImage.open(full_name("layers/pixel-layer.psd"))[0]
    with pytest.raises(TypeError):
        layer.blend_ranges = None  # type: ignore[assignment]
