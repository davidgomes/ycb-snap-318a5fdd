from collections.abc import Mapping
from dataclasses import dataclass, is_dataclass
from typing import TYPE_CHECKING, Any, Optional, Type

from mashumaro.config import TO_DICT_ADD_BY_ALIAS_FLAG
from mashumaro.core.meta.helpers import (
    get_args,
    get_type_origin,
    is_annotated,
    is_optional,
    is_type_var_any,
    not_none_type_arg,
    type_name,
)
from mashumaro.exceptions import BadFlatten

if TYPE_CHECKING:
    from mashumaro.core.meta.code.builder import CodeBuilder


@dataclass
class FlattenPlan:
    fname: str
    inner_type: Any
    could_be_none: bool
    prefix: str
    ser_map: dict[str, str]
    bindings: list[tuple[str, str]]
    input_keys: set[str]


@dataclass
class _FieldWire:
    fname: str
    flattened: bool
    # (key in this dict, key passed to the owner's from_dict)
    accepted: list[tuple[str, str]]
    keys: set[str]
    lookup: str
    plan: Optional[FlattenPlan] = None


def collect_flatten_plans(
    builder: "CodeBuilder",
    field_types: dict[str, Any],
) -> dict[str, FlattenPlan]:
    dynamic = builder.is_code_generation_option_enabled(
        TO_DICT_ADD_BY_ALIAS_FLAG
    )
    wires = _analyze_fields(
        builder,
        field_types,
        dynamic_by_alias=dynamic,
        stack=frozenset(),
    )
    plans: dict[str, FlattenPlan] = {}
    for fname, wire in wires.items():
        if wire.plan is not None:
            plans[fname] = wire.plan
    return plans


def _analyze_fields(
    builder: "CodeBuilder",
    field_types: dict[str, Any],
    *,
    dynamic_by_alias: bool,
    stack: frozenset[Type[Any]],
) -> dict[str, _FieldWire]:
    wires: dict[str, _FieldWire] = {}
    for fname, ftype in field_types.items():
        metadata = builder.metadatas.get(fname, {})
        wires[fname] = _analyze_field(
            builder,
            fname,
            ftype,
            metadata,
            dynamic_by_alias=dynamic_by_alias,
            stack=stack,
        )
    _ensure_no_collisions(builder, wires)
    return wires


def _analyze_field(
    builder: "CodeBuilder",
    fname: str,
    ftype: Any,
    metadata: Mapping[str, Any],
    *,
    dynamic_by_alias: bool,
    stack: frozenset[Type[Any]],
) -> _FieldWire:
    flatten = metadata.get("flatten", None)
    prefix_opt = metadata.get("flatten_prefix", None)
    rename = metadata.get("flatten_rename", None)
    cls_name = type_name(builder.cls, short=True)
    if flatten is True:
        return _analyze_flatten_field(
            builder,
            fname,
            ftype,
            prefix_opt,
            rename,
            dynamic_by_alias=dynamic_by_alias,
            stack=stack,
            cls_name=cls_name,
        )
    if flatten not in (None, False):
        raise BadFlatten(
            f"Option flatten for field {fname!r} in {cls_name} "
            "must be a boolean"
        )
    if prefix_opt not in (None, False) or rename:
        raise BadFlatten(
            f"Field {fname!r} in {cls_name} uses flatten_prefix or "
            "flatten_rename without flatten=True"
        )
    return _plain_wire(
        builder, fname, ftype, metadata, dynamic_by_alias=dynamic_by_alias
    )


def _analyze_flatten_field(
    builder: "CodeBuilder",
    fname: str,
    ftype: Any,
    prefix_opt: Any,
    rename: Any,
    *,
    dynamic_by_alias: bool,
    stack: frozenset[Type[Any]],
    cls_name: str,
) -> _FieldWire:
    if prefix_opt not in (None, False) and rename is not None:
        raise BadFlatten(
            f"Field {fname!r} in {cls_name} cannot use both flatten_prefix "
            "and flatten_rename; they are mutually exclusive"
        )
    prefix = _normalize_prefix(fname, prefix_opt, cls_name)
    rename_map = _normalize_rename(fname, rename, cls_name)
    inner_type = _flatten_inner_type(builder, fname, ftype, cls_name)
    origin = get_type_origin(inner_type)
    if origin in stack:
        raise BadFlatten(
            f"Cyclic flatten of {type_name(origin, short=True)} "
            f"via field {fname!r} in {cls_name}"
        )
    child_builder, child_origin = _child_builder(builder, inner_type)
    child_dynamic = dynamic_by_alias and (
        child_builder.is_code_generation_option_enabled(
            TO_DICT_ADD_BY_ALIAS_FLAG
        )
    )
    child_field_types = child_builder.get_field_types(include_extras=True)
    child_wires = _analyze_fields(
        child_builder,
        child_field_types,
        dynamic_by_alias=child_dynamic,
        stack=stack | {child_origin},
    )
    _validate_rename_keys(fname, rename_map, child_wires, cls_name)
    use_rename = rename_map is not None
    binding_map: dict[str, str] = {}
    ser_map: dict[str, str] = {}
    for child_name, child in child_wires.items():
        if use_rename and child_name in rename_map:
            target = rename_map[child_name]
            lookup = _primary_lookup(child)
            binding_map[target] = lookup
            for key, _lookup in child.accepted:
                ser_map[key] = target
            continue
        for key, lookup in child.accepted:
            binding_map[prefix + key] = lookup
    keys = set(binding_map)
    plan = FlattenPlan(
        fname=fname,
        inner_type=inner_type,
        could_be_none=_could_be_none(builder, fname, ftype),
        prefix=prefix,
        ser_map=ser_map if use_rename else {},
        bindings=list(binding_map.items()),
        input_keys=set(keys),
    )
    # The enclosing from_dict looks these keys up directly. Translating
    # them into the child dict happens in this field's unpacker.
    return _FieldWire(
        fname=fname,
        flattened=True,
        accepted=[(key, key) for key in sorted(keys)],
        keys=keys,
        plan=plan,
        lookup=fname,
    )


def _primary_lookup(wire: _FieldWire) -> str:
    return wire.lookup


def _plain_wire(
    builder: "CodeBuilder",
    fname: str,
    ftype: Any,
    metadata: Mapping[str, Any],
    *,
    dynamic_by_alias: bool,
) -> _FieldWire:
    config = builder.get_config()
    alias = builder.field_alias(fname, ftype, metadata, config)
    serialize_by_alias = bool(
        builder.get_dialect_or_config_option("serialize_by_alias", False)
    )
    allow_plain_name = bool(config.allow_deserialization_not_by_alias)
    if alias:
        lookup = alias
        if dynamic_by_alias:
            ser_keys = {fname, alias}
        elif serialize_by_alias:
            ser_keys = {alias}
        else:
            ser_keys = {fname}
        accepted_keys = set(ser_keys)
        accepted_keys.add(alias)
        if allow_plain_name:
            accepted_keys.add(fname)
    else:
        lookup = fname
        accepted_keys = {fname}
    # Ser-only keys are translated to the lookup from_dict expects.
    # Deser keys pass through as themselves so alias handling is unchanged.
    accepted: list[tuple[str, str]] = []
    for key in accepted_keys:
        if alias and key == alias:
            accepted.append((key, alias))
        elif alias and key == fname and allow_plain_name:
            accepted.append((key, fname))
        else:
            accepted.append((key, lookup))
    # Stable order: deser lookup first so later writers prefer it on clashes.
    accepted.sort(key=lambda item: (item[0] != lookup, item[0]))
    return _FieldWire(
        fname=fname,
        flattened=False,
        accepted=accepted,
        keys={key for key, _lookup in accepted},
        lookup=lookup,
    )


def _normalize_prefix(fname: str, prefix_opt: Any, cls_name: str) -> str:
    if prefix_opt is None or prefix_opt is False:
        return ""
    if prefix_opt is True:
        return f"{fname}_"
    if isinstance(prefix_opt, str):
        return prefix_opt
    raise BadFlatten(
        f"flatten_prefix for field {fname!r} in {cls_name} must be a "
        "string or True"
    )


def _normalize_rename(
    fname: str, rename: Any, cls_name: str
) -> Optional[dict[str, str]]:
    if rename is None:
        return None
    if not isinstance(rename, Mapping):
        raise BadFlatten(
            f"flatten_rename for field {fname!r} in {cls_name} must be a "
            "mapping of field names to keys"
        )
    normalized: dict[str, str] = {}
    seen_targets: dict[str, str] = {}
    for src, dst in rename.items():
        if not isinstance(src, str) or not isinstance(dst, str):
            raise BadFlatten(
                f"Invalid flatten_rename key {src!r} for field {fname!r} "
                f"in {cls_name}"
            )
        if dst in seen_targets:
            raise BadFlatten(
                f"Duplicate flatten_rename target {dst!r} for field "
                f"{fname!r} in {cls_name}"
            )
        seen_targets[dst] = src
        normalized[src] = dst
    return normalized


def _validate_rename_keys(
    fname: str,
    rename_map: Optional[dict[str, str]],
    child_wires: dict[str, _FieldWire],
    cls_name: str,
) -> None:
    if not rename_map:
        return
    for src, _dst in rename_map.items():
        child = child_wires.get(src)
        if child is None:
            raise BadFlatten(
                f"Invalid flatten_rename key {src!r} for field {fname!r} "
                f"in {cls_name}"
            )
        if child.flattened:
            raise BadFlatten(
                f"Invalid flatten_rename key {src!r} for field {fname!r} "
                f"in {cls_name}: field is itself flattened"
            )


def _flatten_inner_type(
    builder: "CodeBuilder", fname: str, ftype: Any, cls_name: str
) -> Any:
    params = builder.get_field_resolved_type_params(fname)
    real = builder.get_real_type(fname, ftype)
    real = _strip_annotated(real)
    if is_optional(real, params):
        inner = not_none_type_arg(get_args(real), params)
        real = _strip_annotated(inner)
    origin = get_type_origin(real)
    if not isinstance(origin, type) or not is_dataclass(origin):
        raise BadFlatten(
            f"Field {fname!r} in {cls_name} cannot be flattened: "
            f"{type_name(real, short=True)} is not a dataclass"
        )
    return real


def _strip_annotated(typ: Any) -> Any:
    if is_annotated(typ):
        return get_type_origin(typ)
    return typ


def _could_be_none(builder: "CodeBuilder", fname: str, ftype: Any) -> bool:
    default = builder.get_field_default(fname)
    params = builder.get_field_resolved_type_params(fname)
    real = _strip_annotated(builder.get_real_type(fname, ftype))
    return bool(
        ftype in (Any, type(None), None)
        or is_type_var_any(real)
        or is_optional(real, params)
        or default is None
    )


def _child_builder(
    parent: "CodeBuilder", inner_type: Any
) -> tuple["CodeBuilder", Type[Any]]:
    from mashumaro.core.meta.code.builder import CodeBuilder

    origin = get_type_origin(inner_type)
    type_args = get_args(inner_type)
    if origin is inner_type:
        type_args = ()
    child = CodeBuilder(
        origin,
        type_args,
        dialect=parent.dialect,
        format_name=parent.format_name,
        default_dialect=parent.default_dialect,
    )
    child.reset()
    return child, origin


def _ensure_no_collisions(
    builder: "CodeBuilder", wires: dict[str, _FieldWire]
) -> None:
    cls_name = type_name(builder.cls, short=True)
    owner: dict[str, tuple[str, bool]] = {}
    discriminator = builder.get_discriminator(look_in_parents=True)
    if discriminator and discriminator.field:
        owner[discriminator.field] = (discriminator.field, False)
    for fname, wire in wires.items():
        for key in wire.keys:
            prev = owner.get(key)
            if prev is not None and (wire.flattened or prev[1]):
                raise BadFlatten(
                    f"Flattened keys collide on {key!r} "
                    f"(field {prev[0]!r} and field {fname!r}) in {cls_name}"
                )
            if prev is None:
                owner[key] = (fname, wire.flattened)
