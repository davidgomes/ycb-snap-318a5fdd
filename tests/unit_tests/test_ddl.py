from typing import List

import pytest

from sqlfmt.ddl import (
    DdlColumn,
    DdlFormatter,
    DdlTable,
    DdlTableConstraint,
    parse_ddl_table,
)
from sqlfmt.line import Line
from sqlfmt.mode import Mode
from sqlfmt.query_formatter import QueryFormatter


def parse(source_string: str, mode: Mode) -> List[Line]:
    analyzer = mode.dialect.initialize_analyzer(mode.line_length)
    return analyzer.parse_query(source_string).lines


def format_lines(source_string: str, mode: Mode) -> List[Line]:
    analyzer = mode.dialect.initialize_analyzer(mode.line_length)
    raw_query = analyzer.parse_query(source_string)
    return QueryFormatter(mode).format(raw_query).lines


FILMS = """
CREATE TABLE films (
    code        char(5) CONSTRAINT firstkey PRIMARY KEY,
    title       varchar(40) NOT NULL,
    did         integer NOT NULL,
    date_prod   date,
    kind        varchar(10),
    len         interval hour to minute
);
"""

FILMS_TABLE = DdlTable(
    table_name="films",
    columns=[
        DdlColumn("code", "char(5)", True),
        DdlColumn("title", "varchar(40)", True),
        DdlColumn("did", "integer", True),
        DdlColumn("date_prod", "date"),
        DdlColumn("kind", "varchar(10)"),
        DdlColumn("len", "interval hour to minute"),
    ],
)


def test_ddl_column_equality() -> None:
    assert DdlColumn("a", "int") == DdlColumn("a", "int", False)
    assert DdlColumn("a", "int") != DdlColumn("a", "int", True)
    assert DdlColumn("a", "int") != DdlColumn("a", "bigint")
    assert DdlColumn("a", "int") != DdlColumn("b", "int")


def test_ddl_column_str() -> None:
    assert "<+constraint>" in str(DdlColumn("code", "char(5)", True))
    assert "<+constraint>" not in str(DdlColumn("code", "char(5)"))
    assert str(DdlColumn("code", "char(5)")) == "code char(5)"


def test_ddl_table_constraint_is_lowercase() -> None:
    constraint = DdlTableConstraint("PRIMARY   KEY")
    assert constraint.keyword == "primary key"
    assert constraint == DdlTableConstraint("primary key")
    assert constraint != DdlTableConstraint("unique")


def test_ddl_table_properties() -> None:
    table = DdlTable(
        "t",
        [DdlColumn("a", "int", True), DdlColumn("b", "int"), DdlColumn("c", "int")],
        [DdlTableConstraint("unique"), DdlTableConstraint("check")],
    )
    assert table.column_count == 3
    assert table.constraint_count == 2
    assert table.constrained_columns == [DdlColumn("a", "int", True)]
    assert table.unconstrained_columns == [DdlColumn("b", "int"), DdlColumn("c", "int")]

    assert DdlTable("t", []) == DdlTable("t", [], [])
    assert DdlTable("t", []).constraint_count == 0
    assert DdlTable("t", []) != DdlTable("u", [])


def test_parse_ddl_table(default_mode: Mode) -> None:
    assert parse_ddl_table(parse(FILMS, default_mode)) == FILMS_TABLE


def test_parse_ddl_table_any_line_splits(default_mode: Mode) -> None:
    one_line = " ".join(FILMS.split())
    many_lines = "\n".join(FILMS.split())
    assert parse_ddl_table(parse(one_line, default_mode)) == FILMS_TABLE
    assert parse_ddl_table(parse(many_lines, default_mode)) == FILMS_TABLE
    assert parse_ddl_table(format_lines(FILMS, default_mode)) == FILMS_TABLE


@pytest.mark.parametrize(
    "column_definition,expected_type_name",
    [
        ("a numeric(10,2)", "numeric(10, 2)"),
        ("a NUMERIC ( 10 , 2 ) not null", "numeric(10, 2)"),
        ("a DOUBLE PRECISION", "double precision"),
        ("a timestamp with time zone default now()", "timestamp with time zone"),
        ("a int[][]", "int[][]"),
        ("a ARRAY<STRUCT<b INT64 NOT NULL>>", "array<struct<b int64 not null>>"),
        ('a text collate "C" not null', 'text collate "C"'),
        ("a serial primary key", "serial primary key"),
        ("a int unique", "int unique"),
        ("a", ""),
    ],
)
def test_parse_ddl_table_type_name(
    column_definition: str, expected_type_name: str, default_mode: Mode
) -> None:
    source_string = f"create table t ({column_definition})"
    table = parse_ddl_table(parse(source_string, default_mode))
    assert table is not None
    assert table.columns[0].type_name == expected_type_name


def test_parse_ddl_table_type_name_is_lowercase(clickhouse_mode: Mode) -> None:
    source_string = "CREATE TABLE T (Id UInt64 NOT NULL, Name Nullable(String))"
    table = parse_ddl_table(parse(source_string, clickhouse_mode))
    assert table == DdlTable(
        "T",
        [DdlColumn("Id", "uint64", True), DdlColumn("Name", "nullable(string)")],
    )


@pytest.mark.parametrize(
    "constraint",
    [
        "not null",
        "null",
        "default 1",
        "references t(id)",
        "constraint c check (a > 0)",
        "check (a > 0)",
    ],
)
def test_parse_ddl_table_inline_constraints(
    constraint: str, default_mode: Mode
) -> None:
    source_string = f"create table t (a int {constraint})"
    table = parse_ddl_table(parse(source_string, default_mode))
    assert table == DdlTable("t", [DdlColumn("a", "int", True)])


def test_parse_ddl_table_constraints(default_mode: Mode) -> None:
    source_string = """
    create table if not exists my_schema.orders (
        id int,
        customer_id int,
        CHECK (id > 0),
        CONSTRAINT pk PRIMARY KEY (id),
        primary key (id, customer_id),
        foreign key (customer_id) references customers(id),
        unique (id),
        constraint positive_customer check (customer_id > 0)
    )
    partition by id
    cluster by customer_id
    options (description = 'orders');
    """
    assert parse_ddl_table(parse(source_string, default_mode)) == DdlTable(
        table_name="my_schema.orders",
        columns=[DdlColumn("id", "int"), DdlColumn("customer_id", "int")],
        table_constraints=[
            DdlTableConstraint("check"),
            DdlTableConstraint("constraint"),
            DdlTableConstraint("primary key"),
            DdlTableConstraint("foreign key"),
            DdlTableConstraint("unique"),
            DdlTableConstraint("constraint"),
        ],
    )


def test_parse_ddl_table_names(default_mode: Mode) -> None:
    source_string = 'create or replace temp table `p.d.t` ("My Col" int, options json)'
    assert parse_ddl_table(parse(source_string, default_mode)) == DdlTable(
        "`p.d.t`", [DdlColumn('"My Col"', "int"), DdlColumn("options", "json")]
    )


def test_parse_ddl_table_ignores_jinja_statements(default_mode: Mode) -> None:
    source_string = """
    create table {{ this }} (
        a int,
        {% if foo %}
        b int
        {% endif %}
    )
    """
    assert parse_ddl_table(parse(source_string, default_mode)) == DdlTable(
        "{{ this }}", [DdlColumn("a", "int"), DdlColumn("b", "int")]
    )


def test_parse_ddl_table_finds_first_table(default_mode: Mode) -> None:
    source_string = "select 1;\ncreate table t (a int);\ncreate table u (b int);\n"
    assert parse_ddl_table(parse(source_string, default_mode)) == DdlTable(
        "t", [DdlColumn("a", "int")]
    )


@pytest.mark.parametrize(
    "source_string",
    [
        "",
        "select 1",
        "create table t as select 1",
        "create table t (a int) as select 1",
        "create table t like u",
        "create table t (like u)",
        "create table t clone u",
        "create table t (a int) with (fillfactor = 70)",
        "create function f(a int) returns int as (a)",
    ],
)
def test_parse_ddl_table_returns_none(source_string: str, default_mode: Mode) -> None:
    assert parse_ddl_table(parse(source_string, default_mode)) is None


def test_ddl_formatter_lines_cannot_merge(default_mode: Mode) -> None:
    lines = format_lines("create table t (a int, b int);\nselect 1;\n", default_mode)
    assert [str(line) for line in lines] == [
        "create table t (\n",
        "    a int,\n",
        "    b int\n",
        ")\n",
        ";\n",
        "select 1\n",
        ";\n",
    ]
    assert [line.can_merge for line in lines] == [False] * 5 + [True] * 2


def test_ddl_formatter_splits_long_table_constraints(default_mode: Mode) -> None:
    source_string = (
        "create table t (a int, b int, constraint my_long_constraint_name "
        "foreign key (a, b) references my_other_table(a, b) on delete cascade, "
        "constraint my_check check (a > b))"
    )
    lines = format_lines(source_string, default_mode)
    assert "".join([str(line) for line in lines]) == (
        "create table t (\n"
        "    a int,\n"
        "    b int,\n"
        "    constraint my_long_constraint_name\n"
        "        foreign key (a, b)\n"
        "        references my_other_table(a, b)\n"
        "        on delete cascade,\n"
        "    constraint my_check check (a > b)\n"
        ")\n"
    )


def test_ddl_formatter_splits_long_header() -> None:
    mode = Mode(line_length=40)
    source_string = "create table if not exists my_project.my_dataset.t (a int)"
    lines = format_lines(source_string, mode)
    assert "".join([str(line) for line in lines]) == (
        "create table if not exists\n    my_project.my_dataset.t (\n    a int\n)\n"
    )


def test_ddl_formatter_keeps_comment_order(default_mode: Mode) -> None:
    source_string = (
        "create table t (\n"
        "    a numeric( -- first\n"
        "    -- second\n"
        "    10, 2), -- third\n"
        "    b int\n"
        ")\n"
    )
    lines = format_lines(source_string, default_mode)
    assert "".join([line.render_with_comments(88) for line in lines]) == (
        "create table t (\n"
        "    -- first\n"
        "    -- second\n"
        "    a numeric(10, 2),  -- third\n"
        "    b int\n"
        ")\n"
    )


def test_ddl_formatter_ignores_other_statements(default_mode: Mode) -> None:
    formatter = DdlFormatter(default_mode)
    lines = parse("select 1;\ncreate table t as select 1;\n", default_mode)
    assert formatter.format(lines) == lines
