from __future__ import annotations

import random
from typing import Any

import hypothesis.strategies as st
import pytest
from hypothesis import HealthCheck, given, settings

import narwhals as nw
from tests.utils import (
    DUCKDB_VERSION,
    PANDAS_VERSION,
    POLARS_VERSION,
    Constructor,
    ConstructorEager,
    assert_equal_data,
)

pytest.importorskip("pandas")
import pandas as pd

data = {"a": [1.0, 2.0, 1.0, 3.0, 1.0, 4.0, 1.0]}

kwargs_and_expected = (
    {
        "name": "x1",
        "kwargs": {"window_size": 3, "quantile": 0.25},
        "expected": [None, None, 1.0, 1.5, 1.0, 2.0, 1.0],
    },
    {
        "name": "x2",
        "kwargs": {"window_size": 3, "quantile": 0.25, "min_samples": 1},
        "expected": [1.0, 1.25, 1.0, 1.5, 1.0, 2.0, 1.0],
    },
    {
        "name": "x3",
        "kwargs": {"window_size": 2, "quantile": 0.25, "min_samples": 1},
        "expected": [1.0, 1.25, 1.25, 1.5, 1.5, 1.75, 1.75],
    },
    {
        "name": "x4",
        "kwargs": {"window_size": 4, "quantile": 0.25, "min_samples": 1, "center": True},
        "expected": [1.25, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0],
    },
    {
        "name": "x5",
        "kwargs": {
            "window_size": 3,
            "quantile": 0.75,
            "interpolation": "higher",
            "min_samples": 1,
        },
        "expected": [1.0, 2.0, 2.0, 3.0, 3.0, 4.0, 4.0],
    },
    {
        "name": "x6",
        "kwargs": {
            "window_size": 5,
            "quantile": 0.75,
            "interpolation": "higher",
            "min_samples": 1,
            "center": True,
        },
        "expected": [2.0, 3.0, 2.0, 3.0, 3.0, 4.0, 4.0],
    },
    {
        "name": "x7",
        "kwargs": {"window_size": 3, "quantile": 0.25, "interpolation": "lower"},
        "expected": [None, None, 1.0, 1.0, 1.0, 1.0, 1.0],
    },
    {
        "name": "x8",
        "kwargs": {
            "window_size": 2,
            "quantile": 0.25,
            "interpolation": "midpoint",
            "min_samples": 1,
        },
        "expected": [1.0, 1.5, 1.5, 2.0, 2.0, 2.5, 2.5],
    },
    {
        "name": "x9",
        "kwargs": {
            "window_size": 3,
            "quantile": 0.9,
            "interpolation": "nearest",
            "min_samples": 1,
        },
        "expected": [1.0, 2.0, 2.0, 3.0, 3.0, 4.0, 4.0],
    },
)


@pytest.mark.parametrize("kwargs_and_expected", kwargs_and_expected)
def test_rolling_quantile_expr(
    constructor_eager: ConstructorEager, kwargs_and_expected: dict[str, Any]
) -> None:
    name = kwargs_and_expected["name"]
    kwargs = kwargs_and_expected["kwargs"]
    expected = kwargs_and_expected["expected"]

    df = nw.from_native(constructor_eager(data))
    result = df.select(nw.col("a").rolling_quantile(**kwargs).alias(name))

    assert_equal_data(result, {name: expected})


@pytest.mark.parametrize("kwargs_and_expected", kwargs_and_expected)
def test_rolling_quantile_series(
    constructor_eager: ConstructorEager, kwargs_and_expected: dict[str, Any]
) -> None:
    name = kwargs_and_expected["name"]
    kwargs = kwargs_and_expected["kwargs"]
    expected = kwargs_and_expected["expected"]

    df = nw.from_native(constructor_eager(data), eager_only=True)
    result = df.select(df["a"].rolling_quantile(**kwargs).alias(name))

    assert_equal_data(result, {name: expected})


@pytest.mark.parametrize("quantile", [-0.1, 1.1])
def test_rolling_quantile_invalid_quantile(quantile: float) -> None:
    with pytest.raises(ValueError, match=r"Quantile must be between 0\.0 and 1\.0"):
        nw.col("a").rolling_quantile(3, quantile=quantile)


def test_rolling_quantile_invalid_interpolation() -> None:
    with pytest.raises(ValueError, match="Interpolation must be one of"):
        nw.col("a").rolling_quantile(3, quantile=0.5, interpolation="cubic")  # type: ignore[arg-type]


def test_rolling_quantile_series_invalid_arguments(
    constructor_eager: ConstructorEager,
) -> None:
    s = nw.from_native(constructor_eager(data), eager_only=True)["a"]
    with pytest.raises(ValueError, match=r"Quantile must be between 0\.0 and 1\.0"):
        s.rolling_quantile(3, quantile=2.0)
    with pytest.raises(ValueError, match="Interpolation must be one of"):
        s.rolling_quantile(3, quantile=0.5, interpolation="cubic")  # type: ignore[arg-type]


@given(
    center=st.booleans(),
    quantile=st.floats(0, 1),
    interpolation=st.sampled_from(["linear", "lower", "higher", "midpoint"]),
    values=st.lists(st.floats(-10, 10), min_size=5, max_size=10),
)
@settings(suppress_health_check=[HealthCheck.too_slow])
@pytest.mark.slow
@pytest.mark.filterwarnings("ignore:.*is_sparse is deprecated:DeprecationWarning")
def test_rolling_quantile_hypothesis(
    center: bool,  # noqa: FBT001
    quantile: float,
    interpolation: Any,
    values: list[float],
) -> None:
    pytest.importorskip("pyarrow")
    import pyarrow as pa

    s = pd.Series(values)
    window_size = random.randint(1, len(s))  # noqa: S311
    min_samples = random.randint(1, window_size)  # noqa: S311
    mask = random.sample(range(len(s)), 2)

    s[mask] = None
    df = pd.DataFrame({"a": s})
    expected = (
        s.rolling(window=window_size, center=center, min_periods=min_samples)
        .quantile(quantile, interpolation=interpolation)
        .to_frame("a")
    )

    result = nw.from_native(pa.Table.from_pandas(df)).select(
        nw.col("a").rolling_quantile(
            window_size,
            quantile=quantile,
            interpolation=interpolation,
            center=center,
            min_samples=min_samples,
        )
    )
    expected_dict = nw.from_native(expected, eager_only=True).to_dict(as_series=False)
    assert_equal_data(result, expected_dict)


def _skip_or_xfail_lazy(constructor: Constructor, request: pytest.FixtureRequest) -> None:
    if "polars" in str(constructor) and POLARS_VERSION < (1, 10):
        pytest.skip()
    if "modin" in str(constructor):
        # unreliable
        pytest.skip()
    if any(x in str(constructor) for x in ("duckdb", "pyspark", "sqlframe")):
        # DuckDB does not support `percentile_cont` as a windowed aggregate function,
        # and there is no quantile implementation for spark-like backends.
        request.applymarker(pytest.mark.xfail(raises=NotImplementedError))


@pytest.mark.parametrize(
    ("expected_a", "window_size", "min_samples", "center"),
    [
        ([None, None, 1.25, None, None, 4.5, 7.25], 2, None, False),
        ([None, None, 1.25, 1.25, 2.5, 4.5, 5.0], 3, 2, False),
        ([1.0, None, 1.25, 1.25, 2.5, 4.5, 5.0], 3, 1, False),
        ([1.25, 1.0, 1.25, 2.5, 4.5, 5.0, 7.25], 3, 1, True),
        ([1.25, 1.0, 1.25, 1.5, 3.0, 5.0, 5.0], 4, 1, True),
    ],
)
def test_rolling_quantile_expr_lazy_ungrouped(
    constructor: Constructor,
    expected_a: list[float],
    window_size: int,
    min_samples: int,
    request: pytest.FixtureRequest,
    *,
    center: bool,
) -> None:
    _skip_or_xfail_lazy(constructor, request)
    data = {
        "a": [1, None, 2, None, 4, 6, 11],
        "b": [1, None, 2, 3, 4, 5, 6],
        "i": list(range(7)),
    }
    df = nw.from_native(constructor(data))
    result = (
        df.with_columns(
            nw.col("a")
            .rolling_quantile(
                window_size, quantile=0.25, min_samples=min_samples, center=center
            )
            .over(order_by="b")
        )
        .select("a", "i")
        .sort("i")
    )
    expected = {"a": expected_a, "i": list(range(7))}
    assert_equal_data(result, expected)


@pytest.mark.parametrize(
    ("expected_a", "window_size", "min_samples", "center"),
    [
        ([None, None, 1.25, None, None, 4.5, 7.25], 2, None, False),
        ([None, None, 1.25, 1.25, None, 4.5, 5.0], 3, 2, False),
        ([1.0, None, 1.25, 1.25, 4.0, 4.5, 5.0], 3, 1, False),
        ([1.25, 1.0, 1.25, 2.0, 4.5, 5.0, 7.25], 3, 1, True),
        ([1.25, 1.0, 1.25, 1.25, 4.5, 5.0, 5.0], 4, 1, True),
    ],
)
def test_rolling_quantile_expr_lazy_grouped(
    constructor: Constructor,
    expected_a: list[float],
    window_size: int,
    min_samples: int,
    request: pytest.FixtureRequest,
    *,
    center: bool,
) -> None:
    _skip_or_xfail_lazy(constructor, request)
    if "pandas" in str(constructor) and PANDAS_VERSION < (1, 2):
        pytest.skip()
    if any(x in str(constructor) for x in ("dask", "pyarrow_table")):
        request.applymarker(pytest.mark.xfail)
    data = {
        "a": [1, None, 2, None, 4, 6, 11],
        "g": [1, 1, 1, 1, 2, 2, 2],
        "b": [1, None, 2, 3, 4, 5, 6],
        "i": list(range(7)),
    }
    df = nw.from_native(constructor(data))
    result = (
        df.with_columns(
            nw.col("a")
            .rolling_quantile(
                window_size, quantile=0.25, min_samples=min_samples, center=center
            )
            .over("g", order_by="b")
        )
        .sort("i")
        .select("a")
    )
    expected = {"a": expected_a}
    assert_equal_data(result, expected)


def test_rolling_quantile_duckdb_raises() -> None:
    pytest.importorskip("duckdb")
    import duckdb

    if DUCKDB_VERSION < (1, 3):
        pytest.skip()
    df = nw.from_native(duckdb.sql("select 1.0 as a, 0 as i"))
    with pytest.raises(NotImplementedError, match="percentile_cont"):
        df.with_columns(nw.col("a").rolling_quantile(2, quantile=0.5).over(order_by="i"))
