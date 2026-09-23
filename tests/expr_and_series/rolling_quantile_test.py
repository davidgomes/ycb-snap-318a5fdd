from __future__ import annotations

import random
from typing import Any

import hypothesis.strategies as st
import pytest
from hypothesis import given

import narwhals as nw
from tests.utils import (
    PANDAS_VERSION,
    POLARS_VERSION,
    Constructor,
    ConstructorEager,
    assert_equal_data,
)

SQL_LIKE = ("duckdb", "sqlframe", "pyspark", "ibis")


def polars_nearest_differs(constructor: Any, interpolation: str) -> bool:
    # Polars changed which element `'nearest'` picks in rolling quantiles in 1.32.
    return (
        "polars" in str(constructor)
        and interpolation == "nearest"
        and POLARS_VERSION < (1, 32)
    )

data = {"a": [None, 1, 2, None, 4, 6, 11]}

kwargs: dict[str, dict[str, Any]] = {
    "x1": {"window_size": 3},
    "x2": {"window_size": 3, "min_samples": 1},
    "x3": {"window_size": 2, "min_samples": 1},
    "x4": {"window_size": 5, "min_samples": 1, "center": True},
    "x5": {"window_size": 4, "min_samples": 1, "center": True},
}

# Expected values for `quantile=0.3`, for each interpolation method.
expected_by_interpolation: dict[str, dict[str, list[Any]]] = {
    "linear": {
        "x1": [None] * 6 + [5.2],
        "x2": [None, 1, 1.3, 1.3, 2.6, 4.6, 5.2],
        "x3": [None, 1, 1.3, 2, 4, 4.6, 7.5],
        "x4": [1.3, 1.3, 1.6, 1.9, 3.8, 5.2, 5.2],
        "x5": [1, 1.3, 1.3, 1.6, 3.2, 5.2, 5.2],
    },
    "lower": {
        "x1": [None] * 6 + [4],
        "x2": [None, 1, 1, 1, 2, 4, 4],
        "x3": [None, 1, 1, 2, 4, 4, 6],
        "x4": [1, 1, 1, 1, 2, 4, 4],
        "x5": [1, 1, 1, 1, 2, 4, 4],
    },
    "higher": {
        "x1": [None] * 6 + [6],
        "x2": [None, 1, 2, 2, 4, 6, 6],
        "x3": [None, 1, 2, 2, 4, 6, 11],
        "x4": [2, 2, 2, 2, 4, 6, 6],
        "x5": [1, 2, 2, 2, 4, 6, 6],
    },
    "nearest": {
        "x1": [None] * 6 + [6],
        "x2": [None, 1, 1, 1, 2, 4, 6],
        "x3": [None, 1, 1, 2, 4, 4, 6],
        "x4": [1, 1, 2, 2, 4, 6, 6],
        "x5": [1, 1, 1, 2, 4, 6, 6],
    },
    "midpoint": {
        "x1": [None] * 6 + [5],
        "x2": [None, 1, 1.5, 1.5, 3, 5, 5],
        "x3": [None, 1, 1.5, 2, 4, 5, 8.5],
        "x4": [1.5, 1.5, 1.5, 1.5, 3, 5, 5],
        "x5": [1, 1.5, 1.5, 1.5, 3, 5, 5],
    },
}


@pytest.mark.parametrize("interpolation", list(expected_by_interpolation))
def test_rolling_quantile_expr(
    constructor_eager: ConstructorEager,
    interpolation: Any,
    request: pytest.FixtureRequest,
) -> None:
    if polars_nearest_differs(constructor_eager, interpolation):
        request.applymarker(pytest.mark.xfail)
    df = nw.from_native(constructor_eager(data))
    result = df.select(
        **{
            name: nw.col("a").rolling_quantile(
                quantile=0.3, interpolation=interpolation, **kwds
            )
            for name, kwds in kwargs.items()
        }
    )
    assert_equal_data(result, expected_by_interpolation[interpolation])


@pytest.mark.filterwarnings(
    "ignore:`Series.rolling_quantile` is being called from the stable API although considered an unstable feature."
)
@pytest.mark.parametrize("interpolation", list(expected_by_interpolation))
def test_rolling_quantile_series(
    constructor_eager: ConstructorEager,
    interpolation: Any,
    request: pytest.FixtureRequest,
) -> None:
    if polars_nearest_differs(constructor_eager, interpolation):
        request.applymarker(pytest.mark.xfail)
    df = nw.from_native(constructor_eager(data), eager_only=True)
    result = df.select(
        **{
            name: df["a"].rolling_quantile(
                quantile=0.3, interpolation=interpolation, **kwds
            )
            for name, kwds in kwargs.items()
        }
    )
    assert_equal_data(result, expected_by_interpolation[interpolation])


@pytest.mark.parametrize(
    ("quantile", "expected"),
    [(0.0, [None, 1, 1, 1, 2, 4, 4]), (1.0, [None, 1, 2, 2, 4, 6, 11])],
)
def test_rolling_quantile_bounds(
    constructor_eager: ConstructorEager, quantile: float, expected: list[Any]
) -> None:
    df = nw.from_native(constructor_eager(data))
    result = df.select(nw.col("a").rolling_quantile(3, quantile=quantile, min_samples=1))
    assert_equal_data(result, {"a": expected})


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
    request: pytest.FixtureRequest,
    *,
    center: bool,
) -> None:
    if "polars" in str(constructor) and POLARS_VERSION < (1, 10):
        pytest.skip()
    if "modin" in str(constructor):
        # unreliable
        pytest.skip()
    if "duckdb" in str(constructor):
        # DuckDB does not support `percentile_cont` as a windowed aggregate function.
        request.applymarker(pytest.mark.xfail(raises=NotImplementedError))
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
    if "polars" in str(constructor) and POLARS_VERSION < (1, 10):
        pytest.skip()
    if "pandas" in str(constructor) and PANDAS_VERSION < (1, 2):
        pytest.skip()
    if "modin" in str(constructor):
        # unreliable
        pytest.skip()
    if any(x in str(constructor) for x in ("dask", "pyarrow_table")):
        request.applymarker(pytest.mark.xfail)
    if "duckdb" in str(constructor):
        # DuckDB does not support `percentile_cont` as a windowed aggregate function.
        request.applymarker(pytest.mark.xfail(raises=NotImplementedError))
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


@pytest.mark.parametrize(
    ("interpolation", "expected_a"),
    [
        ("linear", [1, None, 1.3, 1.3, 2.6, 4.6, 5.2]),
        ("lower", [1, None, 1, 1, 2, 4, 4]),
        ("higher", [1, None, 2, 2, 4, 6, 6]),
        ("nearest", [1, None, 1, 1, 2, 4, 6]),
        ("midpoint", [1, None, 1.5, 1.5, 3, 5, 5]),
    ],
)
def test_rolling_quantile_expr_lazy_interpolation(
    constructor: Constructor,
    interpolation: Any,
    expected_a: list[float],
    request: pytest.FixtureRequest,
) -> None:
    if "polars" in str(constructor) and POLARS_VERSION < (1, 10):
        pytest.skip()
    if polars_nearest_differs(constructor, interpolation):
        request.applymarker(pytest.mark.xfail)
    if "modin" in str(constructor):
        # unreliable
        pytest.skip()
    if "duckdb" in str(constructor) or (
        interpolation != "linear" and any(x in str(constructor) for x in SQL_LIKE)
    ):
        pytest.skip(reason="Covered by the `NotImplementedError` tests below")
    data = {
        "a": [1, None, 2, None, 4, 6, 11],
        "b": [1, None, 2, 3, 4, 5, 6],
        "i": list(range(7)),
    }
    df = nw.from_native(constructor(data))
    result = (
        df.with_columns(
            nw.col("a")
            .rolling_quantile(3, quantile=0.3, interpolation=interpolation, min_samples=1)
            .over(order_by="b")
        )
        .select("a", "i")
        .sort("i")
    )
    expected = {"a": expected_a, "i": list(range(7))}
    assert_equal_data(result, expected)


def test_rolling_quantile_duckdb_not_supported(constructor: Constructor) -> None:
    if "duckdb" not in str(constructor):
        pytest.skip()
    data = {"a": [1, None, 2], "b": [1, 2, 3]}
    df = nw.from_native(constructor(data))
    with pytest.raises(
        NotImplementedError,
        match="DuckDB does not support `percentile_cont` as a windowed aggregate",
    ):
        df.with_columns(
            nw.col("a").rolling_quantile(2, quantile=0.5).over(order_by="b")
        ).lazy().collect()


def test_rolling_quantile_sql_only_linear_interpolation(constructor: Constructor) -> None:
    if "duckdb" in str(constructor) or not any(x in str(constructor) for x in SQL_LIKE):
        pytest.skip()
    data = {"a": [1, None, 2], "b": [1, 2, 3]}
    df = nw.from_native(constructor(data))
    with pytest.raises(NotImplementedError, match="Only linear interpolation"):
        df.with_columns(
            nw.col("a")
            .rolling_quantile(2, quantile=0.5, interpolation="lower")
            .over(order_by="b")
        ).lazy().collect()


@pytest.mark.parametrize(
    ("quantile", "interpolation", "match"),
    [
        (-0.1, "linear", r"^Quantile must be between 0\.0 and 1\.0"),
        (1.1, "linear", r"^Quantile must be between 0\.0 and 1\.0"),
        (0.5, "foo", r"^Interpolation must be one of"),
    ],
)
@pytest.mark.filterwarnings(
    "ignore:`Series.rolling_quantile` is being called from the stable API although considered an unstable feature."
)
def test_rolling_quantile_invalid_params(
    constructor_eager: ConstructorEager, quantile: float, interpolation: Any, match: str
) -> None:
    df = nw.from_native(constructor_eager(data), eager_only=True)

    with pytest.raises(ValueError, match=match):
        df.select(
            nw.col("a").rolling_quantile(
                3, quantile=quantile, interpolation=interpolation
            )
        )
    with pytest.raises(ValueError, match=match):
        df["a"].rolling_quantile(3, quantile=quantile, interpolation=interpolation)


@given(
    center=st.booleans(),
    values=st.lists(st.floats(-10, 10), min_size=3, max_size=10),
    quantile=st.floats(0, 1),
    interpolation=st.sampled_from(["linear", "lower", "higher", "nearest", "midpoint"]),
)
@pytest.mark.filterwarnings("ignore:.*:narwhals.exceptions.NarwhalsUnstableWarning")
@pytest.mark.filterwarnings("ignore:.*is_sparse is deprecated:DeprecationWarning")
@pytest.mark.slow
def test_rolling_quantile_hypothesis(
    center: bool,  # noqa: FBT001
    values: list[float],
    quantile: float,
    interpolation: Any,
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


@given(
    center=st.booleans(),
    values=st.lists(st.floats(-10, 10), min_size=3, max_size=10),
    quantile=st.floats(0, 1),
    # Depending on the Polars version, 'nearest' and 'midpoint' can differ from pandas.
    interpolation=st.sampled_from(["linear", "lower", "higher"]),
)
@pytest.mark.filterwarnings("ignore:.*:narwhals.exceptions.NarwhalsUnstableWarning")
@pytest.mark.filterwarnings("ignore:.*is_sparse is deprecated:DeprecationWarning")
@pytest.mark.slow
def test_rolling_quantile_hypothesis_polars(
    center: bool,  # noqa: FBT001
    values: list[float],
    quantile: float,
    interpolation: Any,
) -> None:
    pytest.importorskip("pandas")
    pytest.importorskip("polars")
    import pandas as pd
    import polars as pl

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
    result = nw.from_native(pl.from_pandas(df)).select(
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
