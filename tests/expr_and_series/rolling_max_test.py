from __future__ import annotations

import random
from typing import Any

import hypothesis.strategies as st
import pytest
from hypothesis import given

import narwhals as nw
from tests.utils import (
    DUCKDB_VERSION,
    PANDAS_VERSION,
    POLARS_VERSION,
    Constructor,
    ConstructorEager,
    assert_equal_data,
)

data = {"a": [3.0, 1.0, 4.0, 1.0, 5.0, 9.0, 2.0]}

kwargs_and_expected: dict[str, dict[str, Any]] = {
    "x1": {"kwargs": {"window_size": 3}, "expected": [None, None, 4, 4, 5, 9, 9]},
    "x2": {
        "kwargs": {"window_size": 3, "min_samples": 1},
        "expected": [3, 3, 4, 4, 5, 9, 9],
    },
    "x3": {
        "kwargs": {"window_size": 2, "min_samples": 1},
        "expected": [3, 3, 4, 4, 5, 9, 9],
    },
    "x4": {
        "kwargs": {"window_size": 5, "min_samples": 1, "center": True},
        "expected": [4, 4, 5, 9, 9, 9, 9],
    },
    "x5": {
        "kwargs": {"window_size": 4, "min_samples": 1, "center": True},
        "expected": [3, 4, 4, 5, 9, 9, 9],
    },
}


def test_rolling_max_expr(constructor_eager: ConstructorEager) -> None:
    df = nw.from_native(constructor_eager(data))
    result = df.select(
        **{
            name: nw.col("a").rolling_max(**values["kwargs"])
            for name, values in kwargs_and_expected.items()
        }
    )
    expected = {name: values["expected"] for name, values in kwargs_and_expected.items()}
    assert_equal_data(result, expected)


def test_rolling_max_series(constructor_eager: ConstructorEager) -> None:
    df = nw.from_native(constructor_eager(data), eager_only=True)
    result = df.select(
        **{
            name: df["a"].rolling_max(**values["kwargs"])
            for name, values in kwargs_and_expected.items()
        }
    )
    expected = {name: values["expected"] for name, values in kwargs_and_expected.items()}
    assert_equal_data(result, expected)


@pytest.mark.parametrize(
    ("expected_a", "window_size", "min_samples", "center"),
    [
        ([None, None, 2, None, None, 6, 11], 2, None, False),
        ([None, None, 2, None, None, 6, 11], 2, 2, False),
        ([None, None, 2, 2, 4, 6, 11], 3, 2, False),
        ([1, None, 2, 2, 4, 6, 11], 3, 1, False),
        ([2, 1, 2, 4, 6, 11, 11], 3, 1, True),
        ([2, 1, 2, 4, 6, 11, 11], 4, 1, True),
        ([2, 2, 4, 6, 11, 11, 11], 5, 1, True),
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
        ([None, None, 2, None, None, 6, 11], 2, None, False),
        ([None, None, 2, None, None, 6, 11], 2, 2, False),
        ([None, None, 2, 2, None, 6, 11], 3, 2, False),
        ([1, None, 2, 2, 4, 6, 11], 3, 1, False),
        ([2, 1, 2, 2, 6, 11, 11], 3, 1, True),
        ([2, 1, 2, 2, 6, 11, 11], 4, 1, True),
        ([2, 2, 2, 2, 11, 11, 11], 5, 1, True),
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
    if ("polars" in str(constructor) and POLARS_VERSION < (1, 10)) or (
        "duckdb" in str(constructor) and DUCKDB_VERSION < (1, 3)
    ):
        pytest.skip()
    if "pandas" in str(constructor) and PANDAS_VERSION < (1, 2):
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


@given(center=st.booleans(), values=st.lists(st.floats(-10, 10), min_size=3, max_size=10))
@pytest.mark.filterwarnings("ignore:.*is_sparse is deprecated:DeprecationWarning")
@pytest.mark.slow
def test_rolling_max_hypothesis(center: bool, values: list[float]) -> None:  # noqa: FBT001
    pytest.importorskip("pandas")
    pytest.importorskip("pyarrow")
    import pandas as pd
    import pyarrow as pa

    s = pd.Series(values)
    n_missing = random.randint(0, len(s) - 1)  # noqa: S311
    window_size = random.randint(1, len(s))  # noqa: S311
    min_samples = random.randint(1, window_size)  # noqa: S311
    mask = random.sample(range(len(s)), n_missing)
    s[mask] = None
    df = pd.DataFrame({"a": s})
    expected = (
        s.rolling(window=window_size, center=center, min_periods=min_samples)
        .max()
        .to_frame("a")
    )
    result = nw.from_native(pa.Table.from_pandas(df)).select(
        nw.col("a").rolling_max(window_size, center=center, min_samples=min_samples)
    )
    expected_dict = nw.from_native(expected, eager_only=True).to_dict(as_series=False)
    assert_equal_data(result, expected_dict)
