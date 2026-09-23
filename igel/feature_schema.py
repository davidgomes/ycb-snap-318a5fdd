"""Selection, persistence and enforcement of the raw feature schema."""

import logging

import pandas as pd

logger = logging.getLogger(__name__)

SCHEMA_VERSION = 1
ALLOWED_KEYS = ("include", "exclude", "drop_constant", "drop_duplicate")


class FeatureSchemaError(ValueError):
    """Raised when a features configuration or input data violates the feature schema."""


def _normalize_names(value, key: str) -> list:
    if isinstance(value, str):
        value = [value]
    if not isinstance(value, (list, tuple)):
        raise FeatureSchemaError(
            f"dataset.features.{key} must be a column name or a list of column names, "
            f"got {type(value).__name__}"
        )
    names = list(value)
    invalid = [n for n in names if not isinstance(n, str) or not n.strip()]
    if invalid:
        raise FeatureSchemaError(
            f"dataset.features.{key} entries must be non-empty strings, got invalid entries: {invalid}"
        )
    seen, duplicated = set(), []
    for n in names:
        if n in seen and n not in duplicated:
            duplicated.append(n)
        seen.add(n)
    if duplicated:
        raise FeatureSchemaError(
            f"dataset.features.{key} contains duplicated entries: {duplicated}"
        )
    return names


def validate_features_config(config, columns: list, targets: list) -> dict:
    """
    validate the dataset.features configuration against the raw dataset columns
    @return: normalized configuration
    """
    if not isinstance(config, dict):
        raise FeatureSchemaError(
            f"dataset.features must be a mapping with the keys {list(ALLOWED_KEYS)}"
        )
    unknown_keys = [k for k in config if k not in ALLOWED_KEYS]
    if unknown_keys:
        raise FeatureSchemaError(
            f"unknown dataset.features option(s): {unknown_keys}. "
            f"Allowed options: {list(ALLOWED_KEYS)}"
        )

    normalized = {}
    for key in ("include", "exclude"):
        value = config.get(key)
        if value is None:
            normalized[key] = None
            continue
        names = _normalize_names(value, key)
        unknown = [n for n in names if n not in columns]
        if unknown:
            raise FeatureSchemaError(
                f"dataset.features.{key} references unknown column(s): {unknown}"
            )
        target_cols = [n for n in names if n in targets]
        if target_cols:
            raise FeatureSchemaError(
                f"dataset.features.{key} must not contain target column(s): {target_cols}"
            )
        normalized[key] = names

    for key in ("drop_constant", "drop_duplicate"):
        value = config.get(key, False)
        if not isinstance(value, bool):
            raise FeatureSchemaError(
                f"dataset.features.{key} must be a boolean, got {value!r}"
            )
        normalized[key] = value
    return normalized


def _same_values(a: pd.Series, b: pd.Series) -> pd.Series:
    a = a.reset_index(drop=True)
    b = b.reset_index(drop=True)
    return (a == b) | (a.isna() & b.isna())


def build_feature_schema(df: pd.DataFrame, config, targets: list) -> dict:
    """
    derive the raw feature schema from the training data and the dataset.features configuration
    """
    targets = list(targets or [])
    columns = list(df.columns)
    config = validate_features_config(config, columns, targets)

    selected = (
        list(config["include"])
        if config["include"] is not None
        else [c for c in columns if c not in targets]
    )
    excluded = list(config["exclude"] or [])
    selected = [c for c in selected if c not in excluded]

    constant = []
    if config["drop_constant"]:
        constant = [c for c in selected if df[c].nunique(dropna=False) <= 1]
        selected = [c for c in selected if c not in constant]

    input_features, aliases, duplicate = [], {}, []
    for col in selected:
        canonical = None
        if config["drop_duplicate"]:
            canonical = next(
                (
                    kept
                    for kept in input_features
                    if _same_values(df[kept], df[col]).all()
                ),
                None,
            )
        if canonical is None:
            input_features.append(col)
        else:
            aliases.setdefault(canonical, []).append(col)
            duplicate.append(col)

    if not input_features:
        raise FeatureSchemaError(
            "dataset.features configuration removes every feature; at least one input feature is required"
        )

    schema = {
        "version": SCHEMA_VERSION,
        "config": config,
        "target": targets,
        "input_features": input_features,
        "dropped_features": {
            "excluded": excluded,
            "constant": constant,
            "duplicate": duplicate,
        },
        "duplicate_feature_aliases": aliases,
    }
    logger.info(f"feature schema: {schema}")
    return schema


def apply_feature_schema(df: pd.DataFrame, schema: dict) -> pd.DataFrame:
    """
    select and order the model input features of df according to the schema.
    Extra columns are ignored and any recorded alias may supply its canonical feature.
    """
    aliases = schema.get("duplicate_feature_aliases", {}) or {}
    missing, conflicts, data = [], [], {}
    for feature in schema["input_features"]:
        sources = [
            c for c in [feature] + list(aliases.get(feature, [])) if c in df.columns
        ]
        if not sources:
            missing.append(feature)
            continue
        first = df[sources[0]]
        disagreeing = [
            s for s in sources[1:] if not _same_values(first, df[s]).all()
        ]
        if disagreeing:
            conflicts.append(sources)
            continue
        data[feature] = first.to_numpy()

    if missing:
        raise FeatureSchemaError(
            f"missing required feature(s): {missing}. "
            f"Expected input features: {schema['input_features']}"
        )
    if conflicts:
        details = "; ".join(str(cols) for cols in conflicts)
        raise FeatureSchemaError(
            f"conflicting values between duplicate feature columns: {details}"
        )
    return pd.DataFrame(data, columns=schema["input_features"], index=df.index)
