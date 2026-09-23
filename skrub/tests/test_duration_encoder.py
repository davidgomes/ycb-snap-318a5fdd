from datetime import timedelta

import numpy as np
import pytest

from skrub import DurationEncoder, TableVectorizer, ToFloat
from skrub import _dataframe as sbd
from skrub import selectors as s
from skrub._single_column_transformer import RejectColumn
from skrub._to_str import ToStr


def durations(df_module):
    return df_module.make_column(
        "d",
        [
            timedelta(days=1, hours=2, minutes=3, seconds=4, microseconds=5),
            None,
            timedelta(days=3, hours=5),
            timedelta(hours=-1),
        ],
    )


def _values(df, name):
    return sbd.to_numpy(sbd.col(df, name)).astype("float64")


def test_default(df_module):
    encoder = DurationEncoder()
    out = encoder.fit_transform(durations(df_module))
    assert encoder.resolution_ == "microsecond"
    assert encoder.components_ == [
        "total_seconds",
        "days",
        "hours",
        "minutes",
        "seconds",
        "microseconds",
        "log1p_total_seconds",
    ]
    assert encoder.get_feature_names_out() == [f"d_{c}" for c in encoder.components_]
    assert sbd.column_names(out) == encoder.get_feature_names_out()
    total = 86400 + 2 * 3600 + 3 * 60 + 4 + 5e-6
    np.testing.assert_allclose(
        _values(out, "d_total_seconds"), [total, np.nan, 3 * 86400 + 5 * 3600, -3600]
    )
    np.testing.assert_allclose(_values(out, "d_days"), [1, np.nan, 3, -1])
    np.testing.assert_allclose(_values(out, "d_hours"), [2, np.nan, 5, 23])
    np.testing.assert_allclose(_values(out, "d_minutes"), [3, np.nan, 0, 0])
    np.testing.assert_allclose(_values(out, "d_seconds"), [4, np.nan, 0, 0])
    np.testing.assert_allclose(_values(out, "d_microseconds"), [5, np.nan, 0, 0])
    np.testing.assert_allclose(
        _values(out, "d_log1p_total_seconds"),
        [np.log1p(total), np.nan, np.log1p(277200), -np.log1p(3600)],
        rtol=1e-6,
    )
    for col in sbd.to_column_list(out):
        assert sbd.is_float(col)
        assert sbd.is_null(col)[1]


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
    encoder = DurationEncoder(resolution=resolution).fit(durations(df_module))
    assert encoder.resolution_ == resolution
    assert encoder.components_ == expected


@pytest.mark.parametrize(
    "values, expected",
    [
        ([timedelta(days=2), timedelta(days=-3)], "day"),
        ([timedelta(days=2), timedelta(hours=5)], "hour"),
        ([timedelta(minutes=5), timedelta(days=1)], "minute"),
        ([timedelta(seconds=5), timedelta(days=1)], "second"),
        ([timedelta(microseconds=5), timedelta(days=1)], "microsecond"),
        ([None, None], "minute"),
    ],
)
def test_auto_resolution(df_module, values, expected):
    col = df_module.make_column("d", values)
    if not sbd.is_duration(col):
        col = sbd.cast(col, sbd.dtype(durations(df_module)))
    assert DurationEncoder().fit(col).resolution_ == expected


def test_explicit_components(df_module):
    encoder = DurationEncoder(
        components=("days", "sin_of_day", "cos_of_day"), resolution="day"
    )
    out = encoder.fit_transform(durations(df_module))
    assert encoder.components_ == ["days", "sin_of_day", "cos_of_day"]
    assert sbd.column_names(out) == ["d_days", "d_sin_of_day", "d_cos_of_day"]
    frac = np.asarray([(2 * 3600 + 3 * 60 + 4 + 5e-6) / 86400, np.nan, 5 / 24, 23 / 24])
    np.testing.assert_allclose(
        _values(out, "d_sin_of_day"), np.sin(2 * np.pi * frac), atol=1e-6
    )
    np.testing.assert_allclose(
        _values(out, "d_cos_of_day"), np.cos(2 * np.pi * frac), atol=1e-6
    )


@pytest.mark.parametrize(
    "handle_negative, expected",
    [("keep", -3600), ("clip", 0), ("abs", 3600)],
)
def test_handle_negative(df_module, handle_negative, expected):
    out = DurationEncoder(
        components=["total_seconds"], handle_negative=handle_negative
    ).fit_transform(durations(df_module))
    assert _values(out, "d_total_seconds")[3] == expected


def test_scaling_minmax(df_module):
    col = df_module.make_column("d", [timedelta(days=d) for d in [1, 2, 3]])
    encoder = DurationEncoder(components=["days"], scaling="minmax")
    out = encoder.fit_transform(col)
    assert encoder.scaling_params_ == {"days": {"min": 1.0, "max": 3.0}}
    np.testing.assert_allclose(_values(out, "d_days"), [0, 0.5, 1])
    test = df_module.make_column("d", [timedelta(days=d) for d in [0, 2, 10]])
    np.testing.assert_allclose(_values(encoder.transform(test), "d_days"), [0, 0.5, 1])


def test_scaling_standard(df_module):
    col = df_module.make_column("d", [timedelta(days=d) for d in [1, 2, 3]])
    encoder = DurationEncoder(components=["days"], scaling="standard")
    out = encoder.fit_transform(col)
    std = np.std([1, 2, 3])
    assert encoder.scaling_params_["days"]["mean"] == 2.0
    assert encoder.scaling_params_["days"]["std"] == pytest.approx(std)
    np.testing.assert_allclose(
        _values(out, "d_days"), [-1 / std, 0, 1 / std], rtol=1e-6
    )


def test_scaling_robust(df_module):
    col = df_module.make_column("d", [timedelta(days=d) for d in [1, 2, 3, 4, 5]])
    encoder = DurationEncoder(components=["days"], scaling="robust")
    out = encoder.fit_transform(col)
    assert encoder.scaling_params_ == {"days": {"median": 3.0, "iqr": 2.0}}
    np.testing.assert_allclose(_values(out, "d_days"), [-1, -0.5, 0, 0.5, 1])


@pytest.mark.parametrize("scaling", ["minmax", "standard", "robust"])
def test_scaling_constant(df_module, scaling):
    col = df_module.make_column("d", [timedelta(days=2), None, timedelta(days=2)])
    encoder = DurationEncoder(components=["days", "total_seconds"], scaling=scaling)
    out = encoder.fit_transform(col)
    np.testing.assert_array_equal(_values(out, "d_days"), [0, np.nan, 0])
    test = df_module.make_column("d", [timedelta(days=5)])
    np.testing.assert_array_equal(_values(encoder.transform(test), "d_days"), [0])


def test_no_scaling_params(df_module):
    encoder = DurationEncoder().fit(durations(df_module))
    assert not hasattr(encoder, "scaling_params_")


def test_reject(df_module):
    with pytest.raises(RejectColumn, match=".*does not have Duration dtype"):
        DurationEncoder().fit_transform(df_module.make_column("x", [1.0, 2.0]))


@pytest.mark.parametrize(
    "params, error",
    [
        ({"components": 3}, TypeError),
        ({"components": None}, TypeError),
        ({"components": "days"}, ValueError),
        ({"components": ["days", "bogus"]}, ValueError),
        ({"components": []}, ValueError),
        ({"resolution": "year"}, ValueError),
        ({"handle_negative": "drop"}, ValueError),
        ({"scaling": "log"}, ValueError),
    ],
)
def test_bad_params(df_module, params, error):
    with pytest.raises(error):
        DurationEncoder(**params).fit(durations(df_module))


def test_transform_matches_fit_transform(df_module):
    col = durations(df_module)
    encoder = DurationEncoder(scaling="standard")
    df_module.assert_frame_equal(encoder.fit_transform(col), encoder.transform(col))


def test_selector(df_module):
    df = df_module.make_dataframe({"d": [timedelta(days=1)], "x": [1.0], "s": ["a"]})
    assert s.duration().expand(df) == ["d"]
    assert repr(s.duration()) == "duration()"


def test_to_float_to_str_reject(df_module):
    with pytest.raises(RejectColumn):
        ToFloat().fit_transform(durations(df_module))
    with pytest.raises(RejectColumn):
        ToStr().fit_transform(durations(df_module))


def test_table_vectorizer(df_module):
    df = df_module.make_dataframe(
        {"d": [timedelta(days=1), timedelta(days=2, hours=3)], "x": [1.0, 2.0]}
    )
    vectorizer = TableVectorizer()
    out = vectorizer.fit_transform(df)
    assert vectorizer.kind_to_columns_["duration"] == ["d"]
    assert isinstance(vectorizer.transformers_["d"], DurationEncoder)
    assert sbd.column_names(out) == [
        "d_total_seconds",
        "d_days",
        "d_hours",
        "d_log1p_total_seconds",
        "x",
    ]
    out = TableVectorizer(duration="drop").fit_transform(df)
    assert sbd.column_names(out) == ["x"]
