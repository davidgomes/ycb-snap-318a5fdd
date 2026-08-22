from abc import ABC
from collections.abc import Callable, Generator, Iterator
from functools import wraps
from typing import TYPE_CHECKING, Any, TypeAlias, TypeVar, final, overload

from typing_extensions import Never, ParamSpec

from returns.interfaces.specific import validated as validated_interface
from returns.primitives.container import BaseContainer, container_equality
from returns.primitives.exceptions import UnwrapFailedError
from returns.primitives.hkt import Kind2, SupportsKind2

if TYPE_CHECKING:
    from returns.result import Result  # noqa: WPS433

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
    validated_interface.ValidatedBased2[_ValueType_co, _ErrorType_co],
    ABC,
):
    """
    Base class for :class:`~Valid` and :class:`~Invalid`.

    Collects all independent validation errors instead of stopping
    at the first failure. ``bind`` still short-circuits.

    :class:`~Validated` does not have a public constructor.
    Use :func:`~Valid` and :func:`~Invalid` to construct the needed values.
    """

    __slots__ = ()
    __match_args__ = ('_inner_value',)

    _inner_value: _ValueType_co | tuple[_ErrorType_co, ...]

    #: Typesafe equality comparison with other `Validated` objects.
    equals = container_equality

    def swap(self) -> 'Validated[tuple[_ErrorType_co, ...], _ValueType_co]':
        """
        Swaps value and error types.

        ``Valid(x)`` becomes ``Invalid((x,))``.
        ``Invalid(errs)`` becomes ``Valid(errs)``.

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

          >>> from returns.validated import Invalid, Valid

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

        When both containers are ``Invalid``, errors are concatenated
        in left-to-right order: ``self`` then ``other``.

        .. code:: python

          >>> from returns.validated import Invalid, Valid

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

        Failed containers short-circuit and do not run ``function``.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

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
        Maps a pure function over each individual stored error.

        .. code:: python

          >>> from returns.validated import Invalid, Valid

          >>> def altable(arg: str) -> str:
          ...      return arg + 'b'

          >>> assert Valid('a').alt(altable) == Valid('a')
          >>> assert Invalid(('a', 'c')).alt(altable) == Invalid(('ab', 'cb'))

        """

    def lash(
        self,
        function: Callable[
            [tuple[_ErrorType_co, ...]],
            Kind2['Validated', _ValueType_co, _NewErrorType],
        ],
    ) -> 'Validated[_ValueType_co, _NewErrorType]':
        """
        Composes failed container with a function that returns a container.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

          >>> def lashable(errs: tuple[str, ...]) -> Validated[str, str]:
          ...      if len(errs) > 1:
          ...          return Valid(''.join(errs) + 'b')
          ...      return Invalid((errs[0] + 'c',))

          >>> assert Valid('a').lash(lashable) == Valid('a')
          >>> assert Invalid(('a',)).lash(lashable) == Invalid(('ac',))
          >>> assert Invalid(('a', 'a')).lash(lashable) == Valid('aab')

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

          >>> from returns.validated import Invalid, Valid
          >>> assert Valid(1).value_or(2) == 1
          >>> assert Invalid((1,)).value_or(2) == 2

        """

    def unwrap(self) -> _ValueType_co:
        """
        Get value or raise exception.

        .. code:: pycon
          :force:

          >>> from returns.validated import Invalid, Valid
          >>> assert Valid(1).unwrap() == 1

          >>> Invalid((1,)).unwrap()
          Traceback (most recent call last):
            ...
          returns.primitives.exceptions.UnwrapFailedError

        """

    def failure(self) -> tuple[_ErrorType_co, ...]:
        """
        Get failed value or raise exception.

        .. code:: pycon
          :force:

          >>> from returns.validated import Invalid, Valid
          >>> assert Invalid((1,)).failure() == (1,)

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
        Creates a successful ``Validated`` container.

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
        Creates a failed ``Validated`` from a single error.

        The error is wrapped into a 1-tuple so accumulation is uniform.

        .. code:: python

          >>> from returns.validated import Invalid, Validated
          >>> assert Validated.from_failure(1) == Invalid((1,))

        """
        return Invalid((inner_value,))

    @classmethod
    def from_result(
        cls,
        inner_value: 'Result[_NewValueType, _NewErrorType]',
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Converts a ``Result`` into a ``Validated``.

        ``Success`` becomes ``Valid``.
        ``Failure``'s error is wrapped in a 1-tuple to become ``Invalid``.

        .. code:: python

          >>> from returns.result import Failure, Success
          >>> from returns.validated import Invalid, Valid, Validated
          >>> assert Validated.from_result(Success(1)) == Valid(1)
          >>> assert Validated.from_result(Failure(1)) == Invalid((1,))

        """
        from returns.pipeline import is_successful  # noqa: PLC0415

        if is_successful(inner_value):
            return Valid(inner_value.unwrap())
        return Invalid((inner_value.failure(),))

    @classmethod
    def from_validated(
        cls,
        inner_value: 'Validated[_NewValueType, _NewErrorType]',
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Returns the same ``Validated`` instance it receives.

        .. code:: python

          >>> from returns.validated import Valid, Validated
          >>> container = Valid(1)
          >>> assert Validated.from_validated(container) is container

        """
        return inner_value

    @classmethod
    def combine(
        cls,
        first: 'Validated[_FirstType, _ErrorType_co]',
        second: 'Validated[_SecondType, _ErrorType_co]',
        function: Callable[[_FirstType, _SecondType], _NewValueType],
    ) -> 'Validated[_NewValueType, _ErrorType_co]':
        """
        Combines two containers with a binary function.

        Uses applicative ``apply``, so both failures are accumulated.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

          >>> assert Validated.combine(
          ...     Valid(1), Valid(2), lambda a, b: a + b,
          ... ) == Valid(3)
          >>> assert Validated.combine(
          ...     Invalid(('a',)), Invalid(('b',)), lambda a, b: a + b,
          ... ) == Invalid(('a', 'b'))

        """
        def factory(right: _SecondType):
            return lambda left: function(left, right)

        return first.apply(second.map(factory))

    @classmethod
    def combine_n(
        cls,
        containers: tuple['Validated[Any, _ErrorType_co]', ...],
        function: Callable[..., _NewValueType],
    ) -> 'Validated[_NewValueType, _ErrorType_co]':
        """
        Combines N containers with an N-ary function.

        Accumulates every failure in left-to-right order.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

          >>> def add3(first: int, second: int, third: int) -> int:
          ...     return first + second + third

          >>> assert Validated.combine_n(
          ...     (Valid(1), Valid(2), Valid(3)), add3,
          ... ) == Valid(6)
          >>> assert Validated.combine_n(
          ...     (Invalid(('a',)), Valid(2), Invalid(('c',))), add3,
          ... ) == Invalid(('a', 'c'))

        """
        errors: list[_ErrorType_co] = []
        collected: list[Any] = []
        for container in containers:
            if isinstance(container, Invalid):
                errors.extend(container.failure())
            else:
                collected.append(container.unwrap())
        if errors:
            return Invalid(tuple(errors))
        return Valid(function(*collected))


@final  # noqa: WPS338
class Invalid(Validated[Any, _ErrorType_co]):  # noqa: WPS338
    """
    Represents a calculation which has failed.

    Stores one or more errors as an immutable tuple.
    """

    __slots__ = ()

    _inner_value: tuple[_ErrorType_co, ...]

    def __init__(self, inner_value: tuple[_ErrorType_co, ...]) -> None:
        """Invalid constructor. ``inner_value`` is stored as a tuple."""
        super().__init__(tuple(inner_value))

    if not TYPE_CHECKING:  # noqa: WPS604  # pragma: no branch

        def alt(self, function):
            """Applies ``function`` to each individual error."""
            return Invalid(tuple(
                function(error) for error in self._inner_value
            ))

        def map(self, function):
            """Does nothing for ``Invalid``."""
            return self

        def bind(self, function):
            """Does nothing for ``Invalid``."""
            return self

        #: Alias for `bind` method. Part of the `ValidatedLikeN` interface.
        bind_validated = bind

        def lash(self, function):
            """Composes this container with a function returning container."""
            return function(self._inner_value)

        def apply(self, container):
            """Accumulates errors when the other container is also invalid."""
            if isinstance(container, Invalid):
                return Invalid(self._inner_value + container.failure())
            return self

        def value_or(self, default_value):
            """Returns default value for failed container."""
            return default_value

    def swap(self):
        """Invalid containers swap to :class:`Valid`."""
        return Valid(self._inner_value)

    def unwrap(self) -> Never:
        """Raises an exception, since it does not have a value inside."""
        raise UnwrapFailedError(self)

    def failure(self) -> tuple[_ErrorType_co, ...]:
        """Returns failed errors."""
        return self._inner_value


@final
class Valid(Validated[_ValueType_co, Any]):
    """
    Represents a calculation which has succeeded and contains the result.

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

        #: Alias for `bind` method. Part of the `ValidatedLikeN` interface.
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

    def swap(self):
        """Valid containers swap to :class:`Invalid`."""
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
    *,
    exceptions: tuple[type[_ExceptionType], ...],
) -> Callable[
    [Callable[_FuncParams, _ValueType_co]],
    Callable[_FuncParams, Validated[_ValueType_co, _ExceptionType]],
]: ...


def validated(  # noqa: WPS234
    function: Callable[_FuncParams, _ValueType_co] | None = None,
    /,
    *,
    exceptions: tuple[type[_ExceptionType], ...] = (Exception,),
) -> (
    Callable[_FuncParams, ValidatedE[_ValueType_co]]
    | Callable[
        [Callable[_FuncParams, _ValueType_co]],
        Callable[_FuncParams, Validated[_ValueType_co, _ExceptionType]],
    ]
):
    """
    Decorator to convert exception-throwing function to ``Validated``.

    Caught exceptions become ``Invalid`` with the error wrapped in a 1-tuple.

    .. code:: python

      >>> from returns.validated import Invalid, Valid, validated

      >>> @validated
      ... def might_raise(arg: int) -> float:
      ...     return 1 / arg

      >>> assert might_raise(1) == Valid(1.0)
      >>> assert isinstance(might_raise(0), Invalid)
      >>> assert might_raise.__name__ == 'might_raise'

    You can also use it with explicit exception types:

    .. code:: python

      >>> from returns.validated import Invalid, Valid, validated

      >>> @validated(exceptions=(ZeroDivisionError,))
      ... def might_raise(arg: int) -> float:
      ...     return 1 / arg

      >>> assert might_raise(1) == Valid(1.0)
      >>> assert isinstance(might_raise(0), Invalid)

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

    if function is not None:
        return factory(function, exceptions)
    return lambda inner: factory(inner, exceptions)
