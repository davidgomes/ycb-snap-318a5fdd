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

_Mode = typing.Literal["json", "ndjson", "json-seq"]

# JSON whitespace is only these four characters (RFC 8259).
_JSON_WHITESPACE = " \t\r\n"
_RECORD_SEPARATOR = "\x1e"
_DECODER = json.JSONDecoder()


def _is_json_whitespace(text: str) -> bool:
    return not text.strip(_JSON_WHITESPACE)


def _detect_json_encoding(data: bytes) -> str:
    """
    Detect the encoding of a JSON byte string.

    Matches :func:`json.detect_encoding`: UTF-8/16/32, including a leading BOM.
    """
    if data.startswith((codecs.BOM_UTF32_BE, codecs.BOM_UTF32_LE)):
        return "utf-32"
    if data.startswith((codecs.BOM_UTF16_BE, codecs.BOM_UTF16_LE)):
        return "utf-16"
    if data.startswith(codecs.BOM_UTF8):
        return "utf-8-sig"

    if len(data) >= 4:
        if not data[0]:
            # 00 00 -- -- - utf-32-be
            # 00 XX -- -- - utf-16-be
            return "utf-16-be" if data[1] else "utf-32-be"
        if not data[1]:
            # XX 00 00 00 - utf-32-le
            # XX 00 XX 00 - utf-16-le
            return "utf-16-le" if data[2] or data[3] else "utf-32-le"
    elif len(data) == 2:
        if not data[0]:
            # 00 XX - utf-16-be
            return "utf-16-be"
        if not data[1]:
            # XX 00 - utf-16-le
            return "utf-16-le"
    return "utf-8"


def _is_known_encoding(encoding: str) -> bool:
    """Return True if `encoding` is a text codec that can decode bytes."""
    try:
        decoder = codecs.getincrementaldecoder(encoding)(errors="strict")
        decoded = decoder.decode(b"", final=True)
    except (LookupError, UnicodeError, TypeError, ValueError):
        return False
    return isinstance(decoded, str)


def _json_mode(subtype: str) -> _Mode | None:
    if subtype == "json" or (len(subtype) > len("+json") and subtype.endswith("+json")):
        return "json"
    if subtype in {"ndjson", "x-ndjson"}:
        return "ndjson"
    if subtype == "json-seq":
        return "json-seq"
    return None


def parse_json_content_type(content_type: str | None) -> tuple[_Mode, str | None]:
    """
    Return ``(mode, charset)`` for a JSON response ``Content-Type``.

    ``charset`` is ``None`` when the header does not include one. Raises
    :class:`DecodingError` for a non-JSON media type or an unknown charset.
    """
    if content_type is None or not content_type.strip():
        raise DecodingError("Response Content-Type is not a JSON media type")

    message = email.message.Message()
    message["content-type"] = content_type
    mode = None
    if message.get_content_maintype() == "application":
        mode = _json_mode(message.get_content_subtype())
    if mode is None:
        raise DecodingError("Response Content-Type is not a JSON media type")

    missing = object()
    if message.get_param("charset", missing) is missing:
        return mode, None

    charset = message.get_content_charset()
    if not charset or not _is_known_encoding(charset):
        raise DecodingError("Response Content-Type charset is not a valid encoding")
    return mode, charset


def _parse_exact_json(text: str) -> typing.Any:
    """Parse one JSON value surrounded only by JSON whitespace."""
    start = 0
    limit = len(text)
    while start < limit and text[start] in _JSON_WHITESPACE:
        start += 1
    try:
        value, end = _DECODER.raw_decode(text, start)
    except json.JSONDecodeError as exc:
        raise DecodingError("Invalid JSON document") from exc
    if not _is_json_whitespace(text[end:]):
        raise DecodingError("Trailing data after JSON value")
    return value


def _is_number_literal(text: str, start: int, end: int) -> bool:
    head = text[start:end].lstrip(_JSON_WHITESPACE)
    return bool(head) and head[0] in "-0123456789"


class JSONStreamParser:
    """
    Feed decoded response bytes and iterate parsed JSON values.

    Bytes are accepted incrementally. Values are produced as soon as a complete
    JSON value (or array element, line, or record) is available.
    """

    def __init__(self, content_type: str | None) -> None:
        mode, charset = parse_json_content_type(content_type)
        self._mode: _Mode = mode
        self._charset = charset
        self._byte_buffer = bytearray()
        self._decoder: codecs.IncrementalDecoder | None = None
        self._text = ""
        self._pos = 0
        self._state = "start"
        self._bom_skipped = False
        self._expect_value = False
        self._saw_ndjson_value_line = False
        # Set when encoding detection consumes a leading UTF-8 BOM, so NDJSON
        # can still reject a BOM that is not on the first non-blank line.
        self._stripped_leading_utf8_bom = False
        self._ndjson_bom_checked = False

    def feed(self, data: bytes) -> typing.Iterator[typing.Any]:
        self._append_bytes(data, final=False)
        try:
            yield from self._drive(final=False)
        finally:
            self._compact()

    def finish(self) -> typing.Iterator[typing.Any]:
        self._append_bytes(b"", final=True)
        try:
            yield from self._drive(final=True)
        finally:
            self._compact()

    def _append_bytes(self, data: bytes, final: bool) -> None:
        text = self._decode_bytes(data, final=final)
        if text:
            self._text += text

    def _decode_bytes(self, data: bytes, final: bool) -> str:
        if self._decoder is None:
            if self._charset is None:
                self._byte_buffer.extend(data)
                # Four octets are enough to distinguish UTF-8/16/32. Shorter
                # prefixes stay buffered until the body ends.
                if not final and len(self._byte_buffer) < 4:
                    return ""
                encoding = _detect_json_encoding(bytes(self._byte_buffer))
                pending = bytes(self._byte_buffer)
                self._byte_buffer.clear()
                if encoding == "utf-8-sig":
                    self._stripped_leading_utf8_bom = True
            else:
                encoding = self._charset
                pending = data
            self._decoder = codecs.getincrementaldecoder(encoding)(errors="strict")
            data = pending
        try:
            return self._decoder.decode(data, final)
        except UnicodeDecodeError as exc:
            raise DecodingError(str(exc)) from exc

    def _compact(self) -> None:
        if self._pos:
            self._text = self._text[self._pos :]
            self._pos = 0

    def _drive(self, final: bool) -> typing.Iterator[typing.Any]:
        if self._mode == "json":
            yield from self._iter_json_document(final)
        elif self._mode == "ndjson":
            yield from self._iter_ndjson(final)
        else:
            yield from self._iter_json_seq(final)

    def _skip_ws(self) -> None:
        text = self._text
        pos = self._pos
        limit = len(text)
        while pos < limit and text[pos] in _JSON_WHITESPACE:
            pos += 1
        self._pos = pos

    def _skip_leading(self) -> None:
        """Skip leading JSON whitespace and a single U+FEFF BOM."""
        text = self._text
        pos = self._pos
        limit = len(text)
        bom_skipped = self._bom_skipped
        while pos < limit:
            char = text[pos]
            if char in _JSON_WHITESPACE:
                pos += 1
                continue
            if char == "\ufeff" and not bom_skipped:
                bom_skipped = True
                pos += 1
                continue
            break
        self._pos = pos
        self._bom_skipped = bom_skipped

    def _read_value(self, final: bool) -> tuple[bool, typing.Any]:
        """
        Parse one JSON value at the current position.

        Returns ``(False, None)`` when the buffered text is an incomplete value.
        A number that ends at the buffer boundary is treated as incomplete so a
        later chunk can extend it.
        """
        try:
            value, end = _DECODER.raw_decode(self._text, self._pos)
        except json.JSONDecodeError as exc:
            if final:
                raise DecodingError("Invalid JSON document") from exc
            return False, None
        if (
            not final
            and end == len(self._text)
            and _is_number_literal(self._text, self._pos, end)
        ):
            return False, None
        self._pos = end
        return True, value

    def _iter_json_document(self, final: bool) -> typing.Iterator[typing.Any]:
        while self._state != "done":
            if self._state == "start":
                self._skip_leading()
                if self._pos >= len(self._text):
                    if final:
                        raise DecodingError("JSON document is empty")
                    return
                if self._text[self._pos] == "[":
                    self._pos += 1
                    self._state = "element"
                    self._expect_value = False
                    continue
                ok, value = self._read_value(final)
                if not ok:
                    return
                yield value
                self._state = "trailing"
                continue

            if self._state == "element":
                self._skip_ws()
                if self._pos >= len(self._text):
                    if final:
                        raise DecodingError("Invalid JSON document")
                    return
                if self._text[self._pos] == "]":
                    if self._expect_value:
                        raise DecodingError("Invalid JSON document")
                    self._pos += 1
                    self._state = "trailing"
                    continue
                ok, value = self._read_value(final)
                if not ok:
                    return
                yield value
                self._state = "separator"
                continue

            if self._state == "separator":
                self._skip_ws()
                if self._pos >= len(self._text):
                    if final:
                        raise DecodingError("Invalid JSON document")
                    return
                char = self._text[self._pos]
                if char == ",":
                    self._pos += 1
                    self._state = "element"
                    self._expect_value = True
                    continue
                if char == "]":
                    self._pos += 1
                    self._state = "trailing"
                    continue
                raise DecodingError("Invalid JSON document")

            self._skip_ws()
            if self._pos < len(self._text):
                raise DecodingError("Trailing data after JSON value")
            if final:
                self._state = "done"
            return

    def _next_line(self, final: bool) -> str | None:
        """
        Return the next LF/CR/CRLF-delimited line without its terminator.

        A trailing CR is held back when more bytes may arrive, so it can still
        form a CRLF pair. ``None`` means the current line is incomplete, or
        that the buffer has been consumed when ``final`` is set.
        """
        text = self._text
        start = self._pos
        limit = len(text)
        index = start
        while index < limit:
            char = text[index]
            if char == "\n":
                line = text[start:index]
                self._pos = index + 1
                return line
            if char == "\r":
                if index + 1 == limit and not final:
                    return None
                newline = 2 if index + 1 < limit and text[index + 1] == "\n" else 1
                line = text[start:index]
                self._pos = index + newline
                return line
            index += 1
        if final and start < limit:
            line = text[start:]
            self._pos = limit
            return line
        return None

    def _iter_ndjson(self, final: bool) -> typing.Iterator[typing.Any]:
        if self._stripped_leading_utf8_bom and not self._ndjson_bom_checked:
            # The codec already removed a BOM at the start of the payload.
            # That is valid only when the first line still contains JSON; a
            # line break before that value means the BOM was on its own line.
            for char in self._text[self._pos :]:
                if char in "\r\n":
                    raise DecodingError(
                        "UTF-8 BOM is only allowed at the start of the first "
                        "non-blank line"
                    )
                if char not in _JSON_WHITESPACE:
                    self._ndjson_bom_checked = True
                    break
            else:
                if not final:
                    return
                self._ndjson_bom_checked = True

        while True:
            line = self._next_line(final)
            if line is None:
                return
            if _is_json_whitespace(line):
                continue
            if not self._saw_ndjson_value_line:
                self._saw_ndjson_value_line = True
                if line.startswith("\ufeff"):
                    line = line[1:]
                    if _is_json_whitespace(line):
                        raise DecodingError("Invalid JSON document")
            yield _parse_exact_json(line)

    def _emit_record(
        self, record: str, followed_by_rs: bool
    ) -> typing.Iterator[typing.Any]:
        if record.endswith("\n"):
            record = record[:-1]
        if _is_json_whitespace(record):
            if followed_by_rs:
                return
            raise DecodingError("Incomplete JSON text sequence record")
        yield _parse_exact_json(record)

    def _iter_json_seq(self, final: bool) -> typing.Iterator[typing.Any]:
        if self._state == "start":
            self._skip_ws()
            if self._pos >= len(self._text):
                if final:
                    # Empty or whitespace-only sequences yield nothing.
                    self._state = "done"
                return
            if self._text[self._pos] != _RECORD_SEPARATOR:
                raise DecodingError(
                    "JSON text sequence must start with a record separator"
                )
            self._pos += 1
            self._state = "record"

        while self._state == "record":
            separator = self._text.find(_RECORD_SEPARATOR, self._pos)
            if separator == -1:
                if not final:
                    return
                record = self._text[self._pos :]
                self._pos = len(self._text)
                self._state = "done"
                yield from self._emit_record(record, followed_by_rs=False)
                return
            record = self._text[self._pos : separator]
            self._pos = separator + 1
            yield from self._emit_record(record, followed_by_rs=True)
