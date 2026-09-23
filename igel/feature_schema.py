"""Select, persist, and reapply the raw feature schema used at fit time."""

from __future__ import annotations


class FeatureSchemaError(ValueError):
    """Invalid feature configuration or input that does not match a saved schema."""


def _name_list(value, field: str) -> list:
    if value is None:
        return []
    if isinstance(value, str):
        names = [value]
    elif isinstance(value, (list, tuple)):
        names = list(value)
    else:
        raise FeatureSchemaError(
            f"{field} must be a column name or a list of unique "
            f"non-empty raw feature names"
        )

    cleaned = []
    seen = set()
    duplicates = []
    for name in names:
        if not isinstance(name, str) or name.strip() == "":
            raise FeatureSchemaError(
                f"{field} entries must be unique non-empty raw feature names"
            )
        if name in seen and name not in duplicates:
            duplicates.append(name)
        seen.add(name)
        cleaned.append(name)
    if duplicates:
        raise FeatureSchemaError(
            f"duplicated {field} entries: {duplicates}"
        )
    return cleaned


def _validate_known_features(names, field, columns, targets):
    target_hits = [name for name in names if name in targets]
    if target_hits:
        raise FeatureSchemaError(
            f"target columns are not allowed in {field}: {target_hits}"
        )
    unknown = [name for name in names if name not in columns]
    if unknown:
        raise FeatureSchemaError(f"unknown {field} entries: {unknown}")


def _flag(config: dict, field: str) -> bool:
    if field not in config or config[field] is None:
        return False
    value = config[field]
    if not isinstance(value, bool):
        raise FeatureSchemaError(f"{field} must be a boolean")
    return value


def _series_equal(left, right) -> bool:
    return left.reset_index(drop=True).equals(right.reset_index(drop=True))


def build_feature_schema(dataset, features_config, target_columns=None) -> dict:
    """
    Resolve dataset.features against a training frame.

    include fixes raw feature order. exclude removes raw columns. Constant
    columns are dropped when drop_constant is true. Duplicate columns are
    canonicalized by keeping the first surviving column when drop_duplicate
    is true.
    """
    if not isinstance(features_config, dict):
        raise FeatureSchemaError(
            "dataset.features must be a mapping with include, exclude, "
            "drop_constant, and drop_duplicate"
        )

    targets = list(target_columns or [])
    columns = list(dataset.columns)
    raw_features = [column for column in columns if column not in targets]

    include = _name_list(features_config.get("include"), "include")
    exclude = _name_list(features_config.get("exclude"), "exclude")
    _validate_known_features(include, "include", columns, targets)
    _validate_known_features(exclude, "exclude", columns, targets)

    drop_constant = _flag(features_config, "drop_constant")
    drop_duplicate = _flag(features_config, "drop_duplicate")

    selected = list(include) if include else list(raw_features)
    exclude_set = set(exclude)
    dropped_excluded = [column for column in selected if column in exclude_set]
    selected = [column for column in selected if column not in exclude_set]

    dropped_constant = []
    if drop_constant:
        kept = []
        for column in selected:
            if dataset[column].nunique(dropna=False) <= 1:
                dropped_constant.append(column)
            else:
                kept.append(column)
        selected = kept

    duplicate_feature_aliases = {}
    dropped_duplicate = []
    if drop_duplicate:
        canonical = []
        for column in selected:
            match = next(
                (
                    kept
                    for kept in canonical
                    if _series_equal(dataset[column], dataset[kept])
                ),
                None,
            )
            if match is None:
                canonical.append(column)
            else:
                duplicate_feature_aliases.setdefault(match, []).append(column)
                dropped_duplicate.append(column)
        selected = canonical

    if not selected:
        raise FeatureSchemaError(
            "feature configuration removes every feature"
        )

    return {
        "input_features": selected,
        "dropped_features": {
            "excluded": dropped_excluded,
            "constant": dropped_constant,
            "duplicate": dropped_duplicate,
        },
        "duplicate_feature_aliases": duplicate_feature_aliases,
    }


def apply_feature_schema(dataset, schema):
    """
    Project a frame onto the persisted input features.

    Extra raw columns are ignored. A canonical feature may be satisfied by
    any recorded alias. When more than one source is present, every source
    must agree on every row.
    """
    import pandas as pd

    input_features = list(schema.get("input_features") or [])
    aliases = schema.get("duplicate_feature_aliases") or {}
    missing = []
    data = {}

    for feature in input_features:
        sources = [feature, *list(aliases.get(feature) or [])]
        present = [name for name in sources if name in dataset.columns]
        if not present:
            missing.append(feature)
            continue

        conflicts = []
        for index, left in enumerate(present):
            for right in present[index + 1 :]:
                if not _series_equal(dataset[left], dataset[right]):
                    for name in (left, right):
                        if name not in conflicts:
                            conflicts.append(name)
        if conflicts:
            raise FeatureSchemaError(
                f"conflicting columns {conflicts} do not agree row-wise "
                f"for feature '{feature}'"
            )

        chosen = feature if feature in dataset.columns else present[0]
        data[feature] = dataset[chosen].reset_index(drop=True)

    if missing:
        raise FeatureSchemaError(f"missing required features: {missing}")

    return pd.DataFrame(data)


def project_with_targets(dataset, schema, target_columns=None):
    """Apply the schema and keep any target columns that are present."""
    import pandas as pd

    selected = apply_feature_schema(dataset, schema)
    targets = [
        column
        for column in list(target_columns or [])
        if column in dataset.columns
    ]
    if not targets:
        return selected
    target_frame = dataset[targets].reset_index(drop=True)
    return pd.concat([selected, target_frame], axis=1)
