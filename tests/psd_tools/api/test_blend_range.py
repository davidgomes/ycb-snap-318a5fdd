import io

import numpy as np
import pytest
from PIL import Image

from psd_tools.api.blend_range import BlendRangeChannel, BlendRanges
from psd_tools.api.psd_image import PSDImage
from psd_tools.psd.layer_and_mask import LayerBlendingRanges


def _gray(values: list[float]) -> np.ndarray:
    return np.array(values, dtype=np.float32).reshape(1, -1, 1)


def test_channel_default() -> None:
    channel = BlendRangeChannel.default()
    assert channel.is_default
    assert channel.this_layer_black == (0, 0)
    assert channel.this_layer_white == (255, 255)
    assert channel.underlying_black == (0, 0)
    assert channel.underlying_white == (255, 255)
    assert channel.to_raw() == [(0, 65535), (0, 65535)]
    assert not channel.this_layer_black_split
    assert channel.describe()


def test_channel_raw_round_trip() -> None:
    raw = [(50 | (150 << 8), 200 | (230 << 8)), (10 | (10 << 8), 240 | (240 << 8))]
    channel = BlendRangeChannel.from_raw(raw)
    assert channel.this_layer_black == (50, 150)
    assert channel.this_layer_white == (200, 230)
    assert channel.underlying_black == (10, 10)
    assert channel.underlying_white == (240, 240)
    assert channel.this_layer_black_split
    assert channel.this_layer_white_split
    assert not channel.underlying_black_split
    assert not channel.underlying_white_split
    assert not channel.is_default
    assert channel.to_raw() == raw


def test_channel_from_values() -> None:
    channel = BlendRangeChannel.from_values(this_layer_black=30, underlying_white=200)
    assert channel.this_layer_black == (30, 30)
    assert channel.this_layer_white == (255, 255)
    assert channel.underlying_black == (0, 0)
    assert channel.underlying_white == (200, 200)
    assert BlendRangeChannel.from_values().is_default


def test_channel_mutable() -> None:
    channel = BlendRangeChannel.default()
    channel.underlying_black = (20, 40)
    assert channel.underlying_black_split
    assert channel.to_raw()[1][0] == 20 | (40 << 8)
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


def test_blend_ranges_from_null() -> None:
    raw = LayerBlendingRanges(None, None)  # type: ignore[arg-type]
    ranges = BlendRanges.from_raw(raw)
    assert ranges.channels == []
    assert ranges.composite.is_default
    assert ranges.is_default
    assert ranges.describe()


def test_blend_ranges_apply_to_raw() -> None:
    composite = BlendRangeChannel.from_values(this_layer_black=64)
    channels = [BlendRangeChannel.default() for _ in range(3)]
    channels[1].underlying_white = (180, 220)
    ranges = BlendRanges.from_channels(composite, channels)
    assert not ranges.is_default

    raw = LayerBlendingRanges()
    ranges.apply_to_raw(raw)
    assert raw.composite_ranges == [(64 | (64 << 8), 65535), (0, 65535)]
    assert len(raw.channel_ranges) == 3
    assert BlendRanges.from_raw(raw) == ranges

    with io.BytesIO() as f:
        raw.write(f)
        f.seek(0)
        assert BlendRanges.from_raw(LayerBlendingRanges.read(f)) == ranges


@pytest.mark.parametrize(
    "composite_ranges, channel_ranges",
    [
        ([(0, 65535)], []),
        ([(0, 65535), (0, 65535), (0, 65535)], []),
        ([(0, 65535), (0, 65535)], [[(0, 65535)]]),
    ],
)
def test_raw_write_validation(composite_ranges, channel_ranges) -> None:  # type: ignore[no-untyped-def]
    raw = LayerBlendingRanges(composite_ranges, channel_ranges)
    with pytest.raises(ValueError):
        raw.write(io.BytesIO())


def test_compute_visibility_default() -> None:
    ranges = BlendRanges.from_raw(LayerBlendingRanges())
    weight = ranges.compute_visibility(np.random.rand(3, 4, 3), np.random.rand(3, 4, 3))
    assert weight.shape == (3, 4, 1)
    np.testing.assert_allclose(weight, 1.0)


def test_compute_visibility_hard_cutoff() -> None:
    ranges = BlendRanges(BlendRangeChannel.from_values(this_layer_black=128))
    source = _gray([0.0, 127 / 255, 128 / 255, 1.0])
    weight = ranges.compute_visibility(source, np.zeros_like(source))
    np.testing.assert_allclose(weight[0, :, 0], [0.0, 0.0, 1.0, 1.0])


def test_compute_visibility_split_fade() -> None:
    composite = BlendRangeChannel.default()
    composite.this_layer_white = (100, 200)
    ranges = BlendRanges(composite)
    source = _gray([50 / 255, 100 / 255, 150 / 255, 200 / 255, 1.0])
    weight = ranges.compute_visibility(source, np.zeros_like(source))
    np.testing.assert_allclose(weight[0, :, 0], [1.0, 1.0, 0.5, 0.0, 0.0], atol=1e-5)


def test_compute_visibility_underlying() -> None:
    ranges = BlendRanges(BlendRangeChannel.from_values(underlying_white=100))
    source = np.ones((1, 2, 3), dtype=np.float32)
    backdrop = np.array([[[0.2] * 3, [0.8] * 3]], dtype=np.float32)
    weight = ranges.compute_visibility(source, backdrop)
    np.testing.assert_allclose(weight[0, :, 0], [1.0, 0.0])


def test_compute_visibility_luminosity() -> None:
    ranges = BlendRanges(BlendRangeChannel.from_values(this_layer_black=100))
    # Pure green has luminosity 0.587 * 255 ~ 150; pure blue ~ 29.
    source = np.array([[[0.0, 1.0, 0.0], [0.0, 0.0, 1.0]]], dtype=np.float32)
    weight = ranges.compute_visibility(source, np.zeros_like(source))
    np.testing.assert_allclose(weight[0, :, 0], [1.0, 0.0])


def test_compute_visibility_per_channel() -> None:
    channels = [BlendRangeChannel.default() for _ in range(4)]
    channels[0] = BlendRangeChannel.from_values(this_layer_white=100)
    ranges = BlendRanges(BlendRangeChannel.default(), channels)
    source = np.array([[[1.0, 0.0, 0.0], [0.0, 1.0, 1.0]]], dtype=np.float32)
    weight = ranges.compute_visibility(source, np.zeros_like(source))
    np.testing.assert_allclose(weight[0, :, 0], [0.0, 1.0])


def test_to_pil_mask() -> None:
    ranges = BlendRanges(BlendRangeChannel.from_values(this_layer_black=128))
    source = _gray([0.0, 1.0])
    mask = ranges.to_pil_mask(source, np.zeros_like(source))
    assert isinstance(mask, Image.Image)
    assert mask.mode == "L"
    assert mask.size == (2, 1)
    assert np.asarray(mask).tolist() == [[0, 255]]


def _make_psd() -> PSDImage:
    psd = PSDImage.new("RGB", (4, 1))
    pixels = np.array(
        [[[0, 0, 0], [100, 100, 100], [200, 200, 200], [255, 255, 255]]],
        dtype=np.uint8,
    )
    psd.create_pixel_layer(Image.fromarray(pixels), name="layer")
    return psd


def test_layer_blend_ranges_persist() -> None:
    psd = _make_psd()
    layer = psd[0]
    assert layer.blend_ranges.is_default

    ranges = layer.blend_ranges
    ranges.composite.this_layer_black = (50, 150)
    ranges.channels[2].underlying_white = (240, 240)
    layer.blend_ranges = ranges
    assert psd.is_updated()
    assert layer.blend_ranges == ranges

    with io.BytesIO() as f:
        psd.save(f)
        f.seek(0)
        reloaded = PSDImage.open(f)
    assert reloaded[0].blend_ranges == ranges


def test_layer_blend_ranges_type_check() -> None:
    psd = _make_psd()
    with pytest.raises(TypeError):
        psd[0].blend_ranges = "invalid"  # type: ignore[assignment]
