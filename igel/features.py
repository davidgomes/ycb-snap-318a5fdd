"""Raw feature selection and the persisted feature schema."""

import logging
from pathlib import Path

import joblib
import pandas as pd

logger = logging.getLogger(__name__)

FEATURES_OPTIONS = ("include", "exclude", "drop_constant", "drop_duplicate")


class FeatureSchemaError(ValueError):
    """raised when a feature configuration or input data violates the feature schema"""


def _normalize_names(value, option: str) -> list:
    if value is None:
        return []
    names = [value] if isinstance(value, str) else value
    if not isinstance(names, (list, tuple)):
        raise FeatureSchemaError(
            f"dataset.features.{option} must be a column name or a list of column names, "
            f"got {type(value).__name__}"
        )
    invalid = [n for n in names if not isinstance(n, str) or not n.strip()]
    if invalid:
        raise FeatureSchemaError(
            f"dataset.features.{option} entries must be non-empty column names, "
            f"invalid entries: {invalid}"
        )
    duplicated = sorted({n for n in names if names.count(n) > 1})
    if duplicated:
        raise FeatureSchemaError(
            f"dataset.features.{option} contains duplicated entries: {duplicated}"
        )
    return list(names)


def _normalize_flag(value, option: str) -> bool:
    if value is None:
        return True
    if not isinstance(value, bool):
        raise FeatureSchemaError(
            f"dataset.features.{option} must be a boolean, got {value!r}"
        )
    return value


def normalize_features_config(features) -> dict:
    """validate dataset.features and return it with defaults applied"""
    if not isinstance(features, dict):
        raise FeatureSchemaError(
            f"dataset.features must be a mapping with the options {list(FEATURES_OPTIONS)}"
        )
    unknown = [k for k in features if k not in FEATURES_OPTIONS]
    if unknown:
        raise FeatureSchemaError(
            f"unknown dataset.features option(s): {unknown}. "
            f"Supported options: {list(FEATURES_OPTIONS)}"
        )
    include = features.get("include")
    config = {
        "include": None
        if include is None
        else _normalize_names(include, "include"),
        "exclude": _normalize_names(features.get("exclude"), "exclude"),
        "drop_constant": _normalize_flag(
            features.get("drop_constant"), "drop_constant"
        ),
        "drop_duplicate": _normalize_flag(
            features.get("drop_duplicate"), "drop_duplicate"
        ),
    }
    if config["include"] is not None and not config["include"]:
        raise FeatureSchemaError("dataset.features.include must not be empty")
    return config


def _columns_agree(a: pd.Series, b: pd.Series) -> bool:
    a = a.reset_index(drop=True)
    b = b.reset_index(drop=True)
    try:
        equal = a == b
    except TypeError:
        return False
    return bool((equal | (a.isna() & b.isna())).all())


def build_feature_schema(df: pd.DataFrame, features, target=None) -> dict:
    """
    select the raw features used as model inputs from the training data
    @param df: raw training dataframe
    @param features: dataset.features configuration
    @param target: list of target columns, which are never used as features
    @return: feature schema as a dict
    """
    config = normalize_features_config(features)
    targets = list(target or [])
    raw_columns = list(df.columns)

    for option in ("include", "exclude"):
        names = config[option] or []
        unknown = [n for n in names if n not in raw_columns]
        if unknown:
            raise FeatureSchemaError(
                f"dataset.features.{option} contains unknown column(s): {unknown}. "
                f"Available columns: {raw_columns}"
            )
        target_entries = [n for n in names if n in targets]
        if target_entries:
            raise FeatureSchemaError(
                f"dataset.features.{option} must not contain target column(s): "
                f"{target_entries}"
            )

    candidates = (
        config["include"]
        if config["include"] is not None
        else [c for c in raw_columns if c not in targets]
    )
    excluded = list(config["exclude"])
    remaining = [c for c in candidates if c not in excluded]

    constant = []
    if config["drop_constant"]:
        constant = [c for c in remaining if df[c].nunique(dropna=False) <= 1]
        remaining = [c for c in remaining if c not in constant]

    input_features = []
    duplicate = []
    aliases = {}
    for column in remaining:
        canonical = None
        if config["drop_duplicate"]:
            canonical = next(
                (
                    kept
                    for kept in input_features
                    if _columns_agree(df[kept], df[column])
                ),
                None,
            )
        if canonical is None:
            input_features.append(column)
        else:
            duplicate.append(column)
            aliases.setdefault(canonical, []).append(column)

    if not input_features:
        raise FeatureSchemaError(
            f"dataset.features configuration removes every feature "
            f"(excluded: {excluded}, constant: {constant}, duplicate: {duplicate})"
        )

    schema = {
        "input_features": input_features,
        "target": targets,
        "dropped_features": {
            "excluded": excluded,
            "constant": constant,
            "duplicate": duplicate,
        },
        "duplicate_feature_aliases": aliases,
        "features_config": config,
    }
    logger.info(f"feature schema: {schema}")
    return schema


def apply_feature_schema(df: pd.DataFrame, schema: dict, target=None):
    """
    map raw input data onto the schema's input features (followed by the target columns)
    @param df: raw dataframe
    @param schema: feature schema created by build_feature_schema
    @param target: target columns that must be kept after the input features
    @return: dataframe with the selected columns in schema order
    """
    columns = set(df.columns)
    aliases = schema.get("duplicate_feature_aliases", {})
    selected = {}
    missing = []
    conflicts = []
    for feature in schema["input_features"]:
        sources = [
            c for c in [feature] + aliases.get(feature, []) if c in columns
        ]
        if not sources:
            missing.append(feature)
            continue
        values = df[sources[0]]
        disagreeing = [
            s for s in sources[1:] if not _columns_agree(values, df[s])
        ]
        if disagreeing:
            conflicts.append([sources[0]] + disagreeing)
        selected[feature] = values

    if missing:
        accepted = {f: aliases[f] for f in missing if f in aliases}
        hint = f" (accepted aliases: {accepted})" if accepted else ""
        raise FeatureSchemaError(
            f"missing required feature(s): {missing}{hint}"
        )
    if conflicts:
        raise FeatureSchemaError(
            f"duplicate feature columns have conflicting values: {conflicts}"
        )

    targets = list(target or [])
    missing_targets = [t for t in targets if t not in columns]
    if missing_targets:
        raise FeatureSchemaError(
            f"missing required target column(s): {missing_targets}"
        )
    for t in targets:
        selected[t] = df[t]
    return pd.DataFrame(selected, index=df.index)


def save_feature_schema(schema: dict, path) -> None:
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    joblib.dump(schema, path)
    logger.info(f"feature schema saved to {path}")


def load_feature_schema(description: dict, description_file):
    """
    load the feature schema recorded in description.json, if any.
    A schema next to the description file takes precedence over the recorded path,
    so that a moved results directory keeps working.
    """
    recorded = description.get("feature_schema_path")
    if not recorded:
        return None
    results_dir = Path(description_file).parent
    recorded_path = Path(recorded)
    candidates = [results_dir / recorded_path.name]
    candidates.append(
        recorded_path
        if recorded_path.is_absolute()
        else results_dir / recorded_path
    )
    for candidate in candidates:
        if candidate.exists():
            logger.info(f"loading feature schema from {candidate}")
            return joblib.load(candidate)
    raise FileNotFoundError(
        f"feature schema not found, looked in: {[str(c) for c in candidates]}"
    )
