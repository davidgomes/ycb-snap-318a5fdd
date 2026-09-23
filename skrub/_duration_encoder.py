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

_RESOLUTIONS = ["day", "hour", "minute", "second", "microsecond"]

_REMAINDER_COMPONENTS = {
    "hour": "hours",
    "minute": "minutes",
    "second": "seconds",
    "microsecond": "microseconds",
}

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

_HANDLE_NEGATIVE_OPTIONS = ["keep", "clip", "abs"]
_SCALING_OPTIONS = [None, "minmax", "standard", "robust"]


def _components_for_resolution(resolution):
    idx = _RESOLUTIONS.index(resolution)
    remainders = [_REMAINDER_COMPONENTS[r] for r in _RESOLUTIONS[1 : idx + 1]]
    return ["total_seconds", "days", *remainders, "log1p_total_seconds"]


@dispatch
def _total_microseconds(col):
    # Avoid circular import
    from ._dispatch import raise_dispatch_unregistered_type

    raise_dispatch_unregistered_type(col, kind="Series")


@_total_microseconds.specialize("pandas", argument_type="Column")
def _total_microseconds_pandas(col):
    us = col // pd.Timedelta(microseconds=1)
    return us.to_numpy(dtype="float64", na_value=np.nan)


@_total_microseconds.specialize("polars", argument_type="Column")
def _total_microseconds_polars(col):
    import polars as pl

    return col.dt.total_microseconds().cast(pl.Float64).fill_null(np.nan).to_numpy()


@dispatch
def _make_output(col, features):
    # Avoid circular import
    from ._dispatch import raise_dispatch_unregistered_type

    raise_dispatch_unregistered_type(col, kind="Series")


@_make_output.specialize("pandas", argument_type="Column")
def _make_output_pandas(col, features):
    return pd.DataFrame(
        {name: values.astype("float32") for name, values in features.items()},
        index=col.index,
    )


@_make_output.specialize("polars", argument_type="Column")
def _make_output_polars(col, features):
    import polars as pl

    return pl.DataFrame(
        [
            pl.Series(name, values, dtype=pl.Float32, nan_to_null=True)
            for name, values in features.items()
        ]
    )


def _extract(us, component):
    """Compute one component from an array of (float) total microseconds.

    Remainder components use floor division, consistently with
    ``datetime.timedelta``: -1 hour is represented as -1 day + 23 hours.
    """
    if component == "total_seconds":
        return us / _US_PER_SECOND
    if component == "days":
        return np.floor_divide(us, _US_PER_DAY)
    if component == "hours":
        return np.floor_divide(np.mod(us, _US_PER_DAY), _US_PER_HOUR)
    if component == "minutes":
        return np.floor_divide(np.mod(us, _US_PER_HOUR), _US_PER_MINUTE)
    if component == "seconds":
        return np.floor_divide(np.mod(us, _US_PER_MINUTE), _US_PER_SECOND)
    if component == "microseconds":
        return np.mod(us, _US_PER_SECOND)
    if component == "log1p_total_seconds":
        seconds = us / _US_PER_SECOND
        return np.sign(seconds) * np.log1p(np.abs(seconds))
    angle = 2 * np.pi * np.mod(us, _US_PER_DAY) / _US_PER_DAY
    if component == "sin_of_day":
        return np.sin(angle)
    assert component == "cos_of_day", component
    return np.cos(angle)


def _detect_resolution(us):
    us = us[~np.isnan(us)]
    if us.size == 0:
        return "minute"
    for resolution, unit in [
        ("microsecond", _US_PER_SECOND),
        ("second", _US_PER_MINUTE),
        ("minute", _US_PER_HOUR),
        ("hour", _US_PER_DAY),
    ]:
        if np.any(np.mod(us, unit) != 0):
            return resolution
    return "day"


class DurationEncoder(SingleColumnTransformer):
    """
    Extract numeric features from a duration (timedelta) column.

    The ``DurationEncoder`` converts duration columns (pandas ``timedelta64``,
    polars ``Duration``) to numerical features that can be used by learners,
    such as the total number of seconds, the number of days, and the
    hours, minutes, … remaining after those days.

    Parameters
    ----------
    components : "auto" or list of str, default="auto"
        The features to extract. If ``"auto"``, the features are determined by
        ``resolution``. Otherwise, a list (or tuple) of component names, a subset
        of ``"total_seconds"``, ``"days"``, ``"hours"``, ``"minutes"``,
        ``"seconds"``, ``"microseconds"``, ``"log1p_total_seconds"``,
        ``"sin_of_day"`` and ``"cos_of_day"``. The features are extracted in the
        order given, and ``resolution`` is ignored.

        - ``"total_seconds"``: the duration in seconds.
        - ``"days"``: the number of whole days.
        - ``"hours"``: the number of whole hours after removing the days.
        - ``"minutes"``: the number of whole minutes after removing the hours.
        - ``"seconds"``: the number of whole seconds after removing the minutes.
        - ``"microseconds"``: the microseconds after removing the seconds.
        - ``"log1p_total_seconds"``: ``log(1 + total_seconds)``. For negative
          durations, ``-log(1 + |total_seconds|)``.
        - ``"sin_of_day"``, ``"cos_of_day"``: circular encoding of the time of
          day covered by the duration (the remainder after removing the days).

    resolution : "auto", "day", "hour", "minute", "second" or "microsecond", \
            default="auto"
        The finest granularity of the extracted remainder components, used when
        ``components="auto"``. ``"day"`` extracts ``["total_seconds", "days",
        "log1p_total_seconds"]``, ``"hour"`` adds ``"hours"`` before
        ``"log1p_total_seconds"``, ``"minute"`` adds ``"minutes"``, and so on.
        If ``"auto"``, the resolution is the finest level at which the data
        seen during ``fit`` carries information: for example, if all durations
        are whole days, the resolution is ``"day"``. If all values are null,
        the resolution defaults to ``"minute"``.

    handle_negative : "keep", "clip" or "abs", default="keep"
        How negative durations are treated before extracting the features.
        ``"keep"`` leaves them unchanged, ``"clip"`` replaces them with a
        duration of zero, and ``"abs"`` takes their absolute value.

    scaling : None, "minmax", "standard" or "robust", default=None
        Optional scaling applied to each extracted feature, using statistics
        computed during ``fit``.

        - ``None``: no scaling.
        - ``"minmax"``: scale to ``[0, 1]`` using the training minimum and
          maximum. Values outside of the training range are clipped.
        - ``"standard"``: subtract the training mean and divide by the
          training standard deviation.
        - ``"robust"``: subtract the training median and divide by the
          training interquartile range (75th - 25th percentile).

        When the training range, standard deviation or interquartile range is
        zero (e.g. constant feature), the scaled output is zero.

    Attributes
    ----------
    resolution_ : str or None
        The resolution used to choose the components. It is the detected
        resolution when ``resolution="auto"``, and ``None`` when ``components``
        is an explicit list.

    components_ : list of str
        The extracted components, in output order.

    scaling_params_ : dict
        Only set when ``scaling`` is not ``None``. Maps each component to a dict
        of the statistics used for scaling: ``{"min", "max"}`` for
        ``"minmax"``, ``{"mean", "std"}`` for ``"standard"``, and ``{"median",
        "iqr"}`` for ``"robust"``.

    all_outputs_ : list of str
        The names of the output columns, of the form
        ``"{column_name}_{component}"``.

    See Also
    --------
    DatetimeEncoder :
        Extract temporal features from datetime columns.

    Notes
    -----
    All extracted features are provided as float32 columns. Null values in the
    input are null in all output columns.

    Remainder components ("days", "hours", …) of negative durations follow the
    convention of Python's ``datetime.timedelta``: -1 hour is -1 day and 23
    hours.

    An input column that does not have a duration dtype is rejected by
    raising a ``RejectColumn`` exception. The ``TableVectorizer`` only sends
    duration columns to its ``duration`` encoder.

    Examples
    --------
    >>> import pandas as pd
    >>> from skrub import DurationEncoder
    >>> lag = pd.to_timedelta(
    ...     pd.Series(["1 days 02:30:00", None, "3 days 00:15:00"], name="lag")
    ... )
    >>> lag
    0   1 days 02:30:00
    1               NaT
    2   3 days 00:15:00
    Name: lag, dtype: timedelta64[...]
    >>> encoder = DurationEncoder()
    >>> encoder.fit_transform(lag)
       lag_total_seconds  lag_days  lag_hours  lag_minutes  lag_log1p_total_seconds
    0            95400.0       1.0        2.0         30.0                11.465844
    1                NaN       NaN        NaN          NaN                      NaN
    2           260100.0       3.0        0.0         15.0                12.468825
    >>> encoder.resolution_
    'minute'
    >>> encoder.components_
    ['total_seconds', 'days', 'hours', 'minutes', 'log1p_total_seconds']

    When all durations are whole days, only days-level features are extracted:

    >>> contract = pd.to_timedelta(pd.Series([30, 365], name="contract"), unit="D")
    >>> DurationEncoder().fit_transform(contract)
       contract_total_seconds  contract_days  contract_log1p_total_seconds
    0               2592000.0           30.0                     14.767941
    1              31536000.0          365.0                     17.266640

    The components can be chosen explicitly, in which case ``resolution`` is
    ignored:

    >>> DurationEncoder(components=["days", "sin_of_day"]).fit_transform(lag)
       lag_days  lag_sin_of_day
    0       1.0        0.608761
    1       NaN             NaN
    2       3.0        0.065403

    Features can be scaled with statistics learned during ``fit``:

    >>> encoder = DurationEncoder(components=["days"], scaling="minmax")
    >>> encoder.fit_transform(lag)
       lag_days
    0       0.0
    1       NaN
    2       1.0
    >>> encoder.scaling_params_
    {'days': {'min': 1.0, 'max': 3.0}}

    Non-duration columns are rejected:

    >>> DurationEncoder().fit_transform(pd.Series([1.5, 2.0], name="x"))
    Traceback (most recent call last):
        ...
    skrub._single_column_transformer.RejectColumn: Column 'x' does not have a Duration dtype.
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
                f"Column {sbd.name(column)!r} does not have a Duration dtype."
            )
        us = self._get_microseconds(column)
        if isinstance(self.components, str):
            if self.resolution == "auto":
                self.resolution_ = _detect_resolution(us)
            else:
                self.resolution_ = self.resolution
            self.components_ = _components_for_resolution(self.resolution_)
        else:
            self.resolution_ = None
            self.components_ = list(self.components)

        features = {c: _extract(us, c) for c in self.components_}
        if self.scaling is not None:
            self.scaling_params_ = {
                c: self._fit_scaling(values) for c, values in features.items()
            }
        col_name = sbd.name(column)
        self.all_outputs_ = [f"{col_name}_{c}" for c in self.components_]
        return self._make_output(column, features)

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
        us = self._get_microseconds(column)
        features = {c: _extract(us, c) for c in self.components_}
        return self._make_output(column, features)

    def _get_microseconds(self, column):
        us = _total_microseconds(column)
        if self.handle_negative == "clip":
            return np.maximum(us, 0.0)
        if self.handle_negative == "abs":
            return np.abs(us)
        return us

    def _make_output(self, column, features):
        if self.scaling is not None:
            features = {c: self._apply_scaling(c, v) for c, v in features.items()}
        features = dict(zip(self.all_outputs_, features.values()))
        return _make_output(column, features)

    def _fit_scaling(self, values):
        values = values[~np.isnan(values)]
        if values.size == 0:
            values = np.asarray([np.nan])
        if self.scaling == "minmax":
            return {"min": float(np.min(values)), "max": float(np.max(values))}
        if self.scaling == "standard":
            # The std of a constant feature is not always exactly 0 due to
            # rounding errors in the mean.
            is_constant = np.min(values) == np.max(values)
            return {
                "mean": float(np.mean(values)),
                "std": 0.0 if is_constant else float(np.std(values)),
            }
        assert self.scaling == "robust", self.scaling
        q25, q75 = np.percentile(values, [25, 75])
        return {"median": float(np.median(values)), "iqr": float(q75 - q25)}

    def _apply_scaling(self, component, values):
        params = self.scaling_params_[component]
        if self.scaling == "minmax":
            offset, scale = params["min"], params["max"] - params["min"]
        elif self.scaling == "standard":
            offset, scale = params["mean"], params["std"]
        else:
            offset, scale = params["median"], params["iqr"]
        if not scale > 0:
            return np.where(np.isnan(values), np.nan, 0.0)
        scaled = (values - offset) / scale
        if self.scaling == "minmax":
            scaled = np.clip(scaled, 0.0, 1.0)
        return scaled

    def _check_params(self):
        if isinstance(self.components, str):
            if self.components != "auto":
                raise ValueError(
                    "'components' must be 'auto' or a list of component names, "
                    f"got {self.components!r}."
                )
            if self.resolution not in ["auto", *_RESOLUTIONS]:
                raise ValueError(
                    f"'resolution' options are {['auto', *_RESOLUTIONS]}, "
                    f"got {self.resolution!r}."
                )
        elif isinstance(self.components, (list, tuple)):
            if not all(isinstance(c, str) for c in self.components):
                raise TypeError(
                    "'components' must be 'auto' or a list of strings, "
                    f"got {self.components!r}."
                )
            unknown = [c for c in self.components if c not in _ALL_COMPONENTS]
            if unknown:
                raise ValueError(
                    f"Unknown components {unknown}. "
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
                "'components' must be 'auto' or a list of strings, "
                f"got {self.components!r} of type {type(self.components).__name__}."
            )
        if self.handle_negative not in _HANDLE_NEGATIVE_OPTIONS:
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
