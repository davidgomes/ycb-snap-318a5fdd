import pytest

from returns.converters import result_to_validated, validated_to_result
from returns.methods import cond
from returns.pointfree import bind_validated
from returns.result import Failure, Success
from returns.validated import Invalid, Valid, Validated, validated


@validated
def _function(number: int) -> float:
    return number / number


@validated(exceptions=(ZeroDivisionError,))
def _function_two(number: int | str) -> float:
    assert isinstance(number, int)
    return number / number


def test_validated_success():
    """Ensures that validated decorator works correctly for Valid case."""
    assert _function(1) == Valid(1.0)


def test_validated_failure():
    """Ensures that validated decorator works correctly for Invalid case."""
    errors = _function(0).failure()
    assert len(errors) == 1
    assert isinstance(errors[0], ZeroDivisionError)


def test_validated_failure_with_expected_error():
    """Ensures that validated decorator catches only listed exceptions."""
    errors = _function_two(0).failure()
    assert isinstance(errors[0], ZeroDivisionError)


def test_validated_unexpected_error():
    """Ensures that validated decorator re-raises unlisted exceptions."""
    with pytest.raises(AssertionError):
        _function_two('0')


def test_validated_preserves_name():
    """Ensures that validated decorator preserves function names."""
    assert _function.__name__ == '_function'
    assert _function_two.__name__ == '_function_two'


def test_result_to_validated():
    """Ensures that ``Result`` is converted to ``Validated``."""
    assert result_to_validated(Success(1)) == Valid(1)
    assert result_to_validated(Failure('a')) == Invalid(('a',))


def test_validated_to_result():
    """Ensures that ``Validated`` is converted to ``Result``."""
    assert validated_to_result(Valid(1)) == Success(1)
    assert validated_to_result(Invalid(('a', 'b'))) == Failure(('a', 'b'))


def test_cond():
    """Ensures that ``cond`` works with ``Validated``."""
    assert cond(Validated, True, 1, 'error') == Valid(1)  # noqa: FBT003
    assert cond(Validated, False, 1, 'error') == Invalid(  # noqa: FBT003
        ('error',),
    )


def test_bind_validated():
    """Ensures that ``bind_validated`` pointfree function works."""

    def factory(number: int) -> Validated[int, str]:
        if number > 0:
            return Valid(number + 1)
        return Invalid(('negative',))

    assert bind_validated(factory)(Valid(1)) == Valid(2)
    assert bind_validated(factory)(Valid(-1)) == Invalid(('negative',))
    assert bind_validated(factory)(Invalid(('a',))) == Invalid(('a',))
