import numpy as np
from sklearn.utils.validation import check_is_fitted

from . import _dataframe as sbd
from ._dispatch import dispatch
from ._single_column_transformer import RejectColumn, SingleColumnTransformer
from ._sklearn_compat import TransformerTags

__all__ = ["DurationEncoder"]

# Integer nanoseconds per remainder unit. Component breakdown follows pandas'
# timedelta normalization: floor-divide the total, with a non-negative remainder.
_NS_PER_US = np.int64(1_000)
_NS_PER_S = np.int64(1_000_000_000)
_NS_PER_MIN = np.int64(60) * _NS_PER_S
_NS_PER_HOUR = np.int64(60) * _NS_PER_MIN
_NS_PER_DAY = np.int64(24) * _NS_PER_HOUR

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
_VALID_COMPONENT_SET = set(_VALID_COMPONENTS)

# Remainder components included at each resolution, finest last in this list
# but emitted in descending granularity (hours before minutes, and so on).
_RESOLUTION_REMAINDERS = {
    "day": (),
    "hour": ("hours",),
    "minute": ("hours", "minutes"),
    "second": ("hours", "minutes", "seconds"),
    "microsecond": ("hours", "minutes", "seconds", "microseconds"),
}
_RESOLUTIONS = tuple(_RESOLUTION_REMAINDERS)

_HANDLE_NEGATIVE = ("clip", "abs", "keep")
_SCALINGS = ("minmax", "standard", "robust")

# Native numpy timedelta units, in nanoseconds (before the dtype step count).
_UNIT_TO_NS = {
    "ns": 1,
    "us": 1_000,
    "ms": 1_000_000,
    "s": 1_000_000_000,
    "m": 60_000_000_000,
    "h": 3_600_000_000_000,
    "D": 86_400_000_000_000,
    "W": 604_800_000_000_000,
}


def _components_for_resolution(resolution):
    return [
        "total_seconds",
        "days",
        *_RESOLUTION_REMAINDERS[resolution],
        "log1p_total_seconds",
    ]


def _infer_resolution(work, null_mask):
    """Finest resolution whose remainder is not identically zero.

    ``work`` is the duration in nanoseconds after ``handle_negative``. Null
    rows must be ignored. An all-null column defaults to ``"minute"``.
    """
    valid = work[~null_mask]
    if valid.size == 0:
        return "minute"
    if not np.all(valid % _NS_PER_S == 0):
        return "microsecond"
    if not np.all(valid % _NS_PER_MIN == 0):
        return "second"
    if not np.all(valid % _NS_PER_HOUR == 0):
        return "minute"
    if not np.all(valid % _NS_PER_DAY == 0):
        return "hour"
    return "day"


def _apply_negative_policy(ns, null_mask, how):
    work = np.array(ns, dtype=np.int64, copy=True)
    work[null_mask] = 0
    if how == "clip":
        np.maximum(work, np.int64(0), out=work)
    elif how == "abs":
        np.absolute(work, out=work)
    return work


def _extract_components(work, null_mask):
    """Map nanosecond totals to every supported component.

    Null positions are zero in ``work`` and restored to NaN here.
    """
    days = np.floor_divide(work, _NS_PER_DAY).astype(np.float64)
    rem_day = np.remainder(work, _NS_PER_DAY)
    hours = np.floor_divide(rem_day, _NS_PER_HOUR).astype(np.float64)
    rem_hour = np.remainder(rem_day, _NS_PER_HOUR)
    minutes = np.floor_divide(rem_hour, _NS_PER_MIN).astype(np.float64)
    rem_min = np.remainder(rem_hour, _NS_PER_MIN)
    seconds = np.floor_divide(rem_min, _NS_PER_S).astype(np.float64)
    rem_s = np.remainder(rem_min, _NS_PER_S)
    microseconds = rem_s.astype(np.float64) / np.float64(_NS_PER_US)
    total_seconds = work.astype(np.float64) / 1e9
    with np.errstate(divide="ignore", invalid="ignore"):
        log1p_total_seconds = np.log1p(total_seconds)
    frac_day = rem_day.astype(np.float64) / np.float64(_NS_PER_DAY)
    angle = 2.0 * np.pi * frac_day
    features = {
        "total_seconds": total_seconds,
        "days": days,
        "hours": hours,
        "minutes": minutes,
        "seconds": seconds,
        "microseconds": microseconds,
        "log1p_total_seconds": log1p_total_seconds,
        "sin_of_day": np.sin(angle),
        "cos_of_day": np.cos(angle),
    }
    if null_mask.any():
        for values in features.values():
            values[null_mask] = np.nan
    return features


def _finite_values(values):
    values = np.asarray(values, dtype=np.float64)
    return values[np.isfinite(values)]


def _fit_scaling_params(values, method):
    valid = _finite_values(values)
    if method == "minmax":
        if valid.size == 0:
            return {"min": 0.0, "max": 0.0}
        return {"min": float(np.min(valid)), "max": float(np.max(valid))}
    if method == "standard":
        if valid.size == 0:
            return {"mean": 0.0, "std": 0.0}
        # Population standard deviation (ddof=0), matching StandardScaler.
        # A constant column, including a single observation, has std 0.
        return {
            "mean": float(np.mean(valid)),
            "std": float(np.std(valid, ddof=0)),
        }
    if valid.size == 0:
        return {"median": 0.0, "iqr": 0.0}
    q25, q75 = np.percentile(valid, [25, 75])
    return {"median": float(np.median(valid)), "iqr": float(q75 - q25)}


def _scale_values(values, params, method):
    values = np.asarray(values, dtype=np.float64)
    null = np.isnan(values)
    if method == "minmax":
        span = params["max"] - params["min"]
        if not np.isfinite(span) or span == 0.0:
            out = np.zeros(values.shape, dtype=np.float64)
        else:
            out = (values - params["min"]) / span
            out = np.clip(out, 0.0, 1.0)
    elif method == "standard":
        std = params["std"]
        if not np.isfinite(std) or std == 0.0:
            out = np.zeros(values.shape, dtype=np.float64)
        else:
            out = (values - params["mean"]) / std
    else:
        iqr = params["iqr"]
        if not np.isfinite(iqr) or iqr == 0.0:
            out = np.zeros(values.shape, dtype=np.float64)
        else:
            out = (values - params["median"]) / iqr
    out[null] = np.nan
    return out


def _assemble_frame(column, features):
    """Build a dataframe whose columns follow ``features`` insertion order."""
    name = sbd.name(column)
    columns = []
    for component, values in features.items():
        values = np.asarray(values, dtype=np.float64)
        feature_name = f"{name}_{component}"
        feature = sbd.make_column_like(column, values, feature_name)
        if sbd.is_polars(feature):
            feature = feature.fill_nan(None)
        columns.append(feature)
    frame = sbd.make_dataframe_like(column, columns)
    return sbd.copy_index(column, frame)


@dispatch
def _duration_nanoseconds(col):
    # Avoid circular import
    from ._dispatch import raise_dispatch_unregistered_type

    raise_dispatch_unregistered_type(col, kind="Series")


@_duration_nanoseconds.specialize("pandas", argument_type="Column")
def _duration_nanoseconds_pandas(col):
    arr = col.to_numpy()
    unit, step = np.datetime_data(arr.dtype)
    if unit not in _UNIT_TO_NS:
        raise ValueError(
            f"Unsupported timedelta unit {unit!r}. "
            "DurationEncoder supports ns, us, ms, s, m, h, D and W."
        )
    raw = arr.astype(np.int64, copy=True)
    null_mask = np.asarray(col.isna(), dtype=bool)
    raw[null_mask] = 0
    multiplier = int(step) * _UNIT_TO_NS[unit]
    if multiplier != 1 and raw.size:
        limit = np.iinfo(np.int64).max // multiplier
        if np.any(np.abs(raw) > limit):
            raise ValueError(
                "Duration values are too large to represent as nanoseconds "
                "(the limit is about 292 years)."
            )
    ns = raw * np.int64(multiplier)
    return ns, null_mask


@_duration_nanoseconds.specialize("polars", argument_type="Column")
def _duration_nanoseconds_polars(col):
    ns_col = col.dt.total_nanoseconds()
    null_mask = np.asarray(ns_col.is_null().to_numpy(), dtype=bool)
    ns = np.asarray(ns_col.fill_null(0).to_numpy(), dtype=np.int64)
    return ns, null_mask


class DurationEncoder(SingleColumnTransformer):
    """Extract numeric features from a duration column.

    ``DurationEncoder`` converts a pandas ``timedelta64`` column or a polars
    ``Duration`` column into numeric features: the total length in seconds,
    calendar-style remainders (days, hours, minutes, seconds, microseconds),
    a ``log1p`` of the total length, and optional cyclical time-of-day features.

    Parameters
    ----------
    components : "auto" or list/tuple of str, default="auto"
        Which features to extract. ``"auto"`` selects a list from ``resolution``
        (see below). An explicit list or tuple names the components and ignores
        ``resolution``. The output column order is the order of that list.

        Valid names are ``"total_seconds"``, ``"days"``, ``"hours"`` (remainder
        after days), ``"minutes"`` (remainder after hours), ``"seconds"``
        (remainder seconds), ``"microseconds"`` (sub-second remainder, in
        microseconds), ``"log1p_total_seconds"``, ``"sin_of_day"`` and
        ``"cos_of_day"``.

        Passing a non-sequence (for example an integer) raises ``TypeError``.
        A string other than ``"auto"`` also raises ``TypeError``. Unknown names
        inside a list or tuple raise ``ValueError``.

    resolution : {"auto", "day", "hour", "minute", "second", "microsecond"}, \
            default="auto"
        Finest remainder granularity used when ``components="auto"``. The
        extracted columns are always, in this order: ``"total_seconds"``,
        ``"days"``, the remainder components up to the chosen resolution in
        descending granularity, then ``"log1p_total_seconds"``.

        - ``"day"``: ``total_seconds``, ``days``, ``log1p_total_seconds``
        - ``"hour"``: adds ``hours``
        - ``"minute"``: adds ``minutes``
        - ``"second"``: adds ``seconds``
        - ``"microsecond"``: adds ``microseconds``

        ``"sin_of_day"`` and ``"cos_of_day"`` are not part of any resolution.
        They are available only through an explicit ``components`` list.

        ``"auto"`` inspects the fitted data and keeps the finest level that
        carries information. For example, if every duration is a whole number
        of days, the resolution is ``"day"``. If every value is null, the
        resolution is ``"minute"``.

    handle_negative : {"keep", "clip", "abs"}, default="keep"
        How to treat negative durations before extracting features.
        ``"clip"`` replaces them with a zero-length timedelta, ``"abs"`` takes
        the absolute value, and ``"keep"`` leaves them unchanged.

        With ``"keep"``, components follow pandas' timedelta normalization:
        ``days`` is ``floor(total / 1 day)`` and the remainder components are
        non-negative. ``log1p_total_seconds`` is NaN when the total is below
        -1 second.

    scaling : {"minmax", "standard", "robust"} or None, default=None
        Optional scaling applied to each extracted feature after it is
        computed.

        - ``None``: no scaling.
        - ``"minmax"``: scale to ``[0, 1]`` with the training minimum and
          maximum. Values outside that range are clipped to ``[0, 1]``.
        - ``"standard"``: subtract the training mean and divide by the
          population standard deviation (division by N).
        - ``"robust"``: subtract the training median and divide by the
          interquartile range (75th percentile minus 25th percentile).

        If the training range, standard deviation, or interquartile range is
        zero, that feature is returned as zeros. Null inputs stay null.

    Attributes
    ----------
    resolution_ : str or None
        Resolution used to build ``components_``. ``None`` when ``components``
        is an explicit list (``resolution`` is ignored).

    components_ : list of str
        Component names extracted by this encoder, in output order.

    scaling_params_ : dict
        Present only when ``scaling`` is not ``None``. Maps each component name
        to the statistics used to scale it: ``{"min", "max"}`` for
        ``"minmax"``, ``{"mean", "std"}`` for ``"standard"``, and
        ``{"median", "iqr"}`` for ``"robust"``.

    See Also
    --------
    DatetimeEncoder :
        Extract features from a datetime column.

    TableVectorizer :
        Route duration columns to a ``DurationEncoder``.

    Examples
    --------
    >>> from datetime import timedelta
    >>> import pandas as pd
    >>> from skrub import DurationEncoder

    >>> since_login = pd.Series(
    ...     [timedelta(days=1, hours=2), None, timedelta(minutes=30)],
    ...     name="since_login",
    ... )
    >>> DurationEncoder(components=["days", "hours", "minutes"]).fit_transform(
    ...     since_login
    ... )
       since_login_days  since_login_hours  since_login_minutes
    0               1.0                2.0                  0.0
    1               NaN                NaN                  NaN
    2               0.0                0.0                 30.0

    ``resolution="auto"`` drops remainder columns that are zero for every
    observed value. Thirty minutes forces resolution ``"minute"``:

    >>> encoder = DurationEncoder().fit(since_login)
    >>> encoder.resolution_
    'minute'
    >>> encoder.components_
    ['total_seconds', 'days', 'hours', 'minutes', 'log1p_total_seconds']
    >>> encoder.get_feature_names_out()
    ['since_login_total_seconds', 'since_login_days', 'since_login_hours', 'since_login_minutes', 'since_login_log1p_total_seconds']

    Whole days stop at ``"day"``:

    >>> whole_days = pd.Series([timedelta(days=2), timedelta(days=5)], name="stay")
    >>> DurationEncoder().fit(whole_days).resolution_
    'day'

    An all-null column defaults to ``"minute"``:

    >>> all_null = pd.Series([pd.NaT, pd.NaT], dtype="timedelta64[ns]", name="stay")
    >>> DurationEncoder().fit(all_null).resolution_
    'minute'

    Negative durations can be kept, clipped to zero, or replaced by their
    absolute value. Kept values use pandas' normalized components
    (``-3 hours`` is ``-1 days + 21 hours``):

    >>> overdue = pd.Series([timedelta(hours=-3)], name="overdue")
    >>> DurationEncoder(
    ...     components=["total_seconds", "days", "hours"], handle_negative="keep"
    ... ).fit_transform(overdue)
       overdue_total_seconds  overdue_days  overdue_hours
    0               -10800.0          -1.0           21.0
    >>> DurationEncoder(
    ...     components=["total_seconds", "days", "hours"], handle_negative="abs"
    ... ).fit_transform(overdue)
       overdue_total_seconds  overdue_days  overdue_hours
    0                10800.0           0.0            3.0
    >>> DurationEncoder(
    ...     components=["total_seconds", "days", "hours"], handle_negative="clip"
    ... ).fit_transform(overdue)
       overdue_total_seconds  overdue_days  overdue_hours
    0                     0.0           0.0            0.0

    ``scaling`` is fit on the training column. ``"minmax"`` maps the training
    range to ``[0, 1]`` and clips unseen values:

    >>> wait = pd.Series(
    ...     [timedelta(seconds=0), timedelta(seconds=10)], name="wait"
    ... )
    >>> DurationEncoder(components=["total_seconds"], scaling="minmax").fit_transform(
    ...     wait
    ... )
       wait_total_seconds
    0                 0.0
    1                 1.0

    A constant column has a zero range, so the scaled output is zeros:

    >>> constant = pd.Series(
    ...     [timedelta(seconds=5), timedelta(seconds=5)], name="wait"
    ... )
    >>> DurationEncoder(
    ...     components=["total_seconds"], scaling="standard"
    ... ).fit_transform(constant)
       wait_total_seconds
    0                 0.0
    1                 0.0

    Non-duration columns are rejected:

    >>> DurationEncoder().fit_transform(pd.Series(["1 day", "2 days"], name="stay"))
    Traceback (most recent call last):
        ...
    skrub._single_column_transformer.RejectColumn: Column 'stay' does not have a duration dtype.

    ``components`` must be ``"auto"`` or a list or tuple of names:

    >>> DurationEncoder(components=1).fit(since_login)
    Traceback (most recent call last):
        ...
    TypeError: components must be 'auto' or a list or tuple of component names; got int.
    >>> DurationEncoder(components=["days", "fortnights"]).fit(since_login)
    Traceback (most recent call last):
        ...
    ValueError: Unknown duration component(s): ['fortnights']. Valid components are: ['total_seconds', 'days', 'hours', 'minutes', 'seconds', 'microseconds', 'log1p_total_seconds', 'sin_of_day', 'cos_of_day'].
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
        """Fit the encoder and transform a duration column.

        Parameters
        ----------
        column : pandas or polars Series with a duration dtype
            The input to transform.

        y : None
            Ignored.

        Returns
        -------
        transformed : DataFrame
            One numeric column per extracted component.
        """
        del y
        self._validate_params()
        if not sbd.is_duration(column):
            raise RejectColumn(
                f"Column {sbd.name(column)!r} does not have a duration dtype."
            )
        ns, null_mask = _duration_nanoseconds(column)
        work = _apply_negative_policy(ns, null_mask, self.handle_negative)
        self._resolve_components(work, null_mask)
        self._fit_scaling(work, null_mask)
        return self.transform(column)

    def transform(self, column):
        """Transform a duration column.

        Parameters
        ----------
        column : pandas or polars Series with a duration dtype
            The input to transform.

        Returns
        -------
        transformed : DataFrame
            One numeric column per extracted component.
        """
        check_is_fitted(self, "components_")
        ns, null_mask = _duration_nanoseconds(column)
        work = _apply_negative_policy(ns, null_mask, self.handle_negative)
        extracted = _extract_components(work, null_mask)
        selected = {name: extracted[name] for name in self.components_}
        if self.scaling is not None:
            selected = {
                name: _scale_values(
                    selected[name], self.scaling_params_[name], self.scaling
                )
                for name in self.components_
            }
        frame = _assemble_frame(column, selected)
        self.all_outputs_ = sbd.column_names(frame)
        return frame

    def _validate_params(self):
        if self.handle_negative not in _HANDLE_NEGATIVE:
            raise ValueError(
                f"handle_negative must be one of {_HANDLE_NEGATIVE}, "
                f"got {self.handle_negative!r}."
            )
        if self.scaling not in (None, *_SCALINGS):
            raise ValueError(
                f"scaling must be None or one of {_SCALINGS}, got {self.scaling!r}."
            )
        components = self.components
        if components == "auto":
            if self.resolution not in ("auto", *_RESOLUTIONS):
                raise ValueError(
                    "resolution must be 'auto' or one of "
                    f"{list(_RESOLUTIONS)}, got {self.resolution!r}."
                )
            return
        if isinstance(components, (str, bytes)) or not isinstance(
            components, (list, tuple)
        ):
            raise TypeError(
                "components must be 'auto' or a list or tuple of component names; "
                f"got {type(components).__name__}."
            )
        unknown = [name for name in components if name not in _VALID_COMPONENT_SET]
        if unknown:
            raise ValueError(
                f"Unknown duration component(s): {unknown}. "
                f"Valid components are: {list(_VALID_COMPONENTS)}."
            )
        duplicates = [name for name in components if components.count(name) > 1]
        if duplicates:
            raise ValueError(
                f"Duplicate duration component(s): {sorted(set(duplicates))}."
            )
        if len(components) == 0:
            raise ValueError("components must contain at least one component name.")

    def _resolve_components(self, work, null_mask):
        if self.components == "auto":
            if self.resolution == "auto":
                resolution = _infer_resolution(work, null_mask)
            else:
                resolution = self.resolution
            self.resolution_ = resolution
            self.components_ = _components_for_resolution(resolution)
            return
        # An explicit list ignores resolution.
        self.resolution_ = None
        self.components_ = list(self.components)

    def _fit_scaling(self, work, null_mask):
        if self.scaling is None:
            if hasattr(self, "scaling_params_"):
                del self.scaling_params_
            return
        extracted = _extract_components(work, null_mask)
        self.scaling_params_ = {
            name: _fit_scaling_params(extracted[name], self.scaling)
            for name in self.components_
        }

    def _more_tags(self):
        return {"preserves_dtype": []}

    def __sklearn_tags__(self):
        tags = super().__sklearn_tags__()
        tags.transformer_tags = TransformerTags(preserves_dtype=[])
        return tags
