import math
from datetime import timedelta

import numpy as np
import pandas as pd
import pytest

from skrub import DurationEncoder
from skrub import _dataframe as sbd
from skrub._single_column_transformer import RejectColumn


def _column(df_module, name, values):
    return df_module.make_column(name, values)


def _values(col):
    if sbd.is_polars(col):
        import polars as pl

        return np.asarray(
            col.cast(pl.Float64).fill_null(float("nan")).to_numpy(), dtype=float
        )
    return np.asarray(col.to_numpy(), dtype=float)


def _frame_column(frame, name):
    return sbd.col(frame, name)


def test_import():
    import skrub

    assert skrub.DurationEncoder is DurationEncoder


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
def test_resolution_component_lists(resolution, expected, df_module):
    column = _column(df_module, "d", [timedelta(microseconds=1)])
    encoder = DurationEncoder(resolution=resolution).fit(column)
    assert encoder.resolution_ == resolution
    assert encoder.components_ == expected
    transformed = encoder.transform(column)
    assert sbd.column_names(transformed) == [f"d_{name}" for name in expected]
    assert encoder.get_feature_names_out() == [f"d_{name}" for name in expected]


def test_component_values_match_pandas_normalization(df_module):
    values = [
        timedelta(days=1, hours=2, minutes=3, seconds=4, microseconds=5),
        timedelta(hours=-3),
        timedelta(days=-1, microseconds=-1),
        timedelta(0),
        None,
    ]
    column = _column(df_module, "d", values)
    encoder = DurationEncoder(resolution="microsecond", handle_negative="keep")
    transformed = encoder.fit_transform(column)

    reference = pd.Series(values, name="d")
    components = reference.dt.components
    expected = {
        "total_seconds": reference.dt.total_seconds().to_numpy(),
        "days": components["days"].to_numpy(),
        "hours": components["hours"].to_numpy(),
        "minutes": components["minutes"].to_numpy(),
        "seconds": components["seconds"].to_numpy(),
        "microseconds": (
            components["milliseconds"] * 1000
            + components["microseconds"]
            + components["nanoseconds"] / 1000
        ).to_numpy(),
    }
    with np.errstate(invalid="ignore"):
        expected["log1p_total_seconds"] = np.log1p(
            reference.dt.total_seconds().to_numpy()
        )
    for name, expected_values in expected.items():
        got = _values(_frame_column(transformed, f"d_{name}"))
        np.testing.assert_allclose(got, expected_values, equal_nan=True)

    # Cyclical features are not part of any resolution.
    assert "sin_of_day" not in encoder.components_
    assert "cos_of_day" not in encoder.components_


def test_auto_resolution(df_module):
    whole_days = _column(df_module, "d", [timedelta(days=2), None, timedelta(days=5)])
    assert DurationEncoder().fit(whole_days).resolution_ == "day"

    hours = _column(df_module, "d", [timedelta(days=1), timedelta(hours=5)])
    assert DurationEncoder().fit(hours).resolution_ == "hour"

    minutes = _column(df_module, "d", [timedelta(hours=1), timedelta(minutes=30)])
    assert DurationEncoder().fit(minutes).resolution_ == "minute"

    seconds = _column(df_module, "d", [timedelta(minutes=1), timedelta(seconds=2)])
    assert DurationEncoder().fit(seconds).resolution_ == "second"

    microseconds = _column(
        df_module, "d", [timedelta(seconds=1), timedelta(microseconds=1)]
    )
    assert DurationEncoder().fit(microseconds).resolution_ == "microsecond"


def test_auto_resolution_all_null(df_module):
    if df_module.name == "pandas":
        column = pd.Series([pd.NaT, pd.NaT], dtype="timedelta64[ns]", name="d")
    else:
        import polars as pl

        column = pl.Series("d", [None, None], dtype=pl.Duration("us"))
    encoder = DurationEncoder().fit(column)
    assert encoder.resolution_ == "minute"
    transformed = encoder.transform(column)
    for name in sbd.column_names(transformed):
        assert sbd.is_all_null(_frame_column(transformed, name))


def test_explicit_components_ignore_resolution(df_module):
    column = _column(df_module, "d", [timedelta(days=1, minutes=1)])
    encoder = DurationEncoder(
        components=("seconds", "total_seconds"), resolution="not-a-resolution"
    )
    transformed = encoder.fit_transform(column)
    assert encoder.resolution_ is None
    assert encoder.components_ == ["seconds", "total_seconds"]
    assert sbd.column_names(transformed) == ["d_seconds", "d_total_seconds"]
    np.testing.assert_allclose(
        _values(_frame_column(transformed, "d_total_seconds")), [86460.0]
    )


def test_components_validation(df_module):
    column = _column(df_module, "d", [timedelta(days=1)])
    with pytest.raises(TypeError, match="list or tuple"):
        DurationEncoder(components=1).fit(column)
    with pytest.raises(TypeError, match="list or tuple"):
        DurationEncoder(components="days").fit(column)
    with pytest.raises(ValueError, match="Unknown duration component"):
        DurationEncoder(components=["days", "fortnights"]).fit(column)
    with pytest.raises(ValueError, match="resolution must be"):
        DurationEncoder(resolution="year").fit(column)
    with pytest.raises(ValueError, match="handle_negative"):
        DurationEncoder(handle_negative="drop").fit(column)
    with pytest.raises(ValueError, match="scaling"):
        DurationEncoder(scaling="maxabs").fit(column)


def test_handle_negative(df_module):
    column = _column(df_module, "d", [timedelta(hours=-3), None])
    kept = DurationEncoder(
        components=["total_seconds", "days", "hours"], handle_negative="keep"
    ).fit_transform(column)
    np.testing.assert_allclose(
        _values(_frame_column(kept, "d_total_seconds")), [-10800.0, np.nan]
    )
    np.testing.assert_allclose(_values(_frame_column(kept, "d_days")), [-1.0, np.nan])
    np.testing.assert_allclose(_values(_frame_column(kept, "d_hours")), [21.0, np.nan])

    absolute = DurationEncoder(
        components=["total_seconds", "hours"], handle_negative="abs"
    ).fit_transform(column)
    np.testing.assert_allclose(
        _values(_frame_column(absolute, "d_total_seconds")), [10800.0, np.nan]
    )
    np.testing.assert_allclose(
        _values(_frame_column(absolute, "d_hours")), [3.0, np.nan]
    )

    clipped = DurationEncoder(
        components=["total_seconds"], handle_negative="clip"
    ).fit_transform(column)
    np.testing.assert_allclose(
        _values(_frame_column(clipped, "d_total_seconds")), [0.0, np.nan]
    )


def test_nulls_propagate_to_every_output(df_module):
    column = _column(df_module, "d", [timedelta(days=1), None, timedelta(hours=2)])
    transformed = DurationEncoder(resolution="hour").fit_transform(column)
    for name in sbd.column_names(transformed):
        nulls = sbd.to_numpy(sbd.is_null(_frame_column(transformed, name)))
        assert list(nulls) == [False, True, False]


def test_log1p_and_cyclical(df_module):
    column = _column(
        df_module,
        "d",
        [timedelta(0), timedelta(seconds=math.e - 1), timedelta(hours=6)],
    )
    transformed = DurationEncoder(
        components=["log1p_total_seconds", "sin_of_day", "cos_of_day"]
    ).fit_transform(column)
    np.testing.assert_allclose(
        _values(_frame_column(transformed, "d_log1p_total_seconds")),
        [0.0, 1.0, np.log1p(6 * 3600)],
    )
    np.testing.assert_allclose(
        _values(_frame_column(transformed, "d_sin_of_day")),
        [0.0, np.sin(2 * np.pi * (math.e - 1) / 86400), 1.0],
        atol=1e-12,
    )
    np.testing.assert_allclose(
        _values(_frame_column(transformed, "d_cos_of_day")),
        [1.0, np.cos(2 * np.pi * (math.e - 1) / 86400), 0.0],
        atol=1e-12,
    )


def test_scaling_minmax_clips_unseen(df_module):
    train = _column(df_module, "d", [timedelta(seconds=10), timedelta(seconds=20)])
    encoder = DurationEncoder(components=["total_seconds"], scaling="minmax").fit(train)
    assert encoder.scaling_params_["total_seconds"]["min"] == 10.0
    assert encoder.scaling_params_["total_seconds"]["max"] == 20.0
    transformed = encoder.transform(
        _column(
            df_module,
            "d",
            [timedelta(seconds=0), timedelta(seconds=15), timedelta(seconds=40)],
        )
    )
    np.testing.assert_allclose(
        _values(_frame_column(transformed, "d_total_seconds")), [0.0, 0.5, 1.0]
    )


def test_scaling_standard_and_robust(df_module):
    train = _column(
        df_module,
        "d",
        [
            timedelta(seconds=0),
            timedelta(seconds=10),
            timedelta(seconds=20),
            timedelta(seconds=30),
        ],
    )
    values = np.array([0.0, 10.0, 20.0, 30.0])

    standard = DurationEncoder(components=["total_seconds"], scaling="standard").fit(
        train
    )
    params = standard.scaling_params_["total_seconds"]
    assert params["mean"] == pytest.approx(float(np.mean(values)))
    assert params["std"] == pytest.approx(float(np.std(values, ddof=0)))
    scaled = _values(_frame_column(standard.transform(train), "d_total_seconds"))
    np.testing.assert_allclose(scaled, (values - params["mean"]) / params["std"])

    robust = DurationEncoder(components=["total_seconds"], scaling="robust").fit(train)
    q25, q75 = np.percentile(values, [25, 75])
    robust_params = robust.scaling_params_["total_seconds"]
    assert robust_params["median"] == pytest.approx(float(np.median(values)))
    assert robust_params["iqr"] == pytest.approx(float(q75 - q25))
    robust_scaled = _values(_frame_column(robust.transform(train), "d_total_seconds"))
    np.testing.assert_allclose(
        robust_scaled,
        (values - robust_params["median"]) / robust_params["iqr"],
    )


def test_scaling_constant_is_zero_and_nulls_stay_null(df_module):
    column = _column(df_module, "d", [timedelta(seconds=5), None, timedelta(seconds=5)])
    for method in ("minmax", "standard", "robust"):
        encoder = DurationEncoder(components=["total_seconds", "days"], scaling=method)
        transformed = encoder.fit_transform(column)
        assert set(encoder.scaling_params_) == {"total_seconds", "days"}
        got = _values(_frame_column(transformed, "d_total_seconds"))
        np.testing.assert_allclose(got, [0.0, np.nan, 0.0])


def test_scaling_params_absent_without_scaling(df_module):
    column = _column(df_module, "d", [timedelta(seconds=1), timedelta(seconds=2)])
    encoder = DurationEncoder(scaling="minmax").fit(column)
    assert hasattr(encoder, "scaling_params_")
    encoder.set_params(scaling=None)
    encoder.fit(column)
    assert not hasattr(encoder, "scaling_params_")


def test_reject_non_duration(df_module):
    with pytest.raises(RejectColumn, match="duration dtype"):
        DurationEncoder().fit_transform(df_module.make_column("n", [1, 2, 3]))
    with pytest.raises(RejectColumn, match="duration dtype"):
        DurationEncoder().fit_transform(df_module.example_column)


def test_pandas_index_is_preserved():
    column = pd.Series(
        [timedelta(days=1), None], index=pd.Index([10, 11], name="row"), name="d"
    )
    transformed = DurationEncoder(resolution="day").fit_transform(column)
    assert list(transformed.index) == [10, 11]
    assert transformed.index.name == "row"


def test_duration_too_large_for_nanoseconds():
    column = pd.Series(pd.to_timedelta([10**12], unit="s"), name="d")
    with pytest.raises(ValueError, match="too large"):
        DurationEncoder().fit_transform(column)
