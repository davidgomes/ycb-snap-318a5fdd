import pytest

from returns.primitives.exceptions import UnwrapFailedError
from returns.validated import Invalid, Valid, Validated


def test_bind_success():
    """Ensures that bind runs the function for ``Valid``."""
    assert Valid(1).bind(lambda number: Valid(number + 1)) == Valid(2)


def test_bind_success_to_failure():
    """Ensures that bind can still fail when the function fails."""
    assert Valid(1).bind(lambda number: Invalid(('e',))) == Invalid(('e',))


def test_bind_short_circuits():
    """Ensures that bind does not run the function for ``Invalid``."""

    def _explode(number: int) -> Validated[int, str]:
        raise AssertionError(number)

    assert Invalid(('a', 'b')).bind(_explode) == Invalid(('a', 'b'))
    assert Invalid(('a',)).bind_validated(_explode) == Invalid(('a',))


def test_bind_validated_alias():
    """Ensures that ``bind_validated`` matches ``bind`` on success."""
    bound = Valid(1).bind_validated(lambda number: Valid(number + 1))

    assert bound == Valid(2)


def test_map_and_value_or():
    """Ensures map and value_or follow success and failure separately."""
    assert Valid(1).map(lambda number: number + 1) == Valid(2)
    assert Invalid(('a',)).map(lambda number: number + 1) == Invalid(('a',))
    assert Valid(1).value_or(0) == 1
    assert Invalid(('a',)).value_or(0) == 0


def test_lash():
    """Ensures lash receives the whole error tuple and ignores success."""
    assert Valid(1).lash(lambda errors: Valid(len(errors))) == Valid(1)
    assert Invalid(('a', 'b')).lash(
        lambda errors: Valid(len(errors)),
    ) == Valid(2)


def test_do_notation():
    """Ensures do-notation returns the success or the first failure."""
    assert Validated.do(
        first + second for first in Valid(2) for second in Valid(3)
    ) == Valid(5)
    assert Validated.do(
        first + second for first in Invalid(('a',)) for second in Valid(3)
    ) == Invalid(('a',))


def test_iter_success():
    """Ensures successful containers can be iterated."""
    assert list(Valid(1)) == [1]


def test_iter_failure():
    """Ensures iterating a failure raises the halted container."""
    with pytest.raises(UnwrapFailedError):
        list(Invalid(('a',)))
