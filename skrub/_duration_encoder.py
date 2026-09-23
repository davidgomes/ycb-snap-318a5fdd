import numpy as np
from sklearn.utils.validation import check_is_fitted

from . import _dataframe as sbd
from ._dispatch import dispatch
from ._single_column_transformer import RejectColumn, SingleColumnTransformer
from ._sklearn_compat import TransformerTags

__all__ = ["DurationEncoder"]

# Component order used by resolution presets. An explicit ``components`` list
# keeps the order the user provided; these names are only the allowed set and
# the order of the automatic presets.
_COMPONENT_ORDER = (
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
_COMPONENT_SET = frozenset(_COMPONENT_ORDER)

_RESOLUTIONS = ("auto", "day", "hour", "minute", "second", "microsecond")
_HANDLE_NEGATIVE = ("keep", "clip", "abs")
_SCALINGS = (None, "minmax", "standard", "robust")

# Remainder components included at each resolution, finest last.
_REMAINDERS_BY_RESOLUTION = {
    "day": (),
    "hour": ("hours",),
    "minute": ("hours", "minutes"),
    "second": ("hours", "minutes", "seconds"),
    "microsecond": ("hours", "minutes", "seconds", "microseconds"),
}

_NS_PER_SECOND = 1_000_000_000
_NS_PER_MINUTE = 60 * _NS_PER_SECOND
_NS_PER_HOUR = 60 * _NS_PER_MINUTE
_NS_PER_DAY = 24 * _NS_PER_HOUR
_SECONDS_PER_DAY = 24 * 60 * 60

_PANDAS_UNIT_NS = {
    "ns": 1,
    "us": 1_000,
    "ms": 1_000_000,
    "s": _NS_PER_SECOND,
    "m": _NS_PER_MINUTE,
    "h": _NS_PER_HOUR,
    "D": _NS_PER_DAY,
    "W": 7 * _NS_PER_DAY,
}
_POLARS_UNIT_NS = {"ns": 1, "us": 1_000, "ms": 1_000_000}


def _components_for_resolution(resolution):
    return [
        "total_seconds",
        "days",
        *_REMAINDERS_BY_RESOLUTION[resolution],
        "log1p_total_seconds",
    ]


def _check_components(components):
    if components == "auto":
        return
    if isinstance(components, str):
        raise ValueError(
            "components must be 'auto' or a list/tuple of component names; "
            f"got {components!r}. Pass a list, for example ['days']."
        )
    if isinstance(components, (bytes, bytearray)) or not isinstance(
        components, (list, tuple)
    ):
        raise TypeError(
            "components must be 'auto' or a list/tuple of strings, "
            f"got {type(components).__name__}."
        )
    unknown = [
        component
        for component in components
        if not isinstance(component, str) or component not in _COMPONENT_SET
    ]
    if unknown:
        raise ValueError(
            f"Unknown duration component(s): {unknown}. "
            f"Valid components are: {list(_COMPONENT_ORDER)}."
        )
    if len(set(components)) != len(components):
        raise ValueError(f"Duplicate component names in {list(components)}.")
    if len(components) == 0:
        raise ValueError("components must contain at least one feature name.")


def _check_choice(name, value, allowed):
    if value not in allowed:
        raise ValueError(f"{name} must be one of {allowed}, got {value!r}.")


def _divmod_ns(ticks, tick_ns, period_ns):
    """Floor-divide ``ticks * tick_ns`` by ``period_ns``.

    Returns ``(quotient, remainder_ns)`` where ``remainder_ns`` is in
    ``[0, period_ns)``. Negative durations use the same normalization as
    ``datetime.timedelta``: the remainder is non-negative and the quotient
    rounds toward negative infinity.
    """
    ticks = np.asanyarray(ticks, dtype=np.int64)
    tick_ns = int(tick_ns)
    period_ns = int(period_ns)
    if tick_ns > 0 and period_ns % tick_ns == 0:
        ticks_per = period_ns // tick_ns
        if ticks_per <= np.iinfo(np.int64).max:
            divisor = np.int64(ticks_per)
            quotient = ticks // divisor
            remainder_ns = (ticks % divisor) * np.int64(tick_ns)
            return quotient, remainder_ns
    # Tick size does not divide the period (for example a whole number of
    # weeks). Python integers keep the floor division exact.
    totals = np.asanyarray(ticks, dtype=object) * tick_ns
    quotient = np.asarray(totals // period_ns, dtype=np.int64)
    remainder_ns = np.asarray(totals % period_ns, dtype=np.int64)
    return quotient, remainder_ns


def _extract_components(ticks, tick_ns):
    """Split integer duration ticks into numeric feature arrays."""
    days, remainder_ns = _divmod_ns(ticks, tick_ns, _NS_PER_DAY)
    hours = remainder_ns // np.int64(_NS_PER_HOUR)
    remainder_ns = remainder_ns % np.int64(_NS_PER_HOUR)
    minutes = remainder_ns // np.int64(_NS_PER_MINUTE)
    remainder_ns = remainder_ns % np.int64(_NS_PER_MINUTE)
    seconds = remainder_ns // np.int64(_NS_PER_SECOND)
    remainder_ns = remainder_ns % np.int64(_NS_PER_SECOND)
    microseconds = remainder_ns.astype(np.float64) / 1_000.0
    total_seconds = ticks.astype(np.float64) * (float(tick_ns) / _NS_PER_SECOND)
    with np.errstate(divide="ignore", invalid="ignore"):
        log1p_total_seconds = np.log1p(total_seconds)
    seconds_in_day = (hours.astype(np.float64) * 3600.0) + (
        minutes.astype(np.float64) * 60.0
    )
    seconds_in_day += seconds.astype(np.float64)
    seconds_in_day += microseconds / 1_000_000.0
    angle = (2.0 * np.pi) * (seconds_in_day / _SECONDS_PER_DAY)
    return {
        "total_seconds": total_seconds,
        "days": days.astype(np.float64),
        "hours": hours.astype(np.float64),
        "minutes": minutes.astype(np.float64),
        "seconds": seconds.astype(np.float64),
        "microseconds": microseconds,
        "log1p_total_seconds": log1p_total_seconds,
        "sin_of_day": np.sin(angle),
        "cos_of_day": np.cos(angle),
    }


def _infer_resolution(features, valid):
    if valid.size == 0 or not np.any(valid):
        return "minute"
    checks = (
        ("microsecond", "microseconds"),
        ("second", "seconds"),
        ("minute", "minutes"),
        ("hour", "hours"),
    )
    for resolution, name in checks:
        if np.any(features[name][valid] != 0):
            return resolution
    return "day"


def _apply_handle_negative(ticks, handle_negative):
    if handle_negative == "keep":
        return ticks
    out = np.array(ticks, dtype=np.int64, copy=True)
    if handle_negative == "clip":
        out[out < 0] = 0
        return out
    negative = out < 0
    minimum = out == np.iinfo(np.int64).min
    normal = negative & ~minimum
    out[normal] = -out[normal]
    out[minimum] = np.iinfo(np.int64).max
    return out


def _finite_sample(values, valid):
    sample = values[valid]
    return sample[np.isfinite(sample)]


def _fit_scaling_params(values, valid, scaling):
    sample = _finite_sample(values, valid)
    if scaling == "minmax":
        if sample.size == 0:
            return {"min": float("nan"), "max": float("nan")}
        return {"min": float(np.min(sample)), "max": float(np.max(sample))}
    if scaling == "standard":
        if sample.size == 0:
            return {"mean": float("nan"), "std": float("nan")}
        return {
            "mean": float(np.mean(sample)),
            "std": float(np.std(sample, ddof=0)),
        }
    if sample.size == 0:
        return {"median": float("nan"), "iqr": float("nan")}
    q25, q75 = np.percentile(sample, [25, 75])
    return {"median": float(np.median(sample)), "iqr": float(q75 - q25)}


def _apply_scaling(values, params, scaling):
    out = np.array(values, dtype=np.float64, copy=True)
    finite = np.isfinite(out)
    if scaling == "minmax":
        low = params["min"]
        high = params["max"]
        span = high - low
        if not np.isfinite(span) or span == 0:
            scaled = np.zeros_like(out)
        else:
            scaled = np.clip((out - low) / span, 0.0, 1.0)
    elif scaling == "standard":
        scale = params["std"]
        if not np.isfinite(scale) or scale == 0:
            scaled = np.zeros_like(out)
        else:
            scaled = (out - params["mean"]) / scale
    else:
        scale = params["iqr"]
        if not np.isfinite(scale) or scale == 0:
            scaled = np.zeros_like(out)
        else:
            scaled = (out - params["median"]) / scale
    scaled = np.where(finite, scaled, out)
    return scaled


def _null_mask(column):
    return np.asarray(sbd.to_numpy(sbd.is_null(column)), dtype=bool)


@dispatch
def _duration_ticks(col):
    from ._dispatch import raise_dispatch_unregistered_type

    raise_dispatch_unregistered_type(col, kind="Series")


@_duration_ticks.specialize("pandas", argument_type="Column")
def _duration_ticks_pandas(col):
    array = np.asarray(col.to_numpy())
    if not np.issubdtype(array.dtype, np.timedelta64):
        raise RejectColumn(
            f"Column {sbd.name(col)!r} does not contain durations; "
            f"got dtype '{sbd.dtype(col)}'."
        )
    unit, step = np.datetime_data(array.dtype)
    if unit not in _PANDAS_UNIT_NS:
        raise ValueError(f"Unsupported timedelta unit {unit!r}.")
    tick_ns = _PANDAS_UNIT_NS[unit] * int(step)
    ticks = array.view(np.int64).copy()
    null = _null_mask(col)
    ticks[null] = 0
    return ticks, tick_ns, null


@_duration_ticks.specialize("polars", argument_type="Column")
def _duration_ticks_polars(col):
    import polars as pl

    unit = col.dtype.time_unit
    if not isinstance(unit, str):
        unit = str(getattr(unit, "name", unit)).lower()
    if unit not in _POLARS_UNIT_NS:
        raise ValueError(f"Unsupported duration time unit {unit!r}.")
    physical = col.cast(pl.Int64).fill_null(0)
    ticks = np.array(physical.to_numpy(), dtype=np.int64, copy=True)
    null = _null_mask(col)
    ticks[null] = 0
    return ticks, _POLARS_UNIT_NS[unit], null


def _assemble_frame(column, components, features):
    base = sbd.name(column)
    names = [f"{base}_{component}" for component in components]
    built = []
    for name, component in zip(names, components):
        values = np.array(features[component], dtype=np.float64, copy=True)
        built.append(sbd.make_column_like(column, values, name))
    frame = sbd.copy_index(column, sbd.make_dataframe_like(column, built))
    if len(column) == 0:
        return frame
    not_nulls = ~sbd.is_null(column)
    null_fill = sbd.copy_index(column, sbd.all_null_like(built[0]))
    return sbd.where_row(frame, not_nulls, null_fill)


class DurationEncoder(SingleColumnTransformer):
    """Extract numeric features from a duration column.

    Parameters
    ----------
    components : "auto" or list/tuple of str, default="auto"
        Features to extract. ``"auto"`` chooses a preset from ``resolution``.
        Otherwise pass component names. An explicit list is used as given
        (including its order) and ``resolution`` is ignored.

        Valid names are ``"total_seconds"``, ``"days"``, ``"hours"`` (remainder
        after whole days, in ``[0, 24)``), ``"minutes"`` (remainder after whole
        hours), ``"seconds"`` (remainder after whole minutes),
        ``"microseconds"`` (fractional second, in ``[0, 1_000_000)``),
        ``"log1p_total_seconds"``, ``"sin_of_day"`` and ``"cos_of_day"``.
        The cyclical features are not part of any resolution preset.

    resolution : {"auto", "day", "hour", "minute", "second", "microsecond"}, \
            default="auto"
        Finest remainder kept when ``components="auto"``. ``"day"`` extracts
        ``total_seconds``, ``days`` and ``log1p_total_seconds``. Each finer
        level adds the corresponding remainder before ``log1p_total_seconds``:
        ``hours``, then ``minutes``, then ``seconds``, then ``microseconds``.
        ``"auto"`` uses the finest level that is not identically zero on the
        training column. If every value is null, the resolution is
        ``"minute"``.

    handle_negative : {"keep", "clip", "abs"}, default="keep"
        How to treat negative durations before extracting features.
        ``"clip"`` replaces them with a zero-length duration, ``"abs"`` takes
        the absolute value, and ``"keep"`` leaves them unchanged. With
        ``"keep"``, remainder components stay non-negative, matching
        ``datetime.timedelta`` (a negative total can still have a positive
        hour/minute/second remainder and a negative day count).

    scaling : {None, "minmax", "standard", "robust"}, default=None
        Optional scaling applied after extraction, fitted on non-null training
        values. ``"minmax"`` scales to ``[0, 1]`` and clips unseen values to
        that range. ``"standard"`` subtracts the training mean and divides by
        the population standard deviation. ``"robust"`` subtracts the training
        median and divides by the interquartile range (75th minus 25th
        percentile). If the training range, standard deviation, or
        interquartile range is zero, the scaled column is all zeros (nulls
        stay null).

    Attributes
    ----------
    resolution_ : str or None
        Resolution used to build ``components_`` when ``components="auto"``.
        ``None`` when ``components`` is an explicit list.

    components_ : list of str
        Component names, in output order.

    scaling_params_ : dict
        Per-component training statistics. Present only when ``scaling`` is
        not ``None``. Keys are ``"min"`` and ``"max"`` for ``"minmax"``,
        ``"mean"`` and ``"std"`` for ``"standard"``, and ``"median"`` and
        ``"iqr"`` for ``"robust"``.

    See Also
    --------
    DatetimeEncoder :
        Encode date and datetime columns.

    TableVectorizer :
        Route duration columns to a ``DurationEncoder``.

    Examples
    --------
    >>> from datetime import timedelta
    >>> import pandas as pd
    >>> from skrub import DurationEncoder
    >>> delay = pd.Series(
    ...     [timedelta(days=1, hours=3), None, timedelta(minutes=30)],
    ...     name="delay",
    ... )
    >>> encoder = DurationEncoder().fit(delay)
    >>> encoder.resolution_
    'minute'
    >>> encoder.components_
    ['total_seconds', 'days', 'hours', 'minutes', 'log1p_total_seconds']
    >>> DurationEncoder(components=["days", "hours"]).fit_transform(delay)
       delay_days  delay_hours
    0         1.0          3.0
    1         NaN          NaN
    2         0.0          0.0

    A column that is not a duration is rejected:

    >>> DurationEncoder().fit_transform(pd.Series([1.0], name="n"))
    Traceback (most recent call last):
        ...
    skrub._single_column_transformer.RejectColumn: Column 'n' does not contain durations; got dtype 'float64'.
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

    def _validate_params(self):
        _check_components(self.components)
        _check_choice("resolution", self.resolution, _RESOLUTIONS)
        _check_choice("handle_negative", self.handle_negative, _HANDLE_NEGATIVE)
        _check_choice("scaling", self.scaling, _SCALINGS)

    def _reject_unless_duration(self, column):
        if not sbd.is_duration(column):
            raise RejectColumn(
                f"Column {sbd.name(column)!r} does not contain durations; "
                f"got dtype '{sbd.dtype(column)}'."
            )

    def fit_transform(self, column, y=None):
        """Fit the encoder and transform a duration column.

        Parameters
        ----------
        column : pandas or polars Series
            A ``timedelta64`` (pandas) or ``Duration`` (polars) column.

        y : None
            Ignored.

        Returns
        -------
        dataframe
            One float64 column per extracted component. Null inputs are null
            in every output column.
        """
        del y
        self._validate_params()
        self._reject_unless_duration(column)
        return self._encode(column, fit=True)

    def transform(self, column):
        """Transform a duration column.

        Parameters
        ----------
        column : pandas or polars Series
            A ``timedelta64`` (pandas) or ``Duration`` (polars) column.

        Returns
        -------
        dataframe
            One float64 column per extracted component.
        """
        check_is_fitted(self, "components_")
        if self.scaling is not None:
            check_is_fitted(self, "scaling_params_")
        self._validate_params()
        self._reject_unless_duration(column)
        return self._encode(column, fit=False)

    def _encode(self, column, *, fit):
        ticks, tick_ns, null = _duration_ticks(column)
        ticks = _apply_handle_negative(ticks, self.handle_negative)
        features = _extract_components(ticks, tick_ns)
        valid = ~null
        if fit:
            if self.components == "auto":
                if self.resolution == "auto":
                    self.resolution_ = _infer_resolution(features, valid)
                else:
                    self.resolution_ = self.resolution
                self.components_ = _components_for_resolution(self.resolution_)
            else:
                self.resolution_ = None
                self.components_ = list(self.components)
            if self.scaling is None:
                self.__dict__.pop("scaling_params_", None)
            else:
                self.scaling_params_ = {
                    name: _fit_scaling_params(features[name], valid, self.scaling)
                    for name in self.components_
                }
        selected = {}
        for name in self.components_:
            values = np.array(features[name], dtype=np.float64, copy=True)
            if self.scaling is not None:
                values = _apply_scaling(
                    values, self.scaling_params_[name], self.scaling
                )
            values[null] = np.nan
            selected[name] = values
        frame = _assemble_frame(column, self.components_, selected)
        self.all_outputs_ = list(sbd.column_names(frame))
        return frame

    def _more_tags(self):
        return {"preserves_dtype": []}

    def __sklearn_tags__(self):
        tags = super().__sklearn_tags__()
        tags.transformer_tags = TransformerTags(preserves_dtype=[])
        return tags
