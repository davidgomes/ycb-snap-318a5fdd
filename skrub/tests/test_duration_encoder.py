import math
from datetime import timedelta

import numpy as np
import pandas as pd
import pytest

from skrub import DurationEncoder, TableVectorizer
from skrub import _dataframe as sbd
from skrub._single_column_transformer import RejectColumn

ALL_COMPONENTS = [
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


def _values(df, name):
    """Column values as a list, with None for nulls (NaN in pandas)."""
    return [
        None if v is None or math.isnan(v) else v
        for v in sbd.to_list(sbd.col(df, name))
    ]


@pytest.fixture
def durations(df_module):
    return df_module.make_column(
        "d",
        [
            timedelta(days=1, hours=2, minutes=3, seconds=4, microseconds=5),
            None,
            timedelta(days=3, minutes=30),
        ],
    )


def test_fit_transform(durations, use_fit_transform):
    enc = DurationEncoder(resolution="microsecond")
    if use_fit_transform:
        out = enc.fit_transform(durations)
    else:
        out = enc.fit(durations).transform(durations)
    assert enc.resolution_ == "microsecond"
    assert enc.components_ == [
        "total_seconds",
        "days",
        "hours",
        "minutes",
        "seconds",
        "microseconds",
        "log1p_total_seconds",
    ]
    assert sbd.column_names(out) == [f"d_{c}" for c in enc.components_]
    assert enc.all_outputs_ == sbd.column_names(out)
    assert enc.get_feature_names_out() == sbd.column_names(out)
    for col in sbd.to_column_list(out):
        assert sbd.dtype(col) == sbd.dtype(sbd.to_float32(col))
    expected = {
        "total_seconds": [93784.000005, None, 261000.0],
        "days": [1.0, None, 3.0],
        "hours": [2.0, None, 0.0],
        "minutes": [3.0, None, 30.0],
        "seconds": [4.0, None, 0.0],
        "microseconds": [5.0, None, 0.0],
        "log1p_total_seconds": [np.log1p(93784.000005), None, np.log1p(261000.0)],
    }
    for component, values in expected.items():
        assert _values(out, f"d_{component}") == pytest.approx(values, rel=1e-6)


@pytest.mark.parametrize(
    "resolution, components",
    [
        ("day", ["total_seconds", "days", "log1p_total_seconds"]),
        ("hour", ["total_seconds", "days", "hours", "log1p_total_seconds"]),
        (
            "minute",
            ["total_seconds", "days", "hours", "minutes", "log1p_total_seconds"],
        ),
        (
            "second",
            [
                "total_seconds",
                "days",
                "hours",
                "minutes",
                "seconds",
                "log1p_total_seconds",
            ],
        ),
        (
            "microsecond",
            [
                "total_seconds",
                "days",
                "hours",
                "minutes",
                "seconds",
                "microseconds",
                "log1p_total_seconds",
            ],
        ),
    ],
)
def test_resolution_components(durations, resolution, components):
    enc = DurationEncoder(resolution=resolution)
    out = enc.fit_transform(durations)
    assert enc.resolution_ == resolution
    assert enc.components_ == components
    assert sbd.column_names(out) == [f"d_{c}" for c in components]


@pytest.mark.parametrize(
    "values, resolution",
    [
        ([timedelta(days=1), timedelta(days=12), None], "day"),
        ([timedelta(days=-3), timedelta(days=2)], "day"),
        ([timedelta(days=1), timedelta(hours=5)], "hour"),
        ([timedelta(hours=-5)], "hour"),
        ([timedelta(days=1), timedelta(hours=1, minutes=30)], "minute"),
        ([timedelta(minutes=1, seconds=1)], "second"),
        ([timedelta(seconds=1, milliseconds=1)], "microsecond"),
        ([timedelta(microseconds=1)], "microsecond"),
        ([None, None], "minute"),
    ],
)
def test_auto_resolution(df_module, values, resolution):
    col = df_module.make_column("d", values)
    if sbd.is_pandas(col):
        col = col.astype("timedelta64[ns]")
    else:
        col = col.cast(df_module.module.Duration("us"))
    enc = DurationEncoder()
    enc.fit(col)
    assert enc.resolution_ == resolution
    expected = DurationEncoder(resolution=resolution).fit(col).components_
    assert enc.components_ == expected


def test_auto_resolution_empty_column(df_module):
    col = df_module.make_column("d", [timedelta(days=1)])
    col = sbd.slice(col, 0)
    enc = DurationEncoder()
    out = enc.fit_transform(col)
    assert enc.resolution_ == "minute"
    assert sbd.shape(out) == (0, 5)


def test_auto_resolution_ignores_nanoseconds():
    col = pd.Series(
        [pd.Timedelta(days=1, nanoseconds=500), pd.Timedelta(days=2)],
        dtype="timedelta64[ns]",
    )
    assert DurationEncoder().fit(col).resolution_ == "day"


def test_polars_time_units():
    pl = pytest.importorskip("polars")
    from polars.testing import assert_frame_equal

    values = [timedelta(days=1, milliseconds=7), None, timedelta(hours=-3)]
    expected = None
    for unit in ["ns", "us", "ms"]:
        col = pl.Series("d", values).cast(pl.Duration(unit))
        enc = DurationEncoder()
        out = enc.fit_transform(col)
        assert enc.resolution_ == "microsecond"
        if expected is None:
            expected = out
        assert_frame_equal(out, expected)


def test_explicit_components(durations):
    components = ["cos_of_day", "days", "sin_of_day", "total_seconds"]
    enc = DurationEncoder(components=components, resolution="day")
    out = enc.fit_transform(durations)
    expected_order = ["total_seconds", "days", "sin_of_day", "cos_of_day"]
    assert enc.components_ == expected_order
    assert enc.resolution_ is None
    assert sbd.column_names(out) == [f"d_{c}" for c in expected_order]

    day_fraction = [(2 * 3600 + 3 * 60 + 4.000005) / 86400, 30 * 60 / 86400]
    expected_sin = [np.sin(2 * np.pi * day_fraction[0]), np.sin(np.pi / 24)]
    expected_cos = [np.cos(2 * np.pi * day_fraction[0]), np.cos(np.pi / 24)]
    assert _values(out, "d_sin_of_day") == pytest.approx(
        [expected_sin[0], None, expected_sin[1]], rel=1e-5
    )
    assert _values(out, "d_cos_of_day") == pytest.approx(
        [expected_cos[0], None, expected_cos[1]], rel=1e-5
    )


def test_cyclical_components_never_in_auto(durations):
    for resolution in ["auto", "day", "hour", "minute", "second", "microsecond"]:
        enc = DurationEncoder(resolution=resolution).fit(durations)
        assert "sin_of_day" not in enc.components_
        assert "cos_of_day" not in enc.components_


def test_all_components(durations):
    enc = DurationEncoder(components=tuple(reversed(ALL_COMPONENTS)))
    out = enc.fit_transform(durations)
    assert enc.components_ == ALL_COMPONENTS
    assert sbd.shape(out) == (3, len(ALL_COMPONENTS))


def test_duplicated_components(durations):
    enc = DurationEncoder(components=["days", "days"]).fit(durations)
    assert enc.components_ == ["days"]


@pytest.mark.parametrize("components", [1, 2.5, None, {"days": 1}, {"days"}])
def test_components_bad_type(durations, components):
    with pytest.raises(TypeError, match="'components' must be 'auto' or a list"):
        DurationEncoder(components=components).fit(durations)


def test_components_bad_element_type(durations):
    with pytest.raises(TypeError, match="'components' must be 'auto' or a list"):
        DurationEncoder(components=["days", 3]).fit(durations)


@pytest.mark.parametrize(
    "components, match",
    [
        (["days", "weeks"], r"Unknown components \['weeks'\]"),
        (("year",), r"Unknown components \['year'\]"),
        ([], "must not be empty"),
        ("days", "'components' must be 'auto' or a list"),
    ],
)
def test_components_bad_value(durations, components, match):
    with pytest.raises(ValueError, match=match):
        DurationEncoder(components=components).fit(durations)


@pytest.mark.parametrize(
    "params, match",
    [
        (dict(resolution="week"), "'resolution' options are"),
        (dict(resolution=None), "'resolution' options are"),
        (dict(handle_negative="drop"), "'handle_negative' options are"),
        (dict(scaling="log"), "'scaling' options are"),
    ],
)
def test_bad_params(durations, params, match):
    with pytest.raises(ValueError, match=match):
        DurationEncoder(**params).fit(durations)


def test_reject_non_duration(df_module):
    with pytest.raises(RejectColumn, match="does not have Duration dtype"):
        DurationEncoder().fit_transform(df_module.example_column)
    datetimes = sbd.col(df_module.example_dataframe, "datetime-col")
    with pytest.raises(RejectColumn, match="does not have Duration dtype"):
        DurationEncoder().fit_transform(datetimes)


@pytest.fixture
def signed_durations(df_module):
    return df_module.make_column(
        "d",
        [
            timedelta(days=2, hours=3),
            -timedelta(days=1, hours=2, minutes=30),
            None,
        ],
    )


def test_handle_negative_keep(signed_durations):
    components = ["total_seconds", "days", "hours", "minutes", "log1p_total_seconds"]
    out = DurationEncoder(components=components).fit_transform(signed_durations)
    assert _values(out, "d_total_seconds") == [183600.0, -95400.0, None]
    assert _values(out, "d_days") == [2.0, -1.0, None]
    assert _values(out, "d_hours") == [3.0, -2.0, None]
    assert _values(out, "d_minutes") == [0.0, -30.0, None]
    assert _values(out, "d_log1p_total_seconds") == pytest.approx(
        [np.log1p(183600.0), -np.log1p(95400.0), None], rel=1e-6
    )


def test_handle_negative_clip(signed_durations):
    components = ["total_seconds", "days", "hours", "log1p_total_seconds"]
    enc = DurationEncoder(components=components, handle_negative="clip")
    out = enc.fit_transform(signed_durations)
    assert _values(out, "d_total_seconds") == [183600.0, 0.0, None]
    assert _values(out, "d_days") == [2.0, 0.0, None]
    assert _values(out, "d_hours") == [3.0, 0.0, None]
    assert _values(out, "d_log1p_total_seconds")[1] == 0.0


def test_handle_negative_abs(signed_durations):
    components = ["total_seconds", "days", "hours", "minutes"]
    enc = DurationEncoder(components=components, handle_negative="abs")
    out = enc.fit_transform(signed_durations)
    assert _values(out, "d_total_seconds") == [183600.0, 95400.0, None]
    assert _values(out, "d_days") == [2.0, 1.0, None]
    assert _values(out, "d_hours") == [3.0, 2.0, None]
    assert _values(out, "d_minutes") == [0.0, 30.0, None]


def test_handle_negative_applied_in_transform(df_module):
    train = df_module.make_column("d", [timedelta(days=1), timedelta(days=2)])
    test = df_module.make_column("d", [timedelta(days=-4)])
    enc = DurationEncoder(components=["days"], handle_negative="abs").fit(train)
    assert _values(enc.transform(test), "d_days") == [4.0]
    enc = DurationEncoder(components=["days"], handle_negative="clip").fit(train)
    assert _values(enc.transform(test), "d_days") == [0.0]


def test_handle_negative_auto_resolution(df_module):
    col = df_module.make_column("d", [-timedelta(hours=1), timedelta(days=2)])
    assert DurationEncoder().fit(col).resolution_ == "hour"
    assert DurationEncoder(handle_negative="clip").fit(col).resolution_ == "day"


@pytest.fixture
def train_days(df_module):
    return df_module.make_column("d", [timedelta(days=d) for d in [1, 2, 3, 10]])


def test_scaling_none(train_days):
    enc = DurationEncoder(components=["days"]).fit(train_days)
    assert not hasattr(enc, "scaling_params_")
    assert _values(enc.transform(train_days), "d_days") == [1.0, 2.0, 3.0, 10.0]


def test_scaling_minmax(df_module, train_days):
    enc = DurationEncoder(components=["days", "total_seconds"], scaling="minmax")
    out = enc.fit_transform(train_days)
    assert enc.scaling_params_ == {
        "total_seconds": {"min": 86400.0, "max": 864000.0},
        "days": {"min": 1.0, "max": 10.0},
    }
    assert _values(out, "d_days") == pytest.approx([0.0, 1 / 9, 2 / 9, 1.0])
    test = df_module.make_column(
        "d", [timedelta(days=0), timedelta(days=5.5), timedelta(days=20), None]
    )
    out = enc.transform(test)
    assert _values(out, "d_days") == pytest.approx([0.0, 4 / 9, 1.0, None])
    assert _values(out, "d_total_seconds") == pytest.approx([0.0, 0.5, 1.0, None])


def test_scaling_standard(df_module, train_days):
    enc = DurationEncoder(components=["days"], scaling="standard")
    out = enc.fit_transform(train_days)
    days = np.asarray([1.0, 2.0, 3.0, 10.0])
    assert enc.scaling_params_["days"] == pytest.approx(
        {"mean": days.mean(), "std": days.std()}
    )
    assert _values(out, "d_days") == pytest.approx(
        list((days - days.mean()) / days.std()), rel=1e-6
    )
    test = df_module.make_column("d", [timedelta(days=20)])
    assert _values(enc.transform(test), "d_days") == pytest.approx(
        [(20 - days.mean()) / days.std()], rel=1e-6
    )


def test_scaling_robust(df_module, train_days):
    enc = DurationEncoder(components=["days"], scaling="robust")
    out = enc.fit_transform(train_days)
    q25, median, q75 = np.percentile([1.0, 2.0, 3.0, 10.0], [25, 50, 75])
    assert enc.scaling_params_["days"] == pytest.approx(
        {"median": median, "iqr": q75 - q25}
    )
    assert _values(out, "d_days") == pytest.approx(
        [(d - median) / (q75 - q25) for d in [1.0, 2.0, 3.0, 10.0]], rel=1e-6
    )


def test_scaling_ignores_nulls(df_module):
    col = df_module.make_column("d", [timedelta(days=1), None, timedelta(days=3)])
    enc = DurationEncoder(components=["days"], scaling="minmax").fit(col)
    assert enc.scaling_params_ == {"days": {"min": 1.0, "max": 3.0}}
    enc = DurationEncoder(components=["days"], scaling="standard").fit(col)
    assert enc.scaling_params_ == {"days": {"mean": 2.0, "std": 1.0}}


@pytest.mark.parametrize("scaling", ["minmax", "standard", "robust"])
def test_scaling_constant_column(df_module, scaling):
    col = df_module.make_column("d", [timedelta(hours=5)] * 3 + [None])
    enc = DurationEncoder(resolution="hour", scaling=scaling)
    out = enc.fit_transform(col)
    for name in sbd.column_names(out):
        assert _values(out, name) == [0.0, 0.0, 0.0, None]
    other = df_module.make_column("d", [timedelta(days=2)])
    for name in enc.all_outputs_:
        assert _values(enc.transform(other), name) == [0.0]


@pytest.mark.parametrize("scaling", ["minmax", "standard", "robust"])
def test_scaling_all_null_column(df_module, scaling):
    col = df_module.make_column("d", [timedelta(hours=5), None])
    col = sbd.slice(col, 1, 2)
    out = DurationEncoder(scaling=scaling).fit_transform(col)
    for name in sbd.column_names(out):
        assert _values(out, name) == [None]


def test_scaling_params_removed_on_refit(train_days):
    enc = DurationEncoder(scaling="minmax").fit(train_days)
    assert hasattr(enc, "scaling_params_")
    enc.set_params(scaling=None).fit(train_days)
    assert not hasattr(enc, "scaling_params_")


def test_nulls_propagate(df_module, durations):
    out = DurationEncoder(components=ALL_COMPONENTS).fit_transform(durations)
    for col in sbd.to_column_list(out):
        assert sbd.to_list(sbd.is_null(col)) == [False, True, False]


def test_all_null_column(df_module):
    col = df_module.make_column("d", [timedelta(days=1), None, None])
    col = sbd.slice(col, 1, 3)
    enc = DurationEncoder()
    out = enc.fit_transform(col)
    assert enc.resolution_ == "minute"
    assert sbd.column_names(out) == [
        "d_total_seconds",
        "d_days",
        "d_hours",
        "d_minutes",
        "d_log1p_total_seconds",
    ]
    for col in sbd.to_column_list(out):
        assert sbd.to_list(sbd.is_null(col)) == [True, True]


def test_pandas_index_preserved():
    col = pd.Series(
        pd.to_timedelta(["1 days", None, "2 days"]), index=[10, 5, 7], name="d"
    )
    out = DurationEncoder().fit_transform(col)
    assert list(out.index) == [10, 5, 7]
    assert out["d_days"].isna().tolist() == [False, True, False]


def test_output_names_use_transform_column_name(df_module, durations):
    enc = DurationEncoder(components=["days"]).fit(durations)
    renamed = sbd.rename(durations, "other")
    assert sbd.column_names(enc.transform(renamed)) == ["other_days"]
    assert enc.get_feature_names_out() == ["d_days"]


def test_table_vectorizer_dispatch(df_module):
    df = df_module.make_dataframe(
        {
            "duration": [timedelta(days=1), timedelta(days=2, hours=3), None],
            "number": [1.0, 2.0, 3.0],
        }
    )
    tv = TableVectorizer()
    out = tv.fit_transform(df)
    assert tv.kind_to_columns_["duration"] == ["duration"]
    assert tv.column_to_kind_["duration"] == "duration"
    assert isinstance(tv.transformers_["duration"], DurationEncoder)
    assert tv.transformers_["duration"].resolution_ == "hour"
    assert tv.input_to_outputs_["duration"] == [
        "duration_total_seconds",
        "duration_days",
        "duration_hours",
        "duration_log1p_total_seconds",
    ]
    assert sbd.column_names(out) == tv.input_to_outputs_["duration"] + ["number"]
    assert _values(out, "duration_hours") == [0.0, 3.0, None]
    df_module.assert_frame_equal(tv.transform(df), out)


def test_table_vectorizer_duration_param(df_module):
    df = df_module.make_dataframe(
        {"duration": [timedelta(days=1), timedelta(days=2)], "number": [1.0, 2.0]}
    )
    tv = TableVectorizer(duration=DurationEncoder(components=["days"]))
    out = tv.fit_transform(df)
    assert sbd.column_names(out) == ["duration_days", "number"]

    out = TableVectorizer(duration="drop").fit_transform(df)
    assert sbd.column_names(out) == ["number"]

    out = TableVectorizer(duration="passthrough").fit_transform(df)
    assert sbd.column_names(out) == ["duration", "number"]
    assert sbd.is_duration(sbd.col(out, "duration"))
