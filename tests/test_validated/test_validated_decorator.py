import pytest

from returns.validated import Invalid, Valid, validated


@validated
def _function(number: int) -> float:
    """Divide a number by itself."""
    return number / number


@validated(exceptions=(ZeroDivisionError,))
def _function_two(number: int | str) -> float:
    """Divide a number by itself, catching only zero division."""
    assert isinstance(number, int)
    return number / number


@validated((ZeroDivisionError,))
def _function_three(number: int | str) -> float:
    """Divide a number by itself using a positional exception tuple."""
    assert isinstance(number, int)
    return number / number


def test_validated_success():
    """Ensures that the decorator returns Valid for a successful call."""
    assert _function(1) == Valid(1.0)
    assert _function.__name__ == '_function'


def test_validated_failure():
    """Ensures that the decorator returns Invalid for a caught error."""
    failed = _function(0)

    assert isinstance(failed, Invalid)
    assert isinstance(failed.failure()[0], ZeroDivisionError)
    assert failed.failure()[0].args == ('division by zero',)


def test_validated_failure_with_expected_error():
    """Ensures explicit exception types are wrapped into a 1-tuple."""
    failed = _function_two(0)
    failed_positional = _function_three(0)

    assert isinstance(failed.failure()[0], ZeroDivisionError)
    assert isinstance(failed_positional.failure()[0], ZeroDivisionError)
    assert _function_two.__name__ == '_function_two'
    assert _function_three.__name__ == '_function_three'


def test_unexpected_error_is_raised():
    """Ensures exceptions outside the allow-list propagate."""
    with pytest.raises(AssertionError):
        _function_two('0')
