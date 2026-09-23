from functools import partial

from sqlfmt import actions
from sqlfmt.rule import Rule
from sqlfmt.rules.common import CREATE_TABLE, SQL_COMMENT, group
from sqlfmt.rules.core import CORE
from sqlfmt.tokens import TokenType

TABLE = [
    *CORE,
    Rule(
        # a trailing comma after the last item in the column list is dropped
        # (it is only valid in some dialects, and it is never required)
        name="trailing_comma",
        priority=439,  # comma is 440
        pattern=group(r",") + r"(?=(\s|" + SQL_COMMENT + r")*\))",
        action=actions.handle_ddl_trailing_comma,
    ),
    Rule(
        name="create_table",
        priority=900,
        pattern=group(CREATE_TABLE) + group(r"\W", r"$"),
        action=partial(
            actions.handle_reserved_keyword,
            action=partial(
                actions.add_node_to_buffer, token_type=TokenType.WORD_OPERATOR
            ),
        ),
    ),
    Rule(
        name="constraint_keyword",
        priority=1000,
        pattern=group(
            r"not\s+null",
            r"null",
            r"default",
            r"constraint",
            r"check",
            r"unique",
            r"primary\s+key",
            r"foreign\s+key",
            r"references",
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
            r"is(\s+not)?(\s+distinct\s+from)?",
            r"(not\s+)?i?like",
            r"(not\s+)?similar\s+to",
            r"(not\s+)?r?like",
            r"(not\s+)?regexp",
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
        # clauses that follow the column list. Inside the column list, these
        # words are lexed as names (e.g., bq column OPTIONS(...))
        name="post_body_keyword",
        priority=1300,
        pattern=group(
            r"partition\s+by",
            r"cluster\s+by",
            r"clustered\s+by",
            r"sorted\s+by",
            r"distributed\s+by",
            r"order\s+by",
            r"options",
            r"with",
            r"inherits",
            r"tablespace",
            r"tblproperties",
            r"location",
            r"using",
            r"comment",
            r"engine",
            r"settings",
        )
        + group(r"\W", r"$"),
        action=partial(
            actions.handle_reserved_keyword,
            action=partial(
                actions.handle_keyword_outside_brackets,
                token_type=TokenType.UNTERM_KEYWORD,
            ),
        ),
    ),
]
