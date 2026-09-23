"""Raw feature selection schema persisted with a fitted igel model."""

from pathlib import Path

import joblib
import pandas as pd


class FeatureSchemaError(ValueError):
    """Invalid feature configuration or inference data that breaks the schema."""


def build_feature_schema(dataset, features_config, target_columns):
    """
    Validate ``dataset.features`` and select model inputs from raw columns.

    ``include`` whitelists columns and fixes their order. ``exclude`` removes
    raw columns. Constant columns are dropped when ``drop_constant`` is set.
    Duplicate columns are canonicalized when ``drop_duplicate`` is set: the
    first surviving column is kept and later copies are recorded as aliases.
    """
    if not isinstance(features_config, dict):
        raise FeatureSchemaError(
            "dataset.features must be an object with include, exclude, "
            "drop_constant, and drop_duplicate"
        )

    targets = [column for column in (target_columns or [])]
    raw_columns = [str(column) for column in dataset.columns]
    known = set(raw_columns)

    include = _normalize_name_list(
        features_config.get("include"), "include", known, targets
    )
    exclude = _normalize_name_list(
        features_config.get("exclude"), "exclude", known, targets
    )

    drop_constant = _as_bool(
        features_config.get("drop_constant"), "drop_constant"
    )
    drop_duplicate = _as_bool(
        features_config.get("drop_duplicate"), "drop_duplicate"
    )

    target_set = set(targets)
    if include is not None:
        selected = list(include)
    else:
        selected = [column for column in raw_columns if column not in target_set]

    excluded = list(exclude or [])
    if excluded:
        excluded_set = set(excluded)
        selected = [column for column in selected if column not in excluded_set]

    constant = []
    if drop_constant:
        constant = [
            column for column in selected if _is_constant(dataset[column])
        ]
        constant_set = set(constant)
        selected = [column for column in selected if column not in constant_set]

    duplicate_aliases = {}
    duplicates = []
    if drop_duplicate:
        canonical = []
        for column in selected:
            match = next(
                (
                    earlier
                    for earlier in canonical
                    if _series_equal(dataset[column], dataset[earlier])
                ),
                None,
            )
            if match is None:
                canonical.append(column)
            else:
                duplicate_aliases.setdefault(match, []).append(column)
                duplicates.append(column)
        selected = canonical

    if not selected:
        raise FeatureSchemaError(
            "feature selection removed every feature"
        )

    return {
        "input_features": selected,
        "dropped_features": {
            "excluded": excluded,
            "constant": constant,
            "duplicate": duplicates,
        },
        "duplicate_feature_aliases": duplicate_aliases,
    }


def apply_feature_schema(dataset, schema):
    """
    Project raw inference columns onto the persisted input feature order.

    Extra columns are ignored. A canonical feature may be supplied directly or
    by any recorded alias. When more than one of those sources is present,
    every row must agree.
    """
    input_features = list(schema.get("input_features") or [])
    aliases = schema.get("duplicate_feature_aliases") or {}
    available = set(dataset.columns)

    missing = []
    conflicts = []
    resolved = {}
    for feature in input_features:
        sources = []
        if feature in available:
            sources.append(feature)
        for alias in aliases.get(feature, []):
            if alias in available:
                sources.append(alias)
        if not sources:
            missing.append(feature)
            continue
        disagree = _disagreeing_columns(dataset, sources)
        if disagree:
            conflicts.append((feature, disagree))
            continue
        resolved[feature] = dataset[sources[0]]

    problems = []
    if missing:
        problems.append(f"missing required features: {missing}")
    for feature, columns in conflicts:
        problems.append(
            f"conflicting columns {columns} for feature '{feature}'"
        )
    if problems:
        raise FeatureSchemaError("; ".join(problems))

    return pd.DataFrame(resolved, index=dataset.index)[input_features]


def save_feature_schema(schema, path):
    path = Path(path)
    joblib.dump(schema, str(path))
    return path


def load_feature_schema(path, fallback_dir=None):
    candidate = Path(path)
    if not candidate.exists() and fallback_dir is not None:
        candidate = Path(fallback_dir) / candidate.name
    if not candidate.exists():
        raise FeatureSchemaError(f"feature schema file not found: {path}")
    schema = joblib.load(str(candidate))
    if not isinstance(schema, dict) or "input_features" not in schema:
        raise FeatureSchemaError(f"invalid feature schema file: {candidate}")
    return schema


def input_width_from_description(description):
    """Return the model input width recorded in description.json."""
    if not isinstance(description, dict):
        raise FeatureSchemaError(
            "description.json must be an object to derive the export input width"
        )
    shape = description.get("train_data_shape")
    if shape is not None and len(shape) >= 2 and shape[1] is not None:
        return int(shape[1])
    input_features = description.get("input_features")
    if input_features is not None:
        return len(input_features)
    raise FeatureSchemaError(
        "description.json is missing train_data_shape needed to derive "
        "the export input width"
    )


def _normalize_name_list(value, field, known_columns, target_columns):
    if value is None:
        return None
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
        if name in seen:
            duplicates.append(name)
        else:
            seen.add(name)
            cleaned.append(name)
    if duplicates:
        raise FeatureSchemaError(
            f"duplicated {field} entries: {sorted(set(duplicates))}"
        )

    target_hits = [name for name in cleaned if name in set(target_columns)]
    if target_hits:
        raise FeatureSchemaError(
            f"target columns in {field}: {target_hits}"
        )

    unknown = [name for name in cleaned if name not in known_columns]
    if unknown:
        raise FeatureSchemaError(f"unknown {field} entries: {unknown}")
    return cleaned


def _as_bool(value, field):
    if value is None:
        return False
    if isinstance(value, bool):
        return value
    raise FeatureSchemaError(f"{field} must be a boolean")


def _is_constant(series):
    return int(series.nunique(dropna=False)) <= 1


def _series_equal(left, right):
    left_values = left.reset_index(drop=True)
    right_values = right.reset_index(drop=True)
    if len(left_values) != len(right_values):
        return False
    both_missing = left_values.isna() & right_values.isna()
    try:
        equal = left_values.eq(right_values)
    except TypeError:
        equal = left_values.astype(str).eq(right_values.astype(str))
    return bool((equal.fillna(False) | both_missing).all())


def _disagreeing_columns(dataset, columns):
    disagree = set()
    for index, left in enumerate(columns):
        for right in columns[index + 1 :]:
            if not _series_equal(dataset[left], dataset[right]):
                disagree.add(left)
                disagree.add(right)
    return [column for column in columns if column in disagree]
