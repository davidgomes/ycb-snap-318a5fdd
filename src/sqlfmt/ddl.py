"""Structured CREATE TABLE model and layout.

Formatting rules for the statements this module owns:

* the opening parenthesis stays on the table-name line; the closing
  parenthesis is its own depth-0 line
* each column and each table-level constraint is its own indented line,
  separated by commas with no extra trailing comma introduced
* nested type arguments stay on the column line; a name immediately
  followed by ``(`` has no space before it, and commas inside those
  parentheses are followed by a single space
* inline constraints stay on the column line; ``check`` has a space
  before ``(``
* table-level constraints keep their argument list on one line when that
  line fits, with a space between the keyword and ``(``
* ``partition by``, ``cluster by``, and ``options (...)`` are depth-0
  clauses whose argument lists stay on one line
* DDL keywords and type names are lowercased; the terminating semicolon
  is its own depth-0 line
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import List, Optional, Sequence, Tuple

from sqlfmt.comment import Comment
from sqlfmt.line import Line
from sqlfmt.merger import LineMerger
from sqlfmt.mode import Mode
from sqlfmt.node import Node
from sqlfmt.node_manager import NodeManager
from sqlfmt.splitter import LineSplitter
from sqlfmt.tokens import TokenType

_TYPE_AND_KEYWORDS = frozenset(
    {
        "char",
        "character",
        "varchar",
        "nvarchar",
        "nchar",
        "text",
        "string",
        "clob",
        "bpchar",
        "varying",
        "national",
        "large",
        "object",
        "int",
        "integer",
        "int2",
        "int4",
        "int8",
        "int16",
        "int32",
        "int64",
        "int128",
        "bigint",
        "smallint",
        "tinyint",
        "byteint",
        "mediumint",
        "serial",
        "bigserial",
        "smallserial",
        "float",
        "float4",
        "float8",
        "float32",
        "float64",
        "double",
        "real",
        "decimal",
        "numeric",
        "number",
        "bignumeric",
        "dec",
        "precision",
        "money",
        "bool",
        "boolean",
        "date",
        "datetime",
        "timestamp",
        "timestamptz",
        "time",
        "timetz",
        "interval",
        "year",
        "month",
        "day",
        "hour",
        "minute",
        "second",
        "zone",
        "timezone",
        "with",
        "without",
        "local",
        "at",
        "array",
        "struct",
        "map",
        "record",
        "table",
        "row",
        "variant",
        "json",
        "jsonb",
        "bytes",
        "bytea",
        "binary",
        "varbinary",
        "blob",
        "uuid",
        "xml",
        "geography",
        "geometry",
        "void",
        "any",
        "unknown",
        "oid",
        "name",
        "citext",
        "inet",
        "cidr",
        "macaddr",
        "bit",
        "varbit",
        "enum",
        "hstore",
        "tsvector",
        "tsquery",
        "long",
        "short",
        "unsigned",
        "signed",
        "zerofill",
        "collate",
        "for",
        "data",
        "to",
        "not",
        "null",
        "default",
        "references",
        "constraint",
        "check",
        "unique",
        "primary",
        "foreign",
        "key",
        "options",
    }
)

_POST_KEYWORDS = ("partition by", "cluster by", "options")
_SINGLE_TERMINATORS = frozenset(
    {"default", "references", "constraint", "check", "null"}
)
_PREAMBLE = frozenset({"create table", "create table if not exists"})


@dataclass
class DdlColumn:
    """One column in a CREATE TABLE body."""

    name: str
    type_name: str
    has_inline_constraint: bool = False

    def __str__(self) -> str:
        marker = "<+constraint>" if self.has_inline_constraint else ""
        return f"{self.name} {self.type_name}{marker}"


@dataclass
class DdlTableConstraint:
    """A table-level constraint. ``keyword`` is normalized to lowercase."""

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


@dataclass
class _Parts:
    header: List[Node]
    items: List[List[Node]]
    closing: Optional[Node]
    post_clauses: List[List[Node]]
    semicolon: Optional[Node]


def parse_ddl_table(lines: List[Line]) -> Optional[DdlTable]:
    """Parse ``lines`` from a CREATE TABLE statement.

    Returns None when ``lines`` is not a CREATE TABLE this module formats
    (including CREATE TABLE AS / LIKE). Line breaks in ``lines`` do not
    matter; tokens are read in order.
    """
    all_nodes = _iter_nodes(lines)
    content = _content_nodes(all_nodes)
    parts = _split_parts(content)
    if parts is None:
        return None

    columns: List[DdlColumn] = []
    constraints: List[DdlTableConstraint] = []
    for item in parts.items:
        kind, keyword = _classify_item(item)
        if kind == "constraint" and keyword is not None:
            constraints.append(DdlTableConstraint(keyword))
        else:
            columns.append(_parse_column(item, all_nodes))

    return DdlTable(
        table_name=_table_name(parts.header),
        columns=columns,
        table_constraints=constraints,
    )


def reflow_create_table_lines(lines: List[Line], mode: Mode) -> List[Line]:
    """Re-layout CREATE TABLE statements in an already-lexed query."""
    result: List[Line] = []
    index = 0
    while index < len(lines):
        if _line_starts_formatted_create_table(lines[index]):
            end = _statement_end(lines, index)
            previous = (
                result[-1].nodes[-1]
                if result and result[-1].nodes
                else lines[index].previous_node
            )
            result.extend(
                _reflow_statement(lines[index:end], mode, previous_node=previous)
            )
            index = end
        else:
            result.append(lines[index])
            index += 1
    return result


def create_table_should_format(source: str, keyword_end: int) -> bool:
    """True when ``source`` after the CREATE TABLE keyword is in scope.

    CREATE TABLE AS, CREATE TABLE LIKE, and statements with other trailing
    clauses are left unchanged.
    """
    name_end = _read_qualified_name(source, keyword_end)
    if name_end is None:
        return False
    cursor = _skip_ws_comments(source, name_end)
    if cursor >= len(source) or source[cursor] == ";":
        return True
    if _match_kw(source, cursor, "as") or _match_kw(source, cursor, "like"):
        return False
    if source[cursor] == "(":
        inner = _skip_ws_comments(source, cursor + 1)
        if _match_kw(source, inner, "like"):
            return False
        end = _scan_balanced(source, cursor)
        if end is None:
            return False
        cursor = end
    while True:
        cursor = _skip_ws_comments(source, cursor)
        if cursor >= len(source) or source[cursor] == ";":
            return True
        matched = False
        for keyword in _POST_KEYWORDS:
            end = _match_kw(source, cursor, keyword)
            if end is not None:
                cursor = _skip_clause_body(source, end)
                matched = True
                break
        if not matched:
            return False


def _reflow_statement(
    block: List[Line], mode: Mode, previous_node: Optional[Node]
) -> List[Line]:
    if any(node.formatting_disabled for line in block for node in line.nodes):
        return block
    content = _content_nodes(_iter_nodes(block))
    parts = _split_parts(content)
    if parts is None:
        return block

    for node in parts.header:
        if _is_open_paren(node):
            node.prefix = " "
    for item in parts.items:
        if _classify_item(item)[0] == "column":
            _apply_type_case(_type_nodes(item))
    for clause in parts.post_clauses:
        _lowercase_known_names(clause)

    segments: List[Tuple[List[Node], bool]] = [(parts.header, True)]
    for item in parts.items:
        allow_overlong = _classify_item(item)[0] == "column"
        segments.append((item, allow_overlong))
    if parts.closing is not None:
        segments.append(([parts.closing], True))
    for clause in parts.post_clauses:
        segments.append((clause, True))
    if parts.semicolon is not None:
        segments.append(([parts.semicolon], True))

    comments = _comments_for_segments(block, [nodes for nodes, _ in segments])
    manager = NodeManager(mode.dialect.case_sensitive_names)
    lines: List[Line] = []
    previous = previous_node
    for (nodes, allow_overlong), segment_comments in zip(
        segments, comments, strict=True
    ):
        if not nodes:
            continue
        built = _layout_segment(
            nodes, segment_comments, previous, manager, mode, allow_overlong
        )
        lines.extend(built)
        if built and built[-1].nodes:
            previous = built[-1].nodes[-1]
    return lines or block


def _layout_segment(
    nodes: Sequence[Node],
    comments: List[Comment],
    previous: Optional[Node],
    manager: NodeManager,
    mode: Mode,
    allow_overlong: bool,
) -> List[Line]:
    line = Line.from_nodes(
        previous_node=previous,
        nodes=list(nodes),
        comments=list(comments),
    )
    manager.append_newline(line)
    if allow_overlong or not line.is_too_long(mode.line_length):
        return [line]
    split = LineSplitter(manager).maybe_split(line)
    return LineMerger(mode).maybe_merge_lines(split)


def _line_starts_formatted_create_table(line: Line) -> bool:
    for node in line.nodes:
        if node.token.type is TokenType.NEWLINE:
            continue
        if node.formatting_disabled or node.token.type is not TokenType.NAME:
            return False
        return _norm(node) in _PREAMBLE
    return False


def _statement_end(lines: Sequence[Line], start: int) -> int:
    cursor = start
    while cursor < len(lines):
        has_semicolon = any(
            node.token.type is TokenType.SEMICOLON for node in lines[cursor].nodes
        )
        cursor += 1
        if has_semicolon:
            break
    return cursor


def _iter_nodes(lines: Sequence[Line]) -> List[Node]:
    nodes: List[Node] = []
    for line in lines:
        nodes.extend(line.nodes)
    return nodes


def _content_nodes(nodes: Sequence[Node]) -> List[Node]:
    return [node for node in nodes if node.token.type is not TokenType.NEWLINE]


def _norm(node: Node) -> str:
    return " ".join(node.token.token.lower().split())


def _is_open_paren(node: Node) -> bool:
    return node.token.type is TokenType.BRACKET_OPEN and node.value == "("


def _is_close_paren(node: Node) -> bool:
    return node.token.type is TokenType.BRACKET_CLOSE and node.value == ")"


def _preamble_length(nodes: Sequence[Node]) -> Optional[int]:
    if not nodes:
        return None
    first = _norm(nodes[0])
    if first in _PREAMBLE:
        return 1
    if first != "create" or len(nodes) < 2 or _norm(nodes[1]) != "table":
        return None
    if (
        len(nodes) >= 5
        and _norm(nodes[2]) == "if"
        and _norm(nodes[3]) == "not"
        and _norm(nodes[4]) == "exists"
    ):
        return 5
    return 2


def _split_parts(nodes: Sequence[Node]) -> Optional[_Parts]:
    preamble = _preamble_length(nodes)
    if preamble is None:
        return None
    header = list(nodes[:preamble])
    index = preamble
    while index < len(nodes):
        node = nodes[index]
        if _is_open_paren(node) and len(node.open_brackets) == 0:
            header.append(node)
            index += 1
            break
        if _norm(node) in _POST_KEYWORDS or node.token.type is TokenType.SEMICOLON:
            break
        header.append(node)
        index += 1

    items: List[List[Node]] = []
    current: List[Node] = []
    closing: Optional[Node] = None
    if header and _is_open_paren(header[-1]):
        while index < len(nodes):
            node = nodes[index]
            if _is_close_paren(node) and len(node.open_brackets) == 0:
                if current:
                    items.append(current)
                    current = []
                closing = node
                index += 1
                break
            if node.token.type is TokenType.COMMA and len(node.open_brackets) == 1:
                if current:
                    current.append(node)
                    items.append(current)
                elif items:
                    items[-1].append(node)
                else:
                    items.append([node])
                current = []
                index += 1
                continue
            current.append(node)
            index += 1
        else:
            if current:
                items.append(current)

    post_clauses: List[List[Node]] = []
    current_post: List[Node] = []
    semicolon: Optional[Node] = None
    while index < len(nodes):
        node = nodes[index]
        if node.token.type is TokenType.SEMICOLON and len(node.open_brackets) == 0:
            semicolon = node
            index += 1
            break
        if _norm(node) in _POST_KEYWORDS and len(node.open_brackets) == 0:
            if current_post:
                post_clauses.append(current_post)
            current_post = [node]
        else:
            current_post.append(node)
        index += 1
    if current_post:
        post_clauses.append(current_post)
    return _Parts(header, items, closing, post_clauses, semicolon)


def _table_name(header: Sequence[Node]) -> str:
    preamble = _preamble_length(header) or 0
    parts: List[str] = []
    for node in header[preamble:]:
        if _is_open_paren(node) or node.token.type is TokenType.NEWLINE:
            break
        if node.token.type is TokenType.DOT:
            parts.append(".")
        else:
            parts.append(node.token.token)
    return "".join(parts)


def _strip_trailing_separator(item: Sequence[Node]) -> List[Node]:
    if (
        item
        and item[-1].token.type is TokenType.COMMA
        and len(item[-1].open_brackets) <= 1
    ):
        return list(item[:-1])
    return list(item)


def _classify_item(item: Sequence[Node]) -> Tuple[str, Optional[str]]:
    core = _strip_trailing_separator(item)
    keyword = _constraint_keyword(core)
    if keyword is None:
        return "column", None
    return "constraint", keyword


def _constraint_keyword(core: Sequence[Node]) -> Optional[str]:
    if not core:
        return None
    first = _norm(core[0])
    if first == "primary key":
        return "primary key"
    if first == "foreign key":
        return "foreign key"
    if first == "primary" and len(core) > 1 and _norm(core[1]) == "key":
        return "primary key"
    if first == "foreign" and len(core) > 1 and _norm(core[1]) == "key":
        return "foreign key"
    if first == "constraint":
        return "constraint"
    if first in {"unique", "check"}:
        if len(core) == 1 or _is_open_paren(core[1]):
            return first
    return None


def _terminator_length(nodes: Sequence[Node], index: int) -> int:
    current = _norm(nodes[index])
    if current == "not null":
        return 1
    if (
        current == "not"
        and index + 1 < len(nodes)
        and _norm(nodes[index + 1]) == "null"
    ):
        return 2
    if current in _SINGLE_TERMINATORS:
        return 1
    return 0


def _type_nodes(item: Sequence[Node]) -> List[Node]:
    core = _strip_trailing_separator(item)
    if len(core) < 2:
        return []
    rest = core[1:]
    for index in range(len(rest)):
        if _terminator_length(rest, index):
            return rest[:index]
    return rest


def _parse_column(item: Sequence[Node], all_nodes: Sequence[Node]) -> DdlColumn:
    core = _strip_trailing_separator(item)
    name = core[0].token.token if core else ""
    type_nodes = _type_nodes(item)
    has_constraint = len(core) > 1 + len(type_nodes) and bool(
        _terminator_length(core[1:], len(type_nodes)) if core[1:] else 0
    )
    # ``_type_nodes`` stops at the first terminator, so any token after the
    # type means an inline constraint was present.
    if not has_constraint:
        has_constraint = len(core) > len(type_nodes) + 1
    return DdlColumn(
        name=name,
        type_name=_reconstruct_type(type_nodes, all_nodes),
        has_inline_constraint=has_constraint,
    )


def _reconstruct_type(type_nodes: Sequence[Node], all_nodes: Sequence[Node]) -> str:
    if not type_nodes:
        return ""
    positions = {id(node): index for index, node in enumerate(all_nodes)}
    start = positions[id(type_nodes[0])]
    end = positions[id(type_nodes[-1])]
    return _render_type_span(all_nodes[start : end + 1]).strip()


def _render_type_span(span: Sequence[Node]) -> str:
    content = _content_nodes(span)
    following = _next_content(content)
    parts: List[str] = []
    seen_content = False
    content_index = 0
    for node in span:
        if node.token.type is TokenType.NEWLINE:
            parts.append(node.token.token)
            continue
        nxt = following[content_index]
        text = node.token.token
        if _should_lower_type_text(node, nxt, is_first=not seen_content):
            text = text.lower()
        parts.append(f"{node.token.prefix}{text}")
        seen_content = True
        content_index += 1
    return "".join(parts)


def _next_content(content: Sequence[Node]) -> List[Optional[Node]]:
    return [
        content[index + 1] if index + 1 < len(content) else None
        for index in range(len(content))
    ]


def _should_lower_type_text(node: Node, nxt: Optional[Node], is_first: bool) -> bool:
    kind = node.token.type
    if kind is TokenType.QUOTED_NAME:
        return False
    if kind in (
        TokenType.WORD_OPERATOR,
        TokenType.BOOLEAN_OPERATOR,
        TokenType.UNTERM_KEYWORD,
        TokenType.BRACKET_OPEN,
    ):
        return True
    if kind is not TokenType.NAME:
        return False
    if is_first or _norm(node) in _TYPE_AND_KEYWORDS:
        return True
    return nxt is not None and nxt.token.type is TokenType.BRACKET_OPEN


def _apply_type_case(type_nodes: Sequence[Node]) -> None:
    following = _next_content(type_nodes)
    for index, node in enumerate(type_nodes):
        if node.token.type is not TokenType.NAME:
            continue
        if _should_lower_type_text(node, following[index], is_first=index == 0):
            node.value = node.token.token.lower()


def _lowercase_known_names(nodes: Sequence[Node]) -> None:
    for node in nodes:
        if node.token.type is TokenType.NAME and _norm(node) in _TYPE_AND_KEYWORDS:
            node.value = node.token.token.lower()


def _comments_for_segments(
    block: Sequence[Line], segments: Sequence[Sequence[Node]]
) -> List[List[Comment]]:
    owners = {
        id(node): index for index, segment in enumerate(segments) for node in segment
    }
    grouped: List[List[Comment]] = [[] for _ in segments]
    leading: List[Comment] = []
    for line in block:
        for comment in line.comments:
            previous = comment.previous_node
            while previous is not None and previous.token.type is TokenType.NEWLINE:
                previous = previous.previous_node
            if previous is None or id(previous) not in owners:
                leading.append(comment)
                continue
            segment_index = owners[id(previous)]
            if comment.is_standalone or comment.is_multiline:
                if segment_index + 1 < len(grouped):
                    segment_index += 1
            grouped[segment_index].append(comment)
    if grouped:
        grouped[0] = leading + grouped[0]
    elif leading:
        return [leading]
    return grouped


def _skip_ws_comments(source: str, index: int) -> int:
    length = len(source)
    while index < length:
        if source[index].isspace():
            index += 1
            continue
        if (
            source.startswith("--", index)
            or source.startswith("#", index)
            or source.startswith("//", index)
        ):
            newline = source.find("\n", index)
            index = length if newline < 0 else newline + 1
            continue
        if source.startswith("/*", index):
            end = source.find("*/", index + 2)
            index = length if end < 0 else end + 2
            continue
        break
    return index


def _match_kw(source: str, index: int, words: str) -> Optional[int]:
    cursor = index
    for part_index, part in enumerate(words.split()):
        if part_index:
            cursor = _skip_ws_comments(source, cursor)
        match = re.match(r"[A-Za-z_][A-Za-z0-9_]*", source[cursor:])
        if match is None or match.group(0).lower() != part:
            return None
        cursor += match.end()
    if cursor < len(source) and (source[cursor].isalnum() or source[cursor] == "_"):
        return None
    return cursor


def _skip_quoted(source: str, index: int) -> Optional[int]:
    quote = source[index]
    index += 1
    length = len(source)
    while index < length:
        if source[index] == "\\":
            index += 2
            continue
        if source[index] == quote:
            if index + 1 < length and source[index + 1] == quote:
                index += 2
                continue
            return index + 1
        index += 1
    return None


def _try_skip_string(source: str, index: int) -> Optional[int]:
    dollar = re.match(r"\$(\w*)\$", source[index:])
    if dollar is not None:
        tag = dollar.group(0)
        end = source.find(tag, index + len(tag))
        if end < 0:
            return None
        return end + len(tag)
    match = re.match(
        r"(?:[uU]&|[bB][rR]|[rR][bB]|[nNxXbBrRuU])?(['\"`])",
        source[index:],
    )
    if match is None:
        return None
    return _skip_quoted(source, index + match.start(1))


def _scan_balanced(source: str, index: int) -> Optional[int]:
    """Return the index just after the bracket group that starts at ``index``."""
    length = len(source)
    if index >= length or source[index] not in "([{":
        return None
    stack = [source[index]]
    closes = {"(": ")", "[": "]", "{": "}"}
    opens = {")": "(", "]": "[", "}": "{"}
    index += 1
    while index < length and stack:
        if source[index].isspace():
            index += 1
            continue
        if (
            source.startswith("--", index)
            or source.startswith("#", index)
            or source.startswith("//", index)
        ):
            newline = source.find("\n", index)
            index = length if newline < 0 else newline + 1
            continue
        if source.startswith("/*", index):
            end = source.find("*/", index + 2)
            if end < 0:
                return None
            index = end + 2
            continue
        skipped = _try_skip_string(source, index)
        if skipped is not None and skipped > index:
            index = skipped
            continue
        character = source[index]
        if character in closes:
            stack.append(character)
        elif character in opens:
            if stack[-1] != opens[character]:
                return None
            stack.pop()
            if not stack:
                return index + 1
        index += 1
    return None


def _read_ident(source: str, index: int) -> Optional[int]:
    index = _skip_ws_comments(source, index)
    if index >= len(source):
        return None
    if source.startswith("{{", index):
        end = source.find("}}", index + 2)
        return None if end < 0 else end + 2
    if source.startswith("{%", index):
        end = source.find("%}", index + 2)
        return None if end < 0 else end + 2
    if source[index] in '"`':
        return _skip_quoted(source, index)
    if source[index] == "[":
        return _scan_balanced(source, index)
    match = re.match(r"[^\W\d]\w*", source[index:], re.UNICODE)
    if match is None:
        return None
    return index + match.end()


def _read_qualified_name(source: str, index: int) -> Optional[int]:
    cursor = _read_ident(source, index)
    if cursor is None:
        return None
    while True:
        after = _skip_ws_comments(source, cursor)
        if after < len(source) and source[after] == ".":
            nxt = _read_ident(source, after + 1)
            if nxt is None:
                return cursor
            cursor = nxt
            continue
        return cursor


def _skip_clause_body(source: str, index: int) -> int:
    length = len(source)
    depth = 0
    while index < length:
        if source[index].isspace():
            index += 1
            continue
        if (
            source.startswith("--", index)
            or source.startswith("#", index)
            or source.startswith("//", index)
        ):
            newline = source.find("\n", index)
            index = length if newline < 0 else newline + 1
            continue
        if source.startswith("/*", index):
            end = source.find("*/", index + 2)
            index = length if end < 0 else end + 2
            continue
        skipped = _try_skip_string(source, index)
        if skipped is not None and skipped > index:
            index = skipped
            continue
        if depth == 0 and source[index] == ";":
            return index
        if depth == 0 and any(
            _match_kw(source, index, keyword) is not None for keyword in _POST_KEYWORDS
        ):
            return index
        character = source[index]
        if character in "([{":
            depth += 1
        elif character in ")]}" and depth:
            depth -= 1
        index += 1
    return index
