"""
Conversion between the structural forms of a TOML table.

The same nested data can be written as a standard table with a ``[header]``,
as an inline table (``key = {...}``) or as a group of dotted-key assignments
(``key.child = ...``). The functions in this module rewrite a document in
place from one form to another. Values are kept intact and comments are
carried along wherever the target form can hold them.
"""

from __future__ import annotations

from typing import TypeVar

from tomlkit.container import Container
from tomlkit.container import ends_with_whitespace
from tomlkit.exceptions import ConversionError
from tomlkit.exceptions import ParseError
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

_D = TypeVar("_D", bound=Container)


def to_inline_table(key_path: str, doc: _D) -> _D:
    """Convert the standard table at ``key_path`` into an inline table.

    Nested tables are converted into nested inline tables. The table header's
    comment is kept as the comment of the new ``key = {...}`` line; comments
    inside the table cannot be represented in an inline table and are dropped.
    Nothing happens if the value is already an inline table.

    :Example:

    >>> doc = parse('[server]\\nhost = "localhost"\\nport = 8080\\n')
    >>> print(to_inline_table("server", doc).as_string())
    server = {host = "localhost", port = 8080}
    """
    hits = _resolve(doc, key_path)
    form = _form(hits)
    if form is InlineTable:
        return doc
    if form is not Table:
        raise ConversionError(
            key_path, "Only a table can be converted to an inline table"
        )

    fragments = [hit.item for hit in hits]
    if _has_aot(fragments):
        raise ConversionError(
            key_path, "An array of tables cannot be placed in an inline table"
        )

    first = hits[0]
    header = _header_trivia(fragments)
    inline = _to_inline(fragments)
    inline.trivia.indent = _line_indent(header.indent) if header else ""
    if header and header.comment:
        inline.trivia.comment_ws = header.comment_ws
        inline.trivia.comment = header.comment
    inline.trivia.trail = "\n"

    entry = _line(first.keys[:-1], _key(first.keys[-1]), inline)
    _detach_all(hits, first.anchor.container)
    _place_values(first.anchor, [entry])

    return doc


def to_standard_table(key_path: str, doc: _D) -> _D:
    """Convert the inline table at ``key_path`` into a standard ``[header]`` table.

    Nested inline tables become nested standard tables. The comment of the
    ``key = {...}`` line becomes the comment of the table header. Nothing
    happens if the value is already a table.

    :Example:

    >>> doc = parse('server = {host = "localhost", port = 8080}  # main\\n')
    >>> print(to_standard_table("server", doc).as_string())
    [server]  # main
    host = "localhost"
    port = 8080
    """
    hits = _resolve(doc, key_path)
    form = _form(hits)
    if form is Table:
        return doc
    if form is not InlineTable:
        raise ConversionError(
            key_path, "Only an inline table can be converted to a standard table"
        )

    hit = hits[0]
    if isinstance(hit.anchor.owner, InlineTable):
        raise ConversionError(
            key_path, "A standard table cannot be placed in an inline table"
        )

    inline = hit.item
    trivia = Trivia(indent=_line_indent(inline.trivia.indent))
    if inline.trivia.comment:
        trivia.comment_ws = inline.trivia.comment_ws
        trivia.comment = inline.trivia.comment

    table = _to_table(inline, hit.keys[-1].key, trivia)
    key, node = _nest(hit.keys[:-1], hit.keys[-1], table)
    _detach(hit.trail, hit.top)
    _trim_plain_region(hit.anchor.container)
    _place_table(hit.anchor, key, node, table)

    return doc


def to_dotted_keys(key_path: str, doc: _D, max_depth: int | None = None) -> _D:
    """Flatten the table or inline table at ``key_path`` into dotted keys.

    The assignments are written into the container that holds the table.
    ``max_depth`` limits how many levels of nesting are flattened: ``None``
    flattens everything and ``1`` only turns the immediate children into
    dotted keys, deeper tables are kept as inline table values. The table
    header's comment becomes a standalone comment before the first dotted key.

    :Example:

    >>> doc = parse('[server]\\nhost = "localhost"\\n\\n[server.tls]\\ncert = "a"\\n')
    >>> print(to_dotted_keys("server", doc).as_string())
    server.host = "localhost"
    <BLANKLINE>
    server.tls.cert = "a"
    """
    if max_depth is not None and (not isinstance(max_depth, int) or max_depth < 1):
        raise ConversionError(key_path, "max_depth must be a positive integer or None")

    hits = _resolve(doc, key_path)
    if _form(hits) is None:
        raise ConversionError(
            key_path, "Only a table or an inline table can be flattened"
        )

    fragments = [hit.item for hit in hits]
    if _has_aot(fragments):
        raise ConversionError(
            key_path, "An array of tables cannot be written with dotted keys"
        )

    first = hits[0]
    header = _header_trivia(fragments)
    indent = _line_indent(header.indent) if header else ""
    entries: list[tuple[Key | None, Item]] = []
    if header and header.comment:
        entries.append((None, _comment(header, indent)))

    if _has_keys(fragments):
        _flatten(fragments, first.keys, 1, max_depth, indent, entries)
    else:
        empty = InlineTable(Container(), Trivia(indent=indent), new=True)
        entries.append(_line(first.keys[:-1], _key(first.keys[-1]), empty))

    while isinstance(entries[-1][1], Whitespace):
        entries.pop()

    _detach_all(hits, first.anchor.container)
    _place_values(first.anchor, entries)

    return doc


def to_super_table(dotted_prefix: str, doc: _D) -> _D:
    """Group the dotted keys starting with ``dotted_prefix`` into a ``[prefix]`` table.

    A standalone comment immediately preceding the first matching assignment
    becomes the comment of the table header. Comments and blank lines placed
    between the matching assignments are moved into the table with them.

    :Example:

    >>> doc = parse('server.host = "localhost"\\nserver.port = 8080\\n')
    >>> print(to_super_table("server", doc).as_string())
    [server]
    host = "localhost"
    port = 8080
    """
    names = _parse_path(dotted_prefix)
    matches: list[_Match] = []
    existing: list[Table] = []
    _collect_dotted(_check(doc), None, names, 0, dotted_prefix, matches, existing)
    if not matches:
        raise ConversionError(dotted_prefix, "No dotted keys start with this prefix")

    first = matches[0]
    anchor = first.trail[0]
    container, owner = anchor.container, anchor.owner
    body = container.body
    local = [m for m in matches if m.trail[0].container is container]
    tops = {m.trail[0].index for m in local}

    moved: list[int] = []
    run: list[int] = []
    after_match = True
    for index in range(min(tops) + 1, max(tops)):
        key, item = body[index]
        if isinstance(item, Null):
            continue
        if index in tops:
            if after_match:
                moved.extend(run)
            run = []
            after_match = True
        elif key is None:
            run.append(index)
        else:
            run = []
            after_match = False
    if after_match:
        moved.extend(run)

    comment: tuple[int, Comment] | None = None
    for index in range(min(tops) - 1, -1, -1):
        item = body[index][1]
        if isinstance(item, Null):
            continue
        if isinstance(item, Comment):
            comment = (index, item)
        break

    entries: list[tuple[Key | None, Item]] = []
    for index in sorted([*tops, *moved]):
        if index in tops:
            for match in local:
                if match.trail[0].index == index:
                    entries.extend(_chain_entries(match.node))
        else:
            entries.append(body[index])
    for match in matches:
        if match not in local:
            entries.extend(_chain_entries(match.node))

    for key, item in entries:
        if key is None:
            _ensure_newline(item)
        else:
            _set_line_trivia(item, None)

    for match in matches:
        _detach(match.trail)
    for index in moved:
        _remove(container, index)
    if comment is not None:
        _remove(container, comment[0])
    _trim_plain_region(container)
    _refresh(container, owner)

    if existing:
        table = existing[0]
        _place_values(_Slot(table.value, table, len(table.value.body)), entries)
        if comment is not None and not table.trivia.comment:
            _set_comment(table.trivia, comment[1])
    else:
        trivia = Trivia()
        if comment is not None:
            _set_comment(trivia, comment[1])
        table = _table(entries, trivia, name=names[-1].key)
        key, node = _nest(first.keys[:-1], first.keys[-1], table)
        _place_table(anchor, key, node, table)

    return doc


class _Slot:
    """A body entry of a container, addressed by its position."""

    __slots__ = ("container", "index", "owner")

    def __init__(
        self, container: Container, owner: Table | InlineTable | None, index: int
    ) -> None:
        self.container = container
        self.owner = owner
        self.index = index

    @property
    def key(self) -> Key:
        key = self.container.body[self.index][0]
        assert key is not None
        return key

    @property
    def item(self) -> Item:
        return self.container.body[self.index][1]


class _Hit:
    """One occurrence of a requested key, reached through ``trail``.

    Dotted-key assignments are stored as chains of implicit tables, so a key
    defined by dotted keys occurs once per assignment. ``top`` is the position
    in ``trail`` of the entry sitting directly in the enclosing container, the
    anchor. It precedes the last position when the occurrence is part of a
    dotted-key chain, and ``keys`` holds the key path relative to the anchor.
    """

    def __init__(self, trail: list[_Slot]) -> None:
        top = len(trail) - 1
        while top > 0 and trail[top - 1].key.is_dotted():
            top -= 1

        self.trail = trail
        self.top = top
        self.item = trail[-1].item
        self.keys = [slot.key for slot in trail[top:]]

    @property
    def anchor(self) -> _Slot:
        return self.trail[self.top]


class _Match:
    """A dotted-key chain whose implicit table ``node`` sits at a prefix."""

    def __init__(self, trail: list[_Slot], node: Table) -> None:
        self.trail = trail
        self.node = node
        self.keys = [slot.key for slot in trail]


class _Entry:
    """A logical entry of a table assembled from one or more fragments."""

    __slots__ = ("inline", "item", "key", "tables")

    def __init__(
        self,
        key: Key | None,
        item: Item | None = None,
        tables: list[Table | InlineTable] | None = None,
        inline: bool = False,
    ) -> None:
        self.key = key
        self.item = item
        self.tables = tables
        self.inline = inline


def _check(doc: Container) -> Container:
    if not isinstance(doc, Container):
        raise TypeError(f"Expected a TOML document, got {type(doc).__name__}")

    return doc


def _parse_path(key_path: str) -> list[SingleKey]:
    if not isinstance(key_path, str):
        raise TypeError(f"Key path must be a string, got {type(key_path).__name__}")

    parser = Parser(key_path)
    try:
        key = parser._parse_key()
    except ParseError:
        raise ConversionError(key_path, "Invalid key path") from None

    if not parser.end():
        raise ConversionError(key_path, "Invalid key path")

    return list(key)


def _resolve(doc: Container, key_path: str) -> list[_Hit]:
    names = [key.key for key in _parse_path(key_path)]
    hits: list[_Hit] = []

    def walk(
        container: Container,
        owner: Table | InlineTable | None,
        depth: int,
        trail: list[_Slot],
    ) -> None:
        for index, (key, item) in enumerate(container.body):
            if not isinstance(key, SingleKey) or key.key != names[depth]:
                continue

            path = [*trail, _Slot(container, owner, index)]
            if depth == len(names) - 1:
                hits.append(_Hit(path))
            elif isinstance(item, (Table, InlineTable)):
                walk(item.value, item, depth + 1, path)
            else:
                name = ".".join(names[: depth + 1])
                raise ConversionError(key_path, f'"{name}" is not a table')

    walk(_check(doc), None, 0, [])
    if not hits:
        raise ConversionError(key_path, "Key does not exist")

    return hits


def _form(hits: list[_Hit]) -> type[Table | InlineTable] | None:
    items = [hit.item for hit in hits]
    if len(items) == 1 and isinstance(items[0], InlineTable):
        return InlineTable
    if all(isinstance(item, Table) for item in items):
        return Table

    return None


def _collect_dotted(
    container: Container,
    owner: Table | None,
    names: list[SingleKey],
    depth: int,
    dotted_prefix: str,
    matches: list[_Match],
    existing: list[Table],
) -> None:
    last = len(names) - 1
    for index, (key, item) in enumerate(container.body):
        if not isinstance(key, SingleKey) or key != names[depth]:
            continue

        slot = _Slot(container, owner, index)
        if key.is_dotted() and isinstance(item, Table):
            match = _match_chain(slot, item, names, depth, dotted_prefix)
            if match is not None:
                matches.append(match)
        elif isinstance(item, Table):
            if depth == last:
                existing.append(item)
            else:
                _collect_dotted(
                    item.value, item, names, depth + 1, dotted_prefix, matches, existing
                )
        elif isinstance(item, InlineTable):
            if depth < last:
                raise ConversionError(
                    dotted_prefix,
                    "A standard table cannot be placed in an inline table",
                )
        else:
            name = ".".join(name.key for name in names[: depth + 1])
            raise ConversionError(dotted_prefix, f'"{name}" is not a table')


def _match_chain(
    slot: _Slot, node: Table, names: list[SingleKey], depth: int, dotted_prefix: str
) -> _Match | None:
    trail = [slot]
    last = len(names) - 1
    while depth < last:
        depth += 1
        found = _child(node.value, names[depth])
        if found is None:
            return None

        index, key, item = found
        if key.is_dotted() and isinstance(item, Table):
            trail.append(_Slot(node.value, node, index))
            node = item
            continue

        if isinstance(item, InlineTable):
            if depth == last:
                return None
            raise ConversionError(
                dotted_prefix, "A standard table cannot be placed in an inline table"
            )

        name = ".".join(name.key for name in names[: depth + 1])
        raise ConversionError(dotted_prefix, f'"{name}" is not a table')

    return _Match(trail, node)


def _child(container: Container, name: Key) -> tuple[int, Key, Item] | None:
    for index, (key, item) in enumerate(container.body):
        if isinstance(key, SingleKey) and key == name:
            return index, key, item

    return None


def _chain_entries(node: Table) -> list[tuple[Key | None, Item]]:
    return [(key, item) for key, item in node.value.body if key is not None]


def _content(fragments: list[Table | InlineTable]) -> list[_Entry]:
    """Merge the bodies of the fragments of a table, grouping sub-tables by key."""
    entries: list[_Entry] = []
    tables: dict[str, _Entry] = {}
    for fragment in fragments:
        inline = isinstance(fragment, InlineTable)
        for key, item in fragment.value.body:
            if key is None:
                if isinstance(item, Comment) or (
                    isinstance(item, Whitespace) and not inline
                ):
                    entries.append(_Entry(None, item, inline=inline))
            elif isinstance(item, (Table, InlineTable)):
                entry = tables.get(key.key)
                if entry is None:
                    entry = tables[key.key] = _Entry(key, tables=[item], inline=inline)
                    entries.append(entry)
                else:
                    assert entry.tables is not None
                    entry.tables.append(item)
            else:
                entries.append(_Entry(key, item, inline=inline))

    return entries


def _has_keys(fragments: list[Table | InlineTable]) -> bool:
    return any(key is not None for f in fragments for key, _ in f.value.body)


def _has_aot(fragments: list[Table | InlineTable]) -> bool:
    for fragment in fragments:
        for _, item in fragment.value.body:
            if isinstance(item, AoT) or (isinstance(item, Table) and _has_aot([item])):
                return True

    return False


def _header_trivia(fragments: list[Table | InlineTable]) -> Trivia | None:
    """The trivia of the fragment that renders the header or the inline line.

    Implicit tables are skipped: they are never rendered, and the parser
    copies the header trivia of their first child table into them.
    """
    for fragment in fragments:
        if isinstance(fragment, InlineTable) or not fragment.is_super_table():
            return fragment.trivia

    return None


def _to_inline(fragments: list[Table | InlineTable]) -> InlineTable:
    if len(fragments) == 1 and isinstance(fragments[0], InlineTable):
        return fragments[0]

    container = Container()
    for entry in _content(fragments):
        if entry.key is None:
            continue

        if entry.tables is not None:
            key, item = _key(entry.key), _to_inline(entry.tables)
        else:
            assert entry.item is not None
            key, item = _copy_key(entry.key), entry.item

        _strip_trivia(item)
        container._raw_append(key, item)

    return InlineTable(container, Trivia(), new=True)


def _to_table(inline: InlineTable, name: str, trivia: Trivia) -> Table:
    values: list[tuple[Key | None, Item]] = []
    tables: list[tuple[Key, Table]] = []
    for key, item in inline.value.body:
        if key is None:
            if isinstance(item, Comment):
                item.trivia.indent = ""
                _ensure_newline(item)
                values.append((None, item))
        elif isinstance(item, InlineTable):
            tables.append((_key(key, sep=""), _to_table(item, key.key, Trivia())))
        else:
            _set_line_trivia(item, "")
            values.append((key, item))

    table = _table(values, trivia, name=name)
    for key, child in tables:
        if table.value.body:
            child.trivia.indent = "\n"
        table.value._raw_append(key, child)

    _refresh(table.value, table)

    return table


def _flatten(
    fragments: list[Table | InlineTable],
    prefix: list[Key],
    depth: int,
    max_depth: int | None,
    indent: str,
    out: list[tuple[Key | None, Item]],
) -> None:
    for entry in _content(fragments):
        if entry.key is None:
            assert entry.item is not None
            if isinstance(entry.item, Comment):
                if entry.inline:
                    entry.item.trivia.indent = indent
                _ensure_newline(entry.item)
            out.append((None, entry.item))
            continue

        if entry.tables is None:
            assert entry.item is not None
            _set_line_trivia(entry.item, indent if entry.inline else None)
            out.append(_line(prefix, _copy_key(entry.key), entry.item))
            continue

        header = _header_trivia(entry.tables)
        comment = header.comment if header else ""
        if _has_keys(entry.tables) and (max_depth is None or depth < max_depth):
            if header and comment:
                out.append((None, _comment(header, indent)))
            _flatten(
                entry.tables, [*prefix, entry.key], depth + 1, max_depth, indent, out
            )
        else:
            value = _to_inline(entry.tables)
            value.trivia.indent = indent
            value.trivia.comment_ws = header.comment_ws if header and comment else ""
            value.trivia.comment = comment
            value.trivia.trail = "\n"
            out.append(_line(prefix, _key(entry.key), value))


def _key(key: Key, sep: str = " = ", dotted: bool = False) -> SingleKey:
    assert isinstance(key, SingleKey)
    new = SingleKey(key.key, t=key.t, sep=sep)
    new._dotted = dotted

    return new


def _copy_key(key: Key) -> SingleKey:
    assert isinstance(key, SingleKey)

    return SingleKey(key.key, t=key.t, sep=key.sep, original=key.as_string())


def _table(
    entries: list[tuple[Key | None, Item]],
    trivia: Trivia | None = None,
    *,
    name: str,
    super_table: bool = False,
) -> Table:
    container = Container()
    for key, item in entries:
        container._raw_append(key, item)

    return Table(container, trivia or Trivia(), False, super_table, name)


def _line(prefix: list[Key], key: SingleKey, item: Item) -> tuple[SingleKey, Item]:
    """The body entry of the assignment ``prefix.key = item``."""
    for part in reversed(prefix):
        item = _table([(key, item)], name=part.key, super_table=True)
        key = _key(part, sep="", dotted=True)

    return key, item


def _nest(prefix: list[Key], key: Key, table: Table) -> tuple[SingleKey, Table]:
    """Wrap ``table`` in implicit tables so that it renders as ``[prefix.key]``."""
    new_key = _key(key, sep="")
    for part in reversed(prefix):
        table = _table([(new_key, table)], name=part.key, super_table=True)
        new_key = _key(part, sep="")

    return new_key, table


def _comment(trivia: Trivia, indent: str) -> Comment:
    return Comment(Trivia(indent, trivia.comment_ws, trivia.comment, "\n"))


def _set_comment(trivia: Trivia, comment: Comment) -> None:
    # Standalone comments do not render their comment_ws, so it still holds the
    # spacing of the header comment when the comment was created from one.
    trivia.comment_ws = comment.trivia.comment_ws or " "
    trivia.comment = comment.trivia.comment


def _line_indent(indent: str) -> str:
    return indent.rsplit("\n", 1)[-1]


def _strip_trivia(item: Item) -> None:
    """Prepare an item to be rendered as a value of an inline table."""
    item.trivia.indent = ""
    item.trivia.comment_ws = ""
    item.trivia.comment = ""
    item.trivia.trail = ""
    if isinstance(item, Table):
        for key, child in item.value.body:
            if key is not None:
                _strip_trivia(child)


def _set_line_trivia(item: Item, indent: str | None) -> None:
    """Make a value, or the leaves of a dotted-key chain, render on its own line."""
    if isinstance(item, Table):
        for key, child in item.value.body:
            if key is not None:
                _set_line_trivia(child, indent)
        return

    if indent is not None:
        item.trivia.indent = indent
    if not item.trivia.trail.endswith("\n"):
        item.trivia.trail += "\n"


def _ensure_newline(item: Item) -> None:
    if isinstance(item, (Whitespace, Null)):
        return

    if isinstance(item, AoT):
        if item.body:
            _ensure_newline(item.body[-1])
        return

    if isinstance(item, Table):
        last = item.value._previous_item()
        if last is not None:
            _ensure_newline(last)
            return

    if not item.trivia.trail.endswith("\n"):
        item.trivia.trail += "\n"


def _is_header(key: Key | None, item: Item) -> bool:
    return isinstance(item, AoT) or (
        isinstance(item, Table) and key is not None and not key.is_dotted()
    )


def _header_indent(item: Item) -> str:
    if isinstance(item, AoT):
        return item.body[0].trivia.indent if item.body else ""

    return item.trivia.indent


def _first_header(container: Container) -> int:
    for index, (key, item) in enumerate(container.body):
        if _is_header(key, item):
            return index

    return len(container.body)


def _previous(container: Container, index: int) -> Item | None:
    for _, item in reversed(container.body[:index]):
        if not isinstance(item, Null):
            return item

    return None


def _header_follows(container: Container, index: int) -> bool:
    """Whether a header without a leading blank line is the next item at ``index``."""
    for key, item in container.body[index:]:
        if isinstance(item, Null):
            continue

        return _is_header(key, item) and "\n" not in _header_indent(item)

    return False


def _insert(
    container: Container, index: int, entries: list[tuple[Key | None, Item]]
) -> None:
    for position, (key, item) in enumerate(entries, start=index):
        if position >= len(container.body):
            container._raw_append(key, item)
            continue

        for k, v in container._map.items():
            if isinstance(v, tuple):
                container._map[k] = tuple(i + 1 if i >= position else i for i in v)
            elif v >= position:
                container._map[k] = v + 1

        container.body.insert(position, (key, item))
        if key is None:
            continue

        current = container._map.get(key)
        if current is None:
            container._map[key] = position
        else:
            indices = current if isinstance(current, tuple) else (current,)
            container._map[key] = tuple(sorted((*indices, position)))
        dict.__setitem__(container, key.key, item.value)


def _remove(container: Container, index: int) -> None:
    if container.body[index][0] is None:
        container.body[index] = (None, Null())
    else:
        container._remove_at(index)


def _refresh(container: Container, owner: Table | InlineTable | None) -> None:
    container._table_keys = [k for k, v in container.body if isinstance(v, Table)]
    if owner is not None:
        dict.clear(owner)
        for key, item in container.body:
            if key is not None:
                dict.__setitem__(owner, key.key, item)


def _detach(trail: list[_Slot], limit: int = 0) -> None:
    """Remove the entry at the end of ``trail`` and the implicit tables it empties.

    Entries above ``trail[limit]`` are never removed.
    """
    for position in range(len(trail) - 1, limit - 1, -1):
        slot = trail[position]
        _remove(slot.container, slot.index)
        _refresh(slot.container, slot.owner)
        if position == limit or not _prunable(slot, trail[position - 1]):
            break


def _prunable(slot: _Slot, parent: _Slot) -> bool:
    owner = slot.owner
    if not isinstance(owner, Table):
        return False
    if any(k is not None or isinstance(v, Comment) for k, v in slot.container.body):
        return False

    return parent.key.is_dotted() or owner.is_super_table()


def _detach_all(hits: list[_Hit], container: Container) -> None:
    # The container receiving the converted entries must survive, so pruning
    # stops at the anchor of the hits located in it.
    for hit in hits:
        slot = hit.anchor
        closes_document = (
            slot.owner is None
            and _is_header(slot.key, slot.item)
            and _previous(slot.container, len(slot.container.body)) is slot.item
        )
        _detach(hit.trail, hit.top if slot.container is container else 0)
        if closes_document:
            _trim_last_section(slot.container)


def _trim_last_section(container: Container) -> None:
    """Drop the blank lines that separated the last section from a removed one."""
    body = container.body
    while True:
        index = len(body) - 1
        while index >= 0 and isinstance(body[index][1], Null):
            index -= 1
        if index < 0 or not _is_header(*body[index]):
            return

        item = body[index][1]
        if isinstance(item, AoT):
            if not item.body:
                return
            item = item.body[-1]

        body = item.value.body
        index = len(body) - 1
        while index >= 0 and isinstance(body[index][1], (Null, Whitespace)):
            body[index] = (None, Null())
            index -= 1


def _trim_plain_region(container: Container) -> None:
    """Drop blank lines left alone before the first header once values moved out."""
    region = [
        index
        for index, (_, item) in enumerate(container.body[: _first_header(container)])
        if not isinstance(item, Null)
    ]
    if all(isinstance(container.body[index][1], Whitespace) for index in region):
        for index in region:
            container.body[index] = (None, Null())


def _make_explicit(owner: Table | InlineTable | None) -> None:
    if isinstance(owner, Table) and owner.is_super_table():
        owner._is_super_table = False
        owner.trivia.comment_ws = ""
        owner.trivia.comment = ""


def _normalize_inline(table: InlineTable) -> None:
    entries = [(key, item) for key, item in table.value.body if key is not None]
    for _, item in entries:
        _strip_trivia(item)

    container = table.value
    container.body.clear()
    container._map.clear()
    dict.clear(container)
    for key, item in entries:
        container._raw_append(key, item)

    table._new = True
    _refresh(container, table)


def _place_values(anchor: _Slot, entries: list[tuple[Key | None, Item]]) -> None:
    """Insert plain entries at the anchor, or where plain values are allowed."""
    container, owner = anchor.container, anchor.owner
    if isinstance(owner, InlineTable):
        _insert(container, anchor.index, [e for e in entries if e[0] is not None])
        _normalize_inline(owner)
        return

    if any(key is not None for key, _ in entries):
        _make_explicit(owner)

    index = anchor.index
    if index > _first_header(container):
        index = container._get_last_index_before_table()

    previous = _previous(container, index)
    if previous is not None:
        _ensure_newline(previous)
    if _header_follows(container, index) and not isinstance(entries[-1][1], Whitespace):
        entries = [*entries, (None, Whitespace("\n"))]

    _insert(container, index, entries)
    _refresh(container, owner)


def _place_table(anchor: _Slot, key: Key, node: Table, table: Table) -> None:
    """Insert the header table ``table``, wrapped in ``node``, after the plain values."""
    container, owner = anchor.container, anchor.owner
    index = _first_header(container)

    indent = _line_indent(table.trivia.indent)
    previous = _previous(container, index)
    if previous is not None and not (
        isinstance(previous, Whitespace) or ends_with_whitespace(previous)
    ):
        _ensure_newline(previous)
        indent = "\n" + indent
    table.trivia.indent = indent

    if _header_follows(container, index) and not ends_with_whitespace(table):
        table.value._raw_append(None, Whitespace("\n"))

    _insert(container, index, [(key, node)])
    _refresh(container, owner)
