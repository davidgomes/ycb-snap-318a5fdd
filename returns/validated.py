from abc import ABC
from collections.abc import Callable, Generator, Iterator
from functools import wraps
from typing import TYPE_CHECKING, Any, TypeAlias, TypeVar, final, overload

from typing_extensions import Never, ParamSpec

from returns.interfaces.specific import validated
from returns.primitives.container import BaseContainer, container_equality
from returns.primitives.exceptions import UnwrapFailedError
from returns.primitives.hkt import Kind2, SupportsKind2

if TYPE_CHECKING:
    from returns.result import Result

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
    validated.ValidatedBased2[_ValueType_co, _ErrorType_co],
    ABC,
):
    """
    Error-accumulating container: base class for :class:`~Valid` and :class:`~Invalid`.

    ``.apply`` accumulates errors, while ``.bind`` short-circuits.

    .. code:: python

      >>> from returns.validated import Validated, Valid, Invalid
      >>> assert Validated.combine(
      ...     Invalid(('a',)), Invalid(('b',)), lambda x, y: x + y,
      ... ) == Invalid(('a', 'b'))

    """

    __slots__ = ()
    __match_args__ = ('_inner_value',)

    _inner_value: Any

    #: Typesafe equality comparison with other `Validated` objects.
    equals = container_equality

    def swap(self) -> 'Validated[Any, Any]':
        """
        Swaps values and errors.

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
        Maps successful value.

        .. code:: python

          >>> from returns.validated import Valid, Invalid
          >>> assert Valid(1).map(str) == Valid('1')
          >>> assert Invalid(('a',)).map(str) == Invalid(('a',))

        """

    def apply(
        self,
        function: Kind2[
            'Validated',
            Callable[[_ValueType_co], _NewValueType],
            _ErrorType_co,
        ],
    ) -> 'Validated[_NewValueType, _ErrorType_co]':
        """
        Applies wrapped function, accumulating errors.

        .. code:: python

          >>> from returns.validated import Valid, Invalid
          >>> assert Valid(1).apply(Valid(str)) == Valid('1')
          >>> assert Invalid(('a',)).apply(Invalid(('b',))) == Invalid(
          ...     ('a', 'b'),
          ... )

        """

    def bind(
        self,
        function: Callable[
            [_ValueType_co],
            Kind2['Validated', _NewValueType, _ErrorType_co],
        ],
    ) -> 'Validated[_NewValueType, _ErrorType_co]':
        """
        Composes with a function returning ``Validated``, short-circuiting.

        .. code:: python

          >>> from returns.validated import Valid, Invalid
          >>> assert Valid(1).bind(lambda x: Valid(x + 1)) == Valid(2)
          >>> assert Invalid(('a',)).bind(lambda x: Valid(x)) == Invalid(
          ...     ('a',),
          ... )

        """

    #: Alias for `bind` method. Part of the `ValidatedBasedN` interface.
    bind_validated = bind

    def alt(
        self,
        function: Callable[[_ErrorType_co], _NewErrorType],
    ) -> 'Validated[_ValueType_co, _NewErrorType]':
        """
        Maps each individual error.

        .. code:: python

          >>> from returns.validated import Valid, Invalid
          >>> assert Invalid(('a', 'b')).alt(str.upper) == Invalid(('A', 'B'))
          >>> assert Valid(1).alt(str.upper) == Valid(1)

        """

    def lash(
        self,
        function: Callable[
            [tuple[_ErrorType_co, ...]],
            Kind2['Validated', _ValueType_co, _NewErrorType],
        ],
    ) -> 'Validated[_ValueType_co, _NewErrorType]':
        """
        Composes failed errors tuple with a function returning ``Validated``.

        .. code:: python

          >>> from returns.validated import Valid, Invalid
          >>> assert Invalid(('a',)).lash(lambda e: Valid(len(e))) == Valid(1)
          >>> assert Valid(1).lash(lambda e: Valid(0)) == Valid(1)

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
          >>> assert Invalid(('a',)).value_or(2) == 2

        """

    def unwrap(self) -> _ValueType_co:
        """
        Get value or raise exception.

        .. code:: pycon
          :force:

          >>> from returns.validated import Valid, Invalid
          >>> assert Valid(1).unwrap() == 1

          >>> Invalid(('a',)).unwrap()
          Traceback (most recent call last):
            ...
          returns.primitives.exceptions.UnwrapFailedError

        """

    def failure(self) -> tuple[_ErrorType_co, ...]:  # type: ignore[override]
        """
        Get errors tuple or raise exception.

        .. code:: pycon
          :force:

          >>> from returns.validated import Valid, Invalid
          >>> assert Invalid(('a',)).failure() == ('a',)

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

        .. code:: python

          >>> from returns.validated import Validated, Invalid
          >>> assert Validated.from_failure('a') == Invalid(('a',))

        """
        return Invalid((inner_value,))

    @classmethod
    def from_validated(
        cls,
        inner_value: 'Validated[_NewValueType, _NewErrorType]',
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Returns the same ``Validated`` instance.

        .. code:: python

          >>> from returns.validated import Validated, Valid
          >>> instance = Valid(1)
          >>> assert Validated.from_validated(instance) is instance

        """
        return inner_value

    @classmethod
    def from_result(
        cls,
        inner_value: 'Result[_NewValueType, _NewErrorType]',
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Converts ``Result`` to ``Validated``.

        .. code:: python

          >>> from returns.result import Success, Failure
          >>> from returns.validated import Validated, Valid, Invalid
          >>> assert Validated.from_result(Success(1)) == Valid(1)
          >>> assert Validated.from_result(Failure('a')) == Invalid(('a',))

        """
        from returns.result import Success  # noqa: PLC0415

        if isinstance(inner_value, Success):
            return Valid(inner_value.unwrap())
        return Invalid((inner_value.failure(),))

    @classmethod
    def combine(
        cls,
        first: 'Validated[_FirstType, _NewErrorType]',
        second: 'Validated[_SecondType, _NewErrorType]',
        function: Callable[[_FirstType, _SecondType], _NewValueType],
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Combines two containers with a binary function, accumulating errors.

        .. code:: python

          >>> from returns.validated import Validated, Valid, Invalid
          >>> add = lambda x, y: x + y
          >>> assert Validated.combine(Valid(1), Valid(2), add) == Valid(3)
          >>> assert Validated.combine(
          ...     Valid(1), Invalid(('b',)), add,
          ... ) == Invalid(('b',))

        """
        return first.apply(
            second.map(lambda y: lambda x: function(x, y)),  # type: ignore
        )

    @classmethod
    def combine_n(
        cls,
        containers: tuple['Validated[Any, _NewErrorType]', ...],
        function: Callable[..., _NewValueType],
    ) -> 'Validated[_NewValueType, _NewErrorType]':
        """
        Combines N containers with an N-ary function, accumulating errors.

        .. code:: python

          >>> from returns.validated import Validated, Valid, Invalid
          >>> assert Validated.combine_n(
          ...     (Valid(1), Valid(2), Valid(3)), lambda a, b, c: a + b + c,
          ... ) == Valid(6)
          >>> assert Validated.combine_n(
          ...     (Invalid(('a',)), Valid(2), Invalid(('c',))),
          ...     lambda a, b, c: a + b + c,
          ... ) == Invalid(('a', 'c'))

        """
        acc: Validated[tuple[Any, ...], _NewErrorType] = Valid(())
        for container in containers:
            acc = acc.apply(
                container.map(
                    lambda value: lambda values: (*values, value),
                ),
            )
        return acc.map(lambda values: function(*values))


@final
class Invalid(Validated[Any, _ErrorType_co]):
    """Represents a failed validation with accumulated errors."""

    __slots__ = ()
    __match_args__ = ('_inner_value',)

    _inner_value: tuple[_ErrorType_co, ...]

    def __init__(self, inner_value: tuple[_ErrorType_co, ...]) -> None:
        """Invalid constructor, errors are stored as a tuple."""
        super().__init__(tuple(inner_value))

    if not TYPE_CHECKING:  # noqa: WPS604  # pragma: no branch

        def alt(self, function):
            """Maps each error."""
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
            """Composes errors with a function returning a container."""
            return function(self._inner_value)

        def apply(self, container):
            """Accumulates errors from both containers."""
            if isinstance(container, Invalid):
                return Invalid(self._inner_value + container.failure())
            return self

        def value_or(self, default_value):
            """Returns default value for ``Invalid``."""
            return default_value

    def swap(self):
        """Errors tuple becomes a value."""
        return Valid(self._inner_value)

    def unwrap(self) -> Never:
        """Raises an exception, since it does not have a value inside."""
        raise UnwrapFailedError(self)

    def failure(self) -> tuple[_ErrorType_co, ...]:
        """Returns errors tuple."""
        return self._inner_value


@final
class Valid(Validated[_ValueType_co, Any]):
    """Represents a successful validation."""

    __slots__ = ()
    __match_args__ = ('_inner_value',)

    _inner_value: _ValueType_co

    def __init__(self, inner_value: _ValueType_co) -> None:
        """Valid constructor."""
        super().__init__(inner_value)

    if not TYPE_CHECKING:  # noqa: WPS604  # pragma: no branch

        def alt(self, function):
            """Does nothing for ``Valid``."""
            return self

        def map(self, function):
            """Maps the value."""
            return Valid(function(self._inner_value))

        def bind(self, function):
            """Binds the value."""
            return function(self._inner_value)

        #: Alias for `bind` method. Part of the `ValidatedBasedN` interface.
        bind_validated = bind

        def lash(self, function):
            """Does nothing for ``Valid``."""
            return self

        def apply(self, container):
            """Applies wrapped function or propagates errors."""
            if isinstance(container, Valid):
                return self.map(container.unwrap())
            return container

        def value_or(self, default_value):
            """Returns inner value for ``Valid``."""
            return self._inner_value

    def swap(self):
        """Value becomes a single error."""
        return Invalid((self._inner_value,))

    def unwrap(self) -> _ValueType_co:
        """Returns the inner value."""
        return self._inner_value

    def failure(self) -> Never:
        """Raises an exception for ``Valid``."""
        raise UnwrapFailedError(self)


ValidatedE: TypeAlias = Validated[_ValueType_co, Exception]


@overload
def validated(
    function: Callable[_FuncParams, _ValueType_co],
) -> Callable[_FuncParams, ValidatedE[_ValueType_co]]: ...


@overload
def validated(
    *,
    exceptions: tuple[type[Exception], ...],
) -> Callable[
    [Callable[_FuncParams, _ValueType_co]],
    Callable[_FuncParams, ValidatedE[_ValueType_co]],
]: ...


def validated(  # noqa: WPS234
    function: Callable[_FuncParams, _ValueType_co] | None = None,
    exceptions: tuple[type[Exception], ...] | None = None,
) -> (
    Callable[_FuncParams, ValidatedE[_ValueType_co]]
    | Callable[
        [Callable[_FuncParams, _ValueType_co]],
        Callable[_FuncParams, ValidatedE[_ValueType_co]],
    ]
):
    """
    Decorator to convert exception-throwing function to ``Validated``.

    .. code:: python

      >>> from returns.validated import Valid, Invalid, validated

      >>> @validated
      ... def might_raise(arg: int) -> float:
      ...     return 1 / arg

      >>> assert might_raise(1) == Valid(1.0)
      >>> assert isinstance(might_raise(0), Invalid)

      >>> @validated(exceptions=(ZeroDivisionError,))
      ... def might_raise_zero(arg: int) -> float:
      ...     return 1 / arg

      >>> assert isinstance(might_raise_zero(0), Invalid)

    """

    def factory(
        inner_function: Callable[_FuncParams, _ValueType_co],
        inner_exceptions: tuple[type[Exception], ...],
    ) -> Callable[_FuncParams, ValidatedE[_ValueType_co]]:
        @wraps(inner_function)
        def decorator(*args: _FuncParams.args, **kwargs: _FuncParams.kwargs):
            try:
                return Valid(inner_function(*args, **kwargs))
            except inner_exceptions as exc:
                return Invalid((exc,))

        return decorator

    if callable(function):
        return factory(function, (Exception,))
    if isinstance(function, tuple):
        exceptions = function  # type: ignore
        function = None
    return lambda function: factory(function, exceptions)  # type: ignore
