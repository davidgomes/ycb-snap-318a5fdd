from sqlfmt.api import format_string
from sqlfmt.ddl import (
    DdlColumn,
    DdlTable,
    DdlTableConstraint,
    parse_ddl_table,
)
from sqlfmt.mode import Mode


def test_ddl_value_equality() -> None:
    plain = DdlColumn("id", "int")
    assert plain == DdlColumn("id", "int", False)
    assert plain != DdlColumn("id", "int", True)
    assert "<+constraint>" not in str(plain)
    constrained = DdlColumn("id", "int", True)
    assert str(constrained) == "id int<+constraint>"

    assert DdlTableConstraint("CHECK") == DdlTableConstraint("check")
    assert DdlTableConstraint("CHECK").keyword == "check"

    empty = DdlTable("t", [])
    other = DdlTable("t", [], [])
    assert empty == other
    assert empty.column_count == 0
    assert empty.constraint_count == 0
    assert empty.constrained_columns == []
    assert empty.unconstrained_columns == []
    empty.table_constraints.append(DdlTableConstraint("unique"))
    assert other.table_constraints == []
    assert empty.constraint_count == 1


def test_parse_ddl_table_preserves_type_spacing_and_constraints() -> None:
    source = (
        "CREATE TABLE sch.Films (\n"
        "    code CHAR(5) CONSTRAINT firstkey PRIMARY KEY,\n"
        "    amount DECIMAL(10,  2) NOT NULL,\n"
        "    len INTERVAL HOUR TO MINUTE,\n"
        "    tags ARRAY<STRUCT<Foo INT64, Bar STRING>>,\n"
        "    PRIMARY KEY (code),\n"
        "    CHECK (amount > 0),\n"
        "    CONSTRAINT named UNIQUE (code)\n"
        ")\n"
        ";\n"
    )
    mode = Mode()
    query = mode.dialect.initialize_analyzer(88).parse_query(source)
    table = parse_ddl_table(query.lines)
    assert table is not None
    assert table.table_name == "sch.Films"
    assert table.column_count == 4
    assert table.constraint_count == 3
    assert table.columns[0] == DdlColumn("code", "char(5)", True)
    assert table.columns[1] == DdlColumn("amount", "decimal(10,  2)", True)
    assert table.columns[2] == DdlColumn("len", "interval hour to minute", False)
    assert table.columns[3].type_name == "array<struct<Foo int64, Bar string>>"
    assert table.columns[3].has_inline_constraint is False
    assert [c.keyword for c in table.table_constraints] == [
        "primary key",
        "check",
        "constraint",
    ]
    assert [c.name for c in table.constrained_columns] == ["code", "amount"]
    assert [c.name for c in table.unconstrained_columns] == ["len", "tags"]


def test_parse_ddl_table_returns_none_for_other_statements() -> None:
    mode = Mode()
    analyzer = mode.dialect.initialize_analyzer(88)
    for source in (
        "select 1;\n",
        "create table foo as select 1;\n",
        "create table foo like bar;\n",
        "create table foo (like bar);\n",
    ):
        assert parse_ddl_table(analyzer.parse_query(source).lines) is None


def test_create_table_formatting_and_idempotence() -> None:
    source = """
    CREATE TABLE IF NOT EXISTS sch.foo (
        id INT64 NOT NULL,
        amount numeric(10,2) DEFAULT 0,
        status STRING CHECK (status IN ('a', 'b')),
        parent INT REFERENCES Other_Table(id, name),
        PRIMARY KEY (id),
        FOREIGN KEY (parent) REFERENCES Other_Table(id)
    )
    PARTITION BY DATE(created_at)
    CLUSTER BY status
    OPTIONS(description = 'x');
    """
    mode = Mode()
    formatted = format_string(source, mode)
    assert formatted == (
        "create table if not exists sch.foo (\n"
        "    id int64 not null,\n"
        "    amount numeric(10, 2) default 0,\n"
        "    status string check (status in ('a', 'b')),\n"
        "    parent int references other_table(id, name),\n"
        "    primary key (id),\n"
        "    foreign key (parent) references other_table(id)\n"
        ")\n"
        "partition by date(created_at)\n"
        "cluster by status\n"
        "options (description = 'x')\n"
        ";\n"
    )
    assert format_string(formatted, mode) == formatted


def test_column_and_post_body_may_exceed_line_length() -> None:
    mode = Mode(line_length=40)
    source = (
        "create table t (\n"
        "    very_long_column numeric(38, 10) not null default 0,\n"
        "    constraint c check (a > 0 and b > 0 and c > 0 and d > 0)\n"
        ")\n"
        "partition by a_really_long_expression(column_one, column_two)\n"
        ";\n"
    )
    formatted = format_string(source, mode)
    assert format_string(formatted, mode) == formatted
    lines = formatted.splitlines()
    column = next(line for line in lines if "very_long_column" in line)
    partition = next(line for line in lines if line.startswith("partition by"))
    assert len(column) > 40
    assert len(partition) > 40
    assert all(len(line) <= 40 or line == column or line == partition for line in lines)
