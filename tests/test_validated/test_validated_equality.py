from copy import copy, deepcopy

import pytest

from returns.primitives.exceptions import ImmutableStateError
from returns.validated import Invalid, Valid


def test_equals():
    """Ensures that ``.equals`` method works correctly."""
    inner_value = 1

    assert Valid(inner_value).equals(Valid(inner_value))
    assert Invalid((inner_value,)).equals(Invalid((inner_value,)))


def test_not_equals():
    """Ensures that ``.equals`` method works correctly."""
    inner_value = 1

    assert not Valid(inner_value).equals(Invalid((inner_value,)))
    assert not Valid(inner_value).equals(Valid(0))
    assert not Invalid((inner_value,)).equals(Valid(inner_value))
    assert not Invalid((inner_value,)).equals(Invalid((0,)))


def test_non_equality():
    """Ensures that containers are not compared to regular values."""
    input_value = 5

    assert Invalid((input_value,)) != input_value
    assert Valid(input_value) != input_value
    assert Invalid((input_value,)) != Valid(input_value)
    assert hash(Invalid((1,)))
    assert hash(Valid(1))


def test_immutability():
    """Ensures that Validated containers are immutable."""
    with pytest.raises(ImmutableStateError):
        Valid(1).missing = 2

    with pytest.raises(ImmutableStateError):
        Invalid((1,)).missing = 2


def test_immutable_copy():
    """Ensures that containers return themselves from copy helpers."""
    valid = Valid(1)
    invalid = Invalid((0,))
    assert valid is copy(valid)
    assert valid is deepcopy(valid)
    assert invalid is copy(invalid)
    assert invalid is deepcopy(invalid)
