from returns.iterables import Fold
from returns.validated import Invalid, Valid


def test_apply_success():
    """Ensures apply calls a successful function."""
    assert Valid(1).apply(Valid(lambda number: number + 1)) == Valid(2)


def test_apply_failure_with_success_function():
    """Ensures a failed value ignores a successful function."""
    assert Invalid(('a',)).apply(
        Valid(lambda number: number + 1),
    ) == Invalid(('a',))


def test_apply_success_with_failed_function():
    """Ensures a failed function is returned unchanged."""
    failed = Invalid(('b', 'c'))

    assert Valid(1).apply(failed) is failed


def test_apply_concatenates_errors_left_to_right():
    """Ensures two failures keep self's errors before the other's."""
    assert Invalid(('a', 'b')).apply(
        Invalid(('c',)),
    ) == Invalid(('a', 'b', 'c'))


def test_alt_maps_each_error():
    """Ensures alt transforms every error element and ignores success."""
    assert Valid(1).alt(lambda error: error + '!') == Valid(1)
    assert Invalid(('a', 'b')).alt(
        lambda error: error + '!',
    ) == Invalid(('a!', 'b!'))


def test_swap():
    """Ensures swap wraps a success and unwraps a failure tuple."""
    assert Valid('a').swap() == Invalid(('a',))
    assert Invalid(('a', 'b')).swap() == Valid(('a', 'b'))
    assert Valid(1).swap().swap() == Valid((1,))


def test_fold_collect_accumulates_errors():
    """Ensures ``Fold.collect`` keeps every independent failure."""
    collected = Fold.collect(
        [Invalid(('a',)), Valid(1), Invalid(('b', 'c'))],
        Valid(()),
    )

    assert collected == Invalid(('a', 'b', 'c'))


def test_fold_collect_success():
    """Ensures ``Fold.collect`` still gathers successful values."""
    assert Fold.collect(
        [Valid(1), Valid(2), Valid(3)],
        Valid(()),
    ) == Valid((1, 2, 3))
