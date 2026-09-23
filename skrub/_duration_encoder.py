import numpy as np
from sklearn.utils.validation import check_is_fitted

from . import _dataframe as sbd
from ._single_column_transformer import RejectColumn, SingleColumnTransformer
from ._sklearn_compat import TransformerTags

__all__ = ["DurationEncoder"]

_US_PER_SECOND = 1_000_000
_US_PER_MINUTE = 60 * _US_PER_SECOND
_US_PER_HOUR = 60 * _US_PER_MINUTE
_US_PER_DAY = 24 * _US_PER_HOUR

# Also the output order of the extracted features.
_COMPONENTS = [
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
_HANDLE_NEGATIVE = ["keep", "clip", "abs"]
_SCALINGS = [None, "minmax", "standard", "robust"]


def _components_for_resolution(resolution):
    n_remainders = _RESOLUTIONS.index(resolution)
    remainders = _COMPONENTS[2 : 2 + n_remainders]
    return ["total_seconds", "days", *remainders, "log1p_total_seconds"]


def _detect_resolution(abs_microseconds):
    if abs_microseconds.size == 0:
        return "minute"
    for resolution, unit in [
        ("microsecond", _US_PER_SECOND),
        ("second", _US_PER_MINUTE),
        ("minute", _US_PER_HOUR),
        ("hour", _US_PER_DAY),
    ]:
        if np.any(abs_microseconds % unit):
            return resolution
    return "day"


def _to_abs_microseconds(values):
    # Casting to a coarser unit floors, so cast absolute values to truncate
    # towards zero.
    return np.abs(values).astype("timedelta64[us]").astype(np.int64)


def _extract(values, components):
    negative = values < np.timedelta64(0, "s")

    def signed(x):
        return np.where(negative, -x, x).astype(np.float64)

    total_seconds = values / np.timedelta64(1, "s")
    days, time_of_day = np.divmod(_to_abs_microseconds(values), _US_PER_DAY)
    hours, rest = np.divmod(time_of_day, _US_PER_HOUR)
    minutes, rest = np.divmod(rest, _US_PER_MINUTE)
    seconds, microseconds = np.divmod(rest, _US_PER_SECOND)
    angle = 2 * np.pi * signed(time_of_day) / _US_PER_DAY
    features = {
        "total_seconds": total_seconds,
        "days": signed(days),
        "hours": signed(hours),
        "minutes": signed(minutes),
        "seconds": signed(seconds),
        "microseconds": signed(microseconds),
        "log1p_total_seconds": np.sign(total_seconds) * np.log1p(np.abs(total_seconds)),
        "sin_of_day": np.sin(angle),
        "cos_of_day": np.cos(angle),
    }
    return {c: features[c] for c in components}


def _fit_scaling(x, scaling):
    if x.size == 0:
        x = np.full(1, np.nan)
    if scaling == "minmax":
        return {"min": float(np.min(x)), "max": float(np.max(x))}
    if scaling == "standard":
        # np.std can be slightly above 0 for a constant input due to rounding
        std = 0.0 if np.min(x) == np.max(x) else float(np.std(x))
        return {"mean": float(np.mean(x)), "std": std}
    q25, median, q75 = np.percentile(x, [25, 50, 75])
    return {"median": float(median), "iqr": float(q75 - q25)}


def _apply_scaling(x, scaling, params):
    if scaling == "minmax":
        offset, scale = params["min"], params["max"] - params["min"]
    elif scaling == "standard":
        offset, scale = params["mean"], params["std"]
    else:
        offset, scale = params["median"], params["iqr"]
    if scale == 0:
        return np.zeros_like(x)
    scaled = (x - offset) / scale
    if scaling == "minmax":
        scaled = np.clip(scaled, 0.0, 1.0)
    return scaled


class DurationEncoder(SingleColumnTransformer):
    """
    Extract numeric features such as total seconds, days, hours, … from a duration column.

    The ``DurationEncoder`` converts durations (``timedelta64`` columns in
    pandas, ``Duration`` columns in polars), such as "time since last login"
    or "contract length", to numerical features that can be used by learners.
    It extracts the total duration in seconds, the number of days and the
    remaining hours, minutes, seconds, … as well as a log-transformed total
    duration. Cyclical features for the time of day may also be included, and
    the features can optionally be scaled.

    Parameters
    ----------
    components : "auto" or list of str, default="auto"
        The features to extract. If ``"auto"``, they are determined by
        ``resolution`` (see below). Otherwise, it must be a list or tuple
        containing some of the following names:

        - ``"total_seconds"``: the duration expressed in seconds.
        - ``"days"``: the number of whole days.
        - ``"hours"``: whole hours remaining after removing the days (0 to 23).
        - ``"minutes"``: whole minutes remaining after removing the hours (0 to
          59).
        - ``"seconds"``: whole seconds remaining after removing the minutes (0
          to 59).
        - ``"microseconds"``: microseconds remaining after removing the seconds
          (0 to 999999).
        - ``"log1p_total_seconds"``: ``log(1 + total_seconds)``, which
          compresses the range of long durations.
        - ``"sin_of_day"`` and ``"cos_of_day"``: sine and cosine of the time of
          day (the part of the duration left after removing whole days), with a
          period of 24 hours.

        Whatever the order in which they are given, the features are always
        output in the order listed above. When ``components`` is a list,
        ``resolution`` is ignored.

    resolution : str, default="auto"
        The finest granularity of the remainder components extracted when
        ``components="auto"``. Must be ``"auto"``, ``"day"``, ``"hour"``,
        ``"minute"``, ``"second"`` or ``"microsecond"``. The extracted features
        are ``"total_seconds"``, then ``"days"``, then the remainder components
        down to ``resolution``, then ``"log1p_total_seconds"``. For example,
        ``resolution="day"`` extracts ``["total_seconds", "days",
        "log1p_total_seconds"]`` and ``resolution="minute"`` extracts
        ``["total_seconds", "days", "hours", "minutes",
        "log1p_total_seconds"]``. If ``"auto"``, the resolution is inferred
        during ``fit`` as the finest level that carries information: for
        example if all durations are whole days, the resolution is ``"day"``.
        If the column contains only nulls, the resolution is ``"minute"``. The
        cyclical components ``"sin_of_day"`` and ``"cos_of_day"`` are only
        extracted when explicitly requested in ``components``.

    handle_negative : "keep", "clip" or "abs", default="keep"
        How negative durations are treated before extracting features:
        ``"keep"`` leaves them unchanged, ``"clip"`` replaces them with a
        zero-length duration and ``"abs"`` takes their absolute value.

    scaling : None, "minmax", "standard" or "robust", default=None
        Scaling applied to each extracted feature, using statistics computed
        on the (non-null) values seen during ``fit``:

        - ``None``: no scaling.
        - ``"minmax"``: scale to [0, 1] using the minimum and maximum; values
          outside of the training range are clipped.
        - ``"standard"``: subtract the mean and divide by the standard
          deviation.
        - ``"robust"``: subtract the median and divide by the interquartile
          range (75th percentile - 25th percentile).

        If the range, standard deviation or interquartile range is zero (e.g.
        for a constant column), the scaled feature is 0 everywhere.

    Attributes
    ----------
    components_ : list of str
        The extracted components, in output order.

    resolution_ : str or None
        The resolution that determined the components: ``resolution``, or the
        resolution inferred from the data if ``resolution="auto"``. ``None``
        when ``components`` is an explicit list.

    scaling_params_ : dict
        Only set when ``scaling`` is not ``None``. Maps each component to a
        dict of the statistics used to scale it: ``"min"`` and ``"max"`` for
        ``"minmax"``, ``"mean"`` and ``"std"`` for ``"standard"``, and
        ``"median"`` and ``"iqr"`` for ``"robust"``.

    all_outputs_ : list of str
        The names of the output columns, of the form
        ``"{column_name}_{component}"``.

    See Also
    --------
    DatetimeEncoder :
        Extract temporal features from a datetime column.

    TableVectorizer :
        Uses a ``DurationEncoder`` for duration columns by default.

    Notes
    -----
    All extracted features are provided as float32 columns. Null values in
    the input result in null values in all the output columns.

    When negative durations are kept, the day and remainder components carry
    the sign of the duration: for example, minus 1 day and 2 hours gives
    ``days=-1`` and ``hours=-2``. ``"log1p_total_seconds"`` is then
    ``-log(1 + |total_seconds|)``, so that it is defined for all durations.

    An input column that does not have a duration dtype is rejected by raising
    a ``RejectColumn`` exception. **Note:** the ``TableVectorizer`` only sends
    duration columns to its ``duration`` transformer.

    Examples
    --------
    >>> import pandas as pd
    >>> from skrub import DurationEncoder
    >>> since_login = pd.Series(
    ...     pd.to_timedelta(["1 days 02:30:00", None, "3 days 00:15:00"]),
    ...     name="since_login",
    ... )
    >>> since_login
    0   1 days 02:30:00
    1               NaT
    2   3 days 00:15:00
    Name: since_login, dtype: timedelta64[...]

    By default, the resolution is inferred from the data. Here, the durations
    contain hours and minutes but no seconds:

    >>> encoder = DurationEncoder()
    >>> encoder.fit_transform(since_login)
       since_login_total_seconds  ...  since_login_log1p_total_seconds
    0                    95400.0  ...                        11.465844
    1                        NaN  ...                              NaN
    2                   260100.0  ...                        12.468825
    >>> encoder.resolution_
    'minute'
    >>> encoder.components_
    ['total_seconds', 'days', 'hours', 'minutes', 'log1p_total_seconds']
    >>> encoder.get_feature_names_out()
    ['since_login_total_seconds', 'since_login_days', 'since_login_hours', \
'since_login_minutes', 'since_login_log1p_total_seconds']

    We can choose the resolution explicitly:

    >>> DurationEncoder(resolution="day").fit_transform(since_login)
       since_login_total_seconds  since_login_days  since_login_log1p_total_seconds
    0                    95400.0               1.0                        11.465844
    1                        NaN               NaN                              NaN
    2                   260100.0               3.0                        12.468825

    Or list the components to extract, including the cyclical encoding of the
    time of day, which is never extracted by default:

    >>> DurationEncoder(
    ...     components=["days", "sin_of_day", "cos_of_day"]
    ... ).fit_transform(since_login)
       since_login_days  since_login_sin_of_day  since_login_cos_of_day
    0               1.0                0.608761                0.793353
    1               NaN                     NaN                     NaN
    2               3.0                0.065403                0.997859

    Negative durations can be kept, clipped to 0 or replaced by their absolute
    value:

    >>> overdue = pd.Series(pd.to_timedelta(["2 days", "-1 days"]), name="overdue")
    >>> DurationEncoder(components=["days"]).fit_transform(overdue)
       overdue_days
    0           2.0
    1          -1.0
    >>> DurationEncoder(components=["days"], handle_negative="clip").fit_transform(
    ...     overdue
    ... )
       overdue_days
    0           2.0
    1           0.0
    >>> DurationEncoder(components=["days"], handle_negative="abs").fit_transform(
    ...     overdue
    ... )
       overdue_days
    0           2.0
    1           1.0

    The extracted features can be scaled with statistics learned during
    ``fit``:

    >>> encoder = DurationEncoder(components=["days"], scaling="minmax")
    >>> encoder.fit_transform(since_login)
       since_login_days
    0               0.0
    1               NaN
    2               1.0
    >>> encoder.scaling_params_
    {'days': {'min': 1.0, 'max': 3.0}}
    >>> encoder.transform(pd.Series(pd.to_timedelta(["2 days", "10 days"])))
       None_days
    0        0.5
    1        1.0

    Non-duration columns are rejected by raising a ``RejectColumn`` exception:

    >>> DurationEncoder().fit_transform(pd.Series([1.5, 2.0], name="length"))
    Traceback (most recent call last):
        ...
    skrub._single_column_transformer.RejectColumn: Column 'length' does not have Duration dtype.
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
        values, null = self._prepare(column)
        if isinstance(self.components, str):
            if self.resolution == "auto":
                self.resolution_ = _detect_resolution(
                    _to_abs_microseconds(values[~null])
                )
            else:
                self.resolution_ = self.resolution
            self.components_ = _components_for_resolution(self.resolution_)
        else:
            self.resolution_ = None
            self.components_ = [c for c in _COMPONENTS if c in self.components]
        name = sbd.name(column)
        self.all_outputs_ = [f"{name}_{c}" for c in self.components_]
        features = _extract(values, self.components_)
        if self.scaling is None:
            self.__dict__.pop("scaling_params_", None)
        else:
            self.scaling_params_ = {
                c: _fit_scaling(x[~null], self.scaling) for c, x in features.items()
            }
        return self._make_output(column, features, null)

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
        check_is_fitted(self, "components_")
        values, null = self._prepare(column)
        return self._make_output(column, _extract(values, self.components_), null)

    def _prepare(self, column):
        values = sbd.to_numpy(column).copy()
        null = np.isnat(values)
        values[null] = 0
        if self.handle_negative == "clip":
            values = np.maximum(values, np.timedelta64(0, "s"))
        elif self.handle_negative == "abs":
            values = np.abs(values)
        return values, null

    def _make_output(self, column, features, null):
        name = sbd.name(column)
        output_cols = {}
        for component, values in features.items():
            if self.scaling is not None:
                values = _apply_scaling(
                    values, self.scaling, self.scaling_params_[component]
                )
            output_cols[f"{name}_{component}"] = sbd.make_column_like(
                column, values.astype(np.float32), f"{name}_{component}"
            )
        output = sbd.copy_index(column, sbd.make_dataframe_like(column, output_cols))
        if null.any():
            first_col = next(iter(output_cols.values()))
            null_values = sbd.copy_index(column, sbd.all_null_like(first_col))
            output = sbd.where_row(output, ~sbd.is_null(column), null_values)
        return output

    def _check_params(self):
        if isinstance(self.components, str):
            if self.components != "auto":
                raise ValueError(
                    "'components' must be 'auto' or a list of component names, "
                    f"got {self.components!r}."
                )
        elif not isinstance(self.components, (list, tuple)):
            raise TypeError(
                "'components' must be 'auto' or a list or tuple of strings, got "
                f"an object of type {type(self.components).__name__}."
            )
        else:
            if not all(isinstance(c, str) for c in self.components):
                raise TypeError(
                    "'components' must be 'auto' or a list or tuple of strings, "
                    f"got {self.components!r}."
                )
            unknown = [c for c in self.components if c not in _COMPONENTS]
            if unknown:
                raise ValueError(
                    f"Unknown components {unknown}. Valid components are {_COMPONENTS}."
                )
            if not self.components:
                raise ValueError("'components' must not be empty.")
        allowed = ["auto"] + _RESOLUTIONS
        if self.resolution not in allowed:
            raise ValueError(
                f"'resolution' options are {allowed}, got {self.resolution!r}."
            )
        if self.handle_negative not in _HANDLE_NEGATIVE:
            raise ValueError(
                f"'handle_negative' options are {_HANDLE_NEGATIVE}, "
                f"got {self.handle_negative!r}."
            )
        if self.scaling not in _SCALINGS:
            raise ValueError(
                f"'scaling' options are {_SCALINGS}, got {self.scaling!r}."
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
            Transformed feature names, of the form
            ``"{column_name}_{component}"``.
        """
        check_is_fitted(self, "all_outputs_")
        return self.all_outputs_
