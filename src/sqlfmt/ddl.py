"""Structured CREATE TABLE model and statement layout."""

from dataclasses import dataclass, field
from typing import Dict, Iterable, List, Optional, Sequence, Tuple

from sqlfmt.comment import Comment
from sqlfmt.line import Line
from sqlfmt.merger import LineMerger
from sqlfmt.mode import Mode
from sqlfmt.node import Node
from sqlfmt.node_manager import NodeManager
from sqlfmt.splitter import LineSplitter
from sqlfmt.tokens import Token, TokenType

_HEADER_MODIFIERS = {
    "global",
    "local",
    "temp",
    "temporary",
    "transient",
    "volatile",
    "unlogged",
    "external",
    "iceberg",
    "dynamic",
}

_TABLE_CONSTRAINT_PHRASES: Tuple[Tuple[str, ...], ...] = (
    ("primary", "key"),
    ("foreign", "key"),
    ("unique",),
    ("check",),
    ("constraint",),
)

# Keywords that end the type expression and mark an inline column constraint.
_INLINE_PHRASES: Tuple[Tuple[str, ...], ...] = (
    ("not", "null"),
    ("default",),
    ("references",),
    ("constraint",),
    ("check",),
    ("null",),
)

_POST_BODY = {"partition by", "cluster by", "options"}

# Builtin type names and type-expression keywords. Identifiers outside this set
# keep their source case (for example struct field names).
_TYPE_WORDS = {
    "array",
    "bigint",
    "bigserial",
    "binary",
    "bit",
    "blob",
    "bool",
    "boolean",
    "bpchar",
    "bytea",
    "byteint",
    "bytes",
    "char",
    "character",
    "charset",
    "clob",
    "collate",
    "date",
    "datetime",
    "datetime64",
    "day",
    "dec",
    "decimal",
    "double",
    "enum",
    "fixedstring",
    "float",
    "float32",
    "float4",
    "float64",
    "float8",
    "geography",
    "geometry",
    "hour",
    "hllsketch",
    "inet",
    "int",
    "int2",
    "int4",
    "int8",
    "int16",
    "int32",
    "int64",
    "integer",
    "interval",
    "json",
    "jsonb",
    "large",
    "local",
    "longblob",
    "longtext",
    "lowcardinality",
    "map",
    "mediumblob",
    "mediumint",
    "mediumtext",
    "minute",
    "money",
    "month",
    "national",
    "nchar",
    "nclob",
    "nullable",
    "number",
    "numeric",
    "nvarchar",
    "object",
    "period",
    "precision",
    "real",
    "record",
    "row",
    "second",
    "serial",
    "set",
    "signed",
    "smallint",
    "smallserial",
    "string",
    "struct",
    "super",
    "table",
    "text",
    "time",
    "timestamp",
    "timestamp_ltz",
    "timestamp_ntz",
    "timestamp_tz",
    "timestamptz",
    "timezone",
    "tinyblob",
    "tinyint",
    "tinytext",
    "to",
    "uuid",
    "varbinary",
    "varbit",
    "varchar",
    "variant",
    "varying",
    "void",
    "week",
    "with",
    "without",
    "xml",
    "year",
    "zone",
    "unsigned",
    "zerofill",
}


@dataclass
class DdlColumn:
    """One column definition inside a CREATE TABLE body."""

    name: str
    type_name: str
    has_inline_constraint: bool = False

    def __str__(self) -> str:
        text = self.name if not self.type_name else f"{self.name} {self.type_name}"
        if self.has_inline_constraint:
            return f"{text} <+constraint>"
        return text


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


def parse_ddl_table(lines: Sequence[Line]) -> Optional[DdlTable]:
    """
    Parse ``lines`` produced by the sqlfmt analyzer for a CREATE TABLE query.

    Line breaks are ignored, so this works for any valid tokenization of the
    statement, formatted or not. Returns None when ``lines`` is not a
    CREATE TABLE statement.
    """
    for statement in _statement_nodes(lines):
        parsed = _parse_statement(statement)
        if parsed is not None:
            return parsed
    return None


def relayout_ddl_lines(lines: List[Line], max_length: int) -> List[Line]:
    """Apply CREATE TABLE layout on top of the ordinary split/merge result."""
    if not lines:
        return lines
    node_manager = NodeManager(case_sensitive_names=False)
    mode = Mode(line_length=max_length)
    merger = LineMerger(mode)
    splitter = LineSplitter(node_manager)
    output: List[Line] = []
    for statement in _statement_line_groups(lines):
        output.extend(
            _relayout_statement(statement, max_length, node_manager, splitter, merger)
        )
    return output


_IGNORED_TOKENS = {TokenType.FMT_OFF, TokenType.FMT_ON}


def _parse_statement(nodes: Sequence[Node]) -> Optional[DdlTable]:
    content = [
        node
        for node in nodes
        if not node.is_newline and node.token.type not in _IGNORED_TOKENS
    ]
    if not content:
        return None
    if all(
        node.token.type in (TokenType.DATA, TokenType.SEMICOLON) for node in content
    ):
        return _parse_data_statement(content)

    index = _consume_header(content, 0)
    if index is None:
        return None

    name_nodes: List[Node] = []
    while index < len(content) and not _ends_table_name(content[index]):
        name_nodes.append(content[index])
        index += 1
    table_name = _render_identifier(name_nodes)

    columns: List[DdlColumn] = []
    constraints: List[DdlTableConstraint] = []
    if index < len(content) and _is_open_paren(content[index]):
        open_paren = content[index]
        body, _after = _column_body(content, index + 1)
        for item in _split_top_level_items(body, open_paren):
            if _leading_phrase(item, _TABLE_CONSTRAINT_PHRASES) is not None:
                keyword = _leading_phrase(item, _TABLE_CONSTRAINT_PHRASES)
                assert keyword is not None
                constraints.append(DdlTableConstraint(" ".join(keyword)))
            elif item:
                columns.append(_parse_column(item))
    return DdlTable(table_name, columns, constraints)


def _parse_data_statement(nodes: Sequence[Node]) -> Optional[DdlTable]:
    """
    Parse a CREATE TABLE that the main ruleset left as raw DATA.

    AS and LIKE forms have no column list to recover. Other statements are
    re-lexed with the DDL rules so columns and constraints are still visible.
    """
    text = "".join(
        f"{node.token.prefix}{node.token.token}"
        for node in nodes
        if node.token.type is TokenType.DATA
    )
    from sqlfmt.rules.ddl import create_table_passes_through

    if create_table_passes_through(text.lstrip(), 0):
        return _parse_name_only(text)
    relexed = _relex_ddl(text)
    if relexed:
        parsed = _parse_statement(relexed)
        if parsed is not None:
            return parsed
    return _parse_name_only(text)


def _parse_name_only(text: str) -> Optional[DdlTable]:
    import re

    match = re.search(
        r"create\s+(?:or\s+replace\s+)?"
        r"(?:(?:global|local|temp|temporary|transient|volatile|unlogged"
        r"|external|iceberg|dynamic)\s+)*"
        r"table\s+(?:if\s+not\s+exists\s+)?"
        r"(?P<name>(?:(?:\"[^\"]+\"|`[^`]+`|\[[^\]]+\]|[A-Za-z_][\w]*)\s*\.\s*)*"
        r"(?:\"[^\"]+\"|`[^`]+`|\[[^\]]+\]|[A-Za-z_][\w]*))",
        text,
        flags=re.IGNORECASE | re.DOTALL,
    )
    if not match:
        return None
    name = " ".join(match.group("name").split())
    return DdlTable(name, [], [])


def _relex_ddl(text: str) -> List[Node]:
    from sqlfmt.analyzer import Analyzer
    from sqlfmt.rules.ddl import DDL

    analyzer = Analyzer(
        line_length=88,
        rules=sorted(DDL, key=lambda rule: rule.priority),
        node_manager=NodeManager(case_sensitive_names=False),
    )
    query = analyzer.parse_query(text)
    nodes: List[Node] = []
    for line in query.lines:
        nodes.extend(line.nodes)
    return nodes


def _parse_column(item: Sequence[Node]) -> DdlColumn:
    name = item[0].token.token.strip()
    type_nodes: List[Node] = []
    has_inline = False
    index = 1
    while index < len(item):
        if _same_depth(item[index], item[0]):
            phrase = _match_phrase(item, index, _INLINE_PHRASES)
            if phrase is not None:
                has_inline = True
                break
        type_nodes.append(item[index])
        index += 1
    return DdlColumn(name, _render_type(type_nodes), has_inline)


def _render_type(nodes: Sequence[Node]) -> str:
    if not nodes:
        return ""
    parts: List[str] = []
    for index, node in enumerate(nodes):
        text = _type_token_text(nodes, index)
        if index == 0:
            parts.append(text)
        else:
            parts.append(f"{node.token.prefix}{text}")
    return "".join(parts).strip()


def _type_token_text(nodes: Sequence[Node], index: int) -> str:
    token = nodes[index].token
    raw = token.token
    if token.type is TokenType.QUOTED_NAME or not any(char.isalpha() for char in raw):
        return raw
    if token.type in {
        TokenType.DDL_KEYWORD,
        TokenType.UNTERM_KEYWORD,
        TokenType.WORD_OPERATOR,
        TokenType.BOOLEAN_OPERATOR,
        TokenType.ON,
        TokenType.BRACKET_OPEN,
    }:
        return raw.lower()
    folded = " ".join(raw.lower().split())
    if folded in _TYPE_WORDS or raw.lower() in _TYPE_WORDS:
        return raw.lower()
    if index == 0 or _adjacent_to_type_constructor(nodes, index):
        return raw.lower()
    return raw


def _adjacent_to_type_constructor(nodes: Sequence[Node], index: int) -> bool:
    """Lowercase qualified names and names that call a type constructor."""
    if index + 1 < len(nodes):
        nxt = nodes[index + 1].token
        if nxt.type is TokenType.DOT:
            return True
        if nxt.type is TokenType.BRACKET_OPEN and nxt.token == "(":
            return True
    if index > 0 and nodes[index - 1].token.type is TokenType.DOT:
        return True
    return False


def _render_identifier(nodes: Sequence[Node]) -> str:
    if not nodes:
        return ""
    parts = [nodes[0].token.token]
    for node in nodes[1:]:
        parts.append(f"{node.token.prefix}{node.token.token}")
    return " ".join("".join(parts).split())


def _consume_header(nodes: Sequence[Node], index: int) -> Optional[int]:
    if index >= len(nodes):
        return None
    words = _words(nodes[index])
    if not words or words[0] != "create":
        return None
    if _is_exact_header(words):
        return index + 1
    if words != ["create"]:
        return None
    index += 1
    if (
        index + 1 < len(nodes)
        and _words(nodes[index]) == ["or"]
        and _words(nodes[index + 1]) == ["replace"]
    ):
        index += 2
    while (
        index < len(nodes)
        and _words(nodes[index])[:1]
        and _words(nodes[index])[0] in _HEADER_MODIFIERS
    ):
        if len(_words(nodes[index])) != 1:
            break
        index += 1
    if index >= len(nodes) or _words(nodes[index]) != ["table"]:
        return None
    index += 1
    if (
        index + 2 < len(nodes)
        and _words(nodes[index]) == ["if"]
        and _words(nodes[index + 1]) == ["not"]
        and _words(nodes[index + 2]) == ["exists"]
    ):
        index += 3
    return index


def _is_exact_header(words: Sequence[str]) -> bool:
    index = 0
    if not words or words[0] != "create":
        return False
    index = 1
    if (
        index + 1 < len(words)
        and words[index] == "or"
        and words[index + 1] == "replace"
    ):
        index += 2
    while index < len(words) and words[index] in _HEADER_MODIFIERS:
        index += 1
    if index >= len(words) or words[index] != "table":
        return False
    index += 1
    if index == len(words):
        return True
    return words[index:] == ["if", "not", "exists"]


def _ends_table_name(node: Node) -> bool:
    if _is_open_paren(node) or node.token.type is TokenType.SEMICOLON:
        return True
    if node.depth[0] == 0 and node.value in _POST_BODY:
        return True
    return _words(node) in (["as"], ["like"]) and node.depth[0] == 0


def _column_body(nodes: Sequence[Node], index: int) -> Tuple[List[Node], int]:
    depth = 1
    body: List[Node] = []
    while index < len(nodes):
        node = nodes[index]
        if _is_open_paren(node):
            depth += 1
        elif node.token.type is TokenType.BRACKET_CLOSE and node.value == ")":
            depth -= 1
            if depth == 0:
                return body, index + 1
        body.append(node)
        index += 1
    return body, index


def _split_top_level_items(
    body: Sequence[Node], open_paren: Node
) -> List[List[Node]]:
    items: List[List[Node]] = []
    current: List[Node] = []
    for node in body:
        if _is_top_level_comma(node, open_paren):
            if current:
                items.append(current)
            current = []
            continue
        current.append(node)
    if current:
        items.append(current)
    return items


def _is_top_level_comma(node: Node, open_paren: Node) -> bool:
    """
    True for commas that separate columns and table constraints.

    ``timestamp with time zone`` lexes ``with`` as an unterminated keyword, so
    the comma after that type is not the last open bracket. It still separates
    columns as long as no real bracket was opened after the column list.
    """
    if not node.is_comma:
        return False
    try:
        index = next(
            i for i, bracket in enumerate(node.open_brackets) if bracket is open_paren
        )
    except StopIteration:
        return False
    for bracket in node.open_brackets[index + 1 :]:
        if bracket.token.type is not TokenType.UNTERM_KEYWORD:
            return False
        if bracket.value not in {"with", "with recursive"}:
            return False
    return True


def _parse_leading_keyword(item: Sequence[Node]) -> Optional[str]:
    phrase = _leading_phrase(item, _TABLE_CONSTRAINT_PHRASES)
    if phrase is None:
        return None
    return " ".join(phrase)


def _leading_phrase(
    item: Sequence[Node], phrases: Sequence[Sequence[str]]
) -> Optional[Tuple[str, ...]]:
    if not item:
        return None
    return _match_phrase(item, 0, phrases)


def _match_phrase(
    nodes: Sequence[Node], index: int, phrases: Sequence[Sequence[str]]
) -> Optional[Tuple[str, ...]]:
    for phrase in phrases:
        if _phrase_matches(nodes, index, phrase):
            return tuple(phrase)
    return None


def _phrase_matches(nodes: Sequence[Node], index: int, phrase: Sequence[str]) -> bool:
    got: List[str] = []
    cursor = index
    while cursor < len(nodes) and len(got) < len(phrase):
        words = _words(nodes[cursor])
        if not words or words != list(phrase[len(got) : len(got) + len(words)]):
            return False
        got.extend(words)
        cursor += 1
    return got == list(phrase)


def _words(node: Node) -> List[str]:
    return node.value.lower().split()


def _same_depth(left: Node, right: Node) -> bool:
    return left.open_brackets == right.open_brackets


def _is_open_paren(node: Node) -> bool:
    return node.token.type is TokenType.BRACKET_OPEN and node.value == "("


def _statement_nodes(lines: Sequence[Line]) -> Iterable[List[Node]]:
    for group in _statement_line_groups(list(lines)):
        nodes: List[Node] = []
        for line in group:
            nodes.extend(line.nodes)
        if any(not node.is_newline for node in nodes):
            yield nodes


def _statement_line_groups(lines: List[Line]) -> List[List[Line]]:
    groups: List[List[Line]] = []
    current: List[Line] = []
    for line in lines:
        current.append(line)
        if any(node.token.type is TokenType.SEMICOLON for node in line.nodes):
            groups.append(current)
            current = []
    if current:
        groups.append(current)
    return groups


def _relayout_statement(
    lines: List[Line],
    max_length: int,
    node_manager: NodeManager,
    splitter: LineSplitter,
    merger: LineMerger,
) -> List[Line]:
    content_indexes = [i for i, line in enumerate(lines) if _line_has_content(line)]
    if not content_indexes:
        return lines
    body = lines[content_indexes[0] : content_indexes[-1] + 1]
    nodes = [node for line in body for node in line.nodes if not node.is_newline]
    if not nodes or not _is_create_table_node(nodes[0]):
        return lines

    if _statement_locked(nodes):
        return lines

    segments = _layout_segments(nodes)
    comments = [comment for line in body for comment in line.comments]
    buckets = _assign_comments(segments, comments)
    laid_out: List[Line] = []
    for segment, segment_comments, kind in zip(
        segments, buckets, _segment_kinds(segments), strict=True
    ):
        attached, preface = _split_preface_comments(segment, segment_comments)
        laid_out.extend(preface)
        if kind in {"column", "constraint"}:
            _drop_type_with_depth(segment)
        if kind == "column":
            _apply_type_case(segment)
        laid_out.extend(
            _emit_segment(
                segment,
                attached,
                kind,
                max_length,
                node_manager,
                splitter,
                merger,
            )
        )
    return lines[: content_indexes[0]] + laid_out + lines[content_indexes[-1] + 1 :]


def _line_has_content(line: Line) -> bool:
    return any(not node.is_newline for node in line.nodes)


def _statement_locked(nodes: Sequence[Node]) -> bool:
    """Leave fmt-off regions and jinja statements to the generic formatter."""
    for node in nodes:
        if node.formatting_disabled:
            return True
        if node.token.type in (TokenType.FMT_OFF, TokenType.FMT_ON):
            return True
        if node.token.type.is_jinja:
            return True
    return False


def _is_create_table_node(node: Node) -> bool:
    return node.token.type is TokenType.DDL_KEYWORD and _is_exact_header(_words(node))


def _layout_segments(nodes: Sequence[Node]) -> List[List[Node]]:
    open_index = _column_list_open_index(nodes)
    if open_index is None:
        return _post_body_segments(list(nodes))

    open_paren = nodes[open_index]
    header = list(nodes[: open_index + 1])
    body, close_index = _column_body(nodes, open_index + 1)
    # _column_body's index is relative to the sliced call... we passed the full
    # sequence and an index, and it returns the index AFTER the close paren
    # in that same sequence. Good.
    close_node = nodes[close_index - 1] if close_index > open_index else None
    segments: List[List[Node]] = [header]
    current: List[Node] = []
    for node in body:
        if _is_top_level_comma(node, open_paren):
            current.append(node)
            segments.append(current)
            current = []
        else:
            current.append(node)
    if current:
        segments.append(current)
    if close_node is not None and _is_close_paren(close_node):
        segments.append([close_node])
    rest = list(nodes[close_index:])
    segments.extend(_post_body_segments(rest))
    return [segment for segment in segments if segment]


def _column_list_open_index(nodes: Sequence[Node]) -> Optional[int]:
    for index, node in enumerate(nodes):
        if node.depth[0] == 0 and node.value in _POST_BODY:
            return None
        if _is_open_paren(node) and node.depth[0] == 0:
            return index
    return None


def _post_body_segments(nodes: List[Node]) -> List[List[Node]]:
    if not nodes:
        return []
    segments: List[List[Node]] = []
    current: List[Node] = []
    for node in nodes:
        if node.token.type is TokenType.SEMICOLON:
            if current:
                segments.append(current)
                current = []
            segments.append([node])
            continue
        if current and _is_post_body_keyword(node):
            segments.append(current)
            current = [node]
            continue
        current.append(node)
    if current:
        segments.append(current)
    return segments


def _is_post_body_keyword(node: Node) -> bool:
    return (
        node.token.type is TokenType.DDL_KEYWORD
        and node.value in _POST_BODY
        and node.depth[0] == 0
    )


def _is_close_paren(node: Node) -> bool:
    return node.token.type is TokenType.BRACKET_CLOSE and node.value == ")"


def _segment_kinds(segments: Sequence[Sequence[Node]]) -> List[str]:
    kinds: List[str] = []
    for segment in segments:
        first = segment[0]
        if first.token.type is TokenType.SEMICOLON:
            kinds.append("force")
        elif _is_close_paren(first) and len(segment) == 1:
            kinds.append("force")
        elif _is_create_table_node(first):
            kinds.append("force")
        elif _is_post_body_keyword(first):
            kinds.append("force")
        elif _parse_leading_keyword(segment) is not None:
            kinds.append("constraint")
        else:
            kinds.append("column")
    return kinds


def _emit_segment(
    nodes: Sequence[Node],
    comments: List[Comment],
    kind: str,
    max_length: int,
    node_manager: NodeManager,
    splitter: LineSplitter,
    merger: LineMerger,
) -> List[Line]:
    line = Line.from_nodes(
        previous_node=nodes[0].previous_node,
        nodes=list(nodes),
        comments=list(comments),
    )
    node_manager.append_newline(line)
    # Columns and post-body clauses stay on one line even past the line length.
    # Table constraints split when that minimal line would be too long.
    if kind in {"force", "column"} or not line.is_too_long(max_length):
        return [line]
    return merger.maybe_merge_lines(splitter.maybe_split(line))


def _drop_type_with_depth(nodes: Sequence[Node]) -> None:
    """
    ``timestamp with time zone`` leaves ``with`` open, which would indent every
    later column by an extra level. Column and constraint lines stay at the
    column-list indent.
    """
    for node in nodes:
        trimmed = [
            bracket
            for bracket in node.open_brackets
            if not (
                bracket.token.type is TokenType.UNTERM_KEYWORD
                and bracket.value in {"with", "with recursive"}
            )
        ]
        if len(trimmed) != len(node.open_brackets):
            node.open_brackets = trimmed


def _apply_type_case(nodes: Sequence[Node]) -> None:
    """Lowercase type names on a column segment, including case-sensitive dialects."""
    body = list(nodes)
    if body and body[-1].is_comma:
        body = body[:-1]
    if len(body) < 2:
        return
    type_nodes: List[Node] = []
    for index in range(1, len(body)):
        if _same_depth(body[index], body[0]) and _match_phrase(
            body, index, _INLINE_PHRASES
        ):
            break
        type_nodes.append(body[index])
    for index, node in enumerate(type_nodes):
        if node.formatting_disabled:
            continue
        lowered = _type_token_text(type_nodes, index)
        # Force type-name lowercase without restoring source case on other words.
        if lowered == node.value.lower() and node.value != lowered:
            node.value = lowered


def _split_preface_comments(
    segment: Sequence[Node], comments: Sequence[Comment]
) -> Tuple[List[Comment], List[Line]]:
    """
    Keep a standalone comment inside the column list indented with the columns
    when the next segment (usually the closing parenthesis) is shallower.
    """
    attached: List[Comment] = []
    preface: List[Line] = []
    segment_depth = len(segment[0].open_brackets) if segment else 0
    for comment in comments:
        anchor = _comment_anchor(comment)
        anchor_depth = len(anchor.open_brackets) if anchor is not None else 0
        if (
            anchor is not None
            and (comment.is_standalone or comment.is_multiline)
            and anchor_depth > segment_depth
        ):
            preface.append(_comment_only_line(comment, anchor))
        else:
            attached.append(comment)
    return attached, preface


def _comment_anchor(comment: Comment) -> Optional[Node]:
    previous = comment.previous_node
    seen: set[int] = set()
    while previous is not None and previous.is_newline and id(previous) not in seen:
        seen.add(id(previous))
        previous = previous.previous_node
    return previous


def _comment_only_line(comment: Comment, anchor: Node) -> Line:
    newline = Node(
        token=Token(TokenType.NEWLINE, "", "\n", anchor.token.epos, anchor.token.epos),
        previous_node=anchor,
        prefix="",
        value="\n",
        open_brackets=list(anchor.open_brackets),
        open_jinja_blocks=list(anchor.open_jinja_blocks),
    )
    return Line(previous_node=anchor, nodes=[newline], comments=[comment])


def _assign_comments(
    segments: Sequence[Sequence[Node]], comments: Sequence[Comment]
) -> List[List[Comment]]:
    buckets: List[List[Comment]] = [[] for _ in segments]
    if not segments:
        return buckets
    index_by_node: Dict[int, int] = {}
    for index, segment in enumerate(segments):
        for node in segment:
            index_by_node[id(node)] = index
    for comment in comments:
        previous = comment.previous_node
        seen: set[int] = set()
        while (
            previous is not None
            and previous.is_newline
            and id(previous) not in seen
        ):
            seen.add(id(previous))
            previous = previous.previous_node
        if previous is not None and id(previous) in index_by_node:
            target = index_by_node[id(previous)]
            if (comment.is_standalone or comment.is_multiline) and target + 1 < len(
                segments
            ):
                target += 1
            buckets[target].append(comment)
        else:
            buckets[0].append(comment)
    return buckets

