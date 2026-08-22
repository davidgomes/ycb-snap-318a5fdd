from operator import add

from returns.result import Failure, Success
from returns.validated import Invalid, Valid, Validated


def _add3(first: int, second: int, third: int) -> int:
    return first + second + third


def _zero() -> int:
    return 0


def test_from_value_and_failure():
    """Ensures unit constructors wrap values correctly."""
    assert Validated.from_value(1) == Valid(1)
    assert Validated.from_failure(1) == Invalid((1,))
    assert isinstance(Invalid((1,)).failure(), tuple)


def test_from_validated_returns_same_instance():
    """Ensures from_validated is identity."""
    container = Valid(1)
    failed = Invalid(('e',))
    assert Validated.from_validated(container) is container
    assert Validated.from_validated(failed) is failed


def test_from_result():
    """Ensures Result is converted into Validated."""
    assert Validated.from_result(Success(1)) == Valid(1)
    assert Validated.from_result(Failure('e')) == Invalid(('e',))


def test_swap():
    """Ensures swap wraps Valid values into a 1-tuple."""
    assert Valid(1).swap() == Invalid((1,))
    assert Invalid((1, 2)).swap() == Valid((1, 2))


def test_repr():
    """Ensures repr contains the inner value."""
    assert str(Valid(1)) == '<Valid: 1>'
    assert str(Invalid((1, 2))) == '<Invalid: (1, 2)>'


def test_do_notation():
    """Ensures do-notation works and short-circuits."""
    success = Validated.do(
        first + second
        for first in Valid(2)
        for second in Valid(3)
    )
    failed = Validated.do(
        first + second
        for first in Invalid(('a',))
        for second in Valid(3)
    )
    assert success == Valid(5)
    assert failed == Invalid(('a',))


def test_combine():
    """Ensures combine uses applicative accumulation."""
    both_valid = Validated.combine(Valid(1), Valid(2), add)
    both_invalid = Validated.combine(Invalid(('a',)), Invalid(('b',)), add)
    mixed = Validated.combine(Valid(1), Invalid(('b',)), add)

    assert both_valid == Valid(3)
    assert both_invalid == Invalid(('a', 'b'))
    assert mixed == Invalid(('b',))


def test_combine_n_success():
    """Ensures combine_n applies the function when all are valid."""
    valid_inputs = (Valid(1), Valid(2), Valid(3))
    success = Validated.combine_n(valid_inputs, _add3)
    assert success == Valid(6)
    assert Validated.combine_n((), _zero) == Valid(0)


def test_combine_n_accumulates_errors():
    """Ensures combine_n accumulates all errors."""
    first_invalid = Invalid(('a',))
    second_invalid = Invalid(('c', 'd'))
    mixed_inputs = (first_invalid, Valid(2), second_invalid)
    failed = Validated.combine_n(mixed_inputs, _add3)
    assert failed == Invalid(('a', 'c', 'd'))
