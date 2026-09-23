import io

import numpy as np
import pytest

from psd_tools.api.blend_range import BlendRangeChannel, BlendRanges
from psd_tools.psd.layer_and_mask import LayerBlendingRanges


def test_channel_raw_roundtrip() -> None:
    raw = [(0x6432, 0xFFC8), (0, 65535)]
    ch = BlendRangeChannel.from_raw(raw)
    assert ch.this_layer_black == (50, 100)
    assert ch.this_layer_white == (200, 255)
    assert ch.this_layer_black_split and ch.this_layer_white_split
    assert not ch.underlying_black_split
    assert ch.to_raw() == raw
    assert BlendRangeChannel.default().is_default
    assert ch.describe()


def test_ranges_from_null() -> None:
    br = BlendRanges.from_raw(LayerBlendingRanges(None, None))  # type: ignore[arg-type]
    assert len(br) == 0 and br.is_default and br.composite.is_default


def test_ranges_indexing_and_apply() -> None:
    br = BlendRanges.from_raw(LayerBlendingRanges())
    assert br.channel_count == 4 and br[-1] is br.channels[3]
    br.channels[0] = BlendRangeChannel.from_values(this_layer_black=10)
    raw = LayerBlendingRanges()
    br.apply_to_raw(raw)
    assert raw.channel_ranges[0][0] == (0x0A0A, 65535)
    assert not br.is_default and br.describe()


def test_compute_visibility() -> None:
    br = BlendRanges.from_channels(
        BlendRangeChannel.from_values(this_layer_black=(50, 100)), []
    )
    weight = br.compute_visibility(np.full((2, 3, 3), 0.3), np.zeros((2, 3, 3)))
    assert weight.shape == (2, 3, 1)
    np.testing.assert_allclose(weight, (0.3 * 255 - 50) / 50, rtol=1e-5)
    assert br.to_pil_mask(np.ones((2, 3, 3)), np.zeros((2, 3, 3))).mode == "L"

    br = BlendRanges.from_channels(
        BlendRangeChannel.default(),
        [BlendRangeChannel.from_values(underlying_white=100)],
    )
    weight = br.compute_visibility(np.zeros((1, 2, 3)), np.array([[[0.2], [0.9]]]))
    assert weight.ravel().tolist() == [1.0, 0.0]


def test_write_validation() -> None:
    with pytest.raises(ValueError):
        LayerBlendingRanges(composite_ranges=[(0, 65535)]).write(io.BytesIO())
    with pytest.raises(ValueError):
        LayerBlendingRanges(channel_ranges=[[(0, 65535)]]).write(io.BytesIO())
