import numpy as np
import pandas as pd
from sklearn.utils.validation import check_is_fitted

from . import _dataframe as sbd
from ._dispatch import dispatch
from ._single_column_transformer import RejectColumn, SingleColumnTransformer
from ._sklearn_compat import TransformerTags

__all__ = ["DurationEncoder"]

_US_PER_SECOND = 1_000_000
_US_PER_MINUTE = 60 * _US_PER_SECOND
_US_PER_HOUR = 60 * _US_PER_MINUTE
_US_PER_DAY = 24 * _US_PER_HOUR

_ALL_COMPONENTS = [
    "total_seconds",
    "days",
    "hours",
    "minutes",
    "seconds",
    "microseconds",
    "log1p_total_seconds",
    "sin_of_day",
    "cos_of_day",
]

_RESOLUTIONS = ["day", "hour", "minute", "second", "microsecond"]

_REMAINDER_COMPONENTS = {
    "day": [],
    "hour": ["hours"],
    "minute": ["hours", "minutes"],
    "second": ["hours", "minutes", "seconds"],
    "microsecond": ["hours", "minutes", "seconds", "microseconds"],
}

_HANDLE_NEGATIVE_OPTIONS = ["keep", "clip", "abs"]
_SCALING_OPTIONS = [None, "minmax", "standard", "robust"]


def _components_for_resolution(resolution):
    return (
        ["total_seconds", "days"]
        + _REMAINDER_COMPONENTS[resolution]
        + ["log1p_total_seconds"]
    )


@dispatch
def _total_microseconds(col):
    # Avoid circular import
    from ._dispatch import raise_dispatch_unregistered_type

    raise_dispatch_unregistered_type(col, kind="Series")


@_total_microseconds.specialize("pandas", argument_type="Column")
def _total_microseconds_pandas(col):
    values = (col / pd.Timedelta(1, "us")).to_numpy(dtype="float64", na_value=np.nan)
    return np.round(values)


@_total_microseconds.specialize("polars", argument_type="Column")
def _total_microseconds_polars(col):
    import polars as pl

    return col.dt.total_microseconds().cast(pl.Float64).to_numpy()


@dispatch
def _make_output(col, data):
    # Avoid circular import
    from ._dispatch import raise_dispatch_unregistered_type

    raise_dispatch_unregistered_type(col, kind="Series")


@_make_output.specialize("pandas", argument_type="Column")
def _make_output_pandas(col, data):
    return pd.DataFrame(data, index=col.index)


@_make_output.specialize("polars", argument_type="Column")
def _make_output_polars(col, data):
    import polars as pl

    return pl.DataFrame(
        [pl.Series(name, values).fill_nan(None) for name, values in data.items()]
    )


def _extract(total_us, component):
    """Compute one component from total microseconds (NaN for nulls).

    Remainder components follow the same convention as Python's
    ``datetime.timedelta`` and pandas: ``days`` is floored and the remainder
    within the day is always non-negative.
    """
    if component == "total_seconds":
        return total_us / _US_PER_SECOND
    if component == "log1p_total_seconds":
        seconds = total_us / _US_PER_SECOND
        return np.sign(seconds) * np.log1p(np.abs(seconds))
    if component == "days":
        return np.floor_divide(total_us, _US_PER_DAY)
    in_day = np.mod(total_us, _US_PER_DAY)
    if component == "hours":
        return np.floor_divide(in_day, _US_PER_HOUR)
    if component == "minutes":
        return np.floor_divide(np.mod(in_day, _US_PER_HOUR), _US_PER_MINUTE)
    if component == "seconds":
        return np.floor_divide(np.mod(in_day, _US_PER_MINUTE), _US_PER_SECOND)
    if component == "microseconds":
        return np.mod(in_day, _US_PER_SECOND)
    angle = 2 * np.pi * in_day / _US_PER_DAY
    if component == "sin_of_day":
        return np.sin(angle)
    assert component == "cos_of_day"
    return np.cos(angle)


def _detect_resolution(total_us):
    values = total_us[~np.isnan(total_us)]
    if not len(values):
        return "minute"
    if np.any(np.mod(values, _US_PER_SECOND) != 0):
        return "microsecond"
    if np.any(np.mod(values, _US_PER_MINUTE) != 0):
        return "second"
    if np.any(np.mod(values, _US_PER_HOUR) != 0):
        return "minute"
    if np.any(np.mod(values, _US_PER_DAY) != 0):
        return "hour"
    return "day"


def _fit_scaling(values, scaling):
    values = values[~np.isnan(values)]
    if not len(values):
        values = np.asarray([0.0])
    if scaling == "minmax":
        return {"min": float(np.min(values)), "max": float(np.max(values))}
    if scaling == "standard":
        return {"mean": float(np.mean(values)), "std": float(np.std(values))}
    assert scaling == "robust"
    q25, q50, q75 = np.percentile(values, [25, 50, 75])
    return {"median": float(q50), "iqr": float(q75 - q25)}


def _apply_scaling(values, scaling, params):
    if scaling == "minmax":
        offset, scale = params["min"], params["max"] - params["min"]
    elif scaling == "standard":
        offset, scale = params["mean"], params["std"]
    else:
        assert scaling == "robust"
        offset, scale = params["median"], params["iqr"]
    if scale == 0:
        return np.where(np.isnan(values), np.nan, 0.0)
    scaled = (values - offset) / scale
    if scaling == "minmax":
        scaled = np.clip(scaled, 0.0, 1.0)
    return scaled


class DurationEncoder(SingleColumnTransformer):
    """
    Extract numeric features such as number of days, hours, … from a duration column.

    The ``DurationEncoder`` converts duration columns (``timedelta64`` in pandas,
    ``Duration`` in polars) to numerical features that can be used by learners:
    the total length of the duration in seconds, its number of whole days, the
    remaining hours, minutes, … and a log-transformed total.

    Parameters
    ----------
    components : "auto" or list of str, default="auto"
        The features to extract. If ``"auto"``, the features are determined by
        ``resolution``. Otherwise, a list or tuple containing some of
        ``"total_seconds"``, ``"days"``, ``"hours"`` (remainder after days),
        ``"minutes"`` (remainder after hours), ``"seconds"`` (remainder after
        minutes), ``"microseconds"`` (remainder after seconds),
        ``"log1p_total_seconds"``, ``"sin_of_day"`` and ``"cos_of_day"``. When
        an explicit list is given, ``resolution`` is ignored.

    resolution : str, default="auto"
        The finest granularity of the remainder components, used when
        ``components="auto"``. One of ``"day"``, ``"hour"``, ``"minute"``,
        ``"second"``, ``"microsecond"`` or ``"auto"``. The extracted components
        are ``"total_seconds"``, ``"days"``, the remainder components down to
        the chosen resolution (``"hours"``, ``"minutes"``, …), and
        ``"log1p_total_seconds"``. For example ``resolution="hour"`` extracts
        ``["total_seconds", "days", "hours", "log1p_total_seconds"]``. If
        ``"auto"``, the finest level that carries information in the data seen
        during ``fit`` is used (for example ``"day"`` if all durations are whole
        days). If all values are null, ``"minute"`` is used. The cyclical
        components ``"sin_of_day"`` and ``"cos_of_day"`` are never extracted
        automatically.

    handle_negative : "keep", "clip" or "abs", default="keep"
        How negative durations are treated before extracting features.
        ``"keep"`` leaves them unchanged, ``"clip"`` replaces them with a
        zero-length duration and ``"abs"`` takes their absolute value.

    scaling : None, "minmax", "standard" or "robust", default=None
        Optional scaling applied to each extracted feature, using statistics
        computed during ``fit``. ``"minmax"`` scales to ``[0, 1]`` (values
        outside the training range are clipped), ``"standard"`` subtracts the
        mean and divides by the standard deviation, ``"robust"`` subtracts the
        median and divides by the interquartile range. When the range, standard
        deviation or interquartile range is zero, the output is all zeros.

    Attributes
    ----------
    resolution_ : str or None
        The resolution that was used. ``None`` when ``components`` is an
        explicit list.

    components_ : list of str
        The extracted components.

    scaling_params_ : dict
        Only present when ``scaling`` is not ``None``. Maps each component to
        a dict of the statistics used to scale it (``"min"`` and ``"max"``,
        ``"mean"`` and ``"std"``, or ``"median"`` and ``"iqr"``).

    all_outputs_ : list of str
        The names of the output columns, of the form
        ``"{column_name}_{component}"``.

    See Also
    --------
    DatetimeEncoder :
        Extract features from datetime columns.

    Notes
    -----
    All extracted features are provided as float32 columns. Null values in the
    input are null in all output columns.

    ``days`` and the remainder components follow the convention of Python's
    :class:`datetime.timedelta` and pandas: ``days`` is rounded towards minus
    infinity and the remainders are always non-negative (so ``-1 hour`` is
    ``-1`` day and ``23`` hours). ``log1p_total_seconds`` is
    ``sign(x) * log1p(|x|)`` where ``x`` is the total number of seconds, so it
    is defined for negative durations.

    An input column that does not have a Duration dtype is rejected by raising
    a ``RejectColumn`` exception.

    Examples
    --------
    >>> import pandas as pd
    >>> from skrub import DurationEncoder
    >>> delay = pd.Series(
    ...     pd.to_timedelta(["1 days 02:00:00", None, "3 days 05:00:00"]),
    ...     name="delay",
    ... )
    >>> delay
    0   1 days 02:00:00
    1               NaT
    2   3 days 05:00:00
    Name: delay, dtype: timedelta64[...]
    >>> encoder = DurationEncoder()
    >>> encoder.fit_transform(delay)
       delay_total_seconds  delay_days  delay_hours  delay_log1p_total_seconds
    0              93600.0         1.0          2.0                  11.446796
    1                  NaN         NaN          NaN                        NaN
    2             277200.0         3.0          5.0                  12.532498
    >>> encoder.resolution_
    'hour'
    >>> encoder.components_
    ['total_seconds', 'days', 'hours', 'log1p_total_seconds']

    Components can be chosen explicitly:

    >>> DurationEncoder(components=["days", "sin_of_day"]).fit_transform(delay)
       delay_days  delay_sin_of_day
    0         1.0          0.500000
    1         NaN               NaN
    2         3.0          0.965926

    Non-duration columns are rejected:

    >>> DurationEncoder().fit_transform(pd.Series([1, 2], name="n"))
    Traceback (most recent call last):
        ...
    skrub._single_column_transformer.RejectColumn: Column 'n' does not have Duration dtype.
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
        column : pandas or polars Series with dtype Duration
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
                f"Column {sbd.name(column)!r} does not have Duration dtype."
            )
        total_us = self._get_total_microseconds(column)
        if isinstance(self.components, str):
            if self.resolution == "auto":
                self.resolution_ = _detect_resolution(total_us)
            else:
                self.resolution_ = self.resolution
            self.components_ = _components_for_resolution(self.resolution_)
        else:
            self.resolution_ = None
            self.components_ = list(self.components)
        col_name = sbd.name(column)
        self.all_outputs_ = [f"{col_name}_{c}" for c in self.components_]
        if self.scaling is not None:
            self.scaling_params_ = {
                c: _fit_scaling(_extract(total_us, c), self.scaling)
                for c in self.components_
            }
        return self._transform(column, total_us)

    def transform(self, column):
        """Transform a column.

        Parameters
        ----------
        column : pandas or polars Series with dtype Duration
            The input to transform.

        Returns
        -------
        transformed : DataFrame
            The extracted features.
        """
        check_is_fitted(self, "all_outputs_")
        return self._transform(column, self._get_total_microseconds(column))

    def _get_total_microseconds(self, column):
        total_us = _total_microseconds(column)
        if self.handle_negative == "clip":
            total_us = np.where(total_us < 0, 0.0, total_us)
        elif self.handle_negative == "abs":
            total_us = np.abs(total_us)
        return total_us

    def _transform(self, column, total_us):
        data = {}
        for component, out_name in zip(self.components_, self.all_outputs_):
            values = _extract(total_us, component)
            if self.scaling is not None:
                values = _apply_scaling(
                    values, self.scaling, self.scaling_params_[component]
                )
            data[out_name] = values.astype("float32")
        return _make_output(column, data)

    def _check_params(self):
        if isinstance(self.components, str):
            if self.components != "auto":
                raise ValueError(
                    "'components' must be 'auto' or a list of component names, "
                    f"got {self.components!r}."
                )
        elif isinstance(self.components, (list, tuple)):
            unknown = [c for c in self.components if c not in _ALL_COMPONENTS]
            if unknown:
                raise ValueError(
                    f"Unknown components {unknown!r}. "
                    f"Valid components are {_ALL_COMPONENTS}."
                )
            if not self.components:
                raise ValueError("'components' must not be empty.")
            if len(set(self.components)) != len(self.components):
                raise ValueError(
                    f"'components' contains duplicates: {self.components!r}."
                )
        else:
            raise TypeError(
                "'components' must be 'auto' or a list or tuple of strings, "
                f"got an object of type {type(self.components).__name__}."
            )
        allowed_resolutions = ["auto"] + _RESOLUTIONS
        if (
            not isinstance(self.resolution, str)
            or self.resolution not in allowed_resolutions
        ):
            raise ValueError(
                f"'resolution' options are {allowed_resolutions}, "
                f"got {self.resolution!r}."
            )
        if (
            not isinstance(self.handle_negative, str)
            or self.handle_negative not in _HANDLE_NEGATIVE_OPTIONS
        ):
            raise ValueError(
                f"'handle_negative' options are {_HANDLE_NEGATIVE_OPTIONS}, "
                f"got {self.handle_negative!r}."
            )
        if self.scaling not in _SCALING_OPTIONS:
            raise ValueError(
                f"'scaling' options are {_SCALING_OPTIONS}, got {self.scaling!r}."
            )

    def _more_tags(self):
        return {"preserves_dtype": []}

    def __sklearn_tags__(self):
        tags = super().__sklearn_tags__()
        tags.transformer_tags = TransformerTags(preserves_dtype=[])
        return tags

    def get_feature_names_out(self, input_features=None):
        """Get output feature names for transformation.

        Parameters
        ----------
        input_features : array-like of str or None, default=None
            Ignored.

        Returns
        -------
        feature_names_out : list of str
            Transformed feature names.
        """
        check_is_fitted(self, "all_outputs_")
        return self.all_outputs_
