"""Conversion between standard tables, inline tables and dotted keys."""

from __future__ import annotations

from typing import Union

from tomlkit.container import Container
from tomlkit.exceptions import ConversionError
from tomlkit.items import AoT
from tomlkit.items import Comment
from tomlkit.items import DottedKey
from tomlkit.items import InlineTable
from tomlkit.items import Item
from tomlkit.items import Key
from tomlkit.items import SingleKey
from tomlkit.items import Table
from tomlkit.items import Trivia
from tomlkit.items import Whitespace
from tomlkit.toml_document import TOMLDocument


__all__ = ["to_dotted_keys", "to_inline_table", "to_standard_table", "to_super_table"]

_Owner = Union[TOMLDocument, Table]
_Entry = tuple[Union[Key, None], Item]


def _split(key_path: str) -> list[str]:
    parts = [p.strip() for p in key_path.split(".")]
    if not key_path or any(not p for p in parts):
        raise ConversionError(key_path, f"Invalid key path: {key_path!r}")
    return parts


def _container(owner: _Owner) -> Container:
    return owner if isinstance(owner, Container) else owner.value


def _find_index(container: Container, name: str) -> int | None:
    for i, (k, v) in enumerate(container.body):
        if k is not None and k.key == name and not isinstance(v, Whitespace):
            return i
    return None


def _resolve_owner(doc: TOMLDocument, parts: list[str], key_path: str) -> _Owner:
    owner: _Owner = doc
    for part in parts:
        idx = _find_index(_container(owner), part)
        if idx is None:
            raise ConversionError(key_path, f"Key {key_path!r} does not exist")
        child = _container(owner).body[idx][1]
        if not isinstance(child, Table):
            raise ConversionError(
                key_path, f"Intermediate key {part!r} in {key_path!r} is not a table"
            )
        owner = child
    return owner


def _resolve(doc: TOMLDocument, key_path: str) -> tuple[_Owner, int, Item]:
    parts = _split(key_path)
    owner = _resolve_owner(doc, parts[:-1], key_path)
    idx = _find_index(_container(owner), parts[-1])
    if idx is None:
        raise ConversionError(key_path, f"Key {key_path!r} does not exist")
    return owner, idx, _container(owner).body[idx][1]


def _is_header_table(key: Key | None, item: Item) -> bool:
    return isinstance(item, (Table, AoT)) and not (key is not None and key.is_dotted())


def _values_end(entries: list[_Entry]) -> int:
    for i, (k, v) in enumerate(entries):
        if _is_header_table(k, v):
            return i
    return len(entries)


def _fresh_key(key: Key) -> SingleKey:
    single = key if isinstance(key, SingleKey) else list(key)[-1]
    return SingleKey(single.key, t=single.t)


def _make_key(parts: list[SingleKey]) -> Key:
    fresh = [SingleKey(p.key, t=p.t) for p in parts]
    return fresh[0] if len(fresh) == 1 else DottedKey(fresh)


def _as_line(item: Item) -> Item:
    item.trivia.indent = ""
    if "\n" not in item.trivia.trail:
        item.trivia.trail = "\n"
    return item


def _rebuild(owner: _Owner, entries: list[_Entry]) -> None:
    container = _container(owner)
    container._map = {}
    container._body = []
    container._table_keys = []
    dict.clear(container)
    container.parsing(True)
    for k, v in entries:
        if k is not None and k.is_multi():
            k = DottedKey([SingleKey(p.key, t=p.t) for p in k], sep=k.sep)
        container.append(k, v)
    container.parsing(False)
    if isinstance(owner, Table):
        dict.clear(owner)
        for k, v in container.body:
            if k is not None:
                dict.__setitem__(owner, k.key, v)


def _table_to_inline(table: Table, key_path: str) -> InlineTable:
    inline = InlineTable(Container(), Trivia(trail="\n"), new=True)
    for k, v in table.value.body:
        if k is None:
            continue
        if isinstance(v, AoT):
            raise ConversionError(
                key_path, f"Cannot convert {key_path!r}: it contains an array of tables"
            )
        if isinstance(v, Table):
            v = _table_to_inline(v, key_path)
        v.trivia.indent = ""
        v.trivia.comment_ws = ""
        v.trivia.comment = ""
        v.trivia.trail = ""
        inline.append(_fresh_key(k), v)
    return inline


def _inline_to_table(inline: InlineTable, name: str) -> Table:
    table = Table(Container(), Trivia(indent="\n"), False, name=name)
    values: list[_Entry] = []
    tables: list[_Entry] = []
    for k, v in inline.value.body:
        if k is None:
            continue
        if isinstance(v, InlineTable):
            tables.append((k, _inline_to_table(v, f"{name}.{k.as_string()}")))
        else:
            values.append((k, _as_line(v)))
    for k, v in values + tables:
        table.append(_fresh_key(k), v)
    return table


def to_inline_table(key_path: str, doc: TOMLDocument) -> TOMLDocument:
    """Convert the standard table at ``key_path`` into an inline table."""
    owner, idx, item = _resolve(doc, key_path)
    if isinstance(item, InlineTable):
        return doc
    if not isinstance(item, Table):
        raise ConversionError(key_path, f"{key_path!r} is not a table")

    inline = _table_to_inline(item, key_path)
    if item.trivia.comment:
        inline.trivia.comment_ws = item.trivia.comment_ws or " "
        inline.trivia.comment = item.trivia.comment

    entries = list(_container(owner).body)
    key = entries.pop(idx)[0]
    entries.insert(_values_end(entries), (_fresh_key(key), inline))
    _rebuild(owner, entries)
    return doc


def to_standard_table(key_path: str, doc: TOMLDocument) -> TOMLDocument:
    """Convert the inline table at ``key_path`` into a standard ``[header]`` table."""
    owner, idx, item = _resolve(doc, key_path)
    if isinstance(item, Table):
        return doc
    if not isinstance(item, InlineTable):
        raise ConversionError(key_path, f"{key_path!r} is not an inline table")

    entries = list(_container(owner).body)
    key = entries.pop(idx)[0]
    table = _inline_to_table(item, key_path)
    if item.trivia.comment:
        table.trivia.comment_ws = " "
        table.trivia.comment = item.trivia.comment
    entries.insert(_values_end(entries), (_fresh_key(key), table))
    _rebuild(owner, entries)
    return doc


def _flatten(
    item: Table | InlineTable,
    prefix: list[SingleKey],
    depth: int,
    max_depth: int | None,
    key_path: str,
) -> list[_Entry]:
    out: list[_Entry] = []
    for k, v in item.value.body:
        if k is None:
            if isinstance(v, Comment):
                out.append((None, v))
            continue
        path = prefix + [SingleKey(p.key, t=p.t) for p in k]
        if isinstance(v, AoT):
            raise ConversionError(
                key_path, f"Cannot convert {key_path!r}: it contains an array of tables"
            )
        if isinstance(v, (Table, InlineTable)):
            if max_depth is None or depth < max_depth:
                out.extend(_flatten(v, path, depth + 1, max_depth, key_path))
                continue
            if isinstance(v, Table):
                v = _table_to_inline(v, key_path)
        out.append((_make_key(path), _as_line(v)))
    return out


def to_dotted_keys(
    key_path: str, doc: TOMLDocument, max_depth: int | None = None
) -> TOMLDocument:
    """Flatten the table at ``key_path`` into dotted-key assignments."""
    owner, idx, item = _resolve(doc, key_path)
    if not isinstance(item, (Table, InlineTable)):
        raise ConversionError(key_path, f"{key_path!r} is not a table")
    if max_depth is not None and max_depth < 1:
        raise ConversionError(key_path, "max_depth must be at least 1")

    entries = list(_container(owner).body)
    key = entries.pop(idx)[0]
    new = _flatten(item, [_fresh_key(key)], 1, max_depth, key_path)
    if isinstance(item, Table) and item.trivia.comment:
        new.insert(0, (None, Comment(Trivia(comment=item.trivia.comment, trail="\n"))))

    pos = idx if not _is_header_table(key, item) else _values_end(entries)
    if pos > 0:
        prev = entries[pos - 1][1]
        if not isinstance(prev, Whitespace) and "\n" not in prev.trivia.trail:
            prev.trivia.trail += "\n"
    entries[pos:pos] = new
    _rebuild(owner, entries)
    return doc


def _leaves(key: Key, item: Item, prefix: list[SingleKey]) -> list[_Entry]:
    path = prefix + list(key)
    if isinstance(item, Table) and key.is_dotted():
        out: list[_Entry] = []
        for k, v in item.value.body:
            if k is not None:
                out.extend(_leaves(k, v, path))
        return out
    return [(DottedKey(path) if len(path) > 1 else path[0], item)]


def to_super_table(dotted_prefix: str, doc: TOMLDocument) -> TOMLDocument:
    """Group dotted-key entries sharing ``dotted_prefix`` into a ``[prefix]`` table."""
    parts = _split(dotted_prefix)
    for split in range(len(parts)):
        try:
            owner = _resolve_owner(doc, parts[:split], dotted_prefix)
        except ConversionError:
            break
        rest = parts[split:]
        entries = list(_container(owner).body)
        new_entries: list[_Entry] = []
        matches: list[_Entry] = []
        first_match: int | None = None
        for k, v in entries:
            if k is None or not (k.is_multi() or k.is_dotted()):
                new_entries.append((k, v))
                continue
            for leaf_key, leaf in _leaves(k, v, []):
                names = [p.key for p in leaf_key]
                if len(names) > len(rest) and names[: len(rest)] == rest:
                    if first_match is None:
                        first_match = len(new_entries)
                    matches.append((_make_key(list(leaf_key)[len(rest) :]), leaf))
                else:
                    new_entries.append((leaf_key, leaf))
        if not matches:
            continue

        header_comment = ""
        assert first_match is not None
        if first_match > 0:
            prev_key, prev = new_entries[first_match - 1]
            if prev_key is None and isinstance(prev, Comment):
                header_comment = prev.trivia.comment
                del new_entries[first_match - 1]

        name = ".".join(SingleKey(p).as_string() for p in parts)
        table = Table(Container(), Trivia(indent="\n"), False, name=name)
        if header_comment:
            table.trivia.comment_ws = " "
            table.trivia.comment = header_comment
        for k, v in matches:
            table.append(k, _as_line(v))

        outer: Table = table
        for part in reversed(rest[1:]):
            wrapper = Table(Container(), Trivia(), False, is_super_table=True)
            wrapper.append(SingleKey(part), outer)
            outer = wrapper
        new_entries.append((SingleKey(rest[0]), outer))
        _rebuild(owner, new_entries)
        return doc

    raise ConversionError(
        dotted_prefix, f"No dotted keys found with prefix {dotted_prefix!r}"
    )
