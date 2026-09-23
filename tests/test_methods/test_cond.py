import pytest

from returns.methods import cond
from returns.pointfree import cond as pointfree_cond
from returns.validated import Invalid, Valid, Validated


@pytest.mark.parametrize(
    ('is_success', 'expected'),
    [
        (True, Valid(1)),
        (False, Invalid(('a',))),
    ],
)
def test_cond_validated(is_success: bool, expected):  # noqa: FBT001
    """Ensures that ``cond`` creates ``Validated`` containers."""
    assert cond(Validated, is_success, 1, 'a') == expected
    assert pointfree_cond(Validated, 1, 'a')(is_success) == expected
