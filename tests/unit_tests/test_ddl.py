from sqlfmt.analyzer import Analyzer
from sqlfmt.api import format_string
from sqlfmt.ddl import (
    DdlColumn,
    DdlTable,
    DdlTableConstraint,
    parse_ddl_table,
)
from sqlfmt.mode import Mode


def test_parse_unformatted_and_formatted(default_analyzer: Analyzer) -> None:
    source = """
    CREATE TABLE IF NOT EXISTS Foo.Bar (
        id INT NOT NULL,
        name VARCHAR(10,  2),
        flag BOOLEAN NULL,
        PRIMARY KEY (id),
        CONSTRAINT c CHECK (id > 0),
        UNIQUE (name)
    )
    PARTITION BY id
    ;
    """
    raw = default_analyzer.parse_query(source_string=source)
    parsed = parse_ddl_table(raw.lines)
    assert parsed == DdlTable(
        table_name="foo.bar",
        columns=[
            DdlColumn("id", "int", True),
            DdlColumn("name", "varchar(10,  2)", False),
            DdlColumn("flag", "boolean", True),
        ],
        table_constraints=[
            DdlTableConstraint("primary key"),
            DdlTableConstraint("constraint"),
            DdlTableConstraint("unique"),
        ],
    )
    assert parsed is not None
    assert parsed.column_count == 3
    assert parsed.constraint_count == 3
    assert [c.name for c in parsed.constrained_columns] == ["id", "flag"]
    assert [c.name for c in parsed.unconstrained_columns] == ["name"]
    assert str(parsed.columns[0]) == "id int <+constraint>"
    assert str(parsed.columns[1]) == "name varchar(10,  2)"
    assert "<+constraint>" not in str(parsed.columns[1])

    formatted = format_string(source, Mode())
    again = default_analyzer.parse_query(source_string=formatted)
    formatted_table = parse_ddl_table(again.lines)
    assert formatted_table is not None
    assert formatted_table.table_name == parsed.table_name
    assert formatted_table.table_constraints == parsed.table_constraints
    assert [c.name for c in formatted_table.columns] == [c.name for c in parsed.columns]
    assert [c.has_inline_constraint for c in formatted_table.columns] == [
        c.has_inline_constraint for c in parsed.columns
    ]
    # formatting normalizes spaces after commas inside type parens
    assert formatted_table.columns[1].type_name == "varchar(10, 2)"


def test_parse_returns_none_for_select(default_analyzer: Analyzer) -> None:
    query = default_analyzer.parse_query(source_string="select 1;")
    assert parse_ddl_table(query.lines) is None


def test_parse_create_table_as_has_no_columns(default_analyzer: Analyzer) -> None:
    query = default_analyzer.parse_query(
        source_string="create table foo as select 1;"
    )
    parsed = parse_ddl_table(query.lines)
    assert parsed == DdlTable(table_name="foo", columns=[])


def test_column_equality_ignores_nothing_public() -> None:
    assert DdlColumn("a", "int") == DdlColumn("a", "int", False)
    assert DdlColumn("a", "int", True) != DdlColumn("a", "int", False)
    assert DdlTableConstraint("CHECK") == DdlTableConstraint("check")
