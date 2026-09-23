"""
Conversion between the three structural forms TOML offers for nested data:
standard ``[header]`` tables, inline tables and dotted-key assignments.

Every function mutates the given document in place and returns it.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import TYPE_CHECKING
from typing import TypeVar

from tomlkit.container import Container
from tomlkit.container import ends_with_whitespace
from tomlkit.exceptions import ConversionError
from tomlkit.exceptions import ParseError
from tomlkit.items import AbstractTable
from tomlkit.items import AoT
from tomlkit.items import Comment
from tomlkit.items import InlineTable
from tomlkit.items import Item
from tomlkit.items import Key
from tomlkit.items import Null
from tomlkit.items import SingleKey
from tomlkit.items import Table
from tomlkit.items import Trivia
from tomlkit.items import Whitespace
from tomlkit.parser import Parser


__all__ = ["to_dotted_keys", "to_inline_table", "to_standard_table", "to_super_table"]

if TYPE_CHECKING:
    _Entry = tuple[Key | None, Item]

D = TypeVar("D", bound="Container | AbstractTable")


def to_inline_table(key_path: str, doc: D) -> D:
    """Convert the standard table at ``key_path`` into an inline table.

    Nested sub-tables become nested inline tables. The header comment is kept
    as the trailing comment of the new key/value pair; comments found inside
    the table are moved in front of it.

    :Example:

    >>> doc = parse("[server]\\nhost = 'localhost'\\nport = 8080\\n")
    >>> print(dumps(to_inline_table("server", doc)), end="")
    server = {host = 'localhost', port = 8080}
    """
    loc = _resolve(doc, key_path)
    target = loc.item
    if isinstance(target, InlineTable):
        return doc

    if not isinstance(target, Table):
        raise ConversionError(key_path, "not a table")

    if loc.key.is_dotted():
        raise ConversionError(key_path, "table is defined by dotted keys")

    node = _Node(target.trivia)
    _collect(target.value, node, key_path)

    comments: list[Comment] = []
    inline = _node_to_inline(node, comments)
    inline.trivia.comment_ws, inline.trivia.comment = _header_comment(target.trivia)
    inline.trivia.trail = "\n"

    body = list(loc.container.body)
    entries, position = _detach(body, loc.index)

    entries.extend((None, c) for c in comments)
    entries.append((_leaf_key(loc.key), inline))
    _insert_plain(body, _plain_insert_index(body, position), entries)

    _set_body(loc.container, loc.owner, body)
    _make_explicit(loc)

    return doc


def to_standard_table(key_path: str, doc: D) -> D:
    """Convert the inline table at ``key_path`` into a ``[header]`` table.

    Nested inline tables become nested tables. The comment trailing the inline
    table becomes the comment of the table header.

    :Example:

    >>> doc = parse("server = {host = 'localhost', port = 8080}  # main\\n")
    >>> print(dumps(to_standard_table("server", doc)), end="")
    [server]  # main
    host = 'localhost'
    port = 8080
    """
    loc = _resolve(doc, key_path)
    target = loc.item
    if isinstance(target, Table):
        return doc

    if not isinstance(target, InlineTable):
        raise ConversionError(key_path, "not an inline table")

    for key, item in loc.ancestors:
        if not isinstance(item, Table) or key.is_dotted():
            raise ConversionError(
                key_path,
                "a header table cannot be nested in an inline table or dotted key",
            )

    node = _Node(target.trivia)
    _collect(target.value, node, key_path)
    table = _node_to_table(node, loc.key)

    body = list(loc.container.body)
    comments, position = _detach(body, loc.index)
    _insert_table(body, _table_key(loc.key), table, position, comments)

    _set_body(loc.container, loc.owner, body)

    return doc


def to_dotted_keys(key_path: str, doc: D, max_depth: int | None = None) -> D:
    """Flatten the table at ``key_path`` into dotted-key assignments placed in
    its parent container.

    ``max_depth`` limits how many levels are flattened: ``None`` flattens
    everything, ``1`` only produces keys for the immediate children, deeper
    tables being kept as inline tables. The header comment becomes a standalone
    comment in front of the first dotted key.

    :Example:

    >>> doc = parse("[server]\\nhost = 'localhost'\\nport = 8080\\n")
    >>> print(dumps(to_dotted_keys("server", doc)), end="")
    server.host = 'localhost'
    server.port = 8080
    """
    if max_depth is not None and max_depth < 1:
        raise ValueError("max_depth must be None or a positive integer")

    loc = _resolve(doc, key_path)
    target = loc.item
    if not isinstance(target, (Table, InlineTable)):
        raise ConversionError(key_path, "not a table")

    if loc.key.is_dotted():
        return doc

    in_inline = isinstance(loc.owner, InlineTable)
    node = _Node(target.trivia, inline=isinstance(target, InlineTable))
    _collect(target.value, node, key_path)

    if not node.members:
        if node.inline:
            return doc
        # An empty table has no dotted-key form, keep it as an empty inline table.
        return to_inline_table(key_path, doc)

    flattener = _Flattener(
        max_depth, target.trivia.indent if node.inline else "", in_inline
    )
    flattener.comment(_header_comment(target.trivia)[1])
    flattener.flatten(node, [loc.key], 1)
    entries = flattener.entries

    body = list(loc.container.body)
    if in_inline:
        body[loc.index : loc.index + 1] = entries
        body = _normalize_inline_body(loc.owner, body)
    elif isinstance(target, InlineTable):
        _ensure_previous_newline(body, loc.index)
        body[loc.index : loc.index + 1] = entries
    else:
        comments, position = _detach(body, loc.index)
        _insert_plain(body, _plain_insert_index(body, position), comments + entries)

    _set_body(loc.container, loc.owner, body)
    _make_explicit(loc)

    return doc


def to_super_table(dotted_prefix: str, doc: D) -> D:
    """Group the dotted-key assignments starting with ``dotted_prefix`` into a
    new ``[dotted_prefix]`` table.

    A standalone comment immediately preceding the first matching assignment
    becomes the comment of the table header.

    :Example:

    >>> doc = parse("name = 'app'\\nserver.host = 'localhost'\\nserver.port = 8080\\n")
    >>> print(dumps(to_super_table("server", doc)), end="")
    name = 'app'
    <BLANKLINE>
    [server]
    host = 'localhost'
    port = 8080
    """
    names = _parse_key_path(dotted_prefix)
    host, owner, rest = _find_dotted_host(doc, names, dotted_prefix)

    table_body: list[_Entry] = []
    body: list[_Entry] = []
    first_match: int | None = None
    for key, item in host.body:
        if isinstance(item, Null):
            continue

        if key is None or not key.is_dotted() or key.key != rest[0]:
            body.append((key, item))
            continue

        matched = []
        unmatched = []
        for keys, leaf in _chain_leaves(item, [key]):
            if [k.key for k in keys[: len(rest)]] == rest:
                matched.append((keys[len(rest) :], leaf))
            else:
                unmatched.append((keys, leaf))

        if not matched:
            body.append((key, item))
            continue

        if first_match is None:
            first_match = len(body)
        body.extend(_chain(keys, leaf) for keys, leaf in unmatched)
        for rel, leaf in matched:
            table_body.extend(_prefix_members(rel, leaf, dotted_prefix))

    if first_match is None:
        raise ConversionError(dotted_prefix, "no matching dotted keys")

    trivia = Trivia()
    comments, first_match = _take_leading_comments(body, first_match)
    _merge_whitespace(body, first_match)
    if comments:
        trivia.comment_ws = " "
        trivia.comment = comments.pop()[1].trivia.comment

    key: Key = SingleKey(rest[-1])
    new_item: Item = _make_table(table_body, trivia, key.key)
    for name in reversed(rest[:-1]):
        container = Container()
        container._raw_append(key, new_item)
        new_item = Table(container, Trivia(), False, is_super_table=True, name=name)
        key = SingleKey(name)

    _insert_table(body, key, new_item, first_match, comments)
    _set_body(host, owner, body)

    return doc


@dataclass
class _Location:
    container: Container
    owner: Container | AbstractTable
    owner_key: Key | None
    index: int
    key: Key
    item: Item
    ancestors: list[tuple[Key, Item]]


class _Node:
    """Logical content of a table: comments, values and sub-tables, with
    sub-tables split over several entries (dotted keys) merged together."""

    def __init__(self, trivia: Trivia | None = None, inline: bool = False) -> None:
        self.trivia = trivia
        self.inline = inline
        self.members: list[tuple] = []
        self._children: dict[str, _Node] = {}

    def child(self, key: Key, trivia: Trivia | None, inline: bool) -> _Node:
        if key.key not in self._children:
            node = _Node(trivia, inline)
            self._children[key.key] = node
            self.members.append(("table", key, node))

        return self._children[key.key]


def _collect(container: Container, node: _Node, key_path: str) -> None:
    for key, item in container.body:
        if key is None:
            if isinstance(item, Comment):
                node.members.append(("comment", item))
            continue

        if isinstance(item, AoT):
            raise ConversionError(key_path, "contains an array of tables")

        if isinstance(item, Table):
            trivia = None if key.is_dotted() else item.trivia
            _collect(item.value, node.child(key, trivia, node.inline), key_path)
        elif isinstance(item, InlineTable):
            _collect(item.value, node.child(key, item.trivia, True), key_path)
        else:
            node.members.append(("value", key, item))


def _node_to_inline(node: _Node, comments: list[Comment]) -> InlineTable:
    """Build an inline table from ``node``. Comments that cannot be expressed
    inside an inline table are appended to ``comments``."""
    container = Container()
    for member in node.members:
        if member[0] == "comment":
            comments.append(_comment(member[1].trivia.comment))
            continue

        if member[0] == "value":
            item = member[2]
            if item.trivia.comment:
                comments.append(_comment(item.trivia.comment))
        else:
            child = member[2]
            if child.trivia is not None and child.trivia.comment:
                comments.append(_comment(child.trivia.comment))
            item = _node_to_inline(child, comments)

        _reset_trivia(item, "" if not container.body else " ", "")
        container._raw_append(_leaf_key(member[1]), item)

    return InlineTable(container, Trivia(trail=""), new=False)


def _node_to_table(node: _Node, key: Key) -> Table:
    plain: list[_Entry] = []
    tables: list[_Entry] = []
    for member in node.members:
        if member[0] == "comment":
            plain.append((None, _comment(member[1].trivia.comment)))
        elif member[0] == "value":
            item = member[2]
            _reset_trivia(item, "", "\n")
            plain.append((_leaf_key(member[1]), item))
        else:
            tables.append((_table_key(member[1]), _node_to_table(member[2], member[1])))

    body = plain
    for table_key, table in tables:
        if body and not isinstance(body[-1][1], Whitespace):
            table.trivia.indent = "\n"
        body.append((table_key, table))

    trivia = Trivia()
    if node.trivia is not None:
        trivia.comment_ws, trivia.comment = _header_comment(node.trivia)

    return _make_table(body, trivia, key.key)


class _Flattener:
    def __init__(self, max_depth: int | None, indent: str, in_inline: bool) -> None:
        self.max_depth = max_depth
        self.indent = indent
        self.in_inline = in_inline
        self.entries: list[_Entry] = []

    def comment(self, text: str) -> None:
        if text and not self.in_inline:
            self.entries.append((None, _comment(text, self.indent)))

    def flatten(self, node: _Node, path: list[Key], depth: int) -> None:
        for member in node.members:
            if member[0] == "comment":
                self.comment(member[1].trivia.comment)
                continue

            keys = [*path, member[1]]
            if member[0] == "value":
                self.leaf(keys, member[2], node.inline)
                continue

            child = member[2]
            comment = child.trivia.comment if child.trivia is not None else ""
            if child.members and (self.max_depth is None or depth < self.max_depth):
                self.comment(comment)
                self.flatten(child, keys, depth + 1)
                continue

            comments: list[Comment] = []
            leaf = _node_to_inline(child, comments)
            for c in comments:
                self.comment(c.trivia.comment)
            if comment and not self.in_inline:
                leaf.trivia.comment_ws, leaf.trivia.comment = " ", comment
            self.leaf(keys, leaf, True)

    def leaf(self, keys: list[Key], item: Item, from_inline: bool) -> None:
        if self.in_inline:
            _reset_trivia(item, "", "")
        else:
            if from_inline:
                item.trivia.indent = self.indent
            if "\n" not in item.trivia.trail:
                item.trivia.trail = "\n"

        self.entries.append(_chain(keys, item))


def _chain(keys: list[Key], leaf: Item) -> _Entry:
    """Build the entry of the dotted-key assignment ``keys = leaf``, using the
    same representation as the parser: a chain of dotted super tables."""
    key: Key = _leaf_key(keys[-1])
    item = leaf
    for k in reversed(keys[:-1]):
        container = Container()
        container._raw_append(key, item)
        item = Table(container, Trivia(), False, is_super_table=True, name=k.key)
        key = _table_key(k)
        key._dotted = True

    return key, item


def _chain_leaves(item: Item, keys: list[Key]) -> list[tuple[list[Key], Item]]:
    if not isinstance(item, Table):
        return [(keys, item)]

    leaves = []
    for k, v in item.value.body:
        if k is not None:
            leaves.extend(_chain_leaves(v, [*keys, k]))

    return leaves


def _prefix_members(rel: list[Key], leaf: Item, dotted_prefix: str) -> list[_Entry]:
    """Entries of the new super table for a dotted key whose remaining part
    after the prefix is ``rel``."""
    if rel:
        if "\n" not in leaf.trivia.trail:
            leaf.trivia.trail = "\n"
        return [_chain(rel, leaf)]

    if not isinstance(leaf, InlineTable):
        raise ConversionError(dotted_prefix, "prefix is assigned a non-table value")

    entries = []
    for k, v in leaf.value.body:
        if k is None:
            continue
        for keys, item in _chain_leaves(v, [k]) if k.is_dotted() else [([k], v)]:
            _reset_trivia(item, "", "\n")
            entries.append(_chain(keys, item))

    return entries


def _parse_key_path(key_path: str) -> list[str]:
    if not isinstance(key_path, str) or not key_path.strip():
        raise ConversionError(key_path, "invalid key path")

    parser = Parser(key_path)
    try:
        key = parser._parse_key()
    except ParseError:
        raise ConversionError(key_path, "invalid key path") from None

    if not parser.end():
        raise ConversionError(key_path, "invalid key path")

    return [k.key for k in key]


def _root(doc: Container | AbstractTable) -> Container:
    return doc.value if isinstance(doc, AbstractTable) else doc


def _resolve(doc: Container | AbstractTable, key_path: str) -> _Location:
    names = _parse_key_path(key_path)
    owner: Container | AbstractTable = doc
    owner_key: Key | None = None
    container = _root(doc)
    ancestors: list[tuple[Key, Item]] = []

    for i, name in enumerate(names):
        idx = container._map.get(SingleKey(name))
        if idx is None:
            raise ConversionError(key_path, f'key "{name}" does not exist')

        is_last = i == len(names) - 1
        if isinstance(idx, tuple):
            if is_last:
                raise ConversionError(key_path, "table is defined in several places")

            next_key = SingleKey(names[i + 1])
            candidates = [
                j
                for j in idx
                if isinstance(container.body[j][1], (Table, InlineTable))
                and next_key in container.body[j][1].value._map
            ]
            if not candidates:
                raise ConversionError(key_path, f'key "{names[i + 1]}" does not exist')
            if len(candidates) > 1:
                raise ConversionError(key_path, "key path is ambiguous")
            idx = candidates[0]

        key, item = container.body[idx]
        if is_last:
            return _Location(container, owner, owner_key, idx, key, item, ancestors)

        if not isinstance(item, (Table, InlineTable)):
            raise ConversionError(
                key_path, f'"{".".join(names[: i + 1])}" is not a table'
            )

        ancestors.append((key, item))
        owner, owner_key, container = item, key, item.value

    raise AssertionError("unreachable")


def _find_dotted_host(
    doc: Container | AbstractTable, names: list[str], dotted_prefix: str
) -> tuple[Container, Container | AbstractTable, list[str]]:
    """Find the container holding the dotted keys for ``names``, walking
    through header tables. Returns it with its owner and the remaining keys."""
    owner: Container | AbstractTable = doc
    container = _root(doc)
    for i, name in enumerate(names):
        idx = container._map.get(SingleKey(name))
        if idx is None:
            break

        entries = [
            container.body[j] for j in (idx if isinstance(idx, tuple) else (idx,))
        ]
        rest = names[i:]
        for key, item in entries:
            if not key.is_dotted():
                continue
            for keys, _ in _chain_leaves(item, [key]):
                if (
                    len(keys) >= len(rest)
                    and [k.key for k in keys[: len(rest)]] == rest
                ):
                    return container, owner, rest

        headers = [
            item
            for key, item in entries
            if isinstance(item, Table) and not key.is_dotted()
        ]
        if len(headers) != 1:
            break

        owner = headers[0]
        container = owner.value

    raise ConversionError(dotted_prefix, "no matching dotted keys")


def _set_body(
    container: Container, owner: Container | AbstractTable, body: list[_Entry]
) -> None:
    dict.clear(container)
    container._map = {}
    container._body = []
    container._table_keys = []
    for key, item in body:
        if not isinstance(item, Null):
            container._raw_append(key, item)

    if owner is not container:
        dict.clear(owner)
        for key, item in container.body:
            if key is not None:
                dict.__setitem__(owner, key.key, item)


def _make_table(body: list[_Entry], trivia: Trivia, name: str) -> Table:
    container = Container()
    for key, item in body:
        container._raw_append(key, item)

    return Table(container, trivia, False, is_super_table=False, name=name)


def _make_explicit(loc: _Location) -> None:
    """A header super table receiving values must render its own header."""
    if (
        isinstance(loc.owner, Table)
        and loc.owner_key is not None
        and not loc.owner_key.is_dotted()
    ):
        loc.owner._is_super_table = False


def _is_header(key: Key | None, item: Item) -> bool:
    return isinstance(item, (Table, AoT)) and key is not None and not key.is_dotted()


def _first_header(body: list[_Entry]) -> int:
    return next((i for i, e in enumerate(body) if _is_header(*e)), len(body))


def _plain_insert_index(body: list[_Entry], position: int) -> int:
    """Where to put key/value pairs that replace the entry at ``position``."""
    first_header = _first_header(body)
    if position <= first_header:
        return position

    last = _last_pair(body, first_header)
    return first_header if last is None else last + 1


def _last_pair(body: list[_Entry], end: int) -> int | None:
    return max((i for i in range(end) if body[i][0] is not None), default=None)


def _insert_plain(body: list[_Entry], idx: int, entries: list[_Entry]) -> None:
    _ensure_previous_newline(body, idx)
    body[idx:idx] = entries
    end = idx + len(entries)
    if end < len(body) and _is_header(*body[end]) and entries:
        after = body[end][1]
        indent = after.trivia.indent if isinstance(after, Table) else ""
        if isinstance(after, AoT) and after.body:
            indent = after.body[0].trivia.indent
        if "\n" not in indent:
            body.insert(end, (None, Whitespace("\n")))


def _insert_table(
    body: list[_Entry],
    key: Key,
    table: Table,
    fallback: int,
    leading: list[_Entry] | None = None,
) -> None:
    """Insert ``table``, preceded by the ``leading`` comments, after the
    key/value pairs of ``body``."""
    leading = list(leading or [])
    first_header = _first_header(body)
    last = _last_pair(body, first_header)
    if last is None:
        idx = min(fallback, first_header)
    else:
        idx = last + 1
        while idx < first_header and isinstance(body[idx][1], (Whitespace, Null)):
            idx += 1

    previous = _previous(body, idx)[1]
    if (
        previous is not None
        and not isinstance(previous, Whitespace)
        and not ends_with_whitespace(previous)
        and "\n" not in table.trivia.indent
    ):
        _ensure_previous_newline(body, idx)
        if leading:
            leading.insert(0, (None, Whitespace("\n")))
        else:
            table.trivia.indent = "\n" + table.trivia.indent

    following = next((v for _, v in body[idx:] if not isinstance(v, Null)), None)
    if following is not None and not isinstance(following, Whitespace):
        _tail(table)._raw_append(None, Whitespace("\n"))

    body[idx:idx] = [*leading, (key, table)]


def _detach(body: list[_Entry], idx: int) -> tuple[list[_Entry], int]:
    """Remove ``body[idx]`` along with the comment lines directly above it.
    Returns those comments and the position the entry was removed from."""
    key, removed = body.pop(idx)
    comments, idx = _take_leading_comments(body, idx)

    if _is_header(key, removed):
        previous_key, previous = _previous(body, idx)
        if not comments and previous is not None and _is_header(previous_key, previous):
            # Lines above a header are stored at the end of the preceding table.
            tail = _tail(previous)
            while tail.body and isinstance(tail.body[-1][1], (Comment, Null)):
                item = tail.body.pop()[1]
                if isinstance(item, Comment):
                    comments.insert(0, (None, item))

        _inherit_trailing_whitespace(body, idx, removed)
    else:
        _merge_whitespace(body, idx)

    return comments, idx


def _merge_whitespace(body: list[_Entry], idx: int) -> None:
    """Avoid doubling blank lines when the entry between two of them left."""
    if (
        0 < idx < len(body)
        and isinstance(body[idx - 1][1], Whitespace)
        and isinstance(body[idx][1], Whitespace)
    ):
        del body[idx]


def _take_leading_comments(body: list[_Entry], idx: int) -> tuple[list[_Entry], int]:
    """Remove the comment lines directly above position ``idx``."""
    start = idx
    while start > 0 and isinstance(body[start - 1][1], (Comment, Null)):
        start -= 1

    comments = [e for e in body[start:idx] if isinstance(e[1], Comment)]
    del body[start:idx]

    return comments, start


def _inherit_trailing_whitespace(body: list[_Entry], idx: int, removed: Table) -> None:
    """Blank lines following a table are stored inside it. Keep the ones that
    followed the removed table, replacing those stored in the preceding table
    which used to separate it from the removed one."""
    trailing = []
    for _, item in reversed(_tail(removed).body):
        if not isinstance(item, (Whitespace, Null)):
            break
        trailing.insert(0, item)

    key, previous = _previous(body, idx)
    if previous is None or not _is_header(key, previous):
        body[idx:idx] = [(None, item) for item in trailing]
        return

    tail = _tail(previous)
    while tail.body and isinstance(tail.body[-1][1], (Whitespace, Null)):
        tail.body.pop()

    for item in trailing:
        tail._raw_append(None, item)


def _tail(item: Table | AoT) -> Container:
    """The container in which the last line rendered by ``item`` lives."""
    while True:
        if isinstance(item, AoT):
            if not item.body:
                return Container()
            item = item.body[-1]

        last = next(
            (e for e in reversed(item.value.body) if not isinstance(e[1], Null)), None
        )
        if last is None or not _is_header(*last):
            return item.value

        item = last[1]


def _previous(body: list[_Entry], idx: int) -> tuple[Key | None, Item | None]:
    return next(
        (e for e in reversed(body[:idx]) if not isinstance(e[1], Null)), (None, None)
    )


def _ensure_previous_newline(body: list[_Entry], idx: int) -> None:
    previous = _previous(body, idx)[1]
    if previous is None or isinstance(previous, (Whitespace, Table, AoT)):
        return

    if "\n" not in previous.trivia.trail:
        previous.trivia.trail += "\n"


def _normalize_inline_body(owner: InlineTable, body: list[_Entry]) -> list[_Entry]:
    entries = [(k, v) for k, v in body if k is not None]
    for i, (_, item) in enumerate(entries):
        _reset_trivia(item, "" if i == 0 or owner._new else " ", "")

    return entries


def _reset_trivia(item: Item, indent: str, trail: str) -> None:
    item.trivia.indent = indent
    item.trivia.comment_ws = ""
    item.trivia.comment = ""
    item.trivia.trail = trail


def _header_comment(trivia: Trivia) -> tuple[str, str]:
    if not trivia.comment:
        return "", ""

    return trivia.comment_ws or " ", trivia.comment


def _comment(text: str, indent: str = "") -> Comment:
    return Comment(Trivia(indent=indent, comment_ws="", comment=text, trail="\n"))


def _normalized_key(key: Key, sep: str) -> SingleKey:
    if not isinstance(key, SingleKey):
        return SingleKey(key.key, sep=sep)

    return SingleKey(key.key, t=key.t, sep=sep, original=key.as_string().strip())


def _leaf_key(key: Key) -> SingleKey:
    return _normalized_key(key, " = ")


def _table_key(key: Key) -> SingleKey:
    return _normalized_key(key, "")
