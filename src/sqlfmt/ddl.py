from dataclasses import dataclass, field, replace
from typing import List, Optional, Tuple

from sqlfmt.line import Line
from sqlfmt.node import Node
from sqlfmt.node_manager import NodeManager
from sqlfmt.tokens import Token, TokenType

CREATE_TABLE_HEADER_WORDS = {
    "create",
    "or",
    "replace",
    "temp",
    "temporary",
    "table",
    "if",
    "not",
    "exists",
}
INLINE_CONSTRAINT_KEYWORDS = {
    "not null",
    "default",
    "references",
    "constraint",
    "check",
    "null",
}
TABLE_CONSTRAINT_KEYWORDS = {
    "primary key",
    "foreign key",
    "unique",
    "check",
    "constraint",
}
POST_BODY_KEYWORDS = {"partition by", "cluster by", "options"}
# keywords that must be separated from a following opening paren
SPACED_BEFORE_PAREN = {"check", "unique", "primary key", "foreign key", "key"}
UNSPACED_BEFORE_PAREN = {"options"}
# a long table constraint may only be wrapped before one of its clauses
WRAP_BEFORE = TABLE_CONSTRAINT_KEYWORDS | {
    "references",
    "on",
    "match",
    "not",
    "deferrable",
    "initially",
}

INDENT = " " * 4


@dataclass
class DdlColumn:
    name: str
    type_name: str
    has_inline_constraint: bool = False

    def __str__(self) -> str:
        s = f"{self.name} {self.type_name}".strip()
        if self.has_inline_constraint:
            s += " <+constraint>"
        return s


@dataclass
class DdlTableConstraint:
    keyword: str

    def __post_init__(self) -> None:
        self.keyword = _normalize(self.keyword)


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


def _normalize(value: str) -> str:
    return " ".join(value.lower().split())


def _norm(node: Node) -> str:
    if node.token.type is TokenType.QUOTED_NAME:
        return node.token.token
    return _normalize(node.token.token)


def _words(nodes: List[Node]) -> List[str]:
    return " ".join(_norm(n) for n in nodes).split()


@dataclass
class _DdlParts:
    keyword: List[Node]
    name: List[Node]
    open_paren: Node
    items: List[List[Node]]
    close_paren: Node
    clauses: List[List[Node]]
    semicolon: Optional[Node]


def _split_top_level(nodes: List[Node], at_comma: bool) -> List[List[Node]]:
    """
    Splits nodes at depth-0 commas (dropping them) or, if not at_comma,
    before each depth-0 post-body keyword.
    """
    groups: List[List[Node]] = []
    current: List[Node] = []
    depth = 0
    for node in nodes:
        if depth == 0:
            if at_comma and node.token.type is TokenType.COMMA:
                groups.append(current)
                current = []
                continue
            if (
                not at_comma
                and current
                and _norm(node) in POST_BODY_KEYWORDS
                and node.token.type in (TokenType.UNTERM_KEYWORD, TokenType.NAME)
            ):
                groups.append(current)
                current = []
        if node.token.type is TokenType.BRACKET_OPEN:
            depth += 1
        elif node.token.type is TokenType.BRACKET_CLOSE:
            depth -= 1
        current.append(node)
    if current or (at_comma and groups):
        groups.append(current)
    return groups


def _get_parts(lines: List[Line]) -> Optional[_DdlParts]:
    nodes = [
        n for line in lines for n in line.nodes if n.token.type is not TokenType.NEWLINE
    ]
    if not nodes or any(n.token.type.is_jinja or n.formatting_disabled for n in nodes):
        return None

    try:
        open_idx = next(
            i for i, n in enumerate(nodes) if n.token.type is TokenType.BRACKET_OPEN
        )
    except StopIteration:
        return None
    if nodes[open_idx].token.token != "(":
        return None

    header = nodes[:open_idx]
    name_start = 0
    seen_words: List[str] = []
    for i, node in enumerate(header):
        words = _norm(node).split()
        if node.token.type is not TokenType.QUOTED_NAME and all(
            w in CREATE_TABLE_HEADER_WORDS for w in words
        ):
            seen_words.extend(words)
            name_start = i + 1
        else:
            break
    if not seen_words or seen_words[0] != "create" or "table" not in seen_words:
        return None
    name = header[name_start:]
    if not name or any(
        n.token.type not in (TokenType.NAME, TokenType.QUOTED_NAME, TokenType.DOT)
        or (i > 0 and n.token.prefix)
        for i, n in enumerate(name)
    ):
        return None

    depth = 0
    close_idx = None
    for i in range(open_idx, len(nodes)):
        t = nodes[i].token.type
        if t is TokenType.BRACKET_OPEN:
            depth += 1
        elif t is TokenType.BRACKET_CLOSE:
            depth -= 1
            if depth == 0:
                close_idx = i
                break
    if close_idx is None:
        return None

    rest = nodes[close_idx + 1 :]
    semicolon = None
    if rest and rest[-1].token.type is TokenType.SEMICOLON:
        semicolon = rest[-1]
        rest = rest[:-1]
    if any(n.token.type is TokenType.SEMICOLON for n in rest):
        return None
    clauses = _split_top_level(rest, at_comma=False)
    if clauses and _norm(clauses[0][0]) not in POST_BODY_KEYWORDS:
        return None

    items = _split_top_level(nodes[open_idx + 1 : close_idx], at_comma=True)
    if not items or any(not item for item in items):
        return None

    return _DdlParts(
        keyword=header[:name_start],
        name=name,
        open_paren=nodes[open_idx],
        items=items,
        close_paren=nodes[close_idx],
        clauses=clauses,
        semicolon=semicolon,
    )


def _is_table_constraint(item: List[Node]) -> bool:
    words = _words(item[:2])
    if not words:
        return False
    first = words[0]
    two = " ".join(words[:2])
    return first in TABLE_CONSTRAINT_KEYWORDS or two in TABLE_CONSTRAINT_KEYWORDS


def _table_constraint_keyword(item: List[Node]) -> str:
    words = _words(item[:2])
    two = " ".join(words[:2])
    if two in ("primary key", "foreign key"):
        return two
    return words[0]


def _constraint_start(item: List[Node]) -> Optional[int]:
    """
    Index of the first depth-0 inline constraint keyword in a column
    definition (after the column name), or None
    """
    depth = 0
    for i, node in enumerate(item):
        t = node.token.type
        if i > 0 and depth == 0 and t is not TokenType.QUOTED_NAME:
            value = _norm(node)
            if value in INLINE_CONSTRAINT_KEYWORDS:
                return i
            if value == "not" and i + 1 < len(item) and _norm(item[i + 1]) == "null":
                return i
        if t is TokenType.BRACKET_OPEN:
            depth += 1
        elif t is TokenType.BRACKET_CLOSE:
            depth -= 1
    return None


def _reconstruct(nodes: List[Node]) -> str:
    """
    Rebuild source text of nodes using their original inter-token whitespace
    """
    parts: List[str] = []
    for i, node in enumerate(nodes):
        value = (
            _norm(node)
            if node.token.type is not TokenType.QUOTED_NAME
            else (node.token.token)
        )
        if i > 0:
            prev = nodes[i - 1].token
            if prev.epos == node.token.spos:
                parts.append(node.token.prefix)
            else:
                parts.append(" ")
        parts.append(value)
    return "".join(parts).strip()


def parse_ddl_table(lines: List[Line]) -> Optional[DdlTable]:
    """
    Returns a DdlTable describing a CREATE TABLE statement parsed into lines,
    or None if the lines do not contain a CREATE TABLE statement with a
    column list
    """
    parts = _get_parts(lines)
    if parts is None:
        return None
    columns: List[DdlColumn] = []
    constraints: List[DdlTableConstraint] = []
    for item in parts.items:
        if _is_table_constraint(item):
            constraints.append(DdlTableConstraint(_table_constraint_keyword(item)))
            continue
        start = _constraint_start(item)
        type_nodes = item[1:start] if start is not None else item[1:]
        columns.append(
            DdlColumn(
                name=_reconstruct(item[:1]),
                type_name=_reconstruct(type_nodes),
                has_inline_constraint=start is not None,
            )
        )
    return DdlTable(
        table_name="".join(_norm(n) for n in parts.name),
        columns=columns,
        table_constraints=constraints,
    )


def _prefixes(nodes: List[Node]) -> List[str]:
    prefixes: List[str] = []
    for i, node in enumerate(nodes):
        if i == 0:
            prefixes.append("")
            continue
        prev = _norm(nodes[i - 1])
        if node.token.type is TokenType.BRACKET_OPEN and node.token.token == "(":
            if prev in SPACED_BEFORE_PAREN:
                prefixes.append(" ")
                continue
            if prev in UNSPACED_BEFORE_PAREN:
                prefixes.append("")
                continue
        prefixes.append(node.prefix)
    return prefixes


def _render(nodes: List[Node], prefixes: List[str]) -> str:
    return "".join(p + _value(n) for n, p in zip(nodes, prefixes, strict=True))


def _value(node: Node) -> str:
    if node.token.type in (TokenType.QUOTED_NAME, TokenType.NUMBER):
        return node.value
    if node.token.type is TokenType.NAME and _norm(node) not in (
        INLINE_CONSTRAINT_KEYWORDS | TABLE_CONSTRAINT_KEYWORDS | {"key"}
    ):
        return node.value
    return " ".join(node.value.lower().split())


def _wrap(
    nodes: List[Node], depth: int, max_length: int
) -> List[Tuple[int, List[Node], List[str]]]:
    """
    Greedily wraps nodes at depth-0 spaces so that no line exceeds
    max_length; continuation lines are indented one extra level. Lines only
    break before constraint clauses, so argument lists are never split.
    """
    prefixes = _prefixes(nodes)
    units: List[Tuple[List[Node], List[str]]] = []
    bracket_depth = 0
    for node, prefix in zip(nodes, prefixes, strict=True):
        if not units or (bracket_depth == 0 and _norm(node) in WRAP_BEFORE):
            units.append(([], []))
        units[-1][0].append(node)
        units[-1][1].append(prefix)
        if node.token.type is TokenType.BRACKET_OPEN:
            bracket_depth += 1
        elif node.token.type is TokenType.BRACKET_CLOSE:
            bracket_depth -= 1

    out: List[Tuple[int, List[Node], List[str]]] = []
    cur_nodes: List[Node] = []
    cur_prefixes: List[str] = []
    cur_depth = depth
    for u_nodes, u_prefixes in units:
        if cur_nodes:
            candidate = len(INDENT * cur_depth) + len(
                _render(cur_nodes + u_nodes, cur_prefixes + u_prefixes)
            )
            if candidate > max_length:
                out.append((cur_depth, cur_nodes, cur_prefixes))
                cur_nodes, cur_prefixes = [], []
                cur_depth = depth + 1
        if not cur_nodes:
            u_prefixes = [""] + u_prefixes[1:]
        cur_nodes = cur_nodes + u_nodes
        cur_prefixes = cur_prefixes + u_prefixes
    if cur_nodes:
        out.append((cur_depth, cur_nodes, cur_prefixes))
    return out


def _make_line(
    depth: int,
    nodes: List[Node],
    prefixes: List[str],
    bracket: Node,
    node_manager: NodeManager,
    previous_node: Optional[Node],
) -> Line:
    new_nodes = [
        replace(n, prefix=p, value=_value(n), open_brackets=[bracket] * depth)
        for n, p in zip(nodes, prefixes, strict=True)
    ]
    for i in range(1, len(new_nodes)):
        new_nodes[i].previous_node = new_nodes[i - 1]
    if new_nodes:
        new_nodes[0].previous_node = previous_node
    line = Line(previous_node=previous_node, nodes=new_nodes)
    node_manager.append_newline(line)
    return line


def format_ddl_lines(
    lines: List[Line], max_length: int, case_sensitive_names: bool = False
) -> Optional[List[Line]]:
    """
    Formats the lines of a single CREATE TABLE statement. Returns None if the
    lines are not a supported CREATE TABLE statement.
    """
    lead: List[Line] = []
    for line in lines:
        if any(n.token.type is not TokenType.NEWLINE for n in line.nodes):
            break
        lead.append(line)
    body = lines[len(lead) :]
    parts = _get_parts(body)
    if parts is None:
        return None

    node_manager = NodeManager(case_sensitive_names)
    out: List[Line] = []
    prev: Optional[Node] = lead[-1].nodes[-1] if lead and lead[-1].nodes else None
    if prev is None and lines and lines[0].previous_node is not None:
        prev = lines[0].previous_node

    def emit(depth: int, nodes: List[Node], prefixes: List[str]) -> None:
        nonlocal prev
        line = _make_line(depth, nodes, prefixes, parts.open_paren, node_manager, prev)
        out.append(line)
        prev = line.nodes[-1]

    header = [*parts.keyword, *parts.name, parts.open_paren]
    header_prefixes = _prefixes(header)
    header_prefixes[-1] = " "
    emit(0, header, header_prefixes)

    last = len(parts.items) - 1
    for i, item in enumerate(parts.items):
        nodes = list(item)
        if i < last:
            comma = Token(
                TokenType.COMMA, "", ",", item[-1].token.epos, item[-1].token.epos
            )
            nodes.append(
                Node(token=comma, previous_node=item[-1], prefix="", value=",")
            )
        if _is_table_constraint(item):
            for depth, w_nodes, w_prefixes in _wrap(nodes, 1, max_length):
                emit(depth, w_nodes, w_prefixes)
        else:
            emit(1, nodes, _prefixes(nodes))

    emit(0, [parts.close_paren], [""])
    for clause in parts.clauses:
        emit(0, clause, _prefixes(clause))
    if parts.semicolon is not None:
        emit(0, [parts.semicolon], [""])

    comments = [c for line in body for c in line.comments]
    out[0].comments = comments
    return lead + out
