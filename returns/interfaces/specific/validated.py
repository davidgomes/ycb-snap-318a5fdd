"""
An interface for containers that accumulate independent failures.

Unlike :class:`~returns.interfaces.failable.DiverseFailableN`, this hierarchy
does not extend :class:`~returns.interfaces.swappable.SwappableN`.
``Validated.swap`` wraps a success value into a one-element error tuple,
so swapping twice is not the identity and the double-swap law does not hold.
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
class _ValidatedLawSpec(LawSpecDef):
    """Short-circuit laws for failure-aware validation containers."""

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
        """Ensures that a successful function does not run on a failure."""
        wrapped_function = container.from_value(function)
        assert_equal(
            container.from_failure(raw_value),
            container.from_failure(raw_value).apply(wrapped_function),
        )

    @law_definition
    def alt_short_circuit_law(
        raw_value: _FirstType,
        container: ValidatedLikeN[_FirstType, _SecondType, _ThirdType],
        function: Callable[[_SecondType], _NewFirstType],
    ) -> None:
        """Ensures that you cannot alt a success."""
        assert_equal(
            container.from_value(raw_value),
            container.from_value(raw_value).alt(function),
        )


class ValidatedLikeN(
    failable.FailableN[_FirstType, _SecondType, _ThirdType],
    altable.AltableN[_FirstType, _SecondType, _ThirdType],
    Lawful['ValidatedLikeN[_FirstType, _SecondType, _ThirdType]'],
):
    """
    Interface for types that look like ``Validated`` but may not unwrap.

    ``bind`` short-circuits on failure.
    ``apply`` is free to accumulate failures from both sides.
    """

    __slots__ = ()

    _laws: ClassVar[Sequence[Law]] = (
        Law3(_ValidatedLawSpec.map_short_circuit_law),
        Law3(_ValidatedLawSpec.bind_short_circuit_law),
        Law3(_ValidatedLawSpec.apply_short_circuit_law),
        Law3(_ValidatedLawSpec.alt_short_circuit_law),
    )

    @abstractmethod
    def lash(  # type: ignore[override]
        self: _ValidatedLikeType,
        function: Callable[
            [tuple[_SecondType, ...]],
            KindN[_ValidatedLikeType, _FirstType, _UpdatedType, _ThirdType],
        ],
    ) -> KindN[_ValidatedLikeType, _FirstType, _UpdatedType, _ThirdType]:
        """
        Composes a failed container with a function returning a container.

        The function receives the accumulated error tuple,
        the same value ``failure`` returns.
        ``alt`` still maps each element inside that tuple.
        """

    @abstractmethod
    def bind_validated(
        self: _ValidatedLikeType,
        function: Callable[[_FirstType], Validated[_UpdatedType, _SecondType]],
    ) -> KindN[_ValidatedLikeType, _UpdatedType, _SecondType, _ThirdType]:
        """Binds a function that returns ``Validated`` over this container."""

    @classmethod
    @abstractmethod
    def from_failure(
        cls: type[_ValidatedLikeType],
        inner_value: _UpdatedType,
    ) -> KindN[_ValidatedLikeType, _FirstType, _UpdatedType, _ThirdType]:
        """
        Creates a failed container from a single error.

        Implementations must wrap ``inner_value`` into a one-element tuple
        so later accumulation stays uniform.
        """

    @classmethod
    @abstractmethod
    def from_validated(
        cls: type[_ValidatedLikeType],
        inner_value: Validated[_ValueType, _ErrorType],
    ) -> KindN[_ValidatedLikeType, _ValueType, _ErrorType, _ThirdType]:
        """Creates a container from an existing ``Validated`` value."""

    @classmethod
    @abstractmethod
    def from_result(
        cls: type[_ValidatedLikeType],
        inner_value: Result[_ValueType, _ErrorType],
    ) -> KindN[_ValidatedLikeType, _ValueType, _ErrorType, _ThirdType]:
        """
        Creates a container from a ``Result``.

        ``Success`` becomes a valid value.
        ``Failure`` becomes a single-error invalid value.
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
    Intermediate type with unwrapping for ``Validated``-like containers.

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
    Base type for real ``Validated`` types.

    Can be unwrapped. ``failure`` returns the accumulated error tuple.
    """

    __slots__ = ()


#: Type alias for kinds with two type arguments.
ValidatedBased2 = ValidatedBasedN[_FirstType, _SecondType, Never]

#: Type alias for kinds with three type arguments.
ValidatedBased3 = ValidatedBasedN[_FirstType, _SecondType, _ThirdType]
