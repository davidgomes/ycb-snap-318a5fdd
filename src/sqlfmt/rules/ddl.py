from functools import partial
from typing import TYPE_CHECKING

from sqlfmt import actions
from sqlfmt.ddl import create_table_passes_through
from sqlfmt.rule import Rule
from sqlfmt.rules.common import group
from sqlfmt.rules.core import CORE
from sqlfmt.tokens import TokenType

if TYPE_CHECKING:
    import re

    from sqlfmt.analyzer import Analyzer


def handle_create_table(
    analyzer: "Analyzer",
    source_string: str,
    match: "re.Match[str]",
) -> None:
    """Lex CREATE TABLE as DDL, or as unsupported text for AS SELECT and LIKE."""
    from sqlfmt.rules.unsupported import UNSUPPORTED

    if create_table_passes_through(source_string, match.end(1)):
        actions.lex_ruleset(analyzer, source_string, match, new_ruleset=UNSUPPORTED)
    else:
        actions.lex_ruleset(analyzer, source_string, match, new_ruleset=DDL)


DDL = [
    *CORE,
    Rule(
        name="ddl_keyword",
        priority=1100,
        pattern=group(
            r"create\s+table(\s+if\s+not\s+exists)?",
            r"partition\s+by",
            r"cluster\s+by",
            r"primary\s+key",
            r"foreign\s+key",
            r"not\s+null",
            r"set\s+null",
            r"set\s+default",
            r"on\s+delete",
            r"on\s+update",
            r"references",
            r"default",
            r"constraint",
            r"check",
            r"unique",
            r"cascade",
            r"restrict",
            r"null",
        )
        + group(r"\W", r"$"),
        action=partial(
            actions.handle_reserved_keyword,
            action=partial(
                actions.add_node_to_buffer, token_type=TokenType.WORD_OPERATOR
            ),
        ),
    ),
    Rule(
        name="ddl_word_operator",
        priority=1200,
        pattern=group(
            r"as",
            r"(not\s+)?between",
            r"(not\s+)?exists",
            r"(not\s+)?i?like",
            r"(not\s+)?rlike",
            r"(global\s+)?(not\s+)?in",
            r"is(\s+not)?",
            r"similar\s+to",
        )
        + group(r"\W", r"$"),
        action=partial(
            actions.handle_reserved_keyword,
            action=partial(
                actions.add_node_to_buffer, token_type=TokenType.WORD_OPERATOR
            ),
        ),
    ),
    Rule(
        name="ddl_on",
        priority=1220,
        pattern=group(r"on") + group(r"\W", r"$"),
        action=partial(
            actions.handle_reserved_keyword,
            action=partial(actions.add_node_to_buffer, token_type=TokenType.ON),
        ),
    ),
    Rule(
        name="ddl_boolean_operator",
        priority=1300,
        pattern=group(
            r"and",
            r"or",
            r"not",
        )
        + group(r"\W", r"$"),
        action=partial(
            actions.handle_reserved_keyword,
            action=partial(
                actions.add_node_to_buffer, token_type=TokenType.BOOLEAN_OPERATOR
            ),
        ),
    ),
]
