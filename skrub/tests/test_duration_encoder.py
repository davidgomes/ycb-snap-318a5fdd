from datetime import timedelta

import numpy as np
import pytest

from skrub import DurationEncoder, TableVectorizer
from skrub import _dataframe as sbd
from skrub import selectors as s
from skrub._single_column_transformer import RejectColumn
from skrub._to_float import ToFloat
from skrub._to_str import ToStr


def durations(df_module, values, name="d"):
    return df_module.make_column(name, values)


def values(df, component, name="d"):
    return sbd.to_numpy(sbd.col(df, f"{name}_{component}")).astype("float64")


def test_default_components(df_module):
    col = durations(
        df_module,
        [
            timedelta(days=1, hours=2, minutes=30),
            None,
            timedelta(days=3, minutes=15),
        ],
    )
    encoder = DurationEncoder()
    out = encoder.fit_transform(col)
    assert encoder.resolution_ == "minute"
    assert encoder.components_ == [
        "total_seconds",
        "days",
        "hours",
        "minutes",
        "log1p_total_seconds",
    ]
    assert sbd.column_names(out) == [f"d_{c}" for c in encoder.components_]
    assert encoder.get_feature_names_out() == sbd.column_names(out)
    for c in sbd.column_names(out):
        assert sbd.dtype(sbd.col(out, c)) == sbd.dtype(sbd.to_float32(sbd.col(out, c)))
    np.testing.assert_allclose(values(out, "total_seconds"), [95400, np.nan, 260100])
    np.testing.assert_allclose(values(out, "days"), [1, np.nan, 3])
    np.testing.assert_allclose(values(out, "hours"), [2, np.nan, 0])
    np.testing.assert_allclose(values(out, "minutes"), [30, np.nan, 15])
    np.testing.assert_allclose(
        values(out, "log1p_total_seconds"),
        np.log1p([95400, np.nan, 260100]),
        rtol=1e-6,
    )


def test_nulls_propagate(df_module):
    col = durations(df_module, [timedelta(seconds=1.5), None])
    out = DurationEncoder(
        components=["total_seconds", "days", "microseconds", "sin_of_day"]
    ).fit_transform(col)
    for c in sbd.column_names(out):
        assert sbd.to_list(sbd.is_null(sbd.col(out, c))) == [False, True]


@pytest.mark.parametrize(
    "resolution, expected",
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
def test_resolution(df_module, resolution, expected):
    col = durations(df_module, [timedelta(days=2)])
    encoder = DurationEncoder(resolution=resolution).fit(col)
    assert encoder.resolution_ == resolution
    assert encoder.components_ == expected


@pytest.mark.parametrize(
    "value, expected",
    [
        (timedelta(days=2), "day"),
        (timedelta(days=2, hours=3), "hour"),
        (timedelta(hours=3, minutes=1), "minute"),
        (timedelta(minutes=1, seconds=4), "second"),
        (timedelta(seconds=4, microseconds=12), "microsecond"),
    ],
)
def test_auto_resolution(df_module, value, expected):
    col = durations(df_module, [timedelta(days=1), value, None])
    assert DurationEncoder().fit(col).resolution_ == expected


def test_auto_resolution_all_null(df_module):
    col = sbd.all_null_like(durations(df_module, [timedelta(days=1)] * 3))
    assert sbd.is_duration(col)
    encoder = DurationEncoder().fit(col)
    assert encoder.resolution_ == "minute"
    out = encoder.transform(col)
    for c in sbd.column_names(out):
        assert sbd.is_all_null(sbd.col(out, c))


def test_remainder_components(df_module):
    col = durations(
        df_module,
        [timedelta(days=5, hours=23, minutes=59, seconds=58, microseconds=123456)],
    )
    out = DurationEncoder(resolution="microsecond").fit_transform(col)
    assert values(out, "days") == [5]
    assert values(out, "hours") == [23]
    assert values(out, "minutes") == [59]
    assert values(out, "seconds") == [58]
    assert values(out, "microseconds") == [123456]


def test_explicit_components(df_module):
    col = durations(df_module, [timedelta(hours=6), timedelta(hours=18)])
    encoder = DurationEncoder(
        components=["sin_of_day", "days", "cos_of_day"], resolution="day"
    )
    out = encoder.fit_transform(col)
    assert encoder.components_ == ["sin_of_day", "days", "cos_of_day"]
    assert sbd.column_names(out) == ["d_sin_of_day", "d_days", "d_cos_of_day"]
    np.testing.assert_allclose(values(out, "sin_of_day"), [1, -1], atol=1e-6)
    np.testing.assert_allclose(values(out, "cos_of_day"), [0, 0], atol=1e-6)
    encoder = DurationEncoder(components=("days",)).fit(col)
    assert encoder.components_ == ["days"]


def test_cyclical_not_in_resolutions(df_module):
    col = durations(df_module, [timedelta(seconds=1, microseconds=1)])
    encoder = DurationEncoder(resolution="microsecond").fit(col)
    assert "sin_of_day" not in encoder.components_
    assert "cos_of_day" not in encoder.components_


@pytest.mark.parametrize(
    "handle_negative, expected",
    [("keep", [-7200, 3600]), ("clip", [0, 3600]), ("abs", [7200, 3600])],
)
def test_handle_negative(df_module, handle_negative, expected):
    col = durations(df_module, [timedelta(hours=-2), timedelta(hours=1)])
    out = DurationEncoder(
        components=["total_seconds"], handle_negative=handle_negative
    ).fit_transform(col)
    np.testing.assert_allclose(values(out, "total_seconds"), expected)


def test_negative_keep_components(df_module):
    col = durations(df_module, [timedelta(hours=-1)])
    out = DurationEncoder(resolution="hour").fit_transform(col)
    # same convention as datetime.timedelta: -1 day + 23 hours
    assert values(out, "days") == [-1]
    assert values(out, "hours") == [23]
    np.testing.assert_allclose(
        values(out, "log1p_total_seconds"), [-np.log1p(3600)], rtol=1e-6
    )


def test_scaling_minmax(df_module):
    train = durations(df_module, [timedelta(days=1), timedelta(days=3), None])
    encoder = DurationEncoder(components=["days"], scaling="minmax")
    out = encoder.fit_transform(train)
    assert encoder.scaling_params_ == {"days": {"min": 1.0, "max": 3.0}}
    np.testing.assert_allclose(values(out, "days"), [0, 1, np.nan])
    test = durations(df_module, [timedelta(days=0), timedelta(days=2), timedelta(9)])
    np.testing.assert_allclose(values(encoder.transform(test), "days"), [0, 0.5, 1])


def test_scaling_standard(df_module):
    train = durations(df_module, [timedelta(days=d) for d in [1, 2, 3, 6]])
    encoder = DurationEncoder(components=["days"], scaling="standard")
    out = encoder.fit_transform(train)
    days = np.asarray([1, 2, 3, 6], dtype="float64")
    assert encoder.scaling_params_["days"]["mean"] == pytest.approx(days.mean())
    assert encoder.scaling_params_["days"]["std"] == pytest.approx(days.std())
    np.testing.assert_allclose(
        values(out, "days"), (days - days.mean()) / days.std(), rtol=1e-6
    )


def test_scaling_robust(df_module):
    train = durations(df_module, [timedelta(days=d) for d in [1, 2, 3, 4, 100]])
    encoder = DurationEncoder(components=["days"], scaling="robust")
    out = encoder.fit_transform(train)
    assert encoder.scaling_params_ == {"days": {"median": 3.0, "iqr": 2.0}}
    np.testing.assert_allclose(values(out, "days"), [-1, -0.5, 0, 0.5, 48.5])


@pytest.mark.parametrize("scaling", ["minmax", "standard", "robust"])
def test_scaling_constant(df_module, scaling):
    train = durations(df_module, [timedelta(seconds=0.1)] * 10 + [None])
    encoder = DurationEncoder(scaling=scaling).fit(train)
    assert set(encoder.scaling_params_) == set(encoder.components_)
    out = encoder.transform(
        durations(df_module, [timedelta(seconds=0.1), timedelta(days=4), None])
    )
    for c in encoder.components_:
        np.testing.assert_array_equal(values(out, c), [0, 0, np.nan])


def test_no_scaling_params_by_default(df_module):
    encoder = DurationEncoder().fit(durations(df_module, [timedelta(days=1)]))
    assert not hasattr(encoder, "scaling_params_")


def test_transform_matches_fit_transform(df_module):
    col = durations(df_module, [timedelta(minutes=m) for m in [1, 70, 2000]])
    encoder = DurationEncoder(scaling="standard")
    df_module.assert_frame_equal(encoder.fit_transform(col), encoder.transform(col))


def test_reject_non_duration(df_module):
    with pytest.raises(RejectColumn, match=".*does not have a Duration dtype"):
        DurationEncoder().fit_transform(df_module.make_column("d", [1.5, 2.0]))
    date = sbd.to_datetime(df_module.make_column("d", ["2020-01-02"]), "%Y-%m-%d")
    with pytest.raises(RejectColumn):
        DurationEncoder().fit_transform(date)


@pytest.mark.parametrize(
    "params, error",
    [
        ({"components": 3}, TypeError),
        ({"components": None}, TypeError),
        ({"components": ["days", 1]}, TypeError),
        ({"components": ["days", "weeks"]}, ValueError),
        ({"components": "days"}, ValueError),
        ({"components": []}, ValueError),
        ({"components": ["days", "days"]}, ValueError),
        ({"resolution": "week"}, ValueError),
        ({"handle_negative": "drop"}, ValueError),
        ({"scaling": "log"}, ValueError),
    ],
)
def test_bad_params(df_module, params, error):
    col = durations(df_module, [timedelta(days=1)])
    with pytest.raises(error):
        DurationEncoder(**params).fit(col)


def test_resolution_ignored_with_explicit_components(df_module):
    col = durations(df_module, [timedelta(days=1)])
    encoder = DurationEncoder(components=["days"], resolution="not used").fit(col)
    assert encoder.resolution_ is None
    assert encoder.components_ == ["days"]


def test_duration_selector(df_module):
    df = df_module.make_dataframe({"td": [timedelta(days=1)], "f": [1.5], "s": ["a"]})
    assert s.duration().expand(df) == ["td"]
    assert repr(s.duration()) == "duration()"


def test_to_float_and_to_str_reject_durations(df_module):
    col = durations(df_module, [timedelta(days=1)])
    with pytest.raises(RejectColumn):
        ToFloat().fit_transform(col)
    with pytest.raises(RejectColumn):
        ToStr().fit_transform(col)


def test_table_vectorizer(df_module):
    df = df_module.make_dataframe(
        {
            "wait": [timedelta(days=1), timedelta(days=2, hours=4), None],
            "x": [1.0, 2.0, 3.0],
        }
    )
    tv = TableVectorizer()
    out = tv.fit_transform(df)
    assert tv.column_to_kind_["wait"] == "duration"
    assert tv.kind_to_columns_["duration"] == ["wait"]
    assert isinstance(tv.transformers_["wait"], DurationEncoder)
    assert tv.input_to_outputs_["wait"] == [
        "wait_total_seconds",
        "wait_days",
        "wait_hours",
        "wait_log1p_total_seconds",
    ]
    np.testing.assert_allclose(values(out, "hours", "wait"), [0, 4, np.nan])
    df_module.assert_frame_equal(tv.transform(df), out)

    tv = TableVectorizer(duration=DurationEncoder(components=["days"])).fit(df)
    assert tv.input_to_outputs_["wait"] == ["wait_days"]
    tv = TableVectorizer(duration="drop").fit(df)
    assert tv.all_outputs_ == ["x"]
    assert "duration" in tv._repr_html_()
