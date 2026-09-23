from __future__ import annotations

from typing import Any

import pytest

import narwhals as nw
from narwhals.exceptions import InvalidOperationError
from tests.utils import (
    DUCKDB_VERSION,
    PANDAS_VERSION,
    POLARS_VERSION,
    Constructor,
    ConstructorEager,
    assert_equal_data,
)

data = {"a": [None, 1, 2, None, 4, 6, 11]}

kwargs_and_expected: dict[str, dict[str, Any]] = {
    "linear": {
        "kwargs": {
            "window_size": 3,
            "quantile": 0.25,
            "interpolation": "linear",
            "min_samples": 1,
        },
        "expected": [None, 1.0, 1.25, 1.25, 2.5, 4.5, 5.0],
    },
    "lower": {
        "kwargs": {
            "window_size": 3,
            "quantile": 0.25,
            "interpolation": "lower",
            "min_samples": 1,
        },
        "expected": [None, 1.0, 1.0, 1.0, 2.0, 4.0, 4.0],
    },
    "higher": {
        "kwargs": {
            "window_size": 3,
            "quantile": 0.25,
            "interpolation": "higher",
            "min_samples": 1,
        },
        "expected": [None, 1.0, 2.0, 2.0, 4.0, 6.0, 6.0],
    },
    "midpoint": {
        "kwargs": {
            "window_size": 3,
            "quantile": 0.25,
            "interpolation": "midpoint",
            "min_samples": 1,
        },
        "expected": [None, 1.0, 1.5, 1.5, 3.0, 5.0, 5.0],
    },
    "nearest": {
        "kwargs": {
            "window_size": 3,
            "quantile": 0.5,
            "interpolation": "nearest",
        },
        "expected": [None] * 6 + [6.0],
    },
    "center": {
        "kwargs": {
            "window_size": 4,
            "quantile": 0.75,
            "interpolation": "linear",
            "min_samples": 1,
            "center": True,
        },
        "expected": [1.0, 1.75, 1.75, 3.0, 5.0, 8.5, 8.5],
    },
    "bounds": {
        "kwargs": {"window_size": 3, "quantile": 0.0, "min_samples": 1},
        "expected": [None, 1.0, 1.0, 1.0, 2.0, 4.0, 4.0],
    },
}


def test_rolling_quantile_expr(constructor_eager: ConstructorEager) -> None:
    df = nw.from_native(constructor_eager(data))
    result = df.select(
        **{
            name: nw.col("a").rolling_quantile(**values["kwargs"])
            for name, values in kwargs_and_expected.items()
        }
    )
    expected = {name: values["expected"] for name, values in kwargs_and_expected.items()}
    assert_equal_data(result, expected)


@pytest.mark.parametrize(
    ("expected_a", "window_size", "min_samples", "center"),
    [
        ([None, None, 1.25, None, None, 4.5, 7.25], 2, None, False),
        ([None, None, 1.25, None, None, 4.5, 7.25], 2, 2, False),
        ([None, None, 1.25, 1.25, 2.5, 4.5, 5.0], 3, 2, False),
        ([1.0, None, 1.25, 1.25, 2.5, 4.5, 5.0], 3, 1, False),
        ([1.25, 1.0, 1.25, 2.5, 4.5, 5.0, 7.25], 3, 1, True),
        ([1.25, 1.0, 1.25, 1.5, 3.0, 5.0, 5.0], 4, 1, True),
        ([1.25, 1.25, 1.5, 1.75, 3.5, 5.0, 5.0], 5, 1, True),
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
    if ("polars" in str(constructor) and POLARS_VERSION < (1, 10)) or (
        "duckdb" in str(constructor) and DUCKDB_VERSION < (1, 3)
    ):
        pytest.skip()
    if any(x in str(constructor) for x in ("duckdb", "sqlframe", "ibis", "pyspark")):
        # `percentile_cont` is not available as a windowed aggregate.
        request.applymarker(pytest.mark.xfail(raises=NotImplementedError))
    if "modin" in str(constructor):
        pytest.skip()
    frame = {
        "a": [1, None, 2, None, 4, 6, 11],
        "b": [1, None, 2, 3, 4, 5, 6],
        "i": list(range(7)),
    }
    df = nw.from_native(constructor(frame))
    result = (
        df.with_columns(
            nw.col("a")
            .rolling_quantile(
                window_size,
                quantile=0.25,
                interpolation="linear",
                min_samples=min_samples,
                center=center,
            )
            .over(order_by="b")
        )
        .select("a", "i")
        .sort("i")
    )
    assert_equal_data(result, {"a": expected_a, "i": list(range(7))})


@pytest.mark.parametrize(
    ("expected_a", "window_size", "min_samples", "center"),
    [
        ([None, None, 1.0, None, None, 4.0, 6.0], 2, None, False),
        ([None, None, 1.0, None, None, 4.0, 6.0], 2, 2, False),
        ([None, None, 1.0, 1.0, None, 4.0, 4.0], 3, 2, False),
        ([1.0, None, 1.0, 1.0, 4.0, 4.0, 4.0], 3, 1, False),
        ([1.0, 1.0, 1.0, 2.0, 4.0, 4.0, 6.0], 3, 1, True),
        ([1.0, 1.0, 1.0, 1.0, 4.0, 4.0, 4.0], 4, 1, True),
        ([1.0, 1.0, 1.0, 1.0, 4.0, 4.0, 4.0], 5, 1, True),
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
    if ("polars" in str(constructor) and POLARS_VERSION < (1, 10)) or (
        "duckdb" in str(constructor) and DUCKDB_VERSION < (1, 3)
    ):
        pytest.skip()
    if "pandas" in str(constructor) and PANDAS_VERSION < (1, 2):
        pytest.skip()
    if any(
        x in str(constructor)
        for x in ("dask", "pyarrow_table", "duckdb", "sqlframe", "ibis", "pyspark")
    ):
        request.applymarker(pytest.mark.xfail(raises=NotImplementedError))
    if "modin" in str(constructor):
        pytest.skip()
    frame = {
        "a": [1, None, 2, None, 4, 6, 11],
        "g": [1, 1, 1, 1, 2, 2, 2],
        "b": [1, None, 2, 3, 4, 5, 6],
        "i": list(range(7)),
    }
    df = nw.from_native(constructor(frame))
    result = (
        df.with_columns(
            nw.col("a")
            .rolling_quantile(
                window_size,
                quantile=0.25,
                interpolation="lower",
                min_samples=min_samples,
                center=center,
            )
            .over("g", order_by="b")
        )
        .sort("i")
        .select("a")
    )
    assert_equal_data(result, {"a": expected_a})


@pytest.mark.filterwarnings(
    "ignore:`Series.rolling_quantile` is being called from the stable API although considered an unstable feature."
)
def test_rolling_quantile_series(constructor_eager: ConstructorEager) -> None:
    df = nw.from_native(constructor_eager(data), eager_only=True)
    result = df.select(
        **{
            name: df["a"].rolling_quantile(**values["kwargs"])
            for name, values in kwargs_and_expected.items()
        }
    )
    expected = {name: values["expected"] for name, values in kwargs_and_expected.items()}
    assert_equal_data(result, expected)


@pytest.mark.parametrize(
    ("window_size", "min_samples", "quantile", "context"),
    [
        (
            -1,
            None,
            0.5,
            pytest.raises(
                ValueError, match="window_size must be greater or equal than 1"
            ),
        ),
        (
            1,
            2,
            0.5,
            pytest.raises(
                InvalidOperationError,
                match="`min_samples` must be less or equal than `window_size`",
            ),
        ),
        (
            3,
            None,
            -0.1,
            pytest.raises(ValueError, match=r"Quantile must be between 0\.0 and 1\.0"),
        ),
        (
            3,
            None,
            1.1,
            pytest.raises(ValueError, match=r"Quantile must be between 0\.0 and 1\.0"),
        ),
    ],
)
def test_rolling_quantile_expr_invalid_params(
    constructor_eager: ConstructorEager,
    window_size: int,
    min_samples: int | None,
    quantile: float,
    context: Any,
) -> None:
    df = nw.from_native(constructor_eager(data))
    with context:
        df.select(
            nw.col("a").rolling_quantile(
                window_size=window_size, quantile=quantile, min_samples=min_samples
            )
        )


def test_rolling_quantile_invalid_interpolation(
    constructor_eager: ConstructorEager,
) -> None:
    df = nw.from_native(constructor_eager(data))
    with pytest.raises(ValueError, match="Interpolation must be one of"):
        df.select(
            nw.col("a").rolling_quantile(
                window_size=3,
                quantile=0.5,
                interpolation="not-a-method",  # type: ignore[arg-type]
            )
        )


def _collect_rolling_quantile(df: Any) -> Any:
    result = df.select(nw.col("a").rolling_quantile(2, quantile=0.5).over(order_by="i"))
    return result.collect() if hasattr(result, "collect") else result


def test_rolling_quantile_duckdb_over_unavailable(constructor: Constructor) -> None:
    if "duckdb_lazy_constructor" not in str(constructor):
        pytest.skip()
    frame = {"a": [1.0, 2.0, 3.0], "i": [0, 1, 2]}
    df = nw.from_native(constructor(frame))
    with pytest.raises(
        NotImplementedError,
        match=r"rolling_quantile with `\.over\(\)` is not available on DuckDB",
    ):
        _collect_rolling_quantile(df)
