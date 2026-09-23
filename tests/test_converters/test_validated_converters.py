from returns.converters import result_to_validated, validated_to_result
from returns.result import Failure, Success
from returns.validated import Invalid, Valid


def test_result_to_validated():
    """Ensures that ``Result`` is converted to ``Validated``."""
    assert result_to_validated(Success(1)) == Valid(1)
    assert result_to_validated(Failure('a')) == Invalid(('a',))


def test_validated_to_result():
    """Ensures that ``Validated`` is converted to ``Result``."""
    assert validated_to_result(Valid(1)) == Success(1)
    assert validated_to_result(Invalid(('a', 'b'))) == Failure(('a', 'b'))


def test_round_trip():
    """Ensures that errors are wrapped once when converting back and forth."""
    assert validated_to_result(result_to_validated(Failure('a'))) == Failure(
        ('a',),
    )
    assert result_to_validated(validated_to_result(Valid(1))) == Valid(1)
