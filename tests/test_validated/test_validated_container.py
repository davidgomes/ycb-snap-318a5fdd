import pytest

from returns.converters import result_to_validated, validated_to_result
from returns.interfaces.failable import DiverseFailableN, FailableN
from returns.interfaces.specific.validated import ValidatedLikeN
from returns.iterables import Fold
from returns.methods import cond
from returns.pointfree import bind_validated
from returns.primitives.exceptions import UnwrapFailedError
from returns.result import Failure, Success
from returns.validated import Invalid, Valid, Validated, validated


def test_interface_hierarchy():
    """Ensures ``Validated`` is failable but not diverse failable."""
    assert issubclass(Validated, ValidatedLikeN)
    assert issubclass(Validated, FailableN)
    assert not issubclass(Validated, DiverseFailableN)


def test_invalid_stores_tuple():
    """Ensures ``Invalid`` always stores errors as a tuple."""
    assert Invalid(['a', 'b']).failure() == ('a', 'b')  # type: ignore[arg-type]
    assert isinstance(Invalid(('a',)).failure(), tuple)


def test_from_failure_wraps_single_error():
    """Ensures ``from_failure`` creates a 1-tuple of errors."""
    assert Validated.from_failure('a') == Invalid(('a',))
    assert Validated.from_failure(('a', 'b')) == Invalid((('a', 'b'),))


def test_apply_accumulates_in_order():
    """Ensures ``apply`` concatenates errors left to right."""
    assert Invalid(('a', 'b')).apply(Invalid(('c',))) == Invalid(
        ('a', 'b', 'c'),
    )
    assert Invalid(('a',)).apply(Valid(str)) == Invalid(('a',))
    assert Valid(1).apply(Invalid(('a',))) == Invalid(('a',))
    assert Valid(1).apply(Valid(str)) == Valid('1')


def test_bind_short_circuits():
    """Ensures ``bind`` does not call the function on ``Invalid``."""
    calls = []

    def factory(arg: int) -> Validated[int, str]:
        calls.append(arg)
        return Invalid(('never',))

    assert Invalid(('a',)).bind(factory) == Invalid(('a',))
    assert Invalid(('a',)).bind_validated(factory) == Invalid(('a',))
    assert not calls
    assert Valid(1).bind(factory) == Invalid(('never',))
    assert Valid(1).bind_validated(factory) == Invalid(('never',))


def test_swap():
    """Ensures ``swap`` wraps values into a tuple and unwraps errors."""
    assert Valid(1).swap() == Invalid((1,))
    assert Invalid((1, 2)).swap() == Valid((1, 2))


def test_alt_maps_each_error():
    """Ensures ``alt`` maps every single error."""
    assert Invalid((1, 2)).alt(str) == Invalid(('1', '2'))
    assert Valid(1).alt(str) == Valid(1)


def test_lash():
    """Ensures ``lash`` passes the whole tuple of errors."""
    assert Invalid((1, 2)).lash(lambda errors: Valid(len(errors))) == Valid(2)
    assert Valid(1).lash(lambda errors: Valid(0)) == Valid(1)


def test_from_result_and_validated():
    """Ensures conversion classmethods work."""
    assert Validated.from_result(Success(1)) == Valid(1)
    assert Validated.from_result(Failure('a')) == Invalid(('a',))

    instance = Invalid(('a',))
    assert Validated.from_validated(instance) is instance


def test_unwrap_and_failure():
    """Ensures unwrapping methods work."""
    assert Valid(1).unwrap() == 1
    assert Valid(1).value_or(2) == 1
    assert Invalid(('a',)).value_or(2) == 2
    assert Invalid(('a',)).failure() == ('a',)

    with pytest.raises(UnwrapFailedError):
        Invalid(('a',)).unwrap()
    with pytest.raises(UnwrapFailedError):
        Valid(1).failure()


def test_unwrap_chains_exception():
    """Ensures ``unwrap`` chains the first exception error."""
    error = ValueError()
    with pytest.raises(UnwrapFailedError) as exc_info:
        Invalid(('a', error)).unwrap()
    assert exc_info.value.__cause__ is error


def test_equality_and_repr():
    """Ensures standard container behaviour is inherited."""
    assert Valid(1) == Valid(1)
    assert Valid(1) != Invalid((1,))
    assert Valid(1).equals(Valid(1))
    assert repr(Valid(1)) == '<Valid: 1>'
    assert repr(Invalid(('a',))) == "<Invalid: ('a',)>"
    assert hash(Invalid(('a',))) == hash(Invalid(('a',)))


def test_do_notation():
    """Ensures do-notation short-circuits."""
    assert Validated.do(
        first + second for first in Valid(1) for second in Valid(2)
    ) == Valid(3)
    assert Validated.do(
        first + second
        for first in Invalid(('a',))
        for second in Invalid(('b',))
    ) == Invalid(('a',))


def test_combine():
    """Ensures ``combine`` accumulates errors in argument order."""
    def add(first: int, second: int) -> int:
        return first + second

    assert Validated.combine(Valid(1), Valid(2), add) == Valid(3)
    assert Validated.combine(Invalid(('a',)), Valid(2), add) == Invalid(
        ('a',),
    )
    assert Validated.combine(Valid(1), Invalid(('b',)), add) == Invalid(
        ('b',),
    )
    assert Validated.combine(
        Invalid(('a',)),
        Invalid(('b',)),
        add,
    ) == Invalid(('a', 'b'))


def test_combine_n():
    """Ensures ``combine_n`` accumulates all errors in order."""
    def build(*args: int) -> tuple[int, ...]:
        return args

    assert Validated.combine_n((), build) == Valid(())
    assert Validated.combine_n((Valid(1), Valid(2), Valid(3)), build) == Valid(
        (1, 2, 3),
    )
    assert Validated.combine_n(
        (Invalid(('a',)), Valid(2), Invalid(('b', 'c'))),
        build,
    ) == Invalid(('a', 'b', 'c'))


def test_fold_collect():
    """Ensures ``Fold.collect`` accumulates errors through ``apply``."""
    acc = Valid(())
    assert Fold.collect([Valid(1), Valid(2)], acc) == Valid((1, 2))
    assert Fold.collect(
        [Invalid(('a',)), Valid(2), Invalid(('b',))],
        acc,
    ) == Invalid(('a', 'b'))
    assert Fold.collect_all(
        [Invalid(('a',)), Valid(2), Invalid(('b',))],
        acc,
    ) == Valid((2,))


def test_cond():
    """Ensures ``cond`` creates ``Validated`` values."""
    assert cond(Validated, True, 1, 'a') == Valid(1)  # noqa: FBT003
    assert cond(Validated, False, 1, 'a') == Invalid(('a',))  # noqa: FBT003


def test_pointfree_bind_validated():
    """Ensures pointfree ``bind_validated`` works."""
    def factory(arg: int) -> Validated[int, str]:
        return Valid(arg + 1)

    bound = bind_validated(factory)
    assert bound(Valid(1)) == Valid(2)
    assert bound(Invalid(('a',))) == Invalid(('a',))


def test_converters():
    """Ensures ``Result`` and ``Validated`` converters work."""
    assert result_to_validated(Success(1)) == Valid(1)
    assert result_to_validated(Failure('a')) == Invalid(('a',))
    assert validated_to_result(Valid(1)) == Success(1)
    assert validated_to_result(Invalid(('a', 'b'))) == Failure(('a', 'b'))


def test_validated_decorator():
    """Ensures ``validated`` decorator catches exceptions."""
    @validated
    def might_raise(arg: int) -> float:
        return 1 / arg

    assert might_raise.__name__ == 'might_raise'
    assert might_raise(1) == Valid(1.0)
    failed = might_raise(0)
    assert isinstance(failed, Invalid)
    assert isinstance(failed.failure()[0], ZeroDivisionError)


def test_validated_decorator_exceptions():
    """Ensures ``validated`` decorator catches only listed exceptions."""
    @validated(exceptions=(ZeroDivisionError,))
    def might_raise(arg: int) -> float:
        if arg < 0:
            raise ValueError(arg)
        return 1 / arg

    assert might_raise.__name__ == 'might_raise'
    assert isinstance(might_raise(0), Invalid)
    with pytest.raises(ValueError, match='-1'):
        might_raise(-1)


@pytest.mark.parametrize(
    'container',
    [
        Valid(10),
        Valid(42),
        Invalid(('a', 'b')),
        Invalid(()),
    ],
)
def test_pattern_matching(container: Validated[int, str]):
    """Ensures ``Validated`` containers work with pattern matching."""
    match container:
        case Valid(10):
            assert container.unwrap() == 10
        case Valid(value):
            assert value == 42
        case Invalid((first, second)):
            assert (first, second) == ('a', 'b')
        case Invalid(errors):
            assert errors == ()
        case _:
            pytest.fail('Was not matched')
