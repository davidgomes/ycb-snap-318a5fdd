"""
Handlers for Content-Encoding.

See: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Content-Encoding
"""

from __future__ import annotations

import codecs
import io
import json
import re
import typing
import zlib

from ._exceptions import DecodingError

# Brotli support is optional
try:
    # The C bindings in `brotli` are recommended for CPython.
    import brotli
except ImportError:  # pragma: no cover
    try:
        # The CFFI bindings in `brotlicffi` are recommended for PyPy
        # and other environments.
        import brotlicffi as brotli
    except ImportError:
        brotli = None


# Zstandard support is optional
try:
    import zstandard
except ImportError:  # pragma: no cover
    zstandard = None  # type: ignore


class ContentDecoder:
    def decode(self, data: bytes) -> bytes:
        raise NotImplementedError()  # pragma: no cover

    def flush(self) -> bytes:
        raise NotImplementedError()  # pragma: no cover


class IdentityDecoder(ContentDecoder):
    """
    Handle unencoded data.
    """

    def decode(self, data: bytes) -> bytes:
        return data

    def flush(self) -> bytes:
        return b""


class DeflateDecoder(ContentDecoder):
    """
    Handle 'deflate' decoding.

    See: https://stackoverflow.com/questions/1838699
    """

    def __init__(self) -> None:
        self.first_attempt = True
        self.decompressor = zlib.decompressobj()

    def decode(self, data: bytes) -> bytes:
        was_first_attempt = self.first_attempt
        self.first_attempt = False
        try:
            return self.decompressor.decompress(data)
        except zlib.error as exc:
            if was_first_attempt:
                self.decompressor = zlib.decompressobj(-zlib.MAX_WBITS)
                return self.decode(data)
            raise DecodingError(str(exc)) from exc

    def flush(self) -> bytes:
        try:
            return self.decompressor.flush()
        except zlib.error as exc:  # pragma: no cover
            raise DecodingError(str(exc)) from exc


class GZipDecoder(ContentDecoder):
    """
    Handle 'gzip' decoding.

    See: https://stackoverflow.com/questions/1838699
    """

    def __init__(self) -> None:
        self.decompressor = zlib.decompressobj(zlib.MAX_WBITS | 16)

    def decode(self, data: bytes) -> bytes:
        try:
            return self.decompressor.decompress(data)
        except zlib.error as exc:
            raise DecodingError(str(exc)) from exc

    def flush(self) -> bytes:
        try:
            return self.decompressor.flush()
        except zlib.error as exc:  # pragma: no cover
            raise DecodingError(str(exc)) from exc


class BrotliDecoder(ContentDecoder):
    """
    Handle 'brotli' decoding.

    Requires `pip install brotlipy`. See: https://brotlipy.readthedocs.io/
        or   `pip install brotli`. See https://github.com/google/brotli
    Supports both 'brotlipy' and 'Brotli' packages since they share an import
    name. The top branches are for 'brotlipy' and bottom branches for 'Brotli'
    """

    def __init__(self) -> None:
        if brotli is None:  # pragma: no cover
            raise ImportError(
                "Using 'BrotliDecoder', but neither of the 'brotlicffi' or 'brotli' "
                "packages have been installed. "
                "Make sure to install httpx using `pip install httpx[brotli]`."
            ) from None

        self.decompressor = brotli.Decompressor()
        self.seen_data = False
        self._decompress: typing.Callable[[bytes], bytes]
        if hasattr(self.decompressor, "decompress"):
            # The 'brotlicffi' package.
            self._decompress = self.decompressor.decompress  # pragma: no cover
        else:
            # The 'brotli' package.
            self._decompress = self.decompressor.process  # pragma: no cover

    def decode(self, data: bytes) -> bytes:
        if not data:
            return b""
        self.seen_data = True
        try:
            return self._decompress(data)
        except brotli.error as exc:
            raise DecodingError(str(exc)) from exc

    def flush(self) -> bytes:
        if not self.seen_data:
            return b""
        try:
            if hasattr(self.decompressor, "finish"):
                # Only available in the 'brotlicffi' package.

                # As the decompressor decompresses eagerly, this
                # will never actually emit any data. However, it will potentially throw
                # errors if a truncated or damaged data stream has been used.
                self.decompressor.finish()  # pragma: no cover
            return b""
        except brotli.error as exc:  # pragma: no cover
            raise DecodingError(str(exc)) from exc


class ZStandardDecoder(ContentDecoder):
    """
    Handle 'zstd' RFC 8878 decoding.

    Requires `pip install zstandard`.
    Can be installed as a dependency of httpx using `pip install httpx[zstd]`.
    """

    # inspired by the ZstdDecoder implementation in urllib3
    def __init__(self) -> None:
        if zstandard is None:  # pragma: no cover
            raise ImportError(
                "Using 'ZStandardDecoder', ..."
                "Make sure to install httpx using `pip install httpx[zstd]`."
            ) from None

        self.decompressor = zstandard.ZstdDecompressor().decompressobj()
        self.seen_data = False

    def decode(self, data: bytes) -> bytes:
        assert zstandard is not None
        self.seen_data = True
        output = io.BytesIO()
        try:
            output.write(self.decompressor.decompress(data))
            while self.decompressor.eof and self.decompressor.unused_data:
                unused_data = self.decompressor.unused_data
                self.decompressor = zstandard.ZstdDecompressor().decompressobj()
                output.write(self.decompressor.decompress(unused_data))
        except zstandard.ZstdError as exc:
            raise DecodingError(str(exc)) from exc
        return output.getvalue()

    def flush(self) -> bytes:
        if not self.seen_data:
            return b""
        ret = self.decompressor.flush()  # note: this is a no-op
        if not self.decompressor.eof:
            raise DecodingError("Zstandard data is incomplete")  # pragma: no cover
        return bytes(ret)


class MultiDecoder(ContentDecoder):
    """
    Handle the case where multiple encodings have been applied.
    """

    def __init__(self, children: typing.Sequence[ContentDecoder]) -> None:
        """
        'children' should be a sequence of decoders in the order in which
        each was applied.
        """
        # Note that we reverse the order for decoding.
        self.children = list(reversed(children))

    def decode(self, data: bytes) -> bytes:
        for child in self.children:
            data = child.decode(data)
        return data

    def flush(self) -> bytes:
        data = b""
        for child in self.children:
            data = child.decode(data) + child.flush()
        return data


class ByteChunker:
    """
    Handles returning byte content in fixed-size chunks.
    """

    def __init__(self, chunk_size: int | None = None) -> None:
        self._buffer = io.BytesIO()
        self._chunk_size = chunk_size

    def decode(self, content: bytes) -> list[bytes]:
        if self._chunk_size is None:
            return [content] if content else []

        self._buffer.write(content)
        if self._buffer.tell() >= self._chunk_size:
            value = self._buffer.getvalue()
            chunks = [
                value[i : i + self._chunk_size]
                for i in range(0, len(value), self._chunk_size)
            ]
            if len(chunks[-1]) == self._chunk_size:
                self._buffer.seek(0)
                self._buffer.truncate()
                return chunks
            else:
                self._buffer.seek(0)
                self._buffer.write(chunks[-1])
                self._buffer.truncate()
                return chunks[:-1]
        else:
            return []

    def flush(self) -> list[bytes]:
        value = self._buffer.getvalue()
        self._buffer.seek(0)
        self._buffer.truncate()
        return [value] if value else []


class TextChunker:
    """
    Handles returning text content in fixed-size chunks.
    """

    def __init__(self, chunk_size: int | None = None) -> None:
        self._buffer = io.StringIO()
        self._chunk_size = chunk_size

    def decode(self, content: str) -> list[str]:
        if self._chunk_size is None:
            return [content] if content else []

        self._buffer.write(content)
        if self._buffer.tell() >= self._chunk_size:
            value = self._buffer.getvalue()
            chunks = [
                value[i : i + self._chunk_size]
                for i in range(0, len(value), self._chunk_size)
            ]
            if len(chunks[-1]) == self._chunk_size:
                self._buffer.seek(0)
                self._buffer.truncate()
                return chunks
            else:
                self._buffer.seek(0)
                self._buffer.write(chunks[-1])
                self._buffer.truncate()
                return chunks[:-1]
        else:
            return []

    def flush(self) -> list[str]:
        value = self._buffer.getvalue()
        self._buffer.seek(0)
        self._buffer.truncate()
        return [value] if value else []


class TextDecoder:
    """
    Handles incrementally decoding bytes into text
    """

    def __init__(self, encoding: str = "utf-8") -> None:
        self.decoder = codecs.getincrementaldecoder(encoding)(errors="replace")

    def decode(self, data: bytes) -> str:
        return self.decoder.decode(data)

    def flush(self) -> str:
        return self.decoder.decode(b"", True)


class LineDecoder:
    """
    Handles incrementally reading lines from text.

    Has the same behaviour as the stdllib splitlines,
    but handling the input iteratively.
    """

    def __init__(self) -> None:
        self.buffer: list[str] = []
        self.trailing_cr: bool = False

    def decode(self, text: str) -> list[str]:
        # See https://docs.python.org/3/library/stdtypes.html#str.splitlines
        NEWLINE_CHARS = "\n\r\x0b\x0c\x1c\x1d\x1e\x85\u2028\u2029"

        # We always push a trailing `\r` into the next decode iteration.
        if self.trailing_cr:
            text = "\r" + text
            self.trailing_cr = False
        if text.endswith("\r"):
            self.trailing_cr = True
            text = text[:-1]

        if not text:
            # NOTE: the edge case input of empty text doesn't occur in practice,
            # because other httpx internals filter out this value
            return []  # pragma: no cover

        trailing_newline = text[-1] in NEWLINE_CHARS
        lines = text.splitlines()

        if len(lines) == 1 and not trailing_newline:
            # No new lines, buffer the input and continue.
            self.buffer.append(lines[0])
            return []

        if self.buffer:
            # Include any existing buffer in the first portion of the
            # splitlines result.
            lines = ["".join(self.buffer) + lines[0]] + lines[1:]
            self.buffer = []

        if not trailing_newline:
            # If the last segment of splitlines is not newline terminated,
            # then drop it from our output and start a new buffer.
            self.buffer = [lines.pop()]

        return lines

    def flush(self) -> list[str]:
        if not self.buffer and not self.trailing_cr:
            return []

        lines = ["".join(self.buffer)]
        self.buffer = []
        self.trailing_cr = False
        return lines


# JSON only treats these four characters as insignificant whitespace.
JSON_WHITESPACE = " \t\n\r"
_JSON_WHITESPACE_RE = re.compile(r"[ \t\n\r]*")
_JSON_ARRAY_TOKEN_RE = re.compile(r'[\[\]{},"]')
_JSON_STRING_TOKEN_RE = re.compile(r'["\\]')
_NEWLINE_RE = re.compile(r"[\r\n]")
_json_decoder = json.JSONDecoder()


def _skip_json_whitespace(text: str, pos: int) -> int:
    match = _JSON_WHITESPACE_RE.match(text, pos)
    assert match is not None
    return match.end()


def parse_json_text(text: str) -> typing.Any:
    """
    Parse exactly one JSON text, allowing only surrounding whitespace.
    """
    start = _skip_json_whitespace(text, 0)
    if start == len(text):
        raise DecodingError("Expected a JSON value, but found no data.")
    try:
        value, end = _json_decoder.raw_decode(text, start)
    except json.JSONDecodeError as exc:
        raise DecodingError(f"Invalid JSON: {exc}") from exc
    if _skip_json_whitespace(text, end) != len(text):
        raise DecodingError("Unexpected data after JSON value.")
    return value


class JSONTextDecoder:
    """
    Handles incrementally decoding bytes into JSON text.

    If no encoding is given, it is detected from the leading bytes, as
    UTF-8, UTF-16, or UTF-32. A UTF-8 BOM is passed through as text, so that
    each JSON format can apply its own rules for where a BOM is allowed.
    """

    def __init__(self, encoding: str | None = None) -> None:
        self._buffer = b""
        self._decoder: codecs.IncrementalDecoder | None = None
        if encoding is not None:
            self._decoder = codecs.getincrementaldecoder(encoding)(errors="strict")

    def _detect(self, data: bytes) -> codecs.IncrementalDecoder:
        encoding = json.detect_encoding(data)
        if encoding == "utf-8-sig":
            encoding = "utf-8"
        return codecs.getincrementaldecoder(encoding)(errors="strict")

    def _decode(self, data: bytes, final: bool) -> str:
        if self._decoder is None:
            self._buffer += data
            if len(self._buffer) < 4 and not final:
                return ""
            data, self._buffer = self._buffer, b""
            self._decoder = self._detect(data)
        try:
            return self._decoder.decode(data, final)
        except UnicodeDecodeError as exc:
            raise DecodingError(str(exc)) from exc

    def decode(self, data: bytes) -> str:
        return self._decode(data, final=False)

    def flush(self) -> str:
        return self._decode(b"", final=True)


class JSONStreamDecoder:
    def decode(self, text: str) -> list[typing.Any]:
        raise NotImplementedError()  # pragma: no cover

    def flush(self) -> list[typing.Any]:
        raise NotImplementedError()  # pragma: no cover


class JSONDocumentDecoder(JSONStreamDecoder):
    """
    Handles incrementally parsing an `application/json` document.

    A top-level array yields each of its elements as soon as the element is
    complete. Any other top-level value is yielded once the document ends.
    """

    def __init__(self) -> None:
        self._state = "start"  # "start", "value", "array", or "end".
        self._bom_allowed = True
        self._fragments: list[str] = []
        self._depth = 0
        self._in_string = False
        self._escape = False
        self._after_comma = False
        self._element_complete = False

    def decode(self, text: str) -> list[typing.Any]:
        values: list[typing.Any] = []
        pos = 0
        while pos < len(text):
            if self._state == "start":
                pos = _skip_json_whitespace(text, pos)
                if pos == len(text):
                    break
                if text[pos] == "\ufeff" and self._bom_allowed:
                    self._bom_allowed = False
                    pos += 1
                elif text[pos] == "[":
                    self._state = "array"
                    pos += 1
                else:
                    self._state = "value"
            elif self._state == "value":
                self._fragments.append(text[pos:])
                pos = len(text)
            elif self._state == "array":
                pos = self._decode_array(text, pos, values)
            else:
                if _skip_json_whitespace(text, pos) != len(text):
                    raise DecodingError("Unexpected data after JSON value.")
                pos = len(text)
        return values

    def _parse_element(self, tail: str, values: list[typing.Any]) -> None:
        self._fragments.append(tail)
        element = "".join(self._fragments)
        self._fragments = []
        values.append(parse_json_text(element))

    def _decode_array(self, text: str, pos: int, values: list[typing.Any]) -> int:
        """
        Scan array content for element boundaries, tracking strings and nested
        containers. Each complete element is then parsed, which also validates
        the scanned structure.

        Strings, objects, and arrays are complete as soon as they are closed.
        Numbers and literals are only complete once a `,` or `]` follows them.
        """
        segment_start = pos
        while pos < len(text):
            if self._element_complete:
                pos = _skip_json_whitespace(text, pos)
                if pos == len(text):
                    return pos
                token = text[pos]
                pos += 1
                if token not in ",]":
                    raise DecodingError("Expected ',' or ']' after array element.")
                self._element_complete = False
                self._after_comma = token == ","
                segment_start = pos
                if token == "]":
                    self._state = "end"
                    return pos
                continue

            if self._in_string:
                if self._escape:
                    self._escape = False
                    pos += 1
                    continue
                match = _JSON_STRING_TOKEN_RE.search(text, pos)
                if match is None:
                    pos = len(text)
                    break
                pos = match.end()
                if match.group() == "\\":
                    self._escape = True
                else:
                    self._in_string = False
                    if not self._depth:
                        self._parse_element(text[segment_start:pos], values)
                        self._element_complete = True
                        segment_start = pos
                continue

            match = _JSON_ARRAY_TOKEN_RE.search(text, pos)
            if match is None:
                pos = len(text)
                break
            pos = match.end()
            token = match.group()
            if token == '"':
                self._in_string = True
            elif token in "[{":
                self._depth += 1
            elif token in "]}" and self._depth:
                self._depth -= 1
                if not self._depth:
                    self._parse_element(text[segment_start:pos], values)
                    self._element_complete = True
                    segment_start = pos
            elif token in ",]" and not self._depth:
                self._fragments.append(text[segment_start : pos - 1])
                element = "".join(self._fragments)
                self._fragments = []
                segment_start = pos
                closing = token == "]"
                if element.strip(JSON_WHITESPACE):
                    values.append(parse_json_text(element))
                elif self._after_comma or not closing:
                    raise DecodingError("Expected a JSON value in array.")
                self._after_comma = not closing
                if closing:
                    self._state = "end"
                    return pos
        if segment_start < pos:
            self._fragments.append(text[segment_start:pos])
        return pos

    def flush(self) -> list[typing.Any]:
        if self._state == "start":
            raise DecodingError("Expected a JSON value, but found no data.")
        if self._state == "array":
            raise DecodingError("Unterminated JSON array.")
        if self._state == "value":
            text = "".join(self._fragments)
            self._fragments = []
            self._state = "end"
            return [parse_json_text(text)]
        return []


class NDJSONDecoder(JSONStreamDecoder):
    """
    Handles incrementally parsing newline delimited JSON.

    Lines may be separated by LF, CR, or CRLF. Blank lines are ignored.
    """

    def __init__(self) -> None:
        self._buffer: list[str] = []
        self._seen_value = False

    def _parse_line(self, line: str, values: list[typing.Any]) -> None:
        if not line.strip(JSON_WHITESPACE):
            return
        if not self._seen_value:
            self._seen_value = True
            if line.startswith("\ufeff"):
                line = line[1:]
        values.append(parse_json_text(line))

    def decode(self, text: str) -> list[typing.Any]:
        values: list[typing.Any] = []
        lines = _NEWLINE_RE.split(text)
        if len(lines) == 1:
            if text:
                self._buffer.append(text)
            return values

        lines[0] = "".join(self._buffer) + lines[0]
        self._buffer = [lines.pop()]
        for line in lines:
            self._parse_line(line, values)
        return values

    def flush(self) -> list[typing.Any]:
        values: list[typing.Any] = []
        line = "".join(self._buffer)
        self._buffer = []
        self._parse_line(line, values)
        return values


class JSONSeqDecoder(JSONStreamDecoder):
    """
    Handles incrementally parsing JSON text sequences, as described in RFC 7464.

    Each record begins with an RS character, and is terminated by the next RS
    character or by the end of the payload.
    """

    RS = "\x1e"

    def __init__(self) -> None:
        self._started = False
        self._record: list[str] = []

    def _end_record(self, values: list[typing.Any], final: bool) -> None:
        record = "".join(self._record)
        self._record = []
        if record.endswith("\n"):
            record = record[:-1]
        if not record.strip(JSON_WHITESPACE):
            if final:
                raise DecodingError("Incomplete JSON text sequence record.")
            return
        values.append(parse_json_text(record))

    def decode(self, text: str) -> list[typing.Any]:
        values: list[typing.Any] = []
        parts = text.split(self.RS)
        if not self._started:
            if parts[0].strip(JSON_WHITESPACE):
                raise DecodingError(
                    "JSON text sequence must begin with a record separator."
                )
            if len(parts) == 1:
                return values
            self._started = True
            parts = parts[1:]

        self._record.append(parts[0])
        for part in parts[1:]:
            self._end_record(values, final=False)
            self._record = [part]
        return values

    def flush(self) -> list[typing.Any]:
        values: list[typing.Any] = []
        if self._started:
            self._end_record(values, final=True)
        return values


SUPPORTED_DECODERS = {
    "identity": IdentityDecoder,
    "gzip": GZipDecoder,
    "deflate": DeflateDecoder,
    "br": BrotliDecoder,
    "zstd": ZStandardDecoder,
}


if brotli is None:
    SUPPORTED_DECODERS.pop("br")  # pragma: no cover
if zstandard is None:
    SUPPORTED_DECODERS.pop("zstd")  # pragma: no cover
