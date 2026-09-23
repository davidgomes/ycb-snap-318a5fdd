from copy import copy, deepcopy

import pytest

from returns.pointfree import bind_validated
from returns.primitives.exceptions import (
    ImmutableStateError,
    UnwrapFailedError,
)
from returns.result import Failure, Success
from returns.validated import Invalid, Valid, Validated


def _increment(number: int) -> Validated[int, str]:
    return Valid(number + 1)


def _fail(number: int) -> Validated[int, str]:
    return Invalid(('a',))


def _count_errors(errors: tuple[str, ...]) -> Validated[int, str]:
    return Valid(len(errors))


def _drop_first_error(errors: tuple[str, ...]) -> Validated[int, str]:
    return Invalid(errors[1:])


def test_invalid_stores_tuple():
    """Ensures that ``Invalid`` always stores errors as a tuple."""
    errors = ['a', 'b']

    assert Invalid(('a', 'b')).failure() == ('a', 'b')
    assert Invalid(errors).failure() == ('a', 'b')  # type: ignore[arg-type]
    assert Invalid(errors) == Invalid(('a', 'b'))  # type: ignore[arg-type]


def test_from_failure_wraps_single_error():
    """Ensures that ``from_failure`` wraps an error into a 1-tuple."""
    errors = ('a', 'b')

    assert Validated.from_failure('a') == Invalid(('a',))
    assert Validated.from_failure('a').failure() == ('a',)
    assert Validated.from_failure(errors) == Invalid((errors,))


def test_from_value():
    """Ensures that ``from_value`` creates ``Valid``."""
    assert Validated.from_value(1) == Valid(1)


def test_equality():
    """Ensures that containers are compared by type and inner value."""
    assert Valid(1) == Valid(1)
    assert Valid(1).equals(Valid(1))
    assert Invalid((1,)) == Invalid((1,))
    assert Invalid((1,)).equals(Invalid((1,)))


def test_non_equality():
    """Ensures that different containers are not equal."""
    assert Valid(1) != Valid(2)
    assert Valid((1,)) != Invalid((1,))
    assert Invalid((1,)) != Invalid((1, 1))
    assert Valid(1) != Success(1)
    assert Invalid((1,)) != Failure((1,))
    assert Valid(1) != 1


def test_hash_and_repr():
    """Ensures that containers are hashable and have readable repr."""
    assert hash(Valid(1)) == hash(Valid(1))
    assert hash(Invalid(('a',))) == hash(Invalid(('a',)))
    assert repr(Valid(1)) == '<Valid: 1>'
    assert repr(Invalid(('a', 'b'))) == "<Invalid: ('a', 'b')>"


@pytest.mark.parametrize('container', [Valid(1), Invalid((1,))])
def test_immutability(container: Validated[int, int]):
    """Ensures that containers are immutable."""
    with pytest.raises(ImmutableStateError):
        container._inner_value = 2  # noqa: SLF001

    with pytest.raises(ImmutableStateError):
        container.missing = 2

    assert container is copy(container)
    assert container is deepcopy(container)


def test_swap():
    """Ensures that ``swap`` wraps values and unwraps errors."""
    assert Valid(1).swap() == Invalid((1,))
    assert Invalid((1, 2)).swap() == Valid((1, 2))
    assert Valid(1).swap().swap() == Valid((1,))


def test_from_validated():
    """Ensures that ``from_validated`` returns the same instance."""
    valid = Valid(1)
    invalid = Invalid(('a',))

    assert Validated.from_validated(valid) is valid
    assert Validated.from_validated(invalid) is invalid


def test_from_result():
    """Ensures that ``from_result`` converts ``Result`` to ``Validated``."""
    errors = ('a',)

    assert Validated.from_result(Success(1)) == Valid(1)
    assert Validated.from_result(Failure('a')) == Invalid(('a',))
    assert Validated.from_result(Failure(errors)) == Invalid((errors,))


def test_map():
    """Ensures that ``map`` only works for ``Valid``."""
    invalid = Invalid(('a',))

    assert Valid(1).map(str) == Valid('1')
    assert invalid.map(str) is invalid


def test_alt():
    """Ensures that ``alt`` maps every single error."""
    valid = Valid(1)
    invalid = Invalid(('a', 'bc'))

    assert invalid.alt(len) == Invalid((1, 2))
    assert invalid.alt(str.upper).failure() == ('A', 'BC')
    assert valid.alt(len) is valid


def test_lash():
    """Ensures that ``lash`` receives all errors at once."""
    valid: Validated[int, str] = Valid(1)
    invalid: Validated[int, str] = Invalid(('a', 'b'))

    assert invalid.lash(_count_errors) == Valid(2)
    assert invalid.lash(_drop_first_error) == Invalid(('b',))
    assert valid.lash(_count_errors) is valid


def test_bind_short_circuits():
    """Ensures that ``bind`` does not accumulate errors."""
    invalid: Validated[int, str] = Invalid(('b',))

    assert Valid(1).bind(_increment) == Valid(2)
    assert Valid(1).bind(_fail) == Invalid(('a',))
    assert invalid.bind(_fail) is invalid


def test_bind_validated():
    """Ensures that ``bind_validated`` works as ``bind``."""
    invalid: Validated[int, str] = Invalid(('b',))

    assert Valid(1).bind_validated(_increment) == Valid(2)
    assert Valid(1).bind_validated(_fail) == Invalid(('a',))
    assert invalid.bind_validated(_fail) is invalid


def test_pointfree_bind_validated():
    """Ensures that pointfree ``bind_validated`` works."""
    bound = bind_validated(_increment)

    assert bound(Valid(1)) == Valid(2)
    assert bound(Invalid(('a',))) == Invalid(('a',))


def test_unwrap_and_failure():
    """Ensures that unwrapping works correctly."""
    assert Valid(1).unwrap() == 1
    assert Invalid(('a',)).failure() == ('a',)

    with pytest.raises(UnwrapFailedError):
        Invalid(('a',)).unwrap()

    with pytest.raises(UnwrapFailedError):
        Valid(1).failure()


def test_value_or():
    """Ensures that ``value_or`` works correctly."""
    assert Valid(1).value_or(2) == 1
    assert Invalid(('a',)).value_or(2) == 2


def test_do_notation():
    """Ensures that do-notation short-circuits on the first failure."""
    assert Validated.do(
        first + second
        for first in Valid(1)
        for second in Valid(2)
    ) == Valid(3)

    assert Validated.do(
        first + second
        for first in Invalid(('a',))
        for second in Invalid(('b',))
    ) == Invalid(('a',))
