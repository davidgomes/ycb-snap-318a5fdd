import numpy as np

from . import _dataframe as sbd
from ._single_column_transformer import RejectColumn, SingleColumnTransformer
from ._sklearn_compat import TransformerTags
from sklearn.utils.validation import check_is_fitted

__all__ = ["DurationEncoder"]

_SECONDS_PER_DAY = 86400
_SECONDS_PER_HOUR = 3600
_SECONDS_PER_MINUTE = 60

_RESOLUTION_LEVELS = ["day", "hour", "minute", "second", "microsecond"]

_RESOLUTION_TO_COMPONENTS = {
    "day": ["total_seconds", "days", "log1p_total_seconds"],
    "hour": ["total_seconds", "days", "hours", "log1p_total_seconds"],
    "minute": [
        "total_seconds",
        "days",
        "hours",
        "minutes",
        "log1p_total_seconds",
    ],
    "second": [
        "total_seconds",
        "days",
        "hours",
        "minutes",
        "seconds",
        "log1p_total_seconds",
    ],
    "microsecond": [
        "total_seconds",
        "days",
        "hours",
        "minutes",
        "seconds",
        "microseconds",
        "log1p_total_seconds",
    ],
}

_ALL_COMPONENTS = frozenset(
    {
        "total_seconds",
        "days",
        "hours",
        "minutes",
        "seconds",
        "microseconds",
        "log1p_total_seconds",
        "sin_of_day",
        "cos_of_day",
    }
)

_CANONICAL_ORDER = [
    "total_seconds",
    "days",
    "hours",
    "minutes",
    "seconds",
    "microseconds",
    "sin_of_day",
    "cos_of_day",
    "log1p_total_seconds",
]


def _decompose_total_seconds(total_seconds):
    total_seconds = np.asarray(total_seconds, dtype=np.float64)
    days = np.floor(total_seconds / _SECONDS_PER_DAY)
    rem = total_seconds - days * _SECONDS_PER_DAY
    hours = np.floor(rem / _SECONDS_PER_HOUR)
    rem = rem - hours * _SECONDS_PER_HOUR
    minutes = np.floor(rem / _SECONDS_PER_MINUTE)
    rem = rem - minutes * _SECONDS_PER_MINUTE
    seconds = np.floor(rem)
    microseconds = (rem - seconds) * 1e6
    return days, hours, minutes, seconds, microseconds


def _detect_resolution(total_seconds):
    valid = total_seconds[~np.isnan(total_seconds)]
    if valid.size == 0:
        return "minute"
    _, hours, minutes, seconds, microseconds = _decompose_total_seconds(valid)
    if np.any(np.abs(microseconds) > 1e-3):
        return "microsecond"
    if np.any(seconds != 0):
        return "second"
    if np.any(minutes != 0):
        return "minute"
    if np.any(hours != 0):
        return "hour"
    return "day"


def _sort_components(components):
    order = {name: idx for idx, name in enumerate(_CANONICAL_ORDER)}
    return sorted(components, key=lambda name: order[name])


def _extract_component_values(total_seconds, component):
    days, hours, minutes, seconds, microseconds = _decompose_total_seconds(
        total_seconds
    )
    if component == "total_seconds":
        return total_seconds
    if component == "days":
        return days
    if component == "hours":
        return hours
    if component == "minutes":
        return minutes
    if component == "seconds":
        return seconds
    if component == "microseconds":
        return microseconds
    if component == "log1p_total_seconds":
        return np.log1p(total_seconds)
    fraction_of_day = np.mod(total_seconds, _SECONDS_PER_DAY) / _SECONDS_PER_DAY
    angle = 2 * np.pi * fraction_of_day
    if component == "sin_of_day":
        return np.sin(angle)
    if component == "cos_of_day":
        return np.cos(angle)
    raise ValueError(f"Unknown component {component!r}.")


def _apply_handle_negative(total_seconds, handle_negative):
    if handle_negative == "keep":
        return total_seconds
    if handle_negative == "abs":
        return np.abs(total_seconds)
    if handle_negative == "clip":
        return np.maximum(total_seconds, 0.0)
    raise ValueError(
        f"'handle_negative' must be one of ['clip', 'abs', 'keep'], "
        f"got {handle_negative!r}."
    )


def _compute_scaling_params(values, scaling):
    valid = values[~np.isnan(values)]
    if valid.size == 0:
        if scaling == "minmax":
            return {"min": 0.0, "max": 0.0}
        if scaling == "standard":
            return {"mean": 0.0, "std": 0.0}
        if scaling == "robust":
            return {"median": 0.0, "iqr": 0.0}
    if scaling == "minmax":
        return {"min": float(np.min(valid)), "max": float(np.max(valid))}
    if scaling == "standard":
        return {"mean": float(np.mean(valid)), "std": float(np.std(valid))}
    if scaling == "robust":
        q25, q75 = np.quantile(valid, [0.25, 0.75])
        return {"median": float(np.median(valid)), "iqr": float(q75 - q25)}
    raise ValueError(
        f"'scaling' must be one of [None, 'minmax', 'standard', 'robust'], "
        f"got {scaling!r}."
    )


def _apply_scaling(values, params, scaling):
    if scaling == "minmax":
        min_val = params["min"]
        max_val = params["max"]
        if max_val == min_val:
            return np.zeros_like(values, dtype=np.float32)
        scaled = (values - min_val) / (max_val - min_val)
        scaled = np.clip(scaled, 0.0, 1.0)
        return scaled.astype(np.float32)
    if scaling == "standard":
        mean = params["mean"]
        std = params["std"]
        if std == 0.0:
            return np.zeros_like(values, dtype=np.float32)
        return ((values - mean) / std).astype(np.float32)
    if scaling == "robust":
        median = params["median"]
        iqr = params["iqr"]
        if iqr == 0.0:
            return np.zeros_like(values, dtype=np.float32)
        return ((values - median) / iqr).astype(np.float32)
    return values.astype(np.float32)


class DurationEncoder(SingleColumnTransformer):
    """Extract numeric features from duration columns.

    Parameters
    ----------
    components : "auto" or list of str, default="auto"
        Components to extract. When ``"auto"``, components are chosen from
        ``resolution``. When an explicit list is provided, ``resolution`` is
        ignored.

    resolution : str, default="auto"
        Finest granularity for remainder components when ``components="auto"``.
        One of ``"auto"``, ``"day"``, ``"hour"``, ``"minute"``, ``"second"``,
        or ``"microsecond"``.

    handle_negative : {"clip", "abs", "keep"}, default="keep"
        Treatment of negative durations before extraction.

    scaling : {None, "minmax", "standard", "robust"}, default=None
        Optional feature scaling applied after extraction.

    Attributes
    ----------
    components_ : list of str
        Resolved list of extracted components.

    resolution_ : str
        Resolved resolution level.

    scaling_params_ : dict or None
        Per-component scaling statistics when ``scaling`` is not ``None``.

    all_outputs_ : list of str
        Names of the output columns.
    """

    def __init__(
        self,
        components="auto",
        resolution="auto",
        handle_negative="keep",
        scaling=None,
    ):
        self.components = components
        self.resolution = resolution
        self.handle_negative = handle_negative
        self.scaling = scaling

    def fit_transform(self, column, y=None):
        del y
        self._check_params()
        if not sbd.is_duration(column):
            raise RejectColumn(
                f"Column {sbd.name(column)!r} does not have Duration dtype."
            )

        not_nulls = ~sbd.is_null(column)
        total_seconds = np.asarray(
            sbd.to_numpy(sbd.total_seconds(column)), dtype=np.float64
        )
        total_seconds = np.where(not_nulls, total_seconds, np.nan)
        total_seconds = _apply_handle_negative(total_seconds, self.handle_negative)

        if self.components == "auto":
            if self.resolution == "auto":
                self.resolution_ = _detect_resolution(total_seconds)
            else:
                self.resolution_ = self.resolution
            self.components_ = list(_RESOLUTION_TO_COMPONENTS[self.resolution_])
        else:
            self.components_ = _sort_components(list(self.components))
            if self.resolution == "auto":
                self.resolution_ = _detect_resolution(total_seconds)
            else:
                self.resolution_ = self.resolution

        col_name = sbd.name(column)
        self.all_outputs_ = [f"{col_name}_{component}" for component in self.components_]

        if self.scaling is None:
            self.scaling_params_ = None
        else:
            self.scaling_params_ = {}
            for component in self.components_:
                values = _extract_component_values(total_seconds, component)
                self.scaling_params_[component] = _compute_scaling_params(
                    values, self.scaling
                )

        return self.transform(column)

    def transform(self, column):
        check_is_fitted(self, "all_outputs_")
        col_name = sbd.name(column)
        not_nulls = ~sbd.is_null(column)
        null_mask = sbd.copy_index(column, sbd.all_null_like(sbd.to_float32(column)))

        total_seconds = np.asarray(
            sbd.to_numpy(sbd.total_seconds(column)), dtype=np.float64
        )
        total_seconds = np.where(not_nulls, total_seconds, np.nan)
        total_seconds = _apply_handle_negative(total_seconds, self.handle_negative)

        extracted = []
        for component in self.components_:
            values = _extract_component_values(total_seconds, component)
            if self.scaling is not None:
                values = _apply_scaling(
                    values, self.scaling_params_[component], self.scaling
                )
            else:
                values = values.astype(np.float32)
            extracted.append(
                sbd.make_column_like(column, values, name=f"{col_name}_{component}")
            )

        result = sbd.make_dataframe_like(column, extracted)
        result = sbd.copy_index(column, result)
        return sbd.where_row(result, not_nulls, null_mask)

    def _check_params(self):
        if self.components != "auto":
            if not isinstance(self.components, (list, tuple)):
                raise TypeError(
                    "'components' must be 'auto' or a list/tuple of strings."
                )
            unknown = set(self.components) - _ALL_COMPONENTS
            if unknown:
                raise ValueError(
                    f"Unknown component names: {sorted(unknown)!r}."
                )

        allowed_resolutions = {"auto", *_RESOLUTION_LEVELS}
        if self.resolution not in allowed_resolutions:
            raise ValueError(
                f"'resolution' must be one of {sorted(allowed_resolutions)!r}, "
                f"got {self.resolution!r}."
            )

        if self.handle_negative not in {"clip", "abs", "keep"}:
            raise ValueError(
                f"'handle_negative' must be one of ['clip', 'abs', 'keep'], "
                f"got {self.handle_negative!r}."
            )

        if self.scaling not in {None, "minmax", "standard", "robust"}:
            raise ValueError(
                f"'scaling' must be one of [None, 'minmax', 'standard', 'robust'], "
                f"got {self.scaling!r}."
            )

    def _more_tags(self):
        return {"preserves_dtype": []}

    def __sklearn_tags__(self):
        tags = super().__sklearn_tags__()
        tags.transformer_tags = TransformerTags(preserves_dtype=[])
        return tags

    def get_feature_names_out(self, input_features=None):
        check_is_fitted(self, "all_outputs_")
        return self.all_outputs_
