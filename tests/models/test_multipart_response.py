"""Tests for parsing multipart HTTP response bodies."""

from __future__ import annotations

import gzip
import typing

import pytest

import httpx


def _body(
    parts: list[tuple[list[tuple[str, str]], bytes]],
    *,
    boundary: str = "b",
    newline: bytes = b"\r\n",
    preamble: bytes = b"",
    epilogue: bytes = b"",
    close: bool = True,
) -> bytes:
    """Build a multipart body. Part content excludes the delimiter's line ending."""
    marker = boundary.encode("ascii")
    chunks: list[bytes] = []
    if preamble:
        chunks.append(preamble)
    for headers, content in parts:
        chunks.append(b"--" + marker + newline)
        for name, value in headers:
            chunks.append(
                name.encode("ascii") + b": " + value.encode("ascii") + newline
            )
        chunks.append(newline + content + newline)
    if close:
        chunks.append(b"--" + marker + b"--" + newline)
    if epilogue:
        chunks.append(epilogue)
    return b"".join(chunks)


def _response(
    body: bytes,
    content_type: str | None = "multipart/mixed; boundary=b",
    *,
    content: typing.Iterable[bytes] | bytes | None = None,
    request: httpx.Request | None = None,
) -> httpx.Response:
    headers = {} if content_type is None else {"Content-Type": content_type}
    return httpx.Response(
        200,
        headers=headers,
        content=body if content is None else content,
        request=request,
    )


def _one_byte_chunks(body: bytes) -> typing.Iterator[bytes]:
    for index in range(len(body)):
        yield body[index : index + 1]


def _split_every_crlf(body: bytes) -> typing.Iterator[bytes]:
    start = 0
    index = 0
    while index < len(body) - 1:
        if body[index : index + 2] == b"\r\n":
            yield body[start : index + 1]
            start = index + 1
            index += 2
        else:
            index += 1
    if start <= len(body):
        yield body[start:]


def test_parses_parts_headers_and_body() -> None:
    body = _body(
        [
            (
                [("Content-Type", "text/plain"), ("X-A", "1"), ("X-A", "2")],
                b"one",
            ),
            ([("Content-Type", "application/octet-stream")], b"two"),
        ]
    )
    response = _response(body)
    parts = list(response.iter_multipart())

    assert parts == list(response.iter_multipart())
    assert isinstance(parts[0], httpx.MultipartPart)
    assert isinstance(parts[0].headers, httpx.Headers)
    assert isinstance(parts[0].content, bytes)
    assert parts[0].content == b"one"
    assert parts[1].content == b"two"
    assert parts[0].headers.raw == [
        (b"Content-Type", b"text/plain"),
        (b"X-A", b"1"),
        (b"X-A", b"2"),
    ]
    assert parts[0].headers.multi_items() == [
        ("content-type", "text/plain"),
        ("x-a", "1"),
        ("x-a", "2"),
    ]
    assert parts[0].headers["x-a"] == "1, 2"
    assert parts[1].headers["content-type"] == "application/octet-stream"
    assert response.is_closed


def test_empty_headers_and_empty_part_body() -> None:
    body = _body([([], b""), ([], b"x")])
    parts = list(_response(body).iter_multipart())
    assert [part.content for part in parts] == [b"", b"x"]
    assert list(parts[0].headers.keys()) == []


def test_ignores_preamble_and_epilogue() -> None:
    body = _body(
        [([("Content-Type", "text/plain")], b"keep")],
        preamble=b"ignore --bXYZ\r\n",
        epilogue=b"--b\r\n\r\nnope\r\n--b--\r\n",
    )
    parts = list(_response(body).iter_multipart())
    assert len(parts) == 1
    assert parts[0].content == b"keep"


def test_only_closing_boundary_yields_no_parts() -> None:
    assert list(_response(b"--b--\r\n").iter_multipart()) == []
    assert list(_response(b"--b--").iter_multipart()) == []
    assert list(_response(b"preamble\r\n--b--\r\nepilogue").iter_multipart()) == []
    assert list(_response(b"--b-- \t\r\n").iter_multipart()) == []


def test_line_endings_and_excluded_delimiter_terminator() -> None:
    content = b"a\r\nb\nc\rd"
    for newline in (b"\r\n", b"\n", b"\r"):
        body = _body([([], content)], newline=newline)
        parts = list(_response(body).iter_multipart())
        assert parts[0].content == content


def test_blank_body_line_before_delimiter_is_excluded() -> None:
    # The empty line's terminator is the one that precedes the delimiter.
    body = b"--b\r\n\r\n\r\n--b--\r\n"
    assert list(_response(body).iter_multipart())[0].content == b""

    # An earlier empty line is real body content.
    body = b"--b\r\n\r\n\r\n\r\n--b--\r\n"
    assert list(_response(body).iter_multipart())[0].content == b"\r\n"


def test_boundary_like_text_inside_a_part_is_content() -> None:
    # Lines that begin with the boundary but are not exact delimiters stay
    # in the body. An exact `--b--` line would close the part.
    content = b"hello --b\r\n--b extra\r\n--bXYZ\r\n--b---\r\n--b--x"
    body = _body([([], content)])
    assert list(_response(body).iter_multipart())[0].content == content


def test_delimiter_allows_trailing_whitespace() -> None:
    body = b"--b \r\n\r\nhello\r\n--b--\t\r\n"
    assert list(_response(body).iter_multipart())[0].content == b"hello"


def test_dash_in_boundary_is_not_confused_with_closing_delimiter() -> None:
    boundary = "abc-"
    content = b"--abc--\r\ntext"
    body = _body([([], content)], boundary=boundary)
    response = _response(body, f"multipart/mixed; boundary={boundary}")
    assert list(response.iter_multipart())[0].content == content


def test_message_starting_with_non_delimiter_boundary_prefix_errors() -> None:
    body = b"--bXYZ\r\n--b\r\n\r\nkeep\r\n--b--\r\n"
    with pytest.raises(httpx.DecodingError):
        list(_response(body).iter_multipart())

    body = b"--b extra\r\n--b\r\n\r\nkeep\r\n--b--\r\n"
    with pytest.raises(httpx.DecodingError):
        list(_response(body).iter_multipart())


def test_header_continuation_and_duplicates() -> None:
    body = (
        b"--b\r\n"
        b"Content-Type: text/plain;\r\n"
        b" charset=utf-8\r\n"
        b"X-Fold: hello\r\n"
        b"\t\tworld\r\n"
        b" more\r\n"
        b"\r\n"
        b"z\r\n"
        b"--b--\r\n"
    )
    part = list(_response(body).iter_multipart())[0]
    assert part.headers.raw == [
        (b"Content-Type", b"text/plain; charset=utf-8"),
        (b"X-Fold", b"hello\t\tworld more"),
    ]
    assert part.headers.multi_items() == [
        ("content-type", "text/plain; charset=utf-8"),
        ("x-fold", "hello\t\tworld more"),
    ]
    assert part.content == b"z"


@pytest.mark.parametrize(
    "headers",
    [
        b"Not-A-Header\r\n",
        b": missing-name\r\n",
        b" Content-Type: text/plain\r\n",
        b"Content-Type: text/plain\r\n   \r\n",
        b"Content-Type: text/plain\r\n\t\r\n",
    ],
)
def test_malformed_headers(headers: bytes) -> None:
    body = b"--b\r\n" + headers + b"\r\nbody\r\n--b--\r\n"
    with pytest.raises(httpx.DecodingError):
        list(_response(body).iter_multipart())


@pytest.mark.parametrize(
    "content_type",
    [
        "text/plain",
        "text/plain; boundary=b",
        "multipart",
        "multipart/",
        "multipart/ ; boundary=b",
        "MULTIPART/",
        "multipart/mixed",
        "multipart/mixed; charset=utf-8",
        "multipart/mixed; boundary",
        "multipart/mixed; boundary=",
        'multipart/mixed; boundary=""',
        'multipart/mixed; boundary="   "',
        "multipart/mixed; boundary==b",
        'multipart/mixed; boundary="=b"',
        "multipart/mixed; boundary=b\x00b",
        "multipart/mixed; boundary=b\r\nX-Injected: no",
        "multipart/mixed; boundary=b\n",
        "multipart/mixed; boundary=b\rrest",
        'multipart/mixed; boundary="b',
    ],
)
def test_invalid_content_type_raises(content_type: str) -> None:
    body = _body([([], b"x")])
    with pytest.raises(httpx.DecodingError):
        list(_response(body, content_type).iter_multipart())


def test_non_ascii_boundary_raises() -> None:
    # Header values are stored as bytes; a non-ASCII boundary is rejected
    # once the Content-Type is decoded.
    response = httpx.Response(
        200,
        headers=[(b"Content-Type", "multipart/mixed; boundary=bé".encode())],
        content=_body([([], b"x")]),
    )
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())


def test_missing_content_type_raises() -> None:
    with pytest.raises(httpx.DecodingError):
        list(_response(_body([([], b"x")]), content_type=None).iter_multipart())


@pytest.mark.parametrize(
    "content_type",
    [
        "MULTIPART/FORM-DATA; BOUNDARY=b",
        "multipart/related; boundary=b",
        'multipart/mixed; boundary="b"',
        'multipart/mixed; boundary = "b"',
        'multipart/mixed;\tboundary\t=\t"b"\t',
        'multipart/mixed; boundary=" b "',
        "multipart/mixed; boundary= b ",
        "multipart/mixed; charset=utf-8; boundary=b",
        'multipart/mixed; boundary=b; charset="utf-8"',
        'multipart/mixed; charset="a;b"; boundary=b',
        "multipart/mixed; boundary=aaa; boundary=b",
        'multipart/mixed; boundary="aaa"; boundary=b',
        "multipart/mixed; boundary=; boundary=b",
        "multipart/mixed; boundary=foo=bar",
        'multipart/mixed; boundary="foo bar"',
        "multipart/mixed; boundary=foo bar",
    ],
)
def test_accepts_content_type_boundary_variants(content_type: str) -> None:
    if "foo=bar" in content_type and "boundary=foo=bar" in content_type:
        boundary = "foo=bar"
    elif 'boundary="foo bar"' in content_type or "boundary=foo bar" in content_type:
        boundary = "foo bar"
    else:
        boundary = "b"
    body = _body([([], b"ok")], boundary=boundary)
    parts = list(_response(body, content_type).iter_multipart())
    assert parts[0].content == b"ok"


def test_last_invalid_boundary_wins() -> None:
    body = _body([([], b"ok")])
    content_type = "multipart/mixed; boundary=b; boundary="
    with pytest.raises(httpx.DecodingError):
        list(_response(body, content_type).iter_multipart())


def test_quoted_boundary_may_contain_semicolon() -> None:
    body = _body([([], b"ok")], boundary="bb;b")
    content_type = 'multipart/mixed; boundary="bb;b"'
    assert list(_response(body, content_type).iter_multipart())[0].content == b"ok"


@pytest.mark.parametrize(
    "body_extra",
    [b"", b"no-delimiter", b"--b\r\n\r\nunterminated"],
)
def test_malformed_framing(body_extra: bytes) -> None:
    with pytest.raises(httpx.DecodingError):
        list(_response(body_extra).iter_multipart())
    # In-memory parsing is repeatable, including the error.
    with pytest.raises(httpx.DecodingError):
        list(_response(body_extra).iter_multipart())


def test_missing_closing_boundary_is_malformed() -> None:
    body = _body([([], b"orphan")], close=False)
    with pytest.raises(httpx.DecodingError):
        list(_response(body).iter_multipart())


def test_delimiter_in_header_block_is_malformed() -> None:
    body = b"--b\r\n--b--\r\n"
    with pytest.raises(httpx.DecodingError):
        list(_response(body).iter_multipart())


def test_in_memory_iteration_is_repeatable_after_read() -> None:
    payload = _body([([], b"memo")])

    def chunks() -> typing.Iterator[bytes]:
        yield payload

    response = _response(b"", content=chunks())
    assert response.read() == payload
    assert response.is_stream_consumed
    first = list(response.iter_multipart())
    second = list(response.iter_multipart())
    assert first == second
    assert first[0].content == b"memo"


def test_streaming_iteration_consumes_and_closes() -> None:
    payload = _body(
        [([("Content-Type", "text/plain")], b"one"), ([], b"two")],
        epilogue=b"tail",
    )

    def chunks() -> typing.Iterator[bytes]:
        yield payload[:5]
        yield payload[5:]

    response = _response(b"", content=chunks())
    assert not response.is_closed
    parts = list(response.iter_multipart())
    assert [part.content for part in parts] == [b"one", b"two"]
    assert response.is_closed
    assert response.is_stream_consumed
    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_multipart())


def test_streaming_one_byte_chunks_and_split_crlf() -> None:
    payload = _body([([("Content-Type", "text/plain")], b"abc\r\ndef")])
    response = _response(b"", content=_one_byte_chunks(payload))
    assert list(response.iter_multipart())[0].content == b"abc\r\ndef"

    response = _response(b"", content=_split_every_crlf(payload))
    assert list(response.iter_multipart())[0].content == b"abc\r\ndef"


def test_partial_streaming_iteration_closes_response() -> None:
    payload = _body([([], b"one"), ([], b"two")])
    response = _response(b"", content=iter([payload]))
    iterator = response.iter_multipart()
    assert next(iterator).content == b"one"
    iterator.close()
    assert response.is_closed
    assert response.is_stream_consumed
    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_multipart())


def test_invalid_content_type_does_not_consume_stream() -> None:
    consumed = False

    def chunks() -> typing.Iterator[bytes]:
        nonlocal consumed
        consumed = True
        yield b"x"

    response = _response(b"", content_type="text/plain", content=chunks())
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())
    assert consumed is False
    assert response.is_stream_consumed is False
    assert response.is_closed is False


def test_streaming_framing_error_then_stream_consumed() -> None:
    response = _response(b"", content=iter([b"not-a-multipart-body"]))
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())
    assert response.is_stream_consumed
    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_multipart())


def test_closed_stream_raises_stream_closed() -> None:
    response = _response(b"", content=iter([_body([([], b"x")])]))
    response.close()
    with pytest.raises(httpx.StreamClosed):
        list(response.iter_multipart())


def test_previously_iterated_raw_stream_is_consumed() -> None:
    response = _response(b"", content=iter([_body([([], b"x")])]))
    list(response.iter_bytes())
    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_multipart())


def test_decoding_error_includes_request() -> None:
    request = httpx.Request("GET", "https://example.org/")
    response = _response(b"nope", request=request)
    with pytest.raises(httpx.DecodingError) as exc_info:
        list(response.iter_multipart())
    assert exc_info.value.request is request


def test_gzip_content_encoding_is_decoded() -> None:
    payload = _body([([], b"plain")])
    compressed = gzip.compress(payload)
    headers = {
        "Content-Type": "multipart/mixed; boundary=b",
        "Content-Encoding": "gzip",
    }
    response = httpx.Response(200, headers=headers, content=compressed)
    assert list(response.iter_multipart())[0].content == b"plain"

    response = httpx.Response(200, headers=headers, content=iter([compressed]))
    assert list(response.iter_multipart())[0].content == b"plain"


def test_multipart_part_is_exported() -> None:
    assert httpx.MultipartPart.__module__ == "httpx"
    part = httpx.MultipartPart([("A", "1")], b"xyz")
    assert part.headers["a"] == "1"
    assert part.content == b"xyz"


@pytest.mark.anyio
async def test_aiter_multipart_repeatable_in_memory() -> None:
    body = _body([([("Content-Type", "text/plain")], b"async")])
    response = _response(body)
    first = [part async for part in response.aiter_multipart()]
    second = [part async for part in response.aiter_multipart()]
    assert first == second
    assert first[0].content == b"async"
    assert first[0].headers["content-type"] == "text/plain"


@pytest.mark.anyio
async def test_aiter_multipart_streaming_consumes_stream() -> None:
    payload = _body([([], b"left"), ([], b"right")])

    async def chunks() -> typing.AsyncIterator[bytes]:
        yield payload[:7]
        yield payload[7:]

    response = _response(b"", content=chunks())
    parts = [part async for part in response.aiter_multipart()]
    assert [part.content for part in parts] == [b"left", b"right"]
    assert response.is_closed
    assert response.is_stream_consumed
    with pytest.raises(httpx.StreamConsumed):
        [part async for part in response.aiter_multipart()]


@pytest.mark.anyio
async def test_aiter_multipart_one_byte_chunks() -> None:
    payload = _body([([], b"z")])

    async def chunks() -> typing.AsyncIterator[bytes]:
        for index in range(len(payload)):
            yield payload[index : index + 1]

    response = _response(b"", content=chunks())
    parts = [part async for part in response.aiter_multipart()]
    assert parts[0].content == b"z"


@pytest.mark.anyio
async def test_aiter_after_aread_is_repeatable() -> None:
    payload = _body([([], b"cached")])

    async def chunks() -> typing.AsyncIterator[bytes]:
        yield payload

    response = _response(b"", content=chunks())
    assert await response.aread() == payload
    first = [part async for part in response.aiter_multipart()]
    second = [part async for part in response.aiter_multipart()]
    assert first == second
    assert first[0].content == b"cached"
