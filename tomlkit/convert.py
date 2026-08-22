from __future__ import annotations

from tomlkit.container import Container
from tomlkit.exceptions import ConversionError
from tomlkit.items import AoT
from tomlkit.items import Comment
from tomlkit.items import DottedKey
from tomlkit.items import InlineTable
from tomlkit.items import Key
from tomlkit.items import Null
from tomlkit.items import SingleKey
from tomlkit.items import Table
from tomlkit.items import Whitespace
from tomlkit.items import Trivia


def _resolve(key_path: str, doc):
    current = doc
    parts = key_path.split(".") if key_path else []
    for i, part in enumerate(parts):
        if not isinstance(current, (Container, Table, InlineTable)):
            raise ConversionError(key_path, f"Key path {key_path!r} has a non-table intermediate")
        try:
            current = current[part]
        except KeyError:
            raise ConversionError(key_path, f"Key {key_path!r} does not exist") from None
    if not parts:
        return None, doc
    parent = doc
    for part in parts[:-1]:
        parent = parent[part]
    return parent, current


def _container(value: Container) -> Container:
    return value.copy()


def _insert_comment(container: Container, index: int, text: str) -> None:
    for key, value in list(container._map.items()):
        if isinstance(value, tuple):
            container._map[key] = tuple(i + 1 if i >= index else i for i in value)
        elif value >= index:
            container._map[key] = value + 1
    container._body.insert(index, (None, Comment(Trivia(comment_ws="  ", comment=text))))


def _swap(container: Container, key, value, new_key=None) -> None:
    index = container._map[key]
    if isinstance(index, tuple):
        index = index[0]
    old_key, _ = container._body[index]
    new_key = new_key or old_key
    container._body[index] = (new_key, value)
    del container._map[old_key]
    container._map[new_key] = index
    if new_key.key != old_key.key:
        dict.__delitem__(container, old_key.key)
    dict.__setitem__(container, new_key.key, value.value)


def _convert_inline(value):
    if isinstance(value, AoT):
        raise ValueError
    if isinstance(value, Table):
        value = InlineTable(_container(value.value), value.trivia.copy(), new=True)
    if isinstance(value, InlineTable):
        for key, child in list(value.value.body):
            if isinstance(child, Table):
                _swap(
                    value.value,
                    key,
                    _convert_inline(child),
                    SingleKey(key.key, sep=" = "),
                )
            elif isinstance(child, AoT):
                raise ValueError
    return value


def to_inline_table(key_path: str, doc):
    parent, target = _resolve(key_path, doc)
    if not isinstance(target, (Table, InlineTable)):
        raise ConversionError(key_path, f"{key_path!r} is not a table")
    if isinstance(target, InlineTable):
        return doc
    try:
        converted = _convert_inline(target)
    except ValueError:
        raise ConversionError(key_path, f"{key_path!r} contains an array of tables") from None
    _swap(
        parent,
        SingleKey(key_path.rsplit(".", 1)[-1]),
        converted,
        SingleKey(key_path.rsplit(".", 1)[-1], sep=" = "),
    )
    return doc


def _convert_standard(value):
    if isinstance(value, InlineTable):
        value = Table(_container(value.value), value.trivia.copy(), False)
    if isinstance(value, Table):
        for key, child in list(value.value.body):
            if isinstance(child, InlineTable):
                _swap(value.value, key, _convert_standard(child))
    return value


def to_standard_table(key_path: str, doc):
    parent, target = _resolve(key_path, doc)
    if not isinstance(target, (Table, InlineTable)):
        raise ConversionError(key_path, f"{key_path!r} is not a table")
    if isinstance(target, Table):
        return doc
    converted = _convert_standard(target)
    _swap(parent, SingleKey(key_path.rsplit(".", 1)[-1]), converted)
    return doc


def _leaves(table, prefix, max_depth, depth=0):
    for key, value in table.value.body:
        if key is None or isinstance(value, (Whitespace, Comment, Null)):
            continue
        full = prefix.concat(key) if prefix else key
        if isinstance(full, DottedKey):
            full.sep = " = "
        if isinstance(value, (Table, InlineTable)) and (
            max_depth is None or depth < max_depth
        ):
            yield from _leaves(value, full, max_depth, depth + 1)
        else:
            yield full, value


def to_dotted_keys(key_path: str, doc, max_depth: int | None = None):
    parent, target = _resolve(key_path, doc)
    if not isinstance(target, (Table, InlineTable)):
        raise ConversionError(key_path, f"{key_path!r} is not a table")
    leaves = list(_leaves(target, None, max_depth))
    if not leaves:
        raise ConversionError(key_path, f"{key_path!r} has no values")
    old_key = SingleKey(key_path.rsplit(".", 1)[-1])
    idx = parent._map[old_key]
    if isinstance(idx, tuple):
        idx = idx[0]
    if target.trivia.comment:
        _insert_comment(parent, idx, target.trivia.comment)
        idx += 1
    old_key = parent.body[idx][0]
    parent.remove(old_key)
    parent._insert_at(idx, leaves[0][0], leaves[0][1])
    for dotted, value in leaves[1:]:
        parent._insert_at(idx + 1, dotted, value)
        idx += 1
    return doc


def to_super_table(dotted_prefix: str, doc):
    matches = []
    for idx, (key, value) in enumerate(doc.body):
        if isinstance(key, Key) and key.is_multi() and key.key.startswith(dotted_prefix + "."):
            matches.append((idx, key, value))
    if not matches:
        raise ConversionError(dotted_prefix, f"No dotted keys found for {dotted_prefix!r}")
    first = matches[0][0]
    table = Table(Container(True), Trivia(), False, is_super_table=True)
    for _, key, value in matches:
        remainder = key.key[len(dotted_prefix) + 1 :]
        table.append(remainder, value)
    for _, key, _ in reversed(matches):
        doc.remove(key)
    doc.append(SingleKey(dotted_prefix), table)
    if first > 0 and isinstance(doc.body[first - 1][1], Comment):
        table.trivia.comment = doc.body[first - 1][1].trivia.comment
    return doc
