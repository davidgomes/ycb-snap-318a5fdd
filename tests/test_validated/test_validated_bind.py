from returns.validated import Invalid, Valid, Validated


def test_bind():
    """Ensures that bind works and short-circuits on Invalid."""

    def factory(inner_value: int) -> Validated[int, str]:
        if inner_value > 0:
            return Valid(inner_value * 2)
        return Invalid((str(inner_value),))

    assert Valid(5).bind(factory) == Valid(10)
    assert Valid(0).bind(factory) == Invalid(('0',))
    assert Invalid(('a',)).bind(factory) == Invalid(('a',))
    assert Invalid(('a',)).bind_validated(factory) == Invalid(('a',))


def test_lash():
    """Ensures that lash works."""

    def factory(errors: tuple[int, ...]) -> Validated[str, int]:
        return Invalid((errors[0] + 1,))

    assert Valid(5).lash(factory) == Valid(5)
    assert Invalid((5,)).lash(factory) == Invalid((6,))
