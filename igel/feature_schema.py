"""Selection and enforcement of the raw input features a model is trained on."""

import numpy as np
import pandas as pd

FEATURE_OPTIONS = ("include", "exclude", "drop_constant", "drop_duplicate")


class FeatureSchemaError(ValueError):
    """
    raised when dataset.features is invalid or when data does not satisfy a persisted feature schema
    """


def _column_names(option: str, value):
    if value is None:
        return None
    names = [value] if isinstance(value, str) else value
    if not isinstance(names, (list, tuple)):
        raise FeatureSchemaError(
            f"dataset.features.{option} must be a column name or a list of column names, "
            f"got {value!r}"
        )
    invalid = [n for n in names if not isinstance(n, str) or not n.strip()]
    if invalid:
        raise FeatureSchemaError(
            f"dataset.features.{option} must only contain non-empty column names, "
            f"got invalid entries: {invalid!r}"
        )
    duplicated = list(
        dict.fromkeys(n for i, n in enumerate(names) if n in names[:i])
    )
    if duplicated:
        raise FeatureSchemaError(
            f"dataset.features.{option} contains duplicated entries: {duplicated}"
        )
    return list(names)


def _flag(features_props: dict, option: str) -> bool:
    value = features_props.get(option)
    if value is None:
        return False
    if not isinstance(value, bool):
        raise FeatureSchemaError(
            f"dataset.features.{option} must be true or false, got {value!r}"
        )
    return value


def _check_names(option: str, names: list, available: list, targets: list):
    named_targets = [n for n in names if n in targets]
    if named_targets:
        raise FeatureSchemaError(
            f"dataset.features.{option} must not contain target column(s): {named_targets}"
        )
    unknown = [n for n in names if n not in available]
    if unknown:
        raise FeatureSchemaError(
            f"dataset.features.{option} contains unknown column(s): {unknown}. "
            f"Available feature columns: {available}"
        )


def _rows_agree(left: pd.Series, right: pd.Series) -> np.ndarray:
    left_values = left.to_numpy(dtype=object)
    right_values = right.to_numpy(dtype=object)
    both_missing = pd.isna(left_values) & pd.isna(right_values)
    return (left_values == right_values) | both_missing


def build_feature_schema(dataset: pd.DataFrame, features_props, targets=()):
    """
    validate the dataset.features options against the raw training data and derive the feature schema
    @param dataset: raw training data (including the target columns)
    @param features_props: the dataset.features options
    @param targets: target columns, which can never be features
    @return: dict with input_features, dropped_features and duplicate_feature_aliases
    """
    if not isinstance(features_props, dict):
        raise FeatureSchemaError(
            f"dataset.features must be a mapping with the options {list(FEATURE_OPTIONS)}, "
            f"got {features_props!r}"
        )
    unsupported = [k for k in features_props if k not in FEATURE_OPTIONS]
    if unsupported:
        raise FeatureSchemaError(
            f"dataset.features contains unsupported option(s): {unsupported}. "
            f"Supported options: {list(FEATURE_OPTIONS)}"
        )

    targets = list(targets)
    available = [c for c in dataset.columns if c not in targets]
    include = _column_names("include", features_props.get("include"))
    exclude = _column_names("exclude", features_props.get("exclude")) or []
    drop_constant = _flag(features_props, "drop_constant")
    drop_duplicate = _flag(features_props, "drop_duplicate")
    if include is not None:
        _check_names("include", include, available, targets)
    _check_names("exclude", exclude, available, targets)

    features = [
        c for c in (available if include is None else include) if c not in exclude
    ]

    constant = []
    if drop_constant:
        constant = [c for c in features if dataset[c].nunique(dropna=False) <= 1]
        features = [c for c in features if c not in constant]

    duplicate = []
    aliases = {}
    if drop_duplicate:
        canonical = []
        for column in features:
            original = next(
                (
                    kept
                    for kept in canonical
                    if _rows_agree(dataset[kept], dataset[column]).all()
                ),
                None,
            )
            if original is None:
                canonical.append(column)
            else:
                duplicate.append(column)
                aliases.setdefault(original, []).append(column)
        features = canonical

    if not features:
        raise FeatureSchemaError(
            f"dataset.features removes every feature, no input features remain "
            f"(excluded: {exclude}, constant: {constant}, duplicate: {duplicate})"
        )

    return {
        "input_features": features,
        "dropped_features": {
            "excluded": exclude,
            "constant": constant,
            "duplicate": duplicate,
        },
        "duplicate_feature_aliases": aliases,
    }


def apply_feature_schema(dataset: pd.DataFrame, schema: dict) -> pd.DataFrame:
    """
    select the schema's input features from raw data, in the schema's order.
    Extra columns are ignored and any recorded alias may supply its canonical feature.
    @param dataset: raw data
    @param schema: persisted feature schema
    @return: dataframe whose columns are exactly the schema's input features
    """
    aliases = schema.get("duplicate_feature_aliases") or {}
    selected = {}
    missing = []
    conflicts = []
    for feature in schema["input_features"]:
        sources = [
            c for c in [feature, *aliases.get(feature, [])] if c in dataset.columns
        ]
        if not sources:
            missing.append(feature)
            continue
        reference = dataset[sources[0]]
        disagreeing = []
        conflicting_rows = np.zeros(len(dataset), dtype=bool)
        for column in sources[1:]:
            agree = _rows_agree(reference, dataset[column])
            if not agree.all():
                disagreeing.append(column)
                conflicting_rows |= ~agree
        if disagreeing:
            conflicts.append(
                f"{[sources[0], *disagreeing]} (first conflicting row: "
                f"{int(np.flatnonzero(conflicting_rows)[0])})"
            )
        selected[feature] = reference

    if missing:
        hints = "".join(
            f"; {f!r} may also be supplied as {aliases[f]}"
            for f in missing
            if aliases.get(f)
        )
        raise FeatureSchemaError(f"missing required feature(s): {missing}{hints}")
    if conflicts:
        raise FeatureSchemaError(
            "duplicate feature columns must agree in every row, "
            f"but these columns conflict: {', '.join(conflicts)}"
        )
    return pd.DataFrame(selected, index=dataset.index)
