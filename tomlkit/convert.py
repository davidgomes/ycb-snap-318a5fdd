"""Convert between header tables, inline tables, and dotted keys."""

from __future__ import annotations

from dataclasses import dataclass
from dataclasses import field

from tomlkit.container import Container
from tomlkit.exceptions import ConversionError
from tomlkit.exceptions import TOMLKitError
from tomlkit.items import AoT
from tomlkit.items import Array
from tomlkit.items import Comment
from tomlkit.items import DottedKey
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


@dataclass
class _Entry:
    container: Container
    owner: Container | Table | InlineTable
    index: int
    key: Key
    value: Item


@dataclass
class _Resolved:
    key_path: str
    entries: list[_Entry]


@dataclass
class _Match:
    container: Container
    owner: Container | Table | InlineTable
    index: int
    suffix: list[SingleKey]
    value: Item
    rel_keys: list[SingleKey]


@dataclass
class _Search:
    matches: list[_Match] = field(default_factory=list)
    blocked: bool = False


def to_inline_table(key_path: str, doc: Container | Table | InlineTable):
    """Convert the standard table at ``key_path`` into an inline table.

    The document is mutated in place and returned. Nested tables are converted
    recursively. Arrays of tables cannot be represented inline and raise
    :class:`~tomlkit.exceptions.ConversionError`.
    """
    resolved = _resolve(doc, key_path)
    values = [entry.value for entry in resolved.entries]
    if len(values) == 1 and isinstance(values[0], InlineTable):
        return doc

    if not values or not all(isinstance(value, Table) for value in values):
        _fail(key_path, "expected a standard table")

    tables = [value for value in values if isinstance(value, Table)]
    if any(_contains_aot(table) for table in tables):
        _fail(key_path, "an array of tables cannot be converted to an inline table")

    inline = _tables_to_inline(tables)
    source = next((table for table in tables if table.trivia.comment), tables[0])
    _copy_comment(inline, source.trivia)
    inline.trivia.trail = "\n"
    inline.trivia.indent = ""
    _replace_key(resolved, inline, header=False)
    return doc


def to_standard_table(key_path: str, doc: Container | Table | InlineTable):
    """Convert the inline table at ``key_path`` into a ``[header]`` table.

    The inline table's key comment becomes the table header comment. Nested
    inline tables are converted recursively. The document is mutated in place
    and returned.
    """
    resolved = _resolve(doc, key_path)
    values = [entry.value for entry in resolved.entries]
    if values and all(isinstance(value, Table) for value in values):
        return doc

    if len(values) != 1 or not isinstance(values[0], InlineTable):
        _fail(key_path, "expected an inline table")

    inline = values[0]
    assert isinstance(inline, InlineTable)
    table = _inline_to_standard(inline, recursive=True)
    table.name = resolved.entries[0].key.key
    _replace_key(resolved, table, header=True)
    _bubble_inline_ancestors(doc, table)
    return doc


def to_dotted_keys(
    key_path: str,
    doc: Container | Table | InlineTable,
    max_depth: int | None = None,
):
    """Flatten the table at ``key_path`` into dotted-key assignments.

    ``max_depth`` limits how many nested tables are flattened. ``None``
    flattens every level; ``1`` flattens only the table's immediate children.
    A standard table's header comment becomes a standalone comment before the
    first dotted key. The document is mutated in place and returned.
    """
    resolved = _resolve(doc, key_path)
    values = [entry.value for entry in resolved.entries]
    if not values or not all(
        isinstance(value, (Table, InlineTable)) for value in values
    ):
        _fail(key_path, "expected a table or inline table")

    plan: list[tuple] = []
    prefix = [_single(resolved.entries[0].key, sep="")]
    for value in values:
        if isinstance(value, (Table, InlineTable)) and value.trivia.comment:
            plan.append(("comment", value.trivia.comment))
        _flatten_into(value, prefix, max_depth, 1, plan)

    # An empty table has no dotted assignments. Leave it in place so the
    # empty mapping and its header comment survive.
    if not any(event[0] == "item" for event in plan):
        return doc

    owner = resolved.entries[0].owner
    _delete_key(owner, resolved.entries[0].key.key)
    _emit_plan(owner, plan)
    return doc


def to_super_table(dotted_prefix: str, doc: Container | Table | InlineTable):
    """Group dotted-key entries that share ``dotted_prefix`` into a table.

    A standalone comment immediately preceding the first match becomes the
    new table's header comment. The document is mutated in place and returned.
    """
    promoted: set[int] = set()
    while True:
        parts = _parse_key_path(dotted_prefix)
        search = _Search()
        _gather(_container_of(doc), doc, parts, search)
        if not search.matches:
            if search.blocked:
                _fail(dotted_prefix, "traverses a non-table value")
            _fail(dotted_prefix, "no matching dotted-key entries")

        inlines: list[InlineTable] = []
        seen: set[int] = set()
        for match in search.matches:
            owner = match.owner
            if not isinstance(owner, InlineTable) or id(owner) in promoted:
                continue
            if id(owner) in seen:
                continue
            seen.add(id(owner))
            inlines.append(owner)
        if not inlines:
            break

        progressed = False
        for inline in inlines:
            promoted.add(id(inline))
            if _promote_inline(doc, inline):
                progressed = True
        if not progressed:
            break

    groups: dict[tuple[int, tuple[str, ...]], list[_Match]] = {}
    for match in search.matches:
        token = (id(match.container), tuple(key.key for key in match.rel_keys))
        groups.setdefault(token, []).append(match)

    for matches in groups.values():
        _apply_super_group(matches)
    return doc


def _fail(key_path: str, reason: str) -> None:
    raise ConversionError(key_path, f"Cannot convert {key_path!r}: {reason}")


def _container_of(owner: Container | Table | InlineTable) -> Container:
    if isinstance(owner, (Table, InlineTable)):
        return owner.value
    return owner


def _parse_key_path(key_path: str) -> list[SingleKey]:
    if not isinstance(key_path, str) or not key_path.strip():
        raise ConversionError(
            key_path if isinstance(key_path, str) else str(key_path),
            f"Invalid key path {key_path!r}",
        )

    parser = Parser(key_path.strip())
    try:
        parsed = parser._parse_key()
    except TOMLKitError as exc:
        raise ConversionError(key_path, f"Invalid key path {key_path!r}") from exc

    if not parser.end():
        raise ConversionError(key_path, f"Invalid key path {key_path!r}")

    return [_single(part, sep="") for part in parsed]


def _resolve(doc: Container | Table | InlineTable, key_path: str) -> _Resolved:
    parts = _parse_key_path(key_path)
    nodes: list[tuple[Container, Container | Table | InlineTable]] = [
        (_container_of(doc), doc)
    ]
    for index, part in enumerate(parts):
        found: list[_Entry] = []
        for container, owner in nodes:
            found.extend(_match_part(container, owner, part))
        if not found:
            _fail(key_path, "key does not exist")

        if index == len(parts) - 1:
            containers = {id(entry.container) for entry in found}
            if len(containers) != 1:
                _fail(key_path, "key is not a single table")
            return _Resolved(key_path, found)

        nodes = []
        for entry in found:
            if not isinstance(entry.value, (Table, InlineTable)):
                _fail(key_path, "traverses a non-table value")
            nodes.append((entry.value.value, entry.value))

    _fail(key_path, "key does not exist")
    raise AssertionError


def _match_part(
    container: Container,
    owner: Container | Table | InlineTable,
    part: SingleKey,
) -> list[_Entry]:
    found = []
    for index, (key, value) in enumerate(container.body):
        if key is None or isinstance(value, Null) or key.key != part.key:
            continue
        found.append(_Entry(container, owner, index, key, value))
    return found


def _replace_key(resolved: _Resolved, item: Item, *, header: bool) -> None:
    entry = resolved.entries[0]
    original = entry.key
    name = original.key
    _delete_key(entry.owner, name)
    new_key = _single(original, sep="" if header else " = ")
    _store(entry.owner, new_key, item)


def _delete_key(owner: Container | Table | InlineTable, name: str) -> None:
    container = _container_of(owner)
    if name not in container:
        return
    del owner[name]


def _store(owner: Container | Table | InlineTable, key: Key, item: Item) -> None:
    if isinstance(owner, InlineTable):
        _store_inline(owner, key, item)
        return
    owner.append(key, item)


def _store_inline(owner: InlineTable, key: Key, item: Item) -> None:
    """Insert into an inline table without dropping comments or commas.

    Dotted keys are stored the way the parser stores them: a dotted head key
    whose value is a one-branch super table. Repeated heads stay as separate
    body entries so ``a.b`` and ``a.c`` both render.
    """
    if not isinstance(item, (Whitespace, Comment, Null)):
        _prepare_nested_trivia(item)
    if key.is_multi():
        parts = list(key)
        item = _super_under_head(parts, item, key.sep)
        key = _single(parts[0], sep="")
        key._dotted = True
    owner.value._raw_append(key, item)
    dict.__setitem__(owner, key.key, owner.value.item(key))
    _refresh_inline(owner)


def _super_under_head(parts: list[SingleKey], item: Item, sep: str) -> Table:
    """Build the super table that hangs off ``parts[0]``."""
    rest = parts[1:]
    node: Item = item
    node_key: Key = _single(rest[-1], sep=sep or " = ")
    for part in reversed(rest[:-1]):
        parent = Table(
            Container(True),
            Trivia(trail="\n"),
            False,
            is_super_table=True,
            name=part.key,
        )
        dotted = _single(part, sep="")
        dotted._dotted = True
        parent.raw_append(node_key, node)
        node = parent
        node_key = dotted
    table = Table(
        Container(True),
        Trivia(trail="\n"),
        False,
        is_super_table=True,
        name=parts[0].key,
    )
    table.raw_append(node_key, node)
    return table


def _contains_aot(value: Item) -> bool:
    if isinstance(value, AoT):
        return True
    if isinstance(value, (Table, InlineTable)):
        return any(
            isinstance(child, Item) and _contains_aot(child)
            for _, child in value.value.body
        )
    if isinstance(value, Array):
        return any(isinstance(child, Item) and _contains_aot(child) for child in value)
    return False


def _tables_to_inline(tables: list[Table]) -> InlineTable:
    order: list[str] = []
    grouped: dict[str, list[tuple[Key, Item]]] = {}
    notes: dict[str, list[Comment]] = {}
    for table in tables:
        pending: list[Comment] = []
        for key, value in table.value.body:
            if isinstance(value, Null):
                continue
            if key is None:
                if isinstance(value, Comment):
                    pending.append(value)
                continue
            if key.key not in grouped:
                order.append(key.key)
                grouped[key.key] = []
                notes[key.key] = []
            grouped[key.key].append((key, value))
            notes[key.key].extend(pending)
            pending = []

    pairs = [_merge_inline_key(grouped[name], notes[name]) for name in order]
    return _build_inline(pairs)


def _merge_inline_key(
    pairs: list[tuple[Key, Item]], comments: list[Comment]
) -> tuple[Key, Item]:
    key = pairs[0][0]
    values = [value for _, value in pairs]
    if len(values) == 1 and isinstance(values[0], InlineTable):
        item: Item = values[0]
    elif all(isinstance(value, Table) for value in values):
        tables = [value for value in values if isinstance(value, Table)]
        item = _tables_to_inline(tables)
        source = next((table for table in tables if table.trivia.comment), None)
        if source is not None:
            _copy_comment(item, source.trivia)
    else:
        item = values[-1]

    if comments and hasattr(item, "trivia") and not item.trivia.comment:
        item.trivia.comment = comments[-1].trivia.comment
        item.trivia.comment_ws = ""
    _prepare_nested_trivia(item)
    return _single(key, sep=" = "), item


def _build_inline(pairs: list[tuple[Key, Item]]) -> InlineTable:
    container = Container(True)
    for key, value in pairs:
        container._raw_append(key, value)
    return InlineTable(container, Trivia(), new=True)


def _inline_to_standard(inline: InlineTable, *, recursive: bool) -> Table:
    table = Table(Container(), Trivia(trail="\n"), False, is_super_table=False)
    _copy_comment(table, inline.trivia)
    if not table.trivia.trail:
        table.trivia.trail = "\n"

    pending: list[Comment] = []
    plain: list[tuple[list[Comment], Key, Item, bool]] = []
    headers: list[tuple[list[Comment], Key, Item]] = []
    for key, value in list(inline.value.body):
        if isinstance(value, Null):
            continue
        if key is None:
            if isinstance(value, Comment):
                pending.append(value)
            continue
        converted, is_header, dotted = _convert_inline_child(key, value, recursive)
        if is_header:
            headers.append((pending, key, converted))
        else:
            plain.append((pending, key, converted, dotted))
        pending = []

    for comments, key, converted, dotted in plain:
        _add_comments(table, comments)
        new_key = _single(key, sep="" if dotted else " = ", dotted=dotted)
        _seal_trivia(converted)
        table.append(new_key, converted)
    for comments, key, converted in headers:
        _add_comments(table, comments)
        new_key = _single(key, sep="")
        _seal_trivia(converted)
        table.append(new_key, converted)
    _add_comments(table, pending)
    return table


def _convert_inline_child(
    key: Key, value: Item, recursive: bool
) -> tuple[Item, bool, bool]:
    if isinstance(value, InlineTable) and recursive:
        return _inline_to_standard(value, recursive=True), True, False
    if isinstance(value, Table) and key.is_dotted():
        return value, False, True
    if isinstance(value, (Table, AoT)):
        return value, True, False
    return value, False, False


def _add_comments(table: Table, comments: list[Comment]) -> None:
    for comment in comments:
        table.add(comment)


def _bubble_inline_ancestors(
    doc: Container | Table | InlineTable, table: Table
) -> None:
    current: Item = table
    while True:
        parent = _find_parent(doc, current)
        if parent is None or not isinstance(parent[0], InlineTable):
            return
        inline, _key = parent
        promoted = _inline_to_standard(inline, recursive=False)
        promoted.name = _key.key if _key is not None else promoted.name
        grand = _find_parent(doc, inline)
        if grand is None:
            return
        grand_owner, grand_key = grand
        if grand_key is None:
            return
        _delete_key(grand_owner, grand_key.key)
        _store(grand_owner, _single(grand_key, sep=""), promoted)
        current = promoted


def _find_parent(
    node: Container | Table | InlineTable | AoT, target: Item
) -> tuple[Container | Table | InlineTable, Key] | None:
    container = node.value if isinstance(node, (Table, InlineTable)) else node
    if isinstance(node, AoT):
        for child in node.body:
            if child is target:
                return None
            found = _find_parent(child, target)
            if found is not None:
                return found
        return None

    if not isinstance(container, Container):
        return None

    for key, value in container.body:
        if (
            value is target
            and key is not None
            and isinstance(node, (Table, InlineTable, Container))
        ):
            return node, key
        if isinstance(value, (Table, InlineTable, AoT)):
            found = _find_parent(value, target)
            if found is not None:
                return found
    return None


def _flatten_into(
    value: Table | InlineTable,
    prefix: list[SingleKey],
    max_depth: int | None,
    depth: int,
    plan: list[tuple],
) -> None:
    for key, child in value.value.body:
        if isinstance(child, Null):
            continue
        if key is None:
            if isinstance(child, Comment):
                plan.append(("comment", child.trivia.comment))
            continue
        if key.is_dotted() and isinstance(child, Table):
            _flatten_into(
                child,
                [*prefix, _single(key, sep="")],
                max_depth,
                depth,
                plan,
            )
            continue
        _flatten_child(key, child, prefix, max_depth, depth, plan)


def _flatten_child(
    key: Key,
    child: Item,
    prefix: list[SingleKey],
    max_depth: int | None,
    depth: int,
    plan: list[tuple],
) -> None:
    child_prefix = [*prefix, _single(key, sep="")]
    can_flatten = isinstance(child, (Table, InlineTable)) and (
        max_depth is None or depth < max_depth
    )
    if can_flatten:
        if not _has_keys(child):
            top_key, top_value = _wrap_header(child_prefix, _empty_table(child))
            plan.append(("item", top_key, top_value))
            return
        if child.trivia.comment:
            plan.append(("comment", child.trivia.comment))
        _flatten_into(child, child_prefix, max_depth, depth + 1, plan)
        return
    if isinstance(child, (Table, AoT)):
        top_key, top_value = _wrap_header(child_prefix, child)
        plan.append(("item", top_key, top_value))
        return
    dotted = child_prefix[0] if len(child_prefix) == 1 else _dotted(child_prefix)
    if len(child_prefix) == 1:
        dotted = _single(child_prefix[0], sep=" = ")
    _ensure_kv_trivia(child)
    plan.append(("item", dotted, child))


def _emit_plan(owner: Container | Table | InlineTable, plan: list[tuple]) -> None:
    container = _container_of(owner)
    pending: list[str] = []
    for event in plan:
        if event[0] == "comment":
            pending.append(event[1])
            continue
        _key, item = event[1], event[2]
        _store(owner, _key, item)
        if not pending:
            continue
        anchor = _last_index_containing(container, id(item))
        if anchor is None:
            pending.clear()
            continue
        for offset, text in enumerate(pending):
            _insert_comment(container, anchor + offset, text)
        pending.clear()

    for text in pending:
        body = text.strip()
        if body and not body.startswith("#"):
            body = "# " + body
        # Inside an inline table the comment is rendered directly after the
        # previous value, so it needs a leading space.
        indent = " " if isinstance(owner, InlineTable) else ""
        owner.add(Comment(Trivia(indent=indent, comment=body, trail="\n")))


def _wrap_header(parts: list[SingleKey], leaf: Item) -> tuple[Key, Item]:
    if not isinstance(leaf, (Table, AoT)):
        if len(parts) == 1:
            return _single(parts[0], sep=" = "), leaf
        return _dotted(parts), leaf

    current = leaf
    current_key: Key = _single(parts[-1], sep="")
    if isinstance(current, Table):
        current.name = parts[-1].key
        _ensure_header_trivia(current)
    for part in reversed(parts[:-1]):
        parent = Table(
            Container(True),
            Trivia(trail="\n"),
            False,
            is_super_table=True,
            name=part.key,
        )
        parent.raw_append(current_key, current)
        current = parent
        current_key = _single(part, sep="")
    return current_key, current


def _gather(
    container: Container,
    owner: Container | Table | InlineTable,
    parts: list[SingleKey],
    search: _Search,
) -> None:
    if not parts:
        return

    head, *rest = parts
    for index, (key, value) in enumerate(container.body):
        if key is None or isinstance(value, Null) or key.key != head.key:
            continue
        _gather_entry(container, owner, index, key, value, rest, parts, search)


def _gather_entry(
    container: Container,
    owner: Container | Table | InlineTable,
    index: int,
    key: Key,
    value: Item,
    rest: list[SingleKey],
    parts: list[SingleKey],
    search: _Search,
) -> None:
    if key.is_dotted() and isinstance(value, Table):
        if rest:
            _gather_dotted(value.value, rest, container, owner, index, parts, search)
            return
        _collect_leaves(value.value, [], container, owner, index, parts, search)
        return

    if rest and isinstance(value, (Table, InlineTable)):
        _gather(value.value, value, rest, search)
        return

    if rest:
        search.blocked = True


def _gather_dotted(
    container: Container,
    parts: list[SingleKey],
    origin: Container,
    owner: Container | Table | InlineTable,
    origin_index: int,
    rel_keys: list[SingleKey],
    search: _Search,
) -> None:
    head, *rest = parts
    for key, value in container.body:
        if key is None or isinstance(value, Null) or key.key != head.key:
            continue
        if key.is_dotted() and isinstance(value, Table):
            if rest:
                _gather_dotted(
                    value.value, rest, origin, owner, origin_index, rel_keys, search
                )
            else:
                _collect_leaves(
                    value.value, [], origin, owner, origin_index, rel_keys, search
                )
            continue
        if rest and isinstance(value, (Table, InlineTable)):
            # The prefix continues through a concrete table that lives under
            # an intermediate dotted key. Record leaves relative to that table
            # by restarting the search there.
            _gather(value.value, value, rest, search)
            continue
        if rest:
            search.blocked = True


def _collect_leaves(
    container: Container,
    suffix: list[SingleKey],
    origin: Container,
    owner: Container | Table | InlineTable,
    origin_index: int,
    rel_keys: list[SingleKey],
    search: _Search,
) -> None:
    for key, value in container.body:
        if key is None or isinstance(value, Null):
            continue
        if key.is_dotted() and isinstance(value, Table):
            _collect_leaves(
                value.value,
                [*suffix, _single(key, sep="")],
                origin,
                owner,
                origin_index,
                rel_keys,
                search,
            )
            continue
        search.matches.append(
            _Match(
                origin,
                owner,
                origin_index,
                [*suffix, _single(key, sep="")],
                value,
                list(rel_keys),
            )
        )


def _apply_super_group(matches: list[_Match]) -> None:
    matches.sort(key=lambda match: match.index)
    container = matches[0].container
    owner = matches[0].owner
    rel_keys = matches[0].rel_keys
    first = min(match.index for match in matches)
    comment = _take_preceding_comment(container, first)

    if isinstance(owner, InlineTable):
        created: Item = _group_inline(matches)
    else:
        created = _group_table(matches)
    if comment is not None and hasattr(created, "trivia"):
        _copy_comment(created, comment.trivia)
        if isinstance(created, Table) and created.trivia.comment:
            if not created.trivia.comment_ws:
                created.trivia.comment_ws = " "
            created.trivia.indent = comment.trivia.indent
            created.trivia.trail = "\n"

    for index in sorted({match.index for match in matches}, reverse=True):
        _remove_index(owner, container, index)

    top_key, top_value = _wrap_header(rel_keys, created)
    if isinstance(owner, InlineTable) and isinstance(top_value, Table):
        # A header cannot live inside an inline table. Grouping already
        # returned an inline table when the owner itself is inline; this
        # branch only wraps multi-part prefixes.
        top_value = _super_chain_to_inline(rel_keys, matches)
        top_key = _single(rel_keys[0], sep=" = ")
        if comment is not None:
            _copy_comment(top_value, comment.trivia)
    _store(owner, top_key, top_value)


def _group_table(matches: list[_Match]) -> Table:
    table = Table(Container(), Trivia(trail="\n"), False, is_super_table=False)
    table.name = matches[0].rel_keys[-1].key
    for match in matches:
        _ensure_kv_trivia(match.value)
        if len(match.suffix) == 1:
            key = _single(match.suffix[0], sep=" = ")
            if isinstance(match.value, (Table, AoT)):
                key = _single(match.suffix[0], sep="")
            table.append(key, match.value)
            continue
        table.append(_dotted(match.suffix), match.value)
    return table


def _group_inline(matches: list[_Match]) -> InlineTable:
    root = _build_inline([])
    for match in matches:
        _put_inline_path(root, match.suffix, match.value)
    return root


def _super_chain_to_inline(
    rel_keys: list[SingleKey], matches: list[_Match]
) -> InlineTable:
    """Build nested inline tables for a multi-part prefix inside an inline table."""
    # rel_keys are the prefix; the value inserted at the owner is the first key,
    # and the remaining prefix plus suffixes live underneath it.
    root = _build_inline([])
    for match in matches:
        path = rel_keys[1:] + match.suffix
        _put_inline_path(root, path, match.value)
    return root


def _put_inline_path(table: InlineTable, path: list[SingleKey], value: Item) -> None:
    if len(path) == 1:
        _prepare_nested_trivia(value)
        _store(table, _single(path[0], sep=" = "), value)
        return
    head = path[0]
    existing = None
    for key, child in table.value.body:
        if key is not None and key.key == head.key and isinstance(child, InlineTable):
            existing = child
            break
    if existing is None:
        existing = _build_inline([])
        _store(table, _single(head, sep=" = "), existing)
    _put_inline_path(existing, path[1:], value)


def _take_preceding_comment(container: Container, index: int) -> Comment | None:
    cursor = index - 1
    while cursor >= 0 and isinstance(container.body[cursor][1], (Null, Whitespace)):
        cursor -= 1
    if cursor < 0:
        return None
    _key, value = container.body[cursor]
    if not isinstance(value, Comment):
        return None
    container._body[cursor] = (None, Null())
    return value


def _remove_index(
    owner: Container | Table | InlineTable, container: Container, index: int
) -> None:
    key = container.body[index][0]
    container._remove_at(index)
    if isinstance(owner, InlineTable):
        _refresh_inline(owner)
    if not isinstance(owner, (Table, InlineTable)) or key is None:
        return
    if key in container._map:
        dict.__setitem__(owner, key.key, container.item(key))
        return
    if key.key in owner:
        dict.__delitem__(owner, key.key)


def _insert_comment(container: Container, index: int, text: str) -> None:
    comment = Comment(Trivia(comment=text, trail="\n"))
    for key, position in list(container._map.items()):
        if isinstance(position, tuple):
            container._map[key] = tuple(
                item + 1 if item >= index else item for item in position
            )
        elif position >= index:
            container._map[key] = position + 1
    container._body.insert(index, (None, comment))


def _last_index_containing(container: Container, ident: int) -> int | None:
    found = None
    for index, (_key, value) in enumerate(container.body):
        if _contains_id(value, ident):
            found = index
    return found


def _contains_id(value: Item, ident: int) -> bool:
    if id(value) == ident:
        return True
    if isinstance(value, (Table, InlineTable)):
        return any(_contains_id(child, ident) for _, child in value.value.body)
    if isinstance(value, AoT):
        return any(_contains_id(child, ident) for child in value.body)
    return False


def _copy_comment(item: Item, source: Trivia) -> None:
    item.trivia.comment = source.comment
    item.trivia.comment_ws = source.comment_ws
    if item.trivia.comment and not item.trivia.comment_ws:
        item.trivia.comment_ws = " "


def _has_keys(value: Table | InlineTable) -> bool:
    return any(
        key is not None and not isinstance(child, Null)
        for key, child in value.value.body
    )


def _empty_table(source: Table | InlineTable) -> Table:
    table = Table(Container(), Trivia(trail="\n"), False, is_super_table=False)
    if source.trivia.comment:
        _copy_comment(table, source.trivia)
    return table


def _promote_inline(doc: Container | Table | InlineTable, inline: InlineTable) -> bool:
    """Turn an inline table into a header table so a sub-table can live in it."""
    parent = _find_parent(doc, inline)
    if parent is None:
        return False
    owner, key = parent
    table = _inline_to_standard(inline, recursive=False)
    table.name = key.key
    _delete_key(owner, key.key)
    _store(owner, _single(key, sep=""), table)
    _bubble_inline_ancestors(doc, table)
    return True


def _refresh_inline(table: InlineTable) -> None:
    """Let the inline renderer insert commas after structural edits.

    Parsed inline tables store commas as whitespace. After keys are removed
    or appended those commas no longer separate the surviving keys.
    """
    table._new = True
    for _, value in table.value.body:
        if isinstance(value, Whitespace) and "," in value.s:
            value._s = value.s.replace(",", "")


def _seal_trivia(item: Item) -> None:
    """Give every rendered value a trailing newline outside inline tables."""
    if isinstance(item, (Whitespace, Comment, Null)):
        return
    if isinstance(item, (Table, InlineTable)):
        for _, child in item.value.body:
            if isinstance(child, Item):
                _seal_trivia(child)
        if isinstance(item, Table):
            _ensure_header_trivia(item)
        return
    if isinstance(item, AoT):
        for child in item.body:
            _seal_trivia(child)
        return
    _ensure_kv_trivia(item)


def _prepare_nested_trivia(item: Item) -> None:
    """Normalize trivia for a value nested inside an inline table.

    Inline tables render a value's comment before the next comma or ``}`` and
    drop newlines from the trail. The comment text itself must end with a
    newline so it does not swallow that delimiter.
    """
    if isinstance(item, (Whitespace, Comment, Null)) or not hasattr(item, "trivia"):
        return
    trivia = item.trivia
    if trivia.comment:
        # Inline tables render the comment after the value and strip newlines
        # from the trail, so the comment itself must end with a newline.
        # Otherwise ``# comment}`` swallows the closing brace.
        text = trivia.comment.strip()
        if text and not text.startswith("#"):
            text = "# " + text
        trivia.comment = f" {text}\n"
        trivia.comment_ws = ""
        trivia.trail = ""
    elif not isinstance(item, (Table, AoT)):
        trivia.trail = ""
    if not isinstance(item, (Table, AoT)):
        trivia.indent = ""


def _ensure_kv_trivia(item: Item) -> None:
    if isinstance(item, (Whitespace, Comment, Null, Table, AoT)) or not hasattr(
        item, "trivia"
    ):
        return
    if item.trivia.indent.strip(" ") == "" or "\n" not in item.trivia.indent:
        item.trivia.indent = ""
    if not item.trivia.trail:
        item.trivia.trail = "\n"


def _ensure_header_trivia(item: Item) -> None:
    if isinstance(item, Table) and not item.trivia.trail:
        item.trivia.trail = "\n"


def _single(key: Key, *, sep: str | None = None, dotted: bool = False) -> SingleKey:
    key_type = key.t if isinstance(key, SingleKey) else None
    created = SingleKey(key.key, t=key_type, sep=sep)
    created._dotted = dotted
    return created


def _dotted(parts: list[SingleKey], sep: str = " = ") -> DottedKey:
    return DottedKey([_single(part, sep="") for part in parts], sep=sep)
