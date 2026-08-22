from returns.iterables import Fold
from returns.validated import Invalid, Valid


def test_fold_collect_accumulates_errors():
    """Fold.collect accumulates Invalid errors through apply."""
    acc = Valid(())
    collected = Fold.collect([Valid(1), Valid(2)], acc)
    failed = Fold.collect(
        [Invalid(('a',)), Valid(1), Invalid(('b',))],
        acc,
    )
    assert collected == Valid((1, 2))
    assert failed == Invalid(('a', 'b'))
