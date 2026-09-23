import copy
import pickle  # noqa: S403
from operator import add

import pytest

from returns.iterables import Fold
from returns.primitives.exceptions import (
    ImmutableStateError,
    UnwrapFailedError,
)
from returns.result import Failure, Success
from returns.validated import Invalid, Valid, Validated

_FIRST_ERRORS = ('a',)
_SECOND_ERRORS = ('b', 'c')
_ALL_ERRORS = ('a', 'b', 'c')


def _join(*args: str) -> str:
    return ''.join(args)


def test_invalid_stores_tuple():
    """Ensures that ``Invalid`` always stores errors as a tuple."""
    generated = (error for error in _SECOND_ERRORS)

    assert Invalid(['a', 'b']).failure() == ('a', 'b')
    assert Invalid(_FIRST_ERRORS).failure() == _FIRST_ERRORS
    assert Invalid(generated).failure() == _SECOND_ERRORS


def test_from_failure_wraps_single_error():
    """Ensures that ``from_failure`` wraps a single error into a tuple."""
    assert Validated.from_failure('a') == Invalid(('a',))
    assert Validated.from_failure(_SECOND_ERRORS) == Invalid((_SECOND_ERRORS,))


def test_from_value():
    """Ensures that ``from_value`` creates ``Valid``."""
    assert Validated.from_value(1) == Valid(1)


def test_apply_accumulates_errors_in_order():
    """Ensures that ``apply`` concatenates self errors with other errors."""
    first = Invalid(_FIRST_ERRORS)
    second = Invalid(_SECOND_ERRORS)
    function = Valid(str)

    assert first.apply(second) == Invalid(_ALL_ERRORS)
    assert second.apply(first) == Invalid(('b', 'c', 'a'))
    assert first.apply(function) == first
    assert Valid(1).apply(first) == first
    assert Valid(1).apply(function) == Valid('1')


def test_bind_short_circuits():
    """Ensures that ``bind`` stops on the first failure."""
    first = Invalid(_FIRST_ERRORS)
    second = Invalid(_SECOND_ERRORS)

    def factory(number: int) -> Validated[int, str]:
        return second

    assert first.bind(factory) == first
    assert Valid(1).bind(factory) == second
    assert Valid(1).bind_validated(Valid) == Valid(1)
    assert first.bind_validated(Valid) == first


def test_map():
    """Ensures that ``map`` works only for ``Valid``."""
    assert Valid(1).map(str) == Valid('1')
    assert Invalid(_FIRST_ERRORS).map(str) == Invalid(_FIRST_ERRORS)


def test_alt_maps_each_error():
    """Ensures that ``alt`` is applied to each error."""
    errors = ('a', 'bb')

    assert Invalid(errors).alt(len) == Invalid((1, 2))
    assert Valid('a').alt(len) == Valid('a')


def test_lash():
    """Ensures that ``lash`` receives all errors."""

    def factory(errors: tuple[str, ...]) -> Validated[int, str]:
        return Valid(len(errors))

    assert Invalid(_SECOND_ERRORS).lash(factory) == Valid(2)
    assert Valid(1).lash(factory) == Valid(1)


def test_swap():
    """Ensures that ``swap`` wraps values into a tuple of errors."""
    assert Valid(1).swap() == Invalid((1,))
    assert Invalid(_SECOND_ERRORS).swap() == Valid(_SECOND_ERRORS)
    assert Valid(1).swap().swap() == Valid((1,))


def test_unwrap_and_failure():
    """Ensures that ``unwrap`` and ``failure`` work correctly."""
    invalid = Invalid(_FIRST_ERRORS)

    assert Valid(1).unwrap() == 1
    assert invalid.failure() == _FIRST_ERRORS

    with pytest.raises(UnwrapFailedError) as exc_info:
        invalid.unwrap()
    assert exc_info.value.halted_container == invalid

    with pytest.raises(UnwrapFailedError):
        Valid(1).failure()


def test_value_or():
    """Ensures that ``value_or`` works correctly."""
    assert Valid(1).value_or(2) == 1
    assert Invalid(_FIRST_ERRORS).value_or(2) == 2


def test_from_result():
    """Ensures that ``from_result`` converts ``Result`` to ``Validated``."""
    assert Validated.from_result(Success(1)) == Valid(1)
    assert Validated.from_result(Failure('a')) == Invalid(('a',))


def test_from_validated_returns_same_instance():
    """Ensures that ``from_validated`` returns the same instance."""
    valid = Valid(1)
    invalid = Invalid(_FIRST_ERRORS)

    assert Validated.from_validated(valid) is valid
    assert Validated.from_validated(invalid) is invalid


def test_combine():
    """Ensures that ``combine`` accumulates errors from both containers."""
    first = Invalid(_FIRST_ERRORS)
    second = Invalid(_SECOND_ERRORS)
    one = Valid(1)
    two = Valid(2)

    assert Validated.combine(one, two, add) == Valid(3)
    assert Validated.combine(first, two, add) == first
    assert Validated.combine(one, second, add) == second
    assert Validated.combine(first, second, add) == Invalid(_ALL_ERRORS)


def test_combine_argument_order():
    """Ensures that ``combine`` passes values in the given order."""
    first = Valid('a')
    second = Valid('b')

    assert Validated.combine(first, second, add) == Valid('ab')


def test_combine_n():
    """Ensures that ``combine_n`` accumulates errors from all containers."""
    valid_items = (Valid('a'), Valid('b'), Valid('c'))
    mixed_items = (
        Invalid(_FIRST_ERRORS),
        Valid('d'),
        Invalid(_SECOND_ERRORS),
    )

    assert Validated.combine_n(valid_items, _join) == Valid('abc')
    assert Validated.combine_n(mixed_items, _join) == Invalid(_ALL_ERRORS)
    assert Validated.combine_n((), _join) == Valid('')


def test_fold_collect_accumulates_errors():
    """Ensures that ``Fold.collect`` accumulates errors through ``apply``."""
    valid_items = [Valid(1), Valid(2)]
    mixed_items = [Invalid(_FIRST_ERRORS), Valid(2), Invalid(_SECOND_ERRORS)]

    assert Fold.collect(valid_items, Valid(())) == Valid((1, 2))
    assert Fold.collect(mixed_items, Valid(())) == Invalid(_ALL_ERRORS)


def test_do_notation():
    """Ensures that do-notation short-circuits on failures."""
    first: Validated[int, str] = Invalid(_FIRST_ERRORS)
    second: Validated[int, str] = Invalid(_SECOND_ERRORS)

    assert Validated.do(
        left + right for left in Valid(1) for right in Valid(2)
    ) == Valid(3)
    assert Validated.do(
        left + right for left in first for right in second
    ) == Invalid(_FIRST_ERRORS)


def test_equality_and_repr():
    """Ensures that equality, hash, and repr work correctly."""
    invalid = Invalid((1, 2))

    assert Valid(1) == Valid(1)
    assert Valid(1) != Invalid((1,))
    assert invalid != Failure((1, 2))
    assert Valid(1).equals(Valid(1))
    assert hash(invalid) == hash((1, 2))
    assert repr(Valid(1)) == '<Valid: 1>'
    assert repr(invalid) == '<Invalid: (1, 2)>'


def test_immutability():
    """Ensures that containers are immutable."""
    invalid = Invalid(_FIRST_ERRORS)

    with pytest.raises(ImmutableStateError):
        invalid._inner_value = _SECOND_ERRORS  # noqa: SLF001

    pickled = pickle.dumps(invalid)
    assert copy.copy(invalid) == invalid
    assert pickle.loads(pickled) == invalid  # noqa: S301


@pytest.mark.parametrize(
    'container',
    [
        Valid(10),
        Valid(42),
        Invalid(('a', 'b')),
        Invalid((ValueError(),)),
    ],
)
def test_validated_pattern_matching(container: Validated[int, object]):
    """Ensures ``Validated`` containers work properly with pattern matching."""
    match container:
        case Valid(10):
            assert container.unwrap() == 10
        case Valid(number):
            assert number == 42
        case Invalid(('a', 'b')):
            assert container.failure() == ('a', 'b')
        case Invalid((ValueError(),)):
            assert isinstance(container.failure()[0], ValueError)
        case _:
            pytest.fail('Was not matched')
