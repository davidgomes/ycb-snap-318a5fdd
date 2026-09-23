"""Convert between standard tables, inline tables, and dotted keys."""

from __future__ import annotations

from typing import TypeVar

from tomlkit.container import Container
from tomlkit.exceptions import ConversionError
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


T = TypeVar("T", bound=Container)


def to_inline_table(key_path: str, doc: T) -> T:
    """Convert the standard table at ``key_path`` into an inline table.

    The document is mutated in place and returned. Nested tables become nested
    inline tables. An existing inline table is left unchanged.
    """
    _owner, container, idx, value, ancestors = _resolve(doc, key_path)
    if isinstance(value, InlineTable):
        return doc
    if not isinstance(value, Table) or idx is None:
        raise ConversionError(key_path, f"{key_path!r} is not a standard table")
    _reject_aot(value, key_path)
    inline = _table_to_inline(value)
    key = container.body[idx][0]
    assert key is not None
    new_key = _clone_key(key, dotted=False, sep=_assignment_sep(key))
    _splice(container, idx, [(new_key, inline)], kind="keys")
    _sync_owner(container, ancestors)
    _invalidate(ancestors)
    return doc


def to_standard_table(key_path: str, doc: T) -> T:
    """Convert the inline table at ``key_path`` into a header table.

    The document is mutated in place and returned. The inline table's comment
    becomes the table header comment. Nested inline tables become nested tables.
    An existing standard table is left unchanged.
    """
    _owner, container, idx, value, ancestors = _resolve(doc, key_path)
    if isinstance(value, Table):
        return doc
    if not isinstance(value, InlineTable) or idx is None:
        raise ConversionError(key_path, f"{key_path!r} is not an inline table")
    table = _inline_to_table(value)
    key = container.body[idx][0]
    assert key is not None
    table.name = key.key
    new_key = _clone_key(key, dotted=False, sep="")
    _splice(container, idx, [(new_key, table)], kind="header")
    _sync_owner(container, ancestors)
    _invalidate(ancestors)
    return doc


def to_dotted_keys(key_path: str, doc: T, max_depth: int | None = None) -> T:
    """Flatten the table at ``key_path`` into dotted-key assignments.

    ``max_depth`` limits how many levels are flattened. ``None`` flattens every
    nested table. ``1`` flattens only the table's own children. The table
    header comment becomes a standalone comment before the first dotted key.
    """
    if max_depth is not None and max_depth < 1:
        raise ConversionError(key_path, "max_depth must be >= 1 or None")
    _owner, container, idx, value, ancestors = _resolve(doc, key_path)
    if not isinstance(value, (Table, InlineTable)) or idx is None:
        raise ConversionError(
            key_path, f"{key_path!r} is not a table or inline table"
        )
    key = container.body[idx][0]
    assert key is not None
    pieces = _flatten(value, max_depth, key_path)
    entries: list[tuple[Key | None, Item]] = []
    header = _header_comment(value)
    if header is not None:
        entries.append((None, header))
    for kind, payload in pieces:
        if kind == "comment":
            entries.append((None, payload))
            continue
        rel_path, item = payload
        dotted = not isinstance(item, (Table, AoT))
        if isinstance(item, AoT):
            raise ConversionError(
                key_path, f"{key_path!r} contains an array of tables"
            )
        full = [_clone_key(key, dotted=False, sep=key.sep), *rel_path]
        top_key, top_val = _build_chain(full, item, dotted=dotted)
        entries.append((top_key, top_val))
    _splice(container, idx, entries, kind="keys")
    _sync_owner(container, ancestors)
    _invalidate(ancestors)
    return doc


def to_super_table(dotted_prefix: str, doc: T) -> T:
    """Group dotted-key entries that share ``dotted_prefix`` into a header table.

    A standalone comment immediately before the first match becomes the table
    header comment. Raises ``ConversionError`` when nothing matches.
    """
    parts = _parse_key_path(dotted_prefix)
    container, owner, remaining = _walk_to_dotted_region(doc, parts)
    if not remaining:
        raise ConversionError(
            dotted_prefix, f"No dotted keys found for {dotted_prefix!r}"
        )
    collected, first_idx, prefix_keys = _detach_dotted(container, remaining)
    if not collected or first_idx is None:
        raise ConversionError(
            dotted_prefix, f"No dotted keys found for {dotted_prefix!r}"
        )
    comment_ws = ""
    comment = ""
    if first_idx > 0:
        prev_key, prev_val = container.body[first_idx - 1]
        if prev_key is None and isinstance(prev_val, Comment):
            comment = prev_val.trivia.comment
            comment_ws = prev_val.trivia.comment_ws or " "
            container.body[first_idx - 1] = (None, Null())
    table = _build_grouped_table(collected, comment, comment_ws)
    top_key, top_val = _chain_standard(prefix_keys, table)
    if isinstance(container.body[first_idx][1], Null):
        container.body[first_idx] = (top_key, top_val)
    else:
        container.body.insert(first_idx, (top_key, top_val))
    _stabilize_headers(container)
    _reindex(container)
    _sync_owner(container, [owner] if isinstance(owner, Table) else [])
    if isinstance(owner, Table):
        owner.display_name = None
        owner.invalidate_display_name()
    return doc


def _parse_key_path(key_path: str) -> list[str]:
    if not isinstance(key_path, str) or key_path == "":
        raise ConversionError(
            "" if not isinstance(key_path, str) else key_path,
            "Key path must be a non-empty string",
        )
    parts: list[str] = []
    i = 0
    n = len(key_path)
    while i < n:
        ch = key_path[i]
        if ch in {'"', "'"}:
            quote = ch
            i += 1
            buf: list[str] = []
            while i < n and key_path[i] != quote:
                if quote == '"' and key_path[i] == "\\":
                    i += 1
                    if i >= n:
                        raise ConversionError(key_path, f"Invalid key path {key_path!r}")
                    esc = key_path[i]
                    buf.append(
                        {
                            "n": "\n",
                            "t": "\t",
                            "\\": "\\",
                            '"': '"',
                            "b": "\b",
                            "f": "\f",
                            "r": "\r",
                        }.get(esc, esc)
                    )
                else:
                    buf.append(key_path[i])
                i += 1
            if i >= n or key_path[i] != quote:
                raise ConversionError(key_path, f"Invalid key path {key_path!r}")
            i += 1
            parts.append("".join(buf))
        else:
            start = i
            while i < n and key_path[i] != ".":
                i += 1
            if i == start:
                raise ConversionError(key_path, f"Invalid key path {key_path!r}")
            parts.append(key_path[start:i])
        if i < n:
            if key_path[i] != ".":
                raise ConversionError(key_path, f"Invalid key path {key_path!r}")
            i += 1
            if i >= n:
                raise ConversionError(key_path, f"Invalid key path {key_path!r}")
    if not parts:
        raise ConversionError(key_path, f"Invalid key path {key_path!r}")
    return parts


def _resolve(doc: Container, key_path: str):
    parts = _parse_key_path(key_path)
    try:
        return _resolve_parts(doc, None, parts, [], key_path)
    except ConversionError:
        raise
    except Exception as exc:  # pragma: no cover - defensive
        raise ConversionError(key_path, str(exc)) from exc


def _resolve_parts(
    container: Container,
    owner,
    parts: list[str],
    ancestors: list,
    key_path: str,
):
    name, *rest = parts
    sk = SingleKey(name)
    if sk not in container._map:
        raise ConversionError(key_path, f"Key {name!r} does not exist")
    idx = container._map[sk]
    if isinstance(idx, tuple):
        if not rest:
            raise ConversionError(
                key_path,
                "Key refers to multiple out-of-order tables",
            )
        for i in idx:
            _k, val = container.body[i]
            if isinstance(val, (Table, InlineTable)) and _container_has(
                val.value, rest
            ):
                ancestors.append(val)
                return _resolve_parts(val.value, val, rest, ancestors, key_path)
        raise ConversionError(key_path, f"Key {rest[0]!r} does not exist")
    key, val = container.body[idx]
    if key is None or isinstance(val, Null):
        raise ConversionError(key_path, f"Key {name!r} does not exist")
    if not rest:
        return owner, container, idx, val, ancestors
    if isinstance(val, (Table, InlineTable)):
        ancestors.append(val)
        return _resolve_parts(val.value, val, rest, ancestors, key_path)
    raise ConversionError(key_path, f"{name!r} is not a table")


def _container_has(container: Container, parts: list[str]) -> bool:
    sk = SingleKey(parts[0])
    if sk not in container._map:
        return False
    if len(parts) == 1:
        return True
    idx = container._map[sk]
    indices = idx if isinstance(idx, tuple) else (idx,)
    for i in indices:
        _k, val = container.body[i]
        if isinstance(val, (Table, InlineTable)) and _container_has(val.value, parts[1:]):
            return True
    return False


def _reject_aot(node: Item, key_path: str) -> None:
    if isinstance(node, AoT):
        raise ConversionError(key_path, f"{key_path!r} contains an array of tables")
    if isinstance(node, (Table, InlineTable)):
        for _k, val in node.value.body:
            if isinstance(val, (Table, InlineTable, AoT)):
                _reject_aot(val, key_path)


def _table_to_inline(table: Table) -> InlineTable:
    container = Container()
    entries = _logical_entries(table)
    for index, (key, value, comments) in enumerate(entries):
        value = _value_to_inline(value)
        trailing = _detach_comment(value)
        if index:
            container._raw_append(None, Whitespace(","))
        if comments or trailing is not None:
            container._raw_append(None, Whitespace(" "))
            for comment in comments:
                container._raw_append(None, _fresh_comment(comment))
            if trailing is not None:
                container._raw_append(None, trailing)
            container._raw_append(None, Whitespace("\n "))
        else:
            container._raw_append(None, Whitespace(" "))
        new_key = _clone_key(key, dotted=False, sep=_assignment_sep(key))
        _prepare_inline_item(value)
        container._raw_append(new_key, value)
    if entries:
        container._raw_append(None, Whitespace(" "))
    trivia = table.trivia.copy()
    inline = InlineTable(container, trivia, new=False)
    _sync_table(inline)
    return inline


def _value_to_inline(value: Item) -> Item:
    if isinstance(value, Table):
        return _table_to_inline(value)
    if isinstance(value, InlineTable):
        rebuilt = Table(value.value, value.trivia.copy(), False, False)
        # Reuse inline children, converting any nested standard tables.
        return _table_to_inline(rebuilt)
    return value


def _inline_to_table(inline: InlineTable) -> Table:
    trivia = inline.trivia.copy()
    if trivia.comment and not trivia.comment_ws:
        trivia.comment_ws = " "
    if "\n" not in trivia.trail:
        trivia.trail += "\n"
    table = Table(Container(), trivia, False, is_super_table=None)
    for key, value in inline.value.body:
        if isinstance(value, (Null, Whitespace)):
            continue
        if key is None:
            if isinstance(value, Comment):
                table.value._raw_append(None, value)
            continue
        if isinstance(value, InlineTable):
            value = _inline_to_table(value)
        if isinstance(value, Table):
            value.display_name = None
            value._is_super_table = None
            new_key = _clone_key(key, dotted=False, sep="")
            if "\n" not in value.trivia.indent:
                value.trivia.indent = "\n" + value.trivia.indent
        else:
            new_key = _clone_key(key, dotted=False, sep=_assignment_sep(key))
            _ensure_newline(value)
        table.value._raw_append(new_key, value)
        if new_key is not None:
            dict.__setitem__(table, new_key.key, value)
    return table


def _flatten(node: Table | InlineTable, max_depth: int | None, key_path: str):
    pieces = []
    entries = _logical_entries(node)
    trailing: list[Comment] = []
    # logical_entries keeps only comments that precede a key; trailing handled there
    for key, value, comments in entries:
        for comment in comments:
            pieces.append(("comment", _fresh_comment(comment)))
        descend = isinstance(value, (Table, InlineTable)) and (
            max_depth is None or max_depth > 1
        )
        if descend:
            inner = _flatten(
                value, None if max_depth is None else max_depth - 1, key_path
            )
            for kind, payload in inner:
                if kind == "comment":
                    pieces.append((kind, payload))
                else:
                    rel, item = payload
                    pieces.append(("item", ([_clone_key(key, dotted=False, sep=key.sep), *rel], item)))
        else:
            if isinstance(value, AoT):
                raise ConversionError(
                    key_path, f"{key_path!r} contains an array of tables"
                )
            if isinstance(value, Table):
                value.display_name = None
                value._is_super_table = None
            pieces.append(("item", ([_clone_key(key, dotted=False, sep=key.sep)], value)))
    pieces.extend(("comment", c) for c in trailing)
    return pieces


def _logical_entries(node: Table | InlineTable):
    grouped: dict[str, list[tuple[Key, Item]]] = {}
    comments: dict[str, list[Comment]] = {}
    order: list[str] = []
    pending: list[Comment] = []
    for key, value in node.value.body:
        if isinstance(value, (Null, Whitespace)):
            continue
        if key is None:
            if isinstance(value, Comment):
                pending.append(value)
            continue
        if key.key not in grouped:
            grouped[key.key] = []
            comments[key.key] = pending
            pending = []
            order.append(key.key)
        grouped[key.key].append((key, value))
    result = []
    for name in order:
        pairs = grouped[name]
        key = pairs[0][0]
        values = [val for _k, val in pairs]
        result.append((key, _merge_values(key, values), comments[name]))
    return result


def _merge_values(key: Key, values: list[Item]) -> Item:
    if len(values) == 1:
        return values[0]
    tables = [val for val in values if isinstance(val, Table)]
    if len(tables) != len(values):
        return values[-1]
    container = Container(True)
    merged = Table(
        container,
        tables[0].trivia.copy(),
        tables[0].is_aot_element(),
        None,
        name=getattr(tables[0], "name", None) or key.key,
    )
    for table in tables:
        for child_key, child_val in table.value.body:
            if isinstance(child_val, Null):
                continue
            container._raw_append(child_key, child_val)
    _sync_table(merged)
    return merged


def _build_chain(path: list[SingleKey], value: Item, *, dotted: bool):
    if isinstance(value, Table):
        value.display_name = None
        value.name = path[-1].key
        value._is_super_table = None
    leaf_sep = "" if isinstance(value, (Table, AoT)) else _assignment_sep(path[-1])
    current_key = _clone_key(path[-1], dotted=False, sep=leaf_sep)
    current: Item = value
    _ensure_newline(current)
    for part in reversed(path[:-1]):
        current = _wrap_super(current_key, current)
        current_key = _clone_key(part, dotted=dotted, sep="")
    return current_key, current


def _wrap_super(key: SingleKey, item: Item) -> Table:
    container = Container(True)
    table = Table(container, Trivia(trail="\n"), False, is_super_table=True)
    container._raw_append(key, item)
    dict.__setitem__(table, key.key, item)
    return table


def _header_comment(node: Table | InlineTable) -> Comment | None:
    comment = node.trivia.comment
    if not comment:
        return None
    return Comment(
        Trivia(
            indent="",
            comment_ws="",
            comment=comment,
            trail="\n",
        )
    )


def _fresh_comment(comment: Comment) -> Comment:
    return Comment(comment.trivia.copy())


def _detach_comment(item: Item) -> Comment | None:
    trivia = getattr(item, "trivia", None)
    if trivia is None or isinstance(item, (Whitespace, Null)):
        return None
    if not trivia.comment:
        return None
    comment = Comment(
        Trivia(comment=trivia.comment, comment_ws=trivia.comment_ws, trail="\n")
    )
    trivia.comment = ""
    trivia.comment_ws = ""
    return comment


def _prepare_inline_item(item: Item) -> None:
    if isinstance(item, (Whitespace, Null)):
        return
    trivia = getattr(item, "trivia", None)
    if trivia is None:
        return
    trivia.indent = ""
    trivia.trail = ""


def _ensure_newline(item: Item) -> None:
    if isinstance(item, (Whitespace, Null)):
        return
    trivia = getattr(item, "trivia", None)
    if trivia is None:
        return
    if "\n" not in trivia.trail:
        trivia.trail += "\n"


def _assignment_sep(key: Key) -> str:
    sep = getattr(key, "sep", None) or ""
    if sep.strip() == "":
        return " = "
    return sep


def _clone_key(key: Key, *, dotted: bool, sep: str | None = None) -> SingleKey:
    src = key if isinstance(key, SingleKey) else next(iter(key))
    original = src._original
    if sep == "":
        original = original.rstrip()
    cloned = SingleKey(
        src.key,
        t=src.t,
        sep=src.sep if sep is None else sep,
        original=original,
    )
    cloned._dotted = dotted
    return cloned


def _is_header(key: Key | None, value: Item) -> bool:
    return (
        key is not None
        and isinstance(value, (Table, AoT))
        and not key.is_dotted()
    )


def _splice(
    container: Container,
    idx: int,
    entries: list[tuple[Key | None, Item]],
    *,
    kind: str,
) -> None:
    """Replace ``body[idx]`` and keep a parse(dumps()) round trip.

    Key/value entries that would fall inside a previous header are moved in
    front of that header. A new header that would capture following keys is
    placed after those keys.
    """
    container.body[idx : idx + 1] = entries
    if not entries:
        _reindex(container)
        return
    if kind == "keys":
        end = idx + len(entries)
        _hoist_before_headers(container, idx, end)
    _stabilize_headers(container)
    _reindex(container)


def _hoist_before_headers(container: Container, start: int, end: int) -> None:
    first_header = None
    for index, (key, value) in enumerate(container.body):
        if index >= start:
            break
        if _is_header(key, value):
            first_header = index
            break
    if first_header is None:
        return
    block = container.body[start:end]
    del container.body[start:end]
    container.body[first_header:first_header] = block


def _stabilize_headers(container: Container) -> None:
    """Move free keys that follow a header to before that header.

    Otherwise ``parse(dumps())`` would treat those keys as children of the header.
    """
    index = len(container.body) - 1
    while index >= 0:
        key, value = container.body[index]
        if _is_header(key, value):
            _shift_following_keys(container, index)
        index -= 1


def _shift_following_keys(container: Container, header_idx: int) -> None:
    end = header_idx + 1
    while end < len(container.body):
        key, value = container.body[end]
        if isinstance(value, Null):
            end += 1
            continue
        if _is_header(key, value):
            break
        end += 1
    block = [
        (key, value)
        for key, value in container.body[header_idx + 1 : end]
        if not isinstance(value, Null)
    ]
    if not block:
        return
    for index in range(header_idx + 1, end):
        container.body[index] = (None, Null())
    for offset, entry in enumerate(block):
        container.body.insert(header_idx + offset, entry)


def _reindex(container: Container) -> None:
    body = [
        (key, value)
        for key, value in container.body
        if not isinstance(value, Null)
    ]
    parsed = container._parsed
    container._body = []
    container._map = {}
    container._table_keys = []
    for key in list(dict.keys(container)):
        dict.__delitem__(container, key)
    container._parsed = parsed
    for key, value in body:
        container._raw_append(key, value)


def _sync_table(table: Table | InlineTable) -> None:
    for key in list(dict.keys(table)):
        dict.__delitem__(table, key)
    for key, value in table.value.body:
        if key is not None and not isinstance(value, Null):
            dict.__setitem__(table, key.key, value)


def _sync_owner(container: Container, ancestors: list) -> None:
    if not ancestors:
        return
    owner = ancestors[-1]
    if isinstance(owner, (Table, InlineTable)) and owner.value is container:
        _sync_table(owner)


def _invalidate(ancestors: list) -> None:
    for ancestor in ancestors:
        if isinstance(ancestor, Table):
            ancestor.display_name = None
            ancestor.invalidate_display_name()


def _walk_to_dotted_region(doc: Container, parts: list[str]):
    container: Container = doc
    owner = None
    remaining = list(parts)
    while len(remaining) > 1:
        sk = SingleKey(remaining[0])
        if sk not in container._map:
            return container, owner, remaining
        idx = container._map[sk]
        if isinstance(idx, tuple):
            return container, owner, remaining
        key, val = container.body[idx]
        if (
            key is not None
            and isinstance(val, Table)
            and not key.is_dotted()
        ):
            owner = val
            container = val.value
            remaining = remaining[1:]
            continue
        return container, owner, remaining
    return container, owner, remaining


def _detach_dotted(container: Container, parts: list[str]):
    """Remove dotted entries under ``parts`` and return suffix entries.

    The returned prefix keys are the original key objects for ``parts``.
    """
    collected: list[tuple[list[SingleKey], Item]] = []
    first_idx: int | None = None
    found_keys: dict[int, SingleKey] = {}

    def walk(current: Container, part_idx: int) -> None:
        nonlocal first_idx
        head = parts[part_idx]
        rest = part_idx + 1 < len(parts)
        remove: list[int] = []
        for index, (key, value) in enumerate(list(current.body)):
            if (
                key is None
                or not isinstance(key, SingleKey)
                or key.key != head
                or not key.is_dotted()
                or not isinstance(value, Table)
            ):
                continue
            if rest:
                before = len(collected)
                walk(value.value, part_idx + 1)
                if len(collected) == before:
                    continue
                if _effectively_empty(value):
                    remove.append(index)
            else:
                collected.extend(_extract_paths(value))
                remove.append(index)
            if part_idx not in found_keys:
                found_keys[part_idx] = key
            if first_idx is None and current is container:
                first_idx = index
        for index in reversed(remove):
            current._remove_at(index)

    walk(container, 0)
    prefix_keys = [found_keys[i] for i in range(len(parts)) if i in found_keys]
    while len(prefix_keys) < len(parts):
        prefix_keys.append(SingleKey(parts[len(prefix_keys)]))
    return collected, first_idx, prefix_keys[: len(parts)]


def _effectively_empty(table: Table) -> bool:
    for key, value in table.value.body:
        if key is not None and not isinstance(value, Null):
            return False
    return True


def _extract_paths(table: Table) -> list[tuple[list[SingleKey], Item]]:
    extracted: list[tuple[list[SingleKey], Item]] = []
    for key, value in table.value.body:
        if key is None or isinstance(value, (Null, Whitespace)):
            continue
        if not isinstance(key, SingleKey):
            key = _clone_key(key, dotted=key.is_dotted(), sep=key.sep)
        if key.is_dotted() and isinstance(value, Table):
            for path, item in _extract_paths(value):
                extracted.append(([_clone_key(key, dotted=True, sep=""), *path], item))
        else:
            extracted.append(([_clone_key(key, dotted=False, sep=key.sep)], value))
    return extracted


def _build_grouped_table(
    entries: list[tuple[list[SingleKey], Item]], comment: str, comment_ws: str
) -> Table:
    trivia = Trivia(
        indent="",
        comment_ws=comment_ws if comment else "",
        comment=comment,
        trail="\n",
    )
    table = Table(Container(), trivia, False, is_super_table=None)
    for path, item in entries:
        _place_suffix(table, path, item)
    return table


def _place_suffix(table: Table, path: list[SingleKey], item: Item) -> None:
    if len(path) == 1:
        if isinstance(item, (Table, AoT)):
            key = _clone_key(path[0], dotted=False, sep="")
            if isinstance(item, Table):
                item.display_name = None
                item._is_super_table = None
        else:
            key = _clone_key(path[0], dotted=False, sep=_assignment_sep(path[0]))
        _ensure_newline(item)
        table.value._raw_append(key, item)
        dict.__setitem__(table, key.key, item)
        return
    head = _clone_key(path[0], dotted=True, sep="")
    child = _suffix_table(path[1:], item)
    table.value._raw_append(head, child)
    dict.__setitem__(table, head.key, child)


def _suffix_table(path: list[SingleKey], item: Item) -> Table:
    """Build a dotted super-table chain for path[:-1] ending at item."""
    if len(path) == 1:
        leaf_sep = "" if isinstance(item, (Table, AoT)) else _assignment_sep(path[0])
        key = _clone_key(path[0], dotted=False, sep=leaf_sep)
        if isinstance(item, Table):
            item.display_name = None
            item._is_super_table = None
        _ensure_newline(item)
        return _wrap_super(key, item)
    key = _clone_key(path[0], dotted=True, sep="")
    return _wrap_super(key, _suffix_table(path[1:], item))


def _chain_standard(prefix_keys: list[SingleKey], table: Table):
    table.name = prefix_keys[-1].key
    table.display_name = None
    node: Item = table
    key = _clone_key(prefix_keys[-1], dotted=False, sep="")
    for part in reversed(prefix_keys[:-1]):
        node = _wrap_super(key, node)
        node.name = part.key
        key = _clone_key(part, dotted=False, sep="")
    return key, node
