import pytest

from returns.primitives.exceptions import UnwrapFailedError
from returns.validated import Invalid, Valid


def test_unwrap():
    """Ensures unwrap works."""
    assert Valid(1).unwrap() == 1
    with pytest.raises(UnwrapFailedError):
        Invalid((1,)).unwrap()


def test_failure():
    """Ensures failure works."""
    assert Invalid((1, 2)).failure() == (1, 2)
    with pytest.raises(UnwrapFailedError):
        Valid(1).failure()


def test_value_or():
    """Ensures value_or works."""
    assert Valid(1).value_or(2) == 1
    assert Invalid((1,)).value_or(2) == 2
