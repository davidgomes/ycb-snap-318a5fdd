from operator import add

import pytest

from returns.iterables import Fold
from returns.validated import Invalid, Valid, Validated

_first_error = Invalid(('a',))
_other_errors = Invalid(('b', 'c'))
_all_valid = (Valid(1), Valid(2), Valid(3))


def _collect(*args: int) -> tuple[int, ...]:
    return args


@pytest.mark.parametrize(
    ('container', 'function', 'expected'),
    [
        (Valid(1), Valid(str), Valid('1')),
        (Valid(1), _first_error, _first_error),
        (_first_error, Valid(str), _first_error),
        (_first_error, _other_errors, Invalid(('a', 'b', 'c'))),
        (_other_errors, _first_error, Invalid(('b', 'c', 'a'))),
    ],
)
def test_apply(container, function, expected):
    """Ensures that ``apply`` accumulates errors, self errors go first."""
    assert container.apply(function) == expected


def test_fold_collect_accumulates_errors():
    """Ensures that ``Fold.collect`` collects all errors in order."""
    acc: Validated[tuple[int, ...], str] = Valid(())
    all_valid = [Valid(1), Valid(2)]
    has_invalid = [_first_error, Valid(2), _other_errors]

    assert Fold.collect([], acc) == Valid(())
    assert Fold.collect(all_valid, acc) == Valid((1, 2))
    assert Fold.collect(has_invalid, acc) == Invalid(('a', 'b', 'c'))


@pytest.mark.parametrize(
    ('first', 'second', 'expected'),
    [
        (Valid(1), Valid(2), Valid(3)),
        (_first_error, Valid(2), _first_error),
        (Valid(1), _other_errors, _other_errors),
        (_first_error, _other_errors, Invalid(('a', 'b', 'c'))),
    ],
)
def test_combine(first, second, expected):
    """Ensures that ``combine`` accumulates errors in order."""
    assert Validated.combine(first, second, add) == expected


def test_combine_argument_order():
    """Ensures that ``combine`` passes values in order."""
    combined = Validated.combine(Valid(1), Valid(2), _collect)

    assert combined == Valid((1, 2))


@pytest.mark.parametrize(
    ('containers', 'expected'),
    [
        ((), Valid(())),
        ((Valid(1),), Valid((1,))),
        (_all_valid, Valid((1, 2, 3))),
        ((Valid(1), _first_error, Valid(3)), _first_error),
        (
            (_first_error, Valid(2), _other_errors, Invalid(('d',))),
            Invalid(('a', 'b', 'c', 'd')),
        ),
    ],
)
def test_combine_n(containers, expected):
    """Ensures that ``combine_n`` accumulates all errors in order."""
    assert Validated.combine_n(containers, _collect) == expected


def test_combine_n_skips_function_on_failure():
    """Ensures that ``combine_n`` does not call function for failures."""
    calls: list[tuple[int, ...]] = []

    def factory(*args: int) -> int:
        calls.append(args)
        return len(args)

    with_errors = (Valid(1), _first_error)
    without_errors = (Valid(1), Valid(2))

    assert Validated.combine_n(with_errors, factory) == _first_error
    assert not calls
    assert Validated.combine_n(without_errors, factory) == Valid(2)
    assert calls == [(1, 2)]
