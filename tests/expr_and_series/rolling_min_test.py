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
    "x1": {"kwargs": {"window_size": 3}, "expected": [None] * 6 + [4.0]},
    "x2": {
        "kwargs": {"window_size": 3, "min_samples": 1},
        "expected": [None, 1.0, 1.0, 1.0, 2.0, 4.0, 4.0],
    },
    "x3": {
        "kwargs": {"window_size": 2, "min_samples": 1},
        "expected": [None, 1.0, 1.0, 2.0, 4.0, 4.0, 6.0],
    },
    "x4": {
        "kwargs": {"window_size": 5, "min_samples": 1, "center": True},
        "expected": [1.0, 1.0, 1.0, 1.0, 2.0, 4.0, 4.0],
    },
    "x5": {
        "kwargs": {"window_size": 4, "min_samples": 1, "center": True},
        "expected": [1.0, 1.0, 1.0, 1.0, 2.0, 4.0, 4.0],
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


@pytest.mark.parametrize(
    ("expected_a", "window_size", "min_samples", "center"),
    [
        ([None, None, 1, None, None, 4, 6], 2, None, False),
        ([None, None, 1, None, None, 4, 6], 2, 2, False),
        ([None, None, 1, 1, 2, 4, 4], 3, 2, False),
        ([1, None, 1, 1, 2, 4, 4], 3, 1, False),
        ([1, 1, 1, 2, 4, 4, 6], 3, 1, True),
        ([1, 1, 1, 1, 2, 4, 4], 4, 1, True),
        ([1, 1, 1, 1, 2, 4, 4], 5, 1, True),
    ],
)
def test_rolling_min_expr_lazy_ungrouped(
    constructor: Constructor,
    expected_a: list[float],
    window_size: int,
    min_samples: int,
    *,
    center: bool,
) -> None:
    if ("polars" in str(constructor) and POLARS_VERSION < (1, 10)) or (
        "duckdb" in str(constructor) and DUCKDB_VERSION < (1, 3)
    ):
        pytest.skip()
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
            .rolling_min(window_size, min_samples=min_samples, center=center)
            .over(order_by="b")
        )
        .select("a", "i")
        .sort("i")
    )
    assert_equal_data(result, {"a": expected_a, "i": list(range(7))})


@pytest.mark.parametrize(
    ("expected_a", "window_size", "min_samples", "center"),
    [
        ([None, None, 1, None, None, 4, 6], 2, None, False),
        ([None, None, 1, None, None, 4, 6], 2, 2, False),
        ([None, None, 1, 1, None, 4, 4], 3, 2, False),
        ([1, None, 1, 1, 4, 4, 4], 3, 1, False),
        ([1, 1, 1, 2, 4, 4, 6], 3, 1, True),
        ([1, 1, 1, 1, 4, 4, 4], 4, 1, True),
        ([1, 1, 1, 1, 4, 4, 4], 5, 1, True),
    ],
)
def test_rolling_min_expr_lazy_grouped(
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
    if any(x in str(constructor) for x in ("dask", "pyarrow_table")):
        request.applymarker(pytest.mark.xfail)
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
            .rolling_min(window_size, min_samples=min_samples, center=center)
            .over("g", order_by="b")
        )
        .sort("i")
        .select("a")
    )
    assert_equal_data(result, {"a": expected_a})


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
    ("window_size", "min_samples", "context"),
    [
        (
            -1,
            None,
            pytest.raises(
                ValueError, match="window_size must be greater or equal than 1"
            ),
        ),
        (
            2,
            -1,
            pytest.raises(
                ValueError, match="min_samples must be greater or equal than 1"
            ),
        ),
        (
            1,
            2,
            pytest.raises(
                InvalidOperationError,
                match="`min_samples` must be less or equal than `window_size`",
            ),
        ),
    ],
)
def test_rolling_min_expr_invalid_params(
    constructor_eager: ConstructorEager,
    window_size: int,
    min_samples: int | None,
    context: Any,
) -> None:
    df = nw.from_native(constructor_eager(data))
    with context:
        df.select(
            nw.col("a").rolling_min(window_size=window_size, min_samples=min_samples)
        )
