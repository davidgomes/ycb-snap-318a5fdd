from returns.methods import cond
from returns.pointfree import cond as pointfree_cond
from returns.validated import Invalid, Valid, Validated


def test_cond_validated_success():
    """Ensures cond builds ``Valid`` when the predicate holds."""
    assert cond(
        Validated,
        is_success=True,
        success_value='ok',
        error_value='err',
    ) == Valid('ok')


def test_cond_validated_failure():
    """Ensures cond wraps the error when the predicate fails."""
    assert cond(
        Validated,
        is_success=False,
        success_value='ok',
        error_value='err',
    ) == Invalid(('err',))


def test_pointfree_cond_validated():
    """Ensures the pointfree cond helper supports ``Validated``."""
    choose = pointfree_cond(Validated, 'ok', 'err')
    assert choose(True) == Valid('ok')  # noqa: FBT003
    assert choose(False) == Invalid(('err',))  # noqa: FBT003
