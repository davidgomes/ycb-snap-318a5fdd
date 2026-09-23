"""
An interface for error-accumulating validation containers.

``Validated`` collects every independent failure instead of keeping only
the first one. It cannot extend
:class:`~returns.interfaces.failable.DiverseFailableN`: that interface
requires :class:`~returns.interfaces.swappable.SwappableN`, and
``Validated.swap`` wraps a success value into a one-element tuple, so
swapping twice is not the identity.
"""

from __future__ import annotations

from abc import abstractmethod
from collections.abc import Callable, Sequence
from typing import TYPE_CHECKING, ClassVar, TypeVar, final

from typing_extensions import Never

from returns.interfaces import altable, equable, failable, unwrappable
from returns.primitives.asserts import assert_equal
from returns.primitives.hkt import KindN
from returns.primitives.laws import (
    Law,
    Law3,
    Lawful,
    LawSpecDef,
    law_definition,
)

if TYPE_CHECKING:
    from returns.result import Result  # noqa: WPS433
    from returns.validated import Validated  # noqa: WPS433

_FirstType = TypeVar('_FirstType')
_SecondType = TypeVar('_SecondType')
_ThirdType = TypeVar('_ThirdType')
_UpdatedType = TypeVar('_UpdatedType')

_ValidatedLikeType = TypeVar('_ValidatedLikeType', bound='ValidatedLikeN')

# New values:
_ValueType = TypeVar('_ValueType')
_ErrorType = TypeVar('_ErrorType')

# Only used in laws:
_NewFirstType = TypeVar('_NewFirstType')

# Unwrappable:
_FirstUnwrappableType = TypeVar('_FirstUnwrappableType')
_SecondUnwrappableType = TypeVar('_SecondUnwrappableType')


@final
class _ValidatedFailableLawSpec(LawSpecDef):
    """
    Short-circuit laws for accumulative failures.

    ``map``, ``bind``, and ``apply`` must not run on failed containers.
    ``apply`` still concatenates errors when both sides failed; these laws
    only cover a failure value applied to a successful function.
    """

    __slots__ = ()

    @law_definition
    def map_short_circuit_law(
        raw_value: _SecondType,
        container: ValidatedLikeN[_FirstType, _SecondType, _ThirdType],
        function: Callable[[_FirstType], _NewFirstType],
    ) -> None:
        """Ensures that you cannot map a failure."""
        assert_equal(
            container.from_failure(raw_value),
            container.from_failure(raw_value).map(function),
        )

    @law_definition
    def bind_short_circuit_law(
        raw_value: _SecondType,
        container: ValidatedLikeN[_FirstType, _SecondType, _ThirdType],
        function: Callable[
            [_FirstType],
            KindN[ValidatedLikeN, _NewFirstType, _SecondType, _ThirdType],
        ],
    ) -> None:
        """Ensures that you cannot bind a failure."""
        assert_equal(
            container.from_failure(raw_value),
            container.from_failure(raw_value).bind(function),
        )

    @law_definition
    def apply_short_circuit_law(
        raw_value: _SecondType,
        container: ValidatedLikeN[_FirstType, _SecondType, _ThirdType],
        function: Callable[[_FirstType], _NewFirstType],
    ) -> None:
        """Ensures that applying a successful function keeps a failure."""
        wrapped_function = container.from_value(function)
        assert_equal(
            container.from_failure(raw_value),
            container.from_failure(raw_value).apply(wrapped_function),
        )


class ValidatedLikeN(
    failable.FailableN[_FirstType, _SecondType, _ThirdType],
    altable.AltableN[_FirstType, _SecondType, _ThirdType],
    Lawful['ValidatedLikeN[_FirstType, _SecondType, _ThirdType]'],
):
    """
    Base type for containers that accumulate failures.

    Like :class:`~returns.interfaces.specific.result.ResultLikeN`, but
    failures are tuples of errors and ``swap`` is intentionally not part
    of :class:`~returns.interfaces.swappable.SwappableN`.
    """

    __slots__ = ()

    _laws: ClassVar[Sequence[Law]] = (
        Law3(_ValidatedFailableLawSpec.map_short_circuit_law),
        Law3(_ValidatedFailableLawSpec.bind_short_circuit_law),
        Law3(_ValidatedFailableLawSpec.apply_short_circuit_law),
    )

    @abstractmethod
    def bind_validated(
        self: _ValidatedLikeType,
        function: Callable[
            [_FirstType],
            Validated[_UpdatedType, _SecondType],
        ],
    ) -> KindN[_ValidatedLikeType, _UpdatedType, _SecondType, _ThirdType]:
        """Runs a ``Validated``-returning function over a container."""

    @classmethod
    @abstractmethod
    def from_failure(
        cls: type[_ValidatedLikeType],
        inner_value: _UpdatedType,
    ) -> KindN[_ValidatedLikeType, _FirstType, _UpdatedType, _ThirdType]:
        """
        Creates a failed container from a single error.

        Implementations wrap ``inner_value`` into a one-element tuple so
        later concatenation treats every failure the same way.
        """

    @classmethod
    @abstractmethod
    def from_validated(
        cls: type[_ValidatedLikeType],
        inner_value: Validated[_ValueType, _ErrorType],
    ) -> KindN[_ValidatedLikeType, _ValueType, _ErrorType, _ThirdType]:
        """Creates a container from an existing ``Validated`` instance."""

    @classmethod
    @abstractmethod
    def from_result(
        cls: type[_ValidatedLikeType],
        inner_value: Result[_ValueType, _ErrorType],
    ) -> KindN[_ValidatedLikeType, _ValueType, _ErrorType, _ThirdType]:
        """
        Converts a ``Result`` into a ``Validated``.

        ``Success`` becomes a valid value.
        ``Failure`` becomes a one-element error tuple.
        """


#: Type alias for kinds with two type arguments.
ValidatedLike2 = ValidatedLikeN[_FirstType, _SecondType, Never]

#: Type alias for kinds with three type arguments.
ValidatedLike3 = ValidatedLikeN[_FirstType, _SecondType, _ThirdType]


class UnwrappableValidated(
    ValidatedLikeN[_FirstType, _SecondType, _ThirdType],
    unwrappable.Unwrappable[_FirstUnwrappableType, _SecondUnwrappableType],
    equable.Equable,
):
    """
    Intermediate type with unwrapping for ``Validated`` containers.

    It is a raw type and should not be used directly.
    Use ``ValidatedBasedN`` instead.
    """

    __slots__ = ()


class ValidatedBasedN(
    UnwrappableValidated[
        _FirstType,
        _SecondType,
        _ThirdType,
        # Unwraps:
        _FirstType,
        tuple[_SecondType, ...],
    ],
):
    """
    Base type for concrete ``Validated`` types.

    Successful containers unwrap to the success value.
    Failed containers unwrap their error tuple via ``failure``.
    """

    __slots__ = ()


#: Type alias for kinds with two type arguments.
ValidatedBased2 = ValidatedBasedN[_FirstType, _SecondType, Never]

#: Type alias for kinds with three type arguments.
ValidatedBased3 = ValidatedBasedN[_FirstType, _SecondType, _ThirdType]
