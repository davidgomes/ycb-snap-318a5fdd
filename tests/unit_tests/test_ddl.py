from typing import List

from sqlfmt.api import format_string
from sqlfmt.ddl import (
    DdlColumn,
    DdlTable,
    DdlTableConstraint,
    parse_ddl_table,
)
from sqlfmt.dialect import ClickHouse, Polyglot
from sqlfmt.line import Line
from sqlfmt.mode import Mode


def _lines(source: str, dialect: str = "polyglot") -> List[Line]:
    mode = Mode(dialect_name=dialect)
    analyzer = mode.dialect.initialize_analyzer(line_length=mode.line_length)
    return analyzer.parse_query(source).lines


def test_parse_create_table_columns_and_constraints() -> None:
    source = """
        CREATE TABLE IF NOT EXISTS schema.Films (
            code CHAR(5) CONSTRAINT firstkey PRIMARY KEY,
            title VARCHAR(40) NOT NULL,
            amount DECIMAL( 10 ,2 ),
            payload STRUCT<Name STRING, Vals ARRAY<INT64>>,
            PRIMARY KEY (code),
            CHECK (amount > 0),
            CONSTRAINT c UNIQUE (code)
        )
    """
    table = parse_ddl_table(_lines(source))
    assert table == DdlTable(
        "schema.Films",
        [
            DdlColumn("code", "char(5)", True),
            DdlColumn("title", "varchar(40)", True),
            DdlColumn("amount", "decimal( 10 ,2 )", False),
            DdlColumn("payload", "struct<Name string, Vals array<int64>>", False),
        ],
        [
            DdlTableConstraint("PRIMARY KEY"),
            DdlTableConstraint("check"),
            DdlTableConstraint("Constraint"),
        ],
    )
    assert table is not None
    assert table.column_count == 4
    assert table.constraint_count == 3
    assert [column.name for column in table.constrained_columns] == ["code", "title"]
    assert [column.name for column in table.unconstrained_columns] == [
        "amount",
        "payload",
    ]
    assert str(table.columns[0]) == "code char(5) <+constraint>"
    assert str(table.columns[2]) == "amount decimal( 10 ,2 )"


def test_parse_timestamp_with_time_zone_splits_columns() -> None:
    source = (
        "create table t ("
        "c timestamp with time zone, "
        "d double precision, "
        "b character varying (10)"
        ");"
    )
    table = parse_ddl_table(_lines(source))
    assert table is not None
    assert [(column.name, column.type_name) for column in table.columns] == [
        ("c", "timestamp with time zone"),
        ("d", "double precision"),
        ("b", "character varying (10)"),
    ]


def test_parse_ignores_non_create_table() -> None:
    assert parse_ddl_table(_lines("select 1;")) is None
    like = parse_ddl_table(_lines("create table child (like parent);"))
    assert like is not None
    assert like.table_name == "child"
    assert like.columns == []


def test_parse_data_create_table_still_reads_columns() -> None:
    source = """-- fmt: off
create table films (
    code char(5) not null,
    primary key (code)
);
-- fmt: on
"""
    table = parse_ddl_table(_lines(source))
    assert table == DdlTable(
        "films",
        [DdlColumn("code", "char(5)", True)],
        [DdlTableConstraint("primary key")],
    )


def test_format_create_table_layout() -> None:
    source = """
        CREATE TABLE IF NOT EXISTS schema.Films (
            code CHAR(5) CONSTRAINT firstkey PRIMARY KEY,
            amount DECIMAL(10,2),
            payload STRUCT<Name STRING, Vals ARRAY<INT64>>,
            PRIMARY KEY (code, title),
            CHECK(amount>0)
        )
        PARTITION BY date_prod
        OPTIONS(description='x', friendly_name='y');
    """
    expected = """create table if not exists schema.films (
    code char(5) constraint firstkey primary key,
    amount decimal(10, 2),
    payload struct<name string, vals array<int64>>,
    primary key (code, title),
    check (amount > 0)
)
partition by date_prod
options(description = 'x', friendly_name = 'y')
;
"""
    assert format_string(source, Mode()) == expected
    assert format_string(expected, Mode()) == expected


def test_as_and_like_pass_through() -> None:
    source = "CREATE TABLE foo AS\nSELECT 1 AS a, 2 AS b;\n"
    assert format_string(source, Mode()) == source
    like = "CREATE TABLE new LIKE old;\n"
    assert format_string(like, Mode()) == like


def test_type_continuation_stays_one_column_per_line() -> None:
    source = (
        "create table t (c timestamp with time zone, "
        "d double precision, b character varying (10));\n"
    )
    expected = (
        "create table t (\n"
        "    c timestamp with time zone,\n"
        "    d double precision,\n"
        "    b character varying(10)\n"
        ")\n"
        ";\n"
    )
    assert format_string(source, Mode()) == expected


def test_clickhouse_type_names_are_lowercased() -> None:
    source = "CREATE TABLE T (Id INT, Name VARCHAR(10));\n"
    assert (
        format_string(source, Mode(dialect_name="clickhouse"))
        == "create table T (\n    Id int,\n    Name varchar(10)\n)\n;\n"
    )
    table = parse_ddl_table(_lines(source, "clickhouse"))
    assert table == DdlTable(
        "T",
        [DdlColumn("Id", "int"), DdlColumn("Name", "varchar(10)")],
    )


def test_dialects_construct() -> None:
    assert isinstance(Polyglot(), Polyglot)
    assert isinstance(ClickHouse(), ClickHouse)
