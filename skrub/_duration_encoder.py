import numpy as np
import pandas as pd
from sklearn.utils.validation import check_is_fitted

from . import _dataframe as sbd
from ._dispatch import dispatch
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


@dispatch
def _duration_seconds(column):
    from ._dispatch import raise_dispatch_unregistered_type
    raise_dispatch_unregistered_type(column, kind="Series")


@_duration_seconds.specialize("pandas", argument_type="Column")
def _duration_seconds_pandas(column):
    result = column.astype("timedelta64[ns]").view("int64").astype(float) / 1e9
    result[column.isna().to_numpy()] = np.nan
    return result


@_duration_seconds.specialize("polars", argument_type="Column")
def _duration_seconds_polars(column):
    import polars as pl
    scale = {"ns": 1e9, "us": 1e6, "ms": 1e3}[column.dtype.time_unit]
    return column.cast(pl.Float64) / scale


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
        seconds = np.asarray(_duration_seconds(column), dtype=float)
        if self.handle_negative == "clip":
            seconds = np.maximum(seconds, 0)
        elif self.handle_negative == "abs":
            seconds = np.abs(seconds)
        if self.components == "auto":
            valid = seconds[~np.isnan(seconds)]
            self.resolution_ = "minute" if len(valid) == 0 else self._auto_resolution(valid)
            self.components_ = _RESOLUTION_COMPONENTS[self.resolution_] + ["log1p_total_seconds"]
        else:
            self.resolution_ = self.resolution
            self.components_ = list(self.components)
        self.all_outputs_ = [f"{sbd.name(column)}_{c}" for c in self.components_]
        values = self._extract(seconds)
        if self.scaling is not None:
            self.scaling_params_ = {}
            for i, component in enumerate(self.components_):
                x = values[:, i]
                finite = x[~np.isnan(x)]
                if self.scaling == "minmax":
                    a, b = np.min(finite) if len(finite) else 0, np.max(finite) if len(finite) else 0
                    self.scaling_params_[component] = {"min": a, "max": b}
                elif self.scaling == "standard":
                    self.scaling_params_[component] = {"mean": np.nanmean(x), "std": np.nanstd(x)}
                else:
                    self.scaling_params_[component] = {"median": np.nanmedian(x), "iqr": np.nanpercentile(x, 75) - np.nanpercentile(x, 25)}
        return self.transform(column)

    def transform(self, column):
        check_is_fitted(self, "all_outputs_")
        seconds = np.asarray(_duration_seconds(column), dtype=float)
        if self.handle_negative == "clip":
            seconds = np.maximum(seconds, 0)
        elif self.handle_negative == "abs":
            seconds = np.abs(seconds)
        values = self._extract(seconds)
        if self.scaling is not None:
            for i, component in enumerate(self.components_):
                p = self.scaling_params_[component]
                if self.scaling == "minmax":
                    d = p["max"] - p["min"]; values[:, i] = 0 if d == 0 else np.clip((values[:, i] - p["min"]) / d, 0, 1)
                else:
                    center = p["mean"] if self.scaling == "standard" else p["median"]
                    scale = p["std"] if self.scaling == "standard" else p["iqr"]
                    values[:, i] = 0 if scale == 0 else (values[:, i] - center) / scale
        return sbd.copy_index(column, sbd.make_dataframe_like(column, {n: values[:, i].astype("float32") for i, n in enumerate(self.all_outputs_)}))

    def _extract(self, seconds):
        days = np.floor(seconds / 86400)
        remainder = seconds - days * 86400
        vals = {"total_seconds": seconds, "days": days,
                "hours": np.floor(remainder / 3600),
                "minutes": np.floor((remainder % 3600) / 60),
                "seconds": np.floor(remainder % 60),
                "microseconds": np.floor((remainder % 1) * 1e6),
                "log1p_total_seconds": np.log1p(np.maximum(seconds, 0)),
                "sin_of_day": np.sin(seconds / 86400 * 2 * np.pi),
                "cos_of_day": np.cos(seconds / 86400 * 2 * np.pi)}
        return np.column_stack([vals[c] for c in self.components_])

    def _auto_resolution(self, seconds):
        if np.any(~np.isclose(seconds % 86400, 0)):
            if np.any(~np.isclose(seconds % 3600, 0)):
                if np.any(~np.isclose(seconds % 60, 0)):
                    if np.any(~np.isclose(seconds % 1, 0)):
                        return "microsecond"
                    return "second"
                return "minute"
            return "hour"
        return "day"

    def _check_params(self):
        if self.components != "auto" and (not isinstance(self.components, (list, tuple))):
            raise TypeError("components must be 'auto' or a list/tuple.")
        if self.components != "auto" and any(c not in _COMPONENTS for c in self.components):
            raise ValueError("Unknown duration component.")
        if self.resolution not in ("auto",) + _RESOLUTIONS:
            raise ValueError("Unknown duration resolution.")
        if self.handle_negative not in ("keep", "clip", "abs"):
            raise ValueError("Unknown handle_negative value.")
        if self.scaling not in (None, "minmax", "standard", "robust"):
            raise ValueError("Unknown scaling value.")

    def get_feature_names_out(self, input_features=None):
        check_is_fitted(self, "all_outputs_")
        return np.asarray(self.all_outputs_)
