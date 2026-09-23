"""Lex rules for CREATE TABLE statements."""

import re
from functools import partial
from typing import TYPE_CHECKING, List, Optional, Tuple

from sqlfmt import actions
from sqlfmt.rule import Rule
from sqlfmt.rules.common import group
from sqlfmt.tokens import TokenType

if TYPE_CHECKING:
    from sqlfmt.analyzer import Analyzer

# Populated by sqlfmt.rules once MAIN exists. Lex actions read this at runtime.
DDL: List[Rule] = []

_MODIFIER = (
    r"global|local|temp|temporary|transient|volatile|unlogged|external|iceberg|dynamic"
)
CREATE_TABLE_HEADER = (
    r"create(?:\s+or\s+replace)?"
    rf"(?:\s+(?:{_MODIFIER}))*"
    r"\s+table"
    r"(?:\s+if\s+not\s+exists)?"
)

# Phrases that take a space before "(" and stay on the same line as their
# arguments when the splitter would otherwise treat them as names.
_DDL_PHRASES = (
    r"primary\s+key",
    r"foreign\s+key",
    r"unique",
    r"check",
    r"constraint",
    r"references",
    r"default",
    r"not\s+null",
    r"null",
    r"options",
    r"partition\s+by",
    r"cluster\s+by",
)

DDL_SPECIFIC: List[Rule] = [
    Rule(
        name="ddl_bracket_open",
        priority=499,  # before CORE bracket_open (500)
        pattern=group(r"\("),
        action=actions.handle_ddl_column_list_paren,
    ),
    Rule(
        name="create_table_header",
        priority=1001,
        pattern=group(CREATE_TABLE_HEADER) + group(r"\W", r"$"),
        action=partial(
            actions.handle_reserved_keyword,
            action=partial(
                actions.add_node_to_buffer, token_type=TokenType.DDL_KEYWORD
            ),
        ),
    ),
    Rule(
        name="ddl_keyword",
        priority=1002,
        pattern=group(*_DDL_PHRASES) + group(r"\W", r"$"),
        action=partial(
            actions.handle_reserved_keyword,
            action=partial(
                actions.add_node_to_buffer, token_type=TokenType.DDL_KEYWORD
            ),
        ),
    ),
]


def lex_create_table(
    analyzer: "Analyzer", source_string: str, match: re.Match
) -> None:
    """Lex a CREATE TABLE statement, or leave AS/LIKE forms untouched."""
    from sqlfmt.rules.unsupported import UNSUPPORTED

    if create_table_passes_through(source_string, analyzer.pos):
        actions.lex_ruleset(
            analyzer, source_string, match, new_ruleset=UNSUPPORTED
        )
    else:
        actions.lex_ruleset(analyzer, source_string, match, new_ruleset=DDL)


_MODIFIERS = {
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


def create_table_passes_through(source: str, pos: int) -> bool:
    """
    True for CREATE TABLE AS ... and CREATE TABLE ... LIKE ...

    Those statements are out of scope and must be left unchanged.
    """
    tokens = list(_rough_tokens(source, pos))
    index = _consume_header(tokens, 0)
    if index is None:
        return False
    index = _consume_table_name(tokens, index)
    return _has_as_or_like(tokens, index)


def _consume_header(
    tokens: List[Tuple[str, str]], index: int
) -> Optional[int]:
    if index >= len(tokens) or tokens[index] != ("word", "create"):
        return None
    index += 1
    if (
        index + 1 < len(tokens)
        and tokens[index] == ("word", "or")
        and tokens[index + 1] == ("word", "replace")
    ):
        index += 2
    while (
        index < len(tokens)
        and tokens[index][0] == "word"
        and tokens[index][1] in _MODIFIERS
    ):
        index += 1
    if index >= len(tokens) or tokens[index] != ("word", "table"):
        return None
    index += 1
    if (
        index + 2 < len(tokens)
        and tokens[index] == ("word", "if")
        and tokens[index + 1] == ("word", "not")
        and tokens[index + 2] == ("word", "exists")
    ):
        index += 3
    return index


def _consume_table_name(tokens: List[Tuple[str, str]], index: int) -> int:
    if index >= len(tokens):
        return index
    if not _is_ident(tokens[index]):
        return index
    index += 1
    while index + 1 < len(tokens) and tokens[index] == ("sym", "."):
        if _is_ident(tokens[index + 1]):
            index += 2
        else:
            break
    return index


def _is_ident(token: Tuple[str, str]) -> bool:
    return token[0] in {"word", "lit"}


def _has_as_or_like(tokens: List[Tuple[str, str]], index: int) -> bool:
    """
    Walk the remainder of the statement.

    AS at parenthesis depth 0, LIKE at depth 0, or a column-list item whose
    first word is LIKE (Postgres CREATE TABLE (LIKE ...)) pass through.
    """
    depth = 0
    item_start = False
    seen_column_list = False
    while index < len(tokens):
        kind, text = tokens[index]
        if kind == "sym" and text == "(":
            if depth == 0:
                seen_column_list = True
                item_start = True
            depth += 1
        elif kind == "sym" and text == ")":
            depth = max(0, depth - 1)
            item_start = False
        elif kind == "sym" and text == "," and depth == 1:
            item_start = True
        elif kind == "sym" and text == ";":
            return False
        elif kind == "word" and depth == 0 and text in {"as", "like"}:
            return True
        elif (
            kind == "word"
            and item_start
            and depth == 1
            and seen_column_list
            and text == "like"
        ):
            return True
        elif kind == "word" and item_start:
            item_start = False
        index += 1
    return False


def _rough_tokens(source: str, pos: int) -> List[Tuple[str, str]]:
    """A tiny scanner that skips comments and strings. Yields words and symbols."""
    tokens: List[Tuple[str, str]] = []
    i = pos
    n = len(source)
    while i < n:
        char = source[i]
        if char.isspace():
            i += 1
            continue
        if source.startswith("--", i) or source.startswith("//", i) or char == "#":
            newline = source.find("\n", i)
            i = n if newline < 0 else newline + 1
            continue
        if source.startswith("/*", i):
            end = source.find("*/", i + 2)
            i = n if end < 0 else end + 2
            continue
        if char in {"'", '"', "`"}:
            i = _skip_quoted(source, i, char)
            tokens.append(("lit", char))
            continue
        if char == "$":
            dollar_end = _skip_dollar(source, i)
            if dollar_end is not None:
                tokens.append(("lit", "$"))
                i = dollar_end
                continue
        if char == "[":
            end = source.find("]", i + 1)
            if end >= 0:
                tokens.append(("lit", "["))
                i = end + 1
                continue
        if char.isalnum() or char == "_":
            j = i + 1
            while j < n and (source[j].isalnum() or source[j] == "_"):
                j += 1
            tokens.append(("word", source[i:j].lower()))
            i = j
            continue
        tokens.append(("sym", char))
        i += 1
        if char == ";":
            break
    return tokens


def _skip_quoted(source: str, start: int, quote: str) -> int:
    i = start + 1
    n = len(source)
    while i < n:
        if source[i] == "\\":
            i += 2
            continue
        if source[i] == quote:
            if i + 1 < n and source[i + 1] == quote:
                i += 2
                continue
            return i + 1
        i += 1
    return n


def _skip_dollar(source: str, start: int) -> Optional[int]:
    match = re.match(r"\$(?:\w*)\$", source[start:])
    if not match:
        return None
    tag = match.group(0)
    end = source.find(tag, start + len(tag))
    if end < 0:
        return len(source)
    return end + len(tag)
