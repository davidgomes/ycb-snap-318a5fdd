from dataclasses import dataclass
from typing import List, Optional

from sqlfmt.ddl import DdlFormatter, is_create_table_node
from sqlfmt.jinjafmt import JinjaFormatter
from sqlfmt.line import Line
from sqlfmt.merger import LineMerger
from sqlfmt.mode import Mode
from sqlfmt.node import Node
from sqlfmt.node_manager import NodeManager
from sqlfmt.query import Query
from sqlfmt.splitter import LineSplitter
from sqlfmt.tokens import TokenType


@dataclass
class QueryFormatter:
    mode: Mode

    def _split_lines(self, lines: List[Line]) -> List[Line]:
        """
        Splits lines to make line depth consistent and syntax
        apparent
        """
        node_manager = NodeManager(self.mode.dialect.case_sensitive_names)
        splitter = LineSplitter(node_manager)
        new_lines = []
        for line in lines:
            splits = list(splitter.maybe_split(line))
            new_lines.extend(splits)
        return new_lines

    def _format_jinja(self, lines: List[Line]) -> List[Line]:
        """
        Formats the contents of jinja tags (the code between
        the curlies) by mutating existing jinja nodes
        """
        formatter = JinjaFormatter(mode=self.mode)
        new_lines: List[Line] = []
        for line in lines:
            new_lines.extend(formatter.format_line(line))
        return new_lines

    def _merge_lines(self, lines: List[Line]) -> List[Line]:
        """
        Merge lines to minimize vertical space used by the
        query, while maintaining the syntax hierarchy achieved
        by the splitter
        """
        merger = LineMerger(mode=self.mode)
        lines = merger.maybe_merge_lines(lines)
        return lines

    def _merge_lines_and_format_ddl(self, lines: List[Line]) -> List[Line]:
        """
        CREATE TABLE statements have a fixed layout (one column per line),
        so they are formatted by the DdlFormatter instead of being merged.
        All other lines are merged.
        """
        node_manager = NodeManager(self.mode.dialect.case_sensitive_names)
        ddl_formatter = DdlFormatter(mode=self.mode, node_manager=node_manager)
        new_lines: List[Line] = []
        head = 0
        i = 0
        while i < len(lines):
            if not (lines[i].nodes and is_create_table_node(lines[i].nodes[0])):
                i += 1
                continue
            end = i
            while end < len(lines) - 1 and not any(
                node.token.type is TokenType.SEMICOLON for node in lines[end].nodes
            ):
                end += 1
            ddl_lines = ddl_formatter.format_lines(lines[i : end + 1])
            if ddl_lines is not None:
                new_lines.extend(self._merge_lines(lines[head:i]))
                new_lines.extend(ddl_lines)
                head = end + 1
            i = end + 1
        new_lines.extend(self._merge_lines(lines[head:]))
        return new_lines

    def _dedent_jinja_blocks(self, lines: List[Line]) -> List[Line]:
        """
        Jinja block tags, like {% if foo %} and {% endif %}, shouldn't
        be printed at their depth, since their contents may be dedented
        farther. This dedents the tags as necessary, in a single pass
        """
        start_node: Optional[Node] = None
        for line in lines:
            if (
                line.is_standalone_jinja_statement
                and line.nodes[0].is_closing_jinja_block
                and not line.formatting_disabled
            ):
                assert start_node
                line.nodes[0].open_brackets = start_node.open_brackets

            if line.nodes and line.nodes[-1].open_jinja_blocks:
                start_node = line.nodes[-1].open_jinja_blocks[-1]
                if (
                    len(line.open_brackets) < len(start_node.open_brackets)
                    and not start_node.formatting_disabled
                ):
                    start_node.open_brackets = line.open_brackets

        return lines

    def _remove_extra_blank_lines(self, lines: List[Line]) -> List[Line]:
        """
        A query can have at most 2 consecutive blank lines at depth (0,0)
        and 1 consecutive blank line at any other depth. See issue #249
        for motivation and details.
        """
        new_lines: List[Line] = []
        # initialize cnt high so we remove any extra lines at the beginning
        # of files.
        cnt = 2
        for line in lines:
            if line.is_blank_line:
                max_cnt = 2 if line.depth == (0, 0) else 1
                if cnt < max_cnt or line.formatting_disabled:
                    new_lines.append(line)
                cnt += 1
            else:
                new_lines.append(line)
                cnt = 0
        return new_lines

    def format(self, raw_query: Query) -> Query:
        """
        Applies 4 transformations to a Query:
        1. Splits lines
        2. Formats jinja tags
        3. Dedents jinja block tags to match their least-indented contents
        4. Merges lines (and formats CREATE TABLE statements)
        5. Removes extra blank lines
        """
        lines = raw_query.lines

        pipeline = [
            self._split_lines,
            self._format_jinja,
            self._dedent_jinja_blocks,
            self._merge_lines_and_format_ddl,
            self._remove_extra_blank_lines,
        ]

        for transform in pipeline:
            lines = transform(lines)

        formatted_query = Query(
            source_string=raw_query.source_string,
            line_length=raw_query.line_length,
            lines=lines,
        )

        return formatted_query
