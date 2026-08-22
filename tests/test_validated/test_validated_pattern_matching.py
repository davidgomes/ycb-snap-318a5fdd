import pytest

from returns.validated import Invalid, Valid, Validated


@pytest.mark.parametrize(
    'container',
    [
        Valid(10),
        Valid(42),
        Invalid((1,)),
        Invalid((1, 2)),
    ],
)
def test_validated_pattern_matching(container: Validated[int, int]):
    """Ensures ``Validated`` containers work with pattern matching."""
    match container:
        case Valid(10):
            assert isinstance(container, Valid)
            assert container.unwrap() == 10
        case Valid(value):
            assert isinstance(container, Valid)
            assert value == 42
            assert container.unwrap() == value
        case Invalid((1, 2)):
            assert container.failure() == (1, 2)
        case Invalid(_):
            assert isinstance(container, Invalid)
            assert container.failure() == (1,)
        case _:
            pytest.fail('Was not matched')
