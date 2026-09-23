from returns.result import Failure, Success
from returns.validated import Invalid, Valid, Validated


def test_from_value():
    """Ensures that ``from_value`` builds a success."""
    assert Validated.from_value(1) == Valid(1)


def test_from_failure_wraps_single_error():
    """Ensures that a single error is stored as a one-element tuple."""
    container = Validated.from_failure('a')

    assert container == Invalid(('a',))
    assert container.failure() == ('a',)


def test_from_result_success():
    """Ensures that ``Success`` becomes ``Valid``."""
    assert Validated.from_result(Success(1)) == Valid(1)


def test_from_result_failure():
    """Ensures that ``Failure`` becomes a one-element ``Invalid``."""
    assert Validated.from_result(Failure('a')) == Invalid(('a',))


def test_from_validated_returns_same_instance():
    """Ensures that ``from_validated`` does not copy the container."""
    container = Valid(1)

    assert Validated.from_validated(container) is container
    assert Validated.from_validated(Invalid(('a',))) is not Valid(1)


def test_from_validated_invalid_instance():
    """Ensures that failed containers are also returned as-is."""
    container = Invalid(('a', 'b'))

    assert Validated.from_validated(container) is container


def test_equality():
    """Ensures structural equality for both concrete containers."""
    assert Valid(1) == Valid(1)
    assert Valid(1) != Valid(2)
    assert Invalid(('a',)) == Invalid(('a',))
    assert Invalid(('a',)) != Invalid(('b',))
    assert Valid(1) != Invalid((1,))
    assert Valid(1).equals(Valid(1))
    assert not Valid(1).equals(Invalid(('a',)))


def test_repr():
    """Ensures containers render their inner value."""
    assert repr(Valid(1)) == '<Valid: 1>'
    assert repr(Invalid(('a',))) == "<Invalid: ('a',)>"
