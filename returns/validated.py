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
_UpdatedType = TypeVar('_UpdatedType')

_FirstType = TypeVar('_FirstType')
_FuncParams = ParamSpec('_FuncParams')


def _concat_sequence(
    first: Any,
) -> Callable[[tuple[Any, ...]], tuple[Any, ...]]:
    """Append one successful value to an accumulated tuple."""
    return lambda second: (*second, first)


def _apply_to_left(
    function: Callable[[_FirstType, _NewValueType], _UpdatedType],
    right: _NewValueType,
) -> Callable[[_FirstType], _UpdatedType]:
    """Bind the right argument of a binary function."""
    return lambda left: function(left, right)


class Validated(  # type: ignore[type-var]
    BaseContainer,
    SupportsKind2['Validated', _ValueType_co, _ErrorType_co],
    ValidatedBased2[_ValueType_co, _ErrorType_co],
    ABC,
):
    """
    Base class for :class:`~Valid` and :class:`~Invalid`.

    :class:`~Validated` does not have a public constructor.
    Use :func:`~Valid` and :func:`~Invalid` to construct values.

    ``Validated`` collects every independent failure when values are combined
    with :meth:`~Validated.apply`, :meth:`~Validated.combine`,
    or :meth:`~Validated.combine_n`.
    :meth:`~Validated.bind` still short-circuits on the first failure,
    same as :class:`~returns.result.Result`.

    Invalid values store errors in an immutable tuple.
    :meth:`~Validated.from_failure` wraps a single error into a 1-tuple
    so accumulation is uniform.
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
        Composes successful container with a pure function.

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
            'Validated',
            Callable[[_ValueType_co], _NewValueType],
            _ErrorType_co,
        ],
    ) -> 'Validated[_NewValueType, _ErrorType_co]':
        """
        Calls a wrapped function in a container on this container.

        Two ``Invalid`` containers accumulate errors left to right:
        this container's errors are concatenated with the other container's
        errors.

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
            Kind2['Validated', _NewValueType, _ErrorType_co],
        ],
    ) -> 'Validated[_NewValueType, _ErrorType_co]':
        """
        Composes successful container with a function that returns a container.

        Failures short-circuit: the function is not called
        and errors are not accumulated.

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

    #: Alias for ``bind`` method. Part of the ``ValidatedBasedN`` interface.
    bind_validated = bind

    def alt(
        self,
        function: Callable[[_ErrorType_co], _NewErrorType],
    ) -> 'Validated[_ValueType_co, _NewErrorType]':
        """
        Composes failed container with a pure function to modify each error.

        The function is applied to each element of the error tuple.

        .. code:: python

          >>> from returns.validated import Invalid, Valid

          >>> def altable(arg: str) -> str:
          ...     return arg + 'b'

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

        The function receives the whole error tuple,
        the same value :meth:`~Validated.failure` returns.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

          >>> def lashable(
          ...     errors: tuple[str, ...],
          ... ) -> Validated[str, str]:
          ...     if len(errors) > 1:
          ...         return Valid('recovered')
          ...     return Invalid(errors + ('c',))

          >>> assert Valid('a').lash(lashable) == Valid('a')
          >>> assert Invalid(('a',)).lash(lashable) == Invalid(('a', 'c'))
          >>> assert Invalid(('a', 'b')).lash(lashable) == Valid('recovered')

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

        Do-notation uses :meth:`~Validated.bind` semantics:
        the first failure stops the expression.
        Use :meth:`~Validated.combine` when every error must be kept.

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
          ...     for second in Invalid(('b',))
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

    @classmethod
    def from_value(
        cls,
        inner_value: _NewValueType,
    ) -> 'Validated[_NewValueType, Any]':
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
    ) -> 'Validated[Any, _NewErrorType]':
        """
        Creates a failed container from a single error.

        The error is wrapped into a one-element tuple so later ``apply``
        calls can concatenate error tuples uniformly.

        .. code:: python

          >>> from returns.validated import Invalid, Validated
          >>> assert Validated.from_failure('err') == Invalid(('err',))

        """
        return Invalid((inner_value,))

    @classmethod
    def from_validated(
        cls,
        inner_value: 'Validated[_NewValueType, _NewErrorType]',
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Creates a new ``Validated`` from an existing ``Validated``.

        Returns the same instance it receives.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated
          >>> valid = Valid(1)
          >>> invalid = Invalid(('err',))
          >>> assert Validated.from_validated(valid) is valid
          >>> assert Validated.from_validated(invalid) is invalid

        """
        return inner_value

    @classmethod
    def from_result(
        cls,
        inner_value: Result[_NewValueType, _NewErrorType],
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Creates a ``Validated`` from a ``Result``.

        ``Success`` becomes ``Valid``.
        ``Failure``'s error is wrapped into a one-element tuple
        and becomes ``Invalid``.

        .. code:: python

          >>> from returns.result import Failure, Success
          >>> from returns.validated import Invalid, Valid, Validated

          >>> assert Validated.from_result(Success(1)) == Valid(1)
          >>> assert Validated.from_result(Failure('err')) == Invalid(('err',))

        """
        if isinstance(inner_value, Success):
            return Valid(inner_value.unwrap())
        return Invalid((inner_value.failure(),))

    @classmethod
    def combine(
        cls,
        first: 'Validated[_FirstType, _NewErrorType]',
        second: 'Validated[_NewValueType, _NewErrorType]',
        function: Callable[[_FirstType, _NewValueType], _UpdatedType],
    ) -> 'Validated[_UpdatedType, _NewErrorType]':
        """
        Combines two containers with a binary function.

        Uses applicative combination, so errors from both sides accumulate
        in left-to-right order. The function runs only when both are valid.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

          >>> def add(left: int, right: int) -> int:
          ...     return left + right

          >>> assert Validated.combine(Valid(2), Valid(3), add) == Valid(5)
          >>> assert Validated.combine(
          ...     Invalid(('a',)),
          ...     Invalid(('b',)),
          ...     add,
          ... ) == Invalid(('a', 'b'))
          >>> assert Validated.combine(
          ...     Invalid(('a',)),
          ...     Valid(1),
          ...     add,
          ... ) == Invalid(('a',))

        """
        return first.apply(
            second.map(lambda right: _apply_to_left(function, right)),
        )

    @classmethod
    def combine_n(
        cls,
        containers: tuple['Validated[Any, _NewErrorType]', ...],
        function: Callable[..., _NewValueType],
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Combines ``N`` containers with an ``N``-ary function.

        Errors from every failed container are accumulated left to right.
        The function runs only when every container is valid.

        .. code:: python

          >>> from returns.validated import Invalid, Valid, Validated

          >>> def add_three(first: int, second: int, third: int) -> int:
          ...     return first + second + third

          >>> assert Validated.combine_n(
          ...     (Valid(1), Valid(2), Valid(3)),
          ...     add_three,
          ... ) == Valid(6)
          >>> assert Validated.combine_n(
          ...     (Invalid(('a',)), Valid(2), Invalid(('b',))),
          ...     add_three,
          ... ) == Invalid(('a', 'b'))

        """
        acc: Validated[tuple[Any, ...], Any] = Valid(())
        for container in containers:
            acc = acc.apply(container.apply(Valid(_concat_sequence)))
        return acc.map(lambda collected: function(*collected))

    def swap(self) -> 'Validated[Any, Any]':
        """
        Swaps the success value and the error tuple.

        ``Valid(x)`` becomes ``Invalid((x,))``.
        ``Invalid(errors)`` becomes ``Valid(errors)``:
        the error tuple itself is the new success value.

        Swapping twice is not the identity, because the success value
        is wrapped in a tuple on the way through ``Invalid``.

        .. code:: python

          >>> from returns.validated import Invalid, Valid

          >>> assert Valid(1).swap() == Invalid((1,))
          >>> assert Invalid((1, 2)).swap() == Valid((1, 2))
          >>> assert Valid(1).swap().swap() == Valid((1,))

        """


@final  # noqa: WPS338
class Invalid(Validated[Any, _ErrorType_co]):  # noqa: WPS338
    """
    Represents a validation that failed.

    Stores one or more errors in an immutable tuple.
    """

    __slots__ = ()

    _inner_value: tuple[_ErrorType_co, ...]

    def __init__(self, inner_value: tuple[_ErrorType_co, ...]) -> None:
        """Invalid constructor. ``inner_value`` must be an error tuple."""
        super().__init__(inner_value)

    if not TYPE_CHECKING:  # noqa: WPS604  # pragma: no branch

        def alt(self, function):
            """Applies ``function`` to each error in the tuple."""
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
            """Calls ``function`` with the whole error tuple."""
            return function(self.failure())

        def apply(self, container):
            """
            Accumulates errors when ``container`` is also ``Invalid``.

            Otherwise keeps this container's errors.
            """
            if isinstance(container, Invalid):
                return Invalid((*self.failure(), *container.failure()))
            return self

        def value_or(self, default_value):
            """Returns default value for a failed container."""
            return default_value

    def swap(self) -> 'Valid[tuple[_ErrorType_co, ...]]':
        """Invalid swaps to :class:`Valid` holding the error tuple."""
        return Valid(self._inner_value)

    def unwrap(self) -> Never:
        """Raises an exception, since it does not have a success value."""
        raise UnwrapFailedError(self)

    def failure(self) -> tuple[_ErrorType_co, ...]:
        """Returns the accumulated error tuple."""
        return self._inner_value


@final
class Valid(Validated[_ValueType_co, Any]):
    """
    Represents a validation that succeeded.

    Contains the resulting value.
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

    def swap(self) -> 'Invalid[_ValueType_co]':
        """Valid swaps to :class:`Invalid` with a one-element error tuple."""
        return Invalid((self._inner_value,))

    def unwrap(self) -> _ValueType_co:
        """Returns the unwrapped value from a successful container."""
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
    Decorator to convert exception-throwing function to ``Validated``.

    Should be used with care, since it only catches ``Exception`` subclasses.
    It does not catch ``BaseException`` subclasses.

    Caught exceptions are stored as ``Invalid((exception,))``.

    .. code:: python

      >>> from returns.validated import Invalid, Valid, validated

      >>> @validated
      ... def might_raise(arg: int) -> float:
      ...     return 1 / arg

      >>> assert might_raise(1) == Valid(1.0)
      >>> assert isinstance(might_raise(0), Invalid)
      >>> assert might_raise.__name__ == 'might_raise'

    You can also use it with explicit exception types as the first argument:

    .. code:: python

      >>> from returns.validated import Invalid, Valid, validated

      >>> @validated(exceptions=(ZeroDivisionError,))
      ... def might_raise(arg: int) -> float:
      ...     return 1 / arg

      >>> assert might_raise(1) == Valid(1.0)
      >>> assert isinstance(might_raise(0), Invalid)
      >>> assert might_raise.__name__ == 'might_raise'

    In this case, only exceptions that are explicitly
    listed are going to be caught.
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
