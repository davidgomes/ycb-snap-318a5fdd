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

_FirstType = TypeVar('_FirstType')
_SecondType = TypeVar('_SecondType')
_FuncParams = ParamSpec('_FuncParams')


class Validated(  # type: ignore[type-var]
    BaseContainer,
    SupportsKind2['Validated', _ValueType_co, _ErrorType_co],
    ValidatedBased2[_ValueType_co, _ErrorType_co],
    ABC,
):
    """
    Base class for :class:`~Valid` and :class:`~Invalid`.

    Works like :class:`returns.result.Result`,
    but ``.apply`` accumulates errors from all failed containers
    instead of stopping on the first one.
    ``.bind`` still short-circuits on the first failure.

    :class:`~Invalid` always stores its errors as a ``tuple``.

    .. code:: python

      >>> from returns.validated import Validated, Valid, Invalid

      >>> assert Validated.combine(
      ...     Valid(1), Valid(2), lambda first, second: first + second,
      ... ) == Valid(3)

      >>> assert Validated.combine(
      ...     Invalid(('a',)), Invalid(('b',)), lambda first, second: 0,
      ... ) == Invalid(('a', 'b'))

    """

    __slots__ = ()
    __match_args__ = ('_inner_value',)

    _inner_value: _ValueType_co | tuple[_ErrorType_co, ...]

    #: Typesafe equality comparison with other `Validated` objects.
    equals = container_equality

    def swap(self) -> 'Validated[tuple[_ErrorType_co, ...], _ValueType_co]':
        """
        Swaps value and error types.

        Values become a single error, errors become a tuple value.
        Because of that ``x.swap().swap() == x`` does not hold.

        .. code:: python

          >>> from returns.validated import Valid, Invalid
          >>> assert Valid(1).swap() == Invalid((1,))
          >>> assert Invalid((1, 2)).swap() == Valid((1, 2))

        """

    def map(
        self,
        function: Callable[[_ValueType_co], _NewValueType],
    ) -> 'Validated[_NewValueType, _ErrorType_co]':
        """
        Composes successful container with a pure function.

        .. code:: python

          >>> from returns.validated import Valid, Invalid

          >>> def mappable(string: str) -> str:
          ...      return string + 'b'

          >>> assert Valid('a').map(mappable) == Valid('ab')
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
        Calls a wrapped function in a container on this container.

        When both containers are failed, their errors are accumulated:
        errors of this container go first, then errors of the other one.

        .. code:: python

          >>> from returns.validated import Valid, Invalid

          >>> def appliable(string: str) -> str:
          ...      return string + 'b'

          >>> assert Valid('a').apply(Valid(appliable)) == Valid('ab')
          >>> assert Invalid(('a',)).apply(Valid(appliable)) == Invalid(('a',))
          >>> assert Valid('a').apply(Invalid((1,))) == Invalid((1,))
          >>> assert Invalid((1,)).apply(Invalid((2,))) == Invalid((1, 2))

        """

    def bind(
        self,
        function: Callable[
            [_ValueType_co],
            Kind2['Validated', _NewValueType, _ErrorType_co],
        ],
    ) -> 'Validated[_NewValueType, _ErrorType_co]':
        """
        Composes successful container with a function that returns a container.

        Short-circuits on the first failure, does not accumulate errors.

        .. code:: python

          >>> from returns.validated import Validated, Valid, Invalid

          >>> def bindable(arg: str) -> Validated[str, str]:
          ...      if len(arg) > 1:
          ...          return Valid(arg + 'b')
          ...      return Invalid((arg + 'c',))

          >>> assert Valid('aa').bind(bindable) == Valid('aab')
          >>> assert Valid('a').bind(bindable) == Invalid(('ac',))
          >>> assert Invalid(('a',)).bind(bindable) == Invalid(('a',))

        """

    #: Alias for `bind_validated` method, it is the same as `bind` here.
    bind_validated = bind

    def alt(
        self,
        function: Callable[[_ErrorType_co], _NewErrorType],
    ) -> 'Validated[_ValueType_co, _NewErrorType]':
        """
        Composes failed container with a pure function applied to each error.

        .. code:: python

          >>> from returns.validated import Valid, Invalid

          >>> def altable(arg: str) -> str:
          ...      return arg + 'b'

          >>> assert Valid('a').alt(altable) == Valid('a')
          >>> assert Invalid(('a', 'c')).alt(altable) == Invalid(('ab', 'cb'))

        """

    def lash(  # type: ignore[override]
        self,
        function: Callable[
            [tuple[_ErrorType_co, ...]],
            Kind2['Validated', _ValueType_co, _NewErrorType],
        ],
    ) -> 'Validated[_ValueType_co, _NewErrorType]':
        """
        Composes failed container with a function that returns a container.

        The function receives the whole tuple of errors.

        .. code:: python

          >>> from returns.validated import Validated, Valid, Invalid

          >>> def lashable(errors: tuple[str, ...]) -> Validated[int, str]:
          ...      if len(errors) > 1:
          ...          return Invalid(errors[:1])
          ...      return Valid(len(errors))

          >>> assert Valid(0).lash(lashable) == Valid(0)
          >>> assert Invalid(('a',)).lash(lashable) == Valid(1)
          >>> assert Invalid(('a', 'b')).lash(lashable) == Invalid(('a',))

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
        Allows working with unwrapped values of containers in a safe way.

        Like ``.bind`` it short-circuits on the first failure.

        .. code:: python

          >>> from returns.validated import Validated, Valid, Invalid

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

        See :ref:`do-notation` to learn more.

        """
        try:
            return Validated.from_value(next(expr))
        except UnwrapFailedError as exc:
            return exc.halted_container  # type: ignore

    def value_or(
        self,
        default_value: _NewValueType,
    ) -> _ValueType_co | _NewValueType:
        """
        Get value or default value.

        .. code:: python

          >>> from returns.validated import Valid, Invalid
          >>> assert Valid(1).value_or(2) == 1
          >>> assert Invalid((1,)).value_or(2) == 2

        """

    def unwrap(self) -> _ValueType_co:
        """
        Get value or raise exception.

        .. code:: pycon
          :force:

          >>> from returns.validated import Valid, Invalid
          >>> assert Valid(1).unwrap() == 1

          >>> Invalid((1,)).unwrap()
          Traceback (most recent call last):
            ...
          returns.primitives.exceptions.UnwrapFailedError

        """

    def failure(self) -> tuple[_ErrorType_co, ...]:
        """
        Get the tuple of errors or raise exception.

        .. code:: pycon
          :force:

          >>> from returns.validated import Valid, Invalid
          >>> assert Invalid((1, 2)).failure() == (1, 2)

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
        Creates new successful container.

        .. code:: python

          >>> from returns.validated import Validated, Valid
          >>> assert Validated.from_value(1) == Valid(1)

        """
        return Valid(inner_value)

    @classmethod
    def from_failure(
        cls,
        inner_value: _NewErrorType,
    ) -> 'Validated[Any, _NewErrorType]':
        """
        Creates new failed container from a single error.

        The error is wrapped into a 1-tuple.

        .. code:: python

          >>> from returns.validated import Validated, Invalid
          >>> assert Validated.from_failure(1) == Invalid((1,))

        """
        return Invalid((inner_value,))

    @classmethod
    def from_result(
        cls,
        inner_value: Result[_NewValueType, _NewErrorType],
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Creates new ``Validated`` instance from ``Result`` instance.

        .. code:: python

          >>> from returns.result import Failure, Success
          >>> from returns.validated import Validated, Valid, Invalid
          >>> assert Validated.from_result(Success(1)) == Valid(1)
          >>> assert Validated.from_result(Failure(1)) == Invalid((1,))

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
        Returns the given ``Validated`` instance as is.

        .. code:: python

          >>> from returns.validated import Validated, Valid, Invalid
          >>> assert Validated.from_validated(Valid(1)) == Valid(1)
          >>> assert Validated.from_validated(Invalid((1,))) == Invalid((1,))

        """
        return inner_value

    @classmethod
    def combine(
        cls,
        first: 'Validated[_FirstType, _NewErrorType]',
        second: 'Validated[_SecondType, _NewErrorType]',
        function: Callable[[_FirstType, _SecondType], _NewValueType],
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Combines two containers with a binary function.

        Errors from both containers are accumulated in order.

        .. code:: python

          >>> from returns.validated import Validated, Valid, Invalid

          >>> def add(first: int, second: int) -> int:
          ...     return first + second

          >>> assert Validated.combine(Valid(1), Valid(2), add) == Valid(3)
          >>> assert Validated.combine(
          ...     Invalid(('a',)), Valid(2), add,
          ... ) == Invalid(('a',))
          >>> assert Validated.combine(
          ...     Invalid(('a',)), Invalid(('b',)), add,
          ... ) == Invalid(('a', 'b'))

        """
        return first.apply(
            second.map(
                lambda second_value: lambda first_value: function(
                    first_value,
                    second_value,
                ),
            ),
        )

    @classmethod
    def combine_n(
        cls,
        containers: 'tuple[Validated[Any, _NewErrorType], ...]',
        function: Callable[..., _NewValueType],
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Combines any number of containers with an N-ary function.

        Errors from all failed containers are accumulated in order.

        .. code:: python

          >>> from returns.validated import Validated, Valid, Invalid

          >>> def total(*args: int) -> int:
          ...     return sum(args)

          >>> assert Validated.combine_n(
          ...     (Valid(1), Valid(2), Valid(3)), total,
          ... ) == Valid(6)
          >>> assert Validated.combine_n(
          ...     (Invalid(('a',)), Valid(2), Invalid(('b', 'c'))), total,
          ... ) == Invalid(('a', 'b', 'c'))

        """
        acc: Validated[tuple[Any, ...], _NewErrorType] = Valid(())
        for container in containers:
            acc = cls.combine(
                acc,
                container,
                lambda collected, current: (*collected, current),
            )
        return acc.map(lambda collected: function(*collected))


@final  # noqa: WPS338
class Invalid(Validated[Any, _ErrorType_co]):  # noqa: WPS338
    """
    Represents a failed validation.

    Contains an immutable tuple of errors.
    """

    __slots__ = ()

    _inner_value: tuple[_ErrorType_co, ...]

    def __init__(self, inner_value: tuple[_ErrorType_co, ...]) -> None:
        """Invalid constructor, always stores errors as a tuple."""
        super().__init__(tuple(inner_value))

    if not TYPE_CHECKING:  # noqa: WPS604  # pragma: no branch

        def alt(self, function):
            """Composes each error with a pure function."""
            return Invalid(tuple(function(error) for error in self._inner_value))

        def map(self, function):
            """Does nothing for ``Invalid``."""
            return self

        def bind(self, function):
            """Does nothing for ``Invalid``."""
            return self

        #: Alias for `bind` method. Part of the `ValidatedBasedN` interface.
        bind_validated = bind

        def lash(self, function):
            """Composes this container with a function returning container."""
            return function(self._inner_value)

        def apply(self, container):
            """Accumulates errors when ``container`` is also ``Invalid``."""
            if isinstance(container, Invalid):
                return Invalid(self._inner_value + container._inner_value)  # noqa: SLF001
            return self

        def value_or(self, default_value):
            """Returns default value for failed container."""
            return default_value

    def swap(self) -> 'Valid[tuple[_ErrorType_co, ...]]':
        """Invalid swaps to :class:`Valid` with a tuple of errors."""
        return Valid(self._inner_value)

    def unwrap(self) -> Never:
        """Raises an exception, since it does not have a value inside."""
        for error in self._inner_value:
            if isinstance(error, Exception):
                raise UnwrapFailedError(self) from error
        raise UnwrapFailedError(self)

    def failure(self) -> tuple[_ErrorType_co, ...]:
        """Returns the tuple of errors."""
        return self._inner_value


@final
class Valid(Validated[_ValueType_co, Any]):
    """
    Represents a successful validation.

    Contains the computation value.
    """

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
            """Composes current container with a pure function."""
            return Valid(function(self._inner_value))

        def bind(self, function):
            """Binds current container to a function that returns container."""
            return function(self._inner_value)

        #: Alias for `bind` method. Part of the `ValidatedBasedN` interface.
        bind_validated = bind

        def lash(self, function):
            """Does nothing for ``Valid``."""
            return self

        def apply(self, container):
            """Calls a wrapped function in a container on this container."""
            if isinstance(container, Valid):
                return self.map(container.unwrap())
            return container

        def value_or(self, default_value):
            """Returns the value for successful container."""
            return self._inner_value

    def swap(self) -> 'Invalid[_ValueType_co]':
        """Valid swaps to :class:`Invalid` with a single error."""
        return Invalid((self._inner_value,))

    def unwrap(self) -> _ValueType_co:
        """Returns the unwrapped value from successful container."""
        return self._inner_value

    def failure(self) -> Never:
        """Raises an exception for successful container."""
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
    Decorator to convert exception-throwing function to ``Validated``.

    Caught exceptions are returned as ``Invalid`` with a single error.
    It only catches ``Exception`` subclasses by default.

    .. code:: python

      >>> from returns.validated import Invalid, Valid, validated

      >>> @validated
      ... def might_raise(arg: int) -> float:
      ...     return 1 / arg

      >>> assert might_raise(1) == Valid(1.0)
      >>> assert isinstance(might_raise(0), Invalid)

    You can also pass explicit exception types to catch:

    .. code:: python

      >>> @validated(exceptions=(ZeroDivisionError,))
      ... def might_raise(arg: int) -> float:
      ...     return 1 / arg

      >>> assert might_raise(1) == Valid(1.0)
      >>> assert isinstance(might_raise(0), Invalid)

    Similar to :func:`returns.result.safe`.
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
