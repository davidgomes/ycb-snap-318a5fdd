from __future__ import annotations

import io
import mimetypes
import os
import re
import typing
from pathlib import Path

from ._exceptions import DecodingError
from ._types import (
    AsyncByteStream,
    FileContent,
    FileTypes,
    RequestData,
    RequestFiles,
    SyncByteStream,
)
from ._utils import (
    peek_filelike_length,
    primitive_value_to_str,
    to_bytes,
)

_HTML5_FORM_ENCODING_REPLACEMENTS = {'"': "%22", "\\": "\\\\"}
_HTML5_FORM_ENCODING_REPLACEMENTS.update(
    {chr(c): "%{:02X}".format(c) for c in range(0x1F + 1) if c != 0x1B}
)
_HTML5_FORM_ENCODING_RE = re.compile(
    r"|".join([re.escape(c) for c in _HTML5_FORM_ENCODING_REPLACEMENTS.keys()])
)


def _format_form_param(name: str, value: str) -> bytes:
    """
    Encode a name/value pair within a multipart form.
    """

    def replacer(match: typing.Match[str]) -> str:
        return _HTML5_FORM_ENCODING_REPLACEMENTS[match.group(0)]

    value = _HTML5_FORM_ENCODING_RE.sub(replacer, value)
    return f'{name}="{value}"'.encode()


def _guess_content_type(filename: str | None) -> str | None:
    """
    Guesses the mimetype based on a filename. Defaults to `application/octet-stream`.

    Returns `None` if `filename` is `None` or empty.
    """
    if filename:
        return mimetypes.guess_type(filename)[0] or "application/octet-stream"
    return None


def get_multipart_boundary_from_content_type(
    content_type: bytes | None,
) -> bytes | None:
    if not content_type or not content_type.startswith(b"multipart/form-data"):
        return None
    # parse boundary according to
    # https://www.rfc-editor.org/rfc/rfc2046#section-5.1.1
    if b";" in content_type:
        for section in content_type.split(b";"):
            if section.strip().lower().startswith(b"boundary="):
                return section.strip()[len(b"boundary=") :].strip(b'"')
    return None


class DataField:
    """
    A single form field item, within a multipart form field.
    """

    def __init__(self, name: str, value: str | bytes | int | float | None) -> None:
        if not isinstance(name, str):
            raise TypeError(
                f"Invalid type for name. Expected str, got {type(name)}: {name!r}"
            )
        if value is not None and not isinstance(value, (str, bytes, int, float)):
            raise TypeError(
                "Invalid type for value. Expected primitive type,"
                f" got {type(value)}: {value!r}"
            )
        self.name = name
        self.value: str | bytes = (
            value if isinstance(value, bytes) else primitive_value_to_str(value)
        )

    def render_headers(self) -> bytes:
        if not hasattr(self, "_headers"):
            name = _format_form_param("name", self.name)
            self._headers = b"".join(
                [b"Content-Disposition: form-data; ", name, b"\r\n\r\n"]
            )

        return self._headers

    def render_data(self) -> bytes:
        if not hasattr(self, "_data"):
            self._data = to_bytes(self.value)

        return self._data

    def get_length(self) -> int:
        headers = self.render_headers()
        data = self.render_data()
        return len(headers) + len(data)

    def render(self) -> typing.Iterator[bytes]:
        yield self.render_headers()
        yield self.render_data()


class FileField:
    """
    A single file field item, within a multipart form field.
    """

    CHUNK_SIZE = 64 * 1024

    def __init__(self, name: str, value: FileTypes) -> None:
        self.name = name

        fileobj: FileContent

        headers: dict[str, str] = {}
        content_type: str | None = None

        # This large tuple based API largely mirror's requests' API
        # It would be good to think of better APIs for this that we could
        # include in httpx 2.0 since variable length tuples(especially of 4 elements)
        # are quite unwieldly
        if isinstance(value, tuple):
            if len(value) == 2:
                # neither the 3rd parameter (content_type) nor the 4th (headers)
                # was included
                filename, fileobj = value
            elif len(value) == 3:
                filename, fileobj, content_type = value
            else:
                # all 4 parameters included
                filename, fileobj, content_type, headers = value  # type: ignore
        else:
            filename = Path(str(getattr(value, "name", "upload"))).name
            fileobj = value

        if content_type is None:
            content_type = _guess_content_type(filename)

        has_content_type_header = any("content-type" in key.lower() for key in headers)
        if content_type is not None and not has_content_type_header:
            # note that unlike requests, we ignore the content_type provided in the 3rd
            # tuple element if it is also included in the headers requests does
            # the opposite (it overwrites the headerwith the 3rd tuple element)
            headers["Content-Type"] = content_type

        if isinstance(fileobj, io.StringIO):
            raise TypeError(
                "Multipart file uploads require 'io.BytesIO', not 'io.StringIO'."
            )
        if isinstance(fileobj, io.TextIOBase):
            raise TypeError(
                "Multipart file uploads must be opened in binary mode, not text mode."
            )

        self.filename = filename
        self.file = fileobj
        self.headers = headers

    def get_length(self) -> int | None:
        headers = self.render_headers()

        if isinstance(self.file, (str, bytes)):
            return len(headers) + len(to_bytes(self.file))

        file_length = peek_filelike_length(self.file)

        # If we can't determine the filesize without reading it into memory,
        # then return `None` here, to indicate an unknown file length.
        if file_length is None:
            return None

        return len(headers) + file_length

    def render_headers(self) -> bytes:
        if not hasattr(self, "_headers"):
            parts = [
                b"Content-Disposition: form-data; ",
                _format_form_param("name", self.name),
            ]
            if self.filename:
                filename = _format_form_param("filename", self.filename)
                parts.extend([b"; ", filename])
            for header_name, header_value in self.headers.items():
                key, val = f"\r\n{header_name}: ".encode(), header_value.encode()
                parts.extend([key, val])
            parts.append(b"\r\n\r\n")
            self._headers = b"".join(parts)

        return self._headers

    def render_data(self) -> typing.Iterator[bytes]:
        if isinstance(self.file, (str, bytes)):
            yield to_bytes(self.file)
            return

        if hasattr(self.file, "seek"):
            try:
                self.file.seek(0)
            except io.UnsupportedOperation:
                pass

        chunk = self.file.read(self.CHUNK_SIZE)
        while chunk:
            yield to_bytes(chunk)
            chunk = self.file.read(self.CHUNK_SIZE)

    def render(self) -> typing.Iterator[bytes]:
        yield self.render_headers()
        yield from self.render_data()


class MultipartStream(SyncByteStream, AsyncByteStream):
    """
    Request content as streaming multipart encoded form data.
    """

    def __init__(
        self,
        data: RequestData,
        files: RequestFiles,
        boundary: bytes | None = None,
    ) -> None:
        if boundary is None:
            boundary = os.urandom(16).hex().encode("ascii")

        self.boundary = boundary
        self.content_type = "multipart/form-data; boundary=%s" % boundary.decode(
            "ascii"
        )
        self.fields = list(self._iter_fields(data, files))

    def _iter_fields(
        self, data: RequestData, files: RequestFiles
    ) -> typing.Iterator[FileField | DataField]:
        for name, value in data.items():
            if isinstance(value, (tuple, list)):
                for item in value:
                    yield DataField(name=name, value=item)
            else:
                yield DataField(name=name, value=value)

        file_items = files.items() if isinstance(files, typing.Mapping) else files
        for name, value in file_items:
            yield FileField(name=name, value=value)

    def iter_chunks(self) -> typing.Iterator[bytes]:
        for field in self.fields:
            yield b"--%s\r\n" % self.boundary
            yield from field.render()
            yield b"\r\n"
        yield b"--%s--\r\n" % self.boundary

    def get_content_length(self) -> int | None:
        """
        Return the length of the multipart encoded content, or `None` if
        any of the files have a length that cannot be determined upfront.
        """
        boundary_length = len(self.boundary)
        length = 0

        for field in self.fields:
            field_length = field.get_length()
            if field_length is None:
                return None

            length += 2 + boundary_length + 2  # b"--{boundary}\r\n"
            length += field_length
            length += 2  # b"\r\n"

        length += 2 + boundary_length + 4  # b"--{boundary}--\r\n"
        return length

    # Content stream interface.

    def get_headers(self) -> dict[str, str]:
        content_length = self.get_content_length()
        content_type = self.content_type
        if content_length is None:
            return {"Transfer-Encoding": "chunked", "Content-Type": content_type}
        return {"Content-Length": str(content_length), "Content-Type": content_type}

    def __iter__(self) -> typing.Iterator[bytes]:
        for chunk in self.iter_chunks():
            yield chunk

    async def __aiter__(self) -> typing.AsyncIterator[bytes]:
        for chunk in self.iter_chunks():
            yield chunk


# Response multipart parsing.
#
# Request encoding above and response parsing below intentionally use different
# boundary rules. Response parsing follows the multipart framing described for
# `Response.iter_multipart()`.

_PREAMBLE = 0
_HEADERS = 1
_BODY = 2
_EPILOGUE = 3

_ParsedHeaders = list[tuple[bytes, bytes]]


class _ParsedPart(typing.NamedTuple):
    headers: _ParsedHeaders
    content: bytes


def parse_multipart_response_boundary(content_type: str | None) -> bytes:
    """
    Return the boundary from a ``multipart/*`` Content-Type value.

    Raises ``DecodingError`` when the response is not multipart or the
    boundary parameter is missing or invalid.
    """
    if content_type is None or "\r" in content_type or "\n" in content_type:
        raise DecodingError("Invalid multipart content type")

    segments = _split_content_type(content_type)
    media_type = segments[0].strip(" \t")
    main_type, separator, subtype = media_type.partition("/")
    if (
        separator != "/"
        or main_type.lower() != "multipart"
        or subtype.strip(" \t") == ""
    ):
        raise DecodingError("Invalid multipart content type")

    boundary: str | None = None
    for segment in segments[1:]:
        name, equals, value = segment.strip(" \t").partition("=")
        if equals != "=" or name.strip(" \t").lower() != "boundary":
            continue
        boundary = value

    if boundary is None:
        raise DecodingError("Missing multipart boundary")
    return _validate_boundary(boundary).encode("ascii")


def _split_content_type(value: str) -> list[str]:
    """Split a Content-Type header on semicolons, respecting quoted strings."""
    parts: list[str] = []
    current: list[str] = []
    in_quotes = False
    escaped = False
    for char in value:
        if escaped:
            current.append(char)
            escaped = False
            continue
        if char == "\\" and in_quotes:
            current.append(char)
            escaped = True
            continue
        if char == '"':
            in_quotes = not in_quotes
            current.append(char)
            continue
        if char == ";" and not in_quotes:
            parts.append("".join(current))
            current = []
            continue
        current.append(char)
    parts.append("".join(current))
    return parts


def _unquote(value: str) -> str:
    chars: list[str] = []
    escaped = False
    for char in value:
        if escaped:
            chars.append(char)
            escaped = False
            continue
        if char == "\\":
            escaped = True
            continue
        chars.append(char)
    if escaped:
        chars.append("\\")
    return "".join(chars)


def _validate_boundary(value: str) -> str:
    """Strip optional quotes and surrounding whitespace, then validate."""
    value = value.strip(" \t")
    if value.startswith('"'):
        if len(value) < 2 or not value.endswith('"'):
            raise DecodingError("Invalid multipart boundary")
        value = _unquote(value[1:-1])
    value = value.strip(" \t")
    if value == "" or not value.isascii() or value.startswith("=") or "\x00" in value:
        raise DecodingError("Invalid multipart boundary")
    return value


def _is_transport_padding(data: bytes) -> bool:
    return data.strip(b" \t") == b""


def _delimiter_kind(line: bytes, boundary: bytes) -> str:
    """Return ``open``, ``close``, or ``none`` for a single line."""
    prefix = b"--" + boundary
    if not line.startswith(prefix):
        return "none"
    rest = line[len(prefix) :]
    if rest.startswith(b"--") and _is_transport_padding(rest[2:]):
        return "close"
    if _is_transport_padding(rest):
        return "open"
    return "none"


def _strip_last_terminator(body: bytearray) -> None:
    if body.endswith(b"\r\n"):
        del body[-2:]
    elif body.endswith((b"\n", b"\r")):
        del body[-1:]


class _LineBuffer:
    """Incremental line splitter for LF, CRLF, and bare CR."""

    def __init__(self) -> None:
        self._buf = bytearray()

    def feed(self, data: bytes) -> list[tuple[bytes, bytes]]:
        if data:
            self._buf.extend(data)
        lines: list[tuple[bytes, bytes]] = []
        buf = self._buf
        pos = 0
        end = len(buf)
        while pos < end:
            cr = buf.find(b"\r", pos)
            lf = buf.find(b"\n", pos)
            if cr == -1 and lf == -1:
                break
            if lf != -1 and (cr == -1 or lf < cr):
                lines.append((bytes(buf[pos:lf]), b"\n"))
                pos = lf + 1
                continue
            # A trailing CR may be the start of a CRLF split across chunks.
            if cr == -1 or cr == end - 1:
                break
            if buf[cr + 1] == 0x0A:
                lines.append((bytes(buf[pos:cr]), b"\r\n"))
                pos = cr + 2
            else:
                lines.append((bytes(buf[pos:cr]), b"\r"))
                pos = cr + 1
        if pos:
            del buf[:pos]
        return lines

    def flush(self) -> tuple[bytes, bytes] | None:
        if not self._buf:
            return None
        data = bytes(self._buf)
        self._buf.clear()
        if data.endswith(b"\r"):
            return (data[:-1], b"\r")
        return (data, b"")


class _MultipartDecoder:
    def __init__(self, boundary: bytes) -> None:
        self._boundary = boundary
        self._prefix = b"--" + boundary
        self._lines = _LineBuffer()
        self._state = _PREAMBLE
        self._seen_first_line = False
        self._fields: _ParsedHeaders = []
        self._body = bytearray()

    def feed(self, data: bytes) -> list[_ParsedPart]:
        parts: list[_ParsedPart] = []
        for line, terminator in self._lines.feed(data):
            parts.extend(self._process_line(line, terminator))
        return parts

    def flush(self) -> list[_ParsedPart]:
        parts: list[_ParsedPart] = []
        last = self._lines.flush()
        if last is not None:
            line, terminator = last
            parts.extend(self._process_line(line, terminator))
        if self._state != _EPILOGUE:
            raise DecodingError("Malformed multipart framing")
        return parts

    def _process_line(self, line: bytes, terminator: bytes) -> list[_ParsedPart]:
        kind = _delimiter_kind(line, self._boundary)
        if not self._seen_first_line:
            self._seen_first_line = True
            if kind == "none" and line.startswith(self._prefix):
                raise DecodingError("Malformed multipart framing")

        if self._state == _EPILOGUE:
            return []

        if self._state != _HEADERS and kind == "open":
            part = self._finish_part() if self._state == _BODY else None
            self._state = _HEADERS
            self._fields = []
            self._body = bytearray()
            return [part] if part is not None else []

        if self._state != _HEADERS and kind == "close":
            part = self._finish_part() if self._state == _BODY else None
            self._state = _EPILOGUE
            return [part] if part is not None else []

        if self._state == _PREAMBLE:
            return []

        if self._state == _HEADERS:
            if line == b"":
                self._state = _BODY
                self._body = bytearray()
                return []
            self._consume_header_line(line)
            return []

        self._body.extend(line)
        self._body.extend(terminator)
        return []

    def _consume_header_line(self, line: bytes) -> None:
        if line.startswith((b" ", b"\t")):
            if not line.strip(b" \t") or not self._fields:
                raise DecodingError("Malformed multipart header")
            name, value = self._fields[-1]
            self._fields[-1] = (name, value + line)
            return
        if b":" not in line:
            raise DecodingError("Malformed multipart header")
        name, value = line.split(b":", 1)
        name = name.strip(b" \t")
        if not name:
            raise DecodingError("Malformed multipart header")
        self._fields.append((name, value.lstrip(b" \t")))

    def _finish_part(self) -> _ParsedPart:
        _strip_last_terminator(self._body)
        return _ParsedPart(list(self._fields), bytes(self._body))
