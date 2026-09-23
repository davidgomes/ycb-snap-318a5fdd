"""
Incremental JSON iterators for response bodies.

Supports a single JSON document (``application/json`` and ``application/*+json``),
newline-delimited JSON, and RFC 7464 JSON text sequences.
"""

from __future__ import annotations

import codecs
import email.message
import json
import typing

from ._exceptions import DecodingError

_JSON_WS = " \t\r\n"
_NEED_MORE = object()


def resolve_json_content_type(content_type: str | None) -> tuple[str, str | None]:
    """
    Return ``(format, charset)`` for a JSON streaming media type.

    ``format`` is ``"json"``, ``"ndjson"``, or ``"json-seq"``.
    ``charset`` is ``None`` when the caller should autodetect the JSON encoding.
    """
    if content_type is None or not content_type.strip():
        raise DecodingError(
            "Response is missing a Content-Type header for JSON streaming"
        )

    message = email.message.Message()
    message["content-type"] = content_type
    maintype = message.get_content_maintype()
    subtype = message.get_content_subtype()
    if maintype != "application" or not subtype:
        raise DecodingError(
            f"Response Content-Type {content_type!r} is not a JSON media type"
        )

    if subtype == "json" or subtype.endswith("+json"):
        kind = "json"
    elif subtype in {"ndjson", "x-ndjson"}:
        kind = "ndjson"
    elif subtype == "json-seq":
        kind = "json-seq"
    else:
        raise DecodingError(
            f"Response Content-Type {content_type!r} is not a JSON media type"
        )

    charset: str | None = None
    for key, value in message.get_params() or []:
        if key.lower() == "charset":
            charset = value
    if charset is not None and not _is_known_encoding(charset):
        raise DecodingError(
            f"Response Content-Type charset {charset!r} is not a known encoding"
        )
    return kind, charset


def _is_known_encoding(encoding: str) -> bool:
    try:
        codecs.lookup(encoding)
    except LookupError:
        return False
    return True


def detect_json_encoding(data: bytes) -> str:
    """
    Detect a JSON text encoding from its leading bytes.

    Matches the UTF-8/16/32 detection used by :mod:`json`, including a UTF-8 BOM.
    """
    if data.startswith((codecs.BOM_UTF32_BE, codecs.BOM_UTF32_LE)):
        return "utf-32"
    if data.startswith((codecs.BOM_UTF16_BE, codecs.BOM_UTF16_LE)):
        return "utf-16"
    if data.startswith(codecs.BOM_UTF8):
        return "utf-8-sig"

    if len(data) >= 4:
        if not data[0]:
            return "utf-16-be" if data[1] else "utf-32-be"
        if not data[1]:
            return "utf-16-le" if data[2] or data[3] else "utf-32-le"
    elif len(data) == 2:
        if not data[0]:
            return "utf-16-be"
        if not data[1]:
            return "utf-16-le"
    return "utf-8"


class _JSONTextDecoder:
    """Decode a JSON byte stream, honoring an explicit charset when given."""

    def __init__(self, encoding: str | None) -> None:
        self._encoding = encoding
        self._pending = b""
        self._decoder: codecs.IncrementalDecoder | None
        if encoding is None:
            self._decoder = None
        else:
            self._decoder = codecs.getincrementaldecoder(encoding)(errors="strict")

    def decode(self, data: bytes) -> str:
        try:
            if self._decoder is None:
                self._pending += data
                if len(self._pending) < 4:
                    return ""
                encoding = detect_json_encoding(self._pending)
                self._decoder = codecs.getincrementaldecoder(encoding)(errors="strict")
                pending = self._pending
                self._pending = b""
                return self._decoder.decode(pending)
            return self._decoder.decode(data)
        except UnicodeDecodeError as exc:
            raise DecodingError(str(exc)) from exc

    def flush(self) -> str:
        try:
            if self._decoder is None:
                if not self._pending:
                    return ""
                encoding = detect_json_encoding(self._pending)
                self._decoder = codecs.getincrementaldecoder(encoding)(errors="strict")
                pending = self._pending
                self._pending = b""
                return self._decoder.decode(pending) + self._decoder.decode(b"", True)
            return self._decoder.decode(b"", True)
        except UnicodeDecodeError as exc:
            raise DecodingError(str(exc)) from exc


def iter_json_bytes(
    chunks: typing.Iterator[bytes], kind: str, encoding: str | None
) -> typing.Iterator[typing.Any]:
    decoder = _JSONTextDecoder(encoding)
    parser = _parser_for(kind)
    for chunk in chunks:
        text = decoder.decode(chunk)
        if text:
            yield from parser.feed(text)
    tail = decoder.flush()
    if tail:
        yield from parser.feed(tail)
    yield from parser.finish()


async def aiter_json_bytes(
    chunks: typing.AsyncIterator[bytes], kind: str, encoding: str | None
) -> typing.AsyncIterator[typing.Any]:
    decoder = _JSONTextDecoder(encoding)
    parser = _parser_for(kind)
    async for chunk in chunks:
        text = decoder.decode(chunk)
        if text:
            for value in parser.feed(text):
                yield value
    tail = decoder.flush()
    if tail:
        for value in parser.feed(tail):
            yield value
    for value in parser.finish():
        yield value


class _Parser(typing.Protocol):
    def feed(self, text: str) -> list[typing.Any]: ...

    def finish(self) -> list[typing.Any]: ...


def _parser_for(kind: str) -> _Parser:
    if kind == "json":
        return _JSONDocumentParser()
    if kind == "ndjson":
        return _NDJSONParser()
    if kind == "json-seq":
        return _JSONSeqParser()
    raise DecodingError(f"Unsupported JSON stream format {kind!r}")  # pragma: no cover


def _loads_one(text: str) -> typing.Any:
    try:
        return json.loads(text)
    except json.JSONDecodeError as exc:
        raise DecodingError(str(exc)) from exc


def _scan_number(text: str, eof: bool) -> int | None:
    """
    Return the end index of a JSON number that is followed by a delimiter.

    ``None`` means more bytes are required. A number that runs to the end of
    ``text`` is complete only at EOF, because another digit or exponent may follow.
    """
    index = 0
    size = len(text)
    if index < size and text[index] == "-":
        index += 1
        if index >= size:
            if eof:
                raise DecodingError("Invalid JSON number")
            return None
    if index >= size or not text[index].isdigit():
        raise DecodingError("Invalid JSON number")
    if text[index] == "0":
        index += 1
        if index < size and text[index].isdigit():
            raise DecodingError("Invalid JSON number")
    else:
        while index < size and text[index].isdigit():
            index += 1
    if index < size and text[index] == ".":
        index += 1
        if index >= size:
            if eof:
                raise DecodingError("Invalid JSON number")
            return None
        if not text[index].isdigit():
            raise DecodingError("Invalid JSON number")
        while index < size and text[index].isdigit():
            index += 1
    if index < size and text[index] in "eE":
        index += 1
        if index < size and text[index] in "+-":
            index += 1
        if index >= size:
            if eof:
                raise DecodingError("Invalid JSON number")
            return None
        if not text[index].isdigit():
            raise DecodingError("Invalid JSON number")
        while index < size and text[index].isdigit():
            index += 1
    if index == size and not eof:
        return None
    return index


class _JSONDocumentParser:
    """Parse one JSON text, yielding array elements or a single value."""

    def __init__(self) -> None:
        self._buf = ""
        self._decoder = json.JSONDecoder()
        self._state = "start"
        self._saw_bom = False

    def feed(self, text: str) -> list[typing.Any]:
        self._buf += text
        return self._drain(eof=False)

    def finish(self) -> list[typing.Any]:
        return self._drain(eof=True)

    def _skip_ws(self) -> None:
        self._buf = self._buf.lstrip(_JSON_WS)

    def _skip_intro(self) -> None:
        while True:
            self._skip_ws()
            if not self._saw_bom and self._buf.startswith("\ufeff"):
                self._saw_bom = True
                self._buf = self._buf[1:]
                continue
            return

    def _reject_trailing(self) -> None:
        if self._buf.lstrip(_JSON_WS):
            raise DecodingError("Trailing data after JSON value")
        self._buf = ""

    def _read_value(self, eof: bool) -> typing.Any:
        if not self._buf:
            return _NEED_MORE
        if self._buf[0] == "-" or self._buf[0].isdigit():
            end = _scan_number(self._buf, eof)
            if end is None:
                return _NEED_MORE
            fragment = self._buf[:end]
            try:
                value, index = self._decoder.raw_decode(fragment)
            except json.JSONDecodeError as exc:
                raise DecodingError(str(exc)) from exc
            self._buf = self._buf[index:]
            return value
        try:
            value, index = self._decoder.raw_decode(self._buf)
        except json.JSONDecodeError as exc:
            if eof:
                raise DecodingError(str(exc)) from exc
            return _NEED_MORE
        self._buf = self._buf[index:]
        return value

    def _drain(self, eof: bool) -> list[typing.Any]:
        values: list[typing.Any] = []
        while True:
            if self._state == "done":
                self._reject_trailing()
                break
            if self._state == "start":
                self._skip_intro()
                if not self._buf:
                    if eof:
                        raise DecodingError(
                            "Attempted to decode an empty JSON document"
                        )
                    break
                if self._buf[0] == "[":
                    self._buf = self._buf[1:]
                    self._state = "element"
                    continue
                item = self._read_value(eof)
                if item is _NEED_MORE:
                    break
                values.append(item)
                self._state = "done"
                continue
            if self._state == "element":
                self._skip_ws()
                if not self._buf:
                    if eof:
                        raise DecodingError("Unterminated JSON array")
                    break
                if self._buf[0] == "]":
                    self._buf = self._buf[1:]
                    self._state = "done"
                    continue
                item = self._read_value(eof)
                if item is _NEED_MORE:
                    break
                values.append(item)
                self._state = "comma"
                continue
            if self._state == "comma":
                self._skip_ws()
                if not self._buf:
                    if eof:
                        raise DecodingError("Unterminated JSON array")
                    break
                if self._buf[0] == "]":
                    self._buf = self._buf[1:]
                    self._state = "done"
                    continue
                if self._buf[0] != ",":
                    raise DecodingError("Expecting ',' delimiter in JSON array")
                self._buf = self._buf[1:]
                self._state = "required"
                continue
            # A value is required (for example after a comma). `]` is an error.
            self._skip_ws()
            if not self._buf:
                if eof:
                    raise DecodingError("Unterminated JSON array")
                break
            if self._buf[0] == "]":
                raise DecodingError("Trailing comma in JSON array")
            item = self._read_value(eof)
            if item is _NEED_MORE:
                break
            values.append(item)
            self._state = "comma"
        return values


class _LineBuffer:
    """Split text on LF, CR, and CRLF only."""

    def __init__(self) -> None:
        self._buf = ""
        self._skip_lf = False

    def feed(self, text: str) -> list[str]:
        lines: list[str] = []
        if self._skip_lf:
            self._skip_lf = False
            if text.startswith("\n"):
                text = text[1:]
        self._buf += text
        while self._buf:
            newline = self._buf.find("\n")
            carriage = self._buf.find("\r")
            if newline == -1 and carriage == -1:
                break
            if carriage != -1 and (newline == -1 or carriage < newline):
                lines.append(self._buf[:carriage])
                if carriage + 1 == len(self._buf):
                    self._buf = ""
                    self._skip_lf = True
                    break
                if self._buf[carriage + 1] == "\n":
                    self._buf = self._buf[carriage + 2 :]
                else:
                    self._buf = self._buf[carriage + 1 :]
            else:
                lines.append(self._buf[:newline])
                self._buf = self._buf[newline + 1 :]
        return lines

    def flush(self) -> list[str]:
        self._skip_lf = False
        if not self._buf:
            return []
        line = self._buf
        self._buf = ""
        return [line]


class _NDJSONParser:
    def __init__(self) -> None:
        self._lines = _LineBuffer()
        self._saw_value_line = False

    def feed(self, text: str) -> list[typing.Any]:
        return self._parse_lines(self._lines.feed(text))

    def finish(self) -> list[typing.Any]:
        return self._parse_lines(self._lines.flush())

    def _parse_lines(self, lines: list[str]) -> list[typing.Any]:
        values: list[typing.Any] = []
        for line in lines:
            if line.strip(_JSON_WS) == "":
                continue
            if not self._saw_value_line and line.startswith("\ufeff"):
                line = line[1:]
            self._saw_value_line = True
            values.append(_loads_one(line))
        return values


class _JSONSeqParser:
    def __init__(self) -> None:
        self._buf = ""
        self._in_record = False

    def feed(self, text: str) -> list[typing.Any]:
        self._buf += text
        return self._drain(eof=False)

    def finish(self) -> list[typing.Any]:
        return self._drain(eof=True)

    def _drain(self, eof: bool) -> list[typing.Any]:
        values: list[typing.Any] = []
        while True:
            if not self._in_record:
                self._buf = self._buf.lstrip(_JSON_WS)
                if not self._buf:
                    break
                if self._buf[0] != "\x1e":
                    raise DecodingError(
                        "JSON text sequence must begin with a record separator"
                    )
                self._buf = self._buf[1:]
                self._in_record = True
                continue
            separator = self._buf.find("\x1e")
            if separator == -1:
                if not eof:
                    break
                values.extend(self._parse_record(self._buf, final=True))
                self._buf = ""
                self._in_record = False
                break
            record = self._buf[:separator]
            self._buf = self._buf[separator + 1 :]
            values.extend(self._parse_record(record, final=False))
        return values

    def _parse_record(self, record: str, final: bool) -> list[typing.Any]:
        if record.endswith("\n"):
            record = record[:-1]
        if record.strip(_JSON_WS) == "":
            if final:
                raise DecodingError(
                    "JSON text sequence ended inside an empty record"
                )
            return []
        return [_loads_one(record)]
