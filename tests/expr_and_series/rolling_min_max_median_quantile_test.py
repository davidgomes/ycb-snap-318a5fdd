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

kwargs_and_expected: dict[str, dict[str, dict[str, Any]]] = {
    "rolling_min": {
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
        "x5": {
            "kwargs": {"window_size": 4, "min_samples": 1, "center": True},
            "expected": [1, 1, 1, 1, 2, 4, 4],
        },
    },
    "rolling_max": {
        "x1": {"kwargs": {"window_size": 3}, "expected": [None] * 6 + [11]},
        "x2": {
            "kwargs": {"window_size": 3, "min_samples": 1},
            "expected": [None, 1, 2, 2, 4, 6, 11],
        },
        "x3": {
            "kwargs": {"window_size": 2, "min_samples": 1},
            "expected": [None, 1, 2, 2, 4, 6, 11],
        },
        "x4": {
            "kwargs": {"window_size": 5, "min_samples": 1, "center": True},
            "expected": [2, 2, 4, 6, 11, 11, 11],
        },
        "x5": {
            "kwargs": {"window_size": 4, "min_samples": 1, "center": True},
            "expected": [1, 2, 2, 4, 6, 11, 11],
        },
    },
    "rolling_median": {
        "x1": {"kwargs": {"window_size": 3}, "expected": [None] * 6 + [6.0]},
        "x2": {
            "kwargs": {"window_size": 3, "min_samples": 1},
            "expected": [None, 1.0, 1.5, 1.5, 3.0, 5.0, 6.0],
        },
        "x3": {
            "kwargs": {"window_size": 2, "min_samples": 1},
            "expected": [None, 1.0, 1.5, 2.0, 4.0, 5.0, 8.5],
        },
        "x4": {
            "kwargs": {"window_size": 5, "min_samples": 1, "center": True},
            "expected": [1.5, 1.5, 2.0, 3.0, 5.0, 6.0, 6.0],
        },
        "x5": {
            "kwargs": {"window_size": 4, "min_samples": 1, "center": True},
            "expected": [1.0, 1.5, 1.5, 2.0, 4.0, 6.0, 6.0],
        },
    },
}


def _expr_method(name: str) -> Any:
    return getattr(nw.col("a"), name)


@pytest.mark.parametrize("method_name", ["rolling_min", "rolling_max", "rolling_median"])
def test_rolling_expr(constructor_eager: ConstructorEager, method_name: str) -> None:
    df = nw.from_native(constructor_eager(data))
    cases = kwargs_and_expected[method_name]
    result = df.select(
        **{
            name: _expr_method(method_name)(**values["kwargs"])
            for name, values in cases.items()
        }
    )
    expected = {name: values["expected"] for name, values in cases.items()}
    assert_equal_data(result, expected)


@pytest.mark.parametrize("method_name", ["rolling_min", "rolling_max", "rolling_median"])
@pytest.mark.filterwarnings(
    "ignore:`Series.rolling_min` is being called from the stable API although considered an unstable feature."
)
@pytest.mark.filterwarnings(
    "ignore:`Series.rolling_max` is being called from the stable API although considered an unstable feature."
)
@pytest.mark.filterwarnings(
    "ignore:`Series.rolling_median` is being called from the stable API although considered an unstable feature."
)
def test_rolling_series(constructor_eager: ConstructorEager, method_name: str) -> None:
    df = nw.from_native(constructor_eager(data), eager_only=True)
    cases = kwargs_and_expected[method_name]
    result = df.select(
        **{
            name: getattr(df["a"], method_name)(**values["kwargs"])
            for name, values in cases.items()
        }
    )
    expected = {name: values["expected"] for name, values in cases.items()}
    assert_equal_data(result, expected)


_LAZY_UNGROUPED: dict[str, list[tuple[list[float | None], int, int | None, bool]]] = {
    "rolling_min": [
        ([None, None, 1.0, None, None, 4.0, 6.0], 2, None, False),
        ([None, None, 1.0, None, None, 4.0, 6.0], 2, 2, False),
        ([None, None, 1.0, 1.0, 2.0, 4.0, 4.0], 3, 2, False),
        ([1.0, None, 1.0, 1.0, 2.0, 4.0, 4.0], 3, 1, False),
        ([1.0, 1.0, 1.0, 2.0, 4.0, 4.0, 6.0], 3, 1, True),
        ([1.0, 1.0, 1.0, 1.0, 2.0, 4.0, 4.0], 4, 1, True),
        ([1.0, 1.0, 1.0, 1.0, 2.0, 4.0, 4.0], 5, 1, True),
    ],
    "rolling_max": [
        ([None, None, 2.0, None, None, 6.0, 11.0], 2, None, False),
        ([None, None, 2.0, None, None, 6.0, 11.0], 2, 2, False),
        ([None, None, 2.0, 2.0, 4.0, 6.0, 11.0], 3, 2, False),
        ([1.0, None, 2.0, 2.0, 4.0, 6.0, 11.0], 3, 1, False),
        ([2.0, 1.0, 2.0, 4.0, 6.0, 11.0, 11.0], 3, 1, True),
        ([2.0, 1.0, 2.0, 4.0, 6.0, 11.0, 11.0], 4, 1, True),
        ([2.0, 2.0, 4.0, 6.0, 11.0, 11.0, 11.0], 5, 1, True),
    ],
    "rolling_median": [
        ([None, None, 1.5, None, None, 5.0, 8.5], 2, None, False),
        ([None, None, 1.5, None, None, 5.0, 8.5], 2, 2, False),
        ([None, None, 1.5, 1.5, 3.0, 5.0, 6.0], 3, 2, False),
        ([1.0, None, 1.5, 1.5, 3.0, 5.0, 6.0], 3, 1, False),
        ([1.5, 1.0, 1.5, 3.0, 5.0, 6.0, 8.5], 3, 1, True),
        ([1.5, 1.0, 1.5, 2.0, 4.0, 6.0, 6.0], 4, 1, True),
        ([1.5, 1.5, 2.0, 3.0, 5.0, 6.0, 6.0], 5, 1, True),
    ],
}

_LAZY_GROUPED: dict[str, list[tuple[list[float | None], int, int | None, bool]]] = {
    "rolling_min": [
        ([None, None, 1.0, None, None, 4.0, 6.0], 2, None, False),
        ([None, None, 1.0, None, None, 4.0, 6.0], 2, 2, False),
        ([None, None, 1.0, 1.0, None, 4.0, 4.0], 3, 2, False),
        ([1.0, None, 1.0, 1.0, 4.0, 4.0, 4.0], 3, 1, False),
        ([1.0, 1.0, 1.0, 2.0, 4.0, 4.0, 6.0], 3, 1, True),
        ([1.0, 1.0, 1.0, 1.0, 4.0, 4.0, 4.0], 4, 1, True),
        ([1.0, 1.0, 1.0, 1.0, 4.0, 4.0, 4.0], 5, 1, True),
    ],
    "rolling_max": [
        ([None, None, 2.0, None, None, 6.0, 11.0], 2, None, False),
        ([None, None, 2.0, None, None, 6.0, 11.0], 2, 2, False),
        ([None, None, 2.0, 2.0, None, 6.0, 11.0], 3, 2, False),
        ([1.0, None, 2.0, 2.0, 4.0, 6.0, 11.0], 3, 1, False),
        ([2.0, 1.0, 2.0, 2.0, 6.0, 11.0, 11.0], 3, 1, True),
        ([2.0, 1.0, 2.0, 2.0, 6.0, 11.0, 11.0], 4, 1, True),
        ([2.0, 2.0, 2.0, 2.0, 11.0, 11.0, 11.0], 5, 1, True),
    ],
    "rolling_median": [
        ([None, None, 1.5, None, None, 5.0, 8.5], 2, None, False),
        ([None, None, 1.5, None, None, 5.0, 8.5], 2, 2, False),
        ([None, None, 1.5, 1.5, None, 5.0, 6.0], 3, 2, False),
        ([1.0, None, 1.5, 1.5, 4.0, 5.0, 6.0], 3, 1, False),
        ([1.5, 1.0, 1.5, 2.0, 5.0, 6.0, 8.5], 3, 1, True),
        ([1.5, 1.0, 1.5, 1.5, 5.0, 6.0, 6.0], 4, 1, True),
        ([1.5, 1.5, 1.5, 1.5, 6.0, 6.0, 6.0], 5, 1, True),
    ],
}


def _lazy_data() -> dict[str, list[Any]]:
    return {
        "a": [1, None, 2, None, 4, 6, 11],
        "b": [1, None, 2, 3, 4, 5, 6],
        "i": list(range(7)),
    }


def _skip_lazy_backend(constructor: Constructor) -> None:
    if ("polars" in str(constructor) and POLARS_VERSION < (1, 10)) or (
        "duckdb" in str(constructor) and DUCKDB_VERSION < (1, 3)
    ):
        pytest.skip()
    if "modin" in str(constructor):
        pytest.skip()


@pytest.mark.parametrize("method_name", ["rolling_min", "rolling_max", "rolling_median"])
@pytest.mark.parametrize("case", range(7))
def test_rolling_expr_lazy_ungrouped(
    constructor: Constructor, method_name: str, case: int
) -> None:
    _skip_lazy_backend(constructor)
    expected_a, window_size, min_samples, center = _LAZY_UNGROUPED[method_name][case]
    df = nw.from_native(constructor(_lazy_data()))
    method = getattr(nw.col("a"), method_name)
    result = (
        df.with_columns(
            method(window_size, min_samples=min_samples, center=center).over(order_by="b")
        )
        .select("a", "i")
        .sort("i")
    )
    assert_equal_data(result, {"a": expected_a, "i": list(range(7))})


@pytest.mark.parametrize("method_name", ["rolling_min", "rolling_max", "rolling_median"])
@pytest.mark.parametrize("case", range(7))
def test_rolling_expr_lazy_grouped(
    constructor: Constructor, method_name: str, case: int, request: pytest.FixtureRequest
) -> None:
    _skip_lazy_backend(constructor)
    if "pandas" in str(constructor) and PANDAS_VERSION < (1, 2):
        pytest.skip()
    if any(x in str(constructor) for x in ("dask", "pyarrow_table")):
        request.applymarker(pytest.mark.xfail)
    expected_a, window_size, min_samples, center = _LAZY_GROUPED[method_name][case]
    grouped = _lazy_data()
    grouped["g"] = [1, 1, 1, 1, 2, 2, 2]
    df = nw.from_native(constructor(grouped))
    method = getattr(nw.col("a"), method_name)
    result = (
        df.with_columns(
            method(window_size, min_samples=min_samples, center=center).over(
                "g", order_by="b"
            )
        )
        .sort("i")
        .select("a")
    )
    assert_equal_data(result, {"a": expected_a})


_QUANTILE_EAGER: dict[str, dict[str, Any]] = {
    "linear": {
        "kwargs": {"window_size": 3, "quantile": 0.75, "min_samples": 1},
        "expected": [None, 1.0, 1.75, 1.75, 3.5, 5.5, 8.5],
    },
    "lower": {
        "kwargs": {
            "window_size": 3,
            "quantile": 0.75,
            "interpolation": "lower",
            "min_samples": 1,
        },
        "expected": [None, 1.0, 1.0, 1.0, 2.0, 4.0, 6.0],
    },
    "higher": {
        "kwargs": {
            "window_size": 3,
            "quantile": 0.75,
            "interpolation": "higher",
            "min_samples": 1,
        },
        "expected": [None, 1.0, 2.0, 2.0, 4.0, 6.0, 11.0],
    },
    "nearest": {
        "kwargs": {
            "window_size": 3,
            "quantile": 0.75,
            "interpolation": "nearest",
            "min_samples": 1,
        },
        "expected": [None, 1.0, 2.0, 2.0, 4.0, 6.0, 11.0],
    },
    "midpoint": {
        "kwargs": {
            "window_size": 4,
            "quantile": 0.5,
            "interpolation": "midpoint",
            "min_samples": 1,
            "center": True,
        },
        "expected": [1.0, 1.5, 1.5, 2.0, 4.0, 6.0, 6.0],
    },
}


@pytest.mark.parametrize("name", list(_QUANTILE_EAGER))
def test_rolling_quantile_expr(constructor_eager: ConstructorEager, name: str) -> None:
    case = _QUANTILE_EAGER[name]
    df = nw.from_native(constructor_eager(data))
    result = df.select(nw.col("a").rolling_quantile(**case["kwargs"]).alias(name))
    assert_equal_data(result, {name: case["expected"]})


@pytest.mark.parametrize("name", list(_QUANTILE_EAGER))
@pytest.mark.filterwarnings(
    "ignore:`Series.rolling_quantile` is being called from the stable API although considered an unstable feature."
)
def test_rolling_quantile_series(constructor_eager: ConstructorEager, name: str) -> None:
    case = _QUANTILE_EAGER[name]
    df = nw.from_native(constructor_eager(data), eager_only=True)
    result = df.select(df["a"].rolling_quantile(**case["kwargs"]).alias(name))
    assert_equal_data(result, {name: case["expected"]})


@pytest.mark.parametrize(
    ("expected_a", "window_size", "min_samples", "center"),
    [
        ([None, None, 1.75, None, None, 5.5, 9.75], 2, None, False),
        ([None, None, 1.75, 1.75, 3.5, 5.5, 8.5], 3, 2, False),
        ([1.0, None, 1.75, 1.75, 3.5, 5.5, 8.5], 3, 1, False),
        ([1.75, 1.0, 1.75, 3.5, 5.5, 8.5, 9.75], 3, 1, True),
        ([1.75, 1.75, 3.0, 4.5, 7.25, 8.5, 8.5], 5, 1, True),
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
    if "duckdb" in str(constructor):
        pytest.skip()
    _skip_lazy_backend(constructor)
    df = nw.from_native(constructor(_lazy_data()))
    result = (
        df.with_columns(
            nw.col("a")
            .rolling_quantile(
                window_size, quantile=0.75, min_samples=min_samples, center=center
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
        ([None, None, 1.75, None, None, 5.5, 9.75], 2, None, False),
        ([None, None, 1.75, 1.75, None, 5.5, 8.5], 3, 2, False),
        ([1.0, None, 1.75, 1.75, 4.0, 5.5, 8.5], 3, 1, False),
        ([1.75, 1.0, 1.75, 2.0, 5.5, 8.5, 9.75], 3, 1, True),
        ([1.75, 1.75, 1.75, 1.75, 8.5, 8.5, 8.5], 5, 1, True),
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
    if "duckdb" in str(constructor):
        pytest.skip()
    _skip_lazy_backend(constructor)
    if "pandas" in str(constructor) and PANDAS_VERSION < (1, 2):
        pytest.skip()
    if any(x in str(constructor) for x in ("dask", "pyarrow_table")):
        request.applymarker(pytest.mark.xfail)
    grouped = _lazy_data()
    grouped["g"] = [1, 1, 1, 1, 2, 2, 2]
    df = nw.from_native(constructor(grouped))
    result = (
        df.with_columns(
            nw.col("a")
            .rolling_quantile(
                window_size, quantile=0.75, min_samples=min_samples, center=center
            )
            .over("g", order_by="b")
        )
        .sort("i")
        .select("a")
    )
    assert_equal_data(result, {"a": expected_a})


def test_rolling_quantile_duckdb_over_unavailable() -> None:
    pytest.importorskip("duckdb")
    pytest.importorskip("pyarrow")
    import duckdb
    import pyarrow as pa

    if DUCKDB_VERSION < (1, 3):
        pytest.skip()
    _table = pa.table({"a": [1.0, 2.0, 3.0], "i": [0, 1, 2]})
    rel = duckdb.sql("select * from _table")
    df = nw.from_native(rel)
    with pytest.raises(NotImplementedError, match="percentile_cont"):
        df.select(
            nw.col("a").rolling_quantile(2, quantile=0.5).over(order_by="i")
        ).to_native()


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
@pytest.mark.parametrize("method_name", ["rolling_min", "rolling_max", "rolling_median"])
def test_rolling_invalid_params(
    method_name: str, window_size: int, min_samples: int | None, context: Any
) -> None:
    with context:
        getattr(nw.col("a"), method_name)(
            window_size=window_size, min_samples=min_samples
        )


@pytest.mark.parametrize(
    ("quantile", "interpolation", "match"),
    [
        (1.5, "linear", "Quantile must be between 0.0 and 1.0"),
        (-0.1, "linear", "Quantile must be between 0.0 and 1.0"),
        (0.5, "nope", "Interpolation must be one of"),
    ],
)
def test_rolling_quantile_invalid_params(
    quantile: float, interpolation: str, match: str
) -> None:
    with pytest.raises(ValueError, match=match):
        nw.col("a").rolling_quantile(
            3,
            quantile=quantile,
            interpolation=interpolation,  # type: ignore[arg-type]
        )
    with pytest.raises(ValueError, match=match):
        nw.from_native(
            __import__("pyarrow").table({"a": [1.0, 2.0, 3.0]}), eager_only=True
        )["a"].rolling_quantile(3, quantile=quantile, interpolation=interpolation)  # type: ignore[arg-type]
