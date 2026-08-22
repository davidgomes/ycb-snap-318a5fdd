from returns.methods import cond
from returns.pointfree import bind_validated
from returns.validated import Invalid, Valid, Validated


def test_cond_validated():
    """Ensures cond dispatches Validated via from_failure."""
    assert cond(Validated, True, 'ok', 'err') == Valid('ok')  # noqa: FBT003
    assert cond(Validated, False, 'ok', 'err') == Invalid(('err',))  # noqa: FBT003


def test_bind_validated_pointfree():
    """Ensures pointfree bind_validated is exported and works."""

    def factory(arg: int) -> Validated[int, str]:
        return Valid(arg + 1)

    bound = bind_validated(factory)
    assert bound(Valid(1)) == Valid(2)
    assert bound(Invalid(('e',))) == Invalid(('e',))
