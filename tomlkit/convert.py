"""
Conversion between the three structural forms TOML offers for nested data:
standard ``[header]`` tables, inline tables and dotted-key assignments.
"""

from __future__ import annotations

from tomlkit.container import Container
from tomlkit.exceptions import ConversionError
from tomlkit.exceptions import TOMLKitError
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

# Contexts a container can live in, which decide where entries may be placed:
# "doc"/"table" accept header tables, "dotted" is the inside of a dotted-key
# chain and "inline" is the inside of an inline table.
_DOC = "doc"
_TABLE = "table"
_DOTTED = "dotted"
_INLINE = "inline"


class _Node:
    """A table's logical content, with dotted-key fragments and
    out-of-order pieces merged together."""

    def __init__(self) -> None:
        self.entries: list[tuple[SingleKey | None, _Node | Item]] = []
        self.index: dict[str, int] = {}
        self.comment = ""
        self.comment_ws = ""

    def has_keys(self) -> bool:
        return any(k is not None for k, _ in self.entries)

    def child(self, key: Key, key_path: str) -> _Node:
        pos = self.index.get(key.key)
        if pos is not None:
            existing = self.entries[pos][1]
            if not isinstance(existing, _Node):
                raise ConversionError(key_path, f'key "{key.key}" is defined twice')
            return existing

        node = _Node()
        self.index[key.key] = len(self.entries)
        self.entries.append((key, node))
        return node

    def add_leaf(self, key: Key, item: Item, key_path: str) -> None:
        if key.key in self.index:
            raise ConversionError(key_path, f'key "{key.key}" is defined twice')

        self.index[key.key] = len(self.entries)
        self.entries.append((key, item))

    def contains_aot(self) -> bool:
        return any(
            isinstance(v, AoT) or (isinstance(v, _Node) and v.contains_aot())
            for _, v in self.entries
        )


def _parse_key_path(key_path: str) -> list[str]:
    if not isinstance(key_path, str) or not key_path.strip():
        raise ConversionError(str(key_path), "invalid key path")

    try:
        parser = Parser(key_path)
        key = parser._parse_key()
        complete = parser.end()
    except TOMLKitError:
        complete = False

    if not complete:
        raise ConversionError(key_path, "invalid key path")

    return [k.key for k in key]


def _fill(node: _Node, container: Container, key_path: str) -> None:
    for k, v in container.body:
        if k is None:
            if isinstance(v, Comment):
                node.entries.append((None, v))
            continue

        if isinstance(v, (Table, InlineTable)):
            child = node.child(k, key_path)
            _take_comment(child, v)
            _fill(child, v.value, key_path)
        else:
            node.add_leaf(k, v, key_path)


def _take_comment(node: _Node, table: Table | InlineTable) -> None:
    if node.comment or not table.trivia.comment:
        return

    if isinstance(table, Table) and table.is_super_table():
        return

    node.comment = table.trivia.comment
    node.comment_ws = table.trivia.comment_ws or " "


def _build(items: list[Table | InlineTable], key_path: str) -> _Node:
    node = _Node()
    for it in items:
        _take_comment(node, it)
        _fill(node, it.value, key_path)

    return node


def _fresh_key(key: Key, sep: str = " = ", dotted: bool = False) -> SingleKey:
    if isinstance(key, SingleKey):
        new = SingleKey(key.key, t=key.t, sep=sep, original=key.as_string().strip())
    else:
        new = SingleKey(key.key, sep=sep)

    new._dotted = dotted
    return new


def _comment(text: str) -> Comment:
    return Comment(Trivia(comment_ws="", comment=text, trail="\n"))


def _leaf(item: Item, inline: bool) -> Item:
    trivia = item.trivia
    trivia.indent = ""
    if inline:
        trivia.comment_ws = ""
        trivia.comment = ""
        trivia.trail = ""
    elif "\n" not in trivia.trail:
        trivia.trail = "\n"

    return item


def _emit_inline(node: _Node) -> InlineTable:
    container = Container()
    for k, v in node.entries:
        if k is None:
            continue

        value = _emit_inline(v) if isinstance(v, _Node) else _leaf(v, inline=True)
        if isinstance(value, InlineTable):
            value.trivia.trail = ""
        container._raw_append(_fresh_key(k), value)

    return InlineTable(container, Trivia(), new=True)


def _emit_standard(node: _Node, name: str) -> Table:
    container = Container()
    subtables = []
    for k, v in node.entries:
        if k is None:
            container._raw_append(None, _comment(v.trivia.comment))
        elif isinstance(v, _Node):
            subtables.append((k, v))
        else:
            container._raw_append(_fresh_key(k), _leaf(v, inline=False))

    for k, v in subtables:
        table = _emit_standard(v, k.key)
        if container.body:
            table.trivia.indent = "\n"
        container._raw_append(_fresh_key(k), table)

    trivia = Trivia(comment_ws=node.comment_ws if node.comment else "")
    trivia.comment = node.comment
    return Table(container, trivia, False, is_super_table=False, name=name)


def _chain(path: list[Key], value: Item) -> tuple[SingleKey, Item]:
    """Build the nested super-table structure the parser produces
    for a single ``a.b.c = value`` assignment."""
    key: SingleKey = _fresh_key(path[-1])
    for k in reversed(path[:-1]):
        container = Container(True)
        container._raw_append(key, value)
        value = Table(container, Trivia(), False, is_super_table=True, name=k.key)
        key = _fresh_key(k, sep="", dotted=True)

    return key, value


def _emit_dotted(
    prefix: list[Key],
    node: _Node,
    depth: int,
    max_depth: int | None,
    inline: bool,
) -> list[tuple[SingleKey | None, Item]]:
    out: list[tuple[SingleKey | None, Item]] = []
    if node.comment and not inline:
        out.append((None, _comment(node.comment)))

    for k, v in node.entries:
        if k is None:
            if not inline:
                out.append((None, _comment(v.trivia.comment)))
            continue

        path = [*prefix, k]
        if not isinstance(v, _Node):
            out.append(_chain(path, _leaf(v, inline)))
        elif v.has_keys() and (max_depth is None or depth < max_depth):
            out.extend(_emit_dotted(path, v, depth + 1, max_depth, inline))
        else:
            out.append(_chain(path, _inline_value(v, inline)))

    return out


def _inline_value(node: _Node, inline: bool) -> InlineTable:
    value = _emit_inline(node)
    if inline:
        value.trivia.trail = ""
    elif node.comment:
        value.trivia.comment_ws = node.comment_ws
        value.trivia.comment = node.comment

    return value


class _Target:
    """The entries a key path resolves to, all within one container."""

    def __init__(
        self,
        container: Container,
        owner: AbstractTable | None,
        owner_key: Key | None,
        context: str,
        indices: list[int],
        parent: tuple[Container, AbstractTable | None] | None,
    ) -> None:
        self.container = container
        self.owner = owner
        self.owner_key = owner_key
        self.context = context
        self.indices = indices
        # The container holding ``owner``, with its own owner.
        self.parent = parent

    @property
    def key(self) -> Key:
        return self.container.body[self.indices[0]][0]

    @property
    def items(self) -> list[Item]:
        return [self.container.body[i][1] for i in self.indices]


def _child_context(context: str, key: Key, item: Item) -> str:
    if isinstance(item, InlineTable) or context == _INLINE:
        return _INLINE
    if key.is_dotted() or context == _DOTTED:
        return _DOTTED
    return _TABLE


def _resolve(doc: Container, key_path: str) -> _Target:
    parts = _parse_key_path(key_path)
    candidates = [(doc, None, None, _DOC, None)]
    matches = []

    for depth, part in enumerate(parts):
        matches = [
            (*candidate, i)
            for candidate in candidates
            for i, (k, _) in enumerate(candidate[0].body)
            if k is not None and k.key == part
        ]
        if not matches:
            raise ConversionError(key_path, "key does not exist")

        if depth == len(parts) - 1:
            break

        candidates = []
        for container, owner, _, context, _, i in matches:
            k, v = container.body[i]
            if not isinstance(v, (Table, InlineTable)):
                raise ConversionError(
                    key_path, f'"{".".join(parts[: depth + 1])}" is not a table'
                )
            candidates.append(
                (v.value, v, k, _child_context(context, k, v), (container, owner))
            )

    if len({id(m[0]) for m in matches}) > 1:
        raise ConversionError(key_path, "the table is split across several containers")

    container, owner, owner_key, context, parent, _ = matches[0]
    return _Target(
        container, owner, owner_key, context, [m[-1] for m in matches], parent
    )


def _reindex(container: Container, owner: AbstractTable | None) -> None:
    body = list(container._body)
    container._body.clear()
    container._map = {}
    container._table_keys = []
    dict.clear(container)
    for k, v in body:
        container._raw_append(k, v)

    if owner is not None:
        dict.clear(owner)
        for k, v in body:
            if k is not None:
                dict.__setitem__(owner, k.key, v)


def _normalize_inline(table: InlineTable) -> None:
    """Drop the original separators so the inline table renders
    its (possibly new) entries with generated ones."""
    body = [(k, v) for k, v in table.value.body if k is not None]
    for _, v in body:
        v.trivia.indent = ""

    table.value._body[:] = body
    table._new = True
    _reindex(table.value, table)


def _is_value_entry(key: Key, item: Item) -> bool:
    return not isinstance(item, (Table, AoT)) or key.is_dotted()


def _previous_item(container: Container, pos: int) -> Item | None:
    prev = container._previous_item_with_index(pos)
    return prev[1] if prev else None


def _separate_next_table(container: Container, pos: int) -> None:
    """Keep a blank line before the header table following ``pos``."""
    for k, v in container.body[pos:]:
        if isinstance(v, Null):
            continue
        if isinstance(v, AoT) and v.body:
            v = v.body[0]
        if isinstance(v, Table) and not k.is_dotted() and "\n" not in v.trivia.indent:
            v.trivia.indent = "\n" + v.trivia.indent
        return


def _replace_values(
    target: _Target, entries: list[tuple[SingleKey | None, Item]], key_path: str
) -> None:
    """Replace the target entries with value entries (key/value pairs,
    dotted keys or comments)."""
    container, owner = target.container, target.owner
    dest, dest_owner = container, owner
    unsuper = (
        target.context == _TABLE
        and isinstance(owner, Table)
        and owner.is_super_table()
        and not target.owner_key.is_dotted()
    )
    if unsuper and target.parent is not None:
        # The owner is an implicit piece of a table defined elsewhere,
        # so giving it values would define that table a second time.
        siblings = [
            (k, v)
            for k, v in target.parent[0].body
            if k is not None and k.key == target.owner_key.key and v is not owner
        ]
        if siblings:
            headers = [
                v
                for k, v in siblings
                if isinstance(v, Table) and not k.is_dotted() and not v.is_super_table()
            ]
            if len(headers) != 1:
                raise ConversionError(
                    key_path, f'"{target.owner_key.key}" is already defined elsewhere'
                )
            dest, dest_owner = headers[0].value, headers[0]
            unsuper = False

    was_values = all(_is_value_entry(*container.body[i]) for i in target.indices)
    for i in target.indices:
        container._body[i] = (None, Null())

    if dest is not container or (target.context in (_DOC, _TABLE) and not was_values):
        pos = dest._get_last_index_before_table()
    else:
        pos = target.indices[0]

    prev = _previous_item(dest, pos)
    if (
        target.context != _INLINE
        and prev is not None
        and not isinstance(prev, (Whitespace, Table, AoT))
        and "\n" not in prev.trivia.trail
    ):
        prev.trivia.trail += "\n"

    dest._body[pos:pos] = entries
    if target.context in (_DOC, _TABLE):
        _separate_next_table(dest, pos + len(entries))
    _reindex(dest, dest_owner)

    if dest is not container:
        _reindex(container, owner)
        if not any(k is not None for k, _ in container.body):
            parent, parent_owner = target.parent
            for i, (_, v) in enumerate(parent.body):
                if v is owner:
                    parent._body[i] = (None, Null())
            _reindex(parent, parent_owner)

    if isinstance(owner, InlineTable):
        _normalize_inline(owner)
    elif unsuper:
        owner._is_super_table = False


def _insert_table(
    container: Container,
    owner: AbstractTable | None,
    key: SingleKey,
    table: Table,
) -> None:
    pos = container._get_last_index_before_table()
    prev = _previous_item(container, pos)
    if prev is not None and not isinstance(prev, Whitespace):
        table.trivia.indent = "\n"

    container._body.insert(pos, (key, table))
    _separate_next_table(container, pos + 1)
    _reindex(container, owner)


def to_inline_table(key_path: str, doc: Container) -> Container:
    """
    Convert the standard table at ``key_path`` into an inline table.

    Nested sub-tables become nested inline tables and the table header's
    comment becomes the trailing comment of the new assignment.

    :Example:

    >>> doc = parse("[server]\\nhost = 'localhost'\\nport = 8080\\n")
    >>> print(dumps(to_inline_table("server", doc)), end="")
    server = {host = 'localhost', port = 8080}
    """
    target = _resolve(doc, key_path)
    items = target.items
    if len(items) == 1 and isinstance(items[0], InlineTable):
        return doc

    if not all(isinstance(it, Table) for it in items):
        raise ConversionError(key_path, "not a table")

    node = _build(items, key_path)
    if node.contains_aot():
        raise ConversionError(
            key_path, "an array of tables can't be part of an inline table"
        )

    inline = target.context == _INLINE
    if inline and not isinstance(target.owner, InlineTable) and len(items) > 1:
        raise ConversionError(key_path, "unsupported dotted-key layout")

    value = _inline_value(node, inline)
    _replace_values(target, [(_fresh_key(target.key), value)], key_path)
    return doc


def to_standard_table(key_path: str, doc: Container) -> Container:
    """
    Convert the inline table at ``key_path`` into a standard ``[header]`` table.

    Nested inline tables become nested sub-tables and the comment on the
    inline table's line becomes the table header's comment.

    :Example:

    >>> doc = parse("server = {host = 'localhost', port = 8080}\\n")
    >>> print(dumps(to_standard_table("server", doc)), end="")
    [server]
    host = 'localhost'
    port = 8080
    """
    target = _resolve(doc, key_path)
    items = target.items
    if (
        len(items) == 1
        and isinstance(items[0], Table)
        and not target.key.is_dotted()
        and target.context in (_DOC, _TABLE)
    ):
        return doc

    if len(items) != 1 or not isinstance(items[0], InlineTable):
        raise ConversionError(key_path, "not an inline table")

    if target.context not in (_DOC, _TABLE):
        raise ConversionError(
            key_path,
            "a standard table can't be nested in an inline table or dotted key",
        )

    key = target.key
    table = _emit_standard(_build(items, key_path), key.key)
    target.container._body[target.indices[0]] = (None, Null())
    _insert_table(target.container, target.owner, _fresh_key(key), table)
    return doc


def to_dotted_keys(
    key_path: str, doc: Container, max_depth: int | None = None
) -> Container:
    """
    Flatten the table or inline table at ``key_path`` into dotted-key
    assignments in its parent container.

    ``max_depth`` limits how many levels are flattened: ``None`` flattens
    everything, ``1`` only turns the immediate children into dotted keys
    (deeper tables become inline tables). The table header's comment becomes
    a standalone comment before the first dotted key.

    :Example:

    >>> doc = parse("[server]\\nhost = 'localhost'\\nport = 8080\\n")
    >>> print(dumps(to_dotted_keys("server", doc)), end="")
    server.host = 'localhost'
    server.port = 8080
    """
    if max_depth is not None and max_depth < 1:
        raise ValueError("max_depth must be None or a positive integer")

    target = _resolve(doc, key_path)
    items = target.items
    if not all(isinstance(it, (Table, InlineTable)) for it in items):
        raise ConversionError(key_path, "not a table or inline table")

    node = _build(items, key_path)
    if node.contains_aot():
        raise ConversionError(
            key_path, "an array of tables can't be expressed with dotted keys"
        )

    inline = target.context == _INLINE
    key = target.key
    if node.has_keys():
        entries = _emit_dotted([key], node, 1, max_depth, inline)
    else:
        entries = [(_fresh_key(key), _inline_value(node, inline))]

    if inline and not isinstance(target.owner, InlineTable) and len(entries) > 1:
        raise ConversionError(key_path, "unsupported dotted-key layout")

    _replace_values(target, entries, key_path)
    return doc


def _collect_prefix_tables(
    table: Table,
    rest: list[str],
    trail: list[tuple[Container, AbstractTable | None, int]],
    out: list[tuple[list[tuple[Container, AbstractTable | None, int]], Table]],
    dotted_prefix: str,
) -> None:
    if not rest:
        out.append((trail, table))
        return

    for i, (k, v) in enumerate(table.value.body):
        if k is None or k.key != rest[0]:
            continue

        if not (isinstance(v, Table) and k.is_dotted()):
            raise ConversionError(
                dotted_prefix, "the prefix is already assigned a value"
            )

        _collect_prefix_tables(
            v, rest[1:], [*trail, (table.value, table, i)], out, dotted_prefix
        )


def to_super_table(dotted_prefix: str, doc: Container) -> Container:
    """
    Group the dotted-key assignments sharing ``dotted_prefix`` into a new
    ``[dotted_prefix]`` table.

    A standalone comment immediately preceding the first matching assignment
    becomes the table header's comment.

    :Example:

    >>> doc = parse("server.host = 'localhost'\\nserver.port = 8080\\n")
    >>> print(dumps(to_super_table("server", doc)), end="")
    [server]
    host = 'localhost'
    port = 8080
    """
    parts = _parse_key_path(dotted_prefix)
    container: Container = doc
    owner: AbstractTable | None = None
    depth = 0
    while True:
        entries = [
            (k, v) for k, v in container.body if k is not None and k.key == parts[depth]
        ]
        if any(k.is_dotted() and isinstance(v, Table) for k, v in entries):
            break

        if depth == len(parts) - 1:
            raise ConversionError(dotted_prefix, "no matching dotted keys found")

        headers = [v for _, v in entries if isinstance(v, Table)]
        if len(headers) != 1:
            raise ConversionError(dotted_prefix, "no matching dotted keys found")

        owner = headers[0]
        container = owner.value
        depth += 1

    rel = parts[depth:]
    matches: list[tuple[list[tuple[Container, AbstractTable | None, int]], Table]] = []
    for i, (k, v) in enumerate(container.body):
        if k is not None and k.key == rel[0] and k.is_dotted() and isinstance(v, Table):
            _collect_prefix_tables(
                v, rel[1:], [(container, owner, i)], matches, dotted_prefix
            )

    if not matches:
        raise ConversionError(dotted_prefix, "no matching dotted keys found")

    moved: list[tuple[Key | None, Item]] = []
    for _, table in matches:
        moved.extend(
            (k, v)
            for k, v in table.value.body
            if k is not None or isinstance(v, Comment)
        )

    first_index = matches[0][0][0][2]
    comment = ""
    comment_ws = ""
    prev = container._previous_item_with_index(first_index)
    if prev is not None and isinstance(prev[1], Comment):
        comment = prev[1].trivia.comment
        comment_ws = prev[1].trivia.comment_ws or " "
        container._body[prev[0]] = (None, Null())

    for trail, _ in reversed(matches):
        for parent, parent_owner, i in reversed(trail):
            parent._body[i] = (None, Null())
            if parent is container:
                break
            if any(k is not None for k, _ in parent.body):
                _reindex(parent, parent_owner)
                break

    body = Container()
    for k, v in moved:
        if not isinstance(v, Whitespace):
            v.trivia.indent = ""
        body._raw_append(k, v)

    trivia = Trivia(comment_ws=comment_ws, comment=comment)
    table = Table(body, trivia, False, is_super_table=False, name=rel[-1])
    key = SingleKey(rel[-1])
    for name in reversed(rel[:-1]):
        wrapper = Container()
        wrapper._raw_append(key, table)
        table = Table(wrapper, Trivia(), False, is_super_table=True, name=name)
        key = SingleKey(name)

    _insert_table(container, owner, key, table)
    return doc
