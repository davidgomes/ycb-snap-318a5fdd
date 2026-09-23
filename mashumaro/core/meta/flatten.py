"""Flatten nested dataclass fields into the parent dictionary."""

from dataclasses import dataclass, is_dataclass
from typing import TYPE_CHECKING, Any, Optional

from mashumaro.config import TO_DICT_ADD_BY_ALIAS_FLAG
from mashumaro.core.meta.helpers import (
    get_args,
    get_class_that_defines_method,
    get_type_origin,
    is_annotated,
    is_final,
    is_new_type,
    is_optional,
    is_type_alias_type,
    not_none_type_arg,
    type_name,
)
from mashumaro.exceptions import FlattenError

if TYPE_CHECKING:  # pragma: no cover
    from mashumaro.core.meta.code.builder import CodeBuilder


@dataclass
class FieldWire:
    """Keys one dataclass field contributes to its owner's dict."""

    ser_keys: set[str]
    de_keys: tuple[str, ...]
    flattened: bool = False


@dataclass
class FlattenPlan:
    """How one flattened field is merged into the class being compiled."""

    fname: str
    field_type: Any
    origin: type
    type_args: tuple[Any, ...]
    mode: str
    prefix: str
    output_map: dict[str, str]
    input_bindings: dict[str, str]
    de_parent_keys: frozenset[str]


@dataclass
class ClassWire:
    fields: dict[str, FieldWire]
    plans: dict[str, FlattenPlan]
    extra_de_keys: tuple[str, ...] = ()
    extra_ser_keys: tuple[str, ...] = ()


def build_flatten_plans(builder: "CodeBuilder") -> dict[str, FlattenPlan]:
    """Validate flatten options and return plans for direct flattened fields."""
    wire = _class_wire(builder, ())
    return wire.plans


def _class_name(cls: type) -> str:
    return type_name(cls, short=True)


def _class_wire(builder: "CodeBuilder", stack: tuple[type, ...]) -> ClassWire:
    cls = builder.cls
    if cls in stack:
        raise FlattenError(f"Cyclic flatten detected for {_class_name(cls)}")
    stack = stack + (cls,)
    fields: dict[str, FieldWire] = {}
    plans: dict[str, FlattenPlan] = {}
    for fname, ftype in builder.get_field_types(include_extras=True).items():
        metadata = builder.metadatas.get(fname, {})
        _validate_option_shape(cls, fname, metadata)
        if metadata.get("flatten"):
            wire, plan = _flattened_field_wire(
                builder, fname, ftype, metadata, stack
            )
            fields[fname] = wire
            plans[fname] = plan
        else:
            fields[fname] = _normal_field_wire(builder, fname, ftype, metadata)
    extra_de: list[str] = []
    extra_ser: list[str] = []
    discriminator = builder.get_discriminator(look_in_parents=False)
    if discriminator is not None and discriminator.field:
        extra_de.append(discriminator.field)
        extra_ser.append(discriminator.field)
    _check_collisions(cls, fields, extra_de, extra_ser)
    return ClassWire(
        fields=fields,
        plans=plans,
        extra_de_keys=tuple(extra_de),
        extra_ser_keys=tuple(extra_ser),
    )


def _validate_option_shape(cls: type, fname: str, metadata: Any) -> None:
    cname = _class_name(cls)
    flatten = metadata.get("flatten", False)
    if flatten is None:
        flatten = False
    if not isinstance(flatten, bool):
        raise FlattenError(
            f'Invalid flatten value for field "{fname}" in {cname}: '
            "expected a bool"
        )
    prefix = metadata.get("flatten_prefix", None)
    rename = metadata.get("flatten_rename", None)
    prefix_set = prefix not in (None, False)
    rename_set = rename is not None
    if prefix_set and not (prefix is True or isinstance(prefix, str)):
        raise FlattenError(
            f'Invalid flatten_prefix for field "{fname}" in {cname}: '
            "expected a string or True"
        )
    if rename_set and not isinstance(rename, dict):
        raise FlattenError(
            f'Invalid flatten_rename for field "{fname}" in {cname}: '
            "expected a dict"
        )
    if prefix_set and rename_set:
        raise FlattenError(
            "flatten_prefix and flatten_rename are mutually exclusive "
            f'for field "{fname}" in {cname}'
        )
    if (prefix_set or rename_set) and not flatten:
        raise FlattenError(
            "flatten_prefix and flatten_rename require flatten=True "
            f'for field "{fname}" in {cname}'
        )
    if rename_set:
        for key, value in rename.items():
            if (
                not isinstance(key, str)
                or not isinstance(value, str)
                or key == ""
                or value == ""
            ):
                raise FlattenError(
                    f"Invalid flatten_rename key or value for field "
                    f'"{fname}" in {cname}: keys and values must be '
                    "non-empty strings"
                )


def _normal_field_wire(
    builder: "CodeBuilder",
    fname: str,
    ftype: Any,
    metadata: Any,
) -> FieldWire:
    field = builder.dataclass_fields.get(fname)
    init_ok = True if field is None else bool(field.init)
    alias = builder.get_field_alias_value(fname, ftype, metadata)
    omitted = metadata.get("serialize") == "omit"
    return FieldWire(
        ser_keys=_ser_keys(builder, fname, alias, omitted),
        de_keys=_de_keys(builder, fname, alias, init_ok),
        flattened=False,
    )


def _ser_keys(
    builder: "CodeBuilder",
    fname: str,
    alias: Optional[str],
    omitted: bool,
) -> set[str]:
    if omitted:
        return set()
    by_alias_feature = builder.is_code_generation_option_enabled(
        TO_DICT_ADD_BY_ALIAS_FLAG
    )
    serialize_by_alias = bool(
        builder.get_dialect_or_config_option("serialize_by_alias", False)
    )
    if not alias:
        return {fname}
    keys: set[str] = set()
    if serialize_by_alias or by_alias_feature:
        keys.add(alias)
    if not serialize_by_alias or by_alias_feature:
        keys.add(fname)
    return keys


def _de_keys(
    builder: "CodeBuilder",
    fname: str,
    alias: Optional[str],
    init_ok: bool,
) -> tuple[str, ...]:
    if not init_ok:
        return ()
    allow_alt = bool(builder.get_config().allow_deserialization_not_by_alias)
    if alias and alias != fname:
        if allow_alt:
            return (alias, fname)
        return (alias,)
    return (fname,)


def _flattened_field_wire(
    builder: "CodeBuilder",
    fname: str,
    ftype: Any,
    metadata: Any,
    stack: tuple[type, ...],
) -> tuple[FieldWire, FlattenPlan]:
    cls = builder.cls
    cname = _class_name(cls)
    inner = _unwrap_flatten_type(builder, fname, ftype)
    parsed = _as_dataclass(inner)
    if parsed is None:
        raise FlattenError(
            f'Cannot flatten field "{fname}" of type '
            f"{type_name(inner, short=True)} in {cname}: "
            "type is not a dataclass"
        )
    origin, type_args = parsed
    child = _child_builder(builder, origin)
    child_wire = _class_wire(child, stack)
    _validate_rename_targets(cls, fname, metadata, child_wire)
    mode, prefix, output_map, bindings, ser_keys, de_keys = (
        _project_child_keys(cls, fname, metadata, child_wire)
    )
    plan = FlattenPlan(
        fname=fname,
        field_type=ftype,
        origin=origin,
        type_args=type_args,
        mode=mode,
        prefix=prefix,
        output_map=output_map,
        input_bindings=bindings,
        de_parent_keys=frozenset(de_keys),
    )
    omitted = metadata.get("serialize") == "omit"
    wire = FieldWire(
        ser_keys=set() if omitted else ser_keys,
        de_keys=tuple(de_keys),
        flattened=True,
    )
    return wire, plan


def _validate_rename_targets(
    cls: type,
    fname: str,
    metadata: Any,
    child_wire: ClassWire,
) -> None:
    rename = metadata.get("flatten_rename")
    if not rename:
        return
    cname = _class_name(cls)
    seen_targets: dict[str, str] = {}
    for src, dst in rename.items():
        field_wire = child_wire.fields.get(src)
        if field_wire is None or field_wire.flattened:
            raise FlattenError(
                f'Invalid flatten_rename key {src!r} for field "{fname}" '
                f"in {cname}"
            )
        if dst in seen_targets:
            raise FlattenError(
                f"Duplicate flatten_rename target {dst!r} for field "
                f'"{fname}" in {cname}'
            )
        seen_targets[dst] = src


def _project_child_keys(
    cls: type,
    fname: str,
    metadata: Any,
    child_wire: ClassWire,
) -> tuple[str, str, dict[str, str], dict[str, str], set[str], list[str]]:
    """Return mode, prefix, output map, bindings, ser keys, de keys."""
    rename = metadata.get("flatten_rename") or None
    prefix_opt = metadata.get("flatten_prefix", None)
    if prefix_opt is True:
        prefix = f"{fname}_"
    elif isinstance(prefix_opt, str):
        prefix = prefix_opt
    else:
        prefix = ""
    use_prefix = prefix_opt not in (None, False)
    use_rename = bool(rename)

    parts: list[tuple[str, set[str]]] = []
    output_map: dict[str, str] = {}
    bindings: dict[str, str] = {}
    ser_keys: set[str] = set()
    de_keys: list[str] = []

    def _add_part(part_name: str, keys: set[str]) -> None:
        parts.append((part_name, keys))

    if use_rename:
        mode = "rename"
        assert rename is not None
        renamed = set(rename)
        for name, field_wire in child_wire.fields.items():
            if name in renamed:
                wire_name = rename[name]
                contributed = set()
                for ser_key in field_wire.ser_keys:
                    output_map[ser_key] = wire_name
                    contributed.add(wire_name)
                    ser_keys.add(wire_name)
                if field_wire.de_keys:
                    bindings[wire_name] = field_wire.de_keys[0]
                    contributed.add(wire_name)
                    de_keys.append(wire_name)
                if contributed:
                    _add_part(name, contributed)
            else:
                contributed = set(field_wire.ser_keys) | set(
                    field_wire.de_keys
                )
                ser_keys.update(field_wire.ser_keys)
                for de_key in field_wire.de_keys:
                    bindings[de_key] = de_key
                    de_keys.append(de_key)
                _add_part(name, contributed)
        for extra in child_wire.extra_de_keys:
            bindings.setdefault(extra, extra)
            de_keys.append(extra)
        for extra in child_wire.extra_ser_keys:
            ser_keys.add(extra)
        if child_wire.extra_de_keys or child_wire.extra_ser_keys:
            _add_part(
                "<discriminator>",
                set(child_wire.extra_de_keys) | set(child_wire.extra_ser_keys),
            )
    elif use_prefix:
        mode = "prefix"
        for name, field_wire in child_wire.fields.items():
            contributed = {prefix + key for key in field_wire.ser_keys}
            contributed.update(prefix + key for key in field_wire.de_keys)
            ser_keys.update(prefix + key for key in field_wire.ser_keys)
            for de_key in field_wire.de_keys:
                parent_key = prefix + de_key
                bindings[parent_key] = de_key
                de_keys.append(parent_key)
            _add_part(name, contributed)
        for extra in child_wire.extra_de_keys:
            parent_key = prefix + extra
            bindings[parent_key] = extra
            de_keys.append(parent_key)
        for extra in child_wire.extra_ser_keys:
            ser_keys.add(prefix + extra)
        if child_wire.extra_de_keys or child_wire.extra_ser_keys:
            _add_part(
                "<discriminator>",
                {prefix + key for key in child_wire.extra_de_keys}
                | {prefix + key for key in child_wire.extra_ser_keys},
            )
    else:
        mode = "identity"
        for name, field_wire in child_wire.fields.items():
            contributed = set(field_wire.ser_keys) | set(field_wire.de_keys)
            ser_keys.update(field_wire.ser_keys)
            for de_key in field_wire.de_keys:
                bindings[de_key] = de_key
                de_keys.append(de_key)
            _add_part(name, contributed)
        for extra in child_wire.extra_de_keys:
            bindings.setdefault(extra, extra)
            de_keys.append(extra)
        for extra in child_wire.extra_ser_keys:
            ser_keys.add(extra)
        if child_wire.extra_de_keys or child_wire.extra_ser_keys:
            _add_part(
                "<discriminator>",
                set(child_wire.extra_de_keys) | set(child_wire.extra_ser_keys),
            )

    _check_part_collisions(cls, fname, parts)
    return mode, prefix, output_map, bindings, ser_keys, de_keys


def _check_part_collisions(
    cls: type, fname: str, parts: list[tuple[str, set[str]]]
) -> None:
    owners: dict[str, str] = {}
    for part_name, keys in parts:
        for key in keys:
            previous = owners.get(key)
            if previous is not None and previous != part_name:
                raise FlattenError(
                    f"Flatten key collision on {key!r} between "
                    f"{previous!r} and {part_name!r} in flattened field "
                    f'"{fname}" of {_class_name(cls)}'
                )
            owners[key] = part_name


def _check_collisions(
    cls: type,
    fields: dict[str, FieldWire],
    extra_de: list[str],
    extra_ser: list[str],
) -> None:
    owners: dict[str, tuple[str, bool]] = {}
    items = list(fields.items())
    if extra_de or extra_ser:
        items.append(
            (
                "<discriminator>",
                FieldWire(
                    set(extra_ser),
                    tuple(extra_de),
                    flattened=False,
                ),
            )
        )
    for name, field_wire in items:
        keys = set(field_wire.ser_keys) | set(field_wire.de_keys)
        for key in keys:
            previous = owners.get(key)
            if previous is not None and previous[0] != name:
                if field_wire.flattened or previous[1]:
                    raise FlattenError(
                        f"Flatten key collision on {key!r} between "
                        f"{previous[0]!r} and {name!r} in "
                        f"{_class_name(cls)}"
                    )
            else:
                owners[key] = (name, field_wire.flattened)


def _unwrap_flatten_type(
    builder: "CodeBuilder", fname: str, ftype: Any
) -> Any:
    resolved = builder.get_field_resolved_type_params(fname)
    try:
        typ = builder.get_real_type(fname, ftype)
    except KeyError:
        typ = ftype
    seen: set[int] = set()
    for _ in range(12):
        if typ is None or id(typ) in seen:
            break
        seen.add(id(typ))
        if is_type_alias_type(typ):
            typ = typ.__value__
            continue
        if is_annotated(typ):
            args = get_args(typ)
            if args:
                typ = args[0]
                continue
            typ = get_type_origin(typ)
            continue
        if is_final(typ):
            args = get_args(typ)
            if args:
                typ = args[0]
                continue
            break
        if is_optional(typ, resolved):
            inner = not_none_type_arg(get_args(typ), resolved)
            if inner is None:
                break
            typ = inner
            continue
        if is_new_type(typ):
            typ = typ.__supertype__
            continue
        break
    return typ


def _as_dataclass(typ: Any) -> Optional[tuple[type, tuple[Any, ...]]]:
    if typ is None:
        return None
    origin = get_type_origin(typ)
    if not isinstance(origin, type):
        return None
    try:
        ok = is_dataclass(origin)
    except TypeError:
        return None
    if not ok:
        return None
    if origin is typ:
        return origin, ()
    return origin, tuple(get_args(typ))


def _child_builder(parent: "CodeBuilder", cls: type) -> "CodeBuilder":
    from mashumaro.core.meta.code.builder import CodeBuilder

    method_name = parent.get_pack_method_name((), parent.format_name)
    defined = get_class_that_defines_method(method_name, cls)
    method_exists = defined is cls
    flags = parent.get_pack_method_flags(cls)
    passes_dialect = parent.dialect is not None and "dialect=dialect" in flags
    if (not method_exists) or passes_dialect:
        dialect = parent.dialect
        default_dialect = parent.default_dialect if not method_exists else None
    else:
        dialect = None
        default_dialect = None
    child = CodeBuilder(
        cls,
        dialect=dialect,
        default_dialect=default_dialect,
        format_name=parent.format_name,
        allow_postponed_evaluation=parent.allow_postponed_evaluation,
    )
    child.reset()
    return child
