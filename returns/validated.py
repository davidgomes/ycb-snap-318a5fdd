from abc import ABC
from collections.abc import Callable, Generator, Iterator
from functools import partial, wraps
from typing import TYPE_CHECKING, Any, TypeVar, final, overload

from typing_extensions import Never, ParamSpec

from returns.interfaces.specific import validated as validated_interface
from returns.primitives.container import BaseContainer, container_equality
from returns.primitives.exceptions import UnwrapFailedError
from returns.primitives.hkt import Kind2, SupportsKind2
from returns.result import Result, Success

# Definitions:
_ValueType_co = TypeVar('_ValueType_co', covariant=True)
_NewValueType = TypeVar('_NewValueType')
_ErrorType_co = TypeVar('_ErrorType_co', covariant=True)
_NewErrorType = TypeVar('_NewErrorType')
_UpdatedType = TypeVar('_UpdatedType')

_FirstType = TypeVar('_FirstType')
_FuncParams = ParamSpec('_FuncParams')


def _supply_argument(
    argument: Any,
) -> Callable[[Callable[..., Any]], Callable[..., Any]]:
    """Return a function that supplies ``argument`` as the next argument."""

    def factory(function: Callable[..., Any]) -> Callable[..., Any]:
        return partial(function, argument)

    return factory


def _call_supplied(function: Callable[..., _NewValueType]) -> _NewValueType:
    """Call a fully supplied function produced by :func:`_supply_argument`."""
    return function()


class Validated(  # type: ignore[type-var]
    BaseContainer,
    SupportsKind2['Validated', _ValueType_co, _ErrorType_co],
    validated_interface.ValidatedBased2[_ValueType_co, _ErrorType_co],
    ABC,
):
    """
    Error-accumulating container with ``Valid`` and ``Invalid`` variants.

    Use :class:`~Valid` and :class:`~Invalid` to construct values.
    ``Invalid`` stores an immutable tuple of errors. Independent checks
    combine through :meth:`~Validated.apply`, :meth:`~Validated.combine`,
    and :meth:`~Validated.combine_n`, which keep every error.
    :meth:`~Validated.bind` still short-circuits on the first failure.

    The second type argument is one error element. A failed container holds
    ``tuple[Error, ...]``.
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
          >>> assert Invalid((1,)).map(mappable) == Invalid((1,))

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

        Two ``Invalid`` containers concatenate errors from left to right:
        this container's errors, then the function container's errors.

        .. code:: python

          >>> from returns.validated import Invalid, Valid

          >>> assert Valid(1).apply(
          ...     Valid(lambda number: number + 1),
          ... ) == Valid(2)
          >>> assert Valid(1).apply(Invalid(('e',))) == Invalid(('e',))
          >>> assert Invalid((1,)).apply(Valid(str)) == Invalid((1,))
          >>> assert Invalid((1, 2)).apply(
          ...     Invalid((3,)),
          ... ) == Invalid((1, 2, 3))

        """

    def bind(
        self,
        function: Callable[
            [_ValueType_co],
            Kind2['Validated', _NewValueType, _ErrorType_co],
        ],
    ) -> 'Validated[_NewValueType, _ErrorType_co]':
        """
        Composes a successful container with a function returning a container.

        Failures are returned unchanged and the function is not called.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

          >>> def bindable(number: int) -> Validated[int, str]:
          ...     if number > 0:
          ...         return Valid(number + 1)
          ...     return Invalid(('negative',))

          >>> assert Valid(1).bind(bindable) == Valid(2)
          >>> assert Valid(0).bind(bindable) == Invalid(('negative',))
          >>> assert Invalid(('kept',)).bind(bindable) == Invalid(('kept',))

        """

    #: Alias for ``bind``. Part of the ``ValidatedBasedN`` interface.
    bind_validated = bind

    def alt(
        self,
        function: Callable[[_ErrorType_co], _NewErrorType],
    ) -> 'Validated[_ValueType_co, _NewErrorType]':
        """
        Maps each error element of a failed container.

        Successful containers are returned unchanged.

        .. code:: python

          >>> from returns.validated import Invalid, Valid

          >>> assert Valid(1).alt(str) == Valid(1)
          >>> assert Invalid((1, 2)).alt(str) == Invalid(('1', '2'))

        """

    def lash(  # type: ignore[override]
        self,
        function: Callable[
            [tuple[_ErrorType_co, ...]],
            Kind2['Validated', _ValueType_co, _NewErrorType],
        ],
    ) -> 'Validated[_ValueType_co, _NewErrorType]':
        """
        Composes a failed container with a function returning a container.

        The function receives the whole error tuple.
        Successful containers are returned unchanged.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

          >>> def lashable(
          ...     errors: tuple[str, ...],
          ... ) -> Validated[str, str]:
          ...     return Valid(','.join(errors))

          >>> assert Valid('ok').lash(lashable) == Valid('ok')
          >>> assert Invalid(('a', 'b')).lash(lashable) == Valid('a,b')

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

        Do-notation uses ``bind`` and stops at the first failure.

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
        Get the success value or a default.

        .. code:: python

          >>> from returns.validated import Invalid, Valid

          >>> assert Valid(1).value_or(0) == 1
          >>> assert Invalid((1,)).value_or(0) == 0

        """

    def unwrap(self) -> _ValueType_co:
        """
        Get the success value or raise exception.

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
        Get the error tuple or raise exception.

        .. code:: pycon
          :force:

          >>> from returns.validated import Invalid, Valid
          >>> assert Invalid((1, 2)).failure() == (1, 2)

          >>> Valid(1).failure()
          Traceback (most recent call last):
            ...
          returns.primitives.exceptions.UnwrapFailedError

        """

    def swap(self) -> 'Validated[Any, Any]':
        """
        Swaps success and failure, wrapping a success value in a 1-tuple.

        ``Valid(x)`` becomes ``Invalid((x,))``.
        ``Invalid(errors)`` becomes ``Valid(errors)`` and keeps the tuple.

        Swapping twice is not the identity, because the success value is
        wrapped on the way through ``Invalid``.

        .. code:: python

          >>> from returns.validated import Invalid, Valid

          >>> assert Valid(1).swap() == Invalid((1,))
          >>> assert Invalid((1, 2)).swap() == Valid((1, 2))
          >>> assert Valid(1).swap().swap() == Valid((1,))

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
        Create a failed container from a single error element.

        The error is wrapped in a 1-tuple so later accumulation is uniform.

        .. code:: python

          >>> from returns.validated import Invalid, Validated
          >>> assert Validated.from_failure('e') == Invalid(('e',))
          >>> assert Validated.from_failure((1, 2)).failure() == ((1, 2),)

        """
        return Invalid((inner_value,))

    @classmethod
    def from_result(
        cls,
        inner_value: Result[_NewValueType, _NewErrorType],
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Convert ``Result`` into ``Validated``.

        ``Success`` becomes ``Valid``.
        ``Failure`` becomes ``Invalid`` with the error wrapped in a 1-tuple.

        .. code:: python

          >>> from returns.result import Failure, Success
          >>> from returns.validated import Invalid, Valid, Validated

          >>> assert Validated.from_result(Success(1)) == Valid(1)
          >>> assert Validated.from_result(Failure('e')) == Invalid(('e',))

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

          >>> from returns.validated import Invalid, Valid, Validated

          >>> valid = Valid(1)
          >>> invalid = Invalid(('e',))
          >>> assert Validated.from_validated(valid) is valid
          >>> assert Validated.from_validated(invalid) is invalid

        """
        return inner_value

    @classmethod
    def combine(
        cls,
        first: 'Validated[_FirstType, _NewErrorType]',
        second: 'Validated[_NewValueType, _NewErrorType]',
        function: Callable[[_FirstType, _NewValueType], _UpdatedType],
    ) -> 'Validated[_UpdatedType, _NewErrorType]':
        """
        Combine two containers with a binary function.

        Uses applicative combination. Errors accumulate left to right.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

          >>> def add(left: int, right: int) -> int:
          ...     return left + right

          >>> assert Validated.combine(Valid(2), Valid(3), add) == Valid(5)
          >>> assert Validated.combine(
          ...     Invalid(('a',)), Invalid(('b',)), add,
          ... ) == Invalid(('a', 'b'))
          >>> assert Validated.combine(
          ...     Valid(1), Invalid(('b',)), add,
          ... ) == Invalid(('b',))
          >>> assert Validated.combine(
          ...     Invalid(('a',)), Valid(1), add,
          ... ) == Invalid(('a',))

        """
        return cls.combine_n((first, second), function)

    @classmethod
    def combine_n(
        cls,
        containers: tuple['Validated[Any, _NewErrorType]', ...],
        function: Callable[..., _NewValueType],
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Combine ``N`` containers with an ``N``-ary function.

        Every failed container contributes its errors, from left to right.
        The function runs only when every container is successful.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

          >>> assert Validated.combine_n((), lambda: 1) == Valid(1)
          >>> assert Validated.combine_n(
          ...     (Valid(1), Valid(2), Valid(3)),
          ...     lambda left, middle, right: left + middle + right,
          ... ) == Valid(6)
          >>> assert Validated.combine_n(
          ...     (Invalid(('a',)), Valid(2), Invalid(('b', 'c'))),
          ...     lambda left, middle, right: left,
          ... ) == Invalid(('a', 'b', 'c'))

        """
        accumulated: Validated[Any, _NewErrorType] = cls.from_value(function)
        for container in containers:
            accumulated = accumulated.apply(container.map(_supply_argument))
        return accumulated.map(_call_supplied)


@final
class Invalid(Validated[Any, _ErrorType_co]):
    """Failed validation. Stores an immutable tuple of error elements."""

    __slots__ = ()

    _inner_value: tuple[_ErrorType_co, ...]

    def __init__(self, inner_value: tuple[_ErrorType_co, ...]) -> None:
        """Normalize ``inner_value`` into a tuple of errors."""
        super().__init__(tuple(inner_value))

    if not TYPE_CHECKING:  # noqa: WPS604  # pragma: no branch

        def alt(self, function):
            """Map ``function`` over each error element."""
            return Invalid(
                tuple(function(error) for error in self._inner_value),
            )

        def map(self, function):
            """Does nothing for ``Invalid``."""
            return self

        def bind(self, function):
            """Does nothing for ``Invalid``."""
            return self

        #: Alias for ``bind``. Part of the ``ValidatedBasedN`` interface.
        bind_validated = bind

        def lash(self, function):
            """Call ``function`` with the error tuple."""
            return function(self._inner_value)

        def apply(self, container):
            """Keep these errors, appending the other failure's errors."""
            if isinstance(container, Invalid):
                return Invalid(self._inner_value + container.failure())
            return self

        def value_or(self, default_value):
            """Return the default for a failed container."""
            return default_value

    def swap(self):
        """Failures swap to :class:`Valid`, keeping the error tuple."""
        return Valid(self._inner_value)

    def unwrap(self) -> Never:
        """Raise an exception, since there is no success value inside."""
        single_error = (
            self._inner_value[0] if len(self._inner_value) == 1 else None
        )
        if isinstance(single_error, Exception):
            raise UnwrapFailedError(self) from single_error
        raise UnwrapFailedError(self)

    def failure(self) -> tuple[_ErrorType_co, ...]:
        """Return the error tuple."""
        return self._inner_value


@final
class Valid(Validated[_ValueType_co, Any]):
    """Successful validation. Contains the computed value."""

    __slots__ = ()

    _inner_value: _ValueType_co

    def __init__(self, inner_value: _ValueType_co) -> None:
        """Wrap a successful value."""
        super().__init__(inner_value)

    if not TYPE_CHECKING:  # noqa: WPS604  # pragma: no branch

        def alt(self, function):
            """Does nothing for ``Valid``."""
            return self

        def map(self, function):
            """Compose the current value with a pure function."""
            return Valid(function(self._inner_value))

        def bind(self, function):
            """Bind the current value to a function returning a container."""
            return function(self._inner_value)

        #: Alias for ``bind``. Part of the ``ValidatedBasedN`` interface.
        bind_validated = bind

        def lash(self, function):
            """Does nothing for ``Valid``."""
            return self

        def apply(self, container):
            """Call a wrapped function, or return the other failure."""
            if isinstance(container, Valid):
                return self.map(container.unwrap())
            return container

        def value_or(self, default_value):
            """Return the successful value."""
            return self._inner_value

    def swap(self):
        """Successes swap to :class:`Invalid` with a 1-tuple."""
        return Invalid((self._inner_value,))

    def unwrap(self) -> _ValueType_co:
        """Return the unwrapped success value."""
        return self._inner_value

    def failure(self) -> Never:
        """Raise an exception for a successful container."""
        raise UnwrapFailedError(self)


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
        Callable[_FuncParams, _ValueType_co]
        | tuple[type[_ExceptionType], ...]
    ),
) -> (
    Callable[_FuncParams, Validated[_ValueType_co, Exception]]
    | Callable[
        [Callable[_FuncParams, _ValueType_co]],
        Callable[_FuncParams, Validated[_ValueType_co, _ExceptionType]],
    ]
):
    """
    Decorator to convert an exception-throwing function into ``Validated``.

    Caught exceptions are stored as ``Invalid((exception,))``.
    The wrapped function's name is preserved.

    Only ``Exception`` subclasses are caught by default.
    ``BaseException`` subclasses are not caught.

    .. code:: python

      >>> from returns.validated import Invalid, Valid, validated

      >>> @validated
      ... def might_raise(arg: int) -> float:
      ...     return 1 / arg

      >>> assert might_raise.__name__ == 'might_raise'
      >>> assert might_raise(1) == Valid(1.0)
      >>> assert isinstance(might_raise(0), Invalid)

    Explicit exception types are passed with ``exceptions``:

    .. code:: python

      >>> from returns.validated import Invalid, Valid, validated

      >>> @validated(exceptions=(ZeroDivisionError,))
      ... def might_raise(arg: int) -> float:
      ...     return 1 / arg

      >>> assert might_raise.__name__ == 'might_raise'
      >>> assert might_raise(1) == Valid(1.0)
      >>> failure = might_raise(0)
      >>> assert isinstance(failure, Invalid)
      >>> assert isinstance(failure.failure()[0], ZeroDivisionError)

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
