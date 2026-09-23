import pytest

from sqlfmt.analyzer import Analyzer
from sqlfmt.api import format_string
from sqlfmt.ddl import DdlColumn, DdlTable, DdlTableConstraint, parse_ddl_table
from sqlfmt.mode import Mode

SOURCE = (
    "CREATE TABLE IF NOT EXISTS my_db.Orders (id INT64 NOT NULL, "
    "amounts ARRAY<STRUCT<a INT64, b NUMERIC(10,2)>>, qty INT64 CHECK(qty > 0), "
    "PRIMARY KEY(id), CONSTRAINT fk FOREIGN KEY(id) REFERENCES c(id), "
    "CHECK(qty < 100), UNIQUE(qty)) PARTITION BY DATE(ts);"
)


@pytest.mark.parametrize("formatted", [False, True])
def test_parse_ddl_table(default_analyzer: Analyzer, formatted: bool) -> None:
    source = format_string(SOURCE, Mode()) if formatted else SOURCE
    table = parse_ddl_table(default_analyzer.parse_query(source).lines)
    assert table == DdlTable(
        table_name="my_db.orders",
        columns=[
            DdlColumn("id", "int64", True),
            DdlColumn(
                "amounts",
                (
                    "array<struct<a int64, b numeric(10,2)>>"
                    if not formatted
                    else "array<struct<a int64, b numeric(10, 2)>>"
                ),
            ),
            DdlColumn("qty", "int64", True),
        ],
        table_constraints=[
            DdlTableConstraint("PRIMARY KEY"),
            DdlTableConstraint("constraint"),
            DdlTableConstraint("check"),
            DdlTableConstraint("unique"),
        ],
    )
    assert table.column_count == 3
    assert table.constraint_count == 4
    assert [c.name for c in table.constrained_columns] == ["id", "qty"]
    assert [c.name for c in table.unconstrained_columns] == ["amounts"]


def test_column_str() -> None:
    assert "<+constraint>" in str(DdlColumn("a", "int", True))
    assert "<+constraint>" not in str(DdlColumn("a", "int"))


@pytest.mark.parametrize(
    "source",
    ["create table t as select 1", "create table t like s", "select 1"],
)
def test_parse_ddl_table_not_create_table(
    default_analyzer: Analyzer, source: str
) -> None:
    assert parse_ddl_table(default_analyzer.parse_query(source).lines) is None


@pytest.mark.parametrize(
    "source", ["create table t as select 1\n", "create table t like s\n"]
)
def test_passthrough(source: str) -> None:
    assert format_string(source, Mode()) == source
