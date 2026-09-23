from __future__ import annotations

from typing import Any

import pytest

import narwhals as nw
from tests.utils import (
    DUCKDB_VERSION,
    PANDAS_VERSION,
    POLARS_VERSION,
    Constructor,
    ConstructorEager,
    assert_equal_data,
)

data = {"a": [1.0, 2.0, 1.0, 3.0, 1.0, 4.0, 1.0]}

kwargs_and_expected = (
    {
        "name": "x1",
        "kwargs": {"window_size": 3},
        "expected": [None, None, 2.0, 3.0, 3.0, 4.0, 4.0],
    },
    {
        "name": "x2",
        "kwargs": {"window_size": 3, "min_samples": 1},
        "expected": [1.0, 2.0, 2.0, 3.0, 3.0, 4.0, 4.0],
    },
    {
        "name": "x3",
        "kwargs": {"window_size": 2, "min_samples": 1},
        "expected": [1.0, 2.0, 2.0, 3.0, 3.0, 4.0, 4.0],
    },
    {
        "name": "x4",
        "kwargs": {"window_size": 5, "min_samples": 1, "center": True},
        "expected": [2.0, 3.0, 3.0, 4.0, 4.0, 4.0, 4.0],
    },
    {
        "name": "x5",
        "kwargs": {"window_size": 4, "min_samples": 1, "center": True},
        "expected": [2.0, 2.0, 3.0, 3.0, 4.0, 4.0, 4.0],
    },
)


@pytest.mark.parametrize("kwargs_and_expected", kwargs_and_expected)
def test_rolling_max_expr(
    constructor_eager: ConstructorEager, kwargs_and_expected: dict[str, Any]
) -> None:
    name = kwargs_and_expected["name"]
    kwargs = kwargs_and_expected["kwargs"]
    expected = kwargs_and_expected["expected"]

    df = nw.from_native(constructor_eager(data))
    result = df.select(nw.col("a").rolling_max(**kwargs).alias(name))

    assert_equal_data(result, {name: expected})


@pytest.mark.parametrize("kwargs_and_expected", kwargs_and_expected)
def test_rolling_max_series(
    constructor_eager: ConstructorEager, kwargs_and_expected: dict[str, Any]
) -> None:
    name = kwargs_and_expected["name"]
    kwargs = kwargs_and_expected["kwargs"]
    expected = kwargs_and_expected["expected"]

    df = nw.from_native(constructor_eager(data), eager_only=True)
    result = df.select(df["a"].rolling_max(**kwargs).alias(name))

    assert_equal_data(result, {name: expected})


@pytest.mark.parametrize(
    ("expected_a", "window_size", "min_samples", "center"),
    [
        ([None, None, 2.0, None, None, 6.0, 11.0], 2, None, False),
        ([None, None, 2.0, 2.0, 4.0, 6.0, 11.0], 3, 2, False),
        ([1.0, None, 2.0, 2.0, 4.0, 6.0, 11.0], 3, 1, False),
        ([2.0, 1.0, 2.0, 4.0, 6.0, 11.0, 11.0], 3, 1, True),
        ([2.0, 2.0, 4.0, 6.0, 11.0, 11.0, 11.0], 5, 1, True),
    ],
)
def test_rolling_max_expr_lazy_ungrouped(
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
        # unreliable
        pytest.skip()
    data = {
        "a": [1, None, 2, None, 4, 6, 11],
        "b": [1, None, 2, 3, 4, 5, 6],
        "i": list(range(7)),
    }
    df = nw.from_native(constructor(data))
    result = (
        df.with_columns(
            nw.col("a")
            .rolling_max(window_size, min_samples=min_samples, center=center)
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
        ([None, None, 2.0, None, None, 6.0, 11.0], 2, None, False),
        ([None, None, 2.0, 2.0, None, 6.0, 11.0], 3, 2, False),
        ([1.0, None, 2.0, 2.0, 4.0, 6.0, 11.0], 3, 1, False),
        ([2.0, 1.0, 2.0, 2.0, 6.0, 11.0, 11.0], 3, 1, True),
        ([2.0, 2.0, 2.0, 2.0, 11.0, 11.0, 11.0], 5, 1, True),
    ],
)
def test_rolling_max_expr_lazy_grouped(
    constructor: Constructor,
    expected_a: list[float],
    window_size: int,
    min_samples: int,
    request: pytest.FixtureRequest,
    *,
    center: bool,
) -> None:
    if (
        ("polars" in str(constructor) and POLARS_VERSION < (1, 10))
        or ("duckdb" in str(constructor) and DUCKDB_VERSION < (1, 3))
        or ("pandas" in str(constructor) and PANDAS_VERSION < (1, 2))
    ):
        pytest.skip()
    if any(x in str(constructor) for x in ("dask", "pyarrow_table")):
        request.applymarker(pytest.mark.xfail)
    if "modin" in str(constructor):
        # unreliable
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
            .rolling_max(window_size, min_samples=min_samples, center=center)
            .over("g", order_by="b")
        )
        .sort("i")
        .select("a")
    )
    expected = {"a": expected_a}
    assert_equal_data(result, expected)
