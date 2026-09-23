import logging
from typing import Any, Optional

import numpy as np
import pytest

from psd_tools.api.blend_range import BlendRangeChannel, BlendRanges
from psd_tools.api.layers import Layer
from psd_tools.api.psd_image import PSDImage
from psd_tools.composite import composite
from psd_tools.constants import BlendMode, CompatibilityMode
from PIL import Image

from ..utils import full_name

logger = logging.getLogger(__name__)


def _mse(x: Any, y: Any) -> Any:
    return np.nanmean((x - y) ** 2)


def composite_error(
    layer: Any, threshold: float, force: bool = True, channel: Optional[str] = None
) -> Any:
    reference = layer.numpy(channel)
    color, _, alpha = composite(layer, force=force)
    result = color
    if reference.shape[2] > color.shape[2]:
        result = np.concatenate((color, alpha), axis=2)
    error = _mse(reference, result)
    assert error <= threshold
    return error


def check_composite_quality(
    filename: str, threshold: float = 0.1, force: bool = False
) -> None:
    psd = PSDImage.open(full_name(filename))
    composite_error(psd, threshold, force)


@pytest.mark.parametrize(
    ("filename",),
    [
        ("background-red-opacity-80.psd",),
        ("32bit.psd",),
        ("clipping-mask2.psd",),
        ("clipping-mask.psd",),
        ("clipping-mask2.psd",),
        ("clipping-mask3.psd",),
        ("opacity-fill.psd",),
        ("transparency/transparency-group.psd",),
        ("transparency/knockout-isolated-groups.psd",),
        ("transparency/clip-opacity.psd",),
        ("transparency/fill-opacity.psd",),
        ("mask.psd",),
        ("mask-disabled.psd",),
        # ('vector-mask.psd', ),  # 32-bit blending not working.
        ("vector-mask-disabled.psd",),
        ("vector-mask3.psd",),
    ],
)
def test_composite_quality(filename: str) -> None:
    check_composite_quality(filename, 0.01, False)


@pytest.mark.parametrize(
    ("filename",),
    [
        ("advanced-blending.psd",),
        ("vector-mask2.psd",),
    ],
)
@pytest.mark.xfail
def test_composite_quality_xfail(filename: str) -> None:
    check_composite_quality(filename, 0.01, False)


@pytest.mark.parametrize(
    "filename",
    [
        "smartobject-layer.psd",
        "type-layer.psd",
        "gradient-fill.psd",
        "shape-layer.psd",
        "pixel-layer.psd",
        "solid-color-fill.psd",
        "pattern-fill.psd",
    ],
)
def test_composite_minimal(filename: str) -> None:
    source = PSDImage.open(full_name("layers-minimal/" + filename))
    reference = PSDImage.open(full_name("layers/" + filename)).numpy()
    color, _, alpha = composite(source, force=True)
    result = color
    if reference.shape[2] > color.shape[2]:
        result = np.concatenate((color, alpha), axis=2)
    assert _mse(reference, result) <= 0.017


@pytest.mark.parametrize(
    "colormode, depth",
    [
        ("bitmap", 1),
        ("cmyk", 8),
        ("duotone", 8),
        ("grayscale", 8),
        ("index_color", 8),
        ("rgb", 8),
        ("rgba", 8),
        ("lab", 8),
        ("multichannel", 16),
    ],
)
def test_composite_colormodes(colormode: str, depth: int) -> None:
    filename = "colormodes/4x4_%gbit_%s.psd" % (depth, colormode)
    psd = PSDImage.open(full_name(filename))
    composite_error(psd, 0.01, False, "color")


# These failures are due to inaccurate gradient fill synthesis.
@pytest.mark.parametrize(
    "colormode, depth",
    [
        ("cmyk", 16),
        ("grayscale", 16),
        ("lab", 16),
        ("rgb", 16),
        ("grayscale", 32),
        ("rgb", 32),
    ],
)
@pytest.mark.xfail
def test_composite_colormodes_xfail(colormode: str, depth: int) -> None:
    filename = "colormodes/4x4_%gbit_%s.psd" % (depth, colormode)
    psd = PSDImage.open(full_name(filename))
    composite_error(psd, 0.01, False, "color")


def test_composite_artboard() -> None:
    psd = PSDImage.open(full_name("artboard.psd"))
    document_image = psd.numpy()
    assert document_image.shape[:2] == (psd.height, psd.width)
    artboard = psd[0]
    artboard_image = composite(artboard)[0]
    assert artboard_image.shape[:2] == (artboard.height, artboard.width)


def test_composite_viewport() -> None:
    psd = PSDImage.open(full_name("layers/smartobject-layer.psd"))
    bbox = (1, 1, 31, 31)

    shape = (bbox[3] - bbox[1], bbox[2] - bbox[0], 1)
    assert composite(psd)[1].shape == (psd.height, psd.width, 1)
    assert composite(psd, viewport=bbox)[1].shape == shape

    assert composite(psd[0])[1].shape == (psd[0].height, psd[0].width, 1)
    assert composite(psd[0], viewport=bbox)[1].shape == shape


@pytest.mark.parametrize(
    "colormode, depth, mode, ignore_preview, apply_icc",
    [
        ("bitmap", 1, "1", False, False),
        ("cmyk", 8, "CMYK", False, False),
        ("duotone", 8, "L", False, False),
        ("grayscale", 8, "L", False, False),
        ("index_color", 8, "P", False, False),
        ("rgb", 8, "RGB", False, False),
        ("rgba", 8, "RGB", False, False),  # Extra alpha is not transparency
        ("lab", 8, "LAB", False, False),
        ("multichannel", 16, "L", False, False),
        ("bitmap", 1, "1", True, False),
        ("cmyk", 8, "CMYK", True, False),
        ("duotone", 8, "LA", True, False),
        ("grayscale", 8, "L", True, False),
        ("index_color", 8, "RGBA", True, False),
        ("rgb", 8, "RGB", True, False),
        ("rgba", 8, "RGB", True, False),  # Extra alpha is not transparency
        ("lab", 8, "LAB", True, False),
        ("multichannel", 16, "LA", True, False),
        ("cmyk", 8, "RGBA", True, True),
        ("rgb", 8, "RGB", False, True),
        ("duotone", 8, "L", False, True),
    ],
)
def test_composite_pil(
    colormode: str, depth: int, mode: str, ignore_preview: bool, apply_icc: bool
) -> None:
    from PIL import Image

    filename = "colormodes/4x4_%gbit_%s.psd" % (depth, colormode)
    psd = PSDImage.open(full_name(filename))
    image = psd.composite(ignore_preview=ignore_preview, apply_icc=apply_icc)
    assert isinstance(image, Image.Image)
    assert image.mode == mode
    for layer in psd:
        assert isinstance(layer.composite(apply_icc=apply_icc), Image.Image)


def test_composite_layer_filter() -> None:
    psd = PSDImage.open(full_name("colormodes/4x4_8bit_rgba.psd"))
    # Check layer_filter.
    rendered = psd.composite(layer_filter=lambda x: False)
    reference = psd.topil()
    assert reference is not None
    assert all(a != b for a, b in zip(rendered.getextrema(), reference.getextrema()))


def test_apply_mask() -> None:
    from PIL import Image

    psd = PSDImage.open(full_name("masks/2.psd"))
    reference = np.asarray(Image.open(full_name("masks/2.png"))) / 255.0
    result = np.concatenate(composite(psd)[::2], axis=2)
    assert reference.shape == result.shape
    # Hidden color seems different.
    assert _mse(reference[:, :, -1], result[:, :, -1]) <= 0.01


def test_group_mask() -> None:
    psd = PSDImage.open(full_name("masks3.psd"))
    reference = psd.numpy()
    result = composite(psd, force=True)[0]
    assert _mse(reference, result) <= 0.01


def test_apply_opacity() -> None:
    psd = PSDImage.open(full_name("opacity-fill.psd"))
    result = composite(psd)
    assert _mse(psd.numpy("shape"), result[2]) < 0.01


def test_composite_clipping_mask() -> None:
    psd = PSDImage.open(full_name("clipping-mask.psd"))
    reference = composite(psd)
    result = composite(psd, layer_filter=lambda x: x.name != "Shape 3")
    assert _mse(reference[0], result[0]) > 0


def test_composite_group_clipping_photoshop() -> None:
    psd = PSDImage.open(full_name("group-clipping/group-clipping.psd"))
    reference = Image.open(full_name("group-clipping/group-clipping-photoshop.png"))
    psd.compatibility_mode = CompatibilityMode.PHOTOSHOP
    result = psd.composite(force=True)
    assert (
        _mse(np.array(reference, dtype=np.float32), np.array(result, dtype=np.float32))
        <= 0.001
    )


def test_composite_group_clipping_clip_studio() -> None:
    psd = PSDImage.open(full_name("group-clipping/group-clipping.psd"))
    reference = Image.open(full_name("group-clipping/group-clipping-clip-studio.png"))
    psd.compatibility_mode = CompatibilityMode.CLIP_STUDIO_PAINT
    result = psd.composite(force=True)
    assert (
        _mse(np.array(reference, dtype=np.float32), np.array(result, dtype=np.float32))
        <= 0.001
    )


def test_composite_stroke() -> None:
    psd = PSDImage.open(full_name("stroke.psd"))
    reference = composite(psd, force=True)
    result = composite(psd)
    assert _mse(reference[0], result[0]) > 0


def test_composite_pixel_layer_with_vector_stroke() -> None:
    psd = PSDImage.open(full_name("effects/stroke-without-vector-mask.psd"))
    reference = composite(psd, force=True)
    result = composite(psd)
    assert _mse(reference[0], result[0]) <= 0.01


RED = (255, 0, 0)
BLUE = (0, 0, 255)


def _gray(*values: int) -> list[tuple[int, int, int]]:
    return [(v, v, v) for v in values]


def _row_image(pixels: list[tuple[int, int, int]]) -> Image.Image:
    return Image.fromarray(np.array([pixels], dtype=np.uint8))


def _blend_if_psd(
    bottom: list[tuple[int, int, int]], top: list[tuple[int, int, int]]
) -> tuple[PSDImage, Layer]:
    psd = PSDImage.new("RGB", (len(bottom), 1))
    psd.create_pixel_layer(_row_image(bottom), name="bottom")
    top_layer = psd.create_pixel_layer(_row_image(top), name="top")
    return psd, top_layer


def _composite_pixels(psd: PSDImage) -> list[tuple[int, int, int]]:
    color = composite(psd)[0]
    return [tuple(pixel) for pixel in np.round(255 * color[0]).astype(int).tolist()]


def test_composite_blend_if_this_layer(tmp_path: Any) -> None:
    top = _gray(0, 64, 128, 255)
    psd, layer = _blend_if_psd([RED] * 4, top)
    assert _composite_pixels(psd) == top

    layer.blend_ranges = BlendRanges(
        BlendRangeChannel.from_values(this_layer_black=100)
    )
    expected = [RED, RED, top[2], top[3]]
    assert _composite_pixels(psd) == expected

    # The saved preview and the reloaded layers honor the blend ranges.
    out = tmp_path / "blend_if.psd"
    psd.save(str(out))
    reloaded = PSDImage.open(str(out))
    assert _composite_pixels(reloaded) == expected
    preview = reloaded.topil()
    assert preview is not None
    assert [tuple(p) for p in np.asarray(preview.convert("RGB"))[0].tolist()] == (
        expected
    )


def test_composite_blend_if_underlying() -> None:
    bottom = _gray(0, 64, 192, 255)
    psd, layer = _blend_if_psd(bottom, [BLUE] * 4)
    layer.blend_ranges = BlendRanges(
        BlendRangeChannel.from_values(underlying_white=128)
    )
    assert _composite_pixels(psd) == [BLUE, BLUE, bottom[2], bottom[3]]


def test_composite_blend_if_channel() -> None:
    top = [(200, 0, 0), (50, 0, 0), (50, 200, 0), (0, 0, 200)]
    psd, layer = _blend_if_psd(_gray(255, 255, 255, 255), top)
    blend_ranges = layer.blend_ranges
    blend_ranges[0].this_layer_white = (100, 100)  # Red of this layer.
    blend_ranges[1].this_layer_black = (0, 100)  # Green of this layer, split.
    layer.blend_ranges = blend_ranges

    # Red hides the first pixel; the green ramp hides pixels without green.
    assert _composite_pixels(psd) == [
        (255, 255, 255),
        (255, 255, 255),
        (50, 200, 0),
        (255, 255, 255),
    ]


def test_composite_blend_if_split_fades_linearly() -> None:
    values = (0, 51, 102, 204, 255)
    psd, layer = _blend_if_psd(_gray(0, 0, 0, 0, 0), _gray(*values))
    layer.blend_ranges = BlendRanges(BlendRangeChannel(this_layer_black=(0, 255)))
    # Over black, the result is the layer value weighted by its visibility.
    color = composite(psd)[0][0, :, 0]
    np.testing.assert_allclose(color, [(v / 255.0) ** 2 for v in values], atol=1e-5)


def test_composite_blend_if_group() -> None:
    top = _gray(0, 64, 128, 255)
    psd, layer = _blend_if_psd([RED] * 4, top)
    group = psd.create_group([layer], blend_mode=BlendMode.NORMAL)
    group.blend_ranges = BlendRanges(
        BlendRangeChannel.from_values(this_layer_black=100)
    )
    assert _composite_pixels(psd) == [RED, RED, top[2], top[3]]
