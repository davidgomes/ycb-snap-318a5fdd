from __future__ import annotations

from typing import Any, Literal

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

eager_cases: dict[str, dict[str, dict[str, Any]]] = {
    "rolling_min": {
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
    },
    "rolling_max": {
        "x1": {"kwargs": {"window_size": 3}, "expected": [None] * 6 + [11.0]},
        "x2": {
            "kwargs": {"window_size": 3, "min_samples": 1},
            "expected": [None, 1.0, 2.0, 2.0, 4.0, 6.0, 11.0],
        },
        "x3": {
            "kwargs": {"window_size": 2, "min_samples": 1},
            "expected": [None, 1.0, 2.0, 2.0, 4.0, 6.0, 11.0],
        },
        "x4": {
            "kwargs": {"window_size": 5, "min_samples": 1, "center": True},
            "expected": [2.0, 2.0, 4.0, 6.0, 11.0, 11.0, 11.0],
        },
        "x5": {
            "kwargs": {"window_size": 4, "min_samples": 1, "center": True},
            "expected": [1.0, 2.0, 2.0, 4.0, 6.0, 11.0, 11.0],
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
    "rolling_quantile": {
        "x1": {
            "kwargs": {"window_size": 3, "quantile": 0.75},
            "expected": [None] * 6 + [8.5],
        },
        "x2": {
            "kwargs": {"window_size": 3, "min_samples": 1, "quantile": 0.75},
            "expected": [None, 1.0, 1.75, 1.75, 3.5, 5.5, 8.5],
        },
        "x3": {
            "kwargs": {"window_size": 2, "min_samples": 1, "quantile": 0.75},
            "expected": [None, 1.0, 1.75, 2.0, 4.0, 5.5, 9.75],
        },
        "x4": {
            "kwargs": {
                "window_size": 5,
                "min_samples": 1,
                "center": True,
                "quantile": 0.75,
            },
            "expected": [1.75, 1.75, 3.0, 4.5, 7.25, 8.5, 8.5],
        },
        "x5": {
            "kwargs": {
                "window_size": 4,
                "min_samples": 1,
                "center": True,
                "quantile": 0.75,
            },
            "expected": [1.0, 1.75, 1.75, 3.0, 5.0, 8.5, 8.5],
        },
    },
}

OrderStat = Literal["rolling_min", "rolling_max", "rolling_median"]

lazy_ungrouped: dict[
    OrderStat, list[tuple[list[float | None], int, int | None, bool]]
] = {
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

lazy_grouped: dict[OrderStat, list[tuple[list[float | None], int, int | None, bool]]] = {
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

lazy_quantile_ungrouped: list[tuple[list[float | None], int, int | None, bool]] = [
    ([None, None, 1.75, None, None, 5.5, 9.75], 2, None, False),
    ([None, None, 1.75, None, None, 5.5, 9.75], 2, 2, False),
    ([None, None, 1.75, 1.75, 3.5, 5.5, 8.5], 3, 2, False),
    ([1.0, None, 1.75, 1.75, 3.5, 5.5, 8.5], 3, 1, False),
    ([1.75, 1.0, 1.75, 3.5, 5.5, 8.5, 9.75], 3, 1, True),
    ([1.75, 1.0, 1.75, 3.0, 5.0, 8.5, 8.5], 4, 1, True),
    ([1.75, 1.75, 3.0, 4.5, 7.25, 8.5, 8.5], 5, 1, True),
]

lazy_quantile_grouped: list[tuple[list[float | None], int, int | None, bool]] = [
    ([None, None, 1.75, None, None, 5.5, 9.75], 2, None, False),
    ([None, None, 1.75, None, None, 5.5, 9.75], 2, 2, False),
    ([None, None, 1.75, 1.75, None, 5.5, 8.5], 3, 2, False),
    ([1.0, None, 1.75, 1.75, 4.0, 5.5, 8.5], 3, 1, False),
    ([1.75, 1.0, 1.75, 2.0, 5.5, 8.5, 9.75], 3, 1, True),
    ([1.75, 1.0, 1.75, 1.75, 5.5, 8.5, 8.5], 4, 1, True),
    ([1.75, 1.75, 1.75, 1.75, 8.5, 8.5, 8.5], 5, 1, True),
]


def _skip_old_lazy(constructor: Constructor) -> None:
    if ("polars" in str(constructor) and POLARS_VERSION < (1, 10)) or (
        "duckdb" in str(constructor) and DUCKDB_VERSION < (1, 3)
    ):
        pytest.skip()
    if "modin" in str(constructor):
        pytest.skip()


@pytest.mark.parametrize("method", list(eager_cases))
def test_rolling_order_stat_expr(
    constructor_eager: ConstructorEager, method: str
) -> None:
    df = nw.from_native(constructor_eager(data))
    cases = eager_cases[method]
    result = df.select(
        **{
            name: getattr(nw.col("a"), method)(**values["kwargs"])
            for name, values in cases.items()
        }
    )
    expected = {name: values["expected"] for name, values in cases.items()}
    assert_equal_data(result, expected)


@pytest.mark.parametrize("method", list(eager_cases))
def test_rolling_order_stat_series(
    constructor_eager: ConstructorEager, method: str
) -> None:
    df = nw.from_native(constructor_eager(data), eager_only=True)
    cases = eager_cases[method]
    result = df.select(
        **{
            name: getattr(df["a"], method)(**values["kwargs"])
            for name, values in cases.items()
        }
    )
    expected = {name: values["expected"] for name, values in cases.items()}
    assert_equal_data(result, expected)


@pytest.mark.parametrize(
    ("interpolation", "expected"),
    [
        ("linear", [None, 1.0, 1.75, 1.75, 3.5, 5.5, 8.5]),
        ("lower", [None, 1.0, 1.0, 1.0, 2.0, 4.0, 6.0]),
        ("higher", [None, 1.0, 2.0, 2.0, 4.0, 6.0, 11.0]),
        ("nearest", [None, 1.0, 2.0, 2.0, 4.0, 6.0, 11.0]),
        ("midpoint", [None, 1.0, 1.5, 1.5, 3.0, 5.0, 8.5]),
    ],
)
def test_rolling_quantile_interpolation_expr(
    constructor_eager: ConstructorEager,
    interpolation: Literal["linear", "lower", "higher", "nearest", "midpoint"],
    expected: list[float | None],
) -> None:
    df = nw.from_native(constructor_eager(data))
    result = df.select(
        a=nw.col("a").rolling_quantile(
            3, quantile=0.75, interpolation=interpolation, min_samples=1
        )
    )
    assert_equal_data(result, {"a": expected})


@pytest.mark.parametrize("method", list(lazy_ungrouped))
@pytest.mark.parametrize("case_index", range(7))
def test_rolling_order_stat_expr_lazy_ungrouped(
    constructor: Constructor, method: OrderStat, case_index: int
) -> None:
    _skip_old_lazy(constructor)
    expected_a, window_size, min_samples, center = lazy_ungrouped[method][case_index]
    frame = {
        "a": [1, None, 2, None, 4, 6, 11],
        "b": [1, None, 2, 3, 4, 5, 6],
        "i": list(range(7)),
    }
    df = nw.from_native(constructor(frame))
    result = (
        df.with_columns(
            getattr(nw.col("a"), method)(
                window_size, min_samples=min_samples, center=center
            ).over(order_by="b")
        )
        .select("a", "i")
        .sort("i")
    )
    assert_equal_data(result, {"a": expected_a, "i": list(range(7))})


@pytest.mark.parametrize("method", list(lazy_grouped))
@pytest.mark.parametrize("case_index", range(7))
def test_rolling_order_stat_expr_lazy_grouped(
    constructor: Constructor,
    method: OrderStat,
    case_index: int,
    request: pytest.FixtureRequest,
) -> None:
    _skip_old_lazy(constructor)
    if "pandas" in str(constructor) and PANDAS_VERSION < (1, 2):
        pytest.skip()
    if any(x in str(constructor) for x in ("dask", "pyarrow_table")):
        request.applymarker(pytest.mark.xfail)
    expected_a, window_size, min_samples, center = lazy_grouped[method][case_index]
    frame = {
        "a": [1, None, 2, None, 4, 6, 11],
        "g": [1, 1, 1, 1, 2, 2, 2],
        "b": [1, None, 2, 3, 4, 5, 6],
        "i": list(range(7)),
    }
    df = nw.from_native(constructor(frame))
    result = (
        df.with_columns(
            getattr(nw.col("a"), method)(
                window_size, min_samples=min_samples, center=center
            ).over("g", order_by="b")
        )
        .sort("i")
        .select("a")
    )
    assert_equal_data(result, {"a": expected_a})


@pytest.mark.parametrize("case_index", range(7))
def test_rolling_quantile_expr_lazy_ungrouped(
    constructor: Constructor, case_index: int
) -> None:
    if "duckdb" in str(constructor) and "sqlframe" not in str(constructor):
        pytest.skip()
    _skip_old_lazy(constructor)
    expected_a, window_size, min_samples, center = lazy_quantile_ungrouped[case_index]
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
                window_size, quantile=0.75, min_samples=min_samples, center=center
            )
            .over(order_by="b")
        )
        .select("a", "i")
        .sort("i")
    )
    assert_equal_data(result, {"a": expected_a, "i": list(range(7))})


@pytest.mark.parametrize("case_index", range(7))
def test_rolling_quantile_expr_lazy_grouped(
    constructor: Constructor, case_index: int, request: pytest.FixtureRequest
) -> None:
    if "duckdb" in str(constructor) and "sqlframe" not in str(constructor):
        pytest.skip()
    _skip_old_lazy(constructor)
    if "pandas" in str(constructor) and PANDAS_VERSION < (1, 2):
        pytest.skip()
    if any(x in str(constructor) for x in ("dask", "pyarrow_table")):
        request.applymarker(pytest.mark.xfail)
    expected_a, window_size, min_samples, center = lazy_quantile_grouped[case_index]
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
                window_size, quantile=0.75, min_samples=min_samples, center=center
            )
            .over("g", order_by="b")
        )
        .sort("i")
        .select("a")
    )
    assert_equal_data(result, {"a": expected_a})


def test_rolling_quantile_duckdb_not_implemented(constructor: Constructor) -> None:
    if "duckdb" not in str(constructor) or "sqlframe" in str(constructor):
        pytest.skip()
    frame = {"a": [1.0, 2.0, 3.0], "i": [0, 1, 2]}
    df = nw.from_native(constructor(frame))
    with pytest.raises(NotImplementedError, match="percentile_cont"):
        df.with_columns(
            nw.col("a").rolling_quantile(window_size=2, quantile=0.5).over(order_by="i")
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
            window_size=3,
            quantile=quantile,
            interpolation=interpolation,  # type: ignore[arg-type]
        )


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
def test_rolling_min_invalid_window(
    window_size: int, min_samples: int | None, context: Any
) -> None:
    with context:
        nw.col("a").rolling_min(window_size=window_size, min_samples=min_samples)
