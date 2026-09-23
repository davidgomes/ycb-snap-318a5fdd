import re
from functools import partial
from typing import TYPE_CHECKING

from sqlfmt import actions
from sqlfmt.rule import Rule
from sqlfmt.rules.common import group
from sqlfmt.rules.core import CORE
from sqlfmt.tokens import TokenType

if TYPE_CHECKING:
    from sqlfmt.analyzer import Analyzer


def _add_keyword(
    analyzer: "Analyzer",
    source_string: str,
    match: re.Match,
    token_type: TokenType,
) -> None:
    """Lex a DDL keyword and normalize its printed value to lowercase."""
    actions.add_node_to_buffer(analyzer, source_string, match, token_type=token_type)
    node = analyzer.node_buffer[-1]
    node.value = " ".join(node.token.token.lower().split())


def _add_create_table_keyword(
    analyzer: "Analyzer",
    source_string: str,
    match: re.Match,
) -> None:
    _add_keyword(analyzer, source_string, match, TokenType.NAME)


def _add_word_operator(
    analyzer: "Analyzer",
    source_string: str,
    match: re.Match,
) -> None:
    actions.handle_reserved_keyword(
        analyzer,
        source_string,
        match,
        action=partial(_add_keyword, token_type=TokenType.WORD_OPERATOR),
    )


def _add_boolean_operator(
    analyzer: "Analyzer",
    source_string: str,
    match: re.Match,
) -> None:
    actions.handle_reserved_keyword(
        analyzer,
        source_string,
        match,
        action=partial(_add_keyword, token_type=TokenType.BOOLEAN_OPERATOR),
    )


CREATE_TABLE = [
    *CORE,
    Rule(
        name="create_table_keyword",
        priority=1000,
        pattern=group(r"create\s+table(?:\s+if\s+not\s+exists)?") + group(r"\W", r"$"),
        action=partial(
            actions.handle_reserved_keyword,
            action=_add_create_table_keyword,
        ),
    ),
    Rule(
        name="ddl_not_null",
        priority=1010,
        pattern=group(r"not\s+null") + group(r"\W", r"$"),
        action=_add_word_operator,
    ),
    Rule(
        name="ddl_primary_key",
        priority=1020,
        pattern=group(r"primary\s+key") + group(r"\W", r"$"),
        action=_add_word_operator,
    ),
    Rule(
        name="ddl_foreign_key",
        priority=1030,
        pattern=group(r"foreign\s+key") + group(r"\W", r"$"),
        action=_add_word_operator,
    ),
    Rule(
        name="ddl_partition_by",
        priority=1040,
        pattern=group(r"partition\s+by") + group(r"\W", r"$"),
        action=_add_word_operator,
    ),
    Rule(
        name="ddl_cluster_by",
        priority=1050,
        pattern=group(r"cluster\s+by") + group(r"\W", r"$"),
        action=_add_word_operator,
    ),
    Rule(
        name="ddl_word_operator",
        priority=1100,
        pattern=group(
            r"check",
            r"unique",
            r"default",
            r"references",
            r"constraint",
            r"options",
            r"null",
            r"(not\s+)?in",
            r"is(\s+not)?(\s+distinct\s+from)?",
            r"(not\s+)?i?like",
            r"(not\s+)?rlike",
            r"(not\s+)?regexp",
            r"(not\s+)?between",
            r"(not\s+)?similar\s+to",
            r"exists",
        )
        + group(r"\W", r"$"),
        action=_add_word_operator,
    ),
    Rule(
        name="ddl_boolean_operator",
        priority=1200,
        pattern=group(
            r"and",
            r"or",
            r"not",
        )
        + group(r"\W", r"$"),
        action=_add_boolean_operator,
    ),
]
