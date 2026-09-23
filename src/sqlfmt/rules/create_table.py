from functools import partial

from sqlfmt import actions
from sqlfmt.rule import Rule
from sqlfmt.rules.common import group
from sqlfmt.rules.core import CORE
from sqlfmt.tokens import TokenType

CREATE_TABLE = [
    *CORE,
    Rule(
        name="word_operator",
        priority=1100,
        pattern=group(
            # constraints
            r"not\s+null",
            r"null",
            r"default",
            r"references",
            r"constraint",
            r"check",
            r"primary\s+key",
            r"foreign\s+key",
            r"unique(\s+key)?",
            r"on\s+(delete|update)",
            r"not\s+enforced",
            # generated columns
            r"as",
            # expressions in checks and defaults
            r"(not\s+)?between",
            r"(not\s+)?in",
            r"is(\s+not)?",
            r"(not\s+)?i?like",
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
        name="boolean_operator",
        priority=1200,
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
    Rule(
        name="post_body_keyword",
        priority=1300,
        pattern=group(
            r"partition\s+by",
            r"cluster\s+by",
            r"options",
        )
        + group(r"\W", r"$"),
        action=partial(
            actions.handle_reserved_keyword,
            action=actions.handle_ddl_post_body_keyword,
        ),
    ),
]
