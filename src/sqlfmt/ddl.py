import re
from dataclasses import dataclass, field, replace
from typing import Dict, List, Optional, Tuple

from sqlfmt.comment import Comment
from sqlfmt.line import Line
from sqlfmt.mode import Mode
from sqlfmt.node import Node
from sqlfmt.node_manager import NodeManager
from sqlfmt.rules.common import CREATE_TABLE
from sqlfmt.tokens import TokenType

CREATE_TABLE_KEYWORD = re.compile(CREATE_TABLE, re.IGNORECASE)
TABLE_CONSTRAINT_KEYWORDS = (
    "constraint",
    "primary key",
    "foreign key",
    "unique",
    "check",
)
INLINE_CONSTRAINT_KEYWORDS = (
    "not null",
    "default",
    "references",
    "constraint",
    "check",
    "null",
)
# a table constraint that is too long for one line is split before these
CONSTRAINT_CLAUSE_KEYWORDS = (
    "primary key",
    "foreign key",
    "unique",
    "check",
    "references",
    "on delete",
    "on update",
)


@dataclass
class DdlColumn:
    """
    A column definition in a CREATE TABLE statement. type_name is everything
    between the column name and the first inline constraint (if any)
    """

    name: str
    type_name: str
    has_inline_constraint: bool = False

    def __str__(self) -> str:
        definition = f"{self.name} {self.type_name}".rstrip()
        if self.has_inline_constraint:
            return f"{definition} <+constraint>"
        return definition


@dataclass
class DdlTableConstraint:
    """
    A table-level constraint in a CREATE TABLE statement, like
    PRIMARY KEY (a, b) or CONSTRAINT my_check CHECK (a > b)
    """

    keyword: str

    def __post_init__(self) -> None:
        self.keyword = " ".join(self.keyword.lower().split())


@dataclass
class DdlTable:
    """
    The columns and table-level constraints defined by a CREATE TABLE statement
    """

    table_name: str
    columns: List[DdlColumn]
    table_constraints: List[DdlTableConstraint] = field(default_factory=list)

    @property
    def column_count(self) -> int:
        return len(self.columns)

    @property
    def constraint_count(self) -> int:
        return len(self.table_constraints)

    @property
    def constrained_columns(self) -> List[DdlColumn]:
        return [column for column in self.columns if column.has_inline_constraint]

    @property
    def unconstrained_columns(self) -> List[DdlColumn]:
        return [column for column in self.columns if not column.has_inline_constraint]


def parse_ddl_table(lines: List[Line]) -> Optional[DdlTable]:
    """
    Returns a DdlTable for the first CREATE TABLE statement (with column
    definitions) in lines, or None if lines do not contain one. The lines
    can be split in any way (e.g., as lexed from the source, or formatted).
    """
    nodes = [
        node
        for line in lines
        for node in line.nodes
        if not (node.is_newline or node.is_jinja_statement)
    ]
    for start in range(len(nodes)):
        statement = _CreateTableStatement.from_nodes(nodes, start)
        if statement is not None:
            return statement.to_ddl_table()
    return None


def _is_keyword(node: Node, keywords: Tuple[str, ...]) -> bool:
    return node.token.type is TokenType.WORD_OPERATOR and node.value in keywords


def _is_create_table_keyword(node: Node) -> bool:
    return node.is_unterm_keyword and bool(CREATE_TABLE_KEYWORD.fullmatch(node.value))


def _render(nodes: List[Node], lowercase_names: bool = False) -> str:
    return "".join(
        [
            node.prefix
            + (
                node.value.lower()
                if lowercase_names and node.token.type is TokenType.NAME
                else node.value
            )
            for node in nodes
        ]
    ).strip()


@dataclass
class _CreateTableStatement:
    header: List[Node]  # create table keyword, table name, and opening paren
    items: List[List[Node]]  # columns and table constraints, with their commas
    closing_paren: Node
    tail: List[Node]  # clauses after the closing paren, and the semicolon

    @classmethod
    def from_nodes(
        cls, nodes: List[Node], start: int
    ) -> Optional["_CreateTableStatement"]:
        """
        Returns the structure of the CREATE TABLE statement that begins at
        nodes[start], or None if a CREATE TABLE statement with column definitions
        does not begin there. nodes must not include newlines.
        """
        if not _is_create_table_keyword(nodes[start]):
            return None

        idx = start + 1
        expecting_name = True
        while idx < len(nodes):
            token_type = nodes[idx].token.type
            if expecting_name and token_type in (
                TokenType.NAME,
                TokenType.QUOTED_NAME,
                TokenType.JINJA_EXPRESSION,
            ):
                expecting_name = False
            elif not expecting_name and token_type is TokenType.DOT:
                expecting_name = True
            else:
                break
            idx += 1
        if (
            expecting_name
            or idx == len(nodes)
            or nodes[idx].token.type is not TokenType.BRACKET_OPEN
            or nodes[idx].value != "("
        ):
            return None
        header = nodes[start : idx + 1]

        items: List[List[Node]] = [[]]
        depth = 0
        idx += 1
        while idx < len(nodes):
            node = nodes[idx]
            if node.is_closing_bracket:
                if depth == 0:
                    break
                depth -= 1
            elif node.is_opening_bracket:
                depth += 1
            elif node.token.type is TokenType.SEMICOLON:
                return None
            items[-1].append(node)
            if depth == 0 and node.is_comma:
                items.append([])
            idx += 1
        else:
            return None
        closing_paren = nodes[idx]

        tail: List[Node] = []
        for node in nodes[idx + 1 :]:
            tail.append(node)
            if node.token.type is TokenType.SEMICOLON:
                break

        return cls(
            header=header,
            items=[item for item in items if item],
            closing_paren=closing_paren,
            tail=tail,
        )

    @property
    def node_count(self) -> int:
        return (
            len(self.header)
            + sum([len(item) for item in self.items])
            + 1
            + len(self.tail)
        )

    def to_ddl_table(self) -> DdlTable:
        columns: List[DdlColumn] = []
        constraints: List[DdlTableConstraint] = []
        for item in self.items:
            nodes = item[:-1] if item[-1].is_comma else item
            if not nodes:
                continue
            elif _is_keyword(nodes[0], TABLE_CONSTRAINT_KEYWORDS):
                constraints.append(DdlTableConstraint(keyword=nodes[0].value))
            else:
                columns.append(self._parse_column(nodes))
        return DdlTable(
            table_name=_render(self.header[1:-1]),
            columns=columns,
            table_constraints=constraints,
        )

    @staticmethod
    def _parse_column(nodes: List[Node]) -> DdlColumn:
        type_nodes: List[Node] = []
        has_inline_constraint = False
        depth = 0
        for node in nodes[1:]:
            if depth == 0 and _is_keyword(node, INLINE_CONSTRAINT_KEYWORDS):
                has_inline_constraint = True
                break
            elif node.is_opening_bracket:
                depth += 1
            elif node.is_closing_bracket:
                depth -= 1
            type_nodes.append(node)
        return DdlColumn(
            name=nodes[0].value,
            type_name=_render(type_nodes, lowercase_names=True),
            has_inline_constraint=has_inline_constraint,
        )

    def groups(self) -> List[Tuple[str, List[Node]]]:
        """
        Returns the nodes of this statement, in order, grouped by the
        line they start: the header, each item, the closing paren, each
        clause after the closing paren, and the semicolon
        """
        groups: List[Tuple[str, List[Node]]] = [
            ("header", self.header),
            *[("item", item) for item in self.items],
            ("closing_paren", [self.closing_paren]),
        ]
        for node in self.tail:
            if node.is_unterm_keyword or node.token.type is TokenType.SEMICOLON:
                groups.append(("clause", [node]))
            else:
                groups[-1][1].append(node)
        return groups


def _standalone_except_last(comments: List[Comment]) -> List[Comment]:
    """
    A line renders its standalone comments above it and any inline comments
    after it, so only the last comment can be inline without reordering them
    """
    return [
        (
            comment
            if idx == len(comments) - 1 or comment.is_standalone
            else replace(comment, is_standalone=True)
        )
        for idx, comment in enumerate(comments)
    ]


@dataclass
class DdlFormatter:
    """
    Lays out CREATE TABLE statements with column definitions, which the
    LineMerger does not merge. The header (through the opening paren), each
    column definition or table constraint, the closing paren, each clause
    after the closing paren, and the semicolon get their own lines.
    """

    mode: Mode

    def __post_init__(self) -> None:
        self.node_manager = NodeManager(self.mode.dialect.case_sensitive_names)

    def format(self, lines: List[Line]) -> List[Line]:
        """
        Lays out each CREATE TABLE statement in lines, which must already be
        split. Returns all lines; the lines of CREATE TABLE statements can't
        be merged.
        """
        new_lines: List[Line] = []
        idx = 0
        while idx < len(lines):
            end = self._end_of_create_table(lines, idx)
            if end is None:
                new_lines.append(lines[idx])
                idx += 1
            else:
                new_lines.extend(self._format_create_table(lines[idx:end]))
                idx = end
        return new_lines

    def _end_of_create_table(self, lines: List[Line], start: int) -> Optional[int]:
        """
        If lines[start] begins a CREATE TABLE statement with column definitions,
        returns the index after the last line of that statement; otherwise
        returns None
        """
        if not lines[start].nodes or not _is_create_table_keyword(
            lines[start].nodes[0]
        ):
            return None

        nodes: List[Node] = []
        end = start
        for end in range(start, len(lines)):
            nodes.extend([node for node in lines[end].nodes if not node.is_newline])
            if any(node.token.type is TokenType.SEMICOLON for node in nodes):
                break

        statement = _CreateTableStatement.from_nodes(nodes, 0)
        if statement is None or statement.node_count != len(nodes):
            return None
        return end + 1

    def _format_create_table(self, lines: List[Line]) -> List[Line]:
        """
        Returns new lines for the lines of a single CREATE TABLE statement.
        Blank lines and lines with formatting disabled are kept as-is, unless
        a blank line falls within the contents of a new line.
        """
        nodes: List[Node] = []
        comments: Dict[int, List[Comment]] = {}
        # lines to keep as-is, keyed by the index of their first (or next) node
        kept_lines: Dict[int, List[Line]] = {}
        for line in lines:
            content = [node for node in line.nodes if not node.is_newline]
            if line.formatting_disabled or not content:
                kept_lines.setdefault(len(nodes), []).append(line)
            else:
                comments[len(nodes)] = line.comments
            nodes.extend(content)

        statement = _CreateTableStatement.from_nodes(nodes, 0)
        assert statement is not None and statement.node_count == len(nodes), (
            "Internal Error! Could not format CREATE TABLE statement. Please "
            "open an issue."
        )

        new_lines: List[Line] = []

        def add_lines(new: List[Line]) -> None:
            for line in new:
                line.can_merge = False
            new_lines.extend(new)

        def previous_node() -> Optional[Node]:
            return new_lines[-1].nodes[-1] if new_lines else lines[0].previous_node

        start = skip_until = 0
        for kind, group in statement.groups():
            run: List[Node] = []
            run_comments: List[Comment] = []
            for idx in range(start, start + len(group)):
                for kept_line in kept_lines.get(idx, []):
                    if kept_line.formatting_disabled or not run:
                        add_lines(
                            self._lines_from_run(
                                kind, run, run_comments, previous_node()
                            )
                        )
                        run, run_comments = [], []
                        add_lines([kept_line])
                        skip_until = idx + len(
                            [node for node in kept_line.nodes if not node.is_newline]
                        )
                    else:
                        run_comments.extend(kept_line.comments)
                if idx >= skip_until:
                    run_comments.extend(comments.get(idx, []))
                    run.append(nodes[idx])
            add_lines(self._lines_from_run(kind, run, run_comments, previous_node()))
            start += len(group)

        add_lines(kept_lines.get(len(nodes), []))
        return new_lines

    def _lines_from_run(
        self,
        kind: str,
        nodes: List[Node],
        comments: List[Comment],
        previous_node: Optional[Node],
    ) -> List[Line]:
        """
        Returns the line(s) for a run of nodes from a single group
        (see _CreateTableStatement.groups)
        """
        runs = self._split_jinja_statements(nodes)
        if len(runs) == 1 and self._is_too_long(runs[0], previous_node):
            if kind == "header" and len(runs[0]) > 2:
                # put the table name on its own line
                runs = [runs[0][:1], runs[0][1:]]
            elif kind == "item" and _is_keyword(runs[0][0], TABLE_CONSTRAINT_KEYWORDS):
                runs = self._split_table_constraint(runs[0])

        comments = _standalone_except_last(comments)
        head_comments = [c for c in comments if c.is_standalone or c.is_multiline]
        tail_comments = [c for c in comments if not (c.is_standalone or c.is_multiline)]
        lines: List[Line] = []
        for idx, run in enumerate(runs):
            line = Line.from_nodes(
                previous_node=previous_node,
                nodes=run.copy(),
                comments=(head_comments if idx == 0 else [])
                + (tail_comments if idx == len(runs) - 1 else []),
            )
            self.node_manager.append_newline(line)
            lines.append(line)
            previous_node = line.nodes[-1]
        return lines

    @staticmethod
    def _split_jinja_statements(nodes: List[Node]) -> List[List[Node]]:
        """
        Jinja statements (like {% if %} and {% endif %}) get their own lines
        """
        runs: List[List[Node]] = [[]]
        for node in nodes:
            if node.is_jinja_statement:
                runs.extend([[node], []])
            else:
                runs[-1].append(node)
        return [run for run in runs if run]

    @staticmethod
    def _split_table_constraint(nodes: List[Node]) -> List[List[Node]]:
        """
        Splits a table constraint before each of its clauses, without splitting
        any argument lists. Lines after the first are indented one more level.
        """
        runs: List[List[Node]] = [[]]
        depth = 0
        for node in nodes:
            if (
                depth == 0
                and runs[-1]
                and _is_keyword(node, CONSTRAINT_CLAUSE_KEYWORDS)
            ):
                runs.append(
                    [replace(node, open_brackets=[*node.open_brackets, nodes[0]])]
                )
            else:
                runs[-1].append(node)
            if node.is_opening_bracket:
                depth += 1
            elif node.is_closing_bracket:
                depth -= 1
        return runs

    def _is_too_long(self, nodes: List[Node], previous_node: Optional[Node]) -> bool:
        line = Line.from_nodes(previous_node=previous_node, nodes=nodes, comments=[])
        return line.is_too_long(self.mode.line_length)
