from __future__ import annotations

from collections.abc import Callable
from typing import TYPE_CHECKING, TypeVar

from returns.interfaces.specific.validated import ValidatedLikeN
from returns.primitives.hkt import Kinded, KindN, kinded

if TYPE_CHECKING:
    from returns.validated import Validated  # noqa: WPS433

_FirstType = TypeVar('_FirstType')
_SecondType = TypeVar('_SecondType')
_ThirdType = TypeVar('_ThirdType')
_UpdatedType = TypeVar('_UpdatedType')

_ValidatedLikeKind = TypeVar('_ValidatedLikeKind', bound=ValidatedLikeN)


def bind_validated(
    function: Callable[[_FirstType], Validated[_UpdatedType, _SecondType]],
) -> Kinded[
    Callable[
        [KindN[_ValidatedLikeKind, _FirstType, _SecondType, _ThirdType]],
        KindN[_ValidatedLikeKind, _UpdatedType, _SecondType, _ThirdType],
    ]
]:
    """
    Composes successful container with a function that returns ``Validated``.

    .. code:: python

      >>> from returns.validated import Valid, Invalid
      >>> from returns.pointfree import bind_validated

      >>> bound = bind_validated(lambda arg: Valid(arg + 1))
      >>> assert bound(Valid(1)) == Valid(2)
      >>> assert bound(Invalid(('a',))) == Invalid(('a',))

    """

    @kinded
    def factory(
        container: KindN[_ValidatedLikeKind, _FirstType, _SecondType, _ThirdType],
    ) -> KindN[_ValidatedLikeKind, _UpdatedType, _SecondType, _ThirdType]:
        return container.bind_validated(function)

    return factory
