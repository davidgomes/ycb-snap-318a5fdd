"""Select raw columns and reuse that schema when scoring a fitted model."""

import json
from pathlib import Path

import joblib
import pandas as pd
from igel.constants import Constants


class FeatureSchemaError(ValueError):
    """Raised when feature selection configuration or inputs are invalid."""


def build_feature_schema(dataset, features_config, targets=None):
    """
    Build the raw feature schema used by a fitted model.

    include fixes the raw column order. exclude removes raw columns.
    Constant columns are dropped when requested, and duplicate columns are
    replaced by the first surviving column.
    """
    if not isinstance(features_config, dict):
        raise FeatureSchemaError(
            "dataset.features must be a mapping with include, exclude, "
            "drop_constant, and drop_duplicate"
        )

    targets = list(targets or [])
    columns = list(dataset.columns)
    include = features_config.get("include", None)
    exclude = features_config.get("exclude", None)
    include_names = (
        None
        if include is None
        else _validate_feature_names(include, "include", columns, targets)
    )
    exclude_names = (
        None
        if exclude is None
        else _validate_feature_names(exclude, "exclude", columns, targets)
    )

    if include_names is not None:
        candidates = list(include_names)
    else:
        target_names = set(targets)
        candidates = [
            column for column in columns if column not in target_names
        ]

    exclude_names = set(exclude_names or [])
    excluded = [column for column in candidates if column in exclude_names]
    candidates = [
        column for column in candidates if column not in exclude_names
    ]

    constant = []
    if _as_bool(features_config.get("drop_constant", False), "drop_constant"):
        kept = []
        for column in candidates:
            if _is_constant(dataset[column]):
                constant.append(column)
            else:
                kept.append(column)
        candidates = kept

    duplicate = []
    aliases = {}
    if _as_bool(features_config.get("drop_duplicate", False), "drop_duplicate"):
        kept = []
        for column in candidates:
            canonical = _matching_column(dataset, column, kept)
            if canonical is None:
                kept.append(column)
            else:
                duplicate.append(column)
                aliases.setdefault(canonical, []).append(column)
        candidates = kept

    if not candidates:
        raise FeatureSchemaError("Feature configuration removed every feature")

    return {
        "input_features": candidates,
        "dropped_features": {
            "excluded": excluded,
            "constant": constant,
            "duplicate": duplicate,
        },
        "duplicate_feature_aliases": aliases,
    }


def training_frame(dataset, schema, targets=None):
    """Return model inputs followed by any target columns that are present."""
    columns = list(schema["input_features"])
    for target in targets or []:
        if target not in columns:
            columns.append(target)
    present = [column for column in columns if column in dataset.columns]
    return dataset.loc[:, present].copy()


def apply_feature_schema(dataset, schema, keep_columns=None):
    """
    Map raw columns onto the fitted schema.

    Extra columns are ignored. A canonical feature or any recorded alias
    satisfies that input. When several of those sources are present, every
    row must agree.
    """
    if not isinstance(schema, dict) or not schema.get("input_features"):
        raise FeatureSchemaError(
            "Persisted feature schema is missing input_features"
        )

    input_features = list(schema["input_features"])
    groups = _alias_groups(schema)
    available = set(dataset.columns)
    missing = []
    sources_for = {}
    for feature in input_features:
        sources = _sources_for(feature, groups.get(feature, []), available)
        if not sources:
            missing.append(feature)
        else:
            sources_for[feature] = sources

    if missing:
        raise FeatureSchemaError(
            "Missing required features: "
            + ", ".join(str(name) for name in missing)
        )

    conflicts = []
    chosen = {}
    for feature in input_features:
        sources = sources_for[feature]
        base = sources[0]
        disagreed = [
            other
            for other in sources[1:]
            if not _values_agree(dataset[base], dataset[other])
        ]
        if disagreed:
            for name in [base] + disagreed:
                if name not in conflicts:
                    conflicts.append(name)
        else:
            chosen[feature] = base

    if conflicts:
        raise FeatureSchemaError(
            "Conflicting columns do not agree row-wise: "
            + ", ".join(str(name) for name in conflicts)
        )

    selected = pd.DataFrame(
        {
            feature: dataset[chosen[feature]].to_numpy()
            for feature in input_features
        },
        columns=input_features,
    )
    for column in keep_columns or []:
        if column in dataset.columns and column not in selected.columns:
            selected[column] = dataset[column].to_numpy()
    selected.index = dataset.index
    return selected


def load_persisted_feature_schema(
    schema_path=None, description_path=None, description=None
):
    """Load feature_schema.joblib, falling back to description fields."""
    candidates = []
    if schema_path:
        candidates.append(Path(schema_path))
    if description_path:
        candidates.append(
            Path(description_path).parent / Constants.feature_schema_file
        )

    for candidate in candidates:
        if candidate.is_file():
            with open(candidate, "rb") as handle:
                loaded = joblib.load(handle)
            if isinstance(loaded, dict) and loaded.get("input_features"):
                return loaded

    description = description or {}
    if description.get("input_features"):
        dropped = description.get("dropped_features") or {}
        return {
            "input_features": list(description["input_features"]),
            "dropped_features": {
                "excluded": list(dropped.get("excluded") or []),
                "constant": list(dropped.get("constant") or []),
                "duplicate": list(dropped.get("duplicate") or []),
            },
            "duplicate_feature_aliases": description.get(
                "duplicate_feature_aliases"
            )
            or {},
        }

    if schema_path:
        raise FeatureSchemaError(
            f"Could not load feature schema from {schema_path}"
        )
    return None


def load_description_for_export(model_path=None, description_file=None):
    """Read the description.json that belongs to a fitted model."""
    candidates = []
    if model_path:
        model = Path(model_path)
        if model.is_dir():
            candidates.append(model / Constants.description_file)
        candidates.append(model.parent / Constants.description_file)
    if description_file:
        candidates.append(Path(description_file))

    seen = set()
    for candidate in candidates:
        key = str(candidate)
        if key in seen:
            continue
        seen.add(key)
        if candidate.is_file():
            with open(candidate, "r", encoding="utf-8") as handle:
                loaded = json.load(handle)
            if isinstance(loaded, dict):
                return loaded

    raise FeatureSchemaError(
        "description.json was not found; export cannot derive the ONNX "
        "input width"
    )


def onnx_input_width(description):
    """Derive the model input width recorded in description.json."""
    if not isinstance(description, dict):
        raise FeatureSchemaError(
            "description.json must be an object to derive the ONNX input width"
        )

    shape = description.get("train_data_shape")
    if (
        isinstance(shape, (list, tuple))
        and len(shape) >= 2
        and shape[1] is not None
    ):
        return int(shape[1])

    features = description.get("input_features")
    if isinstance(features, (list, tuple)) and features:
        return len(features)

    raise FeatureSchemaError(
        "description.json must include train_data_shape or input_features "
        "to derive the ONNX input width"
    )


def _validate_feature_names(value, field, columns, targets):
    if isinstance(value, str):
        names = [value]
    elif isinstance(value, (list, tuple)):
        names = list(value)
    else:
        raise FeatureSchemaError(
            f"{field} must be a column name or a list of unique "
            "non-empty raw feature names"
        )

    invalid = [
        name
        for name in names
        if not isinstance(name, str) or name.strip() == ""
    ]
    if invalid:
        rendered = ", ".join(repr(name) for name in invalid)
        raise FeatureSchemaError(
            f"{field} entries must be unique non-empty raw feature "
            f"names: {rendered}"
        )

    duplicates = []
    seen = set()
    for name in names:
        if name in seen and name not in duplicates:
            duplicates.append(name)
        seen.add(name)
    if duplicates:
        raise FeatureSchemaError(
            "Duplicated {field} entries: {names}".format(
                field=field, names=", ".join(duplicates)
            )
        )

    target_hits = [name for name in names if name in set(targets)]
    if target_hits:
        raise FeatureSchemaError(
            "Target columns cannot be used in {field}: {names}".format(
                field=field, names=", ".join(target_hits)
            )
        )

    column_names = set(columns)
    unknown = [name for name in names if name not in column_names]
    if unknown:
        raise FeatureSchemaError(
            "Unknown {field} entries: {names}".format(
                field=field, names=", ".join(unknown)
            )
        )
    return names


def _as_bool(value, field):
    if isinstance(value, bool) or value is None:
        return bool(value)
    if isinstance(value, (int, float)):
        return value != 0
    if isinstance(value, str):
        lowered = value.strip().lower()
        if lowered in {"1", "true", "yes", "y", "t"}:
            return True
        if lowered in {"0", "false", "no", "n", "f", ""}:
            return False
    raise FeatureSchemaError(f"{field} must be a boolean")


def _is_constant(series):
    return int(pd.Series(series).nunique(dropna=False)) <= 1


def _values_agree(left, right):
    left_values = pd.Series(left).reset_index(drop=True)
    right_values = pd.Series(right).reset_index(drop=True)
    if len(left_values) != len(right_values):
        return False
    equal = left_values.eq(right_values)
    missing = left_values.isna() & right_values.isna()
    return bool((equal | missing).all())


def _matching_column(dataset, column, earlier_columns):
    for earlier in earlier_columns:
        if _values_agree(dataset[earlier], dataset[column]):
            return earlier
    return None


def _alias_groups(schema):
    raw = schema.get("duplicate_feature_aliases") or {}
    if not isinstance(raw, dict):
        raise FeatureSchemaError(
            "duplicate_feature_aliases must be a mapping of canonical "
            "features to alias names"
        )
    groups = {}
    for canonical, aliases in raw.items():
        if aliases is None:
            groups[canonical] = []
        elif isinstance(aliases, str):
            groups[canonical] = [aliases]
        elif isinstance(aliases, (list, tuple)):
            groups[canonical] = list(aliases)
        else:
            raise FeatureSchemaError(
                "duplicate_feature_aliases values must be column names"
            )
    return groups


def _sources_for(feature, aliases, available):
    sources = []
    if feature in available:
        sources.append(feature)
    for alias in aliases:
        if alias in available and alias not in sources:
            sources.append(alias)
    return sources
