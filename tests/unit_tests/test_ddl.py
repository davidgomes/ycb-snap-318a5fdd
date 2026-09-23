from typing import List, Optional

import pytest

from sqlfmt.analyzer import Analyzer
from sqlfmt.api import format_string
from sqlfmt.ddl import DdlColumn, DdlTable, DdlTableConstraint, parse_ddl_table
from sqlfmt.line import Line
from sqlfmt.mode import Mode
from sqlfmt.query_formatter import QueryFormatter

SOURCE = """
CREATE TABLE IF NOT EXISTS ds.foo (
  id INT64 NOT NULL,
  amount NUMERIC(10,2),
  tags ARRAY<STRUCT<a INT64,
     b STRING>>,
  code CHAR(5) CONSTRAINT firstkey PRIMARY KEY,
  created DATE DEFAULT CURRENT_DATE(),
  CONSTRAINT pk PRIMARY KEY (id), CHECK (amount > 0),
  UNIQUE(code), FOREIGN KEY (id) REFERENCES other (id)
) PARTITION BY created;
""".strip()

EXPECTED = DdlTable(
    table_name="ds.foo",
    columns=[
        DdlColumn("id", "int64", has_inline_constraint=True),
        DdlColumn("amount", "numeric(10, 2)"),
        DdlColumn("tags", "array<struct<a int64, b string>>"),
        DdlColumn("code", "char(5)", has_inline_constraint=True),
        DdlColumn("created", "date", has_inline_constraint=True),
    ],
    table_constraints=[
        DdlTableConstraint("constraint"),
        DdlTableConstraint("check"),
        DdlTableConstraint("unique"),
        DdlTableConstraint("foreign key"),
    ],
)


def parse(analyzer: Analyzer, source: str) -> Optional[DdlTable]:
    return parse_ddl_table(analyzer.parse_query(source_string=source).lines)


def test_parse_raw_lines(default_analyzer: Analyzer) -> None:
    assert parse(default_analyzer, SOURCE) == EXPECTED


def test_parse_formatted_lines(default_mode: Mode, default_analyzer: Analyzer) -> None:
    raw_query = default_analyzer.parse_query(source_string=SOURCE)
    formatted_lines: List[Line] = QueryFormatter(default_mode).format(raw_query).lines
    assert parse_ddl_table(formatted_lines) == EXPECTED


def test_parse_formatted_string(default_mode: Mode, default_analyzer: Analyzer) -> None:
    formatted = format_string(SOURCE, default_mode)
    assert parse(default_analyzer, formatted) == EXPECTED


def test_table_properties(default_analyzer: Analyzer) -> None:
    table = parse(default_analyzer, SOURCE)
    assert table is not None
    assert table.column_count == 5
    assert table.constraint_count == 4
    assert [c.name for c in table.constrained_columns] == ["id", "code", "created"]
    assert [c.name for c in table.unconstrained_columns] == ["amount", "tags"]


@pytest.mark.parametrize(
    "source,expected_keywords",
    [
        ("create table t (a int, check (a > 0))", ["check"]),
        ("create table t (a int, constraint c check (a > 0))", ["constraint"]),
        (
            "create table t (a int, b int, primary key (a, b), unique (b))",
            ["primary key", "unique"],
        ),
        ("create table t (a int)", []),
    ],
)
def test_parse_table_constraints(
    default_analyzer: Analyzer, source: str, expected_keywords: List[str]
) -> None:
    table = parse(default_analyzer, source)
    assert table is not None
    assert [c.keyword for c in table.table_constraints] == expected_keywords


@pytest.mark.parametrize(
    "source,expected",
    [
        ("create table t (a int not null)", DdlColumn("a", "int", True)),
        ("create table t (a int null)", DdlColumn("a", "int", True)),
        ("create table t (a int default 1)", DdlColumn("a", "int", True)),
        ("create table t (a int references b(id))", DdlColumn("a", "int", True)),
        ("create table t (a int check (a > 0))", DdlColumn("a", "int", True)),
        ("create table t (a int constraint c unique)", DdlColumn("a", "int", True)),
        (
            "create table t (s struct<a int64 not null>)",
            DdlColumn("s", "struct<a int64 not null>"),
        ),
        (
            "create table t (d timestamp with time zone)",
            DdlColumn("d", "timestamp with time zone"),
        ),
        ("create table t (e enum('A', 'B'))", DdlColumn("e", "enum('A', 'B')")),
    ],
)
def test_parse_column(
    default_analyzer: Analyzer, source: str, expected: DdlColumn
) -> None:
    table = parse(default_analyzer, source)
    assert table is not None
    assert table.columns == [expected]


@pytest.mark.parametrize(
    "source",
    [
        "select 1",
        "create table foo as select 1",
        "create table foo (a, b) as select 1, 2",
        "create table foo like bar",
        "create table foo (like bar including all)",
        "create view foo as select 1",
    ],
)
def test_parse_not_create_table(default_analyzer: Analyzer, source: str) -> None:
    assert parse(default_analyzer, source) is None


def test_parse_create_table_after_other_statement(
    default_analyzer: Analyzer,
) -> None:
    table = parse(default_analyzer, "select 1; create table t (a int);")
    assert table == DdlTable("t", [DdlColumn("a", "int")])


def test_column_str() -> None:
    assert str(DdlColumn("a", "int", has_inline_constraint=True)) == (
        "a int <+constraint>"
    )
    assert "<+constraint>" not in str(DdlColumn("a", "int"))


def test_equality_and_normalization() -> None:
    assert DdlColumn("a", "INT64") == DdlColumn("a", "int64")
    assert DdlColumn("a", "int") != DdlColumn("a", "int", has_inline_constraint=True)
    assert DdlTableConstraint("PRIMARY  KEY").keyword == "primary key"
    assert DdlTable("t", [DdlColumn("a", "int")]) == DdlTable(
        "t", [DdlColumn("a", "int")], []
    )


def test_formatted_lines_fit_line_length(default_mode: Mode) -> None:
    source = (
        "create table orders (order_id bigint not null, "
        "constraint fk_orders_customer_region foreign key "
        "(customer_identifier_with_long_name, region_identifier_long) "
        "references customers_and_regions(customer_id, region_id) "
        "on delete cascade on update no action)"
    )
    formatted = format_string(source, default_mode)
    assert all(len(line) <= default_mode.line_length for line in formatted.splitlines())
