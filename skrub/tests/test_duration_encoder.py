import math
from datetime import timedelta

import numpy as np
import pytest
from sklearn.base import clone

from skrub import Cleaner, DurationEncoder, TableVectorizer
from skrub import _dataframe as sbd
from skrub import selectors as s
from skrub._duration_encoder import _divmod_ns
from skrub._single_column_transformer import RejectColumn
from skrub._to_float import ToFloat
from skrub._to_str import ToStr

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


def _python_parts(delta, handle_negative="keep"):
    if handle_negative == "clip" and delta < timedelta(0):
        delta = timedelta(0)
    elif handle_negative == "abs" and delta < timedelta(0):
        delta = abs(delta)
    seconds_within_day = delta.seconds + delta.microseconds / 1_000_000
    hours, remainder = divmod(delta.seconds, 3600)
    minutes, seconds = divmod(remainder, 60)
    total_seconds = delta.total_seconds()
    angle = 2.0 * math.pi * seconds_within_day / 86_400
    with np.errstate(divide="ignore", invalid="ignore"):
        log1p_total_seconds = float(np.log1p(total_seconds))
    return {
        "total_seconds": total_seconds,
        "days": float(delta.days),
        "hours": float(hours),
        "minutes": float(minutes),
        "seconds": float(seconds),
        "microseconds": float(delta.microseconds),
        "log1p_total_seconds": log1p_total_seconds,
        "sin_of_day": math.sin(angle),
        "cos_of_day": math.cos(angle),
    }


def _values():
    return [
        timedelta(days=1, hours=3, minutes=30, seconds=15, microseconds=500),
        timedelta(seconds=-90.5),
        None,
        timedelta(0),
        timedelta(days=2),
        timedelta(microseconds=-1),
        timedelta(hours=6),
        timedelta(seconds=90.5),
    ]


def _assert_close(actual, expected):
    if expected is None or (isinstance(expected, float) and math.isnan(expected)):
        assert actual is None or (isinstance(actual, float) and math.isnan(actual))
        return
    assert actual == pytest.approx(expected, rel=1e-9, abs=1e-9)


def test_import_and_defaults():
    import skrub

    assert skrub.DurationEncoder is DurationEncoder
    encoder = DurationEncoder()
    assert encoder.components == "auto"
    assert encoder.resolution == "auto"
    assert encoder.handle_negative == "keep"
    assert encoder.scaling is None


@pytest.mark.parametrize("handle_negative", ["keep", "clip", "abs"])
def test_components_match_timedelta_normalization(df_module, handle_negative):
    values = _values()
    column = df_module.make_column("delay", values)
    encoder = DurationEncoder(
        components=_ALL_COMPONENTS, handle_negative=handle_negative
    )
    transformed = encoder.fit_transform(column)
    assert encoder.resolution_ is None
    assert encoder.components_ == _ALL_COMPONENTS
    assert list(encoder.get_feature_names_out()) == [
        f"delay_{name}" for name in _ALL_COMPONENTS
    ]
    assert sbd.column_names(transformed) == list(encoder.get_feature_names_out())
    for name in _ALL_COMPONENTS:
        actual = sbd.to_list(sbd.col(transformed, f"delay_{name}"))
        for value, got in zip(values, actual):
            if value is None:
                if df_module.name == "polars":
                    assert got is None
                else:
                    assert isinstance(got, float) and math.isnan(got)
                continue
            expected = _python_parts(value, handle_negative)[name]
            _assert_close(got, expected)


def test_auto_resolution_levels(df_module):
    cases = [
        ([timedelta(days=2), timedelta(days=-4), None], "day"),
        ([timedelta(hours=5), timedelta(days=1), None], "hour"),
        ([timedelta(minutes=30), timedelta(hours=1), None], "minute"),
        ([timedelta(seconds=30), timedelta(minutes=1), None], "second"),
        ([timedelta(microseconds=5), timedelta(seconds=1), None], "microsecond"),
        ([timedelta(0), timedelta(days=3)], "day"),
    ]
    for values, resolution in cases:
        encoder = DurationEncoder().fit(df_module.make_column("d", values))
        assert encoder.resolution_ == resolution
        if resolution == "day":
            assert encoder.components_ == [
                "total_seconds",
                "days",
                "log1p_total_seconds",
            ]
        elif resolution == "hour":
            assert "hours" in encoder.components_
            assert "minutes" not in encoder.components_
        elif resolution == "minute":
            assert encoder.components_[-2] == "minutes"
        elif resolution == "second":
            assert encoder.components_[-2] == "seconds"
        else:
            assert encoder.components_[-2] == "microseconds"
        assert encoder.components_[0] == "total_seconds"
        assert encoder.components_[1] == "days"
        assert encoder.components_[-1] == "log1p_total_seconds"
        assert "sin_of_day" not in encoder.components_
        assert "cos_of_day" not in encoder.components_


def test_explicit_resolution_ignores_data(df_module):
    column = df_module.make_column("d", [timedelta(days=1), timedelta(microseconds=1)])
    encoder = DurationEncoder(resolution="hour").fit(column)
    assert encoder.resolution_ == "hour"
    assert encoder.components_ == [
        "total_seconds",
        "days",
        "hours",
        "log1p_total_seconds",
    ]


def test_explicit_components_ignore_resolution_and_keep_order(df_module):
    column = df_module.make_column("d", [timedelta(days=1, seconds=1)])
    components = ["log1p_total_seconds", "days", "sin_of_day"]
    encoder = DurationEncoder(components=components, resolution="microsecond")
    transformed = encoder.fit_transform(column)
    assert encoder.resolution_ is None
    assert encoder.components_ == components
    assert sbd.column_names(transformed) == [f"d_{name}" for name in components]


def test_explicit_components_accept_tuples(df_module):
    column = df_module.make_column("d", [timedelta(hours=1)])
    encoder = DurationEncoder(components=("hours", "total_seconds")).fit(column)
    assert encoder.components_ == ["hours", "total_seconds"]


def test_all_null_resolution_defaults_to_minute(df_module):
    if df_module.name == "pandas":
        import pandas as pd

        column = pd.Series([pd.NaT, pd.NaT], dtype="timedelta64[ns]", name="d")
    else:
        import polars as pl

        column = pl.Series("d", [None, None], dtype=pl.Duration("ns"))
    encoder = DurationEncoder().fit(column)
    assert encoder.resolution_ == "minute"
    assert encoder.components_ == [
        "total_seconds",
        "days",
        "hours",
        "minutes",
        "log1p_total_seconds",
    ]
    transformed = encoder.transform(column)
    for name in sbd.column_names(transformed):
        assert sbd.to_list(sbd.is_null(sbd.col(transformed, name))) == [True, True]
    assert not hasattr(encoder, "scaling_params_")


def test_nulls_propagate(df_module):
    column = df_module.make_column("d", [timedelta(days=1, seconds=2), None])
    transformed = DurationEncoder(resolution="second").fit_transform(column)
    input_nulls = sbd.to_list(sbd.is_null(column))
    for name in sbd.column_names(transformed):
        assert sbd.to_list(sbd.is_null(sbd.col(transformed, name))) == input_nulls


def test_reject_non_duration(df_module):
    column = df_module.make_column("n", [1, 2, None])
    with pytest.raises(RejectColumn, match="does not contain durations"):
        DurationEncoder().fit_transform(column)


@pytest.mark.parametrize(
    "components, error",
    [
        (1, TypeError),
        (None, TypeError),
        ("days", ValueError),
        (["not-a-component"], ValueError),
        (["days", "days"], ValueError),
        ([], ValueError),
        (["days", 1], ValueError),
    ],
)
def test_invalid_components(df_module, components, error):
    column = df_module.make_column("d", [timedelta(days=1)])
    with pytest.raises(error):
        DurationEncoder(components=components).fit_transform(column)


@pytest.mark.parametrize(
    "kwargs",
    [
        {"resolution": "fortnight"},
        {"handle_negative": "drop"},
        {"scaling": "maxabs"},
    ],
)
def test_invalid_choices(df_module, kwargs):
    column = df_module.make_column("d", [timedelta(days=1)])
    with pytest.raises(ValueError):
        DurationEncoder(**kwargs).fit_transform(column)


def test_cyclical_time_of_day(df_module):
    column = df_module.make_column(
        "d",
        [timedelta(0), timedelta(hours=6), timedelta(hours=-6), None],
    )
    transformed = DurationEncoder(
        components=["sin_of_day", "cos_of_day"]
    ).fit_transform(column)
    sin_values = sbd.to_list(sbd.col(transformed, "d_sin_of_day"))
    cos_values = sbd.to_list(sbd.col(transformed, "d_cos_of_day"))
    assert sin_values[0] == pytest.approx(0.0, abs=1e-12)
    assert cos_values[0] == pytest.approx(1.0, abs=1e-12)
    assert sin_values[1] == pytest.approx(1.0, abs=1e-12)
    assert cos_values[1] == pytest.approx(0.0, abs=1e-12)
    # -6 hours is 18:00 on the previous day.
    assert sin_values[2] == pytest.approx(-1.0, abs=1e-12)
    assert cos_values[2] == pytest.approx(0.0, abs=1e-12)
    assert sin_values[3] is None or math.isnan(sin_values[3])


def test_nanosecond_remainder():
    import pandas as pd

    column = pd.Series([1, 0, pd.NaT], dtype="timedelta64[ns]", name="d")
    encoder = DurationEncoder().fit(column)
    assert encoder.resolution_ == "microsecond"
    transformed = encoder.transform(column)
    microseconds = sbd.to_list(sbd.col(transformed, "d_microseconds"))
    assert microseconds[0] == pytest.approx(0.001)
    assert microseconds[1] == pytest.approx(0.0)
    assert microseconds[2] is None or math.isnan(microseconds[2])


def test_pandas_second_unit_negative_remainder():
    import pandas as pd

    column = pd.Series([90, -90], dtype="timedelta64[s]", name="d")
    transformed = DurationEncoder(resolution="second").fit_transform(column)
    assert sbd.to_list(sbd.col(transformed, "d_days")) == [0.0, -1.0]
    assert sbd.to_list(sbd.col(transformed, "d_hours")) == [0.0, 23.0]
    assert sbd.to_list(sbd.col(transformed, "d_minutes")) == [1.0, 58.0]
    assert sbd.to_list(sbd.col(transformed, "d_seconds")) == [30.0, 30.0]
    assert sbd.to_list(sbd.col(transformed, "d_total_seconds")) == [90.0, -90.0]


def test_transform_uses_fitted_resolution(df_module):
    train = df_module.make_column("d", [timedelta(days=2), timedelta(days=3)])
    encoder = DurationEncoder().fit(train)
    assert encoder.resolution_ == "day"
    test = df_module.make_column("d", [timedelta(seconds=30)])
    transformed = encoder.transform(test)
    assert sbd.column_names(transformed) == [
        "d_total_seconds",
        "d_days",
        "d_log1p_total_seconds",
    ]
    total_seconds = sbd.to_list(sbd.col(transformed, "d_total_seconds"))
    assert total_seconds[0] == pytest.approx(30.0)


def test_transform_follows_column_name(df_module):
    column = df_module.make_column("delay", [timedelta(days=1)])
    encoder = DurationEncoder(resolution="day").fit(column)
    renamed = sbd.rename(column, "elapsed")
    transformed = encoder.transform(renamed)
    assert sbd.column_names(transformed) == [
        "elapsed_total_seconds",
        "elapsed_days",
        "elapsed_log1p_total_seconds",
    ]
    assert list(encoder.get_feature_names_out()) == sbd.column_names(transformed)


def test_minmax_scaling_clips(df_module):
    train = df_module.make_column("d", [timedelta(seconds=0), timedelta(seconds=10)])
    encoder = DurationEncoder(components=["total_seconds"], scaling="minmax")
    transformed = encoder.fit_transform(train)
    assert sbd.to_list(sbd.col(transformed, "d_total_seconds")) == pytest.approx(
        [0.0, 1.0]
    )
    assert encoder.scaling_params_["total_seconds"]["min"] == pytest.approx(0.0)
    assert encoder.scaling_params_["total_seconds"]["max"] == pytest.approx(10.0)
    unseen = df_module.make_column(
        "d", [timedelta(seconds=-5), timedelta(seconds=20), None]
    )
    scaled = sbd.to_list(sbd.col(encoder.transform(unseen), "d_total_seconds"))
    assert scaled[0] == pytest.approx(0.0)
    assert scaled[1] == pytest.approx(1.0)
    assert scaled[2] is None or math.isnan(scaled[2])


def test_standard_and_robust_scaling(df_module):
    standard = DurationEncoder(components=["total_seconds"], scaling="standard")
    standard_out = standard.fit_transform(
        df_module.make_column("d", [timedelta(seconds=0), timedelta(seconds=10)])
    )
    assert sbd.to_list(sbd.col(standard_out, "d_total_seconds")) == pytest.approx(
        [-1.0, 1.0]
    )
    assert standard.scaling_params_["total_seconds"]["mean"] == pytest.approx(5.0)
    assert standard.scaling_params_["total_seconds"]["std"] == pytest.approx(5.0)

    robust = DurationEncoder(components=["total_seconds"], scaling="robust")
    values = [timedelta(seconds=v) for v in (0, 10, 20, 30)]
    robust_out = robust.fit_transform(df_module.make_column("d", values))
    assert sbd.to_list(sbd.col(robust_out, "d_total_seconds")) == pytest.approx(
        [-1.0, -1.0 / 3.0, 1.0 / 3.0, 1.0]
    )
    params = robust.scaling_params_["total_seconds"]
    assert params["median"] == pytest.approx(15.0)
    assert params["iqr"] == pytest.approx(15.0)


@pytest.mark.parametrize("scaling", ["minmax", "standard", "robust"])
def test_constant_scaling_is_zero(df_module, scaling):
    column = df_module.make_column(
        "d", [timedelta(seconds=5), timedelta(seconds=5), None]
    )
    encoder = DurationEncoder(components=["total_seconds"], scaling=scaling)
    transformed = encoder.fit_transform(column)
    values = sbd.to_list(sbd.col(transformed, "d_total_seconds"))
    assert values[0] == pytest.approx(0.0)
    assert values[1] == pytest.approx(0.0)
    assert values[2] is None or math.isnan(values[2])


def test_no_scaling_params_without_scaling(df_module):
    encoder = DurationEncoder().fit(df_module.make_column("d", [timedelta(days=1)]))
    assert not hasattr(encoder, "scaling_params_")


def test_clone_drops_fitted_state(df_module):
    column = df_module.make_column("d", [timedelta(hours=2)])
    encoder = DurationEncoder(resolution="hour", scaling="standard").fit(column)
    cloned = clone(encoder)
    assert not hasattr(cloned, "components_")
    assert not hasattr(cloned, "scaling_params_")
    cloned.fit(column)
    assert cloned.resolution_ == "hour"
    assert "mean" in cloned.scaling_params_["total_seconds"]


def test_pandas_index_is_preserved():
    import pandas as pd

    column = pd.Series(
        [timedelta(hours=1), None],
        index=[5, 6],
        name="d",
    )
    transformed = DurationEncoder(resolution="hour").fit_transform(column)
    assert list(transformed.index) == [5, 6]


def test_divmod_ns_slow_path():
    ticks = np.array([1, -1, 2], dtype=np.int64)
    quotient, remainder = _divmod_ns(ticks, 7, 86_400_000_000_000)
    assert list(quotient) == [0, -1, 0]
    assert int(remainder[0]) == 7
    assert int(remainder[1]) == 86_400_000_000_000 - 7
    assert int(remainder[2]) == 14


def test_duration_selector(df_module):
    from datetime import datetime

    frame = df_module.make_dataframe(
        {
            "delay": [timedelta(days=1), None],
            "when": [datetime(2020, 1, 1), None],
            "count": [1, 2],
        }
    )
    assert s.duration().expand(frame) == ["delay"]
    assert "delay" not in s.any_date().expand(frame)
    assert "delay" not in (s.numeric() - s.duration()).expand(frame)
    assert repr(s.duration()) == "duration()"


def test_to_float_and_to_str_reject_duration(df_module):
    column = df_module.make_column("d", [timedelta(days=1), None])
    with pytest.raises(RejectColumn):
        ToFloat().fit_transform(column)
    with pytest.raises(RejectColumn):
        ToStr().fit_transform(column)


def test_cleaner_keeps_duration_dtype(df_module):
    frame = df_module.make_dataframe(
        {"delay": [timedelta(days=1), timedelta(hours=2)], "count": [1, 2]}
    )
    for cleaner in (
        Cleaner(numeric_dtype="float32"),
        Cleaner(cast_to_str=True),
    ):
        transformed = cleaner.fit_transform(frame)
        assert sbd.is_duration(sbd.col(transformed, "delay"))


def test_table_vectorizer_routes_duration_columns(df_module):
    frame = df_module.make_dataframe(
        {
            "delay": [timedelta(days=1), timedelta(hours=2), None],
            "count": [1, 2, 3],
            "city": ["paris", "lyon", "paris"],
        }
    )
    vectorizer = TableVectorizer().fit(frame)
    assert vectorizer.column_to_kind_["delay"] == "duration"
    assert isinstance(vectorizer.transformers_["delay"], DurationEncoder)
    assert vectorizer.column_to_kind_["count"] == "numeric"
    outputs = vectorizer.input_to_outputs_["delay"]
    assert "delay_total_seconds" in outputs
    assert "delay_days" in outputs
    transformed = vectorizer.transform(frame)
    days = sbd.to_list(sbd.col(transformed, "delay_days"))
    assert days[0] == pytest.approx(1.0)
    assert days[1] == pytest.approx(0.0)
    assert days[2] is None or (isinstance(days[2], float) and math.isnan(days[2]))
    # The vectorizer casts encoder outputs to float32.
    if df_module.name == "pandas":
        assert sbd.dtype(sbd.col(transformed, "delay_days")) == np.float32
    else:
        assert (
            sbd.dtype(sbd.col(transformed, "delay_days")) == df_module.dtypes["float32"]
        )


def test_table_vectorizer_duration_drop_and_custom_resolution(df_module):
    frame = df_module.make_dataframe(
        {"delay": [timedelta(days=1, minutes=5), timedelta(hours=3)], "count": [1, 2]}
    )
    dropped = TableVectorizer(duration="drop").fit_transform(frame)
    assert not any(name.startswith("delay") for name in sbd.column_names(dropped))

    custom = TableVectorizer(duration=DurationEncoder(resolution="day"))
    transformed = custom.fit_transform(frame)
    names = sbd.column_names(transformed)
    assert "delay_days" in names
    assert "delay_minutes" not in names
    assert custom.transformers_["delay"].resolution_ == "day"
