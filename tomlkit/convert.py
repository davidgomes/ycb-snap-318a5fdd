from __future__ import annotations

from dataclasses import dataclass
from dataclasses import field
from typing import TypeVar

from tomlkit.container import Container
from tomlkit.container import OutOfOrderTableProxy
from tomlkit.exceptions import ConversionError
from tomlkit.exceptions import TOMLKitError
from tomlkit.items import AoT
from tomlkit.items import Array
from tomlkit.items import Comment
from tomlkit.items import InlineTable
from tomlkit.items import Item
from tomlkit.items import Key
from tomlkit.items import SingleKey
from tomlkit.items import Table
from tomlkit.items import Trivia
from tomlkit.items import Whitespace
from tomlkit.parser import Parser


D = TypeVar("D", bound=Container)


@dataclass
class _Located:
    container: Container
    owner: Table | InlineTable | None
    index: int | tuple[int, ...]
    key: Key
    item: Item


@dataclass
class _KV:
    key: SingleKey
    value: Item
    comment: str = ""
    comment_ws: str = ""


@dataclass
class _Leaf:
    parts: list[str]
    keys: list[SingleKey]
    value: Item
    comments: list[Comment] = field(default_factory=list)
    as_header: bool = False


def to_inline_table(key_path: str, doc: D) -> D:
    """Convert the standard table at ``key_path`` into an inline table.

    The document is mutated in place and returned. Nested tables become nested
    inline tables. The call is a no-op when the value is already an inline table.
    """
    located = _locate(doc, key_path)
    item = located.item
    if isinstance(item, InlineTable):
        return doc
    if not isinstance(item, Table) or isinstance(located.index, tuple):
        raise ConversionError(key_path, f"{key_path!r} is not a standard table")
    if _contains_aot(item):
        raise ConversionError(
            key_path, f"{key_path!r} contains an array of tables and cannot be inlined"
        )

    inline = _table_to_inline(item)
    _replace_at(located, _clean_key(located.key), inline)
    return doc


def to_standard_table(key_path: str, doc: D) -> D:
    """Convert the inline table at ``key_path`` into a standard ``[header]`` table.

    The document is mutated in place and returned. The inline table's trailing
    comment becomes the table header comment. Nested inline tables become nested
    standard tables. The call is a no-op when the value is already a standard table.
    """
    located = _locate(doc, key_path)
    item = located.item
    if isinstance(item, Table) and not isinstance(located.index, tuple):
        return doc
    if not isinstance(item, InlineTable) or isinstance(located.index, tuple):
        raise ConversionError(key_path, f"{key_path!r} is not an inline table")

    name = _clean_key(located.key).key
    table = _inline_to_table(item, name=name)
    _replace_at(located, _clean_key(located.key), table)
    return doc


def to_dotted_keys(key_path: str, doc: D, max_depth: int | None = None) -> D:
    """Flatten the table at ``key_path`` into dotted-key assignments.

    ``max_depth`` limits how many nested table levels are opened. ``None`` flattens
    every level and ``1`` keeps immediate child tables intact. The table header
    comment becomes a standalone comment before the first dotted key.
    """
    if max_depth is not None and (not isinstance(max_depth, int) or max_depth < 1):
        raise ConversionError(key_path, "max_depth must be a positive integer or None")

    located = _locate(doc, key_path)
    item = located.item
    if not isinstance(item, (Table, InlineTable)) or isinstance(located.index, tuple):
        raise ConversionError(key_path, f"{key_path!r} is not a table")

    prefix = _clean_key(located.key)
    entries = _flatten(item, [prefix], max_depth)
    owns_header = (
        isinstance(item, Table) and not item.is_super_table() and item.trivia.comment
    ) or (isinstance(item, InlineTable) and item.trivia.comment)
    header = _comment_from_trivia(item.trivia) if owns_header else None

    body: list[tuple[Key | None, Item]] = []
    if header is not None:
        body.append((None, header))
    body.extend(_entries_to_body(entries))
    _splice(located, body)
    return doc


def to_super_table(dotted_prefix: str, doc: D) -> D:
    """Group dotted-key assignments that share ``dotted_prefix`` into a table.

    A standalone comment immediately before the first match becomes the new table's
    header comment. Raises :class:`ConversionError` when nothing matches.
    """
    prefix_keys = _parse_key_path(dotted_prefix)
    prefix_parts = [key.key for key in prefix_keys]
    containers = list(_iter_real_containers(doc, None))
    found = False
    for container, owner in containers:
        if _regroup(container, owner, prefix_parts, prefix_keys):
            found = True
    if not found:
        raise ConversionError(
            dotted_prefix, f"No dotted keys found for {dotted_prefix!r}"
        )
    return doc


def _parse_key_path(key_path: str) -> list[SingleKey]:
    if not isinstance(key_path, str) or key_path == "":
        raise ConversionError(
            "" if not isinstance(key_path, str) else key_path,
            "A dotted key path is required",
        )
    parser = Parser(key_path)
    try:
        key = parser._parse_key()
        if not parser.end():
            raise ConversionError(key_path, f"Invalid key path {key_path!r}")
    except ConversionError:
        raise
    except TOMLKitError as exc:
        raise ConversionError(key_path, f"Invalid key path {key_path!r}") from exc
    return [_clean_key(part) for part in key]


def _locate(doc: Container, key_path: str) -> _Located:
    parts = [key.key for key in _parse_key_path(key_path)]
    return _locate_parts(doc, None, parts, key_path)


def _locate_parts(
    container: Container,
    owner: Table | InlineTable | None,
    parts: list[str],
    key_path: str,
) -> _Located:
    idx = container._map.get(SingleKey(parts[0]))
    if idx is None:
        raise ConversionError(key_path, f"Key {key_path!r} does not exist")

    if isinstance(idx, tuple):
        if len(parts) == 1:
            proxy = OutOfOrderTableProxy(container, idx)
            return _Located(container, owner, idx, SingleKey(parts[0]), proxy)  # type: ignore[arg-type]
        for fragment_index in idx:
            fragment = container._body[fragment_index][1]
            if _can_descend(fragment, parts[1]):
                assert isinstance(fragment, (Table, InlineTable))
                return _locate_parts(fragment.value, fragment, parts[1:], key_path)
        if not any(
            isinstance(container._body[i][1], (Table, InlineTable)) for i in idx
        ):
            raise ConversionError(key_path, f"Key {key_path!r} is not a table")
        raise ConversionError(key_path, f"Key {key_path!r} does not exist")

    body_key, item = container._body[idx]
    if len(parts) == 1:
        return _Located(
            container, owner, idx, body_key if body_key else SingleKey(parts[0]), item
        )
    if not isinstance(item, (Table, InlineTable)):
        raise ConversionError(key_path, f"Key {key_path!r} is not a table")
    return _locate_parts(item.value, item, parts[1:], key_path)


def _can_descend(item: Item, part: str) -> bool:
    if not isinstance(item, (Table, InlineTable)):
        return False
    return SingleKey(part) in item.value._map


def _contains_aot(item: Item) -> bool:
    if isinstance(item, AoT):
        return True
    if isinstance(item, (Table, InlineTable)):
        return any(_contains_aot(child) for _, child in item.value.body)
    if isinstance(item, Array):
        return any(_contains_aot(child) for child in item)
    return False


def _clean_key(key: Key) -> SingleKey:
    part = key if isinstance(key, SingleKey) else next(iter(key))
    return SingleKey(part.key, t=part.t, sep=" = ")


def _comment_from_trivia(trivia: Trivia) -> Comment:
    return Comment(
        Trivia(
            indent="",
            comment_ws="",
            comment=trivia.comment,
            trail="\n",
        )
    )


def _visible_header(table: Table) -> tuple[str, str]:
    if table.is_super_table():
        return "", ""
    return table.trivia.comment, table.trivia.comment_ws


def _table_to_inline(table: Table) -> InlineTable:
    entries = _collect_inline_entries(table.value)
    comment, comment_ws = _visible_header(table)
    trivia = Trivia(
        indent=table.trivia.indent,
        comment_ws=comment_ws or (" " if comment else ""),
        comment=comment,
        trail=table.trivia.trail if "\n" in table.trivia.trail else "\n",
    )
    return _build_inline(entries, trivia)


def _collect_inline_entries(container: Container) -> list[Comment | _KV]:
    entries: list[Comment | _KV] = []
    for key, value in container.body:
        if key is None:
            if isinstance(value, Comment):
                entries.append(value)
            continue
        if isinstance(value, Table):
            nested = _table_to_inline(value)
            header, header_ws = _visible_header(value)
            nested.trivia.comment = ""
            nested.trivia.comment_ws = ""
            nested.trivia.trail = ""
            nested.trivia.indent = ""
            entries.append(_KV(_clean_key(key), nested, header, header_ws or " "))
            continue
        if isinstance(value, InlineTable):
            comment = value.trivia.comment
            comment_ws = value.trivia.comment_ws or " "
            value.trivia.comment = ""
            value.trivia.comment_ws = ""
            value.trivia.trail = ""
            value.trivia.indent = ""
            entries.append(_KV(_clean_key(key), value, comment, comment_ws))
            continue
        comment = ""
        comment_ws = " "
        if hasattr(value, "trivia"):
            comment = value.trivia.comment
            comment_ws = value.trivia.comment_ws or " "
            value.trivia.comment = ""
            value.trivia.comment_ws = ""
            value.trivia.trail = ""
            value.trivia.indent = ""
        entries.append(_KV(_clean_key(key), value, comment, comment_ws))
    return _merge_duplicate_inline_keys(entries)


def _merge_duplicate_inline_keys(
    entries: list[Comment | _KV],
) -> list[Comment | _KV]:
    merged: list[Comment | _KV] = []
    index_by_key: dict[str, int] = {}
    for entry in entries:
        if isinstance(entry, Comment):
            merged.append(entry)
            continue
        previous = index_by_key.get(entry.key.key)
        if (
            previous is not None
            and isinstance(merged[previous], _KV)
            and isinstance(merged[previous].value, InlineTable)
            and isinstance(entry.value, InlineTable)
        ):
            _append_inline_contents(merged[previous].value, entry.value)
            if entry.comment and not merged[previous].comment:
                merged[previous].comment = entry.comment
                merged[previous].comment_ws = entry.comment_ws
            continue
        index_by_key[entry.key.key] = len(merged)
        merged.append(entry)
    return merged


def _append_inline_contents(dest: InlineTable, src: InlineTable) -> None:
    for key, value in src.value.body:
        dest.value._raw_append(key, value)
        if key is not None:
            dict.__setitem__(dest, key.key, value)
            dict.__setitem__(dest.value, key.key, value.value)


def _build_inline(entries: list[Comment | _KV], trivia: Trivia) -> InlineTable:
    commented = any(
        isinstance(entry, Comment) or (isinstance(entry, _KV) and entry.comment)
        for entry in entries
    )
    container = Container(True)
    if not commented:
        for entry in entries:
            if isinstance(entry, _KV):
                container._raw_append(entry.key, entry.value)
        inline = InlineTable(container, trivia, new=True)
        _sync_owner(inline, container)
        return inline

    for entry in entries:
        if isinstance(entry, Comment):
            if container.body and not _previous_ends_with_newline(container):
                container._raw_append(None, Whitespace("\n"))
            if "\n" not in entry.trivia.trail:
                entry.trivia.trail += "\n"
            container._raw_append(None, entry)
            continue
        if _has_real_key(container):
            if not _ends_with_comma(container):
                container._raw_append(None, Whitespace(","))
            if not _previous_ends_with_newline(container):
                container._raw_append(None, Whitespace("\n"))
        container._raw_append(entry.key, entry.value)
        if entry.comment:
            container._raw_append(None, Whitespace(","))
            container._raw_append(None, Whitespace(entry.comment_ws or " "))
            container._raw_append(
                None,
                Comment(Trivia(comment=entry.comment, trail="\n")),
            )
    inline = InlineTable(container, trivia, new=False)
    _sync_owner(inline, container)
    return inline


def _has_real_key(container: Container) -> bool:
    return any(key is not None for key, _ in container.body)


def _ends_with_comma(container: Container) -> bool:
    for key, value in reversed(container.body):
        if key is not None:
            return False
        if isinstance(value, Whitespace) and "," in value.s:
            return True
    return False


def _previous_ends_with_newline(container: Container) -> bool:
    if not container.body:
        return False
    value = container.body[-1][1]
    if isinstance(value, Whitespace):
        return value.s.endswith("\n")
    if isinstance(value, Comment):
        return value.trivia.trail.endswith("\n")
    if hasattr(value, "trivia"):
        return value.trivia.trail.endswith("\n")
    return False


def _inline_to_table(inline: InlineTable, name: str | None) -> Table:
    container = Container(True)
    table = Table(
        container,
        Trivia(
            indent=inline.trivia.indent,
            comment_ws=inline.trivia.comment_ws
            or (" " if inline.trivia.comment else ""),
            comment=inline.trivia.comment,
            trail="\n",
        ),
        False,
        is_super_table=False,
        name=name,
    )
    _fill_table_from_inline(table, inline.value)
    _sync_owner(table, container)
    return table


def _fill_table_from_inline(table: Table, source: Container) -> None:
    pending_comment: Comment | None = None
    index = 0
    body = list(source.body)
    while index < len(body):
        key, value = body[index]
        if key is None:
            if isinstance(value, Comment):
                pending_comment = value
            index += 1
            continue

        comment, consumed = _following_inline_comment(body, index)
        index = consumed
        if isinstance(value, InlineTable):
            header = ""
            header_ws = ""
            if value.trivia.comment:
                header = value.trivia.comment
                header_ws = value.trivia.comment_ws
            elif comment is not None:
                header = comment.trivia.comment
                header_ws = comment.trivia.comment_ws or " "
                comment = None
            nested = _inline_to_table(value, name=key.key)
            if header:
                nested.trivia.comment = header
                nested.trivia.comment_ws = header_ws or " "
            _append_table_child(table, _clean_key(key), nested, pending_comment)
            pending_comment = None
            if comment is not None:
                _append_table_child(table, None, _normalize_comment(comment), None)
            continue

        if isinstance(value, Table):
            _append_table_child(table, _clean_key(key), value, pending_comment)
        else:
            _prepare_assignment_value(value)
            if (
                comment is not None
                and hasattr(value, "trivia")
                and not value.trivia.comment
            ):
                value.trivia.comment = comment.trivia.comment
                value.trivia.comment_ws = comment.trivia.comment_ws or " "
                comment = None
            _append_table_child(table, _clean_key(key), value, pending_comment)
        pending_comment = None
        if comment is not None:
            _append_table_child(table, None, _normalize_comment(comment), None)


def _following_inline_comment(
    body: list[tuple[Key | None, Item]], start: int
) -> tuple[Comment | None, int]:
    """Return a same-line trailing comment and the index after it.

    Newlines before a comment keep it as a standalone comment for the main loop.
    """
    index = start + 1
    saw_newline = False
    while index < len(body):
        key, value = body[index]
        if key is not None:
            break
        if isinstance(value, Whitespace):
            if "\n" in value.s:
                saw_newline = True
            index += 1
            continue
        if isinstance(value, Comment) and not saw_newline:
            return value, index + 1
        break
    return None, start + 1


def _append_table_child(
    table: Table,
    key: Key | None,
    item: Item,
    pending_comment: Comment | None,
) -> None:
    if pending_comment is not None:
        table.value._raw_append(None, _normalize_comment(pending_comment))
    if isinstance(item, Table) and table.value.body and not _ends_with_blank(table):
        table.value._raw_append(None, Whitespace("\n"))
    table.value._raw_append(key, item)
    if key is not None:
        dict.__setitem__(table, key.key, item)
        dict.__setitem__(table.value, key.key, item.value)


def _ends_with_blank(table: Table) -> bool:
    previous = table.value._previous_item()
    if isinstance(previous, Whitespace):
        return "\n" in previous.s
    if previous is None or not hasattr(previous, "trivia"):
        return False
    return (
        previous.trivia.trail.endswith("\n\n") or previous.trivia.trail.count("\n") > 1
    )


def _normalize_comment(comment: Comment) -> Comment:
    if "\n" not in comment.trivia.trail:
        comment.trivia.trail += "\n"
    return comment


def _prepare_assignment_value(item: Item) -> None:
    if not hasattr(item, "trivia") or isinstance(item, (Whitespace, Comment)):
        return
    if item.trivia.comment and not item.trivia.comment_ws:
        item.trivia.comment_ws = " "
    if "\n" not in item.trivia.trail:
        item.trivia.trail += "\n"


def _flatten(
    table: Table | InlineTable, prefix: list[SingleKey], budget: int | None
) -> list[_Leaf]:
    leaves: list[_Leaf] = []
    pending: list[Comment] = []
    _flatten_container(table.value, prefix, budget, leaves, pending)
    if pending and leaves:
        leaves[-1].comments.extend(pending)
    return leaves


def _flatten_container(
    container: Container,
    prefix: list[SingleKey],
    budget: int | None,
    leaves: list[_Leaf],
    pending: list[Comment],
) -> None:
    for key, value in container.body:
        if key is None:
            if isinstance(value, Comment):
                pending.append(_normalize_comment(value))
            continue
        if key.is_dotted() and isinstance(value, Table):
            _flatten_container(
                value.value, [*prefix, _clean_key(key)], budget, leaves, pending
            )
            continue
        child_keys = [*prefix, _clean_key(key)]
        if isinstance(value, (Table, InlineTable)) and _opens_child(budget):
            child_pending = _take_pending(pending)
            header = _header_before_children(value)
            if header is not None:
                child_pending.insert(0, header)
            next_budget = None if budget is None else budget - 1
            _flatten_container(
                value.value, child_keys, next_budget, leaves, child_pending
            )
            if child_pending and leaves:
                leaves[-1].comments.extend(child_pending)
                child_pending.clear()
            elif child_pending:
                pending.extend(child_pending)
            continue
        leaf_value, as_header = _leaf_value(value)
        leaves.append(
            _Leaf(
                parts=[part.key for part in child_keys],
                keys=child_keys,
                value=leaf_value,
                comments=_take_pending(pending),
                as_header=as_header,
            )
        )


def _opens_child(budget: int | None) -> bool:
    return budget is None or budget > 1


def _take_pending(pending: list[Comment]) -> list[Comment]:
    taken = list(pending)
    pending.clear()
    return taken


def _header_before_children(value: Table | InlineTable) -> Comment | None:
    if isinstance(value, Table):
        comment, _ = _visible_header(value)
        if comment:
            return _comment_from_trivia(value.trivia)
        return None
    if value.trivia.comment:
        return _comment_from_trivia(value.trivia)
    return None


def _leaf_value(value: Item) -> tuple[Item, bool]:
    if isinstance(value, (Table, AoT)):
        return value, True
    return value, False


def _entries_to_body(leaves: list[_Leaf]) -> list[tuple[Key | None, Item]]:
    body: list[tuple[Key | None, Item]] = []
    for leaf in leaves:
        for comment in leaf.comments:
            body.append((None, _normalize_comment(comment)))
        if leaf.as_header:
            key, item = _nest_header(leaf.keys, leaf.value)
        else:
            _prepare_assignment_value(leaf.value)
            key, item = _nest_dotted(leaf.keys, leaf.value)
        body.append((key, item))
    return body


def _nest_header(parts: list[SingleKey], leaf: Item) -> tuple[Key, Item]:
    if isinstance(leaf, Table):
        leaf._is_super_table = False
        leaf.name = parts[-1].key
        if "\n" not in leaf.trivia.trail:
            leaf.trivia.trail += "\n"
    current: Item = leaf
    for depth in range(len(parts) - 2, -1, -1):
        parent_name = parts[depth]
        child_name = parts[depth + 1]
        current = _wrap_super(parent_name, child_name, current)
    return SingleKey(parts[0].key, t=parts[0].t), current


def _nest_dotted(parts: list[SingleKey], value: Item) -> tuple[Key, Item]:
    _prepare_assignment_value(value)
    leaf_key = SingleKey(parts[-1].key, t=parts[-1].t, sep=" = ")
    if len(parts) == 1:
        return leaf_key, value

    current_key: Key = leaf_key
    current_val: Item = value
    for name in reversed(parts[:-1]):
        dotted_child = isinstance(current_val, Table) and current_val.is_super_table()
        if dotted_child:
            current_key._dotted = True  # type: ignore[attr-defined]
        current_val = _wrap_super(name, current_key, current_val)
        current_key = SingleKey(name.key, t=name.t)
        current_key._dotted = True
    return current_key, current_val


def _wrap_super(parent_name: SingleKey, child_key: Key, child: Item) -> Table:
    container = Container(True)
    parent = Table(
        container,
        Trivia(trail="\n"),
        False,
        is_super_table=True,
        name=parent_name.key,
    )
    container._raw_append(child_key, child)
    _sync_owner(parent, container)
    return parent


def _iter_real_containers(container: Container, owner: Table | InlineTable | None):
    yield container, owner
    for key, value in list(container.body):
        if isinstance(value, Table) and not (key is not None and key.is_dotted()):
            yield from _iter_real_containers(value.value, value)
        elif isinstance(value, AoT):
            for table in value.body:
                yield from _iter_real_containers(table.value, table)


def _regroup(
    container: Container,
    owner: Table | InlineTable | None,
    prefix_parts: list[str],
    prefix_keys: list[SingleKey],
) -> bool:
    new_body: list[tuple[Key | None, Item] | None] = []
    grouped: list[_Leaf] = []
    insert_at: int | None = None
    header: Comment | None = None

    for key, value in list(container.body):
        if key is not None and key.is_dotted() and isinstance(value, Table):
            leaves = _dotted_leaves(key, value)
            matching = [
                leaf
                for leaf in leaves
                if leaf.parts[: len(prefix_parts)] == prefix_parts
                and len(leaf.parts) > len(prefix_parts)
            ]
            if not matching:
                new_body.append((key, value))
                continue
            if insert_at is None:
                header = _pop_immediate_comment(new_body)
                insert_at = len(new_body)
                new_body.append(None)
            nonmatching = [leaf for leaf in leaves if leaf not in matching]
            grouped.extend(matching)
            for leaf in nonmatching:
                new_body.extend(_entries_to_body([leaf]))
            continue
        new_body.append((key, value))

    if insert_at is None:
        return False

    table = _table_from_leaves(prefix_keys, grouped, header)
    top_key, top_item = _nest_header(prefix_keys, table)
    new_body[insert_at] = (top_key, top_item)
    container._body = [entry for entry in new_body if entry is not None]
    _finalize(container, owner)
    return True


def _dotted_leaves(top_key: Key, table: Table) -> list[_Leaf]:
    leaves: list[_Leaf] = []

    def walk(current: Table, keys: list[SingleKey], pending: list[Comment]) -> None:
        local = list(pending)
        for key, value in current.value.body:
            if key is None:
                if isinstance(value, Comment):
                    local.append(value)
                continue
            child_keys = [*keys, _clean_key(key)]
            if isinstance(value, Table) and key.is_dotted():
                walk(value, child_keys, local)
                local = []
                continue
            leaves.append(
                _Leaf(
                    parts=[part.key for part in child_keys],
                    keys=child_keys,
                    value=value,
                    comments=local,
                )
            )
            local = []

    walk(table, [_clean_key(top_key)], [])
    return leaves


def _pop_immediate_comment(
    body: list[tuple[Key | None, Item] | None],
) -> Comment | None:
    index = len(body) - 1
    while index >= 0:
        entry = body[index]
        if entry is None or not isinstance(entry[1], Whitespace):
            break
        index -= 1
    if index < 0:
        return None
    entry = body[index]
    if entry is None or not isinstance(entry[1], Comment):
        return None
    comment = entry[1]
    del body[index:]
    return comment


def _table_from_leaves(
    prefix_keys: list[SingleKey], leaves: list[_Leaf], header: Comment | None
) -> Table:
    container = Container(True)
    comment = header.trivia.comment if header is not None else ""
    table = Table(
        container,
        Trivia(
            comment_ws=" " if comment else "",
            comment=comment,
            trail="\n",
        ),
        False,
        is_super_table=False,
        name=prefix_keys[-1].key,
    )
    for leaf in leaves:
        for note in leaf.comments:
            container._raw_append(None, _normalize_comment(note))
        relative = leaf.keys[len(prefix_keys) :]
        if len(relative) == 1:
            _prepare_assignment_value(leaf.value)
            container._raw_append(relative[0], leaf.value)
            continue
        key, item = _nest_dotted(relative, leaf.value)
        container._raw_append(key, item)
    _sync_owner(table, container)
    return table


def _replace_at(located: _Located, key: Key, item: Item) -> None:
    if isinstance(located.index, tuple):
        raise ConversionError(key.key, f"{key.key!r} is not a single table")
    located.container._body[located.index] = (key, item)
    _finalize(located.container, located.owner)


def _splice(located: _Located, entries: list[tuple[Key | None, Item]]) -> None:
    if isinstance(located.index, tuple):
        raise ConversionError(
            located.key.key, f"{located.key.key!r} is not a single table"
        )
    located.container._body[located.index : located.index + 1] = entries
    _finalize(located.container, located.owner)


def _sync_owner(owner: Table | InlineTable, container: Container) -> None:
    if isinstance(owner, Table):
        _ensure_toml_order(container)
    _reindex(container, owner)


def _finalize(container: Container, owner: Table | InlineTable | None) -> None:
    if not isinstance(owner, InlineTable):
        _ensure_toml_order(container)
    _reindex(container, owner)


def _emits_header(key: Key | None, item: Item) -> bool:
    if isinstance(item, AoT):
        return True
    if not isinstance(item, Table):
        return False
    return not (key is not None and key.is_dotted() and _is_dotted_assignment(item))


def _is_dotted_assignment(table: Table) -> bool:
    if not table.is_super_table():
        return False
    for key, value in table.value.body:
        if key is None:
            continue
        if isinstance(value, AoT):
            return False
        if isinstance(value, Table) and not (
            key.is_dotted() and _is_dotted_assignment(value)
        ):
            return False
    return True


def _ensure_toml_order(container: Container) -> None:
    """Keep header tables after direct assignments so re-parsing preserves values."""
    blocks: list[tuple[list[tuple[Key | None, Item]], tuple[Key | None, Item]]] = []
    trivia: list[tuple[Key | None, Item]] = []
    for entry in container.body:
        key, item = entry
        if key is None and isinstance(item, (Whitespace, Comment)):
            trivia.append(entry)
            continue
        blocks.append((trivia, entry))
        trivia = []

    plain = []
    tables = []
    for block in blocks:
        entry = block[1]
        if _emits_header(entry[0], entry[1]):
            tables.append(block)
        else:
            plain.append(block)

    body: list[tuple[Key | None, Item]] = []
    for trivia_part, entry in [*plain, *tables]:
        body.extend(trivia_part)
        body.append(entry)
    body.extend(trivia)
    container._body = body


def _reindex(container: Container, owner: Table | InlineTable | None) -> None:
    container._map.clear()
    container._table_keys = []
    for key in list(dict.keys(container)):
        dict.__delitem__(container, key)
    if owner is not None:
        for key in list(dict.keys(owner)):
            dict.__delitem__(owner, key)

    for index, (key, item) in enumerate(container._body):
        if key is None:
            continue
        previous = container._map.get(key)
        if previous is None:
            container._map[key] = index
        elif isinstance(previous, tuple):
            container._map[key] = (*previous, index)
        else:
            container._map[key] = (previous, index)
        if isinstance(item, Table):
            container._table_keys.append(key)
        dict.__setitem__(container, key.key, item.value)
        if owner is not None:
            dict.__setitem__(owner, key.key, item)
