from __future__ import annotations

from collections.abc import Iterator

from tomlkit.container import Container
from tomlkit.exceptions import ConversionError
from tomlkit.items import AoT
from tomlkit.items import Array
from tomlkit.items import Comment
from tomlkit.items import InlineTable
from tomlkit.items import Item
from tomlkit.items import Key
from tomlkit.items import KeyType
from tomlkit.items import Null
from tomlkit.items import SingleKey
from tomlkit.items import Table
from tomlkit.items import Trivia
from tomlkit.items import Whitespace


def _error(path: str, message: str) -> ConversionError:
    return ConversionError(message, path)


def _resolve(key_path: str, doc: Container) -> tuple[Container, Key, Item]:
    parts = key_path.split(".") if key_path else []
    if not parts:
        raise _error(key_path, "A key path is required")

    parent = doc
    for part in parts[:-1]:
        try:
            value = parent.item(part)
        except Exception as exc:
            raise _error(key_path, f'Key "{part}" does not exist') from exc
        if not isinstance(value, (Table, InlineTable)):
            raise _error(key_path, f'Key "{part}" is not a table')
        parent = value.value

    key = SingleKey(parts[-1])
    try:
        value = parent.item(key)
        index = parent._map[key]
        if isinstance(index, tuple):
            index = index[-1]
        key = parent.body[index][0]
        assert key is not None
    except Exception as exc:
        raise _error(key_path, f'Key "{key_path}" does not exist') from exc
    return parent, key, value


def _replace(parent: Container, key: Key, old: Item, new: Item) -> None:
    parent._replace(key, key, new)


def _has_aot(value: Item) -> bool:
    if isinstance(value, AoT):
        return True
    if isinstance(value, (Table, InlineTable)):
        return any(_has_aot(child) for child in value.values())
    if isinstance(value, Array):
        return any(_has_aot(child) for child in value)
    return False


def _converted_table(source: Table | InlineTable, inline: bool) -> Table | InlineTable:
    target: Table | InlineTable
    if inline:
        target = InlineTable(Container(source.value._parsed), source.trivia.copy())
    else:
        target = Table(
            Container(source.value._parsed),
            source.trivia.copy(),
            False,
            is_super_table=source.is_super_table() if isinstance(source, Table) else None,
            name=getattr(source, "name", None),
            display_name=getattr(source, "display_name", None),
        )

    for key, value in source.value.body:
        if isinstance(value, (Whitespace, Comment, Null)):
            if not inline and isinstance(value, Whitespace) and "," in value.s:
                continue
            target.value._raw_append(key, value)
            continue
        if isinstance(value, Table) and inline:
            value = _converted_table(value, True)
            if key is not None and not key.sep:
                key.sep = " = "
        elif isinstance(value, InlineTable) and not inline:
            value = _converted_table(value, False)
        target.value._raw_append(key, value)
        if key is not None:
            dict.__setitem__(target, key.key, value)
    return target


def to_inline_table(key_path: str, doc: Container) -> Container:
    parent, key, value = _resolve(key_path, doc)
    if isinstance(value, InlineTable):
        return doc
    if not isinstance(value, Table):
        raise _error(key_path, "The target is not a standard table")
    if _has_aot(value):
        raise _error(key_path, "An array of tables cannot be converted to an inline table")
    _replace(parent, key, value, _converted_table(value, True))
    return doc


def to_standard_table(key_path: str, doc: Container) -> Container:
    parent, key, value = _resolve(key_path, doc)
    if isinstance(value, Table):
        return doc
    if not isinstance(value, InlineTable):
        raise _error(key_path, "The target is not an inline table")
    _replace(parent, key, value, _converted_table(value, False))
    return doc


def _flatten(
    value: Table | InlineTable, prefix: tuple[SingleKey, ...], max_depth: int | None
) -> Iterator[tuple[tuple[SingleKey, ...], Item]]:
    for key, child in value.value.body:
        if key is None or isinstance(child, (Whitespace, Comment, Null)):
            continue
        child_key = next(iter(key))
        path = prefix + (child_key,)
        if isinstance(child, (Table, InlineTable)) and (
            max_depth is None or len(path) < max_depth
        ):
            yield from _flatten(child, path, max_depth)
        else:
            yield path, child


def to_dotted_keys(
    key_path: str, doc: Container, max_depth: int | None = None
) -> Container:
    parent, key, value = _resolve(key_path, doc)
    if not isinstance(value, (Table, InlineTable)):
        raise _error(key_path, "The target is not a table")
    if max_depth is not None and max_depth < 1:
        raise ValueError("max_depth must be positive or None")

    target = Table(Container(True), Trivia(), False, is_super_table=True)
    if value.trivia.comment:
        target.value._raw_append(
            None,
            Comment(
                Trivia(
                    indent=value.trivia.indent,
                    comment=value.trivia.comment,
                    trail=value.trivia.trail,
                )
            ),
        )
    for path, child in _flatten(value, (), max_depth):
        dotted_key = (
            SingleKey(
                ".".join(part.key for part in path),
                KeyType.Bare,
                original=".".join(part.as_string() for part in path),
            )
            if len(path) > 1
            else path[0]
        )
        target.value._raw_append(dotted_key, child)
        dict.__setitem__(target, dotted_key.key, child)

    key._dotted = True
    _replace(parent, key, value, target)
    return doc


def to_super_table(dotted_prefix: str, doc: Container) -> Container:
    parent, key, value = _resolve(dotted_prefix, doc)
    if not isinstance(value, Table) or not value.is_super_table():
        raise _error(dotted_prefix, "No dotted keys match the requested prefix")

    index = parent._map.get(key)
    if isinstance(index, int) and index > 0:
        previous_key, previous = parent.body[index - 1]
        if previous_key is None and isinstance(previous, Comment):
            value.trivia.comment_ws = previous.trivia.comment_ws
            value.trivia.comment = previous.trivia.comment
            value.trivia.trail = previous.trivia.trail
            parent.body[index - 1] = (None, Null())

    key._dotted = False
    value._is_super_table = False
    for child_key, _ in value.value.body:
        if child_key is not None:
            child_key._dotted = False
    return doc
