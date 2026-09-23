"""
Incremental JSON iterators for response bodies.

Supports a single JSON document (``application/json`` and ``application/*+json``),
NDJSON (``application/ndjson`` and ``application/x-ndjson``), and JSON text
sequences (``application/json-seq``, RFC 7464).
"""

from __future__ import annotations

import codecs
import email.message
import json
from collections.abc import Iterator
from json import JSONDecodeError
from typing import Any

from ._exceptions import DecodingError

_JSON_WS = frozenset(" \t\r\n")
_RS = "\x1e"
_BOM = "\ufeff"
_DECODER = json.JSONDecoder()
_LITERALS: tuple[tuple[str, Any], ...] = (
    ("true", True),
    ("false", False),
    ("null", None),
)
_SPECIALS: tuple[tuple[str, float], ...] = (
    ("-Infinity", float("-inf")),
    ("Infinity", float("inf")),
    ("NaN", float("nan")),
)


def _is_json_ws(text: str) -> bool:
    return all(character in _JSON_WS for character in text)


def _skip_ws(text: str, index: int) -> int:
    length = len(text)
    while index < length and text[index] in _JSON_WS:
        index += 1
    return index


def _is_known_encoding(encoding: str) -> bool:
    try:
        codecs.lookup(encoding)
    except LookupError:
        return False
    return True


def resolve_json_content_type(content_type: str | None) -> tuple[str, str | None]:
    """
    Return ``(format, charset)`` for a JSON streaming media type.

    ``format`` is ``"json"``, ``"ndjson"``, or ``"json-seq"``.
    ``charset`` is the charset parameter, or ``None`` when it is absent.
    """
    if content_type is None or not content_type.strip():
        raise DecodingError("Response Content-Type is not a JSON media type")

    message = email.message.Message()
    message["content-type"] = content_type
    media_type = message.get_content_type()
    # ``Message.get_content_type()`` falls back to ``text/plain`` when the
    # header is not a media type. Only honor that fallback for a real
    # ``text/plain`` value.
    if media_type == "text/plain" and not content_type.lower().lstrip().startswith(
        "text/plain"
    ):
        raise DecodingError("Response Content-Type is not a JSON media type")

    main_type, _, subtype = media_type.partition("/")
    if subtype in {"ndjson", "x-ndjson"} and main_type == "application":
        json_format = "ndjson"
    elif subtype == "json-seq" and main_type == "application":
        json_format = "json-seq"
    elif main_type == "application" and (
        subtype == "json" or subtype.endswith("+json")
    ):
        json_format = "json"
    else:
        raise DecodingError("Response Content-Type is not a JSON media type")

    charset = message.get_content_charset(failobj=None)
    if charset is not None and not _is_known_encoding(charset):
        raise DecodingError(f"JSON charset {charset!r} is not a known encoding")
    return json_format, charset


def detect_json_encoding(data: bytes, *, final: bool) -> str | None:
    """
    Detect a JSON text encoding, matching :func:`json.loads` on complete input.

    Return ``None`` when ``data`` is a still-ambiguous prefix.
    """
    if data.startswith((codecs.BOM_UTF32_BE, codecs.BOM_UTF32_LE)):
        return "utf-32"
    if data.startswith(codecs.BOM_UTF16_BE):
        return "utf-16"
    if data.startswith(codecs.BOM_UTF16_LE):
        # ``FF FE`` is also the prefix of a UTF-32 LE BOM.
        if data.startswith(codecs.BOM_UTF32_LE):
            return "utf-32"
        if len(data) < 4 and not final:
            return None
        return "utf-16"
    if data.startswith(codecs.BOM_UTF8):
        return "utf-8-sig"
    if (
        not final
        and data
        and len(data) < len(codecs.BOM_UTF8)
        and codecs.BOM_UTF8.startswith(data)
    ):
        return None

    if len(data) >= 4:
        if data[0] == 0:
            return "utf-16-be" if data[1] else "utf-32-be"
        if data[1] == 0:
            return "utf-16-le" if data[2] or data[3] else "utf-32-le"
        return "utf-8"

    if not final:
        if not data:
            return None
        if data[0] == 0:
            # ``00 XX`` is UTF-16 BE; ``00 00`` may still be UTF-32 BE.
            if len(data) >= 2 and data[1] != 0:
                return "utf-16-be"
            return None
        if len(data) >= 2 and data[1] == 0:
            return None
        if len(data) == 1:
            return None
        return "utf-8"

    if len(data) == 2:
        if data[0] == 0:
            return "utf-16-be"
        if data[1] == 0:
            return "utf-16-le"
    return "utf-8"


class JSONTextDecoder:
    """
    Incrementally decode JSON bytes.

    When ``encoding`` is ``None``, the encoding is detected from the leading
    bytes (UTF-8/16/32, including a UTF-8 BOM). Otherwise ``encoding`` must
    already name a known codec.
    """

    def __init__(self, encoding: str | None) -> None:
        self._encoding = encoding
        self._pending = bytearray()
        self._decoder: codecs.IncrementalDecoder | None
        if encoding is None:
            self._decoder = None
        else:
            self._decoder = codecs.getincrementaldecoder(encoding)(errors="strict")

    def decode(self, data: bytes) -> str:
        if self._decoder is None:
            if data:
                self._pending.extend(data)
            encoding = detect_json_encoding(bytes(self._pending), final=False)
            if encoding is None:
                return ""
            self._decoder = codecs.getincrementaldecoder(encoding)(errors="strict")
            data = bytes(self._pending)
            self._pending.clear()
        return self._decode(data, final=False)

    def flush(self) -> str:
        if self._decoder is None:
            if not self._pending:
                return ""
            encoding = detect_json_encoding(bytes(self._pending), final=True)
            if encoding is None:  # pragma: no cover
                encoding = "utf-8"
            self._decoder = codecs.getincrementaldecoder(encoding)(errors="strict")
            data = bytes(self._pending)
            self._pending.clear()
            return self._decode(data, final=True)
        return self._decode(b"", final=True)

    def _decode(self, data: bytes, *, final: bool) -> str:
        assert self._decoder is not None
        try:
            text = self._decoder.decode(data, final)
        except UnicodeError as exc:
            raise DecodingError(str(exc)) from exc
        if not isinstance(text, str):
            raise DecodingError("JSON charset did not decode to text")
        return text


def _scan_json_number(text: str, index: int, *, final: bool) -> int | None:
    """
    Return the end index of a JSON number, or ``None`` if more input is required.
    """
    length = len(text)
    cursor = index
    if cursor >= length:
        return None

    if text[cursor] == "-":
        cursor += 1
        if cursor >= length:
            if final:
                raise DecodingError("Invalid JSON number")
            return None

    if cursor >= length or not text[cursor].isdigit():
        raise DecodingError("Invalid JSON number")

    if text[cursor] == "0":
        cursor += 1
        if cursor < length and text[cursor].isdigit():
            raise DecodingError("Invalid JSON number")
    else:
        while cursor < length and text[cursor].isdigit():
            cursor += 1

    if cursor >= length:
        return cursor if final else None

    if text[cursor] == ".":
        cursor += 1
        if cursor >= length:
            if final:
                raise DecodingError("Invalid JSON number")
            return None
        if not text[cursor].isdigit():
            raise DecodingError("Invalid JSON number")
        while cursor < length and text[cursor].isdigit():
            cursor += 1
        if cursor >= length:
            return cursor if final else None

    if cursor < length and text[cursor] in "eE":
        cursor += 1
        if cursor >= length:
            if final:
                raise DecodingError("Invalid JSON number")
            return None
        if text[cursor] in "+-":
            cursor += 1
            if cursor >= length:
                if final:
                    raise DecodingError("Invalid JSON number")
                return None
        if cursor >= length or not text[cursor].isdigit():
            raise DecodingError("Invalid JSON number")
        while cursor < length and text[cursor].isdigit():
            cursor += 1
        if cursor >= length:
            return cursor if final else None

    return cursor


def _scan_literal(text: str, index: int, *, final: bool) -> tuple[Any, int] | None:
    remainder = text[index:]
    for literal, value in _LITERALS:
        if remainder.startswith(literal):
            end = index + len(literal)
            if end < len(text) and text[end].isalpha():
                raise DecodingError("Invalid JSON literal")
            if end == len(text) and not final:
                return None
            return value, end
        if literal.startswith(remainder):
            if final:
                raise DecodingError("Invalid JSON literal")
            return None
    raise DecodingError("Invalid JSON literal")


def _scan_special(
    text: str, index: int, *, final: bool
) -> tuple[Any, int] | None | bool:
    """
    Parse ``NaN`` / ``Infinity`` / ``-Infinity``.

    Return ``False`` when the text is not one of those constants.
    Return ``None`` when more input is required.
    """
    remainder = text[index:]
    if not remainder:
        return False
    first = remainder[0]
    if first == "-" and not remainder.startswith("-I"):
        return False
    if first not in {"N", "I", "-"}:
        return False

    for literal, value in _SPECIALS:
        if first != literal[0]:
            continue
        if remainder.startswith(literal):
            end = index + len(literal)
            if end < len(text) and text[end].isalnum():
                raise DecodingError("Invalid JSON text")
            if end == len(text) and not final:
                return None
            return value, end
        if literal.startswith(remainder):
            if final:
                raise DecodingError("Invalid JSON text")
            return None
    raise DecodingError("Invalid JSON text")


def _parse_value(text: str, index: int, *, final: bool) -> tuple[Any, int] | None:
    """
    Parse one JSON value starting at ``index``.

    Return ``None`` when the value is incomplete and ``final`` is false.
    """
    if index >= len(text):
        if final:
            raise DecodingError("Unexpected end of JSON text")
        return None

    special = _scan_special(text, index, final=final)
    if special is None:
        return None
    if isinstance(special, tuple):
        return special

    first = text[index]
    if first == "-" or first.isdigit():
        end = _scan_json_number(text, index, final=final)
        if end is None:
            return None
        try:
            value, raw_end = _DECODER.raw_decode(text, index)
        except JSONDecodeError as exc:
            raise DecodingError("Invalid JSON number") from exc
        if raw_end != end:
            raise DecodingError("Invalid JSON number")
        return value, end

    if first in {"t", "f", "n"}:
        return _scan_literal(text, index, final=final)

    try:
        value, end = _DECODER.raw_decode(text, index)
    except JSONDecodeError as exc:
        if final:
            raise DecodingError("Invalid JSON text") from exc
        return None
    if end <= index:
        raise DecodingError("Invalid JSON text")
    if end == len(text) and not final and first not in {'"', "{", "["}:
        return None
    return value, end


def parse_exact_json_text(text: str) -> Any:
    """Parse one JSON text surrounded only by JSON whitespace."""
    index = _skip_ws(text, 0)
    if index >= len(text):
        raise DecodingError("JSON text is empty")
    parsed = _parse_value(text, index, final=True)
    if parsed is None:  # pragma: no cover
        raise DecodingError("JSON text is empty")
    value, end = parsed
    end = _skip_ws(text, end)
    if end != len(text):
        raise DecodingError("Trailing data after JSON text")
    return value


class _BufferedParser:
    def __init__(self) -> None:
        self.buffer = ""
        self.index = 0

    def _skip(self) -> None:
        self.index = _skip_ws(self.buffer, self.index)

    def _compact(self) -> None:
        if self.index >= 8192 or (self.index > 0 and self.index == len(self.buffer)):
            self.buffer = self.buffer[self.index :]
            self.index = 0


class _JSONDocumentParser(_BufferedParser):
    """One JSON document. Top-level arrays yield each element."""

    def __init__(self) -> None:
        super().__init__()
        self.state = "start"
        self._bom_checked = False
        self._array_needs_value = False
        self._held: Any = None
        self._has_held = False

    def feed(self, text: str) -> Iterator[Any]:
        if text:
            self.buffer += text
        yield from self._drain(final=False)

    def finish(self) -> Iterator[Any]:
        yield from self._drain(final=True)

    def _take_held(self) -> Any:
        held = self._held
        self._held = None
        self._has_held = False
        return held

    def _drain(self, *, final: bool) -> Iterator[Any]:
        while True:
            if self.state == "start":
                self._skip()
                if self.index >= len(self.buffer):
                    if final:
                        raise DecodingError("JSON document is empty")
                    break
                if not self._bom_checked:
                    self._bom_checked = True
                    if self.buffer[self.index] == _BOM:
                        self.index += 1
                        continue
                if self.buffer[self.index] == "[":
                    self.index += 1
                    self.state = "array_value"
                    self._array_needs_value = False
                    continue
                parsed = _parse_value(self.buffer, self.index, final=final)
                if parsed is None:
                    if final:
                        raise DecodingError("Invalid JSON text")
                    break
                value, end = parsed
                if _skip_ws(self.buffer, end) < len(self.buffer):
                    raise DecodingError("Trailing data after JSON document")
                # A later chunk may still contain trailing data, so the single
                # top-level value is emitted only once the payload has ended.
                self._held = value
                self._has_held = True
                self.index = end
                self.state = "after"
                continue

            if self.state == "array_value":
                self._skip()
                if self.index >= len(self.buffer):
                    if final:
                        raise DecodingError("Incomplete JSON array")
                    break
                if self.buffer[self.index] == "]":
                    if self._array_needs_value:
                        raise DecodingError("Invalid JSON array")
                    self.index += 1
                    self.state = "after"
                    continue
                parsed = _parse_value(self.buffer, self.index, final=final)
                if parsed is None:
                    if final:
                        raise DecodingError("Incomplete JSON array")
                    break
                value, end = parsed
                cursor = _skip_ws(self.buffer, end)
                if cursor < len(self.buffer) and self.buffer[cursor] not in ",]":
                    raise DecodingError("Trailing data in JSON array")
                self._held = value
                self._has_held = True
                self.index = end
                self.state = "array_sep"
                continue

            if self.state == "array_sep":
                self._skip()
                if self.index >= len(self.buffer):
                    if final:
                        raise DecodingError("Incomplete JSON array")
                    break
                character = self.buffer[self.index]
                if character == ",":
                    self.index += 1
                    self.state = "array_value"
                    self._array_needs_value = True
                    yield self._take_held()
                    continue
                if character == "]":
                    self.index += 1
                    self.state = "after"
                    yield self._take_held()
                    continue
                raise DecodingError("Trailing data in JSON array")

            self._skip()
            if self.index < len(self.buffer):
                raise DecodingError("Trailing data after JSON document")
            if final and self._has_held:
                yield self._take_held()
            break

        if final and self.state != "after":
            # ``start`` / array states already raise above when incomplete.
            if self.state == "start":
                raise DecodingError("JSON document is empty")
            raise DecodingError("Incomplete JSON document")
        self._compact()


class _NDJSONParser:
    """Newline-delimited JSON. Blank lines are ignored."""

    def __init__(self) -> None:
        self._partial = ""
        self._pending_cr = False
        self._allow_bom = True

    def feed(self, text: str) -> Iterator[Any]:
        yield from self._consume(text, final=False)

    def finish(self) -> Iterator[Any]:
        yield from self._consume("", final=True)

    def _consume(self, text: str, *, final: bool) -> Iterator[Any]:
        if self._pending_cr:
            text = "\r" + text
            self._pending_cr = False
        if not final and text.endswith("\r"):
            self._pending_cr = True
            text = text[:-1]

        data = self._partial + text
        self._partial = ""
        start = 0
        cursor = 0
        length = len(data)
        while cursor < length:
            character = data[cursor]
            if character != "\n" and character != "\r":
                cursor += 1
                continue
            yield from self._line(data[start:cursor])
            if character == "\r" and cursor + 1 < length and data[cursor + 1] == "\n":
                cursor += 2
            else:
                cursor += 1
            start = cursor
        remainder = data[start:]
        if final:
            if remainder:
                yield from self._line(remainder)
        else:
            self._partial = remainder

    def _line(self, line: str) -> list[Any]:
        if _is_json_ws(line):
            return []
        if self._allow_bom:
            self._allow_bom = False
            if line.startswith(_BOM):
                line = line[1:]
                if _is_json_ws(line):
                    raise DecodingError("JSON text is empty")
        elif line.startswith(_BOM):
            raise DecodingError("Unexpected UTF-8 BOM in NDJSON")
        return [parse_exact_json_text(line)]


class _JSONSeqParser(_BufferedParser):
    """
    JSON text sequence (RFC 7464), with the record rules used by ``iter_json``.

    An empty or whitespace-only payload yields nothing. After the first record
    separator, a final record that does not contain a JSON text is an error.
    Whitespace-only records are ignored only when another record separator
    follows them.
    """

    def __init__(self) -> None:
        super().__init__()
        self.phase = "lead"
        self._held: Any = None
        self._has_held = False

    def feed(self, text: str) -> Iterator[Any]:
        if text:
            self.buffer += text
        yield from self._drain(final=False)

    def finish(self) -> Iterator[Any]:
        yield from self._drain(final=True)

    def _take_held(self) -> Any:
        held = self._held
        self._held = None
        self._has_held = False
        return held

    def _drain(self, *, final: bool) -> Iterator[Any]:
        while True:
            if self.phase == "lead":
                self._skip()
                if self.index >= len(self.buffer):
                    break
                if self.buffer[self.index] != _RS:
                    raise DecodingError(
                        "JSON text sequence must start with a record separator"
                    )
                self.index += 1
                self.phase = "value"
                continue

            if self.phase == "value":
                self._skip()
                if self.index >= len(self.buffer):
                    if final:
                        raise DecodingError(
                            "JSON text sequence ended inside an empty record"
                        )
                    break
                if self.buffer[self.index] == _RS:
                    self.index += 1
                    continue
                parsed = _parse_value(self.buffer, self.index, final=final)
                if parsed is None:
                    if final:
                        raise DecodingError("Invalid JSON text sequence record")
                    break
                value, end = parsed
                cursor = _skip_ws(self.buffer, end)
                if cursor < len(self.buffer) and self.buffer[cursor] != _RS:
                    raise DecodingError("Trailing data in JSON text sequence record")
                if cursor < len(self.buffer):
                    self.index = cursor + 1
                    self.phase = "value"
                    yield value
                    continue
                # Whitespace may be followed by trailing data in a later chunk.
                self._held = value
                self._has_held = True
                self.index = end
                self.phase = "tail"
                continue

            self._skip()
            if self.index >= len(self.buffer):
                if final and self._has_held:
                    yield self._take_held()
                break
            if self.buffer[self.index] != _RS:
                raise DecodingError("Trailing data in JSON text sequence record")
            self.index += 1
            self.phase = "value"
            if self._has_held:
                yield self._take_held()

        self._compact()


def parser_for_json_format(
    json_format: str,
) -> _JSONDocumentParser | _NDJSONParser | _JSONSeqParser:
    if json_format == "json":
        return _JSONDocumentParser()
    if json_format == "ndjson":
        return _NDJSONParser()
    if json_format == "json-seq":
        return _JSONSeqParser()
    raise DecodingError(
        "Response Content-Type is not a JSON media type"
    )  # pragma: no cover
