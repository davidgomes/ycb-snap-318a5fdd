"""Structured CREATE TABLE parsing and statement reflow."""

import re
from dataclasses import dataclass, field
from typing import List, Optional, Sequence, Tuple

from sqlfmt.comment import Comment
from sqlfmt.line import Line
from sqlfmt.node import Node
from sqlfmt.node_manager import NodeManager
from sqlfmt.tokens import TokenType

_CREATE_TABLE_KEYWORDS = {"create table", "create table if not exists"}
_TABLE_CONSTRAINT_KEYWORDS = {
    "primary key",
    "foreign key",
    "unique",
    "check",
    "constraint",
}
_INLINE_CONSTRAINT_KEYWORDS = {
    "not null",
    "default",
    "references",
    "constraint",
    "check",
    "null",
}
_CLAUSE_KEYWORDS = {"partition by", "cluster by"}
_NAME_PART_TYPES = {
    TokenType.NAME,
    TokenType.QUOTED_NAME,
    TokenType.DOT,
    TokenType.JINJA_EXPRESSION,
    TokenType.JINJA_STATEMENT,
    TokenType.JINJA_BLOCK_START,
    TokenType.JINJA_BLOCK_KEYWORD,
    TokenType.JINJA_BLOCK_END,
}
_LOWERCASED_TYPE_TOKENS = {
    TokenType.NAME,
    TokenType.WORD_OPERATOR,
    TokenType.UNTERM_KEYWORD,
    TokenType.BOOLEAN_OPERATOR,
    TokenType.ON,
    TokenType.SET_OPERATOR,
    TokenType.STATEMENT_START,
    TokenType.STATEMENT_END,
    TokenType.BRACKET_OPEN,
}


def _normalized_text(node: Node) -> str:
    return " ".join(node.token.token.lower().split())


def _is_create_table_node(node: Node) -> bool:
    return (
        node.token.type is TokenType.WORD_OPERATOR
        and _normalized_text(node) in _CREATE_TABLE_KEYWORDS
    )


def _bracket_delta(node: Node) -> int:
    if node.token.type in (TokenType.BRACKET_OPEN, TokenType.STATEMENT_START):
        return 1
    if node.token.type in (TokenType.BRACKET_CLOSE, TokenType.STATEMENT_END):
        return -1
    return 0


def _is_table_paren(node: Node) -> bool:
    return node.token.type is TokenType.BRACKET_OPEN and node.value.startswith("(")


def _matches_inline_constraint(nodes: Sequence[Node], index: int) -> bool:
    word = _normalized_text(nodes[index])
    if word in _INLINE_CONSTRAINT_KEYWORDS:
        return True
    if (
        word == "not"
        and index + 1 < len(nodes)
        and _normalized_text(nodes[index + 1]) == "null"
    ):
        return True
    return False


def _find_inline_constraint(nodes: Sequence[Node]) -> Optional[int]:
    """Index of the first inline constraint keyword at the column's top level."""
    depth = 0
    for index, node in enumerate(nodes):
        if depth == 0 and _matches_inline_constraint(nodes, index):
            return index
        depth += _bracket_delta(node)
    return None


def _is_table_constraint(nodes: Sequence[Node]) -> bool:
    if not nodes:
        return False
    word = _normalized_text(nodes[0])
    if word in _TABLE_CONSTRAINT_KEYWORDS:
        return True
    if word == "primary" and len(nodes) > 1 and _normalized_text(nodes[1]) == "key":
        return True
    if word == "foreign" and len(nodes) > 1 and _normalized_text(nodes[1]) == "key":
        return True
    return False


def _constraint_keyword(nodes: Sequence[Node]) -> str:
    word = _normalized_text(nodes[0])
    if word == "primary" and len(nodes) > 1 and _normalized_text(nodes[1]) == "key":
        return "primary key"
    if word == "foreign" and len(nodes) > 1 and _normalized_text(nodes[1]) == "key":
        return "foreign key"
    return word


def _content_nodes(lines: Sequence[Line]) -> List[Node]:
    nodes: List[Node] = []
    for line in lines:
        for node in line.nodes:
            if not node.is_newline:
                nodes.append(node)
    return nodes


def _find_create_table(nodes: Sequence[Node]) -> Optional[int]:
    depth = 0
    for index, node in enumerate(nodes):
        if depth == 0 and _is_create_table_node(node):
            return index
        depth += _bracket_delta(node)
    return None


def _split_header(nodes: Sequence[Node]) -> Tuple[List[Node], List[Node]]:
    """Split the create-table keyword and table name from the body."""
    index = 1
    while index < len(nodes) and nodes[index].token.type in _NAME_PART_TYPES:
        index += 1
    if index < len(nodes) and _is_table_paren(nodes[index]):
        return list(nodes[: index + 1]), list(nodes[index + 1 :])
    return list(nodes[:index]), list(nodes[index:])


def _take_body(nodes: Sequence[Node]) -> Tuple[List[Node], Optional[Node], List[Node]]:
    """Split nodes inside the table parentheses from the closing paren and tail.

    ``nodes`` begins just after the opening parenthesis. Depth starts at 1.
    """
    depth = 1
    body: List[Node] = []
    for index, node in enumerate(nodes):
        if node.token.type is TokenType.SEMICOLON and depth == 1:
            return body, None, list(nodes[index:])
        if node.token.type in (TokenType.BRACKET_CLOSE, TokenType.STATEMENT_END):
            depth -= 1
            if depth == 0 and node.value == ")":
                return body, node, list(nodes[index + 1 :])
            body.append(node)
            continue
        body.append(node)
        if node.token.type in (TokenType.BRACKET_OPEN, TokenType.STATEMENT_START):
            depth += 1
    return body, None, []


def _split_top_level_items(body: Sequence[Node]) -> List[List[Node]]:
    items: List[List[Node]] = []
    current: List[Node] = []
    depth = 0
    for node in body:
        if node.is_comma and depth == 0:
            if current and not all(seen.is_comma for seen in current):
                current.append(node)
                items.append(current)
                current = []
            else:
                # A comma before the first item of the statement, or a
                # repeated comma. Keep the token on the following item.
                current.append(node)
            continue
        current.append(node)
        depth += _bracket_delta(node)
    if current:
        items.append(current)
    return items


def _split_semicolon(
    nodes: Sequence[Node],
) -> Tuple[List[Node], Optional[Node], List[Node]]:
    for index, node in enumerate(nodes):
        if node.token.type is TokenType.SEMICOLON:
            return list(nodes[:index]), node, list(nodes[index + 1 :])
    return list(nodes), None, []


def _is_clause_start(nodes: Sequence[Node], index: int) -> bool:
    word = _normalized_text(nodes[index])
    if word in _CLAUSE_KEYWORDS:
        return True
    if word != "options":
        return False
    for follower in nodes[index + 1 :]:
        if follower.is_newline:
            continue
        return _is_table_paren(follower)
    return False


def _split_clauses(nodes: Sequence[Node]) -> List[List[Node]]:
    clauses: List[List[Node]] = []
    current: List[Node] = []
    depth = 0
    for index, node in enumerate(nodes):
        if depth == 0 and current and _is_clause_start(nodes, index):
            clauses.append(current)
            current = []
        current.append(node)
        depth += _bracket_delta(node)
    if current:
        clauses.append(current)
    return clauses


def _join_original(nodes: Sequence[Node]) -> str:
    return "".join(node.token.prefix + node.token.token for node in nodes).strip()


def _type_token_text(node: Node) -> str:
    text = node.token.token
    if node.token.type in _LOWERCASED_TYPE_TOKENS or (
        node.token.type is TokenType.BRACKET_OPEN
        and any(char.isalpha() for char in text)
    ):
        return text.lower()
    return text


def _join_type(nodes: Sequence[Node]) -> str:
    return "".join(node.token.prefix + _type_token_text(node) for node in nodes).strip()


def _column_body(item: Sequence[Node]) -> List[Node]:
    """Drop the commas that separate this item from its siblings."""
    nodes = list(item)
    while nodes and nodes[0].is_comma:
        nodes.pop(0)
    if nodes and nodes[-1].is_comma:
        nodes.pop()
    return nodes


@dataclass
class DdlColumn:
    """One column in a CREATE TABLE statement.

    ``type_name`` is the type expression with original inter-token spacing and
    with DDL keywords and type names lowercased. ``has_inline_constraint`` is
    true when the column has an inline NOT NULL, DEFAULT, REFERENCES,
    CONSTRAINT, CHECK, or NULL constraint.
    """

    name: str
    type_name: str
    has_inline_constraint: bool = False

    def __str__(self) -> str:
        text = f"{self.name} {self.type_name}".rstrip()
        if self.has_inline_constraint:
            return f"{text} <+constraint>"
        return text


@dataclass
class DdlTableConstraint:
    """A table-level constraint. ``keyword`` is stored in lowercase."""

    keyword: str

    def __post_init__(self) -> None:
        self.keyword = self.keyword.lower()


@dataclass
class DdlTable:
    """A parsed CREATE TABLE statement."""

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


def _parse_column(item: Sequence[Node]) -> DdlColumn:
    body = _column_body(item)
    name = body[0].token.token
    remainder = body[1:]
    split_at = _find_inline_constraint(remainder)
    if split_at is None:
        type_nodes = remainder
        has_constraint = False
    else:
        type_nodes = list(remainder[:split_at])
        has_constraint = True
    return DdlColumn(
        name=name,
        type_name=_join_type(type_nodes),
        has_inline_constraint=has_constraint,
    )


def parse_ddl_table(lines: List[Line]) -> Optional[DdlTable]:
    """Parse a CREATE TABLE statement from analyzer lines.

    ``lines`` may be the raw lex of any valid CREATE TABLE layout, or the
    lines produced by formatting. Returns None when ``lines`` do not contain
    a CREATE TABLE statement.
    """
    nodes = _content_nodes(lines)
    start = _find_create_table(nodes)
    if start is None:
        return None
    nodes = nodes[start:]
    for index, node in enumerate(nodes):
        if node.token.type is TokenType.SEMICOLON:
            nodes = nodes[:index]
            break

    header, remainder = _split_header(nodes)
    table_name = _join_original(header[1:])
    if header and _is_table_paren(header[-1]):
        table_name = _join_original(header[1:-1])
        body, _close, _after = _take_body(remainder)
    else:
        body = []

    columns: List[DdlColumn] = []
    constraints: List[DdlTableConstraint] = []
    for item in _split_top_level_items(body):
        body_nodes = _column_body(item)
        if not body_nodes:
            continue
        if _is_table_constraint(body_nodes):
            constraints.append(DdlTableConstraint(_constraint_keyword(body_nodes)))
        else:
            columns.append(_parse_column(item))
    return DdlTable(
        table_name=table_name,
        columns=columns,
        table_constraints=constraints,
    )


def _skip_gap(source: str, index: int) -> int:
    length = len(source)
    while index < length:
        if source[index].isspace():
            index += 1
            continue
        if source.startswith(("--", "//", "#"), index):
            newline = source.find("\n", index)
            index = length if newline < 0 else newline + 1
            continue
        if source.startswith("/*", index):
            end = source.find("*/", index + 2)
            index = length if end < 0 else end + 2
            continue
        if source.startswith("{#", index):
            end = source.find("#}", index + 2)
            index = length if end < 0 else end + 2
            continue
        break
    return index


def _scan_quoted(source: str, index: int) -> Optional[int]:
    quote = source[index]
    index += 1
    length = len(source)
    while index < length:
        if source[index] == "\\" and quote != "`":
            index += 2
            continue
        if quote in {"'", '"'} and source.startswith(quote * 2, index):
            index += 2
            continue
        if source[index] == quote:
            return index + 1
        index += 1
    return None


def _scan_name_part(source: str, index: int) -> Optional[int]:
    if source.startswith("{{", index):
        end = source.find("}}", index + 2)
        return None if end < 0 else end + 2
    if index < len(source) and source[index] in {"'", '"', "`"}:
        return _scan_quoted(source, index)
    match = re.match(r"\w+", source[index:])
    if match:
        return index + match.end()
    return None


def _after_table_name(source: str, start: int) -> int:
    index = _skip_gap(source, start)
    while index < len(source):
        nxt = _scan_name_part(source, index)
        if nxt is None:
            return index
        index = nxt
        after_gap = _skip_gap(source, index)
        if after_gap < len(source) and source[after_gap] == ".":
            index = _skip_gap(source, after_gap + 1)
            continue
        return index
    return index


def _starts_keyword(source: str, index: int, keyword: str) -> bool:
    end = index + len(keyword)
    if source[index:end].lower() != keyword:
        return False
    if end < len(source) and (source[end].isalnum() or source[end] == "_"):
        return False
    return True


def _match_paren(source: str, open_index: int) -> Optional[int]:
    """Return the index just after the parenthesis that matches ``open_index``."""
    depth = 0
    index = open_index
    length = len(source)
    while index < length:
        if source[index] in {"'", '"', "`"}:
            end = _scan_quoted(source, index)
            index = length if end is None else end
            continue
        if source.startswith(("$",), index):
            tag = re.match(r"\$\w*\$", source[index:])
            if tag:
                marker = tag.group(0)
                end = source.find(marker, index + len(marker))
                index = length if end < 0 else end + len(marker)
                continue
        if source.startswith(("--", "//", "#"), index):
            newline = source.find("\n", index)
            index = length if newline < 0 else newline + 1
            continue
        if source.startswith("/*", index):
            end = source.find("*/", index + 2)
            index = length if end < 0 else end + 2
            continue
        if source.startswith(("{{", "{%", "{#"), index):
            closers = {"{{": "}}", "{%": "%}", "{#": "#}"}
            closer = closers[source[index : index + 2]]
            end = source.find(closer, index + 2)
            index = length if end < 0 else end + len(closer)
            continue
        if source[index] == "(":
            depth += 1
        elif source[index] == ")":
            depth -= 1
            if depth == 0:
                return index + 1
        index += 1
    return None


def create_table_passes_through(source: str, start: int) -> bool:
    """True when a CREATE TABLE statement is CTAS or LIKE and must be unchanged."""
    index = _skip_gap(source, start)
    if index < len(source) and (
        _starts_keyword(source, index, "as") or _starts_keyword(source, index, "like")
    ):
        return True
    index = _after_table_name(source, start)
    index = _skip_gap(source, index)
    if index >= len(source):
        return False
    if _starts_keyword(source, index, "as") or _starts_keyword(source, index, "like"):
        return True
    if source[index] != "(":
        return False
    inner = _skip_gap(source, index + 1)
    if _starts_keyword(source, inner, "like"):
        return True
    end = _match_paren(source, index)
    if end is None:
        return False
    after = _skip_gap(source, end)
    return _starts_keyword(source, after, "as") or _starts_keyword(
        source, after, "like"
    )


def _lowercase_type_names(item: Sequence[Node]) -> None:
    body = _column_body(item)
    if len(body) < 2 or _is_table_constraint(body):
        return
    split_at = _find_inline_constraint(body[1:])
    type_nodes = body[1:] if split_at is None else body[1 : 1 + split_at]
    for node in type_nodes:
        if node.token.type is TokenType.NAME:
            node.value = node.token.token.lower()


def _lowercase_options(nodes: Sequence[Node]) -> None:
    for node in nodes:
        if node.token.type is TokenType.NAME and _normalized_text(node) == "options":
            node.value = "options"


def _starts_statement(line: Line) -> bool:
    for node in line.nodes:
        if node.is_newline:
            continue
        return _is_create_table_node(node)
    return False


def _statement_follows(line: Line) -> bool:
    for node in line.nodes:
        if node.is_newline:
            continue
        if node.is_unterm_keyword and node.depth[0] == 0:
            return True
        return _is_create_table_node(node)
    return False


def _can_reflow(lines: Sequence[Line]) -> bool:
    for line in lines:
        for node in line.nodes:
            if node.formatting_disabled:
                return False
    return True


def _attach_comments(
    comments: Sequence[Comment], groups: Sequence[Sequence[Node]]
) -> List[List[Comment]]:
    buckets: List[List[Comment]] = [[] for _ in groups]
    if not groups:
        return buckets
    ordered = sorted(comments, key=lambda comment: comment.token.spos)
    for comment in ordered:
        position = comment.token.spos
        target = 0
        for index, group in enumerate(groups):
            if group and group[0].token.spos <= position:
                target = index
            else:
                break
        standalone = comment.is_standalone or comment.is_multiline
        if (
            standalone
            and target + 1 < len(groups)
            and groups[target]
            and position >= groups[target][-1].token.epos
            and groups[target + 1]
            and position < groups[target + 1][0].token.spos
        ):
            target += 1
        buckets[target].append(comment)
    return buckets


def _reflow_chunk(lines: Sequence[Line], node_manager: NodeManager) -> List[Line]:
    content = _content_nodes(lines)
    if not content or not _is_create_table_node(content[0]):
        return list(lines)

    header, remainder = _split_header(content)
    if header and _is_table_paren(header[-1]):
        header[-1].prefix = " "
        body, close, after = _take_body(remainder)
    else:
        body, close, after = [], None, list(remainder)

    items = _split_top_level_items(body)
    for item in items:
        _lowercase_type_names(item)

    after, semicolon, leftover = _split_semicolon(after)
    clauses = _split_clauses(after)
    for clause in clauses:
        _lowercase_options(clause)

    groups: List[List[Node]] = []
    if header:
        groups.append(header)
    groups.extend(items)
    if close is not None:
        groups.append([close])
    groups.extend(clauses)
    if semicolon is not None:
        groups.append([semicolon])
    if leftover:
        groups.append(leftover)

    flat = [node for group in groups for node in group]
    if len(flat) != len(content) or any(
        left is not right for left, right in zip(flat, content, strict=True)
    ):
        return list(lines)

    comments = [comment for line in lines for comment in line.comments]
    buckets = _attach_comments(comments, groups)
    if sum(len(bucket) for bucket in buckets) != len(comments):
        return list(lines)

    new_lines: List[Line] = []
    previous = lines[0].previous_node
    for group, group_comments in zip(groups, buckets, strict=True):
        line = Line.from_nodes(
            previous_node=previous,
            nodes=list(group),
            comments=list(group_comments),
        )
        node_manager.append_newline(line)
        new_lines.append(line)
        previous = line.nodes[-1]
    return new_lines


def reflow_create_table_statements(
    lines: List[Line], node_manager: NodeManager
) -> List[Line]:
    """Rewrite CREATE TABLE statements into the ddl layout.

    CREATE TABLE AS SELECT and CREATE TABLE ... LIKE are lexed as unsupported
    statements and are left untouched.
    """
    result: List[Line] = []
    index = 0
    while index < len(lines):
        if not _starts_statement(lines[index]):
            result.append(lines[index])
            index += 1
            continue
        end = index + 1
        while end < len(lines):
            if _statement_follows(lines[end]):
                break
            end += 1
            if any(
                node.token.type is TokenType.SEMICOLON for node in lines[end - 1].nodes
            ):
                break
        chunk = lines[index:end]
        if _can_reflow(chunk):
            result.extend(_reflow_chunk(chunk, node_manager))
        else:
            result.extend(chunk)
        index = end
    return result
