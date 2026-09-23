from typing import List

import pytest

from sqlfmt.analyzer import Analyzer
from sqlfmt.api import format_string
from sqlfmt.ddl import (
    DdlColumn,
    DdlTable,
    DdlTableConstraint,
    parse_ddl_table,
)
from sqlfmt.line import Line
from sqlfmt.mode import Mode
from sqlfmt.query_formatter import QueryFormatter

SOURCE = """
CREATE TABLE IF NOT EXISTS My_Schema.Films (
    code CHAR(5) CONSTRAINT firstkey PRIMARY KEY,
    title VARCHAR(40)   NOT NULL,
    price NUMERIC(10,2) DEFAULT 0,
    tags ARRAY<STRUCT<name STRING, vals ARRAY<INT64>>>
    , len INTERVAL HOUR TO MINUTE,
    stamp TIMESTAMP WITH TIME ZONE NULL,
    did INTEGER REFERENCES distributors (did),
    PRIMARY KEY (code, title),
    CONSTRAINT fk_did FOREIGN KEY (did) REFERENCES distributors (did),
    UNIQUE (title),
    CHECK (price > 0)
) PARTITION BY DATE(stamp) OPTIONS(description="films");
"""

EXPECTED = DdlTable(
    table_name="my_schema.films",
    columns=[
        DdlColumn("code", "char(5)", True),
        DdlColumn("title", "varchar(40)", True),
        DdlColumn("price", "numeric(10, 2)", True),
        DdlColumn("tags", "array<struct<name string, vals array<int64>>>"),
        DdlColumn("len", "interval hour to minute"),
        DdlColumn("stamp", "timestamp with time zone", True),
        DdlColumn("did", "integer", True),
    ],
    table_constraints=[
        DdlTableConstraint("primary key"),
        DdlTableConstraint("constraint"),
        DdlTableConstraint("unique"),
        DdlTableConstraint("check"),
    ],
)


def _raw_lines(analyzer: Analyzer, source: str) -> List[Line]:
    return analyzer.parse_query(source_string=source.lstrip()).lines


def test_parse_ddl_table_raw_lines(default_analyzer: Analyzer) -> None:
    assert parse_ddl_table(_raw_lines(default_analyzer, SOURCE)) == EXPECTED


def test_parse_ddl_table_single_line_source(default_analyzer: Analyzer) -> None:
    source = " ".join(SOURCE.split())
    assert parse_ddl_table(_raw_lines(default_analyzer, source)) == EXPECTED


def test_parse_ddl_table_formatted_lines(
    default_mode: Mode, default_analyzer: Analyzer
) -> None:
    raw_query = default_analyzer.parse_query(source_string=SOURCE.lstrip())
    formatted_query = QueryFormatter(default_mode).format(raw_query)
    assert parse_ddl_table(formatted_query.lines) == EXPECTED

    formatted = format_string(SOURCE, default_mode)
    assert parse_ddl_table(_raw_lines(default_analyzer, formatted)) == EXPECTED


@pytest.mark.parametrize(
    "source",
    [
        "select 1",
        "create table foo as select 1",
        "create table foo as (select 1)",
        "create table foo (a int) as select 1 as a",
        "create table foo like bar",
        "create view foo as select 1",
        "",
    ],
)
def test_parse_ddl_table_not_create_table(
    default_analyzer: Analyzer, source: str
) -> None:
    assert parse_ddl_table(_raw_lines(default_analyzer, source)) is None


def test_ddl_table_properties(default_analyzer: Analyzer) -> None:
    table = parse_ddl_table(_raw_lines(default_analyzer, SOURCE))
    assert table is not None
    assert table.column_count == 7
    assert table.constraint_count == 4
    assert [c.name for c in table.constrained_columns] == [
        "code",
        "title",
        "price",
        "stamp",
        "did",
    ]
    assert [c.name for c in table.unconstrained_columns] == ["tags", "len"]


def test_ddl_classes_equality_and_str() -> None:
    assert DdlColumn("a", "int") == DdlColumn("a", "int", False)
    assert DdlColumn("a", "int") != DdlColumn("a", "int", True)
    assert DdlTableConstraint("PRIMARY   KEY") == DdlTableConstraint("primary key")
    assert DdlTable("t", [DdlColumn("a", "int")]) == DdlTable(
        "t", [DdlColumn("a", "int")], []
    )
    assert "<+constraint>" in str(DdlColumn("a", "int", True))
    assert "<+constraint>" not in str(DdlColumn("a", "int", False))


def test_type_name_stops_at_top_level_constraint_only(
    default_analyzer: Analyzer,
) -> None:
    source = "create table t (s struct<a int64 not null, b string> not null)"
    table = parse_ddl_table(_raw_lines(default_analyzer, source))
    assert table is not None
    assert table.columns == [DdlColumn("s", "struct<a int64 not null, b string>", True)]


@pytest.mark.parametrize("line_length", [88, 40])
def test_create_table_line_length(line_length: int) -> None:
    mode = Mode(line_length=line_length)
    source = (
        "create table t (id int, constraint fk_long_name foreign key (a, b, c) "
        "references other_table(a, b, c) on delete cascade, "
        "check (a > 0 and b > 0 and c > 0 and a < 1000000 and b < 1000000))"
    )
    formatted = format_string(source, mode)
    assert all(len(line) <= line_length for line in formatted.splitlines())
    assert format_string(formatted, mode) == formatted
