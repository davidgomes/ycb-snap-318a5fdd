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
JSON_NON_WHITESPACE = re.compile(r"[^ \t\n\r]")
JSON_STRUCTURAL = re.compile(r'["\[\]{}]')
JSON_STRING_SPECIAL = re.compile(r'["\\]')
JSON_SCALAR_END = re.compile(r"[^0-9A-Za-z+\-.]")
NEWLINE = re.compile(r"[\r\n]")
JSON_DECODER = json.JSONDecoder()


def decode_json(text: str) -> typing.Any:
    try:
        return json.loads(text)
    except json.JSONDecodeError as exc:
        raise DecodingError(f"Invalid JSON: {exc}") from exc


class JSONStreamDecoder:
    """
    Handles incrementally decoding bytes into JSON values.

    The text encoding is either given explicitly, or detected as UTF-8, UTF-16,
    or UTF-32 in the same way as `json.loads()`.
    """

    def __init__(self, encoding: str | None = None) -> None:
        self._buffer = b""
        self._text_decoder: codecs.IncrementalDecoder | None = None
        if encoding is not None:
            try:
                # Rejects unknown codecs, and codecs that do not decode to text.
                "".encode(encoding)
                self._text_decoder = codecs.getincrementaldecoder(encoding)()
            except (LookupError, UnicodeError) as exc:
                message = f"Unsupported charset for JSON: {encoding!r}"
                raise DecodingError(message) from exc

    def decode(self, data: bytes) -> list[typing.Any]:
        return self._decode_text(self._decode_bytes(data, final=False))

    def flush(self) -> list[typing.Any]:
        values = self._decode_text(self._decode_bytes(b"", final=True))
        values.extend(self._flush_text())
        return values

    def _decode_bytes(self, data: bytes, final: bool) -> str:
        if self._text_decoder is None:
            # Encoding detection looks at up to the first four bytes.
            self._buffer += data
            if len(self._buffer) < 4 and not final:
                return ""
            encoding = json.detect_encoding(self._buffer)
            self._text_decoder = codecs.getincrementaldecoder(encoding)()
            data, self._buffer = self._buffer, b""
        try:
            return self._text_decoder.decode(data, final)
        except UnicodeError as exc:
            raise DecodingError(str(exc)) from exc

    def _decode_text(self, text: str) -> list[typing.Any]:
        raise NotImplementedError()  # pragma: no cover

    def _flush_text(self) -> list[typing.Any]:
        raise NotImplementedError()  # pragma: no cover


class JSONDocumentDecoder(JSONStreamDecoder):
    """
    Handles a single JSON text, such as an `application/json` document.

    If the top-level value is an array then each element is returned as soon
    as it is complete. Otherwise the top-level value is returned once all of
    the content has been received.
    """

    def __init__(self, encoding: str | None = None) -> None:
        super().__init__(encoding)
        self._state = "start"
        self._seen_bom = False
        self._parts: list[str] = []
        # Scanning state, used to find where an array element ends.
        self._scalar = False
        self._depth = 0
        self._in_string = False
        self._escape = False

    def _decode_text(self, text: str) -> list[typing.Any]:
        values: list[typing.Any] = []
        pos = 0
        while pos < len(text):
            if self._state == "value":
                self._parts.append(text[pos:])
                break

            if self._state == "element":
                end = self._scan_element(text, pos)
                if end is None:
                    self._parts.append(text[pos:])
                    break
                self._parts.append(text[pos:end])
                values.append(decode_json("".join(self._parts)))
                self._parts = []
                self._state = "after_element"
                pos = end
                continue

            match = JSON_NON_WHITESPACE.search(text, pos)
            if match is None:
                break
            pos = match.start()
            char = match.group()

            if self._state == "start":
                if char == "\ufeff" and not self._seen_bom:
                    self._seen_bom = True
                    pos += 1
                elif char == "[":
                    self._state = "array"
                    pos += 1
                else:
                    self._state = "value"
            elif self._state == "after_element":
                if char not in ",]":
                    raise DecodingError("Invalid JSON: Expecting ',' delimiter")
                self._state = "next_element" if char == "," else "end"
                pos += 1
            elif self._state == "end":
                raise DecodingError("Invalid JSON: Extra data")
            elif char == "]" and self._state == "array":
                self._state = "end"
                pos += 1
            elif char in '"[{' or not JSON_SCALAR_END.match(char):
                pos = self._start_element(text, pos, values)
            else:
                raise DecodingError("Invalid JSON: Expecting value")
        return values

    def _start_element(self, text: str, pos: int, values: list[typing.Any]) -> int:
        """
        Decodes the array element starting at `pos` if it is complete within
        `text`, otherwise starts scanning for the end of the element.
        """
        try:
            value, end = JSON_DECODER.raw_decode(text, pos)
        except json.JSONDecodeError:
            pass
        else:
            # A number or literal is only complete once it is followed by a
            # character that cannot continue it, eg. "1.5e" may become "1.5e3".
            if end < len(text) and JSON_SCALAR_END.match(text, end):
                values.append(value)
                self._state = "after_element"
                return end
        self._scalar = text[pos] not in '"[{'
        self._state = "element"
        return pos

    def _flush_text(self) -> list[typing.Any]:
        if self._state == "value":
            return [decode_json("".join(self._parts))]
        if self._state == "start":
            raise DecodingError("Invalid JSON: Expecting value")
        if self._state != "end":
            raise DecodingError("Invalid JSON: Unterminated array")
        return []

    def _scan_element(self, text: str, pos: int) -> int | None:
        """
        Returns the index just past the end of the current array element,
        or `None` if the element continues beyond the end of `text`.

        The element text is validated once it has been fully received.
        """
        if self._scalar:
            match = JSON_SCALAR_END.search(text, pos)
            return None if match is None else match.start()

        while True:
            if self._escape:
                if pos == len(text):
                    return None
                pos += 1
                self._escape = False
            pattern = JSON_STRING_SPECIAL if self._in_string else JSON_STRUCTURAL
            match = pattern.search(text, pos)
            if match is None:
                return None
            pos = match.end()
            char = match.group()
            if char == "\\":
                self._escape = True
                continue
            if char == '"':
                self._in_string = not self._in_string
            elif char in "[{":
                self._depth += 1
            else:
                self._depth -= 1
            if self._depth == 0 and not self._in_string:
                return pos


class NDJSONDecoder(JSONStreamDecoder):
    """
    Handles newline delimited JSON, with one JSON text on each non-blank line.
    """

    def __init__(self, encoding: str | None = None) -> None:
        super().__init__(encoding)
        self._line: list[str] = []
        self._seen_line = False

    def _decode_text(self, text: str) -> list[typing.Any]:
        values: list[typing.Any] = []
        pos = 0
        # A CRLF is split as a CR followed by an empty line, which is ignored.
        for match in NEWLINE.finditer(text):
            self._line.append(text[pos : match.start()])
            values.extend(self._decode_line())
            pos = match.end()
        self._line.append(text[pos:])
        return values

    def _flush_text(self) -> list[typing.Any]:
        return self._decode_line()

    def _decode_line(self) -> list[typing.Any]:
        line = "".join(self._line)
        self._line = []
        if not line.strip(JSON_WHITESPACE):
            return []
        if not self._seen_line:
            self._seen_line = True
            line = line.removeprefix("\ufeff")
        return [decode_json(line)]


class JSONSeqDecoder(JSONStreamDecoder):
    """
    Handles JSON text sequences, where each JSON text is preceded by an
    ASCII record separator.

    See: https://www.rfc-editor.org/rfc/rfc7464
    """

    def __init__(self, encoding: str | None = None) -> None:
        super().__init__(encoding)
        self._started = False
        self._record: list[str] = []

    def _decode_text(self, text: str) -> list[typing.Any]:
        values: list[typing.Any] = []
        pos = 0
        if not self._started:
            match = JSON_NON_WHITESPACE.search(text)
            if match is None:
                return []
            if match.group() != "\x1e":
                raise DecodingError(
                    "Invalid JSON text sequence: Expecting record separator"
                )
            self._started = True
            pos = match.end()
        while (index := text.find("\x1e", pos)) != -1:
            self._record.append(text[pos:index])
            values.extend(self._decode_record(final=False))
            pos = index + 1
        self._record.append(text[pos:])
        return values

    def _flush_text(self) -> list[typing.Any]:
        return self._decode_record(final=True) if self._started else []

    def _decode_record(self, final: bool) -> list[typing.Any]:
        record = "".join(self._record)
        self._record = []
        if record.strip(JSON_WHITESPACE):
            return [decode_json(record)]
        if final:
            raise DecodingError("Invalid JSON text sequence: Expecting value")
        # Empty records between two record separators are skipped.
        return []


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
