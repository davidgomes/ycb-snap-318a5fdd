import logging

import pandas as pd

logger = logging.getLogger(__name__)

FEATURE_SCHEMA_FILE = "feature_schema.joblib"
ALLOWED_FEATURE_OPTIONS = ("include", "exclude", "drop_constant", "drop_duplicate")


class FeatureSchemaError(ValueError):
    """raised when the feature configuration or input data violates the feature schema"""


def _normalize_names(value, option):
    if value is None:
        return None
    if isinstance(value, str):
        value = [value]
    if not isinstance(value, (list, tuple)):
        raise FeatureSchemaError(
            f"dataset.features.{option} must be a column name or a list of column names"
        )
    names = list(value)
    for name in names:
        if not isinstance(name, str) or not name.strip():
            raise FeatureSchemaError(
                f"dataset.features.{option} entries must be non-empty strings, got {name!r}"
            )
    duplicated = sorted({n for n in names if names.count(n) > 1})
    if duplicated:
        raise FeatureSchemaError(
            f"dataset.features.{option} contains duplicated entries: {duplicated}"
        )
    return names


def _series_equal(a: pd.Series, b: pd.Series) -> pd.Series:
    """row-wise equality treating missing values as equal"""
    return (a == b) | (a.isna() & b.isna())


def build_feature_schema(df: pd.DataFrame, features_cfg: dict, targets=None) -> dict:
    """derive the raw feature schema from the training dataframe and dataset.features config"""
    if not isinstance(features_cfg, dict):
        raise FeatureSchemaError("dataset.features must be a mapping")
    unknown = [k for k in features_cfg if k not in ALLOWED_FEATURE_OPTIONS]
    if unknown:
        raise FeatureSchemaError(
            f"unknown dataset.features options: {unknown}. "
            f"Allowed options: {list(ALLOWED_FEATURE_OPTIONS)}"
        )
    targets = list(targets or [])
    include = _normalize_names(features_cfg.get("include"), "include")
    exclude = _normalize_names(features_cfg.get("exclude"), "exclude")
    drop_constant = features_cfg.get("drop_constant", False)
    drop_duplicate = features_cfg.get("drop_duplicate", False)
    for opt, val in (("drop_constant", drop_constant), ("drop_duplicate", drop_duplicate)):
        if not isinstance(val, bool):
            raise FeatureSchemaError(f"dataset.features.{opt} must be a boolean")

    raw_features = [c for c in df.columns if c not in targets]
    for opt, names in (("include", include), ("exclude", exclude)):
        if not names:
            continue
        in_target = [n for n in names if n in targets]
        if in_target:
            raise FeatureSchemaError(
                f"dataset.features.{opt} must not contain target columns: {in_target}"
            )
        unknown_cols = [n for n in names if n not in raw_features]
        if unknown_cols:
            raise FeatureSchemaError(
                f"dataset.features.{opt} contains unknown columns: {unknown_cols}"
            )

    selected = list(include) if include else list(raw_features)
    excluded = [c for c in selected if exclude and c in exclude]
    selected = [c for c in selected if c not in excluded]

    constant = []
    if drop_constant:
        constant = [c for c in selected if df[c].nunique(dropna=False) <= 1]
        selected = [c for c in selected if c not in constant]

    duplicate = []
    aliases = {}
    if drop_duplicate:
        kept = []
        for col in selected:
            canonical = next(
                (k for k in kept if bool(_series_equal(df[k], df[col]).all())),
                None,
            )
            if canonical is None:
                kept.append(col)
            else:
                duplicate.append(col)
                aliases.setdefault(canonical, []).append(col)
        selected = kept

    if not selected:
        raise FeatureSchemaError(
            "dataset.features configuration removes every feature; at least one input feature is required"
        )

    schema = {
        "input_features": selected,
        "dropped_features": {
            "excluded": excluded,
            "constant": constant,
            "duplicate": duplicate,
        },
        "duplicate_feature_aliases": aliases,
    }
    logger.info(f"feature schema: {schema}")
    return schema


def apply_feature_schema(df: pd.DataFrame, schema: dict, keep=None) -> pd.DataFrame:
    """
    select and order the schema's input features from a raw dataframe.
    Extra columns are ignored; aliases may stand in for canonical features.
    Columns listed in keep (e.g. targets) are appended when present.
    """
    aliases = schema.get("duplicate_feature_aliases", {}) or {}
    columns = {}
    missing = []
    for feature in schema["input_features"]:
        sources = [c for c in [feature, *aliases.get(feature, [])] if c in df.columns]
        if not sources:
            missing.append(feature)
            continue
        first = df[sources[0]]
        for other in sources[1:]:
            if not bool(_series_equal(first, df[other]).all()):
                raise FeatureSchemaError(
                    f"conflicting values for feature '{feature}' between duplicate "
                    f"columns {sources}"
                )
        columns[feature] = first.values
    if missing:
        raise FeatureSchemaError(f"missing required input features: {missing}")
    out = pd.DataFrame(columns, index=df.index)
    for col in keep or []:
        if col in df.columns:
            out[col] = df[col]
    return out
