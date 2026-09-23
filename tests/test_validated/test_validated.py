import operator

import pytest

from returns.iterables import Fold
from returns.methods import cond, unwrap_or_failure
from returns.pointfree import bind_validated
from returns.primitives.exceptions import UnwrapFailedError
from returns.result import Failure, Success
from returns.validated import Invalid, Valid, Validated


def _double(inner_value: int) -> Validated[int, str]:
    """Return the doubled value as ``Valid``."""
    return Valid(inner_value * 2)


def _forbidden(inner_value: int) -> Validated[int, str]:
    """Fail the test when a short-circuit calls this callback."""
    raise AssertionError(inner_value)


def _suffix(text: str) -> str:
    """Append an exclamation mark."""
    return f'{text}!'


def _recover(errors: tuple[str, ...]) -> Validated[str, str]:
    """Join accumulated errors into one success value."""
    return Valid('-'.join(errors))


def _add_three(first: int, second: int, third: int) -> int:
    """Add three integers."""
    return first + second + third


def _returns_validated(arg: int) -> Validated[int, str]:
    """Increment a number and wrap it into ``Valid``."""
    return Valid(arg + 1)


def test_map_and_bind_short_circuit():
    """Ensures map and bind do not run on failures."""
    mapped = Valid(2).map(lambda number: number + 1)
    assert mapped == Valid(3)
    assert Valid(2).bind(_double) == Valid(4)
    assert Valid(2).bind_validated(_double) == Valid(4)

    failed = Invalid(('kept',))
    assert failed.map(lambda number: number + 1) is failed
    assert failed.bind(_forbidden) is failed
    assert failed.bind_validated(_forbidden) is failed


def test_apply_accumulates_errors_left_to_right():
    """Ensures apply keeps successes and concatenates failures."""
    assert Valid('a').apply(Valid(_suffix)) == Valid('a!')

    failed = Invalid(('a', 'b'))
    assert failed.apply(Valid(_suffix)) == failed

    invalid_function = Invalid((1, 2))
    assert Valid('a').apply(invalid_function) == invalid_function

    left = Invalid((1,))
    right = Invalid((2, 3))
    assert left.apply(right) == Invalid((1, 2, 3))

    merged = Invalid(('b', 'c')).apply(Invalid(('a',)))
    assert merged == Invalid(('b', 'c', 'a'))


def test_alt_maps_each_error():
    """Ensures alt transforms every error element and ignores successes."""
    assert Valid('a').alt(str.upper) == Valid('a')

    mapped = Invalid(('a', 'bb')).alt(len)
    assert mapped == Invalid((1, 2))


def test_lash_receives_error_tuple():
    """Ensures lash passes the whole error tuple to the callback."""
    assert Valid('ok').lash(_recover) == Valid('ok')
    assert Invalid(('a', 'b')).lash(_recover) == Valid('a-b')


def test_swap_wraps_success_and_unwraps_errors():
    """Ensures swap matches the tuple-wrapping contract."""
    assert Valid(1).swap() == Invalid((1,))
    assert Invalid((1, 2)).swap() == Valid((1, 2))
    assert Valid(1).swap().swap() == Valid((1,))
    assert Valid(1).swap().swap() != Valid(1)


def test_from_failure_wraps_single_error():
    """Ensures a single failure is stored as a one-element tuple."""
    assert Validated.from_failure('err') == Invalid(('err',))
    assert Validated.from_failure(('err',)) == Invalid((('err',),))
    assert isinstance(Invalid(('a', 'b')).failure(), tuple)
    assert Invalid(('a', 'b')).failure() == ('a', 'b')


def test_from_validated_returns_same_instance():
    """Ensures ``from_validated`` does not copy its argument."""
    valid = Valid(1)
    invalid = Invalid(('err',))
    assert Validated.from_validated(valid) is valid
    assert Validated.from_validated(invalid) is invalid


def test_from_result_wraps_failure_error():
    """Ensures ``Result`` converts with a one-element error tuple."""
    assert Validated.from_result(Success(1)) == Valid(1)
    assert Validated.from_result(Failure('err')) == Invalid(('err',))


def test_value_or_unwrap_and_failure():
    """Ensures unwrapping helpers follow success and failure states."""
    assert Valid(1).value_or(2) == 1
    assert Invalid((1,)).value_or(2) == 2
    assert Valid(1).unwrap() == 1
    assert unwrap_or_failure(Valid(1)) == 1
    assert unwrap_or_failure(Invalid(('a', 'b'))) == ('a', 'b')

    with pytest.raises(UnwrapFailedError):
        Invalid((1,)).unwrap()
    with pytest.raises(UnwrapFailedError):
        Valid(1).failure()


def test_equality_and_repr():
    """Ensures containers compare by type and inner value."""
    assert Valid(1) == Valid(1)
    assert Valid(1) != Invalid((1,))
    assert Invalid((1, 2)) == Invalid((1, 2))
    assert Invalid((1,)) != Invalid((1, 2))
    assert str(Valid(1)) == '<Valid: 1>'
    assert str(Invalid((1, 2))) == '<Invalid: (1, 2)>'
    assert hash(Valid(1)) == hash(1)


def test_do_notation_short_circuits():
    """Ensures do-notation stops at the first failure."""
    summed: Validated[int, str] = Validated.do(
        first + second for first in Valid(2) for second in Valid(3)
    )
    assert summed == Valid(5)

    halted = Validated.do(
        first + second
        for first in Invalid(('a',))
        for second in Invalid(('b',))
    )
    assert halted == Invalid(('a',))


def test_combine_accumulates_both_sides():
    """Ensures binary combine keeps every independent error."""
    added = Validated.combine(Valid(2), Valid(3), operator.add)
    assert added == Valid(5)

    both_failed = Validated.combine(
        Invalid(('a',)),
        Invalid(('b',)),
        operator.add,
    )
    assert both_failed == Invalid(('a', 'b'))

    right_failed = Validated.combine(
        Valid(1),
        Invalid(('b',)),
        operator.add,
    )
    assert right_failed == Invalid(('b',))

    left_failed = Validated.combine(
        Invalid(('a',)),
        Valid(1),
        operator.add,
    )
    assert left_failed == Invalid(('a',))


def test_combine_n_accumulates_every_error():
    """Ensures N-ary combine runs only when every value is valid."""
    total = Validated.combine_n(
        (Valid(1), Valid(2), Valid(3)),
        _add_three,
    )
    assert total == Valid(6)

    partial = Validated.combine_n(
        (Invalid(('a',)), Valid(2), Invalid(('b', 'c'))),
        _add_three,
    )
    assert partial == Invalid(('a', 'b', 'c'))
    assert Validated.combine_n((), lambda: 'empty') == Valid('empty')

    incremented = Validated.combine_n(
        (Valid(1),),
        lambda number: number + 1,
    )
    assert incremented == Valid(2)


def test_fold_collect_accumulates_errors():
    """Ensures iterable collection reuses apply and keeps error order."""
    collected = Fold.collect(
        [Valid(1), Valid(2), Valid(3)],
        Valid(()),
    )
    assert collected == Valid((1, 2, 3))

    containers = [
        Valid(1),
        Invalid(('a',)),
        Valid(3),
        Invalid(('b',)),
    ]
    failed = Fold.collect(containers, Valid(()))
    assert failed == Invalid(('a', 'b'))


def test_cond_and_pointfree_bind():
    """Ensures cond and pointfree bind dispatch to Validated."""
    success = cond(
        Validated,
        is_success=True,
        success_value='ok',
        error_value='bad',
    )
    assert success == Valid('ok')

    failure = cond(
        Validated,
        is_success=False,
        success_value='ok',
        error_value='bad',
    )
    assert failure == Invalid(('bad',))
    assert bind_validated(_returns_validated)(Valid(1)) == Valid(2)

    bound = bind_validated(_returns_validated)(Invalid(('err',)))
    assert bound == Invalid(('err',))
