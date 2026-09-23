import datetime

import numpy as np
import pandas as pd
import pytest

from skrub import DurationEncoder, TableVectorizer
from skrub import _dataframe as sbd
from skrub import selectors as s
from skrub._single_column_transformer import RejectColumn
from skrub._to_float import ToFloat
from skrub._to_str import ToStr


def _duration_column(df_module, name, values):
    return df_module.make_column(name, values)


def _values(frame, column):
    return np.asarray(sbd.to_numpy(sbd.col(frame, column)), dtype=np.float64)


def test_auto_resolution_levels(df_module):
    td = datetime.timedelta
    cases = [
        (
            [td(days=2), td(days=5)],
            "day",
            ["total_seconds", "days", "log1p_total_seconds"],
        ),
        (
            [td(days=1), td(hours=6)],
            "hour",
            ["total_seconds", "days", "hours", "log1p_total_seconds"],
        ),
        (
            [td(hours=1), td(minutes=30)],
            "minute",
            ["total_seconds", "days", "hours", "minutes", "log1p_total_seconds"],
        ),
        (
            [td(minutes=1), td(seconds=30)],
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
            [td(seconds=1), td(microseconds=5)],
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
    ]
    for values, resolution, components in cases:
        column = _duration_column(df_module, "gap", values)
        encoder = DurationEncoder().fit(column)
        assert encoder.resolution_ == resolution
        assert encoder.components_ == components
        assert encoder.get_feature_names_out() == [f"gap_{name}" for name in components]
        assert not hasattr(encoder, "scaling_params_")


def test_explicit_resolution_and_components(df_module):
    column = _duration_column(
        df_module,
        "gap",
        [datetime.timedelta(days=1, seconds=1), datetime.timedelta(hours=2)],
    )
    encoder = DurationEncoder(resolution="day").fit(column)
    assert encoder.resolution_ == "day"
    assert encoder.components_ == ["total_seconds", "days", "log1p_total_seconds"]

    encoder = DurationEncoder(
        components=("days", "sin_of_day"), resolution="microsecond"
    ).fit(column)
    assert encoder.resolution_ is None
    assert encoder.components_ == ["days", "sin_of_day"]
    assert encoder.get_feature_names_out() == ["gap_days", "gap_sin_of_day"]


def test_component_values_and_nulls(df_module):
    column = _duration_column(
        df_module,
        "gap",
        [
            datetime.timedelta(days=1, hours=2, minutes=3, seconds=4, microseconds=5),
            None,
            datetime.timedelta(0),
        ],
    )
    encoder = DurationEncoder(resolution="microsecond")
    out = encoder.fit_transform(column)
    assert _values(out, "gap_total_seconds")[0] == pytest.approx(
        86400 + 2 * 3600 + 3 * 60 + 4 + 5e-6
    )
    assert _values(out, "gap_days")[0] == pytest.approx(1)
    assert _values(out, "gap_hours")[0] == pytest.approx(2)
    assert _values(out, "gap_minutes")[0] == pytest.approx(3)
    assert _values(out, "gap_seconds")[0] == pytest.approx(4)
    assert _values(out, "gap_microseconds")[0] == pytest.approx(5)
    assert _values(out, "gap_log1p_total_seconds")[0] == pytest.approx(
        np.log1p(86400 + 2 * 3600 + 3 * 60 + 4 + 5e-6)
    )
    for name in encoder.all_outputs_:
        assert np.isnan(_values(out, name)[1])
        expected = 0.0 if "log1p" not in name else np.log1p(0)
        assert _values(out, name)[2] == pytest.approx(expected)


def test_negative_durations(df_module):
    column = _duration_column(
        df_module,
        "gap",
        [datetime.timedelta(seconds=-90), datetime.timedelta(days=-2)],
    )
    kept = DurationEncoder(resolution="second", handle_negative="keep").fit_transform(
        column
    )
    assert _values(kept, "gap_total_seconds") == pytest.approx([-90.0, -2 * 86400.0])
    assert _values(kept, "gap_days") == pytest.approx([-1.0, -2.0])
    assert _values(kept, "gap_hours") == pytest.approx([23.0, 0.0])
    assert _values(kept, "gap_minutes") == pytest.approx([58.0, 0.0])
    assert _values(kept, "gap_seconds") == pytest.approx([30.0, 0.0])

    clipped = DurationEncoder(
        resolution="second", handle_negative="clip"
    ).fit_transform(column)
    assert np.allclose(_values(clipped, "gap_total_seconds"), 0.0)
    assert np.allclose(_values(clipped, "gap_days"), 0.0)

    absolute = DurationEncoder(
        resolution="second", handle_negative="abs"
    ).fit_transform(column)
    assert _values(absolute, "gap_total_seconds") == pytest.approx([90.0, 2 * 86400.0])
    assert _values(absolute, "gap_days") == pytest.approx([0.0, 2.0])
    assert _values(absolute, "gap_minutes") == pytest.approx([1.0, 0.0])
    assert _values(absolute, "gap_seconds") == pytest.approx([30.0, 0.0])


def test_cyclical_components(df_module):
    column = _duration_column(
        df_module, "gap", [datetime.timedelta(hours=6), datetime.timedelta(seconds=-90)]
    )
    out = DurationEncoder(components=["sin_of_day", "cos_of_day"]).fit_transform(column)
    assert _values(out, "gap_sin_of_day")[0] == pytest.approx(1.0, abs=1e-5)
    assert _values(out, "gap_cos_of_day")[0] == pytest.approx(0.0, abs=1e-5)
    angle = 2 * np.pi * (-90) / 86400
    assert _values(out, "gap_sin_of_day")[1] == pytest.approx(np.sin(angle), abs=1e-5)
    assert _values(out, "gap_cos_of_day")[1] == pytest.approx(np.cos(angle), abs=1e-5)


def test_all_null_resolution_defaults_to_minute(df_module):
    if df_module.name == "pandas":
        column = pd.Series([pd.NaT, pd.NaT], dtype="timedelta64[ns]", name="gap")
    else:
        import polars as pl

        column = pl.Series("gap", [None, None], dtype=pl.Duration("us"))
    encoder = DurationEncoder().fit(column)
    assert encoder.resolution_ == "minute"
    out = encoder.transform(column)
    assert encoder.all_outputs_ == [
        "gap_total_seconds",
        "gap_days",
        "gap_hours",
        "gap_minutes",
        "gap_log1p_total_seconds",
    ]
    for name in encoder.all_outputs_:
        assert np.isnan(_values(out, name)).all()


def test_scaling(df_module):
    column = _duration_column(
        df_module,
        "gap",
        [
            datetime.timedelta(seconds=0),
            datetime.timedelta(seconds=10),
            datetime.timedelta(seconds=30),
        ],
    )
    encoder = DurationEncoder(components=["total_seconds"], scaling="minmax")
    out = encoder.fit_transform(column)
    assert encoder.scaling_params_["total_seconds"]["min"] == pytest.approx(0.0)
    assert encoder.scaling_params_["total_seconds"]["max"] == pytest.approx(30.0)
    assert _values(out, "gap_total_seconds") == pytest.approx([0.0, 10 / 30, 1.0])
    unseen = _duration_column(
        df_module,
        "gap",
        [datetime.timedelta(seconds=60), datetime.timedelta(seconds=-10)],
    )
    scaled = encoder.transform(unseen)
    assert _values(scaled, "gap_total_seconds") == pytest.approx([1.0, 0.0])

    standard = DurationEncoder(components=["total_seconds"], scaling="standard").fit(
        column
    )
    values = np.array([0.0, 10.0, 30.0])
    assert standard.scaling_params_["total_seconds"]["mean"] == pytest.approx(
        values.mean()
    )
    assert standard.scaling_params_["total_seconds"]["std"] == pytest.approx(
        values.std(ddof=0)
    )
    standardized = standard.transform(column)
    expected = (values - values.mean()) / values.std(ddof=0)
    assert _values(standardized, "gap_total_seconds") == pytest.approx(expected)

    robust = DurationEncoder(components=["total_seconds"], scaling="robust").fit(column)
    q25, median, q75 = np.percentile(values, [25, 50, 75])
    assert robust.scaling_params_["total_seconds"]["median"] == pytest.approx(median)
    assert robust.scaling_params_["total_seconds"]["iqr"] == pytest.approx(q75 - q25)
    robust_out = robust.transform(column)
    assert _values(robust_out, "gap_total_seconds") == pytest.approx(
        (values - median) / (q75 - q25)
    )


def test_constant_scaling_is_zero(df_module):
    column = _duration_column(
        df_module,
        "gap",
        [datetime.timedelta(days=1), datetime.timedelta(days=1), None],
    )
    for scaling in ("minmax", "standard", "robust"):
        encoder = DurationEncoder(components=["total_seconds"], scaling=scaling)
        out = encoder.fit_transform(column)
        values = _values(out, "gap_total_seconds")
        assert values[0] == pytest.approx(0.0)
        assert values[1] == pytest.approx(0.0)
        assert np.isnan(values[2])
        unseen = encoder.transform(
            _duration_column(df_module, "gap", [datetime.timedelta(days=4)])
        )
        assert _values(unseen, "gap_total_seconds")[0] == pytest.approx(0.0)


def test_invalid_parameters(df_module):
    column = _duration_column(df_module, "gap", [datetime.timedelta(days=1)])
    with pytest.raises(TypeError, match="components"):
        DurationEncoder(components=1).fit(column)
    with pytest.raises(ValueError, match="Unknown duration component"):
        DurationEncoder(components=["total_seconds", "not_a_component"]).fit(column)
    with pytest.raises(ValueError, match="resolution"):
        DurationEncoder(resolution="year").fit(column)
    with pytest.raises(ValueError, match="handle_negative"):
        DurationEncoder(handle_negative="drop").fit(column)
    with pytest.raises(ValueError, match="scaling"):
        DurationEncoder(scaling="maxabs").fit(column)
    with pytest.raises(RejectColumn, match="duration"):
        DurationEncoder().fit_transform(df_module.make_column("gap", [1.0, 2.0]))


def test_fit_transform_matches_fit_then_transform(df_module):
    column = _duration_column(
        df_module,
        "gap",
        [datetime.timedelta(hours=3), None, datetime.timedelta(days=2, minutes=1)],
    )
    encoder = DurationEncoder(scaling="standard")
    transformed = encoder.fit(column).transform(column)
    direct = DurationEncoder(scaling="standard").fit_transform(column)
    df_module.assert_frame_equal(transformed, direct)


def test_duration_selector(df_module):
    td = datetime.timedelta
    frame = df_module.make_dataframe(
        {
            "gap": [td(days=1), td(hours=2)],
            "when": ["2020-01-01", "2020-01-02"],
            "n": [1.0, 2.0],
        }
    )
    assert s.duration().expand(frame) == ["gap"]
    assert "gap" not in s.numeric().expand(frame)
    assert "gap" not in s.any_date().expand(frame)


def test_to_float_and_to_str_reject_duration(df_module):
    column = _duration_column(df_module, "gap", [datetime.timedelta(days=1), None])
    with pytest.raises(RejectColumn):
        ToFloat().fit_transform(column)
    with pytest.raises(RejectColumn):
        ToStr().fit_transform(column)


def test_table_vectorizer_routes_duration(df_module):
    frame = df_module.make_dataframe(
        {
            "gap": [datetime.timedelta(days=1), datetime.timedelta(hours=6), None],
            "n": [1.0, 2.0, 3.0],
            "city": ["paris", "lyon", "paris"],
        }
    )
    vectorizer = TableVectorizer()
    out = vectorizer.fit_transform(frame)
    assert vectorizer.column_to_kind_["gap"] == "duration"
    assert isinstance(vectorizer.transformers_["gap"], DurationEncoder)
    assert "gap_total_seconds" in sbd.column_names(out)
    assert "gap_days" in sbd.column_names(out)
    assert "gap_hours" in sbd.column_names(out)
    assert np.isnan(_values(out, "gap_total_seconds")[2])
    assert _values(out, "gap_days")[0] == pytest.approx(1.0)
    assert _values(out, "gap_hours")[1] == pytest.approx(6.0)

    custom = TableVectorizer(duration=DurationEncoder(components=["total_seconds"]))
    custom_out = custom.fit_transform(frame)
    gap_outputs = [
        name for name in sbd.column_names(custom_out) if name.startswith("gap_")
    ]
    assert gap_outputs == ["gap_total_seconds"]
