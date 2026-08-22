from __future__ import annotations

from dataclasses import MISSING, dataclass
from typing import Any, Callable, Mapping, TypeVar, get_type_hints

from attrs import Factory, NOTHING

from ._compat import adapted_fields, fields, has, is_typeddict
from .errors import ClassValidationError, ForbiddenExtraKeysError

T = TypeVar("T")


@dataclass(frozen=True)
class PartialResult:
    """The result of attempting to structure an object field by field."""

    value: Any
    is_complete: bool
    structured_fields: frozenset[str]
    failed_fields: frozenset[str]
    errors: Exception | None
    error_map: dict[str, Exception]
    _refiner: Callable[[Mapping[str, Any]], PartialResult] | None = None

    def refine(self, data: Mapping[str, Any]) -> PartialResult:
        """Retry structuring with additional or corrected input."""
        if self._refiner is None:
            raise TypeError("This result cannot be refined.")
        return self._refiner(data)


def partial_structure(
    converter: Any, data: Mapping[str, Any], cl: type[T]
) -> PartialResult:
    """Partially structure a mapping into an attrs, dataclass, or TypedDict."""
    original = dict(data) if isinstance(data, Mapping) else data

    def run(new_data: Mapping[str, Any]) -> PartialResult:
        return _partial_structure(converter, new_data, cl)

    result = _partial_structure(converter, original, cl)
    return PartialResult(
        result.value,
        result.is_complete,
        result.structured_fields,
        result.failed_fields,
        result.errors,
        result.error_map,
        lambda update: run({**original, **update}),
    )


def _partial_structure(converter: Any, data: Mapping[str, Any], cl: type[T]) -> PartialResult:
    if not isinstance(data, Mapping):
        error = TypeError(f"expected a mapping, not {data.__class__.__name__}")
        return PartialResult(None, False, frozenset(), frozenset(), error, {"": error})

    is_td = is_typeddict(cl)
    if is_td:
        annotations = get_type_hints(cl, include_extras=True)
        field_info = [(name, name, annotations[name], name in cl.__required_keys__, NOTHING, True)
                      for name in annotations]
    else:
        origin = getattr(cl, "__origin__", None)
        actual_cl = origin or cl
        field_info = []
        for field in adapted_fields(actual_cl):
            default = field.default
            field_info.append(
                (field.name, field.alias, field.type, default is NOTHING, default, field.init)
            )

    structured: set[str] = set()
    failed: set[str] = set()
    error_map: dict[str, Exception] = {}
    values: dict[str, Any] = {}
    init_values: dict[str, Any] = {}
    allowed_keys = {key for name, key, _, _, _, _ in field_info}

    for name, key, type_, required, default, init in field_info:
        if not init and not is_td:
            continue
        if key not in data:
            failed.add(name)
            error_map[name] = KeyError(key)
            fallback, has_fallback = _default_value(default, is_td)
            if has_fallback:
                values[name] = fallback
                if init:
                    init_values[key] = fallback
            continue

        raw = data[key]
        try:
            nested_type = getattr(type_, "__origin__", None) or type_
            if has(nested_type) and isinstance(raw, Mapping):
                nested = _partial_structure(converter, raw, nested_type)
                if nested.value is None:
                    raise nested.errors or ValueError(f"could not structure {name}")
                value = nested.value
                if not nested.is_complete:
                    failed.add(name)
                    error_map[name] = nested.errors or ValueError(
                        f"nested field {name} is incomplete"
                    )
                else:
                    structured.add(name)
            else:
                value = converter.structure(raw, type_) if type_ is not None else raw
                structured.add(name)
            values[name] = value
            if init:
                init_values[key] = value
        except Exception as exc:
            failed.add(name)
            error_map[name] = exc
            fallback, has_fallback = _default_value(default, is_td)
            if has_fallback:
                values[name] = fallback
                if init:
                    init_values[key] = fallback

    extras = set(data) - allowed_keys
    if extras and getattr(converter, "forbid_extra_keys", False):
        error_map[""] = ForbiddenExtraKeysError("", cl, extras)

    required_without_value = any(
        name not in values and required and init
        for name, _, _, required, _, init in field_info
    )
    value = None
    if not required_without_value:
        try:
            if is_td:
                value = values
            else:
                value = cl(**init_values)
                for name, key, type_, _, _, init in field_info:
                    if not init and key in data:
                        setattr(value, name, converter.structure(data[key], type_))
        except Exception as exc:
            error_map.setdefault("", exc)

    errors: Exception | None = None
    if error_map:
        exceptions = list(error_map.values())
        errors = (
            ClassValidationError(f"While partially structuring {cl!r}", exceptions, cl)
            if converter.detailed_validation
            else exceptions[0]
        )
    complete = not failed and not error_map
    return PartialResult(
        value,
        complete,
        frozenset(structured),
        frozenset(failed),
        errors,
        error_map,
    )


def _default_value(default: Any, is_td: bool) -> tuple[Any, bool]:
    if is_td:
        return (None, False)
    if default is NOTHING or default is MISSING:
        return (None, False)
    if isinstance(default, Factory):
        if default.takes_self:
            return (None, False)
        return default.factory(), True
    return default, True
