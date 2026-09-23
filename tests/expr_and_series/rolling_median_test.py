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
        "kwargs": {"window_size": 3},
        "expected": [None, None, 1.0, 2.0, 1.0, 3.0, 1.0],
    },
    {
        "name": "x2",
        "kwargs": {"window_size": 3, "min_samples": 1},
        "expected": [1.0, 1.5, 1.0, 2.0, 1.0, 3.0, 1.0],
    },
    {
        "name": "x3",
        "kwargs": {"window_size": 2, "min_samples": 1},
        "expected": [1.0, 1.5, 1.5, 2.0, 2.0, 2.5, 2.5],
    },
    {
        "name": "x4",
        "kwargs": {"window_size": 5, "min_samples": 1, "center": True},
        "expected": [1.0, 1.5, 1.0, 2.0, 1.0, 2.0, 1.0],
    },
    {
        "name": "x5",
        "kwargs": {"window_size": 4, "min_samples": 1, "center": True},
        "expected": [1.5, 1.0, 1.5, 1.5, 2.0, 2.0, 1.0],
    },
)


@pytest.mark.parametrize("kwargs_and_expected", kwargs_and_expected)
def test_rolling_median_expr(
    constructor_eager: ConstructorEager, kwargs_and_expected: dict[str, Any]
) -> None:
    name = kwargs_and_expected["name"]
    kwargs = kwargs_and_expected["kwargs"]
    expected = kwargs_and_expected["expected"]

    df = nw.from_native(constructor_eager(data))
    result = df.select(nw.col("a").rolling_median(**kwargs).alias(name))

    assert_equal_data(result, {name: expected})


@pytest.mark.parametrize("kwargs_and_expected", kwargs_and_expected)
def test_rolling_median_series(
    constructor_eager: ConstructorEager, kwargs_and_expected: dict[str, Any]
) -> None:
    name = kwargs_and_expected["name"]
    kwargs = kwargs_and_expected["kwargs"]
    expected = kwargs_and_expected["expected"]

    df = nw.from_native(constructor_eager(data), eager_only=True)
    result = df.select(df["a"].rolling_median(**kwargs).alias(name))

    assert_equal_data(result, {name: expected})


@given(center=st.booleans(), values=st.lists(st.floats(-10, 10), min_size=5, max_size=10))
@settings(suppress_health_check=[HealthCheck.too_slow])
@pytest.mark.slow
@pytest.mark.skipif(POLARS_VERSION < (1,), reason="different null behavior")
@pytest.mark.filterwarnings("ignore:.*is_sparse is deprecated:DeprecationWarning")
def test_rolling_median_hypothesis(center: bool, values: list[float]) -> None:  # noqa: FBT001
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
        .median()
        .to_frame("a")
    )

    result = nw.from_native(pa.Table.from_pandas(df)).select(
        nw.col("a").rolling_median(window_size, center=center, min_samples=min_samples)
    )
    expected_dict = nw.from_native(expected, eager_only=True).to_dict(as_series=False)
    assert_equal_data(result, expected_dict)


@pytest.mark.parametrize(
    ("expected_a", "window_size", "min_samples", "center"),
    [
        ([None, None, 1.5, None, None, 5.0, 8.5], 2, None, False),
        ([None, None, 1.5, 1.5, 3.0, 5.0, 6.0], 3, 2, False),
        ([1.0, None, 1.5, 1.5, 3.0, 5.0, 6.0], 3, 1, False),
        ([1.5, 1.0, 1.5, 3.0, 5.0, 6.0, 8.5], 3, 1, True),
        ([1.5, 1.0, 1.5, 2.0, 4.0, 6.0, 6.0], 4, 1, True),
    ],
)
def test_rolling_median_expr_lazy_ungrouped(
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
            .rolling_median(window_size, min_samples=min_samples, center=center)
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
        ([None, None, 1.5, None, None, 5.0, 8.5], 2, None, False),
        ([None, None, 1.5, 1.5, None, 5.0, 6.0], 3, 2, False),
        ([1.0, None, 1.5, 1.5, 4.0, 5.0, 6.0], 3, 1, False),
        ([1.5, 1.0, 1.5, 2.0, 5.0, 6.0, 8.5], 3, 1, True),
        ([1.5, 1.0, 1.5, 1.5, 5.0, 6.0, 6.0], 4, 1, True),
    ],
)
def test_rolling_median_expr_lazy_grouped(
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
            .rolling_median(window_size, min_samples=min_samples, center=center)
            .over("g", order_by="b")
        )
        .sort("i")
        .select("a")
    )
    expected = {"a": expected_a}
    assert_equal_data(result, expected)
