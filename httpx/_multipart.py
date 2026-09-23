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


def _split_header_params(value: str) -> list[str]:
    """
    Split a header value on `;`, ignoring any `;` inside quoted strings.
    """
    sections: list[str] = []
    current: list[str] = []
    in_quotes = escaped = False
    for char in value:
        if escaped:
            escaped = False
        elif in_quotes and char == "\\":
            escaped = True
        elif char == '"':
            in_quotes = not in_quotes
        elif char == ";" and not in_quotes:
            sections.append("".join(current))
            current = []
            continue
        current.append(char)
    sections.append("".join(current))
    return sections


def get_multipart_boundary_from_response_content_type(
    content_type: str | None,
) -> bytes:
    """
    Return the boundary of a `multipart/*` response Content-Type.

    Raises `DecodingError` if the content type is not multipart, or if the
    boundary is missing or invalid.
    """
    if content_type is None:
        raise DecodingError("Response has no Content-Type, expected multipart/*.")

    media_type, *params = _split_header_params(content_type)
    main_type, _, subtype = media_type.strip(" \t").lower().partition("/")
    if main_type != "multipart" or not subtype.strip(" \t"):
        raise DecodingError(
            f"Response Content-Type is not multipart/*: {content_type!r}."
        )

    boundary: str | None = None
    for param in params:
        name, _, value = param.partition("=")
        if name.strip(" \t").lower() == "boundary":
            boundary = value

    if boundary is None:
        raise DecodingError("Multipart response Content-Type has no boundary.")
    if "\r" in content_type or "\n" in content_type:
        raise DecodingError("Invalid multipart boundary.")

    boundary = boundary.strip(" \t")
    if boundary.startswith('"'):
        if len(boundary) < 2 or not boundary.endswith('"'):
            raise DecodingError("Invalid multipart boundary.")
        boundary = boundary[1:-1]
    if (
        not boundary
        or not boundary.isascii()
        or boundary.startswith("=")
        or "\x00" in boundary
    ):
        raise DecodingError("Invalid multipart boundary.")
    return boundary.encode("ascii")


_LINE_TERMINATOR_RE = re.compile(rb"\r\n|\r|\n")

MultipartPartItem = typing.Tuple[typing.List[typing.Tuple[bytes, bytes]], bytes]


class MultipartDecoder:
    """
    Incrementally parses a multipart body into `(headers, content)` items,
    where `headers` is a list of raw `(name, value)` byte pairs.

    Accepts LF, CRLF, or CR line terminators. Any preamble before the first
    delimiter line and any epilogue after the closing delimiter are ignored.
    """

    def __init__(self, boundary: bytes) -> None:
        self._dash_boundary = b"--" + boundary
        self._buffer = bytearray()
        self._pos = 0
        self._search_from = 0
        self._at_message_start = True
        self._state = "preamble"  # "preamble" | "headers" | "body" | "epilogue"
        self._headers: list[tuple[bytes, bytes]] = []
        self._body: list[bytes] = []
        self._body_terminator = b""

    def decode(self, data: bytes) -> list[MultipartPartItem]:
        if self._state == "epilogue":
            return []
        self._buffer += data
        items = self._process(final=False)
        if self._state == "epilogue":
            self._buffer.clear()
            self._pos = self._search_from = 0
        else:
            del self._buffer[: self._pos]
            self._search_from -= self._pos
            self._pos = 0
        return items

    def flush(self) -> list[MultipartPartItem]:
        items = [] if self._state == "epilogue" else self._process(final=True)
        self._buffer.clear()
        self._pos = self._search_from = 0
        if self._state != "epilogue":
            raise DecodingError("Multipart body is missing its closing boundary.")
        return items

    def _process(self, final: bool) -> list[MultipartPartItem]:
        items = []
        while self._state != "epilogue":
            line = self._next_line(final)
            if line is None:
                break
            item = self._handle_line(*line)
            if item is not None:
                items.append(item)
        return items

    def _next_line(self, final: bool) -> tuple[bytes, bytes] | None:
        buffer = self._buffer
        match = _LINE_TERMINATOR_RE.search(buffer, self._search_from)
        if match is not None:
            if match.end() == len(buffer) and match.group() == b"\r" and not final:
                # A trailing CR may be the first half of a CRLF split across chunks.
                self._search_from = match.start()
                return None
            line = bytes(buffer[self._pos : match.start()])
            self._pos = self._search_from = match.end()
            return line, match.group()
        if final and self._pos < len(buffer):
            line = bytes(buffer[self._pos :])
            self._pos = self._search_from = len(buffer)
            return line, b""
        self._search_from = len(buffer)
        return None

    def _match_delimiter(self, line: bytes) -> bool | None:
        """
        Returns `None` if `line` is not a delimiter line, otherwise whether
        it is the closing delimiter.
        """
        if not line.startswith(self._dash_boundary):
            return None
        rest = line[len(self._dash_boundary) :].rstrip(b" \t")
        if rest == b"":
            return False
        if rest == b"--":
            return True
        return None

    def _handle_line(self, line: bytes, terminator: bytes) -> MultipartPartItem | None:
        at_message_start = self._at_message_start
        self._at_message_start = False
        is_closing = self._match_delimiter(line)

        if self._state == "preamble":
            if is_closing is None:
                if at_message_start and line.startswith(self._dash_boundary):
                    raise DecodingError("Malformed multipart delimiter line.")
                return None
            self._start_part(is_closing)
            return None

        if is_closing is not None:
            item = (self._headers, b"".join(self._body))
            self._start_part(is_closing)
            return item

        if self._state == "headers":
            if line:
                self._parse_header_line(line)
            else:
                self._state = "body"
            return None

        self._body += [self._body_terminator, line]
        self._body_terminator = terminator
        return None

    def _start_part(self, is_closing: bool) -> None:
        self._state = "epilogue" if is_closing else "headers"
        self._headers = []
        self._body = []
        self._body_terminator = b""

    def _parse_header_line(self, line: bytes) -> None:
        if line[:1] in (b" ", b"\t"):
            if not self._headers:
                raise DecodingError(
                    "Malformed multipart header: unexpected leading whitespace."
                )
            continuation = line.strip(b" \t")
            if not continuation:
                raise DecodingError("Malformed multipart header: empty continuation.")
            name, value = self._headers[-1]
            value = value + b" " + continuation if value else continuation
            self._headers[-1] = (name, value)
            return

        name, sep, value = line.partition(b":")
        name = name.rstrip(b" \t")
        if not sep or not name:
            raise DecodingError(f"Malformed multipart header line: {line!r}.")
        self._headers.append((name, value.strip(b" \t")))


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
