"""Select, persist, and reapply the raw feature schema used by a fitted model."""

from pathlib import Path

import joblib
import pandas as pd

_DROPPED_KEYS = ("excluded", "constant", "duplicate")


class FeatureSchemaError(ValueError):
    """Invalid feature configuration or input columns for a saved schema."""


def build_feature_schema(dataset, features_config, target_columns=None):
    """
    Resolve ``dataset.features`` against a raw training frame.

    ``include`` fixes raw feature order. ``exclude`` removes raw columns.
    Constant columns are dropped when ``drop_constant`` is true. Duplicate
    columns are canonicalized when ``drop_duplicate`` is true: the first
    surviving column is kept and later matches are recorded as its aliases.
    """
    if not isinstance(features_config, dict):
        raise FeatureSchemaError(
            "dataset.features must be a mapping with include, exclude, "
            "drop_constant, and drop_duplicate"
        )

    targets = [] if not target_columns else list(target_columns)
    target_set = set(targets)
    raw_columns = [column for column in dataset.columns if column not in target_set]

    include = _normalize_name_spec(
        features_config.get("include", None),
        "include",
        dataset.columns,
        target_set,
    )
    exclude = _normalize_name_spec(
        features_config.get("exclude", None),
        "exclude",
        dataset.columns,
        target_set,
    )
    drop_constant = _optional_bool(features_config, "drop_constant")
    drop_duplicate = _optional_bool(features_config, "drop_duplicate")

    excluded_set = set(exclude)
    if include is None:
        selected = [column for column in raw_columns if column not in excluded_set]
    else:
        selected = [column for column in include if column not in excluded_set]

    constant = []
    if drop_constant:
        kept = []
        for column in selected:
            if _is_constant(dataset[column]):
                constant.append(column)
            else:
                kept.append(column)
        selected = kept

    duplicate = []
    duplicate_feature_aliases = {}
    if drop_duplicate:
        canonical = []
        for column in selected:
            match = next(
                (
                    kept
                    for kept in canonical
                    if columns_agree(dataset[column], dataset[kept])
                ),
                None,
            )
            if match is None:
                canonical.append(column)
            else:
                duplicate.append(column)
                duplicate_feature_aliases.setdefault(match, []).append(column)
        selected = canonical

    if not selected:
        raise FeatureSchemaError(
            "Feature configuration removed every feature"
        )

    return {
        "input_features": list(selected),
        "dropped_features": {
            "excluded": list(exclude),
            "constant": list(constant),
            "duplicate": list(duplicate),
        },
        "duplicate_feature_aliases": duplicate_feature_aliases,
    }


def apply_feature_schema(dataset, schema):
    """
    Project a raw frame onto the persisted canonical feature order.

    Extra columns are ignored. A canonical feature may be supplied directly
    or by any recorded alias. When more than one of those sources is present
    they must agree on every row.
    """
    if not isinstance(schema, dict) or "input_features" not in schema:
        raise FeatureSchemaError(
            "Persisted feature schema is missing input_features"
        )

    input_features = list(schema.get("input_features") or [])
    alias_map = schema.get("duplicate_feature_aliases") or {}
    resolved = {}
    missing = []

    for feature in input_features:
        sources = []
        if feature in dataset.columns:
            sources.append(feature)
        for alias in alias_map.get(feature, []):
            if alias in dataset.columns and alias not in sources:
                sources.append(alias)
        if not sources:
            missing.append(feature)
            continue
        conflicts = _conflicting_sources(dataset, sources)
        if conflicts:
            raise FeatureSchemaError(
                "Duplicate feature columns disagree row-wise: "
                f"{conflicts}"
            )
        resolved[feature] = dataset[sources[0]].to_numpy()

    if missing:
        raise FeatureSchemaError(f"Missing required feature(s): {missing}")

    return pd.DataFrame(resolved, index=dataset.index)


def columns_agree(left, right):
    """Return True when two columns match on every row."""
    if len(left) != len(right):
        return False
    if left.equals(right):
        return True
    try:
        both_missing = left.isna() & right.isna()
        return bool((left.eq(right) | both_missing).all())
    except Exception:
        return False


def save_feature_schema(schema, path):
    schema_path = Path(path)
    schema_path.parent.mkdir(parents=True, exist_ok=True)
    with open(schema_path, "wb") as handle:
        joblib.dump(schema, handle)
    return schema_path


def load_feature_schema(description, description_file=None):
    """
    Load the schema written at fit time.

    The joblib file is preferred. ``description.json`` is used when the file
    has been moved next to the description or only the recorded fields remain.
    """
    if not isinstance(description, dict):
        raise FeatureSchemaError(
            "description.json does not contain a feature schema"
        )

    schema_path = description.get("feature_schema_path")
    candidates = []
    if schema_path:
        candidates.append(Path(schema_path))
        if description_file:
            candidates.append(
                Path(description_file).parent / Path(schema_path).name
            )
    for candidate in candidates:
        if candidate.is_file():
            schema = joblib.load(str(candidate))
            if isinstance(schema, dict) and schema.get("input_features"):
                return schema

    input_features = description.get("input_features")
    if input_features:
        dropped = description.get("dropped_features") or {}
        return {
            "input_features": list(input_features),
            "dropped_features": {
                key: list(dropped.get(key) or []) for key in _DROPPED_KEYS
            },
            "duplicate_feature_aliases": dict(
                description.get("duplicate_feature_aliases") or {}
            ),
        }

    if schema_path:
        raise FeatureSchemaError(
            f"Could not load feature schema from {schema_path}"
        )
    return None


def input_width_from_description(description):
    """Return the model input width recorded in ``description.json``."""
    if not isinstance(description, dict):
        raise FeatureSchemaError(
            "description.json does not include an input width"
        )

    shape = description.get("train_data_shape")
    if isinstance(shape, (list, tuple)) and len(shape) >= 2:
        return int(shape[1])

    input_features = description.get("input_features")
    if isinstance(input_features, list):
        return len(input_features)

    raise FeatureSchemaError(
        "description.json does not include an input width"
    )


def _normalize_name_spec(value, field, columns, target_set):
    if value is None:
        return None if field == "include" else []
    if isinstance(value, str):
        names = [value]
    elif isinstance(value, list):
        names = list(value)
    else:
        raise FeatureSchemaError(
            f"{field} must be a column name or a list of unique "
            "non-empty raw feature names"
        )

    normalized = []
    for name in names:
        if not isinstance(name, str) or not name.strip():
            raise FeatureSchemaError(
                f"{field} contains an empty feature name"
            )
        normalized.append(name)

    duplicates = []
    seen = set()
    for name in normalized:
        if name in seen and name not in duplicates:
            duplicates.append(name)
        seen.add(name)
    if duplicates:
        raise FeatureSchemaError(
            f"Duplicate entries in {field}: {duplicates}"
        )

    unknown = [name for name in normalized if name not in columns]
    if unknown:
        raise FeatureSchemaError(f"Unknown feature(s) in {field}: {unknown}")

    targets = [name for name in normalized if name in target_set]
    if targets:
        raise FeatureSchemaError(
            f"Target column(s) in {field} are not allowed: {targets}"
        )
    return normalized


def _optional_bool(config, key):
    if key not in config or config[key] is None:
        return False
    value = config[key]
    if isinstance(value, bool):
        return value
    raise FeatureSchemaError(f"{key} must be a boolean")


def _is_constant(series):
    return int(series.nunique(dropna=True)) <= 1


def _conflicting_sources(dataset, sources):
    conflicting = []
    for index, left in enumerate(sources):
        for right in sources[index + 1 :]:
            if not columns_agree(dataset[left], dataset[right]):
                if left not in conflicting:
                    conflicting.append(left)
                if right not in conflicting:
                    conflicting.append(right)
    return conflicting
