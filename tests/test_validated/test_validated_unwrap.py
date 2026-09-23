import pytest

from returns.primitives.exceptions import UnwrapFailedError
from returns.validated import Invalid, Valid


def test_unwrap_success():
    """Ensures unwrap returns the success value."""
    assert Valid(5).unwrap() == 5


def test_unwrap_failure():
    """Ensures unwrap raises for a non-exception error."""
    with pytest.raises(UnwrapFailedError):
        Invalid((5,)).unwrap()


def test_unwrap_empty_failure():
    """Ensures unwrap raises when the error tuple is empty."""
    with pytest.raises(UnwrapFailedError):
        Invalid(()).unwrap()


def test_unwrap_failure_with_exception():
    """Ensures unwrap raises from the first stored exception."""
    expected = ValueError('error')
    with pytest.raises(UnwrapFailedError) as excinfo:
        Invalid((expected, 'other')).unwrap()

    assert excinfo.value.__cause__ is expected


def test_failure_success():
    """Ensures failure raises for a success container."""
    with pytest.raises(UnwrapFailedError):
        Valid(1).failure()


def test_failure_returns_tuple():
    """Ensures failure returns the stored tuple."""
    assert Invalid(('a', 'b')).failure() == ('a', 'b')
