from collections.abc import Sequence

import numpy as np
from sklearn.utils.validation import check_is_fitted

from . import _dataframe as sbd
from ._single_column_transformer import RejectColumn, SingleColumnTransformer

__all__ = ["DurationEncoder"]

_REMAINDERS = ["days", "hours", "minutes", "seconds", "microseconds"]
_RESOLUTIONS = ["day", "hour", "minute", "second", "microsecond"]
_COMPONENTS = ["total_seconds", *_REMAINDERS, "log1p_total_seconds", "sin_of_day", "cos_of_day"]


class DurationEncoder(SingleColumnTransformer):
    """Extract numerical features from a duration column."""

    def __init__(self, components="auto", resolution="auto", handle_negative="keep", scaling=None):
        self.components = components
        self.resolution = resolution
        self.handle_negative = handle_negative
        self.scaling = scaling

    def _check_params(self):
        if self.components != "auto":
            if not isinstance(self.components, Sequence) or isinstance(self.components, (str, bytes)):
                raise TypeError("'components' must be 'auto' or a list/tuple of strings.")
            unknown = set(self.components) - set(_COMPONENTS)
            if unknown:
                raise ValueError(f"Unknown duration components: {sorted(unknown)}.")
        if self.resolution not in ("auto", *_RESOLUTIONS):
            raise ValueError(f"Invalid resolution {self.resolution!r}.")
        if self.handle_negative not in ("keep", "clip", "abs"):
            raise ValueError(f"Invalid handle_negative {self.handle_negative!r}.")
        if self.scaling not in (None, "minmax", "standard", "robust"):
            raise ValueError(f"Invalid scaling {self.scaling!r}.")

    def _seconds(self, column):
        values = np.asarray(sbd.total_seconds(column), dtype=float)
        if self.handle_negative == "clip":
            values = np.maximum(values, 0)
        elif self.handle_negative == "abs":
            values = np.abs(values)
        return values

    def fit_transform(self, column, y=None):
        del y
        self._check_params()
        if not sbd.is_duration(column):
            raise RejectColumn(f"Column {sbd.name(column)!r} does not have Duration dtype.")
        seconds = self._seconds(column)
        if self.components == "auto":
            finite = seconds[np.isfinite(seconds)]
            if self.resolution == "auto":
                if len(finite) == 0:
                    self.resolution_ = "minute"
                elif np.any(np.abs(finite % 86400) > 1e-9):
                    self.resolution_ = "hour"
                    if np.any(np.abs(finite % 3600) > 1e-9):
                        self.resolution_ = "minute"
                    if np.any(np.abs(finite % 60) > 1e-9):
                        self.resolution_ = "second"
                    if np.any(np.abs(finite * 1e6 % 1) > 1e-6):
                        self.resolution_ = "microsecond"
                else:
                    self.resolution_ = "day"
            else:
                self.resolution_ = self.resolution
            end = _RESOLUTIONS.index(self.resolution_) + 1
            self.components_ = ["total_seconds", *_REMAINDERS[:end], "log1p_total_seconds"]
        else:
            self.components_ = list(self.components)
            self.resolution_ = self.resolution
        self.all_outputs_ = [f"{sbd.name(column)}_{c}" for c in self.components_]
        if self.scaling is not None:
            raw = self._extract(seconds)
            self.scaling_params_ = {}
            for i, component in enumerate(self.components_):
                vals = raw[:, i]
                valid = vals[np.isfinite(vals)]
                params = {}
                if self.scaling == "minmax":
                    params.update(min=float(np.min(valid)), max=float(np.max(valid)))
                elif self.scaling == "standard":
                    params.update(mean=float(np.mean(valid)), std=float(np.std(valid)))
                else:
                    params.update(median=float(np.median(valid)),
                                  iqr=float(np.percentile(valid, 75) - np.percentile(valid, 25)))
                self.scaling_params_[component] = params
        return self.transform(column)

    def _extract(self, seconds):
        days = np.floor(seconds / 86400)
        remainder = seconds - days * 86400
        hours = np.floor(remainder / 3600)
        remainder -= hours * 3600
        minutes = np.floor(remainder / 60)
        remainder -= minutes * 60
        whole_seconds = np.floor(remainder)
        microseconds = (remainder - whole_seconds) * 1e6
        values = {
            "total_seconds": seconds, "days": days, "hours": hours,
            "minutes": minutes, "seconds": whole_seconds, "microseconds": microseconds,
            "log1p_total_seconds": np.sign(seconds) * np.log1p(np.abs(seconds)),
            "sin_of_day": np.sin(seconds / 86400 * 2 * np.pi),
            "cos_of_day": np.cos(seconds / 86400 * 2 * np.pi),
        }
        return np.column_stack([values[c] for c in self.components_])

    def transform(self, column):
        check_is_fitted(self, "components_")
        values = self._extract(self._seconds(column))
        if self.scaling is not None:
            for i, component in enumerate(self.components_):
                p = self.scaling_params_[component]
                if self.scaling == "minmax":
                    span = p["max"] - p["min"]
                    values[:, i] = 0 if span == 0 else np.clip((values[:, i] - p["min"]) / span, 0, 1)
                elif self.scaling == "standard":
                    values[:, i] = 0 if p["std"] == 0 else (values[:, i] - p["mean"]) / p["std"]
                else:
                    values[:, i] = 0 if p["iqr"] == 0 else (values[:, i] - p["median"]) / p["iqr"]
        result = sbd.make_dataframe_like(column, {
            name: values[:, i] for i, name in enumerate(self.all_outputs_)
        })
        return sbd.copy_index(column, result)

    def get_feature_names_out(self, input_features=None):
        check_is_fitted(self, "all_outputs_")
        return np.asarray(self.all_outputs_)
