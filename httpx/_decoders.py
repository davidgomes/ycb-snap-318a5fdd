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


JSON_WHITESPACE = " \t\n\r"
_JSON_WHITESPACE_RE = re.compile(r"[ \t\n\r]*")
_LINE_BREAK_RE = re.compile(r"[\r\n]")
_BYTE_ORDER_MARK = "\ufeff"
_RECORD_SEPARATOR = "\x1e"
_json_decoder = json.JSONDecoder()


def _decode_json_text(text: str) -> typing.Any:
    try:
        return _json_decoder.decode(text)
    except json.JSONDecodeError as exc:
        raise DecodingError(f"Invalid JSON: {exc}") from exc


def detect_json_encoding(data: bytes) -> str:
    """
    Detect the encoding of JSON text from its initial bytes,
    as described in RFC 4627, section 3.
    """
    if data.startswith(codecs.BOM_UTF8):
        return "utf-8"
    if data.startswith(codecs.BOM_UTF32_LE):
        return "utf-32-le"
    if data.startswith(codecs.BOM_UTF32_BE):
        return "utf-32-be"
    if data.startswith(codecs.BOM_UTF16_LE):
        return "utf-16-le"
    if data.startswith(codecs.BOM_UTF16_BE):
        return "utf-16-be"
    if len(data) >= 4:
        if data[:3] == b"\x00\x00\x00":
            return "utf-32-be"
        if data[0] != 0 and data[1:4] == b"\x00\x00\x00":
            return "utf-32-le"
    if len(data) >= 2:
        if data[0] == 0 and data[1] != 0:
            return "utf-16-be"
        if data[0] != 0 and data[1] == 0:
            return "utf-16-le"
    return "utf-8"


class JSONTextDecoder:
    """
    Handles incrementally decoding bytes into JSON text, either with an
    explicit encoding, or by detecting the encoding from the initial bytes.

    Byte order marks are preserved in the decoded text.
    """

    def __init__(self, encoding: str | None = None) -> None:
        self._decoder = (
            None
            if encoding is None
            else codecs.getincrementaldecoder(encoding)(errors="strict")
        )
        self._buffer = b""

    def decode(self, data: bytes) -> str:
        if self._decoder is None:
            self._buffer += data
            if len(self._buffer) < 4:
                return ""
            data, self._buffer = self._buffer, b""
            encoding = detect_json_encoding(data)
            self._decoder = codecs.getincrementaldecoder(encoding)(errors="strict")
        return self._decode(data, final=False)

    def flush(self) -> str:
        if self._decoder is None:
            data, self._buffer = self._buffer, b""
            encoding = detect_json_encoding(data)
            self._decoder = codecs.getincrementaldecoder(encoding)(errors="strict")
            return self._decode(data, final=True)
        return self._decode(b"", final=True)

    def _decode(self, data: bytes, final: bool) -> str:
        assert self._decoder is not None
        try:
            return self._decoder.decode(data, final)
        except UnicodeDecodeError as exc:
            raise DecodingError(f"Invalid JSON text encoding: {exc}") from exc


class JSONStreamDecoder:
    def decode(self, text: str) -> list[typing.Any]:
        raise NotImplementedError()  # pragma: no cover

    def flush(self) -> list[typing.Any]:
        raise NotImplementedError()  # pragma: no cover


class JSONDocumentDecoder(JSONStreamDecoder):
    """
    Incrementally parses a single JSON text, as used by `application/json`.

    If the top-level value is an array then each element is returned
    individually, otherwise the single value is returned.
    """

    _START = "start"
    _VALUE = "value"
    _ARRAY_FIRST = "array-first"
    _ARRAY_ELEMENT = "array-element"
    _ARRAY_SEPARATOR = "array-separator"
    _DONE = "done"

    def __init__(self) -> None:
        self._buffer = ""
        self._state = self._START
        self._skipped_bom = False
        # Avoid re-parsing an incomplete value on every small chunk,
        # which would otherwise make parsing large values quadratic.
        self._retry_length = 0

    def decode(self, text: str) -> list[typing.Any]:
        self._buffer += text
        if len(self._buffer) < self._retry_length:
            return []
        return self._process(final=False)

    def flush(self) -> list[typing.Any]:
        values = self._process(final=True)
        if self._state == self._START:
            raise DecodingError("Invalid JSON: no JSON value in response content")
        if self._state != self._DONE:
            raise DecodingError("Invalid JSON: unexpected end of response content")
        return values

    def _process(self, final: bool) -> list[typing.Any]:
        values: list[typing.Any] = []
        buffer = self._buffer
        pos = 0
        self._retry_length = 0

        while True:
            pos = _JSON_WHITESPACE_RE.match(buffer, pos).end()  # type: ignore[union-attr]
            if pos == len(buffer):
                break
            char = buffer[pos]

            if self._state == self._START:
                if char == _BYTE_ORDER_MARK and not self._skipped_bom:
                    self._skipped_bom = True
                    pos += 1
                elif char == "[":
                    self._state = self._ARRAY_FIRST
                    pos += 1
                else:
                    self._state = self._VALUE
                continue

            if self._state == self._ARRAY_SEPARATOR:
                if char == ",":
                    self._state = self._ARRAY_ELEMENT
                elif char == "]":
                    self._state = self._DONE
                else:
                    raise DecodingError("Invalid JSON: expected ',' or ']' in array")
                pos += 1
                continue

            if self._state == self._ARRAY_FIRST and char == "]":
                self._state = self._DONE
                pos += 1
                continue

            if self._state == self._DONE:
                raise DecodingError("Invalid JSON: extra data after JSON value")

            try:
                value, end = _json_decoder.raw_decode(buffer, pos)
            except json.JSONDecodeError as exc:
                if final:
                    raise DecodingError(f"Invalid JSON: {exc}") from exc
                self._retry_length = 2 * (len(buffer) - pos)
                break
            if end == len(buffer) and not final:
                # A number at the end of the buffer might continue
                # in the next chunk.
                break
            values.append(value)
            pos = end
            self._state = (
                self._DONE if self._state == self._VALUE else self._ARRAY_SEPARATOR
            )

        self._buffer = buffer[pos:]
        return values


class NDJSONDecoder(JSONStreamDecoder):
    """
    Incrementally parses newline delimited JSON, as used by
    `application/ndjson` and `application/x-ndjson`.
    """

    def __init__(self) -> None:
        self._pending: list[str] = []
        self._seen_line = False

    def decode(self, text: str) -> list[typing.Any]:
        lines = _LINE_BREAK_RE.split(text)
        if len(lines) == 1:
            self._pending.append(text)
            return []
        lines[0] = "".join(self._pending) + lines[0]
        self._pending = [lines.pop()]
        return [value for line in lines for value in self._decode_line(line)]

    def flush(self) -> list[typing.Any]:
        line = "".join(self._pending)
        self._pending = []
        return self._decode_line(line)

    def _decode_line(self, line: str) -> list[typing.Any]:
        if not line.strip(JSON_WHITESPACE):
            return []
        if not self._seen_line:
            self._seen_line = True
            if line.startswith(_BYTE_ORDER_MARK):
                line = line[1:]
        return [_decode_json_text(line)]


class JSONSequenceDecoder(JSONStreamDecoder):
    """
    Incrementally parses JSON text sequences, as used by `application/json-seq`.

    See: https://www.rfc-editor.org/rfc/rfc7464
    """

    def __init__(self) -> None:
        self._pending: list[str] = []
        self._started = False

    def decode(self, text: str) -> list[typing.Any]:
        segments = text.split(_RECORD_SEPARATOR)
        self._pending.append(segments[0])
        values: list[typing.Any] = []
        for segment in segments[1:]:
            values.extend(self._end_segment(final=False))
            self._pending = [segment]
        if not self._started:
            self._check_prefix()
        return values

    def flush(self) -> list[typing.Any]:
        if not self._started:
            self._check_prefix()
            return []
        return self._end_segment(final=True)

    def _check_prefix(self) -> None:
        if "".join(self._pending).strip(JSON_WHITESPACE):
            raise DecodingError("Invalid JSON text sequence: expected record separator")

    def _end_segment(self, final: bool) -> list[typing.Any]:
        if not self._started:
            self._check_prefix()
            self._started = True
            return []

        record = "".join(self._pending)
        if record.endswith("\n"):
            record = record[:-1]
        if not record.strip(JSON_WHITESPACE):
            if final:
                raise DecodingError(
                    "Invalid JSON text sequence: record contains no JSON text"
                )
            return []
        return [_decode_json_text(record)]


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
