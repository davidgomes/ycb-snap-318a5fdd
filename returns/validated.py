from abc import ABC
from collections.abc import Callable, Generator, Iterator
from functools import wraps
from typing import TYPE_CHECKING, Any, TypeVar, final, overload

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

_FirstValueType = TypeVar('_FirstValueType')
_SecondValueType = TypeVar('_SecondValueType')
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
    but ``.apply`` accumulates all errors instead of stopping
    at the first failure. ``.bind`` still short-circuits,
    because the next step depends on the previous value.

    :class:`~Validated` does not have a public constructor.
    Use :func:`~Valid` and :func:`~Invalid` to construct the needed values.

    See also:
        - https://typelevel.org/cats/datatypes/validated.html

    """

    __slots__ = ()
    __match_args__ = ('_inner_value',)

    _inner_value: _ValueType_co | tuple[_ErrorType_co, ...]

    #: Typesafe equality comparison with other `Validated` objects.
    equals = container_equality

    def swap(self) -> 'Validated[tuple[_ErrorType_co, ...], _ValueType_co]':
        """
        Swaps value and errors.

        A value becomes a single error, errors become a value.

        .. code:: python

          >>> from returns.validated import Invalid, Valid
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

        When both containers are failed, their errors are accumulated:
        errors of this container go first, then errors of the passed one.

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

        Short-circuits on the first failure, errors are not accumulated.

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
        Composes failed container with a pure function to modify each error.

        .. code:: python

          >>> from returns.validated import Invalid, Valid

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

          >>> from returns.validated import Invalid, Valid, Validated

          >>> def lashable(errors: tuple[str, ...]) -> Validated[int, str]:
          ...      if len(errors) > 1:
          ...          return Invalid(('too many errors',))
          ...      return Valid(0)

          >>> assert Valid(1).lash(lashable) == Valid(1)
          >>> assert Invalid(('a',)).lash(lashable) == Valid(0)
          >>> assert Invalid(('a', 'b')).lash(lashable) == Invalid(
          ...     ('too many errors',),
          ... )

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

        Just like ``.bind``, it stops at the first failure.

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
        This feature requires our :ref:`mypy plugin <mypy-plugins>`.

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
        Get a tuple of errors or raise exception.

        .. code:: pycon
          :force:

          >>> from returns.validated import Invalid, Valid
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
        One more value to create success unit values.

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
        Creates a failed container from a single error.

        The error is wrapped into a tuple, so it can be accumulated later.

        .. code:: python

          >>> from returns.validated import Invalid, Validated
          >>> assert Validated.from_failure('a') == Invalid(('a',))

        """
        return Invalid((inner_value,))

    @classmethod
    def from_validated(
        cls,
        inner_value: 'Validated[_NewValueType, _NewErrorType]',
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Creates a new ``Validated`` instance from existing ``Validated``.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated
          >>> container = Valid(1)
          >>> assert Validated.from_validated(container) is container

        This is a part of
        :class:`returns.interfaces.specific.validated.ValidatedBasedN`
        interface.
        """
        return inner_value

    @classmethod
    def from_result(
        cls,
        inner_value: Result[_NewValueType, _NewErrorType],
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Creates a new ``Validated`` instance from ``Result``.

        ``Failure`` error is wrapped into a tuple.

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
    def combine(
        cls,
        first: 'Validated[_FirstValueType, _NewErrorType]',
        second: 'Validated[_SecondValueType, _NewErrorType]',
        function: Callable[[_FirstValueType, _SecondValueType], _NewValueType],
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Combines two containers with a binary function.

        Errors from both containers are accumulated in order.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

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
        return cls.combine_n((first, second), function)

    @classmethod
    def combine_n(
        cls,
        containers: tuple['Validated[Any, _NewErrorType]', ...],
        function: Callable[..., _NewValueType],
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Combines any number of containers with an N-ary function.

        Errors from all containers are accumulated in order.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

          >>> def join(*args: str) -> str:
          ...     return ''.join(args)

          >>> assert Validated.combine_n(
          ...     (Valid('a'), Valid('b'), Valid('c')), join,
          ... ) == Valid('abc')
          >>> assert Validated.combine_n(
          ...     (Invalid((1,)), Valid('b'), Invalid((2, 3))), join,
          ... ) == Invalid((1, 2, 3))

        """
        acc: Validated[tuple[Any, ...], _NewErrorType] = Valid(())
        for container in containers:
            acc = acc.apply(container.map(_concat_value))
        return acc.map(lambda collected: function(*collected))


@final
class Invalid(Validated[Any, _ErrorType_co]):
    """
    Represents a validation which has failed.

    Contains a tuple of all collected errors.
    """

    __slots__ = ()

    _inner_value: tuple[_ErrorType_co, ...]

    def __init__(self, inner_value: tuple[_ErrorType_co, ...]) -> None:
        """Invalid constructor."""
        super().__init__(tuple(inner_value))

    if not TYPE_CHECKING:  # noqa: WPS604  # pragma: no branch

        def alt(self, function):
            """Composes each error with a pure function."""
            return Invalid(
                tuple(function(error) for error in self._inner_value),
            )

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
            """Accumulates errors from both containers."""
            if isinstance(container, Invalid):
                return Invalid(self._inner_value + container.failure())
            return self

        def value_or(self, default_value):
            """Returns default value for failed container."""
            return default_value

        def swap(self):
            """Errors swap to :class:`Valid`."""
            return Valid(self._inner_value)

    def unwrap(self) -> Never:
        """Raises an exception, since it does not have a value inside."""
        raise UnwrapFailedError(self)

    def failure(self) -> tuple[_ErrorType_co, ...]:
        """Returns a tuple of errors."""
        return self._inner_value


@final
class Valid(Validated[_ValueType_co, Any]):
    """
    Represents a validation which has succeeded and contains the value.

    Quite similar to ``Success`` type.
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

        def swap(self):
            """Value swaps to :class:`Invalid` with a single error."""
            return Invalid((self._inner_value,))

    def unwrap(self) -> _ValueType_co:
        """Returns the unwrapped value from successful container."""
        return self._inner_value

    def failure(self) -> Never:
        """Raises an exception for successful container."""
        raise UnwrapFailedError(self)


def _concat_value(
    current: _NewValueType,
) -> Callable[[tuple[Any, ...]], tuple[Any, ...]]:
    return lambda collected: (*collected, current)


# Decorators:

_ExceptionType = TypeVar('_ExceptionType', bound=Exception)


@overload
def validated(
    function: Callable[_FuncParams, _ValueType_co],
    /,
) -> Callable[_FuncParams, Validated[_ValueType_co, Exception]]: ...


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
    Callable[_FuncParams, Validated[_ValueType_co, Exception]]
    | Callable[
        [Callable[_FuncParams, _ValueType_co]],
        Callable[_FuncParams, Validated[_ValueType_co, _ExceptionType]],
    ]
):
    """
    Decorator to convert exception-throwing function to ``Validated`` container.

    Caught exception is wrapped into a single-error ``Invalid`` container.
    Should be used with care, since it only catches ``Exception`` subclasses.
    It does not catch ``BaseException`` subclasses.

    .. code:: python

      >>> from returns.validated import Invalid, Valid, validated

      >>> @validated
      ... def might_raise(arg: int) -> float:
      ...     return 1 / arg

      >>> assert might_raise(1) == Valid(1.0)
      >>> assert isinstance(might_raise(0), Invalid)

    You can also use it with explicit exception types as the first argument:

    .. code:: python

      >>> from returns.validated import Invalid, Valid, validated

      >>> @validated(exceptions=(ZeroDivisionError,))
      ... def might_raise(arg: int) -> float:
      ...     return 1 / arg

      >>> assert might_raise(1) == Valid(1.0)
      >>> assert isinstance(might_raise(0), Invalid)

    In this case, only exceptions that are explicitly
    listed are going to be caught.

    Similar to :func:`returns.result.safe` decorator.
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
