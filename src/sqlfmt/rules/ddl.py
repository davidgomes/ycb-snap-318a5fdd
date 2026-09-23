from functools import partial

from sqlfmt import actions
from sqlfmt.rule import Rule
from sqlfmt.rules.core import CORE
from sqlfmt.tokens import TokenType

DDL = [
    *CORE,
    Rule(
        # Stay at depth 0. An unterminated keyword would indent the argument
        # list and swallow the following post-body clauses.
        name="post_body",
        priority=1000,
        pattern=actions.group(
            r"partition\s+by",
            r"cluster\s+by",
            r"options",
        )
        + actions.group(r"\W", r"$"),
        action=partial(
            actions.handle_reserved_keyword,
            action=partial(
                actions.add_node_to_buffer, token_type=TokenType.WORD_OPERATOR
            ),
        ),
    ),
    Rule(
        name="word_operator",
        priority=1100,
        pattern=actions.group(
            r"primary",
            r"foreign",
            r"key",
            r"unique",
            r"check",
            r"constraint",
            r"references",
            r"default",
            r"null",
            r"not",
        )
        + actions.group(r"\W", r"$"),
        action=partial(
            actions.handle_reserved_keyword,
            action=partial(
                actions.add_node_to_buffer, token_type=TokenType.WORD_OPERATOR
            ),
        ),
    ),
]
