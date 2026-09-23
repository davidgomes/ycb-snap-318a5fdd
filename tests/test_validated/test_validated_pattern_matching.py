import pytest

from returns.validated import Invalid, Valid, Validated

_UNMATCHED = 'Was not matched'


@pytest.mark.parametrize(
    'container',
    [
        Valid(10),
        Valid(42),
        Invalid((ZeroDivisionError(),)),
        Invalid(('left', 'right')),
    ],
)
def test_validated_pattern_matching(container: Validated[int, object]):
    """Ensures ``Validated`` containers work with pattern matching."""
    match container:
        case Valid(10):
            assert isinstance(container, Valid)
            assert container.unwrap() == 10
        case Valid(number):
            assert isinstance(container, Valid)
            assert number == 42
            assert container.unwrap() == number
        case Invalid((ZeroDivisionError(),)):
            assert isinstance(container, Invalid)
            assert isinstance(container.failure()[0], ZeroDivisionError)
        case Invalid(errors):
            assert errors == ('left', 'right')
            assert container.failure() == errors
        case _:
            pytest.fail(_UNMATCHED)
