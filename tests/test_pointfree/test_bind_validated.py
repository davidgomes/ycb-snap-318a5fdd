from returns.pointfree import bind_validated
from returns.validated import Invalid, Valid, Validated


def _returns_validated(number: int) -> Validated[int, str]:
    return Valid(number + 1)


def test_bind_validated_success():
    """Ensures the pointfree helper binds a successful container."""
    assert bind_validated(_returns_validated)(Valid(1)) == Valid(2)


def test_bind_validated_failure():
    """Ensures the pointfree helper short-circuits on failure."""
    assert bind_validated(_returns_validated)(Invalid(('e',))) == Invalid((
        'e',
    ))
