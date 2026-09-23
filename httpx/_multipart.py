from __future__ import annotations

import enum
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


_WSP = " \t"
_WSP_BYTES = b" \t"


class _RawMultipartPart(typing.NamedTuple):
    headers: list[tuple[bytes, bytes]]
    content: bytes


class _MultipartState(enum.Enum):
    PREAMBLE = enum.auto()
    HEADERS = enum.auto()
    BODY = enum.auto()
    EPILOGUE = enum.auto()


def _parse_multipart_response_boundary(content_type: str | None) -> bytes:
    """
    Return the multipart boundary from a Content-Type header.

    Raises `DecodingError` when the response is not `multipart/*` or the
    boundary parameter is missing or invalid.
    """
    if content_type is None:
        raise DecodingError("Response is not multipart")
    # Any CR or LF in the header makes the boundary invalid, including
    # values that would otherwise parse.
    if "\r" in content_type or "\n" in content_type:
        raise DecodingError("Invalid multipart boundary")

    media, params = _split_content_type(content_type)
    type_token, sep, subtype = media.strip(_WSP).partition("/")
    if sep != "/" or type_token.strip(_WSP).lower() != "multipart":
        raise DecodingError("Response is not multipart")
    if subtype.strip(_WSP) == "":
        raise DecodingError("Empty multipart subtype")

    boundary: str | None = None
    saw_boundary = False
    for name, raw_value in params:
        if name.strip(_WSP).lower() != "boundary":
            continue
        saw_boundary = True
        boundary = _normalize_boundary(raw_value)

    if not saw_boundary or boundary is None:
        raise DecodingError("Invalid multipart boundary")
    return boundary.encode("ascii")


def _split_content_type(header: str) -> tuple[str, list[tuple[str, str]]]:
    """
    Split a Content-Type value into the media type and parameters.

    Semicolons inside double quotes do not separate parameters. When several
    parameters share a name, callers keep the last one.
    """
    sections: list[str] = []
    buf: list[str] = []
    in_quotes = False
    for char in header:
        if char == '"':
            in_quotes = not in_quotes
            buf.append(char)
        elif char == ";" and not in_quotes:
            sections.append("".join(buf))
            buf = []
        else:
            buf.append(char)
    sections.append("".join(buf))

    params: list[tuple[str, str]] = []
    for section in sections[1:]:
        if "=" not in section:
            name = section.strip(_WSP)
            if name:
                params.append((name, ""))
            continue
        name, value = section.split("=", 1)
        params.append((name, value))
    return sections[0], params


def _normalize_boundary(raw: str) -> str | None:
    """
    Strip surrounding whitespace and one optional pair of quotes.

    Returns None when the boundary is empty, non-ASCII, starts with '=',
    or contains a NUL.
    """
    value = raw.strip(_WSP)
    if value.startswith('"'):
        if len(value) < 2 or not value.endswith('"'):
            return None
        value = value[1:-1].strip(_WSP)
    if value == "" or "\x00" in value or value.startswith("=") or not value.isascii():
        return None
    return value


def _delimiter_kind(line: bytes, boundary: bytes) -> str | None:
    """
    Return 'open', 'close', or None.

    A delimiter line is exactly `--boundary` or `--boundary--` with optional
    trailing SP/HTAB. The closing form is checked first so a boundary that
    itself contains dashes is not misread.
    """
    prefix = b"--" + boundary
    if not line.startswith(prefix):
        return None
    rest = line[len(prefix) :]
    if rest.startswith(b"--"):
        padding = rest[2:]
        if padding.strip(_WSP_BYTES) == b"":
            return "close"
        return None
    if rest.strip(_WSP_BYTES) == b"":
        return "open"
    return None


class _MultipartParser:
    """
    Incremental parser for a `multipart/*` body.

    Preamble and epilogue are ignored. Line endings may be LF, CRLF, or CR,
    including a CRLF pair split across chunks. Each part body excludes the
    line terminator that precedes the next delimiter.
    """

    def __init__(self, boundary: bytes) -> None:
        self._boundary = boundary
        self._buffer = bytearray()
        self._pos = 0
        self._state = _MultipartState.PREAMBLE
        self._seen_line = False
        self._headers: list[tuple[bytes, bytes]] = []
        self._body = bytearray()
        self._pending_line: bytes | None = None
        self._pending_term = b""

    def feed(self, data: bytes) -> list[_RawMultipartPart]:
        if self._state is _MultipartState.EPILOGUE or not data:
            return []
        self._buffer.extend(data)
        return self._drain(final=False)

    def flush(self) -> list[_RawMultipartPart]:
        parts = self._drain(final=True)
        if self._state is not _MultipartState.EPILOGUE:
            raise DecodingError("Malformed multipart framing")
        return parts

    def _drain(self, final: bool) -> list[_RawMultipartPart]:
        parts: list[_RawMultipartPart] = []
        while True:
            taken = self._take_line(final)
            if taken is None:
                break
            line, term = taken
            part = self._on_line(line, term)
            if part is not None:
                parts.append(part)
            if self._state is _MultipartState.EPILOGUE:
                self._discard()
                break
        return parts

    def _take_line(self, final: bool) -> tuple[bytes, bytes] | None:
        buf = self._buffer
        start = self._pos
        end = len(buf)
        index = start
        while index < end:
            byte = buf[index]
            if byte == 0x0A:  # LF
                line = bytes(buf[start:index])
                self._pos = index + 1
                self._compact()
                return line, b"\n"
            if byte == 0x0D:  # CR, or CRLF when the next byte is LF
                if index + 1 < end:
                    if buf[index + 1] == 0x0A:
                        line = bytes(buf[start:index])
                        self._pos = index + 2
                        self._compact()
                        return line, b"\r\n"
                    line = bytes(buf[start:index])
                    self._pos = index + 1
                    self._compact()
                    return line, b"\r"
                if final:
                    line = bytes(buf[start:index])
                    self._pos = index + 1
                    self._compact()
                    return line, b"\r"
                # Hold a trailing CR so a following LF can complete a CRLF.
                return None
            index += 1
        if final and start < end:
            line = bytes(buf[start:end])
            self._pos = end
            self._compact()
            return line, b""
        return None

    def _compact(self) -> None:
        if self._pos >= 65536 and self._pos > len(self._buffer) // 2:
            del self._buffer[: self._pos]
            self._pos = 0
        elif self._pos == len(self._buffer) and self._pos:
            self._buffer.clear()
            self._pos = 0

    def _discard(self) -> None:
        self._buffer.clear()
        self._pos = 0

    def _on_line(self, line: bytes, term: bytes) -> _RawMultipartPart | None:
        if not self._seen_line:
            self._seen_line = True
            # A message that starts with a boundary-like line which is not an
            # exact delimiter is malformed. The same text later in the body
            # is ordinary content.
            if _delimiter_kind(line, self._boundary) is None and line.startswith(
                b"--" + self._boundary
            ):
                raise DecodingError("Malformed multipart framing")

        if self._state is _MultipartState.PREAMBLE:
            kind = _delimiter_kind(line, self._boundary)
            if kind == "open":
                self._state = _MultipartState.HEADERS
                self._headers = []
            elif kind == "close":
                self._state = _MultipartState.EPILOGUE
            return None

        if self._state is _MultipartState.HEADERS:
            if line == b"":
                self._state = _MultipartState.BODY
                self._body = bytearray()
                self._pending_line = None
                self._pending_term = b""
                return None
            self._push_header(line)
            return None

        if self._state is _MultipartState.BODY:
            kind = _delimiter_kind(line, self._boundary)
            if kind is not None:
                return self._finish_part(kind)
            if self._pending_line is not None:
                self._body.extend(self._pending_line)
                self._body.extend(self._pending_term)
            self._pending_line = line
            self._pending_term = term
            return None

        return None

    def _push_header(self, line: bytes) -> None:
        if line[:1] in (b" ", b"\t"):
            if not self._headers:
                raise DecodingError("Malformed multipart headers")
            if line.strip(_WSP_BYTES) == b"":
                raise DecodingError("Malformed multipart headers")
            name, value = self._headers[-1]
            self._headers[-1] = (name, value + line)
            return
        if b":" not in line:
            raise DecodingError("Malformed multipart headers")
        name, value = line.split(b":", 1)
        name = name.strip(_WSP_BYTES)
        if not name:
            raise DecodingError("Malformed multipart headers")
        self._headers.append((name, value.lstrip(_WSP_BYTES)))

    def _finish_part(self, kind: str) -> _RawMultipartPart:
        if self._pending_line is not None:
            self._body.extend(self._pending_line)
        part = _RawMultipartPart(list(self._headers), bytes(self._body))
        self._headers = []
        self._body = bytearray()
        self._pending_line = None
        self._pending_term = b""
        if kind == "open":
            self._state = _MultipartState.HEADERS
        else:
            self._state = _MultipartState.EPILOGUE
        return part
