import re
from dataclasses import dataclass, field
from typing import List, Optional, Sequence, Set, Tuple

from sqlfmt.comment import Comment
from sqlfmt.line import Line
from sqlfmt.mode import Mode
from sqlfmt.node import Node
from sqlfmt.node_manager import NodeManager
from sqlfmt.rules.common import CREATE_TABLE
from sqlfmt.tokens import TokenType

INLINE_CONSTRAINT_KEYWORDS = (
    "not null",
    "default",
    "references",
    "constraint",
    "check",
    "null",
)
TABLE_CONSTRAINT_KEYWORDS = (
    "primary key",
    "foreign key",
    "unique",
    "check",
    "constraint",
)
# a long table constraint may be wrapped before any of these keywords
CONSTRAINT_CLAUSE_KEYWORDS = (
    "primary key",
    "foreign key",
    "unique",
    "check",
    "references",
    "on delete",
    "on update",
    "match",
)

_CREATE_TABLE_PROG = re.compile(CREATE_TABLE, re.IGNORECASE)
_QUOTED_PROG = re.compile(r"""('(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"|`[^`]*`)""")
_WORD_TOKEN_TYPES = (
    TokenType.NAME,
    TokenType.WORD_OPERATOR,
    TokenType.UNTERM_KEYWORD,
    TokenType.BOOLEAN_OPERATOR,
    TokenType.ON,
)


def _normalize_words(text: str) -> str:
    return " ".join(text.lower().split())


def _lowercase_outside_quotes(text: str) -> str:
    parts = _QUOTED_PROG.split(text)
    return "".join(p if i % 2 else p.lower() for i, p in enumerate(parts))


@dataclass
class DdlColumn:
    name: str
    type_name: str
    has_inline_constraint: bool = False

    def __post_init__(self) -> None:
        self.type_name = _lowercase_outside_quotes(self.type_name.strip())

    def __str__(self) -> str:
        parts = [self.name]
        if self.type_name:
            parts.append(self.type_name)
        if self.has_inline_constraint:
            parts.append("<+constraint>")
        return " ".join(parts)


@dataclass
class DdlTableConstraint:
    keyword: str

    def __post_init__(self) -> None:
        self.keyword = _normalize_words(self.keyword)

    def __str__(self) -> str:
        return self.keyword


@dataclass
class DdlTable:
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
        return [c for c in self.columns if c.has_inline_constraint]

    @property
    def unconstrained_columns(self) -> List[DdlColumn]:
        return [c for c in self.columns if not c.has_inline_constraint]


@dataclass
class _CreateTableStatement:
    """
    The nodes of a CREATE TABLE statement, split into its component parts
    """

    header: List[Node]  # create table keyword(s) and the table name
    table_name: List[Node]
    open_paren: Node
    items: List[List[Node]]  # columns and table constraints, without commas
    commas: List[Node]  # commas[i] follows items[i]
    close_paren: Node
    post_body: List[Node]
    terminator: Optional[Node]
    end: int  # index of the first node after the statement


def _bracket_delta(node: Node) -> int:
    if node.token.type in (TokenType.BRACKET_OPEN, TokenType.STATEMENT_START):
        return 1
    elif node.token.type in (TokenType.BRACKET_CLOSE, TokenType.STATEMENT_END):
        return -1
    return 0


def _top_level_words(nodes: Sequence[Node]) -> List[Tuple[str, int]]:
    """
    Returns the (lowercased) words of keyword-like nodes that are not nested
    in brackets, with the index of the node that contains each word
    """
    words: List[Tuple[str, int]] = []
    depth = 0
    for i, node in enumerate(nodes):
        if depth == 0 and node.token.type in _WORD_TOKEN_TYPES:
            words.extend((word, i) for word in node.value.lower().split())
        depth += _bracket_delta(node)
    return words


def _find_keyword(
    words: List[Tuple[str, int]], keywords: Sequence[str], start: int = 0
) -> Optional[Tuple[str, int]]:
    """
    Returns the first keyword (and the index of its first node) that
    appears in words at or after position start
    """
    for pos in range(start, len(words)):
        for keyword in keywords:
            keyword_words = keyword.split()
            candidate = [w for w, _ in words[pos : pos + len(keyword_words)]]
            if candidate == keyword_words:
                return keyword, words[pos][1]
    return None


def _render(nodes: Sequence[Node]) -> str:
    return "".join(str(node) for node in nodes).strip()


def _create_keyword_length(nodes: Sequence[Node]) -> int:
    """
    Returns the number of nodes at the start of nodes that make up a
    CREATE TABLE keyword, or 0 if nodes do not start with CREATE TABLE
    """
    words: List[str] = []
    length = 0
    for i, node in enumerate(nodes[:12]):
        if node.token.type not in _WORD_TOKEN_TYPES:
            break
        words.append(_normalize_words(node.value))
        if _CREATE_TABLE_PROG.fullmatch(" ".join(words)):
            length = i + 1
    return length


def _parse_statement(nodes: Sequence[Node]) -> Optional[_CreateTableStatement]:
    """
    Splits a sequence of nodes (without newlines) that starts with
    CREATE TABLE into its component parts. Returns None if nodes do not
    start with CREATE TABLE <name> (...), or if the statement is a
    CREATE TABLE AS SELECT or CREATE TABLE ... LIKE
    """
    i = _create_keyword_length(nodes)
    if i == 0:
        return None

    table_name: List[Node] = []
    while i < len(nodes) and not (
        nodes[i].token.type is TokenType.BRACKET_OPEN and nodes[i].value == "("
    ):
        if nodes[i].divides_queries or _normalize_words(nodes[i].value) in (
            "as",
            "like",
            "clone",
            "copy",
        ):
            return None
        table_name.append(nodes[i])
        i += 1
    if i >= len(nodes) or not table_name:
        return None
    header = list(nodes[:i])
    open_paren = nodes[i]
    i += 1

    items: List[List[Node]] = [[]]
    commas: List[Node] = []
    depth = 0
    while i < len(nodes):
        node = nodes[i]
        delta = _bracket_delta(node)
        if node.divides_queries:
            return None
        elif depth == 0 and delta < 0:
            break
        elif depth == 0 and node.is_comma:
            commas.append(node)
            items.append([])
        else:
            items[-1].append(node)
            depth += delta
        i += 1
    else:
        return None
    close_paren = nodes[i]
    i += 1

    first_words = _top_level_words(items[0])
    if first_words and first_words[0] == ("like", 0):
        return None
    if i < len(nodes) and _normalize_words(nodes[i].value) == "as":
        return None

    post_body: List[Node] = []
    terminator: Optional[Node] = None
    while i < len(nodes):
        node = nodes[i]
        i += 1
        if node.token.type is TokenType.SEMICOLON:
            terminator = node
            break
        post_body.append(node)

    return _CreateTableStatement(
        header=header,
        table_name=table_name,
        open_paren=open_paren,
        items=items,
        commas=commas,
        close_paren=close_paren,
        post_body=post_body,
        terminator=terminator,
        end=i,
    )


def _table_constraint_keyword(item: Sequence[Node]) -> Optional[str]:
    """
    Returns the leading keyword of a table-level constraint, or None if
    item is a column definition
    """
    words = _top_level_words(item)
    if not words or words[0][1] != 0:
        return None
    match = _find_keyword(words[:2], TABLE_CONSTRAINT_KEYWORDS)
    if match is None or match[1] != 0:
        return None
    return match[0]


def _parse_column(item: Sequence[Node]) -> DdlColumn:
    name = item[0].value
    rest = item[1:]
    match = _find_keyword(_top_level_words(rest), INLINE_CONSTRAINT_KEYWORDS)
    type_end = match[1] if match is not None else len(rest)
    return DdlColumn(
        name=name,
        type_name=_render(rest[:type_end]),
        has_inline_constraint=match is not None,
    )


def parse_ddl_table(lines: List[Line]) -> Optional[DdlTable]:
    """
    Returns a DdlTable describing the first CREATE TABLE statement in lines,
    or None if lines do not contain a CREATE TABLE statement (CREATE TABLE AS
    SELECT and CREATE TABLE ... LIKE are not considered CREATE TABLE
    statements). lines may be split at any position (e.g., as lexed from
    the source, or as formatted).
    """
    nodes = [node for line in lines for node in line.nodes if not node.is_newline]
    start = 0
    while start < len(nodes):
        if _create_keyword_length(nodes[start:]):
            statement = _parse_statement(nodes[start:])
            if statement is None:
                return None
            columns: List[DdlColumn] = []
            constraints: List[DdlTableConstraint] = []
            for item in statement.items:
                if not item:
                    continue
                keyword = _table_constraint_keyword(item)
                if keyword is not None:
                    constraints.append(DdlTableConstraint(keyword=keyword))
                else:
                    columns.append(_parse_column(item))
            return DdlTable(
                table_name=_render(statement.table_name),
                columns=columns,
                table_constraints=constraints,
            )
        for offset, node in enumerate(nodes[start:], start=1):
            if node.token.type is TokenType.SEMICOLON:
                start += offset
                break
        else:
            break
    return None


def is_create_table_node(node: Node) -> bool:
    """
    True if node is the CREATE TABLE keyword lexed by the create_table rule
    """
    return node.token.type is TokenType.WORD_OPERATOR and bool(
        _CREATE_TABLE_PROG.fullmatch(node.value)
    )


@dataclass
class _IndentedLine(Line):
    """
    A Line that is indented further than its depth; used for the
    continuation lines of long table constraints
    """

    extra_indent: int = 0

    @property
    def prefix(self) -> str:
        return super().prefix + " " * 4 * self.extra_indent


@dataclass
class DdlFormatter:
    mode: Mode
    node_manager: NodeManager

    def format_lines(self, lines: List[Line]) -> Optional[List[Line]]:
        """
        Formats lines that contain exactly one CREATE TABLE statement
        (optionally terminated by a semicolon). Returns None if the statement
        cannot be formatted as DDL.
        """
        if not lines:
            return None

        nodes: List[Node] = []
        comments: List[Comment] = []
        follows_blank_line: Set[int] = set()
        pending_blank_line = False
        for line in lines:
            comments.extend(line.comments)
            if line.is_blank_line:
                pending_blank_line = True
                continue
            for node in line.nodes:
                if node.is_newline:
                    continue
                if node.formatting_disabled or node.is_jinja_statement:
                    return None
                if pending_blank_line:
                    follows_blank_line.add(id(node))
                    pending_blank_line = False
                nodes.append(node)

        statement = _parse_statement(nodes)
        if statement is None or statement.end != len(nodes):
            return None
        items = [] if statement.items == [[]] else statement.items
        if any(not item for item in items):
            return None

        groups: List[Tuple[List[Node], int]] = []
        groups.append((statement.header + [statement.open_paren], 0))
        for i, item in enumerate(items):
            item_nodes = item + ([statement.commas[i]] if i < len(items) - 1 else [])
            if _table_constraint_keyword(item) is not None:
                groups.extend(self._wrap_table_constraint(item_nodes))
            else:
                groups.append((item_nodes, 0))
        groups.append(([statement.close_paren], 0))
        groups.extend((clause, 0) for clause in self._split_post_body(statement))
        if statement.terminator is not None:
            groups.append(([statement.terminator], 0))

        # the paren that opens the column list follows the table name, but it
        # is not a function call
        statement.open_paren.prefix = " "

        new_lines: List[Line] = []
        previous_node = lines[0].previous_node
        for i, (group_nodes, extra_indent) in enumerate(groups):
            if i > 0 and id(group_nodes[0]) in follows_blank_line:
                blank_line = Line(previous_node=previous_node, nodes=[])
                self.node_manager.append_newline(blank_line)
                new_lines.append(blank_line)
                previous_node = blank_line.nodes[-1]
            line = self._create_line(previous_node, group_nodes, extra_indent)
            self.node_manager.append_newline(line)
            new_lines.append(line)
            previous_node = line.nodes[-1]

        self._attach_comments(new_lines, comments)
        return new_lines

    @staticmethod
    def _create_line(
        previous_node: Optional[Node], nodes: List[Node], extra_indent: int
    ) -> Line:
        if extra_indent:
            return _IndentedLine(
                previous_node=previous_node,
                nodes=list(nodes),
                extra_indent=extra_indent,
            )
        return Line(previous_node=previous_node, nodes=list(nodes))

    def _wrap_table_constraint(
        self, item_nodes: List[Node]
    ) -> List[Tuple[List[Node], int]]:
        """
        Table constraints are printed on a single line, unless that line is too
        long, in which case they are wrapped before sub-clause keywords (like
        references), with further-indented continuation lines. Argument lists
        are never split.
        """
        chunks: List[List[Node]] = []
        depth = 0
        for i, node in enumerate(item_nodes):
            if (
                i == 0
                or depth == 0
                and _normalize_words(node.value) in CONSTRAINT_CLAUSE_KEYWORDS
            ):
                chunks.append([node])
            else:
                chunks[-1].append(node)
            depth += _bracket_delta(node)

        wrapped: List[Tuple[List[Node], int]] = [(chunks[0], 0)]
        for chunk in chunks[1:]:
            current, extra_indent = wrapped[-1]
            candidate = current + chunk
            if (
                len(self._create_line(None, candidate, extra_indent))
                <= self.mode.line_length
            ):
                wrapped[-1] = (candidate, extra_indent)
            else:
                wrapped.append((chunk, 1))
        return wrapped

    @staticmethod
    def _split_post_body(statement: _CreateTableStatement) -> List[List[Node]]:
        """
        Splits the nodes after the column list into clauses that each start
        with a keyword (like partition by)
        """
        clauses: List[List[Node]] = []
        depth = 0
        for node in statement.post_body:
            if depth == 0 and (not clauses or node.is_unterm_keyword):
                clauses.append([node])
            else:
                clauses[-1].append(node)
            depth += _bracket_delta(node)
        return clauses

    @staticmethod
    def _attach_comments(lines: List[Line], comments: List[Comment]) -> None:
        """
        Standalone comments are attached to the first line that follows them;
        inline comments are attached to the line they follow
        """
        content_lines = [line for line in lines if not line.is_blank_line]
        for comment in comments:
            spos = comment.token.spos
            if comment.is_standalone:
                target = next(
                    (line for line in content_lines if line.nodes[0].token.spos > spos),
                    content_lines[-1],
                )
            else:
                target = content_lines[0]
                for line in content_lines:
                    if line.nodes[0].token.spos < spos:
                        target = line
                    else:
                        break
            target.comments.append(comment)
