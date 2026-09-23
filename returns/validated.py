from __future__ import annotations

from abc import ABC
from collections.abc import Callable, Generator, Iterator
from functools import wraps
from typing import TYPE_CHECKING, Any, TypeVar, final, overload

from typing_extensions import Never, ParamSpec

from returns.interfaces.specific import validated as validated_interface
from returns.primitives.container import BaseContainer, container_equality
from returns.primitives.exceptions import UnwrapFailedError
from returns.primitives.hkt import Kind2, SupportsKind2

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

    ``Validated`` collects every independent error instead of stopping at
    the first failure. Use :meth:`~Validated.apply` (or :meth:`combine`)
    when inputs are independent. Use :meth:`~Validated.bind` when each
    step depends on the previous success: binding still short-circuits.

    :class:`~Validated` does not have a public constructor.
    Use :func:`~Valid`, :func:`~Invalid`, :meth:`~Validated.from_value`,
    or :meth:`~Validated.from_failure`.

    .. code:: python

      >>> from returns.validated import Invalid, Valid, Validated

      >>> assert Validated.from_value(1) == Valid(1)
      >>> assert Validated.from_failure('e') == Invalid(('e',))

    """

    __slots__ = ()
    __match_args__ = ('_inner_value',)

    _inner_value: _ValueType_co | tuple[_ErrorType_co, ...]

    #: Typesafe equality comparison with other ``Validated`` objects.
    equals = container_equality

    def map(
        self,
        function: Callable[[_ValueType_co], _NewValueType],
    ) -> Validated[_NewValueType, _ErrorType_co]:
        """
        Composes a successful container with a pure function.

        .. code:: python

          >>> from returns.validated import Invalid, Valid

          >>> def mappable(string: str) -> str:
          ...     return string + 'b'

          >>> assert Valid('a').map(mappable) == Valid('ab')
          >>> assert Invalid(('a',)).map(mappable) == Invalid(('a',))

        """

    def apply(
        self,
        container: Kind2[
            Validated,
            Callable[[_ValueType_co], _NewValueType],
            _ErrorType_co,
        ],
    ) -> Validated[_NewValueType, _ErrorType_co]:
        """
        Calls a wrapped function on this container.

        Two failed containers concatenate their errors, self's first.

        .. code:: python

          >>> from returns.validated import Invalid, Valid

          >>> def appliable(string: str) -> str:
          ...     return string + 'b'

          >>> assert Valid('a').apply(Valid(appliable)) == Valid('ab')
          >>> assert Invalid(('a',)).apply(
          ...     Valid(appliable),
          ... ) == Invalid(('a',))
          >>> assert Valid('a').apply(Invalid((1,))) == Invalid((1,))
          >>> assert Invalid((1,)).apply(Invalid((2,))) == Invalid((1, 2))

        """

    def bind(
        self,
        function: Callable[
            [_ValueType_co],
            Kind2[Validated, _NewValueType, _ErrorType_co],
        ],
    ) -> Validated[_NewValueType, _ErrorType_co]:
        """
        Composes a successful container with a function returning a container.

        Failed containers short-circuit and keep their errors.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

          >>> def bindable(arg: str) -> Validated[str, str]:
          ...     if len(arg) > 1:
          ...         return Valid(arg + 'b')
          ...     return Invalid((arg + 'c',))

          >>> assert Valid('aa').bind(bindable) == Valid('aab')
          >>> assert Valid('a').bind(bindable) == Invalid(('ac',))
          >>> assert Invalid(('a',)).bind(bindable) == Invalid(('a',))

        """

    #: Alias for :meth:`~Validated.bind_validated`. Same as ``bind`` here.
    bind_validated = bind

    def alt(
        self,
        function: Callable[[_ErrorType_co], _NewErrorType],
    ) -> Validated[_ValueType_co, _NewErrorType]:
        """
        Maps a function over each error in a failed container.

        .. code:: python

          >>> from returns.validated import Invalid, Valid

          >>> def altable(arg: str) -> str:
          ...     return arg + 'b'

          >>> assert Valid('a').alt(altable) == Valid('a')
          >>> assert Invalid(('a', 'c')).alt(altable) == Invalid((
          ...     'ab', 'cb',
          ... ))

        """

    def lash(  # type: ignore[override]
        self,
        function: Callable[
            [tuple[_ErrorType_co, ...]],
            Kind2[Validated, _ValueType_co, _NewErrorType],
        ],
    ) -> Validated[_ValueType_co, _NewErrorType]:
        """
        Composes a failed container with a function returning a container.

        The function receives the whole error tuple.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

          >>> def lashable(
          ...     errors: tuple[str, ...],
          ... ) -> Validated[str, str]:
          ...     if len(errors) > 1:
          ...         return Valid('recovered')
          ...     return Invalid(errors)

          >>> assert Valid('a').lash(lashable) == Valid('a')
          >>> assert Invalid(('a',)).lash(lashable) == Invalid(('a',))
          >>> assert Invalid(('a', 'b')).lash(lashable) == Valid('recovered')

        """

    def __iter__(self) -> Iterator[_ValueType_co]:
        """API for :ref:`do-notation`."""
        yield self.unwrap()

    @classmethod
    def do(
        cls,
        expr: Generator[_NewValueType, None, None],
    ) -> Validated[_NewValueType, _NewErrorType]:
        """
        Unwraps containers inside a generator expression.

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
          >>> assert Valid(1).value_or(2) == 1
          >>> assert Invalid((1,)).value_or(2) == 2

        """

    def unwrap(self) -> _ValueType_co:
        """
        Get the success value or raise an exception.

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
        Get the error tuple or raise an exception.

        .. code:: pycon
          :force:

          >>> from returns.validated import Invalid, Valid
          >>> assert Invalid((1,)).failure() == (1,)

          >>> Valid(1).failure()
          Traceback (most recent call last):
            ...
          returns.primitives.exceptions.UnwrapFailedError

        """

    def swap(self) -> Validated[tuple[_ErrorType_co, ...], _ValueType_co]:
        """
        Swaps success values and error tuples.

        A success becomes a one-element failure.
        A failure's error tuple becomes the success value.

        .. code:: python

          >>> from returns.validated import Invalid, Valid
          >>> assert Valid(1).swap() == Invalid((1,))
          >>> assert Invalid((1, 2)).swap() == Valid((1, 2))

        """

    @classmethod
    def from_value(
        cls,
        inner_value: _NewValueType,
    ) -> Validated[_NewValueType, Any]:
        """
        Creates a successful container from a raw value.

        .. code:: python

          >>> from returns.validated import Valid, Validated
          >>> assert Validated.from_value(1) == Valid(1)

        """
        return Valid(inner_value)

    @classmethod
    def from_failure(
        cls,
        inner_value: _NewErrorType,
    ) -> Validated[Any, _NewErrorType]:
        """
        Creates a failed container from a single error.

        The error is wrapped into a one-element tuple so concatenation
        always works on tuples.

        .. code:: python

          >>> from returns.validated import Invalid, Validated
          >>> assert Validated.from_failure(1) == Invalid((1,))

        """
        return Invalid((inner_value,))

    @classmethod
    def from_validated(
        cls,
        inner_value: Validated[_NewValueType, _NewErrorType],
    ) -> Validated[_NewValueType, _NewErrorType]:
        """
        Returns the same ``Validated`` instance it receives.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated
          >>> success = Valid(1)
          >>> failure = Invalid((1,))
          >>> assert Validated.from_validated(success) is success
          >>> assert Validated.from_validated(failure) is failure

        """
        return inner_value

    @classmethod
    def from_result(
        cls,
        inner_value: Result[_NewValueType, _NewErrorType],
    ) -> Validated[_NewValueType, _NewErrorType]:
        """
        Converts ``Result`` into ``Validated``.

        .. code:: python

          >>> from returns.result import Failure, Success
          >>> from returns.validated import Invalid, Valid, Validated

          >>> assert Validated.from_result(Success(1)) == Valid(1)
          >>> assert Validated.from_result(Failure(1)) == Invalid((1,))

        """
        from returns.result import Success  # noqa: PLC0415

        if isinstance(inner_value, Success):
            return Valid(inner_value.unwrap())
        return Invalid((inner_value.failure(),))

    @classmethod
    def combine(
        cls,
        first: Validated[_FirstType, _ErrorType_co],
        second: Validated[_SecondType, _ErrorType_co],
        function: Callable[[_FirstType, _SecondType], _NewValueType],
    ) -> Validated[_NewValueType, _ErrorType_co]:
        """
        Combines two containers with a binary function.

        Errors of both failed containers are accumulated left to right.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

          >>> def add(first: int, second: int) -> int:
          ...     return first + second

          >>> assert Validated.combine(Valid(1), Valid(2), add) == Valid(3)
          >>> assert Validated.combine(
          ...     Invalid(('a',)), Invalid(('b',)), add,
          ... ) == Invalid(('a', 'b'))
          >>> assert Validated.combine(
          ...     Invalid(('a',)), Valid(2), add,
          ... ) == Invalid(('a',))

        """
        return cls.combine_n((first, second), function)

    @classmethod
    def combine_n(
        cls,
        containers: tuple[Validated[Any, _ErrorType_co], ...],
        function: Callable[..., _NewValueType],
    ) -> Validated[_NewValueType, _ErrorType_co]:
        """
        Combines ``N`` containers with an ``N``-ary function.

        Failures accumulate in left-to-right order.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

          >>> assert Validated.combine_n(
          ...     (Valid(1), Valid(2), Valid(3)),
          ...     lambda first, second, third: first + second + third,
          ... ) == Valid(6)
          >>> assert Validated.combine_n(
          ...     (Invalid(('a',)), Valid(2), Invalid(('b',))),
          ...     lambda first, second, third: first,
          ... ) == Invalid(('a', 'b'))
          >>> assert Validated.combine_n(
          ...     (Valid(1),), lambda value: value,
          ... ) == Valid(1)

        """
        pending = list(containers)
        current: Validated[tuple[Any, ...], _ErrorType_co] = pending.pop().map(
            lambda inner: (inner,),
        )
        while pending:
            container = pending.pop()
            current = container.apply(current.map(_prepend))
        return current.map(lambda args: function(*args))


def _prepend(
    tail: tuple[Any, ...],
) -> Callable[[Any], tuple[Any, ...]]:
    """Returns a function that prepends one value to an accumulated tuple."""
    return lambda head: (head, *tail)


@final
class Invalid(Validated[Any, _ErrorType_co]):
    """A calculation that failed and collected one or more errors."""

    __slots__ = ()

    _inner_value: tuple[_ErrorType_co, ...]

    def __init__(self, inner_value: tuple[_ErrorType_co, ...]) -> None:
        """Invalid constructor. Stores errors as an immutable tuple."""
        super().__init__(tuple(inner_value))

    if not TYPE_CHECKING:  # noqa: WPS604  # pragma: no branch

        def alt(self, function):
            """Maps ``function`` over each error in the tuple."""
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
            """Composes this container with a function returning one."""
            return function(self._inner_value)

        def apply(self, container):
            """Concatenates errors when both containers failed."""
            if isinstance(container, Invalid):
                return Invalid(self._inner_value + container.failure())
            return self

        def value_or(self, default_value):
            """Returns the default value for a failed container."""
            return default_value

    def swap(self) -> Valid[tuple[_ErrorType_co, ...]]:
        """Failures swap to :class:`~Valid` holding the error tuple."""
        return Valid(self._inner_value)

    def unwrap(self) -> Never:
        """Raises an exception, since there is no success value inside."""
        raise UnwrapFailedError(self)

    def failure(self) -> tuple[_ErrorType_co, ...]:
        """Returns the error tuple."""
        return self._inner_value


@final
class Valid(Validated[_ValueType_co, Any]):
    """Represents a calculation which succeeded and contains the result."""

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
            """Binds this container to a function returning a container."""
            return function(self._inner_value)

        #: Alias for ``bind``. Part of the ``ValidatedBasedN`` interface.
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
            """Returns the value for a successful container."""
            return self._inner_value

    def swap(self) -> Invalid[_ValueType_co]:
        """Successes swap to :class:`~Invalid` with a one-element tuple."""
        return Invalid((self._inner_value,))

    def unwrap(self) -> _ValueType_co:
        """Returns the unwrapped value from a successful container."""
        return self._inner_value

    def failure(self) -> Never:
        """Raises an exception for a successful container."""
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

    Should be used with care, since it only catches ``Exception`` subclasses.
    It does not catch ``BaseException`` subclasses.

    Caught exceptions are stored as a one-element error tuple.
    The wrapped function's name is preserved.

    .. code:: python

      >>> from returns.validated import Invalid, Valid, validated

      >>> @validated
      ... def might_raise(arg: int) -> float:
      ...     return 1 / arg

      >>> assert might_raise.__name__ == 'might_raise'
      >>> assert might_raise(1) == Valid(1.0)
      >>> assert isinstance(might_raise(0), Invalid)

    You can also pass explicit exception types:

    .. code:: python

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

    if isinstance(exceptions, tuple):
        return lambda function: factory(function, exceptions)
    return factory(
        exceptions,
        (Exception,),  # type: ignore[arg-type]
    )


if TYPE_CHECKING:
    from returns.result import Result  # noqa: WPS433
