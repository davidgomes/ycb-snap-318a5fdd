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

# Remainder components, coarse to fine. ``total_seconds`` is always first and
# ``log1p_total_seconds`` always last; cyclical features are not part of a
# resolution level.
_REMAINDER_COMPONENTS = ("hours", "minutes", "seconds", "microseconds")

_RESOLUTION_LEVELS = ("day", "hour", "minute", "second", "microsecond")

_NS_MICROSECOND = 1_000
_NS_SECOND = 1_000_000_000
_NS_MINUTE = 60 * _NS_SECOND
_NS_HOUR = 60 * _NS_MINUTE
_NS_DAY = 24 * _NS_HOUR
_SECONDS_PER_DAY = 86_400.0

_RESOLUTION_COMPONENTS = {
    "day": ("total_seconds", "days", "log1p_total_seconds"),
    "hour": ("total_seconds", "days", "hours", "log1p_total_seconds"),
    "minute": (
        "total_seconds",
        "days",
        "hours",
        "minutes",
        "log1p_total_seconds",
    ),
    "second": (
        "total_seconds",
        "days",
        "hours",
        "minutes",
        "seconds",
        "log1p_total_seconds",
    ),
    "microsecond": (
        "total_seconds",
        "days",
        "hours",
        "minutes",
        "seconds",
        "microseconds",
        "log1p_total_seconds",
    ),
}


def _duration_to_nanoseconds(col):
    """Return int64 nanoseconds and a boolean null mask.

    Null entries are stored as 0 in the nanosecond array. The mask must be
    applied again after feature extraction.
    """
    arr = np.asarray(sbd.to_numpy(col))
    if not np.issubdtype(arr.dtype, np.timedelta64):
        raise RejectColumn(
            f"Column {sbd.name(col)!r} does not have a duration dtype."
        )
    null_mask = np.isnat(arr)
    nanoseconds = arr.astype("timedelta64[ns]").view(np.int64).copy()
    nanoseconds[null_mask] = 0
    return nanoseconds, null_mask


def _apply_handle_negative(nanoseconds, how):
    if how == "keep":
        return nanoseconds
    if how == "abs":
        return np.abs(nanoseconds)
    if how == "clip":
        return np.maximum(nanoseconds, np.int64(0))
    raise ValueError(
        f"'handle_negative' options are ['clip', 'abs', 'keep'], got {how!r}."
    )


def _infer_resolution(nanoseconds, null_mask):
    """Finest resolution whose remainder is not identically zero.

    A column of whole days resolves to ``"day"``. A column of whole hours that
    is not all whole days resolves to ``"hour"``, and so on. Sub-second values
    resolve to ``"microsecond"``. When every value is null, the resolution is
    ``"minute"``.
    """
    valid = np.abs(nanoseconds[~null_mask])
    if valid.size == 0:
        return "minute"
    if np.any(valid % _NS_SECOND != 0):
        return "microsecond"
    if np.any(valid % _NS_MINUTE != 0):
        return "second"
    if np.any(valid % _NS_HOUR != 0):
        return "minute"
    if np.any(valid % _NS_DAY != 0):
        return "hour"
    return "day"


def _split_duration(nanoseconds):
    """Split nanoseconds into day and clock remainders.

    Remainders follow :class:`datetime.timedelta` normalization: ``days`` is
    the floor quotient and ``hours``, ``minutes``, ``seconds`` and
    ``microseconds`` are non-negative. ``microseconds`` is the fractional
    second in whole microseconds (milliseconds included), in ``0 .. 999999``.
    """
    nanoseconds = nanoseconds.astype(np.int64, copy=False)
    days, remainder = np.divmod(nanoseconds, np.int64(_NS_DAY))
    hours, remainder = np.divmod(remainder, np.int64(_NS_HOUR))
    minutes, remainder = np.divmod(remainder, np.int64(_NS_MINUTE))
    seconds, remainder = np.divmod(remainder, np.int64(_NS_SECOND))
    microseconds = remainder // np.int64(_NS_MICROSECOND)
    return days, hours, minutes, seconds, microseconds


def _extract_components(nanoseconds, components):
    """Extract requested components as float64 arrays (nulls still zero)."""
    requested = set(components)
    extracted = {}
    total_seconds = nanoseconds.astype(np.float64) / 1e9
    if "total_seconds" in requested:
        extracted["total_seconds"] = total_seconds
    if requested & set(_REMAINDER_COMPONENTS + ("days",)):
        days, hours, minutes, seconds, microseconds = _split_duration(nanoseconds)
        parts = {
            "days": days,
            "hours": hours,
            "minutes": minutes,
            "seconds": seconds,
            "microseconds": microseconds,
        }
        for name, values in parts.items():
            if name in requested:
                extracted[name] = values.astype(np.float64, copy=False)
    if "log1p_total_seconds" in requested:
        with np.errstate(invalid="ignore", divide="ignore"):
            extracted["log1p_total_seconds"] = np.log1p(total_seconds)
    if "sin_of_day" in requested or "cos_of_day" in requested:
        angle = 2.0 * np.pi * total_seconds / _SECONDS_PER_DAY
        if "sin_of_day" in requested:
            extracted["sin_of_day"] = np.sin(angle)
        if "cos_of_day" in requested:
            extracted["cos_of_day"] = np.cos(angle)
    return extracted


def _finite_values(values):
    return values[np.isfinite(values)]


def _fit_scaling_params(scaling, extracted, component_order):
    params = {}
    for name in component_order:
        valid = _finite_values(extracted[name])
        if scaling == "minmax":
            if valid.size == 0:
                params[name] = {"min": np.nan, "max": np.nan}
            else:
                params[name] = {
                    "min": float(np.min(valid)),
                    "max": float(np.max(valid)),
                }
        elif scaling == "standard":
            if valid.size == 0:
                params[name] = {"mean": np.nan, "std": np.nan}
            else:
                params[name] = {
                    "mean": float(np.mean(valid)),
                    # Population standard deviation, matching StandardScaler.
                    "std": float(np.std(valid, ddof=0)),
                }
        elif scaling == "robust":
            if valid.size == 0:
                params[name] = {"median": np.nan, "iqr": np.nan}
            else:
                q25, median, q75 = np.percentile(valid, [25, 50, 75])
                params[name] = {
                    "median": float(median),
                    "iqr": float(q75 - q25),
                }
        else:
            raise ValueError(
                "'scaling' options are [None, 'minmax', 'standard', 'robust'], "
                f"got {scaling!r}."
            )
    return params


def _scale_values(scaling, params, values):
    """Scale ``values`` in place-returned array. Non-finite inputs stay NaN."""
    finite = np.isfinite(values)
    if scaling == "minmax":
        center = params["min"]
        scale = params["max"] - params["min"]
        clip = True
    elif scaling == "standard":
        center = params["mean"]
        scale = params["std"]
        clip = False
    else:
        center = params["median"]
        scale = params["iqr"]
        clip = False
    out = np.empty(values.shape, dtype=np.float64)
    if not np.isfinite(scale) or scale == 0.0:
        out[finite] = 0.0
    else:
        out[finite] = (values[finite] - center) / scale
        if clip:
            np.clip(out, 0.0, 1.0, out=out)
            # clip may have written into non-finite slots; restore them below
    out[~finite] = np.nan
    return out


def _to_output_column(like, values, name, null_mask):
    data = []
    for is_null, value in zip(null_mask, values):
        if is_null:
            data.append(None)
        else:
            data.append(float(value))
    column = sbd.make_column_like(like, data, name)
    return sbd.to_float32(column)


class DurationEncoder(SingleColumnTransformer):
    """Extract numeric features from a duration column.

    ``DurationEncoder`` converts pandas ``timedelta64`` or polars ``Duration``
    columns into numeric features such as the total number of seconds, calendar
    remainders (days, hours, …) and an optional log transform. It is the
    duration counterpart of :class:`~skrub.DatetimeEncoder`.

    Parameters
    ----------
    components : "auto" or list of str, default="auto"
        Features to extract. ``"auto"`` selects components from ``resolution``.
        Otherwise pass a list or tuple of component names. The valid names are
        ``"total_seconds"``, ``"days"``, ``"hours"`` (remainder after days),
        ``"minutes"`` (remainder after hours), ``"seconds"`` (remainder
        seconds), ``"microseconds"`` (fractional second, in microseconds),
        ``"log1p_total_seconds"``, ``"sin_of_day"`` and ``"cos_of_day"``.
        When ``components`` is an explicit list, ``resolution`` is ignored and
        features are emitted in the list order. A value that is not ``"auto"``
        or a list/tuple raises ``TypeError``. Unknown names raise ``ValueError``.

    resolution : {"auto", "day", "hour", "minute", "second", "microsecond"}, \
            default="auto"
        Finest remainder to extract when ``components="auto"``. The output is
        always ``"total_seconds"``, ``"days"``, the remainder components down to
        this resolution from coarsest to finest, then ``"log1p_total_seconds"``.
        ``"day"`` extracts ``["total_seconds", "days", "log1p_total_seconds"]``;
        ``"hour"`` adds ``"hours"``; ``"minute"`` adds ``"minutes"``; ``"second"``
        adds ``"seconds"``; ``"microsecond"`` adds ``"microseconds"``.
        ``"sin_of_day"`` and ``"cos_of_day"`` are never added by a resolution
        level. ``"auto"`` picks the finest level that is not identically zero
        on the fitted column (for example whole days resolve to ``"day"``).
        If every value is null, ``"auto"`` resolves to ``"minute"``.

    handle_negative : {"keep", "clip", "abs"}, default="keep"
        How to treat negative durations before extracting features.
        ``"clip"`` replaces them with a zero-length duration, ``"abs"`` takes
        the absolute value, and ``"keep"`` leaves them unchanged. With
        ``"keep"``, ``days`` and ``total_seconds`` may be negative while the
        clock remainders stay non-negative, matching
        :class:`datetime.timedelta`.

    scaling : {None, "minmax", "standard", "robust"}, default=None
        Optional scaling applied independently to each extracted feature, using
        statistics from ``fit``. ``None`` does not scale. ``"minmax"`` maps the
        training minimum and maximum to ``[0, 1]`` and clips unseen values to
        that range. ``"standard"`` subtracts the training mean and divides by
        the population standard deviation (``ddof=0``). ``"robust"`` subtracts
        the training median and divides by the interquartile range (75th
        percentile minus 25th percentile, linear interpolation). If the training
        range, standard deviation, or interquartile range is zero, that feature
        is returned as zeros. Non-finite inputs stay missing.

    Attributes
    ----------
    components_ : list of str
        The component names that are extracted, in output order.

    resolution_ : str or None
        The resolution used to build ``components_`` when ``components="auto"``.
        ``None`` when an explicit component list was provided.

    scaling_params_ : dict of dict
        Fitted per-component statistics. Present only when ``scaling`` is not
        ``None``. Keys depend on ``scaling``: ``"min"`` and ``"max"`` for
        ``"minmax"``; ``"mean"`` and ``"std"`` for ``"standard"``; ``"median"``
        and ``"iqr"`` for ``"robust"``.

    all_outputs_ : list of str
        Output column names, ``"{column}_{component}"``.

    See Also
    --------
    DatetimeEncoder :
        Extract numeric features from a datetime column.

    Notes
    -----
    All extracted features are float32 columns. Null inputs propagate to every
    output column. ``log1p_total_seconds`` is ``log(1 + total_seconds)`` after
    ``handle_negative``; it is NaN when ``total_seconds < -1``. ``sin_of_day``
    and ``cos_of_day`` are the sine and cosine of the duration's position
    within a 24 hour cycle.

    A column that is not a pandas ``timedelta64`` or a polars ``Duration`` is
    rejected with ``RejectColumn``. The ``TableVectorizer`` sends duration
    columns to its ``duration`` transformer, so a ``DurationEncoder`` is safe
    to use there.

    Examples
    --------
    >>> import pandas as pd
    >>> from datetime import timedelta
    >>> from skrub import DurationEncoder
    >>> since = pd.Series(
    ...     [timedelta(days=2), timedelta(hours=6), None], name="since"
    ... )
    >>> encoder = DurationEncoder()
    >>> encoded = encoder.fit_transform(since)
    >>> encoder.resolution_
    'hour'
    >>> encoder.components_
    ['total_seconds', 'days', 'hours', 'log1p_total_seconds']
    >>> encoded["since_days"]
    0    2.0
    1    0.0
    2    NaN
    Name: since_days, dtype: float32
    >>> encoded["since_hours"]
    0    0.0
    1    6.0
    2    NaN
    Name: since_hours, dtype: float32

    Whole days do not produce clock remainders when ``resolution="auto"``:

    >>> contract = pd.Series(
    ...     [timedelta(days=1), timedelta(days=30)], name="contract"
    ... )
    >>> DurationEncoder().fit(contract).resolution_
    'day'

    An explicit component list ignores ``resolution``. Cyclical features are
    only available this way:

    >>> quarter = pd.Series([timedelta(hours=6)], name="quarter")
    >>> encoder = DurationEncoder(
    ...     components=["sin_of_day", "cos_of_day"], resolution="day"
    ... )
    >>> encoder.fit_transform(quarter).columns.tolist()
    ['quarter_sin_of_day', 'quarter_cos_of_day']
    >>> encoder.resolution_ is None
    True

    Non-duration columns are rejected:

    >>> DurationEncoder().fit_transform(pd.Series([1, 2], name="x"))
    Traceback (most recent call last):
        ...
    skrub._single_column_transformer.RejectColumn: Column 'x' does not have a duration dtype.
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
            The extracted features.
        """
        del y
        self._check_params()
        if not sbd.is_duration(column):
            raise RejectColumn(
                f"Column {sbd.name(column)!r} does not have a duration dtype."
            )
        nanoseconds, null_mask = _duration_to_nanoseconds(column)
        nanoseconds = _apply_handle_negative(nanoseconds, self.handle_negative)
        self._resolve_components(nanoseconds, null_mask)
        extracted = self._masked_components(nanoseconds, null_mask)
        if self.scaling is None:
            if hasattr(self, "scaling_params_"):
                del self.scaling_params_
        else:
            self.scaling_params_ = _fit_scaling_params(
                self.scaling, extracted, self.components_
            )
        return self._assemble(column, extracted, null_mask)

    def transform(self, column):
        """Transform a duration column.

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
        if not sbd.is_duration(column):
            raise RejectColumn(
                f"Column {sbd.name(column)!r} does not have a duration dtype."
            )
        nanoseconds, null_mask = _duration_to_nanoseconds(column)
        nanoseconds = _apply_handle_negative(nanoseconds, self.handle_negative)
        extracted = self._masked_components(nanoseconds, null_mask)
        return self._assemble(column, extracted, null_mask)

    def _check_params(self):
        if self.handle_negative not in ("clip", "abs", "keep"):
            raise ValueError(
                "'handle_negative' options are ['clip', 'abs', 'keep'], "
                f"got {self.handle_negative!r}."
            )
        if self.scaling not in (None, "minmax", "standard", "robust"):
            raise ValueError(
                "'scaling' options are [None, 'minmax', 'standard', 'robust'], "
                f"got {self.scaling!r}."
            )
        components = self.components
        if components == "auto":
            allowed = ("auto",) + _RESOLUTION_LEVELS
            if self.resolution not in allowed:
                raise ValueError(
                    f"'resolution' options are {list(allowed)}, "
                    f"got {self.resolution!r}."
                )
            return
        if isinstance(components, (list, tuple)):
            unknown = [name for name in components if name not in _VALID_COMPONENTS]
            if unknown or not all(isinstance(name, str) for name in components):
                bad = unknown or [
                    name for name in components if not isinstance(name, str)
                ]
                raise ValueError(
                    f"Unknown duration component(s) {bad!r}. "
                    f"Valid components are {list(_VALID_COMPONENTS)}."
                )
            if len(set(components)) != len(components):
                raise ValueError(
                    "Duplicate duration components are not allowed, "
                    f"got {list(components)!r}."
                )
            return
        raise TypeError(
            "components must be 'auto' or a list or tuple of component names, "
            f"got {type(components).__name__}."
        )

    def _resolve_components(self, nanoseconds, null_mask):
        if self.components == "auto":
            if self.resolution == "auto":
                self.resolution_ = _infer_resolution(nanoseconds, null_mask)
            else:
                self.resolution_ = self.resolution
            self.components_ = list(_RESOLUTION_COMPONENTS[self.resolution_])
        else:
            self.resolution_ = None
            self.components_ = list(self.components)

    def _masked_components(self, nanoseconds, null_mask):
        extracted = _extract_components(nanoseconds, self.components_)
        for name in self.components_:
            values = extracted[name]
            values = values.copy()
            values[null_mask] = np.nan
            extracted[name] = values
        return extracted

    def _assemble(self, column, extracted, null_mask):
        if self.scaling is not None:
            check_is_fitted(self, "scaling_params_")
            for name in self.components_:
                extracted[name] = _scale_values(
                    self.scaling, self.scaling_params_[name], extracted[name]
                )
        col_name = sbd.name(column)
        output_columns = []
        self.all_outputs_ = []
        for name in self.components_:
            feature_name = f"{col_name}_{name}"
            self.all_outputs_.append(feature_name)
            output_columns.append(
                _to_output_column(column, extracted[name], feature_name, null_mask)
            )
        if not output_columns:
            return _empty_frame(column)
        frame = sbd.make_dataframe_like(column, output_columns)
        return sbd.copy_index(column, frame)

    def _more_tags(self):
        return {"preserves_dtype": []}

    def __sklearn_tags__(self):
        tags = super().__sklearn_tags__()
        tags.transformer_tags = TransformerTags(preserves_dtype=[])
        return tags


def _empty_frame(column):
    """Dataframe with the same number of rows as ``column`` and no columns."""
    n_rows = sbd.shape(column)[0]
    if sbd.is_pandas(column):
        import pandas as pd

        return pd.DataFrame(index=sbd.index(column))
    import polars as pl

    if n_rows == 0:
        return pl.DataFrame()
    return pl.DataFrame({"__tmp": [None] * n_rows}).drop("__tmp")
