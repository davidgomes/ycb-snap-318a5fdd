import operator

import pytest

from returns.interfaces.failable import DiverseFailableN
from returns.interfaces.specific.validated import ValidatedLikeN
from returns.interfaces.swappable import SwappableN
from returns.iterables import Fold
from returns.primitives.exceptions import UnwrapFailedError
from returns.result import Failure, Success
from returns.validated import Invalid, Valid, Validated, validated


def _increment(number: int) -> int:
    return number + 1


def _explode(number: int) -> Validated[int, str]:
    raise AssertionError(number)


def _append_error(number: int) -> Validated[int, str]:
    return Invalid(('x',))


def _keep_errors(errors: tuple[str, ...]) -> Valid[str]:
    return Valid(errors[0])


def _join_errors(errors: tuple[str, ...]) -> Valid[str]:
    return Valid('-'.join(errors))


@validated(exceptions=(ValueError,))
def _calculate_filtered(number: int) -> int:
    if number == 0:
        raise ValueError(number)
    if number < 0:
        raise TypeError(number)
    return number + 1


@validated
def _calculate_default(number: int) -> int:
    return number // number


def test_matches_container_interface():
    """``Validated`` participates in its own failable hierarchy."""
    assert issubclass(Validated, ValidatedLikeN)
    assert not issubclass(Validated, DiverseFailableN)
    assert not issubclass(Validated, SwappableN)


def test_pattern_matching_and_repr():
    """Structural matches expose the inner value and errors."""
    assert Valid.__match_args__ == ('_inner_value',)
    assert Invalid.__match_args__ == ('_inner_value',)
    assert str(Valid(1)) == '<Valid: 1>'
    assert str(Invalid((1, 2))) == '<Invalid: (1, 2)>'

    match Valid(1):
        case Valid(1):
            matched_value = True
        case _:
            matched_value = False
    match Invalid((1, 2)):
        case Invalid((1, second)):
            matched_error = second
        case Invalid(errors):
            matched_error = errors[0]

    assert matched_value is True
    assert matched_error == 2


def test_equality_and_swap_order():
    """Equality is structural and swap wraps successful values."""
    assert Valid(1) == Valid(1)
    assert Valid(1) != Invalid((1,))
    assert Invalid((1, 2)) == Invalid((1, 2))
    assert Invalid((1,)) != Invalid((1, 2))
    swapped_twice = Valid(1).swap().swap()
    assert swapped_twice == Valid((1,))
    assert swapped_twice != Valid(1)
    assert hash(Valid(1)) == hash(Valid(1))


def test_apply_accumulates_left_to_right():
    """Two failures concatenate this container's errors, then the other's."""
    both_failed = Invalid((1, 2)).apply(Invalid((3,)))
    assert both_failed == Invalid((1, 2, 3))

    failed_value = Invalid((1,)).apply(Valid(_increment))
    assert failed_value == Invalid((1,))

    failed_function = Valid(1).apply(Invalid(('e',)))
    assert failed_function == Invalid(('e',))

    both_valid = Valid(1).apply(Valid(_increment))
    assert both_valid == Valid(2)


def test_bind_short_circuits_without_calling():
    """Bind does not run the function, and does not append its errors."""
    assert Invalid(('kept',)).bind(_explode) == Invalid(('kept',))
    bound = Valid(1).bind(_append_error)
    assert bound == Invalid(('x',))


def test_alt_maps_each_error():
    """Alt walks error elements and leaves successes alone."""
    mapped = Invalid((1, 2)).alt(_increment)
    assert mapped == Invalid((2, 3))
    assert Invalid(()).alt(_increment) == Invalid(())
    assert Valid(1).alt(_increment) == Valid(1)


def test_lash_receives_error_tuple():
    """Lash sees the whole tuple and ignores successes."""
    assert Valid('ok').lash(_keep_errors) == Valid('ok')  # type: ignore[arg-type]
    lashed = Invalid(('a', 'b')).lash(_join_errors)
    assert lashed == Valid('a-b')


def test_unwrap_and_failure_branches():
    """Unwrap chains a single exception and failure rejects successes."""
    assert Valid(1).unwrap() == 1
    assert Invalid((1, 2)).failure() == (1, 2)
    assert Valid(1).value_or(0) == 1
    assert Invalid((1,)).value_or(0) == 0

    with pytest.raises(UnwrapFailedError):
        Valid(1).failure()

    with pytest.raises(UnwrapFailedError):
        Invalid(('e', 'f')).unwrap()

    with pytest.raises(UnwrapFailedError):
        Invalid(('e',)).unwrap()

    with pytest.raises(UnwrapFailedError) as exc_info:
        Invalid((ValueError('boom'),)).unwrap()
    assert isinstance(exc_info.value.__cause__, ValueError)


def test_from_helpers_keep_identity():
    """Unit helpers wrap one error and reuse validated instances."""
    valid = Valid(1)
    invalid = Invalid(('e',))
    assert Validated.from_validated(valid) is valid
    assert Validated.from_validated(invalid) is invalid
    assert Validated.from_failure('e').failure() == ('e',)
    assert Validated.from_result(Success(1)) == Valid(1)
    assert Validated.from_result(Failure('e')) == Invalid(('e',))


def test_do_notation_short_circuits():
    """Do-notation stops on the first failure."""
    succeeded: Validated[int, str] = Validated.do(
        first + second for first in Valid(2) for second in Valid(3)
    )
    assert succeeded == Valid(5)
    failed = Validated.do(
        first + second
        for first in Invalid(('a',))
        for second in Valid(3)
    )
    assert failed == Invalid(('a',))


def test_combine_and_fold_collect_errors_in_order():
    """Applicative helpers and ``Fold.collect`` share error order."""
    combined = Validated.combine(
        Invalid(('a',)),
        Invalid(('b',)),
        operator.add,
    )
    assert combined == Invalid(('a', 'b'))

    collected = Fold.collect(
        [Invalid(('a',)), Valid(1), Invalid(('b',))],
        Valid(()),
    )
    assert collected == Invalid(('a', 'b'))

    collected_values = Fold.collect(
        [Valid(1), Valid(2)],
        Valid(()),
    )
    assert collected_values == Valid((1, 2))

    partial_success = Fold.collect_all(
        [Valid(1), Invalid(('a',)), Valid(2)],
        Valid(()),
    )
    assert partial_success == Valid((1, 2))


def test_decorator_exception_filter():
    """Only listed exception types become ``Invalid``."""
    assert _calculate_filtered.__name__ == '_calculate_filtered'
    assert _calculate_filtered(1) == Valid(2)
    failure = _calculate_filtered(0)
    assert isinstance(failure, Invalid)
    assert isinstance(failure.failure()[0], ValueError)
    with pytest.raises(TypeError):
        _calculate_filtered(-1)


def test_decorator_defaults_to_exception():
    """Calling the decorator on a function catches ``Exception``."""
    assert _calculate_default.__name__ == '_calculate_default'
    assert _calculate_default(1) == Valid(1)
    failure = _calculate_default(0)
    assert isinstance(failure, Invalid)
    assert isinstance(failure.failure()[0], ZeroDivisionError)
