from datetime import timedelta

import numpy as np
import pandas as pd
import pytest

from skrub import DurationEncoder, TableVectorizer
from skrub import _dataframe as sbd
from skrub import selectors as s
from skrub._single_column_transformer import RejectColumn
from skrub._to_float import ToFloat
from skrub._to_str import ToStr


def _series(df_module, name, values):
    return df_module.make_column(name, values)


def test_resolution_levels(df_module):
    col = _series(
        df_module,
        "wait",
        [
            timedelta(days=1, hours=2, minutes=3, seconds=4, microseconds=5),
            None,
        ],
    )
    expected = {
        "day": ["total_seconds", "days", "log1p_total_seconds"],
        "hour": ["total_seconds", "days", "hours", "log1p_total_seconds"],
        "minute": [
            "total_seconds",
            "days",
            "hours",
            "minutes",
            "log1p_total_seconds",
        ],
        "second": [
            "total_seconds",
            "days",
            "hours",
            "minutes",
            "seconds",
            "log1p_total_seconds",
        ],
        "microsecond": [
            "total_seconds",
            "days",
            "hours",
            "minutes",
            "seconds",
            "microseconds",
            "log1p_total_seconds",
        ],
    }
    for resolution, components in expected.items():
        enc = DurationEncoder(resolution=resolution)
        out = enc.fit_transform(col)
        assert enc.resolution_ == resolution
        assert enc.components_ == components
        assert list(sbd.column_names(out)) == [f"wait_{c}" for c in components]
        assert enc.get_feature_names_out() == [f"wait_{c}" for c in components]
        hours = None
        if resolution != "day":
            hours = sbd.to_numpy(sbd.col(out, "wait_hours"))
        if hours is not None:
            assert hours[0] == pytest.approx(2.0)
            assert np.isnan(hours[1])
        days = sbd.to_numpy(sbd.col(out, "wait_days"))
        assert days[0] == pytest.approx(1.0)
        assert np.isnan(days[1])
        assert not hasattr(enc, "scaling_params_")


def test_auto_resolution(df_module):
    cases = {
        "day": [timedelta(days=2), timedelta(days=5)],
        "hour": [timedelta(hours=2), timedelta(days=1, hours=3)],
        "minute": [timedelta(minutes=5), timedelta(hours=1, minutes=1)],
        "second": [timedelta(seconds=30), timedelta(minutes=1, seconds=1)],
        "microsecond": [timedelta(microseconds=10), timedelta(seconds=1)],
    }
    for resolution, values in cases.items():
        col = _series(df_module, "d", values)
        enc = DurationEncoder().fit(col)
        assert enc.resolution_ == resolution
        assert enc.components_ == [
            "total_seconds",
            "days",
            *{
                "day": [],
                "hour": ["hours"],
                "minute": ["hours", "minutes"],
                "second": ["hours", "minutes", "seconds"],
                "microsecond": ["hours", "minutes", "seconds", "microseconds"],
            }[resolution],
            "log1p_total_seconds",
        ]


def test_auto_resolution_all_null(df_module):
    col = _series(df_module, "d", [None, None])
    # pandas may not infer a timedelta dtype from None alone
    if df_module.name == "pandas":
        col = pd.Series([pd.NaT, pd.NaT], dtype="timedelta64[ns]", name="d")
    else:
        import polars as pl

        col = pl.Series("d", [None, None], dtype=pl.Duration("us"))
    enc = DurationEncoder().fit(col)
    assert enc.resolution_ == "minute"


def test_explicit_components_ignore_resolution(df_module):
    col = _series(df_module, "d", [timedelta(hours=6), timedelta(days=-1, seconds=-1)])
    enc = DurationEncoder(
        components=["sin_of_day", "cos_of_day", "total_seconds"],
        resolution="day",
    )
    out = enc.fit_transform(col)
    assert enc.resolution_ is None
    assert enc.components_ == ["sin_of_day", "cos_of_day", "total_seconds"]
    assert list(sbd.column_names(out)) == [
        "d_sin_of_day",
        "d_cos_of_day",
        "d_total_seconds",
    ]
    # 6 hours is a quarter day
    assert sbd.to_numpy(sbd.col(out, "d_sin_of_day"))[0] == pytest.approx(1.0)
    assert sbd.to_numpy(sbd.col(out, "d_cos_of_day"))[0] == pytest.approx(0.0, abs=1e-6)


def test_components_type_errors():
    with pytest.raises(TypeError):
        DurationEncoder(components=1).fit(pd.to_timedelta(pd.Series([1]), unit="D"))
    with pytest.raises(ValueError):
        DurationEncoder(components=["days", "fortnight"]).fit(
            pd.to_timedelta(pd.Series([1]), unit="D")
        )


def test_handle_negative(df_module):
    col = _series(df_module, "d", [timedelta(hours=-3), timedelta(hours=2)])
    kept = DurationEncoder(resolution="hour", handle_negative="keep").fit_transform(col)
    clipped = DurationEncoder(resolution="hour", handle_negative="clip").fit_transform(
        col
    )
    absolute = DurationEncoder(resolution="hour", handle_negative="abs").fit_transform(
        col
    )
    assert sbd.to_numpy(sbd.col(kept, "d_total_seconds"))[0] == pytest.approx(-3 * 3600)
    assert sbd.to_numpy(sbd.col(clipped, "d_total_seconds"))[0] == pytest.approx(0.0)
    assert sbd.to_numpy(sbd.col(absolute, "d_total_seconds"))[0] == pytest.approx(
        3 * 3600
    )
    # negative timedelta normalizes to -1 day + 21 hours
    assert sbd.to_numpy(sbd.col(kept, "d_days"))[0] == pytest.approx(-1.0)
    assert sbd.to_numpy(sbd.col(kept, "d_hours"))[0] == pytest.approx(21.0)


def test_scaling(df_module):
    col = _series(df_module, "d", [timedelta(seconds=0), timedelta(seconds=10)])
    enc = DurationEncoder(components=["total_seconds"], scaling="minmax").fit(col)
    out = enc.transform(col)
    values = sbd.to_numpy(sbd.col(out, "d_total_seconds"))
    assert values[0] == pytest.approx(0.0)
    assert values[1] == pytest.approx(1.0)
    assert "min" in enc.scaling_params_["total_seconds"]
    unseen = _series(df_module, "d", [timedelta(seconds=-10), timedelta(seconds=30)])
    scaled = sbd.to_numpy(sbd.col(enc.transform(unseen), "d_total_seconds"))
    assert scaled[0] == pytest.approx(0.0)
    assert scaled[1] == pytest.approx(1.0)

    standard = DurationEncoder(components=["total_seconds"], scaling="standard").fit(
        col
    )
    std_values = sbd.to_numpy(sbd.col(standard.transform(col), "d_total_seconds"))
    assert std_values.mean() == pytest.approx(0.0, abs=1e-5)

    robust = DurationEncoder(components=["total_seconds"], scaling="robust").fit(col)
    robust_values = sbd.to_numpy(sbd.col(robust.transform(col), "d_total_seconds"))
    assert robust_values[0] < 0
    assert robust_values[1] > 0

    constant = _series(df_module, "d", [timedelta(seconds=4), timedelta(seconds=4)])
    for scaling in ("minmax", "standard", "robust"):
        encoded = DurationEncoder(components=["total_seconds"], scaling=scaling)
        result = sbd.to_numpy(
            sbd.col(encoded.fit_transform(constant), "d_total_seconds")
        )
        assert result[0] == pytest.approx(0.0)
        assert result[1] == pytest.approx(0.0)


def test_nulls_propagate_and_reject(df_module):
    col = _series(df_module, "d", [timedelta(days=1), None])
    out = DurationEncoder(resolution="day").fit_transform(col)
    for name in sbd.column_names(out):
        values = sbd.to_numpy(sbd.col(out, name))
        assert np.isnan(values[1])
    with pytest.raises(RejectColumn):
        DurationEncoder().fit_transform(df_module.example_column)


def test_to_float_and_to_str_reject_duration(df_module):
    col = _series(df_module, "d", [timedelta(days=1)])
    with pytest.raises(RejectColumn):
        ToFloat().fit_transform(col)
    with pytest.raises(RejectColumn):
        ToStr().fit_transform(col)


def test_duration_selector(df_module):
    duration_col = _series(df_module, "elapsed", [timedelta(days=1)])
    other = _series(df_module, "count", [1])
    df = sbd.make_dataframe_like(duration_col, [duration_col, other])
    assert s.duration().expand(df) == ["elapsed"]
    assert "elapsed" not in s.numeric().expand(df)


def test_table_vectorizer_routes_duration(df_module):
    duration_col = _series(
        df_module, "elapsed", [timedelta(days=2), timedelta(hours=5)]
    )
    numeric = _series(df_module, "count", [1.0, 2.0])
    df = sbd.make_dataframe_like(duration_col, [duration_col, numeric])
    vectorizer = TableVectorizer()
    out = vectorizer.fit_transform(df)
    assert vectorizer.column_to_kind_["elapsed"] == "duration"
    assert isinstance(vectorizer.transformers_["elapsed"], DurationEncoder)
    assert "elapsed_total_seconds" in sbd.column_names(out)
    assert "elapsed_days" in sbd.column_names(out)
    assert np.isfinite(sbd.to_numpy(sbd.col(out, "elapsed_total_seconds"))).all()
