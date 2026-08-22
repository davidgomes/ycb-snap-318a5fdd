import numpy as np
import pandas as pd
from sklearn.utils.validation import check_is_fitted

from . import _dataframe as sbd
from ._single_column_transformer import RejectColumn, SingleColumnTransformer

__all__ = ["DurationEncoder"]

_COMPONENTS = (
    "total_seconds",
    "days",
    "hours",
    "minutes",
    "seconds",
    "microseconds",
    "log1p_total_seconds",
    "sin_of_day",
    "cos_of_day",
)
_RESOLUTIONS = ("day", "hour", "minute", "second", "microsecond")
_REMAINDER = ("days", "hours", "minutes", "seconds", "microseconds")
_UNIT_NS = {"day": 86_400_000_000_000, "hour": 3_600_000_000_000,
            "minute": 60_000_000_000, "second": 1_000_000_000,
            "microsecond": 1_000}


class DurationEncoder(SingleColumnTransformer):
    """Extract numerical features from a duration column."""

    def __init__(self, components="auto", resolution="auto",
                 handle_negative="keep", scaling=None):
        self.components = components
        self.resolution = resolution
        self.handle_negative = handle_negative
        self.scaling = scaling

    def fit_transform(self, column, y=None):
        del y
        if not sbd.is_duration(column):
            raise RejectColumn(
                f"Column {sbd.name(column)!r} does not have Duration dtype."
            )
        self._check_params()
        values = self._values(column)
        if self.components == "auto":
            self.resolution_ = self._resolve_resolution(values)
            self.components_ = self._components_for_resolution(self.resolution_)
        else:
            self.resolution_ = self.resolution
            requested = set(self.components)
            self.components_ = [c for c in _COMPONENTS if c in requested]
        self.all_outputs_ = [f"{sbd.name(column)}_{c}" for c in self.components_]
        if self.scaling is not None:
            raw = self._extract(values)
            self.scaling_params_ = {}
            for i, component in enumerate(self.components_):
                x = raw[:, i]
                valid = x[~np.isnan(x)]
                if self.scaling == "minmax":
                    params = {"min": np.min(valid) if len(valid) else 0.0,
                              "max": np.max(valid) if len(valid) else 0.0}
                elif self.scaling == "standard":
                    params = {"mean": np.mean(valid) if len(valid) else 0.0,
                              "std": np.std(valid) if len(valid) else 0.0}
                else:
                    params = {"median": np.median(valid) if len(valid) else 0.0,
                              "iqr": (np.percentile(valid, 75) - np.percentile(valid, 25))
                              if len(valid) else 0.0}
                self.scaling_params_[component] = params
        return self.transform(column)

    def transform(self, column):
        check_is_fitted(self, "components_")
        values = self._values(column)
        result = self._extract(values)
        if self.scaling is not None:
            for i, component in enumerate(self.components_):
                p = self.scaling_params_[component]
                if self.scaling == "minmax":
                    denom = p["max"] - p["min"]
                    result[:, i] = 0 if denom == 0 else np.clip(
                        (result[:, i] - p["min"]) / denom, 0, 1
                    )
                elif self.scaling == "standard":
                    result[:, i] = 0 if p["std"] == 0 else (result[:, i] - p["mean"]) / p["std"]
                else:
                    result[:, i] = 0 if p["iqr"] == 0 else (result[:, i] - p["median"]) / p["iqr"]
        return sbd.copy_index(
            column, sbd.make_dataframe_like(column, dict(zip(self.all_outputs_, result.T)))
        )

    def _values(self, column):
        values = pd.to_timedelta(sbd.to_pandas(column)).astype("timedelta64[ns]").astype("float64")
        values[pd.isna(sbd.to_pandas(column))] = np.nan
        if self.handle_negative == "clip":
            values = np.maximum(values, 0)
        elif self.handle_negative == "abs":
            values = np.abs(values)
        return values

    def _extract(self, values):
        seconds = values / 1e9
        out = []
        for component in self.components_:
            if component == "total_seconds":
                out.append(seconds)
            elif component == "days":
                out.append(np.trunc(seconds / 86400))
            elif component == "hours":
                out.append(np.trunc(np.mod(seconds, 86400) / 3600))
            elif component == "minutes":
                out.append(np.trunc(np.mod(seconds, 3600) / 60))
            elif component == "seconds":
                out.append(np.trunc(np.mod(seconds, 60)))
            elif component == "microseconds":
                out.append(np.trunc(np.mod(values, 1e9) / 1e3))
            elif component == "log1p_total_seconds":
                out.append(np.sign(seconds) * np.log1p(np.abs(seconds)))
            elif component == "sin_of_day":
                out.append(np.sin(seconds / 86400 * 2 * np.pi))
            elif component == "cos_of_day":
                out.append(np.cos(seconds / 86400 * 2 * np.pi))
        return np.column_stack(out).astype("float32")

    def _resolve_resolution(self, values):
        valid = values[~np.isnan(values)]
        if not len(valid):
            return "minute"
        for resolution in _RESOLUTIONS:
            if np.any(np.mod(valid, _UNIT_NS[resolution]) != 0):
                continue
            return resolution
        return "microsecond"

    def _components_for_resolution(self, resolution):
        return ["total_seconds", "days"] + list(_REMAINDER[1:_REMAINDER.index(resolution) + 1]) + [
            "log1p_total_seconds"
        ]

    def _check_params(self):
        if self.components != "auto" and (
            not isinstance(self.components, (list, tuple))
        ):
            raise TypeError("'components' must be 'auto' or a list/tuple.")
        if self.components != "auto" and any(c not in _COMPONENTS for c in self.components):
            raise ValueError("Unknown duration component.")
        if self.resolution not in ("auto",) + _RESOLUTIONS:
            raise ValueError("Invalid duration resolution.")
        if self.handle_negative not in ("keep", "clip", "abs"):
            raise ValueError("Invalid handle_negative.")
        if self.scaling not in (None, "minmax", "standard", "robust"):
            raise ValueError("Invalid scaling.")

    def get_feature_names_out(self, input_features=None):
        check_is_fitted(self, "all_outputs_")
        return np.asarray(self.all_outputs_)
