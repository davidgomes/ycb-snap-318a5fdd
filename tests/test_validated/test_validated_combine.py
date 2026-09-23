from operator import add

from returns.validated import Invalid, Valid, Validated


def test_combine_success():
    """Ensures combine applies a binary function to two successes."""
    assert Validated.combine(
        Valid(1),
        Valid(2),
        add,
    ) == Valid(3)


def test_combine_accumulates_errors_left_to_right():
    """Ensures combine keeps the left failure before the right one."""
    assert Validated.combine(
        Invalid(('a',)),
        Invalid(('b', 'c')),
        add,
    ) == Invalid(('a', 'b', 'c'))


def test_combine_partial_failure():
    """Ensures a single failure is preserved from either side."""
    assert Validated.combine(
        Invalid(('a',)),
        Valid(2),
        lambda left, right: left,
    ) == Invalid(('a',))
    assert Validated.combine(
        Valid(1),
        Invalid(('b',)),
        lambda left, right: left,
    ) == Invalid(('b',))


def test_combine_n_success():
    """Ensures combine_n applies an n-ary function."""
    assert Validated.combine_n(
        (Valid(1), Valid(2), Valid(3)),
        lambda first, second, third: first + second + third,
    ) == Valid(6)


def test_combine_n_accumulates_all_errors():
    """Ensures combine_n keeps every failure, in order."""
    assert Validated.combine_n(
        (Invalid(('a',)), Valid(2), Invalid(('b', 'c'))),
        lambda first, second, third: first + second + third,
    ) == Invalid(('a', 'b', 'c'))


def test_combine_n_single_and_empty():
    """Ensures combine_n works for one container and for no containers."""
    assert Validated.combine_n(
        (Valid(1),),
        lambda number: number + 1,
    ) == Valid(2)
    assert Validated.combine_n((), lambda: 'empty') == Valid('empty')
    assert Validated.combine_n(
        (Invalid(('only',)),),
        lambda number: number,
    ) == Invalid(('only',))
