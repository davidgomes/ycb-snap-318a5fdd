import pytest

from returns.validated import Invalid, Valid, Validated


@pytest.mark.parametrize(
    'container',
    [
        Valid(10),
        Valid(42),
        Invalid(('first', 'second')),
        Invalid(('only',)),
    ],
)
def test_validated_pattern_matching(container: Validated[int, str]):
    """Ensures ``Validated`` containers work properly with pattern matching."""
    match container:
        case Valid(10):
            assert isinstance(container, Valid)
            assert container.unwrap() == 10
        case Valid(number):
            assert isinstance(container, Valid)
            assert number == 42
            assert container.unwrap() == number
        case Invalid((first_error, second_error)):
            assert isinstance(container, Invalid)
            assert container.failure() == (first_error, second_error)
        case Invalid(errors):
            assert isinstance(container, Invalid)
            assert errors == ('only',)
        case _:
            pytest.fail('Was not matched')
