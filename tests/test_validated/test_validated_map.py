from returns.validated import Invalid, Valid


def _double(number: int) -> int:
    return number * 2


def test_map():
    """Ensures that map works."""
    mapped = Valid(1).map(str)
    skipped = Invalid((1,)).map(str)
    assert mapped == Valid('1')
    assert skipped == Invalid((1,))


def test_alt_maps_each_error():
    """Ensures that alt maps each error element."""
    success = Valid(1).alt(str)
    failed = Invalid((1, 2)).alt(str)
    assert success == Valid(1)
    assert failed == Invalid(('1', '2'))


def test_apply_accumulates_errors():
    """Ensures apply concatenates errors left-to-right."""
    both_valid = Valid(2).apply(Valid(_double))
    invalid_value = Invalid((1,)).apply(Valid(_double))
    invalid_func = Valid(2).apply(Invalid((3,)))
    both_invalid = Invalid((1,)).apply(Invalid((2, 3)))

    assert both_valid == Valid(4)
    assert invalid_value == Invalid((1,))
    assert invalid_func == Invalid((3,))
    assert both_invalid == Invalid((1, 2, 3))
