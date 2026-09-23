import numpy as np
from PIL import Image

from psd_tools.api.blend_range import BlendRangeChannel
from psd_tools.api.psd_image import PSDImage
from psd_tools.composite import composite


def _make_psd(backdrop: tuple[int, int, int]) -> PSDImage:
    psd = PSDImage.new("RGB", (4, 1))
    psd.create_pixel_layer(Image.new("RGB", (4, 1), backdrop), name="background")
    pixels = np.array(
        [[[0, 0, 0], [100, 100, 100], [200, 200, 200], [255, 0, 0]]],
        dtype=np.uint8,
    )
    psd.create_pixel_layer(Image.fromarray(pixels), name="top")
    return psd


def test_blend_if_default_is_noop() -> None:
    psd = _make_psd((0, 0, 255))
    color, _, _ = composite(psd)
    np.testing.assert_allclose(color[0, 0], [0.0, 0.0, 0.0], atol=1e-6)


def test_blend_if_this_layer_black() -> None:
    psd = _make_psd((0, 0, 255))
    layer = psd[1]
    ranges = layer.blend_ranges
    ranges.composite.this_layer_black = (50, 150)
    layer.blend_ranges = ranges

    color, _, alpha = composite(psd)
    np.testing.assert_allclose(alpha[..., 0], 1.0, atol=1e-6)
    # Black is hidden, revealing the blue backdrop.
    np.testing.assert_allclose(color[0, 0], [0.0, 0.0, 1.0], atol=1e-6)
    # Gray 100 sits halfway along the split slider.
    np.testing.assert_allclose(
        color[0, 1],
        [0.5 * 100 / 255, 0.5 * 100 / 255, 0.5 + 0.5 * 100 / 255],
        atol=1e-5,
    )
    np.testing.assert_allclose(color[0, 2], [200 / 255] * 3, atol=1e-5)
    # Red luminosity is 0.299 * 255 ~ 76, i.e. 26% along the fade.
    weight = (0.299 * 255 - 50) / 100
    np.testing.assert_allclose(color[0, 3], [weight, 0.0, 1.0 - weight], atol=1e-4)


def test_blend_if_underlying() -> None:
    psd = _make_psd((255, 255, 255))
    layer = psd[1]
    ranges = layer.blend_ranges
    ranges.composite = BlendRangeChannel.from_values(underlying_white=200)
    layer.blend_ranges = ranges

    color, _, _ = composite(psd)
    # The white backdrop is beyond the underlying white cut-off everywhere.
    np.testing.assert_allclose(color[0], 1.0, atol=1e-6)


def test_blend_if_per_channel() -> None:
    psd = _make_psd((0, 255, 0))
    layer = psd[1]
    ranges = layer.blend_ranges
    ranges.channels[0] = BlendRangeChannel.from_values(this_layer_white=150)
    layer.blend_ranges = ranges

    color, _, _ = composite(psd)
    np.testing.assert_allclose(color[0, 0], [0.0, 0.0, 0.0], atol=1e-6)
    np.testing.assert_allclose(color[0, 1], [100 / 255] * 3, atol=1e-5)
    # Red channel values above 150 hide the pixel.
    np.testing.assert_allclose(color[0, 2], [0.0, 1.0, 0.0], atol=1e-6)
    np.testing.assert_allclose(color[0, 3], [0.0, 1.0, 0.0], atol=1e-6)
