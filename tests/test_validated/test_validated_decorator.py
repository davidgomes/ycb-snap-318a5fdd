import pytest

from returns.validated import Invalid, Valid, validated


@validated
def _function(number: int) -> float:
    return number / number


@validated(exceptions=(ZeroDivisionError,))
def _function_two(number: int | str) -> float:
    assert isinstance(number, int)
    return number / number


@validated((ZeroDivisionError,))
def _function_three(number: int) -> float:
    return number / number


def test_validated_success():
    """Ensures the decorator returns ``Valid`` and keeps the name."""
    assert _function(1) == Valid(1.0)
    assert _function.__name__ == '_function'


def test_validated_failure():
    """Ensures a caught exception is stored in a one-element tuple."""
    failed = _function(0)

    assert isinstance(failed, Invalid)
    assert isinstance(failed.failure()[0], ZeroDivisionError)


def test_validated_expected_error_keyword():
    """Ensures the exceptions parameter catches only the listed types."""
    failed = _function_two(0)

    assert isinstance(failed.failure()[0], ZeroDivisionError)
    assert _function_two.__name__ == '_function_two'


def test_validated_expected_error_positional():
    """Ensures a positional exception tuple is accepted."""
    failed = _function_three(0)

    assert isinstance(failed.failure()[0], ZeroDivisionError)
    assert _function_three.__name__ == '_function_three'


def test_validated_unexpected_error_propagates():
    """Ensures exceptions outside the list are not swallowed."""
    with pytest.raises(AssertionError):
        _function_two('0')
