"""
Handlers for Content-Encoding.

See: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Content-Encoding
"""

from __future__ import annotations

import codecs
import io
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


def _split_header_params(value: str) -> list[str]:
    params: list[str] = []
    current: list[str] = []
    in_quotes = False
    escaped = False
    for char in value:
        if escaped:
            escaped = False
        elif in_quotes and char == "\\":
            escaped = True
        elif char == '"':
            in_quotes = not in_quotes
        elif char == ";" and not in_quotes:
            params.append("".join(current))
            current = []
            continue
        current.append(char)
    params.append("".join(current))
    return params


def get_multipart_response_boundary(content_type: str | None) -> bytes:
    """
    Return the boundary for a `multipart/*` Content-Type, or raise DecodingError.
    """
    if content_type is None:
        raise DecodingError("Response has no Content-Type header.")
    if "\r" in content_type or "\n" in content_type:
        raise DecodingError("Invalid multipart boundary.")
    media_type, *params = _split_header_params(content_type)
    media_type = media_type.strip(" \t").lower()
    if not media_type.startswith("multipart/") or media_type == "multipart/":
        raise DecodingError("Response is not multipart.")

    boundary: str | None = None
    for param in params:
        name, sep, value = param.partition("=")
        if sep and name.strip(" \t").lower() == "boundary":
            boundary = value
    if boundary is None:
        raise DecodingError("Missing multipart boundary.")

    boundary = boundary.strip(" \t")
    if len(boundary) >= 2 and boundary[0] == boundary[-1] == '"':
        boundary = boundary[1:-1]
    if (
        not boundary
        or not boundary.isascii()
        or boundary.startswith("=")
        or "\x00" in boundary
    ):
        raise DecodingError("Invalid multipart boundary.")
    return boundary.encode("ascii")


class MultipartDecoder:
    """
    Incrementally parse a multipart body into (headers, content) pairs.
    """

    def __init__(self, boundary: bytes) -> None:
        self._dash_boundary = b"--" + boundary
        self._buffer = b""
        self._state = "preamble"
        self._first_line = True
        self._headers: list[tuple[bytes, bytes]] = []
        self._body = bytearray()
        self._pending_terminator: bytes | None = None

    def decode(self, data: bytes) -> list[tuple[list[tuple[bytes, bytes]], bytes]]:
        self._buffer += data
        parts = []
        while True:
            idx_cr = self._buffer.find(b"\r")
            idx_lf = self._buffer.find(b"\n")
            candidates = [i for i in (idx_cr, idx_lf) if i != -1]
            if not candidates:
                break
            idx = min(candidates)
            if self._buffer[idx : idx + 1] == b"\r":
                if idx + 1 == len(self._buffer):
                    break  # May be a CRLF split across chunks.
                end = idx + 2 if self._buffer[idx + 1 : idx + 2] == b"\n" else idx + 1
            else:
                end = idx + 1
            line, terminator = self._buffer[:idx], self._buffer[idx:end]
            self._buffer = self._buffer[end:]
            self._process_line(line, terminator, parts)
        return parts

    def flush(self) -> list[tuple[list[tuple[bytes, bytes]], bytes]]:
        parts: list[tuple[list[tuple[bytes, bytes]], bytes]] = []
        if self._buffer:
            buffer = self._buffer
            self._buffer = b""
            if buffer.endswith(b"\r"):
                self._process_line(buffer[:-1], b"\r", parts)
            else:
                self._process_line(buffer, b"", parts)
        if self._state != "epilogue":
            raise DecodingError("Malformed multipart body: missing closing boundary.")
        return parts

    def _delimiter(self, line: bytes) -> str | None:
        stripped = line.rstrip(b" \t")
        if stripped == self._dash_boundary:
            return "delimiter"
        if stripped == self._dash_boundary + b"--":
            return "close"
        return None

    def _process_line(
        self,
        line: bytes,
        terminator: bytes,
        parts: list[tuple[list[tuple[bytes, bytes]], bytes]],
    ) -> None:
        state = self._state
        if state == "epilogue":
            return

        delimiter = self._delimiter(line)
        if state == "preamble":
            first_line, self._first_line = self._first_line, False
            if delimiter is None:
                if first_line and line.startswith(self._dash_boundary):
                    raise DecodingError("Malformed multipart boundary line.")
                return
            self._state = "epilogue" if delimiter == "close" else "headers"
        elif state == "headers":
            if line == b"":
                self._state = "body"
                self._body = bytearray()
                self._pending_terminator = None
            else:
                self._parse_header_line(line)
        else:
            if delimiter is None:
                if self._pending_terminator is not None:
                    self._body += self._pending_terminator
                self._body += line
                self._pending_terminator = terminator
                return
            parts.append((self._headers, bytes(self._body)))
            self._headers = []
            self._body = bytearray()
            self._state = "epilogue" if delimiter == "close" else "headers"

    def _parse_header_line(self, line: bytes) -> None:
        if line[:1] in (b" ", b"\t"):
            value = line.strip(b" \t")
            if not self._headers or not value:
                raise DecodingError("Malformed multipart part header.")
            name, previous = self._headers[-1]
            self._headers[-1] = (name, previous + b" " + value)
            return
        name, sep, value = line.partition(b":")
        if not sep or not name:
            raise DecodingError("Malformed multipart part header.")
        self._headers.append((name, value.strip(b" \t")))
