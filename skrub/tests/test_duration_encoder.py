from datetime import timedelta

import numpy as np
import pytest

from skrub import DurationEncoder, TableVectorizer
from skrub import _dataframe as sbd
from skrub import selectors as s
from skrub._single_column_transformer import RejectColumn
from skrub._to_float import ToFloat
from skrub._to_str import ToStr


def _duration_col(df_module, values, name="elapsed"):
    if df_module.name == "polars":
        import polars as pl

        return pl.Series(name, values, dtype=pl.Duration)
    import pandas as pd

    return pd.Series(values, name=name, dtype="timedelta64[ns]")


def test_reject_non_duration(df_module):
    col = df_module.make_column("x", [1, 2, 3])
    with pytest.raises(RejectColumn):
        DurationEncoder().fit_transform(col)


def test_basic_extraction(df_module):
    col = _duration_col(
        df_module,
        [timedelta(days=1, hours=2, minutes=30), None, timedelta(hours=3)],
    )
    enc = DurationEncoder(resolution="minute")
    result = enc.fit_transform(col)
    assert enc.components_ == [
        "total_seconds",
        "days",
        "hours",
        "minutes",
        "log1p_total_seconds",
    ]
    assert enc.get_feature_names_out() == [
        "elapsed_total_seconds",
        "elapsed_days",
        "elapsed_hours",
        "elapsed_minutes",
        "elapsed_log1p_total_seconds",
    ]
    assert sbd.to_list(result["elapsed_days"])[:1] == [1.0]
    assert sbd.to_list(result["elapsed_days"])[2] == 0.0
    assert sbd.is_null(result["elapsed_days"])[1]
    assert sbd.to_list(result["elapsed_hours"])[:1] == [2.0]
    assert sbd.to_list(result["elapsed_hours"])[2] == 3.0
    assert sbd.is_null(result["elapsed_hours"])[1]


def test_resolution_auto_whole_days(df_module):
    col = _duration_col(df_module, [timedelta(days=2), timedelta(days=5)])
    enc = DurationEncoder()
    enc.fit_transform(col)
    assert enc.resolution_ == "day"
    assert enc.components_ == ["total_seconds", "days", "log1p_total_seconds"]


def test_resolution_auto_all_null(df_module):
    if df_module.name == "pandas":
        import pandas as pd

        col = pd.Series([pd.NaT, pd.NaT], name="elapsed", dtype="timedelta64[ns]")
    else:
        import polars as pl

        col = pl.Series("elapsed", [None, None], dtype=pl.Duration)
    enc = DurationEncoder()
    enc.fit_transform(col)
    assert enc.resolution_ == "minute"


def test_explicit_components_ignore_resolution(df_module):
    col = _duration_col(df_module, [timedelta(days=1)])
    enc = DurationEncoder(components=["days", "total_seconds"], resolution="microsecond")
    enc.fit_transform(col)
    assert enc.components_ == ["total_seconds", "days"]


def test_cyclical_components(df_module):
    col = _duration_col(df_module, [timedelta(hours=6)])
    enc = DurationEncoder(components=["sin_of_day", "cos_of_day"])
    result = enc.fit_transform(col)
    assert result.shape[1] == 2
    assert np.isclose(sbd.to_list(result["elapsed_sin_of_day"])[0], 1.0)
    assert np.isclose(sbd.to_list(result["elapsed_cos_of_day"])[0], 0.0)


@pytest.mark.parametrize("handle_negative,expected", [
    ("clip", [0.0, 3600.0]),
    ("abs", [3600.0, 3600.0]),
    ("keep", [-3600.0, 3600.0]),
])
def test_handle_negative(df_module, handle_negative, expected):
    col = _duration_col(
        df_module, [timedelta(hours=-1), timedelta(hours=1)]
    )
    enc = DurationEncoder(
        components=["total_seconds"], handle_negative=handle_negative
    )
    result = enc.fit_transform(col)
    assert sbd.to_list(result["elapsed_total_seconds"]) == expected


def test_scaling_minmax(df_module):
    col = _duration_col(
        df_module, [timedelta(hours=1), timedelta(hours=3), timedelta(hours=5)]
    )
    enc = DurationEncoder(components=["total_seconds"], scaling="minmax")
    result = enc.fit_transform(col)
    assert enc.scaling_params_["total_seconds"]["min"] == 3600.0
    assert enc.scaling_params_["total_seconds"]["max"] == 18000.0
    assert sbd.to_list(result["elapsed_total_seconds"]) == [0.0, 0.5, 1.0]
    outlier = enc.transform(
        _duration_col(df_module, [timedelta(hours=10)])
    )
    assert sbd.to_list(outlier["elapsed_total_seconds"]) == [1.0]


def test_scaling_constant_column(df_module):
    col = _duration_col(df_module, [timedelta(hours=2), timedelta(hours=2)])
    enc = DurationEncoder(components=["total_seconds"], scaling="standard")
    result = enc.fit_transform(col)
    assert sbd.to_list(result["elapsed_total_seconds"]) == [0.0, 0.0]


def test_invalid_components_type(df_module):
    col = _duration_col(df_module, [timedelta(hours=1)])
    with pytest.raises(TypeError, match="components"):
        DurationEncoder(components=3).fit_transform(col)


def test_invalid_component_name(df_module):
    col = _duration_col(df_module, [timedelta(hours=1)])
    with pytest.raises(ValueError, match="Unknown component"):
        DurationEncoder(components=["not_a_component"]).fit_transform(col)


def test_to_float_rejects_duration(df_module):
    col = _duration_col(df_module, [timedelta(hours=1)])
    with pytest.raises(RejectColumn):
        ToFloat().fit_transform(col)


def test_to_str_rejects_duration(df_module):
    col = _duration_col(df_module, [timedelta(hours=1)])
    with pytest.raises(RejectColumn):
        ToStr().fit_transform(col)


def test_duration_selector(df_module):
    df = df_module.make_dataframe(
        {
            "delta": [timedelta(hours=1), timedelta(hours=2), timedelta(hours=3)],
            "x": [1, 2, 3],
        }
    )
    selected = s.select(df, s.duration())
    assert sbd.column_names(selected) == ["delta"]


def test_table_vectorizer_routes_duration(df_module):
    col = _duration_col(df_module, [timedelta(days=1), timedelta(days=2)])
    df = df_module.make_dataframe({"elapsed": col, "x": [1.0, 2.0]})
    vec = TableVectorizer()
    result = vec.fit_transform(df)
    assert vec.column_to_kind_["elapsed"] == "duration"
    assert "elapsed_days" in sbd.column_names(result)
