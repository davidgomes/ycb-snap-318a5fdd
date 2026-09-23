from __future__ import annotations

from typing import Any

import pytest

import narwhals as nw
from narwhals.exceptions import InvalidOperationError
from tests.utils import (
    DUCKDB_VERSION,
    POLARS_VERSION,
    Constructor,
    ConstructorEager,
    assert_equal_data,
)

data = {"a": [None, 1, 2, None, 4, 6, 11]}

kwargs_and_expected: dict[str, dict[str, Any]] = {
    "x1": {"kwargs": {"window_size": 3}, "expected": [None] * 6 + [4]},
    "x2": {
        "kwargs": {"window_size": 3, "min_samples": 1},
        "expected": [None, 1, 1, 1, 2, 4, 4],
    },
    "x3": {
        "kwargs": {"window_size": 2, "min_samples": 1},
        "expected": [None, 1, 1, 2, 4, 4, 6],
    },
    "x4": {
        "kwargs": {"window_size": 5, "min_samples": 1, "center": True},
        "expected": [1, 1, 1, 1, 2, 4, 4],
    },
}


def test_rolling_min_expr(constructor_eager: ConstructorEager) -> None:
    df = nw.from_native(constructor_eager(data))
    result = df.select(
        **{
            name: nw.col("a").rolling_min(**values["kwargs"])
            for name, values in kwargs_and_expected.items()
        }
    )
    expected = {name: values["expected"] for name, values in kwargs_and_expected.items()}
    assert_equal_data(result, expected)


@pytest.mark.filterwarnings(
    "ignore:`Series.rolling_min` is being called from the stable API although considered an unstable feature."
)
def test_rolling_min_series(constructor_eager: ConstructorEager) -> None:
    df = nw.from_native(constructor_eager(data), eager_only=True)
    result = df.select(
        **{
            name: df["a"].rolling_min(**values["kwargs"])
            for name, values in kwargs_and_expected.items()
        }
    )
    expected = {name: values["expected"] for name, values in kwargs_and_expected.items()}
    assert_equal_data(result, expected)


@pytest.mark.parametrize(
    ("expected_a", "window_size", "min_samples", "center"),
    [([None, None, 1, None, None, 4, 6], 2, None, False)],
)
def test_rolling_min_expr_lazy(
    constructor: Constructor,
    expected_a: list[float],
    window_size: int,
    min_samples: int,
    request: pytest.FixtureRequest,
    *,
    center: bool,
) -> None:
    if ("polars" in str(constructor) and POLARS_VERSION < (1, 10)) or (
        "duckdb" in str(constructor) and DUCKDB_VERSION < (1, 3)
    ):
        pytest.skip()
    if "pandas" in str(constructor):
        pytest.skip()
    if any(x in str(constructor) for x in ("dask", "pyarrow_table")):
        request.applymarker(pytest.mark.xfail)
    if "modin" in str(constructor):
        pytest.skip()
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
            .rolling_min(window_size, min_samples=min_samples, center=center)
            .over("g", order_by="b")
        )
        .sort("i")
        .select("a")
    )
    assert_equal_data(result, {"a": expected_a})


def test_rolling_max_expr(constructor_eager: ConstructorEager) -> None:
    df = nw.from_native(constructor_eager(data))
    result = df.select(nw.col("a").rolling_max(window_size=3, min_samples=1))
    assert_equal_data(result, {"a": [None, 1, 2, 2, 4, 6, 11]})


def test_rolling_median_expr(constructor_eager: ConstructorEager) -> None:
    df = nw.from_native(constructor_eager(data))
    result = df.select(nw.col("a").rolling_median(window_size=3, min_samples=1))
    assert_equal_data(result, {"a": [None, 1.0, 1.5, 1.5, 3.0, 5.0, 6.0]})


def test_rolling_quantile_expr(constructor_eager: ConstructorEager) -> None:
    df = nw.from_native(constructor_eager(data))
    result = df.select(
        nw.col("a").rolling_quantile(window_size=3, quantile=0.5, min_samples=1)
    )
    assert_equal_data(result, {"a": [None, 1.0, 1.5, 1.5, 3.0, 5.0, 6.0]})


def test_rolling_quantile_invalid(constructor_eager: ConstructorEager) -> None:
    df = nw.from_native(constructor_eager(data))
    with pytest.raises(ValueError, match=r"Quantile must be between 0.0 and 1.0"):
        df.select(nw.col("a").rolling_quantile(window_size=2, quantile=1.5))
    with pytest.raises(ValueError, match="Interpolation must be one of"):
        df.select(
            nw.col("a").rolling_quantile(
                window_size=2, quantile=0.5, interpolation="nope"  # type: ignore[arg-type]
            )
        )


def test_rolling_quantile_duckdb_not_implemented() -> None:
    pytest.importorskip("duckdb")
    import duckdb

    df = nw.from_native(duckdb.sql("select 1.0 as a, 1 as i"))
    with pytest.raises(NotImplementedError, match="percentile_cont"):
        df.select(nw.col("a").rolling_quantile(2, quantile=0.5).over(order_by="i"))


def test_rolling_min_invalid(constructor_eager: ConstructorEager) -> None:
    df = nw.from_native(constructor_eager(data))
    with pytest.raises(InvalidOperationError, match="min_samples"):
        df.select(nw.col("a").rolling_min(window_size=1, min_samples=2))
