import pytest

from returns.validated import Invalid, Valid, validated


@validated
def _function(number: int) -> float:
    return number / number


@validated(exceptions=(ZeroDivisionError,))
def _function_two(number: int | str) -> float:
    assert isinstance(number, int)
    return number / number


@validated((ZeroDivisionError,))  # no name
def _function_three(number: int | str) -> float:
    assert isinstance(number, int)
    return number / number


def test_validated_valid():
    """Ensures that validated decorator works correctly for Valid case."""
    assert _function(1) == Valid(1.0)


def test_validated_invalid():
    """Ensures that validated decorator wraps an exception into a tuple."""
    failed = _function(0)

    assert isinstance(failed, Invalid)
    assert len(failed.failure()) == 1
    assert isinstance(failed.failure()[0], ZeroDivisionError)


def test_validated_invalid_with_expected_error():
    """Ensures that validated decorator catches listed exceptions."""
    failed = _function_two(0)
    assert isinstance(failed.failure()[0], ZeroDivisionError)

    failed2 = _function_three(0)
    assert isinstance(failed2.failure()[0], ZeroDivisionError)


def test_validated_with_non_expected_error():
    """Ensures that validated decorator does not catch unlisted exceptions."""
    with pytest.raises(AssertionError):
        _function_two('0')


def test_validated_preserves_name():
    """Ensures that validated decorator preserves the function's name."""
    assert _function.__name__ == '_function'
    assert _function_two.__name__ == '_function_two'
    assert _function_three.__name__ == '_function_three'
