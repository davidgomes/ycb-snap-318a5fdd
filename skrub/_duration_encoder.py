"""Encode duration (timedelta) columns as numeric features."""

import numpy as np
from sklearn.utils.validation import check_is_fitted

from . import _dataframe as sbd
from ._single_column_transformer import RejectColumn, SingleColumnTransformer
from ._sklearn_compat import TransformerTags

__all__ = ["DurationEncoder"]

_VALID_COMPONENTS = (
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

# Remainder components included at each resolution, finest last.
_REMAINDERS = {
    "day": (),
    "hour": ("hours",),
    "minute": ("hours", "minutes"),
    "second": ("hours", "minutes", "seconds"),
    "microsecond": ("hours", "minutes", "seconds", "microseconds"),
}

_NS_PER_US = 1_000
_NS_PER_SECOND = 1_000_000_000
_NS_PER_MINUTE = 60 * _NS_PER_SECOND
_NS_PER_HOUR = 60 * _NS_PER_MINUTE
_NS_PER_DAY = 24 * _NS_PER_HOUR

_HANDLE_NEGATIVE = ("keep", "clip", "abs")
_SCALING = (None, "minmax", "standard", "robust")


def _components_for_resolution(resolution):
    return [
        "total_seconds",
        "days",
        *_REMAINDERS[resolution],
        "log1p_total_seconds",
    ]


def _duration_nanoseconds(col):
    """Return ``(nanoseconds int64, null mask)`` for a duration column."""
    n_rows = sbd.shape(col)[0]
    if sbd.is_pandas(col):
        values = col.to_numpy()
        nulls = np.isnat(values)
        ns = np.zeros(n_rows, dtype=np.int64)
        if np.any(~nulls):
            ns[~nulls] = values[~nulls].astype("timedelta64[ns]").astype(np.int64)
        return ns, nulls

    unit = str(col.dtype.time_unit)
    if unit == "ns":
        raw = col.dt.total_nanoseconds()
        factor = 1
    elif unit == "us":
        raw = col.dt.total_microseconds()
        factor = _NS_PER_US
    elif unit == "ms":
        raw = col.dt.total_milliseconds()
        factor = 1_000_000
    else:
        raise RejectColumn(
            f"Column {sbd.name(col)!r} has unsupported duration unit {unit!r}."
        )
    nulls = np.asarray(raw.is_null().to_numpy(), dtype=bool)
    filled = np.asarray(raw.fill_null(0).to_numpy(), dtype=np.int64)
    return filled * np.int64(factor), nulls


def _apply_handle_negative(ns, nulls, handle_negative):
    out = ns.copy()
    valid = ~nulls
    if handle_negative == "clip":
        out[valid] = np.maximum(out[valid], 0)
    elif handle_negative == "abs":
        out[valid] = np.abs(out[valid])
    return out


def _infer_resolution(ns, nulls):
    valid = ns[~nulls]
    if valid.size == 0:
        return "minute"
    if np.any(valid % _NS_PER_SECOND != 0):
        return "microsecond"
    if np.any(valid % _NS_PER_MINUTE != 0):
        return "second"
    if np.any(valid % _NS_PER_HOUR != 0):
        return "minute"
    if np.any(valid % _NS_PER_DAY != 0):
        return "hour"
    return "day"


def _extract_components(ns, nulls):
    """Map integer nanoseconds to the supported numeric components.

    Remainder components follow ``datetime.timedelta`` normalization: the
    day count is floored and hours/minutes/seconds/microseconds stay
    non-negative. Sub-microsecond residues are dropped.
    """
    days = np.floor_divide(ns, _NS_PER_DAY)
    rem = ns - days * _NS_PER_DAY
    hours = rem // _NS_PER_HOUR
    rem = rem % _NS_PER_HOUR
    minutes = rem // _NS_PER_MINUTE
    rem = rem % _NS_PER_MINUTE
    seconds = rem // _NS_PER_SECOND
    rem = rem % _NS_PER_SECOND
    microseconds = rem // _NS_PER_US
    total_seconds = ns / _NS_PER_SECOND
    frac = (ns - days * _NS_PER_DAY) / _NS_PER_DAY
    angle = 2.0 * np.pi * frac
    with np.errstate(invalid="ignore"):
        log_total = np.log1p(total_seconds)

    extracted = {
        "total_seconds": total_seconds,
        "days": days.astype(np.float64),
        "hours": hours.astype(np.float64),
        "minutes": minutes.astype(np.float64),
        "seconds": seconds.astype(np.float64),
        "microseconds": microseconds.astype(np.float64),
        "log1p_total_seconds": log_total,
        "sin_of_day": np.sin(angle),
        "cos_of_day": np.cos(angle),
    }
    for values in extracted.values():
        values[nulls] = np.nan
    return extracted


def _fit_scaling(values, scaling):
    finite = values[np.isfinite(values)]
    if scaling == "minmax":
        if finite.size == 0:
            return {"min": 0.0, "max": 0.0}
        return {"min": float(finite.min()), "max": float(finite.max())}
    if scaling == "standard":
        if finite.size == 0:
            return {"mean": 0.0, "std": 0.0}
        return {"mean": float(finite.mean()), "std": float(finite.std(ddof=0))}
    if finite.size == 0:
        return {"median": 0.0, "iqr": 0.0}
    q1, median, q3 = np.percentile(finite, [25, 50, 75])
    return {
        "median": float(median),
        "iqr": float(q3 - q1),
    }


def _apply_scaling(values, scaling, params):
    out = values.astype(np.float64, copy=True)
    nulls = ~np.isfinite(values)
    if scaling == "minmax":
        span = params["max"] - params["min"]
        if span == 0:
            out = np.zeros_like(out)
        else:
            out = (out - params["min"]) / span
            out = np.clip(out, 0.0, 1.0)
    elif scaling == "standard":
        if params["std"] == 0:
            out = np.zeros_like(out)
        else:
            out = (out - params["mean"]) / params["std"]
    else:
        if params["iqr"] == 0:
            out = np.zeros_like(out)
        else:
            out = (out - params["median"]) / params["iqr"]
    out[nulls] = np.nan
    return out


class DurationEncoder(SingleColumnTransformer):
    """Extract numeric features from a duration (timedelta) column.

    ``DurationEncoder`` turns a single ``timedelta64`` (pandas) or ``Duration``
    (polars) column into numeric features such as the total length in seconds,
    calendar-style remainder components, and an optional log transform.

    Parameters
    ----------
    components : "auto" or list of str, default="auto"
        Which features to extract. ``"auto"`` selects a list from ``resolution``.
        An explicit list or tuple of component names is used as-is and
        ``resolution`` is ignored. Valid names are ``"total_seconds"``,
        ``"days"``, ``"hours"``, ``"minutes"``, ``"seconds"``,
        ``"microseconds"``, ``"log1p_total_seconds"``, ``"sin_of_day"`` and
        ``"cos_of_day"``. ``"hours"`` is the remainder after whole days,
        ``"minutes"`` the remainder after whole hours, and ``"seconds"`` the
        remainder seconds. The cyclical features are only produced when named
        explicitly.

    resolution : {"auto", "day", "hour", "minute", "second", "microsecond"}, \
            default="auto"
        Finest remainder component to extract when ``components="auto"``.
        ``"day"`` yields ``total_seconds``, ``days`` and ``log1p_total_seconds``.
        Finer values add ``hours``, then ``minutes``, ``seconds`` and
        ``microseconds`` before ``log1p_total_seconds``. ``"auto"`` inspects the
        training column and keeps the finest level that is not uniformly zero.
        All-null columns default to ``"minute"``.

    handle_negative : {"keep", "clip", "abs"}, default="keep"
        How to treat negative durations before extracting features.
        ``"clip"`` replaces them with a zero-length duration, ``"abs"`` uses
        the absolute value, and ``"keep"`` leaves them unchanged.

    scaling : {"minmax", "standard", "robust"} or None, default=None
        Optional scaling applied independently to each extracted feature.
        ``"minmax"`` scales to ``[0, 1]`` with the training min and max and
        clips unseen values to that range. ``"standard"`` subtracts the
        training mean and divides by the training standard deviation.
        ``"robust"`` subtracts the training median and divides by the training
        interquartile range. A zero range, standard deviation or interquartile
        range produces zeros. ``None`` leaves the extracted values unchanged.

    Attributes
    ----------
    components_ : list of str
        The component names that are extracted, in output order.

    resolution_ : str or None
        The resolution used to build ``components_`` when ``components="auto"``.
        ``None`` when an explicit component list was provided.

    scaling_params_ : dict
        Per-component training statistics used for scaling. Present only when
        ``scaling`` is not ``None``.

    See Also
    --------
    DatetimeEncoder :
        Extract features from datetime columns.

    Notes
    -----
    Null inputs stay null in every output column. Non-duration columns are
    rejected with ``RejectColumn``.

    Examples
    --------
    >>> import pandas as pd
    >>> from skrub import DurationEncoder
    >>> overtime = pd.to_timedelta(
    ...     pd.Series(["1 day", None, "3 hours"], name="overtime")
    ... )
    >>> encoded = DurationEncoder(resolution="hour").fit_transform(overtime)
    >>> list(encoded.columns)
    ['overtime_total_seconds', 'overtime_days', 'overtime_hours', 'overtime_log1p_total_seconds']
    >>> encoded["overtime_hours"].tolist()
    [0.0, nan, 3.0]
    """  # noqa: E501

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
        """Fit the encoder and transform a column.

        Parameters
        ----------
        column : pandas or polars Series with a duration dtype
            The input to transform.

        y : None
            Ignored.

        Returns
        -------
        transformed : DataFrame
            The extracted features.
        """
        del y
        self._check_params()
        if not sbd.is_duration(column):
            raise RejectColumn(
                f"Column {sbd.name(column)!r} does not have a duration dtype."
            )
        ns, nulls = _duration_nanoseconds(column)
        ns = _apply_handle_negative(ns, nulls, self.handle_negative)
        if self.components == "auto":
            if self.resolution == "auto":
                self.resolution_ = _infer_resolution(ns, nulls)
            else:
                self.resolution_ = self.resolution
            self.components_ = _components_for_resolution(self.resolution_)
        else:
            self.resolution_ = None
            self.components_ = list(self.components)

        extracted = _extract_components(ns, nulls)
        if self.scaling is None:
            if hasattr(self, "scaling_params_"):
                del self.scaling_params_
        else:
            self.scaling_params_ = {
                name: _fit_scaling(extracted[name], self.scaling)
                for name in self.components_
            }
        return self.transform(column)

    def transform(self, column):
        """Transform a column.

        Parameters
        ----------
        column : pandas or polars Series with a duration dtype
            The input to transform.

        Returns
        -------
        transformed : DataFrame
            The extracted features.
        """
        check_is_fitted(self, "components_")
        name = sbd.name(column)
        ns, nulls = _duration_nanoseconds(column)
        ns = _apply_handle_negative(ns, nulls, self.handle_negative)
        extracted = _extract_components(ns, nulls)
        frames = []
        output_names = []
        for component in self.components_:
            values = extracted[component]
            if self.scaling is not None:
                values = _apply_scaling(
                    values, self.scaling, self.scaling_params_[component]
                )
            out_name = f"{name}_{component}"
            output_names.append(out_name)
            series = sbd.to_float32(sbd.make_column_like(column, values, out_name))
            frames.append(series)
        self.all_outputs_ = output_names
        return sbd.copy_index(column, sbd.make_dataframe_like(column, frames))

    def get_feature_names_out(self, input_features=None):
        """Get output feature names for transformation.

        Parameters
        ----------
        input_features : array-like of str or None, default=None
            Ignored.

        Returns
        -------
        feature_names_out : list of str
            Transformed feature names, ``"{column}_{component}"``.
        """
        del input_features
        check_is_fitted(self, "all_outputs_")
        return list(self.all_outputs_)

    def _check_params(self):
        components = self.components
        if isinstance(components, str):
            if components != "auto":
                raise TypeError(
                    "components must be 'auto' or a list or tuple of "
                    f"component names, got {components!r}."
                )
        elif isinstance(components, (list, tuple)):
            unknown = [c for c in components if c not in _VALID_COMPONENTS]
            if unknown or any(not isinstance(c, str) for c in components):
                raise ValueError(
                    "Unknown duration components: "
                    f"{unknown or [c for c in components if not isinstance(c, str)]}. "
                    f"Valid components are {list(_VALID_COMPONENTS)}."
                )
        else:
            raise TypeError(
                "components must be 'auto' or a list or tuple of "
                f"component names, got {components!r}."
            )

        if self.resolution not in ("auto", *_RESOLUTIONS):
            raise ValueError(
                f"'resolution' must be 'auto' or one of {list(_RESOLUTIONS)}, "
                f"got {self.resolution!r}."
            )
        if self.handle_negative not in _HANDLE_NEGATIVE:
            raise ValueError(
                f"'handle_negative' must be one of {list(_HANDLE_NEGATIVE)}, "
                f"got {self.handle_negative!r}."
            )
        if self.scaling not in _SCALING:
            raise ValueError(
                f"'scaling' must be one of {list(_SCALING)}, got {self.scaling!r}."
            )

    def _more_tags(self):
        return {"preserves_dtype": []}

    def __sklearn_tags__(self):
        tags = super().__sklearn_tags__()
        tags.transformer_tags = TransformerTags(preserves_dtype=[])
        return tags
