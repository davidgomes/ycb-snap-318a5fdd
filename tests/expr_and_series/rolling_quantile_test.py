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
    "x1": {
        "kwargs": {"window_size": 3, "quantile": 0.25},
        "expected": [None, None, 2, 1, 2.5, 3, 3.5],
    },
    "x2": {
        "kwargs": {"window_size": 3, "quantile": 0.25, "min_samples": 1},
        "expected": [3, 1.5, 2, 1, 2.5, 3, 3.5],
    },
    "x3": {
        "kwargs": {"window_size": 2, "quantile": 0.25, "min_samples": 1},
        "expected": [3, 1.5, 1.75, 1.75, 2, 6, 3.75],
    },
    "x4": {
        "kwargs": {"window_size": 5, "quantile": 0.25, "min_samples": 1, "center": True},
        "expected": [2, 1, 1, 1, 2, 1.75, 3.5],
    },
    "x5": {
        "kwargs": {"window_size": 4, "quantile": 0.25, "min_samples": 1, "center": True},
        "expected": [1.5, 2, 1, 1, 3.25, 1.75, 3.5],
    },
    "x6": {
        "kwargs": {"window_size": 3, "quantile": 0.4, "interpolation": "nearest"},
        "expected": [None, None, 3, 1, 4, 5, 5],
    },
    "x7": {
        "kwargs": {"window_size": 3, "quantile": 0.4, "interpolation": "lower"},
        "expected": [None, None, 1, 1, 1, 1, 2],
    },
    "x8": {
        "kwargs": {"window_size": 3, "quantile": 0.4, "interpolation": "higher"},
        "expected": [None, None, 3, 1, 4, 5, 5],
    },
    "x9": {
        "kwargs": {"window_size": 3, "quantile": 0.4, "interpolation": "midpoint"},
        "expected": [None, None, 2, 1, 2.5, 3, 3.5],
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
    ("expected_a", "window_size", "min_samples", "center"),
    [
        ([None, None, 1.25, None, None, 4.5, 7.25], 2, None, False),
        ([None, None, 1.25, None, None, 4.5, 7.25], 2, 2, False),
        ([None, None, 1.25, 1.25, 2.5, 4.5, 5], 3, 2, False),
        ([1, None, 1.25, 1.25, 2.5, 4.5, 5], 3, 1, False),
        ([1.25, 1, 1.25, 2.5, 4.5, 5, 7.25], 3, 1, True),
        ([1.25, 1, 1.25, 1.5, 3, 5, 5], 4, 1, True),
        ([1.25, 1.25, 1.5, 1.75, 3.5, 5, 5], 5, 1, True),
    ],
)
def test_rolling_quantile_expr_lazy_ungrouped(
    constructor: Constructor,
    expected_a: list[float],
    window_size: int,
    min_samples: int,
    *,
    center: bool,
) -> None:
    if any(x in str(constructor) for x in ("duckdb", "modin")):
        # duckdb: not supported, modin: unreliable
        pytest.skip()
    if "polars" in str(constructor) and POLARS_VERSION < (1, 10):
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
        ([None, None, 1.25, None, None, 4.5, 7.25], 2, 2, False),
        ([None, None, 1.25, 1.25, None, 4.5, 5], 3, 2, False),
        ([1, None, 1.25, 1.25, 4, 4.5, 5], 3, 1, False),
        ([1.25, 1, 1.25, 2, 4.5, 5, 7.25], 3, 1, True),
        ([1.25, 1, 1.25, 1.25, 4.5, 5, 5], 4, 1, True),
        ([1.25, 1.25, 1.25, 1.25, 5, 5, 5], 5, 1, True),
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
    if any(x in str(constructor) for x in ("duckdb", "modin")):
        # duckdb: not supported, modin: unreliable
        pytest.skip()
    if ("polars" in str(constructor) and POLARS_VERSION < (1, 10)) or (
        "pandas" in str(constructor) and PANDAS_VERSION < (1, 2)
    ):
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


def test_rolling_quantile_duckdb_not_implemented(constructor: Constructor) -> None:
    if "duckdb" not in str(constructor) or DUCKDB_VERSION < (1, 3):
        pytest.skip()
    df = nw.from_native(constructor({"a": [1, 2, 3], "b": [1, 2, 3]}))
    with pytest.raises(NotImplementedError):
        df.with_columns(
            nw.col("a").rolling_quantile(2, quantile=0.5).over(order_by="b")
        ).collect()


def test_rolling_quantile_non_linear_not_implemented(constructor: Constructor) -> None:
    if not any(x in str(constructor) for x in ("dask", "sqlframe", "pyspark", "ibis")):
        pytest.skip()
    df = nw.from_native(constructor({"a": [1, 2, 3], "b": [1, 2, 3]}))
    with pytest.raises(NotImplementedError, match="linear"):
        df.with_columns(
            nw.col("a")
            .rolling_quantile(2, quantile=0.5, interpolation="lower")
            .over(order_by="b")
        ).collect()


@pytest.mark.parametrize(
    ("quantile", "interpolation", "context"),
    [
        (
            -0.1,
            "linear",
            pytest.raises(ValueError, match=r"Quantile must be between 0\.0 and 1\.0"),
        ),
        (
            1.5,
            "linear",
            pytest.raises(ValueError, match=r"Quantile must be between 0\.0 and 1\.0"),
        ),
        (0.5, "foo", pytest.raises(ValueError, match="Interpolation must be one of")),
    ],
)
def test_rolling_quantile_invalid_params(
    constructor_eager: ConstructorEager, quantile: float, interpolation: Any, context: Any
) -> None:
    df = nw.from_native(constructor_eager(data), eager_only=True)
    with context:
        nw.col("a").rolling_quantile(2, quantile=quantile, interpolation=interpolation)
    with context:
        df["a"].rolling_quantile(2, quantile=quantile, interpolation=interpolation)


@given(
    center=st.booleans(),
    quantile=st.floats(0, 1),
    interpolation=st.sampled_from(["linear", "lower", "higher", "midpoint"]),
    values=st.lists(st.floats(-10, 10), min_size=3, max_size=10),
)
@pytest.mark.filterwarnings("ignore:.*is_sparse is deprecated:DeprecationWarning")
@pytest.mark.slow
def test_rolling_quantile_hypothesis(
    center: bool,  # noqa: FBT001
    quantile: float,
    interpolation: Any,
    values: list[float],
) -> None:
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
