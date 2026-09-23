"""
Incremental JSON iterators for response bodies.

Supports JSON documents (`application/json` and `application/*+json`),
newline-delimited JSON, and RFC 7464 JSON text sequences.
"""

from __future__ import annotations

import codecs
import email.message
import json
import typing

from ._exceptions import DecodingError

# JSON whitespace is only SP, HTAB, LF, and CR (RFC 8259).
# str.isspace() is not usable here: it treats U+001E, the JSON text
# sequence record separator, as whitespace.
_JSON_WS = " \t\r\n"
_UTF8_BOM = "\ufeff"
_RS = "\x1e"

_DOCUMENT = "document"
_NDJSON = "ndjson"
_SEQUENCE = "sequence"

_JSON_SUBTYPES = frozenset({"json", "ndjson", "x-ndjson", "json-seq"})
_JSON_SUFFIX = "+json"


class _NeedMore:
    """Sentinel: a JSON value is not complete in the current buffer."""


class _BlankLine:
    """Sentinel: an NDJSON line carried no JSON value."""


_NEED_MORE = _NeedMore()
_BLANK = _BlankLine()


def _is_known_encoding(encoding: str) -> bool:
    if not encoding:
        return False
    try:
        codecs.lookup(encoding)
    except LookupError:
        return False
    return True


def _skip_ws(text: str, index: int) -> int:
    length = len(text)
    while index < length and text[index] in _JSON_WS:
        index += 1
    return index


def _is_json_ws_only(text: str) -> bool:
    return _skip_ws(text, 0) == len(text)


def _is_supported_json_subtype(subtype: str) -> bool:
    if subtype in _JSON_SUBTYPES:
        return True
    # ``application/*+json`` requires a non-empty prefix before ``+json``.
    # Other type trees (for example ``image/svg+json``) are rejected by the
    # main-type check.
    return len(subtype) > len(_JSON_SUFFIX) and subtype.endswith(_JSON_SUFFIX)


def _resolve_json_content_type(content_type: str | None) -> tuple[str, str | None]:
    """
    Return ``(mode, charset)`` for a supported JSON ``Content-Type``.

    ``charset`` is ``None`` when the header does not include one, in which
    case the body is decoded with JSON encoding detection. An explicit
    charset must name a known codec.
    """
    if content_type is None or not content_type.strip():
        raise DecodingError("Response Content-Type is not a supported JSON media type")

    message = email.message.Message()
    message["content-type"] = content_type
    maintype = message.get_content_maintype()
    subtype = message.get_content_subtype()
    if maintype != "application" or not _is_supported_json_subtype(subtype):
        raise DecodingError(
            "Response Content-Type is not a supported JSON media type: "
            f"{content_type!r}"
        )

    charset = message.get_content_charset(failobj=None)
    if charset is not None and not _is_known_encoding(charset):
        raise DecodingError(
            "Response Content-Type charset is not a valid encoding: "
            f"{charset!r}"
        )

    if subtype in {"ndjson", "x-ndjson"}:
        return _NDJSON, charset
    if subtype == "json-seq":
        return _SEQUENCE, charset
    return _DOCUMENT, charset


def _detect_json_encoding(data: bytes) -> str:
    """
    Detect the encoding of a JSON byte string.

    This matches :func:`json.loads` on bytes: UTF-8, UTF-16, or UTF-32,
    including a leading BOM (RFC 8259).
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
            # XX 00 XX -- - utf-16-le
            return "utf-16-le" if data[2] or data[3] else "utf-32-le"
    elif len(data) == 2:
        if not data[0]:
            return "utf-16-be"
        if not data[1]:
            return "utf-16-le"
    return "utf-8"


def _json_decoder(kwargs: dict[str, typing.Any]) -> json.JSONDecoder:
    options = dict(kwargs)
    decoder_cls = options.pop("cls", json.JSONDecoder)
    decoder = typing.cast(json.JSONDecoder, decoder_cls(**options))
    return decoder


def _scan_number(text: str, start: int) -> int:
    """
    Return the end of the longest JSON-number prefix starting at ``start``.

    The prefix may be an incomplete or illegal number. It always extends at
    least through a leading ``-`` or digit when one is present, and through
    any following digits, fraction, or exponent that are already buffered.
    """
    length = len(text)
    index = start
    if index < length and text[index] == "-":
        index += 1
    if index >= length or not ("0" <= text[index] <= "9"):
        return index

    index += 1
    while index < length and "0" <= text[index] <= "9":
        index += 1
    if index < length and text[index] == ".":
        index += 1
        while index < length and "0" <= text[index] <= "9":
            index += 1
    if index < length and text[index] in "eE":
        index += 1
        if index < length and text[index] in "+-":
            index += 1
        while index < length and "0" <= text[index] <= "9":
            index += 1
    return index


def _split_lines(text: str, *, final: bool) -> tuple[list[str], str]:
    """
    Split ``text`` on LF, CR, and CRLF.

    A trailing CR is kept in the remainder unless ``final`` is set, so a
    CRLF split across chunks stays one line break. The remainder is the
    unfinished tail (empty when ``final``).
    """
    lines: list[str] = []
    start = 0
    index = 0
    length = len(text)
    while index < length:
        char = text[index]
        if char == "\n":
            lines.append(text[start:index])
            index += 1
            start = index
        elif char == "\r":
            if index + 1 == length and not final:
                break
            lines.append(text[start:index])
            index += 1
            if index < length and text[index] == "\n":
                index += 1
            start = index
        else:
            index += 1
    if final:
        if start < length:
            lines.append(text[start:])
        return lines, ""
    return lines, text[start:]


class _JSONTextDecoder:
    """Incrementally decode JSON bytes using a charset or encoding detection."""

    def __init__(self, encoding: str | None) -> None:
        self._encoding = encoding
        self._pending = bytearray()
        self._decoder: codecs.IncrementalDecoder | None = None

    def decode(self, data: bytes, *, final: bool) -> str:
        if self._decoder is None:
            if self._encoding is None:
                self._pending.extend(data)
                # Four bytes are enough to distinguish UTF-8/16/32, including BOMs.
                if not final and len(self._pending) < 4:
                    return ""
                encoding = _detect_json_encoding(bytes(self._pending))
                data = bytes(self._pending)
                self._pending.clear()
            else:
                encoding = self._encoding
            try:
                self._decoder = codecs.getincrementaldecoder(encoding)()
            except LookupError as exc:  # pragma: no cover
                raise DecodingError(
                    "Response Content-Type charset is not a valid encoding: "
                    f"{encoding!r}"
                ) from exc
        try:
            return self._decoder.decode(data, final)
        except UnicodeError as exc:
            # Includes UnicodeDecodeError and codec errors such as
            # "UTF-16 stream does not start with BOM".
            raise DecodingError(f"Could not decode JSON content: {exc}") from exc


class _JSONParser:
    """
    Pull parser that turns a decoded JSON byte stream into values.

    ``feed`` and ``finish`` return values that are complete. ``finish`` must
    be called once, after the last byte.
    """

    def __init__(self, mode: str, encoding: str | None, **kwargs: typing.Any) -> None:
        self._mode = mode
        self._decoder = _json_decoder(kwargs)
        self._text_decoder = _JSONTextDecoder(encoding)
        self._buf = ""
        self._pos = 0
        # Document state.
        self._state = "start"
        self._bom_checked = False
        # NDJSON: a UTF-8 BOM is allowed only on the first non-blank line.
        self._ndjson_bom_allowed = True
        # JSON text sequence: leading whitespace is skipped once.
        self._seq_started = False

    def feed(self, data: bytes) -> list[typing.Any]:
        text = self._text_decoder.decode(data, final=False)
        if not text:
            return []
        return self._consume(text, final=False)

    def finish(self) -> list[typing.Any]:
        text = self._text_decoder.decode(b"", final=True)
        return self._consume(text, final=True)

    def _consume(self, text: str, *, final: bool) -> list[typing.Any]:
        if self._mode == _NDJSON:
            return self._consume_ndjson(text, final=final)
        if self._mode == _SEQUENCE:
            return self._consume_sequence(text, final=final)
        return self._consume_document(text, final=final)

    def _parse_number(
        self, text: str, start: int, final: bool
    ) -> tuple[typing.Any, int] | _NeedMore:
        # Digits, '.', and an exponent can all extend a number that is valid
        # on its own. Hold a number that reaches the end of the buffer so the
        # next chunk can grow it (``12`` + ``3``) or terminate it (``12`` + ``,``).
        end = _scan_number(text, start)
        if end == len(text) and not final:
            return _NEED_MORE
        try:
            value, parsed_end = self._decoder.raw_decode(text, start)
        except json.JSONDecodeError as exc:
            raise DecodingError(f"Invalid JSON value: {exc.msg}") from exc
        if parsed_end != end:
            raise DecodingError("Invalid JSON number")
        return value, parsed_end

    def _parse_value(
        self, text: str, start: int, final: bool
    ) -> tuple[typing.Any, int] | _NeedMore:
        if start >= len(text):
            if final:
                raise DecodingError("Invalid JSON value")
            return _NEED_MORE

        char = text[start]
        if char == "-" or "0" <= char <= "9":
            return self._parse_number(text, start, final)

        try:
            value, end = self._decoder.raw_decode(text, start)
        except json.JSONDecodeError as exc:
            if final:
                raise DecodingError(f"Invalid JSON value: {exc.msg}") from exc
            return _NEED_MORE

        # ``true`` / ``false`` / ``null`` are only confirmed when another
        # character (or EOF) shows the literal has ended. ``truee`` is not
        # the value ``true`` followed by trailing data.
        if end == len(text) and not final and char in "tfn":
            return _NEED_MORE
        return value, end

    def _parse_exact(self, text: str, start: int) -> typing.Any:
        """Parse one JSON text; only surrounding whitespace may remain."""
        if start >= len(text):
            raise DecodingError("Invalid JSON value")
        parsed = self._parse_value(text, start, final=True)
        if isinstance(parsed, _NeedMore):  # pragma: no cover
            raise DecodingError("Invalid JSON value")
        value, end = parsed
        if _skip_ws(text, end) != len(text):
            raise DecodingError("Trailing data after JSON value")
        return value

    def _consume_document(self, text: str, *, final: bool) -> list[typing.Any]:
        self._buf += text
        values: list[typing.Any] = []
        try:
            self._parse_document(values, final=final)
        finally:
            if self._pos:
                self._buf = self._buf[self._pos :]
                self._pos = 0
        return values

    def _parse_document(self, values: list[typing.Any], *, final: bool) -> None:
        while True:
            state = self._state
            if state == "start":
                self._pos = _skip_ws(self._buf, self._pos)
                if self._pos >= len(self._buf):
                    if final:
                        raise DecodingError("JSON document is empty")
                    return
                if not self._bom_checked:
                    self._bom_checked = True
                    if self._buf[self._pos] == _UTF8_BOM:
                        self._pos += 1
                        continue
                if self._buf[self._pos] == "[":
                    self._pos += 1
                    self._state = "array-item"
                    continue
                self._state = "value"
                continue

            if state in {"value", "array-item", "array-required"}:
                self._pos = _skip_ws(self._buf, self._pos)
                if self._pos >= len(self._buf):
                    if final:
                        raise DecodingError("Invalid JSON document")
                    return
                if state == "array-item" and self._buf[self._pos] == "]":
                    self._pos += 1
                    self._state = "after"
                    continue
                if state == "array-required" and self._buf[self._pos] == "]":
                    raise DecodingError("Invalid JSON document")
                parsed = self._parse_value(self._buf, self._pos, final)
                if isinstance(parsed, _NeedMore):
                    return
                value, end = parsed
                if end <= self._pos:  # pragma: no cover
                    raise DecodingError("Invalid JSON value")
                values.append(value)
                self._pos = end
                self._state = "after" if state == "value" else "array-sep"
                continue

            if state == "array-sep":
                self._pos = _skip_ws(self._buf, self._pos)
                if self._pos >= len(self._buf):
                    if final:
                        raise DecodingError("Invalid JSON document")
                    return
                char = self._buf[self._pos]
                if char == ",":
                    self._pos += 1
                    self._state = "array-required"
                    continue
                if char == "]":
                    self._pos += 1
                    self._state = "after"
                    continue
                raise DecodingError("Invalid JSON document")

            # state == "after": only whitespace may follow the value.
            self._pos = _skip_ws(self._buf, self._pos)
            if self._pos != len(self._buf):
                raise DecodingError("Trailing data after JSON value")
            return

    def _consume_ndjson(self, text: str, *, final: bool) -> list[typing.Any]:
        self._buf += text
        lines, self._buf = _split_lines(self._buf, final=final)
        values: list[typing.Any] = []
        for line in lines:
            parsed = self._parse_ndjson_line(line)
            if parsed is not _BLANK:
                values.append(parsed)
        return values

    def _parse_ndjson_line(self, line: str) -> typing.Any:
        if self._ndjson_bom_allowed and line.startswith(_UTF8_BOM):
            # One UTF-8 BOM, and only at column 0 of the first non-blank line
            # (a BOM-only line is ignored and consumes that allowance).
            self._ndjson_bom_allowed = False
            line = line[1:]
        if _is_json_ws_only(line):
            return _BLANK
        self._ndjson_bom_allowed = False
        return self._parse_exact(line, _skip_ws(line, 0))

    def _consume_sequence(self, text: str, *, final: bool) -> list[typing.Any]:
        self._buf += text
        values: list[typing.Any] = []
        if not self._seq_started:
            index = _skip_ws(self._buf, 0)
            if index == len(self._buf):
                # Empty, or only leading whitespace so far.
                self._buf = ""
                return values
            if self._buf[index] != _RS:
                raise DecodingError(
                    "JSON text sequence must begin with a record separator"
                )
            self._buf = self._buf[index:]
            self._seq_started = True

        while self._buf:
            if not self._buf.startswith(_RS):  # pragma: no cover
                raise DecodingError(
                    "JSON text sequence records must begin with a record separator"
                )
            next_rs = self._buf.find(_RS, 1)
            if next_rs == -1:
                if not final:
                    break
                record = self._buf[1:]
                followed_by_rs = False
                self._buf = ""
            else:
                record = self._buf[1:next_rs]
                followed_by_rs = True
                self._buf = self._buf[next_rs:]
            if record.endswith("\n"):
                record = record[:-1]
            if _is_json_ws_only(record):
                # Blank records are ignored only between two RS markers.
                # A final RS, RS+LF, or RS+whitespace+LF is an error.
                if followed_by_rs:
                    continue
                raise DecodingError("JSON text sequence ended within an empty record")
            values.append(self._parse_exact(record, _skip_ws(record, 0)))
        return values
