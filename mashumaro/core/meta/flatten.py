"""Plan ``flatten`` fields and reject invalid configurations."""

from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass
from typing import Any, Optional, Type

from mashumaro.exceptions import BadFlatten

LoadFields = Callable[[Type, tuple], tuple[str, Sequence["FieldView"]]]


@dataclass(frozen=True)
class FieldView:
    """One dataclass field, as seen while planning flatten keys."""

    name: str
    type_name: str
    alias: Optional[str]
    optional: bool
    init: bool
    serialize_omit: bool
    flatten: Any
    flatten_prefix: Any
    flatten_rename: Any
    child_type: Optional[Type]
    child_type_args: tuple
    serialize_by_alias: bool
    by_alias_flag: bool
    allow_not_by_alias: bool


@dataclass(frozen=True)
class KeyGroup:
    """Keys one logical value occupies in a class dictionary."""

    source: str
    direct_field: Optional[str]
    occupied: frozenset[str]
    emitted: frozenset[str]
    lookup: tuple[str, ...]
    unpack: bool


@dataclass
class FlattenPlan:
    """How one flattened field is packed into and unpacked from its parent."""

    fname: str
    child_type: Type
    type_args: tuple
    optional: bool
    prefix: str
    remap: Optional[dict[str, str]]
    bindings: list[tuple[tuple[str, ...], str]]
    input_keys: set[str]
    init: bool


def analyze_flatten(
    cls: Type,
    cls_name: str,
    fields: Sequence[FieldView],
    load_fields: LoadFields,
) -> dict[str, FlattenPlan]:
    _groups, plans = _walk(cls, cls_name, fields, load_fields, set())
    return plans


def _walk(
    cls: Type,
    cls_name: str,
    fields: Sequence[FieldView],
    load_fields: LoadFields,
    visiting: set[Type],
) -> tuple[list[KeyGroup], dict[str, FlattenPlan]]:
    if cls in visiting:
        raise BadFlatten(f"Cyclic flatten reference involving {cls_name}")
    visiting.add(cls)
    try:
        groups: list[KeyGroup] = []
        plans: dict[str, FlattenPlan] = {}
        for field in fields:
            if not _flatten_enabled(field, cls_name):
                groups.append(_normal_group(field, cls_name))
                continue
            lifted, plan = _expand_flatten(
                field, cls_name, load_fields, visiting
            )
            groups.extend(lifted)
            plans[field.name] = plan
        _check_collisions(groups, cls_name)
        return groups, plans
    finally:
        visiting.discard(cls)


def _flatten_enabled(field: FieldView, cls_name: str) -> bool:
    value = field.flatten
    prefix_set = _prefix_is_set(field.flatten_prefix)
    rename_set = _rename_is_set(field.flatten_rename)
    if value is None or value is False:
        if prefix_set or rename_set:
            raise BadFlatten(
                f"Field {field.name!r} in {cls_name} sets flatten_prefix "
                f"or flatten_rename but flatten is not enabled"
            )
        return False
    if value is not True:
        raise BadFlatten(
            f"Invalid flatten value {value!r} for field {field.name!r} "
            f"in {cls_name}: expected True or False"
        )
    return True


def _prefix_is_set(value: Any) -> bool:
    return value is not None and value is not False


def _rename_is_set(value: Any) -> bool:
    if value is None:
        return False
    if isinstance(value, Mapping) and len(value) == 0:
        return False
    return True


def _checked_prefix(field: FieldView, cls_name: str) -> Optional[Any]:
    value = field.flatten_prefix
    if value is None or value is False:
        return None
    if value is True or isinstance(value, str):
        return value
    raise BadFlatten(
        f"Invalid flatten_prefix {value!r} for field {field.name!r} "
        f"in {cls_name}: expected a string or True"
    )


def _checked_rename(
    field: FieldView, cls_name: str
) -> Optional[dict[str, str]]:
    value = field.flatten_rename
    if value is None:
        return None
    if not isinstance(value, dict):
        raise BadFlatten(
            f"Invalid flatten_rename {value!r} for field {field.name!r} "
            f"in {cls_name}: expected a dict"
        )
    if not value:
        return None
    normalized: dict[str, str] = {}
    for key, dst in value.items():
        if (
            not isinstance(key, str)
            or not isinstance(dst, str)
            or key == ""
            or dst == ""
        ):
            raise BadFlatten(
                f"Invalid flatten_rename key {key!r} for field "
                f"{field.name!r} in {cls_name}"
            )
        normalized[key] = dst
    return normalized


def _normal_group(field: FieldView, cls_name: str) -> KeyGroup:
    alias = field.alias
    fname = field.name
    canonical = alias or fname
    lookup = [canonical]
    if field.allow_not_by_alias and alias and fname != canonical:
        lookup.append(fname)
    if field.serialize_omit:
        emitted: set[str] = set()
    elif field.by_alias_flag and alias:
        emitted = {fname, alias}
    elif field.serialize_by_alias and alias:
        emitted = {alias}
    else:
        emitted = {fname}
    unpack = field.init
    occupied = set(emitted)
    if unpack:
        occupied.update(lookup)
    return KeyGroup(
        source=f"{cls_name}.{fname}",
        direct_field=fname,
        occupied=frozenset(occupied),
        emitted=frozenset(emitted),
        lookup=tuple(lookup),
        unpack=unpack,
    )


def _expand_flatten(
    field: FieldView,
    cls_name: str,
    load_fields: LoadFields,
    visiting: set[Type],
) -> tuple[list[KeyGroup], FlattenPlan]:
    prefix_opt = _checked_prefix(field, cls_name)
    rename = _checked_rename(field, cls_name)
    if prefix_opt is not None and rename is not None:
        raise BadFlatten(
            f"flatten_prefix and flatten_rename are mutually exclusive "
            f"for field {field.name!r} in {cls_name}"
        )
    child_type = field.child_type
    if child_type is None:
        raise BadFlatten(
            f"Field {field.name!r} of type {field.type_name} in "
            f"{cls_name} cannot be flattened because it is not a dataclass"
        )
    if prefix_opt is True:
        prefix = f"{field.name}_"
    elif isinstance(prefix_opt, str):
        prefix = prefix_opt
    else:
        prefix = ""

    child_name, child_fields = load_fields(child_type, field.child_type_args)
    if rename is not None:
        _validate_rename_keys(
            rename, child_fields, field.name, cls_name, child_name
        )
    child_groups, _child_plans = _walk(
        child_type,
        child_name,
        child_fields,
        load_fields,
        visiting,
    )

    bindings: list[tuple[tuple[str, ...], str]] = []
    input_keys: set[str] = set()
    remap: Optional[dict[str, str]] = {} if rename is not None else None
    lifted: list[KeyGroup] = []
    for group in child_groups:
        renamed_to = None
        if (
            rename is not None
            and group.direct_field
            and group.direct_field in rename
        ):
            renamed_to = rename[group.direct_field]
            if remap is not None:
                for emitted_key in group.emitted:
                    remap[emitted_key] = renamed_to
        if renamed_to is not None:
            emitted_out = (
                frozenset([renamed_to]) if group.emitted else frozenset()
            )
            lookup_out = (renamed_to,) if group.unpack else tuple()
        elif rename is not None:
            emitted_out = group.emitted
            lookup_out = group.lookup if group.unpack else tuple()
        else:
            emitted_out = frozenset(prefix + key for key in group.emitted)
            lookup_out = (
                tuple(prefix + key for key in group.lookup)
                if group.unpack
                else tuple()
            )
        occupied = set(emitted_out)
        occupied.update(lookup_out)
        lifted.append(
            KeyGroup(
                source=f"{cls_name}.{field.name} -> {group.source}",
                direct_field=None,
                occupied=frozenset(occupied),
                emitted=emitted_out,
                lookup=lookup_out,
                unpack=bool(field.init and group.unpack),
            )
        )
        if field.init and group.unpack and lookup_out:
            bindings.append((lookup_out, group.lookup[0]))
            input_keys.update(lookup_out)

    plan = FlattenPlan(
        fname=field.name,
        child_type=child_type,
        type_args=field.child_type_args,
        optional=field.optional,
        prefix=prefix,
        remap=remap,
        bindings=bindings,
        input_keys=input_keys,
        init=field.init,
    )
    return lifted, plan


def _validate_rename_keys(
    rename: dict[str, str],
    child_fields: Sequence[FieldView],
    fname: str,
    cls_name: str,
    child_name: str,
) -> None:
    by_name = {child.name: child for child in child_fields}
    seen_targets: dict[str, str] = {}
    for key, target in rename.items():
        if key not in by_name:
            raise BadFlatten(
                f"Invalid flatten_rename key {key!r} for field {fname!r} "
                f"in {cls_name}: not a field of {child_name}"
            )
        child = by_name[key]
        if child.flatten is True:
            raise BadFlatten(
                f"Invalid flatten_rename key {key!r} for field {fname!r} "
                f"in {cls_name}: {key!r} is itself flattened"
            )
        previous = seen_targets.get(target)
        if previous is not None:
            raise BadFlatten(
                f"Duplicate flatten_rename target {target!r} for keys "
                f"{previous!r} and {key!r} in field {fname!r} of {cls_name}"
            )
        seen_targets[target] = key


def _check_collisions(groups: Sequence[KeyGroup], cls_name: str) -> None:
    seen: dict[str, str] = {}
    for group in groups:
        for key in group.occupied:
            previous = seen.get(key)
            if previous is not None and previous != group.source:
                raise BadFlatten(
                    f"Key {key!r} collides between {previous!r} and "
                    f"{group.source!r} in {cls_name}"
                )
            seen[key] = group.source
