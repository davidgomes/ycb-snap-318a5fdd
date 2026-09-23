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


class MultipartPart:
    """
    A single part from a ``multipart/*`` response body.
    """

    def __init__(self, headers: typing.Any, content: bytes) -> None:
        self.headers = headers
        self.content = content

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, MultipartPart):
            return NotImplemented
        return self.headers == other.headers and self.content == other.content

    def __repr__(self) -> str:
        return f"MultipartPart(headers={self.headers!r}, content={self.content!r})"


def parse_multipart_boundary(content_type: str | None) -> bytes:
    """
    Return the multipart boundary from a Content-Type header.

    Raises ``DecodingError`` when the response is not ``multipart/*`` or the
    boundary parameter is missing or invalid.
    """
    if content_type is None or "\r" in content_type or "\n" in content_type:
        raise DecodingError("Invalid multipart Content-Type")

    media_type, params = _split_content_type(content_type)
    if "/" not in media_type:
        raise DecodingError("Invalid multipart Content-Type")
    type_name, _, subtype = media_type.partition("/")
    if type_name.lower() != "multipart" or subtype == "":
        raise DecodingError("Invalid multipart Content-Type")

    boundary: str | None = None
    for name, value in params:
        if name.lower() == "boundary":
            boundary = value

    if (
        boundary is None
        or boundary == ""
        or boundary.startswith("=")
        or "\x00" in boundary
    ):
        raise DecodingError("Invalid multipart boundary")
    try:
        encoded = boundary.encode("ascii")
    except UnicodeEncodeError:
        raise DecodingError("Invalid multipart boundary") from None
    if any(byte > 127 for byte in encoded):
        raise DecodingError("Invalid multipart boundary")
    return encoded


def _split_content_type(content_type: str) -> tuple[str, list[tuple[str, str]]]:
    length = len(content_type)
    index = 0

    def skip_ws(pos: int) -> int:
        while pos < length and content_type[pos] in " \t":
            pos += 1
        return pos

    index = skip_ws(index)
    start = index
    while index < length and content_type[index] not in " \t;":
        index += 1
    media_type = content_type[start:index]
    index = skip_ws(index)

    params: list[tuple[str, str]] = []
    while index < length:
        if content_type[index] != ";":
            raise DecodingError("Invalid multipart Content-Type")
        index = skip_ws(index + 1)
        if index >= length:
            break

        start = index
        while index < length and content_type[index] not in " \t=;":
            index += 1
        name = content_type[start:index]
        if name == "":
            raise DecodingError("Invalid multipart Content-Type")
        index = skip_ws(index)
        if index >= length or content_type[index] != "=":
            raise DecodingError("Invalid multipart Content-Type")
        index = skip_ws(index + 1)
        if index >= length:
            raise DecodingError("Invalid multipart Content-Type")

        if content_type[index] == '"':
            index += 1
            start = index
            while index < length and content_type[index] != '"':
                index += 1
            if index >= length:
                raise DecodingError("Invalid multipart boundary")
            value = content_type[start:index]
            index = skip_ws(index + 1)
            if index < length and content_type[index] != ";":
                raise DecodingError("Invalid multipart boundary")
        else:
            start = index
            while index < length and content_type[index] != ";":
                index += 1
            value = content_type[start:index].rstrip(" \t")

        params.append((name, value))

    return media_type, params


class _MultipartLineReader:
    """
    Split a byte stream into lines terminated by LF, CRLF, or a bare CR.

    A CR at the end of a chunk is held until the next chunk so a CRLF pair
    split across chunks stays one terminator.
    """

    def __init__(self) -> None:
        self._buffer = bytearray()

    def feed(self, data: bytes) -> list[tuple[bytes, bytes]]:
        self._buffer.extend(data)
        lines: list[tuple[bytes, bytes]] = []
        while True:
            buffer = self._buffer
            cr = buffer.find(0x0D)
            lf = buffer.find(0x0A)
            if cr == -1 and lf == -1:
                return lines
            if lf != -1 and (cr == -1 or lf < cr):
                lines.append((bytes(buffer[:lf]), b"\n"))
                del buffer[: lf + 1]
                continue
            assert cr != -1
            if cr + 1 == len(buffer):
                return lines
            if buffer[cr + 1] == 0x0A:
                lines.append((bytes(buffer[:cr]), b"\r\n"))
                del buffer[: cr + 2]
            else:
                lines.append((bytes(buffer[:cr]), b"\r"))
                del buffer[: cr + 1]

    def flush(self) -> tuple[bytes, bytes] | None:
        if not self._buffer:
            return None
        if self._buffer[-1] == 0x0D:
            line = bytes(self._buffer[:-1])
            self._buffer.clear()
            return line, b"\r"
        line = bytes(self._buffer)
        self._buffer.clear()
        return line, b""


class MultipartDecoder:
    """
    Incremental parser for a ``multipart/*`` body.
    """

    def __init__(self, boundary: bytes) -> None:
        self._dash_boundary = b"--" + boundary
        self._close_boundary = b"--" + boundary + b"--"
        self._lines = _MultipartLineReader()
        self._state = "preamble"
        self._saw_line = False
        self._header_fields: list[tuple[bytes, bytes]] = []
        self._body = bytearray()
        self._pending_terminator: bytes | None = None

    @classmethod
    def from_content_type(cls, content_type: str | None) -> MultipartDecoder:
        return cls(parse_multipart_boundary(content_type))

    def feed(self, data: bytes) -> list[MultipartPart]:
        parts: list[MultipartPart] = []
        for line, terminator in self._lines.feed(data):
            parts.extend(self._on_line(line, terminator))
        return parts

    def finish(self) -> list[MultipartPart]:
        parts: list[MultipartPart] = []
        last = self._lines.flush()
        if last is not None:
            parts.extend(self._on_line(*last))
        if self._state != "epilogue":
            raise DecodingError("Malformed multipart body")
        return parts

    def _delimiter_kind(self, line: bytes) -> str | None:
        if line.startswith(self._close_boundary):
            rest = line[len(self._close_boundary) :]
            if all(byte in b" \t" for byte in rest):
                return "close"
        if line.startswith(self._dash_boundary):
            rest = line[len(self._dash_boundary) :]
            if all(byte in b" \t" for byte in rest):
                return "open"
        return None

    def _on_line(self, line: bytes, terminator: bytes) -> list[MultipartPart]:
        if self._state == "epilogue":
            return []

        if not self._saw_line:
            self._saw_line = True
            if (
                line.startswith(self._dash_boundary)
                and self._delimiter_kind(line) is None
            ):
                raise DecodingError("Malformed multipart body")

        kind = self._delimiter_kind(line)

        if self._state == "preamble":
            if kind == "open":
                self._state = "headers"
                self._header_fields = []
            elif kind == "close":
                self._state = "epilogue"
            return []

        if self._state == "headers":
            self._consume_header_line(line)
            return []

        if kind is not None:
            part = self._build_part()
            if kind == "close":
                self._state = "epilogue"
            else:
                self._state = "headers"
                self._header_fields = []
            return [part]

        if self._pending_terminator is not None:
            self._body.extend(self._pending_terminator)
        self._body.extend(line)
        self._pending_terminator = terminator if terminator else None
        return []

    def _consume_header_line(self, line: bytes) -> None:
        if line == b"":
            self._state = "body"
            self._body = bytearray()
            self._pending_terminator = None
            return

        if line[:1] in (b" ", b"\t"):
            if not self._header_fields or line.strip(b" \t") == b"":
                raise DecodingError("Malformed multipart headers")
            name, value = self._header_fields[-1]
            self._header_fields[-1] = (name, value + line)
            return

        if b":" not in line:
            raise DecodingError("Malformed multipart headers")
        name, value = line.split(b":", 1)
        name = name.strip(b" \t")
        if name == b"":
            raise DecodingError("Malformed multipart headers")
        self._header_fields.append((name, value.lstrip(b" \t")))

    def _build_part(self) -> MultipartPart:
        from ._models import Headers

        return MultipartPart(
            headers=Headers(self._header_fields),
            content=bytes(self._body),
        )
