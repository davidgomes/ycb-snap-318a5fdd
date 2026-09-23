from returns.validated import Invalid, Valid


def _match(container: Valid[int] | Invalid[str]):
    match container:
        case Valid(value):
            return ('valid', value)
        case Invalid(errors):
            return ('invalid', errors)


def test_match_args():
    """Ensures both containers expose the inner value to pattern matching."""
    assert Valid.__match_args__ == ('_inner_value',)
    assert Invalid.__match_args__ == ('_inner_value',)


def test_match_valid():
    """Ensures ``Valid`` matches its success value."""
    assert _match(Valid(1)) == ('valid', 1)
    assert _match(Valid(10)) == ('valid', 10)


def test_match_invalid():
    """Ensures ``Invalid`` matches the error tuple."""
    assert _match(Invalid(('a', 'b'))) == ('invalid', ('a', 'b'))
