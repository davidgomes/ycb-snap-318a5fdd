from functools import partial

from sqlfmt import actions
from sqlfmt.rule import Rule
from sqlfmt.rules.common import CREATE_TABLE, group
from sqlfmt.rules.core import CORE
from sqlfmt.tokens import TokenType

# Covers CREATE TABLE statements with column definitions, e.g.:
# CREATE TABLE IF NOT EXISTS foo (
#     id INT64 NOT NULL,
#     tags ARRAY<STRING>,
#     CONSTRAINT pk PRIMARY KEY (id) NOT ENFORCED
# )
# PARTITION BY DATE(_PARTITIONTIME)
# CLUSTER BY id
# OPTIONS (description = 'bar');
TABLE = [
    *CORE,
    Rule(
        name="table_body_open",
        priority=499,  # bracket_open is 500
        pattern=group(r"\("),
        action=actions.handle_create_table_paren,
    ),
    Rule(
        name="constraint_keyword",
        priority=1000,
        pattern=group(
            r"constraint",
            r"primary\s+key",
            r"foreign\s+key",
            r"unique",
            r"check",
            r"references",
            r"not\s+null",
            r"null",
            r"default",
            r"on\s+(delete|update)",
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
        name="word_operator",
        priority=1100,
        pattern=group(
            r"as",
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
        name="unterm_keyword",
        priority=1300,
        pattern=group(CREATE_TABLE) + group(r"\W", r"$"),
        action=partial(
            actions.handle_reserved_keyword,
            action=partial(
                actions.add_node_to_buffer, token_type=TokenType.UNTERM_KEYWORD
            ),
        ),
    ),
    Rule(
        # clauses that follow the column definitions. Inside brackets,
        # these are names (e.g., a column named options)
        name="table_clause",
        priority=1310,
        pattern=group(
            r"partition\s+by",
            r"cluster\s+by",
            r"options",
        )
        + group(r"\W", r"$"),
        action=partial(
            actions.handle_keyword_outside_brackets,
            action=partial(
                actions.add_node_to_buffer, token_type=TokenType.UNTERM_KEYWORD
            ),
        ),
    ),
]
