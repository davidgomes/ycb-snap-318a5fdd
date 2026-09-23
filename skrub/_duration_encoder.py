import numpy as np
import pandas as pd
from sklearn.utils.validation import check_is_fitted

from . import _dataframe as sbd
from ._dispatch import dispatch
from ._single_column_transformer import RejectColumn, SingleColumnTransformer

__all__ = ["DurationEncoder"]

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
_REMAINDERS = ["hours", "minutes", "seconds", "microseconds"]

_US_PER_SECOND = 1_000_000
_US_PER_MINUTE = 60 * _US_PER_SECOND
_US_PER_HOUR = 60 * _US_PER_MINUTE
_US_PER_DAY = 24 * _US_PER_HOUR


def _components_for_resolution(resolution):
    n_remainders = _RESOLUTIONS.index(resolution)
    return ["total_seconds", "days", *_REMAINDERS[:n_remainders], "log1p_total_seconds"]


@dispatch
def _to_microseconds(col):
    raise NotImplementedError()


@_to_microseconds.specialize("pandas", argument_type="Column")
def _to_microseconds_pandas(col):
    values = col.to_numpy().astype("timedelta64[us]").astype("float64")
    values[col.isna().to_numpy()] = np.nan
    return values


@_to_microseconds.specialize("polars", argument_type="Column")
def _to_microseconds_polars(col):
    import polars as pl

    return col.dt.total_microseconds().cast(pl.Float64).to_numpy()


@dispatch
def _make_output_column(col, values, name):
    raise NotImplementedError()


@_make_output_column.specialize("pandas", argument_type="Column")
def _make_output_column_pandas(col, values, name):
    return pd.Series(values.astype("float32"), name=name, index=col.index)


@_make_output_column.specialize("polars", argument_type="Column")
def _make_output_column_polars(col, values, name):
    import polars as pl

    return pl.Series(name, values.astype("float32")).fill_nan(None)


def _detect_resolution(us):
    us = us[~np.isnan(us)]
    if not us.size:
        return "minute"
    us = np.abs(us)
    for resolution, unit in [
        ("microsecond", _US_PER_SECOND),
        ("second", _US_PER_MINUTE),
        ("minute", _US_PER_HOUR),
        ("hour", _US_PER_DAY),
    ]:
        if np.any(np.mod(us, unit) != 0):
            return resolution
    return "day"


def _extract(us, component):
    if component == "total_seconds":
        return us / _US_PER_SECOND
    if component == "days":
        return np.floor(us / _US_PER_DAY)
    if component == "hours":
        return np.floor(np.mod(us, _US_PER_DAY) / _US_PER_HOUR)
    if component == "minutes":
        return np.floor(np.mod(us, _US_PER_HOUR) / _US_PER_MINUTE)
    if component == "seconds":
        return np.floor(np.mod(us, _US_PER_MINUTE) / _US_PER_SECOND)
    if component == "microseconds":
        return np.mod(us, _US_PER_SECOND)
    if component == "log1p_total_seconds":
        seconds = us / _US_PER_SECOND
        return np.sign(seconds) * np.log1p(np.abs(seconds))
    angle = 2 * np.pi * np.mod(us, _US_PER_DAY) / _US_PER_DAY
    if component == "sin_of_day":
        return np.sin(angle)
    return np.cos(angle)


class DurationEncoder(SingleColumnTransformer):
    """
    Extract numeric features from a duration (timedelta) column.

    Parameters
    ----------
    components : "auto" or list of str, default="auto"
        The features to extract. Valid names are ``"total_seconds"``,
        ``"days"``, ``"hours"``, ``"minutes"``, ``"seconds"``,
        ``"microseconds"``, ``"log1p_total_seconds"``, ``"sin_of_day"`` and
        ``"cos_of_day"``. ``"hours"``, ``"minutes"``, ``"seconds"`` and
        ``"microseconds"`` are remainders after the coarser unit. When
        ``"auto"``, components are chosen from ``resolution``. When a list is
        given, ``resolution`` is ignored.

    resolution : {"auto", "day", "hour", "minute", "second", "microsecond"}, \
            default="auto"
        Finest granularity of the remainder components. With ``"auto"`` it is
        detected during ``fit`` as the finest level carrying information
        (``"minute"`` if all values are null).

    handle_negative : {"keep", "clip", "abs"}, default="keep"
        How negative durations are treated before extraction: left unchanged,
        replaced by zero, or replaced by their absolute value.

    scaling : {None, "minmax", "standard", "robust"}, default=None
        Optional scaling of extracted features using statistics learned during
        ``fit``. ``"minmax"`` clips values outside the training range. Constant
        features are mapped to 0.

    Attributes
    ----------
    resolution_ : str or None
        The resolved resolution (``None`` when ``components`` is a list).

    components_ : list of str
        The extracted components.

    scaling_params_ : dict
        Per-component scaling statistics; only set when ``scaling`` is not
        ``None``.

    all_outputs_ : list of str
        The names of the output columns.

    Examples
    --------
    >>> import pandas as pd
    >>> from skrub import DurationEncoder
    >>> s = pd.Series(pd.to_timedelta(["1 days 02:00:00", "3 days", None]), name="d")
    >>> DurationEncoder().fit_transform(s)
       d_total_seconds  d_days  d_hours  d_log1p_total_seconds
    0          93600.0     1.0      2.0              11.446796
    1         259200.0     3.0      0.0              12.465359
    2              NaN     NaN      NaN                    NaN
    """

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

    def _check_params(self):
        if isinstance(self.components, str):
            if self.components != "auto":
                raise ValueError(
                    f"'components' must be 'auto' or a list of strings, got "
                    f"{self.components!r}."
                )
        elif isinstance(self.components, (list, tuple)):
            unknown = [c for c in self.components if c not in _ALL_COMPONENTS]
            if unknown or not all(isinstance(c, str) for c in self.components):
                raise ValueError(
                    f"Unknown components {unknown!r}. Valid components are "
                    f"{_ALL_COMPONENTS}."
                )
        else:
            raise TypeError(
                "'components' must be 'auto' or a list or tuple of strings, got "
                f"{type(self.components).__name__}."
            )
        if self.resolution != "auto" and self.resolution not in _RESOLUTIONS:
            raise ValueError(
                f"'resolution' must be 'auto' or one of {_RESOLUTIONS}, got "
                f"{self.resolution!r}."
            )
        if self.handle_negative not in ("keep", "clip", "abs"):
            raise ValueError(
                "'handle_negative' must be 'keep', 'clip' or 'abs', got "
                f"{self.handle_negative!r}."
            )
        if self.scaling not in (None, "minmax", "standard", "robust"):
            raise ValueError(
                "'scaling' must be None, 'minmax', 'standard' or 'robust', got "
                f"{self.scaling!r}."
            )

    def _microseconds(self, column):
        us = _to_microseconds(column)
        if self.handle_negative == "clip":
            us = np.where(us < 0, 0.0, us)
        elif self.handle_negative == "abs":
            us = np.abs(us)
        return us

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
        if not sbd.is_duration(column):
            raise RejectColumn(
                f"Column {sbd.name(column)!r} does not have a duration dtype."
            )
        self._check_params()
        us = self._microseconds(column)
        if isinstance(self.components, str):
            self.resolution_ = (
                _detect_resolution(us)
                if self.resolution == "auto"
                else self.resolution
            )
            self.components_ = _components_for_resolution(self.resolution_)
        else:
            self.resolution_ = None
            self.components_ = list(self.components)
        name = sbd.name(column)
        self.all_outputs_ = [f"{name}_{c}" for c in self.components_]
        if self.scaling is not None:
            self.scaling_params_ = {}
            for c in self.components_:
                self.scaling_params_[c] = self._fit_scaling(_extract(us, c))
        return self._transform(column, us)

    def _fit_scaling(self, values):
        values = values[~np.isnan(values)]
        if not values.size:
            values = np.zeros(1)
        if self.scaling == "minmax":
            return {"min": float(values.min()), "max": float(values.max())}
        if self.scaling == "standard":
            return {"mean": float(values.mean()), "std": float(values.std())}
        q25, median, q75 = np.percentile(values, [25, 50, 75])
        return {"median": float(median), "iqr": float(q75 - q25)}

    def _scale(self, values, params):
        if self.scaling == "minmax":
            offset, scale = params["min"], params["max"] - params["min"]
        elif self.scaling == "standard":
            offset, scale = params["mean"], params["std"]
        else:
            offset, scale = params["median"], params["iqr"]
        if scale == 0:
            return np.where(np.isnan(values), np.nan, 0.0)
        values = (values - offset) / scale
        if self.scaling == "minmax":
            values = np.clip(values, 0.0, 1.0)
        return values

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
        return self._transform(column, self._microseconds(column))

    def _transform(self, column, us):
        outputs = []
        for component, out_name in zip(self.components_, self.all_outputs_):
            values = _extract(us, component)
            if self.scaling is not None:
                values = self._scale(values, self.scaling_params_[component])
            outputs.append(_make_output_column(column, values, out_name))
        return sbd.copy_index(column, sbd.make_dataframe_like(column, outputs))

    def get_feature_names_out(self):
        """Get output feature names for transformation.

        Returns
        -------
        list of str
            The names of the output columns.
        """
        check_is_fitted(self, "all_outputs_")
        return self.all_outputs_
