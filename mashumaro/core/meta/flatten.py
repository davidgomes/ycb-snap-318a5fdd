"""Flatten nested dataclass fields into the parent dictionary."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Mapping, Optional, Type

from mashumaro.core.meta.helpers import (
    get_args,
    get_type_origin,
    is_annotated,
    is_optional,
    not_none_type_arg,
    type_name,
)
from mashumaro.exceptions import BadFlatten

# Imported lazily from builder to avoid a cycle at module import time.
# CodeBuilder is only needed as a type of object we call methods on.


@dataclass
class FlattenSpec:
    fname: str
    optional: bool
    origin: Type[Any]
    # Keys that child.to_dict() may emit -> keys written on the parent.
    key_map: dict[str, str]
    # Parent key -> key expected by child.from_dict().
    reverse: dict[str, str]
    output_keys: set[str]


def _unwrap(typ: Any) -> tuple[Any, bool]:
    optional = False
    while is_annotated(typ):
        typ = get_args(typ)[0]
    if is_optional(typ):
        optional = True
        typ = not_none_type_arg(get_args(typ))
        while typ is not None and is_annotated(typ):
            typ = get_args(typ)[0]
    return typ, optional


def _prefix_for(fname: str, raw: Any) -> str:
    if raw is None:
        return ""
    if raw is True:
        return f"{fname}_"
    if isinstance(raw, str):
        return raw
    raise BadFlatten(
        f"flatten_prefix for field {fname!r} must be a string or True"
    )


def _serialized_names(
    builder: Any, fname: str, ftype: Any
) -> tuple[set[str], str]:
    """Possible dict keys for a field, and the key from_dict looks up."""
    from mashumaro.core.meta.code.builder import CodeBuilder

    metadata = builder.metadatas.get(fname, {})
    alias = CodeBuilder._CodeBuilder__get_field_alias(  # type: ignore
        fname, ftype, metadata, builder.get_config()
    )
    names = {fname}
    if alias:
        names.add(alias)
    read_key = alias or fname
    return names, read_key


@dataclass
class _Contribution:
    """One logical field as it shows up in a class's own to_dict/from_dict."""

    source: str
    dict_keys: set[str]
    read_key: str
    flattened_field: bool


def _contributions(
    builder: Any, stack: tuple[Type[Any], ...]
) -> list[_Contribution]:
    result: list[_Contribution] = []
    for fname, ftype in builder.get_field_types(include_extras=True).items():
        metadata = builder.metadatas.get(fname, {})
        if metadata.get("flatten"):
            spec = _spec_for_field(builder, fname, ftype, metadata, stack)
            for out_key in spec.reverse:
                result.append(
                    _Contribution(
                        source=fname,
                        dict_keys={out_key},
                        read_key=out_key,
                        flattened_field=True,
                    )
                )
            continue
        names, read_key = _serialized_names(builder, fname, ftype)
        result.append(
            _Contribution(
                source=fname,
                dict_keys=names,
                read_key=read_key,
                flattened_field=False,
            )
        )
    return result


def _spec_for_field(
    builder: Any,
    fname: str,
    ftype: Any,
    metadata: Mapping[str, Any],
    stack: tuple[Type[Any], ...],
) -> FlattenSpec:
    from dataclasses import is_dataclass

    from mashumaro.core.meta.code.builder import CodeBuilder

    prefix_raw = metadata.get("flatten_prefix", None)
    rename = metadata.get("flatten_rename", None)
    if prefix_raw is not None and rename is not None:
        raise BadFlatten(
            f"Field {fname!r} of {type_name(builder.cls, short=True)}: "
            "flatten_prefix and flatten_rename are mutually exclusive"
        )
    if rename is not None and not isinstance(rename, dict):
        raise BadFlatten(
            f"Field {fname!r} of {type_name(builder.cls, short=True)}: "
            "flatten_rename must be a dict of field names to keys"
        )
    prefix = _prefix_for(fname, prefix_raw)

    inner_type, optional = _unwrap(ftype)
    if inner_type is None:
        raise BadFlatten(
            f"Field {fname!r} of {type_name(builder.cls, short=True)} "
            "cannot be flattened: type is not a dataclass"
        )
    origin = get_type_origin(inner_type)
    if not is_dataclass(origin):
        raise BadFlatten(
            f"Field {fname!r} of {type_name(builder.cls, short=True)} "
            "cannot be flattened: type is not a dataclass"
        )
    if origin in stack:
        raise BadFlatten(
            f"Field {fname!r} of {type_name(builder.cls, short=True)} "
            "cannot be flattened: circular flatten"
        )

    child_builder = CodeBuilder(
        origin,
        dialect=builder.dialect,
        format_name=builder.format_name,
        default_dialect=builder.default_dialect,
    )
    # Touch config so a bad dialect is reported the same way as elsewhere.
    child_builder.get_config()
    direct_names = set(child_builder.get_field_types().keys())
    if rename is not None:
        unknown = [k for k in rename if k not in direct_names]
        if unknown:
            raise BadFlatten(
                f"Field {fname!r} of {type_name(builder.cls, short=True)}: "
                f"invalid flatten_rename keys: {', '.join(map(str, unknown))}"
            )
        values = list(rename.values())
        if len(values) != len(set(values)):
            raise BadFlatten(
                f"Field {fname!r} of {type_name(builder.cls, short=True)}: "
                "duplicate flatten_rename values"
            )
        non_strings = [k for k, v in rename.items() if not isinstance(v, str)]
        if non_strings:
            raise BadFlatten(
                f"Field {fname!r} of {type_name(builder.cls, short=True)}: "
                "flatten_rename values must be strings"
            )
        flattened_children = set()
        for child_name, child_type in child_builder.get_field_types(
            include_extras=True
        ).items():
            child_meta = child_builder.metadatas.get(child_name, {})
            if child_meta.get("flatten") and child_name in rename:
                flattened_children.add(child_name)
        if flattened_children:
            raise BadFlatten(
                f"Field {fname!r} of {type_name(builder.cls, short=True)}: "
                "invalid flatten_rename keys: "
                + ", ".join(sorted(flattened_children))
            )

    key_map: dict[str, str] = {}
    reverse: dict[str, str] = {}
    owned: dict[str, str] = {}

    child_parts = _contributions(child_builder, stack + (origin,))
    for part in child_parts:
        if (
            rename is not None
            and part.source in rename
            and not part.flattened_field
        ):
            out = rename[part.source]
            for dict_key in part.dict_keys:
                key_map[dict_key] = out
            if out in reverse and reverse[out] != part.read_key:
                raise BadFlatten(
                    f"Flattened key collision in "
                    f"{type_name(builder.cls, short=True)}: {out!r}"
                )
            reverse[out] = part.read_key
            prev = owned.get(out)
            if prev is not None and prev != part.source:
                raise BadFlatten(
                    f"Flattened key collision in "
                    f"{type_name(builder.cls, short=True)}: {out!r}"
                )
            owned[out] = part.source
            continue
        for dict_key in part.dict_keys:
            out = dict_key if rename is not None else prefix + dict_key
            key_map[dict_key] = out
            # Prefer the key from_dict actually looks up when several
            # serialized forms share one output key.
            read_as = part.read_key if out not in reverse else reverse[out]
            if rename is None:
                # Distinct prefixed forms stay distinct and are forwarded
                # under the name from_dict expects for that form. When the
                # form is the field name but from_dict wants the alias,
                # store it under the alias so both forms load.
                read_as = part.read_key
            reverse[out] = read_as
            prev = owned.get(out)
            source = part.source
            if prev is not None and prev != source:
                raise BadFlatten(
                    f"Flattened key collision in "
                    f"{type_name(builder.cls, short=True)}: {out!r}"
                )
            owned[out] = source

    return FlattenSpec(
        fname=fname,
        optional=optional,
        origin=origin,
        key_map=key_map,
        reverse=reverse,
        output_keys=set(reverse),
    )


def collect_flatten_specs(builder: Any) -> dict[str, FlattenSpec]:
    """Validate flatten options and return specs keyed by parent field name.

    Also returns nothing for non-flattened fields, but checks that flattened
    keys do not collide with any other field name or alias.
    """
    specs: dict[str, FlattenSpec] = {}
    occupied: dict[str, str] = {}

    field_types = builder.get_field_types(include_extras=True)
    for fname, ftype in field_types.items():
        metadata = builder.metadatas.get(fname, {})
        flatten = metadata.get("flatten")
        prefix_raw = metadata.get("flatten_prefix", None)
        rename = metadata.get("flatten_rename", None)
        if not flatten:
            if prefix_raw is not None or rename is not None:
                raise BadFlatten(
                    f"Field {fname!r} of {type_name(builder.cls, short=True)}: "
                    "flatten_prefix and flatten_rename require flatten=True"
                )
            names, _read = _serialized_names(builder, fname, ftype)
            for key in names:
                prev = occupied.get(key)
                if prev is not None and prev != fname:
                    # Only report collisions that involve a flattened field.
                    # Pre-existing alias clashes are left to runtime.
                    pass
                else:
                    occupied[key] = fname
            continue
        spec = _spec_for_field(builder, fname, ftype, metadata, ())
        specs[fname] = spec

    # Second pass: flattened outputs against every occupied key.
    for fname, spec in specs.items():
        for key in spec.output_keys:
            prev = occupied.get(key)
            if prev is not None and prev != fname:
                raise BadFlatten(
                    f"Flattened key collision in "
                    f"{type_name(builder.cls, short=True)}: {key!r} "
                    f"from field {fname!r} and field {prev!r}"
                )
            occupied[key] = fname
        # A flattened field does not occupy its own python name unless that
        # name is also one of the merged keys (already recorded).
    return specs
