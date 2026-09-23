from abc import ABC
from collections.abc import Callable, Generator, Iterator
from functools import wraps
from typing import TYPE_CHECKING, Any, TypeAlias, TypeVar, final, overload

from typing_extensions import Never, ParamSpec

from returns.interfaces.specific.validated import ValidatedBased2
from returns.primitives.container import BaseContainer, container_equality
from returns.primitives.exceptions import UnwrapFailedError
from returns.primitives.hkt import Kind2, SupportsKind2
from returns.result import Result, Success

# Definitions:
_ValueType_co = TypeVar('_ValueType_co', covariant=True)
_NewValueType = TypeVar('_NewValueType')
_ErrorType_co = TypeVar('_ErrorType_co', covariant=True)
_NewErrorType = TypeVar('_NewErrorType')
_SecondValueType = TypeVar('_SecondValueType')

_FuncParams = ParamSpec('_FuncParams')


class Validated(  # type: ignore[type-var]
    BaseContainer,
    SupportsKind2['Validated', _ValueType_co, _ErrorType_co],
    ValidatedBased2[_ValueType_co, _ErrorType_co],
    ABC,
):
    """
    Collects every independent failure instead of stopping at the first one.

    :class:`~Validated` is an abstract type.
    Use :class:`~Valid` and :class:`~Invalid` to construct values.

    ``.bind`` still short-circuits, same as :class:`~returns.result.Result`.
    ``.apply`` concatenates errors from both sides, left to right.

    Success values live in :class:`~Valid`.
    Failures live in :class:`~Invalid` and store an immutable tuple of errors.

    .. code:: python

      >>> from returns.validated import Invalid, Valid

      >>> assert Valid(1).apply(Valid(lambda number: number + 1)) == Valid(2)
      >>> assert Invalid(('a',)).apply(Invalid(('b',))) == Invalid(('a', 'b'))

    """

    __slots__ = ()
    __match_args__ = ('_inner_value',)

    _inner_value: _ValueType_co | tuple[_ErrorType_co, ...]

    #: Typesafe equality comparison with other ``Validated`` objects.
    equals = container_equality

    def map(
        self,
        function: Callable[[_ValueType_co], _NewValueType],
    ) -> 'Validated[_NewValueType, _ErrorType_co]':
        """
        Composes a successful container with a pure function.

        .. code:: python

          >>> from returns.validated import Invalid, Valid

          >>> def mappable(number: int) -> int:
          ...     return number + 1

          >>> assert Valid(1).map(mappable) == Valid(2)
          >>> assert Invalid(('a',)).map(mappable) == Invalid(('a',))

        """

    def apply(
        self,
        container: Kind2[
            'Validated',
            Callable[[_ValueType_co], _NewValueType],
            _ErrorType_co,
        ],
    ) -> 'Validated[_NewValueType, _ErrorType_co]':
        """
        Calls a wrapped function on this container.

        Two :class:`~Invalid` values concatenate their error tuples.
        ``self``'s errors come first, then the other container's errors.

        .. code:: python

          >>> from returns.validated import Invalid, Valid

          >>> def appliable(number: int) -> int:
          ...     return number + 1

          >>> assert Valid(1).apply(Valid(appliable)) == Valid(2)
          >>> assert Invalid(('a',)).apply(Valid(appliable)) == Invalid(('a',))
          >>> assert Valid(1).apply(Invalid(('b',))) == Invalid(('b',))
          >>> assert Invalid(('a',)).apply(
          ...     Invalid(('b', 'c')),
          ... ) == Invalid(('a', 'b', 'c'))

        """

    def bind(
        self,
        function: Callable[
            [_ValueType_co],
            Kind2['Validated', _NewValueType, _ErrorType_co],
        ],
    ) -> 'Validated[_NewValueType, _ErrorType_co]':
        """
        Composes a successful container with a function that returns one.

        Failures short-circuit: the function is not called
        and earlier errors are not concatenated with later ones.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

          >>> def bindable(number: int) -> Validated[int, str]:
          ...     if number > 0:
          ...         return Valid(number + 1)
          ...     return Invalid(('non-positive',))

          >>> assert Valid(1).bind(bindable) == Valid(2)
          >>> assert Valid(0).bind(bindable) == Invalid(('non-positive',))
          >>> assert Invalid(('a',)).bind(bindable) == Invalid(('a',))

        """

    #: Alias for ``bind``. Part of the ``ValidatedBasedN`` interface.
    bind_validated = bind

    def alt(
        self,
        function: Callable[[_ErrorType_co], _NewErrorType],
    ) -> 'Validated[_ValueType_co, _NewErrorType]':
        """
        Applies ``function`` to each error element of a failure.

        .. code:: python

          >>> from returns.validated import Invalid, Valid

          >>> def altable(error: str) -> str:
          ...     return error + '!'

          >>> assert Valid(1).alt(altable) == Valid(1)
          >>> assert Invalid(('a', 'b')).alt(altable) == Invalid(('a!', 'b!'))

        """

    def lash(  # type: ignore[override]
        self,
        function: Callable[
            [tuple[_ErrorType_co, ...]],
            Kind2['Validated', _ValueType_co, _NewErrorType],
        ],
    ) -> 'Validated[_ValueType_co, _NewErrorType]':
        """
        Composes a failed container with a function that returns a container.

        The function receives the whole error tuple.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

          >>> def lashable(
          ...     errors: tuple[str, ...],
          ... ) -> Validated[int, str]:
          ...     return Valid(len(errors))

          >>> assert Valid(1).lash(lashable) == Valid(1)
          >>> assert Invalid(('a', 'b')).lash(lashable) == Valid(2)

        """

    def swap(self) -> 'Validated[Any, Any]':
        """
        Swaps success and failure, wrapping a success value in a tuple.

        .. code:: python

          >>> from returns.validated import Invalid, Valid

          >>> assert Valid(1).swap() == Invalid((1,))
          >>> assert Invalid(('a', 'b')).swap() == Valid(('a', 'b'))

        Swapping twice is not the identity, because a success value is wrapped
        in a one-element tuple:

        .. code:: python

          >>> assert Valid(1).swap().swap() == Valid((1,))

        """

    def __iter__(self) -> Iterator[_ValueType_co]:
        """API for :ref:`do-notation`."""
        yield self.unwrap()

    @classmethod
    def do(
        cls,
        expr: Generator[_NewValueType, None, None],
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Unwrap containers inside a generator expression.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

          >>> assert Validated.do(
          ...     first + second
          ...     for first in Valid(2)
          ...     for second in Valid(3)
          ... ) == Valid(5)

          >>> assert Validated.do(
          ...     first + second
          ...     for first in Invalid(('a',))
          ...     for second in Valid(3)
          ... ) == Invalid(('a',))

        """
        try:
            return Validated.from_value(next(expr))
        except UnwrapFailedError as exc:
            return exc.halted_container  # type: ignore[return-value]

    def value_or(
        self,
        default_value: _NewValueType,
    ) -> _ValueType_co | _NewValueType:
        """
        Get the success value or return the default.

        .. code:: python

          >>> from returns.validated import Invalid, Valid

          >>> assert Valid(1).value_or(2) == 1
          >>> assert Invalid(('a',)).value_or(2) == 2

        """

    def unwrap(self) -> _ValueType_co:
        """
        Get the success value or raise an exception.

        .. code:: pycon
          :force:

          >>> from returns.validated import Invalid, Valid
          >>> assert Valid(1).unwrap() == 1

          >>> Invalid(('a',)).unwrap()
          Traceback (most recent call last):
            ...
          returns.primitives.exceptions.UnwrapFailedError

        """

    def failure(self) -> tuple[_ErrorType_co, ...]:
        """
        Get the error tuple or raise an exception.

        .. code:: pycon
          :force:

          >>> from returns.validated import Invalid, Valid
          >>> assert Invalid(('a', 'b')).failure() == ('a', 'b')

          >>> Valid(1).failure()
          Traceback (most recent call last):
            ...
          returns.primitives.exceptions.UnwrapFailedError

        """

    @classmethod
    def from_value(
        cls,
        inner_value: _NewValueType,
    ) -> 'Validated[_NewValueType, Any]':
        """
        Create a successful container from a raw value.

        .. code:: python

          >>> from returns.validated import Valid, Validated
          >>> assert Validated.from_value(1) == Valid(1)

        """
        return Valid(inner_value)

    @classmethod
    def from_failure(
        cls,
        inner_value: _NewErrorType,
    ) -> 'Validated[Any, _NewErrorType]':
        """
        Create a failed container from a single error.

        The error is wrapped in a one-element tuple so later ``.apply``
        steps can concatenate errors uniformly.

        .. code:: python

          >>> from returns.validated import Invalid, Validated
          >>> assert Validated.from_failure('a') == Invalid(('a',))

        """
        return Invalid((inner_value,))

    @classmethod
    def from_result(
        cls,
        inner_value: Result[_NewValueType, _NewErrorType],
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Convert a ``Result`` into a ``Validated``.

        :class:`~returns.result.Success` becomes :class:`~Valid`.
        :class:`~returns.result.Failure` becomes :class:`~Invalid`
        with the error wrapped in a one-element tuple.

        .. code:: python

          >>> from returns.result import Failure, Success
          >>> from returns.validated import Invalid, Valid, Validated

          >>> assert Validated.from_result(Success(1)) == Valid(1)
          >>> assert Validated.from_result(Failure('a')) == Invalid(('a',))

        """
        if isinstance(inner_value, Success):
            return Valid(inner_value.unwrap())
        return Invalid((inner_value.failure(),))

    @classmethod
    def from_validated(
        cls,
        inner_value: 'Validated[_NewValueType, _NewErrorType]',
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Return the same ``Validated`` instance.

        .. code:: python

          >>> from returns.validated import Valid, Validated
          >>> container = Valid(1)
          >>> assert Validated.from_validated(container) is container

        """
        return inner_value

    @classmethod
    def combine(
        cls,
        first: 'Validated[_ValueType_co, _ErrorType_co]',
        second: 'Validated[_SecondValueType, _ErrorType_co]',
        function: Callable[[_ValueType_co, _SecondValueType], _NewValueType],
    ) -> 'Validated[_NewValueType, _ErrorType_co]':
        """
        Combine two containers with a binary function.

        Uses applicative combination, so errors from both sides accumulate
        from left to right.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

          >>> assert Validated.combine(
          ...     Valid(1), Valid(2), lambda left, right: left + right,
          ... ) == Valid(3)
          >>> assert Validated.combine(
          ...     Invalid(('a',)),
          ...     Invalid(('b',)),
          ...     lambda left, right: left + right,
          ... ) == Invalid(('a', 'b'))

        """
        return cls.combine_n((first, second), function)

    @classmethod
    def combine_n(
        cls,
        containers: tuple['Validated[Any, _ErrorType_co]', ...],
        function: Callable[..., _NewValueType],
    ) -> 'Validated[_NewValueType, _ErrorType_co]':
        """
        Combine ``N`` containers with an ``N``-ary function.

        Errors from every failed container are accumulated from left to right.
        An empty tuple applies ``function`` with no arguments.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

          >>> assert Validated.combine_n(
          ...     (Valid(1), Valid(2), Valid(3)),
          ...     lambda first, second, third: first + second + third,
          ... ) == Valid(6)
          >>> assert Validated.combine_n(
          ...     (Invalid(('a',)), Valid(2), Invalid(('b', 'c'))),
          ...     lambda first, second, third: first + second + third,
          ... ) == Invalid(('a', 'b', 'c'))
          >>> assert Validated.combine_n((), lambda: 1) == Valid(1)

        """
        if not containers:
            return cls.from_value(function())

        acc: Validated[Any, _ErrorType_co] = cls.from_value(
            _curry_from_right(function, len(containers)),
        )
        for container in reversed(containers):
            acc = container.apply(acc)
        return acc


@final
class Invalid(Validated[Any, _ErrorType_co]):  # noqa: WPS338
    """Failed validation. Stores an immutable tuple of errors."""

    __slots__ = ()

    _inner_value: tuple[_ErrorType_co, ...]

    def __init__(self, inner_value: tuple[_ErrorType_co, ...]) -> None:
        """Invalid constructor."""
        super().__init__(inner_value)

    if not TYPE_CHECKING:  # noqa: WPS604  # pragma: no branch

        def alt(self, function):
            """Applies a function to each error element."""
            return Invalid(tuple(function(error) for error in self.failure()))

        def map(self, function):
            """Does nothing for ``Invalid``."""
            return self

        def bind(self, function):
            """Does nothing for ``Invalid``."""
            return self

        #: Alias for ``bind``. Part of the ``ValidatedBasedN`` interface.
        bind_validated = bind

        def lash(self, function):
            """Composes this container with a function returning a container."""
            return function(self.failure())

        def apply(self, container):
            """Concatenates errors when the function container also failed."""
            if isinstance(container, Invalid):
                return Invalid(self.failure() + container.failure())
            return self

        def value_or(self, default_value):
            """Returns the default value for a failed container."""
            return default_value

    def swap(self) -> 'Valid[tuple[_ErrorType_co, ...]]':
        """Failures swap to :class:`~Valid` holding the error tuple."""
        return Valid(self._inner_value)

    def unwrap(self) -> Never:
        """Raises an exception, since there is no success value inside."""
        if self._inner_value and isinstance(self._inner_value[0], Exception):
            raise UnwrapFailedError(self) from self._inner_value[0]
        raise UnwrapFailedError(self)

    def failure(self) -> tuple[_ErrorType_co, ...]:
        """Returns the error tuple."""
        return self._inner_value


@final
class Valid(Validated[_ValueType_co, Any]):
    """Successful validation. Contains the computed value."""

    __slots__ = ()

    _inner_value: _ValueType_co

    def __init__(self, inner_value: _ValueType_co) -> None:
        """Valid constructor."""
        super().__init__(inner_value)

    if not TYPE_CHECKING:  # noqa: WPS604  # pragma: no branch

        def alt(self, function):
            """Does nothing for ``Valid``."""
            return self

        def map(self, function):
            """Composes the current container with a pure function."""
            return Valid(function(self._inner_value))

        def bind(self, function):
            """Binds the current container to a function."""
            return function(self._inner_value)

        #: Alias for ``bind``. Part of the ``ValidatedBasedN`` interface.
        bind_validated = bind

        def lash(self, function):
            """Does nothing for ``Valid``."""
            return self

        def apply(self, container):
            """Calls a wrapped function on this container."""
            if isinstance(container, Valid):
                return self.map(container.unwrap())
            return container

        def value_or(self, default_value):
            """Returns the success value."""
            return self._inner_value

    def swap(self) -> 'Invalid[_ValueType_co]':
        """Successes swap to :class:`~Invalid` with a one-element tuple."""
        return Invalid((self._inner_value,))

    def unwrap(self) -> _ValueType_co:
        """Returns the unwrapped success value."""
        return self._inner_value

    def failure(self) -> Never:
        """Raises an exception for a successful container."""
        raise UnwrapFailedError(self)


# Aliases:

#: Alias for ``Validated[_ValueType_co, Exception]``.
ValidatedE: TypeAlias = Validated[_ValueType_co, Exception]


# Decorators:

_ExceptionType = TypeVar('_ExceptionType', bound=Exception)


@overload
def validated(
    function: Callable[_FuncParams, _ValueType_co],
    /,
) -> Callable[_FuncParams, ValidatedE[_ValueType_co]]: ...


@overload
def validated(
    exceptions: tuple[type[_ExceptionType], ...],
) -> Callable[
    [Callable[_FuncParams, _ValueType_co]],
    Callable[_FuncParams, Validated[_ValueType_co, _ExceptionType]],
]: ...


def validated(  # noqa: WPS234
    exceptions: (
        Callable[_FuncParams, _ValueType_co] | tuple[type[_ExceptionType], ...]
    ),
) -> (
    Callable[_FuncParams, ValidatedE[_ValueType_co]]
    | Callable[
        [Callable[_FuncParams, _ValueType_co]],
        Callable[_FuncParams, Validated[_ValueType_co, _ExceptionType]],
    ]
):
    """
    Decorator to turn an exception-raising function into ``Validated``.

    Should be used with care, since it only catches ``Exception`` subclasses.
    It does not catch ``BaseException`` subclasses.

    Caught exceptions are stored as a one-element tuple inside
    :class:`~Invalid`.

    .. code:: python

      >>> from returns.validated import Invalid, Valid, validated

      >>> @validated
      ... def might_raise(arg: int) -> float:
      ...     return 1 / arg

      >>> assert might_raise(1) == Valid(1.0)
      >>> assert isinstance(might_raise(0), Invalid)
      >>> assert might_raise.__name__ == 'might_raise'

    You can also pass explicit exception types:

    .. code:: python

      >>> from returns.validated import Invalid, Valid, validated

      >>> @validated(exceptions=(ZeroDivisionError,))
      ... def might_raise(arg: int) -> float:
      ...     return 1 / arg

      >>> assert might_raise(1) == Valid(1.0)
      >>> assert isinstance(might_raise(0), Invalid)
      >>> assert might_raise.__name__ == 'might_raise'

    In this case, only the listed exceptions are caught.
    """

    def factory(
        inner_function: Callable[_FuncParams, _ValueType_co],
        inner_exceptions: tuple[type[_ExceptionType], ...],
    ) -> Callable[_FuncParams, Validated[_ValueType_co, _ExceptionType]]:
        @wraps(inner_function)
        def decorator(
            *args: _FuncParams.args,
            **kwargs: _FuncParams.kwargs,
        ) -> Validated[_ValueType_co, _ExceptionType]:
            try:
                return Valid(inner_function(*args, **kwargs))
            except inner_exceptions as exc:
                return Invalid((exc,))

        return decorator

    if isinstance(exceptions, tuple):
        return lambda function: factory(function, exceptions)
    return factory(
        exceptions,
        (Exception,),  # type: ignore[arg-type]
    )


def _curry_from_right(
    function: Callable[..., _NewValueType],
    count: int,
) -> Any:
    """Build a function that accepts arguments from right to left."""

    def _next(collected: tuple[Any, ...]) -> Any:
        if len(collected) == count:
            return function(*reversed(collected))
        return lambda item: _next((*collected, item))  # noqa: WPS430

    return _next(())
