"""Parse and lay out CREATE TABLE statements."""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import Iterable, List, Optional, Sequence, Tuple

from sqlfmt.line import Line
from sqlfmt.node import Node
from sqlfmt.node_manager import NodeManager
from sqlfmt.tokens import TokenType

_POST_BODY = {"partition by", "cluster by", "options"}
_INLINE_STARTS = {"default", "references", "constraint", "check", "null"}
_TABLE_STARTS = {"primary", "foreign", "unique", "check", "constraint"}
_SKIP_BEFORE_TABLE = {
    "or",
    "replace",
    "global",
    "local",
    "temp",
    "temporary",
    "transient",
    "external",
    "secure",
    "unlogged",
    "volatile",
    "iceberg",
    "dynamic",
}


@dataclass
class _Tok:
    text: str
    prefix: str
    kind: str  # word, quoted, symbol, jinja
    node: Optional[Node] = None

    @property
    def lower(self) -> str:
        return self.text.lower()


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
    """A table-level constraint. ``keyword`` is the leading keyword, lowercased."""

    keyword: str

    def __post_init__(self) -> None:
        self.keyword = self.keyword.lower()


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
        return [col for col in self.columns if col.has_inline_constraint]

    @property
    def unconstrained_columns(self) -> List[DdlColumn]:
        return [col for col in self.columns if not col.has_inline_constraint]


def parse_ddl_table(lines: List[Line]) -> Optional[DdlTable]:
    """Parse a CREATE TABLE statement from analyzer lines.

    Returns None when ``lines`` is not a CREATE TABLE statement. Line breaks
    are ignored; tokens are read in order so both raw and formatted queries
    produce the same table.
    """
    tokens = list(_iter_tokens(lines))
    if not tokens:
        return None
    return _parse_tokens(tokens)


def relayout_create_table(lines: List[Line], node_manager: NodeManager) -> List[Line]:
    """Re-break parenthesized CREATE TABLE statements to the DDL layout."""
    out: List[Line] = []
    i = 0
    while i < len(lines):
        if _line_starts_create(lines[i]):
            end = _statement_end(lines, i)
            block = lines[i:end]
            formatted = _format_block(block, node_manager)
            if formatted is not None:
                if out:
                    formatted[0].previous_node = out[-1].nodes[-1]
                out.extend(formatted)
                if end < len(lines) and formatted and formatted[-1].nodes:
                    lines[end].previous_node = formatted[-1].nodes[-1]
                i = end
                continue
        out.append(lines[i])
        i += 1
    return out


def _iter_tokens(lines: Sequence[Line]) -> Iterable[_Tok]:
    for line in lines:
        for node in line.nodes:
            if node.is_newline:
                continue
            if node.token.type is TokenType.DATA:
                yield from _tokenize_text(node.token.prefix + node.token.token)
            else:
                yield _tok_from_node(node)


def _tok_from_node(node: Node) -> _Tok:
    token = node.token
    if token.type is TokenType.QUOTED_NAME:
        kind = "quoted"
    elif token.type.is_jinja:
        kind = "jinja"
    elif token.type in (
        TokenType.NAME,
        TokenType.UNTERM_KEYWORD,
        TokenType.WORD_OPERATOR,
        TokenType.BOOLEAN_OPERATOR,
        TokenType.NUMBER,
    ) or (token.type is TokenType.BRACKET_OPEN and token.token[:1].isalpha()):
        kind = "word"
    else:
        kind = "symbol"
    return _Tok(text=token.token, prefix=token.prefix, kind=kind, node=node)


_TEXT_TOK = re.compile(
    r"(\s+)"
    r"|(--[^\n]*|/\*.*?\*/|#[^\n]*)"
    r"|((?:'(?:''|[^'])*')|(?:\"(?:\"\"|[^\"])*\")|(?:`(?:``|[^`])*`)|(?:\[[^\]]+\]))"
    r"|(\{\{.*?\}\}|\{%.*?%\}|\{#.*?#\})"
    r"|([A-Za-z_][\w$]*)"
    r"|([(),.;<>])"
    r"|(\S)",
    re.DOTALL,
)


def _tokenize_text(text: str) -> Iterable[_Tok]:
    prefix = ""
    for match in _TEXT_TOK.finditer(text):
        ws, comment, quoted, jinja, word, symbol, other = match.groups()
        if ws:
            prefix += ws
            continue
        if comment:
            prefix += comment
            continue
        if quoted:
            yield _Tok(quoted, prefix, "quoted")
        elif jinja:
            yield _Tok(jinja, prefix, "jinja")
        elif word:
            yield _Tok(word, prefix, "word")
        elif symbol:
            yield _Tok(symbol, prefix, "symbol")
        else:
            yield _Tok(other or "", prefix, "symbol")
        prefix = ""


def _parse_tokens(tokens: List[_Tok]) -> Optional[DdlTable]:
    idx = 0
    n = len(tokens)

    if idx >= n or tokens[idx].lower != "create" or tokens[idx].kind != "word":
        return None
    idx += 1
    while idx < n and tokens[idx].kind == "word" and tokens[idx].lower in _SKIP_BEFORE_TABLE:
        idx += 1
    if idx >= n or tokens[idx].lower != "table" or tokens[idx].kind != "word":
        return None
    idx += 1
    if (
        idx + 2 < n
        and tokens[idx].lower == "if"
        and tokens[idx + 1].lower == "not"
        and tokens[idx + 2].lower == "exists"
    ):
        idx += 3
    name, idx = _read_table_name(tokens, idx)
    if name is None:
        return None
    if idx >= n or tokens[idx].text != "(":
        return DdlTable(table_name=name, columns=[])

    idx += 1
    depth = 1
    columns: List[DdlColumn] = []
    constraints: List[DdlTableConstraint] = []
    item: List[_Tok] = []

    def flush(raw_item: List[_Tok]) -> None:
        body = _strip_trailing_comma(raw_item)
        if not _has_content(body):
            return
        if _is_table_constraint(body):
            constraints.append(DdlTableConstraint(keyword=_constraint_keyword(body)))
        else:
            columns.append(_parse_column(body))

    while idx < n and depth > 0:
        tok = tokens[idx]
        if tok.text == "(" and tok.kind == "symbol":
            depth += 1
            item.append(tok)
        elif tok.text == ")" and tok.kind == "symbol":
            depth -= 1
            if depth == 0:
                flush(item)
                break
            item.append(tok)
        elif tok.text == "," and tok.kind == "symbol" and depth == 1:
            item.append(tok)
            flush(item)
            item = []
        else:
            item.append(tok)
        idx += 1

    return DdlTable(table_name=name, columns=columns, table_constraints=constraints)


def _read_table_name(tokens: Sequence[_Tok], idx: int) -> Tuple[Optional[str], int]:
    parts: List[str] = []
    n = len(tokens)
    while idx < n:
        tok = tokens[idx]
        if tok.kind == "word" and tok.lower in {"as", "like"} and parts:
            break
        if tok.kind in {"word", "quoted", "jinja"} or tok.text == ".":
            if tok.text == ".":
                parts.append(".")
            elif tok.kind == "quoted" or tok.kind == "jinja":
                parts.append(tok.text)
            else:
                parts.append(tok.lower)
            idx += 1
            continue
        break
    if not parts:
        return None, idx
    return "".join(parts), idx


def _strip_trailing_comma(item: Sequence[_Tok]) -> List[_Tok]:
    body = list(item)
    while body and body[-1].text == "," and body[-1].kind == "symbol":
        body.pop()
    return body


def _has_content(item: Sequence[_Tok]) -> bool:
    return any(tok.kind != "symbol" or tok.text not in {","} for tok in item)


def _is_table_constraint(item: Sequence[_Tok]) -> bool:
    words = [tok.lower for tok in item if tok.kind == "word"]
    if not words or words[0] not in _TABLE_STARTS:
        return False
    if words[0] in {"primary", "foreign"}:
        return len(words) > 1 and words[1] == "key"
    return True


def _constraint_keyword(item: Sequence[_Tok]) -> str:
    words = [tok.lower for tok in item if tok.kind == "word"]
    if words[0] in {"primary", "foreign"}:
        return f"{words[0]} key"
    return words[0]


def _parse_column(item: Sequence[_Tok]) -> DdlColumn:
    name_tok = next(tok for tok in item if tok.kind in {"word", "quoted", "jinja"})
    name = name_tok.text if name_tok.kind != "word" else name_tok.lower
    rest = item[item.index(name_tok) + 1 :]
    split_at = _inline_split(rest)
    type_toks = rest[:split_at]
    has_constraint = split_at < len(rest)
    return DdlColumn(
        name=name,
        type_name=_render_type(type_toks),
        has_inline_constraint=has_constraint,
    )


def _inline_split(tokens: Sequence[_Tok]) -> int:
    i = 0
    while i < len(tokens):
        tok = tokens[i]
        if tok.kind == "word" and tok.lower == "not":
            nxt = _next_word(tokens, i + 1)
            if nxt is not None and tokens[nxt].lower == "null":
                return i
        elif tok.kind == "word" and tok.lower in _INLINE_STARTS:
            return i
        i += 1
    return len(tokens)


def _next_word(tokens: Sequence[_Tok], start: int) -> Optional[int]:
    j = start
    while j < len(tokens):
        if tokens[j].kind == "word":
            return j
        j += 1
    return None


def _render_type(tokens: Sequence[_Tok]) -> str:
    chunks: List[str] = []
    for tok in tokens:
        body = tok.text.lower() if tok.kind == "word" else tok.text
        chunks.append(tok.prefix + body)
    return "".join(chunks).strip()


def _line_starts_create(line: Line) -> bool:
    for node in line.nodes:
        if node.is_newline:
            continue
        if node.formatting_disabled:
            return False
        return (
            node.value.lower() == "create"
            and node.token.type is not TokenType.DATA
            and node.depth == (0, 0)
        )
    return False


def _statement_end(lines: Sequence[Line], start: int) -> int:
    for i in range(start, len(lines)):
        for node in lines[i].nodes:
            if node.token.type is TokenType.SEMICOLON:
                return i + 1
    return len(lines)


def _format_block(block: Sequence[Line], node_manager: NodeManager) -> Optional[List[Line]]:
    nodes: List[Node] = []
    comments = []
    for line in block:
        comments.extend(line.comments)
        for node in line.nodes:
            if node.formatting_disabled:
                return None
            if not node.is_newline:
                nodes.append(node)
    paren = _column_list_paren(nodes)
    if paren is None:
        return None
    paren_i = nodes.index(paren)
    close_i = _matching_close(nodes, paren_i)
    if close_i is None:
        return None

    header = nodes[: paren_i + 1]
    inner = nodes[paren_i + 1 : close_i]
    tail = nodes[close_i:]

    items = _split_items(inner, paren)
    previous = block[0].previous_node
    new_lines: List[Line] = []

    def emit(group: List[Node]) -> None:
        nonlocal previous
        if not group:
            return
        ids = {id(n) for n in group}
        group_comments = [
            c
            for c in comments
            if c.previous_node is not None and id(c.previous_node) in ids
        ]
        line = Line.from_nodes(
            previous_node=previous, nodes=group, comments=group_comments
        )
        node_manager.append_newline(line)
        new_lines.append(line)
        previous = line.nodes[-1]

    # comments that precede the statement stay on the header line
    emit(header)
    if new_lines:
        leading = [
            c
            for c in comments
            if c.previous_node is None or id(c.previous_node) not in {id(n) for n in nodes}
        ]
        if leading:
            new_lines[0].comments = leading + new_lines[0].comments

    for item in items:
        emit(item)
    # closing paren and anything up to the next post-body keyword or semicolon
    rest = tail
    if rest and rest[0].token.type is TokenType.BRACKET_CLOSE:
        emit([rest[0]])
        rest = rest[1:]
    clause: List[Node] = []
    for node in rest:
        if node.token.type is TokenType.SEMICOLON:
            if clause:
                emit(clause)
                clause = []
            emit([node])
        elif " ".join(node.value.lower().split()) in _POST_BODY and clause:
            emit(clause)
            clause = [node]
        else:
            clause.append(node)
    if clause:
        emit(clause)
    return new_lines or None


def _column_list_paren(nodes: Sequence[Node]) -> Optional[Node]:
    """Return the column-list '(' of a CREATE TABLE, if this is that statement."""
    values: List[str] = []
    saw_paren: Optional[Node] = None
    for node in nodes:
        if node.token.type is TokenType.BRACKET_OPEN and node.value == "(":
            saw_paren = node
            break
        if node.token.type is TokenType.SEMICOLON:
            return None
        values.append(node.value.lower())
    if saw_paren is None:
        return None
    try:
        table_at = len(values) - 1 - values[::-1].index("table")
    except ValueError:
        return None
    if "create" not in values[:table_at]:
        return None
    after = values[table_at + 1 :]
    if after[:3] == ["if", "not", "exists"]:
        after = after[3:]
    if not after:
        return None
    # CREATE TABLE ... AS / LIKE are not column-list statements
    if after[-1] in {"as", "like"}:
        return None
    if saw_paren.depth != (0, 0):
        return None
    return saw_paren


def _matching_close(nodes: Sequence[Node], paren_i: int) -> Optional[int]:
    depth = 0
    for i in range(paren_i, len(nodes)):
        node = nodes[i]
        if node.token.type is TokenType.BRACKET_OPEN and node.value == "(":
            depth += 1
        elif node.token.type is TokenType.BRACKET_CLOSE and node.value == ")":
            depth -= 1
            if depth == 0:
                return i
    return None


def _split_items(inner: Sequence[Node], paren: Node) -> List[List[Node]]:
    items: List[List[Node]] = []
    current: List[Node] = []

    def is_sep(node: Node) -> bool:
        return bool(
            node.is_comma and node.open_brackets and node.open_brackets[-1] is paren
        )

    for node in inner:
        if is_sep(node):
            current.append(node)
            items.append(current)
            current = []
        else:
            current.append(node)
    if current:
        items.append(current)
    items = [item for item in items if any(not is_sep(n) for n in item)]
    if items and is_sep(items[-1][-1]):
        items[-1] = items[-1][:-1]
    return items
