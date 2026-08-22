import pytest

from returns.validated import Invalid, Valid, validated


@validated
def _function(number: int) -> float:
    return number / number


@validated(exceptions=(ZeroDivisionError,))
def _function_two(number: int | str) -> float:
    assert isinstance(number, int)
    return number / number


def test_validated_success():
    """Ensures that validated decorator works for the success case."""
    assert _function(1) == Valid(1.0)
    assert _function.__name__ == '_function'


def test_validated_failure():
    """Ensures that validated decorator wraps exceptions into Invalid."""
    failed = _function(0)
    assert isinstance(failed, Invalid)
    assert isinstance(failed.failure()[0], ZeroDivisionError)


def test_validated_expected_error():
    """Ensures that only listed exceptions are caught."""
    failed = _function_two(0)
    assert isinstance(failed.failure()[0], ZeroDivisionError)
    assert _function_two.__name__ == '_function_two'


def test_validated_unexpected_error():
    """Ensures that unexpected exceptions are re-raised."""
    with pytest.raises(AssertionError):
        _function_two('0')
