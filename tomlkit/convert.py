"""Convert between standard tables, inline tables, and dotted keys."""

from __future__ import annotations

from dataclasses import dataclass

from tomlkit.container import Container
from tomlkit.exceptions import ConversionError
from tomlkit.exceptions import ParseError
from tomlkit.items import AoT
from tomlkit.items import Array
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


__all__ = [
    "to_dotted_keys",
    "to_inline_table",
    "to_standard_table",
    "to_super_table",
]


class _ContainsAoT(Exception):
    """Internal signal that a subtree contains an array of tables."""


@dataclass
class _Hit:
    """One physical container entry matching a path component."""

    container: Container
    index: int
    key: Key
    item: Item
    owner: Table | InlineTable | None
    parent: _Hit | None = None


def to_inline_table(key_path: str, doc: Container) -> Container:
    """Convert the standard table at ``key_path`` into an inline table.

    Nested tables become nested inline tables. The document is mutated in
    place and returned. Already-inline targets are left unchanged.
    """

    parts = _parse_key_path(key_path)
    hits = _resolve(doc, parts, key_path)
    items = [hit.item for hit in hits]
    if all(isinstance(item, InlineTable) for item in items):
        return doc
    if not all(isinstance(item, Table) for item in items):
        _raise_type(key_path, items[0] if items else None, "a table")

    tables: list[Table] = [item for item in items if isinstance(item, Table)]
    try:
        for table in tables:
            _contains_aot(table)
    except _ContainsAoT:
        raise ConversionError(
            key_path,
            f"Cannot convert {key_path!r} to an inline table: "
            "a descendant is an array of tables",
        ) from None

    comment, comment_ws = _target_header_comment(tables)
    inline = _build_inline(_merged_entries(tables), comment, comment_ws)
    _clear_duplicate_super_comments(hits[0], comment)
    _install(doc, parts, inline)
    inline.trivia.comment = comment
    inline.trivia.comment_ws = comment_ws
    if "\n" not in inline.trivia.trail:
        inline.trivia.trail = "\n"
    return doc


def to_standard_table(key_path: str, doc: Container) -> Container:
    """Convert the inline table at ``key_path`` into a ``[header]`` table.

    Nested inline tables become nested tables. The inline table's comment
    becomes the table header comment. The document is mutated in place and
    returned. Already-standard targets are left unchanged.
    """

    parts = _parse_key_path(key_path)
    hits = _resolve(doc, parts, key_path)
    items = [hit.item for hit in hits]
    if all(isinstance(item, Table) for item in items):
        return doc
    if not all(isinstance(item, InlineTable) for item in items):
        _raise_type(key_path, items[0] if items else None, "an inline table")

    if any(isinstance(hit.owner, InlineTable) for hit in hits):
        # A standard table cannot live inside an inline table. Convert the
        # parent first; that recursively converts this inline table as well.
        to_standard_table(_format_path(parts[:-1]), doc)
        return doc

    inline = next(item for item in items if isinstance(item, InlineTable))
    table = _inline_to_standard(inline, _format_path(parts), parts[-1].key)
    _install(doc, parts, table)
    return doc


def to_dotted_keys(
    key_path: str, doc: Container, max_depth: int | None = None
) -> Container:
    """Flatten the table at ``key_path`` into dotted-key assignments.

    ``max_depth`` limits how many table levels are flattened. ``None``
    flattens every nested table. ``1`` flattens only the target table's
    immediate children. The table header comment becomes a standalone
    comment before the first dotted key.
    """

    parts = _parse_key_path(key_path)
    hits = _resolve(doc, parts, key_path)
    if not hits or not all(isinstance(hit.item, (Table, InlineTable)) for hit in hits):
        _raise_type(
            key_path,
            hits[0].item if hits else None,
            "a table or inline table",
        )

    child_levels = None if max_depth is None else max_depth - 1
    for hit in hits:
        table = _ensure_table(hit)
        _flatten_descendants(table, child_levels)
        _emit_header_comment(hit, table)
        _mark_dotted(hit, table)
    return doc


def to_super_table(dotted_prefix: str, doc: Container) -> Container:
    """Group dotted-key entries that share ``dotted_prefix`` into a table.

    A standalone comment immediately preceding the first match becomes the
    new table's header comment. The document is mutated in place and returned.
    """

    parts = _parse_key_path(dotted_prefix)
    _ensure_standard_ancestors(doc, parts[:-1], dotted_prefix)
    hits = _resolve(doc, parts, dotted_prefix)
    matches = [
        hit
        for hit in hits
        if hit.key.is_dotted() and isinstance(hit.item, (Table, InlineTable))
    ]
    if not matches:
        raise ConversionError(
            dotted_prefix, f"No dotted keys found for {dotted_prefix!r}"
        )

    comment, comment_ws = _take_preceding_comment(matches[0])
    base = _ensure_table(matches[0])
    for hit in reversed(matches[1:]):
        _absorb(base, hit.item)
        _delete_item(hit.container, hit.item, hit.owner)
        _prune_empty_dotted(hit.parent)

    anchor, owner, chain = _super_table_anchor(matches[0])
    _delete_item(matches[0].container, base, matches[0].owner)
    _prune_empty_dotted(matches[0].parent)
    _style_grouped_table(base, parts, comment, comment_ws)
    entry_key, entry = _wrap_super_chain(chain, base)
    _insert_entry(anchor, len(anchor.body), entry_key, entry, owner)
    _relocate_after_scalars(anchor, entry, owner)
    return doc


def _parse_key_path(key_path: str) -> tuple[SingleKey, ...]:
    if not isinstance(key_path, str) or key_path == "":
        raise ConversionError("" if not isinstance(key_path, str) else key_path)
    parser = Parser(key_path)
    try:
        key = parser._parse_key()
    except (ParseError, ConversionError):
        raise ConversionError(key_path) from None
    if not parser.end():
        raise ConversionError(key_path)
    parts = tuple(key)
    if not parts:
        raise ConversionError(key_path)
    return parts


def _format_path(parts: tuple[SingleKey, ...]) -> str:
    return ".".join(part.as_string() for part in parts)


def _raise_type(key_path: str, item: Item | None, expected: str) -> None:
    found = type(item).__name__ if item is not None else "nothing"
    raise ConversionError(
        key_path, f"Cannot convert {key_path!r}: expected {expected}, found {found}"
    )


def _resolve(doc: Container, parts: tuple[SingleKey, ...], key_path: str) -> list[_Hit]:
    level: list[tuple[_Hit | None, Container, Table | InlineTable | None]] = [
        (None, doc, None)
    ]
    found: list[_Hit] = []
    for depth, part in enumerate(parts):
        found = []
        for parent, container, owner in level:
            for index, key, item in _entries(container, part.key):
                found.append(
                    _Hit(
                        container=container,
                        index=index,
                        key=key,
                        item=item,
                        owner=owner,
                        parent=parent,
                    )
                )
        if not found:
            raise ConversionError(
                key_path, f"Key {key_path!r} does not exist."
            )
        if depth == len(parts) - 1:
            return found
        level = []
        for hit in found:
            if isinstance(hit.item, (Table, InlineTable)):
                level.append((hit, hit.item.value, hit.item))
            else:
                raise ConversionError(
                    key_path,
                    f"Key {key_path!r} traverses non-table {hit.key.key!r}",
                )
    raise ConversionError(key_path)


def _entries(container: Container, name: str) -> list[tuple[int, Key, Item]]:
    found = []
    for index, (key, item) in enumerate(container.body):
        if key is not None and key.key == name and not isinstance(item, Null):
            found.append((index, key, item))
    return found


def _install(doc: Container, parts: tuple[SingleKey, ...], new_item: Item) -> None:
    current: Container | Table | InlineTable = doc
    for part in parts[:-1]:
        if isinstance(current, (Table, InlineTable)):
            current = current[part.key]
        else:
            current = current[part.key]
    current[parts[-1].key] = new_item


def _contains_aot(item: Item) -> None:
    if isinstance(item, AoT):
        raise _ContainsAoT()
    if isinstance(item, (Table, InlineTable)):
        for _, child in item.value.body:
            if isinstance(child, Item) and not isinstance(child, (Whitespace, Null)):
                _contains_aot(child)
    elif isinstance(item, Array):
        for child in item:
            if isinstance(child, Item):
                _contains_aot(child)


def _target_header_comment(tables: list[Table]) -> tuple[str, str]:
    """Comment that belongs to the table being converted, not a nested header."""

    for table in tables:
        comment, comment_ws = _own_header_comment(table)
        if comment:
            return comment, comment_ws
    return "", ""


def _own_header_comment(table: Table) -> tuple[str, str]:
    # Implicit super tables created for ``[a.b]`` copy the leaf header comment.
    # That comment belongs to the leaf, which is converted on its own.
    if table.is_super_table() and not table.display_name:
        return "", ""
    comment = table.trivia.comment or ""
    if not comment:
        return "", ""
    return comment, table.trivia.comment_ws or " "


def _merged_entries(tables: list[Table]) -> list[tuple[Key | None, Item]]:
    """Body pairs with out-of-order table fragments merged into the first one."""

    combined: list[tuple[Key | None, Item]] = []
    tables_by_key: dict[str, Table] = {}
    for table in tables:
        for key, item in table.value.body:
            if isinstance(item, (Null, Whitespace)):
                continue
            if key is not None and isinstance(item, Table) and key.key in tables_by_key:
                _absorb(tables_by_key[key.key], item)
                continue
            combined.append((key, item))
            if key is not None and isinstance(item, Table):
                tables_by_key[key.key] = item
    return combined


def _build_inline(
    entries: list[tuple[Key | None, Item]],
    comment: str = "",
    comment_ws: str = "",
) -> InlineTable:
    container = Container(True)
    need_comma = False
    for key, item in entries:
        if key is None:
            if isinstance(item, Comment):
                if need_comma:
                    container.body.append((None, Whitespace(", ")))
                    need_comma = False
                container.body.append((None, _as_inline_comment(item)))
            continue
        if need_comma:
            container.body.append((None, Whitespace(", ")))
        value, trailing = _value_for_inline(item)
        container._raw_append(_inline_key(key), value)
        if trailing:
            container.body.append((None, Whitespace(",")))
            container.body.append((None, _comment_item(trailing, inline=True)))
            need_comma = False
        else:
            need_comma = True
    inline = InlineTable(
        container,
        Trivia(
            comment=comment,
            comment_ws=comment_ws or (" " if comment else ""),
            trail="\n",
        ),
        new=False,
    )
    return inline


def _value_for_inline(item: Item) -> tuple[Item, str]:
    if isinstance(item, Table):
        nested = _build_inline(_merged_entries([item]))
        comment, _ws = _own_header_comment(item)
        return nested, comment
    if isinstance(item, InlineTable):
        comment = item.trivia.comment
        item.trivia.comment = ""
        item.trivia.comment_ws = ""
        item.trivia.trail = ""
        item.trivia.indent = ""
        return item, comment
    if isinstance(item, AoT):
        raise _ContainsAoT()
    comment = ""
    if hasattr(item, "trivia"):
        comment = item.trivia.comment
        item.trivia.comment = ""
        item.trivia.comment_ws = ""
        item.trivia.trail = ""
        item.trivia.indent = ""
    return item, comment


def _inline_key(key: Key) -> SingleKey:
    return _clean_single_key(key, dotted=False, sep=" = ")


def _clean_single_key(key: Key, *, dotted: bool, sep: str | None = None) -> SingleKey:
    """Key whose rendered text does not include the whitespace before ``=``.

    Parsed keys often store that whitespace in ``as_string()``. Using it as a
    dotted prefix produces ``foo .bar``.
    """

    key_type = key.t if isinstance(key, SingleKey) else None
    if sep is None:
        sep = "" if dotted else (key.sep if key.sep and key.sep.strip() else " = ")
    cleaned = SingleKey(key.key, t=key_type, sep=sep)
    cleaned._dotted = dotted
    return cleaned


def _retarget_key(
    container: Container,
    index: int,
    *,
    dotted: bool,
    owner: Table | InlineTable | None,
) -> Key:
    key, item = container.body[index]
    if key is None:
        return key
    cleaned = _clean_single_key(key, dotted=dotted)
    container.body[index] = (cleaned, item)
    _rebuild_map(container)
    _sync_owner(owner, container)
    return cleaned


def _as_inline_comment(comment: Comment) -> Comment:
    trail = comment.trivia.trail if "\n" in comment.trivia.trail else "\n"
    indent = comment.trivia.indent or " "
    return Comment(Trivia(indent=indent, comment=comment.trivia.comment, trail=trail))


def _comment_item(comment: str, *, inline: bool = False) -> Comment:
    text = comment if comment.startswith("#") else "# " + comment
    return Comment(Trivia(indent=" " if inline else "", comment=text, trail="\n"))


def _clear_duplicate_super_comments(hit: _Hit, comment: str) -> None:
    if not comment:
        return
    parent = hit.parent
    while parent is not None and isinstance(parent.item, Table):
        table = parent.item
        if (
            table.trivia.comment == comment
            and table.is_super_table()
            and not table.display_name
        ):
            table.trivia.comment = ""
            table.trivia.comment_ws = ""
        parent = parent.parent


def _inline_to_standard(
    inline: InlineTable, display_name: str | None, name: str | None
) -> Table:
    container = Container(True)
    table = Table(
        container,
        Trivia(
            comment=inline.trivia.comment,
            comment_ws=inline.trivia.comment_ws or (" " if inline.trivia.comment else ""),
            trail="\n",
        ),
        False,
        is_super_table=False,
        name=name,
        display_name=display_name,
    )
    for key, item in inline.value.body:
        if isinstance(item, Null):
            continue
        if isinstance(item, Whitespace):
            continue
        if key is None and isinstance(item, Comment):
            table.raw_append(None, item)
            continue
        if key is None:
            continue
        if isinstance(item, InlineTable):
            child_name = _format_child_display(display_name, key)
            child = _inline_to_standard(item, child_name, key.key)
            table.raw_append(_plain_key(key), child)
            continue
        _normalize_standard_item(item)
        table.raw_append(key, item)
    _tables_after_scalars(container, table)
    return table


def _tables_after_scalars(container: Container, owner: Table | InlineTable | None) -> None:
    """Keep key/value pairs before subtables so headers do not capture them."""

    scalars: list[tuple[Key | None, Item]] = []
    tables: list[tuple[Key | None, Item]] = []
    for entry in container.body:
        key, item = entry
        if key is not None and isinstance(item, (Table, AoT)):
            tables.append(entry)
        else:
            scalars.append(entry)
    if not tables or not scalars:
        return
    container.body[:] = scalars + tables
    _rebuild_map(container)
    _sync_owner(owner, container)


def _format_child_display(parent: str | None, key: Key) -> str | None:
    if not parent:
        return None
    return parent + "." + key.as_string()


def _plain_key(key: Key) -> SingleKey:
    """A non-dotted key that preserves quoting, for a newly created sub-table."""

    sep = key.sep if key.sep and key.sep.strip() else ""
    key_type = key.t if isinstance(key, SingleKey) else None
    copied = SingleKey(key.key, t=key_type, sep=sep)
    copied._dotted = False
    return copied


def _normalize_standard_item(item: Item) -> None:
    if isinstance(item, InlineTable):
        if "\n" not in item.trivia.trail:
            item.trivia.trail = "\n"
        return
    if isinstance(item, Table):
        if "\n" not in item.trivia.trail:
            item.trivia.trail = "\n"
        for key, child in item.value.body:
            if key is not None and isinstance(child, Item):
                _normalize_standard_item(child)
        return
    if isinstance(item, (Whitespace, Comment, Null, AoT)):
        return
    if hasattr(item, "trivia"):
        item.trivia.indent = ""
        if "\n" not in item.trivia.trail:
            item.trivia.trail = "\n"


def _ensure_table(hit: _Hit) -> Table:
    if isinstance(hit.item, Table):
        return hit.item
    if not isinstance(hit.item, InlineTable):
        raise ConversionError("")  # pragma: no cover - guarded by callers
    table = _shallow_table_from_inline(hit.item)
    _replace_entry(hit, table)
    return table


def _shallow_table_from_inline(inline: InlineTable) -> Table:
    """Turn an inline table into a table without flattening nested inline tables."""

    container = Container(True)
    for key, item in inline.value.body:
        if isinstance(item, (Null, Whitespace)):
            continue
        if key is None and isinstance(item, Comment):
            container.body.append((None, item))
            continue
        if key is None:
            continue
        _normalize_standard_item(item)
        container._raw_append(key, item)
    return Table(
        container,
        Trivia(
            comment=inline.trivia.comment,
            comment_ws=inline.trivia.comment_ws,
            trail="\n",
        ),
        False,
        is_super_table=False,
        name=None,
    )


def _replace_entry(hit: _Hit, new_item: Item) -> None:
    index = _index_of(hit.container, hit.item)
    if index is None:
        index = hit.index
    key = hit.container.body[index][0]
    hit.container.body[index] = (key, new_item)
    hit.item = new_item
    hit.index = index
    _rebuild_map(hit.container)
    _sync_owner(hit.owner, hit.container)


def _flatten_descendants(table: Table, levels: int | None) -> None:
    """Flatten nested tables up to ``levels`` more levels (``None`` = unlimited)."""

    if levels is not None and levels <= 0:
        return
    next_levels = None if levels is None else levels - 1
    index = 0
    while index < len(table.value.body):
        key, item = table.value.body[index]
        if key is None or isinstance(item, (Whitespace, Comment, Null, AoT)):
            index += 1
            continue
        if isinstance(item, InlineTable):
            item = _swap_inline_child(table, index, item)
        if isinstance(item, Table):
            header = item.trivia.comment
            implicit = bool(header) and _implicit_super_comment(item, header)
            _retarget_key(table.value, index, dotted=True, owner=table)
            item._is_super_table = True
            item.display_name = None
            if header and not implicit:
                item.trivia.comment = ""
                item.trivia.comment_ws = ""
                _insert_entry(table.value, index, None, _comment_item(header), table)
                index += 1
            _flatten_descendants(item, next_levels)
        index += 1


def _swap_inline_child(table: Table, index: int, inline: InlineTable) -> Table:
    converted = _shallow_table_from_inline(inline)
    key = table.value.body[index][0]
    table.value.body[index] = (key, converted)
    _rebuild_map(table.value)
    _sync_owner(table, table.value)
    return converted


def _emit_header_comment(hit: _Hit, table: Table) -> None:
    comment, _ws = _own_header_comment(table)
    # Inline tables converted in place are not implicit ``[a.b]`` supers.
    # Their key comment is stored on trivia and must move in front.
    if not comment and table.name is None and table.trivia.comment:
        comment = table.trivia.comment
    if not comment:
        return
    table.trivia.comment = ""
    table.trivia.comment_ws = ""
    index = _index_of(hit.container, table)
    if index is None:
        return
    _insert_entry(hit.container, index, None, _comment_item(comment), hit.owner)
    _clear_duplicate_super_comments(hit, comment)


def _mark_dotted(hit: _Hit, table: Table) -> None:
    index = _index_of(hit.container, table)
    if index is not None and hit.container.body[index][0] is not None:
        _retarget_key(hit.container, index, dotted=True, owner=hit.owner)
    else:
        hit.key._dotted = True
    table._is_super_table = True
    table.display_name = None


def _implicit_super_comment(table: Table, comment: str) -> bool:
    """True when ``comment`` is the parser's copy of a nested header comment."""

    if not table.is_super_table() or table.display_name:
        return False
    for _, child in table.value.body:
        if isinstance(child, Table) and child.trivia.comment == comment:
            return True
    return False


def _ensure_standard_ancestors(
    doc: Container, parts: tuple[SingleKey, ...], key_path: str
) -> None:
    """Turn inline tables along ``parts`` into standard tables.

    Dotted keys inside an inline table cannot be grouped into a ``[header]``
    table until their parent is a standard table.
    """

    for depth in range(len(parts)):
        prefix = parts[: depth + 1]
        hits = _resolve(doc, prefix, key_path)
        if any(isinstance(hit.item, InlineTable) for hit in hits):
            to_standard_table(_format_path(prefix), doc)


def _style_grouped_table(
    table: Table,
    parts: tuple[SingleKey, ...],
    comment: str,
    comment_ws: str,
) -> None:
    table._is_super_table = False
    table.display_name = _format_path(parts)
    table.name = parts[-1].key
    table.trivia.comment = comment
    table.trivia.comment_ws = comment_ws
    if "\n" not in table.trivia.trail:
        table.trivia.trail = "\n"


def _super_table_anchor(
    hit: _Hit,
) -> tuple[Container, Table | InlineTable | None, list[Key]]:
    """Container that should receive the grouped table, plus the key chain.

    Dotted ancestors are included in the chain so the new ``[header]`` is a
    later sibling of any dotted keys that were not grouped. Rendering the
    header inside the old dotted entry would capture those siblings.
    """

    chain: list[Key] = [hit.key]
    current = hit
    while (
        current.parent is not None
        and current.parent.key.is_dotted()
        and isinstance(current.parent.item, Table)
    ):
        current = current.parent
        chain.append(current.key)
    chain.reverse()
    return current.container, current.owner, chain


def _wrap_super_chain(keys: list[Key], leaf: Table) -> tuple[Key, Table]:
    """Build the ``[a.b.c]`` super-table chain for ``keys`` ending at ``leaf``."""

    if len(keys) == 1:
        return _clean_single_key(keys[0], dotted=False, sep=""), leaf

    current: Table = leaf
    for depth in range(len(keys) - 2, -1, -1):
        parent_key = keys[depth]
        child_key = keys[depth + 1]
        wrapper = Table(
            Container(True),
            Trivia(trail="\n"),
            False,
            is_super_table=True,
            name=parent_key.key,
        )
        wrapper.raw_append(
            _clean_single_key(child_key, dotted=False, sep=""), current
        )
        current = wrapper
    return _clean_single_key(keys[0], dotted=False, sep=""), current


def _prune_empty_dotted(hit: _Hit | None) -> None:
    while hit is not None and hit.key.is_dotted() and isinstance(hit.item, Table):
        if _has_content(hit.item):
            return
        parent = hit.parent
        _delete_item(hit.container, hit.item, hit.owner)
        hit = parent


def _has_content(table: Table) -> bool:
    for _key, item in table.value.body:
        if not isinstance(item, (Null, Whitespace)):
            return True
    return False


def _take_preceding_comment(hit: _Hit) -> tuple[str, str]:
    current: _Hit | None = hit
    while current is not None:
        found = _preceding_comment(current.container, current.item)
        if found is not None:
            index, text = found
            _delete_at(current.container, index, current.owner)
            return text, " "
        parent = current.parent
        if (
            parent is None
            or not parent.key.is_dotted()
            or not isinstance(parent.item, Table)
            or not _is_first_content(parent.item, current.item)
        ):
            break
        current = parent
    return "", ""


def _preceding_comment(
    container: Container, item: Item
) -> tuple[int, str] | None:
    index = _index_of(container, item)
    if index is None:
        return None
    cursor = index - 1
    while cursor >= 0:
        key, previous = container.body[cursor]
        if isinstance(previous, (Null, Whitespace)):
            cursor -= 1
            continue
        if key is None and isinstance(previous, Comment) and previous.trivia.comment:
            return cursor, previous.trivia.comment
        break
    return None


def _is_first_content(table: Table, item: Item) -> bool:
    for _, child in table.value.body:
        if isinstance(child, (Null, Whitespace)):
            continue
        return child is item
    return False


def _absorb(dest: Table, src: Item) -> None:
    if isinstance(src, InlineTable):
        src_table = _shallow_table_from_inline(src)
    elif isinstance(src, Table):
        src_table = src
    else:
        return
    for key, item in list(src_table.value.body):
        if isinstance(item, Null):
            continue
        dest.raw_append(key, item)


def _delete_item(container: Container, item: Item, owner: Table | InlineTable | None) -> None:
    index = _index_of(container, item)
    if index is None:
        return
    _delete_at(container, index, owner)


def _delete_at(
    container: Container, index: int, owner: Table | InlineTable | None
) -> None:
    del container.body[index]
    _rebuild_map(container)
    _sync_owner(owner, container)


def _relocate_after_scalars(
    container: Container, table: Table, owner: Table | InlineTable | None
) -> None:
    """Move ``table`` past following scalars so its header does not capture them."""

    index = _index_of(container, table)
    if index is None:
        return
    last_scalar = None
    cursor = index + 1
    while cursor < len(container.body):
        key, item = container.body[cursor]
        if isinstance(item, (Table, AoT)) and item is not table:
            break
        if key is not None and not isinstance(item, (Table, AoT, Null)):
            last_scalar = cursor
        cursor += 1
    if last_scalar is None:
        return
    entry = container.body[index]
    segment = container.body[index + 1 : last_scalar + 1]
    container.body[index : last_scalar + 1] = [*segment, entry]
    _rebuild_map(container)
    _sync_owner(owner, container)


def _insert_entry(
    container: Container,
    index: int,
    key: Key | None,
    item: Item,
    owner: Table | InlineTable | None,
) -> None:
    container.body.insert(index, (key, item))
    _rebuild_map(container)
    _sync_owner(owner, container)


def _index_of(container: Container, item: Item) -> int | None:
    for index, (_, current) in enumerate(container.body):
        if current is item:
            return index
    return None


def _rebuild_map(container: Container) -> None:
    container._map.clear()
    alive: dict[str, Item] = {}
    for index, (key, item) in enumerate(container.body):
        if key is None:
            continue
        if key in container._map:
            previous = container._map[key]
            if isinstance(previous, tuple):
                container._map[key] = (*previous, index)
            else:
                container._map[key] = (previous, index)
        else:
            container._map[key] = index
        alive[key.key] = item
    for name in list(dict.keys(container)):
        if name not in alive:
            dict.__delitem__(container, name)
    for name, item in alive.items():
        dict.__setitem__(container, name, item.value)
    container._table_keys = [
        key for key, item in container.body if key is not None and isinstance(item, Table)
    ]


def _sync_owner(owner: Table | InlineTable | None, container: Container) -> None:
    if not isinstance(owner, (Table, InlineTable)):
        return
    alive: dict[str, Item] = {}
    for key, item in container.body:
        if key is not None:
            alive[key.key] = item
    for name in list(dict.keys(owner)):
        if name not in alive:
            dict.__delitem__(owner, name)
    for name, item in alive.items():
        dict.__setitem__(owner, name, item)
