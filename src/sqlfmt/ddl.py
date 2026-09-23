import re
from dataclasses import dataclass, field
from typing import Dict, Iterator, List, Optional, Tuple

from sqlfmt.comment import Comment
from sqlfmt.line import Line
from sqlfmt.merger import LineMerger
from sqlfmt.mode import Mode
from sqlfmt.node import Node
from sqlfmt.node_manager import NodeManager
from sqlfmt.rules.common import CREATE_TABLE
from sqlfmt.splitter import LineSplitter
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
    "unique key",
    "check",
    "constraint",
)
# a table constraint that is too long is split before these keywords
CONTINUATION_KEYWORDS = (
    "references",
    "on delete",
    "on update",
    "not enforced",
)

_CREATE_TABLE_PROG = re.compile(CREATE_TABLE, re.IGNORECASE)
_KEYWORD_TYPES = (
    TokenType.WORD_OPERATOR,
    TokenType.BOOLEAN_OPERATOR,
    TokenType.UNTERM_KEYWORD,
)
_CASE_PRESERVING_TYPES = (
    TokenType.QUOTED_NAME,
    TokenType.JINJA_EXPRESSION,
    TokenType.JINJA_STATEMENT,
    TokenType.JINJA_BLOCK_START,
    TokenType.JINJA_BLOCK_KEYWORD,
    TokenType.JINJA_BLOCK_END,
    TokenType.DATA,
)


def _normalize(value: str) -> str:
    return " ".join(value.lower().split())


def _keyword(node: Node) -> Optional[str]:
    if node.token.type in _KEYWORD_TYPES:
        return _normalize(node.value)
    return None


def is_create_table_keyword(node: Node) -> bool:
    return node.token.type is TokenType.WORD_OPERATOR and bool(
        _CREATE_TABLE_PROG.fullmatch(_normalize(node.value))
    )


@dataclass
class DdlColumn:
    name: str
    type_name: str
    has_inline_constraint: bool = False

    def __str__(self) -> str:
        column = f"{self.name} {self.type_name}".rstrip()
        if self.has_inline_constraint:
            column += " <+constraint>"
        return column


@dataclass
class DdlTableConstraint:
    keyword: str

    def __post_init__(self) -> None:
        self.keyword = _normalize(self.keyword)

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
    The nodes of a CREATE TABLE statement, grouped by their
    role in the statement
    """

    header: List[Node]  # from the create keyword through the opening paren
    items: List[List[Node]]  # column definitions and table constraints
    body_close: Node
    clauses: List[List[Node]]  # partition by, options, etc.
    terminator: Optional[Node]
    trailing: List[Node]  # anything after the terminator


def _segment_statement(nodes: List[Node]) -> Optional[_CreateTableStatement]:
    """
    Group nodes (which must not include newlines) by their role in the first
    CREATE TABLE statement in nodes. Returns None if nodes do not contain a
    complete CREATE TABLE statement with a column list.
    """
    start = next((i for i, n in enumerate(nodes) if is_create_table_keyword(n)), None)
    if start is None:
        return None

    i = start + 1
    while i < len(nodes) and not (
        nodes[i].token.type is TokenType.BRACKET_OPEN and nodes[i].value == "("
    ):
        if (
            nodes[i].token.type is TokenType.SEMICOLON
            or nodes[i].is_opening_bracket
            or nodes[i].is_closing_bracket
        ):
            return None
        i += 1
    if i >= len(nodes):
        return None
    header = nodes[start : i + 1]

    items: List[List[Node]] = []
    item: List[Node] = []
    body_close: Optional[Node] = None
    depth = 1
    i += 1
    while i < len(nodes):
        node = nodes[i]
        i += 1
        if node.token.type is TokenType.SEMICOLON:
            return None
        elif node.is_opening_bracket:
            depth += 1
        elif node.is_closing_bracket:
            depth -= 1
            if depth == 0:
                body_close = node
                break
        elif depth == 1 and node.is_jinja_statement and not item:
            items.append([node])
            continue
        item.append(node)
        if depth == 1 and node.is_comma:
            items.append(item)
            item = []
    if body_close is None:
        return None
    if item:
        items.append(item)

    clauses: List[List[Node]] = []
    terminator: Optional[Node] = None
    depth = 0
    while i < len(nodes):
        node = nodes[i]
        i += 1
        if node.token.type is TokenType.SEMICOLON:
            terminator = node
            break
        elif node.is_opening_bracket:
            depth += 1
        elif node.is_closing_bracket:
            depth -= 1
        if (depth == 0 and node.is_unterm_keyword) or not clauses:
            clauses.append([node])
        else:
            clauses[-1].append(node)

    return _CreateTableStatement(
        header=header,
        items=items,
        body_close=body_close,
        clauses=clauses,
        terminator=terminator,
        trailing=nodes[i:],
    )


def _strip_trailing_comma(item: List[Node]) -> List[Node]:
    if item and item[-1].is_comma:
        return item[:-1]
    return item


def _is_table_constraint(item: List[Node]) -> bool:
    return bool(item) and _keyword(item[0]) in TABLE_CONSTRAINT_KEYWORDS


def _render(nodes: List[Node]) -> str:
    """
    Render nodes with single-space (or no) separators and lowercased
    keywords and names
    """
    parts: List[str] = []
    for node in nodes:
        prefix = " " if node.prefix else ""
        if node.token.type in _CASE_PRESERVING_TYPES:
            value = node.value
        else:
            value = _normalize(node.value)
        parts.append(f"{prefix}{value}")
    return "".join(parts).strip()


def _parse_column(item: List[Node]) -> DdlColumn:
    name, *rest = item
    type_nodes: List[Node] = []
    has_inline_constraint = False
    depth = 0
    for node in rest:
        if depth == 0 and _keyword(node) in INLINE_CONSTRAINT_KEYWORDS:
            has_inline_constraint = True
            break
        if node.is_opening_bracket:
            depth += 1
        elif node.is_closing_bracket:
            depth -= 1
        type_nodes.append(node)
    return DdlColumn(
        name=_render([name]),
        type_name=_render(type_nodes),
        has_inline_constraint=has_inline_constraint,
    )


def parse_ddl_table(lines: List[Line]) -> Optional[DdlTable]:
    """
    Returns a DdlTable describing the first CREATE TABLE statement in lines,
    or None if lines do not contain a CREATE TABLE statement with a column list.
    """
    nodes = [node for line in lines for node in line.nodes if not node.is_newline]
    statement = _segment_statement(nodes)
    if statement is None:
        return None

    columns: List[DdlColumn] = []
    constraints: List[DdlTableConstraint] = []
    for raw_item in statement.items:
        item = _strip_trailing_comma(raw_item)
        if not item or all(n.is_jinja_statement for n in item):
            continue
        elif _is_table_constraint(item):
            keyword = _keyword(item[0])
            assert keyword is not None
            constraints.append(DdlTableConstraint(keyword=keyword))
        else:
            columns.append(_parse_column(item))

    return DdlTable(
        table_name=_render(statement.header[1:-1]),
        columns=columns,
        table_constraints=constraints,
    )


def split_create_table_runs(lines: List[Line]) -> Iterator[Tuple[bool, List[Line]]]:
    """
    Split lines into runs of consecutive lines. Yields a tuple of
    (is_create_table, run) for each run, where CREATE TABLE runs span
    from the line starting with the create keyword to the line with
    the statement's terminating semicolon.
    """
    run: List[Line] = []
    in_create_table = False
    for line in lines:
        if (
            not in_create_table
            and line.nodes
            and is_create_table_keyword(line.nodes[0])
        ):
            if run:
                yield False, run
            run = []
            in_create_table = True
        run.append(line)
        if in_create_table and any(
            n.token.type is TokenType.SEMICOLON for n in line.nodes
        ):
            yield True, run
            run = []
            in_create_table = False

    if in_create_table:
        end = len(run)
        while end > 0 and all(n.is_newline for n in run[end - 1].nodes):
            end -= 1
        yield True, run[:end]
        run = run[end:]
    if run:
        yield False, run


@dataclass
class CreateTableFormatter:
    mode: Mode

    def __post_init__(self) -> None:
        self.node_manager = NodeManager(self.mode.dialect.case_sensitive_names)

    def format(self, lines: List[Line]) -> Optional[List[Line]]:
        """
        Returns new, formatted lines for the CREATE TABLE statement in lines,
        or None if the statement cannot be formatted by this formatter.
        """
        nodes = [node for line in lines for node in line.nodes if not node.is_newline]
        if not nodes or any(n.formatting_disabled for n in nodes):
            return None
        statement = _segment_statement(nodes)
        if statement is None or statement.header[0] is not nodes[0]:
            return None
        if statement.trailing:
            return None

        statement.header[-1].prefix = " "
        for clause in statement.clauses:
            if (
                len(clause) > 1
                and clause[0].is_unterm_keyword
                and clause[0].value == "options"
                and clause[1].is_opening_bracket
            ):
                clause[1].prefix = ""

        groups: List[Tuple[List[Node], bool]] = [(statement.header, False)]
        groups.extend((item, _is_table_constraint(item)) for item in statement.items)
        groups.append(([statement.body_close], False))
        groups.extend((clause, False) for clause in statement.clauses)
        if statement.terminator is not None:
            groups.append(([statement.terminator], False))

        comments = _assign_comments(lines, [g for g, _ in groups])

        new_lines: List[Line] = []
        previous_node = lines[0].previous_node
        for (group, is_table_constraint), group_comments in zip(
            groups, comments, strict=True
        ):
            if is_table_constraint:
                group_lines = self._format_table_constraint(
                    group, group_comments, previous_node
                )
            else:
                group_lines = [self._make_line(group, group_comments, previous_node)]
            new_lines.extend(group_lines)
            previous_node = new_lines[-1].nodes[-1]
        return new_lines

    def _make_line(
        self, nodes: List[Node], comments: List[Comment], previous_node: Optional[Node]
    ) -> Line:
        nodes[0].previous_node = previous_node
        line = Line.from_nodes(
            previous_node=previous_node, nodes=list(nodes), comments=comments
        )
        self.node_manager.append_newline(line)
        return line

    def _format_table_constraint(
        self, nodes: List[Node], comments: List[Comment], previous_node: Optional[Node]
    ) -> List[Line]:
        """
        Table constraints go on a single line if they fit. Otherwise, we try to
        split them before keywords like REFERENCES, with the continuation lines
        indented; if that isn't enough, we fall back to the standard splitting
        and merging behavior.
        """
        line = self._make_line(nodes, comments, previous_node)
        max_length = self.mode.line_length
        if not line.is_too_long(max_length):
            return [line]

        parts: List[List[Node]] = [[]]
        depth = 0
        for node in nodes:
            if (
                parts[-1]
                and depth == 0
                and _keyword(node) in CONTINUATION_KEYWORDS + TABLE_CONSTRAINT_KEYWORDS
            ):
                parts.append([])
            if node.is_opening_bracket:
                depth += 1
            elif node.is_closing_bracket:
                depth -= 1
            parts[-1].append(node)

        def fits(prefix: str, part: List[Node]) -> bool:
            return len(prefix + "".join(str(n) for n in part).lstrip()) <= max_length

        continuation_prefix = line.prefix + " " * 4
        packed = parts[:1]
        for part in parts[1:]:
            prefix = continuation_prefix if len(packed) > 1 else line.prefix
            if fits(prefix, packed[-1] + part):
                packed[-1] = packed[-1] + part
            else:
                packed.append(part)
        parts = packed

        if len(parts) > 1 and all(
            fits(prefix, part)
            for prefix, part in zip(
                [line.prefix] + [continuation_prefix] * (len(parts) - 1),
                parts,
                strict=True,
            )
        ):
            new_lines: List[Line] = []
            for part, part_comments in zip(
                parts, _assign_comments([line], parts), strict=True
            ):
                if new_lines:
                    part[0].open_brackets = [*part[0].open_brackets, nodes[0]]
                new_lines.append(self._make_line(part, part_comments, previous_node))
                previous_node = new_lines[-1].nodes[-1]
            return new_lines

        splitter = LineSplitter(self.node_manager)
        merger = LineMerger(mode=self.mode)
        return merger.maybe_merge_lines(splitter.maybe_split(line))


def _assign_comments(
    lines: List[Line], groups: List[List[Node]]
) -> List[List[Comment]]:
    """
    Returns a list of comments for each group of nodes. Standalone comments
    stay with the first node of their original line; inline comments stay
    with the node that precedes them.
    """
    group_index: Dict[int, int] = {
        id(node): i for i, group in enumerate(groups) for node in group
    }
    assigned: List[List[Comment]] = [[] for _ in groups]
    for line in lines:
        content = [node for node in line.nodes if not node.is_newline]
        default = group_index.get(id(content[0]), 0) if content else len(groups) - 1
        for comment in line.comments:
            if (
                comment.is_standalone
                or comment.is_multiline
                or comment.previous_node is None
            ):
                i = default
            else:
                i = group_index.get(id(comment.previous_node), default)
            assigned[i].append(comment)
    return assigned
