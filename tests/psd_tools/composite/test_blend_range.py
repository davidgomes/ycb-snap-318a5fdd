import numpy as np

from psd_tools.api.blend_range import BlendRangeChannel, BlendRanges
from psd_tools.api.psd_image import PSDImage
from psd_tools.composite import composite

from ..utils import full_name


def _luma(color: np.ndarray) -> np.ndarray:
    return 0.299 * color[:, :, 0] + 0.587 * color[:, :, 1] + 0.114 * color[:, :, 2]


def test_compositor_applies_blend_if() -> None:
    psd = PSDImage.open(full_name("layers/pixel-layer.psd"))
    layer = psd[0]
    baseline_color, baseline_shape, baseline_alpha = composite(layer)

    hidden = BlendRanges.from_channels(
        BlendRangeChannel.from_values(this_layer_black=255),
        [BlendRangeChannel.default() for _ in range(layer.blend_ranges.channel_count)],
    )
    layer.blend_ranges = hidden
    _, hidden_shape, hidden_alpha = composite(layer)
    assert hidden_alpha.max() == 0.0
    assert hidden_shape.max() == 0.0

    psd = PSDImage.open(full_name("layers/pixel-layer.psd"))
    layer = psd[0]
    split = BlendRangeChannel.default()
    split.this_layer_black = (0, 255)
    layer.blend_ranges = BlendRanges.from_channels(
        split,
        [BlendRangeChannel.default() for _ in range(layer.blend_ranges.channel_count)],
    )
    _, _, alpha = composite(layer)
    expected = baseline_shape[:, :, 0] * _luma(baseline_color)
    np.testing.assert_allclose(alpha[:, :, 0], expected, atol=1e-5)

    # A red-channel ceiling hides pixels the gray slider would still show.
    psd = PSDImage.open(full_name("layers/pixel-layer.psd"))
    layer = psd[0]
    layer.blend_ranges = BlendRanges.from_channels(
        BlendRangeChannel.default(),
        [
            BlendRangeChannel.from_values(this_layer_white=0),
            BlendRangeChannel.default(),
            BlendRangeChannel.default(),
            BlendRangeChannel.default(),
        ],
    )
    _, _, red_alpha = composite(layer)
    assert red_alpha.mean() < baseline_alpha.mean()

    psd = PSDImage.open(full_name("layers/pixel-layer.psd"))
    layer = psd[0]
    layer.blend_ranges = BlendRanges.from_channels(
        BlendRangeChannel.from_values(underlying_black=1),
        [BlendRangeChannel.default() for _ in range(layer.blend_ranges.channel_count)],
    )
    _, _, dark_alpha = composite(psd, color=(0.0, 0.0, 0.0), alpha=1.0)
    _, _, light_alpha = composite(psd, color=(1.0, 1.0, 1.0), alpha=1.0)
    assert dark_alpha.max() == 0.0
    assert light_alpha.max() > 0.0
