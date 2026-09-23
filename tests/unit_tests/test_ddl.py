from sqlfmt.api import format_string
from sqlfmt.ddl import (
    DdlColumn,
    DdlTable,
    DdlTableConstraint,
    parse_ddl_table,
)
from sqlfmt.mode import Mode


def test_ddl_models_compare_public_fields() -> None:
    plain = DdlColumn("amount", "decimal(10, 2)")
    also_plain = DdlColumn("amount", "decimal(10, 2)", False)
    constrained = DdlColumn("amount", "decimal(10, 2)", True)

    assert plain == also_plain
    assert plain != constrained
    assert str(plain) == "amount decimal(10, 2)"
    assert "<+constraint>" not in str(plain)
    assert str(constrained) == "amount decimal(10, 2) <+constraint>"

    assert DdlTableConstraint("PRIMARY KEY") == DdlTableConstraint("primary key")
    assert DdlTableConstraint("CHECK").keyword == "check"

    table = DdlTable("t", [plain, constrained])
    same = DdlTable(
        "t",
        [plain, constrained],
        [],
    )
    assert table == same
    assert table.column_count == 2
    assert table.constraint_count == 0
    assert table.constrained_columns == [constrained]
    assert table.unconstrained_columns == [plain]


def test_parse_ddl_table_preserves_type_spacing(default_mode: Mode) -> None:
    source = """
    CREATE TABLE analytics.events (
        amount    DECIMAL  (  10 ,   2 )   NOT NULL,
        name text,
        user_id bigint references users(id),
        PRIMARY KEY (amount),
        CONSTRAINT positive CHECK (amount > 0),
        CHECK (name <> '')
    );
    """
    query = default_mode.dialect.initialize_analyzer(
        line_length=default_mode.line_length
    ).parse_query(source_string=source.lstrip())
    table = parse_ddl_table(query.lines)

    assert table is not None
    assert table.table_name == "analytics.events"
    assert table.columns == [
        DdlColumn("amount", "decimal  (  10 ,   2 )", True),
        DdlColumn("name", "text", False),
        DdlColumn("user_id", "bigint", True),
    ]
    assert table.table_constraints == [
        DdlTableConstraint("primary key"),
        DdlTableConstraint("constraint"),
        DdlTableConstraint("check"),
    ]
    assert table.column_count == 3
    assert table.constraint_count == 3
    assert [column.name for column in table.constrained_columns] == [
        "amount",
        "user_id",
    ]
    assert [column.name for column in table.unconstrained_columns] == ["name"]

    formatted = format_string(source, default_mode)
    formatted_query = default_mode.dialect.initialize_analyzer(
        line_length=default_mode.line_length
    ).parse_query(source_string=formatted)
    formatted_table = parse_ddl_table(formatted_query.lines)
    assert formatted_table is not None
    assert formatted_table.table_name == table.table_name
    assert [column.name for column in formatted_table.columns] == [
        column.name for column in table.columns
    ]
    assert formatted_table.table_constraints == table.table_constraints
    assert formatted_table.columns[0] == DdlColumn("amount", "decimal(10, 2)", True)


def test_parse_ddl_table_returns_none_for_other_statements(default_mode: Mode) -> None:
    analyzer = default_mode.dialect.initialize_analyzer(
        line_length=default_mode.line_length
    )
    for source in (
        "select 1;",
        "create table copied as select 1;",
        "create table copied like source;",
        "create table new_t (like old_t);",
    ):
        query = analyzer.parse_query(source_string=source)
        assert parse_ddl_table(query.lines) is None


def test_create_table_layout_and_line_length(default_mode: Mode) -> None:
    long_default = "y" * 80
    source = (
        "select alpha, beta, gamma, delta, epsilon from events;\n"
        "create table events (\n"
        "  id INT primary key,\n"
        f"  description varchar(255) default '{long_default}',\n"
        "  user_id INT references users(id)\n"
        ")\n"
        "partition by some_really_long_function_name(event_ts);\n"
    )
    short = Mode(line_length=40)
    formatted = format_string(source, short)
    assert formatted == format_string(formatted, short)

    lines = formatted.splitlines()
    assert "create table events (" in lines
    assert "    id int primary key," in lines
    assert "    user_id int references users(id)" in lines
    assert ")" in lines
    assert ";" in lines
    description = next(line for line in lines if "description" in line)
    partition = next(line for line in lines if line.startswith("partition by"))
    assert len(description) > short.line_length
    assert len(partition) > short.line_length
    assert "default '" in description
    assert description.count("\n") == 0

    for line in lines:
        if line in {description, partition}:
            continue
        if line.startswith("select ") or line.startswith("from "):
            assert len(line) <= short.line_length
        if line.startswith("    ") and "description" not in line:
            assert len(line) <= short.line_length

    # the surrounding query is still wrapped at the line length
    assert "select alpha, beta, gamma, delta, epsilon" not in formatted
    assert default_mode.line_length > short.line_length
