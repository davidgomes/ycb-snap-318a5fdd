from __future__ import annotations

from collections.abc import Mapping, Sequence
from typing import Optional, Union

from ...model_tools.definitions import InputField
from ...name_style import NameStyle, convert_snake_style
from ...provider.essential import AggregateCannotProvide, CannotProvide
from ..name_layout.base import KeyPath

FieldAliasesInput = Mapping[str, Union[str, Sequence[str]]]
FieldKeyGroups = dict[KeyPath, dict[str, tuple[str, ...]]]
FieldAndPath = tuple[InputField, Optional[KeyPath]]


def _aliases_to_tuple(value: FieldAliasesInput) -> tuple[tuple[str, tuple[str, ...]], ...]:
    return tuple(
        (field_id, (aliases,) if isinstance(aliases, str) else tuple(aliases))
        for field_id, aliases in sorted(value.items())
    )


def aliases_tuple_to_mapping(value: tuple[tuple[str, tuple[str, ...]], ...]) -> dict[str, Union[str, tuple[str, ...]]]:
    return {
        field_id: aliases[0] if len(aliases) == 1 else aliases
        for field_id, aliases in value
    }


def _normalize_aliases(value: Union[str, Sequence[str]]) -> tuple[str, ...]:
    if isinstance(value, str):
        return (value,)
    return tuple(value)


def _generate_style_aliases(field_id: str, styles: Sequence[NameStyle]) -> tuple[str, ...]:
    result: list[str] = []
    for style in styles:
        try:
            generated = convert_snake_style(field_id, style)
        except ValueError:
            continue
        result.append(generated)
    return tuple(result)


def _dedupe_ordered(keys: Sequence[str]) -> tuple[str, ...]:
    seen: set[str] = set()
    result: list[str] = []
    for key in keys:
        if key not in seen:
            seen.add(key)
            result.append(key)
    return tuple(result)


def resolve_field_key_groups(
    fields_to_paths: Sequence[FieldAndPath[InputField]],
    explicit_aliases: FieldAliasesInput,
    alias_styles: Optional[Sequence[NameStyle]],
) -> FieldKeyGroups:
    per_level: FieldKeyGroups = {}
    per_level_primaries: dict[KeyPath, dict[str, str]] = {}
    per_level_explicit: dict[KeyPath, dict[str, tuple[str, ...]]] = {}

    for field, path in fields_to_paths:
        if path is None or not path or not isinstance(path[-1], str):
            continue

        dict_path = path[:-1]
        primary = path[-1]
        per_level.setdefault(dict_path, {})
        per_level_primaries.setdefault(dict_path, {})[field.id] = primary

        explicit = _normalize_aliases(explicit_aliases[field.id]) if field.id in explicit_aliases else ()
        per_level_explicit.setdefault(dict_path, {})[field.id] = explicit

        generated = _generate_style_aliases(field.id, alias_styles or ())
        ordered = _dedupe_ordered((primary, *explicit, *generated))

        for alias in explicit:
            if alias == primary:
                raise AggregateCannotProvide(
                    "Explicit alias must not be equal to the field primary key",
                    [
                        CannotProvide(
                            f"Field {field.id!r} has explicit alias {alias!r} equal to its primary key {primary!r}",
                            is_demonstrative=True,
                        ),
                    ],
                    is_terminal=True,
                    is_demonstrative=True,
                )

        if len(ordered) > 1:
            per_level[dict_path][field.id] = ordered

    _validate_cross_field_collisions(per_level, per_level_primaries, per_level_explicit)
    return per_level


def _validate_cross_field_collisions(
    per_level: FieldKeyGroups,
    per_level_primaries: dict[KeyPath, dict[str, str]],
    per_level_explicit: dict[KeyPath, dict[str, tuple[str, ...]]],
) -> None:
    errors: list[CannotProvide] = []

    for dict_path, groups in per_level.items():
        primaries = per_level_primaries[dict_path]
        primary_keys = set(primaries.values())
        alias_to_field: dict[str, str] = {}

        for field_id, group in groups.items():
            primary = primaries[field_id]
            for alias in group[1:]:
                if alias in primary_keys and alias != primary:
                    errors.append(
                        CannotProvide(
                            f"Alias {alias!r} of field {field_id!r} collides with primary key of another field",
                            is_demonstrative=True,
                        ),
                    )
                if alias in alias_to_field and alias_to_field[alias] != field_id:
                    errors.append(
                        CannotProvide(
                            f"Alias {alias!r} is used by fields {alias_to_field[alias]!r} and {field_id!r}",
                            is_demonstrative=True,
                        ),
                    )
                alias_to_field[alias] = field_id

    if errors:
        raise AggregateCannotProvide(
            "Invalid field aliases",
            errors,
            is_terminal=True,
            is_demonstrative=True,
        )


def get_known_keys_at_level(
    map_keys: Sequence[str],
    field_key_groups: Mapping[str, tuple[str, ...]],
) -> set[str]:
    known = set(map_keys)
    for group in field_key_groups.values():
        known.update(group)
    return known
