import numpy as np
import pandas as pd
from sklearn.utils.validation import check_is_fitted

from . import _dataframe as sbd
from ._single_column_transformer import RejectColumn, SingleColumnTransformer

__all__ = ["DurationEncoder"]

_COMPONENTS = (
    "total_seconds", "days", "hours", "minutes", "seconds", "microseconds",
    "log1p_total_seconds", "sin_of_day", "cos_of_day",
)
_RESOLUTIONS = ("day", "hour", "minute", "second", "microsecond")
_RESOLUTION_COMPONENTS = {
    "day": ["total_seconds", "days"],
    "hour": ["total_seconds", "days", "hours"],
    "minute": ["total_seconds", "days", "hours", "minutes"],
    "second": ["total_seconds", "days", "hours", "minutes", "seconds"],
    "microsecond": ["total_seconds", "days", "hours", "minutes", "seconds", "microseconds"],
}


class DurationEncoder(SingleColumnTransformer):
    def __init__(self, components="auto", resolution="auto", handle_negative="keep", scaling=None):
        self.components = components
        self.resolution = resolution
        self.handle_negative = handle_negative
        self.scaling = scaling

    def fit_transform(self, column, y=None):
        del y
        if not sbd.is_duration(column):
            raise RejectColumn(f"Column {sbd.name(column)!r} does not have Duration dtype.")
        self._check_params()
        values = self._seconds(column)
        if self.components == "auto":
            self.resolution_ = self._resolve_resolution(values)
            self.components_ = _RESOLUTION_COMPONENTS[self.resolution_] + ["log1p_total_seconds"]
        else:
            self.resolution_ = self.resolution
            self.components_ = list(self.components)
        self.all_outputs_ = [f"{sbd.name(column)}_{c}" for c in self.components_]
        if self.scaling is not None:
            raw = self._extract(values)
            self.scaling_params_ = {}
            for i, name in enumerate(self.components_):
                x = raw[:, i]
                finite = x[np.isfinite(x)]
                if self.scaling == "minmax":
                    a, b = np.min(finite), np.max(finite)
                elif self.scaling == "standard":
                    a, b = np.mean(finite), np.std(finite)
                else:
                    a, b = np.median(finite), np.percentile(finite, 75) - np.percentile(finite, 25)
                self.scaling_params_[name] = {"center": a, "scale": b}
        return self.transform(column)

    def transform(self, column):
        check_is_fitted(self, "components_")
        values = self._seconds(column)
        raw = self._extract(values)
        if self.scaling is not None:
            for i, name in enumerate(self.components_):
                p = self.scaling_params_[name]
                if self.scaling == "minmax":
                    raw[:, i] = np.clip((raw[:, i] - p["center"]) / (p["scale"] - p["center"]), 0, 1) if p["scale"] != p["center"] else 0
                else:
                    raw[:, i] = (raw[:, i] - p["center"]) / p["scale"] if p["scale"] != 0 else 0
        result = sbd.make_dataframe_like(column, {
            f"{sbd.name(column)}_{c}": raw[:, i] for i, c in enumerate(self.components_)
        })
        return sbd.copy_index(column, result)

    def _seconds(self, column):
        if sbd.is_pandas(column):
            x = column.astype("timedelta64[ns]").view("int64").astype(float) / 1e9
            x[column.isna().to_numpy()] = np.nan
        else:
            x = np.asarray(column.cast("duration", time_unit="ns").to_numpy(), dtype=float) / 1e9
        if self.handle_negative == "clip":
            x = np.maximum(x, 0)
        elif self.handle_negative == "abs":
            x = np.abs(x)
        return x

    def _extract(self, x):
        day = np.floor(x / 86400)
        hour = np.floor((x - day * 86400) / 3600)
        minute = np.floor((x - day * 86400 - hour * 3600) / 60)
        second = np.floor(x - day * 86400 - hour * 3600 - minute * 60)
        micro = np.floor((x - np.floor(x)) * 1e6)
        out = {"total_seconds": x, "days": day, "hours": hour, "minutes": minute,
               "seconds": second, "microseconds": micro,
               "log1p_total_seconds": np.sign(x) * np.log1p(np.abs(x)),
               "sin_of_day": np.sin(x / 86400 * 2 * np.pi),
               "cos_of_day": np.cos(x / 86400 * 2 * np.pi)}
        return np.column_stack([out[c] for c in self.components_])

    def _resolve_resolution(self, x):
        if not np.isfinite(x).any():
            return "minute"
        for name, unit in zip(_RESOLUTIONS, (86400, 3600, 60, 1, 1e-6)):
            if np.any(np.mod(x, unit) != 0):
                return name
        return "microsecond"

    def _check_params(self):
        if self.components != "auto" and not isinstance(self.components, (list, tuple)):
            raise TypeError("components must be 'auto', a list, or a tuple")
        if self.components != "auto" and any(c not in _COMPONENTS for c in self.components):
            raise ValueError("Unknown duration component")
        if self.resolution not in ("auto",) + _RESOLUTIONS:
            raise ValueError("Invalid resolution")
        if self.handle_negative not in ("keep", "clip", "abs"):
            raise ValueError("Invalid handle_negative")
        if self.scaling not in (None, "minmax", "standard", "robust"):
            raise ValueError("Invalid scaling")

    def get_feature_names_out(self, input_features=None):
        check_is_fitted(self, "all_outputs_")
        return np.asarray(self.all_outputs_)
