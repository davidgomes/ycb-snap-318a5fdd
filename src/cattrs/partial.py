from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import Factory, define, field

from ._compat import (
    NOTHING,
    adapted_fields,
    get_notrequired_base,
    get_origin,
    has,
    is_annotated,
    is_optional,
    is_typeddict,
)
from .errors import (
    AttributeValidationNote,
    ClassValidationError,
    ForbiddenExtraKeysError,
)

if TYPE_CHECKING:
    from .converters import BaseConverter

T = TypeVar("T")

__all__ = ["PartialResult"]


@define
class PartialResult:
    """The result of a :meth:`BaseConverter.partial_structure` call."""

    value: Any
    is_complete: bool
    structured_fields: frozenset[str]
    failed_fields: frozenset[str]
    errors: Exception | None
    error_map: dict[str, Exception]
    _converter: BaseConverter = field(repr=False, eq=False, alias="_converter")
    _cl: Any = field(repr=False, eq=False, alias="_cl")
    _structured_values: dict[str, Any] = field(
        factory=dict, repr=False, eq=False, alias="_structured_values"
    )
    _constructor_values: dict[str, Any] = field(
        factory=dict, repr=False, eq=False, alias="_constructor_values"
    )
    _nested: dict[str, PartialResult] = field(
        factory=dict, repr=False, eq=False, alias="_nested"
    )
    _extra_keys: frozenset[str] = field(
        factory=frozenset, repr=False, eq=False, alias="_extra_keys"
    )

    def refine(self, data: Mapping[str, Any]) -> PartialResult:
        """Return a new result, filling previously failed fields from *data*.

        Fields already present in :attr:`structured_fields` are preserved.
        """
        return self._converter.partial_structure(data, self._cl, _previous=self)


def _unwrap_annotated(type_: Any) -> Any:
    while is_annotated(type_):
        type_ = type_.__args__[0]
    nrb = get_notrequired_base(type_)
    if nrb is not NOTHING:
        type_ = nrb
        while is_annotated(type_):
            type_ = type_.__args__[0]
    return type_


def _is_structure_class(type_: Any) -> bool:
    type_ = _unwrap_annotated(type_)
    origin = get_origin(type_) or type_
    return has(type_) or has(origin) or is_typeddict(type_) or is_typeddict(origin)


def _nested_class_type(type_: Any) -> Any | None:
    """If *type_* is (optionally) an attrs/dataclass/TypedDict, return that class."""
    type_ = _unwrap_annotated(type_)
    if is_optional(type_):
        args = type_.__args__
        other = args[0] if args[1] is type(None) else args[1]
        if _is_structure_class(other):
            return other
        return None
    if _is_structure_class(type_):
        return type_
    return None


def _field_default(a) -> tuple[bool, Any]:
    """Return (usable, value) for a field default."""
    default = getattr(a, "default", NOTHING)
    if default is NOTHING:
        return False, NOTHING
    if isinstance(default, Factory):
        if default.takes_self:
            return False, NOTHING
        return True, default.factory()
    return True, default


def _input_key(a, use_alias: bool) -> str:
    if use_alias:
        return getattr(a, "alias", a.name)
    return a.name


def _ctor_key(a, typeddict: bool) -> str:
    if typeddict:
        return a.name
    return getattr(a, "alias", a.name)


def partial_structure_impl(
    converter: BaseConverter,
    obj: Any,
    cl: type[T],
    previous: PartialResult | None = None,
) -> PartialResult:
    typeddict = is_typeddict(cl)
    use_alias = getattr(converter, "use_alias", False)
    forbid_extra = getattr(converter, "forbid_extra_keys", False)
    detailed = converter.detailed_validation

    if typeddict:
        from .gen.typeddicts import _adapted_fields, _required_keys

        origin = get_origin(cl) or cl
        attrs = _adapted_fields(origin)
        required = set(_required_keys(origin))
        # TypedDict adapted fields are marked init=False; treat all as init fields.
        for a in attrs:
            object.__setattr__(a, "init", True)
    else:
        origin = get_origin(cl) or cl
        attrs = list(adapted_fields(origin))
        required = {a.name for a in attrs if a.init and a.default is NOTHING}

    init_attrs = [a for a in attrs if a.init]
    field_by_name = {a.name: a for a in init_attrs}
    allowed_keys = {_input_key(a, use_alias) for a in init_attrs}

    structured: dict[str, Any] = dict(previous._structured_values) if previous else {}
    ctor_vals: dict[str, Any] = (
        dict(previous._constructor_values) if previous else {}
    )
    nested_map: dict[str, PartialResult] = dict(previous._nested) if previous else {}
    error_map: dict[str, Exception] = {}
    extra_keys: set[str] = set(previous._extra_keys) if previous else set()

    if not isinstance(obj, Mapping):
        exc: Exception = TypeError(
            f"Expected a mapping to structure {cl!r}, got {obj!r}"
        )
        failed = frozenset(a.name for a in init_attrs if a.name not in structured)
        for name in failed:
            error_map[name] = exc
        errors = _finalize_errors(cl, error_map, extra_keys, detailed, None)
        return PartialResult(
            value=None,
            is_complete=False,
            structured_fields=frozenset(structured),
            failed_fields=failed,
            errors=errors,
            error_map=error_map,
            _converter=converter,
            _cl=cl,
            _structured_values=structured,
            _constructor_values=ctor_vals,
            _nested=nested_map,
            _extra_keys=frozenset(extra_keys),
        )

    if forbid_extra:
        extra_keys |= set(obj.keys()) - allowed_keys

    for a in init_attrs:
        name = a.name
        kn = _input_key(a, use_alias)
        ck = _ctor_key(a, typeddict)

        if name in structured:
            continue

        type_ = a.type
        nrb = get_notrequired_base(type_)
        if nrb is not NOTHING:
            type_ = nrb

        if kn not in obj:
            if name in nested_map:
                nr = nested_map[name]
                if nr.value is not None:
                    ctor_vals[ck] = nr.value
                error_map[name] = nr.errors or _missing_error(name)
                continue
            if name in ctor_vals and previous is not None:
                # Keep prior fallback / nested partial value.
                error_map[name] = (
                    previous.error_map.get(name) or _missing_error(name)
                )
                continue
            usable, default_val = _field_default(a)
            if usable:
                ctor_vals[ck] = default_val
            error_map[name] = _missing_error(name)
            continue

        raw = obj[kn]
        nested_cl = _nested_class_type(type_)

        if nested_cl is not None and raw is not None and isinstance(raw, Mapping):
            if name in nested_map:
                nr = nested_map[name].refine(raw)
            else:
                nr = converter.partial_structure(raw, nested_cl)
            if nr.is_complete:
                structured[name] = nr.value
                ctor_vals[ck] = nr.value
                nested_map.pop(name, None)
            elif nr.value is not None:
                ctor_vals[ck] = nr.value
                nested_map[name] = nr
                error_map[name] = nr.errors or ValueError(
                    f"Partial structure of field {name!r}"
                )
            else:
                nested_map.pop(name, None)
                usable, default_val = _field_default(a)
                if usable:
                    ctor_vals[ck] = default_val
                error_map[name] = nr.errors or ValueError(
                    f"Could not structure field {name!r}"
                )
            continue

        try:
            if typeddict:
                val = converter.structure(raw, type_)
            else:
                val = converter._structure_attribute(a, raw)
            structured[name] = val
            ctor_vals[ck] = val
            nested_map.pop(name, None)
        except Exception as exc:
            usable, default_val = _field_default(a)
            if usable:
                ctor_vals[ck] = default_val
            error_map[name] = exc

    failed_fields = frozenset(error_map)
    structured_fields = frozenset(structured)

    can_construct = True
    for name in required:
        a = field_by_name[name]
        ck = _ctor_key(a, typeddict)
        if ck not in ctor_vals:
            can_construct = False
            break

    value = None
    if can_construct:
        try:
            if typeddict:
                value = {
                    k: ctor_vals[k]
                    for k in (a.name for a in init_attrs)
                    if k in ctor_vals
                }
            else:
                kwargs = {}
                for a in init_attrs:
                    ck = _ctor_key(a, False)
                    if ck in ctor_vals:
                        kwargs[ck] = ctor_vals[ck]
                value = cl(**kwargs)
        except Exception:
            value = None

    extra_err = None
    if extra_keys:
        extra_err = ForbiddenExtraKeysError(None, cl, extra_keys)

    is_complete = not failed_fields and extra_err is None
    errors = _finalize_errors(cl, error_map, extra_keys, detailed, extra_err)

    return PartialResult(
        value=value,
        is_complete=is_complete,
        structured_fields=structured_fields,
        failed_fields=failed_fields,
        errors=errors,
        error_map=error_map,
        _converter=converter,
        _cl=cl,
        _structured_values=structured,
        _constructor_values=ctor_vals,
        _nested=nested_map,
        _extra_keys=frozenset(extra_keys),
    )


def _missing_error(name: str) -> KeyError:
    return KeyError(name)


def _finalize_errors(
    cl: type,
    error_map: dict[str, Exception],
    extra_keys: set[str],
    detailed: bool,
    extra_err: ForbiddenExtraKeysError | None,
) -> Exception | None:
    if not error_map and extra_err is None:
        return None

    if detailed:
        excs: list[Exception] = []
        for name, exc in error_map.items():
            note = AttributeValidationNote(
                f"Structuring class {getattr(cl, '__qualname__', cl)} @ attribute {name}",
                name,
                None,
            )
            notes = getattr(exc, "__notes__", [])
            if not any(
                isinstance(n, AttributeValidationNote) and n.name == name for n in notes
            ):
                exc.__notes__ = [*notes, note]
            excs.append(exc)
        if extra_err is not None:
            excs.append(extra_err)
        return ClassValidationError(f"While structuring {cl!r}", excs, cl)

    if error_map:
        return next(iter(error_map.values()))
    return extra_err
