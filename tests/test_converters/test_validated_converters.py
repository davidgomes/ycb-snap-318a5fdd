from returns.converters import result_to_validated, validated_to_result
from returns.result import Failure, Success
from returns.validated import Invalid, Valid


def test_result_to_validated():
    """Ensures Result converts into Validated."""
    assert result_to_validated(Success(1)) == Valid(1)
    assert result_to_validated(Failure('e')) == Invalid(('e',))


def test_validated_to_result():
    """Ensures Validated converts into Result."""
    assert validated_to_result(Valid(1)) == Success(1)
    assert validated_to_result(Invalid((1, 2))) == Failure((1, 2))
