from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define, field

if TYPE_CHECKING:
    from .converters import BaseConverter

T = TypeVar("T")

__all__ = ["PartialResult"]


@define
class PartialResult:
    """The result of a :meth:`BaseConverter.partial_structure` call.

    :ivar value: The partially structured object, or ``None`` if a value
        could not be produced (a required field without a default failed).
    :ivar is_complete: Whether every applicable field was successfully
        structured from the input (and no forbidden extra keys were present).
    :ivar structured_fields: Field names successfully structured from input.
    :ivar failed_fields: Field names that were missing or failed to structure.
    :ivar errors: A collected exception (or ``None`` if there were no errors).
    :ivar error_map: Mapping of field name to the exception that caused failure.
    """

    value: Any | None
    is_complete: bool
    structured_fields: frozenset[str]
    failed_fields: frozenset[str]
    errors: Exception | None
    error_map: dict[str, Exception]
    _converter: BaseConverter = field(alias="_converter", eq=False, repr=False)
    _cl: Any = field(alias="_cl", eq=False, repr=False)
    _nested: dict[str, PartialResult] = field(
        factory=dict, alias="_nested", eq=False, repr=False
    )
    _values: dict[str, Any] = field(factory=dict, alias="_values", eq=False, repr=False)

    def refine(self, data: Mapping[str, Any]) -> PartialResult:
        """Return a new result, structuring previously failed fields from *data*.

        Successfully structured fields are preserved even if *data* contains
        replacements for them.
        """
        if self.is_complete:
            return self
        return self._converter._partial_structure_from(
            data, self._cl, previous=self
        )
